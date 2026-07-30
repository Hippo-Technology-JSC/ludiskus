# 11 — Frontend (tm + BFF)

Frontend ludiskus nằm **trong app tm** (SolidJS) như BG360/luxport/lunoti, đi
qua **BFF** (cookie httpOnly) — **không cần CORS**, cùng origin.

## 11.1 Proxy BFF

Thêm proxy `/api/ludiskus/*` trong [tm/bff/src/index.ts](../../tm/bff/src/index.ts)
**y hệt** mẫu lunoti (đoạn `app.all("/api/lunoti/*")`): chuyển tiếp sang
`config.ludiskusApiUrl` đổi tiền tố `/api/ludiskus` → `/api/v1`, đính Bearer
token user, tự refresh khi 401.

```ts
// --- ludiskus (forum/discussion) proxy ----------------------------------------
app.all("/api/ludiskus/*", async (c) => {
  // ... giống hệt khối lunoti: lấy/refresh token, dựng target từ
  // config.ludiskusApiUrl + pathname.replace(/^\/api\/ludiskus/, "/api/v1")
});
```

Bổ sung `config.ludiskusApiUrl` (env `LUDISKUS_API_URL`, xem
[12](12-trien-khai-docker.md)). Upload đính kèm dùng presigned URL nên FE **PUT
thẳng lên MinIO** (không qua BFF) — chỉ gọi BFF để lấy URL ký.

## 11.2 Route & màn hình (app tm, dưới `/ludiskus`)

| Route | Màn hình |
|-------|----------|
| `/ludiskus` | Danh sách **cộng đồng** (Space-forum người dùng thấy) + ô tìm kiếm toàn cục |
| `/ludiskus/s/:space` | Trang Space: danh sách Board, topic nổi bật/mới, nút "Tạo chủ đề" |
| `/ludiskus/s/:space/b/:board` | Danh sách Topic trong board (sort: mới/nóng/chưa trả lời), ghim lên đầu |
| `/ludiskus/s/:space/t/:slug` | Chi tiết Topic: post đầu + trả lời, InteractionBar dùng chung, đính kèm, hộp soạn trả lời |
| `/ludiskus/s/:space/new` | Trình soạn Topic (Markdown, chọn board/type/tag, kéo-thả đính kèm, @mention) |
| `/ludiskus/search` | Kết quả tìm kiếm (lọc Space/board/tag/tác giả/thời gian) |
| `/ludiskus/s/:space/moderation` | Bảng kiểm duyệt (hàng chờ + báo cáo) — chỉ moderator |
| `/ludiskus/s/:space/settings` | Cấu hình forum (kiểm duyệt, post_policy, từ cấm, moderator) — owner/admin |

## 11.3 Thành phần UI chính

- **Trình soạn Markdown dùng chung** (preview, toolbar, kéo-thả ảnh/tệp: presign
  → PUT MinIO → chèn placeholder → gắn khi gửi; **gợi ý @mention** tra Profile
  thành viên Space) — dùng cho **mô tả Board, thân Topic và Post** ([03 §3.12](03-mo-hinh-mien.md)).
  FE chỉ render `*_html` đã sanitize từ backend, không tự dựng HTML.
- **Cây Topic/Post** threaded theo `reply_to_id`, `InteractionProvider` batch
  summary và `InteractionBar` chung cho topic/post/reply, badge "Câu trả lời".
- **Thanh tìm kiếm** với debounce, tô sáng đoạn khớp (`title_hl` từ `ts_headline`).
- **Bảng kiểm duyệt**: tab Hàng chờ / Báo cáo; nút Duyệt / Từ chối (kèm lý do).
- **Chuông thông báo**: tái dùng **trung tâm thông báo của lunoti** đã có trong
  tm — ludiskus không dựng chuông riêng; click thông báo điều hướng theo
  `action_url` (vd `/ludiskus/s/.../t/...#post-...`).

## 11.4 Hiển thị danh tính

Tên/avatar tác giả lấy kèm trong payload (ludiskus join từ `profile_cache`),
nên FE không phải gọi HipCore. Profile không còn hiệu lực (`is_active=false`)
hiển thị mờ ("Người dùng đã rời").

## 11.5 Quyền trên UI

FE ẩn/hiện nút theo quyền trả về cùng tài nguyên (`can_post`, `can_moderate`,
`is_member`) — backend vẫn là nơi cưỡng chế (xem [10 §10.12](10-backend-api.md)).
