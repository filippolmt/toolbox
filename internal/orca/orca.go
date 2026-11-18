// Package orca holds the host half of the Orca Shell that is not session
// planning: which server version to run (the --version flag or the desktop
// app's, behind the Desktop seam) and its trust-boundary check, and reading
// the pairing link out of the server log. → CONTEXT.md, Orca Shell.
package orca

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var versionRe = regexp.MustCompile(`^v?([0-9]+(\.[0-9]+)*)$`)

// NormalizeVersion accepts a release version (digits and dots, optional
// leading v) and returns the v-prefixed release tag. The value reaches a
// download URL and a container env, so anything else is refused.
func NormalizeVersion(s string) (string, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return "", fmt.Errorf("--version %q is not a release version (digits and dots, e.g. 1.4.262)", s)
	}
	return "v" + m[1], nil
}

// readiness is the part of Orca's `serve --json` readiness record read here.
type readiness struct {
	Type    string `json:"type"`
	Pairing struct {
		Available bool   `json:"available"`
		URL       string `json:"url"`
		Reason    string `json:"reason"`
		Guidance  string `json:"guidance"`
	} `json:"pairing"`
}

// pollInterval is how often WaitPairing re-reads the log.
const pollInterval = 200 * time.Millisecond

// ErrNotStarted marks an Orca Shell whose installer gave up without starting
// a server (offline with nothing cached, a checksum mismatch): nothing will
// ever serve in it.
var ErrNotStarted = errors.New("orca: no server started")

// Lines the init script (init.d/80-orca.sh) writes to the log: one opens each
// start attempt, the others carry its outcome for the host.
var startRe = regexp.MustCompile(`^toolbox: orca \S+: start `)

const (
	fatalPrefix   = "toolbox: orca: fatal: "
	warningPrefix = "toolbox: orca: warning: "
)

// WaitPairing waits up to timeout for a readiness record in the log at path,
// past byte offset, and returns its pairing link. The latest start attempt
// wins: the log is appended across server starts. A record whose pairing is
// unavailable is an error carrying Orca's own guidance; an installer's fatal
// line is ErrNotStarted with its reason.
func WaitPairing(path string, offset int64, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, err := readLog(path, offset)
		if err != nil {
			return "", err
		}
		if st.fatal != "" {
			return "", fmt.Errorf("%w: %s; read %s", ErrNotStarted, st.fatal, path)
		}
		if rec := st.record; rec != nil {
			if !rec.Pairing.Available {
				return "", fmt.Errorf("orca: the server is up but pairing is unavailable (%s): %s", rec.Pairing.Reason, rec.Pairing.Guidance)
			}
			return rec.Pairing.URL, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("orca: the server did not report ready within %s; read %s", timeout, path)
		}
		time.Sleep(pollInterval)
	}
}

// Warnings returns the latest start attempt's warning lines past offset,
// such as the offline fallback to a cached version other than the requested.
func Warnings(path string, offset int64) ([]string, error) {
	st, err := readLog(path, offset)
	return st.warnings, err
}

// logState is the latest start attempt as the log past an offset tells it.
type logState struct {
	record   *readiness
	fatal    string
	warnings []string
}

// readLog scans the log past offset; a missing log is an empty state (the
// server may not have started).
func readLog(path string, offset int64) (logState, error) {
	var st logState
	f, err := os.Open(path) //nolint:gosec // the log path comes from the session plan
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return st, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := string(bytes.TrimSpace(sc.Bytes()))
		switch {
		case startRe.MatchString(line):
			st = logState{}
		case strings.HasPrefix(line, fatalPrefix):
			st.fatal = strings.TrimPrefix(line, fatalPrefix)
		case strings.HasPrefix(line, warningPrefix):
			st.warnings = append(st.warnings, strings.TrimPrefix(line, warningPrefix))
		case strings.HasPrefix(line, "{"):
			var r readiness
			if json.Unmarshal([]byte(line), &r) == nil && r.Type == "orca_server_ready" {
				st.record = &r
			}
		}
	}
	return st, sc.Err()
}
