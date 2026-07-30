# 12 — Triển khai Docker

Bổ sung ludiskus vào stack hiện có. Theo mẫu `lunoti-api`/`lunoti-worker` và
`luxport-api`/`luxport-worker`: cùng một image, chọn vai trò qua `LUDISKUS_ROLE`.

## 12.1 Tạo database `ludiskus`

Bổ sung [infra/postgres/init-databases.sh](../../infra/postgres/init-databases.sh):

```sh
CREATE USER ludiskus WITH PASSWORD '${LUDISKUS_DB_PASSWORD}';
CREATE DATABASE ludiskus OWNER ludiskus;
```

> Script chỉ chạy khi volume Postgres khởi tạo **lần đầu**. Với stack đang chạy:
> `docker compose exec postgres psql -U postgres -c "CREATE DATABASE ludiskus
> OWNER ludiskus;"` (sau khi tạo user).

## 12.2 OAuth client cho ludiskus

ludiskus cần **hai** client-credentials (tạo sau khi hipcore/lunoti chạy):

```bash
# (a) Đọc Profile/Space của HipCore
docker compose exec hipcore php artisan passport:client --client
#   → LUDISKUS_HIPCORE_CLIENT_ID / _SECRET

# (b) Gửi event sang lunoti — đăng ký client này là source_service=ludiskus ở lunoti
docker compose exec hipcore php artisan passport:client --client
#   → LUDISKUS_LUNOTI_CLIENT_ID / _SECRET
```

## 12.3 Bucket MinIO

Thêm `ludiskus-attachments` vào `minio-init` (cùng dòng `mc mb` với
`luprojet-attachments`…). Space công khai có thể đặt anonymous download; Space
riêng tư phục vụ qua presigned (xem [07](07-dinh-kem.md)).

```sh
mc mb -p local/${LUDISKUS_S3_BUCKET:-ludiskus-attachments}
# (tuỳ chọn) mc anonymous set download local/${LUDISKUS_S3_BUCKET:-ludiskus-attachments}
```

## 12.4 Bổ sung `docker-compose.yml`

Hai service dùng chung image `ludiskus:dev`, vai trò qua `LUDISKUS_ROLE`:

```yaml
  ludiskus-api:
    build: { context: ./ludiskus/backend, target: development }
    image: ludiskus:dev
    command: ["go", "run", "./cmd/api"]
    volumes:
      - ./ludiskus/backend:/src
      - ludiskus_gomod:/go/pkg/mod
      - ludiskus_gocache:/root/.cache/go-build
    environment:
      - LUDISKUS_ROLE=api
      - LUDISKUS_HTTP_ADDR=:8080
      - LUDISKUS_DB_DSN=postgres://ludiskus:${LUDISKUS_DB_PASSWORD:-ludiskus}@postgres:5432/ludiskus?sslmode=disable
      - LUDISKUS_REDIS_URL=redis://redis:6379/6
      - HIPCORE_URL=http://hipcore
      - LUDISKUS_HIPCORE_CLIENT_ID=${LUDISKUS_HIPCORE_CLIENT_ID}
      - LUDISKUS_HIPCORE_CLIENT_SECRET=${LUDISKUS_HIPCORE_CLIENT_SECRET}
      - LUFAMI_API_URL=http://lufami-api:8080
      - LUNOTI_API_URL=http://lunoti-api:8080
      - LUDISKUS_LUNOTI_CLIENT_ID=${LUDISKUS_LUNOTI_CLIENT_ID}
      - LUDISKUS_LUNOTI_CLIENT_SECRET=${LUDISKUS_LUNOTI_CLIENT_SECRET}
      - LUDISKUS_S3_ENDPOINT=http://minio:9000
      - LUDISKUS_S3_PUBLIC_ENDPOINT=${LUDISKUS_S3_PUBLIC_ENDPOINT:-http://localhost:9000}
      - LUDISKUS_S3_ACCESS_KEY=${MINIO_ROOT_USER:-minio}
      - LUDISKUS_S3_SECRET_KEY=${MINIO_ROOT_PASSWORD:-minio12345}
      - LUDISKUS_S3_BUCKET=${LUDISKUS_S3_BUCKET:-ludiskus-attachments}
      - LUDISKUS_MAX_FILE_MB=${LUDISKUS_MAX_FILE_MB:-25}
      - LUDISKUS_PRESIGN_TTL=${LUDISKUS_PRESIGN_TTL:-5m}
    ports: ["${LUDISKUS_API_PORT:-8096}:8080"]
    networks: [hippo]
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
      minio:    { condition: service_started }
      hipcore:  { condition: service_started }
      lunoti-api: { condition: service_started }

  ludiskus-worker:
    image: ludiskus:dev
    command: ["go", "run", "./cmd/worker"]
    volumes: [ ./ludiskus/backend:/src, ludiskus_gomod:/go/pkg/mod, ludiskus_gocache:/root/.cache/go-build ]
    environment:
      - LUDISKUS_ROLE=worker
      - LUDISKUS_DB_DSN=postgres://ludiskus:${LUDISKUS_DB_PASSWORD:-ludiskus}@postgres:5432/ludiskus?sslmode=disable
      - LUDISKUS_REDIS_URL=redis://redis:6379/6
      - HIPCORE_URL=http://hipcore
      - LUDISKUS_HIPCORE_CLIENT_ID=${LUDISKUS_HIPCORE_CLIENT_ID}
      - LUDISKUS_HIPCORE_CLIENT_SECRET=${LUDISKUS_HIPCORE_CLIENT_SECRET}
      - LUFAMI_API_URL=http://lufami-api:8080
      - LUNOTI_API_URL=http://lunoti-api:8080
      - LUDISKUS_LUNOTI_CLIENT_ID=${LUDISKUS_LUNOTI_CLIENT_ID}
      - LUDISKUS_LUNOTI_CLIENT_SECRET=${LUDISKUS_LUNOTI_CLIENT_SECRET}
      - LUDISKUS_S3_ENDPOINT=http://minio:9000
      - LUDISKUS_S3_ACCESS_KEY=${MINIO_ROOT_USER:-minio}
      - LUDISKUS_S3_SECRET_KEY=${MINIO_ROOT_PASSWORD:-minio12345}
      - LUDISKUS_S3_BUCKET=${LUDISKUS_S3_BUCKET:-ludiskus-attachments}
      - LUDISKUS_PROFILE_SYNC_INTERVAL=${LUDISKUS_PROFILE_SYNC_INTERVAL:-6h}
      - LUDISKUS_SPACE_SYNC_INTERVAL=${LUDISKUS_SPACE_SYNC_INTERVAL:-6h}
      - LUDISKUS_CACHE_TTL=${LUDISKUS_CACHE_TTL:-1h}
      - LUDISKUS_ATTACH_TTL=${LUDISKUS_ATTACH_TTL:-24h}
    networks: [hippo]
    depends_on:
      ludiskus-api: { condition: service_started }
```

