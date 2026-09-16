# Wave 1 — type-design-analyzer review (round 2)

**Reviewer:** `pr-review-toolkit:type-design-analyzer`
**Scope:** `sendfile-fix` HEAD `cd0616b0` against parent `d0e7374a` (15 commits)
**Slices re-verified:** B1 + C + F + E backend stack + 9 r1-fix commits
**Round-1 baseline:** `docs/internal/reviews/wave1-review-round1-type-design-analyzer.md` (1 CRIT / 8 MAJOR / 4 MINOR)
**Mode:** read-only review; the only written artifact is this requested report

## Verdict

**PASS for Wave 1 handoff (with one residual MAJOR + two MINOR carryovers).** **0 CRITICAL, 1 MAJOR, 2 MINOR.** All eight r1 MAJOR findings are either fully addressed (7) or carry over with a documented narrower gap (1: TD-M8 write-only outcome-relabel). Both r1 MINOR findings tied to library Load/Store privacy and MediaLibraryEntrySource fixture-source split are closed. Two r1 MINORs (TD-m2 GHReleasePuller configuration, TD-m3 resize Result.Mime typed) remain open — same scope as r1, no regression.

The strongest corrections are:
- TD-C1 closed with a one-test-per-runtime-code contract gate that breaks the build when an `LLMErrorCode` constant diverges from the canonical schemas (live + replay + Zod). This converts the r1 contract-first regression into a build-fail.
- TD-M1 closed with a private `manifestEntry` value-type invariant-bearing record and a single refcount source of truth — the package can no longer construct an entry with a missing refcount or with a positive refcount that disagrees with a parallel map.
- TD-M5 closed with a two-stage parse (permissive `seedFile` DTO → validated `Seed`) and a typed `Version` that compares semver-numerically (`v10 > v2`) with a lexical fallback for date-based seeds.
- TD-M6 closed by collapsing the two prior Budget types into `capabilities.ResizeBudget` with `int64 MaxBytes` end-to-end.

The residual TD-M8 gap is narrower than r1: the typed `MediaDowngradeResult{Applied, Trigger, MediaClass}` is now consumed at the loop call site for both the warn-log and the FR-017a relabel decision. What remains is that the relabel target field (`ts.outcomeRelabel`) is still write-only in production — no emit site consults `outcomeRelabelCode()`; the WS forwarder re-runs `TranslateLLMError` on the raw `pe` and ignores the relabel. The spec's "classify the outcome" contract is implemented in-process but not surfaced to the SPA boundary.

## Re-verification of r1 findings

