package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"ludiskus/internal/domain"
	"ludiskus/internal/repository"
)

func commentBodyHash(body string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(body), " "))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func (s *Service) checkCommentRate(ctx context.Context, profile, targetID, bodyHash string, p domain.CommentPolicy) error {
	if s.redis == nil {
		return nil
	}
	now := time.Now().UTC()
	if override, err := s.redis.Get(ctx, "cmt:rl:override:"+profile).Result(); err == nil && override == "throttled" {
		p.RateLimit.PerMinute = positiveMin(p.RateLimit.PerMinute, 1)
		p.RateLimit.PerHour = positiveMin(p.RateLimit.PerHour, 10)
		p.RateLimit.PerTargetPerHour = positiveMin(p.RateLimit.PerTargetPerHour, 5)
	}
	keys := []struct {
		k     string
		limit int
		ttl   time.Duration
	}{
		{"cmt:rl:m:" + profile + ":" + now.Format("200601021504"), p.RateLimit.PerMinute, time.Minute},
		{"cmt:rl:h:" + profile + ":" + now.Format("2006010215"), p.RateLimit.PerHour, time.Hour},
		{"cmt:rl:t:" + profile + ":" + targetID + ":" + now.Format("2006010215"), p.RateLimit.PerTargetPerHour, time.Hour},
	}
	for _, item := range keys {
		n, err := s.redis.Incr(ctx, item.k).Result()
		if err != nil {
			slog.Warn("comment rate-limit unavailable", "err", err)
			return nil
		}
		if n == 1 {
			_ = s.redis.Expire(ctx, item.k, item.ttl).Err()
		}
		if item.limit > 0 && n > int64(item.limit) {
			return domain.ErrRateLimited
		}
	}
	dup := "cmt:dup:" + profile + ":" + bodyHash
	ok, err := s.redis.SetNX(ctx, dup, "1", time.Minute).Result()
	if err != nil {
		slog.Warn("comment duplicate guard unavailable", "err", err)
		return nil
	}
	if !ok {
		return domain.ErrDuplicateComment
	}
	return nil
}

func positiveMin(current, maximum int) int {
	if current <= 0 || current > maximum {
		return maximum
	}
	return current
}

func (s *Service) DetectCommentAbuse(ctx context.Context) (int64, error) {
	return s.repo.DetectCommentAbuse(ctx)
}

func (s *Service) SyncCommentScores(ctx context.Context) (int64, error) {
	if !s.hipt.Enabled() {
		return 0, nil
	}
	var changed int64
	cursor := ""
	for {
		items, next, err := s.hipt.InteractionAggregates(ctx, time.Unix(0, 0).UTC().Format(time.RFC3339), cursor)
		if err != nil {
			return changed, err
		}
		scores := make(map[string]int64, len(items))
		for _, item := range items {
			if item.Ref.Type == "comment" && item.Ref.ID != "" {
				scores[item.Ref.ID] = item.Counts.Like
			}
		}
		n, err := s.repo.UpdateCommentScores(ctx, scores)
		changed += n
		if err != nil {
			return changed, err
		}
		if next == "" {
			return changed, nil
		}
		cursor = next
	}
}

func (s *Service) AdminCommentAbuseFlags(ctx context.Context, state string) ([]repository.CommentAbuseFlag, error) {
	allowed := map[string]bool{"": true, "open": true, "dismissed": true, "throttled": true, "pre_moderated": true}
	if !allowed[state] {
		return nil, fmt.Errorf("%w: trạng thái cờ abuse không hợp lệ", domain.ErrValidation)
	}
	return s.repo.ListCommentAbuseFlags(ctx, state, 100)
}

func (s *Service) AdminDecideCommentAbuseFlag(ctx context.Context, id, state, actor, note string) (*repository.CommentAbuseFlag, error) {
	if state != "dismissed" && state != "throttled" && state != "pre_moderated" {
		return nil, fmt.Errorf("%w: hành động cờ abuse không hợp lệ", domain.ErrValidation)
	}
	flag, err := s.repo.DecideCommentAbuseFlag(ctx, id, state, actor, strings.TrimSpace(note))
	if err != nil {
		return nil, err
	}
	if s.redis != nil {
		key := "cmt:rl:override:" + flag.ProfileUUID
		if state == "dismissed" {
			err = s.redis.Del(ctx, key).Err()
		} else {
			err = s.redis.Set(ctx, key, state, 30*24*time.Hour).Err()
		}
		if err != nil {
			slog.Warn("comment abuse override unavailable", "profile", flag.ProfileUUID, "err", err)
		}
	}
	return flag, nil
}
