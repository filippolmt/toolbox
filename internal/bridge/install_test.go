package bridge

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAgent struct {
	installCalledWith string
	installErr        error
	uninstallCalled   bool
	uninstallErr      error
	status            AgentStatus
}

func (f *fakeAgent) Install(p string) error { f.installCalledWith = p; return f.installErr }
func (f *fakeAgent) Uninstall() error       { f.uninstallCalled = true; return f.uninstallErr }
func (f *fakeAgent) Status() (AgentStatus, error) {
	return f.status, nil
}
func (f *fakeAgent) IsInstalled() bool { return f.status.Installed }

func TestInstall_WritesTokenAndCallsAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fa := &fakeAgent{}
	if err := Install(fa, "/usr/local/bin/toolbox"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	if _, err := os.Stat(s.Token); err != nil {
		t.Errorf("token file not created: %v", err)
	}
	if fa.installCalledWith != "/usr/local/bin/toolbox" {
		t.Errorf("agent.Install called with %q", fa.installCalledWith)
	}
}

func TestUninstall_RemovesStateAndCallsAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fa := &fakeAgent{}
	if err := Install(fa, "/x"); err != nil {
		t.Fatal(err)
	}
	warning, err := Uninstall(fa)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want none on a clean uninstall", warning)
	}
	if !fa.uninstallCalled {
		t.Error("agent.Uninstall not called")
	}
	s, err := ResolveHostState()
	if err != nil {
		t.Fatalf("ResolveHostState: %v", err)
	}
	if _, err := os.Stat(s.Token); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("token still present after uninstall")
	}
}

func TestInstall_MigratesLegacyStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := filepath.Join(home, LegacyHostDir)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "token"), []byte("tok-legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(&fakeAgent{}, "/x"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, HostDir, "token"))
	if err != nil {
		t.Fatalf("migrated token unreadable: %v", err)
	}
	if string(got) != "tok-legacy" {
		t.Errorf("token = %q, want pre-migration token preserved", got)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy dir still present after migration")
	}
}

func TestInstall_StaleLegacyDirRemovedWhenNewExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Install(&fakeAgent{}, "/x"); err != nil {
		t.Fatal(err)
	}
	newTok, err := os.ReadFile(filepath.Join(home, HostDir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, LegacyHostDir)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "token"), []byte("tok-stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Install(&fakeAgent{}, "/x"); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, HostDir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newTok) {
		t.Errorf("token = %q, want existing token untouched by stale legacy dir", got)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale legacy dir must be removed when the new dir already exists")
	}
}

func TestUninstall_RemovesLegacyDirToo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Install(&fakeAgent{}, "/x"); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, LegacyHostDir)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(&fakeAgent{}); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(legacy); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("legacy dir still present after uninstall")
	}
}

func TestStatus_BridgeAndAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fa := &fakeAgent{status: AgentStatus{Installed: true, Running: true, Detail: "ok"}}
	if err := Install(fa, "/x"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	rep, err := Status(fa)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.TokenPresent {
		t.Error("TokenPresent should be true after Install")
	}
	if !rep.AgentInstalled || !rep.AgentRunning {
		t.Errorf("rep = %+v", rep)
	}
}

func TestStateDirOutcome(t *testing.T) {
	rmErr := errors.New("unlinkat /run: permission denied")
	tests := []struct {
		name      string
		token     string // written under the state dir when non-empty
		rmErr     error
		wantErr   bool
		wantInMsg []string
	}{
		{name: "removed", token: "tok", rmErr: nil},
		{
			name:      "leftovers-warn",
			rmErr:     rmErr,
			wantInMsg: []string{"not removed", "permission denied", "close any open toolbox shells (they bind-mount the state dir) and re-run"},
		},
		{name: "surviving-token-fails", token: "tok", rmErr: rmErr, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := HostState{Dir: dir, Token: filepath.Join(dir, "token")}
			if tc.token != "" {
				if err := os.WriteFile(s.Token, []byte(tc.token), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := stateDirOutcome(s, tc.rmErr)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want a hard error, got none")
				}
				if !errors.Is(err, tc.rmErr) {
					t.Errorf("err = %v, want it to wrap the removal error", err)
				}
				if got != "" {
					t.Errorf("warning = %q, want it empty when the outcome is an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(tc.wantInMsg) == 0 && got != "" {
				t.Errorf("warning = %q, want none", got)
			}
			for _, want := range tc.wantInMsg {
				if !strings.Contains(got, want) {
					t.Errorf("warning = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

// A token that cannot be proven gone is treated as live: the stat can fail
// with the same errno that defeated the removal, and exiting 0 there would let
// the next install pick the old token back up. Provoked with ENOTDIR rather
// than a chmod, which `make go-test` (running as root) would stat straight
// through.
func TestStateDirOutcome_UnprovableTokenFailsClosed(t *testing.T) {
	dir := t.TempDir()
	notADir := filepath.Join(dir, "run")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := HostState{Dir: dir, Token: filepath.Join(notADir, "token")}
	if _, err := os.Stat(s.Token); errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat must fail with something other than NotExist, got %v", err)
	}
	if _, err := stateDirOutcome(s, errors.New("unlinkat /run: permission denied")); err == nil {
		t.Error("a token that cannot be proven gone must fail the command")
	}
}
