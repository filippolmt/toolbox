package reload_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/reload"
)

// zshrcPath is the in-container half of the reload, relative to this package.
const zshrcPath = "../build/assets/zshrc.sh"

// TestReloadMarkerContract binds the two ends of the capability marker. The
// host injects the name (Go, internal/sessionplan) and the image's zsh function
// reads it — two languages joined by nothing a compiler checks, shipping on two
// separate release pipelines.
//
// The failure a rename would cause is the quiet one this whole guard exists
// for: the function would take the absent variable for an old CLI and refuse
// forever, on every image, with the remedy it names (`brew upgrade`) doing
// nothing at all.
func TestReloadMarkerContract(t *testing.T) {
	raw, err := os.ReadFile(zshrcPath)
	if err != nil {
		t.Fatalf("read zshrc.sh: %v", err)
	}
	zshrc := string(raw)

	if !strings.Contains(zshrc, "${"+reload.MarkerEnv+":-}") {
		t.Errorf("zshrc.sh does not gate on %s", reload.MarkerEnv)
	}
	if !strings.Contains(zshrc, "$"+reload.MarkerEnv+"\"") {
		t.Errorf("zshrc.sh does not write to $%s", reload.MarkerEnv)
	}
	if !strings.Contains(zshrc, "toolbox-reload()") {
		t.Error("zshrc.sh defines no toolbox-reload function")
	}

	// The temporary the writer renames over the marker must be a *sibling* of
	// it, never a name in $PWD: a rename is atomic only within one filesystem,
	// and the state mount and the workspace are two of them. This is the one
	// half of the atomic write no behaviour can pin — a cross-directory rename
	// succeeds anyway on a single-filesystem test host — so the text is the
	// guard. Everything downstream of it is pinned by execution, in
	// TestReloadMarkerWriterMatchesGo and the tests beside it.
	if !strings.Contains(zshrc, `local tmp="${`+reload.MarkerEnv+`}.tmp.$$"`) {
		t.Errorf("the reload marker's temporary is not derived from $%s — an atomic rename needs both on the state mount", reload.MarkerEnv)
	}

	// The banner reads the same variable, and for the same reason: it is the
	// one thing the image can know about the CLI driving it. Without this gate
	// a session under an older CLI is advised to run a command that will
	// refuse — the banner promising what the function then declines.
	if strings.Count(zshrc, "${"+reload.MarkerEnv+":-}") < 2 {
		t.Errorf("the banner does not gate its advice on %s", reload.MarkerEnv)
	}
	if !strings.Contains(zshrc, "toolbox-reload%b to move this session onto it") {
		t.Error("the banner no longer names toolbox-reload as the way to adopt the image")
	}

	// The host-to-host handover must never be spelled inside the image: it
	// travels across the re-exec and is unset before any container env is
	// built. Same prefix as the marker, opposite direction — which is exactly
	// why someone will eventually try to merge them.
	if strings.Contains(zshrc, reload.FromEnv) {
		t.Errorf("zshrc.sh references %s, which never enters a container", reload.FromEnv)
	}
}

// zshFunction returns the body of a function exactly as zshrc.sh ships it,
// from its `name() {` header to the first closing brace at column 0. Running
// the extracted text is what makes the tests below a contract rather than a
// re-implementation: the bytes under test are the bytes the image installs.
//
// Both anchors are pinned to column 0 so a mention inside a comment cannot be
// mistaken for the definition, and a t.Fatalf rather than a silent miss is the
// point: an extraction that quietly matched nothing would leave every test
// below passing over an empty function.
func zshFunction(t *testing.T, zshrc, name string) string {
	t.Helper()
	header := "\n" + name + "() {\n"
	start := strings.Index(zshrc, header)
	if start < 0 {
		t.Fatalf("zshrc.sh defines no %s function at column 0", name)
	}
	body := zshrc[start+len("\n"):]
	end := strings.Index(body, "\n}\n")
	if end < 0 {
		t.Fatalf("%s has no closing brace at column 0 — the extraction heuristic needs updating", name)
	}
	return body[:end+len("\n}\n")]
}

