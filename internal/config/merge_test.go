package config

import (
	"reflect"
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
		SDD             map[string]SDDSkill
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
		{
			// Bool shorthand decodes to {Enabled} with nil Steps (registry
			// defaults apply downstream in sessionplan).
			name:    "sdd_bool_shorthand",
			project: "sdd:\n  gsd: true\n  bmad: false\n",
			want: want{Shell: "zsh", SDD: map[string]SDDSkill{
				"gsd":  {Enabled: true},
				"bmad": {Enabled: false},
			}},
		},
		{
			// Object form implies enabled: true; steps replace the registry
			// defaults wholesale (#317).
			name:    "sdd_steps_override_implies_enabled",
			project: "sdd:\n  gsd:\n    steps:\n      - [\"--claude\", \"--global\", \"--config-dir\", \"./.claude\"]\n",
			want: want{Shell: "zsh", SDD: map[string]SDDSkill{
				"gsd": {Enabled: true, Steps: [][]string{
					{"--claude", "--global", "--config-dir", "./.claude"},
				}},
			}},
		},
		{
			name:    "sdd_steps_with_explicit_enabled_false",
			project: "sdd:\n  gsd:\n    enabled: false\n    steps:\n      - [\"--claude\", \"--local\"]\n",
			want: want{Shell: "zsh", SDD: map[string]SDDSkill{
				"gsd": {Enabled: false, Steps: [][]string{{"--claude", "--local"}}},
			}},
		},
		{
			// Unknown key with bool shorthand stays lenient (silently dropped
			// by sessionplan) — only the steps override is strict.
			name:    "sdd_unknown_key_bool_tolerated",
			project: "sdd:\n  gds: true\n",
			want: want{Shell: "zsh", SDD: map[string]SDDSkill{
				"gds": {Enabled: true},
			}},
		},
		{
			name:    "sdd_unknown_key_with_steps_rejected",
			project: "sdd:\n  gds:\n    steps:\n      - [\"--claude\", \"--local\"]\n",
			want:    want{ErrSubstr: "unknown integration"},
		},
		{
			name:    "sdd_empty_steps_rejected",
			project: "sdd:\n  gsd:\n    steps: []\n",
			want:    want{ErrSubstr: "at least one step"},
		},
		{
			// Token with whitespace would shift arg boundaries when the bash
			// bootstrap re-splits the encoded steps string.
			name:    "sdd_step_token_with_space_rejected",
			project: "sdd:\n  gsd:\n    steps:\n      - [\"--config-dir\", \"./my dir\"]\n",
			want:    want{ErrSubstr: "invalid token"},
		},
		{
			name:    "sdd_step_token_with_separator_rejected",
			project: "sdd:\n  gsd:\n    steps:\n      - [\"--claude;--codex\"]\n",
			want:    want{ErrSubstr: "invalid token"},
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
			if tc.want.SDD != nil && !reflect.DeepEqual(cfg.SDD, tc.want.SDD) {
				t.Errorf("SDD = %#v, want %#v", cfg.SDD, tc.want.SDD)
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
