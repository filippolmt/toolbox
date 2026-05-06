// Package config — Plan + Merge are the external seams that own configuration
// resolution end-to-end. Plan handles the filesystem side (walk-up + file IO);
// Merge is the pure-inspection sibling that layers global / project / explicit
// YAML byte buffers into a fully-validated *Config without touching the
// filesystem. Sections in this file are laid out in the order Plan calls them
// internally (Seam → Walk-Up → Defaults Seeding → File Load → Validation →
// Helpers) so reading top-to-bottom matches the runtime call graph.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"

	"github.com/filippolmt/toolbox/internal/catalog"
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
	var globalBytes, projectBytes, explicitBytes []byte

	if explicitOverride != "" {
		b, err := os.ReadFile(explicitOverride)
		if err != nil {
			return nil, fmt.Errorf("read --config %q: %w", explicitOverride, err)
		}
		explicitBytes = b
	} else {
		// Global ~/.toolbox.yaml — optional. Missing file is non-fatal
		// (Pitfall 5 + cmd/root.go pre-Plan-08 line 91).
		if home, herr := os.UserHomeDir(); herr == nil && home != "" {
			globalPath := filepath.Join(home, ".toolbox.yaml")
			b, rerr := os.ReadFile(globalPath)
			switch {
			case rerr == nil:
				globalBytes = b
			case os.IsNotExist(rerr):
				// OK — global is optional.
			default:
				return nil, fmt.Errorf("read global config %q: %w", globalPath, rerr)
			}
		}

		// Project .toolbox.yaml via walk-up — optional.
		if path := walkUp(searchFrom); path != "" {
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil, fmt.Errorf("read project config %q: %w", path, rerr)
			}
			projectBytes = b
		}
	}

	return Merge(globalBytes, projectBytes, explicitBytes)
}

// Merge layers global / project / explicit YAML byte buffers (any of which may
// be nil) into a fully-validated *Config. Pure: no filesystem side-effects.
// Each invocation uses a fresh *viper.Viper so callers see no cross-call state.
func Merge(global, project, explicit []byte) (*Config, error) {
	vp := viper.New()
	vp.SetConfigType("yaml")

	// Defaults Seeding stage.
	seedToolDefaults(vp)

	// File Load stage. explicit short-circuits global + project — mirrors
	// cmd/root.go::initConfig (pre-Plan-08) lines 76-114.
	if len(explicit) > 0 {
		if err := vp.MergeConfig(bytes.NewReader(explicit)); err != nil {
			return nil, fmt.Errorf("parse --config bytes: %w", err)
		}
	} else {
		if len(global) > 0 {
			if err := vp.MergeConfig(bytes.NewReader(global)); err != nil {
				return nil, fmt.Errorf("parse global config bytes: %w", err)
			}
		}
		if len(project) > 0 {
			if err := vp.MergeConfig(bytes.NewReader(project)); err != nil {
				return nil, fmt.Errorf("parse project config bytes: %w", err)
			}
		}
	}

	// Env-prefix overrides — applied to this instance only (D-09).
	vp.SetEnvPrefix("TOOLBOX")
	vp.AutomaticEnv()

	// Unmarshal.
	cfg := &Config{}
	if err := vp.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Tool-defaults backstop (Pitfall 1 — Unmarshal misses defaulted map keys).
	fillToolDefaultsBackstop(cfg)

	// Validation tail.
	if err := applyValidationTail(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
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

// seedToolDefaults applies catalog.Keys() to vp as tools.<key>=true.
// Phase 08 D-10: Plan owns this seeding; cmd/root.go::setDefaults is
// deleted in Plan 04 alongside initConfig thinning.
//
// Pitfall 2 in 08-RESEARCH: dotted-key SetDefault per scalar. Do NOT
// SetDefault a nested object (breaks MergeConfig).
//
// Pitfall 8: mount defaults are NOT seeded here — that responsibility
// stays inside the mount-plan package. Seeding the mounts slice from
// here would double-merge against the mount-plan internal defaults,
// producing duplicate binds.
func seedToolDefaults(vp *viper.Viper) {
	for _, k := range catalog.Keys() {
		vp.SetDefault("tools."+k, true)
	}
}

// =============================================================================
// File Load
// =============================================================================

// (Plan 02 keeps the file-read logic inline inside Plan. If a future plan
// extracts read helpers, they land under this banner.)

// =============================================================================
// Validation
// =============================================================================

// applyValidationTail runs ValidateMountsRoot, the shell default
// fallback, and ValidateShell. Phase 08 D-12: validators run inside the
// Seam; cmd/* never calls Validate* directly. Migrating Load() in Plan
// 05 keeps the same call order so the deprecated wrapper preserves
// today's error surface.
func applyValidationTail(cfg *Config) error {
	if err := ValidateMountsRoot(cfg.MountsRoot); err != nil {
		return err
	}
	if cfg.Shell == "" {
		cfg.Shell = "zsh"
	}
	if err := ValidateShell(cfg.Shell); err != nil {
		return err
	}
	return nil
}

// =============================================================================
// Helpers
// =============================================================================

// fillToolDefaultsBackstop fills cfg.Tools entries that the YAML did
// not override. Mirrors Load() lines 144-151 verbatim — Unmarshal does
// not pull viper's SetDefault entries into a map[string]bool when the
// YAML omits them.
func fillToolDefaultsBackstop(cfg *Config) {
	if cfg.Tools == nil {
		cfg.Tools = map[string]bool{}
	}
	for _, k := range catalog.Keys() {
		if _, ok := cfg.Tools[k]; !ok {
			cfg.Tools[k] = true
		}
	}
}
