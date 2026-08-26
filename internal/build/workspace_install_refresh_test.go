package build

import (
	"io/fs"
	"strings"
	"testing"
)

// refreshStampRoot is the toolbox-owned stamp dir every Workspace Install
// Refresh member keys its (workspace, tool) version stamp under. It lives
// outside the workspace on purpose — a stamp next to the installation would be
// the very churn the gate exists to remove.
// See docs/adr/0001-workspace-install-refresh.md.
const refreshStampRoot = `$HOME/.toolbox-state/install-refresh`

// gateShape spells out the one condition every member must carry: the emptiness
// guard scopes to the version half ALONE. Both ways of getting this wrong are
// live failure modes, which is why the shape is pinned verbatim rather than
// grepped loosely. Drop the guard and an unreadable version reads as "differs
// from the stamp", reopening the gate on every shell — the exact churn the gate
// removes. Stretch it over the whole condition and a deleted install stops
// self-healing, silently, because the artefact half never gets evaluated.
func gateShape(verVar, stampedVar string) string {
	return `{ [ -n "$` + verVar + `" ] && [ "$` + stampedVar + `" != "$` + verVar + `" ]; } || [ ! -f `
}

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
				// Version half of the gate: bundled version vs. stamp. The -n
				// guard must scope to this half alone — see gateShape.
				refreshStampRoot,
				`graphify --version`,
				gateShape("_gfy_ver", "_gfy_stamped"),
				// Artefact half: a deleted skill still self-heals.
				`[ ! -f "$PWD/.claude/skills/graphify/SKILL.md" ]`,
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
				refreshStampRoot,
				`codegraph --version`,
				gateShape("_cg_ver", "_cg_stamped"),
				`[ ! -f "$PWD/.mcp.json" ]`,
				// Upstream's own "rewrite what previous installs configured"
				// semantics (Q8/Q18) — narrower than a full local install.
				`codegraph install --refresh`,
			},
			absent: []string{`--target=claude --location=local --yes`},
		},
		{
			script: "init.d/40-playwright-cli.sh",
			needles: []string{
				refreshStampRoot,
				`playwright-cli --version`,
				gateShape("_pwc_ver", "_pwc_stamped"),
				`[ ! -f "$PWD/.claude/skills/playwright-cli/SKILL.md" ]`,
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
// the COPY block ADR 0002's layer ordering is about. The last `FROM node:` is the
// anchor: fetch-codegraph starts from the same image and has to stay out of the
// match, so this cannot be the first occurrence.
func finalStage(t *testing.T) string {
	t.Helper()
	body := readAsset(t, "Dockerfile")
	from := strings.LastIndex(body, "\nFROM node:")
	if from < 0 {
		t.Fatal("Dockerfile: cannot locate the final `FROM node:` stage")
	}
	return body[from+1:]
}
