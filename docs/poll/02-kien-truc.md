# 02 — Kiến trúc

## 2.1 Vị trí trong hippo

LuPoll **không** là service mới. Nó nằm trong `ludiskus-api` / `ludiskus-worker` và **đứng trên
vai LuComment**: dùng chung registry `comment_services`, resolver `internal/resolver`, bảng
chiếu nội dung `comment_targets`, cache danh tính, hàng chờ kiểm duyệt, outbox.

```
Người dùng ─cookie─▶ app tm ─/api/ludiskus/polls/*──────────▶ bff ─X-Gw-* (HMAC)─▶ ludiskus-api
   │   <PollList resource=…> / <PollEmbed id=…> ở MỌI trang                         │
   │ khách ─/api/public/ludiskus/polls/*─▶ bff passthrough (GET/HEAD) ─────────────▶│
   ▼                                                                                │
                                                         ┌──────────────────────────▼──────┐
 GET {base}/api/v1/s2s/resource-context/{type}/{id}      │          ludiskus-api           │
 ◀╌╌ resolver (CÓ SẴN của LuComment) ╌╌ lumuse · luxtory │  Forum (cũ) · LuComment (cũ)    │
     luprojet · lukode · lurp · …                        │  + LuPoll (mới)                 │
                                                         │    polls ──anchor──▶ comment_targets
 POST /api/v1/s2s/polls…  ╌╌ service tạo/đóng/ẩn poll ╌╌▶│                                 │
                                                         └──┬───────────────┬──────────────┘
                                         MỘT tx: Receipt +  │               │ cache kết quả,
                                         phiếu + UPDATE ±1  ▼               ▼ rate limit
                                                  ┌──────────────┐   ┌──────────────┐
                                                  │  postgres    │   │ redis db 6   │
                                                  │  + outbox    │   │  poll:*      │
                                                  └──────┬───────┘   └──────────────┘
                                                         ▼
                                     ┌─────────────────────────┐  POST /api/v1/events ┌────────┐
                                     │     ludiskus-worker      │─────────────────────▶│ lunoti │
                                     │ quét poll hết hạn ⇒ chốt │                      └────────┘
                                     │ nhắc bỏ phiếu · dọn nháp │  PUT ref ludiskus:poll:{id}
                                     │ đối soát số đếm          │◀───────────────────▶ lufami
                                     └─────────────────────────┘   (Interaction Platform)
```

## 2.2 Các process (không đổi)

| Process | Việc mới |
|---------|----------|
| `ludiskus-api` | Route `/api/v1/polls/*`, `/api/v1/public/polls/*`, `/api/v1/s2s/polls/*`, `/api/v1/admin/poll-*`; nhánh `case "poll"` trong `InteractionContext`; nhận `pollIds` khi tạo chủ đề/bình luận |
| `ludiskus-worker` | 4 ticker mới: **chốt poll hết hạn** (30s), **nhắc bỏ phiếu** (5m), **dọn nháp mồ côi** (1h), **đối soát** (đêm) |

## 2.3 Mười tám quyết định kiến trúc chốt

Đổi bất kỳ quyết định nào phải sửa tài liệu trước khi sửa mã.

