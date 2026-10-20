#!/usr/bin/env bash
# Proof-of-failure companion: full real suite once, focused behavioral mutants.
# Every mutant has a same-layout positive control and a reason-specific red;
# targeting a mutation case is explicit, counted, and never affects default CI
# discovery of the complete selfcheck.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/.." && pwd)"
GUARD="$SCRIPT_DIR/check-ci-cluster-dispatcher.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ci-cluster-dispatcher-test.XXXXXX")" || exit 2
trap '[ -n "${CI_KEEP_TMP:-}" ] || rm -rf "$TMP_DIR"' EXIT
GIT_DIR_ABS="$(git -C "$REPO" rev-parse --absolute-git-dir)"
PASS=0; FAIL=0
check() {
  if [ "$3" -eq "$2" ]; then printf '  PASS [%s]: exit %s\n' "$1" "$3"; PASS=$((PASS + 1))
  else printf '  FAIL [%s]: expected %s, got %s\n' "$1" "$2" "$3"; FAIL=$((FAIL + 1)); fi
}
new_tree() {
  TREE="$TMP_DIR/$1/deploy/ci-worker"
  mkdir -p "$TREE"
  cp "$REPO/deploy/ci-worker/runci.sh" "$TREE/runci.sh"
  cp "$REPO/deploy/ci-worker/ci-cluster.sh" "$TREE/ci-cluster.sh"
}
run_guard() { # output-name cases dispatcher [runner]
  GIT_DIR="$GIT_DIR_ABS" CI_SELFCHECK_CASES="$2" CI_CLUSTER_UNDER_TEST="$3" \
    CI_RUNCI_UNDER_TEST="${4:-$REPO/deploy/ci-worker/runci.sh}" \
    bash "$GUARD" >"$TMP_DIR/$1.log" 2>&1
  RC=$?
}
mutation_check() { # name cases dispatcher reason [runner]
  local name="$1" cases="$2" script="$3" reason="$4" runner="${5:-$REPO/deploy/ci-worker/runci.sh}"
  run_guard "$name-control" "$cases" "$TMP_DIR/control/deploy/ci-worker/ci-cluster.sh"
  check "$name-control" 0 "$RC"
  run_guard "$name" "$cases" "$script" "$runner"
  check "$name-red" 1 "$RC"
  if grep -qE "$reason" "$TMP_DIR/$name.log"; then
    printf '  PASS [%s-reason]: %s\n' "$name" "$reason"; PASS=$((PASS + 1))
  else
    printf '  FAIL [%s-reason]: expected %s\n' "$name" "$reason"; FAIL=$((FAIL + 1))
    printf '%s\n' "$(<"$TMP_DIR/$name.log")"
  fi
}
printf '=== check-ci-cluster-dispatcher proof of failure ===\n'
# No case filter on this call: every case must execute in the normal gate.
env -u CI_SELFCHECK_CASES bash "$GUARD" >"$TMP_DIR/full.log" 2>&1; rc=$?
check full-real-selfcheck 0 "$rc"
if [ "$rc" -ne 0 ]; then printf '%s\n' "$(<"$TMP_DIR/full.log")"; fi
new_tree control
new_tree state-json
python3 -I - "$TREE/ci-cluster.sh" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); s = p.read_text()
old = 'fly machine status "$1" --app "$APP" 2>/dev/null'
assert s.count(old) == 1
p.write_text(s.replace(old, 'fly machine status "$1" --app "$APP" --json 2>/dev/null'))
PY
mutation_check state-json 1 "$TREE/ci-cluster.sh" 'cannot read the state of machine'
mkdir -p "$TMP_DIR/empty"
REPO_ROOT="$TMP_DIR/empty" bash "$GUARD" >"$TMP_DIR/missing.log" 2>&1; check missing-selfcheck 2 "$?"
new_tree go-override
python3 -I - "$TREE/ci-cluster.sh" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); s = p.read_text(); old = '${CI_CLUSTER_GO_MACHINE-080d74db170408}'
assert s.count(old) == 1
p.write_text(s.replace(old, '080d74db170408'))
PY
mutation_check go-override 6c "$TREE/ci-cluster.sh" 'default machine was touched despite an override'
new_tree no-watchdog
python3 -I - "$TREE/ci-cluster.sh" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); s = p.read_text(); old = 'main "$@"'
assert s.count('\n' + old + '\n') == 1
p.write_text(s.replace('\n' + old + '\n', '\nremote_gate_state() { REMOTE_GATE_STATE=alive; }\n' + old + '\n'))
PY
mutation_check no-watchdog 7 "$TREE/ci-cluster.sh" 'expected exit 1, got 143|expected diagnostic: watchdog:gone'
new_tree no-token
python3 -I - "$TREE/ci-cluster.sh" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); s = p.read_text(); old = ' $gate $token( |$)'
assert s.count(old) == 1
p.write_text(s.replace(old, ' $gate( |$)'))
PY
mutation_check no-token 16 "$TREE/ci-cluster.sh" 'expected exit 1, got 143|expected diagnostic: watchdog:gone'
new_tree no-traps
python3 -I - "$TREE/runci.sh" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); lines = p.read_text().splitlines(True)
removed = [line for line in lines if line.startswith('trap ') and 'RUNCI: KILLED by signal' in line]
assert len(removed) == 3
p.write_text(''.join(line for line in lines if line not in removed))
PY
mutation_check no-traps 11 "$TREE/ci-cluster.sh" 'real runner signal fixture failed|FAIL real runner' "$TREE/runci.sh"
new_tree no-reap
python3 -I - "$TREE/ci-cluster.sh" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); s = p.read_text()
old = '    if kill -0 "$CONSOLE_PID" 2>/dev/null; then\n      terminate_and_reap "$CONSOLE_PID"'
assert s.count(old) == 1
p.write_text(s.replace(old, '    if kill -0 "$CONSOLE_PID" 2>/dev/null; then\n      : # MUTANT: abandon the verdict-present child'))
PY
mutation_check no-reap 18 "$TREE/ci-cluster.sh" 'unexpected \^LEAK'
printf 'Results: %s passed, %s failed; evidence=%s\n' "$PASS" "$FAIL" "$TMP_DIR"
[ "$FAIL" -eq 0 ] || exit 1
printf 'All assertions passed.\n'
