# Grill Review — Spec (Round 1): Workspace Media Library + Capability-Aware Presentation Layer

**Review target:** `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md` (plan-spec)
**Governing ADR:** `ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Reviewer mode:** adversarial (grill-spec, **plan-spec mode**) — assumes the spec, implemented as-is, causes a production incident.
**Read-only on spec:** confirmed — the spec was not modified.
**Date:** 2026-07-22
**Evidence base:** code read via graphify + direct Read (`pkg/media/store.go`, `pkg/agent/{translate_error,media_downgrade,loop_media}.go`, `pkg/workspace/{instructions,lock}.go`, `pkg/gateway/rest_workspaces.go`, `pkg/sandbox/`); test files enumerated on `main`; ADR + ADR grill + evidence matrix read in full.

---

## Executive Summary

The spec is **considerably stronger than the ADR it governs**: it faithfully encodes all seven locked operator decisions and the 7-step chain, resolves every ADR-level finding (C1/C2/M1–M6/m1–m3/O1) into explicit FRs/BDDs, adds a real 30-row format × model-class dataset and an 11-row rejection-body dataset, and pre-names the contract schemas per Constraint #8. The traceability discipline is mostly present and the regression section is detailed. This is a good plan-spec.

**However, the spec as written would cause a production security incident and ship several correctness defects if implemented verbatim.** The headline problems:

1. **The step-5 offload copies a file into `work/` and injects a filesystem path into LLM content, but specifies NO filename sanitization.** The manifest `filename` is explicitly user-controlled; a crafted name is both a **path-traversal / sandbox-escape** on the copy *and* a **prompt-injection** vector via the injected path. FR-020 mandates the copy, SC-006 asserts the path lands under `work/`, but nothing mandates or tests that the copy name is sanitized. This is the same class of gap the ADR grill flagged for step-5, now re-opened one layer down.
2. **The spec self-contradicts on content-addressed dedup.** The Edge Cases section asserts sha256-based content-addressed storage with dedup; the Resolved Ambiguities table asserts "No dedup in v0.1.1." An implementer will pick one and the other silently fails.
3. **Traceability has a hard gap:** a MUST requirement (`FR-007a`, the manifest refcount that drives all orphan GC) is **absent from the Traceability Matrix**, and `FR-008`'s only BDD maps to an unnamed placeholder test. The regression section cites a test file (`pkg/media/registry_test.go`) that **does not exist on `main`**.
4. **`SC-010` ("all existing tests pass without modification") directly conflicts with the spec's own directive to REWRITE `resolveMediaRefs`.** Tests like `TestResolveMediaRefs_SVGRasterizeFails_TextInjection` exercise the exact internal branches being replaced.

None of these are architecture problems — the two-layer design is sound and the ADR's locked decisions survive. They are spec-level defects that must be closed before this spec can govern an implementation.

**Verdict: REVISE.** (Not BLOCK: the architecture is recoverable, the operator scope stands, and every finding is a surgical spec fix. But C1 + the traceability/inconsistency MAJORs must be resolved before hand-off to implementation.)

**Counts:** 1 CRITICAL · 7 MAJOR · 5 MINOR · 2 OBSERVATION.

---

## Findings Table

| ID | Severity | Lens | Section | One-line |
|---|---|---|---|---|
| C1 | CRITICAL | Insecurity (STRIDE: EoP + Info Disclosure) / Incompleteness | FR-020, FR-021, US-8, Edge Cases (line 263) | Step-5 offload copies the file into `work/` and injects a filesystem path into LLM content with NO filename sanitization specified — user-controlled `filename` is a path-traversal/sandbox-escape on the copy AND a prompt-injection vector on the injected path. |
| M1 | MAJOR | Traceability | FR-007a vs Traceability Matrix | `FR-007a` (manifest refcount, MUST) is **absent from the Traceability Matrix** — orphan requirement with no BDD/test mapping. |
| M2 | MAJOR | Traceability / Testability | FR-008 matrix row (line 978) | `FR-008`'s BDD ("Explicit delete removes file") maps to the placeholder "(covered by library delete unit test)" — no named test, violating the "every BDD has a TDD test" rule. |
| M3 | MAJOR | Incorrectness / Regression | Regression table line 901; SC-010 | Regression table cites `pkg/media/registry_test.go` which **does not exist on `main`** (verified). SC-010's guarantee is partly grounded in a phantom file. |
| M4 | MAJOR | Inconsistency | SC-010 (line 963) vs Symbols table (line 35) | SC-010 mandates all `loop_media_test.go` tests pass "without modification," but the spec REWRITES `resolveMediaRefs`. `TestResolveMediaRefs_SVGRasterizeFails_TextInjection` (line 537) and `_OversizeImage_UnavailableMarker` (line 150) exercise the branches being replaced — "without modification" and "rewrite" are in direct tension. |
| M5 | MAJOR | Inconsistency (internal) | Edge Cases (line 267) vs Resolved Ambiguities #4 (line 1018) | Self-contradiction: Edge Cases assert sha256 content-addressed **dedup**; Resolved Ambiguity #4 asserts "**No dedup** in v0.1.1." Implementer picks one; the other silently fails. |
| M6 | MAJOR | Incompleteness / Impact | Symbols table line 38; FR-016 | `encodeImageToDataURL` is framed as "extends — add resize-to-fit" but FR-016 also requires **deleting the D2 passthrough branch** (`isImageFormatUnsupportedByGo` → passthrough at `loop_media.go:478`). The additive framing understates a removal; an implementer can ship resize + leave the passthrough roulette intact. |
| M7 | MAJOR | Incompleteness | FR-007 / FR-007a | The manifest-refcount lifecycle is the crux of orphan GC but its trigger points are undefined: WHEN does refcount increment (upload? message-attach? turn-send?) and decrement (session-delete? turn-end?), given "no transcript scanning"? Relationship to the EXISTING path-based `pathStates.refCount` (`store.go:80`) is unspecified — two refcounts, no reconciliation. |
| M8 | MAJOR | Inconsistency (ADR drift) | ADR step-4 effect vs spec FR-017 | The ADR mandates step-4 "classify the **outcome** (media_unsupported iff retry succeeds)" — outcome relabeling after a successful outcome-based retry. The spec has NO FR/BDD/test for this relabeling, yet asserts (FR-017) the classifier is "retained as outcome-labeller." The role is named but the behavior that defines it is omitted. |
| m1 | MINOR | Testability | SC-001 (line 954) | "100% of uploads across all formats return 200," but the BDDs upload only 2 formats (PNG, AVIF). No Scenario Outline asserts 200 for all 11 matrix formats; the "100%" criterion is not covered by an equivalently-scoped BDD. |
| m2 | MINOR | Ambiguity | FR-015; Edge Cases line 264 | "JPEG ladder continues shrinking (0.75× per step) until fit or a floor" — the **floor** is never defined (minimum quality? minimum dimension?). Undefined termination risks a runaway resize loop or an undefined "fit" state. |
| m3 | MINOR | Incompleteness / Testability | FR-021; BDD "AVIF on Claude offloads" | FR-021 mandates 3 distinct format-aware guidance nouns (image/document/file), but the only guidance-content BDD/test covers the **image** noun. No BDD or test for the document-class or file-class guidance. |
| m4 | MINOR | Ambiguity | US-11; FR-028 | The resolver must distinguish `media://workspace/<id>` from legacy `media://<uuid>` (both share the `media://` prefix). "Workspace library lookup fails (wrong ref shape)" is asserted but the parse/disambiguation rule is never specified. |
| m5 | MINOR | Insecurity (STRIDE: Repudiation) | FR-008 vs Ambiguity #10 | Explicit single-file user delete (FR-008) is outside the audit scope. Ambiguity #10 says "audit delete + cascade-delete," but single-file delete (distinct from workspace cascade) has no audit mandate; FR-033 is MAY. |
| O1 | OBSERVATION | Inconsistency (ADR errata) | ADR line 27 vs spec line 42 | The ADR cites `pkg/media/media.go` for `MediaMeta`, which **does not exist** (verified); it lives in `store.go:40`. The spec silently corrects this. Not a spec defect — flag for an ADR errata so the two don't drift. |
| O2 | OBSERVATION | Inoperability / Concurrency | FR-007a, new `pkg/media/library` | The manifest refcount increment/decrement has no concurrency model specified. The repo uses a 64-shard FNV-hash mutex pool for sessions/memory and `workspace.LockID` for workspace ops; the spec doesn't state whether the library reuses this pattern. Racy refcount = orphan-GC correctness. |

