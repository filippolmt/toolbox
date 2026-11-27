package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// InstallTip is the line session start prints while the bridge is not
// installed.
const InstallTip = "toolbox: tip — run 'toolbox bridge install' to forward in-container URLs, editor opens and agent sounds to the host"

// refreshWait bounds how long session start waits for a restarted daemon to
// answer /healthz with the CLI's build identity; a var so tests can shorten it.
var refreshWait = 5 * time.Second

// healthTimeout bounds one /healthz probe from the host.
const healthTimeout = time.Second

// CheckAtSessionStart is the bridge's session-start step. Not installed: the
// install tip, nothing else. Installed: compare the daemon's /healthz build identity
// with cliBuild; on a mismatch, restart the daemon through its supervisor
// when the unit runs self (the same file once symlinks are resolved), else
// leave the unit alone and say which binary it runs. Every failure is a line
// on w: the session always starts.
func CheckAtSessionStart(host fsx.Host, a Agent, self, cliBuild string, w io.Writer) {
	if !a.IsInstalled() {
		_, _ = fmt.Fprintln(w, InstallTip)
		return
	}
	warn := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, "toolbox: warning: bridge daemon: "+format+"\n", args...)
	}
	s, err := ResolveHostState(host)
	if err != nil {
		warn("%v", err)
		return
	}
	unitExe, err := a.ExecPath()
	if err != nil {
		warn("read service file: %v", err)
		return
	}
	running, err := daemonVersion(s)
	if err != nil {
		warn("not reachable: %v", err)
		return
	}
	if running == cliBuild {
		return
	}
	if !sameFile(unitExe, self) {
		_, _ = fmt.Fprintf(w, "toolbox: bridge daemon runs %s from %s, this CLI is %s (%s); run 'toolbox bridge install' to switch the bridge to this binary\n",
			shownVersion(running), unitExe, cliBuild, self)
		return
	}
	if err := a.Restart(); err != nil {
		warn("runs %s, CLI is %s; restart failed: %v", shownVersion(running), cliBuild, err)
		return
	}
	if err := waitForVersion(s, cliBuild); err != nil {
		warn("restarted, but %v", err)
		return
	}
	_, _ = fmt.Fprintf(w, "toolbox: bridge daemon refreshed from %s to %s\n", shownVersion(running), cliBuild)
}

// shownVersion names a daemon whose /healthz carries no version.
func shownVersion(v string) string {
	if v == "" {
		return "a build without a version"
	}
	return v
}

// sameFile reports whether a and b are one file; os.Stat follows symlinks,
// so the Homebrew symlink and the Caskroom binary it points to match.
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// daemonVersion asks the daemon's loopback /healthz for its version; "" is a
// daemon that predates the field.
func daemonVersion(s HostState) (string, error) {
	port, err := LoadPort(s)
	if err != nil {
		return "", err
	}
	client := http.Client{Timeout: healthTimeout}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, RouteHealth))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", RouteHealth, resp.Status)
	}
	var body map[string]string
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err != nil {
		return "", err
	}
	return body[FieldVersion], nil
}

// waitForVersion polls /healthz until it reports want or refreshWait ends.
// The port is reread each time: the restarted daemon rewrites it.
func waitForVersion(s HostState, want string) error {
	deadline := time.Now().Add(refreshWait)
	got, err := "", error(nil)
	for {
		if got, err = daemonVersion(s); err == nil && got == want {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("not reachable after %s: %w", refreshWait, err)
			}
			return fmt.Errorf("still reports %s after %s", shownVersion(got), refreshWait)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
