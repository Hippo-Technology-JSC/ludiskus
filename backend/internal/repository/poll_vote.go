package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
	"sort"
)

func (r *Repo) PollReceipt(ctx context.Context, id, profile string) (*domain.PollReceipt, error) {
	return pollReceiptTx(ctx, r.pool, id, profile)
}
func pollReceiptTx(ctx context.Context, q pollQuerier, id, profile string) (*domain.PollReceipt, error) {
	var v domain.PollReceipt
	e := q.QueryRow(ctx, `SELECT profile_uuid,idempotency_key,notify_result,voted_at FROM poll_voters WHERE poll_id=$1 AND profile_uuid=$2`, id, profile).Scan(&v.ProfileUUID, &v.IdempotencyKey, &v.NotifyResult, &v.VotedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	v.Choices, e = pollChoicesTx(ctx, q, id, profile)
	return &v, e
}
func pollChoicesTx(ctx context.Context, q pollQuerier, id, profile string) ([]domain.PollChoice, error) {
	rows, e := q.Query(ctx, `SELECT option_id,coalesce(rank,0),coalesce(answer,'') FROM poll_votes WHERE poll_id=$1 AND voter_profile_uuid=$2 ORDER BY rank NULLS LAST,option_id`, id, profile)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.PollChoice{}
	for rows.Next() {
		var c domain.PollChoice
		if e = rows.Scan(&c.OptionID, &c.Rank, &c.Answer); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// The UPDATE is the first poll lock in every write. Revalidate the ballot
// against the locked configuration so an edit racing the first vote cannot win.
func (r *Repo) WritePollVote(ctx context.Context, id, profile, key string, in domain.PollVoteInput, retract bool, validate func(*domain.Poll, []domain.PollChoice) ([]domain.PollChoice, error)) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var lockedID string
	e = tx.QueryRow(ctx, `UPDATE polls SET ballot_version=ballot_version+1 WHERE id=$1 AND status='published' AND (anchor_target_id IS NULL OR EXISTS(SELECT 1 FROM comment_targets t WHERE t.id=anchor_target_id AND t.state='active')) AND (who_can_vote<>'invited' OR EXISTS(SELECT 1 FROM poll_invitees i WHERE i.poll_id=polls.id AND i.profile_uuid=$2)) AND (opens_at IS NULL OR opens_at<=statement_timestamp()) AND (closes_at IS NULL OR closes_at>statement_timestamp()) RETURNING id`, id, profile).Scan(&lockedID)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.PollFailure("POLL_CLOSED", 409, "")
	}
	if e != nil {
		return e
	}
	p, e := r.PollTx(ctx, tx, id, false)
	if e != nil {
		return e
	}
	// Waiting for another transaction must not admit a vote past the deadline.
	var open bool
	e = tx.QueryRow(ctx, `SELECT (opens_at IS NULL OR opens_at<=clock_timestamp()) AND (closes_at IS NULL OR closes_at>clock_timestamp()) FROM polls WHERE id=$1`, id).Scan(&open)
	if e != nil {
		return e
	}
	if !open {
		return domain.PollFailure("POLL_CLOSED", 409, "")
	}
	p.Options, e = r.PollOptionsTx(ctx, tx, id)
	if e != nil {
		return e
	}
	receipt, e := pollReceiptTx(ctx, tx, id, profile)
	if e != nil {
		return e
	}
	if !retract && receipt != nil && receipt.IdempotencyKey == key {
		return nil
	}
	if retract {
		if p.IdentityMode == "secret" || !p.AllowRetract {
			return domain.PollLocked("allowRetract")
		}
		if receipt == nil {
			return nil
		}
	} else if receipt != nil && (p.IdentityMode == "secret" || !p.AllowChangeVote) {
		return domain.PollFailure("ALREADY_VOTED", 409, "")
	}
	choices := in.Choices
	if p.Kind == "schedule" {
		choices = in.Answers
	}
	if !retract {
		choices, e = validate(p, choices)
		if e != nil {
			return e
		}
	} else {
		choices = nil
	}
	old := []domain.PollChoice{}
	if receipt != nil {
		old = receipt.Choices
	}
	deltas := pollDeltas(p.Kind, old, choices)
	delta := 0
	if retract {
		delta = -1
		_, e = tx.Exec(ctx, `DELETE FROM poll_voters WHERE poll_id=$1 AND profile_uuid=$2`, id, profile)
	} else {
		notify := true
		if in.NotifyResult != nil {
			notify = *in.NotifyResult
		}
		if receipt == nil {
			delta = 1
		}
		_, e = tx.Exec(ctx, `INSERT INTO poll_voters(poll_id,profile_uuid,idempotency_key,notify_result) VALUES($1,$2,$3,$4) ON CONFLICT(poll_id,profile_uuid) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key,notify_result=EXCLUDED.notify_result,updated_at=now()`, id, profile, key, notify)
		if e == nil && p.IdentityMode == "secret" {
			b, _ := json.Marshal(choices)
			_, e = tx.Exec(ctx, `INSERT INTO poll_ballots(poll_id,choices) VALUES($1,$2)`, id, b)
		} else if e == nil {
			_, e = tx.Exec(ctx, `DELETE FROM poll_votes WHERE poll_id=$1 AND voter_profile_uuid=$2`, id, profile)
			for _, c := range choices {
				if e != nil {
					break
				}
				var rank, answer any
				if c.Rank > 0 {
					rank = c.Rank
				}
				if c.Answer != "" {
					answer = c.Answer
				}
				_, e = tx.Exec(ctx, `INSERT INTO poll_votes(poll_id,voter_profile_uuid,option_id,rank,answer) VALUES($1,$2,$3,$4,$5)`, id, profile, c.OptionID, rank, answer)
			}
		}
	}
	if e != nil {
		return e
	}
	if e = applyOptionDeltas(ctx, tx, id, deltas); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE polls SET voter_count=voter_count+$2,first_vote_at=CASE WHEN $3 THEN first_vote_at ELSE coalesce(first_vote_at,clock_timestamp()) END WHERE id=$1`, id, delta, retract)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type optionDelta struct{ Votes, Maybe int }

func pollDeltas(kind string, old, new []domain.PollChoice) map[string]optionDelta {
	out := map[string]optionDelta{}
	apply := func(choices []domain.PollChoice, sign int) {
		for _, c := range choices {
			d := out[c.OptionID]
			switch {
			case kind == "ranked" && c.Rank != 1:
				continue
			case c.Answer == "maybe":
				d.Maybe += sign
			default:
				d.Votes += sign
			}
			out[c.OptionID] = d
		}
	}
	apply(old, -1)
	apply(new, 1)
	return out
}
func applyOptionDeltas(ctx context.Context, tx pgx.Tx, id string, deltas map[string]optionDelta) error {
	ids := make([]string, 0, len(deltas))
	for id := range deltas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, oid := range ids {
		d := deltas[oid]
		if d.Votes == 0 && d.Maybe == 0 {
			continue
		}
		tag, e := tx.Exec(ctx, `UPDATE poll_options SET vote_count=vote_count+$3,maybe_count=maybe_count+$4 WHERE poll_id=$1 AND id=$2`, id, oid, d.Votes, d.Maybe)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return domain.PollFailure("VOTE_INVALID", 422, "optionId")
		}
	}
	return nil
}
func (r *Repo) PollBallotsTx(ctx context.Context, q pollQuerier, p *domain.Poll) ([][]domain.PollChoice, error) {
	out := [][]domain.PollChoice{}
	if p.IdentityMode == "secret" {
		rows, e := q.Query(ctx, `SELECT choices FROM poll_ballots WHERE poll_id=$1`, p.ID)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var b []byte
			var choices []domain.PollChoice
			if e = rows.Scan(&b); e != nil {
				return nil, e
			}
			if e = json.Unmarshal(b, &choices); e != nil {
				return nil, e
			}
			out = append(out, choices)
		}
		return out, rows.Err()
	}
	rows, e := q.Query(ctx, `SELECT v.profile_uuid::text,c.option_id::text,coalesce(c.rank,0),coalesce(c.answer,'') FROM poll_voters v LEFT JOIN poll_votes c ON c.poll_id=v.poll_id AND c.voter_profile_uuid=v.profile_uuid WHERE v.poll_id=$1 ORDER BY v.profile_uuid,c.rank NULLS LAST,c.option_id`, p.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	prev := ""
	for rows.Next() {
		var profile string
		var oid *string
		var c domain.PollChoice
		if e = rows.Scan(&profile, &oid, &c.Rank, &c.Answer); e != nil {
			return nil, e
		}
		if profile != prev {
			out = append(out, []domain.PollChoice{})
			prev = profile
		}
		if oid != nil {
			c.OptionID = *oid
			out[len(out)-1] = append(out[len(out)-1], c)
		}
	}
	return out, rows.Err()
}
func (r *Repo) PollBallots(ctx context.Context, p *domain.Poll) ([][]domain.PollChoice, error) {
	return r.PollBallotsTx(ctx, r.pool, p)
}
func (r *Repo) SetPollNotify(ctx context.Context, id, profile string, on bool) error {
	tag, e := r.pool.Exec(ctx, `UPDATE poll_voters SET notify_result=$3 WHERE poll_id=$1 AND profile_uuid=$2`, id, profile, on)
	if e == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return e
}
