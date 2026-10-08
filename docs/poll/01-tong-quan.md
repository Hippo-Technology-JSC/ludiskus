# 01 — Tổng quan

## 1.1 Mục tiêu

1. **Một nền bình chọn cho toàn hệ sinh thái.** Service nào muốn hỏi ý kiến người dùng chỉ cần
   (a) đã có hàng trong registry `comment_services` (17 service đã được seed), (b) cài
   `resource-context` hoặc `interaction-context` (đa số đã cài cho LuComment), và (c) nhúng
   component `<PollList>`/`<PollEmbed>`. Không bảng mới ở service đó.
2. **Dùng độc lập được.** Người dùng tạo một poll không gắn vào đâu, gửi liên kết cho bạn bè,
   hoặc đăng trong Space của mình.
3. **Đúng trước, đẹp sau.** Không bao giờ đếm sai, không bao giờ cho bỏ phiếu hai lần, không
   bao giờ lộ kết quả trước thời điểm đã hứa, **không bao giờ lộ danh tính người bỏ phiếu
   trái với mức danh tính đã cam kết lúc họ bỏ phiếu**.
4. **Kiểm phiếu đáng tin cho quyết định nhóm.** Túc số, tỉ lệ tham gia, xếp hạng (IRV/Borda)
   có luật phá hoà được ghi rõ và hiển thị kèm kết quả.

## 1.2 Phạm vi

### Trong phạm vi v1

| Nhóm | Nội dung |
|------|----------|
| Loại bình chọn | `single` (chọn một), `multiple` (chọn nhiều, có min/max), `ranked` (xếp hạng), `scale` (thang điểm một câu), `schedule` (chọn thời gian, có/có thể/không) |
| Gắn kết | Độc lập (cá nhân hoặc trong Space), chủ đề diễn đàn, bài trả lời, bình luận, bất kỳ resource đã đăng ký |
| Danh tính | `public`, `owner_only`, `anonymous`, `secret` |
| Lộ kết quả | `always`, `after_vote`, `after_close`, `owner_only` + ngưỡng tối thiểu người bỏ phiếu cho chế độ ẩn danh |
| Ai được bỏ phiếu | `viewers` (ai xem được), `members` (thành viên Space), `invited` (danh sách chỉ định) |
| Thời gian | Mở hẹn giờ, đóng hẹn giờ, đóng tay, mở lại (có điều kiện), gia hạn |
| Tuỳ chọn | Đổi phiếu, rút phiếu, người bỏ phiếu thêm lựa chọn (có kiểm duyệt), xáo thứ tự lựa chọn, túc số |
| Thông báo | Poll đóng có kết quả, sắp đóng mà chưa bỏ phiếu, lựa chọn mới chờ duyệt, được mời bỏ phiếu |
| Tương tác | Like/bookmark/share qua Interaction Platform; bình luận dưới poll qua LuComment |
| Kiểm duyệt | Báo cáo poll, ẩn/xoá, hàng chờ lựa chọn do người dùng thêm, từ cấm |
| Đọc công khai | Poll `public` đọc ẩn danh qua BFF (chỉ đọc, kết quả theo luật lộ kết quả) |
| Xuất | CSV kết quả; CSV danh sách người bỏ phiếu khi danh tính cho phép |
| S2S | Service tạo poll trên nội dung của chính nó, đọc kết quả, đóng/ẩn thay staff của nó |

### Ngoài phạm vi v1 (có chủ ý)

| Không làm | Vì sao | Khi nào xem lại |
|-----------|--------|-----------------|
| Khảo sát nhiều câu hỏi, form, rẽ nhánh | Ranh giới [README](README.md) — đó là form engine | Khi có kế hoạch form engine riêng |
| Quiz có đáp án đúng | Thuộc `lugame` | Không |
| Khách chưa đăng nhập bỏ phiếu | Không có cách chống bỏ phiếu lặp đáng tin (IP/cookie đều rẻ) | GĐ sau, khi có captcha + chứng thực thiết bị |
| Phiếu có trọng số (cổ phần, chức vụ) | Cần nguồn trọng số tin cậy theo từng tổ chức; `lurp` chưa có | Khi `lurp` có sổ cổ đông |
| Bầu cử có giá trị pháp lý, chống DBA | Hippo không có ký số/PKI/HSM | Không |
| Kết quả cập nhật realtime qua WebSocket | Thêm hạ tầng; hỏi lại 15s + ETag là đủ ([02 QĐ-P14](02-kien-truc.md)) | Khi có ca dùng trình chiếu trực tiếp > 200 người |
| Thưởng điểm hipt cho người bỏ phiếu | Thưởng cho việc bỏ phiếu làm méo kết quả và khuyến khích tạo poll rác | Không |
| Tìm kiếm toàn văn poll toàn hệ | Poll gần như luôn được tìm thấy qua nơi nó được gắn | GĐ sau nếu trang khám phá cần |

