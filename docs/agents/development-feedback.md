# Development feedback loops

The Makefile owns toolchain/image pins and executable commands. These notes
cover the environment constraints that its target descriptions cannot show.

## Test runner

`make go-test`, `go-test-one`, `go-coverage` and `go-test-verbose` run in
`toolbox-gotest:local`, built from `.github/gotest.Dockerfile`: the pinned
`GO_IMAGE` plus the interpreters the shell-driven tests execute (`jq`, `zsh`),
which `ci.yml`'s runner also has. A test that executes a tool calls
`requireTools` (`internal/build`) or an inline `os.Getenv("CI")` check: it
skips locally when the tool is missing and fails under `$CI`, held by
`TestAMissingToolNeverSkipsUnderCI` (`internal/devrules`). A suspiciously fast
`ok` from a plain `go test` outside the runner is a skip, not a pass.

## Daemon gates

```bash
make go-dockergate
make go-dockergate PKG=./internal/container RUN='TestTerminalClose|TestShellProcessCleanup'
```

The target uses the existing runtime image selected by `FULL`; it does not
rebuild that image. When testing changed image assets, run `make build` first
and pass `FULL=` the tag it printed (`toolbox-local:build`, or
`toolbox-local:<worktree>` in a linked worktree): a pull or prefetch can
re-point the registry tag before the gate runs, while the local tag stays on
your build. A linked worktree's build leaves the registry tag alone and its
gate runner image is `toolbox-dockergate:<worktree>`, so builds in parallel
worktrees cannot overwrite each other or the image `./toolbox shell` runs. CI still verifies the
complete gate suite against the runtime image built from the PR.

The development-only runner uses `GO_IMAGE` from the Makefile, the runtime's
Docker client and an installed Python interpreter. It maps the caller's UID/GID
and the daemon socket's group. HOME and temporary fixtures live below
`dist/.scratch/`, mounted at their absolute **host** path inside the runner.
Sibling containers therefore see the same paths under DooD. The Go image's
GOPATH/GOCACHE overrides are cleared so temporary HOME and permission behavior
remain observable instead of being masked by root or an inherited cache path.

Use an unprivileged account and a local Linux Docker daemon or Docker Desktop.
Remote daemons cannot see these host paths. The runner refuses root and an
existing peer anchor: peer tests own that global resource and must not replace
another developer's active namespace. Use a dedicated daemon or close peer
sessions yourself before running the suite. The socket grants daemon access;
run these gates only against trusted repository code.

A failing run ends by restating its `--- FAIL` lines, or says none was printed
when the build or setup failed, and keeps its whole output in
`dist/.scratch/dockergate/last-run.log`. To tell whether a failure is yours,
run the same gate on `main` from a worktree under `dist/.scratch/` — inside
the workspace, so the runner can mount it — then remove it, which deletes that
run's log too:

```bash
git fetch origin
git worktree add dist/.scratch/main-check origin/main
make -C dist/.scratch/main-check go-dockergate PKG=./internal/container RUN=TestName
git worktree remove --force dist/.scratch/main-check
```

Both runs use the image `FULL` names, so this separates test and CLI changes
only. When your branch changes the image, pass the published `main` image as
`FULL` to the baseline run instead of your build.

For tagged tests that do not require a daemon or special mounts:

```bash
make go-test-one PKG=./internal/container RUN=TestExecShell TAGS=dockergate
```

`TAGS` is forwarded directly to the Go test command, rather than relying on an
outer `GOFLAGS` value that the container's build environment replaces.

## New-code preflight

```bash
make go-preflight BASE=origin/main
```

This runs the coverage-enforced suite, checks cognitive complexity in the diff,
and reports estimated coverage of changed executable Go lines. Use the recorded
starting SHA as `BASE` when reviewing a focused session rather than a whole PR.
The complexity check enables the existing linter's `gocognit` rule with the
threshold in `.golangci.yml`. Git's Go function context selects entire changed
functions, including their declarations, so a body-only edit is checked without
flagging untouched sibling functions. Test files follow SonarQube's exception.
The generated selection patch is input to the linter, not a patch to apply.

The coverage estimate reuses `GO_COVERAGE_FILE` and `COVERAGE_MIN` from the
Makefile. It includes committed, staged, working-tree and untracked Go source
changes relative to the merge-base, excludes test files, and intersects added
lines with the profile's executable blocks. Missing profile records are reported
as incomplete coverage, not silently treated as covered code.

Delta coverage is advisory: block-based Go line estimates and gocognit's
complexity calculation are not identical to SonarQube's new-code and analysis
rules. The server's Quality Gate remains authoritative. CI runs the same
complexity check and coverage report alongside the existing gates.

## Scratch and checks

`make scratch` creates `dist/.scratch/` and prints its host path. Put copied
source, throwaway harnesses and diagnostic artifacts there, and bind an ad-hoc
`docker run -v`/`--mount` from a toolbox shell only under that printed path:
the daemon resolves the source on the host, and a container path translated by
hand can create a stray host directory instead. An agent scratchpad and `/tmp`
live on the container's own filesystem: a mount from them comes up empty with
no error, so keep worktrees and fixtures under the workspace too. The mounting
Make targets refuse to run from the container's root filesystem, though not
from a tmpfs or named volume. Git ignores the release directory, while Go
package discovery ignores the hidden child.
Ignoring a directory in Git alone does not exclude it from `go test ./...`.
`make go-fmt` formats surviving regular Go files selected by Git, so ignored
scratch/cache trees, symlinks and unstaged deletions are left alone.

`make check-devtools` runs daemon-free tests of Make target wiring, the daemon
runner's mount/identity/safety arguments and the delta-coverage parser. It needs
Node, Python, Git, Make and shellcheck and also runs in CI. Use it after editing
the development targets or their scripts. A renamed target or script also
renames its citations: `TestCitedMakeTargetsAndScriptsExist` (`internal/devrules`)
fails on any `make <target>` or `.github/scripts/` path the docs still cite.

## Splitting a commit

Commit the behaviour change at green, before the refactor starts: that costs
one command. When the two already share a file, split it this way, within what
the [agent guardrails](../troubleshooting.md#an-agent-command-is-blocked-by-the-agent-guardrails)
allow:

1. Copy every modified file to `dist/.scratch/split/` and `cmp` each copy.
2. Reset only the shared files: `git checkout HEAD -- <path>`.
3. Re-apply the behaviour hunks, `git add <path>`, commit.
4. To test that commit alone, set the rest aside by path,
   `git stash push -m <name> -- <path>…`, run the tests, then restore it by
   the entry `git stash list` shows: `git stash pop stash@{n}`.
5. Copy the saved files back, `cmp` them against the copies, commit the
   refactor.

## GitHub monitoring

The image bakes the official gh skill in the `fetch-gh` stage of the
[Dockerfile](../../internal/build/assets/Dockerfile) and appends compact
run-monitoring guidance there, so every agent root gets it through the
[Global Skill](../../CONTEXT.md#global-skill) sync. Change the guidance in that
stage: a hand edit to an installed copy is replaced at the next start.
