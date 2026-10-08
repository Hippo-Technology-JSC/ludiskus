package service

import (
	"encoding/json"
	"ludiskus/internal/domain"
	"math"
	"reflect"
	"testing"
	"time"
)

func rankedFixture(method string, positions ...string) *domain.Poll {
	p := &domain.Poll{Kind: "ranked", Config: json.RawMessage(`{"method":"` + method + `","max_ranks":4}`)}
	for i, id := range positions {
		p.Options = append(p.Options, domain.PollOption{ID: id, Label: id, Position: i, Status: "active"})
	}
	return p
}
func ballot(ids ...string) []domain.PollChoice {
	b := []domain.PollChoice{}
	for i, id := range ids {
		b = append(b, domain.PollChoice{OptionID: id, Rank: i + 1})
	}
	return b
}
func TestRankedHandExamples(t *testing.T) {
	cases := []struct {
		name, method string
		opts         []string
		votes        [][]domain.PollChoice
		winner       string
		tie          []string
	}{
		{"majority", "irv", []string{"a", "b", "c"}, [][]domain.PollChoice{ballot("a"), ballot("a"), ballot("b")}, "a", nil},
		{"transfer", "irv", []string{"a", "b", "c"}, [][]domain.PollChoice{ballot("a", "b"), ballot("a", "b"), ballot("b"), ballot("b"), ballot("c", "b")}, "b", nil},
		{"two tie", "irv", []string{"a", "b"}, [][]domain.PollChoice{ballot("a"), ballot("b")}, "", []string{"a", "b"}},
		{"zero options", "irv", []string{"a", "b", "c", "d"}, [][]domain.PollChoice{ballot("a"), ballot("b"), ballot("b")}, "b", nil},
		{"position break", "irv", []string{"a", "b", "c"}, [][]domain.PollChoice{ballot("a", "b"), ballot("b", "a"), ballot("c", "b")}, "b", nil},
		{"exhausted", "irv", []string{"a", "b", "c"}, [][]domain.PollChoice{ballot("a"), ballot("a"), ballot("b"), ballot("b"), ballot("c")}, "", []string{"a", "b"}},
		{"empty", "irv", []string{"a", "b", "c"}, nil, "", []string{"a", "b", "c"}},
		{"truncated borda", "borda", []string{"a", "b", "c"}, [][]domain.PollChoice{ballot("a", "b"), ballot("b"), ballot("b")}, "b", nil},
		{"borda tied", "borda", []string{"a", "b"}, [][]domain.PollChoice{ballot("a", "b"), ballot("b", "a")}, "", []string{"a", "b"}},
		{"borda first place", "borda", []string{"a", "b", "c"}, [][]domain.PollChoice{ballot("a", "b"), ballot("b", "a"), ballot("c", "a"), ballot("b")}, "b", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := rankedFixture(c.method, c.opts...)
			r := tallyPoll(p, c.votes)
			if c.winner != "" {
				if r.Winner == nil || *r.Winner != c.winner {
					t.Fatalf("%+v", r)
				}
			} else if !reflect.DeepEqual(r.Tie, c.tie) {
				t.Fatalf("tie=%v want=%v", r.Tie, c.tie)
			}
		})
	}
	// Every permutation of input ballots must leave the result unchanged.
	for i := 0; i < 30; i++ {
		p := rankedFixture("irv", "a", "b", "c", "d")
		b := [][]domain.PollChoice{ballot("a", "b"), ballot("b", "a"), ballot("c", "b"), ballot("d", "a"), ballot("a"), ballot("b", "c")}
		want := tallyPoll(p, b)
		for j := range b {
			k := (j + i) % len(b)
			b[j], b[k] = b[k], b[j]
		}
		if got := tallyPoll(p, b); !reflect.DeepEqual(got, want) {
			t.Fatalf("permutation %d changed tally", i)
		}
	}
}
func TestHamiltonTotals(t *testing.T) {
	for total := 1; total <= 200; total++ {
		for a := 0; a <= total; a++ {
			shares := hamilton([]int{a, (total - a) / 2, total - a - (total-a)/2}, total)
			sum := 0.
			for _, v := range shares {
				sum += v
			}
			if math.Abs(sum-100) > 1e-8 {
				t.Fatalf("%v totals %f", shares, sum)
			}
		}
	}
}
func TestScaleAndSchedule(t *testing.T) {
	one, two, three := 1, 2, 3
	p := &domain.Poll{Kind: "scale", Options: []domain.PollOption{{ID: "a", Value: &one, Status: "active"}, {ID: "b", Value: &two, Status: "active"}, {ID: "c", Value: &three, Status: "active"}}}
	r := tallyPoll(p, [][]domain.PollChoice{{{OptionID: "a"}}, {{OptionID: "c"}}, {{OptionID: "c"}}})
	if r.Mean == nil || *r.Mean != 2.33 || r.Median == nil || *r.Median != 3 {
		t.Fatalf("%+v", r)
	}
	now := time.Now()
	later := now.Add(time.Hour)
	p.Kind = "schedule"
	p.Options = []domain.PollOption{{ID: "a", Status: "active", StartsAt: &later}, {ID: "b", Status: "active", StartsAt: &now}}
	r = tallyPoll(p, [][]domain.PollChoice{{{OptionID: "a", Answer: "yes"}, {OptionID: "b", Answer: "yes"}}, {{OptionID: "a", Answer: "maybe"}, {OptionID: "b", Answer: "maybe"}}})
	if r.Winner == nil || *r.Winner != "b" {
		t.Fatalf("schedule %+v", r)
	}
	p.Kind = "ranked"
	p.Config = json.RawMessage(`{"method":"irv","max_ranks":2}`)
	p.Options[0].Status = "hidden"
	r = tallyPoll(p, [][]domain.PollChoice{ballot("a", "b")})
	if r.Winner == nil || *r.Winner != "b" {
		t.Fatalf("hidden candidate not filtered: %+v", r)
	}
}
func TestLockedIdentityAndResults(t *testing.T) {
	now := time.Now()
	for _, from := range []string{"public", "owner_only", "anonymous", "secret"} {
		for _, to := range []string{"public", "owner_only", "anonymous", "secret"} {
			p := &domain.Poll{FirstVoteAt: &now, IdentityMode: from}
			err := applyPollPatch(p, PollPatch{IdentityMode: &to})
			levels := map[string]int{"public": 0, "owner_only": 1, "anonymous": 2}
			expected := from == to || from != "secret" && to != "secret" && levels[to] >= levels[from]
			if (err == nil) != expected {
				t.Fatalf("%s -> %s: %v", from, to, err)
			}
		}
	}
	for _, mode := range []string{"always", "after_vote", "after_close", "owner_only"} {
		for _, closed := range []bool{false, true} {
			for _, creator := range []bool{false, true} {
				for _, voted := range []bool{false, true} {
					p := &domain.Poll{Status: "published", ResultsVisibility: mode}
					if closed {
						p.Status = "closed"
					}
					yes, _ := resultsVisible(p, creator, false, voted, now)
					want := creator || mode == "always" || mode == "after_vote" && (voted || closed) || mode == "after_close" && closed
					if yes != want {
						t.Fatalf("%s closed=%t creator=%t voted=%t", mode, closed, creator, voted)
					}
				}
			}
		}
	}
	p := &domain.Poll{Status: "published", IdentityMode: "secret", ResultsVisibility: "always", MinVotersForResults: 3, VoterCount: 2}
	if yes, reason := resultsVisible(p, true, true, true, now); yes || reason != "min_voters" {
		t.Fatal("creator bypassed privacy threshold")
	}
}
