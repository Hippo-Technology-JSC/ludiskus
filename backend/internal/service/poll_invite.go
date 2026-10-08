package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
)

func (s *Service) setPollInviteesTx(ctx context.Context, tx pgx.Tx, p *domain.Poll, v pollViewer, profiles []string, remove, replace bool) error {
	if p.WhoCanVote != "invited" || len(profiles) > 500 {
		return domain.ErrValidation
	}
	seen := map[string]bool{}
	for _, id := range profiles {
		if _, e := uuid.Parse(id); e != nil || seen[id] {
			return domain.ErrValidation
		}
		seen[id] = true
	}
	if replace {
		if _, e := tx.Exec(ctx, `DELETE FROM poll_invitees WHERE poll_id=$1 AND NOT(profile_uuid=ANY($2::uuid[]))`, p.ID, profiles); e != nil {
			return e
		}
	}
	added := []string{}
	for _, id := range profiles {
		if remove {
			if _, e := tx.Exec(ctx, `DELETE FROM poll_invitees WHERE poll_id=$1 AND profile_uuid=$2`, p.ID, id); e != nil {
				return e
			}
		} else {
			tag, e := tx.Exec(ctx, `INSERT INTO poll_invitees(poll_id,profile_uuid,invited_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, p.ID, id, nullPollUUID(v.Profile))
			if e != nil {
				return e
			}
			if tag.RowsAffected() > 0 {
				added = append(added, id)
			}
		}
	}
	var n int
	if e := tx.QueryRow(ctx, `SELECT count(*) FROM poll_invitees WHERE poll_id=$1`, p.ID).Scan(&n); e != nil {
		return e
	}
	if n > 2000 {
		return domain.PollFailure("POLL_LIMIT", 409, "invitees")
	}
	p.EligibleCount = &n
	if _, e := tx.Exec(ctx, `UPDATE polls SET eligible_count=$2 WHERE id=$1`, p.ID, n); e != nil {
		return e
	}
	if p.Status != "draft" && len(added) > 0 {
		for _, id := range added {
			if e := s.pollNotifyTx(ctx, tx, p, "invited", "poll:invited:"+p.ID+":"+id, []string{id}); e != nil {
				return e
			}
		}
	}
	return nil
}
func nullPollUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (s *Service) PollInvitees(ctx context.Context, id string, v pollViewer, profiles []string, remove, replace bool) (map[string]any, error) {
	before, _, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	creator, _, _, svc := s.pollRoles(ctx, before, v)
	if !creator && !svc {
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
	if e = s.setPollInviteesTx(ctx, tx, p, v, profiles, remove, replace); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, "invite", map[string]any{"count": len(profiles), "removed": remove}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return s.GetPoll(ctx, id, v)
}

func (s *Service) notifyPublishedPollInvitesTx(ctx context.Context, tx pgx.Tx, p *domain.Poll) error {
	if p.Status != "published" || p.WhoCanVote != "invited" {
		return nil
	}
	rows, e := tx.Query(ctx, `SELECT profile_uuid::text FROM poll_invitees WHERE poll_id=$1 ORDER BY profile_uuid`, p.ID)
	if e != nil {
		return e
	}
	profiles := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		profiles = append(profiles, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range profiles {
		if e = s.pollNotifyTx(ctx, tx, p, "invited", "poll:invited:"+p.ID+":"+id, []string{id}); e != nil {
			return e
		}
	}
	return nil
}
