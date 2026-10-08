# 13 — Lộ trình

Tám giai đoạn, mỗi giai đoạn là một **lát cắt dọc** chạy được đầu-cuối. "Xong khi" là điều kiện
**chặn** sang giai đoạn sau. Mỗi giai đoạn: `go build ./...` + `go vet ./...` + test sạch,
nghiệm thu trên **PostgreSQL thật** bằng khuôn `internal/acceptance` (schema riêng, DB tên kết
thúc `_test`), cập nhật `ludiskus/CHANGELOG.md` và mục trạng thái của [README](README.md).

| GĐ | Tên | Giao được gì | Ước lượng |
|----|-----|--------------|-----------|
| 0 | Nền dữ liệu & policy | Schema `0014`–`0017`, domain, policy 4 tầng, ranh giới module | 3 ngày |
| 1 | Lõi độc lập: `single`/`multiple` | Tạo, đăng, bỏ/đổi/rút phiếu, đóng, kết quả qua API | 5 ngày |
| 2 | Component tm + trang poll | Poll độc lập dùng được thật trong trình duyệt | 4–5 ngày |
| 3 | Gắn kết | Chủ đề, trả lời, bình luận, Resource ngoài; S2S cơ bản | 5 ngày |
| 4 | Loại nâng cao & danh tính | `ranked` (IRV/Borda), `scale`, `schedule`; `anonymous`, `secret`, ảnh chụp | 5–6 ngày |
| 5 | Cử tri, thông báo, tương tác | `members`/`invited`, túc số, nhắc, lunoti, Interaction, bình luận dưới poll | 4–5 ngày |
| 6 | Kiểm duyệt & công khai | Báo cáo, hàng chờ, lựa chọn người dùng thêm, đọc công khai, xuất CSV | 4–5 ngày |
| 7 | Mở cho hệ sinh thái & hardening | `lumuse`/`luprojet`/`lukode`/`luxtory`, đối soát, tải, quản trị | 4–6 ngày |

Tổng **34–40 ngày người**. GĐ0–GĐ3 là MVP dùng được trong diễn đàn và bình luận (17–18 ngày).
GĐ6 **bắt buộc** trước khi bật `public_read` ở bất kỳ đâu.

---

## GĐ0 — Nền dữ liệu & policy

- [ ] Migration `0014`–`0017` + `.down.sql` ([08](08-database.md)).
- [ ] `domain/poll.go`: kiểu, lỗi, `EffectiveState()`, `DefaultPollPolicy`, `PollAttacher`.
- [ ] `repository/poll*.go`: CRUD poll/lựa chọn/mời, chưa có phiếu.
- [ ] `service/poll_policy.go`: hợp nhất 4 tầng, tái dùng hàm merge của `comment_policy.go`
      (tách hàm chung nếu cần — **không** chép).
- [ ] Sửa job dọn Target mồ côi + thứ tự verify ([12 §12.3](12-trien-khai.md)).
- [ ] Seed `poll_policies.json` + nạp trong `loadSeeds()`.
- [ ] `poll_arch_test.go` (4 luật [02 §2.4](02-kien-truc.md)) + kiểm cột `poll_ballots`.

**Xong khi (chặn):**

- [ ] `up → down → up` không lỗi trên DB có dữ liệu LuComment thật.
- [ ] `EffectiveState` có bảng test phủ mọi tổ hợp `status × opens_at × closes_at × Anchor.state`.
- [ ] Mọi `CHECK` của `polls` có một test chèn vi phạm và nhận lỗi (đặc biệt `secret` + `allow_change_vote`).
- [ ] Policy: thiếu hàng ⇒ tắt; tầng 4 không nới được; `kinds` lấy giao.
- [ ] Job dọn Target không xoá Target còn poll (test có FK `RESTRICT` sẽ đỏ nếu quên).
- [ ] `arch_test` đỏ khi thêm `s.repo.GetTopic` vào một file `poll_*.go`.

