#!/usr/bin/env bash
# scripts/adr091-issue-closure.selftest.sh
#
# Self-test for scripts/adr091-issue-closure.sh. Stubs `gh` on PATH with a
# script that reads fixture files from FIXTURES_DIR and asserts every exit
# path the WP-F spec demands.
#
# Test layout (one fixture set per case, isolation via fresh tmpdir):
#
#   T0 acceptance — every issue in its expected state with a qualifying
#                     comment (mirrors both §D3 for closed and §12 with
#                     "stays open because" for open). Script exits 0.
#   T1 reject — closed issue comment is an unrelated "#12 fix" (no #812,
#                     no ADR-091 §). Script exits 1.
#   T2 reject — bare hex string. Script exits 1.
#   T3 reject — "#812 ... ADR-091 §" with no section identifier after §.
#                     Script exits 1.
#   T4 reject — "ADR-091" mention without §. Script exits 1.
#   T5 reject — open-issue comment has the right section but no
#                     "stays open because". Script exits 1.
#   T6 reject — comment cites the right section but a different PR number.
#                     Script exits 1.
#   T7 exit 2 — gh prints qualifying content and then exits non-zero for
#                     one call (the spec's "prints a qualifying comment then
#                     exits non-zero" case). Script exits 2.
#   T8 exit 2 — every gh call fails. Script exits 2 on the first one.
#
# Exit code of this script: 0 only if every assertion passes; 1 otherwise.
# The closure script itself is unmodified.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLOSURE="$SCRIPT_DIR/adr091-issue-closure.sh"
PR=812

PASS=0
FAIL=0
ERRORS=()

TMP_BASE="$(mktemp -d /tmp/adr091-closure-selftest.XXXXXX)" || exit 2
trap 'rm -rf "$TMP_BASE"' EXIT

FIXTURES_DIR="$TMP_BASE/fixtures"
STUB_DIR="$TMP_BASE/stub"
mkdir -p "$FIXTURES_DIR" "$STUB_DIR"

# ─── Assert helpers ──────────────────────────────────────────────────────────

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
  local label="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

# ─── Stub gh ─────────────────────────────────────────────────────────────────

# Reads fixtures from $FIXTURES_DIR/<issue>.<field>.
# If <issue>.<field>.fail exists, exit 1 (any stdout content is irrelevant —
# the closure script's `if ! gh` triggers).
cat > "$STUB_DIR/gh" <<'STUB'
#!/usr/bin/env bash
# Stub gh for the adr091-issue-closure self-test.
# Scans argv for the issue number and the field following --json, then cats
# FIXTURES_DIR/<issue>.<field>; if <issue>.<field>.fail exists, exits 1.
FIXTURES_DIR="${FIXTURES_DIR:-/tmp/adr091-closure-fixtures}"

ISSUE_NUM=""
JSON_FIELD=""
prev=""
for arg in "$@"; do
  case "$prev" in
    --json) JSON_FIELD="$arg" ;;
  esac
  case "$arg" in
    --json) ;;
    --*|-*)
      ;;
    *)
      if [[ "$arg" =~ ^[0-9]+$ ]]; then
        ISSUE_NUM="$arg"
      fi
      ;;
  esac
  prev="$arg"
done

fixture="$FIXTURES_DIR/${ISSUE_NUM}.${JSON_FIELD}"
if [ -e "${fixture}.fail" ]; then
  echo "stub gh: simulated failure for #$ISSUE_NUM field=$JSON_FIELD" >&2
  exit 1
fi
if [ -f "$fixture" ]; then
  cat "$fixture"
  exit 0
fi
echo "stub gh: no fixture for #$ISSUE_NUM field=$JSON_FIELD" >&2
exit 1
STUB
chmod +x "$STUB_DIR/gh"

# ─── Fixture helpers ─────────────────────────────────────────────────────────

write_state() {  # write_state <issue> <state>
  printf '%s\n' "$2" > "$FIXTURES_DIR/$1.state"
  rm -f "$FIXTURES_DIR/$1.state.fail"
}

write_comments() {  # write_comments <issue> [body ...] — one body per arg, newline-joined
  local n="$1"; shift
  local body=""
  for arg in "$@"; do
    if [ -z "$body" ]; then body="$arg"; else body="$body
$arg"; fi
  done
  printf '%s' "$body" > "$FIXTURES_DIR/$n.comments"
  rm -f "$FIXTURES_DIR/$n.comments.fail"
}

# default_accept_fixtures writes the fully-qualifying fixture set used by the
# acceptance test and by every rejection test (each rejection test overrides
# one issue's comments to its bad body).
default_accept_fixtures() {
  for n in 658 614 670 755 763 764 765; do
    write_state "$n" "CLOSED"
    write_comments "$n" "Closed by #${PR} — ADR-091 §D3"
  done
  for n in 784 803; do
    write_state "$n" "OPEN"
    write_comments "$n" "ADR-091 §12: stays open because content-level quoting is a separate decision"
  done
}

