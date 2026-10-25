#!/usr/bin/env bash
# fly-targeted.sh — run ONE targeted command on a SPARE machine of the ci-omnipus-1
# Fly CI cluster. Founder 2026-10-24: "we have the ci machines already, can we not
# leverage them" — the local heavy-command slots are the bottleneck and whole-gate
# runs are too coarse for a lane's one narrow test.
#
# Usage:
#   deploy/ci-worker/fly-targeted.sh <git-ref> <machine-id|auto> -- <command...>
#
#   deploy/ci-worker/fly-targeted.sh origin/release/v0.1.1 auto -- \
#     go test -tags goolm,stdjson -run '^TestX$' -p 1 ./pkg/foo/
#
# What it guarantees — each rule exists because the cluster already bit on its
# absence (deploy/ci-worker/CLAUDE.md, traps and the machine-ownership rules):
#
#   1. ONLY the spare shared machine is eligible by default (48ee71eb1e9768).
#      d8d3977c946558 is EXCLUDED — dedicated to #1266 (chief, 2026-10-25).
#      The performance machine and every tier machine are
#      refused — a targeted run there contends with whole gates, and the
#      2026-10-11 full-e2e-on-shared starvation was exactly such a placement
#      error. An ALREADY-STARTED machine is refused too — this script only
#      ever runs on a machine it started itself. `auto` picks the first
#      eligible stopped spare; an explicit id must be a spare.
#   2. NEVER beside a gate: a machine whose /tmp/runci.lock is held (flock
#      probe) or that is running a runci.sh / another targeted payload is
#      refused, not queued. A machine we just started cannot have a held lock —
#      Fly destroys /tmp (and with it the lock) when a machine stops — but the
#      payload re-checks after start anyway.
#   3. PUSHED refs only: the ref is resolved locally, then confirmed present in
#      `git ls-remote origin`. A local-only ref is a refusal before any machine
#      is touched, never a confusing remote "cannot resolve" half-way through.
#   4. The command runs in a per-run DETACHED WORKTREE built from the cluster's
#      persistent clone (/cache/omnipus) at the exact resolved SHA. The clone
#      itself is never checked out or reset, so runci.sh's state is untouched.
#      The payload echoes its HEAD back and the local side asserts it matches
#      the requested SHA (stale-checkout false-RED, trap 1).
#   5. The command runs as the non-root `ci` user with CGO_ENABLED=0 and
#      runci.sh's cache env (GOCACHE=/cache/go-build, GOPATH=/cache/go,
#      TMPDIR=/cache/tmp). runci.sh re-execs every non-e2e gate as `ci` because
#      tests must not run as root; a targeted run measures the same thing.
#   6. The command's REAL exit code comes back inside the log as an explicit
#      FLY_TARGETED_EXIT marker. The `fly ssh console` wrapper's exit code is
#      recorded but never trusted (wrapper-exit-code false-green, trap 2).
#   7. ALWAYS STOPPED (chief contract, 2026-10-25): a machine this run STARTED
#      is stopped on every exit path, without exception — success, command
#      failure, refusal, abort (Ctrl-C), AND a missing verdict. On a missing
#      verdict (SSH drop, machine stopped under the run, payload killed
#      mid-flight) the payload's own log on the persistent /cache volume is
#      copied back first, and the exit is loud: FLY_TARGETED_NO_VERDICT with
#      the saved log path. The only stop-time declines are for runs that are
#      NOT ours to kill: a foreign gate or another dispatcher's marker active
#      on the machine (loud warning naming it).
#   8. One lane, one machine: a local per-machine lock refuses a second
#      fly-targeted run on the same machine while the first holds it.
#
# Exit codes:
#   0/N  the command's real remote exit code, propagated (N is also printed in
#        the SUMMARY line; an N of 2 is printed explicitly so it cannot be
#        mistaken for the refusal code below)
#   1    evidence incomplete or untrusted: no FLY_TARGETED_VERDICT/EXIT marker
#        (SSH drop — the remote log is copied back and the machine is stopped
#        anyway, with a loud FLY_TARGETED_NO_VERDICT warning), a
#        missing/non-numeric exit marker, or a HEAD mismatch
#   2    usage error or pre-flight refusal: unpushed ref, forbidden machine,
#        local lock held, machine state unreadable, remote busy, payload deploy
#        failed (md5 mismatch — sftp put has produced empty files before), or a
#        remote-side refusal (busy / no repo / no commit / no ci user / lock
#        probe errored)
#
# Env (all optional):
#   FLY_TARGETED_APP            Fly app (default ci-omnipus-1)
#   FLY_TARGETED_SPARES         eligible machine ids (default: 48ee71eb1e9768;
#                               d8d3977c946558 is excluded — dedicated to #1266)
#   FLY_TARGETED_PERF_MACHINE   always-refused id (default 080d74db170408, performance)
#   CI_CLUSTER_GO_MACHINE / _NODE_ / _XPLAT_ / CI_CLUSTER_TIERS
#                               tier map, same vars and defaults as ci-cluster.sh —
#                               those machine ids are refused too
#   FLY_TARGETED_START_TIMEOUT  seconds to wait for SSH reachability (default 180)
#   FLY_TARGETED_LOG_DIR        run dir for the log + payload (default under ${TMPDIR:-/tmp})
#   FLY_TARGETED_REPO_ROOT      checkout used to resolve the ref (default: the repo this
#                               script lives in)
#   FLY_TARGETED_REMOTE_CACHE / _REPO / _CI_USER / _RUNUSER
#                               remote-side root / clone / user / setuser helper
#                               (defaults /cache, /cache/omnipus, ci, runuser — the
#                               values runci.sh uses; overridable for sandboxed tests)
#
# Test suite: deploy/ci-worker/fly-targeted_test.bats (bats; a fake `fly` and
# stubbed remote helpers — no Fly access, no network, no machines).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${FLY_TARGETED_REPO_ROOT:-$(cd "$SCRIPT_DIR/../.." 2>/dev/null && pwd)}"

