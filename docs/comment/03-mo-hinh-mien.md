# 03 — Mô hình miền

Schema SQL đầy đủ ở [10](10-database.md); tài liệu này nói **ý nghĩa** và **vòng đời**.

## 3.1 Bản đồ thực thể

```
comment_services (registry)
   │ 1
   │
   ├───────────────▶ comment_policies (service_code, resource_type='*'|cụ thể)
   │  n
   │ n
   ▼
comment_targets ──1:n──▶ comments ──1:n──▶ comment_mentions
   │  (1 Resource = 1 Target)   │      └─1:n──▶ comment_revisions
   │                            │      └─1:n──▶ attachments (comment_id)
   ├──1:n──▶ comment_participants│      └─1:n──▶ reports / moderation_items (target_type='comment')
   │          (muted, last_read_at)
   └──1:n──▶ comment_notify_buffer

  Ở NGOÀI ludiskus:  lufami.interaction_* cho ref  ludiskus:comment:{id}   (QĐ-5)
```

## 3.2 `ResourceRef` — tham chiếu nội dung

```go
type ResourceRef struct {
    Service string `json:"service"` // = comment_services.code, vd "lumuse"
    Type    string `json:"type"`    // vd "movie"      ^[a-z][a-z0-9_]{0,59}$
    ID      string `json:"id"`      // vd "01JZ…"      ^[A-Za-z0-9_.:-]{1,100}$
}
func (r ResourceRef) Validate() error
func (r ResourceRef) String() string // "lumuse:movie:01JZ…"
```

Ba luật bất di bất dịch:

1. **Charset y hệt Interaction Platform** (QĐ-2). Không nới, không siết.
2. Service phải dùng **một dạng canonical duy nhất** cho `resource_id`. Hôm nay ghi uuid, mai
   ghi slug ⇒ hai Target khác nhau cho cùng một nội dung, số bình luận **tách đôi** và không
   có cách hợp lại tự động.
3. `ResourceRef` **không bao giờ** chứa URL. Đường dẫn hiển thị là `canonical_path` do
   resolver khai, đã validate ([04 §4.2](04-hop-dong-resource.md)).

## 3.3 `CommentTarget` — mục tiêu bình luận

Bản chiếu **tối thiểu** của Resource + trạng thái luồng. **Không sao chép nội dung** của
service khác (chỉ `title`/`summary` ngắn để hiện trong hộp thư và thông báo).

| Nhóm trường | Trường | Ghi chú |
|-------------|--------|---------|
| Danh tính | `id`, `service_code`, `resource_type`, `resource_id` | `UNIQUE (service_code, resource_type, resource_id)` |
| Ngữ cảnh | `space_uuid` (nullable), `owner_type` (`profile`\|`space`), `owner_id` | `space_uuid` quyết định moderator Space nào có quyền |
| Hiển thị | `title`, `summary`, `thumbnail_url`, `canonical_path` | Do resolver khai; escape khi render |
| Quyền | `visibility ∈ (public, authenticated, space, connections, private)` | Nguồn duy nhất cho quyền đọc Thread ([06 §6.2](06-phan-quyen.md)) |
| Trạng thái nội dung | `state ∈ (unverified, active, gone, blocked)` | `verify_failures`, `verified_at` |
| Trạng thái luồng | `thread_state ∈ (open, locked, closed, hidden)` | **Khác** `state`: `state` nói về Resource, `thread_state` nói về luồng |
| Khả dụng | `capabilities jsonb` | Do resolver khai — chỉ **thu hẹp** policy |
| Số đếm | `comment_count`, `reply_count`, `participant_count`, `pending_count`, `last_comment_at`, `last_comment_id` | `comment_count` đếm **root published**; `reply_count` đếm reply published |
| Thời gian | `created_at`, `updated_at` | trigger `set_updated_at()` |

**`state` vs `thread_state`:**

| | `state` | `thread_state` |
|---|---|---|
| Nói về | Resource ở service sở hữu | Luồng bình luận ở `ludiskus` |
| Ai đổi | Resolver / worker verify / push S2S | Chủ nội dung, moderator, service (S2S), tự động khi báo cáo vượt ngưỡng |
| Giá trị & hệ quả | `unverified`: đọc được nếu người gọi có quyền, **không** thông báo, **không** công khai · `active`: bình thường · `gone`: `410`, ẩn Thread khỏi mọi API đọc · `blocked`: `403` | `open`: viết được · `locked`: chỉ đọc, vẫn hiện (`423` khi viết) · `closed`: chỉ đọc, ẩn composer, dùng khi nội dung đã kết thúc · `hidden`: moderator ẩn toàn luồng, chỉ moderator thấy |

