# 04 — Hợp đồng resource & resolver S2S

Tài liệu này là **hợp đồng** giữa LuComment và service sở hữu nội dung. Đọc kèm
[13](13-tich-hop-service.md) (checklist tích hợp) và
[Interaction Platform §9](../../../lufami/docs/interaction.md) (bản gốc của hợp đồng).

## 4.1 Registry — `comment_services`

Chỉ service có hàng trong registry và `is_active = true` mới được có Target.

| Cột | Ý nghĩa |
|-----|---------|
| `code` | Khớp `services.code` ở HipCore và khớp `interaction_services.code` ở `lufami` (QĐ-16) |
| `name` | Tên hiển thị (hộp thư, trang quản trị) |
| `base_url` | Gốc S2S **nội bộ**, vd `http://lumuse-api:8080`. Rỗng ⇒ không resolve được ⇒ chỉ dùng được ở `verify_mode='trust'` |
| `oauth_client_id` | Claim `aud` khi service gọi **ngược** vào `ludiskus`. **Bắt buộc** để dùng nhóm S2S ([06 §6.7](06-phan-quyen.md)) |
| `verify_mode` | `strict` \| `optimistic` \| `trust` — §4.4 |
| `context_path` | `''` (tự dò) \| `'resource-context'` \| `'interaction-context'` — đường resolver đã xác nhận, do worker ghi lại (§4.3) |
| `is_active` | Tắt mềm một service mà không mất dữ liệu |

`base_url` **không** đặt trong biến môi trường: thêm một service không được phải deploy lại
`ludiskus`.

## 4.2 Hợp đồng service sở hữu phải cung cấp

```
GET  {base_url}/api/v1/s2s/resource-context/{resource_type}/{resource_id}
POST {base_url}/api/v1/s2s/resource-context:batch     { "refs": [{type, id}, …] }   # ≤ 100
```

Xác thực: Bearer **client-credentials của `ludiskus`** (`LUDISKUS_HIPCORE_CLIENT_ID/SECRET`,
cùng credential đang dùng để đọc `/api/profiles*`).

Response cho một ref:

```json
{
  "exists": true,
  "type": "movie",
  "id": "01JZ...",
  "spaceUuid": "01JZ...",
  "owner": { "type": "profile", "id": "01JZ..." },
  "visibility": "public",
  "state": "active",
  "title": "Tên phim",
  "summary": "Mô tả ngắn…",
  "thumbnailUrl": "http://minio:9000/lumuse-media/...",
  "canonicalPath": "/lumuse/movies/01JZ...",
  "capabilities": { "comment": true, "attach": false, "maxDepth": 1, "publicRead": true }
}
```

| Trường | Bắt buộc | Luật |
|--------|----------|------|
| `exists` | ✓ | `false` ⇒ LuComment đặt `state='gone'`, Thread trả `410` |
| `visibility` | ✓ | Một trong `public`, `authenticated`, `space`, `connections`, `private`. **Đây là nguồn duy nhất** cho quyền đọc Thread |
| `state` | ✓ | `active` \| `gone` \| `blocked` |
| `owner` | ✓ | Ai được ghim/ẩn/đóng luồng trên nội dung này |
| `spaceUuid` | — | Có ⇒ moderator Space đó có quyền kiểm duyệt Thread. Không có ⇒ chỉ còn 3 đường quyền còn lại |
| `canonicalPath` | — | **Đường dẫn tương đối** trong `tm`, `^/[A-Za-z0-9/_.:-]{0,300}$`. URL tuyệt đối bị **từ chối** (open redirect). Rỗng ⇒ hộp thư dùng permalink `/ludiskus/c/{id}` |
| `title`, `summary` | — | Cắt còn 200 / 400 ký tự khi lưu; escape khi render |
| `thumbnailUrl` | — | Chỉ nhận host nội bộ hoặc host trong `LUDISKUS_COMMENT_THUMB_HOSTS`; ngoài allowlist ⇒ bỏ trắng |
| `capabilities` | — | Chỉ **thu hẹp** policy (§4.6 tầng 4) |

Trường **dùng chung với Interaction Platform**: `exists`, `type`, `id`, `spaceUuid`, `owner`,
`visibility`, `state`, `title`, `summary`, `thumbnailUrl`, `canonicalPath`, `capabilities`.
Chính vì trùng khớp 100% mà QĐ-3 khả thi.

## 4.3 Tương thích ngược với `interaction-context`

Ba service đã cài `GET /api/v1/s2s/interaction-context/{type}/{id}` (`ludiskus`, `lukolek`,
`lufami` cho `quote`). LuComment **không** bắt họ cài thêm gì.

