#!/usr/bin/env bash
# ci-boot.test.sh — sandboxed proof for ci-boot.sh (the CI worker's main process).
#
# Linux only (flock, setsid-free, /proc): run it on a CI worker or any Linux box.
# Every case runs the REAL ci-boot.sh (and, where named, the real idle-watchdog.sh)
# against a private /tmp sandbox — its own lock file, logs dir, halt marker and a
# unique run-process pattern — so it never touches /tmp/runci.lock, /cache or a
# real runci.sh. Each guarded behaviour is also run against a MUTANT of ci-boot.sh
# with that guard removed, and the case must go RED there: a test that cannot fail
# proves nothing (docs/internal/false-green-patterns.md).
#
# Cases (PR #1396 review, 2026-10-21):
#   I1  a watchdog that dies is logged ("WATCHDOG DIED ... status 3") and restarted;
#       the machine is NOT stopped because of it
#   I2a a holder of the run lock when the halt marker appears → halt ABORTED,
#       marker removed, machine stays up (the reviewer's schedule: the watchdog
#       released the lock and a queued gate took it before the main process exited)
#   I2b a run process with the lock free (queued / between lock steps) → ABORTED
#   OK  marker + free lock + no run process → exit 0 (the real stop path)
#   E2E the real idle-watchdog.sh with a 2 s threshold drives ci-boot.sh to exit 0
#       (ci-boot waits 15 s for the lock: the watchdog holds it 5 s after its hook)
#   SF1 the watchdog's state cannot be read (no /proc entry, failing ps) → the loop
#       must not block waiting on it: the halt marker is still handled
#   SF2 after an aborted halt, error output still reaches stderr (a later log-write
#       failure is reported, not swallowed by a lasting 2>/dev/null)
#   SF3 flock itself failing (not a timeout) is reported as a lock-check failure,
#       and is never treated as having the lock
#   T1  a run that appears WHILE ci-boot waits for the lock aborts the halt (the
#       run check happens after the lock is taken, not before)
#   T2  shutdown-check errors abort the halt: an unopenable lock file, and pgrep
#       failing (exit 2) — the machine stays up and the lock is released
#   CTL a broken mutant setup (no interpreter / a mutation anchor gone) must stop this
#       harness with a SETUP ERROR, exit 2 and no PASS rows — never count as a killed
#       mutant (PR #1396 review I3). Each control re-runs this file in a child.
#
# Usage: bash deploy/ci-worker/ci-boot.test.sh      exit 0 = all PASS, 1 = a FAIL,
#        2 = cannot run or SETUP ERROR (a mutant could not be built — nothing was proven)
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SELF="$HERE/$(basename "${BASH_SOURCE[0]}")"
# CI_BOOT_TEST_BOOT / CI_BOOT_TEST_PYTHON exist only for the CTL controls below.
BOOT="${CI_BOOT_TEST_BOOT:-$HERE/ci-boot.sh}"
PYTHON="${CI_BOOT_TEST_PYTHON:-python3}"
HALT_HOOK="$HERE/ci-machine-halt"
WATCHDOG_REAL="$HERE/idle-watchdog.sh"
for t in flock pgrep ps cmp "$PYTHON"; do
  command -v "$t" >/dev/null 2>&1 || { echo "cannot run: '$t' missing (Linux only)"; exit 2; }
done

SB="$(mktemp -d /tmp/ci-boot-test.XXXXXX)"
TAG="cibootfake$$"
PIDS=()
# kill_tree <pid>: the process and everything it started. ci-boot.sh's watchdog and the
# fake runs' sleeps outlive a plain kill of their parent — left behind, they keep a
# remote shell open and keep watching the (deleted) sandbox.
kill_tree() {
  local c
  for c in $(pgrep -P "$1" 2>/dev/null); do kill_tree "$c"; done
  kill -TERM "$1" 2>/dev/null
}
cleanup() {
  local p
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill_tree "$p"; done
  # A ci-boot that exited (the stop path) leaves its watchdog orphaned, out of reach of
  # kill_tree; every watchdog's pid is in its case log. Kill only if the pid still runs
  # one of ours (a fake watchdog's `sleep 300` or the real watchdog from $HERE): on a
  # shared CI machine a reused pid may belong to someone else.
  for p in $(cat "$SB"/*/logs/idle-watchdog.log 2>/dev/null \
      | sed -n 's/.*started under supervision (pid \([0-9]*\)).*/\1/p'); do
    case "$(ps -o args= -p "$p" 2>/dev/null)" in
      "sleep 300"|*"$WATCHDOG_REAL run"*) kill_tree "$p" ;;
    esac
  done
  sleep 0.3
  rm -rf "$SB"
}
trap cleanup EXIT

