# 10 — Database

Database `ludiskus` (PostgreSQL 17), migration nhúng `//go:embed`, **forward-only**
([`internal/database.Migrate`](../../backend/internal/database/database.go) chỉ đọc `*.up.sql`
và bọc **mỗi file trong một transaction**). File `.down.sql` viết kèm cho môi trường dev.

Migration hiện có: `0001_init`, `0002_interaction_cutover`. Phân hệ này dùng **`0003`→`0008`**.

## 10.1 Quy ước

- Khoá chính `uuid PRIMARY KEY DEFAULT gen_random_uuid()` (extension `pgcrypto` đã bật ở 0001).
- Thời gian `timestamptz NOT NULL DEFAULT now()`; trigger `set_updated_at()` (đã có).
- **Enum mới dùng `text` + `CHECK`**, không tạo `CREATE TYPE`. Lý do: 0001 dùng enum thật và
  giờ mỗi lần thêm giá trị lại vướng luật transaction (QĐ-12); từ `0002` trở đi `ludiskus` đã
  chuyển sang `CHECK`.
- FTS dùng lại hàm bất biến `ludiskus_tsv(text)` (đã có, `simple` + `unaccent`).
- Index một phần cho truy vấn nóng, đặt tên `idx_<bảng>_<mục đích>`.
- Mọi cột đếm có `CHECK (>= 0)`.

## 10.2 Bảng tổng quan

| Bảng | Vai trò | Migration |
|------|---------|-----------|
| `comment_services` | Registry service được phép có Target | 0003 |
| `comment_policies` | Policy theo `service × resource_type` | 0003 |
| `comment_targets` | Bản chiếu Resource + trạng thái luồng + số đếm | 0003 |
| `comments` | Bình luận | 0003 |
| `comment_mentions` | @mention đã giải | 0003 |
| `comment_participants` | Theo dõi / mute / đã đọc | 0003 |
| `attachments` (+ cột) | Đính kèm — tái dùng bảng cũ | 0005 |
| `reports`, `moderation_items` (+ giá trị enum) | Báo cáo & hàng chờ — tái dùng bảng cũ | 0004 + 0005 |
| `comment_revisions` | Lịch sử sửa | 0005 |
| `comment_notify_buffer` | Gom nhóm thông báo | 0006 |
| `comment_abuse_flags` | Tín hiệu lạm dụng | 0006 |
| `comment_audit_logs` | Nhật ký hành động | 0006 |
| `comment_count_check` (view) | Đối soát số đếm | 0006 |
| `comments.score_cache`, index trang `top` | Điểm xếp hạng lấy từ Interaction Platform | 0007 |
| constraint `comment_targets.canonical_path` | Chuẩn hóa path và giới hạn 301 ký tự tương thích PostgreSQL | 0008 |

## 10.3 `0003_comment_core.up.sql`

