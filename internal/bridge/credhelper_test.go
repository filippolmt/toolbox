package bridge

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// execPathFixture builds a directory shaped like git's exec-path: an
// executable git-credential-osxkeychain (what a real macOS host has), a
// same-pattern file without the exec bit, and a same-pattern directory. It
// returns the dir and the path of the executable.
func execPathFixture(t *testing.T) (dir, executable string) {
	t.Helper()
	dir = t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	executable = write("git-credential-osxkeychain", 0o755)
	write("git-credential-plain", 0o644)
	if err := os.Mkdir(filepath.Join(dir, "git-credential-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, executable
}

// fakeGit stands in for gitOutput: it answers `--exec-path` with execPath and
// `config --get-all credential.helper` with the given lines, and returns ""
// for anything else — the shape a host without git produces for every query.
func fakeGit(execPath string, helperLines ...string) func(...string) string {
	return func(args ...string) string {
		switch {
		case len(args) == 1 && args[0] == "--exec-path":
			return execPath
		case len(args) > 0 && args[0] == "config":
			return strings.Join(helperLines, "\n")
		default:
			return ""
		}
	}
}

// notOnPath is a PATH lookup that finds nothing.
func notOnPath(string) (string, error) { return "", errors.New("not found") }

const pathHit = "/usr/bin/from-path"

// fromPath is a PATH lookup that finds everything.
func fromPath(string) (string, error) { return pathHit, nil }

func TestEvaluateCredentialHelpers(t *testing.T) {
	// notFound rejects the given helper names, accepts the rest.
	notFound := func(names ...string) func(string) (string, error) {
		miss := map[string]bool{}
		for _, n := range names {
			miss["git-credential-"+n] = true
		}
		return func(bin string) (string, error) {
			if miss[bin] {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + bin, nil
		}
	}

	tests := []struct {
		name       string
		helpers    []string
		goos       string
		lookPath   func(string) (string, error)
		wantOK     bool
		wantSubstr string
	}{
		{"none-macos", nil, "darwin", notFound(), false, "osxkeychain"},
		{"none-linux", nil, "linux", notFound(), false, "libsecret"},
		{"none-other", nil, "windows", notFound(), false, "credential.helper"},
		{"osxkeychain-present", []string{"osxkeychain"}, "darwin", notFound(), true, ""},
		{"libsecret-missing", []string{"libsecret"}, "linux", notFound("libsecret"), false, "git-credential-libsecret"},
		{"builtin-store-ok", []string{"store"}, "linux", notFound("store"), true, ""},
		{"custom-shell-ignored", []string{"!/opt/foo/helper get"}, "linux", notFound(), true, ""},
		{"absolute-path-ignored", []string{"/opt/foo/git-credential-x"}, "linux", notFound(), true, ""},
		{"builtin-with-args", []string{"store --file=/tmp/x"}, "linux", notFound("store"), true, ""},
		{"plain-with-args-present", []string{"osxkeychain --foo"}, "darwin", notFound(), true, ""},
		{"plain-with-args-missing", []string{"libsecret --timeout 5"}, "linux", notFound("libsecret"), false, "git-credential-libsecret"},
		{"chain-one-missing", []string{"osxkeychain", "libsecret"}, "darwin", notFound("libsecret"), false, "libsecret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, advice := evaluateCredentialHelpers(tc.helpers, tc.goos, tc.lookPath)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (advice=%q)", ok, tc.wantOK, advice)
			}
			if tc.wantOK {
				if advice != "" {
					t.Fatalf("advice = %q, want empty", advice)
				}
				return
			}
			if !strings.Contains(advice, tc.wantSubstr) {
				t.Fatalf("advice %q does not contain %q", advice, tc.wantSubstr)
			}
		})
	}
}

