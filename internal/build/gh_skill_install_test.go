package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ghInitScript = "assets/init.d/02-gh-creds.sh"

func TestGhSkillInstallWiresEveryBundledAgentNonFatally(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	callsPath := filepath.Join(dir, "gh-calls")
	gh := `#!/bin/sh
if [ "${1:-}" = skill ] && [ "${2:-}" = install ]; then
    printf '%s\n' "$*" >> "$GH_CALL_LOG"
    exit 1
fi
exit 1
`
	writeGlabTestExecutable(t, filepath.Join(binDir, "gh"), gh)
	for _, name := range []string{"claude", "pi"} {
		writeGlabTestExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 0\n")
	}

	body, err := Assets.ReadFile(ghInitScript)
	if err != nil {
		t.Fatalf("read %s: %v", ghInitScript, err)
	}
	scriptPath := filepath.Join(dir, "02-gh-creds.sh")
	if err := os.WriteFile(scriptPath, body, 0o755); err != nil {
		t.Fatalf("write init script: %v", err)
	}

	home := filepath.Join(dir, "home")
	claudeDir := filepath.Join(home, "custom-claude")
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

	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("read gh calls: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(calls)), "\n")
	want := []string{
		"skill install cli/cli gh --dir " + filepath.Join(claudeDir, "skills") + " --force",
		"skill install cli/cli gh --dir " + filepath.Join(home, ".agents", "skills") + " --force",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("gh calls = %q, want %q", got, want)
	}
}
