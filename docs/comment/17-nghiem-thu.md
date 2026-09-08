# 17 — LuComment: triển khai bổ sung và nghiệm thu

Ngày **2026-09-08**, Asia/Ho_Chi_Minh. Phạm vi là các khoảng thiếu LuComment trong
[đối chiếu tiến độ](../14-tien-do-thuc-te.md), tiếp nối code sẵn có. Không viết lại
các giai đoạn đã có code. Các thay đổi diễn đàn và công việc chưa commit khác được giữ.

## Những phần đã bổ sung

| Nhóm | Thay đổi hiện tại |
|---|---|
| LC-0.4 | Resolver batch gom cache miss theo service, gửi `resource-context:batch`/`interaction-context:batch`, kiểm từng ref trả về và giới hạn 100. Chỉ fallback single khi batch không được hỗ trợ (404/405), không phát 100 request khi provider 503. Worker verify dùng batch; lỗi phụ thuộc không tự biến resource thành gone sau ba lần. |
| LC-0.5 | Mỗi lần đọc policy kiểm version `cmt:pol:v`, bỏ cache process khi version đổi. Redis lỗi thì đọc policy DB; lỗi truy vấn policy được trả về, không âm thầm dùng default. TTL 60 giây vẫn là fallback khi invalidation Redis không được ghi thành công. |
| LC-1.5 | Profile mới theo `LUDISKUS_COMMENT_NEW_PROFILE_HOURS` được giới hạn tối đa 2/phút, 20/giờ, 10/target/giờ. Code đã có hạn chế link và `first_comment`; đợt này bổ sung hạn mức giờ/target và sửa giới hạn phút khi policy để 0. |
| LC-1 / LC-7 | Idempotency repository dùng advisory lock trước lookup, tránh transaction bị abort bởi unique violation; kiểm cùng tác giả/resource/nội dung. Retry đã ghi thành công được service trả lại trước rate/duplicate guard. |
| LC-2 | Queue giữ `attachmentIds`, parent và khóa idempotency; gom các lần replay đồng thời trong một tab, không ghi đè mutation mới thêm trong lúc replay, không tự bỏ bản nháp cũ khi queue đầy. Lỗi quyền vẫn giữ bản nháp và lý do. UI có chờ gửi/gửi lại/bỏ khỏi queue; F5 giữ nội dung và các ID tệp đã upload. |
| LC-3 | Ô tìm trong Thread có debounce, gọi search API hiện có; xoá từ khoá quay lại danh sách. Drawer thật dùng Kobalte, focus/role dialog/Escape; response cũ không ghi đè Thread đã đổi resource. Upload lỗi hiển thị rõ và composer đợi upload xong. |
| LC-4 | Mention cùng owner/reply được ghi buffer trong transaction tạo/duyệt. Chuyển buffer → outbox và xoá buffer trong một transaction, kiểm claim token để worker hết lease không xoá phần worker mới đang giữ. Không sweep bỏ buffer chỉ vì quá một ngày; lỗi tạm giữ lại để retry. |
| LC-5 | Đọc public kiểm lại quyền/policy hiện tại trước cache. Duyệt/từ chối LuComment dùng transaction chung cho queue, trạng thái, số đếm và thông báo tác giả; owner của resource không thuộc Space cũng được duyệt. |

Không có migration LuComment mới trong đợt này; dùng schema hiện hành tới 0011
(0011 thuộc diễn đàn). Không thêm dịch vụ, thư viện hoặc bảng reaction riêng.

## Kết quả đã chạy

| Kiểm tra | Kết quả |
|---|---|
| Go toàn module | `go test ./...`, `go vet ./...` đạt. |
| PostgreSQL + Redis cô lập | **9 nhóm** `TestCommentAcceptance` đạt; mỗi lần có schema riêng trong DB kết thúc `_test`, tự cleanup. Redis là container riêng, không dùng Redis ứng dụng. |
| Idempotency / counts | 20 goroutine cùng key tạo đúng một comment/participant; retry qua service trả cùng ID. |
| Notification failure injection | Trigger làm INSERT outbox lỗi: buffer vẫn còn. Reclaim lease: worker cũ bị từ chối, worker mới chuyển đúng một outbox. Mention tạo trong transaction và flush có link đúng. |
| Duyệt | Member bị từ chối; owner duyệt resource không Space. Trigger làm buffer lỗi: cả comment và queue còn pending; retry thành công, duyệt lần hai bị từ chối. |
| Public / search | Public GET đạt, POST public 405, API người dùng thiếu token 401; search không dấu; đổi private chặn public dù trước đó đã cache; hidden không lộ nội dung. |
| Policy / abuse | Hai Service instance cùng Redis: đổi policy được instance kia nhận; Profile mới bị giới hạn ở lần thứ ba; client Redis đóng vẫn tạo được comment và ghi warn theo fail-open đã thiết kế. |
| Batch / tải | 100 ref tới một provider được gom thành một POST; provider 503 không fan-out. 10.000 root, 20 trang ×20 item không trùng; p95 handler HTTP + PostgreSQL khoảng **25,25 ms**. Không qua BFF và không đại diện tải production. |
| Frontend | Typecheck/production build đạt, **6 unit test** queue/Thread đạt. |
| Chrome | **10 kiểm tra fixture** đạt: 20 Count → một summary request; 375 px; search; F5 giữ draft+tệp; offline queue; queue hiện trong UI; reconnect gửi một lần với key+tệp cũ; drawer; Escape; không có uncaught exception. Đã xem ảnh drawer. |
| Hồi quy diễn đàn | **8 nhóm** `TestForumAcceptance` với PostgreSQL/MinIO thật tiếp tục đạt. |
| Runtime local | Đã restart API/worker LuDiskus. Health OK; DB/Redis/storage ready; worker sync Profile/Space và đăng ký template Lunoti thành công. |

