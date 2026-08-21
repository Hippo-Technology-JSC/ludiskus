# 16 — Danh sách công việc chi tiết

## 16.0 Đọc trước khi nhận việc

**Cách dùng tài liệu này.** Mỗi đầu việc có mã (`LC-<GĐ>.<số>`), file phải sửa, việc phải làm,
**cách kiểm chứng** (một câu lệnh hoặc một thao tác cụ thể, không phải "chạy thử xem sao") và
ước lượng. Làm theo thứ tự trong mỗi giai đoạn; việc ghi *(song song)* chia cho người khác được.

**Chín điều phải biết trước khi viết dòng code đầu tiên:**

1. **Bình luận không phải bài diễn đàn.** File `comment_*.go` **không** được chạm
   `topics`/`posts`/`boards`. Có test chặn (`LC-0.8`). Thấy mình cần một khái niệm của forum
   (board, topic, slug, is_answer) ⇒ đang đặt code sai chỗ.
2. **Quyền đọc chỉ có một cửa.** Mọi route đọc đi qua `ensureReadable`. Không viết nhánh kiểm
   quyền thứ hai trong transport, không "kiểm nhanh cho tiện".
3. **Số đếm: một câu `UPDATE ±1`, cùng transaction.** Không đọc-rồi-ghi, không `COUNT(*)` lúc
   đọc, không cập nhật ngoài transaction của thay đổi trạng thái.
4. **Enum `report_target`:** `ALTER TYPE` phải nằm **một mình** trong `0004`. Đặt thêm bất cứ
   câu nào vào file đó là deploy đỏ (QĐ-12).
5. **Danh sách trắng, không danh sách đen.** Thẻ HTML, scheme URL, host ảnh, mime đính kèm,
   giá trị `sort`, `reason` báo cáo — tất cả là "chỉ cho phép cái này".
6. **Cảnh báo không im lặng.** Cursor sai ⇒ `400`, không trả trang đầu. `sort=top` ở v1 ⇒
   `400`, không rơi mềm về `newest`. Policy đáng ngờ ⇒ `warnings[]`.
7. **Phụ thuộc ngoài đều mềm** (lunoti, lufami, resolver) — trừ Postgres. Không đưa chúng vào
   `/readyz`, không để chúng chặn đường viết bình luận.
8. **Không thêm dependency.** Go: `git diff backend/go.mod` phải rỗng. TypeScript:
   `git diff tm/frontend/package.json` phải rỗng.
9. **`down.sql` phải chạy được thật**, trên DB có dữ liệu.

**Quy ước ước lượng:** 1 ngày = một ngày người tập trung, đã tính cả viết test.

---

