package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

const (
	agentTopologyLib    = "assets/bin/agent-topology-lib.sh"
	agentTopologySource = `. /usr/local/lib/toolbox/agent-topology-lib.sh`
)

func wireAgentTopologyForTest(t *testing.T, dir string, body []byte) []byte {
	t.Helper()
	lib, err := Assets.ReadFile(agentTopologyLib)
	if err != nil {
		t.Fatalf("read %s: %v", agentTopologyLib, err)
	}
	path := filepath.Join(dir, "agent-topology-lib.sh")
	if err := os.WriteFile(path, lib, 0o644); err != nil {
		t.Fatalf("write agent topology library: %v", err)
	}
	return []byte(strings.ReplaceAll(string(body), agentTopologySource, `. "`+path+`"`))
}

func runAgentTopology(t *testing.T, script string, env ...string) string {
	t.Helper()

	body, err := Assets.ReadFile(agentTopologyLib)
	if err != nil {
		t.Fatalf("read %s: %v", agentTopologyLib, err)
	}
	path := filepath.Join(t.TempDir(), "agent-topology-lib.sh")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write agent topology library: %v", err)
	}

	cmd := exec.Command("/bin/bash", "-c", ". \"$1\"; "+script, "bash", path)
	cmd.Env = append([]string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run agent topology library: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestAgentTopologyDeclaresEverySupportedAgent(t *testing.T) {
	got := strings.Fields(runAgentTopology(t, "toolbox_agent_names"))
	if strings.Join(got, "\n") != strings.Join(config.SupportedAgents, "\n") {
		t.Fatalf("shell agent topology = %q, SupportedAgents = %q", got, config.SupportedAgents)
	}
}

func TestAgentTopologyOwnsNativeAndSkillRoots(t *testing.T) {
	got := strings.Split(runAgentTopology(t, `
HOME=/test-home
CLAUDE_CONFIG_DIR=/custom-claude
CODEX_HOME=/custom-codex
for agent in $(toolbox_agent_names); do
    printf '%s|%s|%s\n' "$agent" "$(toolbox_agent_home "$agent")" "$(toolbox_agent_skill_root "$agent")"
done
`), "\n")
	want := []string{
		"claude|/custom-claude|/custom-claude/skills",
		"codex|/custom-codex|/test-home/.agents/skills",
		"pi|/test-home/.pi|/test-home/.agents/skills",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("agent roots = %q, want %q", got, want)
	}
}

func TestAgentTopologyVisitsEachActiveSkillRootOnce(t *testing.T) {
	home := t.TempDir()
	claudeHome := filepath.Join(home, "claude")
	if err := os.Mkdir(claudeHome, 0o755); err != nil {
		t.Fatal(err)
	}

	got := strings.Split(runAgentTopology(t, `
claude() { :; }
pi() { :; }
emit() { printf '%s|%s\n' "$1" "$2"; }
toolbox_for_each_active_skill_root emit
`, "HOME="+home, "CLAUDE_CONFIG_DIR="+claudeHome), "\n")
	want := []string{
		"claude|" + filepath.Join(claudeHome, "skills"),
		"cross-agent|" + filepath.Join(home, ".agents", "skills"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("active skill roots = %q, want %q", got, want)
	}
}

func TestAgentTopologyOwnsClaudeSettingsLock(t *testing.T) {
	home := t.TempDir()
	got := runAgentTopology(t, `
write_settings() { printf written; }
toolbox_with_claude_settings_lock write_settings
printf '|%s' "$([ -f "$HOME/.toolbox-state/.claude-settings.lock" ] && echo locked)"
`, "HOME="+home)
	if got != "written|locked" {
		t.Fatalf("locked writer result = %q, want written|locked", got)
	}
}

func TestAgentTopologyShipsInTheRuntimeImage(t *testing.T) {
	dockerfile := readAsset(t, "Dockerfile")
	const copyLine = "COPY --link --chmod=0644 bin/agent-topology-lib.sh /usr/local/lib/toolbox/agent-topology-lib.sh"
	if !strings.Contains(dockerfile, copyLine) {
		t.Fatalf("Dockerfile does not ship Agent Topology with %q", copyLine)
	}
	smokeBody, err := os.ReadFile("assets/smoke-test.sh")
	if err != nil {
		t.Fatalf("read smoke test: %v", err)
	}
	if !strings.Contains(string(smokeBody), `check_required "agent-topology-lib"`) {
		t.Fatal("smoke test does not verify the Agent Topology library")
	}
}

func TestAgentTopologyHasOneOwnerAcrossImageScripts(t *testing.T) {
	for _, path := range []string{
		"init.d/02-gh.sh",
		"init.d/10-remove-rtk.sh",
		"init.d/20-cf.sh",
		"init.d/25-codex.sh",
		"init.d/30-graphify.sh",
		"init.d/31-codegraph.sh",
		"init.d/35-statusline.sh",
		"init.d/36-mode-flags.sh",
		"init.d/40-playwright-cli.sh",
		"init.d/50-mcp-plugins.sh",
		"init.d/60-glab.sh",
		"init.d/61-herdr.sh",
		"init.d/65-atuin.sh",
		"statusline-command.sh",
	} {
		body := readAsset(t, path)
		if !strings.Contains(body, agentTopologySource) {
			t.Errorf("%s derives agent topology instead of consuming its owner", path)
		}
	}

	entries, err := Assets.ReadDir("assets/init.d")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"statusline-command.sh"}
	for _, entry := range entries {
		paths = append(paths, "init.d/"+entry.Name())
	}
	for _, path := range paths {
		for _, line := range strings.Split(readAsset(t, path), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			for _, duplicate := range []string{
				`${CLAUDE_CONFIG_DIR:-`,
				`${CODEX_HOME:-`,
				`$HOME/.claude`,
				`$HOME/.codex`,
				`$HOME/.pi`,
				`$HOME/.agents`,
				`.claude-settings.lock`,
			} {
				if strings.Contains(line, duplicate) {
					t.Errorf("%s duplicates Agent Topology fact %q", path, duplicate)
				}
			}
		}
	}
}

func TestAgentWiringDeclaresEverySupportedAgent(t *testing.T) {
	herdr := readAsset(t, "init.d/61-herdr.sh")
	atuin := readAsset(t, "init.d/65-atuin.sh")
	atuinAdapters := map[string]string{
		"claude": "claude-code",
		"codex":  "codex",
		"pi":     "pi",
	}

	for _, agent := range config.SupportedAgents {
		if !strings.Contains(herdr, `_herdr_install_integration `+agent) {
			t.Errorf("Herdr wiring is undeclared for supported agent %q", agent)
		}
		adapter, ok := atuinAdapters[agent]
		if !ok {
			t.Errorf("Atuin wiring has no declared adapter for supported agent %q", agent)
			continue
		}
		if !strings.Contains(atuin, `atuin hook install `+adapter) {
			t.Errorf("Atuin wiring is undeclared for supported agent %q", agent)
		}
	}
}
