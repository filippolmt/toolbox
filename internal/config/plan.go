// Package config — Plan + Merge are the external seams that own configuration
// resolution end-to-end. Plan handles the filesystem side (walk-up + file IO);
// Merge is the pure-inspection sibling that layers global / project / explicit
// YAML byte buffers into a fully-validated *Config without touching the
// filesystem. Sections in this file are laid out in the order Plan calls them
// internally (Seam → Walk-Up → Defaults Seeding → File Load → Validation →
// Helpers) so reading top-to-bottom matches the runtime call graph.
package config

import (
	"errors"
	"os"
	"path/filepath"
)

// =============================================================================
// Seam (Plan + Merge)
// =============================================================================

// Plan resolves the full configuration pipeline and returns a fully-validated
// *Config. searchFrom is the directory walk-up starts from (typically CWD).
// explicitOverride is the resolved --config flag value ("" when unset).
//
// Errors propagate; no os.Exit inside the Seam — the caller in cmd/ decides
// how to surface failure. Plan owns walk-up + file IO; Merge (the pure-
// inspection sibling) owns the byte-level merge mechanics.
func Plan(searchFrom string, explicitOverride string) (*Config, error) {
	if explicitOverride == "" {
		_ = walkUp(searchFrom) // wired in Plan 02 — full body
	}
	return nil, errors.New("config.Plan: not yet implemented")
}

// Merge layers global / project / explicit YAML byte buffers (any of which may
// be nil) into a fully-validated *Config. Pure: no filesystem side-effects.
// Each invocation uses a fresh *viper.Viper so callers see no cross-call state.
func Merge(global, project, explicit []byte) (*Config, error) {
	return nil, errors.New("config.Merge: not yet implemented")
}

// =============================================================================
// Walk-Up
// =============================================================================

// walkUp searches upward from start for a .toolbox.yaml file and returns the
// first match's absolute path. Search stops at the user's HOME directory (so
// the global ~/.toolbox.yaml is not re-read as a project file) and at the
// filesystem root. Returns "" when no project config is found.
//
// HOME-resolution failures are swallowed deliberately (Pitfall 5 in
// 08-RESEARCH.md): a misconfigured HOME must not prevent walk-up from
// reaching the filesystem root. The home == "" short-circuit at the top of
// the loop becomes a no-op in that case.
func walkUp(start string) string {
	home, _ := os.UserHomeDir()
	cur := filepath.Clean(start)
	for {
		if home != "" && cur == home {
			return ""
		}
		candidate := filepath.Join(cur, ".toolbox.yaml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// =============================================================================
// Defaults Seeding
// =============================================================================

// (Plan 02 lands seedToolDefaults here.)

// =============================================================================
// File Load
// =============================================================================

// (Plan 02 lands the global / project read helpers here, if extracted.)

// =============================================================================
// Validation
// =============================================================================

// (Plan 02 lands the validation tail helper here, if extracted.)

// =============================================================================
// Helpers
// =============================================================================

// (Catch-all for future utilities.)
