# SonarQube

Static analysis and the coverage gate. The moving parts:

| Piece | Lives in |
|---|---|
| Analysis scope, rule exclusions | [`sonar-project.properties`](../../sonar-project.properties) |
| Reachability check, scan, gate check | [`.github/workflows/sonar.yml`](../../.github/workflows/sonar.yml) |
| Gate reading + PR comment rendering | [`.github/scripts/sonar-gate.sh`](../../.github/scripts/sonar-gate.sh) |
| Unconditional coverage floor | [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) (`test` job) |
| Quality Gate thresholds | SonarQube server (not in this repo — see below) |

## Private project, public repo

The repository is public; the SonarQube project is not, and the server is
self-hosted on a machine that is only powered up 09:00–19:00 Europe/Rome,
Mon–Fri.

That combination rules out the community branch plugin's own PR decoration: its
comment and its check both link to a dashboard an outside reader cannot open,
and they publish the server's hostname to do it. So the decoration is
hand-rolled instead — `sonar-gate.sh` reads the gate over the API and renders
the findings themselves into the comment body, with no link at all. The
hostname reaches CI only through `secrets.SONAR_HOST_URL` and is re-masked with
`::add-mask::` in every job that touches it, because the scanner logs it in
normalised forms GitHub's own redaction does not match.

This rationale belongs here and nowhere else. The workflow header and the
script header each carry one line and a pointer back.

## The two coverage numbers

They measure different things, and the difference is not a bug:

| | Denominator | Threshold | Enforced by |
|---|---|---|---|
| **Sonar** ~80.5% | lines the PR touched (*new code*) | 80% | server-side Quality Gate |
| **Local** ~75.6% | every statement in the tree | 74% | `ci.yml`, `Enforce the coverage floor` |

`go test ./... -coverprofile` counts every package, including ones with no test
file at all; Sonar scores only new code, against the exclusions in
`sonar-project.properties`. Comparing the two numbers directly means nothing.

**Why both exist.** `analyze` is a required check on `main` and goes red when
the gate does, which is what makes 80%-on-new-code a merge blocker. But
`preflight` *skips* `analyze` when the server is unreachable, and GitHub counts
a skipped required check as satisfied — deliberately, so the server's schedule
never blocks a merge. On its own that leaves a PR pushed at 3am with no
coverage gate whatsoever. The floor in `ci.yml` closes that hole: it needs no
server, runs on every PR, and always reports.

The floor sits a couple of points under the current total deliberately. Pinned
to today's exact figure it would go red on the first sizeable untested
addition — on whichever PR happens to make it, with an error naming the global
total rather than that PR's own code. Raise it as the total climbs. Never lower
it to turn a red build green.

The 80% itself cannot be pinned repo-side — Quality Gate conditions are server
state, and `sonar-project.properties` has no key for them. The floor is the
repo-side backstop; the gate condition is documented here so a change to it is
at least reviewable against something.

## Where the gate is read

`sonar-gate.sh` runs after every scan, in one of two modes:

- **`PR_NUMBER` set** — pull-request scope. Renders the gate table and the new
  issues into a PR comment (created once, edited in place on later pushes),
  *then* fails on a red gate. That order matters: a red check with no comment
  gives the author nothing to act on.
- **`PR_NUMBER` empty** — branch scope (`push` to `main`, and the weekday cron).
  The same body goes to the job log instead of a comment, and a red gate still
  fails the job, so a red `main` is not silent. The issue search adds
  `inNewCodePeriod=true` here: `pullRequest=` already narrows the result to what
  the PR introduced, but `branch=` on its own would return the branch's whole
  open backlog under a "new issues" heading.

Fork PRs get no secrets, so `preflight` skips the whole job before this runs.
That is why the step needs no fork guard of its own.

`DRY_RUN=1` prints the comment instead of posting it and takes the same exit
path, so a local run exercises the failure branch rather than always reporting
success. It still needs `PR_NUMBER` or `BRANCH` exported — the scope selects
what is queried, not what is posted.

## Cognitive Complexity is off for test files

`sonar.exclusions` drops `**/*_test.go` from *sources*, but `sonar.tests` +
`sonar.test.inclusions` re-index them as test code, so rules still fire there.
`go:S3776` is the one rule that measures nothing real on a Go test: a
table-driven test is a loop over cases plus nested `t.Run` and assert blocks, so
the score tracks how many cases the table covers, not how hard the test is to
read. Splitting a table to satisfy the threshold costs the at-a-glance view of
the cases and buys nothing. Scoped off via `multicriteria.e1`; every other rule,
and coverage, still applies.

There is no `NOSONAR` anywhere in the tree, and no rule is suppressed for
production code.

## Baseline: the 56 → 0 sweep

The gate was adopted against a backlog of 56 open maintainability findings,
cleared in `690a7ea refactor: resolve the SonarQube maintainability findings`
(PR [#703](https://github.com/filippolmt/toolbox/pull/703)) — 62 functions
extracted across 25 Go files, no behaviour change.

Recorded here because the SonarQube instance is private and its issue history
is not public: without this note the sweep is unverifiable from the repository
alone. The durable evidence is the commit itself plus the green gate on every
run since; if the count has to be re-established, a fresh scan of the merge-base
is the only way.

The same PR added tests to `internal/bridge`, `internal/configui` and
`internal/dockertest`. They were not requested by the refactor — they exist to
hold the coverage gate up, since a 62-function extraction moves statements
around and an untested package drags the total under the floor. They stay.
