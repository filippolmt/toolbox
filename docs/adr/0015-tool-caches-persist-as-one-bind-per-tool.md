# Tool caches persist as one bind per tool, not as `~/.cache` wholesale and not as a volume

Status: accepted

Every figure below is **as measured when this decision was taken** — evidence for the choice, not a description of the repo today. Nothing here is kept in sync; current values live in the files that set them.

The [Mount Plan](../../CONTEXT.md#mount-plan) defaults persist a tool's data
directory and say nothing about its cache. That reads as a deliberate line until
you notice which tools survive a container recreate and which do not: bun, pnpm
and Go's module cache keep theirs, because those tools happen to place the cache
*inside* the data directory the defaults already mount. Go's build cache, uv's
download cache, npm's `_cacache` and golangci-lint's cache do not, because those
tools put them where XDG says to. Nothing under `XDG_CACHE_HOME` is mounted at
all — the one apparent exception, the `playwright-cache` default, is a data
directory that Playwright happens to file under `~/.cache`. Session containers
are created with `AutoRemove`, so what is not mounted is discarded on every
recreate.

The rule the defaults actually encode, then, is *"a tool keeps what it stores
next to its data"* — an accident of each tool's layout, not a decision. A
developer who compiles Go in a shell pays for it on every container recreate by
rebuilding from zero, and pays again in downloads for every package manager that
respects XDG. That is the gap this ADR closes, and the concept it needs is
[Tool Cache](../../CONTEXT.md#tool-cache).

So: five new default binds, one per tool, each named and each disablable —
`go-build`, `golangci-cache`, `uv-cache`, `npm-cache`, `pip-cache`. They ship
enabled.
The three decisions worth recording are the shape, the default, and the
alternative that the measurement favoured and did not win.

**One bind per tool, not one bind for `~/.cache`.** The wholesale mount is one
line and catches every tool present and future, which is exactly the objection:
`~/.cache` is by contract the directory a program may write into on the
understanding that anything there can be deleted, so mounting it persists
whatever any future tool decides to put there, with no per-name control and no
decision taken. This repo chose the explicit form everywhere else it had the
same choice — a catalog row per bundled CLI, a whitelist per host-auth mount, a
name per default bind — and the cost of the explicit form here is five rows and
one more row per tool added later. That cost is the mechanism by which the
choice stays a choice.

**Enabled by default, not opt-in.** [ADR 0013](0013-peer-messaging-ships-off-by-default.md)
is the nearby precedent for the opposite call, and it does not transfer: peer
messaging ships off because it opens a channel between containers and so changes
the security surface a developer did not ask to change. These five cost disk on
the host and nothing else, they benefit anyone who compiles or installs
anything, and each is switched off by name with `toolbox mounts disable
go-build`. A default that is on and disablable reaches the developers who would
never find the opt-in; the per-name disable is what makes that safe, and it is
available only because the shape is a bind.

**A named volume would be faster, and is refused anyway.** Measured inside a
session container on macOS with Docker Desktop on 10 September 2026, over 2000
small files, the container's own overlay against a virtiofs bind: create 56 ms
against 322 ms, read 14 ms against 172 ms, delete 12 ms against 209 ms;
sequential throughput 2.0 GB/s against 1.5 GB/s and a metadata walk equal. Go's
build cache is precisely the small-file write workload in that table, so the
volume is the faster home for it, and the type already supports one — a
`mountplan.Bind` whose `Source` is not an absolute path is read by Docker as a
volume name, with `peerSocketBind` as the precedent and `peer_volume.go` as the
ownership machinery. It loses on control. No config surface can express a
volume: `config.Mount` carries no type field and every `mounts:` entry goes
through `resolveAll`, which stats and creates a host path, which is why the one
volume in the CLI is appended after the resolve and is explicitly out of reach
of `mounts:` patches. A volume here would be a default that no developer could
disable, retarget through `mounts_root`, or inspect from the host — and it is
the caches, of all things, that a developer is most likely to want to move,
share between profiles, or throw away by hand.

## Considered options

**Mount `~/.cache` wholesale, or as a volume.** Covered above: the first
persists by accident what should be persisted by decision; the second buys
speed by removing the developer's control over the mount.

**Unify the shell's Go store with the Makefile's `toolbox-gomod` volume.** A
shell uses `~/go` through a bind while every `make go-*` target uses the volume,
so the module cache exists twice. Measured on the same day: the volume held
2.0 GB against 12 GB in the bind — the bind carries every project on the
machine, the volume only this repo, so the overlap is the smaller number.
Rejected: roughly two gigabytes is not worth a shared module cache written by
the `golang` image as root and by a session as the host UID, and if the volume
ever grows enough to matter the answer is to prune it, not to fuse it.

**Ship `golangci-cache` for a tool the image does not bundle, or leave it
out.** golangci-lint has no catalog row and no Dockerfile layer, and `make go-lint` runs it in a
separate image that never sees a session's binds — so, like `pip-cache`, this
row is inert until a session installs the linter itself with `go install`, at
which point the binary lands in the `go` bind and is on PATH. Kept because that
install is the normal way a Go session gets a linter here, and because the row
costs nothing while unused; named with the `-cache` suffix so it does not read
as golangci-lint's data directory.

**Give pip's cache the same treatment as the rest, or leave it out.** The
image's Python is marked `EXTERNALLY-MANAGED`, so PEP 668 confines pip to
virtual environments and uv is the tool a session reaches for. The row ships at
the developer's explicit request; it earns its keep only for venv work, and
that is the reason it might look unmotivated later.

## Consequences

**The Makefile had the same bug and is fixed alongside, as its own commit.**
`GO_MOUNT` mounted the volume at the `golang` image's `GOPATH` while
`GO_BUILD_ENV` set only `GOFLAGS`, so `GOCACHE` fell back to a path outside the
volume in a container that runs `--rm`: the module half of the volume's comment
was true and the build half was not, and every `make go-*` invocation compiled
from zero. A `GO_CACHE_ENV` fragment now points both `GOCACHE` and
`GOLANGCI_LINT_CACHE` inside the volume, and — unlike `GO_BUILD_ENV`, which the
linter must not inherit because `GOFLAGS=-mod=mod` would let a lint run rewrite
`go.mod` — it is passed to `go-lint` as well, which otherwise re-analysed from
zero on every run. `go-clean-cache` discards all of it, consistent with its
name. This touches the host-side Makefile and not the Mount Plan, so neither
half depends on the other; it rides along because it is the same defect.

**`go-build` is the row to promote if the virtiofs cost proves worse than
rebuilding.** The measurement says the build cache is where a bind hurts most,
and the shape chosen here makes that reversible at the granularity of one
default: `go-build` can become a volume without the other four moving, at the
price of the control described above, and that trade is then made for one cache
rather than for all of them.

**`~/.toolbox` grows on the host, unbounded and unpruned.** Nothing expires
these caches; each tool's own `clean` command is the only pruning, and a
developer who wants the disk back reaches for that or disables the mount. This
is the deliberate half of the trade — the objective was fewer downloads, and a
cache that prunes itself would spend that objective to buy back disk that the
measurement did not show to be scarce.

**The new targets are siblings, not nested, and `parent_dirs` needs no
change.** `/home/toolbox/.cache` is a *parent* the image pre-creates, not a
mount: `playwright-cache` targets `.cache/ms-playwright` and the four XDG rows
land beside it, so there is no mounted parent and no parent-first ordering to
preserve — unlike the bridge run mount, which really does sit inside the
read-only bridge mount. `ParentDirs` was re-checked rather than assumed: the
four `.cache/*` rows fold into the `/home/toolbox/.cache` parent it already
emits, and `npm-cache`'s parent is `/home/toolbox`, which it excludes by
design. The Dockerfile cross-check test is unaffected.

**Only one test pins the default set by hand.** The count and the per-row
assertions in `internal/mountplan/defaults_test.go` are the single literal to
extend — the repo keeps no golden files. Everything else that looked like it
enumerated the defaults turns out to derive from `Defaults()` at run time: the
`len(Defaults())` arithmetic in the merge tests, the sorted name union, the
config-example renderer's coverage and the `mounts list` classification tests
all passed unchanged. That is a property worth keeping when the next row lands.
