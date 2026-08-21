package service

import (
	"context"
	"encoding/json"
	"fmt"

	"ludiskus/internal/domain"
	"ludiskus/internal/notify"
)

// enqueueEvent ghi một event vào outbox (worker sẽ đẩy sang lunoti — docs/08).
func (s *Service) enqueueEvent(ctx context.Context, ev notify.Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.repo.EnqueueOutbox(ctx, ev.EventType, ev.IdempotencyKey, payload, s.cfg.OutboxMaxAttempts)
}

func recipientsOf(uuids []string, exclude string) []notify.Recipient {
	out := []notify.Recipient{}
	seen := map[string]bool{exclude: true}
	for _, u := range uuids {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, notify.Recipient{ProfileUUID: u})
	}
	return out
}

func ptr(s string) *string { return &s }

// topicURL dựng deep-link cho thông báo.
func (s *Service) topicURL(space *domain.CachedSpace, t *domain.Topic, postID string) string {
	scode := t.SpaceUUID
	if space != nil && space.Code != nil && *space.Code != "" {
		scode = *space.Code
	}
	u := fmt.Sprintf("/ludiskus/s/%s/t/%s", scode, t.Slug)
	if postID != "" {
		u += "#post-" + postID
	}
	return u
}

func spaceName(space *domain.CachedSpace) string {
	if space != nil {
		return space.Name
	}
	return ""
}

// afterPostPublished phát thông báo reply + mention cho một post đã published.
func (s *Service) afterPostPublished(ctx context.Context, p *domain.Post) {
	t, err := s.repo.GetTopic(ctx, p.TopicID)
	if err != nil {
		return
	}
	space, _ := s.ident.Space(ctx, p.SpaceUUID)
	actor, _ := s.ident.Profile(ctx, p.AuthorProfileUUID)
	actorName := p.AuthorProfileUUID
	if actor != nil {
		actorName = actor.Name
	}
	url := s.topicURL(space, t, p.ID)

	// Reply: chỉ với trả lời (không phải post đầu).
	if !p.IsFirst {
		subs, _ := s.repo.SubscribersForTopic(ctx, t.ID, t.BoardID, t.SpaceUUID)
		recips := recipientsOf(subs, p.AuthorProfileUUID)
		if len(recips) > 0 {
			data, _ := json.Marshal(map[string]any{
				"actor": actorName, "space": spaceName(space), "topic": t.Title, "url": url,
			})
			s.enqueueEvent(ctx, notify.Event{
				EventType:      "ludiskus.topic.replied",
				IdempotencyKey: ptr("reply:" + p.ID),
				Data:           data,
				Recipients:     recips,
			})
		}
	}

	// Mention.
	mentions, _ := s.repo.MentionsForPost(ctx, p.ID)
	mrecips := recipientsOf(mentions, p.AuthorProfileUUID)
	if len(mrecips) > 0 {
		data, _ := json.Marshal(map[string]any{
			"actor": actorName, "space": spaceName(space), "topic": t.Title,
			"excerpt": excerptOf(p.BodyMD), "url": url,
		})
		s.enqueueEvent(ctx, notify.Event{
			EventType:      "ludiskus.post.mentioned",
			IdempotencyKey: ptr("mention:" + p.ID),
			Data:           data,
			Recipients:     mrecips,
		})
	}
}

// notifyAnswer báo người hỏi khi câu hỏi có câu trả lời được chấp nhận.
func (s *Service) notifyAnswer(ctx context.Context, t *domain.Topic, answerPostID string) {
	space, _ := s.ident.Space(ctx, t.SpaceUUID)
	data, _ := json.Marshal(map[string]any{
		"space": spaceName(space), "topic": t.Title, "url": s.topicURL(space, t, answerPostID),
	})
	s.enqueueEvent(ctx, notify.Event{
		EventType:      "ludiskus.topic.answered",
		IdempotencyKey: ptr("answered:" + t.ID + ":" + answerPostID),
		Data:           data,
		Recipients:     []notify.Recipient{{ProfileUUID: t.AuthorProfileUUID}},
	})
}

