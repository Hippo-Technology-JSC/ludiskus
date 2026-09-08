-- Forum only: support assignment and full-text indexing of the opening post.
ALTER TABLE topics ADD COLUMN assignee_profile_uuid uuid;
CREATE INDEX idx_topics_assignee ON topics(space_uuid, assignee_profile_uuid) WHERE assignee_profile_uuid IS NOT NULL;
CREATE OR REPLACE FUNCTION topics_tsv_trg() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.search_tsv = setweight(ludiskus_tsv(NEW.title), 'A') ||
    setweight(ludiskus_tsv((SELECT body_md FROM posts WHERE topic_id=NEW.id AND is_first LIMIT 1)), 'B');
  RETURN NEW;
END $$;
CREATE FUNCTION forum_first_post_search_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.is_first THEN UPDATE topics SET title=title WHERE id=OLD.topic_id; END IF;
    RETURN OLD;
  END IF;
  IF NEW.is_first THEN UPDATE topics SET title=title WHERE id=NEW.topic_id; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_forum_first_post_search AFTER INSERT OR UPDATE OF body_md OR DELETE ON posts
FOR EACH ROW EXECUTE FUNCTION forum_first_post_search_update();
UPDATE topics SET title=title;
