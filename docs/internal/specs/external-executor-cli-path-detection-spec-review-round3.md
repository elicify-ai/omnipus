# Adversarial Review — External-Executor CLI Path Detection, Prefill & Validation (Round 3)

- **Spec reviewed:** `docs/internal/specs/external-executor-cli-path-detection-spec.md` (post Round-1 + Round-2 revisions)
- **Source ADR:** `docs/internal/architecture/ADR-030-external-executor-cli-path-detection.md`
- **Mode:** `plan-spec` (full BDD + FR + traceability structure present)
- **Reviewer stance:** read-only, adversarial. Assume the worst interpretation of every requirement.

---

## Executive Summary

This round found **1 CRITICAL, 5 MAJOR, 7 MINOR, 2 OBSERVATION**. The Round-1/Round-2
security hardening (admin-gate, audit, rate-limit, identity matcher, HOME-unset) is a
real improvement, but it introduced a **new, unresolved contradiction between the
project's mandatory admin-route security contract and the e2e environment**, and it
left the SPA's behaviour on *non-classification* validate responses undefined — which
is the exact surface the hardening created (403/429/503). The identity matcher is made
a Create-blocker while its matching tokens are explicitly unverified, risking
false-blocking of working installs.

**Verdict: BLOCK** (one CRITICAL: the admin-gate ↔ `dev_mode_bypass` ↔ required-e2e
trilemma, `C1`).

---

## Findings Table

