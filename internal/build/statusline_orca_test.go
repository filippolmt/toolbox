package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A payload shaped like Claude Code's, trailing newline included: the reporter
// must get these bytes, not a re-serialisation or a trimmed copy.
const orcaStatuslinePayload = `{"session_id":"s1","model":{"display_name":"Opus"},"rate_limits":{"five_hour":{"used_percentage":12,"resets_at":0}}}` + "\n"

// runStatusline executes the embedded statusline-command.sh with payload on
// stdin under a temporary HOME. installReporter installs a fake Orca statusline
// reporter that records what it receives in home/received.
func runStatusline(t *testing.T, paneKey string, installReporter bool) (rendered string, home string) {
	t.Helper()
	requireTools(t, "jq")
	home = t.TempDir()
	dir := t.TempDir()
	script := filepath.Join(dir, "statusline-command.sh")
	body := wireAgentTopologyForTest(t, dir, []byte(readAsset(t, "statusline-command.sh")))
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if installReporter {
		hooks := filepath.Join(home, ".orca", "agent-hooks")
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
		// tee: a reporter that prints must still leave the rendered line alone.
		// Write then rename, so a poller never reads a half-written record.
		fake := "#!/bin/sh\ntee \"$HOME/received.tmp\"\nmv \"$HOME/received.tmp\" \"$HOME/received\"\n"
		if err := os.WriteFile(filepath.Join(hooks, "claude-statusline.sh"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("/bin/bash", script)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	if paneKey != "" {
		cmd.Env = append(cmd.Env, "ORCA_PANE_KEY="+paneKey)
	}
	cmd.Stdin = strings.NewReader(orcaStatuslinePayload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("statusline: %v\n%s", err, out)
	}
	return string(out), home
}

// waitForFile polls for path, since the reporter runs in the background.
func waitForFile(path string, d time.Duration) ([]byte, bool) {
	deadline := time.Now().Add(d)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			return b, true
		}
		if time.Now().After(deadline) {
			return nil, false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStatuslineForwardsTheExactPayloadToOrcasReporter(t *testing.T) {
	_, home := runStatusline(t, "tab-1:pane-1", true)
	got, ok := waitForFile(filepath.Join(home, "received"), 5*time.Second)
	if !ok {
		t.Fatal("Orca's reporter was never invoked")
	}
	if string(got) != orcaStatuslinePayload {
		t.Fatalf("reporter received %q, want %q", got, orcaStatuslinePayload)
	}
}

func TestStatuslineRendersTheSameLineWhileForwardingToOrca(t *testing.T) {
	plain, _ := runStatusline(t, "", false)
	forwarded, _ := runStatusline(t, "tab-1:pane-1", true)
	if plain == "" {
		t.Fatal("statusline rendered nothing; the comparison would prove nothing")
	}
	if forwarded != plain {
		t.Fatalf("forwarding changed the rendered line:\n got %q\nwant %q", forwarded, plain)
	}
}
