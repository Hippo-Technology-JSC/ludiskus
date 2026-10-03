package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	_ "golang.org/x/image/webp"
	"ludiskus/internal/domain"
)

// Text formats share a signature. Preserve their declared MIME only after
// sniffing plain text; JSON also has to parse successfully before it is stored.
func detectedMIME(header []byte, declared string) string {
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(header))
	declared, _, _ = mime.ParseMediaType(declared)
	if detected == "text/plain" {
		switch declared {
		case "text/plain", "text/markdown", "text/csv", "application/json":
			return declared
		}
	}
	return detected
}

// PutUpload validates actual bytes before any S3 write. A single conditional
// PUT prevents concurrent requests from overwriting an already completed slot.
func (s *Store) PutUpload(ctx context.Context, key, contentType string, expected, maximum int64, reader io.Reader) (ImportResult, error) {
	if expected <= 0 || expected > maximum {
		return ImportResult{}, domain.ErrTooLarge
	}
	f, err := os.CreateTemp("", "ludiskus-upload-*")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(reader, expected+1))
	if err != nil {
		return ImportResult{}, err
	}
	if n > expected {
		return ImportResult{}, domain.ErrTooLarge
	}
	if n != expected {
		return ImportResult{}, fmt.Errorf("%w: kích thước không khớp", domain.ErrValidation)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, err
	}
	header := make([]byte, 512)
	count, err := io.ReadFull(f, header)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return ImportResult{}, err
	}
	if detectedMIME(header[:count], contentType) != contentType || !s.cfg.MIMEAllowed(contentType) {
		return ImportResult{}, fmt.Errorf("%w: loại tệp không khớp dữ liệu", domain.ErrValidation)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, err
	}
	if contentType == "application/json" {
		decoder := json.NewDecoder(f)
		var value any
		if decoder.Decode(&value) != nil || decoder.Decode(&value) != io.EOF {
			return ImportResult{}, domain.ErrValidation
		}
	} else if strings.HasPrefix(contentType, "image/") {
		cfg, _, e := image.DecodeConfig(f)
		if e != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
			return ImportResult{}, domain.ErrValidation
		}
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return ImportResult{}, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return ImportResult{}, err
	}
	options := minio.PutObjectOptions{ContentType: contentType, DisableMultipart: true}
	options.SetMatchETagExcept("*")
	info, err := s.internal.PutObject(ctx, s.cfg.S3Bucket, key, f, n, options)
	if minio.ToErrorResponse(err).StatusCode == http.StatusPreconditionFailed {
		return ImportResult{}, domain.ErrConflict
	}
	if err != nil {
		return ImportResult{}, err
	}
	if info.Size != n {
		return ImportResult{}, fmt.Errorf("S3 upload size mismatch")
	}
	return ImportResult{SizeBytes: n, ChecksumSHA256: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}

func (s *Store) Open(ctx context.Context, key string) (*minio.Object, minio.ObjectInfo, error) {
	info, err := s.internal.StatObject(ctx, s.cfg.S3Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, info, err
	}
	object, err := s.internal.GetObject(ctx, s.cfg.S3Bucket, key, minio.GetObjectOptions{})
	return object, info, err
}
