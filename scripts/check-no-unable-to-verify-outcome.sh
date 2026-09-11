#!/usr/bin/env bash
# check-no-unable-to-verify-outcome.sh — ADR-084 revision-9 §10 withdrawal
# guard (wave G2, docs/internal/specs/adr-084-086-joint-delivery-plan.md,
# C-01, C-18, R-02).
#
# ADR-084 revision 9 §10 withdraws the three-state criterion outcome in
# full. A criterion's verdict is `met` or `unmet` — there is no
# `unable_to_verify` anywhere as a criterion STATUS, a wire enum value, an
# `Outcome` field, or a rendered SPA branch. Judge spec FR-076 collapses to
# its writer step alone; FR-070's `outcome` field, FR-020a and FR-018a's
# return-shape change are all dead. GOAL-FR-037 backs the same lock from the
# other ADR: exactly three values, `pending` | `met` | `unmet`.
#
# A merge from a pre-ADR-084-revision-9 branch, or an agent that builds
# straight off the in-tree judge spec without reading its withdrawal, can
# resurrect the retired shape as an ordinary, conflict-free addition. This
# script fails the build when that happens.
#
# ─── What this guards, precisely ────────────────────────────────────────────
#   1. The literal string `unable_to_verify` appearing as an ACCEPTED wire
#      value or SPA-visible status anywhere in the four contract copies, the
#      two generated trees, the SPA source tree, or pkg/task/ (where the
#      persisted Go criterion/verdict types live).
#   2. A fourth `CriterionStatus`-typed Go constant in pkg/task/ — the three
#      values `pending`/`met`/`unmet` must stay exactly three, whatever the
#      fourth one would be spelled.
#   3. An `Outcome` struct field on `pkg/task/verdict.go`'s `CriterionVerdict`
#      — ADR-084 revision 9 retired the three-state outcome; `Met bool`
#      stays the only verdict-shape bool (see that file's own C-02/D-H note).
#
# ─── Scan set, and the one deliberate deviation from this wave's row ──────
#
# The wave's row in the joint delivery plan (§3) scopes the scan to
# `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/` and
# `src/components/` — "all exactly zero today" — and names four `pkg/agent`
# files (`goal_compile.go`, `judge.go`, `verifier_adjudication.go`,
# `behavior_scan.go`) as the sole construction-level exclusion for the
# internal tracker ADR-084 §10 explicitly preserves
# (`UnableToVerifyTracker`, `NonVerdictUnableToVerify`). A later resolution,
# R-02, asks this guard to widen its scan to all of `src/`, plus `pkg/task/`
# and `pkg/agent/`, keeping only those same four files excluded, and to add
# the two structural checks above.
#
# This script implements R-02's structural checks and widens the literal
# scan to all of `src/` and to `pkg/task/` — both verified clean today save
# for three known, permanent, negative-oracle regression tests (excluded by
# name below, the same pattern check-no-orphan-turn-watchdog.sh uses for its
# own sibling absence-check test). It does NOT widen the literal scan to
# `pkg/agent/`. Verified evidence: `NonVerdictUnableToVerify` /
# `UnableToVerifyTracker` — the machinery ADR-084 §10 explicitly preserves —
# is not confined to the four named files. As shipped today it is also
# exercised, by name, in (at least) `pkg/agent/goal_compile_test.go`,
# `pkg/agent/judge_declared_check_adr084_test.go`,
# `pkg/agent/judge_check_workspace_reroot_test.go`,
# `pkg/agent/verifier_injection_adr084_test.go` and
# `pkg/agent/verifier_grounding_adr084_test.go` — every one of them a
# legitimate, already-landed regression test for the PRESERVED internal
# mechanism, none of them in the four-file exclusion list, and more will
# land as later rounds (T4 in round 7, among others) add their own coverage
# of the same preserved mechanism. A blanket `pkg/agent/` substring scan
# would be permanently and correctly red against code ADR-084 §10 requires
# to exist, which is not a working regression guard — it is a guard nobody
# can ever leave green. The two structural checks below cover the actual
# risk R-02 named (a reintroduced fourth `CriterionStatus` value or
# `Outcome` field) inside `pkg/task/`, which IS in scope, without that
# collateral damage. Reported to the delivery lead as a plan/reality
# mismatch, not resolved silently.
#
# ─── Named exclusions within the scanned dirs (permanent, not test-only) ──
#   pkg/task/criterion_test.go            — T1's own negative-oracle lock:
#                                            asserts `unable_to_verify` is
#                                            NOT a valid CriterionStatus.
#   pkg/task/verdict_adr084_test.go       — F2's own negative-oracle lock on
#                                            IsValidVerdictEvidenceSource.
#   src/lib/api/criterionStatusEnum.test.ts — T1's SPA-edge zod lock: asserts
#                                            a frame carrying
#                                            `unable_to_verify` is REJECTED.
# These three files exist BECAUSE the value is withdrawn — they prove its
# absence by feeding it in and asserting failure. Flagging them would make
# the permanent regression test that locks this exact requirement trip the
# guard that shares the requirement.
#
# Scope: contracts/, pkg/api/generated/, src/lib/api/generated/, src/,
# pkg/task/. The two generated trees are IN scope, not excluded — they are
# exactly the surface C-18's "four contract copies" doctrine guards; a
# stray "skip anything under a generated/ dir" exclusion would defeat half
# this guard's own scan set, so there is no such exclusion here. Excluded
# within scope: this script itself, its .test.sh companion, node_modules/,
# .git, comment-only mentions, and the three named files above.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# F3 hardening (mirrors check-no-orphan-turn-watchdog.sh): every grep's exit
# status is captured directly, never swallowed with `2>/dev/null || true`.
# grep exits 0 (match), 1 (no match — the expected common case), or >1 (a
# real failure). Any grep exit >1 is a hard failure of the check itself
# (exit 2), never a silently-swallowed "no matches".
#
# TEST-ONLY OVERRIDE: CHECK_NO_UNABLE_TO_VERIFY_OUTCOME_PATTERN_OVERRIDE, if
# set, replaces the literal-scan PATTERN below. It exists solely so this
# script's .test.sh can inject a deliberately invalid ERE to force a real
# grep exit>1 and assert this script reports it as exit 2, not a false "OK".
# Never set in CI/Makefile/pr.yml — production runs always use the real
# pattern.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-unable-to-verify-outcome: cannot cd to $REPO_ROOT" >&2; exit 2; }

