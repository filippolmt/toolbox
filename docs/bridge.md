# Bridge

A per-user host daemon that forwards in-container URL opens (`xdg-open`, `$BROWSER`, OAuth redirects) to the host's real browser, editor opens (`code`/`codium`) to the host's VS Code / VSCodium, and `proximo up|down|status` to the host proximo binary. Opt-in: nothing runs on the host until `toolbox bridge install`.

## Quick start

```bash
toolbox bridge install     # generate token, write LaunchAgent/systemd unit, start daemon
toolbox bridge status      # show install state, port, daemon liveness
toolbox bridge uninstall   # stop daemon, remove unit + token
```

The `bridge:` config key (default on) controls whether the container gets the bridge mounts at all — see [mount gating](#mount-gating).

## Architecture

The container has no display server. CLIs inside `toolbox shell` that invoke `xdg-open <url>`, set `$BROWSER`, or expect an OAuth redirect to land somewhere clickable have no fallback by default. The bridge plumbs URL opens out to the host's real browser; the same channel carries editor opens (`code .`, `codium <file>`) to the host's VS Code / VSCodium:

```
container                                          host
─────────                                          ────
xdg-open <url>                                     toolbox bridge daemon
code/codium <path>                                   │ listens on 127.0.0.1:<port>
  └─ wrappers at tail of Dockerfile                  │ + (Linux) unix run/bridge.sock
       │  read /home/toolbox/.toolbox/bridge/       │ (port + token read from
       │  {port,token} (RO bind-mount)               │  ~/.toolbox/toolbox/bridge/)
       ├─ POST /open  body: { "url": "..." }         ├─ open / xdg-open <url>
       └─ POST /edit  body: { "editor": …, "path": … }
                                                     └─ code / codium <path>
              (both: Authorization: Bearer <token>)
```

### Transport

`bridge_post` in `bridge-lib.sh` tries the daemon's unix socket first (`/home/toolbox/.toolbox/bridge/run/bridge.sock`, present when the host daemon is a Linux build) and falls back to TCP `http://host.docker.internal:<port>` when the socket is absent or the connect dies (curl status `000`). The fallback fires **only** on `000`: a real HTTP status (even 4xx/5xx) is never retried, or a flaky socket would double-exec `/proximo` on the host.

Why two transports: the TCP listener binds `127.0.0.1` only. On macOS that works — Docker Desktop's vpnkit proxies `host.docker.internal` connections from the host loopback. On native Linux (docker-ce) `host.docker.internal:host-gateway` resolves to the docker0 gateway IP (e.g. `172.17.0.1`), where nothing listens — connection refused, bridge unreachable. The unix socket (bound by `bindUnixListener` in `internal/bridge/socket_linux.go`, GOOS-gated like the agents) sidesteps routing entirely: the container reaches it through the `bridge-run` RW bind mount. Docker Desktop cannot share host unix sockets with containers (`docker.sock` is special-cased), so macOS has no unix listener and Docker Desktop on Linux falls through `000` to TCP, which works there via vpnkit.

Version-skew matrix:

| Host CLI | Image | Runtime | Result |
|---|---|---|---|
| new | new | docker-ce Linux | unix socket (the fix) |
| new | pre-socket | docker-ce Linux | TCP-only shim → still unreachable (as before the fix; pull the image) |
| pre-socket | new | any | no `run/` mount → `-S` false → TCP, identical to before |
| new | new | Docker Desktop (macOS or Linux) | socket absent/untraversable → `000` → TCP via vpnkit → works |

The container-side wrappers are installed at the tail of `internal/build/assets/Dockerfile` (after the `COPY init.d/` step): `xdg-open` shadows `/usr/local/bin/xdg-open` and its synonyms (`sensible-browser`, `gnome-open`, `x-www-browser`, `www-browser`, `open`); `code` ships with `codium` as a symlink (editor inferred from `basename $0`). `ENV BROWSER=xdg-open` lets tools that honour `$BROWSER` route through the same wrapper.

### State directory

State lives in `~/.toolbox/toolbox/bridge/` (`HostDir` in `internal/bridge/paths.go`), mounted **read-only** into the container at `/home/toolbox/.toolbox/bridge` (`ContainerDir`) — except the `run/` subdir, which carries the unix socket and gets its own **RW** nested mount (`bridge-run`; `connect()` on a socket inside a RO mount fails with EROFS). Four files plus the socket dir: `token` (bearer secret, generated at install), `port` (chosen at daemon start, written even when the socket transport is active — the TCP fallback and `bridge_ready` need it), `pid`, `log`, `run/bridge.sock`. The dir is mode `0700`; the bind keeps the container from rotating either secret. `~/.toolbox/toolbox/` is the toolbox-own namespace (state lives next to `state/`, home of the pull cache): the `~/.toolbox` root is reserved for per-app config/credential dirs, so a future app can never collide with toolbox-own state. The bridge dir is deliberately a sibling of `state/`, not inside it — `state/` is rw-mounted wholesale into the container and the token must stay read-only. `toolbox shell` migrates a pre-namespace `~/.toolbox/state` onto `~/.toolbox/toolbox/state` once, best-effort (`mountplan.MigrateLegacyToolboxState`).

## Install topology

Per-host supervisor differs by platform — implementation in `internal/bridge/agent_darwin.go` and `agent_linux.go`:

| Host | Unit | Registered via |
|------|------|----------------|
| macOS | `~/Library/LaunchAgents/com.filippolmt.toolbox.bridge.plist` | `launchctl bootstrap gui/<uid>` |
| Linux | `~/.config/systemd/user/toolbox-bridge.service` | `systemctl --user daemon-reload && enable --now` |

Both run the same hidden subcommand `toolbox bridge daemon` in the foreground; the supervisor handles restart-on-crash and login-time start. `toolbox bridge install` writes the unit + token then triggers the bootstrap; `uninstall` reverses both steps. Install/uninstall also retire the pre-rename `browser-bridge` era artifacts: the `com.filippolmt.toolbox.browser` LaunchAgent / `toolbox-browser.service` unit is stopped and removed, and `install` renames a `~/.toolbox/browser` state dir onto the new location (token preserved, so running containers keep authenticating). Stale leftovers go too: the old macOS log file (`toolbox-browser.log`) and any legacy dir recreated by an old binary's CreateIfMissing mount are deleted on the next install / shell start. The `browser-bridge` CLI spelling survives as a deprecated cobra alias — installed units keep invoking it until the user reruns `toolbox bridge install`; drop it only in a major release. Anything else (status reads, log inspection) is read-only on the state dir.

## Security boundary

The daemon refuses anything that isn't:

1. Bound to `127.0.0.1` (no LAN exposure — checked at listen time); the Linux unix socket is `0600` inside a `0700` dir owned by the same UID the container runs as (`--user`), so it adds no principal beyond the user themselves. The bearer token is required on both transports.
2. Authenticated with the exact bearer token from `~/.toolbox/toolbox/bridge/token` (constant-time compare).
3. `/open`: scheme `http` or `https` only — `file://`, `javascript:`, `data:` etc. are rejected with 400. `/edit`: editor in the fixed allowlist (`code`, `codium` — a client-supplied name never reaches exec) and an absolute, existing host path after `filepath.Clean`, otherwise 400.
4. Below the URL length cap.
5. Within the rate limit (one bucket shared by `/open` and `/edit`).

The container side can read `token` because the mount is RO; an attacker who lands shell-equivalent privileges inside the container can therefore *open URLs* on the host browser and *open existing host paths* in an allowlisted editor (non-executing: the editor renders file contents), but cannot exfiltrate the token to a different network namespace (the daemon only accepts `127.0.0.1`, and the container's `127.0.0.1` is a different namespace) and cannot make the daemon exec an arbitrary binary (fixed allowlist, direct exec, no shell).

## Editor shims

`/usr/local/bin/code` (+ `codium` symlink) is a bridge shim, not a real editor — the smoke test asserts the bridge-lib marker in both so a dropped COPY or a shadowing real binary fails the image build. All bridge shims (`xdg-open`, `code`/`codium`, `proximo`) source `/usr/local/lib/toolbox/bridge-lib.sh` for the shared transport (state-dir location, readiness checks, curl POST — `TestShimPathsMatchGoConstants` pins the state dir there against `bridge.ContainerDir`); each shim keeps only its own validation, messages, and exit policy. Behaviour:

- No args → `.`. Each path arg is resolved to absolute (`realpath -m` against `$PWD`), must live under `/workspace`, and is rewritten with `$TOOLBOX_HOST_WORKSPACE` (injected by `sessionplan` in every shell) before one `POST /edit` per path. The daemon stays workspace-agnostic: it receives host paths only.
- Paths outside `/workspace` are rejected locally (the host cannot see them); flags (`-g`, `--diff`, …) are rejected honestly rather than silently dropped. Both exit 1 with no POST.
- Daemon-side launch: editor CLI from `PATH`; macOS falls back to `open -a "Visual Studio Code"` / `open -a "VSCodium"` when the CLI shim isn't installed (per-OS split in `internal/bridge/editor_darwin.go` / `editor_linux.go`).
- Fallback hints mirror `xdg-open` (`toolbox bridge install` when state is missing, `status` when unreachable, "update the host toolbox CLI" on 404 from a pre-`/edit` daemon) — but unlike `xdg-open` (exit 0 so OAuth flows never block) the editor shims exit non-zero: they're interactive commands and a false success is misleading.

## Mount gating

`bridge: true` in config (default — `internal/config/plan.go` seeds it) causes `mountplan.Defaults` (`internal/mountplan/defaults.go`) to append the three bridge binds (RO state ×2 targets + RW `bridge-run`). Setting `bridge: false` drops all three; the in-container wrapper has nothing to talk to and falls back to the one-line tip emitted by `cmd/shell.go` ("install the host daemon with `toolbox bridge install`"). The toggle lives outside `tools:` because it's host-side — it does not invalidate the image hash.

## Uninstall surface

```bash
toolbox bridge uninstall   # supervisor unit + plist/service file
rm -rf ~/.toolbox/toolbox/bridge          # token + port + pid + log
```

Both steps are independent: `uninstall` removes only what `install` wrote. The state dir is left behind if a user revokes the daemon but wants to keep the token around (rare; documented for completeness). No system-level files, no `sudo`, no Homebrew formula touch.

## Troubleshooting

- `toolbox bridge status` prints state-dir path, token presence, port, supervisor install + run state, and the platform-specific detail line (`launchctl print` excerpt on macOS, `systemctl --user status` excerpt on Linux). On Linux it also prints the unix socket path and whether the daemon has bound it (`present=true`).
- Stale socket after a daemon crash (SIGKILL skips the unlink): the shim gets `000` and falls back to TCP; the daemon unlinks-before-listen on restart, so `systemctl --user restart toolbox-bridge` always recovers.
- Daemon log: `~/.toolbox/toolbox/bridge/log` — opened in append mode by the daemon, no built-in rotation; truncate or `logrotate` it yourself if it grows.
- Container-side wrapper failures (no port file, no host route) surface as a single-line tip on shell entry (`cmd/shell.go`'s "bridge tip" path); the wrapper itself exits non-zero so callers can detect the failure.

See also: [proximo](proximo.md) for the `/proximo` endpoint that drives the host proximo stack from inside the shell, and [`toolbox shell` port publishing](commands.md#toolbox-shell) for the inverse direction (host → container connections).
