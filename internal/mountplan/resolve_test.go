package mountplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
)

func TestResolveAllSkipsMissing(t *testing.T) {
	home, _ := os.UserHomeDir()
	mounts := []config.Mount{
		{Source: "/path/that/does/not/exist/ever", Target: "/container/path", ReadOnly: false},
	}

	binds, warnings := resolveAll(mounts, home)

	if len(binds) != 0 {
		t.Errorf("expected 0 resolved mounts for missing path, got %d", len(binds))
	}

	if len(warnings) == 0 {
		t.Error("expected warning for missing path, got none")
	}
}

func TestResolveAllCreatesMissingWhenRequested(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "nested", "autocreate")

	mounts := []config.Mount{
		{Source: target, Target: "/container/auto", ReadOnly: false, CreateIfMissing: true},
	}

	binds, warnings := resolveAll(mounts, home)

	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(binds) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(binds))
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("source dir should have been created: %v", err)
	}
}

func TestResolveAllSymlinkFromCreatesLink(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	hostTarget := filepath.Join(tmp, "host-real")
	if err := os.Mkdir(hostTarget, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	src := filepath.Join(tmp, "toolbox", "link")

	mounts := []config.Mount{
		{Source: src, Target: "/container/link", ReadOnly: true, SymlinkFrom: hostTarget},
	}

	binds, warnings := resolveAll(mounts, home)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(binds) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(binds))
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
	if binds[0].Source != realTarget {
		t.Errorf("expected bind Source %q, got %q", realTarget, binds[0].Source)
	}
}

func TestResolveAllSymlinkFromSkipsWhenTargetMissing(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "link")
	bogus := filepath.Join(tmp, "does-not-exist")

	mounts := []config.Mount{
		{Source: src, Target: "/container/x", ReadOnly: true, SymlinkFrom: bogus},
	}

	binds, warnings := resolveAll(mounts, home)
	if len(binds) != 0 {
		t.Fatalf("expected 0 resolved mounts, got %d", len(binds))
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning when symlink target is missing")
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatal("symlink must not be created when target is missing")
	}
}

// TestResolveAllWarnsOnEvalSymlinksFailure: a source that exists as a
// symlink whose target chain cannot be resolved must surface a warning but
// still emit the bind with the unresolved path — resolution can fail for
// the invoking user (e.g. EACCES on an intermediate dir) while the Docker
// daemon (root) still mounts the path fine, so skipping would break
// today-working mounts.
func TestResolveAllWarnsOnEvalSymlinksFailure(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "broken-link")
	if err := os.Symlink(filepath.Join(tmp, "missing-target"), src); err != nil {
		t.Fatalf("setup: %v", err)
	}

	mounts := []config.Mount{
		{Source: src, Target: "/container/broken", ReadOnly: false},
	}

	binds, warnings := resolveAll(mounts, home)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "failed to resolve symlinks for mount source") {
		t.Errorf("warning %q missing the resolve-failure prefix", warnings[0])
	}
	if !strings.Contains(warnings[0], src) {
		t.Errorf("warning %q does not name the source %q", warnings[0], src)
	}
	if len(binds) != 1 {
		t.Fatalf("expected 1 bind (mount must not be skipped), got %d", len(binds))
	}
	if binds[0].Source != src {
		t.Errorf("expected unresolved source %q, got %q", src, binds[0].Source)
	}
}

// TestResolveAllResolvesHealthySymlinkSource: a source symlink whose chain
// resolves cleanly produces no symlink warning and the bind carries the
// fully resolved real path.
func TestResolveAllResolvesHealthySymlinkSource(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real-dir")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	src := filepath.Join(tmp, "healthy-link")
	if err := os.Symlink(real, src); err != nil {
		t.Fatalf("setup: %v", err)
	}

	mounts := []config.Mount{
		{Source: src, Target: "/container/healthy", ReadOnly: false},
	}

	binds, warnings := resolveAll(mounts, home)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(binds) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(binds))
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if binds[0].Source != want {
		t.Errorf("expected resolved source %q, got %q", want, binds[0].Source)
	}
}

func TestResolveAllReplacesEmptyDirWithSymlink(t *testing.T) {
	home, _ := os.UserHomeDir()
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

	_, warnings := resolveAll(mounts, home)
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

func TestResolveAllKeepsNonEmptyDirEvenWithSymlinkFrom(t *testing.T) {
	home, _ := os.UserHomeDir()
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

	_, warnings := resolveAll(mounts, home)
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

// TestResolveAllRelativeSourceUsesCWD: ./ paths must resolve to the
// process CWD, so users can write `source: ./test` in .toolbox.yaml and
// have it bound from the project root they invoked toolbox shell from.
func TestResolveAllRelativeSourceUsesCWD(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "test"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	prevCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	mounts := []config.Mount{
		{Source: "./test", Target: "/container/test", ReadOnly: false},
	}

	binds, warnings := resolveAll(mounts, home)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(binds) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(binds))
	}

	wantPrefix, err := filepath.EvalSymlinks(filepath.Join(tmp, "test"))
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if binds[0].Source != wantPrefix {
		t.Errorf("expected bind Source %q, got %q", wantPrefix, binds[0].Source)
	}
}

// TestResolveAllRelativeSourceCreatesUnderCWD: relative source +
// CreateIfMissing must create the dir under CWD, not under the literal
// "./test" string.
func TestResolveAllRelativeSourceCreatesUnderCWD(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmp := t.TempDir()

	prevCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	mounts := []config.Mount{
		{Source: "./auto", Target: "/container/auto", ReadOnly: false, CreateIfMissing: true},
	}

	if _, warnings := resolveAll(mounts, home); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if _, err := os.Stat(filepath.Join(tmp, "auto")); err != nil {
		t.Fatalf("expected ./auto to be created under CWD: %v", err)
	}
}

func TestResolveAllReadOnlyMode(t *testing.T) {
	home, _ := os.UserHomeDir()
	tmpDir := t.TempDir()

	mounts := []config.Mount{
		{Source: tmpDir, Target: "/container/test", ReadOnly: true},
		{Source: tmpDir, Target: "/container/test-rw", ReadOnly: false},
	}

	binds, warnings := resolveAll(mounts, home)

	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	if len(binds) != 2 {
		t.Fatalf("expected 2 resolved mounts, got %d", len(binds))
	}

	if binds[0].Mode != "ro" {
		t.Errorf("expected ro mode, got %q", binds[0].Mode)
	}
	if !strings.HasPrefix(binds[0].Source, tmpDir) && binds[0].Source != tmpDir {
		// macOS may resolve /var → /private/var
		realTmp, _ := filepath.EvalSymlinks(tmpDir)
		if binds[0].Source != realTmp {
			t.Errorf("expected source %q (or resolved), got %q", tmpDir, binds[0].Source)
		}
	}

	if binds[1].Mode != "rw" {
		t.Errorf("expected rw mode, got %q", binds[1].Mode)
	}
}

// TestBindString sanity-checks the daemon-edge stringification: lifecycle
// hands these to container.HostConfig.Binds verbatim, so any drift here
// reaches the Docker daemon as a malformed bind spec.
func TestBindString(t *testing.T) {
	tests := []struct {
		bind Bind
		want string
	}{
		{Bind{Source: "/host", Target: "/container", Mode: "rw"}, "/host:/container:rw"},
		{Bind{Source: "/host", Target: "/container", Mode: "ro"}, "/host:/container:ro"},
	}
	for _, tc := range tests {
		if got := tc.bind.String(); got != tc.want {
			t.Errorf("Bind.String() = %q, want %q", got, tc.want)
		}
	}
}
