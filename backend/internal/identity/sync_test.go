package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ludiskus/internal/config"
	"ludiskus/internal/domain"
)

// memberStore là phần Store mà SyncMembers dùng; các phương thức khác không
// được gọi trong phép thử này nên để panic cho lộ ngay nếu đường đi đổi.
type memberStore struct {
	mu      sync.Mutex
	members []domain.CachedMember
	calls   int
}

func (m *memberStore) ReplaceMembers(ctx context.Context, spaceUUID string, ms []domain.CachedMember) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.members = ms
	return nil
}
func (m *memberStore) GetCachedProfile(context.Context, string) (*domain.CachedProfile, error) {
	panic("không dùng")
}
func (m *memberStore) GetCachedProfileByCode(context.Context, string) (*domain.CachedProfile, error) {
	panic("không dùng")
}
func (m *memberStore) UpsertCachedProfile(context.Context, domain.CachedProfile) error {
	panic("không dùng")
}
func (m *memberStore) GetCachedSpace(context.Context, string) (*domain.CachedSpace, error) {
	panic("không dùng")
}
func (m *memberStore) UpsertCachedSpace(context.Context, domain.CachedSpace) error {
	panic("không dùng")
}
func (m *memberStore) ListMembers(context.Context, string) ([]domain.CachedMember, error) {
	panic("không dùng")
}

// TestSyncMembersReadsEveryPage: Space quá một trang thì người ở trang sau phải
// vẫn có trong cache. Đây không chỉ là chuyện gợi ý mention — Role()/IsMember()
// tra cùng bảng này, nên thiếu một người là người đó mất quyền đọc Space.
func TestSyncMembersReadsEveryPage(t *testing.T) {
	const total = 4500
	const perPage = 200
	var pagesServed []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		pagesServed = append(pagesServed, page)
		last := (total + perPage - 1) / perPage
		rows := []map[string]any{}
		for i := (page - 1) * perPage; i < page*perPage && i < total; i++ {
			rows = append(rows, map[string]any{"uuid": fmt.Sprintf("uuid-%04d", i), "role": "member"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       rows,
			"pagination": map[string]int{"current_page": page, "last_page": last},
		})
	}))
	defer server.Close()

	store := &memberStore{}
	cfg := &config.Config{HipcoreURL: server.URL, HipcoreClientID: "c", HipcoreClientSecret: "s", CacheTTL: time.Hour}
	svc := New(store, nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, err := svc.SyncMembers(context.Background(), "space-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("đồng bộ %d thành viên, phải đủ %d (trang đã đọc: %v)", len(got), total, pagesServed)
	}
	// Người CUỐI cùng là người dễ bị rơi nhất khi chỉ đọc một trang.
	if got[len(got)-1].ProfileUUID != fmt.Sprintf("uuid-%04d", total-1) {
		t.Errorf("mất người ở trang cuối: %s", got[len(got)-1].ProfileUUID)
	}
	// ReplaceMembers chỉ được gọi MỘT lần: gọi theo từng trang thì trang sau
	// xoá sạch trang trước và cache chỉ còn đúng trang cuối.
	if store.calls != 1 {
		t.Errorf("ReplaceMembers gọi %d lần, phải đúng 1 lần sau khi gom đủ trang", store.calls)
	}
	if len(store.members) != total {
		t.Errorf("cache còn %d thành viên, phải đủ %d", len(store.members), total)
	}
}

// TestSyncMembersStopsWithoutPagination: hasMore() trả true khi last_page = 0,
// nên nếu HipCore không kèm pagination thì phải có chốt "trang rỗng" chặn lại,
// bằng không vòng lặp chạy mãi.
func TestSyncMembersStopsWithoutPagination(t *testing.T) {
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		served++
		rows := []map[string]any{}
		if served == 1 {
			rows = append(rows, map[string]any{"uuid": "uuid-only", "role": "owner"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
	}))
	defer server.Close()

	store := &memberStore{}
	cfg := &config.Config{HipcoreURL: server.URL, HipcoreClientID: "c", HipcoreClientSecret: "s", CacheTTL: time.Hour}
	svc := New(store, nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if got, err := svc.SyncMembers(context.Background(), "space-1"); err != nil || len(got) != 1 {
			t.Errorf("đồng bộ %v %v", got, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("không có pagination → vòng lặp không dừng")
	}
}
