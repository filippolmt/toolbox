package build

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// The credential shim asks the host helpers not to prompt exactly when the
// caller set GCM_INTERACTIVE to one of the values Git Credential Manager reads
// as "never" — git prune-dead does — and sends the request it always did
// otherwise, so an ordinary clone can still prompt. The shim runs for real,
// against a stand-in transport that records the payload it would POST.
func TestCredentialShimSendsNoPromptOnlyWhenGCMMustNotPrompt(t *testing.T) {
	body, err := fs.ReadFile(Assets, AssetDir+"/bin/git-credential-toolbox")
	if err != nil {
		t.Fatalf("read embedded shim: %v", err)
	}
	dir := t.TempDir()
	lib := filepath.Join(dir, "bridge-lib.sh")
	const fakeLib = `bridge_ready() { return 0; }
bridge_post() { printf '%s' "$2" > "$PAYLOAD_OUT"; printf '{"output":""}' > "$3"; echo 200; }
`
	if err := os.WriteFile(lib, []byte(fakeLib), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "git-credential-toolbox")
	src := strings.Replace(string(body), ". "+bridgeLib, ". "+lib, 1)
	if err := os.WriteFile(shim, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		gcm  string // "" leaves GCM_INTERACTIVE unset
		want bool
	}{{"never", true}, {"Never", true}, {"false", true}, {"0", true}, {"", false}, {"auto", false}} {
		payload := filepath.Join(t.TempDir(), "payload")
		cmd := exec.Command("sh", shim, "get")
		cmd.Stdin = strings.NewReader("protocol=https\nhost=forge.example.internal\n\n")
		cmd.Env = append(os.Environ(), "PAYLOAD_OUT="+payload, "GIT_CREDENTIAL_BRIDGE=")
		if tc.gcm != "" {
			cmd.Env = append(cmd.Env, "GCM_INTERACTIVE="+tc.gcm)
		} else {
			cmd.Env = append(cmd.Env, "GCM_INTERACTIVE=")
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("GCM_INTERACTIVE=%q: shim: %v\n%s", tc.gcm, err, out)
		}
		raw, err := os.ReadFile(payload)
		if err != nil {
			t.Fatalf("GCM_INTERACTIVE=%q: no payload posted: %v", tc.gcm, err)
		}
		var req map[string]any
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("GCM_INTERACTIVE=%q: payload %q: %v", tc.gcm, raw, err)
		}
		if _, got := req["no_prompt"]; got != tc.want {
			t.Errorf("GCM_INTERACTIVE=%q: no_prompt sent = %v, want %v (payload %s)", tc.gcm, got, tc.want, raw)
		}
		if req["op"] != "get" || req["input"] != "protocol=https\nhost=forge.example.internal\n\n" {
			t.Errorf("GCM_INTERACTIVE=%q: op/input not forwarded as given: %s", tc.gcm, raw)
		}
	}
}
