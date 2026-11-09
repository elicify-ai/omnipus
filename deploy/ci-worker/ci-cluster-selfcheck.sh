#!/usr/bin/env bash
# ci-cluster-selfcheck.sh - prove ci-cluster.sh's machine ownership and failure
# reporting against a FAKE `fly` (no network, no machines, no cost).
#
# The real dispatcher once read a machine's state with a flag flyctl does not
# have, so every machine looked "unknown", was "started" (a no-op on a running
# one), recorded as ITS OWN, and stopped at exit - killing runs of other sessions
# (2026-10-11). Each case below fails against that old behaviour.
#
#   1. a machine already STARTED on arrival is used but never started/stopped/claimed;
#      a machine STOPPED on arrival is started, claimed (owner marker) and stopped
#   2. a machine whose state cannot be read is refused (exit 2) and never touched
#   3. a tier whose machine dies mid-run FAILS (tier job exit non-zero, OVERALL: FAIL)
#   4. a machine we started that now carries ANOTHER dispatcher's marker is not stopped
#   5. a machine we started that is running a gate (busy) at exit is not stopped
#   6. the DEFAULT tier map (no CI_CLUSTER_TIERS): the shipped machine ids and gates per tier, a
#      per-tier id override, and an explicitly EMPTY id skipping its tier while an UNSET one does not
#   7. (the shared-machine refusal: one tier per machine, zero Fly calls)
#   8. a gate whose console stays OPEN (it never ends by itself) with the remote gate
#      process GONE (the 2026-10-18 half-open hang) is probed, terminated BY THE
#      DISPATCHER and ends in the explicit STALLED/DIED verdict, exit 1, within the window;
#      a co-tenant's process for the same gate+ref under another attempt does not look alive
#   9. a console that dropped while THIS attempt's announced remote RUNLOG holds a verdict
#      is recovered and PASSes
#  10. a HEALTHY slow gate (a real stand-in process carrying this attempt's argv, probed
#      alive) survives the watchdog and PASSes - liveness, not an idle timer
#  11. the REAL runci.sh HUP/INT/TERM traps (taken from the file, run in an isolated child)
#      write the KILLED trailer and exit 129/130/143
#  12. that trailer from the real traps yields FAIL:killed-by-signal, no STALLED stamp over it
#  13. an old same-HEAD PASS on the machine is never adopted for a current attempt that
#      announced nothing; 14. nor a log that carries another attempt's id
#  15. a failing probe (pgrep error / transport) is 'unreachable', not a clean no-match
#  16. a hung liveness probe is bounded (timeout strikes) and ends in a named STALLED/DIED
#  17. a console that ignores TERM is killed and a hung recovery transfer is bounded
#
# Usage: bash deploy/ci-worker/ci-cluster-selfcheck.sh   (exit 0 = all cases behave)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER="${CI_CLUSTER_UNDER_TEST:-$HERE/ci-cluster.sh}"   # override only to prove the checks bite an older script
RUNCI="$HERE/runci.sh"
[ -f "$CLUSTER" ] || { echo "selfcheck: missing $CLUSTER" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/ci-cluster-selfcheck.XXXXXX")" || exit 2
trap 'pkill -f "$TMP" 2>/dev/null; rm -rf "$TMP"' EXIT
if command -v md5sum >/dev/null 2>&1; then MD5="$(md5sum "$RUNCI" | awk '{print $1}')"; else MD5="$(md5 -q "$RUNCI")"; fi
FULL="$(git -C "$HERE/../.." rev-parse HEAD)"
SHORT="$(git -C "$HERE/../.." rev-parse --short "$FULL")"

mkdir -p "$TMP/bin"
# The fake flyctl is written with a quoted heredoc and the two build-time values
# are passed through the environment, so nothing in it needs escaping.
cat >"$TMP/bin/fly" <<'FAKE'
#!/usr/bin/env bash
# fake flyctl: state lives in files under $FAKE_DIR
#   state-<id> machine state; owner-<id> owner marker;
#   die-<id> the console dies mid-gate (2026-10-11 machine-stop case);
#   hang-<id> the console stays OPEN with no verdict until the DISPATCHER terminates it
#             (2026-10-18 half-open session case). It never ends by itself: if the
#             watchdog is broken, the selfcheck's hard cap fails the case instead.
#             It records "console open" / "console terminated by TERM" in console-events;
#   ignoreterm-<id> (with hang) the console ignores TERM, so only KILL ends it;
#   noannounce-<id> the console prints NOTHING (no RUNLOG/ATTEMPT/HEAD announcement);
#   slow-<id> the console stays open ~5s and then completes (healthy long gate);
#   live-<id> a REAL stand-in process carrying this attempt's runci.sh argv exists while
#             the gate runs (probed by the REAL pgrep through the rendered command);
#   foreign-live-<id> a co-tenant's stand-in for the SAME ref+gate but ANOTHER attempt;
#   unreach-<id> the liveness probe fails at the transport (no sentinel);
#   pgreperr-<id> the probe's pgrep reports a usage/fatal error (PGREP_RC=2);
#   ptimeout-<id> the liveness probe hangs; sftphang-<id> the sftp transfer hangs;
#   killed-<id> the console emits the contents of trailer (captured from the REAL runci.sh traps);
#   runlog-<id> what sftp get delivers instead of the real remote log (@ATTEMPT@ is replaced
#               by the attempt id of the last dispatch); remote/ holds each attempt's real log
D="$FAKE_DIR"
echo "$*" >> "$D/calls.log"
arg() { local want="$1"; shift; while [ $# -gt 0 ]; do [ "$1" = "$want" ] && { echo "$2"; return; }; shift; done; }
standin() { # $1 file-listing-pids $2 ref $3 gate $4 attempt: a process whose argv looks like the remote runci.sh
  bash -c 'sleep 90; :' /cache/runci.sh "$2" "$3" "$4" "$FAKE_MARK" >/dev/null 2>&1 &
  echo $! >> "$1"
}
case "$1 $2" in
  "machine status")
    # Like the real flyctl (v0.4.104): `machine status <id> --app <name>` and nothing else. An
    # unknown flag (the 2026-10-11 bug passed --json) is an error with NO state on stdout.
    if [ "$#" -ne 5 ] || [ "$4" != "--app" ]; then echo "Error: unknown flag in: fly $*" >&2; exit 1; fi
    id="$3"; st=$(cat "$D/state-$id" 2>/dev/null)
    if [ -n "$st" ] && [ "$st" != "unreadable" ]; then printf 'Machine ID: %s\nState: %s\nHostStatus: ok\n' "$id" "$st"; fi
    exit 0 ;;
  "machine start") echo started > "$D/state-$3"; echo "$3 has been started"; exit 0 ;;
  "machine stop")  echo stopped > "$D/state-$3"; echo "STOP $3" >> "$D/stops.log"; exit 0 ;;
  "ssh console")
    id=$(arg --machine "$@"); cmd=$(arg -C "$@")
    st=$(cat "$D/state-$id" 2>/dev/null)
    if [ "$st" != "started" ]; then echo "Error: machine $id is not started"; exit 1; fi
    case "$cmd" in
      true) exit 0 ;;
      "md5sum /cache/runci.sh") echo "$FAKE_MD5  /cache/runci.sh"; exit 0 ;;
      *">"*"/tmp/ci-owner"*) tok=$(printf '%s' "$cmd" | sed -n "s/.*echo \([^ ]*\) > .*/\1/p"); echo "$tok" > "$D/owner-$id"; exit 0 ;;
      "cat /tmp/ci-owner") cat "$D/owner-$id" 2>/dev/null || exit 1; exit 0 ;;
      "sh -c "*pgrep*)
        # the dispatcher's liveness probe. The remote console splits -C into argv WITHOUT a
        # shell, so split it the same way (shlex) and run the result FOR REAL: the rendered
        # sh -c / pgrep command is executed against real process argv, not parsed by the fake.
        if [ -f "$D/ptimeout-$id" ]; then while :; do sleep 1; done; fi
        if [ -f "$D/unreach-$id" ]; then echo "Error: ssh shell: connection refused" >&2; exit 1; fi
        if [ -f "$D/pgreperr-$id" ]; then echo "pgrep: invalid pattern" >&2; echo "PGREP_RC=2"; exit 0; fi
        mapfile -d '' -t av < <(python3 -c 'import shlex,sys; sys.stdout.write("\0".join(shlex.split(sys.argv[1])) + "\0")' "$cmd")
        # The fake runs on the same host as the dispatcher, so pgrep would also see the fake
        # console processes themselves (their argv carries the dispatched command line). The
        # "remote machine" is modelled as only the stand-ins, which carry the selfcheck marker
        # AFTER the attempt id: the pattern is extended with it before it runs.
        av[2]="${av[2]/\';/ .*${FAKE_MARK}\';}"
        "${av[@]}"
        exit $? ;;
      pgrep*)
        # the busy-at-exit check (bare, single-quoted pattern): keepbusy- = a process that
        # outlives our gate (a foreign run); pids on stdout, exit 0 = match
        if [ -f "$D/keepbusy-$id" ]; then echo 4242; exit 0; fi
        exit 1 ;;
      /cache/runci.sh*)
        set -- $cmd
        ref=$2; gate=$3; attempt=$4
        mkdir -p "$D/remote"; rf="$D/remote/$gate@x.log"; : > "$rf"
        echo "$attempt" > "$D/lastattempt-$id"
        say() { echo "$1"; echo "$1" >> "$rf"; }
        if [ ! -f "$D/noannounce-$id" ]; then
          say "RUNLOG: /cache/logs/$gate@x.log"
          say "ATTEMPT: $attempt"
          say "HEAD: $FAKE_SHORT selfcheck"
        fi
        [ -f "$D/live-$id" ] && standin "$D/standin-pids-$id" "$ref" "$gate" "$attempt"
        [ -f "$D/foreign-live-$id" ] && standin "$D/foreign-pids-$id" "$ref" "$gate" "foreign-attempt-1"
        endgate() { [ -f "$D/standin-pids-$id" ] && kill $(cat "$D/standin-pids-$id") 2>/dev/null; rm -f "$D/standin-pids-$id"; }
        # case 4: another dispatcher takes the machine over while this gate runs - done
        # here, at a fixed point after our marker was written, not by a racing
        # background poller (which lost the race under load once this check ran in CI)
        if [ -f "$D/foreign-$id" ]; then echo "ci-cluster:99999:other:1" > "$D/owner-$id"; fi
        if [ -f "$D/die-$id" ]; then echo stopped > "$D/state-$id"; echo "Error: ssh shell: wait: remote command exited without exit status or exit signal"; exit 1; fi
        # half-open-session model: the console never returns and never prints a verdict, and it
        # NEVER ends by itself — only the dispatcher's termination (TERM, or KILL) ends it
        if [ -f "$D/hang-$id" ]; then
          echo $$ > "$D/console-pid"
          if [ -f "$D/ignoreterm-$id" ]; then trap '' TERM; else trap 'echo "console terminated by TERM" >> "$D/console-events"; exit 143' TERM; fi
          echo "console open" >> "$D/console-events"
          while :; do sleep 1; done
        fi
        # healthy-long-gate model: no output growth for ~5s (longer than several watchdog
        # probes), then a normal completion — a pure idle-timer watchdog would kill this; a
        # liveness one must not
        if [ -f "$D/slow-$id" ]; then sleep 5; endgate; say "ALL GATES GREEN"; exit 0; fi
        # the signal-death trailer produced by the REAL runci.sh trap handlers (captured by the
        # selfcheck's fixture), then the session ends without a verdict
        if [ -f "$D/killed-$id" ]; then say "$(cat "$D/trailer")"; endgate; exit 1; fi
        endgate
        say "ALL GATES GREEN"; exit 0 ;;
    esac
    exit 0 ;;
  "ssh sftp")
    id=$(arg --machine "$@")
    st=$(cat "$D/state-$id" 2>/dev/null)
    # a stopped machine serves no files — the real sftp get fails to connect
    if [ "$st" != "started" ]; then echo "Error: machine $id is not started" >&2; exit 1; fi
    if [ -f "$D/sftphang-$id" ]; then while :; do sleep 1; done; fi
    remote="$4"; dest="$5"
    if [ -f "$D/runlog-$id" ]; then
      sed "s/@ATTEMPT@/$(cat "$D/lastattempt-$id" 2>/dev/null)/g" "$D/runlog-$id" > "$dest"
    elif [ -f "$D/remote/$(basename "$remote")" ]; then
      cat "$D/remote/$(basename "$remote")" > "$dest"
    else
      echo "Error: no such file: $remote" >&2; exit 1
    fi
    exit 0 ;;
