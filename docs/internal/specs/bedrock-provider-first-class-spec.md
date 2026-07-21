# Feature Specification: Bedrock as a first-class LLM provider (credential-archetype-aware provider validation)

**Created**: 2026-07-21
**Status**: Draft (revision 3 — addresses grill review pass 2, `bedrock-provider-first-class-spec-review.md`, verdict BLOCK)
**Input**: Operator directives — "i would like to support bedrock as an llm provider"; "make sure the provider ui can support bedrock"; *"list models is the right way we need it anyway for the model selector"* + *"make it to match `ValidateKey`"*; and the dependency decision (2026-07-21): **add Bedrock's control-plane SDK module + bump the AWS SDK to latest**, to enable a live model list. Grounded in [ADR-053](../architecture/ADR-053-bedrock-provider-first-class.md) (Status: Proposed, Option C).

---

## Summary

AWS Bedrock is already implemented, wired into the provider factory, and named in
the wire contracts — but it is unreachable in the product: gated behind a `bedrock`
build tag, absent from the catalog, and structurally rejected by a validation gate
that can only express one kind of credential.

This spec makes Bedrock first-class by **generalizing provider validation to be
credential-archetype-aware**. The gate's *intent* — prove the provider is genuinely
configured and reachable before persisting — is **preserved and extended**, never
bypassed.

**Validation depth (operator decision, resolves grill CRIT-002/N1):** for
`aws_credential_chain`, save-time validation **lists Bedrock models** via the
control-plane `ListFoundationModels` API — the Bedrock analog of `ValidateKey`'s
`FetchModels`. Free, proves the credential chain + region + account-level Bedrock
access, and feeds the model picker. This **adds the `aws-sdk-go-v2/service/bedrock`
control-plane module** (today only `…/service/bedrockruntime` is a dep — verified
in `go.mod`) and **upgrades Bedrock from `'manual'` to `'live'` catalogue mode**
(`src/lib/agents/providerCatalog.ts`), so the picker shows a live model list.

**Dependency decision (operator, 2026-07-21):** add the control-plane module and
**bump all `aws-sdk-go-v2` modules to their latest versions** in the same change.

**Out of scope (deferred):** `ChatStream` for Bedrock (the agent loop degrades
cleanly for a non-streaming provider — `loop.go:6745` → `:6791`).

---

## Revision history (grill resolution map)

