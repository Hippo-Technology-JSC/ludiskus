CREATE TABLE personal_file_imports (
  idempotency_key text PRIMARY KEY,
  token_hash text NOT NULL CHECK (char_length(token_hash)=64),
  actor_user_id text NOT NULL,
  purpose text NOT NULL,
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','completed','failed')),
  attachment_ids uuid[] NOT NULL DEFAULT '{}',
  lease_until timestamptz NOT NULL,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz
);
CREATE INDEX personal_file_imports_lease_idx ON personal_file_imports(lease_until) WHERE status='pending';

