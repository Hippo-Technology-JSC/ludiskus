package service

import (
	"context"
	"errors"
	"time"

	"ludiskus/internal/domain"
	commentresolver "ludiskus/internal/resolver"
)

func (s *Service) VerifyCommentTargets(ctx context.Context) (int, error) {
	before := time.Now().Add(-s.cfg.CommentTargetTTL).Format(time.RFC3339Nano)
	targets, err := s.repo.ListStaleCommentTargets(ctx, before, s.cfg.CommentBatchMax)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, t := range targets {
		svc, e := s.repo.GetCommentService(ctx, t.ServiceCode)
		if e != nil || svc.VerifyMode == "trust" {
			continue
		}
		v, e := s.resolver.Resolve(ctx, t.Ref())
		if e != nil {
			state := t.State
			if errors.Is(e, commentresolver.ErrNotFound) || t.VerifyFailures+1 >= 3 {
				state = "gone"
			}
			_ = s.repo.SetCommentTargetState(ctx, t.ID, state, true)
			continue
		}
		next := t
		applyResolvedTarget(&next, v)
		now := time.Now()
		next.VerifiedAt = &now
		if _, e = s.repo.UpsertCommentTarget(ctx, next); e != nil {
			return done, e
		}
		s.clearCommentCaches(ctx, t.Ref())
		done++
	}
	return done, nil
}

func (s *Service) ReconcileCommentCounts(ctx context.Context, targetID string) (int, error) {
	return s.repo.ReconcileCommentCounts(ctx, targetID, 500)
}
func (s *Service) SweepCommentData(ctx context.Context) (int64, error) {
	return s.repo.CleanupCommentData(ctx, s.cfg.CommentMaxRevisions, s.cfg.CommentAuditRetention)
}

func (s *Service) SetCommentThreadState(ctx context.Context, ref domain.ResourceRef, profileUUID, state string) error {
	if !map[string]bool{"open": true, "locked": true, "closed": true, "hidden": true}[state] {
		return domain.ErrValidation
	}
	t, p, err := s.ensureCommentReadable(ctx, ref, profileUUID)
	if err != nil {
		return err
	}
	if !s.commentCapabilities(ctx, t, profileUUID, p).CanModerate {
		return domain.ErrForbidden
	}
	if err = s.repo.SetCommentThreadState(ctx, t.ID, state); err == nil {
		s.clearCommentCaches(ctx, ref)
	}
	return err
}
