# 14 — Tiến độ thực tế so với kế hoạch

Ngày đối chiếu ban đầu: **2026-09-07**; cập nhật diễn đàn và LuComment: **2026-09-08**. Phạm vi: backend LuDiskus, migration/worker,
frontend và BFF trong `tm`; đối chiếu [lộ trình diễn đàn](13-lo-trinh.md),
[lộ trình LuComment](comment/15-lo-trinh.md) và [đầu việc LC](comment/16-cong-viec-chi-tiet.md).

Mốc mã nguồn: LuDiskus `44fbf05`, tm `b11ce2a`, **kèm working tree tại thời điểm đọc**.
Hai file `backend/internal/{repository,service}/comment_target.go` đã có thay đổi
chưa commit trước đợt rà soát; báo cáo có đọc trạng thái đó, không chỉnh sửa chúng.

## Kết luận và cách hiểu trạng thái

LuDiskus đã có nền diễn đàn, kiểm duyệt, tích hợp Interaction Platform, LuComment,
Personal Files và provider LuSpotlight. Các khoảng thiếu chính của diễn đàn đã được
bổ sung và có kiểm thử tích hợp; **chưa nghiệm thu production**. LuComment cũng đã được bổ sung theo yêu cầu tiếp nối. Bằng chứng và cổng còn mở ở
[nghiệm thu diễn đàn](15-nghiem-thu-dien-dan.md) và [nghiệm thu LuComment](comment/17-nghiem-thu.md).

- **Có code chính**: đã đọc đường triển khai tương ứng; không đồng nghĩa đã đạt mọi tiêu chí của giai đoạn.
- **Một phần**: đã có chức năng nhưng còn yêu cầu cụ thể chưa triển khai đầy đủ.
- **Chưa thấy triển khai**: không tìm thấy đường xử lý trong phạm vi code đã rà soát.
- **Chưa nghiệm thu hiện tại**: cần chạy API/SQL/browser/tải thật; test đơn vị không thay thế được.

Các checklist trong tài liệu kế hoạch là tiêu chí đích. Không đổi toàn bộ thành
`[x]` chỉ vì file hoặc endpoint đã tồn tại. Bảng dưới đây là bản đối chiếu hiện tại;
kết quả ngày 2026-08-21 trong README LuComment là bằng chứng lịch sử.

## Diễn đàn — GĐ0–GĐ5

Đường dẫn backend trong bảng tính từ `backend/`; đường dẫn tm tính từ `tm/frontend/src/`.

| Giai đoạn | Thực tế trong code | Chênh lệch / nghiệm thu còn lại |
|---|---|---|
| **GĐ0 — Đã bổ sung, runtime cơ sở đạt** | API có alias có auth `/api/v1/healthz`, `/api/v1/readyz` cho BFF; migration `0011`. API/worker đã restart; DB/Redis/MinIO ready; worker sync HipCore và đăng ký template Lunoti thành công. | Chưa có phiên browser thật để xác minh đăng nhập → BFF. |
| **GĐ1 — Đã bổ sung UI và quyền** | Cây trả lời, chọn bài cha, phân trang 30 bài và tự tải trang tới `#post-{id}`; Board CRUD trong Settings; capability guards. Topic khoá vẫn đọc được; bài pending/hidden chỉ tác giả/moderator đọc. | Chrome fixture đạt responsive 375 px, cây/deep-link; còn phiên HipCore/BFF thật. |
| **GĐ2 — Đã kiểm outbox, còn gate tích hợp** | Link thông báo dùng Space UUID/Topic UUID + post anchor; outbox retry sau HTTP 503 và hai worker claim đồng thời đạt, 16 event tới Lunoti fixture đúng một lần trong bài test. Reaction/vote vẫn thuộc Lufami. | Luồng S2S Interaction thực tế đang trả 401; chưa xác nhận chuông/email hay reaction end-to-end. |
| **GĐ3 — Đã bổ sung code và test tích hợp** | `search.Engine`, tìm tiêu đề/bài đầu/reply, bỏ dấu, fuzzy tiêu đề, snippet và post anchor; bộ lọc Space/Board/tác giả/type/tag/status/ngày, debounce/phân trang. Stat object MinIO trước khi gắn tệp, transaction kiểm owner/Space/pending; quyền tải theo bài. | PostgreSQL/MinIO thật với dữ liệu cô lập đạt; chưa chạy Personal Files import hay tải nặng trên dữ liệu đại diện. |
| **GĐ4 — Đã bổ sung UI và nghiệm thu tự động** | Queue riêng cho forum kèm nội dung sanitize; guards owner/admin/moderator. Test đủ none/post/pre/first_post, approve/reject, cấm duyệt lặp, ngưỡng report, quyền đọc bài ẩn. | Thông báo tác giả đã kiểm bằng fixture, chưa nhận thực trên chuông/email. |
| **GĐ5 — Đã bổ sung phần sản phẩm chính** | Q&A unanswered theo `answer_post_id`; chọn đáp án/reopen/moderation serialize theo Topic, test 20 lựa chọn đồng thời. Support có assignee/resolve/reopen. `ForumEditor` có toolbar, preview server, mention, Ctrl/Cmd Enter, kéo-thả tệp. Metrics Prometheus aggregate + histogram HTTP và log request ID. | Chưa có distributed tracing, scrape/dashboard vận hành và p95 xuyên BFF với tải đại diện; không dùng số đo fixture để đóng gate production. |