APP="${FLY_TARGETED_APP:-ci-omnipus-1}"
START_TIMEOUT="${FLY_TARGETED_START_TIMEOUT:-180}"
PROBE_INTERVAL=5
PERF_MACHINE="${FLY_TARGETED_PERF_MACHINE:-080d74db170408}"
# d8d3977c946558 is deliberately NOT here: dedicated to #1266 (chief, 2026-10-25).
SPARES="${FLY_TARGETED_SPARES:-48ee71eb1e9768}"
REMOTE_CACHE="${FLY_TARGETED_REMOTE_CACHE:-/cache}"
REMOTE_REPO="${FLY_TARGETED_REMOTE_REPO:-/cache/omnipus}"
CI_USER="${FLY_TARGETED_CI_USER:-ci}"
RUNUSER="${FLY_TARGETED_RUNUSER:-runuser}"
LOCK_ROOT="${FLY_TARGETED_LOCK_DIR:-${TMPDIR:-/tmp}/fly-targeted-locks}"

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
LOG_DIR="${FLY_TARGETED_LOG_DIR:-${TMPDIR:-/tmp}/omnipus-fly-targeted/run-$RUN_ID}"
PAYLOAD_LOCAL="$LOG_DIR/payload.sh"
PAYLOAD_DIR_REMOTE="$REMOTE_CACHE/fly-targeted/$RUN_ID"
PAYLOAD_REMOTE="$PAYLOAD_DIR_REMOTE/payload.sh"
LOG_FILE="$LOG_DIR/run.log"
OWNER_TOKEN="fly-targeted:$$:$(hostname -s 2>/dev/null || echo host):$(date +%s)"
OWNER_MARKER_PATH="/tmp/fly-targeted-owner"

# --- run state (globals: cleanup runs after main returned) ---------------------------------
MACHINE=""        # resolved machine id, set as soon as a claim begins
OWNED=0           # 1 = THIS run started the machine (the only one it may stop)
REF=""
SHORT_SHA=""
CMD_STRING=""
VERDICT=""
REMOTE_RC=""
HEAD_GOT=""

info() { printf '[fly-targeted] %s\n' "$*"; }
warn() { printf '[fly-targeted] WARNING: %s\n' "$*" >&2; }
die()  { printf '[fly-targeted] ERROR: %s\n' "$*" >&2; exit 2; }

