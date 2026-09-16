# Wave 1 — type-design-analyzer review (round 1)

**Reviewer:** `pr-review-toolkit:type-design-analyzer`  
**Scope:** `sendfile-fix` HEAD `fba0acbf` against parent `d0e7374a`  
**Slices:** B1 + C + F + E backend stack (`cf7d8782`, `cda59abe`, `2c97b0bb`, `fba0acbf`)  
**Context:** delivery-plan v3 commit `f6eccbcd` (the document body still self-labels v2), governing ADR/spec, and Wave 0 round-2 type-design report  
**Mode:** read-only review; the only written artifact is this requested report

## Verdict

**REVISE / BLOCK WAVE HANDOFF — 1 CRITICAL, 8 MAJOR, 4 MINOR, 3 OBSERVATIONS.**

The strongest designs are `Library`'s private mutable state, its defensive cloning, the minimal `Puller` interface, and `ErrLadderFloor` as a stable routing sentinel. The stack is nevertheless blocked by a contract/type split that makes both new Slice E codes invalid on the SPA boundary, plus domain models that expose or duplicate state instead of making invariants unrepresentable.

## Findings

| ID | Severity | Surface | Finding | Required correction |
|---|---|---|---|---|
| **TD-C1** | **CRITICAL** | Slice E — `LLMErrorCode` / AsyncAPI | `CodeToolArgs` and `CodeSchema` are runtime wire values, but neither canonical AsyncAPI enum contains them. The gateway serializes `string(le.Code)`, transcript replay preserves the string, generated Go accepts any string, and the generated SPA Zod schemas reject both values. The WS parser consequently drops these error frames as invalid. | Add `tool_args` and `schema` to both canonical schemas (`LLMError.yaml`, `LLMErrorReplay.yaml`) first, regenerate every artifact, and add an exhaustive contract test proving every internal classifier code is accepted by live and replay schemas. Prefer one generated/canonical code vocabulary over a parallel handwritten enum. |
| **TD-M1** | **MAJOR** | Slice B1 — missing `ManifestEntry` / `Refcount` domain types | The library uses generated `gen.MediaLibraryEntry` as its in-memory and persisted domain model and stores the same logical refcount in both `entry.Refcount` and `map[string]int`. Required domain facts are nullable pointers, while two mutable locations can disagree. `Load` even accepts a missing entry-level refcount when the parallel map contains one. | Introduce a private invariant-bearing manifest entry and a non-negative refcount type; keep one refcount source of truth. Validate once during construction/load, then project to `gen.MediaLibraryEntry` only at the API edge. |
| **TD-M2** | **MAJOR** | Slice B1 / Wave 0 carry-forward — `MediaLibraryEntry.workspace_id`, `refcount` | B1 proves `workspace_id` is server-assigned (`Upload` derives it from `Library.workspaceID`), but the contract still does not mark it `readOnly`. FR-007a says every manifest entry maintains a refcount and B1 always initializes it, yet `refcount` is not required. The wire type therefore permits states the domain forbids. | Mark `workspace_id` read-only and make `refcount` a required read-only response field. Decide whether `last_refcount_seen_at` is genuinely optional; if it is an internal GC implementation detail, remove it from the public wire type. Regenerate after schema changes. |
| **TD-M3** | **MAJOR** | Slice C — `Model` / `Catalog.Resolve` / `Catalog.Models` | `Model` is exported and mutable. `Resolve` claims its result is safe to mutate, but only shallow-copies it: `InputModalities` and an explicit `ResizeBudget` pointer alias catalog-owned memory. `Models` also returns shared slices/pointers and merely tells callers not to mutate them. External mutation can corrupt catalog state outside `Catalog.mu` and create races with `Resolve`/`Refresh`. | Return deep-owned values or expose an immutable resolved-model API with private fields and accessors (`Supports`, `Budget`, `ID`, `Provider`). Never return catalog-owned slices or pointers. |
| **TD-M4** | **MAJOR** | Slice C — `Catalog.Refresh`, `Puller`, `Store` mutex contract | Interface comments say `Catalog.Refresh` serializes pulls and that a `Store` need not be concurrent-safe. It does not: concurrent calls can invoke `Puller.Pull` and `Store.Write` concurrently. Version-check and apply are also separate lock operations, so two refreshes can both pass against the same old version and apply out of order. | Add a dedicated refresh mutex around pull → parse → version check → apply → store, or explicitly require concurrency-safe `Puller`/`Store` implementations and make compare-and-apply atomic under one catalog lock. The former matches the documented contract. |
| **TD-M5** | **MAJOR** | Slice C — `Seed.validate`, modality/version types | The parser's comments promise non-empty modality values and at least text support, but validation only checks slice length; `input_modalities:[""]`, duplicates, missing `text`, and empty `provider` all pass. `Version`, `SchemaVersion`, `UpdatedAt`, and `Source` are also optional zero values. Version regression uses lexical string comparison despite the plan requiring semver-tagged updates, so valid `v10` can compare below `v2`. | Separate a permissive JSON DTO from a validated domain seed. Require provider and catalog metadata, reject empty/duplicate modalities, decide and enforce the text invariant, and parse version into a format-aware comparable type. Use a `Modality` string type with known constants while retaining a validated non-empty unknown value for forward compatibility. |
| **TD-M6** | **MAJOR** | Slices C + F — optional/duplicated budget model | There are two representations of the same invariant: `capabilities.ResizeBudget{LongEdgePx int, MaxBytes int64}` and `resize.Budget{LongEdge int, MaxBytes int}`. The catalog allows an unbounded `int64`, while the resize API requires `int`; the eventual adapter can overflow on 32-bit targets. Known resolved models have a non-nil budget, but unknown resolved models return `ResizeBudget:nil` even though `Resolve`/`OptimisticModel` documentation says the catalog default is carried. | Keep budget optional only in the seed DTO. Return a resolved model with a required, validated canonical budget. Reuse one budget type or provide an explicit checked conversion to a resize-specific type; byte counts should remain `int64`. |
| **TD-M7** | **MAJOR** | Slice E — `outcomeFallbackEligible` classification state | FR-017 permits fallback only for `CodeUnknown`, but the implementation also treats `CodeProviderRejected` as inconclusive. This is necessary only because every residual real 4xx is classified as `CodeProviderRejected`, so `CodeUnknown` is unreachable for the intended status-bearing tail. The same enum value now means both a conclusive provider rejection and an inconclusive classification; arbitrary non-excluded 400s with media trigger destructive strip-retry. | Do not overload a user-facing code with classifier confidence. Either classify residual eligible 4xx as `CodeUnknown`, as the spec says, or return an internal classification result such as `{Code, Conclusive, RetryClass}` and gate on that explicit discriminator. |
| **TD-M8** | **MAJOR** | Slice E — `TryMediaDowngrade` result / `outcomeRelabel` | `TryMediaDowngrade` returns only `bool`, losing whether classifier-primary or outcome-fallback fired and which media class changed. The caller recomputes the classifier instead. On success it writes `turnState.outcomeRelabel`, but production code never reads `outcomeRelabelCode`; the only read-like assertion is a test-only helper that mirrors intended behavior rather than consuming the state. The FR-017a type state is therefore write-only and unenforced. | Return a typed result such as `{Applied, Trigger, MediaClass}` where `Trigger` is a closed internal enum. Use that exact result to relabel the observable turn outcome, or delete `outcomeRelabel` if there is no consumer. Add a test through the real emit/persist path, not a test-only mirror. |
| **TD-m1** | MINOR | Slice B1 — `MediaLibraryEntrySource` | `test_fixture` is a production wire enum value and `Upload` accepts it through the same public method as live sources. Test-only provenance has become a public persisted state. | Keep the public source enum production-only; inject fixture entries through a test helper or an internal constructor. |
| **TD-m2** | MINOR | Slice C — `GHReleasePuller` configuration | Required owner/repo/asset and transport endpoints are all exported mutable fields. `NewGHReleasePuller` cannot report invalid required values, while direct literals bypass defaults. Mutating fields during `Pull` also violates the interface's concurrency promise. | Validate required fields in a constructor returning `(*GHReleasePuller, error)` and keep production configuration private/immutable; expose explicit test options for client/base URLs. |
| **TD-m3** | MINOR | Slice F — `resize.Result.Mime` | `Result.Mime` is a free string even though successful results can only be PNG or JPEG. That relationship with `Data` is documented but not represented. | Use a closed `OutputFormat`/`MIME` string type with PNG/JPEG constants. A `oneOf` is unnecessary unless formats later gain variant-specific fields. |
| **TD-m4** | MINOR | Slice B1 — `Library.Load` / `Store` | Persistence lifecycle operations are public even though `New` loads automatically and every mutator persists transactionally. Callers can reload external disk state over a live library or force redundant stores, widening the state-transition surface. | Keep load/store private unless an external lifecycle use case exists; expose a narrowly named recovery/sync operation if required. |
| **TD-O1** | OBSERVATION | Slice B1 — `Library` encapsulation | `Library` keeps its path, workspace identity, maps, clock, and mutex private, and `cloneEntry` deep-copies pointer fields before values leave the lock. This correctly prevents direct caller mutation of current library state. | Preserve this ownership boundary when introducing a private `ManifestEntry`. |
| **TD-O2** | OBSERVATION | Slice C — `Puller` abstraction | `Puller` is a narrow, useful transport seam (`Pull(context.Context) ([]byte, error)`) that keeps parsing and last-known-good policy in `Catalog`. The interface shape itself is appropriate; TD-M4 concerns its concurrency promise, not its usefulness. | Keep the interface narrow; align implementation synchronization with its documented contract. |
| **TD-O3** | OBSERVATION | Slice F — `ErrLadderFloor` / boundary validation | `ErrLadderFloor` is an appropriate sentinel for `errors.Is` routing, and `ResizeToFit` validates nil images and non-positive budgets before work. No richer error type is required for the current caller. | Preserve the sentinel; strengthen the budget type separately under TD-M6. |

