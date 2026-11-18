// Package orca holds the host half of the Orca Shell that is not session
// planning: the trust-boundary check on the requested server version, and
// reading the pairing link out of the server log. → CONTEXT.md, Orca Shell.
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
	"time"
)

var versionRe = regexp.MustCompile(`^v?([0-9]+(\.[0-9]+)*)$`)

// startRe matches the line the init script writes as a server start begins.
var startRe = regexp.MustCompile(`^toolbox: orca \S+: start`)

// ExitedMarker begins the line the init script appends when its run ends
// without a server — an install that failed or a server that exited —
// followed by " with status <n>".
const ExitedMarker = "toolbox: orca: server stopped"

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

// WaitPairing waits up to timeout for a readiness record in the log at path,
// past byte offset, and returns its pairing link. The last record wins: the
// log is appended across server starts. A record whose pairing is unavailable
// is an error carrying Orca's own guidance. Only the latest start counts, and
// one that has stopped is an error, not the link it printed before.
func WaitPairing(path string, offset int64, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		rec, stopped, err := lastRecord(path, offset)
		if err != nil {
			return "", err
		}
		if stopped != "" {
			return "", fmt.Errorf("orca: the server stopped (%q); read %s, then toolbox orca stop and start it again", stopped, path)
		}
		if rec != nil {
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

// lastRecord reads the log past offset as of the latest server start: its
// readiness record, or the stop line written after it. Both empty when there
// is neither yet (a missing log included: the server may not have started).
func lastRecord(path string, offset int64) (last *readiness, stopped string, err error) {
	f, err := os.Open(path) //nolint:gosec // the log path comes from the session plan
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, "", err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		switch {
		case startRe.Match(line):
			last, stopped = nil, ""
		case bytes.HasPrefix(line, []byte(ExitedMarker)):
			last, stopped = nil, string(line)
		case bytes.HasPrefix(line, []byte("{")):
			var r readiness
			if json.Unmarshal(line, &r) == nil && r.Type == "orca_server_ready" {
				last, stopped = &r, ""
			}
		}
	}
	return last, stopped, sc.Err()
}