usage() {
  cat <<'EOF'
fly-targeted.sh — run one targeted command on a spare Fly CI machine.

Usage:
  deploy/ci-worker/fly-targeted.sh <git-ref> <machine-id|auto> -- <command...>

  deploy/ci-worker/fly-targeted.sh origin/release/v0.1.1 auto -- \
    go test -tags goolm,stdjson -run '^TestX$' -p 1 ./pkg/foo/

Only the spare shared machines are eligible (default 48ee71eb1e9768;
d8d3977c946558 is excluded — dedicated to #1266); the performance machine and
the go/node/xplat tier machines are refused. `auto` picks the first spare that
is stopped, unlocked and not busy; an explicit id must be a spare. The ref must
be pushed. The command runs as the non-root `ci` user with CGO_ENABLED=0 in a
per-run detached worktree of the cluster clone; its REAL exit code is parsed
from an explicit marker in the log (never the ssh wrapper's) and propagated.

Exit: 0/N = the command's exit code; 1 = evidence incomplete/untrusted;
2 = usage error or pre-flight refusal. Full contract: file header + tests in
fly-targeted_test.bats; cluster background in deploy/ci-worker/CLAUDE.md.
EOF
}

# --- small helpers --------------------------------------------------------------------------

# Single-quote a string for safe embedding in a generated shell script (POSIX-safe).
shell_quote() {
  printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

# md5 of a LOCAL file. macOS has no md5sum; Linux has no `md5`.
md5_of() {
  if command -v md5sum >/dev/null 2>&1; then
    md5sum "$1" | awk '{print $1}'
  elif command -v md5 >/dev/null 2>&1; then
    md5 -q "$1"
  else
    die "neither md5sum nor md5 found — cannot fingerprint $1"
  fi
}

# The tier→machine map, the SAME data and env vars as ci-cluster.sh (mirrored,
# not sourced: ci-cluster.sh runs its main on load — it has no library mode).
default_tier_map() {
  printf '%s\n' \
    "go|${CI_CLUSTER_GO_MACHINE-080d74db170408}|go-gates" \
    "node|${CI_CLUSTER_NODE_MACHINE-d8d0d90a1e9648}|node-gates" \
    "xplat|${CI_CLUSTER_XPLAT_MACHINE-78113d4f329908}|xplat-gates"
}

tier_machine_ids() {
  local raw line machine out=""
  raw="${CI_CLUSTER_TIERS:-$(default_tier_map)}"
  while IFS= read -r line; do
    case "$line" in ''|'#'*) continue ;; esac
    machine="$(printf '%s' "$line" | cut -d'|' -f2)"
    if [ -n "$machine" ]; then out="$out $machine"; fi
  done <<<"$raw"
  printf '%s' "$out"
}

machine_is_forbidden() { # $1 id — 0 = forbidden (tier machine or the performance machine)
  local id="$1" tiers
  tiers="$(tier_machine_ids)"
  case " $tiers " in *" $id "*) return 0 ;; esac
  if [ "$id" = "$PERF_MACHINE" ]; then return 0; fi
  return 1
}

# The machine's state as `fly machine status` prints it ("State: started").
# NOT --json: flyctl's `machine status` has no such flag — the one time a
# dispatcher read it via `--json | sed`, every machine looked unreadable and
# was started-and-stopped under other sessions (ci-cluster.sh, 2026-10-11).
machine_state() { # $1 machine-id; prints e.g. "started"; empty if unreadable
  fly machine status "$1" --app "$APP" 2>/dev/null \
    | tr -d '\r' | sed -n 's/^State:[[:space:]]*\([a-z]*\).*/\1/p' | head -1
}

# A started machine is not immediately SSH-able: poll until a trivial remote
# command ACTUALLY RUNS — the probe requires its output, not the wrapper's
# exit code. flyctl's console AUTO-STARTS a stopped machine and can return 0
# before any command executed (documented in deploy/ci-worker/CLAUDE.md, and
# observed live 2026-10-25: probes passed at 0s while mkdir/sftp failed for
# the whole boot window — a bare `true` probe cannot see that).
probe_until_reachable() { # $1 machine-id
  local id="$1" waited=0 out
  while [ "$waited" -lt "$START_TIMEOUT" ]; do
    out="$(fly ssh console --app "$APP" --machine "$id" -C 'echo probe-ok' 2>/dev/null | tr -d '\r')"
    case "$out" in
      *probe-ok*)
        info "machine $id reachable after ${waited}s"
        return 0
        ;;
    esac
    sleep "$PROBE_INTERVAL"
    waited=$((waited + PROBE_INTERVAL))
  done
  return 1
}

# True when a gate or another targeted payload is executing on the machine —
# used at STOP time so we never halt a machine under someone else's run.
# (The pre-flight busy probe was removed with the always-stop contract: only
# machines this run STARTED are used, and a just-started machine cannot hold
# a lock — Fly destroys /tmp when a machine stops; the payload re-checks the
# lock after start anyway. NOTE on `fly ssh console -C`: flyctl word-splits
# the string shell-style and execs it DIRECTLY — there is NO shell, so
# redirections, `||`, `;` etc. only work inside an explicit `sh -c '...'`,
# verified on the live cluster 2026-10-25.)
machine_is_busy() { # $1 machine-id
  # Busy = a FOREIGN run only: a gate (runci.sh), or a targeted payload from
  # ANOTHER run. Our OWN payload — its cmdline carries OUR RUN_ID — is not
  # foreign: after an SSH drop it may still be executing on the machine, and
  # the always-stop contract (chief, 2026-10-25) requires stopping the
  # machine under it anyway (halting the VM is what ends the orphan).
  # `fly-targete[d]/` bracket trick (ci-cluster's `/cache/[r]unci.sh` shape):
  # without it the probe's own `sh -c` command line carries the pattern text
  # and pgrep matches ITSELF — observed live 2026-10-25.
  fly ssh console --app "$APP" --machine "$1" \
    -C "sh -c 'pgrep -f \"/cache/[r]unci.sh\" >/dev/null 2>&1 && exit 0; pgrep -af \"$REMOTE_CACHE/fly-targete[d]/\" 2>/dev/null | grep -v \"/$RUN_ID/\" | grep -q .'" \
    >/dev/null 2>&1
}

# True when the machine carries a readable fly-targeted owner marker that is
# NOT ours. /tmp dies when the machine stops, so a stale marker cannot outlive
# its machine.
marker_is_foreign() { # $1 machine-id
  local got
  got="$(fly ssh console --app "$APP" --machine "$1" -C "cat $OWNER_MARKER_PATH" 2>/dev/null \
    | tr -d '\r' | sed -n 's/^fly-targeted:/&/p' | head -1)"
  [ -n "$got" ] && [ "$got" != "$OWNER_TOKEN" ]
}

