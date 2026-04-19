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
		{Source: "/path/that/does/not/exist/ever", Target: "/container/path", ReadOnly: false},
	}

	resolved, warnings := ResolveMounts(mounts)

	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved mounts for missing path, got %d", len(resolved))
	}

	if len(warnings) == 0 {
		t.Error("expected warning for missing path, got none")
	}
}

func TestResolveMountsCreatesMissingWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "nested", "autocreate")

	mounts := []config.Mount{
		{Source: target, Target: "/container/auto", ReadOnly: false, CreateIfMissing: true},
	}

	resolved, warnings := ResolveMounts(mounts)

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(resolved))
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("source dir should have been created: %v", err)
	}
}

func TestResolveMountsSymlinkFromCreatesLink(t *testing.T) {
	tmp := t.TempDir()
	hostTarget := filepath.Join(tmp, "host-real")
	if err := os.Mkdir(hostTarget, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	src := filepath.Join(tmp, "toolbox", "link")

	mounts := []config.Mount{
		{Source: src, Target: "/container/link", ReadOnly: true, SymlinkFrom: hostTarget},
	}

	resolved, warnings := ResolveMounts(mounts)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(resolved))
	}

	linfo, err := os.Lstat(src)
	if err != nil {
		t.Fatalf("symlink not created at %s: %v", src, err)
	}
	if linfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink at %s, got mode %v", src, linfo.Mode())
	}

	// The bind spec must carry the resolved real path, not the symlink path.
	realTarget, err := filepath.EvalSymlinks(hostTarget)
	if err != nil {
		t.Fatalf("EvalSymlinks(hostTarget): %v", err)
	}
	if !strings.HasPrefix(resolved[0], realTarget+":") {
		t.Errorf("expected bind to start with real path %q, got %q", realTarget, resolved[0])
	}
}

func TestResolveMountsSymlinkFromSkipsWhenTargetMissing(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "link")
	bogus := filepath.Join(tmp, "does-not-exist")

	mounts := []config.Mount{
		{Source: src, Target: "/container/x", ReadOnly: true, SymlinkFrom: bogus},
	}

	resolved, warnings := ResolveMounts(mounts)
	if len(resolved) != 0 {
		t.Fatalf("expected 0 resolved mounts, got %d", len(resolved))
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning when symlink target is missing")
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatal("symlink must not be created when target is missing")
	}
}

func TestResolveMountsReplacesEmptyDirWithSymlink(t *testing.T) {
	tmp := t.TempDir()
	hostTarget := filepath.Join(tmp, "host-real")
	if err := os.Mkdir(hostTarget, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	src := filepath.Join(tmp, "empty-dir")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	mounts := []config.Mount{
		{Source: src, Target: "/container/x", ReadOnly: true, SymlinkFrom: hostTarget},
	}

	_, warnings := ResolveMounts(mounts)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	info, err := os.Lstat(src)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("empty dir should have been replaced by symlink, got %v", info.Mode())
	}
}

func TestResolveMountsKeepsNonEmptyDirEvenWithSymlinkFrom(t *testing.T) {
	tmp := t.TempDir()
	hostTarget := filepath.Join(tmp, "host-real")
	if err := os.Mkdir(hostTarget, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	src := filepath.Join(tmp, "with-content")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	mounts := []config.Mount{
		{Source: src, Target: "/container/x", ReadOnly: true, SymlinkFrom: hostTarget},
	}

	_, warnings := ResolveMounts(mounts)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	info, err := os.Lstat(src)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("non-empty dir must not be replaced; got mode %v", info.Mode())
	}
}

func TestResolveMountsFormat(t *testing.T) {
	// Create a temporary directory as an existing source.
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

	// Check read-only format.
	if !strings.HasSuffix(resolved[0], ":ro") {
		t.Errorf("expected :ro suffix, got %q", resolved[0])
	}
	if !strings.Contains(resolved[0], tmpDir+":") {
		t.Errorf("expected source path %q in mount, got %q", tmpDir, resolved[0])
	}

	// Check read-write format.
	if !strings.HasSuffix(resolved[1], ":rw") {
		t.Errorf("expected :rw suffix, got %q", resolved[1])
	}
}
