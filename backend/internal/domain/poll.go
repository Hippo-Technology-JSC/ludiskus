package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PollError carries the stable API code and the field that failed validation.
type PollError struct {
	Code   string
	Status int
	Field  string
}

func (e *PollError) Error() string                            { return e.Code }
func PollFailure(code string, status int, field string) error { return &PollError{code, status, field} }

type Poll struct {
	ID                  string
	AnchorTargetID      *string
	SpaceUUID           *string
	Visibility          *string
	AuthorKind          string
	CreatedBy           *string
	AuthorSpaceUUID     *string
	SourceService       *string
	Question            string
	DescriptionMD       string
	DescriptionHTML     string
	Kind                string
	Config              json.RawMessage
	IdentityMode        string
	ResultsVisibility   string
	WhoCanVote          string
	AllowChangeVote     bool
	AllowRetract        bool
	AllowUserOptions    bool
	ShuffleOptions      bool
	Remind              bool
	MinVotersForResults int
	QuorumPercent       *int
	Status              string
	StatusBeforeHide    *string
	OpensAt             *time.Time
	ClosesAt            *time.Time
	ClosedAt            *time.Time
	ClosedBy            *string
	CloseReason         *string
	ReopenCount         int
	RemindedAt          *time.Time
	EligibleCount       *int
	VoterCount          int
	BallotVersion       int64
	FirstVoteAt         *time.Time
	EditCount           int
	IdempotencyKey      *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	DeletedAt           *time.Time
	Options             []PollOption
	Target              *CommentTarget
}

func (p Poll) EffectiveState(now time.Time) string {
	if p.Status == "deleted" {
		return "deleted"
	}
	if p.Status == "hidden" || p.Status == "rejected" || (p.Target != nil && (p.Target.State == "gone" || p.Target.State == "blocked")) {
		return "hidden"
	}
	if p.Status == "pending" || p.Status == "draft" {
		return p.Status
	}
	if p.Status == "closed" || (p.ClosesAt != nil && !now.Before(*p.ClosesAt)) {
		return "closed"
	}
	if p.OpensAt != nil && now.Before(*p.OpensAt) {
		return "scheduled"
	}
	return "open"
}
func (p Poll) Space() string {
	if p.Target != nil && p.Target.SpaceUUID != nil {
		return *p.Target.SpaceUUID
	}
	if p.SpaceUUID != nil {
		return *p.SpaceUUID
	}
	return ""
}
func (p Poll) Path() string {
	if p.Target != nil && p.Target.CanonicalPath != "" {
		return p.Target.CanonicalPath + "#poll-" + p.ID
	}
	return "/ludiskus/p/" + p.ID
}

type PollOption struct {
	ID         string     `json:"id"`
	PollID     string     `json:"-"`
	Position   int        `json:"position"`
	Label      string     `json:"label"`
	LabelNorm  string     `json:"-"`
	Value      *int       `json:"value,omitempty"`
	StartsAt   *time.Time `json:"startsAt,omitempty"`
	EndsAt     *time.Time `json:"endsAt,omitempty"`
	AddedBy    *string    `json:"-"`
	Status     string     `json:"status"`
	VoteCount  int        `json:"-"`
	MaybeCount int        `json:"-"`
}
type PollChoice struct {
	OptionID string `json:"optionId"`
	Rank     int    `json:"rank,omitempty"`
	Answer   string `json:"answer,omitempty"`
}
type PollVoteInput struct {
	Choices      []PollChoice `json:"choices"`
	Answers      []PollChoice `json:"answers"`
	NotifyResult *bool        `json:"notifyResult"`
}
type PollReceipt struct {
	ProfileUUID    string       `json:"profileUuid"`
	IdempotencyKey string       `json:"-"`
	NotifyResult   bool         `json:"notifyResult"`
	VotedAt        time.Time    `json:"votedAt"`
	Choices        []PollChoice `json:"choices,omitempty"`
}
type PollConfig struct {
	MinChoices int    `json:"min_choices,omitempty"`
	MaxChoices int    `json:"max_choices,omitempty"`
	Method     string `json:"method,omitempty"`
	MaxRanks   int    `json:"max_ranks,omitempty"`
	Min        int    `json:"min,omitempty"`
	Max        int    `json:"max,omitempty"`
	MinLabel   string `json:"min_label,omitempty"`
	MaxLabel   string `json:"max_label,omitempty"`
	AllowMaybe bool   `json:"allow_maybe,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

func (p Poll) Rules() PollConfig { var c PollConfig; _ = json.Unmarshal(p.Config, &c); return c }

type PollPolicy struct {
	Enabled                  bool     `json:"enabled"`
	WhoCanCreate             string   `json:"who_can_create"`
	Kinds                    []string `json:"kinds"`
	IdentityModes            []string `json:"identity_modes"`
	MaxPerAnchor             int      `json:"max_per_anchor"`
	MaxOptions               int      `json:"max_options"`
	MaxDurationDays          int      `json:"max_duration_days"`
	DefaultIdentityMode      string   `json:"default_identity_mode"`
	DefaultResultsVisibility string   `json:"default_results_visibility"`
	AllowUserOptions         bool     `json:"allow_user_options"`
	ModerationMode           string   `json:"moderation_mode"`
	PublicRead               bool     `json:"public_read"`
	NewProfileCanVote        bool     `json:"new_profile_can_vote"`
	RateLimit                struct {
		CreatePerHour  int `json:"create_per_hour"`
		VotesPerMinute int `json:"votes_per_minute"`
	} `json:"rate_limit"`
}

func DefaultPollPolicy() PollPolicy {
	p := PollPolicy{WhoCanCreate: "owner", Kinds: []string{"single", "multiple", "ranked", "scale", "schedule"}, IdentityModes: []string{"public", "owner_only", "anonymous", "secret"}, MaxPerAnchor: 1, MaxOptions: 20, MaxDurationDays: 90, DefaultIdentityMode: "public", DefaultResultsVisibility: "after_vote", AllowUserOptions: true, ModerationMode: "post", NewProfileCanVote: true}
	p.RateLimit.CreatePerHour = 10
	p.RateLimit.VotesPerMinute = 30
	return p
}

type PollResult struct {
	Final         bool            `json:"final"`
	Method        string          `json:"method"`
	VoterCount    int             `json:"voterCount"`
	EligibleCount *int            `json:"eligibleCount"`
	QuorumMet     *bool           `json:"quorumMet"`
	Result        json.RawMessage `json:"result"`
	BallotVersion int64           `json:"-"`
	ComputedAt    time.Time       `json:"computedAt"`
}

// Only this interface crosses the forum/comment boundary. Snap is constructed
// from the new content inside its transaction, never resolved through the pool.
type PollAttacher interface {
	AttachDrafts(context.Context, pgx.Tx, string, ResourceRef, InteractionContext, []string, bool) error
	OnAnchorDecision(context.Context, pgx.Tx, ResourceRef, bool) error
	PollIDs(context.Context, []string) (map[string][]string, error)
}

func ValidatePollKey(key string) error {
	if key == "" {
		return PollFailure("IDEMPOTENCY_KEY_REQUIRED", 428, "")
	}
	if len(key) > 80 {
		return PollFailure("VOTE_INVALID", 422, "Idempotency-Key")
	}
	return nil
}
func PollLocked(field string) error { return PollFailure("POLL_LOCKED", 409, field) }
func (p Poll) String() string       { return fmt.Sprintf("poll:%s", p.ID) }
