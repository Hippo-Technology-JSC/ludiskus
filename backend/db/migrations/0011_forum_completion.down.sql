DROP TRIGGER IF EXISTS trg_forum_first_post_search ON posts;
DROP FUNCTION IF EXISTS forum_first_post_search_update();
CREATE OR REPLACE FUNCTION topics_tsv_trg() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.search_tsv=setweight(ludiskus_tsv(NEW.title),'A'); RETURN NEW; END $$;
UPDATE topics SET search_tsv=setweight(ludiskus_tsv(title),'A');
ALTER TABLE topics DROP COLUMN assignee_profile_uuid;
