package service

import (
	"context"
	"encoding/json"
	"fmt"

	"ludiskus/internal/domain"
	"ludiskus/internal/repository"
)

func (s *Service) CommentServiceForClient(ctx context.Context, clientID string) (*domain.CommentService, error) {
	v, err := s.repo.CommentServiceByClientID(ctx, clientID)
	if err != nil {
		return nil, domain.ErrUnknownServiceClient
	}
	return v, nil
}

type SystemCommentInput struct {
	Ref               domain.ResourceRef `json:"ref"`
	BodyMD            string             `json:"bodyMd"`
	ParentID          *string            `json:"parentId"`
	AuthorProfileUUID *string            `json:"authorProfileUuid"`
	IdempotencyKey    string             `json:"idempotencyKey"`
}

func (s *Service) CreateSystemComment(ctx context.Context, serviceCode string, in SystemCommentInput) (*domain.Comment, bool, error) {
	if in.Ref.Service != serviceCode {
		return nil, false, domain.ErrServiceScope
	}
	if in.IdempotencyKey == "" {
		return nil, false, fmt.Errorf("%w: idempotencyKey là bắt buộc", domain.ErrValidation)
	}
	t, err := s.ensureCommentTarget(ctx, in.Ref, "")
	if err != nil {
		return nil, false, err
	}
	p, err := s.commentPolicy(ctx, t)
	if err != nil {
		return nil, false, err
	}
	body := normalizeCommentBody(in.BodyMD)
	if n := len([]rune(body)); n < p.MinLength || n > p.MaxLength {
		return nil, false, domain.ErrValidation
	}
	source := serviceCode
	c := domain.Comment{TargetID: t.ID, ParentID: in.ParentID, AuthorKind: "service", AuthorProfileUUID: in.AuthorProfileUUID, SourceService: &source, BodyMD: body, BodyHTML: s.md.RenderBasic(body), BodyHash: commentBodyHash(body), MarkdownMode: "basic", Status: domain.CommentPublished, IdempotencyKey: &in.IdempotencyKey}
	if in.ParentID != nil {
		parent, e := s.repo.GetComment(ctx, *in.ParentID)
		if e != nil {
			return nil, false, e
		}
		if parent.TargetID != t.ID {
			return nil, false, domain.ErrValidation
		}
		c.RootID = parent.RootID
		c.Depth = min(parent.Depth+1, p.MaxDepth)
		if parent.Depth+1 > p.MaxDepth {
			c.ReplyToProfileUUID = parent.AuthorProfileUUID
			anchor := parent
			for anchor.Depth >= p.MaxDepth && anchor.ParentID != nil {
				anchor, e = s.repo.GetComment(ctx, *anchor.ParentID)
				if e != nil {
					return nil, false, e
				}
			}
			c.ParentID = &anchor.ID
		}
	}
	notifyPolicy := p
	notifyPolicy.Notify.Owner = false
	notifications := s.commentNotifyRows(ctx, t, &c, notifyPolicy, nil)
	out, created, err := s.repo.InsertComment(ctx, repository.InsertCommentInput{Comment: c, SpaceUUID: t.SpaceUUID, Notifications: notifications})
	if err != nil {
		return nil, false, err
	}
	if created {
		s.afterCommentPublished(ctx, t, out, notifyPolicy, nil, true)
		s.syncInteractionResource(ctx, "comment", out.ID)
	}
	return out, created, nil
}

func (s *Service) S2SSetCommentThreadState(ctx context.Context, serviceCode string, ref domain.ResourceRef, state string) error {
	if ref.Service != serviceCode {
		return domain.ErrServiceScope
	}
	if !map[string]bool{"open": true, "locked": true, "closed": true, "hidden": true}[state] {
		return domain.ErrValidation
	}
	t, err := s.repo.GetCommentTarget(ctx, ref)
	if err != nil {
		return err
	}
	if err = s.repo.SetCommentThreadState(ctx, t.ID, state); err == nil {
		s.clearCommentCaches(ctx, ref)
	}
	return err
}
func (s *Service) S2SModerateComment(ctx context.Context, serviceCode, id, action, actorProfile, reason string) (*domain.Comment, error) {
	c, err := s.repo.GetComment(ctx, id)
	if err != nil {
		return nil, err
	}
	t, err := s.repo.GetCommentTargetByID(ctx, c.TargetID)
	if err != nil {
		return nil, err
	}
	if t.ServiceCode != serviceCode {
		return nil, domain.ErrServiceScope
	}
	if action == "pin" || action == "unpin" {
		return s.repo.SetCommentPinned(ctx, id, action == "pin", actorProfile, 100)
	}
	status := map[string]string{"hide": domain.CommentHidden, "restore": domain.CommentPublished, "delete": domain.CommentDeleted, "approve": domain.CommentPublished, "reject": domain.CommentRejected}[action]
	if status == "" {
		return nil, domain.ErrValidation
	}
	notifications := []repository.CommentNotifyInsert(nil)
	var policy domain.CommentPolicy
	var mentions []string
	if action == "approve" {
		policy, _ = s.commentPolicy(ctx, t)
		mentions, _ = s.repo.CommentMentions(ctx, c.ID)
		notifications = s.commentNotifyRows(ctx, t, c, policy, mentions)
	}
	out, err := s.repo.TransitionCommentByServiceWithNotify(ctx, id, status, serviceCode, actorProfile, reason, notifications)
	if err == nil {
		s.clearCommentCaches(ctx, t.Ref())
		if status == domain.CommentPublished {
			if action == "approve" {
				s.afterCommentPublished(ctx, t, out, policy, mentions, true)
			}
			s.syncInteractionResource(ctx, "comment", id)
		} else {
			s.invalidateInteractionResource(ctx, "comment", id, "visibility")
		}
	}
	return out, err
}

