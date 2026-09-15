package markdown

import (
	"html"
	"regexp"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// handlePattern là ĐỊNH NGHĨA DUY NHẤT của một @handle (code hoặc uuid). Cả
// Mentions() (trích để gửi thông báo) lẫn bộ parse inline (dựng chip hiển thị)
// đều dùng nó, nên thứ người đọc thấy là chip luôn trùng với thứ được báo tin.
const handlePattern = `[A-Za-z0-9][A-Za-z0-9_.\-]{1,63}`

// inlineMentionRe neo tại dấu @ hiện tại của reader (ký tự đứng trước đã được
// kiểm riêng bằng PrecendingCharacter).
var inlineMentionRe = regexp.MustCompile(`^@(` + handlePattern + `)`)

// MentionResolver trả nhãn hiển thị của một handle. ok=false → giữ nguyên
// "@handle" (người không tồn tại hoặc không phải thành viên Space).
type MentionResolver func(handle string) (label string, ok bool)

// mentionResolverKey đưa resolver từ lúc gọi Convert xuống bộ parse inline.
// Đi qua parser.Context (mỗi lần Convert một context) nên goldmark.Markdown
// dùng chung vẫn an toàn khi gọi đồng thời.
var mentionResolverKey = parser.NewContextKey()

var kindMention = ast.NewNodeKind("LudiskusMention")

// mentionNode giữ CẢ handle lẫn nhãn: handle là thứ bền vững ghi vào
// data-mention, nhãn chỉ là ảnh chụp lúc render. Nhãn đã gồm sẵn mọi ký tự sẽ
// hiện ra — phân giải được thì là họ tên trần, không thì là "@code" y như người
// dùng gõ. Renderer không tự thêm "@" vào, nếu không sẽ không cách nào bỏ được.
type mentionNode struct {
	ast.BaseInline
	Handle string
	Label  string
}

func (n *mentionNode) Kind() ast.NodeKind { return kindMention }

func (n *mentionNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Handle": n.Handle, "Label": n.Label}, nil)
}

type mentionParser struct{}

func (mentionParser) Trigger() []byte { return []byte{'@'} }

func (mentionParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	// Chỉ nhận @ ở đầu dòng, sau khoảng trắng hoặc sau "(" — nếu không thì
	// "ai@example.com" sẽ biến thành mention của "example.com".
	if before := block.PrecendingCharacter(); !unicode.IsSpace(before) && before != '(' {
		return nil
	}
	line, _ := block.PeekLine()
	m := inlineMentionRe.FindSubmatchIndex(line)
	if m == nil {
		return nil
	}
	handle := string(line[m[2]:m[3]])
	block.Advance(m[1])
	resolve, _ := pc.Get(mentionResolverKey).(MentionResolver)
	return &mentionNode{Handle: handle, Label: mentionLabel(handle, resolve)}
}

// mentionLabel trả nhãn hiển thị: họ tên nếu phân giải được, nếu không thì giữ
// nguyên "@handle" như người dùng gõ — bỏ "@" lúc chưa biết là ai sẽ biến chữ
// tác giả gõ thành một từ trơ vô nghĩa.
func mentionLabel(handle string, resolve MentionResolver) string {
	if resolve != nil {
		if name, ok := resolve(handle); ok && name != "" {
			return name
		}
	}
	return "@" + handle
}

// mentionSpan là ĐỊNH NGHĨA DUY NHẤT của chip mention trong HTML. Cả bộ render
// goldmark (rich/basic) lẫn chế độ plain đều đi qua đây, nên hai đường không thể
// sinh ra hai hình dạng khác nhau — mà chỉ một hình dạng mới lọt qua bộ lọc
// bluemonday và mới khớp CSS `.mention`.
func mentionSpan(handle, label string) string {
	return `<span class="mention" data-mention="` + html.EscapeString(handle) + `">` + html.EscapeString(label) + `</span>`
}

type mentionRenderer struct{}

func (r mentionRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMention, r.render)
}

func (mentionRenderer) render(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*mentionNode)
	_, _ = w.WriteString(mentionSpan(n.Handle, n.Label))
	return ast.WalkContinue, nil
}

type mentionExtension struct{}

func (mentionExtension) Extend(m goldmark.Markdown) {
	// Ưu tiên 500: sau bộ parse liên kết/nhấn mạnh mặc định, trước linkify (999)
	// — nhưng thực tế không tranh chấp vì '@' không phải trigger của ai khác.
	m.Parser().AddOptions(parser.WithInlineParsers(util.Prioritized(mentionParser{}, 500)))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(util.Prioritized(mentionRenderer{}, 500)))
}

// MentionsIn trích handle theo ĐÚNG cây cú pháp người đọc nhìn thấy: chỉ những
// @mention thật sự thành chip mới được tính. Khác Mentions() (regex trên văn bản
// thô) ở chỗ @ nằm trong code block không còn bị coi là nhắc tên — dán một đoạn
// log hay file cấu hình có "@ai-đó" không còn báo tin cho người ta nữa.
func (r *Renderer) MentionsIn(src string) []string {
	return r.MentionsInMode("rich", src)
}

// MentionsInMode trích theo cây cú pháp của ĐÚNG chế độ sẽ dùng để render. Chế
// độ "plain" không có parser nên dùng lại regex — khớp với cách renderPlain dựng
// chip, nên chip và danh sách trích vẫn không lệch nhau.
func (r *Renderer) MentionsInMode(mode, src string) []string {
	if mode == "plain" {
		return Mentions(src)
	}
	md := r.basicMD
	if mode == "rich" {
		md = r.richMD
	}
	doc := md.Parser().Parse(text.NewReader([]byte(src)))
	seen := map[string]bool{}
	out := []string{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		m, ok := n.(*mentionNode)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		h := strings.ToLower(m.Handle)
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
		return ast.WalkContinue, nil
	})
	return out
}