// TestCheckHostCredentialHelper pins the wiring, not just the halves: the
// macOS false negative came from resolving helpers with a plain PATH lookup,
// and only the exec-path-first composition in checkHostCredentialHelper fixes
// it. Case "osxkeychain-in-exec-path-only" fails if that composition is ever
// reverted to a bare PATH lookup.
func TestCheckHostCredentialHelper(t *testing.T) {
	dir, _ := execPathFixture(t)

	tests := []struct {
		name       string
		git        func(...string) string
		goos       string
		lookPath   func(string) (string, error)
		wantOK     bool
		wantSubstr string
	}{
		{"osxkeychain-in-exec-path-only", fakeGit(dir, "osxkeychain"), "darwin", notOnPath, true, ""},
		{"helper-nowhere", fakeGit(dir, "libsecret"), "linux", notOnPath, false, "git-credential-libsecret"},
		{"no-git-at-all", fakeGit(""), "darwin", notOnPath, false, "no git credential.helper configured"},
		{"no-exec-path-but-on-path", fakeGit("", "libsecret"), "linux", fromPath, true, ""},
		{"blank-lines-dropped", fakeGit(dir, "", "  ", "osxkeychain", ""), "darwin", notOnPath, true, ""},
		{"chain-second-nowhere", fakeGit(dir, "osxkeychain", "libsecret"), "darwin", notOnPath, false, "libsecret"},
		{"non-executable-in-exec-path", fakeGit(dir, "plain"), "linux", notOnPath, false, "git-credential-plain"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, advice := checkHostCredentialHelper(tc.git, tc.goos, tc.lookPath)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (advice=%q)", ok, tc.wantOK, advice)
			}
			if tc.wantOK {
				if advice != "" {
					t.Fatalf("advice = %q, want empty", advice)
				}
				return
			}
			if !strings.Contains(advice, tc.wantSubstr) {
				t.Fatalf("advice %q does not contain %q", advice, tc.wantSubstr)
			}
		})
	}
}

// TestCheckHostCredentialHelper_Live runs the exported entry point against the
// real host git. The host's configuration is unknown, so the assertion is the
// contract that holds either way: advice is present exactly when the check
// fails, and never both.
func TestCheckHostCredentialHelper_Live(t *testing.T) {
	ok, advice := CheckHostCredentialHelper()
	if ok && advice != "" {
		t.Errorf("ok with advice %q, want empty", advice)
	}
	if !ok && advice == "" {
		t.Error("not ok with no advice, want a remediation line")
	}
}

func TestParseHelperList(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace-only", " \n\t\n ", nil},
		{"single", "osxkeychain\n", []string{"osxkeychain"}},
		{"chain-with-blanks", "osxkeychain\n\n  libsecret  \n", []string{"osxkeychain", "libsecret"}},
		{"keeps-arguments", "store --file=/tmp/x\n", []string{"store --file=/tmp/x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHelperList(tc.out)
			if len(got) != len(tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// TestGitOutput covers the shared query seam: trimmed stdout on success, ""
// on any failure — the value an absent or erroring git hands the caller.
func TestGitOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	got := gitOutput("--exec-path")
	if got == "" {
		t.Error("gitOutput(--exec-path) is empty, want git's exec-path")
	}
	if strings.TrimSpace(got) != got {
		t.Errorf("gitOutput did not trim: %q", got)
	}
	if got := gitOutput("no-such-git-subcommand"); got != "" {
		t.Errorf("gitOutput(bogus) = %q, want empty", got)
	}
}

// TestLookHelperIn pins the resolution order in isolation: git's exec-path
// first, PATH second, and only a regular executable file counts.
func TestLookHelperIn(t *testing.T) {
	dir, executable := execPathFixture(t)

	tests := []struct {
		name     string
		execPath string
		bin      string
		lookPath func(string) (string, error)
		want     string
		wantErr  bool
	}{
		{"exec-path-hit", dir, "git-credential-osxkeychain", notOnPath, executable, false},
		{"empty-exec-path-falls-back", "", "git-credential-osxkeychain", fromPath, pathHit, false},
		{"absent-in-exec-path-falls-back", dir, "git-credential-libsecret", fromPath, pathHit, false},
		{"non-executable-ignored", dir, "git-credential-plain", fromPath, pathHit, false},
		{"directory-ignored", dir, "git-credential-dir", fromPath, pathHit, false},
		{"nowhere-errors", dir, "git-credential-libsecret", notOnPath, "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lookHelperIn(tc.execPath, tc.bin, tc.lookPath)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
