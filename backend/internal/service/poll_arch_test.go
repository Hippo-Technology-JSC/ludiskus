package service

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPollModuleBoundaries(t *testing.T) {
	files, e := filepath.Glob("poll*.go")
	if e != nil {
		t.Fatal(e)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		for _, bad := range []string{"s.repo.GetTopic(", "s.repo.GetPost(", "s.repo.GetComment(", "s.repo.ListBoards(", "s.hipt.Complete("} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s crosses poll boundary: %s", file, bad)
			}
		}
	}
	files, _ = filepath.Glob("../repository/poll*.go")
	joins := regexp.MustCompile(`(?i)\bJOIN\s+(topics|posts|comments)\b`)
	for _, file := range files {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if joins.Match(raw) {
			t.Errorf("%s joins content tables", file)
		}
	}
	raw, e := os.ReadFile("../../db/migrations/0014_poll_core.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	start := strings.Index(string(raw), "CREATE TABLE poll_ballots")
	end := strings.Index(string(raw)[start:], ");")
	body := string(raw)[start : start+end]
	if strings.Contains(body, "profile") || strings.Contains(body, "created_at") || strings.Contains(body, "updated_at") {
		t.Fatal("secret ballots carry identity/timestamp")
	}
}
