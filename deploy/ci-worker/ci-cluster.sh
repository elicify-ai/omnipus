#!/usr/bin/env bash
# ci-cluster.sh — fan CI gates out across the Fly worker cluster and scale to zero.
#
# ONE app (default ci-omnipus-1), several machines: each machine is a TIER with its
# own /cache volume and its own /tmp/runci.lock, addressed by machine id:
#
#   fly ssh console --app ci-omnipus-1 --machine <id> -C "/cache/runci.sh <ref> <gate> <attempt>"
#
# The dispatcher runs from a developer machine, inside the repo checkout:
#
#   1. START only the machines the active tiers need, and wait until each is actually
#      SSH-reachable — a started machine is not immediately usable.
#   2. VERIFY /cache/runci.sh on every machine it will use matches the repo copy
#      (md5) BEFORE dispatching anything. runci.sh is deployed PER MACHINE, so
#      without this check tiers can run different script versions and the verdict
#      is meaningless (deploy/ci-worker/CLAUDE.md trap 2).
#   3. DISPATCH one tier per machine, concurrently. Each gate is its own
#      `fly ssh console` invocation captured to its own log file; gates within a
#      tier run sequentially because runci.sh holds a whole-run lock per machine.
#      While a gate's console runs, a liveness watchdog polls the machine: if
#      THIS gate's remote runci.sh process is gone (or the machine unreachable)
#      for DEAD_STRIKES consecutive probes and the capture has no verdict, the
#      verdict is recovered from the remote RUNLOG or the tier ends with an
#      explicit STALLED/DIED failure — a dead session must never hang the
#      dispatcher silently (2026-10-18: a half-open console hung a run ~3h with
#      no verdict after the remote lint gate died mid-write at 13:59:26Z).
#   4. PARSE every log for runci.sh's RESULT markers — the SSH wrapper's exit code
#      is NOT the gate's (wrapper-exit-code false-green, CLAUDE.md trap 3).
#   5. ASSERT every worker printed the same `HEAD:` sha and that it is the ref
#      requested (stale-checkout false-red, CLAUDE.md trap 1).
#   6. COLLECT every dispatched gate's run log off its machine (`fly ssh sftp
#      get` — the console cannot transfer files, it does not forward stdin)
#      and VERIFY each copy: non-zero size AND the run's `HEAD:` line with the
#      expected sha. This happens BEFORE any stop, on every exit path. /tmp
#      dies with the machine, and on 2026-09-16 stopping first destroyed a
#      whole night of failure evidence (4 races, 10 SPA failures, 1 e2e shard,
#      every trace unread). A machine whose evidence cannot be secured is LEFT
#      RUNNING and named in the summary with its hand-retrieval command — a
#      running machine costs money; destroyed evidence costs a night.
#   7. STOP every machine this run started — normal exit, gate failure, AND
#      Ctrl-C (EXIT trap) — but only AFTER its logs are verified local. A
#      dispatcher that leaves 8-CPU machines running on an error is worse than
#      no dispatcher, so pre-flight refusals (nothing ever dispatched) still
#      stop their machines. Machines that were ALREADY running when we arrived
#      are never stopped: another session may own them.
#
# Usage:
#   deploy/ci-worker/ci-cluster.sh <git-ref> [tier ...]
#     <git-ref>   branch, tag, or sha — resolved the same way runci.sh resolves it
#     [tier ...]  restrict the run to these tiers (default: every configured tier
#                 whose machine id is present)
#
# Exit codes:
#   0  every dispatched gate PASSed (and HEAD shas all matched) and every
#      dispatched machine's logs were collected+verified local
#   1  any gate failed, any log has no RESULT line (incomplete) or ended in the
#      explicit STALLED/DIED verdict (remote gate gone, no recoverable verdict),
#      a HEAD mismatch, or a machine's evidence could not be collected+verified —
#      that machine is LEFT RUNNING and named in the summary
#   2  usage/config error or pre-flight refusal (no fly, unresolvable ref, machine
#      unreachable, runci.sh md5 mismatch)
#
# Machines are DATA, never flow. Machine ids come from CI_CLUSTER_*_MACHINE env
# vars (tier machines are provisioned by the harness, not by this script); the
# whole map from CI_CLUSTER_TIERS; the app name from CI_CLUSTER_APP. The script
# works with one, two, or three machines present: a tier with no machine id is
# skipped with a notice, and the rest still run.
#
# Design rationale: docs/internal/architecture/ci-cluster-design.md.
#
# Bash 3.2 compatible on purpose — this runs on developer Macs, whose /bin/bash
# predates mapfile, associative arrays, and empty-array expansion under set -u.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RUNCI_LOCAL="$SCRIPT_DIR/runci.sh"

# --- configuration: the tier→machine→gates map (data, not flow) ---------------------------
APP="${CI_CLUSTER_APP:-ci-omnipus-1}"
START_TIMEOUT="${CI_CLUSTER_START_TIMEOUT:-180}"   # seconds to wait for a started machine
PROBE_INTERVAL=5
# Gate-watchdog cadence (2026-10-19, the 3h silent hang): while a dispatched
# gate's console runs, the tier polls REMOTE liveness — is a runci.sh still
# alive on the machine — every WATCH_INTERVAL seconds, and after DEAD_STRIKES
# consecutive not-alive probes ends the tier with an explicit STALLED/DIED
# verdict instead of waiting forever (a half-open SSH flow never returns).
# Liveness, NOT a timer: a healthy go-race (~25 min) probes 'alive' on every
# poll and is never killed, however long it runs. DEAD_STRIKES > 1 rides out
# the seconds between a console connecting and runci.sh first appearing.
WATCH_INTERVAL="${CI_CLUSTER_WATCH_INTERVAL:-30}"  # seconds between liveness probes
DEAD_STRIKES="${CI_CLUSTER_DEAD_STRIKES:-3}"       # consecutive not-alive probes → STALLED/DIED
# The watchdog's own I/O is bounded on clocks separate from a healthy gate's
# runtime: one control probe or recovery transfer may take CONTROL_TIMEOUT s; a
# console killed by the watchdog gets REAP_GRACE s after TERM before KILL.
CONTROL_TIMEOUT="${CI_CLUSTER_CONTROL_TIMEOUT:-20}"
REAP_GRACE="${CI_CLUSTER_REAP_GRACE:-5}"
# Unique per dispatcher run; with tier+gate it is the attempt identity that
# runci.sh echoes (ATTEMPT: line) and that recovered/collected evidence must carry.
RUN_ID="$$-$(date +%s)-$RANDOM"

# CI_CLUSTER_TIERS overrides the whole map. One tier per line:
#   <name>|<machine-id>|<gate> <gate> ...
# The default map takes machine ids from per-tier env vars. Founder decision 2026-10-21:
# `spa` follows all Go gates in the `go` tier on the PERFORMANCE machine; full e2e runs
# there afterwards, never in parallel. The 2026-10-12 no-resize ruling stands. `node`
# keeps contracts only, and `xplat` stays on SHARED: contracts and spa each reinstall
# node_modules, so keeping contracts separate preserves every per-tier override.
# Override any id with the env var; an explicitly EMPTY id (CI_CLUSTER_NODE_MACHINE=)
# means "tier not provisioned" and the tier is skipped.
default_tier_map() {
  printf '%s\n' \
    "go|${CI_CLUSTER_GO_MACHINE-080d74db170408}|gofmt go-build go-vet lint go-test go-race spa" \
    "node|${CI_CLUSTER_NODE_MACHINE-d8d0d90a1e9648}|contracts" \
    "xplat|${CI_CLUSTER_XPLAT_MACHINE-78113d4f329908}|embed-build records-no-sqlite cli-verb-guard"
}

