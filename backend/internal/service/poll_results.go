package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"ludiskus/internal/domain"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"time"
)

func pollInt(v int) string { return strconv.Itoa(v) }
func makePollResult(p *domain.Poll, ballots [][]domain.PollChoice, final bool) *domain.PollResult {
	t := tallyPoll(p, ballots)
	if p.Kind != "ranked" {
		t = tallyPollCounts(p)
	}
	raw, _ := json.Marshal(t)
	r := &domain.PollResult{Final: final, Method: t.Method, VoterCount: p.VoterCount, EligibleCount: p.EligibleCount, Result: raw, BallotVersion: p.BallotVersion, ComputedAt: time.Now()}
	if p.QuorumPercent != nil {
		met := p.EligibleCount != nil && *p.EligibleCount > 0 && float64(p.VoterCount)*100/float64(*p.EligibleCount) >= float64(*p.QuorumPercent)
		r.QuorumMet = &met
	}
	return r
}
func (s *Service) pollResult(ctx context.Context, p *domain.Poll) (*domain.PollResult, error) {
	start := time.Now()
	defer s.observePoll("ludiskus_poll_tally_duration_seconds", fmt.Sprintf("{method=%q}", p.Kind), start)
	stored, e := s.repo.PollResult(ctx, p.ID)
	if e != nil {
		return nil, e
	}
	closed := p.EffectiveState(time.Now()) == "closed"
	if stored != nil && stored.Final {
		return stored, nil
	}
	anonymous := p.IdentityMode == "anonymous" || p.IdentityMode == "secret"
	snapshot := anonymous || p.Kind == "ranked" && p.VoterCount > s.cfg.PollRankedLiveMax
	if snapshot && !closed && stored != nil {
		interval := s.cfg.PollAnonSnapshotInterval
		if !anonymous {
			interval = time.Minute
		}
		if time.Since(stored.ComputedAt) < interval || anonymous && p.VoterCount-stored.VoterCount < 2 {
			return stored, nil
		}
	}
	if snapshot || closed {
		tx, e := s.repo.BeginPoll(ctx)
		if e != nil {
			return nil, e
		}
		defer tx.Rollback(ctx)
		locked, e := s.repo.PollTx(ctx, tx, p.ID, true)
		if e != nil {
			return nil, e
		}
		locked.Target = p.Target
		locked.Options, e = s.repo.PollOptionsTx(ctx, tx, p.ID)
		if e != nil {
			return nil, e
		}
		stored, e = s.repo.PollResultTx(ctx, tx, p.ID)
		if e != nil {
			return nil, e
		}
		closed = locked.EffectiveState(time.Now()) == "closed"
		if stored != nil && stored.Final {
			return stored, nil
		}
		if !closed && stored != nil {
			interval := s.cfg.PollAnonSnapshotInterval
			if !anonymous {
				interval = time.Minute
			}
			if time.Since(stored.ComputedAt) < interval || anonymous && locked.VoterCount-stored.VoterCount < 2 {
				return stored, nil
			}
		}
		if !closed && anonymous && locked.VoterCount < locked.MinVotersForResults {
			return nil, nil
		}
		var ballots [][]domain.PollChoice
		if locked.Kind == "ranked" {
			ballots, e = s.repo.PollBallotsTx(ctx, tx, locked)
		}
		if e != nil {
			return nil, e
		}
		if closed {
			if e = s.pollEligibleTx(ctx, tx, locked); e != nil {
				return nil, e
			}
		}
		result := makePollResult(locked, ballots, closed)
		if e = s.repo.SavePollResultTx(ctx, tx, p.ID, result); e != nil {
			return nil, e
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return result, nil
	}
	cacheKey := fmt.Sprintf("poll:res:%s:%d", p.ID, p.BallotVersion)
	if s.redis != nil {
		if b, e := s.redis.Get(ctx, cacheKey).Bytes(); e == nil {
			var v domain.PollResult
			if json.Unmarshal(b, &v) == nil {
				return &v, nil
			}
		}
	}
	var ballots [][]domain.PollChoice
	if p.Kind == "ranked" {
		ballots, e = s.repo.PollBallots(ctx, p)
		if e != nil {
			return nil, e
		}
	}
	r := makePollResult(p, ballots, false)
	if s.redis != nil {
		b, _ := json.Marshal(r)
		_ = s.redis.Set(ctx, cacheKey, b, s.cfg.PollResultsCacheTTL).Err()
	}
	return r, nil
}
func (s *Service) GetPoll(ctx context.Context, id string, v pollViewer) (map[string]any, error) {
	p, policy, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	return s.pollView(ctx, p, policy, v)
}
func (s *Service) pollView(ctx context.Context, p *domain.Poll, policy domain.PollPolicy, v pollViewer) (map[string]any, error) {
	creator, owner, mod, svc := s.pollRoles(ctx, p, v)
	var receipt *domain.PollReceipt
	var e error
	if v.Profile != "" {
		receipt, e = s.repo.PollReceipt(ctx, p.ID, v.Profile)
		if e != nil {
			return nil, e
		}
	}
	hasVoted := receipt != nil
	visible, reason := resultsVisible(p, creator, svc, hasVoted, time.Now())
	var result *domain.PollResult
	if visible {
		result, e = s.pollResult(ctx, p)
		if e != nil {
			return nil, e
		}
		if result == nil {
			visible = false
			reason = "min_voters"
		}
	}
	opts := []domain.PollOption{}
	for _, o := range p.Options {
		if o.Status == "active" || !v.Public && (creator || owner || mod || svc) {
			opts = append(opts, o)
		}
	}
	if p.ShuffleOptions {
		hash := sha256.Sum256([]byte(p.ID + ":" + v.Profile))
		rng := rand.New(rand.NewChaCha8(hash))
		rng.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	}
	voterCount := p.VoterCount
	anonOpen := (p.IdentityMode == "anonymous" || p.IdentityMode == "secret") && p.EffectiveState(time.Now()) != "closed"
	if anonOpen {
		voterCount = 0
		if result != nil {
			voterCount = result.VoterCount
		}
	}
	var count any = voterCount
	if p.ResultsVisibility == "owner_only" && !creator && !svc {
		count = nil
	}
	var turnout any
	if count != nil && p.EligibleCount != nil && *p.EligibleCount > 0 {
		turnout = math.Min(1, float64(voterCount)/float64(*p.EligibleCount))
	}
	out := map[string]any{"id": p.ID, "kind": p.Kind, "question": p.Question, "descriptionMd": p.DescriptionMD, "descriptionHtml": p.DescriptionHTML, "config": p.Rules(), "identityMode": p.IdentityMode, "resultsVisibility": p.ResultsVisibility, "whoCanVote": p.WhoCanVote, "allowChangeVote": p.AllowChangeVote, "allowRetract": p.AllowRetract, "allowUserOptions": p.AllowUserOptions, "shuffleOptions": p.ShuffleOptions, "remind": p.Remind, "minVotersForResults": p.MinVotersForResults, "state": p.EffectiveState(time.Now()), "opensAt": p.OpensAt, "closesAt": p.ClosesAt, "closedAt": p.ClosedAt, "closeReason": p.CloseReason, "options": opts, "voterCount": count, "eligibleCount": p.EligibleCount, "turnout": turnout, "quorumPercent": p.QuorumPercent, "results": nil, "resultsHiddenReason": reason, "anchor": nil, "standalone": nil, "firstVoteAt": p.FirstVoteAt}
	if p.IdentityMode == "anonymous" || p.IdentityMode == "secret" {
		out["firstVoteAt"] = nil
		out["optionsLocked"] = p.FirstVoteAt != nil
	}
	if visible {
		out["results"] = result
	}
	if p.Target != nil {
		out["anchor"] = map[string]any{"service": p.Target.ServiceCode, "type": p.Target.ResourceType, "id": p.Target.ResourceID, "title": p.Target.Title, "canonicalPath": p.Target.CanonicalPath}
	} else {
		out["standalone"] = map[string]any{"visibility": p.Visibility, "spaceUuid": p.SpaceUUID}
	}
	author := map[string]any{"kind": p.AuthorKind, "name": "", "avatar": ""}
	if p.CreatedBy != nil {
		if pr, e := s.ident.Profile(ctx, *p.CreatedBy); e == nil {
			author["name"] = pr.Name
			author["avatar"] = pr.Avatar
			if !v.Public {
				author["code"] = pr.Code
			}
		}
	}
	if p.AuthorKind == "space" && p.AuthorSpaceUUID != nil {
		if sp, e := s.ident.Space(ctx, *p.AuthorSpaceUUID); e == nil {
			author["name"] = sp.Name
		}
	}
	out["author"] = author
	history, e := s.repo.PollHistory(ctx, p.ID)
	if e != nil {
		return nil, e
	}
	history["editCount"] = p.EditCount
	out["history"] = history
	if !v.Public {
		canVote := v.Service == "" && s.ensurePollVotable(ctx, p, policy, v.Profile) == nil
		choices := []domain.PollChoice{}
		notify := true
		var votedAt any
		if receipt != nil {
			notify = receipt.NotifyResult
			votedAt = receipt.VotedAt
			if p.IdentityMode != "secret" {
				choices = receipt.Choices
			}
		}
		state := p.EffectiveState(time.Now())
		canAddOption := (state == "draft" || state == "scheduled" || state == "open") && p.Kind != "scale" && !(p.Kind == "ranked" && p.FirstVoteAt != nil) && (creator || p.AllowUserOptions && canVote)
		out["viewer"] = map[string]any{"hasVoted": hasVoted, "choices": choices, "notifyResult": notify, "votedAt": votedAt, "canVote": canVote && (!hasVoted || p.AllowChangeVote), "canChange": canVote && hasVoted && p.AllowChangeVote, "canRetract": canVote && hasVoted && p.AllowRetract, "canAddOption": canAddOption}
		viewVoters := visible && (p.IdentityMode == "public" || p.IdentityMode == "owner_only" && (creator || svc))
		out["capabilities"] = map[string]bool{"edit": creator, "close": creator || owner || mod || svc, "reopen": creator || mod || svc, "hide": owner || mod || svc, "delete": creator || owner || mod || svc, "export": creator || svc, "viewVoters": viewVoters, "viewParticipants": creator || svc || mod && p.WhoCanVote == "members", "invite": creator || svc, "report": !creator && v.Profile != ""}
	}
	// Hash the actual authorized representation. Anonymous counts and results are
	// snapshotted, so the version cannot act as a per-ballot timing oracle.
	hashOut := map[string]any{}
	for k, x := range out {
		hashOut[k] = x
	}
	if anonOpen {
		delete(hashOut, "firstVoteAt")
		delete(hashOut, "optionsLocked")
	}
	raw, _ := json.Marshal(hashOut)
	sum := sha256.Sum256(raw)
	out["version"] = hex.EncodeToString(sum[:16])
	return out, nil
}
func (s *Service) PollPeople(ctx context.Context, id string, v pollViewer, option, cursor string, participants bool) (map[string]any, error) {
	p, _, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	c, _, m, svc := s.pollRoles(ctx, p, v)
	if participants {
		if !c && !svc && !(m && p.WhoCanVote == "members") {
			return nil, domain.ErrForbidden
		}
		option = ""
	} else {
		visible, _ := resultsVisible(p, c, svc, v.Profile != "" && s.hasPollReceipt(ctx, id, v.Profile), time.Now())
		if !visible || p.IdentityMode == "anonymous" || p.IdentityMode == "secret" || p.IdentityMode == "owner_only" && !c && !svc {
			return nil, domain.ErrForbidden
		}
	}
	items, next, e := s.repo.PollPeople(ctx, id, option, cursor, participants)
	if e != nil {
		return nil, e
	}
	return map[string]any{"data": items, "nextCursor": next}, nil
}
func (s *Service) hasPollReceipt(ctx context.Context, id, profile string) bool {
	v, e := s.repo.PollReceipt(ctx, id, profile)
	return e == nil && v != nil
}
func (s *Service) ListPolls(ctx context.Context, v pollViewer, ref *domain.ResourceRef, role, space, cursor, state string) (map[string]any, error) {
	if !s.cfg.PollEnabled {
		return nil, domain.ErrNotFound
	}
	anchor := ""
	create := false
	if ref == nil {
		_, e := s.ensurePollCreatable(ctx, nil, space, v)
		create = e == nil
	}
	if ref != nil {
		t, e := s.ensureCommentTarget(ctx, *ref, v.Profile)
		if e != nil {
			return nil, e
		}
		if v.Service != "" && v.Service != ref.Service {
			return nil, domain.ErrServiceScope
		}
		if v.Service == "" {
			if e = s.ensureTargetReadable(ctx, t, v.Profile); e != nil {
				return nil, e
			}
		}
		anchor = t.ID
		_, e = s.ensurePollCreatable(ctx, t, "", v)
		create = e == nil
	}
	profile := v.Profile
	if ref != nil || space != "" {
		profile = ""
	}
	if ref == nil && space == "" && profile == "" {
		return nil, domain.ErrUnauthorized
	}
	if role == "" {
		role = "created"
	}
	ids, e := s.repo.PollListIDs(ctx, anchor, profile, role, space, cursor, 51)
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	next := ""
	for i, id := range ids {
		if i == 50 {
			next = ids[49]
			break
		}
		p, policy, e := s.ensurePollReadable(ctx, id, v)
		if e != nil {
			if isPollAccessFailure(e) {
				continue
			}
			return nil, e
		}
		if state != "" && p.EffectiveState(time.Now()) != state {
			continue
		}
		if v.Public && p.Visibility != nil && *p.Visibility == "unlisted" {
			continue
		}
		view, e := s.pollView(ctx, p, policy, v)
		if e != nil {
			return nil, e
		}
		out = append(out, view)
	}
	return map[string]any{"polls": out, "canCreate": create, "capabilities": map[string]bool{"create": create}, "nextCursor": next}, nil
}
func isPollAccessFailure(e error) bool {
	if errorsIsPollAccess(e) {
		return true
	}
	pe, ok := e.(*domain.PollError)
	return ok && (pe.Status == 403 || pe.Status == 404 || pe.Status == 410)
}
func errorsIsPollAccess(e error) bool {
	return e == domain.ErrForbidden || e == domain.ErrNotFound || e == domain.ErrResourceBlocked || e == domain.ErrResourceGone
}
func (s *Service) PollSummary(ctx context.Context, v pollViewer, ids []string, refs []domain.ResourceRef) ([]map[string]any, error) {
	if len(ids)+len(refs) > 100 {
		return nil, domain.ErrValidation
	}
	for _, ref := range refs {
		page, e := s.ListPolls(ctx, v, &ref, "", "", "", "")
		if e != nil {
			if isPollAccessFailure(e) {
				continue
			}
			return nil, e
		}
		for _, p := range page["polls"].([]map[string]any) {
			ids = append(ids, p["id"].(string))
		}
	}
	sort.Strings(ids)
	out := []map[string]any{}
	prev := ""
	for _, id := range ids {
		if id == prev {
			continue
		}
		prev = id
		p, e := s.GetPoll(ctx, id, v)
		if e != nil {
			if isPollAccessFailure(e) {
				continue
			}
			return nil, e
		}
		summary := map[string]any{}
		for _, key := range []string{"id", "question", "state", "voterCount", "version"} {
			summary[key] = p[key]
		}
		if viewer, ok := p["viewer"].(map[string]any); ok {
			summary["hasVoted"] = viewer["hasVoted"]
		}
		out = append(out, summary)
	}
	return out, nil
}
