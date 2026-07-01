# 02 — Kiến trúc

## 2.1 Vị trí trong hippo

ludiskus là một service backend mới, đứng cạnh `hipcore`, `bg360-api`,
`luxport-api`, `lunoti-api`, `luprojet-api`…; dùng chung hạ tầng (Postgres,
Redis, MinIO) và auth (HipCore). Có hai loại client:

- **Người dùng cuối** gọi qua **app tm → BFF** (cookie httpOnly → Bearer token
  user) để đọc/đăng/tìm kiếm/kiểm duyệt — như BG360/luxport/lunoti.
- **Service khác** (tuỳ chọn) gọi `ludiskus-api` bằng **HipCore client-
  credentials** để đăng Topic/thông báo hệ thống trong một Space.

ludiskus là **producer** đối với lunoti: nó đẩy *event* (`ludiskus.topic.replied`,
`ludiskus.post.mentioned`, `ludiskus.post.reacted`…) sang `lunoti-api` để lunoti
phát thông báo — ludiskus **không** trực tiếp gửi email/push.

```
   Người dùng ─cookie httpOnly─▶ app tm ─/api/ludiskus/*─▶ bff ─Bearer─▶ ludiskus-api
                                                                            │
            ┌───────────────────────────────────────────────────────────────┤
            │                                                                 │
            ▼  JWKS verify + đọc Profile/Space/members (client-credentials)   │ presigned
      ┌───────────┐                                              ┌────────────▼────────┐
      │  hipcore  │◀──── GET /api/profiles* /api/spaces* ───────│   minio (S3)         │
      │   (IdP)   │      GET /api/spaces/{uuid}/members          │ ludiskus-attachments │
      └───────────┘                                              └─────────────────────┘
            ▲
            │
  ┌─────────┴──────┐   enqueue outbox   ┌──────────┐  SKIP LOCKED  ┌────────────────────┐
  │  ludiskus-api  │───────────────────▶│ postgres │◀──────────────│  ludiskus-worker   │
  │     (Go)       │  topics/posts/...  │ ludiskus │   outbox       │ (push lunoti +     │
  └───────┬────────┘                    │ + queue  │                │  sync cache +      │
          │ hot cache                   └──────────┘                │  index + counters) │
          ▼                                  ▲                       └─────────┬──────────┘
    ┌───────────┐  profile/space cache       │ profile_cache/space_cache       │ POST /api/v1/events
    │   redis   │                            │                                  ▼
    │  (cache)  │                            └──────────────────────────▶ ┌───────────┐
    └───────────┘                                                          │  lunoti   │
                                                                           └───────────┘
```

> **Không cần broker riêng.** Hàng đợi việc nền là bảng `outbox` trong Postgres,
> lấy việc bằng `FOR UPDATE SKIP LOCKED` (cùng mẫu `export_jobs` của lubo,
> `deliveries` của lunoti). Redis dùng cho **cache** Profile/Space/members (hot)
> và khoá nhẹ, không phải hàng đợi. Tìm kiếm dùng **Postgres FTS** (không cần
> service search riêng ở giai đoạn 1).

## 2.2 Các process

| Process | Vai trò | Nguồn |
|---------|---------|-------|
| `ludiskus-api` | REST API: CRUD board/topic/post/reaction/tag, tìm kiếm, đính kèm (cấp presigned), kiểm duyệt, ghi `outbox` | `ludiskus/backend` (Go) |
| `ludiskus-worker` | (1) **đẩy `outbox`** sang lunoti (event reply/mention/reaction/moderation) với retry/backoff; (2) **đồng bộ cache** Profile/Space/members từ HipCore; (3) cập nhật **đếm thống kê** (số post, last_activity) và **chỉ mục FTS** nếu tách rời; (4) dọn **nháp/đính kèm mồ côi** | cùng image, chọn vai trò qua `LUDISKUS_ROLE` |

Tách api/worker giống `bg360-api`/`bg360-worker`, `lunoti-api`/`lunoti-worker`:
cùng image Go, biến `LUDISKUS_ROLE=api|worker` quyết định binary chạy
(`cmd/api` hoặc `cmd/worker`).

## 2.3 Luồng xác thực

**A. Người dùng cuối (giống BG360/luxport/lunoti)**

1. `bff` đăng nhập qua HipCore (password grant), giữ token trong cookie httpOnly.
2. Request `/api/ludiskus/*` được bff chuyển tiếp tới `ludiskus-api`
   (`/api/v1/*`) kèm Bearer token; tự refresh khi 401.
