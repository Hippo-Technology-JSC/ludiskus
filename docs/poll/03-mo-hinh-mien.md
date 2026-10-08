# 03 — Mô hình miền

## 3.1 Thực thể

```
                    comment_targets (bảng chiếu Resource — CÓ SẴN, dùng chung với LuComment)
                          ▲ 0..1 anchor
                          │
 poll_policies ─ áp cho ─▶ Poll ──1..n──▶ PollOption
                          │  │                ▲
                          │  └─0..n─▶ PollInvitee
                          │                   │ option_id
                          ├─0..n─▶ Receipt (poll_voters)  ← mọi mức danh tính
                          ├─0..n─▶ Vote    (poll_votes)   ← public / owner_only / anonymous
                          ├─0..n─▶ Ballot  (poll_ballots) ← CHỈ secret
                          └─0..1─▶ PollResult (poll_results, chốt khi đóng)
```

### Poll

| Trường | Kiểu | Ghi chú |
|--------|------|---------|
| `id` | uuid | Ngẫu nhiên (`gen_random_uuid`) — **không đoán được**, là điều kiện của visibility `unlisted` |
| `anchor_target_id` | uuid? | → `comment_targets.id`. `NULL` ⇒ poll độc lập |
| `space_uuid` | uuid? | **Chỉ** poll độc lập trong Space. Poll gắn kết lấy Space từ Anchor |
| `visibility` | text? | **Chỉ** poll độc lập: `public` · `authenticated` · `unlisted` · `space` · `private` |
| `author_kind` | text | `profile` · `space` (đại diện Space) · `service` (tạo qua S2S) |
| `created_by` | uuid? | Người thật bấm tạo; `NULL` chỉ khi `author_kind='service'` không kèm người |
| `author_space_uuid`, `source_service` | | Như `comments` |
| `question` | text | Văn bản thuần, 3–300 ký tự sau trim |
| `description_md` / `_html` | text | Markdown `basic`, ≤ 2.000 ký tự, tuỳ chọn |
| `kind` | text | §3.2 |
| `config` | jsonb | Tham số riêng của từng kind (§3.2) |
| `identity_mode` | text | `public` · `owner_only` · `anonymous` · `secret` ([05 §5.3](05-bo-phieu-va-kiem-phieu.md)) |
| `results_visibility` | text | `always` · `after_vote` · `after_close` · `owner_only` |
| `who_can_vote` | text | `viewers` · `members` · `invited` |
| `allow_change_vote` | bool | `secret` ⇒ buộc `false` |
| `allow_retract` | bool | `secret` ⇒ buộc `false` |
| `allow_user_options` | bool | Chỉ `single`/`multiple`; không với `ranked` khi đã có phiếu (§3.5) |
| `shuffle_options` | bool | Xáo thứ tự **hiển thị** theo hạt giống `hash(poll_id, viewer)` — ổn định cho cùng một người |
| `min_voters_for_results` | smallint | Mặc định 3; chỉ có hiệu lực với `anonymous`/`secret` ([05 §5.6](05-bo-phieu-va-kiem-phieu.md)) |
| `quorum_percent` | smallint? | 1–100; chỉ khi `who_can_vote ∈ (members, invited)` |
| `status` | text | Trạng thái **lưu**: `draft` · `pending` · `published` · `closed` · `hidden` · `deleted` · `rejected` |
| `opens_at`, `closes_at` | timestamptz? | `closes_at - opens_at ≥ 5 phút`; `closes_at ≤ now + 365 ngày` |
| `closed_at`, `closed_by`, `close_reason` | | `close_reason ∈ (manual, deadline, anchor_gone, moderation, service)` |
| `reopen_count` | smallint | Trần 3 |
| `eligible_count` | int? | Chụp khi mở, cập nhật khi đóng (§3.6) |
| `voter_count` | int | Số Receipt |
| `ballot_version` | bigint | Tăng ở **mọi** thay đổi phiếu; nền của ETag và khoá cache |
| `first_vote_at` | timestamptz? | Mốc khoá cấu hình (§3.5) |
| `idempotency_key` | text? | `UNIQUE` |

