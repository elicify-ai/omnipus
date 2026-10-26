#!/usr/bin/env bats
#
# fly-targeted.sh — bats test suite.
#
# Proves the targeted-runner's contract against a FAKE `fly` (no Fly access,
# no network, no machines, no cost) with sandboxed remote paths and stubbed
# remote helpers (runuser/useradd/flock). The git fixtures are real but local:
# a bare "origin", a developer checkout (ref resolution) and a "cluster clone"
# (what /cache/omnipus is on a real machine).
#
# Cases:
#   usage   argument handling: no args, missing selector, missing '--',
#           empty command, --help
#   ref     an unpushed ref is refused before any machine is touched
#   machine the performance machine, tier machines, unknown ids, the
#           #1266-dedicated d8d3977c946558 and ALREADY-STARTED machines are
#           refused; an unreadable machine state is refused (named) /
#           skipped (auto)
#   lock    the local per-machine lock blocks a second lane; auto skips a
#           locked spare; the payload's own flock re-check refuses a lock
#           that appeared after the claim
#   auto    picks the first eligible stopped spare; skips locked/unreadable;
#           refuses when no spare is eligible
#   stop    an owned machine is ALWAYS stopped — on success, on a failing
#           command, on an abort (SIGTERM), after a refused/failed dispatch,
#           when start succeeds but reachability fails, and when no verdict
#           arrives (the remote log is copied back first, loud
#           FLY_TARGETED_NO_VERDICT warning)
#   verdict the command's REAL exit code is propagated from the marker (never
#           the wrapper's); a HEAD mismatch is a refusal; an empty/truncated
#           sftp put is caught by the md5 verify; the generated payload is
#           syntactically valid bash
set -uo pipefail

SCRIPT="${BATS_TEST_DIRNAME}/fly-targeted.sh"

