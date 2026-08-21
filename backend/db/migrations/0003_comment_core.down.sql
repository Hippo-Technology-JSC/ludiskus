DROP TABLE IF EXISTS comment_participants, comment_mentions, comments, comment_targets,
  comment_policies, comment_services CASCADE;
DROP FUNCTION IF EXISTS comments_tsv_trg();
ALTER TABLE profile_cache DROP COLUMN IF EXISTS created_at;
