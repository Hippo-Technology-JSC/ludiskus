# 10 — Frontend (tm)

## 10.1 Nguyên tắc

- Component **dùng chung** ở `components/poll/`, không nằm trong `components/ludiskus` — giống
  `components/comment/`. Service khác import từ `components/poll`.
- Không thêm dependency (`@kobalte/core`, `lucide-solid`, Tailwind 4 đã có).
- Mọi hộp thoại dùng `components/ui/Dialog` (quy ước toàn tm). Chọn ngày giờ dùng
  `components/ui/DatePicker` + `TimePicker`, **không** `<input type="date">` thô gọi API mỗi lần
  đổi giá trị (ô ngày bắn `input` ở mọi phím với năm 0001/0019/0199 ⇒ chuỗi 422).
- Giao diện **không** quyết định quyền: ẩn/hiện theo `capabilities`/`viewer` mà server trả.

## 10.2 `lib/poll.ts`

```ts
export type PollKind = "single" | "multiple" | "ranked" | "scale" | "schedule";
export type IdentityMode = "public" | "owner_only" | "anonymous" | "secret";
export type ResultsVisibility = "always" | "after_vote" | "after_close" | "owner_only";
export type PollState = "draft" | "pending" | "scheduled" | "open" | "closed" | "hidden";
export interface Poll { /* đúng §9.1 */ }

export const poll = {
  get(id, etag?): Promise<{ poll?: Poll; notModified: boolean; etag: string }>,
  listByResource(ref: ResourceRef): Promise<{ polls: Poll[]; canCreate: boolean }>,
  summary(ids: string[]): Promise<PollSummary[]>,       // gom bởi PollProvider
  create(input, idempotencyKey), update(id, patch), replaceOptions(id, options),
  publish(id), attach(id, ref), close(id), reopen(id, closesAt), extend(id, closesAt), remove(id),
  vote(id, ballot, idempotencyKey), retract(id), setNotify(id, on),
  addOption(id, label), results(id), voters(id, q), participants(id, q),
  invite(id, profileUuids), uninvite(id, profileUuids), report(id, reason, note?),
  publicGet(id), publicListByResource(ref),
};
```

`ResourceRef` import từ `lib/comment.ts` — **một** kiểu cho cả hai phân hệ. `request` dùng
`/api/ludiskus`, `publicRequest` dùng `/api/public/ludiskus/polls` (đường BFF mới, [12 §12.2](12-trien-khai.md)).

`Idempotency-Key` sinh **một lần cho mỗi lần bấm "Bỏ phiếu"** và giữ nguyên qua các lần thử lại
mạng; lần bấm mới (đổi ý) sinh key mới. Sinh key mới ở mỗi lần thử lại = biến một lần thử lại
thành một lần "đổi phiếu", và ở `secret` thành `409 ALREADY_VOTED` dù phiếu đầu đã vào.

## 10.3 Component

| Component | Props | Việc |
|-----------|-------|------|
| `PollEmbed` | `id`, `variant?: "card" \| "full" \| "compact"`, `publicRead?` | Vẽ một poll. Không có quyền ⇒ thẻ khoá "Bạn không xem được bình chọn này" (không lộ câu hỏi). `404` ⇒ "Bình chọn không còn" |
| `PollList` | `resource: ResourceRef`, `allowCreate?` | Mọi poll trên một Resource + nút "Thêm bình chọn" khi `canCreate`. Đây là thứ service khác nhúng |
| `PollComposer` | `anchor?`, `draft?`, `spaceUuid?`, `initial?`, `onSaved` | Hộp thoại tạo/sửa; trả `pollId` (để composer chủ đề/bình luận gửi `pollIds`) |
| `PollComposerButton` | như trên | Nút + chip "1 bình chọn đính kèm" dùng trong composer chủ đề/bình luận |
| `ballots/*Ballot` | `poll`, `onSubmit` | Một component mỗi kind |
| `PollResults` | `poll` | Thanh ngang + số + %; `ranked` mở `RankedRounds` |
| `RankedRounds` | `result` | Bảng/vòng IRV, ghi rõ luật phá hoà đã dùng |
| `PollVoters` | `pollId`, `optionId?` | Danh sách người chọn (chỉ khi `capabilities.viewVoters`) |
| `PollProvider` | — | Gom `summary` trong một frame thành một `POST /polls/summary` (mẫu `CommentProvider`) |

## 10.4 Hộp thoại tạo poll

Bố cục một cột, ba phần, phần nâng cao gập sẵn:

1. **Câu hỏi** + mô tả (`MarkdownInput` chế độ basic) + **loại** (5 ô chọn có biểu tượng và một
   câu giải thích).
