#!/usr/bin/env bash
# Proof that check-docs-reference.sh reports drift without changing the checkout.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/docs-reference-test.XXXXXX")" || exit 1
trap 'rm -rf "$WORK"' EXIT

if ! (cd "$REPO_ROOT" && go run ./cmd/docsref -output-dir "$WORK/reference"); then
  echo "check-docs-reference.test: setup generation failed" >&2
  exit 1
fi

printf '\nintentional drift\n' >> "$WORK/reference/built-in-tools.md"
REFERENCE_DIR="$WORK/reference" bash "$SCRIPT_DIR/check-docs-reference.sh" > "$WORK/check.log" 2>&1
code=$?
if [ "$code" -ne 1 ]; then
  cat "$WORK/check.log" >&2
  echo "check-docs-reference.test: got exit $code, want 1" >&2
  exit 1
fi

echo "check-docs-reference.test: OK (drift produced exit 1)"
