package service

import (
	"testing"

	"ludiskus/internal/domain"
)

func TestInteractionStateAndResourceType(t *testing.T) {
	cases := map[string]string{
		domain.StatusPublished: "active",
		domain.StatusLocked:    "active",
		domain.StatusDeleted:   "gone",
		domain.StatusPending:   "blocked",
		domain.StatusHidden:    "blocked",
	}
	for status, want := range cases {
		if got := interactionState(status); got != want {
			t.Fatalf("interactionState(%q)=%q, want %q", status, got, want)
		}
	}
	if got := interactionResourceType(&domain.Post{IsFirst: true}); got != "post" {
		t.Fatalf("first post type=%q", got)
	}
	if got := interactionResourceType(&domain.Post{}); got != "reply" {
		t.Fatalf("reply type=%q", got)
	}
}
