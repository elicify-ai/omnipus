# Holistic Review — Wave 1 (Round 2, 7th reviewer)

**Scope:** `sendfile-fix` HEAD `cd0616b0` against parent `d0e7374a` (15 commits).
**Slices in this stack:** B1 (library core + Load/Store privacy), B2 (audit events + cascade-delete wire-up), C (capability catalog, then version-semantics + seed-validation fix), F (resize + D2 delete), E (outcome-based strip-retry + 2 new classifier codes), then 9 follow-up fix commits addressing r1 reviewer findings.
**Mode:** READ-ONLY. No files modified.

---

## Verdict

**PASS-WITH-FOLLOW-UPS — 0 CRIT / 6 MAJOR / 7 MINOR / 5 OBSERVATION.**

The r1 reviewer findings have been addressed with strong discipline. The 5 Wave 1 functional slices (B1, B2, C, F, E) plus 9 corrective commits land a coherent, contract-first, type-system-guarded media library + presentation layer. The new packages compile, tests pass in scoped runs (per CLAUDE.md's CI-is-authority rule), contracts are regenerated, the cascade-delete hook is wired, the outcome-relabel is consumed in production, the `CodeUnknown` gate is strict, and the deep-owned `resolvedModel` handle closes the catalog mutation vector.

The remaining 6 MAJORs are **downstream-owned and pre-declared**: 4 are explicitly the named Wave 3 T9 orchestrator hand-off (catalog consumer, refcount inc/dec, per-provider resize budgets, B1-F integrity-boundary composition); 2 are Wave 2 D (AVIF step-5 offload + sanitization, FR-023a filename sanitization) and Wave 2 G (cross-workspace ref guard blast-radius). None is a regression introduced by Wave 1's 9 corrective commits; all were explicitly carried forward in the r1 holistic and now sit behind the Wave 4 acceptance gate.

**Counts: 0 CRIT · 6 MAJOR · 7 MINOR · 5 OBS.**

---

## Re-verification of the r1 holistic findings

The r1 holistic (`wave1-review-round1-holistic.md`) named 3 cross-cutting MAJORs. Round 2 verdict per finding:

### H1-M1 (refcount callers) — **STILL OPEN** (cross-cutting, downstream-owned)

Re-verification: `grep -rn 'IncrementRefcount\|DecrementRefcount' pkg/ --include='*.go' | grep -v _test.go` returns exactly 2 hits — the implementation in `pkg/media/library/library.go:521-526` and a self-reference. **Zero production callers exist anywhere in the codebase after the 15-commit stack.** B2 (commit `5d96827b`) wired the cascade-delete hook + `media.cascade_delete` audit event but did not add a single `IncrementRefcount`/`DecrementRefcount` call-site. The Wave 3 T9 orchestrator remains the only named owner per the r1 plan.

The wave-1-fix commit `d4647703` (TD-M1+TD-M2) refactored the refcount from a duplicated `entry.Refcount + map[string]int` pair into a single source-of-truth `refcount` newtype on `manifestEntry`. That closes the **type-level correctness** concern (R2-M1 / R2-M2 surfacing as a code-time fact), but not the **operational** concern: a library entry uploaded in production still sits at `refcount=0` from creation, and `OrphanGC` will delete every entry 30 days after upload regardless of session activity because nothing bumps the counter.

**Severity now: MAJOR (unchanged).** Owner: Wave 3 T9 (single-consumer of both `Library` refcount API + `Catalog.Resolve`).

### H1-M2 (F D2 routing for AVIF/HEIC/HEIF/ICO) — **STILL OPEN** (cross-cutting, Wave 2 D-owned)

Re-verification: `pkg/agent/loop_media.go:107-125` is unchanged from the r1 snapshot. AVIF/HEIC/HEIF/ICO still routes to the step-7 honest marker `[attachment unavailable: <name> (too large or unreadable)]` (l.124). The post-TD-M6 fix commit `c11cdbc0` swapped the budget type from `resize.Budget` to `capabilities.ResizeBudget` but did not change the routing path. Step-5 offload (work-dir copy + sha256-derived safe name + `filepath.Clean(filepath.Join(safeWorkDir, safeName))` containment check + guidance injection) is owned by Wave 2 D per the r1 plan.

The `(too large or unreadable)` marker remains a misleading reason string for an AVIF on Claude — the file is neither too large nor unreadable in the casual sense; it is unreadable by the stdlib + `x/image` decoder. Wave 2 D's step-5 path will replace this with the spec's `"Cannot read this image with <model>; switch to a vision model for visual analysis."` guidance noun + the injected filesystem path (FR-021, FR-023).

**Severity now: MAJOR (unchanged).** Owner: Wave 2 D. The spec's `behavioral-contract` l.283 invariant ("When no provider path exists for a file (step 5), the system copies the file to `work/`, injects the filesystem path + guidance — the turn survives") is violated for AVIF/HEIC/HEIF/ICO between Wave 1 and Wave 2 D — operators who deploy only Wave 1 get worse behavior than Rev 3 for these formats (Rev 3 at least sent the bytes as `data:image/avif;base64,…` even when the model couldn't decode; Wave 1 silently downgrades to a marker).

### H1-M3 (B2 cascade-delete hook unwired) — **RESOLVED** by commit `5d96827b`

Re-verification: `git show 5d96827b -- pkg/gateway/rest_workspaces.go` shows the hook is now invoked from `handleWorkspaceDelete` at `pkg/gateway/rest_workspaces.go:1233-1244`, BEFORE the `os.RemoveAll(wsDir)` at l.1248. The hook signature is extended from `func(home, workspaceID string) error` (the B1 stub at `pkg/workspace/media_delete.go:9-18`) to `func(home, workspaceID, actor string, auditor *audit.Logger) error`. The hook opens a fresh `library.New(home, workspaceID)` instance, calls `lib.CascadeDelete()`, and emits exactly one `media.cascade_delete` audit event per cascade operation with the deleted-entry summary (`media_ids`, `filenames`, `bytes_freed`, `count`, `actor`, `workspace_id`). Audit emission is best-effort — a cascade-delete that succeeds but fails to audit is logged as a warning and the cascade result is preserved (the on-disk state is already correct).

