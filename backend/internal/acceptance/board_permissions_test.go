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
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ludiskus/internal/auth"
	"ludiskus/internal/config"
	"ludiskus/internal/database"
	"ludiskus/internal/domain"
	"ludiskus/internal/identity"
	"ludiskus/internal/markdown"
	"ludiskus/internal/notify"
	"ludiskus/internal/repository"
	"ludiskus/internal/service"
	transport "ludiskus/internal/transport/http"
)

func TestBoardPermissionsAcceptance(t *testing.T) {
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
	schema := fmt.Sprintf("board_perm_test_%d", time.Now().UnixNano())
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

	testSpace := "10000000-0000-4000-8000-000000000010"
	testOwner := "20000000-0000-4000-8000-000000000011"
	testMemberA := "20000000-0000-4000-8000-000000000012"
	testMemberB := "20000000-0000-4000-8000-000000000013"
	testBoardMod := "20000000-0000-4000-8000-000000000014"
	testOutsider := "20000000-0000-4000-8000-000000000015"

	boardGeneral := "30000000-0000-4000-8000-000000000011"
	boardAnnounce := "30000000-0000-4000-8000-000000000012"
	boardEditorial := "30000000-0000-4000-8000-000000000013"
	boardLockedDown := "30000000-0000-4000-8000-000000000014"

	exec(`INSERT INTO space_cache(space_uuid,name,is_public,creator_profile_uuid) VALUES($1,'Board Perm Test',true,$2)`, testSpace, testOwner)
	for i, id := range []string{testOwner, testMemberA, testMemberB, testBoardMod, testOutsider} {
		exec(`INSERT INTO profile_cache(profile_uuid,code,name,is_active) VALUES($1,$2,$3,true)`, id, fmt.Sprintf("permuser%d", i), fmt.Sprintf("Perm User %d", i))
	}
	for id, role := range map[string]string{testOwner: "owner", testMemberA: "member", testMemberB: "member", testBoardMod: "member"} {
		exec(`INSERT INTO space_member_cache(space_uuid,profile_uuid,role) VALUES($1,$2,$3)`, testSpace, id, role)
	}
	exec(`INSERT INTO space_forums(space_uuid,moderation_mode,post_policy) VALUES($1,'pre','members')`, testSpace)

	exec(`INSERT INTO boards(id,space_uuid,code,name,kind) VALUES($1,$2,'general','General','forum')`, boardGeneral, testSpace)
	exec(`INSERT INTO boards(id,space_uuid,code,name,kind) VALUES($1,$2,'announcements','Announcements','announcement')`, boardAnnounce, testSpace)
	exec(`INSERT INTO boards(id,space_uuid,code,name,kind) VALUES($1,$2,'editorial','Editorial','forum')`, boardEditorial, testSpace)
	exec(`INSERT INTO boards(id,space_uuid,code,name,kind) VALUES($1,$2,'lockdown','Lockdown','forum')`, boardLockedDown, testSpace)

	cfg := &config.Config{CacheTTL: time.Hour, MaxAttachments: 8, MaxFileBytes: 1024 * 1024, OutboxMaxAttempts: 3, PresignTTL: time.Minute, AllowedMIME: []string{"text/plain", "image/png"}}
	repo := repository.New(pool)
	ident := identity.New(repo, nil, cfg, log)
	svc := service.New(repo, ident, nil, notify.New(cfg), markdown.New(), cfg, nil)
	router := transport.NewRouter(svc, auth.NewAuthenticator(nil, "", "", log).WithGateway(secret), log)

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
		router.ServeHTTP(w, r)
		return w
	}

	expectStatus := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("expected status %d, got %d, body: %s", code, w.Code, w.Body.String())
		}
	}

	t.Run("configure_board_permissions", func(t *testing.T) {
		// Non-owner / non-admin cannot access GET or PUT permissions
		w := call("GET", "/api/v1/boards/"+boardAnnounce+"/permissions", testMemberA, nil)
		expectStatus(w, 403)

		// Owner reads initial permissions (version 1)
		w = call("GET", "/api/v1/boards/"+boardAnnounce+"/permissions", testOwner, nil)
		expectStatus(w, 200)
		var detail struct {
			Data domain.BoardPermissionDetail `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &detail)
		if detail.Data.Version != 1 || detail.Data.TopicPolicy.Mode != "inherit_space" {
			t.Fatalf("unexpected initial perm: %+v", detail.Data)
		}

		// Configure BoardAnnouncements: topicPolicy = moderators, replyPolicy = members, moderator = testBoardMod
		putBody := service.UpdateBoardPermissionsInput{
			ExpectedVersion: 1,
			TopicPolicy: domain.BoardPolicyConfig{
				Mode: "moderators",
			},
			ReplyPolicy: domain.BoardPolicyConfig{
				Mode: "members",
			},
			ModeratorProfileUUIDs: []string{testBoardMod},
		}
		w = call("PUT", "/api/v1/boards/"+boardAnnounce+"/permissions", testOwner, putBody)
		expectStatus(w, 200)
		json.Unmarshal(w.Body.Bytes(), &detail)
		if detail.Data.Version != 2 || detail.Data.TopicPolicy.Mode != "moderators" {
			t.Fatalf("expected version 2 with moderators mode, got %+v", detail.Data)
		}

		// Optimistic concurrency: PUT again with old version 1 must return 409
		w = call("PUT", "/api/v1/boards/"+boardAnnounce+"/permissions", testOwner, putBody)
		expectStatus(w, 409)

		// Configure BoardEditorial: topicPolicy = selected (testMemberA only)
		w = call("PUT", "/api/v1/boards/"+boardEditorial+"/permissions", testOwner, service.UpdateBoardPermissionsInput{
			ExpectedVersion: 1,
			TopicPolicy: domain.BoardPolicyConfig{
				Mode:         "selected",
				ProfileUUIDs: []string{testMemberA},
			},
			ReplyPolicy: domain.BoardPolicyConfig{
				Mode: "inherit_space",
			},
		})
		expectStatus(w, 200)

		// Configure BoardLockdown: topicPolicy = nobody
		w = call("PUT", "/api/v1/boards/"+boardLockedDown+"/permissions", testOwner, service.UpdateBoardPermissionsInput{
			ExpectedVersion: 1,
			TopicPolicy: domain.BoardPolicyConfig{
				Mode: "nobody",
			},
			ReplyPolicy: domain.BoardPolicyConfig{
				Mode: "nobody",
			},
		})
		expectStatus(w, 200)

		// Invalid profile (outsider not in space) -> 422
		w = call("PUT", "/api/v1/boards/"+boardGeneral+"/permissions", testOwner, service.UpdateBoardPermissionsInput{
			ExpectedVersion: 1,
			TopicPolicy: domain.BoardPolicyConfig{
				Mode:         "selected",
				ProfileUUIDs: []string{testOutsider},
			},
			ReplyPolicy: domain.BoardPolicyConfig{Mode: "inherit_space"},
		})
		expectStatus(w, 422)
	})

	t.Run("check_board_capabilities", func(t *testing.T) {
		// BoardAnnouncements capabilities for testMemberA
		w := call("GET", "/api/v1/boards/"+boardAnnounce+"/capabilities", testMemberA, nil)
		expectStatus(w, 200)
		var capResp struct {
			Data domain.BoardCapabilities `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &capResp)
		if capResp.Data.CanCreateTopic {
			t.Fatal("testMemberA should NOT be able to create topic in announcements")
		}
		if !capResp.Data.CanReply {
			t.Fatal("testMemberA should be able to reply in announcements")
		}
		if capResp.Data.CanModerate {
			t.Fatal("testMemberA should NOT be able to moderate announcements")
		}

		// BoardAnnouncements capabilities for testBoardMod
		w = call("GET", "/api/v1/boards/"+boardAnnounce+"/capabilities", testBoardMod, nil)
		expectStatus(w, 200)
		json.Unmarshal(w.Body.Bytes(), &capResp)
		if !capResp.Data.CanCreateTopic || !capResp.Data.CanReply || !capResp.Data.CanModerate {
			t.Fatalf("testBoardMod should have full rights on boardAnnounce, got %+v", capResp.Data)
		}
		if capResp.Data.CanManagePermissions {
			t.Fatal("board moderator must NOT have canManagePermissions")
		}

		// Batch capabilities returned in list boards
		w = call("GET", "/api/v1/spaces/"+testSpace+"/boards", testMemberA, nil)
		expectStatus(w, 200)
		var boardsResp struct {
			Data []domain.Board `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &boardsResp)
		if len(boardsResp.Data) < 4 {
			t.Fatalf("expected 4 boards, got %d", len(boardsResp.Data))
		}
		for _, b := range boardsResp.Data {
			if b.Capabilities == nil {
				t.Fatalf("board %s missing capabilities", b.Code)
			}
			if b.ID == boardAnnounce && b.Capabilities.CanCreateTopic {
				t.Fatal("batch capabilities for boardAnnounce should have canCreateTopic=false for memberA")
			}
		}
	})

	t.Run("enforce_create_topic_and_reply", func(t *testing.T) {
		// MemberA attempts to create topic in announcements -> 403
		w := call("POST", "/api/v1/boards/"+boardAnnounce+"/topics", testMemberA, map[string]any{
			"title":  "Member topic",
			"bodyMd": "Should be rejected",
		})
		expectStatus(w, 403)

		// BoardMod creates topic in announcements -> 201
		// Note: since space has moderation_mode='pre', but BoardMod is effective moderator of announcements,
		// they are exempt from pre-moderation and topic is published immediately!
		w = call("POST", "/api/v1/boards/"+boardAnnounce+"/topics", testBoardMod, map[string]any{
			"title":  "Official Announcement",
			"bodyMd": "Welcome to announcements!",
		})
		expectStatus(w, 201)
		var topicResp struct {
			Data domain.Topic `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &topicResp)
		announceTopicID := topicResp.Data.ID
		if topicResp.Data.Status != "published" {
			t.Fatalf("board moderator should be exempt from pre-moderation, status=%s", topicResp.Data.Status)
		}

		// MemberA can reply to this published topic in announcements -> 202 (pending because memberA is not mod)
		w = call("POST", "/api/v1/topics/"+announceTopicID+"/posts", testMemberA, map[string]any{
			"bodyMd": "Thanks for the update!",
		})
		expectStatus(w, 202)

		// MemberA creates topic in editorial -> allowed (selected)
		w = call("POST", "/api/v1/boards/"+boardEditorial+"/topics", testMemberA, map[string]any{
			"title":  "Article by A",
			"bodyMd": "Draft article...",
		})
		expectStatus(w, 202)

		// MemberB creates topic in editorial -> 403 (not selected)
		w = call("POST", "/api/v1/boards/"+boardEditorial+"/topics", testMemberB, map[string]any{
			"title":  "Article by B",
			"bodyMd": "Draft article...",
		})
		expectStatus(w, 403)

		// Owner creates topic in lockdown (nobody) -> 403
		w = call("POST", "/api/v1/boards/"+boardLockedDown+"/topics", testOwner, map[string]any{
			"title":  "Even owner cannot post",
			"bodyMd": "Nobody mode",
		})
		expectStatus(w, 403)
	})

	t.Run("moderation_queue_and_action_boundaries", func(t *testing.T) {
		// Create a pending topic in boardGeneral by testMemberA
		w := call("POST", "/api/v1/boards/"+boardGeneral+"/topics", testMemberA, map[string]any{
			"title":  "General discussion topic",
			"bodyMd": "Please approve this.",
		})
		expectStatus(w, 202)
		var genTopic struct {
			Data domain.Topic `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &genTopic)

		// BoardMod checks queue: should only see items for boardAnnounce, NOT boardGeneral!
		w = call("GET", "/api/v1/spaces/"+testSpace+"/moderation/items", testBoardMod, nil)
		expectStatus(w, 200)
		var queueResp struct {
			Data []domain.ModerationItem `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &queueResp)
		for _, it := range queueResp.Data {
			if it.TargetID == genTopic.Data.ID {
				t.Fatal("boardMod should NOT see general board pending topic in queue")
			}
		}

		// Find the moderation item for the reply in announcements
		var announceItem *domain.ModerationItem
		for i := range queueResp.Data {
			if queueResp.Data[i].TargetType == "post" {
				announceItem = &queueResp.Data[i]
				break
			}
		}
		if announceItem == nil {
			t.Fatal("expected pending reply in boardAnnounce")
		}

		// BoardMod approves the reply in boardAnnounce -> 204
		w = call("POST", "/api/v1/moderation/"+announceItem.ID+"/approve", testBoardMod, nil)
		expectStatus(w, 204)

		// Find moderation item for genTopic
		var genItem domain.ModerationItem
		err = pool.QueryRow(ctx, `SELECT id, space_uuid, target_type, target_id, source, state
			FROM moderation_items WHERE target_id = $1`, genTopic.Data.ID).Scan(
			&genItem.ID, &genItem.SpaceUUID, &genItem.TargetType, &genItem.TargetID, &genItem.Source, &genItem.State)
		if err != nil {
			t.Fatal(err)
		}

		// BoardMod tries to approve genItem in boardGeneral -> 403 Forbidden!
		w = call("POST", "/api/v1/moderation/"+genItem.ID+"/approve", testBoardMod, nil)
		expectStatus(w, 403)
	})

	t.Run("deprecated_min_role_rejection", func(t *testing.T) {
		// Calling createBoard with minRole must return validation_error
		w := call("POST", "/api/v1/spaces/"+testSpace+"/boards", testOwner, map[string]any{
			"code":    "test-deprecate",
			"name":    "Test Deprecate",
			"minRole": "admin",
		})
		expectStatus(w, 422)

		// Calling updateBoard with minRole must return validation_error
		w = call("PATCH", "/api/v1/boards/"+boardGeneral, testOwner, map[string]any{
			"minRole": "admin",
		})
		expectStatus(w, 422)
	})
}
