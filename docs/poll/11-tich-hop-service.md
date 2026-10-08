# 11 — Tích hợp service

## 11.1 Checklist cho service muốn có bình chọn trên nội dung của mình

Nếu service **đã tích hợp LuComment**, phần lớn đã xong:

- [ ] Có hàng trong `comment_services` (`is_active`, `base_url`, `oauth_client_id`). *17 service
      đã được seed ở `0003`; `lurp` và các service mới **chưa** có.*
- [ ] Cài `GET /api/v1/s2s/resource-context/{type}/{id}` (hoặc đã có `interaction-context`). *Đo
      2026-10-08: có router S2S ở `ludiskus`, `lufami`, `lukode`, `lukolek`, `lumuse`, `luprojet`,
      `luxtory`.*
- [ ] Gọi `POST /s2s/comments/targets/invalidate` khi xoá nội dung / siết visibility — **cùng một
      lời gọi** phục vụ cả bình luận và bình chọn.
- [ ] (Tuỳ chọn) khai `capabilities.poll` / `pollCreate` để siết theo từng nội dung.
- [ ] Xin quản trị ludiskus thêm hàng `poll_policies` cho `(service, type)` — **thiếu hàng ⇒ tắt**.
- [ ] Nhúng `<PollList resource={{service, type, id}} />` vào trang chi tiết.
- [ ] Nếu cần tạo poll thay người dùng hoặc đọc kết quả để hành động: dùng nhóm `/s2s/polls`
      bằng client-credentials của chính service (đã có cho LuComment).

`connections` vẫn bị siết thành `private` ([04 §4.3](04-gan-ket-va-phan-quyen.md)) — nội dung
"bạn bè xem được" chỉ chủ nội dung thấy poll.

## 11.2 Policy khởi tạo (seed `poll_policies.json`)