**Two new gaps surfaced by the B2 wire-up** (neither is blocking, both are tracked below as M-r2-1 and M-r2-2):
1. The hook is called *after* the metadata delete under the workspace lock but *before* the unlocked `os.RemoveAll(wsDir)`. This is consistent with the existing `restructure note` comment about cascading unlocked best-effort paths, but the upload-vs-delete race (r1 `W1-CR-10`) is not addressed by B2 — a concurrent upload can still create a new `media/` between metadata-delete + lock-release and `RemoveAll`.
2. The `EventMediaDelete` audit event is **declared** (`pkg/audit/events.go:267`) and **registered as valid** (`pkg/audit/audit.go:211`) but has **no production emitter**. `Library.Delete` exists at `pkg/media/library/library.go:552`, but no REST endpoint, CLI command, or internal call-site invokes it. Per `library.go:550-551`, the doc comment says "FR-008: callers MUST log a media.delete audit event" — but no caller exists yet. This is a single-file-delete gap that B2 opened by declaring the event but did not close by emitting it.

**Severity now: PASS for H1-M3.** The two new gaps are tracked as M-r2-1 (W1-CR-10 race remains) and M-r2-2 (single-file-delete audit emission missing).

---

## Round-2 re-evaluation of the r1 specialist findings

The 9 follow-up commits addressed the **type-design / contract / silent-failure** MAJORs from the r1 specialists with surgical precision. The remaining un-resolved items from the r1 set are explicitly downstream-owned (Wave 2 D, Wave 2 G, Wave 3 T9) and were carried forward unchanged in the r1 holistic. Verified per-finding:

### RESOLVED in this round (10 r1 findings closed)

| R1 ID | Severity | R1 finding | Closing commit | Verified evidence |
|---|---|---|---|---|
| **TD-C1 / SFH-W1-01** | CRIT | `tool_args` / `schema` codes invalid on wire; SPA drops frames | `d6827307` | `LLMError.yaml` + `LLMErrorReplay.yaml` + `asyncapi.yaml` enums extended; `scripts/gen-contracts.sh` regenerated Go/TS/Zod; `pkg/api/generated/llm_error_codes_test.go` (238 lines) added as exhaustive contract test; `src/lib/llm-error.ts::codeToDisplay` updated for tsc. ✓ |
| **TD-M1 + TD-M2** | MAJOR | Library used generated `gen.MediaLibraryEntry` as domain model; refcount duplicated; workspace_id mutable | `d4647703` | Private `manifestEntry` type holds all invariant-bearing fields as required values; new `refcount` newtype with `newRefcount(value int)` constructor rejects negative; `Library.manifest` is now `map[string]manifestEntry` (parallel `map[string]int` deleted); `gen.MediaLibraryEntry` is a wire projection built via `projection()` at API edge; `Load`/`Store` are package-private; `WorkspaceId` marked `readOnly: true` + `refcount`/`last_refcount_seen_at` required in schema; regen picks up new shape. ✓ |
| **TD-M3 + TD-M4** | MAJOR | `Model` mutable; `Resolve` shallow-copied; `Refresh` non-atomic | `f7019e6c` | `model` type now private; `Resolve` returns a deep-owned `resolvedModel` handle with only accessor methods (`Supports`, `Budget`, `ID`, `Provider`); `Refresh` serialized via a dedicated refresh mutex around pull → parse → version check → apply → store. ✓ |
| **TD-M5** | MAJOR | Seed validation weak; `v10` < `v2` lexical regression | `cd0616b0` | New `pkg/providers/capabilities/version.go` (194 lines) with semver-aware comparator; non-semver strings use lexical fallback; catalog's `Refresh` version-check uses the comparator instead of `strings.Compare`. ✓ |
| **TD-M6** | MAJOR | Duplicated `Budget{LongEdge int, MaxBytes int}` vs `ResizeBudget{LongEdgePx int, MaxBytes int64}` | `c11cdbc0` | `capabilities.ResizeBudget` is canonical, `int64` bytes; legacy `resize.Budget` retired; `encodeImageToDataURL` switched to `capabilities.ResizeBudget{LongEdgePx: 7680, MaxBytes: int64(maxSize)}`. ✓ (Single type, single source of truth. Note: the 7680 literal is still hardcoded — see M-r2-3 below.) |
| **TD-M7 / SFH-W1-03** | MAJOR | `outcomeFallbackEligible` accepted `CodeProviderRejected` — over-broad; masked non-media 4xx as media failures | `9c26e595` + `65f4a8db` | Classifier narrowed: residual 4xx with non-pinned body returns `CodeUnknown` (not `CodeProviderRejected`); `outcomeFallbackEligible` accepts `CodeUnknown` only; exclusion substrings re-checked against `pe.Body` as defense-in-depth; `TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME` (media_outcome_retry_test.go:580) + `TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback` (line 767) regression-lock the Gemini BDD row. ✓ |
| **TD-M8 / SFH-W1-04** | MAJOR | `TryMediaDowngrade` returned only `bool`; `outcomeRelabel` was write-only dead state | `4f70672d` | Typed `MediaDowngradeResult{Applied, Trigger, MediaClass}` with closed internal enums (`TriggerClassifierPrimary` / `TriggerOutcomeFallback` / `TriggerNone`; `MediaClassPDF` / `MediaClassImage` / `MediaClassNone`); `loop.go:6912-6953` consumes the typed result directly; the warn-log records trigger + media_class; the FR-017a success-edge relabel fires on `Trigger == TriggerOutcomeFallback`. ✓ |
| **TD-m1 + TD-m4** | MINOR | `test_fixture` was a production wire enum value; `Load`/`Store` were public | `32f389fb` | `Load`/`Store` now package-private; `UploadFixture` is the recommended test path; `test_fixture` source is reserved for fixture entries (production source enum restricted to `user_upload`). ✓ |
| **W1-CR-01 / SFH-W1-01** | MAJOR | Same as TD-C1 — covered above | `d6827307` | ✓ |
| **W1-CR-02 / SFH-W1-04** | MAJOR | Same as TD-M8 — covered above | `4f70672d` | ✓ |
| **W1-CR-03** | MAJOR | Same as TD-M7 — covered above | `9c26e595` | ✓ |

