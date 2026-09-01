package reload_test

import (
	"os"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/reload"
)

// zshrcPath is the in-container half of the reload, relative to this package.
const zshrcPath = "../build/assets/zshrc.sh"

// TestReloadMarkerContract binds the two ends of the capability marker. The
// host injects the name (Go, internal/sessionplan) and the image's zsh function
// reads it — two languages joined by nothing a compiler checks, shipping on two
// separate release pipelines.
//
// The failure a rename would cause is the quiet one this whole guard exists
// for: the function would take the absent variable for an old CLI and refuse
// forever, on every image, with the remedy it names (`brew upgrade`) doing
// nothing at all.
func TestReloadMarkerContract(t *testing.T) {
	raw, err := os.ReadFile(zshrcPath)
	if err != nil {
		t.Fatalf("read zshrc.sh: %v", err)
	}
	zshrc := string(raw)

	if !strings.Contains(zshrc, "${"+reload.MarkerEnv+":-}") {
		t.Errorf("zshrc.sh does not gate on %s", reload.MarkerEnv)
	}
	if !strings.Contains(zshrc, "$"+reload.MarkerEnv+"\"") {
		t.Errorf("zshrc.sh does not write to $%s", reload.MarkerEnv)
	}
	if !strings.Contains(zshrc, "toolbox-reload()") {
		t.Error("zshrc.sh defines no toolbox-reload function")
	}

	// The host-to-host handover must never be spelled inside the image: it
	// travels across the re-exec and is unset before any container env is
	// built. Same prefix as the marker, opposite direction — which is exactly
	// why someone will eventually try to merge them.
	if strings.Contains(zshrc, reload.FromEnv) {
		t.Errorf("zshrc.sh references %s, which never enters a container", reload.FromEnv)
	}
}
