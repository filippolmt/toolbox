package orca

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{"1.4.262": "v1.4.262", "v1.4.262": "v1.4.262", "2": "v2"} {
		if got, err := NormalizeVersion(in); err != nil || got != want {
			t.Errorf("NormalizeVersion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "v", "latest", "1.4.262-beta", "1..2", "1.2/../x", " 1.2", "V1.2", "vv1.2"} {
		if got, err := NormalizeVersion(bad); err == nil {
			t.Errorf("NormalizeVersion(%q) = %q, want an error", bad, got)
		}
	}
}

func writeLog(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "serve.log")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const stale = `{"type":"orca_server_ready","schemaVersion":1,"pairing":{"available":true,"url":"orca://pair#stale"}}` + "\n"

func TestWaitPairingReadsTheRecordAfterTheOffset(t *testing.T) {
	log := writeLog(t, stale+"toolbox: orca v1: start\nnoise {\"not\":\"json\"\n"+
		`{"type":"orca_server_ready","schemaVersion":1,"pairing":{"available":true,"url":"orca://pair#fresh"}}`+"\n")
	got, err := WaitPairing(log, int64(len(stale)), time.Second)
	if err != nil || got != "orca://pair#fresh" {
		t.Errorf("WaitPairing = %q, %v; want the fresh link", got, err)
	}
}

func TestWaitPairingReportsUnavailablePairingWithOrcasGuidance(t *testing.T) {
	log := writeLog(t, `{"type":"orca_server_ready","pairing":{"available":false,"reason":"disabled_by_operator","guidance":"Restart without --no-pairing."}}`+"\n")
	_, err := WaitPairing(log, 0, time.Second)
	if err == nil || !strings.Contains(err.Error(), "Restart without --no-pairing.") {
		t.Errorf("WaitPairing = %v, want Orca's guidance", err)
	}
}

func TestWaitPairingTimeoutNamesTheLog(t *testing.T) {
	log := writeLog(t, stale)
	_, err := WaitPairing(log, int64(len(stale)), 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), log) {
		t.Errorf("WaitPairing = %v, want a timeout naming %s", err, log)
	}
	_, err = WaitPairing(filepath.Join(t.TempDir(), "missing.log"), 0, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "missing.log") {
		t.Errorf("WaitPairing on a missing log = %v, want a timeout naming it", err)
	}
}

func TestWaitPairingSeesARecordWrittenWhileWaiting(t *testing.T) {
	log := writeLog(t, "")
	go func() {
		time.Sleep(200 * time.Millisecond)
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
		_, _ = f.WriteString(`{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#late"}}` + "\n")
		_ = f.Close()
	}()
	got, err := WaitPairing(log, 0, 3*time.Second)
	if err != nil || got != "orca://pair#late" {
		t.Errorf("WaitPairing = %q, %v; want the late link", got, err)
	}
}

// A fatal line from the installer ends the wait at once with ErrNotStarted
// and the installer's reason; only the attempt's own lines count.
func TestWaitPairingStopsAtTheInstallersFatalLine(t *testing.T) {
	log := writeLog(t, stale+"toolbox: orca v1.4.262: start 2026-11-18T00:00:00Z\n"+
		"toolbox: orca: fatal: could not download Orca server v1.4.262 and no server is cached\n")
	start := time.Now()
	_, err := WaitPairing(log, int64(len(stale)), 5*time.Second)
	if !errors.Is(err, ErrNotStarted) || !strings.Contains(err.Error(), "no server is cached") {
		t.Errorf("WaitPairing = %v, want ErrNotStarted with the reason", err)
	}
	if time.Since(start) > time.Second {
		t.Error("WaitPairing waited for the timeout after a fatal line")
	}
}

// A later start supersedes an earlier fatal line: joining an Orca Shell reads
// the whole log.
func TestWaitPairingIgnoresAFatalLineOfAnEarlierStart(t *testing.T) {
	log := writeLog(t, "toolbox: orca v1: start x\ntoolbox: orca: fatal: offline\n"+
		"toolbox: orca v1: start y\n"+
		`{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#fresh"}}`+"\n")
	if got, err := WaitPairing(log, 0, time.Second); err != nil || got != "orca://pair#fresh" {
		t.Errorf("WaitPairing = %q, %v; want the fresh link", got, err)
	}
}

// Warnings are the latest start's `warning:` lines past the offset, without
// the prefix.
func TestWarningsReturnsTheLatestStartsWarnings(t *testing.T) {
	log := writeLog(t, "toolbox: orca v1: start x\ntoolbox: orca: warning: old\n"+
		"toolbox: orca v2: start y\ntoolbox: orca: warning: starting the cached v1 instead of v2\n"+stale)
	got, err := Warnings(log, 0)
	if err != nil || len(got) != 1 || got[0] != "starting the cached v1 instead of v2" {
		t.Errorf("Warnings = %q, %v; want the latest start's one warning", got, err)
	}
}

// A join reads the whole log: only what the latest server start wrote counts,
// and a server that has since exited is reported as stopped, not as serving
// the link it printed before it died.
func TestWaitPairingReportsTheLatestStartsState(t *testing.T) {
	ready := `{"type":"orca_server_ready","pairing":{"available":true,"url":"orca://pair#now"}}` + "\n"
	for _, tc := range []struct {
		label, log, want, wantErr string
	}{
		{"running", stale + "toolbox: orca v1: start x\n" + ready, "orca://pair#now", ""},
		{"crashed", "toolbox: orca v1: start x\n" + ready + ExitedMarker + " with status 1\n", "", "status 1"},
		{"install failed", stale + "toolbox: orca v1: start x\n" + ExitedMarker + " with status 1\n", "", "stopped"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			got, err := WaitPairing(writeLog(t, tc.log), 0, 300*time.Millisecond)
			if tc.wantErr == "" && (err != nil || got != tc.want) {
				t.Errorf("WaitPairing = %q, %v; want %q", got, err, tc.want)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("WaitPairing = %q, %v; want an error with %q", got, err, tc.wantErr)
			}
		})
	}
}

// The init script logs a fatal line and then, on exit, the stop line: the
// fatal reason wins, so the host still stops the Orca Shell (ErrNotStarted).
func TestWaitPairingKeepsTheFatalReasonPastTheStopLine(t *testing.T) {
	log := writeLog(t, "toolbox: orca v1: start x\ntoolbox: orca: fatal: checksum mismatch\n"+
		ExitedMarker+" with status 1\n")
	if _, err := WaitPairing(log, 0, time.Second); !errors.Is(err, ErrNotStarted) || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("WaitPairing = %v, want ErrNotStarted with the reason", err)
	}
}
