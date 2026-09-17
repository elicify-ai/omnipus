#!/usr/bin/env bash
# idle-watchdog.sh — the machine-side backstop that powers off an idle Fly CI machine.
#
# WHY THIS EXISTS (2026-09-17): three ci-omnipus-1 machines (one performance-8x) sat
# `started` with nothing running on them for ~9.5 hours because a dispatcher run died
# without reaching its stop path. ci-cluster.sh stops machines on every exit path
# including its EXIT trap — but no trap fires when the dispatcher process is SIGKILLed,
# the laptop sleeps, or the network drops. The machine must be able to stop ITSELF.
# This is a backstop behind the dispatcher, never a replacement for it.
#
# WHAT "IDLE" MEANS HERE — evidence, not a timer (all three must hold, unbroken):
#   1. no `runci.sh` process on the box (pgrep -f runci.sh). A queued run waiting on
#      the lock is itself a runci.sh process, so queueing counts as busy. An e2e
#      shard's `( … )` subshell keeps its parent's argv, so even a dispatcher
#      SIGKILL mid-e2e leaves pgrep-matchable orphans until the last shard drains —
#      the watchdog waits them out, then fires.
#   2. no holder of /tmp/runci.lock (runci.sh's whole-run mutex, FD 9 + flock —
#      see runci.sh's "whole-run mutex" section; same lock, probed non-destructively).
#   3. no write under /cache/logs/ newer than the idle threshold (each run is tee'd
#      there for its whole life; /tmp dies with the machine, /cache survives —
#      2026-09-16 cost a night of evidence to that fact). This signal is only a
#      measure of the BETWEEN-RUNS void: a LIVE gate can legitimately be silent on
#      /cache/logs for its whole duration, because e2e shard output is redirected to
#      /cache/e2e/ (solo shards print nothing to stdout until they finish). Signals
#      1 and 2 carry every live gate; signal 3 only says "no run started recently".
#
# THRESHOLD — 2700s (45 min), and why that number:
#   It is NOT sized against a gate's quiet periods (those are covered by signals 1
#   and 2 — while any gate or queued run is alive, the box is busy however silent
#   /cache/logs is). It is sized against the longest legitimate NO-PROCESS gap:
#     - ci-cluster dispatches a tier's gates back-to-back: the gap between one gate
#       exiting and the next starting is one SSH round-trip, seconds.
#     - The dispatcher's collect-then-stop phase (sftp of each gate log, verified)
#       runs after the last process exits: ~1–5 min.
#     - A human driving one machine by hand can read a run log for ~10–20 min
#       between dispatching gate N and gate N+1.
#   45 min is >2x the longest of those, and costs at most 45 idle minutes of a
#   performance-8x when it does fire. In the other direction a false fire is cheap —
#   `fly ssh console` / `fly machine start` boots the machine back with /cache warm —
#   while a false kill destroys a run and manufactures a confusing false RED. Err
#   long. Override with IDLE_WATCHDOG_IDLE_SECONDS.
#
# FAIL-SAFE RULES (a running machine costs money; a killed gate costs a run):
#   - Any signal that cannot be determined (pgrep error, lock probe failure,
#     unreadable /cache/logs) counts as UNKNOWN and resets the idle clock. Only an
#     UNBROKEN stretch of confidently-idle observations, as long as the threshold,
#     ever powers off.
#   - If the watchdog cannot write its own log it does NOT power off: a machine that
#     vanishes with no explanation is the same class of problem as a silent test
#     failure.
#   - Immediately before powering off it re-verifies all evidence while HOLDING
#     runci's own lock. A run that started a second before the check is seen by the
#     re-run pgrep; a run that starts a second after queues on our lock and dies
#     BEFORE doing any work (runci's clone/build/test all happen after its flock) —
#     so no gate is ever killed mid-flight by the check-then-act race.
#   - Honest boundary of the evidence model: if a dispatcher death kills runci.sh
#     AND its shard subshells but leaves orphan gateways/browsers running, those
#     orphans match no signal, and the watchdog will power off under them after the
#     threshold. That is the intended recovery, not a mistake — their run is already
#     uncollectable, their /cache evidence survives the stop, and the box is burning
#     a performance-8x on nothing.
#
# START MECHANISM — nohup/setsid daemon, started at dispatch time. Pick exactly one
# place to start it from:
#
#     fly ssh sftp put deploy/ci-worker/idle-watchdog.sh /cache/idle-watchdog.sh \
#         --app ci-omnipus-1 --machine <id>
#     fly ssh console --app ci-omnipus-1 --machine <id> -C \
#         'chmod +x /cache/idle-watchdog.sh && /cache/idle-watchdog.sh start'
#
#   Why nohup and not systemd or cron: this image has neither — PID 1 is
#   `sleep infinity` (Dockerfile CMD) and no cron daemon is installed. And no process
#   of any kind can survive a Fly machine stop: the rootfs is destroyed, only the
#   /cache volume is re-mounted, and nothing under /cache auto-executes on boot. So
#   "survives a machine restart without a human" can only mean "is started again on
#   the next attended start". Machines here are only ever started in order to
#   dispatch work, and every dispatch path already opens an SSH console to the
#   machine — the natural wiring point is ci-cluster.sh's machine prep (right after
#   its /cache/runci.sh md5 check), so the watchdog starts on every dispatcher-driven
#   boot with no human action. setsid (Linux) detaches it fully from the SSH session,
#   which is precisely the failure mode being backstopped. Known gap for the lead to
#   close (Dockerfile is outside this script's lane): a machine started but never
#   dispatched to has no watchdog until the image CMD is changed to something like
#     CMD ["sh", "-c", "/cache/idle-watchdog.sh start; exec sleep infinity"]
#
# STATE AND LOGS — what survives a stop/start lives on /cache (constraint 2):
#   /cache/idle-watchdog/idle-since   epoch of the first idle observation of the
#                                     current idle stretch (survives a WATCHDOG
#                                     restart within one machine life).
#   /cache/logs/idle-watchdog.log     the human-readable record, INCLUDING the
#                                     POWERING OFF entry with all evidence — check
#                                     it before assuming a machine crashed. Never
#                                     matched by runci's "<gate>@*.log" prune glob,
#                                     and excluded from this script's own activity
#                                     scan (a watchdog writing its own log must not
#                                     keep its own machine alive).
#   /tmp/idle-watchdog.gen            boot-generation marker. /tmp is wiped on every
#                                     machine stop, so its absence at startup means
#                                     FRESH BOOT → the persisted idle-since is
#                                     discarded (a just-booted machine may be
#                                     seconds away from its first dispatch; a stale
#                                     pre-boot clock would power it off mid-handshake).
#   /tmp/idle-watchdog.pid            pidfile; dies with the machine by construction,
#                                     so after any restart there is no stale pid.
#
# USAGE: idle-watchdog.sh start     daemonize (idempotent — a live instance is a no-op)
#        idle-watchdog.sh run       foreground loop (tests, debugging)
#        idle-watchdog.sh check     print one observation + the idle clock, then exit
#                                   (read-only — "why is this machine still up?")
# All IDLE_WATCHDOG_* env vars below are the test seam: the verification harness runs
# this exact file, unmodified, against a sandboxed /tmp+/cache with a stub poweroff
# and an injectable clock.
set -uo pipefail

