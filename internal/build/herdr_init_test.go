package build

import (
	"strings"
	"testing"
)

// herdrInitScript is the embedded init.d member under test. The script is a
// static shell asset no Go code reads, so only a test over the embedded bytes
// can hold its invariants — same technique as TestWorkspaceInstallRefreshGate.
const herdrInitScript = "assets/init.d/61-herdr.sh"

func readHerdrInit(t *testing.T) string {
	t.Helper()
	b, err := Assets.ReadFile(herdrInitScript)
	if err != nil {
		t.Fatalf("read %s: %v", herdrInitScript, err)
	}
	return string(b)
}

// TestHerdrInitInstallsBothSkillPaths pins the dual-install and the two ways of
// getting its roots wrong, both of which fail silently.
//
// Claude Code reads only ~/.claude/skills, while every other bundled agent
// reads the cross-agent ~/.agents/skills, so one pass leaves the skill
// invisible to the other side. The split is two-way whatever the agent count —
// TestHerdrInitWiresPi holds pi's half of the ~/.agents gate. The roots must
// come from CLAUDE_CONFIG_DIR / CODEX_HOME with
// the ~ fallback (the Dockerfile sets both, and herdr honours them): a bare
// $HOME path would probe a directory the agent never reads, skipping an install
// that would have landed.
//
// And ~/.agents must be CREATED, not gated on. Unlike ~/.claude it is no bind
// mount — it is container-local, and the only other script that creates it is
// 60-glab.sh, which entrypoint.sh runs backgrounded in parallel with this one.
// Gate on the directory and the Codex skill lands or not depending on which
// script won the race.
func TestHerdrInitInstallsBothSkillPaths(t *testing.T) {
	s := readHerdrInit(t)

	for _, want := range []string{
		`_herdr_claude_dir="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"`,
		`_herdr_codex_dir="${CODEX_HOME:-$HOME/.codex}"`,
		`_herdr_install_skill "$_herdr_claude_dir"`,
		`_herdr_install_skill "$HOME/.agents"`,
		// Atomic per target: a Claude skill dir is one host mount shared by
		// every toolbox container, so a concurrent start must never observe a
		// half-written SKILL.md.
		`tmp=$(mktemp "$dir/.SKILL.md.XXXXXX")`,
		`mv -f "$tmp" "$dir/SKILL.md"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("%s is missing %q", herdrInitScript, want)
		}
	}

	if strings.Contains(s, `[ -d "$HOME/.agents" ]`) {
		t.Errorf("%s gates on $HOME/.agents; it is container-local and must be created (mkdir -p), "+
			"or the Codex half races 60-glab.sh", herdrInitScript)
	}
}

// TestHerdrInitLocksClaudeSettings pins the settings.json lock.
//
// `herdr integration install claude` registers its hook in
// ~/.claude/settings.json, which makes this script the FOURTH concurrent writer
// of that one file — 10-rtk.sh, 35-statusline.sh and 65-atuin.sh are the other
// three, and all of them hold .claude-settings.lock. init.d runs backgrounded in
// parallel, so an unlocked read-modify-write here loses either herdr's own hook
// or another writer's patch, silently and non-deterministically.
func TestHerdrInitLocksClaudeSettings(t *testing.T) {
	s := readHerdrInit(t)

	lock := `$HOME/.toolbox-state/.claude-settings.lock`
	if !strings.Contains(s, lock) {
		t.Fatalf("%s does not take %s; it writes settings.json via `herdr integration install claude`", herdrInitScript, lock)
	}

	// The install must sit INSIDE the flock subshell, not merely somewhere in a
	// script that happens to mention the lock.
	guarded := "flock 200\n        _herdr_install_integration claude"
	if !strings.Contains(s, guarded) {
		t.Errorf("%s: `_herdr_install_integration claude` is not inside the flock subshell (want %q)", herdrInitScript, guarded)
	}
}

// TestHerdrInitWiresPi pins pi as a first-class agent here, not merely a
// bundled binary. config.SupportedAgents and `toolbox worktree --agent pi`
// already declare it one, so a pi shell that herdr cannot drive — or that
// herdr cannot follow back to its session — is the wiring missing, not the
// agent.
//
// Three things, each failing silently on its own:
//
//   - the hook gate is `$HOME/.pi`, the bind mount. NOT ~/.pi/agent: pi creates
//     that itself on first run, so gating there would install for a user who
//     never mounted pi state, and the install would vanish on `toolbox stop`.
//     It guards the hook ALONE — ~/.agents is container-local, so a pi shell
//     reads the skill whether or not pi state is mounted, and `_herdr_pi` is
//     therefore binary presence only, exactly like `_herdr_codex`.
//   - pi shares ~/.agents/skills with Codex, so the skill half must fire for
//     codex OR pi. Gate it on codex alone and a pi-only shell gets no skill.
//   - the integration install must reach the early exit. A script that exits
//     before it on "neither claude nor codex" wires nothing for pi at all.
//
// And ~/.pi/agent must be CREATED before the install. `herdr integration
// install pi` refuses a ~/.pi that pi has never populated ("pi extension
// directory not found"), while 10-rtk.sh and 65-atuin.sh create the tree
// themselves — and init.d runs backgrounded in parallel, so on a fresh ~/.pi
// mount herdr loses the race and prints a failure for a mount that is perfectly
// good.
func TestHerdrInitWiresPi(t *testing.T) {
	s := readHerdrInit(t)

	for _, want := range []string{
		// Binary presence only, so the skill half is not gated on the mount.
		"if command -v pi >/dev/null 2>&1; then\n    _herdr_pi=1",
		// The mount gate sits on the hook install, and only there.
		`if [ -n "$_herdr_pi" ] && [ -d "$HOME/.pi" ]; then`,
		`mkdir -p "$HOME/.pi/agent"`,
		`[ -n "${_herdr_claude}${_herdr_codex}${_herdr_pi}" ]`,
		`if [ -n "$_herdr_codex" ] || [ -n "$_herdr_pi" ]; then`,
		`_herdr_install_integration pi`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("%s is missing %q", herdrInitScript, want)
		}
	}

	// No lock: `herdr integration install pi` writes its own
	// ~/.pi/agent/extensions/herdr-agent-state.ts, disjoint from the rtk and
	// atuin extensions the other init.d members drop in the same directory.
	// Taking .claude-settings.lock here would serialise pi behind a file it
	// never touches — and settings.json, the one file that does need the lock,
	// has exactly one writer in this script (the claude install, pinned by
	// TestHerdrInitLocksClaudeSettings). Counting the locked sites catches a
	// second one wherever it is indented; matching a literal flock+pi pair
	// would only catch it at one exact indent.
	if n := strings.Count(s, "flock 200"); n != 1 {
		t.Errorf("%s takes flock %d times, want 1 (only the claude install writes a shared file)", herdrInitScript, n)
	}
}
