package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// TestHostBestEffortKeepsThePathWhenTheHomeIsGone: the read-only surfaces
// degrade on an unresolvable home, but the PATH never depended on it.
// proximo.CAPath asks the binary for the CA path before falling back to
// ~/.proximo, so dropping the resolver here would silently lose the proximo CA
// mount from `mounts list` on a host whose $HOME is broken.
func TestHostBestEffortKeepsThePathWhenTheHomeIsGone(t *testing.T) {
	t.Setenv("HOME", "")
	host := hostBestEffort()
	if host.Home != "" {
		t.Errorf("Home = %q, want empty on an unresolvable home", host.Home)
	}
	if _, err := host.Look("sh"); err != nil {
		t.Errorf("Look(sh) = %v; the fallback host must keep the real PATH", err)
	}
}

func TestHostBestEffortCarriesTheRealHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if got := hostBestEffort(); got.Home != dir {
		t.Errorf("Home = %q, want %q", got.Home, dir)
	}
}

// TestBridgeAgentPairsHostAndAgent: every `toolbox bridge` subcommand but
// `daemon` opens with this pair, and Install/Uninstall/Status address the host
// while the agent writes the service file. They must name the same home.
func TestBridgeAgentPairsHostAndAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	host, a, err := bridgeAgent()
	if err != nil {
		t.Fatalf("bridgeAgent: %v", err)
	}
	if host.Home != dir {
		t.Errorf("host.Home = %q, want %q", host.Home, dir)
	}
	if a == nil {
		t.Error("bridgeAgent returned no agent and no error")
	}
}

func TestBridgeAgentFailsOnAnUnresolvableHome(t *testing.T) {
	t.Setenv("HOME", "")
	if _, _, err := bridgeAgent(); err == nil {
		t.Fatal("bridgeAgent accepted an unresolvable home; the service file would land outside any user tree")
	}
}

// TestWarnIfNewProfile covers both halves of the notice's guard. The
// empty-home arm is live: runShell passes hostBestEffort() here, precisely so
// a broken $HOME skips the notice instead of stat'ing a cwd-relative
// .toolbox/profiles/<name>.
func TestWarnIfNewProfile(t *testing.T) {
	home := t.TempDir()
	existing := filepath.Join(home, ".toolbox", "profiles", "work")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for _, tc := range []struct {
		name  string
		host  fsx.Host
		p     *mountplan.Profile
		warns bool
	}{
		{"new profile warns", fsx.Host{Home: home}, &mountplan.Profile{Name: "fresh"}, true},
		{"existing profile is silent", fsx.Host{Home: home}, &mountplan.Profile{Name: "work"}, false},
		{"no profile is silent", fsx.Host{Home: home}, nil, false},
		{"no home is silent", fsx.Host{}, &mountplan.Profile{Name: "fresh"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := captureCmdStderr(t, func() { warnIfNewProfile(tc.host, tc.p) })
			if warned := strings.Contains(got, "creating new profile"); warned != tc.warns {
				t.Errorf("warned = %v, want %v (stderr: %q)", warned, tc.warns, got)
			}
		})
	}
}

// captureCmdStderr collects what fn writes to os.Stderr. These notices go to
// the real stderr rather than a cobra writer — they must survive a redirected
// stdout — so the swap is the only way to read them back.
func captureCmdStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	fn()
	os.Stderr = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// TestCommandsThatNeedAHomeRefuseCleanly is the other side of hostBestEffort:
// the commands that cannot work without a home resolve it early and return the
// error, rather than carrying an empty base path into container creation or a
// service file. Early is what makes this reachable — each returns before it
// touches Docker or a supervisor.
func TestCommandsThatNeedAHomeRefuseCleanly(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(t *testing.T) error
	}{
		// install and uninstall are not here: EnsureUserContext refuses them
		// before the host is resolved, and the suite runs as root inside the
		// golang image, so they never reach the seam under test. status takes
		// the same bridgeAgent path without that gate.
		{"bridge status", func(*testing.T) error { return bridgeStatusCmd.RunE(bridgeStatusCmd, nil) }},
		{"shell", func(t *testing.T) error {
			withCfg(t, &config.Config{Shell: "zsh"})
			return runShell(shellCmd, nil)
		}},
		{"worktree session", func(t *testing.T) error {
			// nil client: openSession resolves the host before it reaches
			// Docker, which is the whole point of resolving it early.
			return openSession(context.Background(), nil, t.TempDir(), t.TempDir(), "b", "claude", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", "")
			err := tc.call(t)
			if err == nil {
				t.Fatal("command succeeded with an unresolvable home")
			}
			if !strings.Contains(err.Error(), "resolve home directory") {
				t.Errorf("err = %v, want the home-resolution failure", err)
			}
		})
	}
}
