# Repository Secrets

This document lists every GitHub Actions secret the CI/CD workflows require.
Add each secret under **Settings > Secrets and variables > Actions > Repository secrets**.

---

## Playwright E2E (`pr.yml` — `playwright` job)

### `OPENROUTER_API_KEY_CI`

Used by the Playwright E2E job to start the Omnipus gateway with a live LLM
backend so that chat and agent tests receive real (not mocked) responses.

**Recommended model:** not set here anymore — the model is centralized in
`tests/e2e/e2e-model.json` (one committed source of truth, currently a DeepSeek
model; the guard `scripts/check-no-hardcoded-e2e-model.sh` bans model-id
literals in live config). Changing the model is a one-line change to that file.
Tool-use capability and per-run cost should be re-checked when it changes.

**Suggested monthly cap:** $5. Set a usage limit in your OpenRouter dashboard
under the key's settings to prevent runaway spend if a CI job loops.

**Minimum permissions:** The key only needs `chat.completions` scope. Do not
reuse a key that has billing-write or organisation-admin scope.

---

## Nightly Evals (`evals-nightly.yml`)

### `OPENROUTER_API_KEY_EVAL`

Used by the nightly eval runner for two calls per scenario: one to the agent
model and one to the judge model — both follow the ONE central setting in
`tests/e2e/e2e-model.json` (currently a DeepSeek model), as does the Playwright
e2e job. Cost depends on the configured model; the runner fails the workflow
above $2.00 in a single run.

This can be the same key as `OPENROUTER_API_KEY_CI` or a separate key with a
higher monthly cap (suggested: $25).

### `OMNIPUS_MASTER_KEY_EVAL`

64-character hex-encoded 256-bit AES master key used to encrypt the ephemeral
credential store spun up per-scenario. Each nightly run creates fresh
per-scenario home directories; this key is discarded after the run. Any valid
64-char hex string works — it does not need to match any production key.

Generate with:

```bash
openssl rand -hex 32
```

Copy the output (exactly 64 hex chars, 0-9 / a-f) into the secret value.

### Budget

The nightly eval runs the default 15 scenarios with two LLM calls each; the
runner exits non-zero and fails the workflow if cost exceeds $2.00 in a single
run. Per-run cost depends on the model configured in `tests/e2e/e2e-model.json`.

---

## Rotation policy

Rotate all OpenRouter keys at least every 90 days. After rotation, update
the secret value in GitHub Actions and verify the next CI run passes before
closing the rotation ticket. The `OMNIPUS_MASTER_KEY_EVAL` value is not
sensitive across rotations — regenerate freely.

---

## Security CI jobs (`pr.yml` — `security` job, `security-weekly.yml`)

**No new secrets are required** for either the `security` PR job or the
`security-weekly.yml` weekly audit workflow. Both jobs operate exclusively on
the repository source tree using public Go tooling and no external API calls.

### Tools used

| Tool | Purpose | Failure threshold |
|------|---------|-------------------|
| `govulncheck` (`golang.org/x/vuln`) | Source-level Go vulnerability analysis against the Go vulnerability database (vuln.go.dev). Fails with non-zero exit on any known vulnerability that has a fixed version available. | Any vuln with a fix |
| `grype` (anchore/grype) | Dependency CVE scan against NVD, GitHub Advisory Database, and OS package databases. | CVSS >= 7.0 (high + critical) — `--fail-on high --only-fixed` in PR job; SARIF-only in weekly job |

### Weekly SARIF upload

The `security-weekly.yml` job uploads `grype.sarif` to GitHub's Security tab
via `github/codeql-action/upload-sarif`. This requires the `security-events:
write` permission, which is set at the job level — no additional secret is
needed. Ensure the repository has **GitHub Advanced Security** enabled (free
for public repos; required for SARIF ingestion on private repos).
