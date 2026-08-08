package mountplan

import (
	"github.com/filippolmt/toolbox/internal/bridge"
	"github.com/filippolmt/toolbox/internal/config"
)

// defaults returns the canonical default mount set (D-07).
// ~/.secrets is intentionally NOT included (D-08).
//
// Every auth/state path is addressed through ~/.toolbox/ on the host:
//   - Claude / state / gh / glab live there as real dirs (isolated from
//     the host's own ~/.claude, ~/.config/gh, etc.).
//   - ssh / gitconfig are symlinks to the host's versions, so `ssh-keygen`
//     and `git config` stay in sync with the container.
//
// If a symlink target is missing on the host, that mount is skipped with
// a warning; the user can add it later without re-running any command.
func defaults() []config.Mount {
	return []config.Mount{
		// Claude Code config + credentials.
		{Name: "claude", Source: "~/.toolbox/.claude", Target: "/home/toolbox/.claude", ReadOnly: false, CreateIfMissing: true},
		// OpenAI Codex CLI auth + config — populated by `codex login` inside the container.
		{Name: "codex", Source: "~/.toolbox/.codex", Target: "/home/toolbox/.codex", ReadOnly: false, CreateIfMissing: true},
		// Bash history and other shell state, shared across every toolbox shell.
		{Name: "state", Source: "~/.toolbox/toolbox/state", Target: "/home/toolbox/.toolbox-state", ReadOnly: false, CreateIfMissing: true},
		// SSH keys and git config follow the host via symlinks under ~/.toolbox/,
		// so changes made with `ssh-keygen` / `git config` on the host are
		// immediately visible inside the container.
		// ssh stays read-only: the host's private keys must not be rewritable
		// from the container. gitconfig is read-write and host-synced both ways,
		// so `git config` inside the container edits the real host ~/.gitconfig.
		// See docs/mounts.md for how to re-lock or disable it via ~/.toolbox.yaml.
		{Name: "ssh", Source: "~/.toolbox/ssh", Target: "/home/toolbox/.ssh", ReadOnly: true, SymlinkFrom: "~/.ssh"},
		{Name: "gitconfig", Source: "~/.toolbox/gitconfig", Target: "/home/toolbox/.gitconfig", SymlinkFrom: "~/.gitconfig"},
		// GitHub CLI auth — populated by `gh auth login` inside the container.
		{Name: "gh", Source: "~/.toolbox/gh", Target: "/home/toolbox/.config/gh", ReadOnly: false, CreateIfMissing: true},
		// GitLab CLI auth — populated by `glab auth login` inside the container.
		{Name: "glab", Source: "~/.toolbox/glab", Target: "/home/toolbox/.config/glab-cli", ReadOnly: false, CreateIfMissing: true},
		// gcloud auth + config — populated by `gcloud auth login` inside the container.
		{Name: "gcloud", Source: "~/.toolbox/gcloud", Target: "/home/toolbox/.config/gcloud", ReadOnly: false, CreateIfMissing: true},
		// Google Workspace CLI auth + config — populated by `gws auth login` inside the container.
		// Default config dir is ~/.config/gws (overridable via GOOGLE_WORKSPACE_CLI_CONFIG_DIR).
		// The image sets GOOGLE_WORKSPACE_CLI_KEYRING_BACKEND=file so the encryption key lands
		// in this bind-mount instead of an OS keyring (unavailable inside the container).
		{Name: "gws", Source: "~/.toolbox/gws", Target: "/home/toolbox/.config/gws", ReadOnly: false, CreateIfMissing: true},
		// atuin (SQLite-backed shell history). Default DB at ~/.local/share/atuin/history.db
		// (XDG data dir) is the bind target. The Dockerfile sets ATUIN_CONFIG_DIR=<target>/config
		// so the config.toml + key (sync-encryption secret) land under the same bind-mount —
		// upstream defaults split them across ~/.config/atuin + ~/.local/share/atuin, but
		// consolidating keeps a single ~/.toolbox/atuin/ root on the host.
		{Name: "atuin", Source: "~/.toolbox/atuin", Target: "/home/toolbox/.local/share/atuin", ReadOnly: false, CreateIfMissing: true},
		// Azure CLI auth + config — populated by `az login` inside the container.
		{Name: "azure", Source: "~/.toolbox/azure", Target: "/home/toolbox/.azure", ReadOnly: false, CreateIfMissing: true},
		// Oracle OCI CLI auth + config — populated by `oci setup config` inside the container.
		{Name: "oci", Source: "~/.toolbox/oci", Target: "/home/toolbox/.oci", ReadOnly: false, CreateIfMissing: true},
		// SonarQube CLI state — populated by `sonar auth login` inside the container.
		// Default data dir is ~/.sonar/sonarqube-cli (overridable via SONAR_USER_HOME).
		// The image sets SONARQUBE_CLI_KEYCHAIN_FILE inside this bind-mount because
		// the default token store (libsecret keyring) doesn't exist in the container.
		{Name: "sonar", Source: "~/.toolbox/sonar", Target: "/home/toolbox/.sonar", ReadOnly: false, CreateIfMissing: true},
		// Docker CLI config (~/.docker/config.json) — credHelpers + auths written by
		// `docker login`, `gcloud auth configure-docker`, `aws ecr get-login-password`,
		// etc. Without this bind every registry login wipes on `toolbox stop`. The
		// docker socket itself is mounted separately below as "docker-sock".
		{Name: "docker", Source: "~/.toolbox/docker", Target: "/home/toolbox/.docker", ReadOnly: false, CreateIfMissing: true},
		// Cloudflare CLI (`cf`, Wrangler vNext preview) splits state across two
		// hard-coded upstream paths (no env override on either), so we follow the
		// rtk pattern: both bind sources nested under a single ~/.toolbox/cf/ root
		// on the host (flat layout) while the container keeps the upstream split.
		//
		// The two names read inverted against the upstream dirs (cf-auth backs
		// "cloudflare", cf-config backs ".cf") deliberately: they are named after
		// what they hold, and they stay put because mount names are user-visible
		// in `mounts:` patches and `--share cf`.
		//
		// "cf-auth" — since cf 0.5 the credential store lives under
		// ~/.config/cloudflare/ (xdgAppPaths appName "cloudflare", no leading
		// dot): config/default.json holds the OAuth tokens and
		// profiles/directory-bindings.json the named profiles. Written by
		// `cf auth login`; without this bind the auth wipes on every
		// `toolbox stop` AND stays invisible to every other running toolbox.
		// Earlier layouts (~/.config/.cf/auth.jsonc, ~/.cf/config.toml) are no
		// longer read, so we mount the current path only.
		{Name: "cf-auth", Source: "~/.toolbox/cf/auth", Target: "/home/toolbox/.config/cloudflare", ReadOnly: false, CreateIfMissing: true},
		// "cf-config" — ~/.config/.cf/config.json stores context defaults
		// (`cf context set …`), the shell-completion install marker, and other
		// UI prefs. Lighter than the auth file but still useful to persist.
		{Name: "cf-config", Source: "~/.toolbox/cf/config", Target: "/home/toolbox/.config/.cf", ReadOnly: false, CreateIfMissing: true},
		// Cloudflare Wrangler CLI auth + config — populated by `wrangler login`
		// inside the container. Wrangler uses xdg-app-paths(".wrangler") which
		// on Linux resolves to ~/.config/.wrangler/; OAuth credentials land at
		// ~/.config/.wrangler/config/default.toml. Without this bind every
		// `wrangler login` wipes on `toolbox stop`.
		{Name: "wrangler", Source: "~/.toolbox/wrangler", Target: "/home/toolbox/.config/.wrangler", ReadOnly: false, CreateIfMissing: true},
		// rtk follows XDG, which means it splits state across ~/.config/rtk
		// (config) and ~/.local/share/rtk (data). Both bind sources are
		// nested under a single ~/.toolbox/rtk/ root on the host so all rtk
		// state lives under one parent dir; inside the container the two
		// XDG-compliant targets stay separate because rtk hard-codes the
		// data path (RTK_DB_PATH only partially redirects writes).
		//
		// "rtk" — config.toml (also stores GDPR telemetry consent in
		// [telemetry]) and filters.toml. Written by `rtk config --create`
		// and `rtk init`.
		{Name: "rtk", Source: "~/.toolbox/rtk/config", Target: "/home/toolbox/.config/rtk", ReadOnly: false, CreateIfMissing: true},
		// "rtk-data" — analytics database (history.db, read by `rtk gain`),
		// the telemetry salt file, and tee dumps. Without this bind the
		// savings history wipes on every `toolbox stop`.
		{Name: "rtk-data", Source: "~/.toolbox/rtk/data", Target: "/home/toolbox/.local/share/rtk", ReadOnly: false, CreateIfMissing: true},
		// kubeconfig — populated by `gcloud container clusters get-credentials`,
		// `aws eks update-kubeconfig`, manual edits, etc. Persists across the
		// auto-remove-on-exit container lifecycle so cluster context survives
		// a reopened shell.
		{Name: "kube", Source: "~/.toolbox/kube", Target: "/home/toolbox/.kube", ReadOnly: false, CreateIfMissing: true},
		// Playwright browser cache — populated by `playwright install`; keeps the
		// ~500MB of Chromium/Firefox/Webkit binaries across container restarts.
		{Name: "playwright-cache", Source: "~/.toolbox/playwright-cache", Target: "/home/toolbox/.cache/ms-playwright", ReadOnly: false, CreateIfMissing: true},
		// Playwright-cli workspace config — `playwright-cli install` writes
		// `cli.config.json` (browser channel + launchOptions) into the CWD's
		// `.playwright/` dir, and the entrypoint runs the install from $HOME so
		// the file lands here. The install is non-destructive on existing
		// configs, so user customisations survive subsequent shell starts.
		// Without this bind-mount the config would be wiped on every
		// `toolbox stop` (auto-remove-on-exit container lifecycle).
		{Name: "playwright-config", Source: "~/.toolbox/playwright-config", Target: "/home/toolbox/.playwright", ReadOnly: false, CreateIfMissing: true},
		// User-defined startup hooks. Any *.sh file here is executed by the
		// entrypoint before handing control to the shell — read-only to prevent
		// in-container tampering; edits happen on the host.
		{Name: "startup.d", Source: "~/.toolbox/startup.d", Target: "/home/toolbox/.toolbox-startup.d", ReadOnly: true, CreateIfMissing: true},
		// Host-provided CA certificates. Any *.pem/.crt/.cer/.der file dropped
		// here is trusted at each shell start across the system bundle, NSS db,
		// Node and python-requests (entrypoint.sh, beside the proximo block).
		// Zero-config, no flag. RO so the container can't rewrite user certs;
		// CreateIfMissing materialises the empty folder so it is auto-discoverable.
		// Target /etc/toolbox/certs is a read-source (like proximo's CA), NOT a
		// drop-in trust path — trust is established from it, not by mounting there.
		{Name: "certs", Source: "~/.toolbox/certs", Target: "/etc/toolbox/certs", ReadOnly: true, CreateIfMissing: true},
		// Per-user npm global prefix. Keeps runtime `npm install -g` writable
		// without root and persistent across container recreations. The prefix
		// itself is wired via NPM_CONFIG_PREFIX + PATH in the Dockerfile.
		{Name: "npm-global", Source: "~/.toolbox/npm-global", Target: "/home/toolbox/.npm-global", ReadOnly: false, CreateIfMissing: true},
		// bun state — install cache + global packages + per-user bin (~/.bun/bin).
		// `bun add -g <pkg>` writes here; without this bind, every `toolbox stop`
		// wipes the global package set and re-downloads the install cache.
		// PATH augmentation for ~/.bun/bin is wired in the Dockerfile (Layer 11a).
		{Name: "bun", Source: "~/.toolbox/bun", Target: "/home/toolbox/.bun", ReadOnly: false, CreateIfMissing: true},
		// Per-user Go workspace (GOPATH). Go's default `$HOME/go` resolves
		// to /home/toolbox/go inside the container; this bind-mount persists
		// the module cache (`pkg/mod`) and `go install` binaries (`bin/`)
		// across container recreations. Unconditional — matches the
		// playwright-cache / npm-global pattern (D-11). No GOROOT/GOPATH
		// ENV required (D-08 / D-09): Go auto-detects GOROOT from the
		// `/usr/local/go/bin/go` exec path and defaults GOPATH to $HOME/go.
		{Name: "go", Source: "~/.toolbox/go", Target: "/home/toolbox/go", ReadOnly: false, CreateIfMissing: true},
		// herdr (agent multiplexer TUI) follows XDG and splits durable state across
		// ~/.config/herdr and ~/.local/state/herdr, so both bind sources nest
		// under a single ~/.toolbox/herdr/ root on the host (flat layout, rtk
		// pattern) while the container keeps the XDG-compliant split. Without
		// these binds every detachable session, installed/enabled plugin, and
		// config wipes on `toolbox stop` — herdr's whole value is persistence.
		//
		// "herdr" — config dir: config, plugins, and session state all live
		// here (session data_dir() is derived from config_dir(), i.e.
		// ~/.config/herdr/sessions/<name>). herdr would also drop its unix
		// sockets here by default, but this bind is a Docker-Desktop fakeowner
		// fs that rejects chmod() on sockets (EINVAL) — fatal for the server —
		// so the Dockerfile ENV relocates them to a container-local path
		// (HERDR_SOCKET_PATH / HERDR_CLIENT_SOCKET_PATH). Sockets are ephemeral
		// and no longer live under this mount.
		{Name: "herdr", Source: "~/.toolbox/herdr/config", Target: "/home/toolbox/.config/herdr", ReadOnly: false, CreateIfMissing: true},
		// "herdr-state" — XDG state dir: per-plugin runtime state written via
		// plugin_state_dir(). Kept so plugin state survives alongside the
		// now-user-global plugin registry.
		{Name: "herdr-state", Source: "~/.toolbox/herdr/state", Target: "/home/toolbox/.local/state/herdr", ReadOnly: false, CreateIfMissing: true},
		// Bridge state: token + port + log + pid written by `toolbox bridge
		// daemon` on the host. RO inside the container because the shims only
		// read. CreateIfMissing keeps the mount resolvable even before the
		// user runs `toolbox bridge install` (the shims fall back to printing
		// the URL on stderr in that case). The legacy target keeps pre-rename
		// images working: their shims hardcode the old in-container path.
		{Name: "bridge", Source: "~/" + bridge.HostDir, Target: bridge.ContainerDir, ReadOnly: true, CreateIfMissing: true},
		{Name: "bridge-legacy", Source: "~/" + bridge.HostDir, Target: bridge.LegacyContainerDir, ReadOnly: true, CreateIfMissing: true},
		// Daemon unix socket (bound on native Linux hosts only; on macOS the
		// dir stays empty and the mount is inert). RW because connect() on a
		// socket inside a RO mount fails with EROFS — run/ is the only
		// writable subdir of the bridge state dir. Nested inside the RO
		// "bridge" target: Docker mounts binds in target-depth order, so this
		// overrides the parent. No legacy target — pre-socket shims are
		// TCP-only and would never read it.
		{Name: "bridge-run", Source: "~/" + bridge.HostRunDir, Target: bridge.ContainerRunDir, ReadOnly: false, CreateIfMissing: true},
		// Docker socket for DinD-free container access.
		{Name: "docker-sock", Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: false},
	}
}
