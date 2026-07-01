# 05 — Cache Profile & Space

Để render danh sách topic/post (tên + avatar tác giả), phân quyền (thành viên &
vai trò Space) và phân giải mention/người nhận, ludiskus cần thông tin Profile,
Space và thành viên Space. Nguồn duy nhất là **HipCore**. Gọi HipCore mỗi lần là
không khả thi (chậm, tải lên IdP). Vì vậy ludiskus **cache** theo hai lớp — cùng
khuôn cache Profile của [lunoti](../../lunoti/docs/05-profile-cache.md).

## 5.1 Nguồn dữ liệu HipCore (service-to-service)

HipCore cung cấp API client-credentials (middleware
`EnsureClientIsResourceOwner`, xem [hipcore/routes/api.php](../../hipcore/routes/api.php)):

| Endpoint HipCore | Dùng cho |
|------------------|----------|
| `GET /api/profiles` | Liệt kê Profile (phân trang) — full-sync `profile_cache` |
| `GET /api/profiles/{uuid}` | Một Profile — lazy fill khi cache miss / phân giải mention |
| `GET /api/users/{userId}/profiles` | Profile của một user (khi chỉ có user_id) |
| `GET /api/spaces` | Liệt kê Space — full-sync `space_cache` |
| `GET /api/spaces/{uuid}` | Một Space — lazy fill, kiểm `is_public`/`is_active` |
| `GET /api/spaces/{uuid}/members` | Thành viên + vai trò — đồng bộ `space_member_cache` (phân quyền) |

> Trường dùng được: Profile (`uuid`, `user_id`, `code`, `name`, `avatar`,
> `is_active`) theo [Profile model](../../hipcore/app/Models/Profile.php); Space
> (`uuid`, `name`, `code`, `is_public`, `is_active`, `creator_profile_id`,
> `space_type_id`) theo [Space model](../../hipcore/app/Models/Space.php);
> thành viên qua pivot `profile_space` với `role` (`owner`/`admin`/`member`).
>
> ludiskus là **OAuth client riêng** ở HipCore
> (`LUDISKUS_HIPCORE_CLIENT_ID/SECRET`, grant `client_credentials`); lấy access
> token (cache + tự refresh trước hạn) rồi gọi các endpoint trên.

## 5.2 Hai lớp cache

```
        cần Profile/Space/Members
                  │
                  ▼
        ┌────────────────────────┐  hit   ┌──────────────┐
        │ Redis (hot cache)       │──────▶│  trả dữ liệu  │
        │ ludiskus:prof:{u}       │       └──────────────┘
        │ ludiskus:space:{u}      │
        │ ludiskus:members:{s}    │
        └───────────┬────────────┘ miss
                    ▼
        ┌────────────────────────┐  hit   ┌──────────────┐
        │ Postgres                │──────▶│ nạp lại Redis │
        │ profile/space/member    │       └──────────────┘
        │ _cache                  │
        └───────────┬────────────┘ miss / quá hạn
                    ▼
        ┌────────────────────────┐
        │ HipCore /api/...        │ → ghi *_cache + Redis
        └────────────────────────┘
```

- **Redis** (`LUDISKUS_REDIS_URL`, DB index riêng): cache nóng, TTL
  `LUDISKUS_CACHE_TTL` (mặc định `1h`). Key: `ludiskus:profile:{uuid}`,
  `ludiskus:space:{uuid}`, `ludiskus:members:{space_uuid}`.
- **Bảng `*_cache` (Postgres)**: bản sao bền vững, để (a) render/lọc theo lô mà
  không gọi HipCore, (b) sống sót khi Redis trống, (c) phân quyền nhanh.

## 5.3 Vòng đời & làm mới

| Cơ chế | Khi nào | Hành động |
|--------|---------|-----------|
| **Lazy fill** | Cache miss lúc đọc | Gọi HipCore endpoint tương ứng, ghi `*_cache` + Redis |
| **Full sync** | Ticker worker mỗi `LUDISKUS_PROFILE_SYNC_INTERVAL` / `..._SPACE_SYNC_INTERVAL` (mặc định `6h`) | Phân trang `GET /api/profiles` & `/api/spaces`, upsert, đánh dấu `synced_at`; xoá bản ghi đã biến mất |
| **Member sync** | Khi mở một Space lần đầu sau TTL, hoặc full-sync | `GET /api/spaces/{uuid}/members`, upsert `space_member_cache`, xoá thành viên đã rời |
| **TTL** | Mỗi lần đọc Redis | Hết TTL → đọc lại `*_cache`; bản ghi cũ hơn ngưỡng → refetch HipCore |
| **Chủ động vô hiệu** | Profile/Space/Member đổi | (tương lai) HipCore bắn webhook → xoá key Redis + refetch; hiện tại dựa TTL + full sync |
| **Thủ công** | Vận hành | `POST /api/v1/admin/cache/refresh?type=profile|space|members&id=` |

Idempotent theo khoá; full sync chỉ cập nhật trường đã đổi (so `updated_at`).

## 5.4 Phân quyền theo cache

Mọi kiểm tra quyền dùng `space_member_cache` (không gọi HipCore mỗi request):

| Hành động | Điều kiện |
|-----------|-----------|
| Đọc Space riêng tư (`is_public=false`) | Là thành viên Space |
| Đăng Topic/Post | Thành viên + `post_policy` của SpaceForum cho phép + Board không khoá |
| Reaction / báo cáo | Thành viên (Space riêng tư) hoặc bất kỳ user đã đăng nhập (Space công khai) |
| Kiểm duyệt | role `owner`/`admin` trong Space **hoặc** có trong `space_moderators` |

Khi cache miss thành viên → lazy fill member sync trước khi quyết định; nếu vẫn
không thấy và Space riêng tư → từ chối (an toàn mặc định).

## 5.5 Phân giải mention & người nhận thông báo

- **Mention** `@code`/`@uuid` trong post: tra `profile_cache` theo `code`/`uuid`
  (miss → `GET /api/profiles/{uuid}`); chỉ phân giải tới Profile **là thành viên
  Space** (chống mention người ngoài cộng đồng).
- **Người theo dõi Topic** (Subscription): danh sách `profile_uuid` lấy từ bảng
  `subscriptions` cục bộ; thông tin liên hệ để gửi thì **lunoti** tự cache (đẩy
  event chỉ cần `profile_uuid`).

Nhờ vậy, đẩy event cho hàng trăm người theo dõi chỉ là truy vấn Postgres cục bộ
**không chạm HipCore** — đáp ứng yêu cầu giảm gọi API tới HipCore.

## 5.6 Bảng cache

```
profile_cache(profile_uuid PK, user_id bigint, code text, name text,
              avatar text, is_active bool, synced_at timestamptz)

space_cache(space_uuid PK, code text, name text, is_public bool,
            is_active bool, creator_profile_uuid uuid, space_type text,
            synced_at timestamptz)

space_member_cache(space_uuid, profile_uuid, role text, joined_at timestamptz,
                   synced_at timestamptz, PRIMARY KEY(space_uuid, profile_uuid))
```

## 5.7 Bảo mật & riêng tư

- Token client-credentials của ludiskus chỉ ở backend; không lộ qua BFF/FE.
- `*_cache` chỉ lưu trường cần để hiển thị/phân quyền; tôn trọng `is_active`
  (Profile/Space vô hiệu → ẩn khỏi danh sách & chặn thao tác).
- Space riêng tư: chỉ trả nội dung cho thành viên đã xác nhận qua member cache.
