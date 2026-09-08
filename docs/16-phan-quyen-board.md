# 16 — Phân quyền theo Board

Ngày thiết kế: **2026-09-08**. Trạng thái: **thiết kế bổ sung, chưa triển khai**.
Phạm vi: Board/Topic/Post của diễn đàn; LuComment tiếp tục dùng policy theo resource.

## 16.1 Mục tiêu và hiện trạng

Mỗi Board cấu hình được **ai tạo chủ đề**, **ai trả lời** và **ai kiểm duyệt**.
Ví dụ: Board Thông báo chỉ ban quản trị tạo chủ đề nhưng thành viên được trả lời;
Board Hỗ trợ có nhóm kiểm duyệt riêng; Board Biên tập chỉ một số thành viên được đăng.

Hiện tại, `boards.min_role` được lưu nhưng `CreateTopic`/`CreateReply` chưa áp dụng;
quyền đăng dựa trên `space_forums.post_policy` và trạng thái khoá. `requireModerate`
kiểm vai trò Space/`space_moderators`, chưa có phân công moderator theo Board.
Không coi `min_role` hiện có là bằng chứng đã hoàn thành tính năng này.

## 16.2 Mô hình quyền

Ba quyền độc lập:

| Quyền | Phạm vi |
|---|---|
| `create_topic` | Tạo Topic và Post đầu trong Board. |
| `reply` | Tạo Post trả lời trong Topic thuộc Board. |
| `moderate` | Xem hàng chờ/nội dung ẩn, duyệt/từ chối, xử lý report, sửa/xoá nội dung của người khác, ghim/khoá Topic và quản lý trạng thái Q&A/Support trong Board. |

Chỉ **owner/admin Space** được sửa cấu hình quyền Board hoặc thêm/xoá moderator
Board. Moderator Board không được sửa ACL, quản lý Board khác, thay settings Space
hay tự cấp quyền. Phân công xử lý một moderation item hoặc Support Topic không tự
cấp quyền kiểm duyệt.

Quyền đọc vẫn theo Space và trạng thái Topic/Post. Thiết kế này chưa tạo Board riêng
với ACL đọc; người đọc được Board chưa chắc được đăng hoặc được kiểm duyệt.

### Chính sách tạo chủ đề và trả lời

`topicPolicy` và `replyPolicy` có cùng cấu trúc, được cấu hình độc lập:

| `mode` | Ai được phép ở tầng Board |
|---|---|
| `inherit_space` | Không thêm hạn chế; áp `post_policy` của Space. Mặc định cho cả hai quyền. |
| `members` | Thành viên Space hiện còn hiệu lực. |
| `moderators` | Moderator hiệu lực của Board, gồm cả staff Space kế thừa. |
| `admins` | Owner/admin Space. |
| `selected` | Các Profile được chọn cho đúng quyền đó, là thành viên Space còn hiệu lực. |
| `nobody` | Tắt thao tác này cho mọi người, kể cả staff; quản trị viên cần đổi cấu hình trước khi đăng. |

`selected` là danh sách cho phép, không có luật deny riêng. Danh sách rỗng nghĩa là
không ai được phép. Mode khác không nhận danh sách Profile. Quyền kiểm duyệt không
tự cấp quyền đăng khi policy là `selected`, `admins` hoặc `nobody`.

Mọi policy Board chỉ **thu hẹp** quyền đăng của Space. Ví dụ Space `staff_only` thì
thành viên trong danh sách `selected` vẫn không được đăng. Một moderator chỉ được
phân công ở Board không trở thành staff Space để vượt qua `staff_only`.

### Ai kiểm duyệt

```text
moderators hiệu lực của Board
  = owner/admin Space + moderator Space + moderator được gán trực tiếp cho Board
```

Không có tuỳ chọn loại bỏ staff Space khỏi một Board. Đây là đường quản trị dự phòng
khi danh sách moderator Board rỗng hoặc đã rời Space. Quyền bổ sung chỉ có hiệu lực
trên Board được gán, không lan sang Board cùng Space hoặc LuComment.

