CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TYPE moderation_mode AS ENUM ('none','post','pre','first_post');
CREATE TYPE post_policy     AS ENUM ('members','anyone_authenticated','staff_only');
CREATE TYPE board_kind      AS ENUM ('forum','qna','support','announcement');
CREATE TYPE topic_type      AS ENUM ('discussion','question','announcement');
CREATE TYPE topic_status    AS ENUM ('published','pending','locked','hidden','deleted');
CREATE TYPE post_status     AS ENUM ('published','pending','hidden','deleted');
CREATE TYPE attach_kind     AS ENUM ('image','file');
CREATE TYPE attach_status   AS ENUM ('pending','attached','orphaned');
CREATE TYPE sub_target      AS ENUM ('space','board','topic');
CREATE TYPE report_target   AS ENUM ('post','topic');
CREATE TYPE mod_state       AS ENUM ('pending','approved','rejected');
CREATE TYPE outbox_status   AS ENUM ('queued','sending','sent','failed');

-- Hàm bất biến build tsvector bỏ dấu (docs/06).
CREATE OR REPLACE FUNCTION ludiskus_tsv(txt text) RETURNS tsvector
  LANGUAGE sql IMMUTABLE AS
  $$ SELECT to_tsvector('simple', unaccent(coalesce(txt,''))) $$;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger
  LANGUAGE plpgsql AS
  $$ BEGIN NEW.updated_at = now(); RETURN NEW; END $$;

-- Forum cho mỗi Space ---------------------------------------------------------
CREATE TABLE space_forums (
  space_uuid                 uuid PRIMARY KEY,
  enabled                    boolean NOT NULL DEFAULT true,
  is_public                  boolean NOT NULL DEFAULT false,
  post_policy                post_policy NOT NULL DEFAULT 'members',
  moderation_mode            moderation_mode NOT NULL DEFAULT 'first_post',
  banned_words               text[] NOT NULL DEFAULT '{}',
  report_auto_hide_threshold int NOT NULL DEFAULT 5,
  default_topic_type         topic_type NOT NULL DEFAULT 'discussion',
  settings                   jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_space_forums_updated BEFORE UPDATE ON space_forums
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE space_moderators (
  space_uuid   uuid NOT NULL,
  profile_uuid uuid NOT NULL,
  granted_by   uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_uuid, profile_uuid)
);

-- Board -----------------------------------------------------------------------
CREATE TABLE boards (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid   uuid NOT NULL,
  parent_id    uuid REFERENCES boards(id) ON DELETE SET NULL,
  code         text NOT NULL,
  name         text NOT NULL,
  description_md   text,
  description_html text,
  kind         board_kind NOT NULL DEFAULT 'forum',
  position     int NOT NULL DEFAULT 0,
  is_locked    boolean NOT NULL DEFAULT false,
  min_role     text NOT NULL DEFAULT 'member',
  topic_count  int NOT NULL DEFAULT 0,
  post_count   int NOT NULL DEFAULT 0,
  last_activity_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (space_uuid, code)
);
CREATE TRIGGER trg_boards_updated BEFORE UPDATE ON boards
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Topic -----------------------------------------------------------------------
CREATE TABLE topics (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid    uuid NOT NULL,
  board_id      uuid NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  author_profile_uuid uuid NOT NULL,
  title         text NOT NULL,
  slug          text NOT NULL,
  type          topic_type NOT NULL DEFAULT 'discussion',
  status        topic_status NOT NULL DEFAULT 'published',
  is_pinned     boolean NOT NULL DEFAULT false,
  is_resolved   boolean NOT NULL DEFAULT false,
  answer_post_id uuid,
  reply_count   int NOT NULL DEFAULT 0,
  view_count    int NOT NULL DEFAULT 0,
  reaction_count int NOT NULL DEFAULT 0,
  last_post_at  timestamptz,
  last_post_profile_uuid uuid,
  search_tsv    tsvector,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (space_uuid, slug)
);
CREATE TRIGGER trg_topics_updated BEFORE UPDATE ON topics
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE OR REPLACE FUNCTION topics_tsv_trg() RETURNS trigger
  LANGUAGE plpgsql AS
  $$ BEGIN NEW.search_tsv = setweight(ludiskus_tsv(NEW.title), 'A'); RETURN NEW; END $$;
CREATE TRIGGER trg_topics_tsv BEFORE INSERT OR UPDATE OF title ON topics
  FOR EACH ROW EXECUTE FUNCTION topics_tsv_trg();

