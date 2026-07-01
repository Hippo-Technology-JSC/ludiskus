package service

import (
	"context"

	"ludiskus/internal/domain"
)

// SpaceCard tóm tắt một cộng đồng cho danh sách (docs/11).
type SpaceCard struct {
	Space *domain.CachedSpace `json:"space"`
	Forum *domain.SpaceForum  `json:"forum"`
}

// ListSpaces trả các Space-forum người dùng được xem (thành viên + công khai).
func (s *Service) ListSpaces(ctx context.Context, profileUUID string) ([]SpaceCard, error) {
	uuids := s.viewableSpaces(ctx, profileUUID)
	out := []SpaceCard{}
	for _, u := range uuids {
		forum, err := s.repo.GetForum(ctx, u)
		if err != nil || !forum.Enabled {
			continue
		}
		sp, err := s.ident.Space(ctx, u)
		if err != nil {
			continue
		}
		out = append(out, SpaceCard{Space: sp, Forum: forum})
	}
	return out, nil
}

// RefreshCache làm mới cache thủ công (docs/05 §5.3) — vận hành.
func (s *Service) RefreshCache(ctx context.Context, kind, id string) error {
	switch kind {
	case "profile":
		if id == "" {
			_, err := s.ident.FullSyncProfiles(ctx)
			return err
		}
		_, err := s.ident.Profile(ctx, id)
		return err
	case "space":
		if id == "" {
			_, err := s.ident.FullSyncSpaces(ctx)
			return err
		}
		_, err := s.ident.Space(ctx, id)
		return err
	case "members":
		if id == "" {
			return domain.ErrValidation
		}
		_, err := s.ident.SyncMembers(ctx, id)
		return err
	}
	return domain.ErrValidation
}
