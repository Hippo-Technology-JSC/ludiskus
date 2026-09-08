-- 16: Phân quyền theo Board (LuDiskus)
-- Bảng cấu hình quyền tạo chủ đề / trả lời của từng Board
CREATE TABLE board_permissions (
  board_id                uuid PRIMARY KEY REFERENCES boards(id) ON DELETE CASCADE,
  topic_mode              text NOT NULL DEFAULT 'inherit_space'
                          CHECK (topic_mode IN ('inherit_space', 'members', 'moderators', 'admins', 'selected', 'nobody')),
  reply_mode              text NOT NULL DEFAULT 'inherit_space'
                          CHECK (reply_mode IN ('inherit_space', 'members', 'moderators', 'admins', 'selected', 'nobody')),
  version                 bigint NOT NULL DEFAULT 1,
  updated_by_profile_uuid uuid NOT NULL,
  updated_at              timestamptz NOT NULL DEFAULT now()
);

-- Danh sách Profile được gán cho mode 'selected'
CREATE TABLE board_permission_profiles (
  board_id     uuid NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  action       text NOT NULL CHECK (action IN ('create_topic', 'reply')),
  profile_uuid uuid NOT NULL,
  PRIMARY KEY (board_id, action, profile_uuid)
);

CREATE INDEX idx_board_permission_profiles_lookup ON board_permission_profiles(board_id, action);
CREATE INDEX idx_board_permission_profiles_actor ON board_permission_profiles(profile_uuid);

-- Danh sách Moderator được gán trực tiếp cho Board
CREATE TABLE board_moderators (
  board_id                uuid NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
  profile_uuid            uuid NOT NULL,
  granted_by_profile_uuid uuid NOT NULL,
  created_at              timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (board_id, profile_uuid)
);

CREATE INDEX idx_board_moderators_profile ON board_moderators(profile_uuid);

-- Lịch sử thay đổi phân quyền Board (bảo lưu kể cả khi Board bị xoá)
CREATE TABLE board_permission_audit (
  id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_profile_uuid uuid NOT NULL,
  space_uuid         uuid NOT NULL,
  board_id           uuid NOT NULL,
  version_before     bigint NOT NULL,
  version_after      bigint NOT NULL,
  changes            jsonb NOT NULL,
  request_id         text,
  created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_board_permission_audit_board ON board_permission_audit(board_id, created_at DESC);
CREATE INDEX idx_board_permission_audit_space ON board_permission_audit(space_uuid, created_at DESC);

-- Khởi tạo mặc định cho các board hiện có (kế thừa space, version 1)
INSERT INTO board_permissions (board_id, topic_mode, reply_mode, version, updated_by_profile_uuid)
SELECT id, 'inherit_space', 'inherit_space', 1, '00000000-0000-0000-0000-000000000000'::uuid
FROM boards
ON CONFLICT (board_id) DO NOTHING;