## GĐ0 — Nền dữ liệu, registry & resolver (3–4 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-0.1** | Migration `0003`: `comment_services`, `comment_policies`, `comment_targets`, `comments`, `comment_mentions`, `comment_participants` + toàn bộ index/trigger/CHECK; seed registry; `profile_cache.created_at` | `backend/db/migrations/0003_comment_core.up.sql` / `.down.sql` | `up` → `\dt comment*` thấy 6 bảng; `\d comments` thấy 3 CHECK cấu trúc; `down` → không còn bảng nào, `profile_cache` trở về đúng cột cũ; `up` lại được. Chạy trên DB **có dữ liệu forum thật** | 0.75 |
| **LC-0.2** | `domain/comment.go`: `ResourceRef` + `Validate`, `CommentTarget`, `Comment`, `CommentPolicy`, `Capabilities`, `DefaultCommentPolicy`, `countDelta(old,new,isRoot)`, hằng trạng thái | `backend/internal/domain/comment.go` | Test bảng: 20 ref (hợp lệ + sai charset + quá dài) ra đúng kết quả; `countDelta` phủ **mọi** cặp trong 5 trạng thái × root/reply | 0.5 |
| **LC-0.3** | Repo target + registry + policy: upsert lười, get theo ref, list stale, đọc/ghi policy, participants | `internal/repository/comment_target.go`, `comment_policy.go` | Test tích hợp Postgres thật: `ensureTarget` gọi 2 lần ⇒ 1 hàng; `UNIQUE` chặn ref trùng; xoá target ⇒ cascade participant/mention | 0.75 |
| **LC-0.4** | `internal/resolver`: token client-credentials (cache, làm mới trước hạn 30s), timeout 3s, cache Redis, batch ≤100, **dò đường** `resource-context` → `interaction-context` + ghi `context_path`, phân loại lỗi `NotFound`/`Unavailable`/`Invalid`, đường nội bộ cho `service='ludiskus'` | `internal/resolver/resolver.go` | Gọi thật `lukolek` trong compose: lần 1 có 2 request (dò), lần 2 có 1; `context_path` đã ghi; tắt `lukolek` ⇒ trả `Unavailable` sau ≤ 3s (không treo) | 1.0 |
| **LC-0.5** | Hợp nhất policy 4 tầng + cache process 60s + version Redis `cmt:pol:v`; validate `canonical_path`/`thumbnail_url` | `internal/service/comment_policy.go` | Test: tầng 4 **không** bật được cờ policy đã tắt; số lấy `min`; danh sách lấy giao; `canonical_path` = `//evil.com`, `javascript:…`, `../x`, `https://…` đều bị từ chối | 0.5 |
| **LC-0.6** | Seed policy + nạp trong `loadSeeds()` | `backend/db/seeds/comment_policies.json`, `internal/service/service.go` | Khởi động API ⇒ `SELECT count(*) FROM comment_policies` khớp số hàng trong seed; sửa seed rồi khởi động lại ⇒ cập nhật, không nhân bản | 0.25 |
| **LC-0.7** | **Sửa `auth`**: `ServiceMiddleware` từ chối token có `sub` (`403 not_a_service_token`), thêm `parseService` không ràng buộc audience, `ServiceClientID(ctx)`; `requireServiceClient` tra registry (cache 60s) | `internal/auth/middleware.go`, `internal/transport/http/comment_s2s.go` | Token **người dùng** gọi `/api/v1/s2s/interaction-context/topic/{id}` ⇒ `403` (trước đây `200`); token service có `aud` trong registry ⇒ `200`; `aud` lạ ⇒ `403 UNKNOWN_SERVICE_CLIENT` | 0.5 |
| **LC-0.8** | `comment_arch_test.go`: chặn `comment_*.go` gọi repo forum; chặn file forum dùng `domain.Comment*`; chặn `internal/resolver` import `internal/service` | `internal/service/comment_arch_test.go` | Thêm tạm `s.repo.GetTopic(...)` vào `comment.go` ⇒ test **đỏ**; xoá ⇒ xanh | 0.25 |

---

