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

assert_timed_exit_line() {
  local label="$1" prefix="$2" log="$3" line
  line="$(grep -F -- "$prefix start=" "$log" | grep -E ' start=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z dur=[0-9]+s$')"
  if [ -n "$line" ]; then
    echo "  PASS [$label]: $line"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: timed line missing '$prefix start=<UTC> dur=<Ns>'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected timed line containing '$prefix'")
  fi
}

assert_positive_timed_exit_line() {
  local label="$1" prefix="$2" log="$3" line
  line="$(grep -F -- "$prefix start=" "$log" | grep -E ' start=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z dur=[1-9][0-9]*s$')"
  if [ -n "$line" ]; then
    echo "  PASS [$label]: $line"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: timed line missing '$prefix start=<UTC> dur=<positive seconds>s'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected a positive duration for '$prefix'")
  fi
}

assert_independent_guard_timing() {
  local label="$1" companion_prefix="$2" guard_prefix="$3" log="$4"
  local companion_line guard_line companion_start guard_start companion_dur guard_dur
  companion_line="$(grep -F -- "$companion_prefix start=" "$log")"
  guard_line="$(grep -F -- "$guard_prefix start=" "$log")"
  if [[ "$companion_line" =~ start=([^[:space:]]+)[[:space:]]dur=([0-9]+)s$ ]]; then
    companion_start="${BASH_REMATCH[1]}"
    companion_dur="${BASH_REMATCH[2]}"
  else
    companion_start=""
    companion_dur=""
  fi
  if [[ "$guard_line" =~ start=([^[:space:]]+)[[:space:]]dur=([0-9]+)s$ ]]; then
    guard_start="${BASH_REMATCH[1]}"
    guard_dur="${BASH_REMATCH[2]}"
  else
    guard_start=""
    guard_dur=""
  fi
  if [[ -n "$companion_start" && -n "$guard_start" && "$guard_start" > "$companion_start" &&
        "$companion_dur" =~ ^[0-9]+$ && "$guard_dur" =~ ^[0-9]+$ ]] &&
        (( 10#$guard_dur < 10#$companion_dur )); then
    echo "  PASS [$label]: guard start=$guard_start dur=${guard_dur}s follows companion start=$companion_start dur=${companion_dur}s"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: guard start=$guard_start dur=${guard_dur}s must follow companion start=$companion_start dur=${companion_dur}s with a shorter duration"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected guard's independent start and shorter duration")
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
assert_timed_exit_line "t2-timed-guard-line" "check-foo.sh exit=0" "$ROOT/.out.log"
assert_timed_exit_line "t2-timed-companion-line" ">> companion(check-foo.test.sh) exit=0" "$ROOT/.out.log"
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
assert_timed_exit_line "t3-timed-guard-line" "check-foo.sh exit=1" "$ROOT/.out.log"
rm -rf "$ROOT"

# ── T4: companion fails though the guard itself passes → runner still fails ─
echo ""
echo "T4: a failing companion fails the runner even when the guard itself passes"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo.test.sh" 1
CODE="$(run_guards "$ROOT")"
assert_exit_code "t4-exit" 1 "$CODE"
assert_timed_exit_line "t4-timed-failing-companion-line" ">> companion(check-foo.test.sh) exit=1" "$ROOT/.out.log"
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
assert_timed_exit_line "t10-timed-selfcheck-line" ">> companion(check-foo-selfcheck.sh) exit=0" "$ROOT/.out.log"
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
assert_timed_exit_line "t11-timed-flag-line" ">> companion(check-foo.sh --self-test) exit=0" "$ROOT/.out.log"
rm -rf "$ROOT"

# ── T12: a failing --self-test flag companion fails the runner ─────────────
echo ""
echo "T12: a failing --self-test flag companion fails the runner even when the guard passes"
ROOT="$(fresh_root)"
write_self_test_guard "$ROOT" "check-foo.sh" 1 0
CODE="$(run_guards "$ROOT")"
assert_exit_code "t12-exit" 1 "$CODE"
assert_timed_exit_line "t12-timed-failing-flag-line" ">> companion(check-foo.sh --self-test) exit=1" "$ROOT/.out.log"
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

# ── T14: a slow guard reports elapsed seconds, not a constant zero ──────────
echo ""
echo "T14: a slow guard reports a positive elapsed duration"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-slow.test.sh" 0
write_guard "$ROOT" "check-slow.sh" 0
printf '#!/usr/bin/env bash\nsleep 2\nexit 0\n' > "$ROOT/scripts/check-slow.sh"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t14-exit" 0 "$CODE"
assert_positive_timed_exit_line "t14-positive-duration" "check-slow.sh exit=0" "$ROOT/.out.log"
rm -rf "$ROOT"

# ── T15: a failing -selfcheck.sh still has a timed failure line ─────────────
echo ""
echo "T15: a failing -selfcheck.sh companion reports its nonzero exit"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-foo.sh" 0
write_guard "$ROOT" "check-foo-selfcheck.sh" 1
CODE="$(run_guards "$ROOT")"
assert_exit_code "t15-exit" 1 "$CODE"
assert_timed_exit_line "t15-timed-failing-selfcheck-line" ">> companion(check-foo-selfcheck.sh) exit=1" "$ROOT/.out.log"
rm -rf "$ROOT"

# ── T16: clock failure must not skip checks or hide the final summary ───────
echo ""
echo "T16: a failed UTC clock lookup still runs every guard and reports failure"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-alpha.sh" 0 "alpha.ran"
write_guard "$ROOT" "check-alpha.test.sh" 0 "alpha-companion.ran"
write_guard "$ROOT" "check-zulu.sh" 0 "zulu.ran"
write_guard "$ROOT" "check-zulu.test.sh" 0 "zulu-companion.ran"
mkdir -p "$ROOT/bin"
printf '%s\n' '#!/usr/bin/env bash' 'exit 9' > "$ROOT/bin/date"
chmod +x "$ROOT/bin/date"
CODE="$(PATH="$ROOT/bin:$PATH" run_guards "$ROOT")"
assert_exit_code "t16-exit" 1 "$CODE"
assert_file_exists "t16-first-companion-ran" "$ROOT/alpha-companion.ran"
assert_file_exists "t16-later-guard-ran" "$ROOT/zulu.ran"
assert_file_exists "t16-later-companion-ran" "$ROOT/zulu-companion.ran"
assert_output_contains "t16-clock-error" "GUARD RUNNER TIMING FAILURE:" "$(cat "$ROOT/.out.log")"
assert_output_contains "t16-summary" "=== summary ===" "$(cat "$ROOT/.out.log")"
assert_output_contains "t16-timed-guard" "check-zulu.sh exit=0 start=unavailable dur=" "$(cat "$ROOT/.out.log")"
rm -rf "$ROOT"

# ── T17: a slow companion and fast guard use separate clocks ────────────────
echo ""
echo "T17: a slow companion reports elapsed time and the fast guard starts afresh"
ROOT="$(fresh_root)"
write_guard "$ROOT" "check-clock.sh" 0
printf '#!/usr/bin/env bash\nsleep 3\nexit 0\n' > "$ROOT/scripts/check-clock.test.sh"
CODE="$(run_guards "$ROOT")"
assert_exit_code "t17-exit" 0 "$CODE"
assert_positive_timed_exit_line "t17-companion-positive-duration" ">> companion(check-clock.test.sh) exit=0" "$ROOT/.out.log"
assert_independent_guard_timing "t17-guard-independent-timing" ">> companion(check-clock.test.sh) exit=0" "check-clock.sh exit=0" "$ROOT/.out.log"
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
