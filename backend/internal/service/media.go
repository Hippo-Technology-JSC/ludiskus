package service

import (
	"context"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"

	"ludiskus/internal/domain"
)

func attachmentContentPath(id string) string { return "/api/ludiskus/attachments/" + id + "/content" }
func attachmentUploadPath(id string) string  { return "/api/ludiskus/attachments/" + id + "/upload" }

func (s *Service) UploadAttachment(ctx context.Context, profile, id, contentType string, reader io.Reader) error {
	if s.store == nil {
		return domain.ErrValidation
	}
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	if att.UploaderProfileUUID != profile || att.Status != "pending" || att.PostID != nil || att.CommentID != nil {
		return domain.ErrForbidden
	}
	if att.FinalizedAt != nil {
		return domain.ErrConflict
	}
	if s.cfg.AttachTTL > 0 && time.Since(att.CreatedAt) > s.cfg.AttachTTL {
		return domain.ErrNotFound
	}
	actual, _, err := mime.ParseMediaType(contentType)
	if err != nil || actual != att.ContentType {
		return domain.ErrValidation
	}
	if strings.HasPrefix(att.ObjectKey, "comments/") {
		parts := strings.Split(att.ObjectKey, "/")
		target, e := s.repo.GetCommentTargetByID(ctx, parts[1])
		if e != nil {
			return e
		}
		_, policy, caps, e := s.ensureCommentable(ctx, target.Ref(), profile)
		if e != nil {
			return e
		}
		if !caps.CanAttach || (policy.Attachments.ImagesOnly && att.Kind != "image") {
			return domain.ErrCommentNotAllowed
		}
	} else {
		forum, e := s.requireView(ctx, att.SpaceUUID, profile)
		if e != nil {
			return e
		}
		if e = s.requirePost(ctx, forum, profile); e != nil {
			return e
		}
	}
	maximum := s.cfg.MaxFileBytes
	if att.Purpose != nil && validEditorAssetPurpose(*att.Purpose) && maximum > editorAssetMaxBytes {
		maximum = editorAssetMaxBytes
	}
	result, err := s.store.PutUpload(ctx, att.ObjectKey, att.ContentType, att.SizeBytes, maximum, reader)
	if err != nil {
		return err
	}
	_, err = s.repo.FinalizeAttachment(ctx, att.ID, result.ChecksumSHA256)
	return err
}

func (s *Service) OpenAttachment(ctx context.Context, profile, id string, public bool) (*minio.Object, minio.ObjectInfo, *domain.Attachment, error) {
	att, err := s.repo.GetAttachment(ctx, id)
	if err != nil {
		return nil, minio.ObjectInfo{}, nil, err
	}
	if public {
		if att.CommentID == nil {
			return nil, minio.ObjectInfo{}, nil, domain.ErrNotFound
		}
		c, target, e := s.GetComment(ctx, *att.CommentID, "")
		if e != nil || c.Status != domain.CommentPublished {
			return nil, minio.ObjectInfo{}, nil, domain.ErrNotFound
		}
		if _, e = s.PublicCommentThread(ctx, target.Ref()); e != nil {
			return nil, minio.ObjectInfo{}, nil, domain.ErrNotFound
		}
	}
	if _, err = s.AttachmentContentURL(ctx, profile, id); err != nil {
		return nil, minio.ObjectInfo{}, nil, err
	}
	object, info, err := s.store.Open(ctx, att.ObjectKey)
	if minio.ToErrorResponse(err).StatusCode == 404 {
		err = domain.ErrNotFound
	}
	return object, info, att, err
}
