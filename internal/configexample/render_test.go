package configexample_test

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/configexample"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// TestRenderCoversSchema is the anti-drift guard for the annotated example:
// every config.SchemaKeys() field must be documented, except the deprecated
// browser_bridge alias (only the canonical bridge is shown). A new Config
// field the template forgets turns this red.
func TestRenderCoversSchema(t *testing.T) {
	got := configexample.Render()
	const skip = "browser_bridge"
	for _, key := range config.SchemaKeys() {
		if key == skip {
			if strings.Contains(got, key+":") {
				t.Errorf("deprecated key %q must not be documented in the example", key)
			}
			continue
		}
		if !strings.Contains(got, key+":") {
			t.Errorf("annotated example is missing key %q", key)
		}
	}
}

func TestRenderContainsExpectedSections(t *testing.T) {
	got := configexample.Render()

	for _, want := range []string{"shell:", "image:", "registry_mirror:", "pull:", "mounts_root:", "inherit_host_auth", "mounts:", "proximo:", "worktree:", "Precedence"} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q", want)
		}
	}
	if strings.Contains(got, "tools:") {
		t.Error("template must not contain the removed tools: block")
	}
	for _, k := range catalog.HostAuthEligibleKeys() {
		if !strings.Contains(got, k) {
			t.Errorf("template missing eligible CLI key %q in inherit_host_auth section", k)
		}
	}
	for _, m := range mountplan.Defaults() {
		if !strings.Contains(got, m.Name) {
			t.Errorf("template missing mount name %q", m.Name)
		}
	}
}
