## 2026-08-21

### Added
- Bộ tài liệu thiết kế + kế hoạch thực hiện **LuComment** — phân hệ bình luận dùng chung cho toàn hệ sinh thái, tại [docs/comment/](docs/comment/README.md) (17 tài liệu: mô hình miền, hợp đồng resolver S2S, cây/phân trang, phân quyền, kiểm duyệt, nội dung/đính kèm, thông báo, database `0003`–`0006`, API, frontend, tích hợp service, triển khai, lộ trình GĐ0–GĐ7, danh sách công việc chi tiết).
- Mục "Phân hệ" trong [docs/README.md](docs/README.md) trỏ tới bộ tài liệu trên.
- Triển khai LuComment GĐ0–GĐ7: migration `0003`–`0008`, resolver/registry/policy, API user/public/S2S/admin, cây bình luận, moderation, notification buffer, abuse flags, reconcile và score cache.
- Bộ component `tm/frontend/src/components/comment`, các trang hộp thư/permalink/admin và tích hợp thật vào Lumuse, Lukolek, Lukode và LuProjet.

### Changed
- Siết token S2S theo OAuth audience của registry, hỗ trợ quy ước HipCore Passport `sub=aud` cho client-credentials nhưng vẫn chặn token người dùng.
- BFF thêm public LuComment read-only, giới hạn theo IP và che `resource_id` trong access log.
- Worker nhận notification bằng lease `FOR UPDATE SKIP LOCKED`, ghi buffer cùng transaction tạo/duyệt và kéo like aggregate từ Lufami cho `sort=top`.

## 2026-07-29

### Changed
- Cut-over GĐ7 sang Interaction Platform của Lufami, bỏ API/domain/repository reaction cũ và dùng `InteractionBar` chung trong tm.
- Migration `0002_interaction_cutover` snapshot dữ liệu lịch sử sang outbox backfill rồi drop ngay bảng `reactions` và các cột `reaction_count`; không dual-write.
- Thêm contract S2S `interaction-context` cho topic/post/reply và worker backfill idempotent.

## 2026-07-16

### Changed
- Dùng hostname nội bộ `ludiskus-postgres` và `ludiskus-redis` cho runtime để loại bỏ xung đột DNS với dependency cùng tên trên network `hippo`.
- Chuẩn hóa compose mẫu của `ludiskus` vào external network chung `hippo`, đồng thời đổi mặc định `HIPCORE_URL` và `LUNOTI_API_URL` trong env example sang hostname nội bộ trên network chung.

## 2026-07-10

### Changed
- Thêm `.gitignore` ở root để bỏ qua các file local dạng `.env*` và `docker-compose*.yml`, đồng thời vẫn giữ các file mẫu như `.env.example` và `docker-compose.yml.example`.

## 2026-07-03

### Added
- Thêm `docker-compose.yml`, `.env.example` và `deploy.sh` riêng cho `ludiskus`, tách riêng dependency sang HipCore, lunoti, Redis và MinIO để chạy như một stack microservice độc lập.

## 2026-07-08

### Changed
- Chuyển [`docker-compose.yml`](docker-compose.yml) của `ludiskus` sang stack standalone đúng chuẩn các service khác: tự có `postgres` và `redis`, bỏ phụ thuộc `external` network `hippo`, và để `ludiskus-worker` build trực tiếp từ source thay vì chỉ tham chiếu image có sẵn.
- Chuẩn hóa [`.env.example`](.env.example) sang contract production tự chủ với `LUDISKUS_DB_USER`, `LUDISKUS_DB_PASSWORD`, `LUDISKUS_DB_NAME`, `LUDISKUS_DB_EXPOSE_PORT`, `HIPCORE_URL=http://host.docker.internal:8081`, `LUNOTI_API_URL=http://host.docker.internal:8092`, và endpoint object storage ngoài.
- Bổ sung các biến runtime đang được code thật sử dụng như `HIPCORE_JWKS_URL`, `HIPCORE_AUDIENCE`, `LUDISKUS_DB_MAX_CONNS`, `LUDISKUS_MAX_ATTACHMENTS`, `LUDISKUS_ALLOWED_MIME`, `LUDISKUS_OUTBOX_MAX_ATTEMPTS`, và `LUDISKUS_LOG_LEVEL` vào env/compose.
- Cập nhật [README.md](README.md) với hướng dẫn chạy standalone bằng `docker compose up -d --build`.