PASS=0
FAIL=0
pass() { echo "  PASS [$1]"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL [$1]: $2"; FAIL=$((FAIL + 1)); [ -f "$3" ] && sed 's/^/    | /' "$3" | tail -15; }
# setup_error: a mutant (or other fixture) could not be built. Stop at once with exit 2:
# a missing mutant makes its case "red" for the wrong reason, which would read as killed.
setup_error() { echo "SETUP ERROR: $*"; exit 2; }

# --- sandbox helpers -----------------------------------------------------------------------

# new_case <name>: fresh dirs + env for one case; sets CASE_DIR
new_case() {
  CASE_DIR="$SB/$1"
  mkdir -p "$CASE_DIR/logs" "$CASE_DIR/state"
  export IDLE_WATCHDOG_LOCKFILE="$CASE_DIR/runci.lock"
  export IDLE_WATCHDOG_LOGS_DIR="$CASE_DIR/logs"
  export IDLE_WATCHDOG_STATE_DIR="$CASE_DIR/state"
  export IDLE_WATCHDOG_BOOT_MARKER="$CASE_DIR/boot.gen"
  export IDLE_WATCHDOG_PIDFILE="$CASE_DIR/wd.pid"
  export IDLE_WATCHDOG_RUNCI_PATTERN="$TAG-$1-runci[.]sh"
  export IDLE_WATCHDOG_POWEROFF_CMD="$HALT_HOOK"
  export CI_BOOT_HALT_MARKER="$CASE_DIR/halt"
  export CI_BOOT_LOG="$CASE_DIR/logs/idle-watchdog.log"
  export CI_BOOT_POLL_SECONDS=0.2
  export CI_BOOT_RESTART_SECONDS=0.5
  export CI_BOOT_LOCK_WAIT_SECONDS=2
}

# a fake watchdog that just stays alive (the halt cases trigger the marker by hand)
cat >"$SB/wd-idle.sh" <<'EOF'
#!/usr/bin/env bash
exec sleep 300
EOF
# a fake watchdog that dies with status 3 on its first life, then stays alive
cat >"$SB/wd-dies-once.sh" <<'EOF'
#!/usr/bin/env bash
n=$(cat "$WD_COUNTER" 2>/dev/null || echo 0); n=$((n + 1)); echo "$n" >"$WD_COUNTER"
[ "$n" -eq 1 ] && exit 3
exec sleep 300
EOF
chmod +x "$SB/wd-idle.sh" "$SB/wd-dies-once.sh"

# fake_run <case> <hold-lock:yes|no>. yes: a process holding the run lock like runci.sh's
# whole-run mutex, deliberately NOT matching the run pattern, so the lock guard alone must
# catch it (otherwise the process re-check would mask a missing lock step). no: a process
# matching the run pattern with the lock free (a queued run). Sets RUN_PID.
fake_run() {
  local script="$CASE_DIR/$TAG-$1-runci.sh"
  if [ "$2" = yes ]; then
    script="$CASE_DIR/$TAG-$1-lockholder.sh"
    printf '#!/usr/bin/env bash\nexec 9>>"%s"\nflock 9\nexec sleep 300\n' "$IDLE_WATCHDOG_LOCKFILE" >"$script"
  else
    printf '#!/usr/bin/env bash\nsleep 300\n' >"$script"
  fi
  chmod +x "$script"
  "$script" &
  RUN_PID=$!
  PIDS+=("$RUN_PID")
  sleep 0.5
}

# run_boot <boot-file> <watchdog> [VAR=value...] → starts ci-boot in the background with
# extra environment for that process only; sets BOOT_PID
run_boot() {
  local file="$1" wd="$2"; shift 2
  env "$@" CI_BOOT_WATCHDOG="$wd" bash "$file" >"$CASE_DIR/boot.out" 2>&1 &
  BOOT_PID=$!
  PIDS+=("$BOOT_PID")
}

# wait_exit <pid> <seconds> → 0 + EXIT_RC if it exited in time
wait_exit() {
  local _
  for _ in $(seq 1 $(( $2 * 10 ))); do
    if ! kill -0 "$1" 2>/dev/null; then wait "$1"; EXIT_RC=$?; return 0; fi
    sleep 0.1
  done
  return 1
}

# wait_for <seconds> <command...> → 0 as soon as the command succeeds, 1 on timeout
wait_for() {
  local _ n=$(( $1 * 10 )); shift
  for _ in $(seq 1 "$n"); do "$@" && return 0; sleep 0.1; done
  return 1
}
counter_is() { [ "$(cat "$WD_COUNTER" 2>/dev/null)" = "$1" ]; }
log_has() { grep -q "$1" "$CI_BOOT_LOG" 2>/dev/null; }
flock_child_of() { pgrep -P "$1" -x flock >/dev/null; }

# --- mutants ---------------------------------------------------------------------------------
mutate() { # <name> <old> <new> [<old> <new>...] — any failure is a SETUP ERROR (exit 2)
  local dst="$SB/mut-$1.sh" name="$1"; shift
  "$PYTHON" - "$BOOT" "$dst" "$@" <<'EOF' || setup_error "mutant $name: could not be generated (interpreter failed or mutation anchor missing in $BOOT)"
import sys
src, dst, pairs = sys.argv[1], sys.argv[2], sys.argv[3:]
if not pairs or len(pairs) % 2:
    sys.exit("mutate needs <old> <new> pairs")
s = open(src).read()
for old, new in zip(pairs[0::2], pairs[1::2]):
    if s.count(old) != 1:
        sys.exit("mutation anchor found %d times, want exactly 1: %r" % (s.count(old), old))
    s = s.replace(old, new, 1)
open(dst, "w").write(s)
EOF
  [ -s "$dst" ] || setup_error "mutant $name: $dst missing or empty"
  if cmp -s "$BOOT" "$dst"; then setup_error "mutant $name: identical to $BOOT"; fi
  bash -n "$dst" || setup_error "mutant $name: not valid bash"
}
mutate no-restart '      start_watchdog ;;' '      exit 0 ;;'
mutate no-lock '  flock -w "$LOCK_WAIT_SECONDS" 7
  rc=$?' '  rc=0'
mutate unknown-waits '    unknown)
' '    unknown)
      wait "$WD_PID"