esac
echo "fake fly: unhandled: $*" >&2; exit 1
FAKE
# The owner token embeds hostname. Pin it as fixture input too, so a real
# Fly hostname equal to a default machine id cannot contaminate the call-log
# absence assertions. A genuinely wrong target id must still fail those checks.
cat >"$TMP/bin/hostname" <<'HOST'
#!/usr/bin/env bash
printf '%s\n' 'selfcheck-host'
HOST
chmod +x "$TMP/bin/fly" "$TMP/bin/hostname"
export FAKE_MD5="$MD5" FAKE_SHORT="$SHORT" FAKE_MARK="$TMP"

FAIL=0
fail() { echo "selfcheck FAIL: $*" >&2; FAIL=1; }
ok()   { echo "selfcheck ok:   $*"; }

# new_case <name>: fresh fake state dir
new_case() {
  export FAKE_DIR="$TMP/$1"; rm -rf "$FAKE_DIR"; mkdir -p "$FAKE_DIR"
  : > "$FAKE_DIR/calls.log"; : > "$FAKE_DIR/stops.log"
}
# run_cluster <tiers-spec>: sets RC and OUT
run_cluster() {
  OUT="$TMP/out.txt"
  PATH="$TMP/bin:$PATH" CI_CLUSTER_APP=selfcheck CI_CLUSTER_START_TIMEOUT=20 CI_CLUSTER_LOG_DIR="$FAKE_DIR/logs" \
    CI_CLUSTER_TIERS="$1" GIT_TERMINAL_PROMPT=0 bash "$CLUSTER" "$FULL" >"$OUT" 2>&1
  RC=$?
}
TIERS=$'go|mA|gofmt\nxplat|mB|cli-verb-guard'