# --- configuration (defaults are the production values) -------------------------------------
IDLE_SECONDS="${IDLE_WATCHDOG_IDLE_SECONDS:-2700}"   # idle stretch required before poweroff
POLL_SECONDS="${IDLE_WATCHDOG_POLL_SECONDS:-60}"     # seconds between observations
POWEROFF_CMD="${IDLE_WATCHDOG_POWEROFF_CMD:-poweroff}" # single command, no arguments
LOCKFILE="${IDLE_WATCHDOG_LOCKFILE:-/tmp/runci.lock}" # runci.sh's whole-run mutex
LOGS_DIR="${IDLE_WATCHDOG_LOGS_DIR:-/cache/logs}"      # runci.sh tee's every run here
STATE_DIR="${IDLE_WATCHDOG_STATE_DIR:-/cache/idle-watchdog}"
RUNCI_PATTERN="${IDLE_WATCHDOG_RUNCI_PATTERN:-runci.sh}"
NOW_FILE="${IDLE_WATCHDOG_NOW_FILE:-}"               # unset ⇒ real clock; set ⇒ file containing an epoch (tests)
BOOT_MARKER="${IDLE_WATCHDOG_BOOT_MARKER:-/tmp/idle-watchdog.gen}"
PIDFILE="${IDLE_WATCHDOG_PIDFILE:-/tmp/idle-watchdog.pid}"
LOG_MAX_BYTES="${IDLE_WATCHDOG_LOG_MAX_BYTES:-262144}"

