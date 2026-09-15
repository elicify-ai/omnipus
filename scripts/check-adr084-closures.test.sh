#!/usr/bin/env bash
# check-adr084-closures.test.sh
#
# Proof-of-failure companion for check-adr084-closures.sh (wave G3, JUDGE-FR-061a,
# joint delivery plan §L3/§6 rule 14: "every guard needs a proof-of-failure
# companion that shows it can go red"). Builds synthetic
# <tmp>/pkg/coreagent/core.go, <tmp>/pkg/agent/verifier_capability_gate.go,
# <tmp>/pkg/tools/resolvepath.go, <tmp>/pkg/agent/verifier_budget.go and
# <tmp>/pkg/agent/tool_result_admit.go fixtures under a scratch dir and runs
# the guard against them via the REPO_ROOT env override (same pattern as
# check-browser-tests-gated.test.sh / check-no-handwritten-wire-types.test.sh)
# — it never touches this repository's real scripts/ or pkg/ trees.
#
# R-19 requires one fixture per closure: this file plants exactly one
# missing closure at a time (cases 2-5 below) and asserts each is caught
# and named individually, plus:
#   1. the pre-D1 tree (prohibition still present) — passes with nothing to
#      check, regardless of whether any closure file exists at all;
#   6. the fully co-present post-D1 tree — passes;
#   7. a missing pkg/coreagent/core.go entirely — fails LOUDLY (exit 2),
#      never silently as a pass, mirroring check-no-goal-confirm-gate.sh's
#      sibling existence-check pattern.
#
# D-B note (binding on this file, never on the guard's own logic): no
# fixture here asserts anything about a `met` verdict being rejected,
# downgraded or flipped by a failed quote — that is out of scope for a
# guard that only ever checks whether four named Go symbols exist.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD_SCRIPT="${SCRIPT_DIR}/check-adr084-closures.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/adr084-closures-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

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

# Reset the fixture tree to nothing but a bare pkg/ skeleton.
reset_tree() {
  rm -rf "${TMP_DIR:?}/pkg"
  mkdir -p "${TMP_DIR}/pkg/coreagent" "${TMP_DIR}/pkg/agent" "${TMP_DIR}/pkg/tools"
}

# The exact pre-D1 sentence the guard looks for, byte-identical to the
# constant embedded in check-adr084-closures.sh and to
# pkg/coreagent/judge_rubric_adr084_test.go's oldJudgeRubricProhibition.
OLD_PROHIBITION='Do not run tools, do not request more information, do not speculate beyond what you were given.'

