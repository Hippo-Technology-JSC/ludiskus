# 11 — Backend API

Tiền tố nội bộ `/api/v1`. Frontend gọi qua BFF: `/api/ludiskus/comments/*` →
`/api/v1/comments/*`. Khách (chưa đăng nhập) gọi `/api/public/ludiskus/comments/*` →
`/api/v1/public/comments/*`.

Quy ước phản hồi bám [`respond.go`](../../backend/internal/transport/http/respond.go) sẵn có:
danh sách `{ "data": [...], "nextCursor": "…" }`, đơn lẻ là object trần, lỗi
`{ "error": { "code": …, "message": … } }`.

**Resource ref trong path**: `/r/{service}/{type}/{id}` — `{id}` phải URL-encode.

## 11.1 Nhóm người dùng (`authn.UserMiddleware`)

### Đọc

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/comments/r/{service}/{type}/{id}` | Thông tin Thread: target (title/canonicalPath/threadState), số đếm, `capabilities`, viewer state (`subscribed`, `muted`, `lastReadAt`), `sortOptions`. Trả `ETag` |
| GET | `/comments/r/{service}/{type}/{id}/items` | Trang root. `?sort=newest\|oldest&cursor=&limit=20&previewReplies=3&since=` |
| GET | `/comments/items/{id}` | Một bình luận (permalink, kèm target tối thiểu + đường dẫn tới nhánh) |
| GET | `/comments/items/{id}/replies` | Trả lời của một root. `?cursor=&limit=20` (luôn chiều `oldest`) |
| GET | `/comments/items/{id}/revisions` | Lịch sử sửa (tác giả xem của mình; moderator xem tất cả) |
| GET | `/comments/r/{service}/{type}/{id}/search` | Tìm trong Thread. `?q=&cursor=` |
| GET | `/comments/r/{service}/{type}/{id}/mention-suggest` | Gợi ý @mention theo `policy.mentions.scope`. `?q=` ≤ 10 kết quả |
| POST | `/comments/summary` | **Batch** cho feed: `{ "refs": [{service,type,id}, …] }` ≤ `LUDISKUS_COMMENT_BATCH_MAX` (100). Trả `data[]` + `skipped[]` (`{ref, reason}`) |
| GET | `/comments/inbox` | Thread có phản hồi mới. `?unread=1&cursor=` |
| GET | `/comments/unread-count` | `{ "count": n }` |
| GET | `/comments/mine` | Bình luận của tôi xuyên service. `?status=&service=&q=&cursor=` |

`GET …/items` response một phần tử:

```json
{
  "id": "01JZ…", "targetId": "01JZ…", "parentId": null, "rootId": "01JZ…", "depth": 0,
  "author": { "profileUuid": "…", "name": "Bình", "avatar": "…", "code": "binh" },
  "authorKind": "profile", "replyToProfile": null,
  "bodyHtml": "<p>…</p>", "markdownMode": "basic",
  "status": "published", "isPinned": false, "replyCount": 4,
  "attachments": [{ "id": "…", "fileName": "…", "kind": "image", "url": "…" }],
  "mentions": ["01JZ…"],
  "editedAt": null, "editCount": 0, "createdAt": "2026-08-21T09:00:00Z",
  "canEdit": true, "canDelete": true, "canModerate": false,
  "previewReplies": [ … ], "deleted": false
}
```

Bình luận đã xoá còn giữ chỗ (bia mộ) trả:
`{ "id": …, "deleted": true, "deletedByAuthor": true, "createdAt": …, "replyCount": 2 }` —
**không** có `bodyHtml`, **không** có `author`.

### Ghi

| Method | Path | Mô tả |
|--------|------|-------|
| POST | `/comments/r/{service}/{type}/{id}/items` | Tạo bình luận / trả lời. `201` published, `202` pending |
| PATCH | `/comments/items/{id}` | Sửa trong cửa sổ (`{bodyMd, attachmentIds}`) |
| DELETE | `/comments/items/{id}` | Xoá mềm |
| POST | `/comments/items/{id}/pin` \| `/unpin` | Ghim (theo `policy.pin.by`) |
| POST | `/comments/items/{id}/hide` \| `/restore` | Ẩn / khôi phục (chủ nội dung hoặc moderator) |
| POST | `/comments/items/{id}/report` | `{reason, note}` → `204` (gọi lại cũng `204`) |
| POST | `/comments/r/{service}/{type}/{id}/lock` \| `/unlock` \| `/close` | Đổi `thread_state` |
| PUT | `/comments/r/{service}/{type}/{id}/subscription` | `{muted}` |
| DELETE | `/comments/r/{service}/{type}/{id}/subscription` | Bỏ theo dõi thủ công |
| POST | `/comments/r/{service}/{type}/{id}/read` | Đánh dấu đã đọc |

`POST …/items` body:

```json
{
  "bodyMd": "Hay quá! cc @binh",
  "parentId": "01JZ…",            // bỏ trống ⇒ bình luận gốc
  "attachmentIds": ["01JZ…"],
  "actAsSpaceUuid": null,          // bình luận thay mặt Space (phải là owner/admin Space đó)
  "markdownMode": null             // bỏ trống ⇒ theo policy; chỉ được chọn mức HẸP HƠN
}
```

Header `Idempotency-Key` (tuỳ chọn nhưng frontend **luôn** gửi): gọi lại trả `200` kèm bình
luận đã tạo, không tạo bản thứ hai.

### Kiểm duyệt (moderator Space)

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/comments/moderation/queue` | `?space=&service=&state=pending&cursor=` — kèm ngữ cảnh đủ để quyết định ([07 §7.5](07-kiem-duyet.md)) |
| POST | `/comments/moderation/{item}/approve` | `{note?}` |
| POST | `/comments/moderation/{item}/reject` | `{note?}` |
| GET | `/comments/reports` | `?space=&cursor=` — báo cáo mở về bình luận |
| POST | `/comments/reports/{id}/resolve` \| `/dismiss` | Xử lý báo cáo |

