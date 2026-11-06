# CI eligibility and the issue-tracked failure quarantine

Last reviewed: 2026-10-29 — trunk-based landing branches, default CI model

Process version: 2026-10-29.1

This implements rules 4 and 7 of the founder-approved process rollout. The chief
clarified that a standalone feature need not have an `integration/` branch name.
The required **CI** check is the landing verdict; raw leaf checks stay red when
they fail. A green CI aggregate does not turn a failed test into a passed test.

## When the full workflows run

The PR, build, Cross-Platform, CodeQL and Shellcheck workflows select pull requests
by their **base** (destination), never by source-branch naming or draft status.
Both draft and ready pull requests into `release/v0.1.1` or `main` qualify.
The existing path/language filters and paid `run-e2e` label selection still apply.

| Event | Source / branch | Base | Eligible? |
|---|---|---|---|
| Pull request opened/updated/reopened | `integration/l1-bundle3` | `release/v0.1.1` | Yes, draft or ready |
| Pull request opened/updated/reopened | `feature/1266-chat-history-paging` | `release/v0.1.1` or `main` | Yes, draft or ready |
| Pull request opened/updated/reopened | Any work branch | `integration/l1-bundle3` | No |
| Pull request opened/updated | `landing/t6` (a train's landing branch, or a standalone feature's such as `landing/effort`) | `release/v0.1.1` | Yes, draft or ready (this is the cut) |
| Pull request | any branch | `landing/*` | No; direct pushes are the rule, no PR into a landing branch |
| Push | `main` or `release/v0.1.1` | — | Yes |
| Push | `landing/*` (trunk-based landing branches) | — | No (by design: no full CI until the cut) |
| Push | `integration/*`, `feature/*`, `work/*`, another release branch | — | No |
| Manual full-workflow dispatch | Any branch | — | Removed from the four workflows that had it |
| Weekly scheduled CodeQL scan | Default `main` | — | Retained |

PR events include `opened`, `synchronize`, `reopened`, `ready_for_review`, and
`converted_to_draft`. Draft status never disables a job. Unrestricted manual
full-workflow dispatch was removed from PR, build, Cross-Platform and Shellcheck;
PR's now-dead `llm_e2e` input was removed with it. Add `run-e2e` before the next
PR push or reopen to select paid LLM shards. Release/main pushes retain the full
shard plan. Unrelated scheduled/manual workflows are unchanged.

Trunk-based landing (founder, 2026-10-29): teams push directly to the train's
`landing/<train>` branch, and those pushes start no full CI. Before each push the
author runs only the own-tests rule (root `CLAUDE.md`, "Landing trains"): the code
builds, the linter is clean on the touched packages, and the change's targeted
tests pass, locally or on Fly with founder permission; nobody runs a whole suite.
A local typecheck and compile must pass before a Fly window; use the required slot
runner for heavy local commands (on a pod, `/usr/local/bin/heavy`). The cut, when
the team leads say the train is ready, is the chief's draft PR from the landing
branch to `release/v0.1.1`: it starts the single full CI run, on exactly the cut
commit, and regressions are found there. Add the `run-e2e` label to the cut PR
before its next push or reopen when the paid LLM shards must run.

## Default CI model

E2E shards use `tests/e2e/e2e-model.json`: primary MiniMax
`MiniMax-M3.1-Flash-Preview`, fallback `deepseek/deepseek-v4.1-flash` on
OpenRouter; the secrets are listed in `.github/SECRETS.md`. This is separate from
the UAT provider rule in root `CLAUDE.md`, which governs the model the app under
test uses in a hands-on run.

## One list, no skipped tests

The single aggregate-exception list is
`.github/ci-quarantine.json`.
Each record carries its test/infrastructure ID, exact check scope, open issue
number, added date and reason. The list may shrink without approval. Adding an
entry or broadening an existing entry requires the founder's explicit approval;
an open issue alone does not authorize an addition.

The approved initial list has exactly three records:

| Record | Exact check / scope | Issue | Observed integration evidence |
|---|---|---|---|
| `Conformance_t0_ChatGoalE2E` | `E2E — llm-conformance-chat`, that test only, default project | #1431 | Run 39832315507 |
| `Conformance_t1_StandaloneTaskE2E` | `E2E — llm-conformance-exec`, that test only, default project | #1431 | Run 39832315507 |
| `design-system-browser-install` | `Design system contracts and browser evidence` / `Run npx playwright install --with-deps chromium firefox webkit`; `Design system browser checks — webkit` / `Run npx playwright install --with-deps webkit` | #1426 | Run 39779260869, attempts 1 and 2 |

Issue #1427 is a Playwright upgrade task, not a separate browser-download check in
these runs, so it is not seeded. Other E2E flakes and design-system artifact
network failures are not covered by these entries.

