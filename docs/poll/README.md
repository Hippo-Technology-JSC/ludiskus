# Kế hoạch — LuPoll (phân hệ bình chọn dùng chung cho hệ sinh thái hippo)

> **LuPoll** là phân hệ **bình chọn (poll)** thuộc dịch vụ **ludiskus**. Một cuộc bình chọn
> dùng được theo ba cách: **độc lập** (trang riêng, chia sẻ bằng liên kết, hoặc trong một
> Space), **gắn trong ludiskus** (chủ đề diễn đàn, bài trả lời, bình luận LuComment), và
> **gắn vào bất kỳ nội dung nào** của service khác (một bộ phim `lumuse`, một tài liệu
> `luxtory`, một dự án `luprojet`, một cuộc họp `lurp`…). Nội dung được gắn định danh bằng
> đúng chuẩn `service:type:id` của LuComment và Interaction Platform, và quyền xem lấy từ
> **cùng một resolver S2S** mà LuComment đã chạy thật.

## Thông tin phiên bản

| Mục | Giá trị |
|-----|---------|
| Tên phân hệ | LuPoll (Poll Platform) |
| Thuộc dịch vụ | `ludiskus` backend (`ludiskus-api` + `ludiskus-worker`) + app **tm** (frontend) |
| Phiên bản tài liệu | 1.0 |
| Ngày cập nhật | 2026-10-08 |
| Trạng thái | **Đã có triển khai backend, frontend và tích hợp; xem bằng chứng và giới hạn nghiệm thu tại [15](15-hien-trang-thuc-hien.md)** |
| Tài liệu gốc của ludiskus | [../README.md](../README.md) |
| Tài liệu nền phải đọc kèm | [LuComment — hợp đồng resource](../comment/04-hop-dong-resource.md), [LuComment — phân quyền](../comment/06-phan-quyen.md), [Interaction Platform (lufami)](../../../lufami/docs/interaction.md) |

## Vì sao cần phân hệ này

Khảo sát toàn bộ hippo ngày 2026-10-08 cho thấy **không service nào có bình chọn thật**:

| Chỗ có nhắc tới | Thực tế |
|-----------------|---------|
| `ludiskus` — "vote up/down" cho Q&A ([../13-lo-trinh.md](../13-lo-trinh.md)), "Bình chọn nâng cao — chỉ chuẩn bị chỗ" ([../01-tong-quan.md](../01-tong-quan.md)) | Chỉ có trong tài liệu. Up/down đã thành like/dislike của Interaction Platform (`0002_interaction_cutover`) — **không phải** bình chọn |
| `lufami` — seed policy Interaction **đã khai** `ludiskus` × `poll` ([interaction.md §14.2](../../../lufami/docs/interaction.md)) | Code ludiskus **chưa phục vụ** type `poll` (`service/interaction.go` trả `ErrNotFound`). Chỗ trống đã được giữ sẵn — LuPoll lấp đúng chỗ đó |
| `lufami` — meal-planner "bình chọn món", lucraft "bình chọn thành quả", advent-calendar ô `quiz/poll` | Chỉ là ý tưởng trong tài liệu; advent-calendar định uỷ cho `lugame` |
| `lutat` — `external_survey` | Nhúng khảo sát **bên ngoài** (Qualtrics/SurveyMonkey), không phải bình chọn nội bộ |
| `lurp` — CSAT/NPS/CES | Ghi "Chưa làm" ([lurp/docs/crm.md](../../../lurp/docs/crm.md)) |
| `lukode` presentation | Có long-poll để báo hiện diện, **không** có bình chọn khán giả |

Nghĩa là mỗi nhu cầu "hỏi ý kiến nhanh" — chọn ngày họp, chọn món, biểu quyết trong tổ chức,
hỏi khán giả giữa buổi trình chiếu, hỏi cộng đồng trong diễn đàn — đang **không có chỗ nào**
làm, hoặc sẽ bị mỗi service tự dựng một lần. Dựng **đúng một lần** trong `ludiskus` vì dịch vụ
này đã có sẵn mọi mảnh ghép: resolver S2S + registry + bảng chiếu nội dung (`comment_targets`),
cache Profile/Space/thành viên, kiểm duyệt + báo cáo + hàng chờ, outbox → lunoti có tự đăng ký
Rule, đọc công khai qua BFF, và đã là provider của Interaction Platform.

## Ngăn xếp công nghệ

