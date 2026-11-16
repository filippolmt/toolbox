package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const agentGuardrailsInit = "init.d/37-agent-guardrails.sh"

// guardrailsFixture is one container start: a home with Claude's state
// mounted, a fake claude on PATH and a stand-in for the baked hook binary.
type guardrailsFixture struct {
	dir, home, bin string
}

func newGuardrailsFixture(t *testing.T) guardrailsFixture {
	t.Helper()
	requireTools(t, "jq", "flock")
	dir := t.TempDir()
	f := guardrailsFixture{dir: dir, home: filepath.Join(dir, "home"), bin: filepath.Join(dir, "agent-guardrails")}
	writeTestExecutable(t, f.bin, "#!/bin/sh\nexit 0\n")
	if err := os.MkdirAll(filepath.Join(f.home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f guardrailsFixture) run(t *testing.T) {
	t.Helper()
	binDir := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, filepath.Join(binDir, "claude"), "#!/bin/sh\nexit 0\n")
	for _, tool := range []string{"jq", "flock"} {
		p, _ := exec.LookPath(tool)
		if err := os.Symlink(p, filepath.Join(binDir, tool)); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	body := readAsset(t, agentGuardrailsInit)
	if !strings.Contains(body, bakedGuardrailsBin) {
		t.Fatalf("%s no longer names %s", agentGuardrailsInit, bakedGuardrailsBin)
	}
	body = strings.ReplaceAll(body, bakedGuardrailsBin, f.bin)
	script := filepath.Join(f.dir, "37-agent-guardrails.sh")
	if err := os.WriteFile(script, wireAgentTopologyForTest(t, f.dir, []byte(body)), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = []string{"HOME=" + f.home, "PATH=" + binDir + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s: %v\n%s", agentGuardrailsInit, err, out)
	}
}

func (f guardrailsFixture) settings() string {
	return filepath.Join(f.home, ".claude", "settings.json")
}

const bakedGuardrailsBin = "/usr/local/lib/toolbox/agent-guardrails"

// TestAgentGuardrailsInitWiresTheHookIdempotently: the hook lands as a
// PreToolUse Bash hook beside the developer's own, and a second start writes
// the same bytes.
func TestAgentGuardrailsInitWiresTheHookIdempotently(t *testing.T) {
	f := newGuardrailsFixture(t)
	writeFile(t, f.settings(), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"my-own-hook"}]}]},"theme":"dark"}`)

	f.run(t)
	got := commands(readHooks(t, f.settings()), "PreToolUse")
	want := []string{"my-own-hook", f.bin}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("PreToolUse commands = %q, want %q", got, want)
	}
	first, err := os.ReadFile(f.settings())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), `"theme": "dark"`) && !strings.Contains(string(first), `"theme":"dark"`) {
		t.Errorf("unrelated setting lost:\n%s", first)
	}

	f.run(t)
	second, err := os.ReadFile(f.settings())
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("second start rewrote settings.json:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// TestAgentGuardrailsInitSkipsAMissingBinary: an image without the hook
// leaves settings.json alone rather than wiring a command that cannot run.
func TestAgentGuardrailsInitSkipsAMissingBinary(t *testing.T) {
	f := newGuardrailsFixture(t)
	if err := os.Remove(f.bin); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if _, err := os.Stat(f.settings()); !os.IsNotExist(err) {
		t.Fatalf("settings.json written without the hook binary (stat err %v)", err)
	}
}

// TestAgentGuardrailsInitSkipsAnUnmountedClaudeState: a hook written into a
// home Claude's state is not mounted on would vanish with the container.
func TestAgentGuardrailsInitSkipsAnUnmountedClaudeState(t *testing.T) {
	f := newGuardrailsFixture(t)
	if err := os.RemoveAll(filepath.Join(f.home, ".claude")); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	if _, err := os.Stat(f.settings()); !os.IsNotExist(err) {
		t.Fatalf("settings.json written without Claude's state mounted (stat err %v)", err)
	}
}

// TestAgentGuardrailsInitKeepsItsPlace: a hook the developer adds after the
// guardrails stays after them on the next start; nothing is reordered.
func TestAgentGuardrailsInitKeepsItsPlace(t *testing.T) {
	f := newGuardrailsFixture(t)
	writeFile(t, f.settings(), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"`+f.bin+`"}]},{"matcher":"Bash","hooks":[{"type":"command","command":"added-later"}]}]}}`)
	f.run(t)
	got := commands(readHooks(t, f.settings()), "PreToolUse")
	want := []string{f.bin, "added-later"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("PreToolUse commands = %q, want %q", got, want)
	}
}
