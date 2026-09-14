package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ludiskus/internal/config"
)

func TestClientGetAndPatch(t *testing.T) {
	t.Helper()
	var patched map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "expires_in": 3600})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates":
			if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
				t.Errorf("Authorization = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "template-1", "code": "ludiskus.post.mentioned"}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/templates/template-1":
			if err := json.NewDecoder(r.Body).Decode(&patched); err != nil {
				t.Errorf("decode patch: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		HipcoreURL: server.URL, LunotiAPIURL: server.URL,
		LunotiClientID: "client", LunotiClientSecret: "secret",
	}
	client := New(cfg)
	var templates struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := client.Get(context.Background(), "/api/v1/templates", &templates); err != nil {
		t.Fatal(err)
	}
	if len(templates.Data) != 1 || templates.Data[0].ID != "template-1" {
		t.Fatalf("templates = %#v", templates.Data)
	}
	if err := client.Patch(context.Background(), "/api/v1/templates/template-1", map[string]any{"name": "Mới"}); err != nil {
		t.Fatal(err)
	}
	if patched["name"] != "Mới" {
		t.Fatalf("patched = %#v", patched)
	}
}
