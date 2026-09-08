package service

import (
	"context"
	"fmt"
	"regexp"

	"ludiskus/internal/domain"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// canManageBoardPermissions kiểm tra actor có phải là owner hoặc admin của Space hay không.
func (s *Service) canManageBoardPermissions(ctx context.Context, spaceUUID, profileUUID string) bool {
	if profileUUID == "" {
		return false
	}
	role := s.role(ctx, spaceUUID, profileUUID)
	return role == domain.RoleOwner || role == domain.RoleAdmin
}

// canModerateBoard kiểm tra profile có phải là moderator hiệu lực của Board hay không (§16.2).
// Moderator hiệu lực = Owner/Admin Space + Moderator Space + Moderator gán trực tiếp tại Board.
func (s *Service) canModerateBoard(ctx context.Context, boardID, profileUUID string) bool {
	if profileUUID == "" {
		return false
	}
	board, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return false
	}

	// 1. Quyền Space staff (owner, admin, space_moderator)
	if canModerate(s.role(ctx, board.SpaceUUID, profileUUID)) {
		return true
	}

	// 2. Moderator gán trực tiếp cho Board
	acl, err := s.repo.GetBoardACL(ctx, boardID)
	if err != nil {
		return false
	}
	if !acl.DirectModerators[profileUUID] {
		return false
	}

	// Moderator gán trực tiếp phải là Profile hoạt động và thành viên Space đang có hiệu lực (§16.2)
	p, err := s.ident.Profile(ctx, profileUUID)
	if err != nil || p == nil || !p.IsActive {
		return false
	}
	return s.ident.IsMember(ctx, board.SpaceUUID, profileUUID)
}

