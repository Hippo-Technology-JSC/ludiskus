# 09 — Thông báo, theo dõi & hộp thư

## 9.1 Nguyên tắc

1. `ludiskus` **không** gửi email/push. Nó ghi `outbox`; `ludiskus-worker` đẩy sang **lunoti**
   (`POST /api/v1/events`) — đúng đường đã chạy cho forum ([08](../08-tich-hop-lunoti.md)).
2. **Deep-link là trang của service sở hữu**, không phải trang `ludiskus`:
   `{canonical_path}#comment-{id}`. Thiếu `canonical_path` ⇒ dùng permalink
   `/ludiskus/c/{commentId}` (trang này tự chuyển hướng khi có `canonical_path`).
3. **Gom nhóm là bắt buộc, không phải tối ưu** (QĐ-11). Một Thread nóng phải sinh **một** thông
   báo mỗi 5 phút cho mỗi người, không phải mỗi bình luận một cái.
4. Bình luận `pending` **không** phát gì cho tới khi `approve`.
5. Không bao giờ thông báo cho **chính người gây ra hành động**.

## 9.2 Năm event-type

Thêm vào [`db/seeds/lunoti_event_types.json`](../../backend/db/seeds/lunoti_event_types.json)
(worker `RegisterEventTypes` đã tự đăng ký lúc khởi động):

| `event_type` | Người nhận | Kênh mặc định | Gom nhóm |
|--------------|-----------|---------------|----------|
| `ludiskus.comment.created` | Chủ nội dung (`policy.notify.owner`) | `web` | ✓ theo Target |
| `ludiskus.comment.replied` | Tác giả bình luận cha + participant không mute (`policy.notify.participants`) | `web` | ✓ theo (người nhận, Target) |
| `ludiskus.comment.mentioned` | Profile được mention trong scope | `web`, `email` | ✗ (mention là cá nhân, không gom) |
| `ludiskus.comment.pending` | Moderator Space (hoặc bỏ qua nếu không có) | `web` | ✓ theo (Space) — "n bình luận chờ duyệt" |
| `ludiskus.comment.moderated` | Tác giả bình luận bị ẩn/từ chối/xoá bởi người khác | `web`, `email` | ✗ |

Biến trong template: `actor`, `count`, `others`, `resourceTitle`, `serviceName`, `excerpt`,
`url`, `decision`, `note`. Mẫu tiếng Việt:

```
comment.created   "{{actor}} đã bình luận về \"{{resourceTitle}}\""
comment.replied   count == 1 ⇒ "{{actor}} đã trả lời bình luận của bạn"
                  count  > 1 ⇒ "{{actor}} và {{others}} người khác đã trả lời trong \"{{resourceTitle}}\""
comment.mentioned "{{actor}} đã nhắc đến bạn: {{excerpt}}"
comment.pending   "{{count}} bình luận chờ duyệt trong {{spaceName}}"
comment.moderated "Bình luận của bạn đã bị {{decision}}" + "{{note}}"
```

## 9.3 Buffer gom nhóm

```sql
comment_notify_buffer(
  id bigserial, event_type text, recipient_profile_uuid uuid, target_id uuid,
  comment_id uuid, actor_profile_uuid uuid, occurred_at timestamptz,
  flush_after timestamptz, PRIMARY KEY (id)
)
UNIQUE (event_type, recipient_profile_uuid, target_id, comment_id)
```

Đường ghi (trong **cùng transaction** với `INSERT comments`, hoặc với `approve`):

```
với mỗi người nhận:
  INSERT ... ON CONFLICT DO NOTHING
  flush_after = COALESCE(
     (SELECT min(flush_after) FROM buffer WHERE event_type,recipient,target khớp),
     now() + NOTIFY_DEBOUNCE)          -- 5 phút, giữ mốc của lô đầu tiên
```

Worker `FlushCommentNotify` mỗi 10s:

```
1. SELECT ... WHERE flush_after <= now() ORDER BY flush_after FOR UPDATE SKIP LOCKED LIMIT 200
2. gom theo (event_type, recipient, target)
3. mỗi nhóm ⇒ 1 hàng outbox:
     data = { actor: <tên người mới nhất>, count: n, others: n-1,
              resourceTitle, serviceName, excerpt: <bình luận mới nhất>, url }
     idempotency_key = "cmt:{event}:{recipient}:{target}:{max(comment_id)}"
4. DELETE các hàng đã gom (cùng transaction với INSERT outbox)
```

`idempotency_key` chứa `max(comment_id)` để lần gom sau (đợt bình luận mới) có khoá khác —
nếu dùng khoá theo cửa sổ thời gian thì lunoti sẽ bỏ đợt thứ hai.

Mention **không** đi qua buffer: ghi outbox trực tiếp trong transaction tạo bình luận (nhưng
vẫn `idempotency_key = "cmt:mention:{comment_id}:{recipient}"`).

