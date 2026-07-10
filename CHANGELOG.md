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
