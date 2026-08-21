package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"ludiskus/internal/domain"
)

const commentTargetCols = `id, service_code, resource_type, resource_id, space_uuid,
	owner_type, owner_id, title, summary, thumbnail_url, canonical_path, visibility,
	state, thread_state, capabilities, comment_count, reply_count, participant_count,
	pending_count, last_comment_at, last_comment_id, verify_failures, verified_at,
	created_by, created_at, updated_at`

func scanCommentTarget(row pgx.Row, t *domain.CommentTarget) error {
	return row.Scan(&t.ID, &t.ServiceCode, &t.ResourceType, &t.ResourceID, &t.SpaceUUID,
		&t.OwnerType, &t.OwnerID, &t.Title, &t.Summary, &t.ThumbnailURL,
		&t.CanonicalPath, &t.Visibility, &t.State, &t.ThreadState, &t.Capabilities,
		&t.CommentCount, &t.ReplyCount, &t.ParticipantCount, &t.PendingCount,
		&t.LastCommentAt, &t.LastCommentID, &t.VerifyFailures, &t.VerifiedAt,
		&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
}

func scanCommentService(row pgx.Row, s *domain.CommentService) error {
	return row.Scan(&s.Code, &s.Name, &s.BaseURL, &s.OAuthClientID, &s.VerifyMode,
		&s.ContextPath, &s.IsActive, &s.CreatedAt, &s.UpdatedAt)
}

func (r *Repo) GetCommentService(ctx context.Context, code string) (*domain.CommentService, error) {
	var s domain.CommentService
	err := scanCommentService(r.pool.QueryRow(ctx, `SELECT code,name,base_url,oauth_client_id,
		verify_mode,context_path,is_active,created_at,updated_at FROM comment_services WHERE code=$1`, code), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

func (r *Repo) CommentServiceByClientID(ctx context.Context, id string) (*domain.CommentService, error) {
	var s domain.CommentService
	err := scanCommentService(r.pool.QueryRow(ctx, `SELECT code,name,base_url,oauth_client_id,
		verify_mode,context_path,is_active,created_at,updated_at FROM comment_services
		WHERE oauth_client_id=$1 AND is_active`, id), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

func (r *Repo) ListCommentServices(ctx context.Context) ([]domain.CommentService, error) {
	rows, err := r.pool.Query(ctx, `SELECT code,name,base_url,oauth_client_id,verify_mode,
		context_path,is_active,created_at,updated_at FROM comment_services ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CommentService{}
	for rows.Next() {
		var s domain.CommentService
		if err := scanCommentService(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertCommentService(ctx context.Context, s domain.CommentService) (*domain.CommentService, error) {
	var out domain.CommentService
	err := scanCommentService(r.pool.QueryRow(ctx, `INSERT INTO comment_services
		(code,name,base_url,oauth_client_id,verify_mode,context_path,is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (code) DO UPDATE SET
		name=EXCLUDED.name,base_url=EXCLUDED.base_url,oauth_client_id=EXCLUDED.oauth_client_id,
		verify_mode=EXCLUDED.verify_mode,context_path=EXCLUDED.context_path,is_active=EXCLUDED.is_active
		RETURNING code,name,base_url,oauth_client_id,verify_mode,context_path,is_active,created_at,updated_at`,
		s.Code, s.Name, s.BaseURL, s.OAuthClientID, s.VerifyMode, s.ContextPath, s.IsActive), &out)
	return &out, err
}

func (r *Repo) DisableCommentService(ctx context.Context, code string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE comment_services SET is_active=false WHERE code=$1`, code)
	if err == nil && tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return err
}

func (r *Repo) ApplyCommentServiceClients(ctx context.Context, raw string) error {
	for _, pair := range strings.Split(raw, ",") {
		p := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(p) != 2 || p[0] == "" || p[1] == "" {
			continue
		}
		if _, err := r.pool.Exec(ctx, `UPDATE comment_services SET oauth_client_id=$2 WHERE code=$1`, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) UpdateCommentContextPath(ctx context.Context, code, path string) error {
	_, err := r.pool.Exec(ctx, `UPDATE comment_services SET context_path=$2 WHERE code=$1`, code, path)
	return err
}

func (r *Repo) GetCommentTarget(ctx context.Context, ref domain.ResourceRef) (*domain.CommentTarget, error) {
	var t domain.CommentTarget
	err := scanCommentTarget(r.pool.QueryRow(ctx, `SELECT `+commentTargetCols+` FROM comment_targets
		WHERE service_code=$1 AND resource_type=$2 AND resource_id=$3`, ref.Service, ref.Type, ref.ID), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *Repo) GetCommentTargetByID(ctx context.Context, id string) (*domain.CommentTarget, error) {
	var t domain.CommentTarget
	err := scanCommentTarget(r.pool.QueryRow(ctx, `SELECT `+commentTargetCols+` FROM comment_targets WHERE id=$1`, id), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &t, err
}

func (r *Repo) UpsertCommentTarget(ctx context.Context, t domain.CommentTarget) (*domain.CommentTarget, error) {
	if len(t.Capabilities) == 0 {
		t.Capabilities = json.RawMessage(`{}`)
	}
	var out domain.CommentTarget
	err := scanCommentTarget(r.pool.QueryRow(ctx, `INSERT INTO comment_targets
		(service_code,resource_type,resource_id,space_uuid,owner_type,owner_id,title,summary,
		thumbnail_url,canonical_path,visibility,state,thread_state,capabilities,verified_at,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (service_code,resource_type,resource_id) DO UPDATE SET
		space_uuid=EXCLUDED.space_uuid,owner_type=EXCLUDED.owner_type,owner_id=EXCLUDED.owner_id,
		title=EXCLUDED.title,summary=EXCLUDED.summary,thumbnail_url=EXCLUDED.thumbnail_url,
		canonical_path=EXCLUDED.canonical_path,visibility=EXCLUDED.visibility,state=EXCLUDED.state,
		capabilities=EXCLUDED.capabilities,verified_at=EXCLUDED.verified_at,verify_failures=0
		RETURNING `+commentTargetCols, t.ServiceCode, t.ResourceType, t.ResourceID, t.SpaceUUID, t.OwnerType,
		t.OwnerID, t.Title, t.Summary, t.ThumbnailURL, t.CanonicalPath, t.Visibility, t.State,
		defaultString(t.ThreadState, "open"), t.Capabilities, t.VerifiedAt, t.CreatedBy), &out)
	return &out, err
}

func defaultString(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func (r *Repo) SetCommentTargetState(ctx context.Context, id, state string, failure bool) error {
	q := `UPDATE comment_targets SET state=$2, verified_at=CASE WHEN $3 THEN verified_at ELSE now() END,
		verify_failures=CASE WHEN $3 THEN verify_failures+1 ELSE 0 END WHERE id=$1`
	_, err := r.pool.Exec(ctx, q, id, state, failure)
	return err
}

func (r *Repo) SetCommentThreadState(ctx context.Context, id, state string) error {
	_, err := r.pool.Exec(ctx, `UPDATE comment_targets SET thread_state=$2 WHERE id=$1`, id, state)
	return err
}

func (r *Repo) ListStaleCommentTargets(ctx context.Context, before string, limit int) ([]domain.CommentTarget, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+commentTargetCols+` FROM comment_targets
		WHERE state IN ('unverified','active') AND (verified_at IS NULL OR verified_at < $1::timestamptz)
		ORDER BY (comment_count>0) DESC, verified_at NULLS FIRST LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CommentTarget{}
	for rows.Next() {
		var t domain.CommentTarget
		if err := scanCommentTarget(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertCommentParticipant(ctx context.Context, p domain.CommentParticipant) (bool, error) {
	var inserted bool
	err := r.pool.QueryRow(ctx, `WITH ins AS (INSERT INTO comment_participants
		(target_id,profile_uuid,reason,muted,last_read_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (target_id,profile_uuid) DO UPDATE SET
		reason=CASE WHEN comment_participants.reason='manual' THEN EXCLUDED.reason ELSE comment_participants.reason END,
		muted=EXCLUDED.muted,last_read_at=COALESCE(EXCLUDED.last_read_at,comment_participants.last_read_at)
		RETURNING (xmax=0) AS inserted)
		SELECT inserted FROM ins`, p.TargetID, p.ProfileUUID, p.Reason, p.Muted, p.LastReadAt).Scan(&inserted)
	if err == nil && inserted {
		_, err = r.pool.Exec(ctx, `UPDATE comment_targets SET participant_count=participant_count+1 WHERE id=$1`, p.TargetID)
	}
	return inserted, err
}

func (r *Repo) GetCommentParticipant(ctx context.Context, targetID, profileUUID string) (*domain.CommentParticipant, error) {
	var p domain.CommentParticipant
	err := r.pool.QueryRow(ctx, `SELECT target_id,profile_uuid,reason,muted,last_read_at,last_notified_at,created_at
		FROM comment_participants WHERE target_id=$1 AND profile_uuid=$2`, targetID, profileUUID).
		Scan(&p.TargetID, &p.ProfileUUID, &p.Reason, &p.Muted, &p.LastReadAt, &p.LastNotifiedAt, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *Repo) ListCommentParticipants(ctx context.Context, targetID string) ([]domain.CommentParticipant, error) {
	rows, err := r.pool.Query(ctx, `SELECT target_id,profile_uuid,reason,muted,last_read_at,last_notified_at,created_at
		FROM comment_participants WHERE target_id=$1`, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CommentParticipant{}
	for rows.Next() {
		var p domain.CommentParticipant
		if err := rows.Scan(&p.TargetID, &p.ProfileUUID, &p.Reason, &p.Muted, &p.LastReadAt, &p.LastNotifiedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProfileUUID < out[j].ProfileUUID })
	return out, rows.Err()
}

func (r *Repo) RemoveManualCommentParticipant(ctx context.Context, targetID, profileUUID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM comment_participants WHERE target_id=$1 AND profile_uuid=$2 AND reason='manual'`, targetID, profileUUID)
	if err == nil && tag.RowsAffected() > 0 {
		_, err = r.pool.Exec(ctx, `UPDATE comment_targets SET participant_count=participant_count-1 WHERE id=$1`, targetID)
	}
	return err
}

func (r *Repo) MarkCommentRead(ctx context.Context, targetID, profileUUID string) error {
	now := time.Now().UTC()
	_, err := r.UpsertCommentParticipant(ctx, domain.CommentParticipant{TargetID: targetID, ProfileUUID: profileUUID, Reason: "manual", LastReadAt: &now})
	return err
}