---

## Phase 1 — Structural Integrity (plan-spec checklist)

| # | Check | Result | Evidence |
|---|-------|--------|----------|
| 1 | Every user story has ≥1 acceptance scenario | **PASS** | US-1…US-11 each carry acceptance scenarios (3, 3, 2, 4, 2, 4, 4, 3, 3, 3, 3). |
| 2 | Every acceptance scenario has a BDD | **PASS** | Spot-checked US-1/2/4/8/9 — all ACs map to BDDs (US-8 consolidates AC1+AC2+AC3 into one BDD, acceptable). |
| 3 | Every BDD has a `Traces to:` | **PASS** | All 33 BDD scenarios (incl. the Scenario Outline) carry `Traces to:` lines. |
| 4 | Every BDD has a named TDD test | **FAIL** | "Explicit delete removes file from library" (US-4, AC2) → matrix cell "(covered by library delete unit test)" — a placeholder, not a named test. → M2 |
| 5 | Every FR (MUST/SHOULD) in the traceability matrix | **FAIL** | `FR-007a` (MUST) is defined (line 922) but **absent** from the Traceability Matrix. → M1. (`FR-032`/`FR-033` are MAY → correctly exempt.) |
| 6 | Every BDD in the matrix | **PASS** | Each BDD appears in ≥1 matrix row; "Honest marker for corrupt/empty" routes via FR-023. |
| 7 | Test datasets cover boundary / edge / error | **PASS** | Format×Model matrix (30 rows, incl. pixel-bomb/empty/corrupt); rejection-body dataset (11 rows spanning all exclusion-set members); sha256 dataset (5 rows incl. same-size-swap). |
| 8 | Regression addressed | **PARTIAL FAIL** | Detailed regression table exists, BUT cites `pkg/media/registry_test.go` (non-existent) and asserts `pdfCapableModel` is "tested via loop_media tests" without naming the test. → M3 |
| 9 | SCs measurable | **PARTIAL FAIL** | SC-001 "100%" not backed by an all-formats upload BDD (→ m1); SC-006 "zero `media://` refs injected" is measurable; SC-010 "without modification" conflicts with the rewrite (→ M4). |

