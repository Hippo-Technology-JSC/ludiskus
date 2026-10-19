package membershipsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres maintains the existing service-owned identity cache, in one
// transaction per snapshot. table/parent are constants supplied by the host.
// This is a membership mirror; service roles, grants and resource ACLs stay intact.
func Postgres(pool *pgxpool.Pool, table, parent string, uppercase, ownerAsAdmin, payload bool) Apply {
	return func(ctx context.Context, space string, members []Member) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if payload {
			rows := []map[string]any{}
			for _, m := range members {
				rows = append(rows, map[string]any{"space_uuid": space, "profile_uuid": m.ProfileUUID, "role": m.Role})
			}
			raw, _ := json.Marshal(rows)
			_, err = tx.Exec(ctx, `INSERT INTO space_member_cache(space_uuid,payload,synced_at) VALUES($1,$2,now()) ON CONFLICT(space_uuid) DO UPDATE SET payload=EXCLUDED.payload,synced_at=now()`, space, raw)
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
		// Serialize complete replacements across API and worker processes.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "membership-sync:"+space); err != nil {
			return err
		}
		profiles := make([]string, 0, len(members))
		for _, m := range members {
			profiles = append(profiles, m.ProfileUUID)
		}
		if _, err = tx.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE space_uuid=$1 AND NOT (profile_uuid=ANY($2::uuid[]))`, table), space, profiles); err != nil {
			return err
		}
		for _, m := range members {
			role := m.Role
			if ownerAsAdmin && role == "owner" {
				role = "admin"
			}
			if uppercase {
				role = strings.ToUpper(role)
			}
			query := fmt.Sprintf(`INSERT INTO %s(space_uuid,profile_uuid,role,synced_at) SELECT $1::uuid,$2::uuid,$3::text,now()`, table)
			if parent != "" {
				query += fmt.Sprintf(` WHERE EXISTS(SELECT 1 FROM %s WHERE uuid=$1)`, parent)
			}
			query += ` ON CONFLICT(space_uuid,profile_uuid) DO UPDATE SET role=EXCLUDED.role,synced_at=now()`
			if _, err = tx.Exec(ctx, query, space, m.ProfileUUID, role); err != nil {
				return err
			}
		}
		return tx.Commit(ctx)
	}
}
