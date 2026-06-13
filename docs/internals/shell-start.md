# Shell start internals

Maintainer notes on what happens between `toolbox shell` attaching and the zsh prompt rendering: prompt/locale plumbing, `init.d/` boot scripts, and per-tool bootstrap.

## Prompt glyph width

Every symbol in `internal/build/assets/starship.toml` must be ASCII, unambiguous-narrow Unicode, or a Nerd Font PUA glyph — never an East Asian Ambiguous character or an emoji-presentation sequence (U+FE0F). Starship's *defaults* violate this: kubernetes ships `☸ ` (U+2638, EA-Ambiguous) and gcloud ships `☁️ ` (U+2601+FE0F). Ghostty measures them with Unicode grapheme-cluster width (mode 2027) → 2 columns, while zsh ZLE lays out the line with libc `wcwidth()` → 1 column. One column of drift per glyph meant every Backspace left exactly as many ghost characters as ambiguous emoji visible in the prompt — a months-long "intermittent" bug, because the k8s/gcloud modules only render where those contexts are active. Three earlier fixes (autosuggestions rebind, TERM forwarding, terminfo bundling) chased adjacent symptoms; the real confirmation was `PROMPT='> '` killing the residue while plugin/RPROMPT/highlighter toggles did nothing. **Diagnostic heuristic: any "ghost characters on redraw" report → test `PROMPT='> '` first, before suspecting ZLE plugins.** The four module symbols (`kubernetes`, `gcloud`, `terraform`, `docker_context`) are pinned to PUA glyphs with codepoint comments in `starship.toml`; PUA is width-1 under both width systems by construction, and Nerd Font on the host is already a README prerequisite.

## UTF-8 locale