## GĐ1 — Đọc/viết lõi (5–6 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-1.1** | Repo comment: `Insert` (kèm `root_id`/`depth`), keyset root (`idx_comments_root_page`), `LATERAL` reply mồi, `bumpCounts` (target + parent + root trong một `UPDATE … WHERE id = ANY`), `GetByID`, `ExistsPublishedByAuthor` | `internal/repository/comment.go` | `log_statement=all`: đọc 20 root + 3 reply/root sinh **đúng 3** truy vấn; `EXPLAIN` cho thấy dùng `idx_comments_root_page` (không `Seq Scan`) | 1.0 |
| **LC-1.2** | `ensureTarget` theo 3 chế độ verify; `unverified` ⇒ `visibility='private'`; `verify_failures` ⇒ `gone` ở lần thứ 3; `invalidate` + xoá cache | `internal/service/comment_target.go` | Tắt service đích: `strict` ⇒ `503`; `optimistic` ⇒ target `unverified` + `private`; `trust` ⇒ không có request HTTP nào (đếm bằng log) | 0.75 |
| **LC-1.3** | `ensureReadable` + `ensureCommentable` đúng trình tự [06 §6.2/§6.3](06-phan-quyen.md), kèm `Capabilities.reasons` | `internal/service/comment.go` | Test bảng: 5 `visibility` × (thành viên / không / owner / khách) ⇒ đúng mã lỗi; `connections` cho ra hành vi của `private` | 0.75 |
| **LC-1.4** | `markdown`: `policyPlain`/`policyBasic`/`policyRich` + `Render(mode, src)`; giữ nguyên hành vi cũ cho forum | `internal/markdown/markdown.go` | 20 payload XSS × 3 mức ⇒ không `<script`, không `on*=`, scheme chỉ `http/https/mailto`; test parity: `Render("rich", x)` == `Render(x)` cũ cho 10 mẫu bài forum thật | 0.75 |
| **LC-1.5** | `comment_abuse.go`: rate limit Redis 3 khoá + chặn trùng `body_hash` + siết Profile mới; Redis chết ⇒ fail **open** + log `warn` | `internal/service/comment_abuse.go` | Gửi 6 bình luận/phút ⇒ cái thứ 6 nhận `429` + `Retry-After`; gửi lại đúng nội dung trong 60s ⇒ `409`; `docker stop ludiskus-redis` ⇒ vẫn gửi được, log có `warn` | 0.5 |
| **LC-1.6** | `Create`: validate độ dài/liên kết/mention/đính kèm, quyết định trạng thái, **một transaction** (insert + counts + participant + mentions + attachments), `Idempotency-Key` | `internal/service/comment.go` | Hai goroutine cùng `Idempotency-Key` ⇒ 1 hàng, `comment_count=1`; 10 goroutine bình luận cùng target ⇒ `comment_count=10` | 1.0 |
| **LC-1.7** | Transport nhóm đọc + `POST items` + `POST /comments/summary` (batch ≤100, có `skipped[]`) + `ETag`/`If-None-Match` | `internal/transport/http/comment.go`, `router.go` | `curl` tạo bình luận trên ref `lukolek` ⇒ `201`; gọi lại `GET` với `If-None-Match` ⇒ `304`; batch 100 ref ⇒ một response, p95 < 150ms lần thứ hai | 0.75 |
| **LC-1.8** | Cursor: dựng/giải base64, hai chiều, `400 INVALID_CURSOR`; `sort=top` ⇒ `400` | `internal/service/comment.go`, `util.go` | Chèn bình luận mới giữa hai lần lấy trang ⇒ trang 2 **không** lặp và **không** mất hàng; cursor rác ⇒ `400` | 0.5 |

---

## GĐ2 — Component `tm` + dogfood (4–5 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-2.1** | `lib/comment.ts`: type, `request()`, đối tượng `comment`, `commentKey`, helper cursor | `tm/frontend/src/lib/comment.ts` | `tsc --noEmit` sạch; gọi được 5 endpoint từ console trình duyệt | 0.5 |
| **LC-2.2** | `CommentProvider` + `CommentCount`: gom batch trong một microtask, store theo ref, `bumpCount`, cảnh báo dev khi thiếu Provider | `components/comment/CommentProvider.tsx`, `CommentCount.tsx` | Feed 20 thẻ ⇒ **một** `POST /comments/summary` trong tab Network; thiếu Provider ⇒ có `console.warn` | 0.5 |
| **LC-2.3** | `CommentThread` + `CommentList` + `CommentItem`: 3 `variant`, chọn sắp xếp, "Tải thêm", skeleton, trạng thái rỗng/không quyền | `components/comment/*.tsx` | Ảnh chụp 8 trạng thái ở [12 §12.8](12-frontend.md); màn 375px không tràn ngang | 1.0 |
| **LC-2.4** | `CommentComposer`: đếm ký tự, `Ctrl/Cmd+Enter`, bản nháp `localStorage`, hàng đợi ngoại tuyến với `Idempotency-Key` | `components/comment/CommentComposer.tsx` | Gõ rồi F5 ⇒ nội dung còn; ngắt mạng gửi rồi bật lại ⇒ đúng một bình luận | 0.75 |
| **LC-2.5** | Optimistic UI + luật không rollback im lặng cho `403/410/423/429` | `CommentProvider.tsx`, `CommentThread.tsx` | Đóng luồng ở tab khác rồi gửi ⇒ toast + `capabilities` được tải lại, composer bị ẩn | 0.5 |
| **LC-2.6** | Nhúng thật vào trang chi tiết `lukolek` (mẫu A) | `tm/frontend/src/pages/lukolek/*` | Bình luận thật qua BFF, F5 vẫn thấy, tên/avatar đúng | 0.5 |
| **LC-2.7** | Nhánh `case "comment"` trong provider interaction-context + `syncInteractionResource` cho bình luận; gắn `<InteractionBar>` vào `CommentItem` | `internal/service/interaction.go`, `internal/service/comment.go`, `components/comment/CommentItem.tsx` | Like một bình luận ⇒ `lufami` có resource `ludiskus:comment:{id}`; `LUFAMI_API_URL=""` ⇒ bình luận vẫn chạy, chỉ mất nút | 0.75 |

