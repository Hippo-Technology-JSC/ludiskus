package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"

	"ludiskus/db"
	"ludiskus/internal/notify"
)

type registeredTemplate struct {
	ID            string
	Name          string
	LocaleDefault string
	Bodies        json.RawMessage
}

// registeredRule là những trường của một Rule mà ludiskus tự quản lý; phần còn
// lại (match, lịch, audience_ref) để người vận hành chỉnh trên lunoti.
type registeredRule struct {
	ID         string
	Name       string
	TemplateID string
	Category   string
	Priority   string
	Enabled    bool
}

type seedEventType struct {
	Code            string   `json:"code"`
	Name            string   `json:"name"`
	Category        string   `json:"category"`
	DefaultChannels []string `json:"default_channels"`
}

type seedTemplate struct {
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	LocaleDefault string          `json:"locale_default"`
	Bodies        json.RawMessage `json:"bodies"`
}

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

func (s *Service) ProcessPersonalFileSync(ctx context.Context, log *slog.Logger) {
	if s.personalFiles == nil || !s.personalFiles.Enabled() {
		return
	}
	items, err := s.repo.ClaimPersonalFileSync(ctx, 50)
	if err != nil {
		log.Error("claim personal file sync", "err", err)
		return
	}
	if len(items) == 0 {
		return
	}
	if err := s.personalFiles.Send(ctx, items); err != nil {
		_ = s.repo.MarkPersonalFileSyncFailed(ctx, items, err.Error())
		log.Warn("sync attachments to personal files", "count", len(items), "err", err)
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if err := s.repo.MarkPersonalFileSyncSent(ctx, ids); err != nil {
		log.Error("mark personal file sync sent", "err", err)
	}
}

func (s *Service) ReconcilePersonalFiles(ctx context.Context, log *slog.Logger) {
	n, err := s.repo.EnqueueAttachedPersonalFiles(ctx, 500)
	if err != nil {
		log.Error("reconcile personal files", "err", err)
		return
	}
	if n > 0 {
		log.Info("queued attachment personal file backfill", "count", n)
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
		EventTypes []seedEventType `json:"event_types"`
		Templates  []seedTemplate  `json:"templates"`
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
	templates := s.fetchTemplates(ctx, log)
	for _, t := range sf.Templates {
		body := map[string]any{
			"code": t.Code, "name": t.Name, "localeDefault": t.LocaleDefault, "bodies": t.Bodies,
		}
		var err error
		if current, ok := templates[t.Code]; ok {
			if sameTemplate(current.Name, current.LocaleDefault, current.Bodies, t.Name, t.LocaleDefault, t.Bodies) {
				continue
			}
			err = s.lunoti.Patch(ctx, "/api/v1/templates/"+current.ID, body)
		} else {
			err = s.lunoti.Post(ctx, "/api/v1/templates", body)
		}
		if err != nil {
			log.Warn("đăng ký template", "code", t.Code, "err", err)
		}
	}
	s.registerRules(ctx, log, sf.EventTypes, sf.Templates)
	log.Info("đã đăng ký event-type/template/rule lên lunoti")
}

func (s *Service) fetchTemplates(ctx context.Context, log *slog.Logger) map[string]registeredTemplate {
	var existing struct {
		Data []struct {
			ID            string          `json:"id"`
			Code          string          `json:"code"`
			Name          string          `json:"name"`
			LocaleDefault string          `json:"localeDefault"`
			Bodies        json.RawMessage `json:"bodies"`
		} `json:"data"`
	}
	out := map[string]registeredTemplate{}
	if err := s.lunoti.Get(ctx, "/api/v1/templates", &existing); err != nil {
		log.Warn("không đọc được template hiện có của lunoti", "err", err)
		return out
	}
	for _, item := range existing.Data {
		out[item.Code] = registeredTemplate{item.ID, item.Name, item.LocaleDefault, item.Bodies}
	}
	return out
}

// registerRules nối mỗi event_type với template CÙNG CODE. Thiếu mắt xích này,
// lunoti không tìm được rule nào cho event và rơi vào nhánh "phát ngầm": mọi
// thông báo đều là "Thông báo mới / Bạn có một cập nhật mới.", template đăng ký
// đẹp tới đâu cũng không ai đọc tới (lunoti internal/service/ingest.go).
//
// Quan hệ 1–1 được SUY RA chứ không khai thành danh sách thứ ba trong seed: ba
// danh sách song song là ba cơ hội để lệch nhau mà không ai thấy.
func (s *Service) registerRules(ctx context.Context, log *slog.Logger, eventTypes []seedEventType, tpls []seedTemplate) {
	seeded := map[string]bool{}
	for _, t := range tpls {
		seeded[t.Code] = true
	}
	// Đọc LẠI template sau vòng tạo/sửa ở trên: bản vừa POST mới có ID, còn bản
	// đã tồn tại thì POST trả 409 và client nuốt lỗi nên cũng không có ID.
	templates := s.fetchTemplates(ctx, log)
	rules := s.fetchRules(ctx, log)
	for _, et := range eventTypes {
		if !seeded[et.Code] {
			continue
		}
		tpl, ok := templates[et.Code]
		if !ok {
			log.Warn("bỏ qua rule vì chưa có template", "code", et.Code)
			continue
		}
		body := map[string]any{
			"code": et.Code, "name": et.Name, "sourceService": "ludiskus",
			"triggerType": "event", "eventTypeCode": et.Code,
			"audienceType": "event_recipients", "templateId": tpl.ID,
			// Category phải BẰNG category của event_type: lunoti dùng nó để tra
			// tuỳ chọn nhận thông báo của người dùng, lệch một cái là tuỳ chọn
			// đang có của họ không còn khớp nữa.
			"category": et.Category, "priority": "normal", "enabled": true,
			// Không gửi channels: rỗng thì lunoti dùng defaultChannels của
			// event_type, giữ đúng một nơi khai báo kênh.
		}
		var err error
		if current, ok := rules[et.Code]; ok {
			if current.Name == et.Name && current.TemplateID == tpl.ID &&
				current.Category == et.Category && current.Priority == "normal" && current.Enabled {
				continue
			}
			err = s.lunoti.Patch(ctx, "/api/v1/rules/"+current.ID, body)
		} else {
			err = s.lunoti.Post(ctx, "/api/v1/rules", body)
		}
		if err != nil {
			log.Warn("đăng ký rule", "code", et.Code, "err", err)
		}
	}
}

func (s *Service) fetchRules(ctx context.Context, log *slog.Logger) map[string]registeredRule {
	var existing struct {
		Data []struct {
			ID         string `json:"id"`
			Code       string `json:"code"`
			Name       string `json:"name"`
			TemplateID string `json:"templateId"`
			Category   string `json:"category"`
			Priority   string `json:"priority"`
			Enabled    bool   `json:"enabled"`
		} `json:"data"`
	}
	out := map[string]registeredRule{}
	if err := s.lunoti.Get(ctx, "/api/v1/rules?source=ludiskus", &existing); err != nil {
		log.Warn("không đọc được rule hiện có của lunoti", "err", err)
		return out
	}
	for _, item := range existing.Data {
		out[item.Code] = registeredRule{item.ID, item.Name, item.TemplateID, item.Category, item.Priority, item.Enabled}
	}
	return out
}

func sameTemplate(currentName, currentLocale string, currentBodies json.RawMessage, wantedName, wantedLocale string, wantedBodies json.RawMessage) bool {
	if currentName != wantedName || currentLocale != wantedLocale {
		return false
	}
	var current, wanted any
	if json.Unmarshal(currentBodies, &current) != nil || json.Unmarshal(wantedBodies, &wanted) != nil {
		return false
	}
	return reflect.DeepEqual(current, wanted)
}

// EnsureStorage tạo bucket khi khởi động (nếu cấu hình MinIO).
func (s *Service) EnsureStorage(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	return s.store.EnsureBucket(ctx)
}
