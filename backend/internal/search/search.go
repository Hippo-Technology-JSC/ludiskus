// Package search defines the forum search boundary; PostgreSQL is the current engine.
package search

import (
	"context"
	"ludiskus/internal/domain"
	"time"
)

type Options struct {
	Query                                             string
	Spaces                                            []string
	BoardID, AuthorUUID, TopicType, Tag, Status, Kind string
	From, Until                                       *time.Time
	Limit, Offset                                     int
}
type Engine interface {
	SearchForum(context.Context, Options) ([]domain.Topic, error)
}
