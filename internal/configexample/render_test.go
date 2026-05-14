package configexample_test

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
	"github.com/filippolmt/toolbox/internal/configexample"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

func TestRenderContainsAllToolsAndMounts(t *testing.T) {
	got := configexample.Render()

	for _, want := range []string{"shell:", "mounts_root:", "tools:", "mounts:", "Precedence"} {
		if !strings.Contains(got, want) {
			t.Errorf("template missing %q", want)
		}
	}
	for _, e := range catalog.Entries {
		if !strings.Contains(got, e.Key+":") {
			t.Errorf("template missing tool key %q", e.Key)
		}
		if !strings.Contains(got, e.BuildArg) {
			t.Errorf("template missing build arg %q", e.BuildArg)
		}
	}
	for _, m := range mountplan.Defaults() {
		if !strings.Contains(got, m.Name) {
			t.Errorf("template missing mount name %q", m.Name)
		}
	}
}