## Detailed evidence and causal chains

### TD-C1 — new classifier codes are rejected at the generated boundary

1. Slice E adds the runtime constants in `pkg/agent/translate_error.go:56-69` and returns them from both HTTP and message classifiers at `pkg/agent/translate_error.go:395-399` and `pkg/agent/translate_error.go:433-437`.
2. The canonical live and replay enums still end at `unknown`: `contracts/components/schemas/LLMError.yaml:10-19` and `contracts/components/schemas/LLMErrorReplay.yaml:10-19`.
3. Generated Go does not enforce an enum; both fields are plain strings at `pkg/api/generated/asyncapi_types.gen.go:275-288`.
4. The live gateway copies the internal code into that string at `pkg/gateway/websocket.go:3377-3385`. Transcript persistence does the same at `pkg/agent/turn.go:1293-1301`, and replay forwards the stored string at `pkg/gateway/replay.go:1033-1039`.
5. The SPA validator remains closed over the old enum at `src/lib/api/generated/schemas.ts:7558-7573`. The WS worker drops any failed frame parse at `src/workers/ws-parser.worker.ts:42-68`.
6. Direct observed reproduction on HEAD:

```text
$ node --experimental-strip-types --input-type=module -e '<import generated LLMError; safeParse tool_args/schema>'
tool_args rejected
schema rejected
```

