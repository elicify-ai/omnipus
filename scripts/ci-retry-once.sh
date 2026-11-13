#!/usr/bin/env bash
# Run a command; if it fails, wait CI_RETRY_PAUSE seconds (default 15) and run
# it ONE more time. Exit 0 if either attempt succeeds; otherwise exit with the
# command's own non-zero status after the second failure.
#
# Used by the E2E "Install Playwright browsers" step (#1786), where a short
# network or apt-mirror blip made `playwright install` fail once. It has NO
# per-attempt timeout on purpose: killing npx could leave apt holding the dpkg
# lock and make the retry fail for the wrong reason. A hung attempt is ended by
# the step's own timeout-minutes, which covers both attempts plus the pause.
#
# Usage: scripts/ci-retry-once.sh <command> [args...]

set -u

if [ "$#" -eq 0 ]; then
  echo "ci-retry-once: no command given" >&2
  exit 2
fi

pause="${CI_RETRY_PAUSE:-15}"

"$@"
code=$?
if [ "$code" -eq 0 ]; then
  exit 0
fi

echo "::warning::'$*' failed (attempt 1 of 2, exit $code); retrying in ${pause}s"
sleep "$pause"

"$@"
code=$?
if [ "$code" -eq 0 ]; then
  exit 0
fi

echo "::error::'$*' failed (attempt 2 of 2, exit $code). A failed browser install is usually the runner network or the apt mirror, not a code failure: re-run the job."
exit "$code"
