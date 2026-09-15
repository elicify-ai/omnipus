#!/usr/bin/env bash
# scripts/guards.test.sh
#
# Mutation proof that scripts/guards.sh — the guard RUNNER itself — can go
# red. This is not a feature guard's own proof-of-failure companion; it is
# the L2 gate in docs/internal/specs/adr-084-086-joint-delivery-plan.md §7:
# "the mutation proof that the guard runner can go red. Run this before
# believing any guard verdict. It is meaningless to run L3 first."
#
# Method: each case builds a throwaway REPO_ROOT with its own synthetic
# scripts/ directory (fixture guards, fixture companions, fixture
# quarantine/exempt files) and invokes the REAL scripts/guards.sh against it
# via the REPO_ROOT override. Nothing here ever reads or writes the repo's
# real scripts/ directory. Every case plants either an offender (something
# that must make guards.sh exit non-zero) or a clean tree (something that
# must make it exit zero) and asserts the observed exit code, per R-19(b).
#
# Usage: bash scripts/guards.test.sh
# Exit code: 0 if every assertion passed, 1 if any failed.

set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
GUARDS_SH="$HERE/guards.sh"

PASS=0
FAIL=0
ERRORS=()

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [ "$actual" -eq "$expected" ]; then
    echo "  PASS [$label]: exit $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: exit $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected exit $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" haystack="$3"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_file_exists() {
  local label="$1" path="$2"
  if [ -f "$path" ]; then
    echo "  PASS [$label]: $path exists"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: $path does not exist"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected $path to exist")
  fi
}

assert_file_absent() {
  local label="$1" path="$2"
  if [ ! -f "$path" ]; then
    echo "  PASS [$label]: $path absent, as expected"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: $path unexpectedly exists"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected $path to be absent")
  fi
}

# ─── fixture builders ────────────────────────────────────────────────────────
fresh_root() {
  local root
  root="$(mktemp -d /tmp/guards-test.XXXXXX)"
  mkdir -p "$root/scripts"
  printf '%s\n' "$root"
}

# write_guard ROOT NAME EXIT_CODE [MARKER_RELATIVE_TO_ROOT]
write_guard() {
  local root="$1" name="$2" code="$3" marker="${4:-}"
  {
    echo '#!/usr/bin/env bash'
    echo 'set -u'
    if [ -n "$marker" ]; then
      echo "touch \"$root/$marker\""
    fi
    echo "exit $code"
  } > "$root/scripts/$name"
  chmod +x "$root/scripts/$name"
}

# write_self_test_guard ROOT NAME SELFTEST_EXIT NORMAL_EXIT [MARKER]
# A guard that implements --self-test itself (no separate companion file).
write_self_test_guard() {
  local root="$1" name="$2" selftest_code="$3" normal_code="$4" marker="${5:-}"
  {
    echo '#!/usr/bin/env bash'
    echo 'set -u'
    if [ -n "$marker" ]; then
      echo "touch \"$root/$marker\""
    fi
    echo 'if [ "${1:-}" = "--self-test" ]; then'
    echo "  exit $selftest_code"
    echo 'fi'
    echo "exit $normal_code"
  } > "$root/scripts/$name"
  chmod +x "$root/scripts/$name"
}

# run_guards ROOT — prints the exit code, leaves full output in $ROOT/.out.log
run_guards() {
  local root="$1"
  REPO_ROOT="$root" bash "$GUARDS_SH" > "$root/.out.log" 2>&1
  echo $?
}

echo "=== scripts/guards.test.sh ==="
echo ""

# ── T1: zero guards discovered → the runner fails (planted offender) ───────
echo "T1: an empty scripts/ directory fails the runner"
ROOT="$(fresh_root)"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t1-exit" 1 "$CODE"
assert_output_contains "t1-message" "zero guards discovered" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T2: one guard + passing .test.sh companion → clean tree, passes ────────
echo ""
echo "T2: a guard with a passing .test.sh companion passes (clean tree)"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo.test.sh" 0
CODE="$(run_guards "$ROOT")"
assert_exit_code "t2-exit" 0 "$CODE"
assert_output_contains "t2-guard-line" "check-foo.sh exit=0" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T3: the guard itself fails → the runner fails (planted offender) ───────
echo ""
echo "T3: a guard that exits non-zero fails the runner"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 1
write_guard "$ROOT" "check-foo.test.sh" 0
CODE="$(run_guards "$ROOT")"
assert_exit_code "t3-exit" 1 "$CODE"
assert_output_contains "t3-guard-line" "check-foo.sh exit=1" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T4: companion fails though the guard itself passes → runner still fails ─
echo ""
echo "T4: a failing companion fails the runner even when the guard itself passes"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo.test.sh" 1
CODE="$(run_guards "$ROOT")"
assert_exit_code "t4-exit" 1 "$CODE"
rm -rf "$ROOT"

