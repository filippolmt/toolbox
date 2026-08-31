package sessionplan_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestPlanExportsHostAgentHomes pins the env the bridge needs to run
// `proximo skill install` against the directories THIS session actually
// mounted. The container's /home/toolbox/.claude and /home/toolbox/.codex are
// backed by host paths that mounts_root, --profile and inherit_host_auth each
// move, so the daemon cannot derive them — only the session plan knows.
func TestPlanExportsHostAgentHomes(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		cfg                  *config.Config
		profile              func(t *testing.T) *mountplan.Profile
		wantAgent, wantCodex string // relative to HOME
	}{
		{
			name:      "default mounts root",
			cfg:       &config.Config{Shell: "zsh"},
			wantAgent: ".toolbox",
			wantCodex: filepath.Join(".toolbox", ".codex"),
		},
		{
			name:      "mounts_root retargets both",
			cfg:       &config.Config{Shell: "zsh", MountsRoot: "~/elsewhere"},
			wantAgent: "elsewhere",
			wantCodex: filepath.Join("elsewhere", ".codex"),
		},
		{
			name: "profile retargets both",
			cfg:  &config.Config{Shell: "zsh"},
			profile: func(t *testing.T) *mountplan.Profile {
				p, err := mountplan.NewProfile("work", nil)
				if err != nil {
					t.Fatalf("NewProfile: %v", err)
				}
				return p
			},
			wantAgent: filepath.Join(".toolbox", "profiles", "work"),
			wantCodex: filepath.Join(".toolbox", "profiles", "work", ".codex"),
		},
		{
			// inherit_host_auth moves claude alone: the two homes diverge, which
			// is why they travel as two separate values rather than one root.
			name:      "inherit_host_auth claude alone",
			cfg:       &config.Config{Shell: "zsh", InheritHostAuth: []string{"claude"}},
			wantAgent: "",
			wantCodex: filepath.Join(".toolbox", ".codex"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("PATH", t.TempDir())
			workspace := filepath.Join(home, "ws")
			for _, d := range []string{workspace, filepath.Join(home, ".claude")} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					t.Fatalf("setup %s: %v", d, err)
				}
			}
			var profile *mountplan.Profile
			if tc.profile != nil {
				profile = tc.profile(t)
			}
			plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: tc.cfg, Workspace: workspace, Profile: profile})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			for _, want := range []string{
				bridge.HostAgentHomeEnv + "=" + filepath.Join(home, tc.wantAgent),
				bridge.HostCodexHomeEnv + "=" + filepath.Join(home, tc.wantCodex),
			} {
				if !slices.Contains(plan.Env, want) {
					t.Errorf("plan.Env missing %q; env = %v", want, plan.Env)
				}
			}
		})
	}
}

// TestAgentHomeTargetsAreMounted pins the two literals agentHomeEnv matches on
// against the default mount set they are read from. They are the only link
// between a container path and the host source the bridge daemon writes to, so
// a retargeted default would otherwise turn `proximo skill install` into a
// silent no-op instead of a red test.
func TestAgentHomeTargetsAreMounted(t *testing.T) {
	for _, target := range []string{"/home/toolbox/.claude", "/home/toolbox/.codex"} {
		if !slices.ContainsFunc(mountplan.Defaults(), func(m config.Mount) bool { return m.Target == target }) {
			t.Errorf("no default mount targets %q — agentHomeEnv can no longer find the host source", target)
		}
	}
}
