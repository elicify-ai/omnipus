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
# want <n>: run mutant test n. GUARD_TESTS="4 6" runs only those (each mutant test is a
# full selfcheck run, so the whole file takes longer than 20 minutes); unset runs them all.
want() { [ -z "${GUARD_TESTS:-}" ] || [[ " $GUARD_TESTS " == *" $1 "* ]]; }
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

if want 4; then
echo "Test 4: a dispatcher whose watchdog never probes is caught by selfcheck case 8, for the intended reason"
# The 2026-10-18 hang: with no probing the dead-but-open console is never ended. The selfcheck's
# fake console never ends by itself, so the mutant must fail on the case's own 40s cap
# assertion, not on a setup/syntax error.
MUT4_DIR="$TMP_DIR/mutant4/deploy/ci-worker"
mkdir -p "$MUT4_DIR"
cp "$REPO/deploy/ci-worker/runci.sh" "$MUT4_DIR/runci.sh"
# shellcheck disable=SC2016 # literal sed pattern, not a shell expansion
sed 's|\[ "\$watched" -lt "\$WATCH_INTERVAL" \] && continue|continue|' \
  "$REPO/deploy/ci-worker/ci-cluster.sh" > "$MUT4_DIR/ci-cluster.sh"
if cmp -s "$REPO/deploy/ci-worker/ci-cluster.sh" "$MUT4_DIR/ci-cluster.sh"; then
  echo "  FAIL [mutant4-built]: the mutation did not change ci-cluster.sh, so this test proves nothing"
  FAIL=$((FAIL + 1))
else
  SELFCHECK_HANG_CAP=20 GIT_DIR="$GIT_DIR_ABS" CI_CLUSTER_UNDER_TEST="$MUT4_DIR/ci-cluster.sh" \
    bash "$GUARD" >"$TMP_DIR/mutant4.out" 2>&1; rc=$?
  check "mutant4-no-watchdog" 1 "$rc"
  if grep -q 'selfcheck FAIL: (8) expected STALLED exit 1 within the cap, got 143' "$TMP_DIR/mutant4.out" \
    && ! grep -q -e 'missing' -e 'cannot resolve ref' -e 'syntax error' "$TMP_DIR/mutant4.out"; then
    echo "  PASS [mutant4-reason]: failed on case 8's hang assertion, as intended"; PASS=$((PASS + 1))
  else
    echo "  FAIL [mutant4-reason]: the mutant failed for some other reason:"; sed 's/^/    | /' "$TMP_DIR/mutant4.out" | tail -20
    FAIL=$((FAIL + 1))
  fi
fi

fi

if want 5; then
echo "Test 5: a runci.sh without its HUP/INT/TERM traps is caught by selfcheck case 11, for the intended reason"
# The selfcheck takes the trap statements from runci.sh itself and runs them in a child, so a
# runner whose traps were deleted must fail the case's named assertions (not a setup error).
MUT5_DIR="$TMP_DIR/mutant5/deploy/ci-worker"
mkdir -p "$MUT5_DIR"
cp "$REPO/deploy/ci-worker/ci-cluster.sh" "$REPO/deploy/ci-worker/ci-cluster-selfcheck.sh" "$MUT5_DIR/"
sed -E "/^trap '.*' (HUP|INT|TERM)\$/d" "$REPO/deploy/ci-worker/runci.sh" > "$MUT5_DIR/runci.sh"
if cmp -s "$REPO/deploy/ci-worker/runci.sh" "$MUT5_DIR/runci.sh"; then
  echo "  FAIL [mutant5-built]: the mutation did not change runci.sh, so this test proves nothing"
  FAIL=$((FAIL + 1))
else
  GIT_DIR="$GIT_DIR_ABS" REPO_ROOT="$TMP_DIR/mutant5" bash "$GUARD" >"$TMP_DIR/mutant5.out" 2>&1; rc=$?
  check "mutant5-no-traps" 1 "$rc"
  if grep -q 'selfcheck FAIL: (11) runci.sh does not carry exactly the three HUP/INT/TERM trap statements' "$TMP_DIR/mutant5.out" \
    && grep -q 'selfcheck FAIL: (11) the real runci.sh wrote no KILLED trailer for SIGHUP' "$TMP_DIR/mutant5.out" \
    && ! grep -v '^selfcheck FAIL: (18)' "$TMP_DIR/mutant5.out" | grep -q -e 'missing' -e 'cannot resolve ref' -e 'syntax error'; then
    echo "  PASS [mutant5-reason]: failed on case 11's trap assertions, as intended"; PASS=$((PASS + 1))
  else
    echo "  FAIL [mutant5-reason]: the mutant failed for some other reason:"; sed 's/^/    | /' "$TMP_DIR/mutant5.out" | tail -20
    FAIL=$((FAIL + 1))
  fi
fi

fi

