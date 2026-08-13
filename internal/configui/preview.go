package configui

import (
	"strings"

	"github.com/filippolmt/toolbox/internal/configedit"
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

// baseDoc is the target document as the editor found it. The three fields are
// readMaybe's own return triple and are useless apart: the preview needs the
// bytes to diff against, exists because an absent target is created carrying the
// docs header, and err to say why it can show nothing at all.
type baseDoc struct {
	bytes  []byte
	exists bool
	err    error
}

// readBaseDoc snapshots target for an editor session.
func readBaseDoc(target string) baseDoc {
	b, exists, err := readMaybe(target)
	return baseDoc{bytes: b, exists: exists, err: err}
}

// previewDiff renders mut against base and returns the lines that differ. An
// empty result means the mutation would change nothing — the caller says so
// rather than showing an unchanged fragment, which would imply a pending change
// that does not exist. name only labels parse/encode errors.
func previewDiff(name string, base baseDoc, mut configedit.Mutator) ([]previewLine, error) {
	if base.err != nil {
		return nil, base.err
	}
	// configedit.Render, not configio.RenderDocument: creating the file also
	// writes the docs header, and a preview that omitted it would under-report
	// the write by exactly those lines.
	after, err := configedit.Render(name, base.bytes, base.exists, mut)
	if err != nil {
		return nil, err
	}
	// The "before" side is the file's own bytes, NOT a re-render of them. The
	// write replaces the file with the encoder's output wholesale, so every line
	// the encoder normalises really does change — re-indentation, dropped blank
	// lines, comments lost from a document with no keys. Cancelling that out by
	// rendering both sides would hide part of the edit rather than remove noise,
	// and it described a keyless document as `{}`, a token no file ever holds. An
	// absent target simply has no lines, so a create diffs as pure addition.
	return diffLines(documentLines(base.bytes), documentLines(after)), nil
}

// documentLines splits document bytes into lines, dropping the trailing newline
// so an empty document yields no lines at all.
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
// ponytail: prefix/suffix trimming, not an LCS diff. The band is tight when the
// file is already in the encoder's own shape, which is the case for any file
// toolbox wrote. It widens whenever the write also normalises the document —
// re-indenting a hand-written block, dropping blank lines between blocks — since
// those lines differ too and can sit far apart. That output is still truthful
// (the write really does change them), just less tight than a real LCS diff
// would render it; reach for one if the widening becomes the common case.
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
