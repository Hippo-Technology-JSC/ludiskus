# 06 — Tìm kiếm

Giai đoạn 1 dùng **Postgres Full-Text Search** (FTS) — không cần search engine
riêng, đúng tinh thần "không thêm hạ tầng" của hippo. Interface `search.Engine`
được tách rời để sau này cắm Meilisearch/Elastic nếu cần.

## 6.1 Yêu cầu

Tìm **Topic** và **Post** theo từ khoá, có:

- Toàn văn tiêu đề + nội dung (tiếng Việt có dấu/không dấu).
- Lọc: theo Space, Board, tag, tác giả, loại topic, trạng thái, khoảng thời gian.
- Xếp hạng: theo độ liên quan (`ts_rank`), pha trộn độ mới & hoạt động.
- Phân trang, tô sáng đoạn khớp (`ts_headline`).

## 6.2 Chỉ mục `tsvector`

Mỗi Topic giữ `search_tsv` (title + body post đầu); mỗi Post giữ `search_tsv`
(nội dung). Dùng cấu hình `simple` + extension **`unaccent`** để khớp tiếng Việt
không dấu, kèm **`pg_trgm`** cho khớp gần đúng (typo / khớp một phần slug, tag).

```sql
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Hàm bất biến để build tsvector có bỏ dấu
CREATE FUNCTION ludiskus_tsv(txt text) RETURNS tsvector
  LANGUAGE sql IMMUTABLE AS
  $$ SELECT to_tsvector('simple', unaccent(coalesce(txt,''))) $$;
```

> `unaccent` mặc định không IMMUTABLE; bọc qua hàm wrapper IMMUTABLE (hoặc dùng
> generated column với biểu thức cố định) để tạo index được. Trọng số `A` cho
> tiêu đề, `B` cho thân bài (qua `setweight`) để tiêu đề khớp xếp cao hơn.

Cập nhật `search_tsv`:

- **Topic**: trigger `BEFORE INSERT/UPDATE` trên `topics` set
  `search_tsv = setweight(ludiskus_tsv(title),'A')`; phần body post đầu cập nhật
  khi post đầu thay đổi (trigger trên `posts` hoặc do worker).
- **Post**: trigger set `search_tsv = ludiskus_tsv(body_md)`.

## 6.3 Index

```sql
CREATE INDEX idx_topics_tsv ON topics USING GIN (search_tsv);
CREATE INDEX idx_posts_tsv  ON posts  USING GIN (search_tsv);
CREATE INDEX idx_topics_title_trgm ON topics USING GIN (title gin_trgm_ops);
CREATE INDEX idx_tags_name_trgm ON tags USING GIN (name gin_trgm_ops);
```

## 6.4 Truy vấn

```sql
-- Tìm topic trong các Space người dùng được xem
SELECT t.id, t.title, t.slug, t.space_uuid, t.board_id,
       ts_rank(t.search_tsv, q) AS rank,
       ts_headline('simple', unaccent(t.title), q) AS title_hl
FROM topics t, websearch_to_tsquery('simple', unaccent($1)) q
WHERE t.search_tsv @@ q
  AND t.status = 'published'
  AND t.space_uuid = ANY($2)          -- Space được phép xem (từ member cache)
  AND ($3::uuid IS NULL OR t.board_id = $3)
ORDER BY rank DESC, t.last_post_at DESC
LIMIT $4 OFFSET $5;
```

- `websearch_to_tsquery` cho cú pháp thân thiện (`"cụm từ"`, `-loại trừ`, `OR`).
- Lọc Space theo **danh sách Space người dùng được xem** dựng từ
  `space_member_cache` + Space công khai — bảo đảm không lộ nội dung riêng tư.
- Q&A: ưu tiên topic `is_resolved` hoặc có `is_answer` khi xếp hạng (tuỳ chọn).

## 6.5 Phạm vi & quyền

- Mặc định tìm trong **một Space** (`?space=`) — ngữ cảnh cộng đồng.
- Tìm liên Space chỉ trong phạm vi Space người dùng là thành viên + Space công
  khai; không bao giờ trả Post `pending`/`hidden`/`deleted` cho người thường.
- Moderator có thể lọc thêm `status=pending` trong Space mình quản.

## 6.6 Đồng bộ chỉ mục

Trigger giữ `search_tsv` luôn đúng theo thời gian thực (đủ cho giai đoạn 1). Khi
đổi sang engine ngoài: `ludiskus-worker` đọc `outbox` loại `index.upsert` để đẩy
document sang engine — interface `search.Engine` đã trừu tượng hoá nên transport
HTTP không đổi.
