package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"ludiskus/internal/domain"
)

// --- topics -----------------------------------------------------------------

const topicCols = `id, space_uuid, board_id, author_profile_uuid, title, slug, type, status,
	is_pinned, is_resolved, answer_post_id, reply_count, view_count,
	last_post_at, last_post_profile_uuid, created_at, updated_at`

func scanTopic(row pgx.Row, t *domain.Topic) error {
	return row.Scan(&t.ID, &t.SpaceUUID, &t.BoardID, &t.AuthorProfileUUID, &t.Title, &t.Slug,
		&t.Type, &t.Status, &t.IsPinned, &t.IsResolved, &t.AnswerPostID, &t.ReplyCount,
		&t.ViewCount, &t.LastPostAt, &t.LastPostProfileUUID,
		&t.CreatedAt, &t.UpdatedAt)
}

// CreateTopicWithPost tạo Topic + Post đầu trong một transaction, cập nhật đếm
// board. status truyền vào (published | pending tuỳ kiểm duyệt).
func (r *Repo) CreateTopicWithPost(ctx context.Context, t domain.Topic, p domain.Post) (*domain.Topic, *domain.Post, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	var outT domain.Topic
	err = scanTopic(tx.QueryRow(ctx, `
		INSERT INTO topics (space_uuid, board_id, author_profile_uuid, title, slug, type, status, last_post_at, last_post_profile_uuid)
		VALUES ($1,$2,$3,$4,$5,$6::topic_type,$7::topic_status, now(), $3)
		RETURNING `+topicCols,
		t.SpaceUUID, t.BoardID, t.AuthorProfileUUID, t.Title, t.Slug, t.Type, t.Status), &outT)
	if err != nil {
		if isUnique(err) {
			return nil, nil, domain.ErrConflict
		}
		return nil, nil, err
	}

	var outP domain.Post
	if err := scanPost(tx.QueryRow(ctx, `
		INSERT INTO posts (topic_id, space_uuid, author_profile_uuid, is_first, body_md, body_html, status)
		VALUES ($1,$2,$3, true, $4,$5,$6::post_status)
		RETURNING `+postCols,
		outT.ID, t.SpaceUUID, t.AuthorProfileUUID, p.BodyMD, p.BodyHTML, p.Status), &outP); err != nil {
		return nil, nil, err
	}

	if _, err := tx.Exec(ctx, `UPDATE boards SET topic_count = topic_count + 1,
		post_count = post_count + 1, last_activity_at = now() WHERE id = $1`, t.BoardID); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return &outT, &outP, nil
}

