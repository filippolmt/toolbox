#!/usr/bin/env bash
#
# Post the SonarQube result of a pull-request analysis as a PR comment.
#
# Why this exists instead of the community branch plugin's own decoration: this
# repository is public while the SonarQube project is private, so the plugin's
# comment and check both link to a dashboard an outside reader cannot open, and
# publish the server's hostname to do it. Rendering the findings here keeps the
# host out of the PR entirely and puts the actual issues in front of the reader
# rather than a link.
#
# Reads the analysis identity from .scannerwork/report-task.txt, which the
# scanner writes, so the project key is never restated here.
#
# Environment:
#   SONAR_HOST_URL  server base URL (secret — never echoed)
#   SONAR_TOKEN     analysis/user token with browse permission
#   PR_NUMBER       pull request to comment on
#   GH_TOKEN        token with pull-requests: write (unused when DRY_RUN=1)
#   GH_REPO         owner/repo (unused when DRY_RUN=1)
#   DRY_RUN         when 1, print the comment instead of posting it
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

gate=$(api "/api/qualitygates/project_status?projectKey=$project_key&pullRequest=$PR_NUMBER")
issues=$(api "/api/issues/search?componentKeys=$project_key&pullRequest=$PR_NUMBER&issueStatuses=OPEN,CONFIRMED&ps=50&s=SEVERITY&asc=false")

gate_status=$(jq -r '.projectStatus.status' <<<"$gate")
issue_total=$(jq -r '.total' <<<"$issues")

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
  printf '\n_Sonar is informational here and does not block the merge._\n'
} > sonar-comment.md

if [ "${DRY_RUN:-0}" = "1" ]; then
  cat sonar-comment.md
  exit 0
fi

gh pr comment "$PR_NUMBER" --create-if-none --edit-last --body-file sonar-comment.md