'
mutate stderr-muted 'if ! exec 7>>"$LOCKFILE"; then' 'if ! exec 7>>"$LOCKFILE" 2>/dev/null; then'
mutate flock-error-as-held '"lock check FAILED on $LOCKFILE (flock exit $rc; check flock and the lock path)"' \
  '"$LOCKFILE still held after ${LOCK_WAIT_SECONDS}s (a CI run owns the machine)"'
mutate flock-error-as-locked '    *) abort_halt "lock check FAILED' '    *) : "lock check FAILED'
mutate stale-run-check '  flock -w "$LOCK_WAIT_SECONDS" 7
  rc=$?' '  pgrep -f "$RUNCI_PATTERN" >/dev/null; early=$?
  flock -w "$LOCK_WAIT_SECONDS" 7
  rc=$?' '  pgrep -f "$RUNCI_PATTERN" >/dev/null
  rc=$?
  case "$rc" in
    1) ;; # no run' '  rc=$early
  case "$rc" in
    1) ;; # no run'
mutate lockopen-exits 'if ! exec 7>>"$LOCKFILE"; then' 'if ! exec 7>>"$LOCKFILE"; then exit 0; fi; if false; then'
mutate pgrep-error-as-idle '    *) abort_halt "cannot check for CI run processes (pgrep exit $rc)"; return 1 ;;' '    *) ;;'
mutate no-recheck '    0) abort_halt "a CI run process is present (running or queued)"; return 1 ;;' '    0) ;;'

# --- cases -------------------------------------------------------------------------------------

