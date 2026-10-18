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
#   7. a gate whose console stays OPEN with no verdict while the remote gate process is
#      GONE (the 2026-10-18 half-open hang, lint@d52b70bdc) ends in the explicit
#      STALLED/DIED verdict with exit 1 within the watchdog window — never a hang
#   8. a gate whose console dropped but whose REMOTE RUNLOG holds a verdict is
#      recovered and PASSes (a transport-only drop is not a gate failure)
#   9. a HEALTHY slow gate (console open ~3s, remote process alive) survives the
#      watchdog and PASSes — liveness, not an idle timer, is what is checked
#  10. a gate whose runci.sh wrote the KILLED-by-signal trailer gets the
#      FAIL:killed-by-signal verdict, not a STALLED stamp over the trailer
#
# Usage: bash deploy/ci-worker/ci-cluster-selfcheck.sh   (exit 0 = all cases behave)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER="${CI_CLUSTER_UNDER_TEST:-$HERE/ci-cluster.sh}"   # override only to prove the checks bite an older script
RUNCI="$HERE/runci.sh"
[ -f "$CLUSTER" ] || { echo "selfcheck: missing $CLUSTER" >&2; exit 2; }

TMP="$(mktemp -d "${TMPDIR:-/tmp}/ci-cluster-selfcheck.XXXXXX")" || exit 2
trap 'rm -rf "$TMP"' EXIT
if command -v md5sum >/dev/null 2>&1; then MD5="$(md5sum "$RUNCI" | awk '{print $1}')"; else MD5="$(md5 -q "$RUNCI")"; fi
FULL="$(git -C "$HERE/../.." rev-parse HEAD)"
SHORT="$(git -C "$HERE/../.." rev-parse --short "$FULL")"

mkdir -p "$TMP/bin"
# Test-only `hostname`: a case can drop a hostname file into its state dir and
# the dispatcher's `hostname -s` (baked into OWNER_TOKEN) then reports THAT
# name. 6c uses it to model the Fly worker, where the hostname IS the machine
# id — the condition that made the old bare-string 6c matcher false-red there.
cat >"$TMP/bin/hostname" <<'HOST'
#!/usr/bin/env bash
if [ -n "${FAKE_DIR:-}" ] && [ -f "$FAKE_DIR/hostname" ]; then cat "$FAKE_DIR/hostname"; else /usr/bin/hostname "$@"; fi
HOST
chmod +x "$TMP/bin/hostname"
# The fake flyctl is written with a quoted heredoc and the two build-time values
# are passed through the environment, so nothing in it needs escaping.
cat >"$TMP/bin/fly" <<'FAKE'
#!/usr/bin/env bash
# fake flyctl: state lives in files under $FAKE_DIR
#   state-<id> machine state; owner-<id> owner marker; busy-<id> a live gate process;
#   hostname  the machine name OWNER_TOKEN's `hostname -s` reports (6c: the Fly
#             worker's hostname IS the machine id);
#   die-<id> the console dies mid-gate (2026-10-11 machine-stop case);
#   hang-<id> the console stays OPEN with no verdict (2026-10-18 half-open session case);
#   slow-<id> the console stays open ~3s and then completes (healthy long gate);
#   unreach-<id> pgrep probes fail (machine unreachable mid-run);
#   runlog-<id> contents the sftp get delivers (default: a verdict-bearing RUNLOG)
D="$FAKE_DIR"
echo "$*" >> "$D/calls.log"
arg() { local want="$1"; shift; while [ $# -gt 0 ]; do [ "$1" = "$want" ] && { echo "$2"; return; }; shift; done; }
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
      pgrep*)
        # unreachable model: flyctl prints an error to STDERR and exits 1
        if [ -f "$D/unreach-$id" ]; then echo "Error: ssh shell: connection refused" >&2; exit 1; fi
        # a real pgrep prints the matched pids on stdout — exit 0 with EMPTY
        # output means 'no match', which is how ci-cluster.sh reads it.
        # busy- = OUR gate's live process (the gate console clears it when the
        # gate completes); keepbusy- = a process that outlives our gate (a
        # foreign run), so `busy at exit` cases can model it.
        if [ -f "$D/busy-$id" ] || [ -f "$D/keepbusy-$id" ]; then echo 4242; exit 0; fi
        exit 1 ;;
      /cache/runci.sh*)
        gate=${cmd##* }
        echo "RUNLOG: /cache/logs/$gate@x.log"
        echo "HEAD: $FAKE_SHORT selfcheck"
        # case 4: another dispatcher takes the machine over while this gate runs - done
        # here, at a fixed point after our marker was written, not by a racing
        # background poller (which lost the race under load once this check ran in CI)
        if [ -f "$D/foreign-$id" ]; then echo "ci-cluster:99999:other:1" > "$D/owner-$id"; fi
        if [ -f "$D/die-$id" ]; then echo stopped > "$D/state-$id"; echo "Error: ssh shell: wait: remote command exited without exit status or exit signal"; exit 1; fi
        # half-open-session model: the console never returns and never prints a verdict;
        # the dispatcher's watchdog must end the tier (killed from outside on detection)
        if [ -f "$D/hang-$id" ]; then sleep 45; exit 0; fi
        # healthy-long-gate model: no output growth for ~3s (longer than the test
        # watchdog's strike window), then a normal completion — a pure idle-timer
        # watchdog would kill this; a liveness one must not
        if [ -f "$D/slow-$id" ]; then sleep 3; rm -f "$D/busy-$id"; echo "ALL GATES GREEN"; exit 0; fi
        # runci.sh's signal-death trailer, delivered then the session ends without a verdict
        if [ -f "$D/killed-$id" ]; then echo "RUNCI: KILLED by signal HUP — no verdict written; gate state at death is unknown"; exit 1; fi
        # our gate's process is gone once the gate completes (case 9's stop
        # assert depends on this matching reality)
        rm -f "$D/busy-$id"
        echo "ALL GATES GREEN"; exit 0 ;;
      ls*) echo "/cache/logs/x.log"; exit 0 ;;
      # the dispatcher's run-log fallback runs `sh -c 'ls -1t …'` (the console
      # has no shell, so pipes and globs must be wrapped) — model it the same
      sh\ -c*) echo "/cache/logs/x.log"; exit 0 ;;
    esac
    exit 0 ;;
  "ssh sftp")
    id=$(arg --machine "$@")
    st=$(cat "$D/state-$id" 2>/dev/null)
    # a stopped machine serves no files — the real sftp get fails to connect
    if [ "$st" != "started" ]; then echo "Error: machine $id is not started" >&2; exit 1; fi
    dest="$5"
    if [ -f "$D/runlog-$id" ]; then cat "$D/runlog-$id" > "$dest"; else printf 'HEAD: %s selfcheck\nALL GATES GREEN\n' "$FAKE_SHORT" > "$dest"; fi
    exit 0 ;;
