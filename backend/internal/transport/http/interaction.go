package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"ludiskus/internal/domain"
)

func (s *Server) interactionContext(w http.ResponseWriter, r *http.Request) {
	value, err := s.svc.InteractionContext(
		r.Context(), chi.URLParam(r, "type"), chi.URLParam(r, "id"),
	)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) batchInteractionContext(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Refs []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"refs"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Refs) < 1 || len(input.Refs) > 100 {
		writeError(w, s.log, domain.ErrValidation)
		return
	}
	data := make([]*domain.InteractionContext, 0, len(input.Refs))
	for _, ref := range input.Refs {
		value, err := s.svc.InteractionContext(r.Context(), ref.Type, ref.ID)
		if err == domain.ErrNotFound {
			continue
		}
		if err != nil {
			writeError(w, s.log, err)
			return
		}
		data = append(data, value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