```sql
-- =========================================================================
-- 0003: LuComment — registry, policy, target, comment, mention, participant.
-- Không tenant: phân tách theo service_code + space_uuid (nullable) + owner.
-- KHÔNG tham chiếu topics/posts/boards (QĐ-8, docs/comment/02 §2.4).
-- =========================================================================

CREATE TABLE comment_services (
  code            text PRIMARY KEY CHECK (code ~ '^[a-z][a-z0-9_]{1,39}$'),
  name            text NOT NULL,
  base_url        text NOT NULL DEFAULT '',      -- rỗng ⇒ chỉ dùng được verify_mode='trust'
  oauth_client_id text NOT NULL DEFAULT '',      -- claim `aud` khi service gọi ngược (docs/06 §6.7)
  verify_mode     text NOT NULL DEFAULT 'optimistic'
                  CHECK (verify_mode IN ('strict','optimistic','trust')),
  context_path    text NOT NULL DEFAULT ''
                  CHECK (context_path IN ('','resource-context','interaction-context')),
  is_active       boolean NOT NULL DEFAULT true,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_comment_services_client
  ON comment_services (oauth_client_id) WHERE oauth_client_id <> '';
CREATE TRIGGER trg_comment_services_updated BEFORE UPDATE ON comment_services
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_policies (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  service_code  text NOT NULL REFERENCES comment_services(code) ON DELETE CASCADE,
  resource_type text NOT NULL DEFAULT '*'
                CHECK (resource_type = '*' OR resource_type ~ '^[a-z][a-z0-9_]{0,59}$'),
  config        jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(config) = 'object'),
  is_active     boolean NOT NULL DEFAULT true,
  updated_by    uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (service_code, resource_type)
);
CREATE TRIGGER trg_comment_policies_updated BEFORE UPDATE ON comment_policies
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_targets (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  service_code   text NOT NULL REFERENCES comment_services(code) ON DELETE RESTRICT,
  resource_type  text NOT NULL CHECK (resource_type ~ '^[a-z][a-z0-9_]{0,59}$'),
  resource_id    text NOT NULL CHECK (resource_id ~ '^[A-Za-z0-9_.:-]{1,100}$'),
  space_uuid     uuid,                                  -- NULL = không thuộc Space nào
  owner_type     text CHECK (owner_type IN ('profile','space')),
  owner_id       uuid,
  title          text NOT NULL DEFAULT '',
  summary        text NOT NULL DEFAULT '',
  thumbnail_url  text NOT NULL DEFAULT '',
  canonical_path text NOT NULL DEFAULT ''               -- đường dẫn tương đối trong tm
                 CHECK (canonical_path = '' OR canonical_path ~ '^/[A-Za-z0-9/_.:-]{0,300}$'),
  visibility     text NOT NULL DEFAULT 'private'
                 CHECK (visibility IN ('public','authenticated','space','connections','private')),
  state          text NOT NULL DEFAULT 'unverified'
                 CHECK (state IN ('unverified','active','gone','blocked')),
  thread_state   text NOT NULL DEFAULT 'open'
                 CHECK (thread_state IN ('open','locked','closed','hidden')),
  capabilities   jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(capabilities) = 'object'),
  comment_count     int NOT NULL DEFAULT 0 CHECK (comment_count >= 0),
  reply_count       int NOT NULL DEFAULT 0 CHECK (reply_count >= 0),
  participant_count int NOT NULL DEFAULT 0 CHECK (participant_count >= 0),
  pending_count     int NOT NULL DEFAULT 0 CHECK (pending_count >= 0),
  last_comment_at   timestamptz,
  last_comment_id   uuid,
  verify_failures int NOT NULL DEFAULT 0 CHECK (verify_failures >= 0),
  verified_at    timestamptz,
  created_by     uuid,                                  -- ai làm Target sinh ra (docs/06 §6.2 b4)
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (service_code, resource_type, resource_id)
);
CREATE INDEX idx_comment_targets_space  ON comment_targets (space_uuid)
  WHERE space_uuid IS NOT NULL;
CREATE INDEX idx_comment_targets_owner  ON comment_targets (owner_type, owner_id);
CREATE INDEX idx_comment_targets_active ON comment_targets (last_comment_at DESC)
  WHERE state = 'active' AND comment_count > 0;
-- hàng chờ worker verify / làm tươi: ưu tiên target có bình luận
CREATE INDEX idx_comment_targets_stale  ON comment_targets (verified_at NULLS FIRST)
  WHERE state IN ('unverified','active');
CREATE TRIGGER trg_comment_targets_updated BEFORE UPDATE ON comment_targets
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comments (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  target_id     uuid NOT NULL REFERENCES comment_targets(id) ON DELETE CASCADE,
  parent_id     uuid REFERENCES comments(id) ON DELETE SET NULL,
  root_id       uuid NOT NULL,                    -- = id nếu là root; KHÔNG đặt FK vì root tự
                                                  -- trỏ ⇒ FK sẽ chặn chính câu INSERT đó
  depth         int  NOT NULL DEFAULT 0 CHECK (depth BETWEEN 0 AND 5),
  reply_to_profile_uuid uuid,                     -- khi trả lời bị làm phẳng (docs/05 §5.2)
  author_kind   text NOT NULL DEFAULT 'profile'
                CHECK (author_kind IN ('profile','space','service')),
  author_profile_uuid uuid NOT NULL,              -- người thật, kể cả khi author_kind='space'
  author_space_uuid   uuid,
  source_service      text,                       -- chỉ với author_kind='service'
  body_md       text NOT NULL,
  body_html     text NOT NULL DEFAULT '',
  body_hash     text NOT NULL DEFAULT '',
  markdown_mode text NOT NULL DEFAULT 'basic'
                CHECK (markdown_mode IN ('plain','basic','rich')),
  status        text NOT NULL DEFAULT 'published'
                CHECK (status IN ('published','pending','hidden','deleted','rejected')),
  moderation_source text CHECK (moderation_source IS NULL OR moderation_source IN
                ('pre','first_comment','banned_word','auto_hide','report','service')),
  is_pinned     boolean NOT NULL DEFAULT false,
  pinned_by     uuid,
  pinned_at     timestamptz,
  reply_count   int NOT NULL DEFAULT 0 CHECK (reply_count >= 0),
  anchor        jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(anchor) = 'object'),
  idempotency_key text,
  edited_at     timestamptz,
  edit_count    int NOT NULL DEFAULT 0 CHECK (edit_count >= 0),
  deleted_at    timestamptz,
  deleted_by    uuid,
  delete_reason text,
  search_tsv    tsvector,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CHECK ((parent_id IS NULL) = (depth = 0)),
  CHECK ((author_kind = 'space')   = (author_space_uuid IS NOT NULL)),
  CHECK ((author_kind = 'service') = (source_service    IS NOT NULL))
);
CREATE UNIQUE INDEX uq_comments_idem ON comments (idempotency_key)
  WHERE idempotency_key IS NOT NULL;

-- Trang root: keyset + ghim lên đầu (docs/05 §5.3)
CREATE INDEX idx_comments_root_page ON comments (target_id, is_pinned DESC, created_at DESC, id DESC)
  WHERE parent_id IS NULL AND status = 'published';
-- Trả lời của một nhánh
CREATE INDEX idx_comments_branch ON comments (root_id, created_at, id)
  WHERE parent_id IS NOT NULL AND status = 'published';
-- Hàng chờ kiểm duyệt theo target
CREATE INDEX idx_comments_pending ON comments (target_id, created_at)
  WHERE status = 'pending';
-- "Bình luận của tôi" xuyên service + kiểm first_comment
CREATE INDEX idx_comments_author ON comments (author_profile_uuid, created_at DESC, id DESC);
CREATE INDEX idx_comments_first ON comments (target_id, author_profile_uuid)
  WHERE status = 'published';
-- Chặn trùng nội dung nhanh (docs/06 §6.6 dùng Redis; index này cho job abuse)
CREATE INDEX idx_comments_hash ON comments (body_hash, created_at DESC) WHERE body_hash <> '';
CREATE INDEX idx_comments_tsv  ON comments USING GIN (search_tsv);

CREATE OR REPLACE FUNCTION comments_tsv_trg() RETURNS trigger
  LANGUAGE plpgsql AS
  $$ BEGIN NEW.search_tsv = ludiskus_tsv(NEW.body_md); RETURN NEW; END $$;
CREATE TRIGGER trg_comments_tsv BEFORE INSERT OR UPDATE OF body_md ON comments
  FOR EACH ROW EXECUTE FUNCTION comments_tsv_trg();
CREATE TRIGGER trg_comments_updated BEFORE UPDATE ON comments
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE comment_mentions (
  comment_id   uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  profile_uuid uuid NOT NULL,
  PRIMARY KEY (comment_id, profile_uuid)
);
CREATE INDEX idx_comment_mentions_profile ON comment_mentions (profile_uuid);

CREATE TABLE comment_participants (
  target_id      uuid NOT NULL REFERENCES comment_targets(id) ON DELETE CASCADE,
  profile_uuid   uuid NOT NULL,
  reason         text NOT NULL DEFAULT 'manual'
                 CHECK (reason IN ('authored','replied','mentioned','owner','manual')),
  muted          boolean NOT NULL DEFAULT false,
  last_read_at   timestamptz,
  last_notified_at timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (target_id, profile_uuid)
);
-- Fan-out thông báo: chỉ người chưa mute
CREATE INDEX idx_comment_participants_fanout ON comment_participants (target_id)
  WHERE NOT muted;
-- Hộp thư của một người
CREATE INDEX idx_comment_participants_profile ON comment_participants (profile_uuid);

-- Registry khởi tạo: khớp interaction_services của lufami (QĐ-16).
INSERT INTO comment_services (code, name, base_url, verify_mode) VALUES
  ('ludiskus','Ludiskus','',                          'trust'),
  ('lumuse',  'Lumuse',  'http://lumuse-api:8080',    'optimistic'),
  ('lukolek', 'Lukolek', 'http://lukolek-api:8080',   'optimistic'),
  ('lukode',  'Lukode',  'http://lukode-api:8080',    'optimistic'),
  ('lugame',  'Lugame',  'http://lugame-api:8080',    'optimistic'),
  ('lushoop', 'Lushoop', 'http://lushoop-api:8080',   'optimistic'),
  ('lutriip', 'Lutriip', 'http://lutriip-api:8080',   'optimistic'),
  ('lukomik', 'Lukomik', 'http://lukomik-api:8080',   'optimistic'),
  ('luxtory', 'Luxtory', 'http://luxtory-api:8080',   'optimistic'),
  ('lubo',    'Lubo',    'http://lubo-api:8080',      'optimistic'),
  ('luprojet','Luprojet','http://luprojet-api:8080',  'strict'),
  ('luservit','Luservit','http://luservit-api:8080',  'optimistic'),
  ('luwep',   'Luwep',   'http://luwep-api:8080',     'optimistic'),
  ('lufoodi', 'Lufoodi', 'http://lufoodi-api:8080',   'optimistic'),
  ('lutat',   'Lutat',   'http://lutat-api:8080',     'strict'),
  ('luskool', 'Luskool', 'http://luskool-api:8080',   'strict'),
  ('lufami',  'Lufami',  'http://lufami-api:8080',    'strict')
ON CONFLICT (code) DO NOTHING;
```

