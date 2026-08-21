# 13 — Tích hợp một service

## 13.1 Checklist — định nghĩa "xong"

Sáu bước. Bước 1–4 là bắt buộc; bước 5–6 tuỳ nhu cầu.

1. **Đăng ký service.** Một hàng trong `comment_services` (`base_url`, `oauth_client_id`,
   `verify_mode`) — qua `POST /api/v1/admin/comment-services`. Thiếu `oauth_client_id` ⇒ service
   không dùng được nhóm S2S.
2. **Cài endpoint resolver.** `GET /api/v1/s2s/resource-context/{type}/{id}` + biến thể
   `:batch` ([04 §4.2](04-hop-dong-resource.md)). **Đã có `interaction-context`?** Không cần làm
   gì — resolver tự dò (QĐ-3). Chỉ cần bổ sung `capabilities.comment` nếu muốn tắt bình luận
   cho một số nội dung cụ thể.
3. **Đăng ký policy** cho từng `resource_type` (§13.3) — hoặc chấp nhận policy `*` của service.
4. **Nhúng frontend.** `<CommentThread resource={{service,type,id}} />`; feed dùng
   `<CommentProvider>` + `<CommentCount>`. **Không** tự viết ô bình luận.
5. **Vô hiệu khi nội dung đổi.** Gọi `POST /s2s/comments/targets/invalidate` khi xoá nội dung
   hoặc **siết visibility**; gọi `POST /s2s/comments/targets` khi đổi tiêu đề/ảnh/chủ sở hữu.
   Bỏ bước này ⇒ lệch tối đa `LUDISKUS_COMMENT_TARGET_TTL` (6h) — chấp nhận được với tiêu đề,
   **không** chấp nhận được với visibility.
6. **Đường kiểm duyệt.** Nếu policy đặt `pre`/`first_comment` mà Resource **không có
   `space_uuid`**, phải cài giao diện gọi `POST /s2s/comments/{id}/moderate` cho staff của
   service — nếu không bình luận kẹt ở `pending` mãi ([07 §7.1](07-kiem-duyet.md)).

**Bốn phép kiểm trước khi gọi là xong:**

| Kiểm | Kết quả đúng |
|------|--------------|
| Nội dung riêng tư của người khác | `GET /comments/r/…` trả `403`/`404`, **không** trả bình luận |
| Xoá nội dung ở service rồi mở lại Thread | `410 RESOURCE_GONE` |
| Đổi nội dung từ public sang private | Đường công khai `/api/public/…` trả `404` **ngay** (không đợi TTL) |
| Gửi 2 request tạo bình luận cùng `Idempotency-Key` | Đúng một bình luận, `comment_count` tăng 1 |

## 13.2 Quan hệ với Interaction Platform

| Việc | Interaction (`lufami`) | Comment (`ludiskus`) |
|------|------------------------|----------------------|
| Registry | `interaction_services` | `comment_services` (bản riêng — QĐ-16) |
| Resolver mà service phải cài | `s2s/interaction-context` | **cùng một endpoint** (hoặc `resource-context` mới) |
| Policy | `interaction_policies` | `comment_policies` |
| Component tm | `<InteractionBar>` | `<CommentThread>` (bên trong **có** dùng `<InteractionBar>` cho từng bình luận) |
| Ref của một bình luận | `ludiskus:comment:{id}` ← Interaction quản like/reaction của bình luận | — |

Hai registry phải khớp `base_url`/`oauth_client_id`/`verify_mode`. Khi thêm service mới, làm
**cả hai** trong cùng một PR vận hành. Lệch nhau không gây lỗi chéo (mỗi nền tảng độc lập)
nhưng gây hiện tượng khó hiểu: like chạy mà bình luận báo `SERVICE_NOT_REGISTERED`.

## 13.3 Policy khởi tạo cho hệ sinh thái

Seed `db/seeds/comment_policies.json`. Cột rút gọn: **Ai viết** (`who_can_comment`) · **Sâu**
(`max_depth`) · **MD** (`markdown`) · **KD** (`moderation_mode`) · **Đính kèm** · **Công khai**
(`public_read`).

