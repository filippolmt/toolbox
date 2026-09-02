# Refresh Prompt on a Stopped Container: the branch that can honour a yes, and the countdown that has to invert

Status: accepted

[ADR 0005](0005-prompted-image-refresh-on-shell-start.md) made `toolbox shell`
ask before spending a developer's time on a download. The tree it describes
was wired to the create branch alone, and the justification recorded in the
code for skipping the others was that a container which already exists keeps
the image it was created from, so the question *could not be honoured*.

That is true of a **running** container and false of a **stopped** one. A
stopped container runs no process and holds nobody's session, and
`toolbox-reload` already honours exactly this answer for a live one by
recreating it. Nothing in Docker ever prevented the same move here; the
sentence in the code did. So the one branch where a yes is both meaningful and
costs nobody else a session was the branch that never asked.

We extend the prompt to the **start** branch. A yes destroys the stopped
container and the branch becomes the create it already had to become — which
pulls, creates and starts, all of it code that exists. The reload machinery is
deliberately **not** involved: a reload replaces a *live* session and carries a
handover payload (cwd, agent, re-entry argv) precisely because there is a
session to put back. Here there is none, so the payload would be empty, the
casualty list would name nobody, and a re-exec of the host process would buy
nothing.

**Connect stays as it is.** A running container may have panes, agents or a
sibling shell attached, and they die with it — none of them volunteered.
[ADR 0006](0006-idle-reload-onto-a-newer-image.md) is the accepted answer for
that case, and it is accepted because it waits for quiescence first.

**The countdown inverts on this branch.** On create, a window that runs out
answering yes starts a download — the thing the shell would have done
unconditionally before ADR 0005, which is what made an unattended yes
defensible. Here the same default would destroy a container with nobody
looking. So the unanswered window answers **no**, the question shows `[y/N]`
rather than `[Y/n]`, and the wording says *recreate* rather than *download*.
The same rule settles `pull: always`: a policy that has said yes to every
download has said nothing at all about containers, so it keeps pulling without
asking and recreates nothing. Only a developer's own yes may spend a container.

## Considered options

**Leave connect and start alone.** The status quo, and what a reader of the
old comment would have concluded was mandatory. Rejected because the comment
was wrong: the question was skipped on a branch that could answer it, and
nothing but that sentence defended the choice.

**Route the yes through the session reload.** It is the existing act that
recreates a container onto a newer image, so reusing it looks like the
economical move. Rejected: the reload's whole shape is about replacing a live
session — it re-execs the host process to carry a handover across the
teardown, and it gates on nothing and confirms nothing by design. Every one of
those properties is either dead weight or actively wrong when the container is
stopped and the developer is standing at the prompt being asked.

**Ask on connect too, recreating a running container.** Rejected on
ADR 0006's grounds, which are unchanged: attached panes die with the
container, so an unprompted terminal loses a session its owner never
volunteered.

**Keep the elapsed window answering yes everywhere, for one rule.** Rejected.
The rule was never "the window answers yes"; it was "the window may answer
whatever the caller would have done anyway". A recreate is not that, and one
sentence of consistency is not worth a container destroyed by a clock.

## Consequences

**A yes discards the container's writable layer** — whatever was written
inside it outside the bind mounts. That is the accepted cost, and it is small
by construction: every piece of persistent state lives on the `~/.toolbox/`
bind mounts, and `toolbox-reload` already has these semantics for a live
session, so the precedent is the developer's own habit. What made the cost
unacceptable was only an *unattended* yes, which the inverted countdown
removes.

**The branch is rare, and that is the second reason the cost is small.** An
ordinary shell exit kills an `AutoRemove` container and the daemon reaps it,
and `toolbox stop` removes it outright — so a stopped container survives only
a daemon or host restart, a hand-typed `docker stop`, or a container created
before `AutoRemove` was set at create time. The prompt reaching this branch is
therefore not a common event; it is a hole closed.

**Everything that can fail runs before anything is destroyed.** There are
three such things and the order is the design, not a convenience: the download
must have *landed* — a yes the registry could not honour is not an acceptance,
or the writable layer would be spent for an image that never arrived; the
`:local` overlay must have built, since a `RUN` that no longer works would
otherwise leave the developer with neither a session nor the container they
were asked about; and the host ports must be pre-flighted, because a port
another container holds is fixed at `ContainerCreate` and no removal can undo
it. Each of the three fails the shell with the container intact.

**The answer describes the moment it was given, so the container is read
again.** The question holds the terminal for seconds, and a sibling shell on
the same workspace can start the very container the answer is about inside
that window. Force-removing it then would end a session whose owner never
volunteered — the exact collateral that keeps a running container from being
asked in the first place — so the second read decides: still stopped is the
case that was answered and it is replaced, already gone leaves the name free
which is all the removal was for, running again stands the recreate down and
this session joins it, and an unreadable answer destroys nothing.

**What a yes costs becomes an input to the Image Plan's tree, not a new case
in it.** The tree still has one question and the same settled answers; the
caller supplies the stake, which words the question and points the unanswered
window. Keeping it an input is what stops "which branch am I on" from leaking
into a decision tree that is about the registry and the store.

**The update banner's cache is cleared with the container**, as a session
reload already does and for the same reason: it was published about the
container that no longer exists, and left in place it would announce, at the
first prompt of the rebuilt session, an update that session has just adopted.

**ADR 0005's tree stands**, case by case, and so does everything it says about
the probe, the shared cache and a "no" being a postponement — including here,
where a decline arms the idle reload exactly as it does on a create. What this
supersedes is one clause: which branches reach the question at all.
