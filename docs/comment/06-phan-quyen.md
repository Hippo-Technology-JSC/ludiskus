# 06 — Xác thực & phân quyền

## 6.1 Bốn nhóm gọi

| Nhóm | Middleware | Ai gọi | Prefix |
|------|-----------|--------|--------|
| Người dùng | `authn.UserMiddleware` | `tm` qua BFF (header `X-Gw-*` ký HMAC) hoặc Bearer JWKS trực tiếp | `/api/v1/comments/*` |
| Công khai | *không có* | Trình duyệt ẩn danh qua BFF passthrough `/api/public/ludiskus/comments/*` | `/api/v1/public/comments/*` |
| Service | `authn.ServiceMiddleware` **+ `requireServiceClient`** (§6.7) | Service sở hữu nội dung, token client-credentials | `/api/v1/s2s/comments/*` |
| Quản trị | `authn.ServiceMiddleware` + `requireServiceClient("ludiskus")` | Vận hành / trang quản trị | `/api/v1/admin/comment-*` |

Actor luôn lấy từ context, **không bao giờ** từ body: `author_profile_uuid =
auth.ProfileUUID(ctx)`. Không có `ProfileUUID` (phiên profile hết hạn) ⇒ `401`, kể cả khi
token còn hợp lệ — vì mọi bình luận phải có Profile đứng tên.

Bình luận với `author_kind='space'` (đại diện Space, ví dụ shop trả lời khách): body có
`actAsSpaceUuid`; **bắt buộc** Profile hiện tại là `owner`/`admin` của Space đó theo
`space_member_cache`, và `author_profile_uuid` **vẫn** là người thật (audit).

## 6.2 Quyền đọc Thread — trình tự `ensureReadable`

Đây là hàm duy nhất được phép quyết định quyền đọc. Mọi route đọc đều đi qua nó.

```
1. ref.Validate()                                       sai charset      ⇒ 400 INVALID_REF
2. registry: service có? is_active?                     không            ⇒ 404 SERVICE_NOT_REGISTERED
3. target := ensureTarget(ref)                           (§4.4/§4.5)
4. target.state == 'gone'                                                ⇒ 410 RESOURCE_GONE
   target.state == 'blocked'                                             ⇒ 403 RESOURCE_BLOCKED
   target.state == 'unverified' && người gọi không phải người tạo target ⇒ 404 (giả vờ không có)
5. theo target.visibility:
     public         ⇒ mọi người (kể cả nhóm công khai, nếu policy.public_read)
     authenticated  ⇒ mọi Profile đã đăng nhập
     space          ⇒ ident.IsMember(target.space_uuid, profile)          không ⇒ 403
     connections    ⇒ KHÔNG hỗ trợ ở ludiskus  ⇒ suy biến thành `private` (xem ghi chú)
     private        ⇒ profile == target.owner_id (owner_type='profile')
                       hoặc là owner/admin của Space (owner_type='space')  không ⇒ 403
6. thread_state == 'hidden' && không phải moderator                      ⇒ 404
7. hợp nhất policy ⇒ capabilities; capabilities.canRead == false          ⇒ 403 COMMENT_DISABLED
```

> **Ghi chú `connections`.** Interaction Platform kiểm quan hệ bằng bảng `profile_relations`
> **nằm trong DB `lufami`**. `ludiskus` không có bảng đó và **không** được truy vấn xuyên
> database (QĐ-16). Vì vậy `visibility='connections'` bị **siết thành `private`**: chỉ chủ nội
> dung đọc/viết được. Service nào cần bình luận cho nội dung "bạn bè xem được" phải tự quy đổi
> sang `space` (tạo Space cho nhóm) — được nêu rõ trong checklist [13 §13.1](13-tich-hop-service.md).
> Fail closed, có chủ ý, và **được ghi lại ở đây** để không ai tưởng là bug.

## 6.3 Quyền viết — trình tự `ensureCommentable`

Chạy **sau** `ensureReadable`, thêm:

```
8.  capabilities.canComment (từ policy.who_can_comment):
      authenticated ⇒ có ProfileUUID
      members       ⇒ ident.IsMember(space_uuid, profile);  space_uuid NULL ⇒ suy biến owner_only
      owner_only    ⇒ profile == owner_id
      staff_only    ⇒ canModerate(role) trong space_uuid
9.  target.thread_state:  locked | closed ⇒ 423 THREAD_LOCKED   hidden ⇒ 404
10. target.state == 'unverified' && verify_mode == 'strict' ⇒ 503
11. rate limit (Redis, §6.6)                                ⇒ 429 RATE_LIMITED
12. chặn trùng nội dung (body_hash trong 60s)               ⇒ 409 DUPLICATE_COMMENT
13. validate: độ dài, số liên kết, số đính kèm, số mention  ⇒ 422 VALIDATION_ERROR
```

