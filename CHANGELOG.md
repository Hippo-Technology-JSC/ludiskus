## 2026-09-08

### Thiết kế quyền Board
- Bổ sung [thiết kế phân quyền từng Board](docs/16-phan-quyen-board.md): quyền tạo
  Topic/trả lời, moderator trực tiếp, kế thừa Space, API/schema/UI, tương thích và
  checklist nghiệm thu. Đây là tài liệu thiết kế; chưa thay code hoặc migration.

### LuComment — hoàn thiện tiếp theo yêu cầu
- Bổ sung version policy giữa instance, batch provider/worker, giới hạn Profile mới
  theo giờ/target và idempotency retry an toàn.
- Giữ tệp/parent/key trong offline queue, hiển thị chờ gửi/lỗi/gửi lại; F5 giữ draft.
  Thêm tìm trong Thread và drawer thật, xử lý lỗi upload và response cũ.
- Mention/buffer/outbox, lease và quyết định kiểm duyệt được bảo vệ bằng transaction;
  lỗi không làm mất buffer hoặc quyết định queue trước khi publish. Public kiểm lại quyền trước cache.
- Đạt 9 nhóm nghiệm thu LuComment PostgreSQL/Redis, 6 unit test, 10 Chrome fixture
  checks; 8 nhóm forum hồi quy, Go test/vet và frontend build đạt. Local API/worker đã restart.
- Phiên BFF, chuông/email người thật và bốn service E2E còn mở: [biên bản](docs/comment/17-nghiem-thu.md).

### Diễn đàn — hoàn thiện và nghiệm thu
- Thêm search thân bài/reply, bộ lọc và `search.Engine`; migration 0011 cập nhật
  chỉ mục bài đầu và assignee Topic hỗ trợ.
- Hoàn thiện cây trả lời/phân trang/deep-link, editor Markdown dùng chung, mention,
  preview, quản lý Board, Support, Q&A và queue có nội dung trước khi duyệt.
- Siết quyền bài/tệp, xác minh object MinIO, gắn tệp trong transaction; serialize
  chọn đáp án/reopen/kiểm duyệt. Giữ metadata Board khi PATCH thiếu trường.
- Thêm health alias cho BFF, metric histogram/aggregate và log request ID.
- Đạt 8 nhóm nghiệm thu PostgreSQL/MinIO cô lập, 6 unit test UI, 11 kiểm tra Chrome
  fixture, Go test/vet và frontend typecheck/build. API/worker local đã restart,
  áp migration 0011 và ready DB/Redis/MinIO.
- Còn S2S Interaction thực tế 401, phiên đăng nhập BFF, chuông/email và tải đại diện;
  xem [biên bản nghiệm thu](docs/15-nghiem-thu-dien-dan.md). LuComment không triển khai thêm.

## 2026-09-07

### Documentation
- Thêm [tiến độ thực tế](docs/14-tien-do-thuc-te.md): đối chiếu diễn đàn GĐ0–GĐ5,
  LuComment GĐ0–GĐ7 và tích hợp Personal Files/LuSpotlight/Hipt với code hiện tại;
  ghi rõ khoảng thiếu, công việc còn lại và giới hạn bằng chứng nghiệm thu.
- Đồng bộ README, chỉ mục và lộ trình với kết quả rà soát; giữ kết quả runtime
  2026-08-21 như bằng chứng lịch sử. Sửa hướng dẫn khởi tạo compose từ file mẫu thực có.
- Kiểm tra hiện tại: backend `go test ./...`, `go vet ./...` đạt; không chạy lại
  migration, API/browser, email, tải hay build/typecheck tm trong lần cập nhật tài liệu này.

## 2026-09-05

### Added
- **Provider tìm kiếm cho LuSpotlight**: `POST /api/v1/s2s/search`
  ([`internal/transport/http/s2s_search.go`](backend/internal/transport/http/s2s_search.go)).
  Trả topic đã lọc quyền, kèm `ts_headline` làm đoạn trích và **thứ hạng nội bộ**
  (không gửi `ts_rank` thô — điểm của hai service khác nhau không cùng đơn vị).
  Chi phí thực tế: một handler, vì `Service.Search` đã lọc theo `viewableSpaces`
  của chính người dùng.

### Ghi chú thiết kế
- Route này nằm dưới **middleware auth người dùng bình thường**, cố ý KHÔNG nằm
  trong nhóm `/s2s` dùng `requireService`. Lời gọi chạy thay mặt **người dùng**
  (lufami chuyển tiếp bearer của họ), không thay mặt lufami. Nếu nó nằm dưới
  `requireService` thì ludiskus chỉ biết "có một service gọi tôi" và buộc phải
  tin một `profile_uuid` nào đó trong body — tức là bất kỳ ai giữ một token
  service đều đọc được thảo luận riêng tư của mọi người. Xem
  [`lufami/docs/luspotlight.md`](../lufami/docs/luspotlight.md) §7.1, §8.1.

---

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
