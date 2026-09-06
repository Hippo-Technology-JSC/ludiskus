package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

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
	if !wantsType(in.Types, "topic") {
		writeJSON(w, http.StatusOK, s2sSearchResponse{Items: []s2sSearchItem{}})
		return
	}

	// s.svc.Search đã lọc theo viewableSpaces của chính người dùng. Không có
	// nhánh nào ở đây nới rộng phạm vi đó — đây là toàn bộ hợp đồng bảo mật.
	topics, err := s.svc.Search(r.Context(), s.me(r), service.SearchInput{
		Query: q, Limit: in.Limit,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}

	items := make([]s2sSearchItem, 0, len(topics))
	for i, t := range topics {
		items = append(items, s2sSearchItem{
			Type: "topic", ID: t.ID, Title: t.Title,
			Subtitle:  topicSubtitle(t),
			Snippet:   t.Highlight, // ts_headline đã bọc <mark>; lufami lọc trắng lại
			Icon:      "messages",
			URL:       "/ludiskus/s/" + t.SpaceUUID + "/t/" + t.ID,
			SpaceUUID: t.SpaceUUID,
			UpdatedAt: t.UpdatedAt,
			Rank:      i + 1,
		})
	}
	writeJSON(w, http.StatusOK, s2sSearchResponse{
		Items: items, Truncated: len(items) >= in.Limit,
	})
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
