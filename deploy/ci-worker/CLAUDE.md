# CLAUDE.md — CI worker (`ci-omnipus`)

Scoped guidance for the `deploy/ci-worker/` directory. Loaded automatically by Claude Code
whenever a file in this directory (or a descendant) is read. See the root `CLAUDE.md`'s
"Local PR-runner" pointer for the short version.

## ⚠️ "ALL GATES GREEN" from this worker does NOT mean races were checked

The worker runs a FASTER SUBSET of GitHub CI. Two limits that have already caused a
false sense of completeness (2026-07-26 — a green worker verdict was reported upstream
as if it were a full pass):

1. ~~**There is no `-race` gate here at all.**~~ **Fixed 2026-08-10 — there is now a `go-race`
   gate**, and it is included in `all`. It copies pr.yml's package list, `-timeout 900s`,
   `CGO_ENABLED=1` and DATA RACE carve-out verbatim; keep the two in lockstep, because a worker
   gate that measures something GitHub does not is how a green local verdict stops predicting the
   real one — including pr.yml's **flake filter**, which the gate initially shipped without and which
   made it stricter than GitHub (a PR green upstream could show red here on a timing flake; a
   false-RED gate gets ignored, which is worse than no gate). The filter can never excuse a real race: the carve-out returns before it, AND the
   isolated re-run is itself grepped for `DATA RACE` (checking only its exit code would
   let a race reported in the second run be stamped `FLAKE (passed isolated)` — the
   carve-out's own rationale is that a race can be reported without flipping the exit
   code). The two package lists can no longer diverge, because there is only one: both
   surfaces consume `scripts/race-packages.sh` (the `scripts/e2e-shards.sh` precedent —
   one file, both consumers). Edit that script, never the invocations. This replaced an
   earlier `check-race-package-lockstep.sh` comparator, which could not see the drift class
   that matters most here: it read the repo copy of `runci.sh`, while the worker executes
   `/cache/runci.sh`. The `lint` gate here and pr.yml's guard job still run
   `scripts/check-browser-tests-gated.sh`, which fails if any pkg/tools/browser test
   launches real Chrome without going through the package's own `skipIfNoBrowser(t)`
   convention.
   `pkg/tools`/`pkg/providers` joined the list 2026-08-10, and `pkg/tools/browser` (previously
   excluded — see the `go-race` package-list comment in this file's `run_gorace` for the full
   #615 history: the "launches real Chrome, hits missing dbus" exclusion rationale was wrong,
   the real cause was 17 real-Chrome tests not following the package's own `skipIfNoBrowser`
   convention plus two tight wall-clock assertions) joined 2026-08-11, so `./pkg/tools/...` is
   now fully recursive in both files — nothing under `pkg/tools` or `pkg/providers` is excluded
   from either race surface any more.
   **Two limits remain:**
   (a) `go-test` and `go-race` are separate gates, so running `go-test` alone still carries
   zero race signal — use `all` or run `go-race` explicitly.
   (b) **This worker runs NO real-Chrome tests, in any gate.** Both `run_gotest` and
   `run_gorace` set `CI=true`, which makes `pkg/tools/browser`'s `skipIfNoBrowser(t)` skip
   every test that would launch a browser. That is deliberate and it is what GitHub does —
   GitHub always sets `CI`, so its `Tests` and `-race` steps skip them too. Their coverage
   lives in GitHub's dedicated **`browser-e2e`** job (`.github/workflows/pr.yml`), which sets
   `OMNIPUS_BROWSER_E2E=1`, provisions Chrome via an action, runs WITHOUT `-race`, and fails
   loudly if the tests skip. **There is no `browser-e2e` equivalent here**, so an `all`-green
   verdict on this worker carries zero real-Chrome signal — read GitHub for that. Forcing
   them on here is not the answer: this box has no dbus and a slow shared Chrome, so they
   fail on the environment (`page load failed: context deadline exceeded`) rather than on
   the code, which is precisely how a false RED trains people to ignore a gate.
2. **`run_gotest` excuses flakes.** A package that fails the parallel run but passes the
   isolated `-p 1` re-run prints `FLAKE (passed isolated)` and the gate still returns 0.
   That is intentional for timing-sensitive integration tests — but it means a green
   verdict can contain an absorbed failure. **Read the log for `FLAKE (passed isolated)`
   before treating a green as clean.** (This is exactly how a real `pkg/agent` failure
   was absorbed on 2026-07-26 and reported upstream as an unqualified pass.)

   A detected `DATA RACE` is now carved out and can never be flake-excused (mirrors the
   guard in `pr.yml`) — but since nothing here runs with `-race`, that carve-out only
   fires if a race is reported by a test that shells out with `-race` itself.

**Bottom line:** treat this worker as a fast pre-merge smoke gate, not as the authority on
concurrency. For race coverage, push and read GitHub CI.

