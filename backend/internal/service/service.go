// Package service chứa nghiệp vụ ludiskus (docs/03-08): forum theo Space, board,
// topic/post, reaction, mention, tìm kiếm, đính kèm, kiểm duyệt, thông báo.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"ludiskus/db"
	"ludiskus/internal/config"
	"ludiskus/internal/domain"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/notify"
	"ludiskus/internal/repository"
	"ludiskus/internal/storage"
)

type Service struct {
	repo   *repository.Repo
	ident  *identity.Service
	store  *storage.Store
	lunoti *notify.Client
	md     *markdown.Renderer
	cfg    *config.Config
	redis  *redis.Client

	defaultBoards []seedBoard
	bannedWords   []string
}

type seedBoard struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Position      int    `json:"position"`
	DescriptionMD string `json:"description_md"`
}

func New(repo *repository.Repo, ident *identity.Service, store *storage.Store, lunoti *notify.Client, md *markdown.Renderer, cfg *config.Config, rdb *redis.Client) *Service {
	s := &Service{repo: repo, ident: ident, store: store, lunoti: lunoti, md: md, cfg: cfg, redis: rdb}
	s.loadSeeds()
	return s
}

func (s *Service) Identity() *identity.Service { return s.ident }

func (s *Service) loadSeeds() {
	if raw, err := db.Seeds.ReadFile("seeds/boards.json"); err == nil {
		var sf struct {
			DefaultBoards []seedBoard `json:"default_boards"`
		}
		if json.Unmarshal(raw, &sf) == nil {
			s.defaultBoards = sf.DefaultBoards
		}
	}
	if raw, err := db.Seeds.ReadFile("seeds/banned_words.json"); err == nil {
		var sf struct {
			BannedWords []string `json:"banned_words"`
		}
		if json.Unmarshal(raw, &sf) == nil {
			s.bannedWords = sf.BannedWords
		}
	}
}

// --- health -----------------------------------------------------------------

func (s *Service) Ready(ctx context.Context) map[string]string {
	checks := map[string]string{"db": "ok", "redis": "ok", "storage": "ok"}
	if err := s.repo.Ping(ctx); err != nil {
		checks["db"] = err.Error()
	}
	if s.redis != nil {
		if err := s.redis.Ping(ctx).Err(); err != nil {
			checks["redis"] = err.Error()
		}
	} else {
		checks["redis"] = "disabled"
	}
	if s.store != nil {
		if err := s.store.Ready(ctx); err != nil {
			checks["storage"] = err.Error()
		}
	} else {
		checks["storage"] = "disabled"
	}
	return checks
}

// --- phân quyền (docs/05 §5.4) ---------------------------------------------

// role trả vai trò hiệu lực của profile trong Space: ưu tiên role thành viên,
// nâng moderator nếu có trong space_moderators.
func (s *Service) role(ctx context.Context, spaceUUID, profileUUID string) string {
	if profileUUID == "" {
		return ""
	}
	role := s.ident.Role(ctx, spaceUUID, profileUUID)
	if role == domain.RoleOwner || role == domain.RoleAdmin {
		return role
	}
	if mod, _ := s.repo.IsModerator(ctx, spaceUUID, profileUUID); mod {
		return domain.RoleModerator
	}
	return role
}

func canModerate(role string) bool {
	return role == domain.RoleOwner || role == domain.RoleAdmin || role == domain.RoleModerator
}

// requireView kiểm quyền đọc Space; trả forum nếu được phép.
func (s *Service) requireView(ctx context.Context, spaceUUID, profileUUID string) (*domain.SpaceForum, error) {
	forum, err := s.repo.GetForum(ctx, spaceUUID)
	if err != nil {
		return nil, err
	}
	if !forum.Enabled {
		return nil, domain.ErrNotFound
	}
	if forum.IsPublic {
		return forum, nil
	}
	if s.role(ctx, spaceUUID, profileUUID) == "" {
		return nil, domain.ErrForbidden
	}
	return forum, nil
}

