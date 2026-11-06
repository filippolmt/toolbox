# The image runs on Debian trixie

Status: accepted

Every observation below is **as of the decision** (6 November 2026). It is
evidence for the choice, not a description of the repo today.

The question comes from
[#1383](https://github.com/filippolmt/toolbox/issues/1383).
[ADR 0018](0018-worktrees-belong-to-worktrunk-not-toolbox.md) bundled
Worktrunk (`wt`), but the image's final stage was `node:24-bookworm-slim`, and
bookworm's git (2.39) is below the floor Worktrunk enforces (2.43). Worktrunk
refuses every git-backed command under that floor and offers no flag or
variable to bypass it. Upstream describes the floor as a policy cutoff, not
a feature boundary: it is what Ubuntu 24.04 ships. So the bundled `wt` never
worked. The smoke test missed this because it ran `wt --version` and the
shell init, and neither one reaches the gate. bookworm-backports has no
newer git. trixie ships 2.47.

The decision:

**Every Debian stage moves to trixie**: `node-base` (and with it the final
stage and every npm stage), `fetch-base`, and `fetch-oci`. Nothing ever
recorded why the image was on bookworm. It was the default when the image was
written, and bookworm is now oldstable.

**`fetch-oci` moves together with `node-base`.** The OCI venv's `python3` is a
symlink to the system interpreter, so the two stages must be on the same
Debian release. The final stage's `libpython3.*` packages follow that release's
minor version.

**`fetch-azure` installs the bookworm build of azure-cli on trixie.**
Microsoft's azure-cli apt repository has no trixie suite. Every Depends of the
bookworm deb resolves on trixie (`libssl3` through `libssl3t64`'s Provides),
and `/opt/az` carries its own interpreter, so the stage keeps the `bookworm`
suite literal on a trixie base. It switches when Microsoft publishes a trixie
suite.

## Considered options

**Build git from source in a fetch stage.** Rejected: it needs a compile
stage, which is slow under qemu on arm64. The repo would then own git's
security updates instead of Debian. And it leaves the image on a base that
keeps aging, so the next tool with a floor hits the same wall.

**Put a newer git earlier on `PATH` for `wt` only.** Rejected: there would be
two gits in one image, and which one a given tool picks up would depend on
`PATH` order. Nobody verified that Worktrunk resolves git through `PATH`.

**Pin Worktrunk below the release that introduced the floor.** Rejected: it
is six minor releases back, and Renovate would have to hold that line
indefinitely.

**Keep `fetch-base` on bookworm** to avoid mixing suites in `fetch-azure`.
Rejected: the fetch stages install only download helpers, so their suite is
invisible in the final image. The one exception is the azure deb, and that
works on trixie.

## Consequences

**The playwright dependency list uses trixie's names.** Playwright asks for the
same set on debian13 as on debian12. Only the `t64` renames differ
(`libasound2t64`, `libatk-bridge2.0-0t64`, `libcups2t64` among the listed
ones). `playwright deps` in `smoke-test.sh` still guards the set.

**A tool's floor on a system component is now smoke-tested by use, not by
version.** `worktrunk git floor` runs `wt list` in a scratch repository. An
audit of the other catalog tools for floors of this kind is
[#1384](https://github.com/filippolmt/toolbox/issues/1384).

**The `http.version=HTTP/1.1` pin stays.** Whether trixie's git still needs
it was not checked: that is out of scope here, and
`TestGitHTTPVersionPinned` keeps the pin in place.
