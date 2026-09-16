# Final Full-Diff Review — Code Reviewer

**Branch:** `sendfile-fix`
**Range:** `ae9271d0..1ea79f9e` (43 commits)
**Scope:** 138 changed files, +20,173 / -288 lines
**References read:** ADR-051 Rev 4, workspace-media-library spec (1253 lines, 40 BDDs, 34 FRs), ADR-051 Rev 4 delivery plan v2/v3
**Reviewer:** `pr-review-toolkit:code-reviewer`
**Mode:** read-only; only this report is created

## Verdict

# **BLOCK — 0 CRITICAL / 11 MAJOR / 8 MINOR / 4 OBSERVATION**

The diff delivers substantial, well-isolated, well-tested component code (workspace library, capability catalog, resize pipeline, outcome-based retry, sanitized offload, two-tier resolver). The composition does not deliver the user-visible guarantee the ADR promises. The two user surfaces the spec leads with — workspace media library tab + composer library picker — are wired to REST endpoints that the backend dispatcher 404s on; the workspace-ref presentation path in the agent loop never has a caller-workspace context threaded to it, so uploads return refs that the resolver rejects at the FR-028a guard; the capability catalog is constructed with `puller=nil, store=nil` and no production refresh; the per-model resize budget exists in the catalog but is hard-coded to 7680px at the consumer; the FR-017a relabel is a write-only field; the FR-008 `media.delete` audit event is declared but never emitted.

CI is green (22/22 at run 30029156405 per `ADR-051-rev4-ci-evidence.md`), but the green run is insufficient evidence: the disconnected seams are unit-tested around manually-wired test stubs, so the unit tests pass without exercising the production wiring that doesn't exist.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 11 |
| MINOR | 8 |
| OBSERVATION | 4 |

## Major findings

### MAJOR-01 — Workspace-media REST endpoints are documented + SPA-wired but the backend dispatcher 404s on every nested `/workspaces/{id}/media*` path

**Evidence**

- OpenAPI contract declares `GET /workspaces/{id}/media`, `POST /workspaces/{id}/media/attachments`, `GET /workspaces/{id}/media/{media_id}`, `DELETE /workspaces/{id}/media/{media_id}` at `contracts/openapi.yaml:5446-5568`.
- SPA wires all four operations at `src/lib/api.ts:3287-3319` (Media tab calls `fetchWorkspaceMedia`; composer library picker calls `fetchWorkspaceMedia` + `attachWorkspaceMedia`; Media row delete calls `deleteWorkspaceMedia`).
- The Media tab is added to the workspace tab bar at `src/components/workspaces/WorkspaceTabBar.tsx:32`; clicking it navigates to `/_app/workspaces/$workspaceId/media` and renders `WorkspaceMediaTab` (route file `src/routes/_app/workspaces.$workspaceId.media.tsx`).
- The Go gateway dispatcher `HandleWorkspaces` at `pkg/gateway/rest_workspaces.go:494-545` only branches on `/milestones`, `/delegation`, `/instructions`, and the bare `/workspaces/{id}` resource. Any other subpath 404s at line 530-532: `if strings.Contains(id, "/") { http.NotFound(w, r); return }`.
- The only `pkg/gateway/` symbols added in the diff are the upload-routing branch in `rest.go:8726-8874` and the cascade-delete hook in `rest_workspaces.go:1236-1243`. No `handleWorkspaceMedia*` handler exists.
- Workspace-media image previews are also misrouted: the SPA emits `/api/v1/media/${mediaId}` at `src/lib/omnipus-runtime.ts:283-290` while the existing `HandleMedia` reconstructs a legacy `media://<id>` ref at `pkg/gateway/rest.go:9147-9156` — so the preview URL the SPA renders never resolves to a workspace library file.

**Impact**

- The Media tab loads with a 404 from the backend, then surfaces the SPA's `ApiSchemaError` toast on the second poll. The "list + delete" UAT-009 cannot pass against a live gateway.
- The composer library picker is non-functional. The user opens the picker, sees an empty/error state ("Couldn't load the library."), and cannot attach any library entry to a chat message. The `media://workspace/<ws>/<id>` ref attachment path (FR-022) is unreachable.
- The explicit-delete (FR-008) flow is unreachable.
- This is a direct contract-first violation: Constraint #8 says "wire types are the only legal cross-boundary types," but here wire types describe endpoints that aren't built.

**Why CI missed it**

- `src/components/workspaces/WorkspaceMediaTab.test.tsx` and `src/components/chat/ComposerMediaLibrary.test.tsx` mock the `@/lib/api` module (`vi.mock('@/lib/api', ...)`), so no test crosses the HTTP boundary.
- No `pkg/gateway/` test exercises the `/workspaces/{id}/media*` path; there is no handler to test.

**Required correction**

