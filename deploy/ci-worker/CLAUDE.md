# CLAUDE.md — CI worker (`ci-omnipus`)

Scoped guidance for the `deploy/ci-worker/` directory. Loaded automatically by Claude Code
whenever a file in this directory (or a descendant) is read. See the root `CLAUDE.md`'s
"Local PR-runner" pointer for the short version.

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

**Two false-signal traps (both hit during the v0.1.0 epic, 2026-06-14) — read before trusting a verdict:**

1. **Stale-checkout false-RED.** A bare `git checkout -f <branch>` switches to the *local* branch, which `git fetch` does NOT fast-forward; `reset --hard <branch>` then resets to that stale local commit, silently testing yesterday's code. Symptom: pushed fixes "don't take" — the same tests keep failing across runs. **Fix in runci.sh**: `TARGET="$(git rev-parse --verify --quiet "origin/$REF^{commit}" || git rev-parse --verify --quiet "$REF^{commit}")"`; checkout/reset to that SHA. **Always confirm the `HEAD: <sha> <subject>` line near the top of the run matches the commit you pushed** before trusting the verdict.
2. **Wrapper-exit-code false-GREEN.** `flyctl ssh console -C "…"` and a background-task `<status>completed exit 0</status>` report the **SSH wrapper's** exit code, NOT runci.sh's. A failing gate can still show "exit 0". **Always parse the output** for `GATE FAILURE(S)` / `REAL FAILURE` / `go-test -> exit 1` / the final `RESULT: …` line, never trust the task-completion exit code. Related: the SSH transport itself can drop mid-run (especially during the long e2e gate) even though the actual test process keeps running to completion on the remote worker — if the log goes stale with no final RESULT line, reconnect and check `ps aux | grep -E 'playwright test|omnipus-ci'` on the worker before assuming failure; do NOT `pkill -f` anything with a pattern that could match your own shell's command line (self-kill risk — use exact PIDs).

**E2E gate (Playwright).** The `e2e` gate (and the `e2e` step inside `all`) builds the SPA, embeds it in a fresh gateway binary, seeds the canonical e2e `config.json` (provider pointing at OpenRouter, `dev_mode_bypass=true`), stores the OpenRouter key in `credentials.json` (AES-256-GCM), boots the gateway on port 6060, polls `/health` for up to 60 s, completes onboarding via the public API, runs the full Playwright matrix, and tears the gateway down via a `trap RETURN` (no leaked processes on failure). Wall-clock ~20–30 min for all 40 specs. The 5 GitHub Actions shards (llm-chat, llm-agents, llm-light, ui, stubs) collapse into a single sequential run on the worker — for shard-by-shard execution, use the PR workflow. **Required Fly secret** (set as `fly secrets set … --app ci-omnipus`, never commit the value):
  - `OPENROUTER_API_KEY` — preflight-checked by `runci.sh` (`${OPENROUTER_API_KEY:?…}`); the gate fails fast with a clear error if it's missing.

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

**Cost / lifecycle**: the worker is stopped when idle (no public service). If `fly status` shows the machine `stopped`, run `fly machines start <id> --app ci-omnipus` once before invoking `runci.sh`; the SSH console will auto-start it otherwise. Watch the persistent `/cache` volume for disk pressure — `fly ssh console --app ci-omnipus -C 'df -h /cache'`.