## 6.4 Ma trận hành động

`A` = tác giả bình luận · `O` = chủ nội dung (`target.owner_id`) · `M` = moderator Space (có
`space_uuid`) · `S` = service sở hữu (S2S) · `U` = người dùng khác.

| Hành động | A | O | M | S | U |
|-----------|:-:|:-:|:-:|:-:|:-:|
| Đọc Thread | ✓ | ✓ | ✓ | ✓ | theo §6.2 |
| Viết / trả lời | theo §6.3 | ✓ | ✓ | ✓ (`author_kind=service`) | theo §6.3 |
| Sửa nội dung | ✓ trong `edit_window_minutes` | ✗ | ✗ | ✗ | ✗ |
| Xoá | ✓ (nếu `delete_own`) | ✓ | ✓ | ✓ | ✗ |
| Ẩn / khôi phục | ✗ | ✓ | ✓ | ✓ | ✗ |
| Ghim / bỏ ghim | ✗ | ✓ nếu `pin.by ∈ (owner, both)` | ✓ nếu `pin.by ∈ (moderator, both)` | ✓ | ✗ |
| Đóng / mở luồng | ✗ | ✓ | ✓ | ✓ | ✗ |
| Duyệt / từ chối hàng chờ | ✗ | ✗ | ✓ | ✓ | ✗ |
| Báo cáo | ✗ (bài của mình) | ✓ | ✓ | ✗ | ✓ |
| Xem lịch sử sửa | ✓ (của mình) | ✗ | ✓ | ✓ | ✗ |
| Theo dõi / mute / đánh dấu đã đọc | ✓ | ✓ | ✓ | ✗ | ✓ |
| Sửa policy / registry | ✗ | ✗ | ✗ | ✗ | quản trị |

**Không ai** sửa được nội dung bình luận của người khác — kể cả moderator. Kiểm duyệt là
*ẩn/xoá*, không phải *viết lại lời người khác*.

`O` nhưng không phải `M`: chủ nội dung **không** thấy hàng chờ kiểm duyệt (đó là việc của
Space), nhưng ẩn/ghim được trên nội dung của mình. Ranh giới này giữ cho chủ shop không trở
thành moderator của cả Space.

## 6.5 Vì sao cần đường S2S moderate

`ludiskus` biết chắc hai thứ: ai là chủ nội dung (resolver khai) và ai là moderator Space
(cache HipCore). Nó **không** biết `lumuse` coi ai là biên tập viên, `lushoop` coi ai là nhân
viên shop, `luskool` coi ai là giáo viên — mỗi service có RBAC riêng
(`lurp` permission phẳng, `lushoop` RBAC 3 tầng, `lugame` vai trò theo Space…).

Giải pháp: **service tự quyết, rồi hành động thay**:

```
POST /api/v1/s2s/comments/{id}/moderate
{ "action": "hide" | "restore" | "delete" | "pin" | "unpin" | "approve" | "reject",
  "actorProfileUuid": "01JZ…",     # người thật đã bấm, để audit
  "reason": "spam",                 # tuỳ chọn, vào comment_audit_logs
  "note": "…" }
```

`ludiskus` kiểm: token có `aud` trong registry **và** `service_code` của token khớp
`target.service_code` — tức là service chỉ kiểm duyệt được bình luận **trên nội dung của
chính nó**. Không kiểm RBAC nội bộ của service đó (đó là việc của service).

Mọi hành động S2S đều ghi `comment_audit_logs(actor='service:lumuse', actor_profile_uuid=…)`.

## 6.6 Chống lạm dụng ở tầng quyền

Rate limit Redis, cửa sổ trượt theo phút (cùng mẫu Interaction Platform §13.1):

| Khoá | Giới hạn mặc định |
|------|-------------------|
| `cmt:rl:m:{profile}` | `rate_limit.per_minute` = 5 bình luận/phút (toàn hệ) |
| `cmt:rl:h:{profile}` | `rate_limit.per_hour` = 60/giờ |
| `cmt:rl:t:{profile}:{target}` | `per_target_per_hour` = 20/giờ trên **một** Thread |
| `cmt:dup:{profile}:{body_hash}` | TTL 60s ⇒ `409 DUPLICATE_COMMENT` |
| `cmt:rl:rep:{profile}` | 10 báo cáo/giờ (chống dùng báo cáo để dập nội dung) |

