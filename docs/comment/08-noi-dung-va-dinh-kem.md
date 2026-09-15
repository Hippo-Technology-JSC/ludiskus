# 08 — Nội dung, @mention & đính kèm

## 8.1 Đường đi của một thân bình luận

```
body_md (người dùng gõ)
   │ 1. chuẩn hoá: NFC, bỏ ký tự điều khiển, trim, gộp > 2 dòng trống, trần độ dài
   │ 2. body_hash = sha256(lower(bỏ khoảng trắng thừa))       → chặn trùng (§6.6)
   │ 3. lọc từ cấm trên bản đã bỏ markup                       → [07 §7.3]
   │ 4. đếm liên kết ngoài                                     → policy.max_links
   │ 5. render theo policy.markdown  →  goldmark  →  bluemonday → body_html
   │ 6. trích @mention → giải qua profile_cache → comment_mentions
   ▼
lưu body_md + body_html + body_hash + search_tsv (trigger)
```

`body_html` được **lưu** (không render lúc đọc): đọc nhiều hơn viết rất nhiều lần, và render
lúc đọc làm mọi trang bình luận trả tiền cho markdown.

## 8.2 Ba mức markdown

`internal/markdown` hiện có **một** `Renderer` (GFM đầy đủ + `bluemonday.UGCPolicy`). Bổ sung
hai chế độ, giữ nguyên hành vi cũ cho forum:

| `policy.markdown` | Cho phép | Chặn |
|-------------------|----------|------|
| `plain` | Chỉ xuống dòng + tự động nhận diện URL thành liên kết (`rel=nofollow noopener`, `target=_blank`) | Toàn bộ markup: `*`, `#`, `>`, `` ` ``, bảng, ảnh, HTML thô — escape hết |
| `basic` (**mặc định**) | `**đậm**`, `*nghiêng*`, `` `mã` ``, khối mã, `> trích`, danh sách, liên kết, `~~gạch~~` | Tiêu đề (`#`), ảnh (`![]`), bảng, HTML thô, iframe, footnote |
| `rich` | Như bài diễn đàn hiện nay (GFM đầy đủ, ảnh, bảng) | HTML thô nguy hiểm (bluemonday) |

Cài đặt: ba `*bluemonday.Policy` dựng sẵn lúc khởi tạo (`policyPlain`, `policyBasic`,
`policyRich`) + hai `goldmark.Markdown` (một GFM, một chỉ bật extension cần cho `basic`);
`Render(mode, src)` chọn cặp tương ứng.

Luật bất biến của `basic`/`plain`: **không thẻ `<img>`, không `<table>`, không `<h1..h6>`,
không `<iframe>`, không `style`/`class` do người dùng đặt**. Ảnh trong bình luận đi qua
**đính kèm** (§8.5), không qua markdown — nhờ vậy mọi ảnh đều có bản ghi, có chủ, dọn được.

Kiểm chứng bắt buộc (unit test): 20 payload XSS kinh điển (`<script>`, `javascript:` trong
`[x](javascript:…)`, `onerror` trong HTML thô, `<svg onload>`, `data:text/html`, đóng thẻ
lệch, unicode escape) × 3 chế độ ⇒ đầu ra không chứa `<script`, không chứa `on*=`, không có
scheme ngoài `http/https/mailto`.

## 8.3 Giới hạn

| Giới hạn | Mặc định | Nguồn |
|----------|----------|-------|
| Độ dài `body_md` | 4000 ký tự (rune) | `policy.max_length`, trần cứng 20000 |
| Độ dài tối thiểu | 2 | `policy.min_length` |
| Số liên kết ngoài | 3 | `policy.max_links`; Profile < 24h ⇒ 0 (§6.6) |
| Số @mention | 10 | `policy.mentions.max_per_comment` |
| Số đính kèm | 3 | `policy.attachments.max_per_comment`, và `LUDISKUS_MAX_ATTACHMENTS` là trần cứng |
| Số lần sửa | 10 | `LUDISKUS_COMMENT_MAX_REVISIONS` |
| Dòng trống liên tiếp | 2 | chuẩn hoá, không lỗi |

