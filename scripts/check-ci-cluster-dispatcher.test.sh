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

echo "Test 2: a dispatcher that reads the machine state with --json is caught, for the intended reason"
# The mutant must live in a tree shaped like the repo (deploy/ci-worker/ci-cluster.sh next to
# runci.sh, two levels under a root git can resolve refs in), or ci-cluster.sh dies with exit 2
# on a missing runci.sh BEFORE it reaches the mutated line and the test proves nothing.
MUT_DIR="$TMP_DIR/mutant/deploy/ci-worker"
mkdir -p "$MUT_DIR" "$TMP_DIR/control/deploy/ci-worker"
cp "$REPO/deploy/ci-worker/runci.sh" "$MUT_DIR/runci.sh"
cp "$REPO/deploy/ci-worker/runci.sh" "$TMP_DIR/control/deploy/ci-worker/runci.sh"
cp "$REPO/deploy/ci-worker/ci-cluster.sh" "$TMP_DIR/control/deploy/ci-worker/ci-cluster.sh"
# shellcheck disable=SC2016 # the pattern is a literal sed regex, not a shell expansion
sed 's|fly machine status "\$1" --app "\$APP" 2>/dev/null|fly machine status "$1" --app "$APP" --json 2>/dev/null|' \
  "$REPO/deploy/ci-worker/ci-cluster.sh" > "$MUT_DIR/ci-cluster.sh"
GIT_DIR_ABS="$(git -C "$REPO" rev-parse --absolute-git-dir)"
if cmp -s "$REPO/deploy/ci-worker/ci-cluster.sh" "$MUT_DIR/ci-cluster.sh"; then
  echo "  FAIL [mutant-built]: the mutation did not change ci-cluster.sh, so this test proves nothing"
  FAIL=$((FAIL + 1))
else
  # Control: an UNMUTATED copy in the same layout must pass, or the layout (not the mutation) is what fails.
  GIT_DIR="$GIT_DIR_ABS" CI_CLUSTER_UNDER_TEST="$TMP_DIR/control/deploy/ci-worker/ci-cluster.sh" \
    bash "$GUARD" >"$TMP_DIR/control.out" 2>&1; rc=$?
  [ "$rc" -eq 0 ] || sed 's/^/    | /' "$TMP_DIR/control.out"
  check "unmutated-copy-in-mutant-layout" 0 "$rc"
  GIT_DIR="$GIT_DIR_ABS" CI_CLUSTER_UNDER_TEST="$MUT_DIR/ci-cluster.sh" \
    bash "$GUARD" >"$TMP_DIR/mutant.out" 2>&1; rc=$?
  check "mutant-dispatcher" 1 "$rc"
  # The reason, not just the exit code: the dispatcher reached the mutated state read, got no
  # state back (the fake flyctl rejects --json), and refused the machine.
  if grep -q 'selfcheck FAIL: (1) expected exit 0, got 2: .*cannot read the state of machine' "$TMP_DIR/mutant.out" \
    && ! grep -q -e 'missing' -e 'cannot resolve ref' -e 'md5' "$TMP_DIR/mutant.out"; then
    echo "  PASS [mutant-reason]: failed on the unreadable machine state, as intended"; PASS=$((PASS + 1))
  else
    echo "  FAIL [mutant-reason]: the mutant failed for some other reason:"; sed 's/^/    | /' "$TMP_DIR/mutant.out"
    FAIL=$((FAIL + 1))
  fi
fi

echo "Test 3: a tree without the selfcheck exits 2 (never a silent green)"
mkdir -p "$TMP_DIR/empty"
REPO_ROOT="$TMP_DIR/empty" bash "$GUARD" >/dev/null 2>&1; check "missing-selfcheck" 2 $?

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
echo "All assertions passed."
exit 0