## GĐ1 — Lõi độc lập (`single`, `multiple`)

- [ ] `service/poll.go`: tạo (độc lập), sửa với bảng khoá [03 §3.5](03-mo-hinh-mien.md), đăng, đóng, mở lại, gia hạn, xoá.
- [ ] `service/poll_access.go`: `ensurePollReadable`, `ensurePollVotable` (chưa Anchor).
- [ ] `service/poll_vote.go` + `repository/poll_vote.go`: transaction [05 §5.2](05-bo-phieu-va-kiem-phieu.md), đổi, rút, `applyOptionDeltas`.
- [ ] `service/poll_tally.go`: `single`/`multiple` + làm tròn phần dư lớn nhất.
- [ ] `resultsVisible` + ETag theo người xem.
- [ ] `transport/http/poll.go`: nhóm người dùng trừ phần gắn kết.
- [ ] Rate limit `poll:rl:*`.

**Xong khi (chặn):**

- [ ] 200 goroutine bỏ phiếu cùng lúc vào một poll, mỗi người đổi phiếu 5 lần với thứ tự lựa
      chọn ngẫu nhiên ⇒ không deadlock, `poll_count_check` rỗng.
- [ ] Cùng `Idempotency-Key` gửi song song 10 lần ⇒ một Receipt, `voter_count = 1`, `ballot_version` tăng 1.
- [ ] Đua đóng/bỏ phiếu 1.000 lần ⇒ không phiếu nào có `poll_voters.voted_at > closed_at`.
- [ ] `closes_at` trôi qua khi worker **tắt** ⇒ API vẫn trả `state: closed` và từ chối phiếu.
- [ ] Phần trăm `single` luôn cộng đúng 100,0 (test thuộc tính 10.000 bộ ngẫu nhiên).
- [ ] Người chưa bỏ phiếu ở `after_vote` ⇒ JSON **không chứa** khoá số đếm (kiểm trên JSON đã
      marshal, không trên struct — bài học `omitempty` của LuComment 2026-09-23).
- [ ] Bỏ phiếu xong, `GET` với ETag cũ ⇒ `200` (không `304`) và có kết quả.
- [ ] Sửa `identity_mode` `anonymous → public` sau phiếu đầu ⇒ `409 POLL_LOCKED`.

## GĐ2 — Component tm + trang poll

- [ ] `lib/poll.ts`, `PollEmbed`, `PollComposer`, `SingleBallot`, `MultipleBallot`, `PollResults`, `PollProvider`.
- [ ] `/ludiskus/p/:id`, `/ludiskus/polls`.
- [ ] Hỏi lại 15s khi trong khung nhìn + `ETag`.

**Xong khi (chặn) — Chrome thật qua CDP:**

- [ ] Tạo poll độc lập, gửi liên kết cho tài khoản thứ hai, bỏ phiếu, người thứ nhất thấy số mới trong ≤ 20s.
- [ ] Mất mạng giữa lúc bấm "Bỏ phiếu", thử lại ⇒ một phiếu, không `409`.
- [ ] Không có bước nhảy bố cục khi kết quả hiện ra sau khi bỏ phiếu (đo CLS của thẻ poll).
- [ ] Toàn bộ luồng tạo + bỏ phiếu làm được chỉ bằng bàn phím.
- [ ] Chọn ngày giờ đóng không sinh `422` nào khi gõ năm từng chữ số.

## GĐ3 — Gắn kết

- [ ] `PollAttacher` + `buildTopicContext`/`buildCommentContext` tách khỏi `InteractionContext` ([11 §11.3](11-tich-hop-service.md)).
- [ ] `pollIds` trong `createTopic`, `createReply`, `commentCreate`; `OnAnchorDecision` ở 4 handler duyệt/từ chối.
- [ ] `POST /polls` với `anchor` (Resource ngoài) qua `ensureCommentTarget`; `POST /polls/{id}/attach`.
- [ ] `GET /polls/r/{service}/{type}/{id}`, `POST /polls/summary`.
- [ ] Khoá `poll` trong `comment_policies` (QĐ-P17); `pollIds` trên response bình luận.
- [ ] S2S: `POST /s2s/polls`, `/attach`, `GET`, `/moderate`.
- [ ] tm: `PollList`, `PollComposerButton` trong composer chủ đề/trả lời/bình luận, `PollEmbed` trong `CommentItem`.

