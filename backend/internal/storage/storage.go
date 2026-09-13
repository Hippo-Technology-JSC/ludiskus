// Package storage bọc MinIO/S3: cấp presigned PUT/GET, kiểm tra & xoá object
// đính kèm. Dùng hai client: internal (container-to-container) để thao tác
// object, public (host trình duyệt truy cập được) để ký presigned URL. Xem
// docs/07.
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
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

type ImportResult struct {
	SizeBytes      int64
	ChecksumSHA256 string
}

func newClient(endpoint, accessKey, secretKey string) (*minio.Client, error) {
	host := strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")
	secure := strings.HasPrefix(endpoint, "https://")
	return minio.New(host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: "us-east-1",
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
		return s.internal.MakeBucket(ctx, s.cfg.S3Bucket, minio.MakeBucketOptions{Region: "us-east-1"})
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
		uInternal, errInternal := s.internal.PresignedPutObject(ctx, s.cfg.S3Bucket, objectKey, s.cfg.PresignTTL)
		if errInternal != nil {
			return "", err
		}
		return s.replacePublicEndpoint(uInternal.String()), nil
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
		uInternal, errInternal := s.internal.PresignedGetObject(ctx, s.cfg.S3Bucket, objectKey, s.cfg.PresignTTL, reqParams)
		if errInternal != nil {
			return "", err
		}
		return s.replacePublicEndpoint(uInternal.String()), nil
	}
	return u.String(), nil
}

func (s *Store) replacePublicEndpoint(rawURL string) string {
	if s.cfg.S3PublicEndpoint == "" || s.cfg.S3PublicEndpoint == s.cfg.S3Endpoint {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	pub, err := url.Parse(s.cfg.S3PublicEndpoint)
	if err != nil {
		return rawURL
	}
	u.Scheme = pub.Scheme
	u.Host = pub.Host
	return u.String()
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

// Inspect reads a bounded object once to verify the bytes uploaded by the
// browser before an editor asset becomes readable or attachable.
func (s *Store) Inspect(ctx context.Context, objectKey string, maxBytes int64) (size int64, storedType, detectedType, checksum string, err error) {
	info, err := s.internal.StatObject(ctx, s.cfg.S3Bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return 0, "", "", "", err
	}
	if info.Size <= 0 || info.Size > maxBytes {
		return info.Size, info.ContentType, "", "", fmt.Errorf("kích thước object không hợp lệ")
	}
	object, err := s.internal.GetObject(ctx, s.cfg.S3Bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return 0, "", "", "", err
	}
	defer object.Close()
	buffer := make([]byte, 512)
	n, readErr := io.ReadFull(object, buffer)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return 0, "", "", "", readErr
	}
	detectedType = http.DetectContentType(buffer[:n])
	hash := sha256.New()
	if _, err = hash.Write(buffer[:n]); err != nil {
		return 0, "", "", "", err
	}
	remaining := info.Size - int64(n)
	copied, err := io.Copy(hash, io.LimitReader(object, remaining+1))
	if err != nil {
		return 0, "", "", "", err
	}
	if copied != remaining {
		return 0, "", "", "", fmt.Errorf("kích thước object thay đổi khi kiểm tra")
	}
	return info.Size, info.ContentType, detectedType, hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Store) Remove(ctx context.Context, objectKey string) error {
	return s.internal.RemoveObject(ctx, s.cfg.S3Bucket, objectKey, minio.RemoveObjectOptions{})
}

func readExactBounded(reader io.Reader, expectedSize int64) ([]byte, error) {
	if expectedSize <= 0 {
		return nil, fmt.Errorf("kích thước tệp nguồn không hợp lệ")
	}
	data, err := io.ReadAll(io.LimitReader(reader, expectedSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expectedSize {
		return nil, fmt.Errorf("kích thước tệp nguồn không khớp")
	}
	return data, nil
}

// optimizeImageLosslessly currently handles PNG. It decodes and re-encodes the
// exact pixels with the standard library's best compression and only selects
// the result when it is smaller. Other image formats pass through unchanged.
func optimizeImageLosslessly(data []byte, contentType string, maxPixels uint64) ([]byte, error) {
	if strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0])) != "image/png" {
		return data, nil
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("PNG nguồn không hợp lệ: %w", err)
	}
	pixels := uint64(config.Width) * uint64(config.Height)
	if config.Width <= 0 || config.Height <= 0 || pixels > maxPixels {
		return nil, fmt.Errorf("PNG nguồn vượt giới hạn số pixel")
	}
	image, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("không thể giải mã PNG nguồn: %w", err)
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&output, image); err != nil {
		return nil, fmt.Errorf("không thể nén PNG nguồn: %w", err)
	}
	if output.Len() >= len(data) {
		return data, nil
	}
	return output.Bytes(), nil
}

func (s *Store) putPrepared(ctx context.Context, objectKey, contentType string, data []byte) (*ImportResult, error) {
	info, err := s.internal.PutObject(ctx, s.cfg.S3Bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return nil, err
	}
	if info.Size != int64(len(data)) {
		_ = s.Remove(ctx, objectKey)
		return nil, fmt.Errorf("kích thước object đích không khớp")
	}
	sum := sha256.Sum256(data)
	return &ImportResult{SizeBytes: info.Size, ChecksumSHA256: hex.EncodeToString(sum[:])}, nil
}

// ImportURLPrepared validates the source bytes, applies supported lossless
// image optimization, then performs the first write of the destination object.
func (s *Store) ImportURLPrepared(ctx context.Context, sourceURL, objectKey, contentType string, expectedSize int64, expectedChecksum *string) (*ImportResult, error) {
	u, err := url.Parse(sourceURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("URL nguồn không hợp lệ")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("nguồn trả status %d", res.StatusCode)
	}
	data, err := readExactBounded(res.Body, expectedSize)
	if err != nil {
		return nil, err
	}
	sourceSum := sha256.Sum256(data)
	if expectedChecksum != nil && *expectedChecksum != "" && !strings.EqualFold(*expectedChecksum, hex.EncodeToString(sourceSum[:])) {
		return nil, fmt.Errorf("checksum tệp nguồn không khớp")
	}
	data, err = optimizeImageLosslessly(data, contentType, 40_000_000)
	if err != nil {
		return nil, err
	}
	return s.putPrepared(ctx, objectKey, contentType, data)
}

// CopyPrepared reads an existing object through the same bounded preparation
// path so a Personal Files import is optimized before its destination is saved.
func (s *Store) CopyPrepared(ctx context.Context, sourceKey, targetKey, contentType string, expectedSize int64) (*ImportResult, error) {
	object, err := s.internal.GetObject(ctx, s.cfg.S3Bucket, sourceKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()
	data, err := readExactBounded(object, expectedSize)
	if err != nil {
		return nil, err
	}
	data, err = optimizeImageLosslessly(data, contentType, 40_000_000)
	if err != nil {
		return nil, err
	}
	return s.putPrepared(ctx, targetKey, contentType, data)
}