| Finding (pass / id) | How addressed in this revision |
|---|---|
| P1 CRIT-001 (SSRF bypassed on keyless) | FR-017; SSRF runs on endpoint for **all** archetypes |
| P1 CRIT-002 (validation depth) | Operator: list models via control-plane `ListFoundationModels` |
| P1 CRIT-003 (field name) | `endpoint` (precedent cited); test renamed |
| P1 CRIT-004 (onboarding) | FR-015; both onboarding endpoints |
| P1 CRIT-005 (contract surface) | FR-005: 5 schemas enumerated |
| P1 CRIT-006 (partial-update) | FR-016; endpoint-change re-validates for all archetypes |
| P1 CRIT-007 (FR-002 unverifiable) | FR-018: `ValidateProvider`, `ValidateKey` untouched, golden file |
| P1 CRIT-008 (SC-006 teeth) | SC-006: **CI gate** grepping the PR body (CODEOWNERS can't enforce content) |
| P1 MAJ-001 (auth_kind home / HC #6) | FR-001 publishes the table; FR-020 explicit repair behaviour |
| P1 MAJ-002 (SPA archetype source) | `Provider.yaml` in contract scope |
| P1 MAJ-003 (region mechanism) | FR-019: semantic via the list-models probe |
| P1 MAJ-004 (per-model entitlement) | Probe is account-level; per-model is runtime |
| P1 MAJ-005 (AWS SDK logging) | FR-010: redacting slog sink + error wrapper; SC-007 covers gateway.log |
| P1 MAJ-006 (lite decision) | FR-011: Bedrock stays in lite (decision documented) |
| P1 MAJ-007..010, MIN, OBS | Incorporated (see FRs/SCs) |
| **P2 CRIT-N1** (wrong SDK service) | **FR-003 + FR-021**: add `service/bedrock` control-plane module (operator-approved); correct the service name; IAM `bedrock:ListFoundationModels`; bump AWS SDK to latest |
| **P2 CRIT-N2** (CODEOWNERS can't enforce PR content) | **SC-006**: replaced with a CI status check that greps the PR body for the verification section + file |
| **P2 CRIT-N3** (Go string switches aren't exhaustive) | **FR-001**: typed `Archetype` enum + `ResolveArchetype(string)(Archetype,error)` error-on-unknown + `exhaustive` linter (the real Go idiom) — not a raw-string switch |
| P2 MAJ-N1 (custom-id repair behaviour) | FR-020 explicit: backfill `api_key` only for entries with a stored key; WARN + leave unset otherwise |
| P2 MAJ-N2 (lite baseline undercounts) | SC-004: delta re-measured at impl after the control-plane module is added (target ≤ ~6 MB) |
| P2 MAJ-N4 (contract test covers ½ of FR-005) | Test #31 expanded to assert all 5 schema changes + atomic-commit CI gate |
| P2 MAJ-N5 (api_key endpoint-change SSRF hole) | FR-016/FR-017 cover endpoint changes for **all** archetypes, including `api_key` |
| P2 MAJ-N6 (ValidateInput boundary) | FR-018 specifies `ValidateInput` gains a typed `AuthKind` field |
| P2 MAJ-N8 (atomic-commit enforcement) | SC-005 + new CI contract-staleness gate |
| **P3 CRIT-N4** (live-catalogue flip is a no-op) | **FR-022**: backend `has_models_endpoint` MUST be true for `bedrock` (ListFoundationModels is a live list), overriding the `GetDefaultAPIBase != ""` derivation at `rest.go:5669` (no `case "bedrock":` in the switch at `factory_provider.go:496`); Test #39 asserts `GET /api/v1/providers` returns Bedrock with `has_models_endpoint: true` |
| P3 MAJ-N9 (SC-006 under-specified) | SC-006: workflow path `.github/workflows/pr.yml`, branch-protection required-check, exemption for doc-only changes |
| P3 MAJ-N10 (FR-018 Path A) | FR-018 acknowledges Path A (`AuthKind` on `ValidateInput`); "literally untouched" = body-level, field added |
| P3 MAJ-N11 (api_key endpoint-change behavior) | FR-016: api_key endpoint-only change = SSRF re-check, no billable re-probe (key unchanged) |
| P3 MIN-N4/N5, OBS-N4 | Acknowledged in Assumptions (exhaustive-linter config; FR-020 wording; lite baseline re-measured) |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `providerUpdateHandler` (`pkg/gateway/rest.go:5504-5556`) | **modifies** | `keyChanged` gate + SSRF at `:5532` (verified inside the gate) + `ValidateKey` call. Epicenter of CRIT-001/006/N5 |
| `providers.ValidateKey` (`pkg/providers/validate.go:483`) | **unchanged** | Untouched to guarantee FR-002. Calls `FetchModels` (`:499`) — the pattern mirrored by the new dispatcher |
| `providers.ValidateProvider` | **adds** (new) | Archetype dispatcher over a typed `Archetype`; delegates `api_key` → `ValidateKey` (FR-018) |
| `providers.Archetype` (typed enum) + `ResolveArchetype` | **adds** (new) | `ResolveArchetype(string) (Archetype, error)` errors on unknown; dispatch switch is over the typed enum, `exhaustive`-linter-enforced (CRIT-N3) |
| `factory_provider.go` `case "bedrock:"` | unchanged | Already complete: `api_base` → region-or-endpoint (`:153`) |
| `pkg/providers/bedrock/provider_bedrock.go` | **modifies** | Adds `aws-sdk-go-v2/service/bedrock` import + a `ListFoundationModels` probe; today it imports only `…/bedrockruntime` (`:24-26`) |
| `rest_onboarding.go:131-133, :491-493` | **modifies** | Separate `api_key required` enforcement on onboarding (CRIT-004) |
| `providers_catalog.json` + `catalog.go` SoT | **modifies** | Add `bedrock`; backfill `auth_kind` on all entries (MAJ-007) |
| `catalog_test.go:151-152` | **modifies** | Invert exclusion; correct rationale |
| `src/lib/agents/providerCatalog.ts:16` | **modifies** | Flip `bedrock` from `'manual'` → `'live'` catalogue mode now that a model list exists |
| `ProviderUpdateRequest.yaml` | **modifies** (contract) | + `endpoint` |
| `ProviderCatalogEntry.yaml` | **modifies** (contract) | + `auth_kind` |
| `Provider.yaml` | **modifies** (contract) | + `auth_kind` (SPA source) |
| `OnboardingCompleteRequest.yaml` | **modifies** (contract) | relax `api_key` required → conditional |
| `ProbeProviderRequest.yaml` | **modifies** (contract) | relax `api_key` required → conditional |
| `go.mod` | **modifies** | Add `aws-sdk-go-v2/service/bedrock`; bump all `aws-sdk-go-v2` modules to latest (FR-021) |
| `ProvidersSection.tsx:387-392` | **extends** | Render by `auth_kind` (region for `aws_credential_chain`) |
| `factory_provider_test.go:1157-1160` | **modifies** | Unskip `bedrock`; remove stale rationale (MIN-003) |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents (d=1) | Indirect (d=2) |
|----------------|------------|--------------------------|----------------|
| `rest.go` handler (SSRF + dispatch + partial-update) | **CRITICAL** | Every `PUT /api/v1/providers/{id}` | All 23 existing providers |
| `rest_onboarding.go` (two endpoints) | **HIGH** | First-run onboarding | SC-001 |
| `go.mod` (new control-plane module + version bump) | **MEDIUM** | `provider_bedrock.go`; `govulncheck`; supply-chain surface | All AWS-calling code |
| `ValidateKey` | **NONE** (untouched) | `ValidateProvider` delegates | all api_key validation |
| `ValidateProvider` + `Archetype` (new) | **MEDIUM** | PUT handler, onboarding | probe endpoint |
| 5 contract schemas | **MEDIUM** | Generated Go + TS (atomic regen) | SPA, contract tests |
| `providerCatalog.ts` flip (manual→live) | **LOW** | Model picker for Bedrock | catalogue-mode tests |
| catalog + tag removal | **LOW** | catalog tests | picker |

> **CRITICAL-risk flag:** `rest.go` is on the path of *every* provider save for *all 23* providers. The dominant risk is silently weakening validation or the SSRF guard for the 22 API-key providers. FR-002, FR-017, FR-018 + the golden-file and SSRF-on-keyless tests prevent that.

---

## User Stories & Acceptance Criteria

### US-1 — Select and use Bedrock from the UI (P0)
An operator in an AWS environment wants to pick AWS Bedrock, set a region, and chat — without a fake key, without a special binary, and with a live model picker. **Why P0**: the operator's stated goal. **Independent test**: valid AWS creds → Settings → select Bedrock → region → save → chat.

**Acceptance**:
1. Default build → Bedrock appears in the picker.
2. Bedrock sheet shows a **region** field, no API key field.
3. Region + the model picker's live list → save succeeds, no `api_key is required`.
4. Bedrock configured → chat returns a response.
5. Fresh install → Bedrock selectable **during onboarding** (no 400).

### US-2 — Validation still proves the provider works (P0)
The same confidence as for OpenAI: if save succeeds, it's genuinely usable. **Why P0**: equal to US-1; US-1 without it is a fail-open hole. **Independent test**: Bedrock on a host with **no** AWS creds → rejected, not silently accepted.

**Acceptance**:
1. No resolvable AWS creds → rejected naming the credential chain, nothing persisted.
2. Valid creds, invalid region → rejected naming the region.
3. Valid creds + region → lists models, passes, persists.
4. Any of the 22 API-key providers → validation **exactly** as before (golden-file, FR-002/018).
5. Bedrock save with an **internal endpoint** URL → rejected by SSRF before any probe.
6. Existing Bedrock entry, region changed → re-validates.

### US-3 — Ollama stops demanding a fake key (P1)
Local Ollama added without a dummy key. **Why P1**: proves the archetype design is general, not a Bedrock special case. Ollama + Bedrock ship together (MIN-004). **Independent test**: Ollama + endpoint, no key → saves, works.

**Acceptance**: 1. reachable Ollama, no key → saves. 2. unreachable → rejected.

### US-4 — One build, no tag (P1)
Bedrock present in the default binary. **Why P1**: build consolidation. **Independent test**: `make build`, no tags → Bedrock works.

**Acceptance**: 1. default build → Bedrock works without the tag. 2. tag removed → `-tags bedrock` still builds (inert).

---

## Behavioral Contract

**Primary**
- A provider save is validated by its **credential archetype** before persisting.
- `api_key` → requires non-empty key, probes a completion endpoint — **unchanged** (delegates to `ValidateKey`).
- `aws_credential_chain` → requires a region, resolves the AWS chain, lists Bedrock models (control-plane, free) — mirrors `ValidateKey`'s `FetchModels`.
- `none` → requires a reachable endpoint.
- Validation passes → persists, selectable.

**Error**
- Any archetype failure → persists **nothing**, returns a message naming the precondition — never raw credential material.
- AWS credential failure → reports the chain failure without leaking credential values or SDK traces.
- Missing required archetype field → rejects before any network call.

**Boundary**
- Endpoint field resolves to a URL → SSRF-checked **regardless of archetype or api_key presence** (FR-017).
- Any provider's endpoint/region changes → re-validates (FR-016 covers **all** archetypes, incl. `api_key` — MAJ-N5).
- Existing `api_key` provider partial update (model/label only, key omitted) → unchanged, no re-probe.
- Unknown/empty/missing `auth_kind` → rejected via typed-enum resolution (no raw-string `default` — CRIT-N3).

---

## Edge Cases

- Region as a full URL (`https://bedrock-runtime.us-east-1.amazonaws.com`) → accepted; SSRF-checked (FR-017).
- Non-default AWS partition (`aws-cn-`, `aws-us-gov-`) → accepted (`factory_provider.go:156`).
- Structurally invalid region (`not a region!!`, 256 chars, no hyphen) → not regex-rejected (goes stale); caught semantically by the list-models probe, region named (FR-019).
- Valid creds, account has **no** Bedrock access at all → `ListFoundationModels` returns an authorization error → "account not enabled for Bedrock" (FR-009, account-level).
- Account can list models but lacks a specific operator-added model → save succeeds; that model fails at chat time (documented runtime concern — MAJ-004). With the live picker, the operator picks from models the list returned, so this is largely self-resolving.
- AWS credential resolution hangs (IMDS on non-EC2) → bounded by **10s** save-time timeout (OBS-002).
- Two concurrent provider PUTs → single-writer config closure serializes (`rest.go:5593`); concurrency test.
- `auth_kind` absent on a persisted entry → enumerated one-shot config-load repair (FR-020).
- Custom OpenAI-compatible id not in the catalog mapping → reads persisted `auth_kind`; new ones must set it explicitly (reject if missing — FR-020).
- Empty region → rejected before any AWS call.
- Credentials rotate/expire after save → runtime failure, config untouched.
- Existing tag-built Bedrock config after tag removal → works via id→archetype mapping; no migration (FR-013).

---

## Explicit Non-Behaviors

- Must **not** skip validation for non-API-key providers.
- Must **not** weaken/reorder/fail-open validation for the 22 API-key providers (`ValidateKey` untouched — FR-018).
- Must **not** bypass the SSRF guard when api_key is absent (FR-017).
- Must **not** store/transmit a placeholder key for keyless providers.
- Must **not** implement a Bedrock-specific branch in the gate or UI.
- Must **not** read/log/echo AWS credential material — including SDK-emitted logs and `%w`-wrapped chains (FR-010).
- Must **not** add a new wire protocol (bedrock → existing `anthropic` wire).
- Must **not** require a live AWS account for CI.
- Must **not** do a per-model completion probe at save (account-level list only).
- Must **not** implement `ChatStream` (deferred).
- Must **not** use a raw-string `switch` for archetype dispatch (HC #6) — typed enum + `exhaustive` linter (CRIT-N3).

---

## Integration Boundaries

### AWS Bedrock Runtime (chat)
- **Data in**: region/endpoint, model slug, messages, tools. Creds resolved out-of-band.
- **Contract**: `bedrockruntime` Converse via `aws-sdk-go-v2/service/bedrockruntime` (existing dep).
- **SDK logging**: wired to a redacting slog sink; errors from `config.LoadDefaultConfig`/clients pass through a redaction wrapper (FR-010).

### AWS Bedrock Control Plane (probe + picker)
- **Data in**: region. **Data out**: the foundation-model list.
- **Contract**: `bedrock` `ListFoundationModels` via `aws-sdk-go-v2/service/bedrock` (**new dep — FR-021**). Requires IAM `bedrock:ListFoundationModels`.
- **On failure**: authorization error → "account not enabled for Bedrock"; region error → region named.
- **Development**: simulated twin — a fake control-plane client returning a canned model list.

### SSRF guard (all archetypes)
- **Data in**: resolved endpoint URL (region forms aren't URLs; only `"://"` forms are checked).
- **Contract**: `ssrfChecker.CheckURL` as today, but for **every** save with an endpoint URL.
- **On failure**: 422 "provider endpoint not allowed (SSRF guard)".

### Local Ollama
- OpenAI-compatible HTTP; unreachable → save rejected. Mock server in tests.

---

## BDD Scenarios

### Feature: Credential-archetype-aware provider validation

#### Scenario: Bedrock appears in the picker with a live model list on a default build
**Traces to**: US-1 AS-1
**Category**: Happy Path
- **Given** a `make build` binary
- **When** the operator opens Settings → Providers → add
- **Then** `bedrock` is present with `auth_kind: aws_credential_chain`
- **And** its catalogue mode is `'live'` (models fetched from `ListFoundationModels`)

#### Scenario: Bedrock sheet shows a region field, hides the API key field
**Traces to**: US-1 AS-2
**Category**: Happy Path
- **Given** Bedrock is selected
- **When** the sheet renders
- **Then** a region input is shown and no API key input

#### Scenario: Saving Bedrock lists models and succeeds
**Traces to**: US-1 AS-3; US-2 AS-3
**Category**: Happy Path
- **Given** AWS credentials resolve and region `us-east-1`
- **When** the operator saves
- **Then** status 200
- **And** validation called `ListFoundationModels` (no Converse/billable call)
- **And** the provider persists with `auth_kind: aws_credential_chain`
- **And** no credential entry with prefix `bedrock_` exists in the store

#### Scenario: Saving Bedrock without resolvable credentials is rejected
**Traces to**: US-2 AS-1
**Category**: Error Path
- **Given** no resolvable AWS credentials and a valid region
- **When** saved
- **Then** 422 naming the credential chain; nothing persisted; no credential material in message or gateway.log

#### Scenario: Saving Bedrock without a region is rejected before any AWS call
**Traces to**: US-2 AS-2
**Category**: Error Path
- **Given** empty region
- **When** saved
- **Then** 422; no SDK call

#### Scenario: Structurally invalid region is caught by the list-models probe
**Traces to**: US-2 AS-2
**Category**: Edge Case
- **Given** an invalid region string
- **When** saved
- **Then** the probe fails and the message names the region (FR-019)

#### Scenario: Account with no Bedrock access is reported at account level
**Traces to**: US-2 AS-3
**Category**: Edge Case
- **Given** valid creds but no Bedrock access
- **When** saved
- **Then** `ListFoundationModels` returns an authorization error reported as "account not enabled for Bedrock"

#### Scenario: AWS credential resolution that hangs is bounded by 10s
**Traces to**: US-2 AS-1
**Category**: Edge Case
- **Given** the chain does not respond
- **When** saved
- **Then** the request fails within 10s; message identifies a timeout

#### Scenario Outline: API-key providers validate exactly as before (golden file)
**Traces to**: US-2 AS-4
**Category**: Happy Path (regression)
- **Given** provider `<provider_id>` archetype `api_key`
- **When** saved with `<key_state>`
- **Then** the full HTTP response matches the pre-change golden `<golden>`

**Examples**:

| provider_id | key_state | golden |
|---|---|---|
| openai | valid key | 200 + probe |
| anthropic | valid key | 200 + probe |
| openrouter | empty key, new | 422 `api_key is required` |
| openai | whitespace key | 422, no network |
| groq | omitted key, existing | 200, no probe |

#### Scenario: Bedrock save with an internal endpoint URL is rejected by SSRF
**Traces to**: US-2 AS-5
**Category**: Error Path (security)
- **Given** endpoint `http://169.254.169.254/latest/meta-data/`, no api_key
- **When** saved
- **Then** 422 "provider endpoint not allowed (SSRF guard)"; no probe; nothing persisted

#### Scenario: An api_key provider's endpoint change is SSRF-checked too
**Traces to**: US-2 AS-5 (MAJ-N5)
**Category**: Error Path (security)
- **Given** an existing OpenAI provider whose endpoint is changed to an internal URL
- **When** saved
- **Then** 422 SSRF guard (the new endpoint-change path covers all archetypes)

#### Scenario: Existing Bedrock provider, region changed, re-validates
**Traces to**: US-2 AS-6
**Category**: Edge Path
- **Given** a validated Bedrock entry region `us-east-1`
- **When** region changed to `ap-southeast-3` and saved (no api_key)
- **Then** the archetype validator re-runs for `ap-southeast-3`

#### Scenario: Ollama saves keyless when reachable
**Traces to**: US-3 AS-1
**Category**: Happy Path
- **Given** a reachable Ollama
- **When** saved with no api_key
- **Then** 200; no `ollama_` credential entry

#### Scenario: Ollama rejected when unreachable
**Traces to**: US-3 AS-2
**Category**: Error Path
- **Given** no Ollama listening
- **When** saved
- **Then** 422 citing an unreachable endpoint

#### Scenario Outline: Unknown / empty / missing auth_kind is rejected (typed enum, no default)
**Traces to**: FR-001
**Category**: Error Path
- **Given** auth_kind `<kind>`
- **When** `ResolveArchetype` runs
- **Then** it returns an error; no archetype is assumed

**Examples**:

| kind | note |
|---|---|
| `oauth` | unrecognized |
| `""` | zero value |
| *(unset)* | missing |

#### Scenario: Bedrock works in a default build with no build tag
**Traces to**: US-4 AS-1
**Category**: Happy Path
- **Given** a binary built without the `bedrock` tag
- **When** a Bedrock provider is configured and a turn runs
- **Then** it constructs successfully; no "build with -tags bedrock" error

#### Scenario: Onboarding accepts Bedrock without an api_key
**Traces to**: US-1 AS-5
**Category**: Happy Path
- **Given** a fresh install, Bedrock selected during onboarding, region, no api_key
- **When** onboarding completes
- **Then** not 400 `provider.api_key is required`; the archetype validator runs

#### Scenario: Onboarding probe accepts a keyless provider
**Traces to**: US-1 AS-5
**Category**: Happy Path
- **Given** Bedrock probed during onboarding
- **When** `/onboarding/probe-provider` is called with no api_key
- **Then** the probe runs the archetype validator (not the 400)

#### Scenario: Custom provider without an explicit auth_kind is rejected
**Traces to**: FR-020
**Category**: Error Path
- **Given** a non-catalog id with no `auth_kind`
- **When** saved
- **Then** 422 requiring an explicit `auth_kind`

#### Scenario: Contract regeneration carries all five changes and schema-validates
**Traces to**: FR-005
**Category**: Contract
- **Given** the 5 schema changes committed with regenerated artifacts in one commit
- **When** `make verify-contracts` runs
- **Then** exit 0
- **And** a CI staleness gate confirms `pkg/api/generated/` + `src/lib/api/generated/` match the schemas

#### Scenario: Concurrent Bedrock saves do not corrupt config
**Traces to**: FR-016 (concurrency)
**Category**: Edge Case
- **Given** two simultaneous `PUT /api/v1/providers/bedrock`
- **When** both complete
- **Then** the persisted config is consistent (single-writer serialized)

---

## Test-Driven Development Plan

### Test Hierarchy
| Level | Scope | Purpose |
|---|---|---|
| Unit | archetype resolution (typed), dispatch, per-archetype validators, catalog integrity, SSRF dispatch, redaction | Isolated logic |
| Integration | `PUT /api/v1/providers/{id}`, onboarding, with stubbed AWS control-plane + runtime + HTTP | Gate + validator + SSRF + persistence |
| E2E | Settings → save → chat | Full workflow |

### Test Implementation Order

| # | Test | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestArchetype_ResolveRejectsUnknown` | Unit | Unknown/empty/missing auth_kind | `ResolveArchetype` errors on `oauth`/`""`/unset (CRIT-N3) |
| 2 | `TestArchetype_Dispatch_ExhaustiveLinter` | Unit | — | `exhaustive` linter passes on the typed-enum switch (no raw-string default) |
| 3 | `TestArchetypeResolution_IdMapping` | Unit | — | Published id→archetype table matches code |
| 4 | `TestCatalog_BedrockPresent_LiveMode` | Unit | Bedrock in picker | `bedrock` present, `auth_kind` correct, catalogue mode `'live'` |
| 5 | `TestCatalog_AuthKindCoverage_All24Entries` | Unit | — | Every entry carries explicit `auth_kind` (MAJ-007) |
| 6 | `TestValidate_APIKeyArchetype_GoldenFile` | Unit | Outline | Full HTTP response vs golden for a representative api_key provider |
| 7 | `TestValidateKey_UnchangedSignature` | Unit | Outline | `ValidateKey` untouched; `ValidateProvider` delegates api_key (FR-018) |
| 8 | `TestValidate_APIKey_EmptyShortCircuits` | Unit | Outline (whitespace) | Preserves `validate.go:485-491` no-network |
| 9 | `TestValidate_CredentialChain_MissingRegion` | Unit | Region missing | No SDK call |
| 10 | `TestValidate_CredentialChain_NoCredentials` | Unit | No creds | Fake chain returns failure |
| 11 | `TestValidate_CredentialChain_ListsModels_ControlPlane` | Unit | Bedrock save succeeds | Probe calls **control-plane** `ListFoundationModels`, not `bedrockruntime` Converse (CRIT-N1) |
| 12 | `TestValidate_CredentialChain_NoBedrockAccess` | Unit | No Bedrock access | Authorization error → account-level report |
| 13 | `TestValidate_CredentialChain_InvalidRegion` | Unit | Invalid region | Probe fails, region named (FR-019) |
| 14 | `TestValidate_CredentialChain_Timeout_10s` | Unit | Hanging | 10s bound (OBS-002) |
| 15 | `TestValidate_CredentialChain_NoSecretLeak` | Unit | Message scrub | Validation message **and** slog sink scrubbed (MAJ-005) |
| 16 | `TestValidate_CredentialChain_RedactsSDKErrorWrap` | Unit | — | Fake chain error w/ marker; marker absent from wrapped error + log |
| 17 | `TestValidate_NoneArchetype_Reachable` | Unit | Ollama reachable | Mock HTTP |
| 18 | `TestValidate_NoneArchetype_Unreachable` | Unit | Ollama unreachable | Rejected |
| 19 | `TestSSRF_FiresOnKeylessEndpointSave` | Unit | Internal endpoint | SSRF runs when `keyChanged==false` (CRIT-001) |
| 20 | `TestSSRF_FiresOnAPIKeyEndpointChange` | Unit | api_key endpoint SSRF | Endpoint change SSRF-checked for api_key too (MAJ-N5) |
| 21 | `TestProviderPUT_Bedrock_NoAPIKey_NoStoreEntry` | Integration | Bedrock save | 200; no `bedrock_` entry (MIN-005) |
| 22 | `TestProviderPUT_Bedrock_RejectedNoCreds` | Integration | Bedrock rejected | 422; nothing persisted |
| 23 | `TestProviderPUT_Bedrock_InternalEndpoint_BlockedBySSRF` | Integration | Internal endpoint | 422 SSRF; no probe |
| 24 | `TestProviderPUT_Bedrock_RegionChange_Revalidates` | Integration | Region change | Re-validation runs (CRIT-006) |
| 25 | `TestProviderPUT_APIKeyProviders_GoldenRegression` | Integration | Outline | All existing behavior preserved |
| 26 | `TestProviderPUT_Ollama_NoKey` | Integration | Ollama keyless | No placeholder key |
| 27 | `TestProviderPUT_CustomProvider_NoAuthKind_Rejected` | Integration | Custom rejected | Non-catalog id w/o auth_kind → 422 |
| 28 | `TestProviderPUT_Concurrent_NoCorruption` | Integration | Concurrent | Single-writer serializes |
| 29 | `TestOnboarding_Complete_BedrockAccepted` | Integration | Onboarding accepts | No 400 (CRIT-004) |
| 30 | `TestOnboarding_Probe_BedrockAccepted` | Integration | Onboarding probe | Archetype validator runs |
| 31 | `TestContract_AllFiveSchemas_Changed` | Integration | Contract regen | Asserts all 5 schema changes present + `api_key` not `required` where `bedrock` is enumerated (MAJ-N4) |
| 32 | `TestContract_AtomicCommit_GeneratedNotStale` | Integration | Contract regen | CI staleness gate: generated artifacts match schemas (MAJ-N8) |
| 33 | `TestConfigLoad_BackfillsAuthKind_OneShot` | Unit | — | Pre-existing entries get enumerated `auth_kind`; idempotent; logged |
| 34 | `TestConfigLoad_CustomIdWithoutKey_NotAssumed` | Unit | Custom repair (MAJ-N1) | Entry not in mapping, no stored key, no auth_kind → WARN, left unset, not defaulted |
| 35 | `TestFactory_Bedrock_UnskippedInDefaultBuild` | Integration | No tag | Unskip `bedrock` in `TestEveryProbeProviderBuilds`; constructs without tag (MIN-003) |
| 36 | `TestGoMod_BedrockControlPlane_Latest` | Integration | FR-021 | `service/bedrock` present; all `aws-sdk-go-v2` at latest |
| 37 | `ProvidersSection.archetype.test.tsx` | Unit (FE) | Region/key | Parameterized across 3 archetypes (MAJ-002) |
| 38 | E2E: configure Bedrock → chat | E2E | US-1 AS-4 | Playwright, stubbed Bedrock |
| 39 | `TestProviderGET_Bedrock_HasModelsEndpoint_True` | Integration | Bedrock in picker (live) | `GET /api/v1/providers` returns Bedrock with `has_models_endpoint: true` (CRIT-N4) — replaces the catalog-only Test #4 for the live-mode claim |

### Test Datasets

#### `auth_kind` resolution / rejection
| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | `api_key` | valid | api_key validator (→ ValidateKey) | Outline |
| 2 | `aws_credential_chain` | valid | AWS validator | Bedrock save |
| 3 | `none` | valid | reachability validator | Ollama |
| 4 | `oauth` | unrecognized | error | Unknown auth_kind |
| 5 | `""` | empty | error | Unknown auth_kind |
| 6 | *(unset)* | missing | error | Unknown auth_kind |
| 7 | `API_KEY` | wrong case | error | Unknown auth_kind |
| 8 | absent + id in mapping | missing field | derive from id table | Migration |

#### AWS region input
| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | `us-east-1` | happy | accepted as region | Bedrock save |
| 2 | `https://bedrock-runtime.us-east-1.amazonaws.com` | endpoint | accepted + SSRF-checked | URL edge |
| 3 | `""` | min | reject pre-call | Region missing |
| 4 | `"   "` | whitespace | reject pre-call | Region missing |
| 5 | `not a region!!` | structural | probe fails, region named | Invalid region |
| 6 | `aws-cn-…` form | partition | accepted | Edge |
| 7 | `us-east-1\n` | paste | trimmed, accepted | Bedrock save |
| 8 | `ap-southeast-3` | less-common | accepted | Bedrock save |

#### API-key regression (golden)
| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | valid key, new | happy | 200 + probe | Outline |
| 2 | `""`, new | min | 422 `api_key is required` | Outline |
| 3 | `"   "`, new | whitespace | 422, no network | Outline |
| 4 | omitted, existing | partial | 200, no probe | Outline |
| 5 | valid key, existing | re-probe | 200 + re-probe | Outline |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test | Notes |
|---|---|---|---|
| New API-key provider w/o key → 422 | `provider_validation_test.go` | `TestProviderPUT_APIKeyProviders_GoldenRegression` | verbatim |
| Empty key short-circuits | `validate_test.go` | `TestValidate_APIKey_EmptyShortCircuits` | no AWS path |
| Partial update skips probe | handler tests | dataset row 4 | unchanged for api_key |
| SSRF on api_base | `rest.go` path | `TestSSRF_FiresOnKeylessEndpointSave` + `TestSSRF_FiresOnAPIKeyEndpointChange` | now covers all archetypes |
| Catalog entry count | `catalog_test.go` | updated (24) | updated, not deleted |
| Bedrock excluded | `catalog_test.go:151-152` | **inverted** + rationale corrected | Azure precedent |
| Logo coverage | `TestCatalog_LogoSlugCoverage` | `bedrock` → `intentionalLettermark` (OBS-003) | ollama precedent |
| Bedrock skipped in factory test | `factory_provider_test.go:1159` | **unskipped** + comment removed (MIN-003) | |

---

## Functional Requirements

- **FR-001**: The system MUST resolve a credential archetype via a **typed `Archetype` enum** and a `ResolveArchetype(string) (Archetype, error)` that **errors on unknown/empty/missing** values — never a raw-string `switch` with a `default` (CRIT-N3 / HC #6). The dispatch switch is over the typed enum and MUST pass the `exhaustive` linter. The published id→archetype table is: `api_key` (the 22 API-key providers), `none` (ollama), `aws_credential_chain` (bedrock).
- **FR-002**: `ValidateKey` MUST remain literally untouched; `api_key` validation delegates to it.
- **FR-003**: For `aws_credential_chain`, the system MUST require a region, resolve the AWS credential chain, and list Bedrock models via the **control-plane `bedrock` service's `ListFoundationModels`** (NOT `bedrockruntime`) before persisting — mirroring `ValidateKey`'s `FetchModels`. The IAM principal MUST have `bedrock:ListFoundationModels`.
- **FR-004**: The system MUST add a `bedrock` catalog entry (`auth_kind: aws_credential_chain`), backfill `auth_kind` onto every existing entry atomically (MAJ-007), correct the `catalog_test.go:151-152` rationale, add `bedrock` to `intentionalLettermark` (OBS-003), and flip Bedrock's catalogue mode from `'manual'` to `'live'` in `src/lib/agents/providerCatalog.ts` — which requires the backend `has_models_endpoint` override in FR-022 (the frontend honours the backend boolean over its own list, so the frontend flip alone is a no-op — CRIT-N4).
- **FR-005**: The system MUST make these **5 contract changes in one atomic commit** with regenerated `pkg/api/generated/` + `src/lib/api/generated/`: (1) `endpoint` on `ProviderUpdateRequest.yaml`; (2) `auth_kind` on `ProviderCatalogEntry.yaml`; (3) `auth_kind` on `Provider.yaml`; (4) relax `api_key` required→conditional in `OnboardingCompleteRequest.yaml`; (5) same in `ProbeProviderRequest.yaml`. Wire field = `endpoint` (precedent `OnboardingCompleteRequest.yaml:34` / `ProbeProviderRequest.yaml:83`); persisted = `api_base`.
- **FR-006**: The UI MUST render fields by `auth_kind` from the `Provider` response: region for `aws_credential_chain`, no API key field for keyless archetypes.
- **FR-007**: No placeholder key stored/transmitted for keyless providers.
- **FR-008**: AWS credential resolution bounded by a **10s** save-time timeout, reported distinctly from invalid creds.
- **FR-009** *(account-level)*: Distinguish "credentials invalid" from "account not enabled for Bedrock" via the `ListFoundationModels` response. Per-model entitlement is a runtime concern.
- **FR-010**: No credential material in any message or log (SEC-16). AWS SDK logger wired to a redacting slog sink; errors from `config.LoadDefaultConfig`/clients pass through a redaction wrapper.
- **FR-011**: Remove the `bedrock` tag so the default build includes Bedrock. **Decision (MAJ-006): Bedrock stays in `make build-lite`.** A stale `-tags bedrock` remains a successful no-op.
- **FR-012**: `none`-archetype validated by endpoint reachability.
- **FR-013** *(MUST)*: Existing tag-built Bedrock configs keep working via the id→archetype mapping; no migration.
- **FR-014**: `ChatStream` deferred.
- **FR-015** *(CRIT-004)*: Apply archetype-aware validation to `POST /onboarding/complete` and `POST /onboarding/probe-provider`, relaxing the `api_key required` checks at `rest_onboarding.go:131-133` and `:491-493` for non-`api_key` archetypes.
- **FR-016** *(CRIT-006 + MAJ-N5 + MAJ-N11)*: For **any** archetype, the system MUST re-validate when the endpoint or region changes. The `keyChanged` gate at `rest.go:5504` MUST be supplemented with an `endpointChanged` gate. (Covers `api_key` providers' endpoint changes too — MAJ-N5.) **For an `api_key` provider whose endpoint changes but whose key is unchanged (partial update):** the re-check is **SSRF-only** (no billable re-probe — the persisted key is untouched and was already validated); for a `aws_credential_chain`/`none` provider, the full archetype validator re-runs because the endpoint/region IS the credential (MAJ-N11).
- **FR-017** *(CRIT-001)*: When the endpoint field resolves to a URL, the system MUST run `ssrfChecker.CheckURL` before any validator, **regardless of archetype or api_key presence** — moved out of the `if keyChanged` block.
- **FR-018** *(CRIT-007/N6/N10)*: Add `ValidateProvider(ctx, ValidateInput) ValidationResult` dispatching on a typed `AuthKind` field of `ValidateInput`; `ValidateKey` stays unchanged; api_key delegates to it. A committed golden file asserts full-HTTP-response equivalence for a representative api_key provider. **Path A** (typed field on the existing `ValidateInput`) is chosen over a separate struct — "literally untouched" means `ValidateKey`'s body is unchanged; the `ValidateInput` struct gains one field, populated only by `ValidateProvider`, never by existing `ValidateKey` callers (MAJ-N10).
- **FR-019** *(MAJ-003)*: Region validation is semantic (no regex allowlist): invalid regions caught by the list-models probe, message names the region.
- **FR-020** *(MAJ-001/N1)*: A custom OpenAI-compatible id not in the catalog mapping MUST carry an explicit `auth_kind`; saving one without it is rejected. Config-load repair backfills `auth_kind: api_key` **only** for persisted entries that have a stored api_key credential (evidence: every pre-feature custom provider was api_key-based); entries with no stored key and no mapping are left unset with a WARN and rejected at next save (not defaulted). One-shot, idempotent, logged.
- **FR-021** *(CRIT-N1 + operator decision)*: The system MUST add `aws-sdk-go-v2/service/bedrock` (control-plane) as a dependency and **bump all `aws-sdk-go-v2` modules to their latest versions** in the same change. The previous "AWS SDK already a dep, no new surface" framing is corrected: the control-plane module is a **new** module in the same SDK family (pure Go, AWS-maintained), adding a modest amount to the binary (re-measured at impl — see SC-004).
- **FR-022** *(CRIT-N4)*: The system MUST report `has_models_endpoint: true` for `bedrock` on `GET /api/v1/providers` and the PUT response, because `ListFoundationModels` (FR-003/FR-021) provides a live model list. This overrides the existing `hasEndpoint := GetDefaultAPIBase(name) != ""` derivation at `rest.go:5669` (and the parallel sites at `:5397`, `:5421`, `:6017`), which returns `false` for Bedrock because `GetDefaultAPIBase` has no `case "bedrock":` (`factory_provider.go:496`). Without this, the SPA's `providerCatalogMode()` (`providerCatalog.ts:72`) reads `has_models_endpoint=false` → `'manual'` regardless of the frontend `LIVE_LISTING_PROVIDER_IDS` set, so the live picker never renders and US-1 AS-1 cannot pass.

---

## Success Criteria

- **SC-001**: On valid AWS creds, an operator completes select → region → save → chat via the UI (including onboarding) with no file editing and no fake credentials; the model picker shows a live Bedrock list.
- **SC-002**: Committed golden file matches post-change byte-for-byte for a representative api_key provider; the blank-key 422 is character-identical.
- **SC-003**: Saving Bedrock without creds returns 422 and persists nothing — config byte-identical before/after.
- **SC-004**: Default build supports Bedrock; the size increase (tag removal + control-plane module) is re-measured at implementation and documented in the PR — target ≤ ~6 MB (the +4.27 MB figure was tag-removal only; the control-plane module adds more — MAJ-N2).
- **SC-005**: `make verify-contracts` exits 0; the 5 schema changes + regenerated artifacts are in one atomic commit; a CI staleness gate confirms generated files are not stale (MAJ-N8).
- **SC-006** *(teeth — CRIT-N2 + MAJ-N9)*: A real-AWS verification recording exists at `docs/internal/verification/bedrock-real-aws-<YYYY-MM-DD>.md` (region, AWS account ID last-4, model slug, chat transcript, operator GitHub handle, made on an account that did NOT previously have Bedrock model access). A **CI status check** in `.github/workflows/pr.yml` greps `github.event.pull_request.body` for a `## Real-AWS verification` section linking that file and fails the PR if absent — but **only when the PR diff touches `pkg/providers/bedrock/**` or `pkg/gateway/rest*.go`** (so doc-only / unrelated changes don't trigger it). CODEOWNERS cannot enforce PR-body content, so this is a workflow check (CRIT-N2). The check MUST be configured as a **required status check** in the `main`/release branch-protection rules (documented in the PR, configured by the operator with repo admin).
- **SC-007**: No credential material in any message or in `gateway.log` during a credential-resolution failure (automated scrub over both surfaces).
- **SC-008**: Ollama with no key saves; no `ollama_` credential entry.
- **SC-009**: `aws-sdk-go-v2/service/bedrock` is present and all `aws-sdk-go-v2` modules are at latest (FR-021).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-2 | Unknown/empty/missing auth_kind rejected; Custom rejected | `TestArchetype_ResolveRejectsUnknown`, `TestArchetype_Dispatch_ExhaustiveLinter`, `TestArchetypeResolution_IdMapping`, `TestProviderPUT_CustomProvider_NoAuthKind_Rejected` |
| FR-002 | US-2 | Outline (golden) | `TestValidate_APIKeyArchetype_GoldenFile`, `TestValidateKey_UnchangedSignature`, `TestProviderPUT_APIKeyProviders_GoldenRegression` |
| FR-003 | US-1, US-2 | Bedrock lists models; No creds rejected | `TestValidate_CredentialChain_ListsModels_ControlPlane`, `TestValidate_CredentialChain_NoCredentials`, `TestProviderPUT_Bedrock_NoAPIKey_NoStoreEntry` |
| FR-004 | US-1 | Bedrock in picker (live) | `TestCatalog_BedrockPresent_LiveMode`, `TestCatalog_AuthKindCoverage_All24Entries` |
| FR-005 | — (contract) | Contract regen (all five) | `TestContract_AllFiveSchemas_Changed`, `TestContract_AtomicCommit_GeneratedNotStale` |
| FR-006 | US-1 | Sheet shows region, hides key | `ProvidersSection.archetype.test.tsx` |
| FR-007 | US-1, US-3 | Bedrock save; Ollama keyless | `TestProviderPUT_Bedrock_NoAPIKey_NoStoreEntry`, `TestProviderPUT_Ollama_NoKey` |
| FR-008 | US-2 | Hanging 10s | `TestValidate_CredentialChain_Timeout_10s` |
| FR-009 | US-2 | No Bedrock access | `TestValidate_CredentialChain_NoBedrockAccess` |
| FR-010 | US-2 | No creds; scrub | `TestValidate_CredentialChain_NoSecretLeak`, `TestValidate_CredentialChain_RedactsSDKErrorWrap` |
| FR-011 | US-4 | Default build + inert tag | `TestFactory_Bedrock_UnskippedInDefaultBuild` |
| FR-012 | US-3 | Ollama reachable/unreachable | `TestValidate_NoneArchetype_Reachable`, `TestValidate_NoneArchetype_Unreachable` |
| FR-013 | US-4 | Default build, no tag | `TestFactory_Bedrock_UnskippedInDefaultBuild` |
| FR-014 | — (deferred) | — | — |
| FR-015 | US-1 | Onboarding accepts; probe accepts | `TestOnboarding_Complete_BedrockAccepted`, `TestOnboarding_Probe_BedrockAccepted` |
| FR-016 | US-2 | Region change re-validates; api_key endpoint SSRF | `TestProviderPUT_Bedrock_RegionChange_Revalidates`, `TestSSRF_FiresOnAPIKeyEndpointChange` |
| FR-017 | US-2 | Internal endpoint SSRF | `TestSSRF_FiresOnKeylessEndpointSave`, `TestProviderPUT_Bedrock_InternalEndpoint_BlockedBySSRF` |
| FR-018 | US-2 | Outline (golden) | `TestValidate_APIKeyArchetype_GoldenFile`, `TestValidateKey_UnchangedSignature` |
| FR-019 | US-2 | Invalid region | `TestValidate_CredentialChain_InvalidRegion` |
| FR-020 | — (migration) | Custom rejected | `TestConfigLoad_BackfillsAuthKind_OneShot`, `TestConfigLoad_CustomIdWithoutKey_NotAssumed`, `TestProviderPUT_CustomProvider_NoAuthKind_Rejected` |
| FR-021 | US-1 | Bedrock in picker (live) | `TestGoMod_BedrockControlPlane_Latest` |
| FR-022 | US-1 | Bedrock in picker (live) | `TestProviderGET_Bedrock_HasModelsEndpoint_True` |

**Completeness check**: FR-001…FR-013, FR-015…FR-021 each have ≥1 BDD scenario and ≥1 test. FR-014 deferred by design.

---

## Ambiguity Warnings

All six prior ambiguities are **resolved** (see revision map). The dependency decision (control-plane + version bump) is operator-approved (2026-07-21). No remaining deferrable ambiguities.

---

## Evaluation Scenarios (Holdout)

> Post-implementation evaluation only. Not in the TDD plan or traceability matrix.

1. **Cold operator, real AWS** — fresh install, `~/.aws` profile, configure Bedrock via UI only → chat works, live model picker, no key field, no file edited. (Happy)
2. **Credentials removed after config** — delete creds → chat → clear provider error, config unchanged, no hang. (Error)
3. **Internal endpoint attempt** — set Bedrock endpoint to `http://169.254.169.254/...` → 422 SSRF, no probe, nothing persisted. (Error)
4. **Ollama with no key** — local Ollama → add via UI with no key → saves, works, no placeholder credential. (Happy)
5. **All existing providers still configurable** — OpenAI/Anthropic/OpenRouter as before, including the blank-key error. (Happy)
6. **Binary provenance + onboarding** — `make build`, `omnipus doctor`, onboard with Bedrock → doctor clean, onboarding accepts. (Edge)
7. **Region change without re-key** — validated Bedrock, change region only → re-validation runs. (Edge)
8. **Live picker reflects entitlement** — account with access to only some models → picker lists only those. (Edge)

---

## Assumptions
- Operator AWS credentials are provisioned out-of-band; Omnipus never manages them.
- CI has no AWS account; automated Bedrock tests use a simulated twin (fake control-plane + runtime clients). SC-006 covers real-AWS manually.
- ADR-053 is **Proposed**; this spec assumes Option C ratified.
- `ChatStream` out of scope.
- The existing `case "bedrock":` in `factory_provider.go` is correct and unchanged.
- The control-plane `bedrock` module is pure-Go and AWS-maintained, same security profile as `bedrockruntime`; adding it is consistent with Hard Constraints #1/#2 (single pure-Go binary).

## Clarifications
- **2026-07-21**: Gate intent = prove correct config; dispatches conditionally per archetype (keyless still validated).
- **2026-07-21**: Validation depth (operator) = list models, matching `ValidateKey`.
- **2026-07-21**: Dependency decision (operator) = add Bedrock control-plane module + bump AWS SDK to latest, for a live model list.
- **2026-07-21**: `make build-lite` includes Bedrock (MAJ-006).

## Follow-up (out of scope, tracked)
- `omnipus doctor` Bedrock awareness (OBS-001): a `WARN-BEDROCK-*` check. Separate PR.
- SEC-09 rate-limiting the validation probe per-provider per-operator (MIN-002).
- `ChatStream` for Bedrock (FR-014).