# run_closure runs the closure script in a subprocess with the stub gh on PATH
# and the fixture dir exported to the stub. Captures stdout/stderr to $1 and
# echoes the exit code so assert_exit_code can store it in a variable.
run_closure() {
  FIXTURES_DIR="$FIXTURES_DIR" PATH="$STUB_DIR:$PATH" PR="$PR" \
    bash "$CLOSURE" > "$1" 2>&1
  echo $?
}

# ─── T0 acceptance ───────────────────────────────────────────────────────────

echo "T0: acceptance — every issue in its expected state with a qualifying comment"
default_accept_fixtures
ec="$(run_closure "$TMP_BASE/t0.out")"
assert_exit_code "t0-exit-0" 0 "$ec"

# ─── T1–T6 rejection ────────────────────────────────────────────────────────

echo ""
echo "T1: closed issue with an unrelated #12 comment is rejected"
default_accept_fixtures
write_comments 658 "fixed something unrelated with #12 today"
ec="$(run_closure "$TMP_BASE/t1.out")"
assert_exit_code "t1-exit-1" 1 "$ec"
assert_output_contains "t1-names-issue" "issue #658: state=CLOSED citing-comments=0" "$TMP_BASE/t1.out"

echo ""
echo "T2: closed issue with a bare hex comment is rejected"
default_accept_fixtures
write_comments 614 "abcdef0123456789abcdef0123456789"
ec="$(run_closure "$TMP_BASE/t2.out")"
assert_exit_code "t2-exit-1" 1 "$ec"
assert_output_contains "t2-names-issue" "issue #614: state=CLOSED citing-comments=0" "$TMP_BASE/t2.out"

echo ""
echo "T3: '#<PR> ... ADR-091 §' with no section identifier is rejected"
default_accept_fixtures
write_comments 670 "Closed by #${PR} — ADR-091 § followed by no section"
ec="$(run_closure "$TMP_BASE/t3.out")"
assert_exit_code "t3-exit-1" 1 "$ec"
assert_output_contains "t3-names-issue" "issue #670: state=CLOSED citing-comments=0" "$TMP_BASE/t3.out"

echo ""
echo "T4: 'ADR-091' mention without § is rejected"
default_accept_fixtures
write_comments 755 "ADR-091 finally addressed in this delivery"
ec="$(run_closure "$TMP_BASE/t4.out")"
assert_exit_code "t4-exit-1" 1 "$ec"
assert_output_contains "t4-names-issue" "issue #755: state=CLOSED citing-comments=0" "$TMP_BASE/t4.out"

echo ""
echo "T5: open-issue comment missing 'stays open because' is rejected"
default_accept_fixtures
write_comments 784 "ADR-091 §12: addressed in this delivery"
ec="$(run_closure "$TMP_BASE/t5.out")"
assert_exit_code "t5-exit-1" 1 "$ec"
assert_output_contains "t5-names-issue" "issue #784: state=OPEN reason-comments=0" "$TMP_BASE/t5.out"

echo ""
echo "T6: closed comment cites the right section but a different PR number is rejected"
default_accept_fixtures
write_comments 763 "Closed by #999 — ADR-091 §D3"
ec="$(run_closure "$TMP_BASE/t6.out")"
assert_exit_code "t6-exit-1" 1 "$ec"
assert_output_contains "t6-names-issue" "issue #763: state=CLOSED citing-comments=0" "$TMP_BASE/t6.out"

# ─── T7–T8 exit 2 ───────────────────────────────────────────────────────────

echo ""
echo "T7: gh prints qualifying content but exits non-zero → script exits 2"
default_accept_fixtures
# Mark the very first closed-issue fetch as failing. The closure script's
# fetch() exits 2 the moment gh returns non-zero — any qualifying content
# on the same call is irrelevant.
: > "$FIXTURES_DIR/658.state.fail"
ec="$(run_closure "$TMP_BASE/t7.out")"
assert_exit_code "t7-exit-2" 2 "$ec"
assert_output_contains "t7-names-issue" "gh failed for #658 (state)" "$TMP_BASE/t7.out"

echo ""
echo "T8: every gh call fails → script exits 2 on the first one"
default_accept_fixtures
for n in 658 614 670 755 763 764 765 784 803; do
  : > "$FIXTURES_DIR/$n.state.fail"
done
ec="$(run_closure "$TMP_BASE/t8.out")"
assert_exit_code "t8-exit-2" 2 "$ec"
assert_output_contains "t8-names-issue" "gh failed for #658 (state)" "$TMP_BASE/t8.out"

# ─── Acceptance round-trip ───────────────────────────────────────────────────

# Re-run the acceptance case after T7/T8 left failure markers behind, to be
# sure a follow-up run with a healthy fixture set still exits 0 (the stub
# caches no state).
echo ""
echo "T9: re-run acceptance after the exit-2 cases left fixtures in failure mode"
default_accept_fixtures  # overwrites any earlier .fail files
ec="$(run_closure "$TMP_BASE/t9.out")"
assert_exit_code "t9-exit-0-after-2" 0 "$ec"

# ─── Summary ─────────────────────────────────────────────────────────────────

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]:-}"; do
    [ -n "$e" ] && echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
