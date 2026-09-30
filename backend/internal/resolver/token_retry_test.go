package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"ludiskus/internal/config"
	"ludiskus/internal/domain"
)

func TestProviderRefreshesRejectedTokenOnce(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			var issued, calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/oauth/token" {
					n := issued.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("token-%d", n), "expires_in": 3600})
					return
				}
				calls.Add(1)
				if req.Header.Get("Authorization") == "Bearer token-1" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if req.Header.Get("Authorization") != "Bearer token-2" {
					t.Errorf("unexpected authorization after refresh")
				}
				result := Result{Exists: true, Type: "task", ID: "item-1", Visibility: "space", State: "active"}
				if batch {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []Result{result}})
				} else {
					_ = json.NewEncoder(w).Encode(result)
				}
			}))
			defer server.Close()
			r := New(nil, nil, &config.Config{HipcoreURL: server.URL, HipcoreClientID: "client", HipcoreClientSecret: "secret", CommentResolveTimeout: time.Second})
			ref := domain.ResourceRef{Service: "luprojet", Type: "task", ID: "item-1"}
			if batch {
				items, status, err := r.callBatch(context.Background(), server.URL, "resource-context", []domain.ResourceRef{ref})
				if err != nil || status != http.StatusOK || len(items) != 1 || items[0].ID != ref.ID {
					t.Fatalf("batch status=%d items=%v err=%v", status, items, err)
				}
			} else {
				item, status, err := r.call(context.Background(), server.URL, "resource-context", ref)
				if err != nil || status != http.StatusOK || item.ID != ref.ID {
					t.Fatalf("single status=%d item=%v err=%v", status, item, err)
				}
			}
			if issued.Load() != 2 || calls.Load() != 2 {
				t.Fatalf("issued=%d calls=%d", issued.Load(), calls.Load())
			}
		})
	}
}

func TestProviderStopsAfterSecondUnauthorized(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	r := New(nil, nil, &config.Config{HipcoreURL: server.URL, HipcoreClientID: "client", HipcoreClientSecret: "secret", CommentResolveTimeout: time.Second})
	_, status, err := r.call(context.Background(), server.URL, "resource-context", domain.ResourceRef{Service: "luprojet", Type: "task", ID: "item-1"})
	if err != nil || status != http.StatusUnauthorized || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d err=%v", status, calls.Load(), err)
	}
}
