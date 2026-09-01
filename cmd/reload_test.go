package cmd

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/reload"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// capturedExec is what the syscall would have been handed. The syscall itself
// is the one line on this path no automated gate can run — it replaces the
// test binary — so what it is *given* is asserted instead, exhaustively,
// because arguments are the only thing a branchless exec can get wrong.
type capturedExec struct {
	argv0 string
	argv  []string
	env   []string
}

// stubExecSelf swaps both seams and returns what execReload composed. The
// stubbed exec returns an error because the real one never returns at all:
// a nil here would let a test pass while the process silently continued.
func stubExecSelf(t *testing.T, bin string) *capturedExec {
	t.Helper()
	var got capturedExec

	origExec, origPath := execSelf, executablePath
	execSelf = func(argv0 string, argv []string, env []string) error {
		got = capturedExec{argv0: argv0, argv: argv, env: env}
		return errors.New("exec stub: did not replace the process")
	}
	executablePath = func() (string, error) { return bin, nil }
	t.Cleanup(func() { execSelf, executablePath = origExec, origPath })

	return &got
}

// envValue reads one variable out of a docker-style KEY=VALUE list.
func envValue(env []string, key string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v
		}
	}
	return ""
}

// TestExecReloadCarriesTheHandover asserts the three things the tail call must
// get right: the binary it re-execs, the argv it re-execs with, and the
// payload it hands over. The re-exec is what makes a `brew upgrade` landed
// mid-session take effect, so it must reach the CLI on disk *now* — resolved,
// not the name this process happened to be invoked under.
func TestExecReloadCarriesTheHandover(t *testing.T) {
	got := stubExecSelf(t, "/opt/homebrew/bin/toolbox")

	origArgs := os.Args
	os.Args = []string{"toolbox", "shell", "--peer=false"}
	t.Cleanup(func() { os.Args = origArgs })

	from := reload.From{
		Container:   "toolbox-proj-deadbeef",
		Cwd:         "/workspace/pkg",
		ImageDigest: "sha256:old",
		CLIVersion:  "v0.1.0",
	}
	if err := execReload(&from); err == nil {
		t.Fatal("execReload returned nil — the stub says the process was not replaced")
	}

	if got.argv0 != "/opt/homebrew/bin/toolbox" {
		t.Errorf("exec'd %q, want the resolved binary", got.argv0)
	}
	wantArgv := []string{"/opt/homebrew/bin/toolbox", "shell", "--peer=false"}
	if !slices.Equal(got.argv, wantArgv) {
		t.Errorf("argv = %q, want %q", got.argv, wantArgv)
	}

	decoded, err := reload.Decode(envValue(got.env, reload.FromEnv))
	if err != nil {
		t.Fatalf("the payload the exec carried is unreadable: %v", err)
	}
	if *decoded != from {
		t.Errorf("payload = %+v, want %+v", *decoded, from)
	}
}

// A bare `toolbox shell` has no flags to replay, and argv[0] must still be the
// resolved binary rather than whatever name reached this process.
func TestExecReloadWithNoArguments(t *testing.T) {
	got := stubExecSelf(t, "/usr/local/bin/toolbox")

	origArgs := os.Args
	os.Args = []string{"tb"}
	t.Cleanup(func() { os.Args = origArgs })

	_ = execReload(&reload.From{Container: "c"})

	if !slices.Equal(got.argv, []string{"/usr/local/bin/toolbox"}) {
		t.Errorf("argv = %q, want just the resolved binary", got.argv)
	}
}

// An unresolvable binary must fail loudly and name the way back. Nothing has
// been destroyed at this point — the teardown belongs to the process that
// would have replaced this one — so the session is recoverable, and the shell
// that would normally say how is already gone.
func TestExecReloadWithoutABinaryNamesTheWayBack(t *testing.T) {
	origPath := executablePath
	executablePath = func() (string, error) { return "", errors.New("no /proc/self/exe") }
	t.Cleanup(func() { executablePath = origPath })

	err := execReload(&reload.From{Container: "c"})
	if err == nil {
		t.Fatal("execReload accepted an unresolvable binary")
	}
	if !strings.Contains(err.Error(), "toolbox shell") {
		t.Errorf("error does not name the re-entry command: %v", err)
	}
}