**Xong khi (chặn):**

- [ ] Tạo chủ đề kèm poll trong Space kín ⇒ người ngoài Space `GET /polls/{id}` nhận `404`; thành viên bỏ phiếu được.
- [ ] Chủ đề vào hàng chờ ⇒ poll `pending`, không ai bỏ phiếu được; duyệt ⇒ poll mở; từ chối ⇒ poll `rejected`.
- [ ] Gắn poll vào một ref của `lumuse` thật qua resolver; xoá phim (`invalidate`) ⇒ poll `410`, worker đóng nó.
- [ ] Resolver giả trả `500` với service `strict` ⇒ không tạo được poll (`503`).
- [ ] Bình luận dưới `luprojet` (policy `poll.enabled=false`) ⇒ `pollIds` bị từ chối `403 POLL_DISABLED`; bình luận vẫn **không** được tạo (một transaction).
- [ ] Token service khác gọi `/s2s/polls` trên ref không phải của nó ⇒ `403 SERVICE_SCOPE_MISMATCH`.

## GĐ4 — Loại nâng cao & danh tính

- [ ] `ranked` (IRV + Borda), `scale`, `schedule` ở backend + 3 ballot component + `RankedRounds`.
- [ ] `anonymous` (che ở mọi route), `secret` (`poll_ballots`), ngưỡng `min_voters`, ảnh chụp tạm.
- [ ] Cache kết quả `poll:res:{id}:{ballot_version}`.

**Xong khi (chặn):**

- [ ] `poll_tally_test.go`: ≥ 30 ca IRV viết tay có đáp án (gồm hoà ở đáy cả ba tầng phá hoà,
      phiếu cạn, lựa chọn bị ẩn giữa chừng, hai người cuối hoà), đối chiếu với tính tay.
- [ ] `ranked` 10.000 người × 5 hạng tính ≤ 150 ms trên Postgres compose.
- [ ] Quét **mọi** route (người dùng, công khai, S2S, xuất CSV, audit) với poll `anonymous` và `secret`:
      không response nào chứa `profile_uuid` gắn với lựa chọn (test tự động duyệt bảng route của chi).
- [ ] Bỏ phiếu `secret` ⇒ `poll_ballots` có 1 hàng, `poll_votes` có 0 hàng; log ứng dụng không chứa `optionId`.
- [ ] Tấn công phép trừ: một phiếu `anonymous` lẻ không làm số đếm trả ra thay đổi trước ảnh chụp kế tiếp.

## GĐ5 — Cử tri, thông báo, tương tác

- [ ] `members` (chụp `eligible_count`), `invited` + API mời, túc số.
- [ ] Seed 5 event lunoti + template; `CloseDuePolls`, `RemindPolls`.
- [ ] `case "poll"` trong `InteractionContext`; đẩy/vô hiệu Interaction.
- [ ] `comment_policies` `ludiskus × poll`; `CommentThread` trên `/ludiskus/p/:id`.

**Xong khi (chặn):**

- [ ] lunoti thật: `poll.closed` hiện **đúng tiêu đề template** (không phải "Thông báo mới") ở chuông của người bỏ phiếu.
- [ ] Poll mở lại rồi đóng lần hai ⇒ thông báo lần hai **được gửi** (khoá idempotency có `r{reopen_count}`).
- [ ] `closing_soon` chỉ tới người chưa bỏ phiếu, đúng một lần, kể cả khi hai worker chạy song song.
- [ ] Like poll qua `InteractionBar` thật; poll trong Space kín không like được bởi người ngoài.

