package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/filippolmt/toolbox/internal/fsx"
)

// Host Terminal Relay: /hc runs the host's herdr-core CLI (hc) for an agent in
// the Herdr Core Shell, so it can reach the agents herdr.app runs in the host's
// own terminals. The other direction needs nothing from toolbox: the host's hc
// already reaches the Shell as a machine. → CONTEXT.md, Host Terminal Relay;
// docs/adr/0025-host-terminal-relay.md.

// hcTerminalRe is a terminal the relay may name: an address (t7, t1b) or a
// terminal name, both within hc's own 1-32 [a-z0-9_-]. No `/`, so no
// `machine/t7`: the relay reaches the host's own terminals, never the machines
// the host's hc reaches in turn.
var hcTerminalRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

var hcLinesRe = regexp.MustCompile(`^[0-9]{1,5}$`)

// errHCArgs is every argv the gate refuses; its text is the shim's usage.
var errHCArgs = errors.New("not bridged — only `ls [--all]`, `get <terminal>`, `read <terminal> [--lines N]` and `send <terminal> <text>` reach the host's hc, on the host's own terminals")

// hcAllowlist is the verbs the relay runs, each with the builder that checks
// the container's args and returns the argv the host's hc runs, or nil to
// refuse. Positionals go after `--`, so a terminal or a text that starts with
// `-` stays a positional. Every other hc verb (run, exec, keys, file, put,
// machine, attach, wait, …) would hand the container a shell on the host, or
// hold a request open for minutes.
var hcAllowlist = map[string]func(args []string) []string{
	"ls": func(args []string) []string {
		if slices.ContainsFunc(args, func(a string) bool { return a != "--all" }) {
			return nil
		}
		return append([]string{"ls", "--json"}, args...)
	},
	"get": func(args []string) []string {
		if len(args) != 1 || !hcTerminal(args[0]) {
			return nil
		}
		return []string{"get", "--json", "--", args[0]}
	},
	"read": func(args []string) []string {
		switch {
		case len(args) == 1 && hcTerminal(args[0]):
			return []string{"read", "--", args[0]}
		case len(args) == 3 && hcTerminal(args[0]) && args[1] == "--lines" && hcLinesRe.MatchString(args[2]):
			return []string{"read", "--lines", args[2], "--", args[0]}
		}
		return nil
	},
	"send": func(args []string) []string {
		if len(args) != 2 || !hcTerminal(args[0]) || args[1] == "" {
			return nil
		}
		return []string{"send", "--", args[0], args[1]}
	},
}

func hcTerminal(t string) bool { return hcTerminalRe.MatchString(t) }

// hcArgv checks the container's argv for command and rebuilds the argv the
// host's hc runs.
func hcArgv(command string, args []string) ([]string, error) {
	build, ok := hcAllowlist[command]
	if !ok {
		return nil, errHCArgs
	}
	if argv := build(args); argv != nil {
		return argv, nil
	}
	return nil, errHCArgs
}

// hcReady reports whether `hc get --json` describes an agent waiting for
// input, and whether it is idle (as opposed to done). A shell has no status;
// a working or blocked agent is not waiting.
func hcReady(getJSON []byte) (ready, idle bool) {
	var t struct {
		Status *struct {
			App   string `json:"app"`
			State string `json:"state"`
		} `json:"status"`
	}
	if json.Unmarshal(getJSON, &t) != nil || t.Status == nil || t.Status.App == "" {
		return false, false
	}
	switch t.Status.State {
	case "idle":
		return true, true
	case "done":
		return true, false
	}
	return false, false
}

// hcTimeout bounds one relay request. A send waits for a person's unfinished
// draft in the target terminal (hc's draft_wait, 60s by default) before typing.
const hcTimeout = 90 * time.Second

// hcBodyLimit caps a /hc body: a send carries a whole prompt.
const hcBodyLimit = 64 << 10

type hcRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func (h *handler) handleHC(w http.ResponseWriter, r *http.Request) {
	if !h.gate("hc", w, r) {
		return
	}
	var req hcRequest
	if !h.decodeJSON(w, r, hcBodyLimit, &req) {
		return
	}
	argv, err := hcArgv(req.Command, req.Args)
	if err != nil {
		h.logger.Printf("hc: rejected (argv) command=%q", truncate(req.Command, 64))
		http.Error(w, "hc "+truncate(req.Command, 64)+": "+err.Error(), http.StatusBadRequest)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(hcTimeout + 10*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), hcTimeout)
	defer cancel()
	if req.Command == "send" {
		target := req.Args[0]
		out, _, err := h.fns.hc(ctx, []string{"get", "--json", "--", target})
		if err != nil {
			h.logger.Printf("hc: handler failed: %v command=get", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		ready, idle := hcReady(out)
		if !ready {
			h.logger.Printf("hc: rejected (not a ready agent) terminal=%q", target)
			http.Error(w, "hc send: "+target+" is not an agent waiting for input (a shell, a working or blocked agent, or no such terminal); nothing was typed", http.StatusConflict)
			return
		}
		if idle {
			// hc's own check, at the keystrokes: --if takes idle, not done.
			argv = append([]string{"send", "--if", "idle"}, argv[1:]...)
		}
	}
	out, exit, err := h.fns.hc(ctx, argv)
	if err != nil {
		h.logger.Printf("hc: handler failed: %v command=%q", err, req.Command)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	h.logger.Printf("hc: ok command=%q exit=%d", req.Command, exit)
	writeJSONOK(w, commandResponse{Exit: exit, Output: string(out)})
}

// ErrHCNotInstalled: neither the daemon's PATH nor ~/.local/bin holds hc.
var ErrHCNotInstalled = errors.New("hc (herdr-core) not found on the host — install herdr-core there (herdr.app ships it)")

// launchHC runs the host's hc with argv and returns its combined output and
// exit code; a non-zero exit is hc's answer, not an error. The bridge's
// service PATH lacks ~/.local/bin, where herdr-core installs hc, and hc finds
// the user's server from HOME, so both are set from host.
func launchHC(ctx context.Context, host fsx.Host, argv []string) ([]byte, int, error) {
	var candidates []string
	if host.Home != "" {
		candidates = []string{host.Join(".local", "bin", "hc")}
	}
	bin, err := resolveHostBinary(host, "hc", candidates, ErrHCNotInstalled)
	if err != nil {
		return nil, 0, err
	}
	cmd := exec.CommandContext(ctx, bin, argv...) //nolint:gosec // bin is resolved locally; hcArgv rebuilds argv from an allowlist, positionals after --
	cmd.Env = os.Environ()
	if host.Home != "" {
		cmd.Env = setEnv(cmd.Env, "HOME", host.Home)
	}
	return runHostCommand(ctx, cmd, "hc "+argv[0])
}
