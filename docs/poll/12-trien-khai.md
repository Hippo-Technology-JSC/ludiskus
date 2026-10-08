# 12 — Triển khai & vận hành

Không thêm container, volume, database, bucket hay OAuth client. Không đổi cổng (API `8096` ở
host, `ludiskus-api:8080` trong mạng), Redis vẫn db `/6`.

## 12.1 Biến môi trường mới

Đọc trong `internal/config/config.go` theo mẫu `get(key, fallback)` sẵn có.

| Biến | Mặc định | Ý nghĩa |
|------|----------|---------|
| `LUDISKUS_POLL_ENABLED` | `true` | Tắt toàn phân hệ: mọi route `/polls*` trả `404`, ticker không chạy, `pollIds` trong body bị **từ chối** `422` (không lặng lẽ bỏ qua) |
| `LUDISKUS_POLL_DRAFT_TTL` | `24h` | Nháp chưa gắn quá hạn ⇒ xoá cứng |
| `LUDISKUS_POLL_CLOSE_TICK` | `30s` | Chu kỳ quét poll hết hạn |
| `LUDISKUS_POLL_REMIND_TICK` | `5m` | Chu kỳ nhắc bỏ phiếu |
| `LUDISKUS_POLL_RESULTS_CACHE_TTL` | `10m` | Cache kết quả theo `ballot_version` |
| `LUDISKUS_POLL_ANON_SNAPSHOT_INTERVAL` | `5m` | Ảnh chụp kết quả `anonymous`/`secret` khi đang mở ([05 §5.3](05-bo-phieu-va-kiem-phieu.md)) |
| `LUDISKUS_POLL_NEW_PROFILE_HOURS` | `24` | Ngưỡng tài khoản mới. `profile_cache.created_at` NULL ⇒ coi là **không** mới (cột nullable từ `0003`; fail open có chủ ý, rate limit vẫn chặn) |
| `LUDISKUS_POLL_AUTO_HIDE_THRESHOLD` | `5` | Ngưỡng báo cáo tự ẩn cho poll không thuộc Space |
| `LUDISKUS_POLL_PUBLIC_RPM` | `120` | (BFF) giới hạn mỗi IP cho nhóm công khai |
| `LUDISKUS_POLL_RANKED_LIVE_MAX` | `50000` | Trên số người này, `ranked` đang mở chỉ trả ảnh chụp mỗi phút |

## 12.2 BFF (`tm/bff`)

Route `/api/ludiskus/*` có sẵn đã phủ nhóm người dùng. Thêm **một** passthrough công khai cạnh
`proxyPublicLudiskusComment` (`gateway.ts`):

```
app.all('/api/public/ludiskus/polls/*', proxyPublicLudiskusPoll)
  - chỉ GET/HEAD, còn lại 405
  - không chuyển tiếp danh tính, không cookie
  - rate limit theo IP: LUDISKUS_POLL_PUBLIC_RPM
  - viết lại ⇒ /api/v1/public/polls/*
  - access log che id như cách đang che resource_id của comment
```

Tổng quát hoá hàm hiện có thành `proxyPublicLudiskus(prefix)` thay vì chép — một hàm, hai prefix.

## 12.3 Worker

Bốn ticker mới trong `cmd/worker/main.go`, cạnh ticker của LuComment:

| Ticker | Chu kỳ | Việc | Khoá chống chạy chồng |
|--------|--------|------|-----------------------|
| `CloseDuePolls` | `LUDISKUS_POLL_CLOSE_TICK` | `SELECT … WHERE status='published' AND closes_at <= now() FOR UPDATE SKIP LOCKED LIMIT 100` ⇒ `closed` + chụp `eligible_count` + tính & chốt `poll_results(final=true)` + outbox `poll.closed` — **một transaction mỗi poll** | `SKIP LOCKED` |
| `RemindPolls` | `LUDISKUS_POLL_REMIND_TICK` | Poll `remind` sắp đóng, `reminded_at IS NULL` ⇒ tính người chưa bỏ phiếu (cử tri hợp lệ − Receipt) ⇒ outbox, ghi `reminded_at` cùng tx | `SKIP LOCKED` |
| `SweepPolls` | 1h | Xoá nháp quá hạn; đóng poll có Anchor `gone`; làm mới ảnh chụp ẩn danh đến hạn; xoá cứng phiếu của poll `deleted` > 180 ngày | advisory lock `poll:sweep` |
| `ReconcilePolls` | 03:30 hằng đêm | `poll_count_check` + đối soát `secret`/`ranked` trong Go; sửa số lệch, ghi audit `reconcile`; cờ bất thường [06 §6.5](06-kiem-duyet-chong-lam-dung.md) | advisory lock |

Ticker sẵn có phải sửa ([08 §8.1](08-database.md), [04 §4.2](04-gan-ket-va-phan-quyen.md)):

- Dọn Target mồ côi (`repository/comment_notify.go:158`): thêm `AND NOT EXISTS (SELECT 1 FROM polls WHERE anchor_target_id = comment_targets.id)`.
- Verify Target (`repository/comment_target.go:175`): sắp ưu tiên thêm Target có poll đang mở.

`RegisterEventTypes` không sửa mã — chỉ seed.

## 12.4 Quan sát

Metric (qua `metrics.wrap` sẵn có + bộ đếm mới):

| Metric | Cảnh báo khi |
|--------|-------------|
| `ludiskus_poll_votes_total{kind,identity_mode,result}` (`result` = ok/closed/already/invalid/rate) | `closed` > 5% trong 10 phút (đồng hồ lệch hoặc giao diện không biết poll đã đóng) |
| `ludiskus_poll_vote_duration_seconds` | p95 > 80 ms |
| `ludiskus_poll_tally_duration_seconds{method}` | p95 > 150 ms |
| `ludiskus_poll_close_lag_seconds` (now − closes_at lúc chốt) | > 120 s |
| `ludiskus_poll_reconcile_fixed_total` | > 0 — **mọi** lần sửa số đếm là một bug phải điều tra |

Log: handler `vote` chỉ ghi `poll_id`, `kind`, `status`, `duration` — **không** body, không lựa chọn
([05 §5.3](05-bo-phieu-va-kiem-phieu.md)).

## 12.5 Vận hành

| Tình huống | Làm gì |
|-----------|--------|
| Cần tắt bình chọn trên một loại nội dung | `PUT /admin/poll-policies/{service}/{type}` `enabled:false`; poll đã có chuyển `POLL_DISABLED` khi đọc — không xoá dữ liệu |
| Poll bị spam phiếu | `POST /polls/{id}/close` (M/S) rồi xem `abuse-flags`; không có công cụ xoá phiếu hàng loạt — có chủ ý |
| Kết quả bị nghi sai | `POST /admin/polls/reconcile`; poll đã chốt thì so `poll_results` với kiểm phiếu lại bằng lệnh `go run ./cmd/tools/polltally {id}` (đọc, không ghi) |
| Người dùng yêu cầu xoá dữ liệu cá nhân | Xoá Receipt + phiếu của họ trong poll **đang mở** (như rút phiếu); poll **đã chốt** giữ `poll_results` (không có danh tính) và xoá phiếu có danh tính, ghi audit — số đếm cũ giữ nguyên trong ảnh chụp chốt |