setup() {
  SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/fly-targeted-test.XXXXXX")"
  BIN="$SANDBOX/bin"
  STATE_DIR="$SANDBOX/state"
  mkdir -p "$BIN" "$STATE_DIR"
  export PATH="$BIN:$PATH"
  # The payload writes git's --global config; keep that out of the real HOME.
  export HOME="$SANDBOX/home"
  mkdir -p "$HOME"

  # --- git fixtures: bare origin, developer checkout, cluster clone ----------
  ORIGIN="$SANDBOX/origin.git"
  SRC="$SANDBOX/src"                     # FLY_TARGETED_REPO_ROOT (ref resolution)
  CACHE="$SANDBOX/remote-cache"          # FLY_TARGETED_REMOTE_CACHE (/cache)
  CLONE="$CACHE/omnipus"                 # FLY_TARGETED_REMOTE_REPO (/cache/omnipus)
  git init --bare -q -b main "$ORIGIN"
  git init -q -b main "$SRC"
  git -C "$SRC" remote add origin "$ORIGIN"
  ( cd "$SRC" \
    && echo one >f.txt \
    && git add f.txt \
    && git -c user.name=t -c user.email=t@example.com commit -qm one \
    && git push -q origin main )
  git clone -q "$ORIGIN" "$CLONE"
  FULL_SHA="$(git -C "$SRC" rev-parse HEAD)"
  SHORT_SHA="$(git -C "$SRC" rev-parse --short HEAD)"

  # --- script configuration: everything sandbox-local ------------------------
  export FLY_TARGETED_REPO_ROOT="$SRC"
  export FLY_TARGETED_LOG_DIR="$SANDBOX/logs"
  export FLY_TARGETED_LOCK_DIR="$SANDBOX/locks"
  export FLY_TARGETED_REMOTE_CACHE="$CACHE"
  export FLY_TARGETED_REMOTE_REPO="$CLONE"
  export FLY_TARGETED_RUNUSER="$BIN/fake-runuser"
  export FLY_TARGETED_CI_USER="ci"
  export FLY_TARGETED_SPARES="spare1 spare2"
  export FLY_TARGETED_PERF_MACHINE="perf1"
  export CI_CLUSTER_GO_MACHINE="tiergo"
  export CI_CLUSTER_NODE_MACHINE="tiernode"
  export CI_CLUSTER_XPLAT_MACHINE="tierx"
  export FLY_TARGETED_START_TIMEOUT="2"

  # --- fake fly + stubs -------------------------------------------------------
  export FLY_STUB_LOG="$SANDBOX/fly-calls.log"
  export FLY_STUB_STATE_DIR="$STATE_DIR"
  printf stopped >"$STATE_DIR/spare1"
  printf stopped >"$STATE_DIR/spare2"

  cat >"$BIN/fly" <<'STUB'
#!/usr/bin/env bash
# fake fly: records every call, simulates machine state + ssh console/sftp.
LOG="${FLY_STUB_LOG:?}"
STATE_DIR="${FLY_STUB_STATE_DIR:?}"
rec() { printf '%s\n' "$*" >>"$LOG"; }
state_of() { cat "$STATE_DIR/$1" 2>/dev/null; }
cmd="$1"; shift
case "$cmd" in
  machine)
    sub="$1"; shift
    id="$1"
    case "$sub" in
      status) printf 'ID=%s\nState: %s\n' "$id" "$(state_of "$id")" ;;
      start)  rec "machine start $id";  printf started >"$STATE_DIR/$id" ;;
      stop)   rec "machine stop $id";   printf stopped >"$STATE_DIR/$id" ;;
      *) echo "FLY_STUB: unhandled machine sub: $sub" >&2; exit 3 ;;
    esac
    ;;
  ssh)
    mode="$1"; shift
    machine=""; ccmd=""; local_path=""; remote_path=""; action=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --app)     shift 2 ;;
        --machine) machine="$2"; shift 2 ;;
        -C)        ccmd="$2"; shift 2 ;;
        put)       action="put"; local_path="$2"; remote_path="$3"; shift 3 ;;
        get)       action="get"; remote_path="$2"; local_path="$3"; shift 3 ;;
        *)         shift ;;
      esac
    done
    case "$mode" in
      console)
        rec "console $machine :: $ccmd"
        case "$ccmd" in
          *probe-ok*)
            # the reachability probe: simulate an unreachable machine on demand
            if [ "${FLY_STUB_FAIL_PROBE:-0}" = "1" ]; then
              printf 'booting-no-command-output\n'
            else
              printf 'probe-ok\n'
            fi
            ;;
          *fly-targeted-owner*)
            case "$ccmd" in
              cat\ *)
                if [ "${FLY_STUB_OWNER_FOREIGN:-0}" = "1" ]; then
                  printf 'fly-targeted:FOREIGN\n'
                else
                  cat "$STATE_DIR/owner-marker" 2>/dev/null
                fi
                ;;
              *)
                # extract the token (the word after `echo` in the sh -c
                # payload) so a later read matches ours
                printf '%s\n' "$(printf '%s' "$ccmd" | sed -n "s/.* echo \(fly-targeted:[^ ]*\) >.*/\1/p")" >"$STATE_DIR/owner-marker"
                ;;
            esac
            ;;
          *pgrep*)
            if [ "${FLY_STUB_REMOTE_BUSY:-0}" = "1" ]; then exit 0; else exit 1; fi
            ;;
          mkdir\ -p\ *) eval "mkdir -p ${ccmd#mkdir -p }" ;;
          md5sum*|*md5\ -q*) eval "$ccmd" 2>/dev/null | grep -oE '[0-9a-f]{32}' | head -1 ;;
          bash\ \'*\')
            p="$(printf '%s' "$ccmd" | sed -e "s/^bash '//" -e "s/'\$//")"
            rec "payload-run $p"
            if [ "${FLY_STUB_SKIP_RUN:-0}" = "1" ]; then exit 0; fi
            if [ "${FLY_STUB_PARTIAL:-0}" = "1" ]; then
              bash "$p" 2>&1 | sed -e '/^FLY_TARGETED_VERDICT=/d' -e '/^FLY_TARGETED_EXIT=/d'
              exit "${PIPESTATUS[0]}"
            fi
            if [ "${FLY_STUB_TAMPER_HEAD:-0}" = "1" ]; then
              bash "$p" 2>&1 | sed -e 's/^FLY_TARGETED_HEAD=.*/FLY_TARGETED_HEAD=deadbeef/'
              exit "${PIPESTATUS[0]}"
            fi
            bash "$p" 2>&1
            exit "$?"
            ;;
          *)
            echo "FLY_STUB: unhandled console command: $ccmd" >&2
            exit 3
            ;;
        esac
        ;;
      sftp)
        # $action distinguishes put (LOCAL REMOTE) from get (REMOTE LOCAL)
        case "$action" in
          put) rec "sftp put $local_path -> $remote_path" ;;
          get) rec "sftp get $remote_path -> $local_path" ;;
        esac
        case "$action" in
          get)
            # copy-back: the payload's remote log is a REAL sandbox file —
            # copy it, honoring the failure/empty modes for the refusal tests
            case "${FLY_STUB_SFTP_MODE:-normal}" in
              fail)  exit 1 ;;
              empty) mkdir -p "$(dirname "$local_path")"; : >"$local_path" ;;
              *)     mkdir -p "$(dirname "$local_path")"; cp "$remote_path" "$local_path" ;;
            esac
            ;;
          *)
            case "${FLY_STUB_SFTP_MODE:-normal}" in
              fail)     exit 1 ;;
              empty)    mkdir -p "$(dirname "$remote_path")"; : >"$remote_path" ;;
              truncate) mkdir -p "$(dirname "$remote_path")"; printf 'x' >"$remote_path" ;;
              *)        cp "$local_path" "$remote_path" ;;
            esac
            ;;
        esac
        ;;
      *) echo "FLY_STUB: unhandled ssh sub: $mode" >&2; exit 3 ;;
    esac
    ;;
  *) echo "FLY_STUB: unhandled: $cmd" >&2; exit 3 ;;