# --- run state -----------------------------------------------------------------------------
STARTED_IDS=()   # machine ids THIS run started (and only those — stopped on exit)
TIER_PIDS=()     # background tier jobs
PID_TIERS=()     # pid → tier name, for progress messages
LOG_DIR=""
# Globals published for the EXIT trap: it runs after main has returned, where
# main's locals no longer exist, and the collection phase (and the stop gate
# below) must still work there — Ctrl-C must collect before it stops.
PLAN_TIERS=()    # tier plan mirrors, published when dispatch begins
PLAN_MACHINES=()
PLAN_GATES=()
VERIFIED_IDS=""  # space-separated machine ids whose logs are verified LOCAL
COLLECTION_DONE=0
EXPECTED_FULL="" # published so trap-time collection can verify HEAD shas
EXPECTED_SHORT=""

info() { printf '[ci-cluster] %s\n' "$*"; }
warn() { printf '[ci-cluster] WARNING: %s\n' "$*" >&2; }
die()  { printf '[ci-cluster] ERROR: %s\n' "$*" >&2; exit 2; }

usage() {
  cat <<'EOF'
ci-cluster.sh — fan CI gates across the Fly worker cluster, scale to zero.

Usage: deploy/ci-worker/ci-cluster.sh <git-ref> [tier ...]

Default tiers (concurrent across machines; gates sequential within each tier):
  go     gofmt go-build go-vet lint go-test go-race spa               (performance)
  node   contracts                                     (shared)
  xplat  embed-build records-no-sqlite cli-verb-guard    (shared)
Full e2e runs separately on performance after the entire go tier, including spa.

Env:
  CI_CLUSTER_APP            Fly app name (default ci-omnipus-1)
  CI_CLUSTER_GO_MACHINE     machine id for Go + SPA       (default 080d74db170408, performance)
  CI_CLUSTER_NODE_MACHINE   machine id for contracts      (default d8d0d90a1e9648, shared)
  CI_CLUSTER_XPLAT_MACHINE  machine id of the `xplat` tier (default 78113d4f329908, shared)
  CI_CLUSTER_TIERS          full map override, one "<name>|<machine-id>|<gates>" per line
  CI_CLUSTER_START_TIMEOUT  seconds to wait for a started machine to become reachable
  CI_CLUSTER_WATCH_INTERVAL seconds between gate-liveness probes (default 30)
  CI_CLUSTER_DEAD_STRIKES   consecutive not-alive probes before STALLED/DIED (default 3)
  CI_CLUSTER_CONTROL_TIMEOUT seconds one liveness probe / recovery transfer may take (default 20)
  CI_CLUSTER_REAP_GRACE     seconds a killed console gets after TERM before KILL (default 5)
  CI_CLUSTER_LOG_DIR        where per-gate logs go (default a fresh dir under /tmp)

Tiers with an empty machine id are skipped. Exit 0 = all gates green; 1 = failure;
2 = config error or pre-flight refusal.
EOF
}

# --- small helpers -------------------------------------------------------------------------

# md5 of the LOCAL runci.sh. macOS has no md5sum, so fall back to `md5 -q`.
local_md5() {
  if command -v md5sum >/dev/null 2>&1; then
    md5sum "$RUNCI_LOCAL" | awk '{print $1}'
  elif command -v md5 >/dev/null 2>&1; then
    md5 -q "$RUNCI_LOCAL"
  else
    die "neither md5sum nor md5 found — cannot fingerprint $RUNCI_LOCAL"
  fi
}

# Dispatchable gate names, parsed from the LOCAL runci.sh case block. The md5
# pre-flight check then guarantees the worker runs exactly this script, so the
# list can never drift from what is actually dispatched — adding a gate to
# runci.sh's case block makes it dispatchable here with no second edit.
# (`."` wildcards the literal dollars in `case "$GATE" in` — keeps the pattern
# dollar-free so shellcheck's SC2016 has nothing to complain about.)
known_gates() {
  sed -n '/^case ".GATE" in/,/^esac$/p' "$RUNCI_LOCAL" \
    | grep -oE '^[[:space:]]{2}[a-z0-9-]+\)' \
    | tr -d ' )' | sort -u
}

