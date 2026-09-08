package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"ludiskus/internal/domain"
)

// BoardACL chứa cấu hình quyền và moderator của board phục vụ evaluator.
type BoardACL struct {
	BoardID               string
	TopicMode             string
	ReplyMode             string
	Version               int64
	TopicSelectedProfiles map[string]bool
	ReplySelectedProfiles map[string]bool
	DirectModerators      map[string]bool
}

// GetBoardACL nạp ACL của Board. Nếu chưa có cấu hình riêng, trả mặc định inherit_space.
func (r *Repo) GetBoardACL(ctx context.Context, boardID string) (*BoardACL, error) {
	acl := &BoardACL{
		BoardID:               boardID,
		TopicMode:             domain.BoardPermInheritSpace,
		ReplyMode:             domain.BoardPermInheritSpace,
		Version:               1,
		TopicSelectedProfiles: make(map[string]bool),
		ReplySelectedProfiles: make(map[string]bool),
		DirectModerators:      make(map[string]bool),
	}

	err := r.pool.QueryRow(ctx, `
		SELECT topic_mode, reply_mode, version
		FROM board_permissions
		WHERE board_id = $1`, boardID).Scan(&acl.TopicMode, &acl.ReplyMode, &acl.Version)
	if err != nil && !isNotFound(err) {
		return nil, err
	}

	// Nạp selected profiles
	rows, err := r.pool.Query(ctx, `
		SELECT action, profile_uuid
		FROM board_permission_profiles
		WHERE board_id = $1`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var action, profileUUID string
		if err := rows.Scan(&action, &profileUUID); err != nil {
			return nil, err
		}
		if action == domain.BoardActionCreateTopic {
			acl.TopicSelectedProfiles[profileUUID] = true
		} else if action == domain.BoardActionReply {
			acl.ReplySelectedProfiles[profileUUID] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Nạp direct moderators
	modRows, err := r.pool.Query(ctx, `
		SELECT profile_uuid
		FROM board_moderators
		WHERE board_id = $1`, boardID)
	if err != nil {
		return nil, err
	}
	defer modRows.Close()

	for modRows.Next() {
		var profileUUID string
		if err := modRows.Scan(&profileUUID); err != nil {
			return nil, err
		}
		acl.DirectModerators[profileUUID] = true
	}
	if err := modRows.Err(); err != nil {
		return nil, err
	}

	return acl, nil
}

// GetBoardPermissions trả chi tiết cấu hình phân quyền cho owner/admin Space.
func (r *Repo) GetBoardPermissions(ctx context.Context, boardID, spaceUUID string) (*domain.BoardPermissionDetail, error) {
	acl, err := r.GetBoardACL(ctx, boardID)
	if err != nil {
		return nil, err
	}

	topicProfiles := make([]string, 0, len(acl.TopicSelectedProfiles))
	for p := range acl.TopicSelectedProfiles {
		topicProfiles = append(topicProfiles, p)
	}

	replyProfiles := make([]string, 0, len(acl.ReplySelectedProfiles))
	for p := range acl.ReplySelectedProfiles {
		replyProfiles = append(replyProfiles, p)
	}

	directMods := make([]string, 0, len(acl.DirectModerators))
	for m := range acl.DirectModerators {
		directMods = append(directMods, m)
	}

	// Lấy staff kế thừa từ Space: owner, admin trong space_member_cache + space_moderators
	inheritedStaff, err := r.ListSpaceStaff(ctx, spaceUUID)
	if err != nil {
		return nil, err
	}

	return &domain.BoardPermissionDetail{
		BoardID: boardID,
		Version: acl.Version,
		TopicPolicy: domain.BoardPolicyConfig{
			Mode:         acl.TopicMode,
			ProfileUUIDs: topicProfiles,
		},
		ReplyPolicy: domain.BoardPolicyConfig{
			Mode:         acl.ReplyMode,
			ProfileUUIDs: replyProfiles,
		},
		ModeratorProfileUUIDs:          directMods,
		InheritedModeratorProfileUUIDs: inheritedStaff,
	}, nil
}

// ListSpaceStaff trả danh sách profile UUID là owner/admin hoặc space_moderator của Space.
func (r *Repo) ListSpaceStaff(ctx context.Context, spaceUUID string) ([]string, error) {
	staffMap := make(map[string]bool)

	// Space creator
	if space, err := r.GetCachedSpace(ctx, spaceUUID); err == nil && space.CreatorProfileUUID != nil {
		staffMap[*space.CreatorProfileUUID] = true
	}

	// Members with role owner or admin
	rows, err := r.pool.Query(ctx, `
		SELECT profile_uuid
		FROM space_member_cache
		WHERE space_uuid = $1 AND role IN ('owner', 'admin')`, spaceUUID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var u string
			if rows.Scan(&u) == nil {
				staffMap[u] = true
			}
		}
	}

	// Space moderators
	modRows, err := r.pool.Query(ctx, `
		SELECT profile_uuid
		FROM space_moderators
		WHERE space_uuid = $1`, spaceUUID)
	if err == nil {
		defer modRows.Close()
		for modRows.Next() {
			var u string
			if modRows.Scan(&u) == nil {
				staffMap[u] = true
			}
		}
	}

	out := make([]string, 0, len(staffMap))
	for u := range staffMap {
		out = append(out, u)
	}
	return out, nil
}

// UpdateBoardPermissions lưu toàn bộ chính sách phân quyền và moderator trong một transaction.
func (r *Repo) UpdateBoardPermissions(ctx context.Context, boardID, spaceUUID, actorUUID, reqID string,
	expectedVersion int64, topicPolicy, replyPolicy domain.BoardPolicyConfig, directMods []string) (*domain.BoardPermissionDetail, error) {

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var currentVersion int64 = 1
	var currentTopicMode string = domain.BoardPermInheritSpace
	var currentReplyMode string = domain.BoardPermInheritSpace

	err = tx.QueryRow(ctx, `
		SELECT version, topic_mode, reply_mode
		FROM board_permissions
		WHERE board_id = $1
		FOR UPDATE`, boardID).Scan(&currentVersion, &currentTopicMode, &currentReplyMode)

	if isNotFound(err) {
		// Chưa có bản ghi board_permissions, version khởi đầu là 1
		currentVersion = 1
		if expectedVersion != 1 {
			return nil, fmt.Errorf("%w: version không khớp (hiện tại: 1, yêu cầu: %d)", domain.ErrConflict, expectedVersion)
		}
	} else if err != nil {
		return nil, err
	} else {
		if currentVersion != expectedVersion {
			return nil, fmt.Errorf("%w: version không khớp (hiện tại: %d, yêu cầu: %d)", domain.ErrConflict, currentVersion, expectedVersion)
		}
	}

	newVersion := currentVersion + 1

	// Audit snapshot
	changesPayload, _ := json.Marshal(map[string]any{
		"before": map[string]any{
			"version":   currentVersion,
			"topicMode": currentTopicMode,
			"replyMode": currentReplyMode,
		},
		"after": map[string]any{
			"version":     newVersion,
			"topicPolicy": topicPolicy,
			"replyPolicy": replyPolicy,
			"moderators":  directMods,
		},
	})

	_, err = tx.Exec(ctx, `
		INSERT INTO board_permission_audit (
			actor_profile_uuid, space_uuid, board_id, version_before, version_after, changes, request_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		actorUUID, spaceUUID, boardID, currentVersion, newVersion, changesPayload, reqID)
	if err != nil {
		return nil, err
	}

	// Upsert board_permissions
	_, err = tx.Exec(ctx, `
		INSERT INTO board_permissions (
			board_id, topic_mode, reply_mode, version, updated_by_profile_uuid, updated_at
		) VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (board_id) DO UPDATE SET
			topic_mode = EXCLUDED.topic_mode,
			reply_mode = EXCLUDED.reply_mode,
			version = EXCLUDED.version,
			updated_by_profile_uuid = EXCLUDED.updated_by_profile_uuid,
			updated_at = now()`,
		boardID, topicPolicy.Mode, replyPolicy.Mode, newVersion, actorUUID)
	if err != nil {
		return nil, err
	}

	// Xoá danh sách profile cũ và gán lại
	if _, err := tx.Exec(ctx, `DELETE FROM board_permission_profiles WHERE board_id = $1`, boardID); err != nil {
		return nil, err
	}

	if topicPolicy.Mode == domain.BoardPermSelected {
		for _, u := range topicPolicy.ProfileUUIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO board_permission_profiles (board_id, action, profile_uuid)
				VALUES ($1, 'create_topic', $2)
				ON CONFLICT DO NOTHING`, boardID, u); err != nil {
				return nil, err
			}
		}
	}

	if replyPolicy.Mode == domain.BoardPermSelected {
		for _, u := range replyPolicy.ProfileUUIDs {
			if _, err := tx.Exec(ctx, `
				INSERT INTO board_permission_profiles (board_id, action, profile_uuid)
				VALUES ($1, 'reply', $2)
				ON CONFLICT DO NOTHING`, boardID, u); err != nil {
				return nil, err
			}
		}
	}

	// Xoá moderator cũ và gán lại
	if _, err := tx.Exec(ctx, `DELETE FROM board_moderators WHERE board_id = $1`, boardID); err != nil {
		return nil, err
	}

	for _, m := range directMods {
		if _, err := tx.Exec(ctx, `
			INSERT INTO board_moderators (board_id, profile_uuid, granted_by_profile_uuid, created_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT DO NOTHING`, boardID, m, actorUUID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return r.GetBoardPermissions(ctx, boardID, spaceUUID)
}

// IsDirectBoardModerator kiểm tra profile có được gán trực tiếp làm moderator board hay không.
func (r *Repo) IsDirectBoardModerator(ctx context.Context, boardID, profileUUID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM board_moderators
			WHERE board_id = $1 AND profile_uuid = $2
		)`, boardID, profileUUID).Scan(&exists)
	return exists, err
}

// ListDirectModeratedBoards trả danh sách board_id mà profile được gán moderator trực tiếp trong Space.
func (r *Repo) ListDirectModeratedBoards(ctx context.Context, spaceUUID, profileUUID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bm.board_id
		FROM board_moderators bm
		JOIN boards b ON bm.board_id = b.id
		WHERE b.space_uuid = $1 AND bm.profile_uuid = $2`, spaceUUID, profileUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