Implement three (or four) handlers in a new `pkg/gateway/rest_workspace_media.go` or as additions to `rest_workspaces.go`:
- `handleWorkspaceMediaList` — GET → `library.List()` projected to `gen.MediaLibraryEntry`.
- `handleWorkspaceMediaAttach` — POST → `library.Read(id)` to verify the entry exists + is caller-workspace-authorized; thread a `media://workspace/<ws>/<id>` ref into the next outgoing message (or just succeed; the SPA already keeps the ref in its library-attachment store).
- `handleWorkspaceMediaDelete` — DELETE → `library.Delete(id)` + emit `audit.EventMediaDelete` with `bytes_freed`, `media_id`, `filename`, `workspace_id`, `actor` (the authenticated principal).
- (Optionally) `handleWorkspaceMediaGet` — GET → single entry projection.
- Update `HandleWorkspaces` to dispatch `/media*` paths (precedence matching the existing `/delegation` / `/instructions` pattern).

Add an HTTP integration test that drives a real `httptest.Server` through these paths, then add a real live-gateway smoke to UAT-009.

### MAJOR-02 — `FileMediaStore.SetWorkspaceLibraryProvider` has no production caller; the agent loop cannot resolve any `media://workspace/<ws>/<id>` ref

**Evidence**

- `SetWorkspaceLibraryProvider` is defined at `pkg/media/store.go:223-232` with the doc comment: "It is idempotent and safe to call once at gateway boot."
- The full-tree call sites are `pkg/media/resolve_test.go:72` and `pkg/media/resolve_test.go:180` only. Zero production callers.
- The production turn path goes through `resolveMediaRefsWithOffload` at `pkg/agent/loop.go:6306-6317` and `loop.go:6347-6350` (and the two more sites `loop.go:6510-6514` for `pendingMessages`); each call passes `turnMediaStore` from `al.GetMediaStore()`.
- The resolver at `pkg/agent/loop_media.go:115` calls `store.ResolveWithMetaOpts(ref, media.ResolveOpts{})` with empty caller context.
- A `media://workspace/<ws>/<id>` ref hits `pkg/media/store.go::resolveWorkspaceRef` which calls `ValidateCallerWorkspace(workspaceID, opts)` at `pkg/media/resolve.go:112-121`; with nil `CallerWorkspace` this returns `ErrWorkspaceContextRequired` (`pkg/media/resolve.go:43`).
- Even if a caller-workspace context were threaded, the `libraryProvider` field on the media store is `nil` at production boot (the gateway never sets it), so `pkg/media/store.go:280-288` would still return `unknown ref`.
- The "successful" `mediaPresent` test path at `pkg/agent/loop_media_present_test.go:118-137` registers the ref in a `FileMediaStore` (`store.Store(...)` at line 124) — the legacy global-registry path, not a workspace library — so the test never exercises the workspace path.

**Impact**

Every workspace upload — i.e. every new `media://workspace/<ws>/<id>` ref the user produces — turns into an `[attachment unavailable]` marker at the next turn because the resolver rejects it. The "user uploads persist in the workspace library" feature (the entire Layer 0 of the ADR) is broken end-to-end: upload works, persistence works, the ref shape is correct, but no consumer can resolve the ref.

**Why CI missed it**

The unit tests register refs in the legacy global registry, not the workspace library; the resolver tests manually install a `libraryProvider` and a caller context. No test crosses the full path.

**Required correction**

The agent-loop call sites must pass the caller's workspace context. Two options:

1. Thread the caller's workspace ID through `resolveMediaRefs` / `resolveMediaRefsWithOffload` (a new parameter on both, with the four-arg `resolveMediaRefs` keeping the existing signature by passing `nil` and a new six-arg internal form carrying the context). Each call site at `pkg/agent/loop.go:6306-6317, 6347-6350, 6510-6514` passes `ts.opts.WorkspaceID` (or the equivalent "caller's workspace").
2. The gateway must call `mediaStore.SetWorkspaceLibraryProvider(func(wsID string) (media.WorkspaceLibraryResolver, error) { return agentLoop.GetWorkspaceLibrary(wsID), nil })` at boot, mirroring the documented intent.

Both fixes are needed. Add an integration test that uploads through REST, runs `runTurn` with a real workspace context, and asserts the message reaches `ResolveWithMetaOpts` with the right context and the bytes pass `Library.Read`.

### MAJOR-03 — `FileMediaStore.ResolvePathWithCaller` returns a path without sha256 verification, contradicting FR-002

**Evidence**

- `Library.Read` (`pkg/media/library/library.go:455-493`) is the bytes-returning path that recomputes sha256 and rejects mismatches with `ErrIntegrityCheckFailed`. This is the integrity gate.
- `Library.ResolvePathWithCaller` (`pkg/media/library/library.go:519-537`) is a *separate* path-returning helper that "intentionally returns a path (not decoded bytes)" for transport consumers, deliberately skipping the sha256 check.
- The agent loop consumes the path: `pkg/agent/loop_media.go:126-156` `Stat`s the path and `pkg/agent/loop_media.go:509-562` passes the bytes to `image.Decode` and `pkg/agent/loop_media.go:575-579` to `resize.ResizeToFit`.
- The ADR §Layer 0 says "sha256 is verified on read (grill Tampering) — unverified bytes never reach the decode/normalize pipeline." FR-002 ("System MUST compute sha256 at upload time and verify it on every read — unverified bytes never reach the decode/normalize pipeline").
- The transport consumers that the path-resolver was carved out for are the *channel senders* (telegram, discord, slack, etc.) — these do not decode bytes; they hand the file to a third-party API. For those consumers, a sha256 check would add a redundant full-file read.
- But the agent loop's `resolveMediaRefsWithOffload` IS a decode-bound consumer. It uses `ResolveWithMetaOpts` (which calls `resolveWorkspaceRef` → `resolver.ResolvePathWithCaller`) for the *same* ref the channel senders use, but then `image.Decode`s those bytes. The path is consumed without integrity check.

