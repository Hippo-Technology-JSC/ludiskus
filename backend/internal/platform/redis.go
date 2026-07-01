// Package platform chứa client hạ tầng dùng chung (Redis).
package platform

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// ConnectRedis mở client Redis từ URL dạng redis://host:port/db.
func ConnectRedis(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	c := redis.NewClient(opt)
	if err := c.Ping(ctx).Err(); err != nil {
		c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}
