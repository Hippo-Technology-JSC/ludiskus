package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/unicode/norm"
	"ludiskus/internal/domain"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type PollViewer = pollViewer

func unmarshalPoll(raw []byte, v any) error { return json.Unmarshal(raw, v) }

type PollPatch struct {
	Options             *[]domain.PollOption `json:"options"`
	Question            *string              `json:"question"`
	DescriptionMD       *string              `json:"descriptionMd"`
	Kind                *string              `json:"kind"`
	Config              json.RawMessage      `json:"config"`
	IdentityMode        *string              `json:"identityMode"`
	ResultsVisibility   *string              `json:"resultsVisibility"`
	WhoCanVote          *string              `json:"whoCanVote"`
	AllowChangeVote     *bool                `json:"allowChangeVote"`
	AllowRetract        *bool                `json:"allowRetract"`
	AllowUserOptions    *bool                `json:"allowUserOptions"`
	ShuffleOptions      *bool                `json:"shuffleOptions"`
	Remind              *bool                `json:"remind"`
	MinVotersForResults *int                 `json:"minVotersForResults"`
	QuorumPercent       *int                 `json:"quorumPercent"`
	OpensAt             *time.Time           `json:"opensAt"`
	ClosesAt            *time.Time           `json:"closesAt"`
}
type PollInput struct {
	PollPatch
	Anchor     *domain.ResourceRef `json:"anchor"`
	Standalone *struct {
		Visibility string  `json:"visibility"`
		SpaceUUID  *string `json:"spaceUuid"`
	} `json:"standalone"`
	Draft            bool                `json:"draft"`
	Publish          bool                `json:"publish"`
	ActAsSpaceUUID   *string             `json:"actAsSpaceUuid"`
	ActorProfileUUID *string             `json:"actorProfileUuid"`
	Options          []domain.PollOption `json:"options"`
	Invitees         []string            `json:"invitees"`
}

