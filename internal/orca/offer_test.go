package orca

import (
	"context"
	"encoding/binary"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// shellExec is an Orca Shell whose exec prints stdout and stderr the way the
// daemon frames them, and records the command it was asked to run.
type fakeShell struct {
	stdout, stderr string
	container      string
	cmd            []string
}

func (s *fakeShell) ExecCreate(_ context.Context, container string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
	s.container, s.cmd = container, o.Cmd
	return client.ExecCreateResult{ID: "e1"}, nil
}

func (s *fakeShell) ExecAttach(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
	daemon, cli := net.Pipe()
	go func() {
		_, _ = daemon.Write(frame(stdcopy.Stdout, s.stdout))
		_, _ = daemon.Write(frame(stdcopy.Stderr, s.stderr))
		_ = daemon.Close()
	}()
	return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(cli, "")}, nil
}

// frame is one multiplexed chunk: the stream byte, three zero bytes, the
// big-endian length, then the payload.
func frame(stream stdcopy.StdType, payload string) []byte {
	h := []byte{byte(stream), 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func TestOfferAsksTheOrcaShellsServerForALoopbackLink(t *testing.T) {
	s := &fakeShell{stdout: `{"id":"1","ok":true,"result":{"available":true,"scope":"runtime","pairingUrl":"orca://pair?code=z","endpoint":"ws://127.0.0.1:6768"}}` + "\n"}
	got, err := Offer(t.Context(), s, "toolbox-orca-work")
	if err != nil || got != "orca://pair?code=z" {
		t.Fatalf("Offer = %q, %v; want the offer's link", got, err)
	}
	if s.container != "toolbox-orca-work" || !strings.Contains(strings.Join(s.cmd, " "), "pair --address 127.0.0.1 --json") ||
		!slices.ContainsFunc(s.cmd, func(a string) bool { return strings.Contains(a, "$ORCA_USER_DATA_PATH/server/current/bin/orca-ide") }) {
		t.Errorf("exec in %q ran %q, want the installed orca-ide pair on loopback", s.container, s.cmd)
	}
}

func TestOfferReportsUnavailablePairingWithOrcasGuidance(t *testing.T) {
	s := &fakeShell{stdout: `{"ok":true,"result":{"available":false,"reason":"no_server","guidance":"Start Orca first."}}`}
	if _, err := Offer(t.Context(), s, "toolbox-orca"); err == nil || !strings.Contains(err.Error(), "Start Orca first.") {
		t.Errorf("Offer = %v, want Orca's guidance", err)
	}
}

func TestOfferWithoutJSONShowsWhatTheCommandPrinted(t *testing.T) {
	s := &fakeShell{stderr: "sh: orca-ide: not found\n"}
	if _, err := Offer(t.Context(), s, "toolbox-orca"); err == nil || !strings.Contains(err.Error(), "orca-ide: not found") {
		t.Errorf("Offer = %v, want the command's stderr", err)
	}
}
