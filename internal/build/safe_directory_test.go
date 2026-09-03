package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The safe.directory registration lives in entrypoint.sh as a single locked
// git-config call. These markers bracket it.
const (
	safeDirBlockStart = "sudo flock /tmp/toolbox-gitconfig.lock sh -c '"
	// The block ends with its non-fatal warning. Anchored on the stable prefix
	// of that echo rather than on the full sentence: the tail is user-facing
	// prose and will be reworded, and this test has no business breaking when it
	// is. extractSafeDirectoryBlock takes the rest of the line, so what it hands
	// back is still a complete statement.
	safeDirBlockEnd = `|| echo "toolbox: git safe.directory registration failed`
)

// extractSafeDirectoryBlock lifts the registration out of the embedded
// entrypoint.
//
// Needle-matching the asset cannot hold this block: the three ways it can
// betray its caller are all behavioural — writing into the host's ~/.gitconfig
// instead of the container-local system config, appending a duplicate entry on
// every restart, and aborting the boot when sudo or git is unavailable. Each of
// those spells correctly enough to satisfy any plausible needle, so the test
// runs the block against stub sudo/flock/git and observes what it did.
func extractSafeDirectoryBlock(t *testing.T) string {
	t.Helper()
	body := readAsset(t, "entrypoint.sh")

	_, rest, found := strings.Cut(body, safeDirBlockStart)
	if !found {
		t.Fatalf("entrypoint.sh: cannot find the safe.directory registration (%q) — it was renamed or removed", safeDirBlockStart)
	}
	end := strings.Index(rest, safeDirBlockEnd)
	if end < 0 {
		t.Fatalf("entrypoint.sh: safe.directory registration has no %q — cannot tell where the block ends", safeDirBlockEnd)
	}
	warning := rest[end:]
	if nl := strings.Index(warning, "\n"); nl >= 0 {
		warning = warning[:nl]
	}
	block := safeDirBlockStart + rest[:end] + warning + "\n"
	// The start marker is shared with the credential-helper registration further
	// down, which takes the same lock. Extracting that one instead would leave
	// every subtest below asserting on the wrong block, silently.
	if !strings.Contains(block, "safe.directory") {
		t.Fatalf("extracted the wrong locked block — it mentions no safe.directory:\n%s", block)
	}
	return block
}

// gitStub answers the two git invocations the block makes and records every
// argv it is handed, so the test can assert on the config scope it asked for.
const gitStub = `#!/bin/sh
printf '%s\n' "$*" >> "$SAFE_DIR_ARGV"
[ -z "${GIT_STUB_FAIL:-}" ] || exit 1
for _a in "$@"; do _last=$_a; done
case "$*" in
*"--get-all safe.directory"*) cat "$SAFE_DIR_DB" 2>/dev/null || exit 1 ;;
*"--add safe.directory"*) printf '%s\n' "$_last" >> "$SAFE_DIR_DB" ;;
esac
`

type safeDirHarness struct {
	dir, script, db, argv string
}

func newSafeDirHarness(t *testing.T) *safeDirHarness {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir stub bin: %v", err)
	}
	// sudo runs its argv as-is; flock drops the lock path and runs the rest.
	// The block's serialisation is not what this test is about — its effect on
	// the config is.
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	write("sudo", "#!/bin/sh\nexec \"$@\"\n")
	write("flock", "#!/bin/sh\nshift\nexec \"$@\"\n")
	write("git", gitStub)

	// `set -eu` stands in for the entrypoint's `set -euo pipefail`: a step that
	// fails without being tolerated takes the whole boot down, which is what the
	// test looks for. `pipefail` is not needed to reproduce that here — the only
	// pipeline lives inside the `sh -c` child, where shell options do not cross
	// the exec — but inlining that pipeline later would change this.
	script := filepath.Join(dir, "block.sh")
	body := "#!/bin/sh\nset -eu\n" + extractSafeDirectoryBlock(t)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write block script: %v", err)
	}
	return &safeDirHarness{
		dir:    dir,
		script: script,
		db:     filepath.Join(dir, "safe-directories"),
		argv:   filepath.Join(dir, "git-argv"),
	}
}

func (h *safeDirHarness) run(t *testing.T, extraEnv ...string) (output string, exitCode int) {
	t.Helper()
	env := []string{
		"PATH=" + filepath.Join(h.dir, "bin") + ":/usr/bin:/bin",
		"HOME=" + h.dir,
		"SAFE_DIR_DB=" + h.db,
		"SAFE_DIR_ARGV=" + h.argv,
	}
	cmd := exec.Command("/bin/sh", h.script)
	cmd.Env = append(env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if !asExitError(err, &ee) {
			t.Fatalf("run safe.directory block: %v (output %q)", err, out)
		}
		exitCode = ee.ExitCode()
	}
	return string(out), exitCode
}

