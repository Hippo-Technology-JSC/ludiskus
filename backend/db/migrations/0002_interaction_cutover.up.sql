-- GĐ7: cut-over một lần sang Interaction Platform của lufami.
-- Snapshot dữ liệu lịch sử vào outbox backfill trước khi bỏ ngay schema reaction
-- cũ. Từ migration này Ludiskus không còn đọc/ghi reaction tại DB của mình.
CREATE TABLE interaction_backfill_outbox (
  id               bigserial PRIMARY KEY,
  post_id          uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  resource_type    text NOT NULL CHECK (resource_type IN ('post','reply')),
  actor_profile_uuid uuid NOT NULL,
  interaction_kind text NOT NULL CHECK (interaction_kind IN ('like','dislike','reaction')),
  reaction_code    text NOT NULL DEFAULT '',
  occurred_at      timestamptz NOT NULL,
  attempts         int NOT NULL DEFAULT 0,
  scheduled_at     timestamptz NOT NULL DEFAULT now(),
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (post_id, actor_profile_uuid, interaction_kind, reaction_code)
);

INSERT INTO interaction_backfill_outbox (
  post_id, resource_type, actor_profile_uuid,
  interaction_kind, reaction_code, occurred_at
)
SELECT
  r.post_id,
  CASE WHEN p.is_first THEN 'post' ELSE 'reply' END,
  r.profile_uuid,
  CASE
    WHEN lower(trim(r.kind)) IN ('like','upvote') THEN 'like'
    WHEN lower(trim(r.kind)) IN ('dislike','downvote') THEN 'dislike'
    ELSE 'reaction'
  END,
  CASE
    WHEN lower(trim(r.kind)) IN ('like','upvote','dislike','downvote') THEN ''
    WHEN lower(trim(r.kind)) IN (
      'love','care','haha','wow','sad','angry','celebrate',
      'support','insightful','confused','agree','disagree'
    ) THEN lower(trim(r.kind))
    ELSE 'agree'
  END,
  r.created_at
FROM reactions r
JOIN posts p ON p.id = r.post_id
ON CONFLICT DO NOTHING;

DROP TABLE reactions;
ALTER TABLE posts DROP COLUMN reaction_count;
ALTER TABLE topics DROP COLUMN reaction_count;

CREATE INDEX idx_interaction_backfill_due
  ON interaction_backfill_outbox (scheduled_at, id);