func (s *Service) ExportComments(ctx context.Context, serviceCode string, ref domain.ResourceRef) ([]domain.Comment, error) {
	if ref.Service != serviceCode {
		return nil, domain.ErrServiceScope
	}
	t, err := s.repo.GetCommentTarget(ctx, ref)
	if err != nil {
		return nil, err
	}
	roots, err := s.repo.ListRootComments(ctx, t.ID, "", "oldest", nil, nil, nil, "", 1000)
	if err != nil {
		return nil, err
	}
	out := []domain.Comment{}
	for _, root := range roots {
		out = append(out, root)
		replies, e := s.repo.ListCommentReplies(ctx, root.ID, "", nil, "", 10000)
		if e != nil {
			return nil, e
		}
		out = append(out, replies...)
	}
	return out, nil
}

func (s *Service) AdminCommentServices(ctx context.Context) ([]domain.CommentService, error) {
	return s.repo.ListCommentServices(ctx)
}
func (s *Service) AdminUpsertCommentService(ctx context.Context, in domain.CommentService) (*domain.CommentService, error) {
	if in.Code == "" || in.Name == "" {
		return nil, domain.ErrValidation
	}
	if in.VerifyMode == "" {
		in.VerifyMode = "optimistic"
	}
	in.IsActive = true
	return s.repo.UpsertCommentService(ctx, in)
}
func (s *Service) AdminDisableCommentService(ctx context.Context, code string) error {
	return s.repo.DisableCommentService(ctx, code)
}
func (s *Service) AdminCommentPolicies(ctx context.Context, serviceCode string) ([]repository.CommentPolicyRow, error) {
	return s.repo.ListCommentPolicies(ctx, serviceCode)
}
func (s *Service) AdminPutCommentPolicy(ctx context.Context, serviceCode, resourceType string, raw json.RawMessage, actor *string) ([]string, error) {
	var partial map[string]any
	if json.Unmarshal(raw, &partial) != nil {
		return nil, domain.ErrValidation
	}
	t := domain.CommentTarget{ServiceCode: serviceCode, ResourceType: resourceType, Capabilities: json.RawMessage(`{}`)}
	merged, err := mergeJSONObjects(domain.DefaultCommentPolicy(), raw)
	if err != nil {
		return nil, err
	}
	var p domain.CommentPolicy
	if json.Unmarshal(merged, &p) != nil || validateCommentPolicy(p) != nil {
		return nil, domain.ErrValidation
	}
	if err = s.repo.UpsertCommentPolicy(ctx, serviceCode, resourceType, raw, actor); err != nil {
		return nil, err
	}
	s.invalidateCommentPolicyCache(ctx)
	warnings := []string{}
	svc, _ := s.repo.GetCommentService(ctx, serviceCode)
	if (p.ModerationMode == "pre" || p.ModerationMode == "first_comment") && (svc == nil || svc.OAuthClientID == "") {
		warnings = append(warnings, "Chế độ tiền kiểm nhưng service chưa có oauth_client_id; bình luận có thể kẹt ở hàng chờ")
	}
	_ = t
	return warnings, nil
}

func (s *Service) CommentCountsS2S(ctx context.Context, serviceCode string, refs []domain.ResourceRef) ([]CommentSummary, error) {
	out := []CommentSummary{}
	for _, ref := range refs {
		if ref.Service != serviceCode {
			return nil, domain.ErrServiceScope
		}
		t, err := s.repo.GetCommentTarget(ctx, ref)
		if err != nil {
			continue
		}
		out = append(out, CommentSummary{Ref: ref, CommentCount: t.CommentCount, ReplyCount: t.ReplyCount, LastCommentAt: t.LastCommentAt})
	}
	return out, nil
}

func (s *Service) CommentAdminReconcile(ctx context.Context, target string) (int, error) {
	return s.ReconcileCommentCounts(ctx, target)
}
