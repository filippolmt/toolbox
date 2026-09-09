package config

import "testing"

// TestMergePeerMessaging asserts the `peer_messaging` toggle round-trips
// through the layered merge: absent anywhere means off (the shipped default,
// ADR 0013), an explicit `true` at either layer opts in, and the project layer
// wins over the global one in both directions.
func TestMergePeerMessaging(t *testing.T) {
	tests := []struct {
		name    string
		global  string
		project string
		want    bool
	}{
		{name: "absent_defaults_off"},
		{name: "project_opts_in", project: "peer_messaging: true\n", want: true},
		{name: "global_opts_in", global: "peer_messaging: true\n", want: true},
		{name: "project_true_wins", global: "peer_messaging: false\n", project: "peer_messaging: true\n", want: true},
		{name: "project_false_wins", global: "peer_messaging: true\n", project: "peer_messaging: false\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Merge([]byte(tc.global), []byte(tc.project), nil)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if cfg.PeerMessaging != tc.want {
				t.Errorf("PeerMessaging = %v, want %v", cfg.PeerMessaging, tc.want)
			}
		})
	}
}

// TestPeerMessagingResolvesFromEnv pins what the `peer_messaging` seed in
// seedEnvBoundKeys is still load-bearing for now that its value equals the
// bool zero: viper's AutomaticEnv resolves TOOLBOX_* only for a key already in
// its key set, so dropping the seed as a redundant `false` would silently
// retire the variable — with every other peer test still green.
func TestPeerMessagingResolvesFromEnv(t *testing.T) {
	t.Setenv("TOOLBOX_PEER_MESSAGING", "true")

	cfg, err := Merge(nil, nil, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !cfg.PeerMessaging {
		t.Error("PeerMessaging = false, want true: TOOLBOX_PEER_MESSAGING did not resolve")
	}
}
