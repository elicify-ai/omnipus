# Final Full-Diff Review — Code Simplifier

**Branch:** `sendfile-fix`  
**Range:** `ae9271d0..1ea79f9e` (43 commits)  
**Scope:** 138 changed files, +20,173 / -288 lines  
**References read:** ADR-051 Rev 4, workspace-media-library specification, ADR-051 Rev 4 delivery plan  
**Reviewer:** `pr-review-toolkit:code-simplifier`  
**Mode:** read-only; only this requested report was created

## Verdict

# BLOCK — 0 CRITICAL / 9 MAJOR

The diff has substantial tested implementation, but the final composition is not shippable. The two primary user surfaces are implemented as parallel islands: workspace uploads create refs that the production turn path cannot resolve, and the SPA calls workspace-media REST operations that the backend never dispatches. The same pattern repeats in the capability puller, per-model resize budgets, and orphan GC: large abstractions are present and heavily unit-tested, but have no production caller.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 9 |
| MINOR | 7 |
| OBSERVATION | 2 |

## Major findings

### MAJOR-01 — The workspace-ref presentation path is disconnected and bypasses its integrity boundary

**Evidence**

- Uploads return `media://workspace/<ws>/<id>` refs at `pkg/gateway/rest.go:8814-8874`.
- The production turn path passes those refs to `resolveMediaRefsWithOffload` at `pkg/agent/loop.go:6314-6317`.
- That resolver calls `ResolveWithMetaOpts(ref, media.ResolveOpts{})` with an empty caller context at `pkg/agent/loop_media.go:115`.
- Workspace refs explicitly reject an empty caller context at `pkg/media/store.go:275-282`.
- `FileMediaStore.SetWorkspaceLibraryProvider` has no production caller; its only callers are tests (`pkg/media/store.go:223-232`, `pkg/media/resolve_test.go:71-80`, `pkg/media/resolve_test.go:179-185`).
- Even after wiring the provider/context, the path resolver deliberately returns an unverified raw path at `pkg/media/library/library.go:507-537`; the decode-bound turn path then `Stat`s and decodes that path directly at `pkg/agent/loop_media.go:126-156` and `pkg/agent/loop_media.go:509-562`. `Library.Read`, the SHA-256 integrity gate at `pkg/media/library/library.go:455-493`, is not used.

**Impact**

Every new workspace upload reaches the turn as an unavailable attachment instead of provider media/offload. If the missing resolver wiring is added alone, workspace media will still enter `image.Decode` without the required SHA-256-on-read check.

**Why CI missed it**

- Presentation tests register legacy refs in `FileMediaStore`, not workspace-library refs (`pkg/agent/loop_media_present_test.go:118-137`).
- Resolver tests manually install the provider and caller context (`pkg/media/resolve_test.go:71-103`).
- Upload tests stop after direct `Library.Read`; they do not feed the returned ref through `runTurn` (`pkg/gateway/upload_test.go:435-477`).

**Simplification**

Use one decode-bound resolver for workspace refs: accept the turn workspace ID, resolve the cached `Library`, call its verified read API, and present the verified bytes. Keep `FileMediaStore` only as the legacy/session-inline fallback. Remove the unused path-provider seam from the decode path rather than maintaining two workspace resolution APIs with different integrity guarantees.

### MAJOR-02 — The contract and SPA define a workspace-media REST product that the backend does not implement

**Evidence**

- OpenAPI declares list, attach, get, and delete operations at `contracts/openapi.yaml:5446-5568`.
- SPA wrappers call all of those endpoints at `src/lib/api.ts:3283-3319`.
- The Media tab and composer picker depend on them at `src/components/workspaces/WorkspaceMediaTab.tsx:63-90` and `src/components/chat/ComposerMediaLibrary.tsx:75-100`.
- `HandleWorkspaces` handles only milestones, delegation, instructions, and the one-segment workspace resource; every other nested path returns 404 at `pkg/gateway/rest_workspaces.go:494-544`.
- There are no production gateway symbols for the generated operation IDs or `MediaAttachmentRequest`.
- Workspace image previews are also misrouted: the SPA emits `/api/v1/media/${mediaId}` at `src/lib/omnipus-runtime.ts:283-290`, while `HandleMedia` reconstructs a legacy `media://<id>` ref at `pkg/gateway/rest.go:9147-9156`.

**Impact**

The Media tab, picker, attach action, explicit delete, and workspace-media preview all 404 in production.

**Why CI missed it**

