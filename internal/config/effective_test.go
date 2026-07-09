package config

import "testing"

func TestEffectiveValue(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *Config
		key    string
		want   string
		wantOK bool
	}{
		{"shell unset", &Config{}, "shell", "zsh", true},
		{"shell set", &Config{Shell: "zsh"}, "shell", "zsh", true},
		{"agent unset", &Config{}, "agent", DefaultAgent, true},
		{"agent set", &Config{Agent: "codex"}, "agent", "codex", true},
		{"pull unset", &Config{}, "pull", PullAuto, true},
		{"pull set", &Config{Pull: PullAlways}, "pull", "always", true},
		{"image is exempt", &Config{}, "image", "", false},
		{"mounts_root is exempt", &Config{}, "mounts_root", "", false},
		{"bridge tri-state is exempt", &Config{}, "bridge", "", false},
		{"mounts collection is exempt", &Config{}, "mounts", "", false},
		{"unknown key", &Config{}, "nope", "", false},
		{"nil config", nil, "agent", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := EffectiveValue(tc.cfg, tc.key)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("EffectiveValue(%q) = (%q, %v), want (%q, %v)", tc.key, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestEffectiveValueCoversSchema is the anti-drift guard for the accessor:
// every scalar SchemaKeys() field is either answered by EffectiveValue or
// listed here with the reason it stays out. A new Config field that is neither
// answered nor exempt turns this red, forcing a deliberate classification.
func TestEffectiveValueCoversSchema(t *testing.T) {
	exempt := map[string]string{
		"browser_bridge":     "deprecated alias of bridge",
		"image":              "no fallback: empty is the effective 'no override' value",
		"registry_mirror":    "no fallback: empty means no relocation",
		"mounts_root":        "no fallback: empty means ~/.toolbox",
		"bridge":             "tri-state, resolved host-side at runtime",
		"proximo":            "tri-state, resolved host-side at runtime",
		"managed_statusline": "tri-state, resolved host-side at runtime",
		"mounts":             "collection: structured per-entry rendering",
		"inherit_host_auth":  "collection: structured per-entry rendering",
		"shells":             "collection: structured per-entry rendering",
		"sdd":                "collection: structured per-entry rendering",
		"env":                "collection: structured per-entry rendering",
		"worktree":           "collection: structured per-entry rendering",
	}
	for _, key := range SchemaKeys() {
		_, answered := EffectiveValue(&Config{}, key)
		_, isExempt := exempt[key]
		switch {
		case answered && isExempt:
			t.Errorf("key %q is both answered by EffectiveValue and listed exempt", key)
		case !answered && !isExempt:
			t.Errorf("key %q is neither answered by EffectiveValue nor listed exempt: classify it in the accessor or the exempt table", key)
		}
	}
}