# Resolve REF exactly the way runci.sh does: freshly-fetched origin/<ref> first,
# then ref-as-given (sha/tag). Prints the full sha; dies if unresolvable.
# GIT_TERMINAL_PROMPT=0: an unattended dispatcher must FAIL FAST on a credential
# prompt, not hang on one — the tolerated failure falls back to local refs.
resolve_ref() {
  local ref="$1" target
  GIT_TERMINAL_PROMPT=0 git -C "$REPO_ROOT" fetch --quiet origin >/dev/null 2>&1 \
    || info "git fetch failed — resolving '$ref' from local refs (may be stale)"
  target=$(git -C "$REPO_ROOT" rev-parse --verify --quiet "origin/$ref^{commit}") \
    || target=$(git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref^{commit}") \
    || true
  [ -n "${target:-}" ] || die "cannot resolve ref '$ref' in $REPO_ROOT"
  printf '%s' "$target"
}

# --- fly wrappers --------------------------------------------------------------------------

# A started machine is not immediately SSH-able: poll a trivial remote command
# until the SSH wrapper itself succeeds (for a probe, the wrapper's exit code IS
# the signal — connection failure is the only failure mode of `true`).
probe_until_reachable() { # $1 tier $2 machine-id
  local tier="$1" id="$2" waited=0
  while [ "$waited" -lt "$START_TIMEOUT" ]; do
    if fly ssh console --app "$APP" --machine "$id" -C true >/dev/null 2>&1; then
      info "[$tier] machine $id reachable after ${waited}s"
      return 0
    fi
    sleep "$PROBE_INTERVAL"
    waited=$((waited + PROBE_INTERVAL))
  done
  return 1
}

# Verify the DEPLOYED /cache/runci.sh matches the repo copy. Per-machine deploys
# make a mismatch silent without this check: tiers would run different script
# versions and their verdicts would not be comparable.
verify_md5_on_machine() { # $1 tier $2 machine-id $3 expected-md5
  local tier="$1" id="$2" want="$3" got
  got=$(fly ssh console --app "$APP" --machine "$id" -C 'md5sum /cache/runci.sh' 2>/dev/null \
        | tr -d '\r' | sed -n 's/^\([0-9a-f]\{32\}\) .*/\1/p' | head -1)
  if [ -z "$got" ]; then
    die "[$tier] cannot read md5 of /cache/runci.sh on machine $id — is runci.sh deployed there?"
  fi
  if [ "$got" != "$want" ]; then
    printf '[ci-cluster] ERROR: [%s] /cache/runci.sh on machine %s is md5 %s but the repo copy is %s.\n' \
      "$tier" "$id" "$got" "$want" >&2
    echo "  Tiers would run different script versions — refusing to dispatch. Redeploy first:" >&2
    echo "    fly ssh sftp put deploy/ci-worker/runci.sh /cache/runci.sh --app $APP --machine $id" >&2
    echo "    fly ssh console --app $APP --machine $id -C 'chmod +x /cache/runci.sh'" >&2
    echo "  (sftp, NOT a base64 pipe through the console — the console does not forward stdin.)" >&2
    exit 2
  fi
  info "[$tier] runci.sh md5 ok on machine $id ($want)"
}

# The machine's state as `fly machine status` prints it ("State: started"). NOT
# `--json`: flyctl v0.4.104's `fly machine status` has no such flag, so the old
# `--json | sed` form always read an empty state, and every machine fell into the
# "unknown" arm below, which started it (a no-op on a running machine), recorded
# it as OURS, and stopped it at exit - including machines another session was
# using (2026-10-11: a go-build killed mid-run). Prints nothing if the state
# cannot be read.
machine_state() { # $1 machine-id
  fly machine status "$1" --app "$APP" 2>/dev/null \
    | tr -d '\r' | sed -n 's/^State:[[:space:]]*\([a-z]*\).*/\1/p' | head -1
}

# Ownership token of THIS dispatcher run, written to /tmp/ci-owner on every
# machine this run started. /tmp is destroyed when the machine stops, so a stale
# marker can never outlive its machine. At stop time a machine whose marker names
# a DIFFERENT dispatcher is left alone.
OWNER_TOKEN="ci-cluster:$$:$(hostname -s 2>/dev/null || echo host):$(date +%s)"

write_owner_marker() { # $1 machine-id
  fly ssh console --app "$APP" --machine "$1" -C "sh -c 'echo $OWNER_TOKEN > /tmp/ci-owner'" >/dev/null 2>&1 \
    || warn "could not write the owner marker on machine $1 (it will still be stopped at exit unless a foreign marker or a running gate is found)"
}

# True when the machine carries a readable owner marker that is NOT ours. An
# unreadable machine (unreachable) or a missing marker is not foreign.
marker_is_foreign() { # $1 machine-id
  local got
  got=$(fly ssh console --app "$APP" --machine "$1" -C 'cat /tmp/ci-owner' 2>/dev/null | tr -d '\r' | sed -n '/^ci-cluster:/p' | head -1)
  [ -n "$got" ] && [ "$got" != "$OWNER_TOKEN" ]
}

# True when a runci.sh gate is currently executing on the machine: something
# (another dispatcher, a manual run) is using it, so stopping would kill that run.
machine_is_busy() { # $1 machine-id
  fly ssh console --app "$APP" --machine "$1" -C "pgrep -f '/cache/[r]unci.sh'" >/dev/null 2>&1
}

# Start the machine only if it is STOPPED, and record ownership of exactly those.
# A machine that is already started (or starting) belongs to whoever started it:
# it is used but never claimed, never stopped. A state that cannot be read is a
# pre-flight refusal - never claimed, never touched: guessing wrongly in either
# direction either kills someone's run or leaves a machine billing.
preflight_machine() { # $1 tier $2 machine-id $3 expected-md5
  local tier="$1" id="$2" want="$3" state claimed=""
  state=$(machine_state "$id")
  case "$state" in
    started)
      info "[$tier] machine $id already started - using it, will NOT stop it (not owned by this run)"
      ;;
    starting)
      info "[$tier] machine $id is starting (someone else started it) - waiting for it, will NOT stop it"
      ;;
    stopped)
      info "[$tier] starting machine $id"
      fly machine start "$id" --app "$APP" \
        || die "[$tier] fly machine start $id failed (state was 'stopped')"
      STARTED_IDS+=("$id"); claimed=1
      ;;
    *)
      die "[$tier] cannot read the state of machine $id ('${state:-no answer}') - refusing to touch a machine whose ownership is unknown (check: fly machine status $id --app $APP)"
      ;;
  esac
  probe_until_reachable "$tier" "$id" \
    || die "[$tier] machine $id not SSH-reachable within ${START_TIMEOUT}s - run 'fly machine status $id --app $APP' to inspect"
  [ -n "$claimed" ] && write_owner_marker "$id"
  verify_md5_on_machine "$tier" "$id" "$want"
}

# --- bounded control operations ---------------------------------------------------------------
#
# The watchdog exists because an unbounded wait on the remote session hung a tier
# for ~3h. Its OWN I/O (liveness probes, recovery transfers, reaping the killed
# console) must therefore be bounded too, on clocks separate from the healthy
# gate's runtime: a go-race may run 25+ min, but one control probe may not run
# CONTROL_TIMEOUT seconds, nor may a killed console take more than REAP_GRACE
# seconds (+ a short KILL grace) to be gone.

# run_bounded <secs> <stdout-file> <stderr-file> <cmd...>
# Runs cmd with its output in files (never a pipe the caller could block on) and
# stdin closed. Returns the command's status, or 124 when it exceeded <secs>
# (the command is then TERMed, KILLed and reaped; a child that still cannot be
# reaped is abandoned rather than waited on).
run_bounded() {
  local secs="$1" out="$2" err="$3" pid ticks=0 max
  shift 3
  max=$((secs * 5))
  "$@" >"$out" 2>"$err" </dev/null &
  pid=$!
  while kill -0 "$pid" 2>/dev/null; do
    if [ "$ticks" -ge "$max" ]; then
      terminate_child "$pid" >/dev/null
      return 124
    fi
    sleep 0.2
    ticks=$((ticks + 1))
  done
  wait "$pid" 2>/dev/null
}

# terminate_child <pid>: TERM, wait up to REAP_GRACE s, then KILL and wait up to
# 2 s more. Returns 0 when the child is gone, 1 when it could not be reaped (it
# is then disowned so no later `wait` can block on it).
terminate_child() {
  local pid="$1" i
  kill "$pid" 2>/dev/null
  for ((i = 0; i < REAP_GRACE * 5; i++)); do
    kill -0 "$pid" 2>/dev/null || { wait "$pid" 2>/dev/null; return 0; }
    sleep 0.2
  done
  kill -9 "$pid" 2>/dev/null
  for ((i = 0; i < 10; i++)); do
    kill -0 "$pid" 2>/dev/null || { wait "$pid" 2>/dev/null; return 0; }
    sleep 0.2
  done
  disown "$pid" 2>/dev/null
  return 1
}

# --- attempt identity -------------------------------------------------------------------------
#
# Every dispatched gate invocation gets a unique attempt id (this dispatcher run +
# tier + gate). It travels as runci.sh's third argument, so it is in the remote
# process's argv (the liveness probe matches it) and echoed as an `ATTEMPT:` line
# in the run log (recovered and collected evidence must carry it). An older run of
# the same gate on the same commit therefore can never stand in for this attempt.
attempt_of() { printf '%s.%s.%s' "$RUN_ID" "$1" "$2"; } # $1 tier $2 gate

# True when the log has the exact line `ATTEMPT: <id>`.
log_has_attempt() { # $1 log-file $2 attempt
  [ -f "$1" ] && tr -d '\r' <"$1" | grep -qxF "ATTEMPT: $2"
}

# True when the log carries MORE THAN ONE distinct `ATTEMPT:` identity: two
# invocations wrote into one file, so a verdict line in it cannot be attributed
# to either. Such a log is never evidence for any attempt.
log_mixed_attempts() { # $1 log-file
  [ -f "$1" ] && [ "$(tr -d '\r' <"$1" | sed -n 's/^ATTEMPT: //p' | sort -u | wc -l)" -gt 1 ]
}

