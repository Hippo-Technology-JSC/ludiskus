package service

import (
	"context"
	"github.com/google/uuid"
	"ludiskus/internal/domain"
	"strings"
	"time"
)

func (s *Service) PollOptions(ctx context.Context, id string, v pollViewer, options []domain.PollOption, replace bool) (map[string]any, error) {
	before, policy, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	creator, _, _, _ := s.pollRoles(ctx, before, v)
	if replace && !creator {
		return nil, domain.ErrForbidden
	}
	if !creator {
		if !before.AllowUserOptions {
			return nil, domain.ErrForbidden
		}
		if e = s.ensurePollVotable(ctx, before, policy, v.Profile); e != nil {
			return nil, e
		}
		if e = s.pollRate(ctx, "option:"+v.Profile+":"+id, 3, 86400); e != nil {
			return nil, e
		}
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
	state := p.EffectiveState(time.Now())
	if state != "draft" && state != "scheduled" && state != "open" {
		return nil, domain.PollLocked("options")
	}
	if replace && p.FirstVoteAt != nil {
		return nil, domain.PollLocked("options")
	}
	if !replace && (p.Kind == "scale" || p.Kind == "ranked" && p.FirstVoteAt != nil) {
		return nil, domain.PollLocked("options")
	}
	if !replace && len(options) != 1 {
		return nil, domain.ErrValidation
	}
	insert := []domain.PollOption{}
	if replace {
		preparePollOptions(p, options)
		insert = p.Options
	} else {
		for _, o := range options {
			o.ID = uuid.NewString()
			o.PollID = id
			o.Position = len(p.Options)
			o.Label = strings.TrimSpace(o.Label)
			o.LabelNorm = normalizePollLabel(o.Label)
			o.Status = "active"
			if !creator {
				o.AddedBy = &v.Profile
				if policy.ModerationMode == "pre" || p.IdentityMode == "anonymous" || p.IdentityMode == "secret" || matchesBanned(o.Label, s.bannedWords) {
					o.Status = "pending"
				}
			}
			p.Options = append(p.Options, o)
			insert = append(insert, o)
		}
	}
	if e = validatePoll(p, policy); e != nil {
		return nil, e
	}
	if replace {
		if _, e = tx.Exec(ctx, `DELETE FROM poll_options WHERE poll_id=$1`, id); e != nil {
			return nil, e
		}
	}
	for _, o := range insert {
		if e = s.repo.InsertPollOptionTx(ctx, tx, o); e != nil {
			return nil, e
		}
		if o.Status == "pending" {
			if e = s.enqueuePollModerationTx(ctx, tx, p, "poll_option", o.ID, "pre"); e != nil {
				return nil, e
			}
			if p.CreatedBy != nil {
				key := "poll:option_pending:" + id + ":" + pollInt(int(time.Now().Unix()/600))
				if e = s.pollNotifyTx(ctx, tx, p, "option_pending", key, []string{*p.CreatedBy}); e != nil {
					return nil, e
				}
			}
		}
	}
	p.BallotVersion++
	if e = s.repo.SavePollTx(ctx, tx, p, false); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, "options", map[string]bool{"replace": replace}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s.GetPoll(ctx, id, v)
}
