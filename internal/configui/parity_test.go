package configui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configrender"
)

// rendererScalar extracts the `key: <value>` line the resolved renderer
// (`config show`) emits for a top-level scalar key.
func rendererScalar(t *testing.T, cfg *config.Config, key string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := configrender.Resolved(&buf, cfg); err != nil {
		t.Fatalf("Resolved: %v", err)
	}
	prefix := key + ": "
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			return v
		}
	}
	t.Fatalf("key %q not found in renderer output:\n%s", key, buf.String())
	return ""
}

// TestRendererParity turns "same fallback" from convention into contract: for
// every fallback-bearing scalar key, the resolved renderer, the config.EffectiveValue
// accessor, and the TUI displayValue must agree. A fallback changed in one
// consumer without going through the shared accessor fails here, naming the key.
// Fixtures: "unset" exercises the raw-empty fallback for every owned scalar (the
// case that catches a consumer bypassing the seam); "planned" mirrors post-Plan
// state; "full" has every scalar set explicitly.
func TestRendererParity(t *testing.T) {
	fixtures := map[string]*config.Config{
		"unset":   {},
		"planned": {Shell: "zsh", Pull: config.PullAuto},
		"full":    {Shell: "zsh", Agent: "codex", Pull: config.PullAlways},
	}
	for name, cfg := range fixtures {
		for _, key := range config.SchemaKeys() {
			want, ok := config.EffectiveValue(cfg, key)
			if !ok {
				continue // only the accessor-owned scalars are parity-guarded
			}
			gotRenderer := rendererScalar(t, cfg, key)
			gotTUI := displayValue(cfg, key)
			if gotRenderer != want || gotTUI != want {
				t.Errorf("[%s] key %q must agree: renderer=%q accessor=%q tui=%q",
					name, key, gotRenderer, want, gotTUI)
			}
		}
	}
}
