#!/usr/bin/env bash
# check-single-verdict-projection.sh — ADR-086 GOAL-FR-036 / ADR-084
# JUDGE-FR-076 (step 2) mechanical guard: ONE verdict -> criterion-status
# writer, in ONE file.
#
# Wave G7 of the ADR-084/085/086 joint delivery
# (docs/internal/specs/adr-084-086-joint-delivery-plan.md §3 row G7, and the
# C-03 collision resolution that created this guard).
#
# ─── Why this guard exists ──────────────────────────────────────────────────
# Two of the three specifications independently planned the same writer:
# ADR-086's wave W7 created pkg/agent/verdict_projection.go and ADR-084's
# wave W4a created pkg/agent/verdict_status_projection.go, both to write the
# Judge's per-criterion result onto a criterion's status. Four analysts found
# the collision. C-03 resolved it: ONE file, pkg/agent/verdict_projection.go,
# built by wave E14 — and pkg/agent/verdict_status_projection.go is never
# created, by anyone, ever.
#
# Two writers would not fail loudly. They would each be correct in
# isolation, run on different call paths, and disagree only sometimes —
# which criterion shows met on the board would depend on which path
# adjudicated last. That is why the constraint is CARDINALITY, not mere
# absence of the second filename: a second writer under any name is the
# defect.
#
# ─── The two clauses ────────────────────────────────────────────────────────
#
# CLAUSE 1 — the retired name must not reappear, as a FILE or as a non-comment
#   source reference. `verdict_status_projection` in a comment is fine and
#   expected: pkg/agent/verdict_projection.go's own header says the name must
#   never exist, and that sentence is the documentation of this rule.
#
# CLAUSE 2 — cardinality. Exactly ONE non-test Go file under pkg/ or cmd/ may
#   contain an ASSIGNMENT of the criterion met/unmet constants
#   (task.CritMet / task.CritUnmet, or the bare CritMet / CritUnmet inside
#   pkg/task itself), and that file must be pkg/agent/verdict_projection.go.
#
#   RED ON ZERO as well as on two (delivery plan R-19(c)). Zero means the
#   projection was deleted, gutted, or quietly reduced to a no-op — and a
#   guard that only counted "not more than one" would call that a pass. A
#   silently absent projection is exactly the failure ADR-086 D8 was written
#   to prevent: goal progress stops being visible, and nothing goes red.
#
#   The unit counted is the FILE, not the line. verdict_projection.go
#   legitimately holds two assignment lines (one for met, one for unmet) and
#   may hold more; what must never happen is a SECOND file writing these
#   constants.
#
#   Matched shapes are assignments and composite-literal entries:
#   `x.Status = task.CritMet`, `status := CritUnmet`, `Status: task.CritMet,`.
#   NOT matched, deliberately, and verified against the tree:
#     - the constant declarations themselves in pkg/task/criterion.go
#       (`CritMet     CriterionStatus = "met"` — CritMet is on the LEFT of
#       the `=`, the right-hand side is a string);
#     - `case CritPending, CritMet, CritUnmet:` in IsValidCriterionStatus;
#     - comparisons (`== task.CritMet`): the pattern requires the constant to
#       follow the FIRST `=`, so the second `=` of `==` fails the match.
#
# ─── Scope and exclusions ───────────────────────────────────────────────────
# Hand-written, non-test Go. `_test.go` files are out of scope by
# construction — the projection's own test file asserts on these constants
# constantly, and counting it would make the cardinality meaningless.
# Generated artifacts, node_modules, .git, docs/ and scripts/ are outside the
# scan roots. The TypeScript side is not scanned: `met`/`unmet` cross the
# wire as generated enum strings, and the SPA never writes a criterion's
# status — it renders one.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# Every grep's exit status is captured directly rather than swallowed with
# `|| true` (grep exits 0 match / 1 no-match / >1 real failure); anything >1
# is a hard exit 2. And every scan runs in the CURRENT shell, not in a
# command substitution — an `exit` inside `$(...)` ends only the subshell and
# would leave this script free to print "OK" after a failed scan, which is
# the false-green pattern in docs/internal/false-green-patterns.md.
#
# TEST-ONLY OVERRIDE: CHECK_SINGLE_VERDICT_PROJECTION_PATTERN_OVERRIDE, if
# set, replaces clause 2's assignment pattern. It exists solely so
# check-single-verdict-projection.test.sh can inject a deliberately invalid
# ERE and assert this script reports exit 2 rather than a false "OK". Never
# set it in CI, the Makefile or pr.yml.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-single-verdict-projection: cannot cd to $REPO_ROOT" >&2; exit 2; }

