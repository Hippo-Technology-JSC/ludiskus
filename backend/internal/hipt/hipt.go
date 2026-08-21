// Package hipt là client tối giản gọi lufami để trao thưởng điểm hipt cho user
// khi họ hoàn thành hoạt động trong ludiskus. Xem lufami/docs/hipt-integration.md.
// Dùng lại chính OAuth client HipCore của ludiskus (client id = claim aud được
// đăng ký trong registry điểm của lufami).
package hipt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Client struct {
	base         string
	hipcoreURL   string
	clientID     string
	clientSecret string
	http         *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func New(lufamiURL, hipcoreURL, clientID, clientSecret string) *Client {
	return &Client{
		base:         strings.TrimRight(lufamiURL, "/"),
		hipcoreURL:   strings.TrimRight(hipcoreURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         &http.Client{Timeout: 15 * time.Second},
	}
}

// Enabled: đủ cấu hình để gọi lufami không.
func (c *Client) Enabled() bool {
	return c.base != "" && c.clientID != "" && c.clientSecret != ""
}

// UpsertTask đăng ký/cập nhật một nhiệm vụ (idempotent).
func (c *Client) UpsertTask(ctx context.Context, code string, body map[string]any) error {
	if !c.Enabled() {
		return nil
	}
	return c.do(ctx, http.MethodPut, "/api/v1/s2s/tasks/"+url.PathEscape(code), body, nil)
}

// Complete báo user hoàn thành nhiệm vụ → lufami trả thưởng. idempotencyKey PHẢI
// ổn định theo sự kiện để retry an toàn (không double-pay).
func (c *Client) Complete(ctx context.Context, code, profileUUID, idempotencyKey string, evidence map[string]any) error {
	if !c.Enabled() {
		return nil
	}
	return c.do(ctx, http.MethodPost, "/api/v1/s2s/tasks/"+url.PathEscape(code)+"/complete", map[string]any{
		"profileUuid":    profileUUID,
		"idempotencyKey": idempotencyKey,
		"evidence":       evidence,
	}, nil)
}

func (c *Client) UpsertInteractionResources(ctx context.Context, resources any) error {
	if !c.Enabled() {
		return fmt.Errorf("lufami interaction chưa cấu hình")
	}
	return c.do(ctx, http.MethodPost, "/api/v1/s2s/interactions/resources",
		map[string]any{"data": resources}, nil)
}

func (c *Client) InvalidateInteractionResources(
	ctx context.Context, refs any, reason string,
) error {
	if !c.Enabled() {
		return fmt.Errorf("lufami interaction chưa cấu hình")
	}
	return c.do(ctx, http.MethodPost, "/api/v1/s2s/interactions/resources/invalidate",
		map[string]any{"refs": refs, "reason": reason}, nil)
}

func (c *Client) BackfillInteractions(ctx context.Context, batches any) error {
	if !c.Enabled() {
		return fmt.Errorf("lufami interaction chưa cấu hình")
	}
	return c.do(ctx, http.MethodPost, "/api/v1/s2s/interactions/backfill",
		map[string]any{"data": batches}, nil)
}

type AggregateSync struct {
	Ref struct {
		Service string `json:"service"`
		Type    string `json:"type"`
		ID      string `json:"id"`
	} `json:"ref"`
	Counts struct {
		Like int64 `json:"like"`
	} `json:"counts"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (c *Client) InteractionAggregates(ctx context.Context, updatedSince, cursor string) ([]AggregateSync, string, error) {
	if !c.Enabled() {
		return nil, "", nil
	}
	path := "/api/v1/s2s/interactions/aggregates?service=ludiskus&type=comment&limit=100&updatedSince=" + url.QueryEscape(updatedSince)
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	var page struct {
		Data       []AggregateSync `json:"data"`
		NextCursor string          `json:"nextCursor"`
	}
	err := c.do(ctx, http.MethodGet, path, nil, &page)
	return page.Data, page.NextCursor, err
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	var r *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("lufami %s %s: status %d", method, path, res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-30*time.Second)) {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"scope":         {""},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.hipcoreURL+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth/token status %d", res.StatusCode)
	}
	var b struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&b); err != nil {
		return "", err
	}
	c.token = b.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(b.ExpiresIn) * time.Second)
	return c.token, nil
}