case_i1() { # <boot-file> → 0 if behaviour correct
  new_case "i1-$(basename "$1")"
  export WD_COUNTER="$CASE_DIR/counter"
  run_boot "$1" "$SB/wd-dies-once.sh"
  wait_for 10 counter_is 2 || { kill_tree "$BOOT_PID"; return 1; }
  kill -0 "$BOOT_PID" 2>/dev/null || return 1
  grep -q 'WATCHDOG DIED: .* exited with status 3' "$CI_BOOT_LOG" || return 1
  kill_tree "$BOOT_PID"
  return 0
}

case_i2a() { # run holds the lock when the marker appears
  new_case "i2a-$(basename "$1")"
  fake_run "i2a-$(basename "$1")" yes
  run_boot "$1" "$SB/wd-idle.sh"
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  if wait_exit "$BOOT_PID" 5; then kill_tree "$RUN_PID"; return 1; fi
  kill_tree "$RUN_PID"; kill_tree "$BOOT_PID"
  [ ! -e "$CI_BOOT_HALT_MARKER" ] || return 1
  grep -q 'halt ABORTED: .*still held' "$CI_BOOT_LOG" || return 1
  return 0
}

case_i2b() { # run process present, lock free
  new_case "i2b-$(basename "$1")"
  fake_run "i2b-$(basename "$1")" no
  run_boot "$1" "$SB/wd-idle.sh"
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  if wait_exit "$BOOT_PID" 3; then kill_tree "$RUN_PID"; return 1; fi
  flock -n "$IDLE_WATCHDOG_LOCKFILE" true || { kill_tree "$RUN_PID"; kill_tree "$BOOT_PID"; return 1; }
  kill_tree "$RUN_PID"; kill_tree "$BOOT_PID"
  grep -q 'halt ABORTED: a CI run process is present' "$CI_BOOT_LOG" || return 1
  return 0
}

case_ok() { # marker, lock free, no run → exit 0
  new_case "ok-$(basename "$1")"
  run_boot "$1" "$SB/wd-idle.sh"
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  wait_exit "$BOOT_PID" 4 || { kill_tree "$BOOT_PID"; return 1; }
  [ "$EXIT_RC" -eq 0 ] || return 1
  grep -q 'halt: run lock held, no CI run process' "$CI_BOOT_LOG" || return 1
  return 0
}

case_e2e() { # the real idle-watchdog drives the stop
  new_case "e2e"
  # The real watchdog keeps the run lock for 5 s after its hook returns (attempt_poweroff),
  # so ci-boot must wait longer than that, as in production (60 s); new_case's 2 s is for
  # the fake-watchdog cases, where nothing but the fake run holds the lock.
  export IDLE_WATCHDOG_IDLE_SECONDS=2 IDLE_WATCHDOG_POLL_SECONDS=1 CI_BOOT_LOCK_WAIT_SECONDS=15
  run_boot "$BOOT" "$WATCHDOG_REAL"
  wait_exit "$BOOT_PID" 30 || { kill_tree "$BOOT_PID"; unset IDLE_WATCHDOG_IDLE_SECONDS IDLE_WATCHDOG_POLL_SECONDS; return 1; }
  unset IDLE_WATCHDOG_IDLE_SECONDS IDLE_WATCHDOG_POLL_SECONDS
  [ "$EXIT_RC" -eq 0 ] || return 1
  grep -q 'POWERING OFF' "$CI_BOOT_LOG" && grep -q 'halt: run lock held' "$CI_BOOT_LOG" || return 1
  return 0
}

# stays_up_aborted <seconds> <log-pattern> → ci-boot still running after a halt request,
# marker gone, the abort reason logged, no successful-halt record. Stops ci-boot.
stays_up_aborted() {
  if wait_exit "$BOOT_PID" "$1"; then return 1; fi
  kill_tree "$BOOT_PID"
  [ ! -e "$CI_BOOT_HALT_MARKER" ] || return 1
  grep -q "$2" "$CI_BOOT_LOG" || return 1
  ! grep -q 'halt: run lock held' "$CI_BOOT_LOG"
}

