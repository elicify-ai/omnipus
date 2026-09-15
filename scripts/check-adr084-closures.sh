#!/usr/bin/env bash
# check-adr084-closures.sh — ADR-084 D1 closure guard (JUDGE-FR-061a).
#
# Wave G3 of the ADR-084/085/086 joint delivery
# (docs/internal/specs/adr-084-086-joint-delivery-plan.md, wave row G3).
#
# ─── What this guard is, and is NOT (OQ-13, read before "fixing" this) ─────
# ADR-084 revision 9 §3 D1 removes the Judge's old "passive, do-not-
# investigate" instruction from its default rubric (JudgeDefaultRubric,
# pkg/coreagent/core.go) and turns it into an active reviewer that reads
# files and forms a view. The ADR's own prerequisite (JUDGE-FR-061a) is that
# the rubric change must never ship ahead of the machinery it now assumes
# exists: read confinement for the System Agent (so an active Judge cannot
# read every other session's transcript), the god-mode/capability gate, the
# verifier tool-call/byte/token budget, and the tool-result capture seam
# that carries the injection banner.
#
# A script that runs on a pull request can only ever observe files as they
# stand AT HEAD of that PR's merge result — it cannot see the sequence of
# commits that produced that state, so it cannot distinguish "these landed
# in the right order" from "these all landed in the same commit". THIS
# SCRIPT ENFORCES CO-PRESENCE, NOT MERGE ORDER, and must never be described
# or relied on as doing the latter (joint delivery plan §8, blocking waves
# G3/E11, OQ-13's resolution). If a future revision needs true merge-order
# enforcement, that is a branch-protection rule, not a shell script — it is
# explicitly out of scope here.
#
# ─── What it checks ──────────────────────────────────────────────────────
# 1. Does pkg/coreagent/core.go's JudgeDefaultRubric constant still contain
#    the exact pre-D1 prohibition sentence ("Do not run tools, do not
#    request more information, do not speculate beyond what you were
#    given.")? If YES, D1 has not shipped yet on this tree — there is
#    nothing to enforce, and the guard passes.
# 2. If the prohibition is ABSENT (D1 has shipped), all four closure
#    symbols below must be present in hand-written source, or the guard
#    fails — the rubric change reached HEAD without (or ahead of) the
#    machinery ADR-084 §3 D1 requires it to assume exists:
#      (a) pkg/agent/verifier_capability_gate.go — VerifierGodModeRefusalReason
#          (E1 — the god-mode/capability gate)
#      (b) pkg/tools/resolvepath.go — WithReadConfined AND ReadConfined
#          (E0 — the read-confinement ctx seam, JUDGE-FR-060/060b)
#      (c) pkg/agent/verifier_budget.go — NewVerifierBudget
#          (E2 — the verifier tool-call/byte/token budget)
#      (d) pkg/agent/tool_result_admit.go — admitToolResult
#          (E2 — the tool-result capture seam and injection banner)
#
# D-B is binding on this guard's own fixtures (never on production code it
# scans): no fixture in this wave's companion may assert that a `met`
# verdict is rejected, downgraded or flipped because a quote failed to
# verify. A failed quote is reported and justified by the Judge, never
# gated — that is D-B's rule, and this guard has nothing to do with verdict
# outcomes at all. It only ever checks whether four named Go symbols exist.
#
# This script READS pkg/coreagent/core.go; it never writes it (core.go's
# rubric constant is owned by wave E11 and its seed/skills/catalogue
# regions by wave E1 — see the joint delivery plan §5).
#
# Exit: 0 clean (either D1 hasn't shipped, or it has and all four closures
# are present), 1 offenders found (D1 shipped ahead of a closure), 2 the
# check itself could not run (missing directory/file — never a silent
# pass).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-adr084-closures: cannot cd to $REPO_ROOT" >&2; exit 2; }