// requirePost kiểm quyền đăng bài theo post_policy.
func (s *Service) requirePost(ctx context.Context, forum *domain.SpaceForum, profileUUID string) error {
	if profileUUID == "" {
		return domain.ErrUnauthorized
	}
	role := s.role(ctx, forum.SpaceUUID, profileUUID)
	switch forum.PostPolicy {
	case domain.PolicyStaff:
		if !canModerate(role) {
			return domain.ErrForbidden
		}
	case domain.PolicyAnyone:
		// chỉ cần đăng nhập (đã có profileUUID)
	default: // members
		if role == "" {
			return domain.ErrForbidden
		}
	}
	return nil
}

func (s *Service) requireModerate(ctx context.Context, spaceUUID, profileUUID string) error {
	if !canModerate(s.role(ctx, spaceUUID, profileUUID)) {
		return domain.ErrForbidden
	}
	return nil
}

// --- forum (bật/cấu hình) ---------------------------------------------------

func (s *Service) GetForum(ctx context.Context, spaceUUID, profileUUID string) (*domain.SpaceForum, error) {
	return s.requireView(ctx, spaceUUID, profileUUID)
}

// EnableForum bật forum cho Space (owner/admin), seed board mặc định.
func (s *Service) EnableForum(ctx context.Context, spaceUUID, profileUUID string) (*domain.SpaceForum, error) {
	sp, err := s.ident.Space(ctx, spaceUUID)
	if err != nil {
		return nil, err
	}
	role := s.role(ctx, spaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	forum := domain.SpaceForum{
		SpaceUUID: spaceUUID, Enabled: true, IsPublic: sp.IsPublic,
		PostPolicy: domain.PolicyMembers, ModerationMode: domain.ModFirstPost,
		BannedWords: s.bannedWords, ReportAutoHideThreshold: 5,
		DefaultTopicType: "discussion", Settings: json.RawMessage("{}"),
	}
	out, err := s.repo.UpsertForum(ctx, forum)
	if err != nil {
		return nil, err
	}
	for _, b := range s.defaultBoards {
		html := s.md.Render(b.DescriptionMD)
		_ = s.repo.SeedBoard(ctx, domain.Board{
			SpaceUUID: spaceUUID, Code: b.Code, Name: b.Name,
			DescriptionMD: &b.DescriptionMD, DescriptionHTML: &html,
			Kind: b.Kind, Position: b.Position,
		})
	}
	return out, nil
}

type ForumSettings struct {
	IsPublic                *bool     `json:"isPublic"`
	PostPolicy              *string   `json:"postPolicy"`
	ModerationMode          *string   `json:"moderationMode"`
	BannedWords             *[]string `json:"bannedWords"`
	ReportAutoHideThreshold *int      `json:"reportAutoHideThreshold"`
	DefaultTopicType        *string   `json:"defaultTopicType"`
}

func (s *Service) UpdateForumSettings(ctx context.Context, spaceUUID, profileUUID string, in ForumSettings) (*domain.SpaceForum, error) {
	role := s.role(ctx, spaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	forum, err := s.repo.GetForum(ctx, spaceUUID)
	if err != nil {
		return nil, err
	}
	if in.IsPublic != nil {
		forum.IsPublic = *in.IsPublic
	}
	if in.PostPolicy != nil {
		if !validPolicy(*in.PostPolicy) {
			return nil, fmt.Errorf("%w: postPolicy không hợp lệ", domain.ErrValidation)
		}
		forum.PostPolicy = *in.PostPolicy
	}
	if in.ModerationMode != nil {
		if !validMode(*in.ModerationMode) {
			return nil, fmt.Errorf("%w: moderationMode không hợp lệ", domain.ErrValidation)
		}
		forum.ModerationMode = *in.ModerationMode
	}
	if in.BannedWords != nil {
		forum.BannedWords = *in.BannedWords
	}
	if in.ReportAutoHideThreshold != nil {
		forum.ReportAutoHideThreshold = *in.ReportAutoHideThreshold
	}
	if in.DefaultTopicType != nil {
		forum.DefaultTopicType = *in.DefaultTopicType
	}
	return s.repo.UpsertForum(ctx, *forum)
}

// --- moderator --------------------------------------------------------------

func (s *Service) ListModerators(ctx context.Context, spaceUUID, profileUUID string) ([]string, error) {
	if err := s.requireModerate(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	return s.repo.ListModerators(ctx, spaceUUID)
}

func (s *Service) AddModerator(ctx context.Context, spaceUUID, profileUUID, target string) error {
	role := s.role(ctx, spaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return s.repo.AddModerator(ctx, spaceUUID, target, profileUUID)
}

func (s *Service) RemoveModerator(ctx context.Context, spaceUUID, profileUUID, target string) error {
	role := s.role(ctx, spaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return s.repo.RemoveModerator(ctx, spaceUUID, target)
}

// --- board ------------------------------------------------------------------

func (s *Service) ListBoards(ctx context.Context, spaceUUID, profileUUID string) ([]domain.Board, error) {
	if _, err := s.requireView(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	return s.repo.ListBoards(ctx, spaceUUID)
}

type BoardInput struct {
	ParentID      *string `json:"parentId"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	DescriptionMD string  `json:"descriptionMd"`
	Kind          string  `json:"kind"`
	Position      int     `json:"position"`
	IsLocked      bool    `json:"isLocked"`
	MinRole       string  `json:"minRole"`
}

func (s *Service) CreateBoard(ctx context.Context, spaceUUID, profileUUID string, in BoardInput) (*domain.Board, error) {
	role := s.role(ctx, spaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	if strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: code và name là bắt buộc", domain.ErrValidation)
	}
	if !validBoardKind(in.Kind) {
		in.Kind = "forum"
	}
	if in.MinRole == "" {
		in.MinRole = domain.RoleMember
	}
	html := s.md.Render(in.DescriptionMD)
	b := domain.Board{
		SpaceUUID: spaceUUID, ParentID: in.ParentID, Code: in.Code, Name: in.Name,
		Kind: in.Kind, Position: in.Position, IsLocked: in.IsLocked, MinRole: in.MinRole,
	}
	if in.DescriptionMD != "" {
		b.DescriptionMD = &in.DescriptionMD
		b.DescriptionHTML = &html
	}
	return s.repo.CreateBoard(ctx, b)
}

func (s *Service) UpdateBoard(ctx context.Context, boardID, profileUUID string, in BoardInput) (*domain.Board, error) {
	b, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	role := s.role(ctx, b.SpaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return nil, domain.ErrForbidden
	}
	if in.Name != "" {
		b.Name = in.Name
	}
	b.Position = in.Position
	b.IsLocked = in.IsLocked
	if in.MinRole != "" {
		b.MinRole = in.MinRole
	}
	if in.DescriptionMD != "" {
		html := s.md.Render(in.DescriptionMD)
		b.DescriptionMD = &in.DescriptionMD
		b.DescriptionHTML = &html
	}
	return s.repo.UpdateBoard(ctx, boardID, *b)
}

func (s *Service) DeleteBoard(ctx context.Context, boardID, profileUUID string) error {
	b, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return err
	}
	role := s.role(ctx, b.SpaceUUID, profileUUID)
	if role != domain.RoleOwner && role != domain.RoleAdmin {
		return domain.ErrForbidden
	}
	return s.repo.DeleteBoard(ctx, boardID)
}

// --- validators -------------------------------------------------------------

func validPolicy(p string) bool {
	return p == domain.PolicyMembers || p == domain.PolicyAnyone || p == domain.PolicyStaff
}
func validMode(m string) bool {
	return m == domain.ModNone || m == domain.ModPost || m == domain.ModPre || m == domain.ModFirstPost
}
func validBoardKind(k string) bool {
	switch k {
	case "forum", "qna", "support", "announcement":
		return true
	}
	return false
}
func validTopicType(t string) bool {
	return t == "discussion" || t == "question" || t == "announcement"
}