# One structured line per watchdog decision; the selfcheck asserts on these.
watch_event() { # $1 tier $2 gate $3 text
  [ -n "$LOG_DIR" ] && printf '%s\n' "$3" >>"$LOG_DIR/watchdog-$1-$2.events"
}

# --- dispatch ------------------------------------------------------------------------------

# Remote liveness of THIS attempt's gate on a machine. Prints exactly one of:
#   alive        a process whose argv carries this attempt's runci.sh invocation
#                (ref + gate + attempt id) exists
#   gone         the probe ran and pgrep cleanly reported NO match (exit 1)
#   unreachable  the probe itself failed: transport error, no result sentinel,
#                or pgrep reporting a usage/fatal error (exit 2/3)
#   timeout      the probe exceeded CONTROL_TIMEOUT (a hung control channel)
# The remote console splits -C into argv WITHOUT a shell, so the probe is wrapped
# in `sh -c` with ONE single-quoted pgrep pattern, and the shell appends a
# `PGREP_RC=<n>` sentinel: the sentinel — not flyctl's exit status or stderr —
# is what separates a clean no-match from a failed probe. The pattern is
# attempt-specific, so a co-tenant's runci.sh (even the same gate and ref) can
# never make a dead gate look alive; the [r]unci bracket keeps pgrep from
# matching the probe's own command line.
remote_gate_state() { # $1 machine-id $2 tier $3 gate
  local id="$1" tier="$2" gate="$3" attempt pat cmd outf errf rc sentinel
  attempt=$(attempt_of "$tier" "$gate")
  local part
  for part in "$REF" "$gate" "$attempt"; do
    case "$part" in
      ''|*[!A-Za-z0-9._/@-]*) printf 'unreachable'; return 0 ;;   # cannot be quoted safely
    esac
  done
  pat="/cache/[r]unci[.]sh ${REF//./[.]} ${gate//./[.]} ${attempt//./[.]}"
  cmd="sh -c \"pgrep -f '$pat'; echo PGREP_RC=\$?\""
  outf="$LOG_DIR/probe-$tier-$gate.out"
  errf="$LOG_DIR/probe-$tier-$gate.err"
  run_bounded "$CONTROL_TIMEOUT" "$outf" "$errf" \
    fly ssh console --app "$APP" --machine "$id" -C "$cmd"
  rc=$?
  if [ "$rc" -eq 124 ]; then printf 'timeout'; return 0; fi
  sentinel=$(tr -d '\r' <"$outf" 2>/dev/null | sed -n 's/^PGREP_RC=\([0-9][0-9]*\)$/\1/p' | tail -1)
  case "$sentinel" in
    0) if tr -d '\r' <"$outf" | grep -qE '^[0-9]+$'; then printf 'alive'; else printf 'unreachable'; fi ;;
    1) printf 'gone' ;;
    *) printf 'unreachable' ;;
  esac
}

# End a gate that has no verdict in its console capture. Three entry shapes:
#   reason="watchdog:gone"        console still open, remote runci.sh gone for
#                                 $DEAD_STRIKES probes — the 2026-10-18 half-open
#                                 hang (lint@d52b70bdc: remote died 13:59:26Z
#                                 mid-write, local console blocked for ~3h)
#   reason="watchdog:unreachable|timeout"  machine/control channel unusable
#   reason="exited:$rc"           the console exited by itself and the capture
#                                 has no RESULT (session dropped at the very end)
# Recovery FIRST: the console transport can die while the REMOTE run survives
# and completes — runci.sh tee's its whole run to /cache/logs (RUNLOG), and
# that file then holds the verdict the console never delivered. Recovery trusts
# ONLY the RUNLOG path THIS attempt announced in its own console capture, and
# only a copy that carries this attempt's `ATTEMPT:` line, this run's HEAD and a
# verdict. No announcement, a failed or timed-out transfer, or evidence of any
# other attempt (including an older green run of the same gate and commit) means
# no recovery: the log gets an explicit CI-CLUSTER: STALLED/DIED marker naming
# the gate, the machine, the reason and the recovery outcome, and the tier fails.
# Never silent; never a hang; never an old run's PASS.
handle_incomplete_gate() { # $1 tier $2 machine-id $3 gate $4 log $5 reason
  local tier="$1" id="$2" gate="$3" log="$4" reason="$5" remote rec last attempt why sha rc
  attempt=$(attempt_of "$tier" "$gate")
  grep -qE '^(ALL GATES GREEN|GATE FAILURE\(S\))' "$log" 2>/dev/null && return 0
  remote=$(remote_runlog_for_gate "$tier" "$gate")
  if [ -z "$remote" ]; then
    why="no RUNLOG announced by this attempt ($attempt); nothing to recover"
  else
    rec="$LOG_DIR/$tier-$gate.recovered.log"
    rm -f "$rec"
    run_bounded "$CONTROL_TIMEOUT" /dev/null "$LOG_DIR/recover-$tier-$gate.err" \
      fly ssh sftp get "$remote" "$rec" --app "$APP" --machine "$id"
    rc=$?
    sha=$(head_sha_of_log "$rec")
    if [ "$rc" -eq 124 ]; then
      why="recovery transfer of $remote timed out after ${CONTROL_TIMEOUT}s"
    elif [ "$rc" -ne 0 ] || [ ! -s "$rec" ]; then
      why="recovery transfer of $remote failed (exit $rc)"
    elif ! log_has_attempt "$rec" "$attempt"; then
      why="$remote does not carry this attempt's id $attempt (older or foreign run log; not adopted)"
    elif log_mixed_attempts "$rec"; then
      why="$remote carries more than one attempt identity (two runs wrote one file); no verdict in it can be attributed to $attempt, not adopted"
    elif ! head_matches "$sha" "$EXPECTED_FULL"; then
      why="$remote reports HEAD '${sha:-none}', expected ${EXPECTED_SHORT}; not adopted"
    elif ! grep -qE '^(ALL GATES GREEN|GATE FAILURE\(S\))' "$rec" 2>/dev/null; then
      why="$remote is this attempt's log but holds no verdict (the run did not finish)"
    else
      # The recovered RUNLOG becomes the gate's verdict — but the original
      # console capture stays part of the record: it carries the `RUNLOG:`
      # line the collection phase reads, plus whatever output reached us
      # before the transport died. The capture by definition has no verdict.
      mv "$log" "$log.console-capture"
      {
        echo "CI-CLUSTER: console transport dropped before the verdict reached us; verdict RECOVERED from the remote run log $remote (attempt $attempt)"
        cat "$rec"
        echo ""
        echo "CI-CLUSTER: the original console capture (no verdict reached us over the transport) is preserved in $log.console-capture"
      } >"$log"
      rm -f "$rec"
      info "[$tier] gate $gate on machine $id: console dropped ($reason) but the remote run COMPLETED — verdict recovered from $remote"
      return 0
    fi
    rm -f "$rec"
  fi
  # No verdict anywhere: say STALLED/DIED, loudly, with the tail for triage —
  # UNLESS the log already carries runci.sh's KILLED-by-signal trailer, which
  # already states truthfully why there is no verdict (the run died on a
  # signal); stamping STALLED/DIED over it would claim something the log
  # contradicts. Either way the tier fails.
  last=$(tail -n 1 "$log" 2>/dev/null | cut -c 1-200)
  if grep -q 'RUNCI: KILLED by signal' "$log" 2>/dev/null; then
    printf '[%s] ERROR: gate %s on machine %s was KILLED by a signal (trailer in the log) — no verdict exists; treating the tier as failed (%s)\n' \
      "$tier" "$gate" "$id" "$reason" >&2
    return 1
  fi
  {
    echo ""
    echo "CI-CLUSTER: STALLED/DIED — gate $gate on machine $id: $reason; no RESULT in the console capture and no recoverable verdict (recovery: $why). Last log line: ${last:-<empty>}"
  } >>"$log"
  printf '[%s] ERROR: STALLED/DIED — gate %s on machine %s: %s; no RESULT in the capture and none recoverable (%s). Last line: %s\n' \
    "$tier" "$gate" "$id" "$reason" "$why" "${last:-<empty>}" >&2
  return 1
}

