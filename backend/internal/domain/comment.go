package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	CommentPublished = "published"
	CommentPending   = "pending"
	CommentHidden    = "hidden"
	CommentDeleted   = "deleted"
	CommentRejected  = "rejected"
)

var (
	ErrInvalidRef           = errors.New("INVALID_REF")
	ErrInvalidCursor        = errors.New("INVALID_CURSOR")
	ErrSortNotSupported     = errors.New("SORT_NOT_SUPPORTED")
	ErrServiceNotRegistered = errors.New("SERVICE_NOT_REGISTERED")
	ErrCommentDisabled      = errors.New("COMMENT_DISABLED")
	ErrCommentNotAllowed    = errors.New("COMMENT_NOT_ALLOWED")
	ErrResourceBlocked      = errors.New("RESOURCE_BLOCKED")
	ErrResourceGone         = errors.New("RESOURCE_GONE")
	ErrThreadLocked         = errors.New("THREAD_LOCKED")
	ErrEditWindowClosed     = errors.New("EDIT_WINDOW_CLOSED")
	ErrDuplicateComment     = errors.New("DUPLICATE_COMMENT")
	ErrRateLimited          = errors.New("RATE_LIMITED")
	ErrResolverUnavailable  = errors.New("RESOURCE_RESOLVER_UNAVAILABLE")
	ErrResolverMissing      = errors.New("RESOURCE_RESOLVER_MISSING")
	ErrServiceScope         = errors.New("SERVICE_SCOPE_MISMATCH")
	ErrUnknownServiceClient = errors.New("UNKNOWN_SERVICE_CLIENT")
)

var (
	serviceCodeRE  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)
	resourceTypeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,59}$`)
	resourceIDRE   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,100}$`)
)

type ResourceRef struct {
	Service string `json:"service"`
	Type    string `json:"type"`
	ID      string `json:"id"`
}

func (r ResourceRef) Validate() error {
	if !serviceCodeRE.MatchString(r.Service) || !resourceTypeRE.MatchString(r.Type) || !resourceIDRE.MatchString(r.ID) {
		return fmt.Errorf("%w: tham chiếu nội dung không hợp lệ", ErrInvalidRef)
	}
	return nil
}

func (r ResourceRef) String() string { return r.Service + ":" + r.Type + ":" + r.ID }

func ParseResourceRef(v string) (ResourceRef, error) {
	p := strings.SplitN(v, ":", 3)
	if len(p) != 3 {
		return ResourceRef{}, ErrInvalidRef
	}
	r := ResourceRef{Service: p[0], Type: p[1], ID: p[2]}
	return r, r.Validate()
}