# 1. started-on-arrival machine is never touched; stopped-on-arrival one is claimed and stopped
new_case c1; echo started > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
run_cluster "$TIERS"
[ "$RC" -eq 0 ] || fail "(1) expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
grep -q 'STOP mB' "$FAKE_DIR/stops.log" || fail "(1) the machine this run started (mB) was not stopped"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" && fail "(1) STOPPED a machine that was already running on arrival (mA)"
grep -q 'machine start mA' "$FAKE_DIR/calls.log" && fail "(1) tried to start the already-running machine mA"
grep -q 'ci-cluster:' "$FAKE_DIR/owner-mB" 2>/dev/null || fail "(1) no owner marker written on the machine this run started"
[ -f "$FAKE_DIR/owner-mA" ] && fail "(1) wrote an owner marker on a machine it did not start"
[ "$FAIL" -eq 0 ] && ok "(1) running machine untouched; stopped machine started, marked and stopped"

# 2. unreadable state -> refuse, touch nothing
new_case c2; echo unreadable > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
run_cluster "$TIERS"
[ "$RC" -eq 2 ] || fail "(2) expected pre-flight refusal (exit 2), got $RC"
grep -q 'machine start mA' "$FAKE_DIR/calls.log" && fail "(2) started a machine whose state could not be read"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" && fail "(2) stopped a machine whose state could not be read"
[ "$FAIL" -eq 0 ] && ok "(2) unreadable state refused, machine untouched"