SELF_LOG_BASENAME="idle-watchdog.log" # runci prunes only "<gate>@*.log", so this name is never pruned
LOG_FILE="$LOGS_DIR/$SELF_LOG_BASENAME"
IDLE_SINCE_FILE="$STATE_DIR/idle-since"
STAT_MODE="" # set by detect_stat_mode: gnu | bsd | none

# --- small utilities ------------------------------------------------------------------------

now() { # current epoch — real clock, or the injected one for tests
  if [ -n "$NOW_FILE" ]; then
    local injected
    injected=$(cat "$NOW_FILE" 2>/dev/null) || { echo 0; return 1; }
    case "$injected" in
      ''|*[!0-9]*) echo 0; return 1 ;;
      *) echo "$injected"; return 0 ;;
    esac
  fi
  date +%s
}

utc_stamp() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# detect_stat_mode: mtime in epoch seconds is `stat -c %Y` (GNU/Linux) or `stat -f %m`
# (BSD/macOS). Detected once; "none" leaves the log signal permanently UNKNOWN, which
# by the fail-safe rules means the watchdog can observe but never power off.
detect_stat_mode() {
  if stat -c %Y / >/dev/null 2>&1; then STAT_MODE=gnu
  elif stat -f %m / >/dev/null 2>&1; then STAT_MODE=bsd
  else STAT_MODE=none
  fi
}

stat_mtime() { # $1 file → epoch mtime on stdout; non-zero if it could not be read
  case "$STAT_MODE" in
    gnu) stat -c %Y -- "$1" 2>/dev/null ;;
    bsd) stat -f %m -- "$1" 2>/dev/null ;;
    *)   return 2 ;;
  esac
}

# log_msg: the operator-facing record. Always echoes to stdout (foreground/debug) and
# appends to $LOG_FILE with a size cap. Non-zero return = the log could not be
# written — callers treat that as "do not act" (never an unexplained poweroff).
log_msg() { # $@ message
  local line size tmp
  line="$(utc_stamp) idle-watchdog[$$]: $*"
  echo "$line"
  if [ -f "$LOG_FILE" ]; then
    size=$(wc -c <"$LOG_FILE" 2>/dev/null | tr -d '[:space:]') || size=0
    case "$size" in ''|*[!0-9]*) size=0 ;; esac
    if [ "$size" -gt "$LOG_MAX_BYTES" ]; then
      # Keep the most recent half; the cut can start mid-line, which is fine for a log.
      tmp="$LOG_FILE.old"
      if tail -c $((LOG_MAX_BYTES / 2)) "$LOG_FILE" >"$tmp" 2>/dev/null; then
        mv -f "$tmp" "$LOG_FILE" || rm -f "$tmp"
      else
        rm -f "$tmp"
      fi
    fi
  fi
  mkdir -p "$LOGS_DIR" 2>/dev/null
  printf '%s\n' "$line" >>"$LOG_FILE" 2>/dev/null || {
    echo "idle-watchdog[$$]: CANNOT WRITE $LOG_FILE — treating state as undeterminable" >&2
    return 1
  }
  return 0
}

