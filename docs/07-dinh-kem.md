# 07 — Đính kèm tập tin/hình ảnh

Ludiskus dùng bucket riêng `ludiskus-attachments` trên **RustFS dùng chung**, chỉ truy cập endpoint nội bộ từ backend. Tất cả upload/get file trong tm đi qua API và `frontend/src/components/files/FilePicker.tsx`.

## 7.1 Upload qua API

1. FilePicker gọi adapter `ludiskusAttachmentAdapter` cho topic/reply hoặc comment dùng chung. Adapter gọi `POST /api/v1/attachments/uploads` với `{spaceUuid, fileName, contentType, sizeBytes}`; comment gửi `resourceRef` thay cho `spaceUuid`.
2. Backend kiểm tra quyền đăng/đính kèm, MIME allowlist và kích thước; tạo slot pending với object key ngẫu nhiên. `uploadUrl` trong response là đường dẫn API cùng origin `/api/ludiskus/attachments/{id}/upload`.
3. Adapter gửi bytes bằng `POST /api/v1/attachments/{id}/upload`, Content-Type khớp slot. Backend kiểm tra lại chủ sở hữu/quyền hiện tại, số byte thực tế, MIME sniffing, JSON hợp lệ và kích thước ảnh tối đa 40 triệu pixel trước khi ghi S3; lưu SHA-256 và finalized_at. Tệp thiếu bytes, dư bytes hay giả MIME bị từ chối. PUT S3 có điều kiện tránh ghi đè slot đã upload.
4. Khi đăng topic/reply/comment, frontend gửi `attachmentIds`; server kiểm tra phạm vi và claim pending attachment. Comment chỉ nhận tệp đã hoàn tất của chính profile và đúng resource target.

Ảnh editor dùng `ludiskusEditorAssets`: `POST /api/v1/editor-assets/uploads` với `Idempotency-Key`, upload bytes qua cùng route attachment, rồi `POST /api/v1/editor-assets/{id}/complete`. Kết quả chứa `contentPath` cùng origin và Markdown ảnh. Clipboard/kéo thả ảnh đi qua cùng FilePicker. Form chặn gửi/lưu trong lúc xử lý tệp, request nhận AbortSignal và bị huỷ khi picker unmount.

`LUDISKUS_MAX_FILE_MB` mặc định 25 MiB/tệp; ảnh editor tối đa 5 MiB. Slot hết hạn theo `LUDISKUS_ATTACH_TTL` (mặc định 24 giờ). Nginx/BFF dành riêng route upload tối đa 32 MiB và timeout upstream 65/60 giây. Đổi giới hạn backend cao hơn cần điều chỉnh các giới hạn này tương ứng.

## 7.2 Đọc tệp qua API

`GET /api/v1/attachments/{id}/url` trả `/api/ludiskus/attachments/{id}/content`. Trường `url` trong danh sách topic/post/comment cũng dùng đường dẫn API.

`GET/HEAD /api/v1/attachments/{id}/content` kiểm quyền xem Space/board/topic/post hoặc resource comment, rồi stream bytes từ RustFS; hỗ trợ Range, ETag và conditional GET. Response có cache private cần revalidate, Content-Disposition, nosniff và CSP sandbox. Không redirect hay phát hành presigned/public S3 URL.

Comment công khai trả đường dẫn `/api/public/ludiskus/comments/attachments/{id}/content`. API `/api/v1/public/comments/attachments/{id}/content` chỉ phục vụ comment đã published, resource active/public, policy cho public read và thread không hidden; kiểm tra lại mỗi lần đọc. BFF không chuyển cookie/token/identity vào đường này. Comment đã xoá, pending hoặc resource chuyển riêng tư không được đọc công khai.

## 7.3 Chọn từ Tệp của tôi

Ảnh editor giữ endpoint `/editor-assets/import`; tệp topic/reply dùng `POST /attachments/import` với `{spaceUuid, purpose, selectionToken}` và `Idempotency-Key`. Backend kiểm quyền, redeem token với lufami, copy/import bytes và xác nhận checksum/kích thước/MIME; frontend chỉ nhận metadata/path API, không tải S3 trực tiếp. Purpose tệp là `topic-attachment` hoặc `reply-attachment`.

## 7.4 Cấu hình và triển khai

Tái sử dụng `LUDISKUS_S3_ENDPOINT`, `LUDISKUS_S3_ACCESS_KEY`, `LUDISKUS_S3_SECRET_KEY`, `LUDISKUS_S3_BUCKET`. Endpoint mẫu là `http://rustfs-tm:9000`, client path-style; API và worker cùng tham gia `SHARED_DOCKER_NETWORK` (mặc định hippo). Không cần S3 public endpoint hay presign TTL.

Compose mẫu không dựng storage container, init container hay volume MinIO/RustFS riêng. Backend khởi tạo bucket nếu thiếu, credentials phải có quyền tương ứng. Bucket giữ private.

Triển khai đồng thời backend ludiskus, tm BFF và frontend vì API presign cũ đã được thay thế. Nếu chuyển từ MinIO cũ, cần sao chép dữ liệu sang bucket RustFS với object keys giữ nguyên; đổi endpoint không tự di chuyển file. Chưa có thay đổi dữ liệu production trong lần cập nhật này.

## 7.5 Kiểm chứng

Go unit/HTTP fixtures kiểm tra signed path-style S3, bytes/MIME/dung lượng, JSON, ảnh, chống ghi đè và streaming GET/HEAD/Range/304. Frontend kiểm tra FilePicker, adapters, comment MIME và huỷ; BFF kiểm tra upload, đọc binary, auth, giới hạn chunked và public media không nhận identity.

Acceptance PostgreSQL/storage thật là opt-in: `LUDISKUS_TEST_DSN` phải là database riêng hậu tố `_test`; giữ cờ hiện có `LUDISKUS_TEST_MINIO=1` để bật kiểm thử S3 thật với endpoint/credentials đã cấu hình. Không dùng database production. Khi chưa có các cấu hình này, acceptance integration được skip.
