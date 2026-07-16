#!/usr/bin/env bash
# Omnipus CI worker entrypoint for a single gate run.
# Usage: runci.sh <git-ref> <gate>
#   gate ∈ { all | go-build | go-vet | lint | go-test | contracts | spa | gofmt | quick | embed-build | e2e }
# Requires env GIT_REMOTE (authenticated clone URL), set as a Fly secret.
#   The `e2e` gate additionally requires OPENROUTER_API_KEY (Fly secret) — set on ci-omnipus via
#   `fly secrets set OPENROUTER_API_KEY=<value> --app ci-omnipus`.
#   Optional for faster sharded e2e: OPENROUTER_API_KEY_B / OPENROUTER_API_KEY_C give the
#   concurrent LLM shards their own rate-limit windows; unset ⇒ they fall back to the primary
#   key (the run still works, just at single-key parallelism). See run_e2e below.
set -uo pipefail

REF="${1:-HEAD}"
GATE="${2:-all}"
REPO_DIR=/cache/omnipus   # on the persistent volume → clone survives stop/start
TAGS="goolm,stdjson"
export PATH=/usr/local/go/bin:/cache/go/bin:$PATH
export HOME="${HOME:-/root}"   # non-login SSH shell has no HOME; gen-contracts.sh uses set -u

log() { printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }
rc=0; step() { local name="$1"; shift; log "$name"; "$@"; local e=$?; printf '\033[1m%s -> exit %d\033[0m\n' "$name" "$e"; [ $e -ne 0 ] && rc=1; return 0; }

# --- sync repo ---
if [ ! -d "$REPO_DIR/.git" ]; then
  log "clone"; git clone "${GIT_REMOTE:?GIT_REMOTE not set}" "$REPO_DIR" || exit 2
fi
cd "$REPO_DIR" || exit 2
log "fetch + checkout $REF"
git fetch --all --prune --quiet || exit 2
# Resolve REF to a concrete commit, ALWAYS preferring the freshly-fetched remote
# branch over any stale local branch of the same name. (A bare `git checkout -f
# <branch>` switches to the local branch and `reset --hard <branch>` resets to it,
# which silently tests a stale commit when the local branch isn't fast-forwarded.)
# Fall back to REF-as-given so an explicit SHA or tag still works.
TARGET="$(git rev-parse --verify --quiet "origin/$REF^{commit}" || git rev-parse --verify --quiet "$REF^{commit}")"
[ -z "$TARGET" ] && { echo "cannot resolve ref $REF"; exit 2; }
git checkout -f "$TARGET" || exit 2
git reset --hard "$TARGET" --quiet || true
echo "HEAD: $(git rev-parse --short HEAD) $(git log -1 --format='%s')"

# Go's //go:embed all:spa needs pkg/gateway/spa/ non-empty. For compile/unit gates a stub is enough
# (the real SPA is only needed to produce a servable binary → the embed-build gate).
ensure_spa_stub() {
  if [ ! -e pkg/gateway/spa/index.html ]; then
    mkdir -p pkg/gateway/spa
    printf '<!doctype html><title>ci-stub</title>' > pkg/gateway/spa/index.html
  fi
}
run_spaembed() { npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa pkg/gateway/spa; }

