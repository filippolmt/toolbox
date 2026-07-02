// Package configui is the host-side presentation layer for `toolbox config ui`:
// an interactive terminal UI (bubbletea) for viewing and editing .toolbox.yaml
// across the Global (~/.toolbox.yaml) and Repo (./.toolbox.yaml) layers.
//
// It contains no config domain logic. Every value it shows is resolved by
// internal/config (Plan) and attributed by internal/configedit (Compute);
// every edit is validated by internal/configedit Doctor and written by the
// comment-preserving internal/configio path (the same path `config set` uses).
// The bubbletea program is kept deliberately thin over the pure adapter
// functions in adapter.go so the resolve/validate/write behaviour is testable
// without driving a terminal.
package configui
