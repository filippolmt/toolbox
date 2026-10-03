package build

import (
	"os"
	"os/exec"
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
// start. CI has no virtiofs mount on which to reproduce the inode instability;
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
	dir, script, gitArgv, findmntArgv string
}

func newCheckStatHarness(t *testing.T) *checkStatHarness {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir stub bin: %v", err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", name, err)
		}
	}
	write("sudo", "#!/bin/sh\nexec \"$@\"\n")
	write("flock", "#!/bin/sh\nshift\nexec \"$@\"\n")
	write("findmnt", `#!/bin/sh
printf '%s\n' "$*" >> "$CHECKSTAT_FINDMNT_ARGV"
[ -z "${FINDMNT_FAIL:-}" ] || exit 1
printf '%s\n' "$FINDMNT_FSTYPE"
`)
	write("git", `#!/bin/sh
printf '%s\n' "$*" >> "$CHECKSTAT_GIT_ARGV"
[ -z "${GIT_STUB_FAIL:-}" ] || exit 1
`)

	script := filepath.Join(dir, "block.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nset -eu\n"+extractCheckStatBlock(t)), 0o755); err != nil {
		t.Fatalf("write block script: %v", err)
	}
	return &checkStatHarness{
		dir:         dir,
		script:      script,
		gitArgv:     filepath.Join(dir, "git-argv"),
		findmntArgv: filepath.Join(dir, "findmnt-argv"),
	}
}

func (h *checkStatHarness) run(t *testing.T, fsType string, extraEnv ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", h.script)
	cmd.Dir = h.dir
	cmd.Env = append([]string{
		"PATH=" + filepath.Join(h.dir, "bin") + ":/usr/bin:/bin",
		"HOME=" + h.dir,
		"FINDMNT_FSTYPE=" + fsType,
		"CHECKSTAT_GIT_ARGV=" + h.gitArgv,
		"CHECKSTAT_FINDMNT_ARGV=" + h.findmntArgv,
	}, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("run core.checkStat block: %v (output %q)", err, out)
	return "", -1
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
