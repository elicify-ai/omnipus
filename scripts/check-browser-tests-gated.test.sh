#!/usr/bin/env bash
# check-browser-tests-gated.test.sh
#
# Self-test for check-browser-tests-gated.sh (F15 regression guard).
#
# Creates temporary fixture Go files under a scratch dir laid out as
# <tmp>/pkg/tools/browser/*.go and runs the lint script against them via the
# REPO_ROOT env override (same pattern as check-no-handwritten-wire-types.
# test.sh). Covers:
#
#   1. A test containing a string literal with an unbalanced '{' (one MORE
#      open than close, e.g. `opener := "{"`) immediately followed by a
#      SECOND, genuinely ungated real-Chrome test — CAUGHT, with the finding
#      attributed to the RIGHT function. Before the F15 fix, the raw brace
#      count from the string desynced the depth counter so it never returned
#      to zero at the first function's real closing brace; the scanner kept
#      absorbing lines (including the second function's own `func Test...`
#      line and its later, unrelated skipIfNoBrowser(t) call) into the FIRST
#      function's body. That merged the second function's gate call into the
#      first function's recorded body, so `has_gate` came back true for a
#      function that never actually called it — the exact "guard silently
#      always passes" failure class this project treats as a defect in its
#      own right. This fixture reproduces that shape.
#   2. A properly gated real-Chrome test with a string literal containing a
#      brace — NOT caught (the brace-in-string must not desync a WELL-formed,
#      correctly gated function into a false positive either).
#   3. A real-Chrome test with no gate at all, no strings involved — CAUGHT
#      (baseline non-regression: the original #615 case still works).
#   4. A file with an unterminated string literal (malformed source) — the
#      script fails LOUDLY (exit 2), not silently (exit 0).
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-browser-tests-gated.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/browser-tests-gated-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_fixture() {
  local content="$1"
  rm -rf "${TMP_DIR:?}/pkg"
  mkdir -p "${TMP_DIR}/pkg/tools/browser"
  printf '%s\n' "$content" > "${TMP_DIR}/pkg/tools/browser/fixture_test.go"
}

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" -eq "$expected" ]]; then
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
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

echo "=== check-browser-tests-gated self-test (F15) ==="
echo ""

# ─── Test 1: brace-in-string desync hides a later ungated real-Chrome test ─

echo "Test 1: an unbalanced brace inside a string literal must not hide a later ungated test"
setup_fixture '
package browser

import "testing"

func TestBraceInString(t *testing.T) {
	opener := "{"
	cfg := newCoordinatorTestConfig(t)
	_ = cfg
	_ = opener
}

func TestActuallyGated(t *testing.T) {
	skipIfNoBrowser(t)
	_ = 1
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "brace-in-string-exit" 1 "$EXIT_CODE"
assert_output_contains "brace-in-string-finding" "TestBraceInString" "$OUTPUT"
assert_output_contains "brace-in-string-marker" "newCoordinatorTestConfig" "$OUTPUT"
# The finding must be attributed to the function that ACTUALLY lacks the
# gate, not silently merged away and not misattributed to the second,
# correctly gated function.
assert_output_not_contains "brace-in-string-not-misattributed" "TestActuallyGated calls" "$OUTPUT"

# ─── Test 2: gated test with a brace-bearing string is not a false positive ─

echo ""
echo "Test 2: a correctly gated test whose string happens to contain a brace is NOT flagged"
setup_fixture '
package browser

import "testing"

func TestGatedWithBraceString(t *testing.T) {
	skipIfNoBrowser(t)
	marker := "}"
	cfg := newCoordinatorTestConfig(t)
	_ = cfg
	_ = marker
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "gated-brace-string-exit" 0 "$EXIT_CODE"
assert_output_not_contains "gated-brace-string-not-flagged" "TestGatedWithBraceString" "$OUTPUT"

# ─── Test 3: baseline — plain ungated real-Chrome test is still caught ─────

echo ""
echo "Test 3: a plain ungated real-Chrome test (no strings involved) is still caught"
setup_fixture '
package browser

import "testing"

func TestPlainUngated(t *testing.T) {
	cfg := resolveTestBinary(t)
	_ = cfg
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "plain-ungated-exit" 1 "$EXIT_CODE"
assert_output_contains "plain-ungated-finding" "TestPlainUngated" "$OUTPUT"

# ─── Test 4: unterminated string — fail loudly (exit 2), never silently ────

echo ""
echo "Test 4: a file with an unterminated string literal fails loudly (exit 2), not silently (exit 0)"
setup_fixture '
package browser

import "testing"

func TestUnterminatedString(t *testing.T) {
	broken := "never closed
	cfg := newCoordinatorTestConfig(t)
	_ = cfg
	_ = broken
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "unterminated-string-exit" 2 "$EXIT_CODE"
assert_output_not_contains "unterminated-string-not-silent-ok" "check-browser-tests-gated: OK" "$OUTPUT"

# ─── Summary ────────────────────────────────────────────────────────────────

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [[ "$FAIL" -gt 0 ]]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]}"; do
    echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