> `oauth_client_id` **không** seed được ở migration (mỗi môi trường có client id khác nhau) —
> nạp bằng `POST /api/v1/admin/comment-services` khi triển khai, hoặc bằng biến môi trường
> `LUDISKUS_COMMENT_SERVICE_CLIENTS="lumuse=abc,lukolek=def"` đọc lúc khởi động
> ([14 §14.1](14-trien-khai-docker.md)). Không có `oauth_client_id` ⇒ service đó **không** dùng
> được nhóm S2S (đọc/ghi qua người dùng vẫn chạy).

Policy khởi tạo nạp từ `db/seeds/comment_policies.json` lúc khởi động (mẫu `loadSeeds()` sẵn
có), **không** nhồi vào migration — để sửa policy mặc định không cần migration mới. Bảng đầy
đủ: [13 §13.3](13-tich-hop-service.md).

## 10.4 `0003` — bổ sung cho bảng cũ

```sql
-- Tuổi Profile phục vụ siết tài khoản mới (docs/06 §6.6).
ALTER TABLE profile_cache ADD COLUMN IF NOT EXISTS created_at timestamptz;
```

`identity.fetchProfile` gán thêm trường này khi HipCore trả về; Profile cũ chưa có ⇒ `NULL` ⇒
**không** bị coi là mới (fail open cho tiện dụng, vì đây là chống spam chứ không phải quyền).

