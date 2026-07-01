# 01 — Tổng quan

## 1.1 Mục tiêu

Bổ sung cho hippo một dịch vụ **thảo luận dạng diễn đàn/cộng đồng** dùng chung,
để mỗi **Space** của HipCore có thể trở thành một **cộng đồng/forum riêng** mà
không service nào phải tự dựng. ludiskus cung cấp:

1. **Space như một cộng đồng/forum** — mỗi Space (nguồn HipCore) là một không
   gian thảo luận độc lập: có Board (chuyên mục), Topic (chủ đề), Post (bài/trả
   lời). Một Space riêng tư (`is_public=false`) chỉ thành viên thấy; Space công
   khai cho phép đọc rộng hơn theo cấu hình.
2. **Thảo luận bằng Profile** — mọi hành động (đăng topic, trả lời, reaction,
   báo cáo) gắn với **Profile** đang hoạt động của người dùng (không phải user
   thô), thống nhất với mô hình danh tính của hippo.
3. **Cache Profile & Space** — ludiskus lưu cache thông tin Profile, Space và
   **thành viên Space** (lấy từ HipCore) để hiển thị tác giả, phân quyền và liệt
   kê nhanh mà **giảm số lần gọi API tới HipCore**.
4. **Thông báo qua lunoti** — khi có **trả lời**, **mention** (`@profile`),
   **reaction**, hay được duyệt/từ chối bài, ludiskus đẩy *event* sang lunoti;
   lunoti lo việc phát web/email/… theo preference người nhận.
5. **Tìm kiếm bài viết/chủ đề** — tìm toàn văn theo tiêu đề + nội dung, lọc theo
   Space/Board/tag/tác giả/thời gian, xếp hạng theo độ liên quan & hoạt động.
6. **Đính kèm tập tin/hình ảnh** — tải tệp lên MinIO (presigned), gắn vào bài
   viết, kiểm tra loại/kích thước.
7. **Kiểm duyệt nội dung theo Space** — mỗi Space cấu hình chế độ kiểm duyệt
   (không / hậu kiểm / tiền kiểm / kiểm bài đầu của thành viên mới), lọc từ cấm,
   báo cáo vi phạm, hàng đợi duyệt cho moderator.

Nguyên tắc cốt lõi:

1. **Một mô hình, nhiều dạng cộng đồng** — forum, Q&A, hỗ trợ kỹ thuật, social,
   chia sẻ kiến thức đều dùng chung Space→Board→Topic→Post; khác nhau ở **cấu
   hình** (loại board, kiểm duyệt, quyền) chứ không tách schema.
2. **Danh tính & thành viên thuộc HipCore** — ludiskus không sở hữu user/Profile/
   Space; nó **cache** chúng và soi quyền theo vai trò thành viên Space
   (`owner/admin/member` trong pivot `profile_space`) cộng vai trò riêng của
   forum (`moderator`).
3. **Hoà nhập kiến trúc hippo** — tái dùng HipCore (auth + nguồn Profile/Space),
   Postgres, Redis, MinIO; frontend nằm trong app **tm** và đi qua **BFF**
   (cookie httpOnly) như BG360/luxport/lunoti, **không cần CORS**.
4. **Phát thông báo qua lunoti, không tự gửi** — ludiskus chỉ mô tả *cái gì xảy
   ra* (event) và *gửi cho ai*; lunoti quyết định kênh theo preference. ludiskus
   **không** đụng SMTP/SMS/push.

## 1.2 Phạm vi

Trong phạm vi:

- Dịch vụ `ludiskus` (Go): REST API (`ludiskus-api`) + worker (`ludiskus-worker`)
  cho việc nền (đẩy event sang lunoti, đồng bộ cache, dọn nháp/đính kèm mồ côi,
  cập nhật chỉ mục tìm kiếm, đếm thống kê).
- Mô hình Board / Topic / Post / Reaction / Tag / Subscription / Attachment /
  Report / ModerationItem trên nền Space.
- Kiểm duyệt cấu hình theo Space (4 chế độ + lọc từ cấm + báo cáo).
- Tìm kiếm toàn văn bằng **Postgres FTS** (`tsvector` + `unaccent`, GIN).
- Đính kèm tập tin/hình ảnh qua **MinIO** (presigned upload/download).
- Cache Profile + Space + thành viên Space (Redis hot + bảng bền vững).
- Tích hợp lunoti: đăng ký EventType + Rule, đẩy event reply/mention/reaction/
  moderation.
- Frontend cộng đồng/forum trong app tm (danh sách Space-cộng đồng, board, topic,
  trình soạn bài, tìm kiếm, bảng kiểm duyệt).
- Đóng gói Docker; bổ sung `docker-compose.yml` / `docker-compose.prod.yml`.

Ngoài phạm vi (giai đoạn sau):

- Realtime/WebSocket (gõ trực tiếp, "đang soạn…"); giai đoạn 1 dùng polling +
  thông báo qua lunoti.
- Soạn thảo WYSIWYG nâng cao; giai đoạn 1 nội dung là **Markdown** (sanitize).
- Search engine ngoài (Meilisearch/Elastic); chỉ định nghĩa interface, mặc định
  Postgres FTS.
- Bình chọn (vote) nâng cao/điểm uy tín (reputation) — chỉ chuẩn bị chỗ.
- Liên thông liên Space (cross-post), private message giữa Profile.

## 1.3 Người dùng & ca sử dụng

| Vai trò | Ca sử dụng |
|---------|------------|
| Thành viên Space (Profile) | Đọc, đăng Topic, trả lời, reaction, mention, đính kèm, theo dõi, tìm kiếm, báo cáo |
| Moderator / Space admin | Duyệt bài chờ, gỡ/khoá/ghim/di chuyển topic, xử lý báo cáo, cấu hình kiểm duyệt |
| Space owner | Bật/tắt forum cho Space, tạo/sắp xếp Board, phân moderator |
| Service hippo khác | (tuỳ chọn) tạo Topic hệ thống / thông báo trong một Space qua token client-credentials |
| Người vận hành | Theo dõi hàng đợi event→lunoti, đồng bộ cache, dung lượng đính kèm |

## 1.4 Yêu cầu phi chức năng

- **Hiệu năng**: liệt kê topic/post < 200ms nhờ cache Profile/Space (không gọi
  HipCore mỗi lần render danh sách); đăng bài trả nhanh, việc phụ (thông báo,
  cập nhật đếm, index) chạy nền; tìm kiếm < 300ms với GIN index.
- **Độ tin cậy**: đẩy event sang lunoti **idempotent** (hàng đợi `outbox` trong
  Postgres, SKIP LOCKED, retry/backoff) — mất kết nối lunoti không làm hỏng
  thao tác đăng bài.
- **Bảo mật & riêng tư**: endpoint user yêu cầu Bearer token HipCore; phân quyền
  theo thành viên/role Space; Space riêng tư không lộ nội dung cho người ngoài;
  đính kèm phục vụ qua presigned URL (private bucket) trừ Space công khai.
- **Kiểm duyệt**: nội dung tuân theo chế độ kiểm duyệt của Space; bài tiền kiểm
  không hiển thị công khai cho tới khi được duyệt.
- **Khả năng quan sát**: log có cấu trúc, `/healthz` & `/readyz`, metric số
  topic/post, độ trễ outbox→lunoti, tỉ lệ cache hit Profile/Space, hàng chờ kiểm
  duyệt.
