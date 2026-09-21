package build

import (
	"io/fs"
	"strings"
	"testing"
)

// refreshStampRoot is the toolbox-owned stamp dir the library keys every
// (workspace, pass) version stamp under. It lives outside the workspace on
// purpose — a stamp next to the installation would be the very churn the gate
// exists to remove.
// See docs/adr/0001-workspace-install-refresh.md.
const refreshStampRoot = `$HOME/.toolbox-state/install-refresh`

// refreshLibSource is the line that makes a member a member. Sourced by
// absolute path, never through PATH.
const refreshLibSource = `. /usr/local/lib/toolbox/install-refresh-lib.sh`

// refreshLib is where the gate lives now: one implementation, sourced by every
// member. Before it, each member carried its own copy of the same twenty
// lines, and the stamp path, the guard and the failure message could drift
// apart one script at a time.
const refreshLib = "bin/install-refresh-lib.sh"

// refreshCall is how a member asks for a refresh. The library takes the stamp
// key, the artefact and the version, then the install command — so a call site
// carries the four things that actually differ between passes and nothing else.
const refreshCall = "toolbox_install_refresh "

// gateShape spells out the one condition the library must carry: the emptiness
// guard scopes to the version half ALONE. Both ways of getting this wrong are
// live failure modes, which is why the shape is pinned verbatim rather than
// grepped loosely. Drop the guard and an unreadable version reads as "differs
// from the stamp", reopening the gate on every shell — the exact churn the gate
// removes. Stretch it over the whole condition and a deleted install stops
// self-healing, silently, because the artefact half never gets evaluated.
const gateShape = `{ [ -n "$ver" ] && [ "$stamped" != "$ver" ]; } || [ ! -f "$artefact" ] || return 1`

// gateEndMarker closes the gated block. `graphify hook install` must sit after
// it: it writes .git/hooks/, which is never committed and therefore absent from
// a fresh clone, so gating it on a version stamp would leave the graph stale.
const gateEndMarker = "# --- end Workspace Install Refresh gate ---"

// TestWorkspaceInstallRefreshGate pins the static invariants of the Workspace
// Install Refresh family: each of the three per-repo installers re-runs only
// when the bundled tool version moved away from the stamp OR the artefact the
// installer is supposed to have written went missing. Ungated, every image
// upgrade that bumps a bundled tool rewrites tracked workspace files
// (CLAUDE.md, .claude/settings.json, .claude/skills/) and hands the user a
// dirty tree. The scripts are static shell assets that no Go code reads, so
// only a test over the embedded bytes can hold the family together — same
// technique as TestShimPathsMatchGoConstants.
func TestWorkspaceInstallRefreshGate(t *testing.T) {
	cases := []struct {
		script  string
		needles []string
		absent  []string
	}{
		{
			script: "init.d/30-graphify.sh",
			needles: []string{
				// Version half of the gate: bundled version vs. stamp, both
				// handed to the one implementation in the library.
				refreshLibSource,
				`graphify --version`,
				refreshCall + `graphify "$PWD/.claude/skills/graphify/SKILL.md" "$_gfy_ver"`,
				`graphify install --project --platform claude`,
				// Matcher normalisation, only from the known upstream values so
				// a hand-edited hook survives (Q5/Q13).
				`(.hooks.PreToolUse[]? | select(.matcher == $wide) | .matcher) = $new`,
				`narrow("Bash|Grep"; "Grep") | narrow("Read|Glob"; "Glob")`,
				// Finishing upstream's own drop filter, inseparable from the
				// normalisation above: renaming the matchers is exactly what
				// blinds `graphify install`'s "drop what I wrote last time"
				// pass, which keys on (wide literal) AND (entry mentions
				// graphify). Without this, every graphify upgrade appends a
				// second Grep/Glob pair — identical, or worse, differing only
				// in payload, which no verbatim dedup would ever collapse.
				`def gfy: tostring | contains("graphify")`,
				`.hooks.PreToolUse |= map(select((.matcher == $new and gfy) | not))`,
				// The guard that keeps a failed install from leaving the
				// workspace hookless: nothing appended, nothing dropped.
				`if any(.hooks.PreToolUse[]?; .matcher == $wide and gfy)`,
				// graphify install leaves this behind on every run (Q11).
				`rm -f "$PWD/.claude/settings.json.graphify-bak"`,
			},
		},
		{
			script: "init.d/31-codegraph.sh",
			needles: []string{
				refreshLibSource,
				`codegraph --version`,
				refreshCall + `codegraph "$PWD/.mcp.json" "$_cg_ver"`,
				// Upstream's own "rewrite what previous installs configured"
				// semantics (Q8/Q18) — narrower than a full local install.
				`codegraph install --refresh`,
			},
			absent: []string{`--target=claude --location=local --yes`},
		},
		{
			script: "init.d/40-playwright-cli.sh",
			needles: []string{
				refreshLibSource,
				`playwright-cli --version`,
				refreshCall + `playwright-cli "$PWD/.claude/skills/playwright-cli/SKILL.md" "$_pwc_ver"`,
			},
		},
	}

	for _, tc := range cases {
		body := readAsset(t, tc.script)
		for _, needle := range tc.needles {
			if !strings.Contains(body, needle) {
				t.Errorf("%s: missing %q — Workspace Install Refresh gate drifted", tc.script, needle)
			}
		}
		for _, gone := range tc.absent {
			if strings.Contains(body, gone) {
				t.Errorf("%s: still contains %q — the ungated install was meant to be replaced", tc.script, gone)
			}
		}
	}
}

