# Wave 0 — Slice A (Contracts Foundation) — pr-test-analyzer review (round 1)

**Branch:** `sendfile-fix`
**Diff base:** `f6eccbcd..08690ff9` (single commit — `feat(adr-051-rev4): generate media library wire contracts (Slice A / Wave 0)`)
**Reviewer:** pr-test-analyzer (read-only)
**Scope of Slice A:** FR-031 only — generate `MediaLibraryEntry.yaml` + `MediaAttachmentRequest.yaml`; reference from `openapi.yaml`; regenerate all artifacts; verify with `make verify-contracts`.
**Spec TDD coverage slice:** FR-031 maps to `make verify-contracts` in the spec traceability matrix (`docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1158`). Slice A is mechanical, so test coverage outside FR-031 only matters for the **regression coverage** of the prior hand-edit workaround (the `*ErrorPayload` / `*ReplayErrorPayload` pointer adjustment in `pkg/api/generated/asyncapi_types.gen.go`) that this commit eliminates by extending the AsyncAPI Go generator.

---

## Findings

| ID | Severity | File:Line | One-line | Fix |
|---|---|---|---|---|
| TA-1 | OBS | `scripts/gen-asyncapi-go/main_test.go:47` | The new test (`TestGenerateUsesMatchingNamedInlinePayload`) covers exactly one positive path: `*ErrorPayload` (optional `payload`, name-suffix-strip + same-shape match). It does not exercise the other branches of `matchingNamedInlineGoType` (see TA-2 … TA-5). | Optional: split into four explicit sub-tests — required-field returns value-typed, `$ref` property short-circuits, empty-properties short-circuits, candidate-not-in-map short-circuits, non-`Frame`-suffix owner still resolves via property name (suffix not stripped). |
| TA-2 | MAJOR | `scripts/gen-asyncapi-go/main.go:419-438` (`matchingNamedInlineGoType`) | Early-return branch `if property.ref != ""` is untested. A `$ref` property (used pervasively across the spec, e.g. `DoneStats` references `TokenCounts`) must never be re-mapped to a non-existent named type; a regression here could silently rewrite every cross-schema `$ref` in the AsyncAPI schema set. | Add `TestGenerate_RefPropertyShortCircuits` — assert that an `ErrorFrame`-shaped struct whose `payload` property has `ref: "ErrorPayload"` still produces the regular `ErrorPayload` (resolved via `resolveGoType`), not the `candidateName` path. |
| TA-3 | MAJOR | `scripts/gen-asyncapi-go/main.go:419-438` | Required-field branch (`isRequired=true → no pointer`) is untested. The test asserts `*ErrorPayload` with `omitempty`; the generator's required-value path has no direct assertion. | Add `TestGenerate_RequiredMatchingPropertyReturnsValueType` — same shape as TA-1's payload but with `required: { payload: true }`, asserting the field emits `Payload ErrorPayload ` `` `json:"payload"` `` ` (no pointer, no omitempty). Without this, the generator could silently start omitempty-tagging required fields (and break the SPA Zod validator, which is the exact bug the prior hand-edit prevented for `ErrorFrame.payload`). |
| TA-4 | MAJOR | `scripts/gen-asyncapi-go/main.go:440-477` (`sameSchemaShape`) | `sameSchemaShape`'s **false** return paths are untested (every difference branch — different `schemaType`, different `ref`, different `oneOf`/`anyOf` length, mismatched nested property, mismatched `items`, mismatched `required`, mismatched `enum`, mismatched `constValue`). A regression in any of these branches could flip an inline-payload Go emit from `*ErrorPayload` to an anonymous struct, silently regressing the entire ADR-051 B2 fix. | Add at least one "shape mismatch returns inline anonymous struct" test — e.g. same setup as TA-1 but with `ErrorPayload.properties.message.schemaType = "boolean"` (drift), and assert the generated field is `struct{ … }` NOT `*ErrorPayload`. |
| TA-5 | MINOR | `scripts/gen-asyncapi-go/main.go:419-438` | Non-`Frame` owner (no suffix to trim) is untested. `matchingNamedInlineGoType` always runs `strings.TrimSuffix(ownerGoName, "Frame")` (a no-op for owners not ending in `Frame`); the candidate-construction logic for e.g. `ToolCall` + property `tool_call` matching schema `ToolCallToolCall` is unverified. | Add a small `TestGenerate_NonFrameOwnerMatches` — owner `ToolCall`, prop `result`, separate schema `ToolCallResult`, both with shape `string`; assert field emits `Result *ToolCallResult ``. The current implementation looks correct, but the only coverage I find today is from the single Frame-suffix path. |
| TA-6 | OBS | `pkg/api/generated/asyncapi_types.gen.go:260-280` | The hand-edit rationale comment block for `ErrorFrame.Payload` (previously 17 lines explaining ADR-051 B2 + the Zod-codegen TDZ workaround) is deleted. Correct — the generator now produces `*ErrorPayload` directly, so the workaround is no longer needed. **However**, the explanatory rationale for the **inline-mirror pattern itself** (the comment in `contracts/asyncapi.yaml` "Inline mirror of components.schemas.ErrorPayload … because the Zod codegen evaluates schemas eagerly at module load") is the only place the inline-vs-$ref tradeoff is documented. Note for future maintainers: a future contributor who "cleans up" the inline-mirror comment in asyncapi.yaml will re-trigger the original Zod TDZ error unless they convert it to an actual `$ref`. | Consider adding a 1-line pointer comment to `// matchingNamedInlineGoType` explaining why the generator must stay shape-equivalent with the named type, linking to the inline-mirror comment in `contracts/asyncapi.yaml`. Not a bug; a future-proofing observation. |
| TA-7 | OBS | `scripts/gen-asyncapi-go/main.go:429` | The candidate-name construction `strings.TrimSuffix(ownerGoName, "Frame") + toPascalCase(propName)` hard-codes the `Frame` suffix. If the AsyncAPI schema set later adopts a different suffix convention (e.g. `Message`, `Envelope`), every such owner will fall through to the unnamed inline-struct path silently. Not a current regression — the codebase currently uses `Frame` exclusively — but worth a comment in the function doc. | Add a doc comment to `matchingNamedInlineGoType` stating that the `Frame`-suffix strip is a stable project convention (verify with `grep -E '\b[A-Z][a-zA-Z]+Frame\b' contracts/asyncapi.yaml` if it ever changes). |
| TA-8 | OBS | `src/lib/llm-error.ts:1-11` | Out-of-scope for Slice A per the Wave 0 plan, but the diff deletes 60 lines of hand-written wire-type mirrors and replaces them with re-exports of the generated `LLMError` / `LLMErrorReplay` types. **This is a CLAUDE.md Constraint #8 fix** (no hand-written wire types) — the prior file would have been flagged by `Wire-Types Lint` (CI job confirmed to run `scripts/check-no-handwritten-wire-types.sh`). The change is correctly aligned with the contract-first rule and was loaded into the same commit; review-level integrity is fine. Flag for the **holistic** reviewer: whether to include this in Slice A's commit scope or split into a separate cleanup commit. | Acceptable as part of the Slice A PR (the cleanup is mechanically tied to using the same regenerated types). If reviewer wants tighter scope, split into `chore(adr-051-rev4): remove hand-written llm-error mirrors` as a separate stacked commit. |
| TA-9 | OBS | `pkg/gateway/inboundschemas/Media{LibraryEntry,AttachmentRequest}.yaml` | Both new files appear in the diff as added. They are NOT hand-written — `scripts/gen-contracts.sh:78-84` runs `cp contracts/components/schemas/*.yaml pkg/gateway/inboundschemas/` as Step 5 of every `make gen-contracts` / `make verify-contracts` run, and the Makefile drift-gate includes `pkg/gateway/inboundschemas/` in `git diff --exit-code`. Correct behaviour; recorded so reviewers don't mistake them for hand-written. | No action. |
| TA-10 | OBS | `pkg/api/generated/openapi_types.gen.go:5523-5563` | Both new struct types `MediaLibraryEntry` and `MediaAttachmentRequest` are generated from the openapi.yaml routes — fields map correctly to the YAML schema (required fields non-pointer, optional pointers with `omitempty`, `int64 size` correct, RFC3339 time correctly mapped to `time.Time`). Spot-checked against the YAML: matches. The `ContentInjectionOverride` field is `*string` (optional, omitempty), `MediaId` is `string` (required, no omitempty), `Position` is `*int32` (optional). All match the YAML's `required:` block. | No action. |

