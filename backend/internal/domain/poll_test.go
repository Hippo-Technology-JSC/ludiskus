package domain

import (
	"testing"
	"time"
)

func TestPollEffectiveState(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, status := range []string{"draft", "pending", "published", "closed", "hidden", "deleted", "rejected"} {
		for _, when := range []*time.Time{nil, &past, &future} {
			p := Poll{Status: status, ClosesAt: when}
			got := p.EffectiveState(now)
			want := status
			if status == "rejected" {
				want = "hidden"
			}
			if status == "published" {
				want = "open"
				if when == &past {
					want = "closed"
				}
			}
			if got != want {
				t.Fatalf("%s %v got %s want %s", status, when, got, want)
			}
		}
	}
	for _, opens := range []*time.Time{nil, &past, &future} {
		p := Poll{Status: "published", OpensAt: opens}
		want := "open"
		if opens == &future {
			want = "scheduled"
		}
		if got := p.EffectiveState(now); got != want {
			t.Fatal(got, want)
		}
	}
	for _, state := range []string{"gone", "blocked", "active", "unverified"} {
		p := Poll{Status: "published", Target: &CommentTarget{State: state}}
		want := "open"
		if state == "gone" || state == "blocked" {
			want = "hidden"
		}
		if p.EffectiveState(now) != want {
			t.Fatal(state)
		}
	}
}