esac
echo "fake fly: unhandled: $*" >&2; exit 1
FAKE
chmod +x "$TMP/bin/fly"
export FAKE_MD5="$MD5" FAKE_SHORT="$SHORT"

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
ran_gate() { grep -q -- "--machine $1 -C /cache/runci.sh $FULL $2\$" "$FAKE_DIR/calls.log"; } # machine gate

# True when calls.log shows the machine id in a position that means the
# dispatcher ADDRESSED that machine: `machine status|start|stop <id>` or
# `--machine <id>`. A bare `grep -q "$id"` is wrong here and WAS wrong here:
# on the Fly worker `hostname -s` prints the machine id, OWNER_TOKEN carries
# it, and the owner-marker write (`-C sh -c 'echo ci-cluster:<pid>:<hostname>:…'
# --machine mGo`) then contains the default go machine's id while touching
# only mGo — that false-red killed the lint gate's guard run on Fly
# (chief steer, 2026-10-19). Fix the instrument, not the expectation.
touches_machine() { # $1 machine-id $2 calls.log
  grep -Eq "(machine (status|start|stop) $1([[:space:]]|$))|(--machine $1([[:space:]]|$))" "$2"
}

# The shipped placement (founder ruling 2026-10-12, deploy/ci-worker/ci-cluster.sh::default_tier_map).
GO_ID=080d74db170408; NODE_ID=d8d0d90a1e9648; XPLAT_ID=78113d4f329908

