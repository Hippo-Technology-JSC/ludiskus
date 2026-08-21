-- LuComment core: registry, policies, targets, comments and participants.
CREATE TABLE comment_services (
  code text PRIMARY KEY CHECK (code ~ '^[a-z][a-z0-9_]{1,39}$'),
  name text NOT NULL,
  base_url text NOT NULL DEFAULT '',
  oauth_client_id text NOT NULL DEFAULT '',
  verify_mode text NOT NULL DEFAULT 'optimistic'
    CHECK (verify_mode IN ('strict','optimistic','trust')),
  context_path text NOT NULL DEFAULT ''
    CHECK (context_path IN ('','resource-context','interaction-context')),
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_comment_services_client ON comment_services (oauth_client_id)
  WHERE oauth_client_id <> '';
CREATE TRIGGER trg_comment_services_updated BEFORE UPDATE ON comment_services
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_policies (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  service_code text NOT NULL REFERENCES comment_services(code) ON DELETE CASCADE,
  resource_type text NOT NULL DEFAULT '*'
    CHECK (resource_type = '*' OR resource_type ~ '^[a-z][a-z0-9_]{0,59}$'),
  config jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  is_active boolean NOT NULL DEFAULT true,
  updated_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (service_code, resource_type)
);
CREATE TRIGGER trg_comment_policies_updated BEFORE UPDATE ON comment_policies
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_targets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  service_code text NOT NULL REFERENCES comment_services(code) ON DELETE RESTRICT,
  resource_type text NOT NULL CHECK (resource_type ~ '^[a-z][a-z0-9_]{0,59}$'),
  resource_id text NOT NULL CHECK (resource_id ~ '^[A-Za-z0-9_.:-]{1,100}$'),
  space_uuid uuid,
  owner_type text CHECK (owner_type IN ('profile','space')),
  owner_id uuid,
  title text NOT NULL DEFAULT '',
  summary text NOT NULL DEFAULT '',
  thumbnail_url text NOT NULL DEFAULT '',
  canonical_path text NOT NULL DEFAULT ''
    CHECK (canonical_path = '' OR
      (length(canonical_path) <= 301 AND canonical_path ~ '^/[A-Za-z0-9/_.:-]*$')),
  visibility text NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('public','authenticated','space','connections','private')),
  state text NOT NULL DEFAULT 'unverified'
    CHECK (state IN ('unverified','active','gone','blocked')),
  thread_state text NOT NULL DEFAULT 'open'
    CHECK (thread_state IN ('open','locked','closed','hidden')),
  capabilities jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(capabilities) = 'object'),
  comment_count int NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
  reply_count int NOT NULL DEFAULT 0 CHECK (reply_count >= 0),
  participant_count int NOT NULL DEFAULT 0 CHECK (participant_count >= 0),
  pending_count int NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
  last_comment_at timestamptz,
  last_comment_id uuid,
  verify_failures int NOT NULL DEFAULT 0 CHECK (verify_failures >= 0),
  verified_at timestamptz,
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (service_code, resource_type, resource_id)
);
CREATE INDEX idx_comment_targets_space ON comment_targets (space_uuid) WHERE space_uuid IS NOT NULL;
CREATE INDEX idx_comment_targets_owner ON comment_targets (owner_type, owner_id);
CREATE INDEX idx_comment_targets_active ON comment_targets (last_comment_at DESC)
  WHERE state = 'active' AND comment_count > 0;
CREATE INDEX idx_comment_targets_stale ON comment_targets (verified_at NULLS FIRST)
  WHERE state IN ('unverified','active');