esac
STUB
  chmod +x "$BIN/fly"

  # payload runs the command as "ci": skip the user switch, run locally.
  printf '#!/usr/bin/env bash\nwhile [ $# -gt 0 ]; do case "$1" in -u) shift 2 ;; --) shift; break ;; *) shift ;; esac; done\nexec "$@"\n' >"$BIN/fake-runuser"
  # flock stub, named `flock` so the payload's `command -v flock` finds it:
  # held iff the sentinel file exists (on a real machine a gate holds it).
  printf '#!/usr/bin/env bash\nif [ -n "${FAKE_LOCK_HELD:-}" ] && [ -e "$FAKE_LOCK_HELD" ]; then exit 1; fi\nexit 0\n' >"$BIN/flock"
  # useradd stub, named `useradd` so the payload finds it: pretend it created the ci user.
  printf '#!/usr/bin/env bash\nexit 0\n' >"$BIN/useradd"
  chmod +x "$BIN/fake-runuser" "$BIN/flock" "$BIN/useradd"
}

teardown() {
  rm -rf "$SANDBOX"
}

# --- usage / argument handling ---------------------------------------------------------------

@test "usage: no arguments exits 2 and prints usage" {
  run bash "$SCRIPT"
  [ "$status" -eq 2 ]
  grep -q 'Usage:' <<<"$output"
}

@test "usage: missing machine selector exits 2" {
  run bash "$SCRIPT" "$SHORT_SHA"
  [ "$status" -eq 2 ]
  grep -q 'missing machine selector' <<<"$output"
}

@test "usage: missing '--' separator exits 2" {
  run bash "$SCRIPT" "$SHORT_SHA" auto echo hi
  [ "$status" -eq 2 ]
  grep -q "literal '--'" <<<"$output"
}

@test "usage: empty command after '--' exits 2" {
  run bash "$SCRIPT" "$SHORT_SHA" auto --
  [ "$status" -eq 2 ]
  grep -q "empty command" <<<"$output"
}

@test "usage: --help exits 0" {
  run bash "$SCRIPT" --help
  [ "$status" -eq 0 ]
  grep -q 'Usage:' <<<"$output"
}

# --- ref handling -----------------------------------------------------------------------------

@test "ref: an unpushed commit is refused before any machine is touched" {
  # a second commit that exists ONLY in the developer checkout
  unpushed="$( cd "$SRC" \
    && echo two >f.txt \
    && git -c user.name=t -c user.email=t@example.com commit -qam two \
    && git rev-parse HEAD )"
  run bash "$SCRIPT" "$unpushed" auto -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'NOT on origin' <<<"$output"
  [ ! -f "$FLY_STUB_LOG" ]   # fly was never called at all
}

# --- machine eligibility ------------------------------------------------------------------------

@test "machine: the performance machine is refused by name" {
  run bash "$SCRIPT" "$SHORT_SHA" perf1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'not an eligible spare' <<<"$output"
  [ ! -f "$FLY_STUB_LOG" ]
}

@test "machine: a tier machine is refused by name" {
  run bash "$SCRIPT" "$SHORT_SHA" tiergo -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'not an eligible spare' <<<"$output"
  [ ! -f "$FLY_STUB_LOG" ]
}