---

## GĐ3 — Cây, ghim, sửa/xoá, tìm (4–5 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-3.1** | Trả lời + **làm phẳng** + `reply_to_profile_uuid`; `GET /items/{id}/replies` | `internal/service/comment.go`, `repository/comment.go` | `max_depth=2`, trả lời ở tầng 3 ⇒ `depth=2`, `parent_id` = tổ tiên đúng; `max_depth=0` ⇒ `canReply=false` | 0.75 |
| **LC-3.2** | `CommentReplies`: trả lời mồi + "Xem thêm n trả lời" (cursor riêng) + composer trả lời + chip `@tên` | `components/comment/CommentReplies.tsx` | Nhánh 50 trả lời tải theo trang, không nhảy vị trí đọc | 0.5 |
| **LC-3.3** | Sửa: cửa sổ thời gian, `comment_revisions`, render lại, mention mới, `GET /revisions` | `internal/service/comment.go`, `repository/comment.go` | Sửa trong 15 phút ⇒ `200` + 1 hàng revision; sau đó ⇒ `403 EDIT_WINDOW_CLOSED`; sửa 12 lần ⇒ chỉ giữ 10 revision | 0.75 |
| **LC-3.4** | Xoá mềm + bia mộ + `deleted_by` + giảm đếm | `internal/service/comment.go` | Xoá bình luận có 2 trả lời ⇒ response có `deleted:true`, **không** có `bodyHtml`; cây không vỡ; `comment_count` giảm 1 | 0.5 |
| **LC-3.5** | Ghim theo `policy.pin.by` + `max_pinned`; thứ tự `is_pinned DESC` không làm lệch cursor | `internal/service/comment.go` | Ghim 3 rồi ghim cái thứ 4 ⇒ `422`; ghim rồi phân trang 3 trang ⇒ không có hàng lặp | 0.5 |
| **LC-3.6** | Mention đủ đường: trích → scope → `comment_mentions` → `mention-suggest` + `MentionInput` | `internal/service/comment.go`, `components/comment/MentionInput.tsx` | `scope='space'`, mention người ngoài Space ⇒ **0** hàng `comment_mentions`; gợi ý trả ≤10, chọn được bằng bàn phím | 0.75 |
| **LC-3.7** | Migration `0005` phần đính kèm + `PresignInput.resourceRef` + `AttachmentPicker` | `0005_comment_moderation.up.sql`, `internal/service/attachment.go`, `components/comment/AttachmentPicker.tsx` | Đính kèm ảnh thành công; hàng có cả `post_id` và `comment_id` bị `attachments_owner_one` chặn; `up → down → up` sạch | 0.75 |
| **LC-3.8** | Tìm trong Thread (`search_tsv` + trgm) | `internal/repository/comment.go`, `internal/service/comment.go` | Tìm `dieu` khớp bình luận chứa `điều`; truy vấn luôn có `target_id` trong `WHERE` (đọc `EXPLAIN`) | 0.5 |

---

