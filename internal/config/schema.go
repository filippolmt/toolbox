package config

import "reflect"

// SchemaKeys returns every top-level config key — the `mapstructure` tag of
// each exported Config field — in struct-declaration order.
//
// It is the single source of truth for "which config fields exist". The four
// hand-written enumerations that used to drift apart (validation in
// applyValidationTail, provenance in configedit.diffLayer, the resolved
// renderer in cmd, and the annotated example in configexample) each assert
// their coverage against this list in tests, so adding a Config field with a
// `mapstructure` tag forces every site to account for it — a silent gap turns
// a coverage test red instead of shipping. Fields tagged `-` or untagged are
// skipped (not part of the YAML surface).
func SchemaKeys() []string {
	var keys []string
	for f := range reflect.TypeFor[Config]().Fields() {
		tag := f.Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}
		keys = append(keys, tag)
	}
	return keys
}
