package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"ludiskus/internal/domain"
)

const personalFileEventSQL = `jsonb_build_object(
	'eventId','ludiskus-attachment:'||a.id::text||':'||extract(epoch from a.created_at)::bigint,
	'type','file.upsert','occurredAt',a.created_at,'sourceFileId',a.id::text,
	'sourceRevision',extract(epoch from a.created_at)::bigint::text,'ownerUserId',pc.user_id::text,
	'uploadedByProfileUuid',a.uploader_profile_uuid::text,
	'file',jsonb_build_object('name',a.file_name,'mimeType',a.content_type,'sizeBytes',a.size_bytes,
		'mediaKind',CASE WHEN a.content_type LIKE 'image/%' THEN 'image' WHEN a.content_type LIKE 'video/%' THEN 'video' WHEN a.content_type LIKE 'audio/%' THEN 'audio' WHEN a.content_type LIKE 'text/%' OR a.content_type='application/pdf' THEN 'document' WHEN a.content_type LIKE '%zip%' THEN 'archive' ELSE 'other' END,
		'uploadedAt',a.created_at),
	'context',jsonb_build_object('resourceType','attachment','resourceId',a.id::text,'title',a.file_name,'path','/ludiskus'))`

func enqueueAttachedPersonalFiles(ctx context.Context, tx pgx.Tx, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO personal_file_sync_outbox(idempotency_key,source_file_id,event_type,payload)
		SELECT 'upsert:'||a.id::text||':'||extract(epoch from a.created_at)::bigint,a.id::text,'file.upsert',`+personalFileEventSQL+`
		FROM attachments a JOIN profile_cache pc ON pc.profile_uuid=a.uploader_profile_uuid
		WHERE a.id=ANY($1::uuid[]) AND a.status='attached' AND pc.user_id IS NOT NULL
		ON CONFLICT(idempotency_key) DO NOTHING`, ids)
	return err
}

func (r *Repo) EnqueueAttachedPersonalFiles(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	tag, err := r.pool.Exec(ctx, `INSERT INTO personal_file_sync_outbox(idempotency_key,source_file_id,event_type,payload)
	SELECT 'upsert:'||a.id::text||':'||extract(epoch from a.created_at)::bigint,a.id::text,'file.upsert',`+personalFileEventSQL+`
	FROM attachments a JOIN profile_cache pc ON pc.profile_uuid=a.uploader_profile_uuid
	WHERE a.status='attached' AND pc.user_id IS NOT NULL
	  AND NOT EXISTS (SELECT 1 FROM personal_file_sync_outbox o WHERE o.idempotency_key='upsert:'||a.id::text||':'||extract(epoch from a.created_at)::bigint)
	ORDER BY a.created_at,a.id LIMIT $1
	ON CONFLICT(idempotency_key) DO NOTHING`, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repo) ClaimPersonalFileSync(ctx context.Context, limit int) ([]domain.PersonalFileSyncItem, error) {
	rows, err := r.pool.Query(ctx, `UPDATE personal_file_sync_outbox SET status='sending',attempts=attempts+1 WHERE id IN(SELECT id FROM personal_file_sync_outbox WHERE status IN('queued','failed') AND scheduled_at<=now() ORDER BY scheduled_at FOR UPDATE SKIP LOCKED LIMIT $1) RETURNING id,idempotency_key,source_file_id,event_type,payload,attempts,max_attempts,scheduled_at`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PersonalFileSyncItem{}
	for rows.Next() {
		var i domain.PersonalFileSyncItem
		if err := rows.Scan(&i.ID, &i.IdempotencyKey, &i.SourceFileID, &i.EventType, &i.Payload, &i.Attempts, &i.MaxAttempts, &i.ScheduledAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (r *Repo) MarkPersonalFileSyncSent(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE personal_file_sync_outbox SET status='sent',sent_at=now(),last_error=NULL WHERE id=ANY($1::uuid[])`, ids)
	return err
}
func (r *Repo) MarkPersonalFileSyncFailed(ctx context.Context, items []domain.PersonalFileSyncItem, message string) error {
	for _, i := range items {
		status := "failed"
		when := time.Now().Add(time.Duration(1<<min(i.Attempts, 6)) * time.Minute)
		if i.Attempts >= i.MaxAttempts {
			status = "dead"
		}
		if _, err := r.pool.Exec(ctx, `UPDATE personal_file_sync_outbox SET status=$2,scheduled_at=$3,last_error=$4 WHERE id=$1`, i.ID, status, when, message); err != nil {
			return err
		}
	}
	return nil
}
