ALTER TABLE attachments
  ADD COLUMN finalized_at timestamptz,
  ADD COLUMN checksum_sha256 text,
  ADD COLUMN purpose text,
  ADD COLUMN upload_idempotency_key text;

CREATE UNIQUE INDEX attachments_upload_idempotency_key_uq
  ON attachments(upload_idempotency_key)
  WHERE upload_idempotency_key IS NOT NULL;

CREATE INDEX attachments_finalized_orphan_idx
  ON attachments(finalized_at, id)
  WHERE status = 'pending' AND finalized_at IS NOT NULL;

UPDATE attachments
SET finalized_at = created_at
WHERE status = 'attached';