run_gofmt()    { local n; n=$(gofmt -l . 2>/dev/null | grep -v '^$' | wc -l); echo "gofmt unformatted=$n"; [ "$n" = 0 ]; }
run_gobuild()  {
  ensure_spa_stub
  CGO_ENABLED=0 go build -tags "$TAGS" ./... || return 1
  # Lite/mipsle link checks (compile-only, ~seconds with a warm cache). The
  # mipsle targets must strip goolm (its transitive deps don't build on mips
  # softfloat), which is exactly the variant that silently broke when
  # cmd/omnipus/main.go grew a `goolm && stdjson` gate (2026-06-28..07-03,
  # "function main is undeclared") — nothing exercised these tag sets in CI.
  echo "lite/mipsle link checks"
  CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -tags stdjson,lite -o /dev/null ./cmd/omnipus/ || { echo "GATE FAILURE: mipsle lite build broken"; return 1; }
  CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -tags stdjson -o /dev/null ./cmd/omnipus/ || { echo "GATE FAILURE: mipsle build broken"; return 1; }
  CGO_ENABLED=0 go build -tags "$TAGS",lite -o /dev/null ./cmd/omnipus/ || { echo "GATE FAILURE: lite build broken"; return 1; }
}
run_govet()    { ensure_spa_stub; CGO_ENABLED=0 go vet -tags "$TAGS" ./...; }
# golangci-lint with CGO_ENABLED=0 (so //go:build !cgo test helpers compile) and the
# canonical build tags. Pinned to the version pr.yml uses. Installed once to the
# persistent /cache/go/bin (already on PATH) so it survives stop/start.
GOLANGCI_VERSION=v2.10.1
run_lint() {
  ensure_spa_stub
  if ! command -v golangci-lint >/dev/null 2>&1; then
    log "install golangci-lint $GOLANGCI_VERSION"
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
      | sh -s -- -b /cache/go/bin "$GOLANGCI_VERSION" || return 1
  fi
  CGO_ENABLED=0 golangci-lint run --build-tags="$TAGS"
}
# Full suite with a flake filter: a package that fails the contended full run but passes when
# re-run isolated (-p 1) is a timing flake (shared vCPUs) → not a real failure. Fails both = real.
run_gotest() {
  ensure_spa_stub
  local out; out=$(CGO_ENABLED=0 go test -tags "$TAGS" -count=1 -p 4 ./... 2>&1)
  local code=$?
  echo "$out"
  [ $code -eq 0 ] && return 0
  local failed; failed=$(echo "$out" | grep -aE '^FAIL[[:space:]]' | awk '{print $2}' | grep -a '/' | sort -u)
  [ -z "$failed" ] && return $code
  echo ""; echo "=== FLAKE FILTER: re-running failed packages isolated (-p 1): $failed ==="
  local rc=0
  for p in $failed; do
    if CGO_ENABLED=0 go test -tags "$TAGS" -count=1 -p 1 "$p" >/tmp/rr.log 2>&1; then
      echo "FLAKE (passed isolated): $p"
    else
      echo "REAL FAILURE (failed twice): $p"; grep -aE '^--- FAIL' /tmp/rr.log | head; rc=1
    fi
  done
  return $rc
}
# CLI removed-verb guard (US-11 AC4 / FR-013).
# Scanned: docker/ .github/ deploy/ scripts/ cmd/omnipus-launcher-tui/
# NOT scanned: docs/ (may discuss history) or the spec file itself.
run_cli_verb_guard() {
  local PATTERN='(omnipus|[A-Za-z0-9_./-]*omnipus|\$[A-Z_]+)[[:space:]]+(agent|auth|status|cron|migrate|model|skills)\b'
  local DIRS="docker .github deploy scripts cmd/omnipus-launcher-tui"
  local FOUND
  FOUND=$(grep -rE "$PATTERN" --include="*.sh" --include="*.yml" --include="*.yaml" \
    --include="Dockerfile*" --include="*.go" \
    $DIRS 2>/dev/null | grep -v "^Binary" || true)
  if [ -n "$FOUND" ]; then
    echo "ERROR: removed CLI verb found in infra. Update callers to use 'omnipus start', 'omnipus credentials', etc." >&2
    echo "" >&2
    echo "$FOUND" >&2
    return 1
  fi
  echo "OK: no removed CLI verbs in infra."
}
run_npm()      { npm ci --no-audit --no-fund; }
run_typecheck(){ npm run typecheck; }
run_vitest()   { npx vitest run --maxWorkers=4; }  # cap workers: 8 oversubscribe shared vCPUs → perf-test timeouts
run_contracts(){ make verify-contracts; }