| ID | r1 Severity | r2 Status | Evidence |
|---|---|---|---|
| **TD-C1** | CRITICAL | ✅ **CLOSED** | `tool_args` and `schema` added to `contracts/components/schemas/LLMError.yaml:19-20` and `LLMErrorReplay.yaml:19-20`; `contracts/asyncapi.yaml:1352-1353,1384-1385` mirror the same values; `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts:112,121` carry both. Exhaustive gate: `pkg/api/generated/llm_error_codes_test.go:200-237` (`TestContract_LLMError_AllClassifierCodesRoundTrip`) parses every `LLMErrorCode` constant from `pkg/agent/translate_error.go`, marshals `LLMError` + `LLMErrorReplay` for each, validates against both component schemas via `validateAgainstComponentSchemaRawJSON`, and asserts each constant is present in the generated Zod enum. A new constant without updating any of the four layers fails the build with a precise code-named error. Inbound-schema copies `pkg/gateway/inboundschemas/LLMError.yaml` and `LLMErrorReplay.yaml` are byte-identical to the canonical files (verified `diff contracts/.../pkg/gateway/...` returns no differences). |
| **TD-M1** | MAJOR | ✅ **CLOSED** | Private `manifestEntry` (value type, all required domain facts as required values, not pointers) at `pkg/media/library/library.go:130-141`; constructor `newManifestEntry` validates every invariant at `library.go:148-204`; private `refcount int` (unexported) with a package-private `newRefcount` constructor that rejects negative values at `library.go:113-122`. **Single source of truth:** the parallel `map[string]int` from r1 is gone — refcount lives ONLY on `manifestEntry.refcount`. The on-disk envelope at `library.go:1156-1159` is `{Version, Entries}` only — the `Refcounts` map from r1 is gone (Load no longer reconstructs a parallel map). The remaining nil-defensive checks (`library.go:465-467, 538-540`) are correctness guards on read paths, not the persistence shape. |
| **TD-M2** | MAJOR | ✅ **CLOSED** | `workspace_id` is `readOnly: true` at `contracts/components/schemas/MediaLibraryEntry.yaml:23-29` (description explicitly names FR-007a / TD-M2). `refcount` and `last_refcount_seen_at` are both required read-only response fields at `MediaLibraryEntry.yaml:74-83`. Both are marked required server-side and not permitted in the request path (the gen type's `additionalProperties: false` plus the Load-time ID-mismatch check enforce this). |
| **TD-M3** | MAJOR | ✅ **CLOSED (type-safe; test gap remains)** | `model` is private at `pkg/providers/capabilities/catalog.go:101-107`; the consumer-facing type is `resolvedModel` with private fields and accessor methods (`Supports`, `Budget`, `ID`, `Provider`, `Notes`, `InputModalities`) at `catalog.go:109-161`. `InputModalities()` returns a copied slice at `catalog.go:156-161`. `Models()` returns `ModelSnapshot{ID, Handle *resolvedModel}` — the handle is a deep-owned copy via `c.resolve(m)` at `catalog.go:168-177`. The optimistic path (`optimistic` at `catalog.go:519-527`) constructs a fresh `resolvedModel` by value. **Test gap:** no regression test asserts that mutating the returned `resolvedModel` (its `InputModalities` slice or its `notes` string) does NOT corrupt catalog state. The type-safe design forecloses the aliasing; the regression test would catch a future "optimization" that returns a shared slice. |
| **TD-M4** | MAJOR | ✅ **CLOSED (type-safe; test gap remains)** | Dedicated `refreshMu sync.Mutex` at `catalog.go:400` serializes the WHOLE Refresh transaction (`Refresh` → `refreshLocked` at `catalog.go:607-614,621-660`) — pull → parse → version-check → apply → store all run under one lock. State getters (Resolve, Models, Version, etc.) use a separate `stateMu` (read-lock) so the hot read path is not blocked by a refresh. **Test gap:** no regression test fires two concurrent `Refresh` calls and asserts the version-check+apply is atomic; the only concurrency test is `TestCatalog_Resolve_ConcurrentRead` (many concurrent Resolve calls, no Refresh interleaving). |
| **TD-M5** | MAJOR | ✅ **CLOSED** | Two-stage parse: `seedFile` (permissive DTO, zero-value-friendly, unexported) at `catalog.go:229-247` → `Seed` (invariant-bearing, validated) at `catalog.go:204-223` via `seedFile.validate` at `catalog.go:271-377`. Validation enforces: `SchemaVersion`/`UpdatedAt`/`Source` non-empty; `Version` parsed into a `Version` type with semver-aware `Compare` (`catalog.go:72-96`, `version.go:49-96`) — the v10 > v2 lexical bug is fixed. Modality typed string with `ModalityText/Image/PDF/Audio/Video` constants and a `KnownModality` recognition set (`modality.go:14-45`); forward-compat unknown modalities are accepted by the validator and recorded as-is (`catalog.go:329-342`, test `TestParseSeed_AcceptsUnknownModalities`). Empty and duplicate modalities rejected (`catalog.go:331-336`). Text-modality invariant enforced (`catalog.go:343-348`, test `TestParseSeed_RequiresTextModality`). Provider non-empty enforced (`catalog.go:321-323`). Per-model `ResizeBudget` (when present) must be strictly positive, otherwise the catalog default is applied (`catalog.go:352-367`, `TestSeedValidate_DefaultBudgetApplied`, `TestSeedValidate_InlineBudgetWinsOverDefault`). |
| **TD-M6** | MAJOR | ✅ **CLOSED** | One canonical type: `capabilities.ResizeBudget{LongEdgePx int, MaxBytes int64}` at `catalog.go:82-90`. The previous `pkg/media/resize.Budget{LongEdge int, MaxBytes int}` is deleted; `pkg/media/resize/resize.go:82` now accepts `capabilities.ResizeBudget` directly, with `int64` end-to-end so a 32-bit target cannot truncate. The optimistic model path (`catalog.go:519-527`) sets `resizeBudget: c.defaultBudget` (never nil). `TestResolvedModel_AlwaysCarriesBudget` (catalog_test.go:621-633) and `TestOptimisticModel_DefaultBudget` (catalog_test.go:598-613) lock the invariant: every resolved model carries a non-zero `LongEdgePx` and `MaxBytes`. |
| **TD-M7** | MAJOR | ✅ **CLOSED** | `classifyByHTTPStatus` returns `CodeUnknown` for residual 4xx with non-pinned body at `translate_error.go:411-416` (was `CodeProviderRejected` in r1). `outcomeFallbackEligible` accepts ONLY `CodeUnknown` (`media_downgrade.go:256-258`, was `CodeUnknown OR CodeProviderRejected` in r1). The exclusion substrings (context-overflow, content-policy, bad-tool-args, schema) are re-checked in the gate at `media_downgrade.go:259-265` for a second-line defense. Locks: `TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME` (translate_error_test + media_outcome_retry_test.go:580-596), `TestClassifier_CodeUnknown_ForUnrecognizedBody400` (media_outcome_retry_test.go:604-623), `TestClassifier_StillReturnsProviderRejected_ForKnownRejections` (media_outcome_retry_test.go:635-708 — the inverse regression guard), `TestOutcomeFallback_AcceptsCodeUnknown_Only` (media_outcome_retry_test.go:727-765 — gate-half lock independent of classifier), `TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback` (media_outcome_retry_test.go:787-826 — full chain). |
| **TD-M8** | MAJOR | ⚠️ **PARTIALLY CLOSED** (carry-over finding TD-M8-r2 below) | Typed `MediaDowngradeResult{Applied, Trigger DowngradeTrigger, MediaClass MediaClass}` at `media_downgrade.go:107-116` replaces the bool return. Both triggers are closed internal enums (`TriggerClassifierPrimary`, `TriggerOutcomeFallback`, `TriggerNone` at `media_downgrade.go:80-95`). The loop call site reads `downgradeResult.Trigger == TriggerOutcomeFallback` to decide whether to relabel (`loop.go:6949-6951`) — this is exactly the data-derived-from-helper pattern the r1 finding demanded. **Remaining gap:** `ts.outcomeRelabel` is written at `loop.go:6950` (`setOutcomeRelabel(CodeMediaUnsupported)`), but the accessor `outcomeRelabelCode()` at `turn.go:322-327` has no production read site. The WS forwarder (`pkg/gateway/websocket.go:3353-3386`) re-runs `TranslateLLMError(p.ProviderError, p.Message)` on the raw `pe` and ignores the relabel. The test that should exercise this (`TestStep4_RelabelOnSuccess_ViaLoopCallSite`, `media_outcome_retry_test.go:513-535`) uses a test-only helper `recordedVerdictForTurn` (test.go:548-562) that mirrors the intended behavior instead of consuming `ts.outcomeRelabelCode()`. The FR-017a "classify the outcome" contract is implemented in-process but not surfaced to the SPA boundary. |
| **TD-m1** | MINOR | ✅ **CLOSED** | Production wire enum (`MediaLibraryEntrySource`) at `pkg/api/generated/openapi_types.gen.go:1097-1112` carries only `ToolOutput` and `UserUpload` — `test_fixture` is NOT a production value. `fixtureSource` at `pkg/media/library/library.go:328` is a package-private constant used ONLY by `UploadFixture` (`library.go:987-1088`), which is itself package-private. The live `Upload` method (`library.go:338-349`) accepts only `gen.UserUpload` and rejects anything else with `ErrSourceNotAllowed`. Load tolerates pre-existing manifests with `fixtureSource` entries (`library.go:808,850`) so the seed round-trip is forward-compatible. |
| **TD-m2** | MINOR | ❌ **NOT CLOSED (carry-over)** | `GHReleasePuller` at `pkg/providers/capabilities/puller.go:35-66` still exposes all fields as exported mutable (Owner, Repo, Asset, Ref, HTTPClient, BaseURL, RawBaseURL, UserAgent). `NewGHReleasePuller` at `puller.go:86-97` returns `*GHReleasePuller` (no error) and does not validate required fields. A struct literal can set any field directly, including a nil `HTTPClient` (defensively handled at `puller.go:302-307`), a custom `UserAgent` empty string (not defended — empty User-Agent is rejected by GitHub). The fix path is private fields + constructor returning `(*GHReleasePuller, error)` + validate-required-fields. |
| **TD-m3** | MINOR | ❌ **NOT CLOSED (carry-over)** | `pkg/media/resize/resize.go:57-62`: `Result.Mime` is a plain `string`. The doc comment notes the relationship with `Data` ("PNG or JPEG, see Mime") but does not represent it. A closed `OutputFormat` / `MIME` typed string with PNG/JPEG constants would close this. PNG-only results at `resize.go:114`, JPEG at `resize.go:125`. |
| **TD-m4** | MINOR | ✅ **CLOSED** | `Library.Load` and `Library.Store` no longer exist on the public surface. Public methods are `New`, `Path`, `List`, `Upload`, `Read`, `ResolveWithWorkspace`, `IncrementRefcount`, `DecrementRefcount`, `Refcount`, `Delete`, `CascadeDelete`, `OrphanGC`, `UploadFixture`. The mutator methods (`Upload`, `Delete`, `CascadeDelete`, `OrphanGC`, `changeRefcount`) call the package-private `persistLocked` (`library.go:912-931`) transactionally; `load`/`loadLocked` (`library.go:765-822`) are called only by `New` (`library.go:298`). No external caller can reload external disk state over a live library or force a redundant store. |

## Round-2 new / carry-over findings

| ID | Severity | Surface | Finding | Required correction |
|---|---|---|---|---|
| **TD-M8-r2** | **MAJOR** | Slice E — `outcomeRelabel` write-only in production | r1 closed the typed-result half of TD-M8 but left the relabel consumer missing. After Wave 1's correction, `loop.go:6915-6953` correctly classifies and conditionally stamps `ts.outcomeRelabel` from the typed `MediaDowngradeResult` — this is the in-process half. The wire-half is still missing: `pkg/gateway/websocket.go:3353-3386` (the WS-forwarder `EventKindError` case) re-runs `TranslateLLMError(p.ProviderError, p.Message)` on the raw `pe` and emits the original classifier verdict, so a turn where the outcome-based fallback fired and the retry succeeded is delivered to the SPA as `unknown` (the pre-retry verdict) instead of `media_unsupported` (the relabel). The test `TestStep4_RelabelOnSuccess_ViaLoopCallSite` uses a test-only `recordedVerdictForTurn` helper (`media_outcome_retry_test.go:548-562`) that mirrors the intended behavior instead of consuming the production accessor. The FR-017a "classify the outcome: media_unsupported iff retry succeeds" contract is unenforced at the wire boundary. | Either surface the relabel through the emit chain (preferred: add a `Relabel LLMErrorCode` field to `ErrorPayload` at `pkg/agent/events.go:488-497`; have the WS forwarder prefer `Relabel` when non-empty; have `appendErrorTranscript` stamp the relabel onto the persisted JSONL; add an integration test through `TestContract_LLMError_AllClassifierCodesRoundTrip`-style machinery that walks the live WS path end-to-end) OR delete `outcomeRelabel` entirely if the relabel-on-success semantics are decided to not cross the wire. Half-implementing the contract is worse than either choice. |
| **TD-M3-r2** | MINOR | Slice C — `resolvedModel` deep-owned invariant, no regression test | `pkg/providers/capabilities/catalog.go:109-177` correctly returns a deep-owned `*resolvedModel` handle with private fields and accessor methods, and `InputModalities()` returns a copy. The type-safe design forecloses the aliasing; however, no test asserts that mutating the returned handle's `InputModalities()` slice or its `notes` string does NOT corrupt catalog state. A future "optimization" that returns the internal slice directly (or returns `*Notes` as a pointer to share) would silently regress TD-M3. | Add a focused regression test: `m := c.Resolve("gpt-4o"); mods := m.InputModalities(); mods[0] = Modality("corrupted"); assert.Equal(t, ModalityText, c.Resolve("gpt-4o").InputModalities()[0])`. The mutation must not propagate. The handle's private fields are already a compile-time guard against `m.id = "..."`; the slice/sharing test is the run-time half. |
| **TD-m2-r2** | MINOR (carry-over) | Slice C — `GHReleasePuller` configuration | No change from r1. Same finding. | Same as r1: validate required fields in a constructor returning `(*GHReleasePuller, error)`; keep production configuration private/immutable; expose explicit test options for client/base URLs. |

## Read-only / required / discriminator assessment (updated)

### Fields that should be read-only or hidden — all closed

- `MediaLibraryEntry.workspace_id`: `readOnly: true` ✓
- `MediaLibraryEntry.refcount`: `readOnly: true` + required response field ✓
- `MediaLibraryEntry.last_refcount_seen_at`: `readOnly: true` + required response field ✓
- Catalog `model` fields: private, immutable accessor-only API ✓
- Library `manifestEntry` fields: private, package-private constructor ✓
- `Library` lifecycle: `Load` / `Store` removed from public surface ✓

### Optionals that should be required — all closed

- `MediaLibraryEntry.refcount`: required ✓
- Resolved model budget: required (default applied at validate) ✓
- Seed metadata (`version`, `schema_version`, `updated_at`, `source`): required ✓
- `Model.provider`: required ✓
- `Model.InputModalities`: required, non-empty, non-duplicate, includes text ✓

### Enum / `oneOf` decisions — unchanged

- **LLM error codes:** canonical closed enum ✓
- **Modalities:** typed `Modality` with known-constants + forward-compat unknown ✓
- **Resize output format:** still plain `string` (TD-m3 carry-over)
- **Downgrade trigger / media class:** closed internal enums (`DowngradeTrigger`, `MediaClass`) ✓ — these do not cross the gateway boundary
- **LLM error code (live) + relabel:** needs a discriminated-or-flagged type at the WS boundary to surface FR-017a (TD-M8-r2)

## Competing hypotheses and dispositions (r2)

| Hypothesis | Evidence sought | Disposition |
|---|---|---|
| H1 — r1's TD-C1 fix is real: a new `LLMErrorCode` constant without schema update breaks the build | `TestContract_LLMError_AllClassifierCodesRoundTrip` parsing every constant and validating all three layers | **Confirmed.** The test enumerates constants from `pkg/agent/translate_error.go` via Go AST, marshals each through `LLMError` + `LLMErrorReplay`, validates against both canonical JSON schemas, and asserts presence in the generated Zod enum. |
| H2 — r1's TD-M1 fix eliminated the parallel refcount map | Search for `map[string]int` or `Refcounts` in `pkg/media/library/` | **Confirmed.** `library.go:52-58, 263` shows `map[string]manifestEntry` only. `library.go:1156-1159` shows the on-disk envelope is `{Version, Entries}` only — the r1 `Refcounts` map is gone. |
| H3 — `Resolve` and `Models` return deep-owned values | Slice handling in `catalog.go:156-161, 168-177` | **Confirmed.** `InputModalities()` returns a copy; `resolve()` re-allocates the slice. The resolved model holds the budget by value, not pointer. |
| H4 — `Catalog.Refresh` serializes the whole transaction | `refreshMu` around `refreshLocked` | **Confirmed.** `catalog.go:607-614,621-660`. Pull → parse → version-check → apply → store under one lock. |
| H5 — residual `CodeProviderRejected` is no longer accepted by the fallback gate | `outcomeFallbackEligible` at `media_downgrade.go:246-267` | **Confirmed.** `code != CodeUnknown` returns false at `media_downgrade.go:256-258`. The r1 over-broad accept is gone. |
| H6 — the FR-017a outcome relabel reaches the SPA boundary | Search for `outcomeRelabelCode()` callers | **Rejected.** The accessor is defined at `turn.go:322-327` but has zero production callers. The WS forwarder (`websocket.go:3367`) re-runs `TranslateLLMError` on the raw `pe` and ignores the relabel. This is TD-M8-r2. |
| H7 — Slice F needs a wire `oneOf` for PNG vs JPEG | Variant-specific fields/behavior | **Rejected for this wave.** Both outputs have the same `Result{Data, Mime, LongEdge}` shape; a typed MIME enum (TD-m3 carry-over) is sufficient. |

## Verification observed

### Reproduction

```text
$ git rev-parse HEAD
cd0616b0bfe7251ca33b1596f198557b5c983c53

$ git rev-parse d0e7374a
d0e7374ac09da59a9a5949975153e7f2903dd54d
```

15-commit stack observed:

```text
cd0616b0 fix(adr-051-rev4): Slice C version semantics + seed validation (Wave 1 r1 TD-M5)
c11cdbc0 fix(adr-051-rev4): unify resize-budget type (capabilities canonical, int64 bytes) (Wave 1 r1 TD-M6)
65f4a8db fix(adr-051-rev4): strict CodeUnknown gate + Gemini-BDD verification (Wave 1 r1 TD-M7)
32f389fb fix(adr-051-rev4): B1 load/store privacy + fixture-source split (Wave 1 r1 TD-m4+TD-m1)
4f70672d refactor(adr-051-rev4): typed MediaDowngradeResult + consumed outcome-relabel (Wave 1 r1 TD-M8)
9c26e595 fix(adr-051-rev4): strict CodeUnknown gate for outcome-fallback (Wave 1 r1 TD-M7)
f7019e6c refactor(adr-051-rev4): private model + deep-owned Resolve handle + serialized Refresh (Wave 1 r1 TD-M3+TD-M4)
d4647703 refactor(adr-051-rev4): manifestEntry invariant + single refcount source of truth (Wave 1 r1 TD-M1+TD-M2)
d6827307 fix(adr-051-rev4): add tool_args+schema to LLMError enums + exhaustive contract test (Wave 1 r1 SFH-W1-01 + TD-C1)
5d96827b feat(adr-051-rev4): media audit events + cascade-delete wire-up (FR-008/009/033) (Slice B2 / Wave 1b)
fba0acbf feat(adr-051-rev4): outcome-based strip-retry + 2 new classifier codes (FR-017/017a/018/019) (Slice E / Wave 1b)
2c97b0bb feat(adr-051-rev4): resize-to-fit + delete D2 passthrough (FR-011/012/013/014/015/016) (Slice F / Wave 1b)
cda59abe feat(adr-051-rev4): workspace media library storage (FR-001/002/003/004/006/007/007a + cascade-delete hook) (Slice B1 / Wave 1a)
cf7d8782 feat(adr-051-rev4): capability catalog transport (GitHub Release + raw fallback, global seed only) (FR-024/025/026/027) (Slice C / Wave 1b)
```

### File evidence

| File | Lines | What was verified |
|---|---|---|
| `pkg/agent/translate_error.go` | 1-756 | `LLMErrorCode` constants (1-73), classifier status path (320-421), gate exclusion substrings (284-318), strict `CodeUnknown` return at 411-416 |
| `pkg/agent/media_downgrade.go` | 1-368 | `DowngradeTrigger`/`MediaClass`/`MediaDowngradeResult` types (76-116), strict `outcomeFallbackEligible` at 246-267 |
| `pkg/agent/loop.go` | 6900-6959 | Typed result consumed; `downgradeResult.Trigger == TriggerOutcomeFallback` gates `setOutcomeRelabel` |
| `pkg/agent/turn.go` | 261-327 | `outcomeRelabel` field + `setOutcomeRelabel`/`outcomeRelabelCode` accessors |
| `pkg/agent/events.go` | 476-497 | `ErrorPayload` does NOT carry a relabel field |
| `pkg/gateway/websocket.go` | 3353-3386 | WS forwarder re-translates via `TranslateLLMError(p.ProviderError, p.Message)` and ignores any relabel |
| `pkg/providers/capabilities/catalog.go` | 1-666 | Private `model` + `resolvedModel` handle (101-177); typed `Version` + semver-aware Compare (catalog.go 575-580 + version.go 49-96); `seedFile.validate` (271-377); `refreshMu` (400, 607-660) |
| `pkg/providers/capabilities/modality.go` | 1-45 | Typed `Modality` + known-constants + `KnownModality` set |
| `pkg/providers/capabilities/version.go` | 1-194 | Semver-aware `Compare`; lex fallback for date-based strings |
| `pkg/providers/capabilities/puller.go` | 1-314 | `GHReleasePuller` exported-mutable fields (TD-m2 carry-over, no change) |
| `pkg/media/library/library.go` | 1-1159 | Private `manifestEntry` (130-141); `refcount` type (113-122); single-source refcount; `Load`/`Store` private; `UploadFixture` private; `fixtureSource` package-private |
| `pkg/media/resize/resize.go` | 1-203 | `capabilities.ResizeBudget` canonical type accepted (82); `Result.Mime` still plain `string` (TD-m3 carry-over) |
| `pkg/api/generated/llm_error_codes_test.go` | 1-238 | Exhaustive classifier-code contract test |
| `pkg/api/generated/asyncapi_types.gen.go` | 275-289 | Generated `LLMError` + `LLMErrorReplay` Go types |
| `contracts/components/schemas/LLMError.yaml` | 1-31 | `tool_args` + `schema` in enum at 19-20 |
| `contracts/components/schemas/LLMErrorReplay.yaml` | 1-27 | `tool_args` + `schema` in enum at 19-20 |
| `contracts/asyncapi.yaml` | 1333-1393 | Inline mirror schemas with `tool_args` + `schema` at 1352-1353, 1384-1385 |
| `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts` | 112, 121 | Zod enums include both `tool_args` and `schema` |

### Diff identity check

```text
$ diff contracts/components/schemas/LLMError.yaml pkg/gateway/inboundschemas/LLMError.yaml
(no output)
$ diff contracts/components/schemas/LLMErrorReplay.yaml pkg/gateway/inboundschemas/LLMErrorReplay.yaml
(no output)

$ git diff --check d0e7374a..HEAD
(no whitespace errors)
```

### Out-of-scope / not re-verified

- No full Go suite was run; repository instructions make CI authoritative and prohibit the full suite in this devpod.
- No `make verify-contracts` run; the makefile target exists (Makefile:382) but the devpod lacks the network/toolchain for the gen-contracts step.
- The catalog tests for deep-owned handle mutation (TD-M3-r2) and concurrent Refresh atomicity (TD-M4 test gap) are NOT in the test suite — these are the regression gaps called out in the new findings.
- `outcomeRelabelCode` has no production caller (TD-M8-r2) — verified by exhaustive grep across `pkg/`.

## Summary for the Wave handoff

- **0 CRITICAL**, down from 1 in r1
- **1 MAJOR** (TD-M8-r2 carry-over — outcome relabel still write-only in production), down from 8 in r1
- **2 MINOR** (TD-M3-r2 + TD-m2 carry-over), down from 4 in r1

The contract/type-design surface is substantially strengthened: the `LLMError` enum is now contract-first with a build-fail regression guard; the library's internal state is invariant-bearing with a single refcount source of truth; the catalog's resolved-model API is type-safe and the refresh transaction is serialized. The remaining MAJOR is narrower than r1 and centered on a single wire-boundary gap (relabel not surfaced through the WS forwarder); closing it requires either threading the relabel through `ErrorPayload` + the WS path or deleting the field. The remaining MINORs are scope-creep concerns (a regression test for the catalog's handle isolation; the carry-over puller-config and resize-Mime findings).
