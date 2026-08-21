# Kế hoạch — LuComment (phân hệ bình luận dùng chung cho hệ sinh thái hippo)

> **LuComment** là phân hệ **bình luận (comment)** thuộc dịch vụ **ludiskus**. Mọi nội dung
> của **bất kỳ service nào** trong hippo — một bộ phim `lumuse`, một sản phẩm `lushoop`, một
> vật phẩm `lukolek`, một bài trình chiếu `lukode`, một màn chơi `lugame`, một địa điểm
> `lutriip` — đều nhúng được **một luồng bình luận** mà **không tạo bảng riêng** và **không
> tạo topic diễn đàn**. Định danh nội dung dùng đúng chuẩn `service:type:id` của
> **Interaction Platform** (`lufami`) và **tái dùng đúng một hợp đồng S2S** mà service sở
> hữu nội dung đã (hoặc sẽ) cài cho Interaction.

## Thông tin phiên bản

| Mục | Giá trị |
|-----|---------|
| Tên phân hệ | LuComment (Comment Platform) |
| Thuộc dịch vụ | `ludiskus` backend (`ludiskus-api` + `ludiskus-worker`) + app **tm** (frontend) |
| Phiên bản tài liệu | 1.0 |
| Ngày cập nhật | 2026-08-21 |
| Trạng thái | **Đã triển khai GĐ0–GĐ7** — xem bằng chứng và giới hạn nghiệm thu bên dưới |
| Tài liệu gốc của ludiskus | [../README.md](../README.md) |
| Tài liệu nền tảng phải đọc kèm | [Interaction Platform (lufami)](../../../lufami/docs/interaction.md) |

## Trạng thái triển khai 2026-08-21

Đã triển khai toàn bộ lát cắt mã nguồn GĐ0–GĐ7: schema/migration, registry và resolver S2S,
API đọc–viết/cây/đính kèm/kiểm duyệt/thông báo, component SolidJS dùng chung, public BFF,
trang quản trị, ba mẫu tích hợp (`lumuse`, `lukode`, `luprojet`), cờ abuse, đối soát và
`sort=top` từ Interaction Platform. Migration bổ sung `0008` sửa constraint
`canonical_path` để tương thích giới hạn repetition của regular expression PostgreSQL.

Bằng chứng đã chạy: `go test ./...`, `go vet ./...`, build/typecheck backend–frontend–BFF;
migration trên PostgreSQL thật theo vòng `up → down → up`; runtime frontend và public list
Lumuse trả `200`, public POST trả `405`; LuProjet private theo Space trả `404`; gọi
S2S chéo service trả `403`; idempotency bình luận hệ thống cho đúng một hàng. Dataset PostgreSQL
10.000 bình luận cho keyset `newest` 0,253 ms và `top` 1,431 ms (ngưỡng 150 ms). Stack local
đã áp migration `0003`–`0008`, đăng ký event type Lunoti và gọi aggregates LuFami bằng OAuth
thật thành công.

Chưa thể xác minh trong phiên thực hiện: tương tác browser có đăng nhập (không có browser được
kết nối), email thực nhận và p95 HTTP end-to-end. Các mục này là cổng nghiệm thu vận hành,
không phải phần mã nguồn còn thiếu; không được coi là đã đạt cho tới khi chạy trong môi trường
QA có browser/mail/load harness.

## Vì sao cần phân hệ này

Hôm nay muốn cho người dùng "nói gì đó" dưới một nội dung, mỗi service có ba lựa chọn và
cả ba đều tệ:

| Cách làm hiện tại | Ai đang làm | Vấn đề |
|-------------------|-------------|--------|
| Tạo hẳn một **topic ludiskus** cho từng nội dung | `lukolek` (nút "Mở thảo luận", lưu `discussion_topic_id` — [lukolek.md §120](../../../lukolek/docs/lukolek.md)) | Topic phải nằm trong một **Space có forum bật**; nội dung riêng tư không có chỗ; người dùng bị bật ra khỏi trang đang xem; một vật phẩm không đáng một chủ đề diễn đàn |
| Tự làm **bảng riêng** trong service | `lurp` (`rq_comments`), `lukomik` (`reviews`), `lubo` (dự kiến `room_messages`) | Mỗi service tự dựng lại: phân trang, @mention, kiểm duyệt, thông báo, chống spam, đính kèm. Không có nơi nào xem "tất cả bình luận của tôi" |
| Không làm gì | `lumuse`, `lugame`, `lushoop`, `lukode`, `lutriip`, `luwep`… | Cờ `allow_comment` bật sẵn trong DB (`lukode` presentation) mà không có gì đứng sau |