## 10.5 `0004_comment_report_enum.up.sql` — chỉ một câu

```sql
-- QĐ-12: PostgreSQL cấm DÙNG giá trị enum mới trong cùng transaction đã thêm nó
-- (ERROR: unsafe use of new value "comment" of enum type report_target).
-- database.Migrate bọc MỖI file trong một transaction ⇒ phải tách file riêng.
-- File này KHÔNG được thêm bất cứ câu lệnh nào khác.
ALTER TYPE report_target ADD VALUE IF NOT EXISTS 'comment';
```

`.down.sql`: PostgreSQL **không** hỗ trợ xoá giá trị enum. File down chỉ chứa một comment giải
thích + `SELECT 1;` — và điều này phải được nêu trong PR, không phải để người đọc tự phát hiện.

## 10.6 `0005_comment_moderation.up.sql`

```sql
-- Từ đây mới được dùng giá trị 'comment' của report_target.

-- (1) Ba cột space_uuid NOT NULL của 0001 phải nới thành nullable: Target có thể
--     KHÔNG thuộc Space nào (docs/comment/03 §3.3), lúc đó không có giá trị hợp lệ
--     nào để ghi. Mọi truy vấn theo Space đã có `WHERE space_uuid = $1` nên không
--     bị ảnh hưởng; hàng chờ không có Space chỉ giải quyết được qua S2S (07 §7.1).
ALTER TABLE attachments      ALTER COLUMN space_uuid DROP NOT NULL;
ALTER TABLE reports          ALTER COLUMN space_uuid DROP NOT NULL;
ALTER TABLE moderation_items ALTER COLUMN space_uuid DROP NOT NULL;

-- (2) Đính kèm cho bình luận — tái dùng bảng cũ (QĐ-13).
ALTER TABLE attachments ADD COLUMN comment_id uuid REFERENCES comments(id) ON DELETE CASCADE;
ALTER TABLE attachments ADD CONSTRAINT attachments_owner_one
  CHECK (num_nonnulls(post_id, comment_id) <= 1);
CREATE INDEX idx_attach_comment ON attachments (comment_id) WHERE comment_id IS NOT NULL;

-- (3) Một người báo cáo một target một lần (khi report còn mở).
--     CẢNH BÁO: index này áp cho CẢ report cũ của topic/post. DB đang chạy có thể
--     đã có hàng trùng ⇒ phải dọn TRƯỚC trong cùng migration, nếu không CREATE
--     UNIQUE INDEX thất bại và cả file bị rollback.
DELETE FROM reports r USING reports keep
 WHERE r.status = 'open' AND keep.status = 'open'
   AND r.target_type = keep.target_type AND r.target_id = keep.target_id
   AND r.reporter_profile_uuid = keep.reporter_profile_uuid
   AND r.created_at > keep.created_at;
CREATE UNIQUE INDEX uq_reports_open_reporter
  ON reports (target_type, target_id, reporter_profile_uuid) WHERE status = 'open';
CREATE INDEX idx_reports_comment_open
  ON reports (target_id) WHERE target_type = 'comment' AND status = 'open';
CREATE INDEX idx_moditems_comment_pending
  ON moderation_items (space_uuid, created_at) WHERE target_type = 'comment' AND state = 'pending';

CREATE TABLE comment_revisions (
  comment_id uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  revision   int  NOT NULL CHECK (revision >= 1),
  body_md    text NOT NULL,
  edited_by  uuid NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (comment_id, revision)
);
```

