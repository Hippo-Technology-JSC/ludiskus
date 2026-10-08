# 05 — Bỏ phiếu & kiểm phiếu

## 5.1 Lá phiếu gửi lên

```
PUT /api/v1/polls/{id}/vote
Idempotency-Key: 6f1c…                       (bắt buộc, ≤ 80 ký tự)
{
  "choices": [ { "optionId": "…", "rank": 1 }, … ],   // ranked: rank 1..k
  "answers": [ { "optionId": "…", "answer": "yes" } ], // CHỈ schedule: yes | maybe
  "notifyResult": true
}
```

| Kind | Hợp lệ khi |
|------|-----------|
| `single`, `scale` | `choices` đúng 1 phần tử, không `rank` |
| `multiple` | `min_choices ≤ len(choices) ≤ max_choices`, không trùng |
| `ranked` | `1 ≤ len ≤ max_ranks`; `rank` là đúng tập `{1..len}` (không trùng, không thủng) |
| `schedule` | `answers` 0..n phần tử (0 = "không khung nào được" — **là phiếu hợp lệ**, vẫn tạo Receipt); `maybe` chỉ khi `allow_maybe` |

Mọi `optionId` phải thuộc poll và `status='active'`. Sai ⇒ `422 VOTE_INVALID` với
`details.field`. **Thứ tự trong mảng không có nghĩa** (trừ qua `rank`) — server sắp lại.

## 5.2 Ghi phiếu — một transaction

```sql
-- (1) khoá + kiểm còn mở, nguyên tử (QĐ-P10). statement_timestamp() chứ không now():
--     now() là thời điểm BẮT ĐẦU transaction, một tx mở lúc 19:59:59.9 sẽ lọt hạn chót.
UPDATE polls
   SET ballot_version = ballot_version + 1,
       first_vote_at  = COALESCE(first_vote_at, statement_timestamp())
 WHERE id = $1
   AND status = 'published'
   AND (opens_at  IS NULL OR opens_at  <= statement_timestamp())
   AND (closes_at IS NULL OR closes_at >  statement_timestamp())
RETURNING kind, identity_mode, allow_change_vote, config, …;
-- 0 hàng ⇒ ROLLBACK, 409 POLL_CLOSED (hoặc POLL_NOT_OPEN — đọc lại để phân biệt)

-- (2) Receipt
INSERT INTO poll_voters (poll_id, profile_uuid, idempotency_key, notify_result)
VALUES ($1, $me, $key, $notify)
ON CONFLICT (poll_id, profile_uuid) DO UPDATE SET updated_at = now()
RETURNING (xmax = 0) AS inserted, idempotency_key AS prev_key;
```

| Kết quả bước 2 | Xử lý |
|----------------|-------|
| `inserted` | Phiếu mới: `voter_count + 1`, ghi phiếu, delta dương |
| Đã có, `prev_key = $key` | **Phát lại**: ROLLBACK, trả trạng thái hiện tại `200` — không đổi gì, `ballot_version` không tăng |
| Đã có, khác key, `allow_change_vote` | Đổi phiếu (§5.4) |
| Đã có, khác key, không cho đổi (gồm mọi `secret`) | ROLLBACK, `409 ALREADY_VOTED` |

```
(3) ghi poll_votes (hoặc poll_ballots nếu secret)
(4) applyOptionDeltas(tx, deltas)   -- sắp theo option_id tăng dần (QĐ-P11), một UPDATE mỗi lựa chọn
(5) UPDATE polls SET voter_count = voter_count + Δ  (cùng hàng đã khoá ở (1))
COMMIT
```

Sau COMMIT (ngoài transaction, best-effort): xoá `poll:res:{id}:*`, `poll:sum:{id}`.

**Deadlock.** Hàng `polls` luôn bị khoá **trước** (bước 1), nên hai lượt trên cùng poll đã
tuần tự hoá ở đó và về lý thuyết không chạm lựa chọn ngược thứ tự. Vẫn bắt buộc sắp
`option_id` ở bước 4 vì: (a) lượt **xoá lựa chọn bởi kiểm duyệt** (`hidden` ⇒ trừ phiếu)
không đi qua bước 1 theo cùng thứ tự nếu ai đó viết sai; (b) job đối soát cập nhật hàng loạt.
Một quy tắc rẻ hơn một sự cố.

## 5.3 Bốn mức danh tính