// fixtureContainer names the session the tests below reload. The marker is
// keyed on the container name, so it is spelled once.
const fixtureContainer = "toolbox-proj-deadbeef"

// shippedWriter returns the `toolbox-reload` source as the image installs it.
//
// A missing zsh is a skip rather than a failure — the pinned `golang`
// container the Makefile runs the suite in has no interpreter for it — except
// under $CI, where it is fatal. That asymmetry is the guard on the guard: in
// CI the interpreter arrives from an explicit workflow step, and a step is
// exactly the kind of thing a later edit drops. A contract test that skips its
// way to green is the failure this file exists to remove.
func shippedWriter(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("zsh"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("zsh is not installed, so the shipped reload writer cannot be executed — CI must run this contract, never skip it (see the Install zsh step in ci.yml and sonar.yml)")
		}
		t.Skip("zsh not installed — the shipped reload writer cannot be executed here")
	}
	raw, err := os.ReadFile(zshrcPath)
	if err != nil {
		t.Fatalf("read zshrc.sh: %v", err)
	}
	return zshFunction(t, string(raw), "toolbox-reload")
}

// reloadFixture returns the shipped writer, an empty state directory standing
// in for the state mount, and the marker path this session would be handed
// inside it.
func reloadFixture(t *testing.T) (src, state, marker string) {
	t.Helper()
	src = shippedWriter(t)
	state = t.TempDir()
	return src, state, reload.MarkerPath(state, fixtureContainer)
}

// runToolboxReload calls the shipped writer from cwd with marker as
// $TOOLBOX_RELOAD_MARKER (unset when empty) and returns the exit status the
// developer's shell would see. Any pathPrefix entries go in front of the
// inherited PATH, which is how a test shadows one of the commands the writer
// calls.
//
// The environment is built from scratch rather than inherited: this suite may
// itself be running inside a toolbox session, whose own marker variable would
// defeat the capability test below. `zsh -f` keeps the host's startup files
// out of it for the same reason.
func runToolboxReload(t *testing.T, src, cwd, marker string, pathPrefix ...string) int {
	t.Helper()
	cmd := exec.Command("zsh", "-f", "-c", src+"\ntoolbox-reload\n")
	cmd.Dir = cwd
	path := strings.Join(append(pathPrefix, os.Getenv("PATH")), string(os.PathListSeparator))
	cmd.Env = []string{"PATH=" + path}
	if marker != "" {
		cmd.Env = append(cmd.Env, reload.MarkerEnv+"="+marker)
	}
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	t.Fatalf("run toolbox-reload: %v", err)
	return -1
}

// mkdirCwd creates and returns a working directory to reload from, resolving
// symlinks so the expectation matches the physical path zsh reports in $PWD.
func mkdirCwd(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %q: %v", dir, err)
	}
	return resolved
}

// dirNames returns the sorted basenames of everything in dir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %q: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

// brokenRename returns a PATH entry that shadows `mv` with a command which
// always fails, so the writer reaches its rename-failed branch.
//
// Shadowing rather than arranging a real failure is deliberate: the temporary
// is named after the shell's pid, so nothing can be pre-placed to make the
// real rename fail, and every permission trick answers differently depending
// on the uid the suite happens to run as — root in the pinned `golang`
// container, an ordinary user on the CI runner.
func brokenRename(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mv"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write the failing mv: %v", err)
	}
	return dir
}

