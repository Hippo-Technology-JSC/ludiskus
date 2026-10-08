package acceptance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"ludiskus/db"
	"ludiskus/internal/auth"
	"ludiskus/internal/config"
	"ludiskus/internal/database"
	"ludiskus/internal/domain"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/repository"
	"ludiskus/internal/service"
	transport "ludiskus/internal/transport/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPollAcceptance(t *testing.T) {
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
	pc.MaxConns = 30
	admin, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := fmt.Sprintf("poll_acceptance_%d", time.Now().UnixNano())
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
	repo := repository.New(pool)
	cfg := &config.Config{PollEnabled: true, CommentEnabled: true, PollDraftTTL: 24 * time.Hour, PollAnonSnapshotInterval: 5 * time.Minute, PollResultsCacheTTL: 10 * time.Minute, PollRankedLiveMax: 50000, PollAutoHideThreshold: 5, CommentTargetTTL: time.Minute, CommentBatchMax: 100, CommentResolveTimeout: time.Second, CommentPollInterval: 30 * time.Second, CacheTTL: time.Hour, OutboxMaxAttempts: 3}
	ident := identity.New(repo, nil, cfg, log)
	svc := service.New(repo, ident, nil, nil, markdown.New(), cfg, nil)
	h := transport.NewRouter(svc, auth.NewAuthenticator(nil, "", "", log).WithGateway("poll-test-secret"), log)
	owner := uuid.NewString()
	viewer := uuid.NewString()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO profile_cache(profile_uuid,name,is_active,created_at) VALUES($1,'Owner',true,now()-interval '1 year'),($2,'Viewer',true,now()-interval '1 year')`, owner, viewer)
	call := func(profile, method, path, key string, input any) *httptest.ResponseRecorder {
		var body []byte
		if input != nil {
			body, _ = json.Marshal(input)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if profile != "" {
			ts := fmt.Sprint(time.Now().Unix())
			r.Header.Set("X-Gw-User-Id", "fixture")
			r.Header.Set("X-Gw-Profile-Uuid", profile)
			r.Header.Set("X-Gw-Issued-At", ts)
			mac := hmac.New(sha256.New, []byte("poll-test-secret"))
			mac.Write([]byte(strings.Join([]string{"v1", "fixture", "", "", profile, ts}, "\n")))
			r.Header.Set("X-Gw-Signature", hex.EncodeToString(mac.Sum(nil)))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	create := func(kind, identity, results string, publish bool) string {
		t.Helper()
		config := map[string]any{}
		if kind == "multiple" {
			config = map[string]any{"min_choices": 1, "max_choices": 2}
		}
		if kind == "ranked" {
			config = map[string]any{"method": "irv", "max_ranks": 2}
		}
		in := map[string]any{"question": "Chọn phương án nào?", "kind": kind, "config": config, "identityMode": identity, "resultsVisibility": results, "standalone": map[string]any{"visibility": "public"}, "publish": publish, "options": []map[string]string{{"label": "A"}, {"label": "B"}}}
		w := call(owner, "POST", "/api/v1/polls", uuid.NewString(), in)
		if w.Code != 201 {
			t.Fatalf("create %d %s", w.Code, w.Body)
		}
		var resp struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if e = json.Unmarshal(w.Body.Bytes(), &resp); e != nil {
			t.Fatal(e)
		}
		return resp.Data.ID
	}
	optionIDs := func(id string) []string {
		t.Helper()
		p, e := repo.GetPoll(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		out := []string{}
		for _, o := range p.Options {
			out = append(out, o.ID)
		}
		return out
	}
	t.Run("schema_secret_and_check_constraints", func(t *testing.T) {
		var cols []string
		rows, e := pool.Query(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name='poll_ballots' ORDER BY ordinal_position`, schema)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var c string
			_ = rows.Scan(&c)
			cols = append(cols, c)
		}
		rows.Close()
		if strings.Join(cols, ",") != "id,poll_id,choices" {
			t.Fatal(cols)
		}
		id := create("single", "secret", "after_close", true)
		exec(`INSERT INTO poll_ballots(poll_id,choices) VALUES($1,'[]')`, id)
		if _, e = pool.Exec(ctx, `UPDATE polls SET allow_retract=true WHERE id=$1`, id); e == nil {
			t.Fatal("secret retract CHECK not enforced")
		}
	})
	t.Run("etag_permissions_and_single_idempotency", func(t *testing.T) {
		id := create("single", "public", "after_vote", true)
		ids := optionIDs(id)
		w := call(viewer, "GET", "/api/v1/polls/"+id, "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"results":null`) {
			t.Fatal(w.Code, w.Body)
		}
		old := w.Header().Get("ETag")
		key := uuid.NewString()
		in := map[string]any{"choices": []domain.PollChoice{{OptionID: ids[0]}}}
		w = call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", key, in)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		w = call(viewer, "GET", "/api/v1/polls/"+id, "", nil)
		if w.Header().Get("ETag") == old || strings.Contains(w.Body.String(), `"results":null`) {
			t.Fatal("post-vote ETag/result stale", w.Body)
		}
		p, _ := repo.GetPoll(ctx, id)
		version := p.BallotVersion
		for i := 0; i < 10; i++ {
			w = call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", key, in)
			if w.Code != 200 {
				t.Fatal(w.Body)
			}
		}
		p, _ = repo.GetPoll(ctx, id)
		if p.VoterCount != 1 || p.BallotVersion != version {
			t.Fatal("idempotency changed receipt/version", p.VoterCount, p.BallotVersion)
		}
		if w = call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", "", in); w.Code != 428 {
			t.Fatal("missing key", w.Code)
		}
		if w = call("", "PUT", "/api/v1/polls/"+id+"/vote", key, in); w.Code != 401 {
			t.Fatal("guest vote", w.Code)
		}
		if w = call("", "POST", "/api/v1/public/polls/"+id, "", in); w.Code != 405 {
			t.Fatal("public write", w.Code)
		}
	})
	t.Run("200_voters_change_and_retract", func(t *testing.T) {
		id := create("multiple", "public", "always", true)
		ids := optionIDs(id)
		errs := make(chan error, 1000)
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < 200; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				profile := uuid.NewString()
				for j := 0; j < 5; j++ {
					choices := []domain.PollChoice{{OptionID: ids[j%2]}}
					if j%2 == 0 {
						choices = append(choices, domain.PollChoice{OptionID: ids[1]})
					}
					if j%2 == 0 {
						choices = []domain.PollChoice{{OptionID: ids[1]}, {OptionID: ids[0]}}
					}
					in := domain.PollVoteInput{Choices: choices}
					if e := repo.WritePollVote(ctx, id, profile, uuid.NewString(), in, false, func(p *domain.Poll, b []domain.PollChoice) ([]domain.PollChoice, error) { return b, nil }); e != nil {
						errs <- e
						return
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
		var drift int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM poll_count_check WHERE poll_id=$1`, id).Scan(&drift)
		p, _ := repo.GetPoll(ctx, id)
		if drift != 0 || p.VoterCount != 200 || p.Options[0].VoteCount != 200 || p.Options[1].VoteCount != 200 {
			t.Fatalf("drift %d voters %d options %+v", drift, p.VoterCount, p.Options)
		}
		t.Logf("1000 writes / 200 goroutines: %s", time.Since(start))
		if n, e := svc.ReconcilePolls(ctx); e != nil {
			t.Fatal(e)
		} else if n != 0 {
			t.Fatalf("reconcile found %d unexpected drift", n)
		}
	})
	t.Run("secret_replay_and_no_identity_route", func(t *testing.T) {
		id := create("single", "secret", "always", true)
		ids := optionIDs(id)
		key := uuid.NewString()
		in := map[string]any{"choices": []domain.PollChoice{{OptionID: ids[1]}}}
		for i := 0; i < 2; i++ {
			w := call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", key, in)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), `"choices":[{"optionId"`) {
				t.Fatal("secret viewer choices leaked")
			}
		}
		for _, route := range []string{"voters", "export.csv?kind=votes"} {
			w := call(owner, "GET", "/api/v1/polls/"+id+"/"+route, "", nil)
			if w.Code != 403 {
				t.Fatalf("secret %s %d %s", route, w.Code, w.Body)
			}
		}
		var votes, ballots, receipts int
		_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM poll_votes WHERE poll_id=$1),(SELECT count(*) FROM poll_ballots WHERE poll_id=$1),(SELECT count(*) FROM poll_voters WHERE poll_id=$1)`, id).Scan(&votes, &ballots, &receipts)
		if votes != 0 || ballots != 1 || receipts != 1 {
			t.Fatal(votes, ballots, receipts)
		}
		if w := call(viewer, "DELETE", "/api/v1/polls/"+id+"/vote", "", nil); w.Code != 409 {
			t.Fatal(w.Code)
		}
	})
	t.Run("anonymous_snapshot_does_not_jump_for_one_vote", func(t *testing.T) {
		id := create("single", "anonymous", "always", true)
		ids := optionIDs(id)
		for i := 0; i < 3; i++ {
			profile := uuid.NewString()
			w := call(profile, "PUT", "/api/v1/polls/"+id+"/vote", uuid.NewString(), map[string]any{"choices": []domain.PollChoice{{OptionID: ids[0]}}})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
		}
		before := call(owner, "GET", "/api/v1/polls/"+id, "", nil)
		w := call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", uuid.NewString(), map[string]any{"choices": []domain.PollChoice{{OptionID: ids[1]}}})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		after := call(owner, "GET", "/api/v1/polls/"+id, "", nil)
		if before.Header().Get("ETag") != after.Header().Get("ETag") {
			t.Fatalf("anonymous ETag revealed single vote\nbefore %s\nafter %s", before.Body, after.Body)
		}
	})
	t.Run("close_vote_race_and_worker_dedup", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			id := create("single", "public", "after_close", true)
			ids := optionIDs(id)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = repo.WritePollVote(ctx, id, viewer, uuid.NewString(), domain.PollVoteInput{Choices: []domain.PollChoice{{OptionID: ids[0]}}}, false, func(p *domain.Poll, b []domain.PollChoice) ([]domain.PollChoice, error) { return b, nil })
			}()
			go func() {
				defer wg.Done()
				_, err := svc.PollAction(ctx, id, service.PollViewer{Profile: owner}, "close", nil)
				if err != nil {
					t.Error(err)
				}
			}()
			wg.Wait()
			p, _ := repo.GetPoll(ctx, id)
			result, e := repo.PollResult(ctx, id)
			if e != nil || p.Status != "closed" || result == nil || result.VoterCount != p.VoterCount {
				t.Fatalf("race %v %v %v", p, result, e)
			}
		}
	})
	t.Run("csv_injection_and_target_cleanup", func(t *testing.T) {
		id := create("single", "public", "always", true)
		exec(`UPDATE poll_options SET label='=HYPERLINK("x")' WHERE poll_id=$1 AND position=0`, id)
		w := call(owner, "GET", "/api/v1/polls/"+id+"/export.csv", "", nil)
		if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "\xef\xbb\xbf") || !strings.Contains(w.Body.String(), "'=HYPERLINK") {
			t.Fatal(w.Code, w.Body)
		}
		exec(`INSERT INTO comment_services(code,name,base_url,verify_mode,is_active) VALUES('pollfixture','Fixture','','trust',true)`)
		exec(`INSERT INTO comment_targets(id,service_code,resource_type,resource_id,visibility,state,updated_at) VALUES($1,'pollfixture','item','one','public','gone',now()-interval '60 days')`, id)
		exec(`UPDATE polls SET anchor_target_id=$1,visibility=NULL WHERE id=$1`, id)
		if _, e := repo.CleanupCommentData(ctx, 10, 365); e != nil {
			t.Fatal(e)
		}
		var targetExists bool
		_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM comment_targets WHERE id=$1)`, id).Scan(&targetExists)
		if !targetExists {
			t.Fatal("cleanup removed anchored poll target")
		}
	})
	t.Run("seed_preserves_admin_and_feature_gate", func(t *testing.T) {
		original, _ := repo.GetPollPolicy(ctx, "ludiskus", "standalone")
		defer svc.PutPollPolicy(ctx, "ludiskus", "standalone", original, nil)
		raw := json.RawMessage(`{"enabled":false}`)
		if e = svc.PutPollPolicy(ctx, "ludiskus", "standalone", raw, nil); e != nil {
			t.Fatal(e)
		}
		_ = service.New(repo, ident, nil, nil, markdown.New(), cfg, nil)
		stored, e := repo.GetPollPolicy(ctx, "ludiskus", "standalone")
		if e != nil || !strings.Contains(string(stored), `false`) {
			t.Fatal("seed overwrote admin", string(stored), e)
		}
		cfg.PollEnabled = false
		w := call(owner, "GET", "/api/v1/polls/mine", "", nil)
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
		cfg.PollEnabled = true
	})

	t.Run("draft_attach_is_atomic_and_follows_anchor_moderation", func(t *testing.T) {
		space, board := uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO space_cache(space_uuid,name,is_public,creator_profile_uuid) VALUES($1,'Poll fixture',true,$2)`, space, owner)
		exec(`INSERT INTO space_member_cache(space_uuid,profile_uuid,role) VALUES($1,$2,'owner')`, space, owner)
		exec(`INSERT INTO space_forums(space_uuid,is_public,moderation_mode) VALUES($1,true,'none')`, space)
		exec(`INSERT INTO boards(id,space_uuid,code,name,kind) VALUES($1,$2,'poll','Poll fixture','forum')`, board, space)
		owned := create("single", "public", "always", false)
		foreign := create("single", "public", "always", false)
		exec(`UPDATE polls SET created_by=$2 WHERE id=$1`, foreign, viewer)
		attach := func(ids []string, slug string) (*domain.Topic, *domain.Post, error) {
			return repo.CreateTopicWithPostHook(ctx, domain.Topic{SpaceUUID: space, BoardID: board, AuthorProfileUUID: owner, Title: "Poll attach fixture", Slug: slug, Type: "discussion", Status: "pending"}, domain.Post{BodyMD: "Fixture", BodyHTML: "Fixture", Status: "pending"}, nil, func(tx pgx.Tx, topic *domain.Topic, post *domain.Post) error {
				ref := domain.ResourceRef{Service: "ludiskus", Type: "topic", ID: topic.ID}
				snap := domain.InteractionContext{Exists: true, Type: ref.Type, ID: ref.ID, SpaceUUID: &space, Visibility: "public", State: "blocked", Owner: &domain.InteractionOwner{Type: "profile", ID: owner}, Title: topic.Title, CanonicalPath: "/ludiskus/t/" + topic.ID, Capabilities: json.RawMessage(`{}`)}
				return svc.AttachDrafts(ctx, tx, owner, ref, snap, ids, true)
			})
		}
		// Duplicate IDs are rejected after the first draft write; the entire content
		// transaction and that first attachment must roll back.
		if _, _, e := attach([]string{owned, owned}, "must-rollback"); e == nil {
			t.Fatal("duplicate draft accepted")
		}
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM topics WHERE slug='must-rollback'`).Scan(&n)
		if n != 0 {
			t.Fatal("content escaped failed attachment")
		}
		draft, e := repo.GetPoll(ctx, owned)
		if e != nil || draft.AnchorTargetID != nil || draft.Status != "draft" {
			t.Fatalf("draft escaped rollback: %+v %v", draft, e)
		}
		if _, _, e := attach([]string{foreign}, "foreign-must-rollback"); e == nil {
			t.Fatal("another author's draft accepted")
		}
		topic, post, e := attach([]string{owned}, "attached-pending")
		if e != nil {
			t.Fatal(e)
		}
		draft, e = repo.GetPoll(ctx, owned)
		if e != nil || draft.Status != "pending" || draft.Target == nil || draft.Target.ResourceID != topic.ID {
			t.Fatalf("pending anchor: %+v %v", draft, e)
		}
		if _, e := repo.PublishPost(ctx, post.ID); e != nil {
			t.Fatal(e)
		}
		draft, e = repo.GetPoll(ctx, owned)
		if e != nil || draft.Status != "published" || draft.Target.State != "active" {
			t.Fatalf("approval: %+v %v", draft, e)
		}
		if e := repo.SetTopicStatus(ctx, topic.ID, "hidden"); e != nil {
			t.Fatal(e)
		}
		if w := call(owner, "GET", "/api/v1/polls/"+owned, "", nil); w.Code != 403 {
			t.Fatalf("hidden anchor reader: %d %s", w.Code, w.Body)
		}
	})
	t.Run("owner_only_and_identity_tightening", func(t *testing.T) {
		id := create("single", "owner_only", "always", true)
		oid := optionIDs(id)[0]
		if w := call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", uuid.NewString(), map[string]any{"choices": []map[string]string{{"optionId": oid}}}); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		for _, profile := range []string{viewer, ""} {
			path := "/api/v1/polls/" + id + "/voters"
			if profile == "" {
				path = "/api/v1/public/polls/" + id + "/voters"
			}
			w := call(profile, "GET", path, "", nil)
			if w.Code == 200 {
				t.Fatal("owner-only identities disclosed")
			}
		}
		if w := call(owner, "GET", "/api/v1/polls/"+id+"/voters", "", nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w := call(owner, "PATCH", "/api/v1/polls/"+id, "", map[string]string{"identityMode": "public"}); w.Code != 409 {
			t.Fatal("identity loosened", w.Code, w.Body)
		}
		if w := call(owner, "PATCH", "/api/v1/polls/"+id, "", map[string]string{"identityMode": "anonymous"}); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w := call(owner, "GET", "/api/v1/polls/"+id+"/voters", "", nil); w.Code != 403 {
			t.Fatal("tightened identities disclosed", w.Code, w.Body)
		}
	})

	t.Run("final_result_survives_personal_ballot_erasure", func(t *testing.T) {
		id := create("single", "public", "always", true)
		oid := optionIDs(id)[0]
		if w := call(viewer, "PUT", "/api/v1/polls/"+id+"/vote", uuid.NewString(), map[string]any{"choices": []map[string]string{{"optionId": oid}}}); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		if w := call(owner, "POST", "/api/v1/polls/"+id+"/close", "", nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		before, e := repo.PollResult(ctx, id)
		if e != nil || before == nil || !before.Final {
			t.Fatal(before, e)
		}
		exec(`DELETE FROM poll_votes WHERE poll_id=$1 AND voter_profile_uuid=$2`, id, viewer)
		exec(`DELETE FROM poll_voters WHERE poll_id=$1 AND profile_uuid=$2`, id, viewer)
		if fixed, e := svc.ReconcilePolls(ctx); e != nil || fixed != 0 {
			t.Fatal("final erased ballots were recounted", fixed, e)
		}
		after, e := repo.PollResult(ctx, id)
		if e != nil || !bytes.Equal(before.Result, after.Result) || after.VoterCount != 1 {
			t.Fatal("final result changed", after, e)
		}
		var n int
		if e := pool.QueryRow(ctx, `SELECT count(*) FROM poll_count_check WHERE poll_id=$1`, id).Scan(&n); e != nil || n != 0 {
			t.Fatal("erasure flagged as drift", n, e)
		}
	})
	t.Run("migration_down_up_with_all_identity_data", func(t *testing.T) {
		for _, name := range []string{"0017_poll_ops", "0016_poll_moderation", "0015_poll_report_enum", "0014_poll_core"} {
			raw, e := db.Migrations.ReadFile("migrations/" + name + ".down.sql")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = pool.Exec(ctx, string(raw)); e != nil {
				t.Fatal(name, e)
			}
		}
		exec(`DELETE FROM schema_migrations WHERE version>=14`)
		if e = database.Migrate(ctx, pool, log); e != nil {
			t.Fatal(e)
		}
	})
}