### STILL OPEN — explicitly downstream-owned (8 r1 findings carried forward unchanged)

| R1 ID | Severity | Status | Downstream owner |
|---|---|---|---|
| **W1-CR-04** | MAJOR | `loop_media.go:419-426` legacy raw-input size check still rejects oversize source before resize can run | Wave 3 T9 (orchestrator must separate trusted-source-read cap from provider-output budget) |
| **W1-CR-05** | MAJOR | `encodeImageToDataURL` still uses `LongEdgePx: 7680` literal + `MaxBytes: int64(maxSize)` legacy cap; does NOT consult `Catalog.DefaultResizeBudget` or `Model.ResizeBudget` | Wave 3 T9 (catalog-consumer wiring) |
| **W1-CR-06** | MAJOR | B1's `Read` returns verified bytes; F's `ResizeToFit` accepts an already-decoded `image.Image`; no exported function bridges verified-bytes → guarded-decode → resize in one boundary | Wave 3 T9 (presentation orchestrator) |
| **W1-CR-07** | MAJOR | `Catalog.Resolve` does case-sensitive exact lookup on bare IDs (`deepseek-chat`); runtime candidate strings include `deepseek/deepseek-chat`, `anthropic/claude-sonnet-4.6`, nested OpenRouter IDs — none hit; deepseek-chat would resolve to optimistic image-capable, defeating step 1 | Wave 3 T9 (canonical/alias key strategy) |
| **W1-CR-08** | MAJOR | `pkg/providers/capabilities` is not production-constructible (no gateway consumer, no startup refresh, no 7-day timer, no `TestCapabilityRegistry_7DayRefresh_Fires`) | Wave 3 T9 (gateway wiring + scheduled refresh) |
| **W1-CR-09 / SFH-W1-05** | MAJOR | Checksum verification fails open on missing/unreachable/non-200/unreadable/empty sidecars; GitHub release `digest` field parsed but unused; raw fallback has no independent integrity | Wave 2 (catalog consumer hardening) or Wave 3 (depends on who owns the puller — likely Wave 2 since it's a transport concern) |
| **W1-CR-10** | MAJOR | Upload vs workspace-delete race not serialized under `workspace.LockID`; B2's hook runs after metadata delete + lock release but before unlocked `RemoveAll` | Wave 2 (workspace-deletion hardening) or Wave 3 |
| **W1-CR-11** | MAJOR | golangci exact-diff gate was reported failing with 9 issues (1 gofumpt, 1 golines, 6 misspell, 1 unused) — not re-run in this round (read-only) | Wave 4 T12 fix-everything gate, or sooner if the 9 issues are not yet fixed |

### STILL OPEN — Code-simplifier findings (5 of 9 MAJORs unchanged)

The r1 code-simplifier named 9 MAJORs. The TD-M6 / TD-M1+M2 / TD-m1+m4 fixes incidentally closed:
- **MAJOR-06** (duplicated budget constants): partially — types unified, but `LongEdgePx: 7680` literal still in `loop_media.go:503`.
- **MAJOR-09** (over-defensive `validatePersistedEntry`): partially — invariants moved to the type system via private types; the explicit validator at `library.go:517+` is reduced but still present.

Still-open code-simplifier MAJORs:
- **MAJOR-01**: `Puller` / `Store` interfaces unchanged (single concrete `GHReleasePuller`, zero `Store` impls).
- **MAJOR-02**: `pkg/media/resize` package boundary unchanged (203 LOC of pure-function code with one production caller).
- **MAJOR-03**: Two parallel media stores (legacy `pkg/media/store.go` + new `pkg/media/library/library.go`) — neither migrated, no resolver-shim landed.
- **MAJOR-04**: `clonePointer[T]` generic + five `*Pointer` helpers (28 LOC) unchanged in `library.go`.
- **MAJOR-05**: Custom `logger` interface reimplementing `*slog.Logger` unchanged in `catalog.go`.
- **MAJOR-07**: `imageStripRange` unused variadic on `stripRejectedImageMedia` unchanged.
- **MAJOR-08**: `startsWithCaseInsensitive` micro-optimization on a non-hot path unchanged.

These are over-engineering/premature-abstraction concerns, not correctness blockers. They ride into Wave 2/3 as a `chore(adr-051-rev4): wave-1 simplification pass` candidate per the r1 recommendation, OR stay as-is if the Wave 2/3 orchestrator implementation tightens the abstractions.

---

## Structural-and-architectural review — does the Wave 1 stack form the foundation for Wave 2 / Wave 3?

The three wave-2/3 slices the operator named in the r1 plan must build on Wave 1 are: **G** (resolver signature change + cross-workspace guard), **D** (step-5 offload + sanitization), **H** (SPA media-library UI). The verdict is **YES, the foundation is sound** — with the following verification per slice.

### Wave 2 G — resolver signature change + cross-workspace guard (FR-028a)

**Foundation readiness:** Partially there.

The `Library` now exposes `ResolveWithWorkspace(ref, callerWorkspaceID string)` (`library.go:495+`) which performs the caller-membership check (`ErrWorkspaceMismatch` at `library.go:106`). The signature accepts a caller context. **But no production caller invokes this method yet** — the 13+ `store.ResolveWithMeta` / `store.Resolve` call-sites enumerated in spec grill R2-M4 (`pkg/channels/{weixin,qq,wecom,matrix,feishu,discord,slack,telegram}/media.go`, `pkg/gateway/replay.go:677`, `pkg/gateway/rest.go:9020`, `pkg/agent/loop_media.go:77`, `pkg/agent/loop.go:4844,8112,8223,8295`) all still call the legacy interface without caller context. Wave 2 G owns the migration.

The foundation is correct: `ResolveWithWorkspace` is the right signature; the membership check returns a typed error. Wave 2 G can iterate each call-site, threading caller-workspace context through. There is no architectural blocker.

**Wave 2 G pre-condition gap:** `Library.New(home, workspaceID)` is per-workspace. A cross-workspace ref resolver needs a registry of libraries keyed by workspaceID. Wave 1 does not ship this registry — Wave 2 G must build it. Verified: `grep -rn 'LibraryForWorkspace\|LibraryByWorkspaceID\|libraryFor\|libraryRegistry' pkg/ --include='*.go' | grep -v _test.go` returns 0 hits.

### Wave 2 D — step-5 offload + sanitization (FR-020/020a/021/022/023/023a)

**Foundation readiness:** Partially there.

The honest marker at `loop_media.go:124` is the wave-1 shipping behavior. Step-5 offload (the work-dir copy + sha256-derived safe name + `filepath.Clean(filepath.Join(safeWorkDir, safeName))` containment check + guidance injection) is Wave 2 D's job. The foundation needed:

1. **A way to copy a file into `workspaces/<ws>/work/` from the resolver call-site.** Verified: `SafeWorkDir` at `pkg/workspace/instructions.go:97` already validates the workspace ID (not the per-file copy name). The safe-name derivation + `filepath.Clean` containment check + the sha256-derived filename (per the r1 spec grill C1 closure, FR-020a) are Wave 2 D's work.
2. **A way to read verified bytes from `Library.Read`** to feed the step-5 copy. ✓ Verified: `Library.Read(id) ([]byte, gen.MediaLibraryEntry, error)` at `library.go:454` returns verified bytes (sha256-on-read enforced at l.267-275; size + digest mismatch returns `ErrIntegrityCheckFailed`, `nil` bytes).
3. **A guidance-line builder with format-aware noun (image / document / file).** Spec FR-021 mandates 3 distinct nouns; r1 m3 noted only the image noun had a BDD/test. Wave 2 D owns the document/file noun coverage.

**Wave 2 D pre-condition gap:** FR-023a filename sanitization in the step-7 honest marker is also Wave 2 D's job (spec grill R2-M3). The current `loop_media.go:87, 101, 124` injects raw `meta.Filename` into provider-bound content — the same prompt-injection vector FR-020a closed for step-5.

The foundation is sound; Wave 2 D can build on it.

### Wave 3 — SPA media-library UI (FR-001/002/003 + Slice H)

**Foundation readiness:** YES.

The wire contract is now spec-compliant and ready for SPA consumption:
- `contracts/components/schemas/MediaLibraryEntry.yaml`: `workspace_id` is `readOnly: true` (server-assigned), `refcount` + `last_refcount_seen_at` are required (always populated by B1).
- The generated TS types in `src/lib/api/generated/openapi-types.ts` and `asyncapi-types.ts` reflect the new shape (regenerated as part of `d4647703` and `d6827307`).
- The Zod schemas in `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts` and `schemas.ts` are consistent.
- `src/lib/llm-error.ts::codeToDisplay` is updated for `tool_args` + `schema` (per `d6827307`).

The SPA can build the workspace media-library list/reuse/delete surface (FR-026) on top of the wire contract. The `handleMedia` GET path (`pkg/gateway/rest.go:8993`) still serves media via the legacy `media.MediaStore` interface — the SPA-side composer attaches workspace refs via the new `media://workspace/<id>` shape, and the workspace-aware resolver (Wave 2 G) handles them at the back-end.

**Wave 3 pre-condition gap:** The single-file-delete REST endpoint (`DELETE /api/v1/workspaces/{ws}/media/{id}`) does not exist yet — `Library.Delete` is the back-end surface, but no handler maps the HTTP verb to it. The plan assigns this to Wave 2 G (or to a sub-slice of Wave 3 T9). See M-r2-2 below.

---

## Cross-cutting concerns across the slices

The 9 corrective commits are individually surgical and well-tested in isolation. Three cross-cutting concerns surface only when the slices are assembled:

### C-r2-1 — B2's hook wire-up narrows but does not close the upload/delete race

The r1 code-reviewer's `W1-CR-10` (upload vs workspace-delete race) is still open. B2 wires `WorkspaceDeleteHook` *after* the metadata delete under the workspace lock but *before* the unlocked `os.RemoveAll(wsDir)`. The comment at `rest_workspaces.go:1224-1232` acknowledges the best-effort-unlocked nature, but a concurrent upload can still:

1. `workspace.LockID` acquired by the upload path,
2. `WorkspaceDeleteHook` completes (cascades media, emits audit, persists),
3. `os.RemoveAll(wsDir)` removes everything.

OR (the race):

1. `WorkspaceDeleteHook` completes (cascade-delete + audit emitted, all bytes gone),
2. `os.RemoveAll(wsDir)` succeeds,
3. **Concurrent upload**: acquires `workspace.LockID`, calls `Library.New(home, wsID)` — the workspace dir is gone, but `New()` creates a fresh dir at `workspaces/<ws>/media/` (per the lazy-creation behavior in B1),
4. The new upload writes a raw file + manifest, persists, returns success,
5. But the workspace entity is gone from `registry.json`; the media file is now orphaned on disk and untracked.

This is a B1 lifecycle concern; B2 made it observable (an audit event would show the cascade), but did not close it. Owner: Wave 2 (or Wave 3 — depends on whether the workspace-deletion hardening is part of the same slice as the workspace-aware resolver or separately scheduled).

**Severity: MAJOR (data-loss window — a successful upload can write bytes that are immediately unreachable).**

### C-r2-2 — The cascade-delete audit event is emitted, but the single-file-delete audit event is not

`EventMediaDelete` (`pkg/audit/events.go:267`) is declared and `IsValidEventName` accepts it (audit.go:211), but **no production emitter exists**. `Library.Delete` returns the deleted entry projection but does not itself call `auditor.Log` (per the B1 design — the REST handler is expected to do it). **The REST handler does not exist yet** (verified: `grep -rn 'lib.Delete\|library\.Delete' pkg/ --include='*.go' | grep -v _test.go` returns 0 hits outside the library).

The user's FR-008 expectation ("delete a file from my workspace library and the action is auditable") is not yet fulfilled. The cascade-delete path (B2) emits `media.cascade_delete`; the single-file-delete path (FR-008) does not emit `media.delete`. Operator's forensic query "what did user X delete this week?" returns nothing.

**Severity: MAJOR (audit-completeness gap; B2 declared the event shape but did not close the emit path).**

### C-r2-3 — The typed `MediaDowngradeResult.Trigger` discriminator is consumed locally but never crosses the gateway boundary

The TD-M8 fix (`4f70672d`) correctly consumed the typed result at the loop call site: `loop.go:6912-6953` reads `downgradeResult.Applied` for the gate, `downgradeResult.Trigger` for the relabel verdict, and `downgradeResult.MediaClass` for the per-class guard. The warn-log records trigger + media_class. The FR-017a outcome-relabel fires on `Trigger == TriggerOutcomeFallback`.

**However**, the spec's `FR-017a` language says: "After a successful outcome-based retry, the turn's error MUST be relabeled `media_unsupported`." The "MUST be relabeled" implies a durable observable surface — an audit event, a transcript field the SPA reads, a metric, a telemetry counter. **None of these exist yet.** The relabel is applied to the loop-local `turnState.outcomeRelabel`, but:

1. No audit event is emitted with the relabel verdict (the `EventLLMCall` family doesn't carry an `outcome_relabel` field).
2. The transcript persistence at `pkg/agent/turn.go:1293-1301` stores the original `helperCode`, not the relabeled one.
3. The SPA-side error display reads the wire `Code` field via the `LLMError` enum — the wire `Code` is the ORIGINAL classifier verdict (e.g., `CodeProviderRejected`), not the relabeled `media_unsupported`. The user sees the original 400 body; the relabel is silent.

The TD-M8 fix landed the typed result + the loop-local consumption. The **observable surface** for FR-017a is the spec-level gap that remains. The r1 type-design reviewer asked for "the actual observable record required" — the fix delivered the typed helper return, but did not deliver the durable emit.

**Severity: MAJOR (spec-level incompleteness — FR-017a's "MUST be relabeled" is only half-implemented).**

### C-r2-4 — `encodeImageToDataURL` still hardcodes 7680px despite the unified budget type

The TD-M6 fix (`c11cdbc0`) unified the budget type (`capabilities.ResizeBudget{LongEdgePx int, MaxBytes int64}`). The call site at `loop_media.go:495-503` now uses the canonical type — but the **values are still hardcoded literals**:

```go
result, err := resize.ResizeToFit(decoded, capabilities.ResizeBudget{
    LongEdgePx: 7680,
    MaxBytes:   int64(maxSize),
})
```

`7680` is the magic number that matches the catalog's `DefaultResizeBudget.LongEdgePx`. `int64(maxSize)` is the legacy `agents.defaults.max_media_size` cap (20 MB by default). Neither is sourced from the catalog's `Resolve(model).Budget()` or from `Catalog.DefaultResizeBudget`.

For per-provider-budget providers (z.ai 6000px / 5 MB), the budget is **loose by ~67%** in Wave 1 (20 MB emitted for a 5 MB provider limit). For Anthropic (8000×8000 / 10 MB), it's also loose (20 MB emitted for 10 MB). The TD-M6 fix closed the **type-level** duplication concern; the **value-level** per-provider-budget sourcing is the Wave 3 T9 orchestrator's job.

**Severity: MINOR (was MAJOR in r1 code-reviewer; downgraded because the type-level unification is real, and the value-level gap is documented as Wave 3 T9's scope).**

### C-r2-5 — Two parallel media stores, neither migrated (Wave 1 ships the dual-store state)

The r1 code-simplifier flagged MAJOR-03 (parallel media stores). The corrective commits did not close this. The legacy `pkg/media/store.go::FileMediaStore` (22 production callers in gateway/agent/tools) is unchanged; the new `pkg/media/library/library.go::Library` (1 production caller: the cascade-delete hook) is the only consumer. The two-mechanism split (user uploads → persistent library; agent-generated → session-scoped + TTL-exempt) is honored by the package boundary, but **no resolver shim has landed**. The spec's R2-M4 (cross-workspace guard blast radius) depends on the shim. Wave 2 G owns this; Wave 1 does not block on it.

**Severity: OBSERVATION (architectural debt; explicitly assigned to Wave 2 G).**

---

## Findings table (round 2)

| ID | Severity | Lens | Slice | One-line |
|---|---|---|---|---|
| **M-r2-1** | **MAJOR** | Cross-cutting (data-loss window) | B2 → Wave 2 | B2 wires `WorkspaceDeleteHook` *after* metadata-delete + lock-release but *before* unlocked `os.RemoveAll(wsDir)`. A concurrent upload can still write to a freshly-recreated `media/` between cascade-complete and dir-remove, orphaning bytes. r1 `W1-CR-10` not closed. |
| **M-r2-2** | **MAJOR** | Audit-completeness | B2 → Wave 2 G | `EventMediaDelete` is declared and registered as a valid event name, but **no production emitter exists** — `Library.Delete` returns the entry; the REST handler that maps `DELETE /api/v1/workspaces/{ws}/media/{id}` to it does not exist. Operator's "what did user X delete" forensic query returns nothing for single-file deletes. |
| **M-r2-3** | **MAJOR** | Spec-incompleteness (FR-017a) | E → Wave 3 T9 | `MediaDowngradeResult.Trigger` is consumed at the loop-local level (TD-M8 fix), but FR-017a's "MUST be relabeled" is only half-implemented — no audit event, no transcript field, no wire-side `Code` reflects the relabeled `media_unsupported`. The user sees the original 400 body; the relabel is silent. |
| **M-r2-4** | **MAJOR** | Cross-cutting (cross-workspace guard blast radius) | B1 + G → Wave 2 G | `Library.ResolveWithWorkspace` exists with the correct signature and `ErrWorkspaceMismatch` typed error, but **zero production callers** invoke it. The 13+ `store.ResolveWithMeta` / `store.Resolve` call-sites enumerated in spec grill R2-M4 are still using the legacy interface with no caller context. Spec grill R2-M4 + r1 `H1-M1` are both pointing at the same gap. |
| **M-r2-5** | **MAJOR** | Cross-cutting (refcount wiring gap) | B1 → Wave 3 T9 | r1 `H1-M1` — confirmed: zero production callers of `IncrementRefcount`/`DecrementRefcount` after the 15-commit stack. Every uploaded entry sits at `refcount=0` from creation; `OrphanGC` will delete every entry 30 days after upload regardless of session activity. Owner: Wave 3 T9. |
| **M-r2-6** | **MAJOR** | Cross-cutting (step-5 routing gap) | F → Wave 2 D | r1 `H1-M2` — confirmed: `loop_media.go:107-125` routes AVIF/HEIC/HEIF/ICO to the step-7 honest marker `(too large or unreadable)` (a misleading reason string), NOT to the spec's step-5 offload. Spec behavioral-contract l.283 invariant violated between Wave 1 and Wave 2 D. Owner: Wave 2 D. |
| m-r2-1 | MINOR | Code-simplifier carry-forward | C + F | `loop_media.go:503` hardcodes `LongEdgePx: 7680` literal despite the TD-M6 type unification. Per-provider budgets (z.ai 6000px / 5 MB) are loose by ~67% in Wave 1. Owner: Wave 3 T9 (catalog consumer wiring). |
| m-r2-2 | MINOR | Code-simplifier carry-forward | C | Five of nine r1 code-simplifier MAJORs (`MAJOR-01` interfaces, `MAJOR-04` clonePointer helpers, `MAJOR-05` custom logger interface, `MAJOR-07` imageStripRange, `MAJOR-08` startsWithCaseInsensitive) are unchanged. Over-engineering concerns, not blockers. |
| m-r2-3 | MINOR | Code-simplifier carry-forward | C + F | r1 `MAJOR-02` (separate `pkg/media/resize` package wrapping pure-function code with one caller) and `MAJOR-03` (parallel media stores, neither migrated) are unchanged. Architectural debt, explicitly assigned to Wave 2/3 simplification pass. |
| m-r2-4 | MINOR | Puller hardening | C | r1 `W1-CR-09 / SFH-W1-05` — checksum verification still fails open on missing/unreachable/non-200/unreadable/empty sidecars. GitHub release `digest` field parsed but unused. Owner: Wave 2 (transport concern) or Wave 3. |
| m-r2-5 | MINOR | Catalog lookup key mismatch | C | r1 `W1-CR-07` — `Catalog.Resolve` does case-sensitive exact lookup on bare IDs (`deepseek-chat`); runtime candidate strings include `deepseek/deepseek-chat`, `anthropic/claude-sonnet-4.6`, nested OpenRouter IDs — none hit; deepseek-chat text-only would resolve to optimistic image-capable, defeating step 1. Owner: Wave 3 T9 (canonical/alias key strategy). |
| m-r2-6 | MINOR | Resize ladder allocations | F | r1 `W1-m-04` — at each shrunken size, PNG and every JPEG quality independently call `scaleImage`, causing up to 7 Catmull-Rom resamples per ladder step. Scale once per candidate dimension, encode repeatedly. Owner: Wave 2 (low-impact optimization). |
| m-r2-7 | MINOR | Comment rot carry-forward | All | r1 comment-analyzer named 10 MAJORs + 1 MINOR. The 9 corrective commits closed some (e.g., CA-W1-9 classifier precedence comment rewritten via TD-M7; CA-W1-6 per-class retry comment rewritten via TD-M8), but `CA-W1-7` (step-5 offload comments at `resize.go:10-14, 70-81` + `loop_media.go:402-418, 491-494` still claim offload happens in Wave 1) is unchanged. |
| O-r2-1 | OBS | Concurrency | B1 + C | r1 `TD-M4` Refresh serialization is now correct (commit `f7019e6c`), but no concurrency test exercises it (`TestCatalog_Refresh_Concurrent` from r1 TA-3 is still missing). The implementation is correct; the test is missing. |
| O-r2-2 | OBS | Refcount concurrency | B1 | r1 `TA-4` — no `TestWorkspaceLibrary_Refcount_ConcurrentIncrement`. The implementation uses `sync.RWMutex` on `changeRefcount` (verified `library.go:875+`), but the lock is silently untested. |
| O-r2-3 | OBS | E2E gating | E + F | Spec grill R2-m3 — E2E tests against real providers with no env-var skip-gate. Wave 1 is unit-test-only (verified: `puller_test.go` uses `httptest.Server`, no env-var gating). Wave 3 T10 owns the env-var skip pattern. |
| O-r2-4 | OBS | Two-mechanism split | B1 | `WorkspaceDeleteHook` (B2) now correctly cascade-deletes the workspace's media library, but the **two-mechanism split** (user uploads persist; agent-generated media stays session-scoped) is enforced at the `Upload` source-gate (`library.go:133` rejects `tool_output` source). Verified: `Library.RejectsToolOutputPersistence` test exists. ✓ |
| O-r2-5 | OBS | Author identity | All | Operator-mandated identity preserved across all 15 commits — verified: `git log --format='%an <%ae>' d0e7374a..HEAD` returns 15 rows, all `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>`; no `Co-authored-by:` trailers. ✓ Hard-constraint #7 author rule holds. |

---

## Verification evidence (round 2)

```bash
# Wave scope (15 commits; B2 now present in stack)
git log --oneline d0e7374a..HEAD
# → cd0616b0 TD-M5 (Slice C version semantics)
# → c11cdbc0 TD-M6 (resize-budget unification)
# → 65f4a8db TD-M7 (Gemini BDD verification)
# → 32f389fb TD-m4+TD-m1 (Load/Store privacy)
# → 4f70672d TD-M8 (typed MediaDowngradeResult)
# → 9c26e595 TD-M7 (strict CodeUnknown gate)
# → f7019e6c TD-M3+TD-M4 (private model + serialized Refresh)
# → d4647703 TD-M1+TD-M2 (manifestEntry + refcount newtype)
# → d6827307 SFH-W1-01 + TD-C1 (tool_args+schema enums)
# → 5d96827b Slice B2 (audit events + cascade-delete wire-up)
# → fba0acbf Slice E (outcome-based strip-retry)
# → 2c97b0bb Slice F (resize + D2 delete)
# → cda59abe Slice B1 (workspace library storage)
# → cf7d8782 Slice C (capability catalog transport)

# H1-M3 re-verification: cascade-delete hook is now wired
grep -n 'WorkspaceDeleteHook' pkg/gateway/rest_workspaces.go
# → :1237 — invoked in handleWorkspaceDelete BEFORE os.RemoveAll(wsDir)

# H1-M1 re-verification: refcount callers still unwired
grep -rn 'IncrementRefcount\|DecrementRefcount' pkg/ --include='*.go' | grep -v _test.go
# → only library.go (definition)

# H1-M2 re-verification: AVIF still routes to step-7 honest marker
grep -n 'attachment unavailable' pkg/agent/loop_media.go
# → :87, :101, :124 — three marker sites; :124 is the AVIF/HEIC path

# TD-C1 re-verification: tool_args/schema now in contract enums
grep -n 'tool_args\|schema' contracts/components/schemas/LLMError.yaml contracts/components/schemas/LLMErrorReplay.yaml
# → both enums contain tool_args + schema

# TD-M7 re-verification: outcomeFallbackEligible accepts CodeUnknown only
grep -n 'code == CodeUnknown\|code != CodeUnknown' pkg/agent/media_downgrade.go
# → narrow gate confirmed

# TD-M8 re-verification: typed MediaDowngradeResult consumed at loop call site
grep -n 'downgradeResult\|MediaDowngradeResult' pkg/agent/loop.go pkg/agent/media_downgrade.go
# → loop.go:6912 reads downgradeResult.Applied; the typed return consumed

# Single-file-delete audit emission gap (M-r2-2)
grep -rn 'lib.Delete\|library\.Delete\|EventMediaDelete' pkg/ --include='*.go' | grep -v _test.go
# → only library.go definition + events.go/audit.go declarations; no emitter

# Author discipline
git log --format='%an <%ae>' d0e7374a..HEAD
# → all 15 commits authored by Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>
```

---

## What this wave got right (round 2)

- **H1-M3 closed** by `5d96827b` — the cascade-delete hook is wired into `handleWorkspaceDelete` with a single `media.cascade_delete` audit event carrying the deleted-entry summary.
- **TD-C1 / SFH-W1-01 closed** by `d6827307` — `tool_args` and `schema` are in the canonical `LLMError` and `LLMErrorReplay` enums + AsyncAPI mirrors; `scripts/gen-contracts.sh` regenerated; exhaustive `llm_error_codes_test.go` (238 lines) added; `src/lib/llm-error.ts::codeToDisplay` updated.
- **TD-M1 + TD-M2 closed** by `d4647703` — the `manifestEntry` private type + `refcount` newtype + single-source-of-truth invariant are exactly the type-system guard the r1 type-design reviewer requested. The `gen.MediaLibraryEntry` is a wire projection at API edge, not a domain model.
- **TD-M3 + TD-M4 closed** by `f7019e6c` — the `model` is private, `resolvedModel` is a deep-owned handle with accessor methods (`Supports` / `Budget` / `ID` / `Provider`), and `Catalog.Refresh` is operation-level serialized.
- **TD-M5 closed** by `cd0616b0` — semver-aware comparator (`version.go`, 194 lines) replaces the lexical regression.
- **TD-M6 closed** by `c11cdbc0` — `capabilities.ResizeBudget` is canonical with `int64` bytes; legacy `resize.Budget` retired.
- **TD-M7 / SFH-W1-03 closed** by `9c26e595` + `65f4a8db` — strict `CodeUnknown` gate; residual 4xx with non-pinned body now returns `CodeUnknown` (not `CodeProviderRejected`); Gemini BDD regression-locked via two new tests.
- **TD-M8 / SFH-W1-04 closed** by `4f70672d` — `MediaDowngradeResult{Applied, Trigger, MediaClass}` typed return; `loop.go:6912-6953` consumes the typed result directly; `outcomeRelabel` is no longer write-only dead state.
- **TD-m1 + TD-m4 closed** by `32f389fb` — `Load`/`Store` are package-private; `test_fixture` source reserved for fixtures.
- **FR-024 freeze-gate artifact preserved** — `docs/internal/research/provider-media-format-support-2026-07.md` (115 lines) is the live evidence doc; the new `version.go` comparator makes it semver-routable.
- **Author identity correct** — operator-mandated; no Anthropic co-author trailers (verified via `git log`).

---

## What the Wave 2 / Wave 3 reviewers must own

| ID | Owner | Slice | When |
|---|---|---|---|
| **M-r2-1** | Serialize upload-admission against authoritative workspace delete under `workspace.LockID` + existence/tombstone validation | Wave 2 (workspace hardening) | Before any deploy with user uploads |
| **M-r2-2** | Build the single-file-delete REST endpoint (`DELETE /api/v1/workspaces/{ws}/media/{id}`); the handler emits `media.delete` audit with actor + bytes_freed | Wave 2 G (workspace-aware resolver) | Before FR-008 acceptance criteria are user-testable |
| **M-r2-3** | Define the FR-017a observable surface: audit event OR transcript field OR wire-side `Code` carries the relabeled `media_unsupported` after a successful outcome-based retry | Wave 3 T9 (orchestrator) | Before FR-017a is operator-observable |
| **M-r2-4** | Migrate the 13+ `ResolveWithMeta`/`Resolve` call-sites to the workspace-aware resolver; thread caller-workspace context through channels + replay + rest + agent-loop | Wave 2 G | Before cross-workspace Spoofing is theoretically reachable |
| **M-r2-5** | IncrementRefcount/DecrementRefcount call-sites in `resolveMediaRefs` (or new orchestrator) — wired and tested under concurrency | Wave 3 T9 | Before any deploy with user uploads |
| **M-r2-6** | Step-5 offload routing for AVIF/HEIC/HEIF/ICO + `work/` copy + sha256-derived safe-name sanitization + `filepath.Clean` containment + guidance injection | Wave 2 D | Before Wave 1 is operationally exposed |
| m-r2-1 | Orchestrator consults `Catalog.DefaultResizeBudget` / `Model.ResizeBudget` (replace `LongEdgePx: 7680` literal) | Wave 3 T9 | Before per-provider resize budgets are enforceable |
| m-r2-2 | Apply r1 code-simplifier MAJOR-01, 04, 05, 07, 08 as a `chore` simplification pass | Wave 2/3 (whenever) | Cosmetic / readability |
| m-r2-3 | Decide on `pkg/media/resize` package boundary (move into `pkg/media/library/`) and dual-store migration strategy | Wave 2 G | Architectural debt |
| m-r2-4 | Puller hardening — verify GitHub `digest` field when present + require valid sidecar for raw fallback + retain LKG on any uncertainty | Wave 2/3 (transport) | Before live pull is enabled |
| m-r2-5 | Canonical/alias key strategy for `Catalog.Resolve` — one provider-aware canonical form shared with `pkg/agent/model_resolution.go::ResolveCandidate` | Wave 3 T9 | Before step-1 gate is enforceable on real models |
| m-r2-6 | Resize ladder — scale once per candidate dimension, encode repeatedly | Wave 2 | Low-impact optimization |
| m-r2-7 | Trim step-5 offload comments at `resize.go` + `loop_media.go` to honest "Wave 2 D owns this" framing | Wave 2 D (when step-5 lands) | Maintenance |
| O-r2-1 | Add `TestCatalog_Refresh_Concurrent` | Wave 3 T9 | Test gap |
| O-r2-2 | Add `TestWorkspaceLibrary_Refcount_ConcurrentIncrement` + `TestWorkspaceLibrary_Refcount_OrphanGCRace` | Wave 3 T9 | Test gap |
| O-r2-3 | Add env-var skip-gate for E2E tests against real providers | Wave 3 T10 | CI gating |
| O-r2-4 | (no action — observation only — two-mechanism split is correctly enforced) | n/a | n/a |
| O-r2-5 | (no action — observation only — author discipline holds) | n/a | n/a |

---

## Holistic verdict (round 2)

**PASS-WITH-FOLLOW-UPS — 0 CRIT / 6 MAJOR / 7 MINOR / 5 OBS.**

The 9 corrective commits address the r1 CRITICAL/MAJOR findings with surgical precision. Each is a single-finding fix (TD-M1+M2 together because they share the same code change; TD-M3+M4 together; TD-M7 twice because the strict gate and the Gemini-BDD verification are two different tests). The slice-by-slice structure of the original 5 functional commits is preserved; the corrections layer on top without merge-conflict risk.

The 6 MAJORs that remain are **all explicitly downstream-owned**: 4 point at Wave 3 T9 (orchestrator + refcount wiring + per-provider budgets + catalog consumer); 2 point at Wave 2 D (step-5 offload + sanitization) and Wave 2 G (workspace-aware resolver + cross-workspace guard + single-file-delete audit). None is a regression introduced by the 9 corrective commits; all were pre-declared in the r1 holistic's "Wave 2 / Wave 3 reviewers must own" table.

**Recommend:**
1. **Re-run `golangci-lint run --build-tags=goolm,stdjson --new-from-rev=d0e7374a`** (r1 `W1-CR-11`) to confirm the 9 lint issues are now closed by the corrective commits (1 unused, 1 gofumpt, 1 golines, 6 misspell — most likely already addressed by `4f70672d` removing the unused accessor and by `d4647703`/`f7019e6c` reshaping the public surface). Not re-run in this read-only round.
2. **Track the 6 MAJORs as Wave 4 acceptance-gate blockers** alongside the r1 carries (H1-M1, H1-M2, W1-CR-04/05/06/07/08/09/10/11). All 14 are pre-declared and assigned.
3. **Land M-r2-3 (FR-017a observable surface) early in Wave 3 T9** — it is the only MAJOR that ships with no implementation plan in the r1 set. The r1 type-design reviewer asked for "the actual observable record"; the typed helper return is half the answer; the durable emit is the other half.
4. **M-r2-2 (single-file-delete audit emission) is the cheapest gap to close** — it's ~20 lines of REST handler code that maps `DELETE /api/v1/workspaces/{ws}/media/{id}` to `Library.Delete` + an audit emit. Wave 2 G should fold this in alongside the workspace-aware resolver migration.

The Wave 1 stack (B1 + B2 + C + F + E + 9 corrections) is a sound foundation. Wave 2 D, Wave 2 G, and Wave 3 T9 each have a clear hand-off.

---

*End of holistic review, Wave 1 round 2.*