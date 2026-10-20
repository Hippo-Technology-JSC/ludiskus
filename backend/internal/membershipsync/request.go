package membershipsync

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

type requestKey struct{}
type lookupKey struct {
	client *Client
	space  string
}
type result struct {
	members []Member
	err     error
	fresh   bool
	at      time.Time
}
type requestScope struct {
	mu      sync.Mutex
	fresh   bool
	results map[lookupKey]result
}

// RequestContext starts a new logical request scope. Fresh scopes verify once
// per Space per request; read scopes may reuse the process cache for 60 seconds.
// Background callers may create a fresh scope per job, never for a worker's life.
func RequestContext(ctx context.Context, fresh bool) context.Context {
	return context.WithValue(ctx, requestKey{}, &requestScope{fresh: fresh, results: map[lookupKey]result{}})
}

// Middleware must run before authorization middleware. Unsafe HTTP methods,
// WebSocket upgrades, exports, downloads and launch URLs require a current
// HipCore snapshot, even with a warm read cache. Public/health routes cause no HipCore traffic without a lookup.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fresh := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
		fresh = fresh || strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		fresh = fresh || strings.Contains(r.URL.Path, "/export") || strings.HasSuffix(r.URL.Path, "/download") || strings.HasSuffix(r.URL.Path, "/launch")
		next.ServeHTTP(w, r.WithContext(RequestContext(r.Context(), fresh)))
	})
}

// FreshMembers explicitly upgrades a read scope when a domain operation needs
// a fresh snapshot (e.g. forWrite resolvers or workflow approver selection).
func FreshMembers(ctx context.Context, space string) ([]Member, bool, error) {
	if current == nil {
		return nil, false, nil
	}
	ms, err := current.members(ctx, space, true)
	return ms, true, err
}

func (c *Client) members(ctx context.Context, space string, fresh bool) ([]Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scope, _ := ctx.Value(requestKey{}).(*requestScope)
	if scope == nil {
		// Unclassified callers fail safe: never authorize a background action from
		// a cached read, and never retain request results across unrelated jobs.
		ms, _, err := c.lookup(ctx, space, true)
		return ms, err
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	fresh = fresh || scope.fresh
	key := lookupKey{c, space}
	if r, ok := scope.results[key]; ok && time.Since(r.at) < maxAge && (!fresh || r.fresh) {
		return append([]Member(nil), r.members...), r.err
	}
	ms, verified, err := c.lookup(ctx, space, fresh)
	if err != nil {
		verified = time.Now()
	}
	scope.results[key] = result{members: ms, err: err, fresh: fresh, at: verified}
	return append([]Member(nil), ms...), err
}
