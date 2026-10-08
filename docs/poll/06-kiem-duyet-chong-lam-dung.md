# 06 — Kiểm duyệt & chống lạm dụng

## 6.1 Bề mặt nội dung

Poll có ít văn bản hơn bình luận nhưng mỗi chữ có sức nặng hơn: câu hỏi hiện to ở đầu chủ đề,
và nhãn lựa chọn do **người khác** thêm vào (khi `allow_user_options`) là đường lọt nội dung
xấu dưới tên người tạo poll.

| Văn bản | Định dạng | Kiểm |
|---------|-----------|------|
| `question` | Văn bản thuần, escape khi render | Độ dài, từ cấm, số liên kết = 0 |
| `description_md` | Markdown `basic` (`markdown.RenderBasic` — đã có) | Như thân bình luận `basic`, ≤ 3 liên kết |
| `label` lựa chọn | Văn bản thuần | Độ dài, từ cấm, không liên kết, không trùng (không phân biệt hoa thường, đã chuẩn hoá NFC + trim + gộp khoảng trắng) |

Không cho markdown trong nhãn: nhãn hiện trong nút bấm, trong thông báo lunoti, trong CSV — một
chỗ quên escape là XSS hoặc CSV injection. CSV xuất ra **phải** tiền tố `'` cho ô bắt đầu bằng
`= + - @` ([09 §9.6](09-backend-api.md)).

## 6.2 Chế độ kiểm duyệt

| Đối tượng | Theo |
|-----------|------|
| Poll độc lập | `poll_policies.moderation_mode`; poll trong Space kín thêm `banned_words` của `space_forums` |
| Poll trong chủ đề/bài trả lời/bình luận | **Trạng thái của Anchor**: Anchor vào hàng chờ ⇒ poll `pending`; Anchor được duyệt ⇒ poll `published` (hook trong `PollAttacher`); Anchor bị từ chối ⇒ poll `rejected` |
| Poll trên Resource của service khác | `poll_policies.moderation_mode` của cặp đó |
| Lựa chọn do người dùng thêm | `none` ⇒ hiện ngay · `post` ⇒ hiện ngay, báo được · `pre` (mặc định khi poll `anonymous`/`secret`) ⇒ `pending`, C/O/M duyệt |

Từ cấm trúng ⇒ như bình luận: vào hàng chờ với `source='banned_word'`, không từ chối thẳng
(tránh dạy kẻ xấu cách lách).

Hàng chờ dùng bảng `moderation_items` có sẵn với `target_type ∈ ('poll','poll_option')` —
hai giá trị enum mới thêm ở `0015` **một mình** (QĐ-12 của LuComment: PG cấm dùng giá trị enum
mới trong cùng transaction đã thêm nó, và `database.Migrate` bọc mỗi file một transaction).
`moderation_items.space_uuid` đã nullable từ `0005` — poll độc lập cá nhân không có Space, ai
duyệt? Ba đường: C (lựa chọn người dùng thêm), quản trị ludiskus (`/admin`), và S với poll trên
nội dung của nó.

## 6.3 Báo cáo

`POST /polls/{id}/report` và `POST /polls/{id}/options/{optionId}/report`, tái dùng
`reports` + `ReportTarget` + ngưỡng tự ẩn `report_auto_hide_threshold` (mặc định 5; poll độc lập
cá nhân dùng `LUDISKUS_POLL_AUTO_HIDE_THRESHOLD`). Lý do: danh sách trắng như bình luận, thêm
`misleading_options` (lựa chọn đánh lừa, thiếu phương án hiển nhiên).

Tự ẩn **lựa chọn** khi đủ báo cáo: lựa chọn chuyển `hidden`, phiếu của nó **không bị xoá** nhưng
không được đếm:

- `single`/`multiple`/`scale`: số đếm lựa chọn đó không hiện; `voter_count` giữ nguyên; người đã
  chọn nó (khi `allow_change_vote`) nhận gợi ý chọn lại.
- `ranked`: lọc khỏi lá phiếu trước khi kiểm (§5.5) — hạng sau dồn lên.
- Khôi phục lựa chọn ⇒ phiếu tự trở lại, không mất gì.

Không bao giờ xoá phiếu do kiểm duyệt: xoá phiếu là sửa ý người bỏ phiếu.

## 6.4 Giới hạn tốc độ

Redis db 6, khoá `poll:rl:*`, fail open như LuComment (rate limit không phải cơ chế bảo mật;
**một người một phiếu** thì nằm ở Postgres, fail closed).

| Khoá | Mặc định |
|------|----------|
| `poll:rl:create:h:{profile}` | 10 poll/giờ (`rate_limit.create_per_hour`) |
| `poll:rl:create:d:{profile}` | 30 poll/ngày |
| `poll:rl:vote:m:{profile}` | 30 lượt ghi phiếu/phút **toàn hệ** (bỏ + đổi + rút) |
| `poll:rl:flip:{profile}:{poll}` | 10 lần đổi phiếu/giờ trên **một** poll — chống "lắc" kết quả `always` để gây nhiễu |
| `poll:rl:option:{profile}:{poll}` | 3 lựa chọn tự thêm/poll |
| `poll:rl:rep:{profile}` | Dùng chung bộ đếm báo cáo `cmt:rl:rep:{profile}` — báo cáo là một hành vi, không tách theo phân hệ |

## 6.5 Thao túng phiếu

Hippo xác thực bằng HipCore; tạo nhiều tài khoản là rẻ. LuPoll **không** tự chống được sybil,
nhưng không làm nó dễ hơn:

| Biện pháp | Chi tiết |
|-----------|----------|
| Tài khoản mới | `profile_cache.created_at` (đã thêm cho LuComment) < `LUDISKUS_POLL_NEW_PROFILE_HOURS` (24h) ⇒ chỉ bỏ phiếu được nếu policy `new_profile_can_vote = true` (mặc định `true` cho Space, `false` cho poll `public` trên Resource công khai) |
| Cử tri đóng | Quyết định quan trọng ⇒ `members` hoặc `invited`. Gợi ý trong hộp thoại khi chọn `secret` mà để `viewers` |
| Cờ bất thường | Ticker đêm: poll có > 30% phiếu từ tài khoản < 24h, hoặc > 50 phiếu trong 1 phút từ tài khoản không có hoạt động nào khác trong ludiskus ⇒ ghi `poll_audit_logs(action='abuse_flag')` và hiện cảnh báo cho C/M. **Không** tự xoá phiếu |
| Không thưởng | QĐ-P18 — không có lý do kinh tế để cày phiếu trong hệ |

## 6.6 Audit

`poll_audit_logs` ghi: tạo, đăng, sửa sau phiếu đầu (kèm bản cũ), đổi mức danh tính, đổi lộ kết
quả, gia hạn, đóng, mở lại, ẩn, xoá, duyệt/từ chối lựa chọn, gắn Anchor, cờ bất thường, xuất CSV.
`actor` = `profile` | `service:{code}` | `system`. **Không** ghi lá phiếu (audit không được
trở thành kênh lộ danh tính). Giao diện poll hiện dòng "Đã sửa câu hỏi 2 lần · đã gia hạn" cho
mọi người xem được poll — người bỏ phiếu có quyền biết luật chơi đã đổi.