Both new frontend suites mock the API module (`src/components/workspaces/WorkspaceMediaTab.test.tsx:14-20`, `src/components/chat/ComposerMediaLibrary.test.tsx:19-26`), so no test crosses the HTTP boundary.

**Simplification**

Implement one workspace-media dispatcher under `HandleWorkspaces`, with list/get/delete/attach subroutes using the cached `Library`; add one HTTP integration test that exercises the generated paths through the real mux. Do not retain a contract, four client wrappers, two components, and mocked tests without the single backend dispatcher that makes them real.

### MAJOR-03 — The capability update subsystem and per-model resize budgets are production-dead

**Evidence**

- `NewAgentLoop` constructs the catalog with `puller=nil, store=nil` at `pkg/agent/loop.go:638-653`.
- There is no production call to `NewGHReleasePuller`, `SetCapabilityCatalog`, or `Catalog.Refresh`; the only non-test occurrences are definitions/comments.
- There is no startup refresh, 7-day timer, or concrete last-known-good store.
- `resolvedModel.Budget` and `Catalog.DefaultResizeBudget` have no production consumer (`pkg/providers/capabilities/catalog.go:134-138`, `pkg/providers/capabilities/catalog.go:568-573`).
- The image path hardcodes `LongEdgePx: 7680` and derives bytes from the unrelated legacy `maxSize` setting at `pkg/agent/loop_media.go:569-579`.

**Impact**

FR-025 never runs, pulled catalogs can never become live, last-known-good cannot persist, and every model receives the same resize dimensions despite the catalog carrying validated per-model budgets.

**Simplification**

Either wire one concrete gateway-owned catalog service—file store, `GHReleasePuller`, startup refresh, one ticker, and `Resolve(model).Budget()` passed into resize—or delete the pull/store/refresh surface until that service exists. The current middle state pays for roughly 1,200 lines of catalog/puller/version machinery while production consumes only `Supports(image)` from the embedded seed.

### MAJOR-04 — Orphan GC and its refcount framework have no production execution path

**Evidence**

- `Library.OrphanGC` is defined at `pkg/media/library/library.go:722-805`.
- There is no non-test call to `.OrphanGC(` anywhere in `pkg/`, `cmd/`, or `internal/`.
- No timer enumerates cached workspace libraries and no operator configuration reaches `OrphanGCConfig`.
- Nevertheless, the turn path maintains two `sync.Map` layers and per-session wrappers at `pkg/agent/media_present.go:41-84`, `pkg/agent/media_present.go:145-170`, and `pkg/agent/media_present.go:219-259`.
- `sessionRefcounter.DecrementRefcount` is an intentional no-op stub that exists only to satisfy an interface at `pkg/agent/media_present.go:78-84`.

**Impact**

The promised 30-day lifecycle never deletes an orphan. The system carries persistence writes and session bookkeeping on every attachment while the only consumer of that state is never scheduled.

**Simplification**

Introduce one lifecycle owner with a narrow `Increment` interface and a shutdown-aware GC ticker, or remove the refcount/GC scaffolding until it can run. Split increment-only and mutable-library contracts so no-op methods are not required to satisfy an over-broad interface.

### MAJOR-05 — `media_present.go` is a nominal orchestrator; the actual seven-step chain remains a 906-line branch matrix

**Evidence**

- `media_present.go` claims to compose the seven-step presentation chain at `pkg/agent/media_present.go:7-15`, but defines no presenter or presentation function; it contains catalog access, cache, and refcount helpers.
- The real path is `resolveMediaRefsWithOffload`, a seven-argument function at `pkg/agent/loop_media.go:78-92`, embedded in a 906-line file.
- The step ordering is asymmetric: the capability gate is applied only to images at `pkg/agent/loop_media.go:145-157`; PDF handling still uses the old substring allow-list and direct extraction at `pkg/agent/loop_media.go:197-211`.
- A text-only PDF therefore skips the required step-5 offload/guidance and receives only step-6 extraction. Tests assert `buildOffloadGuidance` in isolation rather than a PDF presentation flow (`pkg/agent/loop_media_offload_test.go:255-271`).

**Impact**

The file boundary promised by the ADR adds indirection without centralizing behavior, and new formats must update scattered conditionals. This architecture directly enabled the missing workspace resolver, budget, PDF gate, and integrity wiring above.

**Simplification**

