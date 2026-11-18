package container

import (
	"context"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// Publish the namespace-local PID and birth time atomically before exec.
// An empty marker left by cleanup fences a delayed startup instead of letting
// it create a new orphan after the client has already returned.
const shellProcessWrapper = `import os, sys
path = sys.argv[1]
pending = path + '.pending'
fd = os.open(pending, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
try:
    with os.fdopen(fd, 'w') as marker:
        with open('/proc/self/stat') as stat:
            birth = stat.read().rsplit(')', 1)[1].split()[19]
        marker.write(f'{os.getpid()} {birth}\n')
    try:
        os.link(pending, path)
    except FileExistsError:
        sys.exit(0)
finally:
    os.unlink(pending)
os.execvp(sys.argv[2], sys.argv[2:])
`

// pidfd pins the identified process across HUP and KILL. Comparing birth time
// after opening it prevents a recycled PID from targeting an unrelated shell.
const shellProcessCleanup = `import os, select, signal, sys
path = sys.argv[1]
def stop():
    while True:
        try:
            with open(path) as marker:
                identity = marker.read()
            break
        except FileNotFoundError:
            try:
                fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                os.close(fd)
                return
            except FileExistsError:
                continue
    if not identity:
        return
    try:
        pid, birth = map(int, identity.split())
        if pid <= 1:
            raise ValueError('invalid shell PID')
        try:
            fd = os.pidfd_open(pid)
        except ProcessLookupError:
            return
        try:
            try:
                with open(f'/proc/{pid}/stat') as stat:
                    actual = int(stat.read().rsplit(')', 1)[1].split()[19])
                if actual != birth:
                    return
                signal.pidfd_send_signal(fd, signal.SIGHUP)
                if not select.select([fd], [], [], 0.25)[0]:
                    signal.pidfd_send_signal(fd, signal.SIGKILL)
                    if not select.select([fd], [], [], 1)[0]:
                        raise TimeoutError('disconnected shell did not stop')
            except ProcessLookupError:
                return
            except FileNotFoundError:
                return
        finally:
            os.close(fd)
    finally:
        os.unlink(path)
stop()
`

// cleanupShell runs even after a failed attach: its marker either identifies
// this shell or prevents it from starting later. The helper is itself an exec,
// so both must finish before teardown decides whether a sibling is attached.
func cleanupShell(cli client.APIClient, containerID, execID, marker string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	helper, err := cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd: []string{"/usr/bin/python3", "-c", shellProcessCleanup, marker},
	})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create shell cleanup: %w", err)
	}
	if _, err := cli.ExecStart(ctx, helper.ID, client.ExecStartOptions{Detach: true}); err != nil {
		return fmt.Errorf("start shell cleanup: %w", err)
	}
	result, err := waitForExec(ctx, cli, helper.ID)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("shell cleanup exited with code %d", result.ExitCode)
	}
	_, err = waitForExec(ctx, cli, execID)
	return err
}

func waitForExec(ctx context.Context, cli client.APIClient, id string) (client.ExecInspectResult, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := cli.ExecInspect(ctx, id, client.ExecInspectOptions{})
		if cerrdefs.IsNotFound(err) {
			return client.ExecInspectResult{}, nil
		}
		if err != nil {
			return result, fmt.Errorf("inspect shell cleanup exec: %w", err)
		}
		if !result.Running {
			return result, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return result, fmt.Errorf("wait for shell cleanup exec: %w", ctx.Err())
		}
	}
}
