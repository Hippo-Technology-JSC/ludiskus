package service

import (
	"context"
	"fmt"
	"ludiskus/internal/search"
	"regexp"
	"strings"
	"time"

	"ludiskus/internal/domain"
)

type SearchInput struct {
	Query                          string
	SpaceUUID                      string
	BoardID                        string
	AuthorUUID                     string
	TopicType                      string
	Limit                          int
	Offset                         int
	Tag, Status, Kind, From, Until string
}

var forumUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Search tìm topic trong phạm vi Space người dùng được xem (docs/06 §6.5).
func (s *Service) Search(ctx context.Context, profileUUID string, in SearchInput) ([]domain.Topic, error) {
	if strings.TrimSpace(in.Query) == "" {
		return []domain.Topic{}, nil
	}
	for _, id := range []string{in.SpaceUUID, in.BoardID, in.AuthorUUID} {
		if id != "" && !forumUUID.MatchString(id) {
			return nil, domain.ErrValidation
		}
	}
	var scope []string
	if in.SpaceUUID != "" {
		if _, err := s.requireView(ctx, in.SpaceUUID, profileUUID); err != nil {
			return nil, err
		}
		scope = []string{in.SpaceUUID}
	} else {
		for _, space := range s.viewableSpaces(ctx, profileUUID) {
			if _, err := s.requireView(ctx, space, profileUUID); err == nil {
				scope = append(scope, space)
			}
		}
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 20
	}
	status := in.Status
	if status == "" {
		status = "published"
	}
	if status != "published" && status != "locked" {
		if status != "pending" && status != "hidden" {
			return nil, domain.ErrValidation
		}
		if in.SpaceUUID == "" {
			return nil, domain.ErrForbidden
		}
		if err := s.requireModerate(ctx, in.SpaceUUID, profileUUID); err != nil {
			return nil, err
		}
	}
	if in.Kind != "" && in.Kind != "topic" && in.Kind != "post" {
		return nil, domain.ErrValidation
	}
	if in.TopicType != "" && !validTopicType(in.TopicType) {
		return nil, domain.ErrValidation
	}
	var from, until *time.Time
	for _, date := range []struct {
		text string
		dst  **time.Time
	}{{in.From, &from}, {in.Until, &until}} {
		text, dst := date.text, date.dst
		if text != "" {
			v, e := time.Parse("2006-01-02", text)
			if e != nil {
				return nil, fmt.Errorf("%w: ngày phải có dạng YYYY-MM-DD", domain.ErrValidation)
			}
			*dst = &v
		}
	}
	if until != nil {
		v := until.AddDate(0, 0, 1)
		until = &v
	}
	if from != nil && until != nil && !from.Before(*until) {
		return nil, domain.ErrValidation
	}
	var engine search.Engine = s.repo
	topics, err := engine.SearchForum(ctx, search.Options{Query: in.Query, Spaces: scope, BoardID: in.BoardID, AuthorUUID: in.AuthorUUID, TopicType: in.TopicType, Tag: in.Tag, Status: status, Kind: in.Kind, From: from, Until: until, Limit: in.Limit, Offset: max(0, in.Offset)})
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