case_sf1() { # watchdog state unreadable: no /proc entry and ps failing; marker must still work
  new_case "sf1-$(basename "$1")"
  mkdir -p "$CASE_DIR/noproc" "$CASE_DIR/bin"
  printf '#!/bin/sh\nexit 1\n' >"$CASE_DIR/bin/ps"; chmod +x "$CASE_DIR/bin/ps"
  run_boot "$1" "$SB/wd-idle.sh" CI_BOOT_PROC_DIR="$CASE_DIR/noproc" PATH="$CASE_DIR/bin:$PATH"
  sleep 1
  : >"$CI_BOOT_HALT_MARKER"
  wait_exit "$BOOT_PID" 6 || { kill_tree "$BOOT_PID"; return 1; }
  [ "$EXIT_RC" -eq 0 ] || return 1
  grep -q 'WATCHDOG STATE UNKNOWN' "$CI_BOOT_LOG" || return 1
  ! grep -q 'WATCHDOG DIED' "$CI_BOOT_LOG"
}

case_sf2() { # after one aborted halt, a log-write failure still reaches stderr
  new_case "sf2-$(basename "$1")"
  fake_run "sf2-$(basename "$1")" yes
  run_boot "$1" "$SB/wd-idle.sh" CI_BOOT_LOCK_WAIT_SECONDS=1
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  wait_for 5 log_has 'halt ABORTED' || { kill_tree "$RUN_PID"; kill_tree "$BOOT_PID"; return 1; }
  rm -f "$CI_BOOT_LOG"; mkdir "$CI_BOOT_LOG" # the log path is now unwritable
  : >"$CI_BOOT_HALT_MARKER"                 # second abort → say → write fails
  sleep 3
  kill_tree "$RUN_PID"; kill_tree "$BOOT_PID"
  grep -q "cannot write $CI_BOOT_LOG" "$CASE_DIR/boot.out"
}

case_sf3() { # flock fails (bad wait value, exit 64), no run: reported as a lock failure, stays up
  new_case "sf3-$(basename "$1")"
  run_boot "$1" "$SB/wd-idle.sh" CI_BOOT_LOCK_WAIT_SECONDS=notanumber
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  stays_up_aborted 3 'halt ABORTED: lock check FAILED on .*(flock exit [0-9]*;' || return 1
  ! grep -q 'still held after' "$CI_BOOT_LOG"
}

case_t1() { # a queued run appears while ci-boot waits for the lock
  local holder
  new_case "t1-$(basename "$1")"
  fake_run "t1-$(basename "$1")" yes; holder=$RUN_PID
  run_boot "$1" "$SB/wd-idle.sh" CI_BOOT_LOCK_WAIT_SECONDS=15
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  # readiness: ci-boot is inside its flock wait (no timing guess)
  wait_for 5 flock_child_of "$BOOT_PID" || { kill_tree "$holder"; kill_tree "$BOOT_PID"; return 1; }
  fake_run "t1-$(basename "$1")" no # matches the run pattern, holds no lock
  kill_tree "$holder"                 # lock released → ci-boot takes it, then checks
  stays_up_aborted 4 'halt ABORTED: a CI run process is present'; local rc=$?
  kill_tree "$RUN_PID"
  return $rc
}

case_t2_lockopen() { # the lock file cannot be opened (its directory is missing)
  new_case "t2o-$(basename "$1")"
  run_boot "$1" "$SB/wd-idle.sh" IDLE_WATCHDOG_LOCKFILE="$CASE_DIR/no-such-dir/runci.lock"
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  stays_up_aborted 3 'halt ABORTED: cannot open the run lock'
}

case_t2_pgrep() { # pgrep fails (invalid pattern, exit 2): abort, and the lock is released
  new_case "t2p-$(basename "$1")"
  run_boot "$1" "$SB/wd-idle.sh" IDLE_WATCHDOG_RUNCI_PATTERN='['
  sleep 0.5
  : >"$CI_BOOT_HALT_MARKER"
  if wait_exit "$BOOT_PID" 3; then return 1; fi
  flock -n "$IDLE_WATCHDOG_LOCKFILE" true || { kill_tree "$BOOT_PID"; return 1; }
  stays_up_aborted 0 'halt ABORTED: cannot check for CI run processes (pgrep exit 2)'
}