func (r *Repo) GetTopic(ctx context.Context, id string) (*domain.Topic, error) {
	var t domain.Topic
	err := scanTopic(r.pool.QueryRow(ctx, `SELECT `+topicCols+` FROM topics WHERE id = $1`, id), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *Repo) GetTopicBySlug(ctx context.Context, spaceUUID, slug string) (*domain.Topic, error) {
	var t domain.Topic
	err := scanTopic(r.pool.QueryRow(ctx, `SELECT `+topicCols+` FROM topics
		WHERE space_uuid = $1 AND slug = $2`, spaceUUID, slug), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

// SlugExists kiểm tra slug đã dùng trong Space chưa (sinh slug duy nhất).
func (r *Repo) SlugExists(ctx context.Context, spaceUUID, slug string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM topics
		WHERE space_uuid = $1 AND slug = $2)`, spaceUUID, slug).Scan(&exists)
	return exists, err
}

// ListTopics liệt kê topic published trong board theo sort (latest|top|unanswered).
func (r *Repo) ListTopics(ctx context.Context, boardID, sort string, limit, offset int) ([]domain.Topic, error) {
	order := "t.is_pinned DESC, t.last_post_at DESC NULLS LAST"
	where := "t.board_id = $1 AND t.status = 'published'"
	switch sort {
	case "top":
		order = "t.is_pinned DESC, t.reply_count DESC, t.view_count DESC, t.last_post_at DESC NULLS LAST"
	case "unanswered":
		where += " AND t.reply_count = 0"
	}
	rows, err := r.pool.Query(ctx, `SELECT `+topicColsT+` FROM topics t
		WHERE `+where+` ORDER BY `+order+` LIMIT $2 OFFSET $3`, boardID, limit, offset)
	if err != nil {
		return nil, err
	}
	return collectTopics(rows)
}

func (r *Repo) IncrementTopicView(ctx context.Context, id string) {
	r.pool.Exec(ctx, `UPDATE topics SET view_count = view_count + 1 WHERE id = $1`, id)
}

func (r *Repo) UpdateTopicMeta(ctx context.Context, id, title string) (*domain.Topic, error) {
	var t domain.Topic
	err := scanTopic(r.pool.QueryRow(ctx, `UPDATE topics SET title = $2 WHERE id = $1
		RETURNING `+topicCols, id, title), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *Repo) SetTopicStatus(ctx context.Context, id, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE topics SET status = $2::topic_status WHERE id = $1`, id, status)
	return err
}

func (r *Repo) SetTopicPinned(ctx context.Context, id string, pinned bool) error {
	_, err := r.pool.Exec(ctx, `UPDATE topics SET is_pinned = $2 WHERE id = $1`, id, pinned)
	return err
}

func (r *Repo) SetTopicAnswer(ctx context.Context, topicID, postID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE posts SET is_answer = false WHERE topic_id = $1`, topicID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET is_answer = true WHERE id = $1 AND topic_id = $2`, postID, topicID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE topics SET answer_post_id = $2, is_resolved = true WHERE id = $1`, topicID, postID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// topicColsT thêm prefix t. cho truy vấn có alias.
const topicColsT = `t.id, t.space_uuid, t.board_id, t.author_profile_uuid, t.title, t.slug, t.type,
	t.status, t.is_pinned, t.is_resolved, t.answer_post_id, t.reply_count, t.view_count,
	t.last_post_at, t.last_post_profile_uuid, t.created_at, t.updated_at`

func collectTopics(rows pgx.Rows) ([]domain.Topic, error) {
	defer rows.Close()
	out := []domain.Topic{}
	for rows.Next() {
		var t domain.Topic
		if err := scanTopic(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- posts ------------------------------------------------------------------

const postCols = `id, topic_id, space_uuid, author_profile_uuid, reply_to_id, is_first, body_md,
	body_html, is_answer, status, edited_at, created_at, updated_at`

func scanPost(row pgx.Row, p *domain.Post) error {
	return row.Scan(&p.ID, &p.TopicID, &p.SpaceUUID, &p.AuthorProfileUUID, &p.ReplyToID, &p.IsFirst,
		&p.BodyMD, &p.BodyHTML, &p.IsAnswer, &p.Status, &p.EditedAt,
		&p.CreatedAt, &p.UpdatedAt)
}

// CreateReply tạo post trả lời + cập nhật đếm topic/board (nếu published).
func (r *Repo) CreateReply(ctx context.Context, p domain.Post) (*domain.Post, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var out domain.Post
	if err := scanPost(tx.QueryRow(ctx, `
		INSERT INTO posts (topic_id, space_uuid, author_profile_uuid, reply_to_id, body_md, body_html, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7::post_status)
		RETURNING `+postCols,
		p.TopicID, p.SpaceUUID, p.AuthorProfileUUID, p.ReplyToID, p.BodyMD, p.BodyHTML, p.Status), &out); err != nil {
		return nil, err
	}

	if out.Status == domain.StatusPublished {
		if _, err := tx.Exec(ctx, `UPDATE topics SET reply_count = reply_count + 1,
			last_post_at = now(), last_post_profile_uuid = $2 WHERE id = $1`,
			p.TopicID, p.AuthorProfileUUID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE boards SET post_count = post_count + 1,
			last_activity_at = now() WHERE id = (SELECT board_id FROM topics WHERE id = $1)`,
			p.TopicID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// HasPublishedPostInSpace cho biết profile đã từng có bài published trong Space
// (phục vụ chế độ kiểm duyệt first_post).
func (r *Repo) HasPublishedPostInSpace(ctx context.Context, spaceUUID, profileUUID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM posts
		WHERE space_uuid = $1 AND author_profile_uuid = $2 AND status = 'published')`,
		spaceUUID, profileUUID).Scan(&exists)
	return exists, err
}

func (r *Repo) GetPost(ctx context.Context, id string) (*domain.Post, error) {
	var p domain.Post
	err := scanPost(r.pool.QueryRow(ctx, `SELECT `+postCols+` FROM posts WHERE id = $1`, id), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *Repo) FirstPost(ctx context.Context, topicID string) (*domain.Post, error) {
	var p domain.Post
	err := scanPost(r.pool.QueryRow(ctx, `SELECT `+postCols+` FROM posts
		WHERE topic_id = $1 AND is_first ORDER BY created_at LIMIT 1`, topicID), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *Repo) ListPosts(ctx context.Context, topicID string, limit, offset int) ([]domain.Post, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+postCols+` FROM posts
		WHERE topic_id = $1 AND status = 'published' ORDER BY created_at LIMIT $2 OFFSET $3`,
		topicID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Post{}
	for rows.Next() {
		var p domain.Post
		if err := scanPost(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) UpdatePost(ctx context.Context, id, bodyMD, bodyHTML string) (*domain.Post, error) {
	var p domain.Post
	err := scanPost(r.pool.QueryRow(ctx, `UPDATE posts SET body_md = $2, body_html = $3, edited_at = now()
		WHERE id = $1 RETURNING `+postCols, id, bodyMD, bodyHTML), &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *Repo) SetPostStatus(ctx context.Context, id, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE posts SET status = $2::post_status WHERE id = $1`, id, status)
	return err
}

