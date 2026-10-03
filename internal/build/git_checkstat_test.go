package build

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	checkStatBlockStart = `# git core.checkStat for virtiofs`
	checkStatBlockEnd   = `|| echo "toolbox: git core.checkStat registration failed`
)

// extractCheckStatBlock lifts the runtime filesystem policy out of the embedded
// entrypoint so the test observes the same shell Git users run at container
// start. CI has no virtiofs mount on which to reproduce the stat instability;
// the real-mount repro is therefore complemented by exercising the mitigation's
// complete detection-and-registration boundary here.
func extractCheckStatBlock(t *testing.T) string {
	t.Helper()
	body := readAsset(t, "entrypoint.sh")

	start := strings.Index(body, checkStatBlockStart)
	if start < 0 {
		t.Fatalf("entrypoint.sh: cannot find the virtiofs core.checkStat registration (%q)", checkStatBlockStart)
	}
	rest := body[start:]
	end := strings.Index(rest, checkStatBlockEnd)
	if end < 0 {
		t.Fatalf("entrypoint.sh: core.checkStat registration has no %q", checkStatBlockEnd)
	}
	warning := rest[end:]
	if nl := strings.Index(warning, "\n"); nl >= 0 {
		warning = warning[:nl]
	}
	return rest[:end] + warning + "\n"
}

type checkStatHarness struct {
	*gitConfigBlockHarness
	gitArgv, findmntArgv string
}

func newCheckStatHarness(t *testing.T) *checkStatHarness {
	t.Helper()
	h := newGitConfigBlockHarness(t, extractCheckStatBlock(t))
	gitArgv := filepath.Join(h.dir, "git-argv")
	findmntArgv := filepath.Join(h.dir, "findmnt-argv")
	h.writeStub(t, "findmnt", `#!/bin/sh
printf '%s\n' "$*" >> "$CHECKSTAT_FINDMNT_ARGV"
[ -z "${FINDMNT_FAIL:-}" ] || exit 1
printf '%s\n' "$FINDMNT_FSTYPE"
`)
	h.writeStub(t, "git", `#!/bin/sh
printf '%s\n' "$*" >> "$CHECKSTAT_GIT_ARGV"
[ -z "${GIT_STUB_FAIL:-}" ] || exit 1
`)
	return &checkStatHarness{
		gitConfigBlockHarness: h,
		gitArgv:               gitArgv,
		findmntArgv:           findmntArgv,
	}
}

func (h *checkStatHarness) run(t *testing.T, fsType string, extraEnv ...string) (string, int) {
	t.Helper()
	env := []string{
		"FINDMNT_FSTYPE=" + fsType,
		"CHECKSTAT_GIT_ARGV=" + h.gitArgv,
		"CHECKSTAT_FINDMNT_ARGV=" + h.findmntArgv,
	}
	return h.gitConfigBlockHarness.run(t, "core.checkStat", append(env, extraEnv...)...)
}

func TestVirtiofsCheckStatRegistration(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}

	t.Run("uses minimal system checks on a virtiofs workspace", func(t *testing.T) {
		h := newCheckStatHarness(t)
		if out, code := h.run(t, "virtiofs"); code != 0 {
			t.Fatalf("block exited %d, want 0 (output %q)", code, out)
		}
		want := []string{"config --system core.checkStat minimal"}
		if got := readLines(t, h.gitArgv); !slices.Equal(got, want) {
			t.Errorf("git calls = %q, want %q", got, want)
		}
		wantFindmnt := "-n -o FSTYPE --target " + h.dir
		if got := readLines(t, h.findmntArgv); !slices.Equal(got, []string{wantFindmnt}) {
			t.Errorf("findmnt calls = %q, want target workspace %q", got, wantFindmnt)
		}
	})

	t.Run("leaves native filesystems at Git defaults", func(t *testing.T) {
		h := newCheckStatHarness(t)
		if out, code := h.run(t, "ext4"); code != 0 {
			t.Fatalf("block exited %d, want 0 (output %q)", code, out)
		}
		if got := readLines(t, h.gitArgv); len(got) != 0 {
			t.Errorf("git calls = %q, want none for a native filesystem", got)
		}
	})

	t.Run("a failed registration warns without aborting boot", func(t *testing.T) {
		h := newCheckStatHarness(t)
		out, code := h.run(t, "virtiofs", "GIT_STUB_FAIL=1")
		if code != 0 {
			t.Fatalf("block exited %d, want 0 (output %q)", code, out)
		}
		if !strings.Contains(out, "core.checkStat") {
			t.Errorf("silent failure: output %q says nothing about the failed registration", out)
		}
	})

	t.Run("an unreadable mount table leaves Git defaults in place", func(t *testing.T) {
		h := newCheckStatHarness(t)
		if out, code := h.run(t, "", "FINDMNT_FAIL=1"); code != 0 {
			t.Fatalf("block exited %d, want 0 (output %q)", code, out)
		}
		if got := readLines(t, h.gitArgv); len(got) != 0 {
			t.Errorf("git calls = %q, want none when filesystem detection fails", got)
		}
	})
}