func normalizePollLabel(v string) string {
	return strings.ToLower(norm.NFC.String(strings.Join(strings.Fields(v), " ")))
}
func applyPollPatch(p *domain.Poll, in PollPatch) error {
	locked := p.FirstVoteAt != nil
	bad := func(field string) error { return domain.PollLocked(field) }
	if locked {
		if (in.Question != nil || in.DescriptionMD != nil) && time.Since(*p.FirstVoteAt) > 15*time.Minute {
			return bad("question")
		}
		if in.Kind != nil && *in.Kind != p.Kind {
			return bad("kind")
		}
		if len(in.Config) > 0 && !jsonEqualPoll(in.Config, p.Config) {
			return bad("config")
		}
		if in.IdentityMode != nil && *in.IdentityMode != p.IdentityMode {
			levels := map[string]int{"public": 0, "owner_only": 1, "anonymous": 2}
			a, ok := levels[p.IdentityMode]
			b, ok2 := levels[*in.IdentityMode]
			if !ok || !ok2 || b < a {
				return bad("identityMode")
			}
		}
		if in.ResultsVisibility != nil && *in.ResultsVisibility != p.ResultsVisibility && !(p.EffectiveState(time.Now()) == "closed" && *in.ResultsVisibility == "always") {
			return bad("resultsVisibility")
		}
		if in.WhoCanVote != nil {
			level := map[string]int{"invited": 0, "members": 1, "viewers": 2}
			if level[*in.WhoCanVote] < level[p.WhoCanVote] {
				return bad("whoCanVote")
			}
		}
		if in.AllowChangeVote != nil && *in.AllowChangeVote && !p.AllowChangeVote {
			return bad("allowChangeVote")
		}
		if in.AllowRetract != nil && *in.AllowRetract && !p.AllowRetract {
			return bad("allowRetract")
		}
		if in.ClosesAt != nil && p.ClosesAt != nil && in.ClosesAt.Before(*p.ClosesAt) {
			return bad("closesAt")
		}
		if in.QuorumPercent != nil && (p.QuorumPercent == nil || *p.QuorumPercent != *in.QuorumPercent) {
			return bad("quorumPercent")
		}
		if in.OpensAt != nil && (p.OpensAt == nil || !in.OpensAt.Equal(*p.OpensAt)) {
			return bad("opensAt")
		}
		if in.MinVotersForResults != nil && *in.MinVotersForResults < p.MinVotersForResults {
			return bad("minVotersForResults")
		}
	}
	if in.Question != nil {
		p.Question = strings.TrimSpace(*in.Question)
	}
	if in.DescriptionMD != nil {
		p.DescriptionMD = strings.TrimSpace(*in.DescriptionMD)
	}
	if in.Kind != nil {
		p.Kind = *in.Kind
	}
	if len(in.Config) > 0 {
		p.Config = in.Config
	}
	if in.IdentityMode != nil {
		p.IdentityMode = *in.IdentityMode
	}
	if in.ResultsVisibility != nil {
		p.ResultsVisibility = *in.ResultsVisibility
	}
	if in.WhoCanVote != nil {
		p.WhoCanVote = *in.WhoCanVote
	}
	if in.AllowChangeVote != nil {
		p.AllowChangeVote = *in.AllowChangeVote
	}
	if in.AllowRetract != nil {
		p.AllowRetract = *in.AllowRetract
	}
	if in.AllowUserOptions != nil {
		p.AllowUserOptions = *in.AllowUserOptions
	}
	if in.ShuffleOptions != nil {
		p.ShuffleOptions = *in.ShuffleOptions
	}
	if in.Remind != nil {
		p.Remind = *in.Remind
	}
	if in.MinVotersForResults != nil {
		p.MinVotersForResults = *in.MinVotersForResults
	}
	if in.QuorumPercent != nil {
		p.QuorumPercent = in.QuorumPercent
	}
	if in.OpensAt != nil {
		p.OpensAt = in.OpensAt
	}
	if in.ClosesAt != nil {
		p.ClosesAt = in.ClosesAt
	}
	return nil
}
func jsonEqualPoll(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	aa, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(aa) == string(bb)
}
func validatePoll(p *domain.Poll, policy domain.PollPolicy) error {
	bad := func(field string) error { return domain.PollFailure("VOTE_INVALID", 422, field) }
	if utf8.RuneCountInString(p.Question) < 3 || utf8.RuneCountInString(p.Question) > 300 || countCommentLinks(p.Question) > 0 {
		return bad("question")
	}
	if utf8.RuneCountInString(p.DescriptionMD) > 2000 || countCommentLinks(p.DescriptionMD) > 3 {
		return bad("descriptionMd")
	}
	if !slices.Contains(policy.Kinds, p.Kind) {
		return bad("kind")
	}
	if !slices.Contains(policy.IdentityModes, p.IdentityMode) {
		return bad("identityMode")
	}
	if !slices.Contains([]string{"always", "after_vote", "after_close", "owner_only"}, p.ResultsVisibility) {
		return bad("resultsVisibility")
	}
	if !slices.Contains([]string{"viewers", "members", "invited"}, p.WhoCanVote) {
		return bad("whoCanVote")
	}
	if p.WhoCanVote == "members" && p.Space() == "" {
		return domain.PollFailure("MEMBERS_REQUIRES_SPACE", 422, "whoCanVote")
	}
	if p.QuorumPercent != nil && (*p.QuorumPercent < 1 || *p.QuorumPercent > 100 || p.WhoCanVote == "viewers") {
		return bad("quorumPercent")
	}
	if p.MinVotersForResults < 2 || p.MinVotersForResults > 50 {
		return bad("minVotersForResults")
	}
	if p.IdentityMode == "secret" && (p.AllowChangeVote || p.AllowRetract) {
		return bad("allowChangeVote")
	}
	if p.AllowUserOptions && (!policy.AllowUserOptions || (p.Kind != "single" && p.Kind != "multiple")) {
		return bad("allowUserOptions")
	}
	if p.Target == nil && p.Status != "draft" && (p.Visibility == nil || !slices.Contains([]string{"public", "authenticated", "unlisted", "space", "private"}, *p.Visibility)) {
		return bad("standalone.visibility")
	}
	if p.Visibility != nil && *p.Visibility == "space" && p.SpaceUUID == nil {
		return bad("standalone.spaceUuid")
	}
	now := time.Now()
	start := now
	if p.OpensAt != nil {
		start = *p.OpensAt
	}
	if p.ClosesAt != nil {
		if p.ClosesAt.After(now.Add(365*24*time.Hour)) || p.ClosesAt.After(start.Add(time.Duration(policy.MaxDurationDays)*24*time.Hour)) || p.OpensAt != nil && p.ClosesAt.Before(start.Add(5*time.Minute)) {
			return bad("closesAt")
		}
		if p.Status == "draft" && p.ClosesAt.Before(now.Add(5*time.Minute)) {
			return bad("closesAt")
		}
	}
	c := p.Rules()
	if len(p.Config) > 0 {
		var obj map[string]any
		if json.Unmarshal(p.Config, &obj) != nil || obj == nil {
			return bad("config")
		}
	}
	n := 0
	for _, o := range p.Options {
		if o.Status != "hidden" {
			n++
		}
	}
	switch p.Kind {
	case "single":
		if n < 2 || n > min(20, policy.MaxOptions) {
			return bad("options")
		}
	case "multiple":
		if n < 2 || n > min(20, policy.MaxOptions) || c.MinChoices < 1 || c.MaxChoices < c.MinChoices || c.MaxChoices > n {
			return bad("config")
		}
	case "ranked":
		if n < 2 || n > min(20, policy.MaxOptions) || c.MaxRanks < 1 || c.MaxRanks > n || !slices.Contains([]string{"irv", "borda"}, c.Method) {
			return bad("config")
		}
	case "scale":
		if c.Max-c.Min < 2 || c.Max-c.Min > 10 || c.Min < -32768 || c.Max > 32767 || n != c.Max-c.Min+1 {
			return bad("config")
		}
	case "schedule":
		if n < 2 || n > min(30, policy.MaxOptions) {
			return bad("options")
		}
		if _, e := time.LoadLocation(c.Timezone); e != nil {
			return bad("config.timezone")
		}
		for _, o := range p.Options {
			if o.StartsAt == nil || o.EndsAt == nil || !o.EndsAt.After(*o.StartsAt) {
				return bad("options.startsAt")
			}
		}
	}
	seen := map[string]bool{}
	for _, o := range p.Options {
		if o.Status == "hidden" {
			continue
		}
		if utf8.RuneCountInString(o.Label) < 1 || utf8.RuneCountInString(o.Label) > 200 || countCommentLinks(o.Label) > 0 {
			return bad("options.label")
		}
		norm := normalizePollLabel(o.Label)
		if seen[norm] {
			return domain.PollFailure("OPTION_DUPLICATE", 409, "options.label")
		}
		seen[norm] = true
	}
	return nil
}
func preparePollOptions(p *domain.Poll, input []domain.PollOption) {
	p.Options = []domain.PollOption{}
	if p.Kind == "scale" {
		input = nil
		c := p.Rules()
		if c.Max-c.Min >= 2 && c.Max-c.Min <= 10 {
			for v := c.Min; v <= c.Max; v++ {
				value := v
				label := strings.TrimSpace(strings.Join([]string{pollInt(v), map[bool]string{true: c.MinLabel}[v == c.Min], map[bool]string{true: c.MaxLabel}[v == c.Max]}, " "))
				input = append(input, domain.PollOption{Label: label, Value: &value})
			}
		}
	}
	for i, o := range input {
		o.ID = uuid.NewString()
		o.PollID = p.ID
		o.Position = i
		o.Label = strings.TrimSpace(o.Label)
		o.LabelNorm = normalizePollLabel(o.Label)
		o.Status = "active"
		o.AddedBy = nil
		o.VoteCount = 0
		o.MaybeCount = 0
		p.Options = append(p.Options, o)
	}
}
func (s *Service) CreatePoll(ctx context.Context, v pollViewer, key string, in PollInput) (map[string]any, error) {
	if !s.cfg.PollEnabled {
		return nil, domain.ErrNotFound
	}
	if e := domain.ValidatePollKey(key); e != nil {
		return nil, e
	}
	modes := 0
	if in.Anchor != nil {
		modes++
	}
	if in.Standalone != nil {
		modes++
	}
	if in.Draft {
		modes++
	}
	if modes != 1 || in.Draft && in.Publish {
		return nil, domain.ErrValidation
	}
	var t *domain.CommentTarget
	var e error
	space := ""
	if in.Standalone != nil && in.Standalone.SpaceUUID != nil {
		space = *in.Standalone.SpaceUUID
	}
	if in.Anchor != nil {
		if in.Anchor.Service == "ludiskus" && in.Anchor.Type == "poll" {
			return nil, domain.PollFailure("ANCHOR_NOT_ALLOWED", 422, "anchor")
		}
		t, e = s.ensureCommentTarget(ctx, *in.Anchor, v.Profile)
		if e != nil {
			return nil, e
		}
	}
	policy, e := s.ensurePollCreatable(ctx, t, space, v)
	if e != nil {
		return nil, e
	}
	p := &domain.Poll{ID: uuid.NewString(), AuthorKind: "profile", Kind: "single", Config: json.RawMessage(`{}`), IdentityMode: policy.DefaultIdentityMode, ResultsVisibility: policy.DefaultResultsVisibility, WhoCanVote: "viewers", AllowChangeVote: true, AllowRetract: true, MinVotersForResults: 3, Status: "draft", Target: t}
	if v.Profile != "" {
		p.CreatedBy = &v.Profile
	}
	if in.Standalone != nil {
		p.Visibility = &in.Standalone.Visibility
		p.SpaceUUID = in.Standalone.SpaceUUID
	}
	if t != nil {
		p.AnchorTargetID = &t.ID
	}
	if v.Service != "" {
		if t == nil || t.ServiceCode != v.Service {
			return nil, domain.ErrServiceScope
		}
		p.AuthorKind = "service"
		p.SourceService = &v.Service
		p.CreatedBy = in.ActorProfileUUID
	}
	if in.ActAsSpaceUUID != nil {
		r := s.role(ctx, *in.ActAsSpaceUUID, v.Profile)
		if r != domain.RoleOwner && r != domain.RoleAdmin {
			return nil, domain.ErrForbidden
		}
		p.AuthorKind = "space"
		p.AuthorSpaceUUID = in.ActAsSpaceUUID
	}
	if e = applyPollPatch(p, in.PollPatch); e != nil {
		return nil, e
	}
	if p.IdentityMode == "secret" {
		p.AllowChangeVote = false
		p.AllowRetract = false
	}
	preparePollOptions(p, in.Options)
	if e = validatePoll(p, policy); e != nil {
		return nil, e
	}
	p.DescriptionHTML = s.md.RenderBasic(p.DescriptionMD)
	p.IdempotencyKey = stringPointer("poll:create:" + v.Profile + ":" + v.Service + ":" + key)
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, *p.IdempotencyKey); e != nil {
		return nil, e
	}
	if old, err := s.repo.PollByKeyTx(ctx, tx, *p.IdempotencyKey); err == nil {
		if old.Question != p.Question || old.Kind != p.Kind {
			return nil, domain.ErrConflict
		}
		_ = tx.Rollback(ctx)
		return s.GetPoll(ctx, old.ID, v)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if t != nil {
		if e = s.lockPollAnchor(ctx, tx, t.ID, policy.MaxPerAnchor); e != nil {
			return nil, e
		}
	}
	if e = s.pollRate(ctx, "create:h:"+v.Profile, policy.RateLimit.CreatePerHour, 3600); e != nil {
		return nil, e
	}
	if e = s.pollRate(ctx, "create:d:"+v.Profile, 30, 86400); e != nil {
		return nil, e
	}
	if in.Publish {
		if t != nil && t.State == "unverified" {
			return nil, domain.PollFailure("ANCHOR_UNVERIFIED", 409, "anchor")
		}
		p.Status = s.pollPublicationStatus(ctx, p, policy)
	}
	if e = s.repo.SavePollTx(ctx, tx, p, true); e != nil {
		return nil, e
	}
	for _, o := range p.Options {
		if e = s.repo.InsertPollOptionTx(ctx, tx, o); e != nil {
			return nil, e
		}
	}
	if len(in.Invitees) > 0 {
		if e = s.setPollInviteesTx(ctx, tx, p, v, in.Invitees, false, false); e != nil {
			return nil, e
		}
	}
	if e = s.pollEligibleTx(ctx, tx, p); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, "create", map[string]any{}); e != nil {
		return nil, e
	}
	if p.Status == "pending" {
		if e = s.enqueuePollModerationTx(ctx, tx, p, "poll", p.ID, "pre"); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	s.syncInteractionResource(ctx, "poll", p.ID)
	return s.GetPoll(ctx, p.ID, v)
}
func stringPointer(v string) *string { return &v }
func (s *Service) lockPollAnchor(ctx context.Context, tx pgx.Tx, id string, limit int) error {
	var target string
	if e := tx.QueryRow(ctx, `SELECT id FROM comment_targets WHERE id=$1 FOR UPDATE`, id).Scan(&target); e != nil {
		return e
	}
	var n int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM polls WHERE anchor_target_id=$1 AND status<>'deleted'`, id).Scan(&n); e != nil {
		return e
	}
	if n >= limit {
		return domain.PollFailure("POLL_LIMIT", 409, "anchor")
	}
	return nil
}
func (s *Service) pollPublicationStatus(ctx context.Context, p *domain.Poll, policy domain.PollPolicy) string {
	words := s.bannedWords
	if space := p.Space(); space != "" {
		if f, e := s.repo.GetForum(ctx, space); e == nil {
			words = append(slices.Clone(words), f.BannedWords...)
		}
	}
	body := p.Question + " " + p.DescriptionMD
	for _, o := range p.Options {
		body += " " + o.Label
	}
	if policy.ModerationMode == "pre" || matchesBanned(body, words) {
		return "pending"
	}
	return "published"
}
func (s *Service) pollAudit(ctx context.Context, tx pgx.Tx, p *domain.Poll, v pollViewer, action string, detail any) error {
	actor := "profile"
	if v.Service != "" {
		actor = "service:" + v.Service
	} else if v.Profile == "" {
		actor = "system"
	}
	return s.repo.PollAuditTx(ctx, tx, p.ID, actor, v.Profile, action, detail)
}
func (s *Service) pollEligibleTx(ctx context.Context, tx pgx.Tx, p *domain.Poll) error {
	var n int
	switch p.WhoCanVote {
	case "members":
		members, e := s.ident.Members(ctx, p.Space())
		if e != nil {
			return e
		}
		n = len(members)
	case "invited":
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM poll_invitees WHERE poll_id=$1`, p.ID).Scan(&n); e != nil {
			return e
		}
	default:
		p.EligibleCount = nil
		return nil
	}
	p.EligibleCount = &n
	_, e := tx.Exec(ctx, `UPDATE polls SET eligible_count=$2 WHERE id=$1`, p.ID, n)
	return e
}
func (s *Service) PatchPoll(ctx context.Context, id string, v pollViewer, in PollPatch) (map[string]any, error) {
	before, policy, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	creator, _, _, _ := s.pollRoles(ctx, before, v)
	if !creator {
		return nil, domain.ErrForbidden
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	p, e := s.repo.PollTx(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	p.Target = before.Target
	p.Options, e = s.repo.PollOptionsTx(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	oldIdentity := p.IdentityMode
	oldDeadline := p.ClosesAt
	if e = applyPollPatch(p, in); e != nil {
		return nil, e
	}
	if in.Options != nil {
		if p.FirstVoteAt != nil {
			return nil, domain.PollLocked("options")
		}
		preparePollOptions(p, *in.Options)
		p.BallotVersion++
	}
	if e = validatePoll(p, policy); e != nil {
		return nil, e
	}
	if in.Options != nil {
		if _, e = tx.Exec(ctx, `DELETE FROM poll_options WHERE poll_id=$1`, id); e != nil {
			return nil, e
		}
		for i := range p.Options {
			if e = s.repo.InsertPollOptionTx(ctx, tx, p.Options[i]); e != nil {
				return nil, e
			}
		}
	}
	p.DescriptionHTML = s.md.RenderBasic(p.DescriptionMD)
	if p.FirstVoteAt != nil && (in.Question != nil || in.DescriptionMD != nil) {
		p.EditCount++
	}
	if e = s.repo.SavePollTx(ctx, tx, p, false); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, "edit", map[string]any{"previousQuestion": before.Question}); e != nil {
		return nil, e
	}
	if p.IdentityMode != oldIdentity {
		if e = s.pollAudit(ctx, tx, p, v, "identity_changed", map[string]string{"from": oldIdentity, "to": p.IdentityMode}); e != nil {
			return nil, e
		}
	}
	if p.ClosesAt != nil && (oldDeadline == nil || p.ClosesAt.After(*oldDeadline)) {
		if e = s.pollAudit(ctx, tx, p, v, "extend", map[string]any{"closesAt": p.ClosesAt}); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	s.syncInteractionResource(ctx, "poll", id)
	return s.GetPoll(ctx, id, v)
}
func (s *Service) SetPollNotify(ctx context.Context, id, profile string, on bool) error {
	if _, _, e := s.ensurePollReadable(ctx, id, pollViewer{Profile: profile}); e != nil {
		return e
	}
	return s.repo.SetPollNotify(ctx, id, profile, on)
}
