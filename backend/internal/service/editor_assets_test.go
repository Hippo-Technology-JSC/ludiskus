package service

import "testing"

func TestValidateEditorAssetReferences(t *testing.T) {
	id := "123e4567-e89b-12d3-a456-426614174000"
	tests := []struct {
		name string
		body string
		ids  []string
		ok   bool
	}{
		{name: "none", body: "hello", ok: true},
		{name: "attached", body: "![ảnh](/api/ludiskus/attachments/" + id + "/content)", ids: []string{id}, ok: true},
		{name: "missing id", body: "![ảnh](/api/ludiskus/attachments/" + id + "/content)"},
		{name: "malformed", body: "/api/ludiskus/attachments/not-an-id/content", ids: []string{id}},
		{name: "external image unaffected", body: "![ảnh](https://example.test/a.png)", ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEditorAssetReferences(tt.body, tt.ids)
			if (err == nil) != tt.ok {
				t.Fatalf("err=%v, want ok=%v", err, tt.ok)
			}
		})
	}
}
