package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"ludiskus/internal/auth"
	"ludiskus/internal/domain"
	"ludiskus/internal/service"
)

func (s *Server) callingCommentService(r *http.Request) (*domain.CommentService, error) {
	return s.svc.CommentServiceForClient(r.Context(), auth.ServiceClientID(r.Context()))
}
func (s *Server) requireCommentAdmin(w http.ResponseWriter, r *http.Request) (*domain.CommentService, bool) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return nil, false
	}
	if svc.Code != "ludiskus" {
		writeError(w, s.log, domain.ErrServiceScope)
		return nil, false
	}
	return svc, true
}

func (s *Server) requireCommentUIAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !auth.IsSuperuser(r.Context()) || auth.GatewayAudience(r.Context()) != "ludiskus" {
		writeError(w, s.log, domain.ErrForbidden)
		return false
	}
	return true
}

func (s *Server) uiAdminCommentServices(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	out, err := s.svc.AdminCommentServices(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) uiAdminUpsertCommentService(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	var in domain.CommentService
	if !decode(w, r, &in) {
		return
	}
	if code := chi.URLParam(r, "code"); code != "" {
		in.Code = code
	}
	out, err := s.svc.AdminUpsertCommentService(r.Context(), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) uiAdminCommentPolicies(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	out, err := s.svc.AdminCommentPolicies(r.Context(), r.URL.Query().Get("service"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) uiAdminCommentPolicyPut(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	var raw json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	warnings, err := s.svc.AdminPutCommentPolicy(r.Context(), chi.URLParam(r, "service"), chi.URLParam(r, "type"), raw, nil)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings})
}
func (s *Server) uiAdminCommentReconcile(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	n, err := s.svc.CommentAdminReconcile(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"reconciled": n})
}

func (s *Server) uiAdminCommentAbuseFlags(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	out, err := s.svc.AdminCommentAbuseFlags(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) uiAdminDecideCommentAbuseFlag(w http.ResponseWriter, r *http.Request) {
	if !s.requireCommentUIAdmin(w, r) {
		return
	}
	var in struct {
		State string `json:"state"`
		Note  string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.svc.AdminDecideCommentAbuseFlag(r.Context(), chi.URLParam(r, "id"), in.State, auth.ProfileUUID(r.Context()), in.Note)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) s2sCommentTargets(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	var in struct {
		Targets []service.PushCommentTarget `json:"targets"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.svc.PushCommentTargets(r.Context(), svc.Code, in.Targets)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) s2sCommentInvalidate(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	var in struct {
		Refs   []domain.ResourceRef `json:"refs"`
		Reason string               `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err = s.svc.InvalidateCommentTargets(r.Context(), svc.Code, in.Refs, in.Reason); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) s2sCommentSettings(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	var in struct {
		Ref         domain.ResourceRef `json:"ref"`
		ThreadState string             `json:"threadState"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err = s.svc.S2SSetCommentThreadState(r.Context(), svc.Code, in.Ref, in.ThreadState); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) s2sCommentCounts(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	refs := []domain.ResourceRef{}
	for _, raw := range strings.Split(r.URL.Query().Get("refs"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		ref, e := domain.ParseResourceRef(strings.TrimSpace(raw))
		if e != nil {
			writeError(w, s.log, e)
			return
		}
		refs = append(refs, ref)
	}
	out, err := s.svc.CommentCountsS2S(r.Context(), svc.Code, refs)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) s2sSystemComment(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	var in service.SystemCommentInput
	if !decode(w, r, &in) {
		return
	}
	out, created, err := s.svc.CreateSystemComment(r.Context(), svc.Code, in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}
func (s *Server) s2sCommentModerate(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	var in struct {
		Action           string `json:"action"`
		ActorProfileUUID string `json:"actorProfileUuid"`
		Reason           string `json:"reason"`
		Note             string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.svc.S2SModerateComment(r.Context(), svc.Code, chi.URLParam(r, "id"), in.Action, in.ActorProfileUUID, in.Reason)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) s2sCommentExport(w http.ResponseWriter, r *http.Request) {
	svc, err := s.callingCommentService(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	ref, err := domain.ParseResourceRef(r.URL.Query().Get("ref"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	out, err := s.svc.ExportComments(r.Context(), svc.Code, ref)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) adminCommentServices(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	out, err := s.svc.AdminCommentServices(r.Context())
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) adminUpsertCommentService(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	var in domain.CommentService
	if !decode(w, r, &in) {
		return
	}
	if code := chi.URLParam(r, "code"); code != "" {
		in.Code = code
	}
	out, err := s.svc.AdminUpsertCommentService(r.Context(), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) adminDisableCommentService(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	if err := s.svc.AdminDisableCommentService(r.Context(), chi.URLParam(r, "code")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) adminCommentPolicies(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	out, err := s.svc.AdminCommentPolicies(r.Context(), r.URL.Query().Get("service"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) adminCommentPolicyPut(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	var raw json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	warnings, err := s.svc.AdminPutCommentPolicy(r.Context(), chi.URLParam(r, "service"), chi.URLParam(r, "type"), raw, nil)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings})
}
func (s *Server) adminCommentReconcile(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	n, err := s.svc.CommentAdminReconcile(r.Context(), r.URL.Query().Get("target"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"reconciled": n})
}

func (s *Server) adminCommentAbuseFlags(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	out, err := s.svc.AdminCommentAbuseFlags(r.Context(), r.URL.Query().Get("state"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) adminDecideCommentAbuseFlag(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireCommentAdmin(w, r); !ok {
		return
	}
	var in struct {
		State            string `json:"state"`
		Note             string `json:"note"`
		ActorProfileUUID string `json:"actorProfileUuid"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.svc.AdminDecideCommentAbuseFlag(r.Context(), chi.URLParam(r, "id"), in.State, in.ActorProfileUUID, in.Note)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