Target được tạo **lười (lazy)**: request đọc/viết đầu tiên cho một ref sẽ `INSERT … ON CONFLICT
DO UPDATE` (xem [04 §4.5](04-hop-dong-resource.md)). Không có bước "đăng ký nội dung" bắt buộc.

## 3.4 `Comment` — bình luận

| Nhóm | Trường | Ghi chú |
|------|--------|---------|
| Danh tính | `id uuid`, `target_id`, `idempotency_key` (nullable, UNIQUE) | `idempotency_key` chống double-submit |
| Cây | `parent_id` (nullable), `root_id` (self nếu là root), `depth`, `reply_to_profile_uuid` (nullable) | [05](05-cay-va-phan-trang.md) |
| Tác giả | `author_kind ∈ (profile, space, service)`, `author_profile_uuid`, `author_space_uuid` (nullable), `source_service` (nullable) | `service` dùng cho bình luận hệ thống qua S2S |
| Nội dung | `body_md`, `body_html`, `body_hash`, `markdown_mode ∈ (plain, basic, rich)`, `search_tsv` | `body_hash` = sha256 nội dung đã chuẩn hoá, dùng chặn trùng |
| Trạng thái | `status ∈ (published, pending, hidden, deleted, rejected)` | Xem §3.6 |
| Nhấn mạnh | `is_pinned`, `pinned_by`, `pinned_at` | Ghim theo policy (`owner` hoặc `moderator`) |
| Số đếm | `reply_count` (published) | `UPDATE ±1` cùng transaction |
| Sửa/xoá | `edited_at`, `edit_count`, `deleted_at`, `deleted_by`, `delete_reason` | Xoá **mềm**: giữ hàng, xoá `body_md`/`body_html` khi `deleted_by ≠ author` là **không** — xem §3.7 |
| Kiểm duyệt | `moderation_source` (nullable: `pre`, `first_comment`, `banned_word`, `auto_hide`, `report`, `service`) | Vì sao bình luận này bị giữ/ẩn |
| Neo (để dành v2) | `anchor jsonb NOT NULL DEFAULT '{}'` | Bình luận theo toạ độ/dòng ([01 §1.2](01-tong-quan.md) ngoài phạm vi) |
| Thời gian | `created_at`, `updated_at` | |

**Không có** trong `comments`: `like_count`, `reaction_count`, `score` (QĐ-5), `title`, `slug`,
`tags`, `is_answer` (QĐ-8).

## 3.5 `CommentPolicy` & `Capabilities`

`comment_policies (service_code, resource_type, config jsonb)` với `resource_type='*'` là mặc
định của service. Cấu trúc `config` đầy đủ và thứ tự hợp nhất 4 tầng: [04 §4.6](04-hop-dong-resource.md).

`Capabilities` là **kết quả** hợp nhất cho một Target cụ thể, luôn trả kèm mọi response đọc
Thread để frontend không phải biết policy:

```json
{
  "canRead": true, "canComment": true, "canReply": true,
  "canAttach": false, "canMention": true, "canPin": false, "canModerate": false,
  "maxDepth": 2, "maxLength": 4000, "markdown": "basic",
  "editWindowMinutes": 15, "sortOptions": ["newest", "oldest"],
  "interaction": { "like": true, "reaction": true },
  "reasons": { "canAttach": "policy_disabled" }
}
```

`reasons` chỉ xuất hiện cho các cờ **false** và chỉ nhận giá trị trong tập cố định
(`policy_disabled`, `not_member`, `thread_locked`, `not_authenticated`, `rate_limited`,
`resource_gone`) — giao diện dịch sang câu tiếng Việt, **không** hiện chuỗi thô.

## 3.6 Máy trạng thái của `Comment`

```
                     ┌──────────────────────────────────────────────────┐
   [tạo]             │                                                  │
     ├─ mode none/post ─────────────────────▶ published ──[sửa]──▶ published (edited_at)
     │                                          │  │
     ├─ mode pre / first_comment ─▶ pending ─────┼──┼── approve ─▶ published  (⇒ MỚI phát thông báo)
     │                                │          │  │
     │                                └─ reject ─┼──┼─────────────▶ rejected  (tác giả được báo)
     │                                           │  │
     ├─ trùng từ cấm (mode none/post) ─▶ published + moderation_item(banned_word)
     │                                           │  │
     │  báo cáo ≥ ngưỡng ──────────────────▶ hidden (auto_hide) ── approve ─▶ published
     │                                           │              └─ reject ──▶ rejected
     │  moderator/chủ nội dung/service ẩn ─▶ hidden ── restore ─▶ published
     │                                           │
     └─ tác giả xoá / moderator xoá ───────▶ deleted (mềm, không quay lại)
```

