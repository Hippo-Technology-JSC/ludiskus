package http

import (
	"github.com/go-chi/chi/v5"
	"ludiskus/internal/domain"
	"ludiskus/internal/service"
	"net/http"
	"strings"
	"time"
)

func (s *Server) pollGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.svc.PollEnabled() {
			writeError(w, s.log, domain.PollFailure("POLL_NOT_FOUND", 404, ""))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		next.ServeHTTP(w, r)
	})
}
func (s *Server) pollViewer(r *http.Request) (service.PollViewer, error) {
	if strings.Contains(r.URL.Path, "/public/polls") {
		return service.PollViewer{Public: true}, nil
	}
	if strings.Contains(r.URL.Path, "/s2s/polls") {
		svc, e := s.callingCommentService(r)
		if e != nil {
			return service.PollViewer{}, e
		}
		return service.PollViewer{Service: svc.Code}, nil
	}
	return service.PollViewer{Profile: s.me(r)}, nil
}
func (s *Server) pollRespond(w http.ResponseWriter, r *http.Request, out any, e error, status int) {
	if e != nil {
		if e == domain.ErrRateLimited {
			w.Header().Set("Retry-After", "60")
		}
		writeError(w, s.log, e)
		return
	}
	writeJSON(w, status, dataResp(out))
}
func (s *Server) pollCreate(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	var in service.PollInput
	if !decode(w, r, &in) {
		return
	}
	out, e := s.svc.CreatePoll(r.Context(), v, r.Header.Get("Idempotency-Key"), in)
	s.pollRespond(w, r, out, e, 201)
}
func (s *Server) pollGet(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	out, e := s.svc.GetPoll(r.Context(), chi.URLParam(r, "id"), v)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	etag := `"` + out["version"].(string) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if v.Public {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	if r.Method == "HEAD" {
		w.WriteHeader(200)
		return
	}
	writeJSON(w, 200, dataResp(out))
}
func (s *Server) pollPatch(w http.ResponseWriter, r *http.Request) {
	var in service.PollPatch
	if !decode(w, r, &in) {
		return
	}
	out, e := s.svc.PatchPoll(r.Context(), chi.URLParam(r, "id"), service.PollViewer{Profile: s.me(r)}, in)
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollVote(w http.ResponseWriter, r *http.Request) {
	var in domain.PollVoteInput
	if r.Method != "DELETE" && !decode(w, r, &in) {
		return
	}
	out, e := s.svc.VotePoll(r.Context(), chi.URLParam(r, "id"), s.me(r), r.Header.Get("Idempotency-Key"), in, r.Method == "DELETE")
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollAction(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	action := chi.URLParam(r, "action")
	var in struct {
		Action           string     `json:"action"`
		ClosesAt         *time.Time `json:"closesAt"`
		ActorProfileUUID string     `json:"actorProfileUuid"`
	}
	if e = decodeOptional(r, &in); e != nil {
		badRequest(w, "JSON body không hợp lệ")
		return
	}
	if action == "moderate" {
		action = in.Action
	}
	if r.Method == "DELETE" {
		action = "delete"
	}
	if v.Service != "" {
		v.Profile = in.ActorProfileUUID
	}
	out, e := s.svc.PollAction(r.Context(), chi.URLParam(r, "id"), v, action, in.ClosesAt)
	if e == nil && action == "delete" {
		w.WriteHeader(204)
		return
	}
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollOptions(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Options  []domain.PollOption `json:"options"`
		Label    string              `json:"label"`
		StartsAt *time.Time          `json:"startsAt"`
		EndsAt   *time.Time          `json:"endsAt"`
	}
	if !decode(w, r, &in) {
		return
	}
	replace := r.Method == "PUT"
	options := in.Options
	if !replace {
		options = []domain.PollOption{{Label: in.Label, StartsAt: in.StartsAt, EndsAt: in.EndsAt}}
	}
	out, e := s.svc.PollOptions(r.Context(), chi.URLParam(r, "id"), service.PollViewer{Profile: s.me(r)}, options, replace)
	status := 200
	if !replace {
		status = 201
	}
	s.pollRespond(w, r, out, e, status)
}
func (s *Server) pollAttach(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	var in struct {
		Anchor           domain.ResourceRef `json:"anchor"`
		ActorProfileUUID string             `json:"actorProfileUuid"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, e := s.svc.AttachPoll(r.Context(), chi.URLParam(r, "id"), v, in.Anchor, in.ActorProfileUUID)
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollResults(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	out, e := s.svc.GetPoll(r.Context(), chi.URLParam(r, "id"), v)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	writeJSON(w, 200, dataResp(map[string]any{"results": out["results"], "resultsHiddenReason": out["resultsHiddenReason"]}))
}
func (s *Server) pollPeople(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	out, e := s.svc.PollPeople(r.Context(), chi.URLParam(r, "id"), v, r.URL.Query().Get("optionId"), r.URL.Query().Get("cursor"), strings.HasSuffix(r.URL.Path, "/participants"))
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollInvitees(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	var in struct {
		ProfileUUIDs []string `json:"profileUuids"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, e := s.svc.PollInvitees(r.Context(), chi.URLParam(r, "id"), v, in.ProfileUUIDs, r.Method == "DELETE", r.Method == "PUT")
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollList(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	var ref *domain.ResourceRef
	if chi.URLParam(r, "service") != "" {
		value := commentRef(r)
		ref = &value
	} else if v.Service != "" {
		value := domain.ResourceRef{Service: r.URL.Query().Get("service"), Type: r.URL.Query().Get("type"), ID: r.URL.Query().Get("id")}
		if value.Service == "" {
			value.Service = v.Service
		}
		ref = &value
	}
	out, e := s.svc.ListPolls(r.Context(), v, ref, r.URL.Query().Get("role"), chi.URLParam(r, "space"), r.URL.Query().Get("cursor"), r.URL.Query().Get("state"))
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollSummary(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs  []string             `json:"ids"`
		Refs []domain.ResourceRef `json:"refs"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, e := s.svc.PollSummary(r.Context(), service.PollViewer{Profile: s.me(r)}, in.IDs, in.Refs)
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollReport(w http.ResponseWriter, r *http.Request) {
	var in service.ReportInput
	if !decode(w, r, &in) {
		return
	}
	e := s.svc.ReportPoll(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "oid"), service.PollViewer{Profile: s.me(r)}, in)
	s.pollRespond(w, r, map[string]bool{"reported": e == nil}, e, 201)
}
func (s *Server) pollModerateOption(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, e := s.svc.ModeratePollOption(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "oid"), service.PollViewer{Profile: s.me(r)}, in.Action)
	s.pollRespond(w, r, out, e, 200)
}
func (s *Server) pollNotify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NotifyResult bool `json:"notifyResult"`
	}
	if !decode(w, r, &in) {
		return
	}
	e := s.svc.SetPollNotify(r.Context(), chi.URLParam(r, "id"), s.me(r), in.NotifyResult)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) pollExport(w http.ResponseWriter, r *http.Request) {
	v, e := s.pollViewer(r)
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	out, e := s.svc.ExportPoll(r.Context(), chi.URLParam(r, "id"), v, r.URL.Query().Get("kind"))
	if e != nil {
		writeError(w, s.log, e)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="lupoll.csv"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write(out)
}
func (s *Server) pollUserRoutes(r chi.Router) {
	r.Use(s.pollGate)
	r.Post("/", s.pollCreate)
	r.Get("/mine", s.pollList)
	r.Post("/summary", s.pollSummary)
	r.Get("/r/{service}/{type}/{id}", s.pollList)
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", s.pollGet)
		r.Patch("/", s.pollPatch)
		r.Delete("/", s.pollAction)
		r.Put("/vote", s.pollVote)
		r.Delete("/vote", s.pollVote)
		r.Patch("/vote/settings", s.pollNotify)
		r.Put("/options", s.pollOptions)
		r.Post("/options", s.pollOptions)
		r.Post("/attach", s.pollAttach)
		r.Get("/results", s.pollResults)
		r.Get("/voters", s.pollPeople)
		r.Get("/participants", s.pollPeople)
		r.Get("/export.csv", s.pollExport)
		r.Post("/invitees", s.pollInvitees)
		r.Delete("/invitees", s.pollInvitees)
		r.Post("/report", s.pollReport)
		r.Post("/options/{oid}/report", s.pollReport)
		r.Post("/options/{oid}/moderate", s.pollModerateOption)
		r.Post("/{action}", s.pollAction)
	})
}
