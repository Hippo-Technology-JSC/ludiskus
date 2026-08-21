# 05 — Cây trả lời, phân trang & số đếm

## 5.1 Ba trường dựng cây

```
comments(id, parent_id, root_id, depth)
```

- `root_id`: bình luận gốc của nhánh. Root có `root_id = id` (tự trỏ) — nhờ vậy một truy vấn
  `WHERE root_id = ANY($1)` lấy được toàn bộ trả lời của một trang root.
- `depth`: 0 cho root. Lưu sẵn để **không** phải đệ quy.
- `parent_id`: cha trực tiếp — dùng để hiện "đang trả lời ai" và để cộng `reply_count`.

Không dùng `ltree`, không dùng materialized path, không dùng `WITH RECURSIVE`: trần độ sâu
(QĐ-6) làm cho ba cột phẳng là đủ, và mọi truy vấn nằm trong index.

## 5.2 Trần độ sâu và làm phẳng

`max_depth` hiệu lực = min(policy, `capabilities.maxDepth` của Target, trần cứng **5**).
Mặc định **2** (root + trả lời + trả lời của trả lời… không, đúng hơn: `depth ∈ {0,1,2}`).

Thuật toán khi tạo trả lời cho `parent`:

```
if parent.depth + 1 <= maxDepth:
    depth   = parent.depth + 1
    parentID = parent.id
else:
    # LÀM PHẲNG: treo vào tổ tiên sâu nhất còn hợp lệ
    anchor  = tổ tiên của parent có depth == maxDepth - 1      (đi lên bằng parent_id)
    depth   = maxDepth
    parentID = anchor.id
    replyToProfileUUID = parent.author_profile_uuid            # để hiện "@tên"
rootID = parent.root_id
```

Giao diện hiện trả lời làm phẳng với tiền tố `@tên người được trả lời` (một chip bấm được,
cuộn tới bình luận đó). Đây là mô hình của YouTube/Facebook: **hội thoại không mất**, thụt lề
không vô hạn.

`reply_to_profile_uuid` **không** thay `parent_id` — hai thứ khác nhau: `parent_id` là cấu
trúc, `reply_to_profile_uuid` là ngữ nghĩa hiển thị.

Khi `maxDepth = 0` (policy muốn phẳng hoàn toàn, ví dụ ô "để lại lời chúc"): mọi bình luận là
root, nút "Trả lời" bị ẩn (`capabilities.canReply = false`).

## 5.3 Đọc một Thread — hai truy vấn

Yêu cầu: "20 root mới nhất, mỗi root kèm 3 trả lời cũ nhất và tổng số trả lời".

```sql
-- (1) trang root: keyset, ghim lên đầu
SELECT * FROM comments
 WHERE target_id = $1 AND parent_id IS NULL AND status = 'published'
   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))     -- cursor, sort=newest
 ORDER BY is_pinned DESC, created_at DESC, id DESC
 LIMIT $4;

-- (2) trả lời mồi cho các root vừa lấy: LATERAL, không N+1
SELECT r.* FROM unnest($1::uuid[]) AS k(root_id)
 CROSS JOIN LATERAL (
   SELECT * FROM comments c
    WHERE c.root_id = k.root_id AND c.parent_id IS NOT NULL AND c.status = 'published'
    ORDER BY c.created_at ASC, c.id ASC
    LIMIT $2                                                        -- preview_replies, mặc định 3
 ) r;
```

Sau đó **một** lượt `ident.ProfileMap(uuids)` cho toàn bộ tác giả + người được trả lời (hàm
đã có trong `internal/identity`). Tổng: **3 lượt I/O** cho một Thread, bất kể số bình luận.

`reply_count` của mỗi root đọc trực tiếp từ cột (đã đếm sẵn) — không `COUNT(*)`.

Ghim: `is_pinned DESC` đứng **trước** cursor trong `ORDER BY` nên bình luận ghim luôn ở trang
đầu; cursor chỉ áp cho phần không ghim (giới hạn `pin.max_pinned` ≤ 3 giữ cho điều này không
làm lệch phân trang).

## 5.4 Cursor

```
cursor = base64url( "<created_at RFC3339Nano>|<id>" )
```

- Chiều `newest`: `(created_at, id) < (c1, c2)` với `ORDER BY created_at DESC, id DESC`.
- Chiều `oldest`: `(created_at, id) > (c1, c2)` với `ORDER BY created_at ASC, id ASC`.
- Cursor không hợp lệ (giải mã lỗi, thời điểm phi lý) ⇒ `400 INVALID_CURSOR`, **không** âm
  thầm bỏ qua (âm thầm bỏ qua = trả trang đầu, người dùng thấy nội dung lặp).
- Response luôn có `nextCursor` (rỗng nếu hết) và **không** có `total`. Tổng số lấy từ
  `target.comment_count`, không phải `COUNT(*)` mỗi trang.

