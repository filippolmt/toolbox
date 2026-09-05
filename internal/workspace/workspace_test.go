package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cases below are shaped for a Unix host on purpose — a rooted "/", a
// directory literally named "foo:bar", a symlink. The CLI targets exactly the
// platforms the `goos` list in .goreleaser.yaml names, and on none of them is
// any of that exotic.

// tempWorkspaceDir is t.TempDir() narrowed to what this package will accept.
// A TMPDIR carrying a ':' would fail every accepting case on the very check
// these tests exist to pin, which says the machine is misconfigured for a
// Docker-bind tool, not that the code regressed.
func tempWorkspaceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if strings.ContainsRune(dir, ':') {
		t.Skipf("TMPDIR %q contains ':', which no workspace path may; point TMPDIR elsewhere to run this test", dir)
	}
	return dir
}

// assertRejected pins the shape every rejection in this package promises: an
// error naming the reason, and no path left for the caller to hand to Docker
// by mistake. Pass "" for got when the function under test returns only an
// error.
func assertRejected(t *testing.T, got string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got %q and no error, want an error containing %q", got, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("got error %v, want one containing %q", err, want)
	}
	if got != "" {
		t.Errorf("got %q alongside the error, want the empty string", got)
	}
}

func TestValidateRejectsColon(t *testing.T) {
	err := Validate("/Users/alice/foo:bar/project")
	if err == nil {
		t.Fatal("paths with ':' must be rejected to avoid bind-format mis-parsing")
	}
}

func TestValidateAcceptsCommonPaths(t *testing.T) {
	cases := []string{
		"/Users/alice/project",
		"/home/bob/code-with-dashes",
		"/mnt/data/dir.with.dots",
		"/tmp/a_b_c",
		"/",
	}
	for _, p := range cases {
		if err := Validate(p); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidateAbsolute(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr string // substring; empty means the path must be accepted
	}{
		{name: "absolute clean path", path: "/Users/alice/project"},
		{name: "root", path: "/"},
		{name: "dot dot segment is not this check's business", path: "/Users/alice/../alice/project"},
		{name: "relative path", path: "relative/project", wantErr: "not absolute"},
		{name: "dot-relative path", path: "./project", wantErr: "not absolute"},
		{name: "empty path", path: "", wantErr: "not absolute"},
		// Nothing here expands '~': a named shell's `path: ~/project` must be
		// refused rather than reach Docker as a literal directory name.
		{name: "tilde path is not expanded", path: "~/project", wantErr: "not absolute"},
		{name: "absolute path with colon", path: "/Users/alice/foo:bar", wantErr: "':'"},
		{name: "relative path with colon is rejected as relative first", path: "foo:bar", wantErr: "not absolute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAbsolute(tc.path)
			if tc.wantErr != "" {
				assertRejected(t, "", err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("ValidateAbsolute(%q) = %v, want nil", tc.path, err)
			}
		})
	}
}

// TestResolveReturnsCleanAbsoluteCWD pins that Resolve reports the working
// directory itself: `toolbox shell` with no argument mounts what the user is
// standing in, so a divergence here silently mounts the wrong tree.
func TestResolveReturnsCleanAbsoluteCWD(t *testing.T) {
	dir := tempWorkspaceDir(t)
	t.Chdir(dir)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if got != dir {
		t.Errorf("Resolve() = %q, want %q", got, dir)
	}
}

// TestResolveKeepsASymlinkedCWD states the half no caller could infer: a
// developer standing in a symlink gets the symlink as the bind source, the
// same verbatim treatment ResolveExplicit gives an explicit one. Resolving it
// would change the per-workspace container identity behind their back.
func TestResolveKeepsASymlinkedCWD(t *testing.T) {
	base := tempWorkspaceDir(t)
	target := filepath.Join(base, "project")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", target, err)
	}
	link := filepath.Join(base, "link-to-project")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(%q): %v", link, err)
	}
	t.Chdir(link)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() = %v, want nil", err)
	}
	if got != link {
		t.Errorf("Resolve() = %q, want the symlink %q", got, link)
	}
}

// TestResolveRejectsColonInCWD covers the Validate branch inside Resolve: a
// working directory Docker's Binds format would mis-parse must fail loudly
// instead of reaching the bind source.
func TestResolveRejectsColonInCWD(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "foo:bar")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", dir, err)
	}
	t.Chdir(dir)

	got, err := Resolve()
	assertRejected(t, got, err, "':'")
}

func TestResolveExplicit(t *testing.T) {
	base := tempWorkspaceDir(t)

	dir := filepath.Join(base, "project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", dir, err)
	}
	file := filepath.Join(base, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", file, err)
	}
	dirLink := filepath.Join(base, "link-to-project")
	if err := os.Symlink(dir, dirLink); err != nil {
		t.Fatalf("Symlink(%q): %v", dirLink, err)
	}
	fileLink := filepath.Join(base, "link-to-file")
	if err := os.Symlink(file, fileLink); err != nil {
		t.Fatalf("Symlink(%q): %v", fileLink, err)
	}

	cases := []struct {
		name    string
		path    string
		want    string
		wantErr string // substring; empty means the path must be accepted
	}{
		{name: "existing directory", path: dir, want: dir},
		{name: "trailing separator is cleaned away", path: dir + string(os.PathSeparator), want: dir},
		{name: "dot dot segment is collapsed, not rejected", path: filepath.Join(dir, "nowhere", ".."), want: dir},
		// The symlink is kept verbatim: the resolved path becomes the bind
		// source and the per-workspace container identity, so resolving it
		// here would silently rename the user's shell.
		{name: "symlink to a directory is kept unresolved", path: dirLink, want: dirLink},
		{name: "symlink to a file", path: fileLink, wantErr: "not a directory"},
		{name: "regular file", path: file, wantErr: "not a directory"},
		{name: "missing path", path: filepath.Join(base, "absent"), wantErr: "stat "},
		{name: "relative path", path: "project", wantErr: "not absolute"},
		{name: "empty path", path: "", wantErr: "not absolute"},
		{name: "tilde path is not expanded", path: "~/project", wantErr: "not absolute"},
		{name: "absolute path with colon", path: filepath.Join(base, "foo:bar"), wantErr: "':'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveExplicit(tc.path)
			if tc.wantErr != "" {
				assertRejected(t, got, err, tc.wantErr)
				return
			}
			if err != nil {
				t.Fatalf("ResolveExplicit(%q) = %v, want nil", tc.path, err)
			}
			if got != tc.want {
				t.Errorf("ResolveExplicit(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
