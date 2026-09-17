#!/usr/bin/env bash
# check-file-budget.sh
#
# File-size budget gate (founder ruling, 2026-09-15 — see
# docs/internal/architecture/draft-module-map.md, "Size budgets" and "How we
# enforce it (test design)"). A file warns over 2,000 lines and fails over
# 3,155. Same numbers for production and test code — production Go, test Go,
# and TS/TSX alike.
#
# WHY 3,155 AND NOT A ROUND 3,000 (tightened 4,000 -> 3,155 on 2026-09-17,
# budget-ratchet lane): at 4,000 no non-exempt file could fail. Nine files
# sit over 3,000 (not one — the seed count covered production Go only, and
# this gate also scans test Go and TS/TSX), and six of them are LARGER than
# pkg/agent/loop.go, so any limit that bites loop.go bites those six too.
# 3,155 is the highest limit whose failing set is exactly the grandfathered
# set in scripts/budgets/files.txt: the smallest pinned file (loop.go) is
# 3,156 and the largest unpinned file was 3,130, so every value in
# [3130, 3155] fails exactly the seven pinned files; the highest was taken.
# Lowering the limit further means pinning more files first.
#
# WHY A SCRIPT AND NOT A LINTER SETTING
#
# The repo already configured a function-length cap (funlen, 120 lines) and
# it was silently disabled in .golangci.yaml with nobody noticing
# (draft-module-map.md, "How files grew"). A YAML toggle is not a guard; this
# script plus its self-check (check-file-budget-selfcheck.sh) is, following
# the same family as scripts/check-no-jpeg-screencast.sh and the
# check-no-removed-providers.sh / -selfcheck.sh pair.
#
# WHAT IS CHECKED
#
#   *.go under pkg/, cmd/, internal/ (if present), scripts/
#   *.ts and *.tsx under src/, tests/, e2e/ (if present)
#
# Exempt by construction (generated or vendored, never source a human wrote
# by hand in this repo): pkg/api/generated/, src/lib/api/generated/,
# pkg/gateway/spa/, node_modules/, dist/, .gitnexus/, vendor/, .git/.
#
# A file already over the FAIL limit (3,155 lines) at seed time is
# grandfathered by name in scripts/budgets/files.txt (path<TAB>lines) and
# may only shrink — a grandfathered file whose real line count is now HIGHER
# than its listed number fails, even though it is on the list. Any file not
# on the list that is over 3,155 lines fails outright. Any file over 2,000 lines
# (grandfathered or not) that has not already failed is named as a WARN so
# reviewers see it on every PR without blocking the build.
#
# OUTPUT CONTRACT
#
#   WARN <path> <lines> > 2000
#   FAIL <path> <lines> > 3155 (not grandfathered)
#   FAIL <path> <lines> > listed <n>
#   grandfathered: <n> files              (always the last line)
#
# Exit 0 with no FAIL, 1 if any FAIL. A file under an exempt directory
# produces no output at all — it is not scanned, not warned, not failed.
#
# Usage:
#   bash scripts/check-file-budget.sh
#   bash scripts/check-file-budget.sh --root <dir> --budget <file>   (self-check)
#
# Exit: 0 clean/warn-only, 1 a file fails its budget, 2 the check itself
# could not run (bad args, missing root, missing budget file).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NAME="check-file-budget"

ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BUDGET_FILE="$SCRIPT_DIR/budgets/files.txt"

while [ $# -gt 0 ]; do
  case "$1" in
    --root)
      [ $# -ge 2 ] || { echo "$NAME: --root requires a value" >&2; exit 2; }
      ROOT="$2"; shift 2 ;;
    --budget)
      [ $# -ge 2 ] || { echo "$NAME: --budget requires a value" >&2; exit 2; }
      BUDGET_FILE="$2"; shift 2 ;;
    *)
      echo "$NAME: unknown argument: $1" >&2; exit 2 ;;
  esac
done

ROOT="$(cd "$ROOT" 2>/dev/null && pwd || true)"
if [ -z "$ROOT" ]; then
  echo "$NAME: --root directory does not exist" >&2
  exit 2
fi
if [ ! -f "$BUDGET_FILE" ]; then
  echo "$NAME: budget file not found: $BUDGET_FILE" >&2
  exit 2
fi

WARN_LIMIT=2000
# See the header for why this is 3,155 and not a round 3,000.
FAIL_LIMIT=3000

cd "$ROOT"

