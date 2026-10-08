# 07 — Thông báo & tương tác

## 7.1 Event lunoti

Thêm vào `db/seeds/lunoti_event_types.json`. Worker đã có `RegisterEventTypes` đăng ký
EventType + upsert Template + **tạo Rule 1:1 theo code** (`service/worker.go`, `registerRules`)
— nên chỉ cần thêm seed, **không** thêm mã đăng ký. Thiếu Rule là bẫy làm mọi thông báo hiện
"Thông báo mới" ([../08-tich-hop-lunoti.md](../08-tich-hop-lunoti.md)); mỗi event dưới đây phải
có template **cùng code**.

| Code | Category | Người nhận | Khi |
|------|----------|-----------|-----|
| `ludiskus.poll.closed` | `discussion` | C + người bỏ phiếu có `notify_result = true` (trừ ai đã mute) | Worker chốt poll (hạn chót hoặc đóng tay) và kết quả được lộ cho họ |
| `ludiskus.poll.closing_soon` | `discussion` | Cử tri hợp lệ **chưa** có Receipt | 24h (hoặc 10% thời lượng nếu ngắn hơn) trước `closes_at`, chỉ khi `who_can_vote ∈ (members, invited)` và `remind = true` |
| `ludiskus.poll.invited` | `discussion` | Người được mời | Thêm vào `poll_invitees` khi poll đã đăng (hoặc lúc đăng) |
| `ludiskus.poll.option_pending` | `moderation` | C (và M nếu poll trong Space) | Lựa chọn người dùng thêm vào hàng chờ; gom 10 phút/poll |
| `ludiskus.poll.moderated` | `moderation` | C | Poll bị ẩn/xoá/từ chối bởi người khác |

`data` của mọi event: `pollId`, `question` (đã cắt 120 ký tự), `actionUrl`, `anchorTitle` (nếu
có). `ludiskus.poll.closed` thêm `winnerLabel` **chỉ khi** kết quả được lộ cho **mọi** người
nhận (người nhận là người bỏ phiếu và `results_visibility ≠ owner_only`); còn lại template viết
"Bình chọn đã kết thúc" không kèm kết quả. lunoti thay `{{biến}}` thiếu bằng chuỗi rỗng không
báo lỗi — template phải viết sao cho câu vẫn đúng khi `winnerLabel` rỗng.

`actionUrl`: `canonicalPath` của Anchor + `#poll-{id}`, hoặc `/ludiskus/p/{id}` nếu độc lập.

### Không phát

- Poll mới **không** báo cho cả Space — một Space hoạt động sẽ ngập. Người muốn báo thì gắn
  poll vào chủ đề (chủ đề mới đã có kênh thông báo theo dõi của diễn đàn).
- Từng phiếu **không** báo cho C. Thay vào đó trang "Bình chọn của tôi" hiện số người tham gia.
- Anchor `unverified` ⇒ không event nào (như LuComment).

### Số lượng người nhận

`poll.closed` có thể có hàng nghìn người nhận. Chia lô 500 Profile/event, `idempotency_key =
poll:closed:{pollId}:{lô}`. Khoá idempotency của outbox là `UNIQUE` vĩnh viễn — **không** đưa
thời điểm hay số lần đóng vào khoá theo kiểu "gộp theo đồng hồ". Poll mở lại rồi đóng lần hai
dùng `poll:closed:{pollId}:r{reopen_count}:{lô}` để không bị khoá lần một nuốt mất.

`closing_soon` một lần cho mỗi `(poll, reopen_count)`, cờ `polls.reminded_at` chặn gửi lặp; worker
ghi cờ và outbox **cùng transaction**.

## 7.2 Theo dõi & tắt tiếng

Không thêm bảng theo dõi. `poll_voters.notify_result` là cờ duy nhất (bật mặc định, tắt từ menu
poll). C luôn nhận `poll.closed`. Tắt tiếng toàn bộ thông báo bình chọn = tuỳ chọn category trong
lunoti — không làm lại ở ludiskus.

## 7.3 Interaction Platform (like / bookmark / share)

Seed của `lufami` **đã** khai `ludiskus × poll` (like, dislike, reaction
agree/disagree/insightful/confused, bookmark, share). Việc phía ludiskus:

1. Thêm `case "poll"` vào `service.InteractionContext`:
   - Poll gắn kết ⇒ `visibility`, `spaceUuid`, `state` lấy từ Anchor (`comment_targets`); poll
     độc lập ⇒ từ chính poll (`unlisted` khai thành `authenticated` — Interaction Platform không
     có khái niệm "không liệt kê", và khai `public` sẽ cho đọc công khai qua link share).
   - `owner` = C (`profile`) hoặc Space (`author_kind='space'`).
   - `state`: `published`/`closed` ⇒ `active`; `hidden`/`pending`/`rejected`/`draft` ⇒ `blocked`;
     `deleted` ⇒ `gone`.
   - `canonicalPath` = như `actionUrl` §7.1; `title` = `question`.
2. Đẩy `UpsertInteractionResources` khi đăng/đổi visibility/ẩn; `Invalidate` khi xoá (hàm có sẵn
   trong `internal/hipt`), best-effort ngoài transaction.
3. Đề nghị `lufami` **tắt `dislike`** cho `ludiskus × poll`: dislike một câu hỏi bình chọn là
   kênh thứ hai để "bỏ phiếu chống" song song với chính poll. Ghi ở [11 §11.6](11-tich-hop-service.md)
   làm việc phối hợp — không chặn LuPoll.

## 7.4 Bình luận dưới poll

Poll độc lập có trang riêng `/ludiskus/p/:id` ⇒ nhúng `<CommentThread resource={{service:
"ludiskus", type:"poll", id}}>`. Cần:

- hàng `comment_policies` cho `('ludiskus','poll')` (thiếu hàng ⇒ bình luận tắt — hành vi hiện tại
  của `comment_policy.go`);
- `case "poll"` của §7.3 (resolver ludiskus là nội bộ, dùng chung).

Poll gắn kết **không** có luồng bình luận riêng — thảo luận diễn ra ở Anchor (chủ đề, bình luận
mẹ, trang phim). Trang `/ludiskus/p/:id` của poll gắn kết chuyển hướng về `canonicalPath#poll-{id}`
của Anchor.

Poll trong bình luận dưới `ludiskus:poll:*` bị chặn ([03 §3.3](03-mo-hinh-mien.md)).
