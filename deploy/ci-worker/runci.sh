#!/usr/bin/env bash
# Omnipus CI worker entrypoint for a single gate run.
# Usage: runci.sh <git-ref> <gate>
#   gate ∈ { all | go-build | go-vet | lint | go-test | go-race | contracts | spa | gofmt | quick | embed-build | e2e }
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

# --- whole-run mutex ------------------------------------------------------
# This worker is SHARED: every operator/session drives the same machine, and a
# run's state is keyed by shard NAME, not by run. Two overlapping runs therefore
# corrupt each other in at least three ways, all observed on 2026-07-26 when a
# `<ref> e2e` run overlapped a `sendfile-fix all` run:
#   1. $REPO_DIR — both hard-reset the SAME checkout to different SHAs, so the
#      loser builds/tests the other run's code. The `HEAD:` line above proves
#      only what was checked out at START, not that it survived the run.
#   2. /tmp/omnipus-ci — the gateway BINARY both e2e gates build to. Rebuilt
#      underneath a run already launching shards from it.
#   3. /tmp/omnipus-e2e-<shard> — each shard `rm -rf`s its OMNIPUS_HOME and
#      seeds a FRESH master key. Doing that under a live gateway from the other
#      run yields exactly `credentials: decryption failed — wrong master key?`
#      → provider injection rejected → `POST /auth/login` 500 → the shard fails
#      at onboarding and reads as a code regression. It is not one.
# Serialising whole runs fixes all three at once, and is far safer than
# per-run path scoping (tests/e2e/setup.ts hardcodes /tmp/omnipus-ci).
# FD 9 is held for the lifetime of this process; the lock releases on exit.
_LOCKFILE=/tmp/runci.lock
exec 9>"$_LOCKFILE" || { echo "cannot open $_LOCKFILE"; exit 2; }
if ! flock -n 9; then
  echo "another runci.sh is already running on this worker (lock: $_LOCKFILE):"
  pgrep -af 'bash /cache/runci.sh' | grep -v "^$$ " || true
  echo "waiting for it to finish (this run will start automatically)…"
  # Bounded wait: better to queue than to silently corrupt both runs. If the
  # holder is wedged, the timeout surfaces it instead of blocking forever.
  if ! flock -w 5400 9; then
    echo "timed out after 90m waiting for the worker lock — is a run wedged?" >&2
    exit 2
  fi
fi
echo "worker lock acquired (pid $$)"
TAGS="goolm,stdjson"
export PATH=/usr/local/go/bin:/cache/go/bin:$PATH
export HOME="${HOME:-/root}"   # non-login SSH shell has no HOME; gen-contracts.sh uses set -u