TMP_FILES="$(mktemp "${TMPDIR:-/tmp}/file-budget-files.XXXXXX")" || exit 2
trap 'rm -f "$TMP_FILES"' EXIT

: > "$TMP_FILES"

GO_ROOTS=()
for d in pkg cmd internal scripts; do
  [ -d "$d" ] && GO_ROOTS+=("$d")
done
if [ "${#GO_ROOTS[@]}" -gt 0 ]; then
  find "${GO_ROOTS[@]}" -type f -name '*.go' >> "$TMP_FILES" 2>/dev/null || true
fi

TS_ROOTS=()
for d in src tests e2e; do
  [ -d "$d" ] && TS_ROOTS+=("$d")
done
if [ "${#TS_ROOTS[@]}" -gt 0 ]; then
  find "${TS_ROOTS[@]}" -type f \( -name '*.ts' -o -name '*.tsx' \) >> "$TMP_FILES" 2>/dev/null || true
fi

sort -u -o "$TMP_FILES" "$TMP_FILES"

is_exempt() {
  case "${1#./}" in
    pkg/api/generated/*|src/lib/api/generated/*|pkg/gateway/spa/*|node_modules/*|dist/*|.gitnexus/*|vendor/*|.git/*)
      return 0 ;;
    *)
      return 1 ;;
  esac
}

lookup_budget() {
  # $1 = relative path. Prints the listed line count, or nothing if unlisted.
  # Budget file lines: comments start with '#', data is "path<TAB>lines".
  awk -F'\t' -v p="$1" '
    $0 ~ /^#/ { next }
    NF < 2 { next }
    $1 == p { print $2; exit }
  ' "$BUDGET_FILE"
}

GRANDFATHER_COUNT="$(awk '
  $0 ~ /^#/ { next }
  NF == 0 { next }
  { c++ }
  END { print c+0 }
' "$BUDGET_FILE")"

# Build the non-exempt file list as an array, then hand every path to a
# single `wc -l` invocation instead of forking one `wc` per file — with
# ~5,000 files in scope, one process per file is the whole runtime cost
# (measured ~57s); one batched call is well under a second.
FILES=()
while IFS= read -r f; do
  [ -z "$f" ] && continue
  rel="${f#./}"
  is_exempt "$rel" && continue
  FILES+=("$rel")
done < "$TMP_FILES"

FAIL_COUNT=0

if [ "${#FILES[@]}" -gt 0 ]; then
  # `wc -l` appends a "total" summary line only when given more than one
  # path; drop it explicitly rather than relying on array length, so a
  # single-file scope (e.g. the self-check fixtures) is handled the same way.
  #
  # Perf: this whole loop is pure-bash string handling (no per-file fork of
  # tr/sed/awk) and skips straight past any file at or under the warn line —
  # with ~4,850 files in scope but only ~50 ever warn or fail, forking an
  # external process (even a cheap one) per file was the entire runtime cost
  # of an earlier version of this script (~90s instead of well under 10s).
  WC_OUTPUT="$(wc -l -- "${FILES[@]}")"
  while IFS= read -r wcline; do
    [ -z "$wcline" ] && continue
    # `read` with two targets splits on IFS whitespace and dumps everything
    # left over (a path with embedded spaces, however unlikely here) into
    # the second variable — a pure bash builtin, no subprocess.
    read -r lines rel <<< "$wcline"
    [ "$rel" = "total" ] && continue
    [ -z "$lines" ] && continue
    [ "$lines" -le "$WARN_LIMIT" ] && continue

    listed="$(lookup_budget "$rel")"

    if [ -n "$listed" ]; then
      if [ "$lines" -gt "$listed" ]; then
        echo "FAIL $rel $lines > listed $listed"
        FAIL_COUNT=$((FAIL_COUNT + 1))
      elif [ "$lines" -gt "$WARN_LIMIT" ]; then
        echo "WARN $rel $lines > $WARN_LIMIT"
      fi
    else
      if [ "$lines" -gt "$FAIL_LIMIT" ]; then
        echo "FAIL $rel $lines > $FAIL_LIMIT (not grandfathered)"
        FAIL_COUNT=$((FAIL_COUNT + 1))
      elif [ "$lines" -gt "$WARN_LIMIT" ]; then
        echo "WARN $rel $lines > $WARN_LIMIT"
      fi
    fi
  done <<< "$WC_OUTPUT"
fi

echo "grandfathered: ${GRANDFATHER_COUNT} files"

if [ "$FAIL_COUNT" -gt 0 ]; then
  exit 1
fi
exit 0