| Mức | Ai biết **ai chọn gì** | Lưu ở | Đổi/rút phiếu | Dùng khi |
|-----|------------------------|-------|---------------|----------|
| `public` | Mọi người xem được kết quả | `poll_votes` | Theo cấu hình | Chọn quán, chọn ngày — minh bạch là mục đích |
| `owner_only` | Chỉ người tạo (C) và service sở hữu (S) | `poll_votes` | Theo cấu hình | Giáo viên hỏi lớp, quản lý hỏi nhóm |
| `anonymous` | **Không ai qua API/giao diện.** Hệ thống vẫn liên kết để bạn đổi được phiếu | `poll_votes` | Theo cấu hình | Phản hồi thật lòng, vẫn muốn đổi ý được |
| `secret` | **Không ai, kể cả hệ thống lúc chạy** — không có cột nào liên kết | `poll_ballots` | **Không bao giờ** | Bầu người, biểu quyết nhạy cảm |

Giao diện chọn mức danh tính **phải** hiện đúng câu trong cột "Ai biết ai chọn gì" — không được
viết gọn thành "ẩn danh" cho cả hai mức cuối.

### Giới hạn trung thực của `secret`

`poll_ballots` không có Profile, không có thời điểm, `id` ngẫu nhiên. Nhưng:

1. **Thứ tự vật lý.** Hàng mới thường nằm cuối heap; người có quyền đọc trực tiếp Postgres có
   thể ghép `ctid` của phiếu với `voted_at` của Receipt. Không giải được trong Postgres dùng
   chung mà không thêm hạ tầng trộn phiếu. ⇒ `secret` **không chống DBA** — nói rõ ở
   [README](README.md) và trên giao diện.
2. **WAL, bản sao lưu, log truy vấn chậm** có thể chứa câu `INSERT` cùng thời điểm với câu
   `INSERT` Receipt. Cấm bật `log_statement=all` trên production (đã là quy ước); tham số
   phiếu đi qua `$n`, không nối chuỗi.
3. **Log ứng dụng.** Handler `vote` **không** log body; `slog` chỉ ghi `poll_id`, `kind`,
   `status_code`. Có test grep đầu ra log khi bỏ phiếu `secret` ([14 LP-4.6](14-cong-viec-chi-tiet.md)).

Đó là lý do mức này tên là `secret` chứ không phải "bảo mật tuyệt đối", và kết quả xuất mang
nhãn "không có giá trị pháp lý".

### Tấn công phép trừ (áp cho `anonymous` và `secret`)

Nếu kết quả lộ **trong khi poll đang mở** (`always`, `after_vote`), người quan sát hỏi kết quả
ngay trước và ngay sau khi một người cụ thể bỏ phiếu ("mình vừa bấm xong") và **trừ** hai số là
biết người đó chọn gì. `ballot_version` trong ETag còn làm việc này dễ hơn — nó báo chính xác
lúc có phiếu mới.

Ba biện pháp, đều bắt buộc với `anonymous`/`secret` khi poll đang mở:

1. **Ngưỡng tối thiểu** `min_voters_for_results` (mặc định 3, tối thiểu 2): dưới ngưỡng, không ai
   (kể cả C) thấy số đếm — `resultsHiddenReason: "min_voters"`.
2. **Kết quả theo ảnh chụp**, không theo thời gian thực: số đếm trả ra lấy từ
   `poll_results` (bản tạm, `final=false`) được làm mới khi **cả hai**: đã qua
   `LUDISKUS_POLL_ANON_SNAPSHOT_INTERVAL` (mặc định 5 phút) **và** có ≥ 2 phiếu mới. Một phiếu
   lẻ không bao giờ làm số nhảy một mình.
3. **ETag theo ảnh chụp**, không theo `ballot_version`.

Khi poll **đóng**, kết quả cuối được công bố đầy đủ (kể cả dưới ngưỡng) — poll 2 người bỏ phiếu
về bản chất không ẩn danh được, và giao diện tạo poll cảnh báo điều này khi chọn
`anonymous`/`secret` với `who_can_vote = invited` và ít hơn 5 người được mời.

## 5.4 Đổi phiếu, rút phiếu

**Đổi** (Receipt đã có, khác `Idempotency-Key`, `allow_change_vote = true`):

