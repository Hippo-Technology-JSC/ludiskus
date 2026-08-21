package service

import (
	"context"
	"fmt"

	"ludiskus/internal/domain"
)

func (s *Service) ReportComment(ctx context.Context, id, profileUUID, reason string, note *string) error {
	c, t, err := s.GetComment(ctx, id, profileUUID)
	if err != nil {
		return err
	}
	if c.AuthorProfileUUID != nil && *c.AuthorProfileUUID == profileUUID {
		return fmt.Errorf("%w: không thể báo cáo bình luận của chính mình", domain.ErrValidation)
	}
	valid := map[string]bool{"spam": true, "abuse": true, "offtopic": true, "sexual": true, "violence": true, "private_info": true, "other": true}
	if !valid[reason] {
		return fmt.Errorf("%w: lý do báo cáo không hợp lệ", domain.ErrValidation)
	}
	created, err := s.repo.CreateCommentReport(ctx, t.SpaceUUID, id, profileUUID, reason, note)
	if err != nil || !created {
		return err
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return err
	}
	n, err := s.repo.CountOpenReports(ctx, "comment", id)
	if err == nil && c.Status == domain.CommentPublished && p.ReportAutoHideThreshold > 0 && n >= p.ReportAutoHideThreshold {
		hidden, transitionErr := s.repo.TransitionComment(ctx, id, domain.CommentHidden, "", "auto_hide")
		if transitionErr != nil {
			return transitionErr
		}
		if _, itemErr := s.repo.CreateCommentModerationItem(ctx, t.SpaceUUID, id, "auto_hide"); itemErr == nil {
			s.notifyCommentPending(ctx, t, hidden)
		} else {
			return itemErr
		}
		s.invalidateInteractionResource(ctx, "comment", id, "visibility")
		s.clearCommentCaches(ctx, t.Ref())
	}
	return nil
}