The existing test commands, strict skipped/flaky checks, matrices and dependencies
are unchanged. Every selected test still runs; raw failures remain visible and
blocking at the leaf-job level. Every E2E shard uploads its raw JSON report using
a run-attempt-specific artifact name. Only the final CI aggregate interprets
quarantine evidence. It never retries a failure or changes a test assertion.

## How the aggregate decides

`scripts/ci-quarantine.py::aggregate`
uses the direct dependency results, current-run job metadata and exact evidence.
It first validates **every** list entry through the live GitHub issue API, even
when all jobs passed. A closed issue, missing issue, pull request masquerading as
an issue, malformed record or API error fails CI. Remove a resolved entry as part
of its fix; closing its issue while leaving the entry blocks the next aggregate.

| Failure | Required evidence / result |
|---|---|
| Listed conformance failure | Exact check, report file, stable test ID and project match; executed failing attempts; valid and consistent JSON statistics. Both final failures and failed-then-passed (flaky) results are reported. |
| Another failure in the same shard | Blocks CI. A listed test never exempts the whole shard. |
| Missing report, skipped tests, interrupted attempts or global report error | Blocks CI; no claim that tests passed or even completed. |
| Listed installation hang | Exact cancelled install step, Ubuntu apt-mirror log signature and GitHub's job-timeout annotation. A user cancellation or failure at another step does not qualify. |
| Missing webkit browser-report upload after that hang | Only its exact upload step and missing-file message qualify as a consequence of the same failed install. Other upload failures still block. |
| Dependent design-system audit not run | Reported as not run, under #1426; never called coverage. Requires an evidenced install timeout, a successful lock audit and a skipped enforcement audit. The exact audit-summary failure is accounted for as the same dependency chain. |
| Unlisted failure or cancellation anywhere else in the required dependency set | Blocks CI. |

The summary lists the raw result of each required gate and each used quarantine
exception with its exact test/step and issue. A mixed result is labelled FAIL,
not green. If no unlisted failures remain it says **green except quarantined**.
GitHub's overall workflow banner can still be red because the raw jobs were not
softened. The chief reads the required **CI** check for the landing decision.
A report/API/permission failure blocks rather than guessing that an exception
applies. Re-running failed jobs uses the latest attempt for each check while
preserving evidence from successful jobs that did not re-run.

## Required build coverage and evidence-download retry

The catalog-snapshot hermetic build is an explicit dependency of the required
CI aggregate. Its raw failure therefore blocks landing; it is not quarantined.

The `Design system enforcement audit` job retries `Download all evidence
artifacts` exactly once after a failed first download. The first attempt keeps
its raw failure outcome and permits that one retry. The retry is hard-failing;
an `always()` guard also fails unless at least one attempt succeeded. The same
destination and action version are used for both attempts. A first-attempt
success causes no retry. Merge/precondition validation and every audit remain
unchanged and blocking. This handles a transport reset, not a test failure, and
adds nothing to the quarantine list.

The local parsed-workflow proof at
`scripts/ci-gate-wiring.test.py`
feeds a failed hermetic gate through the actual declared dependency set and
exercises first-download success, retry recovery and two failures. It finds
every step named `Download all evidence artifacts` across the workflows, not
just a hand-picked job.

## Verification commands

Run the guard and its dependency-free fixture self-test with Python 3 and the
authenticated GitHub CLI (set `GH_TOKEN` in CI). No Go build or browser is needed.

```bash
GITHUB_REPOSITORY=elicify-ai/omnipus python3 -I scripts/ci-quarantine.py guard
python3 -I scripts/ci-quarantine.py --self-test
```

The self-test exercises the real CLI/classifier with only the GitHub process edge
replaced by fixtures. It includes failed quarantined and failed unlisted jobs,
closed/missing issues, missing reports, an extra failure in a listed shard, exact
installation timeout evidence and re-run attempts. It is run inside the required
CI job before the real aggregate. The separate trigger-table proof at
`scripts/ci-workflow-triggers.test.py`
uses PyYAML for a local, parsed-workflow eligibility check, not a claim that a
GitHub-hosted run was executed.

This is developer delivery infrastructure, not product behaviour; no user-facing
product documentation change is required. The Fly worker's raw verdict and log
parsing are unchanged; it does not apply GitHub's aggregate exceptions.

## Why a landing runs the full CI twice (founder, 2026-10-29)
The cut PR runs the full CI once without the paid live-model (llm-*) shards unless the PR carries the `run-e2e` label. The push to the release branch at landing always runs the full CI including those live-model shards. That second run is intended and is the run that exercises the live-model tests: do not suppress it. Both runs are on the same commit when the landing is a fast-forward.