## 9.4 Ai được thông báo — thuật toán fan-out

```
recipients(comment) =
    (chủ nội dung nếu policy.notify.owner)                       → comment.created
  ∪ (tác giả comment.parent nếu có)                              → comment.replied
  ∪ (participant của target, reason ≠ 'mentioned', NOT muted)    → comment.replied
  ∪ (profile trong comment_mentions của comment)                 → comment.mentioned
  − { author_profile_uuid }                                       (không tự thông báo)
  − { profile có comment_participants.muted = true }
  − { profile không còn quyền đọc target }   ← kiểm ở worker lúc flush, KHÔNG lúc ghi buffer
```

Điểm cuối cùng quan trọng: giữa lúc ghi buffer và lúc flush (tới 5 phút) người nhận có thể đã
bị loại khỏi Space. Worker **phải** kiểm lại `ensureReadable` cho từng người nhận trước khi
đẩy outbox; không kiểm là rò rỉ `excerpt` nội dung riêng tư qua email.

Một người vừa được mention vừa là participant ⇒ chỉ nhận `comment.mentioned` (ưu tiên cụ thể
hơn), việc lọc trùng làm ở bước gom.

## 9.5 Theo dõi, mute, đã đọc

| Endpoint | Việc |
|----------|------|
| `PUT /comments/r/{ref}/subscription` `{ "muted": false }` | Theo dõi thủ công (`reason='manual'`) hoặc mute (giữ hàng, `muted=true`) |
| `DELETE /comments/r/{ref}/subscription` | Bỏ theo dõi thủ công. Participant `authored` **không** xoá được (đã có nội dung trong Thread) — chỉ mute được |
| `POST /comments/r/{ref}/read` | `last_read_at = now()` |
| `GET /comments/inbox?unread=1&cursor=` | Thread có `last_comment_at > last_read_at`, sắp theo `last_comment_at DESC` |
| `GET /comments/unread-count` | Một số cho badge; cache Redis `cmt:unread:{profile}` TTL 30s, xoá khi có bình luận mới trong Thread người đó tham gia |

Tự động thành participant: viết (`authored`), bị trả lời (`replied`), bị mention
(`mentioned`), là chủ nội dung lúc Target sinh (`owner`). Chỉ đọc thì **không**.

## 9.6 Trang "Bình luận của tôi"

`/ludiskus/comments` gồm ba tab, tất cả xuyên service:

1. **Hộp thư** — `GET /comments/inbox`: Thread có phản hồi mới; mỗi dòng: icon service
   (`lukon`), `resourceTitle`, trích bình luận mới nhất, số chưa đọc, mở bằng `canonical_path`.
2. **Bình luận của tôi** — `GET /comments/mine?q=&service=&status=`: mọi bình luận đã viết, kể
   cả `pending`/`hidden`/`rejected` (kèm lý do) — đây là nơi người dùng hiểu **vì sao** bình
   luận của mình không hiện.
3. **Cần duyệt** — chỉ hiện nếu là moderator của ít nhất một Space: hàng chờ
   ([07 §7.5](07-kiem-duyet.md)).

## 9.7 Đồng bộ sang Interaction Platform

Mỗi bình luận `published` là một resource của Interaction Platform
(`ludiskus:comment:{id}` — QĐ-5). Tái dùng `internal/hipt` đang có:

| Khi | Việc |
|-----|------|
| Bình luận `published` (tạo mới hoặc approve) | `hipt.UpsertInteractionResources` (best-effort, goroutine, không chặn response) với `visibility` = của Target, `canonicalPath = {canonical_path}#comment-{id}`, `owner = tác giả` |
| Bình luận `hidden`/`deleted`/`rejected` | `hipt.InvalidateInteractionResources(reason)` |
| Target đổi visibility hoặc `state='gone'` | Invalidate **theo lô 100** cho mọi bình luận `published` của Target (mẫu `invalidateInteractionRefs` đã có) |

Và `ludiskus` phải **mở rộng** provider của mình:
`GET /api/v1/s2s/interaction-context/comment/{id}` — thêm nhánh `case "comment"` vào
[`service.InteractionContext`](../../backend/internal/service/interaction.go), trả `owner` =
tác giả bình luận, `visibility` = của Target, `state` suy từ `comments.status`
(`published`→`active`, `deleted`→`gone`, còn lại→`blocked`), `canonicalPath` như trên.
`lufami` **không** cần đổi gì: nó đã gọi đúng endpoint đó với `type` bất kỳ.

Interaction ngừng hoạt động (`LUFAMI_API_URL` rỗng) ⇒ bình luận vẫn chạy đủ, chỉ mất nút
like/reaction. Đây là phụ thuộc **mềm**, kiểm bằng test.
