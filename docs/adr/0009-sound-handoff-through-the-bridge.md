# Sound handoff through the bridge: herdr stays in the container, the host plays the sound

Status: accepted

Every figure below is **as measured when this decision was taken** — evidence for the choice, not a description of the repo today. Nothing here is kept in sync; current values live in the files that set them.

herdr runs inside the toolbox container: the TUI and its server both, with
`~/.config/herdr` and `~/.local/state/herdr` persisted through the binds at
`internal/mountplan/defaults.go`. Its agent-state sounds never played, and the
cause is not configuration. The Linux build carries no audio backend at all —
it is a static musl binary with no `lib*.so` reference, no `dlopen`, and no
symbol or string for ALSA, PulseAudio, PipeWire, CoreAudio or any Rust audio
crate. `src/sound.rs` writes the MP3 to a temp file named
`herdr-sound-<pid>-<n>.mp3` and spawns external players in a fixed order until
one succeeds:

```
paplay <file>
pw-play <file>
ffplay -nodisp -autoexit -loglevel quiet <file>
mpg123 -q <file>
mpv --no-video --really-quiet <file>
```

None of the five is in the runtime image, and the container has no `/dev/snd`,
no `/proc/asound` and no sound-server socket. So the feature failed twice over,
and it said so: `herdr-client.log` had accumulated 15 warnings across five and
a half weeks — 10 `sound=Done`, 5 `sound=Request` — each reading
`no mp3-capable audio player available` followed by all five players with
`os error 2`. Upstream's documentation describes sound as cross-platform and
names no Linux player prerequisite; the open discussion #1766 asks for exactly
that caveat to be documented. The silence is upstream degrading silently, not a
misconfigured host.

We keep herdr where it is and move only the **playback**. A shim named `paplay`
in the image reads the MP3 herdr just wrote, posts its bytes to a new `/sound`
route on the bridge, and the host daemon writes its own temp file and plays it —
`afplay` on a darwin host, the same probe chain on a Linux one. herdr remains
the source of the signal: which sound, for which agent, and when, all stay its
decision.

## The container keeps herdr, so the two obvious alternatives are out

**An audio path out of the container** — PulseAudio over TCP to a daemon on the
host — was rejected before its cost was even weighed, because it would not
work. herdr does not talk to a sound server; it spawns a player binary. A
`PULSE_SERVER` pointing at the host would be reached by nothing, since the
image has no PulseAudio client either. Adding both the client and a host daemon
means running infrastructure on the developer's machine that exists for this
one purpose, that toolbox neither installs nor verifies, and whose absence
returns the failure mode we are fixing: silence.

**herdr on the host**, with each pane entering a `toolbox shell`, would give
native playback for free — agents would still run in the container, and herdr
detects them by reading the PTY. It loses the control plane. The API socket is
container-local (`HERDR_SOCKET_PATH` relocates it to `/tmp/herdr`, because the
`~/.config/herdr` bind is a Docker Desktop fakeowner filesystem that rejects
`chmod()` on a socket), so a container-side `herdr` CLI cannot reach a host
server: the skill installed by `init.d/61-herdr.sh` stops working, and so does
the `herdr integration install claude` hook that reports agent state. Mounting
the socket back in is not available on this host class either — Docker Desktop
cannot share host unix sockets with containers, which is why the bridge's own
`bridge-run` bind is inert on macOS and the shim falls back to TCP via
`host.docker.internal`. Two herdr binaries — one pinned by `HERDR_VERSION`, one
updated by hand — would also have to keep speaking the same protocol.

## The payload carries content, not a path

The temp file lives in the container's `/tmp`, which the host cannot read, so
the shim must send something. It sends the **bytes**, base64-encoded, and the
daemon writes them to a directory it chooses itself.

A caller-supplied host path is precisely what the bridge already refuses:
`internal/bridge/allowlist.go` keeps `/open` to `http` and `https` because
`file://` "would let a malicious in-container process exfiltrate or trigger
reads against arbitrary host paths via the user's default handler", and
`/proximo` tolerates a host path only because `checkHostDir` validates it and
[ADR 0004](0004-proximo-full-surface-through-the-bridge.md) could name why the
daemon cannot derive it. Here the daemon can derive it, so it does.

