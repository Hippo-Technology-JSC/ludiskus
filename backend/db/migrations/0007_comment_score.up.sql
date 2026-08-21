ALTER TABLE comments ADD COLUMN score_cache bigint NOT NULL DEFAULT 0;
CREATE INDEX idx_comments_top_page ON comments
  (target_id, is_pinned DESC, score_cache DESC, created_at DESC, id DESC)
  WHERE parent_id IS NULL AND status = 'published';
CREATE UNIQUE INDEX uq_comment_abuse_open_signal ON comment_abuse_flags(profile_uuid,signal) WHERE state='open';
