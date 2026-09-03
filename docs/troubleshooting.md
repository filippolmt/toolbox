# Troubleshooting

Symptoms, causes, and fixes for the failure modes users actually hit. Bridge-specific issues live in [bridge troubleshooting](bridge.md#troubleshooting).

## Config or port edits don't take effect

**Symptom:** you edited `.toolbox.yaml` (mounts, env, proximo) or re-ran `toolbox shell -p <port>` / `-B`, but the running container behaves as before.

**Cause:** mounts, [port bindings](commands.md#publishing-ports), env vars, and proximo `ExtraHosts` are all fixed at `ContainerCreate` — Docker accepts no post-hoc changes, and `toolbox shell` reattaches to an existing container instead of recreating it.

**Fix:** `toolbox stop`, then re-run `toolbox shell …`. The container is disposable; all persistent state lives on the `~/.toolbox/` binds, so nothing is lost.

## Port already in use on the host

**Symptom:** `toolbox shell -p <port>` fails at container start with a Docker "address already in use" error.

**Cause:** another host process (or another toolbox container) already binds that host port.

**Fix:** find and stop the listener (`lsof -i :<port>` on macOS/Linux) or publish a different host port (`-p <other>:<port>`). If the conflict is another workspace's toolbox container, `toolbox stop <name|dir>` it first.

## "unknown mount name" at startup

**Symptom:** `toolbox shell` fails immediately with an error about a `mounts:` entry referencing an unknown name.

**Cause:** a patch entry (no `target:`) names a mount that isn't a default and isn't a user entry — usually a typo. Failing loud at `Plan()` is deliberate, so typos surface immediately instead of silently appending a broken mount.

**Fix:** check the spelling against the default names in [mounts](mounts.md#mounts-merge-semantics) (`toolbox mounts list` shows the effective set; the CLI suggests close matches). To add a genuinely new mount, set `target:` too (append form).

## Nerd Font placeholders in the prompt

**Symptom:** the starship prompt shows `?` / `▢` replacement glyphs instead of icons (git branch, kubernetes, language logos).

**Cause:** the host terminal has no Nerd Font configured — glyphs are rendered by the host terminal, not the container.

**Fix:** install a [Nerd Font](https://www.nerdfonts.com/) on the host and select it in your terminal profile, e.g. `brew install --cask font-jetbrains-mono-nerd-font`. (Background on why the bundled prompt pins specific glyphs: [shell-start internals](internals/shell-start.md#prompt-glyph-width).)

## xdg-open, code, or OAuth login does nothing

See [bridge troubleshooting](bridge.md#troubleshooting) — the usual causes are the bridge daemon not installed (`toolbox bridge install`), not running (`toolbox bridge status`), or a version-skewed host CLI. For OAuth callbacks that never reach the in-container CLI, see the [loopback bridge](commands.md#loopback-bridge).

## herdr plays no sound when an agent finishes

**Symptom:** herdr's agent-state chimes never sound, with nothing on screen to say why. `~/.config/herdr/herdr-client.log` carries `sound playback failed … no mp3-capable audio player available`, naming all five players it tried.

**Cause:** the container has no audio backend and none of the five players herdr spawns, no `/dev/snd` and no sound-server socket. The playback is handed to the host instead, over the bridge.

**Fix:** install the bridge (`toolbox bridge install` on the host — `toolbox bridge status` if it is already installed), and make sure the host CLI is new enough to serve `/sound`: an older daemon answers 404 and the shim fails through exactly as if no player existed. Then check the herdr side, `ui.sound.enabled` and the optional `ui.toast.delivery` banner — [agent sounds](bridge.md#agent-sounds) documents both, and a running herdr server needs `herdr server reload-config` before it sees a config edit.

**Sounds still only sometimes?** That is by design and it is herdr's rule, not the bridge's: `Request` (an agent waiting on a human) always fires, while `Done` fires only when the pane's tab is inactive or the terminal window is unfocused. Panes in the same tab share that state, so a single-tab split layout stays quiet on `Done` while you are looking at it.

## Stale local branches pile up after merges

**Symptom:** `git branch` lists many local branches whose PRs were already merged — squash-merged branches (the local copy isn't recognised as merged) and leftover `worktree-agent-*` branches from agent worktrees.

**Cause:** a squash merge rewrites history, so `git branch -d` refuses the branch as "not fully merged"; the remote branch is gone but the local tracking branch lingers. Agent worktrees leave throwaway branches behind once their worktree is removed.

**Fix:** run `git prune-dead` (baked into the image, invoked as a `git` subcommand). It `git fetch --prune`s, then deletes a branch **only on positive proof that its PR/MR was merged**, asking the forge one branch at a time; it also deletes `worktree-agent-*` leftovers actually merged into the default branch, and `git worktree prune`s stale entries. It never touches the current branch or the repository's default branch (resolved from `origin/HEAD`, falling back to a local `main`/`master`, then the current branch), so a branch with a live remote — or one checked out in another worktree — is left alone.

**A gone upstream is not the proof.** A squash-merged PR and one closed without merging leave exactly the same local state — upstream gone, commits unreachable from the default branch — and the second still holds the only copy of its work. So anything the forge does not confirm as merged is kept, with the reason printed: closed or never opened, unreadable state, or no logged-in CLI.

**A merge on someone else's fork is not your merge.** Neither CLI scopes its branch filter to the head repository — on `cli/cli`, `gh pr list --head patch-1 --state merged` returns merged PRs from thirty different fork owners. So the query names the repository explicitly (`--repo <owner>/<name>`, derived from `origin`, rather than whichever remote the CLI would have picked on a fork clone) and discards any match that came from a fork. Without that, a common branch name like `fix` or `patch-1` would read as merged because a stranger's did.

**Which forge, and being logged in.** The CLI is chosen by asking `gh` and then `glab` which one holds a session for origin's exact host (`gh auth status --hostname <host>`), so github.com, gitlab.com, GitHub Enterprise and a self-hosted GitLab all work without the domain name mattering — what counts is where you are logged in. With no session for that host, nothing is deleted and the run tells you to `gh auth login --hostname <host>` (or `glab auth login --hostname <host>`). Both CLIs inherit their host credentials through [`inherit_host_auth`](configuration.md#inherit-host-auth).

Deletes use `git branch -D` (force), since a squash-merged branch never reads as merged locally. Recover one from the reflog — `git reflog`, then `git branch <name> <sha>` — until it is garbage-collected.

## A new `.test` app is unreachable from the container

**Symptom:** `https://<name>.test` works in the host browser but fails to resolve/connect inside a shell that was already open.

**Cause:** proximo-routed hosts are discovered and pinned at container create time; apps started afterwards are invisible to the running container.

**Fix:** start the proximo stack (or the new app) first, then `toolbox stop` + `toolbox shell` to re-discover. Details: [proximo boundaries](proximo.md#boundaries-and-caveats).

## "manifest unknown" with a registry mirror

**Symptom:** with [`registry_mirror`](configuration.md#image-selection) configured, the first `toolbox shell` on a machine warns about a failed pull (`manifest unknown` / `not found` from the mirror), then aborts with `image "<mirror>/filippolmt/toolbox:latest" not available locally and pull failed — check registry access`.

**Cause:** a pull-through cache (Harbor proxy project, ECR pull-through, Artifactory/Nexus remote repo) only copies an image from GHCR when something asks for it — and some return `manifest unknown` on the very first request while they ingest the upstream copy asynchronously; replication-based mirrors serve nothing until a replication run completes. The registry refresh is best-effort, but on a first shell there is no local copy to fall back to, so `imageplan.Ensure` fails loud instead of starting a container without an image. Only *successful* pulls stamp the refresh TTL cache, so the failed attempt doesn't poison later ones — a retry asks the mirror again.

**Fix:** warm the cache before the first shell — `docker pull <mirror-host>/filippolmt/toolbox:latest` (re-run once if the mirror 404s while it ingests). Once the mirror serves the manifest, `toolbox shell` works with the default `pull: auto`. Alternatively take the registry out of the startup path entirely: pull manually from the mirror as above, then set `toolbox config set --pull never --where global` — the local copy becomes authoritative and no round-trip happens at shell start. Note the presence check is by exact ref: an image pulled from GHCR directly needs a retag first (`docker tag ghcr.io/filippolmt/toolbox:latest <mirror-host>/filippolmt/toolbox:latest`). Mechanism and precedence: [image selection](configuration.md#image-selection).
