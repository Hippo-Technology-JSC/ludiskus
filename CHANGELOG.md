## 2026-09-15

### Hiệu năng gợi ý @mention cho Space vài nghìn thành viên
- Tìm thành viên nay lọc và cắt **ngay trong SQL** (`space_member_cache ⨝ profile_cache`).
  Đo trên Space 5.004 thành viên: **1,356 s → 24 ms** khi lọc, **1,272 s → 16 ms** khi truy
  vấn rỗng. Dùng chung cho `ForumMembers` (diễn đàn) và nhánh `scope="space"` của LuComment.
- Dùng `position()` thay `LIKE '%…%'`: khớp chuỗi con y hệt `strings.Contains` và không có ký
  tự đại diện để phải escape — gõ `%` chỉ là gõ một ký tự, không thành "khớp tất cả".
- Thứ tự kết quả nay xác định (khớp từ đầu chuỗi trước → tên → uuid). Danh sách bị cắt ở
  10/20 nên trước đây thứ tự tuỳ Postgres, gõ cùng một chữ hai lần có thể ra hai danh sách.
- Bù phần chênh: thành viên chưa có hàng `profile_cache` bị truy vấn join bỏ qua, trong khi
  đường cũ nạp lười từ HipCore nên vẫn thấy. Nạp lười đúng phần thiếu, có chặn trên.
- **Sửa lỗi nghiêm trọng phát hiện kèm**: `SyncMembers` chỉ lấy MỘT trang `per_page=1000` rồi
  `ReplaceMembers`, nên Space quá 1.000 thành viên thì người thứ 1.001 trở đi biến mất khỏi
  `space_member_cache`. Vì `Role()`/`IsMember()` tra chính bảng đó, những người ấy **mất luôn
  quyền đọc và đăng bài**, không chỉ vắng mặt trong gợi ý mention. Nay duyệt hết trang và chỉ
  `ReplaceMembers` một lần sau khi gom đủ.
- Đạt: 2 unit test phân trang (4.500 thành viên qua 23 trang; chốt chặn vòng lặp vô hạn khi
  API không kèm pagination), nhóm nghiệm thu `member_suggest_scales_to_thousands` trên
  PostgreSQL thật (5.004 thành viên, có ngưỡng 500 ms bắt việc quay lại nạp từng Profile),
  nhóm `mention_space_scope_uses_space_members` phủ nhánh space của LuComment. Năm đối chứng
  ngược đều đỏ đúng chỗ.

### LuComment — mention hiện họ tên và siết đúng context
- `@code` trong bình luận nay render thành chip chỉ hiện **họ tên**. Extension mention
  được gắn cho cả ba chế độ markdown (`plain`/`basic`/`rich`) — LuComment mặc định chạy
  `basic`, nên nếu chỉ `rich` biết đổi tên thì bình luận vẫn hiện `@code`.
- Policy `basic` của bluemonday phải khai thêm `span` + `class` + `data-mention`, nếu không
  chip bị cắt sạch sau khi sanitize.
- **Sửa lệch so với bản thiết kế**: docs/comment/08 ghi Profile ngoài scope "vẫn render tên
  (đẹp)". Làm vậy là rò tên người ngoài context — gõ `@code` của bất kỳ Profile nào trong hệ
  thống cũng khiến họ tên thật hiện ra cho cả luồng. Nay ngoài scope **giữ nguyên `@code`**,
  nên hiện tên ⟺ được báo tin.
- Nhãn hiển thị và hàng `comment_mentions` đi qua **một hàm duy nhất**
  (`commentMentionTarget`), trần `max_per_comment` áp cho cả hai. Bình luận do service viết
  (S2S) không phân giải tên vì đường đó cố ý không báo tin cho ai.
- Trích mention chuyển sang `MentionsInMode` theo cây cú pháp của đúng chế độ, nên `@ai-đó`
  trong khối code không còn báo tin.
- **Ba lệch trong danh sách gợi ý** giữa "được gợi ý" và "nhắc được": `scope = "none"` vẫn gợi
  ý người trong khi không ai nhắc được; chủ tài nguyên nhắc được nhưng không được gợi ý nếu
  chưa từng bình luận; Profile đã ngừng hoạt động vẫn được gợi ý. Đã sửa cả ba.
- Chống trùng thông báo của LuComment **vốn đã đúng** (`commentNotifyRows` loại người được
  nhắc khỏi `created`/`replied`) — khác diễn đàn; nay có cổng chặn giữ nguyên điều đó.
- CSS `.mention` chuyển thành quy tắc trần thay vì lồng trong `.prose-forum`: cùng HTML ấy còn
  hiện ở bình luận (`.prose`), hàng chờ kiểm duyệt và trung tâm bình luận (không class nào).
- Đạt: 3 unit test markdown đa chế độ (có cổng parity riêng cho từng chế độ), nhóm nghiệm thu
  `mention_shows_name_and_stays_in_context` trên PostgreSQL thật, Go build/vet/test, frontend
  typecheck + 14 vitest. Bốn đối chứng ngược đều đỏ đúng chỗ.
