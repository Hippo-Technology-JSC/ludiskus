// Package resolver resolves foreign resources through their service-owned S2S
// contract. It deliberately depends on repository/domain, never service.
package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"ludiskus/internal/config"
	"ludiskus/internal/domain"
	"ludiskus/internal/repository"
)

var (
	ErrNotFound    = errors.New("resolver not found")
	ErrUnavailable = errors.New("resolver unavailable")
	ErrInvalid     = errors.New("resolver invalid response")
)

type Result struct {
	Exists        bool                 `json:"exists"`
	Type          string               `json:"type"`
	ID            string               `json:"id"`
	SpaceUUID     *string              `json:"spaceUuid,omitempty"`
	Owner         *domain.CommentOwner `json:"owner,omitempty"`
	Visibility    string               `json:"visibility"`
	State         string               `json:"state"`
	Title         string               `json:"title"`
	Summary       string               `json:"summary"`
	ThumbnailURL  string               `json:"thumbnailUrl"`
	CanonicalPath string               `json:"canonicalPath"`
	Capabilities  json.RawMessage      `json:"capabilities"`
}

type LocalFunc func(context.Context, string, string) (*Result, error)

type Resolver struct {
	repo     *repository.Repo
	redis    *redis.Client
	cfg      *config.Config
	http     *http.Client
	mu       sync.Mutex
	token    string
	tokenExp time.Time
	local    LocalFunc
}

func New(repo *repository.Repo, rdb *redis.Client, cfg *config.Config) *Resolver {
	return &Resolver{repo: repo, redis: rdb, cfg: cfg, http: &http.Client{Timeout: cfg.CommentResolveTimeout}}
}

func (r *Resolver) SetLocal(fn LocalFunc) { r.local = fn }

func cacheKey(ref domain.ResourceRef) string {
	return "cmt:res:" + ref.Service + ":" + ref.Type + ":" + ref.ID
}

func (r *Resolver) Resolve(ctx context.Context, ref domain.ResourceRef) (*Result, error) {
	if err := ref.Validate(); err != nil {
		return nil, ErrInvalid
	}
	if ref.Service == "ludiskus" && r.local != nil {
		return r.local(ctx, ref.Type, ref.ID)
	}
	if r.redis != nil {
		if raw, err := r.redis.Get(ctx, cacheKey(ref)).Bytes(); err == nil {
			var out Result
			if json.Unmarshal(raw, &out) == nil {
				return &out, nil
			}
		}
	}
	svc, err := r.repo.GetCommentService(ctx, ref.Service)
	if err != nil || !svc.IsActive {
		return nil, ErrNotFound
	}
	if svc.BaseURL == "" {
		return nil, ErrUnavailable
	}
	paths := []string{svc.ContextPath}
	if svc.ContextPath == "" {
		paths = []string{"resource-context", "interaction-context"}
	}
	for i, path := range paths {
		out, status, err := r.call(ctx, svc.BaseURL, path, ref)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
			if i+1 < len(paths) {
				continue
			}
			return nil, ErrNotFound
		}
		if status >= 500 {
			return nil, ErrUnavailable
		}
		if status != http.StatusOK {
			return nil, ErrInvalid
		}
		if err := validateResult(ref, out); err != nil {
			return nil, err
		}
		if svc.ContextPath == "" {
			_ = r.repo.UpdateCommentContextPath(ctx, svc.Code, path)
		}
		if r.redis != nil {
			raw, _ := json.Marshal(out)
			_ = r.redis.Set(ctx, cacheKey(ref), raw, r.cfg.CommentTargetTTL).Err()
		}
		return out, nil
	}
	return nil, ErrNotFound
}

func (r *Resolver) ResolveBatch(ctx context.Context, refs []domain.ResourceRef) (map[string]*Result, map[string]error) {
	out := map[string]*Result{}
	skipped := map[string]error{}
	if len(refs) > r.cfg.CommentBatchMax {
		refs = refs[:r.cfg.CommentBatchMax]
	}
	for _, ref := range refs {
		v, err := r.Resolve(ctx, ref)
		if err != nil {
			skipped[ref.String()] = err
		} else {
			out[ref.String()] = v
		}
	}
	return out, skipped
}

func (r *Resolver) InvalidateCache(ctx context.Context, ref domain.ResourceRef) {
	if r.redis != nil {
		_ = r.redis.Del(ctx, cacheKey(ref)).Err()
	}
}

func (r *Resolver) call(ctx context.Context, base, path string, ref domain.ResourceRef) (*Result, int, error) {
	token, err := r.accessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	u := strings.TrimRight(base, "/") + "/api/v1/s2s/" + path + "/" + url.PathEscape(ref.Type) + "/" + url.PathEscape(ref.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	res, err := r.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, nil
	}
	var wrapper struct {
		Data *Result `json:"data"`
	}
	var direct Result
	var raw json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, res.StatusCode, err
	}
	if json.Unmarshal(raw, &wrapper) == nil && wrapper.Data != nil {
		return wrapper.Data, res.StatusCode, nil
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		return nil, res.StatusCode, err
	}
	return &direct, res.StatusCode, nil
}

func validateResult(ref domain.ResourceRef, v *Result) error {
	if !v.Exists {
		return ErrNotFound
	}
	if v.Type != "" && v.Type != ref.Type || v.ID != "" && v.ID != ref.ID {
		return ErrInvalid
	}
	validVis := map[string]bool{"public": true, "authenticated": true, "space": true, "connections": true, "private": true}
	validState := map[string]bool{"active": true, "gone": true, "blocked": true}
	if !validVis[v.Visibility] || !validState[v.State] {
		return ErrInvalid
	}
	if v.CanonicalPath != "" && (!strings.HasPrefix(v.CanonicalPath, "/") || strings.HasPrefix(v.CanonicalPath, "//") || strings.Contains(v.CanonicalPath, "..") || len(v.CanonicalPath) > 301) {
		return ErrInvalid
	}
	if len([]rune(v.Title)) > 200 {
		v.Title = string([]rune(v.Title)[:200])
	}
	if len([]rune(v.Summary)) > 400 {
		v.Summary = string([]rune(v.Summary)[:400])
	}
	if len(v.Capabilities) == 0 {
		v.Capabilities = json.RawMessage(`{}`)
	}
	return nil
}

func (r *Resolver) accessToken(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.token != "" && time.Now().Before(r.tokenExp.Add(-30*time.Second)) {
		return r.token, nil
	}
	if r.cfg.HipcoreClientID == "" || r.cfg.HipcoreClientSecret == "" {
		return "", fmt.Errorf("hipcore client chưa cấu hình")
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {r.cfg.HipcoreClientID}, "client_secret": {r.cfg.HipcoreClientSecret}, "scope": {""}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.HipcoreURL+"/oauth/token", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := r.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth status %d", res.StatusCode)
	}
	var b struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&b); err != nil {
		return "", err
	}
	r.token = b.AccessToken
	r.tokenExp = time.Now().Add(time.Duration(b.ExpiresIn) * time.Second)
	return r.token, nil
}
