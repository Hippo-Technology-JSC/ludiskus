# 04 — Gắn kết & phân quyền

## 4.1 Hợp đồng với nội dung được gắn — không có gì mới

Service muốn có poll trên nội dung của mình dùng **đúng** hợp đồng của LuComment
([../comment/04-hop-dong-resource.md](../comment/04-hop-dong-resource.md)):

- hàng trong `comment_services` (registry chung; `verify_mode`, `base_url`, `oauth_client_id`);
- `GET /api/v1/s2s/resource-context/{type}/{id}` **hoặc** `interaction-context` (resolver tự dò,
  ghi `context_path`);
- khi xoá nội dung / đổi visibility: `POST /api/v1/s2s/comments/targets/invalidate` (hoặc đẩy
  `targets`) — **cùng một lời gọi** làm tươi cho cả bình luận và bình chọn (QĐ-P3).

Trường mới duy nhất, tuỳ chọn, trong `capabilities` của response:

```json
"capabilities": { "comment": true, "poll": true, "pollCreate": "owner" }
```

| Khoá | Ý nghĩa | Thiếu |
|------|---------|-------|
| `poll` | `false` ⇒ cấm poll trên nội dung này dù policy bật | Coi như **không khai** (không phải `false`) — giống `comment` ([../comment/04 §4.3](../comment/04-hop-dong-resource.md)) |
| `pollCreate` | Siết `who_can_create`: `owner` · `staff` · `none` | Không siết |

Chỉ **thu hẹp** (tầng 4 của §4.6). Thiếu khoá thì policy quyết định — 17 service đang tích hợp
LuComment không phải sửa gì để dùng LuPoll.

## 4.2 `ensureTarget` cho Anchor

Tái dùng nguyên `ensureCommentTarget` (`service/comment_target.go`) — bao gồm cả ba chế độ
`strict`/`optimistic`/`trust` và quy tắc **Target `unverified` ⇒ `private`, chỉ người tạo
thấy**. Hệ quả cho poll:

| Tình huống | Poll |
|-----------|------|
| Anchor `active` | Bình thường |
| Anchor `unverified` (resolver lỗi, chế độ `optimistic`) | Chỉ người tạo poll thấy và chưa ai bỏ phiếu được; `publish` trả `409 ANCHOR_UNVERIFIED`. Worker verify của LuComment làm tươi; xong là poll mở |
| Anchor `unverified`, `strict` | Không tạo được poll: `503 RESOURCE_RESOLVER_UNAVAILABLE` |
| Anchor `gone` | `410 RESOURCE_GONE` mọi route; worker đóng poll ([03 §3.4](03-mo-hinh-mien.md)) |
| Anchor `blocked` | `403 RESOURCE_BLOCKED` |

Không có worker verify riêng cho poll. Ticker `VerifyCommentTargets` (30s) đã quét mọi
`comment_targets` cũ hoặc `unverified` — **phải sửa một chỗ**: điều kiện ưu tiên hiện là
`comment_count > 0`; thêm `OR EXISTS (SELECT 1 FROM polls WHERE anchor_target_id = t.id AND
status IN ('published','pending'))`, nếu không Target chỉ có poll mà không có bình luận sẽ bị
làm tươi chậm nhất ([14 LP-3.3](14-cong-viec-chi-tiet.md)).

## 4.3 Quyền đọc — `ensurePollReadable`

Hàm **duy nhất** quyết định quyền đọc poll. Mọi route đọc — kể cả `results`, `voters`,
`export`, summary batch — đi qua nó.

