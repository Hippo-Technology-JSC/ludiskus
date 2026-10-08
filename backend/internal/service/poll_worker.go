package service

import (
	"context"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
	"time"
)

func (s *Service) claimPoll(ctx context.Context, tx pgx.Tx, id string) (*domain.Poll, error) {
	var locked string
	e := tx.QueryRow(ctx, `SELECT id FROM polls WHERE id=$1 FOR UPDATE SKIP LOCKED`, id).Scan(&locked)
	if e != nil {
		return nil, e
	}
	p, e := s.repo.PollTx(ctx, tx, id, false)
	if e != nil {
		return nil, e
	}
	p.Options, e = s.repo.PollOptionsTx(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	if p.AnchorTargetID != nil {
		p.Target, e = s.repo.GetCommentTargetByID(ctx, *p.AnchorTargetID)
	}
	return p, e
}
func (s *Service) CloseDuePolls(ctx context.Context) (int, error) {
	if !s.cfg.PollEnabled {
		return 0, nil
	}
	ids, e := s.repo.PollCandidateIDs(ctx, "close", 100)
	if e != nil {
		return 0, e
	}
	n := 0
	for _, id := range ids {
		tx, e := s.repo.BeginPoll(ctx)
		if e != nil {
			return n, e
		}
		p, e := s.claimPoll(ctx, tx, id)
		if e == pgx.ErrNoRows {
			_ = tx.Rollback(ctx)
			continue
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			return n, e
		}
		now := time.Now()
		if p.Status != "published" || p.ClosesAt == nil || p.ClosesAt.After(now) {
			_ = tx.Rollback(ctx)
			continue
		}
		s.observePoll("ludiskus_poll_close_lag_seconds", "", *p.ClosesAt)
		p.Status = "closed"
		p.ClosedAt = &now
		p.CloseReason = stringPointer("deadline")
		if e = s.pollEligibleTx(ctx, tx, p); e == nil {
			e = s.repo.SavePollTx(ctx, tx, p, false)
		}
		if e == nil {
			e = s.finalizePollTx(ctx, tx, p)
		}
		if e == nil {
			e = s.pollAudit(ctx, tx, p, pollViewer{}, "close", map[string]string{"reason": "deadline"})
		}
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e != nil {
			return n, e
		}
		n++
	}
	return n, nil
}
func (s *Service) RemindPolls(ctx context.Context) (int, error) {
	if !s.cfg.PollEnabled {
		return 0, nil
	}
	ids, e := s.repo.PollCandidateIDs(ctx, "remind", 100)
	if e != nil {
		return 0, e
	}
	n := 0
	for _, id := range ids {
		tx, e := s.repo.BeginPoll(ctx)
		if e != nil {
			return n, e
		}
		p, e := s.claimPoll(ctx, tx, id)
		if e == pgx.ErrNoRows {
			_ = tx.Rollback(ctx)
			continue
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			return n, e
		}
		if p.Status != "published" || p.RemindedAt != nil || p.EffectiveState(time.Now()) != "open" {
			_ = tx.Rollback(ctx)
			continue
		}
		profiles := []string{}
		if p.WhoCanVote == "members" {
			members, e := s.ident.Members(ctx, p.Space())
			if e != nil {
				_ = tx.Rollback(ctx)
				return n, e
			}
			for _, m := range members {
				profiles = append(profiles, m.ProfileUUID)
			}
		} else {
			rows, e := tx.Query(ctx, `SELECT profile_uuid::text FROM poll_invitees WHERE poll_id=$1`, id)
			if e != nil {
				_ = tx.Rollback(ctx)
				return n, e
			}
			for rows.Next() {
				var profile string
				if e = rows.Scan(&profile); e != nil {
					break
				}
				profiles = append(profiles, profile)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				_ = tx.Rollback(ctx)
				return n, e
			}
		}
		rows, e := tx.Query(ctx, `SELECT profile_uuid::text FROM poll_voters WHERE poll_id=$1`, id)
		if e != nil {
			_ = tx.Rollback(ctx)
			return n, e
		}
		voted := map[string]bool{}
		for rows.Next() {
			var profile string
			if e = rows.Scan(&profile); e != nil {
				break
			}
			voted[profile] = true
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			_ = tx.Rollback(ctx)
			return n, e
		}
		remaining := []string{}
		for _, profile := range profiles {
			if !voted[profile] {
				remaining = append(remaining, profile)
			}
		}
		e = s.pollNotifyTx(ctx, tx, p, "closing_soon", "poll:remind:"+id+":r"+pollInt(p.ReopenCount), remaining)
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE polls SET reminded_at=now() WHERE id=$1`, id)
		}
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e != nil {
			return n, e
		}
		n++
	}
	return n, nil
}
func (s *Service) SweepPolls(ctx context.Context) error {
	if !s.cfg.PollEnabled {
		return nil
	}
	if e := s.repo.PollSweep(ctx, s.cfg.PollDraftTTL); e != nil {
		return e
	}
	ids, e := s.repo.PollCandidateIDs(ctx, "gone", 100)
	if e != nil {
		return e
	}
	for _, id := range ids {
		tx, e := s.repo.BeginPoll(ctx)
		if e != nil {
			return e
		}
		p, e := s.claimPoll(ctx, tx, id)
		if e == pgx.ErrNoRows {
			_ = tx.Rollback(ctx)
			continue
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			return e
		}
		if p.Target == nil || p.Target.State != "gone" || (p.Status != "published" && p.Status != "pending") {
			_ = tx.Rollback(ctx)
			continue
		}
		now := time.Now()
		p.Status = "closed"
		p.ClosedAt = &now
		p.CloseReason = stringPointer("anchor_gone")
		e = s.repo.SavePollTx(ctx, tx, p, false)
		if e == nil {
			ballots, err := s.repo.PollBallotsTx(ctx, tx, p)
			e = err
			if e == nil {
				e = s.repo.SavePollResultTx(ctx, tx, p.ID, makePollResult(p, ballots, true))
			}
		}
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e != nil {
			return e
		}
	}
	ids, e = s.repo.PollCandidateIDs(ctx, "snapshot", 1000)
	if e != nil {
		return e
	}
	for _, id := range ids {
		p, e := s.repo.GetPoll(ctx, id)
		if e != nil {
			return e
		}
		if p.VoterCount >= p.MinVotersForResults {
			if _, e = s.pollResult(ctx, p); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Service) ReconcilePolls(ctx context.Context) (int, error) {
	if !s.cfg.PollEnabled {
		return 0, nil
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	var yes bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('poll:reconcile',0))`).Scan(&yes); e != nil || !yes {
		return 0, e
	}
	rows, e := tx.Query(ctx, `SELECT id::text FROM polls WHERE status<>'draft' AND NOT(status='deleted' AND deleted_at<now()-interval '180 days') ORDER BY id`)
	if e != nil {
		return 0, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return 0, e
	}
	fixed := 0
	for _, id := range ids {
		p, e := s.repo.PollTx(ctx, tx, id, true)
		if e != nil {
			return fixed, e
		}
		final, e := s.repo.PollResultTx(ctx, tx, id)
		if e != nil {
			return fixed, e
		}
		if final != nil && final.Final {
			continue
		}
		p.Options, e = s.repo.PollOptionsTx(ctx, tx, id)
		if e != nil {
			return fixed, e
		}
		ballots, e := s.repo.PollBallotsTx(ctx, tx, p)
		if e != nil {
			return fixed, e
		}
		counts := map[string]struct{ votes, maybe int }{}
		for _, b := range ballots {
			for _, c := range b {
				v := counts[c.OptionID]
				if p.Kind == "ranked" && c.Rank != 1 {
					continue
				}
				if c.Answer == "maybe" {
					v.maybe++
				} else {
					v.votes++
				}
				counts[c.OptionID] = v
			}
		}
		changed := 0
		for _, o := range p.Options {
			c := counts[o.ID]
			if o.VoteCount != c.votes || o.MaybeCount != c.maybe {
				if _, e = tx.Exec(ctx, `UPDATE poll_options SET vote_count=$2,maybe_count=$3 WHERE id=$1`, o.ID, c.votes, c.maybe); e != nil {
					return fixed, e
				}
				changed++
			}
		}
		var actual int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM poll_voters WHERE poll_id=$1`, id).Scan(&actual); e != nil {
			return fixed, e
		}
		if p.VoterCount != actual {
			changed++
			if _, e = tx.Exec(ctx, `UPDATE polls SET voter_count=$2,ballot_version=ballot_version+1 WHERE id=$1`, id, actual); e != nil {
				return fixed, e
			}
		}
		if changed > 0 {
			if p.VoterCount == actual {
				if _, e = tx.Exec(ctx, `UPDATE polls SET ballot_version=ballot_version+1 WHERE id=$1`, id); e != nil {
					return fixed, e
				}
			}
			if e = s.pollAudit(ctx, tx, p, pollViewer{}, "reconcile", map[string]int{"fixed": changed}); e != nil {
				return fixed, e
			}
			fixed += changed
			for j := 0; j < changed; j++ {
				s.observePoll("ludiskus_poll_reconcile_fixed_total", "", time.Now())
			}
		}
		var total, newProfiles int
		if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE c.created_at>v.voted_at-make_interval(hours=>$2)) FROM poll_voters v LEFT JOIN profile_cache c ON c.profile_uuid=v.profile_uuid WHERE v.poll_id=$1`, id, s.cfg.PollNewProfileHours).Scan(&total, &newProfiles); e != nil {
			return fixed, e
		}
		var burst int
		if e = tx.QueryRow(ctx, `SELECT COALESCE(max(n),0) FROM (SELECT count(*) n FROM poll_voters WHERE poll_id=$1 GROUP BY date_trunc('minute',voted_at) HAVING count(*)>50) b`, id).Scan(&burst); e != nil {
			return fixed, e
		}
		if total >= 10 && newProfiles*100 > total*30 || burst > 50 {
			var existing bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM poll_audit_logs WHERE poll_id=$1 AND action='abuse_flag')`, id).Scan(&existing); e != nil {
				return fixed, e
			}
			if !existing {
				if e = s.pollAudit(ctx, tx, p, pollViewer{}, "abuse_flag", map[string]any{"signal": "new_profiles_or_vote_burst", "count": newProfiles, "total": total, "peakMinute": burst}); e != nil {
					return fixed, e
				}
			}
		}
	}
	return fixed, tx.Commit(ctx)
}
