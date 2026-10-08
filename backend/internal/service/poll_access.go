package service

import (
	"context"
	"ludiskus/internal/auth"
	"ludiskus/internal/domain"
	"time"
)

type pollViewer struct {
	Profile string
	Service string
	Public  bool
}

func (s *Service) PollEnabled() bool { return s.cfg.PollEnabled }
func (s *Service) pollRoles(ctx context.Context, p *domain.Poll, v pollViewer) (creator, owner, moderator, svc bool) {
	svc = p.Target != nil && v.Service != "" && v.Service == p.Target.ServiceCode
	creator = p.CreatedBy != nil && v.Profile != "" && *p.CreatedBy == v.Profile
	creator = creator || p.SourceService != nil && svc && *p.SourceService == v.Service
	if p.AuthorKind == "space" && p.AuthorSpaceUUID != nil && v.Profile != "" {
		r := s.role(ctx, *p.AuthorSpaceUUID, v.Profile)
		creator = r == domain.RoleOwner || r == domain.RoleAdmin
	}
	if p.Target != nil && p.Target.OwnerID != nil {
		if p.Target.OwnerType != nil && *p.Target.OwnerType == "space" {
			r := s.role(ctx, *p.Target.OwnerID, v.Profile)
			owner = r == domain.RoleOwner || r == domain.RoleAdmin
		} else {
			owner = v.Profile != "" && *p.Target.OwnerID == v.Profile
		}
	}
	moderator = auth.IsSuperuser(ctx)
	if space := p.Space(); space != "" && v.Profile != "" {
		moderator = moderator || canModerate(s.role(ctx, space, v.Profile))
	}
	return
}
func (s *Service) ensurePollReadable(ctx context.Context, id string, v pollViewer) (*domain.Poll, domain.PollPolicy, error) {
	empty := domain.PollPolicy{}
	if !s.cfg.PollEnabled {
		return nil, empty, domain.PollFailure("POLL_NOT_FOUND", 404, "")
	}
	p, e := s.repo.GetPoll(ctx, id)
	if e != nil {
		return nil, empty, e
	}
	if p.Status == "deleted" {
		return nil, empty, domain.PollFailure("POLL_DELETED", 410, "")
	}
	if p.Target != nil {
		if v.Service != "" && v.Service != p.Target.ServiceCode {
			return nil, empty, domain.ErrServiceScope
		}
		t, e := s.ensureCommentTarget(ctx, p.Target.Ref(), v.Profile)
		if e != nil {
			return nil, empty, e
		}
		p.Target = t
		if t.State == "gone" {
			return nil, empty, domain.ErrResourceGone
		}
		if t.State == "blocked" {
			return nil, empty, domain.ErrResourceBlocked
		}
		if t.State == "unverified" {
			if p.CreatedBy == nil || *p.CreatedBy != v.Profile {
				return nil, empty, domain.ErrNotFound
			}
		} else if v.Service == "" {
			if e = s.ensureTargetReadable(ctx, t, v.Profile); e != nil {
				return nil, empty, e
			}
		}
	}
	creator, owner, mod, svc := s.pollRoles(ctx, p, v)
	if p.Target == nil {
		if v.Service != "" {
			return nil, empty, domain.ErrServiceScope
		}
		allowed := false
		if p.Visibility != nil {
			switch *p.Visibility {
			case "public", "unlisted":
				allowed = true
			case "authenticated":
				allowed = v.Profile != ""
			case "space":
				allowed = p.SpaceUUID != nil && s.ident.IsMember(ctx, *p.SpaceUUID, v.Profile)
			case "private":
				allowed = creator
				if !allowed && v.Profile != "" {
					allowed, e = s.repo.PollInvited(ctx, id, v.Profile)
					if e != nil {
						return nil, empty, e
					}
				}
			}
		}
		if !allowed && !creator && !mod {
			return nil, empty, domain.PollFailure("POLL_NOT_FOUND", 404, "")
		}
	}
	if (p.Status == "draft" || p.Status == "pending" || p.Status == "rejected") && !creator && !svc && !(p.Status == "pending" && mod) {
		return nil, empty, domain.PollFailure("POLL_NOT_FOUND", 404, "")
	}
	if p.Status == "hidden" && !owner && !mod && !svc {
		return nil, empty, domain.PollFailure("POLL_NOT_FOUND", 404, "")
	}
	policy, e := s.pollPolicy(ctx, p.Target)
	if e != nil {
		return nil, policy, e
	}
	if !policy.Enabled {
		return nil, policy, domain.PollFailure("POLL_DISABLED", 403, "")
	}
	if v.Public {
		state := p.EffectiveState(time.Now())
		visible := p.Target != nil && p.Target.State == "active" && p.Target.Visibility == "public" || p.Target == nil && p.Visibility != nil && (*p.Visibility == "public" || *p.Visibility == "unlisted")
		if !visible || !policy.PublicRead || (state != "open" && state != "scheduled" && state != "closed") {
			return nil, policy, domain.PollFailure("POLL_NOT_FOUND", 404, "")
		}
	}
	return p, policy, nil
}
func (s *Service) ensurePollVotable(ctx context.Context, p *domain.Poll, policy domain.PollPolicy, profile string) error {
	if profile == "" {
		return domain.ErrUnauthorized
	}
	state := p.EffectiveState(time.Now())
	if state == "scheduled" {
		return domain.PollFailure("POLL_NOT_OPEN", 409, "")
	}
	if state != "open" {
		return domain.PollFailure("POLL_CLOSED", 409, "")
	}
	if p.Target != nil && p.Target.State != "active" {
		return domain.PollFailure("ANCHOR_UNVERIFIED", 409, "")
	}
	switch p.WhoCanVote {
	case "members":
		if p.Space() == "" || !s.ident.IsMember(ctx, p.Space(), profile) {
			return domain.PollFailure("NOT_ELIGIBLE", 403, "")
		}
	case "invited":
		yes, e := s.repo.PollInvited(ctx, p.ID, profile)
		if e != nil {
			return e
		}
		if !yes {
			return domain.PollFailure("NOT_ELIGIBLE", 403, "")
		}
	}
	if !policy.NewProfileCanVote {
		if pr, e := s.ident.Profile(ctx, profile); e == nil && pr.CreatedAt != nil && time.Since(*pr.CreatedAt) < time.Duration(s.cfg.PollNewProfileHours)*time.Hour {
			return domain.PollFailure("NOT_ELIGIBLE", 403, "")
		}
	}
	return nil
}
func resultsVisible(p *domain.Poll, creator, svc, hasVoted bool, now time.Time) (bool, string) {
	closed := p.EffectiveState(now) == "closed"
	allowed := creator || svc
	reason := p.ResultsVisibility
	switch p.ResultsVisibility {
	case "always":
		allowed = true
	case "after_vote":
		allowed = allowed || hasVoted || closed
	case "after_close":
		allowed = allowed || closed
	}
	if !allowed {
		return false, reason
	}
	if !closed && (p.IdentityMode == "anonymous" || p.IdentityMode == "secret") && p.VoterCount < p.MinVotersForResults {
		return false, "min_voters"
	}
	return true, ""
}
func (s *Service) ensurePollCreatable(ctx context.Context, t *domain.CommentTarget, space string, v pollViewer) (domain.PollPolicy, error) {
	p, e := s.pollPolicy(ctx, t)
	if e != nil {
		return p, e
	}
	if !p.Enabled {
		return p, domain.PollFailure("POLL_DISABLED", 403, "")
	}
	if v.Service != "" {
		if t == nil || t.ServiceCode != v.Service {
			return p, domain.ErrServiceScope
		}
		return p, nil
	}
	if v.Profile == "" {
		return p, domain.ErrUnauthorized
	}
	if t != nil {
		if t.ServiceCode == "ludiskus" && t.ResourceType == "poll" {
			return p, domain.PollFailure("ANCHOR_NOT_ALLOWED", 422, "anchor")
		}
		if e = s.ensureTargetReadable(ctx, t, v.Profile); e != nil && t.State != "unverified" {
			return p, e
		}
		owner, mod := false, false
		fake := &domain.Poll{Target: t}
		_, owner, mod, _ = s.pollRoles(ctx, fake, v)
		switch p.WhoCanCreate {
		case "owner":
			if !owner {
				return p, domain.ErrForbidden
			}
		case "staff", "staff_only":
			if !mod {
				return p, domain.ErrForbidden
			}
		case "none":
			return p, domain.ErrForbidden
		}
	} else if space != "" {
		if !s.ident.IsMember(ctx, space, v.Profile) {
			return p, domain.ErrForbidden
		}
		if (p.WhoCanCreate == "staff" || p.WhoCanCreate == "staff_only") && !canModerate(s.role(ctx, space, v.Profile)) {
			return p, domain.ErrForbidden
		}
		if f, e := s.repo.GetForum(ctx, space); e == nil {
			var setting struct {
				Poll struct {
					Enabled *bool `json:"enabled"`
				} `json:"poll"`
			}
			_ = unmarshalPoll(f.Settings, &setting)
			if setting.Poll.Enabled != nil && !*setting.Poll.Enabled {
				return p, domain.PollFailure("POLL_DISABLED", 403, "")
			}
		}
	}
	return p, nil
}
