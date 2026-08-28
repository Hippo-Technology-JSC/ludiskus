# 07 — Đính kèm tập tin/hình ảnh

Dùng **MinIO** (S3) dùng chung của hippo — cùng pattern asset của BG360
(`bg360-assets`) và luprojet (`luprojet-attachments`). ludiskus dùng bucket
riêng `ludiskus-attachments`.

## 7.1 Luồng upload (presigned PUT)

Trình duyệt upload thẳng lên MinIO, backend không trung chuyển byte:

```
1. FE → ludiskus-api: POST /api/v1/attachments/presign
      { file_name, content_type, size_bytes, space_uuid }
2. api kiểm: quyền đăng trong Space, mime hợp lệ, size ≤ trần
      → tạo Attachment status=pending, object_key = spaces/{space}/{yyyy}/{mm}/{uuid}/{file}
      → trả presigned PUT URL (hết hạn ngắn) + attachment_id
3. FE → MinIO: PUT (trực tiếp lên presigned URL)
4. FE → ludiskus-api: khi đăng Post, gửi kèm attachment_ids
      → api xác nhận object tồn tại (HEAD), đổi status=attached, gắn post_id
```

> Backend giữ `LUDISKUS_S3_*` secret; presigned URL có TTL ngắn
> (`LUDISKUS_PRESIGN_TTL`, mặc định `5m`). FE chỉ thấy URL ký, không thấy key.

## 7.2 Tải xuống / hiển thị

- **Space riêng tư** → bucket **private**: phục vụ qua **presigned GET**
  (`GET /api/v1/attachments/{id}/url`) TTL ngắn; chỉ cấp cho người được xem Post.
- **Space công khai** → có thể đặt prefix công khai (anonymous download như
  `luxport-assets`/`lutriip-media`) để CDN/trình duyệt tải trực tiếp; ảnh nhúng
  trong `body_html` trỏ tới URL công khai.
- Quyết định public/private theo `space_cache.is_public` tại thời điểm cấp URL.

## 7.3 Kiểm tra & giới hạn

| Kiểm tra | Quy tắc |
|----------|---------|
| MIME | Danh sách trắng: ảnh (`image/png,jpeg,gif,webp`), tài liệu (`pdf`, `txt`, `zip`, office…) — cấu hình `LUDISKUS_ALLOWED_MIME` |
| Kích thước | ≤ `LUDISKUS_MAX_FILE_MB` (mặc định `25`); ảnh có trần riêng |
| Số đính kèm/Post | ≤ `LUDISKUS_MAX_ATTACHMENTS` (mặc định `10`) |
| Xác thực nội dung | Đối chiếu `content_type` khai báo với phần mở rộng & magic bytes khi HEAD (chống đổi đuôi) |
| Quét mã độc | (tương lai) hook ClamAV qua worker trước khi `attached` |

Ảnh: worker (tuỳ chọn) đọc kích thước (`width/height`), sinh **thumbnail** lưu
cùng prefix để hiển thị nhẹ.

## 7.4 Dọn rác (orphan cleanup)

- Attachment `pending` quá `LUDISKUS_ATTACH_TTL` (mặc định `24h`) mà chưa gắn
  Post → `ludiskus-worker` đánh dấu `orphaned` rồi xoá object MinIO + bản ghi.
- Khi Post/Topic bị `deleted` cứng → xoá object đính kèm tương ứng (hoặc giữ nếu
  còn tham chiếu chia sẻ).

## 7.5 Bảo mật

- Object key không đoán được (chứa uuid); không liệt kê bucket công khai.
- Validate `space_uuid` của attachment khớp Post khi gắn (chống gắn chéo Space).
- Trần dung lượng theo Space (tuỳ chọn `settings.storage_quota_mb`) để chống lạm
  dụng; worker tổng hợp dung lượng đã dùng.
# Chọn lại tệp từ Personal Files

Màn hình tạo topic có thể dùng shared `PersonalFilePicker` thay cho upload mới.
Frontend chỉ gửi selection token và idempotency key; không gửi object key/URL.
LuDiskus redeem token bằng OAuth service:

- source `ludiskus`: nhận native reference rồi copy object trong cùng bucket;
- source khác: stream từ URL TTL ngắn do Lufami resolve;
- mọi bản copy kiểm size/checksum, tạo attachment `pending`, sau đó complete
  selection;
- `personal_file_imports` giữ idempotency nên retry không tạo attachment đôi.
