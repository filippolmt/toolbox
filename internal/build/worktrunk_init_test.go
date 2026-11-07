package build

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	worktrunkInitScript = "assets/init.d/62-worktrunk.sh"
	worktrunkSkillSrc   = "/usr/local/share/worktrunk/skills/worktrunk"
)

// worktrunkFixture is one container start's worth of state: a home with every
// agent's state root mounted, and the skill the image ships.
type worktrunkFixture struct {
	dir, home, skillSrc string
}

func newWorktrunkFixture(t *testing.T) worktrunkFixture {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH: the script merges agent hooks with jq, which the runtime image and CI ship")
	}
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock not on PATH")
	}
	dir := t.TempDir()
	f := worktrunkFixture{dir: dir, home: filepath.Join(dir, "home"), skillSrc: filepath.Join(dir, "skill-src")}
	for _, d := range []string{".claude", ".codex", ".pi"} {
		if err := os.MkdirAll(filepath.Join(f.home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(f.skillSrc, "reference"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.skillSrc, "SKILL.md"), "# worktrunk fixture\n")
	writeFile(t, filepath.Join(f.skillSrc, "reference", "config.md"), "config\n")
	return f
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run executes the embedded script once, with fake agents and a fake wt whose
// pi install writes the extension where PI_CODING_AGENT_DIR points.
func (f worktrunkFixture) run(t *testing.T) string {
	t.Helper()
	binDir := filepath.Join(f.dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "pi"} {
		writeTestExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 0\n")
	}
	writeTestExecutable(t, filepath.Join(binDir, "wt"), `#!/bin/sh
if [ "$*" = "config plugins pi install --yes" ]; then
    mkdir -p "$PI_CODING_AGENT_DIR/extensions"
    printf '// worktrunk pi fixture\n' > "$PI_CODING_AGENT_DIR/extensions/worktrunk.ts"
    exit 0
fi
exit 1
`)
	for _, tool := range []string{"jq", "flock"} {
		p, _ := exec.LookPath(tool)
		if err := os.Symlink(p, filepath.Join(binDir, tool)); err != nil && !os.IsExist(err) {
			t.Fatal(err)
		}
	}

	body := readAsset(t, "init.d/62-worktrunk.sh")
	body = strings.ReplaceAll(body, worktrunkSkillSrc, f.skillSrc)
	script := filepath.Join(f.dir, "62-worktrunk.sh")
	if err := os.WriteFile(script, wireAgentTopologyForTest(t, f.dir, []byte(body)), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Env = []string{"HOME=" + f.home, "PATH=" + binDir + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", worktrunkInitScript, err, out)
	}
	return string(out)
}

type hookGroup struct {
	Matcher *string `json:"matcher,omitempty"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

func readHooks(t *testing.T, path string) map[string][]hookGroup {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, b)
	}
	return doc.Hooks
}

// commands lists every command registered for event, in order.
func commands(hooks map[string][]hookGroup, event string) []string {
	var out []string
	for _, g := range hooks[event] {
		for _, h := range g.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

func countMarkers(cmds []string) int {
	n := 0
	for _, c := range cmds {
		if strings.Contains(c, "config state marker") {
			n++
		}
	}
	return n
}

// TestWorktrunkInitWiresClaudeHooksIdempotently: every start rewrites the
// marker hooks, so a version bump that changes a command replaces it instead
// of stacking a second copy, and hooks other tools own survive the merge.
func TestWorktrunkInitWiresClaudeHooksIdempotently(t *testing.T) {
	f := newWorktrunkFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	writeFile(t, settings, `{
  "model": "opus",
  "hooks": {
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "other-tool prompt"}]},
      {"hooks": [{"type": "command", "command": "/usr/local/bin/wt -C \"$CLAUDE_PROJECT_DIR\" config state marker set stale || true"}]}
    ]
  }
}`)

	f.run(t)
	f.run(t)

	hooks := readHooks(t, settings)
	for event, want := range map[string]string{
		"UserPromptSubmit":  "config state marker set 🤖",
		"Notification":      "config state marker set 💬",
		"PreToolUse":        "config state marker set 💬",
		"PermissionRequest": "config state marker set 💬",
		"Stop":              "config state marker set 💬",
		"SessionEnd":        "config state marker clear",
	} {
		cmds := commands(hooks, event)
		if countMarkers(cmds) != 1 {
			t.Errorf("%s: %d marker hooks, want exactly 1: %q", event, countMarkers(cmds), cmds)
			continue
		}
		found := false
		for _, c := range cmds {
			if strings.Contains(c, want) {
				found = true
				if !strings.HasPrefix(c, `/usr/local/bin/wt -C "$CLAUDE_PROJECT_DIR" `) {
					t.Errorf("%s: %q does not target the session's project dir", event, c)
				}
			}
		}
		if !found {
			t.Errorf("%s: no %q hook in %q", event, want, cmds)
		}
	}
	if cmds := commands(hooks, "UserPromptSubmit"); len(cmds) != 2 || cmds[0] != "other-tool prompt" {
		t.Errorf("UserPromptSubmit = %q, want the other tool's hook kept ahead of the marker", cmds)
	}
	for _, g := range hooks["PreToolUse"] {
		if countMarkers(commandsOf(g)) > 0 && (g.Matcher == nil || *g.Matcher != "AskUserQuestion") {
			t.Errorf("PreToolUse marker is not scoped to AskUserQuestion")
		}
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), `"model": "opus"`) {
		t.Errorf("settings.json lost unrelated keys:\n%s", b)
	}
}

func commandsOf(g hookGroup) []string {
	var out []string
	for _, h := range g.Hooks {
		out = append(out, h.Command)
	}
	return out
}

// TestWorktrunkInitLeavesUnparseableSettingsAlone: a settings.json jq cannot
// read is the user's to fix; replacing it with a fresh document would drop
// everything in it.
func TestWorktrunkInitLeavesUnparseableSettingsAlone(t *testing.T) {
	f := newWorktrunkFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	const broken = "{ \"model\": \"opus\",\n"
	writeFile(t, settings, broken)

	out := f.run(t)

	b, _ := os.ReadFile(settings)
	if string(b) != broken {
		t.Errorf("settings.json was rewritten:\n%s", b)
	}
	if !strings.Contains(out, "non-fatal") {
		t.Errorf("no non-fatal notice for the skipped Claude wiring:\n%s", out)
	}
}

// TestWorktrunkInitLeavesCodexHooksAlone: Codex's markers ship in the image's
// system layer, so the script writes nothing into ~/.codex/hooks.json.
func TestWorktrunkInitLeavesCodexHooksAlone(t *testing.T) {
	f := newWorktrunkFixture(t)
	hooksFile := filepath.Join(f.home, ".codex", "hooks.json")
	const body = `{"hooks":{"PreToolUse":[{"matcher":"^Bash$","hooks":[{"type":"command","command":"echo mine"}]}]}}`
	writeFile(t, hooksFile, body)

	f.run(t)

	if b, err := os.ReadFile(hooksFile); err != nil || string(b) != body {
		t.Errorf("hooks.json changed: %v\n%s", err, b)
	}
}

func TestWorktrunkInitWiresPi(t *testing.T) {
	f := newWorktrunkFixture(t)
	f.run(t)
	b, err := os.ReadFile(filepath.Join(f.home, ".pi", "agent", "extensions", "worktrunk.ts"))
	if err != nil || !strings.Contains(string(b), "worktrunk pi fixture") {
		t.Fatalf("pi extension not installed from wt: %v %q", err, b)
	}
}

// TestWorktrunkInitInstallsSkillIntoBothRoots: Claude reads its own skills
// dir, Codex and pi the cross-agent one; a stale copy is replaced whole.
func TestWorktrunkInitInstallsSkillIntoBothRoots(t *testing.T) {
	f := newWorktrunkFixture(t)
	stale := filepath.Join(f.home, ".claude", "skills", "worktrunk", "reference", "removed.md")
	writeFile(t, stale, "gone upstream\n")

	f.run(t)

	for _, root := range []string{
		filepath.Join(f.home, ".claude", "skills"),
		filepath.Join(f.home, ".agents", "skills"),
	} {
		for _, rel := range []string{"SKILL.md", "reference/config.md"} {
			if _, err := os.Stat(filepath.Join(root, "worktrunk", rel)); err != nil {
				t.Errorf("%s: %v", root, err)
			}
		}
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale skill file survived the refresh: %v", err)
	}
}

// TestWorktrunkInitTakesTheSharedLocks: settings.json has other init.d
// writers running in parallel.
func TestWorktrunkInitTakesTheSharedLocks(t *testing.T) {
	s := readAsset(t, "init.d/62-worktrunk.sh")
	for _, want := range []string{
		"toolbox_with_claude_settings_lock _wt_wire_claude",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("%s is missing %q", worktrunkInitScript, want)
		}
	}
}

// TestWorktrunkInitWiresMarkersOnly: upstream's plugin also auto-approves
// entering worktrees and reroutes Claude's worktree isolation through wt.
// Those change agent behaviour, which is why ADR 0018 keeps the plugin opt-in;
// the image wires the markers alone.
func TestWorktrunkInitWiresMarkersOnly(t *testing.T) {
	s := readAsset(t, "init.d/62-worktrunk.sh")
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, banned := range []string{"approve-enter-worktree", "WorktreeCreate", "WorktreeRemove"} {
			if strings.Contains(line, banned) {
				t.Errorf("%s wires %q: %s", worktrunkInitScript, banned, line)
			}
		}
	}
}

// TestWorktrunkInitKeepsHookPositionsStable: a marker group is rewritten where
// it already sits, so a restart does not reorder the file; a marker hook on an
// event the current wiring no longer uses is dropped; and the file keeps its
// mode.
func TestWorktrunkInitKeepsHookPositionsStable(t *testing.T) {
	f := newWorktrunkFixture(t)
	hooksFile := filepath.Join(f.home, ".claude", "settings.json")
	writeFile(t, hooksFile, `{"hooks":{
  "Stop":[
    {"hooks":[{"type":"command","command":"/usr/local/bin/wt config state marker set old || true"}]},
    {"hooks":[{"type":"command","command":"herdr stop"}]}
  ],
  "SubagentStop":[
    {"hooks":[{"type":"command","command":"/usr/local/bin/wt config state marker set old || true"}]}
  ]
}}`)
	if err := os.Chmod(hooksFile, 0o644); err != nil {
		t.Fatal(err)
	}

	f.run(t)

	hooks := readHooks(t, hooksFile)
	cmds := commands(hooks, "Stop")
	if len(cmds) != 2 || !strings.Contains(cmds[0], "config state marker set 💬") || cmds[1] != "herdr stop" {
		t.Errorf("Stop = %q, want the marker rewritten in first position, Herdr's hook after it", cmds)
	}
	if n := countMarkers(commands(hooks, "SubagentStop")); n != 0 {
		t.Errorf("stale SubagentStop marker hook kept (%d)", n)
	}
	info, err := os.Stat(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("hooks.json mode = %o, want 644 kept", info.Mode().Perm())
	}
}