# Runs in a BACKGROUND SUBSHELL (one per tier): it writes only to log files and
# stdout, never to parent state. Gates run sequentially — runci.sh holds a
# whole-run flock on the machine's /tmp/runci.lock, so a second concurrent
# invocation would queue for up to 90 minutes, not run in parallel.
# All gates in a tier run even after one fails (runci.sh `all` behaves the same);
# the verdict comes from each log, never from the SSH wrapper's exit code.
#
# Each gate's console runs in the BACKGROUND of this subshell under a liveness
# watchdog (2026-10-19 — was a bare synchronous call that hung for ~3h with no
# verdict when the remote session died without closing the SSH flow: the
# remote runci.sh was gone at 13:59:26Z, the local console blocked on a
# half-open socket forever). While the console runs, the subshell probes
# remote_gate_state every WATCH_INTERVAL: 'alive' resets the strike count, so
# a healthy gate of ANY duration (go-race ~25 min) is never touched; after
# DEAD_STRIKES consecutive not-alive probes the console is terminated (bounded:
# TERM, KILL, abandon — never an unbounded wait) and handle_incomplete_gate
# either recovers this attempt's verdict from its announced RUNLOG or ends the
# tier with the explicit STALLED/DIED failure. Every probe and the termination
# are recorded in $LOG_DIR/watchdog-<tier>-<gate>.events.
dispatch_tier() { # $1 tier $2 machine-id $3 gates(space-separated)
  local tier="$1" id="$2" gates="$3" gate log rc con state strikes watched attempt tier_rc=0 reason
  for gate in $gates; do
    log="$LOG_DIR/$tier-$gate.log"
    attempt=$(attempt_of "$tier" "$gate")
    : >"$LOG_DIR/watchdog-$tier-$gate.events"
    printf '[%s] dispatching gate %-18s -> machine %s (attempt %s)\n' "$tier" "$gate" "$id" "$attempt"
    fly ssh console --app "$APP" --machine "$id" -C "/cache/runci.sh $REF $gate $attempt" >"$log" 2>&1 &
    con=$!
    strikes=0
    watched=0
    # Two clocks on purpose (the first cut slept the full WATCH_INTERVAL per
    # gate because a console that is still STARTING is genuinely alive at the
    # first check):
    #   * the wait hops in 1s steps, so a fast gate (fake fly, small gate) costs
    #     ~1s here, never a full interval;
    #   * the remote liveness probe and the verdict re-check run only once per
    #     WATCH_INTERVAL — probe cadence is what DEAD_STRIKES multiplies, and a
    #     healthy go-race (~25 min) probes 'alive' on every cycle and is never
    #     killed.
    while kill -0 "$con" 2>/dev/null; do
      grep -qE '^(ALL GATES GREEN|GATE FAILURE\(S\))' "$log" 2>/dev/null && break
      sleep 1
      kill -0 "$con" 2>/dev/null || break            # finished during the hop
      watched=$((watched + 1))
      [ "$watched" -lt "$WATCH_INTERVAL" ] && continue
      watched=0
      grep -qE '^(ALL GATES GREEN|GATE FAILURE\(S\))' "$log" 2>/dev/null && break
      state=$(remote_gate_state "$id" "$tier" "$gate")
      watch_event "$tier" "$gate" "probe $state"
      if [ "$state" = "alive" ]; then
        strikes=0
        continue
      fi
      strikes=$((strikes + 1))
      info "[$tier] gate $gate: remote gate state '$state' ($strikes/$DEAD_STRIKES strikes)"
      if [ "$strikes" -ge "$DEAD_STRIKES" ]; then
        reason="watchdog:$state (no verdict, remote not alive for $DEAD_STRIKES probes)"
        [ "$state" = "timeout" ] && reason="watchdog:timeout (no verdict; the control probe exceeded ${CONTROL_TIMEOUT}s on $DEAD_STRIKES consecutive probes)"
        watch_event "$tier" "$gate" "terminate console"
        if terminate_child "$con"; then
          watch_event "$tier" "$gate" "console reaped"
        else
          watch_event "$tier" "$gate" "console abandoned"
          reason="$reason; the console did not terminate after TERM+KILL and was abandoned"
        fi
        con=""    # handled by the watchdog — the plain path below must not wait again
        if handle_incomplete_gate "$tier" "$id" "$gate" "$log" "$reason"; then
          break   # verdict recovered — continue with the next gate
        fi
        tier_rc=1
        break 2   # the machine is in no state to run the remaining gates — stop here
      fi
    done
    # The console may outlive its own verdict (printed ALL GATES GREEN, then the
    # session hung) — the verdict is in hand, so reap the corpse, bounded.
    rc=0
    if [ -n "$con" ]; then
      if grep -qE '^(ALL GATES GREEN|GATE FAILURE\(S\))' "$log" 2>/dev/null; then
        kill -0 "$con" 2>/dev/null && { terminate_child "$con" || warn "[$tier] console for gate $gate outlived its verdict and could not be reaped; abandoned"; }
      else
        wait "$con" 2>/dev/null
        rc=$?
      fi
    fi
    printf '[%s] gate %s finished on machine %s (log: %s)\n' "$tier" "$gate" "$id" "$log"
    # A gate with no RESULT line did not complete: the machine was stopped under
    # it, or the SSH session dropped. Recover the verdict from the remote RUNLOG
    # if the run actually finished, else fail the tier with STALLED/DIED — never
    # "exited 0", and the next gates would only fail on the same dead machine.
    if ! grep -qE '^(ALL GATES GREEN|GATE FAILURE\(S\))' "$log" 2>/dev/null \
       && ! grep -q 'unknown gate' "$log" 2>/dev/null; then
      if handle_incomplete_gate "$tier" "$id" "$gate" "$log" "console exited $rc with no verdict (session dropped or machine stopped)"; then
        continue
      fi
      tier_rc=1
      break
    fi
  done
  printf '[%s] tier complete: %s\n' "$tier" "$gates"
  return "$tier_rc"
}

# --- verdict collection --------------------------------------------------------------------

