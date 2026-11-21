#!/usr/bin/env bash
# Says plainly, in a normal Go job, which Office preview tests it hands over to
# the dedicated job. `go test` hides a skip's reason unless it runs with -v, so
# the job annotation and summary carry the statement.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
job="${OMNIPUS_OFFICE_REAL_TESTS_JOB:?OMNIPUS_OFFICE_REAL_TESTS_JOB must name the dedicated job}"
names="$(awk '!/^[[:space:]]*(#|$)/ {print $2}' "$here/real-tests.txt")"
if [ -z "$names" ]; then
  echo "office-real: real-tests.txt lists no tests" >&2
  exit 1
fi
count="$(printf '%s\n' "$names" | wc -l | tr -d ' ')"
message="$count Office preview tests need real LibreOffice or the real PDF renderer. They are not run here: they belong to the job \"$job\", which fails the PR unless each one passes."
echo "::notice title=Office preview tests handed over::$message"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    printf '## Office preview tests handed over\n\n%s\n\n' "$message"
    # shellcheck disable=SC2016 # the backticks are Markdown
    printf '%s\n' "$names" | sed 's/.*/- `&`/'
  } >> "$GITHUB_STEP_SUMMARY"
fi