| # | Quyết định | Vì sao | Hệ quả |
|---|-----------|--------|--------|
| **QĐ-P1** | Là **phân hệ trong `ludiskus`**, không service mới | Mọi mảnh ghép (resolver, registry, cache danh tính, kiểm duyệt, outbox→lunoti có Rule, BFF public) đã chạy thật ở LuComment | File `poll*.go` **không JOIN** `topics`/`posts`/`comments`; tách thành service riêng sau này chỉ là chuyển file |
| **QĐ-P2** | Poll là **Resource hạng nhất** với ref `ludiskus:poll:{id}` | Seed Interaction Platform đã khai sẵn `ludiskus × poll`; luxtory `service-embed` (kế hoạch GĐ6 của nó) nhúng theo ref; LuComment bình luận theo ref | Thêm `case "poll"` vào `service.InteractionContext`; poll độc lập được like, được bình luận, được nhúng mà không cần thêm hợp đồng |
| **QĐ-P3** | **Anchor tuỳ chọn, tối đa một**, lưu là khoá ngoại tới **`comment_targets`** — bảng chiếu nội dung có sẵn | Một nội dung phải có **một** bản chiếu metadata/visibility trong `ludiskus`. Hai bảng chiếu = hai lần đẩy invalidate, và chắc chắn một ngày sẽ lệch | `POST /s2s/comments/targets/invalidate` mà service đang gọi cho LuComment **tự động** có hiệu lực cho poll. Không thêm registry, không thêm endpoint đẩy metadata |
| **QĐ-P4** | **Anchor ≠ Embed.** Anchor quyết định quyền + vòng đời; Embed chỉ là nơi vẽ | Một poll gắn vào chủ đề có thể được nhúng thêm vào tài liệu luxtory. Nếu nơi nhúng cấp quyền thì tài liệu public sẽ làm lộ poll của Space kín | Mọi Embed gọi cùng `GET /polls/{id}` — người không có quyền thấy thẻ khoá, không thấy câu hỏi |
| **QĐ-P5** | Poll gắn vào nội dung **thừa hưởng** visibility của Anchor, **không** tự khai visibility | Cho poll tự mở rộng hơn Anchor là đường rò rỉ (poll public dưới tài liệu private lộ tiêu đề và câu hỏi) | `polls.visibility` chỉ có giá trị khi **độc lập**; `CHECK` ép điều đó |
| **QĐ-P6** | **Một câu hỏi mỗi poll**; không survey, không quiz | Ranh giới [README](README.md) | Không có bảng câu hỏi; mở rộng sang form là phân hệ khác |
| **QĐ-P7** | **Bốn mức danh tính**, trong đó `secret` lưu phiếu ở **bảng khác** không có cột Profile và thời điểm | "Ẩn danh" chỉ bằng cách che ở API thì chỉ cần một bug ở một route là lộ hết. Không có cột thì không lộ được | `poll_votes` (có Profile) và `poll_ballots` (không Profile); `secret` ⇒ **không đổi, không rút phiếu được**, được ép bằng `CHECK` ([05 §5.3](05-bo-phieu-va-kiem-phieu.md)) |
| **QĐ-P8** | **Mức danh tính chỉ được siết, không được nới** sau phiếu đầu tiên | Người bỏ phiếu đã quyết định dựa trên lời hứa ẩn danh. Đổi `anonymous` → `public` sau đó là phản bội họ, bất kể ai bấm | `public → owner_only → anonymous` được phép; mọi hướng ngược lại và mọi chuyển **vào/ra** `secret` bị từ chối `409 POLL_LOCKED` |
| **QĐ-P9** | **Receipt** (`poll_voters`) có ở mọi mức danh tính và là thứ duy nhất chặn bỏ phiếu hai lần | Với `secret` không thể dùng `UNIQUE(poll, profile)` trên bảng phiếu vì bảng phiếu không có Profile | `PRIMARY KEY (poll_id, profile_uuid)` trên `poll_voters`; ghi Receipt **cùng transaction** với phiếu |
| **QĐ-P10** | Mỗi lượt bỏ phiếu **mở đầu bằng một câu `UPDATE polls … WHERE <đang mở> RETURNING`** | Câu đó vừa khoá hàng poll vừa kiểm "còn mở" nguyên tử. Lượt đóng poll cũng `UPDATE` cùng hàng ⇒ hai việc tự xếp hàng, không có khe giữa "kiểm" và "ghi" | Thông lượng phiếu trên **một** poll bị tuần tự hoá ở hàng `polls`. Chấp nhận: ~2 ms/tx ⇒ vài trăm phiếu/giây/poll, dư cho hippo. Có cổng đo ở GĐ7 |
| **QĐ-P11** | Số đếm `poll_options.vote_count` cập nhật **cùng transaction**, câu `UPDATE ±1`, **theo thứ tự `option_id` tăng dần** | Cùng nguyên tắc QĐ-10 của LuComment. Thứ tự cố định vì phiếu `multiple`/`schedule` chạm nhiều hàng lựa chọn: hai lượt chạm ngược thứ tự là **deadlock** | Một hàm duy nhất `applyOptionDeltas(tx, deltas)` sắp xếp trước khi ghi; test đồng thời phải chạy với thứ tự lựa chọn ngẫu nhiên |
| **QĐ-P12** | Kết quả `ranked` **tính khi đọc** từ phiếu, cache theo `ballot_version`; chốt vào `poll_results` khi đóng | IRV không cộng dồn được; giữ số đếm phụ cho từng vòng là sai ngay khi có người đổi phiếu | Đọc poll `ranked` đang mở tốn một truy vấn phiếu khi cache trượt; trần 10.000 phiếu tính ≤ 150 ms ([05 §5.5](05-bo-phieu-va-kiem-phieu.md)) |
| **QĐ-P13** | **Trạng thái thời gian tính lười** từ `opens_at`/`closes_at` lúc đọc và lúc ghi; worker chỉ chốt và phát sự kiện | Nếu "đóng lúc 20:00" phụ thuộc worker thì worker chết = poll mở mãi | Poll đúng hạn kể cả khi worker dừng; `closed_at` / sự kiện `poll.closed` có thể trễ ≤ 30s |
| **QĐ-P14** | **Không realtime**: hỏi lại 15s + `ETag` khi tab hiển thị | Không thêm hạ tầng; `lugame-realtime` là cổng của lugame, không phải hạ tầng chung | ETag = `ballot_version` + **trạng thái người xem** ([05 §5.6](05-bo-phieu-va-kiem-phieu.md)) |
| **QĐ-P15** | **Lộ kết quả áp ở server**; số đếm không bao giờ rời server khi người xem chưa được thấy | Ẩn bằng CSS/JS là lộ qua DevTools | Cache công khai chỉ cho poll mà kết quả đã lộ **cho mọi người** (`always`, hoặc `after_close` đã đóng) |
| **QĐ-P16** | Policy riêng **`poll_policies`** theo `(anchor_service, anchor_type)`, hợp nhất 4 tầng như LuComment; **thiếu hàng ⇒ tắt** | Đồng nhất với hành vi `comment_policies` đang chạy; bật bình chọn trên một loại nội dung là quyết định có chủ ý | Seed bật sẵn cho `ludiskus × {standalone, topic, post, reply, comment}` và các cặp đợt 1 ([11 §11.2](11-tich-hop-service.md)) |
| **QĐ-P17** | Poll trong bình luận phải qua **hai lớp policy**: `poll_policies(ludiskus, comment)` **và** khoá `poll` trong `comment_policies` của Target chứa bình luận | Bật bình chọn trong bình luận dưới bộ phim không có nghĩa là bật trong bình luận dưới dự án nội bộ | Thêm khoá `poll: {enabled, max_per_comment}` vào config `comment_policies`, mặc định **tắt** |
| **QĐ-P18** | **Không cho khách bỏ phiếu**, **không** thưởng hipt | [01 §1.2](01-tong-quan.md) | Nhóm công khai chỉ đọc; không gọi `hipt.Complete` từ file `poll*.go` (có test) |