Move the actual decision function into `media_present.go` with a small dependency struct (`library resolver`, `catalog`, `offload sink`, `ref incrementer`) and a single per-attachment result union (`native`, `text`, `offload`, `marker`). Keep byte encoding helpers separate. Eliminate the seven positional/nilable arguments and make every step explicit in one switch.

### MAJOR-06 — The typed outcome-retry state contains one always-empty field and one write-only field

**Evidence**

- `MediaDowngradeResult` promises `{Applied, Trigger, MediaClass}` at `pkg/agent/media_downgrade.go:50-59`.
- Both callers discard the media class returned by `applyMediaDowngrade` at `pkg/agent/media_downgrade.go:104-107` and `pkg/agent/media_downgrade.go:125-127`.
- `applyMediaDowngrade` computes PDF/image classes separately at `pkg/agent/media_downgrade.go:137-159`, but never places them in the result.
- The production log reads `downgradeResult.MediaClass` at `pkg/agent/loop.go:6985-6995`, so it always emits the zero/empty value.
- `turnState.outcomeRelabel` is written at `pkg/agent/turn.go:307-315` and `pkg/agent/loop.go:7008-7010`, but has no production read site. The comment naming `emitError` as a consumer at `pkg/agent/turn.go:318-322` is not matched by code.

**Impact**

The new type gives a false appearance of stronger modeling while dropping both pieces of claimed observability: media class is wrong and FR-017a relabel state is inert.

**Simplification**

Have `applyMediaDowngrade` return one fully-populated result—no parallel tuple—and consume the relabel immediately at the actual record/emit boundary. If no persisted classification exists, remove `outcomeRelabel` rather than keeping write-only state.

### MAJOR-07 — `UploadFixture` is an exported, near-complete duplicate of `Upload`

**Evidence**

- Production upload occupies `pkg/media/library/library.go:331-453`.
- `UploadFixture` repeats the same temp-file, hash, copy, sync, rename, manifest, persistence, and rollback sequence at `pkg/media/library/library.go:1022-1135`.
- Despite comments calling it “package-private,” capitalized `UploadFixture` is exported in Go.
- The duplicate has already drifted: production upload syncs the containing directory at `pkg/media/library/library.go:402-410`; fixture upload does not after its rename at `pkg/media/library/library.go:1088-1093`.

**Impact**

About 100 lines of stateful filesystem code must be fixed twice, and the test-only source is available to production callers.

**Simplification**

Extract one unexported `upload(filename, source, reader)` implementation. Keep the public `Upload` source check. If external-package tests need fixture source, expose a test-only wrapper from an `_test.go` export file so it is absent from production binaries.

### MAJOR-08 — The catalog contains a 194-line version micro-framework plus unused public surfaces

**Evidence**

- `pkg/providers/capabilities/version.go:25-194` hand-parses and compares a subset of semver.
- `ParseVersion` has an error return that never returns an error (`pkg/providers/capabilities/version.go:45-61`).
- The type exists for one production comparison at `pkg/providers/capabilities/catalog.go:640-647`.
- `KnownModality` claims to be a runtime boundary at `pkg/providers/capabilities/modality.go:31-45` but has only test consumers; production `Supports` simply scans the stored slice at `pkg/providers/capabilities/catalog.go:120-132`.
- The custom `logger` interface and `noopLogger` duplicate `*slog.Logger` plus `slog.DiscardHandler` at `pkg/providers/capabilities/catalog.go:407-413` and `pkg/providers/capabilities/catalog.go:662-666`.

**Impact**

A small “reject older catalog” check carries a bespoke parser, speculative error contract, dead public map, and logging abstraction.

**Simplification**

Keep catalog version as a string. Compare valid semver with the existing standard Go semver package and ISO-date versions lexically in one helper. Delete the never-error return, `KnownModality`, custom logger, and `noopLogger`.

### MAJOR-09 — A 238-line contract test parses both Go AST and generated TypeScript text

**Evidence**

- `pkg/api/generated/llm_error_codes_test.go:49-105` parses `translate_error.go` via `go/ast`.
- `pkg/api/generated/llm_error_codes_test.go:107-165` hand-parses `z.enum([...])` from generated TypeScript source, including quote/escape scanning.
- It then validates every code against the existing component-schema validator at `pkg/api/generated/llm_error_codes_test.go:167-238`.

**Impact**

The test is coupled to source layout in two languages and can break when either generator changes formatting, even when the actual wire contract remains valid. Test infrastructure outweighs the property under test.

**Simplification**

