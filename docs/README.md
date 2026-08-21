# ludiskus — Dịch vụ thảo luận (Forum / Community Discussion)

ludiskus bổ sung cho hippo một dịch vụ **thảo luận dạng diễn đàn/cộng đồng**:
mỗi **Space** của HipCore có thể hoạt động như một cộng đồng/forum riêng, người
dùng tham gia thảo luận bằng **Profile**, có tìm kiếm, đính kèm tập tin/hình
ảnh, kiểm duyệt nội dung theo cấu hình Space, và phối hợp với **lunoti** để
thông báo khi có trả lời/mention; tương tác xã hội dùng chung Lufami.

Service đi theo đúng khuôn của các service Go trong hippo (lubo, luxport,
**lunoti**): một image, hai vai trò `ludiskus-api` / `ludiskus-worker` chọn qua
`LUDISKUS_ROLE`; dùng chung Postgres, Redis, MinIO, auth HipCore; frontend nằm
trong app **tm** đi qua **BFF**.

## Mục lục

| # | Tài liệu | Nội dung |
|---|----------|----------|
| 01 | [Tổng quan](01-tong-quan.md) | Mục tiêu, phạm vi, vai trò, yêu cầu phi chức năng |
| 02 | [Kiến trúc](02-kien-truc.md) | Vị trí trong hippo, process, luồng xác thực, layout mã |
| 03 | [Mô hình miền](03-mo-hinh-mien.md) | Space-cộng đồng, Board, Topic, Post, Reaction, Tag, Subscription, Report |
| 04 | [Kiểm duyệt nội dung](04-kiem-duyet.md) | Chế độ kiểm duyệt theo Space, hàng đợi duyệt, lọc từ cấm, báo cáo |
| 05 | [Cache Profile & Space](05-cache-profile-space.md) | Hai lớp cache, đồng bộ thành viên, phân quyền |
| 06 | [Tìm kiếm](06-tim-kiem.md) | Postgres FTS (tsvector + unaccent), bộ lọc, xếp hạng |
| 07 | [Đính kèm tập tin/hình ảnh](07-dinh-kem.md) | MinIO, presigned upload/download, kiểm tra loại/kích thước |
| 08 | [Tích hợp lunoti](08-tich-hop-lunoti.md) | EventType/Rule cho reply, mention, reaction |
| 09 | [Database](09-database.md) | Migration, bảng, index, FTS |
| 10 | [Backend API](10-backend-api.md) | REST `/api/v1`, phân quyền |
| 11 | [Frontend (tm + BFF)](11-frontend.md) | Route, proxy BFF, màn hình |
| 12 | [Triển khai Docker](12-trien-khai-docker.md) | compose, DB, OAuth client, bucket, biến môi trường |
| 13 | [Lộ trình](13-lo-trinh.md) | Các giai đoạn triển khai |

## Phân hệ

| Phân hệ | Tài liệu | Nội dung |
|---------|----------|----------|
| **LuComment** | [docs/comment/](comment/README.md) | Bình luận **dùng chung cho toàn hệ sinh thái**: mọi service nhúng được một luồng bình luận dưới bất kỳ nội dung nào (`service:type:id`) mà không tạo bảng riêng và không tạo topic diễn đàn. Đã triển khai GĐ0–GĐ7; bộ 17 tài liệu giữ cả thiết kế, contract và trạng thái nghiệm thu. |

## Mục tiêu sử dụng

ludiskus được thiết kế để phục vụ nhiều dạng cộng đồng trên cùng một mô hình:

- **Forum truyền thống** — Space → nhiều Board → Topic → Post lồng nhau.
- **Q&A community** — Topic dạng `question`, đánh dấu câu trả lời (`is_answer`), vote.
- **Internal discussion** — Space nội bộ (`is_public=false`), chỉ thành viên.
- **Technical support** — Board hỗ trợ, trạng thái `open/resolved`, gán xử lý.
- **Gaming / social community** — reaction, tag, pin, hoạt động sôi nổi.
- **Knowledge sharing** — Topic `announcement`/wiki, tìm kiếm toàn văn.

Khác biệt chỉ là **cấu hình của Space** (loại board, chế độ kiểm duyệt, quyền
đăng bài) chứ không phải mô hình dữ liệu khác nhau.