write_owner_marker() { # $1 machine-id
  # `sh -c` wrapper required: -C applies no shell, so `>` needs one (same
  # construct as ci-cluster.sh's write_owner_marker).
  fly ssh console --app "$APP" --machine "$1" \
    -C "sh -c 'echo $OWNER_TOKEN > $OWNER_MARKER_PATH'" >/dev/null 2>&1 \
    || warn "could not write the owner marker on machine $1 (it is still stopped at exit unless busy or foreign)"
}

# --- the local per-machine lock (one lane, one machine) -------------------------------------

acquire_local_lock() { # $1 machine-id
  local dir="$LOCK_ROOT/$1"
  mkdir -p "$LOCK_ROOT" 2>/dev/null || { warn "cannot create lock root $LOCK_ROOT"; return 1; }
  if ! mkdir "$dir" 2>/dev/null; then
    warn "machine $1 is already claimed by another fly-targeted lane (lock: $dir)"
    if [ -f "$dir/pid" ]; then
      warn "holder pid: $(cat "$dir/pid" 2>/dev/null || echo unknown)"
    fi
    return 1
  fi
  printf '%s\n' "$$" >"$dir/pid" 2>/dev/null || true
  return 0
}

release_local_lock() {
  [ -n "$MACHINE" ] || return 0
  rm -rf "${LOCK_ROOT:?}/$MACHINE" 2>/dev/null || true
}

# --- ref resolution (pushed refs only) -------------------------------------------------------

# Resolve <ref> to a full SHA that is CONFIRMED to exist on origin. Resolution
# prefers freshly-fetched origin/<ref>, then <ref> as given (tag or sha) — the
# same preference runci.sh applies remotely. "Pushed" is ls-remote membership of
# the SHA, never local refs alone.
resolve_pushed_ref() { # $1 ref; prints full sha
  local ref="$1" target list
  GIT_TERMINAL_PROMPT=0 git -C "$REPO_ROOT" fetch --quiet origin >/dev/null 2>&1 \
    || info "git fetch failed — resolving '$ref' from local refs (may be stale)"
  target="$(git -C "$REPO_ROOT" rev-parse --verify --quiet "origin/$ref^{commit}")" \
    || target="$(git -C "$REPO_ROOT" rev-parse --verify --quiet "$ref^{commit}")" \
    || true
  if [ -z "${target:-}" ]; then
    die "cannot resolve ref '$ref' in $REPO_ROOT"
  fi
  list="$(GIT_TERMINAL_PROMPT=0 git -C "$REPO_ROOT" ls-remote origin 2>/dev/null)" \
    || die "git ls-remote origin failed — cannot confirm '$ref' is pushed"
  if [ -z "$list" ]; then
    die "origin lists no refs — cannot confirm '$ref' is pushed"
  fi
  # Membership via `case`, NOT `printf | grep -q`: on a big ls-remote (this
  # repo has ~1900 refs) grep -q exits at the first match, printf gets SIGPIPE
  # on the rest, and pipefail turns the pipeline non-zero — a VALID pushed ref
  # gets refused (observed 2026-10-25 on the first live run). A 40-hex sha
  # matching as a substring means the same sha at a line start, so the looser
  # match is exact in practice.
  case "$list" in
    *"$target"*) : ;;
    *) die "ref '$ref' resolves to $target locally, but that commit is NOT on origin — push it first (fly-targeted only runs pushed refs)" ;;
  esac
  printf '%s' "$target"
}

# --- machine claim (selection + lock + start + reachability + busy check) --------------------

# Claims ONE machine: forbidden check, local lock, start-if-stopped, reachability,
# and — for machines we did not start ourselves — the busy probe. Mode "named"
# dies on any refusal (after unwinding the claim); mode "auto" returns 1 so the
# caller can try the next spare. Sets MACHINE immediately; sets OWNED=1 iff this
# run started the machine.
claim_machine() { # $1 id $2 mode(named|auto)
  local id="$1" mode="$2" state refuse=""
  MACHINE="$id"
  if machine_is_forbidden "$id"; then
    refuse="machine $id is a tier/performance machine (tier map or $PERF_MACHINE) — fly-targeted only uses the spares: $SPARES"
  elif ! state="$(machine_state "$id")" || [ -z "$state" ]; then
    refuse="cannot read the state of machine $id — refusing to touch a machine whose ownership is unknown (fly machine status $id --app $APP)"
  elif ! acquire_local_lock "$id"; then
    refuse="local per-machine lock held for $id"
  else
    case "$state" in
      stopped)
        info "starting machine $id"
        # A just-started machine cannot hold a lock: Fly destroys /tmp (the
        # lock lives there) when a machine stops. The payload re-checks anyway.
        if ! fly machine start "$id" --app "$APP" >/dev/null 2>&1; then
          refuse="fly machine start $id failed"
        else
          # OWNED is set the moment the start succeeds — NOT after the
          # reachability probe (chief contract, 2026-10-25: a machine this
          # run started is always stopped, so a start followed by a failed
          # probe must still be owned and stopped by cleanup).
          OWNED=1
          if ! probe_until_reachable "$id"; then
            refuse="machine $id not SSH-reachable within ${START_TIMEOUT}s (fly machine status $id --app $APP)"
          fi
        fi
        ;;
      started|starting)
        # Never run on a machine this script did not start: another session
        # owns it, and a targeted verdict on someone else's machine is not
        # ours to produce (or to stop). Refuse; `auto` moves to the next spare.
        refuse="machine $id is already $state — fly-targeted only runs on machines it started itself (another session owns it)"
        ;;
      *)
        refuse="machine $id is in state '$state' — refusing (fly machine status $id --app $APP)"
        ;;
    esac
  fi

  [ -z "$refuse" ] && return 0

  if [ "$mode" = "named" ]; then
    if [ "$OWNED" -eq 1 ]; then
      stop_owned_machine "claim refused after start"
      OWNED=0
    fi
    release_local_lock
    die "$refuse"
  fi
  info "skipping spare $id: $refuse"
  if [ "$OWNED" -eq 1 ]; then
    # Started while probing this candidate; nothing was dispatched — stop it.
    stop_owned_machine "candidate spare rejected after start"
    OWNED=0
  fi
  release_local_lock
  return 1
}