```
1. poll tồn tại?                                     không          ⇒ 404 POLL_NOT_FOUND
   poll.status = 'deleted'                                          ⇒ 410 POLL_DELETED
2. nếu có Anchor:
     target := ensureCommentTarget(anchor_ref)        (§4.2)        ⇒ 410 / 403 / 503
     ensureCommentReadable(target, viewer)  ← CÙNG HÀM của LuComment, bỏ qua bước policy bình luận
   nếu độc lập: theo poll.visibility
     public | unlisted ⇒ mọi người (unlisted: không liệt kê, không tìm, không đọc công khai qua danh sách)
     authenticated     ⇒ có ProfileUUID
     space             ⇒ ident.IsMember(poll.space_uuid, viewer)
     private           ⇒ viewer = created_by, hoặc viewer ∈ poll_invitees
3. status ∈ (draft, pending, rejected) và viewer không phải C (và không phải M khi pending) ⇒ 404
4. status = 'hidden' (hoặc Anchor gone/blocked) và viewer không phải O/M         ⇒ 404
5. policy hợp nhất ⇒ capabilities; capabilities.enabled = false                   ⇒ 403 POLL_DISABLED
```

**Chú ý bước 2 khi Anchor là ludiskus.** `InteractionContext` cho `topic/post/reply` khai
`visibility = space` (forum riêng) hoặc `public` — ở **mức Space**. Hôm nay đúng, vì ACL theo
Board (`0012`) chỉ điều khiển **quyền tạo chủ đề và trả lời**, không điều khiển quyền xem. Nếu
sau này Board có quyền xem riêng, `InteractionContext` phải phản ánh nó, nếu không poll (và cả
bình luận, like) trong Board kín sẽ lộ cho cả Space. Ghi ở đây để người làm ACL xem theo Board
nhớ ra chỗ này.

`connections` bị siết thành `private` như LuComment ([../comment/06 §6.2](../comment/06-phan-quyen.md)) —
**trừ** khi LuDoku-style S2S tới `lufami` được bổ sung chung cho cả hai phân hệ; LuPoll không
tự làm riêng.

## 4.4 Quyền bỏ phiếu — `ensurePollVotable`

Chạy **sau** `ensurePollReadable`:

```
6.  có ProfileUUID                                                        không ⇒ 401
7.  effective state = open                       scheduled ⇒ 409 POLL_NOT_OPEN · closed ⇒ 409 POLL_CLOSED
8.  who_can_vote:
      viewers ⇒ ✓ (đã đọc được)
      members ⇒ ident.IsMember(space của Anchor hoặc của poll, viewer)    không ⇒ 403 NOT_ELIGIBLE
      invited ⇒ viewer ∈ poll_invitees                                     không ⇒ 403 NOT_ELIGIBLE
9.  capabilities.vote (policy) và Anchor.capabilities.poll ≠ false        không ⇒ 403 POLL_DISABLED
10. tài khoản mới (< LUDISKUS_POLL_NEW_PROFILE_HOURS) và policy.new_profile_can_vote = false ⇒ 403 NOT_ELIGIBLE
11. rate limit                                                            ⇒ 429 RATE_LIMITED
```

Bước 7 ở đây chỉ để trả lỗi đẹp. Kiểm **có hiệu lực** nằm trong câu `UPDATE polls … WHERE`
đầu transaction (QĐ-P10) — giữa bước 7 và lúc ghi, poll có thể vừa đóng.

## 4.5 Quyền tạo và gắn

```
ensurePollCreatable(anchor | standalone, viewer):
  độc lập cá nhân    ⇒ policy (ludiskus, standalone).who_can_create ∈ (authenticated)
  độc lập trong Space⇒ thành viên Space; policy.who_can_create = staff_only ⇒ phải là M
                        và space_forums.settings.poll.enabled ≠ false (Space tắt được bình chọn)
  gắn vào Resource   ⇒ ensurePollReadable trên Anchor (bước 2) rồi:
      who_can_create (hợp nhất với Anchor.capabilities.pollCreate):
        owner        ⇒ viewer = target.owner_id (owner_type profile) hoặc là owner/admin Space (owner_type space)
        participants ⇒ viewer đọc được Anchor  (ví dụ: ai cũng thêm được poll dưới một phim)
        staff        ⇒ M của target.space_uuid, hoặc chỉ qua S2S
        none         ⇒ chỉ S2S
  số poll trên Anchor < policy.max_per_anchor                         ⇒ 409 POLL_LIMIT
  rate limit tạo poll                                                 ⇒ 429
```

