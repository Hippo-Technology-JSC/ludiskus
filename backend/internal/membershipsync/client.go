// Package membershipsync keeps active Space memberships synchronized with HipCore.
// Canonical source: tm/tools/membership-sync; distributed by generate.py.
package membershipsync

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
	"strings"
	"sync"
	"time"
)

const path = "/api/internal/membership-snapshot"
const maxAge = 5 * time.Second

type Member struct {
	ProfileUUID string `json:"profile_uuid"`
	Role        string `json:"role"`
}
type snapshot struct {
	Revision        string   `json:"revision"`
	Members         []Member `json:"members"`
	refreshed, used time.Time
}
type Apply func(context.Context, string, []Member) error

type Client struct {
	apply        Apply
	base, secret string
	http         *http.Client
	mu           sync.Mutex // serializes refreshes: an older response cannot overwrite a newer one
	spaces       map[string]snapshot
}

var current *Client // configured once before serving requests

// Start is opt-in with the existing gateway signing key. Standalone services
// without that trust relationship retain their original membership resolvers.
func Start(ctx context.Context, base, secret string, apply Apply) {
	if secret == "" {
		return
	}
	current = New(base, secret)
	current.apply = apply
	go current.Run(ctx)
}
func New(base, secret string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), secret: secret,
		http: &http.Client{Timeout: 3 * time.Second}, spaces: map[string]snapshot{}}
}

// Members returns enabled=true even on failure; callers MUST propagate the error
// rather than falling back to a long-lived membership cache.
func Members(ctx context.Context, space string) ([]Member, bool, error) {
	if current == nil {
		return nil, false, nil
	}
	ms, err := current.Members(ctx, space)
	return ms, true, err
}
func (c *Client) Members(ctx context.Context, space string) ([]Member, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.spaces[space]
	if !ok || time.Since(s.refreshed) >= maxAge {
		if err := c.refresh(ctx, []string{space}); err != nil {
			return nil, err
		}
		s = c.spaces[space]
	}
	s.used = time.Now()
	c.spaces[space] = s
	return append([]Member(nil), s.Members...), nil
}

// Run refreshes active Spaces every second, including when no request arrives.
// Idle Spaces leave the subscription after 10 minutes. Reconnection always asks
// for current snapshots, so no event replay, dropped notification or restart can
// restore a revoked membership. No permissions or resource ACLs are synthesized.
func (c *Client) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			keys := []string{}
			for k, s := range c.spaces {
				if time.Since(s.used) > 10*time.Minute {
					delete(c.spaces, k)
				} else {
					keys = append(keys, k)
				}
			}
			for len(keys) > 0 {
				n := min(len(keys), 100)
				if err := c.refresh(ctx, keys[:n]); err != nil {
					slog.Warn("HipCore membership synchronization failed", "error", err)
					break
				}
				keys = keys[n:]
			}
			c.mu.Unlock()
		}
	}
}
func (c *Client) refresh(ctx context.Context, keys []string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	known := map[string]string{}
	for _, k := range keys {
		known[k] = c.spaces[k].Revision
	}
	body, _ := json.Marshal(map[string]any{"spaces": known})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := fmt.Sprint(time.Now().Unix())
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, []byte(c.secret))
	fmt.Fprintf(mac, "%s\nPOST\n%s\n%x", timestamp, path, digest)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Membership-Timestamp", timestamp)
	req.Header.Set("X-Membership-Signature", hex.EncodeToString(mac.Sum(nil)))
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("membership snapshot: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("membership snapshot: HTTP %d", res.StatusCode)
	}
	var payload struct {
		Spaces map[string]snapshot `json:"spaces"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&payload); err != nil {
		return fmt.Errorf("membership snapshot: %w", err)
	}
	now := time.Now()
	// Validate the complete response before publishing any part of the batch.
	for _, k := range keys {
		s, ok := payload.Spaces[k]
		if !ok || s.Revision == "" {
			return fmt.Errorf("membership snapshot missing Space %s", k)
		}
		old, exists := c.spaces[k]
		if s.Members == nil && (!exists || old.Revision != s.Revision) {
			return fmt.Errorf("membership snapshot missing members")
		}
		for _, m := range s.Members {
			if m.ProfileUUID == "" || (m.Role != "owner" && m.Role != "admin" && m.Role != "member") {
				return fmt.Errorf("membership snapshot invalid member")
			}
		}
	}
	for _, k := range keys {
		s := payload.Spaces[k]
		if s.Members != nil && c.apply != nil {
			if err := c.apply(ctx, k, s.Members); err != nil {
				return fmt.Errorf("persist membership snapshot: %w", err)
			}
		}
	}
	for _, k := range keys {
		s := payload.Spaces[k]
		old := c.spaces[k]
		if s.Members == nil {
			s.Members = old.Members
		}
		s.refreshed = now
		s.used = old.used
		if s.used.IsZero() {
			s.used = now
		}
		c.spaces[k] = s
	}
	return nil
}
