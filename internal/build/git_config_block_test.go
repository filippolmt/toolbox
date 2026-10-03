package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitConfigBlockHarness runs one extracted entrypoint block against executable
// stubs. The system-gitconfig blocks share this process boundary; each test
// supplies only the collaborators and observations specific to its policy.
type gitConfigBlockHarness struct {
	dir, bin, script string
}

func newGitConfigBlockHarness(t *testing.T, block string) *gitConfigBlockHarness {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir stub bin: %v", err)
	}
	h := &gitConfigBlockHarness{
		dir:    dir,
		bin:    bin,
		script: filepath.Join(dir, "block.sh"),
	}
	// The block's serialisation is not under test: sudo runs its argv as-is and
	// flock drops the lock path before running the protected command.
	h.writeStub(t, "sudo", "#!/bin/sh\nexec \"$@\"\n")
	h.writeStub(t, "flock", "#!/bin/sh\nshift\nexec \"$@\"\n")

	// `set -eu` stands in for the entrypoint's strict mode. A failure the block
	// does not tolerate must therefore take down this process too.
	body := "#!/bin/sh\nset -eu\n" + block
	if err := os.WriteFile(h.script, []byte(body), 0o755); err != nil {
		t.Fatalf("write block script: %v", err)
	}
	return h
}

func (h *gitConfigBlockHarness) writeStub(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.bin, name), []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

func (h *gitConfigBlockHarness) run(t *testing.T, label string, extraEnv ...string) (output string, exitCode int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", h.script)
	cmd.Dir = h.dir
	cmd.Env = append([]string{
		"PATH=" + h.bin + ":/usr/bin:/bin",
		"HOME=" + h.dir,
	}, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("run %s block: %v (output %q)", label, err, out)
	return "", -1
}
