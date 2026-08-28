package domain

import (
	"encoding/json"
	"time"
)

type PersonalFileSyncItem struct {
	ID             string
	IdempotencyKey string
	SourceFileID   string
	EventType      string
	Payload        json.RawMessage
	Attempts       int
	MaxAttempts    int
	ScheduledAt    time.Time
}