// TestWorkspaceInstallRefreshGateLivesInOneplace is the anti-duplication half
// of the family. Every member used to spell the stamp path, the -n guard, the
// stamp write and the failure message out for itself, which is how two of them
// ended up with subtly different wording and how a third could have drifted
// without any test noticing. The gate now lives in the library and nowhere
// else: a member that hand-rolls a stamp path has forked it back apart.
func TestWorkspaceInstallRefreshGateLivesInOneplace(t *testing.T) {
	lib := readAsset(t, refreshLib)
	for _, needle := range []string{refreshStampRoot, gateShape, "toolbox_install_refresh()"} {
		if !strings.Contains(lib, needle) {
			t.Errorf("%s: missing %q — the shared gate drifted", refreshLib, needle)
		}
	}

	for _, member := range []string{
		"init.d/30-graphify.sh",
		"init.d/31-codegraph.sh",
		"init.d/40-playwright-cli.sh",
	} {
		body := readAsset(t, member)
		if !strings.Contains(body, refreshLibSource) {
			t.Errorf("%s: does not source %s", member, refreshLib)
		}
		if strings.Contains(body, refreshStampRoot) {
			t.Errorf("%s: builds a stamp path of its own instead of calling the library", member)
		}
	}
}

// TestGraphifyHookInstallOutsideGate holds the one deliberate exception in the
// family: `graphify hook install` writes .git/hooks/, which is never committed,
// so a fresh clone (or one made inside the container) has no hook at all. Put
// it inside the version gate and the graph silently stops rebuilding on commit
// for every workspace whose stamp is already current.
func TestGraphifyHookInstallOutsideGate(t *testing.T) {
	body := readAsset(t, "init.d/30-graphify.sh")

	end := strings.Index(body, gateEndMarker)
	if end < 0 {
		t.Fatalf("30-graphify.sh: missing %q — the test cannot tell gated code from ungated code without it", gateEndMarker)
	}
	hook := strings.Index(body, "graphify hook install >")
	if hook < 0 {
		t.Fatal("30-graphify.sh: `graphify hook install` invocation is gone")
	}
	if hook < end {
		t.Error("30-graphify.sh: `graphify hook install` runs inside the version gate — it writes .git/hooks/, absent from a fresh clone, so it must run on every shell")
	}
}

func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(Assets, AssetDir+"/"+name)
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(b)
}

// finalStage returns the text of the Dockerfile's final stage — the RUN tail and
// the COPY block ADR 0002's layer ordering is about.
//
// The anchor is the LAST `FROM` in the file, which is the final stage by
// definition. It used to be the last `FROM node:`, and that broke silently the
// day the node image was named once as `node-base`: every stage then derived
// from the alias, the only literal `node:` left was the alias declaration near
// the top, and the helper handed every caller the whole file from there down.
// Nothing went red — the tests that read this all ask "does the final stage
// contain X", and a superset answers yes.
func finalStage(t *testing.T) string {
	t.Helper()
	body := readAsset(t, "Dockerfile")
	from := strings.LastIndex(body, "\nFROM ")
	if from < 0 {
		t.Fatal("Dockerfile: cannot locate the final stage — no FROM found")
	}
	return body[from+1:]
}
