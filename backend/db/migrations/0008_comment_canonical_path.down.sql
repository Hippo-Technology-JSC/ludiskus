ALTER TABLE comment_targets DROP CONSTRAINT IF EXISTS comment_targets_canonical_path_check;
ALTER TABLE comment_targets ADD CONSTRAINT comment_targets_canonical_path_check
  CHECK (canonical_path = '' OR
    (length(canonical_path) <= 301 AND canonical_path ~ '^/[A-Za-z0-9/_.:-]*$'));
