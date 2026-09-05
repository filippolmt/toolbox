# Each module declares the Docker methods it calls; the edge keeps the full client

Status: accepted, amended below
Date: 2026-09-04

Figures in this document are measurements taken when the decision was made,
not claims about the tree as it stands.

Every module that talks to the daemon declares `client.APIClient` — the whole
Docker SDK surface, some ninety methods — and calls between one and five of
them. `imagereclaim.sweep` declares the entire daemon and calls `ImageList`
and `ImageRemove`. `imageplan.Ensure` declares it and calls `ImageInspect`.
Which calls a module makes, in what order, with which options, is therefore
carried in prose and nowhere in a signature, and the two named interfaces in
the whole of `internal/` outside tests are `bridge.Agent` and `worktree.Git`.

The visible cost is eleven hand-rolled test adapters, one per test package,
each a struct embedding a nil `client.APIClient` so that an unstubbed call
panics. `ImageInspect` is implemented five times, `ImagePull` four,
`ContainerInspect` and `ContainerStop` and `ContainerRemove` three each. The
field naming has already drifted between copies. `internal/dockertest` exists,
states the position out loud — the fakes differ in which methods they exercise
— and holds leaf helpers instead of a fake.

That embedding is not merely a shortcut. Two of the sharpest assertions in the
image family are assertions of *absence* — that `ImageRemove` takes neither
`force` nor `PruneChildren`, that the registry is asked nothing while the
attempt stamp is fresh — and both are expressed as a panic on a method nobody
stubbed. The convention works. It is also unnameable in the interface, which
is the whole complaint: this is a real seam, with a real client on one side and
eleven fakes on the other, declared at the wrong width.

So each leaf module declares the interface it actually calls, unexported, in
its own package, named for the role it fills rather than for Docker. An
exported function may take an unexported interface type: callers pass a value
that satisfies it and never need to name the type.

## What the edge does instead

`internal/container` does not narrow. It keeps `client.APIClient`.

It calls fourteen methods of its own, and it passes its client down to
`imageplan`, `imageprefetch`, `imagereclaim`, `localimage`, `teardown` and
`build`. Go assigns one interface to another only when the target's method set
is a subset of the source's, so a narrowed `container` would have to declare
the union of its own calls and every callee's — around twenty methods of
ninety. That buys little depth and costs a rule that must be re-checked every
time a call into a leaf is added, whose violation is a compile error in the
package that did not change. `internal/container` *is* the Docker edge; the
concrete client belongs there, and saying so in the type is honest rather than
lazy.

The leverage was never at the edge. It is in the leaves, at one to three
methods each.

## What was rejected

**A package of shared interfaces** — `internal/dockerapi` exporting
`ImageInspector`, `ImagePuller` and so on, imported by everyone. It
reintroduces the thing being removed: one hub, centrally maintained, that every
module depends on and no module owns. Its apparent advantage is that a shared
fake has something to satisfy by name, and that advantage is not real — a struct
with the right methods satisfies an unexported interface in another package
structurally, which is exactly how a shared fake will be used here.

**Narrowing everything in one change.** The image family — `imageplan`,
`imagepull`, `imageprefetch`, `imagereclaim`, `localimage` — has the best ratio
and is the same set a later consolidation of the image-sync policy will touch.
`teardown` at five methods and `worktree` at one carry no decisions of their
own; `internal/build` is separately owed a split, and folding that argument in
would settle two things at once badly.

**Narrowing without replacing the adapters.** A struct embedding
`client.APIClient` satisfies a one-method interface too, so narrowing alone
changes nothing observable and leaves `ImageInspect` faked five times. The
adapters of the packages in scope are replaced in the same work; the ones
outside it are not touched.

## The constraint that decided the slice

The subset rule bites one level down as well. `imageprefetch` reaches
`ImageInspect` through `imageref.LocalRepoDigest`, and `localimage` reaches
`ImageBuild` through `build.BuildOverlay`. Both are called with the caller's own
client, so either those two functions narrow in this same slice or the two
leaves are forced to keep the full client and gain nothing.

They narrow. This is dependency closure, not scope creep, and with it in place
no interface in the slice exceeds three methods.

## The fake

One struct in `internal/dockertest`, with one function field per method the
in-scope leaves call. A nil field panics with a message naming the method, so
the zero value refuses every call exactly as a nil embed does today and the
absence assertions survive with a better failure than a nil-pointer
dereference.

The struct must not embed `client.APIClient`. Embedding it to avoid writing a
method would satisfy every narrow interface by accident and undo, silently and
permanently, the only thing the narrowing buys in tests.

## Consequences

**One module's Docker surface stays illegible from its signature.** A reader of
`internal/container` still cannot tell from a signature which fourteen methods
it reaches, and its tests still need a fourteen-method fake. That is the
deliberate price of exempting the edge, and it will look like an oversight to
anyone who reads the leaves first.

**A new call between leaves can fail to compile somewhere surprising.** Adding a
call from one leaf into another may require widening the caller's interface, and
the error names the assignment rather than the change that caused it. This is
the standing cost of consumer-declared interfaces. It is also the argument that
exempted the edge, where the union would have been widest and the surprises
most frequent.

**`internal/build` becomes untidy before it becomes tidy.** Two of its functions
carry narrow interfaces while the rest of the package holds the full client,
because the subset rule forced them into this slice and the package's own split
is a separate decision. Anyone reading `build` in between will find two
conventions in one package.

**The duplication is only half removed.** `teardown` and `worktree` keep their
hand-rolled adapters, so `ContainerInspect` stays implemented three times until
the second slice lands. A partial answer is the point of slicing, but a reader
who greps for the pattern will find it still there and may conclude the decision
was not carried out.

## Amendment: the second slice landed in the same work

The slicing above held for the length of one review. Asked to finish the job,
the same change went on to narrow `teardown` and `build.BuildImage`, and the
answer for `worktree` turned out not to be the one this ADR assumed.

**`teardown` declares `containerRuntime`** — inspect, stop, remove, kill, and
the exec inspect behind the sibling-terminal question. Its adapter moved onto
the shared fake, which grew the five container-side fields. The move is not
only deduplication: the hand-rolled adapter answered an unstubbed stop, kill or
remove with success, so three of its five endpoints could be reached by a
teardown nobody had asked to reach them. Under the shared fake they panic, and
three tests gained the stub they had been silently borrowing.

**`build` no longer holds two conventions.** `BuildImage` needs `ImageBuild`
and nothing else, so it takes the same `imageBuilder` the overlay does. The
consequence recorded above — "`internal/build` becomes untidy before it becomes
tidy" — never had to be paid, and the package's own split is still a separate
decision.

**`worktree` cannot narrow, and its one method was a miscount.** It calls
`ImageInspect`… of a container: one method of its own. But it also hands its
client to `container.Stop`, whose parameter is `client.APIClient` — so the
subset rule makes `worktree`'s declared surface a superset of the edge's, and
narrowing it means narrowing the edge. Narrowing `Stop` alone would be cheap
(it is a one-line delegation to `teardown.StopOne`, so its need is exactly
`containerRuntime`), and it was rejected for the reason this ADR already gives
for exempting the edge: a daemon call added inside that tree would then have to
be widened in three packages, with the compile error landing in `worktree`,
which did not change.

So the standing consequence is narrower than the one recorded above. The two
packages still declaring `client.APIClient` — `internal/container` and
`internal/worktree` — are the two that cannot stop, and each keeps a
hand-rolled adapter because `dockertest.Fake` deliberately does not satisfy
`client.APIClient` and so cannot stand in where that is the parameter. What is
left is not a later slice. It is the shape of the edge.
