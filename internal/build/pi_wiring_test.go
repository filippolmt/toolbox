package build

import (
	"strings"
	"testing"
)

// TestInitDWiresPiEverywhereItWiresCodex pins the third agent into the two
// init.d members that wire agents but have no test of their own. herdr's half
// lives in TestHerdrInitWiresPi; this covers rtk and atuin.
//
// The failure it exists to catch is silent by construction. pi is a first-class
// agent — internal/catalog bundles it, config.SupportedAgents and `toolbox
// worktree --agent pi` declare it one — and none of that makes rtk's Bash
// rewrite hook or atuin's history capture see it. Only these scripts do. Drop
// a block and pi still launches, still resumes, and quietly runs unwired.
//
// The gate is the ~/.pi bind mount, never ~/.pi/agent: pi creates that itself
// on first run whether or not the state is mounted, so a hook installed behind
// the wrong gate is thrown away by the next `toolbox stop` — and in atuin's
// case the marker recording the install, which lives in the state volume,
// outlives it, so the retry never fires.
func TestInitDWiresPiEverywhereItWiresCodex(t *testing.T) {
	for _, tc := range []struct {
		script string
		wants  []string
	}{
		{
			script: "assets/init.d/10-rtk.sh",
			wants: []string{
				`if command -v pi >/dev/null 2>&1 && [ -d "$HOME/.pi" ]; then`,
				// --auto-patch + </dev/null: entrypoint has no terminal, and a
				// TTY prompt here deadlocks the shell start.
				`rtk init -g --agent pi --auto-patch </dev/null`,
			},
		},
		{
			script: "assets/init.d/65-atuin.sh",
			wants: []string{
				`_pi_marker="$_atuin_hooks_dir/pi-${_atuin_key}"`,
				`[ ! -f "$_pi_marker" ] && command -v pi >/dev/null 2>&1 && [ -d "$HOME/.pi" ]`,
				`atuin hook install pi`,
			},
		},
	} {
		b, err := Assets.ReadFile(tc.script)
		if err != nil {
			t.Fatalf("read %s: %v", tc.script, err)
		}
		s := string(b)
		for _, want := range tc.wants {
			if !strings.Contains(s, want) {
				t.Errorf("%s is missing %q", tc.script, want)
			}
		}
		// No lock. Each agent gets its own file under ~/.pi/agent/extensions/
		// (rtk.ts, atuin.ts, herdr-agent-state.ts), so there is no shared
		// writer to serialise — taking .claude-settings.lock would only
		// serialise pi behind a file it never touches.
		if strings.Contains(s, "flock 200\n        atuin hook install pi") ||
			strings.Contains(s, "flock 200\n        rtk init -g --agent pi") {
			t.Errorf("%s locks the pi install; it writes no shared file", tc.script)
		}
	}
}

// TestPerRepoInstallersRefreshWhatPiReads extends the Workspace Install Refresh
// family to pi's skill roots. graphify and playwright-cli each install a
// per-agent copy of their skill — graphify into the repo's
// .pi/agent/skills/graphify/, playwright-cli into the cross-agent
// .agents/skills/playwright-cli/ that codex and pi both read — and neither is
// written by the claude install the members already refresh. Without a second
// pass the two agents see a skill frozen at whatever version first installed
// it, silently, exactly like the claude half would without its own gate.
//
// Each agent carries its OWN stamp. A shared one would let the claude install
// satisfy pi's gate: the stamp would already equal the bundled version, so the
// pi copy would never be written at all. And each keeps its own artefact half,
// so deleting one agent's skill self-heals without touching the other's.
func TestPerRepoInstallersRefreshWhatPiReads(t *testing.T) {
	for _, tc := range []struct {
		script string
		wants  []string
	}{
		{
			script: "assets/init.d/30-graphify.sh",
			wants: []string{
				// Gate is the ~/.pi bind mount, never ~/.pi/agent — same
				// reasoning as TestInitDWiresPiEverywhereItWiresCodex.
				`if command -v pi >/dev/null 2>&1 && [ -d "$HOME/.pi" ]; then`,
				`toolbox_install_refresh graphify-pi "$PWD/.pi/agent/skills/graphify/SKILL.md" "$_gfy_ver"`,
				`graphify install --project --platform pi`,
			},
		},
		{
			script: "assets/init.d/40-playwright-cli.sh",
			wants: []string{
				// One pass for both readers of .agents/skills, gated on either
				// agent being present — the same shape 61-herdr.sh uses for the
				// home-directory copy.
				`if command -v codex >/dev/null 2>&1 || command -v pi >/dev/null 2>&1; then`,
				`toolbox_install_refresh playwright-cli-agents "$PWD/.agents/skills/playwright-cli/SKILL.md" "$_pwc_ver"`,
				`playwright-cli install --skills agents`,
			},
		},
	} {
		b, err := Assets.ReadFile(tc.script)
		if err != nil {
			t.Fatalf("read %s: %v", tc.script, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s is missing %q", tc.script, want)
			}
		}
	}
}