Redis chết ⇒ **cho qua** (fail open) và log `warn`: rate limit không phải cơ chế bảo mật, mất
nó không được làm sập tính năng. Ngược lại, quyền (§6.2/§6.3) **fail closed**.

Tài khoản mới (Profile tạo < `LUDISKUS_COMMENT_NEW_PROFILE_HOURS`, mặc định 24h) bị siết:
`per_minute = 2`, `max_links = 0`, và nếu policy là `none`/`post` thì vẫn vào hàng chờ như
`first_comment`. Tuổi Profile lấy từ `profile_cache` (bổ sung cột `created_at` khi sync —
[10 §10.4](10-database.md)).

## 6.7 Lỗ hổng `ServiceMiddleware` phải lấp trước GĐ1

Đọc [`internal/auth/middleware.go`](../../backend/internal/auth/middleware.go):
`ServiceMiddleware` chỉ kiểm **chữ ký + hạn** của token. Nó **không** kiểm:

- token có `sub` hay không ⇒ **access token của một người dùng bình thường đi qua được**;
- `aud` của token là client nào ⇒ **bất kỳ OAuth client nào của HipCore cũng gọi được**.

Hôm nay hậu quả còn nhỏ (`/admin/cache/refresh` làm mới cache; `/s2s/interaction-context` chỉ
đọc metadata). Với LuComment thì **không** nhỏ: nhóm S2S có quyền ẩn/xoá bình luận, đóng luồng,
đăng bình luận hệ thống và xuất toàn bộ Thread.

**Bắt buộc trước khi mở route S2S đầu tiên** — thêm vào `internal/auth`:

```go
// ServiceClaims lấy từ token client-credentials đã verify.
type ServiceClaims struct{ ClientID string /* = claim aud */ }

// ServiceMiddleware (sửa): thêm 2 luật
//  1. sub khác rỗng ⇒ 403 not_a_service_token   (token người dùng bị chặn)
//  2. đặt ClientID = aud vào context
func ServiceClientID(ctx context.Context) string
```

và trong `internal/transport/http/comment_s2s.go`:

```go
// requireServiceClient tra aud trong comment_services.oauth_client_id (cache 60s),
// trả (serviceCode, ok). ok == false ⇒ 403 UNKNOWN_SERVICE_CLIENT.
```

Hai chi tiết dễ sập khi triển khai:

1. **`HIPCORE_AUDIENCE`.** `auth.parse` thêm `jwt.WithAudience(a.audience)` khi biến này khác
   rỗng — nghĩa là **mọi** token phải có `aud` đúng bằng giá trị đó. Nhưng `aud` của HipCore
   là **client id của bên gọi** (`AccessToken::convertToJWT` dùng
   `permittedFor($client->getIdentifier())`), nên bật `HIPCORE_AUDIENCE` sẽ chặn hết token S2S
   của service khác. Vì vậy: nhóm S2S phải parse **không ràng buộc audience** rồi tự kiểm `aud`
   theo registry. Ghi rõ trong [14 §14.3](14-trien-khai-docker.md).
2. **Token của chính `ludiskus`** (dùng cho nhóm quản trị) cũng phải có hàng trong registry với
   `code='ludiskus'` và `oauth_client_id` = client id của nó, nếu không nhóm `/admin/comment-*`
   tự chặn chính mình.

## 6.8 Nhóm công khai — luật cứng

`GET /api/v1/public/comments/*` chỉ trả dữ liệu khi **tất cả** đúng:

1. `target.state = 'active'` (không phải `unverified`);
2. `target.visibility = 'public'`;
3. `policy.public_read = true`;
4. `target.thread_state ∈ (open, locked, closed)` (không phải `hidden`).

Và luôn **lược bỏ**: `author_profile_uuid` thô (chỉ trả `name` + `avatar` + `code`), viewer
state, `body_md` (chỉ `body_html`), lịch sử sửa, danh sách participant, mọi thứ liên quan
`pending`/`hidden`. Không có endpoint ghi nào trong nhóm này (QĐ-14).

Cache: `cmt:pub:{ref}:{cursor}` TTL 30s — và **phải** bị xoá khi visibility siết
([04 §4.5](04-hop-dong-resource.md)).
