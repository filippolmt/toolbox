package config

import "reflect"

// SchemaKeys returns every top-level config key — the `mapstructure` tag of
// each exported Config field — in struct-declaration order.
//
// It is the single source of truth for "which config fields exist", and the
// declaration order is the order every surface presents the keys in. Keys()
// pairs each of them with its row: the one declaration carrying that key's doc,
// default, example prose, render shape, editor kind, validator and fallback.
// Adding a Config field with a `mapstructure` tag and no row turns
// TestEveryKeyHasACompleteRow red instead of shipping a silent gap.
//
// Consumers that need only the names — provenance (configedit.diffLayer, which
// reflects over Config generically) and the doctor's unknown-key check — read
// this list directly. Fields tagged `-` or untagged are skipped (not part of
// the YAML surface).
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