# 6a. the default map: three tiers on their shipped machines with their shipped gates
new_case c6a; for m in $GO_ID $NODE_ID $XPLAT_ID; do echo stopped > "$FAKE_DIR/state-$m"; done
run_default
[ "$RC" -eq 0 ] || fail "(6a) default map: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
for g in gofmt go-build go-vet lint go-test go-race; do ran_gate $GO_ID $g || fail "(6a) go tier: gate $g did not run on $GO_ID"; done
for g in contracts spa; do ran_gate $NODE_ID $g || fail "(6a) node tier: gate $g did not run on $NODE_ID"; done
for g in embed-build records-no-sqlite cli-verb-guard; do ran_gate $XPLAT_ID $g || fail "(6a) xplat tier: gate $g did not run on $XPLAT_ID"; done
[ "$(grep -c 'machine start' "$FAKE_DIR/calls.log")" -eq 3 ] || fail "(6a) the default map did not start exactly its three machines"
[ "$FAIL" -eq 0 ] && ok "(6a) default tier map: shipped ids and gates"

# 6b. an explicitly EMPTY id skips its tier; the other two still run on their defaults
new_case c6b; for m in $GO_ID $XPLAT_ID; do echo stopped > "$FAKE_DIR/state-$m"; done
run_default CI_CLUSTER_NODE_MACHINE=
[ "$RC" -eq 0 ] || fail "(6b) empty node id: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
touches_machine "$NODE_ID" "$FAKE_DIR/calls.log" && fail "(6b) the node machine was touched although its id was explicitly empty"
grep -q -- 'contracts' "$FAKE_DIR/calls.log" && fail "(6b) the node tier ran although its id was explicitly empty"
ran_gate $GO_ID go-race || fail "(6b) control: the go tier did not run on its default"
ran_gate $XPLAT_ID cli-verb-guard || fail "(6b) control: the xplat tier did not run on its default"
[ "$FAIL" -eq 0 ] && ok "(6b) explicitly empty id skips only its tier"

# 6c. a per-tier id override replaces only that tier's default. Runs under a
#     HOSTILE hostname (the Fly worker's: `hostname -s` prints the machine id),
#     which is exactly the condition that made the old bare-string matcher
#     false-red on the go machine — the owner-marker write legitimately carries
#     that string while touching only mGo.
new_case c6c; for m in mGo $NODE_ID $XPLAT_ID; do echo stopped > "$FAKE_DIR/state-$m"; done
printf '%s\n' "$GO_ID" > "$FAKE_DIR/hostname"
run_default CI_CLUSTER_GO_MACHINE=mGo
[ "$RC" -eq 0 ] || fail "(6c) go override: expected exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
ran_gate mGo go-race || fail "(6c) the go tier did not run on the overridden machine"
touches_machine "$GO_ID" "$FAKE_DIR/calls.log" && fail "(6c) the go tier's default machine was touched despite an override"
ran_gate $NODE_ID spa || fail "(6c) control: the node tier did not run on its default"
[ "$FAIL" -eq 0 ] && ok "(6c) per-tier override (under hostname == default machine id)"

# ---- cases 7-10: the 2026-10-18 half-open-session hang and its verdicts ----
# Like run_cluster, but at the watchdog's fastest cadence AND with a hard 60s
# cap: cases 7 and 8 have a console that would hang forever, so if the
# watchdog is broken these cases must FAIL by cap (RC=143), not hang the
# selfcheck. RC=1/0 within the cap IS the no-hang proof.
run_cluster_bounded() { # $1 tiers-spec
  OUT="$TMP/out.txt"
  PATH="$TMP/bin:$PATH" CI_CLUSTER_APP=selfcheck CI_CLUSTER_START_TIMEOUT=20 CI_CLUSTER_LOG_DIR="$FAKE_DIR/logs" \
    CI_CLUSTER_TIERS="$1" CI_CLUSTER_WATCH_INTERVAL=1 CI_CLUSTER_DEAD_STRIKES=2 GIT_TERMINAL_PROMPT=0 \
    bash "$CLUSTER" "$FULL" >"$OUT" 2>&1 &
  local p=$! i=0
  while kill -0 "$p" 2>/dev/null && [ "$i" -lt 60 ]; do sleep 1; i=$((i + 1)); done
  if kill -0 "$p" 2>/dev/null; then
    kill -9 "$p" 2>/dev/null; wait "$p" 2>/dev/null; RC=143
    echo "selfcheck: dispatcher was KILLED at the 60s cap — it HUNG (watchdog broken?)" >> "$OUT"
    return
  fi
  wait "$p"; RC=$?
}

