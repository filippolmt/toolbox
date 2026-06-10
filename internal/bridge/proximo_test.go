package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeProximo writes an executable shell script named "proximo" into a temp
// dir and returns the dir, for PATH-based resolution tests.
func fakeProximo(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh script fake not runnable on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "proximo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLaunchProximo_OutputAndZeroExit(t *testing.T) {
	dir := fakeProximo(t, `echo "stack is up"; exit 0`)
	t.Setenv("PATH", dir)
	out, exit, err := launchProximo(context.Background(), "status")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	if got := string(out); got != "stack is up\n" {
		t.Errorf("output = %q", got)
	}
}

func TestLaunchProximo_NonZeroExitIsNotAnError(t *testing.T) {
	dir := fakeProximo(t, `echo "boom" >&2; exit 3`)
	t.Setenv("PATH", dir)
	out, exit, err := launchProximo(context.Background(), "up")
	if err != nil {
		t.Fatalf("non-zero exit must not be an error, got %v", err)
	}
	if exit != 3 {
		t.Errorf("exit = %d, want 3", exit)
	}
	if got := string(out); got != "boom\n" {
		t.Errorf("combined output = %q", got)
	}
}

func TestLaunchProximo_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	_, _, err := launchProximo(context.Background(), "status")
	if err == nil {
		t.Fatal("want error when proximo is not installed")
	}
}

func TestLaunchProximo_ContextTimeout(t *testing.T) {
	// Absolute path: t.Setenv below replaces PATH, which the script inherits.
	// exec: the deadline kill must hit the pipe holder itself, or a surviving
	// sleep child keeps CombinedOutput waiting through the whole WaitDelay.
	dir := fakeProximo(t, `exec /bin/sleep 10`)
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out, exit, err := launchProximo(ctx, "up")
	if err == nil {
		t.Fatalf("want error on context timeout, got exit=%d out=%q", exit, out)
	}
}

func TestResolveProximoBinary_FallbackProbes(t *testing.T) {
	// PATH lookup fails; a fallback candidate exists.
	t.Setenv("PATH", t.TempDir())
	dir := fakeProximo(t, "exit 0")
	bin := filepath.Join(dir, "proximo")
	got, err := resolveProximoBinary([]string{filepath.Join(t.TempDir(), "absent"), bin})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != bin {
		t.Errorf("resolved %q, want %q", got, bin)
	}
}

func TestResolveProximoBinary_NotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := resolveProximoBinary([]string{filepath.Join(t.TempDir(), "absent")})
	if err == nil {
		t.Fatal("want error when nothing resolves")
	}
	if !errors.Is(err, ErrProximoNotInstalled) {
		t.Errorf("err = %v, want ErrProximoNotInstalled", err)
	}
}

func TestAppendPathDirs(t *testing.T) {
	sep := string(os.PathListSeparator)
	tests := []struct {
		name string
		env  []string
		dirs []string
		want []string
	}{
		{
			name: "appends missing dirs after existing entries",
			env:  []string{"PATH=/usr/bin" + sep + "/bin"},
			dirs: []string{"/opt/homebrew/bin"},
			want: []string{"PATH=/usr/bin" + sep + "/bin" + sep + "/opt/homebrew/bin"},
		},
		{
			name: "skips dirs already on PATH",
			env:  []string{"PATH=/usr/bin" + sep + "/opt/homebrew/bin"},
			dirs: []string{"/opt/homebrew/bin", "/usr/local/bin"},
			want: []string{"PATH=/usr/bin" + sep + "/opt/homebrew/bin" + sep + "/usr/local/bin"},
		},
		{
			name: "creates PATH entry when none exists",
			env:  []string{"HOME=/root"},
			dirs: []string{"/usr/local/bin"},
			want: []string{"HOME=/root", "PATH=/usr/local/bin"},
		},
		{
			name: "empty PATH value yields no leading separator",
			env:  []string{"PATH="},
			dirs: []string{"/usr/local/bin"},
			want: []string{"PATH=/usr/local/bin"},
		},
		{
			name: "empty dirs returns env unchanged",
			env:  []string{"PATH=/usr/bin", "HOME=/root"},
			dirs: nil,
			want: []string{"PATH=/usr/bin", "HOME=/root"},
		},
		{
			name: "dedupes duplicate input dirs",
			env:  []string{"PATH=/usr/bin"},
			dirs: []string{"/usr/local/bin", "/usr/local/bin"},
			want: []string{"PATH=/usr/bin" + sep + "/usr/local/bin"},
		},
		{
			name: "non-PATH vars pass through untouched",
			env:  []string{"HOME=/root", "PATH=/usr/bin", "LANG=C"},
			dirs: []string{"/usr/local/bin"},
			want: []string{"HOME=/root", "PATH=/usr/bin" + sep + "/usr/local/bin", "LANG=C"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendPathDirs(tt.env, tt.dirs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("appendPathDirs(%q, %q) = %q, want %q", tt.env, tt.dirs, got, tt.want)
			}
		})
	}
}

func TestProximoChildPathDirs_IncludesBinDir(t *testing.T) {
	dirs := proximoChildPathDirs("/somewhere/bin")
	if len(dirs) == 0 || dirs[0] != "/somewhere/bin" {
		t.Errorf("binDir must lead the result, got %q", dirs)
	}
}

func TestProximoChildPathDirs_SkipEmptyHome(t *testing.T) {
	t.Setenv("HOME", "")
	for _, d := range proximoChildPathDirs("/somewhere/bin") {
		if !filepath.IsAbs(d) {
			t.Errorf("empty HOME must not yield a relative dir, got %q", d)
		}
	}
}

func TestLaunchProximo_ChildPATHAugmented(t *testing.T) {
	dir := fakeProximo(t, `echo "$PATH"`)
	t.Setenv("PATH", dir)
	out, exit, err := launchProximo(context.Background(), "status")
	if err != nil || exit != 0 {
		t.Fatalf("err = %v, exit = %d", err, exit)
	}
	childPath := strings.TrimSpace(string(out))
	for _, want := range []string{dir, "/opt/homebrew/bin"} {
		if !strings.Contains(childPath, want) {
			t.Errorf("child PATH %q missing %q", childPath, want)
		}
	}
}

func TestProximoFallbackCandidates_SkipEmptyHome(t *testing.T) {
	t.Setenv("HOME", "")
	for _, c := range proximoFallbackCandidates() {
		if c == filepath.Join("go", "bin", "proximo") || c == "/go/bin/proximo" {
			t.Errorf("empty HOME must not yield a bogus go/bin candidate, got %q", c)
		}
	}
}
