//go:build peergate

// Package-internal integration gate for cross-container peer messaging. It
// needs a real Docker daemon and the runtime image present locally under its
// canonical ref, so it sits behind the `peergate` build tag and never runs in
// `make go-check`. CI runs it inside the docker-build job, where the image it
// just built is already loaded.
//
// It asserts the MECHANISM, not the feature: the two conditions toolbox
// builds — the peer's socket directory is shared, and the peer's pid resolves.
// It deliberately does not drive Claude Code or parse /list-agents output,
// which upstream may reformat at will. See
// docs/adr/0003-cross-container-peer-messaging.md.
package container

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mountplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
	"github.com/filippolmt/toolbox/internal/teardown"
)

// dockerExec runs cmd in the container and returns its combined output and
// exit code. Non-TTY so the two streams stay parseable.
func dockerExec(ctx context.Context, t *testing.T, cli client.APIClient, id string, cmd ...string) (string, int) {
	t.Helper()
	created, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	})
	if err != nil {
		t.Fatalf("ExecCreate %v: %v", cmd, err)
	}
	resp, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatalf("ExecAttach %v: %v", cmd, err)
	}
	defer resp.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Reader); err != nil {
		t.Fatalf("read exec output: %v", err)
	}
	inspect, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		t.Fatalf("ExecInspect: %v", err)
	}
	// Strip the 8-byte docker multiplexing frame headers.
	var out strings.Builder
	for b := buf.Bytes(); len(b) >= 8; {
		n := int(b[4])<<24 | int(b[5])<<16 | int(b[6])<<8 | int(b[7])
		if n > len(b)-8 {
			n = len(b) - 8
		}
		out.Write(b[8 : 8+n])
		b = b[8+n:]
	}
	return strings.TrimSpace(out.String()), inspect.ExitCode
}

// startPeerSession creates and starts one opted-in session container for a
// fresh workspace, registering its teardown.
//
// The image under test comes from IMAGE_TAG through the `image:` config key —
// the documented full-ref override, which wins over the canonical GHCR tag —
// so CI runs the gate against the image it just built without a `docker tag`
// aliasing step, and without a second copy of the canonical ref outside
// internal/build.DefaultRegistryImage. Empty falls back to the canonical ref
// for a local run.
func startPeerSession(ctx context.Context, t *testing.T, cli client.APIClient) string {
	t.Helper()
	plan, err := sessionplan.Plan(sessionplan.PlanInput{
		Cfg:       &config.Config{Shell: "zsh", Image: os.Getenv("IMAGE_TAG")},
		Workspace: t.TempDir(),
		Peer:      true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	id, err := createAndStart(ctx, cli, plan)
	if err != nil {
		t.Fatalf("createAndStart %s: %v", plan.ContainerName, err)
	}
	t.Cleanup(func() {
		_ = teardown.StopOne(context.Background(), cli, plan.ContainerName, teardown.DefaultStopGrace)
	})
	return id
}

// peerProbeSocketPath is where the probe binds, inside the shared socket dir.
const peerProbeSocketPath = mountplan.PeerSocketDirTarget + "/probe.sock"

// peerSocketProbe replicates Claude Code's uds-messaging startup: bind a UNIX
// socket in the shared directory, then chmod it to 0600. It leaves the socket
// in place so the peer container can assert on it, and exits non-zero on
// either step — the chmod is the one that fails on a virtiofs bind mount.
const peerSocketProbe = `
import os, socket
p = "` + peerProbeSocketPath + `"
s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.bind(p)
os.chmod(p, 0o600)
`

func TestPeerMessagingMechanism(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cli.Close()

	// A throwaway HOME keeps the run off the developer's own ~/.toolbox. The
	// socket directory is shared through the toolbox-cc-socks volume rather
	// than through HOME, so both sessions land in it regardless.
	t.Setenv("HOME", t.TempDir())

	// The anchor outlives sessions by design, so it is torn down explicitly.
	t.Cleanup(func() {
		_ = teardown.StopOne(context.Background(), cli, sessionplan.PeerAnchorContainerName, teardown.DefaultStopGrace)
	})

	a := startPeerSession(ctx, t, cli)
	b := startPeerSession(ctx, t, cli)

	// Condition 1 — the inbox-socket directory carries a real peer socket, and
	// what A drops in it is what B reads. Without the shared mount /tmp is
	// per-container and every peer socket is unreachable.
	//
	// The probe binds a UNIX socket and chmods it, which is the sequence Claude
	// Code's uds-messaging listener actually runs — not `touch`. The two differ
	// on a bind-mounted host directory: Docker Desktop on macOS serves those
	// over virtiofs, where chmod(2) on a socket inode fails with EINVAL while
	// touch and chmod on a regular file both succeed. A `touch` probe stays
	// green while peer messaging is dead, which is how #796 shipped broken.
	if out, code := dockerExec(ctx, t, cli, a, "python3", "-c", peerSocketProbe); code != 0 {
		t.Fatalf("could not bind and chmod a peer socket in container A (exit %d): %s", code, out)
	}
	if _, code := dockerExec(ctx, t, cli, b, "test", "-S", peerProbeSocketPath); code != 0 {
		t.Errorf("socket bound by A is not visible in B: %s is not shared", mountplan.PeerSocketDirTarget)
	}

	// The directory must be 0700 and owned by the session user: Claude Code
	// falls back to /tmp/cc-socks-<uid> — silently — on anything looser, which
	// would leave every peer alone in its own directory with no error.
	if perm, _ := dockerExec(ctx, t, cli, b, "stat", "-c", "%a %U", "/tmp/cc-socks"); !strings.HasPrefix(perm, "700 ") {
		t.Errorf("/tmp/cc-socks is %q, want mode 700 owned by the session user", perm)
	}

	// Condition 2 — the peer's pid resolves: the registry is pid-keyed and its
	// liveness check runs in the reading session's PID namespace.
	pid, code := dockerExec(ctx, t, cli, a, "sh", "-c", "sleep 300 >/dev/null 2>&1 & echo $!")
	if code != 0 || pid == "" {
		t.Fatalf("could not start a probe process in container A (exit %d, pid %q)", code, pid)
	}
	if _, code := dockerExec(ctx, t, cli, b, "kill", "-0", pid); code != 0 {
		t.Errorf("pid %s from container A does not resolve in B: the PID namespace is not shared", pid)
	}
}