## 2.4 Ranh giới module trong mã nguồn

Kiểm bằng `internal/service/poll_arch_test.go`, theo đúng khuôn `comment_arch_test.go`:

1. File `poll*.go` trong `internal/service` **không** gọi `s.repo.GetTopic`, `GetPost`,
   `GetComment`, `ListBoards`. Kiểm "người gọi có được gắn poll vào chủ đề X không" phải đi qua
   **resolver** (đường nội bộ `SetLocal` gọi `InteractionContext`), giống mọi service khác.
   Ngoại lệ duy nhất: `service/interaction.go` (phục vụ `case "poll"`).
2. Truy vấn trong `repository/poll*.go` **không** JOIN `topics`/`posts`/`comments`. Được JOIN
   `comment_targets` (bảng chiếu dùng chung — QĐ-P3).
3. File mảng forum và `comment*.go` **không** import kiểu `domain.Poll*`. Hai điểm chạm duy
   nhất (tạo chủ đề có `pollIds`, tạo bình luận có `pollIds`) gọi qua **interface hẹp**
   `PollAttacher` khai trong `domain`, hiện thực ở `service/poll_attach.go`.
4. File `poll*.go` không gọi `s.hipt.Complete` (QĐ-P18).

## 2.5 Layout mã nguồn bổ sung

