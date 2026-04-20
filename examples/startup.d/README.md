# Startup hooks

This directory contains **example** scripts for the toolbox's startup-hook mechanism. Each file is illustrative — copy the ones you want into `~/.toolbox/startup.d/` on your host, edit as needed, and they run automatically at every `toolbox shell`.

## How the hook runs

On the host:

```
~/.toolbox/startup.d/
├── gsd.sh          # your scripts, any name ending in .sh
├── direnv.sh
└── ...
```

At container start, `internal/build/assets/entrypoint.sh` iterates the directory and executes each `*.sh` file with `bash`. Output is printed before the bash prompt, indented under a `Toolbox startup hooks:` banner. A failing hook logs its exit code but never aborts the entrypoint — you always get a shell.

The directory is mounted **read-only** into the container at `/home/toolbox/.toolbox-startup.d/`. Edits happen on the host (e.g. with your editor, or via chezmoi / dotfiles), never from inside the container.

## Disabling a hook

Rename it so the glob stops matching:

```
mv ~/.toolbox/startup.d/gsd.sh ~/.toolbox/startup.d/gsd.sh.off
```

## Writing your own hook

Minimum working example:

```bash
#!/usr/bin/env bash
set -eu
echo "    hello from my hook"
```

Good practices:
- **Be idempotent.** A hook runs every start; use sentinels in `~/.toolbox-state/` to skip work that's already done.
- **Be fast in the common case.** If there's nothing to do, the hook should exit in milliseconds.
- **Use `npm install -g`, not sudo.** The container sets `NPM_CONFIG_PREFIX=/home/toolbox/.npm-global` and mounts that dir from `~/.toolbox/npm-global` on the host, so runtime globals survive container recreation.
- **Don't mask credentials with your output.** Toolbox prints a cred-status banner; keep your hook's output terse and aligned.

## Examples

### [`gsd.sh`](./gsd.sh) — Get-Shit-Done

Installs [`get-shit-done-cc`](https://github.com/gsd-build/get-shit-done) idempotently. The installer builds `gsd-sdk` from source and drops it into the per-user npm prefix (persisted via the `~/.toolbox/npm-global` mount), then copies the `/gsd-*` skill pack into `~/.claude/skills/get-shit-done` (persisted via the `~/.toolbox/.claude` mount). Version drift is detected via a sentinel in `~/.toolbox-state/gsd.version`; bump `GSD_VERSION` inside the script and the next startup reconciles.
