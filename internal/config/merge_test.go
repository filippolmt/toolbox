package config

import (
	"strings"
	"testing"
)

// TestMergeScenarios is the table-driven workhorse for the Config Plan
// inspection Seam. Every scenario is expressed as YAML byte literals
// fed into Merge — zero filesystem touches, zero viper-singleton state.
func TestMergeScenarios(t *testing.T) {
	type want struct {
		Shell           string
		MountsRoot      string
		InfraPath       string
		InheritHostAuth []string
		ErrSubstr       string
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
			want: want{Shell: "zsh"},
		},
		{
			name:    "shell_default_zsh",
			project: "# empty\n",
			want:    want{Shell: "zsh"},
		},
		{
			name:    "shell_explicit_zsh",
			project: "shell: zsh\n",
			want:    want{Shell: "zsh"},
		},
		{
			name:    "shell_bash_rejected_with_migration_hint",
			project: "shell: bash\n",
			want:    want{ErrSubstr: "no longer supported"},
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
			want:    want{Shell: "zsh", MountsRoot: "/opt/state"},
		},
		{
			name:    "mounts_root_valid_home_relative",
			project: "mounts_root: ~/toolbox-state\n",
			want:    want{Shell: "zsh", MountsRoot: "~/toolbox-state"},
		},
		{
			name:    "shells_map_loaded",
			project: "shells:\n  infra:\n    path: /tmp/infra\n",
			want:    want{Shell: "zsh", InfraPath: "/tmp/infra"},
		},
		{
			name:    "inherit_host_auth_single_key",
			project: "inherit_host_auth:\n  - gh\n",
			want:    want{Shell: "zsh", InheritHostAuth: []string{"gh"}},
		},
		{
			name:    "inherit_host_auth_multiple",
			project: "inherit_host_auth: [gh, gcloud]\n",
			want:    want{Shell: "zsh", InheritHostAuth: []string{"gh", "gcloud"}},
		},
		{
			name:    "inherit_host_auth_unknown_rejected",
			project: "inherit_host_auth: [ghh]\n",
			want:    want{ErrSubstr: "unknown CLI"},
		},
		{
			name:    "inherit_host_auth_ineligible_rejected",
			project: "inherit_host_auth: [rtk]\n",
			want:    want{ErrSubstr: "does not support host inheritance"},
		},
		{
			// Legacy tools: block is dropped silently (warning fires to stderr;
			// this test asserts no error + no field set).
			name:    "legacy_tools_block_ignored",
			project: "tools:\n  gcloud: false\n",
			want:    want{Shell: "zsh"},
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
			if tc.want.MountsRoot != "" && cfg.MountsRoot != tc.want.MountsRoot {
				t.Errorf("MountsRoot = %q, want %q", cfg.MountsRoot, tc.want.MountsRoot)
			}
			if tc.want.InfraPath != "" {
				if cfg.Shells["infra"].Path != tc.want.InfraPath {
					t.Errorf("Shells[infra].Path = %q, want %q", cfg.Shells["infra"].Path, tc.want.InfraPath)
				}
			}
			if len(tc.want.InheritHostAuth) > 0 {
				if len(cfg.InheritHostAuth) != len(tc.want.InheritHostAuth) {
					t.Fatalf("InheritHostAuth len = %d, want %d (%v vs %v)",
						len(cfg.InheritHostAuth), len(tc.want.InheritHostAuth),
						cfg.InheritHostAuth, tc.want.InheritHostAuth)
				}
				for i, k := range tc.want.InheritHostAuth {
					if cfg.InheritHostAuth[i] != k {
						t.Errorf("InheritHostAuth[%d] = %q, want %q", i, cfg.InheritHostAuth[i], k)
					}
				}
			}
		})
	}
}
