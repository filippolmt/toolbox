//go:build dockergate

package container

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/filippolmt/toolbox/internal/imageref"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// Exercise the actual CLI under a controlling terminal: an attach mock cannot
// reproduce the remote shell surviving the client's terminal hangup.
func TestTerminalCloseDoesNotStrandTheContainer(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "toolbox")
	build := exec.Command("go", "build", "-o", binary, "../..")
	// The package's TestMain guards the ambient HOME against writes.
	build.Env = append(os.Environ(), "HOME="+t.TempDir())
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	args := []string{"testdata/terminal_close.py", binary}
	if image := os.Getenv("IMAGE_TAG"); image != "" {
		args = append(args, "--image", image)
	}
	if output, err := exec.Command("python3", args...).CombinedOutput(); err != nil {
		t.Fatalf("terminal lifecycle: %v\n%s", err, output)
	}
}

func TestShellProcessCleanupFencesStartupAndRejectsReusedPIDs(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cli, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Close() }()
	plan := &sessionplan.SessionPlan{
		Image:         sessionplan.Image{Ref: imageref.ResolveImage(os.Getenv("IMAGE_TAG"), "")},
		ContainerName: "toolbox-shell-cleanup-" + rand.Text(),
		Cmd:           []string{"sleep", "infinity"},
	}
	id, err := createAndStart(ctx, cli, plan, plan.Image)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = cli.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true})
	}()
	const probe = `import pathlib, subprocess, sys, tempfile
wrapper, cleanup = sys.argv[1:]
with tempfile.TemporaryDirectory() as directory:
    path = pathlib.Path(directory) / 'session'
    payload = pathlib.Path(directory) / 'started'
    def run(script, *args):
        return subprocess.run([sys.executable, '-c', script, str(path), *args], capture_output=True)
    assert run(cleanup).returncode == 0
    assert run(wrapper, sys.executable, '-c', f'open({str(payload)!r}, "w").close()').returncode == 0
    assert not payload.exists(), 'cleanup allowed a delayed shell to start'
    path.unlink()
    assert run(wrapper, sys.executable, '-c', 'pass').returncode == 0
    assert run(cleanup).returncode == 0
    assert not path.exists(), 'normal exit leaked the session marker'
    # Pause the filesystem publication boundary after the identity is written:
    # cleanup must win the race without ever allowing the payload to run.
    paused = '''import os, sys
link = os.link
def publish(*args):
    print('PENDING', flush=True)
    input()
    return link(*args)
os.link = publish
exec(sys.argv.pop(1))
'''
    delayed = subprocess.Popen([sys.executable, '-c', paused, wrapper, str(path),
                                sys.executable, '-c', f'open({str(payload)!r}, "w").close()'],
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
    try:
        assert delayed.stdout.readline().strip() == 'PENDING'
        assert run(cleanup).returncode == 0
        delayed.communicate('\n', timeout=5)
        assert delayed.returncode == 0
        assert not payload.exists(), 'concurrent cleanup allowed the payload to run'
    finally:
        if delayed.poll() is None:
            delayed.kill()
            delayed.wait()
    path.unlink()
    sibling = subprocess.Popen(['sleep', 'infinity'])
    try:
        with open(f'/proc/{sibling.pid}/stat') as stat:
            birth = int(stat.read().rsplit(')', 1)[1].split()[19])
        path.write_text(f'{sibling.pid} {birth + 1}')
        assert run(cleanup).returncode == 0
        assert sibling.poll() is None, 'cleanup signalled a reused PID'
        path.write_text('invalid marker')
        assert run(cleanup).returncode != 0
        assert sibling.poll() is None, 'invalid identity signalled a sibling'
    finally:
        sibling.kill()
        sibling.wait()
`
	if output, code := dockerExec(ctx, t, cli, id, "/usr/bin/python3", "-c", probe, shellProcessWrapper, shellProcessCleanup); code != 0 {
		t.Fatalf("session identity probe exited %d: %s", code, output)
	}

	// Start a real Docker exec without ever attaching its output, as when
	// the client loses the attach response after the process has started.
	marker := "/tmp/toolbox-shell-" + rand.Text()
	created, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd: []string{"/usr/bin/python3", "-c", shellProcessWrapper, marker, "sleep", "infinity"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ExecStart(ctx, created.ID, client.ExecStartOptions{Detach: true}); err != nil {
		t.Fatal(err)
	}
	const published = `import pathlib, sys, time
path = pathlib.Path(sys.argv[1])
deadline = time.monotonic() + 5
while not path.exists() and time.monotonic() < deadline:
    time.sleep(0.01)
assert path.exists(), 'remote exec did not publish its identity'
`
	if output, code := dockerExec(ctx, t, cli, id, "/usr/bin/python3", "-c", published, marker); code != 0 {
		t.Fatalf("unattached exec startup exited %d: %s", code, output)
	}
	if err := cleanupShell(cli, id, created.ID, marker); err != nil {
		t.Fatal(err)
	}
	result, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil || result.Running {
		t.Fatalf("unattached exec survived cleanup: running=%v, err=%v", result.Running, err)
	}
}
