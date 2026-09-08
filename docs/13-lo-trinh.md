# 13 — Lộ trình

> **Cập nhật diễn đàn 2026-09-08:** đã bổ sung cây trả lời/editor, tìm thân bài
> và bộ lọc, Support/Q&A, kiểm tệp, queue kiểm duyệt và metrics. Xem
> [tiến độ thực tế](14-tien-do-thuc-te.md) và [nghiệm thu](15-nghiem-thu-dien-dan.md).
> PostgreSQL/MinIO và Chrome fixture đạt; phiên BFF thật, S2S Interaction 401,
> chuông/email và tải production còn mở. LuComment tạm hoãn theo yêu cầu.
> Reaction đã chuyển sang Lufami từ migration `0002`.

Triển khai theo lát cắt dọc chạy được end-to-end, mở rộng dần — giống cách lubo/
lunoti đi từ skeleton tới đầy đủ.

## Giai đoạn 0 — Khung service (skeleton)

- `ludiskus/backend` Go: `cmd/api` + `cmd/worker`, `LUDISKUS_ROLE`, `/healthz`,
  `/readyz`, config từ env, kết nối Postgres (pgx) + Redis + MinIO.
- Migration `0001_init` (toàn bộ bảng [09](09-database.md)).
- Auth: middleware JWKS HipCore (user) + middleware client-credentials (service).
- Docker: `ludiskus-api`/`ludiskus-worker`, DB `ludiskus`, bucket, proxy BFF,
  `.env.example`. **Mốc:** đăng nhập tm → gọi `/api/ludiskus/healthz` qua BFF.

## Giai đoạn 1 — Đọc/đăng cơ bản + cache

- Cache Profile/Space/members (lazy fill + full-sync) — [05](05-cache-profile-space.md).
- Bật forum cho Space, CRUD Board, tạo/đọc Topic + Post (Markdown sanitize),
  threaded reply. Phân quyền theo member cache.
- **Mốc:** tạo cộng đồng từ một Space, đăng chủ đề & trả lời, thấy tên/avatar
  tác giả không gọi HipCore mỗi lần.

## Giai đoạn 2 — Tương tác & thông báo

- Reaction, @mention (trích + phân giải), Subscription (authored/participated).
- `outbox` + `ludiskus-worker` đẩy event sang **lunoti**; đăng ký event-types +
  template — [08](08-tich-hop-lunoti.md). Gộp reaction.
- **Mốc:** trả lời/mention/reaction → người nhận thấy thông báo qua chuông
  lunoti trong tm.

## Giai đoạn 3 — Tìm kiếm & đính kèm

- FTS (`tsvector`/`unaccent`/`pg_trgm`), endpoint `/search` + bộ lọc — [06](06-tim-kiem.md).
- Đính kèm MinIO: presign upload/download, kiểm mime/size, dọn mồ côi — [07](07-dinh-kem.md).
- **Mốc:** tìm chủ đề theo từ khoá (có dấu/không dấu), đính ảnh vào bài.

## Giai đoạn 4 — Kiểm duyệt

- 4 chế độ kiểm duyệt theo Space, lọc từ cấm, báo cáo + tự ẩn theo ngưỡng, hàng
  chờ + approve/reject (đẩy event moderation) — [04](04-kiem-duyet.md).
- Bảng kiểm duyệt + trang cấu hình forum trong tm — [11](11-frontend.md).
- **Mốc:** Space tiền kiểm giữ bài chờ duyệt; moderator duyệt/từ chối, tác giả
  được báo.

## Giai đoạn 5 — Hoàn thiện theo mục tiêu sử dụng

- Q&A: `is_answer`/`is_resolved`, sort "chưa trả lời", vote up/down.
- Support: trạng thái open/resolved, gán assignee.
- Hoàn thiện FE (trình soạn @mention, threaded UI, tô sáng tìm kiếm).
- Production compose, metric/observability, tài liệu tích hợp cho service khác.

## Ngoài lộ trình giai đoạn 1 (cân nhắc sau)

- Realtime (WebSocket) cho topic đang mở; webhook `profile.updated`/
  `space.updated` từ HipCore để vô hiệu cache tức thì.
- Search engine ngoài (Meilisearch) qua interface `search.Engine`.
- Điểm uy tín (reputation), huy hiệu, thông báo gộp digest theo ngày.
- Quét mã độc đính kèm (ClamAV), hạn ngạch dung lượng theo Space.

## Hạng mục bổ sung — Phân quyền từng Board

Triển khai tiếp theo: schema/evaluator → guard API/capability → UI cấu hình →
nghiệm thu quyền chéo Board, thu hồi quyền, version và tương thích dữ liệu cũ.

**Trạng thái: thiết kế, chưa triển khai.** Contract và tiêu chí chi tiết:
[16 — Phân quyền theo Board](16-phan-quyen-board.md).
