package config

import "testing"

// TestMergePeerMessaging asserts the `peer_messaging` toggle round-trips
// through the layered merge: absent anywhere means on (the shipped default),
// an explicit `false` at either layer opts out, and a project `false`
// overrides a global `true`.
func TestMergePeerMessaging(t *testing.T) {
	tests := []struct {
		name    string
		global  string
		project string
		want    bool
	}{
		{name: "absent_defaults_on", want: true},
		{name: "project_opts_out", project: "peer_messaging: false\n"},
		{name: "global_opts_out", global: "peer_messaging: false\n"},
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