# 3. machine dies mid-run -> tier fails
new_case c3; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"; touch "$FAKE_DIR/die-mA"
run_cluster $'go|mA|gofmt go-build\nxplat|mB|cli-verb-guard'
[ "$RC" -eq 1 ] || fail "(3) expected overall FAIL (exit 1), got $RC"
grep -q 'tier go job exited 1' "$OUT" || fail "(3) the dead tier did not report a non-zero job exit"
grep -q 'tier go job exited 0' "$OUT" && fail "(3) the dead tier reported 'exited 0'"
grep -q 'OVERALL: FAIL' "$OUT" || fail "(3) no OVERALL: FAIL"
grep -q 'STALLED/DIED' "$OUT" || fail "(3) the console-exit-no-verdict path did not say STALLED/DIED"
[ "$FAIL" -eq 0 ] && ok "(3) a machine stopped mid-run fails its tier with STALLED/DIED"

# 4. a machine we started now carries another dispatcher's marker -> not stopped
new_case c4; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
touch "$FAKE_DIR/foreign-mB"   # the fake fly overwrites mB's marker once our gate is running
run_cluster "$TIERS"
grep -q 'STOP mB' "$FAKE_DIR/stops.log" && fail "(4) stopped a machine carrying another dispatcher's marker"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(4) control: the machine with our own marker (mA) was not stopped"
[ "$FAIL" -eq 0 ] && ok "(4) foreign owner marker respected"

# 5. busy at exit -> not stopped (keepbusy: a process that outlives OUR gate —
#    our own gate's process is cleared by the fake console when it completes)
new_case c5; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"; touch "$FAKE_DIR/keepbusy-mB"
run_cluster "$TIERS"
grep -q 'STOP mB' "$FAKE_DIR/stops.log" && fail "(5) stopped a machine that is running a gate"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(5) control: the idle machine (mA) was not stopped"
[ "$FAIL" -eq 0 ] && ok "(5) busy machine left running"

# run_default <VAR=value>...: like run_cluster but with CI_CLUSTER_TIERS and the per-tier machine
# variables UNSET (the default map), then the given assignments applied.
run_default() {
  OUT="$TMP/out.txt"
  env -u CI_CLUSTER_TIERS -u CI_CLUSTER_GO_MACHINE -u CI_CLUSTER_NODE_MACHINE -u CI_CLUSTER_XPLAT_MACHINE "$@" \
    PATH="$TMP/bin:$PATH" CI_CLUSTER_APP=selfcheck CI_CLUSTER_START_TIMEOUT=20 CI_CLUSTER_LOG_DIR="$FAKE_DIR/logs" \
    GIT_TERMINAL_PROMPT=0 bash "$CLUSTER" "$FULL" >"$OUT" 2>&1
  RC=$?
}
ran_gate() { grep -q -- "--machine $1 -C /cache/runci.sh $FULL $2 [^ ]*\$" "$FAKE_DIR/calls.log"; } # machine gate

# The shipped placement (founder decisions 2026-10-12 and 2026-10-21,
# deploy/ci-worker/ci-cluster.sh::default_tier_map): SPA follows all Go gates on performance.
GO_ID=080d74db170408; NODE_ID=d8d0d90a1e9648; XPLAT_ID=78113d4f329908

# 6a. the default map: three tiers on their shipped machines with their shipped gates
new_case c6a; for m in $GO_ID $NODE_ID $XPLAT_ID; do echo stopped > "$FAKE_DIR/state-$m"; done
run_default
[ "$RC" -eq 0 ] || fail "(6a) default map: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
for g in gofmt go-build go-vet lint go-test go-race spa; do ran_gate $GO_ID $g || fail "(6a) go tier: gate $g did not run on $GO_ID"; done
ran_gate $NODE_ID contracts || fail "(6a) node tier: contracts did not run on $NODE_ID"
ran_gate $NODE_ID spa && fail "(6a) spa ran on the shared node machine"
GO_GATES=$(grep -- "--machine $GO_ID -C /cache/runci.sh $FULL " "$FAKE_DIR/calls.log" | awk '{print $(NF-1)}')
# Release placement: SPA follows all Go gates on performance. The standalone Go
# repair check was removed with the repair command (founder decision 2026-10-27).
# Keep this independent exact oracle: omissions, extras and reorderings all fail.
[ "$GO_GATES" = $'gofmt\ngo-build\ngo-vet\nlint\ngo-test\ngo-race\nspa' ] || fail "(6a) performance gates out of order: $GO_GATES"
for g in embed-build records-no-sqlite cli-verb-guard; do ran_gate $XPLAT_ID $g || fail "(6a) xplat tier: gate $g did not run on $XPLAT_ID"; done
[ "$(grep -c 'machine start' "$FAKE_DIR/calls.log")" -eq 3 ] || fail "(6a) the default map did not start exactly its three machines"
[ "$FAIL" -eq 0 ] && ok "(6a) default tier map accepted: spa follows all Go gates on $GO_ID; contracts stays on $NODE_ID"

