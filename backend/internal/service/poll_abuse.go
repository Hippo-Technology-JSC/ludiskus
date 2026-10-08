package service

import (
	"context"
	"ludiskus/internal/domain"
	"time"
)

func (s *Service) pollRate(ctx context.Context, key string, limit, seconds int) error {
	if s.redis == nil || limit <= 0 {
		return nil
	}
	key = "poll:rl:" + key
	n, e := s.redis.Incr(ctx, key).Result()
	if e != nil {
		return nil
	}
	if n == 1 {
		_ = s.redis.Expire(ctx, key, time.Duration(seconds)*time.Second).Err()
	}
	if n > int64(limit) {
		return domain.ErrRateLimited
	}
	return nil
}
