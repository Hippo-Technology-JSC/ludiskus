// Package storage bọc MinIO/S3: cấp presigned PUT/GET, kiểm tra & xoá object
// đính kèm. Dùng hai client: internal (container-to-container) để thao tác
// object, public (host trình duyệt truy cập được) để ký presigned URL. Xem
// docs/07.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"ludiskus/internal/config"
)

type Store struct {
	internal *minio.Client
	public   *minio.Client
	cfg      *config.Config
}

func newClient(endpoint, accessKey, secretKey string) (*minio.Client, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	secure := strings.HasPrefix(endpoint, "https://")
	return minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
}

// New khởi tạo client MinIO. Trả (nil, nil) nếu chưa cấu hình endpoint (đính kèm
// bị vô hiệu nhưng service vẫn chạy).
func New(cfg *config.Config) (*Store, error) {
	if cfg.S3Endpoint == "" {
		return nil, nil
	}
	internal, err := newClient(cfg.S3Endpoint, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		return nil, fmt.Errorf("minio internal client: %w", err)
	}
	public, err := newClient(cfg.S3PublicEndpoint, cfg.S3AccessKey, cfg.S3SecretKey)
	if err != nil {
		return nil, fmt.Errorf("minio public client: %w", err)
	}
	return &Store{internal: internal, public: public, cfg: cfg}, nil
}

// EnsureBucket tạo bucket nếu chưa có (idempotent).
func (s *Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.internal.BucketExists(ctx, s.cfg.S3Bucket)
	if err != nil {
		return err
	}
	if !exists {
		return s.internal.MakeBucket(ctx, s.cfg.S3Bucket, minio.MakeBucketOptions{})
	}
	return nil
}

func (s *Store) Ready(ctx context.Context) error {
	_, err := s.internal.BucketExists(ctx, s.cfg.S3Bucket)
	return err
}

// PresignPut cấp URL để FE PUT trực tiếp lên MinIO (TTL ngắn).
func (s *Store) PresignPut(ctx context.Context, objectKey string) (string, error) {
	u, err := s.public.PresignedPutObject(ctx, s.cfg.S3Bucket, objectKey, s.cfg.PresignTTL)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PresignGet cấp URL tải xuống cho Space riêng tư.
func (s *Store) PresignGet(ctx context.Context, objectKey, fileName string) (string, error) {
	reqParams := url.Values{}
	if fileName != "" {
		reqParams.Set("response-content-disposition", "inline; filename=\""+fileName+"\"")
	}
	u, err := s.public.PresignedGetObject(ctx, s.cfg.S3Bucket, objectKey, s.cfg.PresignTTL, reqParams)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// PublicURL URL không ký (Space công khai).
func (s *Store) PublicURL(objectKey string) string {
	return fmt.Sprintf("%s/%s/%s", s.cfg.S3PublicEndpoint, s.cfg.S3Bucket, objectKey)
}

// Stat trả kích thước + content-type thực của object (xác nhận đã upload).
func (s *Store) Stat(ctx context.Context, objectKey string) (size int64, contentType string, err error) {
	info, err := s.internal.StatObject(ctx, s.cfg.S3Bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return 0, "", err
	}
	return info.Size, info.ContentType, nil
}

func (s *Store) Remove(ctx context.Context, objectKey string) error {
	return s.internal.RemoveObject(ctx, s.cfg.S3Bucket, objectKey, minio.RemoveObjectOptions{})
}

func (s *Store) ImportURL(ctx context.Context, sourceURL, objectKey, contentType string, expectedSize int64, expectedChecksum *string) error {
	u, err := url.Parse(sourceURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("URL nguồn không hợp lệ")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("nguồn trả status %d", res.StatusCode)
	}
	hash := sha256.New()
	reader := io.TeeReader(io.LimitReader(res.Body, expectedSize+1), hash)
	info, err := s.internal.PutObject(ctx, s.cfg.S3Bucket, objectKey, reader, expectedSize, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return err
	}
	if info.Size != expectedSize {
		_ = s.Remove(ctx, objectKey)
		return fmt.Errorf("kích thước tệp nguồn không khớp")
	}
	if expectedChecksum != nil && *expectedChecksum != "" && !strings.EqualFold(*expectedChecksum, hex.EncodeToString(hash.Sum(nil))) {
		_ = s.Remove(ctx, objectKey)
		return fmt.Errorf("checksum tệp nguồn không khớp")
	}
	return nil
}

func (s *Store) Copy(ctx context.Context, sourceKey, targetKey string) error {
	_, err := s.internal.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.cfg.S3Bucket, Object: targetKey},
		minio.CopySrcOptions{Bucket: s.cfg.S3Bucket, Object: sourceKey})
	return err
}