| Service | `resource_type` | Ai viết | Sâu | MD | KD | Đính kèm | Công khai | Ghi chú |
|---------|-----------------|---------|:---:|:--:|:--:|:--------:|:---------:|---------|
| `lumuse` | `movie`, `episode`, `album`, `music`, `video`, `playlist` | authenticated | 2 | basic | post | ✗ | ✓ | Spoiler là chuyện của nội dung, không phải của nền tảng |
| `lukolek` | `collection`, `item`, `catalog`, `catalog_item` | authenticated | 2 | basic | post | ảnh | ✓ | Thay nút "Mở thảo luận"; item riêng tư ⇒ resolver trả `private` |
| `lukode` | `presentation`, `software_doc`, `api_spec`, `db_diagram` | members | 1 | rich | post | ✓ | ✓ (chỉ `presentation`) | Ánh xạ cờ `allow_comment` sẵn có vào `capabilities.comment` |
| `lugame` | `game`, `level`, `ugc_level`, `replay`, `achievement` | authenticated | 1 | plain | first_comment | ✗ | ✓ | `rate_limit.per_minute=2`; nội dung do người dùng tạo ⇒ lọc kỹ |
| `lushoop` | `product`, `shop`, `collection`, `promotion` | authenticated | 2 | basic | post | ảnh | ✓ | Hỏi–đáp dưới sản phẩm; **không** thay review; chủ shop ghim câu trả lời chính thức |
| `lutriip` | `place`, `trip`, `itinerary`, `activity` | authenticated | 2 | basic | post | ảnh | ✓ | |
| `lukomik` | `comic`, `chapter`, `joke` | authenticated | 1 | plain | post | ✗ | ✓ | `reviews` của lukomik giữ nguyên |
| `luxtory` | `article`, `document`, `page` | authenticated | 2 | rich | post | ✓ | ✓ | Tài liệu dài ⇒ cho markdown đầy đủ |
| `lubo` | `boardgame`, `scenario`, `ruleset`, `session`, `room` | members | 2 | basic | none | ✗ | ✗ | `room` thay ý tưởng `room_messages` |
| `luprojet` | `project`, `doc`, `update`, `task` | members | 2 | rich | none | ✓ | ✗ | Nội bộ; `verify_mode=strict` |
| `luservit` | `service`, `provider`, `service_package` | authenticated | 1 | basic | post | ✗ | ✓ | Đánh giá sau giao dịch vẫn là việc của `luservit` |
| `luwep` | `page`, `post`, `block` | authenticated | 2 | basic | pre | ✗ | ✓ | Website công khai ⇒ **tiền kiểm**; chủ website là Owner nên duyệt được qua S2S |
| `lufoodi` | `recipe`, `dish`, `ingredient` | authenticated | 2 | basic | post | ảnh | ✓ | |
| `lutat` | `task`, `submission` | members | 1 | plain | pre | ✗ | ✗ | `strict`; bình luận trên submission ảnh hưởng tiền ⇒ kiểm chặt |
| `luskool` | `lesson`, `announcement`, `material`, `student_work` | members | 1 | basic | pre | ✗ | ✗ | `strict`; **tiền kiểm** vì có học sinh; `sort_default=oldest` |
| `lufami` | `family_post`, `advent_day`, `calendar_event`, `person`, `give_item` | members | 2 | basic | none | ảnh | ✗ | Nội dung gia đình; `strict` |
| `ludiskus` | — | — | — | — | — | — | — | **Không** có policy: bài diễn đàn dùng `posts`, không dùng bình luận (QĐ-8) |

Không có trong bảng ⇒ **không** bật bình luận: `lurp` (dùng `rq_comments` riêng, §13.6),
`luwalet`, `luxport`, `lunoti`, `lukon`, `luddress`, `luvektor`, `lulama` (không có nội dung do
người dùng đọc), `luthreed`/`luxworld` (thêm sau khi có trang nội dung công khai).

## 13.4 Client Go mẫu (≈120 dòng, copy vào service tích hợp)

Không có monorepo package chung (mỗi service có `go.mod` riêng) ⇒ **copy**, đúng như
Interaction Platform đã quyết. File `internal/comment/client.go` phía service tích hợp:

