package service

import (
	"context"
	"ludiskus/internal/repository"
)

func (s *Service) PollPolicies(ctx context.Context) ([]repository.PollPolicyRow, error) {
	return s.repo.ListPollPolicies(ctx)
}
func (s *Service) PollAbuseFlags(ctx context.Context) ([]map[string]any, error) {
	return s.repo.PollAbuseFlags(ctx)
}
