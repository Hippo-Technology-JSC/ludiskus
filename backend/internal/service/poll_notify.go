package service

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"ludiskus/internal/domain"
	"ludiskus/internal/notify"
	"sort"
)

func (s *Service) pollNotifyTx(ctx context.Context, tx pgx.Tx, p *domain.Poll, event, key string, profiles []string) error {
	if p.Target != nil && p.Target.State == "unverified" {
		return nil
	}
	sort.Strings(profiles)
	profiles = compactPollProfiles(profiles)
	data := map[string]any{"pollId": p.ID, "question": string([]rune(p.Question)[:min(120, len([]rune(p.Question)))]), "actionUrl": p.Path(), "anchorTitle": ""}
	if p.Target != nil {
		data["anchorTitle"] = p.Target.Title
	}
	raw, _ := json.Marshal(data)
	for start := 0; start < len(profiles); start += 500 {
		recipients := []notify.Recipient{}
		for _, profile := range profiles[start:min(start+500, len(profiles))] {
			recipients = append(recipients, notify.Recipient{ProfileUUID: profile})
		}
		k := key + ":" + pollInt(start/500)
		payload, e := json.Marshal(notify.Event{EventType: "ludiskus.poll." + event, IdempotencyKey: &k, Data: raw, Recipients: recipients})
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO outbox(event_type,idempotency_key,payload,max_attempts) VALUES($1,$2,$3,$4) ON CONFLICT(idempotency_key) DO NOTHING`, "ludiskus.poll."+event, k, payload, s.cfg.OutboxMaxAttempts); e != nil {
			return e
		}
	}
	return nil
}
func compactPollProfiles(in []string) []string {
	out := []string{}
	prev := ""
	for _, v := range in {
		if v != "" && v != prev {
			out = append(out, v)
		}
		prev = v
	}
	return out
}
func (s *Service) finalizePollTx(ctx context.Context, tx pgx.Tx, p *domain.Poll) error {
	stored, e := s.repo.PollResultTx(ctx, tx, p.ID)
	if e != nil {
		return e
	}
	if stored == nil || !stored.Final {
		var ballots [][]domain.PollChoice
		if p.Kind == "ranked" {
			ballots, e = s.repo.PollBallotsTx(ctx, tx, p)
		}
		if e != nil {
			return e
		}
		r := makePollResult(p, ballots, true)
		if e = s.repo.SavePollResultTx(ctx, tx, p.ID, r); e != nil {
			return e
		}
	}
	profiles := []string{}
	if p.CreatedBy != nil {
		profiles = append(profiles, *p.CreatedBy)
	}
	if p.ResultsVisibility != "owner_only" {
		rows, e := tx.Query(ctx, `SELECT profile_uuid::text FROM poll_voters WHERE poll_id=$1 AND notify_result`, p.ID)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			profiles = append(profiles, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	return s.pollNotifyTx(ctx, tx, p, "closed", "poll:closed:"+p.ID+":r"+pollInt(p.ReopenCount), profiles)
}
