package service

import (
	"context"
	"encoding/json"
	"ludiskus/internal/domain"
	"ludiskus/internal/notify"
	"ludiskus/internal/repository"
)

// The queue decision and comment publication share a transaction. A failed
// publication remains pending and may be retried; resource owners are moderators.
func (s *Service) decideCommentModeration(ctx context.Context, item *domain.ModerationItem, actor string, approve bool, note *string) error {
	c, e := s.repo.GetComment(ctx, item.TargetID)
	if e != nil {
		return e
	}
	target, e := s.repo.GetCommentTargetByID(ctx, c.TargetID)
	if e != nil {
		return e
	}
	target, policy, e := s.ensureCommentReadable(ctx, target.Ref(), actor)
	if e != nil {
		return e
	}
	if !s.commentCapabilities(ctx, target, actor, policy).CanModerate {
		return domain.ErrForbidden
	}
	status, reason := "rejected", "rejected"
	if note != nil {
		reason = *note
	}
	var notifications []repository.CommentNotifyInsert
	var mentions []string
	if approve {
		status, reason = "published", "approved"
		mentions, e = s.repo.CommentMentions(ctx, c.ID)
		if e != nil {
			return e
		}
		notifications, e = s.commentNotifyRows(ctx, target, c, policy, mentions)
		if e != nil {
			return e
		}
	}
	decision := "từ chối"
	if approve {
		decision = "duyệt"
	}
	key := "comment-moderated:" + item.ID + ":" + status
	var payload []byte
	if c.AuthorProfileUUID != nil {
		data, _ := json.Marshal(map[string]any{"decision": decision, "note": reason, "url": commentURL(target, c.ID)})
		payload, e = json.Marshal(notify.Event{EventType: "ludiskus.comment.moderated", IdempotencyKey: &key, Data: data, Recipients: []notify.Recipient{{ProfileUUID: *c.AuthorProfileUUID}}})
		if e != nil {
			return e
		}
	}
	out, e := s.repo.DecideCommentModeration(ctx, item.ID, c.ID, status, actor, reason, notifications, payload, key, s.cfg.OutboxMaxAttempts)
	if e != nil {
		return e
	}
	s.clearCommentCaches(ctx, target.Ref())
	if approve {
		s.afterCommentPublished(ctx, target, out, policy, mentions, true)
		s.syncInteractionResource(ctx, "comment", c.ID)
	} else {
		s.invalidateInteractionResource(ctx, "comment", c.ID, "visibility")
	}
	return nil
}
