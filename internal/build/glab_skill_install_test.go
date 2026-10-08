package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const glabInitScript = "assets/init.d/60-glab.sh"

func runGlabInit(t *testing.T, fakeCommands []string, env ...string) []string {
	t.Helper()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	callsPath := filepath.Join(dir, "glab-calls")
	glab := `#!/bin/sh
if [ "${1:-}" = auth ]; then
    exit 1
fi
if [ "${1:-}" = skills ] && [ "${2:-}" = install ]; then
    printf '%s\n' "$*" >> "$GLAB_CALL_LOG"
fi
`
	writeTestExecutable(t, filepath.Join(binDir, "glab"), glab)
	for _, name := range fakeCommands {
		writeTestExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 0\n")
	}

	body, err := Assets.ReadFile(glabInitScript)
	if err != nil {
		t.Fatalf("read %s: %v", glabInitScript, err)
	}
	body = wireAgentTopologyForTest(t, dir, body)
	scriptPath := filepath.Join(dir, "60-glab.sh")
	if err := os.WriteFile(scriptPath, body, 0o755); err != nil {
		t.Fatalf("write init script: %v", err)
	}

	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	cmd := exec.Command("/bin/bash", scriptPath)
	cmd.Env = append([]string{
		"HOME=" + home,
		"PATH=" + binDir,
		"GLAB_CALL_LOG=" + callsPath,
	}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s: %v\n%s", glabInitScript, err, out)
	}

	calls, err := os.ReadFile(callsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read glab calls: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(calls)), "\n")
}

func writeTestExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestGlabSkillInstallHonoursClaudeConfigDir(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, "custom-claude")
	if err := os.Mkdir(claudeDir, 0o755); err != nil {
		t.Fatalf("create Claude config dir: %v", err)
	}

	calls := runGlabInit(t, []string{"claude"}, "CLAUDE_CONFIG_DIR="+claudeDir)
	want := "skills install --path " + filepath.Join(claudeDir, "skills") + " --force"
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("glab calls = %q, want [%q]", calls, want)
	}
}

func TestGlabSkillInstallIncludesPiWithoutCodex(t *testing.T) {
	calls := runGlabInit(t, []string{"pi"})
	const want = "skills install --global --force"
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("glab calls = %q, want [%q]", calls, want)
	}
}
