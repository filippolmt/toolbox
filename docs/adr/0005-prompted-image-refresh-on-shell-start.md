# Prompted Image Refresh on Shell Start: ask before spending the developer's time, and treat "no" as "later"

Status: accepted

`toolbox shell` refreshes the runtime image against its registry on the way
in. The refresh is synchronous and cache-gated, so the cost lands unevenly:
on a cold cache the developer waits out a pull they never asked for, and on
a warm one they get nothing back for the wait they didn't have. Neither
outcome is wrong, and that is the problem — the shell decides, silently,
something the developer has an opinion about.

We make the shell **ask**, with a countdown that answers for a developer who
isn't looking. When the registry is ahead of the local store, a prompt offers
the download with a visible five-second countdown; `y`, a bare Return, or a
timeout all pull synchronously and start the session on the new image, and
`n` starts the session on the image already in the store while the background
prefetch fetches the new one. The countdown is visible rather than implicit
because five seconds of silence is indistinguishable from a hang, and a
developer who looks up to find the download already running should be able
to see why.

Three cases never reach the prompt, because in each of them the answer is
already settled. An image missing from the store entirely is pulled
synchronously and without asking — there is no session to start otherwise, so
the block is not a cost but the only honest thing to do. `pull: always` pulls
without asking, because a policy that has already said yes on every shell
cannot coherently be asked again. `pull: never` neither probes nor prompts,
since a probe is a registry round-trip and not probing is that policy's whole
promise. Without a tty there is no prompt either, and the default inverts:
start now, fetch behind. The interactive default is justified by the work
that follows the wait; a script has no work that follows, so the same wait is
pure latency multiplied by every invocation in a pipeline.

Knowing whether to ask is itself a registry round-trip, so the question is
answered from the update prefetch's shared cache whenever its stamp is warm —
a sibling session that probed a moment ago has already established the fact,
and re-establishing it would reintroduce, one step higher, the latency this
decision is about. Only a cold stamp probes synchronously, and that is
precisely the case where the question is most likely to be worth asking: no
session has been open recently, so the store is probably behind. The probe is
a `DistributionInspect` — metadata, not a download.

## Considered options

**Drop the synchronous refresh entirely** and let the background prefetch own
the registry, with the session adopting the new image at its next reload. It
removes the wait completely and was our first choice. We rejected it because
the first shell after a release then *always* starts behind, and the reload
that would fix it is delayed by the very grace window that protects a session
from being recreated moments after it opened. The mechanism designed to be
unobtrusive would have been most obstructive exactly where it mattered.

**Keep the current unconditional synchronous pull.** Correct, and the status
quo; it simply never lets the developer decline a download they have a reason
to postpone.

**Wait for the pull up to a bounded number of seconds, then continue in the
background.** A timeout instead of a question, which sounds like the best of
both. Rejected: deciding whether to wait needs the synchronous probe anyway,
and a pull of fresh layers almost never lands inside a seconds-scale bound —
so the wait is paid and the session starts on the old image regardless.

## Consequences

A "no" is a postponement, not a refusal, and it says so by arming the
[Idle Reload](../../CONTEXT.md#idle-reload) for that session even when
`update.idle_reload` is off. The alternative — a "no" that holds until the
session ends — would turn a five-minute deferral into a permanent one, which
is not what the developer was asked.

Nothing new downloads on the "no" path. The background prefetch already runs
an immediate pass when a session opens, and it is already what advances the
store; a second fetch started by the declined branch would mean two
concurrent pulls of the same ref at the same moment.
