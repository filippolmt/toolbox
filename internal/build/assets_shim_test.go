package build

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/mountplan"
)

// bridgeLib is the in-container path of the shared shim transport every
// bridge shim sources; must match the Dockerfile COPY target.
const bridgeLib = "/usr/local/lib/toolbox/bridge-lib.sh"

// TestShimPathsMatchGoConstants guards what only this package can see: that
// every bridge shim sources the shared transport at the path the Dockerfile
// COPYs it to, and that bin/code hardcodes the workspace mount target
// (mountplan.WorkspaceTarget). The shims are static shell assets, so a rename
// on the Go side would otherwise drift silently. The daemon↔shim wire contract
// itself (state dir, socket, routes, JSON fields, allowlists) is pinned next
// to the constants it belongs to, by TestBridgeContract_ShimMatchesGo in
// internal/bridge.
func TestShimPathsMatchGoConstants(t *testing.T) {
	sourceLib := `. ` + bridgeLib
	cases := []struct {
		shim    string
		needles []string
	}{
		{"bin/xdg-open", []string{sourceLib}},
		{"bin/code", []string{
			sourceLib,
			`WORKSPACE="` + mountplan.WorkspaceTarget + `"`,
			// The shell's WorkingDir is the host-path mirror whenever
			// mountplan.WorkspaceMirrorPath allows one, so paths reaching the
			// shim are usually host paths already — the mirror branch is what
			// keeps `code .` working there.
			`"${TOOLBOX_HOST_WORKSPACE}" | "${TOOLBOX_HOST_WORKSPACE}"/*)`,
		}},
		{"bin/proximo", []string{sourceLib}},
		{"bin/git-credential-toolbox", []string{sourceLib}},
		{"bin/paplay", []string{sourceLib}},
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