## 1.3 Vai trò

| Vai trò | Ký hiệu | Mô tả |
|---------|---------|-------|
| Người tạo poll | **C** | Profile đứng tên poll (`created_by`). Với poll đại diện Space thì vẫn là người thật bấm tạo |
| Chủ nội dung được gắn | **O** | `owner` mà resolver khai cho Anchor (tác giả chủ đề, tác giả bình luận, chủ bộ phim…) |
| Moderator Space | **M** | Owner/admin/moderator của `space_uuid` của Anchor hoặc của poll độc lập trong Space |
| Service sở hữu Anchor | **S** | Service gọi S2S, hành động thay staff của nó |
| Người bỏ phiếu | **V** | Profile đã có Receipt |
| Người dùng khác | **U** | Ai đó xem được poll |
| Khách | **G** | Chưa đăng nhập, chỉ đọc qua nhóm công khai |

## 1.4 Ca sử dụng

| # | Ca | Loại | Gắn vào | Danh tính | Lộ kết quả |
|---|----|------|---------|-----------|------------|
| UC-1 | Nhóm bạn chọn quán ăn tối nay | `single` | Độc lập, gửi liên kết | `public` | `always` |
| UC-2 | Chọn ngày họp lớp | `schedule` | Độc lập trong Space | `public` | `always` |
| UC-3 | Cộng đồng game chọn chủ đề sự kiện tháng | `ranked` | Chủ đề diễn đàn | `anonymous` | `after_close` |
| UC-4 | Hỏi nhanh trong một bình luận dưới bộ phim | `single` | Bình luận LuComment trên `lumuse:movie` | `public` | `after_vote` |
| UC-5 | Khán giả chấm buổi trình chiếu | `scale` 1–5 | `lukode:presentation` | `anonymous` | `owner_only` |
| UC-6 | Ban chấp hành bầu trưởng ban | `single` | Độc lập trong Space, `members`, túc số 50% | `secret` | `after_close` |
| UC-7 | Dự án bỏ phiếu chọn phương án kỹ thuật | `multiple` max 2 | `luprojet:project` | `owner_only` | `after_vote` |
| UC-8 | `lurp` biểu quyết một nghị quyết nội bộ (**chưa sẵn sàng** — [11 §11.7](11-tich-hop-service.md)) | `single` (Tán thành/Không/Không ý kiến) | Tạo qua S2S trên nội dung của `lurp`, `invited` | `public` | `always` |
| UC-9 | Khối "Bình chọn" giữa tài liệu luxtory | bất kỳ | Gắn `luxtory:document`, nhúng làm khối | bất kỳ | bất kỳ |

## 1.5 Yêu cầu phi chức năng

| Mục | Yêu cầu | Đo bằng |
|-----|---------|---------|
| Đúng số đếm | Sau mọi chuỗi bỏ/đổi/rút phiếu đồng thời, `vote_count` = số đếm từ bảng phiếu; job đối soát báo 0 lệch | Test 200 goroutine × đổi phiếu ngẫu nhiên, rồi chạy đối soát |
| Một người một phiếu | Không bao giờ có hai Receipt cho một `(poll, profile)` | `PRIMARY KEY` + test đồng thời cùng `Idempotency-Key` và khác key |
| Hạn chót | Không phiếu nào được ghi sau khi poll đóng (tay hoặc hẹn giờ) đã commit | Test đua: đóng và bỏ phiếu cùng lúc 1.000 lần |
| Độ trễ bỏ phiếu | p95 ≤ 80 ms ở API (không tính BFF) với 200 người bỏ phiếu đồng thời trên **một** poll | Kịch bản tải [13 GĐ7](13-lo-trinh.md) |
| Độ trễ đọc | p95 ≤ 50 ms cho `GET /polls/{id}` khi có cache; `ranked` 10.000 phiếu tính lại ≤ 150 ms | Như trên |
| Bí mật | Không route nào trả `voter_profile_uuid` trái mức danh tính; poll `secret` không có hàng nào liên kết Profile với lựa chọn | Test quét mọi route + truy vấn SQL kiểm schema |
| Fail closed | Resolver lỗi ⇒ poll gắn vào nội dung nhạy cảm (`strict`) không đọc được, không bỏ phiếu được | Test với resolver giả trả 500 |
| Phụ thuộc mềm | lunoti/lufami/Redis chết không chặn bỏ phiếu | Tắt từng thứ khi chạy test bỏ phiếu |
| Truy cập được | Mọi loại phiếu bỏ được chỉ bằng bàn phím; xếp hạng không bắt buộc kéo-thả | Kiểm tay + Chrome thật ([10 §10.8](10-frontend.md)) |