Moderator gán trực tiếp phải là Profile hoạt động và thành viên Space đang có hiệu
lực. Rời Space/vô hiệu Profile thì quyền gán trực tiếp mất hiệu lực; thu hồi grant
khi đồng bộ thành viên xác nhận sự kiện này. Việc gia nhập lại không tự khôi phục
grant đã thu hồi. Dữ liệu chưa xác minh được không dùng để cấp quyền mới.

Board cha/con chỉ phục vụ tổ chức: **không kế thừa ACL hoặc moderator từ Board cha**
trong phiên bản này. `inherit_space` luôn trỏ trực tiếp tới Space.

## 16.3 Quy tắc áp dụng

Quyền tạo Topic/trả lời được quyết định theo thứ tự:

1. Xác thực Profile hoạt động; forum bật và người dùng có quyền đọc Space.
2. Áp `space_forums.post_policy` theo vai trò Space hiện hành.
3. Kiểm trạng thái khoá: Board khoá chặn cả tạo Topic và reply; Topic khoá chặn reply.
   Staff cũng không vượt khoá ngầm; cần mở khoá bằng thao tác được cấp quyền.
4. Áp `topicPolicy` hoặc `replyPolicy` của Board.
5. Áp chế độ kiểm duyệt Space và các kiểm tra nội dung/tệp. Có quyền đăng không đồng
   nghĩa bài được publish ngay.

Moderator hiệu lực được miễn tiền kiểm **trong Board mình quản**, theo ngoại lệ
staff hiện có. `moderation_mode`, từ cấm và ngưỡng report vẫn kế thừa Space; quyền
chọn người kiểm duyệt không phải cấu hình chế độ tiền/hậu kiểm riêng Board.

Tác giả giữ quyền sửa/xoá bài của mình theo quy tắc nội dung hiện có, nếu còn quyền
đọc; mất quyền tạo bài mới không chuyển quyền sở hữu bài cũ. Những thao tác này vẫn
chịu kiểm tra trạng thái/kiểm duyệt hiện hành. Quyền chọn đáp án và resolve/reopen
của tác giả Topic được giữ; người khác phải là moderator hiệu lực của Board.

Backend xác định Board từ dữ liệu thật: `Post → Topic → Board`, report/moderation
item → target → Board. Không tin `boardId`/`spaceUuid` do client gửi để chứng minh quyền.
Topic chuyển Board phải kiểm quyền quản lý cả nguồn và đích; sau khi chuyển áp ACL
đích cho Topic, Post, tệp và hàng chờ. Grant cũ không đi theo Topic.

Không đổi bài đã publish thành pending chỉ vì sửa ACL. Bài pending tiếp tục chờ,
nhưng danh sách người được duyệt lấy theo quyền hiện tại, không theo snapshot lúc đăng.

## 16.4 Dữ liệu dự kiến

| Bảng/trường | Mục đích và ràng buộc |
|---|---|
| `board_permissions` | `board_id` PK/FK Board, `topic_mode`, `reply_mode`, `version bigint`, `updated_by_profile_uuid`, `updated_at`. Mode có CHECK theo §16.2; xoá Board thì cascade cấu hình. |
| `board_permission_profiles` | `(board_id, action, profile_uuid)` PK; action chỉ `create_topic`/`reply`; danh sách cho mode `selected`. |
| `board_moderators` | `(board_id, profile_uuid)` PK, `granted_by_profile_uuid`, `created_at`. Chỉ chứa moderator gán trực tiếp. |
| `board_permission_audit` | Actor, Board/Space, version trước/sau, thay đổi policy/grants, thời điểm và request ID; lưu lịch sử kể cả khi Board bị xoá. |

UUID Profile/Space thuộc HipCore, không có FK liên DB. Không lưu tên Profile làm khoá
quyền. Backend xác minh membership và cùng Space trước khi nhận grant. Giới hạn
100 Profile mỗi danh sách và 100 moderator trực tiếp/Board cho bản đầu; vượt giới
hạn trả validation error, không cắt danh sách âm thầm.