Đúng một lần dựng đủ tốt — phân trang keyset, cây trả lời có giới hạn, @mention, kiểm
duyệt bốn đường, thông báo có gom nhóm, đính kèm, chống lạm dụng, đọc công khai — rồi mọi
service **nhúng một component** và **cài một endpoint**. Đó là phân hệ này.

`ludiskus` là chỗ đúng để đặt nó: dịch vụ này **đã có** toàn bộ mảnh ghép cần thiết —
markdown sanitize + trích @mention ([internal/markdown](../../backend/internal/markdown/markdown.go)),
cache Profile/Space/members ([05](../05-cache-profile-space.md)), kiểm duyệt 4 chế độ + báo
cáo + hàng chờ ([04](../04-kiem-duyet.md)), đính kèm MinIO ([07](../07-dinh-kem.md)), outbox
→ lunoti ([08](../08-tich-hop-lunoti.md)), FTS bỏ dấu ([06](../06-tim-kiem.md)), và **đã là
provider** của Interaction Platform (`/api/v1/s2s/interaction-context/{type}/{id}`).

## Ngăn xếp công nghệ (Tech Stack)

| Thành phần | Công nghệ |
|------------|-----------|
| Ngôn ngữ backend | **Go 1.24** — bổ sung file vào các package phẳng sẵn có của `ludiskus` (`internal/domain`, `internal/repository`, `internal/service`, `internal/transport/http`) + một package mới `internal/resolver` |
| Web framework | `net/http` + `go-chi/chi v5`, `jackc/pgx v5` (đã có) |
| Lưu trữ | **PostgreSQL 17**, database `ludiskus`; migration nhúng `//go:embed`, **forward-only** (`0003_comment_core` → `0008_comment_canonical_path`) |
| Cache / rate-limit | **Redis** (đã có, db `/6`) — tiền tố khoá `cmt:` |
| Markdown | `yuin/goldmark` + `bluemonday` (đã có) — thêm **chế độ `basic`** cho thân bình luận |
| Đính kèm | **MinIO** bucket `ludiskus-attachments` (đã có), tái dùng bảng `attachments` |
| Like/Reaction/Bookmark/Share của bình luận | **Không tự làm** — uỷ quyền cho **Interaction Platform** của `lufami` với ref `ludiskus:comment:{id}` |
| Thông báo | **lunoti** qua bảng `outbox` sẵn có + buffer gom nhóm |
| Xác thực người dùng | **HipCore** (đường gateway HMAC của `tm/bff`, hoặc Bearer JWKS) |
| Xác thực service→ludiskus | **HipCore client-credentials** + allowlist `aud` trong bảng `comment_services` (xem [06 §6.7](06-phan-quyen.md) — lỗ hổng phải lấp trước GĐ1) |
| Frontend | App **tm** (SolidJS): `lib/comment.ts` + `components/comment/*` **dùng chung cho mọi service**, không nằm trong `components/ludiskus` |
| Thư viện FE | **Không thêm dependency mới** — `@kobalte/core`, `lucide-solid`, Tailwind 4 đã có |
| BFF | Route `/api/ludiskus/*` **đã có sẵn**; chỉ thêm một passthrough công khai `/api/public/ludiskus/comments/*` (theo mẫu `lukolek`/`lukode` đã có) |
| Đóng gói | **Docker** — không thêm container, không thêm volume, không thêm database |

## Mục lục tài liệu

