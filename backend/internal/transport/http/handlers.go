package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"ludiskus/internal/auth"
	"ludiskus/internal/service"
)

func (s *Server) me(r *http.Request) string { return auth.ProfileUUID(r.Context()) }

// --- spaces / forum ---------------------------------------------------------

func (s *Server) listSpaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListSpaces(r.Context(), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) getForum(w http.ResponseWriter, r *http.Request) {
	forum, err := s.svc.GetForum(r.Context(), chi.URLParam(r, "space"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(forum))
}

func (s *Server) enableForum(w http.ResponseWriter, r *http.Request) {
	forum, err := s.svc.EnableForum(r.Context(), chi.URLParam(r, "space"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(forum))
}

func (s *Server) updateForumSettings(w http.ResponseWriter, r *http.Request) {
	var in service.ForumSettings
	if !decode(w, r, &in) {
		return
	}
	forum, err := s.svc.UpdateForumSettings(r.Context(), chi.URLParam(r, "space"), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(forum))
}

func (s *Server) listModerators(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListModerators(r.Context(), chi.URLParam(r, "space"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

type profileBody struct {
	ProfileUUID string `json:"profileUuid"`
}

func (s *Server) addModerator(w http.ResponseWriter, r *http.Request) {
	var b profileBody
	if !decode(w, r, &b) {
		return
	}
	if err := s.svc.AddModerator(r.Context(), chi.URLParam(r, "space"), s.me(r), b.ProfileUUID); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeModerator(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("profileUuid")
	if target == "" {
		var b profileBody
		if decode(w, r, &b) {
			target = b.ProfileUUID
		}
	}
	if err := s.svc.RemoveModerator(r.Context(), chi.URLParam(r, "space"), s.me(r), target); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- boards -----------------------------------------------------------------

func (s *Server) listBoards(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListBoards(r.Context(), chi.URLParam(r, "space"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) createBoard(w http.ResponseWriter, r *http.Request) {
	var in service.BoardInput
	if !decode(w, r, &in) {
		return
	}
	b, err := s.svc.CreateBoard(r.Context(), chi.URLParam(r, "space"), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusCreated, dataResp(b))
}

func (s *Server) updateBoard(w http.ResponseWriter, r *http.Request) {
	var in service.BoardInput
	if !decode(w, r, &in) {
		return
	}
	b, err := s.svc.UpdateBoard(r.Context(), chi.URLParam(r, "id"), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(b))
}

func (s *Server) deleteBoard(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteBoard(r.Context(), chi.URLParam(r, "id"), s.me(r)); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- topics -----------------------------------------------------------------

func (s *Server) listTopics(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	items, err := s.svc.ListTopics(r.Context(), chi.URLParam(r, "id"), s.me(r),
		r.URL.Query().Get("sort"), limit, offset)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) createTopic(w http.ResponseWriter, r *http.Request) {
	var in service.TopicInput
	if !decode(w, r, &in) {
		return
	}
	t, err := s.svc.CreateTopic(r.Context(), chi.URLParam(r, "id"), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	status := http.StatusCreated
	if t.Status != "published" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, dataResp(t))
}

func (s *Server) getTopic(w http.ResponseWriter, r *http.Request) {
	t, err := s.svc.GetTopic(r.Context(), chi.URLParam(r, "id"), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(t))
}

type titleBody struct {
	Title string `json:"title"`
}

func (s *Server) updateTopic(w http.ResponseWriter, r *http.Request) {
	var b titleBody
	if !decode(w, r, &b) {
		return
	}
	t, err := s.svc.UpdateTopic(r.Context(), chi.URLParam(r, "id"), s.me(r), b.Title)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(t))
}

func (s *Server) deleteTopic(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.TopicAction(r.Context(), chi.URLParam(r, "id"), s.me(r), "delete"); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) topicAction(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.TopicAction(r.Context(), chi.URLParam(r, "id"), s.me(r), chi.URLParam(r, "action")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- posts ------------------------------------------------------------------

func (s *Server) listPosts(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r)
	items, err := s.svc.ListPosts(r.Context(), chi.URLParam(r, "id"), s.me(r), limit, offset)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) createReply(w http.ResponseWriter, r *http.Request) {
	var in service.ReplyInput
	if !decode(w, r, &in) {
		return
	}
	p, err := s.svc.CreateReply(r.Context(), chi.URLParam(r, "id"), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	status := http.StatusCreated
	if p.Status != "published" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, dataResp(p))
}

type bodyMDBody struct {
	BodyMD string `json:"bodyMd"`
}

func (s *Server) updatePost(w http.ResponseWriter, r *http.Request) {
	var b bodyMDBody
	if !decode(w, r, &b) {
		return
	}
	p, err := s.svc.UpdatePost(r.Context(), chi.URLParam(r, "id"), s.me(r), b.BodyMD)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(p))
}

func (s *Server) deletePost(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeletePost(r.Context(), chi.URLParam(r, "id"), s.me(r)); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) markAnswer(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.MarkAnswer(r.Context(), chi.URLParam(r, "id"), s.me(r)); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- search / tags ----------------------------------------------------------

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := pageParams(r)
	items, err := s.svc.Search(r.Context(), s.me(r), service.SearchInput{
		Query: q.Get("q"), SpaceUUID: q.Get("space"), BoardID: q.Get("board"),
		AuthorUUID: q.Get("author"), TopicType: q.Get("type"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	limit, _ := pageParams(r)
	items, err := s.svc.ListTags(r.Context(), chi.URLParam(r, "space"), s.me(r),
		r.URL.Query().Get("q"), limit)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

// --- subscriptions ----------------------------------------------------------

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListSubscriptions(r.Context(), s.me(r))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	var in service.SubscriptionInput
	if !decode(w, r, &in) {
		return
	}
	if err := s.svc.Subscribe(r.Context(), s.me(r), in); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- attachments ------------------------------------------------------------

func (s *Server) presign(w http.ResponseWriter, r *http.Request) {
	var in service.PresignInput
	if !decode(w, r, &in) {
		return
	}
	res, err := s.svc.PresignUpload(r.Context(), s.me(r), in)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, dataResp(res))
}

func (s *Server) attachmentURL(w http.ResponseWriter, r *http.Request) {
	url, err := s.svc.AttachmentURL(r.Context(), s.me(r), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteAttachment(r.Context(), s.me(r), chi.URLParam(r, "id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- moderation / reports ---------------------------------------------------

func (s *Server) reportTopic(w http.ResponseWriter, r *http.Request) { s.report(w, r, "topic") }
func (s *Server) reportPost(w http.ResponseWriter, r *http.Request)  { s.report(w, r, "post") }

func (s *Server) report(w http.ResponseWriter, r *http.Request, targetType string) {
	var in service.ReportInput
	if !decode(w, r, &in) {
		return
	}
	if err := s.svc.ReportTarget(r.Context(), s.me(r), targetType, chi.URLParam(r, "id"), in); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listReports(w http.ResponseWriter, r *http.Request) {
	limit, _ := pageParams(r)
	items, err := s.svc.ListReports(r.Context(), chi.URLParam(r, "space"), s.me(r), limit)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) resolveReport(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ResolveReport(r.Context(), chi.URLParam(r, "id"), s.me(r), "resolved"); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dismissReport(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ResolveReport(r.Context(), chi.URLParam(r, "id"), s.me(r), "dismissed"); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) moderationQueue(w http.ResponseWriter, r *http.Request) {
	limit, _ := pageParams(r)
	items, err := s.svc.ListModerationQueue(r.Context(), chi.URLParam(r, "space"), s.me(r),
		r.URL.Query().Get("state"), limit)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, list(items))
}

func (s *Server) approveModeration(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.ApproveModeration(r.Context(), chi.URLParam(r, "item"), s.me(r)); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type noteBody struct {
	Note *string `json:"note"`
}

func (s *Server) rejectModeration(w http.ResponseWriter, r *http.Request) {
	var b noteBody
	_ = decodeOptional(r, &b)
	if err := s.svc.RejectModeration(r.Context(), chi.URLParam(r, "item"), s.me(r), b.Note); err != nil {
		writeError(w, s.log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin ------------------------------------------------------------------

func (s *Server) refreshCache(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := s.svc.RefreshCache(r.Context(), q.Get("type"), q.Get("id")); err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