**Impact**

If a workspace library file is tampered with on disk (post-upload corruption, a malicious workspace editor, an fsync gap) the agent loop's `image.Decode` runs on bytes whose sha256 was never re-validated. The ADR's load-bearing Tampering guard is silently bypassed for the production turn path.

**Why CI missed it**

The Turn code path tests in `loop_media_resize_test.go` and `loop_media_present_test.go` use fresh uploads; no test tampers with bytes after upload and observes the path-resolution flow.

**Required correction**

The agent-loop consumer must use the bytes-returning `Library.Read` (with sha256 verification), not the path-returning `ResolvePathWithCaller`. Either:
- Add a `Library.ReadForDecode` (or similar) that performs the sha256 check and returns the bytes for the agent loop's decode path.
- Or call `Library.Read` directly in `resolveMediaRefsWithOffload` for workspace refs (the bytes-returning path always does the integrity check).
- Or unify the two surfaces so the integrity check is the only path and channels/transport consumers pay the cost of the full read (their files are usually <100 MB; the I/O is one-shot; this is the simpler architecture).

Either way, document the architectural choice in the ADR and ensure tampered bytes NEVER reach `image.Decode`.

### MAJOR-04 — The capability catalog is constructed with `puller=nil, store=nil`; FR-025 7-day refresh and `last-known-good` persistence never run

**Evidence**

- `NewAgentLoop` builds the catalog at `pkg/agent/loop.go:638-653` with `capabilities.NewCatalog(capabilities.EmbeddedSeed(), nil, nil, nil)`.
- `SetCapabilityCatalog` is defined at `pkg/agent/media_present.go:139-144` but has zero production callers (only used in the type description).
- `GHReleasePuller` (`pkg/providers/capabilities/puller.go:35-314`) is fully implemented and tested (`puller_test.go`) but never instantiated in production.
- `Catalog.Refresh` (`pkg/providers/capabilities/catalog.go:607-660`) is the entry point that pulls, validates, applies, and stores; with `puller=nil` it returns `nil` without doing anything (line 608-610).
- The 7-day refresh timer described in `pkg/providers/capabilities/catalog.go:13-21` ("On gateway startup and every 7 days") is not present anywhere in `pkg/gateway/` or `pkg/agent/`.
- The `last-known-good` persistence is wired through the `Store` interface (`pkg/providers/capabilities/catalog.go:196-199`); with `store=nil` it is a no-op.
- The seed file at `pkg/providers/capabilities/data/providers_capabilities_seed.json` is loaded (always), so the catalog is *not* empty — but the catalog can never be updated past the embedded seed.

**Impact**

- FR-025's "On gateway startup and every 7 days, the app fetches catalog updates from the Omnipus repo release endpoint" is unenforced.
- SC-009's "Capability catalog pull failure results in 0 gateway boot failures" is unenforceable: there is no pull to fail, so the only way to test SC-009 is to manually construct a puller and pass it to the catalog.
- The freeze-gate re-validation is the seed's only validation path. Any post-freeze provider change (e.g. Google deprecates an image modality for `gemini-1.5-pro`) is invisible until the binary is re-released with a new seed.

**Why CI missed it**

The catalog unit tests construct the catalog with a fixture seed and exercise every method (`catalog_test.go:1069 lines`). They never test the integration that doesn't exist: a production catalog being updated by a real pull.

**Required correction**

Wire the catalog service at gateway boot: build a `capabilities.NewGHReleasePuller(...)` with the elicify-ai/omnipus coordinates, a `Store` implementation backed by `$OMNIPUS_HOME/capabilities/catalog.json` (fileutil.WriteFileAtomic on write; existing file read on boot), and a `time.Ticker` for the 7-day refresh. Call `agentLoop.SetCapabilityCatalog(catalog)` and `catalog.Refresh(ctx)` once on boot. Add a 7-day ticker. Add an integration test that exercises the puller against an `httptest.Server` returning a fixture catalog and asserts the in-memory model map is updated.

### MAJOR-05 — Per-model resize budgets from the catalog are never consumed; the resize path hardcodes 7680px

**Evidence**

