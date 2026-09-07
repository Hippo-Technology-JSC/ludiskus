package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"ludiskus/internal/config"
)

func TestPresignPut_NoDial(t *testing.T) {
	// Even if S3PublicEndpoint is a completely unreachable host,
	// PresignPut should generate the presigned URL successfully without dialing.
	cfg := &config.Config{
		S3Endpoint:       "http://127.0.0.1:59999",
		S3PublicEndpoint: "http://localhost:9000",
		S3AccessKey:      "minio",
		S3SecretKey:      "minio12345",
		S3Bucket:         "ludiskus-attachments",
		PresignTTL:       5 * time.Minute,
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	urlStr, err := s.PresignPut(context.Background(), "spaces/test-uuid/2026/09/test.png")
	if err != nil {
		t.Fatalf("PresignPut() failed = %v", err)
	}

	if !strings.HasPrefix(urlStr, "http://localhost:9000/ludiskus-attachments/spaces/test-uuid/2026/09/test.png?") {
		t.Errorf("unexpected presign url: %s", urlStr)
	}
	if !strings.Contains(urlStr, "X-Amz-Signature=") {
		t.Errorf("presign url missing signature: %s", urlStr)
	}
}
