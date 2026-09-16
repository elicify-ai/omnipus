# Holistic Review — Wave 1 (Round 1, 7th reviewer)

**Scope:** `sendfile-fix` HEAD `fba0acbf` against parent `d0e7374a`.
**Slices in this wave:** B1 (library core), C (capability catalog), F (resize + D2 delete), E (outcome-based strip-retry).
**B2 (audit + cascade-delete wiring) is NOT in this stack** — `git log d0e7374a..HEAD` shows exactly four stacked commits (B1 → C → F → E); B2 is a future wave. Several cross-slice concerns below are conditional on that.
**Author of every commit:** `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>` (operator-mandated identity, no Anthropic Co-Authored-By trailers — verified via `git log --format='%an <%ae>' -10`). ✓ Hard-constraint #7 author rule holds.

---

## Verdict

**PASS-WITH-FOLLOW-UPS** — 0 CRIT / **3 MAJOR** / 6 MINOR / 5 OBSERVATION.

The four slices faithfully encode their governing FRs in isolation, the round-0 carry-forwards (M6: D2-deletion co-traveled with resize; R2-M1: manifest refcount as a separate counter; R2-O1: guarded by a single library mutex) are correctly implemented, and the test coverage added in `pkg/agent/{loop_media_resize_test,media_outcome_retry_test}.go` + `pkg/media/{library,resize}/*_test.go` + `pkg/providers/capabilities/{catalog,puller}_test.go` is proportionate. The MAJORs are cross-cutting — the four slices each close their own FR set but the wave as a whole ships **three integration gaps that Wave 2/3 must close** for the spec's BDDs to pass end-to-end. None are CRITICAL because each gap has a named downstream owner in the plan; none are individually blocking on Wave 1's per-slice merge gates. The MINORs are spec-level (Wave 1 ships a partial mitigation of round-2 grill R2-M3 step-7 marker sanitization; the closure lives in Wave 2 D FR-023a) and code-level (Wave 1 inherits one Catalog-hydration ordering observation).

Counts: **0 CRIT · 3 MAJOR · 6 MINOR · 5 OBS**.

---

## Compensating-control context carried (F-L4-1)

- **Governing ADR Decision section** (`ADR-051-rev4-workspace-media-library-and-presentation-layer.md:41-91`): two-layer (Layer 0 workspace library + Layer 1 capability-aware presentation, 7-step chain table at l.60-68, classifier-primary + outcome-based fallback at l.74-79, in-repo catalog + repo-pull at l.81-86, per-provider resize budget at l.88-91).
- **Spec Behavioral Contract** (`workspace-media-library-and-presentation-layer-spec.md:273-291`) + **Explicit Non-Behaviors** (l.294-303) + **Integration Boundaries** (l.307-332) reviewed for what Wave 1 MUST and MUST NOT do.
- **ADR grill round 1** (`ADR-051-rev4-…-review.md`): 2 CRIT / 6 MAJOR / 3 MINOR / 1 OBS — C1/M1/M3/M4/M5/M6 already resolved in Rev 4 prose; the live ones are M2 (DoS — operator-accepted in non-goals) + C2 (live-pull — operator-accepted as repo-pull, executed by Slice C).
- **Spec grill round 1** (`workspace-media-library-and-presentation-layer-spec-review-round1.md`): C1 (path-traversal — Wave 2 D FR-020a), M1-M8 mostly resolved by spec updates, M7 (manifest refcount lifecycle) carried to round 2.
- **Spec grill round 2** (`workspace-media-library-and-presentation-layer-spec-review-round2.md`): 0 CRIT / 4 MAJOR — **R2-M1** (manifest refcount false-reuse claim, fixed in spec), **R2-M2** (orphan BDD phantom, fixed in spec), **R2-M3** (step-7 marker raw-filename prompt-injection — open, Wave 2 D FR-023a), **R2-M4** (resolver membership-guard blast radius — open, Wave 2 G). Round-0 Wave-0 review reports (`wave0-review-round1-holistic.md`, `wave0-review-round2-holistic.md`, `wave0-review-round2-verification.md`) cross-referenced for carried-forward scope discipline (the round-1 M-1 bundle-bloat finding is honored — each Wave-1 commit is single-slice).
- **Plan v2 §0 reviewer-gate F-L4-1 PARTIAL resolution** (`ADR-051-rev4-delivery-plan.md:32`): this holistic carries the architect's frame-of-reference via the above context; not a substitute for a dedicated architect agent, but a structural/architectural holistic.