SCAN_DIRS=(contracts pkg/api/generated src/lib/api/generated src pkg/task)
for d in "${SCAN_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-no-unable-to-verify-outcome: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

PATTERN='unable_to_verify'
PATTERN="${CHECK_NO_UNABLE_TO_VERIFY_OUTCOME_PATTERN_OVERRIDE:-$PATTERN}"

GREP_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-unable-to-verify-outcome-stderr.XXXXXX")"
trap 'rm -f "$GREP_STDERR_FILE"' EXIT

# Case-sensitive literal scan: contract/generated/SPA/persisted-Go vocabulary
# is always snake_case `unable_to_verify` (the enum value itself) — never
# `UnableToVerify`/`unableToVerify`, which are the internal Go/tracker
# identifiers this script deliberately does not chase into pkg/agent/.
dir_hits=$(grep -rnE "$PATTERN" \
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yaml' \
  --include='*.yml' --include='*.json' \
  "${SCAN_DIRS[@]}" 2>"$GREP_STDERR_FILE")
dir_status=$?
if [ "$dir_status" -gt 1 ]; then
  echo "check-no-unable-to-verify-outcome: grep failed while scanning source directories (exit $dir_status)" >&2
  cat "$GREP_STDERR_FILE" >&2
  exit 2
fi

hits=$(printf '%s\n' "$dir_hits" \
  | grep -v 'node_modules/' \
  | grep -v '^scripts/check-no-unable-to-verify-outcome\.sh:' \
  | grep -v '^scripts/check-no-unable-to-verify-outcome\.test\.sh:' \
  | grep -v '^pkg/task/criterion_test\.go:' \
  | grep -v '^pkg/task/verdict_adr084_test\.go:' \
  | grep -v '^src/lib/api/criterionStatusEnum\.test\.ts:' \
  || true)

# Drop comment-only lines: Go `//`, TS `//`, YAML/shell `#`, block-comment
# continuation lines starting with `*`. A retirement comment naming the
# value in prose (this file's own header included) is the sanctioned way to
# reference it, not a violation.
violations=$(echo "$hits" | awk -F: '
  NF >= 3 {
    line = ""
    for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
    sub(/^[[:space:]]+/, "", line)
    if (line ~ /^\/\//) next
    if (line ~ /^\*/) next
    if (line ~ /^\/\*/) next
    if (line ~ /^#/) next
    print $0
  }' | grep -v '^$' || true)

if [ -n "$violations" ]; then
  echo "ERROR: withdrawn ADR-084 revision-9 'unable_to_verify' criterion value present:" >&2
  echo "" >&2
  echo "$violations" >&2
  echo "" >&2
  echo "ADR-084 revision 9 §10 withdrew the three-state criterion outcome in full." >&2
  echo "A criterion's verdict is met or unmet — there is no unable_to_verify status," >&2
  echo "enum value or Outcome field anywhere on the contract/generated/SPA/persisted" >&2
  echo "surface. See CLAUDE.md and docs/internal/specs/adr-084-086-joint-delivery-plan.md" >&2
  echo "C-01/C-18. If this came from a merge, resolve by KEEPING THE WITHDRAWAL." >&2
  exit 1
fi

# ─── Structural check 1: exactly three CriterionStatus values ─────────────
# Scan every non-test pkg/task/*.go for `<Name> CriterionStatus = "<value>"`
# const declarations and collect the distinct values. The three-value lock
# (C-01) must hold regardless of what a fourth value would be spelled. Test
# files are filtered out with a plain path-prefix match (each candidate
# path already carries its filename via -H's default "file:line:text"
# form) rather than a second grep, so only one grep's exit status needs
# capturing here.
: > "$GREP_STDERR_FILE"
raw_const_lines=$(grep -HnE '^\s*[A-Za-z0-9_]+\s+CriterionStatus\s*=\s*"[a-z_]*"' \
  pkg/task/*.go 2>"$GREP_STDERR_FILE")
raw_const_status=$?
if [ "$raw_const_status" -gt 1 ]; then
  echo "check-no-unable-to-verify-outcome: grep failed while scanning pkg/task/*.go for CriterionStatus constants (exit $raw_const_status)" >&2
  cat "$GREP_STDERR_FILE" >&2
  exit 2
fi

const_lines=""
if [ -n "$raw_const_lines" ]; then
  const_lines=$(printf '%s\n' "$raw_const_lines" | awk -F: '$1 !~ /_test\.go$/')
fi

values=$(printf '%s\n' "$const_lines" \
  | grep -oE '"[a-z_]*"' \
  | tr -d '"' \
  | sort -u)
value_count=$(printf '%s\n' "$values" | grep -c . || true)

if [ "$value_count" -ne 3 ] || [ "$(printf '%s\n' "$values" | tr '\n' ',' )" != "met,pending,unmet," ]; then
  echo "ERROR: CriterionStatus no longer carries exactly the three locked values (pending, met, unmet):" >&2
  echo "" >&2
  echo "$const_lines" >&2
  echo "" >&2
  echo "ADR-084 revision 9 §10 / GOAL-FR-037 lock the criterion-status vocabulary at" >&2
  echo "exactly three values. A fourth constant (whatever it is spelled) is a" >&2
  echo "regression. If this came from a merge, resolve by KEEPING THE THREE-VALUE LOCK." >&2
  exit 1
fi

# ─── Structural check 2: no Outcome field on CriterionVerdict ─────────────
: > "$GREP_STDERR_FILE"
raw_outcome_hits=$(grep -HnE '^\s*Outcome\s+\S+\s' pkg/task/*.go 2>"$GREP_STDERR_FILE")
raw_outcome_status=$?
if [ "$raw_outcome_status" -gt 1 ]; then
  echo "check-no-unable-to-verify-outcome: grep failed while scanning pkg/task/*.go for an Outcome field (exit $raw_outcome_status)" >&2
  cat "$GREP_STDERR_FILE" >&2
  exit 2
fi

outcome_hits=""
if [ -n "$raw_outcome_hits" ]; then
  outcome_hits=$(printf '%s\n' "$raw_outcome_hits" | awk -F: '$1 !~ /_test\.go$/')
fi

if [ -n "$outcome_hits" ]; then
  echo "ERROR: an Outcome field has been added under pkg/task/:" >&2
  echo "" >&2
  echo "$outcome_hits" >&2
  echo "" >&2
  echo "ADR-084 revision 9 withdraws the three-state outcome in full — Met bool stays" >&2
  echo "the only verdict-shape field (pkg/task/verdict.go's own C-02/D-H note). If this" >&2
  echo "came from a merge, resolve by KEEPING THE WITHDRAWAL." >&2
  exit 1
fi

echo "OK: no withdrawn ADR-084 revision-9 unable_to_verify criterion outcome found; CriterionStatus holds exactly [pending, met, unmet]; no Outcome field on pkg/task/ types."
