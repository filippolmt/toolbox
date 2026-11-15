# Global skills are baked at build; per-repo installs refresh on demand

Status: accepted — supersedes [ADR 0001](0001-workspace-install-refresh.md)

The question comes from
[#1462](https://github.com/filippolmt/toolbox/issues/1462). A toolbox is now
usually opened on a parent folder (`~/projects`) and the developer `cd`s into
individual repos. Two Init Sequence families handled agent skills, and neither
fits that:

- **Global skills.** `02-gh.sh`, `60-glab.sh` and `61-herdr.sh` rewrote their
  skill on every container start (`--force` or an unconditional overwrite).
  `gh skill install` also went to the network and resolved the *latest*
  `cli/cli` release, not the content matching the bundled `gh`.
  `62-worktrunk.sh` already installed a skill extracted at build from its
  release and copied it only when it differed.
- **Per-repo installers** (ADR 0001's Workspace Install Refresh:
  `30-graphify.sh`, `31-codegraph.sh`, `40-playwright-cli.sh`) gated on an
  opt-in marker under `$PWD`. Init scripts run once, at container start, with
  `$PWD` the workspace root. For a parent-folder workspace that is no repo, so
  no repo was ever refreshed: the gate was dead in the common case, and in the
  other it wrote into the repo whenever a container started.

`~/.agents/skills` (Codex, pi) is container-local and empty after every
recreate, so whatever replaces the global half has to keep restoring itself.

## Decision

**Global skills: bake from the release tag at build, copy if different at
start.** The worktrunk pattern, generalised.

- The fetch stage of each tool writes its skill into
  `/usr/local/share/toolbox/skills/<tool>` from the pinned release:
  `gh skill install cli/cli gh --pin v${GH_VERSION}` (with the toolbox
  monitoring section appended there instead of at runtime),
  `glab skills install --path …`, `herdr --skill`, and worktrunk's source
  archive. Renovate moves skill and binary together, and the build fails when a
  skill comes out empty (`test -s`).
- At start, one function, `toolbox_sync_skill` in `bin/agent-topology-lib.sh`,
  copies the baked skill into every active skill root only where the copy there
  differs. No stamps, no network; a deleted skill comes back at the next start;
  a hand edit is overwritten, as it already was for gh and glab.

**Per-repo tools: no automatic refresh, an explicit command.**

- The per-repo refresh leaves the Init Sequence: `30-graphify.sh`,
  `31-codegraph.sh`, the per-repo half of `40-playwright-cli.sh`, the version
  stamps and `bin/install-refresh-lib.sh` are removed. `40-playwright-cli.sh`
  keeps the Chromium sync, which is global and already version-gated.
- `toolbox-refresh [tool…]` runs inside the container. With no arguments it
  re-syncs every global skill and, when the current repo has opted in, re-runs
  that repo's installers: graphify (with the matcher narrowing and
  `graphify hook install`), `codegraph install --refresh`, playwright-cli for
  Claude and for the cross-agent root. With arguments it refreshes only the
  named tools. The repo root comes from `git rev-parse --show-toplevel`, never
  `$PWD`.
- Opt-in stays with `graphify-init`, `codegraph-init` and `pwcli-init`;
  `toolbox-refresh` never opts a repo in.

## Considered options

**Keep the per-repo gate and run it from the shell, per `cd`.** A `chpwd` hook
could find the repo the developer entered and run ADR 0001's gate there. It
would make the gate live again, but it puts installers that write tracked files
behind a directory change, which is the "image upgrade hands you a dirty tree"
surprise ADR 0001 existed to contain, now on every first `cd` after an upgrade.
It also only covers zsh: an agent started in a pane never runs the hook.
Rejected; the explicit command does the same work when the developer asks.

**Stamp the global skills by version** (as `20-cf.sh` does with a marker).
Copy-if-different reaches the same result with no stamp to keep in step, and
it also repairs a skill edited or half-deleted by hand. Rejected.

**Keep fetching the gh skill at start.** It resolved the latest `cli/cli`
release, not the bundled `gh`, and it needed the network on every start.
Rejected.

## Consequences

- A repo's per-repo skills no longer follow an image upgrade by themselves:
  after an upgrade the developer runs `toolbox-refresh` in the repo. That is
  the cost of never writing into a repo the developer did not ask about.
- Two starts on the same image write no global `SKILL.md`; the shared Claude
  skill mount sees no churn.
- `gh skill install` now runs during the image build and talks to the GitHub
  API without a token, so a build runner that has exhausted the anonymous rate
  limit fails that stage. The build retries nothing; a rerun is the remedy.
- The `playwright-config` mount stays: nothing in the image writes to
  `~/.playwright` any more, but `playwright-cli` reads
  `~/.playwright/cli.config.json` as its global config, and the mount keeps a
  developer's edits across `toolbox stop`.
