#!/usr/bin/env bash
#
# Read the SonarQube Quality Gate for the analysis the scanner just uploaded and
# fail on a red one. On a pull request the gate and its new issues are also
# rendered into a PR comment, posted before the failure so the reason is
# readable there. Why the comment is hand-rolled rather than left to the branch
# plugin's own decoration: docs/internals/sonarqube.md.
#
# Reads the analysis identity from .scannerwork/report-task.txt, which the
# scanner writes, so the project key is never restated here.
#
# Environment:
#   SONAR_HOST_URL  server base URL (secret — never echoed)
#   SONAR_TOKEN     analysis/user token with browse permission
#   PR_NUMBER       pull request to comment on; empty selects branch mode,
#                   which gates silently — there is nothing to comment on
#   BRANCH          branch analysed; required when PR_NUMBER is empty
#   GH_TOKEN        token with pull-requests: write (unused when DRY_RUN=1)
#   GH_REPO         owner/repo (unused when DRY_RUN=1)
#   DRY_RUN         when 1, print the comment instead of posting it. Still
#                   needs PR_NUMBER or BRANCH — the scope is what is queried,
#                   not what is posted.
#
set -euo pipefail

TASK_FILE=${TASK_FILE:-.scannerwork/report-task.txt}
[ -r "$TASK_FILE" ] || { echo "no $TASK_FILE — did the scan run?" >&2; exit 1; }

ce_task_id=$(sed -n 's/^ceTaskId=//p' "$TASK_FILE")
project_key=$(sed -n 's/^projectKey=//p' "$TASK_FILE")
[ -n "$ce_task_id" ] && [ -n "$project_key" ] || { echo "malformed $TASK_FILE" >&2; exit 1; }

api() { curl -sS -u "$SONAR_TOKEN:" "$SONAR_HOST_URL$1"; }

# The scanner returns as soon as the report is uploaded; the server processes it
# asynchronously, and querying before it lands reports the *previous* analysis.
echo "waiting for the server to process the report..." >&2
deadline=$((SECONDS + 300))
while :; do
  status=$(api "/api/ce/task?id=$ce_task_id" | jq -r '.task.status')
  case "$status" in
    SUCCESS) break ;;
    FAILED|CANCELED) echo "analysis task ended as $status" >&2; exit 1 ;;
  esac
  [ "$SECONDS" -lt "$deadline" ] || { echo "timed out waiting for the analysis" >&2; exit 1; }
  sleep 5
done

# Every analysis is scoped to either a pull request or a branch, and the API
# spells that scope the same way on both endpoints. The issue search needs one
# extra term in branch mode: pullRequest= already restricts the result to what
# the PR introduced, while branch= would return the branch's entire open
# backlog — which the body below would then label "new issues".
if [ -n "${PR_NUMBER:-}" ]; then
  scope="pullRequest=$PR_NUMBER"
  issues_scope="$scope"
  scope_label="PR $PR_NUMBER"
else
  scope="branch=${BRANCH:?PR_NUMBER is empty, so BRANCH must be set}"
  issues_scope="$scope&inNewCodePeriod=true"
  scope_label="branch $BRANCH"
fi

gate=$(api "/api/qualitygates/project_status?projectKey=$project_key&$scope")
issues=$(api "/api/issues/search?componentKeys=$project_key&$issues_scope&issueStatuses=OPEN,CONFIRMED&ps=50&s=SEVERITY&asc=false")

gate_status=$(jq -r '.projectStatus.status // empty' <<<"$gate")
issue_total=$(jq -r '.total // empty' <<<"$issues")
# An error response still parses as JSON, so an absent field — not a parse
# failure — is what a rejected query looks like. Say which one failed instead
# of dying further down on an empty variable.
if [ -z "$gate_status" ] || [ -z "$issue_total" ]; then
  echo "SonarQube did not return a result for $scope_label of $project_key:" >&2
  jq -r '.errors[]?.msg // empty' <<<"$gate" >&2
  jq -r '.errors[]?.msg // empty' <<<"$issues" >&2
  exit 1
fi

{
  case "$gate_status" in
    OK)    printf '## SonarQube — Quality Gate passed\n\n' ;;
    ERROR) printf '## SonarQube — Quality Gate failed\n\n' ;;
    *)     printf '## SonarQube — Quality Gate: %s\n\n' "$gate_status" ;;
  esac

  # Conditions, worst first: a failing one is what the reader needs, so it leads.
  printf '| Condition | Value | Threshold | |\n|---|---|---|---|\n'
  jq -r '
    # `label` is a jq keyword, hence the name.
    def metricName:
      { new_coverage: "Coverage on new code",
        new_duplicated_lines_density: "Duplicated lines on new code",
        new_violations: "New issues",
        new_security_hotspots_reviewed: "Security hotspots reviewed",
        new_reliability_rating: "Reliability rating on new code",
        new_security_rating: "Security rating on new code",
        new_maintainability_rating: "Maintainability rating on new code"
      }[.] // .;
    .projectStatus.conditions
    | sort_by(.status == "OK")
    | .[]
    | "| \(.metricKey | metricName) | \(.actualValue) | \(.comparator | if . == "GT" then "≤ " else "≥ " end)\(.errorThreshold) | \(if .status == "OK" then "✅" else "❌" end) |"
  ' <<<"$gate"

  if [ "$issue_total" -eq 0 ]; then
    printf '\nNo new issues.\n'
  else
    printf '\n### %s new issue(s)\n\n' "$issue_total"
    jq -r '
      .issues[]
      | "- **\(.component | sub("^[^:]*:"; ""))**\(if .line then ":\(.line)" else "" end) — \(.message)  \n  `\(.rule)` · \(.severity)"
    ' <<<"$issues"
    shown=$(jq -r '.issues | length' <<<"$issues")
    [ "$issue_total" -gt "$shown" ] && printf '\n_Showing %s of %s._\n' "$shown" "$issue_total"
  fi

  # Deliberately no link: the project is private, so a URL would only leak the
  # server's hostname into a public repository without helping the reader.
  if [ "$gate_status" = "OK" ]; then
    printf '\n_Quality Gate passed._\n'
  elif [ -n "${PR_NUMBER:-}" ]; then
    printf '\n_The Quality Gate blocks this merge until the failing conditions above are met._\n'
  else
    printf '\n_The Quality Gate on %s is failing._\n' "$scope_label"
  fi
} > sonar-comment.md

if [ "${DRY_RUN:-0}" = "1" ]; then
  cat sonar-comment.md
elif [ -n "${PR_NUMBER:-}" ]; then
  gh pr comment "$PR_NUMBER" --create-if-none --edit-last --body-file sonar-comment.md
else
  # Branch mode: nothing to comment on, so the rendered body goes to the job log
  # — a red `main` should still say *what* went red without a dashboard link.
  cat sonar-comment.md
fi

# Comment first, then fail: the reason has to be readable on the PR before the
# job goes red, or the red check is all the author gets. A dry run takes the
# same exit path, so it exercises this branch instead of always reporting
# success.
if [ "$gate_status" != "OK" ]; then
  echo "Quality Gate is $gate_status — failing the job." >&2
  exit 1
fi
