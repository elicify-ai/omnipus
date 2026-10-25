#!/usr/bin/env bash
# ci-boot.sh — the main process of the CI worker image (Dockerfile CMD).
#
# Two jobs, both from the PR #1396 review (2026-10-21):
#
#   1. SUPERVISE the idle-watchdog. It runs as this process's child (`run`, not the
#      detached `start`), so its death is seen: logged loudly to stdout (= `fly logs`)
#      and to /cache/logs/idle-watchdog.log, then restarted. A dead watchdog never
#      stops the machine — the box may be busy, and killing a gate because the
#      backstop broke is worse than an idle box. The elicify-guard also reports
#      "self-stop timer running" per CI machine every hour.
#
#   2. STOP THE MACHINE SAFELY. Fly stops a machine when its main process exits
#      (exit 0 is not a failure for the on-failure restart policy); `poweroff` cannot
#      work here (it is systemctl, and there is no systemd). The watchdog's poweroff
#      hook (ci-machine-halt) only drops a halt marker. Before exiting, this process
#      takes runci.sh's whole-run mutex ITSELF and re-checks for a run process under
#      it — the watchdog releases its own hold a few seconds after the hook returns,
#      so without this a gate admitted in that gap would be cut off by the stop. If a
#      run holds the lock, or a runci.sh process exists (a queued run counts), the
#      halt is aborted and the marker removed. The lock stays held until this process
#      is gone, so no gate can be admitted between the check and the exit.
#
# Every setting has a CI_BOOT_* / IDLE_WATCHDOG_* override: that is the test seam
# (deploy/ci-worker/ci-boot.test.sh runs this exact file in a sandbox).
set -uo pipefail

WATCHDOG="${CI_BOOT_WATCHDOG:-/usr/local/bin/idle-watchdog.sh}"
export CI_BOOT_HALT_MARKER="${CI_BOOT_HALT_MARKER:-/tmp/ci-machine.halt}"
export IDLE_WATCHDOG_POWEROFF_CMD="${IDLE_WATCHDOG_POWEROFF_CMD:-/usr/local/bin/ci-machine-halt}"
LOCKFILE="${IDLE_WATCHDOG_LOCKFILE:-/tmp/runci.lock}"
RUNCI_PATTERN="${IDLE_WATCHDOG_RUNCI_PATTERN:-runci.sh}"
LOG_FILE="${CI_BOOT_LOG:-${IDLE_WATCHDOG_LOGS_DIR:-/cache/logs}/idle-watchdog.log}"
POLL_SECONDS="${CI_BOOT_POLL_SECONDS:-2}"
RESTART_SECONDS="${CI_BOOT_RESTART_SECONDS:-10}"
LOCK_WAIT_SECONDS="${CI_BOOT_LOCK_WAIT_SECONDS:-60}"
PROC_DIR="${CI_BOOT_PROC_DIR:-/proc}"

WD_PID=""
WD_STATE=""      # alive | dead | unknown, set by watchdog_state
WD_UNKNOWN=""    # set while the watchdog's state cannot be read (logged once)

say() { # one line to stdout (fly logs) and to the watchdog's log on /cache
  local line
  line="$(date -u +%Y-%m-%dT%H:%M:%SZ) ci-boot[$$]: $*"
  echo "$line"
  mkdir -p "${LOG_FILE%/*}" 2>/dev/null
  printf '%s\n' "$line" >>"$LOG_FILE" 2>/dev/null || echo "ci-boot[$$]: cannot write $LOG_FILE" >&2
}

start_watchdog() {
  "$WATCHDOG" run </dev/null &
  WD_PID=$!
  say "idle-watchdog started under supervision (pid $WD_PID)"
}

