#!/usr/bin/env bash
# check-ci-cluster-dispatcher.sh
#
# Runs deploy/ci-worker/ci-cluster-selfcheck.sh - the fake-`fly` proof that
# ci-cluster.sh never stops a machine it did not start, refuses an unreadable
# machine state, and fails a tier whose machine dies. Until this wrapper existed
# nothing ran that selfcheck, so a regression in the dispatcher's ownership logic
# stayed green until a session's run was killed (2026-10-11). guards.sh
# discovers this file; its proof-of-failure companion is
# check-ci-cluster-dispatcher.test.sh.
#
# Exit: 0 selfcheck passed, 1 selfcheck failed, 2 selfcheck script missing.
#
# Overrides (for the companion test only): REPO_ROOT. CI_CLUSTER_UNDER_TEST is
# passed through to the selfcheck, which uses it to point at another dispatcher.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
SELFCHECK="$REPO_ROOT/deploy/ci-worker/ci-cluster-selfcheck.sh"

if [ ! -f "$SELFCHECK" ]; then
  echo "check-ci-cluster-dispatcher: missing $SELFCHECK - a guard that cannot find its check must not pass" >&2
  exit 2
fi

if ! bash "$SELFCHECK"; then
  echo "check-ci-cluster-dispatcher: FAIL - ci-cluster.sh no longer behaves (see the selfcheck lines above)" >&2
  exit 1
fi
echo "check-ci-cluster-dispatcher: OK"
exit 0