## GĐ4 — Thông báo & hộp thư (4–5 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-4.1** | Migration `0006`: `comment_notify_buffer`, `comment_abuse_flags`, `comment_audit_logs`, view `comment_count_check` | `0006_comment_ops.up.sql` / `.down.sql` | `up → down → up`; `SELECT * FROM comment_count_check` trả 0 hàng trên dữ liệu đúng | 0.5 |
| **LC-4.2** | Fan-out + ghi buffer **trong cùng transaction** tạo/approve; giữ mốc `flush_after` của lô đầu | `internal/service/comment_notify.go` | Tạo bình luận ⇒ đúng số hàng buffer bằng số người nhận; tự bình luận nội dung mình ⇒ **0** hàng | 0.75 |
| **LC-4.3** | Worker `FlushCommentNotify` (10s, `SKIP LOCKED`, gom, một hàng outbox/nhóm, `idempotency_key` chứa `max(comment_id)`) + **kiểm lại quyền đọc từng người nhận** | `internal/service/comment_worker.go`, `cmd/worker/main.go` | 100 bình luận/phút ⇒ ≤1 outbox/người/5 phút, `data.count=100`; loại người nhận khỏi Space giữa cửa sổ ⇒ **không** có outbox cho người đó (test tích hợp) | 1.0 |
| **LC-4.4** | 5 event-type + template tiếng Việt | `backend/db/seeds/lunoti_event_types.json` | Khởi động worker ⇒ lunoti có 5 event-type mới; chuông trong tm hiện đúng câu | 0.5 |
| **LC-4.5** | Theo dõi / mute / đã đọc / `unread-count` (+ cache Redis 30s và xoá cache đúng lúc) | `internal/service/comment_notify.go`, `transport/http/comment.go` | Mute ⇒ không nhận thông báo nhưng vẫn đọc được; đánh dấu đã đọc ⇒ badge về 0; có bình luận mới ⇒ badge lên 1 trong ≤30s | 0.5 |
| **LC-4.6** | `GET /comments/inbox` + `/mine` (lọc theo status/service/q) | `internal/repository/comment.go` | Bình luận `pending`/`hidden`/`rejected` của tôi hiện trong `/mine` **kèm lý do**; không hiện với người khác | 0.5 |
| **LC-4.7** | Trang `/ludiskus/comments` (3 tab) + `/ludiskus/c/:id` | `tm/frontend/src/pages/ludiskus/Comments.tsx`, `CommentPermalink.tsx` | Bấm thông báo ⇒ tới `canonicalPath#comment-{id}`, bình luận được tô sáng; permalink không có `canonicalPath` ⇒ hiện bình luận đơn lẻ | 0.75 |

---

## GĐ5 — Kiểm duyệt & đọc công khai (5–6 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-5.1** | Migration `0004` (**chỉ** `ALTER TYPE`) + phần kiểm duyệt của `0005`: nới `space_uuid` của `attachments`/`reports`/`moderation_items`, dọn report trùng rồi tạo `uq_reports_open_reporter` | `0004_comment_report_enum.up.sql`, `0005_comment_moderation.up.sql` | `up` sạch **trên DB có báo cáo forum thật** (có hàng trùng ⇒ vẫn chạy được); thêm thử một câu nữa vào `0004` ⇒ deploy **đỏ** với "unsafe use of new value" (kiểm một lần rồi revert, để hiểu vì sao phải tách) | 0.75 |
| **LC-5.2** | 4 chế độ kiểm duyệt + từ cấm (tái dùng `matchesBanned`) + `first_comment` **theo Target** | `internal/service/comment_moderation.go` | `pre` ⇒ người khác không thấy; `first_comment` ⇒ bình luận thứ hai đăng thẳng; từ cấm với `post` ⇒ published + có `moderation_item(banned_word)` | 1.0 |
| **LC-5.3** | Báo cáo + `UNIQUE` một người một lần + tự ẩn theo ngưỡng (giảm đếm, **không** báo tác giả) | `internal/service/comment_moderation.go`, `repository/comment_moderation.go` | Báo cáo lần 2 ⇒ `204`, không thêm hàng; đủ ngưỡng ⇒ `status='hidden'`, `comment_count` giảm, không có outbox cho tác giả | 0.75 |
| **LC-5.4** | Hàng chờ + approve/reject + **approve mới ghi buffer** + `comment_audit_logs` | `internal/service/comment_moderation.go` | Duyệt một bình luận `pending` ⇒ thông báo phát **lúc đó**; từ chối ⇒ tác giả nhận `comment.moderated` kèm `note`; mỗi quyết định có 1 hàng audit | 0.75 |
| **LC-5.5** | Nhóm công khai + cache `cmt:pub:` + xoá cache khi siết visibility | `internal/transport/http/comment_public.go`, `internal/service/comment_target.go` | Cửa sổ ẩn danh đọc được Thread public; đổi sang private ⇒ **request tiếp theo** `404`; `POST` ⇒ `405` | 0.75 |
| **LC-5.6** | BFF passthrough `/api/public/ludiskus/comments/*` + rate limit IP + không log query | `tm/bff/src/index.ts`, `bff/src/http.ts` | Không có cookie vẫn đọc được; `POST` ⇒ `405`; 121 request/phút ⇒ `429`; access log không chứa `resource_id` | 0.5 |
| **LC-5.7** | `CommentModerationDialog`, `CommentReportDialog`, tab "Cần duyệt" | `components/comment/*.tsx`, `pages/ludiskus/Comments.tsx` | Moderator duyệt/từ chối được từ giao diện, thấy đủ ngữ cảnh mà không mở trang khác | 0.75 |
| **LC-5.8** | Nhãn trạng thái cho tác giả ("Đang chờ duyệt — chỉ bạn thấy", "Đã bị ẩn: lý do") | `components/comment/CommentItem.tsx` | Bình luận `pending` hiện đúng nhãn, không tính vào số đếm hiển thị | 0.25 |