**Mặc định cho Anchor là `owner`.** Tác giả chủ đề gắn poll vào chủ đề của mình, tác giả bình
luận gắn poll vào bình luận của mình. Resolver của ludiskus khai `owner` của
`comment`/`topic`/`post`/`reply` là tác giả (đã có trong `service/interaction.go`), nên luật
này đúng mà không cần đọc bảng forum.

### Hai luồng gắn

**(a) Gắn ngay khi tạo** — người tạo Anchor đã tồn tại (thêm poll vào bộ phim, vào dự án):

```
POST /polls { anchor: {service, type, id}, … }  ⇒ poll gắn sẵn, status draft|published
```

**(b) Nháp rồi gắn** — Anchor **chưa** tồn tại lúc soạn (soạn chủ đề mới kèm poll, soạn bình
luận kèm poll):

```
1. POST /polls { draft: true, … }                       ⇒ poll draft, không Anchor, chỉ C thấy
2. POST /topics … { pollIds: [id] }                     ⇒ ludiskus gắn trong CÙNG transaction tạo chủ đề
   POST /comments/r/…/items { pollIds: [id] }           ⇒ như trên cho bình luận
   hoặc với service ngoài: tạo nội dung, rồi
   POST /polls/{id}/attach { anchor } (người dùng)  hoặc  POST /s2s/polls/{id}/attach (service)
```

Ràng buộc của bước gắn: poll phải `draft`, `created_by` = người gắn, chưa có Anchor; người gắn
qua được `ensurePollCreatable` trên Anchor. Gắn xong, poll **không** đổi Anchor được nữa
(đổi Anchor = đổi người được xem = phá cam kết với người đã bỏ phiếu).

Nháp không được gắn sau `LUDISKUS_POLL_DRAFT_TTL` (mặc định 24h) bị worker xoá cứng (chưa ai
thấy nó, không có phiếu).

Trong ludiskus, bước gắn ở luồng (b) đi qua interface `domain.PollAttacher`
(`AttachDrafts(ctx, tx, profile, ref, pollIDs) error`) để file forum/comment không biết kiểu
`Poll` (ranh giới [02 §2.4](02-kien-truc.md)). Bình luận/chủ đề vào hàng chờ kiểm duyệt ⇒ poll
gắn với `status='pending'` và mở cùng lúc Anchor được duyệt.

## 4.6 Policy — hợp nhất 4 tầng

Cùng cơ chế với `comment_policies` (tái dùng hàm deep-merge + `restrict` của
`service/comment_policy.go`, tham số hoá theo kiểu config):

1. `domain.DefaultPollPolicy` (hằng trong code, `enabled: false`).
2. `poll_policies` với `anchor_type = '*'` của `anchor_service`.
3. `poll_policies` với `anchor_type` khớp chính xác (poll độc lập dùng `('ludiskus','standalone')`).
4. `capabilities` của Anchor (`poll`, `pollCreate`) — **chỉ thu hẹp**.

Với Anchor là **bình luận**, thêm tầng 5 (QĐ-P17): khoá `poll` trong policy bình luận hiệu lực
của Target chứa bình luận đó. Tắt ⇒ `POLL_DISABLED`.

```json
{
  "enabled": true,
  "who_can_create": "owner",
  "kinds": ["single", "multiple", "ranked", "scale", "schedule"],
  "identity_modes": ["public", "owner_only", "anonymous", "secret"],
  "max_per_anchor": 1,
  "max_options": 20,
  "max_duration_days": 90,
  "default_identity_mode": "public",
  "default_results_visibility": "after_vote",
  "allow_user_options": true,
  "moderation_mode": "post",
  "public_read": false,
  "new_profile_can_vote": true,
  "rate_limit": { "create_per_hour": 10, "votes_per_minute": 30 }
}
```

