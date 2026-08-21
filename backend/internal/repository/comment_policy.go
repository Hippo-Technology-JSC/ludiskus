package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
)

type CommentPolicyRow struct {
	ID           string          `json:"id"`
	ServiceCode  string          `json:"serviceCode"`
	ResourceType string          `json:"resourceType"`
	Config       json.RawMessage `json:"config"`
	IsActive     bool            `json:"isActive"`
}

func (r *Repo) GetCommentPolicy(ctx context.Context, serviceCode, resourceType string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := r.pool.QueryRow(ctx, `SELECT config FROM comment_policies WHERE service_code=$1 AND resource_type=$2 AND is_active`, serviceCode, resourceType).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return raw, err
}

func (r *Repo) ListCommentPolicies(ctx context.Context, serviceCode string) ([]CommentPolicyRow, error) {
	q := `SELECT id,service_code,resource_type,config,is_active FROM comment_policies`
	args := []any{}
	if serviceCode != "" {
		q += ` WHERE service_code=$1`
		args = append(args, serviceCode)
	}
	q += ` ORDER BY service_code,resource_type`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentPolicyRow{}
	for rows.Next() {
		var p CommentPolicyRow
		if err := rows.Scan(&p.ID, &p.ServiceCode, &p.ResourceType, &p.Config, &p.IsActive); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) UpsertCommentPolicy(ctx context.Context, serviceCode, resourceType string, config json.RawMessage, updatedBy *string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO comment_policies(service_code,resource_type,config,updated_by)
		VALUES($1,$2,$3,$4) ON CONFLICT(service_code,resource_type) DO UPDATE SET
		config=EXCLUDED.config,updated_by=EXCLUDED.updated_by,is_active=true`, serviceCode, resourceType, config, updatedBy)
	return err
}