| ID | Severity | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| C1 | CRITICAL | Insecurity / Inconsistency / Infeasibility | FR-013, §Integration Boundaries, TDD #20/#21 | `cli-validate` is spec'd `withAuth → RequireAdmin` but the spec is silent on `RequireNotBypass`. The codebase's hard contract (`redteam_admin_routes_test.go`: "admin-route bypass-exempt set must stay empty" — every admin route returns 503 under `dev_mode_bypass`). The e2e gate runs with `dev_mode_bypass:true` (`deploy/ci-worker/runci.sh:176`). Both consistent resolutions break a stated requirement: (a) add `RequireNotBypass` → validate returns **503** in e2e → the required `external-executor-create.e2e` create flow can't reach `ok` → SC-007 fails; (b) omit `RequireNotBypass` → under bypass an **anonymous** caller is treated as admin (`auth.go:77` `devBypassUser`) and can spawn an arbitrary caller-supplied executable via `cli-validate` — precisely the C6 escalation the redteam tests guard against, on the one endpoint the spec's own F-01 flagged CRITICAL for spawning a caller path. | Decide explicitly. Recommended: keep `RequireNotBypass` (honour the security contract for a path-spawning endpoint) AND change the e2e for this feature to not depend on bypass — seed a real admin token in the e2e `config.json` for the create-validate flow, or make the wizard's create path tolerate a validate call that the environment refuses (treat 503/bypass as "validation unavailable → allow with warning", never fail-open to a *silent* pass). Document the chosen resolution in the spec and in ADR §11, and add the endpoint to `allAdminRoutes` so the bypass-coverage guard exercises it. |
| M1 | MAJOR | Incompleteness | FR-008, FR-018, §Behavioral Contract, D-2 | SPA gating (FR-008/FR-018) branches **only** on `reason ∈ {ok, missing-binary, handshake-failed, unauthenticated, unknown-cli}`. The Round-2 hardening makes `cli-validate` able to return **403** (non-admin, M2), **429** (rate limit), **503** (bypass gate, C1), plus generic 5xx / network-timeout / abort. None of these carry a `reason`, so the Create-gating logic has **no defined branch** — Create is left in an undefined state (blocked forever? allowed? spinner stuck?). This is the surface the hardening itself created. | Add an explicit "validation could not be performed" state to the Behavioral Contract and FR-008: define, per non-200 class, whether Create is blocked, allowed-with-warning, or retryable. Add BDD rows + a dataset (D-2.6…) for 403/429/503/timeout. Never fail-open silently. |
| M2 | MAJOR | Inconsistency (RBAC) | FR-013, §Actors, US-1…US-5 | `createAgent` (`pkg/gateway/rest.go:1532`, route `withAuth` only — `gateway.go:1620`) lets a **non-admin `user` role create a `subagent_3p`** (ownership model, `agent_ownership.go:45`). But `cli-validate` is `RequireAdmin` (SC-008 asserts 403 for `user`). So the spec's own actor — "Operator … creates/edits a subagent_3p" — hits a wizard whose validate-on-blur **403s** for non-admins, while detection/prefill (GET `cli-detect`, `withAuth` only) works. The flow assumes validate is reachable by the same operator who creates. | Reconcile the RBAC. Either (a) gate `POST /agents` create-of-subagent_3p to admin too (and say so), or (b) drop `cli-validate` to `withAuth` and rely on the target-file guard + rate-limit + audit for the (already admin-trusted) `cli_path`. State the decision and which roles reach each of create / detect / validate. |
| M3 | MAJOR | Infeasibility / Incorrectness | FR-015, SC-010, §Per-reason table, A-3 | The identity matcher (`claude-code`→contains "claude", etc.) is made a **Create blocker** (mismatch → `handshake-failed` → FR-008 blocks), yet the spec itself says the exact tokens are "**to be confirmed against real CLI output — spike A-3**". Many CLIs print a bare version (`--version` → `1.2.3`) with **no tool name**. If `claude --version` emits only a version number, a *legitimate, working* `claude` binary → `handshake-failed` → Create blocked, directly contradicting US-3 AC-4 / SC-002. The current `conntest.extractVersion` deliberately tolerates name-less output ("still a valid handshake"); FR-015 removes that tolerance. It also silently tightens `POST /agents/{id}/runner-test`. | Verify actual `--version` output of all three CLIs **before** making the matcher a blocker. If a CLI legitimately omits its name, degrade a name-mismatch-but-valid-version case to a **warning** (allow Create), reserving `handshake-failed` for no-version / wrong-version. Add a fallback rule to the Per-reason table and a dataset row for "valid version, no name token". |
| M4 | MAJOR | Ambiguity / Incompleteness | FR-005, FR-018, §minors (debounce), US-3/US-4 | Validation is debounced ≥400 ms, cancels in-flight, and runs on-blur. The Create/Save button state **when no validation has completed** (operator types a path and clicks Create before blur, or during the debounce, or the request is still in flight) is undefined. FR-018 gates on a `reason` that may not exist yet. If Create is allowed with no `reason`, the **original defect (saving an unvalidated/wrong path) persists**; if blocked, the operator can be stuck with a spinner. | Define the pre-validation Create state explicitly: e.g. Create disabled until a terminal validation result exists for the current path value; on submit, force a synchronous validate. Add BDD + dataset for "submit before validation resolves". |
| M5 | MAJOR | Infeasibility | SC-012, FR-017 | SC-012 ("`cli-validate.detail` contains **no substring** of the process stderr") is not a sound test. Classified messages legitimately share tokens with stderr — e.g. detail "installed; not logged in" vs stderr "error: not logged in" → "not logged in" **is** a substring; and an empty stderr is a substring of *every* string. The criterion can fail on correct output and pass vacuously. | Restate as an equality/allowlist check: assert `detail` is exactly one of the fixed classified strings in the Per-reason table (and that the raw `CombinedOutput`/stderr string is never assigned verbatim to `detail`). Drop "no substring". |
| m1 | MINOR | Overcomplexity / Incompleteness | FR-013 ("cap concurrent in-flight validations ≤2") | The concurrency cap's semantics are undefined: scope (global / per-IP / per-user?), rejection behaviour (429? 503? queue with timeout?), and interaction with the dedicated rate limiter + 15 s timeout — which already bound resource use for an admin-only local diagnostic. As written it risks two legitimate operators starving each other and adds an untested code path. | Either drop the cap (rate-limit + 15 s timeout already bound it) or fully specify scope + the exact status returned on the 3rd concurrent call, and add it to the dataset. |
| m2 | MINOR | Ambiguity / Incorrectness | §Per-OS candidate dirs ("newest `~/.nvm/versions/node/*/bin`"), F-11 ("selects the highest") | "Newest / highest version" is undefined for comparison. `filepath.Glob` returns lexicographic order, under which `v0.9.0` > `v0.10.0` (wrong). Constraint #2 (pure Go, no new deps) forbids pulling a semver lib. | Specify a numeric/semver-aware directory comparison implemented in-package (split on `.`, compare ints) and add a dataset row (`v0.9.0` vs `v0.10.0`) proving the newer wins. |
| m3 | MINOR | Inconsistency | §Scope vs ADR §10 | The spec Scope declares the datepicker→shadcn work "**explicitly out of scope … gets its own plan-spec**," but ADR-030 §10 says it "**bundled on the same branch/PR**." So this PR carries unspecced datepicker code through the 7-reviewer gate, and SC-007's "green" implicitly includes datepicker tests this spec never defines. | Resolve the doc conflict: either drop §10 from the branch/PR (match the spec) or note in the spec that the branch also carries the §10 change (covered by its own DoD) and exclude it from this spec's SC-007 scope. |
| m4 | MINOR | Incompleteness (Security) | §Integration Boundaries (new POST endpoint) | Production wraps all handlers in `CSRFMiddleware` (`WrapHTTPHandler`); admin state-changing routes are **not** CSRF-exempt (`TestAdminRoutes_AllHaveCSRF`). The new `POST /system/cli-validate` is silent on CSRF and on the `exemptPaths` list (`src/lib/api.ts:505` must stay in sync with `csrf.go`). It works only if the call uses the standard `apiFetch` wrapper (attaches `X-CSRF-Token`). | State that `cli-validate` MUST be called via the CSRF-attaching wrapper and MUST NOT be added to `exemptPaths`; add a test asserting 403 on missing CSRF. |
| m5 | MINOR | Infeasibility / Ambiguity | SC-001, SC-003 | SC-001 claims "0 false negatives across the **D-1 matrix**," but F-07's real-install integration test is Linux-only; the macOS (D-1.5) and Windows (D-1.6) rows are table-driven/mocked, so SC-001's "each of the 3 CLIs" real-install guarantee is only Linux. SC-003's "in ≥ the 3 supported CLIs" is awkward and unmeasurable (there are exactly 3). | Scope SC-001 to the OS actually exercised by integration tests and label the macOS/Windows rows as table-only; reword SC-003 to "for all 3 supported CLIs, one interaction prefills the field." |
| m6 | MINOR | Ambiguity | F-08, FR-017, §Per-reason table | Two wording issues: (a) `resolved_path` "returned (`withAuth`-only)" is redundant/misleading given the endpoint is `RequireAdmin` (always ≥ admin); (b) `version` is derived from the binary's `--version` output while FR-017 forbids raw stderr in `detail` — the spec should make explicit that `version` (parsed token) is exempt from the stderr-sanitisation rule, else a reviewer reads a contradiction. | Delete the "`withAuth`-only" qualifier (endpoint is admin-only) and add one line clarifying `version` is a parsed token, distinct from the sanitised `detail`. |
| m7 | MINOR | Incompleteness | US-5, FR-009 | On opening an existing agent whose `cli_path` is **non-empty but stale/broken**, prefill (empty-only) does nothing and validation runs only on blur — so Save is **not blocked** and the operator can persist a still-broken path unchanged. The edit flow reintroduces the create-flow defect for untouched fields. | Specify a validate-on-open (or validate-on-save) for the edit form so an unedited-but-broken `cli_path` is surfaced before Save. |
| o1 | OBSERVATION | Incompleteness | §Per-OS dirs (Windows), D-1 | The Windows well-known scan is under-specified: `%LOCALAPPDATA%\Programs\<tool>` uses a `<tool>` placeholder (actual dir names for claude/codex/opencode unknown), and D-1 has **no Windows well-known-scan row** (only PATH, D-1.6). Windows well-known detection is effectively untested. | Enumerate concrete Windows subdirs and add a Windows well-known-scan dataset row (table-driven so it runs on Linux CI). |
| o2 | OBSERVATION | Inoperability | FR-013 (rate limiter), FR-015 | The dedicated rate-limit's numeric value is unspecified, and there is no runtime kill-switch/flag for the newly-strict identity handshake that now also alters the shared `runner-test` path (M3). If the matcher over-rejects in production, operators must patch/redeploy. | Pin the rate-limit value; consider a config flag to relax the identity match to warn-only if field reports show false negatives. |

