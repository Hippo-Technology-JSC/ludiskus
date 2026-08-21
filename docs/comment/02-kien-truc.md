# 02 — Kiến trúc

## 2.1 Vị trí trong hippo

LuComment **không** là service mới. Nó nằm trong `ludiskus-api` / `ludiskus-worker`, dùng lại
toàn bộ đường danh tính, cache, kiểm duyệt, đính kèm và outbox đang có; chỉ bổ sung
**migration + domain + repository + service + route + component tm**.

```
Người dùng ─cookie httpOnly─▶ app tm ─/api/ludiskus/comments/*─▶ bff ─X-Gw-* (HMAC)─▶ ludiskus-api
   │              <CommentThread> dùng chung ở MỌI trang của MỌI service              │
   │ khách (chưa đăng nhập) ─/api/public/ludiskus/comments/*─▶ bff passthrough ───────▶│
   ▼                                                                                   │
┌───────────┐  JWKS + profile_cache / space_cache / space_member_cache (docs/05)        │
│  hipcore  │◀──────────────────────────────────────────────────────────────────────────┤
│   (IdP)   │                                                                           │
└───────────┘                                            ┌──────────────────────────────▼──┐
                                                          │          ludiskus-api           │
  GET  {base}/api/v1/s2s/resource-context/{type}/{id}      │  Forum: board/topic/post (cũ)   │
  POST {base}/api/v1/s2s/resource-context:batch            │  + BÌNH LUẬN (mới)              │
  ◀╌╌╌ resolver ╌╌╌ lumuse · lukolek · lukode · lugame ╌╌╌╌│                                 │
       lushoop · lutriip · luskool · luwep · …             │  MinIO: đính kèm (bucket cũ)    │
                                                          └───┬──────────────┬──────────────┘
  POST /api/v1/s2s/comments/targets            ╌╌╌╌╌╌╌╌▶ (service đẩy metadata / vô hiệu)
  POST /api/v1/s2s/comments/{id}/moderate      ╌╌╌╌╌╌╌╌▶ (staff của service ẩn 1 bình luận)
                                     enqueue outbox   │              │ cache + rate limit
                            ┌──────────┐◀─────────────┤              ▼
                            │ postgres │  UPDATE ±1    │      ┌──────────────┐
                            │ ludiskus │  cùng tx      │      │ redis (db 6) │
                            │ + outbox │               │      │  cmt:*       │
                            └────┬─────┘               │      └──────────────┘
                                 ▼                     │  summary TTL · rate · flip guard
                    ┌──────────────────────┐  POST /api/v1/events   ┌──────────┐
                    │   ludiskus-worker    │───────────────────────▶│  lunoti  │
                    │ flush notify buffer  │                        └──────────┘
                    │ verify target        │  PUT/DELETE ref ludiskus:comment:{id}
                    │ đối soát số đếm      │◀──────────────────────▶┌──────────┐
                    │ dọn target mồ côi    │   (Interaction Platform)│  lufami  │
                    └──────────────────────┘                        └──────────┘
```

> Không thêm message broker, không thêm container, không thêm database, không thêm bucket.
> Redis chỉ là cache / bộ đếm phụ trợ — **Postgres luôn là nguồn sự thật**.

## 2.2 Các process (không đổi)

| Process | Việc mới của phân hệ này |
|---------|--------------------------|
| `ludiskus-api` | Route `/api/v1/comments/*` (người dùng), `/api/v1/public/comments/*` (khách), `/api/v1/s2s/comments/*` (service), `/api/v1/admin/comment-*` (quản trị); gọi resolver; sanitize; ghi outbox |
| `ludiskus-worker` | 4 ticker mới: flush **notify buffer**, **verify** target `unverified`, **đối soát** số đếm, **dọn** target mồ côi + revision quá hạn. Cùng file `cmd/worker/main.go` |

Không có `LUDISKUS_ROLE` mới, không có binary mới.

## 2.3 Mười sáu quyết định kiến trúc chốt

Mỗi quyết định dưới đây đã được cân nhắc đánh đổi; đổi bất kỳ quyết định nào phải sửa tài
liệu trước khi sửa mã.

