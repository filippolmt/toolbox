package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRTKIsAbsentFromTheRuntimeImage(t *testing.T) {
	dockerfile, err := Assets.ReadFile("assets/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"RTK_VERSION", "rtk-builder", "/usr/local/bin/rtk", "RTK_TELEMETRY_DISABLED", "RTK_TEE"} {
		if strings.Contains(string(dockerfile), forbidden) {
			t.Errorf("Dockerfile still contains active RTK token %q", forbidden)
		}
	}
}

// TestRTKRemovalMigrationCleansManagedAgentWiring exercises the migration at
// the shell-start boundary: persisted agent homes in, only RTK-owned wiring
// removed, unrelated settings left intact.
func TestRTKRemovalMigrationCleansManagedAgentWiring(t *testing.T) {
	home := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(home, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(".claude/settings.json", `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"rtk hook claude"},{"type":"command","command":"keep-me"}]}]},"theme":"dark"}`)
	write(".claude/CLAUDE.md", "before\n@RTK.md\nafter\n")
	write(".claude/RTK.md", "managed\n")
	write(".codex/hooks.json", `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"rtk hook codex"}]}]},"keep":true}`)
	write(".codex/AGENTS.md", "keep\n@"+home+"/.codex/RTK.md\n")
	write(".codex/RTK.md", "managed\n")
	write(".pi/agent/extensions/rtk.ts", "// user-modified RTK extension\n")
	write(".config/rtk/config.toml", "retired\n")
	write(".local/share/rtk/history.db", "retired\n")

	script, err := Assets.ReadFile("assets/init.d/10-remove-rtk.sh")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	script = wireAgentTopologyForTest(t, home, script)
	cmd := exec.Command("bash", "-c", string(script))
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migration: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "kept modified") {
		t.Errorf("migration output = %q, want modified-extension warning", out)
	}

	for _, name := range []string{".claude/RTK.md", ".codex/RTK.md", ".config/rtk", ".local/share/rtk"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".pi/agent/extensions/rtk.ts")); err != nil {
		t.Errorf("modified pi extension was removed: %v", err)
	}
	for _, tc := range []struct {
		name string
		want string
		not  string
	}{
		{".claude/settings.json", `"theme": "dark"`, "rtk"},
		{".claude/settings.json", "keep-me", "rtk"},
		{".claude/CLAUDE.md", "before\nafter", "RTK.md"},
		{".codex/hooks.json", `"keep": true`, "rtk"},
		{".codex/AGENTS.md", "keep", "RTK.md"},
	} {
		body, err := os.ReadFile(filepath.Join(home, tc.name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), tc.want) || strings.Contains(strings.ToLower(string(body)), strings.ToLower(tc.not)) {
			t.Errorf("%s after migration:\n%s", tc.name, body)
		}
	}
}