```
ludiskus/backend/
├── db/migrations/
│   ├── 0014_poll_core.up.sql            # poll_policies, polls, poll_options, poll_voters, poll_votes,
│   │                                    # poll_ballots, poll_invitees
│   ├── 0015_poll_report_enum.up.sql     # CHỈ ALTER TYPE report_target ADD VALUE 'poll','poll_option'
│   ├── 0016_poll_moderation.up.sql      # dùng 'poll': index báo cáo; comment_policies thêm khoá poll
│   └── 0017_poll_ops.up.sql             # poll_results, poll_audit_logs, view poll_count_check
├── db/seeds/
│   ├── poll_policies.json               # (mới)
│   ├── comment_policies.json            # (sửa) thêm ('ludiskus','poll') để bình luận dưới poll
│   └── lunoti_event_types.json          # (sửa) +5 event-type + template
├── internal/
│   ├── domain/poll.go                   # Poll, PollOption, Vote, Ballot, PollPolicy, PollCapabilities,
│   │                                    # EffectiveState, PollAttacher, lỗi poll
│   ├── repository/
│   │   ├── poll.go                      # polls + options + invitees
│   │   ├── poll_vote.go                 # receipt + votes + ballots + applyOptionDeltas
│   │   ├── poll_policy.go
│   │   └── poll_ops.go                  # results, audit, đối soát, quét hết hạn
│   ├── service/
│   │   ├── poll.go                      # tạo/sửa/đăng/đóng/mở lại/xoá + capabilities
│   │   ├── poll_access.go               # ensurePollReadable / Votable / Manageable
│   │   ├── poll_vote.go                 # bỏ/đổi/rút phiếu
│   │   ├── poll_tally.go                # kiểm phiếu: plurality, approval, IRV, Borda, scale, schedule (hàm thuần)
│   │   ├── poll_results.go              # resultsVisible, cache theo ballot_version, ảnh chụp ẩn danh
│   │   ├── poll_abuse.go                # rate limit poll:rl:*
│   │   ├── poll_policy.go               # hợp nhất 4 tầng (tái dùng hàm merge của comment_policy)
│   │   ├── poll_attach.go               # PollAttacher cho forum/comment + attach của service ngoài
│   │   ├── poll_moderation.go           # báo cáo, ẩn, hàng chờ lựa chọn
│   │   ├── poll_notify.go               # event lunoti
│   │   ├── poll_worker.go               # 4 ticker
│   │   ├── poll_arch_test.go
│   │   └── poll_*_test.go               # đặc biệt poll_tally_test.go (bảng ca IRV)
│   └── transport/http/
│       ├── poll.go                      # nhóm người dùng
│       ├── poll_public.go               # KHÔNG auth
│       ├── poll_s2s.go                  # service token + CommentServiceForClient
│       └── poll_admin.go
└── cmd/worker/main.go                   # (sửa) 4 ticker
```

Frontend (`tm/frontend/src`):

```
├── lib/poll.ts                          # client + type + hàng đợi summary
├── components/poll/                     # DÙNG CHUNG, không nằm trong components/ludiskus
│   ├── PollEmbed.tsx                    # vẽ một poll theo id — điểm nhúng chính
│   ├── PollList.tsx                     # mọi poll gắn vào một ResourceRef + nút "Thêm bình chọn"
│   ├── PollComposer.tsx                 # hộp thoại tạo/sửa — dùng components/ui/Dialog
│   ├── ballots/{Single,Multiple,Ranked,Scale,Schedule}Ballot.tsx
│   ├── PollResults.tsx  RankedRounds.tsx  PollVoters.tsx
│   ├── PollProvider.tsx                 # gom yêu cầu summary trong một frame
│   └── index.ts
└── pages/ludiskus/
    ├── PollPage.tsx                     # /ludiskus/p/:id
    └── Polls.tsx                        # /ludiskus/polls — của tôi / đã bỏ phiếu / trong Space
```

## 2.6 Luồng bỏ một phiếu

```
[Bấm "Bỏ phiếu"]  PUT /api/ludiskus/polls/{id}/vote   Idempotency-Key: …
      ▼
ludiskus-api
 1. nạp poll (+ Anchor từ comment_targets); ensureTarget làm tươi nếu cũ   → 404/410/503
 2. ensurePollReadable  (visibility Anchor hoặc của poll độc lập)          → 403/404
 3. ensurePollVotable   (who_can_vote, lời mời, trạng thái hiệu lực)        → 403/409
 4. validate lá phiếu theo kind + config                                   → 422 VOTE_INVALID
 5. rate limit (Redis, fail open)                                          → 429
 6. MỘT transaction:
      UPDATE polls SET ballot_version = ballot_version + 1, voter_count = voter_count + Δ
        WHERE id = $1 AND <đang mở theo statement_timestamp()> RETURNING …  → 0 hàng ⇒ 409 POLL_CLOSED
      INSERT/kiểm poll_voters (Receipt)                                    → trùng & không đổi được ⇒ 409
      tính delta so với phiếu cũ (nếu đổi phiếu)
      ghi poll_votes hoặc poll_ballots
      applyOptionDeltas (sắp theo option_id)
 7. xoá cache poll:res:{id}:*; trả viewer state + kết quả (nếu người xem được thấy)
```
