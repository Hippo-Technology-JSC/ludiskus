// Package domain định nghĩa entity và lỗi miền cho ludiskus (docs/03).
package domain

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not_found")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrValidation   = errors.New("validation_error")
	ErrConflict     = errors.New("conflict")
	ErrTooLarge     = errors.New("too_large")
	ErrPending      = errors.New("pending_moderation") // bài vào hàng chờ duyệt
)

// Vai trò thành viên Space (theo pivot profile_space của HipCore + moderator nội bộ).
const (
	RoleOwner     = "owner"
	RoleAdmin     = "admin"
	RoleModerator = "moderator"
	RoleMember    = "member"
)

// Chế độ kiểm duyệt (docs/04).
const (
	ModNone      = "none"
	ModPost      = "post"
	ModPre       = "pre"
	ModFirstPost = "first_post"
)

// Chính sách đăng bài.
const (
	PolicyMembers = "members"
	PolicyAnyone  = "anyone_authenticated"
	PolicyStaff   = "staff_only"
)

// Trạng thái Topic/Post.
const (
	StatusPublished = "published"
	StatusPending   = "pending"
	StatusLocked    = "locked"
	StatusHidden    = "hidden"
	StatusDeleted   = "deleted"
)

// --- SpaceForum -------------------------------------------------------------

type SpaceForum struct {
	SpaceUUID               string          `json:"spaceUuid"`
	Enabled                 bool            `json:"enabled"`
	IsPublic                bool            `json:"isPublic"`
	PostPolicy              string          `json:"postPolicy"`
	ModerationMode          string          `json:"moderationMode"`
	BannedWords             []string        `json:"bannedWords"`
	ReportAutoHideThreshold int             `json:"reportAutoHideThreshold"`
	DefaultTopicType        string          `json:"defaultTopicType"`
	Settings                json.RawMessage `json:"settings,omitempty"`
	CreatedAt               time.Time       `json:"createdAt"`
	UpdatedAt               time.Time       `json:"updatedAt"`
}

// --- Board ------------------------------------------------------------------

type Board struct {
	ID              string     `json:"id"`
	SpaceUUID       string     `json:"spaceUuid"`
	ParentID        *string    `json:"parentId,omitempty"`
	Code            string     `json:"code"`
	Name            string     `json:"name"`
	DescriptionMD   *string    `json:"descriptionMd,omitempty"`
	DescriptionHTML *string    `json:"descriptionHtml,omitempty"`
	Kind            string     `json:"kind"`
	Position        int        `json:"position"`
	IsLocked        bool       `json:"isLocked"`
	MinRole         string     `json:"minRole"`
	TopicCount      int        `json:"topicCount"`
	PostCount       int        `json:"postCount"`
	LastActivityAt  *time.Time `json:"lastActivityAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// --- Topic ------------------------------------------------------------------

type Topic struct {
	ID                  string         `json:"id"`
	SpaceUUID           string         `json:"spaceUuid"`
	BoardID             string         `json:"boardId"`
	AuthorProfileUUID   string         `json:"authorProfileUuid"`
	Title               string         `json:"title"`
	Slug                string         `json:"slug"`
	Type                string         `json:"type"`
	Status              string         `json:"status"`
	IsPinned            bool           `json:"isPinned"`
	IsResolved          bool           `json:"isResolved"`
	AnswerPostID        *string        `json:"answerPostId,omitempty"`
	ReplyCount          int            `json:"replyCount"`
	ViewCount           int            `json:"viewCount"`
	LastPostAt          *time.Time     `json:"lastPostAt,omitempty"`
	LastPostProfileUUID *string        `json:"lastPostProfileUuid,omitempty"`
	CreatedAt           time.Time      `json:"createdAt"`
	UpdatedAt           time.Time      `json:"updatedAt"`
	Author              *CachedProfile `json:"author,omitempty"`
	Tags                []string       `json:"tags,omitempty"`
	Rank                float64        `json:"rank,omitempty"`
	Highlight           string         `json:"highlight,omitempty"`
}

// --- Post -------------------------------------------------------------------

type Post struct {
	ID                string         `json:"id"`
	TopicID           string         `json:"topicId"`
	SpaceUUID         string         `json:"spaceUuid"`
	AuthorProfileUUID string         `json:"authorProfileUuid"`
	ReplyToID         *string        `json:"replyToId,omitempty"`
	IsFirst           bool           `json:"isFirst"`
	BodyMD            string         `json:"bodyMd"`
	BodyHTML          string         `json:"bodyHtml"`
	IsAnswer          bool           `json:"isAnswer"`
	Status            string         `json:"status"`
	EditedAt          *time.Time     `json:"editedAt,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
	Author            *CachedProfile `json:"author,omitempty"`
	Attachments       []Attachment   `json:"attachments,omitempty"`
}

// --- Tag --------------------------------------------------------------------