The image bakes `ENV LANG=C.UTF-8` (Dockerfile final stage). debian-slim ships no `LANG` at all, so the container otherwise runs in the POSIX locale — under which zsh's ZLE cannot decode multibyte input and renders every UTF-8 byte it has to redraw as `<ffffffff>`. The visible symptom: typing a command that prefix-matches a history entry containing non-ASCII bytes (starship glyphs pasted into heredocs, accented letters, `…`) makes zsh-autosuggestions' ghost text show up as `➜  cd <ffffffff><ffffffff>`. Same ZLE-encoding family as [prompt glyph width](#prompt-glyph-width), different mechanism — that one is width drift, this one is decode failure. `C.UTF-8` is compiled into glibc (`locale -a` lists `C.utf8` on a stock bookworm-slim), so no `locales` package or `locale-gen` layer is needed. Deliberately `LANG` only, **not** `LC_ALL`: `LC_ALL` outranks everything, so baking it would override any locale the user forwards via `.toolbox.yaml` `env:` passthrough. Smoke test asserts `locale charmap` = `UTF-8`.

## MCP plugin auto-build

`internal/build/assets/init.d/50-mcp-plugins.sh` scans `~/.claude/plugins/cache/**` and runs `npm install && npm run build` for any plugin missing a `dist/`. First shell after a plugin install is therefore slower; subsequent shells cached via `.toolbox-built` marker. On failure stderr is captured to `.toolbox-build-error.log` next to the marker (in the same bind-mounted plugin dir, so it survives container restarts) and the last 5 lines are printed inline; failure stays non-fatal.

## Playwright browser cache sync

`internal/build/assets/init.d/40-playwright-cli.sh` does two jobs. Besides the per-repo playwright-cli skill refresh (see [Per-repo playwright-cli skill](#per-repo-playwright-cli-skill) below), it syncs the bundled Chromium to the pinned playwright version. The Dockerfile bakes the `playwright` npm package + `playwright install-deps chromium` (apt deps) only — the **browser binaries** are not baked; they live in the `~/.toolbox/playwright-cache` bind (host-persisted, kept out of the image). Since nothing else downloads them, a `playwright` Renovate bump would otherwise leave the cache on the old Chromium revision and break the default headless launch: playwright resolves `chromium.launch({headless:true})` to a separate `chromium_headless_shell-<rev>` binary that a stale cache never fetched (observed: cache held `chromium-1224` with no headless shell after a bump to 1.60.0, whose pinned rev is 1223). A version sentinel (`<cache>/.toolbox-chromium-version`, compared against the playwright package.json version — read via `node`, not `playwright --version`, to dodge the rtk wrapper) makes the sync a no-op on every shell except the first after a bump, when it runs `playwright install chromium` (full + headless shell) once. Best-effort + non-fatal: an offline shell still starts. This rides the existing `40-` script (no new init.d → no `TestCatalogInitDBijection` / smoke-count edit).

## `cf` Cloudflare CLI skill auto-install

When the `cf` and `claude` binaries are present and `~/.claude` exists, `internal/build/assets/init.d/20-cf.sh` writes a Claude Code skill to `~/.claude/skills/cf/SKILL.md` if absent. Skill is hand-written and points Claude to `cf agent-context <product>` for on-demand product context (instead of pre-baking the ~107-product corpus). Idempotent — only re-creates when the file is missing, so user edits persist.

## Per-repo code-graph skills: graphify and codegraph

Both `graphify` (`init.d/30-graphify.sh`) and `codegraph` (`init.d/31-codegraph.sh`) wire themselves into a project **only when that project has opted in** — neither registers anything globally. Opt-in is a one-time manual step the user runs inside the repo they want indexed:

- graphify: `graphify claude install` (alias `graphify-init`) writes a `## graphify` section into the repo's local `CLAUDE.md` and registers PreToolUse hooks in the repo's `.claude/settings.json`; the graph data lives in `graphify-out/`.
- codegraph: `codegraph install --target=claude --location=local --yes` (alias `codegraph-init`) writes the per-project MCP config + a marker-fenced section into `CLAUDE.md`/`AGENTS.md`; the symbol graph lives in `.codegraph/codegraph.db`.

The `*-init` aliases (`graphify-init`, `codegraph-init`, plus `pwcli-init` for the playwright-cli skill) are defined in `zshrc.sh` as shorthands for these one-time opt-in commands.

On every shell each script gates on the presence of the tool's marker dir in `$PWD` (`graphify-out/` resp. `.codegraph/`) **plus** the `claude` binary and `~/.claude`. When the dir is present it re-runs the install so the marker/config stays in sync with the bundled tool version after an image upgrade; when absent it exits 0 and writes nothing — so opening an un-opted-in repo never dirties it. Both refreshes are idempotent and non-fatal. Because the workspace is a host bind-mount, the marker dirs, MCP config, and `CLAUDE.md` edits persist on the host repo across sessions; codegraph has no global DB location, so persistence is exactly the per-repo `.codegraph/`.

This replaces graphify's previous always-on global `graphify install`, which refreshed `~/.claude/skills/graphify/SKILL.md` on every shell regardless of the repo. The global `/graphify` slash skill is no longer auto-installed; the `graphify` CLI stays bundled and the integration is per-repo via `graphify claude install`.

## Per-repo playwright-cli skill

`playwright-cli` follows the same per-repo opt-in model as graphify/codegraph above — `init.d/40-playwright-cli.sh` registers nothing globally. `playwright-cli install` initialises a workspace in `$PWD`: run with **no** `cd $HOME` wrapper it writes the skill to `$PWD/.claude/skills/playwright-cli/` (plus a `.playwright/` workspace dir), so opt-in is a one-time manual step the user runs inside the repo they want browser automation in:

- `playwright-cli install --skills claude` writes `.claude/skills/playwright-cli/SKILL.md` into the repo (`--skills` also accepts `agents` for the Codex/`~/.agents` layout). The `pwcli-init` shell alias (in `zshrc.sh`) is a shorthand for exactly this.

On every shell the script gates on the presence of `$PWD/.claude/skills/playwright-cli/` **plus** the `claude` binary and `~/.claude`. When the dir is present it re-runs `playwright-cli install --skills claude` (in CWD, no `cd $HOME`) so the skill stays in sync with the bundled playwright-cli version after an image upgrade — the SKILL.md + references are copied from the templates bundled in the playwright-cli package (kept in the image; the `.md` weight-prune is made to spare them — see [Node package weight prune](image-build.md#node-package-weight-prune)), so a Renovate `PLAYWRIGHT_CLI_VERSION` bump refreshes the skill on the next shell, offline. When the dir is absent it exits 0 and writes nothing, so opening an un-opted-in repo never dirties it. Idempotent and non-fatal; because the workspace is a host bind-mount the skill dir persists on the host repo across sessions.

This replaces the previous always-on `(cd "$HOME" && playwright-cli install --skills claude)`, which forced the otherwise-CWD-local install into `~/.claude/skills/playwright-cli/` on every shell regardless of the repo. The `cd $HOME` wrapper was the global switch; dropping it makes the integration per-repo. An existing global `~/.claude/skills/playwright-cli/` from the old behaviour is left as-is — it is simply no longer refreshed (matching how graphify left its old global skill in place).

This is unrelated to the global `playwright` browser-cache sync in the [same script](#playwright-browser-cache-sync), which stays always-on (it tracks the `playwright` package, not the per-repo `playwright-cli` opt-in).

## Skill discovery paths diverge between Claude and Codex

Claude Code reads only `~/.claude/skills/<name>/SKILL.md` (per docs.claude.com); Codex CLI reads only `~/.agents/skills/<name>/SKILL.md` (Agent Skills USER scope per agentskills.io). Despite the shared "Agent Skills" branding, the two locations are NOT mutually compatible. CLI wrappers that ship a SKILL.md need a dual-install pass to be visible in both agents. Reference: `internal/build/assets/init.d/60-glab.sh` runs `glab skills install --path ~/.claude/skills --force` for Claude and `glab skills install --global --force` for Codex, gated on the respective binaries.

## GitLab git credential helper (glab)

When `glab auth status` succeeds, `init.d/60-glab.sh` registers `!glab auth git-credential` as the git credential helper for **every host in glab's config** (`yq '.hosts | keys | .[]' ~/.config/glab-cli/config.yml`) — gitlab.com and any self-hosted instance the user has run `glab auth login --hostname <host>` for; new hosts need zero code changes. Written with `sudo git config --system` into the container's `/etc/gitconfig`: the host's `~/.gitconfig` is a read-only mount and stays byte-identical, while the system file is container-local and dies with the AutoRemove container. Registration is non-fatal — on failure a warning points at the SSH fallback (`git@<host>:…` keeps working via the RO `~/.ssh` mount).

Primary consumer: private Homebrew taps over HTTPS — `brew tap <name> https://<gitlab-host>/<group>/homebrew-tap.git` clones with the glab token, no prompts, no extra setup (the token already persists in `~/.toolbox/glab`). Benefits any in-container git clone/pull of private GitLab repos.

Limitation: the helper covers **git transports only**. Formulas that download release assets / package-registry artifacts over HTTPS go through brew's curl, which does not consult git credential helpers — such formulas need a custom download strategy reading a token. Revisit if a private tap grows that kind of formula.