# Temp lives on the 40G /cache volume, NOT the 7.8G root overlay that / and
# /tmp share. ADR-052's browser tests download Chromium from chrome-for-testing
# into a t.TempDir() (which honours $TMPDIR), and that filled the overlay to
# 0 bytes — go-test then reported "REAL FAILURE (failed twice)" for
# pkg/tools/browser because the flake filter's isolated re-run hit the same
# full disk, not a real defect. Note `df -h /` shows the small overlay, so
# check `df -h /cache` when diagnosing space here.
export TMPDIR=/cache/tmp
mkdir -p "$TMPDIR"

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
  CGO_ENABLED=0 golangci-lint run --build-tags="$TAGS" || return 1
  # #615 regression guard: every pkg/tools/browser real-Chrome test must be
  # gated by the package's own skipIfNoBrowser(t) convention.
  #
  # There is deliberately NO -race package-list lockstep check here any more.
  # That invariant used to be enforced by comparing this file against
  # .github/workflows/pr.yml with a regex scraper, which could not see the
  # drift class most likely to produce a false verdict: this file is executed
  # from /cache/runci.sh on the worker, while the checker read the repo copy.
  # Both surfaces now consume scripts/race-packages.sh — which the worker gets
  # from the checkout — so the lists cannot diverge at all. See run_gorace.
  bash scripts/check-browser-tests-gated.sh || return 1
  # #618 and #617 regression guards, same reasoning: pr.yml runs each in its
  # own job, so omitting either here lets the worker report a green lint while
  # GitHub's is red.
  bash scripts/check-no-handwritten-wire-types.sh || return 1
  bash scripts/check-no-tool-error-from-status.sh || return 1
  # ADR-061 regression guard: the deleted JPEG screencast path must not return.
  bash scripts/check-no-jpeg-screencast.sh || return 1
  # E2E auth cross-talk guard: no spec may POST /api/v1/auth/login (it rotates the
  # single-slot session_token_hash and invalidates the shared storageState cookie
  # for every LATER spec — a failure that lands in an unrelated file). Self-test
  # first (a guard that cannot fail is no guard).
  bash scripts/check-e2e-login-crosstalk.sh --self-test || return 1
  bash scripts/check-e2e-login-crosstalk.sh || return 1
  # ADR-067 SC-008/SC-009/US-11.AC2 regression guard: no alias, migration or
  # deprecation machinery in pkg/providers or pkg/config, no folded-away
  # capabilities package, no bundled SPA catalog. Same reasoning as the guards
  # above: pr.yml runs it in its own step, so omitting it here lets the worker
  # report a green lint while GitHub's is red. Self-test first (a guard that
  # cannot fail is no guard — docs/internal/false-green-patterns.md).
  bash scripts/check-greenfield-providers.sh --self-test || return 1
  bash scripts/check-greenfield-providers.sh || return 1
  # ADR-068 §2.4 regression guard: antigravity / claude-cli / OpenAI device-code
  # flow leave no trace. Self-check first (a guard that cannot fail is no guard).
  bash scripts/check-no-removed-providers-selfcheck.sh || return 1
  bash scripts/check-no-removed-providers.sh
}
# Full suite with a flake filter: a package that fails the contended full run but passes when
# re-run isolated (-p 1) is a timing flake → not a real failure. Fails both = real.
#
# Contention control (2026-07-17): the worker is performance-8x (8 vCPUs). Plain `-p 4` runs 4
# test binaries EACH defaulting to GOMAXPROCS=8 → ~32 scheduler threads oversubscribing 8 cores
# 4:1, starving wall-clock-deadline timing tests (they complete, just late → a shifting random
# subset hits the flake filter each run). Two failure modes must BOTH be avoided:
#   1. cross-process oversubscription (total threads ≫ cores) → scheduling delays; and
#   2. per-test-binary CPU starvation (too few cores for one heavyweight test) → its own
#      internal deadlines blow (e.g. TestCompactionBoundsMemory, a CPU-bound perf test).
# `-p 2` + GOMAXPROCS=4 satisfies both: 2 × 4 = 8 threads ≈ 8 cores (no oversubscription) AND
# each of the 2 concurrent binaries gets 4 cores. (An earlier `-p 4`+GOMAXPROCS=2 attempt fixed
# only #1 and still flaked long CPU-bound tests by throttling them to 2 cores.) Cost: the run
# phase roughly doubles vs -p4 (build is shared) — fine for a pre-merge gate. The isolated
# re-run below is a single process, so it keeps the default (full) GOMAXPROCS to give a flagged
# package maximum CPU when proving pass-vs-real-failure.
# run_gorace — the data-race gate.
#
# This worker previously had NO race gate at all, so every "ALL GATES GREEN"
# verdict it produced carried zero race signal, and the only place races were
# ever detected was a GitHub Actions run — which requires a PR. That made the
# repo's own standard CI surface structurally incapable of catching the defect
# class it most needed to catch.
#
# The package list is NO LONGER copied from .github/workflows/pr.yml — both
# surfaces now consume scripts/race-packages.sh, the single source of truth,
# on the scripts/e2e-shards.sh precedent. Edit that file, not this one. The
# timeout, CGO_ENABLED=1 and the DATA RACE carve-out are still deliberately
# mirrored from pr.yml's "Run go test -race" step; keep those in step by hand.
#
# pkg/task, pkg/plan, pkg/tools and pkg/providers joined the list on
# 2026-08-10 (both files together, as the rule below demands) after a manual
# scoped run found TWO data races in them on an otherwise fully green commit.
#
# pkg/tools/browser joined the recursive glob on 2026-08-11 (#615). It was
# originally left off `./pkg/tools/...` on the theory that the package
# "launches real Chrome and hits missing dbus headlessly" — that diagnosis
# was WRONG: the package already ran its real-Chrome tests and PASSED in the
# plain (non-race) `go test ./...` gate (run_gotest) on these same runners,
# so dbus availability cannot explain a race-only exclusion. The real cause
# was that its 15 real-Chrome tests (plus two more found ungated entirely
# during the #615 fix — pkg/tools/browser/coordinator_test.go's
# TestCoordinator_OwnershipMarker_RoundTrip and
# coordinator_window_size_test.go's
# TestCoordinator_Register_CreateTargetParams_PinsWindowSize) were only
# gated by `testing.Short()` (or, for those two, not gated at all) instead
# of the package's own `skipIfNoBrowser` convention, and two tight
# wall-clock assertions in coldstart_bound_test.go were exactly the shape
# pkg/task hit above (race instrumentation blowing a <200ms bound). Both are
# fixed: every real-Chrome test now uses skipIfNoBrowser (which also removed
# an undeclared ~100MB Chrome-for-Testing network download from every CI
# gate that reached these tests), and the two wall-clock assertions now use
# a //go:build race / !race split bound (runFirstAttachPromptBound), exactly
# like pkg/task's livenessBound above.
#
# A worker gate that measures something GitHub does not (or vice versa) is
# exactly how a green local verdict stops predicting the real one — which is
# why the package list is now shared rather than duplicated. -race forces cgo,
# which is why CGO_ENABLED=1 here does not weaken the pure-Go guarantee — that
# is proved by the CGO_ENABLED=0 build/test gates, not by this one.
run_gorace() {
  ensure_spa_stub
  local out
  # CI=true is REQUIRED here, and is the single thing that makes this gate
  # predict GitHub's. GitHub Actions always sets CI, so pkg/tools/browser's
  # skipIfNoBrowser(t) skips every real-Chrome test in GitHub's -race step.
  # This worker is a plain SSH shell with no CI in the environment, so without
  # this line the SAME command runs ~58 real-Chrome e2e tests under -race here
  # and zero of them there.
  #
  # That is not a theoretical divergence — it is what #615's glob widening
  # actually produced on this worker: race instrumentation plus a shared real
  # Chrome made a DIFFERENT handful of browser e2e tests fail on each run
  # (execute/inspect/text-selector), while GitHub's identical step was
  # unaffected. Real-Chrome coverage belongs to the dedicated browser-e2e job
  # (.github/workflows/pr.yml), which runs WITHOUT -race and with Chrome as a
  # declared dependency. This gate's job is the concurrency logic.
  #
  # Contention control, same rationale and same fix as run_gotest above (this
  # worker is performance-8x, 8 vCPUs): plain `go test -race` with no -p/
  # GOMAXPROCS override runs one binary per package at DEFAULT GOMAXPROCS (=
  # NumCPU, 8 here), so N concurrent race binaries oversubscribe 8 cores N:1 —
  # -race's own instrumentation overhead (2-20x CPU) makes that oversubscription
  # bite harder here than in the plain-test case run_gotest already fixed, and
  # starves exactly the wall-clock-deadline tests run_gotest's comment
  # describes (found empirically: TestValidateCLI_BareNameResolvedViaPATH's
  # 15s CLI-handshake probe timeout blown at 16.18s under an untuned, fully
  # concurrent race-packages.sh run — a real, confirmed CI-throttling gap, not
  # an application bug). `-p 2` + GOMAXPROCS=4 mirrors run_gotest's own fix:
  # 2 × 4 = 8 threads ≈ 8 cores (no oversubscription), 4 cores per binary.
  # Deliberately NOT mirrored to pr.yml's "Run go test -race" step: that job
  # runs on ubuntu-latest's smaller default runners, a different oversubscription
  # regime, exactly like run_gotest's own -p2/GOMAXPROCS=4 is worker-only and
  # already not mirrored there either — see this file's redeploy note on
  # keeping detection semantics (timeout, CGO_ENABLED, DATA RACE carve-out) in
  # lockstep with pr.yml, which concurrency-only scheduling knobs are not part
  # of.
  #
  # shellcheck disable=SC2046 — intentional word-splitting: race-packages.sh
  # emits a space-separated package list that must expand to separate args.
  out=$(CI=true GOMAXPROCS=4 CGO_ENABLED=1 go test -race -tags "$TAGS" -count=1 -p 2 -timeout 900s \
    $(scripts/race-packages.sh) 2>&1)
  local code=$?
  echo "$out"
  # Checked BEFORE the exit-code short-circuit, for the same reason pr.yml and
  # run_gotest do it: a race can be reported without flipping go test's exit
  # code. A detected race is NEVER routed through a flake filter or given an
  # isolated second chance — a race that appears under real parallelism and
  # vanishes at -p 1 is the textbook signature of a genuine concurrency bug.
  if [[ $out == *"DATA RACE"* ]]; then
    echo ""
    echo "=== DATA RACE detected — skipping flake filter entirely ==="
    echo "$out" | grep -aE '^FAIL[[:space:]]|DATA RACE' | head -20
    return 1
  fi
  [ $code -eq 0 ] && return 0

  # Flake filter — ALSO copied from pr.yml's race step, and required for
  # parity. Without it this gate is STRICTER than GitHub's, so a PR that is
  # green upstream can show red here purely on a timing flake, and a
  # false-RED gate gets ignored, which is worse than no gate.
  #
  # It reached that state once: the first race run on this worker failed on
  # `TempDir RemoveAll cleanup: directory not empty` in pkg/gateway — a test
  # TEARDOWN race (a background goroutine still writing after the test body
  # returned, a window -race widens), not a data race. It passed isolated,
  # and GitHub would have excused it.
  #
  # Note this filter can NEVER excuse a real race: the DATA RACE carve-out
  # above returns before reaching here.
  local failed
  failed=$(echo "$out" | grep -aE '^FAIL[[:space:]]' | awk '{print $2}' | grep -a '/' | sort -u)
  [ -z "$failed" ] && return $code
  echo ""
  echo "=== FLAKE FILTER (-race): re-running failed packages isolated (-p 1): $failed ==="
  local rc=0
  for p in $failed; do
    # The re-run is checked for DATA RACE too, not just its exit code.
    #
    # The carve-out above exists because "a race can be reported without
    # flipping go test's exit code". That is equally true of the isolated
    # re-run — so testing only the exit code here would let a race reported
    # in the SECOND run be stamped "FLAKE (passed isolated)" and excused,
    # which is precisely what the carve-out forbids. pr.yml has the same
    # hole; fix both together or the gates diverge.
    #
    # Timeout is 900s, matching BOTH the contended run above and pr.yml's
    # isolated re-run. It was 600s, which had the asymmetry backwards: the
    # isolated -p 1 re-run is SLOWER than the contended one (no parallelism),
    # yet got less time. With ./pkg/tools/... now in the glob, a slow package
    # that trips the flake filter could time out at 600s and be stamped
    # "REAL FAILURE (failed twice)" — a false RED that reads as a code defect.
    # CI=true for the same reason as the contended run above: the isolated
    # re-run must measure the SAME thing, or a package that only "fails" here
    # because it launched a real Chrome would be re-run without one and
    # stamped a flake — or vice versa.
    if CI=true CGO_ENABLED=1 go test -race -tags "$TAGS" -count=1 -timeout 900s -p 1 "$p" >"/tmp/rr_race_$(echo "$p" | tr '/' '_').log" 2>&1 \
       && ! grep -aq "DATA RACE" "/tmp/rr_race_$(echo "$p" | tr '/' '_').log"; then
      echo "FLAKE (passed isolated): $p"
      echo "  contended-run failures (each is a REAL BUG that has not been diagnosed yet):"
      grep -aoE '^\s*--- FAIL: [A-Za-z0-9_/]+' <<<"$out" | awk '{print $3}' | sort -u | sed 's/^/    /'
    else
      echo "REAL FAILURE (failed twice): $p"
      # Per-package log: a single shared path was overwritten each iteration,
      # so on a multi-package failure only the last package's output survived
      # for post-mortem.
      grep -aE '^--- FAIL|DATA RACE' "/tmp/rr_race_$(echo "$p" | tr '/' '_').log" | head
      rc=1
    fi
  done
  return $rc
}

