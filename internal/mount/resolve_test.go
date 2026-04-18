package mount

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"~/.claude", filepath.Join(home, ".claude")},
		{"~", home},
		{"/var/run/docker.sock", "/var/run/docker.sock"},
	}

	for _, tc := range tests {
		got := expandHome(tc.input, home)
		if got != tc.expected {
			t.Errorf("expandHome(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestResolveMountsSkipsMissing(t *testing.T) {
	mounts := []config.Mount{
		{Source: "/path/che/non/esiste/mai", Target: "/container/path", ReadOnly: false},
	}

	resolved, warnings := ResolveMounts(mounts)

	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved mounts for missing path, got %d", len(resolved))
	}

	if len(warnings) == 0 {
		t.Error("expected warning for missing path, got none")
	}
}

func TestResolveMountsFormat(t *testing.T) {
	// Crea directory temporanea come source esistente
	tmpDir := t.TempDir()

	mounts := []config.Mount{
		{Source: tmpDir, Target: "/container/test", ReadOnly: true},
		{Source: tmpDir, Target: "/container/test-rw", ReadOnly: false},
	}

	resolved, warnings := ResolveMounts(mounts)

	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved mounts, got %d", len(resolved))
	}

	// Verifica formato read-only
	if !strings.HasSuffix(resolved[0], ":ro") {
		t.Errorf("expected :ro suffix, got %q", resolved[0])
	}
	if !strings.Contains(resolved[0], tmpDir+":") {
		t.Errorf("expected source path %q in mount, got %q", tmpDir, resolved[0])
	}

	// Verifica formato read-write
	if !strings.HasSuffix(resolved[1], ":rw") {
		t.Errorf("expected :rw suffix, got %q", resolved[1])
	}
}