type CommentService struct {
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	BaseURL       string    `json:"baseUrl"`
	OAuthClientID string    `json:"oauthClientId,omitempty"`
	VerifyMode    string    `json:"verifyMode"`
	ContextPath   string    `json:"contextPath,omitempty"`
	IsActive      bool      `json:"isActive"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type CommentOwner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type CommentTarget struct {
	ID               string          `json:"id"`
	ServiceCode      string          `json:"serviceCode"`
	ResourceType     string          `json:"resourceType"`
	ResourceID       string          `json:"resourceId"`
	SpaceUUID        *string         `json:"spaceUuid,omitempty"`
	OwnerType        *string         `json:"ownerType,omitempty"`
	OwnerID          *string         `json:"ownerId,omitempty"`
	Title            string          `json:"title"`
	Summary          string          `json:"summary"`
	ThumbnailURL     string          `json:"thumbnailUrl,omitempty"`
	CanonicalPath    string          `json:"canonicalPath,omitempty"`
	Visibility       string          `json:"visibility"`
	State            string          `json:"state"`
	ThreadState      string          `json:"threadState"`
	Capabilities     json.RawMessage `json:"resourceCapabilities,omitempty"`
	CommentCount     int             `json:"commentCount"`
	ReplyCount       int             `json:"replyCount"`
	ParticipantCount int             `json:"participantCount"`
	PendingCount     int             `json:"pendingCount"`
	LastCommentAt    *time.Time      `json:"lastCommentAt,omitempty"`
	LastCommentID    *string         `json:"lastCommentId,omitempty"`
	VerifyFailures   int             `json:"verifyFailures,omitempty"`
	VerifiedAt       *time.Time      `json:"verifiedAt,omitempty"`
	CreatedBy        *string         `json:"createdBy,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

func (t CommentTarget) Ref() ResourceRef {
	return ResourceRef{Service: t.ServiceCode, Type: t.ResourceType, ID: t.ResourceID}
}

type PolicyAttachments struct {
	Enabled       bool `json:"enabled"`
	MaxPerComment int  `json:"max_per_comment"`
	ImagesOnly    bool `json:"images_only"`
}

type PolicyMentions struct {
	Enabled       bool   `json:"enabled"`
	Scope         string `json:"scope"`
	MaxPerComment int    `json:"max_per_comment"`
}

type PolicyPin struct {
	Enabled   bool   `json:"enabled"`
	By        string `json:"by"`
	MaxPinned int    `json:"max_pinned"`
}

type PolicyInteraction struct {
	Like     bool `json:"like"`
	Reaction bool `json:"reaction"`
	Bookmark bool `json:"bookmark"`
	Share    bool `json:"share"`
}

type PolicyRateLimit struct {
	PerMinute        int `json:"per_minute"`
	PerHour          int `json:"per_hour"`
	PerTargetPerHour int `json:"per_target_per_hour"`
}

type PolicyNotify struct {
	Owner        bool `json:"owner"`
	Participants bool `json:"participants"`
	Mention      bool `json:"mention"`
}

type CommentPolicy struct {
	Enabled                 bool              `json:"enabled"`
	WhoCanComment           string            `json:"who_can_comment"`
	MaxDepth                int               `json:"max_depth"`
	MaxLength               int               `json:"max_length"`
	MinLength               int               `json:"min_length"`
	Markdown                string            `json:"markdown"`
	ModerationMode          string            `json:"moderation_mode"`
	BannedWordsSource       string            `json:"banned_words_source"`
	BannedWords             []string          `json:"banned_words,omitempty"`
	Attachments             PolicyAttachments `json:"attachments"`
	Mentions                PolicyMentions    `json:"mentions"`
	EditWindowMinutes       int               `json:"edit_window_minutes"`
	DeleteOwn               bool              `json:"delete_own"`
	Pin                     PolicyPin         `json:"pin"`
	Interaction             PolicyInteraction `json:"interaction"`
	RateLimit               PolicyRateLimit   `json:"rate_limit"`
	Notify                  PolicyNotify      `json:"notify"`
	ReportAutoHideThreshold int               `json:"report_auto_hide_threshold"`
	PublicRead              bool              `json:"public_read"`
	SortDefault             string            `json:"sort_default"`
	MaxLinks                int               `json:"max_links"`
	Guest                   bool              `json:"guest"`
}

func DefaultCommentPolicy() CommentPolicy {
	return CommentPolicy{
		Enabled: true, WhoCanComment: "authenticated", MaxDepth: 2,
		MaxLength: 4000, MinLength: 2, Markdown: "basic", ModerationMode: "post",
		BannedWordsSource: "space",
		Attachments:       PolicyAttachments{MaxPerComment: 3, ImagesOnly: true},
		Mentions:          PolicyMentions{Enabled: true, Scope: "space", MaxPerComment: 10},
		EditWindowMinutes: 15, DeleteOwn: true,
		Pin:                     PolicyPin{Enabled: true, By: "owner", MaxPinned: 3},
		Interaction:             PolicyInteraction{Like: true, Reaction: true},
		RateLimit:               PolicyRateLimit{PerMinute: 5, PerHour: 60, PerTargetPerHour: 20},
		Notify:                  PolicyNotify{Owner: true, Participants: true, Mention: true},
		ReportAutoHideThreshold: 5, SortDefault: "newest", MaxLinks: 3,
	}
}

type CommentCapabilities struct {
	CanRead           bool              `json:"canRead"`
	CanComment        bool              `json:"canComment"`
	CanReply          bool              `json:"canReply"`
	CanAttach         bool              `json:"canAttach"`
	CanMention        bool              `json:"canMention"`
	CanPin            bool              `json:"canPin"`
	CanModerate       bool              `json:"canModerate"`
	MaxDepth          int               `json:"maxDepth"`
	MaxLength         int               `json:"maxLength"`
	Markdown          string            `json:"markdown"`
	EditWindowMinutes int               `json:"editWindowMinutes"`
	SortOptions       []string          `json:"sortOptions"`
	Interaction       PolicyInteraction `json:"interaction"`
	Reasons           map[string]string `json:"reasons,omitempty"`
}

type Comment struct {
	ID                 string          `json:"id"`
	TargetID           string          `json:"targetId"`
	ParentID           *string         `json:"parentId,omitempty"`
	RootID             string          `json:"rootId"`
	Depth              int             `json:"depth"`
	ReplyToProfileUUID *string         `json:"replyToProfileUuid,omitempty"`
	AuthorKind         string          `json:"authorKind"`
	AuthorProfileUUID  *string         `json:"authorProfileUuid,omitempty"`
	AuthorSpaceUUID    *string         `json:"authorSpaceUuid,omitempty"`
	SourceService      *string         `json:"sourceService,omitempty"`
	BodyMD             string          `json:"bodyMd,omitempty"`
	BodyHTML           string          `json:"bodyHtml,omitempty"`
	BodyHash           string          `json:"-"`
	MarkdownMode       string          `json:"markdownMode,omitempty"`
	Status             string          `json:"status"`
	ModerationSource   *string         `json:"moderationSource,omitempty"`
	IsPinned           bool            `json:"isPinned"`
	PinnedBy           *string         `json:"pinnedBy,omitempty"`
	PinnedAt           *time.Time      `json:"pinnedAt,omitempty"`
	ReplyCount         int             `json:"replyCount"`
	Anchor             json.RawMessage `json:"anchor,omitempty"`
	IdempotencyKey     *string         `json:"-"`
	EditedAt           *time.Time      `json:"editedAt,omitempty"`
	EditCount          int             `json:"editCount"`
	DeletedAt          *time.Time      `json:"deletedAt,omitempty"`
	DeletedBy          *string         `json:"-"`
	DeleteReason       *string         `json:"deleteReason,omitempty"`
	ScoreCache         int64           `json:"score,omitempty"`
	CreatedAt          time.Time       `json:"createdAt"`
	UpdatedAt          time.Time       `json:"updatedAt"`
	Author             *CachedProfile  `json:"author,omitempty"`
	ReplyToProfile     *CachedProfile  `json:"replyToProfile,omitempty"`
	Attachments        []Attachment    `json:"attachments,omitempty"`
	Mentions           []string        `json:"mentions,omitempty"`
	PreviewReplies     []Comment       `json:"previewReplies,omitempty"`
	Deleted            bool            `json:"deleted,omitempty"`
	DeletedByAuthor    bool            `json:"deletedByAuthor,omitempty"`
	CanEdit            bool            `json:"canEdit"`
	CanDelete          bool            `json:"canDelete"`
	CanModerate        bool            `json:"canModerate"`
}

type CommentRevision struct {
	CommentID string    `json:"commentId"`
	Revision  int       `json:"revision"`
	BodyMD    string    `json:"bodyMd"`
	EditedBy  string    `json:"editedBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type CommentParticipant struct {
	TargetID       string     `json:"targetId"`
	ProfileUUID    string     `json:"profileUuid"`
	Reason         string     `json:"reason"`
	Muted          bool       `json:"muted"`
	LastReadAt     *time.Time `json:"lastReadAt,omitempty"`
	LastNotifiedAt *time.Time `json:"lastNotifiedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

func CountDelta(oldStatus, newStatus string, isRoot bool) (comments, replies, pending int) {
	if oldStatus == CommentPublished {
		if isRoot {
			comments--
		} else {
			replies--
		}
	}
	if newStatus == CommentPublished {
		if isRoot {
			comments++
		} else {
			replies++
		}
	}
	if oldStatus == CommentPending {
		pending--
	}
	if newStatus == CommentPending {
		pending++
	}
	return
}

func countDelta(oldStatus, newStatus string, isRoot bool) (int, int, int) {
	return CountDelta(oldStatus, newStatus, isRoot)
}
