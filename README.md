# ludiskus — Dịch vụ thảo luận (Forum / Community Discussion)

ludiskus bổ sung cho hippo một dịch vụ **thảo luận dạng diễn đàn/cộng đồng**:
mỗi **Space** của HipCore có thể hoạt động như một cộng đồng/forum riêng, người
dùng tham gia thảo luận bằng **Profile**, có tìm kiếm, đính kèm tập tin/hình
ảnh, kiểm duyệt nội dung theo cấu hình Space, và phối hợp với **lunoti** để
thông báo khi có trả lời/mention/reaction.

Service đi theo đúng khuôn của các service Go trong hippo (lubo, luxport,
**lunoti**): một image, hai vai trò `ludiskus-api` / `ludiskus-worker` chọn qua
`LUDISKUS_ROLE`; dùng Postgres, Redis, object storage, auth HipCore; frontend
nằm trong app **tm** đi qua **BFF**.

## Deploy

Repo hiện có sẵn:

- `docker-compose.yml`
- `.env.example`

Chạy standalone:

```bash
cp .env.example .env
docker compose up -d --build
```

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
