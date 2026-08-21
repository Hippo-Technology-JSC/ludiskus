# 01 — Tổng quan

## 1.1 Mục tiêu

Cho **mọi service** trong hippo nhúng được một **luồng bình luận** dưới bất kỳ nội dung nào,
với chi phí tích hợp là **một endpoint S2S + một component frontend**, và với chất lượng
tương đương một hệ bình luận chuyên dụng:

1. **Không bảng riêng cho từng loại nội dung.** Khoá định danh duy nhất là
   `(service_code, resource_type, resource_id)`.
2. **Không kéo người dùng ra khỏi trang đang xem.** Thread hiển thị tại chỗ; thông báo dẫn
   về đúng trang của service sở hữu, không về `ludiskus`.
3. **Không tạo topic diễn đàn giả.** Một vật phẩm `lukolek` không cần một chủ đề trong một
   Space nào cả.
4. **An toàn theo mặc định.** Không xác định được quyền xem nội dung ⇒ **từ chối**. Nội dung
   riêng tư không bao giờ có bình luận công khai.
5. **Một kho bình luận của tôi.** Người dùng xem được mọi bình luận mình đã viết và mọi
   Thread có phản hồi mới, xuyên service.

## 1.2 Phạm vi

### Trong phạm vi v1 (tài liệu này)

| Nhóm | Nội dung |
|------|----------|
| Định danh & hợp đồng | `ResourceRef` chuẩn; registry service; resolver S2S 3 chế độ; đẩy/vô hiệu metadata |
| Nội dung | Viết / sửa (cửa sổ thời gian) / xoá mềm; markdown 3 mức (`plain`/`basic`/`rich`); @mention; đính kèm ảnh & tệp |
| Cấu trúc | Cây trả lời có trần độ sâu + làm phẳng; ghim; phân trang keyset; đếm; tìm trong Thread |
| Quyền | 4 đường (tác giả · chủ nội dung · moderator Space · service sở hữu qua S2S); đọc công khai read-only |
| Kiểm duyệt | 4 chế độ (`none`/`post`/`pre`/`first_comment`); từ cấm; báo cáo + tự ẩn theo ngưỡng; hàng chờ approve/reject; nhật ký quyết định |
| Chống lạm dụng | Rate limit Redis; chặn trùng nội dung; trần độ dài/số liên kết; khoá Thread |
| Thông báo | Event lunoti (`comment.created/replied/mentioned/moderated/pending`); **buffer gom nhóm**; theo dõi/mute Thread; đánh dấu đã đọc |
| Tương tác | Like/Reaction/Bookmark/Share **uỷ quyền** cho Interaction Platform với ref `ludiskus:comment:{id}` |
| Frontend | `<CommentThread>` + composer + moderation dialog dùng chung trong `tm`; trang "Bình luận của tôi"; trang permalink |
| Vận hành | Job đối soát số đếm; dọn Target mồ côi; đo lường |

### Ngoài phạm vi v1 (có thiết kế sẵn, làm sau — [15 §15.9](15-lo-trinh.md))

| Không làm | Lý do |
|-----------|-------|
| **Bình luận của khách (chưa đăng nhập)** | Bề mặt lạm dụng lớn nhất của mọi hệ bình luận (spam, cần captcha, cần chặn IP, cần moderation queue riêng). v1: **đọc** công khai được, **viết** phải đăng nhập |
| **Realtime (WebSocket/SSE)** | v1 dùng poll-khi-focus + `ETag`. Cổng realtime đã có tiền lệ (`lugame-realtime`) nhưng là một container mới — không mở trong v1 |
| **Sắp xếp "nổi bật nhất" (`top`)** | Số like nằm ở `lufami`, không nằm ở `ludiskus`. Cần một đường kéo aggregate theo lô từ Interaction Platform ⇒ GĐ7 |
| **Tìm kiếm bình luận xuyên service** | Rò rỉ phạm vi nhìn thấy: một truy vấn phải kiểm quyền xem trên **n** service. Chỉ tìm trong một Thread |
| **Dịch tự động / kiểm duyệt bằng AI** | `lulama` chạy trên máy chủ riêng, không phải phụ thuộc bắt buộc. Có thể nối sau qua worker |
| **Bình luận theo toạ độ / theo dòng** (comment on canvas, comment on line of code) | `lukomik` §184 và `lukode` cần điều này; nó cần `anchor` (vùng neo) và giải xung đột khi nội dung đổi ⇒ v2, thiết kế ở [03 §3.9](03-mo-hinh-mien.md) đã để sẵn cột `anchor jsonb` |
| **Di trú dữ liệu bình luận cũ của service khác** | `lurp.rq_comments` (gắn workflow, append-only, có mã hoá) **không di trú**. `lukomik.reviews` là review, không phải comment. Xem [13 §13.6](13-tich-hop-service.md) |

## 1.3 Vai trò