CREATE TRIGGER trg_comment_targets_updated BEFORE UPDATE ON comment_targets
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  target_id uuid NOT NULL REFERENCES comment_targets(id) ON DELETE CASCADE,
  parent_id uuid REFERENCES comments(id) ON DELETE SET NULL,
  root_id uuid NOT NULL,
  depth int NOT NULL DEFAULT 0 CHECK (depth BETWEEN 0 AND 5),
  reply_to_profile_uuid uuid,
  author_kind text NOT NULL DEFAULT 'profile' CHECK (author_kind IN ('profile','space','service')),
  author_profile_uuid uuid,
  author_space_uuid uuid,
  source_service text,
  body_md text NOT NULL,
  body_html text NOT NULL DEFAULT '',
  body_hash text NOT NULL DEFAULT '',
  markdown_mode text NOT NULL DEFAULT 'basic' CHECK (markdown_mode IN ('plain','basic','rich')),
  status text NOT NULL DEFAULT 'published'
    CHECK (status IN ('published','pending','hidden','deleted','rejected')),
  moderation_source text CHECK (moderation_source IS NULL OR moderation_source IN
    ('pre','first_comment','banned_word','auto_hide','report','service')),
  is_pinned boolean NOT NULL DEFAULT false,
  pinned_by uuid,
  pinned_at timestamptz,
  reply_count int NOT NULL DEFAULT 0 CHECK (reply_count >= 0),
  anchor jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(anchor) = 'object'),
  idempotency_key text,
  edited_at timestamptz,
  edit_count int NOT NULL DEFAULT 0 CHECK (edit_count >= 0),
  deleted_at timestamptz,
  deleted_by uuid,
  delete_reason text,
  search_tsv tsvector,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((parent_id IS NULL) = (depth = 0)),
  CHECK (author_kind = 'service' OR author_profile_uuid IS NOT NULL),
  CHECK ((author_kind = 'space') = (author_space_uuid IS NOT NULL)),
  CHECK ((author_kind = 'service') = (source_service IS NOT NULL))
);
CREATE UNIQUE INDEX uq_comments_idem ON comments (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_comments_root_page ON comments
  (target_id, is_pinned DESC, created_at DESC, id DESC)
  WHERE parent_id IS NULL AND status = 'published';
CREATE INDEX idx_comments_branch ON comments (root_id, created_at, id)
  WHERE parent_id IS NOT NULL AND status = 'published';
CREATE INDEX idx_comments_pending ON comments (target_id, created_at) WHERE status = 'pending';
CREATE INDEX idx_comments_author ON comments (author_profile_uuid, created_at DESC, id DESC);
CREATE INDEX idx_comments_first ON comments (target_id, author_profile_uuid) WHERE status = 'published';
CREATE INDEX idx_comments_hash ON comments (body_hash, created_at DESC) WHERE body_hash <> '';
CREATE INDEX idx_comments_tsv ON comments USING GIN (search_tsv);
CREATE OR REPLACE FUNCTION comments_tsv_trg() RETURNS trigger LANGUAGE plpgsql AS
  $$ BEGIN NEW.search_tsv = ludiskus_tsv(NEW.body_md); RETURN NEW; END $$;
CREATE TRIGGER trg_comments_tsv BEFORE INSERT OR UPDATE OF body_md ON comments
  FOR EACH ROW EXECUTE FUNCTION comments_tsv_trg();
CREATE TRIGGER trg_comments_updated BEFORE UPDATE ON comments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_mentions (
  comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  PRIMARY KEY (comment_id, profile_uuid)
);
CREATE INDEX idx_comment_mentions_profile ON comment_mentions (profile_uuid);

CREATE TABLE comment_participants (
  target_id uuid NOT NULL REFERENCES comment_targets(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  reason text NOT NULL DEFAULT 'manual'
    CHECK (reason IN ('authored','replied','mentioned','owner','manual')),
  muted boolean NOT NULL DEFAULT false,
  last_read_at timestamptz,
  last_notified_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (target_id, profile_uuid)
);
CREATE INDEX idx_comment_participants_fanout ON comment_participants (target_id) WHERE NOT muted;
CREATE INDEX idx_comment_participants_profile ON comment_participants (profile_uuid);

ALTER TABLE profile_cache ADD COLUMN IF NOT EXISTS created_at timestamptz;

INSERT INTO comment_services (code, name, base_url, verify_mode) VALUES
  ('ludiskus','Ludiskus','','trust'),
  ('lumuse','Lumuse','http://lumuse-api:8080','optimistic'),
  ('lukolek','Lukolek','http://lukolek-api:8080','optimistic'),
  ('lukode','Lukode','http://lukode-api:8080','optimistic'),
  ('lugame','Lugame','http://lugame-api:8080','optimistic'),
  ('lushoop','Lushoop','http://lushoop-api:8080','optimistic'),
  ('lutriip','Lutriip','http://lutriip-api:8080','optimistic'),
  ('lukomik','Lukomik','http://lukomik-api:8080','optimistic'),
  ('luxtory','Luxtory','http://luxtory-api:8080','optimistic'),
  ('lubo','Lubo','http://lubo-api:8080','optimistic'),
  ('luprojet','Luprojet','http://luprojet-api:8080','strict'),
  ('luservit','Luservit','http://luservit-api:8080','optimistic'),
  ('luwep','Luwep','http://luwep-api:8080','optimistic'),
  ('lufoodi','Lufoodi','http://lufoodi-api:8080','optimistic'),
  ('lutat','Lutat','http://lutat-api:8080','strict'),
  ('luskool','Luskool','http://luskool-api:8080','strict'),
  ('lufami','Lufami','http://lufami-api:8080','strict')
ON CONFLICT (code) DO NOTHING;
