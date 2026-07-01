# 09 — Database

Database riêng `ludiskus` trên Postgres dùng chung của hippo (tạo bởi
[infra/postgres/init-databases.sh](../../infra/postgres/init-databases.sh) — xem
[12](12-trien-khai-docker.md)). Migration đặt tại `ludiskus/backend/db/migrations`
theo cặp `.up.sql` / `.down.sql` (giống lubo/luxport/lunoti).

## 9.1 Quy ước

- Khoá chính `uuid` (default `gen_random_uuid()`), cần extension `pgcrypto`.
- `profile_uuid` / `space_uuid` là **uuid của HipCore** (không phải FK — sống ở
  DB khác); ludiskus chỉ cache (`profile_cache`, `space_cache`,
  `space_member_cache`).
- `created_at`/`updated_at` mặc định `now()`; trigger cập nhật `updated_at`.
- Hàng đợi gửi lunoti = bảng `outbox`, lấy việc `FOR UPDATE SKIP LOCKED`.
- FTS: `unaccent` + `pg_trgm`, cột `search_tsv` + GIN (xem [06](06-tim-kiem.md)).

## 9.2 Migration `0001_init.up.sql` (phác thảo)

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TYPE moderation_mode AS ENUM ('none','post','pre','first_post');
CREATE TYPE post_policy     AS ENUM ('members','anyone_authenticated','staff_only');
CREATE TYPE board_kind      AS ENUM ('forum','qna','support','announcement');
CREATE TYPE topic_type      AS ENUM ('discussion','question','announcement');
CREATE TYPE topic_status    AS ENUM ('published','pending','locked','hidden','deleted');
CREATE TYPE post_status      AS ENUM ('published','pending','hidden','deleted');
CREATE TYPE attach_kind      AS ENUM ('image','file');
CREATE TYPE attach_status    AS ENUM ('pending','attached','orphaned');
CREATE TYPE sub_target       AS ENUM ('space','board','topic');
CREATE TYPE report_target    AS ENUM ('post','topic');
CREATE TYPE mod_state        AS ENUM ('pending','approved','rejected');
CREATE TYPE outbox_status    AS ENUM ('queued','sending','sent','failed');

CREATE FUNCTION ludiskus_tsv(txt text) RETURNS tsvector
  LANGUAGE sql IMMUTABLE AS
  $$ SELECT to_tsvector('simple', unaccent(coalesce(txt,''))) $$;

-- Forum cho mỗi Space ----------------------------------------------------------
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

CREATE TABLE space_moderators (
  space_uuid   uuid NOT NULL,
  profile_uuid uuid NOT NULL,
  granted_by   uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (space_uuid, profile_uuid)
);

-- Board -------------------------------------------------------------------------
CREATE TABLE boards (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  space_uuid   uuid NOT NULL,
  parent_id    uuid REFERENCES boards(id) ON DELETE SET NULL,
  code         text NOT NULL,
  name         text NOT NULL,
  description_md   text,
  description_html text,        -- render & sanitize từ description_md
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

-- Topic -------------------------------------------------------------------------
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

-- Post --------------------------------------------------------------------------
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

CREATE TABLE post_mentions (
  post_id        uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  profile_uuid   uuid NOT NULL,
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
  source        text NOT NULL,        -- pre_moderation|first_post|banned_word|reported|auto_hide
  state         mod_state NOT NULL DEFAULT 'pending',
  assignee_profile_uuid uuid,
  decided_by    uuid,
  decided_at    timestamptz,
  note          text,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Outbox đẩy event sang lunoti --------------------------------------------------
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

-- Cache HipCore -----------------------------------------------------------------
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

-- Index nóng --------------------------------------------------------------------
CREATE INDEX idx_boards_space      ON boards (space_uuid, position);
CREATE INDEX idx_topics_board      ON topics (board_id, is_pinned DESC, last_post_at DESC) WHERE status='published';
CREATE INDEX idx_topics_space      ON topics (space_uuid, last_post_at DESC) WHERE status='published';
CREATE INDEX idx_topics_author     ON topics (author_profile_uuid);
CREATE INDEX idx_posts_topic       ON posts (topic_id, created_at) WHERE status='published';
CREATE INDEX idx_posts_author      ON posts (author_profile_uuid);
CREATE INDEX idx_mentions_profile  ON post_mentions (profile_uuid);
CREATE INDEX idx_subs_target       ON subscriptions (target_type, target_id) WHERE NOT muted;
CREATE INDEX idx_reports_open      ON reports (space_uuid) WHERE status='open';
CREATE INDEX idx_moditems_pending  ON moderation_items (space_uuid) WHERE state='pending';
CREATE INDEX idx_outbox_queue      ON outbox (scheduled_at) WHERE status='queued';
CREATE INDEX idx_attach_pending    ON attachments (created_at) WHERE status='pending';
CREATE INDEX idx_member_profile    ON space_member_cache (profile_uuid);
CREATE INDEX idx_topics_tsv        ON topics USING GIN (search_tsv);
CREATE INDEX idx_posts_tsv         ON posts  USING GIN (search_tsv);
CREATE INDEX idx_topics_title_trgm ON topics USING GIN (title gin_trgm_ops);
CREATE INDEX idx_tags_name_trgm    ON tags   USING GIN (name gin_trgm_ops);
```

## 9.3 Trigger

- `set_updated_at` trên các bảng có `updated_at`.
- `topics_tsv`: `BEFORE INSERT/UPDATE OF title` → `search_tsv =
  setweight(ludiskus_tsv(NEW.title),'A')`.
- `posts_tsv`: `BEFORE INSERT/UPDATE OF body_md` → `search_tsv =
  ludiskus_tsv(NEW.body_md)`; nếu `is_first` thì cập nhật thêm trọng số `B` vào
  `topics.search_tsv` (hoặc để service ghép title+body).
- Đếm (`reply_count`, `reaction_count`, `last_post_at`, `topic_count`…) cập nhật
  trong transaction của service hoặc qua worker để tránh hot-row contention.

## 9.4 Lấy việc khỏi outbox (mẫu SKIP LOCKED)

```sql
UPDATE outbox o SET status='sending', attempts=attempts+1
WHERE o.id IN (
  SELECT id FROM outbox
  WHERE status='queued' AND scheduled_at <= now()
  ORDER BY scheduled_at
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
RETURNING o.*;
```

## 9.5 Seed

- `db/seeds/boards.json` — board mặc định khi bật forum cho Space (`general`,
  `qna`, `announcements`) — idempotent theo `(space_uuid, code)`.
- `db/seeds/banned_words.json` — bộ từ cấm mẫu.
- `db/seeds/lunoti_event_types.json` — event-type + template đăng ký lên lunoti
  ([08](08-tich-hop-lunoti.md)).

## 9.6 Down migration

`0001_init.down.sql` drop bảng theo thứ tự ngược (cache, outbox, moderation_items,
reports, subscriptions, attachments, topic_tags, tags, reactions, post_mentions,
posts, topics, boards, space_moderators, space_forums) rồi drop function
`ludiskus_tsv` và các enum type.