## 11.2 Nhóm công khai (không auth)

| Method | Path | Mô tả |
|--------|------|-------|
| GET | `/public/comments/r/{service}/{type}/{id}` | Số đếm + `threadState` + `capabilities` rút gọn (`canRead: true, canComment: false`) |
| GET | `/public/comments/r/{service}/{type}/{id}/items` | Trang root, chiều `newest`/`oldest`, `previewReplies` ≤ 3 |
| GET | `/public/comments/items/{id}/replies` | Trả lời |

Điều kiện + trường bị lược: [06 §6.8](06-phan-quyen.md). Không có endpoint ghi. Rate limit theo
IP tại BFF (`LUDISKUS_COMMENT_PUBLIC_RPM`, mặc định 120/phút/IP) — vì không có Profile để đếm.

## 11.3 Nhóm S2S (token client-credentials, `requireServiceClient`)

| Method | Path | Mô tả |
|--------|------|-------|
| POST | `/s2s/comments/targets` | Đẩy/cập nhật metadata ≤ 100 target. Chỉ cho `service_code` của token |
| POST | `/s2s/comments/targets/invalidate` | `{refs, reason}` — xoá/đổi visibility |
| POST | `/s2s/comments/targets/settings` | `{ref, threadState}` — đóng/mở luồng |
| GET | `/s2s/comments/counts` | `?refs=lumuse:movie:1,lumuse:movie:2` (≤ 100) — cho service tự render SSR |
| POST | `/s2s/comments/items` | Đăng bình luận hệ thống ([08 §8.7](08-noi-dung-va-dinh-kem.md)) |
| POST | `/s2s/comments/{id}/moderate` | `{action, actorProfileUuid, reason?, note?}` ([06 §6.5](06-phan-quyen.md)) |
| GET | `/s2s/comments/export` | `?ref=…&cursor=` — xuất toàn bộ Thread (service sao lưu / di trú đi) |

Mọi endpoint S2S kiểm **hai** điều: `aud` của token có trong `comment_services.oauth_client_id`,
**và** `service_code` suy ra khớp `target.service_code`. Không khớp ⇒ `403 SERVICE_SCOPE_MISMATCH`
— một service không bao giờ chạm được Thread của service khác.

## 11.4 Nhóm quản trị

| Method | Path | Mô tả |
|--------|------|-------|
| GET/POST | `/admin/comment-services` | Liệt kê / thêm service (kèm `oauthClientId`, `verifyMode`) |
| PATCH/DELETE | `/admin/comment-services/{code}` | Sửa / vô hiệu (`is_active=false`, không xoá dữ liệu) |
| GET | `/admin/comment-policies` | `?service=` |
| PUT | `/admin/comment-policies/{service}/{type}` | Lưu policy; **trả kèm `warnings[]`** (ví dụ: "`moderation_mode=pre` nhưng service này chưa có `oauth_client_id` ⇒ bình luận sẽ kẹt ở `pending`") |
| POST | `/admin/comments/reconcile-counters` | `?target=` hoặc toàn bộ |
| GET | `/admin/comments/abuse-flags` | `?state=open&cursor=` |
| POST | `/admin/comments/abuse-flags/{id}` | `{state, note}` |
| GET | `/admin/comments/audit` | `?comment=&target=&actor=&cursor=` |

## 11.5 Mã lỗi

