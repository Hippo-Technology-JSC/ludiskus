DROP INDEX IF EXISTS reports_poll_person_unique;
DROP INDEX idx_reports_poll, idx_moderation_poll;
UPDATE comment_policies SET config = config - 'poll';
