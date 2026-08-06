# Wave 1 Review — Round 2 — Silent Failure Hunter

**Scope:** `d0e7374a..cd0616b0` (`sendfile-fix`, full Wave 1 stack + corrections A–I)
**Reviewer role:** `silent-failure-hunter` (pr-review-toolkit)
**Review focus:** re-verify SFH-W1-01 (exhaustive contract test in `d6827307`) and Wave 0 SFH-05 (unrecognized-code observability), then audit the four corrections (manifestEntry invariant, ResizeBudget unification, Version semver, Modality typed string) for newly introduced silent-failure shapes. Carry forward SFH-W1-03 / SFH-W1-04 / SFH-W1-05 from round 1 against the corrected stack.

**Method:** read-only static review. No source modifications; only test invocations and diff inspection. Full Go suite not run (per repository resource rule); targeted scoped tests were executed.

---

## Verdict

**REVISE — 0 CRIT / 2 MAJOR (residual from round 1) / 1 MAJOR (residual new) / 3 MINOR.**

The corrections meaningfully close three of the four carry-forward MAJORs and the four TD-M* findings:
- **SFH-W1-01** (CRIT → 0). Fully fixed; the AST-driven exhaustive contract test breaks the build if a new classifier code is added to `pkg/agent/translate_error.go` without a matching canonical enums regen.
- **SFH-W1-03** (MAJOR → 0). Strict `CodeUnknown`-only gate; classifier also retuned to surface `CodeUnknown` for residual 4xx. Literal Gemini BDD row 1013 + all reciprocals regression-locked.
- **TD-M1 / TD-M2 / TD-M3 / TD-M4 / TD-M5 / TD-M6 / TD-M8** (MAJOR × 7 → 0). Each has dedicated regression coverage and the underlying types/values are now invariant-bearing. Catalog defaults are applied during validate, deep-owned resolve handles serialize access, `refreshMu` is dedicated, semver comparator closes the v10 < v2 bug, and the typed `MediaDowngradeResult` is consumed by the loop call site.

Two MAJOR residuals and one new MINOR-class follow-up remain:
- **SFH-W1-02** (MAJOR residual) — Wave 0 SFH-05 unrecognized-code observability is still **NOT addressed** in either Go (`defaultUserMessage` in `pkg/agent/translate_error.go:116-121`) or SPA (`codeToMessage` in `src/lib/llm-error.ts:72-77`). Silent forward-compat collapse to `unknown`.
- **SFH-W1-05** (MAJOR residual) — checksum verification fail-open in `pkg/providers/capabilities/puller.go::verify` is **unchanged** from round 1; every soft-skip branch is still there. `TestGHReleasePuller_Pull_NoSidecar` still locks the fail-open path.
- **SFH-W1-04** (MAJOR residual, partial) — the typed `MediaDowngradeResult` is now consumed (TD-M8) and `setOutcomeRelabel` is called from the production loop site, **but `outcomeRelabelCode()` is still write-only**: no production emit, audit, transcript, frame, or log reads it. The relabel's pre-success warn-log records the original `helperCode`, not the relabeled verdict. The "durable observable surface" requirement is still unmet. The `TestStep4_RelabelOnSuccess_ViaLoopCallSite` test still drives a test-only mirror helper (`recordedVerdictForTurn`) that infers the verdict from guard bits, not from the production accessor.

Three MINOR observations:
- **SFH-W1-r2-m1** — `newManifestEntry` invariant tests are missing. The constructor's 8 explicit invariant checks (id, workspace_id, filename, mime, size, sha256, uploaded_at, source, refcount, last_refcount_seen_at) are not directly exercised; tests rely on `Upload` which always supplies valid data.
- **SFH-W1-r2-m2** — `TestStep4_RelabelOnSuccess_ViaLoopCallSite` still uses the mirror helper; the production `setOutcomeRelabel` → `outcomeRelabelCode` path is asserted in a test-only fashion.
- **SFH-W1-r2-m3** — catalog checksum verification is not "fail loud" per FR-025; the 6 soft-skip branches in `verify` continue to silently retain last-known-good of whatever schema-valid corrupted body was returned.

