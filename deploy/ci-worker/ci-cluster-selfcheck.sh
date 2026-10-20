#!/usr/bin/env bash
# Hermetic end-to-end checks: real dispatcher/runner; fake Fly and guest setup.
# Default runs EVERY case. CI_SELFCHECK_CASES is only for targeted mutation/red
# receipts; unknown/empty selections fail instead of producing a zero-test green.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER="${CI_CLUSTER_UNDER_TEST:-$HERE/ci-cluster.sh}"
RUNCI="${CI_RUNCI_UNDER_TEST:-$HERE/runci.sh}"
TESTKIT="$HERE/ci-cluster-testkit.py"
SIGNAL_TESTKIT="${CI_SIGNAL_TESTKIT_UNDER_TEST:-$TESTKIT}"
[ -f "$CLUSTER" ] && [ -f "$RUNCI" ] && [ -f "$TESTKIT" ] || { printf 'selfcheck: missing fixture or script\n' >&2; exit 2; }
TMP="$(mktemp -d "${TMPDIR:-/tmp}/ci-cluster-selfcheck.XXXXXX")" || exit 2
trap '[ -n "${CI_KEEP_TMP:-}" ] || rm -rf "$TMP"' EXIT
FULL="$(git -C "$HERE/../.." rev-parse HEAD)" || exit 2
SHORT="$(git -C "$HERE/../.." rev-parse --short "$FULL")" || exit 2
RUNCI_LOCAL="$(dirname "$CLUSTER")/runci.sh"
if command -v md5sum >/dev/null 2>&1; then MD5="$(md5sum "$RUNCI_LOCAL" | cut -d ' ' -f 1)"; else MD5="$(md5 -q "$RUNCI_LOCAL")"; fi
export FAKE_MD5="$MD5" FAKE_SHORT="$SHORT"
export CI_REAL_GIT="$(command -v git)" CI_TESTKIT="$TESTKIT" CI_REAL_OD="$(command -v od)"
mkdir -p "$TMP/bin"
cat >"$TMP/bin/fly" <<'FAKE'
#!/usr/bin/env bash
# Execute the rendered probe in this shell against a faithful pgrep function.
# The separate parser case checks all 0/1/2 statuses outside the watchdog's
# short control-call deadline; do not spawn that validator on every probe.
id=""; cmd=""; args=("$@")
while [ "$#" -gt 0 ]; do
  case "$1" in --machine) id="$2"; shift ;; -C) cmd="$2"; shift ;; esac
  shift
done
case "$cmd" in
  pgrep*|sh\ -c*pgrep*)
    printf 'ssh console --machine %s -C %s\n' "$id" "$cmd" >>"$FAKE_DIR/calls.log"
    printf 'PROBE %s %s\n' "$id" "$cmd" >>"$FAKE_DIR/events.log"
    if [ "$(<"$FAKE_DIR/state-$id")" != started ]; then printf 'machine is stopped\n' >&2; exit 1; fi
    hang_probe() {
      printf 'HANG %s %s\n' "$1" "$$" >>"$FAKE_DIR/events.log"
      trap 'printf "TERMINATED %s %s 15\n" "$1" "$$" >>"$FAKE_DIR/events.log"; exit 143' TERM
      while :; do sleep 0.1; done
    }
    case "$cmd" in
      *"$FAKE_SHORT"*) [ ! -f "$FAKE_DIR/slowpgrep-$id" ] || hang_probe "probe-$id" ;;
      *) [ ! -f "$FAKE_DIR/hangbusy-$id" ] || hang_probe "busy-$id" ;;
    esac
    if [ -f "$FAKE_DIR/unreach-$id" ]; then printf 'connection refused\n' >&2; exit 1; fi
    pgrep() {
      if [ "$#" -ne 2 ] || [ "$1" != -f ]; then
        printf 'pgrep: only one pattern can be provided\n' >&2; return 2
      fi
      local pattern="$2" line n=0 matched=1
      if [ -f "$FAKE_DIR/procs-$id" ]; then
        while IFS= read -r line; do
          n=$((n + 1))
          if [[ "$line" =~ $pattern ]]; then printf '%s\n' "$n"; matched=0; fi
        done <"$FAKE_DIR/procs-$id"
      fi
      if [ "$matched" -eq 0 ]; then
        printf 'PROBE_RESULT %s alive\n' "$id" >>"$FAKE_DIR/events.log"
      else
        printf 'PROBE_RESULT %s gone\n' "$id" >>"$FAKE_DIR/events.log"
      fi
      return "$matched"
    }
    # Input is only the command emitted by the repository dispatcher/mutants,
    # never arbitrary external data. Parse its argv, then execute only the
    # expected pgrep or sh -c probe form; no real host pgrep is called.
    eval "set -- $cmd"
    if [ "$1" = sh ] && [ "$2" = -c ] && [ "$#" -eq 3 ]; then eval "$3"
    elif [ "$1" = pgrep ]; then pgrep "${@:2}"
    else printf 'unsupported probe form\n' >&2; exit 2; fi
    exit "$?" ;;
