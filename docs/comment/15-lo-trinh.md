# 15 — Lộ trình

Tám giai đoạn, mỗi giai đoạn là một **lát cắt dọc** chạy được end-to-end. Tiêu chí "xong" là
điều kiện **chặn** sang giai đoạn sau, không phải danh sách mong muốn. Mỗi giai đoạn: `go build
./...` + `go vet ./...` + test sạch, **verify SQL thật** trên Postgres của compose, cập nhật
`ludiskus/CHANGELOG.md`.

| GĐ | Tên | Giao được gì | Ước lượng |
|----|-----|--------------|-----------|
| 0 | Nền dữ liệu, registry, resolver | Lược đồ + resolver chạy thật, chưa có API bình luận | 3–4 ngày |
| 1 | Đọc/viết lõi | Viết & đọc Thread qua API, đếm đúng | 5–6 ngày |
| 2 | Component `tm` + dogfood | Bình luận thật trên một service thật | 4–5 ngày |
| 3 | Cây, ghim, sửa/xoá, tìm trong Thread | Trải nghiệm bình luận đầy đủ | 4–5 ngày |
| 4 | Thông báo & hộp thư | lunoti + theo dõi + trang "Bình luận của tôi" | 4–5 ngày |
| 5 | Kiểm duyệt & đọc công khai | 4 chế độ, báo cáo, hàng chờ, trang công khai | 5–6 ngày |
| 6 | Mở cho hệ sinh thái | ≥ 3 service tích hợp thật + trang quản trị | 4–5 ngày |
| 7 | Chống lạm dụng, đối soát, hardening | Cờ abuse, đối soát, sort `top`, tải | 4–6 ngày |

Tổng: **33–42 ngày người**. GĐ0–GĐ4 là MVP có ích (20–25 ngày). GĐ5 bắt buộc trước khi mở cho
nội dung công khai. GĐ6–GĐ7 mở rộng và làm chắc.

---

## GĐ0 — Nền dữ liệu, registry & resolver

- [ ] Migration `0003_comment_core` + `.down.sql` ([10 §10.3](10-database.md)).
- [ ] `internal/domain/comment.go`: `ResourceRef` (+`Validate`), `CommentTarget`, `Comment`,
      `CommentPolicy`, `Capabilities`, `DefaultCommentPolicy`, `countDelta`.
- [ ] `internal/repository/comment_target.go`, `comment_policy.go` (pgx, không JOIN forum).
- [ ] `internal/resolver/resolver.go`: token client-credentials + cache Redis + batch + timeout
      + **dò đường `resource-context` → `interaction-context`** ([04 §4.3](04-hop-dong-resource.md)).
- [ ] `internal/service/comment_policy.go`: hợp nhất 4 tầng + cache version `cmt:pol:v`.
- [ ] `db/seeds/comment_policies.json` + nạp trong `loadSeeds()`.
- [ ] **Sửa `internal/auth`**: `ServiceMiddleware` chặn token có `sub`, đưa `aud` vào context
      ([06 §6.7](06-phan-quyen.md)). Đây là việc **chặn**, không phải việc của GĐ6.
- [ ] `internal/service/comment_arch_test.go` (ranh giới [02 §2.4](02-kien-truc.md)).

**Xong khi (chặn):**

- [ ] `up → down → up` trên DB **có dữ liệu thật** không lỗi.
- [ ] Resolver lấy được metadata thật từ `lukolek` (đang cài `interaction-context`) **và** ghi
      `context_path='interaction-context'` sau lần đầu; lần thứ hai không có request dò.
- [ ] Hợp nhất policy đúng cho cả 4 tầng, có test: tầng 4 **không** nới rộng được cờ bị tắt,
      số lấy `min`, danh sách lấy giao.
- [ ] Token người dùng gọi `/api/v1/s2s/interaction-context/…` nhận `403` (trước đó nhận `200`).
- [ ] `arch_test` đỏ khi thêm `s.repo.GetTopic` vào file `comment_*.go`.

---

## GĐ1 — Đọc/viết lõi

