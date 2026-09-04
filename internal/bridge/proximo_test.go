package bridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
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
	out, exit, err := launchProximo(context.Background(), proximoHost(t, dir), "status", nil, proximoAgentHome{})
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
	out, exit, err := launchProximo(context.Background(), proximoHost(t, dir), "up", nil, proximoAgentHome{})
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

// TestLaunchProximo_MissingBinary pins the composed refusal: resolution fails
// and launchProximo returns it rather than execing something. The candidate
// list is emptied rather than pointed at a temp dir — the well-known paths are
// absolute, and one of them exists in the toolbox image the suite runs in.
func TestLaunchProximo_MissingBinary(t *testing.T) {
	orig := proximoFallbackCandidates
	t.Cleanup(func() { proximoFallbackCandidates = orig })
	proximoFallbackCandidates = func(fsx.Host) []string { return nil }

	_, _, err := launchProximo(context.Background(), proximoHost(t, ""), "status", nil, proximoAgentHome{})
	if err == nil {
		t.Fatal("want error when proximo is not installed")
	}
	if !errors.Is(err, ErrProximoNotInstalled) {
		t.Errorf("err = %v, want ErrProximoNotInstalled", err)
	}
}

func TestLaunchProximo_ContextTimeout(t *testing.T) {
	// Absolute path: the child inherits the daemon's PATH, not the fake's dir.
	// exec: the deadline kill must hit the pipe holder itself, or a surviving
	// sleep child keeps CombinedOutput waiting through the whole WaitDelay.
	dir := fakeProximo(t, `exec /bin/sleep 10`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	out, exit, err := launchProximo(ctx, proximoHost(t, dir), "up", nil, proximoAgentHome{})
	if err == nil {
		t.Fatalf("want error on context timeout, got exit=%d out=%q", exit, out)
	}
}

func TestResolveProximoBinary_FallbackProbes(t *testing.T) {
	// PATH lookup fails; a fallback candidate exists.
	dir := fakeProximo(t, "exit 0")
	bin := filepath.Join(dir, "proximo")
	got, err := resolveProximoBinary(proximoHost(t, ""), []string{filepath.Join(t.TempDir(), "absent"), bin})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != bin {
		t.Errorf("resolved %q, want %q", got, bin)
	}
}

// TestErrProximoNotInstalled_NamesTheHostCommand pins ADR-0004's one
// consequence of leaving `install` host-only: the refusal has to tell the
// caller what to run *on the host*, because nothing in the container can.
func TestErrProximoNotInstalled_NamesTheHostCommand(t *testing.T) {
	msg := ErrProximoNotInstalled.Error()
	for _, want := range []string{"proximo install", "host"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q missing from %q", want, msg)
		}
	}
}

func TestResolveProximoBinary_NotFound(t *testing.T) {
	_, err := resolveProximoBinary(proximoHost(t, ""), []string{filepath.Join(t.TempDir(), "absent")})
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
	dirs := proximoChildPathDirs(proximoHost(t, ""), "/somewhere/bin")
	if len(dirs) == 0 || dirs[0] != "/somewhere/bin" {
		t.Errorf("binDir must lead the result, got %q", dirs)
	}
}

func TestProximoChildPathDirs_SkipEmptyHome(t *testing.T) {
	for _, d := range proximoChildPathDirs(fsx.Host{}, "/somewhere/bin") {
		if !filepath.IsAbs(d) {
			t.Errorf("empty HOME must not yield a relative dir, got %q", d)
		}
	}
}

func TestLaunchProximo_ChildPATHAugmented(t *testing.T) {
	dir := fakeProximo(t, `echo "$PATH"`)
	out, exit, err := launchProximo(context.Background(), proximoHost(t, dir), "status", nil, proximoAgentHome{})
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
	for _, c := range proximoFallbackCandidates(fsx.Host{}) {
		if c == filepath.Join("go", "bin", "proximo") || c == "/go/bin/proximo" {
			t.Errorf("empty HOME must not yield a bogus go/bin candidate, got %q", c)
		}
	}
}

