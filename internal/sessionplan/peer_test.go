package sessionplan

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/fsx"
	"github.com/filippolmt/toolbox/internal/mountplan"
)

// TestPlanPeerPidMode asserts the peer opt-in is the only thing that puts the
// session in the shared PID namespace. The value names the toolbox-owned
// anchor container; internal/container is what makes sure it exists.
func TestPlanPeerPidMode(t *testing.T) {
	planHost := fsx.Host{Home: t.TempDir()}
	for _, tc := range []struct {
		name string
		peer bool
		want string
	}{
		{name: "off_by_default"},
		{name: "opted_in", peer: true, want: "container:" + PeerAnchorContainerName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Plan(PlanInput{Host: planHost, Cfg: &config.Config{Shell: "zsh"}, Workspace: t.TempDir(), Peer: tc.peer})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.PidMode != tc.want {
				t.Errorf("PidMode = %q, want %q", plan.PidMode, tc.want)
			}
		})
	}
}

// TestContainerNameFoldsPeer asserts the opt-in is part of the container
// identity on both name branches. Mounts and HostConfig are fixed at
// ContainerCreate, so a session whose opt-in changed must not reattach to a
// container carrying the old PidMode — that failure is silent: the shell
// starts, looks healthy, and simply sees no peers.
func TestContainerNameFoldsPeer(t *testing.T) {
	profile, err := mountplan.NewProfile("work", nil)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	for _, tc := range []struct {
		name      string
		shellName string
		profile   *mountplan.Profile
	}{
		{name: "workspace_session"},
		{name: "workspace_session_with_profile", profile: profile},
		{name: "named_shell", shellName: "infra"},
		{name: "named_shell_with_profile", shellName: "infra", profile: profile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			off := containerName("/repo/demo", tc.shellName, tc.profile, false)
			on := containerName("/repo/demo", tc.shellName, tc.profile, true)
			if off == on {
				t.Errorf("peer opt-in did not change the container name: %q", off)
			}
		})
	}
}

// TestPeerNamedFoldIsInjective pins the fold apart from every name a user can
// type. A `-peer` suffix on the sanitized name is not injective: `toolbox
// shell infra --peer` and `toolbox shell infra-peer` both sanitize into the
// same container, and the second would reattach into a shared PID namespace it
// never asked for — silently, since HostConfig is fixed at ContainerCreate.
// The separator has to be a character SanitizeShellName cannot produce.
func TestPeerNamedFoldIsInjective(t *testing.T) {
	for _, name := range []string{"infra-peer", "infra peer", "infra.peer", "INFRA-PEER"} {
		t.Run(name, func(t *testing.T) {
			optedIn := containerName("/repo/demo", "infra", nil, true)
			typed := containerName("/repo/demo", name, nil, false)
			if optedIn == typed {
				t.Errorf("`toolbox shell %s` collides with the opted-in container %q", name, optedIn)
			}
		})
	}
}
