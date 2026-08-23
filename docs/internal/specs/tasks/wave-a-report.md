# Wave A report — feat/context-budget-and-tool-result-routing

Gated 2026-08-23 against branch head `36801b44` (origin == local, verified via
`git ls-remote` and `git rev-parse HEAD`).

## Commits on the branch (newest first)

| SHA | Subject |
|---|---|
| `36801b44` | feat(contracts): ADR-066/067/068 shared wire contracts (A-CONTRACT) — ONE commit, 104 files |
| `c60da266` | docs(adr+spec): ADR-067 — one dispute issue per assembly run (B2 first run: 194 disputes) |
| `b1b724d1` | build(deps): resolve eslint-plugin-jsx-a11y peer conflict so npm ci succeeds |
| `70428d73` | docs(tasks): Wave 0 consolidation report |
| `e62c7bed` | docs(adr): ADR-066/067/068 accepted — implementation approved 2026-08-23 |

Merge-base with `origin/main`: `ad859cbe` (== `origin/main` head at gate time).

## T067-01 (A-CONTRACT) — what landed (per the contract agent's report)

Schema files under `contracts/components/schemas/`:

- **NEW:** ProvidersCatalog, CatalogProvider, CatalogModel, CatalogProtocol, CatalogResizeLimits,
  ContextSettings, ContextSettingsUpdate, ContextModelOverride, ContextWindowSource, DefaultModel,
  DefaultModelUpdateRequest, EntitlementResponse, EntitlementModel, ProviderDependent,
  ProviderDeleteRequest, ProviderDeleteResponse, ToolResultProjectionFrame.
- **MODIFIED:** Provider (six-value status, protocol, custom, company, locality, cli_kind,
  auth_method/dependents/backs_default required, account_label, updated_at); ProviderUpdateRequest
  (protocol, auth_method, api_base); Agent (degraded_reason enum [needs_provider], needs_model
  required, optional context_window_effective/source/clamped/override); AgentUpdateRequest
  (context_window_override nullable); ProbeProviderRequest (free-string id 1..64, auth, api_key?,
  model?, api_base?, protocol?); ProbeProviderResponse (probed_model); LLMError + LLMErrorReplay and
  the two asyncapi inline copies (six new codes needs_provider / model_unassigned / turn_canceled /
  turn_timed_out / context_unrecoverable / context_window_unknown with copy+attribution, `user`
  attribution added); ToolCall (content_state); WsFrameType (tool_result_projection);
  ProviderCatalogEntry (description only).
- **DELETED:** ModelCapabilities.
- `contracts/openapi.yaml`: +GET /providers/catalog (ETag/304/503), +GET/PUT /providers/default-model,
  +DELETE /providers/{id}, +POST /providers/{id}/entitlement, +GET/PUT /settings/context;
  -GET /providers/model-capabilities.
- `contracts/asyncapi.yaml`: tool_result_projection channel/operation/message/schema + WsFrameType.
- `inboundschemas/` twins machine-synced by gen-contracts and committed.

Go call sites fixed in the same commit:

- `pkg/gateway/rest.go::HandleProviders` — model-capabilities case removed, capabilities import dropped;
  four `gen.Provider` literals now emit `auth_method: api_key` + `dependents: []`.
- `pkg/gateway/rest.go` executor defaults — `gen.ClaudeCode/Codex/Opencode` → `gen.ExternalCliTool*`
  (oapi-codegen re-prefixed the enum because the new cli_kind enums reuse `codex`).
- `pkg/gateway/rest_onboarding.go::probeProvider` — Auth must be `api_key` else 400 stub;
  ApiKey/ApiBase pointers.
- `pkg/gateway/rest_executor_defaults_test.go`, `rest_agent_type_test.go` — same enum rename.
- `pkg/gateway/rest_model_capabilities_test.go` — DELETED.
- `pkg/api/generated/fixtures.go` — ModelCapabilities fixtures removed; enum renames.
- `pkg/api/generated/contract_test.go` — ModelCapabilities tests removed; Provider fixtures gain the
  required fields; `ProviderValidationOutcome*` → bare `NoCredit/Unreachable/Restricted/InvalidKey/Valid`.
