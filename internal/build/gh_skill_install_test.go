package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ghInitScript = "assets/init.d/02-gh.sh"

func runGhInit(t *testing.T, fakeCommands ...string) (home, claudeDir string, calls []string) {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	callsPath := filepath.Join(dir, "gh-calls")
	gh := `#!/bin/sh
if [ "${1:-}" = skill ] && [ "${2:-}" = install ]; then
    printf '%s\n' "$*" >> "$GH_CALL_LOG"
    while [ "$#" -gt 0 ]; do
        if [ "$1" = --dir ]; then
            /bin/mkdir -p "$2/gh"
            [ -f "$2/gh/SKILL.md" ] || printf '# gh fixture\n' > "$2/gh/SKILL.md"
            break
        fi
        shift
    done
    exit 1
fi
exit 1
`
	writeTestExecutable(t, filepath.Join(binDir, "gh"), gh)
	writeTestExecutable(t, filepath.Join(binDir, "grep"), "#!/bin/sh\nexec /usr/bin/grep \"$@\"\n")
	writeTestExecutable(t, filepath.Join(binDir, "cat"), "#!/bin/sh\nexec /bin/cat \"$@\"\n")
	for _, name := range fakeCommands {
		writeTestExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 0\n")
	}

	body, err := Assets.ReadFile(ghInitScript)
	if err != nil {
		t.Fatalf("read %s: %v", ghInitScript, err)
	}
	body = wireAgentTopologyForTest(t, dir, body)
	scriptPath := filepath.Join(dir, filepath.Base(ghInitScript))
	if err := os.WriteFile(scriptPath, body, 0o755); err != nil {
		t.Fatalf("write init script: %v", err)
	}

	home = filepath.Join(dir, "home")
	claudeDir = filepath.Join(home, "custom-claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("create Claude config dir: %v", err)
	}
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + binDir,
		"GH_CALL_LOG=" + callsPath,
		"CLAUDE_CONFIG_DIR=" + claudeDir,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s: %v\n%s", ghInitScript, err, out)
	}

	loggedCalls, err := os.ReadFile(callsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return home, claudeDir, nil
		}
		t.Fatalf("read gh calls: %v", err)
	}
	return home, claudeDir, strings.Split(strings.TrimSpace(string(loggedCalls)), "\n")
}

func TestGhSkillInstallKeepsCompactMonitoringAcrossRetries(t *testing.T) {
	home, claudeDir, _ := runGhInit(t, "claude", "pi")
	dir := filepath.Dir(home)
	cmd := exec.Command("/bin/bash", filepath.Join(dir, filepath.Base(ghInitScript)))
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + filepath.Join(dir, "bin"),
		"GH_CALL_LOG=" + filepath.Join(dir, "gh-calls"),
		"CLAUDE_CONFIG_DIR=" + claudeDir,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("retry gh init: %v\n%s", err, out)
	}
	for _, root := range []string{filepath.Join(claudeDir, "skills"), filepath.Join(home, ".agents", "skills")} {
		body, err := os.ReadFile(filepath.Join(root, "gh", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Count(text, "<!-- toolbox-gh-monitoring -->") != 1 || !strings.Contains(text, "--compact --exit-status") || !strings.Contains(text, "headSha") {
			t.Fatalf("skill at %s lost or duplicated the compact monitoring guidance", root)
		}
	}
}

func TestGhSkillInstallIncludesCodexWithoutPi(t *testing.T) {
	home, _, calls := runGhInit(t, "codex")
	want := "skill install cli/cli gh --dir " + filepath.Join(home, ".agents", "skills") + " --force"
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("gh calls = %q, want [%q]", calls, want)
	}
}

func TestGhSkillInstallWiresClaudeAndPiNonFatally(t *testing.T) {
	home, claudeDir, got := runGhInit(t, "claude", "pi")
	want := []string{
		"skill install cli/cli gh --dir " + filepath.Join(claudeDir, "skills") + " --force",
		"skill install cli/cli gh --dir " + filepath.Join(home, ".agents", "skills") + " --force",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gh calls = %q, want %q", got, want)
	}
}
