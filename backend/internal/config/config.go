// Package config nạp cấu hình từ biến môi trường (12-factor). Xem
// ludiskus/docs/12-trien-khai-docker.md.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Role       string // "api" | "worker"
	HTTPAddr   string
	DBDSN      string
	DBMaxConns int32
	RedisURL   string

	HipcoreURL      string
	HipcoreJWKSURL  string
	HipcoreAudience string
	// OAuth client của ludiskus (client_credentials) để đọc /api/profiles*,
	// /api/spaces*, /api/spaces/{uuid}/members (docs/05).
	HipcoreClientID     string
	HipcoreClientSecret string

	// Bí mật chia sẻ để verify danh tính do API gateway ký (đường tin-cậy-gateway).
	GatewaySecret string

	// lunoti (đẩy event reply/mention — interaction do lufami sở hữu).
	LunotiAPIURL       string
	LunotiClientID     string
	LunotiClientSecret string

	// lufami (điểm hipt) — dùng lại OAuth client HipCore ở trên làm credential;
	// chỉ cần biết địa chỉ lufami. Để trống LUFAMI_API_URL = tắt tích hợp điểm.
	LufamiURL string

	// MinIO / S3 (đính kèm — docs/07).
	S3Endpoint       string
	S3PublicEndpoint string
	S3AccessKey      string
	S3SecretKey      string
	S3Bucket         string
	S3UseSSL         bool

	// Cache (docs/05)
	CacheTTL            time.Duration
	ProfileSyncInterval time.Duration
	SpaceSyncInterval   time.Duration

	// Đính kèm (docs/07)
	MaxFileBytes   int64
	MaxAttachments int
	PresignTTL     time.Duration
	AttachTTL      time.Duration
	AllowedMIME    []string

	// Thông báo (docs/08)
	OutboxMaxAttempts int

	// LuComment (docs/comment/14).
	CommentEnabled         bool
	CommentResolveTimeout  time.Duration
	CommentTargetTTL       time.Duration
	CommentSummaryTTL      time.Duration
	CommentNotifyDebounce  time.Duration
	CommentBatchMax        int
	CommentMaxRevisions    int
	CommentNewProfileHours int
	CommentPublicRPM       int
	CommentPollInterval    time.Duration
	CommentAuditRetention  int
	CommentReconcileHour   int
	CommentThumbHosts      []string
	CommentServiceClients  string

	LogLevel string
}

