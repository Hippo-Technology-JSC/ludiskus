package service

import (
	"ludiskus/internal/domain"
	"math"
	"sort"
)

// Ordinary ballots use the counters committed in the same transaction as the
// receipt. Reads remain O(options), regardless of the number of participants.
func tallyPollCounts(p *domain.Poll) pollTally {
	r := pollTally{Method: p.Kind, Options: []tallyOption{}, Winners: []string{}, Tie: []string{}}
	options := []domain.PollOption{}
	for _, o := range p.Options {
		if o.Status == "active" {
			options = append(options, o)
		}
	}
	sort.SliceStable(options, func(i, j int) bool { return options[i].Position < options[j].Position })
	counts := make([]int, len(options))
	for i, o := range options {
		counts[i] = o.VoteCount
	}
	shares := hamilton(counts, p.VoterCount)
	best := -1
	weighted, total := 0, 0
	values := []domain.PollOption{}
	for i, o := range options {
		percent := shares[i]
		if p.Kind == "multiple" || p.Kind == "schedule" {
			percent = 0
			if p.VoterCount > 0 {
				percent = math.Round(float64(o.VoteCount)*1000/float64(p.VoterCount)) / 10
			}
		}
		r.Options = append(r.Options, tallyOption{ID: o.ID, Label: o.Label, Count: o.VoteCount, Maybe: o.MaybeCount, Percent: percent, Score: o.VoteCount})
		if o.VoteCount > best {
			best = o.VoteCount
			r.Winners = []string{o.ID}
		} else if o.VoteCount == best {
			r.Winners = append(r.Winners, o.ID)
		}
		if o.Value != nil {
			weighted += *o.Value * o.VoteCount
			total += o.VoteCount
			values = append(values, o)
		}
	}
	if p.VoterCount == 0 {
		r.Winners = []string{}
	}
	if p.Kind == "scale" && total > 0 {
		mean := math.Round(float64(weighted)*100/float64(total)) / 100
		r.Mean = &mean
		sort.Slice(values, func(i, j int) bool { return *values[i].Value < *values[j].Value })
		at := func(n int) int {
			c := 0
			for _, o := range values {
				c += o.VoteCount
				if c > n {
					return *o.Value
				}
			}
			return 0
		}
		median := float64(at(total / 2))
		if total%2 == 0 {
			median = float64(at(total/2-1)+at(total/2)) / 2
		}
		r.Median = &median
	}
	if p.Kind == "schedule" {
		byID := map[string]domain.PollOption{}
		for _, o := range options {
			byID[o.ID] = o
		}
		sort.SliceStable(r.Options, func(i, j int) bool {
			a, b := r.Options[i], r.Options[j]
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			if a.Maybe != b.Maybe {
				return a.Maybe > b.Maybe
			}
			ta, tb := byID[a.ID].StartsAt, byID[b.ID].StartsAt
			return ta != nil && tb != nil && ta.Before(*tb)
		})
		r.Winners = []string{}
		if p.VoterCount > 0 && len(r.Options) > 0 {
			r.Winners = []string{r.Options[0].ID}
		}
	}
	if len(r.Winners) == 1 {
		r.Winner = &r.Winners[0]
	} else if len(r.Winners) > 1 {
		r.Tie = append(r.Tie, r.Winners...)
	}
	return r
}
