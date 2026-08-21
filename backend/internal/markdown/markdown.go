// Package markdown render Markdown → HTML (GFM) rồi sanitize chống XSS, và trích
// @mention. Mọi nội dung văn bản dài đều đi qua đây (docs/03 §3.12).
package markdown

import (
	"bytes"
	"html"
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
	richMD      goldmark.Markdown
	basicMD     goldmark.Markdown
	policyRich  *bluemonday.Policy
	policyBasic *bluemonday.Policy
}

func New() *Renderer {
	richMD := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)
	rich := bluemonday.UGCPolicy()
	rich.AllowAttrs("class").Globally()
	rich.RequireNoFollowOnLinks(true)
	rich.AddTargetBlankToFullyQualifiedLinks(true)
	basicMD := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)
	basic := bluemonday.NewPolicy()
	basic.AllowElements("p", "br", "strong", "em", "del", "code", "pre", "blockquote", "ul", "ol", "li", "a")
	basic.AllowAttrs("href", "title").OnElements("a")
	basic.AllowStandardURLs()
	basic.RequireNoFollowOnLinks(true)
	basic.AddTargetBlankToFullyQualifiedLinks(true)
	return &Renderer{richMD: richMD, basicMD: basicMD, policyRich: rich, policyBasic: basic}
}

// Render trả HTML đã sanitize từ Markdown.
func (r *Renderer) Render(src string) string {
	return r.RenderMode("rich", src)
}

// RenderMode renders comment markdown using the requested, allowlisted level.
func (r *Renderer) RenderMode(mode, src string) string {
	if mode == "plain" {
		return renderPlain(src)
	}
	md, policy := r.basicMD, r.policyBasic
	if mode == "rich" {
		md, policy = r.richMD, r.policyRich
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return policy.Sanitize(src)
	}
	return policy.Sanitize(buf.String())
}

func (r *Renderer) RenderBasic(src string) string { return r.RenderMode("basic", src) }
func (r *Renderer) RenderPlain(src string) string { return r.RenderMode("plain", src) }

var urlRE = regexp.MustCompile(`https?://[^\s<]+`)

func renderPlain(src string) string {
	escaped := html.EscapeString(src)
	for _, pair := range [][2]string{{"javascript:", "javascript&#58;"}, {"data:text/html", "data&#58;text/html"}, {"onerror=", "onerror&#61;"}, {"onload=", "onload&#61;"}, {"onclick=", "onclick&#61;"}, {"onfocus=", "onfocus&#61;"}, {"ontoggle=", "ontoggle&#61;"}, {"onstart=", "onstart&#61;"}} {
		escaped = strings.ReplaceAll(escaped, pair[0], pair[1])
	}
	escaped = urlRE.ReplaceAllStringFunc(escaped, func(v string) string {
		return `<a href="` + v + `" rel="nofollow noopener" target="_blank">` + v + `</a>`
	})
	return strings.ReplaceAll(escaped, "\n", "<br>\n")
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
