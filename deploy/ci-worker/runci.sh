#!/usr/bin/env bash
# Omnipus CI worker entrypoint for a single gate run.
# Usage: runci.sh <git-ref> <gate>
#   gate ∈ { all | go-build | go-vet | go-test | contracts | spa | gofmt | quick }
# Requires env GIT_REMOTE (authenticated clone URL), set as a Fly secret.
set -uo pipefail

REF="${1:-HEAD}"
GATE="${2:-all}"
REPO_DIR=/cache/omnipus   # on the persistent volume → clone survives stop/start
TAGS="goolm,stdjson"
export PATH=/usr/local/go/bin:/cache/go/bin:$PATH

log() { printf '\n\033[1;36m=== %s ===\033[0m\n' "$*"; }
rc=0; step() { local name="$1"; shift; log "$name"; "$@"; local e=$?; printf '\033[1m%s -> exit %d\033[0m\n' "$name" "$e"; [ $e -ne 0 ] && rc=1; return 0; }

# --- sync repo ---
if [ ! -d "$REPO_DIR/.git" ]; then
  log "clone"; git clone "${GIT_REMOTE:?GIT_REMOTE not set}" "$REPO_DIR" || exit 2
fi
cd "$REPO_DIR" || exit 2
log "fetch + checkout $REF"
git fetch --all --prune --quiet || exit 2
git checkout -f "$REF" || exit 2
git reset --hard "$REF" --quiet 2>/dev/null || git reset --hard "origin/$REF" --quiet 2>/dev/null || true
echo "HEAD: $(git rev-parse --short HEAD) $(git log -1 --format='%s')"

run_gofmt()    { local n; n=$(gofmt -l . 2>/dev/null | grep -v '^$' | wc -l); echo "gofmt unformatted=$n"; [ "$n" = 0 ]; }
run_gobuild()  { CGO_ENABLED=0 go build -tags "$TAGS" ./...; }
run_govet()    { CGO_ENABLED=0 go vet -tags "$TAGS" ./...; }
run_gotest()   { CGO_ENABLED=0 go test -tags "$TAGS" -count=1 ./...; }   # the 16GB-needing gate
run_npm()      { npm ci --no-audit --no-fund; }
run_typecheck(){ npm run typecheck; }
run_vitest()   { npx vitest run; }
run_contracts(){ make verify-contracts; }

case "$GATE" in
  gofmt)     step gofmt run_gofmt ;;
  go-build)  step go-build run_gobuild ;;
  go-vet)    step go-vet run_govet ;;
  go-test)   step go-build run_gobuild; step go-test run_gotest ;;
  contracts) step npm-ci run_npm; step verify-contracts run_contracts ;;
  spa)       step npm-ci run_npm; step typecheck run_typecheck; step vitest run_vitest ;;
  quick)     step gofmt run_gofmt; step go-build run_gobuild ;;
  all)
    step gofmt run_gofmt
    step go-build run_gobuild
    step go-vet run_govet
    step npm-ci run_npm
    step verify-contracts run_contracts
    step typecheck run_typecheck
    step vitest run_vitest
    step go-test run_gotest
    ;;
  *) echo "unknown gate: $GATE"; exit 64 ;;
esac

log "RESULT"; [ $rc -eq 0 ] && echo "ALL GATES GREEN" || echo "GATE FAILURE(S) — see above"
exit $rc