func TestLaunchProximo_ForwardsArgs(t *testing.T) {
	dir := fakeProximo(t, `printf '[%s]' "$@"`)
	out, exit, err := launchProximo(context.Background(), proximoHost(t, dir), "errors", []string{"--since", "5m", "with space"}, proximoAgentHome{})
	if err != nil || exit != 0 {
		t.Fatalf("err = %v, exit = %d", err, exit)
	}
	if got, want := string(out), "[errors][--since][5m][with space]"; got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
}

func TestIsProximoOutputFlag(t *testing.T) {
	// Upstream spells it --out/-o (proximo internal/cli/errors.go:225,695);
	// --output is covered too, cheaply, in case it ever grows the synonym.
	rejected := []string{"-o", "-o=/tmp/x", "-o/tmp/x", "--out", "--out=/tmp/x", "--output", "--output=/tmp/x", "-jo", "-jo/tmp/x"}
	for _, arg := range rejected {
		if !isProximoOutputFlag(arg) {
			t.Errorf("%q must be rejected — via the bridge it writes to the host filesystem", arg)
		}
	}
	// Every real proximo flag except -o is long-only, so none of these collide.
	allowed := []string{"transcript", "dom", "--since", "5m", "--json", "--limit", "--host", "--service", "--all", "--outs", "-", "--", "/tmp/o", "app.test"}
	for _, arg := range allowed {
		if isProximoOutputFlag(arg) {
			t.Errorf("%q must pass — only output redirection is gated", arg)
		}
	}
}

// TestLaunchProximo_SkillRunsInAgentHome pins the home-rewritten execution
// mode: `skill` is the one verb whose effect is files an *in-container* agent
// must read, so it runs against the host directories mountplan binds to
// /home/toolbox/.claude and /home/toolbox/.codex, at global scope.
func TestLaunchProximo_SkillRunsInAgentHome(t *testing.T) {
	dir := fakeProximo(t, `printf 'HOME=%s CODEX_HOME=%s ARGV=%s' "$HOME" "$CODEX_HOME" "$*"`)
	home := t.TempDir()
	out, exit, err := launchProximo(context.Background(), proximoHostAt(t, home, dir), "skill", []string{"install"}, proximoAgentHome{})
	if err != nil || exit != 0 {
		t.Fatalf("err = %v, exit = %d", err, exit)
	}
	agentHome := filepath.Join(home, ".toolbox")
	want := "HOME=" + agentHome + " CODEX_HOME=" + filepath.Join(agentHome, ".codex") + " ARGV=skill install --scope global"
	if got := string(out); got != want {
		t.Errorf("execution = %q, want %q", got, want)
	}
}

// TestLaunchProximo_SkillUsesCallerAgentHome is the case the default cannot
// serve: mounts_root / --profile / inherit_host_auth move the host source
// behind the container's agent homes, so the session's own paths win over the
// daemon's ~/.toolbox guess.
func TestLaunchProximo_SkillUsesCallerAgentHome(t *testing.T) {
	dir := fakeProximo(t, `printf 'HOME=%s CODEX_HOME=%s' "$HOME" "$CODEX_HOME"`)
	host := proximoHost(t, dir) // its home is the default the caller's paths must override
	profile := t.TempDir()
	codex := filepath.Join(profile, ".codex")
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	out, _, err := launchProximo(context.Background(), host, "skill", []string{"install"},
		proximoAgentHome{Home: profile, CodexHome: codex})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "HOME="+profile+" CODEX_HOME="+codex; got != want {
		t.Errorf("execution = %q, want %q", got, want)
	}
}

