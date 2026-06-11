# Host CLI internals

Maintainer notes on the host-side Go CLI's shared building blocks. Pipeline seams (config plan, mount plan, session plan, tool catalog) are documented in the package code itself.

## Shared fs primitives

Three host-filesystem primitives were copy-pasted across packages as the CLI grew: home-directory resolution (`os.UserHomeDir` + the literal `"resolve home directory: %w"` in six packages — only `configio` guarded the empty-`$HOME` case, the rest would silently `filepath.Join("", …)`), tilde expansion (`mountplan.expandHome`, re-inlined in `inherit_host_auth.go`), and crash-safe atomic writes (`configio.AtomicWriteFile`, not reused by `bridge/token.go`'s bare `os.WriteFile`).

`internal/fsx` collapses them into one stdlib-only leaf package (no import-cycle risk, so every package can depend on it):

- `fsx.Home()` — strict resolution, **with** the empty-`$HOME` guard. Adopting it at the five sites that lacked the guard is strictly safer: they already hard-failed on a `UserHomeDir` error and now also fail loud on an empty `$HOME` instead of joining onto `""`.
- `fsx.ExpandTilde(p, home)` — moved verbatim from `mountplan.expandHome`; `resolve.go` and `inherit_host_auth.go` both call it.
- `fsx.AtomicWriteFile(dest, data, mode)` — implementation moved from `configio`; every bridge state write reuses it (`token.go`, `port.go`, `daemon.go`'s pid file, `agent.go`'s service files), so a crash mid-write never leaves a torn token/port/plist behind.

`configio.GlobalConfigDir` / `configio.AtomicWriteFile` are kept as thin facades over `fsx` so `cmd/*` keeps a single config-IO import surface and existing callers/tests are untouched — the implementation lives once, in `fsx`.

Deliberately **not** routed through `fsx.Home`: the best-effort `home, _ := os.UserHomeDir()` sites (`config/plan.go` global-config read, `mountplan.Merge`'s pre-stat, `cmd/shell_named.go`) that must tolerate an empty home rather than hard-fail. `fsx`'s package doc reserves these for direct `os.UserHomeDir` use; routing them through the loud `Home()` would invert their contract. Likewise `config.ValidateMountsRoot`'s `~`/`~/` checks are *validation* (classifying a string), not expansion, so they do not call `ExpandTilde`.

The linux/darwin bridge service supervisors share their template-render and mkdir-then-write skeletons via `renderTemplate` / `writeServiceFile` in the non-tagged `bridge/agent.go`; the platform files keep only the genuinely divergent content (systemd unit vs launchd plist, `systemctl` vs `launchctl`).