### PollOption

| Trường | Ghi chú |
|--------|---------|
| `id`, `poll_id`, `position` | `position` liên tục từ 0, `UNIQUE (poll_id, position)` |
| `label` | Văn bản thuần 1–200 ký tự; `UNIQUE (poll_id, lower(label))` khi `status='active'` |
| `value` | smallint? — chỉ `scale` (giá trị điểm) |
| `starts_at`, `ends_at` | Chỉ `schedule` |
| `added_by` | Profile đã thêm (lựa chọn do người dùng thêm); `NULL` = của người tạo |
| `status` | `active` · `pending` (chờ duyệt) · `hidden` |
| `vote_count` | Số phiếu (`single`/`multiple`/`scale`); số "có" (`schedule`); số phiếu xếp **hạng 1** (`ranked`, chỉ để hiển thị nhanh) |
| `maybe_count` | Chỉ `schedule` |

### Receipt (`poll_voters`)

`(poll_id, profile_uuid)` là khoá chính. Mang `voted_at`, `updated_at`, `acting_space_uuid`
(bỏ phiếu thay mặt Space — v1 **không** mở, giữ cột để khỏi đổi schema), `notify_result`
(người bỏ phiếu muốn nhận thông báo khi có kết quả, mặc định `true`).

Receipt trả lời câu "**ai đã** bỏ phiếu", không trả lời "bỏ phiếu **cho gì**". Kể cả ở mức
`secret`, sự tham gia là thông tin **không bí mật** — giống danh sách cử tri đã ký nhận ở
bầu cử giấy. Điều này cho phép nhắc người chưa bỏ phiếu và tính túc số; nó được ghi rõ trên
giao diện ("Danh sách người đã tham gia có thể được người tổ chức xem; lựa chọn của bạn thì
không").

### Vote (`poll_votes`) và Ballot (`poll_ballots`)

| | Vote | Ballot |
|---|------|--------|
| Mức danh tính | `public`, `owner_only`, `anonymous` | `secret` |
| Khoá | `(poll_id, voter_profile_uuid, option_id)` | `id` ngẫu nhiên |
| Có Profile | ✓ | **✗ — không có cột** |
| Có thời điểm | ✓ | **✗ — không có cột** |
| Dữ liệu | `rank` (ranked), `answer` (`yes`/`maybe` cho schedule) | `choices jsonb` — mảng `option_id` (theo thứ tự hạng nếu `ranked`), hoặc `{"option":…, "answer":…}` cho schedule |
| Đổi/rút | Được nếu policy cho | **Không bao giờ** — không còn biết phiếu nào là của ai |

`anonymous` vẫn lưu Profile trong `poll_votes` để người bỏ phiếu **đổi được phiếu của mình**;
tính ẩn danh của nó là ở API và giao diện. Muốn "không ai, kể cả hệ thống, biết" thì dùng
`secret` và chấp nhận không đổi được phiếu. Bảng lựa chọn mức danh tính ở
[05 §5.3](05-bo-phieu-va-kiem-phieu.md) nói rõ đánh đổi này cho người tạo poll.

## 3.2 Năm loại bình chọn

| Kind | Lá phiếu | `config` | Kết quả |
|------|----------|----------|---------|
| `single` | Đúng 1 lựa chọn | `{}` | Số phiếu + % trên số người bỏ phiếu; người thắng = nhiều phiếu nhất (có thể hoà) |
| `multiple` | `min_choices`…`max_choices` lựa chọn | `{"min_choices":1,"max_choices":3}`; `1 ≤ min ≤ max ≤ số lựa chọn`, `max ≤ 20` | Số phiếu mỗi lựa chọn + % trên **số người** (tổng > 100% là đúng) |
| `ranked` | Xếp hạng 1..k, `k ≤ max_ranks`, không trùng hạng, không bỏ trống giữa | `{"method":"irv","max_ranks":5}`, `method ∈ (irv, borda)` | IRV: các vòng loại + người thắng đạt > 50% phiếu còn hiệu lực; Borda: điểm mỗi lựa chọn ([05 §5.5](05-bo-phieu-va-kiem-phieu.md)) |
| `scale` | Đúng 1 mức | `{"min":1,"max":5,"min_label":"Tệ","max_label":"Tuyệt"}`, `max - min ∈ [2, 10]` | Phân bố + trung bình + trung vị. Lựa chọn **sinh tự động** (`value` = min..max), người tạo không nhập |
| `schedule` | Mỗi khung giờ: `yes` · `maybe` · (bỏ trống = không) | `{"allow_maybe":true,"timezone":"Asia/Ho_Chi_Minh"}`; mỗi lựa chọn có `starts_at`/`ends_at`, ≤ 30 khung | Mỗi khung: số "có", số "có thể"; xếp hạng theo `yes*2 + maybe` (hiển thị cả hai số, không chỉ điểm) |

Số lựa chọn: 2–20 (`single`/`multiple`/`ranked`), 2–30 (`schedule`). Lựa chọn do người dùng
thêm tính vào trần, trần cứng 50.

## 3.3 Anchor — ba chế độ gắn

| Chế độ | `anchor_target_id` | `space_uuid` | `visibility` | Quyền đọc lấy từ |
|--------|--------------------|--------------|--------------|------------------|
| Độc lập cá nhân | `NULL` | `NULL` | `public` / `authenticated` / `unlisted` / `private` | Chính poll |
| Độc lập trong Space | `NULL` | có | `space` hoặc `public` (khi Space cho) | Chính poll + thành viên Space |
| Gắn vào Resource | có | `NULL` | `NULL` | `comment_targets` của Anchor ([04](04-gan-ket-va-phan-quyen.md)) |

`CHECK`:
`(anchor_target_id IS NULL) = (visibility IS NOT NULL)` và
`anchor_target_id IS NULL OR space_uuid IS NULL` và
`visibility <> 'space' OR space_uuid IS NOT NULL`.

**Anchor là ludiskus cũng đi qua `comment_targets`.** Gắn poll vào chủ đề `T` nghĩa là
`ensureCommentTarget(ludiskus:topic:T)` rồi trỏ tới hàng đó. Resolver gọi nội bộ
`InteractionContext` (đã có), nên chủ đề, bài trả lời, bình luận và **cả poll khác** đều có
bản chiếu theo cùng một cách — không có nhánh đặc biệt cho ludiskus trong `poll*.go`.

Chống vòng: Anchor **không** được là `ludiskus:poll:*` (poll gắn vào poll vô nghĩa và tạo
chuỗi quyền đệ quy). Poll trong bình luận mà bình luận đó nằm dưới `ludiskus:poll:*` cũng bị
chặn (`422 ANCHOR_NOT_ALLOWED`).

## 3.4 Máy trạng thái

Trạng thái **lưu** (`status`) chỉ đổi do hành động. Trạng thái **hiệu lực** tính lúc đọc:

```
effective(p, now) =
  deleted                      nếu p.status = 'deleted'
  hidden                       nếu p.status ∈ ('hidden','rejected')
                                 hoặc Anchor.state ∈ ('gone','blocked')
  pending                      nếu p.status = 'pending'
  draft                        nếu p.status = 'draft'
  closed                       nếu p.status = 'closed'
                                 hoặc (p.closes_at IS NOT NULL AND now >= p.closes_at)
  scheduled                    nếu p.opens_at IS NOT NULL AND now < p.opens_at
  open                         còn lại (p.status = 'published')
```

```
            publish (kiểm duyệt pre)      duyệt
  draft ───────────────────────▶ pending ──────▶ published ──(now ≥ opens_at)──▶ "open"
    │  publish (không kiểm duyệt)                 ▲   │                             │
    └─────────────────────────────────────────────┘   │ close tay / now ≥ closes_at │
                                         từ chối ▼    ▼                             ▼
                                        rejected     closed ◀───────────────────────┘
                                                      │ reopen (C/O/M/S, ≤ 3 lần, closes_at mới > now)
                                                      └──────────────▶ published
  bất kỳ (trừ deleted) ── ẩn (O/M/S, báo cáo) ──▶ hidden ── khôi phục ──▶ trạng thái trước
  bất kỳ ── xoá ──▶ deleted (giữ hàng; phiếu giữ để đối soát; mọi route trả 410)
```

`reopen` bị cấm khi `identity_mode='secret'` **và** `results_visibility='after_close'` đã lộ
kết quả: mở lại sau khi mọi người đã thấy kết quả là mời bỏ phiếu chiến thuật — cuộc bầu kín
phải làm lại bằng poll mới.

Worker "chốt hết hạn" ([12 §12.3](12-trien-khai.md)) tìm poll `published` có `closes_at ≤ now`,
`UPDATE status='closed', close_reason='deadline'`, chốt `poll_results` và phát `poll.closed`.
Nếu worker chậm, người dùng **vẫn** thấy poll đóng đúng giờ nhờ `effective()`.

**Anchor biến mất** (`comment_targets.state = 'gone'`): poll trở thành `hidden` hiệu lực
ngay; worker chuyển hẳn sang `closed` với `close_reason='anchor_gone'` để không ai bỏ phiếu
được nếu Anchor được khôi phục — khôi phục Anchor thì poll **vẫn đóng**, người tạo mở lại nếu
muốn.

## 3.5 Khoá cấu hình sau phiếu đầu tiên

`first_vote_at` được ghi ở phiếu đầu tiên (trong cùng transaction). Từ đó:

| Trường | Trước phiếu đầu | Sau phiếu đầu |
|--------|-----------------|---------------|
| `question`, `description` | Sửa tự do | Sửa trong **15 phút** từ `first_vote_at` (lỗi chính tả); mỗi lần sửa ghi audit và hiện "đã sửa" |
| Nhãn lựa chọn | Sửa/xoá/thêm/đổi thứ tự tự do | **Không sửa, không xoá** (đổi nghĩa phiếu đã bỏ). Người tạo được **thêm** với `single`/`multiple`/`schedule` |
| `kind`, `config` | Tự do | Khoá |
| `identity_mode` | Tự do | **Chỉ siết**: `public → owner_only → anonymous`; không vào/ra `secret` (QĐ-P8) |
| `results_visibility` | Tự do | Khoá; ngoại lệ: chuyển sang `always` **sau khi đóng** |
| `who_can_vote` | Tự do | Chỉ **nới** (`invited → members → viewers`), không siết — siết là tước quyền người đã bỏ phiếu |
| `allow_change_vote` | Tự do | Chỉ `true → false` |
| `closes_at` | Tự do | Chỉ **lùi về sau** (gia hạn) hoặc đóng ngay; không rút ngắn |
| `quorum_percent` | Tự do | Khoá |

Thêm lựa chọn vào `ranked` sau phiếu đầu bị cấm: các phiếu cũ không xếp hạng lựa chọn mới,
IRV coi như họ "không thích nhất" — kết quả lệch có hệ thống.

## 3.6 Cử tri hợp lệ và túc số

| `who_can_vote` | Cử tri hợp lệ | `eligible_count` |
|----------------|---------------|------------------|
| `viewers` | Ai đọc được poll | `NULL` — không xác định, không có túc số |
| `members` | Thành viên **đang hoạt động** của Space (Anchor hoặc poll) lúc **bỏ phiếu** | Chụp lúc mở từ `ident.Members`, cập nhật lại lúc đóng |
| `invited` | Profile trong `poll_invitees` (và vẫn đọc được poll) | `count(poll_invitees)` |

`members` mà poll không thuộc Space nào ⇒ `422 MEMBERS_REQUIRES_SPACE` khi tạo.

Tỉ lệ tham gia = `voter_count / eligible_count`, chặn trên 100% (người vào Space sau khi mở
vẫn được bỏ phiếu; người rời Space sau khi bỏ phiếu vẫn giữ phiếu). Túc số đạt khi tỉ lệ
**lúc đóng** ≥ `quorum_percent`. Không đạt ⇒ kết quả vẫn hiển thị nhưng mang nhãn
"**Không đủ túc số**" và `poll_results.result.quorum_met = false`; LuPoll không tự huỷ kết quả —
diễn giải là việc của tổ chức.
