"""Real-CLI regression: terminal hangup must not leave an orphan exec."""

import argparse
import errno
import fcntl
import os
from pathlib import Path
import pty
import select
import subprocess
import tempfile
import termios
import time
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("binary", type=Path)
parser.add_argument("--workspace-parent", type=Path)
parser.add_argument("--image", default="ghcr.io/filippolmt/toolbox:latest")
args = parser.parse_args()
binary = args.binary.resolve()


def wait_for(fd, marker):
    output = b""
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        if select.select([fd], [], [], 0.1)[0]:
            try:
                chunk = os.read(fd, 65536)
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise
                break
            if not chunk:
                break
            output += chunk
            if marker in output:
                return
    # Shell startup output may contain credentials: never dump it on failure.
    raise AssertionError(f"terminal did not produce {marker!r}")


def ready(fd):
    # The echoed command must not itself contain the complete output marker.
    os.write(fd, b"printf '\\nTERMINAL_%s\\n' READY\n")
    wait_for(fd, b"TERMINAL_READY\r\n")


with tempfile.TemporaryDirectory(dir=args.workspace_parent) as directory:
    workspace = Path(directory)
    name = "terminal-close-" + uuid.uuid4().hex
    config = workspace / "config.yaml"
    config.write_text(
        f"mounts_root: {workspace}/state\n"
        f"image: {args.image}\n"
        "pull: never\nbridge: false\nproximo: false\nimage_reclaim: false\n"
        "mounts:\n"
        "  - {name: ssh, disabled: true}\n"
        "  - {name: gitconfig, disabled: true}\n"
        "  - {name: docker-sock, disabled: true}\n"
        f"shells:\n  {name}:\n    path: {workspace}\n"
    )
    command = [str(binary), "--config", str(config), "shell", name]
    sessions = []

    def launch():
        master, slave = pty.openpty()

        def terminal():
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)

        env = os.environ.copy()
        # Do not allow the invoking user's image/bridge/peer overrides to leak in.
        for key in ("TOOLBOX_IMAGE", "TOOLBOX_PULL", "TOOLBOX_BRIDGE", "TOOLBOX_PEER_MESSAGING"):
            env.pop(key, None)
        proc = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave,
                                preexec_fn=terminal, env=env)
        os.close(slave)
        session = [proc, master]
        sessions.append(session)
        ready(master)
        return session

    def hangup(session):
        proc, fd = session
        os.close(fd)
        session[1] = None
        assert proc.wait(timeout=10) == 0, "terminal hangup skipped clean shell teardown"

    def exit_shell(session):
        proc, fd = session
        os.write(fd, b"exit\n")
        assert proc.wait(timeout=10) == 0, "exit returned an error"

    def assert_removed():
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            remaining = subprocess.check_output(
                ["docker", "ps", "-aq", "--filter", "name=^toolbox-named-" + name + "$"],
                text=True,
            )
            if not remaining.strip():
                return
            time.sleep(0.1)
        raise AssertionError("container still running after terminal hangup -> reattach -> exit")

    try:
        first = launch()
        hangup(first)
        second = launch()
        exit_shell(second)
        assert_removed()
        print("PASS: terminal hangup -> reattach -> exit removes the container", flush=True)

        first = launch()
        sibling = launch()
        hangup(first)
        ready(sibling[1])
        exit_shell(sibling)
        assert_removed()
        print("PASS: hangup preserves the sibling; its final exit removes the container", flush=True)
    finally:
        for proc, fd in sessions:
            if proc.poll() is None:
                proc.kill()
                proc.wait()
            if fd is not None:
                os.close(fd)
        subprocess.run(["docker", "rm", "-f", "toolbox-named-" + name],
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
