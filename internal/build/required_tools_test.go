package build

import (
	"os"
	"os/exec"
	"testing"
)

// requireTools skips the test when a tool it executes is missing from PATH,
// except under $CI, where the runner must provide every one (ci.yml): a skip
// there would retire the test silently. Locally, `make go-test` runs in an
// image that carries them.
func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("%s not on PATH: CI must provide it, never skip the test", tool)
			}
			t.Skipf("%s not on PATH: `make go-test` provides it", tool)
		}
	}
}
