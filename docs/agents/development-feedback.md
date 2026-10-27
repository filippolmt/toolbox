# Development feedback loops

The Makefile owns toolchain/image pins and executable commands. These notes
cover the environment constraints that its target descriptions cannot show.

## Daemon gates

```bash
make go-dockergate
make go-dockergate PKG=./internal/container RUN='TestTerminalClose|TestShellProcessCleanup'
```

The target uses the existing runtime image selected by `FULL`; it does not
rebuild that image. When testing changed image assets, run `make build` first
and pass `FULL=toolbox-local:build`: a pull or prefetch can re-point the
registry tag before the gate runs, while the local tag stays on your build. CI still verifies the
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
source, throwaway harnesses and diagnostic artifacts there: Git ignores the
release directory, while Go package discovery ignores the hidden child.
Ignoring a directory in Git alone does not exclude it from `go test ./...`.
`make go-fmt` formats surviving regular Go files selected by Git, so ignored
scratch/cache trees, symlinks and unstaged deletions are left alone.

`make check-devtools` runs daemon-free tests of Make target wiring, the daemon
runner's mount/identity/safety arguments and the delta-coverage parser. It needs
Node, Python, Git, Make and shellcheck and also runs in CI. Use it after editing
the development targets or their scripts. A renamed target or script also
renames its citations: `TestCitedMakeTargetsAndScriptsExist` (`internal/devrules`)
fails on any `make <target>` or `.github/scripts/` path the docs still cite.

## GitHub monitoring

The image's [gh initializer](../../internal/build/assets/init.d/02-gh.sh) adds
compact run-monitoring guidance after refreshing the official gh skill in each
active agent root. The addition is idempotent and also applies to an existing
skill when the upstream install fails. Keep this guidance in the initializer,
not just in the container-local skill that the next shell refresh replaces.