# runci.sh's own words are the only truth (wrapper exit codes lie green).
#   PASS                  log contains the final "ALL GATES GREEN"
#   FAIL:gate             log contains "GATE FAILURE(S)"
#   FAIL:real             log contains "REAL FAILURE" (dropped before RESULT — still a failure)
#   FAIL:unknown-gate     deployed runci.sh rejected the gate name
#   FAIL:no-log           the fly invocation never produced a log
#   FAIL:stalled          the watchdog found the remote gate gone with no verdict anywhere
#                         (2026-10-19: replaces the old silent ~3h hang on a dead session)
#   FAIL:killed-by-signal runci.sh died on a signal and wrote its KILLED trailer —
#                         no RESULT exists because the run never reached a verdict
#   FAIL:no-result-line   SSH dropped mid-run (or the run was interrupted) — incomplete
verdict_of_log() { # $1 log-file
  local log="$1"
  [ -f "$log" ] || { printf 'FAIL:no-log'; return; }
  if grep -q '^ALL GATES GREEN$' "$log"; then printf 'PASS'; return; fi
  if grep -q 'GATE FAILURE(S)' "$log"; then printf 'FAIL:gate'; return; fi
  if grep -q 'REAL FAILURE' "$log"; then printf 'FAIL:real'; return; fi
  if grep -q 'unknown gate' "$log"; then printf 'FAIL:unknown-gate'; return; fi
  if grep -q 'RUNCI: KILLED by signal' "$log"; then printf 'FAIL:killed-by-signal'; return; fi
  if grep -q 'CI-CLUSTER: STALLED/DIED' "$log"; then printf 'FAIL:stalled'; return; fi
  printf 'FAIL:no-result-line'
}

# The `HEAD: <sha> <subject>` line runci.sh prints after checkout.
head_sha_of_log() { # $1 log-file
  [ -f "$1" ] || return 0
  sed -n 's/^HEAD: \([0-9a-f][0-9a-f]*\) .*/\1/p' "$1" | head -1
}

# A worker's short sha must be a prefix of the locally-resolved full sha. All
# workers reporting the expected sha ⇒ they all match each other; a mismatch
# means some tier tested a different commit — a silent, convincing wrong answer.
head_matches() { # $1 reported-short-sha $2 expected-full-sha
  [ -n "$1" ] || return 1
  case "$2" in
    "$1"*) return 0 ;;
    *)     return 1 ;;
  esac
}

# --- evidence collection (BEFORE any stop) --------------------------------------------------
#
# /tmp dies with the machine. On 2026-09-16 three machines were stopped with
# real defects (4 data races, 10 SPA failures, 1 e2e shard) still only in
# their /tmp logs — every trace destroyed unread. runci.sh now tee's each run
# to /cache/logs/<gate>@<ref>-<stamp>.log (persistent), and this phase copies
# those files off BEFORE anything is stopped. `fly ssh sftp get` because the
# console cannot transfer files (it does not forward stdin). Every copy is
# VERIFIED: non-zero size AND the run's `HEAD:` line — a zero-byte or
# HEAD-less file looks like evidence and is not.

# Remote run-log path THIS attempt announced. Reads only the machine-readable
# `RUNLOG: <path>` line runci.sh prints as its first output into this attempt's
# own console capture (the capture is truncated per dispatch, so the line cannot
# come from an earlier run). There is deliberately NO fallback to "the newest log
# of this gate on the machine": that selected an earlier run of the same gate and
# commit and could certify it as the current attempt. Prints nothing when this
# attempt announced no path.
remote_runlog_for_gate() { # $1 tier $2 gate
  sed -n 's/^RUNLOG: \(\/cache\/logs\/.*\)$/\1/p' "$LOG_DIR/$1-$2.log" 2>/dev/null | tr -d '\r' | head -1
}

# Copy one machine's dispatched-gate run logs off the machine and VERIFY each
# one (non-zero size, HEAD sha = this run's ref). Gates with no local console
# log were never dispatched — nothing to collect, not a failure. Returns
# non-zero if any dispatched gate's evidence is not verified local.
collect_machine_logs() { # $1 tier $2 machine-id $3 gates(space-separated)
  local tier="$1" id="$2" gates="$3" gate remote dest sha rc=0 attempt xrc
  for gate in $gates; do
    [ -f "$LOG_DIR/$tier-$gate.log" ] || continue
    dest="$LOG_DIR/worker/$tier-$gate.log"
    attempt=$(attempt_of "$tier" "$gate")
    remote=$(remote_runlog_for_gate "$tier" "$gate")
    if [ -z "$remote" ]; then
      warn "[$tier] evidence NOT collected: gate $gate on machine $id announced no run log for attempt $attempt (an older log of the same gate is never substituted)"
      rc=1
      continue
    fi
    rm -f "$dest"
    run_bounded "$CONTROL_TIMEOUT" /dev/null "$LOG_DIR/collect-$tier-$gate.err" \
      fly ssh sftp get "$remote" "$dest" --app "$APP" --machine "$id"
    xrc=$?
    if [ "$xrc" -ne 0 ] || [ ! -s "$dest" ]; then
      warn "[$tier] evidence NOT collected: gate $gate log on machine $id could not be copied non-empty ($remote; transfer exit $xrc$([ "$xrc" -eq 124 ] && echo ", timed out"))"
      rc=1
      continue
    fi
    if ! log_has_attempt "$dest" "$attempt"; then
      warn "[$tier] evidence NOT verified: gate $gate log copied from machine $id does not carry this attempt's id $attempt — another run's file, not this one"
      rc=1
      continue
    fi
    if log_mixed_attempts "$dest"; then
      warn "[$tier] evidence NOT verified: gate $gate log copied from machine $id carries more than one attempt identity (two runs wrote one file) - no verdict in it can be attributed to attempt $attempt"
      rc=1
      continue
    fi
    sha=$(head_sha_of_log "$dest")
    if [ -z "$sha" ] || ! head_matches "$sha" "$EXPECTED_FULL"; then
      warn "[$tier] evidence NOT verified: gate $gate log copied from machine $id has HEAD '${sha:-none}', expected ${EXPECTED_SHORT} — possibly another run's file, not this one"
      rc=1
      continue
    fi
    info "[$tier] collected + verified $dest (from machine $id: $remote)"
  done
  return "$rc"
}

# Collection phase over EVERY machine this run used. Verified machines are
# recorded in VERIFIED_IDS — stop_started_machines only stops those. Safe to
# call twice (main's happy path and, if interrupted first, the EXIT trap): the
# second run just re-verifies.
collect_all_and_verify() {
  local i rc=0
  COLLECTION_DONE=1
  [ -n "$LOG_DIR" ] || return 0
  mkdir -p "$LOG_DIR/worker" || warn "cannot create $LOG_DIR/worker — worker copies will fail (machines will be left running)"
  for i in "${!PLAN_MACHINES[@]}"; do
    if collect_machine_logs "${PLAN_TIERS[$i]}" "${PLAN_MACHINES[$i]}" "${PLAN_GATES[$i]}"; then
      case " $VERIFIED_IDS " in
        *" ${PLAN_MACHINES[$i]} "*) ;;
        *) VERIFIED_IDS="$VERIFIED_IDS ${PLAN_MACHINES[$i]}" ;;
      esac
    else
      rc=1
    fi
  done
  return "$rc"
}

# --- teardown ------------------------------------------------------------------------------

