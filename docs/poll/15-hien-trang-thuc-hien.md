# LuPoll — hiện trạng thực hiện

Cập nhật: **2026-10-08**. Tài liệu này ghi nhận mã đã triển khai và bằng chứng kiểm chứng; các checklist trong 13/14 vẫn là tiêu chí nghiệm thu, không tự động được coi là đã đạt chỉ vì đã có mã.

## Phạm vi đã triển khai

| Phần | Hiện trạng |
|---|---|
| Lưu trữ | Migration `0014`–`0017`, policy, option/vote/receipt/ballot, kết quả chốt, audit, index và view đối soát. Enum báo cáo tách migration riêng. |
| API | CRUD, đăng nháp, gắn anchor, bỏ/đổi/rút phiếu, thêm lựa chọn, lời mời, kết quả, người tham gia, CSV, báo cáo/kiểm duyệt và quản trị. Nhóm người dùng, S2S, công khai và ETag theo biểu diễn được phép xem. |
| Phân quyền | Poll độc lập/Space và poll có anchor; resolver/registry/`comment_targets` dùng chung với LuComment. Policy bốn tầng, tầng cuối chỉ siết quyền. Không có registry hoặc service mới. |
| Phiếu/kết quả | `single`, `multiple`, `ranked` (IRV/Borda), `scale`, `schedule`; bốn mức danh tính; idempotency, khóa hàng khi ghi, cập nhật bộ đếm trong transaction. Poll ẩn danh dùng snapshot; phiếu kín không lưu danh tính/thời điểm trong `poll_ballots`. |
| Vòng đời | Nháp, lịch mở, đóng, mở lại, gia hạn, ẩn/khôi phục/xóa mềm; cấu hình khóa sau phiếu đầu. Kết quả chốt giữ nguyên sau khi dữ liệu phiếu cá nhân bị xóa. |
| Gắn nội dung | `PollAttacher`: topic/post/reply/comment nhận `pollIds`, gắn trong transaction tạo nội dung; lỗi gắn rollback cả nội dung. Kiểm duyệt anchor chuyển trạng thái poll theo. |
| Worker | Đóng poll mỗi 30 giây, nhắc mỗi 5 phút, dọn nháp mỗi giờ, đối soát lúc 03:30 Việt Nam; khóa/claim chống xử lý trùng, outbox lunoti, cờ bất thường và metrics không chứa danh tính. |
| Frontend | Client `lib/poll.ts`, `PollProvider`, `PollEmbed`, `PollComposer`, năm ballot, kết quả/vòng IRV, danh sách người tham gia, `PollList`, Space và tab quản trị LuPoll trong comment-admin. |
| Tích hợp ngoài | `PollList` tại chi tiết phim Lumuse, dự án LuProjet, tài liệu Luxtory, SharedView presentation LuKode, sự kiện lịch Lufami. Khối `poll` Luxtory chọn/tạo poll và xuất liên kết. |

Frontend tái sử dụng Dialog/footer, DatePicker/TimePicker, RadioGroup, Repeater, MarkdownInput và ProfilePicker. Ranked có thao tác lên/xuống bằng bàn phím và thông báo `aria-live`; Schedule hỗ trợ Có/Có thể/Không. Client giữ khóa bỏ phiếu khi thử lại, chỉ hỏi lại poll đang mở khi tab và embed đang hiển thị, xóa dữ liệu hiển thị nếu mất quyền.

Seed poll/comment policy giữ nguyên các hàng quản trị đã sửa. Danh mục lunoti giữ các event cũ và bổ sung năm event LuPoll. Module có thể tắt bằng `LUDISKUS_POLL_ENABLED=false`. Không thêm dependency Go/TypeScript, container, database, bucket hoặc volume.

## Kiểm chứng đã chạy