func get(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func Load() (*Config, error) {
	cfg := &Config{
		Role:                  get("LUDISKUS_ROLE", "api"),
		HTTPAddr:              get("LUDISKUS_HTTP_ADDR", ":8080"),
		DBDSN:                 os.Getenv("LUDISKUS_DB_DSN"),
		RedisURL:              get("LUDISKUS_REDIS_URL", "redis://redis:6379/6"),
		HipcoreURL:            strings.TrimRight(get("HIPCORE_URL", "http://hipcore"), "/"),
		HipcoreAudience:       os.Getenv("HIPCORE_AUDIENCE"),
		HipcoreClientID:       os.Getenv("LUDISKUS_HIPCORE_CLIENT_ID"),
		HipcoreClientSecret:   os.Getenv("LUDISKUS_HIPCORE_CLIENT_SECRET"),
		GatewaySecret:         os.Getenv("GATEWAY_SIGNING_SECRET"),
		LunotiAPIURL:          strings.TrimRight(os.Getenv("LUNOTI_API_URL"), "/"),
		LunotiClientID:        os.Getenv("LUDISKUS_LUNOTI_CLIENT_ID"),
		LunotiClientSecret:    os.Getenv("LUDISKUS_LUNOTI_CLIENT_SECRET"),
		LufamiURL:             strings.TrimRight(os.Getenv("LUFAMI_API_URL"), "/"),
		S3Endpoint:            strings.TrimRight(get("LUDISKUS_S3_ENDPOINT", "http://minio:9000"), "/"),
		S3PublicEndpoint:      strings.TrimRight(get("LUDISKUS_S3_PUBLIC_ENDPOINT", "http://localhost:9000"), "/"),
		S3AccessKey:           get("LUDISKUS_S3_ACCESS_KEY", "minio"),
		S3SecretKey:           get("LUDISKUS_S3_SECRET_KEY", "minio12345"),
		S3Bucket:              get("LUDISKUS_S3_BUCKET", "ludiskus-attachments"),
		LogLevel:              get("LUDISKUS_LOG_LEVEL", "info"),
		CommentEnabled:        get("LUDISKUS_COMMENT_ENABLED", "true") != "false",
		CommentThumbHosts:     splitCSV(os.Getenv("LUDISKUS_COMMENT_THUMB_HOSTS")),
		CommentServiceClients: commentServiceClients(),
	}
	if cfg.DBDSN == "" {
		return nil, fmt.Errorf("LUDISKUS_DB_DSN is required")
	}
	cfg.S3UseSSL = strings.HasPrefix(get("LUDISKUS_S3_ENDPOINT", "http://minio:9000"), "https://")
	cfg.HipcoreJWKSURL = get("HIPCORE_JWKS_URL", cfg.HipcoreURL+"/api/.well-known/jwks.json")
	cfg.AllowedMIME = splitCSV(get("LUDISKUS_ALLOWED_MIME",
		"image/png,image/jpeg,image/gif,image/webp,application/pdf,text/plain,application/zip"))

	var err error
	if cfg.CacheTTL, err = time.ParseDuration(get("LUDISKUS_CACHE_TTL", "1h")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_CACHE_TTL: %w", err)
	}
	if cfg.ProfileSyncInterval, err = time.ParseDuration(get("LUDISKUS_PROFILE_SYNC_INTERVAL", "6h")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_PROFILE_SYNC_INTERVAL: %w", err)
	}
	if cfg.SpaceSyncInterval, err = time.ParseDuration(get("LUDISKUS_SPACE_SYNC_INTERVAL", "6h")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_SPACE_SYNC_INTERVAL: %w", err)
	}
	if cfg.PresignTTL, err = time.ParseDuration(get("LUDISKUS_PRESIGN_TTL", "5m")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_PRESIGN_TTL: %w", err)
	}
	if cfg.AttachTTL, err = time.ParseDuration(get("LUDISKUS_ATTACH_TTL", "24h")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_ATTACH_TTL: %w", err)
	}
	maxMB, err := strconv.Atoi(get("LUDISKUS_MAX_FILE_MB", "25"))
	if err != nil || maxMB < 1 {
		return nil, fmt.Errorf("LUDISKUS_MAX_FILE_MB: phải là số nguyên dương")
	}
	cfg.MaxFileBytes = int64(maxMB) * 1024 * 1024

	if cfg.MaxAttachments, err = strconv.Atoi(get("LUDISKUS_MAX_ATTACHMENTS", "10")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_MAX_ATTACHMENTS: %w", err)
	}
	if cfg.OutboxMaxAttempts, err = strconv.Atoi(get("LUDISKUS_OUTBOX_MAX_ATTEMPTS", "6")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_OUTBOX_MAX_ATTEMPTS: %w", err)
	}
	if cfg.CommentResolveTimeout, err = time.ParseDuration(get("LUDISKUS_COMMENT_RESOLVE_TIMEOUT", "3s")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_COMMENT_RESOLVE_TIMEOUT: %w", err)
	}
	if cfg.CommentTargetTTL, err = time.ParseDuration(get("LUDISKUS_COMMENT_TARGET_TTL", "6h")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_COMMENT_TARGET_TTL: %w", err)
	}
	if cfg.CommentSummaryTTL, err = time.ParseDuration(get("LUDISKUS_COMMENT_SUMMARY_TTL", "60s")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_COMMENT_SUMMARY_TTL: %w", err)
	}
	if cfg.CommentNotifyDebounce, err = time.ParseDuration(get("LUDISKUS_COMMENT_NOTIFY_DEBOUNCE", "5m")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_COMMENT_NOTIFY_DEBOUNCE: %w", err)
	}
	if cfg.CommentPollInterval, err = time.ParseDuration(get("LUDISKUS_COMMENT_POLL_INTERVAL", "60s")); err != nil {
		return nil, fmt.Errorf("LUDISKUS_COMMENT_POLL_INTERVAL: %w", err)
	}
	for key, dst := range map[string]*int{
		"LUDISKUS_COMMENT_BATCH_MAX":            &cfg.CommentBatchMax,
		"LUDISKUS_COMMENT_MAX_REVISIONS":        &cfg.CommentMaxRevisions,
		"LUDISKUS_COMMENT_NEW_PROFILE_HOURS":    &cfg.CommentNewProfileHours,
		"LUDISKUS_COMMENT_PUBLIC_RPM":           &cfg.CommentPublicRPM,
		"LUDISKUS_COMMENT_AUDIT_RETENTION_DAYS": &cfg.CommentAuditRetention,
		"LUDISKUS_COMMENT_RECONCILE_HOUR":       &cfg.CommentReconcileHour,
	} {
		defaults := map[string]string{
			"LUDISKUS_COMMENT_BATCH_MAX": "100", "LUDISKUS_COMMENT_MAX_REVISIONS": "10",
			"LUDISKUS_COMMENT_NEW_PROFILE_HOURS": "24", "LUDISKUS_COMMENT_PUBLIC_RPM": "120",
			"LUDISKUS_COMMENT_AUDIT_RETENTION_DAYS": "365", "LUDISKUS_COMMENT_RECONCILE_HOUR": "20",
		}
		v, parseErr := strconv.Atoi(get(key, defaults[key]))
		if parseErr != nil || v < 0 {
			return nil, fmt.Errorf("%s: phải là số nguyên không âm", key)
		}
		*dst = v
	}

	maxConns, err := strconv.Atoi(get("LUDISKUS_DB_MAX_CONNS", "10"))
	if err != nil {
		return nil, fmt.Errorf("LUDISKUS_DB_MAX_CONNS: %w", err)
	}
	cfg.DBMaxConns = int32(maxConns)
	return cfg, nil
}

func commentServiceClients() string {
	if configured := os.Getenv("LUDISKUS_COMMENT_SERVICE_CLIENTS"); configured != "" {
		return configured
	}
	pairs := []string{}
	for _, item := range []struct{ code, env string }{
		{"lumuse", "LUMUSE_HIPCORE_CLIENT_ID"}, {"lukode", "LUKODE_HIPCORE_CLIENT_ID"},
		{"luprojet", "LUPROJET_HIPCORE_CLIENT_ID"}, {"lukolek", "LUKOLEK_HIPCORE_CLIENT_ID"},
	} {
		if value := os.Getenv(item.env); value != "" {
			pairs = append(pairs, item.code+"="+value)
		}
	}
	return strings.Join(pairs, ",")
}

// MIMEAllowed kiểm tra content-type có nằm trong allowlist không.
func (c *Config) MIMEAllowed(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	for _, m := range c.AllowedMIME {
		if m == mime {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, strings.ToLower(v))
		}
	}
	return out
}
