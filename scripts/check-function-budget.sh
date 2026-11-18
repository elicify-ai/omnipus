#!/usr/bin/env bash
# check-function-budget.sh
#
# Function-size budget gate (docs/internal/architecture/draft-module-map.md,
# "Size budgets (founder ruling, 2026-09-15)" / "How we enforce it"). Runs
# the two scanners (scripts/funlen for Go, scripts/tsfunlen.cjs for TS/TSX),
# applies the rule, and reports against the grandfather list.
#
# Rule (same numbers for production and test code). Length is code lines
# only: blank lines and comment-only lines inside the span do not count
# (founder decision 2026-11-07, option C).
#   * a function warns over 120 code lines, fails over 240
#   * a React component warns at both, never fails
#   * a function already over 240 at seed time is grandfathered by name in
#     scripts/budgets/functions.txt; the listed number is a code-line count
#     and it may only shrink — a new function must be under 240 from the start
#   * a non-component function may opt out with a one-line directive in the
#     // comment block immediately above it (no blank line between that block
#     and the function). Both spellings work in Go and in TS/TSX/JS:
#       //budget:exception <one-line reason>
#       // budget:exception <one-line reason>
#     The reason is the rest of the line, trimmed. An empty reason fails the
#     gate. A reason does not fail the size rule. The marker counts only as a
#     real comment (never text inside a string or template), it must sit in
#     the comment-only block directly above the function, and that function
#     must be the first thing on its line; a lookalike elsewhere does nothing. The
#     directive does nothing on a React component (already warn-only): it is not listed and an empty
#     reason on a component is not a failure.
#
# Matching is by `file<TAB>qualified name`, never by line number, so a
# grandfathered function that moves within its file keeps its entry.
#
# Output contract:
#   WARN <file:line> <name> <lines> > 120
#   WARN <file:line> <name> <lines> > 240 component
#   FAIL <file:line> <name> <lines> > 240 (not grandfathered)
#   FAIL <file:line> <name> <lines> > listed <n>
#   FAIL <file:line> <name> <lines> empty exception reason
#   EXCEPTION <file:line> <name> <code lines> <reason>
#   A listed entry matches by exact file+name, or by package directory+name
#   so a function moved to a sibling file in the same package (a file split)
#   keeps its grandfathered number. A move to another directory does not.
#   grandfathered: <n> functions; components over 240: <m>; exceptions: <k>
#   (always last). <k> is the number of EXCEPTION lines, not empty-reason fails.
#
# Exit: 0 clean or warn-only, 1 any FAIL, 2 the gate itself could not run
# (missing scanner, scanner crashed, bad arguments, an option without its value) — a false green here
# would be worse than a false red (docs/internal/false-green-patterns.md).
#
# Usage: bash scripts/check-function-budget.sh [--root <dir>] [--budget <file>]
# --root/--budget exist so scripts/check-function-budget-selfcheck.sh can
# point the real scanners and the real matching logic at a throwaway fixture
# tree instead of this repository.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAME="check-function-budget"

ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUDGET="$SCRIPT_DIR/budgets/functions.txt"

while [ $# -gt 0 ]; do
  case "$1" in
    --root|--budget)
      [ $# -ge 2 ] || { echo "$NAME: $1 needs a value" >&2; exit 2; }
      if [ "$1" = "--root" ]; then ROOT="$2"; else BUDGET="$2"; fi
      shift 2 ;;
    *) echo "$NAME: unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -d "$ROOT" ] || { echo "$NAME: root not found: $ROOT" >&2; exit 2; }