# mutant_tree <n> <file-to-mutate> <sed-expr>: copy the three worker scripts into
# $TMP_DIR/mutant<n>/deploy/ci-worker, apply the sed expression to <file-to-mutate>
# only, and fail the test if the copy is identical (a mutation that changes nothing proves nothing).
mutant_tree() {
  local n="$1" target="$2" expr="$3" d="$TMP_DIR/mutant$1/deploy/ci-worker"
  mkdir -p "$d"
  cp "$REPO/deploy/ci-worker/ci-cluster.sh" "$REPO/deploy/ci-worker/ci-cluster-selfcheck.sh" "$REPO/deploy/ci-worker/runci.sh" "$d/"
  sed -E "$expr" "$REPO/deploy/ci-worker/$target" > "$d/$target.new" && mv "$d/$target.new" "$d/$target"
  if cmp -s "$REPO/deploy/ci-worker/$target" "$d/$target"; then
    echo "  FAIL [mutant$n-built]: the mutation did not change $target, so this test proves nothing"
    FAIL=$((FAIL + 1)); return 1
  fi
  GIT_DIR="$GIT_DIR_ABS" REPO_ROOT="$TMP_DIR/mutant$n" bash "$GUARD" >"$TMP_DIR/mutant$n.out" 2>&1; rc=$?
  return 0
}
# expect_named <n> <label> <fixed-string>...: the mutant exited 1 and printed every named line
expect_named() {
  local n="$1" label="$2" ok=1 want; shift 2
  check "$label-exit" 1 "$rc"
  for want in "$@"; do grep -qF -e "$want" "$TMP_DIR/mutant$n.out" || ok=0; done
  if [ "$ok" -eq 1 ] && ! grep -v '^selfcheck FAIL: (18)' "$TMP_DIR/mutant$n.out" | grep -q -e 'missing' -e 'cannot resolve ref' -e 'syntax error'; then
    echo "  PASS [$label-reason]: failed on the named assertion(s), as intended"; PASS=$((PASS + 1))
  else
    echo "  FAIL [$label-reason]: the mutant did not fail on the named assertion(s):"; sed 's/^/    | /' "$TMP_DIR/mutant$n.out" | tail -20
    FAIL=$((FAIL + 1))
  fi
}

echo "Test 6: a runner that resets its traps at run time passes case 11 but is caught by case 18 (the actual runner)"
if want 6 && mutant_tree 6 runci.sh "/^trap '.*' TERM\$/a trap - HUP INT TERM"; then
  expect_named 6 mutant6-trap-reset "selfcheck FAIL: (18) the ACTUAL runci.sh wrote no KILLED trailer on SIGHUP" \
    "selfcheck FAIL: (18) the ACTUAL runci.sh wrote no KILLED trailer on SIGINT" \
    "selfcheck FAIL: (18) the ACTUAL runci.sh wrote no KILLED trailer on SIGTERM"
  if grep -q 'selfcheck FAIL: (11)' "$TMP_DIR/mutant6.out"; then
    echo "  FAIL [mutant6-case11-blind]: case 11 caught the reset itself, so the mutant no longer shows case 18 is needed"; FAIL=$((FAIL + 1))
  else
    echo "  PASS [mutant6-case11-blind]: case 11 alone does not see the reset; case 18 does"; PASS=$((PASS + 1))
  fi
fi
echo "Test 7: the original shared run-log behaviour (no attempt or pid in the name, no noclobber create) is caught by case 19"
echo "Test 7: a runner log name that omits the attempt and pid (the same-second collision) is caught by case 19"
if want 7 && mutant_tree 7 runci.sh 's/-\$\{safe_attempt\}-\$\$\.log/.log/;/^\( set -C; : > /d'; then
  expect_named 7 mutant7-shared-log "selfcheck FAIL: (19) two attempts in the same second share one run-log path"
fi

echo "Test 8: a dispatcher that accepts a log with two attempt identities is caught by case 20"
if want 8 && mutant_tree 8 ci-cluster.sh 's/-gt 1 \]/-gt 99 ]/'; then
  expect_named 8 mutant8-mixed-log "selfcheck FAIL: (20a) an old attempt's PASS certified the unfinished current attempt" \
    "selfcheck FAIL: (20b) mixed-attempt evidence was accepted as verified"
fi

echo "Test 9: the whole selfcheck passes when it inherits RUNCI_AS_CI=1 (as inside the Fly lint gate)"
if want 9; then
  RUNCI_AS_CI=1 GIT_DIR="$GIT_DIR_ABS" bash "$GUARD" >"$TMP_DIR/inherit.out" 2>&1; rc=$?
  check "inherited-RUNCI_AS_CI" 0 "$rc"
fi

echo "Test 10: a selfcheck that does not clear RUNCI_AS_CI is caught when the variable is inherited"
if want 10 && RUNCI_AS_CI=1 mutant_tree 10 ci-cluster-selfcheck.sh '/^unset RUNCI_AS_CI$/d;s/env -u RUNCI_AS_CI //'; then
  expect_named 10 mutant10-inherited-env "selfcheck FAIL: (19) fixture: a prologue run announced no RUNLOG"
fi

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
[ "$FAIL" -eq 0 ] || exit 1
echo "All assertions passed."
exit 0
