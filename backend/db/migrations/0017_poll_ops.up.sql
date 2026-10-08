CREATE TABLE poll_results (
  poll_id      uuid PRIMARY KEY REFERENCES polls(id) ON DELETE CASCADE,
  final        boolean NOT NULL DEFAULT false,   -- false = ảnh chụp tạm cho anonymous/secret (05 §5.3)
  method       text NOT NULL,
  voter_count  int NOT NULL,
  eligible_count int,
  quorum_met   boolean,
  result       jsonb NOT NULL,
  ballot_version bigint NOT NULL,
  computed_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE poll_audit_logs (
  id                 bigserial PRIMARY KEY,
  poll_id            uuid NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  actor              text NOT NULL,              -- 'profile' | 'service:{code}' | 'system'
  actor_profile_uuid uuid,
  action             text NOT NULL,
  detail             jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_poll_audit_poll ON poll_audit_logs (poll_id, created_at DESC);

-- Đối soát: lệch giữa số đếm lưu và số đếm từ phiếu. Phải rỗng.
CREATE VIEW poll_count_check AS
SELECT o.poll_id, o.id AS option_id, o.vote_count, coalesce(v.n, 0) AS actual
  FROM poll_options o
  JOIN polls p ON p.id = o.poll_id AND p.identity_mode <> 'secret' AND p.kind <> 'ranked'
  LEFT JOIN (SELECT option_id, count(*) FILTER (WHERE answer IS DISTINCT FROM 'maybe') n
               FROM poll_votes GROUP BY option_id) v ON v.option_id = o.id
 WHERE NOT EXISTS(SELECT 1 FROM poll_results f WHERE f.poll_id=p.id AND f.final) AND NOT(p.status='deleted' AND p.deleted_at<now()-interval '180 days') AND o.vote_count <> coalesce(v.n, 0)
UNION ALL
SELECT p.id, NULL, p.voter_count, (SELECT count(*) FROM poll_voters r WHERE r.poll_id = p.id)
  FROM polls p
 WHERE NOT EXISTS(SELECT 1 FROM poll_results f WHERE f.poll_id=p.id AND f.final) AND NOT(p.status='deleted' AND p.deleted_at<now()-interval '180 days') AND p.voter_count <> (SELECT count(*) FROM poll_voters r WHERE r.poll_id = p.id);
