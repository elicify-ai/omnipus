#!/usr/bin/env bash
# check-function-budget.sh
#
# Function-size budget gate (docs/internal/architecture/draft-module-map.md,
# "Size budgets (founder ruling, 2026-09-15)" / "How we enforce it"). Runs
# the two scanners (scripts/funlen for Go, scripts/tsfunlen.cjs for TS/TSX),
# applies the rule, and reports against the grandfather list.
#
# Rule (same numbers for production and test code):
#   * a function warns over 120 lines, fails over 240
#   * a React component warns at both, never fails
#   * a function already over 240 at seed time is grandfathered by name in
#     scripts/budgets/functions.txt; it may only shrink — a new function
#     must be under 240 from the start
#
# Matching is by `file<TAB>qualified name`, never by line number, so a
# grandfathered function that moves within its file keeps its entry.
#
# Output contract:
#   WARN <file:line> <name> <lines> > 120
#   WARN <file:line> <name> <lines> > 240 component
#   FAIL <file:line> <name> <lines> > 240 (not grandfathered)
#   FAIL <file:line> <name> <lines> > listed <n>
#   grandfathered: <n> functions; components over 240: <m>   (always last)
#
# Exit: 0 clean or warn-only, 1 any FAIL, 2 the gate itself could not run
# (missing scanner, scanner crashed, bad arguments) — a false green here
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
    --root) ROOT="$2"; shift 2 ;;
    --budget) BUDGET="$2"; shift 2 ;;
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
      key = f[1] SUBSEP f[2];
      budget_lines[key] = f[3] + 0;
      budget_count++;
    }
    close(budget);
    fail_count = 0;
    comp_over = 0;
  }
  {
    if ($0 !~ /^[0-9]+\t/) next;
    n = split($0, f, "\t");
    if (n < 4) next;
    lines = f[1] + 0;
    filecolon = f[2];
    name = f[3];
    kind = f[4];

    pos = match(filecolon, /:[0-9]+$/);
    file = (pos > 0) ? substr(filecolon, 1, pos - 1) : filecolon;

    if (lines > 120) {
      printf "WARN %s %s %d > 120\n", filecolon, name, lines;
    }

    if (kind == "component") {
      if (lines > 240) {
        printf "WARN %s %s %d > 240 component\n", filecolon, name, lines;
        comp_over++;
      }
      next;
    }

    if (lines > 240) {
      key = file SUBSEP name;
      if (key in budget_lines) {
        blines = budget_lines[key];
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
    printf "grandfathered: %d functions; components over 240: %d\n", budget_count, comp_over;
    exit (fail_count > 0) ? 1 : 0;
  }
' "$TMP/go.out" "$TMP/ts.out"