## GĐ6 — Kiểm duyệt & đọc công khai

- [ ] `0015` đã có; báo cáo poll/lựa chọn, tự ẩn, hàng chờ, `allow_user_options` + duyệt.
- [ ] Nhóm công khai + BFF `proxyPublicLudiskus(prefix)`.
- [ ] Xuất CSV hai dạng; audit; dòng lịch sử trên giao diện.

**Xong khi (chặn):**

- [ ] Khách đọc poll `public` qua BFF; `POST` ⇒ `405`; poll `after_vote` chưa đóng ⇒ `results: null`.
- [ ] Siết visibility Anchor từ `public` ⇒ trong ≤ 1s đường công khai trả `404` (cache `poll:pub:*` bị xoá).
- [ ] Lựa chọn bị ẩn trong `ranked` ⇒ kết quả tính lại đúng (hạng dồn), khôi phục ⇒ quay về kết quả cũ.
- [ ] CSV có ô `=HYPERLINK(...)` được tiền tố `'`; mở bằng Excel/LibreOffice không chạy công thức.

## GĐ7 — Mở cho hệ sinh thái & hardening

- [ ] Seed policy đợt 1 ([11 §11.2](11-tich-hop-service.md)); nhúng `PollList` ở `lumuse`, `luprojet`, `lukode` (SharedView), `lufami`.
- [ ] `luxtory`: đường A hoặc B ([11 §11.5](11-tich-hop-service.md)).
- [ ] `ReconcilePolls`, cờ bất thường, tab quản trị.
- [ ] Kịch bản tải.

**Xong khi (chặn):**

- [ ] ≥ 3 service ngoài ludiskus dùng thật trên stack dev, mỗi cái một kịch bản Chrome thật.
- [ ] Tải: 200 người đồng thời trên một poll, 2.000 poll mở, 30 phút ⇒ p95 bỏ phiếu ≤ 80 ms, không lỗi 5xx, đối soát 0 lệch.
- [ ] Đối soát chạy trên DB tải xong ⇒ 0 dòng sửa. Đối soát **không** báo lệch cho poll đã chốt mà
      người dùng đã xoá dữ liệu cá nhân (so với `poll_results`, không với bảng phiếu — [12 §12.5](12-trien-khai.md)).

---

## Rủi ro

| Rủi ro | Khả năng | Ảnh hưởng | Giảm thiểu |
|--------|----------|-----------|-----------|
| Hàng `polls` nóng tuần tự hoá phiếu (QĐ-P10) | Thấp ở quy mô hippo | Chậm khi một poll viral | Cổng tải GĐ7; nếu vượt: tách `voter_count`/`ballot_version` sang bảng đếm phân mảnh, giữ `UPDATE … WHERE` kiểm trạng thái |
| Người dùng hiểu `anonymous` là "không ai biết, kể cả hệ thống" | Trung bình | Mất niềm tin | Câu chữ cố định [05 §5.3](05-bo-phieu-va-kiem-phieu.md), cấm viết gọn |
| Ai đó "sửa" thuật toán IRV làm đổi kết quả cũ | Thấp | Cao | Poll đã chốt chỉ đọc `poll_results`; bảng ca test khoá hành vi |
| Bảng chiếu dùng chung `comment_targets` làm LuComment và LuPoll dính nhau | Trung bình | Sửa một bên làm hỏng bên kia | Hai điểm sửa ghi rõ ở [12 §12.3](12-trien-khai.md); test acceptance của LuComment chạy trong cổng mỗi GĐ |
| Khách không bỏ phiếu được làm `lukode` thất vọng | Cao | Ca dùng khán giả yếu | Nói trước ([11 §11.6](11-tich-hop-service.md)); ghi vào danh sách xem lại |
| luxtory chưa có `service-embed` | Cao | Trễ khối tài liệu | Đường B độc lập ([11 §11.5](11-tich-hop-service.md)) |
