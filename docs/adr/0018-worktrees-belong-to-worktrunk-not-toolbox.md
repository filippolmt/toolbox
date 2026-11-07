# Worktrees belong to Worktrunk, not toolbox

Status: accepted

Every observation below is **as of the decision** (6 November 2026). It is
evidence for the choice, not a description of the repo today.

The question comes from
[#1370](https://github.com/filippolmt/toolbox/issues/1370). Toolbox shipped its
own worktree manager, `toolbox worktree`, built on one invariant: one branch →
one git worktree under `<repo-root>/.worktrees/tbx-<branch>` → one path-scoped
container running an agent. The invariant had a real cost. A container
scoped to the worktree cannot see the main repo's `.git`, so the
[Session Plan](../../CONTEXT.md#session-plan) carried a dedicated bind for it (`worktree-gitdir`), plus an agent launch
command, a seed pass for gitignored state and a config key to extend it.

The actual workflow never used it. The toolbox is opened on a **parent
folder** holding several repos, and every agent runs inside that single
container. What that workflow lacks is not isolation, it is visibility and
navigation across parallel agents: which one is working, which one waits, what
each branch holds against the default branch, where its PR and CI stand.

[Worktrunk](https://github.com/max-sixty/worktrunk) (`wt`) is built for exactly
that workflow: parallel agents on one shared filesystem. `wt list` shows diff,
ahead/behind, CI, PR and per-agent activity markers; it adds an interactive
picker, `wt switch pr:<n>`, `wt merge`, lifecycle hooks and
`.worktreeinclude` for gitignored state. It ships agent plugins (a Claude
Code configuration skill, activity markers, worktree-lifecycle hooks), and
static musl binaries for both image arches. Its default layout puts each
worktree beside its repo (`../<repo>.<branch>`), which inside a parent-folder
toolbox is already within the mount, so it needs no help from the session
plan.

The decision:

**Toolbox stops managing worktrees.** `toolbox worktree` and everything that
exists only for it — the per-worktree session shape, the `worktree-gitdir`
bind, the agent launch command and the reload path that relaunches it, the
seed pass — are removed. The `agent:` config key goes too if, as it appears,
the worktree command is its only reader. The `worktree:` config key stays
accepted, ignored with a deprecation warning that points to Worktrunk's
`.worktreeinclude`, so an existing config keeps loading; a later release
removes it.

**Worktrunk is bundled in the image, with its shell integration baked in.**
It is one more catalog CLI, pinned and bumped like the others. Its agent
plugins are **not** preinstalled: they change agent behaviour (including an
automatic permission grant for entering worktrees), so installing them is the
developer's call, documented as an opt-in step.

**Toolbox imposes no worktree layout.** Worktrunk's default stands, and its
`worktree-path` setting is the developer's to change.

## Considered options

**Keep `toolbox worktree` and port Worktrunk's best features into it.**
Rejected: it means owning, in Go, a status view, a picker, PR checkout and
merge flows that an upstream project already maintains, for a command the
workflow does not use.

**Run Worktrunk on the host and keep one container per worktree**, launching
toolbox from `wt switch -x` and tearing containers down from `wt` hooks.
Rejected: it adds a host dependency that toolbox cannot pin, and it does not
remove the glue — toolbox would still have to detect a linked worktree and
bind its common git dir, because a plain `toolbox shell` in a worktree has
broken git inside the container. It keeps the isolation the workflow does not
use and pays for it twice.

**Bundle Worktrunk alongside `toolbox worktree`.** Rejected: two worktree
managers with different layouts that do not see each other's worktrees.

## Consequences

**Agents in parallel worktrees share one container.** Processes, ports and
scratch state are no longer isolated per branch. Two dev servers on the same
port collide; Worktrunk's `hash_port` template filter is the upstream answer.
A developer who needs real isolation can still open a separate toolbox on a
worktree path, but git inside it will not resolve: nothing binds the main
repo's `.git` any more.

**Features without an equivalent are gone**, accepted on purpose: the base
branch recorded per worktree and reused by `prune` and `sync`, `sync`'s rebase
with `--force-with-lease` and conflict resume, and the batched
`--delete-remote`.

**Worktrunk's activity markers live in the shared repo's git config.** That is
what should let a `wt list` in any shell see markers written by an agent in
another worktree. Whether markers written inside the container actually show
up in `wt list` was not verified when this was decided.

## Amendment: the image wires the activity markers itself

From [#1388](https://github.com/filippolmt/toolbox/issues/1388). The Claude Code plugin turned out not to be installable here: managed settings
block its marketplace source, and copying the plugin into place by hand would
work around that policy. Its configuration skill had come from another
marketplace instead, and that entry was removed.

So Worktrunk joined Atuin and Herdr as
[Agent Wiring](../../CONTEXT.md#agent-wiring). On every start,
`init.d/62-worktrunk.sh` installs the skill shipped with the bundled release and
registers the activity-marker hooks for Claude Code, Codex and pi. This is
**on by default**, which does not contradict the decision above. That decision
kept the plugins opt-in because they change agent behaviour, and the wiring
leaves out exactly the two hooks that do:

- the `PermissionRequest` auto-approval for entering worktrees;
- the `WorktreeCreate`/`WorktreeRemove` routing of Claude's worktree isolation
  through `wt`.

`TestWorktrunkInitWiresMarkersOnly` holds that line. Installing the upstream
plugin remains possible wherever the marketplace is allowed, and it adds those
two hooks.

## Amendment: the activity markers cross worktrees

From [#1375](https://github.com/filippolmt/toolbox/issues/1375), checked on
7 November 2026 inside one toolbox opened on a parent folder. A marker set
with `wt config state marker set` from a linked worktree shows in a `wt list`
run from the main worktree, and a live Claude Code session's hook kept 🤖 on
its branch while it worked. Markers are stored as
`worktrunk.state.<branch>.marker` in the repository's git config on the host
share; that they outlive the container is inferred from where they are
stored, not observed across a `toolbox stop`. The rest of #1375 stays open:
Codex's `PermissionRequest` and `SessionEnd` hooks and its trust prompt, and
pi's extension, have not been seen firing in a live session.

## Amendment: the image ships Worktrunk defaults

Toolbox serves many teams, and a configuration each developer writes by hand
is one each of them forgets, and rewrites after reinstalling a machine. So the
image now ships an [Image Default](../../CONTEXT.md#image-default) for
Worktrunk: its system config (`/etc/xdg/worktrunk/config.toml`) runs
`post-start` hooks that copy local settings and `.worktreeinclude` files and
prune merged worktrees, and sets commit message generation. Claude Code's
managed instructions tell agents to create worktrees with `wt` instead of
their own worktree isolation.

This narrows "toolbox imposes nothing" above without reversing it. The layout
stays Worktrunk's default; the defaults sit in the system layer, below each
developer's config, which overrides or adds to them; and `--no-hooks` or an
empty `WORKTRUNK_SYSTEM_CONFIG_PATH` turns them off. The two upstream plugin
hooks the earlier amendment left out stay out.

