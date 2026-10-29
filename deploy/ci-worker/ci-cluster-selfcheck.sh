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
# The fake flyctl is written with a quoted heredoc and the two build-time values
# are passed through the environment, so nothing in it needs escaping.
cat >"$TMP/bin/fly" <<'FAKE'
#!/usr/bin/env bash
# fake flyctl: state lives in files under $FAKE_DIR (state-<id>, owner-<id>, busy-<id>, die-<id>)
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
      pgrep*) [ -f "$D/busy-$id" ] && exit 0; exit 1 ;;
      /cache/runci.sh*)
        gate=${cmd##* }
        echo "RUNLOG: /cache/logs/$gate@x.log"
        echo "HEAD: $FAKE_SHORT selfcheck"
        # case 4: another dispatcher takes the machine over while this gate runs - done
        # here, at a fixed point after our marker was written, not by a racing
        # background poller (which lost the race under load once this check ran in CI)
        if [ -f "$D/foreign-$id" ]; then echo "ci-cluster:99999:other:1" > "$D/owner-$id"; fi
        if [ -f "$D/die-$id" ]; then echo stopped > "$D/state-$id"; echo "Error: ssh shell: wait: remote command exited without exit status or exit signal"; exit 1; fi
        echo "ALL GATES GREEN"; exit 0 ;;
      ls*) echo "/cache/logs/x.log"; exit 0 ;;
    esac
    exit 0 ;;
  "ssh sftp")
    dest="$5"; printf 'HEAD: %s selfcheck\nALL GATES GREEN\n' "$FAKE_SHORT" > "$dest"; exit 0 ;;
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
[ "$FAIL" -eq 0 ] && ok "(3) a machine stopped mid-run fails its tier"

# 4. a machine we started now carries another dispatcher's marker -> not stopped
new_case c4; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"
touch "$FAKE_DIR/foreign-mB"   # the fake fly overwrites mB's marker once our gate is running
run_cluster "$TIERS"
grep -q 'STOP mB' "$FAKE_DIR/stops.log" && fail "(4) stopped a machine carrying another dispatcher's marker"
grep -q 'STOP mA' "$FAKE_DIR/stops.log" || fail "(4) control: the machine with our own marker (mA) was not stopped"
[ "$FAIL" -eq 0 ] && ok "(4) foreign owner marker respected"

# 5. busy at exit -> not stopped
new_case c5; echo stopped > "$FAKE_DIR/state-mA"; echo stopped > "$FAKE_DIR/state-mB"; touch "$FAKE_DIR/busy-mB"
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
GO_GATES=$(grep -- "--machine $GO_ID -C /cache/runci.sh $FULL " "$FAKE_DIR/calls.log" | sed 's/.* //')
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

if [ "$FAIL" -ne 0 ]; then echo "selfcheck: ci-cluster.sh does not behave - see above" >&2; exit 1; fi
echo "selfcheck: OK (ci-cluster.sh never stops a machine it did not start, refuses unreadable states, fails a tier whose machine dies)"
exit 0