> **Hai cái bẫy trong migration này.**
>
> 1. **`space_uuid NOT NULL`.** `attachments`, `reports`, `moderation_items` đều khai
>    `space_uuid uuid NOT NULL` ở `0001` (dòng 164, 190, 202). Bình luận trên Target không
>    thuộc Space nào **không có** giá trị hợp lệ để ghi ⇒ phải nới cả ba. Không nới ⇒ đính kèm
>    và báo cáo chỉ chạy được cho Target có Space, và lỗi chỉ lộ ra lúc chạy thật.
> 2. **`uq_reports_open_reporter` áp cả dữ liệu cũ.** Forum hiện **không** chặn báo cáo trùng,
>    nên DB đang chạy có thể đã có nhiều hàng `open` cùng `(target, reporter)`. Phải dọn ngay
>    trước khi tạo index, trong cùng file — nếu không thì migration thất bại trên môi trường
>    thật mà lại chạy tốt trên DB rỗng.

## 10.7 `0006_comment_ops.up.sql`

```sql
CREATE TABLE comment_notify_buffer (
  id           bigserial PRIMARY KEY,
  event_type   text NOT NULL,
  recipient_profile_uuid uuid NOT NULL,
  target_id    uuid NOT NULL REFERENCES comment_targets(id) ON DELETE CASCADE,
  comment_id   uuid NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
  actor_profile_uuid uuid NOT NULL,
  occurred_at  timestamptz NOT NULL DEFAULT now(),
  flush_after  timestamptz NOT NULL,
  UNIQUE (event_type, recipient_profile_uuid, target_id, comment_id)
);
CREATE INDEX idx_comment_notify_due ON comment_notify_buffer (flush_after, id);

CREATE TABLE comment_abuse_flags (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  profile_uuid uuid NOT NULL,
  signal       text NOT NULL CHECK (signal IN
               ('burst','same_body','link_spam','report_magnet','report_abuse')),
  evidence     jsonb NOT NULL DEFAULT '{}'::jsonb,
  state        text NOT NULL DEFAULT 'open'
               CHECK (state IN ('open','dismissed','throttled','pre_moderated')),
  decided_by   uuid,
  decided_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_comment_abuse_open ON comment_abuse_flags (created_at DESC)
  WHERE state = 'open';
CREATE INDEX idx_comment_abuse_profile ON comment_abuse_flags (profile_uuid, created_at DESC);

CREATE TABLE comment_audit_logs (
  id          bigserial PRIMARY KEY,
  actor       text NOT NULL,                 -- 'profile:{uuid}' | 'service:{code}' | 'system'
  actor_profile_uuid uuid,
  action      text NOT NULL,
  target_id   uuid,
  comment_id  uuid,
  reason      text,
  detail      jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_comment_audit_comment ON comment_audit_logs (comment_id, created_at DESC);
CREATE INDEX idx_comment_audit_time    ON comment_audit_logs (created_at DESC);

-- View đối soát số đếm (docs/05 §5.8 luật 4). Chỉ đọc, dùng bởi job và trang quản trị.
CREATE VIEW comment_count_check AS
SELECT t.id AS target_id,
       t.comment_count,     c.roots      AS real_comment_count,
       t.reply_count,       c.replies    AS real_reply_count,
       t.pending_count,     c.pendings   AS real_pending_count,
       t.participant_count, p.people     AS real_participant_count
  FROM comment_targets t
  LEFT JOIN LATERAL (
    SELECT count(*) FILTER (WHERE parent_id IS NULL     AND status = 'published') AS roots,
           count(*) FILTER (WHERE parent_id IS NOT NULL AND status = 'published') AS replies,
           count(*) FILTER (WHERE status = 'pending')                             AS pendings
      FROM comments WHERE target_id = t.id
  ) c ON true
  LEFT JOIN LATERAL (
    SELECT count(*) AS people FROM comment_participants WHERE target_id = t.id
  ) p ON true
 WHERE t.comment_count     <> c.roots
    OR t.reply_count       <> c.replies
    OR t.pending_count     <> c.pendings
    OR t.participant_count <> p.people;
```