Thuật toán chọn đường (`resolver.endpointFor`):

1. `comment_services.context_path` khác rỗng ⇒ dùng luôn đường đó.
2. Rỗng ⇒ thử `/api/v1/s2s/resource-context/…`.
3. Nhận `404` hoặc `405` ⇒ thử `/api/v1/s2s/interaction-context/…`.
4. Đường nào trả `200` ⇒ ghi vào `context_path` (một câu `UPDATE`) và cache trong Redis
   `cmt:path:{service}` TTL 24h.
5. Cả hai `404` ⇒ lỗi `RESOURCE_RESOLVER_MISSING`, xử lý theo `verify_mode`.

Chi phí: **một** request thừa cho mỗi service, **một lần**.

`capabilities.comment` không có trong response cũ ⇒ coi như **không khai** (không phải
`false`), nên policy quyết định. Đây là lý do trường này phải "thu hẹp" chứ không "quyết
định": nếu thiếu nó mà mặc định là `false` thì 3 service đang tích hợp Interaction sẽ **không
bật được** bình luận.

## 4.4 Ba chế độ xác minh

| `verify_mode` | Khi Target chưa có trong DB | Dùng cho |
|---------------|-----------------------------|----------|
| `strict` | Gọi resolver **đồng bộ**; resolver lỗi/timeout ⇒ `503 RESOURCE_RESOLVER_UNAVAILABLE`, **không** tạo Target | Nội dung nhạy cảm: `luskool`, `lufami`, `lurp`, `luwalet` |
| `optimistic` | Gọi resolver đồng bộ với timeout ngắn; **thành công** ⇒ Target `active`. **Thất bại** ⇒ vẫn tạo Target `state='unverified'` với `visibility='private'` (chỉ người tạo thấy) và xếp worker verify trong ≤ 60s | **Mặc định** |
| `trust` | Không gọi resolver. Metadata **chỉ** đến từ push S2S (§4.5). Chưa push ⇒ Target `unverified` | Service tự đẩy metadata đầy đủ; hoặc `base_url` rỗng |

Điểm khác Interaction Platform: ở `optimistic`, Target chưa verify được nhận
`visibility='private'` chứ không phải giữ nguyên. Lý do: một *like* rò rỉ ra ngoài chỉ là con
số; một *bình luận* rò rỉ ra ngoài là **văn bản của người dùng**. Fail closed.

Hệ quả của `unverified`:

- Không phát thông báo (kể cả @mention).
- Không trả qua nhóm API công khai.
- Không xuất hiện trong hộp thư của người khác.
- `verify_failures >= 3` ⇒ `state='gone'`; các bình luận **giữ nguyên** (không xoá) nhưng
  Thread trả `410`; số đếm giữ nguyên để đối soát.

## 4.5 Làm tươi & vô hiệu metadata

**Đẩy chủ động (service → ludiskus)** — khuyến nghị mạnh:

```
POST /api/v1/s2s/comments/targets            { "targets": [ {ref, …các trường như §4.2} ] }   # ≤ 100
POST /api/v1/s2s/comments/targets/invalidate { "refs": [ {service,type,id} ], "reason": "deleted" }
POST /api/v1/s2s/comments/targets/settings   { "ref": …, "threadState": "locked" }
```

Gọi `targets` khi đổi tiêu đề / ảnh / **visibility** / chủ sở hữu. Gọi `invalidate` khi xoá
nội dung (`reason ∈ deleted|visibility|blocked|restored`).

**Kéo định kỳ (worker)**: làm tươi Target có `verified_at` cũ hơn
`LUDISKUS_COMMENT_TARGET_TTL` (mặc định 6h), **ưu tiên** Target có `comment_count > 0`, theo
lô 100 ref qua endpoint batch.

**Đổi visibility sang hẹp hơn** phải làm ngay 3 việc, trong một transaction:

1. `UPDATE comment_targets SET visibility = …`
2. Xoá cache Redis `cmt:sum:{ref}` và `cmt:res:{ref}`
3. Nếu visibility mới không còn là `public`: xoá cache đọc công khai (`cmt:pub:{ref}:*`)

Không làm bước 3 là lỗi rò rỉ **thật**: một Thread từng public sẽ còn đọc được ẩn danh trong
TTL.

## 4.6 Hợp nhất policy → capabilities (4 tầng)

Deep-merge theo thứ tự **tăng dần ưu tiên**:

1. `domain.DefaultCommentPolicy` — hằng trong code.
2. `comment_policies` với `resource_type = '*'` của service.
3. `comment_policies` với `resource_type` khớp **chính xác**.
4. `comment_targets.capabilities` — do service sở hữu khai qua resource-context.

**Tầng 4 chỉ được thu hẹp**: cờ boolean hợp nhất bằng **AND**; số (`maxDepth`, `maxLength`,
`maxAttachments`) lấy **min**; danh sách lấy **giao**. Service không thể tự bật `attach` nếu
policy đã tắt, không thể nâng `maxLength` lên trên trần policy.

Cấu trúc `config` đầy đủ:

```json
{
  "enabled": true,
  "who_can_comment": "authenticated",
  "max_depth": 2,
  "max_length": 4000,
  "min_length": 2,
  "markdown": "basic",
  "moderation_mode": "post",
  "banned_words_source": "space",
  "attachments": { "enabled": false, "max_per_comment": 3, "images_only": true },
  "mentions": { "enabled": true, "scope": "space", "max_per_comment": 10 },
  "edit_window_minutes": 15,
  "delete_own": true,
  "pin": { "enabled": true, "by": "owner", "max_pinned": 3 },
  "interaction": { "like": true, "reaction": true, "bookmark": false, "share": false },
  "rate_limit": { "per_minute": 5, "per_hour": 60, "per_target_per_hour": 20 },
  "notify": { "owner": true, "participants": true, "mention": true },
  "report_auto_hide_threshold": 5,
  "public_read": false,
  "sort_default": "newest",
  "max_links": 3,
  "guest": false
}
```

| Khoá | Giá trị & ý nghĩa |
|------|-------------------|
| `who_can_comment` | `authenticated` (mọi user đăng nhập) · `members` (thành viên `space_uuid`; Target không có `space_uuid` ⇒ suy biến thành `owner_only`) · `owner_only` (chỉ chủ nội dung) · `staff_only` (chỉ moderator/owner) |
| `markdown` | `plain` (escape hết, chỉ xuống dòng + link tự động) · `basic` (in đậm/nghiêng/mã/trích/danh sách/liên kết) · `rich` (như bài diễn đàn: tiêu đề, bảng, ảnh) — [08 §8.2](08-noi-dung-va-dinh-kem.md) |
| `moderation_mode` | `none` · `post` (hậu kiểm) · `pre` (tiền kiểm) · `first_comment` (bình luận đầu của mỗi người trong Target đó vào hàng chờ) — [07 §7.2](07-kiem-duyet.md) |
| `banned_words_source` | `space` (dùng `space_forums.banned_words` của `space_uuid`) · `service` (dùng `comment_policies.config.banned_words`) · `none` |
| `mentions.scope` | `space` (chỉ mention thành viên Space) · `participants` (chỉ người đã trong Thread) · `none` |
| `pin.by` | `owner` · `moderator` · `both` |
| `public_read` | Cho phép nhóm API công khai đọc Thread — **chỉ có hiệu lực khi** `visibility='public'` **và** `state='active'` |
| `guest` | Phải luôn `false` ở v1; giữ khoá để không phải đổi schema khi mở ([15 §15.9](15-lo-trinh.md)) |

**Cache policy**: policy đổi rất hiếm, đọc rất nhiều ⇒ cache trong process TTL 60s + khoá
version Redis `cmt:pol:v` (`INCR` khi quản trị sửa) để các pod đồng bộ nhanh hơn TTL.

## 4.7 Client resolver — yêu cầu kỹ thuật

`internal/resolver/resolver.go` (mô phỏng `lufami/internal/resolver`, ~200 dòng):

- Token client-credentials **cache trong process**, làm mới trước hạn 30s (sao y `notify.Client`).
- `http.Client{Timeout: cfg.CommentResolveTimeout}` — mặc định **3s**, không dùng client mặc định.
- Cache kết quả Redis `cmt:res:{service}:{type}:{id}` TTL = `CommentTargetTTL`; miss thì gọi HTTP.
- Batch: gom tối đa 100 ref/request; ref lỗi lẻ **không** làm hỏng cả lô (trả `skipped[]`).
- **Đường nội bộ không qua HTTP**: `ref.Service == "ludiskus"` gọi thẳng
  `service.InteractionContext` (đã có sẵn) — không tự gọi HTTP vào chính mình.
- Mọi lỗi phân loại thành: `NotFound` (⇒ `gone`), `Unavailable` (⇒ theo `verify_mode`),
  `Invalid` (⇒ `400`, log `warn`, không retry).
- Log **không** ghi `resource_id` của Space riêng tư ở mức `info`; chỉ ở `debug`.
