package http

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"ludiskus/internal/domain"
	"net/http"
	"strings"
)

func (s *Server) pollAdmin(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/comment-admin/") {
		if !s.requireCommentUIAdmin(w, r) {
			return
		}
	} else {
		if _, ok := s.requireCommentAdmin(w, r); !ok {
			return
		}
	}
	if !s.svc.PollEnabled() {
		writeError(w, s.log, domain.ErrNotFound)
		return
	}
	path := r.URL.Path
	switch {
	case strings.Contains(path, "policies") && r.Method == "PUT":
		var in json.RawMessage
		if !decode(w, r, &in) {
			return
		}
		actor := s.me(r)
		var profile *string
		if actor != "" {
			profile = &actor
		}
		e := s.svc.PutPollPolicy(r.Context(), chi.URLParam(r, "service"), chi.URLParam(r, "type"), in, profile)
		s.pollRespond(w, r, map[string]bool{"updated": e == nil}, e, 200)
	case strings.Contains(path, "policies"):
		out, e := s.svc.PollPolicies(r.Context())
		s.pollRespond(w, r, out, e, 200)
	case strings.Contains(path, "reconcile"):
		out, e := s.svc.ReconcilePolls(r.Context())
		s.pollRespond(w, r, map[string]int{"fixed": out}, e, 200)
	case strings.Contains(path, "abuse-flags"):
		out, e := s.svc.PollAbuseFlags(r.Context())
		s.pollRespond(w, r, out, e, 200)
	}
}
