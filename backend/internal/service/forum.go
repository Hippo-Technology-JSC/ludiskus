package service

import (
	"context"
	"fmt"
	"ludiskus/internal/domain"
	"mime"
	"strings"
)

// Forum helpers deliberately do not enter the LuComment paths.
func (s *Service) readableForumTopic(ctx context.Context, t *domain.Topic, profile string) error {
	if _, err := s.requireView(ctx, t.SpaceUUID, profile); err != nil {
		return err
	}
	if t.Status == domain.StatusDeleted {
		return domain.ErrNotFound
	}
	if t.Status != domain.StatusPublished && t.Status != domain.StatusLocked && t.AuthorProfileUUID != profile && !canModerate(s.role(ctx, t.SpaceUUID, profile)) {
		return domain.ErrNotFound
	}
	return nil
}
func (s *Service) forumTopicCapabilities(ctx context.Context, t *domain.Topic, profile string) {
	t.CanModerate = canModerate(s.role(ctx, t.SpaceUUID, profile))
	t.CanManage = t.CanModerate || t.AuthorProfileUUID == profile
	f, err := s.requireView(ctx, t.SpaceUUID, profile)
	t.CanReply = err == nil && t.Status == domain.StatusPublished && s.requirePost(ctx, f, profile) == nil
	if b, err := s.repo.GetBoard(ctx, t.BoardID); err == nil {
		t.BoardKind = b.Kind
		t.CanReply = t.CanReply && !b.IsLocked
	}
}
func (s *Service) AssignForumTopic(ctx context.Context, id, profile string, assignee *string) error {
	t, err := s.repo.GetTopic(ctx, id)
	if err != nil {
		return err
	}
	if err = s.readableForumTopic(ctx, t, profile); err != nil {
		return err
	}
	if err = s.requireModerate(ctx, t.SpaceUUID, profile); err != nil {
		return err
	}
	b, err := s.repo.GetBoard(ctx, t.BoardID)
	if err != nil {
		return err
	}
	if b.Kind != "support" {
		return domain.ErrValidation
	}
	if assignee != nil && *assignee == "" {
		assignee = nil
	}
	if assignee != nil && !forumUUID.MatchString(*assignee) {
		return domain.ErrValidation
	}
	if assignee != nil && !s.ident.IsMember(ctx, t.SpaceUUID, *assignee) {
		return domain.ErrValidation
	}
	return s.repo.AssignForumTopic(ctx, id, assignee)
}
func (s *Service) ForumMembers(ctx context.Context, space, profile, q string) ([]domain.CachedProfile, error) {
	if _, err := s.requireView(ctx, space, profile); err != nil {
		return nil, err
	}
	if s.role(ctx, space, profile) == "" {
		return nil, domain.ErrForbidden
	}
	members, err := s.ident.Members(ctx, space)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.ProfileUUID)
	}
	profiles := s.ident.ProfileMap(ctx, ids)
	out := []domain.CachedProfile{}
	q = strings.ToLower(strings.TrimSpace(q))
	for _, id := range ids {
		p := profiles[id]
		if p == nil || !p.IsActive {
			continue
		}
		code := ""
		if p.Code != nil {
			code = *p.Code
		}
		if strings.Contains(strings.ToLower(p.Name+" "+code), q) {
			out = append(out, *p)
			if len(out) == 20 {
				break
			}
		}
	}
	return out, nil
}
func (s *Service) ForumPreview(ctx context.Context, space, profile, body string) (string, error) {
	if _, err := s.requireView(ctx, space, profile); err != nil {
		return "", err
	}
	if len(body) > 100000 {
		return "", domain.ErrTooLarge
	}
	return s.md.Render(body), nil
}
func (s *Service) validateForumAttachments(ctx context.Context, space, profile string, ids []string) error {
	if len(ids) > s.cfg.MaxAttachments {
		return domain.ErrTooLarge
	}
	if len(ids) > 0 && s.store == nil {
		return domain.ErrValidation
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return domain.ErrValidation
		}
		seen[id] = true
		a, err := s.repo.GetAttachment(ctx, id)
		if err != nil {
			return err
		}
		if a.SpaceUUID != space || a.UploaderProfileUUID != profile || a.Status != "pending" || a.PostID != nil || a.CommentID != nil {
			return domain.ErrForbidden
		}
		size, typ, err := s.store.Stat(ctx, a.ObjectKey)
		if err != nil {
			return fmt.Errorf("%w: chưa tải xong tệp", domain.ErrValidation)
		}
		actual, _, _ := mime.ParseMediaType(typ)
		if size != a.SizeBytes || size > s.cfg.MaxFileBytes || actual != a.ContentType || !s.cfg.MIMEAllowed(actual) {
			return fmt.Errorf("%w: tệp không khớp loại hoặc kích thước đã khai báo", domain.ErrValidation)
		}
	}
	return nil
}

func (s *Service) ForumCapabilities(ctx context.Context, space, profile string) map[string]bool {
	role := s.role(ctx, space, profile)
	out := map[string]bool{"canManage": role == domain.RoleOwner || role == domain.RoleAdmin, "canModerate": canModerate(role), "canPost": false}
	if f, e := s.requireView(ctx, space, profile); e == nil {
		out["canPost"] = s.requirePost(ctx, f, profile) == nil
	}
	return out
}
func (s *Service) ForumMetrics(ctx context.Context) (map[string]int64, error) {
	return s.repo.ForumMetrics(ctx)
}

type ForumQueueItem struct {
	domain.ModerationItem
	Title    string `json:"title"`
	BodyHTML string `json:"bodyHtml"`
	TopicID  string `json:"topicId"`
	PostID   string `json:"postId"`
}

func (s *Service) ForumQueue(ctx context.Context, space, profile string, limit, offset int) ([]ForumQueueItem, error) {
	if err := s.requireModerate(ctx, space, profile); err != nil {
		return nil, err
	}
	rows, err := s.repo.ListForumQueue(ctx, space, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]ForumQueueItem, 0, len(rows))
	for _, row := range rows {
		item := ForumQueueItem{ModerationItem: row}
		var p *domain.Post
		if row.TargetType == "topic" {
			p, err = s.repo.FirstPost(ctx, row.TargetID)
		} else {
			p, err = s.repo.GetPost(ctx, row.TargetID)
		}
		if err != nil {
			return nil, err
		}
		topic, err := s.repo.GetTopic(ctx, p.TopicID)
		if err != nil {
			return nil, err
		}
		item.Title = topic.Title
		item.BodyHTML = p.BodyHTML
		item.TopicID = topic.ID
		item.PostID = p.ID
		out = append(out, item)
	}
	return out, nil
}