// TestReloadMarkerWriterMatchesGo is the half of the marker contract no string
// match can reach: that the bytes the in-container zsh function writes are the
// bytes TakeMarker parses. The format crosses a language boundary — a zsh
// writer nothing tested, a Go reader nothing production calls — and a drift is
// silent in the way Session Reload cannot afford: the host reads the marker to
// decide which container to destroy, so one it misparses leaves the old
// container holding the name the next `toolbox shell` resolves to.
//
// Byte equality with WriteMarker, not just a successful read: the Go writer is
// what the reload tests drive, so pinning the two writers together is what
// keeps those tests honest about the production format.
func TestReloadMarkerWriterMatchesGo(t *testing.T) {
	// The awkward shapes a working directory actually takes. Every byte but NUL
	// and `/` is legal in a directory name, and the last case is the one that
	// pins the trim: the writer's own newline and a newline that belongs to
	// the path are the same byte, so TakeMarker must undo exactly one.
	for _, name := range []string{"plain", "with space", "trailing space ", "unicode-é", "trailing newline\n"} {
		t.Run(name, func(t *testing.T) {
			src, _, marker := reloadFixture(t)
			cwd := mkdirCwd(t, name)

			if code := runToolboxReload(t, src, cwd, marker); code != 0 {
				t.Fatalf("toolbox-reload exited %d, want 0", code)
			}

			got, err := os.ReadFile(marker)
			if err != nil {
				t.Fatalf("read marker: %v", err)
			}
			reference := filepath.Join(t.TempDir(), "reference")
			if err := reload.WriteMarker(reference, cwd); err != nil {
				t.Fatalf("WriteMarker: %v", err)
			}
			want, err := os.ReadFile(reference)
			if err != nil {
				t.Fatalf("read reference: %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("zsh wrote %q, WriteMarker writes %q — the two spellings of the marker have drifted", got, want)
			}

			if cwdRead, requested := reload.TakeMarker(marker); !requested || cwdRead != cwd {
				t.Errorf("TakeMarker = (%q, %v), want (%q, true)", cwdRead, requested, cwd)
			}
		})
	}
}

// TestReloadMarkerWriterOverwrites pins the rename half. The marker is written
// beside its target and moved over it, so a second reload in the same session
// must replace the first request rather than append to it — a marker carrying
// two paths parses as neither.
func TestReloadMarkerWriterOverwrites(t *testing.T) {
	src, _, marker := reloadFixture(t)
	first, second := mkdirCwd(t, "first"), mkdirCwd(t, "second")

	if code := runToolboxReload(t, src, first, marker); code != 0 {
		t.Fatalf("first toolbox-reload exited %d, want 0", code)
	}
	if code := runToolboxReload(t, src, second, marker); code != 0 {
		t.Fatalf("second toolbox-reload exited %d, want 0", code)
	}

	if cwd, requested := reload.TakeMarker(marker); !requested || cwd != second {
		t.Errorf("TakeMarker = (%q, %v), want (%q, true)", cwd, requested, second)
	}
}

// TestReloadMarkerWriterRefusesWithoutTheCapability pins the refusal the
// capability marker exists for: no variable means a CLI too old to read the
// marker, and writing one anyway would spend the session for nothing.
func TestReloadMarkerWriterRefusesWithoutTheCapability(t *testing.T) {
	src, state, _ := reloadFixture(t)

	if code := runToolboxReload(t, src, mkdirCwd(t, "work"), ""); code != 1 {
		t.Errorf("toolbox-reload exited %d with no %s, want 1", code, reload.MarkerEnv)
	}
	if got := dirNames(t, state); len(got) != 0 {
		t.Errorf("the refusal wrote %v into the state directory, want nothing", got)
	}
}

// TestReloadMarkerWriterPublishesNothingItCannotWrite pins the first failure
// branch: the marker's own directory is gone, so the write fails before a
// temporary exists. Nothing may reach the host — a half-published request is
// read as a request, and the host destroys a container over it.
func TestReloadMarkerWriterPublishesNothingItCannotWrite(t *testing.T) {
	src, state, _ := reloadFixture(t)
	marker := reload.MarkerPath(filepath.Join(state, "absent"), fixtureContainer)

	if code := runToolboxReload(t, src, mkdirCwd(t, "work"), marker); code != 1 {
		t.Errorf("toolbox-reload exited %d on an unwritable marker, want 1", code)
	}
	if _, requested := reload.TakeMarker(marker); requested {
		t.Error("a failed write still published a reload request")
	}
	if got := dirNames(t, state); len(got) != 0 {
		t.Errorf("the failed write left %v behind, want nothing", got)
	}
}

// TestReloadMarkerWriterRemovesTheTemporaryWhenTheRenameFails pins the second
// failure branch, the one the write reaches only after succeeding: the
// temporary exists and the rename onto the marker fails.
//
// It is the branch that leaves litter. The state directory is a mount shared
// by every session in the workspace and also holds the decline stamp and the
// shell history, so an orphaned `.tmp.<pid>` per failed reload accumulates
// there for as long as the mount lives.
func TestReloadMarkerWriterRemovesTheTemporaryWhenTheRenameFails(t *testing.T) {
	src, state, marker := reloadFixture(t)

	if code := runToolboxReload(t, src, mkdirCwd(t, "work"), marker, brokenRename(t)); code != 1 {
		t.Errorf("toolbox-reload exited %d on a failed rename, want 1", code)
	}
	if _, requested := reload.TakeMarker(marker); requested {
		t.Error("a failed rename still published a reload request")
	}
	if got := dirNames(t, state); len(got) != 0 {
		t.Errorf("the failed rename left %v behind, want nothing — the temporary must be removed", got)
	}
}

// TestReloadMarkerWriterIgnoresAStaleTemporary pins what a session killed
// between the write and the rename leaves behind. A crash strands a
// `.tmp.<pid>` in the very directory the host reads markers from, and the host
// must still find exactly one readable request there.
//
// Two things are asserted and a third deliberately is not. The leftover is
// inert and the reload still publishes its marker; the writer litters nothing
// into the workspace the shell was standing in. That the temporary is a
// *sibling* of the marker — the precondition for the rename being atomic at
// all, since the state mount and the workspace are two different filesystems —
// cannot be observed from here, because a cross-directory rename still
// succeeds on a single-filesystem test host. TestReloadMarkerContract pins
// that one on the text of the writer instead.
func TestReloadMarkerWriterIgnoresAStaleTemporary(t *testing.T) {
	src, state, marker := reloadFixture(t)
	cwd := mkdirCwd(t, "work")

	stale := marker + ".tmp.999"
	if err := os.WriteFile(stale, []byte("/a/path/from/a/dead/session\n"), 0o644); err != nil {
		t.Fatalf("seed a stale temporary: %v", err)
	}

	if code := runToolboxReload(t, src, cwd, marker); code != 0 {
		t.Fatalf("toolbox-reload exited %d, want 0", code)
	}

	want := []string{filepath.Base(marker), filepath.Base(stale)}
	slices.Sort(want)
	if got := dirNames(t, state); !slices.Equal(got, want) {
		t.Errorf("state dir holds %v, want %v — the writer's temporary is not renamed over the marker", got, want)
	}
	if got := dirNames(t, cwd); len(got) != 0 {
		t.Errorf("the writer left %v in the working directory, want nothing", got)
	}

	if got, requested := reload.TakeMarker(marker); !requested || got != cwd {
		t.Errorf("TakeMarker = (%q, %v), want (%q, true) — a stale temporary was taken for the request", got, requested, cwd)
	}
}

// TestReloadMarkerWriterRefusesADirectoryMarker pins the failure that looks
// like a success. `mv file dir` does not overwrite the directory — it moves
// the file *into* it — so a marker path that is a directory would let the
// writer report a reload, exit the shell, and strand the request where the
// host never looks. The developer would lose the session and get no reload:
// the exact silent loss the marker exists to prevent, arrived at from the
// other side.
//
// Nothing in the tree creates a directory there today — the host builds the
// path from the state mount and the container name, both pinned — so this
// guards a state no producer currently reaches. It is here because the cost is
// one flag and the failure is silent.
func TestReloadMarkerWriterRefusesADirectoryMarker(t *testing.T) {
	src, state, marker := reloadFixture(t)
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatalf("seed a directory at the marker path: %v", err)
	}

	if code := runToolboxReload(t, src, mkdirCwd(t, "work"), marker); code != 1 {
		t.Errorf("toolbox-reload exited %d with a directory at the marker path, want 1", code)
	}
	if _, requested := reload.TakeMarker(marker); requested {
		t.Error("a directory at the marker path was taken for a reload request")
	}
	if got := dirNames(t, marker); len(got) != 0 {
		t.Errorf("the writer moved %v inside the directory, where the host never looks", got)
	}
	if got := dirNames(t, state); !slices.Equal(got, []string{filepath.Base(marker)}) {
		t.Errorf("state dir holds %v, want just the directory it was seeded with", got)
	}
}
