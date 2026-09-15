#!/usr/bin/env bash
# check-no-inferred-check.sh — ADR-084 revision 9 D14 rule 2 / FR-109 mechanical guard.
#
# Wave E3 (docs/internal/specs/adr-084-086-joint-delivery-plan.md, round 1)
# deleted the "inferred artifact check": rung 1.5 used to synthesise a
# `test -f && test -s` (or grep-for-substring) shell check from a
# `KindProse`/`JudgmentArtifact` criterion's WORDING alone, never a declared
# check. FR-109 forbids that outright — "Tier 2 runs ONLY a check the
# criterion declared. A check MUST NOT be synthesised from a criterion's
# prose." — and requires the five symbols below to be gone in full, not
# disabled:
#
#   planArtifactCheck, artifactPathRe, artifactContainsRe,
#   artifactCheckCandidate, isSafeWorkspaceArtifactPath
#
# A `KindProse`/`JudgmentArtifact` criterion is now an ordinary prose
# criterion that reaches the Judge, which opens the file it names — a
# better answer than an inferred existence check, not merely a different
# one. What survives is rung 1 (`task.KindCheck`, a DECLARED check with a
# DECLARED command) and rung 2 (`task.KindBehavior`) — both were always
# declared, neither was ever inferred.
#
# A merge from a pre-revision-9 branch can restore all five symbols as an
# ordinary, conflict-free addition — this script fails the build when any
# of them reappears as a definition or non-comment reference. Mirrors
# scripts/check-no-goal-confirm-gate.sh (ADR-081) and
# scripts/check-no-orphan-turn-watchdog.sh (ADR-082), matching the judge
# spec's own instruction at FR-109's traceability row: "Matches the
# check-no-goal-confirm-gate.sh precedent."
#
# Companion wave: G1 (scripts/guards.sh) discovers this file and its
# check-no-inferred-check.test.sh companion with zero wiring edits — this
# wave (G5) does not touch Makefile, .github/workflows/pr.yml or
# deploy/ci-worker/runci.sh.
#
# Scope: hand-written Go, TypeScript, YAML and shell sources under pkg/,
# cmd/, scripts/, .github/, deploy/, tests/, src/, and Makefile — the same
# scan surface as check-no-orphan-turn-watchdog.sh. Excluded within scope:
# this script itself, its own .test.sh companion, generated artifacts,
# node_modules, .git, and comment-only mentions (a retirement comment
# naming these symbols in prose — such as
# pkg/agent/judge_no_inferred_check_adr084_test.go's own header, which
# explains what the test proves is GONE — is the sanctioned way to
# reference them and MUST NOT be flagged).
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# Both SYMBOLS greps below capture grep's own exit status rather than
# swallowing it with `2>/dev/null || true` (the check-no-orphan-turn-
# watchdog.sh F3 hardening pattern). grep exits 0 (match), 1 (no match —
# the expected common case), or >1 (a real failure: invalid regex, an
# unreadable file, etc.). Any grep exit >1 is a hard failure (exit 2,
# stderr shown) rather than a swallowed clean pass — a broken regex or an
# unreadable file must never silently report "OK".
#
# TEST-ONLY OVERRIDE: CHECK_NO_INFERRED_CHECK_SYMBOLS_OVERRIDE, if set,
# replaces the SYMBOLS pattern below. It exists solely so
# check-no-inferred-check.test.sh can inject a deliberately invalid ERE to
# force a real grep exit>1 and assert this script reports it as exit 2
# rather than a false "OK". Never set in CI/Makefile/pr.yml.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-inferred-check: cannot cd to $REPO_ROOT" >&2; exit 2; }

SCAN_DIRS=(pkg cmd scripts .github deploy tests src)
for d in "${SCAN_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-no-inferred-check: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done
if [ ! -f Makefile ]; then
  echo "check-no-inferred-check: expected file 'Makefile' not found under $REPO_ROOT" >&2
  exit 2
fi

SYMBOLS='planArtifactCheck|artifactPathRe|artifactContainsRe|artifactCheckCandidate|isSafeWorkspaceArtifactPath'

# Test-only override — see the hardening note above the Exit line.
SYMBOLS="${CHECK_NO_INFERRED_CHECK_SYMBOLS_OVERRIDE:-$SYMBOLS}"

GREP_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-inferred-check-stderr.XXXXXX")"
trap 'rm -f "$GREP_STDERR_FILE"' EXIT

# Two passes: extension-filtered recursion over the scan dirs (--include only
# applies to files grep finds by recursing into a directory — it does NOT
# filter an explicitly-named file argument like Makefile below, which has no
# extension at all), plus a plain, unfiltered scan of Makefile itself.
dir_hits=$(grep -rnE "$SYMBOLS" \
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yml' \
  --include='*.yaml' --include='*.sh' \
  "${SCAN_DIRS[@]}" 2>"$GREP_STDERR_FILE")
dir_status=$?
if [ "$dir_status" -gt 1 ]; then
  echo "check-no-inferred-check: grep failed while scanning source directories (exit $dir_status)" >&2
  cat "$GREP_STDERR_FILE" >&2
  exit 2
fi

: > "$GREP_STDERR_FILE"
makefile_hits=$(grep -nE "$SYMBOLS" Makefile 2>"$GREP_STDERR_FILE" | sed 's#^#Makefile:#')
makefile_status=$?
if [ "$makefile_status" -gt 1 ]; then
  echo "check-no-inferred-check: grep failed while scanning Makefile (exit $makefile_status)" >&2
  cat "$GREP_STDERR_FILE" >&2
  exit 2
fi

hits=$(printf '%s\n%s\n' "$dir_hits" "$makefile_hits" \
  | grep -v '/generated/' \
  | grep -v '_generated' \
  | grep -v 'node_modules/' \
  | grep -v '^scripts/check-no-inferred-check\.sh:' \
  | grep -v '^scripts/check-no-inferred-check\.test\.sh:' \
  || true)

# Drop comment-only lines: Go/TS/shell `//`/`#` line comments and block-
# comment continuation lines starting with `*`. A retirement comment naming
# a symbol in prose (e.g. "pre-wave-E3, planArtifactCheck would have
# found this...") is the desired outcome, not a violation.
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
  echo "ERROR: retired ADR-084 inferred-artifact-check symbol(s) present in hand-written source:" >&2
  echo "" >&2
  echo "$violations" >&2
  echo "" >&2
  echo "These five symbols were deleted in full by ADR-084 revision 9's D14 rule 2 / FR-109" >&2
  echo "(wave E3) — not disabled, REMOVED. Tier 2 runs ONLY a check the criterion declared;" >&2
  echo "a check MUST NOT be synthesised from a criterion's prose." >&2
  echo "If this came from a merge, resolve by KEEPING THE DELETION — see" >&2
  echo "CLAUDE.md 'Retired surfaces' and" >&2
  echo "docs/internal/architecture/ADR-084-judge-as-an-active-reviewer.md." >&2
  exit 1
fi

echo "OK: no retired ADR-084 inferred-artifact-check symbols found."