---

## Structural Integrity Results (plan-spec checks)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | PASS | US-1…US-6 all do. |
| Every acceptance scenario has ≥1 BDD scenario | PARTIAL | US-4 AC-2, US-5 AC-1, US-6 AC-2 lack a dedicated BDD scenario (covered only via datasets/matrix). |
| Every BDD scenario has a `Traces to:` back-reference | PASS | All present. |
| Every BDD scenario has a corresponding TDD test | PARTIAL | "Manual override is what gets validated" and "Validate-on-blur (edit)" map to vitest bundles, not 1:1 named tests; the new 403/429/503 surface (M1) has **no** BDD/test. |
| Every functional requirement in the traceability matrix | PASS | FR-001…FR-018 all listed. |
| Every BDD scenario in the traceability matrix | PARTIAL | Matrix keys off FR→BDD, not per-BDD-row; the added "rate-limited"/"HOME unset"/"wrong binary" rows are covered, but non-200 handling is absent (M1). |
| Test datasets cover boundary/edge/error | PARTIAL | Strong for path values (D-3) and classification (D-2); **missing** rows for non-classification HTTP responses (M1), pre-validation submit (M4), nvm version ordering (m2), Windows well-known scan (o1), valid-version-no-name (M3). |
| Regression impact explicitly addressed | PASS | §Regression Impact Summary + Regression Test Requirements are concrete (runner-test identity tightening called out). |
| Success criteria measurable, no subjective language | PARTIAL | SC-012 unsound (M5); SC-001/SC-003 overclaim/awkward (m5). |