@test "machine: an unknown id is refused" {
  run bash "$SCRIPT" "$SHORT_SHA" no-such-machine -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'not an eligible spare' <<<"$output"
  [ ! -f "$FLY_STUB_LOG" ]
}

@test "machine: d8d3977c946558 is NOT in the default spare set (dedicated to #1266)" {
  # pins the shipped default (chief, 2026-10-25): with FLY_TARGETED_SPARES
  # unset, the #1266-dedicated machine is refused before fly is ever called.
  unset FLY_TARGETED_SPARES
  run bash "$SCRIPT" "$SHORT_SHA" d8d3977c946558 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'not an eligible spare' <<<"$output"
  grep -q '48ee71eb1e9768' <<<"$output"   # the remaining default spare is named
  [ ! -f "$FLY_STUB_LOG" ]
}

@test "machine: an unreadable state is refused (named) and never touched" {
  rm -f "$STATE_DIR/spare1"
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'ownership is unknown' <<<"$output"
  ! grep -q 'machine start' "$FLY_STUB_LOG"
}

# --- the local per-machine lock ------------------------------------------------------------------

@test "lock: a second lane is refused while the local per-machine lock is held" {
  mkdir -p "$FLY_TARGETED_LOCK_DIR/spare1"
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'already claimed' <<<"$output"
  ! grep -q 'machine start' "$FLY_STUB_LOG"
}

@test "machine: an ALREADY-STARTED machine is refused and never touched (chief contract, 2026-10-25)" {
  # fly-targeted only ever runs on a machine it started itself: another
  # session owns a started machine, so it is refused — not used, not stopped.
  printf started >"$STATE_DIR/spare1"
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'only runs on machines it started itself' <<<"$output"
  ! grep -q 'machine start' "$FLY_STUB_LOG"
  ! grep -q 'machine stop' "$FLY_STUB_LOG"
  ! grep -q 'payload-run' "$FLY_STUB_LOG"
}

@test "lock: the payload's flock re-check refuses a lock that appeared after the claim" {
  export FAKE_LOCK_HELD="$SANDBOX/held-sentinel"
  : >"$FAKE_LOCK_HELD"
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'is busy' <<<"$output"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"   # owned, nothing ran → stopped
  grep -q 'FLY_TARGETED_VERDICT=busy' "$FLY_TARGETED_LOG_DIR/run.log"
  ! grep -q 'FLY_TARGETED_VERDICT=command' "$FLY_TARGETED_LOG_DIR/run.log"
}

# --- auto selection -------------------------------------------------------------------------------

@test "auto: picks the first stopped spare and runs the command" {
  run bash "$SCRIPT" "$SHORT_SHA" auto -- echo targeted-ok
  [ "$status" -eq 0 ]
  grep -q 'selected spare spare1' <<<"$output"
  grep -q 'targeted-ok' <<<"$output"
  grep -q 'command exit 0' <<<"$output"
  grep -q 'machine start spare1' "$FLY_STUB_LOG"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
}

@test "auto: skips a spare held by a local lock and takes the next" {
  mkdir -p "$FLY_TARGETED_LOCK_DIR/spare1"
  run bash "$SCRIPT" "$SHORT_SHA" auto -- echo hi
  [ "$status" -eq 0 ]
  grep -q 'selected spare spare2' <<<"$output"
}

@test "auto: skips a spare with an unreadable state" {
  rm -f "$STATE_DIR/spare1"
  run bash "$SCRIPT" "$SHORT_SHA" auto -- echo hi
  [ "$status" -eq 0 ]
  grep -q 'selected spare spare2' <<<"$output"
}

@test "auto: refuses when no spare is eligible" {
  printf started >"$STATE_DIR/spare1"
  rm -f "$STATE_DIR/spare2"
  export FLY_STUB_REMOTE_BUSY=1
  run bash "$SCRIPT" "$SHORT_SHA" auto -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'no eligible spare' <<<"$output"
  ! grep -q 'machine start' "$FLY_STUB_LOG"
}

# --- stop-on-exit ---------------------------------------------------------------------------------

@test "stop: a failing command propagates its REAL exit code and stops the owned machine" {
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo targeted-boom '>&2;' exit 3
  [ "$status" -eq 3 ]
  grep -q 'command exit 3' <<<"$output"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
  grep -q 'targeted-boom' "$FLY_TARGETED_LOG_DIR/run.log"
}

