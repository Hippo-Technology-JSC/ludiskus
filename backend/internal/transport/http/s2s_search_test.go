package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ludiskus/internal/auth"
	"ludiskus/internal/domain"
)

func s2sItems(typ string, n int) []s2sSearchItem {
	out := make([]s2sSearchItem, n)
	for i := range out {
		out[i] = s2sSearchItem{Type: typ, ID: typ + string(rune('a'+i)), Rank: 99}
	}
	return out
}

// Trộn xen kẽ, cắt về limit, rank 1..n đánh SAU khi cắt.
func TestInterleaveS2S(t *testing.T) {
	got := interleaveS2S(4, s2sItems("topic", 1), s2sItems("board", 5))
	wantTypes := []string{"topic", "board", "board", "board"}
	if len(got) != len(wantTypes) {
		t.Fatalf("len = %d, muốn %d", len(got), len(wantTypes))
	}
	for i, it := range got {
		if it.Type != wantTypes[i] || it.Rank != i+1 {
			t.Errorf("items[%d] = %s/%d, muốn %s/%d", i, it.Type, it.Rank, wantTypes[i], i+1)
		}
	}
	if got := interleaveS2S(20, s2sItems("topic", 20), s2sItems("board", 20)); len(got) != 20 {
		t.Fatalf("vượt limit: %d", len(got))
	}
}

func TestBoardSubtitleAndTruncate(t *testing.T) {
	if got := boardSubtitle(domain.Board{TopicCount: 3}); got != "Chuyên mục · 3 chủ đề" {
		t.Fatalf("subtitle = %q", got)
	}
	if got := boardSubtitle(domain.Board{}); got != "Chuyên mục" {
		t.Fatalf("subtitle = %q", got)
	}
	if got := truncateRunes("Thảo  luận\nchung", 7); got != "Thảo lu…" {
		t.Fatalf("truncate = %q", got)
	}
}

func TestWithoutSuperuserOnlyNarrows(t *testing.T) {
	if auth.IsSuperuser(auth.WithoutSuperuser(context.Background())) {
		t.Fatal("WithoutSuperuser phải trả ctx không phải superuser")
	}
}

// Hai nhánh trả rỗng mà không chạm service: query < 2 rune, và `types` không
// chứa loại nào ludiskus phục vụ.
func TestS2SSearchEmptyWithoutTouchingService(t *testing.T) {
	s := &Server{}
	for _, body := range []string{
		`{"query":"a"}`,
		`{"query":"  đ "}`,
		`{"query":"thao luan","types":["document"]}`,
	} {
		rec := httptest.NewRecorder()
		s.s2sSearch(rec, httptest.NewRequest(http.MethodPost, "/api/v1/s2s/search", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", body, rec.Code)
		}
		var out s2sSearchResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if out.Items == nil || len(out.Items) != 0 {
			t.Fatalf("%s: muốn items rỗng (không null), được %v", body, out.Items)
		}
	}
}