```
old := SELECT option_id, rank, answer FROM poll_votes WHERE poll_id=$1 AND voter_profile_uuid=$me FOR UPDATE
deltas := diff(old, new)        -- theo từng option_id: -1, 0, +1 (vote_count) và maybe_count
DELETE các hàng bỏ, INSERT các hàng thêm, UPDATE rank/answer hàng giữ
applyOptionDeltas(deltas)       -- voter_count KHÔNG đổi
UPDATE poll_voters SET idempotency_key = $key, updated_at = now()
```

Gửi lại **đúng** lá phiếu cũ với key mới ⇒ `deltas` rỗng ⇒ vẫn `200`, `ballot_version` vẫn tăng
(đơn giản hơn so sánh trước); chấp nhận.

**Rút** (`DELETE /polls/{id}/vote`, `allow_retract = true`, không `secret`): xoá phiếu + Receipt,
`voter_count − 1`, delta âm. Rút xong bỏ phiếu lại được (Receipt mới).

`ranked`: `vote_count` của lựa chọn chỉ phản ánh **số phiếu xếp hạng 1** (để thẻ nhỏ hiển thị
nhanh); kết quả thật luôn tính từ phiếu (§5.5).

## 5.5 Kiểm phiếu

Hàm thuần trong `service/poll_tally.go`, không truy cập DB, nhận `[]Ballot` và cấu hình — để
test bằng bảng ca.

### `single`, `scale`

- Số phiếu mỗi lựa chọn = `vote_count`.
- Phần trăm trên `voter_count`, làm tròn **phương pháp phần dư lớn nhất** (Hamilton) đến 1 chữ
  số thập phân để **tổng luôn đúng 100,0%**. Làm tròn từng số riêng cho ra 99,9% hoặc 100,1% —
  người dùng thấy ngay và mất tin.
- Người thắng: mọi lựa chọn có số phiếu lớn nhất (có thể > 1 ⇒ `tie: true`). **Không** tự phá
  hoà ở `single`.
- `scale`: thêm `mean` (2 chữ số), `median`, phân bố. Không thêm độ lệch chuẩn ở v1.

### `multiple`

Số phiếu mỗi lựa chọn; phần trăm trên `voter_count`, **không** chuẩn hoá (tổng > 100% là đúng
và giao diện ghi chú "mỗi người chọn được nhiều phương án").

### `schedule`

Mỗi khung: `yes`, `maybe`. "Khung tốt nhất" = `yes` lớn nhất; hoà ⇒ `maybe` lớn nhất; vẫn hoà ⇒
khung bắt đầu sớm nhất. Trả cả danh sách đã sắp, không chỉ người thắng.

### `ranked` — IRV (`method = "irv"`)

Đầu vào: mỗi lá phiếu là danh sách `option_id` theo hạng, **đã lọc** các lựa chọn `hidden`
(hạng sau dồn lên).

```
continuing := mọi lựa chọn active
lặp vòng r = 1, 2, …:
  mỗi phiếu đếm cho lựa chọn ĐẦU TIÊN của nó còn trong continuing; không còn ⇒ exhausted
  active_total := tổng phiếu không exhausted
  nếu ∃ x: count[x] * 2 > active_total             ⇒ x thắng, dừng
  nếu |continuing| = 2 và hai số bằng nhau          ⇒ hoà, dừng (winner = null, tie = [a, b])
  nếu |continuing| = 1                              ⇒ thắng, dừng
  loại:
    - vòng 1: loại MỘT LẦN mọi lựa chọn có 0 phiếu (không thể ảnh hưởng kết quả)
    - còn lại: loại lựa chọn có count nhỏ nhất; hoà ở đáy thì phá hoà theo thứ tự:
        (a) số phiếu ở vòng trước đó, lùi dần về vòng 1 — nhỏ hơn bị loại
        (b) tổng số lần được xếp hạng ở bất kỳ vị trí nào — ít hơn bị loại
        (c) `position` lớn hơn bị loại — ghi tie_break = "position"
```

Đầu ra (lưu nguyên vào `poll_results.result` khi chốt):

```json
{ "method": "irv", "winner": "opt-B", "tie": [],
  "rounds": [
    { "counts": {"opt-A": 41, "opt-B": 38, "opt-C": 21}, "exhausted": 0,
      "eliminated": ["opt-C"], "tieBreak": null },
    { "counts": {"opt-A": 47, "opt-B": 50}, "exhausted": 3, "eliminated": [], "tieBreak": null } ] }
```

