# 15 — Nghiệm thu diễn đàn

Ngày: **2026-09-08**, múi giờ Asia/Ho_Chi_Minh. Log Docker dùng UTC nên có ngày
2026-09-07. Phạm vi: forum LuDiskus và các trang `tm/frontend/src/pages/ludiskus`.
LuComment tạm hoãn; không dùng các kiểm tra dưới đây để nghiệm thu LuComment.

## Kết quả

| Lớp kiểm tra | Kết quả và bằng chứng |
|---|---|
| Backend | `go test ./...`, `go vet ./...` đạt. Suite acceptance mặc định skip nếu không có DSN riêng. |
| PostgreSQL thật | 8 subtest của `internal/acceptance/forum_test.go` đạt trên DB `ludiskus_forum_acceptance_20260907_test`; schema riêng mỗi lần, tự xoá sau test. |
| Migration | 0011 up → down → up với dữ liệu forum đạt. DB local đang chạy đã có `topics.assignee_profile_uuid` và `trg_forum_first_post_search`; worker ghi applied migration. |
| Nội dung/quyền | Gateway fixture 401/403/404/422 đúng kỳ vọng; private Space, Topic khoá, parent khác Topic, bài ẩn, queue moderator, Q&A/Support. Hai mươi lựa chọn đáp án đồng thời còn đúng một Post `is_answer` khớp Topic. |
| Kiểm duyệt | none/post/pre/first_post, approve/reject, cấm approve lần hai, hai report tự ẩn; người thường không thấy bài ẩn, tác giả/moderator vẫn xem được. |
| MinIO thật | Presign → PUT → Stat → gắn Post đạt; thiếu object/reuse/owner sai bị từ chối. Tệp pending chỉ uploader lấy URL; tệp bài ẩn bị giới hạn theo quyền xem bài. Object kiểm thử được xoá. |
| Lunoti fixture | HTTP 503 → retry; hai worker claim đồng thời; 16 event được fixture nhận, không lặp trong lần test. Link mention đúng Space UUID/Topic UUID/#post. Không gửi thông báo kiểm thử tới người thật. |
| Frontend | `npm run typecheck`, `npm run build` đạt; 6 unit test editor/tree đạt. |
| Chrome fixture | 11 kiểm tra đạt: deep-link tải 3 trang/65 bài; 375 px Topic/Search/NewTopic không tràn ngang; parent reply, mention, search page 2/date filter; moderator xem nội dung; Board editor; không có uncaught exception. Đã xem ảnh 375 px. |
| Runtime local | API/worker restart thành công; `/healthz` OK; `/readyz` DB/Redis/storage OK; `/metrics` có histogram và số liệu forum. Worker sync 89 Profile/90 Space và đăng ký template Lunoti thành công. |
| Độ trễ | 40 lần tìm với handler + PostgreSQL fixture: p95 **1,385 ms**. Đây là tập nhỏ, không qua BFF/mạng trình duyệt; không đại diện tải production. |

Artifact tạm: `/private/tmp/ludiskus-forum-acceptance.log`,
`/private/tmp/ludiskus-forum-browser-results.json`,
`/private/tmp/ludiskus-forum-topic-375.png`,
`/private/tmp/ludiskus-forum-frontend-build.log`. Các artifact `/private/tmp` có thể bị dọn;
source test/harness được giữ trong repo để tái tạo.

## Lệnh tái hiện

Backend, chạy từ `ludiskus/backend`:

```sh
GOCACHE=/private/tmp/ludiskus-progress-gocache go test ./...
GOCACHE=/private/tmp/ludiskus-progress-gocache go vet ./...
```

Nghiệm thu tích hợp cần DSN có database kết thúc bằng `_test`, role tạo schema,
PostgreSQL có `pgcrypto`, `unaccent`, `pg_trgm`. Không dùng DB ứng dụng. Ví dụ với
DB kiểm thử đã chuẩn bị trong Compose local:

```sh
docker exec hippo-ludiskus-api-1 sh -c '
  export LUDISKUS_TEST_DSN="${LUDISKUS_DB_DSN%/*}/ludiskus_forum_acceptance_20260907_test?sslmode=disable"
  export LUDISKUS_TEST_MINIO=1
  GOCACHE=/tmp/ludiskus-forum-gocache go test ./internal/acceptance -run TestForumAcceptance -v -count=1
'
```

MinIO dùng cấu hình storage container, bucket kiểm thử `ludiskus-forum-acceptance`.
Identity và OAuth/Lunoti của suite luôn là fixture; không dùng người nhận thật.
DB và bucket kiểm thử rỗng có thể được giữ để chạy lại; từng schema/object tự cleanup.

