package configui

import (
	"errors"
	"testing"
)

// TestRunRequiresTTY: under `go test` stdin/stdout are not TTYs, so Run must
// fail fast with ErrNotTTY and never launch the program.
func TestRunRequiresTTY(t *testing.T) {
	err := Run(t.TempDir())
	if !errors.Is(err, ErrNotTTY) {
		t.Fatalf("Run without a TTY = %v, want ErrNotTTY", err)
	}
}