[ -f "$BUDGET" ] || { echo "$NAME: budget file not found: $BUDGET" >&2; exit 2; }
[ -d "$SCRIPT_DIR/funlen" ] || { echo "$NAME: Go scanner not found: $SCRIPT_DIR/funlen" >&2; exit 2; }
[ -f "$SCRIPT_DIR/tsfunlen.cjs" ] || { echo "$NAME: TS scanner not found: $SCRIPT_DIR/tsfunlen.cjs" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/function-budget.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

# Capture each scanner's exit code directly (no pipe) — a pipeline's status
# is its last command's, so `scanner | something` would silently hide a
# scanner crash (docs/internal/false-green-patterns.md).
GO_RC=0
go run "$SCRIPT_DIR/funlen" -root "$ROOT" -limit 120 >"$TMP/go.out" 2>"$TMP/go.err" || GO_RC=$?
if [ "$GO_RC" -ne 0 ]; then
  echo "$NAME: Go scanner failed (exit $GO_RC)" >&2
  cat "$TMP/go.err" >&2
  exit 2
fi

TS_RC=0
node "$SCRIPT_DIR/tsfunlen.cjs" --root "$ROOT" --limit 120 >"$TMP/ts.out" 2>"$TMP/ts.err" || TS_RC=$?
if [ "$TS_RC" -ne 0 ]; then
  echo "$NAME: TS scanner failed (exit $TS_RC)" >&2
  cat "$TMP/ts.err" >&2
  exit 2
fi

# The matching/reporting logic lives in AWK (portable across the BSD awk
# shipped on macOS and gawk on Linux CI, and immune to bash 3.2's lack of
# associative arrays) rather than a third shell/script deliverable.
awk -v budget="$BUDGET" '
  BEGIN {
    FS = "\t";
    while ((getline line < budget) > 0) {
      if (line ~ /^[[:space:]]*(#|$)/) continue;
      n = split(line, f, "\t");
      if (n < 3) continue;
      # Two keys per entry. Exact file+name first. Then package dir+name,
      # so a grandfathered function that moves to a sibling file in the
      # same package (a file split) keeps its entry; a move to another
      # package does not. If two listed functions in one dir share a name,
      # the dir key keeps the larger number (the exact key still wins).
      key = f[1] SUBSEP f[2];
      budget_lines[key] = f[3] + 0;
      dir = f[1]; sub(/\/[^\/]*$/, "", dir);
      dkey = dir SUBSEP f[2];
      if (!(dkey in budget_dir) || f[3] + 0 > budget_dir[dkey]) budget_dir[dkey] = f[3] + 0;
      budget_count++;
    }
    close(budget);
    fail_count = 0;
    comp_over = 0;
    exc_count = 0;
  }
  {
    if ($0 !~ /^[0-9]+\t/) next;
    n = split($0, f, "\t");
    if (n < 4) next;
    lines = f[1] + 0;
    filecolon = f[2];
    name = f[3];
    kind = f[4];
    exc = (n >= 5 && f[5] != "") ? f[5] : "0";
    flag = substr(exc, 1, 1);
    reason = (flag == "2") ? substr(exc, 2) : "";

    pos = match(filecolon, /:[0-9]+$/);
    file = (pos > 0) ? substr(filecolon, 1, pos - 1) : filecolon;

    if (lines > 120) {
      printf "WARN %s %s %d > 120\n", filecolon, name, lines;
    }

    # Components stay warn-only. A budget directive on one is ignored,
    # including an empty reason: the exception path does not apply.
    if (kind == "component") {
      if (lines > 240) {
        printf "WARN %s %s %d > 240 component\n", filecolon, name, lines;
        comp_over++;
      }
      next;
    }

    if (flag == "1" || (flag == "2" && reason ~ /^[[:space:]]*$/)) {
      printf "FAIL %s %s %d empty exception reason\n", filecolon, name, lines;
      fail_count++;
      next;
    }
    if (flag == "2") {
      printf "EXCEPTION %s %s %d %s\n", filecolon, name, lines, reason;
      exc_count++;
      next;
    }

    if (lines > 240) {
      key = file SUBSEP name;
      dir = file; sub(/\/[^\/]*$/, "", dir);
      dkey = dir SUBSEP name;
      if (key in budget_lines || dkey in budget_dir) {
        blines = (key in budget_lines) ? budget_lines[key] : budget_dir[dkey];
        if (lines > blines) {
          printf "FAIL %s %s %d > listed %d\n", filecolon, name, lines, blines;
          fail_count++;
        }
      } else {
        printf "FAIL %s %s %d > 240 (not grandfathered)\n", filecolon, name, lines;
        fail_count++;
      }
    }
  }
  END {
    printf "grandfathered: %d functions; components over 240: %d; exceptions: %d\n", budget_count, comp_over, exc_count;
    exit (fail_count > 0) ? 1 : 0;
  }
' "$TMP/go.out" "$TMP/ts.out"
