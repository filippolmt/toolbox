# Host CLI internals

Maintainer notes on the host-side Go CLI's shared building blocks. Pipeline seams (config plan, mount plan, session plan, tool catalog) are documented in the package code itself.

## Shared fs primitives

Three host-filesystem primitives were copy-pasted across packages as the CLI grew: home-directory resolution (`os.UserHomeDir` + the literal `"resolve home directory: %w"` in six packages — only `configio` guarded the empty-`$HOME` case, the rest would silently `filepath.Join("", …)`), tilde expansion (`mountplan.expandHome`, re-inlined in `inherit_host_auth.go`), and crash-safe atomic writes (`configio.AtomicWriteFile`, not reused by `bridge/token.go`'s bare `os.WriteFile`).

`internal/fsx` collapses them into one stdlib-only leaf package (no import-cycle risk, so every package can depend on it):

- `fsx.Home()` — strict resolution, **with** the empty-`$HOME` guard. Adopting it at the five sites that lacked the guard is strictly safer: they already hard-failed on a `UserHomeDir` error and now also fail loud on an empty `$HOME` instead of joining onto `""`.
- `fsx.ExpandTilde(p, home)` — moved verbatim from `mountplan.expandHome`; `resolve.go` and `inherit_host_auth.go` both call it.
- `fsx.AtomicWriteFile(dest, data, mode)` — implementation moved from `configio`; every bridge state write reuses it (`token.go`, `port.go`, `daemon.go`'s pid file, `agent.go`'s service files), so a crash mid-write never leaves a torn token/port/plist behind.

Deliberately **not** routed through `fsx.Home`: the best-effort `home, _ := os.UserHomeDir()` sites that must tolerate an empty home rather than hard-fail — the `config/plan.go` global-config read, `cmd/shells.go`, and `cmd/shell_named.go`'s `defaultShellPath`, which degrades to `/tmp/<name>` when the home is empty. The qualifier is the site, not the file: the config write behind that same degradation (`upsertShellInUserConfig`) resolves strictly through `fsx.Home` instead — its own doc comment says why. `fsx`'s package doc reserves these for direct `os.UserHomeDir` use; routing them through the loud `Home()` would invert their contract. Likewise `config.ValidateMountsRoot`'s `~`/`~/` checks are *validation* (classifying a string), not expansion, so they do not call `ExpandTilde`.

## The host as a declared input

What `fsx.Host` is and why it exists: the **Declared Host** entry in [`CONTEXT.md`](../../CONTEXT.md). What follows is only the call-site mechanics.

`fsx.CurrentHost()` is the one place the ambient read still happens — call it at the `cmd` edge and pass the value down. Where a command must have a home (`shell`, `worktree`, every `bridge` subcommand) it surfaces the error; the read-only surfaces that used to degrade on the discarded `os.UserHomeDir()` error still do, through `cmd.hostBestEffort()`.

Which entry points validate, and which tolerate:

| Takes a `Host` and calls `Validate` | Takes one and tolerates an empty `Home` |
|---|---|
| `mountplan.Plan` (after `Merge`, so a rejected mount list still reports first), `StateDirPath`, `OverlayDockerfilePath` | `mountplan.Merge` — only `inherit_host_auth`'s pre-stat and `proximo.CAMount`'s `~/.proximo` fallback read the home, and both degrade rather than fail |
| `bridge.ResolveHostState`, `NewAgent`; `bridge.proximoAgentHomeEnv` on the branch where the caller sent no agent home of its own | `proximo.CAPath` / `Enabled` / `Env`, `bridge.proximoFallbackCandidates` / `proximoChildPathDirs` — a probe for an optional tool |

`Host.Look` is the PATH half, and a `Host` with no resolver has an **empty** PATH rather than the process's — see the type's doc comment for why the convenience fallback would have undone the change. The two callers that ask about binaries are `proximo.CAPath` (queries `proximo config ca-path` before the `~/.proximo` fallback) and `bridge.resolveProximoBinary`. `bridge/credhelper.go` and `bridge/sound_linux.go` already injected `exec.LookPath` into a pure core and keep doing so; `bridge/editor_{linux,darwin}.go` stay on the direct call — the lookup *is* the whole one-line function, and reaching them would mean plumbing a `Host` through `handlerFns` for an existence check.

The linux/darwin bridge service supervisors share their template-render and mkdir-then-write skeletons via `renderTemplate` / `writeServiceFile` in the non-tagged `bridge/agent.go`; the platform files keep only the genuinely divergent content (systemd unit vs launchd plist, `systemctl` vs `launchctl`).
