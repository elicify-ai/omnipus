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
#
# Usage: bash deploy/ci-worker/ci-boot.test.sh      exit 0 = all PASS, 1 = a FAIL, 2 = cannot run
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BOOT="$HERE/ci-boot.sh"
HALT_HOOK="$HERE/ci-machine-halt"
WATCHDOG_REAL="$HERE/idle-watchdog.sh"
for t in flock pgrep ps; do
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

# run_boot <boot-file> <watchdog> → starts ci-boot in the background; sets BOOT_PID
run_boot() {
  CI_BOOT_WATCHDOG="$2" bash "$1" >"$CASE_DIR/boot.out" 2>&1 &
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

# --- mutants ---------------------------------------------------------------------------------
mutate() { # <name> <python-replace-old> <new>
  python3 - "$BOOT" "$SB/mut-$1.sh" "$2" "$3" <<'EOF'
import sys
src, dst, old, new = sys.argv[1:5]
s = open(src).read()
assert old in s, "mutation anchor missing: " + old
open(dst, "w").write(s.replace(old, new, 1))
EOF
}
mutate no-restart '    start_watchdog
  fi
  sleep "$POLL_SECONDS"' '    exit 0
  fi
  sleep "$POLL_SECONDS"'
mutate no-lock 'if ! flock -w "$LOCK_WAIT_SECONDS" 7 2>/dev/null; then' 'if false; then'
mutate no-recheck '    0) abort_halt "a CI run process is present (running or queued)"; return 1 ;;' '    0) ;;'

# --- cases -------------------------------------------------------------------------------------

case_i1() { # <boot-file> → 0 if behaviour correct
  new_case "i1-$(basename "$1")"
  export WD_COUNTER="$CASE_DIR/counter"
  run_boot "$1" "$SB/wd-dies-once.sh"
  sleep 3
  kill -0 "$BOOT_PID" 2>/dev/null || return 1
  [ "$(cat "$WD_COUNTER" 2>/dev/null)" = 2 ] || return 1
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

expect() { # <label> <want: green|red> <case-fn> <boot-file>
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
echo "=== $PASS passed, $FAIL failed ==="
[ "$FAIL" -eq 0 ]
