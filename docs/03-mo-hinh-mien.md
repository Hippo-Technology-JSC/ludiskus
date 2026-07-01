# 03 — Mô hình miền

Mô hình xoay quanh: **Space (cộng đồng) → Board (chuyên mục) → Topic (chủ đề) →
Post (bài/trả lời)**, kèm Reaction, Tag, Attachment, Subscription và Report.

```
Space (HipCore, cache) 1──* Board 1──* Topic 1──* Post
                                                  │   │
        Tag *──* Topic ──────────────────────────┘   ├──* Reaction
                                                      ├──* Attachment
        Subscription (Profile theo dõi Space/Board/Topic)
        Report (Profile báo cáo Post/Topic) ──▶ ModerationItem
```

> **Space, Profile, SpaceMember thuộc HipCore** — ludiskus chỉ giữ **cache**
> (xem [05](05-cache-profile-space.md)); các bảng dưới đây chỉ lưu `*_uuid` trỏ
> sang HipCore, **không phải FK** liên DB.

## 3.1 SpaceForum (cấu hình forum cho một Space)

Một Space của HipCore "trở thành" cộng đồng/forum khi có bản ghi cấu hình này.
Đây là nơi đặt **cấu hình kiểm duyệt** và quyền đăng bài.

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `space_uuid` | uuid (PK) | Space của HipCore |
| `enabled` | bool | Forum đã bật cho Space chưa |
| `is_public` | bool | Đồng bộ từ Space; quyết định người ngoài đọc được không |
| `post_policy` | enum | `members` (mặc định) \| `anyone_authenticated` \| `staff_only` |
| `moderation_mode` | enum | `none` \| `post` (hậu kiểm) \| `pre` (tiền kiểm) \| `first_post` (kiểm bài đầu của thành viên mới) — xem [04](04-kiem-duyet.md) |
| `banned_words` | text[] | Danh sách từ cấm (lọc tự động) |
| `report_auto_hide_threshold` | int | Số báo cáo để tự ẩn chờ duyệt (0 = tắt) |
| `default_topic_type` | enum | `discussion` \| `question` \| `announcement` |
| `settings` | jsonb | Mở rộng (cho phép tag tự do, cho đính kèm, kích thước tối đa…) |
| `created_at`/`updated_at` | timestamptz | |

## 3.2 Board (Chuyên mục)

Phân vùng trong một Space (sub-forum). Một Space có nhiều Board; Board có thể
lồng một cấp (`parent_id`).

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `id` | uuid | Khoá chính |
| `space_uuid` | uuid | → Space |
| `parent_id` | uuid? | Board cha (lồng một cấp) |
| `code` | text | Định danh trong Space (unique theo `space_uuid`) |
| `name` | text | Tên hiển thị |
| `description_md` | text? | Mô tả board — **Markdown** (giới thiệu, nội quy chuyên mục) |
| `description_html` | text? | HTML đã render & sanitize từ `description_md` |
| `kind` | enum | `forum` \| `qna` \| `support` \| `announcement` — gợi ý UI & hành vi mặc định |
| `position` | int | Thứ tự sắp xếp |
| `is_locked` | bool | Khoá đăng topic mới |
| `min_role` | enum | Vai trò tối thiểu để đăng (`member`/`moderator`/`admin`) |
| `topic_count` / `post_count` | int | Đếm (cập nhật nền) |
| `last_activity_at` | timestamptz? | Hoạt động gần nhất (sắp xếp) |
| `created_at`/`updated_at` | timestamptz | |

## 3.3 Topic (Chủ đề / Thread)

Một luồng thảo luận. **Post đầu tiên** chứa nội dung mở đầu của Topic.

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `id` | uuid | Khoá chính |
| `space_uuid` | uuid | → Space (lặp lại để truy vấn/quyền nhanh) |
| `board_id` | uuid | → Board |
| `author_profile_uuid` | uuid | Người tạo (Profile) |
| `title` | text | Tiêu đề |
| `slug` | text | Slug từ title (unique theo space) cho URL đẹp |
| `type` | enum | `discussion` \| `question` \| `announcement` |
| `status` | enum | `published` \| `pending` (chờ duyệt) \| `locked` \| `hidden` \| `deleted` |
| `is_pinned` | bool | Ghim đầu board |
| `is_resolved` | bool | (type=question/support) đã có câu trả lời được chấp nhận |
| `answer_post_id` | uuid? | Post được đánh dấu là câu trả lời |
| `reply_count` | int | Số trả lời (không tính post đầu) |
| `view_count` | int | Lượt xem |
| `reaction_count` | int | Tổng reaction |
| `last_post_at` | timestamptz? | Mốc post mới nhất (sắp xếp "mới hoạt động") |
| `last_post_profile_uuid` | uuid? | Người trả lời gần nhất |
| `search_tsv` | tsvector | Chỉ mục FTS (title + body post đầu) — [06](06-tim-kiem.md) |
| `created_at`/`updated_at` | timestamptz | |