# ── End-to-end (Playwright) gate — sharded ────────────────────────────────────
#
# Builds the SPA + gateway binary once, then fans the Playwright suite out across the
# shards defined in tests/e2e/shards.json — the SAME plan .github/workflows/pr.yml uses
# (both go through scripts/e2e-shards.sh), so the two CI surfaces can never drift apart.
# Each shard runs against its OWN isolated gateway: own port, own OMNIPUS_HOME, own
# encrypted credential store, own auth + skip-manifest files. That per-gateway isolation
# is what lets the shards run CONCURRENTLY even though playwright.config.ts pins workers=1
# (which exists to protect a SINGLE gateway's shared config/credentials from concurrent
# writes — a per-gateway constraint, not a global one). This replaces the old single
# sequential run (~3 h with real-LLM latency) with ~one-slowest-shard wall-clock.
#
# Per-shard OpenRouter keys (runner-agnostic — identical on this Fly worker via Fly
# secrets or on a GitHub-hosted / external self-hosted runner via Actions secrets):
#   OPENROUTER_API_KEY    (slot a, REQUIRED — the primary key)
#   OPENROUTER_API_KEY_B  (slot b, OPTIONAL → falls back to slot a when unset)
#   OPENROUTER_API_KEY_C  (slot c, OPTIONAL → falls back to slot a when unset)
# Pinning each concurrent LLM shard to its own key keeps them off one shared rate-limit
# window (the 429 contention that used to force a single serial run). A worker that sets
# only the one key still runs — every shard just resolves to slot a.
#
# Env knobs:
#   E2E_SPECS      space-separated spec subset → runs the OLD single-gateway path (fast
#                  targeted re-verify; keeps the HTML report). Non-empty ⇒ no sharding.
#   E2E_SHARDED=0  force the single-gateway path for the full matrix (debugging).
#
# Pre-conditions (Fly secrets on the worker): GIT_REMOTE, OPENROUTER_API_KEY.

# Build the SPA, embed it, build the gateway binary to /tmp/omnipus-ci (the path
# tests/e2e/setup.ts hardcodes as DEFAULT_OMNIPUS_BINARY for self-managed-gateway specs),
# and install the matching Chromium. Shared by every shard — run exactly once.
_e2e_build() {
  # SPA + embed sync (the //go:embed in pkg/gateway/embed.go needs pkg/gateway/spa/ non-empty).
  log "e2e: build SPA + sync to embed"
  npm run build >/dev/null || return 1
  rm -rf pkg/gateway/spa
  cp -r dist/spa pkg/gateway/spa
  [ -n "$(ls -A pkg/gateway/spa/assets 2>/dev/null)" ] || { echo "SPA sync produced empty assets/" >&2; return 1; }

  # CGO=0 matches the runtime build path. /tmp/omnipus-ci is hardcoded by tests/e2e/setup.ts.
  log "e2e: build gateway binary"
  CGO_ENABLED=0 go build -tags "$TAGS" -o /tmp/omnipus-ci ./cmd/omnipus/ || return 1

  # Force the chromium revision the installed @playwright/test expects (the caret range in
  # package.json may resolve past the image-baked revision). Cached after the first run;
  # shared by all shards via the image-level $PLAYWRIGHT_BROWSERS_PATH.
  log "e2e: install matching chromium"
  npx playwright install chromium || return 1
}

# Reap any still-running shard gateways by EXACT pid from their pidfiles. Never
# pkill-by-pattern — a pattern can match this very shell (see deploy/ci-worker/CLAUDE.md).
_e2e_reap_pidfiles() {
  local f pid
  for f in /tmp/e2e-shard-*.gwpid; do
    [ -e "$f" ] || continue
    pid="$(cat "$f" 2>/dev/null || true)"
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
    rm -f "$f"
  done
}