CORE_GO="pkg/coreagent/core.go"
CAP_GATE_GO="pkg/agent/verifier_capability_gate.go"
RESOLVEPATH_GO="pkg/tools/resolvepath.go"
BUDGET_GO="pkg/agent/verifier_budget.go"
ADMIT_GO="pkg/agent/tool_result_admit.go"

if [ ! -f "$CORE_GO" ]; then
  echo "check-adr084-closures: expected file '$CORE_GO' not found under $REPO_ROOT" >&2
  echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
  echo "   verdict for a tree this script never actually scanned)" >&2
  exit 2
fi

# The exact pre-D1 sentence (verbatim from ADR-084 §2.1's C2 evidence quote,
# and from pkg/coreagent/judge_rubric_adr084_test.go's own
# oldJudgeRubricProhibition oracle) — a fixed-string match, deliberately not
# a looser substring, so a comment merely discussing the old wording cannot
# produce a false "D1 hasn't shipped" reading.
OLD_PROHIBITION='Do not run tools, do not request more information, do not speculate beyond what you were given.'

if grep -qF "$OLD_PROHIBITION" "$CORE_GO"; then
  echo "check-adr084-closures: OK — $CORE_GO still carries the pre-D1 rubric prohibition;"
  echo "  ADR-084 D1 has not shipped on this tree, so there is nothing to close yet."
  exit 0
fi

# D1 has shipped (the prohibition is gone). All four closures must be
# present, in hand-written source, NOW.
missing=0
report() {
  echo "  MISSING: $1"
  missing=1
}

if [ ! -f "$CAP_GATE_GO" ] || ! grep -q 'func VerifierGodModeRefusalReason' "$CAP_GATE_GO" 2>/dev/null; then
  report "E1 god-mode/capability gate — func VerifierGodModeRefusalReason not found in $CAP_GATE_GO"
fi

if [ ! -f "$RESOLVEPATH_GO" ] \
  || ! grep -q 'func WithReadConfined' "$RESOLVEPATH_GO" 2>/dev/null \
  || ! grep -q 'func ReadConfined' "$RESOLVEPATH_GO" 2>/dev/null; then
  report "E0 read confinement — func WithReadConfined and/or func ReadConfined not found in $RESOLVEPATH_GO"
fi

if [ ! -f "$BUDGET_GO" ] || ! grep -q 'func NewVerifierBudget' "$BUDGET_GO" 2>/dev/null; then
  report "E2 verifier budget — func NewVerifierBudget not found in $BUDGET_GO"
fi

if [ ! -f "$ADMIT_GO" ] || ! grep -q 'admitToolResult' "$ADMIT_GO" 2>/dev/null; then
  report "E2 tool-result capture seam — admitToolResult not found in $ADMIT_GO"
fi

if [ "$missing" -ne 0 ]; then
  echo "" >&2
  echo "check-adr084-closures: FAIL — $CORE_GO's JudgeDefaultRubric no longer carries the" >&2
  echo "pre-D1 'do not investigate' prohibition (ADR-084 D1 has shipped), but at least one" >&2
  echo "closure prerequisite ADR-084 §3 D1 depends on is absent from HEAD (see MISSING lines" >&2
  echo "above). This guard enforces CO-PRESENCE AT HEAD ONLY — it cannot detect or enforce" >&2
  echo "merge order — so this failure means the rubric rewrite and its closures are not" >&2
  echo "co-present on this tree right now, not that they merged out of order." >&2
  exit 1
fi

echo "check-adr084-closures: OK — ADR-084 D1 has shipped ($CORE_GO's rubric no longer carries"
echo "  the pre-D1 prohibition) and all four closure prerequisites are present:"
echo "  - E1 god-mode/capability gate ($CAP_GATE_GO)"
echo "  - E0 read confinement ($RESOLVEPATH_GO)"
echo "  - E2 verifier budget ($BUDGET_GO)"
echo "  - E2 tool-result capture seam ($ADMIT_GO)"
exit 0