| # | Quyết định | Vì sao | Hệ quả |
|---|-----------|--------|--------|
| **QĐ-1** | Là **phân hệ trong `ludiskus`**, không service mới | `ludiskus` đã có markdown+sanitize, cache Profile/Space, kiểm duyệt 4 chế độ, đính kèm MinIO, outbox→lunoti, FTS bỏ dấu. Dựng service mới là copy lại 6 thứ đó | Bảng/route/migration của phân hệ **không JOIN** sang `topics`/`posts`/`boards`; tách được thành `lucomment` sau này chỉ bằng đổi mapping BFF |
| **QĐ-2** | Định danh `service:type:id`, **cùng charset và cùng dạng canonical** với Interaction Platform | Cùng một nội dung phải có **một** danh tính trên cả hai nền tảng, nếu không thì "10 like" và "10 bình luận" nói về hai thứ khác nhau | `resource_type ~ '^[a-z][a-z0-9_]{0,59}$'`, `resource_id ~ '^[A-Za-z0-9_.:-]{1,100}$'` — sao y `lufami` |
| **QĐ-3** | **Tái dùng một hợp đồng resolver**: `s2s/resource-context`, và **fallback** sang `s2s/interaction-context` | 3 service đã cài `interaction-context` (`ludiskus`, `lukolek`, `lufami/quote`). Bắt họ cài thêm `comment-context` với đúng các trường đó là thuế vô nghĩa | Service tích hợp mới **chỉ cài một endpoint**, dùng cho cả like và bình luận. Resolver thử `resource-context` trước, `404`/`405` ⇒ thử `interaction-context`, ghi nhớ đường đã thành công trong Redis 24h ([04 §4.3](04-hop-dong-resource.md)) |
| **QĐ-4** | **Không FK xuyên service**; verify 3 chế độ `strict`/`optimistic`/`trust` | DB tách rời; resolver có thể chết | `comment_targets.state ∈ (unverified, active, gone, blocked)`; `unverified` **không** phát thông báo, **không** đọc công khai |
| **QĐ-5** | Like/Reaction/Bookmark/Share của bình luận **uỷ quyền cho Interaction Platform**, ref `ludiskus:comment:{id}` | Đã cut-over GĐ7 (`0002_interaction_cutover`) bỏ bảng `reactions`. Dựng lại reaction trong `ludiskus` là đi ngược quyết định đã chốt | Phải **mở rộng** provider `InteractionContext` của `ludiskus` để phục vụ `type='comment'`; frontend dùng `<InteractionBar>` sẵn có; `ludiskus` **không** có cột `like_count` cho bình luận |
| **QĐ-6** | Cây trả lời **có trần độ sâu** (mặc định 2, trần cứng 5) + **làm phẳng** khi vượt | Cây vô hạn làm hỏng cả giao diện (thụt lề) lẫn truy vấn (đệ quy) lẫn phân trang | `depth`, `root_id` lưu sẵn; trả lời vượt trần treo vào tổ tiên hợp lệ + ghi `reply_to_profile_uuid` ([05 §5.2](05-cay-va-phan-trang.md)) |
| **QĐ-7** | Phân trang **keyset `(created_at, id)`**, không bao giờ `OFFSET` | Thread sống: có bình luận mới xen vào giữa hai trang ⇒ `OFFSET` lặp/nhảy nội dung | Cursor là base64 của `created_at|id`; root và reply phân trang **độc lập** |
| **QĐ-8** | Comment **không phải** Post; không tự sinh Topic | Xem ranh giới ở [README](README.md). Trộn hai mô hình là ép mọi Resource phải thuộc một Space có forum | Không migration nào chuyển `posts`↔`comments`; hai mảng chỉ chung hạ tầng |
| **QĐ-9** | Kiểm duyệt **bốn đường**: tác giả · chủ nội dung · moderator Space · service sở hữu (S2S) | `ludiskus` **không thể** biết RBAC của 20 service. Nhưng chủ nội dung và moderator Space thì nó biết chắc | Có endpoint `POST /s2s/comments/{id}/moderate` để service tự quyết staff của nó ([06 §6.5](06-phan-quyen.md)) |
| **QĐ-10** | Số đếm **đồng bộ trong cùng transaction**, một câu `UPDATE ±1`, `CHECK (>= 0)`, job đối soát đêm | Quy mô hippo chưa cần eventual consistency; lệch số đếm là lỗi người dùng thấy ngay và mất niềm tin | Không đọc-rồi-ghi; Redis **không** giữ số đếm gốc, chỉ cache TTL ngắn |
| **QĐ-11** | Thông báo qua **outbox → lunoti** + **buffer gom nhóm** theo `(người nhận, target)` | Một Thread nóng sinh hàng trăm event; lunoti không phải nơi chịu trận | Bảng `comment_notify_buffer`, worker flush mỗi 10s, cửa sổ `LUDISKUS_COMMENT_NOTIFY_DEBOUNCE` mặc định 5m ([09 §9.3](09-thong-bao-va-theo-doi.md)) |
| **QĐ-12** | Mở rộng enum `report_target` phải **tách thành hai migration** | `database.Migrate` bọc **mỗi file** trong một transaction; PostgreSQL cấm **dùng** giá trị enum mới trong cùng transaction đã thêm nó (`ERROR: unsafe use of new value "comment" of enum type report_target`) | `0004_comment_report_enum` chỉ có đúng một câu `ALTER TYPE`; `0005_comment_moderation` mới được dùng `'comment'` ([10 §10.5](10-database.md)) |
| **QĐ-13** | Đính kèm **tái dùng bảng `attachments`** + cột `comment_id` | Bucket, presign, dọn mồ côi, kiểm mime/size đã chạy tốt cho `posts` | `CHECK` đúng một trong `post_id`/`comment_id` khác NULL; worker dọn mồ côi không cần sửa logic |
| **QĐ-14** | **Đọc** công khai qua `/api/public/ludiskus/comments/*`; **viết** luôn cần đăng nhập ở v1 | Trang công khai (`lukode` presentation, `lukolek` collection) đã có tiền lệ passthrough ở BFF. Bình luận của khách là bề mặt lạm dụng lớn nhất | BFF thêm một passthrough (điều kiện **chặn** của GĐ5); chỉ mở khi `visibility=public` **và** policy `public_read=true` |
| **QĐ-15** | **Không realtime, không sort `top`** ở v1 | Realtime = container mới; sort `top` = phụ thuộc aggregate ở `lufami` | v1: poll khi tab được focus + `ETag`/`If-None-Match`; sort `newest`/`oldest` + ghim lên đầu |
| **QĐ-16** | Registry service của LuComment là **bảng riêng** trong DB `ludiskus`, **chấp nhận trùng** với `interaction_services` của `lufami` | Không truy vấn xuyên database; không tạo phụ thuộc khởi động `ludiskus → lufami` | Hai seed phải khớp `base_url`/`oauth_client_id`; lệch nhau chỉ ảnh hưởng nền tảng tương ứng, được nêu rõ ở [13 §13.2](13-tich-hop-service.md) |

