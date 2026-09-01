package imageprefetch

import (
	"os"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// zshrcPath is the on-disk prompt renderer, relative to this package.
const zshrcPath = "../build/assets/zshrc.sh"

// TestUpdateCheckCacheContract binds the two ends of the update-check cache.
// The host writes it (Go, this package) and the in-container zsh precmd hook
// parses it — two languages joined by nothing but a file on a bind mount, and
// the CLI and the image ship on separate release pipelines, so a rename on
// either side would go unnoticed until a banner silently stopped firing.
//
// Mirrors internal/bridge's shim contract test: drift is a red test, not a
// field report.
func TestUpdateCheckCacheContract(t *testing.T) {
	raw, err := os.ReadFile(zshrcPath)
	if err != nil {
		t.Fatalf("read zshrc.sh: %v", err)
	}
	zshrc := string(raw)

	// Every field the writer emits, in the renderer's own parse spelling
	// (`case $line in <field>=*)`).
	body := renderedCache(t)
	for _, field := range []string{"image_update", "image_latest", "image_state", "cli_update", "cli_latest"} {
		if !strings.Contains(body, field+"=") {
			t.Errorf("writer emits no %q field", field)
		}
	}
	// The four the renderer actually reads back. image_latest is written but
	// never parsed: it is what makes the `.shown` signature change when a new
	// digest lands, which is what re-fires the banner.
	for _, field := range []string{"image_update", "image_state", "cli_update", "cli_latest"} {
		if !strings.Contains(zshrc, field+"=*)") {
			t.Errorf("zshrc.sh does not parse %q", field)
		}
	}

	// image_state's vocabulary is compared, not just its name: the host
	// computes the word and the renderer does no arithmetic, so a value the
	// renderer never matches is a state that silently never renders.
	if !strings.Contains(zshrc, "== "+stateUnavailable) {
		t.Errorf("zshrc.sh renders nothing for image_state=%s", stateUnavailable)
	}

	// The file names, likewise spelled on both sides.
	for _, path := range []string{cacheFile, cacheFile + ".shown"} {
		if !strings.Contains(zshrc, "/.toolbox-state/"+path) {
			t.Errorf("zshrc.sh does not read $HOME/.toolbox-state/%s", path)
		}
	}

	// The opt-out is one name read on two sides: the host edge skips the
	// probe, zshrc skips the render. A rename on either side alone would
	// half-disable the feature with nothing to say so.
	if !strings.Contains(zshrc, "${"+sessionplan.NoUpdateCheckEnv+":-}") {
		t.Errorf("zshrc.sh does not honour %s", sessionplan.NoUpdateCheckEnv)
	}

	// The retired in-container poller must stay retired: the host is now the
	// single detector, and a second one polling the same registry over a
	// different transport is the split this collapse removed.
	for _, gone := range []string{"toolbox-update-check", stampFile, "TOOLBOX_UPDATE_CHECK_TTL"} {
		if strings.Contains(zshrc, gone) {
			t.Errorf("zshrc.sh still references the retired poller (%q)", gone)
		}
	}
}

// renderedCache returns one cache body straight from the writer, so the field
// names asserted above are the ones a real poll emits rather than a copy.
func renderedCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeResult(dir, result{
		imageUpdate: true,
		imageLatest: "sha256:x",
		imageState:  stateReady,
		cliUpdate:   true,
		cliLatest:   "v1.2.3",
	})
	raw, err := os.ReadFile(dir + "/" + cacheFile)
	if err != nil {
		t.Fatalf("writer produced no cache: %v", err)
	}
	return string(raw)
}
