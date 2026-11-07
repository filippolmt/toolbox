package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runCodexInit runs init.d/25-codex.sh once against home, with a fake codex
// on PATH.
func runCodexInit(t *testing.T, home string) {
	t.Helper()
	for _, tool := range []string{"jq", "flock"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH: the runtime image and CI ship it", tool)
		}
	}
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestExecutable(t, filepath.Join(binDir, "codex"), "#!/bin/sh\nexit 0\n")
	for _, tool := range []string{"jq", "flock"} {
		p, _ := exec.LookPath(tool)
		if err := os.Symlink(p, filepath.Join(binDir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "25-codex.sh")
	if err := os.WriteFile(script, wireAgentTopologyForTest(t, dir, []byte(readAsset(t, "init.d/25-codex.sh"))), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = []string{"HOME=" + home, "PATH=" + binDir + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run 25-codex.sh: %v\n%s", err, out)
	}
}

// TestCodexInitDropsEarlierImageHooks: earlier images wrote Atuin, Herdr and
// Worktrunk hooks into ~/.codex/hooks.json; the image now ships them in
// /etc/codex/hooks.json, so the old copies would run twice. Only those go:
// the developer's own hooks stay, in place.
func TestCodexInitDropsEarlierImageHooks(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	codexHome := filepath.Join(home, ".codex")
	hooksFile := filepath.Join(codexHome, "hooks.json")
	herdrScript := filepath.Join(codexHome, "herdr-agent-state.sh")
	writeFile(t, herdrScript, "#!/bin/sh\n")
	writeFile(t, hooksFile, `{"hooks":{
  "PreToolUse":[{"matcher":"^Bash$","hooks":[{"type":"command","command":"/usr/local/bin/atuin hook codex"}]}],
  "SessionStart":[{"hooks":[{"type":"command","command":"bash '`+herdrScript+`' session","timeout":10}]}],
  "Stop":[
    {"hooks":[{"type":"command","command":"/usr/local/bin/wt config state marker set 💬 >/dev/null 2>&1 || true"}]},
    {"hooks":[{"type":"command","command":"echo mine"}]}
  ]
}}`)

	runCodexInit(t, home)

	hooks := readHooks(t, hooksFile)
	if len(hooks) != 1 {
		t.Errorf("events left = %v, want only Stop", hooks)
	}
	if cmds := commands(hooks, "Stop"); len(cmds) != 1 || cmds[0] != "echo mine" {
		t.Errorf("Stop = %q, want the developer's hook alone", cmds)
	}
	if _, err := os.Stat(herdrScript); !os.IsNotExist(err) {
		t.Errorf("Herdr's script survived: %v", err)
	}
}

// TestCodexInitLeavesAFileWithoutImageHooksUntouched: no rewrite, so the
// developer's formatting survives every start.
func TestCodexInitLeavesAFileWithoutImageHooksUntouched(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	hooksFile := filepath.Join(home, ".codex", "hooks.json")
	const body = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`
	writeFile(t, hooksFile, body)

	runCodexInit(t, home)

	if b, err := os.ReadFile(hooksFile); err != nil || string(b) != body {
		t.Errorf("hooks.json changed: %v\n%s", err, b)
	}
}