# 6b. an explicitly EMPTY id skips its tier; the other two still run on their defaults
new_case c6b; for m in $GO_ID $XPLAT_ID; do echo stopped > "$FAKE_DIR/state-$m"; done
run_default CI_CLUSTER_NODE_MACHINE=
[ "$RC" -eq 0 ] || fail "(6b) empty node id: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
grep -q -- "$NODE_ID" "$FAKE_DIR/calls.log" && fail "(6b) the node machine was touched although its id was explicitly empty"
grep -q -- 'contracts' "$FAKE_DIR/calls.log" && fail "(6b) the node tier ran although its id was explicitly empty"
ran_gate $GO_ID go-race || fail "(6b) control: the go tier did not run on its default"
ran_gate $GO_ID spa || fail "(6b) spa did not run although only the contracts tier was skipped"
ran_gate $XPLAT_ID cli-verb-guard || fail "(6b) control: the xplat tier did not run on its default"
[ "$FAIL" -eq 0 ] && ok "(6b) explicitly empty id skips only its tier"

# 6c. a per-tier id override replaces only that tier's default
new_case c6c; for m in mGo $NODE_ID $XPLAT_ID; do echo stopped > "$FAKE_DIR/state-$m"; done
run_default CI_CLUSTER_GO_MACHINE=mGo
[ "$RC" -eq 0 ] || fail "(6c) go override: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
ran_gate mGo go-race || fail "(6c) the go tier did not run on the overridden machine"
ran_gate mGo spa || fail "(6c) spa did not follow the go tier's machine override"
grep -q -- "$GO_ID" "$FAKE_DIR/calls.log" && fail "(6c) the go tier's default machine was touched despite an override"
ran_gate $NODE_ID contracts || fail "(6c) control: the node tier did not run on its default"
[ "$FAIL" -eq 0 ] && ok "(6c) go override moves Go and SPA gates together"

# 6d. the other per-tier overrides still move only their own gates
new_case c6d; for m in $GO_ID mNode mXplat; do echo stopped > "$FAKE_DIR/state-$m"; done
run_default CI_CLUSTER_NODE_MACHINE=mNode CI_CLUSTER_XPLAT_MACHINE=mXplat
[ "$RC" -eq 0 ] || fail "(6d) node/xplat overrides: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
ran_gate mNode contracts || fail "(6d) contracts did not run on the overridden node machine"
ran_gate mXplat cli-verb-guard || fail "(6d) xplat did not run on the overridden machine"
ran_gate $GO_ID spa || fail "(6d) control: spa did not stay on the performance machine"
grep -q -e "$NODE_ID" -e "$XPLAT_ID" "$FAKE_DIR/calls.log" && fail "(6d) a default machine was touched despite its override"
[ "$FAIL" -eq 0 ] && ok "(6d) node and xplat overrides preserved"

# 7. the one-tier-per-machine invariant still refuses an overlapping map before any Fly call
new_case c7
run_cluster $'go|mA|gofmt\nnode|mA|spa'
[ "$RC" -eq 2 ] || fail "(7) shared machine: expected config refusal (exit 2), got $RC"
grep -q "tiers 'go' and 'node' share machine mA — one tier per machine" "$OUT" || fail "(7) refusal did not name the conflicting tiers and machine"
[ ! -s "$FAKE_DIR/calls.log" ] || fail "(7) Fly was called before rejecting the overlapping map"
[ "$FAIL" -eq 0 ] && ok "(7) shared machine refused (exit 2), zero Fly calls"

