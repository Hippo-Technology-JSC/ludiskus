package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"ludiskus/internal/domain"
	"strconv"
	"strings"
	"time"
)

func pollCSVCell(s string) string {
	if s != "" && strings.ContainsAny(s[:1], "=+-@\t\r") {
		return "'" + s
	}
	return s
}
func (s *Service) ExportPoll(ctx context.Context, id string, v pollViewer, kind string) ([]byte, error) {
	p, _, e := s.ensurePollReadable(ctx, id, v)
	if e != nil {
		return nil, e
	}
	creator, _, _, svc := s.pollRoles(ctx, p, v)
	if !creator && !svc {
		return nil, domain.ErrForbidden
	}
	if kind == "" {
		kind = "results"
	}
	var b bytes.Buffer
	b.WriteString("\xef\xbb\xbf")
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"Xuất từ LuPoll lúc " + time.Now().Format(time.RFC3339) + " — không có giá trị pháp lý"})
	write := func(row []string) {
		for i, v := range row {
			row[i] = pollCSVCell(v)
		}
		_ = w.Write(row)
	}
	switch kind {
	case "results":
		visible, _ := resultsVisible(p, creator, svc, s.hasPollReceipt(ctx, id, v.Profile), time.Now())
		if !visible {
			return nil, domain.ErrForbidden
		}
		r, e := s.pollResult(ctx, p)
		if e != nil {
			return nil, e
		}
		if r == nil {
			return nil, domain.ErrForbidden
		}
		var result pollTally
		if e = json.Unmarshal(r.Result, &result); e != nil {
			return nil, e
		}
		write([]string{"Lựa chọn", "Số phiếu", "Có thể", "Phần trăm", "Điểm"})
		for _, o := range result.Options {
			write([]string{o.Label, pollInt(o.Count), pollInt(o.Maybe), pollNumber(o.Percent), pollInt(o.Score)})
		}
		for i, round := range result.Rounds {
			write([]string{"Vòng " + pollInt(i+1), "Hết hiệu lực", pollInt(round.Exhausted)})
			for _, o := range p.Options {
				if n, ok := round.Counts[o.ID]; ok {
					write([]string{o.Label, pollInt(n)})
				}
			}
			if round.TieBreak != nil {
				write([]string{"Phá hòa", *round.TieBreak})
			}
		}
		if r.QuorumMet != nil {
			label := "Không đủ túc số"
			if *r.QuorumMet {
				label = "Đủ túc số"
			}
			write([]string{label})
		}
	case "votes":
		if p.IdentityMode == "anonymous" || p.IdentityMode == "secret" {
			return nil, domain.ErrForbidden
		}
		rows, e := s.repo.PollExportVotes(ctx, id)
		if e != nil {
			return nil, e
		}
		write([]string{"Tên", "Mã", "Lựa chọn", "Hạng", "Trả lời"})
		for _, r := range rows {
			write([]string{r["name"], r["code"], r["label"], r["rank"], r["answer"]})
		}
	default:
		return nil, domain.ErrValidation
	}
	w.Flush()
	if e = w.Error(); e != nil {
		return nil, e
	}
	tx, e := s.repo.BeginPoll(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = s.pollAudit(ctx, tx, p, v, "export", map[string]string{"kind": kind}); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}

func pollNumber(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