## 2.4 Ranh giới module trong mã nguồn

Quy tắc kiểm được bằng test (`comment_arch_test.go`):

1. File `*comment*.go` trong `internal/service` **không được** gọi `s.repo.GetTopic`,
   `GetPost`, `ListBoards`, `CreateReply` — tức là **không đọc mảng forum**. Ngoại lệ duy
   nhất: `internal/service/interaction.go` (provider interaction-context) được biết cả hai,
   vì nó phục vụ `topic`/`post`/`reply`/`comment`.
2. File mảng forum **không được** import kiểu `domain.Comment*`.
3. `internal/resolver` **không** import `internal/service` (chống vòng phụ thuộc).
4. Truy vấn bình luận **không** JOIN sang `topics`/`posts`/`boards`. Grep `comment` trong
   `internal/repository/content.go` phải cho 0 kết quả.

Lý do: điều kiện của QĐ-1 — tách thành `lucomment` sau này chỉ được là việc chuyển file,
không phải việc bóc tách truy vấn.

## 2.5 Layout mã nguồn bổ sung

Bám khuôn **phẳng** hiện có của `ludiskus` (một file cho một mảng trong mỗi package), không
tạo cấu trúc `internal/comment/{domain,application,…}`.

```
ludiskus/backend/
├── db/migrations/
│   ├── 0003_comment_core.up.sql          # registry, targets, comments, policies, participants, mentions
│   ├── 0004_comment_report_enum.up.sql   # CHỈ ALTER TYPE report_target ADD VALUE 'comment'  (QĐ-12)
│   ├── 0005_comment_moderation.up.sql    # dùng 'comment': index báo cáo, attachments.comment_id, revisions
│   └── 0006_comment_ops.up.sql           # notify buffer, abuse flags, audit, view đối soát
├── db/seeds/
│   ├── comment_services.json             # (mới) registry khởi tạo
│   ├── comment_policies.json             # (mới) policy mặc định theo service × resource_type
│   └── lunoti_event_types.json           # (sửa) thêm 5 event-type + template bình luận
├── internal/
│   ├── domain/
│   │   └── comment.go                    # ResourceRef, CommentTarget, Comment, Policy, Capabilities, Participant
│   ├── repository/
│   │   ├── comment.go                    # comments + mentions + revisions (keyset, đếm)
│   │   ├── comment_target.go             # targets + registry + participants
│   │   ├── comment_policy.go             # policies + audit + abuse flags
│   │   ├── comment_moderation.go         # hàng chờ + báo cáo cho target_type='comment'
│   │   └── comment_integration_test.go
│   ├── resolver/
│   │   └── resolver.go                   # (mới) client S2S: token + cache + batch + timeout + fallback đường
│   ├── service/
│   │   ├── comment.go                    # tạo/sửa/xoá/đọc/ghim + hợp nhất capabilities khi trả về
│   │   ├── comment_target.go             # ensureTarget, verify, invalidate, lock/unlock
│   │   ├── comment_policy.go             # hợp nhất 4 tầng + cache version
│   │   ├── comment_moderation.go         # 4 chế độ, từ cấm, báo cáo, hàng chờ, S2S moderate
│   │   ├── comment_notify.go             # buffer + event lunoti + theo dõi/mute/đã đọc
│   │   ├── comment_abuse.go              # rate limit + chặn trùng + tín hiệu bất thường
│   │   ├── comment_worker.go             # 4 ticker: flush, verify, đối soát, dọn
│   │   ├── comment_arch_test.go          # chặn vượt ranh giới §2.4
│   │   └── comment_*_test.go
│   ├── markdown/markdown.go              # (sửa) thêm RenderBasic + RenderPlain (docs/08)
│   └── transport/http/
│       ├── comment.go                    # nhóm người dùng
│       ├── comment_public.go             # nhóm công khai — KHÔNG auth
│       ├── comment_s2s.go                # nhóm service token (+ requireServiceClient)
│       └── comment_admin.go              # registry + policy + đối soát
└── cmd/worker/main.go                    # (sửa) 4 ticker mới
```