# ---- cases 8-17: the 2026-10-18 half-open-session hang, its verdicts, and what proves them ----
# Like run_cluster, but at the watchdog's fastest cadence AND with a hard cap (40s, SELFCHECK_HANG_CAP overrides)
# cap: the "dead" fake console NEVER ends by itself (it only ends when the
# dispatcher terminates it), so if the watchdog is broken the case must FAIL by
# cap (RC=143), not hang the selfcheck. RC=1/0 within the cap IS the no-hang proof.
HANG_CAP="${SELFCHECK_HANG_CAP:-40}"
run_cluster_bounded() { # $1 tiers-spec
  OUT="$TMP/out.txt"
  PATH="$TMP/bin:$PATH" CI_CLUSTER_APP=selfcheck CI_CLUSTER_START_TIMEOUT=20 CI_CLUSTER_LOG_DIR="$FAKE_DIR/logs" \
    CI_CLUSTER_TIERS="$1" CI_CLUSTER_WATCH_INTERVAL=1 CI_CLUSTER_DEAD_STRIKES=2 \
    CI_CLUSTER_CONTROL_TIMEOUT=2 CI_CLUSTER_REAP_GRACE=2 GIT_TERMINAL_PROMPT=0 \
    bash "$CLUSTER" "$FULL" >"$OUT" 2>&1 &
  local p=$! i=0
  while kill -0 "$p" 2>/dev/null && [ "$i" -lt "$HANG_CAP" ]; do sleep 1; i=$((i + 1)); done
  if kill -0 "$p" 2>/dev/null; then
    kill -9 "$p" 2>/dev/null; wait "$p" 2>/dev/null; RC=143
    echo "selfcheck: dispatcher was KILLED at the ${HANG_CAP}s cap — it HUNG (watchdog broken?)" >> "$OUT"
    return
  fi
  wait "$p"; RC=$?
}
# two_machines: both machines start stopped (the dispatcher starts, owns and stops them)
two_machines() { echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"; }
HANG_TIERS=$'go|mA|gofmt\nxplat|mB|cli-verb-guard'
# events_count <tier> <gate> <text>: how many watchdog events equal <text>
events_count() { grep -cxF -- "$3" "$FAKE_DIR/logs/watchdog-$1-$2.events" 2>/dev/null || true; }
# console_alive <case>: the fake console's pid is still running
console_alive() { local p; p=$(cat "$FAKE_DIR/console-pid" 2>/dev/null); [ -n "$p" ] && kill -0 "$p" 2>/dev/null; }

# 8. half-open session: console open, no verdict, remote process GONE →
#    explicit STALLED/DIED, exit 1, within the watchdog window, machine stopped
#    (its evidence was collected + verified) — and NOT a hang. The fake console
#    never ends by itself, a co-tenant runs the SAME gate on the SAME ref under
#    another attempt id, and the events prove the dispatcher itself probed
#    (twice, 'gone') and terminated the console.
new_case c8; two_machines
touch "$FAKE_DIR/hang-mA" "$FAKE_DIR/foreign-live-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(8) expected STALLED exit 1 within the cap, got $RC"
grep -q 'STALLED/DIED' "$OUT" || fail "(8) no STALLED/DIED verdict"
grep -q 'FAIL:stalled' "$OUT" || fail "(8) summary did not classify the verdict as FAIL:stalled"
grep -q 'gate gofmt on machine mA' "$OUT" || fail "(8) the verdict does not name the gate and machine"
grep -q 'tier go job exited 1' "$OUT" || fail "(8) the stalled tier did not report a non-zero job exit"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(8) the stalled machine's evidence was not collected and it was not stopped"
[ -s "$FAKE_DIR/logs/worker/go-gofmt.log" ] || fail "(8) no worker copy of the stalled gate's run log"
[ "$(events_count go gofmt 'probe gone')" -ge 2 ] || fail "(8) the watchdog did not record two 'probe gone' events (a co-tenant's same-gate process must not look alive)"
[ "$(events_count go gofmt 'probe alive')" -eq 0 ] || fail "(8) a probe reported 'alive' although only a co-tenant's attempt was running"
[ "$(events_count go gofmt 'terminate console')" -eq 1 ] || fail "(8) the watchdog did not record terminating the console"
[ "$(events_count go gofmt 'console reaped')" -eq 1 ] || fail "(8) the watchdog did not record reaping the console"
grep -q 'console terminated by TERM' "$FAKE_DIR/console-events" 2>/dev/null || fail "(8) the dead console was not terminated by the dispatcher (it cannot end by itself)"
console_alive && fail "(8) the hung console is still running after the dispatcher finished"
[ "$FAIL" -eq 0 ] && ok "(8) half-open hang: probed, terminated by the dispatcher, ends in STALLED/DIED (exit 1), evidence collected, machine stopped"

# 9. transport-only drop: console dropped but the remote RUNLOG THIS ATTEMPT
#    announced holds its verdict (same attempt id) → RECOVERED, PASS, exit 0
new_case c9; two_machines
touch "$FAKE_DIR/hang-mA"
printf 'RUNLOG: /cache/logs/gofmt@x.log\nATTEMPT: @ATTEMPT@\nHEAD: %s selfcheck\nALL GATES GREEN\n' "$FAKE_SHORT" > "$FAKE_DIR/runlog-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 0 ] || fail "(9) expected recovered verdict exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
grep -q 'verdict recovered from' "$OUT" || fail "(9) the recovery was not reported"
grep -q 'OVERALL: PASS' "$OUT" || fail "(9) a recovered complete run did not PASS"
[ "$(events_count go gofmt 'terminate console')" -eq 1 ] || fail "(9) the dispatcher did not terminate the dropped console before recovering"
[ "$FAIL" -eq 0 ] && ok "(9) transport-only drop recovers this attempt's verdict from its announced RUNLOG"

# 10. healthy slow gate: console open ~5s (several probes), a REAL stand-in
#     process carrying this attempt's argv ALIVE → survives the watchdog and PASSes
#     (liveness, not a timer); the probes are recorded as 'alive'
new_case c10; two_machines
touch "$FAKE_DIR/slow-mA" "$FAKE_DIR/live-mA" "$FAKE_DIR/foreign-live-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 0 ] || fail "(10) expected the healthy slow gate to PASS, got $RC: $(grep -m1 'ERROR\|STALLED' "$OUT")"
grep -q 'OVERALL: PASS' "$OUT" || fail "(10) the healthy slow gate did not PASS"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(10) control: the slow machine was not stopped"
[ "$(events_count go gofmt 'probe alive')" -ge 2 ] || fail "(10) the watchdog did not probe the live gate (expected at least two 'probe alive' events)"
[ "$(events_count go gofmt 'terminate console')" -eq 0 ] || fail "(10) a HEALTHY live gate's console was terminated"
[ "$FAIL" -eq 0 ] && ok "(10) healthy slow gate is probed alive and survives the liveness watchdog"

# 11. the REAL runci.sh signal traps, executed in an isolated fixture: the three
#     trap statements are taken from runci.sh itself (never re-typed here), run in
#     a child shell that is then sent HUP, INT and TERM. Each must write the
#     KILLED trailer and exit 129/130/143. A runner without its traps fails here.
TRAPS="$TMP/runci-traps.sh"
grep -E "^trap '.*' (HUP|INT|TERM)$" "$RUNCI" > "$TRAPS"
cat >"$TMP/sigdrive.py" <<'PY'
import signal, subprocess, sys
traps, name = sys.argv[1], sys.argv[2]
def reset():
    for s in (signal.SIGINT, signal.SIGHUP, signal.SIGTERM):
        signal.signal(s, signal.SIG_DFL)
p = subprocess.Popen(["bash", "-c", '. "$1"; echo ready; while :; do sleep 0.1; done', "fixture", traps],
                     stdout=subprocess.PIPE, stderr=subprocess.STDOUT, preexec_fn=reset, text=True)
if p.stdout.readline().strip() != "ready":
    print("fixture did not start"); sys.exit(3)
p.send_signal(getattr(signal, "SIG" + name))
try:
    out = p.stdout.read(); rc = p.wait(timeout=10)
except subprocess.TimeoutExpired:
    p.kill(); print("fixture did not exit after SIG" + name); sys.exit(4)
print(out.strip()); print("EXIT=%d" % rc)
PY
new_case c11
real_signal() { # $1 name $2 expected exit code
  local o
  o=$(python3 "$TMP/sigdrive.py" "$TRAPS" "$1" 2>&1)
  printf '%s' "$o" | grep -qx "RUNCI: KILLED by signal $1 — no verdict written; gate state at death is unknown" \
    || fail "(11) the real runci.sh wrote no KILLED trailer for SIG$1 (traps removed or changed?): $(printf '%s' "$o" | head -2 | tr '\n' ' ')"
  printf '%s' "$o" | grep -qx "EXIT=$2" || fail "(11) the real runci.sh did not exit $2 on SIG$1"
  LAST_TRAILER=$(printf '%s' "$o" | sed -n '/^RUNCI: KILLED/p' | head -1)
}
[ "$(wc -l < "$TRAPS")" -eq 3 ] || fail "(11) runci.sh does not carry exactly the three HUP/INT/TERM trap statements"
real_signal HUP 129; real_signal INT 130; real_signal TERM 143; TRAILER="$LAST_TRAILER"
[ "$FAIL" -eq 0 ] && ok "(11) the real runci.sh traps write the KILLED trailer and exit 129/130/143 on HUP/INT/TERM"

# 12. the trailer the REAL traps wrote, delivered by a console that then ends
#     without a verdict → FAIL:killed-by-signal, and no STALLED/DIED stamp over it
new_case c12; two_machines
touch "$FAKE_DIR/killed-mA"; printf '%s\n' "$TRAILER" > "$FAKE_DIR/trailer"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(12) expected FAIL exit 1, got $RC"
grep -q 'FAIL:killed-by-signal' "$OUT" || fail "(12) summary did not classify the verdict as FAIL:killed-by-signal"
grep -q 'KILLED by a signal' "$OUT" || fail "(12) the dispatcher did not say the gate was killed by a signal"
grep -q 'CI-CLUSTER: STALLED/DIED' "$FAKE_DIR/logs/go-gofmt.log" && fail "(12) a STALLED/DIED stamp was written over the KILLED trailer"
[ "$FAIL" -eq 0 ] && ok "(12) signal death classified as FAIL:killed-by-signal, trailer preserved"

# 13. F1: an OLD green run of the same gate and commit sits on the machine and
#     the current attempt announced nothing → never adopted: STALLED/DIED, exit 1,
#     no PASS, no recovery transfer attempted
new_case c13; two_machines
touch "$FAKE_DIR/hang-mA" "$FAKE_DIR/noannounce-mA"
printf 'RUNLOG: /cache/logs/gofmt@x.log\nATTEMPT: oldrun.go.gofmt\nHEAD: %s selfcheck\nALL GATES GREEN\n' "$FAKE_SHORT" > "$FAKE_DIR/runlog-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(13) a stale same-HEAD PASS must not be adopted: expected exit 1, got $RC"
grep -q 'STALLED/DIED' "$OUT" || fail "(13) no explicit STALLED/DIED verdict without current-attempt evidence"
grep -q 'OVERALL: PASS' "$OUT" && fail "(13) an old PASS certified a dead current attempt"
grep -q 'no RUNLOG announced by this attempt' "$OUT" || fail "(13) the failure does not say that no current-attempt log was announced"
grep -q 'ssh sftp get.*--machine mA' "$FAKE_DIR/calls.log" && fail "(13) the dispatcher fetched a log although this attempt announced none"
[ "$FAIL" -eq 0 ] && ok "(13) an old same-HEAD PASS is never adopted for an unannounced current attempt"

# 14. F1: the current attempt announced its RUNLOG but the file there belongs to
#     ANOTHER attempt (older green run, same gate and HEAD) → not adopted
new_case c14; two_machines
touch "$FAKE_DIR/hang-mA"
printf 'RUNLOG: /cache/logs/gofmt@x.log\nATTEMPT: oldrun.go.gofmt\nHEAD: %s selfcheck\nALL GATES GREEN\n' "$FAKE_SHORT" > "$FAKE_DIR/runlog-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(14) a foreign-attempt log must not be adopted: expected exit 1, got $RC"
grep -q 'STALLED/DIED' "$OUT" || fail "(14) no explicit STALLED/DIED verdict"
grep -q "does not carry this attempt's id" "$OUT" || fail "(14) the failure does not say the log belongs to another attempt"
grep -q 'OVERALL: PASS' "$OUT" && fail "(14) a foreign attempt's PASS certified the current attempt"
[ "$FAIL" -eq 0 ] && ok "(14) a log carrying another attempt's id is never adopted"

# 15. F2: a probe that FAILS (pgrep usage error / transport) is 'unreachable',
#     never mistaken for a clean no-match ('gone') nor for 'alive'
new_case c15; two_machines
touch "$FAKE_DIR/hang-mA" "$FAKE_DIR/pgreperr-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(15) expected STALLED exit 1, got $RC"
[ "$(events_count go gofmt 'probe unreachable')" -ge 2 ] || fail "(15) a failing pgrep probe was not recorded as 'unreachable'"
[ "$(events_count go gofmt 'probe gone')" -eq 0 ] || fail "(15) a failing probe was mistaken for a clean no-match"
grep -q 'watchdog:unreachable' "$OUT" || fail "(15) the verdict does not name the unreachable probe"
[ "$FAIL" -eq 0 ] && ok "(15) a failed probe is told apart from a clean no-match"

# 16. F3: the liveness probe itself hangs → bounded, counted as a strike, and the
#     tier ends in a named non-zero STALLED/DIED (watchdog:timeout) — no hang
new_case c16; two_machines
touch "$FAKE_DIR/hang-mA" "$FAKE_DIR/ptimeout-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(16) a hung control probe must end in exit 1 within the cap, got $RC"
[ "$(events_count go gofmt 'probe timeout')" -ge 2 ] || fail "(16) the hung probe was not bounded and recorded as 'probe timeout'"
grep -q 'watchdog:timeout' "$OUT" || fail "(16) the verdict does not name the control-probe timeout"
grep -q 'STALLED/DIED' "$OUT" || fail "(16) no STALLED/DIED verdict after a hung control probe"
[ "$FAIL" -eq 0 ] && ok "(16) a hung liveness probe is bounded and ends in a named STALLED/DIED"

# 17. F3: the console ignores TERM (non-terminating) and the recovery transfer
#     hangs → KILL after the grace, transfer bounded; named STALLED/DIED, no hang
new_case c17; two_machines
touch "$FAKE_DIR/hang-mA" "$FAKE_DIR/ignoreterm-mA" "$FAKE_DIR/sftphang-mA"
run_cluster_bounded "$HANG_TIERS"
[ "$RC" -eq 1 ] || fail "(17) a TERM-ignoring console and a hung transfer must end in exit 1 within the cap, got $RC"
grep -q 'console terminated by TERM' "$FAKE_DIR/console-events" 2>/dev/null && fail "(17) control: the console should have ignored TERM"
console_alive && fail "(17) the TERM-ignoring console was not killed"
[ "$(events_count go gofmt 'console reaped')" -eq 1 ] || fail "(17) the killed console was not reaped"
grep -q 'timed out after 2s' "$OUT" || fail "(17) the hung recovery transfer was not bounded and named"
grep -q 'STALLED/DIED' "$OUT" || fail "(17) no STALLED/DIED verdict"
[ "$FAIL" -eq 0 ] && ok "(17) a TERM-ignoring console is killed and a hung recovery transfer is bounded"

if [ "$FAIL" -ne 0 ]; then echo "selfcheck: ci-cluster.sh does not behave - see above" >&2; exit 1; fi
echo "selfcheck: OK (never stops a machine it did not start; refuses unreadable states; a dead session ends in STALLED/DIED or a RECOVERED verdict — never a silent hang; healthy slow gates survive; signal deaths keep their trailer)"
exit 0
