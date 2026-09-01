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
// Claude Code reads only ~/.claude/skills, Codex only ~/.agents/skills, so one
// pass leaves the skill invisible to the other agent. The roots must come from
// CLAUDE_CONFIG_DIR / CODEX_HOME with the ~ fallback (the Dockerfile sets both,
// and herdr honours them): a bare $HOME path would probe a directory the agent
// never reads, skipping an install that would have landed.
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
