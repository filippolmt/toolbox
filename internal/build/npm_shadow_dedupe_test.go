package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// npmShadowDedupeScript is the embedded init.d member under test. It is a
// static shell asset no Go code reads, so only a test that RUNS it can hold
// the decision it makes — same technique as TestGlabFlockShimNeverFailsClosed.
const npmShadowDedupeScript = "assets/init.d/15-npm-shadow-dedupe.sh"

// npmShadowFixture lays out the two node_modules trees the script compares:
// the npm-global volume (which PATH puts first) and the image's baked
// /usr/local tree. Both roots are passed to the script as arguments so the
// test never needs to write under /usr/local.
type npmShadowFixture struct {
	root   string
	prefix string
	baked  string
}

func newNpmShadowFixture(t *testing.T) *npmShadowFixture {
	t.Helper()
	root := t.TempDir()
	f := &npmShadowFixture{
		root:   root,
		prefix: filepath.Join(root, "npm-global"),
		baked:  filepath.Join(root, "usr-local", "lib", "node_modules"),
	}
	if err := os.MkdirAll(filepath.Join(f.prefix, "lib", "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir prefix: %v", err)
	}
	if err := os.MkdirAll(f.baked, 0o755); err != nil {
		t.Fatalf("mkdir baked: %v", err)
	}
	return f
}

// shQuote wraps s for safe interpolation into the generated shell stub.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// writeNpmStub stands in for the real npm, which the Go toolchain image does
// not ship. It answers the one call the script makes, `npm rm -g <name>`:
// removal goes through npm because it also unlinks the bin symlinks a plain
// `rm -rf` would leave dangling, which the stub reproduces.
func (f *npmShadowFixture) writeNpmStub(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(f.root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	modules := shQuote(filepath.Join(f.prefix, "lib", "node_modules"))
	stub := "#!/bin/sh\n" +
		"[ \"$1\" = rm ] || exit 0\n" +
		"rm -rf " + modules + "/\"$3\"\n" +
		"for l in " + shQuote(filepath.Join(f.prefix, "bin")) + "/*; do\n" +
		"  [ -L \"$l\" ] && [ ! -e \"$l\" ] && rm -f \"$l\"\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(stub), 0o755); err != nil {
		t.Fatalf("write npm stub: %v", err)
	}
	return bin
}