| # | Tài liệu | Nội dung |
|---|----------|----------|
| 00 | [README.md](README.md) | Trang chỉ mục này + từ điển thuật ngữ + ranh giới khái niệm |
| 01 | [01-tong-quan.md](01-tong-quan.md) | Mục tiêu, phạm vi trong/ngoài, vai trò, ca sử dụng, yêu cầu phi chức năng |
| 02 | [02-kien-truc.md](02-kien-truc.md) | Vị trí trong hippo, sơ đồ, **16 quyết định kiến trúc chốt**, layout mã nguồn |
| 03 | [03-mo-hinh-mien.md](03-mo-hinh-mien.md) | CommentTarget, Comment, Policy, Participant, Revision; vòng đời & máy trạng thái |
| 04 | [04-hop-dong-resource.md](04-hop-dong-resource.md) | Resource ref, registry, resolver S2S, 3 chế độ verify, đẩy/vô hiệu metadata |
| 05 | [05-cay-va-phan-trang.md](05-cay-va-phan-trang.md) | Giới hạn độ sâu, làm phẳng, keyset cursor, sắp xếp, deep-link, đếm |
| 06 | [06-phan-quyen.md](06-phan-quyen.md) | Bốn đường quyền, ma trận hành động, đọc công khai, **lỗ hổng `ServiceMiddleware`** |
| 07 | [07-kiem-duyet.md](07-kiem-duyet.md) | Chế độ kiểm duyệt cho bình luận, từ cấm, báo cáo, tự ẩn, hàng chờ, chống lạm dụng |
| 08 | [08-noi-dung-va-dinh-kem.md](08-noi-dung-va-dinh-kem.md) | Markdown 3 mức, sanitize, @mention, đính kèm, sửa/lịch sử, giới hạn |
| 09 | [09-thong-bao-va-theo-doi.md](09-thong-bao-va-theo-doi.md) | Event lunoti, buffer gom nhóm, theo dõi/mute, đã đọc, hộp thư bình luận |
| 10 | [10-database.md](10-database.md) | Schema đầy đủ + migration `0003`→`0008` (kể cả `.down.sql`), index, đối soát |
| 11 | [11-backend-api.md](11-backend-api.md) | Đặc tả REST: nhóm người dùng, S2S, công khai, quản trị; mã lỗi |
| 12 | [12-frontend.md](12-frontend.md) | `lib/comment.ts`, cây component dùng chung, UX, truy cập được, ngoại tuyến |
| 13 | [13-tich-hop-service.md](13-tich-hop-service.md) | Checklist tích hợp, bảng policy khởi tạo cho toàn hệ sinh thái, client Go mẫu, 4 mẫu tích hợp |
| 14 | [14-trien-khai-docker.md](14-trien-khai-docker.md) | Biến môi trường mới, compose, BFF, vận hành, quan sát |
| 15 | [15-lo-trinh.md](15-lo-trinh.md) | GĐ0–GĐ7, tiêu chí nghiệm thu **chặn**, kiểm thử, bảng rủi ro |
| 16 | [16-cong-viec-chi-tiet.md](16-cong-viec-chi-tiet.md) | Danh sách đầu việc — mã việc, file, cách kiểm chứng, ước lượng |

## Cách đọc

- Quản lý sản phẩm: 01, 15.
- Kỹ sư backend `ludiskus`: 02, 03, 04, 05, 06, 07, 08, 09, 10, 11.
- Kỹ sư frontend `tm`: 03, 05, 11, 12.
- Kỹ sư của service muốn nhúng bình luận: **13** (rồi 04 để cài resolver, 12 để nhúng component).
- DevOps: 02, 14.
- **Người nhận việc để code: 16** (đọc §16.0 trước, rồi mở tài liệu được dẫn trong từng đầu việc).

## Từ điển thuật ngữ

Dùng đúng các tên dưới đây trong tài liệu, mã nguồn, API và giao diện.

