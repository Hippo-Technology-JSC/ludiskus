# 10 — Backend API

Tiền tố nội bộ `/api/v1`. Người dùng gọi qua **BFF** (`/api/ludiskus/*` →
`/api/v1/*`, Bearer token user); một số endpoint hệ thống cho service khác dùng
**client-credentials**. Profile hiện hành lấy từ claim của token.

## 10.1 Sức khoẻ

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness (DB + Redis + MinIO + cấu hình HipCore/lunoti) |

## 10.2 Space-forum (cộng đồng)

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/spaces` | Các Space-forum người dùng thấy (thành viên + công khai) |
| GET | `/api/v1/spaces/{space}` | Thông tin forum của Space (cấu hình hiển thị, board) |
| POST | `/api/v1/spaces/{space}/enable` | Bật forum cho Space (owner/admin) |
| PATCH | `/api/v1/spaces/{space}/settings` | Cấu hình kiểm duyệt/post_policy/từ cấm (owner/admin) |
| GET/POST/PATCH/DELETE | `/api/v1/spaces/{space}/moderators` | Quản moderator |

## 10.3 Board

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/spaces/{space}/boards` | Danh sách board (cây) |
| POST | `/api/v1/spaces/{space}/boards` | Tạo board (owner/admin) |
| PATCH | `/api/v1/boards/{id}` | Sửa (tên, vị trí, khoá) |
| DELETE | `/api/v1/boards/{id}` | Xoá (chuyển topic hoặc cascade) |

## 10.4 Topic

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/boards/{id}/topics` | Liệt kê topic (`?sort=latest|top|unanswered`, ghim lên đầu, phân trang) |
| POST | `/api/v1/boards/{id}/topics` | Tạo topic (kèm post đầu + attachment_ids + tags) |
| GET | `/api/v1/topics/{id}` | Chi tiết topic + post đầu (tăng `view_count`) |
| PATCH | `/api/v1/topics/{id}` | Sửa tiêu đề/tag/type (tác giả hoặc moderator) |
| POST | `/api/v1/topics/{id}/lock` `/pin` `/move` `/resolve` | Hành động moderator/tác giả |
| DELETE | `/api/v1/topics/{id}` | Xoá mềm (tác giả/moderator) |

`POST /api/v1/boards/{id}/topics` — body:

```json
{
  "title": "Cách dùng context trong Go?",
  "type": "question",
  "body_md": "Mình đang… cc @binh",
  "tags": ["go", "concurrency"],
  "attachment_ids": ["…"]
}
```

Phản hồi `201` (publish ngay) hoặc `202` (`status=pending` nếu Space tiền kiểm).

## 10.5 Post / Interaction context

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/topics/{id}/posts` | Trả lời trong topic (phân trang, threaded theo `reply_to_id`) |
| POST | `/api/v1/topics/{id}/posts` | Trả lời (kèm attachment_ids; trích @mention) |
| PATCH | `/api/v1/posts/{id}` | Sửa (tác giả; ghi `edited_at`) |
| DELETE | `/api/v1/posts/{id}` | Xoá mềm |
| POST | `/api/v1/posts/{id}/answer` | Đánh dấu là câu trả lời (tác giả topic; Q&A) |

Like/reaction/bookmark/share được frontend gọi trực tiếp qua BFF Lufami. Contract
S2S cho Lufami:

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/s2s/interaction-context/{type}/{id}` | Metadata/quyền cho `topic`, `post`, `reply` |
| POST | `/api/v1/s2s/interaction-context:batch` | Resolve tối đa 100 refs |

## 10.6 Tìm kiếm

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/search` | Tìm topic/post (`?q=&space=&board=&tag=&author=&type=&from=&to=&sort=`) — [06](06-tim-kiem.md) |
| GET | `/api/v1/spaces/{space}/tags` | Gợi ý tag (trgm) |

## 10.7 Kiểm duyệt (moderator)

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/spaces/{space}/moderation/queue` | Hàng chờ (`?state=pending`) |
| POST | `/api/v1/moderation/{item}/approve` | Duyệt → publish (đẩy event) |
| POST | `/api/v1/moderation/{item}/reject` | Từ chối (kèm `note`) |
| GET | `/api/v1/spaces/{space}/reports` | Báo cáo mở |
| POST | `/api/v1/reports/{id}/resolve` \| `/dismiss` | Xử lý báo cáo |
| POST | `/api/v1/posts/{id}/report` `/topics/{id}/report` | Người dùng báo cáo |

## 10.8 Đính kèm

| Method | Path | Mô tả |
|--------|------|-------|
| POST | `/api/v1/attachments/presign` | Cấp presigned PUT URL ([07](07-dinh-kem.md)) |
| GET | `/api/v1/attachments/{id}/url` | Presigned GET (Space riêng tư) |
| DELETE | `/api/v1/attachments/{id}` | Gỡ đính kèm chưa publish |

## 10.9 Theo dõi (Subscription)

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/api/v1/subscriptions` | Mục đang theo dõi của Profile hiện hành |
| PUT | `/api/v1/subscriptions` | Theo dõi/bỏ/tắt (`{target_type, target_id, muted}`) |

## 10.10 Hệ thống (service → ludiskus, client-credentials)

| Method | Path | Mô tả |
|--------|------|-------|
| POST | `/api/v1/system/topics` | Tạo topic/thông báo hệ thống trong một Space (vd announcement tự động) |
| POST | `/api/v1/admin/cache/refresh` | Làm mới cache (`?type=profile|space|members&id=`) |

## 10.11 Quy ước phản hồi & lỗi

- Thành công: `200`/`201`/`202`/`204`, body JSON `{ data: … }`.
- Lỗi: `{ message, errors? }` (giống quy ước BFF/HipCore).
- Phân trang: `?page=&per_page=` + meta `{ total, page, per_page }`.
- Mã đặc thù: `202` khi bài vào hàng chờ duyệt; `403` khi không đủ quyền Space;
  `409` khi reaction trùng / slug trùng; `413` khi đính kèm vượt trần;
  `422 validation_error` khi mime/size/tag không hợp lệ.

## 10.12 Phân quyền

- **User token**: thao tác trong Space mình là thành viên (đọc Space công khai
  không cần là thành viên); chỉ sửa/xoá nội dung **của chính mình** trừ khi là
  moderator của Space đó.
- **Moderator/owner/admin** (theo `space_member_cache` + `space_moderators`):
  endpoint `/moderation/*`, lock/pin/move, cấu hình `/spaces/{space}/settings`.
- **Service token (client-credentials)**: chỉ `/api/v1/system/*` cho Space được
  phép; không truy cập dữ liệu người dùng khác.