---

## Test Coverage Assessment

- **Negative HTTP paths missing.** The suite tests the 5 `reason` classifications thoroughly but has **no** test for what the SPA does when `cli-validate` returns 403 / 429 / 503 / 5xx / network-timeout — the very responses the Round-2 hardening introduced (M1, C1).
- **Identity matcher lacks a positive-tolerance test.** `TestValidate_WrongBinaryIdentity` proves `/usr/bin/node`→`handshake-failed`, but there is no test that a **real CLI whose `--version` omits its name** still passes (M3) — the false-negative risk is untested.
- **Pre-validation submit untested.** No test exercises "click Create before validate resolves" (M4).
- **Ordering/boundary gaps.** No dataset/test for nvm version ordering (m2) or Windows well-known scanning (o1).
- **Concurrency cap untested-as-specified.** TDD #22 asserts "concurrency cap holds" but the spec never defines the rejection status/scope, so the test target is undefined (m1).

---

## STRIDE Threat Summary

| Component / flow | Threats identified |
|---|---|
| `POST /system/cli-validate` (spawns caller-supplied path) | **Spoofing / EoP**: under `dev_mode_bypass=true` an anonymous caller is admin and can spawn arbitrary executables if `RequireNotBypass` is omitted (C1). **DoS**: `--version` hang × concurrency; bounded by 15 s + rate limit, but cap semantics undefined (m1). **Info disclosure**: `detail`/`version` must not leak raw stderr (FR-017 good; SC-012 test unsound, M5). **Repudiation**: mitigated — audit `{cli, resolved_path, reason}` per call (FR-013). **Tampering**: target-file guard (reject non-regular/non-executable pre-spawn) good; TOCTOU between guard and spawn acknowledged (H-6). |
| `GET /system/cli-detect` (filesystem probes) | Read-only, no subprocess, unaudited by design — acceptable. **Info disclosure**: returns absolute install paths to any authenticated caller (withAuth, non-admin included) — low risk, but note the asymmetry with validate's admin gate (M2). |
| SPA prefill/validate UX | **CSRF**: new POST not addressed (m4). **Fail-open risk**: undefined handling of non-200 could silently allow Create of an unvalidated path (M1, M4). |

---

## Unasked Questions (the spec should have answered)

1. **Does `cli-validate` carry `RequireNotBypass`?** If yes, how does the `dev_mode_bypass=true` e2e reach `ok`? If no, how is anonymous-under-bypass spawning prevented? (C1)
2. **What does the SPA do when validate returns 403 / 429 / 503 / times out / aborts?** Block, allow-with-warning, or retry? (M1)
3. **Which roles can create a `subagent_3p` vs. validate one?** Is create admin-only, or is validate being over-gated relative to create? (M2)
4. **What is the exact `--version` output of `claude` / `codex` / `opencode`?** Does any print a bare version with no name (breaking the identity matcher)? (M3)
5. **Is Create enabled before any validation completes?** What is the button state during the debounce / in-flight window? (M4)
6. **How is "newest" nvm version chosen** without a semver dependency? (m2)
7. **Is the datepicker (§10) in this PR or not?** ADR and spec disagree. (m3)
8. **What is the concurrency-cap rejection status and scope**, and is the cap even needed on top of the rate limiter + 15 s timeout? (m1)

---

## Verdict

**BLOCK** — one CRITICAL (`C1`: the admin-gate ↔ `dev_mode_bypass` ↔ required-e2e trilemma
is unresolved and both consistent resolutions break a hard requirement — the project's
"every admin route rejects bypass" security contract, or SC-007's required e2e). Five MAJOR
findings (undefined non-200 handling, create/validate RBAC asymmetry, unverified identity
matcher as a blocker, undefined pre-validation Create state, unsound SC-012) must also be
resolved before implementation.