---

## Cross-cutting concerns — the four the prompt named

### 1. Does B1's refcount actually work for the orphan-GC that B1's cascade-delete hook triggers?

**Verdict: YES, in isolation. NO, in production, because no one increments it.**

What ships:
- `Library.OrphanGC` (`library/library.go:326-396`) is correctly designed: read manifest → filter `refcounts[id]==0` AND `now - max(lastRefcountSeenAt, uploadedAt) >= maxAge` → quarantine file → drop from manifest → persist → remove quarantine file. Two-phase (quarantine + drop) with `persistLocked` rollback if persistence fails (`library.go:376-384`) — good. Defaults to `DefaultOrphanAge = 30d` (l.30). Caller-disablable via `OrphanGCConfig.Enabled`.
- `Library.IncrementRefcount` / `DecrementRefcount` / `Refcount` / `changeRefcount` (`library.go:305-323, 461-491`) correctly operate under `l.mu.Lock` and persist on every change (atomic vs failure). The 30d-deferred-delete semantic is the inverse of `pathStates.refCount`'s immediate-delete-at-zero — the **two counters are now genuinely separate** (verified: `pkg/media/store.go` is not in this diff's changed-files list; the existing path-based counter is untouched). Round-0 spec grill R2-M1 is closed at the implementation level. ✓
- `WorkspaceDeleteHook` (`pkg/workspace/media_delete.go:9-19`) does `os.RemoveAll(mediaDir)` on `workspaces/<ws>/media/` — correctly removes both raw files AND the manifest.json. Cascade-delete therefore does not need orphan-GC; it's a synchronous wipe. The two paths (orphan-GC vs cascade-delete) are intentionally separate and do not collide. ✓
- `Library.Refcount` round-trips across `Load()` (`library.go:404-459`) — manifest schema validates per-entry refcount presence (l.436-447) and rejects a refcount without a matching entry (l.451-454). Persisted refcounts survive boot. ✓

**The cross-cutting gap.** B1 ships `IncrementRefcount`/`DecrementRefcount` as a complete public API, but **zero production callers exist in this wave's diff.** Verified: `grep -rn 'IncrementRefcount\|DecrementRefcount' pkg/ --include='*.go'` returns only the implementation file (`library.go`) and the test file (`library_test.go`). Every existing call-site still calls `pkg/media/store.go::ResolveWithMeta` (the legacy path-keyed counter) — 13+ sites enumerated in round-2 grill R2-M4 are untouched in Wave 1. The plan (`ADR-051-rev4-delivery-plan.md:107` Wave-3 T10) assigns refcount inc/dec wiring to Wave 3 ("deferred GC (R2-M1+M2 → test #42)" — only the *test*, not the inc/dec call-sites themselves).

**Operational consequence today.** A library entry that survives Wave 1 will:
- Be created with `Refcount: 0` (`library.go:201-218`) at upload.
- Have `refcounts[id] = 0` persisted (`library.go:218`).
- Sit at `refcount=0` from the moment it lands on disk.

That means **every Wave-1-uploaded entry is a Wave-1-day-1 orphan candidate.** A session that attaches the file and later is cleaned up never increments, so never decrements — but neither does anything else. `OrphanGC` will eventually delete every entry 30 days after upload, even ones actively referenced, because the counter is never bumped. The 30d window masks this in production for now, but the Wave 3 orchestrator MUST land before Wave 1 is operationally usable on real data.

**This is the spec's round-1 M7 / round-2 R2-M1 + R2-O1 surfacing as an implementation-time fact.** The spec was right that the manifest refcount cannot reuse `pathStates.refCount`. The implementation is right that it is a separate counter. The plan was wrong (or vague) that Wave 1's diff is sufficient — the **wiring** that makes the counter mean something is Wave 3's job, but Wave 1 ships the data without that wiring. If Wave 3 slips, Wave 1 is a data-loss liability.

