# 08 — Tích hợp lunoti (thông báo)

ludiskus **không tự gửi** email/push. Khi có sự kiện đáng báo (trả lời, mention,
reaction, duyệt bài), ludiskus đẩy một *event* sang **lunoti**; lunoti dựng nội
dung và phát theo preference từng người nhận
([lunoti/docs](../../lunoti/docs/01-tong-quan.md)).

## 8.1 ludiskus là "service gửi event" của lunoti

- ludiskus có một **OAuth client** ở HipCore để gọi lunoti
  (`LUDISKUS_LUNOTI_CLIENT_ID/SECRET`, grant `client_credentials`).
- Gọi `POST {LUNOTI_API_URL}/api/v1/events` với token client; `source_service`
  của event = `ludiskus`.
- Việc gửi đi qua **outbox** trong Postgres (không gọi lunoti đồng bộ trong
  request đăng bài): worker đọc `outbox` (SKIP LOCKED), POST sang lunoti, retry/
  backoff; mất kết nối lunoti **không** làm hỏng thao tác đăng bài.

## 8.2 Đăng ký EventType (một lần, idempotent theo `code`)

Seed `db/seeds/lunoti_event_types.json`; khi `ludiskus-api` khởi động lần đầu (
hoặc lệnh `ludiskus-worker --register`), gọi `POST /api/v1/event-types` lên
lunoti:

| `code` | category | default_channels | Mô tả |
|--------|----------|------------------|-------|
| `ludiskus.topic.replied` | `discussion` | `web` | Có trả lời mới trong topic bạn theo dõi/tạo |
| `ludiskus.post.mentioned` | `discussion` | `web,email` | Bạn được @mention trong một bài |
| `ludiskus.post.reacted` | `social` | `web` | Bài của bạn nhận reaction |
| `ludiskus.topic.answered` | `discussion` | `web,email` | Câu hỏi của bạn được đánh dấu có câu trả lời |
| `ludiskus.moderation.pending` | `moderation` | `web` | Có bài chờ duyệt trong Space bạn quản |
| `ludiskus.moderation.decided` | `moderation` | `web,email` | Bài của bạn được duyệt/từ chối |

Mỗi event-type kèm **Template** (lunoti `bodies` theo kênh/locale) seed cùng lúc.

## 8.3 Bắn event khi nào

| Hành động ludiskus | Event | Người nhận (recipients) |
|--------------------|-------|--------------------------|
| Post trả lời được **publish** | `ludiskus.topic.replied` | Người theo dõi Topic (Subscription, trừ tác giả của chính post & người đã `muted`) |
| Post chứa `@mention` (publish) | `ludiskus.post.mentioned` | Các Profile được mention (đã lọc là thành viên Space) |
| Reaction lên Post | `ludiskus.post.reacted` | `author_profile_uuid` của Post (gộp/đệm để tránh spam) |
| Đánh dấu `is_answer` | `ludiskus.topic.answered` | Tác giả Topic (người hỏi) |
| Bài vào hàng chờ (pre/first/banned/report) | `ludiskus.moderation.pending` | Moderator của Space |
| approve/reject bài | `ludiskus.moderation.decided` | Tác giả bài |

Ví dụ payload đẩy lên lunoti (`POST /api/v1/events`):

```json
{
  "event_type": "ludiskus.post.mentioned",
  "idempotency_key": "post-7a1-mention-3f2",
  "data": {
    "actor": "An",
    "space": "Cộng đồng Go",
    "topic": "Cách dùng context",
    "excerpt": "…như @binh đã nói…",
    "url": "/ludiskus/s/go-vn/t/cach-dung-context#post-7a1"
  },
  "recipients": [ { "profile_uuid": "…binh-uuid…" } ],
  "channels": ["web","email"]
}
```

> `idempotency_key` dựng từ `(post_id, loại event, recipient)` để lunoti **không
> phát trùng** nếu worker retry. lunoti tự lọc theo preference kênh của người
> nhận và sinh Notification in-app + delivery — ludiskus không cần biết kênh.

## 8.4 Gộp & chống ồn

- **Reaction**: đệm theo cửa sổ (vd 5 phút) → một event "X người đã reaction"
  thay vì mỗi reaction một event (`data.count`, `data.actors[]`).
- **Reply dồn dập**: tôn trọng `Subscription.muted`; người đang mở topic không
  cần báo (tuỳ chọn, FE đánh dấu đã đọc).
- Người **tự** thao tác không nhận thông báo về hành động của chính mình.

## 8.5 Bảng `outbox`

```
outbox(id uuid PK, event_type text, idempotency_key text,
       payload jsonb, status enum(queued|sending|sent|failed),
       attempts int, max_attempts int, scheduled_at timestamptz,
       last_error text, created_at, sent_at)
UNIQUE(idempotency_key)
INDEX idx_outbox_queue(scheduled_at) WHERE status='queued'
```

Cùng mẫu hàng đợi `deliveries`/`export_jobs`: `FOR UPDATE SKIP LOCKED`, nhiều
worker song song an toàn, backoff theo `attempts`.

## 8.6 Cấu hình liên quan

| Biến | Mô tả |
|------|-------|
| `LUNOTI_API_URL` | URL `lunoti-api` (vd `http://lunoti-api:8080`) |
| `LUDISKUS_LUNOTI_CLIENT_ID/SECRET` | OAuth client để gọi lunoti |
| `LUDISKUS_REACTION_DEBOUNCE` | Cửa sổ gộp reaction (mặc định `5m`) |

> Người dùng bật/tắt kênh nhận (`web/email/…`) cho từng category **ở lunoti**
> (trang preference của lunoti trong tm). ludiskus chỉ quyết định *có theo dõi
> hay không* (Subscription) và *báo về việc gì* (event/category).