Trả lời của một root phân trang **độc lập**: `GET /comments/items/{rootId}/replies?cursor=`,
luôn theo chiều `oldest` (hội thoại đọc từ trên xuống).

## 5.5 Sắp xếp

| `sort` | Có ở v1 | Thứ tự |
|--------|---------|--------|
| `newest` | ✓ (mặc định) | `is_pinned DESC, created_at DESC, id DESC` |
| `oldest` | ✓ | `is_pinned DESC, created_at ASC, id ASC` |
| `top` | ✗ (GĐ7) | Cần `like_count` từ Interaction Platform ⇒ cần cột đệm `score_cache` + job kéo aggregate. Yêu cầu `sort=top` trả `400 SORT_NOT_SUPPORTED` chứ **không** âm thầm rơi về `newest` |

`sort_default` của policy quyết định mặc định (ví dụ `luskool` muốn `oldest` để bài học đọc
theo trình tự).

## 5.6 Bình luận mới xuất hiện trong lúc đang đọc

Không realtime (QĐ-15), nhưng phải không khó chịu:

1. Frontend giữ `newestSeenAt` = `created_at` của bình luận mới nhất đã render.
2. Khi tab được focus lại (hoặc mỗi `LUDISKUS_COMMENT_POLL_INTERVAL`, mặc định 60s, chỉ khi
   tab đang hiện), gọi
   `GET /comments/r/{ref}/items?since={newestSeenAt}&limit=20` → chỉ **đếm** và trả các bình
   luận mới hơn.
3. Có mới ⇒ hiện dải "**3 bình luận mới** — bấm để xem", **không** tự chèn (tự chèn làm nhảy
   vị trí đọc). Bấm mới prepend.
4. API đọc Thread trả `ETag` = `"{target.comment_count}-{target.last_comment_id}"`; client gửi
   `If-None-Match` ⇒ `304` khi không đổi. Đây là cách rẻ nhất để 100 tab mở cùng lúc không
   làm nặng DB.

## 5.7 Tìm trong một Thread

`GET /comments/r/{service}/{type}/{id}/search?q=…`

- Dùng `search_tsv` (đã có hàm bất biến `ludiskus_tsv()` — bỏ dấu, `simple`) + `pg_trgm` cho
  truy vấn ngắn, đúng mẫu [06](../06-tim-kiem.md) của forum.
- **Luôn** kèm `target_id = $1` trong `WHERE`. Không có endpoint tìm bình luận xuyên Target
  ([01 §1.2](01-tong-quan.md): ngoài phạm vi vì phải kiểm quyền trên n service).
- Kết quả trả kèm `rootId` để giao diện mở đúng nhánh.

Ngoại lệ duy nhất được phép xuyên Target: `GET /comments/mine?q=` — tìm trong **bình luận của
chính mình**, vì quyền xem là hiển nhiên.

## 5.8 Số đếm — luật bất di bất dịch

| Nơi lưu | Đếm gì |
|---------|--------|
| `comment_targets.comment_count` | Số **root** `published` |
| `comment_targets.reply_count` | Số **reply** `published` (mọi độ sâu) |
| `comment_targets.participant_count` | Số hàng trong `comment_participants` (kể cả `muted`) |
| `comment_targets.pending_count` | Số bình luận `pending` — badge cho moderator |
| `comments.reply_count` | Số con `published` **trực tiếp**? **Không** — số **hậu duệ** `published` trong cùng `root_id` khi bình luận là root; với reply thì đếm con trực tiếp. Xem chú thích dưới |
| `comment_targets.last_comment_at/_id` | Bình luận `published` mới nhất — dùng cho `ETag`, badge chưa đọc, sắp thứ tự hộp thư |

> **Chú thích `comments.reply_count`.** Vì cây có trần 2 và trả lời bị làm phẳng vào độ sâu
> trần, "số hậu duệ" của một root **bằng** số hàng có `root_id = root.id AND parent_id IS NOT
> NULL`. Với reply ở độ sâu 1, `reply_count` là số con trực tiếp. Hàm
> `bumpReplyCounts(tx, comment, delta)` cập nhật **cả** root và cha trực tiếp trong một câu
> `UPDATE … WHERE id = ANY($ids)` — đúng một lần chạm mỗi hàng.

Bốn luật:

1. Cập nhật bằng **một câu `UPDATE … SET x = x + $delta`** trong **cùng transaction** với thay
   đổi trạng thái. Không bao giờ đọc-rồi-ghi.
2. `CHECK (comment_count >= 0)` … cho mọi cột đếm ở DB.
3. Delta do **một** hàm thuần tính: `countDelta(old, new, isRoot) (comments, replies, pending int)`
   — có unit test cho toàn bộ cặp trạng thái (§3.6).
4. Job đối soát đêm (`comment_reconcile`) so cột đếm với `COUNT(*)` thật; lệch ⇒ log `error`
   + tự sửa + ghi `comment_audit_logs`. Số đếm âm ⇒ sửa ngay, không đợi đêm.