| Vai trò | Ai | Làm được gì |
|---------|-----|-------------|
| **Người đọc** | Profile thấy được Resource (hoặc khách, nếu Resource `public` + policy `public_read`) | Đọc Thread, đọc trả lời, xem lịch sử sửa công khai ("đã sửa") |
| **Người bình luận** | Profile thoả `who_can_comment` của policy | Viết / trả lời / sửa trong cửa sổ / xoá bình luận của mình; báo cáo; theo dõi/mute Thread |
| **Chủ nội dung** (Owner) | `owner.id` do resolver khai | Toàn bộ quyền người bình luận + ghim / ẩn / đóng luồng **trên nội dung của mình** |
| **Moderator Space** | `owner`/`admin` theo `space_member_cache` + `space_moderators` | Kiểm duyệt mọi Thread có `space_uuid` thuộc Space đó: hàng chờ, ẩn, khôi phục, xử lý báo cáo |
| **Service sở hữu** | Token client-credentials có `aud` trong registry | Đẩy metadata; đóng/mở luồng; ẩn/khôi phục/xoá một bình luận **thay cho staff của nó**; đăng bình luận hệ thống; xuất Thread |
| **Quản trị nền tảng** | Token service của `ludiskus`/vận hành | CRUD registry + policy; chạy đối soát số đếm; đọc nhật ký |

## 1.4 Ca sử dụng tiêu biểu

1. **`lumuse` — bình luận dưới một bộ phim.** Trang phim nhúng `<CommentThread resource={{service:"lumuse",type:"movie",id}}/>`. Policy: ai đã đăng nhập cũng viết được, độ sâu 2, hậu kiểm, đính kèm ảnh tắt.
2. **`lukolek` — thay nút "Mở thảo luận".** Vật phẩm/bộ sưu tập có Thread ngay tại chỗ. Vật phẩm riêng tư ⇒ resolver trả `visibility=private` ⇒ chỉ chủ sở hữu thấy và viết được. Nút tạo topic `ludiskus` **vẫn giữ** cho thảo luận dài.
3. **`lukode` — bài trình chiếu công khai.** Cờ `allow_comment` sẵn có ánh xạ vào `capabilities.comment` trong resource-context. Trang xem công khai (không đăng nhập) đọc được bình luận qua `/api/public/ludiskus/comments/*`; muốn viết thì đăng nhập.
4. **`lugame` — bình luận màn chơi do người dùng tạo (`ugc_level`).** Policy `self_interaction` không liên quan, nhưng `rate_limit` chặt hơn và `moderation_mode=first_comment` để lọc spam tài khoản mới.
5. **`luskool` — bài học.** `verify_mode=strict` (không đoán), `who_can_comment=members`, không đính kèm, kiểm duyệt **tiền kiểm** cho không gian có học sinh; giáo viên là Owner nên ghim/đóng được.
6. **`lushoop` — hỏi đáp dưới sản phẩm.** Thread hiện **bên cạnh** review (review giữ nguyên ở `lushoop`). Chủ shop là Owner ⇒ ghim câu trả lời chính thức lên đầu.
7. **Hộp thư bình luận.** Người dùng mở `/ludiskus/comments` thấy: Thread có phản hồi mới cho mình, bình luận của mình bị ẩn/từ chối, mọi bình luận mình đã viết — xuyên service, mỗi dòng có icon service (`lukon`) và mở bằng `canonical_path`.

## 1.5 Yêu cầu phi chức năng

| Yêu cầu | Mục tiêu | Cách đạt |
|---------|----------|----------|
| Độ trễ đọc Thread | p95 < 120ms cho 20 root + 3 reply mỗi root (cache nóng) | Hai truy vấn keyset + `ProfileMap` một lượt; không N+1 |
| Độ trễ batch đếm cho feed | p95 < 150ms cho 100 ref | `POST /comments/summary`; cache Redis `cmt:sum:` TTL 60s |
| Độ trễ viết | p95 < 250ms kể cả sanitize markdown | Sanitize đồng bộ, thông báo qua outbox (không chặn) |
| Đúng số đếm | Không bao giờ âm; lệch phải tự phát hiện | `UPDATE … ±1` một câu trong cùng transaction, `CHECK (>= 0)`, job đối soát đêm ([10 §10.9](10-database.md)) |
| Chịu lỗi service ngoài | Resolver chết không làm sập Thread đã có | 3 chế độ verify; `optimistic` là mặc định; Thread đã verify vẫn đọc được ([04 §4.4](04-hop-dong-resource.md)) |
| Bão thông báo | Một Thread nhận 100 bình luận/phút ⇒ **≤ 1** thông báo/người/5 phút | Buffer gom nhóm bắt buộc ([09 §9.3](09-thong-bao-va-theo-doi.md)) |
| Riêng tư | Bình luận không bao giờ lộ ra ngoài phạm vi nhìn thấy Resource | Quyền đọc **luôn** dẫn xuất từ `visibility` của Target, không bao giờ từ cache ([06 §6.2](06-phan-quyen.md)) |
| Truy cập được (a11y) | Bàn phím đầy đủ, `aria-live` cho bình luận mới, tôn trọng `prefers-reduced-motion` | [12 §12.6](12-frontend.md) |
| i18n | Chuỗi giao diện tiếng Việt; nội dung do người dùng nhập giữ nguyên | Không dịch tự động ở v1 |
| Không thêm hạ tầng | Không container mới, không DB mới, không broker | [02 §2.1](02-kien-truc.md) |
