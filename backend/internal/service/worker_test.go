package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"ludiskus/db"
	"ludiskus/internal/config"
	"ludiskus/internal/notify"
)

func TestSameTemplate(t *testing.T) {
	wanted := json.RawMessage(`{"vi":{"web":{"title":"Ludiskus","body":"Nội dung"}}}`)
	current := json.RawMessage(`{"vi": {"web": {"body": "Nội dung", "title": "Ludiskus"}}}`)
	if !sameTemplate("Mention", "vi", current, "Mention", "vi", wanted) {
		t.Fatal("JSON tương đương phải được giữ nguyên để không tạo phiên bản template mới")
	}
	if sameTemplate("Mention", "vi", current, "Mention mới", "vi", wanted) {
		t.Fatal("template đổi tên phải được cập nhật")
	}
}

// fakeLunoti dựng một lunoti giả ghi lại mọi lượt ghi, để kiểm xem
// RegisterEventTypes có thật sự tạo Rule nối event_type với template không.
type fakeLunoti struct {
	templates map[string]string // code → id
	rules     map[string]map[string]any
	writes    []string
}

func (f *fakeLunoti) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.writes = append(f.writes, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/templates":
			rows := []map[string]any{}
			for code, id := range f.templates {
				rows = append(rows, map[string]any{"id": id, "code": code, "localeDefault": "vi", "bodies": map[string]any{}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/rules":
			rows := []map[string]any{}
			for code, rule := range f.rules {
				row := map[string]any{"code": code}
				for k, v := range rule {
					row[k] = v
				}
				rows = append(rows, row)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/rules":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			code, _ := body["code"].(string)
			body["id"] = "rule-" + code
			f.rules[code] = body
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
}

func TestRegisterEventTypesCreatesRuleLinkingTemplate(t *testing.T) {
	// Template đã có sẵn trên lunoti nhưng KHÔNG có rule nào — đúng hiện trạng
	// làm mọi thông báo rơi về "Thông báo mới / Bạn có một cập nhật mới.".
	fake := &fakeLunoti{
		templates: map[string]string{"ludiskus.post.mentioned": "tpl-mention", "ludiskus.topic.replied": "tpl-reply"},
		rules:     map[string]map[string]any{},
	}
	server := fake.server(t)
	defer server.Close()
	cfg := &config.Config{
		HipcoreURL: server.URL, LunotiAPIURL: server.URL,
		LunotiClientID: "client", LunotiClientSecret: "secret",
	}
	svc := &Service{lunoti: notify.New(cfg), cfg: cfg}
	svc.RegisterEventTypes(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	rule, ok := fake.rules["ludiskus.post.mentioned"]
	if !ok {
		t.Fatalf("không tạo rule cho mention; các lượt ghi: %v", fake.writes)
	}
	if rule["templateId"] != "tpl-mention" {
		t.Errorf("rule không trỏ đúng template: %#v", rule)
	}
	if rule["eventTypeCode"] != "ludiskus.post.mentioned" || rule["triggerType"] != "event" {
		t.Errorf("rule không gắn vào event_type: %#v", rule)
	}
	if rule["audienceType"] != "event_recipients" {
		t.Errorf("rule phải gửi cho người nhận của event: %#v", rule)
	}
	// Category phải bằng category của event_type, nếu không tuỳ chọn nhận thông
	// báo đang có của người dùng sẽ không còn khớp.
	if rule["category"] != "discussion" {
		t.Errorf("category lệch khỏi event_type: %#v", rule)
	}
	if _, sent := rule["channels"]; sent {
		t.Errorf("không được gửi channels, để lunoti dùng defaultChannels của event_type: %#v", rule)
	}
	// Template chưa đăng ký thì không được dựng rule trỏ vào hư không.
	if _, bad := fake.rules["ludiskus.post.reacted"]; bad {
		t.Error("tạo rule cho event_type không có trong seed")
	}
}

func TestRegisterEventTypesSkipsUnchangedRule(t *testing.T) {
	fake := &fakeLunoti{
		templates: map[string]string{"ludiskus.post.mentioned": "tpl-mention"},
		rules: map[string]map[string]any{"ludiskus.post.mentioned": {
			"id": "rule-1", "name": "Bạn được nhắc đến", "templateId": "tpl-mention",
			"category": "discussion", "priority": "normal", "enabled": true,
		}},
	}
	server := fake.server(t)
	defer server.Close()
	cfg := &config.Config{
		HipcoreURL: server.URL, LunotiAPIURL: server.URL,
		LunotiClientID: "client", LunotiClientSecret: "secret",
	}
	svc := &Service{lunoti: notify.New(cfg), cfg: cfg}
	svc.RegisterEventTypes(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, w := range fake.writes {
		if w == "PATCH /api/v1/rules/rule-1" {
			t.Fatal("rule không đổi mà vẫn bị PATCH mỗi lần khởi động")
		}
	}
}

// TestTemplateVariablesAreActuallySent chặn kiểu hỏng im lặng thứ hai của
// template: lunoti thay mọi `{{biến}}` không có trong data bằng CHUỖI RỖNG
// (lunoti/backend/internal/service/render.go, hàm substitute). Nên gõ nhầm tên
// biến lúc sửa lời văn không báo lỗi ở đâu cả — chỉ là thông báo cụt mất một
// mảnh. Bảng dưới phải phản chiếu đúng các key mà events.go và
// comment_notify.go đặt vào Data; sửa nơi này thì sửa cả nơi kia.
func TestTemplateVariablesAreActuallySent(t *testing.T) {
	sent := map[string][]string{
		"ludiskus.topic.replied":      {"actor", "space", "topic", "url"},
		"ludiskus.post.mentioned":     {"actor", "space", "topic", "excerpt", "url"},
		"ludiskus.topic.answered":     {"space", "topic", "url"},
		"ludiskus.moderation.pending": {"space", "url"},
		"ludiskus.moderation.decided": {"decision", "note", "url"},
		"ludiskus.comment.created":    {"actor", "count", "others", "resourceTitle", "excerpt", "url"},
		"ludiskus.comment.replied":    {"actor", "count", "others", "resourceTitle", "excerpt", "url"},
		"ludiskus.comment.mentioned":  {"actor", "count", "others", "resourceTitle", "excerpt", "url"},
		"ludiskus.comment.pending":    {"count", "spaceName", "url"},
		"ludiskus.comment.moderated":  {"decision", "note", "url"},
	}
	raw, err := db.Seeds.ReadFile("seeds/lunoti_event_types.json")
	if err != nil {
		t.Fatal(err)
	}
	var sf struct {
		Templates []seedTemplate `json:"templates"`
	}
	if err := json.Unmarshal(raw, &sf); err != nil {
		t.Fatal(err)
	}
	if len(sf.Templates) == 0 {
		t.Fatal("seed không có template nào — phép thử sẽ luôn xanh")
	}
	varRe := regexp.MustCompile(`{{\s*([\w.]+)\s*}}`)
	for _, tpl := range sf.Templates {
		known, ok := sent[tpl.Code]
		if !ok {
			t.Errorf("template %s chưa khai báo key nào được gửi", tpl.Code)
			continue
		}
		allowed := map[string]bool{"profile_name": true} // lunoti tự thêm
		for _, k := range known {
			allowed[k] = true
		}
		var bodies map[string]map[string]map[string]string
		if err := json.Unmarshal(tpl.Bodies, &bodies); err != nil {
			t.Errorf("template %s: bodies không đọc được: %v", tpl.Code, err)
			continue
		}
		for locale, channels := range bodies {
			for channel, block := range channels {
				for field, text := range block {
					for _, m := range varRe.FindAllStringSubmatch(text, -1) {
						if !allowed[m[1]] {
							t.Errorf("template %s [%s/%s/%s] dùng {{%s}} mà ludiskus không gửi → chỗ đó sẽ rỗng",
								tpl.Code, locale, channel, field, m[1])
						}
					}
				}
			}
		}
	}
}