## LuComment — GĐ0–GĐ7

Các khoảng thiếu chính được bổ sung ngày 2026-09-08. Không coi đây là nghiệm thu
production của cả tám giai đoạn; xem [biên bản chi tiết](comment/17-nghiem-thu.md).

| Giai đoạn / mã việc | Bằng chứng hiện có | Phần còn cần xác minh |
|---|---|---|
| **GĐ0 — LC-0: đã bổ sung** | Đọc version Redis khi lấy policy, bypass cache khi Redis lỗi; batch resolver thật theo provider, worker dùng batch, không fan-out khi 503. Test 100 ref → một POST. | S2S OAuth/provider thật, batch 100 ref xuyên BFF và timeout phân tán. |
| **GĐ1 — LC-1: có code + test tích hợp** | Advisory lock bảo vệ retry idempotency; service trả kết quả đã ghi trước duplicate guard. Test 20 request repository cùng key → một comment/count/participant. Profile mới siết phút/giờ/target theo cấu hình. | HTTP đồng thời, mọi tổ hợp cây/cursor/số truy vấn và profile cache thực. |
| **GĐ2 — LC-2: đã bổ sung queue** | Queue giữ attachmentIds/parent/key, gom replay cùng tab, không làm mất mutation mới thêm hoặc lỗi quyền. UI quản lý chờ gửi, F5 giữ draft/tệp. Chrome: 20 Count → một summary, offline/reconnect gửi một lần. | Nhiều tab/tài khoản, phiên BFF thật; tệp chưa upload/hết orphan TTL không được bảo đảm bằng lưu ID. |
| **GĐ3 — LC-3: đã bổ sung UI** | Thread search debounce, drawer thật có role/focus/Escape, guards response cũ. Chrome 375 px và 6 unit test queue/Thread đạt. | Cây sâu, ghim khi đồng thời thay đổi dữ liệu, edit/revisions và cuộn ở bốn điểm nhúng thật. |
| **GĐ4 — LC-4: đã sửa tính bền vững** | Mention lưu buffer trong transaction tạo/duyệt. Buffer → outbox + delete atomically, kiểm lease token; giữ buffer khi lỗi tạm, không sweep bỏ vì quá một ngày. Test rollback/lease/mention đạt. | Chuông/email, grouping/read/unread/mute và người nhận thực. |
| **GĐ5 — LC-5: đã bổ sung quyền/transaction** | Public recheck quyền/policy trước cache; test public GET/POST405/auth401/private/hidden. Queue + comment/count + thông báo tác giả duyệt trong transaction; owner không Space được duyệt. Failure injection không làm queue quyết định trước khi publish. | Ma trận bốn chế độ và quyền đầy đủ qua BFF, thay đổi resource từ provider thật. |
| **GĐ6 — LC-6: có code chính** | Bốn điểm nhúng Lumuse, Lukolek, Lukode, LuProjet và admin/S2S hiện có; component dùng chung mới được các điểm nhúng kế thừa. | Chưa có phiên Browser đăng nhập để nghiệm thu bốn luồng runtime; không đánh dấu đã đạt chỉ từ source. |
| **GĐ7 — LC-7: thêm bằng chứng tự động** | 9 nhóm PostgreSQL/Redis fixture đạt; lỗi outbox/provider/Redis và hai lease, 10.000 root/20 trang không trùng; p95 handler + DB ~25,25 ms. Go test/vet, build, 10 Chrome fixture checks đạt; 8 nhóm forum hồi quy đạt. | p95 tải production xuyên BFF, nhiều process và lỗi phụ thuộc thật; không dùng số fixture thay cho các gate này. |

