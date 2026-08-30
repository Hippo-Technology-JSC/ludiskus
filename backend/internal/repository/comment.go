package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"ludiskus/internal/domain"
)

const commentCols = `id,target_id,parent_id,root_id,depth,reply_to_profile_uuid,
	author_kind,author_profile_uuid,author_space_uuid,source_service,body_md,body_html,
	body_hash,markdown_mode,status,moderation_source,is_pinned,pinned_by,pinned_at,
	reply_count,anchor,idempotency_key,edited_at,edit_count,deleted_at,deleted_by,
	delete_reason,score_cache,created_at,updated_at`

func scanComment(row pgx.Row, c *domain.Comment) error {
	return row.Scan(&c.ID, &c.TargetID, &c.ParentID, &c.RootID, &c.Depth, &c.ReplyToProfileUUID,
		&c.AuthorKind, &c.AuthorProfileUUID, &c.AuthorSpaceUUID, &c.SourceService, &c.BodyMD, &c.BodyHTML,
		&c.BodyHash, &c.MarkdownMode, &c.Status, &c.ModerationSource, &c.IsPinned, &c.PinnedBy, &c.PinnedAt,
		&c.ReplyCount, &c.Anchor, &c.IdempotencyKey, &c.EditedAt, &c.EditCount, &c.DeletedAt, &c.DeletedBy,
		&c.DeleteReason, &c.ScoreCache, &c.CreatedAt, &c.UpdatedAt)
}

type InsertCommentInput struct {
	Comment             domain.Comment
	MentionProfileUUIDs []string
	AttachmentIDs       []string
	SpaceUUID           *string
	ModerationSource    string
	Notifications       []CommentNotifyInsert
}

type CommentNotifyInsert struct {
	EventType            string
	RecipientProfileUUID string
	ActorProfileUUID     *string
	FlushAfter           time.Time
}