- `pkg/providers/catalog/catalog.go` + `catalog_test.go` — `ProviderCatalogEntryPlan*/Region*` → bare
  `StandardApi/CodingPlan/Intl/China/Us`; `Anthropic/OpenaiCompatible` → `ProviderCatalogEntryWire*`;
  `TestCatalog_DriftGuard_Id`.

## Fly worker gates (`/cache/runci.sh feat/context-budget-and-tool-result-routing <gate>`)

Every run printed `HEAD: 36801b44 feat(contracts): …` — no stale-checkout trap. Exit codes are the
gate's own `-> exit N` lines parsed from the log, not the SSH wrapper's.

| Gate | Result | Detail |
|---|---|---|
| contracts | GREEN | `npm-ci -> exit 0`, `verify-contracts -> exit 0`, `ALL GATES GREEN` |
| quick | GREEN | `gofmt -> exit 0`, `go-build -> exit 0`, `ALL GATES GREEN` |
| lint | **RED (x2)** | `golangci-lint -> exit 1` — 12 gosec G115 findings, all in `pkg/sandbox/` (re-run once, identical) |
| spa | **RED** | `npm-ci -> exit 0`, `typecheck -> exit 0`, `vitest -> exit 1` — 2 files / 12 tests failed, 6976 passed |

### lint — failing output (verbatim, first 40 lines)

```
pkg/sandbox/sandbox_linux.go:280:22: G115: integer overflow conversion uintptr -> int (gosec)
	defer unix.Close(int(rulesetFd))
	                    ^
pkg/sandbox/sandbox_linux.go:293:36: G115: integer overflow conversion uintptr -> int (gosec)
		if err := addLandlockPathRule(int(rulesetFd), rule.Path, rights); err != nil {
		                                 ^
pkg/sandbox/sandbox_linux.go:317:40: G115: integer overflow conversion uintptr -> int (gosec)
			if err := addLandlockNetPortRule(int(rulesetFd), rule.Port, landlockAccessNetBindTcp); err != nil {
			                                    ^
pkg/sandbox/sandbox_linux.go:343:40: G115: integer overflow conversion uintptr -> int (gosec)
			if err := addLandlockNetPortRule(int(rulesetFd), rule.Port, landlockAccessNetConnectTcp); err != nil {
			                                    ^
pkg/sandbox/sandbox_linux.go:537:10: G115: integer overflow conversion int -> uintptr (gosec)
		uintptr(rulesetFd),
		       ^
pkg/sandbox/sandbox_linux.go:561:10: G115: integer overflow conversion int -> uintptr (gosec)
		uintptr(rulesetFd),
		       ^
pkg/sandbox/sandbox_linux.go:582:12: G115: integer overflow conversion uintptr -> int (gosec)
	return int(version)
	          ^
pkg/sandbox/sandbox_linux.go:697:22: G115: integer overflow conversion uintptr -> int (gosec)
	defer unix.Close(int(rulesetFd))
	                    ^
pkg/sandbox/sandbox_linux.go:731:36: G115: integer overflow conversion uintptr -> int (gosec)
		if err := addLandlockPathRule(int(rulesetFd), rule.Path, rights); err != nil {
		                                 ^
pkg/sandbox/sandbox_linux.go:740:40: G115: integer overflow conversion uintptr -> int (gosec)
			if err := addLandlockNetPortRule(int(rulesetFd), rule.Port, landlockAccessNetBindTcp); err != nil {
			                                    ^
pkg/sandbox/sandbox_linux.go:759:40: G115: integer overflow conversion uintptr -> int (gosec)
			if err := addLandlockNetPortRule(int(rulesetFd), rule.Port, landlockAccessNetConnectTcp); err != nil {
			                                    ^
pkg/sandbox/seccomp_linux.go:334:15: G115: integer overflow conversion int -> uint8 (gosec)
			Jt:   uint8(remaining), // jump to deny (skip remaining JEQs + allow) // #nosec G115 -- bounds-checked above: ...
			           ^
12 issues:
* gosec: 12
```

