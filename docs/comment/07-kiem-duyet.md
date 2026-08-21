# 07 — Kiểm duyệt bình luận

Tái dùng **nguyên khối** cơ chế kiểm duyệt của forum ([04](../04-kiem-duyet.md)): bảng
`reports`, `moderation_items`, `space_moderators`, và bốn chế độ. Khác biệt duy nhất: nguồn
cấu hình không phải `space_forums.moderation_mode` mà là **policy của LuComment**, vì Target
có thể **không thuộc Space nào**.

## 7.1 Ai kiểm duyệt cái gì

| Target | Ai vào được hàng chờ |
|--------|----------------------|
| Có `space_uuid` | Moderator của Space đó (`owner`/`admin` từ `space_member_cache` + `space_moderators`) — hàng chờ hiện trong `/ludiskus/comments?space=…` |
| Không có `space_uuid` | **Không có moderator người**; hàng chờ chỉ giải quyết được qua `POST /s2s/comments/{id}/moderate` của service sở hữu, hoặc bởi chủ nội dung (ẩn/xoá, không phải approve/reject) |

Hệ quả quan trọng: **service nào đặt `moderation_mode = pre`/`first_comment` cho Resource
không có `space_uuid` thì phải tự cài đường duyệt qua S2S**, nếu không bình luận sẽ nằm mãi ở
`pending`. Đây là mục kiểm bắt buộc trong checklist tích hợp ([13 §13.1](13-tich-hop-service.md))
và là một trong các cảnh báo mà `POST /admin/comment-policies` trả về khi lưu policy như vậy.

## 7.2 Bốn chế độ

| `moderation_mode` | Hành vi khi viết |
|-------------------|------------------|
| `none` | `published` ngay; chỉ xử lý hậu kỳ qua báo cáo |
| `post` | `published` ngay, nhưng từ cấm sinh `moderation_item(source=banned_word)` để xem lại; báo cáo + tự ẩn vẫn chạy |
| `pre` | **Mọi** bình luận `pending`, không ai ngoài tác giả + moderator thấy, **không** thông báo |
| `first_comment` | Bình luận **đầu tiên** của Profile đó **trong Target đó** vào `pending`; đã có ≥1 bình luận `published` trong Target ⇒ đăng thẳng |

Bình luận của **moderator Space / chủ nội dung / service** luôn bỏ qua hàng chờ (trust staff),
sao y luật forum.

> `first_comment` của forum tính theo **Space**; của LuComment tính theo **Target**. Cố ý:
> một Target là một cuộc hội thoại nhỏ, và ngưỡng theo Space sẽ vô nghĩa với Target không
> thuộc Space nào. Truy vấn: `EXISTS(SELECT 1 FROM comments WHERE target_id=$1 AND
> author_profile_uuid=$2 AND status='published')` — có index phục vụ đúng việc này
> ([10 §10.3](10-database.md)).

## 7.3 Từ cấm

Nguồn theo `policy.banned_words_source`:

| Giá trị | Nguồn |
|---------|-------|
| `space` | `space_forums.banned_words` của `target.space_uuid` (Space chưa bật forum ⇒ danh sách rỗng, **không** lỗi) |
| `service` | `comment_policies.config.banned_words` (mảng chuỗi) |
| `none` | Không lọc |

So khớp **không phân biệt hoa/thường và không phân biệt dấu**, dùng lại `unaccent` như forum;
hàm `matchesBanned` đã có trong [`internal/service/moderation.go`](../../backend/internal/service/moderation.go)
— tái dùng, không viết lại. So khớp trên `body_md` **sau** khi chuẩn hoá khoảng trắng và bỏ
markup cơ bản, để `s p a m` và `**spam**` không lọt.

Trùng từ cấm:
- chế độ `none`/`post` ⇒ vẫn `published` + `moderation_item(source=banned_word)`;
- chế độ `pre`/`first_comment` ⇒ `pending` với `moderation_source='banned_word'` (ưu tiên hơn
  `pre`, để moderator biết lý do thật).

## 7.4 Báo cáo & tự ẩn

1. `POST /comments/items/{id}/report` với `reason ∈ (spam, abuse, offtopic, sexual, violence,
   private_info, other)` + `note`. Ghi vào `reports` với `target_type='comment'` (QĐ-12).
