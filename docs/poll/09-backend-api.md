# 09 — Backend API

Gốc `/api/v1`. Phong bì `{"data": …}`; lỗi `{"error": {"code", "message", "details"?}}` qua
`writeError` sẵn có (`transport/http/respond.go`). Qua BFF: `/api/ludiskus/…` (người dùng),
`/api/public/ludiskus/polls/…` (khách).

## 9.1 Đối tượng trả về

```json
{
  "id": "…", "kind": "ranked", "question": "Chủ đề sự kiện tháng 11?",
  "descriptionHtml": "<p>…</p>",
  "anchor": { "service": "ludiskus", "type": "topic", "id": "…", "title": "…", "canonicalPath": "…" },
  "standalone": null,                       // hoặc { "visibility": "space", "spaceUuid": "…" }
  "author": { "kind": "profile", "name": "…", "avatar": "…", "code": "…" },
  "config": { "method": "irv", "maxRanks": 3 },
  "identityMode": "anonymous", "resultsVisibility": "after_close", "whoCanVote": "members",
  "allowChangeVote": true, "allowRetract": true, "allowUserOptions": false,
  "state": "open",                          // effective state (03 §3.4)
  "opensAt": null, "closesAt": "2026-11-01T13:00:00Z", "closedAt": null, "closeReason": null,
  "options": [ { "id": "…", "label": "…", "position": 0, "status": "active", "addedByMe": false } ],
  "voterCount": 37, "eligibleCount": 120, "turnout": 0.308, "quorumPercent": 50,
  "results": null, "resultsHiddenReason": "after_close",
  "viewer": { "hasVoted": true, "choices": [ { "optionId": "…", "rank": 1 } ], "notifyResult": true,
              "canVote": true, "canChange": true, "canRetract": true, "canAddOption": false },
  "capabilities": { "edit": false, "close": false, "reopen": false, "hide": false,
                    "delete": false, "export": false, "viewVoters": false, "viewParticipants": false,
                    "invite": false, "report": true },
  "history": { "editCount": 0, "identityChanged": false, "extended": false },
  "version": "b37-v1"                       // = ETag
}
```

`viewer.choices` trả **lựa chọn của chính người xem** ở mọi mức trừ `secret` (ở `secret` hệ thống
không biết — giao diện hiện "Bạn đã bỏ phiếu" và không có nút đổi). `options` được xáo theo
`hash(poll_id, viewer)` khi `shuffle_options`. `results` có dạng theo kind ([05 §5.5](05-bo-phieu-va-kiem-phieu.md)).

## 9.2 Nhóm người dùng (`authn.UserMiddleware`)