---

## Drift-gate (CI) coverage

Confirmed observed in `Makefile:384` and `.github/workflows/pr.yml` (`verify-contracts` job):

```
git diff --exit-code -- contracts/ pkg/api/generated/ src/lib/api/generated/ pkg/gateway/inboundschemas/
```

This is the drift check that **is** exercised by CI. A future contributor who edits `contracts/components/schemas/Media{LibraryEntry,AttachmentRequest}.yaml` or any other contract artifact without re-running `make gen-contracts` will trip this gate. ✅ Coverage confirmed.

## Go-test (CI) coverage of the new test

Confirmed observed in `.github/workflows/pr.yml:362-388` — `Run go test` step runs `go test -tags goolm,stdjson -count=1 ./...`. The `./...` pattern includes `scripts/gen-asyncapi-go/`. Verified locally:

```
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -run '^TestGenerate' ./scripts/gen-asyncapi-go/
=== RUN   TestGenerateUsesMatchingNamedInlinePayload
--- PASS: TestGenerateUsesMatchingNamedInlinePayload (0.00s)
PASS
```

✅ The test is exercised by CI and currently passes.

## Pre-existing test preservation

Confirmed observed: `TestWriteStruct_ThreeWayCollisionErrors` (the prior test at `scripts/gen-asyncapi-go/main_test.go:21-45`) is preserved in the diff (only an appended new test follows it). No test in the diff was lost. ✅

