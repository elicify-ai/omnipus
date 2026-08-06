# Final Full-Diff Review — Comment Analyzer

**Branch:** `sendfile-fix`  
**Diff:** `ae9271d0..1ea79f9e`  
**Focus:** comment accuracy, documentation completeness, stale comments  
**Verdict:** **FAIL — REVISE**  
**CRITICAL:** 0  
**MAJOR:** 9

## Blocking findings

### CA-MAJ-01 — The governing/acceptance documents still describe a pre-implementation state

The ADR remains `Proposed`, the spec remains `Draft`, and the delivery plan calls itself “v2 — pending final-pass review,” even though this branch contains the full implementation, a round-2 plan review, Wave 4 CI evidence, and this final review. The spec’s final “Clarifications” also says four questions are unresolved although the same document resolves all four in its Ambiguity Warnings and FRs. These stale lifecycle statements make it impossible to tell which text is authoritative.

Evidence:
- `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md:3`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:4`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1170`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1246`
- `docs/internal/plans/ADR-051-rev4-delivery-plan.md:1`
- `docs/internal/plans/ADR-051-rev4-delivery-plan.md:9`
- `docs/internal/plans/ADR-051-rev4-delivery-plan.md:228`

Required correction: update statuses/version text and remove or rewrite the stale unresolved clarifications so the final documents have one consistent state.

### CA-MAJ-02 — Required UAT/SC evidence is missing, while the plan presents acceptance as complete

The delivery plan requires committed persona deviations and per-SC observations before final acceptance, and the UAT plan names both artifacts. Neither file exists. Only CI evidence is committed; CI does not substitute for the plan’s behavioral observation gate. The documentation set is therefore incomplete and currently overstates completion.

Evidence:
- `docs/internal/plans/ADR-051-rev4-delivery-plan.md:117`
- `docs/internal/plans/ADR-051-rev4-delivery-plan.md:120`
- `docs/internal/plans/ADR-051-rev4-delivery-plan.md:191`
- `docs/internal/uat/ADR-051-rev4-uat-test-plan.md:113`
- `docs/internal/uat/ADR-051-rev4-uat-test-plan.md:118`
- Missing: `docs/internal/uat/ADR-051-rev4-uat-deviations.md`
- Missing: `docs/internal/uat/ADR-051-rev4-sc-observations.md`

Required correction: commit the required observed evidence, or explicitly mark acceptance/UAT incomplete and stop claiming final delivery.

### CA-MAJ-03 — Capability-catalog documentation claims production startup/7-day refresh wiring that does not exist

Package docs say the puller fetches on gateway startup and every seven days; `SetCapabilityCatalog` says gateway boot injects a puller-backed catalog. In the full diff, `NewGHReleasePuller`, `Catalog.Refresh`, and `SetCapabilityCatalog` have no production call sites—only definitions/tests. Runtime uses only the embedded seed. This makes FR-025/SC-009 documentation materially false.

Evidence:
- `pkg/providers/capabilities/catalog.go:15`
- `pkg/providers/capabilities/catalog.go:607`
- `pkg/providers/capabilities/puller.go:83`
- `pkg/agent/media_present.go:134`
- `pkg/agent/loop.go:215`
- Governing requirement: `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1091`

Required correction: wire startup, persistence, and periodic refresh in production, or change the ADR/spec/comments/UI claims to state that only the compiled seed ships.

### CA-MAJ-04 — Contracts and SPA comments document workspace-media REST endpoints that the backend does not implement

OpenAPI declares list/attach/get/delete operations, and SPA comments describe those endpoints as working server-authoritative surfaces. The full backend diff contains no handlers or route registration for `/workspaces/{id}/media`, `/media/attachments`, or `/media/{media_id}`. The Media tab and picker therefore document a nonexistent server implementation.

Evidence:
- `contracts/openapi.yaml:5448`
- `contracts/openapi.yaml:5477`
- `contracts/openapi.yaml:5508`
- `contracts/openapi.yaml:5539`
- `src/lib/api.ts:3273`
- `src/lib/api.ts:3283`
- `src/lib/api.ts:3295`
- `src/lib/api.ts:3307`
- No matching production handler in `pkg/gateway/`.

Required correction: implement/register the contract operations, or remove the advertised contract/client/UI surface until a backend exists.

### CA-MAJ-05 — Two-tier workspace resolver comments claim gateway injection, but production never wires the provider

`WorkspaceLibraryProvider`, `SetWorkspaceLibraryProvider`, and related comments say the gateway injects the workspace-library cache so channels/replay/tool-result consumers can resolve workspace refs. `SetWorkspaceLibraryProvider` is called only in tests. Production workspace refs therefore cannot traverse the documented `FileMediaStore` two-tier path.

Evidence:
- `pkg/media/resolve.go:45`
- `pkg/media/store.go:223`
- `pkg/media/store.go:293`
- `pkg/media/resolve_test.go:72`
- `pkg/media/resolve_test.go:180`
- Governing claim: `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1094`

Required correction: wire the provider during gateway/agent-loop construction and document the actual ownership/lifecycle of that wiring.

### CA-MAJ-06 — “sha256 verified on every read” documentation is contradicted by the path resolver

The ADR, spec, schema, and package header promise that sha256 is verified on every read and unverified bytes never leave the integrity boundary. `ResolvePathWithCaller` explicitly returns a raw path without reading or verifying it, and its comments justify sending that path to transport consumers. A channel can consequently transmit tampered bytes. The exception is neither permitted nor disclosed by the governing documents.

Evidence:
- `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md:52`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:99`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1064`
- `pkg/media/library/library.go:3`
- `pkg/media/library/library.go:507`
- `pkg/media/resolve.go:35`

Required correction: verify integrity before returning a transport path (or return a verified handle/copy), then make all comments and requirements describe the same boundary.

### CA-MAJ-07 — Orphan-GC documentation describes a running configurable lifecycle, but only a callable method exists

The ADR/spec state that the system runs configurable, default-30-day, operator-disableable orphan GC. The implementation defines `Library.OrphanGC` and unit tests, but the full diff has no production caller, scheduler/ticker, or configuration wiring. Files will never be garbage-collected automatically despite the lifecycle documentation.

Evidence:
- `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md:53`
- `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1069`
- `pkg/media/library/library.go:722`
- All other `OrphanGC` references are in `pkg/media/library/library_test.go`.

Required correction: add the configured production sweep lifecycle and operational documentation, or state clearly that GC is not delivered.

### CA-MAJ-08 — `outcomeRelabel` comments promise emit/persist consumers, but the field is write-only

The comments say `outcomeRelabel` is read by error emit/audit/transcript sites and specifically point to `(*AgentLoop).emitError` as the consumer. The only non-comment use in the full tree is assignment in `setOutcomeRelabel`; there is no read. The comments conceal an incomplete FR-017a integration rather than documenting behavior.

Evidence:
- `pkg/agent/turn.go:261`
- `pkg/agent/turn.go:307`
- `pkg/agent/turn.go:318`
- No `outcomeRelabel` read outside its declaration/setter.

Required correction: wire the documented consumer(s) and test the persisted/emitted verdict, or remove the field and rewrite comments to match the actual typed retry result flow.

### CA-MAJ-09 — Tests and CI-facing comments falsely label local resolver tests as real-model E2E coverage

`TestE2E_AnyFileAnyModel_UsefulTurn` and `TestE2E_TextOnlyModel_ImageSurvivesAsOffload` only call `resolveMediaRefsWithOffload` against locally constructed catalogs and fake bytes. They do not upload through REST, execute a turn, call a provider, or observe a model response. The comments nevertheless call them E2E, say they run “against that real model id,” and associate them with SC-003. This can cause reviewers to accept unit/integration evidence as end-to-end verification.

Evidence:
- `pkg/agent/loop_media_present_test.go:346`
- `pkg/agent/loop_media_present_test.go:358`
- `pkg/agent/loop_media_present_test.go:363`
- `pkg/agent/loop_media_present_test.go:394`
- The comment itself admits live HTTP is only a “future hook” at `pkg/agent/loop_media_present_test.go:354`.

Required correction: rename/reclassify these as integration tests and add real env-gated upload→turn→provider E2E coverage, or stop citing them as SC-003 evidence.

## Non-blocking stale/comment-quality findings

### CA-MIN-01 — UAT-007 contradicts the locked pixel guard

The UAT plan expects a 6000×4000 (24 MP) PNG to resize and reach the model. FR-013 requires every image above 16 MP to stop at DecodeConfig and route to step 7 without decode. The UAT would fail a conforming implementation.

Evidence: `docs/internal/uat/ADR-051-rev4-uat-test-plan.md:63`, `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1076`.

### CA-MIN-02 — Pixel-bomb test commentary is self-contradictory and preserves discarded reasoning

The comment for `TestEncodeImageToDataURL_DecodeConfigGuard_SlipThroughBomb` repeatedly claims the old division guard missed 10,000,000×2, then calculates that the old guard did reject it, explores several dead ends, and finally repeats the original claim. This is misleading maintenance noise, not useful rationale.

Evidence: `pkg/agent/loop_media_resize_test.go:101` (comment block through the crafted-header fixture).

### CA-MIN-03 — CI evidence is stale relative to the reviewed HEAD

The evidence records successful CI for `56093d75`, but reviewed HEAD is `1ea79f9e` and includes later code commit `9af3d75`. The document calls the run final/full-branch evidence without disclosing that it does not cover HEAD.

Evidence: `docs/internal/uat/ADR-051-rev4-ci-evidence.md:5`; `git log ae9271d0..HEAD`.

### CA-MIN-04 — Puller comments overstate checksum and production-client guarantees

The type-level comment says every successful pull verifies SHA-256, but `verify` treats a missing sidecar as success. `client()` also says production always supplies a shared client, while no production constructor call exists and `NewGHReleasePuller` allocates its own client. These claims should be narrowed to observable behavior.

Evidence: `pkg/providers/capabilities/puller.go:27`, `pkg/providers/capabilities/puller.go:83`, `pkg/providers/capabilities/puller.go:227`, `pkg/providers/capabilities/puller.go:299`.

## Verdict

**FAIL — 0 CRITICAL / 9 MAJOR.** The dominant problem is not missing prose; it is prose and comments presenting unconnected package APIs, absent REST handlers, unrun acceptance gates, and integration tests as a completed production feature. All MAJOR findings must be corrected before the full-diff gate can pass.