## 10.8 File `.down.sql`

| File | Nội dung |
|------|----------|
| `0003_comment_core.down.sql` | `DROP TABLE comment_participants, comment_mentions, comments, comment_targets, comment_policies, comment_services CASCADE;` `DROP FUNCTION comments_tsv_trg();` `ALTER TABLE profile_cache DROP COLUMN created_at;` |
| `0004_comment_report_enum.down.sql` | Không thể xoá giá trị enum trong PostgreSQL — chỉ comment giải thích + `SELECT 1;` |
| `0005_comment_moderation.down.sql` | `DROP TABLE comment_revisions;` các `DROP INDEX`; `ALTER TABLE attachments DROP CONSTRAINT attachments_owner_one, DROP COLUMN comment_id;` `ALTER TABLE attachments/reports/moderation_items ALTER COLUMN space_uuid SET NOT NULL;` (chỉ chạy được nếu đã xoá các hàng không có Space); `DROP INDEX uq_reports_open_reporter` — **không** phục hồi được các report trùng đã bị dọn |
| `0006_comment_ops.down.sql` | `DROP VIEW comment_count_check; DROP TABLE comment_audit_logs, comment_abuse_flags, comment_notify_buffer;` |
| `0007_comment_score.down.sql` | Xóa index trang `top`, unique index abuse đang mở và cột `comments.score_cache`. |
| `0008_comment_canonical_path.down.sql` | Khôi phục constraint canonical path bản tương thích cũ. |

