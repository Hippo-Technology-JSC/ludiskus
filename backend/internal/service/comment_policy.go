package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ludiskus/internal/domain"
)

type cachedCommentPolicy struct {
	policy  domain.CommentPolicy
	expires time.Time
}

func mergeJSONObjects(base any, overlays ...json.RawMessage) ([]byte, error) {
	raw, _ := json.Marshal(base)
	var dst map[string]any
	if err := json.Unmarshal(raw, &dst); err != nil {
		return nil, err
	}
	var merge func(map[string]any, map[string]any)
	merge = func(a, b map[string]any) {
		for k, v := range b {
			if bm, ok := v.(map[string]any); ok {
				if am, ok := a[k].(map[string]any); ok {
					merge(am, bm)
					continue
				}
			}
			a[k] = v
		}
	}
	for _, overlay := range overlays {
		if len(overlay) == 0 {
			continue
		}
		var src map[string]any
		if err := json.Unmarshal(overlay, &src); err != nil {
			return nil, err
		}
		merge(dst, src)
	}
	return json.Marshal(dst)
}

func (s *Service) commentPolicy(ctx context.Context, t *domain.CommentTarget) (domain.CommentPolicy, error) {
	key := t.ServiceCode + ":" + t.ResourceType
	s.policyMu.Lock()
	if c, ok := s.policyCache[key]; ok && time.Now().Before(c.expires) {
		s.policyMu.Unlock()
		return restrictPolicy(c.policy, t.Capabilities), nil
	}
	s.policyMu.Unlock()
	overlays := []json.RawMessage{}
	found := false
	if raw, err := s.repo.GetCommentPolicy(ctx, t.ServiceCode, "*"); err == nil {
		overlays = append(overlays, raw)
		found = true
	}
	if raw, err := s.repo.GetCommentPolicy(ctx, t.ServiceCode, t.ResourceType); err == nil {
		overlays = append(overlays, raw)
		found = true
	}
	raw, err := mergeJSONObjects(domain.DefaultCommentPolicy(), overlays...)
	if err != nil {
		return domain.CommentPolicy{}, err
	}
	var p domain.CommentPolicy
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if !found {
		p.Enabled = false
	}
	if err := validateCommentPolicy(p); err != nil {
		return p, err
	}
	s.policyMu.Lock()
	s.policyCache[key] = cachedCommentPolicy{policy: p, expires: time.Now().Add(time.Minute)}
	s.policyMu.Unlock()
	return restrictPolicy(p, t.Capabilities), nil
}

func validateCommentPolicy(p domain.CommentPolicy) error {
	if p.MaxDepth < 0 || p.MaxDepth > 5 || p.MinLength < 0 || p.MaxLength < p.MinLength || p.MaxLength > 20000 {
		return fmt.Errorf("%w: giới hạn policy không hợp lệ", domain.ErrValidation)
	}
	if p.Guest {
		return fmt.Errorf("%w: bình luận khách chưa được hỗ trợ", domain.ErrValidation)
	}
	if !map[string]bool{"authenticated": true, "members": true, "owner_only": true, "staff_only": true}[p.WhoCanComment] {
		return fmt.Errorf("%w: who_can_comment không hợp lệ", domain.ErrValidation)
	}
	if !map[string]bool{"plain": true, "basic": true, "rich": true}[p.Markdown] {
		return fmt.Errorf("%w: markdown không hợp lệ", domain.ErrValidation)
	}
	if !map[string]bool{"none": true, "post": true, "pre": true, "first_comment": true}[p.ModerationMode] {
		return fmt.Errorf("%w: moderation_mode không hợp lệ", domain.ErrValidation)
	}
	return nil
}

type resourceCaps struct {
	Comment    *bool `json:"comment"`
	Attach     *bool `json:"attach"`
	Mention    *bool `json:"mention"`
	MaxDepth   *int  `json:"maxDepth"`
	MaxLength  *int  `json:"maxLength"`
	PublicRead *bool `json:"publicRead"`
}

func restrictPolicy(p domain.CommentPolicy, raw json.RawMessage) domain.CommentPolicy {
	var c resourceCaps
	if len(raw) == 0 || json.Unmarshal(raw, &c) != nil {
		return p
	}
	if c.Comment != nil {
		p.Enabled = p.Enabled && *c.Comment
	}
	if c.Attach != nil {
		p.Attachments.Enabled = p.Attachments.Enabled && *c.Attach
	}
	if c.Mention != nil {
		p.Mentions.Enabled = p.Mentions.Enabled && *c.Mention
	}
	if c.MaxDepth != nil && *c.MaxDepth < p.MaxDepth {
		p.MaxDepth = max(0, *c.MaxDepth)
	}
	if c.MaxLength != nil && *c.MaxLength < p.MaxLength {
		p.MaxLength = max(p.MinLength, *c.MaxLength)
	}
	if c.PublicRead != nil {
		p.PublicRead = p.PublicRead && *c.PublicRead
	}
	return p
}

func (s *Service) commentCapabilities(ctx context.Context, t *domain.CommentTarget, profileUUID string, p domain.CommentPolicy) domain.CommentCapabilities {
	owner := t.OwnerID != nil && *t.OwnerID == profileUUID
	moderator := owner
	member := false
	if t.SpaceUUID != nil && profileUUID != "" {
		member = s.ident.IsMember(ctx, *t.SpaceUUID, profileUUID)
		moderator = moderator || canModerate(s.role(ctx, *t.SpaceUUID, profileUUID))
	}
	canComment := profileUUID != "" && p.Enabled
	switch p.WhoCanComment {
	case "members":
		canComment = canComment && member
	case "owner_only":
		canComment = canComment && owner
	case "staff_only":
		canComment = canComment && moderator
	}
	if t.ThreadState != "open" || t.State == "gone" || t.State == "blocked" {
		canComment = false
	}
	reasons := map[string]string{}
	if !canComment {
		reason := "policy_disabled"
		if profileUUID == "" {
			reason = "not_authenticated"
		} else if t.ThreadState != "open" {
			reason = "thread_locked"
		} else if t.State == "gone" {
			reason = "resource_gone"
		} else if p.WhoCanComment == "members" && !member {
			reason = "not_member"
		}
		reasons["canComment"] = reason
	}
	if !p.Attachments.Enabled {
		reasons["canAttach"] = "policy_disabled"
	}
	return domain.CommentCapabilities{CanRead: p.Enabled, CanComment: canComment, CanReply: canComment && p.MaxDepth > 0,
		CanAttach: canComment && p.Attachments.Enabled, CanMention: canComment && p.Mentions.Enabled,
		CanPin: moderator && p.Pin.Enabled, CanModerate: moderator, MaxDepth: p.MaxDepth, MaxLength: p.MaxLength,
		Markdown: p.Markdown, EditWindowMinutes: p.EditWindowMinutes, SortOptions: []string{"newest", "oldest", "top"},
		Interaction: p.Interaction, Reasons: reasons}
}

func (s *Service) invalidateCommentPolicyCache(ctx context.Context) {
	s.policyMu.Lock()
	s.policyCache = map[string]cachedCommentPolicy{}
	s.policyMu.Unlock()
	if s.redis != nil {
		_ = s.redis.Incr(ctx, "cmt:pol:v").Err()
	}
}
