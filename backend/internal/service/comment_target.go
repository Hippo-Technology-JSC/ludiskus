package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ludiskus/internal/domain"
	commentresolver "ludiskus/internal/resolver"
)

func (s *Service) resolveLocalCommentResource(ctx context.Context, resourceType, resourceID string) (*commentresolver.Result, error) {
	v, err := s.InteractionContext(ctx, resourceType, resourceID)
	if err != nil {
		return nil, err
	}
	var owner *domain.CommentOwner
	if v.Owner != nil {
		owner = &domain.CommentOwner{Type: v.Owner.Type, ID: v.Owner.ID}
	}
	return &commentresolver.Result{Exists: v.Exists, Type: v.Type, ID: v.ID, SpaceUUID: v.SpaceUUID, Owner: owner,
		Visibility: v.Visibility, State: v.State, Title: v.Title, Summary: v.Summary, ThumbnailURL: v.ThumbnailURL,
		CanonicalPath: v.CanonicalPath, Capabilities: v.Capabilities}, nil
}

func (s *Service) ensureCommentTarget(ctx context.Context, ref domain.ResourceRef, creator string) (*domain.CommentTarget, error) {
	if !s.cfg.CommentEnabled {
		return nil, domain.ErrNotFound
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	svc, err := s.repo.GetCommentService(ctx, ref.Service)
	if err != nil || !svc.IsActive {
		return nil, domain.ErrServiceNotRegistered
	}
	if t, err := s.repo.GetCommentTarget(ctx, ref); err == nil {
		return t, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	t := domain.CommentTarget{ServiceCode: ref.Service, ResourceType: ref.Type, ResourceID: ref.ID,
		Visibility: "private", State: "unverified", ThreadState: "open", Capabilities: json.RawMessage(`{}`)}
	if creator != "" {
		t.CreatedBy = &creator
	}
	if svc.VerifyMode != "trust" {
		resolved, resolveErr := s.resolver.Resolve(ctx, ref)
		if resolveErr == nil {
			applyResolvedTarget(&t, resolved)
			now := time.Now()
			t.VerifiedAt = &now
		} else if svc.VerifyMode == "strict" {
			if errors.Is(resolveErr, commentresolver.ErrNotFound) {
				return nil, domain.ErrResolverMissing
			}
			return nil, domain.ErrResolverUnavailable
		}
	}
	out, err := s.repo.UpsertCommentTarget(ctx, t)
	if err != nil {
		return nil, err
	}
	if out.OwnerType != nil && *out.OwnerType == "profile" && out.OwnerID != nil {
		_, _ = s.repo.UpsertCommentParticipant(ctx, domain.CommentParticipant{TargetID: out.ID, ProfileUUID: *out.OwnerID, Reason: "owner"})
	}
	return out, nil
}

func applyResolvedTarget(t *domain.CommentTarget, v *commentresolver.Result) {
	t.Title = v.Title
	t.Summary = v.Summary
	t.ThumbnailURL = v.ThumbnailURL
	t.CanonicalPath = v.CanonicalPath
	t.Visibility = v.Visibility
	t.State = v.State
	t.Capabilities = v.Capabilities
	t.SpaceUUID = v.SpaceUUID
	if v.Owner != nil {
		t.OwnerType = &v.Owner.Type
		t.OwnerID = &v.Owner.ID
	}
}

func (s *Service) ensureCommentReadable(ctx context.Context, ref domain.ResourceRef, profileUUID string) (*domain.CommentTarget, domain.CommentPolicy, error) {
	t, err := s.ensureCommentTarget(ctx, ref, profileUUID)
	if err != nil {
		return nil, domain.CommentPolicy{}, err
	}
	if t.State == "gone" {
		return nil, domain.CommentPolicy{}, domain.ErrResourceGone
	}
	if t.State == "blocked" {
		return nil, domain.CommentPolicy{}, domain.ErrResourceBlocked
	}
	owner := t.OwnerID != nil && *t.OwnerID == profileUUID
	if t.State == "unverified" && (t.CreatedBy == nil || *t.CreatedBy != profileUUID) && !owner {
		return nil, domain.CommentPolicy{}, domain.ErrNotFound
	}
	allowed := false
	switch t.Visibility {
	case "public":
		allowed = true
	case "authenticated":
		allowed = profileUUID != ""
	case "space":
		allowed = t.SpaceUUID != nil && s.ident.IsMember(ctx, *t.SpaceUUID, profileUUID)
	case "private", "connections":
		allowed = owner || (t.OwnerType != nil && *t.OwnerType == "space" && t.OwnerID != nil && canModerate(s.role(ctx, *t.OwnerID, profileUUID)))
	}
	moderator := owner
	if t.SpaceUUID != nil {
		moderator = moderator || canModerate(s.role(ctx, *t.SpaceUUID, profileUUID))
	}
	if !allowed && !moderator {
		return nil, domain.CommentPolicy{}, domain.ErrForbidden
	}
	if t.ThreadState == "hidden" && !moderator {
		return nil, domain.CommentPolicy{}, domain.ErrNotFound
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return nil, p, err
	}
	if !p.Enabled {
		return nil, p, domain.ErrCommentDisabled
	}
	return t, p, nil
}

func (s *Service) ensureCommentable(ctx context.Context, ref domain.ResourceRef, profileUUID string) (*domain.CommentTarget, domain.CommentPolicy, domain.CommentCapabilities, error) {
	if profileUUID == "" {
		return nil, domain.CommentPolicy{}, domain.CommentCapabilities{}, domain.ErrUnauthorized
	}
	t, p, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return nil, p, domain.CommentCapabilities{}, err
	}
	c := s.commentCapabilities(ctx, t, profileUUID, p)
	if t.ThreadState != "open" {
		return nil, p, c, domain.ErrThreadLocked
	}
	if !c.CanComment {
		return nil, p, c, domain.ErrCommentNotAllowed
	}
	return t, p, c, nil
}

type PushCommentTarget struct {
	Ref           domain.ResourceRef   `json:"ref"`
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

func (s *Service) PushCommentTargets(ctx context.Context, serviceCode string, items []PushCommentTarget) ([]domain.CommentTarget, error) {
	if len(items) > s.cfg.CommentBatchMax {
		return nil, fmt.Errorf("%w: tối đa %d nội dung", domain.ErrValidation, s.cfg.CommentBatchMax)
	}
	out := []domain.CommentTarget{}
	for _, item := range items {
		if item.Ref.Service != serviceCode {
			return nil, domain.ErrServiceScope
		}
		if err := item.Ref.Validate(); err != nil {
			return nil, err
		}
		t := domain.CommentTarget{ServiceCode: item.Ref.Service, ResourceType: item.Ref.Type, ResourceID: item.Ref.ID, SpaceUUID: item.SpaceUUID,
			Visibility: defaultString(item.Visibility, "private"), State: defaultString(item.State, "active"), ThreadState: "open", Title: item.Title, Summary: item.Summary,
			ThumbnailURL: item.ThumbnailURL, CanonicalPath: item.CanonicalPath, Capabilities: item.Capabilities}
		if item.Owner != nil {
			t.OwnerType = &item.Owner.Type
			t.OwnerID = &item.Owner.ID
		}
		now := time.Now()
		t.VerifiedAt = &now
		v, err := s.repo.UpsertCommentTarget(ctx, t)
		if err != nil {
			return nil, err
		}
		s.resolver.InvalidateCache(ctx, item.Ref)
		s.clearCommentCaches(ctx, item.Ref)
		out = append(out, *v)
	}
	return out, nil
}

func (s *Service) InvalidateCommentTargets(ctx context.Context, serviceCode string, refs []domain.ResourceRef, reason string) error {
	valid := map[string]string{"deleted": "gone", "blocked": "blocked", "visibility": "unverified", "restored": "active"}
	state, ok := valid[reason]
	if !ok {
		return domain.ErrValidation
	}
	for _, ref := range refs {
		if ref.Service != serviceCode {
			return domain.ErrServiceScope
		}
		t, err := s.repo.GetCommentTarget(ctx, ref)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err = s.repo.SetCommentTargetState(ctx, t.ID, state, false); err != nil {
			return err
		}
		s.resolver.InvalidateCache(ctx, ref)
		s.clearCommentCaches(ctx, ref)
	}
	return nil
}

func (s *Service) clearCommentCaches(ctx context.Context, ref domain.ResourceRef) {
	if s.redis == nil {
		return
	}
	_ = s.redis.Del(ctx, "cmt:sum:"+ref.String(), "cmt:res:"+ref.String()).Err()
	var cursor uint64
	for {
		keys, next, _ := s.redis.Scan(ctx, cursor, "cmt:pub:"+ref.String()+":*", 100).Result()
		if len(keys) > 0 {
			_ = s.redis.Del(ctx, keys...).Err()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
