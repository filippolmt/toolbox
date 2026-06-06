---
paths:
  - "internal/container/**"
  - "internal/sessionplan/**"
  - "internal/runplan/**"
  - "internal/teardown/**"
  - "internal/proximo/**"
  - "internal/browserbridge/**"
  - "cmd/**"
  # Build assets whose invariants are documented here (loopback bridge
  # socat spawner, proximo CA trust in entrypoint):
  - "internal/build/assets/init.d/70-loopback-bridge.sh"
  - "internal/build/assets/entrypoint.sh"
---

# Container runtime gotchas — backstory in [`docs/runtime-notes.md`](../../docs/runtime-notes.md)

- **Image selection**: always `:latest` from GHCR. `toolbox build` overwrites the local cache when you need a custom build. No `local-<hash>` fallback, no catalog-driven image hash. → [image-selection](../../docs/runtime-notes.md#image-selection), [tools-removal](../../docs/runtime-notes.md#tools-removal)
- **Port bindings fixed at container creation**: `toolbox stop` before re-`shell -p …`. → [port-bindings](../../docs/runtime-notes.md#port-bindings-are-fixed-at-container-creation)
- **Loopback bridge `-B`**: static-port OAuth CLIs that bind `127.0.0.1` (codex, shopify, vanilla wrangler) need `toolbox shell -B -p <port>:<port>`; init.d/70 spawns socat per port. Dynamic-port CLIs (cf) keep their build-time sed patch — bridge cannot pre-bind an unknown port. Wildcard-bind CLIs (oci, `0.0.0.0:8181`) take plain `-p` and must NOT get `-B` — socat on eth0:<port> breaks a wildcard bind (EADDRINUSE). `--oauth <tool>` presets expand to the documented recipe (`sessionplan.ExpandOAuth`, map in `internal/sessionplan/oauth.go`) — keep map and runtime-notes survey in sync. **Breaking UX**: `wrangler login` previously worked with `-p 8976:8976` alone; now requires `-B`. → [loopback-bridge](../../docs/runtime-notes.md#loopback-bridge)
- **Codex nested sandbox**: codex always installed → Docker `seccomp=unconfined` always applied. → [codex-sandbox](../../docs/runtime-notes.md#codex-nested-sandbox)
- **Container teardown = AutoRemove**: containers created with `HostConfig.AutoRemove` (`container/lifecycle.go`). Shell exit `ContainerKill`s and returns — daemon removes async (fast prompt; macOS unmount of many binds is the slow part). Consequence: a stopped container is gone, so `runplan.ActionStart` (reuse-stopped) effectively never fires — every `toolbox shell` recreates. `teardown.OnShellExit` does one inspect → sibling-exec→leave / AutoRemove→kill / legacy→`StopOne`; `StopOne` tolerates the remove `Conflict` race. → [container-teardown](../../docs/runtime-notes.md#container-teardown)
- **Browser bridge**: opt-in host daemon (`toolbox browser-bridge install`) forwards in-container `xdg-open` to host browser. State `~/.toolbox/browser/` RO-mounted; `browser_bridge: false` skips the mount. Host-side toggle → no image-hash impact. → [browser-bridge](../../docs/runtime-notes.md#browser-bridge)
- **Proximo integration**: makes [proximo](https://github.com/filippolmt/proximo)-routed `https://<name>.test` apps reachable from inside the container, for ANY client. Tri-state `proximo` (`*bool`, `proximo.Enabled`): omitted → **auto** (on iff proximo's CA exists on host — installed = works everywhere, zero opt-in); `true`/`false` force. Host-side `toolbox shell` reads `proximo.hosts` labels off running containers → pins each to `host-gateway` in `ExtraHosts` (DNS), and RO-mounts proximo's CA (path via `proximo config ca-path`, fallback `~/.proximo/tls/ca.pem`). `entrypoint.sh` then makes trust seamless: `sudo update-ca-certificates` (curl/git/wget/python-ssl) + `certutil` into `~/.pki/nssdb` (chromium, incl. Playwright — `libnss3-tools` in base apt layer) + `NODE_EXTRA_CA_CERTS` (node). Lone gap: python-requests/certifi → set `REQUESTS_CA_BUNDLE=$TOOLBOX_PROXIMO_CA`. Discovery at create-time only (re-`shell` for new hosts). Trust lives in `entrypoint.sh` (NOT `init.d` → ties to no catalog tool, no bijection edit). `internal/proximo` (pure) + `container/lifecycle.go` (discovery). → [proximo-integration](../../docs/runtime-notes.md#proximo-integration)