| Thành phần | Công nghệ |
|------------|-----------|
| Backend | **Go 1.24**, thêm file `poll*.go` vào các package phẳng sẵn có (`internal/domain`, `repository`, `service`, `transport/http`). Không package mới |
| Lưu trữ | **PostgreSQL 17**, database `ludiskus`; migration nhúng, forward-only `0014_poll_core` → `0017_poll_ops` ([08](08-database.md)) |
| Phân giải nội dung được gắn | **Tái dùng** `internal/resolver` + registry `comment_services` + bảng chiếu `comment_targets` của LuComment — không registry thứ hai |
| Cache / rate-limit | **Redis** db `/6` (đã có), tiền tố khoá `poll:` |
| Văn bản | Câu hỏi và nhãn lựa chọn là **văn bản thuần**; mô tả dùng markdown `basic` (đã có trong `internal/markdown`) |
| Like / bookmark / share của poll | **Không tự làm** — Interaction Platform với ref `ludiskus:poll:{id}` (seed `lufami` đã có sẵn) |
| Bình luận dưới poll | **LuComment** với ref `ludiskus:poll:{id}` |
| Thông báo | **lunoti** qua bảng `outbox` sẵn có; worker tự đăng ký EventType + Template + **Rule** |
| Frontend | App **tm** (SolidJS): `lib/poll.ts` + `components/poll/*` dùng chung; trang `/ludiskus/p/:id` |
| Thư viện | **Không thêm dependency** ở Go lẫn TypeScript |
| Đóng gói | Không thêm container, volume, database, bucket |

## Mục lục tài liệu

| # | Tài liệu | Nội dung |
|---|----------|----------|
| 00 | [README.md](README.md) | Trang này + từ điển thuật ngữ + ranh giới khái niệm |
| 01 | [01-tong-quan.md](01-tong-quan.md) | Mục tiêu, phạm vi trong/ngoài, vai trò, ca sử dụng, yêu cầu phi chức năng |
| 02 | [02-kien-truc.md](02-kien-truc.md) | Vị trí trong hippo, sơ đồ, **18 quyết định kiến trúc chốt**, ranh giới module, layout mã |
| 03 | [03-mo-hinh-mien.md](03-mo-hinh-mien.md) | Poll, Option, Vote, Ballot, Receipt, Anchor; năm loại bình chọn; máy trạng thái; khoá cấu hình |
| 04 | [04-gan-ket-va-phan-quyen.md](04-gan-ket-va-phan-quyen.md) | Ba chế độ gắn, hợp đồng với nội dung được gắn, `ensurePollReadable/Votable`, ma trận hành động, policy 4 tầng |
| 05 | [05-bo-phieu-va-kiem-phieu.md](05-bo-phieu-va-kiem-phieu.md) | Luật bỏ phiếu, đổi/rút phiếu, bốn mức danh tính, kiểm phiếu từng loại (IRV, Borda), hiển thị kết quả, đồng thời |
| 06 | [06-kiem-duyet-chong-lam-dung.md](06-kiem-duyet-chong-lam-dung.md) | Kiểm duyệt câu hỏi/lựa chọn, báo cáo, rate limit, tài khoản mới, thao túng phiếu |
| 07 | [07-thong-bao-va-tuong-tac.md](07-thong-bao-va-tuong-tac.md) | Event lunoti, nhắc bỏ phiếu, Interaction Platform, bình luận dưới poll |
| 08 | [08-database.md](08-database.md) | Schema đầy đủ, migration `0014`→`0017` (kể cả `.down.sql`), index, đối soát |
| 09 | [09-backend-api.md](09-backend-api.md) | Đặc tả REST: người dùng, công khai, S2S, quản trị; mã lỗi |
| 10 | [10-frontend.md](10-frontend.md) | `lib/poll.ts`, component dùng chung, UX từng loại phiếu, truy cập được |
| 11 | [11-tich-hop-service.md](11-tich-hop-service.md) | Checklist tích hợp; diễn đàn, bình luận, `lumuse`, `luxtory`, `lurp`, `lukode` |
| 12 | [12-trien-khai.md](12-trien-khai.md) | Biến môi trường, BFF, worker, quan sát, vận hành |
| 13 | [13-lo-trinh.md](13-lo-trinh.md) | GĐ0–GĐ7, tiêu chí nghiệm thu **chặn**, rủi ro |
| 14 | [14-cong-viec-chi-tiet.md](14-cong-viec-chi-tiet.md) | Đầu việc có mã `LP-x.y`, file, cách kiểm chứng, ước lượng |
| 15 | [15-hien-trang-thuc-hien.md](15-hien-trang-thuc-hien.md) | Mã đã triển khai, kiểm chứng, vận hành dev và phần nghiệm thu còn lại |

## Cách đọc

- Quản lý sản phẩm: 01, 03 (§3.2 năm loại), 13.
- Kỹ sư backend `ludiskus`: 02 → 09 theo thứ tự.
- Kỹ sư frontend `tm`: 03, 05 §5.6, 09, 10.
- Kỹ sư của service muốn gắn bình chọn vào nội dung của mình: **11**, rồi 04.
- DevOps: 02, 12.
- **Người nhận việc để code: 14** (đọc §14.0 trước).

## Từ điển thuật ngữ

