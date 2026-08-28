package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestResolvePeer asserts --peer is a per-session override of the
// peer_messaging config key, in both directions: the flag is what the user
// typed for this run, and its absence leaves the configured value alone.
func TestResolvePeer(t *testing.T) {
	tests := []struct {
		name       string
		configured bool
		flag       string // "" = not passed
		want       bool
	}{
		{name: "off_everywhere"},
		{name: "config_only", configured: true, want: true},
		{name: "flag_only", flag: "true", want: true},
		{name: "flag_turns_config_off", configured: true, flag: "false"},
		{name: "flag_agrees_with_config", configured: true, flag: "true", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var flagVal bool
			c := &cobra.Command{Use: "shell"}
			c.Flags().BoolVar(&flagVal, "peer", false, "")
			if tc.flag != "" {
				if err := c.Flags().Set("peer", tc.flag); err != nil {
					t.Fatalf("set flag: %v", err)
				}
			}
			if got := resolvePeer(c, tc.configured, flagVal); got != tc.want {
				t.Errorf("resolvePeer = %v, want %v", got, tc.want)
			}
		})
	}
}