> **Nội dung Topic là Markdown** — thân chủ đề nằm ở **Post đầu** (`is_first=true`,
> `body_md`/`body_html` ở §3.4), nên không nhân đôi trường nội dung trên Topic.
> `title` giữ **text trơn** (không Markdown) để dùng cho slug, FTS và `ts_headline`.

## 3.4 Post (Bài / Trả lời)

Mỗi message trong Topic. Post đầu (`is_first=true`) là thân Topic; các post sau
là trả lời, hỗ trợ lồng (`reply_to_id`).

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `id` | uuid | Khoá chính |
| `topic_id` | uuid | → Topic |
| `space_uuid` | uuid | → Space (lặp lại cho quyền/truy vấn) |
| `author_profile_uuid` | uuid | Người viết |
| `reply_to_id` | uuid? | Post được trả lời (lồng/threading) |
| `is_first` | bool | Post mở đầu Topic |
| `body_md` | text | Nội dung gốc (Markdown) |
| `body_html` | text | HTML đã render & sanitize (phục vụ hiển thị) |
| `is_answer` | bool | (Q&A) được chấp nhận làm câu trả lời |
| `status` | enum | `published` \| `pending` \| `hidden` \| `deleted` |
| `reaction_count` | int | Tổng reaction |
| `edited_at` | timestamptz? | Lần sửa gần nhất |
| `search_tsv` | tsvector | Chỉ mục FTS theo nội dung post |
| `created_at`/`updated_at` | timestamptz | |

> **Mention**: khi lưu, service trích `@code`/`@uuid` trong `body_md`, phân giải
> sang `profile_uuid` (qua cache) và ghi bảng `post_mentions` để đẩy event
> mention sang lunoti ([08](08-tich-hop-lunoti.md)).

## 3.5 Reaction

Phản ứng (emoji/like) của một Profile lên một Post.

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `post_id` | uuid | → Post |
| `profile_uuid` | uuid | Người reaction |
| `kind` | text | `like`, `love`, `up`, `down`, hoặc emoji code |
| `created_at` | timestamptz | |
| PK | (post_id, profile_uuid, kind) | Mỗi người 1 reaction/kind/post |

## 3.6 Tag & TopicTag

Nhãn chủ đề, theo vốn từ của từng Space.

| Tag | Kiểu | Mô tả |
|-----|------|-------|
| `id` | uuid | Khoá chính |
| `space_uuid` | uuid | Tag thuộc Space |
| `slug` | text | Định danh (unique theo space) |
| `name` | text | Hiển thị |
| `usage_count` | int | Số topic dùng (cập nhật nền) |

`topic_tags(topic_id, tag_id)` — quan hệ nhiều-nhiều.

## 3.7 Attachment (Đính kèm)

Tập tin/hình ảnh gắn vào Post, lưu ở MinIO — [07](07-dinh-kem.md).

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `id` | uuid | Khoá chính |
| `space_uuid` | uuid | → Space (cho quyền/truy vết) |
| `post_id` | uuid? | → Post (null khi mới upload, chưa gắn — nháp) |
| `uploader_profile_uuid` | uuid | Người tải lên |
| `object_key` | text | Khoá object trong bucket MinIO |
| `file_name` | text | Tên gốc |
| `content_type` | text | MIME |
| `size_bytes` | bigint | Kích thước |
| `kind` | enum | `image` \| `file` |
| `width`/`height` | int? | Với ảnh |
| `status` | enum | `pending` (đã upload, chưa gắn) \| `attached` \| `orphaned` |
| `created_at` | timestamptz | |

## 3.8 Subscription (Theo dõi)

Profile theo dõi để nhận thông báo. Cấp theo Space / Board / Topic.

| Thuộc tính | Kiểu | Mô tả |
|------------|------|-------|
| `id` | uuid | Khoá chính |
| `profile_uuid` | uuid | Người theo dõi |
| `target_type` | enum | `space` \| `board` \| `topic` |
| `target_id` | uuid | uuid Space / id Board / id Topic |
| `reason` | enum | `manual` \| `authored` (tự theo dõi topic mình tạo) \| `participated` (đã trả lời) |
| `muted` | bool | Tắt thông báo dù vẫn theo dõi |
| `created_at` | timestamptz | |
| UNIQUE | (profile_uuid, target_type, target_id) | |

