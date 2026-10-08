package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/redis/go-redis/v9"
	"ludiskus/db"
	"ludiskus/internal/domain"
	"slices"
	"time"
)

type cachedPollPolicy struct {
	version string
	policy  domain.PollPolicy
	expires time.Time
}

func (s *Service) loadPollSeeds() {
	raw, e := db.Seeds.ReadFile("seeds/poll_policies.json")
	if e != nil {
		return
	}
	var seed struct {
		Policies []struct {
			Service string          `json:"service"`
			Types   []string        `json:"types"`
			Config  json.RawMessage `json:"config"`
		} `json:"policies"`
	}
	if json.Unmarshal(raw, &seed) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, p := range seed.Policies {
		for _, typ := range p.Types {
			_ = s.repo.PutPollPolicy(ctx, p.Service, typ, p.Config, nil, true)
		}
	}
}
func (s *Service) pollPolicy(ctx context.Context, t *domain.CommentTarget) (domain.PollPolicy, error) {
	svc, typ := "ludiskus", "standalone"
	if t != nil {
		svc, typ = t.ServiceCode, t.ResourceType
	}
	key := svc + ":" + typ
	version, cacheOK := "", true
	if s.redis != nil {
		var e error
		version, e = s.redis.Get(ctx, "poll:pol:v").Result()
		cacheOK = e == nil || errors.Is(e, redis.Nil)
	}
	s.policyMu.Lock()
	c, ok := s.pollPolicyCache[key]
	s.policyMu.Unlock()
	if ok && cacheOK && c.version == version && time.Now().Before(c.expires) {
		return restrictPollPolicy(c.policy, t), nil
	}
	overlays := []json.RawMessage{}
	found := false
	for _, name := range []string{"*", typ} {
		b, e := s.repo.GetPollPolicy(ctx, svc, name)
		if e == nil {
			overlays = append(overlays, b)
			found = true
		} else if !errors.Is(e, domain.ErrNotFound) {
			return domain.PollPolicy{}, e
		}
	}
	b, e := mergeJSONObjects(domain.DefaultPollPolicy(), overlays...)
	if e != nil {
		return domain.PollPolicy{}, e
	}
	var p domain.PollPolicy
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if !found {
		p.Enabled = false
	}
	if e = validatePollPolicy(p); e != nil {
		return p, e
	}
	s.policyMu.Lock()
	if s.pollPolicyCache == nil {
		s.pollPolicyCache = map[string]cachedPollPolicy{}
	}
	s.pollPolicyCache[key] = cachedPollPolicy{version, p, time.Now().Add(time.Minute)}
	s.policyMu.Unlock()
	return restrictPollPolicy(p, t), nil
}
func restrictPollPolicy(p domain.PollPolicy, t *domain.CommentTarget) domain.PollPolicy {
	if t == nil {
		return p
	}
	var c struct {
		Poll          *bool    `json:"poll"`
		PollCreate    string   `json:"pollCreate"`
		Kinds         []string `json:"pollKinds"`
		IdentityModes []string `json:"pollIdentityModes"`
		MaxOptions    *int     `json:"pollMaxOptions"`
	}
	_ = json.Unmarshal(t.Capabilities, &c)
	if c.Poll != nil {
		p.Enabled = p.Enabled && *c.Poll
	}
	if len(c.Kinds) > 0 {
		p.Kinds = intersectPollStrings(p.Kinds, c.Kinds)
	}
	if len(c.IdentityModes) > 0 {
		p.IdentityModes = intersectPollStrings(p.IdentityModes, c.IdentityModes)
	}
	if c.MaxOptions != nil {
		p.MaxOptions = min(p.MaxOptions, *c.MaxOptions)
	}
	if c.PollCreate != "" {
		allowed := map[string]int{"authenticated": 0, "participants": 0, "owner": 1, "staff": 2, "staff_only": 2, "none": 3}
		if allowed[c.PollCreate] > allowed[p.WhoCanCreate] {
			p.WhoCanCreate = c.PollCreate
		}
	}
	if t.ServiceCode == "ludiskus" && t.ResourceType == "comment" {
		var parent struct {
			Allowed bool `json:"pollAllowed"`
			Max     int  `json:"pollMaxPerComment"`
		}
		_ = json.Unmarshal(t.Capabilities, &parent)
		p.Enabled = p.Enabled && parent.Allowed
		if parent.Max > 0 {
			p.MaxPerAnchor = min(p.MaxPerAnchor, parent.Max)
		}
	}
	return p
}
func intersectPollStrings(a, b []string) []string {
	out := []string{}
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}
func validatePollPolicy(p domain.PollPolicy) error {
	if !slices.Contains([]string{"authenticated", "participants", "owner", "staff", "staff_only", "none"}, p.WhoCanCreate) || !slices.Contains([]string{"none", "post", "pre"}, p.ModerationMode) || p.MaxOptions < 2 || p.MaxOptions > 50 || p.MaxPerAnchor < 1 || p.MaxDurationDays < 1 || p.MaxDurationDays > 365 || p.RateLimit.CreatePerHour < 1 || p.RateLimit.VotesPerMinute < 1 {
		return domain.ErrValidation
	}
	for _, k := range p.Kinds {
		if !slices.Contains(domain.DefaultPollPolicy().Kinds, k) {
			return domain.ErrValidation
		}
	}
	for _, k := range p.IdentityModes {
		if !slices.Contains(domain.DefaultPollPolicy().IdentityModes, k) {
			return domain.ErrValidation
		}
	}
	return nil
}
func (s *Service) PutPollPolicy(ctx context.Context, svc, typ string, raw json.RawMessage, actor *string) error {
	if typ != "*" {
		if e := (domain.ResourceRef{Service: svc, Type: typ, ID: "policy"}).Validate(); e != nil {
			return e
		}
	}
	b, e := mergeJSONObjects(domain.DefaultPollPolicy(), raw)
	if e != nil {
		return domain.ErrValidation
	}
	var p domain.PollPolicy
	if json.Unmarshal(b, &p) != nil {
		return domain.ErrValidation
	}
	if e = validatePollPolicy(p); e != nil {
		return e
	}
	if e = s.repo.PutPollPolicy(ctx, svc, typ, raw, actor, false); e != nil {
		return e
	}
	s.policyMu.Lock()
	s.pollPolicyCache = map[string]cachedPollPolicy{}
	s.policyMu.Unlock()
	if s.redis != nil {
		_ = s.redis.Incr(ctx, "poll:pol:v").Err()
	}
	return nil
}
