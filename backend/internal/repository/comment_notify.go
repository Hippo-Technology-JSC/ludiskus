package repository

import (
	"context"
	"time"

	"ludiskus/internal/domain"
)

type CommentNotifyRow struct {
	ID                   int64
	EventType            string
	RecipientProfileUUID string
	TargetID             string
	CommentID            string
	ActorProfileUUID     *string
	OccurredAt           time.Time
	FlushAfter           time.Time
}

func (r *Repo) EnqueueCommentNotify(ctx context.Context, event, recipient, targetID, commentID string, actor *string, flushAfter time.Time) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO comment_notify_buffer(event_type,recipient_profile_uuid,target_id,comment_id,actor_profile_uuid,flush_after)
		VALUES($1,$2,$3,$4,$5,COALESCE((SELECT min(flush_after) FROM comment_notify_buffer WHERE event_type=$1 AND recipient_profile_uuid=$2 AND target_id=$3),$6))
		ON CONFLICT DO NOTHING`, event, recipient, targetID, commentID, actor, flushAfter)
	return err
}

func (r *Repo) ClaimDueCommentNotify(ctx context.Context, limit int) ([]CommentNotifyRow, error) {
	rows, err := r.pool.Query(ctx, `WITH token AS (SELECT gen_random_uuid() id), picked AS (
		SELECT id FROM comment_notify_buffer WHERE flush_after<=now()
		AND (claim_token IS NULL OR claimed_at<now()-interval '5 minutes')
		ORDER BY flush_after,id FOR UPDATE SKIP LOCKED LIMIT $1
	), claimed AS (
		UPDATE comment_notify_buffer b SET claim_token=token.id,claimed_at=now()
		FROM picked,token WHERE b.id=picked.id
		RETURNING b.id,b.event_type,b.recipient_profile_uuid,b.target_id,b.comment_id,b.actor_profile_uuid,b.occurred_at,b.flush_after
	) SELECT * FROM claimed ORDER BY flush_after,id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentNotifyRow{}
	for rows.Next() {
		var v CommentNotifyRow
		if err := rows.Scan(&v.ID, &v.EventType, &v.RecipientProfileUUID, &v.TargetID, &v.CommentID, &v.ActorProfileUUID, &v.OccurredAt, &v.FlushAfter); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteCommentNotify(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM comment_notify_buffer WHERE id=ANY($1::bigint[])`, ids)
	return err
}

func (r *Repo) CommentUnreadCount(ctx context.Context, profileUUID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM comment_participants p JOIN comment_targets t ON t.id=p.target_id
		WHERE p.profile_uuid=$1 AND NOT p.muted AND t.state='active' AND t.last_comment_at>COALESCE(p.last_read_at,p.created_at)`, profileUUID).Scan(&n)
	return n, err
}

func (r *Repo) ListCommentInboxTargets(ctx context.Context, profileUUID string, unread bool, limit int) ([]domain.CommentTarget, error) {
	q := `SELECT ` + prefixCols("t", commentTargetCols) + ` FROM comment_participants p JOIN comment_targets t ON t.id=p.target_id
		WHERE p.profile_uuid=$1 AND t.state='active'`
	if unread {
		q += ` AND t.last_comment_at>COALESCE(p.last_read_at,p.created_at)`
	}
	q += ` ORDER BY t.last_comment_at DESC NULLS LAST LIMIT $2`
	rows, err := r.pool.Query(ctx, q, profileUUID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CommentTarget{}
	for rows.Next() {
		var t domain.CommentTarget
		if err := scanCommentTarget(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) ReconcileCommentCounts(ctx context.Context, targetID string, limit int) (int, error) {
	q := `SELECT target_id,real_comment_count,real_reply_count,real_pending_count,real_participant_count FROM comment_count_check`
	args := []any{}
	if targetID != "" {
		q += ` WHERE target_id=$1`
		args = append(args, targetID)
	}
	q += ` LIMIT `
	args = append(args, limit)
	q += `$` + itoa(len(args))
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		var c, rp, p, people int
		if err := rows.Scan(&id, &c, &rp, &p, &people); err != nil {
			return n, err
		}
		tx, e := r.pool.Begin(ctx)
		if e != nil {
			return n, e
		}
		if _, e = tx.Exec(ctx, `UPDATE comment_targets SET comment_count=$2,reply_count=$3,pending_count=$4,participant_count=$5 WHERE id=$1`, id, c, rp, p, people); e == nil {
			_, e = tx.Exec(ctx, `INSERT INTO comment_audit_logs(actor,action,target_id,detail) VALUES('system','reconcile_count',$1,jsonb_build_object('comments',$2,'replies',$3,'pending',$4,'participants',$5))`, id, c, rp, p, people)
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
	return n, rows.Err()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	b := make([]byte, 0, 12)
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

func (r *Repo) CleanupCommentData(ctx context.Context, maxRevisions, auditDays int) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var total int64
	commands := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM comment_targets WHERE state='gone' AND comment_count=0 AND updated_at<now()-interval '30 days'`, nil},
		{`DELETE FROM comment_notify_buffer WHERE flush_after<now()-interval '1 day'`, nil},
		{`DELETE FROM comment_revisions r USING (SELECT comment_id,revision,row_number() OVER(PARTITION BY comment_id ORDER BY revision DESC) rn FROM comment_revisions) x WHERE r.comment_id=x.comment_id AND r.revision=x.revision AND x.rn>$1`, []any{maxRevisions}},
		{`DELETE FROM comment_audit_logs WHERE created_at<now()-make_interval(days=>$1)`, []any{auditDays}},
	}
	for _, c := range commands {
		tag, e := tx.Exec(ctx, c.q, c.args...)
		if e != nil {
			return total, e
		}
		total += tag.RowsAffected()
	}
	return total, tx.Commit(ctx)
}
