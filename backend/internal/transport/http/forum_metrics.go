package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Labels contain only registered route patterns, never IDs, queries, or identities.
type forumMeasure struct {
	count   int64
	seconds float64
	errors  int64
	buckets [7]int64
}

var forumLatencyBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 1}

type forumMetrics struct {
	mu     sync.Mutex
	routes map[string]forumMeasure
	log    *slog.Logger
}

func newForumMetrics(log *slog.Logger) *forumMetrics {
	return &forumMetrics{routes: map[string]forumMeasure{}, log: log}
}
func (m *forumMetrics) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		path := chi.RouteContext(r.Context()).RoutePattern()
		if path == "" || strings.Contains(path, "comment") || strings.Contains(r.URL.Path, "/comment/") || strings.HasPrefix(path, "/metrics") {
			return
		}
		key := r.Method + " " + path
		elapsed := time.Since(start).Seconds()
		m.log.InfoContext(r.Context(), "forum request", "request_id", middleware.GetReqID(r.Context()), "method", r.Method, "route", path, "status", ww.Status(), "duration_ms", elapsed*1000)
		m.mu.Lock()
		v := m.routes[key]
		v.count++
		v.seconds += elapsed
		for i, b := range forumLatencyBuckets {
			if elapsed <= b {
				v.buckets[i]++
			}
		}
		if ww.Status() >= 500 {
			v.errors++
		}
		m.routes[key] = v
		m.mu.Unlock()
	})
}
func (m *forumMetrics) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Fprintln(w, "# TYPE ludiskus_forum_http_requests_total counter\n# TYPE ludiskus_forum_http_duration_seconds histogram\n# TYPE ludiskus_forum_http_errors_total counter")
	keys := make([]string, 0, len(m.routes))
	for k := range m.routes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m.routes[k]
		for i, b := range forumLatencyBuckets {
			fmt.Fprintf(w, "ludiskus_forum_http_duration_seconds_bucket{route=%q,le=%q} %d\n", k, fmt.Sprint(b), v.buckets[i])
		}
		fmt.Fprintf(w, "ludiskus_forum_http_duration_seconds_bucket{route=%q,le=\"+Inf\"} %d\nludiskus_forum_http_duration_seconds_count{route=%q} %d\n", k, v.count, k, v.count)
		fmt.Fprintf(w, "ludiskus_forum_http_requests_total{route=%q} %d\nludiskus_forum_http_duration_seconds_sum{route=%q} %g\nludiskus_forum_http_errors_total{route=%q} %d\n", k, v.count, k, v.seconds, k, v.errors)
	}
}