// install writes a package.json into one of the two node_modules roots. An
// empty version stands for a package.json the script cannot read a version
// out of.
func (f *npmShadowFixture) install(t *testing.T, nodeModules, name, version string) {
	t.Helper()
	dir := filepath.Join(nodeModules, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	body := `{"name":"` + name + `","description":"fixture"}`
	if version != "" {
		body = `{"name":"` + name + `","version":"` + version + `","description":"fixture"}`
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
}

// volume installs into the npm-global tree the way `npm i -g <name>` does: the
// package, plus the bin symlink under prefix/bin that puts it on PATH. That
// link is what makes it a package someone asked for — and the only way it can
// shadow anything.
func (f *npmShadowFixture) volume(t *testing.T, name, version string) {
	t.Helper()
	modules := filepath.Join(f.prefix, "lib", "node_modules")
	f.install(t, modules, name, version)

	entry := filepath.Join(modules, name, "cli.js")
	if err := os.WriteFile(entry, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", entry, err)
	}
	bin := filepath.Join(f.prefix, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", bin, err)
	}
	link := filepath.Join(bin, filepath.Base(name))
	if err := os.Symlink(entry, link); err != nil {
		t.Fatalf("symlink %s: %v", link, err)
	}
}

// hoisted installs into the same tree with NO bin symlink: npm flattens a
// global package's dependencies into that very directory and links bins only
// for the package it was asked to install, so the missing link is what marks
// this one as somebody's dependency rather than an install of its own.
func (f *npmShadowFixture) hoisted(t *testing.T, name, version string) {
	t.Helper()
	f.install(t, filepath.Join(f.prefix, "lib", "node_modules"), name, version)
}

func (f *npmShadowFixture) image(t *testing.T, name, version string) {
	t.Helper()
	f.install(t, f.baked, name, version)
}

// run extracts the embedded script and executes it against the fixture roots.
func (f *npmShadowFixture) run(t *testing.T) {
	t.Helper()
	bin := f.writeNpmStub(t)
	b, err := Assets.ReadFile(npmShadowDedupeScript)
	if err != nil {
		t.Fatalf("read %s: %v", npmShadowDedupeScript, err)
	}
	script := filepath.Join(f.root, "dedupe.sh")
	if err := os.WriteFile(script, b, 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	cmd := exec.Command("/bin/bash", script, f.prefix, f.baked)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run script: %v\n%s", err, out)
	}
}

func (f *npmShadowFixture) present(t *testing.T, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(f.prefix, "lib", "node_modules", name, "package.json"))
	return err == nil
}

// assertPresence runs the script and checks each package against want.
func (f *npmShadowFixture) assertPresence(t *testing.T, want map[string]bool) {
	t.Helper()
	f.run(t)
	for name, wantPresent := range want {
		if got := f.present(t, name); got != wantPresent {
			t.Errorf("%s present = %v, want %v", name, got, wantPresent)
		}
	}
}

// TestNpmShadowDedupeDropsOnlyTheCopiesTheImageOvertook pins the whole decision
// the healer makes, because both ways of getting it wrong are silent.
//
// PATH puts ~/.npm-global/bin ahead of /usr/local/bin so the baked agents can
// self-update and keep winning. The cost of that ordering is that a volume copy
// left behind by an older image — or by a hand `npm i -g` — shadows the
// Renovate-bumped /usr/local one for good, and the bump never reaches the user
// (observed with pyright, then again with pi and codegraph).
//
// So the rule is version-directional, not a name list: a volume copy survives
// ONLY while it is strictly newer than the baked one. Remove less and the
// shadow stays; remove more — every baked name, unconditionally — and the next
// `claude`/`codex`/`pi` self-update gets rolled back to the image pin on every
// single shell start.
func TestNpmShadowDedupeDropsOnlyTheCopiesTheImageOvertook(t *testing.T) {
	f := newNpmShadowFixture(t)

	// Stale shadow: the image moved past the volume. Must go.
	f.volume(t, "@earendil-works/pi-coding-agent", "0.75.5")
	f.image(t, "@earendil-works/pi-coding-agent", "0.86.1")
	// Self-update ahead of the image pin. Must stay.
	f.volume(t, "@anthropic-ai/claude-code", "2.1.0")
	f.image(t, "@anthropic-ai/claude-code", "2.0.9")
	// Same version on both sides: the volume copy is pure duplication.
	f.volume(t, "pyright", "1.1.410")
	f.image(t, "pyright", "1.1.410")
	// Never baked — the user's own tool, no shadow to heal.
	f.volume(t, "@shopify/cli", "3.94.3")

	f.assertPresence(t, map[string]bool{
		"@earendil-works/pi-coding-agent": false,
		"@anthropic-ai/claude-code":       true,
		"pyright":                         false,
		"@shopify/cli":                    true,
	})
}

// TestNpmShadowDedupeRanksAPrereleaseBelowItsRelease pins the comparison
// against the one ordering `sort -V` gets backwards on its own: it reads
// `2.0.0-beta.1` as NEWER than `2.0.0`, because a dash is just another
// separator to it, while semver says a prerelease precedes its release. The
// three baked agents all ship -beta/-rc builds into this very volume, so
// without the fix a stale prerelease shadow is exactly the case that survives
// forever — the one the healer exists to close.
func TestNpmShadowDedupeRanksAPrereleaseBelowItsRelease(t *testing.T) {
	f := newNpmShadowFixture(t)

	// The image shipped the release the volume's prerelease led to. Stale.
	f.volume(t, "@openai/codex", "2.0.0-beta.1")
	f.image(t, "@openai/codex", "2.0.0")
	// A prerelease of a LATER version is genuinely ahead. Must stay.
	f.volume(t, "@anthropic-ai/claude-code", "2.1.0-rc.1")
	f.image(t, "@anthropic-ai/claude-code", "2.0.9")

	f.assertPresence(t, map[string]bool{
		"@openai/codex":             false,
		"@anthropic-ai/claude-code": true,
	})
}

// TestNpmShadowDedupeLeavesHoistedDependenciesAlone pins the scope of the scan.
// npm flattens a global package's dependencies into the same lib/node_modules
// directory as the packages themselves, and a transitive dep re-seeding the
// volume is how the original pyright shadow arrived in the first place.
// Removing one because the image happens to bake the same name breaks the
// package that depends on it — a far worse outcome than the shadow this script
// exists to clear. A global prefix carries no manifest to ask (`npm ls -g
// --depth=0` just enumerates the directory), so the candidate test is the bin
// symlink under prefix/bin: npm writes one only for a package it was asked to
// install, and a package without one cannot shadow anything on PATH anyway.
func TestNpmShadowDedupeLeavesHoistedDependenciesAlone(t *testing.T) {
	f := newNpmShadowFixture(t)

	f.volume(t, "@shopify/cli", "3.94.3")
	// Pulled in as a dependency of the above, and baked too. Not ours to drop.
	f.hoisted(t, "typescript", "5.6.0")
	f.image(t, "typescript", "5.9.0")

	f.assertPresence(t, map[string]bool{
		"@shopify/cli": true,
		"typescript":   true,
	})
}

// TestNpmShadowDedupeKeepsACopyItCannotCompare pins the fail-safe direction of
// the version probe. A package.json whose version the probe cannot read — an
// upstream layout change, a truncated file — must leave the volume copy alone.
// The alternative reading, "unknown means stale", deletes a self-update that
// was ahead of the image on the strength of a failed parse, and does it again
// on every shell start.
func TestNpmShadowDedupeKeepsACopyItCannotCompare(t *testing.T) {
	f := newNpmShadowFixture(t)

	f.volume(t, "@openai/codex", "")
	f.image(t, "@openai/codex", "0.50.0")
	f.volume(t, "@colbymchenry/codegraph", "1.5.0")
	f.image(t, "@colbymchenry/codegraph", "")

	f.assertPresence(t, map[string]bool{
		"@openai/codex":           true,
		"@colbymchenry/codegraph": true,
	})
}

// TestNpmShadowDedupeUnlinksTheBinItRemoves closes the loop the bin symlink
// opened: removal must go through `npm rm -g`, which drops the link along with
// the tree. A plain `rm -rf` of the package would leave a dangling symlink
// first on PATH, turning a stale-version shadow into "command not found" —
// strictly worse than the state the healer was called to fix.
func TestNpmShadowDedupeUnlinksTheBinItRemoves(t *testing.T) {
	f := newNpmShadowFixture(t)

	f.volume(t, "@earendil-works/pi-coding-agent", "0.75.5")
	f.image(t, "@earendil-works/pi-coding-agent", "0.86.1")

	f.assertPresence(t, map[string]bool{"@earendil-works/pi-coding-agent": false})

	link := filepath.Join(f.prefix, "bin", "pi-coding-agent")
	if _, err := os.Lstat(link); err == nil {
		t.Error("the bin symlink outlived the package it pointed at")
	}
}