# The one legal home of the projection.
CANONICAL_FILE='pkg/agent/verdict_projection.go'
RETIRED_NAME='verdict_status_projection'

REQUIRED_DIRS=(pkg pkg/agent cmd src)
for d in "${REQUIRED_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-single-verdict-projection: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

SCAN_DIRS=(pkg cmd src)
[ -d tests ] && SCAN_DIRS+=(tests)

# Clause 2's assignment shape. `(task\.)?` covers both the qualified form
# used everywhere outside pkg/task and the bare form legal inside it.
#
# The `[^=!<>:]` before the plain `=` is what keeps a COMPARISON out:
# `== task.CritMet`'s second `=` is preceded by `=`, and its first `=` is
# followed by `=` rather than by the constant, so neither position matches.
# `!=`, `<=` and `>=` fail for the same reason. It requires SOME character
# before the `=`, which every real assignment has (a line beginning with
# `= task.CritMet` is not Go) and which avoids putting a `^` anchor inside a
# group — GNU grep would read that as an anchor and BSD grep may read it as
# a literal, so the two would disagree about what is being scanned for.
ASSIGN_PATTERN='(:=|[^=!<>:]=|:)[[:space:]]*(task\.)?Crit(Met|Unmet)'
ASSIGN_PATTERN="${CHECK_SINGLE_VERDICT_PROJECTION_PATTERN_OVERRIDE:-$ASSIGN_PATTERN}"

GREP_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-single-verdict-projection-stderr.XXXXXX")"
HITS_FILE="$(mktemp "${TMPDIR:-/tmp}/check-single-verdict-projection-hits.XXXXXX")"
trap 'rm -f "$GREP_STDERR_FILE" "$HITS_FILE"' EXIT

