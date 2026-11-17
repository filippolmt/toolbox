# Shells can outlive their terminals

Status: accepted

From [#1499](https://github.com/filippolmt/toolbox/issues/1499), part of
[#1426](https://github.com/filippolmt/toolbox/issues/1426).

Until this decision a [Shell](../../CONTEXT.md#shell) was disposable by
construction: created with `AutoRemove`, killed by
[Teardown](../../CONTEXT.md#teardown) when the last attached terminal exits.
Nothing that needs to keep running with no terminal open — Orca's headless
runtime, serving agents started from the desktop app — fits that model: closing
the last tab would kill every agent it started.

The decision:

**A Shell can be detached.** A [Detached Shell](../../CONTEXT.md#detached-shell)
is created and started without an exec, carries the `toolbox.detached=true`
container label, and is spared by Teardown on shell exit whether or not sibling
execs remain. It ends only through `toolbox stop <target>` or
`toolbox stop --all`; `toolbox list` marks it. The label lives on the
container, so the policy needs no state beyond the inspect it already does.

**No user-facing flag.** The only planned caller is the Orca Shell of #1426. A `--detach` on
`toolbox shell` would turn every Shell into a candidate for a container nobody
remembers starting, and nothing has asked for it.

## Considered options

**A keep-alive exec**: start an idle exec in the container so the sibling-exec
check always finds one running. Rejected: it fakes a terminal to fool a policy
instead of stating the property, it is a process someone can kill by accident,
and `toolbox list` would have nothing to mark.

**A user-side idle terminal**: document "keep a `toolbox shell` open". Rejected:
it is the hand-assembled recipe #1426 exists to remove, and closing the wrong
tab kills the runtime.

**Stay disposable**: run the server only while a terminal is attached.
Rejected: the desktop app reaches the runtime with no terminal open, which is
the use case.

## Consequences

**The invariant has an exception.** "A Shell is gone once its last terminal
exits" now holds for every Shell without the label. Readers that reasoned from
it — `toolbox list`'s help, the commands guide — say so.

**A detached Shell is visible, not self-cleaning.** It survives until a stop,
so it can be forgotten; `toolbox list` marking it is the mitigation.

**Recreation is a separate step.** An accepted start-up refresh or a Session
Reload must recreate a Detached Shell as detached; that is
[#1500](https://github.com/filippolmt/toolbox/issues/1500), not this decision's
implementation.