expect() { # <label> <want: green|red> <case-fn> <boot-file>
  [ -s "$4" ] || setup_error "$1: boot file $4 missing"
  if "$3" "$4"; then got=green; else got=red; fi
  if [ "$got" = "$2" ]; then pass "$1 ($got)"; else fail "$1" "got $got, want $2" "$CI_BOOT_LOG"; fi
}

echo "=== ci-boot.sh self-test (sandbox $SB) ==="
expect "I1 dead watchdog logged + restarted"            green case_i1  "$BOOT"
expect "I1 mutant without restart is caught"            red   case_i1  "$SB/mut-no-restart.sh"
expect "I2a run holding the lock aborts the halt"       green case_i2a "$BOOT"
expect "I2a mutant without the lock is caught"          red   case_i2a "$SB/mut-no-lock.sh"
expect "I2b queued run process aborts the halt"         green case_i2b "$BOOT"
expect "I2b mutant without the re-check is caught"      red   case_i2b "$SB/mut-no-recheck.sh"
expect "OK idle + free lock exits 0"                    green case_ok  "$BOOT"
expect "E2E real watchdog stops via ci-boot"            green case_e2e "$BOOT"
expect "SF1 unreadable watchdog state never blocks"     green case_sf1 "$BOOT"
expect "SF1 mutant waiting on an unknown child caught"  red   case_sf1 "$SB/mut-unknown-waits.sh"
expect "SF2 errors still reach stderr after an abort"   green case_sf2 "$BOOT"
expect "SF2 mutant muting stderr is caught"             red   case_sf2 "$SB/mut-stderr-muted.sh"
expect "SF3 flock failure reported as a lock failure"   green case_sf3 "$BOOT"
expect "SF3 mutant reporting it as a held lock caught"  red   case_sf3 "$SB/mut-flock-error-as-held.sh"
expect "T2 mutant treating flock failure as locked"     red   case_sf3 "$SB/mut-flock-error-as-locked.sh"
expect "T1 run arriving during the lock wait aborts"    green case_t1  "$BOOT"
expect "T1 mutant checking runs before the lock caught" red   case_t1  "$SB/mut-stale-run-check.sh"
expect "T2 unopenable lock file aborts, stays up"       green case_t2_lockopen "$BOOT"
expect "T2 mutant exiting on lock-open failure caught"  red   case_t2_lockopen "$SB/mut-lockopen-exits.sh"
expect "T2 pgrep failure aborts and frees the lock"     green case_t2_pgrep "$BOOT"
expect "T2 mutant treating pgrep failure as idle caught" red  case_t2_pgrep "$SB/mut-pgrep-error-as-idle.sh"

# control <label> <VAR=value...>: re-run this harness with a broken mutant setup; it must
# end in a SETUP ERROR, exit 2, with no PASS rows. Skipped inside the child itself.
control() {
  local label="$1" out rc; shift
  out="$(env CI_BOOT_TEST_NESTED=1 "$@" bash "$SELF" 2>&1)"; rc=$?
  printf '%s\n' "$out" >"$SB/control.out"
  if [ "$rc" -eq 2 ] && grep -q 'SETUP ERROR\|cannot run' "$SB/control.out" \
      && ! grep -q 'PASS \[' "$SB/control.out"; then
    pass "$label (exit 2, no PASS rows)"
  else
    fail "$label" "harness exit $rc, want 2 with a setup error and no PASS rows" "$SB/control.out"
  fi
}
if [ -z "${CI_BOOT_TEST_NESTED:-}" ]; then
  # an edit that keeps behaviour but removes the no-lock mutation anchor
  sed 's/flock -w "\$LOCK_WAIT_SECONDS" 7/flock --wait "$LOCK_WAIT_SECONDS" 7/' "$BOOT" >"$SB/boot-no-anchor.sh"
  cmp -s "$BOOT" "$SB/boot-no-anchor.sh" && setup_error "control fixture: anchor edit did not apply"
  control "CTL missing mutation anchor is a setup error"  CI_BOOT_TEST_BOOT="$SB/boot-no-anchor.sh"
  control "CTL missing interpreter is a setup error"      CI_BOOT_TEST_PYTHON="$SB/no-such-python3"
  control "CTL failing interpreter is a setup error"      CI_BOOT_TEST_PYTHON=false
fi
echo "=== $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