- `Catalog.Resolve(model).Budget()` returns a `capabilities.ResizeBudget{LongEdgePx, MaxBytes}` per `pkg/providers/capabilities/catalog.go:134-138`; the seed carries per-model budgets (e.g. `glm-5v-turbo: {long_edge_px: 6000, max_bytes: 5242880}` at `seed.json:60`).
- `encodeImageToDataURL` consumes the budget at `pkg/agent/loop_media.go:576-579`:
  ```go
  result, err := resize.ResizeToFit(decoded, capabilities.ResizeBudget{
      LongEdgePx: 7680,
      MaxBytes:   int64(maxSize),
  })
  ```
  The `LongEdgePx` is hard-coded 7680 and the `MaxBytes` is the legacy `maxUploadFileSize` cap from `agents.defaults.max_media_size`, NOT the catalog's per-model budget. The comment at line 575 acknowledges: "Wave 3 wires per-model budgets from the catalog."
- `media_present.go` has a `getCapabilityCatalog()` accessor but no `getCapabilityBudget()`; the orchestrator is not consulted for the resize budget.
- The `capabilities.Catalog.DefaultResizeBudget()` method (`catalog.go:568-573`) returns the default — also unused.
- The `loopMedia` path's caller `resolveMediaRefsWithOffload` does not take or pass a `*capabilities.Catalog` budget; only the capability gate is wired to the catalog.

**Impact**

- FR-014's "tighter per-provider overrides when known" is unenforced. A 6000×4000 image sent to `glm-5v-turbo` (budget 6000px) is resized to 7680px long edge (the default) and rejected at the provider — a guaranteed 4xx for a format the catalog says the model could have accepted.
- The user-visible behavior does not match the seed's documented per-model budgets.

**Why CI missed it**

The catalog tests cover the budget accessor (`TestResolvedModel_AlwaysCarriesBudget` at `catalog_test.go:622-635`). The resize tests use fixed `ResizeBudget` literals. No test crosses the boundary to assert the orchestrator queries the catalog.

**Required correction**

Thread the catalog into the budget lookup at `pkg/agent/loop_media.go:575-579`. Either:
- Pass the catalog into `resolveMediaRefsWithOffload` and have it return a `ResizeBudget` per call (computed from `catalog.Resolve(model).Budget()` or `catalog.DefaultResizeBudget()`).
- Or call `catalog.Resolve(model).Budget()` at the call site in `loop.go:6306-6317` and pass the resulting `capabilities.ResizeBudget` as a new parameter to the resolver.

Add a regression test: upload a 6000×4000 PNG, send to `glm-5v-turbo`, assert the wire data URL is ≤6000px long edge.

### MAJOR-06 — The "outcome-based retry" reclassifies success-only (FR-017a), but the verdict never reaches the wire

**Evidence**

- The classifier adds `CodeToolArgs` and `CodeSchema` to exclude the new shape classes (FR-018). `media_outcome_retry_test.go:580-826` covers the gate.
- `MediaDowngradeResult{Applied, Trigger, MediaClass}` (`pkg/agent/media_downgrade.go:50-59`) is consumed at `pkg/agent/loop.go:6985-7012` to log the `helperCode` and stamp `outcomeRelabel = CodeMediaUnsupported` on success.
- `turnState.outcomeRelabel` is the FR-017a relabel field; the comment at `pkg/agent/turn.go:315-323` says "the gateway boundary in `(*AgentLoop).emitError` is the next-step consumer."
- The `outcomeRelabel` field is set in `setOutcomeRelabel` (`pkg/agent/turn.go:307-313`) and the only non-test occurrence in the production tree is the assignment at `pkg/agent/loop.go:7008-7010`. There is no read in `emitError` (or anywhere else in production).
- The WS forwarder at `pkg/gateway/websocket.go:3353-3386` re-runs `TranslateLLMError(pe, ...)` on the raw `*ProviderError` for the live error frame; it does not consult `turnState.outcomeRelabel` because that field is in the agent's state, not in the error payload.
- The transcript-error write path (`appendErrorTranscript` or equivalent) similarly re-translates the raw error.

**Impact**

FR-017a's "recorded turn classification is `media_unsupported`, not the raw provider code" is unenforced on the wire. The user sees the raw Gemini/z.ai 400 body, not the relabeled "media_unsupported" message. The classifier-primary and outcome-based paths surface identical UX; the only observable difference is the warn-log.

**Why CI missed it**

The unit tests at `media_outcome_retry_test.go` exercise `TryMediaDowngrade` in isolation; they assert the helper's verdict, not the wire emission. The integration with the live error frame is unverified.

**Required correction**

The relabel must reach the wire. Either:
- The error-relay path (WS forwarder + transcript error write) reads `ts.outcomeRelabel` and re-stamps the LLMError code/message before emission.
- Or the orchestrator calls `TranslateLLMError(pe, outcomeCode)` directly at the success branch, with the relabeled code overriding the original classifier.

Add an integration test: simulate a Gemini 400 with media, observe the outcome-retry succeed, then assert the WS error frame's `payload.code == "media_unsupported"` (not the raw classifier's `unknown`).

### MAJOR-07 — `EventMediaDelete` is declared but never emitted in production; the FR-008 audit trail is unenforced

**Evidence**