2. **Lựa chọn** — `Repeater` có kéo thả **và** nút ↑/↓; `scale` thay bằng min/max + nhãn hai đầu;
   `schedule` thay bằng danh sách khung giờ (`DatePicker` + hai `TimePicker`, nút "nhân bản sang
   ngày sau").
3. **Cài đặt** (gập): ai được bỏ phiếu · mức danh tính · lúc lộ kết quả · hạn chót · đổi/rút phiếu ·
   người bỏ phiếu thêm lựa chọn · xáo thứ tự · túc số · nhắc người chưa bỏ phiếu.

Mức danh tính hiển thị là bốn `RadioGroup` item, **mỗi item một câu đầy đủ** từ bảng
[05 §5.3](05-bo-phieu-va-kiem-phieu.md) — ví dụ "Bỏ phiếu kín: không ai, kể cả người tạo, biết
bạn chọn gì. Bạn sẽ không đổi được phiếu." Chọn `secret` tự tắt và khoá "đổi/rút phiếu".

Cảnh báo nội tuyến (không chặn):

- `anonymous`/`secret` + `invited` < 5 người: "Với ít người bỏ phiếu, kết quả cuối có thể cho
  thấy ai chọn gì."
- `secret` + `viewers`: "Ai xem được cũng bỏ phiếu được. Với bầu cử, nên chọn Thành viên Space
  hoặc Danh sách mời."
- `after_vote`: "Người muốn xem kết quả có thể bỏ phiếu cho có. Chọn 'Sau khi đóng' nếu cần kết
  quả trung thực."

Sau phiếu đầu, trường bị khoá hiện ở dạng chỉ đọc kèm biểu tượng khoá và tooltip lý do (không
ẩn đi — người tạo cần biết vì sao không sửa được).

## 10.5 Bỏ phiếu theo từng loại

| Kind | Tương tác | Bàn phím |
|------|-----------|----------|
| `single`, `scale` | `RadioGroup`; `scale` nằm ngang với nhãn hai đầu | Mũi tên, Space |
| `multiple` | Checkbox; bộ đếm "đã chọn 2/3"; vượt `max` thì ô còn lại bị vô hiệu | Tab, Space |
| `ranked` | Danh sách hai cột "Chưa xếp" / "Thứ tự của bạn"; bấm để thêm vào cuối; kéo thả **hoặc** nút ↑/↓/✕ | Enter thêm, Alt+↑/↓ đổi hạng; `aria-live` đọc "Đã xếp X ở hạng 2" |
| `schedule` | Lưới khung giờ × {Có, Có thể}; bấm ô xoay vòng trống → Có → Có thể | Mũi tên di chuyển, Space xoay |

Nút "Bỏ phiếu" bật khi lá phiếu hợp lệ phía client (cùng luật [05 §5.1](05-bo-phieu-va-kiem-phieu.md));
server vẫn là trọng tài. Sau khi gửi: cập nhật từ response (không chèn lạc quan số đếm — số đếm
có thể bị che theo luật lộ kết quả, chèn lạc quan sẽ làm lộ hoặc nhảy số).

Đã bỏ phiếu: hiện lựa chọn của mình + nút "Đổi phiếu" (khi `canChange`) + "Rút phiếu" (khi
`canRetract`) trong menu ⋯. `secret`: chỉ "Bạn đã bỏ phiếu kín lúc 20:14".

## 10.6 Hiển thị kết quả

- `results == null` ⇒ hiện lý do (`resultsHiddenReason`) bằng một câu: "Kết quả sẽ hiện khi
  bình chọn kết thúc (còn 2 ngày)", "Kết quả hiện khi có ít nhất 3 người tham gia".
- Thanh ngang, số tuyệt đối **và** %, lựa chọn của mình có dấu ✓. `multiple` có ghi chú tổng > 100%.
- `ranked`: người thắng + nút "Xem từng vòng" mở `RankedRounds`.
- Túc số: thanh tiến độ "37/120 thành viên (31%) — cần 50%"; khi đóng mà thiếu ⇒ nhãn
  "Không đủ túc số".
- Màu thanh dùng token chủ đề có sẵn, không màu cố định (chế độ tối).
- Dòng lịch sử: "Đã sửa câu hỏi · Đã gia hạn tới 01/11" khi `history` khác rỗng.

## 10.7 Trang và điểm tích hợp trong tm

| Chỗ | Việc |
|-----|------|
| `/ludiskus/p/:id` (`PollPage`) | Poll độc lập: `PollEmbed variant="full"` + `InteractionBar` + `CommentThread` (ref `ludiskus:poll:id`). Poll gắn kết ⇒ chuyển hướng `canonicalPath#poll-{id}` |
| `/ludiskus/polls` (`Polls`) | Tab: Tôi tạo · Tôi đã bỏ phiếu · Được mời · Trong Space của tôi |
| Composer chủ đề, composer trả lời (`components/ludiskus`) | `PollComposerButton` ⇒ `pollIds` khi gửi |
| Bài đầu chủ đề / bài trả lời | `PollList resource={{service:"ludiskus",type:"topic"|"post"|"reply",id}}` dưới thân bài, có `id="poll-{id}"` để deep-link |
| `CommentComposer` / `CommentItem` (`components/comment`) | Nút "Bình chọn" khi `capabilities.poll`; `CommentItem` vẽ `PollEmbed variant="compact"` cho poll gắn vào bình luận |
| Trang Space (ludiskus) | Thẻ "Bình chọn đang mở" — `GET /spaces/{space}/polls?state=open` |
| LuSpotlight | Không thêm provider ở v1 ([01 §1.2](01-tong-quan.md)) |

`CommentItem` cần biết bình luận có poll mà không phải mỗi bình luận một request: response bình
luận thêm `pollIds: []` (ludiskus điền bằng **một** truy vấn theo lô Target ⇒ `polls` khi enrich,
tầng service poll cung cấp qua interface — không JOIN trong repo bình luận); `PollProvider` gom
các id đó thành một `summary`.

## 10.8 Nghiệm thu giao diện

vitest chỉ bắt logic (validate lá phiếu, làm tròn phần dư lớn nhất, sắp lại thứ tự `ranked`).
Lỗi hiển thị — số nhảy khi hỏi lại 15s, thanh kết quả giật khi đổi phiếu, 304 làm kẹt trạng
thái "chưa bỏ phiếu", kéo thả `ranked` trong hộp thoại cuộn — phải chạy **Chrome thật qua CDP**
theo harness `check:*` đang dùng ở tm. Danh sách kiểm ở [13 GĐ2](13-lo-trinh.md).