# drop_comment_lines reads `path:line:content` records on stdin and drops the
# ones whose content begins with a comment token. A retirement comment naming
# the forbidden file in prose is the sanctioned way to record the rule.
drop_comment_lines() {
  awk -F: '
    NF >= 3 {
      line = ""
      for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
      sub(/^[[:space:]]+/, "", line)
      if (line ~ /^\/\//) next
      if (line ~ /^\*/) next
      if (line ~ /^\/\*/) next
      if (line ~ /^#/) next
      print $0
    }'
}

# run_scan <label> <pattern> <outfile> <include-glob> <path>...
# Writes non-test, non-comment hits to <outfile>. A real grep failure ends
# the SCRIPT with exit 2 — this function is never called from a subshell.
run_scan() {
  local label="$1" pattern="$2" outfile="$3" include="$4"
  shift 4
  local raw status
  : > "$outfile"
  : > "$GREP_STDERR_FILE"
  raw=$(grep -rnE "$pattern" --include="$include" "$@" 2>"$GREP_STDERR_FILE")
  status=$?
  if [ "$status" -gt 1 ]; then
    echo "check-single-verdict-projection: grep failed while scanning for $label (exit $status)" >&2
    cat "$GREP_STDERR_FILE" >&2
    exit 2
  fi
  [ -z "$raw" ] && return 0
  printf '%s\n' "$raw" \
    | grep -v '_test\.go:' \
    | grep -v '/generated/' \
    | grep -v 'node_modules/' \
    | drop_comment_lines \
    | grep -v '^$' > "$outfile"
  return 0
}

FOUND=0

# ─── Clause 1a: the retired filename ────────────────────────────────────────

BAD_FILES=$(find "${SCAN_DIRS[@]}" -type f -name "*${RETIRED_NAME}*" 2>/dev/null | sort)
find_status=$?
if [ "$find_status" -ne 0 ]; then
  echo "check-single-verdict-projection: find failed while scanning for the retired filename (exit $find_status)" >&2
  exit 2
fi

if [ -n "$BAD_FILES" ]; then
  FOUND=1
  echo "ERROR: pkg/agent/${RETIRED_NAME}.go is back — it must NEVER exist (C-03):" >&2
  echo "" >&2
  echo "$BAD_FILES" >&2
  echo "" >&2
  echo "  ADR-084's wave W4a and ADR-086's wave W7 each planned a verdict -> criterion-status" >&2
  echo "  writer. C-03 resolved the collision to ONE file, ${CANONICAL_FILE}." >&2
  echo "  If this came from a merge, resolve by KEEPING THE DELETION and folding any behaviour" >&2
  echo "  the second file carried into the canonical one." >&2
  echo "" >&2
fi

# ─── Clause 1b: the retired name as a non-comment source reference ──────────

run_scan "the retired ${RETIRED_NAME} identifier (Go)" "$RETIRED_NAME" "$HITS_FILE" '*.go' "${SCAN_DIRS[@]}"
NAME_HITS="$(cat "$HITS_FILE")"
run_scan "the retired ${RETIRED_NAME} identifier (TS)" "$RETIRED_NAME" "$HITS_FILE" '*.ts' "${SCAN_DIRS[@]}"
NAME_HITS_TS="$(cat "$HITS_FILE")"
NAME_HITS=$(printf '%s\n%s\n' "$NAME_HITS" "$NAME_HITS_TS" | grep -v '^$')

if [ -n "$NAME_HITS" ]; then
  FOUND=1
  echo "ERROR: a non-comment reference to the retired ${RETIRED_NAME} name:" >&2
  echo "" >&2
  echo "$NAME_HITS" >&2
  echo "" >&2
  echo "  Naming it in a comment is fine — that is how the rule is documented. Referring to it" >&2
  echo "  in code means the second writer is being reintroduced." >&2
  echo "" >&2
fi

# ─── Clause 2: cardinality of the met/unmet writer ──────────────────────────

run_scan "criterion met/unmet assignments" "$ASSIGN_PATTERN" "$HITS_FILE" '*.go' pkg cmd
ASSIGN_HITS="$(cat "$HITS_FILE")"

WRITER_FILES=$(printf '%s\n' "$ASSIGN_HITS" | grep -v '^$' | cut -d: -f1 | sort -u)
if [ -z "$WRITER_FILES" ]; then
  WRITER_COUNT=0
else
  WRITER_COUNT=$(printf '%s\n' "$WRITER_FILES" | grep -c .)
fi

if [ "$WRITER_COUNT" -eq 0 ]; then
  FOUND=1
  echo "ERROR: ZERO files assign task.CritMet / task.CritUnmet outside tests — the verdict ->" >&2
  echo "criterion-status projection is gone (ADR-086 GOAL-FR-036, ADR-084 JUDGE-FR-076 step 2):" >&2
  echo "" >&2
  if [ ! -f "$CANONICAL_FILE" ]; then
    echo "  ${CANONICAL_FILE} does not exist at all." >&2
  else
    echo "  ${CANONICAL_FILE} exists but no longer writes either constant." >&2
  fi
  echo "" >&2
  echo "  Without the projection, an adjudicated criterion never leaves 'pending' and goal" >&2
  echo "  progress silently stops being visible — the exact outcome ADR-086 D8 exists to" >&2
  echo "  prevent. This guard is red on zero as well as on two, deliberately." >&2
  echo "" >&2
elif [ "$WRITER_COUNT" -gt 1 ]; then
  FOUND=1
  echo "ERROR: ${WRITER_COUNT} files assign task.CritMet / task.CritUnmet outside tests — there" >&2
  echo "must be exactly ONE verdict -> criterion-status writer (C-03):" >&2
  echo "" >&2
  printf '%s\n' "$WRITER_FILES" | sed 's/^/  /' >&2
  echo "" >&2
  echo "  Two writers do not fail loudly: each is correct alone, they run on different call" >&2
  echo "  paths, and which outcome a criterion ends up showing depends on which path" >&2
  echo "  adjudicated last. Fold the second into ${CANONICAL_FILE}." >&2
  echo "" >&2
elif [ "$WRITER_FILES" != "$CANONICAL_FILE" ]; then
  FOUND=1
  echo "ERROR: the single verdict -> criterion-status writer is in the wrong file:" >&2
  echo "" >&2
  echo "  found:    $WRITER_FILES" >&2
  echo "  expected: $CANONICAL_FILE" >&2
  echo "" >&2
  echo "  C-03 names ${CANONICAL_FILE} as the one legal home for this writer." >&2
  echo "" >&2
fi

if [ "$FOUND" -ne 0 ]; then
  echo "See docs/internal/specs/adr-084-086-joint-delivery-plan.md (C-03, R-19(c))," >&2
  echo "docs/internal/architecture/ADR-086-goal-as-a-first-class-entity.md (D8) and" >&2
  echo "CLAUDE.md 'Retired surfaces — do NOT reintroduce'." >&2
  exit 1
fi

echo "OK: exactly one verdict -> criterion-status writer (${CANONICAL_FILE}), and no ${RETIRED_NAME}."
