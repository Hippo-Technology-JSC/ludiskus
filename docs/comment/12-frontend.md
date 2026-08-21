# 12 — Frontend (app tm, SolidJS)

## 12.1 Điểm nhúng duy nhất

Service khác **chỉ cần biết một component**:

```tsx
import { CommentThread } from "~/components/comment";

<CommentThread
  resource={{ service: "lumuse", type: "movie", id: movie().id }}
  variant="full"            // "full" | "compact" | "drawer"
  previewReplies={3}
  autoLoad                  // false ⇒ chỉ hiện "Xem 12 bình luận"
  onCountChange={(n) => setCommentCount(n)}
/>
```

`CommentThread` tự làm hết: gọi `GET /comments/r/…`, đọc `capabilities`, ẩn/hiện composer, phân
trang, optimistic UI, mở dialog báo cáo/kiểm duyệt, gắn `<InteractionBar>` cho từng bình luận
nếu `capabilities.interaction.like|reaction`. **Trang gọi không cần biết policy.**

Trong feed/thẻ, chỉ cần con số:

```tsx
<CommentProvider>
  <For each={movies()}>{(m) =>
    <Card>
      …
      <CommentCount resource={{ service: "lumuse", type: "movie", id: m.id }} />
    </Card>
  }</For>
</CommentProvider>
```

## 12.2 Batch loader — bắt buộc

Giống `InteractionProvider` đã có: mỗi `<CommentCount>` **đăng ký** ref của mình khi mount;
`CommentProvider` gom mọi ref trong **một** microtask rồi phát **một**
`POST /api/ludiskus/comments/summary`, cache theo ref trong một store, và cung cấp hàm mutate
cục bộ (`bumpCount(ref, +1)`) để khi người dùng gửi bình luận thì con số trên thẻ nhảy ngay.

Không có Provider ⇒ `<CommentCount>` tự gọi lẻ và **cảnh báo trong console** ở chế độ dev
(một feed 50 thẻ mà gọi 50 request là lỗi cần thấy ngay, không phải lỗi im lặng).

## 12.3 Cây component

| File | Việc |
|------|------|
| `CommentProvider.tsx` | Gom batch summary; store theo ref; `bumpCount`; hàng đợi ngoại tuyến |
| `CommentThread.tsx` | Vỏ: tiêu đề ("12 bình luận"), chọn sắp xếp, composer gốc, danh sách, "Tải thêm", dải "n bình luận mới" |
| `CommentList.tsx` | Danh sách root + `nextCursor`; `aria-live="polite"` khi prepend |
| `CommentItem.tsx` | Một bình luận: avatar, tên, thời gian tương đối, nhãn "đã sửa", nhãn "đã ghim", nội dung, đính kèm, `<InteractionBar>`, `CommentActions` |
| `CommentReplies.tsx` | Trả lời mồi + "Xem thêm n trả lời" (cursor riêng) + composer trả lời |
| `CommentComposer.tsx` | Ô soạn: `MentionInput`, `AttachmentPicker`, đếm ký tự còn lại, Ctrl/Cmd+Enter gửi, chống mất bản nháp |
| `MentionInput.tsx` | Gõ `@` ⇒ gọi `mention-suggest` (debounce 200ms), chọn bằng bàn phím, chèn `@code` |
| `AttachmentPicker.tsx` | Presign → PUT MinIO → giữ `attachmentId`; kéo-thả; huỷ; chỉ hiện khi `capabilities.canAttach` |
| `CommentActions.tsx` | Menu: Trả lời · Sửa · Xoá · Ghim · Ẩn · Báo cáo · Sao chép liên kết (chỉ hiện mục được phép theo cờ `canEdit`/`canDelete`/`canModerate` server trả) |
| `CommentReportDialog.tsx` | Chọn lý do + ghi chú |
| `CommentModerationDialog.tsx` | Cho moderator: xem ngữ cảnh, duyệt/từ chối kèm ghi chú |
| `CommentCount.tsx` | Chỉ số đếm + icon; bấm mở Thread (`variant="drawer"`) |
| `index.ts` | Export công khai — **chỉ** `CommentThread`, `CommentCount`, `CommentProvider` là API cho service khác |

`lib/comment.ts`: type (`ResourceRef`, `CommentItem`, `ThreadInfo`, `Capabilities`), hàm
`request()` theo mẫu `lib/ludiskus.ts`, đối tượng `comment = { thread, list, create, update,
remove, replies, report, subscribe, markRead, summaryBatch, mentionSuggest, moderationQueue,
approve, reject }`, helper cursor và `commentKey(ref)`.

## 12.4 Optimistic UI