# Boot one isolated gateway and run one shard's specs against it.
#   $1 name  $2 port  $3 key  $4 space-separated specs ("" = full matrix)
#   $5 extra `playwright test` args ("" keeps config defaults; sharded runs pass
#      --output + --reporter=list so concurrent shards stay off shared report paths)
# Cleans up its own gateway on any return path via a RETURN trap, and drops a pidfile so
# a hard-killed run can still be reaped by _e2e_reap_pidfiles.
_e2e_run_shard() {
  local name="$1" port="$2" key="$3" specs="$4" pwargs="$5"
  local home="/tmp/omnipus-e2e-$name"
  local logf="/tmp/omnipus-e2e-$name.gw.log"
  local pidfile="/tmp/e2e-shard-$name.gwpid"
  local authfile="/tmp/e2e-$name-auth.json"
  local GATEWAY_PID=

  # Inlined (not a nested fn) so it can see the local GATEWAY_PID under `set -u`.
  trap 'if [ -n "$GATEWAY_PID" ]; then kill "$GATEWAY_PID" 2>/dev/null; wait "$GATEWAY_PID" 2>/dev/null; fi; rm -f "$pidfile"' RETURN

  rm -rf "$home"; mkdir -p "$home"

  # Canonical e2e config with THIS shard's port. Each shard has its own credentials.json
  # under $home, so the shared api_key_ref name ("OPENROUTER_API_KEY") resolves to this
  # shard's key value (seeded next). See pr.yml "Seed gateway config" for the rationale
  # behind dev_mode_bypass / audit_log / the model_name alias.
  cat > "$home/config.json" <<EOF
{
  "version": 1,
  "gateway": { "port": $port, "dev_mode_bypass": true },
  "sandbox": { "audit_log": true, "tool_policies": { "spawn": "allow" } },
  "agents": { "defaults": { "model_name": "openrouter-glm", "auto_recap_enabled": true } },
  "providers": [
    {
      "model_name": "openrouter-glm",
      "model": "openrouter/z-ai/glm-5.2",
      "api_base": "https://openrouter.ai/api/v1",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ]
}
EOF

  OMNIPUS_HOME="$home" /tmp/omnipus-ci credentials set OPENROUTER_API_KEY "$key" >/dev/null || return 1

  # OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20 (ADR-045): reap a genuinely leaked
  # (finished-tab) live turn quickly without touching open-tab / transcript-seeded turns.
  OMNIPUS_HOME="$home" OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20 \
    /tmp/omnipus-ci start --allow-empty > "$logf" 2>&1 &
  GATEWAY_PID=$!
  echo "$GATEWAY_PID" > "$pidfile"
  sleep 0.5
  if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
    echo "[$name] gateway died immediately. Panic log:" >&2
    cat "$home/logs/gateway_panic.log" 2>/dev/null || true
    cat "$logf" >&2
    return 1
  fi

  local ready=
  for i in $(seq 1 60); do
    if curl -sf "http://localhost:$port/health" >/dev/null 2>&1; then ready=1; echo "[$name] gateway ready after ${i}s"; break; fi
    if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then echo "[$name] gateway died during startup. Log:" >&2; cat "$logf" >&2; return 1; fi
    sleep 1
  done
  [ -n "$ready" ] || { echo "[$name] gateway not ready within 60s" >&2; cat "$logf" >&2; return 1; }

  # Onboarding must pass the REAL key — the handler appends a second provider entry the
  # agent's model lookup then picks; a placeholder would 401 every LLM call.
  jq -n --arg key "$key" \
    '{provider:{id:"openrouter",api_key:$key,model:"openrouter/z-ai/glm-5.2"},admin:{username:"admin",password:"admin123"}}' \
    | curl -sf -X POST "http://localhost:$port/api/v1/onboarding/complete" \
        -H 'Content-Type: application/json' -d @- >/dev/null \
    || { echo "[$name] onboarding failed" >&2; cat "$logf" >&2; return 1; }

  log "e2e[$name]: run specs${specs:+ ($specs)}"
  # Per-shard OMNIPUS_AUTH_FILE + OMNIPUS_SKIP_MANIFEST_PATH keep global-setup's session
  # cookie (tests/e2e/global-setup.ts) and global-teardown's manifest (global-teardown.ts)
  # off the shared default paths. The manifest is placed INSIDE the shard's own results dir
  # so the derived soft-skips.json accumulator (skip-tracking.ts softSkipsPath, which lives
  # next to the manifest) is per-shard too — otherwise concurrent shards, sharing one repo
  # CWD, would race on test-results/soft-skips.json. $pwargs (sharded only) adds --output +
  # --reporter=list so concurrent shards don't collide on test-results/ and playwright-report/.
  OMNIPUS_HOME="$home" \
  OMNIPUS_URL="http://localhost:$port" \
  OMNIPUS_AUTH_FILE="$authfile" \
  OMNIPUS_SKIP_MANIFEST_PATH="/tmp/e2e-$name-results/skip-manifest.json" \
  OPENROUTER_API_KEY="$key" \
  OPENROUTER_API_KEY_CI="$key" \
    npx playwright test $specs $pwargs
}

run_e2e() {
  local KEY_A KEY_B KEY_C
  KEY_A="${OPENROUTER_API_KEY:?e2e gate requires OPENROUTER_API_KEY Fly secret}"
  KEY_B="${OPENROUTER_API_KEY_B:-$KEY_A}"
  KEY_C="${OPENROUTER_API_KEY_C:-$KEY_A}"

  _e2e_build || return 1

  # Targeted / opt-out path: the OLD single-gateway flow (keeps the HTML report; no
  # artifact-path isolation needed since only one gateway runs).
  if [ -n "${E2E_SPECS:-}" ] || [ "${E2E_SHARDED:-1}" = 0 ]; then
    log "e2e: single-gateway run${E2E_SPECS:+ (targeted: $E2E_SPECS)}"
    _e2e_run_shard single 6060 "$KEY_A" "${E2E_SPECS:-}" ""
    return $?
  fi

  # Full sharded run — one isolated gateway per shard, all concurrent. `check` fails fast
  # if any spec on disk is unassigned (would never run) or the plan is stale.
  scripts/e2e-shards.sh check || return 1
  # Old runs may have left a soft-skips accumulator at the shared (cwd-relative) path
  # test-results/soft-skips.json; clear it so a stale entry can't taint a shard's teardown.
  rm -f test-results/soft-skips.json 2>/dev/null || true

  local -a NAMES=() PIDS=() FAILED=()
  local group slot port specs key
  while IFS=$'\t' read -r group slot port specs; do
    [ -n "$group" ] || continue
    case "$slot" in b) key="$KEY_B" ;; c) key="$KEY_C" ;; *) key="$KEY_A" ;; esac
    log "e2e: launch shard $group (port $port, key slot $slot)"
    ( _e2e_run_shard "$group" "$port" "$key" "$specs" "--output=/tmp/e2e-$group-results --reporter=list" ) \
      > "/tmp/e2e-shard-$group.log" 2>&1 &
    NAMES+=("$group"); PIDS+=($!)
  done < <(scripts/e2e-shards.sh list 2>/dev/null)

  [ "${#NAMES[@]}" -gt 0 ] || { echo "e2e: no shards launched (empty shard plan?)" >&2; return 1; }

  # Reap surviving gateways if the whole run is interrupted (exact pids only).
  trap '_e2e_reap_pidfiles' INT TERM

  local i erc shard_rc=0
  for i in "${!NAMES[@]}"; do
    if wait "${PIDS[$i]}"; then
      printf '\033[1;32me2e shard %s: PASS\033[0m\n' "${NAMES[$i]}"
    else
      erc=$?
      printf '\033[1;31me2e shard %s: FAIL (exit %d)\033[0m\n' "${NAMES[$i]}" "$erc"
      FAILED+=("${NAMES[$i]}")
      shard_rc=1
    fi
  done
  trap - INT TERM

  # Surface each failing shard's full output (redirected per-shard so concurrent shards
  # never interleave in the console).
  if [ "$shard_rc" -ne 0 ]; then
    local n
    for n in "${FAILED[@]}"; do
      log "e2e: FAILED shard '$n' — output"
      cat "/tmp/e2e-shard-$n.log" 2>/dev/null || echo "(no log for $n)"
    done
    echo "e2e: FAILED shards: ${FAILED[*]}" >&2
  fi
  return "$shard_rc"
}

