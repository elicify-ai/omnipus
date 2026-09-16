#!/usr/bin/env bash
# ci-cluster.sh — fan CI gates out across the Fly worker cluster and scale to zero.
#
# ONE app (default ci-omnipus-1), several machines: each machine is a TIER with its
# own /cache volume and its own /tmp/runci.lock, addressed by machine id:
#
#   fly ssh console --app ci-omnipus-1 --machine <id> -C "/cache/runci.sh <ref> <gate>"
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
#   4. PARSE every log for runci.sh's RESULT markers — the SSH wrapper's exit code
#      is NOT the gate's (wrapper-exit-code false-green, CLAUDE.md trap 3).
#   5. ASSERT every worker printed the same `HEAD:` sha and that it is the ref
#      requested (stale-checkout false-red, CLAUDE.md trap 1).
#   6. STOP every machine this run started — normal exit, gate failure, AND Ctrl-C
#      (EXIT trap). A dispatcher that leaves 8-CPU machines running on an error is
#      worse than no dispatcher. Machines that were ALREADY running when we arrived
#      are never stopped: another session may own them.
#
# Usage:
#   deploy/ci-worker/ci-cluster.sh <git-ref> [tier ...]
#     <git-ref>   branch, tag, or sha — resolved the same way runci.sh resolves it
#     [tier ...]  restrict the run to these tiers (default: every configured tier
#                 whose machine id is present)
#
# Exit codes:
#   0  every dispatched gate PASSed (and HEAD shas all matched)
#   1  any gate failed, any log has no RESULT line (incomplete), or a HEAD mismatch
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

# CI_CLUSTER_TIERS overrides the whole map. One tier per line:
#   <name>|<machine-id>|<gate> <gate> ...
# The default map takes machine ids from per-tier env vars because ids are NOT
# known when this script is written — the harness provisions the machines and
# injects their ids. An empty machine id means "tier not provisioned" → skipped.
default_tier_map() {
  printf '%s\n' \
    "go|${CI_CLUSTER_GO_MACHINE:-}|gofmt go-build go-vet lint go-test go-race" \
    "node|${CI_CLUSTER_NODE_MACHINE:-}|contracts spa" \
    "xplat|${CI_CLUSTER_XPLAT_MACHINE:-}|embed-build records-no-sqlite cli-verb-guard"
}

# --- run state -----------------------------------------------------------------------------
STARTED_IDS=()   # machine ids THIS run started (and only those — stopped on exit)
TIER_PIDS=()     # background tier jobs
PID_TIERS=()     # pid → tier name, for progress messages
LOG_DIR=""

info() { printf '[ci-cluster] %s\n' "$*"; }
warn() { printf '[ci-cluster] WARNING: %s\n' "$*" >&2; }
die()  { printf '[ci-cluster] ERROR: %s\n' "$*" >&2; exit 2; }

usage() {
  cat <<'EOF'
ci-cluster.sh — fan CI gates across the Fly worker cluster, scale to zero.

Usage: deploy/ci-worker/ci-cluster.sh <git-ref> [tier ...]

Env:
  CI_CLUSTER_APP            Fly app name (default ci-omnipus-1)
  CI_CLUSTER_GO_MACHINE     machine id of the `go` tier   (default map)
  CI_CLUSTER_NODE_MACHINE   machine id of the `node` tier (default map)
  CI_CLUSTER_XPLAT_MACHINE  machine id of the `xplat` tier (default map)
  CI_CLUSTER_TIERS          full map override, one "<name>|<machine-id>|<gates>" per line
  CI_CLUSTER_START_TIMEOUT  seconds to wait for a started machine to become reachable
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

# Start the machine if it is stopped; record ownership. Machines already running
# are used but never stopped by this run — another session may own them. If the
# state cannot be determined, attempt a start and let the reachability probe
# arbitrate: a start that fails but leaves the machine reachable means it was
# already up (not ours); anything else dies in pre-flight.
preflight_machine() { # $1 tier $2 machine-id $3 expected-md5
  local tier="$1" id="$2" want="$3" state
  state=$(fly machine status "$id" --app "$APP" --json 2>/dev/null \
          | tr -d '\r' | sed -n 's/.*"state"[[:space:]]*:[[:space:]]*"\([a-z]*\)".*/\1/p' | head -1)
  case "$state" in
    started)
      info "[$tier] machine $id already started — using it, will NOT stop it (not owned by this run)"
      ;;
    stopped)
      info "[$tier] starting machine $id"
      fly machine start "$id" --app "$APP" \
        || die "[$tier] fly machine start $id failed (state was 'stopped')"
      STARTED_IDS+=("$id")
      ;;
    starting)
      info "[$tier] machine $id is starting — waiting for it"
      ;;
    *)
      warn "[$tier] machine $id state unknown ('${state:-no status answer}') — attempting start"
      if fly machine start "$id" --app "$APP"; then
        STARTED_IDS+=("$id")
      else
        warn "[$tier] start of $id refused — if it answers the probe it was already up (not owned)"
      fi
      ;;
  esac
  probe_until_reachable "$tier" "$id" \
    || die "[$tier] machine $id not SSH-reachable within ${START_TIMEOUT}s — run 'fly machine status $id --app $APP' to inspect"
  verify_md5_on_machine "$tier" "$id" "$want"
}

