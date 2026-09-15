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
	if t.Status != domain.StatusPublished && t.Status != domain.StatusLocked && t.AuthorProfileUUID != profile && !s.canModerateBoard(ctx, t.BoardID, profile) {
		return domain.ErrNotFound
	}
	return nil
}
func (s *Service) forumTopicCapabilities(ctx context.Context, t *domain.Topic, profile string) {
	t.CanModerate = s.canModerateBoard(ctx, t.BoardID, profile)
	t.CanManage = t.CanModerate || t.AuthorProfileUUID == profile
	b, err := s.repo.GetBoard(ctx, t.BoardID)
	f, ferr := s.requireView(ctx, t.SpaceUUID, profile)
	if err == nil && ferr == nil {
		t.BoardKind = b.Kind
		canReply, _ := s.canReplyBoard(ctx, t, b, f, profile)
		t.CanReply = canReply
	} else {
		t.CanReply = false
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
	if !s.canModerateBoard(ctx, t.BoardID, profile) {
		return domain.ErrForbidden
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
	return s.searchSpaceMembers(ctx, space, q, 20)
}

// searchSpaceMembers tìm thành viên Space để gợi ý @mention. Lọc và cắt diễn ra
// trong SQL (một lượt), không phải nạp từng Profile rồi lọc trong Go.
func (s *Service) searchSpaceMembers(ctx context.Context, space, q string, limit int) ([]domain.CachedProfile, error) {
	// Vẫn gọi Members trước: nó là nơi đồng bộ space_member_cache từ HipCore khi
	// cache nguội. Bỏ bước này thì Space vừa tạo sẽ không gợi ý được ai.
	if _, err := s.ident.Members(ctx, space); err != nil {
		return nil, err
	}
	out, err := s.repo.SearchSpaceMemberProfiles(ctx, space, q, limit)
	if err != nil {
		return nil, err
	}
	if len(out) >= limit {
		return out, nil
	}
	// Bù phần chênh: thành viên chưa có hàng profile_cache (mới vào Space giữa
	// hai lần full-sync của worker) bị truy vấn join bỏ qua, trong khi đường cũ
	// nạp lười từ HipCore nên vẫn thấy. Nạp lười đúng phần thiếu, có chặn trên.
	missing, err := s.repo.SpaceMembersMissingProfile(ctx, space, limit-len(out))
	if err != nil || len(missing) == 0 {
		return out, nil
	}
	seen := make(map[string]bool, len(out))
	for _, p := range out {
		seen[p.ProfileUUID] = true
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	for _, uuid := range missing {
		profile, e := s.ident.Profile(ctx, uuid)
		if e != nil || profile == nil || !profile.IsActive || seen[profile.ProfileUUID] {
			continue
		}
		if !matchProfile(*profile, needle) {
			continue
		}
		out = append(out, *profile)
		if len(out) == limit {
			break
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
	return s.renderBody(ctx, space, body), nil
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
		size, typ, detected, checksum, err := s.store.Inspect(ctx, a.ObjectKey, s.cfg.MaxFileBytes)
		if err != nil {
			return fmt.Errorf("%w: chưa tải xong tệp", domain.ErrValidation)
		}
		actual, _, _ := mime.ParseMediaType(typ)
		detected, _, _ = mime.ParseMediaType(detected)
		if size != a.SizeBytes || size > s.cfg.MaxFileBytes || actual != a.ContentType || detected != a.ContentType || !s.cfg.MIMEAllowed(actual) {
			return fmt.Errorf("%w: tệp không khớp loại hoặc kích thước đã khai báo", domain.ErrValidation)
		}
		if a.FinalizedAt == nil {
			if _, err := s.repo.FinalizeAttachment(ctx, a.ID, checksum); err != nil {
				return err
			}
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

func (s *Service) ForumQueue(ctx context.Context, space, profile, boardFilter string, limit, offset int) ([]ForumQueueItem, error) {
	allowedBoards, err := s.allowedModerationBoards(ctx, space, profile, boardFilter)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListForumQueue(ctx, space, allowedBoards, limit, offset)
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
