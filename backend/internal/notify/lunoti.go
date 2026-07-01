// Package notify đẩy event sang lunoti (POST /api/v1/events) bằng token
// client-credentials của HipCore. Việc gửi đi qua hàng đợi outbox trong
// Postgres (repository) — package này chỉ lo HTTP + token. Xem docs/08.
package notify

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

	"ludiskus/internal/config"
)

type Client struct {
	cfg    *config.Config
	client *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func New(cfg *config.Config) *Client {
	return &Client{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}
}

// Enabled cho biết có cấu hình để gửi event sang lunoti không.
func (c *Client) Enabled() bool {
	return c.cfg.LunotiAPIURL != "" && c.cfg.LunotiClientID != "" && c.cfg.LunotiClientSecret != ""
}

// Event là payload đẩy lên lunoti.
type Event struct {
	EventType      string          `json:"event_type"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	Recipients     []Recipient     `json:"recipients,omitempty"`
	Channels       []string        `json:"channels,omitempty"`
}

type Recipient struct {
	ProfileUUID string `json:"profile_uuid"`
}

// Send POST event tới lunoti. Trả lỗi để worker retry.
func (c *Client) Send(ctx context.Context, ev Event) error {
	if !c.Enabled() {
		return fmt.Errorf("lunoti chưa cấu hình")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.LunotiAPIURL+"/api/v1/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("lunoti events status %d", res.StatusCode)
}

// Post gửi một POST JSON có xác thực tới lunoti (đăng ký event-type/template).
// Bỏ qua lỗi 409 (đã tồn tại) ở phía gọi nếu cần.
func (c *Client) Post(ctx context.Context, path string, body any) error {
	if !c.Enabled() {
		return fmt.Errorf("lunoti chưa cấu hình")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.LunotiAPIURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	if res.StatusCode == http.StatusConflict {
		return nil // đã tồn tại
	}
	return fmt.Errorf("lunoti POST %s status %d", path, res.StatusCode)
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-30*time.Second)) {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.LunotiClientID},
		"client_secret": {c.cfg.LunotiClientSecret},
		"scope":         {""},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.HipcoreURL+"/oauth/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth/token status %d", res.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	c.token = body.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(body.ExpiresIn) * time.Second)
	return c.token, nil
}
