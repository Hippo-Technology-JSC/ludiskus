CREATE TABLE comment_notify_buffer (
  id bigserial PRIMARY KEY,
  event_type text NOT NULL,
  recipient_profile_uuid uuid NOT NULL,
  target_id uuid NOT NULL REFERENCES comment_targets(id) ON DELETE CASCADE,
  comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  actor_profile_uuid uuid,
  occurred_at timestamptz NOT NULL DEFAULT now(),
  flush_after timestamptz NOT NULL,
	claim_token uuid,
	claimed_at timestamptz,
  UNIQUE (event_type, recipient_profile_uuid, target_id, comment_id)
);
CREATE INDEX idx_comment_notify_due ON comment_notify_buffer (flush_after, id);

CREATE TABLE comment_abuse_flags (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_uuid uuid NOT NULL,
  signal text NOT NULL CHECK (signal IN ('burst','same_body','link_spam','report_magnet','report_abuse')),
  evidence jsonb NOT NULL DEFAULT '{}'::jsonb,
  state text NOT NULL DEFAULT 'open' CHECK (state IN ('open','dismissed','throttled','pre_moderated')),
  decided_by uuid,
  decided_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_comment_abuse_open ON comment_abuse_flags (created_at DESC) WHERE state = 'open';
CREATE INDEX idx_comment_abuse_profile ON comment_abuse_flags (profile_uuid, created_at DESC);

CREATE TABLE comment_audit_logs (
  id bigserial PRIMARY KEY,
  actor text NOT NULL,
  actor_profile_uuid uuid,
  action text NOT NULL,
  target_id uuid,
  comment_id uuid,
  reason text,
  detail jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_comment_audit_comment ON comment_audit_logs (comment_id, created_at DESC);
CREATE INDEX idx_comment_audit_time ON comment_audit_logs (created_at DESC);

CREATE VIEW comment_count_check AS
SELECT t.id AS target_id,
       t.comment_count, c.roots AS real_comment_count,
       t.reply_count, c.replies AS real_reply_count,
       t.pending_count, c.pendings AS real_pending_count,
       t.participant_count, p.people AS real_participant_count
  FROM comment_targets t
  LEFT JOIN LATERAL (
    SELECT count(*) FILTER (WHERE parent_id IS NULL AND status = 'published') AS roots,
           count(*) FILTER (WHERE parent_id IS NOT NULL AND status = 'published') AS replies,
           count(*) FILTER (WHERE status = 'pending') AS pendings
      FROM comments WHERE target_id = t.id
  ) c ON true
  LEFT JOIN LATERAL (
    SELECT count(*) AS people FROM comment_participants WHERE target_id = t.id
  ) p ON true
 WHERE t.comment_count <> c.roots OR t.reply_count <> c.replies
    OR t.pending_count <> c.pendings OR t.participant_count <> p.people;