# ── T5: guard with no companion, not exempt → runner fails ─────────────────
echo ""
echo "T5: a guard with no companion and no exemption fails the runner"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-bare.sh" 0
CODE="$(run_guards "$ROOT")"
assert_exit_code "t5-exit" 1 "$CODE"
assert_output_contains "t5-message" "no proof-of-failure companion" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T6: guard with no companion but ON the exemption list → passes ─────────
echo ""
echo "T6: a guard with no companion that IS on the exemption list passes (clean tree)"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-bare.sh" 0
printf 'check-bare.sh\n' > "$ROOT/scripts/guards-no-selftest.exempt"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t6-exit" 0 "$CODE"
rm -rf "$ROOT"

# ── T7: a quarantined guard is discovered but never executed ───────────────
echo ""
echo "T7: a quarantined guard is never executed"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-danger.sh" 1 "danger.ran"
printf 'check-danger.sh\n' > "$ROOT/scripts/guards.quarantine"
CODE="$(run_guards "$ROOT")"
# The only candidate is quarantined, so zero guards remain — same failure
# mode as T1, and the quarantine line proves it was seen, not silently lost.
assert_exit_code "t7-exit" 1 "$CODE"
assert_file_absent "t7-not-run" "$ROOT/danger.ran"
assert_output_contains "t7-quarantine-line" "QUARANTINED: check-danger.sh" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T8: quarantine excludes only the named guard; the rest still runs ──────
echo ""
echo "T8: quarantine excludes only the named guard — a real guard alongside it still runs"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-danger.sh" 1 "danger.ran"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo.test.sh" 0
printf 'check-danger.sh\n' > "$ROOT/scripts/guards.quarantine"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t8-exit" 0 "$CODE"
assert_file_absent "t8-danger-not-run" "$ROOT/danger.ran"
assert_output_contains "t8-guards-count" "guards resolved (companions subtracted): 1" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T9: companion subtraction — guard + its .test.sh count as ONE guard ────
echo ""
echo "T9: a guard plus its .test.sh companion count as one guard, not two"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo.test.sh" 0
CODE="$(run_guards "$ROOT")"
assert_output_contains "t9-count" "guards resolved (companions subtracted): 1" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T10: a -selfcheck.sh companion is discovered, subtracted, and run ──────
echo ""
echo "T10: a -selfcheck.sh companion runs and is subtracted from the guard count"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo-selfcheck.sh" 0 "selfcheck.ran"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t10-exit" 0 "$CODE"
assert_file_exists "t10-companion-ran" "$ROOT/selfcheck.ran"
assert_output_contains "t10-count" "guards resolved (companions subtracted): 1" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T11: both a .test.sh file AND a --self-test flag resolve → BOTH run ────
# Mirrors the real check-no-handwritten-wire-types.sh, which has both today
# (C-93 rule 2: "when both companions resolve for one guard, run both").
echo ""
echo "T11: when both a .test.sh companion and a --self-test flag resolve, both run"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.test.sh" 0 "dottest.ran"
write_self_test_guard "$ROOT" "check-foo.sh" 0 0 "flag.ran"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t11-exit" 0 "$CODE"
assert_file_exists "t11-dottest-ran" "$ROOT/dottest.ran"
assert_file_exists "t11-flag-ran" "$ROOT/flag.ran"
rm -rf "$ROOT"

# ── T12: a failing --self-test flag companion fails the runner ─────────────
echo ""
echo "T12: a failing --self-test flag companion fails the runner even when the guard passes"
ROOT="$(fresh_root)"
write_self_test_guard "$ROOT" "check-foo.sh" 1 0
CODE="$(run_guards "$ROOT")"
assert_exit_code "t12-exit" 1 "$CODE"
rm -rf "$ROOT"

# ── T13: every guard still runs even after an earlier one fails ────────────
echo ""
echo "T13: a failing guard does not stop a later guard from running"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-alpha.sh" 1
write_guard "$ROOT" "check-alpha.test.sh" 0
write_guard "$ROOT" "check-zulu.sh" 0 "zulu.ran"
write_guard "$ROOT" "check-zulu.test.sh" 0
CODE="$(run_guards "$ROOT")"
assert_exit_code "t13-exit" 1 "$CODE"
assert_file_exists "t13-zulu-still-ran" "$ROOT/zulu.ran"
assert_output_contains "t13-zulu-line" "check-zulu.sh exit=0" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

echo ""
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
if [ "$FAIL" -ne 0 ]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]}"; do
    echo "  - $e"
  done
  exit 1
fi
exit 0
