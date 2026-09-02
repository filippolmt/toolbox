# The daemon's refusal is the only in-use check for image reclamation

Status: accepted

Every merge ships a runtime image, so a developer who keeps up accumulates a
local store of images that lost the `latest` tag and nothing else — see
[Superseded Image](../../CONTEXT.md#superseded-image). Reclaiming them is
uncontroversial. What is not is how to establish that one is safe to remove,
because a toolbox container outlives its session: containers are named per
`(workspace, profile)` and reconnected later, so an image with no tag and no
attached shell may still be the image a stopped container of a different
workspace expects to find when its developer comes back tomorrow.

The obvious defence is a census: list the containers, collect the images they
reference, subtract, remove the rest. We rejected it. The census cannot
actually establish what it appears to establish, because between our
`ContainerList` and our `ImageRemove` another `toolbox shell` on the same
machine can create a container from an image the census had just cleared —
the check is time-of-check-to-time-of-use and remains so however carefully it
is written. It buys a list, a comparison and their tests, and closes none of
the window it exists to close.

So [Image Reclamation](../../CONTEXT.md#image-reclamation) performs no
in-use check of its own. It calls `ImageRemove` with neither `force` nor
`PruneChildren` and treats the resulting error as the answer to the question
it did not ask. The daemon already refuses to remove an image any container
references — running or exited — and it refuses atomically, on the far side
of the race a census sits on the wrong side of. Deleting our own check does
not weaken the guarantee; it replaces an advisory one with the authoritative
one.

The absent flags are the load-bearing part of this decision, and both are
easy to add back by someone who reads the call as merely timid. `force`
converts every refusal above into a silent removal of an image a colleague's
stopped session is waiting for, which is the one outcome this whole act must
never produce. `PruneChildren` reaches past the candidate into the images
built on top of it — which on this project is exactly the `:local` overlay a
developer's own `~/.toolbox/Dockerfile` produced, unreproducible from the
registry. Neither flag has a use here that is worth its failure mode.

## Consequences

Two, both uncomfortable, and neither visible at the call site:

**A stopped container pins its image indefinitely.** A developer with many
workspaces will reclaim little or nothing until those containers are removed,
because each one is a legitimate reference and the daemon says so. This is
the correct behaviour and it will be reported as a bug.

**An overlay pins its base the same way.** `:local` is built `FROM` the base
image's ID, so the base has a child and the daemon refuses to remove it.
Anyone using an overlay Dockerfile reclaims nothing until they rebuild.
`PruneChildren` would "fix" this by destroying the overlay, which is why it
is not set.

Both are consequences of the contract rather than gaps in it. The remedy, if
one is ever wanted, is an explicit `toolbox prune` the developer types with
the consequences in front of them — not a flag flipped inside a sweep that
runs unattended at every shell start.

## Verification

The argument above rests on a claim about Docker's behaviour, not about our
code: that the daemon refuses to remove an image referenced by a container
that is merely stopped. A fake client cannot testify to it — it would only
confirm that the fake does what we told it. The claim is therefore pinned by
a real-daemon gate (`-tags dockergate`, CI-only like the other gates) that
creates a container, stops it, and asserts that an unforced `ImageRemove`
fails. Without that test this document asserts something nobody has checked.