| Kiểm chứng | Kết quả |
|---|---|
| `ludiskus/backend`: `go test ./...` | Đạt. Có unit test miền, cấu hình khóa/quyền kết quả, Hamilton, ranked, scale/schedule, kiến trúc và HTTP. |
| PostgreSQL: `TestPollAcceptance` | **12 nhóm đạt** trên database `ludiskus_test` hiện có, schema tạm tự dọn. Gồm up→down→up, phiếu kín, snapshot ẩn danh, ETag, replay, phân quyền, CSV, seed, gắn nháp và kết quả chốt. |
| Đồng thời PostgreSQL | 200 goroutine × 5 lần ghi/đổi/rút; không deadlock, không lệch bộ đếm. 1.000 cuộc đua đóng/bỏ phiếu và kiểm tra chốt/outbox chống trùng đạt. Đây là kiểm tra tính đúng, không phải chứng nhận SLO tải 30 phút. |
| `luxtory/backend`: `go test ./...` | Đạt toàn module. |
| tm frontend | Typecheck và production build đạt. Bộ test liên quan poll/comment/blocks **319 test, 12 file đạt**; parity catalog Go↔TS nằm trong bộ này. |
| tm BFF | Typecheck và **107 test đạt**, gồm GET/HEAD công khai, chặn phương thức ghi, bỏ credential và chuyển conditional ETag. |
| Chrome với fixture | Năm ballot đã thao tác và gửi phiếu qua component thật trong harness rộng 375px; ranked đổi thứ tự, scale và schedule hoạt động. Harness không thay thế API/Chrome nhiều tài khoản. |
| Chrome trên stack dev | `/ludiskus/polls` tải dữ liệu thật; hộp thoại tạo hiển thị năm loại và các thành phần dùng chung. Chưa tạo hoặc gửi lời mời cho người dùng thật trong lần kiểm chứng này. |
| Runtime dev | API/worker ludiskus, API Luxtory và BFF đã nạp mã mới; worker áp dụng migration `0014`–`0017`. `/readyz` ludiskus trả 200 với DB/Redis/storage `ok`; BFF chuyển tiếp API poll và trang danh sách trả 200. |

Bộ test spotlight routes rộng hơn có lỗi sẵn ở catalog `/luw/admin/parts`; lỗi này nằm ngoài LuPoll. Entry `/ludiskus/polls` đã được thêm, nhưng không ghi nhận toàn bộ suite spotlight là đạt.

## Chạy lại và vận hành

Chạy lệnh trong đúng checkout/module:

```sh
# ludiskus/backend
go test ./...
# LUDISKUS_TEST_DSN phải trỏ tới database tên kết thúc bằng _test.
# Acceptance tạo schema riêng và tự dọn; không dùng DSN production.
go test ./internal/acceptance -run TestPollAcceptance -count=1 -v -timeout 8m

# luxtory/backend
go test ./...

# tm/frontend
npm run typecheck
npm run build
npm test -- src/lib/comment.queue.test.ts src/components/comment/CommentThread.test.tsx src/components/poll/PollEmbed.test.tsx src/lib/blocks/

# tm/bff
npm run typecheck
npm test
```

Trang dev: `http://localhost:3000/ludiskus/polls`; liên kết chia sẻ: `/ludiskus/p/{id}`. Giao diện quản trị dùng route comment-admin hiện có, tab LuPoll và cùng quyền quản trị.

Các biến LuPoll và OAuth client ID của service được bổ sung vào `.env.example`, Compose mẫu và Compose root (API + worker; public RPM ở BFF). Mặc định xem [12](12-trien-khai.md). Khi đổi biến môi trường phải recreate container tương ứng; restart chỉ nạp lại mã được mount. Migrations/seeds vẫn qua startup hiện có. CLI kiểm phiếu lại chỉ đọc: `go run ./cmd/tools/polltally POLL_UUID` từ backend, với `LUDISKUS_DB_DSN` được cấu hình trong môi trường; không in danh tính cử tri.

Harness frontend nằm tại `tm/frontend/tools/lupoll-harness/`, chạy bằng Vite với config trong thư mục đó; dùng API fixture và không ghi dữ liệu thật.

## Nghiệm thu còn lại

- GĐ7: chạy đủ **200 người đồng thời, 2.000 poll mở trong 30 phút**, đo p95 ≤ 80 ms, không 5xx và đối soát không lệch. Chưa có bằng chứng đạt SLO này.
- GĐ7: luồng Chrome thực tế trên ít nhất ba service ngoài ludiskus; hiện các điểm nhúng đã có mã/build nhưng chưa chạy trọn luồng bằng dữ liệu thật.
- Các kịch bản Chrome nhiều tài khoản, toàn bộ thao tác chỉ bàn phím, tab ẩn không phát request, người dùng mất quyền trong phiên, quản trị nhiều pod và thông báo đến lunoti thật cần nghiệm thu riêng. Các unit/acceptance đã chạy không thay thế các kiểm tra này.
- Bộ mẫu ranked hiện có các ca viết tay và kiểm tra hoán vị; chưa đủ 30 ca viết tay theo LP-4.1. Chưa chạy kiểm phiếu lại CLI so với 100 poll chốt hay mở CSV bằng LibreOffice theo checklist.
- Chưa triển khai hoặc nghiệm thu production. Các tài liệu 01–14 giữ vai trò đặc tả; không đánh dấu toàn bộ GĐ0–GĐ7 hoàn tất.