- [ ] `repository/comment.go`: insert, keyset root, `LATERAL` reply mồi, `bumpCounts`.
- [ ] `service/comment.go`: `ensureReadable`, `ensureCommentable`, `Create`, `List`, `Thread`.
- [ ] `service/comment_target.go`: `ensureTarget` theo 3 chế độ verify + `invalidate`.
- [ ] `markdown`: `RenderBasic`/`RenderPlain` + 3 policy bluemonday ([08 §8.2](08-noi-dung-va-dinh-kem.md)).
- [ ] `service/comment_abuse.go`: rate limit Redis + chặn trùng `body_hash`.
- [ ] `transport/http/comment.go`: nhóm đọc + `POST items` + `POST /comments/summary`.
- [ ] `ETag`/`If-None-Match` cho `GET /comments/r/…`.

**Xong khi (chặn):**

- [ ] Tạo bình luận trên một ref của `lukolek` qua `curl` với token thật ⇒ `201`, `comment_count = 1`.
- [ ] Hai goroutine cùng gửi một `Idempotency-Key` ⇒ **một** hàng, `comment_count = 1`.
- [ ] 20 XSS payload × 3 chế độ markdown ⇒ đầu ra không có `<script`, `on*=`, scheme lạ.
- [ ] Đọc Thread 20 root + 3 reply/root sinh **đúng 3** truy vấn (bật `log_statement=all` mà đếm).
- [ ] Nội dung `private` của người khác ⇒ `403`; nội dung đã xoá ⇒ `410`.
- [ ] `sort=top` ⇒ `400 SORT_NOT_SUPPORTED` (không âm thầm rơi về `newest`).

---

## GĐ2 — Component `tm` + dogfood

- [ ] `lib/comment.ts` (type + client + cursor + `commentKey`).
- [ ] `CommentProvider`, `CommentThread`, `CommentList`, `CommentItem`, `CommentComposer`,
      `CommentCount`.
- [ ] Nhúng thật vào **một** trang: `lukolek` chi tiết vật phẩm (đã có resolver ⇒ ít việc nhất).
- [ ] Optimistic UI + hàng đợi bản nháp `localStorage`.
- [ ] Gắn `<InteractionBar>` cho từng bình luận + nhánh `case "comment"` trong
      `service.InteractionContext` ([09 §9.7](09-thong-bao-va-theo-doi.md)).

**Xong khi (chặn):**

- [ ] Bình luận thật trên trang thật qua BFF, F5 vẫn thấy, đúng tên/avatar.
- [ ] Like một bình luận qua `<InteractionBar>` ⇒ `lufami` ghi resource `ludiskus:comment:{id}`.
- [ ] Feed 20 thẻ có `<CommentCount>` ⇒ **một** request `POST /comments/summary`.
- [ ] Mất mạng khi gửi ⇒ nội dung không mất; có mạng lại ⇒ gửi đúng một lần.
- [ ] Nghiệm thu giao diện theo [12 §12.8](12-frontend.md) (Chrome thật, so A/B với `HEAD`).

---

## GĐ3 — Cây, ghim, sửa/xoá, tìm

- [ ] Trả lời + **làm phẳng** khi vượt `max_depth` + `reply_to_profile_uuid`.
- [ ] `GET /items/{id}/replies` (cursor riêng), `CommentReplies.tsx`.
- [ ] Sửa trong cửa sổ + `comment_revisions` + nhãn "đã sửa" + `GET /revisions`.
- [ ] Xoá mềm + **bia mộ** khi còn trả lời ([03 §3.7](03-mo-hinh-mien.md)).
- [ ] Ghim/bỏ ghim theo `policy.pin.by` + `max_pinned`.
- [ ] `@mention` đủ đường: trích → scope → `comment_mentions` → `mention-suggest` + `MentionInput`.
- [ ] Đính kèm: `attachments.comment_id` (migration `0005`), `AttachmentPicker`.
- [ ] Tìm trong Thread (`search_tsv`).

**Xong khi (chặn):**

- [ ] Trả lời ở độ sâu 3 với `max_depth=2` ⇒ `depth=2`, `parent_id` = tổ tiên đúng,
      giao diện hiện `@tên`.
- [ ] Xoá bình luận có 2 trả lời ⇒ bia mộ, `bodyHtml` **không** có trong response, cây không vỡ.
- [ ] Sửa sau cửa sổ ⇒ `403 EDIT_WINDOW_CLOSED`; sửa trong cửa sổ ⇒ có hàng `comment_revisions`.
- [ ] Mention người ngoài Space với `scope='space'` ⇒ **không** có hàng `comment_mentions`.
- [ ] Đính kèm ảnh, `up → down → up` migration `0005` sạch, `attachments_owner_one` chặn được
      hàng có cả `post_id` và `comment_id`.
