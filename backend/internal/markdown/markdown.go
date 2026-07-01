// Package markdown render Markdown → HTML (GFM) rồi sanitize chống XSS, và trích
// @mention. Mọi nội dung văn bản dài đều đi qua đây (docs/03 §3.12).
package markdown

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

// mentionRe khớp @code hoặc @uuid (chữ, số, _, ., -). Bỏ qua email vì cần ký tự
// trước @ là khoảng trắng/đầu chuỗi.
var mentionRe = regexp.MustCompile(`(^|[\s(])@([A-Za-z0-9][A-Za-z0-9_.\-]{1,63})`)

type Renderer struct {
	md     goldmark.Markdown
	policy *bluemonday.Policy
}

func New() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Globally()
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return &Renderer{md: md, policy: p}
}

// Render trả HTML đã sanitize từ Markdown.
func (r *Renderer) Render(src string) string {
	var buf bytes.Buffer
	if err := r.md.Convert([]byte(src), &buf); err != nil {
		return r.policy.Sanitize(src)
	}
	return r.policy.Sanitize(buf.String())
}

// Mentions trích danh sách handle (code/uuid) được nhắc tới, đã khử trùng lặp,
// giữ chữ thường để khớp profile_cache.code.
func Mentions(src string) []string {
	matches := mentionRe.FindAllStringSubmatch(src, -1)
	seen := map[string]bool{}
	out := []string{}
	for _, m := range matches {
		h := strings.ToLower(m[2])
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// Excerpt cắt ngắn nội dung thô (bỏ markup cơ bản) cho thông báo.
func Excerpt(src string, max int) string {
	s := strings.Join(strings.Fields(src), " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return strings.TrimSpace(string(runes[:max])) + "…"
}