Luật (c) dựa vào thứ tự do người tạo sắp — **phải** hiện ra trong giao diện ("hoà ở vòng 3, loại
theo thứ tự lựa chọn") vì đó là quyết định tuỳ ý. Luật (a)–(c) là một trong nhiều biến thể IRV;
chọn vì xác định được và giải thích được, ghi ở đây để không ai "sửa" thành biến thể khác mà
làm đổi kết quả của poll đã chốt (poll đã chốt không bao giờ tính lại — đọc `poll_results`).

### `ranked` — Borda (`method = "borda"`)

Borda cắt ngắn: với `m = max_ranks`, hạng `i` (1-based) được `m − i + 1` điểm; lựa chọn không
được xếp hạng được 0. Xếp theo tổng điểm; hoà ⇒ nhiều hạng 1 hơn; vẫn hoà ⇒ `tie`.

### Chi phí

Đọc phiếu `ranked`: một truy vấn `ORDER BY voter_profile_uuid, rank` (hoặc toàn bộ
`poll_ballots`), gom trong Go. Trần: 10.000 người × 5 hạng = 50.000 hàng ⇒ ≤ 150 ms tính cả DB
(cổng [13 GĐ4](13-lo-trinh.md)). Cache Redis `poll:res:{id}:{ballot_version}` TTL 10 phút.
Vượt 50.000 người bỏ phiếu ⇒ chỉ phục vụ ảnh chụp tạm làm mới mỗi phút (giống §5.3) — ghi rõ
trong response `computedAt`.

## 5.6 Lộ kết quả

```go
func resultsVisible(p Poll, viewer Viewer, now time.Time) (bool, reason string)
  C hoặc S                                   ⇒ true  (trừ ngưỡng min_voters khi anonymous/secret và đang mở)
  owner_only                                 ⇒ false, "owner_only"
  always                                     ⇒ true
  after_vote                                 ⇒ viewer có Receipt || closed
  after_close                                ⇒ closed
  sau đó: anonymous/secret && open && voter_count < min_voters ⇒ false, "min_voters"
```

Khi `false`: response có `results: null` và `resultsHiddenReason`. Không bao giờ trả số đếm
từng lựa chọn rồi để giao diện che. `voterCount` (tổng người tham gia) thì **được** trả trừ khi
`owner_only` — biết "đã có 37 người tham gia" không lộ ai chọn gì.

`after_vote` có một bẫy mềm: người muốn xem kết quả mà chưa muốn chọn sẽ bỏ đại một phiếu. Giao
diện **không** có nút "xem kết quả"; người tạo nên chọn `after_close` nếu điều này quan trọng —
gợi ý hiện ở hộp thoại tạo poll.

**ETag và cache** — phụ thuộc người xem, nên:

| Route | Cache-Control | ETag dựng từ |
|-------|---------------|--------------|
| `GET /polls/{id}` (người dùng) | `private, no-cache` | `ballot_version` (hoặc version ảnh chụp, §5.3) + `updated_at` + `hasVoted` + `resultsVisible` + lớp vai trò (C/S/khác) |
| `GET /public/polls/{id}` | `public, max-age=15` **chỉ khi** kết quả lộ cho mọi người (`always`, hoặc đã đóng và không `owner_only`); còn lại `public, max-age=15` với `results: null` | version như trên, **không** gồm trạng thái người xem |

Thiếu `hasVoted` trong ETag ⇒ người vừa bỏ phiếu nhận `304` của bản "chưa được xem kết quả" và
giao diện không bao giờ hiện kết quả — lỗi im lặng, test phải bắt ([14 LP-1.9](14-cong-viec-chi-tiet.md)).

Giao diện hỏi lại `GET /polls/{id}` mỗi 15s khi poll **đang mở và trong khung nhìn**
(`IntersectionObserver` + `document.visibilityState`), dừng khi đóng.

## 5.7 Danh sách người bỏ phiếu

`GET /polls/{id}/voters?optionId=…&cursor=…` — chỉ khi `identity_mode ∈ (public, owner_only)`
và quyền theo ma trận [04 §4.7](04-gan-ket-va-phan-quyen.md). Keyset `(voted_at, profile_uuid)`,
50/trang. Trả `name`, `avatar`, `code` từ `profile_cache` — không trả `profile_uuid` thô cho
nhóm công khai.

`GET /polls/{id}/participants` — Receipt (ai đã tham gia, không có lựa chọn): C, S, và M khi
`who_can_vote = members`. Có ở mọi mức danh tính (§3.1 Receipt).