| Thuật ngữ | Tiếng Việt | Định nghĩa |
|-----------|-----------|------------|
| **Poll** | Cuộc bình chọn | **Một** câu hỏi + tập lựa chọn + luật bỏ phiếu. Bảng `polls`. Ref của chính nó: `ludiskus:poll:{id}` |
| **Option** | Lựa chọn | Một phương án trả lời. Bảng `poll_options`. Với loại `scale` là các mức điểm sinh tự động |
| **Kind** | Loại bình chọn | `single`, `multiple`, `ranked`, `scale`, `schedule` ([03 §3.2](03-mo-hinh-mien.md)) |
| **Vote** | Lá phiếu (có danh tính) | Lựa chọn của một Profile, lưu kèm `voter_profile_uuid`. Bảng `poll_votes`. Có ở mọi mức danh tính **trừ** `secret` |
| **Ballot** | Phiếu kín | Lá phiếu **không** mang danh tính, không mang thời điểm. Bảng `poll_ballots`. Chỉ ở mức `secret` |
| **Receipt** | Biên nhận đã bỏ phiếu | Dấu "Profile X đã bỏ phiếu trong poll Y". Bảng `poll_voters`. Có ở **mọi** mức — là cái chặn bỏ phiếu hai lần |
| **Identity mode** | Mức danh tính | `public` · `owner_only` · `anonymous` · `secret` ([05 §5.3](05-bo-phieu-va-kiem-phieu.md)) |
| **Anchor** | Nội dung được gắn | Resource mà poll thuộc về, quyết định **quyền xem và vòng đời** của poll. Lưu là `polls.anchor_target_id` → `comment_targets`. Poll độc lập không có Anchor |
| **Embed** | Điểm hiển thị | Nơi một poll được **vẽ ra** (khối tài liệu luxtory, đầu bài diễn đàn…). Một poll có một Anchor nhưng có thể có nhiều Embed. Embed **không** cấp thêm quyền |
| **Standalone** | Poll độc lập | Poll không có Anchor; tự mang `visibility` và `space_uuid` |
| **Eligible voters** | Cử tri hợp lệ | Tập Profile được bỏ phiếu theo `who_can_vote`. Với `members` được chụp số lượng (`eligible_count`) để tính tỉ lệ tham gia và túc số |
| **Results visibility** | Lúc lộ kết quả | `always` · `after_vote` · `after_close` · `owner_only` — áp ở server |
| **Effective state** | Trạng thái hiệu lực | Trạng thái tính lúc đọc từ `status` + `opens_at` + `closes_at` + `now()`: `draft`, `scheduled`, `open`, `closed`, `hidden`, `deleted` ([03 §3.4](03-mo-hinh-mien.md)) |
| **PollPolicy** | Chính sách bình chọn | Cấu hình theo `(anchor_service, anchor_type)`: ai được tạo, loại nào được dùng, giới hạn… Bảng `poll_policies`, hợp nhất 4 tầng như LuComment |

## Ranh giới bắt buộc

### Poll ≠ Like/Dislike/Reaction

Like/dislike/reaction là **phản ứng một chạm** với nội dung, thuộc Interaction Platform của
`lufami`. Poll là **câu hỏi có lựa chọn do người tạo định nghĩa**, có hạn chót, có luật kiểm
phiếu. "Upvote câu trả lời hay nhất" trong Q&A là like, **không** phải poll. LuPoll không đếm
like và Interaction Platform không đếm phiếu.

### Poll ≠ Survey / Form

Poll có **đúng một câu hỏi**. Khảo sát nhiều câu, câu hỏi tự luận, rẽ nhánh theo câu trả lời,
CSAT/NPS sau giao dịch là việc của một **form engine** — chưa có trong hippo và **không** được
lén mở rộng LuPoll thành nó (không có `poll_questions`, không có `poll_groups`). Ngoại lệ có
chủ ý: loại `scale` (một câu hỏi, thang điểm) nằm trong phạm vi vì nó vẫn là một câu.

### Poll ≠ Quiz

Quiz có **đáp án đúng** và **điểm**. Poll không có đáp án đúng. Quiz thuộc `lugame`
(advent-calendar đã định uỷ ô `quiz` cho `lugame`). LuPoll không có cột `is_correct`.

### Poll ≠ Bầu cử có giá trị pháp lý

Hippo **không có ký số/PKI** và DBA đọc được mọi bảng. Mức `secret` giữ bí mật **với người
dùng và API**, không chống được người quản trị cơ sở dữ liệu có ý đồ ([05 §5.3](05-bo-phieu-va-kiem-phieu.md)).
Giao diện của poll `secret` **phải** ghi rõ điều này; kết quả xuất ra mang nhãn "không có giá
trị pháp lý". Biểu quyết của HĐQT/đại hội cổ đông **không** dùng LuPoll làm bằng chứng.