Lưu policy, các danh sách, version và audit trong **một transaction**. Quyền của
actor phải được kiểm lại lúc ghi; một grant chỉ được thay đổi bởi owner/admin còn
hiệu lực. Mutation đăng/duyệt kiểm lại ACL version trong transaction để tránh request
đã mở form trước khi thu hồi vẫn ghi thành công. Khi ACL thay đổi trong lúc xử lý,
trả conflict để client tải lại quyền, không tự thử lại bằng quyền cũ.

Bản đầu đọc ACL Board từ DB; không cache quyết định ghi trong process. Nếu bổ sung
cache sau này, key phải gồm Board + Profile + ACL version + version membership.
Mất cache thì đọc DB; không dùng cache stale để cấp quyền. Độ trễ thay đổi membership
HipCore vẫn phụ thuộc luồng đồng bộ hiện có và phải được nghiệm thu riêng; khi cấp/
thu hồi grant cần refresh membership hoặc trả lỗi nếu không xác minh được.

## 16.5 API và capability dự kiến

Tiền tố `/api/v1`, qua BFF `/api/ludiskus`. **Các endpoint bên dưới chưa tồn tại.**

| API | Quyền và hành vi |
|---|---|
| `GET /boards/{id}/permissions` | Owner/admin Space đọc cấu hình, danh sách cấp trực tiếp, danh sách staff kế thừa và version. Không công khai toàn bộ ACL cho mọi người đọc Board. |
| `PUT /boards/{id}/permissions` | Owner/admin thay toàn bộ policy + moderator trực tiếp; yêu cầu `expectedVersion`, trả cấu hình/version mới. |
| `GET /boards/{id}/capabilities` | Người được đọc Board lấy quyền hiệu lực **của chính mình**, không truyền Profile khác để giả lập. |

Ví dụ Board Thông báo: chỉ staff được tạo chủ đề, thành viên được trả lời:

```json
{
  "expectedVersion": 3,
  "topicPolicy": { "mode": "moderators", "profileUuids": [] },
  "replyPolicy": { "mode": "members", "profileUuids": [] },
  "moderatorProfileUuids": ["20000000-0000-4000-8000-000000000002"]
}
```

Ví dụ capability:

```json
{
  "version": 4,
  "canCreateTopic": false,
  "canReply": true,
  "canModerate": false,
  "canManagePermissions": false,
  "reasons": { "canCreateTopic": "board_policy_denied" }
}
```

Capability Board chưa tính trạng thái từng Topic; `Topic.canReply` tiếp tục kết hợp
trạng thái Topic. Board list trả capability theo lô để UI không gọi một request/Board.
Dùng chung evaluator ở endpoint mutation, capability, queue, search trạng thái ẩn và
đọc tệp pending/hidden. Không suy `canCreateTopic` từ capability Space hiện tại.

Quy ước lỗi theo envelope backend hiện hành `{error:{code,message}}`: 401 khi chưa
xác thực, 403 khi thiếu quyền, 404 khi target không tồn tại/không được tiết lộ,
409 khi `expectedVersion` cũ, 422 khi mode/Profile/membership không hợp lệ.

Queue/report cấp Space phải lọc theo tập Board người dùng được kiểm duyệt, **trước
phân trang và tính tổng**. Hỗ trợ bộ lọc `board`; đổi board ID không mở rộng phạm vi.
Các endpoint approve/reject/resolve/dismiss luôn kiểm lại quyền từ target thật.
Moderator Board được đọc pending/hidden và tệp của Board mình quản, không được đọc
nội dung ẩn của Board khác qua search, URL trực tiếp hoặc export.

## 16.6 Giao diện quản trị

Trong **Quản lý chuyên mục → chọn Board → Phân quyền**:

- Hai bộ chọn “Ai được tạo chủ đề?” và “Ai được trả lời?”, kèm mô tả quyền Space
  đang giới hạn kết quả. Mode “Người được chọn” mở tìm thành viên theo tên/@code,
  lưu UUID; hiển thị người đã mất hiệu lực để quản trị viên xử lý.
