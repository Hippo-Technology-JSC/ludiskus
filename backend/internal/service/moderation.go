package service

import (
	"context"
	"strings"

	"ludiskus/internal/domain"
	"ludiskus/internal/markdown"
)

// matchesBanned kiểm tra nội dung có chứa từ cấm (không phân biệt hoa/thường,
// so khớp đơn giản — docs/04 §4.2).
func matchesBanned(body string, banned []string) bool {
	low := strings.ToLower(body)
	for _, w := range banned {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" && strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// decideStatus quyết định trạng thái nội dung mới theo chế độ kiểm duyệt của
// Space (docs/04 §4.1). Trả (status, modSource) — modSource != "" nghĩa là tạo
// ModerationItem.
func (s *Service) decideStatus(ctx context.Context, forum *domain.SpaceForum, authorProfileUUID, role, body string) (string, string, error) {
	// Staff bỏ qua mọi kiểm duyệt.
	if canModerate(role) {
		return domain.StatusPublished, "", nil
	}
	banned := matchesBanned(body, forum.BannedWords)

	switch forum.ModerationMode {
	case domain.ModPre:
		if banned {
			return domain.StatusPending, "banned_word", nil
		}
		return domain.StatusPending, "pre_moderation", nil
	case domain.ModFirstPost:
		had, err := s.repo.HasPublishedPostInSpace(ctx, forum.SpaceUUID, authorProfileUUID)
		if err != nil {
			return "", "", err
		}
		if !had {
			return domain.StatusPending, "first_post", nil
		}
		if banned {
			return domain.StatusPublished, "banned_word", nil
		}
		return domain.StatusPublished, "", nil
	default: // none | post
		if banned {
			return domain.StatusPublished, "banned_word", nil
		}
		return domain.StatusPublished, "", nil
	}
}

// --- reports ----------------------------------------------------------------

type ReportInput struct {
	Reason string  `json:"reason"`
	Note   *string `json:"note"`
}

func (s *Service) ReportTarget(ctx context.Context, profileUUID, targetType, targetID string, in ReportInput) error {
	if profileUUID == "" {
		return domain.ErrUnauthorized
	}
	spaceUUID, ok, err := s.targetSpace(ctx, targetType, targetID)
	if err != nil || !ok {
		return domain.ErrNotFound
	}
	if _, err := s.requireView(ctx, spaceUUID, profileUUID); err != nil {
		return err
	}
	if in.Reason == "" {
		in.Reason = "other"
	}
	if _, err := s.repo.CreateReport(ctx, domain.Report{
		SpaceUUID: spaceUUID, TargetType: targetType, TargetID: targetID,
		ReporterProfileUUID: profileUUID, Reason: in.Reason, Note: in.Note,
	}); err != nil {
		return err
	}
	// Tự ẩn nếu đạt ngưỡng (docs/04 §4.3).
	forum, err := s.repo.GetForum(ctx, spaceUUID)
	if err != nil {
		return nil
	}
	if forum.ReportAutoHideThreshold > 0 {
		n, _ := s.repo.CountOpenReports(ctx, targetType, targetID)
		if n >= forum.ReportAutoHideThreshold {
			s.hideTarget(ctx, targetType, targetID)
			item, e := s.repo.CreateModerationItem(ctx, domain.ModerationItem{
				SpaceUUID: spaceUUID, TargetType: targetType, TargetID: targetID, Source: "auto_hide",
			})
			if e == nil {
				s.notifyModerators(ctx, spaceUUID, item.ID)
			}
		}
	}
	return nil
}

func (s *Service) ListReports(ctx context.Context, spaceUUID, profileUUID string, limit int) ([]domain.Report, error) {
	if err := s.requireModerate(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListOpenReports(ctx, spaceUUID, limit)
}

func (s *Service) ResolveReport(ctx context.Context, reportID, profileUUID, status string) error {
	// status: resolved | dismissed (quyền moderate kiểm gián tiếp qua space khi cần).
	if status != "resolved" && status != "dismissed" {
		return domain.ErrValidation
	}
	return s.repo.SetReportStatus(ctx, reportID, status)
}

// --- moderation queue -------------------------------------------------------

func (s *Service) ListModerationQueue(ctx context.Context, spaceUUID, profileUUID, state string, limit int) ([]domain.ModerationItem, error) {
	if err := s.requireModerate(ctx, spaceUUID, profileUUID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListModerationQueue(ctx, spaceUUID, state, limit)
}

// ApproveModeration duyệt → publish target (docs/04 §4.4).
func (s *Service) ApproveModeration(ctx context.Context, itemID, profileUUID string) error {
	item, err := s.repo.GetModerationItem(ctx, itemID)
	if err != nil {
		return err
	}
	if err := s.requireModerate(ctx, item.SpaceUUID, profileUUID); err != nil {
		return err
	}
	if _, err := s.repo.DecideModerationItem(ctx, itemID, "approved", profileUUID, nil); err != nil {
		return err
	}
	switch item.TargetType {
	case "topic":
		if err := s.repo.SetTopicStatus(ctx, item.TargetID, domain.StatusPublished); err != nil {
			return err
		}
		if fp, e := s.repo.FirstPost(ctx, item.TargetID); e == nil {
			s.repo.PublishPost(ctx, fp.ID)
			s.afterPostPublished(ctx, fp)
		}
	case "post":
		p, e := s.repo.PublishPost(ctx, item.TargetID)
		if e == nil {
			s.afterPostPublished(ctx, p)
		}
	}
	s.notifyModerationDecided(ctx, item, "duyệt", nil)
	return nil
}

// RejectModeration từ chối → ẩn target.
func (s *Service) RejectModeration(ctx context.Context, itemID, profileUUID string, note *string) error {
	item, err := s.repo.GetModerationItem(ctx, itemID)
	if err != nil {
		return err
	}
	if err := s.requireModerate(ctx, item.SpaceUUID, profileUUID); err != nil {
		return err
	}
	if _, err := s.repo.DecideModerationItem(ctx, itemID, "rejected", profileUUID, note); err != nil {
		return err
	}
	s.hideTarget(ctx, item.TargetType, item.TargetID)
	s.notifyModerationDecided(ctx, item, "từ chối", note)
	return nil
}

// --- helpers ----------------------------------------------------------------

func (s *Service) hideTarget(ctx context.Context, targetType, targetID string) {
	switch targetType {
	case "topic":
		s.repo.SetTopicStatus(ctx, targetID, domain.StatusHidden)
	case "post":
		s.repo.SetPostStatus(ctx, targetID, domain.StatusHidden)
	}
}

// targetSpace trả space_uuid của một target (topic|post).
func (s *Service) targetSpace(ctx context.Context, targetType, targetID string) (string, bool, error) {
	switch targetType {
	case "topic":
		t, err := s.repo.GetTopic(ctx, targetID)
		if err != nil {
			return "", false, err
		}
		return t.SpaceUUID, true, nil
	case "post":
		p, err := s.repo.GetPost(ctx, targetID)
		if err != nil {
			return "", false, err
		}
		return p.SpaceUUID, true, nil
	}
	return "", false, nil
}

// excerptOf rút gọn nội dung cho thông báo.
func excerptOf(body string) string { return markdown.Excerpt(body, 140) }