// canCreateTopicBoard kiểm tra quyền tạo chủ đề trong Board (§16.2, §16.3).
func (s *Service) canCreateTopicBoard(ctx context.Context, board *domain.Board, forum *domain.SpaceForum, profileUUID string) (bool, string) {
	if profileUUID == "" {
		return false, "unauthorized"
	}
	// 1. Áp post_policy của Space
	if err := s.requirePost(ctx, forum, profileUUID); err != nil {
		return false, "space_policy_denied"
	}

	// 2. Kiểm trạng thái khoá của Board
	if board.IsLocked {
		return false, "board_locked"
	}

	// 3. Áp topicPolicy của Board
	acl, err := s.repo.GetBoardACL(ctx, board.ID)
	if err != nil {
		return false, "internal_error"
	}

	switch acl.TopicMode {
	case domain.BoardPermInheritSpace:
		return true, ""
	case domain.BoardPermMembers:
		if !s.ident.IsMember(ctx, board.SpaceUUID, profileUUID) {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermModerators:
		if !s.canModerateBoard(ctx, board.ID, profileUUID) {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermAdmins:
		role := s.role(ctx, board.SpaceUUID, profileUUID)
		if role != domain.RoleOwner && role != domain.RoleAdmin {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermSelected:
		if !acl.TopicSelectedProfiles[profileUUID] {
			return false, "board_policy_denied"
		}
		// Phải là thành viên Space còn hiệu lực
		if !s.ident.IsMember(ctx, board.SpaceUUID, profileUUID) {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermNobody:
		return false, "board_policy_denied"
	default:
		return true, ""
	}
}

// canReplyBoard kiểm tra quyền trả lời bài trong Topic của Board (§16.2, §16.3).
func (s *Service) canReplyBoard(ctx context.Context, topic *domain.Topic, board *domain.Board, forum *domain.SpaceForum, profileUUID string) (bool, string) {
	if profileUUID == "" {
		return false, "unauthorized"
	}
	// 1. Áp post_policy của Space
	if err := s.requirePost(ctx, forum, profileUUID); err != nil {
		return false, "space_policy_denied"
	}

	// 2. Kiểm trạng thái khoá của Board
	if board.IsLocked {
		return false, "board_locked"
	}

	// 3. Kiểm trạng thái khoá của Topic
	if topic != nil && topic.Status != domain.StatusPublished {
		return false, "topic_locked"
	}

	// 4. Áp replyPolicy của Board
	acl, err := s.repo.GetBoardACL(ctx, board.ID)
	if err != nil {
		return false, "internal_error"
	}

	switch acl.ReplyMode {
	case domain.BoardPermInheritSpace:
		return true, ""
	case domain.BoardPermMembers:
		if !s.ident.IsMember(ctx, board.SpaceUUID, profileUUID) {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermModerators:
		if !s.canModerateBoard(ctx, board.ID, profileUUID) {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermAdmins:
		role := s.role(ctx, board.SpaceUUID, profileUUID)
		if role != domain.RoleOwner && role != domain.RoleAdmin {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermSelected:
		if !acl.ReplySelectedProfiles[profileUUID] {
			return false, "board_policy_denied"
		}
		if !s.ident.IsMember(ctx, board.SpaceUUID, profileUUID) {
			return false, "board_policy_denied"
		}
		return true, ""
	case domain.BoardPermNobody:
		return false, "board_policy_denied"
	default:
		return true, ""
	}
}

// GetBoardCapabilities trả quyền hiệu lực của người dùng hiện tại trên Board (§16.5).
func (s *Service) GetBoardCapabilities(ctx context.Context, boardID, profileUUID string) (*domain.BoardCapabilities, error) {
	board, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	forum, err := s.requireView(ctx, board.SpaceUUID, profileUUID)
	if err != nil {
		return nil, err
	}

	acl, err := s.repo.GetBoardACL(ctx, boardID)
	if err != nil {
		return nil, err
	}

	canManage := s.canManageBoardPermissions(ctx, board.SpaceUUID, profileUUID)
	canMod := s.canModerateBoard(ctx, boardID, profileUUID)
	canCreate, createReason := s.canCreateTopicBoard(ctx, board, forum, profileUUID)
	canReply, replyReason := s.canReplyBoard(ctx, nil, board, forum, profileUUID)

	reasons := make(map[string]string)
	if !canCreate && createReason != "" {
		reasons["canCreateTopic"] = createReason
	}
	if !canReply && replyReason != "" {
		reasons["canReply"] = replyReason
	}
	if !canMod {
		reasons["canModerate"] = "not_moderator"
	}

	return &domain.BoardCapabilities{
		Version:              acl.Version,
		CanCreateTopic:       canCreate,
		CanReply:             canReply,
		CanModerate:          canMod,
		CanManagePermissions: canManage,
		Reasons:              reasons,
	}, nil
}

// GetBoardPermissions trả chi tiết cấu hình ACL và danh sách moderator của Board (chỉ Owner/Admin Space).
func (s *Service) GetBoardPermissions(ctx context.Context, boardID, profileUUID string) (*domain.BoardPermissionDetail, error) {
	board, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if !s.canManageBoardPermissions(ctx, board.SpaceUUID, profileUUID) {
		return nil, domain.ErrForbidden
	}
	return s.repo.GetBoardPermissions(ctx, boardID, board.SpaceUUID)
}

// UpdateBoardPermissionsInput body gửi lên PUT /boards/{id}/permissions
type UpdateBoardPermissionsInput struct {
	ExpectedVersion       int64                    `json:"expectedVersion"`
	TopicPolicy           domain.BoardPolicyConfig `json:"topicPolicy"`
	ReplyPolicy           domain.BoardPolicyConfig `json:"replyPolicy"`
	ModeratorProfileUUIDs []string                 `json:"moderatorProfileUuids"`
}

func isValidPolicyMode(m string) bool {
	switch m {
	case domain.BoardPermInheritSpace, domain.BoardPermMembers, domain.BoardPermModerators,
		domain.BoardPermAdmins, domain.BoardPermSelected, domain.BoardPermNobody:
		return true
	}
	return false
}

// UpdateBoardPermissions lưu toàn bộ cấu hình ACL và moderator của Board (§16.4, §16.5).
func (s *Service) UpdateBoardPermissions(ctx context.Context, boardID, profileUUID, reqID string, in UpdateBoardPermissionsInput) (*domain.BoardPermissionDetail, error) {
	board, err := s.repo.GetBoard(ctx, boardID)
	if err != nil {
		return nil, err
	}
	if !s.canManageBoardPermissions(ctx, board.SpaceUUID, profileUUID) {
		return nil, domain.ErrForbidden
	}

	// Validate policy modes
	if !isValidPolicyMode(in.TopicPolicy.Mode) {
		return nil, fmt.Errorf("%w: topicPolicy.mode %q không hợp lệ", domain.ErrValidation, in.TopicPolicy.Mode)
	}
	if !isValidPolicyMode(in.ReplyPolicy.Mode) {
		return nil, fmt.Errorf("%w: replyPolicy.mode %q không hợp lệ", domain.ErrValidation, in.ReplyPolicy.Mode)
	}

	// Mode khác 'selected' không nhận danh sách profile
	if in.TopicPolicy.Mode != domain.BoardPermSelected {
		in.TopicPolicy.ProfileUUIDs = nil
	}
	if in.ReplyPolicy.Mode != domain.BoardPermSelected {
		in.ReplyPolicy.ProfileUUIDs = nil
	}

	// Giới hạn 100 profile mỗi danh sách (§16.4)
	if len(in.TopicPolicy.ProfileUUIDs) > 100 {
		return nil, fmt.Errorf("%w: topicPolicy vượt quá giới hạn 100 profile", domain.ErrValidation)
	}
	if len(in.ReplyPolicy.ProfileUUIDs) > 100 {
		return nil, fmt.Errorf("%w: replyPolicy vượt quá giới hạn 100 profile", domain.ErrValidation)
	}
	if len(in.ModeratorProfileUUIDs) > 100 {
		return nil, fmt.Errorf("%w: danh sách moderator vượt quá giới hạn 100 profile", domain.ErrValidation)
	}

	// Xác minh profile UUID và thành viên Space (§16.4)
	validateProfiles := func(label string, uuids []string) error {
		seen := make(map[string]bool)
		for _, u := range uuids {
			if !uuidRegex.MatchString(u) {
				return fmt.Errorf("%w: %s chứa UUID không hợp lệ: %q", domain.ErrValidation, label, u)
			}
			if seen[u] {
				return fmt.Errorf("%w: %s chứa profile trùng lặp: %q", domain.ErrValidation, label, u)
			}
			seen[u] = true
			p, err := s.ident.Profile(ctx, u)
			if err != nil || p == nil || !p.IsActive {
				return fmt.Errorf("%w: profile %q trong %s không hoạt động hoặc không tồn tại", domain.ErrValidation, u, label)
			}
			if !s.ident.IsMember(ctx, board.SpaceUUID, u) {
				return fmt.Errorf("%w: profile %q trong %s không phải thành viên của Space", domain.ErrValidation, u, label)
			}
		}
		return nil
	}

	if in.TopicPolicy.Mode == domain.BoardPermSelected {
		if err := validateProfiles("topicPolicy", in.TopicPolicy.ProfileUUIDs); err != nil {
			return nil, err
		}
	}
	if in.ReplyPolicy.Mode == domain.BoardPermSelected {
		if err := validateProfiles("replyPolicy", in.ReplyPolicy.ProfileUUIDs); err != nil {
			return nil, err
		}
	}
	if err := validateProfiles("moderators", in.ModeratorProfileUUIDs); err != nil {
		return nil, err
	}

	return s.repo.UpdateBoardPermissions(ctx, boardID, board.SpaceUUID, profileUUID, reqID,
		in.ExpectedVersion, in.TopicPolicy, in.ReplyPolicy, in.ModeratorProfileUUIDs)
}

// allowedModerationBoards xác định danh sách board ID mà profile được phép kiểm duyệt trong Space.
// Trả về (allowedBoardIDs, isAllSpace, error). Nếu isAllSpace == true thì được kiểm duyệt mọi board.
func (s *Service) allowedModerationBoards(ctx context.Context, spaceUUID, profileUUID, requestedBoard string) ([]string, error) {
	if profileUUID == "" {
		return nil, domain.ErrUnauthorized
	}

	isStaff := canModerate(s.role(ctx, spaceUUID, profileUUID))
	if isStaff {
		if requestedBoard != "" {
			b, err := s.repo.GetBoard(ctx, requestedBoard)
			if err != nil {
				return nil, err
			}
			if b.SpaceUUID != spaceUUID {
				return nil, domain.ErrForbidden
			}
			return []string{requestedBoard}, nil
		}
		return nil, nil // nil nghĩa là toàn bộ board trong space
	}

	// Moderator gán trực tiếp
	directBoards, err := s.repo.ListDirectModeratedBoards(ctx, spaceUUID, profileUUID)
	if err != nil {
		return nil, err
	}
	if len(directBoards) == 0 {
		return nil, domain.ErrForbidden
	}

	if requestedBoard != "" {
		found := false
		for _, b := range directBoards {
			if b == requestedBoard {
				found = true
				break
			}
		}
		if !found {
			return nil, domain.ErrForbidden
		}
		return []string{requestedBoard}, nil
	}

	return directBoards, nil
}