- “Người kiểm duyệt”: staff Space kế thừa hiển thị riêng, không có nút xoá tại Board;
  moderator gán trực tiếp có nút thêm/gỡ. Chỉ owner/admin thấy thao tác chỉnh quyền.
- Lưu nguyên bộ cấu hình. Nếu có người khác đã sửa, báo xung đột và giữ bản chỉnh sửa
  để đối chiếu với version mới; không tự ghi đè.
- Nút tạo Topic/reply và menu kiểm duyệt theo capability backend; nếu bị thu hồi khi
  đang soạn, giữ bản nháp/tệp, tải lại quyền và hiển thị lý do từ chối.
- Moderator Board có lối vào màn hình kiểm duyệt trong Board, dù không có quyền quản
  trị Space. Màn hình tổng chỉ hiển thị Board được quản; không dùng guard moderator
  Space để chặn toàn bộ trang.

## 16.7 Tương thích và triển khai

1. Thêm bảng/contract/evaluator và test; chọn số migration theo checkout lúc triển khai.
2. Board cũ mặc định `inherit_space` cho cả hai thao tác, chưa có moderator trực tiếp;
   giữ quyền thực tế hiện đang chạy. **Không tự kích hoạt `min_role` cũ** vì trường
   này chưa được enforce và có thể làm người dùng mất quyền bất ngờ.
3. Đánh dấu `min_role` deprecated; API cũ còn gửi trường này phải nhận thông báo rõ
   rằng cần chuyển sang `/permissions`, không lưu im lặng một giá trị không có hiệu lực.
   Không đọc đồng thời `min_role` và policy mới để tính hai lớp quyền mâu thuẫn.
4. Áp evaluator cho mọi đường đăng/duyệt/đọc nội dung ẩn và capability trước khi mở UI.
   Service token không được bỏ qua Board policy; đường đăng hệ thống cần quyền service
   được cấp riêng, ngoài phạm vi cấp grant cho Profile trong thiết kế này.
5. Đưa UI cấu hình vào sử dụng sau khi migration và backend guard hoạt động đầy đủ.

Rollback code về bản chưa biết ACL sẽ bỏ mất giới hạn Board. Khi đã có Board được
cấu hình, không rollback riêng backend trong lúc vẫn cho phép ghi: tạm khoá ghi
forum, bảo toàn bảng ACL/audit và chỉ mở lại sau khi phục hồi enforcement tương ứng.

## 16.8 Tiêu chí nghiệm thu khi triển khai

- [ ] Hai Board cùng Space có người được đăng và moderator khác nhau; quyền không lan.
- [ ] Mỗi mode của topic/reply kết hợp đủ `post_policy` Space; moderator không tự vượt
  `selected`/`nobody`, Space `staff_only`, Board/Topic khoá.
- [ ] Selected Profile ngoài Space, inactive, trùng hoặc UUID sai bị từ chối; rời Space
  mất hiệu lực và grant đã thu hồi không tự sống lại khi tái gia nhập.
- [ ] Moderator Board không sửa ACL/Space settings; owner/admin vẫn quản lý được ACL
  khi danh sách rỗng; moderator Space giữ phạm vi kế thừa.
- [ ] Gọi trực tiếp mutation, giả board ID trong body, sửa item/report ID hoặc search
  pending/hidden không vượt quyền; tệp có cùng ranh giới đọc như bài chứa nó.
- [ ] Queue/search/report phân trang và tổng số chỉ trong Board được phép, không lộ
  dữ liệu Board khác; UI hỗ trợ moderator chỉ có quyền tại Board.
- [ ] Thu hồi quyền khi đang mở editor/duyệt hoặc request đang chạy được kiểm ở backend;
  hai admin lưu cùng version thì chỉ một lần thành công; bản nháp không mất.
- [ ] Chuyển Topic áp ACL Board đích; thay policy không đổi trạng thái bài cũ; grant
  kiểm duyệt không được suy từ assignee hoặc vai trò LuComment.
- [ ] Migration với dữ liệu cũ giữ quyền thực tế, audit đủ actor/before/after; rollback
  không làm mở quyền ngoài ý muốn. Test backend, PostgreSQL và browser desktop/mobile.
