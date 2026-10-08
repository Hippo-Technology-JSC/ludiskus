package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
	"slices"
)

func (s *Service) enqueuePollModerationTx(ctx context.Context, tx pgx.Tx, p *domain.Poll, typ, id, source string) error {
	_, e := tx.Exec(ctx, `INSERT INTO moderation_items(space_uuid,target_type,target_id,source) VALUES($1,$2::report_target,$3,$4)`, nullPollUUID(p.Space()), typ, id, source)
	return e
}
func (s *Service) ReportPoll(ctx context.Context, id, oid string, v pollViewer, in ReportInput) error {
	p, _, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return e
	}
	creator, _, _, _ := s.pollRoles(ctx, p, v)
	if v.Profile == "" || creator {
		return domain.ErrForbidden
	}
	if !slices.Contains([]string{"spam", "abuse", "offtopic", "sexual", "violence", "private_info", "harassment", "hate", "inappropriate", "misleading", "misleading_options", "other"}, in.Reason) {
		return domain.ErrValidation
	}
	if e = s.checkCommentReportRate(ctx, v.Profile); e != nil {
		return e
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	locked, e := s.repo.PollTx(ctx, tx, id, true)
	if e != nil {
		return e
	}
	typ, target := "poll", id
	if oid != "" {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM poll_options WHERE poll_id=$1 AND id=$2)`, id, oid).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return domain.ErrNotFound
		}
		typ, target = "poll_option", oid
	}
	if _, e = tx.Exec(ctx, `INSERT INTO reports(id,space_uuid,target_type,target_id,reporter_profile_uuid,reason,note) VALUES($1,$2,$3::report_target,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, uuid.NewString(), nullPollUUID(p.Space()), typ, target, v.Profile, in.Reason, in.Note); e != nil {
		return e
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM reports WHERE target_type=$1::report_target AND target_id=$2 AND status='open'`, typ, target).Scan(&n); e != nil {
		return e
	}
	threshold := s.cfg.PollAutoHideThreshold
	if p.Space() != "" {
		if f, e := s.repo.GetForum(ctx, p.Space()); e == nil {
			threshold = f.ReportAutoHideThreshold
		}
	}
	if threshold > 0 && n >= threshold {
		if oid == "" && locked.Status != "hidden" {
			if _, e = tx.Exec(ctx, `UPDATE polls SET status_before_hide=status,status='hidden' WHERE id=$1`, id); e != nil {
				return e
			}
		} else if oid != "" {
			if _, e = tx.Exec(ctx, `UPDATE poll_options SET status='hidden' WHERE poll_id=$1 AND id=$2`, id, oid); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE polls SET ballot_version=ballot_version+1 WHERE id=$1`, id); e != nil {
				return e
			}
		}
		if e = s.enqueuePollModerationTx(ctx, tx, p, typ, target, "auto_hide"); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (s *Service) ModeratePollOption(ctx context.Context, id, oid string, v pollViewer, action string) (map[string]any, error) {
	p, _, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	c, o, m, svc := s.pollRoles(ctx, p, v)
	if !c && !o && !m && !svc {
		return nil, domain.ErrForbidden
	}
	status := map[string]string{"approve": "active", "restore": "active", "reject": "hidden", "hide": "hidden"}[action]
	if status == "" {
		return nil, domain.ErrValidation
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = s.repo.PollTx(ctx, tx, id, true); e != nil {
		return nil, e
	}
	tag, e := tx.Exec(ctx, `UPDATE poll_options SET status=$3 WHERE poll_id=$1 AND id=$2`, id, oid, status)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() == 0 {
		return nil, domain.ErrNotFound
	}
	if _, e = tx.Exec(ctx, `UPDATE polls SET ballot_version=ballot_version+1 WHERE id=$1`, id); e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `UPDATE moderation_items SET state=$2::mod_state,decided_by=$3,decided_at=now() WHERE target_type='poll_option' AND target_id=$1 AND state='pending'`, oid, map[string]string{"active": "approved", "hidden": "rejected"}[status], nullPollUUID(v.Profile)); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, "option_"+action, map[string]string{"optionId": oid}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s.GetPoll(ctx, id, v)
}

// The moderation queue can decide pending polls without exposing a pending
// anchor through the public reader. Authorization remains tied to the anchor.
func (s *Service) decidePollModeration(ctx context.Context, item *domain.ModerationItem, profile string, approved bool, note *string) error {
	id := item.TargetID
	if item.TargetType == "poll_option" {
		var e error
		id, e = s.repo.PollOptionParent(ctx, id)
		if e != nil {
			return e
		}
	}
	p, e := s.repo.GetPoll(ctx, id)
	if e != nil {
		return e
	}
	v := pollViewer{Profile: profile}
	_, owner, moderator, _ := s.pollRoles(ctx, p, v)
	if !owner && !moderator {
		return domain.ErrForbidden
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	p, e = s.repo.PollTx(ctx, tx, id, true)
	if e != nil {
		return e
	}
	state, status := "rejected", "rejected"
	if approved {
		state = "approved"
		status = "published"
	}
	if item.TargetType == "poll_option" {
		optionStatus := "hidden"
		if approved {
			optionStatus = "active"
		}
		_, e = tx.Exec(ctx, `UPDATE poll_options SET status=$2 WHERE id=$1`, item.TargetID, optionStatus)
	} else {
		_, e = tx.Exec(ctx, `UPDATE polls SET status=$2,status_before_hide=NULL WHERE id=$1 AND status IN ('pending','hidden')`, id, status)
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE polls SET ballot_version=ballot_version+1 WHERE id=$1`, id); e != nil {
		return e
	}
	decision, e := tx.Exec(ctx, `UPDATE moderation_items SET state=$2::mod_state,decided_by=$3,decided_at=now(),note=$4 WHERE id=$1 AND state='pending'`, item.ID, state, profile, note)
	if e != nil {
		return e
	}
	if decision.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if e = s.pollAudit(ctx, tx, p, v, "moderation_"+state, map[string]any{"targetType": item.TargetType, "targetId": item.TargetID}); e != nil {
		return e
	}
	if p.CreatedBy != nil {
		if e = s.pollNotifyTx(ctx, tx, p, "moderated", "poll:moderated:"+item.ID, []string{*p.CreatedBy}); e != nil {
			return e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	s.syncInteractionResource(ctx, "poll", id)
	return nil
}