type Tag struct {
	ID         string `json:"id"`
	SpaceUUID  string `json:"spaceUuid"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	UsageCount int    `json:"usageCount"`
}

// InteractionContext là contract pull mà Lufami dùng để xác minh metadata.
type InteractionContext struct {
	Type          string            `json:"type"`
	ID            string            `json:"id"`
	Exists        bool              `json:"exists"`
	Owner         *InteractionOwner `json:"owner,omitempty"`
	SpaceUUID     *string           `json:"spaceUuid,omitempty"`
	Visibility    string            `json:"visibility"`
	State         string            `json:"state"`
	Title         string            `json:"title"`
	Summary       string            `json:"summary"`
	ThumbnailURL  string            `json:"thumbnailUrl"`
	CanonicalPath string            `json:"canonicalPath"`
	Capabilities  json.RawMessage   `json:"capabilities"`
}

type InteractionOwner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type InteractionRef struct {
	Service string `json:"service"`
	Type    string `json:"type"`
	ID      string `json:"id"`
}

type InteractionBackfillItem struct {
	ID               int64
	PostID           string
	ResourceType     string
	ActorProfileUUID string
	InteractionKind  string
	ReactionCode     string
	OccurredAt       time.Time
	Attempts         int
}

// --- Attachment -------------------------------------------------------------

type Attachment struct {
	ID                  string    `json:"id"`
	SpaceUUID           string    `json:"spaceUuid"`
	PostID              *string   `json:"postId,omitempty"`
	UploaderProfileUUID string    `json:"uploaderProfileUuid"`
	ObjectKey           string    `json:"objectKey"`
	FileName            string    `json:"fileName"`
	ContentType         string    `json:"contentType"`
	SizeBytes           int64     `json:"sizeBytes"`
	Kind                string    `json:"kind"`
	Width               *int      `json:"width,omitempty"`
	Height              *int      `json:"height,omitempty"`
	Status              string    `json:"status"`
	URL                 string    `json:"url,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

// --- Subscription -----------------------------------------------------------

type Subscription struct {
	ID          string    `json:"id"`
	ProfileUUID string    `json:"profileUuid"`
	TargetType  string    `json:"targetType"`
	TargetID    string    `json:"targetId"`
	Reason      string    `json:"reason"`
	Muted       bool      `json:"muted"`
	CreatedAt   time.Time `json:"createdAt"`
}

// --- Report / ModerationItem ------------------------------------------------

type Report struct {
	ID                  string    `json:"id"`
	SpaceUUID           string    `json:"spaceUuid"`
	TargetType          string    `json:"targetType"`
	TargetID            string    `json:"targetId"`
	ReporterProfileUUID string    `json:"reporterProfileUuid"`
	Reason              string    `json:"reason"`
	Note                *string   `json:"note,omitempty"`
	Status              string    `json:"status"`
	CreatedAt           time.Time `json:"createdAt"`
}

type ModerationItem struct {
	ID                  string     `json:"id"`
	SpaceUUID           string     `json:"spaceUuid"`
	TargetType          string     `json:"targetType"`
	TargetID            string     `json:"targetId"`
	Source              string     `json:"source"`
	State               string     `json:"state"`
	AssigneeProfileUUID *string    `json:"assigneeProfileUuid,omitempty"`
	DecidedBy           *string    `json:"decidedBy,omitempty"`
	DecidedAt           *time.Time `json:"decidedAt,omitempty"`
	Note                *string    `json:"note,omitempty"`
	CreatedAt           time.Time  `json:"createdAt"`
}

// --- Outbox -----------------------------------------------------------------

type OutboxItem struct {
	ID             string          `json:"id"`
	EventType      string          `json:"eventType"`
	IdempotencyKey *string         `json:"idempotencyKey,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"maxAttempts"`
	ScheduledAt    time.Time       `json:"scheduledAt"`
	LastError      *string         `json:"lastError,omitempty"`
	SentAt         *time.Time      `json:"sentAt,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
}

// --- Cache (docs/05) --------------------------------------------------------

type CachedProfile struct {
	ProfileUUID string    `json:"profileUuid"`
	UserID      *int64    `json:"userId,omitempty"`
	Code        *string   `json:"code,omitempty"`
	Name        string    `json:"name"`
	Avatar      *string   `json:"avatar,omitempty"`
	IsActive    bool      `json:"isActive"`
	SyncedAt    time.Time `json:"syncedAt"`
}

type CachedSpace struct {
	SpaceUUID          string    `json:"spaceUuid"`
	Code               *string   `json:"code,omitempty"`
	Name               string    `json:"name"`
	IsPublic           bool      `json:"isPublic"`
	IsActive           bool      `json:"isActive"`
	CreatorProfileUUID *string   `json:"creatorProfileUuid,omitempty"`
	SpaceType          *string   `json:"spaceType,omitempty"`
	SyncedAt           time.Time `json:"syncedAt"`
}

type CachedMember struct {
	SpaceUUID   string     `json:"spaceUuid"`
	ProfileUUID string     `json:"profileUuid"`
	Role        string     `json:"role"`
	JoinedAt    *time.Time `json:"joinedAt,omitempty"`
	SyncedAt    time.Time  `json:"syncedAt"`
}