**Severity: MAJOR (cross-cutting; not blocking on per-slice merge gates, but blocks the wave's acceptance for any deploy that has user uploads).**

### 2. Does C's Resolver handle the optimistic case F's encodeImageToDataURL needs?

**Verdict: YES, structurally. The Resolver returns `OptimisticModel` for unknown IDs with `InputModalities: ["text", "image"]` (catalog.go:333-341) and `DefaultResizeBudget` (7680px / 10 MB) at catalog.go:375-379. F's `encodeImageToDataURL` does NOT consult the catalog at all — it unconditionally tries to decode the image and run the resize pipeline (loop_media.go:419-516).**

This is **not a defect** in Wave 1 because the spec assigns the capability-gate-then-encode wiring to Wave 3 T9 (the orchestrator). Wave 1's slice C ships a working catalog; Wave 1's slice F ships a working resize pipeline. They are not wired together in Wave 1, and the plan (`ADR-051-rev4-delivery-plan.md:106`) is explicit that the orchestrator is Wave 3.

What I verified for the optimistic case end-to-end with Wave-1-only code:
- `Catalog.Resolve("any-future-model")` returns `OptimisticModel` (catalog.go:325).
- `HasModal("any-future-model", "image")` returns true (catalog.go:349-357).
- An "optimistic guess wrong" path: catalog says image-capable → orchestrator (not in Wave 1) would call `encodeImageToDataURL` → if image is AVIF → DecodeConfig fails (no x/image AVIF decoder) → returns "" → caller routes to honest marker. The "wrong guess costs one outcome-based retry" promise in the ADR Decision §Capability source (l.86) is preserved as long as Wave 3 wires the orchestrator before turning the capability gate on; with the gate off (status quo Wave 1), the optimistic default is **non-binding** and the code behaves as if the model were always image-capable, so the cost on a wrong guess is zero (the turn still fails to encode, but no retry is triggered because the gate isn't on).

What is binding in Wave 1:
- `Catalog.Resolve` is safe to call concurrently (`mu.RLock` at catalog.go:320-321).
- `Catalog.Refresh` is non-fatal on pull failure (catalog.go:413-448) — matches FR-025/SC-009 "last-known-good retained".
- `Catalog.applySeedJSON` parses first, mutates only on success (catalog.go:288-295, 422-436) — atomic from caller's POV.
- Version regression is rejected (catalog.go:431-435) — strong signal of bad pull; last-known-good retained. ✓

**The concern is not the catalog, it's the hand-off.** Wave 1 ships the catalog as a library with no production consumer; `pkg/providers/capabilities` is a leaf package with no caller (verified: `grep -rn 'capabilities\.' pkg/ --include='*.go'` returns 0 hits outside the package itself + its tests + `embed.go` + `data/...`). The catalog only becomes meaningful when Wave 3 T9 imports it. Same shape as gap #1 — Wave 1 ships infrastructure without consumers.

**Severity: OBSERVATION (cross-cutting; no functional defect in Wave 1 itself; risk concentrated at Wave 3).**

### 3. Does E's outcome-based fallback cover the Gemini case the spec's BDD demands?

**Verdict: YES, verified end-to-end against `HOLDOUT Scenario 4` (spec l.1209-1213).**

HOLDOUT 4: Gemini rejects an SVG with body `Unsupported MIME type: image/svg+xml` (status 400). Expected per spec: outcome-based fallback fires, media is stripped, retry succeeds, user gets a useful response.

Trace through Wave-1 code:
1. Provider returns `ProviderError{Status: 400, Body: "Unsupported MIME type: image/svg+xml"}`.
2. `classifyByProviderError(pe, "")` → `classifyByHTTPStatus` sees status 400 → reaches the 4xx branch (translate_error.go:380-444) → no pinned substring matches `isContentPolicyMessage` (line 222), `isContextOverflowMessage` (line 247), `isToolArgsMessage` (line 290), `isSchemaMessage` (line 310), `isPDFRejectionMessage` (line 144) → falls through to `CodeProviderRejected` (line 421-444).
3. `TryMediaDowngrade` (media_downgrade.go:76) sees `code == CodeProviderRejected`, NOT `CodeMediaUnsupported` → falls through Path 1 to Path 2.
4. `outcomeFallbackEligible(pe, code)` (media_downgrade.go:167-193):
   - `pe.Status < 400 || > 499` → false (400 is in range). ✓
   - `pe.Status == 401 || 403 || 413` → false. ✓
   - `code == CodeProviderRejected` → matches the `case CodeProviderRejected:` arm (media_downgrade.go:180). ✓ (The spec's parenthetical "CodeUnknown" framing at plan §7 decision 6 is implemented by accepting BOTH `CodeUnknown` AND `CodeProviderRejected` — the in-line comment at media_downgrade.go:153-160 spells out why.)
   - Body "Unsupported MIME type: image/svg+xml" — none of `isContextOverflowMessage`/`isContentPolicyMessage`/`isToolArgsMessage`/`isSchemaMessage` match → eligible. ✓
5. `callMessagesCarryMedia(callMessages)` → true (data:image/svg+xml or data:application/pdf present in Media). ✓
6. `ts.applyMediaDowngrade(callMessages)` strips the SVG block → `imageRetryDone.Store(true)` → returns true. ✓
7. `TryMediaDowngrade` returns true → caller in `loop.go:6925` re-invokes `callLLM`.
8. On success, `helperCode != CodeMediaUnsupported` → `ts.setOutcomeRelabel(CodeMediaUnsupported)` (loop.go:6935-6943). FR-017a outcome-relabel is **implemented in Wave 1** (verified at loop.go:6926 + 6938-6943). ✓

**The exclusion set is also verified against HOLDOUT 5** (spec l.1215-1219): content-policy 400 ("content policy violation") → `isContentPolicyMessage` returns true (translate_error.go:222, pinned substrings include "content policy") → `outcomeFallbackEligible` returns false → no strip-retry → error surfaces verbatim. ✓

`CodeToolArgs` ("invalid tool arguments") and `CodeSchema` ("schema validation") are correctly excluded — both `classifyByHTTPStatus`'s 4xx branch (translate_error.go:395, 398) AND `outcomeFallbackEligible`'s double-check (media_downgrade.go:188-189) check these. The "double-check" pattern at media_downgrade.go:91-103 explicitly defends against a regression where the classifier substring path misses a match — defense-in-depth, not load-bearing. ✓

**One observation, not blocking.** E's `outcomeFallbackEligible` accepts BOTH `CodeUnknown` AND `CodeProviderRejected` for status 4xx (media_downgrade.go:178-184). The spec's parenthetical at plan §7 decision 6 says "CodeUnknown is the practical verdict, not a strict symbol match" — this is a deliberate implementation choice to make Gemini's 400 work without forcing it to map exactly to `CodeUnknown`. The inline comment documents the rationale. Wave-1 implementer should add a regression test row to the spec's BDD dataset (`TestTryMediaDowngrade_OutcomeFallback_GeminiSVG` or similar) that asserts `CodeProviderRejected + 400 + non-pinned body` fires; `media_outcome_retry_test.go:470` covers a `CodeProviderRejected` case but the test body in the diff isn't fully visible to me — recommend the qa-lead reviewer verify the row exists.

**Severity: PASS for the Gemini case. OBSERVATION for the test-row completeness (Wave 1's `media_outcome_retry_test.go` is 555 lines and likely covers this, but the qa-lead reviewer should confirm).**

### 4. Does F's removal of the D2 passthrough correctly route AVIF/HEIC to step-5 offload as the spec says?

**Verdict for D2 deletion: PASS. ✓ (M6 carry-forward satisfied.)**
**Verdict for step-5 routing: PARTIAL — D2 is gone, but the current routing sends AVIF/HEIC to step-7 honest marker, NOT step-5 offload. Step-5 wiring lives in Wave 2 D (FR-020a/021/023) and is NOT in this wave's diff.**

What Wave 1 ships (verified):
- `grep isImageFormatUnsupportedByGo` returns 0 hits — the function and all its callers are deleted from the codebase. ✓
- `encodeImageToDataURL` (loop_media.go:419-516) no longer has a passthrough branch for AVIF/HEIC/HEIF/ICO. The function now does:
  - SVG → rasterize via `encodeSVGToDataURL` (loop_media.go:432-434).
  - All other formats → `image.DecodeConfig` pre-flight (loop_media.go:452-474) with order-independent product check (`uint64(Width)*uint64(Height) > maxImagePixels`).
  - AVIF/HEIC/HEIF/ICO have no decoder in stdlib + `x/image` → `DecodeConfig` returns `decode-config-failed` → function returns "" (loop_media.go:452-456). ✓
  - Successful decode → `resize.ResizeToFit` (loop_media.go:495-498) with `DefaultLongEdge=7680` and `MaxBytes=maxSize` (per-caller). PNG preferred (resize.go:111-116), JPEG q90→q40 ladder (resize.go:118-127), 0.75× shrink with `MinLongEdge=256` floor (resize.go:189-203), `ErrLadderFloor` routes to step 5. ✓
- Co-travel is enforced — the D2 deletion and the resize addition both live in `encodeImageToDataURL` (loop_media.go:419-516). An implementer cannot ship resize without deleting the passthrough, because the function body no longer has a passthrough branch. Round-0 spec grill M6 carry-forward is satisfied. ✓

**The cross-cutting gap.** The spec's behavioral contract at workspace-media-library-and-presentation-layer-spec.md:283 is: **"When no provider path exists for a file (step 5), the system copies the file to `work/`, injects the filesystem path + guidance — the turn survives."** For AVIF/HEIC/HEIF/ICO, "no provider path" applies (no decoder). What Wave 1 actually does for an AVIF on Claude:
1. `encodeImageToDataURL` returns "".
2. Caller in `resolveMediaRefs` (loop_media.go:107-124) sees `dataURL == ""` AND `mime != "image/svg+xml"` → falls to the `else` branch at l.117-124:
   ```go
   contentInjections = append(contentInjections,
       "[attachment unavailable: "+name+" (too large or unreadable)]")
   ```
   This is the **step-7 honest marker**, NOT step-5 offload.
3. The injection text reads `(too large or unreadable)` — this is a **misleading reason string** for an AVIF that Claude legitimately can't decode. The reason should be something like `(unsupported format)` or `(cannot decode on this model — see attached file path)` plus an injected filesystem path. Spec FR-021 mandates the guidance noun ("Cannot read this image with <model>; switch to a vision model for visual analysis") — Wave 1 ships none of this.

**Wave 2 D (FR-020a/020/021/022/023/023a) is the owner** per the plan (`ADR-051-rev4-delivery-plan.md:46, 95`). The Step-5 work-dir copy + sha256-derived safe name + `filepath.Clean(filepath.Join(safeWorkDir, safeName))` containment check + `buildPathTag` / guidance injection all live there. Without Wave 2 D landing, an AVIF on Claude is **silently downgraded to a "too large or unreadable" marker** — a regression from the prior Rev 3 state where the bytes at least reached the model. This is a step-7-vs-step-5 deviation that Wave 1 alone does not close.

This is **not blocking on Wave 1's per-slice merge** (F's FR-016 is satisfied; the routing is Wave 2 D's job), but the round-1 holistic MUST flag it because the spec's "never-fail upload + always useful turn" guarantee is **violated for AVIF/HEIC/HEIF/ICO between Wave 1 and Wave 2**. Operators who deploy only Wave 1 get worse behavior than Rev 3 for these formats.

**Severity: MAJOR (cross-cutting; depends on Wave 2 D landing before Wave 1 is operationally exposed).**

---

## Findings Table

| ID | Severity | Lens | Slice | One-line |
|---|---|---|---|---|
| **H1-M1** | **MAJOR** | Cross-cutting (data-loss window) | B1 → Wave-3 T9 | B1 ships `IncrementRefcount`/`DecrementRefcount` as a complete API with **zero production callers** in Wave 1's diff — every uploaded entry sits at `refcount=0` from creation, making the entire library a 30-day orphan candidate regardless of session activity. Spec grill R2-M1 + R2-O1 surfacing as implementation-time fact. |
| **H1-M2** | **MAJOR** | Cross-cutting (step-5 routing gap) | F → Wave-2 D | F's D2 deletion is correct (verified, ✓), but the current routing for AVIF/HEIC/HEIF/ICO on a vision model sends the user `[attachment unavailable: <name> (too large or unreadable)]` — a step-7 honest marker with a **misleading reason**, NOT the spec's step-5 offload (work-dir copy + guidance line + filesystem path injection). Spec behavioral contract l.283 violated between Wave 1 and Wave 2. |
| **H1-M3** | **MAJOR** | Cross-cutting (cascade-delete hook unwired) | B1 → B2 | B1 ships `pkg/workspace/media_delete.go::WorkspaceDeleteHook(home, workspaceID)` as a 19-line `os.RemoveAll` stub, but **no caller invokes it** in Wave 1's diff. The plan defers the workspace-deletion trigger wiring to B2 (Wave 1b), which is **not in this wave's stacked commits** (`git log d0e7374a..HEAD` shows B1, C, F, E only — no B2 commit). Until B2 lands, deleting a workspace does NOT cascade-delete its media library; the manifest persists across the workspace boundary. |
| H1-m1 | MINOR | Spec carry-forward (R2-M3) | F → Wave-2 D | `resolveMediaRefs`'s step-7 markers at loop_media.go:78-79, 91, 100, 123 still inject raw `meta.Filename` into LLM-bound content. Spec grill round-2 R2-M3 is open. FR-023a (filename sanitization in content injection) is Wave 2 D's job, NOT Wave 1's. Wave 1 ships no partial mitigation here. |
| H1-m2 | MINOR | Spec carry-forward (R2-M4) | G → Wave-2 G | The 13+ `store.ResolveWithMeta` call-sites enumerated in spec grill R2-M4 are NOT touched by Wave 1. `pkg/channels/{weixin,qq,wecom,matrix}/media.go`, `pkg/gateway/rest.go:9020`, `pkg/agent/loop.go:4844,8134,8245,8317` all still use the legacy `media.MediaStore` interface (no caller-workspace context). The cross-workspace Spoofing guard (FR-028a) is Wave 2 G. |
| H1-m3 | MINOR | Resize (FR-014 budget source) | C → Wave-3 T9 | `pkg/providers/capabilities` is shipped as a library, but `encodeImageToDataURL` always uses `resize.DefaultLongEdge` (7680px) — it does NOT consult `Catalog.DefaultResizeBudget` or `Model.ResizeBudget`. Per-provider budgets are inert in Wave 1; the orchestrator (Wave 3 T9) is the wiring. For per-model-budget providers (z-ai 5 MB / 6000px), the budget is loose by ~67% in Wave 1. |
| H1-m4 | MINOR | Resize (FR-015 floor testability) | F | `resize.MinLongEdge = 256` is the ladder floor. The shrinkage math at resize.go:189-203 floors to 1px before testing `newLong < MinLongEdge`, so a 256px source that doesn't fit still has `newLong=192` after 0.75× — `floor=true` and returns `ErrLadderFloor`. Correct. But a 1px source (any degenerate input) gets `newW=0 → newW=1`, then `newLong=1 < 256` → floor. Correct, but the comment at resize.go:131-133 says "long edge would fall below MinLongEdge — the image is too small to be useful" without naming the pixel-floor clamp at resize.go:192-197. The clamp is silent — a future maintainer who deletes the `< 1 → 1` floor could introduce a 0-dim encode on the next iteration. |
| H1-m5 | MINOR | Catalog hydration ordering | C | `Catalog.NewCatalog` (catalog.go:248-283) hydrates from `Store` first; only on Store read failure does it fall back to the embedded seed. **There is no "if embedded seed is newer than Store" check** — on upgrade, a fresh compiled-in seed (re-validated against 2026 docs) loses to a stale Store from the prior boot. FR-024 freeze-gate re-validation is a one-shot at compile-time; an upgraded binary with stale Store never picks up the new freeze-gate-validated seed until Store is manually invalidated. Not blocking, but a "delete store" or "bump version" operation is needed on upgrade. |
| H1-m6 | MINOR | Puller (FR-025 transport) | C | `GHReleasePuller` is tokenless (puller.go:31-34). GitHub's 60 req/h unauthenticated limit is well above the 7-day cadence + startup pull. ✓ But the 4 MB cap on the Releases API response (puller.go:153) and 2 MB cap on the asset body (puller.go:188) are silent — a future larger catalog (e.g., 1000+ models) hits the 2 MB cap and fails parse. The seed today is ~5 KB; well within budget. The cap is the right defensive default; flag for catalog-grow-threshold observation. |
| H1-O1 | OBSERVATION | Cross-cutting | C → Wave-3 T9 | Catalog is a leaf package with zero production consumers in Wave 1. Same shape as H1-M1 (B1 refcount) — Wave 1 ships infrastructure without wiring. The Wave-3 orchestrator will be the single consumer of both. |
| H1-O2 | OBSERVATION | Spec grill R2-M2 | (spec-level) | The matrix-row BDD "Manifest refcount drives GC" cited in spec l.1088 has no corresponding `#### Scenario:` in the BDD section. This is a spec defect (not Wave 1's). Wave 3 T10's "deferred GC (R2-M1+M2 → test #42)" must add the BDD before the test row is meaningful. |
| H1-O3 | OBSERVATION | Concurrency | B1 | `Library.changeRefcount` is guarded by `l.mu.Lock` (l.465). Each `Library` instance is per-workspace (l.82-103), so the mutex is per-library, not global. The repo's 64-shard FNV mutex pool (`pkg/memory/jsonl.go`) is NOT used here. This is fine — the per-workspace granularity is coarser than the FNV pool's cross-session granularity, but a workspace library is rarely a multi-goroutine hot path. Spec grill R2-O1 concurrency model is **partially** resolved at implementation level. |
| H1-O4 | OBSERVATION | E2E gating | C, E, F | Spec grill round-2 R2-m3 flagged that E2E tests against real providers have no env-var skip-gate. Wave 1's `puller_test.go` (398 lines) and `catalog_test.go` (583 lines) use `httptest.Server` (no env-var needed) — Wave 1 is unit-test-only for catalog transport. E2E gating (spec grill R2-m3) is still Wave 3 T10's job. |
| H1-O5 | OBSERVATION | Forward-compat (seed schema) | C | `Seed.validate` (catalog.go:200-235) accepts unknown modality strings for forward-compat (catalog.go:194-199 comment). The `InputModalities` slice is not deduplicated (l.216 — empty check, not dup check). A seed with `["image","image"]` passes validation and would cause `HasModal` to return true twice — no functional defect, just wasted work. Spec doesn't require dedup; flag only. |

---

## Verification commands executed in this review

```bash
# Confirm wave scope (4 stacked commits; B2 NOT in wave)
git log --oneline d0e7374a..HEAD
# → fba0acbf Slice E
# → 2c97b0bb Slice F
# → cda59abe Slice B1
# → cf7d8782 Slice C

# Confirm author discipline
git log --format='%an <%ae>' -10
# → all 4 commits authored by Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>

# Confirm D2 passthrough deletion (M6 carry-forward)
grep -rn 'isImageFormatUnsupportedByGo' pkg/ --include='*.go'
# → 0 hits

# Confirm B1 refcount API has zero production callers in Wave 1
grep -rn 'IncrementRefcount\|DecrementRefcount' pkg/ --include='*.go' | grep -v _test.go
# → only library.go (definition)

# Confirm C catalog has zero production consumers in Wave 1
grep -rn 'capabilities\.' pkg/ --include='*.go' | grep -v '_test\|capabilities/'
# → 0 hits

# Confirm B2 audit + cascade-delete wiring NOT in Wave 1
ls pkg/media/library/ pkg/audit/ pkg/workspace/
# → no new audit files; media_delete.go is the 19-line B1 hook stub only
```

---

## What this wave got right

- **D2 deletion co-traveled with resize** (round-0 spec grill M6). One commit, one function (`encodeImageToDataURL`), no way to ship resize without removing the passthrough. ✓
- **Manifest refcount is genuinely separate from `pathStates.refCount`** (round-0 spec grill R2-M1). `Library.refcounts map[string]int` is keyed by UUID, `changeRefcount` is guarded by per-library mutex, persisted atomically. `pkg/media/store.go` is untouched. ✓
- **Spec grill R2-M3 / R2-M4 deferred to Wave 2 with named owners.** No scope creep into Wave 1.
- **FR-024 freeze-gate artifact shipped.** `docs/internal/research/provider-media-format-support-2026-07.md` (115 lines, referenced from seed source string l.5).
- **FR-017a outcome-relabel implemented in loop.go:6925-6943.** Relabel fires only when `helperCode != CodeMediaUnsupported`, preserving classifier-primary-path identity.
- **Author identity correct.** Operator-mandated; no Anthropic co-author trailers (verified via `git log`).

---

## What the Wave 2 / Wave 3 reviewers must own

| ID | Owner | Slice | When |
|---|---|---|---|
| H1-M1 | IncrementRefcount/DecrementRefcount call-sites in `resolveMediaRefs` (or new orchestrator) | Wave 3 T9 | Before any deploy with user uploads |
| H1-M2 | Step-5 offload routing for AVIF/HEIC/HEIF/ICO + `work/` copy + safe-name sanitization | Wave 2 D | Before Wave 1 is operationally exposed |
| H1-M3 | Workspace-deletion trigger wires to `WorkspaceDeleteHook` | Wave 1b B2 | Should land as the 5th commit of Wave 1b — currently missing |
| H1-m1 | FR-023a step-7 marker sanitization | Wave 2 D | Before any LLM-bound content carries raw filenames |
| H1-m2 | FR-028a caller-workspace-context propagation to 13+ sites | Wave 2 G | Before any cross-workspace Spoofing is theoretically reachable |
| H1-m3 | Orchestrator consults `Catalog.DefaultResizeBudget` / `Model.ResizeBudget` | Wave 3 T9 | Before per-provider resize budgets are enforceable |
| H1-O1 | Catalog consumer wiring | Wave 3 T9 | Co-travels with H1-M1 |
| H1-O2 | BDD scenario "Manifest refcount drives GC" added to spec | Wave 3 T10 | Before test #42 is meaningful |
| H1-O3 | (no action — observation only) | n/a | n/a |
| H1-O4 | E2E env-var skip-gate | Wave 3 T10 | Before CI runs against real providers |
| H1-O5 | (no action — observation only) | n/a | n/a |

---

## Holistic verdict

**PASS-WITH-FOLLOW-UPS — 0 CRIT / 3 MAJOR / 6 MINOR / 5 OBS.**

The four slices individually ship their governing FRs. The cross-cutting gaps (H1-M1 refcount unwired; H1-M2 AVIF/HEIC routes to wrong step; H1-M3 cascade-delete hook unwired) are all **downstream-owned** — B2 (the missing 5th Wave-1b commit), Wave 2 D, Wave 2 G, Wave 3 T9/T10 — and named in the plan. None are blocking on Wave 1's per-slice merge gates. All three MAJORs MUST be tracked as acceptance-gate blockers for the Wave 4 PR-merge gate (`gh workflow run pr.yml --ref sendfile-fix` per plan §T14).

Recommend: (a) rebase B2 onto the existing Wave-1 stack as the 5th commit (`feat(adr-051-rev4): media audit events + cascade-delete wiring`) — closes H1-M3; (b) re-run this holistic after B2 lands; (c) carry H1-M1 + H1-M2 forward to the Wave 2 D / Wave 3 T9 reviewer gates with explicit "do not close without" notes.

---

*End of holistic review, Wave 1 round 1.*