package configexample_test

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/configexample"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

func TestRenderContainsExpectedSections(t *testing.T) {
	got := configexample.Render()

	for _, want := range []string{"shell:", "image:", "registry_mirror:", "pull:", "mounts_root:", "inherit_host_auth", "mounts:", "proximo:", "Precedence"} {
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
