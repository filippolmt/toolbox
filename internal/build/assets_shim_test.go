package build

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// bridgeLib is the in-container path of the shared shim transport every
// bridge shim sources; must match the Dockerfile COPY target.
const bridgeLib = "/usr/local/lib/toolbox/bridge-lib.sh"

// TestShimPathsMatchGoConstants guards the constants duplicated between the
// Go packages and the embedded bridge shims: the state dir
// (bridge.ContainerDir) hardcoded in bin/bridge-lib.sh (the shared
// transport every shim sources), and the workspace mount target
// (mountplan.WorkspaceTarget) in bin/code. The shims are static shell
// assets, so a rename on the Go side would otherwise drift silently.
func TestShimPathsMatchGoConstants(t *testing.T) {
	sourceLib := `. ` + bridgeLib
	cases := []struct {
		shim    string
		needles []string
	}{
		{"bin/bridge-lib.sh", []string{
			`BRIDGE_STATE_DIR="` + bridge.ContainerDir + `"`,
			`BRIDGE_STATE_DIR="` + bridge.LegacyContainerDir + `"`,
			`BRIDGE_SOCK="` + bridge.ContainerSocket + `"`,
		}},
		{"bin/xdg-open", []string{sourceLib}},
		{"bin/code", []string{
			sourceLib,
			`WORKSPACE="` + mountplan.WorkspaceTarget + `"`,
		}},
		{"bin/proximo", []string{sourceLib}},
		{"bin/git-credential-toolbox", []string{sourceLib}},
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
