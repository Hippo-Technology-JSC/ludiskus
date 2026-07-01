package service

import (
	"context"
	"strings"

	"ludiskus/internal/domain"
)

type SearchInput struct {
	Query      string
	SpaceUUID  string
	BoardID    string
	AuthorUUID string
	TopicType  string
	Limit      int
	Offset     int
}

// Search tìm topic trong phạm vi Space người dùng được xem (docs/06 §6.5).
func (s *Service) Search(ctx context.Context, profileUUID string, in SearchInput) ([]domain.Topic, error) {
	if strings.TrimSpace(in.Query) == "" {
		return []domain.Topic{}, nil
	}
	var scope []string
	if in.SpaceUUID != "" {
		if _, err := s.requireView(ctx, in.SpaceUUID, profileUUID); err != nil {
			return nil, err
		}
		scope = []string{in.SpaceUUID}
	} else {
		scope = s.viewableSpaces(ctx, profileUUID)
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 20
	}
	topics, err := s.repo.SearchTopics(ctx, in.Query, scope, in.BoardID, in.AuthorUUID, in.TopicType, in.Limit, in.Offset)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*domain.Topic, len(topics))
	for i := range topics {
		ptrs[i] = &topics[i]
	}
	s.enrichTopics(ctx, ptrs)
	return topics, nil
}

// viewableSpaces gộp Space người dùng là thành viên + Space công khai đã bật forum.
func (s *Service) viewableSpaces(ctx context.Context, profileUUID string) []string {
	set := map[string]bool{}
	out := []string{}
	add := func(u string) {
		if u != "" && !set[u] {
			set[u] = true
			out = append(out, u)
		}
	}
	if profileUUID != "" {
		if mine, err := s.repo.SpacesForProfile(ctx, profileUUID); err == nil {
			for _, u := range mine {
				add(u)
			}
		}
	}
	if pub, err := s.repo.PublicSpaces(ctx); err == nil {
		for _, u := range pub {
			add(u)
		}
	}
	return out
}