// TestLaunchProximo_SkillRejectsBadAgentHome: the paths are chosen by the
// container, so a path that is not an existing host directory fails the
// request loudly instead of quietly installing somewhere else.
func TestLaunchProximo_SkillRejectsBadAgentHome(t *testing.T) {
	host := proximoHost(t, fakeProximo(t, `exit 0`))
	for _, agent := range []proximoAgentHome{
		{Home: "relative/path"},
		{Home: filepath.Join(t.TempDir(), "absent")},
		{Home: "/etc/hosts"},                                   // a file, not a directory
		{Home: t.TempDir(), CodexHome: "/tmp/../tmp/unclean/"}, // codex_home checked too
	} {
		if _, _, err := launchProximo(context.Background(), host, "skill", []string{"install"}, agent); err == nil {
			t.Errorf("agent %+v: want error", agent)
		}
	}
}

// TestProximoSkillArgs covers where --scope global may be appended. Upstream
// registers --scope on the install/uninstall leaves only, never on the `skill`
// parent (proximo internal/cli/skill.go:37-43), so appending it to a bare
// `proximo skill` turns a help listing into `unknown flag: --scope`. The
// default scope is `project`, which resolves against the daemon's working
// directory — nowhere an agent looks — so a leaf without one must get global.
func TestProximoSkillArgs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
	}{
		{"install gets global scope", []string{"install"}, []string{"install", "--scope", "global"}},
		{"uninstall too", []string{"uninstall"}, []string{"uninstall", "--scope", "global"}},
		{"leaf flags are preserved", []string{"install", "--agent", "codex"}, []string{"install", "--agent", "codex", "--scope", "global"}},
		{"an explicit scope wins", []string{"install", "--scope", "project"}, []string{"install", "--scope", "project"}},
		{"an explicit scope wins in = form", []string{"install", "--scope=project"}, []string{"install", "--scope=project"}},
		{"no subcommand is left alone", nil, nil},
		{"help is left alone", []string{"--help"}, []string{"--help"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := proximoSkillArgs(tc.args); !slices.Equal(got, tc.want) {
				t.Errorf("proximoSkillArgs(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestLaunchProximo_PlainVerbKeepsHostHome is the other half of the pair:
// every verb but `skill` acts on the host, so it must see the host's own home.
func TestLaunchProximo_PlainVerbKeepsHostHome(t *testing.T) {
	// The child inherits the daemon process's own environment, so this one
	// keeps rewriting $HOME: it asserts on what the process exports, not on
	// what the Host declares.
	dir := fakeProximo(t, `printf 'HOME=%s ARGV=%s' "$HOME" "$*"`)
	home := t.TempDir()
	t.Setenv("HOME", home)
	out, _, err := launchProximo(context.Background(), proximoHostAt(t, home, dir), "errors", []string{"transcript"}, proximoAgentHome{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), "HOME="+home+" ARGV=errors transcript"; got != want {
		t.Errorf("execution = %q, want %q", got, want)
	}
}

func TestLaunchProximo_SkillRequiresHome(t *testing.T) {
	host := proximoHostAt(t, "", fakeProximo(t, `exit 0`))
	if _, _, err := launchProximo(context.Background(), host, "skill", []string{"install"}, proximoAgentHome{}); err == nil {
		t.Fatal("a host with no home must fail the request, not install into /.toolbox")
	}
}

// proximoHost returns a Host with a home of its own whose PATH resolves the
// fake proximo in binDir — or nothing at all when binDir is empty. Declaring
// the host is what keeps these deterministic: this project's own image ships
// a real proximo at /usr/local/bin, so a PATH scrub alone never proved the
// refusal branch was reachable.
func proximoHost(t *testing.T, binDir string) fsx.Host {
	t.Helper()
	return proximoHostAt(t, t.TempDir(), binDir)
}

// proximoHostAt is proximoHost with the home named by the caller, for the
// tests that assert on a path derived from it.
func proximoHostAt(t *testing.T, home, binDir string) fsx.Host {
	t.Helper()
	return fsx.Host{Home: home, LookPath: func(name string) (string, error) {
		if binDir == "" || name != "proximo" {
			return "", exec.ErrNotFound
		}
		bin := filepath.Join(binDir, name)
		if _, err := os.Stat(bin); err != nil {
			return "", exec.ErrNotFound
		}
		return bin, nil
	}}
}
