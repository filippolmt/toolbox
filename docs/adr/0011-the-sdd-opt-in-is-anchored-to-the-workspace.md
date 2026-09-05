# The SDD opt-in is anchored to the workspace, and only the project layer may write it

Status: accepted

`toolbox config ui` presents a uniform Config Scope axis: every key can be
edited in the Global layer or the Repo layer, and the tab toggles between them.
That uniformity was never chosen key by key — it fell out of the axis being
applied to the whole Config Schema at once. For `sdd` it produces a write with
no coherent meaning, and the code had already said so everywhere except there.

Every other statement about an SDD opt-in is anchored to the workspace: the
install sentinel is keyed by the workspace hash, the artefacts materialise
under `/workspace`, the `toolbox sdd` command calls itself repo-local, and the
`.gitignore` fence — which the enable writes alongside the flag — has no global
expression at all. A `sdd.gsd: true` in `~/.toolbox.yaml` therefore enabled a
skill for every repository on the machine while fencing exactly one: whichever
directory the editor happened to be open in. That asymmetry is the defect, and
its cure is to stop the editor writing the key there.

So `configedit` gains one predicate — which Config Schema keys may only be
written to the workspace layer — and the config editor asks it before opening
the structured editor for a key. In the Global scope the `sdd` row still
displays and `enter` refuses with a status naming the reason and the tab that
gets you out. The CLI, in the same change, stops joining its own path from the
current directory and resolves the project config the same way the editor does:
the walked-up `.toolbox.yaml`, created in the current directory only when the
repo has none. The fence stays in the current directory in both paths.

The two can therefore land in different directories, and that is deliberate:
the flag governs the repo, the fence governs the directory you actually open.
The fence cannot move to the repo root, because `Skill.GitignoreEntries` are
anchored globs — `.claude/get-shit-done/`, `.codex/agents/gsd-*` — that a root
`.gitignore` would not match for a subdirectory checkout, and because the
workspace the container mounts is the current directory, not the git root.

## Rejected alternatives

**Hard-error on a global `sdd` at load time.** The obvious symmetric move: if
the editor may not write it, the loader should not accept it. Runtime
validation is layer-blind — `applyValidationTail` runs on the already-merged
config and only `configedit`'s provenance diff knows which file a value came
from — so this needs a layer-aware validation seam that does not exist, built
to reject configurations people wrote deliberately and that work today. The
grammar of the file is not the write surface, and this change is only about the
write surface.

**Ignore a global `sdd` with a warning.** Cheaper than erroring and worse than
both: a skill the developer has been using silently stops being active, on a
config they never edited. That is the exact loss class this decision exists to
close, reintroduced from the other side.

Neither is needed to close the defect. Once the editor cannot write `sdd` to
the global file, the only route to a global flag is a hand edit — and a hand
edit never wrote a fence for any repo. The "one privileged repo" asymmetry
disappears on its own, leaving the uniform and documentable behaviour: a
hand-written global flag enables the skill everywhere and fences nothing
anywhere.

**Hide the `sdd` row in the Global scope.** It would make a legal
hand-written global flag invisible in the one tool meant to show you your
configuration, and the `$EDITOR` escape — deliberately left open on that row —
is then the only way to see and clean it.

**Auto-switch the scope when `sdd` is edited from Global.** Writing to a file
other than the selected one is precisely the surprise class this issue is
about, not its cure.

**Make both write paths use the current directory.** The other way to make the
CLI and the editor agree. Rejected because the flag is not an SDD artefact: it
is a Config Schema key in a file that also carries `image`, `mounts` and
`shells`, and the config loader reads exactly one project file — the nearest
one walking up. Creating a `subdir/.toolbox.yaml` to enable a skill would
shadow the repo's entire configuration for every shell started there, silently.
The file decides its own scope; a key does not get to decide it.

**Write the rule down instead of enforcing it.** The claim that the CLI and
the editor leave identical file state was already written in two places while
being false — in the `configedit` package doc and in the repo's own SDD rule —
precisely because nothing checked it.

## Consequences

**The predicate is an authority with three readers, and must stay one.** The
editor's refusal, the reset's fence reconciliation, and any future CLI
`--where global` all ask `configedit`. Restating "which keys are
workspace-only" at any of those sites reproduces the fan-out that produced this
defect. It lives next to `Where` rather than in the config UI's Key Descriptor
row for the same reason the descriptor doc already gives for provenance and
deprecated aliases: the row carries presentation facts, and which layer may
write a key is a writing rule.

**Reset is deliberately asymmetric with enable.** `r` on the `sdd` row removes
the flag in either scope — clearing a hand-written global flag is how you get
rid of one — but reconciles `.gitignore` fences only in the Repo scope. Doing
it from the Global scope would remove the current directory's fences on behalf
of a file that applies everywhere: the same defect, reversed. The reconcile
runs against what the layers resolve to *after* the removal, not against the
empty set: reset clears one layer, and a skill a kept flag in the other still
enables still needs its fence.

**The invariant is a test, not prose.** `cmd.TestCLIAndUISDDWritesAreIdentical`
runs both real entry points against the same fixture and compares the bytes,
with the current directory at the project root and inside a subdirectory. It
deliberately does not enter through the shared `configedit` seam, which would
only prove that one function is deterministic.

**Which layer may write a key and which files that key owns are two facts, not
one.** The predicate answers only the first. The reset path asks it for the
scope asymmetry above, then dispatches the `.gitignore` reconciliation on the
key itself — so a second member of the set does nothing until it is given
artefact handling of its own, rather than silently inheriting SDD's and
reconciling the wrong files.
