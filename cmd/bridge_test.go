package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestBridge_RejectsExtraArgs(t *testing.T) {
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	rootCmd.SetArgs([]string{"bridge", "install", "extra"})
	var buf bytes.Buffer
	rootCmd.SetErr(&buf)
	rootCmd.SetOut(&buf)
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "accepts 0 arg") &&
		!strings.Contains(err.Error(), "unknown command") {
		t.Errorf("err = %v", err)
	}
}

// The deprecated `browser-bridge` spelling must keep resolving to the bridge
// command: installed LaunchAgents/systemd units invoke `browser-bridge
// daemon` until the user reruns `toolbox bridge install`.
func TestBridge_LegacyAliasResolves(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"browser-bridge", "daemon"})
	if err != nil {
		t.Fatalf("Find(browser-bridge daemon) err = %v", err)
	}
	if cmd.Name() != "daemon" {
		t.Errorf("resolved %q, want daemon subcommand", cmd.Name())
	}
}

// stdout must distinguish the two uninstall outcomes: the warning itself goes
// to stderr, which a `toolbox bridge uninstall > log` never captures.
func TestUninstallSummary(t *testing.T) {
	if got := uninstallSummary(""); got != "bridge: uninstalled" {
		t.Errorf("uninstallSummary(\"\") = %q", got)
	}
	got := uninstallSummary("bridge state dir /h not removed: permission denied")
	if got != "bridge: daemon removed; state dir left behind" {
		t.Errorf("uninstallSummary(warning) = %q", got)
	}
}
