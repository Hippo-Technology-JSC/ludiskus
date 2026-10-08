package service

import (
	"context"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
	"time"
)

func (s *Service) PollIDs(ctx context.Context, ids []string) (map[string][]string, error) {
	return s.repo.PollIDs(ctx, ids)
}
func (s *Service) AttachDrafts(ctx context.Context, tx pgx.Tx, profile string, ref domain.ResourceRef, snap domain.InteractionContext, ids []string, pending bool) error {
	if len(ids) == 0 {
		return nil
	}
	if !s.cfg.PollEnabled {
		return domain.ErrValidation
	}
	if e := ref.Validate(); e != nil {
		return e
	}
	if ref.Service == "ludiskus" && ref.Type == "poll" {
		return domain.PollFailure("ANCHOR_NOT_ALLOWED", 422, "anchor")
	}
	t := domain.CommentTarget{ServiceCode: ref.Service, ResourceType: ref.Type, ResourceID: ref.ID, SpaceUUID: snap.SpaceUUID, Visibility: snap.Visibility, State: "active", ThreadState: "open", Title: snap.Title, Summary: snap.Summary, ThumbnailURL: snap.ThumbnailURL, CanonicalPath: snap.CanonicalPath, Capabilities: snap.Capabilities, CreatedBy: &profile}
	now := time.Now()
	t.VerifiedAt = &now
	if snap.Owner != nil {
		t.OwnerType = &snap.Owner.Type
		t.OwnerID = &snap.Owner.ID
	}
	policy, e := s.ensurePollCreatable(ctx, &t, "", pollViewer{Profile: profile})
	if e != nil {
		return e
	}
	if len(ids) > policy.MaxPerAnchor {
		return domain.PollFailure("POLL_LIMIT", 409, "pollIds")
	}
	target, e := s.repo.UpsertCommentTargetTx(ctx, tx, t)
	if e != nil {
		return e
	}
	if e = s.lockPollAnchor(ctx, tx, target.ID, policy.MaxPerAnchor); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return domain.ErrValidation
		}
		seen[id] = true
		p, e := s.repo.PollTx(ctx, tx, id, true)
		if e != nil {
			return e
		}
		if p.Status != "draft" || p.AnchorTargetID != nil || p.CreatedBy == nil || *p.CreatedBy != profile || time.Since(p.CreatedAt) > s.cfg.PollDraftTTL {
			return domain.PollLocked("anchor")
		}
		p.Options, e = s.repo.PollOptionsTx(ctx, tx, id)
		if e != nil {
			return e
		}
		p.Target = target
		p.AnchorTargetID = &target.ID
		p.Visibility = nil
		p.SpaceUUID = nil
		p.Status = "published"
		if pending {
			p.Status = "pending"
		}
		if e = validatePoll(p, policy); e != nil {
			return e
		}
		if e = s.pollEligibleTx(ctx, tx, p); e != nil {
			return e
		}
		if e = s.notifyPublishedPollInvitesTx(ctx, tx, p); e != nil {
			return e
		}
		if e = s.repo.SavePollTx(ctx, tx, p, false); e != nil {
			return e
		}
		if e = s.pollAudit(ctx, tx, p, pollViewer{Profile: profile}, "attach", map[string]any{"anchor": ref}); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) OnAnchorDecision(ctx context.Context, tx pgx.Tx, ref domain.ResourceRef, approved bool) error {
	state, status := "blocked", "rejected"
	if approved {
		state, status = "active", "published"
	}
	_, e := tx.Exec(ctx, `UPDATE comment_targets SET state=$4,verified_at=now() WHERE service_code=$1 AND resource_type=$2 AND resource_id=$3`, ref.Service, ref.Type, ref.ID, state)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE polls SET status=$4 WHERE anchor_target_id IN(SELECT id FROM comment_targets WHERE service_code=$1 AND resource_type=$2 AND resource_id=$3) AND status='pending'`, ref.Service, ref.Type, ref.ID, status)
	return e
}
func (s *Service) AttachPoll(ctx context.Context, id string, v pollViewer, ref domain.ResourceRef, actor string) (map[string]any, error) {
	if ref.Service == "ludiskus" && ref.Type == "poll" {
		return nil, domain.PollFailure("ANCHOR_NOT_ALLOWED", 422, "anchor")
	}
	if v.Service != "" && v.Service != ref.Service {
		return nil, domain.ErrServiceScope
	}
	profile := v.Profile
	if v.Service != "" {
		profile = actor
	}
	p, e := s.repo.GetPoll(ctx, id)
	if e != nil {
		return nil, e
	}
	if p.CreatedBy == nil || *p.CreatedBy != profile || p.Status != "draft" || p.AnchorTargetID != nil || time.Since(p.CreatedAt) > s.cfg.PollDraftTTL {
		return nil, domain.ErrForbidden
	}
	t, e := s.ensureCommentTarget(ctx, ref, profile)
	if e != nil {
		return nil, e
	}
	policy, e := s.ensurePollCreatable(ctx, t, "", v)
	if e != nil {
		return nil, e
	}
	if t.State != "active" {
		return nil, domain.PollFailure("ANCHOR_UNVERIFIED", 409, "anchor")
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = s.lockPollAnchor(ctx, tx, t.ID, policy.MaxPerAnchor); e != nil {
		return nil, e
	}
	p, e = s.repo.PollTx(ctx, tx, id, true)
	if e != nil {
		return nil, e
	}
	if p.Status != "draft" || p.AnchorTargetID != nil {
		return nil, domain.ErrConflict
	}
	p.Target = t
	p.AnchorTargetID = &t.ID
	p.SpaceUUID = nil
	p.Visibility = nil
	p.Options, e = s.repo.PollOptionsTx(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	p.Status = s.pollPublicationStatus(ctx, p, policy)
	if e = validatePoll(p, policy); e != nil {
		return nil, e
	}
	if p.Status == "pending" {
		if e = s.enqueuePollModerationTx(ctx, tx, p, "poll", id, "pre"); e != nil {
			return nil, e
		}
	}
	if e = s.pollEligibleTx(ctx, tx, p); e != nil {
		return nil, e
	}
	if e = s.notifyPublishedPollInvitesTx(ctx, tx, p); e != nil {
		return nil, e
	}
	if e = s.repo.SavePollTx(ctx, tx, p, false); e != nil {
		return nil, e
	}
	if e = s.pollAudit(ctx, tx, p, v, "attach", map[string]any{"anchor": ref}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	s.syncInteractionResource(ctx, "poll", id)
	return s.GetPoll(ctx, id, v)
}
