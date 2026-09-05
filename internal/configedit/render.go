package configedit

import (
	"errors"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/configio"
	"github.com/filippolmt/toolbox/internal/fsx"
)

// headerComment is prepended (as a `#` block) to config files created by a
// writer command, pointing the user at the discovery surfaces. Lives here
// (not configio) so the low-level writer stays policy-free (D7).
const headerComment = `.toolbox.yaml — toolbox configuration.
Run 'toolbox config example' for an annotated template covering every field.
Docs: https://github.com/filippolmt/toolbox`

// Render returns the bytes ApplyChecked would write for a file whose current
// content is src, without touching any file — the seam both a preview and
// ApplyChecked's own validation need to see a pending edit truthfully. Because
// every edit any surface performs is a Mutator, every one of them can be shown
// this way instead of written. exists must report whether the target file is
// already there, because that is what decides the header: a file being created
// carries headerComment, and a preview rendering src alone would under-report
// the write by exactly those lines. A nil mutate renders src as-is.
func Render(name string, src []byte, exists bool, mutate Mutator) ([]byte, error) {
	return configio.RenderDocument(name, src, headerAware(!exists, mutate))
}

// headerAware is the shared header policy behind Render, and so behind every
// write (they all render through it): a document being created gains
// headerComment, an existing one is left alone. Keeping it in one place is what
// lets a preview render the same bytes the write produces.
func headerAware(creating bool, mutate func(doc *yaml.Node)) func(doc *yaml.Node) {
	return func(doc *yaml.Node) {
		if creating && doc.HeadComment == "" {
			doc.HeadComment = headerComment
		}
		if mutate != nil {
			mutate(doc)
		}
	}
}

// EnsureFileWithHeader creates path containing only the documentation
// header when it does not exist yet. Used by `config edit` so the editor
// opens a file with discovery pointers instead of a blank page (a
// comment-only file is valid YAML and an empty document to the loader).
func EnsureFileWithHeader(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var b strings.Builder
	for line := range strings.SplitSeq(headerComment, "\n") {
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return fsx.AtomicWriteFile(path, []byte(b.String()), 0o600)
}