# --- dispatch ------------------------------------------------------------------------------

# Runs in a BACKGROUND SUBSHELL (one per tier): it writes only to log files and
# stdout, never to parent state. Gates run sequentially — runci.sh holds a
# whole-run flock on the machine's /tmp/runci.lock, so a second concurrent
# invocation would queue for up to 90 minutes, not run in parallel.
# All gates in a tier run even after one fails (runci.sh `all` behaves the same);
# the verdict comes from each log, never from the SSH wrapper's exit code.
dispatch_tier() { # $1 tier $2 machine-id $3 gates(space-separated)
  local tier="$1" id="$2" gates="$3" gate log
  for gate in $gates; do
    log="$LOG_DIR/$tier-$gate.log"
    printf '[%s] dispatching gate %-18s → machine %s\n' "$tier" "$gate" "$id"
    fly ssh console --app "$APP" --machine "$id" -C "/cache/runci.sh $REF $gate" >"$log" 2>&1
    printf '[%s] gate %s finished on machine %s (log: %s)\n' "$tier" "$gate" "$id" "$log"
  done
  printf '[%s] tier complete: %s\n' "$tier" "$gates"
}

# --- verdict collection --------------------------------------------------------------------

# runci.sh's own words are the only truth (wrapper exit codes lie green).
#   PASS                  log contains the final "ALL GATES GREEN"
#   FAIL:gate             log contains "GATE FAILURE(S)"
#   FAIL:real             log contains "REAL FAILURE" (dropped before RESULT — still a failure)
#   FAIL:unknown-gate     deployed runci.sh rejected the gate name
#   FAIL:no-log           the fly invocation never produced a log
#   FAIL:no-result-line   SSH dropped mid-run (or the run was interrupted) — incomplete
verdict_of_log() { # $1 log-file
  local log="$1"
  [ -f "$log" ] || { printf 'FAIL:no-log'; return; }
  if grep -q '^ALL GATES GREEN$' "$log"; then printf 'PASS'; return; fi
  if grep -q 'GATE FAILURE(S)' "$log"; then printf 'FAIL:gate'; return; fi
  if grep -q 'REAL FAILURE' "$log"; then printf 'FAIL:real'; return; fi
  if grep -q 'unknown gate' "$log"; then printf 'FAIL:unknown-gate'; return; fi
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

# --- teardown ------------------------------------------------------------------------------

stop_started_machines() {
  if [ "${#STARTED_IDS[@]}" -eq 0 ]; then
    info "no machines were started by this run — nothing to stop"
    return 0
  fi
  local id
  for id in "${STARTED_IDS[@]}"; do
    info "stopping machine $id (started by this run)"
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
  local overall=0 a gate log verdict sha
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
      if grep -q 'another runci.sh is already running' "$log" 2>/dev/null; then
        warn "tier ${TIER_NAMES[$a]} gate $gate QUEUED behind another run on that machine — verdict may reflect contention"
      fi
    done
  done
  printf 'logs: %s\n' "$LOG_DIR"
  return "$overall"
}

# --- main ----------------------------------------------------------------------------------

main() {
  local REF EXPECTED_FULL EXPECTED_SHORT LOCAL_MD5 overall
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

  # ---- dispatch concurrently, collect verdicts, summarize ----
  dispatch_all_and_wait
  summarize_run; overall=$?
  if [ "$overall" -eq 0 ]; then
    info "OVERALL: PASS — all dispatched gates green"
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