| Method | Path | Mô tả | Thành công |
|--------|------|-------|-----------|
| `POST` | `/polls` | Tạo. Body: nội dung + cấu hình + **một trong** `anchor{service,type,id}` · `standalone{visibility,spaceUuid?}` · `draft:true`; `publish: true` để đăng luôn; `actAsSpaceUuid?`; header `Idempotency-Key` | `201` |
| `GET` | `/polls/{id}` | Một poll đầy đủ (§9.1). `If-None-Match` ⇒ `304` | `200` |
| `PATCH` | `/polls/{id}` | Sửa theo bảng khoá [03 §3.5](03-mo-hinh-mien.md); trường bị khoá ⇒ `409 POLL_LOCKED` kèm `details.field` | `200` |
| `PUT` | `/polls/{id}/options` | Thay toàn bộ danh sách lựa chọn — **chỉ trước phiếu đầu** | `200` |
| `POST` | `/polls/{id}/options` | Thêm một lựa chọn (C sau phiếu đầu, hoặc người dùng khi `allow_user_options`) | `201` / `202` (vào hàng chờ) |
| `POST` | `/polls/{id}/publish` | `draft` ⇒ `published` hoặc `pending` | `200` |
| `POST` | `/polls/{id}/attach` | Gắn nháp vào Anchor ([04 §4.5](04-gan-ket-va-phan-quyen.md)) | `200` |
| `POST` | `/polls/{id}/close` | Đóng ngay | `200` |
| `POST` | `/polls/{id}/reopen` | Body `closesAt` (bắt buộc, > now) | `200` |
| `POST` | `/polls/{id}/extend` | Body `closesAt` (muộn hơn hiện tại) | `200` |
| `DELETE` | `/polls/{id}` | Xoá mềm | `204` |
| `PUT` | `/polls/{id}/vote` | Bỏ/đổi phiếu ([05 §5.1](05-bo-phieu-va-kiem-phieu.md)); trả poll đầy đủ | `200` |
| `DELETE` | `/polls/{id}/vote` | Rút phiếu | `200` |
| `PATCH` | `/polls/{id}/vote/settings` | `{notifyResult}` | `204` |
| `GET` | `/polls/{id}/results` | Kết quả chi tiết (vòng IRV, phân bố) — cùng luật lộ kết quả | `200` |
| `GET` | `/polls/{id}/voters` | [05 §5.7](05-bo-phieu-va-kiem-phieu.md) | `200` |
| `GET` | `/polls/{id}/participants` | Receipt | `200` |
| `GET` | `/polls/{id}/export.csv` | §9.6 | `200 text/csv` |
| `POST`/`DELETE` | `/polls/{id}/invitees` | `{profileUuids:[…]}` ≤ 500/lượt, trần 2.000 | `200` |
| `POST` | `/polls/{id}/report` · `/polls/{id}/options/{oid}/report` | Báo cáo | `201` |
| `POST` | `/polls/{id}/options/{oid}/moderate` | `{action: approve\|reject\|hide\|restore}` | `200` |
| `POST` | `/polls/{id}/moderate` | `{action: hide\|restore}` (O/M) | `200` |
| `GET` | `/polls/r/{service}/{type}/{id}` | Mọi poll gắn vào một Resource (đã lọc quyền) + `capabilities.create` cho Anchor | `200` |
| `POST` | `/polls/summary` | `{ids:[…]}` hoặc `{refs:[…]}` ≤ 100 — thẻ nhỏ cho feed: câu hỏi, state, voterCount, hasVoted | `200` |
| `GET` | `/polls/mine` | `?role=created\|voted\|invited&state=open\|closed&cursor=` | `200` |
| `GET` | `/spaces/{space}/polls` | Poll độc lập trong Space (không gồm poll gắn kết) | `200` |

Mở rộng route **có sẵn** (không route mới):

| Route có sẵn | Thêm vào body |
|--------------|---------------|
| `POST /boards/{id}/topics` (tạo chủ đề, `createTopic`) | `pollIds: [uuid]` ≤ `max_per_anchor` |
| `POST /topics/{id}/posts` (trả lời, `createReply`) | `pollIds` |
| `POST /comments/r/{service}/{type}/{id}/items` (bình luận) | `pollIds` |

Ba handler gọi `PollAttacher.AttachDrafts` **trong cùng transaction** tạo nội dung. Lỗi gắn
⇒ huỷ cả nội dung (không để chủ đề mồ côi poll mà người dùng tưởng đã gắn).

## 9.3 Nhóm công khai (không auth)

| Method | Path | Điều kiện |
|--------|------|-----------|
| `GET`, `HEAD` | `/public/polls/{id}` | Visibility hiệu lực `public` (Anchor `public` hoặc poll độc lập `public`/`unlisted`), Anchor `active`, `policy.public_read`, state ∈ (`open`, `scheduled`, `closed`) |
| `GET`, `HEAD` | `/public/polls/r/{service}/{type}/{id}` | Như trên cho danh sách theo Anchor; **không** liệt kê poll `unlisted` |

Lược bỏ: `viewer`, `capabilities`, danh sách người bỏ phiếu (kể cả `public` — khách không cần
danh sách tên), `author` chỉ còn `name`/`avatar`. Kết quả theo §5.6 với người xem "chưa bỏ phiếu"
(⇒ `after_vote` không bao giờ lộ cho khách trước khi đóng). Cache `poll:pub:{id}` TTL 15s —
**phải** bị xoá khi visibility Anchor siết (cùng chỗ xoá `cmt:pub:{ref}:*` ở
[../comment/04 §4.5](../comment/04-hop-dong-resource.md)).

## 9.4 Nhóm S2S (`authn.ServiceMiddleware` + `CommentServiceForClient`)

Mọi route kiểm `anchor.service` = service của token (`SERVICE_SCOPE_MISMATCH`), như LuComment.