Frontend, chạy từ `tm/frontend`:

```sh
npm run typecheck
npm test -- src/components/ludiskus/ForumEditor.test.tsx src/components/ludiskus/forumTree.test.ts
npm run build
./node_modules/.bin/vite build --config tools/forum-harness/vite.config.ts
node tools/forum-browser-check.mjs
```

Harness dùng Chrome headless với profile tạm, server localhost và API fixture,
không dùng cookie của người dùng. Cần Google Chrome tại đường dẫn macOS mặc định
và quyền mở cổng loopback. In-app Browser tại thời điểm kiểm tra không có browser
kết nối (`browsers.list() = []`), nên không có nghiệm thu phiên đăng nhập thật.

## Contract đã chốt trong đợt này

- Search trả **một kết quả/Topic**, kèm `matchedPostId`/`snippet` dẫn tới Post khớp
  tốt nhất; mặc định bao gồm Topic published/locked. Bộ lọc ngày áp theo ngày tạo
  Topic, UTC, bao gồm cả ngày `until`. `status=pending|hidden` cần Space cụ thể
  và quyền moderator. Fuzzy hiện áp cho tiêu đề, không phải mọi trường/tag.
- Queue `/spaces/{space}/moderation/items` chỉ gồm topic/post, có nội dung sanitize;
  không dùng queue LuComment. Capability UI là hỗ trợ hiển thị, backend vẫn áp quyền.
- `unanswered` chỉ Q&A chưa có đáp án được chọn, dù đã có reply. Support gán người
  đang là thành viên bằng UUID; chỉ moderator gán. Tác giả/moderator resolve/reopen.
- Board PATCH dùng trường tuỳ chọn, giữ position/isLocked/minRole khi không gửi;
  description có thể xoá bằng chuỗi rỗng.
- Tệp được kiểm metadata thực của object và gắn trong transaction tạo bài. Không
  coi Stat MIME là antivirus hoặc phát hiện nội dung nhị phân giả loại. Public URL
  đã phát và presigned URL còn hạn không có cơ chế thu hồi tức thời trong đợt này.
- Link forum chuẩn `/ludiskus/s/{spaceUuid}/t/{topicId}#post-{postId}`; Topic có thể
  tự tải thêm trang tới anchor, refresh thao tác giữ các trang đã tải.
- `/metrics` expose nội bộ: aggregate forum + histogram route pattern, không nhãn
  ID/query/identity. HTTP error counter đếm 5xx. Log có request ID; chưa có distributed
  tracing. Triển khai cần cấu hình scrape/alert và kiểm soát ingress của metrics.

## Gate còn mở

- [ ] Phiên HipCore → tm/BFF thật: health alias, CRUD/preview/mention, upload/download,
  private/public, moderator/owner/member, desktop/mobile và điều hướng từ thông báo.
- [ ] Reaction/vote và Lufami Interaction: quan sát runtime có request forum S2S trả
  **401** từ `lufami-worker`, cả trước và sau restart LuDiskus. JWKS HTTP 200;
  token client LuDiskus mới cấp verify thành công và gọi cùng endpoint forum S2S
  **200**. Đây chưa phải token của worker Lufami; nghi token cache cũ nhưng chưa
  chứng minh nguyên nhân. Automatic approval review đã chặn restart worker Lufami
  vì ảnh hưởng luồng dùng chung/LuComment; cần người dùng cho phép trước khi làm mới
  process này. Không thay cấu hình hoặc mã auth dùng chung để vượt qua chặn.
- [ ] Chuông/email thực nhận, gom nhóm và trạng thái đọc qua Lunoti. Đăng ký template
  thành công và fixture outbox không thay thế việc người dùng nhận thông báo.
- [ ] Personal Files import/redeem/copy/download với tài khoản và dịch vụ thật.
- [ ] Tải đại diện qua BFF, lỗi Redis/HipCore/Lunoti/MinIO và scrape/alert vận hành.

## Migration và rollback

0011 thêm cột nullable và trigger tìm kiếm; không thay đổi schema LuComment.
API mới cần migration 0011 trước khi phục vụ. Khi rollback, dừng API/worker mới,
quay về code cũ rồi chạy down có kiểm soát. Down bỏ cột assignee (mất phân công
Support), phục hồi search Topic chỉ tiêu đề; không dùng down như thao tác thử trên
DB đang có người dùng. Up/down có dữ liệu chỉ đã nghiệm thu trên DB kiểm thử riêng.
