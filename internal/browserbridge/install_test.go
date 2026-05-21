package browserbridge

import (
	"errors"
	"os"
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
	if err := Uninstall(fa); err != nil {
		t.Fatalf("Uninstall: %v", err)
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
