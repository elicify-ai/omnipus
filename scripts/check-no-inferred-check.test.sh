#!/usr/bin/env bash
# check-no-inferred-check.test.sh
#
# Self-test for check-no-inferred-check.sh (ADR-084 revision 9 D14 rule 2 /
# FR-109 regression guard). A guard that cannot fail is no guard
# (docs/internal/false-green-patterns.md) — this builds a scratch fixture
# tree under a temp directory, points the lint script at it via REPO_ROOT
# (same pattern as check-no-orphan-turn-watchdog.test.sh and
# check-no-tool-error-from-status.test.sh), and NEVER touches the real repo
# tree.
#
# Covers:
#
#   1. A planted definition of a guarded symbol (planArtifactCheck) in a
#      non-comment Go line — CAUGHT.
#   2. A planted call site of a different guarded symbol
#      (isSafeWorkspaceArtifactPath) — CAUGHT.
#   3. A planted reference to artifactPathRe in a non-comment line — CAUGHT.
#   4. The SAME symbol names, but only inside `//` (Go) and `#` (shell)
#      comments — NOT caught (retirement comments, e.g. the real
#      judge_no_inferred_check_adr084_test.go header, are the sanctioned
#      way to reference these names in prose).
#   5. A clean tree with none of the guarded symbols anywhere — exits 0.
#   6. A tree missing a required scan directory — exits 2, never a silent
#      green (the check itself could not run).
#   7. A forced grep failure (an invalid ERE injected via the script's
#      test-only CHECK_NO_INFERRED_CHECK_SYMBOLS_OVERRIDE env var) is
#      reported as exit 2 with the grep error on stderr — NOT swallowed
#      into a false "OK: no ... symbols found."
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-inferred-check.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/no-inferred-check-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{pkg,cmd,scripts,.github,deploy,tests,src}
  printf 'all:\n\t@true\n' > "$TMP_DIR/Makefile"
}

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
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

echo "=== check-no-inferred-check self-test (ADR-084 rev 9 D14 rule 2 / FR-109) ==="
echo ""

# --- Test 1: planted definition of planArtifactCheck — CAUGHT ---

echo "Test 1: a planted planArtifactCheck definition is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture1.go" '
package agent

func planArtifactCheck(c *task.Criterion) (string, bool) {
	return "", false
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-def-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-def-finding" "fixture1.go" "$OUTPUT"

# --- Test 2: planted call site of isSafeWorkspaceArtifactPath — CAUGHT ---

echo ""
echo "Test 2: a planted isSafeWorkspaceArtifactPath call is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture2.go" '
package agent

func guard(p string) bool {
	return isSafeWorkspaceArtifactPath(p)
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-call-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-call-finding" "fixture2.go" "$OUTPUT"

# --- Test 3: planted artifactPathRe reference — CAUGHT ---

echo ""
echo "Test 3: a planted artifactPathRe reference is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture3.go" '
package agent

var artifactPathRe = regexp.MustCompile(`[a-zA-Z0-9_./-]+\.[a-zA-Z0-9]+`)
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-var-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-var-finding" "fixture3.go" "$OUTPUT"

# --- Test 4: same symbols, but only inside comments — NOT caught ---

echo ""
echo "Test 4: guarded symbols mentioned only in // and # comments are NOT caught"
setup_skeleton
setup_fixture "pkg/agent/fixture4.go" '
package agent

// planArtifactCheck, artifactPathRe, artifactContainsRe,
// artifactCheckCandidate and isSafeWorkspaceArtifactPath were deleted by
// ADR-084 revision 9 D14 rule 2 (FR-109, wave E3). Pre-wave-E3,
// planArtifactCheck would have synthesised a check from this criterion.
func noop() {}
'
setup_fixture "deploy/ci-worker/fixture4.sh" '
#!/usr/bin/env bash
# artifactCheckCandidate used to be evaluated here before FR-109 deleted it.
echo "no-op"
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comment-only-exit" 0 "$EXIT_CODE"
assert_output_not_contains "comment-only-not-flagged" "fixture4.go" "$OUTPUT"
assert_output_not_contains "comment-only-shell-not-flagged" "fixture4.sh" "$OUTPUT"

# --- Test 5: a clean tree with nothing guarded anywhere — exits 0 ---

echo ""
echo "Test 5: a clean tree with no guarded symbols anywhere exits 0"
setup_skeleton
setup_fixture "pkg/agent/fixture5.go" '
package agent

func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok-message" "OK:" "$OUTPUT"

# --- Test 6: a tree missing a required scan directory refuses to report green ---

echo ""
echo "Test 6: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg"
# Deliberately omit cmd/, scripts/, .github/, deploy/, tests/, src/, Makefile.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"

# --- Test 7: a forced grep failure is a hard failure, not a false "OK" ------

echo ""
echo "Test 7: a grep failure (invalid ERE) is reported as exit 2, not swallowed as 'no matches'"
setup_skeleton
setup_fixture "pkg/agent/fixture7.go" '
package agent

func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" CHECK_NO_INFERRED_CHECK_SYMBOLS_OVERRIDE='(unbalanced' bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grep-failure-exit" 2 "$EXIT_CODE"
assert_output_not_contains "grep-failure-not-false-ok" "OK: no retired" "$OUTPUT"

# --- Summary ----------------------------------------------------------------

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
