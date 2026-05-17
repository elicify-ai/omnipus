#!/usr/bin/env bash
# check-no-handwritten-wire-types.test.sh
#
# Self-test for check-no-handwritten-wire-types.sh.
#
# Creates temporary fixture files under /tmp/ that simulate:
#   1.  A hand-written Go wire-format struct (Response suffix) → CAUGHT
#   2.  A Go struct with `// not-wire-format` opt-out → SKIPPED
#   3.  A Go generated file → SKIPPED
#   4.  A TS export interface in src/lib/ws.ts → CAUGHT
#   5.  A TS interface with `// not-wire-format` opt-out → SKIPPED
#   6.  A TS interface in generated directory → SKIPPED
#   7.  Script exits 1 when findings exist (non-baseline mode) → exit 1
#   8.  A Go struct with Body/Info/Event suffix (new broader rule) → CAUGHT
#   9.  A Go _test.go file → SKIPPED
#  10.  A TS `export type Foo = { … }` (object-literal) → CAUGHT
#  11.  A TS export interface with non-Frame/Response name (Agent, Session) → CAUGHT
#  12.  A TS file outside the two target files → NOT caught
#  13.  A TS `export type { X } from '…'` re-export → NOT caught
#  14.  A Go struct with only 1 json tag (below threshold) → SKIPPED
#  15.  Python sub-pass failure propagates as exit 2 → (structural only)
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-handwritten-wire-types.sh"

PASS=0
FAIL=0
ERRORS=()

# ─── Fixture helpers ──────────────────────────────────────────────────────────

TMP_DIR=$(mktemp -d /tmp/wire-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_go_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

setup_ts_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

# ─── Assert helpers ───────────────────────────────────────────────────────────

assert_exit_code() {
  local label="$1"
  local expected="$2"
  local actual="$3"
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
  local label="$1"
  local needle="$2"
  local haystack="$3"
  if echo "$haystack" | grep -q "$needle"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1"
  local needle="$2"
  local haystack="$3"
  if echo "$haystack" | grep -q "$needle"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

echo "=== check-no-handwritten-wire-types self-test ==="
echo ""

# ─── Test 1: Go fixture — hand-written Response struct (should be caught) ─────

echo "Test 1: Go hand-written wire-format struct (Response suffix) is caught"

setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway

// FooResponse is a hand-written wire-format struct — should be flagged.
type FooResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "go-caught-exit" 0 "$EXIT_CODE"
assert_output_contains "go-caught-finding" "FooResponse" "$OUTPUT"
assert_output_contains "go-caught-rule" "go-wire-type" "$OUTPUT"

# ─── Test 2: Go fixture — struct with opt-out comment (should be skipped) ─────

echo ""
echo "Test 2: Go struct with not-wire-format comment is skipped"

setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway

// BarResponse has the opt-out marker — should NOT be flagged.
type BarResponse struct { // not-wire-format
	ID   string `json:"id"`
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "go-skipout-exit" 0 "$EXIT_CODE"
assert_output_not_contains "go-skipout-not-flagged" "BarResponse" "$OUTPUT"

# ─── Test 3: Go fixture — struct in generated directory (should be skipped) ───

echo ""
echo "Test 3: Go struct in generated directory is skipped"

setup_go_fixture "pkg/api/generated/baz_gen.go" '
package generated

type BazResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Val  int    `json:"val"`
}
'
# Ensure no gateway fixture triggers
setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway
// empty
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "go-generated-skip-exit" 0 "$EXIT_CODE"
assert_output_not_contains "go-generated-skip-not-flagged" "BazResponse" "$OUTPUT"

# ─── Test 4: TS fixture — export interface in src/lib/ws.ts (should be caught)

echo ""
echo "Test 4: TS hand-written interface in src/lib/ws.ts is caught"

setup_ts_fixture "src/lib/ws.ts" '
// Hand-written wire-format interface — should be flagged.
export interface FooFrame {
  type: string
  payload: unknown
}
'
# Ensure no gateway Go file triggers
setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway
// empty
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-caught-exit" 0 "$EXIT_CODE"
assert_output_contains "ts-caught-finding" "FooFrame" "$OUTPUT"
assert_output_contains "ts-caught-rule" "ts-wire-type" "$OUTPUT"

# ─── Test 5: TS fixture — interface with opt-out (should be skipped) ──────────

echo ""
echo "Test 5: TS interface with not-wire-format comment is skipped"

setup_ts_fixture "src/lib/ws.ts" '
// This internal helper is not a wire type.
export interface BarFrame { // not-wire-format
  type: string
  payload: unknown
}
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-skipout-exit" 0 "$EXIT_CODE"
assert_output_not_contains "ts-skipout-not-flagged" "BarFrame" "$OUTPUT"

# ─── Test 6: TS fixture — interface in generated directory (should be skipped)

echo ""
echo "Test 6: TS interface in generated directory is skipped"

setup_ts_fixture "src/lib/api/generated/asyncapi-types.ts" '
// Generated — should NOT be flagged.
export interface GenFrame {
  type: string
}
'
setup_ts_fixture "src/lib/ws.ts" '
// no hand-written frames here
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-generated-skip-exit" 0 "$EXIT_CODE"
assert_output_not_contains "ts-generated-skip-not-flagged" "GenFrame" "$OUTPUT"

# ─── Test 7: Verify script exits 1 when findings exist (not --baseline) ───────

echo ""
echo "Test 7: Script exits 1 when findings exist (non-baseline mode)"

setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway

type CatchMeResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Err  string `json:"error"`
}
'
# Clear any TS fixture that might produce OK
setup_ts_fixture "src/lib/ws.ts" '
// no hand-written frames here
'

REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" >/dev/null 2>&1
EXIT_CODE_ENV=$?

assert_exit_code "nonbaseline-exit-is-1" 1 "$EXIT_CODE_ENV"

# ─── Test 8: Go struct with Event/Body/Info suffix — new broader rule ─────────

echo ""
echo "Test 8: Go struct with Event/Body/Info suffix is caught by broader rule"

setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway

// activityEvent has a non-legacy suffix but >= 2 json fields — must be caught.
type activityEvent struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Detail string `json:"detail"`
}

