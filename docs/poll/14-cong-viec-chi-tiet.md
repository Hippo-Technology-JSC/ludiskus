# 14 — Danh sách công việc chi tiết

## 14.0 Đọc trước khi nhận việc

Mỗi đầu việc có mã `LP-<GĐ>.<số>`, file phải sửa, việc phải làm, **cách kiểm chứng** (một lệnh
hoặc một thao tác cụ thể) và ước lượng. Làm theo thứ tự trong mỗi GĐ; *(song song)* chia được.

**Mười điều phải biết trước khi viết dòng code đầu tiên:**

1. **Poll không đọc bảng forum hay bình luận.** Muốn biết chủ đề/bình luận là của ai, ai xem
   được ⇒ đi qua resolver/`comment_targets`, như mọi service khác. Có `poll_arch_test`.
2. **Một cửa đọc, một cửa bỏ phiếu.** `ensurePollReadable` / `ensurePollVotable`. Không kiểm quyền
   trong transport.
3. **Kiểm "còn mở" nằm trong câu `UPDATE polls … WHERE … RETURNING`**, không phải trong `if` ở Go.
   `if` ở Go chỉ để trả lỗi đẹp.
4. **`statement_timestamp()`**, không `now()`, trong điều kiện hạn chót.
5. **Số đếm: `UPDATE ±1` cùng transaction, sắp theo `option_id`.** Không `COUNT(*)` lúc đọc,
   không đọc-rồi-ghi.
6. **`poll_ballots` chỉ có ba cột.** Không thêm `created_at`, không thêm trigger, không log body.
7. **Mức danh tính chỉ siết, không nới.** Sau phiếu đầu, mọi PATCH đi qua bảng khoá [03 §3.5](03-mo-hinh-mien.md).
8. **Số đếm không bao giờ rời server khi người xem chưa được thấy.** Kiểm trên JSON đã marshal.
9. **Enum `report_target`:** `0015` chỉ có hai câu `ALTER TYPE`.
10. **Không thêm dependency.** `git diff backend/go.mod` và `git diff tm/frontend/package.json` rỗng.

## GĐ0 — Nền (3 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-0.1 | `db/migrations/0014_poll_core.{up,down}.sql` | Schema [08 §8.1](08-database.md) | `up→down→up` trong acceptance; mỗi `CHECK` một ca vi phạm | 0,5 |
| LP-0.2 | `0015`, `0016`, `0017` | [08 §8.2–8.4](08-database.md) | `0015` đúng hai dòng (`grep -c ADD VALUE` = 2, tổng dòng lệnh = 2) | 0,25 |
| LP-0.3 | `internal/domain/poll.go` | Kiểu, lỗi, `EffectiveState`, `DefaultPollPolicy`, `PollAttacher` | Bảng test `EffectiveState` ≥ 25 ca | 0,5 |
| LP-0.4 | `internal/repository/poll.go`, `poll_policy.go` | CRUD + policy | Test repo trên DB thật | 0,5 |
| LP-0.5 | `internal/service/poll_policy.go` (+ tách hàm merge chung khỏi `comment_policy.go`) | 4 tầng + cache `poll:pol:v` | Test: tầng 4 không nới; test cũ của comment policy vẫn xanh | 0,5 |
| LP-0.6 | `internal/repository/comment_notify.go:158`, `comment_target.go:175` | Dọn Target bỏ qua Target có poll; verify ưu tiên Target có poll mở | Acceptance: Target `gone` có poll ⇒ không bị xoá, job không lỗi FK | 0,25 |
| LP-0.7 | `db/seeds/poll_policies.json`, `loadSeeds()` | Seed ludiskus | Chạy hai lần ⇒ không trùng, không ghi đè hàng đã sửa | 0,25 |
| LP-0.8 | `internal/service/poll_arch_test.go` | 4 luật + cột `poll_ballots` | Thêm `s.repo.GetTopic` vào `poll.go` ⇒ test đỏ | 0,25 |

