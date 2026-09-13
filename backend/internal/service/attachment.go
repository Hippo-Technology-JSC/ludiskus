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
	SpaceUUID      string              `json:"spaceUuid"`
	ResourceRef    *domain.ResourceRef `json:"resourceRef,omitempty"`
	FileName       string              `json:"fileName"`
	ContentType    string              `json:"contentType"`
	SizeBytes      int64               `json:"sizeBytes"`
	Purpose        string              `json:"purpose,omitempty"`
	IdempotencyKey string              `json:"-"`
}

type PresignResult struct {
	AttachmentID string `json:"attachmentId"`
	UploadURL    string `json:"uploadUrl"`
	ObjectKey    string `json:"objectKey"`
}

type EditorAsset struct {
	ID          string `json:"id"`
	FileName    string `json:"fileName"`
	MIMEType    string `json:"mimeType"`
	SizeBytes   int64  `json:"sizeBytes"`
	Kind        string `json:"kind"`
	ContentPath string `json:"contentPath"`
	Markdown    string `json:"markdown"`
}

type EditorPresignResult struct {
	AssetID   string    `json:"assetId"`
	UploadURL string    `json:"uploadUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
}

const editorAssetMaxBytes int64 = 5 * 1024 * 1024

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
		Purpose: stringPtr(in.Purpose), UploadIdempotencyKey: stringPtr(in.IdempotencyKey),
	})
	if err != nil {
		return nil, err
	}
	putURL, err := s.store.PresignPut(ctx, objectKey)
	if err != nil {
		_ = s.repo.DeleteAttachment(ctx, att.ID)
		return nil, err
	}
	return &PresignResult{AttachmentID: att.ID, UploadURL: putURL, ObjectKey: objectKey}, nil
}

func stringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func validEditorAssetPurpose(value string) bool {
	switch value {
	case "topic-body-image", "reply-body-image", "markdown-image":
		return true
	default:
		return false
	}
}

func (s *Service) PresignEditorAsset(ctx context.Context, profileUUID, idempotencyKey string, in PresignInput) (*EditorPresignResult, error) {
	if s.store == nil {
		return nil, fmt.Errorf("%w: đính kèm chưa được cấu hình", domain.ErrValidation)
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || !validEditorAssetPurpose(in.Purpose) || !strings.HasPrefix(strings.ToLower(in.ContentType), "image/") {
		return nil, domain.ErrValidation
	}
	if in.SizeBytes <= 0 || in.SizeBytes > editorAssetMaxBytes {
		return nil, domain.ErrTooLarge
	}
	if existing, err := s.repo.GetAttachmentByUploadIdempotencyKey(ctx, idempotencyKey); err == nil {
		return s.editorPresignForExisting(ctx, profileUUID, in, existing)
	} else if err != domain.ErrNotFound {
		return nil, err
	}
	in.IdempotencyKey = idempotencyKey
	result, err := s.PresignUpload(ctx, profileUUID, in)
	if err != nil {
		// A concurrent request with the same key may have won the unique-index
		// race after the lookup above. Re-read and return that same slot.
		if existing, lookupErr := s.repo.GetAttachmentByUploadIdempotencyKey(ctx, idempotencyKey); lookupErr == nil {
			return s.editorPresignForExisting(ctx, profileUUID, in, existing)
		}
		return nil, err
	}
	return &EditorPresignResult{AssetID: result.AttachmentID, UploadURL: result.UploadURL, ExpiresAt: time.Now().UTC().Add(s.cfg.PresignTTL)}, nil
}

func (s *Service) editorPresignForExisting(ctx context.Context, profileUUID string, in PresignInput, existing *domain.Attachment) (*EditorPresignResult, error) {
	if existing.UploaderProfileUUID != profileUUID || existing.SpaceUUID != in.SpaceUUID || existing.FileName != in.FileName || existing.ContentType != in.ContentType || existing.SizeBytes != in.SizeBytes || existing.Purpose == nil || *existing.Purpose != in.Purpose {
		return nil, domain.ErrConflict
	}
	u, err := s.store.PresignPut(ctx, existing.ObjectKey)
	if err != nil {
		return nil, err
	}
	return &EditorPresignResult{AssetID: existing.ID, UploadURL: u, ExpiresAt: time.Now().UTC().Add(s.cfg.PresignTTL)}, nil
}

func (s *Service) CompleteEditorAsset(ctx context.Context, profileUUID, id string) (*EditorAsset, error) {
	if s.store == nil {
		return nil, fmt.Errorf("%w: đính kèm chưa được cấu hình", domain.ErrValidation)
	}
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return nil, err
	}
	if att.UploaderProfileUUID != profileUUID || att.Status != "pending" || att.PostID != nil || att.CommentID != nil || att.Purpose == nil || !validEditorAssetPurpose(*att.Purpose) {
		return nil, domain.ErrForbidden
	}
	if att.FinalizedAt == nil {
		size, stored, detected, checksum, err := s.store.Inspect(ctx, att.ObjectKey, editorAssetMaxBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: object chưa hợp lệ", domain.ErrValidation)
		}
		stored = strings.ToLower(strings.TrimSpace(strings.Split(stored, ";")[0]))
		detected = strings.ToLower(strings.TrimSpace(strings.Split(detected, ";")[0]))
		if size != att.SizeBytes || stored != strings.ToLower(att.ContentType) || detected != strings.ToLower(att.ContentType) || !s.cfg.MIMEAllowed(detected) {
			return nil, fmt.Errorf("%w: tệp không khớp loại hoặc kích thước đã khai báo", domain.ErrValidation)
		}
		att, err = s.repo.FinalizeAttachment(ctx, id, checksum)
		if err != nil {
			return nil, err
		}
	}
	return editorAssetOf(att), nil
}

func editorAssetOf(att *domain.Attachment) *EditorAsset {
	contentPath := "/api/ludiskus/attachments/" + att.ID + "/content"
	alt := strings.TrimSuffix(att.FileName, path.Ext(att.FileName))
	if strings.TrimSpace(alt) == "" {
		alt = "Ảnh đính kèm"
	}
	alt = strings.NewReplacer("[", "", "]", "", "\n", " ", "\r", " ").Replace(alt)
	return &EditorAsset{ID: att.ID, FileName: att.FileName, MIMEType: att.ContentType, SizeBytes: att.SizeBytes, Kind: att.Kind, ContentPath: contentPath, Markdown: "![" + alt + "](" + contentPath + ")"}
}

func (s *Service) ImportEditorAssets(ctx context.Context, profileUUID, spaceUUID, token, purpose, idempotencyKey string) ([]EditorAsset, error) {
	if !validEditorAssetPurpose(purpose) {
		return nil, domain.ErrValidation
	}
	ids, err := s.ImportPersonalFileSelection(ctx, profileUUID, spaceUUID, token, purpose, idempotencyKey)
	if err != nil {
		return nil, err
	}
	out := make([]EditorAsset, 0, len(ids))
	for _, id := range ids {
		asset, err := s.CompleteEditorAsset(ctx, profileUUID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *asset)
	}
	return out, nil
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
	publicForumFile := false
	if att.CommentID != nil {
		if _, _, err := s.GetComment(ctx, *att.CommentID, profileUUID); err != nil {
			return "", err
		}
	} else {
		if _, err := s.requireView(ctx, att.SpaceUUID, profileUUID); err != nil {
			return "", err
		}
		if att.PostID == nil {
			if att.UploaderProfileUUID != profileUUID {
				return "", domain.ErrForbidden
			}
		} else {
			post, err := s.repo.GetPost(ctx, *att.PostID)
			if err != nil {
				return "", err
			}
			topic, err := s.repo.GetTopic(ctx, post.TopicID)
			if err != nil {
				return "", err
			}
			if err = s.readableForumTopic(ctx, topic, profileUUID); err != nil {
				return "", err
			}
			publicForumFile = post.Status == domain.StatusPublished && (topic.Status == domain.StatusPublished || topic.Status == domain.StatusLocked)
			if post.Status == domain.StatusDeleted || (post.Status != domain.StatusPublished && post.AuthorProfileUUID != profileUUID && !canModerate(s.role(ctx, post.SpaceUUID, profileUUID))) {
				return "", domain.ErrNotFound
			}
		}
	}
	if publicForumFile && s.spaceIsPublic(ctx, att.SpaceUUID) {
		return s.store.PublicURL(att.ObjectKey), nil
	}
	return s.store.PresignGet(ctx, att.ObjectKey, att.FileName)
}

func (s *Service) AttachmentContentURL(ctx context.Context, profileUUID, id string) (string, error) {
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return "", err
	}
	if att.Status == "pending" && att.FinalizedAt == nil {
		return "", domain.ErrNotFound
	}
	return s.AttachmentURL(ctx, profileUUID, id)
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
		if validEditorAssetPurpose(purpose) && (item.SizeBytes <= 0 || item.SizeBytes > editorAssetMaxBytes) {
			cleanup()
			err := fmt.Errorf("%w: ảnh editor vượt giới hạn 5 MiB", domain.ErrTooLarge)
			_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
			return nil, err
		}
		kind := "file"
		if strings.HasPrefix(strings.ToLower(item.MimeType), "image/") {
			kind = "image"
		}
		now := time.Now().UTC()
		objectKey := fmt.Sprintf("spaces/%s/%04d/%02d/personal-import/%s/%s", spaceUUID, now.Year(), now.Month(), randSuffix(), safeName(item.FileName))
		var importedSize int64
		var importedChecksum string
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
			result, err := s.store.CopyPrepared(ctx, source.ObjectKey, objectKey, item.MimeType, item.SizeBytes)
			if err != nil {
				cleanup()
				_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
				return nil, err
			}
			importedSize, importedChecksum = result.SizeBytes, result.ChecksumSHA256
		} else if item.TransferURL != "" {
			result, err := s.store.ImportURLPrepared(ctx, item.TransferURL, objectKey, item.MimeType, item.SizeBytes, item.ChecksumSHA256)
			if err != nil {
				cleanup()
				_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
				return nil, err
			}
			importedSize, importedChecksum = result.SizeBytes, result.ChecksumSHA256
		} else {
			cleanup()
			_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, "selection không có nguồn byte")
			return nil, domain.ErrValidation
		}
		objectKeys = append(objectKeys, objectKey)
		attachment, err := s.repo.CreateAttachment(ctx, domain.Attachment{SpaceUUID: spaceUUID, UploaderProfileUUID: profileUUID,
			ObjectKey: objectKey, FileName: item.FileName, ContentType: item.MimeType, SizeBytes: importedSize, Kind: kind,
			Purpose: stringPtr(purpose)})
		if err != nil {
			cleanup()
			_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
			return nil, err
		}
		created = append(created, attachment.ID)
		if _, err := s.repo.FinalizeAttachment(ctx, attachment.ID, importedChecksum); err != nil {
			cleanup()
			_ = s.repo.FailPersonalFileImport(ctx, idempotencyKey, err.Error())
			return nil, err
		}
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