| Method | Path | Mô tả |
|--------|------|-------|
| `POST` | `/s2s/polls` | Tạo poll trên Resource của mình, `author_kind='service'`, `actorProfileUuid?`, `invitees?` |
| `POST` | `/s2s/polls/{id}/attach` | Gắn nháp của người dùng vào Resource của mình (sau khi service tạo Resource) — body `{anchor, actorProfileUuid}`; kiểm nháp thuộc `actorProfileUuid` |
| `GET` | `/s2s/polls?service=&type=&id=` | Poll trên một Resource, **kèm kết quả đầy đủ** (S xem luôn kết quả) |
| `GET` | `/s2s/polls/{id}` | Một poll + kết quả + `participants` khi `who_can_vote ∈ (members, invited)` |
| `POST` | `/s2s/polls/{id}/moderate` | `{action: close\|reopen\|extend\|hide\|restore\|delete, closesAt?, actorProfileUuid, reason?}` |
| `PUT` | `/s2s/polls/{id}/invitees` | Đặt lại danh sách mời (ví dụ `lurp`: thành phần cuộc họp) |

**Không** có route S2S nào trả lựa chọn của từng người ở `anonymous`/`secret`. `owner_only` thì S
xem được qua `GET /s2s/polls/{id}/voters` (S là C của poll service tạo).

Bổ sung cho provider: `GET /s2s/interaction-context/poll/{id}` ([07 §7.3](07-thong-bao-va-tuong-tac.md)).

## 9.5 Nhóm quản trị (`requireServiceClient("ludiskus")`)

| Method | Path |
|--------|------|
| `GET`/`PUT` | `/admin/poll-policies[/{service}/{type}]` |
| `POST` | `/admin/polls/reconcile` — chạy đối soát ngay, trả số dòng lệch đã sửa |
| `GET` | `/admin/polls/abuse-flags` |

Trang quản trị trong tm thêm tab "Bình chọn" vào khu `/comment-admin` sẵn có; theo đúng mẫu
hiện tại, giao diện gọi các route `/api/v1/comment-admin/poll-*` trong nhóm **người dùng** (kiểm
quyền quản trị y như các route `comment-admin` đang có), còn `/admin/*` ở trên dành cho lời gọi bằng token service.

## 9.6 Xuất CSV

`text/csv; charset=utf-8` có BOM (Excel tiếng Việt). Hai dạng:

- `?kind=results` (C, S): lựa chọn, số phiếu, %, (ranked: từng vòng), túc số. Dòng đầu là
  chú thích "Xuất từ LuPoll lúc … — không có giá trị pháp lý".
- `?kind=votes` (C, S, chỉ `public`/`owner_only`): một dòng mỗi `(người, lựa chọn)`.

Ô bắt đầu bằng `=`, `+`, `-`, `@`, tab, CR ⇒ tiền tố `'` (CSV injection). Ghi audit `export`.

## 9.7 Mã lỗi mới

| HTTP | Code | Khi |
|------|------|-----|
| 404 | `POLL_NOT_FOUND` | Không có, hoặc không được thấy (giả vờ không có) |
| 410 | `POLL_DELETED` | Đã xoá |
| 403 | `POLL_DISABLED` | Policy/capabilities tắt |
| 403 | `NOT_ELIGIBLE` | Không thuộc cử tri hợp lệ |
| 409 | `POLL_NOT_OPEN` | `scheduled` |
| 409 | `POLL_CLOSED` | Đã đóng (kể cả đóng giữa lúc kiểm và lúc ghi) |
| 409 | `ALREADY_VOTED` | Đã bỏ phiếu, không được đổi |
| 409 | `POLL_LOCKED` | Sửa trường bị khoá sau phiếu đầu; `details.field` |
| 409 | `POLL_LIMIT` | Vượt `max_per_anchor` |
| 409 | `ANCHOR_UNVERIFIED` | Đăng poll khi Anchor chưa xác minh |
| 409 | `OPTION_DUPLICATE` | Trùng nhãn sau chuẩn hoá |
| 422 | `VOTE_INVALID` | Lá phiếu sai luật kind |
| 422 | `ANCHOR_NOT_ALLOWED` | Anchor là poll, hoặc bình luận dưới poll |
| 422 | `MEMBERS_REQUIRES_SPACE` | `who_can_vote=members` mà không có Space |
| 428 | `IDEMPOTENCY_KEY_REQUIRED` | `PUT /vote`, `POST /polls` thiếu header |

Dùng lại nguyên: `INVALID_REF`, `SERVICE_NOT_REGISTERED`, `RESOURCE_GONE`, `RESOURCE_BLOCKED`,
`RESOURCE_RESOLVER_UNAVAILABLE`, `RATE_LIMITED` (+ `Retry-After`), `SERVICE_SCOPE_MISMATCH`,
`UNKNOWN_SERVICE_CLIENT`.
