package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"ludiskus/internal/domain"
	"ludiskus/internal/markdown"
)

// --- topics -----------------------------------------------------------------

type TopicInput struct {
	Title                   string   `json:"title"`
	Type                    string   `json:"type"`
	BodyMD                  string   `json:"bodyMd"`
	Tags                    []string `json:"tags"`
	AttachmentIDs           []string `json:"attachmentIds"`
	PersonalSelectionToken  string   `json:"personalSelectionToken,omitempty"`
	SelectionIdempotencyKey string   `json:"selectionIdempotencyKey,omitempty"`
}

// CreateTopic tạo chủ đề + post đầu, áp kiểm duyệt, gắn tag/đính kèm, phát thông
// báo nếu published (docs/04, 08).
func (s *Service) CreateTopic(ctx context.Context, boardID, profileUUID string, in TopicInput) (*domain.Topic, error) {
	board, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	forum, err := s.requireView(ctx, board.SpaceUUID, profileUUID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePost(ctx, forum, profileUUID); err != nil {
		return nil, err
	}
	if board.IsLocked {
		return nil, fmt.Errorf("%w: board đang khoá", domain.ErrForbidden)
	}
	if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.BodyMD) == "" {
		return nil, fmt.Errorf("%w: title và bodyMd là bắt buộc", domain.ErrValidation)
	}
	if !validTopicType(in.Type) {
		in.Type = forum.DefaultTopicType
	}
	if strings.TrimSpace(in.PersonalSelectionToken) != "" {
		imported, err := s.ImportPersonalFileSelection(ctx, profileUUID, board.SpaceUUID, in.PersonalSelectionToken, "topic-attachment", in.SelectionIdempotencyKey)
		if err != nil {
			return nil, err
		}
		in.AttachmentIDs = append(in.AttachmentIDs, imported...)
	}
	if len(in.AttachmentIDs) > s.cfg.MaxAttachments {
		return nil, fmt.Errorf("%w: vượt số lượng đính kèm tối đa", domain.ErrValidation)
	}

	role := s.role(ctx, board.SpaceUUID, profileUUID)
	status, modSource, err := s.decideStatus(ctx, forum, profileUUID, role, in.Title+" "+in.BodyMD)
	if err != nil {
		return nil, err
	}

	slug, err := s.uniqueSlug(ctx, board.SpaceUUID, in.Title)
	if err != nil {
		return nil, err
	}
	html := s.md.Render(in.BodyMD)

	topic, post, err := s.repo.CreateTopicWithPost(ctx,
		domain.Topic{SpaceUUID: board.SpaceUUID, BoardID: boardID, AuthorProfileUUID: profileUUID,
			Title: in.Title, Slug: slug, Type: in.Type, Status: status},
		domain.Post{BodyMD: in.BodyMD, BodyHTML: html, Status: status})
	if err != nil {
		return nil, err
	}

	s.applyTags(ctx, topic.ID, board.SpaceUUID, in.Tags)
	s.attachAndMention(ctx, post, board.SpaceUUID, in.AttachmentIDs, in.BodyMD)

	// Tác giả tự theo dõi topic mình tạo.
	s.repo.EnsureSubscription(ctx, profileUUID, "topic", topic.ID, "authored")

	if status == domain.StatusPublished {
		s.afterPostPublished(ctx, post)
		s.awardTopicPoints(ctx, profileUUID, topic.ID)
	} else {
		item, e := s.repo.CreateModerationItem(ctx, domain.ModerationItem{
			SpaceUUID: board.SpaceUUID, TargetType: "topic", TargetID: topic.ID, Source: modSource,
		})
		if e == nil {
			s.notifyModerators(ctx, board.SpaceUUID, item.ID)
		}
	}
	s.syncInteractionResource(ctx, "topic", topic.ID)
	s.syncInteractionResource(ctx, "post", post.ID)
	return topic, nil
}

func (s *Service) GetTopic(ctx context.Context, topicID, profileUUID string) (*domain.Topic, error) {
	t, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireView(ctx, t.SpaceUUID, profileUUID); err != nil {
		return nil, err
	}
	if t.Status != domain.StatusPublished && !canModerate(s.role(ctx, t.SpaceUUID, profileUUID)) && t.AuthorProfileUUID != profileUUID {
		return nil, domain.ErrNotFound
	}
	s.repo.IncrementTopicView(ctx, topicID)
	s.enrichTopics(ctx, []*domain.Topic{t})
	return t, nil
}

