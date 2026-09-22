#!/usr/bin/env bash
# check-no-subagent-special-case.test.sh — self-test for the ADR-091 guard.
#
# This script validates that the guard correctly rejects each banned symbol
# when reintroduced and passes on a clean tree.
#
# The test creates a temporary worktree copy, injects test cases into it,
# runs the guard, and verifies the expected exit codes.
#
# Exit: 0 all tests pass, 1 a test failed, 2 the test itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
GUARD_SCRIPT="$SCRIPT_DIR/check-no-subagent-special-case.sh"

# Verify the guard script exists
if [ ! -x "$GUARD_SCRIPT" ]; then
  echo "check-no-subagent-special-case.test: guard script not found or not executable: $GUARD_SCRIPT" >&2
  exit 2
fi

# Create a temporary directory for test runs
TMPDIR_BASE="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_BASE"' EXIT

cd "$REPO_ROOT" || { echo "check-no-subagent-special-case.test: cannot cd to $REPO_ROOT" >&2; exit 2; }

TEST_PASS_COUNT=0
TEST_FAIL_COUNT=0

# Helper function to run the guard on a temporary copy
run_guard_test() {
  local test_name="$1"
  local should_pass="$2"  # 1 = should pass (exit 0), 0 = should fail (exit 1)
  local modification_func="$3"

  # Create a temporary copy of the repo
  local test_dir="$TMPDIR_BASE/$test_name"
  mkdir -p "$test_dir"
  cp -R "$REPO_ROOT/pkg" "$test_dir/" 2>/dev/null || true
  cp -R "$REPO_ROOT/cmd" "$test_dir/" 2>/dev/null || true
  cp -R "$REPO_ROOT/src" "$test_dir/" 2>/dev/null || true

  # Apply test modification
  $modification_func "$test_dir"

  # Run the guard in the test directory
  (
    cd "$test_dir"
    REPO_ROOT="$test_dir" "$GUARD_SCRIPT" > /dev/null 2>&1
    exit $?
  )
  local exit_code=$?

  local expected_exit
  if [ "$should_pass" = "1" ]; then
    expected_exit=0
  else
    expected_exit=1
  fi

  if [ $exit_code -eq $expected_exit ]; then
    echo "✓ PASS: $test_name"
    ((TEST_PASS_COUNT++))
  else
    echo "✗ FAIL: $test_name (expected exit $expected_exit, got $exit_code)"
    ((TEST_FAIL_COUNT++))
  fi
}

# Test: clean tree should pass
run_guard_test "clean-tree-passes" 1 "true"

# Test: reintroduce newEphemeralSession should fail
run_guard_test "newEphemeralSession-fails" 0 \
  'function_inject_newEphemeralSession() {
     local dir="$1"
     echo "func newEphemeralSession() {}" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_newEphemeralSession'

# Test: reintroduce maxEphemeralHistorySize should fail
run_guard_test "maxEphemeralHistorySize-fails" 0 \
  'function_inject_maxEphemeralHistorySize() {
     local dir="$1"
     echo "const maxEphemeralHistorySize = 100" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_maxEphemeralHistorySize'

# Test: reintroduce ephemeralSessionStore should fail
run_guard_test "ephemeralSessionStore-fails" 0 \
  'function_inject_ephemeralSessionStore() {
     local dir="$1"
     echo "var ephemeralSessionStore map[string]string" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_ephemeralSessionStore'

# Test: reintroduce executeSync should fail
run_guard_test "executeSync-fails" 0 \
  'function_inject_executeSync() {
     local dir="$1"
     echo "func executeSync() {}" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_executeSync'

# Test: reintroduce DelegationModeAwait should fail
run_guard_test "DelegationModeAwait-fails" 0 \
  'function_inject_DelegationModeAwait() {
     local dir="$1"
     echo "const DelegationModeAwait = 1" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_DelegationModeAwait'

# Test: reintroduce allow_blocking_question should fail (TypeScript)
run_guard_test "allow_blocking_question-fails" 0 \
  'function_inject_allow_blocking_question() {
     local dir="$1"
     mkdir -p "$dir/src/lib"
     echo "export const allow_blocking_question = true;" >> "$dir/src/lib/test_fixture.ts"
   }; function_inject_allow_blocking_question'

# Test: reintroduce parentTS.channel should fail
run_guard_test "parentTS.channel-fails" 0 \
  'function_inject_parentTS_channel() {
     local dir="$1"
     echo "ch := parentTS.channel" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_parentTS_channel'

# Test: reintroduce parentTS.chatID should fail
run_guard_test "parentTS.chatID-fails" 0 \
  'function_inject_parentTS_chatID() {
     local dir="$1"
     echo "id := parentTS.chatID" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_parentTS_chatID'

# Test: reintroduce ParentDurableKey should fail
run_guard_test "ParentDurableKey-fails" 0 \
  'function_inject_ParentDurableKey() {
     local dir="$1"
     echo "key := ParentDurableKey" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_ParentDurableKey'

# Test: reintroduce notifyParentIfAllSiblingsDone should fail
run_guard_test "notifyParentIfAllSiblingsDone-fails" 0 \
  'function_inject_notifyParentIfAllSiblingsDone() {
     local dir="$1"
     echo "func notifyParentIfAllSiblingsDone() {}" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_notifyParentIfAllSiblingsDone'

# Test: reintroduce emitNestedToolCalls should fail
run_guard_test "emitNestedToolCalls-fails" 0 \
  'function_inject_emitNestedToolCalls() {
     local dir="$1"
     echo "func emitNestedToolCalls() {}" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_emitNestedToolCalls'

# Test: ProducingSessionID in comments should pass
run_guard_test "ProducingSessionID-comment-passes" 1 \
  'function_inject_ProducingSessionID_comment() {
     local dir="$1"
     echo "// ADR-091: ProducingSessionID was deleted" >> "$dir/pkg/agent/test_fixture.go"
   }; function_inject_ProducingSessionID_comment'

# Test: ProducingSessionID in events.go scope should pass
run_guard_test "ProducingSessionID-events-scope-passes" 1 \
  'function_inject_ProducingSessionID_events_scope() {
     local dir="$1"
     echo "// ProducingSessionID field deleted by WP-A" >> "$dir/pkg/agent/events.go"
   }; function_inject_ProducingSessionID_events_scope'

# Summary
echo ""
if [ $TEST_FAIL_COUNT -eq 0 ]; then
  echo "All tests passed ($TEST_PASS_COUNT/$((TEST_PASS_COUNT + TEST_FAIL_COUNT)))"
  exit 0
else
  echo "Tests failed: $TEST_FAIL_COUNT failed, $TEST_PASS_COUNT passed"
  exit 1
fi
