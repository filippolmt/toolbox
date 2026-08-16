# Workspace Install Refresh: gate the per-repo installers instead of re-running them every shell

Status: accepted

Three Init Sequence scripts — `30-graphify.sh`, `31-codegraph.sh` and
`40-playwright-cli.sh` — re-ran their tool's per-repo installer on every shell,
gated only on the opt-in artefact already existing in the workspace
(`graphify-out/`, `.codegraph/`, `.claude/skills/playwright-cli/`). Because
those installers write into the workspace, every image upgrade that moved a
bundled tool version rewrote **tracked** files — `CLAUDE.md`,
`.claude/settings.json`, `.claude/skills/graphify/` — and left the user with a
dirty tree they did not ask for. We now gate each refresh on *version differs
**or** an expected artefact is missing*, with a toolbox-owned version stamp kept
outside the workspace under `$HOME/.toolbox-state/`, keyed by `(workspace,
tool)`.

The name for the family is **Workspace Install Refresh** (see `CONTEXT.md`). The
three scripts already referenced each other in their comments ("mirrors
graphify/codegraph") without a written rule, which is the fan-out this repo has
collapsed before under Tool Catalog and Config Schema: unnamed, the fourth
member would have copied the old pattern.

## Considered Options

**Native per-tool stamps.** Each tool would provide its own gate: graphify
already writes `.graphify_version`, codegraph ships `install --refresh`,
playwright-cli has neither. Rejected because one family would run on three
mechanisms, and the one member with no native support would silently stay
ungated. The graphify stamp is a poor input anyway — in project scope nothing
reads it (`_check_skill_version` walks user-scope destinations only), so it is
write-only state whose meaning we would have had to invent.

**A stamp next to the installation** (`.claude/skills/<tool>/.toolbox-version`).
Uniform, but it creates a new workspace file — the exact churn the gate exists
to remove.

**Gate on version alone.** Simpler, but it turns a broken installation into a
permanently broken one: deleting `.mcp.json` or the graphify skill would no
longer self-heal at the next shell, and the symptom (a missing source) does not
point at the cause (an up-to-date stamp). The `or artefact missing` half of the
condition keeps the self-healing property the unconditional re-install had.

**Leave it as it is and commit the churn.** Viable for this repo, but the
scripts ship in the image: the cost lands on every user with an indexed repo,
not on us.

## Consequences

- A tool upgrade now takes effect on the *next* shell after the image bump, not
  retroactively across every already-running workspace — which is what the
  previous behaviour bought at the price of the churn.
- `30-graphify.sh` additionally normalises the two PreToolUse matchers that
  `graphify install` writes — the search guard `Bash|Grep` narrows to `Grep`,
  the read guard `Read|Glob` to `Glob` — and only when a matcher is still the
  known upstream value, so a hand-edited hook survives.
  This is graphify-specific: it is the only member that installs hooks.
- The artefact half watches exactly one artefact per tool — the skill's
  `SKILL.md` for graphify and playwright-cli, `.mcp.json` for codegraph — and
  deliberately not the PreToolUse hook block or the `## graphify` section in
  `CLAUDE.md`. Those two are content a user may edit or remove on purpose, and
  re-installing them on every shell would fight exactly the hand edits the
  matcher normalisation goes out of its way to preserve. A missing skill means
  the installation is broken; a missing hook block may mean the user meant it.
- An unreadable tool version (a probe the upstream CLI breaks) does not count as
  "differs from the stamp" — it would reopen the gate on every shell. The
  emptiness guard therefore scopes to the version half alone, so the artefact
  half still self-heals while the version is unreadable.
- The stamp lives on the state bind mount, so it survives `toolbox stop` and is
  per-host, not per-container.
- `graphify hook install` stays outside the gate. It writes into `.git/hooks/`,
  which is never committed and is therefore absent from a fresh clone, and it
  touches nothing tracked.
