package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"ludiskus/internal/domain"
	"ludiskus/internal/notify"
	"ludiskus/internal/repository"
)

func (s *Service) afterCommentPublished(ctx context.Context, t *domain.CommentTarget, c *domain.Comment, p domain.CommentPolicy, mentions []string, bufferAlreadyWritten bool) {
	// Notifications (including mentions) are already durable in the create/approve transaction.
	rows, _ := s.commentNotifyRows(ctx, t, c, p, mentions)
	if s.redis != nil {
		for _, item := range rows {
			_ = s.redis.Del(ctx, "cmt:unread:"+item.RecipientProfileUUID).Err()
		}
	}
}

func (s *Service) commentNotifyRows(ctx context.Context, t *domain.CommentTarget, c *domain.Comment, p domain.CommentPolicy, mentions []string) ([]repository.CommentNotifyInsert, error) {
	author := ""
	if c.AuthorProfileUUID != nil {
		author = *c.AuthorProfileUUID
	}
	mentionSet := map[string]bool{}
	for _, u := range mentions {
		mentionSet[u] = true
	}
	recipients := map[string]string{}
	if p.Notify.Owner && t.OwnerType != nil && *t.OwnerType == "profile" && t.OwnerID != nil {
		recipients[*t.OwnerID] = "ludiskus.comment.created"
	}
	if c.ParentID != nil {
		parent, err := s.repo.GetComment(ctx, *c.ParentID)
		if err != nil {
			return nil, err
		}
		if parent.AuthorProfileUUID != nil {
			recipients[*parent.AuthorProfileUUID] = "ludiskus.comment.replied"
		}
	}
	if p.Notify.Participants {
		participants, err := s.repo.ListCommentParticipants(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		{
			for _, part := range participants {
				if !part.Muted && part.Reason != "mentioned" {
					recipients[part.ProfileUUID] = "ludiskus.comment.replied"
				}
			}
		}
	}
	flush := time.Now().Add(s.cfg.CommentNotifyDebounce)
	out := make([]repository.CommentNotifyInsert, 0, len(recipients))
	for u, event := range recipients {
		if u == author || mentionSet[u] {
			continue
		}
		out = append(out, repository.CommentNotifyInsert{EventType: event, RecipientProfileUUID: u, ActorProfileUUID: c.AuthorProfileUUID, FlushAfter: flush})
	}
	for u := range mentionSet {
		if u != author {
			out = append(out, repository.CommentNotifyInsert{EventType: "ludiskus.comment.mentioned", RecipientProfileUUID: u, ActorProfileUUID: c.AuthorProfileUUID, FlushAfter: time.Now()})
		}
	}
	return out, nil
}

func commentURL(t *domain.CommentTarget, id string) string {
	if t.CanonicalPath != "" {
		return t.CanonicalPath + "#comment-" + id
	}
	return "/ludiskus/c/" + id
}

func (s *Service) FlushCommentNotify(ctx context.Context) (int, error) {
	rows, err := s.repo.ClaimDueCommentNotify(ctx, 200)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	type group struct {
		event, recipient, target, token string
		latestAt                        time.Time
		rows                            []int64
		comments                        []string
		latest                          string
		actor                           *string
	}
	groups := map[string]*group{}
	for _, row := range rows {
		k := row.EventType + ":" + row.RecipientProfileUUID + ":" + row.TargetID
		g := groups[k]
		if g == nil {
			g = &group{event: row.EventType, recipient: row.RecipientProfileUUID, target: row.TargetID, token: row.ClaimToken}
			groups[k] = g
		}
		g.rows = append(g.rows, row.ID)
		g.comments = append(g.comments, row.CommentID)
		if g.latest == "" || row.OccurredAt.After(g.latestAt) {
			g.latestAt = row.OccurredAt
			g.latest = row.CommentID
			g.actor = row.ActorProfileUUID
		}
	}
	processed := 0
	for _, g := range groups {
		t, e := s.repo.GetCommentTargetByID(ctx, g.target)
		if e != nil {
			return processed, e
		}
		if _, _, e = s.ensureCommentReadable(ctx, t.Ref(), g.recipient); e != nil {
			if !errors.Is(e, domain.ErrForbidden) && !errors.Is(e, domain.ErrNotFound) && !errors.Is(e, domain.ErrResourceGone) {
				return processed, e
			}
			if e = s.repo.CompleteCommentNotify(ctx, g.rows, g.token, "", "", nil, 0); e != nil {
				return processed, e
			}
			processed += len(g.rows)
			continue
		}
		latest, e := s.repo.GetComment(ctx, g.latest)
		if e != nil {
			return processed, e
		}
		if latest.Status != domain.CommentPublished {
			if e = s.repo.CompleteCommentNotify(ctx, g.rows, g.token, "", "", nil, 0); e != nil {
				return processed, e
			}
			processed += len(g.rows)
			continue
		}
		actorName := ""
		if g.actor != nil {
			if profile, _ := s.ident.Profile(ctx, *g.actor); profile != nil {
				actorName = profile.Name
			}
		}
		data, _ := json.Marshal(map[string]any{"actor": actorName, "count": len(g.comments), "others": max(0, len(g.comments)-1), "resourceTitle": t.Title, "excerpt": excerptOf(latest.BodyMD), "url": commentURL(t, latest.ID)})
		sort.Slice(g.rows, func(i, j int) bool { return g.rows[i] < g.rows[j] })
		key := fmt.Sprintf("cmt:batch:%x", sha256.Sum256([]byte(fmt.Sprint(g.rows))))
		payload, e := json.Marshal(notify.Event{EventType: g.event, IdempotencyKey: &key, Data: data, Recipients: []notify.Recipient{{ProfileUUID: g.recipient}}})
		if e != nil {
			return processed, e
		}
		if e = s.repo.CompleteCommentNotify(ctx, g.rows, g.token, g.event, key, payload, s.cfg.OutboxMaxAttempts); e != nil {
			return processed, e
		}
		processed += len(g.rows)
	}
	return processed, nil
}

type CommentInboxItem struct {
	Target domain.CommentTarget `json:"target"`
	Unread bool                 `json:"unread"`
}

func (s *Service) CommentInbox(ctx context.Context, profileUUID string, unread bool) ([]CommentInboxItem, error) {
	targets, err := s.repo.ListCommentInboxTargets(ctx, profileUUID, unread, 100)
	if err != nil {
		return nil, err
	}
	out := make([]CommentInboxItem, 0, len(targets))
	for _, t := range targets {
		if _, _, e := s.ensureCommentReadable(ctx, t.Ref(), profileUUID); e == nil {
			p, _ := s.repo.GetCommentParticipant(ctx, t.ID, profileUUID)
			isUnread := p != nil && t.LastCommentAt != nil && (p.LastReadAt == nil || t.LastCommentAt.After(*p.LastReadAt))
			out = append(out, CommentInboxItem{Target: t, Unread: isUnread})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Target.LastCommentAt, out[j].Target.LastCommentAt
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.After(*b)
	})
	return out, nil
}

func (s *Service) CommentUnreadCount(ctx context.Context, profileUUID string) (int, error) {
	if s.redis != nil {
		if n, err := s.redis.Get(ctx, "cmt:unread:"+profileUUID).Int(); err == nil {
			return n, nil
		}
	}
	n, err := s.repo.CommentUnreadCount(ctx, profileUUID)
	if err == nil && s.redis != nil {
		_ = s.redis.Set(ctx, "cmt:unread:"+profileUUID, n, 30*time.Second).Err()
	}
	return n, err
}