Generate the canonical code list from the contract once and compare runtime values to that generated list, or use a small table of runtime constants plus the existing schema validator. Do not parse generated TypeScript as text from a Go test.

## Minor findings

### MINOR-01 — Test-only range configurability remains in production

`stripRejectedImageMedia` has a variadic `imageStripRange` at `pkg/agent/media_downgrade.go:244-291`; production always calls it without a range (`pkg/agent/media_downgrade.go:148`), while only tests pass the option. Remove the option and test the production full-slice contract.

### MINOR-02 — Manual case-insensitive prefix logic is unjustified

`startsWithCaseInsensitive` is 18 lines at `pkg/agent/media_downgrade.go:293-310` and is described as a hot-path optimization, although downgrade runs at most once per media class per turn. Normalize once or use the standard string helper.

### MINOR-03 — Duplicate helpers remain despite a modern Go baseline

`maxInt` is duplicated in `pkg/media/resize/resize.go:179-184` and `pkg/agent/loop_media.go:599-604`; use the Go `max` builtin.

### MINOR-04 — Frontend byte formatting is duplicated

The same `formatBytes` implementation exists at `src/components/chat/ComposerMediaLibrary.tsx:45-57` and `src/components/workspaces/WorkspaceMediaTab.tsx:45-57`. Reuse the existing formatting utilities or one media formatter.

### MINOR-05 — Review-process narration dominates production comments

Production files retain “Wave 1,” “TD-Mx,” “Slice H,” “T9,” and “Pass-2 fix” history, including `pkg/media/library/library.go:1-39`, `pkg/providers/capabilities/catalog.go:29-53`, `pkg/agent/media_downgrade.go:1-8`, and `src/lib/library-attachment.ts:1-19`. Keep only load-bearing invariants; git history and the ADR own implementation chronology.

### MINOR-06 — Workflow-state artifacts are part of the feature diff

`.fablize/goals.json`, `.fablize/ledger.jsonl`, and `system/cost.json` are execution bookkeeping, not product implementation. Remove them from the deliverable unless the repository explicitly treats them as versioned product data.

### MINOR-07 — The full diff fails `git diff --check`

Trailing whitespace remains in several newly-added intermediate review documents, including `docs/internal/reviews/wave0-review-round1-code-reviewer.md:3-5` and `docs/internal/reviews/wave1-review-round1-type-design-analyzer.md:3-6`.

## Observations

### OBS-01 — Two stores are justified only by the locked two-mechanism decision

The legacy `FileMediaStore` and persistent `Library` are not inherently a simplification defect: ADR-051 explicitly keeps agent-generated media session-scoped while user uploads become workspace-persistent. The defect is the unresolved seam between them, not their coexistence.

### OBS-02 — The resize package boundary is acceptable

`pkg/media/resize` has one production consumer, but the ADR explicitly establishes it as a pure-Go component and its API is narrow. The unused catalog budget—not the package boundary—is the blocking complexity.

## Verification

| Check | Result |
|---|---|
| Full diff enumerated | 43 commits; 138 files; +20,173 / -288 |
| Governing ADR/spec/plan | Read in full |
| Production `SetWorkspaceLibraryProvider` callers | 0 |
| Production catalog pull/refresh wiring | 0 |
| Production orphan-GC callers | 0 |
| Production per-model budget consumers | 0 |
| Workspace-media REST dispatcher/handlers | 0 |
| `outcomeRelabel` production readers | 0 |
| CI evidence | 22/22 jobs green at `56093d75` per `docs/internal/uat/ADR-051-rev4-ci-evidence.md`; green CI does not cover the disconnected seams above |
| Local full Go suite | Not run; repository guidance makes CI authoritative in this devpod |
| `git diff --check ae9271d0..HEAD` | Failed on trailing whitespace in added review docs |

## Required before re-review

1. Wire and integration-test upload → workspace ref → verified library read → presentation.
2. Add the workspace-media REST dispatcher and exercise it through the real mux from the SPA contract paths.
3. Either wire catalog refresh/store/timer and consume per-model budgets, or remove the dead update surface.
4. Add a real orphan-GC lifecycle owner and narrow the refcount interfaces.
5. Replace the nominal orchestrator with one explicit presentation decision function.
6. Repair/remove the dead outcome fields.
7. Collapse duplicated and speculative helpers (`UploadFixture`, `Version`, AST/TS parser test).

Re-run the full-diff code-simplifier only after these integration seams exist; unit tests around manually-wired abstractions are not sufficient evidence.
