package bridge

import (
	"errors"
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
	const dir = "/h/.toolbox/toolbox/bridge"
	rmErr := errors.New("unlinkat /h/.toolbox/toolbox/bridge/run: permission denied")

	if w, err := stateDirOutcome(dir, false, nil); w != "" || err != nil {
		t.Errorf("stateDirOutcome(nil) = (%q, %v), want no warning and no error", w, err)
	}

	w, err := stateDirOutcome(dir, false, rmErr)
	if err != nil {
		t.Errorf("leftovers must not fail the command, got %v", err)
	}
	for _, want := range []string{
		dir,
		"permission denied",
		"close any open toolbox shells (they bind-mount the state dir) and re-run",
	} {
		if !strings.Contains(w, want) {
			t.Errorf("warning = %q, want it to contain %q", w, want)
		}
	}

	w, err = stateDirOutcome(dir, true, rmErr)
	if err == nil {
		t.Fatal("a surviving token must fail the command, not warn")
	}
	if w != "" {
		t.Errorf("warning = %q, want it empty when the outcome is an error", w)
	}
	if !errors.Is(err, rmErr) {
		t.Errorf("err = %v, want it to wrap the removal error", err)
	}
}
