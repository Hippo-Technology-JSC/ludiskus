package markdown

import (
	"regexp"
	"strings"
	"testing"
)

func TestCommentModesSanitizeXSS(t *testing.T) {
	payloads := []string{`<script>alert(1)</script>`, `[x](javascript:alert(1))`, `<img src=x onerror=alert(1)>`, `<svg onload=alert(1)>`, `[x](data:text/html,x)`, `<iframe src="https://evil.test">x</iframe>`, `<a onclick="x">x</a>`, `<math href="javascript:x">x</math>`, `<body onload=x>`, `<div style="background:url(javascript:x)">x</div>`, `<form action="javascript:x">`, `<object data="data:text/html,x">`, `<video onerror=x>`, `<details ontoggle=x>`, `<input autofocus onfocus=x>`, `<marquee onstart=x>`, `<table><tr><td>x</td></tr></table>`, `# heading`, `![image](https://evil.test/x.png)`, `<p class=x onclick=y>x</p>`}
	r := New()
	for _, mode := range []string{"plain", "basic", "rich"} {
		for _, src := range payloads {
			out := strings.ToLower(r.RenderMode(mode, src))
			for _, bad := range []string{"<script", "javascript:", "data:text/html", "onerror=", "onload=", "onclick=", "onfocus=", "ontoggle=", "onstart="} {
				if strings.Contains(out, bad) {
					t.Errorf("mode=%s payload=%q contains %q: %s", mode, src, bad, out)
				}
			}
			if mode != "rich" {
				for _, bad := range []string{"<img", "<table", "<h1", "<iframe"} {
					if strings.Contains(out, bad) {
						t.Errorf("mode=%s must block %s: %s", mode, bad, out)
					}
				}
			}
		}
	}
}
func TestRichParity(t *testing.T) {
	r := New()
	for _, src := range []string{"**bold**", "# title", "|a|b|\n|-|-|\n|1|2|", "[link](https://example.com)"} {
		if r.Render(src) != r.RenderMode("rich", src) {
			t.Fatalf("rich parity failed for %q", src)
		}
	}
}

func TestRichKeepsInternalEditorAssetImage(t *testing.T) {
	const path = "/api/ludiskus/attachments/123e4567-e89b-12d3-a456-426614174000/content"
	out := New().Render("![Ảnh chú thích](" + path + ")")
	if !strings.Contains(out, "<img") || !strings.Contains(out, path) {
		t.Fatalf("internal editor image was removed: %s", out)
	}
}

// --- @mention ---------------------------------------------------------------

func fakeNames(m map[string]string) MentionResolver {
	return func(handle string) (string, bool) { n, ok := m[handle]; return n, ok }
}

func TestMentionShowsNameAndKeepsHandle(t *testing.T) {
	out := New().RenderWithMentions("Chào @an.nguyen nhé", fakeNames(map[string]string{"an.nguyen": "Nguyễn Văn An"}))
	if !strings.Contains(out, `data-mention="an.nguyen"`) {
		t.Fatalf("mất handle gốc: %s", out)
	}
	if !strings.Contains(out, ">Nguyễn Văn An</span>") {
		t.Fatalf("không hiện họ tên trần: %s", out)
	}
	// Phân giải được thì KHÔNG còn dấu @ nào trong phần hiển thị.
	if strings.Contains(out, "@Nguyễn Văn An") || strings.Contains(out, ">@") {
		t.Fatalf("vẫn còn ký tự @ trước tên: %s", out)
	}
	if strings.Contains(out, ">@an.nguyen") {
		t.Fatalf("vẫn hiện code: %s", out)
	}
}

func TestMentionUnresolvedKeepsCode(t *testing.T) {
	// Không phân giải được (người ngoài Space) thì phải giữ nguyên chữ người
	// dùng gõ — cả dấu @ — chứ không nuốt mất hay bỏ @ thành chữ trơ.
	for _, out := range []string{
		New().RenderWithMentions("Chào @nguoila", fakeNames(map[string]string{})),
		New().Render("Chào @nguoila"),
	} {
		if !strings.Contains(out, ">@nguoila</span>") {
			t.Fatalf("mention chưa phân giải phải giữ nguyên @code: %s", out)
		}
	}
}