> Mặc định: tạo topic → `authored`; trả lời topic → `participated` (trừ khi đã
> tắt). Người theo dõi Topic nhận event `topic.replied`; xem [08](08-tich-hop-lunoti.md).

## 3.9 Report & ModerationItem

- **Report** — một Profile báo cáo Post/Topic vi phạm: `id`, `space_uuid`,
  `target_type` (`post`/`topic`), `target_id`, `reporter_profile_uuid`,
  `reason` (enum: `spam`, `abuse`, `offtopic`, `other`), `note?`, `status`
  (`open`/`resolved`/`dismissed`), `created_at`.
- **ModerationItem** — mục trong hàng đợi kiểm duyệt: `id`, `space_uuid`,
  `target_type`, `target_id`, `source` (`pre_moderation` | `first_post` |
  `banned_word` | `reported` | `auto_hide`), `state` (`pending`/`approved`/
  `rejected`), `assignee_profile_uuid?`, `decided_by?`, `decided_at?`,
  `note?`, `created_at`. Chi tiết vòng đời: [04](04-kiem-duyet.md).

## 3.10 ProfileCache, SpaceCache, SpaceMemberCache

- **ProfileCache** — bản sao Profile (uuid, user_id, code, name, avatar,
  is_active) để hiển thị tác giả & phân giải mention/người nhận mà không gọi
  HipCore mỗi lần.
- **SpaceCache** — bản sao Space (uuid, name, code, is_public, is_active,
  creator_profile_uuid, space_type) để gắn nhãn cộng đồng & kiểm `is_public`.
- **SpaceMemberCache** — thành viên & vai trò trong Space (`space_uuid`,
  `profile_uuid`, `role`) để phân quyền đăng/kiểm duyệt mà không gọi HipCore.

Chi tiết & vòng đời cache: [05](05-cache-profile-space.md).

## 3.11 Ánh xạ "mục tiêu sử dụng" → cấu hình

| Mục tiêu | Cấu hình tiêu biểu |
|----------|--------------------|
| Forum truyền thống | Nhiều Board `kind=forum`, post lồng nhau, hậu kiểm |
| Q&A community | Board `kind=qna`, topic `type=question`, dùng `is_answer`/`is_resolved`, reaction up/down |
| Internal discussion | Space `is_public=false`, `post_policy=members`, ít kiểm duyệt |
| Technical support | Board `kind=support`, topic `status` mở/đóng, gán assignee qua ModerationItem/label |
| Gaming/social | Reaction phong phú, tag tự do, ghim, `moderation_mode=post` + báo cáo |
| Knowledge sharing | Board `announcement`, tìm kiếm toàn văn mạnh, ít trả lời |

## 3.12 Quy ước Markdown cho nội dung văn bản

Mọi **nội dung văn bản dài** trong ludiskus dùng **Markdown**, theo cùng một quy
ước lưu trữ và một pipeline render/sanitize chung:

| Thực thể | Trường nguồn (Markdown) | Trường render (HTML) |
|----------|-------------------------|----------------------|
| Board (mô tả) | `boards.description_md` | `boards.description_html` |
| Topic (thân chủ đề) | qua **Post đầu** → `posts.body_md` | `posts.body_html` |
| Post (bài & trả lời) | `posts.body_md` | `posts.body_html` |

> `title` của Topic và `name` của Tag/Board là **text trơn** (không Markdown) —
> phục vụ slug, FTS, `ts_headline` và hiển thị gọn.

**Pipeline (service xử lý, không tin FE):**

1. **Lưu `*_md`** đúng nội dung người dùng nhập (nguồn sự thật, để sửa lại sau).
2. **Render Markdown → HTML** ở backend (vd `goldmark`), bật **GFM** (bảng, danh
   sách việc, ~~gạch~~, autolink), code fence có **highlight**, trích dẫn, ảnh.
3. **Sanitize** HTML (vd `bluemonday`) theo allowlist — chống XSS; chỉ cho thẻ/
   thuộc tính an toàn; `rel="nofollow noopener"` cho link ngoài.
4. **Trích thực thể** sau khi parse: `@mention` ([03 §3.4](#34-post-bài--trả-lời)),
   ảnh/đính kèm (đối chiếu `attachment_ids`, viết lại `src` về URL MinIO hợp lệ —
   [07](07-dinh-kem.md)).
5. Khi **sửa**: render lại `*_html`, cập nhật `search_tsv` từ `*_md`
   ([06](06-tim-kiem.md)), ghi `edited_at`.

FE chỉ hiển thị `*_html` đã sanitize; trình soạn dùng chung một editor Markdown
có preview cho Board/Topic/Post — [11 §11.3](11-frontend.md).