A shared temp directory — one more RW bind, the shim copying the file, the
payload carrying only a basename — was rejected for its total cost rather than
its payload size: another mount in the plan, a basename to validate against
traversal, a cleanup contract straddling the boundary, and the lesson the
[Bridge Run Mount](../../CONTEXT.md#bridge-run-mount) entry already records about what virtiofs
does to a bind the host wants to remove. A host-side cache keyed by content
hash was rejected as a second piece of state to invalidate for a payload that
arrives a few times a week.

The residual risk moves from the path axis to the content axis: any process in
the container can make the host play arbitrary audio up to the body cap. That
is annoyance, not escalation, and it sits under the ceiling ADR 0004 already
declares — the bridge token is readable inside the container by design, and the
daemon cannot tell which workspace a request came from, so a per-workspace
toggle protects against accident, not against a hostile container process.

## Fire-and-forget, because blocking would serialise the sounds

The daemon spawns the player detached and answers `200` immediately. Waiting
for playback would block herdr's client for the length of the chime, and two
completions 200 ms apart would queue instead of overlapping — the overlap is
what herdr would do natively on the host, and it is what we want. The cost is
that a failing player cannot be reported back, so herdr believes it played.
That costs nothing here: its fallback chain is the other four players, which
are absent from the image by construction and will stay absent.

Concurrency is otherwise left alone. Serialising would mean a queue in the
daemon and a `Done` heard three seconds after it happened, which is worse than
two overlapping chimes. herdr already applies its own brake upstream:
`ui.toast.delay_seconds` defaults to 1, and any new state change on a pane
cancels that pane's pending notification.

## Why the shim is called `paplay`

Nobody calls `paplay`. herdr *probes* for it, and the shim's existence is what
selects it out of the five-name chain — which is why the concept gets its own
glossary entry rather than a comment. Taking the first name in the chain wastes
no `exec`; taking a later one would burn four failed spawns per sound,
invisibly, since herdr logs only when all five fail.

A shim named after a PulseAudio tool that never speaks to PulseAudio is a lie
to the next reader, mitigated the way the other three shims mitigate theirs
(`xdg-open`, `code`, `proximo` all shadow a real binary to move the action to
the host) — with the reason in the file header, and by the fact that PulseAudio
is not in the image and is not coming.

The shim exits **non-zero** when the bridge is unreachable. Not for severity:
because herdr then tries the other four, finds them missing, and writes the
same aggregate `no mp3-capable audio player available` line that diagnosed this
bug in the first place. The defect stays self-diagnosing. The one human-facing
message remains the bridge-install tip `cmd/shell.go` already prints when
`bridge` is enabled — reworded, not duplicated.

## What this does not deliver

The firing rule is herdr's and is hardcoded. Of its 24 `ui.sound.*` keys — a
master switch, three MP3 path overrides and twenty per-agent mutes — none
controls *when* a sound fires. The predicate is
`active_tab_suppresses_notifications(is_active_tab, outer_terminal_focus)`, and
the two sounds are gated asymmetrically: `Request` (an agent blocked, waiting
for a human) fires unconditionally, while `Done` fires only when the pane is in
a non-active tab **or** the terminal window is unfocused, read from DEC 1004
focus reporting on the client's stdout. Panes in the same tab share
`is_active_tab`, so splits do not create background: a single-tab multi-pane
layout suppresses every `Done` while the terminal has focus. That is why the
log showed 5 `Request` and 10 `Done` — the ten fired because the developer had
switched away from the window. A detached client gets no sound at all; the
message is dropped, not queued.

This is left exactly as it is. It interrupts when an agent is waiting on a
human and stays quiet for work finished in front of the developer's eyes, which
is the behaviour a tuned notification system would have converged on anyway.

## What stays outside the repo

Two lines live in the developer's own configuration and the image does not
write them.

`bell-features = system` in the host terminal's config is the net that always
sounds. The bell is not focus-gated: on macOS Ghostty rings `NSSound.beep()`
for `system` and plays the file for `audio` with no focus condition, and audio
support for both landed in 1.3.0. It is one sound with no `Done`/`Request`
distinction and the wrong predicate — it is the bell of the program in the
pane, not herdr's state — so it is a net, not a solution.

`[ui.toast] delivery = "terminal"` adds the banner. herdr emits **OSC 9** (not
OSC 777) on the attached client's own stdout, which in the container is the
`docker exec` TTY and therefore reaches the host terminal; sound and toast are
independent `ServerMessage`s, so the toast setting does not gate the sound —
which is why the 15 warnings accumulated with `delivery` at its default `off`.
On macOS the banner is then suppressed whenever the surface is focused:
`requireFocus` is a hardcoded Swift default parameter, not a config key, the
only control is the all-or-nothing `desktop-notifications`, and a suppressed
notification is silently dropped — no banner, no sound, never added to
Notification Center, and actively removed after three seconds. The two gates
compose well by accident: `Done` is emitted only when the window is unfocused,
which is when Ghostty will show it, while `Request` is emitted always and its
banner is lost while the developer is at the terminal. The sound still plays.

A double-BEL hack — one bell for `Done`, two for `Request`, no repo change at
all — was considered and rejected. It distinguishes the two states, and gives
up the per-agent mutes, the background gating, and any hope that a developer
counts beeps.

## Implementation surface

Host CLI:

- `internal/bridge/contract.go` — `RouteSound` and the payload's `Field*`
  constants, plus the body cap. Both halves are pinned automatically by
  `TestBridgeContract_ShimMatchesGo` and `TestBridgeContract_JSONTagsMatchConstants`.
- `internal/bridge/daemon.go` — request/response structs, `handleSound`, the
  `mux.HandleFunc` registration, the `handlerFns` field, `withHostDefaults`,
  `DaemonOptions`. Its own rate-limit bucket, not the one `/open`, `/edit` and
  `/proximo` share, so a burst of chimes cannot starve an OAuth redirect.
- `internal/bridge/sound.go` plus the per-OS trio `sound_darwin.go` (`afplay`),
  `sound_linux.go` (the host probe chain, chosen by the daemon and never by the
  caller, so there is no third allowlist to maintain) and `sound_other.go` (a
  clear refusal). The spawned player gets a deadline and a reap, as herdr's own
  `terminate_and_reap` does; the goroutine that waits on it removes the temp
  file. Logging follows the `/credential` discipline — the verb, the size and
  which sound, never the bytes.
- `cmd/shell.go` — the reworded bridge-install tip.

Body cap: **512 KiB**, `413` beyond it, read as `limit+1` the way `decodeJSON`
already does. herdr's built-in chimes are 34 KB and 47 KB, so roughly 63 KB
base64-encoded at worst; the cap is sized for a developer's own `done_path`
rather than for the minimum observed, because 128 KiB would be discovered as a
`413` from a daemon nobody was watching.

Container assets:

- `internal/build/assets/bin/paplay` — the shim, sourcing `bridge-lib.sh`.
  `paplay <file>` and nothing else; any other argv is a usage error with exit 2.
- `internal/build/assets/Dockerfile` — the `COPY --link --chmod=0755` beside
  the other shims.
- `internal/build/assets/smoke-test.sh` — one check beside the other shim
  checks. No catalog row and no `init.d` script: shims are exempt, and the
  smoke test pins the `init.d` count.

Documentation:

- `CONTEXT.md` — two entries, written **with** the code and not before, because
  the file is a glossary and declares itself not to be a specification.
  **Sound Handoff**: the container→host transfer of a sound to play, carrying
  content rather than a path — linking to `allowlist.go`'s threat model instead
  of restating it. **Probed Shim**: an image shim that answers to a name
  another tool probes for, which is why `internal/build/assets/bin/paplay` has
  no caller anywhere in the tree.
- `docs/bridge.md` — the new route in the security-boundary enumeration.

Gates: `make go-check` and `make test`, coverage at or above 80%. The Linux
daemon branch is the only one CI can exercise; the darwin branch stays a manual
check on the host.

Upstream follow-up: a comment on herdr's discussion #1766, which asks for the
missing-player case to be documented. The defensible ask is that it degrade
rather than fail silently — the client already forwards a pane's terminal bell,
so a bell when no player is available would be audible where an unreadable log
line is not.
