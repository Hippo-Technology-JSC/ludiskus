package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"ludiskus/internal/domain"
)

func TestAttachmentContentContract(t *testing.T) {
	payload := []byte{137, 80, 255, 0, 13, 10}
	modified := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		method, header, value string
		status                int
		want                  []byte
	}{
		{"GET", "", "", 200, payload},
		{"HEAD", "", "", 200, nil},
		{"GET", "Range", "bytes=1-3", 206, payload[1:4]},
		{"GET", "If-None-Match", `"etag"`, 304, nil},
		{"GET", "Range", "bytes=100-200", 416, nil},
	} {
		t.Run(tc.method+tc.header+tc.value, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/api/v1/attachments/id/content", nil)
			if tc.header != "" {
				request.Header.Set(tc.header, tc.value)
			}
			response := httptest.NewRecorder()
			serveAttachmentContent(response, request, bytes.NewReader(payload), minio.ObjectInfo{ETag: "etag", LastModified: modified}, &domain.Attachment{FileName: "ảnh.png", ContentType: "image/png", Kind: "image"})
			if response.Code != tc.status {
				t.Fatalf("status %d want %d", response.Code, tc.status)
			}
			if tc.status != 416 && !bytes.Equal(response.Body.Bytes(), tc.want) {
				t.Fatalf("bytes %v want %v", response.Body.Bytes(), tc.want)
			}
			if response.Header().Get("Location") != "" {
				t.Fatal("must stream, not redirect")
			}
			if (tc.status != 416 && response.Header().Get("Cache-Control") != "private, no-cache") || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing protected content headers")
			}
			if response.Code == http.StatusPartialContent && response.Header().Get("Content-Range") != "bytes 1-3/6" {
				t.Fatal("missing content range")
			}
		})
	}
}
