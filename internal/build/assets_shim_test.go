package build

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/browserbridge"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// TestShimPathsMatchGoConstants guards the constants duplicated between the
// Go packages and the embedded bridge shims: the state dir
// (browserbridge.ContainerDir) hardcoded in bin/xdg-open and bin/code, and
// the workspace mount target (mountplan.WorkspaceTarget) in bin/code. The
// shims are static shell assets, so a rename on the Go side would otherwise
// drift silently.
func TestShimPathsMatchGoConstants(t *testing.T) {
	cases := []struct {
		shim    string
		needles []string
	}{
		{"bin/xdg-open", []string{`STATE_DIR="` + browserbridge.ContainerDir + `"`}},
		{"bin/code", []string{
			`STATE_DIR="` + browserbridge.ContainerDir + `"`,
			`WORKSPACE="` + mountplan.WorkspaceTarget + `"`,
		}},
	}
	for _, tc := range cases {
		body, err := fs.ReadFile(Assets, AssetDir+"/"+tc.shim)
		if err != nil {
			t.Fatalf("read embedded %s: %v", tc.shim, err)
		}
		for _, needle := range tc.needles {
			if !strings.Contains(string(body), needle) {
				t.Errorf("%s: missing %q — shim constant drifted from the Go-side constant", tc.shim, needle)
			}
		}
	}
}
