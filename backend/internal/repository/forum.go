package repository

import (
	"context"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
)

func attachForumFiles(ctx context.Context, tx pgx.Tx, ids []string, post, space, author string) error {
	if len(ids) == 0 {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE attachments SET post_id=$2,status='attached' WHERE id=ANY($1::uuid[]) AND space_uuid=$3 AND uploader_profile_uuid=$4 AND status='pending' AND post_id IS NULL AND comment_id IS NULL`, ids, post, space, author)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(ids)) {
		return domain.ErrValidation
	}
	return enqueueAttachedPersonalFiles(ctx, tx, ids)
}
func (r *Repo) AssignForumTopic(ctx context.Context, id string, assignee *string) error {
	_, err := r.pool.Exec(ctx, `UPDATE topics SET assignee_profile_uuid=$2 WHERE id=$1`, id, assignee)
	return err
}
func (r *Repo) ResolveForumTopic(ctx context.Context, id string, resolved bool) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var topicID string
	if err = tx.QueryRow(ctx, `SELECT id FROM topics WHERE id=$1 FOR UPDATE`, id).Scan(&topicID); err != nil {
		return err
	}
	if !resolved {
		if _, err = tx.Exec(ctx, `UPDATE posts SET is_answer=false WHERE topic_id=$1`, id); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE topics SET is_resolved=$2,answer_post_id=CASE WHEN $2 THEN answer_post_id ELSE NULL END WHERE id=$1`, id, resolved)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ForumMetrics contains aggregate counts only, no content or identity labels.
func (r *Repo) ForumMetrics(ctx context.Context) (map[string]int64, error) {
	var topics, posts, pending, queued, failed int64
	err := r.pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM topics WHERE status IN ('published','locked')),
 (SELECT count(*) FROM posts WHERE status='published'),
 (SELECT count(*) FROM moderation_items WHERE target_type IN ('topic','post') AND state='pending'),
 (SELECT count(*) FROM outbox WHERE (event_type LIKE 'ludiskus.topic.%' OR event_type LIKE 'ludiskus.post.%') AND status IN ('queued','sending')),
 (SELECT count(*) FROM outbox WHERE (event_type LIKE 'ludiskus.topic.%' OR event_type LIKE 'ludiskus.post.%') AND status='failed')`).Scan(&topics, &posts, &pending, &queued, &failed)
	return map[string]int64{"topics": topics, "posts": posts, "moderation_pending": pending, "outbox_pending": queued, "outbox_failed": failed}, err
}

func (r *Repo) ListForumQueue(ctx context.Context, space string, limit, offset int) ([]domain.ModerationItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+modCols+` FROM moderation_items WHERE space_uuid=$1 AND state='pending' AND target_type IN ('topic','post') ORDER BY created_at,id LIMIT $2 OFFSET $3`, space, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ModerationItem{}
	for rows.Next() {
		var item domain.ModerationItem
		if err = scanModItem(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