**Net Phase 1:** 6 PASS / 2 FAIL / 2 PARTIAL FAIL. The two hard FAILs (M1 orphan FR, M2 unnamed test) and the phantom-file regression cite (M3) must be closed for the traceability discipline the plan-spec format demands.

---

## Phase 2 — Eight Lenses

### 1. Ambiguity

**m2 — Resize ladder termination is undefined.** FR-015 + Edge Cases line 264: "the ladder continues shrinking (0.75× per step) until fit or a floor; if still over, step 5 offload." The **floor** is never named. Is it JPEG quality 40 (the ladder's stated bottom), a minimum pixel dimension, or a byte floor? With no floor, "until fit" can loop indefinitely on a pathological input; with an undefined floor, "still over → step 5" is untestable. *Fix:* name the floor explicitly (e.g., "quality 40 AND long edge ≥ 256px; below that, route to step 5") and add a BDD asserting the offload fires at the floor.

**m4 — Workspace-vs-legacy ref disambiguation rule is unspecified.** US-11/FR-028 assert "workspace library lookup fails (wrong ref shape)" then legacy fallback. Both ref shapes share the `media://` prefix; the spec never states the parse rule (e.g. `strings.HasPrefix(ref, "media://workspace/")`). *Fix:* add one line to FR-028 naming the discriminator.

### 2. Incompleteness

**M7 — Manifest refcount lifecycle is the crux of orphan GC and is undefined.** FR-007 + FR-007a say "refcount == 0 after configurable age" with "increment on session/turn reference, decrement on cleanup — no transcript scanning." But: *when* does "session/turn reference" get recorded if not by scanning the transcript? At upload (refcount starts at 1)? At message-attach time (who records it)? At turn-send time (in the orchestrator)? And the decrement — on session delete only, or also on turn end? The refcount's correctness *is* orphan GC's correctness, and the trigger points are absent. Additionally, the EXISTING store already carries a path-based `pathRefState.refCount` (`store.go:80`, used for dedup/lifecycle of the same file under multiple refs) — the spec introduces a SECOND, manifest-level refcount without reconciling the two. *Fix:* (a) name the increment/decrement call-sites; (b) state how the manifest refcount relates to `pathStates.refCount` (independent? superset? replace?).

**M8 — Outcome-relabeling (ADR-mandated) has no FR/BDD/test.** ADR step-4 effect: "strip media → retry exactly once; **classify the outcome (media_unsupported iff retry succeeds)**." This is the behavior that defines the classifier's new "outcome-labeller" role the spec repeatedly invokes (FR-017 "retained as outcome-labeller"). The spec names the role but encodes neither the relabel-on-success requirement nor a test for it. *Fix:* add an FR ("after a successful outcome-based retry, the turn's error is relabeled `media_unsupported`") + a BDD + a test.

**m3 — Format-aware guidance has a testability hole for 2 of 3 nouns.** FR-021 mandates three guidance strings (image / document / file). The only guidance-content assertion in any BDD or test (`TestPresentation_Step5Offload_CopiesToWorkDir_InjectsPath`) is the **image** noun. The document noun (PDF) and the generic file noun are untested. *Fix:* add a BDD/test asserting the document-class guidance for a PDF offload.

### 3. Inconsistency

**M5 — The spec contradicts itself on dedup.** Edge Cases line 267: "two users upload files with the same filename … **content-addressed storage by sha256 — same content deduplicates**; different content gets unique IDs." Resolved Ambiguity #4 (line 1018): "**No dedup in v0.1.1** — one file per upload; sha256 stored in manifest for integrity only. Simpler lifecycle; dedup deferred." These cannot both be true. *Fix:* delete the dedup claim from the Edge Cases (it is superseded by the ambiguity resolution), or re-open ambiguity #4. As written, an implementer following the Edge Cases ships dedup and one following the Ambiguity table ships none.

**M4 — SC-010 vs the `resolveMediaRefs` rewrite.** SC-010 (line 963): "All existing tests in `svg_raster_test.go`, `loop_media_normalization_test.go`, `loop_media_test.go`, `runturn_redo_test.go`, and `translate_error_test.go` pass **without modification**." Symbols table (line 35): `resolveMediaRefs` is "**modifies** — rewrite to call the presentation orchestrator." Verified on `main`: `TestResolveMediaRefs_SVGRasterizeFails_TextInjection` (line 537), `TestResolveMediaRefs_OversizeImage_UnavailableMarker` (line 150), and `TestResolveMediaRefs_ValidRef_Resolved` (line 102) all call `resolveMediaRefs` directly and assert on its internal branching (SVG text-injection on raster-fail, oversize→marker, happy-path resolve). A rewrite that inserts a presentation orchestrator and changes the SVG-on-text-only path to step-5+6 will change those assertions. "Without modification" is either wrong or the "rewrite" is overstated. *Fix:* either (a) narrow SC-010 to "behavior preserved; tests updated to assert the same observable outcome via the orchestrator," or (b) commit to preserving `resolveMediaRefs`'s observable contract so the tests truly need no change — and say which.

**O1 — ADR cites a non-existent file.** ADR line 27: "`MediaMeta` (`pkg/media/media.go`)." `pkg/media/media.go` does not exist (verified); `MediaMeta` is at `store.go:40`. The spec correctly notes this (line 42). Not a spec defect — but the ADR and spec now disagree on a citation, so flag the ADR for an errata to prevent drift.

### 4. Infeasibility

**No CRITICAL infeasibility.** The ADR's C2 (live-pull has no data source) and M3 (step-5 sandbox path) are resolved: the spec ships a compiled-seed catalog + repo-pull (not per-provider API), and step-5 copies into `work/` (Landlock-allowed, verified `SafeWorkDir` at `instructions.go:97` + sandbox `allowedPaths` rooted there). Pure-Go feasibility holds: `golang.org/x/image v0.44.0` is already in `go.mod` (verified) so `x/image/draw` for the resize and `x/image/{webp,bmp,tiff}` decoders are available; oksvg/rasterx already shipped. No CGo introduced (Hard Constraint #2 respected).

**M6 — The impact assessment understates a removal as an extension.** `encodeImageToDataURL` (`loop_media.go:433`, verified) currently has a live D2 passthrough branch: `isImageFormatUnsupportedByGo(mime)` (line 415) returns true for AVIF/HEIC/HEIF/ICO, and the code returns the raw bytes as `data:image/avif;base64,…` (line 478). FR-016 ("MUST NOT passthrough raw unsupported formats … Rev 3 D2 passthrough is deleted") requires **removing** that branch. But the Symbols table (line 38) describes the function only as "extends — add resize-to-fit before PNG encode," and the Impact Assessment marks it HIGH-risk additive. An implementer reading the symbol framing could add resize *and leave the passthrough roulette intact*, which would silently violate FR-016/SC-003 for AVIF/HEIC/ICO. *Fix:* reframe the symbol entry as "modifies — delete D2 passthrough branch (FR-016) AND add resize-to-fit," and add a regression test asserting no `data:image/avif` (or heic/ico) block is ever emitted to a provider.

### 5. Insecurity (STRIDE)

| STRIDE class | Finding |
|---|---|
| **Spoofing (cross-workspace read)** | The new `media://workspace/<id>` ref embeds the workspace, but the existing `store.ResolveWithMeta` (`store.go:217`) takes **no caller-workspace context** — it resolves any ref by global lookup. The spec lists `TestMediaStore_WorkspaceNamespace_DoesNotLeakAcrossWorkspaces` as a regression test (good), but **no FR mandates** cross-workspace isolation as a MUST, and the resolver signature change needed to carry the caller's workspace is not specified. An agent in `ws-2` resolving `media://workspace/ws-1/<id>` is the canonical cross-tenant read. *Fix:* add an FR ("MUST validate caller-workspace membership before resolving a workspace ref") and specify the resolver's new caller-context parameter. |
| **Tampering** | sha256-on-read is well-covered (FR-002, US-2, 5-row dataset). **PASS.** |
| **Repudiation** | m5 — explicit single-file delete (FR-008) is outside the audit mandate; FR-033 is MAY; Ambiguity #10 covers only cascade-delete. A user deleting an attachment leaves no trail. *Fix:* either promote FR-033 to SHOULD for delete, or explicitly accept single-file delete as unaudited. |
| **Info disclosure** | Persistent plaintext acknowledged and accepted. **NEW, unacknowledged:** C1 — the injected filesystem path is LLM-readable content; a crafted filename is a prompt-injection carrier. |
| **Denial of service** | Disc-as-limit + two-mechanism split (FR-005/FR-006) bounds the agent flood — the ADR grill M2 resolution holds. **No new DoS.** |
| **Elevation of privilege** | **C1** — step-5 copy with an unsanitized, user-controlled `filename` writing under `work/` is a sandbox-escape-adjacent path (`../../etc/passwd` writes outside the workspace; an absolute path escapes entirely). The `safeID` guard (`instructions.go:59`) protects the *workspace ID*, not the *copy filename*. |

**C1 (CRITICAL) — full statement.** FR-020 ("copy the offloaded file into `work/` and inject a filesystem path into content") + FR-021 (inject guidance) + the manifest's user-controlled `filename` field (FR-003) together create two compounding vectors with no mitigation specified:

1. **Path traversal / sandbox escape on the copy.** If the implementer copies using the manifest `filename` (the natural choice — it's the human-readable name), a name like `../../../../etc/passwd` or an absolute path `/tmp/evil` writes outside `workspaces/<ws>/work/`. `SafeWorkDir` validates the workspace *id*, not the per-file copy name. SC-006 only asserts the path is "under `workspaces/<ws>/work/`" — it does not assert the copy *operation* is confined there.
2. **Prompt injection via the injected path.** The filesystem path is injected into LLM content (FR-020) that the model reads. A filename carrying newlines + injected instructions (`foo.txt\n\nIgnore previous instructions and …`) is a prompt-injection payload delivered through a channel the model is explicitly told to `read_file`.

*Fix:* add an FR mandating that the step-5 copy (a) derives a safe name (sha256-prefix or UUID, never the raw `filename`) and (b) is confined by a `filepath.Rel`/`filepath.Join`+`filepath.Clean` containment check against `SafeWorkDir` before the copy, mirroring the `safeID` pattern. Add a BDD/test with a traversal payload filename asserting the copy either sanitizes or rejects. Without this, step-5 ships a sandbox-escape-adjacent + prompt-injection path on day one.

### 6. Inoperability

**M3 — Phantom regression file.** The regression table (line 901) lists `pkg/media/store_test.go`, `pkg/media/registry_test.go` as the tests guarding legacy `media://<uuid>` resolution. `store_test.go` exists (verified); **`registry_test.go` does not exist on `main`** (verified twice). SC-010's "all existing tests pass" is therefore partly grounded in a file that isn't there — an implementer chasing SC-010 will either create it (inventing coverage) or skip it (missing the intent). *Fix:* replace the `registry_test.go` cite with the actual test(s) covering registry persistence (in `store_test.go`) or remove the row.

### 7. Incorrectness

**M1 — Orphan MUST requirement.** `FR-007a` ("System MUST maintain a refcount on each manifest entry … to drive FR-007") is a MUST, defined at line 922, but it appears in **no row** of the Traceability Matrix (verified — the matrix jumps FR-007 → FR-008). It has no BDD and no test. This is the spec's own #1 structural rule ("every FR with a MUST/SHOULD has at least one BDD scenario and one test") violated by the spec itself. *Fix:* add a matrix row for FR-007a with a BDD (e.g., "refcount increments on reference, decrements on cleanup") and a named test.

**M2 — Unnamed test for a MUST.** FR-008 (explicit delete, MUST) → matrix cell "(covered by library delete unit test)." A placeholder, not a test name — the "every BDD has a TDD test" rule requires a concrete name. *Fix:* name the test (e.g., `TestWorkspaceLibrary_ExplicitDelete_RemovesBytesAndManifest`).

### 8. Overcomplexity

The scope is large but honestly broken down (the ADR grill's overcomplexity finding — "compose onto existing" was false — is corrected in the spec's Symbols/Impact tables, which accurately show 4 new packages and CRITICAL/HIGH/MEDIUM risk per modified symbol). No new overcomplexity. The one residual: **M7's two-refcount situation** (existing path-based `pathStates.refCount` + new manifest refcount) is avoidable complexity if the manifest refcount can reuse/extend the existing mechanism — the spec should say whether it does, rather than risk two parallel refcount systems.

---

## Phase 3 — Test-Coverage Gap Analysis

| Claim / BDD area | Testable as specified? | Gap |
|---|---|---|
| Never-fail upload (US-1, SC-001) | ⚠️ Partial — BDDs cover PNG + AVIF only | **m1** — no all-11-formats upload BDD for a "100%" SC. |
| sha256-on-read (US-2) | ✅ Yes — 5-row dataset incl. same-size swap | None. |
| Two-mechanism split (US-3) | ✅ Yes | None. |
| Orphan GC refcount (FR-007/007a) | ❌ No — refcount lifecycle unspecified (M7) + orphan FR (M1) | **M1, M7** — untestable until trigger points named. |
| Capability gate (US-5) | ✅ Yes — deepseek/claude fixtures | None. |
| Normalize + resize (US-6) | ✅ Yes — format outline + pixel-bomb fixture | **m2** — ladder floor undefined. |
| Step-4 classifier + outcome (US-7) | ✅ Yes — 11-row rejection dataset | **M8** — outcome-relabel after success untested. |
| Step-5 offload (US-8) | ❌ **C1** — no sanitization test; traversal/prompt-injection untested | **C1** — add traversal-payload test. |
| Step-5 passthrough deletion (FR-016) | ⚠️ No test asserts passthrough is gone | **M6** — add "no `data:image/avif` emitted" test. |
| Step-6 composition (US-9) | ✅ Yes | None. |
| Capability catalog (US-10) | ✅ Yes — unknown/pull-failure/refresh | None. |
| Backward compat (US-11) | ✅ Yes — legacy + TTL-deleted | None. |
| Regression (SC-010) | ⚠️ "without modification" conflicts with rewrite | **M3, M4** — phantom file + rewrite tension. |

---

## Unasked Questions

1. **Step-5 copy filename — safe-derived or original?** (C1) The manifest `filename` is user-controlled; the copy name is unspecified. This is load-bearing for both traversal and prompt-injection.
2. **Does the manifest refcount reuse the existing `pathStates.refCount`, or is it a second independent counter?** (M7) Two refcounts on related data is a correctness hazard; the spec should declare the relationship.
3. **What is the resize-ladder floor?** (m2) Undefined termination.
4. **Is single-file delete audited?** (m5) Distinct from cascade-delete; currently MAY.
5. **Does the resolver carry caller-workspace context for the Spoofing guard?** (STRIDE) `ResolveWithMeta`'s signature has no caller context today; the cross-workspace isolation test exists but the signature change that makes it enforceable is unspecified.
6. **Is the ADR's "classify the outcome (media_unsupported iff retry succeeds)" in or out?** (M8) The ADR mandates it; the spec omits it. Pick one.

---

## Verdict

**REVISE.**

The architecture is sound, the ADR's locked decisions are faithfully encoded, and the ADR-level findings are all resolved. But the spec cannot govern an implementation until these are closed:

- **C1 (CRITICAL)** — specify and test filename sanitization for the step-5 copy (path-traversal + prompt-injection). Must fix before any code is written against FR-020.
- **M1 + M2 (traceability)** — add FR-007a to the matrix with a BDD/test; name FR-008's test.
- **M3** — replace the phantom `registry_test.go` cite with a real test reference.
- **M4** — reconcile SC-010's "without modification" with the `resolveMediaRefs` rewrite.
- **M5** — resolve the dedup self-contradiction (Edge Cases vs Resolved Ambiguity #4).
- **M6** — reframe `encodeImageToDataURL` to surface the D2-passthrough *deletion* (FR-016), not just the resize *addition*.
- **M7** — name the manifest-refcount increment/decrement call-sites and its relationship to `pathStates.refCount`.
- **M8** — add (or explicitly drop) the outcome-relabel requirement from the ADR.

Once C1 and M1–M8 are resolved, the spec can move to PASS. The MINORs (m1–m5) and OBSERVATIONS (O1–O2) are required polish but do not individually block.
