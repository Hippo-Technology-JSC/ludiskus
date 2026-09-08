package acceptance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
	"ludiskus/db"
	"ludiskus/internal/auth"
	"ludiskus/internal/config"
	"ludiskus/internal/database"
	"ludiskus/internal/domain"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/notify"
	"ludiskus/internal/repository"
	"ludiskus/internal/service"
	"ludiskus/internal/storage"
	transport "ludiskus/internal/transport/http"
)

const space = "10000000-0000-4000-8000-000000000001"
const owner = "20000000-0000-4000-8000-000000000001"
const member = "20000000-0000-4000-8000-000000000002"
const outsider = "20000000-0000-4000-8000-000000000003"
const moderator = "20000000-0000-4000-8000-000000000004"
const board = "30000000-0000-4000-8000-000000000001"
const secret = "isolated-forum-acceptance-secret"

// Opt-in real PostgreSQL tests. A dedicated *_test database and a fresh schema
// are mandatory; no shared application schema or recipient is used.
func TestForumAcceptance(t *testing.T) {
	dsn := os.Getenv("LUDISKUS_TEST_DSN")
	if dsn == "" {
		t.Skip("set LUDISKUS_TEST_DSN to a dedicated *_test database")
	}
	pc, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test database")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	for _, ext := range []string{"pgcrypto", "unaccent", "pg_trgm"} {
		if _, err = admin.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS "+ext); err != nil {
			t.Fatal(err)
		}
	}
	schema := fmt.Sprintf("forum_acceptance_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	pc.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err = database.Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO space_cache(space_uuid,name,is_public,creator_profile_uuid) VALUES($1,'Forum acceptance',false,$2)`, space, owner)
	for i, id := range []string{owner, member, outsider, moderator} {
		exec(`INSERT INTO profile_cache(profile_uuid,code,name) VALUES($1,$2,$3)`, id, fmt.Sprintf("tester%d", i), fmt.Sprintf("Tester %d", i))
	}
	for id, role := range map[string]string{owner: "owner", member: "member", moderator: "moderator"} {
		exec(`INSERT INTO space_member_cache(space_uuid,profile_uuid,role) VALUES($1,$2,$3)`, space, id, role)
	}
	exec(`INSERT INTO space_forums(space_uuid,moderation_mode) VALUES($1,'none')`, space)
	exec(`INSERT INTO boards(id,space_uuid,code,name,kind) VALUES($1,$2,'support','Support','support')`, board, space)
	cfg := &config.Config{CacheTTL: time.Hour, MaxAttachments: 8, MaxFileBytes: 1024 * 1024, OutboxMaxAttempts: 3, PresignTTL: time.Minute, AllowedMIME: []string{"text/plain", "image/png"}}
	// Only storage credentials are imported from runtime. All identity/notification
	// clients below remain isolated fixtures, never the application's recipients.
	var store *storage.Store
	if os.Getenv("LUDISKUS_TEST_MINIO") == "1" {
		live, e := config.Load()
		if e != nil {
			t.Fatal(e)
		}
		cfg.S3Endpoint = live.S3Endpoint
		cfg.S3PublicEndpoint = live.S3Endpoint
		cfg.S3AccessKey = live.S3AccessKey
		cfg.S3SecretKey = live.S3SecretKey
		cfg.S3Bucket = "ludiskus-forum-acceptance"
		store, e = storage.New(cfg)
		if e != nil {
			t.Fatal(e)
		}
		if e = store.EnsureBucket(ctx); e != nil {
			t.Fatal(e)
		}
	}
	var delivered atomic.Int64
	var reject atomic.Bool
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"access_token":"fixture","expires_in":3600}`)
			return
		}
		if reject.Load() {
			w.WriteHeader(503)
			return
		}
		delivered.Add(1)
		w.WriteHeader(202)
	}))
	defer downstream.Close()
	cfg.HipcoreURL = downstream.URL
	cfg.LunotiAPIURL = downstream.URL
	cfg.LunotiClientID = "fixture"
	cfg.LunotiClientSecret = "fixture"
	repo := repository.New(pool)
	ident := identity.New(repo, nil, cfg, log)
	svc := service.New(repo, ident, store, notify.New(cfg), markdown.New(), cfg, nil)
	handler := transport.NewRouter(svc, auth.NewAuthenticator(nil, "", "", log).WithGateway(secret), log)
	call := func(method, path, profile string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if profile != "" {
			ts := fmt.Sprint(time.Now().Unix())
			r.Header.Set("X-Gw-User-Id", "fixture")
			r.Header.Set("X-Gw-Profile-Uuid", profile)
			r.Header.Set("X-Gw-Issued-At", ts)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte(strings.Join([]string{"v1", "fixture", "", "", profile, ts}, "\n")))
			r.Header.Set("X-Gw-Signature", hex.EncodeToString(mac.Sum(nil)))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
		}
	}
	var topic domain.Topic
	createTopic := func(profile, title, body, typ string) domain.Topic {
		t.Helper()
		w := call("POST", "/api/v1/boards/"+board+"/topics", profile, map[string]any{"title": title, "bodyMd": body, "type": typ, "tags": []string{"golang"}})
		if w.Code != 201 && w.Code != 202 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var v struct {
			Data domain.Topic `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &v)
		return v.Data
	}
	reply := func(id, profile, body string, parent *string) domain.Post {
		t.Helper()
		w := call("POST", "/api/v1/topics/"+id+"/posts", profile, map[string]any{"bodyMd": body, "replyToId": parent})
		if w.Code != 201 && w.Code != 202 {
			t.Fatalf("reply: %d %s", w.Code, w.Body.String())
		}
		var v struct {
			Data domain.Post `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &v)
		return v.Data
	}
	t.Run("migration_up_down_up_with_data", func(t *testing.T) {
		topic = createTopic(owner, "Câu hỏi kiểm thử", "Nội dung mở đầu độc đáo", "question")
		for _, suffix := range []string{"down", "up"} {
			raw, e := db.Migrations.ReadFile("migrations/0011_forum_completion." + suffix + ".sql")
			if e != nil {
				t.Fatal(e)
			}
			exec(string(raw))
		}
		var title string
		if e := pool.QueryRow(ctx, `SELECT title FROM topics WHERE id=$1`, topic.ID).Scan(&title); e != nil || title != "Câu hỏi kiểm thử" {
			t.Fatalf("data lost: %s %v", title, e)
		}
	})
	t.Run("search_body_filters_and_visibility", func(t *testing.T) {
		p := reply(topic.ID, member, "Từ khoá trong trả lời: lạc đà đặc biệt", nil)
		for _, path := range []string{"?q=lac+da", "?q=lac+da&kind=post&tag=golang&author=" + member + "&type=question&space=" + space, "?q=mo+dau"} {
			w := call("GET", "/api/v1/search"+path, member, nil)
			expect(w, 200)
			if !strings.Contains(w.Body.String(), topic.ID) {
				t.Fatalf("missing search match %s", w.Body.String())
			}
		}
		w := call("GET", "/api/v1/search?q=lac+da", outsider, nil)
		expect(w, 200)
		if strings.Contains(w.Body.String(), p.ID) {
			t.Fatal("private search leak")
		}
		expect(call("GET", "/api/v1/search?q=x&status=pending&space="+space, member, nil), 403)
		expect(call("GET", "/api/v1/search?q=x&from=bad", member, nil), 422)
		expect(call("GET", "/api/v1/search?q=x&author=bad", member, nil), 422)
		expect(call("GET", "/api/v1/search?q=x&from=2026-09-08&until=2026-09-07", member, nil), 422)
		exec(`UPDATE topics SET status='hidden' WHERE id=$1`, topic.ID)
		expect(call("GET", "/api/v1/topics/"+topic.ID+"/posts", member, nil), 404)
		w = call("GET", "/api/v1/search?q=lac+da", owner, nil)
		if strings.Contains(w.Body.String(), p.ID) {
			t.Fatal("hidden post leak")
		}
		expect(call("GET", "/api/v1/search?q=x&status=hidden&space="+space, moderator, nil), 200)
		exec(`UPDATE topics SET status='published' WHERE id=$1`, topic.ID)
	})
	t.Run("thread_qa_support_and_auth", func(t *testing.T) {
		expect(call("GET", "/api/v1/healthz", member, nil), 200)
		expect(call("GET", "/api/v1/topics/"+topic.ID, "", nil), 401)
		expect(call("GET", "/api/v1/topics/"+topic.ID, outsider, nil), 403)
		a := reply(topic.ID, member, "Bài trả lời cấp một @tester0", nil)
		b := reply(topic.ID, owner, "Bài trả lời cấp hai", &a.ID)
		if b.ReplyToID == nil || *b.ReplyToID != a.ID {
			t.Fatal("lost reply parent")
		}
		other := createTopic(owner, "Support task", "Opening support", "discussion")
		expect(call("POST", "/api/v1/topics/"+other.ID+"/posts", member, map[string]any{"bodyMd": "cross topic", "replyToId": a.ID}), 422)
		expect(call("POST", "/api/v1/posts/"+a.ID+"/answer", member, nil), 403)
		expect(call("POST", "/api/v1/posts/"+a.ID+"/answer", owner, nil), 204)
		w := call("GET", "/api/v1/boards/"+board+"/topics?sort=unanswered", owner, nil)
		if strings.Contains(w.Body.String(), topic.ID) {
			t.Fatal("answered topic in unanswered")
		}
		expect(call("POST", "/api/v1/topics/"+topic.ID+"/reopen", owner, nil), 204)
		w = call("GET", "/api/v1/boards/"+board+"/topics?sort=unanswered", owner, nil)
		if !strings.Contains(w.Body.String(), topic.ID) {
			t.Fatal("unanswered question with replies excluded")
		}
		expect(call("PUT", "/api/v1/topics/"+other.ID+"/assignee", member, map[string]string{"assigneeProfileUuid": owner}), 403)
		expect(call("PUT", "/api/v1/topics/"+other.ID+"/assignee", owner, map[string]string{"assigneeProfileUuid": outsider}), 422)
		expect(call("PUT", "/api/v1/topics/"+other.ID+"/assignee", moderator, map[string]string{"assigneeProfileUuid": member}), 204)
		expect(call("POST", "/api/v1/topics/"+other.ID+"/resolve", owner, nil), 204)
		expect(call("POST", "/api/v1/topics/"+other.ID+"/reopen", owner, nil), 204)
		expect(call("POST", "/api/v1/topics/"+topic.ID+"/lock", moderator, nil), 204)
		expect(call("GET", "/api/v1/topics/"+topic.ID, member, nil), 200)
		expect(call("POST", "/api/v1/topics/"+topic.ID+"/posts", member, map[string]string{"bodyMd": "locked"}), 403)
		expect(call("POST", "/api/v1/topics/"+topic.ID+"/unlock", moderator, nil), 204)
		expect(call("POST", "/api/v1/spaces/"+space+"/preview", member, map[string]string{"bodyMd": "**Safe** <script>alert(1)</script>"}), 200)
	})
	t.Run("four_moderation_modes_and_reports", func(t *testing.T) {
		for _, mode := range []string{"none", "post", "pre", "first_post"} {
			exec(`UPDATE space_forums SET moderation_mode=$2::moderation_mode WHERE space_uuid=$1`, space, mode)
			author := member
			if mode == "first_post" {
				author = outsider
				exec(`INSERT INTO space_member_cache(space_uuid,profile_uuid,role) VALUES($1,$2,'member') ON CONFLICT DO NOTHING`, space, outsider)
			}
			item := createTopic(author, "Mode "+mode, "Bài kiểm duyệt "+mode, "discussion")
			want := "published"
			if mode == "pre" || mode == "first_post" {
				want = "pending"
			}
			if item.Status != want {
				t.Fatalf("%s status %s", mode, item.Status)
			}
			if want == "pending" {
				queue := call("GET", "/api/v1/spaces/"+space+"/moderation/items", moderator, nil)
				expect(queue, 200)
				if !strings.Contains(queue.Body.String(), "Bài kiểm duyệt") {
					t.Fatal("moderator queue has no content")
				}
				expect(call("GET", "/api/v1/spaces/"+space+"/moderation/items", member, nil), 403)
				var mid string
				if err := pool.QueryRow(ctx, `SELECT id FROM moderation_items WHERE target_id=$1 AND state='pending'`, item.ID).Scan(&mid); err != nil {
					t.Fatal(err)
				}
				expect(call("POST", "/api/v1/moderation/"+mid+"/approve", member, nil), 403)
				expect(call("POST", "/api/v1/moderation/"+mid+"/approve", moderator, nil), 204)
				expect(call("POST", "/api/v1/moderation/"+mid+"/approve", moderator, nil), 404)
			}
		}
		exec(`UPDATE space_forums SET moderation_mode='pre' WHERE space_uuid=$1`, space)
		item := createTopic(member, "Reject this", "Nội dung chờ từ chối", "discussion")
		var mid string
		pool.QueryRow(ctx, `SELECT id FROM moderation_items WHERE target_id=$1`, item.ID).Scan(&mid)
		expect(call("POST", "/api/v1/moderation/"+mid+"/reject", moderator, map[string]string{"note": "fixture rejection"}), 204)
		exec(`UPDATE space_forums SET moderation_mode='none',report_auto_hide_threshold=2 WHERE space_uuid=$1`, space)
		p := reply(topic.ID, owner, "Bài bị hai người báo cáo", nil)
		for _, actor := range []string{member, moderator} {
			expect(call("POST", "/api/v1/posts/"+p.ID+"/report", actor, map[string]string{"reason": "spam"}), 204)
		}
		for _, actor := range []string{member, owner, moderator} {
			posts := call("GET", "/api/v1/topics/"+topic.ID+"/posts", actor, nil)
			expect(posts, 200)
			visible := strings.Contains(posts.Body.String(), "Bài bị hai người báo cáo")
			if visible != (actor != member) {
				t.Fatalf("hidden post visibility for %s = %v", actor, visible)
			}
		}
		hidden, e := repo.GetPost(ctx, p.ID)
		if e != nil || hidden.Status != "hidden" {
			t.Fatalf("auto hide %v %v", hidden, e)
		}
	})
	t.Run("attachment_validation_real_minio", func(t *testing.T) {
		if store == nil {
			t.Skip("set LUDISKUS_TEST_MINIO=1 for real MinIO")
		}
		w := call("POST", "/api/v1/attachments/presign", member, map[string]any{"spaceUuid": space, "fileName": "acceptance.txt", "contentType": "text/plain", "sizeBytes": 5})
		expect(w, 200)
		var result struct {
			Data service.PresignResult `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		defer store.Remove(ctx, result.Data.ObjectKey)
		expect(call("GET", "/api/v1/attachments/"+result.Data.AttachmentID+"/url", owner, nil), 403)
		expect(call("GET", "/api/v1/attachments/"+result.Data.AttachmentID+"/url", member, nil), 200)
		body := map[string]any{"bodyMd": "Tệp kiểm thử", "attachmentIds": []string{result.Data.AttachmentID}}
		expect(call("POST", "/api/v1/topics/"+topic.ID+"/posts", member, body), 422)
		req, _ := http.NewRequest("PUT", result.Data.UploadURL, strings.NewReader("hello"))
		req.Header.Set("Content-Type", "text/plain")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("upload %d", res.StatusCode)
		}
		expect(call("POST", "/api/v1/topics/"+topic.ID+"/posts", owner, body), 403)
		attached := call("POST", "/api/v1/topics/"+topic.ID+"/posts", member, body)
		expect(attached, 201)
		var attachedPost struct {
			Data domain.Post `json:"data"`
		}
		if err := json.Unmarshal(attached.Body.Bytes(), &attachedPost); err != nil {
			t.Fatal(err)
		}
		expect(call("GET", "/api/v1/attachments/"+result.Data.AttachmentID+"/url", owner, nil), 200)
		if err := repo.SetPostStatus(ctx, attachedPost.Data.ID, "hidden"); err != nil {
			t.Fatal(err)
		}
		expect(call("GET", "/api/v1/attachments/"+result.Data.AttachmentID+"/url", outsider, nil), 404)
		expect(call("GET", "/api/v1/attachments/"+result.Data.AttachmentID+"/url", member, nil), 200)

		expect(call("POST", "/api/v1/topics/"+topic.ID+"/posts", member, body), 403)
	})
	t.Run("concurrent_answer_selection", func(t *testing.T) {
		question := createTopic(owner, "Concurrent answers", "Question", "question")
		a := reply(question.ID, member, "Answer A", nil)
		b := reply(question.ID, owner, "Answer B", nil)
		errs := make(chan error, 20)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			id := a.ID
			if i%2 == 0 {
				id = b.ID
			}
			wg.Add(1)
			go func(id string) { defer wg.Done(); errs <- repo.SetTopicAnswer(ctx, question.ID, id) }(id)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var count int
		var consistent bool
		if err := pool.QueryRow(ctx, `SELECT count(*),bool_and(p.id=t.answer_post_id AND t.is_resolved) FROM posts p JOIN topics t ON t.id=p.topic_id WHERE p.topic_id=$1 AND p.is_answer`, question.ID).Scan(&count, &consistent); err != nil {
			t.Fatal(err)
		}
		if count != 1 || !consistent {
			t.Fatalf("answer state count=%d consistent=%v", count, consistent)
		}
	})
	t.Run("worker_outbox_retry_and_concurrent_claim", func(t *testing.T) {
		reject.Store(true)
		svc.ProcessOutbox(ctx, log)
		var queued int
		pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE status='queued' AND attempts>0`).Scan(&queued)
		if queued == 0 {
			t.Fatal("retry missing")
		}
		exec(`UPDATE outbox SET scheduled_at=now() WHERE status='queued'`)
		reject.Store(false)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); svc.ProcessOutbox(ctx, log) }()
		}
		wg.Wait()
		var sent, remaining int
		pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='sent'),count(*) FILTER(WHERE status<>'sent') FROM outbox`).Scan(&sent, &remaining)
		if sent == 0 || remaining != 0 || int64(sent) != delivered.Load() {
			t.Fatalf("delivery sent=%d remaining=%d received=%d", sent, remaining, delivered.Load())
		}
		var eventURL string
		if e := pool.QueryRow(ctx, `SELECT payload->'data'->>'url' FROM outbox WHERE event_type='ludiskus.post.mentioned' LIMIT 1`).Scan(&eventURL); e != nil {
			t.Fatal(e)
		}
		if !strings.HasPrefix(eventURL, "/ludiskus/s/"+space+"/t/"+topic.ID+"#post-") {
			t.Fatalf("broken notification link %s", eventURL)
		}
		t.Logf("outbox delivered %d events; no real user messages sent", sent)
	})
	t.Run("http_latency_and_metrics", func(t *testing.T) {
		latencies := make([]time.Duration, 40)
		for i := range latencies {
			start := time.Now()
			expect(call("GET", "/api/v1/search?q=bai", owner, nil), 200)
			latencies[i] = time.Since(start)
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95 := latencies[37]
		t.Logf("local handler + real SQL search p95=%s, not full BFF/browser p95", p95)
		w := call("GET", "/metrics", "", nil)
		expect(w, 200)
		if !strings.Contains(w.Body.String(), "ludiskus_forum_http_requests_total") || strings.Contains(w.Body.String(), topic.ID) {
			t.Fatal("invalid metrics labels")
		}
	})
}
