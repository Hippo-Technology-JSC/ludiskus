package service

import (
	"errors"
	"fmt"
	"ludiskus/internal/domain"
	"strings"
	"sync"
	"time"
)

type pollMetric struct {
	Count   int64
	Seconds float64
	Buckets [7]int64
}
type pollMetricStore struct {
	mu     sync.Mutex
	values map[string]pollMetric
}

var pollMetricBounds = []float64{.01, .025, .05, .08, .15, .3, 1}

func (s *Service) observePoll(name, labels string, start time.Time) {
	s.pollMetrics.mu.Lock()
	defer s.pollMetrics.mu.Unlock()
	if s.pollMetrics.values == nil {
		s.pollMetrics.values = map[string]pollMetric{}
	}
	key := name + labels
	v := s.pollMetrics.values[key]
	v.Count++
	elapsed := time.Since(start).Seconds()
	v.Seconds += elapsed
	for i, b := range pollMetricBounds {
		if elapsed <= b {
			v.Buckets[i]++
		}
	}
	s.pollMetrics.values[key] = v
}
func pollVoteOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, domain.ErrRateLimited) {
		return "rate"
	}
	if e, ok := err.(*domain.PollError); ok {
		switch e.Code {
		case "POLL_CLOSED", "POLL_NOT_OPEN":
			return "closed"
		case "ALREADY_VOTED":
			return "already"
		case "RATE_LIMITED":
			return "rate"
		}
	}
	return "invalid"
}
func (s *Service) PollMetricsText() string {
	s.pollMetrics.mu.Lock()
	defer s.pollMetrics.mu.Unlock()
	var b strings.Builder
	for key, v := range s.pollMetrics.values {
		name, labels := key, ""
		if i := strings.Index(key, "{"); i >= 0 {
			name, labels = key[:i], key[i:]
		}
		if strings.HasSuffix(name, "_total") {
			fmt.Fprintf(&b, "%s%s %d\n", name, labels, v.Count)
			continue
		}
		fmt.Fprintf(&b, "%s_count%s %d\n%s_sum%s %g\n", name, labels, v.Count, name, labels, v.Seconds)
		for i, bound := range pollMetricBounds {
			with := fmt.Sprintf("{le=%q}", fmt.Sprint(bound))
			if labels != "" {
				with = strings.TrimSuffix(labels, "}") + fmt.Sprintf(",le=%q}", fmt.Sprint(bound))
			}
			fmt.Fprintf(&b, "%s_bucket%s %d\n", name, with, v.Buckets[i])
		}
		with := "{le=\"+Inf\"}"
		if labels != "" {
			with = strings.TrimSuffix(labels, "}") + ",le=\"+Inf\"}"
		}
		fmt.Fprintf(&b, "%s_bucket%s %d\n", name, with, v.Count)
	}
	return b.String()
}
