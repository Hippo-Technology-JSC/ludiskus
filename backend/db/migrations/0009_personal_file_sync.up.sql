CREATE TABLE personal_file_sync_outbox (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  idempotency_key text NOT NULL UNIQUE,
  source_file_id  text NOT NULL,
  event_type      text NOT NULL CHECK (event_type IN ('file.upsert','file.deleted')),
  payload         jsonb NOT NULL,
  status          text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sending','sent','failed','dead')),
  attempts        int NOT NULL DEFAULT 0,
  max_attempts    int NOT NULL DEFAULT 12,
  scheduled_at    timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  sent_at         timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX personal_file_sync_outbox_claim_idx
  ON personal_file_sync_outbox(scheduled_at) WHERE status IN ('queued','failed');