stop_started_machines() {
  if [ "${#STARTED_IDS[@]}" -eq 0 ]; then
    info "no machines were started by this run — nothing to stop"
    return 0
  fi
  local id p dispatched
  for id in "${STARTED_IDS[@]}"; do
    # Collect-before-stop (2026-09-16 incident): a machine this run DISPATCHED
    # gates on is stopped only once its logs are verified local. A machine that
    # was started but never dispatched to (pre-flight refusal) carries no
    # evidence — stopping it destroys nothing and saves the billing.
    dispatched=""
    for p in ${PLAN_MACHINES[@]+"${PLAN_MACHINES[@]}"}; do
      [ "$p" = "$id" ] && dispatched=1
    done
    if [ -n "$dispatched" ]; then
      case " $VERIFIED_IDS " in
        *" $id "*)
          info "stopping machine $id (started by this run; evidence verified local)" ;;
        *)
          warn "machine $id LEFT RUNNING — this run dispatched gates on it and their logs are NOT verified local."
          warn "Stopping it would risk destroying the only evidence (2026-09-16: a night of failure traces was lost exactly this way)."
          warn "Collect by hand, then stop it — a running machine costs money; destroyed evidence costs a night:"
          echo "  fly ssh console --app $APP --machine $id -C 'ls -1t /cache/logs/'" >&2
          echo "  fly ssh sftp get /cache/logs/<file> <local-copy> --app $APP --machine $id" >&2
          echo "  fly machine stop $id --app $APP   # once the evidence is safe" >&2
          continue
          ;;
      esac
    else
      info "stopping machine $id (started by this run; nothing was dispatched on it)"
    fi
    if marker_is_foreign "$id"; then
      warn "machine $id carries another dispatcher's owner marker - NOT stopping it"
      continue
    fi
    if machine_is_busy "$id"; then
      warn "machine $id is running a gate (a runci.sh is active on it) - NOT stopping it; stop it by hand when idle: fly machine stop $id --app $APP"
      continue
    fi
    if ! fly machine stop "$id" --app "$APP"; then
      warn "could not stop machine $id — STOP IT MANUALLY, it is billing:"
      echo "  fly machine stop $id --app $APP" >&2
    fi
  done
}

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  # Best-effort: kill the tier subshells and their direct fly children. The real
  # backstop is the machine stop below — halting the VM cuts every SSH session
  # and with it the remote runci.sh run. (macOS has no setsid; pkill -P exists.)
  if [ "${#TIER_PIDS[@]}" -gt 0 ]; then
    local pid
    kill "${TIER_PIDS[@]}" 2>/dev/null || true
    for pid in "${TIER_PIDS[@]}"; do
      pkill -TERM -P "$pid" 2>/dev/null || true
    done
  fi
  # Collect-then-stop, not stop-never: if the run never reached the collection
  # phase (interrupt, or a death after dispatch began), collect best-effort
  # NOW, then stop whatever verified. Do not weaken or remove this trap.
  if [ "$COLLECTION_DONE" -ne 1 ]; then
    info "exit before the collection phase — collecting evidence best-effort before any stop"
    collect_all_and_verify || true
  fi
  stop_started_machines
  exit "$rc"
}

# --- tier-map loading, validation, dispatch, summary ---------------------------------------
#
# These helpers fill/mutate main's TIER_* locals through bash dynamic scope —
# the same deliberate pattern runci.sh's _e2e_reap_one documents. They are the
# reason main stays under the 120-line function ceiling.