| Hành động | Lạc quan | Khi lỗi |
|-----------|----------|---------|
| Gửi bình luận | Chèn ngay với `pending: true` (mờ + spinner nhỏ), `bumpCount(+1)` | Giữ nội dung trong composer, hiện toast, cho **Thử lại** (dùng lại `Idempotency-Key` cũ) |
| Sửa | Đổi nội dung ngay | Trả lại nội dung cũ + toast |
| Xoá | Chuyển sang bia mộ ngay, `bumpCount(-1)` | Hồi phục + toast |
| Ghim / ẩn | Đổi trạng thái ngay | Hồi phục + toast |
| Theo dõi / mute | Đổi ngay | Hồi phục im lặng (ít quan trọng) |

**Không** rollback im lặng với `403`, `410`, `423`, `429`: bốn mã này nghĩa là trạng thái phía
server đã thay đổi ngoài dự kiến ⇒ hiện toast **và** tải lại `GET /comments/r/…` để đồng bộ
`capabilities` (policy có thể vừa đổi, luồng có thể vừa bị đóng).

`202` (vào hàng chờ) **không** phải lỗi: giữ bình luận trong danh sách với nhãn "Đang chờ
duyệt — chỉ bạn thấy", và **không** `bumpCount`.

## 12.5 Trang mới

| Route | Trang |
|-------|-------|
| `/ludiskus/comments` | Ba tab: **Hộp thư** · **Bình luận của tôi** · **Cần duyệt** ([09 §9.6](09-thong-bao-va-theo-doi.md)). Mỗi dòng có icon service lấy qua proxy `lukon` sẵn có |
| `/ludiskus/c/:id` | Permalink: gọi `GET /comments/items/{id}`, có `canonicalPath` ⇒ `navigate(path + "#comment-" + id, {replace:true})`; không có ⇒ hiện bình luận đơn lẻ kèm liên kết tới Thread |
| `/ludiskus/comment-admin` | Registry + bảng policy (form + xem JSON + `warnings[]`) + cờ abuse + nút đối soát. Ẩn nếu không có quyền |

Không thêm mục menu mới cho từng service: bình luận sống **trong** trang của service.
`/ludiskus/comments` nằm cùng nhóm với các trang ludiskus hiện có.

## 12.6 Truy cập được (a11y)

- Toàn bộ luồng dùng được bằng bàn phím: Tab tới composer, `Ctrl/Cmd+Enter` gửi, `Esc` đóng
  dialog/huỷ trả lời, mũi tên trong danh sách gợi ý mention, `Enter` chọn.
- `aria-live="polite"` cho vùng danh sách khi có bình luận mới prepend; **không** dù
  `assertive` (đọc chen ngang khi người dùng đang gõ).
- Mỗi bình luận là `<article>` với `aria-label="Bình luận của {tên}, {thời gian}"`; trả lời
  lồng trong `<ul>/<li>` để screen reader hiểu cấu trúc; độ sâu thể hiện bằng
  `aria-level` chứ chỉ bằng thụt lề CSS.
- Nút chỉ có icon (menu, ghim) đều có `aria-label` tiếng Việt; trạng thái ghim/ẩn có **cả**
  icon và nhãn chữ, không phân biệt chỉ bằng màu.
- Tôn trọng `prefers-reduced-motion`: bỏ animation prepend và skeleton pulse.
- Composer có `aria-describedby` trỏ tới dòng "còn n ký tự"; lỗi validate gắn
  `aria-invalid` + `role="alert"`.

## 12.7 Ngoại tuyến / mạng yếu

Hàng đợi `localStorage` (`cmt:queue`) cho **tạo bình luận** và **sửa**: mỗi mục gồm ref,
`Idempotency-Key`, nội dung, thời điểm. Phát lại khi `online` (an toàn nhờ khoá idempotency).
**Không** xếp hàng: xoá, ghim, ẩn, duyệt/từ chối (hành động quản trị phải chắc chắn về trạng
thái hiện tại — phát lại mù là nguy hiểm).

Bản nháp composer lưu `localStorage` theo `(ref, parentId)`, xoá khi gửi thành công. Đóng tab
giữa lúc gõ không mất nội dung.

## 12.8 Nghiệm thu giao diện

Repo `tm` **không có test tooling**; cách nghiệm thu đã dùng cho các mảng khác là **harness
Vite + Chrome thật qua CDP**, luôn so A/B với bản `git show HEAD`. Áp dụng nguyên vẹn:

1. Trang mẫu dựng `<CommentThread>` với ba `capabilities` khác nhau (đủ quyền / chỉ đọc /
   luồng đã đóng) trên dữ liệu giả.
2. Ảnh chụp: rỗng · 1 root · 20 root + trả lời · bình luận đã xoá giữa cây · bình luận
   `pending` · lỗi mạng · chế độ tối · màn hình 375px.
3. Kiểm bằng bàn phím **thật** (Tab/Enter/Esc/mũi tên), kiểm với `prefers-reduced-motion: reduce`.
4. Kiểm feed 50 thẻ ⇒ **một** request `POST /comments/summary` trong tab Network.