---

## GĐ6 — Mở cho hệ sinh thái (4–5 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-6.1** | Nhóm S2S đầy đủ + `SERVICE_SCOPE_MISMATCH` | `internal/transport/http/comment_s2s.go`, `internal/service/comment_target.go` | Service A gọi S2S trên Thread của B ⇒ `403`; `targets` 100 ref ⇒ một response | 1.0 |
| **LC-6.2** | Bình luận hệ thống (`author_kind='service'`, `idempotencyKey` bắt buộc) | `internal/service/comment.go` | Gọi 2 lần cùng khoá ⇒ 1 bình luận; không phát `comment.created` cho chủ nội dung | 0.5 |
| **LC-6.3** | `POST /s2s/comments/{id}/moderate` + audit `service:{code}` | `internal/service/comment_moderation.go` | Ẩn được bình luận trên nội dung của mình; thử trên nội dung service khác ⇒ `403` | 0.5 |
| **LC-6.4** | Nhóm quản trị + `warnings[]` khi lưu policy | `internal/transport/http/comment_admin.go` | Lưu `pre` cho service không có `oauth_client_id` ⇒ response có warning nói rõ bình luận sẽ kẹt | 0.5 |
| **LC-6.5** | Trang `/ludiskus/comment-admin` | `tm/frontend/src/pages/ludiskus/CommentAdmin.tsx` | Thêm service, sửa policy, xem cờ abuse, bấm đối soát — tất cả từ giao diện | 0.75 |
| **LC-6.6** | Client Go mẫu + tài liệu tích hợp | `<service>/internal/comment/client.go`, `<service>/docs/…` | Copy vào `lumuse`, gọi `Invalidate` khi xoá phim ⇒ Thread trả `410` | 0.5 |
| **LC-6.7** | Tích hợp thật 3 service theo 3 mẫu (A: `lumuse`; C: `lukode` public; B: `luprojet` hoặc `luskool`) *(song song)* | các repo service + `tm/frontend/src/pages/*` | Ba trang thật chạy đủ: bình luận, thông báo, kiểm duyệt; trang `lukode` công khai đọc được khi chưa đăng nhập | 1.0 |
| **LC-6.8** | Đồng bộ registry với `interaction_services` của `lufami` | vận hành + `lufami` migration/seed nếu thiếu service | Bảng đối chiếu hai registry khớp `code`/`base_url`/`oauth_client_id` | 0.25 |

---

## GĐ7 — Chống lạm dụng, đối soát & hardening (4–6 ngày)

