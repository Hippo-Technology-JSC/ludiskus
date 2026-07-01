# 04 — Kiểm duyệt nội dung

Kiểm duyệt **cấu hình theo từng Space** (trên `space_forums.moderation_mode` —
[03 §3.1](03-mo-hinh-mien.md)). Mục tiêu: mỗi cộng đồng tự chọn mức độ kiểm soát
phù hợp (forum mở, Q&A, hỗ trợ nội bộ, gaming…).

## 4.1 Bốn chế độ kiểm duyệt

| `moderation_mode` | Hành vi khi đăng Topic/Post |
|-------------------|------------------------------|
| `none` | Đăng là hiển thị ngay; chỉ xử lý hậu kỳ qua **báo cáo** |
| `post` (hậu kiểm) | Hiển thị ngay, nhưng moderator có thể gỡ/ẩn; báo cáo + lọc từ cấm vẫn chạy |
| `pre` (tiền kiểm) | **Mọi** bài vào hàng chờ (`status=pending`), không hiển thị công khai cho tới khi duyệt |
| `first_post` | Chỉ **bài đầu tiên** của một thành viên trong Space vào hàng chờ; sau khi được duyệt một lần, các bài sau đăng thẳng |

Bài của **moderator/admin/owner** Space luôn bỏ qua hàng chờ (trust staff).

## 4.2 Lọc từ cấm (banned words)

- `space_forums.banned_words` (text[]) — so khớp không phân biệt hoa/thường và
  dấu (dùng `unaccent`, xem [06](06-tim-kiem.md)).
- Khi đăng/sửa: nếu trùng từ cấm →
  - chế độ `none`/`post`: vẫn lưu nhưng tạo **ModelationItem** `source=banned_word`
    (chờ moderator xem) — hoặc tự ẩn nếu cấu hình;
  - chế độ `pre`/`first_post`: rơi vào hàng chờ như thường, đánh dấu lý do.
- Danh sách mặc định seed ở `db/seeds/banned_words.json`, Space tự bổ sung.

## 4.3 Báo cáo & tự ẩn

1. Người dùng gửi **Report** (lý do `spam`/`abuse`/`offtopic`/`other`).
2. Khi số report mở của một target đạt `report_auto_hide_threshold` (>0):
   ludiskus đặt target `status=hidden` và tạo **ModerationItem**
   `source=auto_hide` để moderator quyết định cuối cùng.
3. Moderator xử lý Report → `resolved` (kèm hành động) hoặc `dismissed`.

## 4.4 Hàng đợi & vòng đời duyệt

`ModerationItem` là đơn vị công việc của moderator.

```
[đăng/sửa]──┐
            ├─ pre / first_post ───────▶ tạo item source=pre_moderation|first_post (target.status=pending)
[từ cấm]────┘                       │
[auto-hide]─── đạt ngưỡng report ───┘  (target.status=hidden)
                                       │
                          moderator xem hàng chờ
                                       │
                    ┌──────────────────┼─────────────────┐
                    ▼                  ▼                  ▼
                approve            reject             escalate
            target=published   target=rejected     gán assignee
            (đẩy event nếu       (ẩn, báo tác giả)
             là bài mới)
```

- **approve**: chuyển `status=published`; nếu là Post/Topic mới thì **lúc này**
  mới ghi `outbox` để lunoti phát thông báo (tránh thông báo bài chưa duyệt).
- **reject**: `status=rejected`/`hidden`; đẩy event `moderation.rejected` cho
  tác giả (qua lunoti) kèm lý do.
- Mỗi quyết định ghi `decided_by`, `decided_at`, `note` để truy vết.

## 4.5 Quyền kiểm duyệt

- Người có vai trò `owner`/`admin` trong Space (theo SpaceMemberCache —
  [05](05-cache-profile-space.md)) là moderator mặc định.
- Owner có thể phong thêm `moderator` cho Profile khác (lưu trong ludiskus, bảng
  `space_moderators(space_uuid, profile_uuid)` — bổ sung ngoài role HipCore).
- Endpoint kiểm duyệt ([10 §10.7](10-backend-api.md)) yêu cầu quyền moderator
  của **đúng Space** chứa target.

## 4.6 Tương tác với thông báo

- Bài **chờ duyệt** không sinh thông báo reply/mention cho tới khi `approve`.
- Khi `approve`/`reject`, đẩy event tương ứng cho **tác giả** (và người liên
  quan), để lunoti báo "bài của bạn đã được đăng / bị từ chối".
- Action của moderator (gỡ/khoá) có thể (tuỳ chọn) thông báo cho người theo dõi
  Topic.

## 4.7 Mặc định an toàn

- Space mới bật forum: `moderation_mode=first_post`, `post_policy=members`,
  `report_auto_hide_threshold=5` (cân bằng giữa mở và chống spam).
- Space `is_public=false` (nội bộ): có thể đặt `none`/`post` vì người dùng đã
  được kiểm soát ở tầng thành viên.
