// Package identity đọc Profile/Space/thành viên Space từ HipCore
// (client-credentials) và cache hai lớp: Redis (nóng) + bảng *_cache (bền vững).
// Phục vụ hiển thị tác giả, phân quyền và phân giải mention. Xem docs/05.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"ludiskus/internal/config"
	"ludiskus/internal/domain"
)

// Store là phần repository mà identity cần (tránh import vòng).
type Store interface {
	GetCachedProfile(ctx context.Context, uuid string) (*domain.CachedProfile, error)
	GetCachedProfileByCode(ctx context.Context, code string) (*domain.CachedProfile, error)
	UpsertCachedProfile(ctx context.Context, p domain.CachedProfile) error
	GetCachedSpace(ctx context.Context, uuid string) (*domain.CachedSpace, error)
	UpsertCachedSpace(ctx context.Context, s domain.CachedSpace) error
	ListMembers(ctx context.Context, spaceUUID string) ([]domain.CachedMember, error)
	ReplaceMembers(ctx context.Context, spaceUUID string, members []domain.CachedMember) error
}

type Service struct {
	store  Store
	redis  *redis.Client
	cfg    *config.Config
	log    *slog.Logger
	client *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func New(store Store, rdb *redis.Client, cfg *config.Config, log *slog.Logger) *Service {
	return &Service{
		store:  store,
		redis:  rdb,
		cfg:    cfg,
		log:    log,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (s *Service) Enabled() bool {
	return s.cfg.HipcoreClientID != "" && s.cfg.HipcoreClientSecret != ""
}

func profileKey(uuid string) string { return "ludiskus:profile:" + uuid }
func spaceKey(uuid string) string   { return "ludiskus:space:" + uuid }
func membersKey(uuid string) string { return "ludiskus:members:" + uuid }

// --- Profile ----------------------------------------------------------------

// Profile trả Profile theo uuid: Redis → profile_cache → HipCore (lazy fill).
func (s *Service) Profile(ctx context.Context, uuid string) (*domain.CachedProfile, error) {
	if p := getJSON[domain.CachedProfile](ctx, s.redis, profileKey(uuid)); p != nil {
		return p, nil
	}
	if p, err := s.store.GetCachedProfile(ctx, uuid); err == nil {
		if time.Since(p.SyncedAt) < s.cfg.CacheTTL {
			s.setJSON(ctx, profileKey(uuid), p)
			return p, nil
		}
	}
	p, err := s.fetchProfile(ctx, "/api/profiles/"+url.PathEscape(uuid))
	if err != nil {
		if cached, e := s.store.GetCachedProfile(ctx, uuid); e == nil {
			return cached, nil
		}
		return nil, err
	}
	s.persistProfile(ctx, p)
	return p, nil
}

// ProfileByCode dùng cho phân giải @mention — chỉ tra cache cục bộ (full-sync
// đã nạp). Miss → not found (không gọi HipCore vì không có endpoint by-code).
func (s *Service) ProfileByCode(ctx context.Context, code string) (*domain.CachedProfile, error) {
	return s.store.GetCachedProfileByCode(ctx, code)
}

// ProfileMap nạp nhiều Profile (cho danh sách topic/post). Lỗi từng cái bỏ qua.
func (s *Service) ProfileMap(ctx context.Context, uuids []string) map[string]*domain.CachedProfile {
	out := map[string]*domain.CachedProfile{}
	for _, u := range uuids {
		if u == "" || out[u] != nil {
			continue
		}
		if p, err := s.Profile(ctx, u); err == nil {
			out[u] = p
		}
	}
	return out
}

func (s *Service) persistProfile(ctx context.Context, p *domain.CachedProfile) {
	if err := s.store.UpsertCachedProfile(ctx, *p); err != nil {
		s.log.Error("upsert profile_cache", "uuid", p.ProfileUUID, "err", err)
	}
	s.setJSON(ctx, profileKey(p.ProfileUUID), p)
}

// --- Space ------------------------------------------------------------------

func (s *Service) Space(ctx context.Context, uuid string) (*domain.CachedSpace, error) {
	if sp := getJSON[domain.CachedSpace](ctx, s.redis, spaceKey(uuid)); sp != nil {
		return sp, nil
	}
	if sp, err := s.store.GetCachedSpace(ctx, uuid); err == nil {
		if time.Since(sp.SyncedAt) < s.cfg.CacheTTL {
			s.setJSON(ctx, spaceKey(uuid), sp)
			return sp, nil
		}
	}
	sp, err := s.fetchSpace(ctx, uuid)
	if err != nil {
		if cached, e := s.store.GetCachedSpace(ctx, uuid); e == nil {
			return cached, nil
		}
		return nil, err
	}
	if err := s.store.UpsertCachedSpace(ctx, *sp); err != nil {
		s.log.Error("upsert space_cache", "uuid", sp.SpaceUUID, "err", err)
	}
	s.setJSON(ctx, spaceKey(uuid), sp)
	return sp, nil
}

// --- Members & quyền --------------------------------------------------------

// Members trả thành viên Space (cache list trong Redis; miss → DB; rỗng/stale →
// sync từ HipCore).
func (s *Service) Members(ctx context.Context, spaceUUID string) ([]domain.CachedMember, error) {
	if s.redis != nil {
		if raw, err := s.redis.Get(ctx, membersKey(spaceUUID)).Bytes(); err == nil {
			var ms []domain.CachedMember
			if json.Unmarshal(raw, &ms) == nil {
				return ms, nil
			}
		}
	}
	ms, err := s.store.ListMembers(ctx, spaceUUID)
	if err == nil && len(ms) > 0 && time.Since(ms[0].SyncedAt) < s.cfg.CacheTTL {
		s.cacheMembers(ctx, spaceUUID, ms)
		return ms, nil
	}
	synced, serr := s.SyncMembers(ctx, spaceUUID)
	if serr != nil {
		if len(ms) > 0 {
			return ms, nil
		}
		return nil, serr
	}
	return synced, nil
}

// Role trả vai trò của profile trong Space ("" nếu không phải thành viên).
func (s *Service) Role(ctx context.Context, spaceUUID, profileUUID string) string {
	if profileUUID == "" {
		return ""
	}
	ms, err := s.Members(ctx, spaceUUID)
	if err != nil {
		return ""
	}
	for _, m := range ms {
		if m.ProfileUUID == profileUUID {
			return m.Role
		}
	}
	return ""
}

// IsMember tiện ích.
func (s *Service) IsMember(ctx context.Context, spaceUUID, profileUUID string) bool {
	return s.Role(ctx, spaceUUID, profileUUID) != ""
}

func (s *Service) cacheMembers(ctx context.Context, spaceUUID string, ms []domain.CachedMember) {
	if s.redis == nil {
		return
	}
	if raw, err := json.Marshal(ms); err == nil {
		s.redis.Set(ctx, membersKey(spaceUUID), raw, s.cfg.CacheTTL)
	}
}

// SyncMembers gọi HipCore /api/spaces/{uuid}/members → ReplaceMembers.
func (s *Service) SyncMembers(ctx context.Context, spaceUUID string) ([]domain.CachedMember, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("hipcore client chưa cấu hình")
	}
	raw, status, err := s.get(ctx, "/api/spaces/"+url.PathEscape(spaceUUID)+"/members?per_page=1000")
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, domain.ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("hipcore members status %d", status)
	}
	var body struct {
		Data []struct {
			UUID  string `json:"uuid"`
			Role  string `json:"role"`
			Pivot struct {
				Role string `json:"role"`
			} `json:"pivot"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	ms := make([]domain.CachedMember, 0, len(body.Data))
	now := time.Now()
	for _, m := range body.Data {
		if m.UUID == "" {
			continue
		}
		role := m.Role
		if role == "" {
			role = m.Pivot.Role
		}
		if role == "" {
			role = domain.RoleMember
		}
		ms = append(ms, domain.CachedMember{
			SpaceUUID: spaceUUID, ProfileUUID: m.UUID, Role: role, SyncedAt: now,
		})
	}
	if err := s.store.ReplaceMembers(ctx, spaceUUID, ms); err != nil {
		return nil, err
	}
	s.cacheMembers(ctx, spaceUUID, ms)
	return ms, nil
}

// --- Full sync (worker) -----------------------------------------------------

func (s *Service) FullSyncProfiles(ctx context.Context) (int, error) {
	if !s.Enabled() {
		return 0, fmt.Errorf("hipcore client chưa cấu hình")
	}
	count := 0
	for page := 1; ; page++ {
		items, hasMore, err := s.fetchProfilePage(ctx, page, 100)
		if err != nil {
			return count, err
		}
		for _, p := range items {
			if err := s.store.UpsertCachedProfile(ctx, p); err != nil {
				s.log.Error("sync profile upsert", "uuid", p.ProfileUUID, "err", err)
				continue
			}
			count++
		}
		if !hasMore || len(items) == 0 {
			break
		}
	}
	return count, nil
}

func (s *Service) FullSyncSpaces(ctx context.Context) (int, error) {
	if !s.Enabled() {
		return 0, fmt.Errorf("hipcore client chưa cấu hình")
	}
	count := 0
	for page := 1; ; page++ {
		items, hasMore, err := s.fetchSpacePage(ctx, page, 100)
		if err != nil {
			return count, err
		}
		for _, sp := range items {
			if err := s.store.UpsertCachedSpace(ctx, sp); err != nil {
				s.log.Error("sync space upsert", "uuid", sp.SpaceUUID, "err", err)
				continue
			}
			count++
		}
		if !hasMore || len(items) == 0 {
			break
		}
	}
	return count, nil
}

// --- HipCore client ---------------------------------------------------------

func (s *Service) accessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Before(s.tokenExp.Add(-30*time.Second)) {
		return s.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.cfg.HipcoreClientID},
		"client_secret": {s.cfg.HipcoreClientSecret},
		"scope":         {""},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.HipcoreURL+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth/token status %d", res.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	s.token = body.AccessToken
	s.tokenExp = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return s.token, nil
}

func (s *Service) get(ctx context.Context, path string) ([]byte, int, error) {
	token, err := s.accessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.HipcoreURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, e := res.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if e != nil {
			break
		}
	}
	return buf, res.StatusCode, nil
}

type hcProfile struct {
	UUID      string     `json:"uuid"`
	UserID    *int64     `json:"user_id"`
	Code      *string    `json:"code"`
	Name      string     `json:"name"`
	Avatar    *string    `json:"avatar"`
	IsActive  *bool      `json:"is_active"`
	CreatedAt *time.Time `json:"created_at"`
}

func (h hcProfile) toDomain() domain.CachedProfile {
	active := true
	if h.IsActive != nil {
		active = *h.IsActive
	}
	return domain.CachedProfile{
		ProfileUUID: h.UUID, UserID: h.UserID, Code: h.Code, Name: h.Name,
		Avatar: h.Avatar, IsActive: active, CreatedAt: h.CreatedAt, SyncedAt: time.Now(),
	}
}

type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*f = ""
		return nil
	}
	if data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*f = flexString(value)
		return nil
	}
	if data[0] == '{' {
		var value struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		if value.Code != "" {
			*f = flexString(value.Code)
		} else {
			*f = flexString(value.Name)
		}
	}
	return nil
}

type hcSpace struct {
	UUID           string      `json:"uuid"`
	Code           *string     `json:"code"`
	Name           string      `json:"name"`
	IsPublic       *bool       `json:"is_public"`
	IsActive       *bool       `json:"is_active"`
	SpaceType      *flexString `json:"space_type"`
	CreatorProfile *struct {
		UUID string `json:"uuid"`
	} `json:"creator_profile"`
}

func (h hcSpace) toDomain() domain.CachedSpace {
	pub, active := false, true
	if h.IsPublic != nil {
		pub = *h.IsPublic
	}
	if h.IsActive != nil {
		active = *h.IsActive
	}
	var spaceType *string
	if h.SpaceType != nil && string(*h.SpaceType) != "" {
		value := string(*h.SpaceType)
		spaceType = &value
	}
	var creator *string
	if h.CreatorProfile != nil && h.CreatorProfile.UUID != "" {
		creator = &h.CreatorProfile.UUID
	}
	return domain.CachedSpace{
		SpaceUUID: h.UUID, Code: h.Code, Name: h.Name, IsPublic: pub,
		IsActive: active, SpaceType: spaceType, CreatorProfileUUID: creator, SyncedAt: time.Now(),
	}
}

func (s *Service) fetchProfile(ctx context.Context, path string) (*domain.CachedProfile, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("hipcore client chưa cấu hình")
	}
	raw, status, err := s.get(ctx, path)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, domain.ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("hipcore %s status %d", path, status)
	}
	var body struct {
		Data hcProfile `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	p := body.Data.toDomain()
	return &p, nil
}

func (s *Service) fetchSpace(ctx context.Context, uuid string) (*domain.CachedSpace, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("hipcore client chưa cấu hình")
	}
	raw, status, err := s.get(ctx, "/api/spaces/"+url.PathEscape(uuid))
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, domain.ErrNotFound
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("hipcore /api/spaces/%s status %d", uuid, status)
	}
	var body struct {
		Data hcSpace `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	sp := body.Data.toDomain()
	return &sp, nil
}

func (s *Service) fetchProfilePage(ctx context.Context, page, perPage int) ([]domain.CachedProfile, bool, error) {
	raw, status, err := s.get(ctx, fmt.Sprintf("/api/profiles?page=%d&per_page=%d", page, perPage))
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("hipcore /api/profiles status %d", status)
	}
	var body struct {
		Data       []hcProfile `json:"data"`
		Pagination pagination  `json:"pagination"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	out := make([]domain.CachedProfile, 0, len(body.Data))
	for _, h := range body.Data {
		if h.UUID != "" {
			out = append(out, h.toDomain())
		}
	}
	return out, body.Pagination.hasMore(), nil
}

func (s *Service) fetchSpacePage(ctx context.Context, page, perPage int) ([]domain.CachedSpace, bool, error) {
	raw, status, err := s.get(ctx, fmt.Sprintf("/api/spaces?page=%d&per_page=%d", page, perPage))
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("hipcore /api/spaces status %d", status)
	}
	var body struct {
		Data       []hcSpace  `json:"data"`
		Pagination pagination `json:"pagination"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, err
	}
	out := make([]domain.CachedSpace, 0, len(body.Data))
	for _, h := range body.Data {
		if h.UUID != "" {
			out = append(out, h.toDomain())
		}
	}
	return out, body.Pagination.hasMore(), nil
}

type pagination struct {
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
}

func (p pagination) hasMore() bool {
	return p.LastPage == 0 || p.CurrentPage < p.LastPage
}

// --- Redis helpers ----------------------------------------------------------

func (s *Service) setJSON(ctx context.Context, key string, v any) {
	if s.redis == nil {
		return
	}
	if raw, err := json.Marshal(v); err == nil {
		s.redis.Set(ctx, key, raw, s.cfg.CacheTTL)
	}
}

func getJSON[T any](ctx context.Context, rdb *redis.Client, key string) *T {
	if rdb == nil {
		return nil
	}
	raw, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}
	var v T
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}