## GĐ1 — Lõi độc lập (5 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-1.1 | `service/poll.go` | Tạo/sửa/đăng/xoá poll độc lập; bảng khoá sau phiếu đầu | Test bảng: mỗi trường × trước/sau phiếu đầu | 1 |
| LP-1.2 | `service/poll_access.go` | `ensurePollReadable`/`Votable` (độc lập) | Ma trận visibility × vai trò | 0,5 |
| LP-1.3 | `repository/poll_vote.go` | Transaction [05 §5.2](05-bo-phieu-va-kiem-phieu.md), `applyOptionDeltas` | 200 goroutine × 5 lần đổi, thứ tự ngẫu nhiên ⇒ 0 deadlock, `poll_count_check` rỗng | 1 |
| LP-1.4 | `service/poll_vote.go` | Validate lá phiếu, đổi, rút, phát lại theo key | Cùng key ×10 song song ⇒ 1 Receipt | 0,5 |
| LP-1.5 | `service/poll.go` | Đóng, mở lại (≤3), gia hạn | Đua đóng/bỏ phiếu ×1.000 | 0,5 |
| LP-1.6 | `service/poll_tally.go` | `single`/`multiple`, Hamilton | Test thuộc tính: tổng = 100,0 | 0,25 |
| LP-1.7 | `service/poll_access.go` | `resultsVisible` | Bảng 4 chế độ × đã/chưa bỏ phiếu × mở/đóng × vai trò | 0,25 |
| LP-1.8 | `transport/http/poll.go`, `router.go` | Route người dùng (trừ gắn kết), `Idempotency-Key` bắt buộc | Acceptance qua `transport.NewRouter` + `X-Gw-*` ký HMAC | 0,5 |
| LP-1.9 | `transport/http/poll.go` | ETag theo người xem, `Cache-Control: private` | Bỏ phiếu rồi `GET` với ETag cũ ⇒ `200` có kết quả | 0,25 |
| LP-1.10 | `service/poll_abuse.go` | Rate limit `poll:rl:*`, fail open | Tắt Redis ⇒ bỏ phiếu vẫn chạy | 0,25 |

## GĐ2 — tm (4–5 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-2.1 | `tm/frontend/src/lib/poll.ts` | Client + kiểu + key bỏ phiếu giữ qua thử lại | vitest: thử lại dùng cùng key | 0,5 |
| LP-2.2 | `components/poll/PollEmbed.tsx`, `PollResults.tsx`, `PollProvider.tsx` | Vẽ poll, kết quả, gom summary | vitest + Chrome | 1 |
| LP-2.3 | `components/poll/PollComposer.tsx` | Hộp thoại (`ui/Dialog`, `DatePicker`/`TimePicker`, `RadioGroup`, `Repeater`) | Chrome: gõ năm từng chữ số ⇒ 0 request 422 | 1 |
| LP-2.4 | `ballots/SingleBallot.tsx`, `MultipleBallot.tsx` | Bỏ phiếu | Chỉ bàn phím | 0,5 |
| LP-2.5 | `pages/ludiskus/PollPage.tsx`, `Polls.tsx`, route | Trang | Chrome: luồng hai tài khoản | 0,5 |
| LP-2.6 | `components/poll/PollEmbed.tsx` | Hỏi lại 15s khi trong khung nhìn | CDP: tab ẩn ⇒ 0 request | 0,25 |
| LP-2.7 | `tm/frontend/scripts/check-poll.*` | Harness Chrome thật cho GĐ2 | 5 tiêu chí GĐ2 xanh | 0,5 |

## GĐ3 — Gắn kết (5 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-3.1 | `service/interaction.go`, `service/poll_attach.go` | Tách `buildTopicContext`/`buildCommentContext`; `AttachDrafts` upsert Target từ `snap` trong `tx` ([11 §11.3](11-tich-hop-service.md)) | Tạo chủ đề + poll trong một request ⇒ Target `active` cùng visibility với `InteractionContext` đọc lại sau commit | 1 |
| LP-3.2 | `service/forum.go`/`content.go`, `service/comment.go`, handler tương ứng | `pollIds` + `OnAnchorDecision` ở 4 handler duyệt | Chủ đề vào hàng chờ ⇒ poll `pending` ⇒ duyệt ⇒ mở | 1 |
| LP-3.3 | `service/poll.go` | Tạo với `anchor` ngoài + `attach`; Anchor `unverified`/`gone`/`blocked` | Resolver giả 500/`strict` ⇒ 503; `invalidate` ⇒ 410 | 1 |
| LP-3.4 | `transport/http/poll.go` | `GET /polls/r/…`, `POST /polls/summary` | 100 id trong một lô, đã lọc quyền | 0,5 |
| LP-3.5 | `service/comment_policy.go`, `service/comment.go` | Khoá `poll` (QĐ-P17); `pollIds` trong response bình luận (một truy vấn theo lô) | Đếm truy vấn khi đọc 20 bình luận: +1, không +20 | 0,5 |
| LP-3.6 | `transport/http/poll_s2s.go` | S2S tạo/gắn/đọc/kiểm duyệt | Token service khác ⇒ 403 | 0,5 |
| LP-3.7 | `components/poll/PollList.tsx`, `PollComposerButton.tsx`, sửa composer ludiskus + `components/comment` | Nhúng | Chrome: bình luận kèm poll trên trang `lumuse` thật | 0,5 |