| HTTP | `code` | Khi nào |
|------|--------|---------|
| 400 | `INVALID_REF` | `service`/`type`/`id` sai charset |
| 400 | `INVALID_CURSOR` | Cursor không giải mã được |
| 400 | `SORT_NOT_SUPPORTED` | `sort=top` ở v1 (QĐ-15) |
| 401 | `unauthorized` | Không có token / không giải được Profile đang hoạt động |
| 403 | `COMMENT_DISABLED` | `policy.enabled=false` hoặc `capabilities.canRead=false` |
| 403 | `COMMENT_NOT_ALLOWED` | Không thoả `who_can_comment` |
| 403 | `RESOURCE_BLOCKED` | `target.state='blocked'` |
| 403 | `EDIT_WINDOW_CLOSED` | Quá `edit_window_minutes` |
| 403 | `NOT_MEMBER` | `visibility='space'` mà không phải thành viên |
| 403 | `SERVICE_SCOPE_MISMATCH` | Service S2S chạm Thread của service khác |
| 403 | `UNKNOWN_SERVICE_CLIENT` | `aud` không có trong registry |
| 403 | `not_a_service_token` | Token người dùng gọi nhóm S2S ([06 §6.7](06-phan-quyen.md)) |
| 404 | `SERVICE_NOT_REGISTERED` | Service không có/không active trong registry |
| 404 | `not_found` | Bình luận/Target không tồn tại, hoặc `unverified`/`hidden` với người không có quyền |
| 409 | `DUPLICATE_COMMENT` | Cùng `body_hash` trong 60s |
| 410 | `RESOURCE_GONE` | `target.state='gone'` |
| 422 | `VALIDATION_ERROR` | Độ dài, số liên kết, số mention, mime, độ sâu, `reason` báo cáo không hợp lệ |
| 423 | `THREAD_LOCKED` | `thread_state ∈ (locked, closed)` |
| 429 | `RATE_LIMITED` | Vượt rate limit; header `Retry-After` |
| 503 | `RESOURCE_RESOLVER_UNAVAILABLE` | `verify_mode='strict'` mà resolver lỗi |
| 503 | `RESOURCE_RESOLVER_MISSING` | Cả `resource-context` và `interaction-context` đều `404` |

Thông điệp (`message`) **luôn tiếng Việt**, nói được cho người dùng cuối. Mã (`code`) là hợp
đồng cho máy — không đổi khi sửa câu chữ.

## 11.6 Bảng route (để dán vào `router.go`)

```go
r.Route("/api/v1", func(r chi.Router) {
    r.Group(func(r chi.Router) {                       // người dùng
        r.Use(authn.UserMiddleware)
        r.Route("/comments", func(r chi.Router) {
            r.Post("/summary", s.commentSummaryBatch)
            r.Get("/inbox", s.commentInbox)
            r.Get("/unread-count", s.commentUnreadCount)
            r.Get("/mine", s.commentMine)
            r.Route("/r/{service}/{type}/{id}", func(r chi.Router) {
                r.Get("/", s.commentThread)
                r.Get("/items", s.commentList)
                r.Post("/items", s.commentCreate)
                r.Get("/search", s.commentSearch)
                r.Get("/mention-suggest", s.commentMentionSuggest)
                r.Put("/subscription", s.commentSubscribe)
                r.Delete("/subscription", s.commentUnsubscribe)
                r.Post("/read", s.commentMarkRead)
                r.Post("/{action}", s.commentThreadAction)   // lock|unlock|close
            })
            r.Route("/items/{id}", func(r chi.Router) {
                r.Get("/", s.commentGet)
                r.Patch("/", s.commentUpdate)
                r.Delete("/", s.commentDelete)
                r.Get("/replies", s.commentReplies)
                r.Get("/revisions", s.commentRevisions)
                r.Post("/report", s.commentReport)
                r.Post("/{action}", s.commentItemAction)     // pin|unpin|hide|restore
            })
            r.Get("/moderation/queue", s.commentModerationQueue)
            r.Post("/moderation/{item}/approve", s.commentApprove)
            r.Post("/moderation/{item}/reject", s.commentReject)
            r.Get("/reports", s.commentReports)
            r.Post("/reports/{id}/{action}", s.commentReportAction)
        })
    })
    r.Route("/public/comments", func(r chi.Router) {   // KHÔNG middleware
        r.Get("/r/{service}/{type}/{id}", s.publicCommentThread)
        r.Get("/r/{service}/{type}/{id}/items", s.publicCommentList)
        r.Get("/items/{id}/replies", s.publicCommentReplies)
    })
    r.Group(func(r chi.Router) {                       // service + quản trị
        r.Use(authn.ServiceMiddleware)
        r.Route("/s2s/comments", func(r chi.Router) { /* … */ })
        r.Route("/admin", func(r chi.Router) { /* comment-services, comment-policies, … */ })
    })
})
```

> Thứ tự khai báo quan trọng: `/comments/items/{id}/{action}` phải đứng **sau** các route cụ
> thể (`/replies`, `/revisions`, `/report`) vì chi khớp theo thứ tự đăng ký cho wildcard cuối.
> Mẫu này đã dùng cho `topicAction` trong router hiện tại.
