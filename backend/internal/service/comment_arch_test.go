package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ludiskus/internal/domain"
)

func TestCommentModuleBoundaries(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if strings.Contains(name, "comment") {
			for _, forbidden := range []string{"s.repo.GetTopic(", "s.repo.GetPost(", "s.repo.ListBoards(", "s.repo.CreateReply("} {
				if strings.Contains(src, forbidden) {
					t.Errorf("%s crosses forum boundary with %s", name, forbidden)
				}
			}
		} else if (name == "content.go" || name == "spaces.go" || name == "search.go" || name == "moderation.go" || name == "service.go") && strings.Contains(src, "domain.Comment") {
			t.Errorf("forum file %s references comment domain", name)
		}
	}
	resolver, err := os.ReadFile(filepath.Join("..", "resolver", "resolver.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(resolver), `"ludiskus/internal/service"`) {
		t.Error("resolver imports service")
	}
}

func TestTargetCapabilitiesOnlyRestrict(t *testing.T) {
	p := domainDefaultForTest()
	raw := []byte(`{"comment":true,"attach":true,"maxDepth":5,"maxLength":9000,"publicRead":true}`)
	p.Enabled = false
	p.Attachments.Enabled = false
	p.MaxDepth = 2
	p.MaxLength = 4000
	p.PublicRead = false
	got := restrictPolicy(p, raw)
	if got.Enabled || got.Attachments.Enabled || got.MaxDepth != 2 || got.MaxLength != 4000 || got.PublicRead {
		t.Fatalf("target widened policy: %+v", got)
	}
}
func domainDefaultForTest() domain.CommentPolicy { return domain.DefaultCommentPolicy() }
