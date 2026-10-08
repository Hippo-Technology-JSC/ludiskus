package service

import (
	"ludiskus/internal/domain"
	"math"
	"sort"
)

type tallyOption struct {
	ID      string  `json:"optionId"`
	Label   string  `json:"label"`
	Count   int     `json:"count"`
	Maybe   int     `json:"maybe,omitempty"`
	Percent float64 `json:"percent"`
	Score   int     `json:"score,omitempty"`
}
type rankedRound struct {
	Counts     map[string]int `json:"counts"`
	Exhausted  int            `json:"exhausted"`
	Eliminated []string       `json:"eliminated"`
	TieBreak   *string        `json:"tieBreak"`
}
type pollTally struct {
	Method  string        `json:"method"`
	Options []tallyOption `json:"options"`
	Winner  *string       `json:"winner"`
	Winners []string      `json:"winners"`
	Tie     []string      `json:"tie"`
	Rounds  []rankedRound `json:"rounds,omitempty"`
	Mean    *float64      `json:"mean,omitempty"`
	Median  *float64      `json:"median,omitempty"`
}

// Hamilton rounds shares to tenths without changing their total. Stable option
// positions resolve equal remainders; multiple polls deliberately do not normalize.
func hamilton(counts []int, total int) []float64 {
	out := make([]float64, len(counts))
	if total <= 0 {
		return out
	}
	type remainder struct {
		i int
		r float64
	}
	rs := make([]remainder, len(counts))
	sum, used := 0, 0
	for i, n := range counts {
		x := float64(n) * 1000 / float64(total)
		v := int(math.Floor(x))
		out[i] = float64(v) / 10
		used += v
		sum += n
		rs[i] = remainder{i, x - float64(v)}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].r > rs[j].r })
	target := int(math.Round(float64(sum) * 1000 / float64(total)))
	for i := 0; i < target-used && i < len(rs); i++ {
		out[rs[i].i] += .1
	}
	return out
}
func tallyPoll(p *domain.Poll, ballots [][]domain.PollChoice) pollTally {
	result := pollTally{Method: p.Kind, Options: []tallyOption{}, Winners: []string{}, Tie: []string{}}
	active := map[string]domain.PollOption{}
	ordered := []domain.PollOption{}
	for _, o := range p.Options {
		if o.Status == "active" {
			active[o.ID] = o
			ordered = append(ordered, o)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	filtered := make([][]domain.PollChoice, len(ballots))
	for i, b := range ballots {
		for _, v := range b {
			if _, ok := active[v.OptionID]; ok {
				filtered[i] = append(filtered[i], v)
			}
		}
		sort.SliceStable(filtered[i], func(a, b int) bool { return filtered[i][a].Rank < filtered[i][b].Rank })
	}
	counts, maybe, points, totalRanks := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	values := []int{}
	c := p.Rules()
	for _, b := range filtered {
		for i, v := range b {
			totalRanks[v.OptionID]++
			if p.Kind == "ranked" {
				if i == 0 {
					counts[v.OptionID]++
				}
				points[v.OptionID] += max(0, c.MaxRanks-i)
			} else if v.Answer == "maybe" {
				maybe[v.OptionID]++
			} else {
				counts[v.OptionID]++
				if p.Kind == "scale" && active[v.OptionID].Value != nil {
					values = append(values, *active[v.OptionID].Value)
				}
			}
		}
	}
	numbers := make([]int, len(ordered))
	for i, o := range ordered {
		numbers[i] = counts[o.ID]
	}
	shares := hamilton(numbers, len(ballots))
	best := -1
	for i, o := range ordered {
		percent := shares[i]
		if p.Kind == "multiple" || p.Kind == "schedule" || p.Kind == "ranked" {
			percent = 0
			if len(ballots) > 0 {
				percent = math.Round(float64(counts[o.ID])*1000/float64(len(ballots))) / 10
			}
		}
		score := counts[o.ID]
		if p.Kind == "schedule" {
			score = counts[o.ID]*2 + maybe[o.ID]
		} else if p.Kind == "ranked" && c.Method == "borda" {
			score = points[o.ID]
		}
		result.Options = append(result.Options, tallyOption{o.ID, o.Label, counts[o.ID], maybe[o.ID], percent, score})
		if score > best {
			best = score
			result.Winners = []string{o.ID}
		} else if score == best {
			result.Winners = append(result.Winners, o.ID)
		}
	}
	if len(ballots) == 0 {
		result.Winners = []string{}
	}
	if p.Kind == "scale" && len(values) > 0 {
		sort.Ints(values)
		sum := 0
		for _, v := range values {
			sum += v
		}
		mean := math.Round(float64(sum)*100/float64(len(values))) / 100
		median := float64(values[len(values)/2])
		if len(values)%2 == 0 {
			median = float64(values[len(values)/2-1]+values[len(values)/2]) / 2
		}
		result.Mean = &mean
		result.Median = &median
	}
	if p.Kind == "schedule" {
		sort.SliceStable(result.Options, func(i, j int) bool {
			a, b := result.Options[i], result.Options[j]
			if a.Count != b.Count {
				return a.Count > b.Count
			}
			if a.Maybe != b.Maybe {
				return a.Maybe > b.Maybe
			}
			ta, tb := active[a.ID].StartsAt, active[b.ID].StartsAt
			return ta != nil && tb != nil && ta.Before(*tb)
		})
		if len(result.Options) > 0 && len(ballots) > 0 {
			result.Winners = []string{result.Options[0].ID}
		}
	}
	if p.Kind == "ranked" {
		result.Method = c.Method
		if c.Method == "irv" {
			tallyIRV(&result, ordered, filtered, totalRanks)
		} else {
			bestFirst := -1
			wins := []string{}
			for _, id := range result.Winners {
				if counts[id] > bestFirst {
					bestFirst = counts[id]
					wins = []string{id}
				} else if counts[id] == bestFirst {
					wins = append(wins, id)
				}
			}
			result.Winners = wins
			sort.SliceStable(result.Options, func(i, j int) bool {
				a, b := result.Options[i], result.Options[j]
				if a.Score != b.Score {
					return a.Score > b.Score
				}
				return a.Count > b.Count
			})
		}
	}
	if len(result.Winners) == 1 {
		id := result.Winners[0]
		result.Winner = &id
	} else if len(result.Winners) > 1 {
		result.Tie = append([]string{}, result.Winners...)
	}
	return result
}
func tallyIRV(result *pollTally, options []domain.PollOption, ballots [][]domain.PollChoice, totalRanks map[string]int) {
	remaining := map[string]bool{}
	position := map[string]int{}
	for _, o := range options {
		remaining[o.ID] = true
		position[o.ID] = o.Position
	}
	result.Winners = []string{}
	history := []map[string]int{}
	for len(remaining) > 0 {
		round := rankedRound{Counts: map[string]int{}, Eliminated: []string{}}
		ids := []string{}
		for _, o := range options {
			if remaining[o.ID] {
				ids = append(ids, o.ID)
				round.Counts[o.ID] = 0
			}
		}
		for _, b := range ballots {
			found := false
			for _, v := range b {
				if remaining[v.OptionID] {
					round.Counts[v.OptionID]++
					found = true
					break
				}
			}
			if !found {
				round.Exhausted++
			}
		}
		total := len(ballots) - round.Exhausted
		for _, id := range ids {
			if round.Counts[id]*2 > total {
				result.Winners = []string{id}
			}
		}
		if len(result.Winners) > 0 {
			result.Rounds = append(result.Rounds, round)
			return
		}
		if total == 0 || len(ids) == 2 && round.Counts[ids[0]] == round.Counts[ids[1]] {
			result.Winners = ids
			result.Rounds = append(result.Rounds, round)
			return
		}
		if len(ids) == 1 {
			result.Winners = ids
			result.Rounds = append(result.Rounds, round)
			return
		}
		if len(history) == 0 {
			for _, id := range ids {
				if round.Counts[id] == 0 {
					round.Eliminated = append(round.Eliminated, id)
				}
			}
		}
		if len(round.Eliminated) == 0 {
			low := int(^uint(0) >> 1)
			candidates := []string{}
			for _, id := range ids {
				n := round.Counts[id]
				if n < low {
					low = n
					candidates = []string{id}
				} else if n == low {
					candidates = append(candidates, id)
				}
			}
			reason := ""
			for h := len(history) - 1; h >= 0 && len(candidates) > 1; h-- {
				candidates = narrowIRV(candidates, history[h], false)
				if len(candidates) == 1 {
					reason = "previous_round"
				}
			}
			if len(candidates) > 1 {
				candidates = narrowIRV(candidates, totalRanks, false)
				if len(candidates) == 1 {
					reason = "total_rankings"
				}
			}
			if len(candidates) > 1 {
				candidates = narrowIRV(candidates, position, true)
				reason = "position"
			}
			round.Eliminated = []string{candidates[0]}
			if reason != "" {
				round.TieBreak = &reason
			}
		}
		for _, id := range round.Eliminated {
			delete(remaining, id)
		}
		history = append(history, round.Counts)
		result.Rounds = append(result.Rounds, round)
	}
}
func narrowIRV(ids []string, counts map[string]int, largest bool) []string {
	out := []string{}
	best := counts[ids[0]]
	for _, id := range ids {
		v := counts[id]
		better := v < best
		if largest {
			better = v > best
		}
		if better {
			best = v
			out = []string{id}
		} else if v == best {
			out = append(out, id)
		}
	}
	return out
}

// RecountPoll is an offline aggregate for the read-only polltally command.
func RecountPoll(p *domain.Poll, ballots [][]domain.PollChoice) any { return tallyPoll(p, ballots) }