Frontend (`tm/frontend/src`):

```
├── lib/comment.ts                        # client + type + cursor + hàng đợi ngoại tuyến
├── components/comment/                    # DÙNG CHUNG cho mọi service, KHÔNG nằm trong components/ludiskus
│   ├── CommentThread.tsx                 # điểm nhúng duy nhất mà service khác cần biết
│   ├── CommentList.tsx  CommentItem.tsx  CommentReplies.tsx
│   ├── CommentComposer.tsx  MentionInput.tsx  AttachmentPicker.tsx
│   ├── CommentActions.tsx  CommentReportDialog.tsx  CommentModerationDialog.tsx
│   ├── CommentCount.tsx                  # chỉ số đếm, dùng trong thẻ/feed (batch)
│   ├── CommentProvider.tsx               # gom request summary trong 1 frame → 1 POST
│   └── index.ts
└── pages/ludiskus/
    ├── Comments.tsx                      # "Bình luận của tôi" + hộp thư + hàng chờ kiểm duyệt
    └── CommentPermalink.tsx              # /ludiskus/c/:id → chuyển hướng tới canonicalPath#comment-id
```

## 2.6 Luồng viết một bình luận (high level)

```
[Người dùng bấm Gửi]
      │ POST /api/ludiskus/comments/r/{service}/{type}/{id}/items   (Bearer / X-Gw-*)
      ▼
ludiskus-api:
 1. validate ref + registry active                          → 404 / 400
 2. ensureTarget: đọc DB; chưa có ⇒ resolver theo verify_mode → 503 (strict) | unverified (optimistic)
 3. state gone ⇒ 410 · blocked ⇒ 403 · thread locked ⇒ 423
 4. quyền xem theo visibility (public/authenticated/space/connections/private) → 403
 5. hợp nhất policy 4 tầng ⇒ capabilities; comment tắt ⇒ 403 COMMENT_NOT_ALLOWED
 6. rate limit + chặn trùng nội dung (Redis)                 → 429 / 409
 7. validate độ dài, số liên kết, số đính kèm, độ sâu ⇒ làm phẳng nếu cần
 8. render markdown theo mức policy + sanitize + trích @mention (giải qua profile_cache)
 9. quyết định trạng thái theo chế độ kiểm duyệt ⇒ published | pending
10. MỘT transaction: INSERT comment · UPDATE ±1 số đếm target/parent · UPSERT participant
    · INSERT mentions · UPDATE attachments.comment_id · (nếu pending) INSERT moderation_item
    · INSERT comment_notify_buffer cho từng người nhận
      │                                        │
      ▼                                        ▼
 201 (published) | 202 (pending)         worker: flush buffer ⇒ outbox ⇒ lunoti
      │                                        └─ đồng thời: sync resource ludiskus:comment:{id}
      ▼                                           sang Interaction Platform (best-effort)
 frontend chèn lạc quan vào store
```

Chi tiết từng bước: quyền [06](06-phan-quyen.md) · kiểm duyệt [07](07-kiem-duyet.md) ·
nội dung [08](08-noi-dung-va-dinh-kem.md) · thông báo [09](09-thong-bao-va-theo-doi.md).
