#!/usr/bin/env bash
# check-no-legacy-model-store.test.sh
#
# Self-test for check-no-legacy-model-store.sh (session-core U2 / DEL-10 /
# DEL-12 regression guard). A guard that cannot fail is no guard
# (docs/internal/false-green-patterns.md): this builds scratch fixture trees
# under a temp directory, points the guard at them via REPO_ROOT, and never
# touches the real repo tree.
#
# Covers: a retired call is CAUGHT; the same call only in a `//` comment is
# NOT; memory.Store's context-taking TruncateHistory is NOT; a clean tree
# exits 0; a missing scan directory and a missing known symbol (the guard's
# own instrument self-check) exit 2 rather than a silent green.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="${SCRIPT_DIR}/check-no-legacy-model-store.sh"

PASS=0
FAIL=0
TMP_DIR=$(mktemp -d /tmp/legacy-model-store-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

# A fixture tree the guard accepts: pkg/ and cmd/ exist and pkg/session holds
# the known symbol its instrument self-check looks for.
skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR/pkg/session" "$TMP_DIR/pkg/agent" "$TMP_DIR/cmd"
  printf 'package session\n\nfunc (us *UnifiedStore) ReadArchive(id string) {}\n' >"$TMP_DIR/pkg/session/archive.go"
}

# run_guard <label> <expected-exit> [<expected-output-substring>]
run_guard() {
  local label="$1" want="$2" needle="${3:-}" out rc
  out=$(REPO_ROOT="$TMP_DIR" bash "$GUARD" 2>&1)
  rc=$?
  if [ "$rc" -eq "$want" ]; then
    echo "PASS [$label-exit]: exit $rc (expected $want)"
    PASS=$((PASS + 1))
  else
    echo "FAIL [$label-exit]: exit $rc (expected $want)"
    FAIL=$((FAIL + 1))
  fi
  if [ -n "$needle" ]; then
    if printf '%s' "$out" | grep -qF -- "$needle"; then
      echo "PASS [$label-output]: output contains '$needle'"
      PASS=$((PASS + 1))
    else
      echo "FAIL [$label-output]: output does NOT contain '$needle'"
      FAIL=$((FAIL + 1))
    fi
  fi
}

echo "Test 1: a clean tree exits 0"
skeleton
printf 'package agent\n\nfunc f() {}\n' >"$TMP_DIR/pkg/agent/a.go"
run_guard clean 0

echo "Test 2: a retired call in code is caught"
skeleton
printf 'package agent\n\nfunc f(s *S) { s.SetHistory(nil) }\n' >"$TMP_DIR/pkg/agent/a.go"
run_guard offender 1 "SetHistory"

echo "Test 3: a retired per-agent store accessor is caught"
skeleton
printf 'package agent\n\nfunc f(al *L) { _ = al.GetAgentStore("x") }\n' >"$TMP_DIR/pkg/agent/a.go"
run_guard accessor 1 "GetAgentStore"

echo "Test 4: the same call only inside a // comment is not caught"
skeleton
printf 'package agent\n\n// s.SetHistory(nil) was removed\nfunc f() {}\n' >"$TMP_DIR/pkg/agent/a.go"
run_guard comment 0

echo "Test 5: memory.Store's context-taking TruncateHistory is not caught"
skeleton
printf 'package agent\n\nfunc f(m *M) { m.TruncateHistory(context.Background(), "k", 1) }\n' >"$TMP_DIR/pkg/agent/a.go"
run_guard memory-store 0

echo "Test 6: a missing scan directory exits 2, never a silent green"
skeleton
rm -rf "$TMP_DIR/cmd"
run_guard missing-dir 2 "expected directory 'cmd'"

echo "Test 7: a tree without the known symbol exits 2 (instrument self-check)"
skeleton
printf 'package session\n' >"$TMP_DIR/pkg/session/archive.go"
run_guard no-symbol 2 "self-check failed"

echo "=== results: $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
