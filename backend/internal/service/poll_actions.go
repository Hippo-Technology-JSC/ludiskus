package service

import (
	"context"
	"ludiskus/internal/domain"
	"time"
)

func (s *Service) PollAction(ctx context.Context, id string, v pollViewer, action string, closesAt *time.Time) (map[string]any, error) {
	before, policy, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	creator, owner, mod, svc := s.pollRoles(ctx, before, v)
	allowed := false
	switch action {
	case "publish":
		allowed = creator
	case "close", "extend", "delete":
		allowed = creator || owner || mod || svc
	case "reopen":
		allowed = creator || mod || svc
	case "hide", "restore":
		allowed = owner || mod || svc
	default:
		return nil, domain.ErrValidation
	}
	if !allowed {
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
	now := time.Now()
	switch action {
	case "publish":
		if p.Status != "draft" {
			return nil, domain.ErrConflict
		}
		if p.Target != nil && p.Target.State != "active" {
			return nil, domain.PollFailure("ANCHOR_UNVERIFIED", 409, "anchor")
		}
		if e = validatePoll(p, policy); e != nil {
			return nil, e
		}
		p.Status = s.pollPublicationStatus(ctx, p, policy)
		if p.Status == "pending" {
			if e = s.enqueuePollModerationTx(ctx, tx, p, "poll", id, "pre"); e != nil {
				return nil, e
			}
		}
		if e = s.notifyPublishedPollInvitesTx(ctx, tx, p); e != nil {
			return nil, e
		}
	case "close":
		if p.Status != "published" {
			if p.Status == "closed" {
				_ = tx.Rollback(ctx)
				return s.GetPoll(ctx, id, v)
			}
			return nil, domain.ErrConflict
		}
		p.Status = "closed"
		p.ClosedAt = &now
		p.ClosedBy = nil
		if v.Profile != "" {
			p.ClosedBy = &v.Profile
		}
		reason := "manual"
		if svc {
			reason = "service"
		}
		p.CloseReason = &reason
	case "reopen":
		if p.EffectiveState(now) != "closed" || p.ReopenCount >= 3 {
			return nil, domain.PollLocked("reopen")
		}
		if p.IdentityMode == "secret" && p.ResultsVisibility == "after_close" {
			return nil, domain.PollLocked("reopen")
		}
		if closesAt == nil || !closesAt.After(now.Add(5*time.Minute)) {
			return nil, domain.ErrValidation
		}
		p.Status = "published"
		p.ClosesAt = closesAt
		p.ClosedAt = nil
		p.ClosedBy = nil
		p.CloseReason = nil
		p.ReopenCount++
		p.RemindedAt = nil
		if _, e = tx.Exec(ctx, `DELETE FROM poll_results WHERE poll_id=$1`, id); e != nil {
			return nil, e
		}
	case "extend":
		if p.Status != "published" || p.EffectiveState(now) == "closed" {
			return nil, domain.PollFailure("POLL_CLOSED", 409, "")
		}
		if closesAt == nil || p.ClosesAt == nil || !closesAt.After(*p.ClosesAt) {
			return nil, domain.PollLocked("closesAt")
		}
		p.ClosesAt = closesAt
		p.RemindedAt = nil
	case "hide":
		if p.Status != "hidden" {
			p.StatusBeforeHide = stringPointer(p.Status)
			p.Status = "hidden"
		}
	case "restore":
		if p.Status != "hidden" || p.StatusBeforeHide == nil {
			return nil, domain.ErrConflict
		}
		p.Status = *p.StatusBeforeHide
		p.StatusBeforeHide = nil
	case "delete":
		p.Status = "deleted"
		p.DeletedAt = &now
	}
	if e = validatePoll(p, policy); e != nil {
		return nil, e
	}
	if action == "publish" || action == "reopen" || action == "close" {
		if e = s.pollEligibleTx(ctx, tx, p); e != nil {
			return nil, e
		}
	}
	if e = s.repo.SavePollTx(ctx, tx, p, false); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, action, map[string]any{"closesAt": closesAt}); e != nil {
		return nil, e
	}
	if action == "close" {
		if e = s.finalizePollTx(ctx, tx, p); e != nil {
			return nil, e
		}
	}
	if (action == "hide" || action == "delete") && !creator && p.CreatedBy != nil {
		if e = s.pollNotifyTx(ctx, tx, p, "moderated", "poll:moderated:"+id+":"+action+":"+p.UpdatedAt.Format(time.RFC3339Nano), []string{*p.CreatedBy}); e != nil {
			return nil, e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	s.syncInteractionResource(ctx, "poll", id)
	if action == "delete" {
		s.invalidateInteractionResource(ctx, "poll", id, "deleted")
		return nil, nil
	}
	return s.GetPoll(ctx, id, v)
}