load_tier_map() { # fills TIER_NAMES/TIER_MACHINES/TIER_GATES from CI_CLUSTER_TIERS or the default map
  local raw line name rest machine gates
  raw="${CI_CLUSTER_TIERS:-$(default_tier_map)}"
  while IFS= read -r line; do
    case "$line" in ''|'#'*) continue ;; esac
    name=${line%%|*}; rest=${line#*|}
    machine=${rest%%|*}; gates=${rest#*|}
    if [ -z "$machine" ]; then
      info "tier '$name': no machine id — skipping (not provisioned)"
      continue
    fi
    TIER_NAMES+=("$name"); TIER_MACHINES+=("$machine"); TIER_GATES+=("$gates")
    ALL_NAMES+=("$name")
  done <<<"$raw"
  [ "${#TIER_NAMES[@]}" -gt 0 ] || die "no tier has a machine id — set CI_CLUSTER_*_MACHINE or CI_CLUSTER_TIERS"
}

filter_tiers() { # $@ = tier names to keep; dies naming the configured tiers on an unknown one
  local -a WANT=("$@") KEEP_NAMES=() KEEP_MACHINES=() KEEP_GATES=()
  local i w found
  for w in "${WANT[@]}"; do
    found=""
    for i in "${ALL_NAMES[@]}"; do [ "$i" = "$w" ] && found=1; done
    [ -n "$found" ] || die "no configured tier named '$w' (configured: ${ALL_NAMES[*]})"
  done
  for i in "${!TIER_NAMES[@]}"; do
    for w in "${WANT[@]}"; do
      [ "${TIER_NAMES[$i]}" = "$w" ] || continue
      KEEP_NAMES+=("${TIER_NAMES[$i]}"); KEEP_MACHINES+=("${TIER_MACHINES[$i]}"); KEEP_GATES+=("${TIER_GATES[$i]}")
    done
  done
  TIER_NAMES=("${KEEP_NAMES[@]+"${KEEP_NAMES[@]}"}")
  TIER_MACHINES=("${KEEP_MACHINES[@]+"${KEEP_MACHINES[@]}"}")
  TIER_GATES=("${KEEP_GATES[@]+"${KEEP_GATES[@]}"}")
}

validate_tiers() { # one tier per machine; every gate exists in runci.sh's case block; `all` refused
  local a b gate ok
  # One machine, one tier: two tiers on one machine would serialise on the
  # machine's runci.lock and share one checkout — and defeat the point.
  for a in "${!TIER_MACHINES[@]}"; do
    for b in "${!TIER_MACHINES[@]}"; do
      if [ "$a" -lt "$b" ] && [ "${TIER_MACHINES[$a]}" = "${TIER_MACHINES[$b]}" ]; then
        die "tiers '${TIER_NAMES[$a]}' and '${TIER_NAMES[$b]}' share machine ${TIER_MACHINES[$a]} — one tier per machine"
      fi
    done
  done
  # Gate names must exist in THIS repo's runci.sh case block. `all` is refused:
  # it would run every gate serially on one machine, which is exactly what the
  # cluster exists to avoid.
  local -a KNOWN=()
  while IFS= read -r gate; do [ -n "$gate" ] && KNOWN+=("$gate"); done < <(known_gates)
  [ "${#KNOWN[@]}" -gt 0 ] || die "parsed no gates from $RUNCI_LOCAL's case block — runci.sh layout changed?"
  for a in "${!TIER_NAMES[@]}"; do
    for gate in ${TIER_GATES[$a]}; do
      if [ "$gate" = "all" ]; then
        die "tier '${TIER_NAMES[$a]}' uses gate 'all' — split it across tiers instead; 'all' serialises everything on one machine"
      fi
      ok=""
      for b in "${KNOWN[@]}"; do [ "$b" = "$gate" ] && ok=1; done
      [ -n "$ok" ] || die "tier '${TIER_NAMES[$a]}' names gate '$gate', which $RUNCI_LOCAL's case block does not define"
    done
  done
}

write_run_header() { # $@ = tier filters; records what this run dispatched, for post-mortem
  local i
  {
    echo "date:    $(date -u 2>/dev/null || date)"
    echo "app:     $APP"
    echo "ref:     $REF ($EXPECTED_FULL)"
    echo "runci:   md5 $LOCAL_MD5"
    echo "argv:    $0 $REF $*"
    for i in "${!TIER_NAMES[@]}"; do
      echo "tier:    ${TIER_NAMES[$i]} | machine ${TIER_MACHINES[$i]} | gates ${TIER_GATES[$i]}"
    done
  } >"$LOG_DIR/run.txt"
}

dispatch_all_and_wait() { # one tier per machine concurrently; gates sequential within a tier
  local a pid rc
  # Publish the plan to globals BEFORE dispatching: the EXIT trap's collection
  # phase runs after main has returned, where main's TIER_* locals no longer
  # exist, and it must know which machines this run dispatched gates on.
  PLAN_TIERS=("${TIER_NAMES[@]}")
  PLAN_MACHINES=("${TIER_MACHINES[@]}")
  PLAN_GATES=("${TIER_GATES[@]}")
  for a in "${!TIER_NAMES[@]}"; do
    dispatch_tier "${TIER_NAMES[$a]}" "${TIER_MACHINES[$a]}" "${TIER_GATES[$a]}" &
    pid=$!
    TIER_PIDS+=("$pid"); PID_TIERS+=("${TIER_NAMES[$a]}")
  done
  for a in "${!TIER_PIDS[@]}"; do
    wait "${TIER_PIDS[$a]}"; rc=$?
    info "tier ${PID_TIERS[$a]} job exited $rc"
  done
}

summarize_run() { # verdicts + HEAD-sha assertion from the logs; returns 0 only if all PASSed
  local overall=0 a gate log verdict sha p left=""
  printf '\n=== ci-cluster summary — app %s, ref %s @ %s ===\n' "$APP" "$REF" "$EXPECTED_SHORT"
  printf '%-8s %-14s %-20s %s\n' TIER MACHINE GATE VERDICT
  for a in "${!TIER_NAMES[@]}"; do
    for gate in ${TIER_GATES[$a]}; do
      log="$LOG_DIR/${TIER_NAMES[$a]}-$gate.log"
      verdict=$(verdict_of_log "$log")
      if [ "$verdict" = "PASS" ]; then
        sha=$(head_sha_of_log "$log")
        if ! head_matches "$sha" "$EXPECTED_FULL"; then
          verdict="FAIL:head-mismatch(${sha:-no HEAD line})"
        fi
      fi
      printf '%-8s %-14s %-20s %s\n' "${TIER_NAMES[$a]}" "${TIER_MACHINES[$a]}" "$gate" "$verdict"
      [ "$verdict" = "PASS" ] || overall=1
      if [ "$verdict" = "FAIL:stalled" ]; then
        warn "gate ${TIER_NAMES[$a]}/$gate STALLED/DIED: the remote gate process disappeared without a verdict (see the CI-CLUSTER: STALLED/DIED line in the log for the last output and the machine) — root causes seen so far: console session torn down remotely (2026-10-18 lint@d52b70bdc), machine stopped under the run"
      fi
      if grep -q 'another runci.sh is already running' "$log" 2>/dev/null; then
        warn "tier ${TIER_NAMES[$a]} gate $gate QUEUED behind another run on that machine — verdict may reflect contention"
      fi
    done
  done
  # Loud, in the summary, per the collect-before-stop contract: these machines
  # are still running because their evidence could not be verified local.
  for p in ${PLAN_MACHINES[@]+"${PLAN_MACHINES[@]}"}; do
    case " $VERIFIED_IDS " in
      *" $p "*) ;;
      *) left="$left $p" ;;
    esac
  done
  if [ -n "$left" ]; then
    warn "LEFT RUNNING (this run's evidence not verified local — collect by hand before stopping):$left"
    echo "  per machine: fly ssh console --app $APP --machine <id> -C 'ls -1t /cache/logs/'" >&2
    echo "              fly ssh sftp get /cache/logs/<file> <local-copy> --app $APP --machine <id>" >&2
  fi
  printf 'logs: %s (worker copies of the run logs under worker/)\n' "$LOG_DIR"
  return "$overall"
}

# --- main ----------------------------------------------------------------------------------

main() {
  # EXPECTED_FULL/EXPECTED_SHORT are deliberately NOT local: the EXIT trap's
  # collection phase verifies HEAD shas after main has returned and needs them.
  local REF LOCAL_MD5 overall COLLECT_RC
  local -a TIER_NAMES=() TIER_MACHINES=() TIER_GATES=() ALL_NAMES=()

  [ $# -ge 1 ] || { usage >&2; exit 2; }
  case "$1" in
    -h|--help) usage; exit 0 ;;
  esac
  REF="$1"; shift

  command -v fly >/dev/null 2>&1 || die "fly not on PATH — install flyctl first"
  [ -f "$RUNCI_LOCAL" ] || die "$RUNCI_LOCAL not found — run from a checkout of the omnipus repo"

  EXPECTED_FULL=$(resolve_ref "$REF")
  EXPECTED_SHORT=$(git -C "$REPO_ROOT" rev-parse --short "$EXPECTED_FULL")
  LOCAL_MD5=$(local_md5)
  info "app=$APP ref=$REF expected=$EXPECTED_SHORT ($EXPECTED_FULL) runci.md5=$LOCAL_MD5"

  # ---- load + validate the tier map (helpers above mutate main's locals) ----
  load_tier_map
  if [ $# -gt 0 ]; then filter_tiers "$@"; fi
  validate_tiers

  # ---- log dir ----
  LOG_DIR="${CI_CLUSTER_LOG_DIR:-${TMPDIR:-/tmp}/omnipus-ci-cluster/run-$(date +%Y%m%d-%H%M%S)}"
  mkdir -p "$LOG_DIR" || die "cannot create log dir $LOG_DIR"
  write_run_header "$@"
  info "logs: $LOG_DIR"

  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  # ---- pre-flight: start, probe, verify md5 — on EVERY machine before ANY dispatch ----
  local a
  for a in "${!TIER_NAMES[@]}"; do
    preflight_machine "${TIER_NAMES[$a]}" "${TIER_MACHINES[$a]}" "$LOCAL_MD5"
  done

  # ---- dispatch concurrently, COLLECT the evidence, then verdicts, summarize ----
  dispatch_all_and_wait
  collect_all_and_verify; COLLECT_RC=$?
  summarize_run; overall=$?
  if [ "$COLLECT_RC" -ne 0 ]; then
    overall=1
    warn "OVERALL: FAIL — at least one machine's evidence is NOT secured local; that machine is LEFT RUNNING (see LEFT RUNNING above for ids and hand-retrieval commands)"
  fi
  if [ "$overall" -eq 0 ]; then
    info "OVERALL: PASS — all dispatched gates green, all evidence collected+verified"
  else
    warn "OVERALL: FAIL — see verdicts above (FAIL:no-result-line means the log is incomplete: SSH drop or interruption)"
  fi
  # `return`, not `exit`: the script's status flows from the `main "$@"` call at
  # the bottom, and the EXIT trap (machine teardown) still fires on script exit.
  # An `exit` here is also the one construct that makes shellcheck's SC2329 lose
  # track of trap-invoked functions — verified by bisection on this very file.
  return "$overall"
}

main "$@"
