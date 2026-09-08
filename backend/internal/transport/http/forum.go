package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) forumMembers(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.ForumMembers(r.Context(), chi.URLParam(r, "space"), s.me(r), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(out))
}
func (s *Server) forumPreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BodyMD string `json:"bodyMd"`
	}
	if !decode(w, r, &in) {
		return
	}
	html, err := s.svc.ForumPreview(r.Context(), chi.URLParam(r, "space"), s.me(r), in.BodyMD)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": html})
}
func (s *Server) assignForumTopic(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Assignee *string `json:"assigneeProfileUuid"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.svc.AssignForumTopic(r.Context(), chi.URLParam(r, "id"), s.me(r), in.Assignee); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) forumCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.ForumCapabilities(r.Context(), chi.URLParam(r, "space"), s.me(r)))
}
func (s *Server) forumQueue(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	out, err := s.svc.ForumQueue(r.Context(), chi.URLParam(r, "space"), s.me(r), r.URL.Query().Get("board"), limit, offset)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(out))
}
