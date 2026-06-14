#!/usr/bin/env bash
# Omnipus CI worker entrypoint for a single gate run.
# Usage: runci.sh <git-ref> <gate>
#   gate ∈ { all | go-build | go-vet | go-test | contracts | spa | gofmt | quick | embed-build }
# Requires env GIT_REMOTE (authenticated clone URL), set as a Fly secret.
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
git checkout -f "$REF" 2>/dev/null || git checkout -f "origin/$REF" || exit 2
git reset --hard "$REF" --quiet 2>/dev/null || git reset --hard "origin/$REF" --quiet 2>/dev/null || true
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

case "$GATE" in
  gofmt)       step gofmt run_gofmt ;;
  go-build)    step go-build run_gobuild ;;
  go-vet)      step go-vet run_govet ;;
  go-test)     step go-build run_gobuild; step go-test run_gotest ;;
  contracts)   step npm-ci run_npm; step verify-contracts run_contracts ;;
  spa)         step npm-ci run_npm; step typecheck run_typecheck; step vitest run_vitest ;;
  quick)       step gofmt run_gofmt; step go-build run_gobuild ;;
  embed-build) step npm-ci run_npm; step spa-embed run_spaembed; step go-build run_gobuild ;;
  all)
    step npm-ci run_npm
    step gofmt run_gofmt
    step go-build run_gobuild
    step go-vet run_govet
    step verify-contracts run_contracts
    step typecheck run_typecheck
    step vitest run_vitest
    step go-test run_gotest
    ;;
  *) echo "unknown gate: $GATE"; exit 64 ;;
esac

log "RESULT"; [ $rc -eq 0 ] && echo "ALL GATES GREEN" || echo "GATE FAILURE(S) — see above"
exit $rc