# --- the generated payload (runs ON the machine) ---------------------------------------------

build_payload() { # $1 full sha
  local sha="$1"
  mkdir -p "$LOG_DIR" || die "cannot create run dir $LOG_DIR"
  cat >"$PAYLOAD_LOCAL" <<EOF
#!/usr/bin/env bash
# fly-targeted payload $RUN_ID — generated by deploy/ci-worker/fly-targeted.sh; do not edit.
set -uo pipefail
SHA=$(shell_quote "$sha")
# The worktree lives INSIDE this run's dir, one level down: the run dir also
# holds payload.sh and run.log (the no-verdict copy-back evidence), and the
# worktree removal must take ONLY the worktree, never the evidence.
WT=$(shell_quote "$PAYLOAD_DIR_REMOTE/worktree")
REPO=$(shell_quote "$REMOTE_REPO")
CACHE=$(shell_quote "$REMOTE_CACHE")
CI_USER=$(shell_quote "$CI_USER")
RUNUSER_BIN=$(shell_quote "$RUNUSER")
CMD=$(shell_quote "$CMD_STRING")
RUN_PATH=$(shell_quote "/usr/local/go/bin:$REMOTE_CACHE/go/bin:/usr/local/gobin:/usr/local/bin:/usr/bin:/bin")
LOGF=$(shell_quote "$PAYLOAD_DIR_REMOTE/run.log")

# The payload keeps its OWN log on the persistent volume (/cache survives a
# machine stop): if the console stream dies mid-run, the local side has no
# verdict, and the no-verdict path copies THIS file back before stopping.
: >"\$LOGF" 2>/dev/null || LOGF=/dev/null
# Retention: keep the newest 5 run dirs (this run's dir is the newest — it
# was created before this payload started).
( cd "\$CACHE/fly-targeted" 2>/dev/null && ls -1t | tail -n +6 | xargs -r rm -rf -- ) >/dev/null 2>&1 || true

# Console AND payload log (the local side parses the console; the log is the
# copy-back evidence when the console stream dies).
flog() { printf '%s\n' "\$*"; printf '%s\n' "\$*" >>"\$LOGF" 2>/dev/null || true; }

fail() { printf 'FLY_TARGETED_VERDICT=%s\nFLY_TARGETED_EXIT=%s\n' "\$1" "\$2"; printf 'FLY_TARGETED_VERDICT=%s\nFLY_TARGETED_EXIT=%s\n' "\$1" "\$2" >>"\$LOGF" 2>/dev/null || true; exit 0; }
cleanup() {
  rc=\$?
  git -C "\$REPO" worktree remove --force "\$WT" >/dev/null 2>&1 || rm -rf "\$WT" >/dev/null 2>&1 || true
  exit "\$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

# Never beside a gate: runci.sh holds an flock on /tmp/runci.lock for its whole
# run; a live targeted payload means another lane. A CANNOT-TELL probe is a
# refusal, never "assume free".
if ! command -v flock >/dev/null 2>&1; then fail lock-error 95; fi
if ! flock -w0 /tmp/runci.lock true 2>/dev/null; then fail busy 90; fi
if pgrep -f "\$CACHE/[r]unci.sh" >/dev/null 2>&1; then fail busy 90; fi
# Another targeted payload = busy, but NOT this payload itself: its own path
# contains the pattern, so exclude our own pid (\$\$) from the match.
if pgrep -f "\$CACHE/fly-targeted/" 2>/dev/null | grep -qv "^\$\$\$"; then fail busy 90; fi

[ -d "\$REPO/.git" ] || fail no-repo 91
git config --global --add safe.directory '*' >/dev/null 2>&1 || true
cd "\$REPO" || fail no-repo 91
if ! git fetch --all --prune --quiet >/dev/null 2>&1; then
  flog "WARNING: git fetch failed on the worker — continuing with the clone's existing objects"
fi
git cat-file -e "\$SHA^{commit}" 2>/dev/null || fail no-commit 92
if ! git worktree add --detach --quiet "\$WT" "\$SHA" >/dev/null 2>&1; then
  git worktree prune >/dev/null 2>&1 || true
  rm -rf "\$WT" 2>/dev/null || true
  git worktree add --detach --quiet "\$WT" "\$SHA" >/dev/null 2>&1 || fail no-commit 92
fi
flog "FLY_TARGETED_HEAD=\$(git -C "\$WT" rev-parse --short HEAD)"

# SPA embed stub — pkg/gateway/spa is gitignored; compile work needs it non-empty
# (runci.sh ensure_spa_stub, root CLAUDE.md "SPA embed stub trap").
if [ ! -e "\$WT/pkg/gateway/spa/index.html" ]; then
  mkdir -p "\$WT/pkg/gateway/spa"
  printf '<!doctype html><title>ci-stub</title>' > "\$WT/pkg/gateway/spa/index.html"
fi

# Non-root test user + shared caches (runci.sh ci_prepare essentials) — the Fly
# SSH shell is root, and tests must not run as root (runci.sh 2026-10-11).
if ! id -u "\$CI_USER" >/dev/null 2>&1; then
  useradd -m -s /bin/bash "\$CI_USER" >/dev/null 2>&1 || fail no-ci-user 93
fi
mkdir -p "\$CACHE/tmp" "\$CACHE/go" "\$CACHE/go-build" "\$CACHE/npm" 2>/dev/null || fail no-ci-user 93
for d in "\$REPO" "\$WT" "\$CACHE/go" "\$CACHE/go-build" "\$CACHE/npm" "\$CACHE/tmp"; do
  find "\$d" ! -user "\$CI_USER" -exec chown -h "\$CI_USER:\$CI_USER" {} + >/dev/null 2>&1 || true
done
printf '[safe]\n\tdirectory = *\n' > "/home/\$CI_USER/.gitconfig" 2>/dev/null \
  && chown "\$CI_USER:\$CI_USER" "/home/\$CI_USER/.gitconfig" 2>/dev/null || true

cd "\$WT" || fail no-commit 92
# The VERDICT/EXIT pair is written ONLY after the command finished: anything
# that dies before it — an SSH drop, the machine being stopped under the run,
# the payload being killed mid-flight — leaves the local side with NO verdict,
# which routes to the copy-back-then-stop path. The command's output streams
# live to the console AND lands in \$LOGF for the copy-back.
RC=0
if [ "\$(id -u)" = "0" ] && [ "\$RUNUSER_BIN" != "none" ]; then
  \$RUNUSER_BIN -u "\$CI_USER" -- env HOME="/home/\$CI_USER" PATH="\$RUN_PATH" GOCACHE="\$CACHE/go-build" GOPATH="\$CACHE/go" CGO_ENABLED=0 TMPDIR="\$CACHE/tmp" bash -c "\$CMD" 2>&1 | tee -a "\$LOGF"
  p=\${PIPESTATUS[0]}
else
  env HOME="/home/\$CI_USER" PATH="\$RUN_PATH" GOCACHE="\$CACHE/go-build" GOPATH="\$CACHE/go" CGO_ENABLED=0 TMPDIR="\$CACHE/tmp" bash -c "\$CMD" 2>&1 | tee -a "\$LOGF"
  p=\${PIPESTATUS[0]}
fi
[ "\$p" -ne 0 ] && RC=\$p
flog "FLY_TARGETED_VERDICT=command"
echo "FLY_TARGETED_EXIT=\$RC"
echo "FLY_TARGETED_EXIT=\$RC" >>"\$LOGF" 2>/dev/null || true
exit "\$RC"
EOF
}

# Deploy the payload with sftp put — which has silently produced EMPTY and
# truncated files on this cluster before — and VERIFY the copy by md5 before
# anything executes it. A mismatch is a hard refusal, never a shrug. Each step
# names itself on failure (a silent "deploy failed" teaches nothing), and the
# whole deploy retries once after a short wait — a machine seconds into its
# first boot answers SSH but can drop the next call (observed live 2026-10-25).
deploy_payload() {
  # 3 attempts with growing backoff (5s, 10s): a freshly resumed machine
  # answers the reachability probe but can drop the next real SSH calls
  # (observed live 2026-10-25 — 3s was not enough between boot and deploy).
  local try deployed=0
  for try in 1 2 3; do
    if fly ssh console --app "$APP" --machine "$MACHINE" -C "mkdir -p '$PAYLOAD_DIR_REMOTE'" >/dev/null 2>&1 \
      && fly ssh sftp put "$PAYLOAD_LOCAL" "$PAYLOAD_REMOTE" --app "$APP" --machine "$MACHINE" >/dev/null 2>&1; then
      deployed=1
      break
    fi
    warn "payload deploy (attempt $try/3) failed at mkdir/sftp on machine $MACHINE"
    if [ "$try" -lt 3 ]; then sleep $((try * 5)); fi
  done
  if [ "$deployed" -ne 1 ]; then
    return 1
  fi
  local want got
  want="$(md5_of "$PAYLOAD_LOCAL")" || return 1
  got="$(fly ssh console --app "$APP" --machine "$MACHINE" \
    -C "sh -c 'md5sum \"$PAYLOAD_REMOTE\" 2>/dev/null || md5 -q \"$PAYLOAD_REMOTE\" 2>/dev/null'" 2>/dev/null \
    | tr -d '\r' | grep -oE '[0-9a-f]{32}' | head -1)"
  if [ -z "$got" ] || [ "$got" != "$want" ]; then
    warn "payload deploy failed at md5 verify (remote '${got:-none}' vs local '$want')"
    return 1
  fi
  return 0
}

# --- run + verdict (markers, never the wrapper's exit code) ----------------------------------

run_payload() {
  info "dispatching on machine $MACHINE: $CMD_STRING"
  fly ssh console --app "$APP" --machine "$MACHINE" -C "bash '$PAYLOAD_REMOTE'" >"$LOG_FILE" 2>&1
  local wrapper_rc=$?
  info "console closed (ssh wrapper exit $wrapper_rc — informational only; the verdict comes from the log markers)"
}

parse_markers() { # sets VERDICT / REMOTE_RC / HEAD_GOT from $LOG_FILE
  VERDICT="$(tr -d '\r' <"$LOG_FILE" 2>/dev/null | sed -n 's/^FLY_TARGETED_VERDICT=//p' | tail -1)"
  REMOTE_RC="$(tr -d '\r' <"$LOG_FILE" 2>/dev/null | sed -n 's/^FLY_TARGETED_EXIT=//p' | tail -1)"
  HEAD_GOT="$(tr -d '\r' <"$LOG_FILE" 2>/dev/null | sed -n 's/^FLY_TARGETED_HEAD=//p' | tail -1)"
}

# --- teardown --------------------------------------------------------------------------------

stop_owned_machine() { # $1 reason
  [ "$OWNED" -eq 1 ] || return 0
  local id="$MACHINE"
  if machine_is_busy "$id"; then
    warn "machine $id is running a gate or another targeted run — NOT stopping it; stop it by hand when idle: fly machine stop $id --app $APP"
    return 0
  fi
  if marker_is_foreign "$id"; then
    warn "machine $id carries another dispatcher's owner marker — NOT stopping it"
    return 0
  fi
  # The stop is the contract — retry it: the Fly API has transient EOF/flake
  # failures (observed live 2026-10-25), and a single failed stop attempt
  # would leave exactly the orphan this script exists to prevent.
  local try=0
  while [ "$try" -lt 3 ]; do
    if fly machine stop "$id" --app "$APP" >/dev/null 2>&1; then
      info "machine $id stopped ($1)"
      return 0
    fi
    try=$((try + 1))
    [ "$try" -lt 3 ] && sleep 3
  done
  if [ "$(machine_state "$id")" = "stopped" ]; then
    info "machine $id is stopped (the stop command errored, but the state confirms)"
    return 0
  fi
  warn "could NOT stop machine $id after 3 attempts — STOP IT MANUALLY, it is billing: fly machine stop $id --app $APP"
}

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  release_local_lock
  if [ "$OWNED" -eq 1 ]; then
    # Contract (chief, 2026-10-25): a machine this run STARTED is ALWAYS
    # stopped, on every exit path without exception. stop_owned_machine still
    # declines when a FOREIGN gate or another dispatcher's owner marker is
    # live on the machine — killing someone else's active run is a different
    # failure class, and gets a loud warning naming the machine instead.
    stop_owned_machine "this run exited (rc $rc)"
  fi
  exit "$rc"
}

