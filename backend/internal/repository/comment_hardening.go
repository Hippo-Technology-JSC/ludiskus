package repository

import (
	"context"
	"encoding/json"
	"time"
)

type CommentAbuseFlag struct {
	ID          string          `json:"id"`
	ProfileUUID string          `json:"profileUuid"`
	Signal      string          `json:"signal"`
	Evidence    json.RawMessage `json:"evidence"`
	State       string          `json:"state"`
	DecidedBy   *string         `json:"decidedBy,omitempty"`
	DecidedAt   *time.Time      `json:"decidedAt,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
}

func (r *Repo) UpdateCommentScores(ctx context.Context, scores map[string]int64) (int64, error) {
	var changed int64
	for id, score := range scores {
		tag, err := r.pool.Exec(ctx, `UPDATE comments SET score_cache=$2 WHERE id=$1 AND score_cache<>$2`, id, score)
		if err != nil {
			return changed, err
		}
		changed += tag.RowsAffected()
	}
	return changed, nil
}

// DetectCommentAbuse only raises review flags. It never punishes a profile.
func (r *Repo) DetectCommentAbuse(ctx context.Context) (int64, error) {
	queries := []string{
		`INSERT INTO comment_abuse_flags(profile_uuid,signal,evidence)
		 SELECT author_profile_uuid,'burst',jsonb_build_object('comments',count(*),'targets',count(DISTINCT target_id),'window','10m')
		 FROM comments WHERE author_profile_uuid IS NOT NULL AND created_at>now()-interval '10 minutes'
		 GROUP BY author_profile_uuid HAVING count(*)>20 AND count(DISTINCT target_id)>=5 ON CONFLICT DO NOTHING`,
		`INSERT INTO comment_abuse_flags(profile_uuid,signal,evidence)
		 SELECT author_profile_uuid,'same_body',jsonb_build_object('targets',count(DISTINCT target_id),'comments',count(*))
		 FROM comments WHERE author_profile_uuid IS NOT NULL AND created_at>now()-interval '1 hour'
		 GROUP BY author_profile_uuid,body_hash HAVING count(DISTINCT target_id)>=3 ON CONFLICT DO NOTHING`,
		`INSERT INTO comment_abuse_flags(profile_uuid,signal,evidence)
		 SELECT author_profile_uuid,'link_spam',jsonb_build_object('comments',count(*),'window','10m')
		 FROM comments WHERE author_profile_uuid IS NOT NULL AND created_at>now()-interval '10 minutes' AND body_md~*'https?://'
		 GROUP BY author_profile_uuid HAVING count(*)>=3 ON CONFLICT DO NOTHING`,
		`INSERT INTO comment_abuse_flags(profile_uuid,signal,evidence)
		 SELECT c.author_profile_uuid,'report_magnet',jsonb_build_object('autoHidden',count(*))
		 FROM comments c WHERE c.author_profile_uuid IS NOT NULL AND c.moderation_source='auto_hide'
		 AND c.updated_at>now()-interval '7 days' GROUP BY c.author_profile_uuid HAVING count(*)>=5 ON CONFLICT DO NOTHING`,
		`INSERT INTO comment_abuse_flags(profile_uuid,signal,evidence)
		 SELECT r.reporter_profile_uuid,'report_abuse',jsonb_build_object('reports',count(*),'dismissed',count(*) FILTER(WHERE r.status='dismissed'))
		 FROM reports r WHERE r.target_type='comment' AND r.created_at>now()-interval '7 days'
		 GROUP BY r.reporter_profile_uuid HAVING count(*)>=20 AND count(*) FILTER(WHERE r.status='dismissed')::float/count(*)>=0.8 ON CONFLICT DO NOTHING`,
	}
	var total int64
	for _, q := range queries {
		tag, err := r.pool.Exec(ctx, q)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

func (r *Repo) ListCommentAbuseFlags(ctx context.Context, state string, limit int) ([]CommentAbuseFlag, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `SELECT id,profile_uuid,signal,evidence,state,decided_by,decided_at,created_at
		FROM comment_abuse_flags WHERE ($1='' OR state=$1) ORDER BY created_at DESC,id DESC LIMIT $2`, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentAbuseFlag{}
	for rows.Next() {
		var f CommentAbuseFlag
		if err := rows.Scan(&f.ID, &f.ProfileUUID, &f.Signal, &f.Evidence, &f.State, &f.DecidedBy, &f.DecidedAt, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *Repo) DecideCommentAbuseFlag(ctx context.Context, id, state, actor, note string) (*CommentAbuseFlag, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var f CommentAbuseFlag
	err = tx.QueryRow(ctx, `UPDATE comment_abuse_flags SET state=$2,decided_by=$3,decided_at=now() WHERE id=$1
		RETURNING id,profile_uuid,signal,evidence,state,decided_by,decided_at,created_at`, id, state, nullUUID(actor)).
		Scan(&f.ID, &f.ProfileUUID, &f.Signal, &f.Evidence, &f.State, &f.DecidedBy, &f.DecidedAt, &f.CreatedAt)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO comment_audit_logs(actor,actor_profile_uuid,action,reason,detail)
		VALUES($1,$2,'decide_abuse_flag',$3,jsonb_build_object('flagId',$4,'profileUuid',$5,'state',$6))`,
		actorLabel(actor), nullUUID(actor), note, id, f.ProfileUUID, state)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &f, nil
}
