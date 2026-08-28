#!/usr/bin/env bash
# check-no-removed-providers-selfcheck.sh
#
# Proves that scripts/check-no-removed-providers.sh CAN fail (ADR-068 spec
# SC-001 / TDD row 1). A guard that has never been seen to go red is not a
# guard — this repo's docs/internal/false-green-patterns.md records a guard
# test that passed 673/673 with the feature it guarded deleted.
#
# It builds a throw-away synthetic tree (the six scanned roots, a copy of the
# allow-list) and drives the real script against it with REPO_ROOT:
#
#   1. clean tree                                          -> must exit 0
#   2. fixture containing the deleted provider id in pkg/  -> must exit 1
#   3. fixture with the second deleted id in src/          -> must exit 1
#   4. fixture with a deleted OAuth symbol in cmd/         -> must exit 1
#   5. the same text under an allow-listed path            -> must exit 0
#   6. a file whose NAME carries the id                    -> must exit 1
#
# The forbidden strings are assembled from fragments so that this file never
# spells them (it lives under scripts/, outside the scanned roots, but the
# point of the guard is that nothing new spells them either).
#
# Exit: 0 all cases behave, 1 a case misbehaves, 2 harness could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECK="$SCRIPT_DIR/check-no-removed-providers.sh"
ALLOW="$SCRIPT_DIR/no-removed-providers.allow"

[ -f "$CHECK" ] || { echo "selfcheck: missing $CHECK" >&2; exit 2; }
[ -f "$ALLOW" ] || { echo "selfcheck: missing $ALLOW" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/no-removed-providers.XXXXXX")" || exit 2
trap 'rm -rf "$TMP"' EXIT

fresh_tree() {
  rm -rf "$TMP/tree"
  mkdir -p "$TMP/tree/scripts"
  for d in pkg cmd src contracts config docs; do mkdir -p "$TMP/tree/$d"; done
  cp "$ALLOW" "$TMP/tree/scripts/"
}

ID_A="anti""gravity"
ID_B="claude""-cli"
# ADR-068 §8b: RequestDeviceCode is restored (T068-32) and no longer
# forbidden — the fixture symbol must be one still in SYMS.
SYM="createClaude""AuthProvider"

FAIL=0
expect() { # expect <label> <want-exit>
  local label="$1" want="$2" got
  REPO_ROOT="$TMP/tree" bash "$CHECK" >"$TMP/out" 2>&1
  got=$?
  if [ "$got" -ne "$want" ]; then
    echo "selfcheck FAIL: $label — wanted exit $want, got $got" >&2
    sed 's/^/    | /' "$TMP/out" >&2
    FAIL=1
  else
    echo "selfcheck ok:   $label (exit $got)"
  fi
}

fresh_tree
expect "clean synthetic tree" 0

fresh_tree
printf 'package x\n\nconst p = "%s"\n' "$ID_A" > "$TMP/tree/pkg/fixture.go"
expect "fixture with $ID_A in pkg/" 1

fresh_tree
printf 'export const id = "%s";\n' "$ID_B" > "$TMP/tree/src/fixture.ts"
expect "fixture with $ID_B in src/" 1

fresh_tree
printf 'package main\n\nfunc %s() {}\n' "$SYM" > "$TMP/tree/cmd/fixture.go"
expect "fixture with $SYM in cmd/" 1

fresh_tree
# Pick the first non-comment allow-list line that is a plain path (no glob
# metacharacters) so the fixture lands on a path the allow-list names.
allowed="$(grep -vE '^[[:space:]]*(#|$)' "$ALLOW" | grep -E '^(pkg|cmd|src|contracts|config|docs)/' | grep -vE '[*?[]' | head -n1 | tr -d "\\\\")"
[ -n "$allowed" ] || { echo "selfcheck: allow-list has no literal path inside a scanned root to test with" >&2; exit 2; }
mkdir -p "$TMP/tree/$(dirname "$allowed")"
printf 'history: %s and %s were removed\n' "$ID_A" "$ID_B" > "$TMP/tree/$allowed"
expect "same text under allow-listed path $allowed" 0

fresh_tree
printf 'history: %s and %s were removed\n' "$ID_A" "$ID_B" > "$TMP/tree/docs/not-allowed.md"
expect "same text under a NON-allow-listed docs path" 1

fresh_tree
upper="$(printf '%s' "$ID_A" | tr '[:lower:]' '[:upper:]')"
printf 'nothing here\n' > "$TMP/tree/docs/${upper}_USAGE.md"
expect "file NAME carrying $ID_A" 1

# pkg/gateway/spa/ is the gitignored go:embed staging copy of the Vite build,
# not source. A minified third-party bundle there is not a trace and must not
# fail the guard — this went unnoticed until the guard ran on a checkout that
# had actually built the SPA (a fresh GitHub checkout has no such directory).
fresh_tree
mkdir -p "$TMP/tree/pkg/gateway/spa/assets"
printf 'var x="%s";var y="%s";\n' "$ID_A" "$ID_B" > "$TMP/tree/pkg/gateway/spa/assets/app-abc123.js"
expect "build output under pkg/gateway/spa/ is not source" 0

# …and the exclusion is that directory ONLY: the same bytes one level up are
# still a trace, so the skip cannot be widened by accident.
fresh_tree
mkdir -p "$TMP/tree/pkg/gateway"
printf 'var x="%s";\n' "$ID_A" > "$TMP/tree/pkg/gateway/notspa.js"
expect "the same bytes outside pkg/gateway/spa/ still fail" 1

if [ "$FAIL" -ne 0 ]; then
  echo "selfcheck: check-no-removed-providers.sh does not behave — see above" >&2
  exit 1
fi
echo "selfcheck: OK (check-no-removed-providers.sh provably fails on offenders)"
exit 0
