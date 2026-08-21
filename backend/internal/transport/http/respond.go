package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"ludiskus/internal/domain"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": map[string]any{"code": "bad_request", "message": msg},
	})
}

// writeError ánh xạ lỗi miền → mã HTTP với body chuẩn.
func writeError(w http.ResponseWriter, log *slog.Logger, err error) {
	code, status := "internal_error", http.StatusInternalServerError
	switch {
	case errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", http.StatusNotFound
	case errors.Is(err, domain.ErrForbidden):
		code, status = "forbidden", http.StatusForbidden
	case errors.Is(err, domain.ErrUnauthorized):
		code, status = "unauthorized", http.StatusUnauthorized
	case errors.Is(err, domain.ErrValidation):
		code, status = "validation_error", http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrConflict):
		code, status = "conflict", http.StatusConflict
	case errors.Is(err, domain.ErrTooLarge):
		code, status = "too_large", http.StatusRequestEntityTooLarge
	case errors.Is(err, domain.ErrInvalidRef):
		code, status = "INVALID_REF", http.StatusBadRequest
	case errors.Is(err, domain.ErrInvalidCursor):
		code, status = "INVALID_CURSOR", http.StatusBadRequest
	case errors.Is(err, domain.ErrSortNotSupported):
		code, status = "SORT_NOT_SUPPORTED", http.StatusBadRequest
	case errors.Is(err, domain.ErrServiceNotRegistered):
		code, status = "SERVICE_NOT_REGISTERED", http.StatusNotFound
	case errors.Is(err, domain.ErrCommentDisabled):
		code, status = "COMMENT_DISABLED", http.StatusForbidden
	case errors.Is(err, domain.ErrCommentNotAllowed):
		code, status = "COMMENT_NOT_ALLOWED", http.StatusForbidden
	case errors.Is(err, domain.ErrResourceBlocked):
		code, status = "RESOURCE_BLOCKED", http.StatusForbidden
	case errors.Is(err, domain.ErrResourceGone):
		code, status = "RESOURCE_GONE", http.StatusGone
	case errors.Is(err, domain.ErrThreadLocked):
		code, status = "THREAD_LOCKED", http.StatusLocked
	case errors.Is(err, domain.ErrEditWindowClosed):
		code, status = "EDIT_WINDOW_CLOSED", http.StatusForbidden
	case errors.Is(err, domain.ErrDuplicateComment):
		code, status = "DUPLICATE_COMMENT", http.StatusConflict
	case errors.Is(err, domain.ErrRateLimited):
		code, status = "RATE_LIMITED", http.StatusTooManyRequests
	case errors.Is(err, domain.ErrResolverUnavailable):
		code, status = "RESOURCE_RESOLVER_UNAVAILABLE", http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrResolverMissing):
		code, status = "RESOURCE_RESOLVER_MISSING", http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrServiceScope):
		code, status = "SERVICE_SCOPE_MISMATCH", http.StatusForbidden
	case errors.Is(err, domain.ErrUnknownServiceClient):
		code, status = "UNKNOWN_SERVICE_CLIENT", http.StatusForbidden
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		log.Error("internal error", "err", err)
		msg = "internal error"
	}
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

type listResponse[T any] struct {
	Data []T `json:"data"`
}

func list[T any](items []T) listResponse[T] { return listResponse[T]{Data: items} }

func dataResp(v any) map[string]any { return map[string]any{"data": v} }