// notifyModerators báo các moderator của Space có bài chờ duyệt.
func (s *Service) notifyModerators(ctx context.Context, spaceUUID, itemID string) {
	mods := s.moderatorUUIDs(ctx, spaceUUID)
	if len(mods) == 0 {
		return
	}
	space, _ := s.ident.Space(ctx, spaceUUID)
	data, _ := json.Marshal(map[string]any{
		"space": spaceName(space), "url": fmt.Sprintf("/ludiskus/s/%s/moderation", spaceUUID),
	})
	s.enqueueEvent(ctx, notify.Event{
		EventType:      "ludiskus.moderation.pending",
		IdempotencyKey: ptr("modpending:" + itemID),
		Data:           data,
		Recipients:     recipientsOf(mods, ""),
	})
}

func (s *Service) notifyCommentPending(ctx context.Context, target *domain.CommentTarget, comment *domain.Comment) {
	set := map[string]bool{}
	if target.OwnerType != nil && *target.OwnerType == "profile" && target.OwnerID != nil {
		set[*target.OwnerID] = true
	}
	if target.SpaceUUID != nil {
		for _, profileUUID := range s.moderatorUUIDs(ctx, *target.SpaceUUID) {
			set[profileUUID] = true
		}
	}
	if comment.AuthorProfileUUID != nil {
		delete(set, *comment.AuthorProfileUUID)
	}
	recipients := make([]notify.Recipient, 0, len(set))
	for profileUUID := range set {
		recipients = append(recipients, notify.Recipient{ProfileUUID: profileUUID})
	}
	if len(recipients) == 0 {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"count": 1, "spaceName": target.Title, "url": "/ludiskus/c/" + comment.ID,
	})
	s.enqueueEvent(ctx, notify.Event{
		EventType:      "ludiskus.comment.pending",
		IdempotencyKey: ptr("comment-pending:" + comment.ID),
		Data:           data,
		Recipients:     recipients,
	})
}

// notifyModerationDecided báo tác giả bài đã được duyệt/từ chối.
func (s *Service) notifyModerationDecided(ctx context.Context, item *domain.ModerationItem, decision string, note *string) {
	author, ok := s.targetAuthor(ctx, item.TargetType, item.TargetID)
	if !ok || author == "" {
		return
	}
	noteStr := ""
	if note != nil {
		noteStr = *note
	}
	eventType := "ludiskus.moderation.decided"
	url := fmt.Sprintf("/ludiskus/s/%s", item.SpaceUUID)
	keyPrefix := "moddecided:"
	if item.TargetType == "comment" {
		eventType = "ludiskus.comment.moderated"
		url = "/ludiskus/c/" + item.TargetID
		keyPrefix = "comment-moderated:"
	}
	data, _ := json.Marshal(map[string]any{
		"decision": decision, "note": noteStr,
		"url": url,
	})
	s.enqueueEvent(ctx, notify.Event{
		EventType:      eventType,
		IdempotencyKey: ptr(fmt.Sprintf("%s%s:%s", keyPrefix, item.ID, item.State)),
		Data:           data,
		Recipients:     []notify.Recipient{{ProfileUUID: author}},
	})
}

// moderatorUUIDs gộp owner/admin (member cache) + space_moderators.
func (s *Service) moderatorUUIDs(ctx context.Context, spaceUUID string) []string {
	set := map[string]bool{}
	out := []string{}
	add := func(u string) {
		if u != "" && !set[u] {
			set[u] = true
			out = append(out, u)
		}
	}
	if members, err := s.ident.Members(ctx, spaceUUID); err == nil {
		for _, m := range members {
			if m.Role == domain.RoleOwner || m.Role == domain.RoleAdmin {
				add(m.ProfileUUID)
			}
		}
	}
	if mods, err := s.repo.ListModerators(ctx, spaceUUID); err == nil {
		for _, m := range mods {
			add(m)
		}
	}
	return out
}

func (s *Service) targetAuthor(ctx context.Context, targetType, targetID string) (string, bool) {
	switch targetType {
	case "topic":
		if t, err := s.repo.GetTopic(ctx, targetID); err == nil {
			return t.AuthorProfileUUID, true
		}
	case "post":
		if p, err := s.repo.GetPost(ctx, targetID); err == nil {
			return p.AuthorProfileUUID, true
		}
	case "comment":
		if c, err := s.repo.GetComment(ctx, targetID); err == nil && c.AuthorProfileUUID != nil {
			return *c.AuthorProfileUUID, true
		}
	}
	return "", false
}
