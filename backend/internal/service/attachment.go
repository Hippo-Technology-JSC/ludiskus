package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"ludiskus/internal/domain"
)

type PresignInput struct {
	SpaceUUID   string `json:"spaceUuid"`
	FileName    string `json:"fileName"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

type PresignResult struct {
	AttachmentID string `json:"attachmentId"`
	UploadURL    string `json:"uploadUrl"`
	ObjectKey    string `json:"objectKey"`
}

// PresignUpload cấp URL PUT để FE upload trực tiếp lên MinIO (docs/07 §7.1).
func (s *Service) PresignUpload(ctx context.Context, profileUUID string, in PresignInput) (*PresignResult, error) {
	if s.store == nil {
		return nil, fmt.Errorf("%w: đính kèm chưa được cấu hình", domain.ErrValidation)
	}
	forum, err := s.requireView(ctx, in.SpaceUUID, profileUUID)
	if err != nil {
		return nil, err
	}
	if err := s.requirePost(ctx, forum, profileUUID); err != nil {
		return nil, err
	}
	if in.FileName == "" {
		return nil, fmt.Errorf("%w: fileName là bắt buộc", domain.ErrValidation)
	}
	if !s.cfg.MIMEAllowed(in.ContentType) {
		return nil, fmt.Errorf("%w: loại tệp không được phép", domain.ErrValidation)
	}
	if in.SizeBytes <= 0 || in.SizeBytes > s.cfg.MaxFileBytes {
		return nil, fmt.Errorf("%w: kích thước vượt giới hạn", domain.ErrTooLarge)
	}
	kind := "file"
	if strings.HasPrefix(strings.ToLower(in.ContentType), "image/") {
		kind = "image"
	}
	now := time.Now().UTC()
	objectKey := fmt.Sprintf("spaces/%s/%04d/%02d/%s/%s",
		in.SpaceUUID, now.Year(), now.Month(), randSuffix(), safeName(in.FileName))

	att, err := s.repo.CreateAttachment(ctx, domain.Attachment{
		SpaceUUID: in.SpaceUUID, UploaderProfileUUID: profileUUID, ObjectKey: objectKey,
		FileName: in.FileName, ContentType: in.ContentType, SizeBytes: in.SizeBytes, Kind: kind,
	})
	if err != nil {
		return nil, err
	}
	putURL, err := s.store.PresignPut(ctx, objectKey)
	if err != nil {
		return nil, err
	}
	return &PresignResult{AttachmentID: att.ID, UploadURL: putURL, ObjectKey: objectKey}, nil
}

// AttachmentURL trả URL xem/tải (presigned cho Space riêng tư, public cho Space
// công khai) — docs/07 §7.2.
func (s *Service) AttachmentURL(ctx context.Context, profileUUID, id string) (string, error) {
	if s.store == nil {
		return "", domain.ErrNotFound
	}
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return "", err
	}
	if _, err := s.requireView(ctx, att.SpaceUUID, profileUUID); err != nil {
		return "", err
	}
	if s.spaceIsPublic(ctx, att.SpaceUUID) {
		return s.store.PublicURL(att.ObjectKey), nil
	}
	return s.store.PresignGet(ctx, att.ObjectKey, att.FileName)
}

func (s *Service) DeleteAttachment(ctx context.Context, profileUUID, id string) error {
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	if att.UploaderProfileUUID != profileUUID && !canModerate(s.role(ctx, att.SpaceUUID, profileUUID)) {
		return domain.ErrForbidden
	}
	if s.store != nil {
		s.store.Remove(ctx, att.ObjectKey)
	}
	return s.repo.DeleteAttachment(ctx, id)
}

func safeName(name string) string {
	name = path.Base(name)
	name = strings.ReplaceAll(name, " ", "_")
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" || out == "." {
		return "file"
	}
	return out
}