# 7. half-open session: console open, no verdict, remote process GONE →
#    explicit STALLED/DIED, exit 1, within the watchdog window, machine stopped
#    (its evidence was collected + verified) — and NOT a hang
new_case c7; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
touch "$FAKE_DIR/hang-mA"
printf 'HEAD: %s selfcheck\ncut off mid-line inside the size-budget output, no verdict\n' "$FAKE_SHORT" > "$FAKE_DIR/runlog-mA"
run_cluster_bounded $'go|mA|gofmt\nxplat|mB|cli-verb-guard'
[ "$RC" -eq 1 ] || fail "(7) expected STALLED exit 1 within the cap, got $RC"
grep -q 'STALLED/DIED' "$OUT" || fail "(7) no STALLED/DIED verdict"
grep -q 'FAIL:stalled' "$OUT" || fail "(7) summary did not classify the verdict as FAIL:stalled"
grep -q 'gate gofmt on machine mA' "$OUT" || fail "(7) the verdict does not name the gate and machine"
grep -q 'tier go job exited 1' "$OUT" || fail "(7) the stalled tier did not report a non-zero job exit"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(7) the stalled machine's evidence was not collected and it was not stopped"
[ -s "$FAKE_DIR/logs/worker/go-gofmt.log" ] || fail "(7) no worker copy of the stalled gate's run log"
[ "$FAIL" -eq 0 ] && ok "(7) half-open hang ends in STALLED/DIED (exit 1), evidence collected, machine stopped"

# 8. transport-only drop: console dropped but the remote RUNLOG holds the
#    verdict → RECOVERED, the gate PASSes, exit 0
new_case c8; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
touch "$FAKE_DIR/hang-mA"
printf 'HEAD: %s selfcheck\nALL GATES GREEN\n' "$FAKE_SHORT" > "$FAKE_DIR/runlog-mA"
run_cluster_bounded $'go|mA|gofmt\nxplat|mB|cli-verb-guard'
[ "$RC" -eq 0 ] || fail "(8) expected recovered verdict exit 0, got $RC: $(grep -m1 'ERROR' "$OUT")"
grep -q 'verdict recovered from' "$OUT" || fail "(8) the recovery was not reported"
grep -q 'OVERALL: PASS' "$OUT" || fail "(8) a recovered complete run did not PASS"
[ "$FAIL" -eq 0 ] && ok "(8) transport-only drop recovers the verdict from the remote RUNLOG"

# 9. healthy slow gate: console open ~3s (longer than 2 probes), remote process
#    ALIVE → survives the watchdog and PASSes (liveness, not a timer)
new_case c9; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
touch "$FAKE_DIR/slow-mA" "$FAKE_DIR/busy-mA"
run_cluster_bounded $'go|mA|gofmt\nxplat|mB|cli-verb-guard'
[ "$RC" -eq 0 ] || fail "(9) expected the healthy slow gate to PASS, got $RC: $(grep -m1 'ERROR\|STALLED' "$OUT")"
grep -q 'OVERALL: PASS' "$OUT" || fail "(9) the healthy slow gate did not PASS"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(9) control: the slow machine was not stopped"
[ "$FAIL" -eq 0 ] && ok "(9) healthy slow gate survives the liveness watchdog"

# 10. runci.sh died on a signal and wrote its trailer → FAIL:killed-by-signal,
#     and the STALLED/DIED stamp is NOT written over the trailer
new_case c10; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
touch "$FAKE_DIR/killed-mA"
printf 'HEAD: %s selfcheck\nno verdict — the run died on a signal\n' "$FAKE_SHORT" > "$FAKE_DIR/runlog-mA"
run_cluster $'go|mA|gofmt\nxplat|mB|cli-verb-guard'
[ "$RC" -eq 1 ] || fail "(10) expected FAIL exit 1, got $RC"
grep -q 'FAIL:killed-by-signal' "$OUT" || fail "(10) summary did not classify the verdict as FAIL:killed-by-signal"
grep -q 'KILLED by a signal' "$OUT" || fail "(10) the dispatcher did not say the gate was killed by a signal"
grep -q 'CI-CLUSTER: STALLED/DIED' "$FAKE_DIR/logs/go-gofmt.log" && fail "(10) a STALLED/DIED stamp was written over the KILLED trailer"
[ "$FAIL" -eq 0 ] && ok "(10) signal death classified as FAIL:killed-by-signal, trailer preserved"

if [ "$FAIL" -ne 0 ]; then echo "selfcheck: ci-cluster.sh does not behave - see above" >&2; exit 1; fi
echo "selfcheck: OK (never stops a machine it did not start; refuses unreadable states; a dead session ends in STALLED/DIED or a RECOVERED verdict — never a silent hang; healthy slow gates survive; signal deaths keep their trailer)"
exit 0
