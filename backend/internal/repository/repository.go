// Package repository truy cập PostgreSQL bằng pgx (docs/09).
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"ludiskus/internal/domain"
)

type Repo struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func nullJSON(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func jsonOrEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

// --- profile_cache ----------------------------------------------------------

func (r *Repo) GetCachedProfile(ctx context.Context, uuid string) (*domain.CachedProfile, error) {
	var p domain.CachedProfile
	err := r.pool.QueryRow(ctx, `SELECT profile_uuid, user_id, code, name, avatar, is_active, synced_at
		FROM profile_cache WHERE profile_uuid = $1`, uuid).
		Scan(&p.ProfileUUID, &p.UserID, &p.Code, &p.Name, &p.Avatar, &p.IsActive, &p.SyncedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *Repo) GetCachedProfileByCode(ctx context.Context, code string) (*domain.CachedProfile, error) {
	var p domain.CachedProfile
	err := r.pool.QueryRow(ctx, `SELECT profile_uuid, user_id, code, name, avatar, is_active, synced_at
		FROM profile_cache WHERE lower(code) = lower($1) LIMIT 1`, code).
		Scan(&p.ProfileUUID, &p.UserID, &p.Code, &p.Name, &p.Avatar, &p.IsActive, &p.SyncedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &p, err
}

func (r *Repo) UpsertCachedProfile(ctx context.Context, p domain.CachedProfile) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO profile_cache (profile_uuid, user_id, code, name, avatar, is_active, synced_at)
		VALUES ($1,$2,$3,$4,$5,$6, now())
		ON CONFLICT (profile_uuid) DO UPDATE SET
			user_id = EXCLUDED.user_id, code = EXCLUDED.code, name = EXCLUDED.name,
			avatar = EXCLUDED.avatar, is_active = EXCLUDED.is_active, synced_at = now()`,
		p.ProfileUUID, p.UserID, p.Code, p.Name, p.Avatar, p.IsActive)
	return err
}

// --- space_cache ------------------------------------------------------------

func (r *Repo) GetCachedSpace(ctx context.Context, uuid string) (*domain.CachedSpace, error) {
	var s domain.CachedSpace
	err := r.pool.QueryRow(ctx, `SELECT space_uuid, code, name, is_public, is_active,
		creator_profile_uuid, space_type, synced_at FROM space_cache WHERE space_uuid = $1`, uuid).
		Scan(&s.SpaceUUID, &s.Code, &s.Name, &s.IsPublic, &s.IsActive,
			&s.CreatorProfileUUID, &s.SpaceType, &s.SyncedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &s, err
}

func (r *Repo) UpsertCachedSpace(ctx context.Context, s domain.CachedSpace) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO space_cache (space_uuid, code, name, is_public, is_active, creator_profile_uuid, space_type, synced_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now())
		ON CONFLICT (space_uuid) DO UPDATE SET
			code = EXCLUDED.code, name = EXCLUDED.name, is_public = EXCLUDED.is_public,
			is_active = EXCLUDED.is_active, creator_profile_uuid = EXCLUDED.creator_profile_uuid,
			space_type = EXCLUDED.space_type, synced_at = now()`,
		s.SpaceUUID, s.Code, s.Name, s.IsPublic, s.IsActive, s.CreatorProfileUUID, s.SpaceType)
	return err
}

// --- space_member_cache -----------------------------------------------------

func (r *Repo) ListMembers(ctx context.Context, spaceUUID string) ([]domain.CachedMember, error) {
	rows, err := r.pool.Query(ctx, `SELECT space_uuid, profile_uuid, role, joined_at, synced_at
		FROM space_member_cache WHERE space_uuid = $1`, spaceUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.CachedMember{}
	for rows.Next() {
		var m domain.CachedMember
		if err := rows.Scan(&m.SpaceUUID, &m.ProfileUUID, &m.Role, &m.JoinedAt, &m.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceMembers thay toàn bộ thành viên cache của một Space (full member sync).
func (r *Repo) ReplaceMembers(ctx context.Context, spaceUUID string, members []domain.CachedMember) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM space_member_cache WHERE space_uuid = $1`, spaceUUID); err != nil {
		return err
	}
	for _, m := range members {
		if _, err := tx.Exec(ctx, `
			INSERT INTO space_member_cache (space_uuid, profile_uuid, role, joined_at, synced_at)
			VALUES ($1,$2,$3,$4, now())`,
			spaceUUID, m.ProfileUUID, m.Role, m.JoinedAt); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SpacesForProfile trả uuid các Space người dùng là thành viên (cho phạm vi tìm
// kiếm) — gộp với Space công khai ở tầng service.
func (r *Repo) SpacesForProfile(ctx context.Context, profileUUID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT space_uuid FROM space_member_cache WHERE profile_uuid = $1`, profileUUID)
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

// PublicSpaces trả uuid các Space công khai đã bật forum (cho phạm vi tìm kiếm).
func (r *Repo) PublicSpaces(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT space_uuid FROM space_forums WHERE enabled AND is_public`)
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

// --- space_forums -----------------------------------------------------------

const forumCols = `space_uuid, enabled, is_public, post_policy, moderation_mode, banned_words,
	report_auto_hide_threshold, default_topic_type, settings, created_at, updated_at`

func scanForum(row pgx.Row, f *domain.SpaceForum) error {
	return row.Scan(&f.SpaceUUID, &f.Enabled, &f.IsPublic, &f.PostPolicy, &f.ModerationMode,
		&f.BannedWords, &f.ReportAutoHideThreshold, &f.DefaultTopicType, &f.Settings,
		&f.CreatedAt, &f.UpdatedAt)
}

func (r *Repo) GetForum(ctx context.Context, spaceUUID string) (*domain.SpaceForum, error) {
	var f domain.SpaceForum
	err := scanForum(r.pool.QueryRow(ctx, `SELECT `+forumCols+` FROM space_forums WHERE space_uuid = $1`, spaceUUID), &f)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &f, err
}

func (r *Repo) UpsertForum(ctx context.Context, f domain.SpaceForum) (*domain.SpaceForum, error) {
	var out domain.SpaceForum
	err := scanForum(r.pool.QueryRow(ctx, `
		INSERT INTO space_forums (space_uuid, enabled, is_public, post_policy, moderation_mode,
			banned_words, report_auto_hide_threshold, default_topic_type, settings)
		VALUES ($1,$2,$3,$4::post_policy,$5::moderation_mode,$6,$7,$8::topic_type,$9)
		ON CONFLICT (space_uuid) DO UPDATE SET
			enabled = EXCLUDED.enabled, is_public = EXCLUDED.is_public,
			post_policy = EXCLUDED.post_policy, moderation_mode = EXCLUDED.moderation_mode,
			banned_words = EXCLUDED.banned_words,
			report_auto_hide_threshold = EXCLUDED.report_auto_hide_threshold,
			default_topic_type = EXCLUDED.default_topic_type, settings = EXCLUDED.settings
		RETURNING `+forumCols,
		f.SpaceUUID, f.Enabled, f.IsPublic, f.PostPolicy, f.ModerationMode, f.BannedWords,
		f.ReportAutoHideThreshold, f.DefaultTopicType, jsonOrEmpty(f.Settings)), &out)
	return &out, err
}

// --- space_moderators -------------------------------------------------------

func (r *Repo) IsModerator(ctx context.Context, spaceUUID, profileUUID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM space_moderators
		WHERE space_uuid = $1 AND profile_uuid = $2)`, spaceUUID, profileUUID).Scan(&exists)
	return exists, err
}

func (r *Repo) ListModerators(ctx context.Context, spaceUUID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT profile_uuid FROM space_moderators WHERE space_uuid = $1`, spaceUUID)
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

func (r *Repo) AddModerator(ctx context.Context, spaceUUID, profileUUID, grantedBy string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO space_moderators (space_uuid, profile_uuid, granted_by)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, spaceUUID, profileUUID, grantedBy)
	return err
}

func (r *Repo) RemoveModerator(ctx context.Context, spaceUUID, profileUUID string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM space_moderators WHERE space_uuid = $1 AND profile_uuid = $2`,
		spaceUUID, profileUUID)
	return err
}

// --- boards -----------------------------------------------------------------

const boardCols = `id, space_uuid, parent_id, code, name, description_md, description_html, kind,
	position, is_locked, min_role, topic_count, post_count, last_activity_at, created_at, updated_at`

func scanBoard(row pgx.Row, b *domain.Board) error {
	return row.Scan(&b.ID, &b.SpaceUUID, &b.ParentID, &b.Code, &b.Name, &b.DescriptionMD,
		&b.DescriptionHTML, &b.Kind, &b.Position, &b.IsLocked, &b.MinRole, &b.TopicCount,
		&b.PostCount, &b.LastActivityAt, &b.CreatedAt, &b.UpdatedAt)
}

func (r *Repo) ListBoards(ctx context.Context, spaceUUID string) ([]domain.Board, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+boardCols+` FROM boards
		WHERE space_uuid = $1 ORDER BY position, name`, spaceUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Board{}
	for rows.Next() {
		var b domain.Board
		if err := scanBoard(rows, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Repo) GetBoard(ctx context.Context, id string) (*domain.Board, error) {
	var b domain.Board
	err := scanBoard(r.pool.QueryRow(ctx, `SELECT `+boardCols+` FROM boards WHERE id = $1`, id), &b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &b, err
}

func (r *Repo) CreateBoard(ctx context.Context, b domain.Board) (*domain.Board, error) {
	var out domain.Board
	err := scanBoard(r.pool.QueryRow(ctx, `
		INSERT INTO boards (space_uuid, parent_id, code, name, description_md, description_html,
			kind, position, is_locked, min_role)
		VALUES ($1,$2,$3,$4,$5,$6,$7::board_kind,$8,$9,$10)
		RETURNING `+boardCols,
		b.SpaceUUID, b.ParentID, b.Code, b.Name, b.DescriptionMD, b.DescriptionHTML,
		b.Kind, b.Position, b.IsLocked, b.MinRole), &out)
	if isUnique(err) {
		return nil, domain.ErrConflict
	}
	return &out, err
}

// SeedBoard tạo board mặc định idempotent theo (space_uuid, code).
func (r *Repo) SeedBoard(ctx context.Context, b domain.Board) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO boards (space_uuid, code, name, description_md, description_html, kind, position)
		VALUES ($1,$2,$3,$4,$5,$6::board_kind,$7)
		ON CONFLICT (space_uuid, code) DO NOTHING`,
		b.SpaceUUID, b.Code, b.Name, b.DescriptionMD, b.DescriptionHTML, b.Kind, b.Position)
	return err
}

func (r *Repo) UpdateBoard(ctx context.Context, id string, b domain.Board) (*domain.Board, error) {
	var out domain.Board
	err := scanBoard(r.pool.QueryRow(ctx, `
		UPDATE boards SET name = $2, description_md = $3, description_html = $4,
			position = $5, is_locked = $6, min_role = $7
		WHERE id = $1 RETURNING `+boardCols,
		id, b.Name, b.DescriptionMD, b.DescriptionHTML, b.Position, b.IsLocked, b.MinRole), &out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &out, err
}

func (r *Repo) DeleteBoard(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM boards WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repo) bumpBoardActivity(ctx context.Context, q pgxExec, boardID string, topics, posts int) error {
	_, err := q.Exec(ctx, `UPDATE boards SET topic_count = topic_count + $2,
		post_count = post_count + $3, last_activity_at = now() WHERE id = $1`, boardID, topics, posts)
	return err
}

// pgxExec gói cả pool và tx cho các hàm cập nhật đếm.
type pgxExec interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
