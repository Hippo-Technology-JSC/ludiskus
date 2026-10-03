package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"ludiskus/internal/config"
	"ludiskus/internal/domain"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func privateFixture(t *testing.T) (*Store, func() int) {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || !strings.HasPrefix(r.URL.Path, "/attachments/") {
			http.Error(w, "signed internal path only", 403)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			if r.Header.Get("If-None-Match") != "*" {
				t.Error("missing conditional PUT")
			}
			if _, ok := objects[r.URL.Path]; ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(412)
				fmt.Fprint(w, "<Error><Code>PreconditionFailed</Code></Error>")
				return
			}
			var reader io.Reader = r.Body
			if r.Header.Get("X-Amz-Decoded-Content-Length") != "" {
				reader = httputil.NewChunkedReader(reader)
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			objects[r.URL.Path] = data
			w.Header().Set("ETag", `"etag"`)
			return
		}
		data, ok := objects[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(404)
			fmt.Fprint(w, "<Error><Code>NoSuchKey</Code></Error>")
			return
		}
		w.Header().Set("ETag", `"etag"`)
		w.Header().Set("Content-Type", "image/png")
		http.ServeContent(w, r, r.URL.Path, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC), bytes.NewReader(data))
	}))
	t.Cleanup(server.Close)
	store, err := New(&config.Config{S3Endpoint: server.URL, S3Bucket: "attachments", S3AccessKey: "key", S3SecretKey: "secret", AllowedMIME: []string{"image/png", "image/webp", "text/plain", "text/markdown", "text/csv", "application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	return store, func() int { mu.Lock(); defer mu.Unlock(); return len(objects) }
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func TestPrivateUploadRoundTripAndNoOverwrite(t *testing.T) {
	store, count := privateFixture(t)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	body := pngBytes(t)
	result, err := store.PutUpload(context.Background(), "spaces/space/image", "image/png", int64(len(body)), 1024, bytes.NewReader(body))
	if err != nil || result.SizeBytes != int64(len(body)) || len(result.ChecksumSHA256) != 64 {
		t.Fatalf("%+v %v", result, err)
	}
	object, info, err := store.Open(context.Background(), "spaces/space/image")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(object)
	object.Close()
	if err != nil || !bytes.Equal(data, body) || info.Size != int64(len(body)) {
		t.Fatalf("roundtrip %v %+v", err, info)
	}
	_, err = store.PutUpload(context.Background(), "spaces/space/image", "image/png", int64(len(body)), 1024, bytes.NewReader(body))
	if !errors.Is(err, domain.ErrConflict) || count() != 1 {
		t.Fatalf("overwrite %v count=%d", err, count())
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("temp files: %v %v", files, err)
	}
}
func TestUploadValidatesBeforeS3(t *testing.T) {
	store, count := privateFixture(t)
	for _, tc := range []struct {
		name, typ, body string
		size, max       int64
		want            error
	}{
		{"empty", "text/plain", "", 0, 1024, domain.ErrTooLarge},
		{"declared limit", "text/plain", "hello", 5, 4, domain.ErrTooLarge},
		{"actual extra bytes", "text/plain", "hello", 4, 1024, domain.ErrTooLarge},
		{"truncated", "text/plain", "hey", 5, 1024, domain.ErrValidation},
		{"wrong mime", "image/png", "hello", 5, 1024, domain.ErrValidation},
		{"invalid json", "application/json", "{bad}", 5, 1024, domain.ErrValidation},
		{"trailing json", "application/json", "{} {}", 5, 1024, domain.ErrValidation},
		{"fake webp", "image/webp", "RIFF\x00\x00\x00\x00WEBPVP8 ", 16, 1024, domain.ErrValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.PutUpload(context.Background(), tc.name, tc.typ, tc.size, tc.max, strings.NewReader(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	if count() != 0 {
		t.Fatalf("wrote invalid objects: %d", count())
	}
	for _, typ := range []string{"text/markdown", "text/csv", "application/json"} {
		body := "hello"
		if typ == "application/json" {
			body = `{"ok":true}`
		}
		_, err := store.PutUpload(context.Background(), typ, typ, int64(len(body)), 1024, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
	}
}