write_core_go_pre_d1() {
  cat > "${TMP_DIR}/pkg/coreagent/core.go" <<EOF
package coreagent

const JudgeDefaultRubric = \`You are the Judge. ${OLD_PROHIBITION}\`
EOF
}

write_core_go_post_d1() {
  cat > "${TMP_DIR}/pkg/coreagent/core.go" <<'EOF'
package coreagent

const JudgeDefaultRubric = `You are the Judge — an active reviewer with read-only tools.`
EOF
}

# All four closures, present and correctly named.
write_all_closures_present() {
  cat > "${TMP_DIR}/pkg/agent/verifier_capability_gate.go" <<'EOF'
package agent

func VerifierGodModeRefusalReason(godMode bool) (reason string, refuse bool) { return "", false }
EOF
  cat > "${TMP_DIR}/pkg/tools/resolvepath.go" <<'EOF'
package tools

func WithReadConfined(ctx int, confined bool) int { return ctx }
func ReadConfined(ctx int) bool { return false }
EOF
  cat > "${TMP_DIR}/pkg/agent/verifier_budget.go" <<'EOF'
package agent

type VerifierBudget struct{}

func NewVerifierBudget(toolCallCap int, byteCap int64, tokenCeiling int, tokenWarnAt int64) *VerifierBudget {
	return &VerifierBudget{}
}
EOF
  cat > "${TMP_DIR}/pkg/agent/tool_result_admit.go" <<'EOF'
package agent

func (al *AgentLoop) admitToolResult() {}
EOF
}

echo "=== check-adr084-closures self-test (JUDGE-FR-061a, R-19) ==="
echo ""

# ─── Case 1: pre-D1 tree — prohibition present, no closures at all ────────

echo "Case 1: pre-D1 rubric (prohibition still present) passes with zero closures on disk"
reset_tree
write_core_go_pre_d1
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "pre-d1-exit" 0 "$EXIT_CODE"
assert_output_contains "pre-d1-message" "has not shipped" "$OUTPUT"

# ─── Case 2: post-D1, closure (a) missing — E1 capability gate ────────────

echo ""
echo "Case 2: post-D1 with the E1 capability-gate closure missing is caught and named"
reset_tree
write_core_go_post_d1
write_all_closures_present
rm -f "${TMP_DIR}/pkg/agent/verifier_capability_gate.go"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-capgate-exit" 1 "$EXIT_CODE"
assert_output_contains "missing-capgate-named" "E1 god-mode/capability gate" "$OUTPUT"
assert_output_contains "missing-capgate-file" "verifier_capability_gate.go" "$OUTPUT"

# ─── Case 3: post-D1, closure (b) missing — E0 read confinement ───────────

echo ""
echo "Case 3: post-D1 with the E0 read-confinement closure missing is caught and named"
reset_tree
write_core_go_post_d1
write_all_closures_present
cat > "${TMP_DIR}/pkg/tools/resolvepath.go" <<'EOF'
package tools
EOF
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-readconfined-exit" 1 "$EXIT_CODE"
assert_output_contains "missing-readconfined-named" "E0 read confinement" "$OUTPUT"
# A tree with ONLY WithReadConfined and not ReadConfined (or vice versa)
# must also be caught — both halves of the seam are required.
cat > "${TMP_DIR}/pkg/tools/resolvepath.go" <<'EOF'
package tools

func WithReadConfined(ctx int, confined bool) int { return ctx }
EOF
OUTPUT2=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE2=$?
assert_exit_code "half-readconfined-exit" 1 "$EXIT_CODE2"
assert_output_contains "half-readconfined-named" "E0 read confinement" "$OUTPUT2"

# ─── Case 4: post-D1, closure (c) missing — E2 verifier budget ────────────

echo ""
echo "Case 4: post-D1 with the E2 verifier-budget closure missing is caught and named"
reset_tree
write_core_go_post_d1
write_all_closures_present
rm -f "${TMP_DIR}/pkg/agent/verifier_budget.go"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-budget-exit" 1 "$EXIT_CODE"
assert_output_contains "missing-budget-named" "E2 verifier budget" "$OUTPUT"
assert_output_contains "missing-budget-file" "verifier_budget.go" "$OUTPUT"

# ─── Case 5: post-D1, closure (d) missing — E2 tool-result capture seam ───

echo ""
echo "Case 5: post-D1 with the E2 tool-result-capture closure missing is caught and named"
reset_tree
write_core_go_post_d1
write_all_closures_present
rm -f "${TMP_DIR}/pkg/agent/tool_result_admit.go"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-admit-exit" 1 "$EXIT_CODE"
assert_output_contains "missing-admit-named" "E2 tool-result capture seam" "$OUTPUT"
assert_output_contains "missing-admit-file" "tool_result_admit.go" "$OUTPUT"

# ─── Case 6: post-D1, all four closures present — passes ──────────────────

echo ""
echo "Case 6: post-D1 with all four closures present passes"
reset_tree
write_core_go_post_d1
write_all_closures_present
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "all-present-exit" 0 "$EXIT_CODE"
assert_output_contains "all-present-message" "all four closure prerequisites are present" "$OUTPUT"

# ─── Case 7: the honesty line — this guard must describe itself as ────────
# co-presence-only, never as a merge-order enforcer (OQ-13).

echo ""
echo "Case 7: a failing run's own message describes co-presence, never merge order, as what it checks"
reset_tree
write_core_go_post_d1
write_all_closures_present
rm -f "${TMP_DIR}/pkg/agent/verifier_budget.go"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "honesty-exit" 1 "$EXIT_CODE"
assert_output_contains "honesty-co-presence" "CO-PRESENCE AT HEAD ONLY" "$OUTPUT"
assert_output_contains "honesty-cannot-enforce-order" "cannot detect or enforce" "$OUTPUT"

# ─── Case 8: missing pkg/coreagent/core.go entirely — fail loudly (exit 2) ─

echo ""
echo "Case 8: a tree missing pkg/coreagent/core.go entirely fails loudly (exit 2), not silently (exit 0)"
rm -rf "${TMP_DIR:?}/pkg"
mkdir -p "${TMP_DIR}/pkg"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-core-go-exit" 2 "$EXIT_CODE"
assert_output_not_contains "missing-core-go-not-silent-ok" "check-adr084-closures: OK" "$OUTPUT"

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