| Thuật ngữ | Tiếng Việt | Định nghĩa |
|-----------|-----------|------------|
| **Resource** | Nội dung được bình luận | Một thực thể **thuộc service khác**, định danh bởi `ResourceRef`. LuComment không bao giờ biết cấu trúc bên trong nó. |
| **ResourceRef** | Tham chiếu nội dung | Bộ ba `(service_code, resource_type, resource_id)`. Viết gọn `lumuse:movie:01JZ…`. **Cùng chuẩn** với Interaction Platform. |
| **CommentTarget** | Mục tiêu bình luận | Bản chiếu tối thiểu của một Resource trong DB `ludiskus` + trạng thái luồng (mở/đóng) + số đếm. Bảng `comment_targets`. Một Resource ⇒ **đúng một** Target. |
| **Thread** | Luồng bình luận | Toàn bộ cây bình luận thuộc một Target. Không phải thực thể riêng — là cách gọi tập `comments` của một Target. |
| **Comment** | Bình luận | Một bài viết ngắn của Profile trong một Thread. Bảng `comments`. **Không** gọi là Post. |
| **Root comment** | Bình luận gốc | Comment có `depth = 0`, `parent_id IS NULL`. |
| **Reply** | Trả lời | Comment có `parent_id` ≠ NULL. `depth ≥ 1`. |
| **Depth** | Độ sâu | Số cạnh từ Root tới Comment. Trần theo policy (`max_depth`, mặc định 2). |
| **Flatten** | Làm phẳng | Khi người dùng trả lời một Comment đã ở độ sâu trần: Comment mới **treo vào tổ tiên sâu nhất còn hợp lệ**, và ghi `reply_to_profile_uuid` để giao diện hiện "@tên". Xem [05 §5.2](05-cay-va-phan-trang.md). |
| **CommentPolicy** | Chính sách bình luận | Cấu hình hiệu lực cho một `(service, resource_type)`: ai được viết, độ sâu, kiểm duyệt, đính kèm, giới hạn… Bảng `comment_policies` + hợp nhất 4 tầng ([04 §4.6](04-hop-dong-resource.md)). |
| **Capabilities** | Khả dụng | Kết quả hợp nhất policy cho **một Target cụ thể**, trả về cho frontend để ẩn/hiện nút. Chỉ **thu hẹp** được, không nới rộng. |
| **Resolver** | Bộ phân giải | Client S2S trong `ludiskus` gọi service sở hữu để lấy metadata + quyền xem của một Resource. Package `internal/resolver`. |
| **resource-context** | Ngữ cảnh nội dung | Hợp đồng S2S mà **service sở hữu Resource phải cài**. Tương thích ngược với `interaction-context` đang có. Xem [04 §4.2](04-hop-dong-resource.md). |
| **Participant** | Người tham gia | Profile đã viết trong Thread hoặc theo dõi Thread. Bảng `comment_participants`, mang cả `muted` và `last_read_at`. |
| **Owner** | Chủ nội dung | Chủ sở hữu Resource do resolver khai (`owner.type`/`owner.id`). Có quyền ghim/ẩn/đóng luồng **trên nội dung của mình**. |
| **Service staff** | Nhân sự service | Người có quyền kiểm duyệt **theo RBAC của service sở hữu**. LuComment không biết RBAC đó — service hành động thay họ qua S2S ([06 §6.5](06-phan-quyen.md)). |

## Ranh giới bắt buộc

Ba cặp khái niệm dễ lẫn. Lẫn chúng là cách nhanh nhất làm hỏng phân hệ.

### Comment ≠ Post (bài diễn đàn)

| | Post (`posts`) | Comment (`comments`) |
|---|---|---|
| Thuộc về | Một `Topic` trong một `Board` của một Space có forum bật | Một `CommentTarget` = nội dung **của service khác** |
| Ai định nghĩa quyền | `space_forums.post_policy` + vai trò trong Space | `CommentPolicy` + `visibility` do resolver khai |
| Có tiêu đề? | Topic có `title`, `slug`, tag, ghim, "câu trả lời đúng" | Không tiêu đề, không slug, không tag |
| Deep-link | Trang `ludiskus` (`/ludiskus/s/{space}/t/{slug}`) | Trang **của service sở hữu** (`canonical_path` + `#comment-{id}`) |
| Tìm kiếm | Nằm trong FTS toàn diễn đàn ([06](../06-tim-kiem.md)) | Chỉ tìm **trong một Thread** ([05 §5.7](05-cay-va-phan-trang.md)) |

**Không** có đường tự động biến Thread thành Topic hay ngược lại. Không migration nào chuyển
`posts` sang `comments`. Hai mảng dùng chung hạ tầng (markdown, cache, kiểm duyệt, outbox)
và **không dùng chung bảng nội dung**.

### Comment ≠ Chat

Không có "đang gõ…", không có tin nhắn 1-1, không cần WebSocket ở v1. Hippo **không có dịch vụ chat**
([lubo/docs/room-planner.md §92](../../../lubo/docs/room-planner.md) đã ghi nhận điều này) và
LuComment **không trở thành** dịch vụ chat: bình luận là nội dung công khai trong
phạm vi nhìn thấy Resource, có kiểm duyệt, có lịch sử sửa.

### Comment ≠ Review / Rating

Bình luận **không mang điểm số** và **không thay đánh giá**. `lukomik.reviews`,
`luservit` (đánh giá sau giao dịch), `lushoop` (review sản phẩm gắn với đơn hàng đã mua)
giữ nguyên bảng của mình — chúng có luật riêng (một người một lần, phải mua rồi mới viết,
tính điểm trung bình). Nếu một service muốn **cả hai**, nó hiện review của mình *và* nhúng
Thread LuComment bên dưới; hai thứ không trộn vào nhau.
