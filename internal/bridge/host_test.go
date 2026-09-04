package bridge

import (
	"context"
	"errors"
	"testing"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// testHost returns a Host rooted at a fresh temp dir. Every path this package
// resolves — the state dir, the service file, the proximo fallbacks — hangs
// off it, so declaring one is all a test needs; nothing here touches $HOME.
func testHost(t *testing.T) fsx.Host {
	t.Helper()
	return fsx.Host{Home: t.TempDir()}
}

// TestNewAgentResolvesUnderTheDeclaredHome pins the service supervisor to the
// host it was handed: the unit/plist path is what Install writes and
// Uninstall removes, so an agent resolving against a different home than
// ResolveHostState would manage a service for one home while the state dir
// lived in another.
func TestNewAgentResolvesUnderTheDeclaredHome(t *testing.T) {
	host := testHost(t)
	a, err := NewAgent(host)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if a == nil {
		t.Fatal("NewAgent returned no agent and no error")
	}
}

func TestNewAgentRejectsAHostWithoutAHome(t *testing.T) {
	if _, err := NewAgent(fsx.Host{}); err == nil {
		t.Fatal("NewAgent accepted a host with no home; the service file would land outside any user tree")
	}
}

// TestDaemonProximoDefaultUsesTheDeclaredHost: withHostDefaults wraps
// launchProximo in a closure over the daemon's Host, so the binary is looked
// up on that host's PATH rather than the process's. A host that resolves
// nothing must reach the not-installed refusal without exec'ing anything.
func TestDaemonProximoDefaultUsesTheDeclaredHost(t *testing.T) {
	fns := handlerFns{}.withHostDefaults(fsx.Host{Home: t.TempDir()})
	orig := proximoFallbackCandidates
	t.Cleanup(func() { proximoFallbackCandidates = orig })
	proximoFallbackCandidates = func(fsx.Host) []string { return nil }

	if _, _, err := fns.proximo(context.Background(), "status", nil, proximoAgentHome{}); !errors.Is(err, ErrProximoNotInstalled) {
		t.Fatalf("err = %v, want ErrProximoNotInstalled from a host that resolves no binaries", err)
	}
}