esac
exec python3 -I "$CI_TESTKIT" fake "${args[@]}"
FAKE
cat >"$TMP/bin/git" <<'GIT'
#!/usr/bin/env bash
# Network is outside the unit boundary. All local git resolution remains real.
case " $* " in *' fetch '*) exit 0 ;; esac
exec "$CI_REAL_GIT" "$@"
GIT
cat >"$TMP/bin/hostname" <<'HOST'
#!/usr/bin/env bash
if [ -f "$FAKE_DIR/hostname" ]; then printf '%s\n' "$(<"$FAKE_DIR/hostname")"; else /usr/bin/hostname "$@"; fi
HOST
cat >"$TMP/bin/od" <<'OD'
#!/usr/bin/env bash
if [ -f "$FAKE_DIR/fail-entropy" ]; then
  printf 'fixture entropy read failed\n' >&2
  exit 1
fi
exec "$CI_REAL_OD" "$@"
OD
chmod +x "$TMP/bin/fly" "$TMP/bin/git" "$TMP/bin/hostname" "$TMP/bin/od"
FAIL=0; COUNT=0
fail() { printf 'selfcheck FAIL: (%s) %s\n' "$CASE" "$*" >&2; FAIL=1; CASE_FAIL=1; }
expect() { # exact exit and reason; expectation comes from task/review contract
  [ "$RC" -eq "$1" ] || fail "expected exit $1, got $RC: $(grep -m1 'ERROR\|STALLED\|FAIL' "$OUT")"
  grep -qE "$2" "$OUT" || fail "expected diagnostic: $2"
}
contains() { grep -qE "$2" "$1" || fail "expected $2 in $1"; }
absent() { if grep -qE "$2" "$1"; then fail "unexpected $2 in $1"; fi; }
touch_flag() { touch "$FAKE_DIR/$1-mA"; }
touches_machine() {
  # Only actual addressing positions, not hostname inside OWNER_TOKEN (6c).
  grep -Eq "^machine (status|start|stop) $1( |$)|--machine $1( |$)" "$FAKE_DIR/calls.log"
}
ran_gate() {
  grep -Eq -- "--machine $1 -C ['\"]?/cache/runci.sh $FULL $2( |['\"]?$)" "$FAKE_DIR/calls.log"
}
new_case() {
  CASE="$1"; CASE_FAIL=0; COUNT=$((COUNT + 1))
  export FAKE_DIR="$TMP/c$CASE"
  mkdir -p "$FAKE_DIR/remote"
  : >"$FAKE_DIR/calls.log"; : >"$FAKE_DIR/events.log"; : >"$FAKE_DIR/stops.log"
  printf stopped >"$FAKE_DIR/state-mA"; printf stopped >"$FAKE_DIR/state-mB"
  OUT="$FAKE_DIR/dispatcher.log"
  printf 'selfcheck RUN: (%s)\n' "$CASE"
}
run_cluster() { # tiers [interrupt point]; real dispatcher, independent outer cap
  PATH="$TMP/bin:$PATH" CI_CLUSTER_APP=selfcheck CI_CLUSTER_START_TIMEOUT=5 \
    CI_CLUSTER_LOG_DIR="$FAKE_DIR/logs" CI_CLUSTER_TIERS="$1" \
    CI_CLUSTER_WATCH_INTERVAL=1 CI_CLUSTER_DEAD_STRIKES=2 \
    CI_CLUSTER_PROBE_TIMEOUT=2 CI_CLUSTER_RECOVER_TIMEOUT=2 \
    FIXTURE_INTERRUPT="${2:-}" GIT_TERMINAL_PROMPT=0 \
    python3 -I "$TESTKIT" run 30 "$OUT" bash "$CLUSTER" "$FULL"
  RC=$?
}
run_default() { # optional per-tier env override, full default map otherwise
  env -u CI_CLUSTER_TIERS -u CI_CLUSTER_GO_MACHINE -u CI_CLUSTER_NODE_MACHINE -u CI_CLUSTER_XPLAT_MACHINE \
    "$@" PATH="$TMP/bin:$PATH" CI_CLUSTER_APP=selfcheck CI_CLUSTER_START_TIMEOUT=5 \
    CI_CLUSTER_LOG_DIR="$FAKE_DIR/logs" CI_CLUSTER_WATCH_INTERVAL=1 \
    CI_CLUSTER_DEAD_STRIKES=2 CI_CLUSTER_PROBE_TIMEOUT=2 CI_CLUSTER_RECOVER_TIMEOUT=2 \
    python3 -I "$TESTKIT" run 30 "$OUT" bash "$CLUSTER" "$FULL"
  RC=$?
}
no_leaks() { absent "$FAKE_DIR/events.log" '^LEAK '; }
assert_copy_before_stop() {
  if ! python3 -I - "$FAKE_DIR/events.log" <<'PY'
import sys
rows = open(sys.argv[1]).read().splitlines()
copy = [n for n, row in enumerate(rows) if row.startswith('COPIED mA ')]
stop = [n for n, row in enumerate(rows) if row == 'STOP mA']
sys.exit(0 if copy and len(stop) == 1 and max(copy) < stop[0] else 1)
PY
  then fail 'evidence was not copied before the machine stop'; fi
}
TIERS=$'go|mA|gofmt\nxplat|mB|cli-verb-guard'
GO_ID=080d74db170408; NODE_ID=d8d0d90a1e9648; XPLAT_ID=78113d4f329908
ALL_CASES='1 2 3 4 5 6a 6b 6c 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45'
SELECTED="${CI_SELFCHECK_CASES-$ALL_CASES}"
[ -n "$SELECTED" ] || { printf 'selfcheck: empty case selection\n' >&2; exit 2; }
for CASE in $SELECTED; do
  case " $ALL_CASES " in *" $CASE "*) ;; *) printf 'selfcheck: unknown case %s\n' "$CASE" >&2; exit 2 ;; esac
  new_case "$CASE"
  case "$CASE" in
    1)
      printf started >"$FAKE_DIR/state-mB"
      run_cluster "$TIERS"; expect 0 'OVERALL: PASS'
      contains "$FAKE_DIR/stops.log" '^STOP mA$'; absent "$FAKE_DIR/stops.log" '^STOP mB$'
      absent "$FAKE_DIR/calls.log" '^machine start mB '
      [ -s "$FAKE_DIR/owner-mA" ] || fail 'started machine has no owner marker'
      [ ! -f "$FAKE_DIR/owner-mB" ] || fail 'already-running machine was claimed'
      assert_copy_before_stop; no_leaks ;;
    2)
      printf unreadable >"$FAKE_DIR/state-mA"
      run_cluster "$TIERS"; expect 2 'cannot read the state of machine'
      absent "$FAKE_DIR/calls.log" '^machine (start|stop) ' ;;
    3)
      touch_flag die
      run_cluster "$TIERS"; expect 1 'STALLED/DIED'
      contains "$OUT" 'tier go job exited 1'; absent "$OUT" 'tier go job exited 0' ;;
    4)
      touch_flag foreign
      run_cluster "$TIERS"; expect 0 'OVERALL: PASS'
      absent "$FAKE_DIR/stops.log" '^STOP mA$'; contains "$FAKE_DIR/stops.log" '^STOP mB$' ;;
    5)
      printf 'bash /cache/runci.sh foreign-ref other-gate foreign-token\n' >"$FAKE_DIR/procs-mA"
      run_cluster "$TIERS"; expect 0 'OVERALL: PASS'
      absent "$FAKE_DIR/stops.log" '^STOP mA$'; contains "$OUT" 'running a gate'
      contains "$FAKE_DIR/stops.log" '^STOP mB$' ;;
    6a|6b|6c)
      for m in "$GO_ID" "$NODE_ID" "$XPLAT_ID" mGo; do printf stopped >"$FAKE_DIR/state-$m"; done
      case "$CASE" in
        6a) run_default ;;
        6b) run_default CI_CLUSTER_NODE_MACHINE= ;;
        6c) printf '%s\n' "$GO_ID" >"$FAKE_DIR/hostname"; run_default CI_CLUSTER_GO_MACHINE=mGo ;;
      esac
      expect 0 'OVERALL: PASS'
      if [ "$CASE" = 6a ]; then
        for g in gofmt go-build go-vet lint go-test go-race; do ran_gate "$GO_ID" "$g" || fail "go tier omitted $g"; done
        for g in contracts spa; do ran_gate "$NODE_ID" "$g" || fail "node tier omitted $g"; done
        for g in embed-build records-no-sqlite cli-verb-guard; do ran_gate "$XPLAT_ID" "$g" || fail "xplat tier omitted $g"; done
        [ "$(grep -c '^machine start ' "$FAKE_DIR/calls.log")" -eq 3 ] || fail 'default map did not start exactly three machines'
      elif [ "$CASE" = 6b ]; then
        touches_machine "$NODE_ID" && fail 'node machine touched despite explicit empty id'
        absent "$FAKE_DIR/calls.log" '/cache/runci[.]sh [^ ]+ contracts( |$)'
        ran_gate "$GO_ID" go-race || fail 'go control omitted'; ran_gate "$XPLAT_ID" cli-verb-guard || fail 'xplat control omitted'
      else
        touches_machine "$GO_ID" && fail "the go tier's default machine was touched despite an override"
        ran_gate mGo go-race || fail 'overridden go machine omitted'; ran_gate "$NODE_ID" spa || fail 'node control omitted'
      fi ;;
    7)
      touch_flag hang
      run_cluster "$TIERS"; expect 1 'watchdog:gone'
      contains "$OUT" 'FAIL:stalled'; contains "$FAKE_DIR/events.log" '^PROBE '
      contains "$FAKE_DIR/events.log" '^TERMINATED console-mA '
      contains "$FAKE_DIR/stops.log" '^STOP mA$'; assert_copy_before_stop; no_leaks ;;
    8)
      touch_flag recoverpass
      run_cluster "$TIERS"; expect 0 'verdict recovered from'
      contains "$OUT" 'OVERALL: PASS'; no_leaks ;;
    9|12)
      if [ "$CASE" = 9 ]; then touch_flag slow; else touch_flag queued; fi
      run_cluster "$TIERS"; expect 0 'OVERALL: PASS'
      [ "$(grep -c '^PROBE_RESULT mA alive$' "$FAKE_DIR/events.log")" -ge 4 ] || fail 'healthy/queued gate lacked four live probes'
      absent "$OUT" 'STALLED/DIED'; absent "$FAKE_DIR/events.log" '^TERMINATED console-mA '
      contains "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks ;;
    10)
      touch_flag killed
      run_cluster "$TIERS"; expect 1 'FAIL:killed-by-signal'
      absent "$FAKE_DIR/logs/go-gofmt.log" 'CI-CLUSTER: STALLED/DIED'; no_leaks ;;
    11)
      python3 -I "$SIGNAL_TESTKIT" signals "$RUNCI" "$FAKE_DIR/real-runner" "$FULL" >"$FAKE_DIR/signals.log" 2>&1
      RC=$?; [ "$RC" -eq 0 ] || fail "real runner signal fixture failed: $(<"$FAKE_DIR/signals.log")"
      for sig in HUP INT TERM; do contains "$FAKE_DIR/signals.log" "PASS real runner $sig:"; done
      cat "$FAKE_DIR/signals.log" ;;
    13)
      touch_flag predead
      printf 'HEAD: %s old attempt\nALL GATES GREEN\n' "$SHORT" >"$FAKE_DIR/remote/gofmt@$FULL-old.log"
      run_cluster "$TIERS"; expect 1 'FAIL:stalled'
      absent "$OUT" 'verdict recovered|OVERALL: PASS'; absent "$OUT" '^go +mA +gofmt +PASS'
      contains "$OUT" '^xplat +mB +cli-verb-guard +PASS'; no_leaks ;;
    14)
      touch_flag hang; touch_flag slowpgrep
      run_cluster "$TIERS"; expect 1 'watchdog:unreachable'
      contains "$FAKE_DIR/logs/probe-go-gofmt.err" 'CONTROL TIMEOUT'
      contains "$OUT" 'FAIL:stalled'; no_leaks ;;
    15)
      touch_flag recoverfail
      run_cluster "$TIERS"; expect 1 'FAIL:gate'
      contains "$OUT" 'verdict recovered from'; absent "$OUT" 'FAIL:stalled'; no_leaks ;;
    16)
      touch_flag hang
      printf 'bash /cache/runci.sh %s gofmt other-attempt\n' "$FULL" >"$FAKE_DIR/procs-mA"
      run_cluster "$TIERS"; expect 1 'watchdog:gone'
      contains "$OUT" 'FAIL:stalled'; absent "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks ;;
    17)
      touch_flag remotekilled
      run_cluster "$TIERS"; expect 1 'FAIL:killed-by-signal'
      contains "$FAKE_DIR/logs/go-gofmt.recovered.log" '^RUNCI: KILLED by signal HUP'
      absent "$OUT" 'FAIL:stalled'; no_leaks ;;
    18)
      touch_flag verdicthang; touch_flag ignoreterm
      run_cluster "$TIERS"; expect 0 'OVERALL: PASS'
      contains "$FAKE_DIR/events.log" '^HANG console-mA '
      no_leaks; assert_copy_before_stop ;;
    19|20)
      touch_flag hang; touch_flag "hang$( [ "$CASE" = 19 ] && printf recovery || printf collection )"
      run_cluster "$TIERS"; expect 1 'FAIL:stalled'
      if [ "$CASE" = 19 ]; then
        contains "$FAKE_DIR/logs/go-gofmt.recovered.log.transfer.err" 'CONTROL TIMEOUT'
        # Recovery failed, but the later collection can succeed: once that
        # evidence is verified, stopping our idle machine is correct.
        assert_copy_before_stop
      else
        contains "$OUT" 'LEFT RUNNING'; absent "$FAKE_DIR/stops.log" '^STOP mA$'
      fi
      no_leaks ;;
    21|22|23|24|25|26)
      case "$CASE" in 21) mode=wrongtoken ;; 22) mode=wronggate ;; 23) mode=wronghead ;; 24) mode=emptycopy ;; 25) mode=headlesscopy ;; 26) mode=wrongref ;; esac
      touch_flag recoverpass; touch_flag "$mode"
      run_cluster "$TIERS"; expect 1 'FAIL:stalled'
      absent "$OUT" 'verdict recovered|OVERALL: PASS'
      contains "$OUT" 'LEFT RUNNING'; absent "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks ;;
    27|28|29)
      touch_flag hang
      case "$CASE" in
        27) point=gate ;;
        28) touch_flag slowpgrep; point=probe ;;
        29) touch_flag interruptcollection; point=collection ;;
      esac
      run_cluster "$TIERS" "$point"
      [ "$RC" -ne 0 ] || fail 'interrupted dispatcher unexpectedly passed'
      contains "$FAKE_DIR/events.log" '^INTERRUPTED dispatcher TERM$'
      absent "$OUT" 'FIXTURE CAP|OVERALL: PASS'; no_leaks
      if [ "$CASE" = 29 ]; then
        absent "$FAKE_DIR/stops.log" '^STOP mA$'
      else
        contains "$FAKE_DIR/stops.log" '^STOP mA$'; assert_copy_before_stop
      fi ;;
    30)
      touch_flag recoverpass; touch_flag wrongpath
      run_cluster "$TIERS"; expect 1 'FAIL:stalled'
      absent "$OUT" 'verdict recovered|OVERALL: PASS'; absent "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks ;;
    31|32)
      if [ "$CASE" = 31 ]; then touch_flag hangowner; else touch_flag hangbusy; fi
      run_cluster "$TIERS"; expect 0 'OVERALL: PASS'
      # The tested gate passed, but unreadable ownership/idleness is NOT
      # permission to stop its machine. The control call itself must finish.
      absent "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks
      if [ "$CASE" = 31 ]; then contains "$OUT" 'ownership unreadable'; else contains "$OUT" 'running a gate'; fi ;;
    33|34|35)
      touch_flag hang
      if [ "$CASE" = 33 ]; then touch_flag foreignref
      elif [ "$CASE" = 34 ]; then touch_flag foreigngate
      else touch_flag tokenprefix; fi
      run_cluster "$TIERS"; expect 1 'watchdog:gone'
      contains "$OUT" 'FAIL:stalled'; absent "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks ;;
    36)
      # Seed a real success, then reuse that exact evidence directory while
      # entropy fails. No bytes from the previous invocation may be consulted
      # as the current outcome; no new runner may start on refusal.
      run_cluster 'go|mA|gofmt'; expect 0 'OVERALL: PASS'
      cp "$OUT" "$FAKE_DIR/first-success.log"
      cp -R "$FAKE_DIR/logs" "$FAKE_DIR/old-evidence"
      before=$(grep -c "/cache/runci[.]sh $FULL gofmt " "$FAKE_DIR/calls.log")
      touch "$FAKE_DIR/fail-entropy"
      run_cluster 'go|mA|gofmt'
      [ "$RC" -eq 2 ] || fail "reused old PASS after failed attempt creation: expected refusal exit 2, got $RC"
      contains "$OUT" 'refusing existing or unwritable log directory'
      absent "$OUT" 'OVERALL: PASS|^go +mA +gofmt +PASS'
      after=$(grep -c "/cache/runci[.]sh $FULL gofmt " "$FAKE_DIR/calls.log")
      [ "$after" -eq "$before" ] || fail 'refused namespace dispatched a new runner'
      diff -r "$FAKE_DIR/old-evidence" "$FAKE_DIR/logs" >"$FAKE_DIR/evidence.diff" || fail 'reused run changed previous evidence'
      no_leaks ;;
    37)
      touch "$FAKE_DIR/fail-entropy"
      run_cluster 'go|mA|gofmt'; expect 1 'FAIL:dispatch'
      contains "$OUT" 'local tier job failed'
      contains "$FAKE_DIR/logs/go-gofmt.log" 'DISPATCH FAILED.*attempt identity'
      absent "$OUT" 'OVERALL: PASS|^go +mA +gofmt +PASS'
      absent "$FAKE_DIR/calls.log" '/cache/runci[.]sh [^ ]+ gofmt |ssh sftp get'
      contains "$FAKE_DIR/stops.log" '^STOP mA$'; no_leaks ;;
    38)
      # A real TERM handler with the correct trailer but NO exit must not be
      # accepted because fixture cleanup happens to produce 143.
      mutant="$FAKE_DIR/term-hang-runner.sh"
      python3 -I - "$RUNCI" "$mutant" <<'PY'
