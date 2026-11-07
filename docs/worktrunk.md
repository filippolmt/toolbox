# Parallel agents with Worktrunk

The image bundles [Worktrunk](https://github.com/max-sixty/worktrunk) (`wt`), a
git worktree manager built for several agents working side by side on one
filesystem. Toolbox does not manage worktrees itself: why is recorded in
[ADR 0018](adr/0018-worktrees-belong-to-worktrunk-not-toolbox.md). This page
covers only what the toolbox changes about using `wt`; the commands themselves
are upstream's to document — `wt --help`, or the
[Worktrunk docs](https://worktrunk.dev).

## The workflow

Open one toolbox on the folder that holds your repositories, then start one
worktree per task from inside it:

```sh
toolbox shell ~/projects
cd ~/projects/my-repo
wt switch -c -x claude feat-auth -- 'add an auth module'
wt switch -c -x codex fix-pagination -- 'fix the pagination bug'
wt list        # every worktree: diff, ahead/behind, CI, PR, agent activity
```

Every agent runs in the same container. `wt switch <branch>` moves the current
shell into an existing worktree, `wt switch pr:<n>` checks out a pull request,
`wt merge` lands a branch, and `wt remove` cleans one up.

## Layout

Worktrunk's default places each worktree beside its repository, as
`../<repo>.<branch>`. With the toolbox opened on the parent folder that path is
already inside the mount, so it needs nothing from toolbox. Toolbox imposes no
layout: change it with Worktrunk's own `worktree-path` setting.

A worktree placed outside the mounted folder is not visible in the container.
The reverse also holds: a toolbox opened on a single worktree path cannot run
git there, because a linked worktree's `.git` points into the main repository,
which that session does not mount.

## Shell integration

`wt switch` can change the shell's directory only through a shell function.
The image's zshrc evaluates `wt config shell init zsh` on every start, so the
function is always there. Do not run `wt config shell install`: it would write
into the image-owned `~/.zshrc`, which an image update replaces. Shell
customisation goes in `~/.toolbox-state/zshrc.d/*.zsh`
([user config](internals/shell-start.md#user-config-in-zshrcd)).

## State that persists

Worktrunk's user config — the `worktree-path` template, project hook commands
you have approved, anything `wt config` writes — lives in
`~/.config/worktrunk`. The default `worktrunk` mount binds it to
`~/.toolbox/worktrunk` on the host, so it survives `toolbox stop` like every
other tool's state ([mounts](mounts.md)). Per-repository state, including the
agent activity markers `wt list` shows, lives in the repository's own git
config, which every worktree of that repository shares: a `wt list` in any
of them shows the markers set from the others.

A new worktree is a checkout of tracked files only. `wt step copy-ignored`
copies gitignored files (`.env`, local settings) into it from the main
worktree — **every** gitignored file, build output and dependency trees
included, unless a `.worktreeinclude` file at the repository root lists the
paths to keep. Add `--require-include` to make the copy a no-op in a repository
without that file.

## What the image configures

The image ships Worktrunk's system config, `/etc/xdg/worktrunk/config.toml`
(source: `internal/build/assets/worktrunk.toml`), so every developer gets the
same behaviour with nothing to write and no approval prompt:

- **On every new worktree** (`post-start`): your `.claude/settings.local.json`
  is copied in from the main worktree; whatever the repository lists in its
  `.worktreeinclude` is copied too (nothing without one); then `wt step prune`
  removes the worktrees and branches already in the local default branch,
  skipping any with uncommitted changes, locked, or younger than a day.
- **Commit messages** for `wt step commit` and `wt merge` come from the bundled
  Claude Code. They need a Claude login.

Your own `~/.config/worktrunk/config.toml` ranks above it. A value set there
replaces the image's; a `post-start` hook there runs alongside the image's,
and one with the same name replaces it, so `prune = "true"` turns off just the
prune. Keep your hooks in a `[post-start]` table: a `[[post-start]]` pipeline
in your config replaces all of the image's hooks. To skip every hook for one
command, pass `--no-hooks`; to drop the system config for good, set
`WORKTRUNK_SYSTEM_CONFIG_PATH` to an empty value through the toolbox `env:`
key ([env passthrough](configuration.md#env-passthrough)), which takes effect
once the container is recreated (`toolbox stop`). A repository that needs more
files in every worktree adds a `.worktreeinclude`; a team hook belongs in the
repository's `.config/wt.toml`, which Worktrunk asks each developer to approve
once.

## Agent integration

Every container start wires Worktrunk into Claude Code and pi
(`init.d/62-worktrunk.sh`, [Agent Wiring](../CONTEXT.md#agent-wiring)); Codex
gets its markers from the image instead:

- **The configuration skill** (`worktrunk`). It is extracted from the source
  archive of the release that ships the bundled `wt`, so it always describes the
  binary you run, and it goes into every agent's skill directory.
- **Activity markers**: 🤖 while the agent works and 💬 while it waits for you,
  shown in `wt list`. These are hooks that call the bundled `wt`, registered in
  Claude's `settings.json`, pi's extensions directory and Codex's system layer,
  `/etc/codex/hooks.json` ([Image Default](../CONTEXT.md#image-default)). Codex
  runs a system-layer hook with no trust prompt, where one in
  `~/.codex/hooks.json` waits until you trust it by hand; it also cannot be
  switched off one by one, only with all hooks (`[features] hooks = false`).

Upstream's agent plugins add two hooks that are left out on purpose, because
they change how the agent behaves: an automatic approval for entering
worktrees, and the routing of Claude Code's worktree isolation through `wt`.
Why the image wires the markers itself instead of installing the plugin is
recorded in the
[ADR 0018 amendment](adr/0018-worktrees-belong-to-worktrunk-not-toolbox.md#amendment-the-image-wires-the-activity-markers-itself).
Where your Claude Code policy allows the upstream marketplace,
`wt config plugins claude install` still adds the full plugin on top.

## Sharing one container

Parallel worktrees are not isolated from each other: they share processes,
ports and scratch space. Two dev servers on the same port collide; Worktrunk's
`hash_port` template filter derives a stable port per branch. For a branch
that needs real isolation, open a separate toolbox on the parent folder under
a [profile or named shell](shells.md) rather than on the worktree path.

## Migrating from toolbox worktree

`toolbox worktree` and its alias `toolbox wt` are gone; inside the container,
`wt` is now Worktrunk. Worktrees it created under `<repo>/.worktrees/tbx-<branch>`
are ordinary git worktrees: `wt list` shows them, and `git worktree remove`
removes them.

Two config keys only that command read, and both are now ignored with a
warning at load and in `toolbox config doctor`:

- `worktree:` (its `seed` list) — list the paths in `.worktreeinclude` and
  copy them with `wt step copy-ignored`. The old seed list was an allowlist on
  top of fixed defaults (`.claude/settings.local.json`, `.env*`, `openspec/`,
  `.planning/`); without a `.worktreeinclude`, `copy-ignored` copies everything
  gitignored instead.
- `agent:` — name the agent per worktree with `wt switch -x <agent>`.

Remove them from `.toolbox.yaml` to silence the warning. A later release stops
recognising them.
