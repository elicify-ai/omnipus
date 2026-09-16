#!/usr/bin/env bash
# Regenerate reference tables away from the checkout and compare them with the
# committed copies. Exit 0 means current, 1 means drift, and 2 means the check
# itself could not run.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REFERENCE_DIR="${REFERENCE_DIR:-$REPO_ROOT/docs/reference}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/docs-reference.XXXXXX")" || exit 2
trap 'rm -rf "$WORK"' EXIT

if ! (cd "$REPO_ROOT" && go run ./cmd/docsref -output-dir "$WORK/generated"); then
  echo "check-docs-reference: generator failed" >&2
  exit 2
fi

status=0
for name in providers-and-models.md built-in-tools.md; do
  generated="$WORK/generated/$name"
  committed="$REFERENCE_DIR/$name"
  if [ ! -f "$generated" ]; then
    echo "check-docs-reference: generator did not produce $name" >&2
    exit 2
  fi
  if [ ! -f "$committed" ]; then
    echo "check-docs-reference: missing committed $committed" >&2
    diff -u /dev/null "$generated" || true
    status=1
    continue
  fi
  if ! diff -u "$committed" "$generated"; then
    status=1
  fi
done

exit "$status"