# --- the three evidence signals -------------------------------------------------------------
# Each prints one of:  busy:<reason> | idle | unknown:<reason>

runci_process_state() {
  if ! command -v pgrep >/dev/null 2>&1; then
    echo "unknown:no pgrep on this box"; return 0
  fi
  pgrep -f "$RUNCI_PATTERN" >/dev/null 2>&1
  case $? in
    0) echo "busy:process matching '$RUNCI_PATTERN' present" ;;
    1) echo "idle" ;;
    # 2 = pgrep itself errored (bad pattern, /proc unreadable) — never "no match".
    *) echo "unknown:pgrep failed (exit $?)" ;;
  esac
}

lock_state() {
  local rc
  if ! command -v flock >/dev/null 2>&1; then
    echo "unknown:no flock on this box (runci.sh's own mutex would be broken too)"; return 0
  fi
  # Probe on FD 8 (runci uses FD 9; the poweroff path below does use 9 — never both at
  # once). Opening with >> creates the file if absent, exactly as runci's own open does;
  # content is irrelevant to flock, which binds to the open file description.
  if ! exec 8>>"$LOCKFILE" 2>/dev/null; then
    echo "unknown:cannot open $LOCKFILE"; return 0
  fi
  flock -n 8 2>/dev/null
  rc=$?
  exec 8>&- 2>/dev/null
  case "$rc" in
    0) echo "idle" ;;                    # we acquired it ⇒ nobody held it; released on close
    1) echo "busy:/tmp/runci.lock held" ;; # someone holds the whole-run mutex
    *) echo "unknown:flock probe exited $rc" ;;
  esac
}

# newest_ci_log: newest non-watchdog file in $LOGS_DIR. Prints "name<TAB>epoch-mtime".
#   exit 0 = found; exit 1 = determinately none (empty dir / dir absent — runci creates
#   it on first run); exit 2 = cannot determine (unreadable dir, stat failure).
newest_ci_log() {
  local f t best="" best_t=0
  if [ ! -e "$LOGS_DIR" ]; then return 1; fi
  if [ ! -r "$LOGS_DIR" ] || [ ! -x "$LOGS_DIR" ]; then return 2; fi
  # ls -t lists newest-first; run-log names are <gate>@<ref>-<stamp>.log (no newlines,
  # same assumption runci's own prune makes). Own log is skipped: writing it must not
  # count as CI activity. A file vanishing mid-scan (prune at run START, when a process
  # is alive and busy anyway) degrades to the next-newest file — conservative direction.
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    [ "$f" = "$SELF_LOG_BASENAME" ] && continue
    t=$(stat_mtime "$LOGS_DIR/$f") || return 2
    case "$t" in ''|*[!0-9]*) return 2 ;; esac
    if [ "$t" -gt "$best_t" ]; then best_t=$t; best="$f"; fi
  done < <(ls -t -- "$LOGS_DIR" 2>/dev/null)
  [ -n "$best" ] || return 1
  printf '%s\t%s\n' "$best" "$best_t"
}

# observe: busy wins over unknown, unknown wins over idle — the AND of three signals
# where any non-idle reading blocks a poweroff.
observe() {
  local proc lock ncl t age now_s
  proc=$(runci_process_state)
  case "$proc" in busy:*) echo "$proc"; return 0 ;; esac
  lock=$(lock_state)
  case "$lock" in busy:*) echo "$lock"; return 0 ;; esac
  case "$proc" in unknown:*) echo "$proc"; return 0 ;; esac
  case "$lock" in unknown:*) echo "$lock"; return 0 ;; esac
  ncl=$(newest_ci_log)
  case $? in
    2) echo "unknown:cannot read $LOGS_DIR"; return 0 ;;
    1) echo "idle"; return 0 ;; # no CI log has ever been written here
    *)
      t=${ncl#*$'\t'}
      now_s=$(now) || { echo "unknown:cannot read the clock"; return 0; }
      age=$(( now_s - t ))
      # A negative age (mtime in the future / clock skew) counts as RECENT — fail safe.
      if [ "$age" -lt "$IDLE_SECONDS" ]; then
        echo "busy:recent /cache/logs write (${ncl%%$'\t'*}, ${age}s ago)"
      else
        echo "idle"
      fi
      ;;
  esac
}

