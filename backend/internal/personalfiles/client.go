package personalfiles

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
	"ludiskus/internal/domain"
)

type ResolvedItem struct {
	Ordinal         int     `json:"ordinal"`
	PersonalFileID  string  `json:"personalFileId"`
	SourceService   string  `json:"sourceService"`
	SourceFileID    string  `json:"sourceFileId"`
	FileName        string  `json:"fileName"`
	MimeType        string  `json:"mimeType"`
	SizeBytes       int64   `json:"sizeBytes"`
	ChecksumSHA256  *string `json:"checksumSha256,omitempty"`
	TransferURL     string  `json:"transferUrl,omitempty"`
	NativeReference string  `json:"nativeReference,omitempty"`
}

type RedeemResult struct {
	SessionID  string         `json:"sessionId"`
	LeaseUntil time.Time      `json:"leaseUntil"`
	Items      []ResolvedItem `json:"items"`
}

type Client struct {
	cfg      *config.Config
	http     *http.Client
	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func New(cfg *config.Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
}
func (c *Client) Enabled() bool {
	return c != nil && c.cfg.LufamiURL != "" && c.cfg.HipcoreClientID != "" && c.cfg.HipcoreClientSecret != ""
}
func (c *Client) Send(ctx context.Context, items []domain.PersonalFileSyncItem) error {
	if !c.Enabled() {
		return fmt.Errorf("lufami personal files chưa cấu hình")
	}
	events := make([]json.RawMessage, 0, len(items))
	for _, i := range items {
		events = append(events, i.Payload)
	}
	raw, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return err
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.LufamiURL+"/api/v1/s2s/personal-files/events:batch", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("lufami personal files status %d", res.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	if !c.Enabled() {
		return fmt.Errorf("lufami personal files chưa cấu hình")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.LufamiURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var failure struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		_ = json.NewDecoder(res.Body).Decode(&failure)
		if failure.Error.Message != "" {
			return fmt.Errorf("lufami personal files %s: %s", failure.Error.Code, failure.Error.Message)
		}
		return fmt.Errorf("lufami personal files status %d", res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

func (c *Client) Redeem(ctx context.Context, selectionToken, actorUserID, purpose, idempotencyKey string) (*RedeemResult, error) {
	var out RedeemResult
	err := c.post(ctx, "/api/v1/s2s/personal-files/selections/redeem", map[string]string{
		"selectionToken": selectionToken, "actorUserId": actorUserID, "purpose": purpose, "idempotencyKey": idempotencyKey,
	}, &out)
	return &out, err
}

func (c *Client) Complete(ctx context.Context, selectionToken, actorUserID, idempotencyKey string, result any) error {
	return c.post(ctx, "/api/v1/s2s/personal-files/selections/complete", map[string]any{
		"selectionToken": selectionToken, "actorUserId": actorUserID, "idempotencyKey": idempotencyKey, "result": result,
	}, &struct{}{})
}
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-30*time.Second)) {
		return c.token, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {c.cfg.HipcoreClientID}, "client_secret": {c.cfg.HipcoreClientSecret}, "scope": {""}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.HipcoreURL+"/oauth/token", strings.NewReader(form.Encode()))
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
