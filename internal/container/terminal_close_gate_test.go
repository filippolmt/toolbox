//go:build dockergate

package container

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the actual CLI under a controlling terminal: an attach mock cannot
// reproduce the remote shell surviving the client's terminal hangup.
func TestTerminalCloseDoesNotStrandTheContainer(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "toolbox")
	build := exec.Command("go", "build", "-o", binary, "../..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	args := []string{"testdata/terminal_close.py", binary}
	if image := os.Getenv("IMAGE_TAG"); image != "" {
		args = append(args, "--image", image)
	}
	if output, err := exec.Command("python3", args...).CombinedOutput(); err != nil {
		t.Fatalf("terminal lifecycle: %v\n%s", err, output)
	}
}
