package service

import (
	"encoding/json"
	"testing"
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