- `pkg/audit/events.go:259-267` declares `EventMediaDelete = "media.delete"`. `pkg/audit/audit.go:211` registers it in the allow-list.
- `Library.Delete` returns the deleted `gen.MediaLibraryEntry` (the projection is available at `pkg/media/library/library.go:645`).
- A tree-wide search for `EventMediaDelete` in `pkg/gateway/` returns zero production callers. The only consumers are test stubs at `pkg/media/library/library_test.go:538, 558, 730, 760`.
- The cascade-delete event (`EventMediaCascadeDelete`) IS wired correctly via `WorkspaceDeleteHook` at `pkg/workspace/media_delete.go:56-76`.
- The single-file delete REST handler does not exist (see MAJOR-01), so the path is unreachable.

**Impact**

- FR-008 ("System MUST support explicit user delete of a workspace library file (bytes + manifest entry). The single-file delete SHOULD be logged to the audit subsystem (FR-033), matching cascade-delete — a user deleting an attachment leaves a trail") is unenforced.
- FR-033's SHOULD-level audit requirement is missing in production.
- The audit subsystem records the cascade-delete of a workspace, but the same workspace's individual file deletes leave no trail — a forensic gap.

**Why CI missed it**

The audit event is wired into the cascade-delete path correctly. The single-file delete path doesn't exist as a handler, so there is no place to emit the event. The test stubs in `library_test.go` show the intent; production has no consumer.

**Required correction**

Once MAJOR-01's `handleWorkspaceMediaDelete` is implemented, that handler MUST emit `audit.EventMediaDelete` with the shape documented in FR-033:
```go
{
    action: "media.delete",
    actor: <authenticated principal>,
    workspace_id: <id>,
    media_id: <id>,
    filename: <name>,
    bytes_freed: <size>,
    timestamp: <now>,
}
```
The cascade-delete event's shape is the precedent. Add an integration test that issues a DELETE through the real mux and asserts the audit row lands.

### MAJOR-08 — `OrphanGC` has no production caller; the 30-day deferred GC never runs

**Evidence**

- `Library.OrphanGC` is defined at `pkg/media/library/library.go:722-805`. The ADR §Layer 0 promises: "no storage quota (disc-as-limit)… orphan GC (delete files unreferenced by any session/turn after a configurable age, default 30d, operator-disableable)."
- A full-tree grep for `OrphanGC(` finds only `pkg/media/library/library.go` (definition) and `pkg/media/library/library_test.go` (tests). Zero production callers.
- There is no timer, ticker, or scheduled job in `pkg/gateway/`, `pkg/agent/`, or `cmd/omnipus/` that would invoke `OrphanGC` for any cached workspace library.
- The manifest refcount framework (`pkg/agent/media_present.go:41-259`) maintains per-session bookkeeping with `IncrementRefcount` / `DecrementRefcount` (and the `sessionRefcounter.DecrementRefcount` no-op stub at `pkg/agent/media_present.go:78-84`), but the only thing that consumes the refcount is `OrphanGC`, which is never called.

**Impact**