2. Một người báo cáo **một** bình luận **một** lần: `UNIQUE (target_type, target_id,
   reporter_profile_uuid) WHERE status='open'`. Gọi lại ⇒ `204`, không tạo thêm. Index này áp cho
   **cả** báo cáo cũ của topic/post ⇒ phải dọn hàng trùng trong migration ([10 §10.6](10-database.md)).
3. Số báo cáo mở đạt `policy.report_auto_hide_threshold` (>0) ⇒ `status='hidden'`,
   `moderation_source='auto_hide'`, tạo `moderation_item(source=auto_hide)`, **giảm** số đếm,
   thông báo tác giả **không** gửi (tránh biến tự ẩn thành công cụ quấy rối); moderator/service
   được thông báo.
4. Không có moderator (Target không thuộc Space) và không có tích hợp S2S ⇒ item nằm chờ;
   dashboard quản trị hiện cảnh báo "n item mồ côi > 7 ngày".
5. Moderator xử lý: `approve` (bình luận trở lại `published`, mọi report → `dismissed`) hoặc
   `reject` (→ `rejected`, report → `resolved`).

Bình luận **bị báo cáo bởi chính tác giả** ⇒ `422`. Chủ nội dung báo cáo được (và thường ẩn
luôn, nhanh hơn).

## 7.5 Hàng chờ

`GET /comments/moderation/queue?space={uuid}&service={code}&state=pending&cursor=`

Mỗi item trả kèm **ngữ cảnh đủ để quyết định mà không phải mở trang khác**: nội dung bình
luận (`body_html`), tác giả (tên/avatar/tuổi Profile/số bình luận bị từ chối trước đó), Target
(`title` + `canonical_path` + icon service), lý do (`source`), số báo cáo, và bình luận cha nếu
là trả lời.

`POST /comments/moderation/{item}/approve` · `/reject` (body `{ "note": "…" }`).

Khi `approve` một bình luận `pending`: **lúc này** mới ghi `comment_notify_buffer` cho người
nhận — không phải lúc tạo. Đây là điểm sai kinh điển (thông báo về bài chưa duyệt); luật đã
được nêu ở [04 §4.6](../04-kiem-duyet.md) của forum và áp y nguyên.

Mọi quyết định ghi `decided_by`, `decided_at`, `note` vào `moderation_items` **và** một hàng
`comment_audit_logs`.

## 7.6 Chống lạm dụng nâng cao (GĐ7)

Bảng `comment_abuse_flags(profile_uuid, signal, evidence jsonb, state, created_at)` với các
tín hiệu tính bằng SQL trong worker, **không** tự động phạt — chỉ đưa lên dashboard:

| `signal` | Cách phát hiện |
|----------|----------------|
| `burst` | > 20 bình luận/10 phút trên ≥ 5 Target khác nhau |
| `same_body` | Cùng `body_hash` trên ≥ 3 Target trong 1 giờ |
| `link_spam` | ≥ 3 bình luận có liên kết ngoài trong 10 phút từ Profile < 7 ngày tuổi |
| `report_magnet` | ≥ 5 bình luận bị `auto_hide` trong 7 ngày |
| `report_abuse` | Gửi ≥ 20 báo cáo trong 7 ngày mà ≥ 80% bị `dismissed` |

Hành động do người quyết: siết rate limit cho Profile (`cmt:rl:override:{profile}` trong
Redis + hàng trong `comment_abuse_flags` với `state='throttled'`) hoặc chuyển Profile sang
chế độ `pre` toàn hệ (`state='pre_moderated'`). **Không** ban tự động.

## 7.7 Nhật ký

`comment_audit_logs(id, actor, actor_profile_uuid, action, target_id, comment_id, reason,
detail jsonb, created_at)` — `actor` là `profile:{uuid}` | `service:{code}` | `system`.

Ghi log cho: hide/restore/delete bởi người khác tác giả, approve/reject, pin/unpin, lock/unlock
luồng, đổi policy/registry, đối soát sửa số đếm, xử lý cờ abuse. **Không** ghi log cho hành
động đọc (khối lượng vô ích) và **không** ghi `body_md` vào log (đã có trong `comments`).

Giữ `LUDISKUS_COMMENT_AUDIT_RETENTION_DAYS` (mặc định 365), worker dọn theo ngày.
