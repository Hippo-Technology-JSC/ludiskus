-- LuPoll core. Đọc docs/poll/03 trước khi sửa bất kỳ CHECK nào.

CREATE TABLE poll_policies (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  anchor_service text NOT NULL REFERENCES comment_services(code) ON DELETE CASCADE,
  anchor_type    text NOT NULL DEFAULT '*'
    CHECK (anchor_type = '*' OR anchor_type ~ '^[a-z][a-z0-9_]{0,59}$'),
  config     jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  is_active  boolean NOT NULL DEFAULT true,
  updated_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (anchor_service, anchor_type)
);
CREATE TRIGGER trg_poll_policies_updated BEFORE UPDATE ON poll_policies
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- Poll độc lập dùng hàng ('ludiskus','standalone'); 'standalone' hợp lệ theo regex resource_type.

CREATE TABLE polls (
  id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  anchor_target_id  uuid REFERENCES comment_targets(id) ON DELETE RESTRICT,
  space_uuid        uuid,
  visibility        text CHECK (visibility IN ('public','authenticated','unlisted','space','private')),
  author_kind       text NOT NULL DEFAULT 'profile' CHECK (author_kind IN ('profile','space','service')),
  created_by        uuid,
  author_space_uuid uuid,
  source_service    text,
  question          text NOT NULL CHECK (char_length(question) BETWEEN 3 AND 300),
  description_md    text NOT NULL DEFAULT '' CHECK (char_length(description_md) <= 2000),
  description_html  text NOT NULL DEFAULT '',
  kind              text NOT NULL CHECK (kind IN ('single','multiple','ranked','scale','schedule')),
  config            jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  identity_mode     text NOT NULL DEFAULT 'public'
    CHECK (identity_mode IN ('public','owner_only','anonymous','secret')),
  results_visibility text NOT NULL DEFAULT 'after_vote'
    CHECK (results_visibility IN ('always','after_vote','after_close','owner_only')),
  who_can_vote      text NOT NULL DEFAULT 'viewers' CHECK (who_can_vote IN ('viewers','members','invited')),
  allow_change_vote boolean NOT NULL DEFAULT true,
  allow_retract     boolean NOT NULL DEFAULT true,
  allow_user_options boolean NOT NULL DEFAULT false,
  shuffle_options   boolean NOT NULL DEFAULT false,
  remind            boolean NOT NULL DEFAULT false,
  min_voters_for_results smallint NOT NULL DEFAULT 3 CHECK (min_voters_for_results BETWEEN 2 AND 50),
  quorum_percent    smallint CHECK (quorum_percent BETWEEN 1 AND 100),
  status            text NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','pending','published','closed','hidden','deleted','rejected')),
  status_before_hide text,
  opens_at          timestamptz,
  closes_at         timestamptz,
  closed_at         timestamptz,
  closed_by         uuid,
  close_reason      text CHECK (close_reason IN ('manual','deadline','anchor_gone','moderation','service')),
  reopen_count      smallint NOT NULL DEFAULT 0 CHECK (reopen_count BETWEEN 0 AND 3),
  reminded_at       timestamptz,
  eligible_count    int CHECK (eligible_count >= 0),
  voter_count       int NOT NULL DEFAULT 0 CHECK (voter_count >= 0),
  ballot_version    bigint NOT NULL DEFAULT 0,
  first_vote_at     timestamptz,
  edit_count        int NOT NULL DEFAULT 0,
  idempotency_key   text,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  deleted_at        timestamptz,
  -- 03 §3.3: anchored XOR có visibility; anchored không có space_uuid riêng
  CHECK ((anchor_target_id IS NULL) = (visibility IS NOT NULL) OR status = 'draft'),
  CHECK (anchor_target_id IS NULL OR space_uuid IS NULL),
  CHECK (visibility IS DISTINCT FROM 'space' OR space_uuid IS NOT NULL),
  -- 05 §5.3: secret không đổi, không rút
  CHECK (identity_mode <> 'secret' OR (NOT allow_change_vote AND NOT allow_retract)),
  CHECK (who_can_vote <> 'members' OR anchor_target_id IS NOT NULL OR space_uuid IS NOT NULL),
  CHECK (quorum_percent IS NULL OR who_can_vote IN ('members','invited')),
  CHECK (opens_at IS NULL OR closes_at IS NULL OR closes_at >= opens_at + interval '5 minutes'),
  CHECK (author_kind = 'service' OR created_by IS NOT NULL),
  CHECK ((author_kind = 'space') = (author_space_uuid IS NOT NULL)),
  CHECK ((author_kind = 'service') = (source_service IS NOT NULL))
);
CREATE UNIQUE INDEX uq_polls_idem ON polls (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_polls_anchor  ON polls (anchor_target_id, created_at)
  WHERE anchor_target_id IS NOT NULL AND status <> 'deleted';
CREATE INDEX idx_polls_space   ON polls (space_uuid, created_at DESC) WHERE space_uuid IS NOT NULL AND status IN ('published','closed');
CREATE INDEX idx_polls_creator ON polls (created_by, created_at DESC, id DESC);
CREATE INDEX idx_polls_due     ON polls (closes_at) WHERE status = 'published' AND closes_at IS NOT NULL;
CREATE INDEX idx_polls_drafts  ON polls (created_at) WHERE status = 'draft' AND anchor_target_id IS NULL;
CREATE TRIGGER trg_polls_updated BEFORE UPDATE ON polls
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE poll_options (
  id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  poll_id     uuid NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  position    smallint NOT NULL CHECK (position BETWEEN 0 AND 49),
  label       text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 200),
  label_norm  text NOT NULL,                     -- lower(NFC, trim, gộp khoảng trắng) — tính ở Go
  value       smallint,                          -- scale
  starts_at   timestamptz,                       -- schedule
  ends_at     timestamptz,
  added_by    uuid,
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','pending','hidden')),
  vote_count  int NOT NULL DEFAULT 0 CHECK (vote_count >= 0),
  maybe_count int NOT NULL DEFAULT 0 CHECK (maybe_count >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (poll_id, position) DEFERRABLE INITIALLY DEFERRED,   -- đổi thứ tự trong một tx
  CHECK (ends_at IS NULL OR (starts_at IS NOT NULL AND ends_at > starts_at))
);
CREATE UNIQUE INDEX uq_poll_options_label ON poll_options (poll_id, label_norm) WHERE status <> 'hidden';

CREATE TABLE poll_invitees (
  poll_id      uuid NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  invited_by   uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (poll_id, profile_uuid)
);
CREATE INDEX idx_poll_invitees_profile ON poll_invitees (profile_uuid);

-- Receipt: AI đã bỏ phiếu. Có ở mọi mức danh tính (QĐ-P9).
CREATE TABLE poll_voters (
  poll_id           uuid NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  profile_uuid      uuid NOT NULL,
  acting_space_uuid uuid,                         -- giữ chỗ, v1 luôn NULL
  idempotency_key   text NOT NULL,
  notify_result     boolean NOT NULL DEFAULT true,
  voted_at          timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (poll_id, profile_uuid)
);
CREATE INDEX idx_poll_voters_profile ON poll_voters (profile_uuid, voted_at DESC);
CREATE INDEX idx_poll_voters_page    ON poll_voters (poll_id, voted_at, profile_uuid);

-- Vote: CÓ danh tính. public / owner_only / anonymous.
CREATE TABLE poll_votes (
  poll_id            uuid NOT NULL,
  voter_profile_uuid uuid NOT NULL,
  option_id          uuid NOT NULL REFERENCES poll_options(id) ON DELETE CASCADE,
  rank               smallint CHECK (rank BETWEEN 1 AND 20),
  answer             text CHECK (answer IN ('yes','maybe')),
  created_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (poll_id, voter_profile_uuid, option_id),
  FOREIGN KEY (poll_id, voter_profile_uuid) REFERENCES poll_voters(poll_id, profile_uuid) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uq_poll_votes_rank ON poll_votes (poll_id, voter_profile_uuid, rank) WHERE rank IS NOT NULL;
CREATE INDEX idx_poll_votes_option ON poll_votes (option_id, created_at, voter_profile_uuid);

-- Ballot: KHÔNG danh tính, KHÔNG thời điểm. CHỈ secret. Đừng bao giờ thêm cột profile/created_at.
CREATE TABLE poll_ballots (
  id      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  poll_id uuid NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  choices jsonb NOT NULL CHECK (jsonb_typeof(choices) = 'array')
);
CREATE INDEX idx_poll_ballots_poll ON poll_ballots (poll_id);