**Điều chỉnh contract so với kế hoạch cũ:** token client-credentials HipCore có thể có
`sub=aud`; code không chặn mọi token có `sub`. `ServiceMiddleware` chặn subject khác
audience, sau đó các handler áp quyền/registry tương ứng. Khi kiểm LC-0.7 cần dùng
contract này, không áp nguyên văn yêu cầu “token có sub luôn bị từ chối”.

## Phần đã bổ sung ngoài lộ trình diễn đàn ban đầu

| Hạng mục | Bằng chứng code | Giới hạn kết luận |
|---|---|---|
| **Personal Files** | Migration `0009_personal_file_sync`, `0010_personal_file_imports`; `repository/personal_file*.go`, `personalfiles/client.go`; worker sync/backfill; `service/attachment.go` redeem/copy/import/idempotency; NewTopic/Topic dùng `PersonalFilePicker`. | Có đường tích hợp Lufami và tombstone/outbox; chưa chạy lại upload/import/download, ownership và retry E2E. |
| **LuSpotlight** | `internal/transport/http/s2s_search.go`, đăng ký `POST /api/v1/s2s/search` dưới **UserMiddleware**, dùng Search lọc phạm vi người dùng. | Provider kế thừa tìm tiêu đề/thân bài mới; chưa kiểm runtime LuSpotlight hiện tại. |
| **Hipt** | `internal/hipt/hipt.go`, đăng ký task trong worker và lời gọi trong service nội dung. | Có tích hợp source; chưa kiểm phát/thực nhận điểm. |

Realtime/WebSocket, webhook vô hiệu cache từ HipCore, search engine ngoài,
reputation/badge/digest ngày, antivirus và quota Space vẫn là phạm vi cân nhắc sau;
không cộng chúng vào tiêu chí hoàn tất MVP hiện hành.

## Công việc tiếp theo trong phạm vi được giao

1. Xác minh token cache worker Lufami đang trả S2S 401 (token LuDiskus mới gọi 200).
   Restart worker Lufami bị automatic approval review chặn do ảnh hưởng phạm vi dùng chung;
   cần cho phép trước thao tác này, sau đó nghiệm thu reaction/vote.
2. Nghiệm thu phiên đăng nhập thật qua BFF: tạo Board/Topic, reply, preview/mention,
   gắn tệp, kiểm duyệt, Q&A/Support và thông báo deep-link ở desktop/mobile.
3. Xác nhận chuông/email Lunoti thực nhận và Personal Files import với tài khoản kiểm thử.
4. Đo tải đại diện xuyên BFF, kiểm lỗi phụ thuộc và thiết lập scrape/alert phù hợp.

Yêu cầu tiếp nối đã mở lại phạm vi LuComment; các sửa đổi và cổng còn mở được ghi
ở bảng trên và [biên bản LuComment](comment/17-nghiem-thu.md).

## Kiểm chứng hiện tại

Ngày **2026-09-08** (Asia/Ho_Chi_Minh; log container dùng UTC ngày 2026-09-07):
backend `go test ./...`/`go vet ./...` đạt; suite opt-in có 8 nhóm trên PostgreSQL và
MinIO thật đạt; tm typecheck/build, 6 unit test và 11 kiểm tra Chrome fixture đạt.
Migration 0011 up → down → up có dữ liệu đạt; schema thật đã áp 0011; health/readiness
và metrics hoạt động sau restart. Chi tiết lệnh, giới hạn và lỗi tích hợp quan sát
được ở [biên bản nghiệm thu](15-nghiem-thu-dien-dan.md).

Không dùng phần trăm hoàn thành; không đồng nhất fixture, runtime cơ sở và nghiệm thu
người dùng thật. Kết quả LuComment 2026-08-21 vẫn chỉ là bằng chứng lịch sử.

Bổ sung LuComment cùng ngày: 9 nhóm integration PostgreSQL/Redis, 6 unit test và
10 Chrome fixture checks đạt; API/worker đã nạp code và ready. Chưa có Browser
kết nối để xác nhận phiên đăng nhập, thông báo người thật và bốn service E2E.

## Thiết kế mới — Phân quyền Board (2026-09-08)

Đã bổ sung [thiết kế quyền tạo Topic/trả lời và moderator từng Board](16-phan-quyen-board.md).
**Chưa triển khai hoặc nghiệm thu**: `min_role` hiện được lưu nhưng chưa được áp khi
đăng bài; quyền kiểm duyệt vẫn ở cấp Space. Không cộng hạng mục này vào các kết quả
kiểm thử diễn đàn/LuComment đã ghi phía trên.
