package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"ludiskus/internal/domain"
)

// --- reports ----------------------------------------------------------------

func (r *Repo) CreateReport(ctx context.Context, rep domain.Report) (*domain.Report, error) {
	var out domain.Report
	err := r.pool.QueryRow(ctx, `
		INSERT INTO reports (space_uuid, target_type, target_id, reporter_profile_uuid, reason, note)
		VALUES ($1,$2::report_target,$3,$4,$5,$6)
		RETURNING id, space_uuid, target_type, target_id, reporter_profile_uuid, reason, note, status, created_at`,
		rep.SpaceUUID, rep.TargetType, rep.TargetID, rep.ReporterProfileUUID, rep.Reason, rep.Note).
		Scan(&out.ID, &out.SpaceUUID, &out.TargetType, &out.TargetID, &out.ReporterProfileUUID,
			&out.Reason, &out.Note, &out.Status, &out.CreatedAt)
	return &out, err
}

func (r *Repo) CountOpenReports(ctx context.Context, targetType, targetID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM reports
		WHERE target_type = $1::report_target AND target_id = $2 AND status = 'open'`,
		targetType, targetID).Scan(&n)
	return n, err
}

func (r *Repo) CreateCommentReport(ctx context.Context, spaceUUID *string, commentID, reporter, reason string, note *string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `INSERT INTO reports(space_uuid,target_type,target_id,reporter_profile_uuid,reason,note)
		VALUES($1,'comment',$2,$3,$4,$5) ON CONFLICT DO NOTHING`, spaceUUID, commentID, reporter, reason, note)
	return err == nil && tag.RowsAffected() > 0, err
}

func (r *Repo) ListOpenReports(ctx context.Context, spaceUUID string, limit int) ([]domain.Report, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, space_uuid, target_type, target_id,
		reporter_profile_uuid, reason, note, status, created_at FROM reports
		WHERE space_uuid = $1 AND status = 'open' ORDER BY created_at DESC LIMIT $2`, spaceUUID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Report{}
	for rows.Next() {
		var rep domain.Report
		if err := rows.Scan(&rep.ID, &rep.SpaceUUID, &rep.TargetType, &rep.TargetID,
			&rep.ReporterProfileUUID, &rep.Reason, &rep.Note, &rep.Status, &rep.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

func (r *Repo) SetReportStatus(ctx context.Context, id, status string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE reports SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repo) ResolveReportsForTarget(ctx context.Context, targetType, targetID, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE reports SET status = $3
		WHERE target_type = $1::report_target AND target_id = $2 AND status = 'open'`,
		targetType, targetID, status)
	return err
}

// --- moderation_items -------------------------------------------------------

const modCols = `id, space_uuid, target_type, target_id, source, state, assignee_profile_uuid,
	decided_by, decided_at, note, created_at`

func scanModItem(row pgx.Row, m *domain.ModerationItem) error {
	return row.Scan(&m.ID, &m.SpaceUUID, &m.TargetType, &m.TargetID, &m.Source, &m.State,
		&m.AssigneeProfileUUID, &m.DecidedBy, &m.DecidedAt, &m.Note, &m.CreatedAt)
}

func (r *Repo) CreateModerationItem(ctx context.Context, m domain.ModerationItem) (*domain.ModerationItem, error) {
	var out domain.ModerationItem
	err := scanModItem(r.pool.QueryRow(ctx, `
		INSERT INTO moderation_items (space_uuid, target_type, target_id, source, state)
		VALUES ($1,$2::report_target,$3,$4,'pending')
		RETURNING `+modCols,
		m.SpaceUUID, m.TargetType, m.TargetID, m.Source), &out)
	return &out, err
}

func (r *Repo) CreateCommentModerationItem(ctx context.Context, spaceUUID *string, commentID, source string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx, `INSERT INTO moderation_items(space_uuid,target_type,target_id,source,state)
		VALUES($1,'comment',$2,$3,'pending') RETURNING id`, spaceUUID, commentID, source).Scan(&id)
	return id, err
}

func (r *Repo) GetModerationItem(ctx context.Context, id string) (*domain.ModerationItem, error) {
	var m domain.ModerationItem
	err := scanModItem(r.pool.QueryRow(ctx, `SELECT `+modCols+` FROM moderation_items WHERE id = $1`, id), &m)
	if isNotFound(err) {
		return nil, domain.ErrNotFound
	}
	return &m, err
}

func (r *Repo) ListModerationQueue(ctx context.Context, spaceUUID, state string, limit int) ([]domain.ModerationItem, error) {
	if state == "" {
		state = "pending"
	}
	rows, err := r.pool.Query(ctx, `SELECT `+modCols+` FROM moderation_items
		WHERE space_uuid = $1 AND state = $2::mod_state ORDER BY created_at LIMIT $3`,
		spaceUUID, state, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ModerationItem{}
	for rows.Next() {
		var m domain.ModerationItem
		if err := scanModItem(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) DecideModerationItem(ctx context.Context, id, state, decidedBy string, note *string) (*domain.ModerationItem, error) {
	var m domain.ModerationItem
	err := scanModItem(r.pool.QueryRow(ctx, `
		UPDATE moderation_items SET state = $2::mod_state, decided_by = $3, decided_at = now(), note = $4
		WHERE id = $1 AND state = 'pending' RETURNING `+modCols,
		id, state, decidedBy, note), &m)
	if isNotFound(err) {
		return nil, domain.ErrNotFound
	}
	return &m, err
}

// --- outbox -----------------------------------------------------------------

func (r *Repo) EnqueueOutbox(ctx context.Context, eventType string, idempotencyKey *string, payload []byte, maxAttempts int) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO outbox (event_type, idempotency_key, payload, max_attempts)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		eventType, idempotencyKey, jsonOrEmpty(payload), maxAttempts)
	return err
}

const outboxCols = `id, event_type, idempotency_key, payload, status, attempts, max_attempts,
	scheduled_at, last_error, sent_at, created_at`

// ClaimOutbox lấy một việc khỏi hàng đợi (SKIP LOCKED) và đánh dấu sending.
func (r *Repo) ClaimOutbox(ctx context.Context) (*domain.OutboxItem, error) {
	var o domain.OutboxItem
	err := r.pool.QueryRow(ctx, `
		UPDATE outbox SET status = 'sending', attempts = attempts + 1
		WHERE id = (
			SELECT id FROM outbox WHERE status = 'queued' AND scheduled_at <= now()
			ORDER BY scheduled_at FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING `+outboxCols).
		Scan(&o.ID, &o.EventType, &o.IdempotencyKey, &o.Payload, &o.Status, &o.Attempts,
			&o.MaxAttempts, &o.ScheduledAt, &o.LastError, &o.SentAt, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *Repo) MarkOutboxSent(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `UPDATE outbox SET status = 'sent', sent_at = now() WHERE id = $1`, id)
	return err
}

// MarkOutboxFailed đặt lại queued với backoff, hoặc failed nếu vượt trần.
func (r *Repo) MarkOutboxFailed(ctx context.Context, o *domain.OutboxItem, errMsg string) error {
	if o.Attempts >= o.MaxAttempts {
		_, err := r.pool.Exec(ctx, `UPDATE outbox SET status = 'failed', last_error = $2 WHERE id = $1`,
			o.ID, errMsg)
		return err
	}
	// backoff: 2^attempts phút (tối đa ~1h)
	backoff := 1 << min(o.Attempts, 6)
	_, err := r.pool.Exec(ctx, `UPDATE outbox SET status = 'queued', last_error = $2,
		scheduled_at = now() + make_interval(mins => $3) WHERE id = $1`, o.ID, errMsg, backoff)
	return err
}
