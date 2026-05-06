package config

import "github.com/filippolmt/toolbox/internal/catalog"

// DefaultTools returns the canonical default tools map. Thin shim over
// catalog.Defaults — kept here so callers that already imported
// internal/config for the Config type don't need a second import. Tool
// source-of-truth lives in internal/catalog (Phase 07).
func DefaultTools() map[string]bool { return catalog.Defaults() }

// IsDefaultTools reports whether the given tools map matches the defaults.
// Thin shim over catalog.IsDefault.
func IsDefaultTools(m map[string]bool) bool { return catalog.IsDefault(m) }
