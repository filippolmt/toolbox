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