-- Post ------------------------------------------------------------------------
CREATE TABLE posts (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  topic_id      uuid NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  space_uuid    uuid NOT NULL,
  author_profile_uuid uuid NOT NULL,
  reply_to_id   uuid REFERENCES posts(id) ON DELETE SET NULL,
  is_first      boolean NOT NULL DEFAULT false,
  body_md       text NOT NULL,
  body_html     text NOT NULL DEFAULT '',
  is_answer     boolean NOT NULL DEFAULT false,
  status        post_status NOT NULL DEFAULT 'published',
  reaction_count int NOT NULL DEFAULT 0,
  search_tsv    tsvector,
  edited_at     timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_posts_updated BEFORE UPDATE ON posts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE OR REPLACE FUNCTION posts_tsv_trg() RETURNS trigger
  LANGUAGE plpgsql AS
  $$ BEGIN NEW.search_tsv = ludiskus_tsv(NEW.body_md); RETURN NEW; END $$;
CREATE TRIGGER trg_posts_tsv BEFORE INSERT OR UPDATE OF body_md ON posts
  FOR EACH ROW EXECUTE FUNCTION posts_tsv_trg();

CREATE TABLE post_mentions (
  post_id      uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  PRIMARY KEY (post_id, profile_uuid)
);

CREATE TABLE reactions (
  post_id      uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  kind         text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (post_id, profile_uuid, kind)
);

CREATE TABLE tags (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid  uuid NOT NULL,
  slug        text NOT NULL,
  name        text NOT NULL,
  usage_count int NOT NULL DEFAULT 0,
  UNIQUE (space_uuid, slug)
);
CREATE TABLE topic_tags (
  topic_id uuid NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
  tag_id   uuid NOT NULL REFERENCES tags(id)   ON DELETE CASCADE,
  PRIMARY KEY (topic_id, tag_id)
);

CREATE TABLE attachments (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid    uuid NOT NULL,
  post_id       uuid REFERENCES posts(id) ON DELETE CASCADE,
  uploader_profile_uuid uuid NOT NULL,
  object_key    text NOT NULL,
  file_name     text NOT NULL,
  content_type  text NOT NULL,
  size_bytes    bigint NOT NULL,
  kind          attach_kind NOT NULL,
  width  int, height int,
  status        attach_status NOT NULL DEFAULT 'pending',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE subscriptions (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_uuid uuid NOT NULL,
  target_type  sub_target NOT NULL,
  target_id    uuid NOT NULL,
  reason       text NOT NULL DEFAULT 'manual',
  muted        boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (profile_uuid, target_type, target_id)
);

CREATE TABLE reports (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid    uuid NOT NULL,
  target_type   report_target NOT NULL,
  target_id     uuid NOT NULL,
  reporter_profile_uuid uuid NOT NULL,
  reason        text NOT NULL,
  note          text,
  status        text NOT NULL DEFAULT 'open',
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE moderation_items (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid    uuid NOT NULL,
  target_type   report_target NOT NULL,
  target_id     uuid NOT NULL,
  source        text NOT NULL,
  state         mod_state NOT NULL DEFAULT 'pending',
  assignee_profile_uuid uuid,
  decided_by    uuid,
  decided_at    timestamptz,
  note          text,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Outbox đẩy event sang lunoti (docs/08) -------------------------------------
CREATE TABLE outbox (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type      text NOT NULL,
  idempotency_key text UNIQUE,
  payload         jsonb NOT NULL,
  status          outbox_status NOT NULL DEFAULT 'queued',
  attempts        int NOT NULL DEFAULT 0,
  max_attempts    int NOT NULL DEFAULT 6,
  scheduled_at    timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  sent_at         timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Cache HipCore (docs/05) -----------------------------------------------------
CREATE TABLE profile_cache (
  profile_uuid uuid PRIMARY KEY,
  user_id      bigint,
  code         text,
  name         text NOT NULL DEFAULT '',
  avatar       text,
  is_active    boolean NOT NULL DEFAULT true,
  synced_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE space_cache (
  space_uuid   uuid PRIMARY KEY,
  code         text,
  name         text NOT NULL DEFAULT '',
  is_public    boolean NOT NULL DEFAULT false,
  is_active    boolean NOT NULL DEFAULT true,
  creator_profile_uuid uuid,
  space_type   text,
  synced_at    timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE space_member_cache (
  space_uuid   uuid NOT NULL,
  profile_uuid uuid NOT NULL,
  role         text NOT NULL DEFAULT 'member',
  joined_at    timestamptz,
  synced_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_uuid, profile_uuid)
);

-- Index nóng ------------------------------------------------------------------
CREATE INDEX idx_boards_space      ON boards (space_uuid, position);
CREATE INDEX idx_topics_board      ON topics (board_id, is_pinned DESC, last_post_at DESC) WHERE status = 'published';
CREATE INDEX idx_topics_space      ON topics (space_uuid, last_post_at DESC) WHERE status = 'published';
CREATE INDEX idx_topics_author     ON topics (author_profile_uuid);
CREATE INDEX idx_posts_topic       ON posts (topic_id, created_at) WHERE status = 'published';
CREATE INDEX idx_posts_author      ON posts (author_profile_uuid);
CREATE INDEX idx_mentions_profile  ON post_mentions (profile_uuid);
CREATE INDEX idx_subs_target       ON subscriptions (target_type, target_id) WHERE NOT muted;
CREATE INDEX idx_reports_open      ON reports (space_uuid) WHERE status = 'open';
CREATE INDEX idx_moditems_pending  ON moderation_items (space_uuid) WHERE state = 'pending';
CREATE INDEX idx_outbox_queue      ON outbox (scheduled_at) WHERE status = 'queued';
CREATE INDEX idx_attach_pending    ON attachments (created_at) WHERE status = 'pending';
CREATE INDEX idx_member_profile    ON space_member_cache (profile_uuid);
CREATE INDEX idx_profile_user      ON profile_cache (user_id);
CREATE INDEX idx_topics_tsv        ON topics USING GIN (search_tsv);
CREATE INDEX idx_posts_tsv         ON posts  USING GIN (search_tsv);
CREATE INDEX idx_topics_title_trgm ON topics USING GIN (title gin_trgm_ops);
CREATE INDEX idx_tags_name_trgm    ON tags   USING GIN (name gin_trgm_ops);
