package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"ludiskus/internal/domain"
	"ludiskus/internal/service"
)

func commentRef(r *http.Request) domain.ResourceRef {
	return domain.ResourceRef{Service: chi.URLParam(r, "service"), Type: chi.URLParam(r, "type"), ID: chi.URLParam(r, "id")}
}
func queryInt(r *http.Request, key string, fallback int) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(key))
	if v <= 0 {
		return fallback
	}
	return v
}

func (s *Server) commentThread(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.CommentThread(r.Context(), commentRef(r), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	etag := s.svc.CommentETag(out.Target)
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) commentList(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.ListComments(r.Context(), commentRef(r), s.me(r), r.URL.Query().Get("sort"), r.URL.Query().Get("cursor"), queryInt(r, "limit", 20), queryInt(r, "previewReplies", 3))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) commentCreate(w http.ResponseWriter, r *http.Request) {
	var in service.CreateCommentInput
	if !decode(w, r, &in) {
		return
	}
	out, created, err := s.svc.CreateComment(r.Context(), commentRef(r), s.me(r), r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		if err == domain.ErrRateLimited {
			w.Header().Set("Retry-After", "60")
		}
		writeError(w, s.log, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		if out.Status == domain.CommentPending {
			status = http.StatusAccepted
		}
	}
	writeJSON(w, status, out)
}
func (s *Server) commentGet(w http.ResponseWriter, r *http.Request) {
	out, target, err := s.svc.GetComment(r.Context(), chi.URLParam(r, "id"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment": out, "target": target})
}
func (s *Server) commentReplies(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.CommentReplies(r.Context(), chi.URLParam(r, "id"), s.me(r), r.URL.Query().Get("cursor"), queryInt(r, "limit", 20))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) commentUpdate(w http.ResponseWriter, r *http.Request) {
	var in service.CreateCommentInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.svc.UpdateComment(r.Context(), chi.URLParam(r, "id"), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) commentDelete(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.DeleteComment(r.Context(), chi.URLParam(r, "id"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) commentRevisions(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.CommentRevisions(r.Context(), chi.URLParam(r, "id"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) commentSearch(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.SearchComments(r.Context(), commentRef(r), s.me(r), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) commentMentionSuggest(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.MentionSuggestions(r.Context(), commentRef(r), s.me(r), r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) commentSummaryBatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Refs []domain.ResourceRef `json:"refs"`
	}
	if !decode(w, r, &in) {
		return
	}
	data, skipped, err := s.svc.CommentSummaries(r.Context(), in.Refs, s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "skipped": skipped})
}
func (s *Server) commentMine(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := s.svc.CommentMine(r.Context(), s.me(r), q.Get("status"), q.Get("service"), q.Get("q"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) commentInbox(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.CommentInbox(r.Context(), s.me(r), r.URL.Query().Get("unread") == "1")
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
func (s *Server) commentUnreadCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.svc.CommentUnreadCount(r.Context(), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}
func (s *Server) commentSubscribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Muted bool `json:"muted"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.svc.SetCommentSubscription(r.Context(), commentRef(r), s.me(r), in.Muted); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) commentUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RemoveCommentSubscription(r.Context(), commentRef(r), s.me(r)); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) commentMarkRead(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.MarkCommentRead(r.Context(), commentRef(r), s.me(r)); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) commentThreadAction(w http.ResponseWriter, r *http.Request) {
	action := chi.URLParam(r, "action")
	state := map[string]string{"lock": "locked", "unlock": "open", "close": "closed"}[action]
	if state == "" {
		badRequest(w, "Hành động không hợp lệ")
		return
	}
	if err := s.svc.SetCommentThreadState(r.Context(), commentRef(r), s.me(r), state); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) commentItemAction(w http.ResponseWriter, r *http.Request) {
	id, action := chi.URLParam(r, "id"), chi.URLParam(r, "action")
	var out any
	var err error
	switch action {
	case "pin", "unpin":
		out, err = s.svc.PinComment(r.Context(), id, s.me(r), action == "pin")
	case "hide", "restore":
		out, err = s.svc.ModerateComment(r.Context(), id, s.me(r), action, "")
	default:
		badRequest(w, "Hành động không hợp lệ")
		return
	}
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) commentReport(w http.ResponseWriter,r *http.Request){var in struct{Reason string `json:"reason"`;Note *string `json:"note"`};if !decode(w,r,&in){return};if err:=s.svc.ReportComment(r.Context(),chi.URLParam(r,"id"),s.me(r),in.Reason,in.Note);err!=nil{writeError(w,s.log,err);return};w.WriteHeader(http.StatusNoContent)}
func (s *Server) commentModerationQueue(w http.ResponseWriter,r *http.Request){space:=r.URL.Query().Get("space");out,err:=s.svc.ListModerationQueue(r.Context(),space,s.me(r),r.URL.Query().Get("state"),queryInt(r,"limit",50));if err!=nil{writeError(w,s.log,err);return};writeJSON(w,http.StatusOK,map[string]any{"data":out})}
func (s *Server) commentApprove(w http.ResponseWriter,r *http.Request){if err:=s.svc.ApproveModeration(r.Context(),chi.URLParam(r,"item"),s.me(r));err!=nil{writeError(w,s.log,err);return};w.WriteHeader(http.StatusNoContent)}
func (s *Server) commentReject(w http.ResponseWriter,r *http.Request){var in struct{Note *string `json:"note"`};if !decode(w,r,&in){return};if err:=s.svc.RejectModeration(r.Context(),chi.URLParam(r,"item"),s.me(r),in.Note);err!=nil{writeError(w,s.log,err);return};w.WriteHeader(http.StatusNoContent)}
