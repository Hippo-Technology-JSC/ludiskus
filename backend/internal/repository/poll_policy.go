package repository

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
)

type PollPolicyRow struct {
	ID            string          `json:"id"`
	AnchorService string          `json:"anchorService"`
	AnchorType    string          `json:"anchorType"`
	Config        json.RawMessage `json:"config"`
	IsActive      bool            `json:"isActive"`
}

func (r *Repo) GetPollPolicy(ctx context.Context, svc, typ string) (json.RawMessage, error) {
	var raw json.RawMessage
	e := r.pool.QueryRow(ctx, `SELECT config FROM poll_policies WHERE anchor_service=$1 AND anchor_type=$2 AND is_active`, svc, typ).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return raw, e
}
func (r *Repo) PutPollPolicy(ctx context.Context, svc, typ string, raw json.RawMessage, actor *string, seed bool) error {
	sql := `INSERT INTO poll_policies(anchor_service,anchor_type,config,updated_by) VALUES($1,$2,$3,$4) ON CONFLICT(anchor_service,anchor_type) `
	if seed {
		sql += `DO NOTHING`
	} else {
		sql += `DO UPDATE SET config=EXCLUDED.config,updated_by=EXCLUDED.updated_by,is_active=true`
	}
	_, e := r.pool.Exec(ctx, sql, svc, typ, raw, actor)
	return e
}
func (r *Repo) ListPollPolicies(ctx context.Context) ([]PollPolicyRow, error) {
	rows, e := r.pool.Query(ctx, `SELECT id,anchor_service,anchor_type,config,is_active FROM poll_policies ORDER BY anchor_service,anchor_type`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []PollPolicyRow{}
	for rows.Next() {
		var p PollPolicyRow
		if e = rows.Scan(&p.ID, &p.AnchorService, &p.AnchorType, &p.Config, &p.IsActive); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
