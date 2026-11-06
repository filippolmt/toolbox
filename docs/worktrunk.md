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
config.

A new worktree is a checkout of tracked files only. `wt step copy-ignored`
copies gitignored files (`.env`, local settings) into it from the main
worktree — **every** gitignored file, build output and dependency trees
included, unless a `.worktreeinclude` file at the repository root lists the
paths to keep. Add `--require-include` to make the copy a no-op in a repository
without that file. Run it by hand, or as a `post-start` hook in the project's
Worktrunk config so every new worktree gets it.

## Agent plugins (opt-in)

Worktrunk ships plugins for Claude Code, Codex and pi: activity markers in
`wt list`, worktree-lifecycle hooks, and a configuration skill. Toolbox does
not install them, because they change how the agent behaves — the Claude Code
plugin, for one, grants the agent permission to enter worktrees without
asking. Installing one is your call:

```sh
wt config plugins claude install
```

It writes into the agent's own config directory, which for Claude Code is the
`claude` mount (`~/.toolbox/.claude` on the host), so it is installed once and
persists across containers.

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