- [ ] Tìm "đieu" khớp bình luận chứa "điều" (bỏ dấu).

---

## GĐ4 — Thông báo & hộp thư

- [ ] Migration `0006` (buffer + audit).
- [ ] `service/comment_notify.go`: ghi buffer trong cùng transaction; fan-out theo
      [09 §9.4](09-thong-bao-va-theo-doi.md).
- [ ] Worker `FlushCommentNotify` (10s) + **kiểm lại quyền đọc từng người nhận lúc flush**.
- [ ] 5 event-type + template trong `lunoti_event_types.json`.
- [ ] Theo dõi/mute/đã đọc + `GET /comments/inbox` + `/unread-count` + `/mine`.
- [ ] Trang `/ludiskus/comments` (3 tab) + `/ludiskus/c/:id`.

**Xong khi (chặn):**

- [ ] 100 bình luận trong 1 phút vào một Thread ⇒ **≤ 1** thông báo/người trong cửa sổ 5 phút,
      nội dung dạng "A và 99 người khác…".
- [ ] Tự bình luận nội dung của mình ⇒ **không** có thông báo nào.
- [ ] Người bị loại khỏi Space giữa lúc buffer chờ ⇒ **không** nhận thông báo (kiểm bằng test
      tích hợp, không phải bằng mắt).
- [ ] Mention ⇒ thông báo **ngay** (không đợi debounce), có `email` nếu người nhận bật.
- [ ] Bấm thông báo ⇒ tới đúng `canonicalPath#comment-{id}` và bình luận được tô sáng.

---

## GĐ5 — Kiểm duyệt & đọc công khai

*Điều kiện chặn của phần công khai: passthrough BFF ở [14 §14.4](14-trien-khai-docker.md).*

- [ ] Migration `0004` (enum) **và** `0005` phần báo cáo/kiểm duyệt; nới
      `moderation_items.space_uuid` thành nullable.
- [ ] 4 chế độ kiểm duyệt + từ cấm (tái dùng `matchesBanned`) + `first_comment` theo Target.
- [ ] Báo cáo + tự ẩn theo ngưỡng + hàng chờ + approve/reject + `comment_audit_logs`.
- [ ] Thông báo `comment.pending` / `comment.moderated`; **approve mới ghi buffer**.
- [ ] `transport/http/comment_public.go` + BFF passthrough + cache `cmt:pub:` + **xoá cache khi
      siết visibility**.
- [ ] `CommentModerationDialog`, `CommentReportDialog`, tab "Cần duyệt".

**Xong khi (chặn):**

- [ ] `mode=pre`: bình luận không hiện với người khác; moderator duyệt ⇒ hiện **và** thông báo
      phát **lúc đó** (không phải lúc tạo).
- [ ] `mode=first_comment`: bình luận thứ hai của cùng người trong cùng Target đăng thẳng.
- [ ] Từ cấm với `mode=post` ⇒ vẫn published + có `moderation_item(source=banned_word)`.
- [ ] Đủ ngưỡng báo cáo ⇒ tự ẩn, `comment_count` giảm, tác giả **không** nhận thông báo tự ẩn.
- [ ] Trang công khai (cửa sổ ẩn danh) đọc được Thread của nội dung public; đổi nội dung sang
      private ⇒ **request tiếp theo** trả `404` (không đợi TTL).
- [ ] `POST` vào `/api/public/ludiskus/comments/*` ⇒ `405`.
- [ ] `up → down → up` cho `0004`/`0005`; ghi rõ trong PR rằng `down` của `0004` không xoá được
      giá trị enum.

---

## GĐ6 — Mở cho hệ sinh thái

- [ ] Nhóm S2S đầy đủ (`targets`, `invalidate`, `settings`, `counts`, `items`, `moderate`,
      `export`) + `requireServiceClient` + `SERVICE_SCOPE_MISMATCH`.
- [ ] Nhóm quản trị + trang `/ludiskus/comment-admin` + `warnings[]` khi lưu policy.
- [ ] Client Go mẫu ([13 §13.4](13-tich-hop-service.md)) + tài liệu tích hợp trong repo service.
- [ ] Tích hợp thật **ba** service theo ba mẫu khác nhau: `lumuse` (mẫu A), `lukode`
      presentation công khai (mẫu C), `luskool` hoặc `luprojet` (mẫu B, `strict`).