| Khoá | Ghi chú |
|------|---------|
| `kinds`, `identity_modes` | Hợp nhất tầng 4 lấy **giao** |
| `max_per_anchor` | Số poll **chưa xoá** trên một Anchor; `min` khi hợp nhất. Chủ đề diễn đàn: 1; tài liệu luxtory: 10 |
| `moderation_mode` | `none` · `post` · `pre` — poll mới (độc lập) đi hàng chờ khi `pre`; poll trong chủ đề/bình luận **theo trạng thái của Anchor** |
| `public_read` | Như LuComment: chỉ có hiệu lực khi visibility hiệu lực = `public` và Anchor `active` |

Cache: trong process 60s + khoá version Redis `poll:pol:v` (`INCR` khi quản trị sửa).

## 4.7 Ma trận hành động

`C` người tạo · `O` chủ Anchor · `M` moderator Space · `S` service sở hữu Anchor (S2S) ·
`V` người đã bỏ phiếu · `U` người dùng khác đọc được poll.

| Hành động | C | O | M | S | V | U |
|-----------|:-:|:-:|:-:|:-:|:-:|:-:|
| Đọc poll | ✓ | ✓ | ✓ | ✓ | ✓ | §4.3 |
| Bỏ / đổi / rút phiếu | §4.4 | §4.4 | §4.4 | ✗ | §4.4 | §4.4 |
| Sửa nội dung, cấu hình | ✓ theo [03 §3.5](03-mo-hinh-mien.md) | ✗ | ✗ | ✗ | ✗ | ✗ |
| Thêm lựa chọn | ✓ | ✗ | ✗ | ✗ | nếu `allow_user_options` | nếu `allow_user_options` |
| Đóng sớm / gia hạn | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ |
| Mở lại | ✓ | ✗ | ✓ | ✓ | ✗ | ✗ |
| Ẩn / khôi phục | ✗ | ✓ | ✓ | ✓ | ✗ | ✗ |
| Xoá | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ |
| Duyệt lựa chọn người dùng thêm | ✓ | ✓ | ✓ | ✓ | ✗ | ✗ |
| Mời / bỏ mời (`invited`) | ✓ | ✗ | ✗ | ✓ | ✗ | ✗ |
| Xem kết quả | luôn | theo `results_visibility` | theo `results_visibility` | luôn | theo `results_visibility` | theo `results_visibility` |
| Xem ai chọn gì | `public`, `owner_only` | `public` | `public` | `public` | `public` | `public` |
| Xem ai đã tham gia (Receipt) | ✓ (mọi mức) | ✗ | ✓ nếu `members` | ✓ | ✗ | ✗ |
| Xuất CSV | ✓ | ✗ | ✗ | ✓ | ✗ | ✗ |
| Báo cáo | ✗ | ✓ | ✓ | ✗ | ✓ | ✓ |

Ba điểm cố ý:

- **`results_visibility = owner_only` nghĩa là C (và S), không phải M.** Moderator Space là
  người giữ trật tự, không phải người được biết kết quả mà người tạo muốn giữ.
- **`owner_only` (danh tính) cũng chỉ C.** Chủ Anchor (O) không phải người tổ chức poll; tác giả
  chủ đề không thấy danh tính phiếu trong poll của người khác dưới chủ đề đó.
- **`S` xem được kết quả và xuất** vì service sở hữu thường cần kết quả để hành động (nghị quyết
  `lurp`). Nhưng `S` **cũng không** thấy ai chọn gì ở `anonymous`/`secret` — không có route nào
  trả thông tin đó, kể cả S2S.

## 4.8 Đại diện Space và poll của service

- `author_kind = 'space'`: body có `actAsSpaceUuid`, người gọi phải là owner/admin của Space theo
  `space_member_cache`; `created_by` vẫn là người thật (audit) — như bình luận đại diện Space.
  Quyền C được trao cho **mọi** owner/admin của Space đó, không chỉ người bấm tạo.
- `author_kind = 'service'`: tạo qua `POST /s2s/polls` trên Anchor **của chính service đó**
  (`ref.service` = service của token, kiểm bằng `CommentServiceForClient` — đã có). Quyền C
  thuộc về service (S). Có thể kèm `actorProfileUuid` để audit.
