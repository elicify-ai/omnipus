# ADR-005: Embedded-SPA E2E Test Pipeline & CI Gateway Contract

## Status

Accepted (2026-04-17) — first applied in PR #B (Plan 3 feature/next-wave-3-pr-b).

## Context

Omnipus ships as a single Go binary with the React SPA embedded via `go:embed`. End-to-end tests must exercise the compiled binary — the Vite dev server is not representative of shipped behavior (different proxy config, no credential store, no gateway sandbox). Every future test wave (Plan 3 PR-B Playwright, PR-C perf, PR-D security) needs to boot an identical gateway in CI.

Without a shared contract, each wave rediscovers the same CI blockers: wrong port, missing `dev_mode_bypass`, missing tool-capable model, and credential store not provisioned before the gateway starts.

## Decision

All E2E test jobs MUST boot the gateway via this contract, in this order:

1. **Clean `$OMNIPUS_HOME`.** Seed a unique temp dir; never reuse state between jobs.

2. **SPA sync verified.** `rm -rf pkg/gateway/spa/assets && cp -r dist/spa/* pkg/gateway/spa/` followed by a non-empty-dir check. Without this, `go:embed` picks up stale assets and Playwright tests load a blank page.

3. **Seed `config.json` before boot** with: `gateway.port` matching the test runner's expected port (6060 for Playwright), `gateway.dev_mode_bypass: true` to unblock onboarding endpoints, and `agents.defaults.model_name` pointing at a tool-capable model (`glm-5-turbo` per CLAUDE.md known-blockers #3).

4. **Seed credentials via CLI before boot.** Run `./omnipus credentials set openrouter_api_key "${OPENROUTER_API_KEY_CI}"` with `OMNIPUS_MASTER_KEY` set. The same `OMNIPUS_MASTER_KEY` is passed to the gateway at launch so both the CLI and the gateway share the same AES-256-GCM key. Env-only injection (passing `OPENROUTER_API_KEY` directly to the gateway process) is not sufficient because `pkg/credentials/inject.go:InjectFromConfig` reads from the encrypted store, not from the environment directly. Verified post-boot via `/api/v1/providers/status`.

5. **Health-check via `/health` poll** on the seeded port. Fail fast if the gateway PID dies within 500 ms of launch (catches panic-at-boot separately from slow-boot). The 60 s timeout is a CI runner safety net, not an accepted boot budget.

6. **Single worker.** Playwright `workers: 1`. The gateway is a single-writer process for `config.json`, `credentials.json`, and per-entity JSONL files. Concurrent test workers race on these files.

7. **Artifact uploads.** On any failure, upload `playwright-report/` and `test-results/` (traces and videos). Always upload `$OMNIPUS_HOME/logs/` including `gateway_panic.log` — this file is the primary diagnostic for silent gateway crashes.

## Consequences

- PR-C (perf) and PR-D (security) inherit this contract verbatim — they change which scenarios run, not how the gateway boots.
- Future CI changes that violate the contract (e.g., raising workers, skipping SPA sync, omitting `dev_mode_bypass`) should be caught in review by referencing this ADR.
- The `dev_mode_bypass` flag is a workaround for a pre-existing onboarding auth bug (onboarding endpoints require bearer auth before an admin account exists). When that bug is fixed, step 3 no longer needs the bypass field and this ADR should be updated.
- The contract is self-validating: the PID-alive check, health poll, and provider status check each produce distinct failure messages and artifact evidence when any step breaks.

## Related

- CLAUDE.md "Build & E2E Testing" — the operational doc this ADR formalizes
- ADR-004 Credential Boot Contract — defines the `OMNIPUS_MASTER_KEY` unlock path and auto-generate behavior relied on in step 4
- Plan 3 `temporal-puzzling-melody.md` §5 CI workflow map
