# Idle Reload onto a Newer Image: one mechanism, a second trigger, and a predicate that says when it is safe

Status: accepted

A session that falls behind the local store already learns about it — the
update banner says so at the prompt — and already has the remedy:
`toolbox-reload` recreates the container on the newer image and puts the
developer back where they were. What it does not have is a way to happen
without being typed. On a machine where images ship on every merge, the
banner is a recurring reminder to run a command whose answer is always yes.

We add a second **trigger** for the existing act, never a second act. The
`precmd` hook writes the [Reload Marker](../../CONTEXT.md#reload-marker) and
exits exactly as the typed function does, so teardown, create, re-exec and
the re-entry form are reached through the one path that already carries the
handover payload and the failure advice. Having the host detect and force the
recreation instead would have produced a second way to destroy a container,
and the two would have drifted.

The trigger fires only under
[Session Quiescence](../../CONTEXT.md#session-quiescence): shell at the
prompt, no background jobs, exactly one interactive shell holding a tty in
the container, a five-minute window elapsed since the more recent of the
container's own birth and a declined start-up prompt, and
`TOOLBOX_RELOAD_MARKER` present. The sibling clause is the one worth
defending: attached panes die with the container, so without it an unattended
reload would end a session whose owner never volunteered for it — and a tmux
pane inside the container counts, deliberately, because it dies the same way
an external attach does. Counting interactive shells from inside the
container keeps the check local: asking the daemon over DooD would turn a
prompt hook into a daemon round-trip and would fail wherever DooD is off.

It is opt-in through `update.idle_reload`, config-file only. The suppression
switch that silences the banner is a per-session convenience and belongs in
the environment; arming something that destroys a container is a durable
preference, and a developer who wants it just once already has the command.
`TOOLBOX_NO_UPDATE_CHECK` disarms this along with the probe and the banner:
having said "tell me nothing about updates" must not leave the session
liable to be rebuilt underfoot in silence. The typed command survives every
switch here, because it reloads onto whatever the store holds and stays
useful after a local `toolbox build`, where no update check is involved.

The image ships on merge and the CLI on tag, so a new image can meet a CLI
that injects no marker. That refusal is inherited unchanged and needs no new
version check: presence of the variable is the capability, and the hook
already reads it.

## Considered options

**Reload as soon as the new image is in the store.** The most direct reading
of "do it for me", and the reason quiescence exists: it would kill a test run
or a working agent mid-flight.

**Prompt in the shell and reload on a timeout.** Rejected because the reload's
own documented rule is that it gates on nothing and confirms nothing — a
prompt there would fire on nearly every reload, which is what that rule was
written to prevent.

**Leave it manual.** The status quo, and still what an unset key gives.

## Consequences

The rule that the reload gates on nothing survives intact, because it always
described the *act*, and the act is unchanged. Every gate added here lives in
the trigger — which is the only place a gate makes sense once the developer
is no longer the one deciding.

Abstention is announced. When the image is ready and quiescence does not
hold, the banner names the single failing clause, in the fixed order sibling
→ window → job, most durable first: a transient reason resolves itself while
it is being read, so leading with it prints a line that is already false. One
reason rather than all of them follows the prefetch's existing discipline —
the banner is a line in a prompt, not a diagnostic report.

The window and the prefetch TTL are separate constants despite currently
expressing similar durations. Folding them together would let anyone tuning
the probe cadence shorten the protection a session gets, without any sign
that the two were connected.

Coverage is split by what each layer can actually prove. Go units and the
existing shell-side contract test cover the pieces they own; the "exactly one
interactive shell" clause additionally needs the real-daemon gate, because it
is the kind of condition that passes in a unit test and fails in a real
terminal.