```go
// Client gọi ludiskus để đẩy metadata target và kiểm duyệt thay staff của service.
type Client struct { base, hipcore, clientID, clientSecret string; http *http.Client
                     mu sync.Mutex; token string; exp time.Time }

func (c *Client) Enabled() bool               // base + credential khác rỗng

// UpsertTargets đẩy metadata (≤100). Gọi khi đổi tiêu đề/ảnh/visibility/chủ sở hữu.
func (c *Client) UpsertTargets(ctx context.Context, targets []Target) error

// Invalidate gọi khi xoá nội dung hoặc SIẾT visibility. reason: deleted|visibility|blocked.
func (c *Client) Invalidate(ctx context.Context, refs []Ref, reason string) error

// SetThreadState đóng/mở luồng: open|locked|closed.
func (c *Client) SetThreadState(ctx context.Context, ref Ref, state string) error

// Counts lấy số bình luận cho render phía server (SSR: luwep, lukode public).
func (c *Client) Counts(ctx context.Context, refs []Ref) (map[string]int, error)

// Moderate: service tự kiểm RBAC của mình rồi gọi. action: hide|restore|delete|pin|unpin|approve|reject
func (c *Client) Moderate(ctx context.Context, commentID, action, actorProfileUUID, reason string) error

// SystemComment đăng bình luận hệ thống. idemKey BẮT BUỘC theo nghiệp vụ.
func (c *Client) SystemComment(ctx context.Context, ref Ref, bodyMD, idemKey string) error
```

Luật khi copy:

- Token client-credentials cache trong process, làm mới trước hạn 30s (sao y `notify.Client`
  của `ludiskus`).
- **Mọi lời gọi là best-effort**: chạy trong goroutine với `context.WithoutCancel`, lỗi chỉ
  `log.Warn`. Bình luận không được làm hỏng nghiệp vụ chính.
- **Ngoại lệ**: `Invalidate` với `reason='visibility'` hoặc `'deleted'` **phải** retry (ít nhất
  3 lần, backoff) — đây là đường bảo mật, không phải đường tiện dụng. Không xong thì ghi vào
  outbox của service để thử lại.

## 13.5 Bốn mẫu tích hợp

| Mẫu | Khi nào | Việc phải làm |
|-----|---------|---------------|
| **A. Chỉ nhúng** | Nội dung công khai, không có quyền phức tạp (`lumuse`, `lukomik`, `lufoodi`) | Bước 1–4. Không cần client Go: resolver là endpoint đọc, `invalidate` gọi từ handler xoá |
| **B. Nhúng + quyền riêng** | Nội dung có visibility phức tạp (`lukolek`, `luprojet`, `luskool`) | Thêm: resolver trả đúng `visibility`/`space_uuid`/`owner`; `verify_mode='strict'`; `Invalidate` có retry |
| **C. Trang công khai SSR** | Trang render phía server, có người chưa đăng nhập đọc (`luwep`, `lukode` public) | Thêm: `policy.public_read=true`; BFF passthrough; `Counts()` để in số bình luận vào HTML; nhúng `<CommentThread>` ở phần hydrate |
| **D. Trust mode** | Service muốn tự đẩy hết metadata, không mở endpoint đọc (`lufami` cho nội dung gia đình) | `verify_mode='trust'`, `base_url=''`; **bắt buộc** `UpsertTargets` trước khi trang có thể bình luận; Target chưa push ⇒ `unverified` ⇒ 404 |

## 13.6 Dữ liệu bình luận sẵn có — di trú hay không

| Nơi | Quyết định | Lý do |
|-----|-----------|-------|
| `lurp.rq_comments` (bình luận trên Request) | **Không di trú** | Gắn chặt workflow, có trigger append-only, nằm trong ranh giới mã hoá/audit của `lurp`. Đưa ra ngoài là mất tính chất pháp lý của hồ sơ |
| `lukomik.reviews` | **Không di trú** | Là *review* (có điểm, một người một lần), không phải bình luận — xem ranh giới ở [README](README.md) |
| `lukode.presentation.allow_comment` | **Không có dữ liệu**, chỉ có cờ | Ánh xạ cờ vào `capabilities.comment`; không cần migration |
| `lukolek.discussion_topic_id` (topic ludiskus đã tạo) | **Giữ nguyên**, chạy song song | Topic đã tạo là thảo luận thật, có người theo dõi. Từ nay nút mặc định là bình luận tại chỗ; nút "Mở thảo luận" thành thứ cấp. **Không** chuyển post thành comment (QĐ-8) |
| `lubo` `room_messages` (chưa làm) | **Không làm nữa** | Dùng `lubo:room` làm Target thay vì dựng bảng mới |

Nếu sau này thực sự cần di trú một bảng ngoài vào LuComment, đường đúng là
`POST /s2s/comments/items` với `idempotencyKey` theo id cũ + `createdAt` do service khai (thêm
trường `createdAt` chỉ nhận từ token service, có kiểm không được ở tương lai) — làm theo lô,
không dual-write, đúng khuôn cut-over `0002_interaction_cutover` đã dùng.