func (r *Repo) InsertComment(ctx context.Context, in InsertCommentInput) (*domain.Comment, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	if in.Comment.IdempotencyKey != nil {
		var old domain.Comment
		if err := scanComment(tx.QueryRow(ctx, `SELECT `+commentCols+` FROM comments WHERE idempotency_key=$1`, *in.Comment.IdempotencyKey), &old); err == nil {
			return &old, false, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, err
		}
	}
	if in.Comment.ID == "" {
		if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&in.Comment.ID); err != nil {
			return nil, false, err
		}
	}
	if in.Comment.ParentID == nil {
		in.Comment.RootID = in.Comment.ID
		in.Comment.Depth = 0
	}
	if len(in.Comment.Anchor) == 0 {
		in.Comment.Anchor = json.RawMessage(`{}`)
	}
	var out domain.Comment
	err = scanComment(tx.QueryRow(ctx, `INSERT INTO comments
		(id,target_id,parent_id,root_id,depth,reply_to_profile_uuid,author_kind,author_profile_uuid,
		author_space_uuid,source_service,body_md,body_html,body_hash,markdown_mode,status,
		moderation_source,anchor,idempotency_key)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+commentCols, in.Comment.ID, in.Comment.TargetID, in.Comment.ParentID, in.Comment.RootID,
		in.Comment.Depth, in.Comment.ReplyToProfileUUID, in.Comment.AuthorKind, in.Comment.AuthorProfileUUID,
		in.Comment.AuthorSpaceUUID, in.Comment.SourceService, in.Comment.BodyMD, in.Comment.BodyHTML,
		in.Comment.BodyHash, in.Comment.MarkdownMode, in.Comment.Status, in.Comment.ModerationSource,
		in.Comment.Anchor, in.Comment.IdempotencyKey), &out)
	if isUnique(err) && in.Comment.IdempotencyKey != nil {
		if e := scanComment(tx.QueryRow(ctx, `SELECT `+commentCols+` FROM comments WHERE idempotency_key=$1`, *in.Comment.IdempotencyKey), &out); e == nil {
			return &out, false, nil
		}
	}
	if err != nil {
		return nil, false, err
	}
	cDelta, rDelta, pDelta := domain.CountDelta("", out.Status, out.ParentID == nil)
	_, err = tx.Exec(ctx, `UPDATE comment_targets SET comment_count=comment_count+$2,
		reply_count=reply_count+$3,pending_count=pending_count+$4,
		last_comment_at=CASE WHEN $2+$3>0 THEN $5 ELSE last_comment_at END,
		last_comment_id=CASE WHEN $2+$3>0 THEN $1 ELSE last_comment_id END WHERE id=$6`,
		out.ID, cDelta, rDelta, pDelta, out.CreatedAt, out.TargetID)
	if err != nil {
		return nil, false, err
	}
	if out.Status == domain.CommentPublished && out.ParentID != nil {
		ids := []string{*out.ParentID}
		if out.RootID != *out.ParentID {
			ids = append(ids, out.RootID)
		}
		if _, err = tx.Exec(ctx, `UPDATE comments SET reply_count=reply_count+1 WHERE id=ANY($1::uuid[])`, ids); err != nil {
			return nil, false, err
		}
	}
	if out.AuthorProfileUUID != nil {
		var inserted bool
		err = tx.QueryRow(ctx, `WITH ins AS (INSERT INTO comment_participants(target_id,profile_uuid,reason)
			VALUES($1,$2,'authored') ON CONFLICT(target_id,profile_uuid) DO UPDATE SET
			reason=CASE WHEN comment_participants.reason='manual' THEN 'authored' ELSE comment_participants.reason END
			RETURNING (xmax=0)) SELECT * FROM ins`, out.TargetID, *out.AuthorProfileUUID).Scan(&inserted)
		if err != nil {
			return nil, false, err
		}
		if inserted {
			if _, err = tx.Exec(ctx, `UPDATE comment_targets SET participant_count=participant_count+1 WHERE id=$1`, out.TargetID); err != nil {
				return nil, false, err
			}
		}
	}
	for _, u := range in.MentionProfileUUIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO comment_mentions(comment_id,profile_uuid) VALUES($1,$2) ON CONFLICT DO NOTHING`, out.ID, u); err != nil {
			return nil, false, err
		}
	}
	if len(in.AttachmentIDs) > 0 {
		tag, e := tx.Exec(ctx, `UPDATE attachments SET comment_id=$2,status='attached'
			WHERE id=ANY($1::uuid[]) AND status='pending' AND space_uuid IS NOT DISTINCT FROM $3`, in.AttachmentIDs, out.ID, in.SpaceUUID)
		if e != nil {
			return nil, false, e
		}
		if tag.RowsAffected() != int64(len(in.AttachmentIDs)) {
			return nil, false, fmt.Errorf("%w: đính kèm không hợp lệ", domain.ErrValidation)
		}
		if err = enqueueAttachedPersonalFiles(ctx, tx, in.AttachmentIDs); err != nil {
			return nil, false, err
		}
	}
	if out.Status == domain.CommentPending || in.ModerationSource != "" {
		source := in.ModerationSource
		if source == "" {
			source = "pre"
		}
		if _, err = tx.Exec(ctx, `INSERT INTO moderation_items(space_uuid,target_type,target_id,source)
			VALUES($1,'comment',$2,$3)`, in.SpaceUUID, out.ID, source); err != nil {
			return nil, false, err
		}
	}
	for _, notification := range in.Notifications {
		if _, err = tx.Exec(ctx, `INSERT INTO comment_notify_buffer(event_type,recipient_profile_uuid,target_id,comment_id,actor_profile_uuid,flush_after)
			VALUES($1,$2,$3,$4,$5,COALESCE((SELECT min(flush_after) FROM comment_notify_buffer WHERE event_type=$1 AND recipient_profile_uuid=$2 AND target_id=$3),$6))
			ON CONFLICT DO NOTHING`, notification.EventType, notification.RecipientProfileUUID, out.TargetID, out.ID, notification.ActorProfileUUID, notification.FlushAfter); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

func (r *Repo) GetComment(ctx context.Context, id string) (*domain.Comment, error) {
	var c domain.Comment
	err := scanComment(r.pool.QueryRow(ctx, `SELECT `+commentCols+` FROM comments WHERE id=$1`, id), &c)
	if isNotFound(err) {
		return nil, domain.ErrNotFound
	}
	return &c, err
}

func (r *Repo) ListRootComments(ctx context.Context, targetID, viewer, sort string, cursorPinned *bool, cursorScore *int64, cursorAt *time.Time, cursorID string, limit int) ([]domain.Comment, error) {
	order := "created_at DESC,id DESC"
	if sort == "oldest" {
		order = "created_at ASC,id ASC"
	} else if sort == "top" {
		order = "score_cache DESC,created_at DESC,id DESC"
	}
	q := `SELECT ` + commentCols + ` FROM comments WHERE target_id=$1 AND parent_id IS NULL
		AND (status='published' OR (status='deleted' AND reply_count>0) OR (status='pending' AND author_profile_uuid=NULLIF($2,'')::uuid))`
	args := []any{targetID, viewer}
	if cursorAt != nil {
		args = append(args, *cursorPinned, *cursorAt, cursorID)
		p, at, id := len(args)-2, len(args)-1, len(args)
		if sort == "oldest" {
			q += fmt.Sprintf(` AND (is_pinned<$%d OR (is_pinned=$%d AND (created_at,id)>($%d,$%d)))`, p, p, at, id)
		} else if sort == "top" {
			args = append(args, *cursorScore)
			score := len(args)
			q += fmt.Sprintf(` AND (is_pinned<$%d OR (is_pinned=$%d AND (score_cache,created_at,id)<($%d,$%d,$%d)))`, p, p, score, at, id)
		} else {
			q += fmt.Sprintf(` AND (is_pinned<$%d OR (is_pinned=$%d AND (created_at,id)<($%d,$%d)))`, p, p, at, id)
		}
	}
	q += ` ORDER BY is_pinned DESC,` + order + fmt.Sprintf(` LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Comment{}
	for rows.Next() {
		var c domain.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) PreviewCommentReplies(ctx context.Context, roots []string, viewer string, limit int) (map[string][]domain.Comment, error) {
	out := map[string][]domain.Comment{}
	if len(roots) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+prefixCols("c", commentCols)+` FROM unnest($1::uuid[]) k(root_id)
		CROSS JOIN LATERAL (SELECT * FROM comments c WHERE c.root_id=k.root_id AND c.parent_id IS NOT NULL
		AND (c.status='published' OR (c.status='deleted' AND c.reply_count>0) OR (c.status='pending' AND c.author_profile_uuid=NULLIF($2,'')::uuid)) ORDER BY c.created_at,c.id LIMIT $3) c`, roots, viewer, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c domain.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		out[c.RootID] = append(out[c.RootID], c)
	}
	return out, rows.Err()
}

func (r *Repo) ListCommentReplies(ctx context.Context, rootID, viewer string, cursorAt *time.Time, cursorID string, limit int) ([]domain.Comment, error) {
	q := `SELECT ` + commentCols + ` FROM comments WHERE root_id=$1 AND parent_id IS NOT NULL
		AND (status='published' OR (status='deleted' AND reply_count>0) OR (status='pending' AND author_profile_uuid=NULLIF($2,'')::uuid))`
	args := []any{rootID, viewer}
	if cursorAt != nil {
		q += ` AND (created_at,id)>($3,$4)`
		args = append(args, *cursorAt, cursorID)
	}
	q += fmt.Sprintf(` ORDER BY created_at,id LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Comment{}
	for rows.Next() {
		var c domain.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) SearchComments(ctx context.Context, targetID, q string, limit int) ([]domain.Comment, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+commentCols+` FROM comments WHERE target_id=$1 AND status='published'
		AND (search_tsv @@ plainto_tsquery('simple',unaccent($2)) OR body_md % $2)
		ORDER BY ts_rank(search_tsv,plainto_tsquery('simple',unaccent($2))) DESC,created_at DESC LIMIT $3`, targetID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Comment{}
	for rows.Next() {
		var c domain.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) ExistsPublishedCommentByAuthor(ctx context.Context, targetID, profileUUID string) (bool, error) {
	var v bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM comments WHERE target_id=$1 AND author_profile_uuid=$2 AND status='published')`, targetID, profileUUID).Scan(&v)
	return v, err
}

func (r *Repo) UpdateCommentBody(ctx context.Context, c *domain.Comment, bodyMD, bodyHTML, bodyHash, mode, editor string) (*domain.Comment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO comment_revisions(comment_id,revision,body_md,edited_by)
		VALUES($1,$2,$3,$4)`, c.ID, c.EditCount+1, c.BodyMD, editor); err != nil {
		return nil, err
	}
	var out domain.Comment
	err = scanComment(tx.QueryRow(ctx, `UPDATE comments SET body_md=$2,body_html=$3,body_hash=$4,
		markdown_mode=$5,edited_at=now(),edit_count=edit_count+1 WHERE id=$1 RETURNING `+commentCols, c.ID, bodyMD, bodyHTML, bodyHash, mode), &out)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *Repo) CommentRevisions(ctx context.Context, id string, limit int) ([]domain.CommentRevision, error) {
	rows, err := r.pool.Query(ctx, `SELECT comment_id,revision,body_md,edited_by,created_at FROM comment_revisions WHERE comment_id=$1 ORDER BY revision DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CommentRevision{}
	for rows.Next() {
		var v domain.CommentRevision
		if err := rows.Scan(&v.CommentID, &v.Revision, &v.BodyMD, &v.EditedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) TransitionComment(ctx context.Context, id, newStatus, actor, reason string) (*domain.Comment, error) {
	return r.transitionComment(ctx, id, newStatus, actor, reason, nil, "")
}

func (r *Repo) TransitionCommentWithNotify(ctx context.Context, id, newStatus, actor, reason string, notifications []CommentNotifyInsert) (*domain.Comment, error) {
	return r.transitionComment(ctx, id, newStatus, actor, reason, notifications, "")
}

func (r *Repo) TransitionCommentByServiceWithNotify(ctx context.Context, id, newStatus, serviceCode, actorProfile, reason string, notifications []CommentNotifyInsert) (*domain.Comment, error) {
	return r.transitionComment(ctx, id, newStatus, actorProfile, reason, notifications, "service:"+serviceCode)
}

func (r *Repo) transitionComment(ctx context.Context, id, newStatus, actor, reason string, notifications []CommentNotifyInsert, auditActor string) (*domain.Comment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var old domain.Comment
	if err = scanComment(tx.QueryRow(ctx, `SELECT `+commentCols+` FROM comments WHERE id=$1 FOR UPDATE`, id), &old); errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if old.Status == domain.CommentDeleted {
		return nil, domain.ErrConflict
	}
	cD, rD, pD := domain.CountDelta(old.Status, newStatus, old.ParentID == nil)
	var out domain.Comment
	err = scanComment(tx.QueryRow(ctx, `UPDATE comments SET status=$2,
		deleted_at=CASE WHEN $2='deleted' THEN now() ELSE deleted_at END,
		deleted_by=CASE WHEN $2='deleted' THEN $3::uuid ELSE deleted_by END,delete_reason=$4,
		moderation_source=CASE WHEN $2 IN ('hidden','rejected') THEN
			CASE WHEN $4='auto_hide' THEN 'auto_hide' ELSE COALESCE(moderation_source,'service') END
			ELSE moderation_source END
		WHERE id=$1 RETURNING `+commentCols, id, newStatus, nullUUID(actor), reason), &out)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE comment_targets SET comment_count=comment_count+$2,reply_count=reply_count+$3,
		pending_count=pending_count+$4 WHERE id=$1`, old.TargetID, cD, rD, pD)
	if err != nil {
		return nil, err
	}
	if old.ParentID != nil && (old.Status == domain.CommentPublished) != (newStatus == domain.CommentPublished) {
		delta := -1
		if newStatus == domain.CommentPublished {
			delta = 1
		}
		ids := []string{*old.ParentID}
		if old.RootID != *old.ParentID {
			ids = append(ids, old.RootID)
		}
		if _, err = tx.Exec(ctx, `UPDATE comments SET reply_count=reply_count+$2 WHERE id=ANY($1::uuid[])`, ids, delta); err != nil {
			return nil, err
		}
	}
	if auditActor == "" {
		auditActor = actorLabel(actor)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO comment_audit_logs(actor,actor_profile_uuid,action,target_id,comment_id,reason)
		VALUES($1,$2,$3,$4,$5,$6)`, auditActor, nullUUID(actor), newStatus, old.TargetID, id, reason); err != nil {
		return nil, err
	}
	if old.Status != domain.CommentPublished && newStatus == domain.CommentPublished {
		for _, notification := range notifications {
			if _, err = tx.Exec(ctx, `INSERT INTO comment_notify_buffer(event_type,recipient_profile_uuid,target_id,comment_id,actor_profile_uuid,flush_after)
				VALUES($1,$2,$3,$4,$5,COALESCE((SELECT min(flush_after) FROM comment_notify_buffer WHERE event_type=$1 AND recipient_profile_uuid=$2 AND target_id=$3),$6))
				ON CONFLICT DO NOTHING`, notification.EventType, notification.RecipientProfileUUID, out.TargetID, out.ID, notification.ActorProfileUUID, notification.FlushAfter); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

func nullUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func actorLabel(v string) string {
	if v == "" {
		return "system"
	}
	return "profile:" + v
}

func (r *Repo) SetCommentPinned(ctx context.Context, id string, pinned bool, actor string, maxPinned int) (*domain.Comment, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var c domain.Comment
	if err = scanComment(tx.QueryRow(ctx, `SELECT `+commentCols+` FROM comments WHERE id=$1 FOR UPDATE`, id), &c); err != nil {
		return nil, err
	}
	if pinned {
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM comments WHERE target_id=$1 AND is_pinned AND id<>$2`, c.TargetID, id).Scan(&n); err != nil {
			return nil, err
		}
		if n >= maxPinned {
			return nil, fmt.Errorf("%w: đã đạt số bình luận ghim tối đa", domain.ErrValidation)
		}
	}
	err = scanComment(tx.QueryRow(ctx, `UPDATE comments SET is_pinned=$2,pinned_by=CASE WHEN $2 THEN $3::uuid ELSE NULL END,pinned_at=CASE WHEN $2 THEN now() ELSE NULL END WHERE id=$1 RETURNING `+commentCols, id, pinned, actor), &c)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Repo) CommentMentions(ctx context.Context, id string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT profile_uuid FROM comment_mentions WHERE comment_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Repo) MineComments(ctx context.Context, profileUUID, status, service, q string, limit int) ([]domain.Comment, error) {
	query := `SELECT ` + prefixCols("c", commentCols) + ` FROM comments c JOIN comment_targets t ON t.id=c.target_id WHERE c.author_profile_uuid=$1`
	args := []any{profileUUID}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND c.status=$%d`, len(args))
	}
	if service != "" {
		args = append(args, service)
		query += fmt.Sprintf(` AND t.service_code=$%d`, len(args))
	}
	if q != "" {
		args = append(args, q)
		query += fmt.Sprintf(` AND c.search_tsv@@plainto_tsquery('simple',unaccent($%d))`, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY c.created_at DESC LIMIT $%d`, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Comment{}
	for rows.Next() {
		var c domain.Comment
		if err := scanComment(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func prefixCols(prefix, cols string) string {
	out := ""
	for i, v := range splitComma(cols) {
		if i > 0 {
			out += ","
		}
		out += prefix + "." + v
	}
	return out
}
func splitComma(v string) []string {
	var out []string
	start := 0
	for i, c := range v {
		if c == ',' {
			out = append(out, trim(v[start:i]))
			start = i + 1
		}
	}
	out = append(out, trim(v[start:]))
	return out
}
func trim(v string) string {
	for len(v) > 0 && (v[0] == ' ' || v[0] == '\n' || v[0] == '\t') {
		v = v[1:]
	}
	for len(v) > 0 && (v[len(v)-1] == ' ' || v[len(v)-1] == '\n' || v[len(v)-1] == '\t') {
		v = v[:len(v)-1]
	}
	return v
}
