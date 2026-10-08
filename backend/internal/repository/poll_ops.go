package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
	"time"
)

func (r *Repo) PollResultTx(ctx context.Context, q pollQuerier, id string) (*domain.PollResult, error) {
	var v domain.PollResult
	e := q.QueryRow(ctx, `SELECT final,method,voter_count,eligible_count,quorum_met,result,ballot_version,computed_at FROM poll_results WHERE poll_id=$1`, id).Scan(&v.Final, &v.Method, &v.VoterCount, &v.EligibleCount, &v.QuorumMet, &v.Result, &v.BallotVersion, &v.ComputedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	return &v, e
}
func (r *Repo) PollResult(ctx context.Context, id string) (*domain.PollResult, error) {
	return r.PollResultTx(ctx, r.pool, id)
}
func (r *Repo) SavePollResultTx(ctx context.Context, tx pgx.Tx, id string, v *domain.PollResult) error {
	_, e := tx.Exec(ctx, `INSERT INTO poll_results(poll_id,final,method,voter_count,eligible_count,quorum_met,result,ballot_version,computed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(poll_id) DO UPDATE SET final=EXCLUDED.final,method=EXCLUDED.method,voter_count=EXCLUDED.voter_count,eligible_count=EXCLUDED.eligible_count,quorum_met=EXCLUDED.quorum_met,result=EXCLUDED.result,ballot_version=EXCLUDED.ballot_version,computed_at=EXCLUDED.computed_at WHERE NOT poll_results.final`, id, v.Final, v.Method, v.VoterCount, v.EligibleCount, v.QuorumMet, v.Result, v.BallotVersion, v.ComputedAt)
	return e
}
func (r *Repo) PollHistory(ctx context.Context, id string) (map[string]any, error) {
	var extended, identity bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM poll_audit_logs WHERE poll_id=$1 AND action='extend'),EXISTS(SELECT 1 FROM poll_audit_logs WHERE poll_id=$1 AND action='identity_changed')`, id).Scan(&extended, &identity)
	return map[string]any{"extended": extended, "identityChanged": identity}, e
}
func (r *Repo) PollPeople(ctx context.Context, id, option, cursor string, participants bool) ([]map[string]any, string, error) {
	rows, e := r.pool.Query(ctx, `SELECT v.profile_uuid::text,v.voted_at,coalesce(p.name,''),coalesce(p.avatar,''),coalesce(p.code,''),CASE WHEN $4 THEN '[]'::jsonb ELSE COALESCE((SELECT jsonb_agg(jsonb_build_object('optionId',c.option_id,'rank',coalesce(c.rank,0),'answer',coalesce(c.answer,''))) FROM poll_votes c WHERE c.poll_id=v.poll_id AND c.voter_profile_uuid=v.profile_uuid),'[]'::jsonb) END FROM poll_voters v LEFT JOIN profile_cache p ON p.profile_uuid=v.profile_uuid WHERE v.poll_id=$1 AND ($2='' OR EXISTS(SELECT 1 FROM poll_votes c WHERE c.poll_id=v.poll_id AND c.voter_profile_uuid=v.profile_uuid AND c.option_id::text=$2)) AND ($3='' OR (v.voted_at,v.profile_uuid)>(SELECT voted_at,profile_uuid FROM poll_voters WHERE poll_id=$1 AND profile_uuid::text=$3)) ORDER BY v.voted_at,v.profile_uuid LIMIT 51`, id, option, cursor, participants)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	out := []map[string]any{}
	next := ""
	for rows.Next() {
		var profile, name, avatar, code string
		var at time.Time
		var choices json.RawMessage
		if e = rows.Scan(&profile, &at, &name, &avatar, &code, &choices); e != nil {
			return nil, "", e
		}
		if len(out) == 50 {
			next = out[49]["profileUuid"].(string)
			break
		}
		item := map[string]any{"profileUuid": profile, "name": name, "avatar": avatar, "code": code, "votedAt": at, "choices": choices}
		out = append(out, item)
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}

	return out, next, nil
}
func (r *Repo) PollExportVotes(ctx context.Context, id string) ([]map[string]string, error) {
	rows, e := r.pool.Query(ctx, `SELECT coalesce(p.name,''),coalesce(p.code,''),o.label,coalesce(v.rank::text,''),coalesce(v.answer,'') FROM poll_votes v JOIN poll_options o ON o.id=v.option_id LEFT JOIN profile_cache p ON p.profile_uuid=v.voter_profile_uuid WHERE v.poll_id=$1 ORDER BY v.voter_profile_uuid,o.position`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var name, code, label, rank, answer string
		if e = rows.Scan(&name, &code, &label, &rank, &answer); e != nil {
			return nil, e
		}
		out = append(out, map[string]string{"name": name, "code": code, "label": label, "rank": rank, "answer": answer})
	}
	return out, rows.Err()
}
func (r *Repo) PollCandidateIDs(ctx context.Context, mode string, limit int) ([]string, error) {
	where := map[string]string{"close": `status='published' AND closes_at<=now()`, "remind": `status='published' AND remind AND reminded_at IS NULL AND closes_at>now() AND closes_at-now() <= least(interval '24 hours', (closes_at-coalesce(opens_at,created_at))*0.1) AND who_can_vote IN ('members','invited')`, "snapshot": `status='published' AND identity_mode IN ('anonymous','secret') AND (closes_at IS NULL OR closes_at>now())`, "gone": `status IN ('published','pending') AND EXISTS(SELECT 1 FROM comment_targets t WHERE t.id=anchor_target_id AND t.state='gone')`, "all": `status<>'draft'`}[mode]
	if where == "" {
		return nil, domain.ErrValidation
	}
	rows, e := r.pool.Query(ctx, `SELECT id::text FROM polls WHERE `+where+` ORDER BY created_at LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (r *Repo) PollSweep(ctx context.Context, ttl time.Duration) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var locked bool
	if e = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('poll:sweep',0))`).Scan(&locked); e != nil || !locked {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM polls WHERE status='draft' AND anchor_target_id IS NULL AND created_at<now()-make_interval(secs=>$1)`, ttl.Seconds()); e != nil {
		return e
	}
	for _, table := range []string{"poll_votes", "poll_ballots", "poll_voters"} {
		if _, e = tx.Exec(ctx, `DELETE FROM `+table+` WHERE poll_id IN (SELECT id FROM polls WHERE status='deleted' AND deleted_at<now()-interval '180 days')`); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (r *Repo) PollAbuseFlags(ctx context.Context) ([]map[string]any, error) {
	rows, e := r.pool.Query(ctx, `SELECT poll_id::text,detail,created_at FROM poll_audit_logs WHERE action='abuse_flag' ORDER BY created_at DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id string
		var detail json.RawMessage
		var at time.Time
		if e = rows.Scan(&id, &detail, &at); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"pollId": id, "detail": detail, "createdAt": at})
	}
	return out, rows.Err()
}

func (r *Repo) PollOptionParent(ctx context.Context, id string) (string, error) {
	var parent string
	e := r.pool.QueryRow(ctx, `SELECT poll_id::text FROM poll_options WHERE id=$1`, id).Scan(&parent)
	return parent, e
}
