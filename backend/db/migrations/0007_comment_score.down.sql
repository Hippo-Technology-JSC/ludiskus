DROP INDEX IF EXISTS idx_comments_top_page;
DROP INDEX IF EXISTS uq_comment_abuse_open_signal;
ALTER TABLE comments DROP COLUMN IF EXISTS score_cache;