3. `ludiskus-api` xác minh chữ ký token qua JWKS của HipCore
   (`/api/.well-known/jwks.json`), lấy `sub` (user id) và **Profile đang hoạt
   động** (claim profile của HipCore) — mọi hành động gắn `author_profile_uuid`.

→ Cùng origin với app tm nên **không cần CORS**.

**B. Service → ludiskus (client-credentials, tuỳ chọn)**

1. Service có OAuth client riêng ở HipCore (Passport `client_credentials`).
2. Lấy access token (`POST /oauth/token`, grant `client_credentials`), đính kèm
   khi gọi endpoint hệ thống (vd đăng topic thông báo trong Space).
3. `ludiskus-api` nhận diện token client (không có `sub` user) và ánh xạ sang
   `source_service`; chỉ chấp nhận hành động hệ thống được phép.

**C. ludiskus → HipCore (đọc Profile/Space) và ludiskus → lunoti (đẩy event)**

ludiskus **bản thân** là một OAuth client của HipCore
(`LUDISKUS_HIPCORE_CLIENT_ID/SECRET`, grant `client_credentials`) để gọi
`/api/profiles*`, `/api/spaces*` và `/api/spaces/{uuid}/members` — xem
[05](05-cache-profile-space.md). Để đẩy event sang lunoti, ludiskus là một
**OAuth client gửi event của lunoti** (`LUDISKUS_LUNOTI_CLIENT_ID/SECRET`) gọi
`POST /api/v1/events` với `source_service=ludiskus` — xem
[08](08-tich-hop-lunoti.md).

## 2.4 Luồng đăng bài & thông báo (high level)

```
[Người dùng đăng Post / trả lời]
        │  (Bearer user → ludiskus-api)
        ▼
  ludiskus-api:
   1. kiểm quyền (thành viên Space? board cho đăng? bị cấm?)
   2. áp kiểm duyệt (chế độ Space → publish ngay | vào hàng chờ duyệt) [04]
   3. lưu post (sanitize markdown, trích @mention, gắn attachment đã upload)
   4. cập nhật đếm (topic.reply_count, last_activity) — hoặc enqueue worker
   5. ghi `outbox`: event cho người theo dõi topic + người được mention
        │                                   │
        ▼                                   ▼
   201 trả về (hoặc 202 nếu chờ duyệt)   Postgres outbox (SKIP LOCKED)
                                            │
                                            ▼
                                    ludiskus-worker
                                            │ resolve người nhận từ cache
                                            └─ POST /api/v1/events → lunoti
                                                 (reply / mention / reaction)
```

Chi tiết kiểm duyệt: [04](04-kiem-duyet.md). Chi tiết resolve người nhận từ
cache: [05](05-cache-profile-space.md). Chi tiết event: [08](08-tich-hop-lunoti.md).

## 2.5 Layout mã nguồn dự kiến (theo mẫu lunoti/luxport)

```
ludiskus/
├── docs/                       # tài liệu này
└── backend/
    ├── cmd/
    │   ├── api/main.go          # khởi động REST API
    │   └── worker/main.go       # outbox→lunoti + sync cache + counters + cleanup
    ├── internal/
    │   ├── config/              # đọc env (DB DSN, Redis, MinIO, HipCore, lunoti)
    │   ├── auth/                # JWKS HipCore: middleware user + middleware service
    │   ├── domain/              # struct: Board, Topic, Post, Reaction, Tag, Attachment, Report, Subscription
    │   ├── service/             # logic: board, topic, post, reaction, search, moderation, subscription
    │   ├── repository/          # truy vấn Postgres (pgx)
    │   ├── search/              # FTS: build tsquery, rank, lọc
    │   ├── storage/             # MinIO: presign upload/download, kiểm tra mime/size
    │   ├── moderation/          # chế độ Space, lọc từ cấm, hàng đợi duyệt
    │   ├── notify/              # outbox + client lunoti (POST /events)
    │   ├── identity/            # client HipCore /api/profiles* /api/spaces* + cache (Redis + bảng)
    │   ├── transport/           # HTTP handlers + router
    │   └── platform/            # redis, minio, slog, healthz/readyz
    ├── db/migrations/           # 000x_*.up.sql / .down.sql
    ├── db/seeds/                # board mặc định, từ cấm mẫu, event_types đăng ký lunoti
    └── deploy/Dockerfile
```
