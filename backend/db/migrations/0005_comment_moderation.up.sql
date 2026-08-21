ALTER TABLE attachments ALTER COLUMN space_uuid DROP NOT NULL;
ALTER TABLE reports ALTER COLUMN space_uuid DROP NOT NULL;
ALTER TABLE moderation_items ALTER COLUMN space_uuid DROP NOT NULL;

ALTER TABLE attachments ADD COLUMN comment_id uuid REFERENCES comments(id) ON DELETE CASCADE;
ALTER TABLE attachments ADD CONSTRAINT attachments_owner_one
  CHECK (num_nonnulls(post_id, comment_id) <= 1);
CREATE INDEX idx_attach_comment ON attachments (comment_id) WHERE comment_id IS NOT NULL;

DELETE FROM reports r USING reports keep
 WHERE r.status = 'open' AND keep.status = 'open'
   AND r.target_type = keep.target_type AND r.target_id = keep.target_id
   AND r.reporter_profile_uuid = keep.reporter_profile_uuid
   AND (r.created_at, r.id) > (keep.created_at, keep.id);
CREATE UNIQUE INDEX uq_reports_open_reporter
  ON reports (target_type, target_id, reporter_profile_uuid) WHERE status = 'open';
CREATE INDEX idx_reports_comment_open ON reports (target_id)
  WHERE target_type = 'comment' AND status = 'open';
CREATE INDEX idx_moditems_comment_pending ON moderation_items (space_uuid, created_at)
  WHERE target_type = 'comment' AND state = 'pending';

CREATE TABLE comment_revisions (
  comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  revision int NOT NULL CHECK (revision >= 1),
  body_md text NOT NULL,
  edited_by uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (comment_id, revision)
);
