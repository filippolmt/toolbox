package config

import "testing"

// TestMergePeerMessaging asserts the `peer_messaging` opt-in round-trips
// through the layered merge: absent anywhere means off, the project layer
// turns it on, and an explicit project `false` overrides a global `true`.
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
