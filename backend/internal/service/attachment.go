package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"ludiskus/internal/auth"
	"ludiskus/internal/domain"
)

type PresignInput struct {
	SpaceUUID   string              `json:"spaceUuid"`
	ResourceRef *domain.ResourceRef `json:"resourceRef,omitempty"`
	FileName    string              `json:"fileName"`
	ContentType string              `json:"contentType"`
	SizeBytes   int64               `json:"sizeBytes"`
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
	var commentTarget *domain.CommentTarget
	var err error
	if in.ResourceRef != nil {
		var p domain.CommentPolicy
		var caps domain.CommentCapabilities
		commentTarget, p, caps, err = s.ensureCommentable(ctx, *in.ResourceRef, profileUUID)
		if err != nil {
			return nil, err
		}
		if !caps.CanAttach {
			return nil, domain.ErrCommentNotAllowed
		}
		if p.Attachments.ImagesOnly && !strings.HasPrefix(strings.ToLower(in.ContentType), "image/") {
			return nil, fmt.Errorf("%w: chỉ cho phép tệp ảnh", domain.ErrValidation)
		}
		if commentTarget.SpaceUUID != nil {
			in.SpaceUUID = *commentTarget.SpaceUUID
		}
	} else {
		forum, e := s.requireView(ctx, in.SpaceUUID, profileUUID)
		if e != nil {
			return nil, e
		}
		if e = s.requirePost(ctx, forum, profileUUID); e != nil {
			return nil, e
		}
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
	objectKey := fmt.Sprintf("spaces/%s/%04d/%02d/%s/%s", in.SpaceUUID, now.Year(), now.Month(), randSuffix(), safeName(in.FileName))
	if commentTarget != nil {
		objectKey = fmt.Sprintf("comments/%s/%04d/%02d/%s/%s", commentTarget.ID, now.Year(), now.Month(), randSuffix(), safeName(in.FileName))
	}

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
	if att.CommentID != nil {
		if _, _, err := s.GetComment(ctx, *att.CommentID, profileUUID); err != nil {
			return "", err
		}
	} else if _, err := s.requireView(ctx, att.SpaceUUID, profileUUID); err != nil {
		return "", err
	}
	if att.CommentID == nil && s.spaceIsPublic(ctx, att.SpaceUUID) {
		return s.store.PublicURL(att.ObjectKey), nil
	}
	return s.store.PresignGet(ctx, att.ObjectKey, att.FileName)
}

func (s *Service) DeleteAttachment(ctx context.Context, profileUUID, id string) error {
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	canDelete := att.UploaderProfileUUID == profileUUID
	if !canDelete && att.CommentID != nil {
		if c, _, e := s.GetComment(ctx, *att.CommentID, profileUUID); e == nil {
			canDelete = c.CanModerate
		}
	}
	if !canDelete && att.CommentID == nil {
		canDelete = canModerate(s.role(ctx, att.SpaceUUID, profileUUID))
	}
	if !canDelete {
		return domain.ErrForbidden
	}
	if s.store != nil {
		s.store.Remove(ctx, att.ObjectKey)
	}
	return s.repo.DeleteAttachment(ctx, id)
}

func (s *Service) ImportPersonalFileSelection(ctx context.Context, profileUUID, spaceUUID, selectionToken, purpose, idempotencyKey string) ([]string, error) {
	if s.store == nil || s.personalFiles == nil || !s.personalFiles.Enabled() {
		return nil, fmt.Errorf("%w: Tệp của tôi chưa được cấu hình", domain.ErrValidation)
	}
	actorUserID := auth.UserID(ctx)
	if actorUserID == "" || len(idempotencyKey) < 8 {
		return nil, domain.ErrValidation
	}
	tokenSum := sha256.Sum256([]byte(selectionToken))
	tokenHash := hex.EncodeToString(tokenSum[:])
	leaseUntil := time.Now().UTC().Add(2 * time.Minute)
	completed, existingIDs, err := s.repo.ClaimPersonalFileImport(ctx, idempotencyKey, tokenHash, actorUserID, purpose, leaseUntil)
	if err != nil {
		return nil, err
	}
	if completed {
		if err := s.personalFiles.Complete(ctx, selectionToken, actorUserID, idempotencyKey, map[string]any{"attachmentIds": existingIDs}); err != nil {
			return nil, err
		}
		return existingIDs, nil
	}
	redeemed, err := s.personalFiles.Redeem(ctx, selectionToken, actorUserID, purpose, idempotencyKey)
	if err != nil {
		_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
		return nil, err
	}
	created := make([]string, 0, len(redeemed.Items))
	objectKeys := make([]string, 0, len(redeemed.Items))
	cleanup := func() {
		for _, id := range created {
			_ = s.repo.DeleteAttachment(ctx, id)
		}
		for _, key := range objectKeys {
			_ = s.store.Remove(ctx, key)
		}
	}
	for _, item := range redeemed.Items {
		kind := "file"
		if strings.HasPrefix(strings.ToLower(item.MimeType), "image/") {
			kind = "image"
		}
		now := time.Now().UTC()
		objectKey := fmt.Sprintf("spaces/%s/%04d/%02d/personal-import/%s/%s", spaceUUID, now.Year(), now.Month(), randSuffix(), safeName(item.FileName))
		if item.NativeReference != "" {
			source, sourceErr := s.repo.GetAttachment(ctx, item.NativeReference)
			if sourceErr != nil {
				cleanup()
				_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, sourceErr.Error())
				return nil, sourceErr
			}
			uploader, profileErr := s.ident.Profile(ctx, source.UploaderProfileUUID)
			if profileErr != nil || uploader.UserID == nil || fmt.Sprint(*uploader.UserID) != actorUserID {
				cleanup()
				_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, "native reference không thuộc user")
				return nil, domain.ErrForbidden
			}
			if err := s.store.Copy(ctx, source.ObjectKey, objectKey); err != nil {
				cleanup()
				_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
				return nil, err
			}
		} else if item.TransferURL != "" {
			if err := s.store.ImportURL(ctx, item.TransferURL, objectKey, item.MimeType, item.SizeBytes, item.ChecksumSHA256); err != nil {
				cleanup()
				_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
				return nil, err
			}
		} else {
			cleanup()
			_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, "selection không có nguồn byte")
			return nil, domain.ErrValidation
		}
		objectKeys = append(objectKeys, objectKey)
		attachment, err := s.repo.CreateAttachment(ctx, domain.Attachment{SpaceUUID: spaceUUID, UploaderProfileUUID: profileUUID,
			ObjectKey: objectKey, FileName: item.FileName, ContentType: item.MimeType, SizeBytes: item.SizeBytes, Kind: kind})
		if err != nil {
			cleanup()
			_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
			return nil, err
		}
		created = append(created, attachment.ID)
	}
	if err := s.repo.CompletePersonalFileImport(ctx, idempotencyKey, created); err != nil {
		cleanup()
		return nil, err
	}
	if err := s.personalFiles.Complete(ctx, selectionToken, actorUserID, idempotencyKey, map[string]any{"attachmentIds": created}); err != nil {
		return nil, err
	}
	return created, nil
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