func (s *Service) GetTopicBySlug(ctx context.Context, spaceUUID, slug, profileUUID string) (*domain.Topic, error) {
	if _, err := s.requireView(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	t, err := s.repo.GetTopicBySlug(ctx, spaceUUID, slug)
	if err != nil {
		return nil, err
	}
	s.repo.IncrementTopicView(ctx, t.ID)
	s.enrichTopics(ctx, []*domain.Topic{t})
	return t, nil
}

func (s *Service) ListTopics(ctx context.Context, boardID, profileUUID, sort string, limit, offset int) ([]domain.Topic, error) {
	board, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireView(ctx, board.SpaceUUID, profileUUID); err != nil {
		return nil, err
	}
	topics, err := s.repo.ListTopics(ctx, boardID, sort, limit, offset)
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

func (s *Service) ListSpaceTopics(ctx context.Context, spaceUUID, profileUUID, sort string, limit, offset int) ([]domain.Topic, error) {
	if _, err := s.requireView(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	topics, err := s.repo.ListSpaceTopics(ctx, spaceUUID, sort, limit, offset)
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

func (s *Service) UpdateTopic(ctx context.Context, topicID, profileUUID, title string) (*domain.Topic, error) {
	t, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return nil, err
	}
	if t.AuthorProfileUUID != profileUUID && !canModerate(s.role(ctx, t.SpaceUUID, profileUUID)) {
		return nil, domain.ErrForbidden
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("%w: title là bắt buộc", domain.ErrValidation)
	}
	out, err := s.repo.UpdateTopicMeta(ctx, topicID, title)
	if err == nil {
		s.syncInteractionResource(ctx, "topic", topicID)
	}
	return out, err
}

// TopicAction: lock | unlock | pin | unpin | delete (moderator/tác giả).
func (s *Service) TopicAction(ctx context.Context, topicID, profileUUID, action string) error {
	t, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return err
	}
	mod := canModerate(s.role(ctx, t.SpaceUUID, profileUUID))
	owner := t.AuthorProfileUUID == profileUUID
	switch action {
	case "lock":
		if !mod {
			return domain.ErrForbidden
		}
		return s.repo.SetTopicStatus(ctx, topicID, domain.StatusLocked)
	case "unlock":
		if !mod {
			return domain.ErrForbidden
		}
		return s.repo.SetTopicStatus(ctx, topicID, domain.StatusPublished)
	case "pin":
		if !mod {
			return domain.ErrForbidden
		}
		return s.repo.SetTopicPinned(ctx, topicID, true)
	case "unpin":
		if !mod {
			return domain.ErrForbidden
		}
		return s.repo.SetTopicPinned(ctx, topicID, false)
	case "delete":
		if !mod && !owner {
			return domain.ErrForbidden
		}
		refs, _ := s.repo.InteractionRefsForTopic(ctx, topicID)
		if err := s.repo.SetTopicStatus(ctx, topicID, domain.StatusDeleted); err != nil {
			return err
		}
		s.invalidateInteractionRefs(ctx, refs, "deleted")
		return nil
	}
	return fmt.Errorf("%w: action không hợp lệ", domain.ErrValidation)
}

// --- posts ------------------------------------------------------------------

type ReplyInput struct {
	BodyMD        string   `json:"bodyMd"`
	ReplyToID     *string  `json:"replyToId"`
	AttachmentIDs []string `json:"attachmentIds"`
}

func (s *Service) CreateReply(ctx context.Context, topicID, profileUUID string, in ReplyInput) (*domain.Post, error) {
	t, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return nil, err
	}
	if t.Status == domain.StatusLocked {
		return nil, fmt.Errorf("%w: chủ đề đã khoá", domain.ErrForbidden)
	}
	forum, err := s.requireView(ctx, t.SpaceUUID, profileUUID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePost(ctx, forum, profileUUID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.BodyMD) == "" {
		return nil, fmt.Errorf("%w: bodyMd là bắt buộc", domain.ErrValidation)
	}

	role := s.role(ctx, t.SpaceUUID, profileUUID)
	status, modSource, err := s.decideStatus(ctx, forum, profileUUID, role, in.BodyMD)
	if err != nil {
		return nil, err
	}
	html := s.md.Render(in.BodyMD)
	post, err := s.repo.CreateReply(ctx, domain.Post{
		TopicID: topicID, SpaceUUID: t.SpaceUUID, AuthorProfileUUID: profileUUID,
		ReplyToID: in.ReplyToID, BodyMD: in.BodyMD, BodyHTML: html, Status: status,
	})
	if err != nil {
		return nil, err
	}

	s.attachAndMention(ctx, post, t.SpaceUUID, in.AttachmentIDs, in.BodyMD)
	s.repo.EnsureSubscription(ctx, profileUUID, "topic", topicID, "participated")

	if status == domain.StatusPublished {
		s.afterPostPublished(ctx, post)
	} else {
		item, e := s.repo.CreateModerationItem(ctx, domain.ModerationItem{
			SpaceUUID: t.SpaceUUID, TargetType: "post", TargetID: post.ID, Source: modSource,
		})
		if e == nil {
			s.notifyModerators(ctx, t.SpaceUUID, item.ID)
		}
	}
	s.syncInteractionResource(ctx, interactionResourceType(post), post.ID)
	return post, nil
}

func (s *Service) ListPosts(ctx context.Context, topicID, profileUUID string, limit, offset int) ([]domain.Post, error) {
	t, err := s.repo.GetTopic(ctx, topicID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireView(ctx, t.SpaceUUID, profileUUID); err != nil {
		return nil, err
	}
	posts, err := s.repo.ListPosts(ctx, topicID, limit, offset)
	if err != nil {
		return nil, err
	}
	s.enrichPosts(ctx, t.SpaceUUID, posts)
	return posts, nil
}

func (s *Service) UpdatePost(ctx context.Context, postID, profileUUID, bodyMD string) (*domain.Post, error) {
	p, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if p.AuthorProfileUUID != profileUUID && !canModerate(s.role(ctx, p.SpaceUUID, profileUUID)) {
		return nil, domain.ErrForbidden
	}
	if strings.TrimSpace(bodyMD) == "" {
		return nil, fmt.Errorf("%w: bodyMd là bắt buộc", domain.ErrValidation)
	}
	html := s.md.Render(bodyMD)
	out, err := s.repo.UpdatePost(ctx, postID, bodyMD, html)
	if err == nil {
		s.syncInteractionResource(ctx, interactionResourceType(out), postID)
	}
	return out, err
}

func (s *Service) DeletePost(ctx context.Context, postID, profileUUID string) error {
	p, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return err
	}
	if p.AuthorProfileUUID != profileUUID && !canModerate(s.role(ctx, p.SpaceUUID, profileUUID)) {
		return domain.ErrForbidden
	}
	if err := s.repo.SetPostStatus(ctx, postID, domain.StatusDeleted); err != nil {
		return err
	}
	s.invalidateInteractionResource(ctx, interactionResourceType(p), postID, "deleted")
	return nil
}

// MarkAnswer đánh dấu post là câu trả lời (chỉ tác giả topic) — Q&A (docs/03).
func (s *Service) MarkAnswer(ctx context.Context, postID, profileUUID string) error {
	p, err := s.repo.GetPost(ctx, postID)
	if err != nil {
		return err
	}
	t, err := s.repo.GetTopic(ctx, p.TopicID)
	if err != nil {
		return err
	}
	if t.AuthorProfileUUID != profileUUID && !canModerate(s.role(ctx, t.SpaceUUID, profileUUID)) {
		return domain.ErrForbidden
	}
	if err := s.repo.SetTopicAnswer(ctx, t.ID, postID); err != nil {
		return err
	}
	s.notifyAnswer(ctx, t, postID)
	return nil
}

// --- subscriptions ----------------------------------------------------------

func (s *Service) ListSubscriptions(ctx context.Context, profileUUID string) ([]domain.Subscription, error) {
	if profileUUID == "" {
		return nil, domain.ErrUnauthorized
	}
	return s.repo.ListSubscriptions(ctx, profileUUID)
}

type SubscriptionInput struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	Muted      bool   `json:"muted"`
}

func (s *Service) Subscribe(ctx context.Context, profileUUID string, in SubscriptionInput) error {
	if profileUUID == "" {
		return domain.ErrUnauthorized
	}
	switch in.TargetType {
	case "space", "board", "topic":
	default:
		return fmt.Errorf("%w: targetType không hợp lệ", domain.ErrValidation)
	}
	return s.repo.UpsertSubscription(ctx, domain.Subscription{
		ProfileUUID: profileUUID, TargetType: in.TargetType, TargetID: in.TargetID,
		Reason: "manual", Muted: in.Muted,
	})
}

// --- tags -------------------------------------------------------------------

func (s *Service) applyTags(ctx context.Context, topicID, spaceUUID string, tags []string) {
	for _, raw := range tags {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		slug := slugify(name)
		if slug == "" {
			continue
		}
		tag, err := s.repo.UpsertTag(ctx, spaceUUID, slug, name)
		if err != nil {
			continue
		}
		s.repo.AttachTag(ctx, topicID, tag.ID)
	}
}

func (s *Service) ListTags(ctx context.Context, spaceUUID, profileUUID, query string, limit int) ([]domain.Tag, error) {
	if _, err := s.requireView(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	return s.repo.ListTags(ctx, spaceUUID, query, limit)
}

// --- enrichment & helpers ---------------------------------------------------

func (s *Service) attachAndMention(ctx context.Context, post *domain.Post, spaceUUID string, attachmentIDs []string, bodyMD string) {
	if s.store != nil && len(attachmentIDs) > 0 {
		s.repo.AttachToPost(ctx, attachmentIDs, post.ID, spaceUUID)
	}
	handles := markdown.Mentions(bodyMD)
	uuids := []string{}
	for _, h := range handles {
		prof, err := s.ident.ProfileByCode(ctx, h)
		if err != nil || prof == nil {
			continue
		}
		if !s.ident.IsMember(ctx, spaceUUID, prof.ProfileUUID) {
			continue // chỉ mention thành viên Space (docs/05 §5.5)
		}
		uuids = append(uuids, prof.ProfileUUID)
	}
	if len(uuids) > 0 {
		s.repo.AddMentions(ctx, post.ID, uuids)
	}
}

func (s *Service) enrichTopics(ctx context.Context, topics []*domain.Topic) {
	if len(topics) == 0 {
		return
	}
	ids := make([]string, 0, len(topics))
	authors := make([]string, 0, len(topics))
	for _, t := range topics {
		ids = append(ids, t.ID)
		authors = append(authors, t.AuthorProfileUUID)
	}
	pm := s.ident.ProfileMap(ctx, authors)
	tagMap, _ := s.repo.TagsForTopics(ctx, ids)
	for _, t := range topics {
		t.Author = pm[t.AuthorProfileUUID]
		t.Tags = tagMap[t.ID]
	}
}

func (s *Service) enrichPosts(ctx context.Context, spaceUUID string, posts []domain.Post) {
	if len(posts) == 0 {
		return
	}
	ids := make([]string, 0, len(posts))
	authors := make([]string, 0, len(posts))
	for i := range posts {
		ids = append(ids, posts[i].ID)
		authors = append(authors, posts[i].AuthorProfileUUID)
	}
	pm := s.ident.ProfileMap(ctx, authors)
	attMap, _ := s.repo.AttachmentsForPosts(ctx, ids)
	public := s.spaceIsPublic(ctx, spaceUUID)
	for i := range posts {
		posts[i].Author = pm[posts[i].AuthorProfileUUID]
		atts := attMap[posts[i].ID]
		for j := range atts {
			if public {
				atts[j].URL = s.store.PublicURL(atts[j].ObjectKey)
			}
		}
		posts[i].Attachments = atts
	}
}

func (s *Service) spaceIsPublic(ctx context.Context, spaceUUID string) bool {
	if s.store == nil {
		return false
	}
	sp, err := s.ident.Space(ctx, spaceUUID)
	return err == nil && sp.IsPublic
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = removeDiacritics(s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 80 {
		s = s[:80]
		s = strings.Trim(s, "-")
	}
	return s
}

func (s *Service) uniqueSlug(ctx context.Context, spaceUUID, title string) (string, error) {
	base := slugify(title)
	if base == "" {
		base = "chu-de"
	}
	slug := base
	for i := 2; i < 1000; i++ {
		exists, err := s.repo.SlugExists(ctx, spaceUUID, slug)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
	return fmt.Sprintf("%s-%s", base, randSuffix()), nil
}
