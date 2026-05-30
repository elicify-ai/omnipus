# Omnipus E2E Test Suite

Playwright end-to-end tests for the Omnipus gateway + SPA.

## Test prerequisites

The following must be satisfied before running the suite. Missing prerequisites
fail the run immediately — they are not soft-skipped.

### Required

**`OPENROUTER_API_KEY_CI`** (environment variable, required, no soft-skip)

A valid OpenRouter API key. Used by tests that drive real LLM calls (chat, subagent
spawn, handoff, media screenshot). Its absence is a CI configuration failure, not a
per-test skip condition.

- In CI: add as a repository secret under Settings > Secrets > Actions.
  The `playwright` job in `.github/workflows/pr.yml` injects it as `OPENROUTER_API_KEY_CI`.
- Locally: `export OPENROUTER_API_KEY_CI="sk-..."` before running `npx playwright test`.

The `global-setup.ts` preflight throws immediately if this variable is unset:
```
[E2E preflight] OPENROUTER_API_KEY_CI is not set.
```

**`OMNIPUS_HOME=/tmp/<fresh-dir>`** (environment variable, required per run)

The gateway's workspace directory. Must be a clean directory for each test run to
prevent state leakage between runs. The CI workflow creates it fresh:
```bash
rm -rf /tmp/omnipus-e2e && mkdir -p /tmp/omnipus-e2e
```

For local runs, use a unique directory per session:
```bash
export OMNIPUS_HOME=/tmp/omnipus-e2e-$(date +%s)
```

### Kernel requirement for security tests

**Linux >= 6.7** is required for kernel-level security tests (Landlock ABI v4,
`NET_BIND_TCP`). The test packages `pkg/sandbox/backend_linux_subprocess_test.go`
and related files check kernel version and skip non-applicable scenarios on older
kernels. The Playwright suite itself does not require a specific kernel version, but
the backend security tests that complement it do.

## Running locally

```bash
# Build the binary
CGO_ENABLED=0 go build -o ./omnipus ./cmd/omnipus/

# Start a fresh gateway
export OMNIPUS_HOME=/tmp/omnipus-e2e-local
rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty &

# Run tests (with required key)
export OPENROUTER_API_KEY_CI="sk-..."
npx playwright test
```

## Skip policy

**Soft-skips are not permitted** except for entries explicitly tracked in
`tests/e2e/fixtures/skip-tracking.ts:SKIP_ALLOWLIST`. Every allow-list entry must
include a GitHub issue URL and a target resolution date.

If a test calls `softSkip()` without a matching allow-list entry, it **fails**
(not skips) with an `[skip-tracking] UNAUTHORIZED SKIP` error. This is intentional —
it prevents silent drift back into soft-skip culture.

To add a legitimate skip:
1. Add an entry to `SKIP_ALLOWLIST` in `tests/e2e/fixtures/skip-tracking.ts`.
2. Include `{ test: "<exact title>", issue: "<GitHub URL>", until: "YYYY-MM-DD" }`.
3. Resolve the issue and remove the entry before the `until` date.

## CI configuration

CI runs are in `.github/workflows/pr.yml` under the `playwright` job. The job:
1. Builds the Go binary with the SPA embedded.
2. Seeds an `OMNIPUS_HOME` with `config.json` pointing at `z-ai/glm-5v-turbo` via OpenRouter.
3. Seeds the OpenRouter credential into `credentials.json`.
4. Starts the gateway and waits for `/health`.
5. **Verifies `OPENROUTER_API_KEY_CI` is set** (preflight step) before running any test.
6. Runs `npx playwright test`.

Required secrets (Settings > Secrets > Actions):
- `OPENROUTER_API_KEY_CI` — OpenRouter API key.
- `OMNIPUS_MASTER_KEY_CI` — AES-256 master key (hex) for the test `credentials.json`.

## Skip-tracking dashboard

The skip-tracking system prevents unauthorized or silent test skips from accumulating
across commits. It consists of three components:

### skip-manifest.json

Written to `test-results/skip-manifest.json` (configurable via
`OMNIPUS_SKIP_MANIFEST_PATH`) at the end of every run by `global-teardown.ts`.

```json
{
  "timestamp": "2026-05-04T12:34:56Z",
  "run_id": "<CI run ID or 'local'>",
  "git_sha": "<short SHA>",
  "branch": "<branch name>",
  "skips": [
    { "test": "some test title", "reason": "...", "kind": "softSkip" }
  ],
  "allowlisted": [
    { "test": "...", "issue": "https://github.com/...", "until": "YYYY-MM-DD", "note": "..." }
  ],
  "unauthorized_skips": [
    { "test": "...", "reason": "..." }
  ]
}
```

The manifest is written regardless of whether any skips occurred (it will have
empty arrays). It is useful for auditing skip trends across CI runs.

### skip-baseline.json

Located at `tests/e2e/fixtures/skip-baseline.json`. This is the "previous green
main" anchor. The teardown gate fails the CI run if:

```
manifest.unauthorized_skips.length > baseline.baseline_unauthorized_skips
```

**Updating the baseline** (only when a long-term skip is intentionally absorbed):
1. Ensure the skip has a valid `SKIP_ALLOWLIST` entry with `issue` and `until`.
2. Manually increment `baseline_unauthorized_skips` in `skip-baseline.json`.
3. Commit with a comment explaining the rationale.

Never auto-increment the baseline from code or CI scripts.

### What causes CI to fail

The teardown exits with code 1 (failing the run) when:

1. `manifest.unauthorized_skips.length > baseline.baseline_unauthorized_skips` —
   the unauthorized skip count has risen above the previous-green-main anchor.

2. Any entry in `SKIP_ALLOWLIST` has an `until` date that is in the past —
   the entry has expired and the underlying issue must be resolved or the
   deadline extended with justification.

Note: individual tests that call `softSkip()` without a valid allow-list entry
are already marked **FAILED** before teardown runs — the teardown is defense-in-depth.

### SKIP_ALLOWLIST entry requirements

Each entry must include:
- `test` — exact test title string (first argument to `test(...)`)
- `issue` — GitHub issue or PR URL: `https://github.com/<owner>/<repo>/issues/<n>`
  or `https://github.com/<owner>/<repo>/pull/<n>`
- `until` — target resolution date in `YYYY-MM-DD` format

The validator runs at module load time. Any malformed entry throws immediately,
before any test runs, so CI fails loudly rather than silently accepting bad metadata.

## Test structure

```
tests/e2e/
  *.spec.ts               — test suites (one per feature area)
  global-setup.ts         — auth setup + preflight checks
  global-teardown.ts      — skip manifest writer + baseline gate + expired-entry gate
  setup.ts                — self-managed gateway helpers (startGateway, stopGateway)
  fixtures/
    skip-tracking.ts      — softSkip() + SKIP_ALLOWLIST + manifest writer + validator
    skip-baseline.json    — previous-green-main anchor for the baseline gate
    aging.ts              — agedTranscript() + agedSessionExists() helpers for T4.2
    console-errors.ts     — fixture that asserts zero unexpected JS console errors
    a11y.ts               — axe accessibility check helper
    login.ts              — loginAs() helper
    onboard-via-api.ts    — onboardViaAPI() helper
    selectors.ts          — canonical DOM selector helpers
    session-setup.ts      — session creation helpers
    .auth/admin.json      — persisted auth state (gitignored)
```
