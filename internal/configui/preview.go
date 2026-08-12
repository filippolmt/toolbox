package configui

import (
	"strings"

	"github.com/filippolmt/toolbox/internal/configedit"
	"github.com/filippolmt/toolbox/internal/configio"
)

// The preview's pure half: what the pending edit would do to the real document.
//
// It renders the edit's own Mutator — the value the save uses — against the
// document as it stood when the editor opened, and reports the lines that
// differ. Deriving the panel from the real mutation is what makes it truthful
// about the writers that *read* the document before deciding (a mounts disable
// patch merged into a richer user entry, a shells rename that keeps its env
// block): a hand-built fragment cannot know any of that, because the result
// depends on what is already in the file.

// previewLine is one line of a rendered preview diff. Removed lines carry the
// document's own content, so Text is kept separate from the marker to let the
// view style the two sides differently.
type previewLine struct {
	Added bool
	Text  string
}

// previewDiff renders mut against base and returns the lines that differ. An
// empty result means the mutation would change nothing — the caller says so
// rather than showing an unchanged fragment, which would imply a pending change
// that does not exist. name only labels parse/encode errors.
func previewDiff(name string, base []byte, mut configedit.Mutator) ([]previewLine, error) {
	// The "before" side is base re-rendered by the same encoder rather than base
	// itself, so the diff shows the mutation's effect and not the difference
	// between the file's formatting and the encoder's.
	before, err := configio.RenderDocument(name, base, nil)
	if err != nil {
		return nil, err
	}
	after, err := previewAfter(name, base, mut)
	if err != nil {
		return nil, err
	}
	return diffLines(documentLines(before), documentLines(after)), nil
}

// previewAfter returns the bytes the target file would hold once mut is applied
// to base — byte-for-byte what the writer would put there.
func previewAfter(name string, base []byte, mut configedit.Mutator) ([]byte, error) {
	return configio.RenderDocument(name, base, mut)
}

// documentLines splits rendered document bytes into lines, dropping the
// trailing newline so an empty document yields no lines at all.
func documentLines(doc []byte) []string {
	s := strings.TrimSuffix(string(doc), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// diffLines reports the band of lines in which before and after differ, with
// their common prefix and suffix trimmed away: the removals first, then the
// additions. An empty result means the two are identical.
//
// ponytail: prefix/suffix trimming, not an LCS diff — a single config edit
// touches one contiguous region, so the band is tight in practice. Two edits at
// opposite ends of a long document widen it to everything in between (still
// truthful, just less tight); reach for a real LCS diff only if that becomes
// the common case.
func diffLines(before, after []string) []previewLine {
	head := 0
	for head < len(before) && head < len(after) && before[head] == after[head] {
		head++
	}
	tail := 0
	for tail < len(before)-head && tail < len(after)-head &&
		before[len(before)-1-tail] == after[len(after)-1-tail] {
		tail++
	}
	var out []previewLine
	for _, l := range before[head : len(before)-tail] {
		out = append(out, previewLine{Text: l})
	}
	for _, l := range after[head : len(after)-tail] {
		out = append(out, previewLine{Added: true, Text: l})
	}
	return out
}