Kiểm bắt buộc: `up → down → up` trên DB **có dữ liệu thật** không lỗi ([15 §15.2](15-lo-trinh.md)).

## 10.9 Đối soát & vận hành số đếm

| Việc | Khi | Cách |
|------|-----|------|
| Sửa lệch | Job đêm (`comment_reconcile`, 03:00) | Đọc `comment_count_check` (thường trả 0 hàng); mỗi hàng ⇒ `UPDATE` về giá trị thật + `comment_audit_logs(action='reconcile_count')` + log `error` |
| Đếm âm | Ngay khi phát hiện | `CHECK` ở DB làm transaction thất bại ⇒ API trả `500`, log `error` kèm `target_id`; job sửa ở lần chạy gần nhất. Không "im lặng kẹp về 0" trong mã ứng dụng |
| Rebuild thủ công | Khi vận hành yêu cầu | `POST /api/v1/admin/comments/reconcile-counters?target=` hoặc toàn bộ (chạy theo lô 500 Target, có log tiến độ) |
| Dọn Target mồ côi | Job giờ | `state='gone'` **và** `comment_count = 0` **và** `updated_at < now() - 30 ngày` ⇒ `DELETE` (cascade xoá participant/mention) |
| Dọn buffer chết | Job giờ | Hàng `comment_notify_buffer` có `flush_after < now() - 1 ngày` (bình luận đã bị xoá trước khi flush) ⇒ `DELETE` |
| Dọn revision dư | Job ngày | Giữ `LUDISKUS_COMMENT_MAX_REVISIONS` bản mới nhất mỗi bình luận |
| Dọn audit | Job ngày | `created_at < now() - LUDISKUS_COMMENT_AUDIT_RETENTION_DAYS` |

## 10.10 Ước lượng khối lượng & khi nào phải partition

Giả định thực tế của hippo: 20 service × 50.000 Resource có bình luận × trung bình 4 bình luận
= **4 triệu hàng `comments`**, ~1KB/hàng ⇒ ~4GB + index. Postgres một node xử lý thoải mái với
các index ở §10.3 (mọi truy vấn nóng đều có index một phần và đều bị chặn bởi `target_id` hoặc
`author_profile_uuid`).

**Không partition ở v1.** Ngưỡng cần xem lại: `comments` > 50 triệu hàng, hoặc một Target > 100
nghìn bình luận (lúc đó `idx_comments_branch` vẫn đủ, nhưng `comment_count_check` chạy đêm sẽ
nặng ⇒ đổi sang đối soát theo lô Target thay vì view toàn bảng).