// TestTakeReloadHandover pins the asymmetry at the cmd boundary: no payload is
// the ordinary case and must be silent, while a payload that cannot be read is
// a hard error naming the way back — never a shell that starts anyway and
// leaves the old container holding the name.
func TestTakeReloadHandover(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		if err := os.Unsetenv(reload.FromEnv); err != nil {
			t.Fatalf("Unsetenv: %v", err)
		}
		got, err := takeReloadHandover()
		if got != nil || err != nil {
			t.Errorf("takeReloadHandover() = (%+v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		t.Setenv(reload.FromEnv, `{"cwd":"/workspace"}`)
		_, err := takeReloadHandover()
		if err == nil {
			t.Fatal("takeReloadHandover accepted a payload with no container name")
		}
		if !strings.Contains(err.Error(), "toolbox shell") {
			t.Errorf("error does not name the re-entry command: %v", err)
		}
	})

	t.Run("readable", func(t *testing.T) {
		t.Setenv(reload.FromEnv, `{"container":"toolbox-proj-deadbeef","cwd":"/workspace"}`)
		got, err := takeReloadHandover()
		if err != nil {
			t.Fatalf("takeReloadHandover: %v", err)
		}
		if got == nil || got.Container != "toolbox-proj-deadbeef" {
			t.Fatalf("handover = %+v, want the container name", got)
		}
		if _, ok := os.LookupEnv(reload.FromEnv); ok {
			t.Errorf("%s survived into the environment a container env is built from", reload.FromEnv)
		}
	})
}

// TestRunSessionTailCall pins the one decision this seam owns: a session that
// simply ended must not exec, and one that asked for a reload must. Getting
// the first wrong re-runs `toolbox shell` after every exit; getting the second
// wrong leaves the developer on the old image with the marker consumed and
// nothing to show for it.
func TestRunSessionTailCall(t *testing.T) {
	origShell := shellFn
	t.Cleanup(func() { shellFn = origShell })

	t.Run("ordinary exit does not exec", func(t *testing.T) {
		got := stubExecSelf(t, "/usr/local/bin/toolbox")
		shellFn = func(context.Context, client.APIClient, *sessionplan.SessionPlan) (*reload.From, error) {
			return nil, nil
		}
		if err := runSession(context.Background(), nil, nil); err != nil {
			t.Fatalf("runSession: %v", err)
		}
		if got.argv0 != "" {
			t.Errorf("an ordinary exit re-exec'd %q", got.argv0)
		}
	})

	t.Run("a reload request tail-calls", func(t *testing.T) {
		got := stubExecSelf(t, "/usr/local/bin/toolbox")
		shellFn = func(context.Context, client.APIClient, *sessionplan.SessionPlan) (*reload.From, error) {
			return &reload.From{Container: "toolbox-proj-deadbeef"}, nil
		}
		if err := runSession(context.Background(), nil, nil); err == nil {
			t.Fatal("runSession returned nil — the stub says the process was not replaced")
		}
		if got.argv0 != "/usr/local/bin/toolbox" {
			t.Errorf("the reload did not reach the exec: argv0 = %q", got.argv0)
		}
	})

	t.Run("a failed session never execs", func(t *testing.T) {
		got := stubExecSelf(t, "/usr/local/bin/toolbox")
		shellFn = func(context.Context, client.APIClient, *sessionplan.SessionPlan) (*reload.From, error) {
			return nil, errors.New("create failed")
		}
		if err := runSession(context.Background(), nil, nil); err == nil {
			t.Fatal("runSession swallowed the session error")
		}
		if got.argv0 != "" {
			t.Errorf("a failed session re-exec'd %q", got.argv0)
		}
	})
}