func (h *safeDirHarness) registered(t *testing.T) []string {
	t.Helper()
	return readLines(t, h.db)
}

func (h *safeDirHarness) gitArgv(t *testing.T) []string {
	t.Helper()
	return readLines(t, h.argv)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TestSafeDirectoryRegistration covers why the block exists at all.
//
// A bind-mounted directory transiently reports uid 0 while its own contents
// keep the host uid, and git checks the worktree rather than the files in it,
// so it refuses a repository that is ours with "dubious ownership". Captured on
// the workspace with a 2s probe: of 95 failures, 91 showed euid=501 against a
// mount point at uid=0 in the same instant, .git one level down staying at 501.
// Registering makes the question moot, because git consults safe.directory
// whenever the ownership check does not pass — which also covers the other two
// roads to that same fatal: a genuinely foreign-uid repo, and an ownership
// question git could not answer at all (is_path_owned_by_current_uid() reads
// "not mine" from any lstat() failure).
func TestSafeDirectoryRegistration(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}

	// The entry is the wildcard rather than a list of paths because the affected
	// repositories cannot be enumerated at boot: ~/.claude is a bind mount of
	// the same kind as the workspace, and `claude plugin update` clones each
	// git-subdir plugin into a randomly named directory under
	// ~/.claude/plugins/cache. git matches safe.directory against the worktree
	// root it discovered, so a path that does not exist yet when this runs can
	// never be covered by naming it — and on the git this image ships, only an
	// exact path or the wildcard matches at all: no glob form does, the trailing
	// `/*` a newer git accepts recursively included. Tightening this back to a
	// path (or to that glob) reopens the reported failure.
	t.Run("registers the wildcard, never an enumerated path", func(t *testing.T) {
		h := newSafeDirHarness(t)
		if out, code := h.run(t); code != 0 {
			t.Fatalf("block exited %d, want 0 (output %q)", code, out)
		}
		want := []string{"*"}
		if got := h.registered(t); !slices.Equal(got, want) {
			t.Errorf("registered %q, want %q — a named path cannot cover a worktree whose name is generated after boot", got, want)
		}
	})

	t.Run("never touches the host gitconfig", func(t *testing.T) {
		h := newSafeDirHarness(t)
		h.run(t)
		argv := h.gitArgv(t)
		if len(argv) == 0 {
			t.Fatal("git was never called — the block registered nothing")
		}
		for _, call := range argv {
			if strings.Contains(call, "--global") || strings.Contains(call, "--file") {
				t.Errorf("git called with a non-system scope: %q — ~/.gitconfig is a RW host mount and must not be polluted", call)
			}
			if !strings.Contains(call, "--system") {
				t.Errorf("git called without --system: %q", call)
			}
		}
	})

	t.Run("adds nothing on a re-run", func(t *testing.T) {
		h := newSafeDirHarness(t)
		h.run(t)
		first := h.registered(t)
		// The entrypoint runs once per container start, not per shell: a second
		// `toolbox shell` arrives via ExecCreate on the resolved shell command,
		// never through the entrypoint. So what re-runs this is
		// runplan.ActionStart on a stopped container, against the /etc/gitconfig
		// the first boot already wrote.
		if out, code := h.run(t); code != 0 {
			t.Fatalf("second run exited %d, want 0 (output %q)", code, out)
		}
		if got := h.registered(t); !slices.Equal(got, first) {
			t.Errorf("re-run changed the registrations: %q, want %q", got, first)
		}
	})

	// 30-graphify.sh asks git whether the workspace is a work tree with output
	// suppressed, so an ownership fatal there skips the hook install in silence.
	// The registration has to be in place before any init script runs.
	t.Run("runs before the init sequence", func(t *testing.T) {
		body := readAsset(t, "entrypoint.sh")
		block := strings.Index(body, safeDirBlockStart)
		initSeq := strings.Index(body, `INIT_D="/usr/local/lib/toolbox/init.d"`)
		if block < 0 || initSeq < 0 {
			t.Fatalf("entrypoint.sh: cannot locate both anchors (block=%d, init.d dispatch=%d) — one was renamed, and a missing marker must not pass this subtest by arithmetic", block, initSeq)
		}
		if block > initSeq {
			t.Errorf("safe.directory block sits after the init sequence (offsets %d > %d): init scripts run git against the workspace with output suppressed, so an ownership fatal there fails silently", block, initSeq)
		}
	})

	t.Run("a failing git never aborts the boot", func(t *testing.T) {
		h := newSafeDirHarness(t)
		out, code := h.run(t, "GIT_STUB_FAIL=1")
		if code != 0 {
			t.Fatalf("block exited %d on a failing git, want 0 — a registration failure must not block the shell (output %q)", code, out)
		}
		if !strings.Contains(out, "safe.directory") {
			t.Errorf("silent failure: output %q says nothing about the failed registration", out)
		}
	})
}