run_gotest() {
  ensure_spa_stub
  # CI=true for the same reason run_gorace sets it: this gate exists to predict
  # GitHub's "Tests" job, and GitHub always sets CI. After #615 converted
  # pkg/tools/browser's real-Chrome tests to the package's own skipIfNoBrowser(t)
  # convention, those tests SKIP in GitHub's Tests job — their coverage moved to
  # the dedicated browser-e2e job, which provisions Chrome as a declared
  # dependency and runs without -race.
  #
  # This worker is a plain SSH shell with no CI set, so without this line it runs
  # ~58 real-Chrome e2e tests that GitHub's Tests job no longer runs at all. This
  # box has no dbus and a slow shared Chrome, so they fail on the environment
  # rather than on the code: the observed failure was
  #   "post-redirect SSRF error must mention redirect/blocked/SSRF/169.254;
  #    got: browser_navigate: page load failed: context deadline exceeded"
  # — a navigation timeout, not a broken SSRF guard. The flake filter correctly
  # refused to excuse it (it failed both runs), which is exactly why the gate
  # must not measure something GitHub does not.
  local out; out=$(CI=true GOMAXPROCS=4 CGO_ENABLED=0 go test -tags "$TAGS" -count=1 -p 2 ./... 2>&1)
  local code=$?
  echo "$out"
  # DATA RACE carve-out — checked BEFORE the exit-code short-circuit, because a
  # race can be reported without flipping the overall `go test` exit code (a test
  # that shells out with -race and only reads the child's stdout, for example).
  # A race that vanishes under the isolated `-p 1` re-run is the TEXTBOOK
  # signature of a real concurrency bug, not a flake, so it is never excused.
  # Native bash substring match, NOT `echo | grep -q`: grep -q exits on the first
  # match and closes the pipe, echo dies of SIGPIPE, and under `set -o pipefail`
  # the pipeline status becomes 141 — the test would silently evaluate false on
  # any multi-MB log. (Mirrors the same guard in .github/workflows/pr.yml.)
  if [[ $out == *"DATA RACE"* ]]; then
    echo ""
    echo "=== DATA RACE detected — never excused by an isolated re-run ==="
    echo "$out" | grep -aE '^FAIL[[:space:]]|DATA RACE' | head -20
    return 1
  fi
  [ $code -eq 0 ] && return 0
  local failed; failed=$(echo "$out" | grep -aE '^FAIL[[:space:]]' | awk '{print $2}' | grep -a '/' | sort -u)
  [ -z "$failed" ] && return $code
  echo ""; echo "=== FLAKE FILTER: re-running failed packages isolated (-p 1): $failed ==="
  local rc=0
  for p in $failed; do
    # Which TESTS failed the contended run, for this package only. Go prefixes
    # nothing package-scoped onto "--- FAIL:" lines, so scope by taking the
    # slice of $out between this package's first failure and its "^FAIL <pkg>"
    # summary line; simpler and good enough: collect all contended failures once
    # and intersect per package below (a test name is unique enough in practice).
    local run1; run1=$(echo "$out" | grep -aoE '^\s*--- FAIL: [A-Za-z0-9_/]+' | awk '{print $3}' | sort -u)
    # CI=true here too: the isolated re-run must measure the same thing as the
    # contended run, or a package that only failed because it launched a real
    # Chrome would be re-run without one and stamped a flake (or vice versa).
    if CI=true CGO_ENABLED=0 go test -tags "$TAGS" -count=1 -p 1 "$p" >/tmp/rr.log 2>&1; then
      echo "FLAKE (passed isolated): $p"
      echo "  contended-run failures (each one is a REAL BUG that has not been diagnosed yet):"
      echo "$run1" | sed 's/^/    /'
    else
      local run2; run2=$(grep -aoE '^\s*--- FAIL: [A-Za-z0-9_/]+' /tmp/rr.log | awk '{print $3}' | sort -u)
      local both; both=$(comm -12 <(echo "$run1") <(echo "$run2"))
      if [ -n "$both" ]; then
        echo "REAL FAILURE (same test failed BOTH runs): $p"
        echo "$both" | sed 's/^/    /'
      else
        # Both runs failed, but on DIFFERENT tests. That is two independent
        # flakes, NOT one deterministic failure — the old code called this
        # "failed twice" and sent an investigation chasing a regression that
        # did not exist. Still a gate failure; just labelled honestly.
        echo "GATE FAILURE (different tests failed each run — two independent flakes, not one deterministic failure): $p"
        echo "  contended run:"; echo "$run1" | sed 's/^/    /'
        echo "  isolated run:";  echo "$run2" | sed 's/^/    /'
      fi
      # Full assertion text, not just the "--- FAIL" header. The header alone
      # discards the indented failure message, which is the only thing that
      # makes a failure diagnosable from CI output.
      echo "  --- isolated-run detail ---"
      grep -aA 12 -E '^\s*--- FAIL' /tmp/rr.log | head -120
      rc=1
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
#   E2E_MAX_PARALLEL  max shards in flight at once (default 2). The 8-core worker
#                  is oversubscribed by 5 concurrent gateways+browsers, which
#                  starves CPU and flakes timing-sensitive specs; 2 keeps each
#                  shard's headroom while staying well under the serial runtime.
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
  #
  # Use the REPO-LOCAL playwright, never bare `npx` — the image bakes a pinned GLOBAL
  # playwright at /usr/bin/playwright (Dockerfile: npm install -g @playwright/test@1.49.0).
  # `npx playwright` can resolve that global binary, which installs the revision IT wants
  # and exits 0, leaving the revision the test runner actually needs absent. That is not
  # hypothetical: on 2026-07-26 the image held chromium 1148 (global 1.49.0) + 1228 while
  # the local runner wanted 1223, and the whole e2e gate reported 48 phantom "failures"
  # across 5 shards — every one of them `browserType.launch: Executable doesn't exist`,
  # each "failing" in 4-6ms because no browser ever started. Infra noise indistinguishable
  # from a real regression at a glance.
  log "e2e: install matching chromium"
  local pw=./node_modules/.bin/playwright
  [ -x "$pw" ] || { echo "e2e: $pw missing or not executable — npm ci must run first" >&2; return 1; }
  # chromium_headless_shell is a SEPARATE download from chromium; the suite launches it
  # directly, so installing only `chromium` leaves the headless path broken.
  "$pw" install chromium chromium-headless-shell || return 1

  # A zero exit above is NOT proof the right browser landed — installing the WRONG
  # revision also exits 0. Verify the exact revision this runner resolves is on disk,
  # and fail loudly (naming the path) rather than handing the shards a broken browser.
  node -e '
    const fs = require("fs"), path = require("path");
    const root = process.env.PLAYWRIGHT_BROWSERS_PATH || "";
    const want = require("./node_modules/playwright-core/browsers.json").browsers
      .filter(b => b.name === "chromium" || b.name === "chromium-headless-shell");
    let bad = 0;
    for (const b of want) {
      const dir = path.join(root, `${b.name.replace(/-/g, "_")}-${b.revision}`);
      if (!fs.existsSync(dir)) { console.error(`MISSING ${b.name} rev ${b.revision} at ${dir}`); bad++; }
      else console.log(`ok ${b.name} rev ${b.revision}`);
    }
    process.exit(bad ? 1 : 0);
  ' || { echo "e2e: required browser revision absent after install — see MISSING above" >&2; return 1; }
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
  #
  # SELF-DISARMING (`trap - RETURN` as the handler's last act), and every local read
  # defensively defaulted. A `trap … RETURN` is GLOBAL shell state, not function-local:
  # installing it here leaves it installed after _e2e_run_shard returns, so it fires
  # AGAIN for whichever function returns next. On the sharded path each _e2e_run_shard
  # runs in a background subshell, so the trap dies with the subshell and the bug is
  # invisible. On the SINGLE-GATEWAY path (E2E_SPECS=… or E2E_SHARDED=0) the call is
  # in-process, so the trap survived into run_e2e and fired on ITS return — by which
  # point GATEWAY_PID/pidfile were out of scope, and `set -u` turned that into
  # "GATEWAY_PID: unbound variable", exit 1, AFTER the results had already printed.
  # That manufactured a false RED on the exact path used for targeted re-verification.
  trap 'if [ -n "${GATEWAY_PID:-}" ]; then kill "$GATEWAY_PID" 2>/dev/null; wait "$GATEWAY_PID" 2>/dev/null; fi; [ -n "${pidfile:-}" ] && rm -f "$pidfile"; trap - RETURN' RETURN

  rm -rf "$home"; mkdir -p "$home"

  # Canonical e2e config with THIS shard's port. Each shard has its own credentials.json
  # under $home, so the shared api_key_ref name ("OPENROUTER_API_KEY") resolves to this
  # shard's key value (seeded next). See pr.yml "Seed gateway config" for the rationale
  # behind dev_mode_bypass / audit_log.
  #
  # C1 fix (ADR-067 FR-034): providers[] rows are keyed by the EXACT (provider, model)
  # pair — `provider` is the catalog id ("openrouter"), `model` is the BARE catalog
  # model id ("z-ai/glm-5.2"). The old "<protocol>/<model>" prefix-splitting migration
  # was deleted deliberately (it silently mis-routed vendors), so a row with an empty
  # `provider` now fails ModelConfig.Validate ("provider is required") instead of being
  # guessed. agents.defaults.default_model is the (provider, model) pair — the retired
  # model_name alias is gone (ADR-068 CRIT-001).
  cat > "$home/config.json" <<EOF
{
  "version": 1,
  "gateway": { "port": $port, "dev_mode_bypass": true },
  "sandbox": { "audit_log": true, "tool_policies": { "spawn": "allow" } },
  "agents": { "defaults": { "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" }, "auto_recap_enabled": true } },
  "providers": [
    {
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
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
    '{provider:{auth_method:"api_key",id:"openrouter",api_key:$key,model:"z-ai/glm-5.2"},admin:{username:"admin",password:"admin123"}}' \
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

  # Bounded concurrency. Running all 5 shards at once (5 gateways + 5 chromium)
  # oversubscribes the 8-core worker; the resulting CPU starvation makes
  # timing-sensitive specs flake (proven: a single-gateway re-run passed 16/17
  # specs that the 5-wide run failed). Cap the in-flight shards so each gets
  # real CPU headroom. LLM shards are mostly I/O-bound (waiting on OpenRouter),
  # so 2 concurrent keeps wall-clock well under the old serial run while giving
  # the render-bound shards (ui, stubs) room. Override with E2E_MAX_PARALLEL.
  local MAX_PARALLEL="${E2E_MAX_PARALLEL:-2}"
  local -a NAMES=() FAILED=()
  local -A PID2NAME=()
  local group slot port specs key
  local running=0 shard_rc=0

  # Reap exactly one finished shard: wait for ANY tracked background shard,
  # attribute its exit code to the right shard name via PID2NAME, record
  # PASS/FAIL. `wait -n -p` (bash >= 5.1; worker is 5.2) reports WHICH job
  # finished. Decrements `running`. Relies on bash dynamic scope to mutate
  # run_e2e's locals (running/PID2NAME/FAILED/shard_rc).
  _e2e_reap_one() {
    local finished_pid='' code name
    wait -n -p finished_pid "${!PID2NAME[@]}"; code=$?
    if [ -z "$finished_pid" ]; then
      # No tracked child was reaped (all already gone) — clear bookkeeping so
      # the drain loop can't spin forever on a phantom count.
      running=0; PID2NAME=(); return 0
    fi
    name="${PID2NAME[$finished_pid]:-?}"
    unset 'PID2NAME[$finished_pid]'
    running=$((running - 1))
    if [ "$code" -eq 0 ]; then
      printf '\033[1;32me2e shard %s: PASS\033[0m\n' "$name"
    else
      printf '\033[1;31me2e shard %s: FAIL (exit %d)\033[0m\n' "$name" "$code"
      FAILED+=("$name"); shard_rc=1
    fi
  }

  # Reap surviving gateways if the whole run is interrupted (exact pids only).
  trap '_e2e_reap_pidfiles' INT TERM

  local soloflag src
  while IFS=$'\t' read -r group slot port soloflag specs; do
    [ -n "$group" ] || continue
    case "$slot" in b) key="$KEY_B" ;; c) key="$KEY_C" ;; *) key="$KEY_A" ;; esac
    if [ "$soloflag" = "true" ]; then
      # Solo shard: drain every in-flight shard, then run THIS one alone in the
      # foreground so its timing-sensitive specs get the whole worker (no CPU
      # contention from a co-tenant gateway+browser — the failure mode that
      # flaked ui specs at MAX_PARALLEL=2 when they overlapped a CPU-heavy shard).
      while [ "$running" -gt 0 ]; do _e2e_reap_one; done
      log "e2e: launch shard $group (port $port, key slot $slot; SOLO)"
      ( _e2e_run_shard "$group" "$port" "$key" "$specs" "--output=/tmp/e2e-$group-results --reporter=list" ) \
        > "/tmp/e2e-shard-$group.log" 2>&1
      src=$?
      NAMES+=("$group")
      if [ "$src" -eq 0 ]; then
        printf '\033[1;32me2e shard %s: PASS\033[0m\n' "$group"
      else
        printf '\033[1;31me2e shard %s: FAIL (exit %d)\033[0m\n' "$group" "$src"
        FAILED+=("$group"); shard_rc=1
      fi
      continue
    fi
    # Concurrency gate: block until a slot frees up.
    while [ "$running" -ge "$MAX_PARALLEL" ]; do _e2e_reap_one; done
    log "e2e: launch shard $group (port $port, key slot $slot; $((running + 1))/$MAX_PARALLEL in flight)"
    ( _e2e_run_shard "$group" "$port" "$key" "$specs" "--output=/tmp/e2e-$group-results --reporter=list" ) \
      > "/tmp/e2e-shard-$group.log" 2>&1 &
    PID2NAME[$!]="$group"; NAMES+=("$group"); running=$((running + 1))
  done < <(scripts/e2e-shards.sh list 2>/dev/null)

  [ "${#NAMES[@]}" -gt 0 ] || { echo "e2e: no shards launched (empty shard plan?)" >&2; return 1; }

  # Drain the remaining in-flight shards.
  while [ "$running" -gt 0 ]; do _e2e_reap_one; done
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
  go-race)         step go-race run_gorace ;;
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
    step go-race run_gorace
    step e2e run_e2e
    ;;
  *) echo "unknown gate: $GATE"; exit 64 ;;
esac

log "RESULT"; [ $rc -eq 0 ] && echo "ALL GATES GREEN" || echo "GATE FAILURE(S) — see above"
exit $rc
