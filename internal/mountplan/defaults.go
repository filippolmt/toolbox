package mountplan

import "github.com/filippolmt/toolbox/internal/config"

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
		{Name: "state", Source: "~/.toolbox/state", Target: "/home/toolbox/.toolbox-state", ReadOnly: false, CreateIfMissing: true},
		// SSH keys and git config follow the host via symlinks under ~/.toolbox/,
		// so changes made with `ssh-keygen` / `git config` on the host are
		// immediately visible inside the container (and vice versa).
		{Name: "ssh", Source: "~/.toolbox/ssh", Target: "/home/toolbox/.ssh", ReadOnly: true, SymlinkFrom: "~/.ssh"},
		{Name: "gitconfig", Source: "~/.toolbox/gitconfig", Target: "/home/toolbox/.gitconfig", ReadOnly: true, SymlinkFrom: "~/.gitconfig"},
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
		// "cf-auth" — ~/.cf/config.toml stores the OAuth access_token + refresh_token
		// written by `cf auth login`. Without this bind the auth wipes on every
		// `toolbox stop`.
		{Name: "cf-auth", Source: "~/.toolbox/cf/auth", Target: "/home/toolbox/.cf", ReadOnly: false, CreateIfMissing: true},
		// "cf-config" — ~/.config/cf/config.json stores context defaults
		// (`cf context set …`), the shell-completion install marker, and other
		// UI prefs. Lighter than the auth file but still useful to persist.
		{Name: "cf-config", Source: "~/.toolbox/cf/config", Target: "/home/toolbox/.config/cf", ReadOnly: false, CreateIfMissing: true},
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
		// Browser bridge state: token + port + log + pid written by
		// `toolbox browser-bridge daemon` on the host. RO inside the container
		// because the wrapper only reads. CreateIfMissing keeps the mount
		// resolvable even before the user runs `toolbox browser-bridge install`
		// (the wrapper falls back to printing the URL on stderr in that case).
		{Name: "browser-bridge", Source: "~/.toolbox/browser", Target: "/home/toolbox/.toolbox/browser", ReadOnly: true, CreateIfMissing: true},
		// Docker socket for DinD-free container access.
		{Name: "docker-sock", Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: false},
	}
}