func TestMentionIgnoresEmailAndCode(t *testing.T) {
	r := New()
	resolve := fakeNames(map[string]string{"example.com": "KHÔNG ĐƯỢC HIỆN", "bob": "Bob Trần"})
	for _, tc := range []struct{ src, must string }{
		{"mail ai@example.com nhé", "ai@example.com"},
		{"`@bob` là code", "<code>@bob</code>"},
		{"```\n@bob\n```", "<code>@bob\n</code>"},
		{"[x](https://t.test/@bob)", "https://t.test/@bob"},
	} {
		out := r.RenderWithMentions(tc.src, resolve)
		if !strings.Contains(out, tc.must) {
			t.Errorf("src=%q phải giữ %q, nhận: %s", tc.src, tc.must, out)
		}
		if strings.Contains(out, "Bob Trần") || strings.Contains(out, "KHÔNG ĐƯỢC HIỆN") {
			t.Errorf("src=%q bị đổi thành mention: %s", tc.src, out)
		}
	}
}

// TestMentionChipMatchesExtraction là cổng chặn quan trọng nhất: người đọc thấy
// chip nào thì Mentions() phải trích đúng handle đó, nếu không sẽ có bài hiện
// tên ai đó mà người ấy không hề được báo tin (hoặc ngược lại).
func TestMentionChipMatchesExtraction(t *testing.T) {
	chipRe := regexp.MustCompile(`data-mention="([^"]+)"`)
	r := New()
	for _, src := range []string{
		"@bob đầu dòng",
		"giữa câu @bob nhé",
		"(@bob) trong ngoặc",
		"- @bob\n- @an.nguyen",
		"> @bob trích dẫn",
		"mail ai@example.com và @bob",
		"@bob @bob @an.nguyen trùng lặp",
		"không có ai ở đây",
		"@bob\n@an.nguyen xuống dòng",
		"`@bob` chỉ là code",    // không chip, cũng không báo tin
		"**@bob** trong in đậm", // dấu * không phải khoảng trắng → cả hai bỏ qua
		"```\n@bob\n```",
		"[x](https://t.test/@bob)",
	} {
		out := r.Render(src)
		seen := map[string]bool{}
		chips := []string{}
		for _, m := range chipRe.FindAllStringSubmatch(out, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				chips = append(chips, m[1])
			}
		}
		want := r.MentionsIn(src)
		if strings.Join(chips, ",") != strings.Join(want, ",") {
			t.Errorf("src=%q chip=%v nhưng MentionsIn=%v", src, chips, want)
		}
	}
}

func TestMentionNameIsEscapedAndNotForgeable(t *testing.T) {
	out := New().RenderWithMentions("@evil", fakeNames(map[string]string{"evil": `<img src=x onerror=alert(1)>`}))
	// Tên lấy từ HipCore vẫn phải escape: nó nằm trong nội dung chip do ta tự
	// ghép chuỗi, không đi qua đường markdown.
	if strings.Contains(out, "<img") {
		t.Fatalf("tên chưa được escape: %s", out)
	}
	if !strings.Contains(out, "&lt;img") {
		t.Fatalf("tên bị nuốt thay vì escape: %s", out)
	}
	// Người dùng tự gõ HTML chip không được qua cửa: goldmark không bật Unsafe
	// nên raw HTML bị loại trước cả bluemonday.
	forged := New().Render(`<span class="mention" data-mention="admin">@Quản trị viên</span>`)
	if strings.Contains(forged, "data-mention") {
		t.Fatalf("giả mạo chip lọt lưới: %s", forged)
	}
}

// TestMentionsInIgnoresCodeBlocks giữ khác biệt giữa hai đường trích: bài viết
// dùng MentionsIn (theo cây cú pháp) nên dán log có "@ai-đó" không báo tin;
// Mentions() dạng regex vẫn còn cho bình luận, vốn không render chip.
func TestMentionsInIgnoresCodeBlocks(t *testing.T) {
	const src = "```\n@bob\n```"
	if got := New().MentionsIn(src); len(got) != 0 {
		t.Fatalf("code block vẫn bị coi là mention: %v", got)
	}
	if got := Mentions(src); len(got) != 1 || got[0] != "bob" {
		t.Fatalf("Mentions() dạng regex đã đổi hành vi ngoài dự tính: %v", got)
	}
}