- [ ] Đồng bộ registry với `interaction_services` của `lufami`.

**Xong khi (chặn):**

- [ ] Ba service chạy thật: bình luận, thông báo, kiểm duyệt, và **một** service dùng
      `POST /s2s/comments/{id}/moderate` từ giao diện staff của nó.
- [ ] Service A gọi S2S trên Thread của service B ⇒ `403 SERVICE_SCOPE_MISMATCH`.
- [ ] Bình luận hệ thống (`author_kind='service'`) hiện đúng, gọi lại cùng `idempotencyKey` ⇒
      không tạo bản thứ hai.
- [ ] Lưu policy `pre` cho service không có `oauth_client_id` ⇒ response có `warnings[]` nói rõ
      bình luận sẽ kẹt.

---

## GĐ7 — Chống lạm dụng, đối soát & hardening

- [ ] `comment_abuse_flags` + 5 tín hiệu + trang xử lý (siết/tiền kiểm, **không** ban tự động).
- [ ] Job đối soát đêm + `POST /admin/comments/reconcile-counters`.
- [ ] Dọn: target mồ côi, buffer chết, revision dư, audit quá hạn.
- [ ] Sort `top`: cột `score_cache` + job kéo aggregate từ Interaction Platform theo lô.
- [ ] Đo tải; quyết định có cần partition (ngưỡng ở [10 §10.10](10-database.md)).

**Xong khi (chặn):**

- [ ] Sửa lệch số đếm bằng tay trong DB ⇒ job đêm phát hiện, sửa đúng, ghi audit.
- [ ] Một Thread nhận 10.000 bình luận ⇒ trang đầu vẫn < 150ms; cursor tới trang cuối không
      dùng `OFFSET`.
- [ ] Kịch bản spam (1 Profile, 50 bình luận/phút, 10 Target) ⇒ bị `429` sau ngưỡng, sinh cờ
      `burst`, **không** có bình luận nào lọt quá giới hạn.
- [ ] `sort=top` trả đúng thứ tự theo số like của `lufami` và tự rơi về `newest` khi
      `LUFAMI_API_URL` rỗng (lúc này mới được rơi mềm, vì cờ đã bật).

---

## 15.8 Kiểm thử

**Unit** (`service`, không cần DB):
hợp nhất policy 4 tầng · `countDelta` cho **mọi** cặp trạng thái × root/reply · làm phẳng cây ở
mọi `max_depth` 0..5 · validate `canonical_path` (chặn `//evil.com`, `javascript:`, `../`,
URL tuyệt đối) · sanitize 20 payload XSS × 3 mức markdown · scope mention · quyết định
gửi/không gửi thông báo · dựng/giải cursor (kể cả `created_at` trùng nhau) · `body_hash` chuẩn
hoá (`**spam**` và `s p a m` cùng khớp từ cấm).

**Integration** (Postgres thật, mẫu `*_integration_test.go`):
`UNIQUE (service_code, resource_type, resource_id)` chặn Target trùng · `ON DELETE CASCADE` khi
xoá Target · `attachments_owner_one` · keyset phân trang không lặp/không nhảy khi chèn giữa hai
trang · `LATERAL` reply mồi không N+1 · `comment_count_check` khớp đếm thủ công · vòng đời kiểm
duyệt đủ 6 nhánh · `up → down → up`.

**Đồng thời:**
hai goroutine cùng `Idempotency-Key` ⇒ 1 hàng · 10 goroutine cùng bình luận một Target ⇒
`comment_count = 10`, không lệch · xoá và trả lời xen kẽ ⇒ `reply_count` không âm · flush buffer
từ 2 worker ⇒ mỗi nhóm đúng một hàng outbox (`FOR UPDATE SKIP LOCKED`).

**E2E qua BFF:** đăng nhập → bình luận nội dung của service khác → trả lời → được thông báo →
sửa → xoá → báo cáo → moderator duyệt → mở bằng phiên ẩn danh (nội dung public) → siết
visibility → ẩn danh nhận `404`.

**Tải:** batch 100 ref (p95 < 150ms cache nóng) · Thread 10.000 bình luận · 100 bình luận/phút
vào một Thread (kiểm gom thông báo) · resolver của một service treo 30s (kiểm `optimistic`
không chặn).

## 15.9 Sau v1