## Local PR-runner (ci-omnipus Fly worker)

The Go test/build suite is run on a dedicated Fly worker, **never in the dev pod** (linking the full `pkg/gateway` test binary with the pure-Go OLM crypto via the `goolm` tag OOMs the pod — see the root CLAUDE.md's "Testing & building — CI is the authority" section). The worker is a sized, on-demand box with persistent caches, driven via `flyctl ssh console`.

- **App**: `ci-omnipus` (`sin` region, `performance-8x/16GB`, persistent `/cache` volume for go-build/mod cache, npm cache, and the cloned repo).
- **Source of truth**: `deploy/ci-worker/runci.sh` (in this repo) **and** the deployed copy at `/cache/runci.sh` on the worker. **Editing the repo file does NOT update the executing copy** — see "Redeploying runci.sh" below.
- **Trigger** (one gate at a time):
  ```bash
  fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> <gate>"
  ```
- **Gates**: `all | go-build | go-vet | go-test | contracts | spa | gofmt | quick | embed-build | e2e`. `go-test` includes a flake filter (a package failing the contended `-p4` full run is re-run isolated `-p 1`; "failed twice = REAL FAILURE"). `e2e` runs the full Playwright matrix (40 specs, ~20–30 min) — see "E2E gate" below.
- **When to use it**:
  - Pre-push verification on a feature branch **before** opening a PR (when you want a signal without burning a PR slot).
  - Pre-merge gate on a hotfix / release branch (faster turnaround than the public PR workflow).
  - Any time the dev pod can't run the suite (OOM, RAM pressure, root disk > 90%).

**Three false-signal traps — read before trusting a verdict** (the first two hit during the
v0.1.0 epic, 2026-06-14; the third on 2026-07-26):

1. **Stale-checkout false-RED.** A bare `git checkout -f <branch>` switches to the *local* branch, which `git fetch` does NOT fast-forward; `reset --hard <branch>` then resets to that stale local commit, silently testing yesterday's code. Symptom: pushed fixes "don't take" — the same tests keep failing across runs. **Fix in runci.sh**: `TARGET="$(git rev-parse --verify --quiet "origin/$REF^{commit}" || git rev-parse --verify --quiet "$REF^{commit}")"`; checkout/reset to that SHA. **Always confirm the `HEAD: <sha> <subject>` line near the top of the run matches the commit you pushed** before trusting the verdict.
2. **Wrapper-exit-code false-GREEN.** `flyctl ssh console -C "…"` and a background-task `<status>completed exit 0</status>` report the **SSH wrapper's** exit code, NOT runci.sh's. A failing gate can still show "exit 0". **Always parse the output** for `GATE FAILURE(S)` / `REAL FAILURE` / `go-test -> exit 1` / the final `RESULT: …` line, never trust the task-completion exit code. Related: the SSH transport itself can drop mid-run (especially during the long e2e gate) even though the actual test process keeps running to completion on the remote worker — if the log goes stale with no final RESULT line, reconnect and check `ps aux | grep -E 'playwright test|omnipus-ci'` on the worker before assuming failure; do NOT `pkill -f` anything with a pattern that could match your own shell's command line (self-kill risk — use exact PIDs).

3. **Missing-browser false-RED (e2e).** A Playwright browser-revision mismatch makes the
   ENTIRE e2e gate fail in a way that reads like a mass code regression. Symptom to
   recognise instantly: **every test fails in 4–6 ms, including all retries**, across
   specs that share nothing (fonts, providers, channel routing, whatsapp), with
   `browserType.launch: Executable doesn't exist at …`. A real assertion failure cannot
   complete in 4 ms — that timing means no browser ever started. On 2026-07-26 this
   produced "48 failed" across 5 shards on a commit whose every other gate (gofmt,
   go-build, go-vet, golangci-lint, verify-contracts, typecheck, vitest, **go-test**) was
   green. Cause: the image bakes a pinned GLOBAL playwright (`@playwright/test@1.49.0` →
   chromium 1148) and `_e2e_build` used bare `npx playwright install chromium`, which can
   resolve that global binary, install ITS revision, and exit 0 — leaving the revision the
   repo-local runner actually wants (1223) absent. **`|| return 1` cannot catch this:
   installing the wrong revision also exits 0.** Fixed by installing via
   `./node_modules/.bin/playwright` explicitly, adding the separately-downloaded
   `chromium-headless-shell`, and asserting the resolved revisions exist on disk after the
   install. If the e2e gate ever goes broadly red again, check the failure DURATIONS
   before reading it as a regression.

**E2E gate (Playwright).** The `e2e` gate (and the `e2e` step inside `all`) builds the SPA + gateway binary once, then **fans the Playwright suite out across the shards defined in `tests/e2e/shards.json`** — the SAME plan `.github/workflows/pr.yml` uses (both consume `scripts/e2e-shards.sh`), so the two CI surfaces can never drift. Each shard boots its OWN isolated gateway (own port `6060`–`6064`, own `OMNIPUS_HOME`, own `credentials.json`, own auth + skip-manifest files), completes onboarding via the public API, runs its slice of the matrix with `--reporter=list --output=/tmp/e2e-<shard>-results`, and is torn down via a per-shard `trap RETURN`. A whole-run interrupt reaps survivors by **exact pid** from `/tmp/e2e-shard-*.gwpid` — never pkill-by-pattern (self-kill risk). Because the shards run concurrently, wall-clock drops from the old ~3 h single serial run to roughly the slowest single shard (~15 min). `scripts/e2e-shards.sh check` runs first and **FAILS the gate** if any `tests/e2e/*.spec.ts` is unassigned (it would silently never run) or the plan references a deleted spec. Per-shard PASS/FAIL is printed at the end, and each failing shard's full log (`/tmp/e2e-shard-<name>.log`) is dumped.

  **Env knobs:** `E2E_SPECS="<space-separated specs>"` runs the OLD single-gateway path for a fast targeted re-verify (keeps the HTML report); `E2E_SHARDED=0` forces the single-gateway path for the full matrix.

  **Fly secrets** (set as `fly secrets set … --app ci-omnipus`, never commit the value):
  - `OPENROUTER_API_KEY` — **required**; preflight-checked by `runci.sh` (`${OPENROUTER_API_KEY:?…}`); the gate fails fast with a clear error if it's missing. Every shard uses it (key slot `a`) unless a slot-specific key is set.
  - `OPENROUTER_API_KEY_B`, `OPENROUTER_API_KEY_C` — **optional**; give the concurrent LLM shards (`llm-agents`→B, `llm-light`→C) their own OpenRouter rate-limit windows, removing cross-shard 429 contention. Unset ⇒ those shards fall back to `OPENROUTER_API_KEY`, so the gate still runs (at single-key parallelism).

**Redeploying runci.sh** (after editing the repo file — the worker keeps using its cached copy):
```bash
# 1. base64 the local file and pipe it through SSH
base64 deploy/ci-worker/runci.sh | fly ssh console --app ci-omnipus -C 'cat > /tmp/runci.sh.b64'
# 2. decode + install on the worker
fly ssh console --app ci-omnipus -C 'base64 -d /tmp/runci.sh.b64 > /cache/runci.sh && chmod +x /cache/runci.sh && md5sum /cache/runci.sh'
# 3. confirm md5 matches `md5sum deploy/ci-worker/runci.sh` locally
# NB: redeploying runci.sh alone is enough for a script-only change. For a Dockerfile change
# (e.g. new Playwright browser, new system dep), you must `fly deploy --app ci-omnipus` to
# rebuild the image — `runci.sh` lives at /usr/local/bin/runci.sh inside the image.
```

**⚠️ The worker is SHARED — runs are now serialised by a mutex (`/tmp/runci.lock`).**
Every session drives the same machine, and run state is keyed by shard NAME, not by run,
so two overlapping runs corrupt each other three ways (all observed 2026-07-26, when a
`/home/dev/omnipus3` `e2e` run overlapped a `/home/dev/omnipus` `all` run on `sendfile-fix`):

1. **`/cache/omnipus`** — both hard-reset the same checkout to different SHAs; the loser
   builds and tests the other run's code. The `HEAD:` line proves what was checked out **at
   start**, not that it survived.
2. **`/tmp/omnipus-ci`** — the gateway binary both e2e gates build to, rebuilt underneath a
   run that is already launching shards from it.
3. **`/tmp/omnipus-e2e-<shard>`** — each shard `rm -rf`s its `OMNIPUS_HOME` and seeds a
   FRESH master key. Doing that under the other run's live gateway produces
   `credentials: decryption failed — wrong master key?` → provider injection rejected →
   `POST /auth/login` 500 → the shard dies at onboarding. **This reads exactly like a code
   regression and is not one** — it cost a full misread of three LLM shards.

`runci.sh` now takes an exclusive `flock` on `/tmp/runci.lock` for the whole run: a second
invocation prints the holder, waits (up to 90 min), then runs automatically. So you no
longer need to pre-check — but if a verdict looks impossible, confirm your run wasn't
queued behind or overlapping another. Never kill another operator's run, and never
pattern-kill (self-kill risk, above). **Redeploying `/cache/runci.sh` while a run is in
flight still mutates the script underneath it** — the lock does not protect against that,
so hold redeploys until the worker is idle.

**Cost / lifecycle**: the worker is stopped when idle (no public service). If `fly status` shows the machine `stopped`, run `fly machines start <id> --app ci-omnipus` once before invoking `runci.sh`; the SSH console will auto-start it otherwise. Watch the persistent `/cache` volume for disk pressure — `fly ssh console --app ci-omnipus -C 'df -h /cache'`.
