package domain

import "testing"

func TestResourceRefValidate(t *testing.T) {
	cases := []struct {
		ref ResourceRef
		ok  bool
	}{{ResourceRef{"lumuse", "movie", "01JZ.a-1"}, true}, {ResourceRef{"LUMUSE", "movie", "1"}, false}, {ResourceRef{"a", "movie", "1"}, false}, {ResourceRef{"lumuse", "Movie", "1"}, false}, {ResourceRef{"lumuse", "movie", "a/b"}, false}, {ResourceRef{"lumuse", "movie", ""}, false}}
	for _, tc := range cases {
		if got := tc.ref.Validate() == nil; got != tc.ok {
			t.Errorf("%+v valid=%v want %v", tc.ref, got, tc.ok)
		}
	}
}
func TestCountDelta(t *testing.T) {
	statuses := []string{"", CommentPublished, CommentPending, CommentHidden, CommentDeleted, CommentRejected}
	for _, old := range statuses {
		for _, next := range statuses {
			for _, root := range []bool{true, false} {
				c, r, p := CountDelta(old, next, root)
				wantC, wantR, wantP := 0, 0, 0
				if old == CommentPublished {
					if root {
						wantC--
					} else {
						wantR--
					}
				}
				if next == CommentPublished {
					if root {
						wantC++
					} else {
						wantR++
					}
				}
				if old == CommentPending {
					wantP--
				}
				if next == CommentPending {
					wantP++
				}
				if c != wantC || r != wantR || p != wantP {
					t.Fatalf("%s -> %s root=%v got %d/%d/%d", old, next, root, c, r, p)
				}
			}
		}
	}
}

func TestSanitizeCanonicalPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/luprojet/15b0d2e5-9d9a-4335-ad97-bf4d78896c25", "/luprojet/15b0d2e5-9d9a-4335-ad97-bf4d78896c25"},
		{"/luprojet/15b0d2e5-9d9a-4335-ad97-bf4d78896c25?tab=reports&date=2026-09-09", "/luprojet/15b0d2e5-9d9a-4335-ad97-bf4d78896c25"},
		{"/luprojet/15b0d2e5-9d9a-4335-ad97-bf4d78896c25#reports", "/luprojet/15b0d2e5-9d9a-4335-ad97-bf4d78896c25"},
		{"//invalid/double/slash", ""},
		{"../relative/path", ""},
		{"https://evil.example/x", ""},
		{"invalid_without_slash", ""},
	}
	for _, tc := range cases {
		got := SanitizeCanonicalPath(tc.input)
		if got != tc.want {
			t.Errorf("SanitizeCanonicalPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