Theo thứ tự giá trị/rủi ro:

1. **Bình luận theo neo** (`anchor` đã có cột): bình luận trên một dòng mã (`lukode`), một
   vùng ảnh (`lukomik`), một mốc thời gian video (`lumuse`). Việc khó không phải lưu neo mà là
   **giải neo khi nội dung đổi** — cần chiến lược "neo mờ" và trạng thái `anchor_lost`.
2. **Realtime**: SSE một chiều cho một Thread đang mở (nhẹ hơn WebSocket, không cần container
   mới nếu chấp nhận giữ kết nối trên `ludiskus-api`), hoặc dùng `lugame-realtime` làm cổng chung.
3. **Bình luận của khách**: cần captcha, chặn theo IP, hàng chờ riêng, và `guest` trong policy
   (đã để sẵn khoá). Chỉ mở cho `luwep`/`lukode` public với tiền kiểm bắt buộc.
4. **Kiểm duyệt bằng `lulama`**: phân loại độc hại/spam cho bình luận `pending` để xếp thứ tự
   hàng chờ. `lulama` chạy máy chủ riêng ⇒ phải là phụ thuộc mềm, có hàng đợi.
5. **Digest ngày**: gom mọi hoạt động bình luận thành một email/ngày qua lunoti.
6. **Điểm hipt**: nhiệm vụ "bình luận đầu tiên", "được 10 lượt thích" — nối vào `internal/hipt`
   đã có. **Cần chống farm trước**, nếu không sẽ đẻ ra spam để lấy điểm.
7. **Tách thành `lucomment`**: schema, package, prefix API và migration đã tách sẵn (QĐ-1 +
   [02 §2.4](02-kien-truc.md)); chỉ đổi mapping ở `tm/bff/src/config.ts` và `base_url` trong
   registry của `lufami`.

## 15.10 Rủi ro & lưu ý

| Rủi ro | Mức | Cách chặn |
|--------|-----|-----------|
| **Rò rỉ nội dung riêng tư qua Thread** | Cao | Quyền đọc **luôn** dẫn xuất từ `target.visibility`, một hàm duy nhất (`ensureReadable`); `unverified` ⇒ `private`; `connections` ⇒ siết thành `private`; kiểm lại quyền **lúc flush** thông báo |
| **Rò rỉ qua cache đọc công khai** | Cao | `invalidate` phải xoá `cmt:pub:*`; cache TTL 30s; **không bao giờ** dùng cache để quyết định quyền |
| **XSS qua thân bình luận** | Cao | 3 policy bluemonday dựng sẵn; `basic`/`plain` không cho `<img>`/HTML thô; test 20 payload là điều kiện chặn GĐ1 |
| **Open redirect qua `canonical_path`** | Trung bình | Chỉ nhận đường dẫn tương đối đã validate regex; URL tuyệt đối bị từ chối ở tầng resolver, không phải ở frontend |
| **Lệch số đếm** | Trung bình | `UPDATE ±1` một câu trong cùng tx, `CHECK >= 0`, view đối soát, job đêm, log `error` |
| **Bão thông báo** | Trung bình | Buffer gom nhóm là **bắt buộc**; test 100 bình luận/phút là điều kiện chặn GĐ4 |
| **Enum `report_target`** | Trung bình | QĐ-12: tách migration. Sai điều này thì deploy **thất bại ngay** (dễ phát hiện) nhưng lại rất dễ mắc |
| **Bình luận kẹt `pending`** | Trung bình | `warnings[]` khi lưu policy + chỉ số "item mồ côi > 7 ngày" ([14 §14.6](14-trien-khai-docker.md)) |
| **`resource_id` đổi dạng canonical** | Trung bình | Nêu rõ trong checklist; đổi giữa chừng ⇒ Thread mồ côi, **không có** cách hợp tự động |
| **Service chết làm hỏng trang** | Thấp | `optimistic` mặc định; resolver timeout 3s; phụ thuộc mềm với lunoti/lufami; `readyz` không kiểm service ngoài |
| **`ServiceMiddleware` quá lỏng** | Cao | Việc **chặn** ở GĐ0, không để tới GĐ6 ([06 §6.7](06-phan-quyen.md)) |
| **Lẫn Comment với Post** | Thấp nhưng tốn kém | `arch_test` + ranh giới ở [README](README.md); review PR phải bắt được mọi JOIN sang `posts` |