## Test count delta

- Before commit `f6eccbcd`: 1 test (`TestWriteStruct_ThreeWayCollisionErrors`)
- After commit `08690ff9`: 2 tests (the above + `TestGenerateUsesMatchingNamedInlinePayload`)
- Net delta: +1 test, +33 LoC test code, +60 LoC generator code (`matchingNamedInlineGoType` + `sameSchemaShape`).

---

## Verdict

**Verdict:** ACCEPT-WITH-FOLLOWUP (1 OBSERVATION-only flag for the holistic reviewer on scope creep into `src/lib/llm-error.ts`; all other findings are gap-fills for the new generator logic, not blocking).

**Counts:** **0** CRITICAL, **3** MAJOR, **1** MINOR, **6** OBSERVATION.

**Wave 0 uniqueness:** Per the Wave 0 plan, Slice A normally gets **no review gate** ("wire-gen is mechanical + verifiably correct on `make verify-contracts`"). The pr-test-analyzer was dispatched under operator override; the finding above is the responsible-surfaces-not-the-blocking-surfaces distinction — the new generator logic is unit-tested, but the unit test covers a single positive branch out of six distinct branches in the new function. The test is sufficient for the regression it claims (the prior hand-edit), but not for the broader surface area the new code introduces.

**Recommended next action:** add the four sub-tests from TA-2 / TA-3 / TA-4 (and optionally TA-5) before the next wave, in a single follow-up commit `test(gen-asyncapi-go): cover all matchingNamedInlineGoType branches`. None of these is a blocker for Wave 1 (B1, B2, C, F, E) — the only path the new generator currently uses against the live `contracts/asyncapi.yaml` is the optional-field branch (TA-1 already covered). The required-field branch (`isRequired=true`) and the ref/empty/not-in-map short-circuits are guard rails for future schema additions.