- **Còn nợ**: `MentionSuggestions` nhánh `space` tra Profile theo TỪNG thành viên
  (`ident.Profile` mỗi người) rồi mới lọc theo `q` — Space vài nghìn thành viên là vài nghìn
  lượt tra cho mỗi lần gõ. Cần một truy vấn join `space_member_cache ⨝ profile_cache` lọc
  ngay trong SQL; chưa làm vì nằm ngoài phạm vi lần sửa này.

### Thông báo mention — sửa 2 lỗi
- **Nhận hai thông báo cho một bài**: người được @mention gần như luôn đang theo dõi
  chủ đề, nên nhận cả `topic.replied` lẫn `post.mentioned`. Nay họ bị loại khỏi danh
  sách nhận thông báo trả lời; mention là cái được giữ vì nói rõ hơn hẳn.
- **Nội dung thông báo chung chung**: `rules` của lunoti **rỗng hoàn toàn** — ludiskus
  chỉ đăng ký EventType và Template chứ chưa bao giờ đăng ký Rule. Không có Rule khớp,
  lunoti rơi vào nhánh phát ngầm và mọi thông báo đều là "Thông báo mới / Bạn có một
  cập nhật mới.", nên bản sửa nội dung template ngày 2026-09-14 không hề được đọc tới.
  `RegisterEventTypes` nay tạo/cập nhật Rule nối event-type với template cùng code.
- Rule lấy `category` theo event-type (lệch là tuỳ chọn nhận thông báo của người dùng
  không còn khớp) và **không** gửi `channels` để lunoti dùng `defaultChannels`.
- Thêm cổng chặn thứ ba: lunoti thay `{{biến}}` không có trong data bằng **chuỗi rỗng**,
  nên gõ nhầm tên biến lúc sửa lời văn chỉ làm thông báo cụt mất một mảnh chứ không
  báo lỗi ở đâu. Test soi mọi `{{biến}}` trong seed so với các key ludiskus thật sự gửi.
- Đạt: 3 unit test (lunoti giả + biến template), cổng chặn "một bài → một thông báo"
  trong nhóm nghiệm thu PostgreSQL thật, Go build/vet/test. Cả ba cổng đều qua đối
  chứng ngược. Đã build lại + restart `ludiskus-api`/`ludiskus-worker` trên stack dev:
  10 Rule đã được tạo trong lunoti, mỗi cái trỏ đúng template cùng code.
- **Còn nợ**: chưa xem tận mắt một thông báo mới sinh ra sau khi có Rule — cần đăng
  một bài có mention trên stack thật. Thông báo CŨ giữ nguyên nội dung chung chung vì
  `notifications.title/body` được lưu lúc phát, không render lại.

### Diễn đàn — mention hiện họ tên
- `@code` trong bài viết nay render thành chip chỉ hiện **họ tên**, không kèm dấu
  `@` và không kèm code; soạn thảo giữ nguyên cách gõ `@` + code và danh sách gợi ý.
- Handle không phân giải được (người ngoài Space, code sai) giữ nguyên `@code` —
  bỏ `@` ở đó sẽ biến chữ tác giả gõ thành một từ trơ không ai hiểu.
- Bộ parse mention là extension goldmark (`internal/markdown/mention.go`) chứ không
  phải thay chuỗi trên HTML đã render, nên `@` trong code span, code block, email và
  URL không bị đụng tới; handle gốc nằm ở `data-mention` để truy ngược.
- `Service.resolveMention` dùng chung cho cả `post_mentions` lẫn nhãn hiển thị, nên
  người được hiện tên đúng là người nhận thông báo.
- **Sửa lỗi phát hiện kèm**: cổng chặn "chip khớp danh sách trích" lộ ra việc dán một
  khối code có `@ai-đó` vẫn báo tin cho người ấy dù bài không hề hiện tên. Bài viết
  chuyển sang `Renderer.MentionsIn` (theo cây cú pháp) nên chỉ còn nhắc tên đúng
  người hiện thành chip; `markdown.Mentions` dạng regex giữ nguyên cho bình luận.
- Áp cho tạo topic, trả lời, sửa bài, xem trước và mô tả Board. Thêm style
  `.prose-forum .mention` (sáng/tối) dùng chung cho mọi nơi render `body_html`.
- Đạt: 6 unit test markdown (có cổng chặn "chip khớp danh sách trích"), 1 unit test
  ForumEditor, nhóm nghiệm thu `mention_shows_display_name_in_preview_and_post` trên
  PostgreSQL thật, Go build/vet/test và frontend typecheck/vitest.
- **Còn nợ**: `body_html` là ảnh chụp lúc ghi — bài **đã tồn tại từ trước** vẫn hiện
  `@code` cho tới khi được sửa lại; chưa có job backfill render lại. Chưa mở bằng
  Chrome thật để xem chip ở 375 px.

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
