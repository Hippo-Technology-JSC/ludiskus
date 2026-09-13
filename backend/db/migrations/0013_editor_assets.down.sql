DROP INDEX IF EXISTS attachments_finalized_orphan_idx;
DROP INDEX IF EXISTS attachments_upload_idempotency_key_uq;

ALTER TABLE attachments
  DROP COLUMN IF EXISTS upload_idempotency_key,
  DROP COLUMN IF EXISTS purpose,
  DROP COLUMN IF EXISTS checksum_sha256,
  DROP COLUMN IF EXISTS finalized_at;