# watchdog_state: sets WD_STATE. A dead child can linger as a zombie until reaped,
# and kill -0 succeeds on a zombie, so the state letter is read from /proc with a
# shell builtin (no ps that could fail). "dead" only when death is CONFIRMED: an
# unreadable state is "unknown", and the caller must never wait on such a child (a
# live child would block the loop, and with it the halt marker, until it exits).
watchdog_state() {
  local stat
  if [ -z "$WD_PID" ] || ! kill -0 "$WD_PID" 2>/dev/null; then
    WD_STATE=dead; return
  fi
  if ! { read -r stat <"$PROC_DIR/$WD_PID/stat"; } 2>/dev/null; then # logged by the caller
    WD_STATE=unknown; return
  fi
  stat="${stat##*) }" # /proc/PID/stat: "pid (comm) S ..."; comm may hold spaces
  case "${stat%% *}" in
    Z) WD_STATE=dead ;;
    ?) WD_STATE=alive ;;
    *) WD_STATE=unknown ;;
  esac
}

# FD 7 redirections are done with a bare `exec`, which applies them to this whole
# process: never add 2>/dev/null there, or every later error message is lost too.
abort_halt() { # $1 reason
  exec 7>&-
  rm -f -- "$CI_BOOT_HALT_MARKER" \
    || say "cannot remove halt marker $CI_BOOT_HALT_MARKER — the halt will be retried and aborted again"
  say "halt ABORTED: $1 — machine stays up, the watchdog keeps watching"
}

# try_halt: called when the halt marker exists. Exits 0 (machine stops) only while
# holding the run lock with no run process present; otherwise aborts and returns.
try_halt() {
  local rc
  if ! exec 7>>"$LOCKFILE"; then
    abort_halt "cannot open the run lock $LOCKFILE (check that its directory exists and is writable)"
    return 1
  fi
  # The watchdog still holds the lock for a few seconds after its hook returned;
  # wait for it. A run that owns the lock for longer wins, and we abort. flock exits
  # 1 on a timeout; any other non-zero status is flock itself failing, not a run.
  flock -w "$LOCK_WAIT_SECONDS" 7
  rc=$?
  case "$rc" in
    0) ;;
    1) abort_halt "$LOCKFILE still held after ${LOCK_WAIT_SECONDS}s (a CI run owns the machine)"; return 1 ;;
    *) abort_halt "lock check FAILED on $LOCKFILE (flock exit $rc; check flock and the lock path)"; return 1 ;;
  esac
  pgrep -f "$RUNCI_PATTERN" >/dev/null
  rc=$?
  case "$rc" in
    1) ;; # no run process: safe
    0) abort_halt "a CI run process is present (running or queued)"; return 1 ;;
    *) abort_halt "cannot check for CI run processes (pgrep exit $rc)"; return 1 ;;
  esac
  say "halt: run lock held, no CI run process — main process exits 0, Fly stops the machine"
  exit 0 # FD 7 keeps the run lock until this process is gone
}

say "boot: watchdog=$WATCHDOG lock=$LOCKFILE halt-marker=$CI_BOOT_HALT_MARKER"
start_watchdog
while :; do
  if [ -e "$CI_BOOT_HALT_MARKER" ]; then
    try_halt || true
  fi
  watchdog_state
  case "$WD_STATE" in
    alive)
      if [ -n "$WD_UNKNOWN" ]; then say "watchdog state readable again (pid $WD_PID alive)"; WD_UNKNOWN=""; fi ;;
    unknown)
      # Keep handling the halt marker; never block in wait on a child that may be alive.
      if [ -z "$WD_UNKNOWN" ]; then
        say "WATCHDOG STATE UNKNOWN: cannot read $PROC_DIR/$WD_PID/stat — not restarting it, retrying every ${POLL_SECONDS}s"
        WD_UNKNOWN=1
      fi ;;
    dead)
      WD_UNKNOWN=""
      wait "$WD_PID" 2>/dev/null
      rc=$?
      say "WATCHDOG DIED: idle-watchdog (pid $WD_PID) exited with status $rc — idle self-stop is OFF until it restarts; restarting in ${RESTART_SECONDS}s"
      sleep "$RESTART_SECONDS"
      start_watchdog ;;
  esac
  sleep "$POLL_SECONDS"
done
