#!/usr/bin/env bash
# Frozen first RED run for #1449. Team-lead runs this. The author does not.
# Rules: /workspace/logs/RUN-RULES.md
# - timeout inside heavy, kill-after 30s
# - one heavy command at a time, Go -p 1, narrow -run
# - the 100k allocation test is not matched by ^TestGroupChat1449_
# - one typecheck per tree, reused from /workspace/logs/typecheck-cache when the tree is clean
# A timeout (exit 124) is TIMED OUT, not RED and not PASS.
set -u
cd /workspace/wt/1449-gc-qa
LOGROOT="/workspace/logs/1449-red/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$LOGROOT"
fail=0

run_step() {
  local name="$1" limit="$2"
  shift 2
  local log="$LOGROOT/${name}.log"
  echo "STEP ${name} limit=${limit}"
  /usr/local/bin/heavy timeout --kill-after=30s "$limit" "$@" >"$log" 2>&1
  local code=$?
  if [[ "$code" -eq 124 || "$code" -eq 137 ]]; then
    echo "TIMED OUT ${name} exit=${code}" | tee -a "$log"
    fail=$((fail + 1))
    return
  fi
  echo "exit=${code}" | tee -a "$log"
  if [[ "$code" -ne 0 ]]; then
    echo "FAIL ${name} exit=${code} log=${log}"
    fail=$((fail + 1))
  else
    echo "OK ${name} log=${log}"
  fi
}

run_step rooms-red 8m \
  env CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -timeout 6m \
  -run '^TestGroupChat1449_' ./pkg/rooms/

run_step tools-red 10m \
  env CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -timeout 8m \
  -run '^TestGroupChat1449_' ./pkg/tools/

run_step agent-origin-red 20m \
  env CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -timeout 15m \
  -run '^TestGroupChat1449_RoomOriginOnlyWhenTheStepAnswersThatGroupAlone$' ./pkg/agent/

run_step gateway-http-red 20m \
  env CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -timeout 15m \
  -run '^TestGroupChat1449_HTTPMessageRules$' ./pkg/gateway/

run_step vitest-display 8m \
  npx vitest run src/components/rooms/groupchat1449.display.test.tsx

run_step vitest-composer 8m \
  npx vitest run src/components/rooms/groupchat1449.composer.test.tsx

run_step vitest-live 8m \
  npx vitest run src/components/rooms/groupchat1449.live.test.tsx

run_step vitest-avatar-stack 8m \
  npx vitest run src/components/ui/avatar-stack.test.tsx

run_step vitest-history 8m \
  npx vitest run src/store/groupchat1449.history-limit.test.ts

run_step vitest-library 8m \
  npx vitest run src/components/library/groupchat1449.delete-confirm.test.tsx

# Typecheck once per clean tree. A dirty tree is not cached.
if [[ -z "$(git status --porcelain)" ]]; then
  tree="$(git rev-parse 'HEAD^{tree}')"
  cache="/workspace/logs/typecheck-cache/${tree}.exit"
  mkdir -p /workspace/logs/typecheck-cache
  if [[ -f "$cache" ]]; then
    echo "TYPECHECK reused ${cache} exit=$(cat "$cache")" | tee "$LOGROOT/typecheck.log"
    reused="$(cat "$cache")"
    if [[ "$reused" -ne 0 ]]; then
      fail=$((fail + 1))
    fi
  else
    run_step typecheck 15m npm run typecheck
    # run_step already counted a failure. Record the exit for the next clean tree.
    code="$(awk -F= '/^exit=/{print $2}' "$LOGROOT/typecheck.log" | tail -1)"
    if [[ -n "${code}" ]]; then
      printf '%s\n' "$code" >"$cache"
      echo "typecheck log=$LOGROOT/typecheck.log" >>"$cache"
    fi
  fi
else
  echo "TYPECHECK tree dirty — running without cache" | tee "$LOGROOT/typecheck.log"
  run_step typecheck 15m npm run typecheck
fi

if [[ -n "${OMNIPUS_URL:-}" ]]; then
  run_step playwright-remote-image 20m \
    npx playwright test tests/e2e/groupchat1449-remote-image.spec.ts
else
  echo "BLOCKED: OMNIPUS_URL is unset. Playwright T-56 / S-106 was not run. This is not a product PASS." | tee "$LOGROOT/playwright-remote-image.log"
  echo "exit=2" >>"$LOGROOT/playwright-remote-image.log"
  fail=$((fail + 1))
fi

echo "RED-RUN fail_steps=${fail} logs=${LOGROOT}"
exit "$fail"
