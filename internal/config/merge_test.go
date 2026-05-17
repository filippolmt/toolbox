package config

import (
	"strings"
	"testing"

	"github.com/filippolmt/toolbox/internal/catalog"
)

// TestMergeScenarios is the table-driven workhorse for the Config Plan
// inspection Seam (CFG-06). Every scenario is expressed as YAML byte literals
// fed into Merge — zero filesystem touches, zero viper-singleton state.
//
// Pitfall 1 reminder (08-RESEARCH §Common Pitfalls): the AutomaticEnv hook
// does NOT round-trip through Unmarshal. Env-var precedence over file works
// only at the Get-style accessor layer in cmd/*. Adding an env-precedence
// subtest here would silently fail. Don't.
func TestMergeScenarios(t *testing.T) {
	type want struct {
		Shell      string
		ToolGcloud bool
		ToolGo     bool
		MountsRoot string
		InfraPath  string
		ErrSubstr  string
	}
	tests := []struct {
		name     string
		global   string
		project  string
		explicit string
		want     want
	}{
		{
			name: "pure_defaults",
			want: want{Shell: "zsh", ToolGcloud: true, ToolGo: true},
		},
		{
			name:   "global_only_disables_gcloud",
			global: "tools:\n  gcloud: false\n",
			want:   want{Shell: "zsh", ToolGcloud: false, ToolGo: true},
		},
		{
			name:    "project_only_disables_go",
			project: "tools:\n  go: false\n",
			want:    want{Shell: "zsh", ToolGcloud: true, ToolGo: false},
		},
		{
			name:    "project_overrides_global",
			global:  "tools:\n  gcloud: false\n",
			project: "tools:\n  gcloud: true\n",
			want:    want{Shell: "zsh", ToolGcloud: true, ToolGo: true},
		},
		{
			name:     "explicit_override_short_circuits_layers",
			global:   "tools:\n  gcloud: false\n",
			project:  "tools:\n  go: false\n",
			explicit: "shell: bash\n",
			want:     want{Shell: "bash", ToolGcloud: true, ToolGo: true},
		},
		{
			name:    "single_tool_disable_preserves_others",
			project: "tools:\n  gcloud: false\n",
			// Verified per-tool below in the assertion loop.
			want: want{Shell: "zsh", ToolGcloud: false, ToolGo: true},
		},
		{
			name:    "shell_default_zsh",
			project: "# empty\n",
			want:    want{Shell: "zsh", ToolGcloud: true, ToolGo: true},
		},
		{
			name:    "shell_explicit_bash",
			project: "shell: bash\n",
			want:    want{Shell: "bash", ToolGcloud: true, ToolGo: true},
		},
		{
			name:    "shell_invalid_rejected",
			project: "shell: fish\n",
			want:    want{ErrSubstr: "fish"},
		},
		{
			name:    "mounts_root_bare_tilde_rejected",
			project: "mounts_root: \"~\"\n",
			want:    want{ErrSubstr: "isolation"},
		},
		{
			name:    "mounts_root_relative_rejected",
			project: "mounts_root: ./relative\n",
			want:    want{ErrSubstr: "mounts_root"},
		},
		{
			name:    "mounts_root_valid_absolute",
			project: "mounts_root: /opt/state\n",
			want:    want{Shell: "zsh", ToolGcloud: true, ToolGo: true, MountsRoot: "/opt/state"},
		},
		{
			name:    "mounts_root_valid_home_relative",
			project: "mounts_root: ~/toolbox-state\n",
			want:    want{Shell: "zsh", ToolGcloud: true, ToolGo: true, MountsRoot: "~/toolbox-state"},
		},
		{
			name:    "shells_map_loaded",
			project: "shells:\n  infra:\n    path: /tmp/infra\n",
			want:    want{Shell: "zsh", ToolGcloud: true, ToolGo: true, InfraPath: "/tmp/infra"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Merge([]byte(tc.global), []byte(tc.project), []byte(tc.explicit))

			if tc.want.ErrSubstr != "" {
				if err == nil {
					t.Fatalf("Merge should have errored, got cfg=%+v", cfg)
				}
				if !strings.Contains(err.Error(), tc.want.ErrSubstr) {
					t.Errorf("err = %q, want substring %q", err, tc.want.ErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if cfg.Shell != tc.want.Shell {
				t.Errorf("Shell = %q, want %q", cfg.Shell, tc.want.Shell)
			}
			if cfg.Tools["gcloud"] != tc.want.ToolGcloud {
				t.Errorf("Tools[gcloud] = %v, want %v", cfg.Tools["gcloud"], tc.want.ToolGcloud)
			}
			if cfg.Tools["go"] != tc.want.ToolGo {
				t.Errorf("Tools[go] = %v, want %v", cfg.Tools["go"], tc.want.ToolGo)
			}
			if tc.want.MountsRoot != "" && cfg.MountsRoot != tc.want.MountsRoot {
				t.Errorf("MountsRoot = %q, want %q", cfg.MountsRoot, tc.want.MountsRoot)
			}
			if tc.want.InfraPath != "" {
				if cfg.Shells["infra"].Path != tc.want.InfraPath {
					t.Errorf("Shells[infra].Path = %q, want %q", cfg.Shells["infra"].Path, tc.want.InfraPath)
				}
			}
			// CFG-06 single_tool_disable_preserves_others: flipping one tool false
			// must leave every other catalog tool at default-true.
			if tc.name == "single_tool_disable_preserves_others" {
				for _, k := range catalog.Keys() {
					if k == "gcloud" {
						continue
					}
					if !cfg.Tools[k] {
						t.Errorf("tool %q should remain true after one-key override, got false", k)
					}
				}
			}
		})
	}
}
