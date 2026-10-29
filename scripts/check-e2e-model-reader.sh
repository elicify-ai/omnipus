#!/usr/bin/env bash
# check-e2e-model-reader.sh — runs the tests of scripts/e2e-model.sh in CI.
#
# scripts/e2e-model.sh is the one shell reader of tests/e2e/e2e-model.json
# and the CI job-start check (preflight). Its behaviour is pinned by two test
# scripts that no workflow ran on their own:
#   scripts/e2e-model.test.sh        the contract cases (shape rules, env
#                                    lines, two-row seed, preflight routes)
#   scripts/e2e-model-extra.test.sh  the hardening cases (loopback-only test
#                                    URL, --strict 401/403 and 2xx body
#                                    checks, mid-run fallback notes, temp-dir
#                                    cleanup)
# This guard runs both, so scripts/guards.sh (make lint-guards, the pr.yml
# guard job, runci.sh run_lint) fails when the reader regresses.
#
# Needs bash, jq, curl and python3 (the tests start one-shot local stubs; no
# request leaves the machine, every key is a fake).
#
# Exit: 0 both test scripts passed; 1 a test script failed; 2 the check could
# not run (a test script or the reader is missing).
#
# Override (for the companion check-e2e-model-reader.test.sh only):
#   REPO_ROOT — the tree whose scripts/ is tested.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"

for f in scripts/e2e-model.sh scripts/e2e-model.test.sh scripts/e2e-model-extra.test.sh; do
  if [ ! -f "$REPO_ROOT/$f" ]; then
    echo "check-e2e-model-reader: $f not found under $REPO_ROOT — refusing a green verdict for tests that never ran" >&2
    exit 2
  fi
done

failed=0
for t in e2e-model.test.sh e2e-model-extra.test.sh; do
  log="$(mktemp "${TMPDIR:-/tmp}/check-e2e-model-reader.XXXXXX")" || { echo "check-e2e-model-reader: mktemp failed" >&2; exit 2; }
  if bash "$REPO_ROOT/scripts/$t" >"$log" 2>&1; then
    echo "check-e2e-model-reader: $t PASS"
  else
    echo "check-e2e-model-reader: $t FAILED:" >&2
    sed 's/^/  /' "$log" >&2
    failed=1
  fi
  rm -f "$log"
done

if [ "$failed" -ne 0 ]; then
  echo "check-e2e-model-reader: FAILED — scripts/e2e-model.sh no longer meets its tests" >&2
  exit 1
fi
echo "check-e2e-model-reader: OK — scripts/e2e-model.sh passes its contract and hardening tests"
exit 0
