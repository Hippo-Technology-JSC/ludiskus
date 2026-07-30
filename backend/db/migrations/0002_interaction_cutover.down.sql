-- Rollback tốt nhất có thể: chỉ phục hồi các hàng chưa được worker chuyển.
-- Các hàng đã chuyển và xoá khỏi outbox vẫn nằm ở Lufami, không được copy ngược.
ALTER TABLE topics ADD COLUMN reaction_count int NOT NULL DEFAULT 0;
ALTER TABLE posts ADD COLUMN reaction_count int NOT NULL DEFAULT 0;

CREATE TABLE reactions (
  post_id      uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  kind         text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (post_id, profile_uuid, kind)
);

INSERT INTO reactions (post_id, profile_uuid, kind, created_at)
SELECT post_id, actor_profile_uuid,
       CASE
         WHEN interaction_kind IN ('like','dislike') THEN interaction_kind
         ELSE reaction_code
       END,
       occurred_at
FROM interaction_backfill_outbox
ON CONFLICT DO NOTHING;

UPDATE posts p
SET reaction_count = x.total
FROM (
  SELECT post_id, count(*)::int total FROM reactions GROUP BY post_id
) x
WHERE p.id = x.post_id;

DROP TABLE interaction_backfill_outbox;
