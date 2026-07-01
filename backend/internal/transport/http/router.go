// Package http: handler + router REST /api/v1 (docs/10).
package http

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"ludiskus/internal/auth"
	"ludiskus/internal/service"
)

type Server struct {
	svc *service.Service
	log *slog.Logger
}

func NewRouter(svc *service.Service, authn *auth.Authenticator, log *slog.Logger) http.Handler {
	s := &Server{svc: svc, log: log}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	r.Route("/api/v1", func(r chi.Router) {
		// --- Người dùng → ludiskus (Bearer user, qua BFF) ---
		r.Group(func(r chi.Router) {
			r.Use(authn.UserMiddleware)

			// Space-forum
			r.Get("/spaces", s.listSpaces)
			r.Route("/spaces/{space}", func(r chi.Router) {
				r.Get("/", s.getForum)
				r.Post("/enable", s.enableForum)
				r.Patch("/settings", s.updateForumSettings)
				r.Get("/boards", s.listBoards)
				r.Post("/boards", s.createBoard)
				r.Get("/tags", s.listTags)
				r.Get("/moderators", s.listModerators)
				r.Post("/moderators", s.addModerator)
				r.Delete("/moderators", s.removeModerator)
				r.Get("/moderation/queue", s.moderationQueue)
				r.Get("/reports", s.listReports)
			})

			// Board
			r.Route("/boards/{id}", func(r chi.Router) {
				r.Patch("/", s.updateBoard)
				r.Delete("/", s.deleteBoard)
				r.Get("/topics", s.listTopics)
				r.Post("/topics", s.createTopic)
			})

			// Topic
			r.Route("/topics/{id}", func(r chi.Router) {
				r.Get("/", s.getTopic)
				r.Patch("/", s.updateTopic)
				r.Delete("/", s.deleteTopic)
				r.Post("/{action}", s.topicAction) // lock|unlock|pin|unpin
				r.Get("/posts", s.listPosts)
				r.Post("/posts", s.createReply)
				r.Post("/report", s.reportTopic)
			})

			// Post
			r.Route("/posts/{id}", func(r chi.Router) {
				r.Patch("/", s.updatePost)
				r.Delete("/", s.deletePost)
				r.Post("/answer", s.markAnswer)
				r.Put("/reactions", s.toggleReaction)
				r.Post("/report", s.reportPost)
			})

			// Tìm kiếm
			r.Get("/search", s.search)

			// Theo dõi
			r.Get("/subscriptions", s.listSubscriptions)
			r.Put("/subscriptions", s.subscribe)

			// Đính kèm
			r.Post("/attachments/presign", s.presign)
			r.Get("/attachments/{id}/url", s.attachmentURL)
			r.Delete("/attachments/{id}", s.deleteAttachment)

			// Kiểm duyệt
			r.Post("/moderation/{item}/approve", s.approveModeration)
			r.Post("/moderation/{item}/reject", s.rejectModeration)
			r.Post("/reports/{id}/resolve", s.resolveReport)
			r.Post("/reports/{id}/dismiss", s.dismissReport)
		})

		// --- Service/admin → ludiskus (client-credentials) ---
		r.Group(func(r chi.Router) {
			r.Use(authn.ServiceMiddleware)
			r.Post("/admin/cache/refresh", s.refreshCache)
		})
	})

	return r
}

// --- system -----------------------------------------------------------------

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	checks := s.svc.Ready(r.Context())
	status := http.StatusOK
	for _, v := range checks {
		if v != "ok" && v != "disabled" {
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, checks)
}

// --- helpers ----------------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		badRequest(w, "JSON body không hợp lệ")
		return false
	}
	return true
}

// decodeOptional giải JSON body nếu có; body rỗng không phải lỗi.
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(v)
	if err == io.EOF {
		return nil
	}
	return err
}

func pageParams(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("per_page"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return limit, (page - 1) * limit
}
