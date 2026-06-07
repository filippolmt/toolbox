package configedit

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"kitten", "sitting", 3},
		{"infar", "infra", 2}, // transposition costs 2 in plain Levenshtein
		{"mont_root", "mounts_root", 2},
		{"héllo", "hello", 1}, // rune-wise, not byte-wise
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestClosest(t *testing.T) {
	candidates := []string{"infra", "qa", "scratch"}

	if got := closest("infar", candidates); got != "infra" {
		t.Errorf("closest(infar) = %q, want infra", got)
	}
	if got := closest("completely-different", candidates); got != "" {
		t.Errorf("Closest beyond maxDist must return \"\", got %q", got)
	}
	if got := closest("x", nil); got != "" {
		t.Errorf("Closest with no candidates must return \"\", got %q", got)
	}
	// Tie keeps first in slice order.
	if got := closest("ab", []string{"aa", "bb"}); got != "aa" {
		t.Errorf("Closest tie must keep first candidate, got %q", got)
	}
}

func TestDidYouMean(t *testing.T) {
	if got := DidYouMean("scrach", []string{"scratch"}); got != `; did you mean "scratch"?` {
		t.Errorf("DidYouMean = %q", got)
	}
	if got := DidYouMean("nothing-close", []string{"scratch"}); got != "" {
		t.Errorf("DidYouMean with no close match must return \"\", got %q", got)
	}
}