# --- idle-clock persistence -----------------------------------------------------------------

read_idle_since() { # → epoch or nothing
  [ -r "$IDLE_SINCE_FILE" ] || return 1
  local v
  v=$(cat "$IDLE_SINCE_FILE" 2>/dev/null) || return 1
  case "$v" in ''|*[!0-9]*) return 1 ;; esac
  echo "$v"
}

write_idle_since() { # $1 epoch
  mkdir -p "$STATE_DIR" 2>/dev/null || return 1
  printf '%s\n' "$1" >"$IDLE_SINCE_FILE" 2>/dev/null || return 1
}

clear_idle_since() { rm -f -- "$IDLE_SINCE_FILE" 2>/dev/null; }

# boot_check: reset or adopt the persisted idle clock. /tmp is wiped on every machine
# stop, so a missing BOOT_MARKER means this is a fresh boot — discard any pre-boot
# idle-since (the machine may be seconds from its first dispatch) and start the grace
# period from now. A PRESENT marker means the same machine life: a restarted watchdog
# adopts the recorded clock instead of re-earning the whole threshold.
boot_check() {
  mkdir -p "$STATE_DIR" 2>/dev/null || true
  if [ -e "$BOOT_MARKER" ]; then return 0; fi
  clear_idle_since
  : >"$BOOT_MARKER" 2>/dev/null || true
}

# --- poweroff --------------------------------------------------------------------------------

# attempt_poweroff: log the why FIRST (the log is on /cache and must survive us),
# then act while holding runci's own mutex so no gate can be killed mid-flight.
attempt_poweroff() {
  local idle_since now_s ncl t age
  idle_since=$(read_idle_since)
  idle_since=${idle_since:-0}
  now_s=$(now) || { log_msg "cannot read the clock — NOT powering off" || true; return 1; }

  # Take runci's whole-run mutex (FD 9, as runci itself does). A run that began a
  # moment ago is caught by the pgrep re-check below; a run that begins after this
  # point queues on our lock and dies before doing any work.
  if ! exec 9>>"$LOCKFILE" 2>/dev/null; then
    log_msg "cannot open $LOCKFILE for the pre-poweroff check — NOT powering off" || true
    return 1
  fi
  if ! flock -n 9 2>/dev/null; then
    exec 9>&- 2>/dev/null
    log_msg "a run arrived and holds $LOCKFILE — poweroff aborted" || true
    return 1
  fi
  # We hold the lock, so lock_state() would report OURSELVES busy — re-check only the
  # process and log signals here, both of which are independent of FD 9.
  if [ "$(runci_process_state)" != "idle" ]; then
    exec 9>&- 2>/dev/null
    log_msg "pre-poweroff re-check not idle — poweroff aborted" || true
    return 1
  fi
  ncl=$(newest_ci_log)
  case $? in
    0)
      t=${ncl#*$'\t'}
      age=$(( now_s - t ))
      if [ "$age" -lt "$IDLE_SECONDS" ]; then
        exec 9>&- 2>/dev/null
        log_msg "pre-poweroff re-check: recent /cache/logs write — poweroff aborted" || true
        return 1
      fi
      ;;
    1) ncl=$'none-ever\t-"; age="never" ;;
    *)
      exec 9>&- 2>/dev/null
      log_msg "pre-poweroff re-check: cannot determine /cache/logs state — NOT powering off" || true
      return 1
      ;;
  esac

  # Constraint 4: the record must land BEFORE the machine dies, and if it cannot land
  # at all, the poweroff must not happen — no unexplained vanishings.
  log_msg "POWERING OFF: idle $(( now_s - idle_since ))s (threshold ${IDLE_SECONDS}s) — no '${RUNCI_PATTERN}' process, no ${LOCKFILE} holder, newest ${LOGS_DIR} write: ${ncl%%$'\t'*} (${age}s ago)" \
    || { exec 9>&- 2>/dev/null; echo "idle-watchdog[$$]: log unwritable — NOT powering off" >&2; return 1; }

  "$POWEROFF_CMD"
  local rc=$?
  # A successful poweroff never returns to us. If it does (failed command, wrong box),
  # release the mutex — otherwise every new run would queue 90 min behind a dead holder
  # — and keep watching; the next cycle re-earns the whole verdict from scratch.
  sleep 5
  exec 9>&- 2>/dev/null
  log_msg "poweroff command returned rc=$rc and the machine is still up — resuming watch" || true
  return 1
}