This is a contract-first violation and an observable data-loss path, not merely stale generated typing.

### TD-M1 / TD-M2 — B1's internal and public models permit contradictory states

- `Library` correctly hides its fields, but its core state is `map[string]gen.MediaLibraryEntry` plus `map[string]int` at `pkg/media/library/library.go:52-58`.
- The persisted form repeats the same split at `pkg/media/library/library.go:68-72`.
- Upload initializes both copies at `pkg/media/library/library.go:201-218`; refcount mutation updates both at `pkg/media/library/library.go:467-488`; persistence re-projects both at `pkg/media/library/library.go:493-507`.
- Load treats the parallel map as authoritative and reconstructs `entry.Refcount`; a nil entry refcount passes because mismatch checking is conditional at `pkg/media/library/library.go:537-545`.
- Server-required identity/integrity fields remain generated pointers because a response/wire shape is being used as a domain object (`Id`, `Mime`, `Size`, `Sha256`, `UploadedAt` at `pkg/media/library/library.go:203-214`). The rest of the package must repeatedly defend against nil (`pkg/media/library/library.go:249-250`, `517-546`).
- Defensive cloning at `pkg/media/library/library.go:575-592` prevents returned pointers from mutating the map, which rejects the stronger hypothesis that public read results directly alias library state. It does not solve invalid-state representability or duplicated ownership.

A domain `ManifestEntry` should make ID, workspace, MIME, size, digest, upload time, and refcount required values. `gen.MediaLibraryEntry` should be a projection, not storage.