from pathlib import Path
import sys
p = Path(sys.argv[1]); text = p.read_text()
lines = text.splitlines(True)
matched = [n for n, line in enumerate(lines) if line.startswith("trap ") and "RUNCI: KILLED by signal TERM" in line]
assert len(matched) == 1
n = matched[0]
assert "exit 143" in lines[n]
lines[n] = lines[n].replace("exit 143", "trap \"\" TERM; while :; do sleep 0.1; done")
Path(sys.argv[2]).write_text("".join(lines))
PY
      python3 -I "$SIGNAL_TESTKIT" signals "$mutant" "$FAKE_DIR/term-hang" "$FULL" >"$FAKE_DIR/term-hang.log" 2>&1
      RC=$?; [ "$RC" -eq 1 ] || fail "TERM trailer-then-hang fixture must fail: expected 1, got $RC"
      contains "$FAKE_DIR/term-hang.log" 'FAIL real runner TERM: timed out'
      contains "$FAKE_DIR/term-hang.log" 'PASS real runner HUP:'
      contains "$FAKE_DIR/term-hang.log" 'PASS real runner INT:'
      absent "$FAKE_DIR/term-hang.log" 'PASS real runner TERM:'
      contains "$FAKE_DIR/term-hang/TERM.outcome.json" '"timed_out": true'
      contains "$FAKE_DIR/term-hang/TERM.outcome.json" '"passed": false' ;;
    39|40|41|42)
      if [ "$CASE" = 39 ] || [ "$CASE" = 40 ]; then
        touch_flag recoverpass
        if [ "$CASE" = 39 ]; then touch_flag partialfailrecovery; else touch_flag partialhangrecovery; fi
        run_cluster 'go|mA|gofmt'; expect 1 'FAIL:stalled'
        absent "$OUT" 'verdict recovered|OVERALL: PASS'
        contains "$FAKE_DIR/logs/go-gofmt.recovered.log" '^ALL GATES GREEN$'
        contains "$FAKE_DIR/logs/go-gofmt.recovered.log" '^ATTEMPT: '
      else
        if [ "$CASE" = 41 ]; then touch_flag partialfailcollection; else touch_flag partialhangcollection; fi
        run_cluster 'go|mA|gofmt'; expect 1 'OVERALL: FAIL'
        contains "$FAKE_DIR/logs/worker/go-gofmt.log" '^ALL GATES GREEN$'
        contains "$FAKE_DIR/logs/worker/go-gofmt.log" '^ATTEMPT: '
        absent "$FAKE_DIR/stops.log" '^STOP mA$'; contains "$OUT" 'LEFT RUNNING'
      fi
      contains "$FAKE_DIR/events.log" '^PARTIAL_VALID_BYTES '
      no_leaks ;;
    43)
      python3 -I "$TESTKIT" identity "$RUNCI" "$FAKE_DIR/root-runner" "$FULL" >"$FAKE_DIR/identity.log" 2>&1
      RC=$?; [ "$RC" -eq 0 ] || fail "real runner identity fixture failed: $(<"$FAKE_DIR/identity.log")"
      [ "$(grep -c '^PASS real runner identity:' "$FAKE_DIR/identity.log")" -eq 2 ] || fail 'two distinct real producer/re-exec controls did not pass'
      cat "$FAKE_DIR/identity.log" ;;
    44)
      touch_flag killtier
      run_cluster 'go|mA|gofmt'; expect 1 'local tier job failed'
      contains "$OUT" '^go +mA +gofmt +PASS'
      absent "$OUT" 'OVERALL: PASS'
      contains "$FAKE_DIR/events.log" '^INTERRUPTED tier '
      assert_copy_before_stop; no_leaks ;;
    45)
      touch_flag slow
      run_cluster 'go|mA|gofmt'; expect 0 'OVERALL: PASS'
      python3 -I "$TESTKIT" probe-contract "$FAKE_DIR/events.log" "$FAKE_DIR/probe-contract" >"$FAKE_DIR/probe-contract.log" 2>&1
      RC=$?; [ "$RC" -eq 0 ] || fail "rendered shell/argv status contract failed: $(<"$FAKE_DIR/probe-contract.log")"
      contains "$FAKE_DIR/probe-contract.log" 'PASS rendered probe statuses 0/1/2' ;;
  esac
  if [ "$CASE_FAIL" -eq 0 ]; then printf 'selfcheck ok:   (%s)\n' "$CASE"; fi
  printf '%s\n' "$CASE_FAIL" >"$FAKE_DIR/case.exit"
done
printf 'selfcheck: cases=%s failures=%s evidence=%s\n' "$COUNT" "$FAIL" "$TMP"
[ "$FAIL" -eq 0 ] || exit 1
[ "$COUNT" -gt 0 ] || exit 2
printf 'selfcheck: OK\n'
