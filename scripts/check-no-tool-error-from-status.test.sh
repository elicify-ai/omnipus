#!/usr/bin/env bash
# check-no-tool-error-from-status.test.sh
#
# Self-test for check-no-tool-error-from-status.sh (F14 regression guard).
#
# Creates temporary fixture files under a scratch dir and runs the lint
# script against them via the REPO_ROOT env override (same pattern as
# check-no-handwritten-wire-types.test.sh). Covers:
#
#   1. The bare-assignment form (`const isError = status.type ===
#      'incomplete'`) — CAUGHT. This is what the pre-F14 pattern already
#      caught; kept here as a non-regression check.
#   2. The single-line JSX self-closing prop form (`<Foo isError={status.type
#      === 'incomplete'} />`) — CAUGHT. Before F14 this was end-anchored
#      (`[},;]?\s*(//.*)?$`), so the trailing ` />` after the closing `}`
#      broke the match and this exact, most-common-in-the-SPA shape slipped
#      through silently. This is the finding's core repro.
#   3. The legitimate widened-disjunct form (`status.type === 'incomplete' ||
#      !!error`) — NOT caught (the `||` disjunct means the real signal is
#      already read; this is GenericToolCall.tsx's actual shape).
#   4. A plain variable-passthrough JSX prop (`isError={isError}`) — NOT
#      caught (no `.type === 'incomplete'` substring at all; this is how
#      FileWriteConfirm.tsx/BrowserNavigate.tsx actually pass the flag).
#   5. A store-derived JSX prop (`isError={tc.status === 'error'}`) — NOT
#      caught (real outcome, not the AssistantUI status heuristic; this is
#      ChatScreen.tsx's actual shape).
#   6. Occurrence inside a `//` comment — NOT caught.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-tool-error-from-status.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/tool-error-status-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

clear_fixtures() {
  rm -rf "${TMP_DIR:?}/src"
  mkdir -p "${TMP_DIR}/src"
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

echo "=== check-no-tool-error-from-status self-test (F14) ==="
echo ""
mkdir -p "${TMP_DIR}/src"

# ─── Test 1: bare assignment form (non-regression) ────────────────────────

echo "Test 1: bare 'const isError = status.type === incomplete' is caught"
clear_fixtures
setup_fixture "src/components/Fixture1.tsx" "
function Widget({ status }) {
  const isError = status.type === 'incomplete'
  return isError
}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "bare-assign-exit" 1 "$EXIT_CODE"
assert_output_contains "bare-assign-finding" "Fixture1.tsx" "$OUTPUT"

# ─── Test 2: JSX self-closing prop form (the F14 repro) ───────────────────

echo ""
echo "Test 2: single-line JSX self-closing 'isError={status.type === incomplete} />' is caught"
clear_fixtures
setup_fixture "src/components/Fixture2.tsx" "
function Widget({ status }) {
  return <Foo isError={status.type === 'incomplete'} />
}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "jsx-selfclose-exit" 1 "$EXIT_CODE"
assert_output_contains "jsx-selfclose-finding" "Fixture2.tsx" "$OUTPUT"

# ─── Test 3: legitimate widened-disjunct form is NOT caught ───────────────

echo ""
echo "Test 3: 'status.type === incomplete || !!error' (GenericToolCall.tsx shape) is NOT caught"
clear_fixtures
setup_fixture "src/components/Fixture3.tsx" "
function Widget({ status, error }) {
  const isError = isErrorProp ?? (status.type === 'incomplete' || !!error)
  return isError
}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "disjunct-safe-exit" 0 "$EXIT_CODE"
assert_output_not_contains "disjunct-safe-not-flagged" "Fixture3.tsx" "$OUTPUT"

# ─── Test 4: plain variable-passthrough JSX prop is NOT caught ────────────

echo ""
echo "Test 4: plain variable passthrough 'isError={isError}' (FileWriteConfirm.tsx shape) is NOT caught"
clear_fixtures
setup_fixture "src/components/Fixture4.tsx" "
function Widget({ isError }) {
  return <Foo isError={isError} />
}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "passthrough-safe-exit" 0 "$EXIT_CODE"
assert_output_not_contains "passthrough-safe-not-flagged" "Fixture4.tsx" "$OUTPUT"

# ─── Test 5: store-derived JSX prop is NOT caught ──────────────────────────

echo ""
echo "Test 5: store-derived 'isError={tc.status === error}' (ChatScreen.tsx shape) is NOT caught"
clear_fixtures
setup_fixture "src/components/Fixture5.tsx" "
function Widget({ tc }) {
  return <Foo isError={tc.status === 'error'} />
}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "store-derived-safe-exit" 0 "$EXIT_CODE"
assert_output_not_contains "store-derived-safe-not-flagged" "Fixture5.tsx" "$OUTPUT"

# ─── Test 6: occurrence inside a comment is NOT caught ─────────────────────

echo ""
echo "Test 6: forbidden shape inside a // comment is NOT caught"
clear_fixtures
setup_fixture "src/components/Fixture6.tsx" "
function Widget({ status }) {
  // const isError = status.type === 'incomplete'
  // <Foo isError={status.type === 'incomplete'} />
  return null
}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comment-safe-exit" 0 "$EXIT_CODE"
assert_output_not_contains "comment-safe-not-flagged" "Fixture6.tsx" "$OUTPUT"

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
