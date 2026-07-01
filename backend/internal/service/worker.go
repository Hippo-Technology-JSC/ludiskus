package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"ludiskus/db"
	"ludiskus/internal/notify"
)

// ProcessOutbox đẩy toàn bộ việc trong outbox sang lunoti tới khi rỗng (docs/08).
func (s *Service) ProcessOutbox(ctx context.Context, log *slog.Logger) {
	for {
		item, err := s.repo.ClaimOutbox(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("claim outbox", "err", err)
			}
			return
		}
		if item == nil {
			return
		}
		var ev notify.Event
		if err := json.Unmarshal(item.Payload, &ev); err != nil {
			s.repo.MarkOutboxFailed(ctx, item, "payload không hợp lệ: "+err.Error())
			continue
		}
		if !s.lunoti.Enabled() {
			s.repo.MarkOutboxFailed(ctx, item, "lunoti chưa cấu hình")
			continue
		}
		if err := s.lunoti.Send(ctx, ev); err != nil {
			s.repo.MarkOutboxFailed(ctx, item, err.Error())
			continue
		}
		s.repo.MarkOutboxSent(ctx, item.ID)
	}
}

// SyncCaches full-sync profile_cache + space_cache từ HipCore (docs/05 §5.3).
func (s *Service) SyncCaches(ctx context.Context, log *slog.Logger) {
	if !s.ident.Enabled() {
		return
	}
	if n, err := s.ident.FullSyncProfiles(ctx); err != nil {
		log.Error("full sync profiles", "err", err)
	} else {
		log.Info("synced profiles", "count", n)
	}
	if n, err := s.ident.FullSyncSpaces(ctx); err != nil {
		log.Error("full sync spaces", "err", err)
	} else {
		log.Info("synced spaces", "count", n)
	}
}

// CleanupOrphans xoá đính kèm pending quá hạn (docs/07 §7.4).
func (s *Service) CleanupOrphans(ctx context.Context, log *slog.Logger) {
	if s.store == nil {
		return
	}
	ttl := int(s.cfg.AttachTTL.Seconds())
	items, err := s.repo.ListOrphanAttachments(ctx, ttl, 200)
	if err != nil {
		log.Error("list orphan attachments", "err", err)
		return
	}
	for _, a := range items {
		s.store.Remove(ctx, a.ObjectKey)
		s.repo.DeleteAttachment(ctx, a.ID)
	}
	if len(items) > 0 {
		log.Info("cleaned orphan attachments", "count", len(items))
	}
}

// RegisterEventTypes đăng ký event-type + template hệ thống lên lunoti
// (idempotent — docs/08 §8.2). Best-effort: lỗi không chặn khởi động.
func (s *Service) RegisterEventTypes(ctx context.Context, log *slog.Logger) {
	if !s.lunoti.Enabled() {
		log.Warn("lunoti chưa cấu hình — bỏ qua đăng ký event-type")
		return
	}
	raw, err := db.Seeds.ReadFile("seeds/lunoti_event_types.json")
	if err != nil {
		return
	}
	var sf struct {
		EventTypes []struct {
			Code            string   `json:"code"`
			Name            string   `json:"name"`
			Category        string   `json:"category"`
			DefaultChannels []string `json:"default_channels"`
		} `json:"event_types"`
		Templates []struct {
			Code          string          `json:"code"`
			Name          string          `json:"name"`
			LocaleDefault string          `json:"locale_default"`
			Bodies        json.RawMessage `json:"bodies"`
		} `json:"templates"`
	}
	if err := json.Unmarshal(raw, &sf); err != nil {
		return
	}
	for _, et := range sf.EventTypes {
		if err := s.lunoti.Post(ctx, "/api/v1/event-types", map[string]any{
			"code": et.Code, "sourceService": "ludiskus", "name": et.Name,
			"category": et.Category, "defaultChannels": et.DefaultChannels,
		}); err != nil {
			log.Warn("đăng ký event-type", "code", et.Code, "err", err)
		}
	}
	for _, t := range sf.Templates {
		if err := s.lunoti.Post(ctx, "/api/v1/templates", map[string]any{
			"code": t.Code, "name": t.Name, "localeDefault": t.LocaleDefault, "bodies": t.Bodies,
		}); err != nil {
			log.Warn("đăng ký template", "code", t.Code, "err", err)
		}
	}
	log.Info("đã đăng ký event-type/template lên lunoti")
}

// EnsureStorage tạo bucket khi khởi động (nếu cấu hình MinIO).
func (s *Service) EnsureStorage(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.EnsureBucket(ctx)
}
