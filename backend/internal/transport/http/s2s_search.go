package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"ludiskus/internal/auth"
	"ludiskus/internal/domain"
	"ludiskus/internal/service"
)

// Hợp đồng Search Provider của LuSpotlight (lufami/docs/luspotlight.md §8).
//
// Route này nằm dưới ĐÚNG middleware auth người dùng như mọi route khác của
// ludiskus — cố ý KHÔNG dùng nhóm S2S/requireService. Lời gọi này chạy thay mặt
// NGƯỜI DÙNG, không thay mặt lufami: lufami chuyển tiếp bearer của người dùng
// và ký lại header gateway, còn quyền thì do chính ludiskus quyết định, bằng
// đúng danh sách Space mà người đó được xem — y hệt route /search công khai.
//
// Nếu route này nằm dưới requireService thì ludiskus sẽ chỉ biết "có một service
// gọi tôi" và buộc phải tin một profile_uuid nào đó trong body — tức là bất kỳ
// ai giữ một token service đều đọc được thảo luận riêng tư của mọi người.
//
// Hai loại, CÙNG một phạm vi: `topic` (Service.Search) và `board`
// (Service.SearchBoards) đều lấy Space từ searchableSpaces — không loại nào có
// mệnh đề quyền riêng.

type s2sSearchRequest struct {
	Query string   `json:"query"`
	Limit int      `json:"limit"`
	Types []string `json:"types"`
}

type s2sSearchItem struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Subtitle  string    `json:"subtitle,omitempty"`
	Snippet   string    `json:"snippet,omitempty"`
	Icon      string    `json:"icon,omitempty"`
	URL       string    `json:"url"`
	SpaceUUID string    `json:"spaceUuid,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Rank là THỨ HẠNG trong nội bộ ludiskus, bắt đầu từ 1. Cố ý không gửi
	// ts_rank thô: điểm của ludiskus và điểm của luxtory không cùng đơn vị, nên
	// cộng chúng lại ở lufami sẽ tạo ra một con số trông có nghĩa mà không có.
	Rank int `json:"rank"`
}

type s2sSearchResponse struct {
	Items      []s2sSearchItem `json:"items"`
	NextCursor *string         `json:"nextCursor"`
	Truncated  bool            `json:"truncated"`
}

func (s *Server) s2sSearch(w http.ResponseWriter, r *http.Request) {
	var in s2sSearchRequest
	if !decode(w, r, &in) {
		return
	}
	q := strings.TrimSpace(in.Query)
	// Chuỗi rỗng KHÔNG có nghĩa là "trả tất cả" (§8.2). Trả rỗng, 200.
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, s2sSearchResponse{Items: []s2sSearchItem{}})
		return
	}
	if in.Limit <= 0 || in.Limit > 20 {
		in.Limit = 8
	}

	// Superuser tìm như người thường: role() nâng superuser thành owner của
	// MỌI Space, đúng cho trang quản trị, sai cho ô tìm kiếm chung.
	ctx := auth.WithoutSuperuser(r.Context())
	me := s.me(r)

	// Mỗi nguồn xin ĐỦ `limit`: trộn xen kẽ rồi cắt thì nguồn này hụt, nguồn
	// kia bù được.
	var topics, boards []s2sSearchItem

	if wantsType(in.Types, "topic") {
		// s.svc.Search đã lọc theo searchableSpaces của chính người dùng. Không
		// có nhánh nào ở đây nới rộng phạm vi đó — đây là toàn bộ hợp đồng bảo
		// mật.
		list, err := s.svc.Search(ctx, me, service.SearchInput{Query: q, Limit: in.Limit})
		if err != nil {
			writeError(w, s.log, err)
			return
		}
		for _, t := range list {
			topics = append(topics, s2sSearchItem{
				Type: "topic", ID: t.ID, Title: t.Title,
				Subtitle:  topicSubtitle(t),
				Snippet:   t.Highlight, // ts_headline đã bọc <mark>; lufami lọc trắng lại
				Icon:      "messages",
				URL:       "/ludiskus/s/" + t.SpaceUUID + "/t/" + t.ID,
				SpaceUUID: t.SpaceUUID,
				UpdatedAt: t.UpdatedAt,
			})
		}
	}

	if wantsType(in.Types, "board") {
		// Cùng cổng với ListBoards: requireView trên Space (qua
		// searchableSpaces, dùng chung với Search ở trên).
		list, err := s.svc.SearchBoards(ctx, me, q, in.Limit)
		if err != nil {
			writeError(w, s.log, err)
			return
		}
		for _, b := range list {
			it := s2sSearchItem{
				Type: "board", ID: b.ID, Title: b.Name,
				Subtitle:  boardSubtitle(b),
				Icon:      "layers",
				URL:       "/ludiskus/s/" + b.SpaceUUID + "/b/" + b.ID,
				SpaceUUID: b.SpaceUUID,
				UpdatedAt: b.UpdatedAt,
			}
			if b.DescriptionMD != nil {
				it.Snippet = truncateRunes(*b.DescriptionMD, 160)
			}
			boards = append(boards, it)
		}
	}

	items := interleaveS2S(in.Limit, topics, boards)
	writeJSON(w, http.StatusOK, s2sSearchResponse{
		Items: items, Truncated: len(items) >= in.Limit,
	})
}

// interleaveS2S trộn xen kẽ các nguồn (cùng cách với lutriip): nối đuôi thì
// nguồn hỏi trước luôn chiếm các hạng đầu, và lufami sẽ đọc nhầm thứ tự hỏi
// thành tín hiệu độ liên quan. Cắt về `limit` rồi MỚI đánh rank 1..n — rank
// phải khớp đúng danh sách được gửi đi.
func interleaveS2S(limit int, sources ...[]s2sSearchItem) []s2sSearchItem {
	total, longest := 0, 0
	for _, src := range sources {
		total += len(src)
		longest = max(longest, len(src))
	}
	items := make([]s2sSearchItem, 0, total)
	for i := 0; i < longest; i++ {
		for _, src := range sources {
			if i < len(src) {
				items = append(items, src[i])
			}
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	for i := range items {
		items[i].Rank = i + 1
	}
	return items
}

func boardSubtitle(b domain.Board) string {
	if b.TopicCount > 0 {
		return "Chuyên mục · " + strconv.Itoa(b.TopicCount) + " chủ đề"
	}
	return "Chuyên mục"
}

// truncateRunes cắt theo rune: cắt giữa một ký tự tiếng Việt nhiều byte sinh ra
// UTF-8 hỏng.
func truncateRunes(s string, n int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return strings.TrimRight(string(rs[:n]), " ") + "…"
}

func topicSubtitle(t domain.Topic) string {
	if t.ReplyCount == 1 {
		return "1 trả lời"
	}
	if t.ReplyCount > 1 {
		return strconv.Itoa(t.ReplyCount) + " trả lời"
	}
	return "Chưa có trả lời"
}

// wantsType: types rỗng = mọi loại provider hỗ trợ (§8.2).
func wantsType(types []string, t string) bool {
	if len(types) == 0 {
		return true
	}
	for _, want := range types {
		if want == t {
			return true
		}
	}
	return false
}