// PublishPost chuyển post pending → published và cập nhật đếm (khi duyệt).
func (r *Repo) PublishPost(ctx context.Context, id string) (*domain.Post, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var p domain.Post
	if err := scanPost(tx.QueryRow(ctx, `UPDATE posts SET status = 'published'
		WHERE id = $1 RETURNING `+postCols, id), &p); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if !p.IsFirst {
		if _, err := tx.Exec(ctx, `UPDATE topics SET reply_count = reply_count + 1,
			last_post_at = now(), last_post_profile_uuid = $2 WHERE id = $1`,
			p.TopicID, p.AuthorProfileUUID); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `UPDATE topics SET status = 'published' WHERE id = $1`, p.TopicID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

// --- tags -------------------------------------------------------------------

func (r *Repo) UpsertTag(ctx context.Context, spaceUUID, slug, name string) (*domain.Tag, error) {
	var t domain.Tag
	err := r.pool.QueryRow(ctx, `
		INSERT INTO tags (space_uuid, slug, name) VALUES ($1,$2,$3)
		ON CONFLICT (space_uuid, slug) DO UPDATE SET usage_count = tags.usage_count + 1
		RETURNING id, space_uuid, slug, name, usage_count`,
		spaceUUID, slug, name).Scan(&t.ID, &t.SpaceUUID, &t.Slug, &t.Name, &t.UsageCount)
	return &t, err
}

func (r *Repo) AttachTag(ctx context.Context, topicID, tagID string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO topic_tags (topic_id, tag_id) VALUES ($1,$2)
		ON CONFLICT DO NOTHING`, topicID, tagID)
	return err
}

func (r *Repo) TagsForTopics(ctx context.Context, topicIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(topicIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT tt.topic_id, tg.slug FROM topic_tags tt
		JOIN tags tg ON tg.id = tt.tag_id WHERE tt.topic_id = ANY($1) ORDER BY tg.slug`, topicIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, slug string
		if err := rows.Scan(&tid, &slug); err != nil {
			return nil, err
		}
		out[tid] = append(out[tid], slug)
	}
	return out, rows.Err()
}

func (r *Repo) ListTags(ctx context.Context, spaceUUID, query string, limit int) ([]domain.Tag, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, space_uuid, slug, name, usage_count FROM tags
		WHERE space_uuid = $1 AND ($2 = '' OR name ILIKE '%'||$2||'%' OR slug ILIKE '%'||$2||'%')
		ORDER BY usage_count DESC, name LIMIT $3`, spaceUUID, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Tag{}
	for rows.Next() {
		var t domain.Tag
		if err := rows.Scan(&t.ID, &t.SpaceUUID, &t.Slug, &t.Name, &t.UsageCount); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- mentions ---------------------------------------------------------------

func (r *Repo) MentionsForPost(ctx context.Context, postID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT profile_uuid FROM post_mentions WHERE post_id = $1`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *Repo) AddMentions(ctx context.Context, postID string, profileUUIDs []string) error {
	for _, u := range profileUUIDs {
		if _, err := r.pool.Exec(ctx, `INSERT INTO post_mentions (post_id, profile_uuid)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, postID, u); err != nil {
			return err
		}
	}
	return nil
}

// --- attachments ------------------------------------------------------------

const attCols = `id, COALESCE(space_uuid::text,''), post_id, comment_id, uploader_profile_uuid, object_key, file_name,
	content_type, size_bytes, kind, width, height, status, created_at`

func scanAttachment(row pgx.Row, a *domain.Attachment) error {
	return row.Scan(&a.ID, &a.SpaceUUID, &a.PostID, &a.CommentID, &a.UploaderProfileUUID, &a.ObjectKey,
		&a.FileName, &a.ContentType, &a.SizeBytes, &a.Kind, &a.Width, &a.Height, &a.Status, &a.CreatedAt)
}

func (r *Repo) CreateAttachment(ctx context.Context, a domain.Attachment) (*domain.Attachment, error) {
	var out domain.Attachment
	err := scanAttachment(r.pool.QueryRow(ctx, `
		INSERT INTO attachments (space_uuid, uploader_profile_uuid, object_key, file_name,
			content_type, size_bytes, kind, status)
		VALUES (NULLIF($1,'')::uuid,$2,$3,$4,$5,$6,$7::attach_kind,'pending')
		RETURNING `+attCols,
		a.SpaceUUID, a.UploaderProfileUUID, a.ObjectKey, a.FileName, a.ContentType,
		a.SizeBytes, a.Kind), &out)
	return &out, err
}

func (r *Repo) GetAttachment(ctx context.Context, id string) (*domain.Attachment, error) {
	var a domain.Attachment
	err := scanAttachment(r.pool.QueryRow(ctx, `SELECT `+attCols+` FROM attachments WHERE id = $1`, id), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &a, err
}

// AttachToPost gắn các attachment pending vào post (kiểm space khớp).
func (r *Repo) AttachToPost(ctx context.Context, ids []string, postID, spaceUUID string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE attachments SET post_id = $2, status = 'attached'
		WHERE id = ANY($1::uuid[]) AND space_uuid = $3 AND status = 'pending'`, ids, postID, spaceUUID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(ids)) {
		return fmt.Errorf("%w: đính kèm không hợp lệ", domain.ErrValidation)
	}
	if err := enqueueAttachedPersonalFiles(ctx, tx, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) AttachmentsForPosts(ctx context.Context, postIDs []string) (map[string][]domain.Attachment, error) {
	out := map[string][]domain.Attachment{}
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+attCols+` FROM attachments
		WHERE post_id = ANY($1) AND status = 'attached' ORDER BY created_at`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.Attachment
		if err := scanAttachment(rows, &a); err != nil {
			return nil, err
		}
		if a.PostID != nil {
			out[*a.PostID] = append(out[*a.PostID], a)
		}
	}
	return out, rows.Err()
}

func (r *Repo) AttachmentsForComments(ctx context.Context, commentIDs []string) (map[string][]domain.Attachment, error) {
	out := map[string][]domain.Attachment{}
	if len(commentIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+attCols+` FROM attachments
		WHERE comment_id = ANY($1::uuid[]) AND status = 'attached' ORDER BY created_at`, commentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.Attachment
		if err := scanAttachment(rows, &a); err != nil {
			return nil, err
		}
		if a.CommentID != nil {
			out[*a.CommentID] = append(out[*a.CommentID], a)
		}
	}
	return out, rows.Err()
}

func (r *Repo) ListOrphanAttachments(ctx context.Context, ttlSeconds, limit int) ([]domain.Attachment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+attCols+` FROM attachments
		WHERE status = 'pending' AND created_at < now() - make_interval(secs => $1)
		ORDER BY created_at LIMIT $2`, ttlSeconds, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Attachment{}
	for rows.Next() {
		var a domain.Attachment
		if err := scanAttachment(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) DeleteAttachment(ctx context.Context, id string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO personal_file_sync_outbox(idempotency_key,source_file_id,event_type,payload)
		SELECT 'delete:'||a.id::text||':'||extract(epoch from now())::bigint,a.id::text,'file.deleted',jsonb_build_object(
		'eventId','ludiskus-attachment-delete:'||a.id::text||':'||extract(epoch from now())::bigint,'type','file.deleted','occurredAt',now(),
		'sourceFileId',a.id::text,'sourceRevision',extract(epoch from now())::bigint::text,'ownerUserId',pc.user_id::text,
		'uploadedByProfileUuid',a.uploader_profile_uuid::text,'file',jsonb_build_object('name',a.file_name,'mimeType',a.content_type,'sizeBytes',a.size_bytes,'mediaKind','other','uploadedAt',a.created_at))
		FROM attachments a JOIN profile_cache pc ON pc.profile_uuid=a.uploader_profile_uuid WHERE a.id=$1 AND pc.user_id IS NOT NULL`, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM attachments WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- subscriptions ----------------------------------------------------------

func (r *Repo) UpsertSubscription(ctx context.Context, s domain.Subscription) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO subscriptions (profile_uuid, target_type, target_id, reason, muted)
		VALUES ($1,$2::sub_target,$3,$4,$5)
		ON CONFLICT (profile_uuid, target_type, target_id) DO UPDATE SET
			muted = EXCLUDED.muted, reason = EXCLUDED.reason`,
		s.ProfileUUID, s.TargetType, s.TargetID, s.Reason, s.Muted)
	return err
}

// EnsureSubscription tạo subscription mặc định (authored/participated) nếu chưa có.
func (r *Repo) EnsureSubscription(ctx context.Context, profileUUID, targetType, targetID, reason string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO subscriptions (profile_uuid, target_type, target_id, reason)
		VALUES ($1,$2::sub_target,$3,$4)
		ON CONFLICT (profile_uuid, target_type, target_id) DO NOTHING`,
		profileUUID, targetType, targetID, reason)
	return err
}

func (r *Repo) ListSubscriptions(ctx context.Context, profileUUID string) ([]domain.Subscription, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, profile_uuid, target_type, target_id, reason, muted, created_at
		FROM subscriptions WHERE profile_uuid = $1 ORDER BY created_at DESC`, profileUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Subscription{}
	for rows.Next() {
		var s domain.Subscription
		if err := rows.Scan(&s.ID, &s.ProfileUUID, &s.TargetType, &s.TargetID, &s.Reason, &s.Muted, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SubscribersForTopic trả profile_uuid theo dõi topic (qua topic/board/space),
// không muted, đã khử trùng lặp.
func (r *Repo) SubscribersForTopic(ctx context.Context, topicID, boardID, spaceUUID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT profile_uuid FROM subscriptions
		WHERE NOT muted AND (
			(target_type = 'topic' AND target_id = $1) OR
			(target_type = 'board' AND target_id = $2) OR
			(target_type = 'space' AND target_id = $3))`,
		topicID, boardID, spaceUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
