package http

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/minio/minio-go/v7"

	"ludiskus/internal/domain"
	"ludiskus/internal/service"
)

func (s *Server) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	err := s.svc.UploadAttachment(r.Context(), s.me(r), chi.URLParam(r, "id"), r.Header.Get("Content-Type"), r.Body)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{"uploaded": true})
}

func (s *Server) attachmentContent(w http.ResponseWriter, r *http.Request) {
	s.serveAttachment(w, r, false)
}
func (s *Server) publicAttachmentContent(w http.ResponseWriter, r *http.Request) {
	s.serveAttachment(w, r, true)
}
func (s *Server) serveAttachment(w http.ResponseWriter, r *http.Request, public bool) {
	object, info, att, err := s.svc.OpenAttachment(r.Context(), s.me(r), chi.URLParam(r, "id"), public)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	defer object.Close()
	serveAttachmentContent(w, r, object, info, att)
}

func serveAttachmentContent(w http.ResponseWriter, r *http.Request, object io.ReadSeeker, info minio.ObjectInfo, att *domain.Attachment) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Type", att.ContentType)
	disposition := "attachment"
	if att.Kind == "image" || att.ContentType == "application/pdf" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": att.FileName}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if info.ETag != "" {
		w.Header().Set("ETag", strconv.Quote(strings.Trim(info.ETag, "\"")))
	}
	http.ServeContent(w, r, att.FileName, info.LastModified, object)
}

func (s *Server) importAttachments(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SpaceUUID      string `json:"spaceUuid"`
		Purpose        string `json:"purpose"`
		SelectionToken string `json:"selectionToken"`
	}
	if !decode(w, r, &in) {
		return
	}
	assets, err := s.svc.ImportAttachmentAssets(r.Context(), s.me(r), in.SpaceUUID, in.SelectionToken, in.Purpose, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Data []service.EditorAsset `json:"data"`
	}{assets})
}