case "$GATE" in
  gofmt)           step gofmt run_gofmt ;;
  go-build)        step go-build run_gobuild ;;
  go-vet)          step go-vet run_govet ;;
  lint)            step golangci-lint run_lint ;;
  go-test)         step go-build run_gobuild; step go-test run_gotest ;;
  contracts)       step npm-ci run_npm; step verify-contracts run_contracts ;;
  spa)             step npm-ci run_npm; step typecheck run_typecheck; step vitest run_vitest ;;
  quick)           step gofmt run_gofmt; step go-build run_gobuild ;;
  embed-build)     step npm-ci run_npm; step spa-embed run_spaembed; step go-build run_gobuild ;;
  e2e)             step e2e run_e2e ;;
  cli-verb-guard)  step cli-verb-guard run_cli_verb_guard ;;
  all)
    step cli-verb-guard run_cli_verb_guard
    step npm-ci run_npm
    step gofmt run_gofmt
    step go-build run_gobuild
    step go-vet run_govet
    step golangci-lint run_lint
    step verify-contracts run_contracts
    step typecheck run_typecheck
    step vitest run_vitest
    step go-test run_gotest
    step e2e run_e2e
    ;;
  *) echo "unknown gate: $GATE"; exit 64 ;;
esac

log "RESULT"; [ $rc -eq 0 ] && echo "ALL GATES GREEN" || echo "GATE FAILURE(S) — see above"
exit $rc
