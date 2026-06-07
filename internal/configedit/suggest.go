// Package configedit is the semantic layer between cmd/* config-editing
// subcommands and the low-level configio writer: --where target resolution,
// header-on-create upserts, shells/mounts section writers, per-key
// provenance, doctor checks, and "did you mean" suggestions. Cobra-free so
// every behaviour is unit-testable without command plumbing.
package configedit

import "fmt"

// suggestMaxDist is the Levenshtein cut-off for closest — mirrors cobra's
// unexported suggestion threshold so `toolbox shells get infar` feels like
// cobra's own unknown-command suggestions.
const suggestMaxDist = 2

// levenshtein returns the edit distance between a and b (two-row DP).
// go.mod carries no string-distance dependency and cobra's ld() is
// unexported, so the ~30 lines live here.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// closest returns the candidate nearest to input within suggestMaxDist, or
// "" when nothing is close enough. Ties keep the first candidate in slice
// order so callers with sorted candidates get deterministic suggestions.
func closest(input string, candidates []string) string {
	best, bestDist := "", suggestMaxDist+1
	for _, c := range candidates {
		if d := levenshtein(input, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

// DidYouMean renders the conventional suggestion suffix for an unknown-name
// error, or "" when no candidate is close enough. Callers append it to
// their own error message.
func DidYouMean(input string, candidates []string) string {
	if c := closest(input, candidates); c != "" {
		return fmt.Sprintf("; did you mean %q?", c)
	}
	return ""
}
