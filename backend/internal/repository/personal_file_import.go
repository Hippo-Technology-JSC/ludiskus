package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"ludiskus/internal/domain"
)

func (r *Repo) ClaimPersonalFileImport(ctx context.Context, idempotencyKey, tokenHash, actorUserID, purpose string, leaseUntil time.Time) (bool, []string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO personal_file_imports(idempotency_key,token_hash,actor_user_id,purpose,lease_until)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(idempotency_key) DO NOTHING`, idempotencyKey, tokenHash, actorUserID, purpose, leaseUntil)
	if err != nil {
		return false, nil, err
	}
	var storedHash, storedActor, storedPurpose, status string
	var ids []string
	var currentLease time.Time
	err = tx.QueryRow(ctx, `SELECT token_hash,actor_user_id,purpose,status,attachment_ids::text[],lease_until
		FROM personal_file_imports WHERE idempotency_key=$1 FOR UPDATE`, idempotencyKey).
		Scan(&storedHash, &storedActor, &storedPurpose, &status, &ids, &currentLease)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, domain.ErrNotFound
	}
	if err != nil {
		return false, nil, err
	}
	if storedHash != tokenHash || storedActor != actorUserID || storedPurpose != purpose {
		return false, nil, domain.ErrConflict
	}
	if status == "completed" {
		return true, ids, tx.Commit(ctx)
	}
	if status == "pending" && currentLease.After(time.Now()) && !currentLease.Equal(leaseUntil) {
		return false, nil, domain.ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE personal_file_imports SET status='pending',lease_until=$2,last_error=NULL WHERE idempotency_key=$1`, idempotencyKey, leaseUntil)
	if err != nil {
		return false, nil, err
	}
	return false, nil, tx.Commit(ctx)
}

func (r *Repo) CompletePersonalFileImport(ctx context.Context, idempotencyKey string, attachmentIDs []string) error {
	_, err := r.pool.Exec(ctx, `UPDATE personal_file_imports SET status='completed',attachment_ids=$2::uuid[],completed_at=now(),last_error=NULL
		WHERE idempotency_key=$1`, idempotencyKey, attachmentIDs)
	return err
}

func (r *Repo) FailPersonalFileImport(ctx context.Context, idempotencyKey, message string) error {
	_, err := r.pool.Exec(ctx, `UPDATE personal_file_imports SET status='failed',last_error=$2,lease_until=now() WHERE idempotency_key=$1`, idempotencyKey, message)
	return err
}