Vượt trần ⇒ `422 VALIDATION_ERROR` với `errors: { body: "…" }` — thông điệp tiếng Việt, nói rõ
số hiện tại và trần (không phải "invalid input").

## 8.4 @mention

Tái dùng `Renderer.MentionsInMode(mode, src)` — trích theo **cây cú pháp của đúng chế độ
markdown sẽ dùng để render**, không phải regex trên văn bản thô. Nhờ vậy dán một đoạn log hay
cấu hình có `@ai-đó` trong khối code không báo tin cho người ta. (`markdown.Mentions` dạng regex
vẫn còn, nay chỉ dùng cho chế độ `plain` vốn không có parser.)

Giải handle → Profile:

1. `ident.ProfileByCode(handle)` (cache Redis + `profile_cache`).
2. Không thấy và handle giống uuid ⇒ `ident.Profile(handle)`.
3. Vẫn không thấy ⇒ **bỏ qua im lặng** (không lỗi, không thông báo). Không bao giờ gọi HipCore
   theo từng mention trong hot-path.

Áp `policy.mentions.scope`:

| `scope` | Profile được mention |
|---------|---------------------|
| `space` | Phải là thành viên `target.space_uuid` (`space_member_cache`). Target không có `space_uuid` ⇒ suy biến thành `participants` |
| `participants` | Phải có hàng trong `comment_participants` của Target, hoặc là chủ nội dung |
| `none` | Không mention ai; `@…` chỉ là văn bản |

### Hiển thị: họ tên, và CHỈ khi thật sự nhắc được

`body_html` render `@code` thành chip `<span class="mention" data-mention="code">Họ Tên</span>`
— người đọc thấy **họ tên trần**, không thấy code và không có dấu `@` đứng trước. Cả ba chế độ
markdown (`plain`/`basic`/`rich`) đều dựng chip; LuComment mặc định chạy `basic`.

> **Sửa so với bản thiết kế đầu:** trước đây mục này ghi Profile ngoài scope "vẫn render tên
> (đẹp)". Làm vậy là **rò tên người ngoài context**: bất kỳ ai gõ `@code` của bất kỳ Profile nào
> trong toàn hệ thống cũng khiến họ tên thật của người đó hiện ra cho mọi người đọc luồng ấy —
> đúng thứ mà LuComment phải tránh, vì nó gắn lên tài nguyên của nhiều service khác nhau.
> Nay Profile **ngoài scope giữ nguyên `@code`**, đúng chữ tác giả gõ. Chỉ người thật sự vào
> `comment_mentions` mới được hiện tên, nên **hiện tên ⟺ được báo tin**, không có nửa vời.

Cả nhãn hiển thị lẫn hàng `comment_mentions` đều đi qua **một hàm duy nhất**
(`Service.commentMentionTarget`), nên hai thứ không thể lệch nhau. Trần
`max_per_comment` áp cho cả hai: mention thứ 11 không được báo tin thì cũng không được hiện tên.

Bình luận do **service** viết (đường S2S) **không** phân giải tên: đường đó cố ý không ghi
`comment_mentions` và không báo tin cho ai, nên hiện họ tên ở đó là hứa một thông báo không
bao giờ đến.

Gợi ý mention ở frontend: `GET /comments/r/{ref}/mention-suggest?q=` trả ≤ 10 Profile **đúng
theo scope** — không bao giờ để frontend tự đoán danh sách. Danh sách ứng viên dựng theo đúng
phạm vi mà `commentMentionTarget` cho phép:

- `scope = "none"` → trả **rỗng**. Gợi ý một cái tên mà hệ thống sẽ lặng lẽ vứt đi là mời người
  dùng làm một việc vô nghĩa.
- nhánh `participants` → gồm cả **chủ tài nguyên**, kể cả khi họ chưa từng bình luận (vì họ
  nhắc được). Thiếu họ là có người nhắc được mà không ai tìm thấy.
- Profile đã ngừng hoạt động **không** được gợi ý (giống `ForumMembers` của diễn đàn).
- Nhánh `scope = "space"` lọc và cắt **ngay trong SQL** (`Repo.SearchSpaceMemberProfiles`),
  dùng chung với `ForumMembers` của diễn đàn — xem [05 §5.5](../05-cache-profile-space.md).
  Space 5.004 thành viên: 1,356 s → 24 ms.

