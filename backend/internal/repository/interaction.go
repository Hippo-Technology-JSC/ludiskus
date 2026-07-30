package repository

import (
	"context"

	"ludiskus/internal/domain"
)

func (r *Repo) ClaimInteractionBackfill(
	ctx context.Context, limit int,
) ([]domain.InteractionBackfillItem, error) {
	if limit < 1 || limit > 100 {
		limit = 100
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id,post_id,resource_type,actor_profile_uuid,
		       interaction_kind,reaction_code,occurred_at,attempts
		FROM interaction_backfill_outbox
		WHERE scheduled_at<=now()
		ORDER BY id
		FOR UPDATE SKIP LOCKED
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	items := []domain.InteractionBackfillItem{}
	for rows.Next() {
		var item domain.InteractionBackfillItem
		if err := rows.Scan(
			&item.ID, &item.PostID, &item.ResourceType, &item.ActorProfileUUID,
			&item.InteractionKind, &item.ReactionCode, &item.OccurredAt, &item.Attempts,
		); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, item := range items {
		if _, err := tx.Exec(ctx, `
			UPDATE interaction_backfill_outbox
			SET attempts=attempts+1,scheduled_at=now()+interval '5 minutes'
			WHERE id=$1`, item.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repo) CompleteInteractionBackfill(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM interaction_backfill_outbox WHERE id=ANY($1)`, ids)
	return err
}

func (r *Repo) FailInteractionBackfill(
	ctx context.Context, ids []int64, message string,
) error {
	if len(ids) == 0 {
		return nil
	}
	if len(message) > 1000 {
		message = message[:1000]
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE interaction_backfill_outbox
		SET last_error=$2, scheduled_at=now()+make_interval(
			secs=>LEAST(3600, 30*power(2,LEAST(attempts,7)))::double precision
		)
		WHERE id=ANY($1)`, ids, message)
	return err
}

func (r *Repo) InteractionRefsForSpace(
	ctx context.Context, spaceUUID string,
) ([]domain.InteractionRef, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT 'topic',id FROM topics WHERE space_uuid=$1
		UNION ALL
		SELECT CASE WHEN is_first THEN 'post' ELSE 'reply' END,id
		FROM posts WHERE space_uuid=$1`, spaceUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []domain.InteractionRef{}
	for rows.Next() {
		ref := domain.InteractionRef{Service: "ludiskus"}
		if err := rows.Scan(&ref.Type, &ref.ID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (r *Repo) InteractionRefsForTopic(
	ctx context.Context, topicID string,
) ([]domain.InteractionRef, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT 'topic',$1::uuid
		UNION ALL
		SELECT CASE WHEN is_first THEN 'post' ELSE 'reply' END,id
		FROM posts WHERE topic_id=$1`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []domain.InteractionRef{}
	for rows.Next() {
		ref := domain.InteractionRef{Service: "ludiskus"}
		if err := rows.Scan(&ref.Type, &ref.ID); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