### TD-M3 / TD-M4 / TD-M5 — Catalog's lock protects the map, not its exported referents or refresh transaction

- `Model` exposes mutable slices and pointers at `pkg/providers/capabilities/catalog.go:79-95`.
- `Resolve` promises a safe copy at `pkg/providers/capabilities/catalog.go:312-325`, but `cloneWithDefaultBudget` only replaces a nil budget; explicit budgets and `InputModalities` remain aliases (`pkg/providers/capabilities/catalog.go:450-459`).
- `Models` admits its budget pointer is shared at `pkg/providers/capabilities/catalog.go:359-369`. The map copy is therefore not an ownership boundary.
- `Refresh` has no operation-level mutex (`pkg/providers/capabilities/catalog.go:406-447`). `Version()` locks only its read, and `applySeed()` locks only replacement, leaving compare-and-apply non-atomic (`pkg/providers/capabilities/catalog.go:297-310`, `431-436`).
- The documented validation list at `pkg/providers/capabilities/catalog.go:186-199` is stronger than the implementation at `pkg/providers/capabilities/catalog.go:200-234`; there is no modality-item, provider, metadata, or version-format check.

### TD-M6 — seed optionality leaked into the resolved model

`ResizeBudget` is legitimately optional while decoding seed JSON, but it is not optional after resolution: the catalog always has a validated default. Known models without overrides are normalized to a non-nil pointer at `pkg/providers/capabilities/catalog.go:453-458`, while unknown models return nil at `pkg/providers/capabilities/catalog.go:319-340`. The existing test explicitly codifies this inconsistent result at `pkg/providers/capabilities/catalog_test.go:282-295`.

The resize package then defines a second budget with a narrower byte type at `pkg/media/resize/resize.go:34-40`. `ResizeToFit` validates at call time (`pkg/media/resize/resize.go:82-88`), which safely rejects invalid calls, but it does not prevent the invalid value from existing or make catalog-to-resize conversion safe.

### TD-M7 / TD-M8 — classifier confidence and retry outcome need distinct types

- The governing FR states that outcome fallback fires only when classification returns `CodeUnknown`: `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1080-1082`.
- The plan's locked decision also says an unrecognized 400 returns `CodeUnknown`: `docs/internal/plans/ADR-051-rev4-delivery-plan.md:221`.
- In code, every residual 4xx becomes `CodeProviderRejected` at `pkg/agent/translate_error.go:369-401`.
- `outcomeFallbackEligible` compensates by accepting both values at `pkg/agent/media_downgrade.go:167-192`. Tests intentionally expect arbitrary residual 400s to strip media, including a body whose image mention is off-context (`pkg/agent/media_outcome_retry_test.go:449-486`).
- `TryMediaDowngrade` returns only a bool at `pkg/agent/media_downgrade.go:76-110`, forcing `loop.go` to reclassify at `pkg/agent/loop.go:6915-6933`.
- The success branch stores a relabel at `pkg/agent/loop.go:6934-6947`, but the field/accessor at `pkg/agent/turn.go:261-326` has no production read site. The focused test uses a test-only `recordedVerdictForTurn` rather than `turnState.outcomeRelabelCode` (`pkg/agent/media_outcome_retry_test.go:489-550`).

The type system should distinguish:

- classification code (user-facing/wire),
- classification confidence or fallback eligibility (internal),
- downgrade trigger (classifier-primary vs outcome-fallback),
- affected media class (PDF vs image), and
- post-retry outcome.

A closed internal result type is sufficient; this does not require a wire `oneOf`.

## Read-only / required / discriminator assessment

### Fields that should be read-only or hidden

- `MediaLibraryEntry.workspace_id`: public response field, but server-owned; add `readOnly: true`.
- Catalog `Model` fields: should be immutable to consumers, either through private fields/accessors or deep-owned snapshots.
- `GHReleasePuller` production configuration: required values should be constructor-owned and immutable after construction.
- Library manifest internals: generated pointer fields should be hidden behind an invariant-bearing private domain entry.

### Optionals that should be required

- Public `MediaLibraryEntry.refcount`: required by FR-007a and always initialized by B1.
- Resolved model budget: required after catalog fallback resolution; optional only in the seed DTO.
- Seed metadata (`version`, `schema_version`, `updated_at`, `source`) and `Model.provider`: required for accepted live catalog state.
- `last_refcount_seen_at`: either required in the internal GC entry or absent from the public contract; its current public optional state has no clear contract.