> **Cổng host `8096`** (đã dùng: hipcore 8082, bg360 8090, luxport 8091, lunoti
> 8092, luprojet 8093, lutriip 8094, luwalet 8095). **Redis DB index `/6`** (đã
> dùng: lunoti `/3`, luwalet `/5`). Volume `ludiskus_gomod`/`ludiskus_gocache`
> cache build Go.

## 12.5 Nối frontend qua BFF

Thêm biến cho service `bff` (docker-compose.yml):

```yaml
      - LUDISKUS_API_URL=http://ludiskus-api:8080
```

và proxy `/api/ludiskus/*` trong [tm/bff/src/index.ts](../../tm/bff/src/index.ts)
([11 §11.1](11-frontend.md)).

## 12.6 Biến môi trường mới (`.env.example`)

| Biến | Mặc định | Mô tả |
|------|----------|-------|
| `LUDISKUS_DB_PASSWORD` | `ludiskus` | Mật khẩu role Postgres `ludiskus` |
| `LUDISKUS_API_PORT` | `8096` | Cổng host cho `ludiskus-api` |
| `LUDISKUS_HIPCORE_CLIENT_ID/SECRET` | — | OAuth client đọc `/api/profiles*`, `/api/spaces*` |
| `LUFAMI_API_URL` | `http://lufami-api:8080` | Interaction Platform và điểm hipt |
| `LUDISKUS_LUNOTI_CLIENT_ID/SECRET` | — | OAuth client gửi event sang lunoti |
| `LUDISKUS_S3_BUCKET` | `ludiskus-attachments` | Bucket đính kèm |
| `LUDISKUS_S3_PUBLIC_ENDPOINT` | `http://localhost:9000` | Endpoint MinIO trình duyệt truy cập (ký URL) |
| `LUDISKUS_MAX_FILE_MB` | `25` | Trần kích thước đính kèm |
| `LUDISKUS_PRESIGN_TTL` | `5m` | Hạn presigned URL |
| `LUDISKUS_CACHE_TTL` | `1h` | TTL cache Profile/Space/members trong Redis |
| `LUDISKUS_PROFILE_SYNC_INTERVAL` / `_SPACE_SYNC_INTERVAL` | `6h` | Chu kỳ full-sync cache |
| `LUDISKUS_ATTACH_TTL` | `24h` | Hạn dọn đính kèm mồ côi |

## 12.7 Production

Lặp lại cấu hình tương ứng trong `docker-compose.prod.yml` (image build sẵn,
mount read-only, `COOKIE_SECURE` do bff xử lý). Worker và api dùng chung image.
Bí mật (DB, secret client HipCore/lunoti, MinIO) đặt qua biến môi trường triển
khai, không commit.

## 12.8 Lịch nội bộ

Full-sync cache, dọn đính kèm mồ côi, backfill interaction lịch sử và đẩy outbox chạy **nội bộ
trong `ludiskus-worker`** (ticker Go + khoá Redis nhẹ) — không cần service cron
riêng.
