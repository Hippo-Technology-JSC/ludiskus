package repository

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
)

const pollCols = `id,anchor_target_id,space_uuid,visibility,author_kind,created_by,author_space_uuid,source_service,question,description_md,description_html,kind,config,identity_mode,results_visibility,who_can_vote,allow_change_vote,allow_retract,allow_user_options,shuffle_options,remind,min_voters_for_results,quorum_percent,status,status_before_hide,opens_at,closes_at,closed_at,closed_by,close_reason,reopen_count,reminded_at,eligible_count,voter_count,ballot_version,first_vote_at,edit_count,idempotency_key,created_at,updated_at,deleted_at`

func scanPoll(row pgx.Row, p *domain.Poll) error {
	return row.Scan(&p.ID, &p.AnchorTargetID, &p.SpaceUUID, &p.Visibility, &p.AuthorKind, &p.CreatedBy, &p.AuthorSpaceUUID, &p.SourceService, &p.Question, &p.DescriptionMD, &p.DescriptionHTML, &p.Kind, &p.Config, &p.IdentityMode, &p.ResultsVisibility, &p.WhoCanVote, &p.AllowChangeVote, &p.AllowRetract, &p.AllowUserOptions, &p.ShuffleOptions, &p.Remind, &p.MinVotersForResults, &p.QuorumPercent, &p.Status, &p.StatusBeforeHide, &p.OpensAt, &p.ClosesAt, &p.ClosedAt, &p.ClosedBy, &p.CloseReason, &p.ReopenCount, &p.RemindedAt, &p.EligibleCount, &p.VoterCount, &p.BallotVersion, &p.FirstVoteAt, &p.EditCount, &p.IdempotencyKey, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt)
}
func (r *Repo) BeginPoll(ctx context.Context) (pgx.Tx, error) { return r.pool.Begin(ctx) }
func (r *Repo) GetPoll(ctx context.Context, id string) (*domain.Poll, error) {
	if _, e := uuid.Parse(id); e != nil {
		return nil, domain.PollFailure("POLL_NOT_FOUND", 404, "")
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	p, e := r.PollTx(ctx, tx, id, false)
	if e != nil {
		return nil, e
	}
	p.Options, e = r.PollOptionsTx(ctx, tx, id)
	if e == nil && p.AnchorTargetID != nil {
		p.Target, e = r.GetCommentTargetByID(ctx, *p.AnchorTargetID)
	}
	return p, e
}

type pollQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (r *Repo) PollTx(ctx context.Context, q pollQuerier, id string, lock bool) (*domain.Poll, error) {
	sql := `SELECT ` + pollCols + ` FROM polls WHERE id=$1`
	if lock {
		sql += ` FOR UPDATE`
	}
	var p domain.Poll
	e := scanPoll(q.QueryRow(ctx, sql, id), &p)
	if isNotFound(e) {
		return nil, domain.PollFailure("POLL_NOT_FOUND", 404, "")
	}
	return &p, e
}
func (r *Repo) SavePollTx(ctx context.Context, tx pgx.Tx, p *domain.Poll, insert bool) error {
	sql := `UPDATE polls SET anchor_target_id=$2,space_uuid=$3,visibility=$4,author_kind=$5,created_by=$6,author_space_uuid=$7,source_service=$8,question=$9,description_md=$10,description_html=$11,kind=$12,config=$13,identity_mode=$14,results_visibility=$15,who_can_vote=$16,allow_change_vote=$17,allow_retract=$18,allow_user_options=$19,shuffle_options=$20,remind=$21,min_voters_for_results=$22,quorum_percent=$23,status=$24,status_before_hide=$25,opens_at=$26,closes_at=$27,closed_at=$28,closed_by=$29,close_reason=$30,reopen_count=$31,reminded_at=$32,eligible_count=$33,voter_count=$34,ballot_version=$35,first_vote_at=$36,edit_count=$37,idempotency_key=$38,deleted_at=$39 WHERE id=$1 RETURNING id,anchor_target_id,space_uuid,visibility,author_kind,created_by,author_space_uuid,source_service,question,description_md,description_html,kind,config,identity_mode,results_visibility,who_can_vote,allow_change_vote,allow_retract,allow_user_options,shuffle_options,remind,min_voters_for_results,quorum_percent,status,status_before_hide,opens_at,closes_at,closed_at,closed_by,close_reason,reopen_count,reminded_at,eligible_count,voter_count,ballot_version,first_vote_at,edit_count,idempotency_key,created_at,updated_at,deleted_at`
	if insert {
		sql = `INSERT INTO polls (id,anchor_target_id,space_uuid,visibility,author_kind,created_by,author_space_uuid,source_service,question,description_md,description_html,kind,config,identity_mode,results_visibility,who_can_vote,allow_change_vote,allow_retract,allow_user_options,shuffle_options,remind,min_voters_for_results,quorum_percent,status,status_before_hide,opens_at,closes_at,closed_at,closed_by,close_reason,reopen_count,reminded_at,eligible_count,voter_count,ballot_version,first_vote_at,edit_count,idempotency_key,deleted_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39) RETURNING id,anchor_target_id,space_uuid,visibility,author_kind,created_by,author_space_uuid,source_service,question,description_md,description_html,kind,config,identity_mode,results_visibility,who_can_vote,allow_change_vote,allow_retract,allow_user_options,shuffle_options,remind,min_voters_for_results,quorum_percent,status,status_before_hide,opens_at,closes_at,closed_at,closed_by,close_reason,reopen_count,reminded_at,eligible_count,voter_count,ballot_version,first_vote_at,edit_count,idempotency_key,created_at,updated_at,deleted_at`
	}
	e := scanPoll(tx.QueryRow(ctx, sql, p.ID, p.AnchorTargetID, p.SpaceUUID, p.Visibility, p.AuthorKind, p.CreatedBy, p.AuthorSpaceUUID, p.SourceService, p.Question, p.DescriptionMD, p.DescriptionHTML, p.Kind, p.Config, p.IdentityMode, p.ResultsVisibility, p.WhoCanVote, p.AllowChangeVote, p.AllowRetract, p.AllowUserOptions, p.ShuffleOptions, p.Remind, p.MinVotersForResults, p.QuorumPercent, p.Status, p.StatusBeforeHide, p.OpensAt, p.ClosesAt, p.ClosedAt, p.ClosedBy, p.CloseReason, p.ReopenCount, p.RemindedAt, p.EligibleCount, p.VoterCount, p.BallotVersion, p.FirstVoteAt, p.EditCount, p.IdempotencyKey, p.DeletedAt), p)
	if isUnique(e) {
		return domain.ErrConflict
	}
	return e
}
func (r *Repo) PollOptionsTx(ctx context.Context, q pollQuerier, id string) ([]domain.PollOption, error) {
	rows, e := q.Query(ctx, `SELECT id,poll_id,position,label,label_norm,value,starts_at,ends_at,added_by,status,vote_count,maybe_count FROM poll_options WHERE poll_id=$1 ORDER BY position,id`, id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.PollOption{}
	for rows.Next() {
		var o domain.PollOption
		if e = rows.Scan(&o.ID, &o.PollID, &o.Position, &o.Label, &o.LabelNorm, &o.Value, &o.StartsAt, &o.EndsAt, &o.AddedBy, &o.Status, &o.VoteCount, &o.MaybeCount); e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (r *Repo) InsertPollOptionTx(ctx context.Context, tx pgx.Tx, o domain.PollOption) error {
	_, e := tx.Exec(ctx, `INSERT INTO poll_options(id,poll_id,position,label,label_norm,value,starts_at,ends_at,added_by,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, o.ID, o.PollID, o.Position, o.Label, o.LabelNorm, o.Value, o.StartsAt, o.EndsAt, o.AddedBy, o.Status)
	if isUnique(e) {
		return domain.PollFailure("OPTION_DUPLICATE", 409, "label")
	}
	return e
}
func (r *Repo) PollIDs(ctx context.Context, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, e := r.pool.Query(ctx, `SELECT t.resource_id,p.id::text FROM comment_targets t JOIN polls p ON p.anchor_target_id=t.id WHERE t.service_code='ludiskus' AND t.resource_type='comment' AND t.resource_id=ANY($1) AND p.status <> 'deleted' ORDER BY p.created_at`, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, p string
		if e = rows.Scan(&id, &p); e != nil {
			return nil, e
		}
		out[id] = append(out[id], p)
	}
	return out, rows.Err()
}
func (r *Repo) PollByKeyTx(ctx context.Context, tx pgx.Tx, key string) (*domain.Poll, error) {
	var p domain.Poll
	e := scanPoll(tx.QueryRow(ctx, `SELECT `+pollCols+` FROM polls WHERE idempotency_key=$1`, key), &p)
	return &p, e
}
func (r *Repo) PollInvited(ctx context.Context, id, profile string) (bool, error) {
	var yes bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM poll_invitees WHERE poll_id=$1 AND profile_uuid=$2)`, id, profile).Scan(&yes)
	return yes, e
}
func (r *Repo) PollListIDs(ctx context.Context, anchor, profile, role, space, cursor string, limit int) ([]string, error) {
	rows, e := r.pool.Query(ctx, `SELECT p.id::text FROM polls p WHERE
 ($1='' OR p.anchor_target_id::text=$1) AND ($2='' OR
 ($3='created' AND p.created_by::text=$2) OR ($3='voted' AND EXISTS(SELECT 1 FROM poll_voters v WHERE v.poll_id=p.id AND v.profile_uuid::text=$2)) OR ($3='invited' AND EXISTS(SELECT 1 FROM poll_invitees i WHERE i.poll_id=p.id AND i.profile_uuid::text=$2)))
 AND ($4='' OR (p.anchor_target_id IS NULL AND p.space_uuid::text=$4 AND p.status IN ('published','closed')))
 AND ($5='' OR (p.created_at,p.id)<(SELECT created_at,id FROM polls WHERE id::text=$5))
 AND p.status<>'deleted' ORDER BY p.created_at DESC,p.id DESC LIMIT $6`, anchor, profile, role, space, cursor, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (r *Repo) PollAuditTx(ctx context.Context, tx pgx.Tx, id, actor, profile, action string, detail any) error {
	b, e := json.Marshal(detail)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO poll_audit_logs(poll_id,actor,actor_profile_uuid,action,detail) VALUES($1,$2,$3,$4,$5)`, id, actor, nullUUID(profile), action, b)
	return e
}
