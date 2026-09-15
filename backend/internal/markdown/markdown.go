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
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

// mentionRe khớp @code hoặc @uuid (chữ, số, _, ., -). Bỏ qua email vì cần ký tự
// trước @ là khoảng trắng/đầu chuỗi.
var mentionRe = regexp.MustCompile(`(^|[\s(])@(` + handlePattern + `)`)

type Renderer struct {
	richMD      goldmark.Markdown
	basicMD     goldmark.Markdown
	policyRich  *bluemonday.Policy
	policyBasic *bluemonday.Policy
}

func New() *Renderer {
	richMD := goldmark.New(
		goldmark.WithExtensions(extension.GFM, mentionExtension{}),
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)
	rich := bluemonday.UGCPolicy()
	rich.AllowAttrs("class").Globally()
	rich.AllowAttrs("data-mention").Matching(regexp.MustCompile(`^` + handlePattern + `$`)).OnElements("span")
	rich.RequireNoFollowOnLinks(true)
	rich.AddTargetBlankToFullyQualifiedLinks(true)
	basicMD := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Linkify, mentionExtension{}),
		goldmark.WithRendererOptions(gmhtml.WithHardWraps()),
	)
	basic := bluemonday.NewPolicy()
	basic.AllowElements("p", "br", "strong", "em", "del", "code", "pre", "blockquote", "ul", "ol", "li", "a", "span")
	basic.AllowAttrs("href", "title").OnElements("a")
	// Chỉ cho span mang đúng hai thuộc tính của chip mention, và class phải là
	// "mention" — không mở cửa class tự do như policy rich.
	basic.AllowAttrs("class").Matching(regexp.MustCompile(`^mention$`)).OnElements("span")
	basic.AllowAttrs("data-mention").Matching(regexp.MustCompile(`^` + handlePattern + `$`)).OnElements("span")
	basic.AllowStandardURLs()
	basic.RequireNoFollowOnLinks(true)
	basic.AddTargetBlankToFullyQualifiedLinks(true)
	return &Renderer{richMD: richMD, basicMD: basicMD, policyRich: rich, policyBasic: basic}
}

// Render trả HTML đã sanitize từ Markdown. @mention hiện nguyên handle.
func (r *Renderer) Render(src string) string {
	return r.RenderMode("rich", src)
}

// RenderWithMentions như Render nhưng đổi "@code" thành họ tên do resolve trả
// về. Handle gốc vẫn nằm ở data-mention để còn dựng lại được sau này.
func (r *Renderer) RenderWithMentions(src string, resolve MentionResolver) string {
	return r.RenderModeWithMentions("rich", src, resolve)
}

// RenderModeWithMentions là RenderMode có phân giải tên. Cả ba chế độ đều dựng
// chip: LuComment mặc định chạy "basic", nên nếu chỉ "rich" biết đổi tên thì
// bình luận vẫn hiện @code trong khi bài viết hiện họ tên.
func (r *Renderer) RenderModeWithMentions(mode, src string, resolve MentionResolver) string {
	if resolve == nil {
		return r.RenderMode(mode, src)
	}
	if mode == "plain" {
		return renderPlain(src, resolve)
	}
	md, policy := r.basicMD, r.policyBasic
	if mode == "rich" {
		md, policy = r.richMD, r.policyRich
	}
	pc := parser.NewContext()
	pc.Set(mentionResolverKey, resolve)
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf, parser.WithContext(pc)); err != nil {
		return policy.Sanitize(src)
	}
	return policy.Sanitize(buf.String())
}

// RenderMode renders comment markdown using the requested, allowlisted level.
func (r *Renderer) RenderMode(mode, src string) string {
	if mode == "plain" {
		return renderPlain(src, nil)
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

// renderPlain không có parser, nên chip mention ở đây dựng bằng regex trên văn
// bản ĐÃ escape. An toàn vì handle chỉ gồm chữ/số/._- (không có ký tự nào bị
// escape làm đổi hình), và mentionRe đòi ký tự trước @ là khoảng trắng/"(" nên
// không đụng vào email hay đuôi URL.
func renderPlain(src string, resolve MentionResolver) string {
	escaped := html.EscapeString(src)
	for _, pair := range [][2]string{{"javascript:", "javascript&#58;"}, {"data:text/html", "data&#58;text/html"}, {"onerror=", "onerror&#61;"}, {"onload=", "onload&#61;"}, {"onclick=", "onclick&#61;"}, {"onfocus=", "onfocus&#61;"}, {"ontoggle=", "ontoggle&#61;"}, {"onstart=", "onstart&#61;"}} {
		escaped = strings.ReplaceAll(escaped, pair[0], pair[1])
	}
	escaped = urlRE.ReplaceAllStringFunc(escaped, func(v string) string {
		return `<a href="` + v + `" rel="nofollow noopener" target="_blank">` + v + `</a>`
	})
	escaped = mentionRe.ReplaceAllStringFunc(escaped, func(v string) string {
		m := mentionRe.FindStringSubmatch(v)
		return m[1] + mentionSpan(m[2], mentionLabel(m[2], resolve))
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