**Attribution:** the contract commit `36801b44` touched 0 files under `pkg/sandbox/`. The two files were
last changed by `ef100ce7 fix(security): enable gosec on the security packages; close a latent seccomp
bypass` (2026-08-22, on `origin/release/v0.1.1`, in this branch's history; NOT in `origin/main`). This is a
pre-existing defect inherited from the release branch, not a contract defect — left unfixed per the gate
mandate. Per Constraint #7 it is still ours to fix before shipping (tracked as a blocker below).

### spa — failing tests (verbatim, first 40 lines of the failure block)

```
 FAIL  src/lib/__adr052__wireContracts.test.ts > ADR-052 FR-039 — Agent.memory_enabled wire contract > defaults to true when omitted (ordinary agents keep memory on)
AssertionError: expected false to be true // Object.is equality
 ❯ src/lib/__adr052__wireContracts.test.ts:238:28
    237|     const result = AgentSchema.safeParse(withoutField)
    238|     expect(result.success).toBe(true)
 FAIL  src/lib/__adr052__wireContracts.test.ts > ADR-052 FR-039 — Agent.memory_enabled wire contract > the seeded Judge is explicitly false (memory OFF, FR-039) — differs from the default-true case
AssertionError: expected false to be true // Object.is equality
 ❯ src/lib/__adr052__wireContracts.test.ts:244:28
 FAIL  src/lib/__adr052__wireContracts.test.ts > ADR-052 FR-039 — Agent.memory_enabled wire contract > explicit true round-trips as true (differentiator vs explicit false)
AssertionError: expected false to be true // Object.is equality
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #22 > returns Provider with validation on 200 no_credit outcome
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/openrouter: Required
 ❯ performRequest src/lib/api.ts:893:25
 ❯ src/lib/api.providers.test.ts:112:20
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #22 > returns Provider with validation on 200 unreachable outcome
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/openrouter: Required
 ❯ src/lib/api.providers.test.ts:132:20
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #22 > returns Provider with validation on 200 restricted outcome
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/openrouter: Required
 ❯ src/lib/api.providers.test.ts:150:20
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #22 > returns Provider with no validation on 200 valid outcome (no banner needed)
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/openrouter: Required
 ❯ src/lib/api.providers.test.ts:165:20
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #32 / m4 / R-C — key omission > omits api_key from the request body when apiKey arg is undefined (model-only edit)
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/openrouter: Required
 ❯ src/lib/api.providers.test.ts:188:5
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #32 / m4 / R-C — key omission > omits api_key when only models (slug list) changed
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/mygw: Required
 ❯ src/lib/api.providers.test.ts:205:5
 FAIL  src/lib/api.providers.test.ts > configureProvider — Test #32 / m4 / R-C — key omission > includes api_key in the body when a key is provided
ApiSchemaError: API response schema mismatch for PUT /api/v1/providers/openrouter: Required
 ❯ src/lib/api.providers.test.ts:221:5
 FAIL  src/lib/api.providers.test.ts > probeProvider — MAJOR-4 / validation passthrough > sends the correct request body (id + api_key, no endpoint when omitted)
AssertionError: expected undefined to be '' // Object.is equality
 ❯ src/lib/api.providers.test.ts:364:27
 FAIL  src/lib/api.providers.test.ts > probeProvider — MAJOR-4 / validation passthrough > forwards endpoint when provided
 ❯ src/lib/api.providers.test.ts:374:27
 Test Files  2 failed | 426 passed (428)
      Tests  12 failed | 6976 passed | 2 expected fail (6990)
```

**Attribution (inferred from the messages, not yet confirmed by reading the fixtures):** these are
SPA test fixtures that predate the contract change — `AgentSchema` now requires `needs_model`, and
`Provider` now requires `auth_method`/`dependents`/`backs_default`, so fixture objects fail Zod parsing
(`Required`); the `probeProvider` client body shape changed (`endpoint` → `api_base`, `auth` added).
The contract itself is intact (`verify-contracts` GREEN, `typecheck` GREEN). Not fixed here: the SPA
client/fixture updates are feature work for the Wave that consumes these contracts, not a
contract/generation/typecheck defect.

## GitHub CI (`gh workflow run pr.yml --ref feat/context-budget-and-tool-result-routing`)

- Run: https://github.com/elicify-ai/omnipus/actions/runs/32618001382 (head `36801b44`)
- State at 04:40:32Z (11 min after trigger): **completed — failure**.
- Passed: CLI Removed-Verb Guard, E2E shard plan check, ESLint (SPA), TypeScript Type Check, #615 guard,
  Security Check, Tool-Error-From-Status Lint, Verify Contracts, CGO_ENABLED=0 Build Gate, #615 real-Chrome
  browser tests, Wire-Types Lint, Vitest — components-agents-settings / components-chat /
  components-workspaces / components-layout / components-misc.
- Failed: **Linter** (same 12 gosec G115 in `pkg/sandbox`), **Vitest — lib-store** (same 12 tests as
  the Fly `spa` gate), **Tests** (Go).
- Skipped (gated on the above): Security Tests, E2E matrix, Perf Smoke, E2E shard plan.

### GitHub `Tests` job — REAL FAILURE (failed twice) in 4 packages

`pkg/config`, `pkg/gateway`, `pkg/providers`, `tests/security`. Unique failing tests (28):

```
pkg/config:      TestNoAgentConfigWorkspaceIdentifier
pkg/gateway:     TestAudit_ProbeAndTestNotAudited(/onboarding_probe)
                 TestOnboardingProbe_NoCreditWarns
                 TestProbeProvider_ValidateInbound_ValidBody
                 TestHandleOnboardingProbeProvider_SuccessWithModels
                 TestHandleOnboardingProbeProvider_UpstreamUnauthorized
                 TestHandleOnboardingProbeProvider_MissingFields(/empty_api_key, /unknown_provider_no_endpoint)
                 TestHandleOnboardingProbeProvider_PublicModelsBadKey
                 TestHandleOnboardingProbeProvider_PublicModelsGoodKey
                 TestHandleOnboardingProbeProvider_SSRFBlocksInternalEndpoint
                 TestHandleOnboardingProbeProvider_SSRFAllowsAllowlistedLoopback
                 TestHandleOnboardingProbeProvider_EmptyModelsWarns
pkg/providers:   TestProbeEnumProvidersResolveBase(/api_key, /sign_in, /openai-compatible)
                 TestProbeEnumProvidersAreKnownProtocols
                 TestEveryProbeProviderBuilds(/api_key, /sign_in, /openai-compatible)
tests/security:  TestPathTraversal_ReadFile(/unix_parent_traversal)
```

The `pkg/gateway` / `pkg/providers` cluster is consistent with the contract commit's
`probeProvider` stub (`Auth` must be `api_key` else 400; `endpoint` → `api_base`; probe-id enum
removed, so the `pkg/providers` tests enumerating the old `ProbeProviderRequestId` enum now iterate
`api_key`/`sign_in`/`openai-compatible` — the auth enum — instead). `TestNoAgentConfigWorkspaceIdentifier`
and `TestPathTraversal_ReadFile` are NOT obviously contract-related; root cause unverified (they could be
inherited from the release branch like the gosec findings). Not fixed — feature-code territory.

## Blockers carried forward

1. `lint` RED — 12 gosec G115 in `pkg/sandbox/sandbox_linux.go` / `seccomp_linux.go` (inherited from
   `ef100ce7` on `release/v0.1.1`). Needs a narrow `#nosec G115` with bounds rationale or a typed
   conversion helper; Constraint #7 says we own it regardless of origin.
2. `spa` RED — 12 vitest failures in `src/lib/__adr052__wireContracts.test.ts` and
   `src/lib/api.providers.test.ts` (fixtures/client not yet on the new Provider/Agent/Probe contract).
3. GitHub `Tests` RED — 28 Go tests across `pkg/config`, `pkg/gateway`, `pkg/providers`,
   `tests/security` (probe stub + two unattributed).
4. Not run on Fly: `go-test`, `go-race`, `e2e` (out of the Wave A gate list; GitHub's `Tests` stands in
   for `go-test`; race and e2e were skipped upstream because earlier jobs failed).

## Unverified

- Root cause of the vitest and Go failures is inferred from error messages, not from reading the
  fixtures/tests.
- Whether `TestNoAgentConfigWorkspaceIdentifier` and `TestPathTraversal_ReadFile` also fail on
  `origin/release/v0.1.1` or `origin/main`.
