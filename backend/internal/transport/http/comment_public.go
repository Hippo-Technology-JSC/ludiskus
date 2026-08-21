package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) publicCommentThread(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.PublicCommentThread(r.Context(), commentRef(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	etag := s.svc.CommentETag(out.Target)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=30")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) publicCommentList(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.PublicCommentList(r.Context(), commentRef(r), r.URL.Query().Get("sort"), r.URL.Query().Get("cursor"), queryInt(r, "limit", 20), queryInt(r, "previewReplies", 3))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) publicCommentReplies(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.PublicCommentReplies(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("cursor"), queryInt(r, "limit", 20))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, out)
}
