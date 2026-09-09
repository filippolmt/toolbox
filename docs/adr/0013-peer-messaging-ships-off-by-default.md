# Peer messaging ships off by default, amending ADR 0003

Status: accepted

Every figure below is **as measured when this decision was taken** — evidence for the choice, not a description of the repo today. Nothing here is kept in sync; current values live in the files that set them.

[ADR 0003](0003-cross-container-peer-messaging.md) made cross-container peer
messaging work and turned it **on by default**, on the argument that
"messaging between sessions is the useful default" and that declining it for a
single run has to be as cheap as leaving it on. That argument rested on two
premises. The first is that the feature is used. The second is that the
isolation cost — participating containers share a PID namespace and therefore
see each other's process table — is worth paying unattended, by every
workspace, unless told otherwise.

The first premise no longer holds. The maintainer stopped using
`ListAgents` / `SendMessage` across containers, and the repo has no other
user to speak for: public, three stargazers, no forks, no human contributor
other than the author in the preceding year, and no pull request from anyone
but the author and Renovate. There is no population paying the isolation cost
in exchange for a feature someone is using.

Meanwhile the standing cost is not zero and not hypothetical. With the default
on, `toolbox-peer-anchor` is created by the first shell of the day and outlives
every session that referenced it — ADR 0003 recorded this as "the anchor is
effectively always-on". Nothing sweeps it but `toolbox stop --all`, and
[`List`](../../CONTEXT.md#shell) hides it by name, so the honest reading of the
current behaviour is: an always-on container the inventory command declines to
show, holding a namespace for a feature nobody invokes.

So the shipped default moves to `false`. The seeded value in
`internal/config/plan.go` is the whole change in behaviour; everything ADR 0003
built stays exactly as it is — the anchor, its `tini -g` PID 1 and the reaping
self-heal, the `toolbox-cc-socks` volume and its one-time ownership init, the
fold of the setting into the container identity, and the real-daemon gate that
pins the messaging path. `peer_messaging: true` in either config layer, or
`toolbox shell --peer` for one run, turns it on and gets all of it back.

## Considered options

**Delete the feature.** Tempting, and wrong. Removing it takes the anchor, the
volume init, the identity fold, the stale-anchor replacement and their tests
with it — a large, hard-to-reverse subtraction whose only benefit is code that
costs nothing while the default is off. Peer messaging also rests on Claude
Code internals that move: an upgrade that makes cross-container messaging
attractive again would find the machinery here and one bool to flip, or a
rewrite from the ADR. Off-by-default keeps the option; deletion spends it.

**Leave the default on and opt out in `~/.toolbox.yaml`.** This is what the
maintainer would do for their own machine, and it fixes nothing for the
shipped artefact: a fresh install still stands up an anchor for a feature its
user has not asked for, and the isolation cost stays the unattended default.
The reason to prefer the config layer — not making others pay for one user's
change of habit — has no one to protect here.

**Make the setting tri-state (`*bool`), like `image_reclaim`.** The tri-state
exists where "absent" and "explicitly false" must be told apart, so a lower
layer cannot silently re-arm a sweep the user disabled. With the default off,
absent and explicit `false` resolve to the same value through every consumer
that acts on it, so the third state would change no behaviour. It would change
one report: `configedit.Compute` credits a layer only where its resolved value
differs from the layer below, so a hand-written `peer_messaging: false` is now
attributed to `(default)` by `config show --origin` — the mirror of the
`peer_messaging: true` that was invisible there before the flip, and the same
quirk every key written at its own default value already has (`bridge: true`,
`pull: auto`). A tri-state for this key alone would buy one accurate origin
line and hand every consumer a nil to handle; the fix, if the line is worth
fixing, is provenance reading the raw file layers for all keys, not a third
state here. The plain `bool` stays; the
`SetDefault` behind it also stays, seeded `false`, because seeding the key is
what makes it env-resolvable — the reason it sits in `EnvBoundKeys`.

## Consequences

**Flipping the default orphans containers again, in mirror image.** The
setting is part of the container identity, so the same workspace resolves to a
different container name across the flip, exactly as ADR 0003 recorded for the
flip in the other direction. The blast radius is bounded by `AutoRemove: true`
on session containers: a shell that is not running at the moment of the change
has already been removed and costs nothing. Exit the open shells before
upgrading and the rename is invisible.

**A machine that already ran the old default keeps its anchor and volume until
swept.** The first shell after the flip no longer calls `ensureAnchor`, so
nothing removes what the old default left running. `toolbox stop --all` sweeps
the anchor; `docker volume rm toolbox-cc-socks` removes the volume, safely
while no participating shell is running.

**The stale-anchor self-heal now runs only for opted-in shells.** An anchor
whose PID 1 predates the reaping init is replaced by `ensureAnchor`, which is
reached only on the opt-in path — so a user who opts back in after a long gap
is the one who meets a stale anchor, and the replacement (still gated on
`anchorHeld`) is still there to handle it.

**Sessions inside the same container are unaffected.** They could always
message each other; that never depended on this setting.

## Verification

The default itself is a unit-test question and is pinned in `internal/config`.
The part that a flipped default can silently break is coverage of the opt-in
path: the real-daemon peer gate (`-tags dockergate`, CI only — the test's
temporary `HOME` is invisible to the host daemon under DooD) must request the
opt-in explicitly rather than inherit it from the shipped default, or the
change would quietly retire the one test that proves the messaging path works.