Artifact tạm: `/private/tmp/lucomment-acceptance.log`,
`/private/tmp/lucomment-frontend-build.log`, `/private/tmp/lucomment-browser-results.json`,
`/private/tmp/lucomment-drawer-375.png`. Artifact có thể được hệ thống dọn; source test
và harness dưới đây dùng để tái tạo.

## Tái chạy

Từ `ludiskus/backend`:

```sh
GOCACHE=/private/tmp/ludiskus-progress-gocache go test ./...
GOCACHE=/private/tmp/ludiskus-progress-gocache go vet ./...
```

Integration cần DB riêng có `pgcrypto`, `unaccent`, `pg_trgm` và role tạo schema.
Ví dụ Compose local đã có DB `ludiskus_forum_acceptance_20260907_test`:

```sh
docker run -d --rm --name lucomment-acceptance-redis --network hippo_hippo \
  redis:7-alpine redis-server --save '' --appendonly no
docker exec -e LUDISKUS_TEST_REDIS_URL=redis://lucomment-acceptance-redis:6379 \
  hippo-ludiskus-api-1 sh -c '
    export LUDISKUS_TEST_DSN="${LUDISKUS_DB_DSN%/*}/ludiskus_forum_acceptance_20260907_test?sslmode=disable"
    GOCACHE=/tmp/ludiskus-forum-gocache go test ./internal/acceptance -run TestCommentAcceptance -v -count=1
  '
docker stop lucomment-acceptance-redis
```

Test từ chối DB không có hậu tố `_test`. Redis phải có hostname chứa `acceptance`;
**test xoá DB Redis này trước khi chạy**, nên chỉ dùng container tạm riêng như lệnh
trên. Identity/provider/Lunoti là fixture; không gửi thông báo kiểm thử tới người thật.

Từ `tm/frontend`:

```sh
npm test -- src/lib/comment.queue.test.ts src/components/comment/CommentThread.test.tsx
npm run build
./node_modules/.bin/vite build --config tools/comment-harness/vite.config.ts
node tools/comment-browser-check.mjs
```

Chrome harness dùng profile mới và API fixture, không lấy cookie của người dùng.
Cần Google Chrome macOS và quyền bind localhost. Các warning jsdom/Solid/localStorage
của môi trường unit test không thay thế kiểm chứng Chrome thật đã chạy riêng.

## Cổng chưa đóng và giới hạn

- [ ] Phiên đăng nhập thật xuyên HipCore → BFF → LuComment trên Lumuse, Lukolek,
  Lukode, LuProjet. Bốn điểm nhúng có code nhưng không coi là bốn luồng runtime đã đạt.
  Browser kết nối hiện trả danh sách rỗng; không có phiên người dùng để làm E2E này.
- [ ] Chuông/email thực nhận, gom nhóm, read/unread/mute và deep-link từ Lunoti thật.
  Đăng ký template và transaction fixture đạt không thay thế bằng chứng nhận thông báo.
- [ ] Tải xuyên BFF với request đồng thời, nhiều service, S2S OAuth/token thực và
  lỗi phụ thuộc phân tán. Số đo 10k root ở trên chỉ là handler + DB fixture.
- [ ] Kiểm nghiệm cây sâu/ghim khi đồng thời thêm/xoá, edit/revisions, bốn embed và
  import Personal Files với tài khoản thật. Bộ test hiện tại không phủ toàn bộ tổ hợp LC.

Queue lưu ID tệp đã upload, không lưu nội dung file chưa upload xong. Tệp pending
vẫn chịu chính sách dọn orphan hiện có; nếu hết hạn, lỗi được giữ trong queue để người
dùng xử lý. Replay được gom trong cùng tab; nhiều tab dựa thêm vào idempotency backend.
Search UI có trong Thread đăng nhập, không mở một endpoint search public mới.
