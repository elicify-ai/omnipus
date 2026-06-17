#!/usr/bin/env bash
# Omnipus CI worker entrypoint for a single gate run.
# Usage: runci.sh <git-ref> <gate>
#   gate ∈ { all | go-build | go-vet | go-test | contracts | spa | gofmt | quick | embed-build | e2e }
# Requires env GIT_REMOTE (authenticated clone URL), set as a Fly secret.
#   The `e2e` gate additionally requires OPENROUTER_API_KEY (Fly secret) — set on ci-omnipus via
#   `fly secrets set OPENROUTER_API_KEY=<value> --app ci-omnipus`.
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
run_gobuild()  { ensure_spa_stub; CGO_ENABLED=0 go build -tags "$TAGS" ./...; }
run_govet()    { ensure_spa_stub; CGO_ENABLED=0 go vet -tags "$TAGS" ./...; }
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
run_npm()      { npm ci --no-audit --no-fund; }
run_typecheck(){ npm run typecheck; }
run_vitest()   { npx vitest run --maxWorkers=4; }  # cap workers: 8 oversubscribe shared vCPUs → perf-test timeouts
run_contracts(){ make verify-contracts; }

# End-to-end (Playwright) gate. Mirrors the GitHub Actions `pr.yml` matrix but in a single gate:
# builds the SPA + the gateway binary, prepares a fresh OMNIPUS_HOME with the canonical e2e
# config, seeds the OpenRouter credential, starts the gateway, polls /health, completes
# onboarding via the public API, and runs the full Playwright matrix. Cleans up the gateway
# on EXIT (success or failure) via a trap.
#
# Pre-conditions (set as Fly secrets on the worker):
#   - GIT_REMOTE          (required for all gates)
#   - OPENROUTER_API_KEY  (required for e2e — preflight checks and fails fast)
#
# Wall-clock: ~20–30 min for the full 40-spec matrix on a performance-8x/16GB machine.
# The 5 GitHub Actions shards (llm-chat, llm-agents, llm-light, ui, stubs) collapse into
# a single sequential run here — for shard-by-shard execution use the GitHub Actions
# workflow on a PR.
run_e2e() {
  : "${OPENROUTER_API_KEY:?e2e gate requires OPENROUTER_API_KEY Fly secret}"
  # OMNIPUS_HOME MUST be exported before any omnipus-binary call, otherwise the binary
  # falls back to the default home (~/.omnipus → /root/.omnipus on this worker) and our
  # seeded /tmp/omnipus-e2e config is ignored — gateway would come up on the default port
  # 5000 instead of our seeded 6060, and health-check polling would never see /health.
  export OMNIPUS_HOME=/tmp/omnipus-e2e
  local E2E_BIN=/tmp/omnipus-e2e-bin
  local E2E_LOG=/tmp/omnipus-e2e.log
  local GATEWAY_PID=

  # Trap cleans up the gateway on ANY exit path (success, failure, signal). Inlined
  # (NOT a nested function) because nested functions have their own scope and cannot
  # see the parent's `local GATEWAY_PID` — `set -u` then fires on the unset reference.
  # The trap body runs in the function's own scope, so the local is visible here.
  trap '[ -n "$GATEWAY_PID" ] && kill "$GATEWAY_PID" 2>/dev/null; wait "$GATEWAY_PID" 2>/dev/null; return $?' RETURN

  # 1. Build SPA + sync into the embed target (the //go:embed in pkg/gateway/embed.go
  #    requires pkg/gateway/spa/ to be non-empty).
  log "e2e: build SPA + sync to embed"
  npm run build >/dev/null || return 1
  rm -rf pkg/gateway/spa
  cp -r dist/spa pkg/gateway/spa
  [ -n "$(ls -A pkg/gateway/spa/assets 2>/dev/null)" ] || { echo "SPA sync produced empty assets/" >&2; return 1; }

  # 2. Build the gateway binary with the embedded SPA. CGO=0 matches the runtime build
  #    path so the test binary doesn't pick up CGO-only deps that the production binary
  #    would lack.
  log "e2e: build gateway binary"
  CGO_ENABLED=0 go build -tags "$TAGS" -o "$E2E_BIN" ./cmd/omnipus/ || return 1

  # 3. Fresh OMNIPUS_HOME every run.
  log "e2e: prepare OMNIPUS_HOME"
  rm -rf "$OMNIPUS_HOME"
  mkdir -p "$OMNIPUS_HOME"

  # 4. Seed the canonical e2e config.json. The dev_mode_bypass flag unblocks
  #    onboarding endpoints that require bearer auth before an admin exists.
  #    The model_name MUST match the provider entry's model_name (alias) so
  #    the onboarding handler updates the existing entry instead of overwriting
  #    agent defaults with a non-aliased model path. See .github/workflows/pr.yml
  #    "Seed gateway config" for the same rationale.
  cat > "$OMNIPUS_HOME/config.json" <<'EOF'
{
  "version": 1,
  "gateway": {
    "port": 6060,
    "dev_mode_bypass": true
  },
  "sandbox": {
    "audit_log": true,
    "tool_policies": { "spawn": "allow" }
  },
  "agents": {
    "defaults": { "model_name": "openrouter-glm" }
  },
  "providers": [
    {
      "model_name": "openrouter-glm",
      "model": "openrouter/google/gemini-2.5-flash",
      "api_base": "https://openrouter.ai/api/v1",
      "api_key_ref": "OPENROUTER_API_KEY"
    }
  ]
}
EOF

  # 5. Seed the OpenRouter credential in the encrypted store. credentials.json is
  #    AES-256-GCM with an Argon2id-derived key from $OMNIPUS_HOME/master.key (auto-
  #    generated on first boot if absent). The credential name MUST match the
  #    provider's api_key_ref in config.json.
  log "e2e: seed OpenRouter credential"
  "$E2E_BIN" credentials set OPENROUTER_API_KEY "$OPENROUTER_API_KEY" >/dev/null || return 1

  # 6. Start gateway in background, capture PID for cleanup. --allow-empty permits
  #    boot without a fully-configured provider (defensive; the e2e flow configures
  #    one above).
  log "e2e: start gateway"
  "$E2E_BIN" gateway --allow-empty > "$E2E_LOG" 2>&1 &
  GATEWAY_PID=$!
  sleep 0.5
  if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
    echo "Gateway process died immediately. Panic log:" >&2
    cat "$OMNIPUS_HOME/logs/gateway_panic.log" 2>/dev/null || true
    cat "$E2E_LOG" >&2
    return 1
  fi

  # 7. Poll /health (up to 60s — generous CI safety net, not a budget).
  log "e2e: wait for gateway /health"
  for i in $(seq 1 60); do
    if curl -sf http://localhost:6060/health >/dev/null 2>&1; then
      echo "Gateway ready after ${i}s"
      break
    fi
    if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
      echo "Gateway died during startup wait. Log:" >&2
      cat "$E2E_LOG" >&2
      return 1
    fi
    sleep 1
  done
  if ! curl -sf http://localhost:6060/health >/dev/null 2>&1; then
    echo "Gateway did not become ready within 60s" >&2
    cat "$E2E_LOG" >&2
    return 1
  fi

  # 8. Complete onboarding via API. Must pass the REAL OpenRouter key — the handler
  #    appends a second provider entry (api_key_ref="openrouter_API_KEY", mixed case)
  #    and the agent's model lookup picks this newer entry. A placeholder would 401
  #    on every LLM call.
  log "e2e: complete onboarding"
  jq -n --arg key "$OPENROUTER_API_KEY" \
    '{provider:{id:"openrouter",api_key:$key,model:"openrouter/google/gemini-2.5-flash"},admin:{username:"admin",password:"admin123"}}' \
    | curl -sf -X POST http://localhost:6060/api/v1/onboarding/complete \
        -H 'Content-Type: application/json' -d @- >/dev/null \
    || { echo "Onboarding API failed" >&2; cat "$E2E_LOG" >&2; return 1; }

  # 9. Run the full Playwright matrix. The repo's playwright.config.ts already pins
  #    workers=1 (single gateway, shared credentials — concurrent writes are unsafe
  #    per CLAUDE.md concurrency model) and retries=3 in CI. We don't override.
  #    Both OPENROUTER_API_KEY (alias for the gateway's api_key_ref) and
  #    OPENROUTER_API_KEY_CI (required by tests/e2e/global-setup.ts preflight check
  #    — see .github/workflows/pr.yml env block) are exported with the same value.
  log "e2e: run Playwright matrix"
  OMNIPUS_URL=http://localhost:6060 \
  OPENROUTER_API_KEY="$OPENROUTER_API_KEY" \
  OPENROUTER_API_KEY_CI="$OPENROUTER_API_KEY" \
    npx playwright test

  # 10. Cleanup runs via the trap.
}

case "$GATE" in
  gofmt)       step gofmt run_gofmt ;;
  go-build)    step go-build run_gobuild ;;
  go-vet)      step go-vet run_govet ;;
  go-test)     step go-build run_gobuild; step go-test run_gotest ;;
  contracts)   step npm-ci run_npm; step verify-contracts run_contracts ;;
  spa)         step npm-ci run_npm; step typecheck run_typecheck; step vitest run_vitest ;;
  quick)       step gofmt run_gofmt; step go-build run_gobuild ;;
  embed-build) step npm-ci run_npm; step spa-embed run_spaembed; step go-build run_gobuild ;;
  e2e)         step e2e run_e2e ;;
  all)
    step npm-ci run_npm
    step gofmt run_gofmt
    step go-build run_gobuild
    step go-vet run_govet
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