## GĐ4 — Loại nâng cao & danh tính (5–6 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-4.1 | `service/poll_tally.go`, `poll_tally_test.go` | IRV + Borda + 3 tầng phá hoà | ≥ 30 ca viết tay | 1,5 |
| LP-4.2 | `service/poll_tally.go` | `scale`, `schedule` | Bảng ca | 0,5 |
| LP-4.3 | `repository/poll_vote.go` | Nhánh `poll_ballots` cho `secret` | `poll_votes` 0 hàng sau phiếu `secret` | 0,5 |
| LP-4.4 | `service/poll_results.go`, `repository/poll_ops.go` | Cache theo `ballot_version`; ảnh chụp tạm ẩn danh | Phiếu lẻ không đổi số trả ra trước ảnh chụp | 0,5 |
| LP-4.5 | `transport/http/*_test.go` | Quét mọi route của chi với poll `anonymous`/`secret` | Không response nào lộ liên kết profile↔lựa chọn | 0,5 |
| LP-4.6 | `transport/http/poll.go` | Log không body | Bắt đầu ra `slog` khi bỏ phiếu `secret`, grep `optionId` = 0 | 0,25 |
| LP-4.7 | `components/poll/ballots/{Ranked,Scale,Schedule}Ballot.tsx`, `RankedRounds.tsx` | UI ba loại | Chrome + chỉ bàn phím; `aria-live` cho `ranked` | 1,5 |

## GĐ5 — Cử tri, thông báo, tương tác (4–5 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-5.1 | `service/poll_access.go`, `poll.go` | `members`/`invited`, `eligible_count`, túc số, API mời | Rời Space sau khi bỏ phiếu ⇒ phiếu giữ; tỉ lệ ≤ 100% | 1 |
| LP-5.2 | `db/seeds/lunoti_event_types.json` | 5 event + 5 template cùng code | lunoti thật: tiêu đề đúng template | 0,5 |
| LP-5.3 | `service/poll_worker.go`, `cmd/worker/main.go` | `CloseDuePolls`, `RemindPolls` | Hai worker song song ⇒ mỗi poll chốt/nhắc đúng một lần | 1 |
| LP-5.4 | `service/interaction.go` | `case "poll"` + đẩy/vô hiệu Interaction | `InteractionBar` thật | 0,5 |
| LP-5.5 | `db/seeds/comment_policies.json`, `PollPage.tsx` | Bình luận dưới poll | Bình luận trên poll độc lập | 0,25 |

## GĐ6 — Kiểm duyệt & công khai (4–5 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-6.1 | `service/poll_moderation.go` | Báo cáo, tự ẩn, ẩn/khôi phục, từ cấm | Lựa chọn bị ẩn ⇒ `ranked` tính lại đúng | 1 |
| LP-6.2 | `service/poll.go`, UI | `allow_user_options` + hàng chờ | Trùng nhãn sau chuẩn hoá ⇒ 409 | 0,5 |
| LP-6.3 | `transport/http/poll_public.go`, `tm/bff/src/gateway.ts`, `index.ts` | Nhóm công khai; tổng quát hoá `proxyPublicLudiskus(prefix)` | `POST` ⇒ 405; siết visibility ⇒ 404 ≤ 1s | 1 |
| LP-6.4 | `transport/http/poll.go` | Xuất CSV, BOM, chống CSV injection | Mở trong LibreOffice không chạy công thức | 0,5 |
| LP-6.5 | `service/poll.go`, UI | Audit + dòng lịch sử | Gia hạn ⇒ "Đã gia hạn" hiện cho người bỏ phiếu | 0,5 |

## GĐ7 — Hệ sinh thái & hardening (4–6 ngày)

| Mã | File | Việc | Kiểm chứng | Ước lượng |
|----|------|------|-----------|-----------|
| LP-7.1 | `db/seeds/poll_policies.json`, trang tm của `lumuse`/`luprojet`/`lukode`/`lufami` | Đợt 1 | Một kịch bản Chrome mỗi service | 1,5 |
| LP-7.2 | `luxtory` + `tm/frontend/src/lib/blocks/*` | Đường A hoặc B | Test parity catalog Go↔TS xanh; xuất Markdown ra liên kết | 1–2 |
| LP-7.3 | `service/poll_worker.go` | `ReconcilePolls`, `SweepPolls`, cờ bất thường | Gây lệch giả ⇒ đối soát sửa + metric | 0,5 |
| LP-7.4 | `transport/http/poll_admin.go`, trang `/comment-admin` | Tab quản trị | Sửa policy ⇒ có hiệu lực ≤ 60s trên mọi pod | 0,5 |
| LP-7.5 | `cmd/tools/polltally` | Kiểm phiếu lại chỉ đọc | So với `poll_results` của 100 poll đã chốt | 0,25 |
| LP-7.6 | kịch bản tải | [13 GĐ7](13-lo-trinh.md) | p95 ≤ 80 ms, 0 lệch | 1 |
