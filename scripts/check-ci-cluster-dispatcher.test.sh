#!/usr/bin/env bash
# check-ci-cluster-dispatcher.test.sh
#
# Proof-of-failure companion for check-ci-cluster-dispatcher.sh. A guard that
# cannot fail is no guard (docs/internal/false-green-patterns.md): this proves
# the wrapper goes RED on a dispatcher with the 2026-10-11 bug (the machine
# state read through a flag flyctl does not have), GREEN on the real one, and
# exits 2 when the selfcheck it wraps is missing.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
GUARD="$SCRIPT_DIR/check-ci-cluster-dispatcher.sh"
TMP_DIR="$(mktemp -d /tmp/ci-cluster-dispatcher-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

PASS=0
FAIL=0
check() { # label expected actual
  if [ "$3" -eq "$2" ]; then
    echo "  PASS [$1]: exit $3 (expected $2)"; PASS=$((PASS + 1))
  else
    echo "  FAIL [$1]: exit $3 (expected $2)"; FAIL=$((FAIL + 1))
  fi
}

echo "=== check-ci-cluster-dispatcher self-test ==="

echo "Test 1: the real dispatcher passes"
bash "$GUARD" >"$TMP_DIR/real.out" 2>&1; rc=$?
[ "$rc" -eq 0 ] || sed 's/^/    | /' "$TMP_DIR/real.out"
check "real-dispatcher" 0 "$rc"

echo "Test 2: a dispatcher that reads the machine state with --json is caught"
mkdir -p "$TMP_DIR/mutant"
# shellcheck disable=SC2016 # the pattern is a literal sed regex, not a shell expansion
sed 's|fly machine status "\$1" --app "\$APP" 2>/dev/null|fly machine status "$1" --app "$APP" --json 2>/dev/null|' \
  "$REPO/deploy/ci-worker/ci-cluster.sh" > "$TMP_DIR/mutant/ci-cluster.sh"
if cmp -s "$REPO/deploy/ci-worker/ci-cluster.sh" "$TMP_DIR/mutant/ci-cluster.sh"; then
  echo "  FAIL [mutant-built]: the mutation did not change ci-cluster.sh, so this test proves nothing"
  FAIL=$((FAIL + 1))
else
  CI_CLUSTER_UNDER_TEST="$TMP_DIR/mutant/ci-cluster.sh" bash "$GUARD" >/dev/null 2>&1; check "mutant-dispatcher" 1 $?
fi

echo "Test 3: a tree without the selfcheck exits 2 (never a silent green)"
mkdir -p "$TMP_DIR/empty"
REPO_ROOT="$TMP_DIR/empty" bash "$GUARD" >/dev/null 2>&1; check "missing-selfcheck" 2 $?

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
echo "All assertions passed."
exit 0