- The 30-day deferred GC never deletes an orphan. The workspace library grows without bound (modulo the user's explicit delete).
- The "two-mechanism split bounds the flood vector" argument relies on GC to prevent unbounded growth. Without GC, a workspace that sees a brief flurry of uploads becomes permanently bloated.
- The manifest refcount framework is dead weight in the turn path: every workspace ref processed costs a `sync.Map` write/read that the GC never observes.

**Why CI missed it**

`Library.OrphanGC` is heavily unit-tested (manifest refcount, age threshold, operator-disable, batch failure). The tests run in-process; they don't require a production caller.

**Required correction**

Add a lifecycle owner. Options:
- A `pkg/workspace/gc.go` service that, on agent loop construction, walks the cached workspace libraries every `gcInterval` (default 24h) and calls `OrphanGC(config)` with the operator-configured `MaxAge` and `Enabled` flags. Configuration via `agents.defaults.media_orphan_gc` or `gateway.media_orphan_gc`.
- A `time.Ticker` registered with the gateway's lifecycle, with graceful shutdown.

Add an integration test: upload a file, force the refcount to zero + clock forward, call the lifecycle, assert the file is gone.

### MAJOR-09 — The "7-step presentation chain" is a 906-line branch matrix in `pkg/agent/loop_media.go::resolveMediaRefsWithOffload`, not an orchestrator

**Evidence**

- `pkg/agent/media_present.go` claims (line 7-15) to hold the cross-slice integration that "composes the individual Wave 1-2 slices … into the 7-step presentation chain." The file is 259 lines. It defines `workspaceRefcounter`, `sessionRefcounter`, `modelSupportsImage`, `getCapabilityCatalog`, `SetCapabilityCatalog`, `getTurnRefcounter`, `GetWorkspaceLibrary`, `getWorkspaceLibrary`, `decrementSessionMediaRefcounts`. No presenter. No presentation function. No 7-step chain.
- The actual chain lives in `pkg/agent/loop_media.go:78-227::resolveMediaRefsWithOffload`, a 7-argument function with nilable parameters and inline branching per format.
- The capability gate is applied only to images (line 145-157); PDFs still use the old substring allow-list (`pdfCapableModel` at `loop_media.go:250-291`) and direct extraction (`loop_media.go:197-211`).
- The offload sink is invoked at the image branch (line 169-173, 186-192) and at the SVG fallback (line 168-173), but NOT at the PDF branch — a text-only PDF on a text-only model skips step 5 (offload + guidance) and gets only step 6 (text extraction). The spec says step 5 fires whenever "no provider path exists," which includes text-only models for PDFs.
- The text-only model + PDF scenario has no test asserting guidance + extracted text (the spec HOLDOUT 1 scenario). The test at `loop_media_offload_test.go:255-271` exercises `buildOffloadGuidance` in isolation.

**Impact**

- The orchestrator file is a nominal placeholder. The 7-step chain is duplicated and partial in the per-format branch of the resolver. New formats (audio, video) require updating scattered conditionals.
- The PDF presentation path does not run the capability gate; a text-only model gets raw text extraction with no guidance, contradicting the spec's "useful turn" guarantee for PDF.
- The architectural separation (decision function vs. byte encoding helpers) that the spec envisions is absent.

**Why CI missed it**

The unit tests cover each step in isolation (resize, offload, classifier) but no test asserts the 7-step chain's end-to-end behavior for the text-only-PDF or any multi-step flow.

**Required correction**

Extract the decision logic into a single `pkg/agent/media_present.go::present` function with one per-attachment result union:
```go
type Decision int
const (
    SendNative Decision = iota
    SendText
    Offload
    MarkUnavailable
)
func present(ref, mime, model, refBudget, capabilities) Decision
```
- Step 1: capability gate (image AND pdf on text-only model → offload).
- Step 2: normalize+resize (vision + decodable).
- Step 3: native block (PDF on capable model, HEIC on Gemini).
- Step 4: outcome-based retry (driven by the classifier; orthogonal to the decision function).
- Step 5: offload (no path / step 4 exhausted).
- Step 6: text injection (text-extractable + steps 5 fires).
- Step 7: honest marker.

Keep the byte encoding helpers (`encodeImageToDataURL`, `encodePDFToDataURL`, `buildDocumentInjection`) separate. The decision function returns one Decision per attachment; the caller executes it.

### MAJOR-10 — The composer file-attach path passes NO `workspace_id`; user uploads continue to use the legacy session-scoped path

**Evidence**

- `uploadFiles(sessionId, files)` in `src/lib/api.ts:2706-2725` constructs a `FormData` with only `session_id` and the files. No `workspace_id`.
- The upload endpoint at `pkg/gateway/rest.go:8737-8888` accepts `workspace_id` as a query param or form field; when present, routes to the workspace library; when absent, falls back to the legacy session-scoped path.
- The composer attach button uses `omnipusAttachmentAdapter` (`src/lib/attachment-adapter.ts:144-247`) which calls `uploadFiles(sessionId, [file])` with the active session ID but no workspace context.
- The composer knows the active workspace (`useWorkspacesStore((s) => s.activeWorkspaceId)` is the access pattern used by `ComposerMediaLibraryButton` at `src/components/chat/ComposerMediaLibrary.tsx:67`), so the upload could be routed to the workspace library with one extra `formData.append('workspace_id', wsId)` call.
- The slice-H library picker (`ComposerMediaLibrary`) bypasses the file-attach button entirely; it calls `attachWorkspaceMedia(workspaceId, mediaId)` which 404s (see MAJOR-01).

**Impact**

- The "any file, any model → useful turn" two-layer architecture in the ADR is not exercised end-to-end. The user uploads a file, the file is stored in `$OMNIPUS_HOME/uploads/<session>/` (the legacy dir), the ref is a `media://<uuid>` legacy ref, the agent loop's resolveMediaRefs path resolves it via the legacy global registry. The workspace library is never touched by the typical composer flow.
- The Layer 0 invariant ("any file upload succeeds and stores raw bytes + manifest on disk under `workspaces/<ws>/media/`") is not the path user uploads take.
- The workspace library exists but is unused in production.

**Why CI missed it**

The upload test at `pkg/gateway/upload_test.go:435-477` exercises the `workspace_id` routing directly. The SPA integration test at `src/lib/attachment-adapter.test.ts` (if any) mocks the API. No end-to-end SPA-attach test asserts the workspace-library path.

**Required correction**

Either:
- Pass `activeWorkspaceId` through `uploadFiles(sessionId, files, options?: {workspaceId?: string})` and have the composer add it when the active workspace is non-null.
- Or (simpler, but defeats the spec) document the legacy path as the composer flow and reserve the workspace library for the library picker. This requires updating the ADR and the spec.

Pick the first option if the user-facing claim "uploaded files persist across conversations in this workspace" (the Media tab copy at `src/components/workspaces/WorkspaceMediaTab.tsx:124`) is to be true.

### MAJOR-11 — `MediaDowngradeResult.MediaClass` is dropped at the call site; the warn-log always emits the zero value

**Evidence**

- The spec says the helper "reports the affected MediaClass" (FR-019: per-class guards preserved).
- `applyMediaDowngrade` returns a `(MediaDowngradeResult, MediaClass)` tuple at `pkg/agent/media_downgrade.go:137-160`. The caller discards the second return:
  ```go
  // pkg/agent/loop.go:6985
  if downgradeResult := TryMediaDowngrade(ts, callMessages, pe); downgradeResult.Applied {
      ...
      "media_class": string(downgradeResult.MediaClass),  // always ""
  }
  ```
  The tuple is destructured; `MediaClass` is dropped; `downgradeResult.MediaClass` is the zero value of `MediaClass` (i.e. `""`).
- The call site logs `"media_class": string(downgradeResult.MediaClass)`, which is `""` for both classifier-primary and outcome-fallback paths.
- The PDF and image paths compute distinct `MediaClassPDF` / `MediaClassImage` values internally (`media_downgrade.go:137-159`); the runtime observability is lost.

**Impact**

- The warn-log at `pkg/agent/loop.go:6985-7010` records `"media_class": ""` always. Operator-side triage (which media class was downgraded) is impossible from the log.
- The typed `MediaClass` enum and the `MediaClass` field in the result are dead.

**Why CI missed it**

`TestRunTurn_MediaRetry_FiresOncePerTurn` at `runturn_redo_test.go:45-60` tests `downgradeResult.Applied` only; it does not assert `MediaClass`.

**Required correction**

Either:
- Remove `MediaClass` from the result and the call site (the per-class guards are tested in isolation).
- Or make the helper return the `MediaClass` via a populated result; have the call site log it correctly.

Pick the second option if the operator observability is the goal. Add a test asserting `downgradeResult.MediaClass == MediaClassImage` (or `MediaClassPDF`) for the appropriate fixture.

## Minor findings

### MINOR-01 — `eventMediaDelete` audit wiring depends on the absent REST handler

Once MAJOR-01 + MAJOR-07 are addressed, the audit emit MUST chain on the new `handleWorkspaceMediaDelete` (covered by MAJOR-07). Listed separately to keep MAJOR-01 focused on the routing.

### MINOR-02 — UAT-007 expects a 24 MP PNG to resize and reach the model; FR-013 requires routing to the honest marker

`docs/internal/uat/ADR-051-rev4-uat-test-plan.md:63` describes UAT-007 as a 6000×4000 (24 MP) image resizing and reaching the model. FR-013 mandates that images above 16 MP stop at `DecodeConfig` and route to step 7. The UAT-007 fixture will fail against a conforming implementation. Update UAT-007 to either use a sub-16-MP image that triggers the resize ladder, or change the assertion to expect the honest marker.

### MINOR-03 — `TestE2E_*` are integration tests mislabeled as end-to-end

`TestE2E_AnyFileAnyModel_UsefulTurn` (`pkg/agent/loop_media_present_test.go:346-392`) and `TestE2E_TextOnlyModel_ImageSurvivesAsOffload` (line 394+) construct a local `FileMediaStore`, register a ref, and call `resolveMediaRefsWithOffload` directly. No upload, no turn, no provider. The test file's own comment at line 354 admits "A live HTTP provider call is a future hook." Rename these to `TestIntegration_*` and add real env-gated upload→turn→provider E2E coverage, or stop citing them as SC-003 evidence.

### MINOR-04 — `startIndices` byte-count conversion in `Offload`/`Step-5` is `int64(maxSize)` (legacy `max_media_size`), not the catalog's `MaxBytes`

`pkg/agent/loop_media.go:578` derives `MaxBytes: int64(maxSize)` from the per-agent `max_media_size` config. The catalog's per-model `MaxBytes` (e.g. `20 MB` for OpenAI, `10 MB` for Anthropic) is never consulted. Even after MAJOR-05's budget-lookup fix, the MaxBytes side remains wired to the wrong source. Thread the per-model budget through the same seam as LongEdgePx.

### MINOR-05 — The `MediaClass` enum's first value `MediaClassNone` collides with the result's zero value

`pkg/agent/media_downgrade.go:42-48` defines `MediaClassNone = "none"`. The zero value of `MediaClass` is `""`. The test suite can't distinguish "no class" from "unset." Either use `MediaClassNone` as the explicit "no class" sentinel and assert on the typed constant, or drop `MediaClassNone` and use a typed-int.

### MINOR-06 — `actor` passed to `WorkspaceDeleteHook` is empty string

`pkg/gateway/rest_workspaces.go:1242` calls `workspace.WorkspaceDeleteHook(a.homePath, id, "", a.auditor)`. The comment at `pkg/workspace/media_delete.go:25-28` notes "empty string when no principal is resolved (e.g. unauthenticated dev-mode bypass)." The handler is authenticated; thread `a.callerIdentity(r).Username` to populate the actor.

### MINOR-07 — Stale `Wave 1` / `TD-Mx` / `Slice H` review-process narration in production comments

Per CLAUDE.md guidance ("code style — DO NOT ADD ANY COMMENTS unless asked") and the operator's "git history and the ADR own implementation chronology" preference, strip the implementation history from production comments. The `pkg/media/library/library.go:1-39`, `pkg/providers/capabilities/catalog.go:29-53`, `pkg/agent/media_downgrade.go:1-8`, and `src/lib/library-attachment.ts:1-19` doc blocks contain review-process labels that should be reduced to load-bearing invariants.

### MINOR-08 — `.fablize/goals.json` and `.fablize/ledger.jsonl` are workflow-state artifacts in the deliverable

Per MINOR-06 from the code-simplifier review, these are execution bookkeeping, not product implementation. Remove from the deliverable unless the repository explicitly treats them as versioned product data.

## Observations

### OBS-01 — Two stores are justified by the locked two-mechanism decision

The legacy `FileMediaStore` (session-scoped / TTL-deleting) and the persistent `Library` (workspace-scoped / orphan-GC-deleting) coexist for a deliberate reason: ADR-051's two-mechanism split (user uploads persist; agent media stays ephemeral). The defect is the unresolved seam between them, not the coexistence.

### OBS-02 — `UploadFixture` exists for legitimate test reasons; the duplication is the only defect

`UploadFixture` is the test-only path for fixture-source entries (Wave 1 TD-m1). The type-design-analyzer's TD-M10 (114-line near-clone of `Upload`) is a duplication smell, not a design defect.

### OBS-03 — The `WorkspaceTabBar` integration is correct

The `Media` tab at `src/components/workspaces/WorkspaceTabBar.tsx:32` is a deep-linkable sub-route. Once the backend handler exists, the user-visible flow is end-to-end. The SPA wiring is correct; the backend is missing.

### OBS-04 — The capability puller checksum-soft-on-404 is intentional

`pkg/providers/capabilities/puller.go:251-256` treats a 404 sidecar as "no sidecar, release is still trusted." This is the documented GitHub Release signing path. Not a defect.

## Verification

| Check | Result |
|---|---|
| Full diff enumerated | 43 commits; 138 files; +20,173 / -288 |
| Governing ADR/spec/plan | Read in full |
| Production `SetWorkspaceLibraryProvider` callers | 0 (only tests) |
| Production catalog pull/refresh wiring | 0 (puller=nil, store=nil) |
| Production orphan-GC callers | 0 |
| Production per-model budget consumers | 0 |
| Workspace-media REST dispatcher/handlers | 0 |
| `EventMediaDelete` production emitters | 0 |
| `outcomeRelabel` production readers | 0 |
| `MediaDowngradeResult.MediaClass` populated at call site | No (tuple dropped) |
| SHA-256-on-read coverage of `resolveMediaRefs` path | No (path-only resolver) |
| CI evidence | 22/22 jobs green at `56093d75` per `docs/internal/uat/ADR-051-rev4-ci-evidence.md`; CI does not cover the disconnected seams above |
| Local full Go suite | Not run; repository guidance makes CI authoritative in this devpod |
| `git diff --check ae9271d0..HEAD` | Failed on trailing whitespace in added review docs |

## Required before re-review

1. **Wire the workspace-media REST routes** (MAJOR-01): three handlers, dispatcher precedence, audit emit (covers MAJOR-07), live-gateway smoke for the Media tab + composer library picker.
2. **Wire the workspace-library resolver and call-site context** (MAJOR-02): `SetWorkspaceLibraryProvider` at boot, caller-workspace context threaded through `resolveMediaRefs` at `loop.go:6306-6510`.
3. **Restore the sha256-on-read integrity gate** (MAJOR-03): the agent-loop consumer must use a verified-bytes path, not the transport-only `ResolvePathWithCaller`.
4. **Wire the capability catalog service** (MAJOR-04): `GHReleasePuller` + file-backed `Store` + 7-day ticker + boot-time `Refresh`.
5. **Consume the per-model resize budget** (MAJOR-05 + MINOR-04): thread `Catalog.Resolve(model).Budget()` into the resize call.
6. **Land the FR-017a relabel on the wire** (MAJOR-06): wire the WS forwarder and transcript error write to consult `turnState.outcomeRelabel`.
7. **Add the orphan-GC lifecycle owner** (MAJOR-08): one scheduler that calls `OrphanGC` per cached library on a 24h cadence.
8. **Replace the nominal orchestrator with a real presentation function** (MAJOR-09): one `present(ref, model, catalog, budget) Decision` function, per-attachment result union, all 7 steps explicit.
9. **Route the composer file-attach through the workspace library** (MAJOR-10): pass `workspace_id` from `useWorkspacesStore.activeWorkspaceId` through `uploadFiles`.
10. **Populate `MediaDowngradeResult.MediaClass` end-to-end** (MAJOR-11): the typed enum must reach the warn-log; add a test that asserts it.
11. **Fix the actor string on cascade-delete** (MINOR-06): pass the authenticated principal.
12. **Reconcile UAT-007 with FR-013** (MINOR-02): the UAT fixture will fail a conforming implementation.
13. **Rename the mislabeled E2E tests** (MINOR-03) and add real env-gated E2E coverage.
14. **Strip review-process narration from production comments** (MINOR-07); remove workflow-state artifacts (MINOR-08).

Re-run the full 7-reviewer gate only after these integration seams exist; unit tests around manually-wired abstractions are not sufficient evidence. The component code is sound; the composition is incomplete.
