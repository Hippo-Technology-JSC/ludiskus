package markdown

import (
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
