package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"ludiskus/internal/domain"
)

func (s *Service) PublicCommentThread(ctx context.Context, ref domain.ResourceRef) (*CommentThread, error) {
	key := "cmt:pub:" + ref.String() + ":thread"
	if s.redis != nil {
		if raw, err := s.redis.Get(ctx, key).Bytes(); err == nil {
			var cached CommentThread
			if json.Unmarshal(raw, &cached) == nil {
				return &cached, nil
			}
		}
	}
	out, err := s.CommentThread(ctx, ref, "")
	if err != nil {
		slog.Warn("public comment thread unavailable", "ref", ref.String(), "err", err)
		return nil, domain.ErrNotFound
	}
	p, err := s.commentPolicy(ctx, out.Target)
	if err != nil || out.Target.State != "active" || out.Target.Visibility != "public" || !p.PublicRead || out.Target.ThreadState == "hidden" {
		return nil, domain.ErrNotFound
	}
	out.Capabilities.CanComment = false
	out.Capabilities.CanReply = false
	out.Capabilities.CanAttach = false
	out.Capabilities.CanMention = false
	out.Capabilities.CanPin = false
	out.Capabilities.CanModerate = false
	out.Capabilities.Interaction = domain.PolicyInteraction{}
	out.Capabilities.Reasons = map[string]string{"canComment": "not_authenticated"}
	if s.redis != nil {
		if raw, marshalErr := json.Marshal(out); marshalErr == nil {
			_ = s.redis.Set(ctx, key, raw, s.cfg.CommentSummaryTTL).Err()
		}
	}
	return out, nil
}

func (s *Service) PublicCommentList(ctx context.Context, ref domain.ResourceRef, sort, cursor string, limit, preview int) (*CommentPage, error) {
	if _, err := s.PublicCommentThread(ctx, ref); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("cmt:pub:%s:list:%s:%s:%d:%d", ref.String(), sort, cursor, limit, preview)
	if s.redis != nil {
		if raw, err := s.redis.Get(ctx, key).Bytes(); err == nil {
			var cached CommentPage
			if json.Unmarshal(raw, &cached) == nil {
				return &cached, nil
			}
		}
	}
	page, err := s.ListComments(ctx, ref, "", sort, cursor, limit, min(preview, 3))
	if err != nil {
		return nil, err
	}
	stripPublicComments(page.Data)
	if s.redis != nil {
		if raw, marshalErr := json.Marshal(page); marshalErr == nil {
			_ = s.redis.Set(ctx, key, raw, s.cfg.CommentSummaryTTL).Err()
		}
	}
	return page, nil
}
func (s *Service) PublicCommentReplies(ctx context.Context, id, cursor string, limit int) (*CommentPage, error) {
	c, t, err := s.GetComment(ctx, id, "")
	if err != nil {
		return nil, domain.ErrNotFound
	}
	if _, err = s.PublicCommentThread(ctx, t.Ref()); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("cmt:pub:%s:replies:%s:%s:%d", t.Ref().String(), c.RootID, cursor, limit)
	if s.redis != nil {
		if raw, cacheErr := s.redis.Get(ctx, key).Bytes(); cacheErr == nil {
			var cached CommentPage
			if json.Unmarshal(raw, &cached) == nil {
				return &cached, nil
			}
		}
	}
	page, err := s.CommentReplies(ctx, c.RootID, "", cursor, limit)
	if err != nil {
		return nil, err
	}
	stripPublicComments(page.Data)
	if s.redis != nil {
		if raw, marshalErr := json.Marshal(page); marshalErr == nil {
			_ = s.redis.Set(ctx, key, raw, s.cfg.CommentSummaryTTL).Err()
		}
	}
	return page, nil
}
func stripPublicComments(items []domain.Comment) {
	for i := range items {
		items[i].BodyMD = ""
		items[i].AuthorProfileUUID = nil
		items[i].Mentions = nil
		items[i].CanEdit = false
		items[i].CanDelete = false
		items[i].CanModerate = false
		stripPublicComments(items[i].PreviewReplies)
	}
}