| Mã | Việc | File | Kiểm chứng | Ngày |
|----|------|------|------------|------|
| **LC-7.1** | 5 tín hiệu abuse (SQL trong worker) + trang xử lý (siết / tiền kiểm, **không** ban tự động) | `internal/service/comment_abuse.go`, `comment_worker.go` | Kịch bản spam 50 bình luận/phút trên 10 Target ⇒ sinh cờ `burst`; siết Profile ⇒ `per_minute` giảm ngay (đọc `cmt:rl:override:`) | 1.0 |
| **LC-7.2** | Job đối soát (canh giờ, theo lô 500) + `POST /admin/comments/reconcile-counters` | `internal/service/comment_worker.go` | Sửa lệch `comment_count` bằng tay ⇒ job sửa đúng + 1 hàng audit + log `error`; deploy giữa ngày **không** kích hoạt quét | 0.75 |
| **LC-7.3** | Dọn: target mồ côi, buffer chết, revision dư, audit quá hạn | `internal/service/comment_worker.go` | Target `gone` + 0 bình luận + 31 ngày ⇒ bị xoá; target `gone` **có** bình luận ⇒ **không** bị xoá | 0.5 |
| **LC-7.4** | Sort `top`: cột `score_cache` + job kéo aggregate từ Interaction Platform theo lô | `0007_comment_score.up.sql`, `internal/service/comment_worker.go` | `sort=top` trả đúng thứ tự theo like; `LUFAMI_API_URL=""` ⇒ rơi mềm về `newest` (lúc này được phép) | 1.0 |
| **LC-7.5** | Tải & profiling: Thread 10.000 bình luận, batch 100 ref, resolver treo 30s | script tải trong `scratchpad`, không commit | Trang đầu < 150ms; batch p95 < 150ms; resolver treo **không** làm chậm đường đọc Thread đã verify | 0.75 |
| **LC-7.6** | Hoàn thiện tài liệu: cập nhật `docs/README.md` của ludiskus, `CHANGELOG.md`, ghi trạng thái thật vào bộ tài liệu này | `ludiskus/docs/README.md`, `ludiskus/CHANGELOG.md`, `docs/comment/*` | Mọi liên kết trong 17 file mở được; `README.md` của bộ này ghi đúng trạng thái | 0.5 |

---

## 16.1 Việc làm song song được

| Nhóm | Việc | Phụ thuộc |
|------|------|-----------|
| Backend nền | LC-0.1 → 0.3 → 0.4/0.5 | 0.4 và 0.5 độc lập nhau |
| An ninh | LC-0.7, LC-0.8 | Độc lập, làm ngay từ đầu |
| Frontend | LC-2.1 → 2.2/2.3/2.4 | Chỉ cần API của GĐ1 xong |
| Tích hợp service | LC-6.7 (ba service) | Ba người ba service, sau LC-6.1 |
| Kiểm duyệt vs công khai | LC-5.2/5.3/5.4 ‖ LC-5.5/5.6 | Hai nhánh độc lập trong GĐ5 |

## 16.2 Bảng đối chiếu tài liệu ↔ mã nguồn

| Tài liệu | File mã nguồn chính |
|----------|--------------------|
| [03](03-mo-hinh-mien.md) mô hình miền | `internal/domain/comment.go` |
| [04](04-hop-dong-resource.md) resolver & policy | `internal/resolver/resolver.go`, `internal/service/comment_policy.go`, `comment_target.go` |
| [05](05-cay-va-phan-trang.md) cây & phân trang | `internal/repository/comment.go` |
| [06](06-phan-quyen.md) phân quyền | `internal/service/comment.go` (`ensureReadable`/`ensureCommentable`), `internal/auth/middleware.go` |
| [07](07-kiem-duyet.md) kiểm duyệt | `internal/service/comment_moderation.go` |
| [08](08-noi-dung-va-dinh-kem.md) nội dung | `internal/markdown/markdown.go`, `internal/service/attachment.go` |
| [09](09-thong-bao-va-theo-doi.md) thông báo | `internal/service/comment_notify.go`, `comment_worker.go`, `internal/service/interaction.go` |
| [10](10-database.md) database | `db/migrations/0003…0006` |
| [11](11-backend-api.md) API | `internal/transport/http/comment*.go`, `router.go` |
| [12](12-frontend.md) frontend | `tm/frontend/src/lib/comment.ts`, `components/comment/*` |
| [13](13-tich-hop-service.md) tích hợp | `db/seeds/comment_policies.json`, client Go trong repo service |
| [14](14-trien-khai-docker.md) triển khai | `internal/config/config.go`, `cmd/worker/main.go`, `tm/bff/src/index.ts` |