Bất biến:

- Chỉ `published` được tính vào `comment_count`/`reply_count` và được trả cho người không
  phải tác giả/moderator.
- `pending` **không** phát bất kỳ thông báo nào (kể cả @mention) cho tới khi `approve` — sao
  y luật của forum ([04 §4.6](../04-kiem-duyet.md)).
- `deleted` là trạng thái cuối. Không có "khôi phục bình luận đã xoá" trong API (dữ liệu vẫn
  còn trong DB để điều tra; khôi phục là việc thủ công của vận hành).
- Đổi trạng thái **luôn** kèm cập nhật số đếm trong cùng transaction; delta được tính bởi một
  hàm duy nhất `countDelta(oldStatus, newStatus, isRoot)` (có unit test cho cả 25 cặp).

## 3.7 Xoá mềm và điều gì còn hiện

Xoá bình luận trong một cây là bài toán quen: xoá node cha thì con đi đâu?

| Tình huống | Hành vi |
|-----------|---------|
| Xoá bình luận **không có** trả lời published | `status=deleted`; **ẩn hoàn toàn** khỏi danh sách |
| Xoá bình luận **có** trả lời published | `status=deleted`, giữ chỗ trong cây dưới dạng **bia mộ**: `{ "deleted": true, "deletedAt": …, "byAuthor": true|false }`, `body_md`/`body_html` **không** trả về API |
| Tác giả tự xoá | `deleted_by = author_profile_uuid`, giao diện hiện "Bình luận đã được người viết xoá" |
| Moderator/chủ nội dung xoá | `deleted_by ≠ author`, giao diện hiện "Bình luận đã bị xoá bởi kiểm duyệt"; tác giả nhận thông báo `comment.moderated` |
| Target `state='gone'` | Toàn bộ Thread ngừng trả về (`410`); **không** xoá dữ liệu (service có thể phục hồi nội dung) |

`body_md` của bình luận đã xoá **vẫn nằm trong DB** (điều tra lạm dụng, khiếu nại) nhưng
**không bao giờ** ra khỏi API — kể cả cho moderator, trừ endpoint `GET
/comments/items/{id}/revisions` dành cho moderator ([08 §8.6](08-noi-dung-va-dinh-kem.md)).

## 3.8 `CommentParticipant`

`(target_id, profile_uuid)` + `reason ∈ (authored, replied, mentioned, owner, manual)`,
`muted bool`, `last_read_at`, `last_notified_at`.

Ba việc trong một bảng:

1. **Fan-out thông báo**: ai cần biết có bình luận mới (trừ người tự viết, trừ `muted`).
2. **Đã đọc / chưa đọc**: `last_read_at` so với `target.last_comment_at` ⇒ badge.
3. **Theo dõi thủ công**: người chỉ đọc vẫn bấm "Theo dõi" được (`reason='manual'`).

Tự động thêm participant khi: viết bình luận (`authored`), được trả lời (`replied`), được
mention (`mentioned`), là chủ nội dung tại thời điểm Target được tạo (`owner`). **Không** tự
thêm khi chỉ đọc.

## 3.9 `CommentRevision`

`(comment_id, revision, body_md, edited_by, created_at)` — giữ **tối đa
`LUDISKUS_COMMENT_MAX_REVISIONS`** (mặc định 10) bản gần nhất, worker dọn phần dư.

Vì sao cần: sửa bình luận sau khi có người trả lời là cách kinh điển để bóp méo hội thoại.
Giao diện luôn hiện nhãn "đã sửa" kèm thời điểm; moderator xem được nội dung trước đó.

## 3.10 Quan hệ với các bảng sẵn có của `ludiskus`

| Bảng sẵn có | Dùng thế nào |
|-------------|--------------|
| `profile_cache`, `space_cache`, `space_member_cache` | Nguồn tên/avatar tác giả và vai trò trong Space. **Không** gọi HipCore trong hot-path ([05](../05-cache-profile-space.md)) |
| `space_moderators` | Moderator Space cho Target có `space_uuid` |
| `attachments` | Thêm cột `comment_id`; `CHECK` đúng một trong `post_id`/`comment_id` (QĐ-13) |
| `reports`, `moderation_items` | Thêm giá trị enum `'comment'` cho `report_target` (QĐ-12) |
| `outbox` | Dùng chung, phân biệt bằng tiền tố `event_type = 'ludiskus.comment.*'` |
| `space_forums.banned_words` | Nguồn từ cấm khi Target có `space_uuid` và policy đặt `banned_words_source='space'` ([07 §7.3](07-kiem-duyet.md)) |
| `topics`, `posts`, `boards` | **Không dùng, không JOIN** (QĐ-8 + §2.4) |