### Enum / `oneOf` decisions

- **LLM error codes:** first fix the canonical closed enum. A `oneOf` discriminator is not required while all variants share the same fields. If the contract intends to enforce code-dependent `retryable` constants or variant-specific fields later, then an inline discriminated `oneOf` becomes justified.
- **Modalities:** not a `oneOf`; a model supports a set of simultaneous modalities. Use a validated string type with known constants and a deliberate unknown-forward-compatible case.
- **Resize output format:** a closed two-value enum is enough; no `oneOf` until PNG/JPEG results diverge structurally.
- **Downgrade trigger/media class:** closed internal enums or a discriminated Go result type are appropriate; these do not cross the gateway boundary.

## Competing hypotheses and dispositions

| Hypothesis | Evidence sought | Disposition |
|---|---|---|
| H1 — generated Go prevents an unsupported LLM code from reaching the wire | Generated `LLMError.Code` type | **Rejected.** It is plain `string` in `asyncapi_types.gen.go:277/286`; gateway and replay assign strings directly. |
| H2 — `CodeToolArgs` / `CodeSchema` are internal-only classifier states | Live/replay emit paths | **Rejected.** Gateway live frames, transcript persistence, and replay all carry `string(llm.Code)`; direct SPA schema parsing rejects both. |
| H3 — `Resolve`/`Models` return ownership-isolated catalog values | Deep-copy behavior for slices/pointers | **Rejected.** Map structs are copied shallowly; modality backing arrays and explicit budget pointers remain shared. |
| H4 — `Catalog.Refresh` serializes `Puller` and `Store` as documented | An operation-level mutex or singleflight | **Rejected.** Only state getters/apply use `mu`; pull, version check, and store are outside one serialized transaction. |
| H5 — residual `CodeProviderRejected` is intentionally identical to `CodeUnknown` in plan/spec | Governing FR and locked plan decision | **Rejected.** Both explicitly say the fallback is `CodeUnknown`-only; implementation broadens the semantic after discovering residual 4xx never produce unknown. |
| H6 — library read APIs expose pointers that mutate internal state | Clone behavior | **Rejected.** `cloneEntry` deep-copies every pointer before returning. The remaining defect is internal invalid-state/duplicate ownership, not direct caller aliasing. |
| H7 — Slice F needs a wire `oneOf` for PNG vs JPEG | Variant-specific fields/behavior | **Rejected for this wave.** Both outputs have the same shape; a typed MIME enum is sufficient. |

## Verification observed

### Target reproduction

```text
$ git rev-parse HEAD
fba0acbf9d8ffbc2b24fb69bf636fa2c7e0b37ca

$ git rev-parse d0e7374a
d0e7374ac09da59a9a5949975153e7f2903dd54d
```

Commit stack observed:

```text
cf7d8782 Slice C
cda59abe Slice B1
2c97b0bb Slice F
fba0acbf Slice E
```

### Scoped behavior tests

```text
$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
    ./pkg/media/library ./pkg/providers/capabilities ./pkg/media/resize
ok  github.com/elicify-ai/omnipus/pkg/media/library
ok  github.com/elicify-ai/omnipus/pkg/providers/capabilities
ok  github.com/elicify-ai/omnipus/pkg/media/resize

$ CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/agent \
    -run '^(TestClassifier_CodeToolArgs|TestClassifier_CodeSchema|TestStep4_.*)$'
ok  github.com/elicify-ai/omnipus/pkg/agent
```

These passes confirm the implementation behaves as its tests specify; they do not clear the type findings. In particular, the agent tests codify `CodeProviderRejected` as the fallback's practical inconclusive value, and the catalog test codifies a nil budget for unknown resolved models.

### Before/after boundary check

- Parent `d0e7374a`: no runtime `tool_args`/`schema` classifier values existed.
- HEAD `fba0acbf`: runtime classifier emits both values, while `LLMError.yaml`, `LLMErrorReplay.yaml`, generated AsyncAPI TS types, and their enums were not updated for those values.
- Direct HEAD Zod observation: both new values are rejected.
- `git diff --check d0e7374a..fba0acbf`: clean.

No full Go suite was run; repository instructions make CI authoritative and prohibit the full suite in this devpod. No graph update was run because this review was explicitly read-only.