---

## Re-verification of round-1 findings

| ID | Round 1 status | Round 2 status | Evidence |
|---|---|---|---|
| **SFH-W1-01** (CRIT) | Slice E emits `tool_args` / `schema` codes that the SPA Zod enum rejects | **FULLY ADDRESSED** | `contracts/components/schemas/LLMError.yaml:19-20` + `LLMErrorReplay.yaml:19-20` enumerate both new codes; asyncapi.yaml inline mirrors updated; `pkg/api/generated/llm_error_codes_test.go::TestContract_LLMError_AllClassifierCodesRoundTrip` parses the Go AST, marshals both shapes per code, and validates against both JSON schemas plus the generated Zod enums. `src/lib/llm-error.ts:59-62` adds display copy. Verified: `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestContract_LLMError_AllClassifierCodesRoundTrip$' -p 1 ./pkg/api/generated/` → `ok 0.012s`. |
| **SFH-W1-02** (MAJOR, Wave 0 SFH-05 carry-forward) | Both `defaultUserMessage` (Go) and `codeToMessage` (SPA) silently collapse unrecognized codes to `unknown` without telemetry | **NOT ADDRESSED** | `pkg/agent/translate_error.go:116-121` is byte-identical to round 1 (single if-else fall-through to `userMessages[CodeUnknown]`; no `log.Warn` / metric / counter). `src/lib/llm-error.ts:72-77` is byte-identical to round 1 (single if-else fall-through to `codeToDisplay.unknown`; no `console.warn`). No new line references unrecognized-code observability in any of the 8 new commits touching these files. The fix is deferred to a later wave per the round-2 disposition. |
| **SFH-W1-03** (MAJOR) | `outcomeFallbackEligible` over-broad — accepts every residual 4xx | **FULLY ADDRESSED** | Classifier (`pkg/agent/translate_error.go::classifyByHTTPStatus` line 416) now returns `CodeUnknown` for residual 4xx with no pinned body — the canonical Gemini row 1013 body classifies as `CodeUnknown`. Gate (`pkg/agent/media_downgrade.go::outcomeFallbackEligible` line 256) now accepts only `CodeUnknown`; the `CodeProviderRejected` branch is removed. New test file additions (commit 65f4a8db): `TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME`, `TestClassifier_CodeUnknown_ForUnrecognizedBody400` (5 sub-cases: novel/whitespace/empty/off-context/numeric), `TestClassifier_StillReturnsProviderRejected_ForKnownRejections` (12 sub-cases covering 401/403/413/tool-args/schema/context-overflow/content-policy/xAI/429/5xx/408), `TestOutcomeFallback_AcceptsCodeUnknown_Only` (11 sub-cases pinning the gate's CodeUnknown-only contract), `TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback` (full chain classifier → gate → strip). Verified: all 5 named tests PASS under the same scoped invocation; the literal "Unsupported MIME type: image/svg+xml" body is now the regression-locked BDD row. |
| **SFH-W1-04** (MAJOR) | FR-017a relabel is write-only; test uses mirror helper | **PARTIALLY ADDRESSED** | The typed `MediaDowngradeResult{Applied, Trigger, MediaClass}` (TD-M8) is consumed at `loop.go:6926-6951`: the warn-log records the trigger and media_class from the helper, and `setOutcomeRelabel(CodeMediaUnsupported)` is called when `Trigger == TriggerOutcomeFallback` (production path, gated). BUT the accessor `outcomeRelabelCode()` defined in `turn.go:322-326` has **no production caller** (`grep -rn "outcomeRelabelCode" pkg/ cmd/` returns only its definition). The pre-success warn-log records the pre-relabel `helperCode` (the original classifier verdict, e.g. `provider_rejected`), not the relabeled `CodeMediaUnsupported`. The relabel lives only in the in-memory field; no log/audit/transcript/frame contains the post-relabel verdict. The test `TestStep4_RelabelOnSuccess_ViaLoopCallSite` still drives `recordedVerdictForTurn` (lines 537-562), a test-only mirror that infers `CodeMediaUnsupported` from guard bits (`ts.imageRetryDone.Load() || ts.mediaRetryDone.Load()`), never calling the production accessor. Removing `outcomeRelabel`, `setOutcomeRelabel`, and `outcomeRelabelCode` would not cause this test to fail. |
| **SFH-W1-05** (MAJOR) | Checksum verification fails open on missing/unreadable sidecar | **NOT ADDRESSED** | `pkg/providers/capabilities/puller.go` is byte-identical to commit `cf7d8782` (Slice C) — `git log --oneline d0e7374a..HEAD -- pkg/providers/capabilities/puller.go` lists only `cf7d8782`. The `verify` function still soft-skips on: empty `checksumURL` (line 238), request-construction failure (line 245), transport failure (line 252), non-200 status (line 258), read failure (line 263), empty body (line 270). `TestGHReleasePuller_Pull_NoSidecar` (puller_test.go:226-256) still locks the fail-open behavior (assertion: `require.NoError(t, err)` when sidecar is 404). The GitHub release asset's `digest` field is still parsed (puller.go:79) but never consulted — `verify` does not accept the digest as an alternative integrity source. The raw fallback has no independent integrity guarantee. This contradicts FR-025's "semver-tagged, checksummed" transport. Schema-valid corrupted body can be applied and persisted as new last-known-good; the warning at `Catalog.Refresh` only fires on a present-mismatch, not on the broader verification-skip case. |

---

## New corrections — silent-failure audit

### 1. manifestEntry invariant (TD-M1 + TD-M2, commit `d4647703`)

**Implemented as designed:** private `manifestEntry` struct with 10 required-value fields, private `refcount` newtype with `newRefcount` constructor, parallel `map[string]int` removed, `Load` / `Store` methods deleted, projection built at the API edge only. Schema changes: `MediaLibraryEntry.workspace_id` marked `readOnly`; `refcount` and `last_refcount_seen_at` are required.

**Silent-failure shape:** the constructor's 8 explicit invariant checks (id not Nil, workspace_id not empty, filename normalized, mime not empty, size in [0, MaxFileSize], sha256 valid 64-hex, uploaded_at not zero, source valid or fixture) are not directly exercised. No `TestNewManifestEntry_RejectsEmpty*` / `TestNewManifestEntry_RejectsInvalidDigest` / `TestNewManifestEntry_RejectsOversize` tests. Tests rely on `Upload` (lines 338-492) which only ever calls `newManifestEntry` with valid data via `UploadFixture` (lines 987-1116).

- The `refcount` type's negative-rejection is empirically covered at `library_test.go:351` (second `DecrementRefcount` returns `ErrRefcountUnderflow`), so the new refcount-type behavior is at least indirectly locked.
- The `manifestEntry` invariant coverage is **missing** at the constructor level. A future regression that loosened `newManifestEntry` (e.g. accepting empty workspace_id) would not be caught — every test path drives `Upload` which derives the workspace_id from `l.workspaceID` (always non-empty in a constructed Library).

**Required:** add a `package library` (or `_test.go` extension) test that exercises `newManifestEntry` directly with each invalid combination, similar to the `TestParseSeed_Rejects*` set that TD-M5 added for the seed validator.

**Minor.** The invariant is enforced by construction; the production code cannot land an invalid entry. The risk is silent loosening at the constructor — caught at the type level today, only the test-coverage surface is missing.

### 2. ResizeBudget unification (TD-M6, commit `c11cdbc0`)

**Implemented as designed:** `resize.Budget`, `resize.DefaultLongEdge`, `resize.DefaultMaxBytes` are removed (verified: `grep -rn "resize.Budget\|resize.Default" pkg/media/resize/` returns no hits). `ResizeToFit` accepts `capabilities.ResizeBudget` directly. Byte counts are `int64` end-to-end — no int cast at the boundary. `model.resizeBudget` is a value type (not a pointer) in the catalog. The seed DTO's `ResizeBudget` is still `*ResizeBudget` (optional in wire), but `seedFile.validate` mutates the DTO in place to apply the catalog default to any model that omitted one. `OptimisticModel` returns a handle carrying the catalog default. `TestResolvedModel_AlwaysCarriesBudget`, `TestOptimisticModel_DefaultBudget`, `TestSeedValidate_DefaultBudgetApplied`, `TestSeedValidate_InlineBudgetWinsOverDefault`, `TestResizeBudget_OverflowSafety`, `TestResizeBudget_NonPositiveRejected` all pass.

**Silent-failure shape:** none observed. The int64 boundary is exercised (`1<<62` budget; 1-byte budget that still drives `ErrLadderFloor`); the non-positive guard is exercised (zero/negative long_edge and max_bytes each surface as explicit errors). The `loop_media.go:504` call site now constructs `capabilities.ResizeBudget` directly with the resolved model's budget — the int32-truncation hazard is closed end-to-end.

**No MAJOR finding here.**

### 3. Version semver comparator (TD-M5, commit `cd0616b0`)

**Implemented as designed:** `Version` struct with `major/minor/patch/prerelease/isSemver` fields. `ParseVersion` returns a `Version` (never errors today; reserved for future strict mode). `Compare` uses numeric comparison when both sides parse as semver, lexical fallback otherwise. ISO-date strings ("2026-07-23") fall through to lexical (the chronological invariant for date-based tracking is preserved by both interpretations, but the comment at version.go:14-18 is precise about the two-way split). The regression check at `catalog.go:643` uses `s.Version.Compare(currentVersion) < 0` instead of the lexical `<` — the v10 < v2 bug is closed.

`TestVersion_SemverComparison` (15 sub-cases: v10 > v2, v1.2.3 < v1.10.0, v1.2.10 > v1.2.2, prerelease < stable per semver §11, shorthand expansion, date lexical, non-semver lexical, date-vs-semver) all PASS. `TestCatalog_Refresh_VersionRegressSemver` is the live regression test asserting v2.0.0 is rejected below v10.0.0 (and v1.0.10 is accepted above v1.0.0). PASS.

**Silent-failure shape:** none observed at the comparator boundary. The two-way split is documented in the comment and exercised by the test set.

**No MAJOR finding here.**

### 4. Modality typed string (TD-M5, commit `cd0616b0`)

**Implemented as designed:** typed `Modality string` with five named constants (`ModalityText`/`ModalityImage`/`ModalityPDF`/`ModalityAudio`/`ModalityVideo`) and a `KnownModality` map (`map[Modality]bool`) for the recognition boundary. The validator accepts any non-empty unknown modality (forward-compat: a future "3d" or "hologram" modality passes validation, is recorded in the catalog as-is, and is reported by `Supports` if the model's slice explicitly carries it).

The 8 new validation tests (`TestParseSeed_RejectsEmptyModality`, `TestParseSeed_RejectsDuplicateModality`, `TestParseSeed_RequiresTextModality`, `TestParseSeed_RejectsEmptyProvider`, `TestParseSeed_RejectsInvalidResizeBudget` × 4 sub-cases, `TestParseSeed_RejectsMissingSchemaVersion`, `TestParseSeed_RejectsMissingUpdatedAt`, `TestParseSeed_RejectsMissingSource`) all pass. The text-invariant (every model must include "text" in its modalities) is locked. Empty / duplicate / missing provider / invalid budget surfaces as an explicit error from `seedFile.validate` — no silent default.

**Silent-failure shape:** none observed. The forward-compat acceptance (unknown modalities allowed) is the correct tradeoff: the runtime does not invent modality names; the named constants are the public API; the `KnownModality` set is the runtime's recognition boundary. A future maintainer adding "image" support would have to add the constant AND extend the `KnownModality` map AND possibly adjust the supported-modality check at the orchestrator; the catalog layer itself does not silently mis-classify.

**No MAJOR finding here.**

---

## New silent-failure shapes introduced by the corrections

**None of the four corrections introduces a new wire-level silent failure.** The changes close type-system holes that previously allowed invalid states to be representable (manifestEntry parallel refcount map; optional budget on resolved model; lexical version comparison; `*string` modality). The corrections are net-positive: each one converts a runtime-checked invariant into a type-checked invariant (or a constructor-validated invariant).

The new shapes are observability and test-coverage gaps, not new wire-level silent failures:

- **SFH-W1-r2-m1** — `newManifestEntry` invariant tests missing (see correction #1 above).
- **SFH-W1-r2-m2** — `TestStep4_RelabelOnSuccess_ViaLoopCallSite` still uses a test-only mirror helper (see SFH-W1-04 carry-forward above).
- **SFH-W1-r2-m3** — catalog checksum verification is not "fail loud" per FR-025 (see SFH-W1-05 carry-forward above).

---

## Requested path audit

| Path | Result | Evidence |
|---|---|---|
| Slice E: Gemini 400 unsupported SVG MIME | **Fires (full chain)** | `classifyByHTTPStatus` line 416 returns `CodeUnknown`; `outcomeFallbackEligible` line 256 accepts only `CodeUnknown`; `TryMediaDowngrade` returns `Applied=true, Trigger=TriggerOutcomeFallback, MediaClass=MediaClassImage`; `TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback` PASS. |
| Slice B1: SHA mismatch | **Hard-fail; safe** | `Library.Read` returns `nil, entry, ErrIntegrityCheckFailed`; `TestWorkspaceLibrary_Read_VerifiesSHA256_TamperDetected` PASS. |
| Slice F: pixel bomb | **Guarded before decode; not swallowed** | `encodeImageToDataURL` rejects >16-MP declared raster at `loop_media.go:447-474`; `ResizeToFit` accepts already-decoded image; `ErrLadderFloor` is the routing sentinel for the router. The exact step-7 reason (`[attachment unavailable: ... (pixel budget exceeded)]`) is NOT preserved at the function boundary (round-1 minor SFH-W1-m1 still open; carried forward but not in this round's mandate). |
| Slice C: corrupt download | **Hard-fail only when sidecar is present and mismatches; otherwise fail-open** | `puller.go::verify` still has 6 soft-skip branches (line 238, 245, 252, 258, 263, 270). `TestGHReleasePuller_Pull_NoSidecar` PASSES with the fail-open behavior locked. Carry-forward SFH-W1-05 unchanged. |
| Slice E: classifier code observed at SPA boundary | **Both new codes pass through** | `TestContract_LLMError_AllClassifierCodesRoundTrip` enumerates the Go constants, marshals `LLMError` and `LLMErrorReplay` for each, validates against both canonical JSON schemas, and asserts presence in the generated Zod enums. PASS. |
| Slice E: unrecognized code on wire | **Silently collapses to `unknown`** | Go `defaultUserMessage` (line 116-121) unchanged; SPA `codeToMessage` (line 72-77) unchanged. No WARN / console.warn / metric / counter. Carry-forward SFH-W1-02 unchanged. |
| Slice E: FR-017a relabel is observable | **No; field is write-only** | `loop.go:6950` writes `ts.setOutcomeRelabel(CodeMediaUnsupported)`; no production code reads `ts.outcomeRelabelCode()`. The pre-retry warn-log records the original `helperCode` (`provider_rejected`), not the relabeled verdict. `recordedVerdictForTurn` (test-only mirror) infers the verdict from guard bits and is the only reader. Carry-forward SFH-W1-04 partially addressed (typed result consumed at the loop call site; durable observable surface still missing). |
| TD-M1 + TD-M2: manifestEntry construction | **Valid by construction; not directly tested** | 8 explicit invariant checks at `newManifestEntry` (lines 159-211). The `refcount` type's negative-rejection is empirically covered via `ErrRefcountUnderflow` (library_test.go:351). The other 7 invariants (id/workspace_id/filename/mime/size/sha256/uploaded_at/source) are not directly tested at the constructor level. Minor coverage gap (SFH-W1-r2-m1). |
| TD-M6: ResizeBudget int64 boundary | **No truncation possible** | `int64(len(pngData)) <= budget.MaxBytes` at resize.go:113, 124. `TestResizeBudget_OverflowSafety` with `1<<62` budget and 1-byte budget PASS. |
| TD-M5: Version semver | **v10 > v2 closed** | `TestVersion_SemverComparison` (15 sub-cases) PASS. `TestCatalog_Refresh_VersionRegressSemver` is the live regression. |
| TD-M5: Modality typed string | **Forward-compat unknown accepted; known set locked** | `TestParseSeed_RejectsEmptyModality`, `TestParseSeed_RejectsDuplicateModality`, `TestParseSeed_RequiresTextModality`, `TestParseSeed_RejectsEmptyProvider` PASS. `KnownModality` map bounds the runtime's recognition. |

---

## Investigation hypotheses

| Hypothesis | Evidence sought | Result |
|---|---|---|
| H1: SFH-W1-01 contract test actually enumerates Go constants | AST parsing of translate_error.go, marshaling per code, schema validation, Zod enum assertion | **Verified.** `classifierCodesFromAgent` (llm_error_codes_test.go:53-105) parses `*ast.GenDecl` with `token.CONST` and `*ast.ValueSpec` whose `Type` is the ident `LLMErrorCode`. `liveCodesFromZodSchemas` (lines 111-165) regex-parses `_asyncapi-zod-schemas.generated.ts` for the `z.enum([...])` body after the `LLMError` and `LLMErrorReplay` markers. `assertCodeInComponentSchema` (lines 170-185) marshals JSON and runs the component-schema validator. Test passes on the current 9-code enum. Confidence: high. |
| H2: SFH-W1-02 unrecognized-code observability is added in any of the 8 correction commits | `log.Warn` / `console.warn` / `metric` / `slog` on the Go fallback; `console.warn` on the SPA fallback | **Rejected.** No match for any of those in the 8 commits that touch `translate_error.go` or `llm-error.ts`. The fallback function bodies are byte-identical to round 1. Confidence: high. |
| H3: SFH-W1-04 outcomeRelabelCode has a production reader | Repository-wide grep for `outcomeRelabelCode` outside its definition | **Rejected.** The only hits are the accessor definition (turn.go:322-326) and the SFH-W1-r2 reviews. The production loop writes via `setOutcomeRelabel` (loop.go:6950) but no emit/audit/transcript/frame/WS-forwarder call reads it back. The relabeled verdict therefore never reaches the user, never lands in audit, never reaches the transcript. Confidence: high. |
| H4: SFH-W1-05 checksum fail-open is closed in any correction commit | Diff of `pkg/providers/capabilities/puller.go` against round-1 | **Rejected.** The file is byte-identical to its introduction in `cf7d8782`. The 6 soft-skip branches remain. `TestGHReleasePuller_Pull_NoSidecar` still locks the fail-open. Confidence: high. |
| H5: TD-M1 / TD-M2 manifestEntry invariants are tested at the constructor | Direct test of `newManifestEntry` with each invalid input | **Rejected.** The only direct test of refcount behavior is `ErrRefcountUnderflow` (library_test.go:351). The 7 other invariants (id/workspace_id/filename/mime/size/sha256/uploaded_at/source) are not directly exercised — every test path drives `Upload` which only writes valid data. Confidence: high. |
| H6: TD-M6 int64 truncation hazard is closed end-to-end | `int64(len(...)) <= budget.MaxBytes` at the resize comparison boundary | **Verified.** resize.go:113, 124. `TestResizeBudget_OverflowSafety` passes a `1<<62` budget (overflows int32) and a 1-byte budget, asserting the int64 comparison is actually consulted in both cases. Confidence: high. |
| H7: TD-M5 semver comparator fixes v10 < v2 | `TestVersion_SemverComparison` table-driven sub-cases | **Verified.** The v10 > v2 case (line 344) and the v1.2.3 < v1.10.0 case (line 347) are explicit in the test table. Both PASS. `TestCatalog_Refresh_VersionRegressSemver` is the live regression at the catalog boundary. Confidence: high. |
| H8: TD-M5 modality typed string constrains the runtime's recognition | `KnownModality` map vs. `Modality` constant set | **Verified.** The constants and the map are the same set (5 entries). Forward-compat unknown modalities (e.g. "3d") are accepted by `seedFile.validate` (no per-modality check) but `Supports` returns false on them unless the model slice explicitly carries the unknown value. The test for this is implicit in `TestParseSeed_AcceptsUnknownModalities` (catalog_test.go:187-202). Confidence: high. |
| H9: The corrections introduced any new wire-level silent failure | New code paths producing invalid state, new fallbacks that swallow errors, new observability gaps | **Rejected.** Each correction converts a runtime-checked invariant into a type/constructor-checked invariant. No new fallbacks, no new silent collapses, no new error-swallowing catch blocks observed. Confidence: high. |

---

## Reproduction and verification observed

```text
# SFH-W1-01 — exhaustive contract test (NEW)
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestContract_LLMError_AllClassifierCodesRoundTrip$' -p 1 ./pkg/api/generated/
ok      github.com/elicify-ai/omnipus/pkg/api/generated    0.012s

# SFH-W1-03 — strict CodeUnknown gate (5 new tests, all PASS)
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run \
  '^(TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME|TestClassifier_CodeUnknown_ForUnrecognizedBody400|TestClassifier_StillReturnsProviderRejected_ForKnownRejections|TestOutcomeFallback_AcceptsCodeUnknown_Only|TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback)$' \
  -p 1 -v ./pkg/agent
... PASS: TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME (0.00s)
... PASS: TestClassifier_CodeUnknown_ForUnrecognizedBody400 (5 sub-cases)
... PASS: TestClassifier_StillReturnsProviderRejected_ForKnownRejections (12 sub-cases)
... PASS: TestOutcomeFallback_AcceptsCodeUnknown_Only (11 sub-cases)
... PASS: TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback (0.00s)
ok      github.com/elicify-ai/omnipus/pkg/agent  0.022s

# TD-M1 / TD-M2 / TD-M5 / TD-M6 (catalog + resize)
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run \
  '^(TestParseSeed_RejectsEmptyModality|TestParseSeed_RejectsDuplicateModality|TestParseSeed_RequiresTextModality|TestParseSeed_RejectsEmptyProvider|TestParseSeed_RejectsInvalidResizeBudget|TestParseSeed_RejectsMissingSchemaVersion|TestParseSeed_RejectsMissingUpdatedAt|TestParseSeed_RejectsMissingSource|TestVersion_SemverComparison|TestVersion_IsSemver|TestCatalog_Refresh_VersionRegressSemver|TestResolvedModel_AlwaysCarriesBudget|TestOptimisticModel_DefaultBudget|TestSeedValidate_DefaultBudgetApplied|TestSeedValidate_InlineBudgetWinsOverDefault)$' \
  -p 1 -v ./pkg/providers/capabilities
... all PASS
ok      github.com/elicify-ai/omnipus/pkg/providers/capabilities  0.006s

$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run \
  '^(TestResizeBudget_OverflowSafety|TestResizeBudget_NonPositiveRejected)$' \
  -p 1 -v ./pkg/media/resize
... all PASS
ok      github.com/elicify-ai/omnipus/pkg/media/resize  0.008s

# TD-M1 / TD-M2 (library full suite)
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/media/library
ok      github.com/elicify-ai/omnipus/pkg/media/library  0.124s
```

All scoped tests pass. No production files were modified.

---

## Gaps in verification

- I did not run the full Go test suite (per CLAUDE.md resource rule). The corrections touch `pkg/agent`, `pkg/media/library`, `pkg/media/resize`, `pkg/providers/capabilities`, `pkg/api/generated`; the package-level scoped runs above are the local substitute.
- I did not exercise the new exhaustive contract test with a deliberately-mismatched schema (e.g. add a `CodeFuture` to `translate_error.go` and verify the test FAILS with a precise pointer to the missing code). The test's logic is reasoned from the source (AST parse + JSON schema validate + Zod regex parse); the failure mode is "assertCodeInComponentSchema" returning an error or `_, ok := zodSet[code]; assert.True(t, ok)` failing with the precise SFH-W1-01 / TD-C1 message. Empirical proof of the failure mode would require modifying the spec or the constants and would change the working tree.
- I did not empirically test the SFH-W1-05 fail-open behavior against a tampered sidecar (the round-1 review covered this); the carry-forward conclusion is grounded in the byte-identical `puller.go` diff.
- I did not exercise the SFH-W1-04 `outcomeRelabelCode` path with a follow-on WS frame / audit emit (the durable observable surface is not wired in production; the gap is the absence of any caller).

---

## Summary

The corrections meaningfully advance the Wave 1 stack:
- **SFH-W1-01** is closed with an AST-driven exhaustive contract test that catches future drift at the build level.
- **SFH-W1-03** is closed with a strict `CodeUnknown`-only gate and a classifier retuned to surface `CodeUnknown` for the residual 4xx tail.
- **TD-M1 / TD-M2 / TD-M3 / TD-M4 / TD-M5 / TD-M6 / TD-M8** are closed with invariant-bearing types, deep-owned handles, dedicated mutexes, semver-aware comparators, typed modality strings, and a typed `MediaDowngradeResult` consumed at the production loop call site.

The residuals are observable-state gaps that the round-1 review identified and the corrections did not (or could not, in this scope) close:
- **SFH-W1-02** — unrecognized-code observability is still silent on both Go and SPA sides.
- **SFH-W1-04** — the FR-017a relabel is stamped into a write-only field; the durable observable surface is still missing; the test relies on a test-only mirror.
- **SFH-W1-05** — checksum verification still fails open on every soft-skip branch; the test explicitly locks the fail-open behavior.

None of the four corrections (manifestEntry invariant, ResizeBudget unification, Version semver, Modality typed string) introduced a new wire-level silent failure. The new silent-failure shapes introduced are observability and test-coverage gaps, not new error paths.

The Wave 1 stack is materially safer than at round 1: the three CRIT/MAJOR wire-boundary issues (SFH-W1-01 / SFH-W1-03) and the seven TD-M* type-boundary issues are closed. The two MAJOR residuals (SFH-W1-02 unrecognized-code observability, SFH-W1-05 checksum fail-open) and the partial SFH-W1-04 (write-only relabel) are the blockers for the v0.2 security hardening sign-off and the Wave 3 integration handoff. They are each independent, localized, and previously analyzed — appropriate candidates for a follow-up Wave 1 round 3 (or, more likely, a pre-merge cleanup commit) rather than a structural redesign.

*End of review.*
