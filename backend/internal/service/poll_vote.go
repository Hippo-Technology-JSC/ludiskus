package service

import (
	"context"
	"fmt"
	"ludiskus/internal/domain"
	"slices"
	"sort"
	"time"
)

func validatePollBallot(p *domain.Poll, choices []domain.PollChoice) ([]domain.PollChoice, error) {
	bad := func(field string) ([]domain.PollChoice, error) {
		return nil, domain.PollFailure("VOTE_INVALID", 422, field)
	}
	opts := map[string]bool{}
	for _, o := range p.Options {
		opts[o.ID] = o.Status == "active"
	}
	seen := map[string]bool{}
	ranks := map[int]bool{}
	c := p.Rules()
	for _, v := range choices {
		if !opts[v.OptionID] || seen[v.OptionID] {
			return bad("optionId")
		}
		seen[v.OptionID] = true
		if p.Kind == "schedule" {
			if v.Rank != 0 || !slices.Contains([]string{"yes", "maybe"}, v.Answer) || (v.Answer == "maybe" && !c.AllowMaybe) {
				return bad("answer")
			}
		} else if v.Answer != "" {
			return bad("answer")
		}
		if p.Kind == "ranked" {
			if v.Rank < 1 || v.Rank > len(choices) || ranks[v.Rank] {
				return bad("rank")
			}
			ranks[v.Rank] = true
		} else if v.Rank != 0 {
			return bad("rank")
		}
	}
	switch p.Kind {
	case "single", "scale":
		if len(choices) != 1 {
			return bad("choices")
		}
	case "multiple":
		if len(choices) < c.MinChoices || len(choices) > c.MaxChoices {
			return bad("choices")
		}
	case "ranked":
		if len(choices) < 1 || len(choices) > c.MaxRanks {
			return bad("choices")
		}
	case "schedule":
		if len(choices) > len(p.Options) {
			return bad("answers")
		}
	default:
		return bad("kind")
	}
	out := slices.Clone(choices)
	sort.Slice(out, func(i, j int) bool {
		if p.Kind == "ranked" {
			return out[i].Rank < out[j].Rank
		}
		return out[i].OptionID < out[j].OptionID
	})
	return out, nil
}
func (s *Service) VotePoll(ctx context.Context, id, profile, key string, in domain.PollVoteInput, retract bool) (view map[string]any, voteErr error) {
	start := time.Now()
	kind, identity := "unknown", "unknown"
	defer func() {
		labels := fmt.Sprintf("{kind=%q,identity_mode=%q,result=%q}", kind, identity, pollVoteOutcome(voteErr))
		s.observePoll("ludiskus_poll_votes_total", labels, start)
		s.observePoll("ludiskus_poll_vote_duration_seconds", "", start)
	}()
	if !retract {
		if e := domain.ValidatePollKey(key); e != nil {
			return nil, e
		}
	}
	p, policy, e := s.ensurePollReadable(ctx, id, pollViewer{Profile: profile})
	if e != nil {
		return nil, e
	}
	kind, identity = p.Kind, p.IdentityMode
	if e = s.ensurePollVotable(ctx, p, policy, profile); e != nil {
		return nil, e
	}
	if p.Kind == "schedule" && len(in.Choices) > 0 || p.Kind != "schedule" && len(in.Answers) > 0 {
		return nil, domain.PollFailure("VOTE_INVALID", 422, "choices")
	}
	if e = s.pollRate(ctx, "vote:m:"+profile, policy.RateLimit.VotesPerMinute, 60); e != nil {
		return nil, e
	}
	if receipt, e := s.repo.PollReceipt(ctx, id, profile); e != nil {
		return nil, e
	} else if receipt != nil {
		if e = s.pollRate(ctx, "flip:"+profile+":"+id, 10, 3600); e != nil {
			return nil, e
		}
	}
	e = s.repo.WritePollVote(ctx, id, profile, key, in, retract, validatePollBallot)
	if e != nil {
		return nil, e
	}
	return s.GetPoll(ctx, id, pollViewer{Profile: profile})
}