## 8.5 Đính kèm

Tái dùng toàn bộ đường đã có ([07](../07-dinh-kem.md)): presign PUT → PUT thẳng MinIO → gửi
`attachmentIds` khi tạo bình luận → `HEAD` xác nhận → `status='attached'`.

Thay đổi duy nhất ở backend:

```sql
ALTER TABLE attachments ALTER COLUMN space_uuid DROP NOT NULL;   -- Target có thể không thuộc Space
ALTER TABLE attachments ADD COLUMN comment_id uuid REFERENCES comments(id) ON DELETE CASCADE;
ALTER TABLE attachments ADD CONSTRAINT attachments_owner_one
  CHECK (num_nonnulls(post_id, comment_id) <= 1);
```

và `PresignInput` nhận thêm `resourceRef` (thay cho `spaceUuid` khi đính kèm cho bình luận) để
kiểm quyền theo Target thay vì theo Space.

| Luật | Chi tiết |
|------|----------|
| Quyền presign | Phải qua `ensureCommentable` cho Target đó (không phải chỉ "là thành viên Space") |
| Loại tệp | `policy.attachments.images_only=true` ⇒ chỉ `image/*` trong `LUDISKUS_ALLOWED_MIME` |
| Kích thước | `LUDISKUS_MAX_FILE_MB` (25MB) — không đổi |
| Gắn chéo | `attachment.space_uuid` phải khớp `target.space_uuid`; Target không có Space ⇒ `space_uuid = NULL` (cột đã nới) và `object_key = comments/{target_id}/{yyyy}/{mm}/{uuid}/{file}` |
| Dọn mồ côi | Không đổi: `pending` quá `LUDISKUS_ATTACH_TTL` ⇒ `orphaned` ⇒ xoá object |
| Hiển thị | Target `visibility='public'` ⇒ URL công khai; còn lại ⇒ presigned GET TTL ngắn qua `GET /attachments/{id}/url` (đã có, chỉ thêm nhánh kiểm quyền theo Target) |

## 8.6 Sửa & lịch sử

```
PATCH /comments/items/{id}   { "bodyMd": "…", "attachmentIds": [...] }
```

- Chỉ tác giả, chỉ khi `now - created_at <= policy.edit_window_minutes` (mặc định 15) **và**
  `status = 'published'`. Ngoài cửa sổ ⇒ `403 EDIT_WINDOW_CLOSED`.
- Bình luận `pending` sửa được **không** giới hạn thời gian (chưa ai thấy) nhưng vẫn ở
  `pending` và **reset** quyết định kiểm duyệt (từ cấm chạy lại).
- Mỗi lần sửa: `INSERT comment_revisions(body_md cũ)`, `edited_at = now()`, `edit_count += 1`,
  render lại, trích lại mention (**chỉ thêm** người mới; người bị bỏ khỏi nội dung **không**
  bị xoá khỏi `comment_participants`).
- Sửa **không** phát thông báo mới cho người cũ; mention **mới thêm** thì có.
- Giao diện: nhãn "đã sửa · 5 phút trước", bấm mở lịch sử (tác giả và moderator).

## 8.7 Bình luận hệ thống (service viết)

`POST /api/v1/s2s/comments/items` với `author_kind='service'`:

- Bắt buộc `sourceService` = service của token; `authorProfileUuid` **tuỳ chọn** (nếu có, hiện
  như "X (thay mặt hệ thống)").
- Luôn `published`, bỏ qua kiểm duyệt và rate limit, `markdown_mode='basic'`.
- **Không** phát thông báo `comment.created` cho chủ nội dung (chống service tự spam chủ nội
  dung), nhưng **có** phát `comment.replied` nếu là trả lời một bình luận của người thật.
- Bắt buộc `idempotencyKey` — service tự sinh theo nghiệp vụ (`"order:123:shipped"`); gọi lại
  trả `200` với bình luận cũ, không tạo bản thứ hai.
- Ứng dụng thật: `lushoop` trả lời tự động "đơn đã giao"; `lugame` đăng "màn chơi đã cập nhật
  phiên bản 2"; `lurp` đăng kết quả một bước workflow.
