// Package configui is the host-side presentation layer for `toolbox config ui`:
// an interactive terminal UI (bubbletea) for viewing and editing .toolbox.yaml
// across the Global (~/.toolbox.yaml) and Repo (./.toolbox.yaml) layers.
//
// It contains no config domain logic. Every value it shows is resolved by
// internal/config (Plan), attributed by internal/configedit (Compute) and read
// back per layer by internal/configedit (FileValues); every edit is validated
// by internal/configedit Doctor and written by the comment-preserving
// internal/configio path (the same path `config set` uses). What is left here
// is presentation: which row a key gets, which editor it opens, what the panes
// say.
//
// The bubbletea program is driven in tests the way bubbletea drives it — key
// presses through Update, rendered text off View — so no decision it makes is
// reachable only through a private method.
package configui
