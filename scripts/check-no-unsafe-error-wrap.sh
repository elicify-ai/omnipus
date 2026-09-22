#!/usr/bin/env bash
# check-no-unsafe-error-wrap.sh
#
# Regression guard for the two wrapcheck-adjacent defect classes that broke
# 18 packages when wrapcheck was first enabled (reverted at ed86a62f9):
#
#   1. Wrapping a sentinel whose identity is the contract. io.Reader.Read
#      must return io.EOF bare — consumers, including the standard library,
#      compare `err == io.EOF`, which errors.Is does not rescue. The same
#      trap exists for Write/Seek/ReadAt/WriteAt. Wrapping io.EOF /
#      context.Canceled / http.ErrServerClosed as a selector is flagged
#      anywhere.
#
#   2. An unconditional wrap: `fmt.Errorf("...: %w", err)` (or
#      `fmt.Errorf("...: %w", f())`) when err / f() may be nil. fmt.Errorf
#      with a nil %w argument returns a NON-nil error whose message is
#      `...: %!w(<nil>)` — every successful call reports failure. Observed
#      live on boundedBody.Read in the reverted change.
#
# WHY A SCRIPT AND NOT JUST wrapcheck
#
# wrapcheck requires wrapping errors from external packages. That is the
# right rule at package boundaries. It does not know that wrapping io.EOF
# or wrapping a nil success is worse than leaving the error bare. The
# previous enablement satisfied wrapcheck by wrapping those sites, and the
# tests (and production readers) broke. This guard is the other half of
# wrapcheck: it fails the build if either defect class reappears, whether
# wrapcheck is on or off.
#
# Scope: hand-written Go under pkg/ and cmd/. Generated code
# (pkg/api/generated), the embedded SPA, vendor, testdata and dot-dirs are
# skipped by the scanner.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# Usage:
#   bash scripts/check-no-unsafe-error-wrap.sh [--root <dir>]
# --root exists so check-no-unsafe-error-wrap.test.sh can point the real
# scanner at a throwaway fixture tree instead of this repository.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
NAME="check-no-unsafe-error-wrap"
ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"

while [ $# -gt 0 ]; do
  case "$1" in
    --root) ROOT="$2"; shift 2 ;;
    *) echo "$NAME: unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -d "$ROOT" ] || { echo "$NAME: root not found: $ROOT" >&2; exit 2; }
[ -d "$SCRIPT_DIR/unsafe-error-wrap" ] || {
  echo "$NAME: scanner not found: $SCRIPT_DIR/unsafe-error-wrap" >&2
  exit 2
}

for d in pkg cmd; do
  if [ ! -d "$ROOT/$d" ]; then
    echo "$NAME: expected directory '$d' not found under $ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

TMP="$(mktemp -d "${TMPDIR:-/tmp}/unsafe-error-wrap.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

# Capture the scanner's exit code directly (no pipe) — a pipeline's status
# is its last command's, so `scanner | something` would silently hide a
# compile error (docs/internal/false-green-patterns.md).
SCAN_RC=0
# -C forces the nested module (scripts/unsafe-error-wrap/go.mod) so this
# does not compile against the parent module's dependency graph.
go run -C "$SCRIPT_DIR/unsafe-error-wrap" . -root "$ROOT" >"$TMP/out" 2>"$TMP/err" || SCAN_RC=$?

if [ "$SCAN_RC" -eq 0 ]; then
  cat "$TMP/out"
  if [ -s "$TMP/err" ]; then
    cat "$TMP/err" >&2
  fi
  exit 0
fi

if [ "$SCAN_RC" -eq 1 ] && grep -q 'unsafe wrap' "$TMP/out"; then
  cat "$TMP/out"
  if [ -s "$TMP/err" ]; then
    cat "$TMP/err" >&2
  fi
  exit 1
fi

echo "$NAME: scanner failed (exit $SCAN_RC)" >&2
if [ -s "$TMP/err" ]; then
  cat "$TMP/err" >&2
fi
if [ -s "$TMP/out" ]; then
  cat "$TMP/out" >&2
fi
exit 2
