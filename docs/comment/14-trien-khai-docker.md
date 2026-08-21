# 14 — Triển khai & vận hành

**Không** thêm container, **không** thêm volume, **không** thêm database, **không** thêm bucket.
Chỉ thêm biến môi trường cho `ludiskus-api` / `ludiskus-worker`, một passthrough ở BFF, và bốn
ticker trong worker.

## 14.1 Biến môi trường mới

Thêm vào [`internal/config/config.go`](../../backend/internal/config/config.go) theo đúng mẫu
`get()`/`time.ParseDuration` đang dùng:

```go
CommentEnabled          bool          // LUDISKUS_COMMENT_ENABLED              true
CommentResolveTimeout   time.Duration // LUDISKUS_COMMENT_RESOLVE_TIMEOUT      3s
CommentTargetTTL        time.Duration // LUDISKUS_COMMENT_TARGET_TTL           6h
CommentSummaryTTL       time.Duration // LUDISKUS_COMMENT_SUMMARY_TTL          60s
CommentNotifyDebounce   time.Duration // LUDISKUS_COMMENT_NOTIFY_DEBOUNCE      5m
CommentBatchMax         int           // LUDISKUS_COMMENT_BATCH_MAX            100
CommentMaxRevisions     int           // LUDISKUS_COMMENT_MAX_REVISIONS        10
CommentNewProfileHours  int           // LUDISKUS_COMMENT_NEW_PROFILE_HOURS    24
CommentPublicRPM        int           // LUDISKUS_COMMENT_PUBLIC_RPM           120
CommentPollInterval     time.Duration // LUDISKUS_COMMENT_POLL_INTERVAL        60s  (trả cho FE)
CommentAuditRetention   int           // LUDISKUS_COMMENT_AUDIT_RETENTION_DAYS 365
CommentThumbHosts       []string      // LUDISKUS_COMMENT_THUMB_HOSTS          (rỗng = chỉ host nội bộ)
CommentServiceClients   string        // LUDISKUS_COMMENT_SERVICE_CLIENTS      "lumuse=id1,lukolek=id2"
```

Không có biến nào chứa `base_url` của service khác — chỗ đó là `comment_services.base_url`
(thêm service không phải deploy lại, [04 §4.1](04-hop-dong-resource.md)).

`LUDISKUS_COMMENT_SERVICE_CLIENTS` được đọc **lúc khởi động** và `UPSERT` vào
`comment_services.oauth_client_id` (idempotent). Đây là cách nạp client id theo môi trường mà
không nhồi vào migration ([10 §10.3](10-database.md)).

`LUDISKUS_COMMENT_ENABLED=false` ⇒ toàn bộ route bình luận trả `404` và worker bỏ 4 ticker mới;
mọi thứ khác của `ludiskus` chạy bình thường. Đây là công tắc gỡ nhanh khi có sự cố.

## 14.2 Compose

`ludiskus/docker-compose.yml.example` và `docker-compose.yml` gốc của hippo: thêm đúng các
biến §14.1 vào **cả hai** service `ludiskus-api` và `ludiskus-worker` (worker cần
`CommentNotifyDebounce`, `CommentTargetTTL`, `CommentMaxRevisions`, `CommentAuditRetention`).

Biến đã có và **phải** có giá trị để phân hệ chạy đủ:

| Biến | Vì sao cần cho bình luận |
|------|--------------------------|
| `LUDISKUS_HIPCORE_CLIENT_ID/SECRET` | Resolver gọi service khác **bằng chính credential này** |
| `LUNOTI_API_URL` + `LUDISKUS_LUNOTI_CLIENT_ID/SECRET` | Thông báo. Thiếu ⇒ bình luận vẫn chạy, không có thông báo |
| `LUFAMI_API_URL` | Đồng bộ resource `ludiskus:comment:{id}` sang Interaction Platform. Thiếu ⇒ mất like/reaction trên bình luận, phần còn lại chạy đủ |
| `GATEWAY_SIGNING_SECRET` | Đường tin-cậy-gateway của BFF |
| `LUDISKUS_S3_*` | Đính kèm. Thiếu ⇒ `capabilities.canAttach=false`, không lỗi |

## 14.3 Cảnh báo `HIPCORE_AUDIENCE`

`.env.example` để trống biến này và **phải giữ trống** cho tới khi nhóm S2S được sửa theo
[06 §6.7](06-phan-quyen.md). Lý do: `auth.parse` thêm `jwt.WithAudience(cfg.HipcoreAudience)`
cho **mọi** token, nhưng HipCore đặt `aud` = **client id của bên gọi**
(`AccessToken::convertToJWT` → `permittedFor($client->getIdentifier())`). Bật biến này với một
giá trị cố định ⇒ mọi token S2S từ service khác bị `401`, và biểu hiện là "bình luận không hiện
ở service X" — rất khó lần ra.

Sau khi §6.7 xong: nhóm người dùng vẫn dùng `parse` có audience; nhóm S2S dùng `parseService`
**không** ràng buộc audience rồi tự kiểm `aud` theo `comment_services`.

## 14.4 BFF — passthrough công khai (điều kiện chặn của GĐ5)

`tm/bff/src/config.ts` đã có route `ludiskus` (`prefix: "ludiskus"`, `upstreamBasePath:
"/api/v1"`) ⇒ **nhóm người dùng không cần sửa gì**.

Nhóm công khai cần một passthrough **không yêu cầu phiên**, theo đúng mẫu đã có cho
`lukolek`/`lukode` trong `bff/src/index.ts`:

```ts
// Public ludiskus comments (đọc bình luận trên nội dung công khai, chưa đăng nhập).
const publicLudiskus = config.services.find((s) => s.name === "ludiskus");
if (publicLudiskus) {
  app.all("/api/public/ludiskus/comments/*", proxyPublicLudiskusComment(publicLudiskus));
}
```

Luật của handler:

- Chỉ chuyển tiếp `GET` (mọi method khác ⇒ `405`) tới `/api/v1/public/comments/*`.
- **Không** đính header `X-Gw-*`, **không** đính `Authorization`.
- Rate limit theo IP (`LUDISKUS_COMMENT_PUBLIC_RPM`) tại BFF — service không thấy IP thật.
- Không log query string (tránh ghi `resource_id` của nội dung vào access log).

## 14.5 Worker — bốn ticker mới

`cmd/worker/main.go`:

```go
const (
    commentNotifyInterval    = 10 * time.Second  // flush comment_notify_buffer
    commentVerifyInterval    = 30 * time.Second  // verify target unverified / làm tươi metadata
    commentSweepInterval     = time.Hour         // dọn target mồ côi, buffer chết, revision dư
    commentReconcileInterval = 24 * time.Hour    // đối soát số đếm (chạy 03:00)
)
```

Mỗi ticker gọi đúng một hàm trong `service/comment_worker.go`, mỗi hàm tự giới hạn lô
(`LIMIT 200` cho flush, `LIMIT 100` cho verify, `LIMIT 500` cho reconcile) và tự log
`info` khi có việc, `error` khi lỗi — sao y `ProcessOutbox`/`CleanupOrphans` hiện tại.

Đối soát chạy 24h một lần nhưng phải **canh giờ**, không chạy ngay lúc khởi động (một lần
deploy giữa giờ cao điểm không được kéo theo một lượt quét toàn bảng): kiểm giờ hiện tại,
chỉ chạy khi giờ UTC khớp `LUDISKUS_COMMENT_RECONCILE_HOUR` (mặc định 20 ≈ 03:00 giờ VN).

## 14.6 Quan sát

Log có cấu trúc (`slog`, JSON — đã cấu hình), mỗi dòng kèm `service_code`, `resource_type`,
`target_id`; **không** kèm `body_md`, **không** kèm `resource_id` ở mức `info` cho Target
không public.

Chỉ số nên theo dõi (đọc từ log/DB, chưa có Prometheus trong `ludiskus`):

| Chỉ số | Truy vấn / nguồn | Ngưỡng báo động |
|--------|------------------|-----------------|
| Target `unverified` quá 1 giờ | `SELECT count(*) FROM comment_targets WHERE state='unverified' AND created_at < now()-interval '1 hour'` | > 100 ⇒ resolver của service nào đó đang chết |
| `verify_failures >= 3` | cùng bảng | > 0 ⇒ kiểm `base_url` |
| Buffer tồn | `SELECT count(*) FROM comment_notify_buffer WHERE flush_after < now()-interval '5 min'` | > 500 ⇒ worker không chạy |
| Outbox `failed` tiền tố `ludiskus.comment.` | bảng `outbox` | > 0 ⇒ lunoti hoặc credential |
| Hàng đối soát lệch | `SELECT count(*) FROM comment_count_check` | > 0 ⇒ có bug trong đường đếm |
| Item hàng chờ mồ côi | `moderation_items` `state='pending'`, `space_uuid IS NULL`, > 7 ngày | > 0 ⇒ service chưa cài đường S2S moderate ([13 §13.1](13-tich-hop-service.md) bước 6) |
| Cờ abuse mở | `comment_abuse_flags` `state='open'` | Xem hàng ngày |

`/readyz` **không** thêm mục mới: bình luận không có phụ thuộc cứng nào ngoài Postgres (đã
kiểm). Resolver/lunoti/lufami là phụ thuộc **mềm** — đưa vào `readyz` sẽ làm cả `ludiskus`
không ready khi một service khác chết, đó là sai.

## 14.7 Sổ tay xử lý sự cố

| Hiện tượng | Nguyên nhân thường gặp | Xử lý |
|-----------|------------------------|-------|
| "Không bình luận được" ở một service | `comment_services.is_active=false`, hoặc policy `enabled=false`, hoặc `who_can_comment=members` mà Target không có `space_uuid` (suy biến thành `owner_only` — [04 §4.6](04-hop-dong-resource.md)) | Đọc `capabilities.reasons` trong response `GET /comments/r/…` — nó nói đúng lý do |
| Thread trống ở trang công khai nhưng có bình luận khi đăng nhập | `policy.public_read=false` hoặc `visibility ≠ public` hoặc `state='unverified'` | §6.8 |
| Bình luận kẹt `pending` | `pre`/`first_comment` + Target không thuộc Space + service chưa cài S2S moderate | Tạm đổi policy sang `post`, rồi làm bước 6 của checklist |
| Số bình luận sai | Bug đường đếm hoặc crash giữa transaction (không thể — cùng tx) hoặc sửa dữ liệu bằng tay | `POST /admin/comments/reconcile-counters?target=` |
| Bình luận không có nút like | `LUFAMI_API_URL` rỗng, hoặc `lufami` chưa có `ludiskus` trong `interaction_services`, hoặc provider `interaction-context` chưa có nhánh `comment` ([09 §9.7](09-thong-bao-va-theo-doi.md)) | Kiểm theo thứ tự đó |
| Thông báo dồn dập | `LUDISKUS_COMMENT_NOTIFY_DEBOUNCE` bị đặt quá nhỏ, hoặc mention bị lạm dụng | Tăng debounce; siết `mentions.max_per_comment` |
| `401` trên nhóm S2S | `HIPCORE_AUDIENCE` đang được đặt (§14.3) hoặc thiếu `oauth_client_id` trong registry | §6.7 |
