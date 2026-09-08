package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"ludiskus/internal/auth"
	transport "ludiskus/internal/transport/http"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"ludiskus/internal/config"
	"ludiskus/internal/database"
	"ludiskus/internal/domain"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/repository"
	"ludiskus/internal/resolver"
	"ludiskus/internal/service"
)

// Uses only a private schema in a *_test DB, fixture providers and optional
// dedicated Redis. No real messages, users or provider resources are modified.
func TestCommentAcceptance(t *testing.T) {
	dsn := os.Getenv("LUDISKUS_TEST_DSN")
	if dsn == "" {
		t.Skip("set LUDISKUS_TEST_DSN to a dedicated *_test database")
	}
	ctx := context.Background()
	pc, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test database")
	}
	admin, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := fmt.Sprintf("comment_acceptance_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	pc.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if e = database.Migrate(ctx, pool, log); e != nil {
		t.Fatal(e)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO profile_cache(profile_uuid,code,name,is_active,created_at) VALUES($1,'owner','Owner',true,now()-interval '1 year'),($2,'member','Member',true,now()-interval '1 year')`, owner, member)
	exec(`INSERT INTO comment_services(code,name,base_url,verify_mode,is_active) VALUES('fixture','Fixture','','trust',true)`)
	repo := repository.New(pool)
	cfg := &config.Config{CommentEnabled: true, CommentNewProfileHours: 24, CommentBatchMax: 100, CommentTargetTTL: time.Minute, CommentResolveTimeout: 100 * time.Millisecond, CommentNotifyDebounce: time.Millisecond, CommentPollInterval: 30 * time.Second, CacheTTL: time.Hour, OutboxMaxAttempts: 3}
	var rdb *redis.Client
	if u := os.Getenv("LUDISKUS_TEST_REDIS_URL"); u != "" {
		opt, e := redis.ParseURL(u)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(opt.Addr, "acceptance") {
			t.Fatal("Redis must be a dedicated acceptance container")
		}
		rdb = redis.NewClient(opt)
		defer rdb.Close()
		if e = rdb.Ping(ctx).Err(); e != nil {
			t.Fatal(e)
		}
		if e = rdb.FlushDB(ctx).Err(); e != nil {
			t.Fatal(e)
		}
	}
	ident := identity.New(repo, nil, cfg, log)
	newService := func() *service.Service { return service.New(repo, ident, nil, nil, markdown.New(), cfg, rdb) }
	svc := newService()
	policy := domain.DefaultCommentPolicy()
	policy.PublicRead = true
	policy.Mentions.Scope = "participants"
	raw, _ := json.Marshal(policy)
	if e = repo.UpsertCommentPolicy(ctx, "fixture", "*", raw, nil); e != nil {
		t.Fatal(e)
	}
	ownerType := "profile"
	target, e := repo.UpsertCommentTarget(ctx, domain.CommentTarget{ServiceCode: "fixture", ResourceType: "item", ResourceID: "one", OwnerType: &ownerType, OwnerID: ptr(owner), Title: "Fixture", CanonicalPath: "/fixture/one", Visibility: "public", State: "active", ThreadState: "open", Capabilities: json.RawMessage(`{}`)})
	if e != nil {
		t.Fatal(e)
	}
	insert := func(body, key string) *domain.Comment {
		t.Helper()
		c, _, e := repo.InsertComment(ctx, repository.InsertCommentInput{Comment: domain.Comment{TargetID: target.ID, AuthorKind: "profile", AuthorProfileUUID: ptr(member), BodyMD: body, BodyHTML: body, BodyHash: body, MarkdownMode: "basic", Status: "published", IdempotencyKey: &key}})
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	t.Run("concurrent_idempotency_and_counts", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		var created atomic.Int64
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, fresh, e := repo.InsertComment(ctx, repository.InsertCommentInput{Comment: domain.Comment{TargetID: target.ID, AuthorKind: "profile", AuthorProfileUUID: ptr(member), BodyMD: "same", BodyHTML: "same", BodyHash: "same", MarkdownMode: "basic", Status: "published", IdempotencyKey: ptr("same-request")}})
				errs <- e
				if fresh {
					created.Add(1)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		got, e := repo.GetCommentTargetByID(ctx, target.ID)
		if e != nil {
			t.Fatal(e)
		}
		if created.Load() != 1 || got.CommentCount != 1 || got.ParticipantCount != 1 {
			t.Fatalf("created=%d count=%d people=%d", created.Load(), got.CommentCount, got.ParticipantCount)
		}
	})
	t.Run("notification_atomic_failure_and_lease", func(t *testing.T) {
		c := insert("Notification", "notify")
		if e = repo.EnqueueCommentNotify(ctx, "ludiskus.comment.mentioned", owner, target.ID, c.ID, ptr(member), time.Now().Add(-time.Second)); e != nil {
			t.Fatal(e)
		}
		rows, e := repo.ClaimDueCommentNotify(ctx, 200)
		if e != nil || len(rows) != 1 {
			t.Fatalf("claim %v %d", e, len(rows))
		}
		exec(`CREATE FUNCTION fail_comment_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture outbox failure'; END $$; CREATE TRIGGER fail_comment_outbox BEFORE INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION fail_comment_outbox()`)
		e = repo.CompleteCommentNotify(ctx, []int64{rows[0].ID}, rows[0].ClaimToken, rows[0].EventType, "notify-key", []byte(`{}`), 3)
		if e == nil {
			t.Fatal("expected outbox failure")
		}
		var count int
		pool.QueryRow(ctx, `SELECT count(*) FROM comment_notify_buffer`).Scan(&count)
		if count != 1 {
			t.Fatal("notification lost on failure")
		}
		exec(`DROP TRIGGER fail_comment_outbox ON outbox; DROP FUNCTION fail_comment_outbox()`)
		exec(`UPDATE comment_notify_buffer SET claimed_at=now()-interval '6 minutes'`)
		next, e := repo.ClaimDueCommentNotify(ctx, 200)
		if e != nil || len(next) != 1 {
			t.Fatal("reclaim failed")
		}
		if e = repo.CompleteCommentNotify(ctx, []int64{rows[0].ID}, rows[0].ClaimToken, rows[0].EventType, "stale", []byte(`{}`), 3); !errors.Is(e, domain.ErrConflict) {
			t.Fatalf("stale worker accepted: %v", e)
		}
		if e = repo.CompleteCommentNotify(ctx, []int64{next[0].ID}, next[0].ClaimToken, next[0].EventType, "notify-key", []byte(`{}`), 3); e != nil {
			t.Fatal(e)
		}
		pool.QueryRow(ctx, `SELECT count(*) FROM comment_notify_buffer`).Scan(&count)
		if count != 0 {
			t.Fatal("buffer not drained")
		}
		pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE idempotency_key='notify-key'`).Scan(&count)
		if count != 1 {
			t.Fatal("outbox missing")
		}
	})
	t.Run("mention_created_in_transaction_and_flush", func(t *testing.T) {
		_, e := repo.UpsertCommentParticipant(ctx, domain.CommentParticipant{TargetID: target.ID, ProfileUUID: owner, Reason: "owner"})
		if e != nil {
			t.Fatal(e)
		}
		c, _, e := svc.CreateComment(ctx, target.Ref(), member, "mention-request", service.CreateCommentInput{BodyMD: "Xin chào @owner"})
		if e != nil {
			t.Fatal(e)
		}
		replayed, fresh, e := svc.CreateComment(ctx, target.Ref(), member, "mention-request", service.CreateCommentInput{BodyMD: "Xin chào @owner"})
		if e != nil || fresh || replayed.ID != c.ID {
			t.Fatalf("idempotent service retry %v fresh=%v", e, fresh)
		}
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM comment_notify_buffer WHERE comment_id=$1 AND event_type='ludiskus.comment.mentioned'`, c.ID).Scan(&n)
		if n != 1 {
			t.Fatalf("mention buffer=%d", n)
		}
		if _, e = svc.FlushCommentNotify(ctx); e != nil {
			t.Fatal(e)
		}
		pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE event_type='ludiskus.comment.mentioned' AND payload->'data'->>'url'=$1`, "/fixture/one#comment-"+c.ID).Scan(&n)
		if n != 1 {
			t.Fatal("mention outbox absent")
		}
	})
	t.Run("public_visibility_search_and_tombstone", func(t *testing.T) {
		c := insert("Từ khóa nghiệm thu", "search")
		if _, e := svc.PublicCommentThread(ctx, target.Ref()); e != nil {
			t.Fatal(e)
		}
		found, e := svc.SearchComments(ctx, target.Ref(), member, "nghiem")
		if e != nil || len(found) == 0 {
			t.Fatalf("search %v %d", e, len(found))
		}
		if _, e = repo.TransitionComment(ctx, c.ID, "hidden", owner, "fixture"); e != nil {
			t.Fatal(e)
		}
		page, e := svc.PublicCommentList(ctx, target.Ref(), "newest", "", 20, 3)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Data {
			if v.ID == c.ID && !v.Deleted {
				t.Fatal("hidden content exposed")
			}
		}
		defer exec(`UPDATE comment_targets SET visibility='public' WHERE id=$1`, target.ID)
		exec(`UPDATE comment_targets SET visibility='private' WHERE id=$1`, target.ID)
		if _, e = svc.PublicCommentThread(ctx, target.Ref()); e == nil {
			t.Fatal("private public-readable")
		}
		exec(`UPDATE comment_targets SET visibility='public' WHERE id=$1`, target.ID)
	})
	t.Run("policy_two_instances_and_new_profile_rate", func(t *testing.T) {
		if rdb == nil {
			t.Skip("requires dedicated LUDISKUS_TEST_REDIS_URL")
		}
		second := newService()
		before, e := second.CommentThread(ctx, target.Ref(), member)
		if e != nil {
			t.Fatal(e)
		}
		if !before.Capabilities.CanComment {
			t.Fatal("expected enabled")
		}
		disabled := policy
		disabled.Enabled = false
		raw, _ := json.Marshal(disabled)
		if _, e = svc.AdminPutCommentPolicy(ctx, "fixture", "*", raw, ptr(owner)); e != nil {
			t.Fatal(e)
		}
		after, e := second.CommentThread(ctx, target.Ref(), member)
		if e == nil && after.Capabilities.CanComment {
			t.Fatal("second instance retained stale policy")
		}
		raw, _ = json.Marshal(policy)
		if _, e = svc.AdminPutCommentPolicy(ctx, "fixture", "*", raw, ptr(owner)); e != nil {
			t.Fatal(e)
		}
		exec(`INSERT INTO profile_cache(profile_uuid,code,name,is_active,created_at) VALUES($1,'newbie','New',true,now())`, outsider)
		for i := 0; i < 3; i++ {
			_, _, e := svc.CreateComment(ctx, target.Ref(), outsider, fmt.Sprintf("new-%d", i), service.CreateCommentInput{BodyMD: fmt.Sprintf("New profile message %d", i)})
			if i < 2 && e != nil {
				t.Fatal(e)
			}
			if i == 2 && !errors.Is(e, domain.ErrRateLimited) {
				t.Fatalf("new profile rate: %v", e)
			}
		}
	})
	t.Run("batch_provider_and_failure", func(t *testing.T) {
		var requests atomic.Int64
		var unavailable atomic.Bool
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/oauth/token" {
				io.WriteString(w, `{"access_token":"fixture","expires_in":3600}`)
				return
			}
			requests.Add(1)
			if unavailable.Load() {
				w.WriteHeader(503)
				return
			}
			if !strings.HasSuffix(r.URL.Path, "resource-context:batch") {
				t.Errorf("unexpected provider path %s", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			var input struct {
				Refs []domain.ResourceRef `json:"refs"`
			}
			json.NewDecoder(r.Body).Decode(&input)
			values := []resolver.Result{}
			for _, ref := range input.Refs {
				values = append(values, resolver.Result{Exists: true, Type: ref.Type, ID: ref.ID, Visibility: "public", State: "active"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": values})
		}))
		defer upstream.Close()
		_, e := repo.UpsertCommentService(ctx, domain.CommentService{Code: "batchtest", Name: "Batch", BaseURL: upstream.URL, VerifyMode: "strict", IsActive: true})
		if e != nil {
			t.Fatal(e)
		}
		configCopy := *cfg
		configCopy.HipcoreURL = upstream.URL
		configCopy.HipcoreClientID = "fixture"
		configCopy.HipcoreClientSecret = "fixture"
		resolverClient := resolver.New(repo, nil, &configCopy)
		refs := []domain.ResourceRef{}
		for i := 0; i < 100; i++ {
			refs = append(refs, domain.ResourceRef{Service: "batchtest", Type: "item", ID: fmt.Sprint(i)})
		}
		got, failed := resolverClient.ResolveBatch(ctx, refs)
		if len(got) != 100 || len(failed) != 0 || requests.Load() != 1 {
			t.Fatalf("batch got=%d failures=%d requests=%d", len(got), len(failed), requests.Load())
		}
		unavailable.Store(true)
		_, failed = resolverClient.ResolveBatch(ctx, refs)
		if len(failed) != 100 || requests.Load() != 2 {
			t.Fatal("provider outage fan-out")
		}
	})
	t.Run("moderation_owner_and_atomic_approval", func(t *testing.T) {
		c, _, e := repo.InsertComment(ctx, repository.InsertCommentInput{Comment: domain.Comment{TargetID: target.ID, AuthorKind: "profile", AuthorProfileUUID: ptr(member), BodyMD: "Pending @owner", BodyHTML: "Pending", BodyHash: "pending", MarkdownMode: "basic", Status: "pending", IdempotencyKey: ptr("pending-request")}, MentionProfileUUIDs: []string{owner}, ModerationSource: "pre"})
		if e != nil {
			t.Fatal(e)
		}
		var itemID string
		if e = pool.QueryRow(ctx, `SELECT id FROM moderation_items WHERE target_id=$1 AND state='pending'`, c.ID).Scan(&itemID); e != nil {
			t.Fatal(e)
		}
		if e = svc.ApproveModeration(ctx, itemID, member); !errors.Is(e, domain.ErrForbidden) {
			t.Fatalf("member approved %v", e)
		}
		exec(`CREATE FUNCTION fail_comment_buffer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture buffer failure'; END $$; CREATE TRIGGER fail_comment_buffer BEFORE INSERT ON comment_notify_buffer FOR EACH ROW EXECUTE FUNCTION fail_comment_buffer()`)
		if e = svc.ApproveModeration(ctx, itemID, owner); e == nil {
			t.Fatal("expected failed approval")
		}
		var status, state string
		pool.QueryRow(ctx, `SELECT c.status,m.state FROM comments c JOIN moderation_items m ON m.target_id=c.id WHERE m.id=$1`, itemID).Scan(&status, &state)
		if status != "pending" || state != "pending" {
			t.Fatalf("non-atomic approval comment=%s queue=%s", status, state)
		}
		exec(`DROP TRIGGER fail_comment_buffer ON comment_notify_buffer; DROP FUNCTION fail_comment_buffer()`)
		if e = svc.ApproveModeration(ctx, itemID, owner); e != nil {
			t.Fatal(e)
		}
		if e = svc.ApproveModeration(ctx, itemID, owner); !errors.Is(e, domain.ErrNotFound) {
			t.Fatalf("repeat approve %v", e)
		}
	})
	t.Run("http_public_guards_and_10000_root_pagination", func(t *testing.T) {
		exec(`WITH seeded AS (SELECT gen_random_uuid() id,n FROM generate_series(1,10000) n)
   INSERT INTO comments(id,target_id,root_id,author_kind,author_profile_uuid,body_md,body_html,body_hash,markdown_mode,status,created_at)
   SELECT id,$1,id,'profile',$2,'Load '||n,'Load '||n,'load-'||n,'basic','published',now()-interval '1 day'+n*interval '1 millisecond' FROM seeded`, target.ID, member)
		h := transport.NewRouter(svc, auth.NewAuthenticator(nil, "", "", log).WithGateway(secret), log)
		path := "/api/v1/public/comments/r/fixture/item/one/items"
		call := func(method, path string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			return w
		}
		if w := call("POST", path); w.Code != 405 {
			t.Fatalf("public write %d", w.Code)
		}
		if w := call("GET", "/api/v1/comments/r/fixture/item/one/items"); w.Code != 401 {
			t.Fatalf("private API without auth %d", w.Code)
		}
		latencies := []time.Duration{}
		cursor := ""
		seen := map[string]bool{}
		for i := 0; i < 20; i++ {
			start := time.Now()
			w := call("GET", path+"?limit=20&cursor="+cursor)
			latencies = append(latencies, time.Since(start))
			if w.Code != 200 {
				t.Fatalf("list %d %s", w.Code, w.Body.String())
			}
			var page service.CommentPage
			if e := json.Unmarshal(w.Body.Bytes(), &page); e != nil {
				t.Fatal(e)
			}
			if len(page.Data) != 20 {
				t.Fatalf("page size %d", len(page.Data))
			}
			for _, c := range page.Data {
				if seen[c.ID] {
					t.Fatal("duplicate keyset item")
				}
				seen[c.ID] = true
			}
			cursor = page.NextCursor
			if cursor == "" {
				t.Fatal("missing cursor")
			}
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		t.Logf("10k roots: HTTP handler + real PostgreSQL page p95=%s (not BFF/browser load)", latencies[18])
	})
	t.Run("redis_unavailable_fail_open", func(t *testing.T) {
		if rdb == nil {
			t.Skip("requires dedicated Redis")
		}
		rdb.Close()
		_, _, e := svc.CreateComment(ctx, target.Ref(), member, "redis-down", service.CreateCommentInput{BodyMD: "Still works while Redis unavailable"})
		if e != nil {
			t.Fatal(e)
		}
	})

}
func ptr[T any](v T) *T { return &v }