| Service | Type | `who_can_create` | `max_per_anchor` | `kinds` | Ghi chú |
|---------|------|------------------|------------------|---------|---------|
| `ludiskus` | `standalone` | `authenticated` | — | tất cả | `moderation_mode: post`, `public_read: true` |
| `ludiskus` | `topic`, `post` | `owner` | 1 | tất cả | Moderation theo Anchor |
| `ludiskus` | `reply` | `owner` | 1 | `single`, `multiple`, `scale` | Không `ranked`/`schedule` trong một câu trả lời |
| `ludiskus` | `comment` | `owner` | 1 | `single`, `multiple`, `scale` | Cần thêm khoá `poll.enabled` ở `comment_policies` của Target (QĐ-P17) |
| `lumuse` | `movie`, `album`, `playlist` | `participants` | 3 | `single`, `multiple`, `scale` | `public_read: true`, `new_profile_can_vote: false` |
| `luprojet` | `project`, `doc`, `update` | `participants` | 5 | tất cả | `verify_mode=strict` có sẵn |
| `lukode` | `presentation` | `owner` | 20 | tất cả trừ `schedule` | Bình chọn khán giả ([§11.6](#116-mẫu-4--lukode-presentation--bình-chọn-khán-giả)) |
| `luxtory` | `document`, `page` | `participants` | 10 | tất cả | Khối bình chọn ([§11.5](#115-mẫu-3--luxtory-khối-bình-chọn)) |
| `lufami` | `calendar_event`, `family_post` | `participants` | 3 | tất cả | Chọn ngày họp mặt, chọn món |

Mọi service khác: không có hàng ⇒ tắt cho tới khi có yêu cầu.

## 11.3 Mẫu 1 — diễn đàn và bình luận (trong ludiskus)

Không qua HTTP. Hai điểm chạm duy nhất:

```go
// internal/domain/poll.go
type PollAttacher interface {
    // Gắn các poll nháp của profile vào ref trong transaction tx của nội dung vừa tạo.
    // pending=true khi nội dung vào hàng chờ kiểm duyệt.
    // snap là bản chiếu do chính handler dựng (nó vừa tạo nội dung nên biết space,
    // visibility, tác giả) — xem đoạn dưới.
    AttachDrafts(ctx context.Context, tx pgx.Tx, profile string, ref ResourceRef,
                 snap InteractionContext, pollIDs []string, pending bool) error
    // Gọi khi nội dung được duyệt/từ chối để poll đi theo.
    OnAnchorDecision(ctx context.Context, tx pgx.Tx, ref ResourceRef, approved bool) error
}
```

`createTopic`, `createReply`, `commentCreate` nhận `pollIds`, gọi `AttachDrafts` sau khi `INSERT`
nội dung, trước `COMMIT`. `approveModeration`/`rejectModeration` và `commentApprove`/`commentReject`
gọi `OnAnchorDecision`.

**Bẫy của mẫu này: Target cho nội dung vừa tạo chưa thể resolve.** Poll trỏ tới
`comment_targets`, nhưng Target của chủ đề vừa `INSERT` chưa tồn tại, và resolver nội bộ
(`SetLocal` → `InteractionContext`) đọc qua **pool** — nó không thấy hàng chưa commit, trả
`ErrNotFound`, và Target sẽ thành `gone`. Vì vậy `AttachDrafts` **không** gọi resolver: nó
upsert `comment_targets` từ `snap` trong cùng `tx` (`state='active'`, `verified_at=now()`), rồi
worker verify làm tươi như mọi Target khác. `snap` do handler dựng bằng **cùng** hàm dựng
`InteractionContext` (tách phần dựng khỏi phần đọc DB thành `buildTopicContext(topic, forum,
post)`) để hai đường không bao giờ khai visibility khác nhau ([14 LP-3.1](14-cong-viec-chi-tiet.md)).

## 11.4 Mẫu 2 — `lumuse` (Resource công khai)

```tsx
// tm/frontend/src/pages/lumuse/movie/TitleDetail.tsx
<PollList resource={{ service: "lumuse", type: "movie", id: movie.id }} />
<CommentThread resource={{ service: "lumuse", type: "movie", id: movie.id }} … />
```

`lumuse` không sửa backend. Ai đọc được phim thì thêm được poll (`participants`), tối đa 3.

## 11.5 Mẫu 3 — `luxtory` khối bình chọn

LuBlockEditor **không** có cơ chế plugin lúc chạy; thêm khối = một PR sửa catalog Go
(`luxtory/backend/internal/block/catalog.go`) + catalog TS (`tm/frontend/src/lib/blocks/catalog.ts`)
+ exporter (test parity Go↔TS sẽ đỏ nếu chỉ sửa một bên). Khối `service-embed`
(`{service, resourceType, resourceId, mode}`) mới có trong tài liệu luxtory (Phase 6), **chưa có mã**.

Hai đường, chọn khi tới GĐ7:

| Đường | Khi nào | Việc |
|-------|---------|------|
| A — chờ `service-embed` | luxtory đã làm Phase 6 | Khối `service-embed {service:"ludiskus", resourceType:"poll", resourceId}`; view TS nhận ra cặp này và vẽ `PollEmbed` thay thẻ chung |
| B — khối riêng `poll` | luxtory chưa làm Phase 6 | Thêm `Def{Type:"poll", Props:{pollId}}` vào cả hai catalog; exporter HTML/Markdown/plaintext xuất thành **liên kết** "Bình chọn: {câu hỏi}" (`/ludiskus/p/{id}`) giống cách `embed` lùi về liên kết |

Trong cả hai đường: poll được tạo **gắn vào tài liệu** (`luxtory:document:{id}`) để quyền xem của
tài liệu quyết định (QĐ-P5); khối chỉ là Embed. Xoá khối **không** xoá poll (khối có thể được
hoàn tác); poll mồ côi khối vẫn thấy ở danh sách `PollList` cuối tài liệu. Dán khối có `pollId`
của tài liệu khác ⇒ `PollEmbed` vẽ theo quyền của poll đó (QĐ-P4), không phải theo tài liệu đang xem.

## 11.6 Mẫu 4 — `lukode` presentation — bình chọn khán giả

Người trình bày tạo poll gắn vào `lukode:presentation:{id}`, `results_visibility = owner_only`
khi đang hỏi rồi đổi sang `always` sau khi đóng để chiếu kết quả. Khán giả xem qua trang chia sẻ
(`SharedView.tsx` đã nhúng `CommentThread`) ⇒ thêm `PollList`. Hỏi lại 15s là đủ cho lớp học;
buổi > 200 người là ngưỡng xem lại realtime ([01 §1.2](01-tong-quan.md)).

Khán giả là khách chưa đăng nhập **không** bỏ phiếu được (QĐ-P18) — đây là giới hạn sản phẩm lớn
nhất với ca dùng này, cần nói rõ với `lukode` trước khi hứa với người dùng.

## 11.7 Mẫu 5 — `lurp` biểu quyết (chưa sẵn sàng)

`lurp` **chưa** có hàng trong `comment_services` và chưa có `resource-context`. Trước khi dùng:

1. `lurp` cài `resource-context` cho loại nội dung cần biểu quyết (do `lurp` chọn — tài liệu này
   không đặt tên loại nội dung của `lurp`).
2. Thêm hàng registry `lurp` (`verify_mode = strict`) + `oauth_client_id`.
3. `lurp` gọi `POST /s2s/polls` với `who_can_vote = invited`, `PUT /s2s/polls/{id}/invitees` theo
   thành phần tham dự, đọc `GET /s2s/polls/{id}` khi đóng để ghi kết quả vào hồ sơ của nó.

Kết quả LuPoll **không** thay biên bản có chữ ký; `lurp` lưu bản sao kết quả kèm nhãn "không có
giá trị pháp lý" ([README](README.md)).

## 11.8 Client Go mẫu cho service

Service đã có client S2S tới ludiskus cho LuComment ⇒ thêm ba hàm:

```go
func (c *Ludiskus) CreatePoll(ctx context.Context, in CreatePollInput) (*Poll, error)   // POST /s2s/polls
func (c *Ludiskus) GetPoll(ctx context.Context, id string) (*PollWithResults, error)    // GET  /s2s/polls/{id}
func (c *Ludiskus) ModeratePoll(ctx context.Context, id string, a PollAction) error     // POST /s2s/polls/{id}/moderate
```

Hippo không có module Go dùng chung — mỗi service chép tay kiểu dữ liệu. Giữ bản chuẩn ở
[09 §9.1](09-backend-api.md); đổi hợp đồng phải là **thêm** trường, không đổi nghĩa trường cũ.

## 11.9 Việc phối hợp với service khác

| Với | Việc | Chặn LuPoll? |
|-----|------|-------------|
| `lufami` | Tắt `dislike` cho `ludiskus × poll` trong `interaction_policies` ([07 §7.3](07-thong-bao-va-tuong-tac.md)) | Không |
| `luxtory` | Chọn đường A/B §11.5 | Chỉ chặn GĐ7 phần luxtory |
| `lukode` | Nhúng `PollList` vào `SharedView` | Không |
| `lurp` | §11.7 | Chỉ chặn mẫu 5 |
| `tm/bff` | Passthrough công khai `/api/public/ludiskus/polls/*` ([12 §12.2](12-trien-khai.md)) | Chặn GĐ6 (đọc công khai) |