# --- main ------------------------------------------------------------------------------------

main() {
  local selector="" sha="" selected="" id

  [ $# -ge 1 ] || { usage >&2; return 2; }
  case "$1" in -h|--help) usage; return 0 ;; esac
  REF="$1"; shift
  [ $# -ge 1 ] || die "missing machine selector (<machine-id|auto>) — usage: fly-targeted.sh <git-ref> <machine-id|auto> -- <command...>"
  selector="$1"; shift
  [ "${1:-}" = "--" ] || die "the command must be separated by a literal '--' — usage: fly-targeted.sh <git-ref> <machine-id|auto> -- <command...>"
  shift
  [ $# -ge 1 ] || die "empty command after '--' — nothing to run"
  CMD_STRING="$*"

  command -v fly >/dev/null 2>&1 || die "fly not on PATH — install flyctl first"
  if [ ! -d "$REPO_ROOT/.git" ] && [ ! -f "$REPO_ROOT/.git" ]; then
    die "$REPO_ROOT is not a git checkout — run from the omnipus repo or set FLY_TARGETED_REPO_ROOT"
  fi

  # resolve_pushed_ref dies inside a command substitution, and a substitution
  # cannot fail the surrounding assignment — the `||` catches its exit code so
  # a refused ref cannot sail on with an EMPTY sha (the exact false-green shape
  # this script exists to prevent).
  sha="$(resolve_pushed_ref "$REF")" || exit 2
  [ -n "$sha" ] || exit 2
  SHORT_SHA="$(git -C "$REPO_ROOT" rev-parse --short "$sha")"
  info "app=$APP ref=$REF sha=$SHORT_SHA machine=$selector"
  info "command: $CMD_STRING"

  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  # ---- select + claim the machine (refusals here touch nothing that runs) ----
  if [ "$selector" = "auto" ]; then
    for id in $SPARES; do
      if claim_machine "$id" auto; then
        info "auto: selected spare $id"
        selected=1
        break
      fi
    done
    if [ -z "$selected" ]; then
      die "no eligible spare machine (spares: $SPARES) — all are forbidden, locked, busy, or unreadable"
    fi
  else
    case " $SPARES " in
      *" $selector "*) ;;
      *) die "machine '$selector' is not an eligible spare (spares: $SPARES) — tier machines and the performance machine ($PERF_MACHINE) are never used by fly-targeted" ;;
    esac
    claim_machine "$selector" named
  fi

  # ---- deploy the payload (md5-verified: sftp put has produced empty files) ----
  build_payload "$sha"
  write_owner_marker "$MACHINE"
  if ! deploy_payload; then
    die "payload deploy failed on machine $MACHINE (sftp put or md5 verify) — refusing to execute an unverified file; nothing was executed"
  fi

  # ---- run, then trust ONLY the markers in the captured log ----
  run_payload
  parse_markers
  if [ -z "$VERDICT" ]; then
    # No verdict = the run never completed (SSH drop, machine stopped under
    # it, payload killed mid-flight). Evidence first, then stop ANYWAY —
    # contract (chief, 2026-10-25): a machine this run started is always
    # stopped, without exception. The payload keeps its own log on the
    # persistent volume; copy back whatever exists before the stop.
    warn "FLY_TARGETED_NO_VERDICT: no verdict marker in the log — the run did not complete; machine $MACHINE will be STOPPED anyway (always-stop contract)"
    if fly ssh sftp get "$PAYLOAD_DIR_REMOTE/run.log" "$LOG_DIR/remote-run.log" --app "$APP" --machine "$MACHINE" >/dev/null 2>&1 && [ -s "$LOG_DIR/remote-run.log" ]; then
      warn "FLY_TARGETED_NO_VERDICT: remote log copied back to $LOG_DIR/remote-run.log"
    else
      warn "FLY_TARGETED_NO_VERDICT: no remote log could be copied back from $PAYLOAD_DIR_REMOTE/run.log (machine unreachable, or the payload died before creating it)"
    fi
    warn "FLY_TARGETED_NO_VERDICT: partial local log: $LOG_FILE"
    return 1
  fi
  case "$VERDICT" in
    command) ;;
    busy)       warn "machine $MACHINE is busy: a gate holds the runci lock or another targeted run is active — nothing was executed"; return 2 ;;
    lock-error) warn "remote lock probe could not run (no flock on the machine) — refusing rather than assuming the machine is free"; return 2 ;;
    no-repo)    warn "no clone at $REMOTE_REPO on machine $MACHINE — run one gate there (runci.sh) so the clone exists, then retry"; return 2 ;;
    no-commit)  warn "commit $SHORT_SHA is not present on machine $MACHINE after fetch — is the ref really pushed?"; return 2 ;;
    no-ci-user) warn "could not prepare the non-root user '$CI_USER' on machine $MACHINE"; return 2 ;;
    *)          warn "unknown remote verdict '$VERDICT' — treating as a refusal; nothing trusted"; return 2 ;;
  esac
  if [ "$HEAD_GOT" != "$SHORT_SHA" ]; then
    warn "HEAD mismatch: the machine checked out '${HEAD_GOT:-nothing}' but the ref resolves to '$SHORT_SHA' — refusing to trust the result (stale-checkout guard)"
    return 1
  fi
  if ! printf '%s' "$REMOTE_RC" | grep -qE '^[0-9]+$'; then
    warn "exit marker is missing or not numeric ('$REMOTE_RC') — evidence inconsistent"
    return 1
  fi
  local ownership="not owned (was already running) — NOT stopped by this run"
  if [ "$OWNED" -eq 1 ]; then ownership="owned — stopping it on exit"; fi
  printf '[fly-targeted] SUMMARY: command exit %s | machine %s (%s) | sha %s | log %s\n' \
    "$REMOTE_RC" "$MACHINE" "$ownership" "$SHORT_SHA" "$LOG_FILE"
  # `return`, not `exit`: the command's real exit code flows from the
  # `main "$@"` call at the bottom, and the EXIT trap (lock release + machine
  # teardown) still fires when the script exits. A bare `exit` in main is the
  # one construct that makes shellcheck's SC2329 lose track of trap-invoked
  # functions (recorded on ci-cluster.sh, verified there by bisection).
  return "$REMOTE_RC"
}

# The status of the script is main's status; the EXIT trap releases the local
# lock and stops the owned machine on every path out — success, failure,
# refusal, and Ctrl-C. (A `main "$@" || var=$?` wrapper here makes shellcheck's
# SC2329 lose track of the trap-invoked cleanup — verified by bisection — so
# main's return status flows from this bare call, as in ci-cluster.sh.)
main "$@"