// sandboxConfigPutBody has a Body suffix — must be caught.
type sandboxConfigPutBody struct {
	Mode   string `json:"mode"`
	Policy string `json:"policy"`
}
'
setup_ts_fixture "src/lib/ws.ts" '// empty'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "go-event-suffix-exit" 0 "$EXIT_CODE"
assert_output_contains "go-event-suffix-finding" "activityEvent" "$OUTPUT"
assert_output_contains "go-body-suffix-finding" "sandboxConfigPutBody" "$OUTPUT"

# ─── Test 9: Go _test.go file — should be skipped ─────────────────────────────

echo ""
echo "Test 9: Go _test.go file is skipped"

setup_go_fixture "pkg/gateway/fixture_wire_test.go" '
package gateway

// TestHelper is in a test file — should NOT be flagged.
type TestHelper struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}
'
# Ensure no non-test gateway file triggers
setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway
// empty
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "go-testfile-skip-exit" 0 "$EXIT_CODE"
assert_output_not_contains "go-testfile-skip-not-flagged" "TestHelper" "$OUTPUT"

# ─── Test 10: TS export type = { } object literal — should be caught ──────────

echo ""
echo "Test 10: TS export type = { } object literal in api.ts is caught"

setup_ts_fixture "src/lib/api.ts" '
// Hand-written object-literal type — should be flagged.
export type ValidateTokenResponse = {
  valid: boolean
  user_id: string
}
'
setup_ts_fixture "src/lib/ws.ts" '// empty'
setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway
// empty
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-type-obj-exit" 0 "$EXIT_CODE"
assert_output_contains "ts-type-obj-finding" "ValidateTokenResponse" "$OUTPUT"
assert_output_contains "ts-type-obj-rule" "ts-wire-type" "$OUTPUT"

# ─── Test 11: TS export interface with non-legacy name (Agent, Session) ────────

echo ""
echo "Test 11: TS export interface with non-Frame/Response name is caught"

setup_ts_fixture "src/lib/api.ts" '
// Agent and Session are hand-written wire types — must be caught.
export interface Agent {
  id: string
  name: string
}
export interface Session {
  id: string
  agent_id: string
}
'
setup_ts_fixture "src/lib/ws.ts" '// empty'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-agent-session-exit" 0 "$EXIT_CODE"
assert_output_contains "ts-agent-finding" "Agent" "$OUTPUT"
assert_output_contains "ts-session-finding" "Session" "$OUTPUT"

# ─── Test 12: TS file outside the two target files — NOT caught ───────────────

echo ""
echo "Test 12: TS interface in a non-target file is not flagged"

setup_ts_fixture "src/lib/some-other.ts" '
// In a non-target file — should NOT be flagged.
export interface SomeOtherType {
  foo: string
  bar: number
}
'
setup_ts_fixture "src/lib/api.ts" '// empty'
setup_ts_fixture "src/lib/ws.ts" '// empty'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-nontarget-exit" 0 "$EXIT_CODE"
assert_output_not_contains "ts-nontarget-not-flagged" "SomeOtherType" "$OUTPUT"

# ─── Test 13: TS re-export type alias — NOT caught ────────────────────────────

echo ""
echo "Test 13: TS re-export type alias is not flagged"

setup_ts_fixture "src/lib/api.ts" '
// Re-exports from generated — should NOT be flagged.
export type { Agent } from "@/lib/api/generated/openapi-types"
export type { WsFrame } from "@/lib/api/generated/asyncapi-types"
'
setup_ts_fixture "src/lib/ws.ts" '// empty'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-reexport-exit" 0 "$EXIT_CODE"
assert_output_not_contains "ts-reexport-not-flagged" "Agent" "$OUTPUT"

# ─── Test 14: Go struct with only 1 json tag (below threshold) — SKIPPED ──────

echo ""
echo "Test 14: Go struct with only 1 json tag is not flagged"

setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway

// Only one json tag — below the 2-field threshold.
type tinyStruct struct {
	ID string `json:"id"`
}
'
setup_ts_fixture "src/lib/api.ts" '// empty'
setup_ts_fixture "src/lib/ws.ts" '// empty'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "go-single-tag-exit" 0 "$EXIT_CODE"
assert_output_not_contains "go-single-tag-not-flagged" "tinyStruct" "$OUTPUT"

# ─── Test 15: TS WsConnectionCallbacks with not-wire-format opt-out ───────────

echo ""
echo "Test 15: TS WsConnectionCallbacks with not-wire-format is skipped"

setup_ts_fixture "src/lib/ws.ts" '
// Internal callback config — not a wire type.
export interface WsConnectionCallbacks { // not-wire-format
  onFrame: (frame: unknown) => void
  onConnected: () => void
}
'
setup_ts_fixture "src/lib/api.ts" '// empty'
setup_go_fixture "pkg/gateway/fixture_wire.go" '
package gateway
// empty
'

OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" --baseline 2>&1)
EXIT_CODE=$?

assert_exit_code "ts-callbacks-skip-exit" 0 "$EXIT_CODE"
assert_output_not_contains "ts-callbacks-skip-not-flagged" "WsConnectionCallbacks" "$OUTPUT"

# ─── Summary ──────────────────────────────────────────────────────────────────

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