# --- main loop -------------------------------------------------------------------------------

run_loop() {
  local obs idle_since now_s
  detect_stat_mode
  boot_check
  if [ "$STAT_MODE" = none ]; then
    log_msg "no usable stat on this box — the log signal will always read unknown; the watchdog will observe but NEVER power off" || true
  fi
  log_msg "watchdog up (pid $$): idle threshold ${IDLE_SECONDS}s, poll ${POLL_SECONDS}s, lock $LOCKFILE, logs $LOGS_DIR, pattern '$RUNCI_PATTERN'" || true

  while :; do
    obs=$(observe)
    case "$obs" in
      idle)
        now_s=$(now) || now_s=0
        if idle_since=$(read_idle_since); then
          if [ $(( now_s - idle_since )) -ge "$IDLE_SECONDS" ]; then
            attempt_poweroff || true
          fi
        else
          # First idle observation of this stretch — the clock starts NOW, never
          # back-dated from log mtimes (a fresh boot's stale pre-boot clock must not
          # fast-forward the grace period; err late, always).
          write_idle_since "$now_s" \
            && log_msg "idle: no run process, no lock holder, no recent ${LOGS_DIR} write — idle clock starts (threshold ${IDLE_SECONDS}s)" \
            || log_msg "cannot persist idle clock to $IDLE_SINCE_FILE — NOT powering off on this stretch" || true
        fi
        ;;
      *)
        if read_idle_since >/dev/null; then
          clear_idle_since
          log_msg "idle stretch ended: $obs" || true
        fi
        ;;
    esac
    sleep "$POLL_SECONDS"
  done
}

# --- entry points ----------------------------------------------------------------------------

start_cmd() { # daemonize; idempotent
  local pid
  if [ -f "$PIDFILE" ]; then
    pid=$(cat "$PIDFILE" 2>/dev/null)
    case "$pid" in
      ''|*[!0-9]*) : ;;
      *)
        if kill -0 "$pid" 2>/dev/null; then
          echo "idle-watchdog already running (pid $pid)"
          return 0
        fi
        ;;
    esac
    rm -f -- "$PIDFILE"
  fi
  mkdir -p "$LOGS_DIR" 2>/dev/null || true
  # setsid (present on the Linux worker) detaches fully from the SSH session; nohup is
  # the macOS/test fallback. The pidfile lives in /tmp so it dies with the machine —
  # after a restart there IS no watchdog, and no stale pid to clean up by hand.
  if command -v setsid >/dev/null 2>&1; then
    setsid "$0" run </dev/null >>"$LOG_FILE" 2>&1 &
  else
    nohup "$0" run </dev/null >>"$LOG_FILE" 2>&1 &
  fi
  pid=$!
  echo "$pid" >"$PIDFILE" 2>/dev/null || true
  echo "idle-watchdog started (pid $pid, log $LOG_FILE)"
}

case "${1:-}" in
  start) start_cmd ;;
  run)   run_loop ;;
  *)     sed -n '2,120p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2 ;; # usage = the header
esac