@test "stop: start-then-unreachable still stops the machine it started" {
  # chief contract, 2026-10-25: ownership begins at the successful START, not
  # after the reachability probe — a machine we started and cannot reach is
  # still ours to stop. (Red against the old code, which set OWNED after the
  # probe and left the machine running on this path.)
  export FLY_STUB_FAIL_PROBE=1
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'not SSH-reachable' <<<"$output"
  grep -q 'machine start spare1' "$FLY_STUB_LOG"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
}

@test "stop: SIGTERM mid-run aborts and stops the owned machine" {
  # NOTE on timing: bash defers a trap until its current FOREGROUND child
  # exits. `kill -TERM` here signals the script alone (a real Ctrl-C signals
  # the whole foreground process group, killing the fly child too), so the
  # script's 'exit 143' runs only once the fake fly's `sleep N` payload is
  # done — keep N small; the test asserts the trap fired (143) and the owned
  # machine was stopped, not when.
  bash "$SCRIPT" "$SHORT_SHA" spare1 -- sleep 6 >/dev/null 2>&1 &
  pid=$!
  sleep 3
  kill -TERM "$pid" 2>/dev/null || true
  rc=0
  wait "$pid" || rc=$?   # the aborted script exits 143 — guard, do not fail here
  [ "$rc" -eq 143 ]
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
}

@test "stop: a missing verdict copies back the remote log and STOPS the owned machine" {
  # chief contract, 2026-10-25: no verdict ⇒ evidence first (the payload's
  # own log on the persistent volume is copied back), then stop anyway, with
  # a loud FLY_TARGETED_NO_VERDICT warning carrying the saved log path.
  # (Red against the old code, which left the owned machine RUNNING here.)
  export FLY_STUB_PARTIAL=1
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 1 ]
  grep -q 'FLY_TARGETED_NO_VERDICT' <<<"$output"
  grep -q 'will be STOPPED anyway' <<<"$output"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
  # the copy-back exists, is non-empty, and carries the payload's own markers
  [ -s "$FLY_TARGETED_LOG_DIR/remote-run.log" ]
  grep -q 'FLY_TARGETED_HEAD=' "$FLY_TARGETED_LOG_DIR/remote-run.log"
  grep -q 'FLY_TARGETED_EXIT=0' "$FLY_TARGETED_LOG_DIR/remote-run.log"
}

@test "stop: an sftp put that delivers an empty file is caught by the md5 verify" {
  export FLY_STUB_SFTP_MODE=empty
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'md5 verify' <<<"$output"
  ! grep -q 'payload-run' "$FLY_STUB_LOG"               # nothing was executed
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"         # owned, nothing ran → stopped
}

@test "stop: a failed sftp put is a refusal, and the owned machine is stopped" {
  export FLY_STUB_SFTP_MODE=fail
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'payload deploy failed' <<<"$output"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
}

# --- verdict trust ---------------------------------------------------------------------------------

@test "verdict: a HEAD mismatch refuses to trust an otherwise green run" {
  export FLY_STUB_TAMPER_HEAD=1
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 1 ]
  grep -q 'HEAD mismatch' <<<"$output"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
}

@test "verdict: a missing clone on the machine is a remote refusal" {
  rm -rf "$CLONE"
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 2 ]
  grep -q 'no clone at' <<<"$output"
  grep -q 'machine stop spare1' "$FLY_STUB_LOG"
}

@test "verdict: the generated payload is syntactically valid bash and is cleaned up" {
  run bash "$SCRIPT" "$SHORT_SHA" spare1 -- echo hi
  [ "$status" -eq 0 ]
  payload="$FLY_TARGETED_LOG_DIR/payload.sh"
  [ -f "$payload" ]
  bash -n "$payload"
  # the per-run worktree was removed from the cluster clone after the run;
  # the run dir itself REMAINS (payload.sh + run.log are the copy-back
  # evidence, pruned to the newest 5 by the payload) — only the worktree dies
  [ -z "$(find "$CACHE/fly-targeted" -maxdepth 2 -type d -name worktree 2>/dev/null)" ]
  # `/fly-targeted/` with slashes: the sandbox dir is named fly-targeted-test.*,
  # which must not match its own assertion
  ! git -C "$CLONE" worktree list | grep -q '/fly-targeted/'
}
