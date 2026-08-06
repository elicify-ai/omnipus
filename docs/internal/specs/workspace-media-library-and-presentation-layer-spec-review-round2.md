# Grill Review — Spec (Round 2): Workspace Media Library + Capability-Aware Presentation Layer

**Review target:** `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md` (plan-spec)
**Governing ADR:** `ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Reviewer mode:** adversarial (grill-spec, **plan-spec mode**) — assumes the spec, implemented as-is, causes a production incident.
**Read-only on spec:** confirmed — the spec was not modified.
**Date:** 2026-07-22 (round 2)
**Round-1 review:** `workspace-media-library-and-presentation-layer-spec-review-round1.md` (1 CRITICAL + 7 MAJOR + 5 MINOR + 2 OBSERVATION — all corrected per the spec's "Resolved Ambiguities" table and new FRs FR-007a/FR-017a/FR-020a/FR-028a).
**Evidence base:** code read via graphify + direct Read (`pkg/media/store.go` full, `pkg/agent/loop_media.go` resolve/marker paths, `pkg/gateway/rest_workspaces.go` audit precedent, `pkg/audit/audit.go` Entry shape, `pkg/audit/events.go`); all 13+ `Resolve`/`ResolveWithMeta` call-sites enumerated via ripgrep; ADR + ADR grill + evidence matrix re-read in full; round-1 corrections traced through the matrix, test-order, and BDD section.

> **Do-not-re-raise rule honored.** Round-1 findings C1, M1–M8, m1–m5, O1–O2 were each re-verified as resolved (see Phase 1 "Round-1 correction audit" below). This round reports only NEW defects — those the round-1 review missed or that the round-1 corrections introduced.

---

## Executive Summary

Round 1 caught the right things and the corrections landed cleanly for **six of the eight** tracked items: C1's step-5 path-traversal is closed by FR-020a; M5's dedup contradiction is gone (Edge Cases now says "no dedup"); M4's SC-010 no longer says "without modification"; M3's phantom `registry_test.go` is replaced by an explicit "does NOT exist" note + a new `TestStore_RegistryPersistsAcrossBoot`; M6's D2-passthrough deletion is now surfaced in the Symbols table + a dedicated test; M8's outcome-relabel is encoded as FR-017a with BDD + test.

**However, the round-1 M7 fix (FR-007a) is itself defective in two compounding ways, and the C1 fix left a parallel injection surface open one layer down.** The headline new problems:

1. **FR-007a's central claim — that the manifest orphan-GC refcount "REUSES/EXTENDS the existing `pathStates.refCount` … a SINGLE refcount mechanism, not a parallel one" — is factually false against the code.** The existing counter (`pkg/media/store.go:79-99, 168-177, 354-380`) is keyed by **filesystem path**, incremented at **`Store()` (ref registration)**, decremented at **ref release**, and **deletes the file immediately when the count hits zero** (store.go:372-374). The orphan-GC refcount FR-007a describes is keyed by manifest entry, incremented at **session/turn-attach**, decremented at **cleanup**, and must **persist the file for 30 days after zero** before GC. These are contradictory lifecycle rules — you cannot serve "delete immediately at zero" and "delete after 30 days at zero" from one counter. An implementer following FR-007a literally will either corrupt the existing `CleanupPolicyDeleteOnCleanup` lifecycle (by deferring its delete) or build a parallel counter while believing the spec told them to reuse one.

2. **FR-007a's matrix row names a BDD scenario — "Manifest refcount drives GC" — that does not exist in the BDD section.** The phrase appears only in the Traceability Matrix (l.1088), the Test Implementation Order (l.920), the Execution Flows table (l.67), and Ambiguity #9 (l.1137) — never as a `#### Scenario:` heading. Round-1 check #6 ("every BDD in the matrix") passed in round 1; the round-1 M1 correction broke it by adding a matrix row whose BDD was never written.

3. **The step-7 honest marker `[attachment unavailable: <name> (<reason>)]` (FR-023) leaves `<name>` undefined, and the existing code injects the raw user-controlled `meta.Filename` there** (`loop_media.go:78-79, 91, 117`). Round-1 C1 proved the raw filename is a prompt-injection carrier in LLM content; FR-020a closed it for the step-5 copy name but the same vector remains open in the step-7 marker. The spec author demonstrably knows the filename is user-controlled (FR-020a says so) — the omission is the inconsistency.

4. **FR-028a's caller-workspace membership guard (a MUST, STRIDE Spoofing) has an unspecified blast radius.** `ResolveWithMeta`/`Resolve` has 13+ live call-sites — 8 channels (`weixin`, `qq`, `wecom`, `matrix`, `feishu`, `discord`, `slack`, `telegram`), gateway replay (`replay.go:677`), the upload-echo path (`rest.go:9020`), and 5 agent-loop sites. None are enumerated in the Symbols/Impact table, and the spec never states whether channels that resolve a `media://workspace/<id>` ref for OUTBOUND delivery route through the new workspace-aware resolver or bypass the membership check.

None of these are architecture problems — the two-layer design and all seven locked operator decisions still stand. They are spec-level defects (one factual error against code, one structural-integrity regression, one security-completeness gap, one impact-blast-radius gap) that must close before this spec governs an implementation.

**Verdict: REVISE.** (Not BLOCK: no CRITICAL, architecture sound, every finding is a surgical spec fix. Not PASS: 4 MAJOR remain, two of them introduced by the round-1 corrections themselves.)

**Counts:** 0 CRITICAL · 4 MAJOR · 5 MINOR · 3 OBSERVATION.

---

## Findings Table

| ID | Severity | Lens | Section / FR / BDD line | One-line |
|---|---|---|---|---|
| R2-M1 | MAJOR | Incorrectness (factual, vs code) | FR-007a (l.1029), Execution Flows (l.67) | FR-007a's claim that the orphan-GC manifest refcount "REUSES/EXTENDS the existing `pathStates.refCount` … a SINGLE mechanism" is false against the code: the existing counter is path-keyed, Store/release-triggered, and **immediate-delete-at-zero** (store.go:372-374) — incompatible with manifest-keyed, session-attach-triggered, 30d-deferred-delete semantics. |
| R2-M2 | MAJOR | Traceability (orphan BDD) | FR-007a matrix row (l.1088) vs BDD section | The matrix/test-order name a BDD "Manifest refcount drives GC" that **does not exist** as a `#### Scenario:` anywhere in the BDD section — a round-1-correction-introduced structural-integrity fail (Phase-1 check #6 now broken). |
| R2-M3 | MAJOR | Insecurity (STRIDE: Info Disclosure / prompt injection) | FR-023 (l.1047); existing `loop_media.go:78-79,91,117` | The step-7 honest marker `[attachment unavailable: <name> (<reason>)]` leaves `<name>` undefined; the existing code injects the **raw user-controlled `meta.Filename`** into provider-bound content — the same prompt-injection class as round-1 C1, unaddressed by FR-020a (which sanitizes only the step-5 copy name). |
| R2-M4 | MAJOR | Incompleteness / Impact (security blast radius) | FR-028a (l.1054); Symbols/Impact tables (l.33-59) | FR-028a's caller-workspace membership guard is a MUST, but the 13+ `Resolve`/`ResolveWithMeta` call-sites (8 channels, gateway replay, rest upload-echo, 5 agent-loop sites) are not enumerated, and the spec never states whether channels resolving a workspace ref for outbound delivery are routed through the guard — Spoofing coverage is ambiguous. |
| R2-m1 | MINOR | Ambiguity (edge case) | FR-017a (l.1040) × FR-019 (l.1042) | FR-017a defines relabel-to-`media_unsupported` only for the case the step-4 retry **succeeds**; it is silent when the retry itself fails with a DIFFERENT error (e.g. context-overflow) — the final classification and whether the initial media trigger is preserved for telemetry are undefined. |
| R2-m2 | MINOR | Testability (phantom requirement) | FR-024 (l.1048); matrix row (l.1107) | FR-024's MUST "re-validated against fresh 2026 data before freeze" has no defined gate, artifact, or test — the matrix maps it only to `TestCapabilityRegistry_UnknownModel_Optimistic` (the optimistic-default behavior, not re-validation). |
| R2-m3 | MINOR | Operability / Testability | Test-order rows 33-34 (l.917-918); Integration Boundaries (l.315) | The two E2E tests are specified against "real provider for E2E" with no env-var gating; CI has no live provider keys, so as written they are unrunnable/flaky (no `OMNIPUS_E2E_*` skip-pattern exists in the repo). |
| R2-m4 | MINOR | Incompleteness (audit shape) | FR-033 (l.1058); FR-008 (l.1030) | FR-033 says single-file delete is audited "matching the `workspace.delete` precedent," but the audit `Event` name (e.g. `media.delete` vs `workspace.media.delete`) and `Details` fields (file id? workspace id? filename?) for the NEW single-file-delete event are unspecified. |
| R2-m5 | MINOR | Inconsistency (test naming) | Regression table (l.1008) vs test-order (l.913)/matrix (l.1111) | The regression table names the two-tier-resolution test `TestResolver_LegacyRefFallback_AfterNamespaceSplit`; the test-order and matrix name the same test `TestResolver_WorkspaceLibraryFirst_LegacyFallback` — one test, two names. |
| R2-O1 | OBSERVATION | Concurrency / Inoperability | FR-007a (l.1029) | The manifest refcount's concurrency model is unspecified (which mutex serializes cross-package inc/dec?). Round-1 O2 was unaddressed; R2-M1 (reuse claim false) makes it concrete — a real new counter needs a declared lock (`s.mu`? the 64-shard FNV pool?). |
| R2-O2 | OBSERVATION | Infeasibility (memory footprint) | FR-013 (l.1035); Constraint #3 | The `DecodeConfig` guard caps at 16 MP, but a 16 MP synchronous `image.Decode` still allocates ~64 MB (RGBA); a multi-attachment turn (Mistral allows 8) peaks ~512 MB transiently in the turn path. Pre-existing `maxImagePixels`, but worth noting vs the footprint constraint. |
| R2-O3 | OBSERVATION | Operability (sunset enforcement) | FR-029 (l.1053) | "Legacy resolution removed in v0.1.2" is a future-release obligation with no removal gate or tracking test — recorded but not enforced. |

---

## Phase 1 — Structural Integrity (plan-spec checklist, round 2)

| # | Check | Result | Evidence |
|---|-------|--------|----------|
| 1 | Every user story has ≥1 acceptance scenario | **PASS** | US-1…US-11 unchanged; all carry ACs. |
| 2 | Every acceptance scenario has a BDD | **PASS** | Spot-checked US-4/7/8/9/11 — all ACs map to BDDs (incl. the round-1-added FR-017a/FR-020a/FR-028a BDDs). |
| 3 | Every BDD has a `Traces to:` | **PASS** | All scenarios incl. the four new ones carry `Traces to:`. |
| 4 | Every BDD has a named TDD test | **PASS** | Round-1 M2 closed: FR-008 now names `TestWorkspaceLibrary_ExplicitDelete_RemovesBytesAndManifest` (l.919). |
| 5 | Every FR (MUST/SHOULD) in the traceability matrix | **PASS (rows)** / **FAIL (one cell)** | Every MUST/SHOULD FR now has a matrix **row** (FR-007a/FR-017a/FR-020a/FR-028a added). BUT the FR-007a row's BDD cell names a scenario that doesn't exist → R2-M2. |
| 6 | Every BDD in the matrix appears in the BDD section | **FAIL** | "Manifest refcount drives GC" (FR-007a cell, l.1088) has no corresponding `#### Scenario:`. → R2-M2. (Round 1 had this PASS; the M1 correction regressed it.) |
| 7 | Test datasets cover boundary / edge / error | **PASS** | Format×Model (30 rows), rejection-body (11 rows, incl. the two new exclusion members bad-tool-args/schema), sha256 (5 rows) — all intact; FR-015 floor now defined (l.1037) closes the ladder-boundary gap. |
| 8 | Regression addressed | **PASS** | Round-1 M3 closed: phantom `registry_test.go` replaced with an explicit "does NOT exist on main" note (l.1008) + new `TestStore_RegistryPersistsAcrossBoot` (l.925). |
| 9 | SCs measurable | **PASS** | SC-001 now backed by the all-11-formats Scenario Outline (l.371); SC-006 carries the safe-name clause (l.1069); SC-010 reframed to "UPDATED" not "without modification" (l.1073). |

**Net Phase 1:** 8 PASS / 1 FAIL / 0 PARTIAL. The single FAIL (R2-M2) is a structural-integrity regression introduced by the round-1 M1 correction itself.

### Round-1 correction audit (verification that the fixes actually hold)

| Round-1 ID | Claimed fix | Verified? |
|---|---|---|
| C1 | FR-020a sanitizes step-5 copy name (sha256/UUID, containment check) + BDD (l.720) + test (l.921) | ✅ Holds for step-5. ⚠️ Does **not** extend to step-7 marker `<name>` → R2-M3. |
| M1 | FR-007a added to matrix (l.1088) + test (l.920) | ⚠️ Matrix row added, BUT (a) its BDD is a phantom (R2-M2) and (b) its "reuse" claim is false vs code (R2-M1). |
| M2 | FR-008 test named `TestWorkspaceLibrary_ExplicitDelete_RemovesBytesAndManifest` (l.919) | ✅ Holds. |
| M3 | `registry_test.go` phantom replaced with new `TestStore_RegistryPersistsAcrossBoot` (l.925, l.1008) | ✅ Holds. |
| M4 | SC-010 reframed to "UPDATED … via the presentation orchestrator" (l.1073); "without modification" dropped | ✅ Holds. |
| M5 | Edge Cases (l.268) now "no dedup in v0.1.1"; Ambiguity #4 (l.1132) consistent | ✅ Dedup contradiction gone. |
| M6 | Symbols table (l.38) now "DELETE the D2 passthrough branch (FR-016)"; dedicated test (l.922) | ✅ Holds. |
| M7 | FR-007a names inc/dec call-sites + declares "reuses pathStates.refCount" | ❌ **Resolution is defective** — the reuse claim is false (R2-M1) and the BDD is missing (R2-M2). |
| M8 | FR-017a (l.1040) + BDD "Outcome-relabel…" (l.653) + test (l.923) + matrix (l.1099) | ✅ Holds. |
| m1 | Scenario Outline "Every matrix format uploads with HTTP 200" (l.371), 11 examples | ✅ Holds. |
| m2 | FR-015 (l.1037) defines floor "quality 40 AND long edge ≥ 256px"; BDD (l.585); test 14a (l.898) | ✅ Holds. |
| m3 | BDD "PDF offload emits document-class guidance noun" (l.707) | ✅ Holds. |
| m4 | FR-028 (l.1052) names discriminator `strings.HasPrefix(ref, "media://workspace/")` | ✅ Holds. |
| m5 | FR-033 (l.1058) promoted MAY→SHOULD, covers single-file + cascade delete | ✅ Holds at the SHOULD level; residual: event name/details unspecified (R2-m4). |

**Summary:** 13 of 15 round-1 items verified clean. M7's resolution is defective (R2-M1 + R2-M2). C1's resolution is complete for step-5 but leaves the parallel step-7 surface open (R2-M3).

---

## Phase 2 — Eight Lenses (round 2)

### 1. Ambiguity

**R2-m1 — Outcome-relabel on a retry that fails with a different error.** FR-017a (l.1040) defines the relabel precisely for the *successful* case: "After a successful outcome-based retry … the turn's error MUST be relabeled `media_unsupported`." It is silent on the (likely) case where the step-4 retry strips media, re-sends, and the **second** attempt fails with a *different* error — e.g. the media was the trigger but the text alone now blows context-overflow (`CodeContextTooLong`, in the exclusion set), or trips content-policy. FR-019 consumes the per-class guard regardless. Two readings are both defensible: (a) the final error (context-overflow) governs and the media relabel never applies because the retry did not "succeed"; (b) the turn's *history* records `media_unsupported` for the first attempt and `context-overflow` for the second. The spec picks neither. *Fix:* add one clause to FR-017a — "If the retry fails, the relabel does NOT apply; the retry's own error governs the final classification" — and a test row (retry → different excluded error → classification = the retry's error, not `media_unsupported`).

**No other new ambiguity.** Round-1 m2 (ladder floor) and m4 (ref discriminator) are resolved.

### 2. Incompleteness

**R2-M1 — FR-007a's "reuse pathStates.refCount" is factually false against the code.** This is the round's most important finding. FR-007a (l.1029) and the Execution Flows table (l.67) state: *"The manifest refcount REUSES/EXTENDS the existing `pathStates.refCount` (`pkg/media/store.go:80`) — a SINGLE refcount mechanism, not a parallel one (no second counter to reconcile)."* The code contradicts this on every axis:

| Axis | Existing `pathStates.refCount` (verified, `store.go`) | FR-007a manifest refcount |
|---|---|---|
| Map key | **filesystem path** (`s.pathStates[localPath]`, l.168) | manifest entry / workspace ref |
| Increment trigger | `Store()` — ref **registration** (l.176) | "session/turn attaches the ref" (message-store write) |
| Decrement trigger | ref **release** — `releaseRefLocked` from `ReleaseAll`/`CleanExpired` (l.377) | "session cleanup + explicit delete" |
| Delete timing | **immediate** `os.Remove` when count hits 0 (l.372-374, called from l.248/320) | **deferred 30 days** after count hits 0 (FR-007) |

The fourth row is fatal: the existing counter deletes the underlying file the instant its refcount reaches zero (and `deleteEligible`); FR-007 requires the file to *persist 30 days at zero* for reuse before orphan GC touches it. **One counter cannot enforce both "delete immediately at zero" and "delete after 30 days at zero."** If an implementer "reuses" `pathStates.refCount` for orphan GC by deferring its delete, they break the existing `CleanupPolicyDeleteOnCleanup` contract that `ReleaseAll` and `CleanExpired` rely on (agent session-inline media, send_file borrows). If they instead add the 30-day branch only for workspace-library paths, they have built the *second parallel counter* the spec explicitly forbids — while believing they were told not to. Either way the spec misleads. *Fix:* delete the "REUSES/EXTENDS … a SINGLE refcount mechanism" clause; state plainly that the workspace library maintains its **own** manifest refcount (keyed by manifest id, incremented at session/turn-attach, decremented at cleanup, with a "zero-since" timestamp for the 30-day GC), and that the existing path-based `pathStates.refCount` is **unchanged** and continues to govern ref/path lifecycle for the legacy + session-inline stores.

**R2-m2 — FR-024's "re-validated before freeze" is a phantom requirement.** FR-024 (l.1048) is a MUST: the seed is "re-validated against fresh 2026 provider data before freeze." There is no defined gate (a review artifact? a CI check? a sign-off?), no artifact (a diff against the evidence matrix?), and no test. The matrix (l.1107) maps FR-024 to `TestCapabilityRegistry_UnknownModel_Optimistic`, which tests the optimistic-default *behavior*, not re-validation. As written the clause is an untestable process wish dressed as a MUST. *Fix:* either convert the clause to a SHOULD process note with a named artifact (e.g. "a dated re-validation commit on `provider-media-format-support.md` is recorded in the release notes"), or drop it from the MUST strength.

### 3. Inconsistency

**R2-m5 — One two-tier-resolution test, two names.** The Regression table (l.1008) introduces `TestResolver_LegacyRefFallback_AfterNamespaceSplit`; the Test Implementation Order (l.913) and the FR-028/FR-029 matrix rows (l.1111-1113) call the same test `TestResolver_WorkspaceLibraryFirst_LegacyFallback`. Both clearly describe the workspace-library-first → legacy-fallback two-tier order. An implementer will create one and the traceability of the other dangles. *Fix:* pick one name and use it in all three locations.

**No new internal contradiction.** Round-1 M5 (dedup) is resolved and verified clean.

### 4. Infeasibility

**R2-O2 — Resize pipeline transient peak memory (observation, not a blocker).** FR-013's `DecodeConfig` guard (16 MP) correctly prevents the *declared* pixel-bomb from ever reaching `image.Decode`. But a 16 MP image that *passes* the guard still allocates ~64 MB (RGBA, 4 bytes/px) during the synchronous decode-before-resize at presentation time; a multi-attachment turn (Mistral permits 8 images/req) can peak ~512 MB transiently in the turn path. `maxImagePixels` pre-exists this spec, so this is not a *new* violation, but the presentation orchestrator now runs decode+resize synchronously for every image in every turn where it did not before — worth an explicit note against Hard Constraint #3 (or an operator note that the 16 MP cap is the memory cap, not just a decode-safety cap).

**No new infeasibility.** Pure-Go feasibility holds (`x/image v0.44.0` and oksvg/rasterx already in `go.mod`, verified in round 1); no CGo; step-5 `work/` confinement via `SafeWorkDir` verified.

### 5. Insecurity (STRIDE)

| STRIDE class | Round-2 finding |
|---|---|
| **Spoofing (cross-workspace read)** | **R2-M4** — FR-028a's membership guard is a MUST, but its enforcement surface is under-specified. 13+ `Resolve`/`ResolveWithMeta` call-sites exist (verified via ripgrep): 8 channels (`weixin/media.go:715`, `qq/qq.go:417`, `wecom/media.go:497`, `matrix/matrix.go:460` use `ResolveWithMeta`; `feishu`, `discord:187`, `slack:191`, `telegram:459` use `Resolve`), gateway replay (`replay.go:677`), the upload-echo path (`rest.go:9020`), and 5 agent-loop sites (`loop_media.go:75,373`, `loop.go:4844,8112,8223,8295`). The Symbols/Impact table (l.33-59) enumerates none of them. The spec never states whether a channel resolving a `media://workspace/<id>` ref for OUTBOUND delivery is routed through the new caller-workspace-aware resolver or calls the raw `store.Resolve` (which takes no caller context, `store.go:56-59, 217`). If channels bypass the guard, a crafted ref placed in an outbound message part can be resolved cross-tenant. *Fix:* (a) enumerate the resolver call-sites in the Impact table; (b) state explicitly whether channels/replay use the workspace-aware resolver or a deliberately-bypass legacy path, and justify the bypass if so. |
| **Tampering** | sha256-on-read still well-covered (FR-002, US-2, 5-row dataset). **PASS.** |
| **Repudiation** | FR-033 promoted to SHOULD for both single-file + cascade delete (round-1 m5 resolved). Residual: the single-file-delete audit event name + Details fields are unspecified → R2-m4. |
| **Info disclosure (prompt injection)** | **R2-M3** — Round-1 C1 closed the step-5 copy name. The step-7 honest marker `[attachment unavailable: <name> (<reason>)]` (FR-023) is the *same vector class* and is still open: `<name>` is undefined in the spec and the existing code injects raw `meta.Filename` into provider-bound content (`loop_media.go:78-79` `if meta.Filename != "" { name = meta.Filename }`; identically at l.91, l.117). A filename payload (`foo.png]\n\nIgnore previous instructions and …`) is LLM-readable content; the model is not directed to act on it (unlike the step-5 path it was told to `read_file`), so it is strictly lower-impact than C1 — but it is the same author-aware vector (FR-020a explicitly calls filename "user-controlled … a prompt-injection vector") left unmitigated one layer down. *Fix:* extend FR-020a's safe-name rule to every `<name>` interpolated into provider-bound content — explicitly including the FR-023 honest marker — or define `<name>` as a safe-derived value. Add a test with a traversal/newline-payload filename asserting the marker contains no raw filename and no newline breakout. |
| **Denial of service** | Disc-as-limit + two-mechanism split (FR-005/FR-006) bounds the agent flood. No new DoS. |
| **Elevation of privilege** | FR-020a closes the step-5 path-traversal/sandbox-escape (verified: `filepath.Clean(filepath.Join(safeWorkDir, safeName))` + containment, mirroring `safeID`). **PASS for step-5.** |

### 6. Inoperability

**R2-m3 — E2E tests are specified against real providers with no gating.** Test-order rows 33-34 (`TestE2E_AnyFileAnyModel_UsefulTurn`, `TestE2E_TextOnlyModel_ImageSurvivesAsOffload`) and the Integration Boundary ("real provider for E2E", l.315) prescribe live provider calls. The repo has **no** `OMNIPUS_E2E_*` / `*PROVIDER*` env-var skip pattern in any test (verified via ripgrep across `pkg/`). CI does not carry live provider keys, so these E2E tests as written are either unrunnable or silently skipped with no spec-level gate. *Fix:* add a clause that E2E tests gate on an env var (e.g. `OMNIPUS_E2E_PROVIDER_KEY`) and `t.Skip` cleanly when absent, mirroring how the project gates other live-only tests — or explicitly route them through the mock provider.

**R2-O1 — Manifest refcount concurrency model still unspecified (round-1 O2, compounded).** Round-1 O2 observed the manifest refcount has no concurrency story; FR-007a did not add one (it only claimed reuse). Because R2-M1 shows the reuse is false, a real new counter must exist, and its increment (message-store write path) and decrement (session-cleanup path) execute in different packages — the spec must name the lock that serializes them. The repo uses a 64-shard FNV-hash mutex pool for sessions/memory (`pkg/memory/jsonl.go`) and `s.mu` for the media store; the spec should declare which (or a new one). *Fix:* one clause in FR-007a naming the guarding mutex for the manifest refcount.

### 7. Incorrectness

**R2-M1 (restated under incorrectness).** The "reuse" claim is not merely incomplete — it is a positive factual error against the code that will mis-direct implementation. See the Incompleteness lens for the full evidence table.

**R2-M2 (restated under incorrectness).** A traceability matrix cell referencing a non-existent BDD is a correctness defect in the spec's own primary artifact (the trace chain). See Phase 1 check #6.

### 8. Overcomplexity

No new overcomplexity. Round-1's residual (two-refcount concern) is now sharpened by R2-M1: the spec's attempt to *deny* a second counter (by claiming reuse) is precisely what creates the confusion — a clean "two counters with different jobs, here is how they interact" statement is simpler and safer than the false "single mechanism" claim. Fixing R2-M1 resolves the overcomplexity residual as a side effect.

---

## Phase 3 — Test-Coverage Gap Analysis (round 2)

| Claim / BDD area | Round-1 status | Round-2 status | Gap |
|---|---|---|---|
| Orphan GC refcount (FR-007/007a) | ❌ M1+M7 | ❌ **R2-M1 + R2-M2** — the matrix BDD is a phantom and the reuse claim is false; the one named test (`TestWorkspaceLibrary_Refcount_DrivesGC`) is unbacked by a BDD and its "reuses pathStates.refCount" description is wrong | Must add the BDD + correct the reuse claim before the test is meaningful. |
| Step-5 offload sanitization (FR-020a) | ❌ C1 | ✅ test `TestPresentation_Step5_SanitizesTraversalFilename` (l.921) + BDD (l.720) | Closed for step-5. |
| Step-7 honest marker (FR-023) | (not raised) | ❌ **R2-M3** — no test covers a payload filename in the marker `<name>`; existing `TestPresentation_Step7HonestMarker_CorruptFile` uses corrupt/empty files, not injection payloads | Add a marker-name sanitization test. |
| Cross-workspace resolver guard (FR-028a) | (STRIDE note) | ⚠️ **R2-M4** — test `TestResolver_RejectsCrossWorkspaceRef` exists (l.924), but it only exercises the agent-loop resolver; channel/replay/rest call-sites that also resolve refs have no specified coverage | Coverage of the guard is窄; blast radius unenumerated. |
| Outcome-relabel (FR-017a) | ❌ M8 | ✅ BDD (l.653) + test (l.923) | Closed for the success case; the retry-fails-differently edge (R2-m1) untested. |
| Capability re-validation (FR-024) | (not raised) | ⚠️ **R2-m2** — no test/artifact for "re-validated before freeze" | Phantom MUST clause. |
| E2E (TestE2E_*) | (not raised) | ⚠️ **R2-m3** — no env-var gating; unrunnable in CI as written | Add skip-gate. |
| All other areas (US-1/2/3/5/6/9/10/11) | ✅ | ✅ | No new gaps. |

---

## STRIDE (round-2 summary)

| Class | Status | New finding |
|---|---|---|
| Spoofing | **GAP** | R2-M4 — membership-guard blast radius under-enumerated; channel/replay/rest coverage unspecified. |
| Tampering | PASS | — |
| Repudiation | **MINOR GAP** | R2-m4 — single-file-delete audit event shape (name + Details) unspecified. |
| Info Disclosure | **GAP** | R2-M3 — step-7 marker `<name>` prompt-injection surface open (same class as resolved C1). |
| Denial of Service | PASS | — |
| Elevation of Privilege | PASS | FR-020a closes step-5 path traversal. |

---

## Unasked Questions

1. **(R2-M1)** Does the workspace-library manifest refcount reuse `pathStates.refCount` or not? The code says it cannot; the spec says it does. Pick one and reconcile with the existing immediate-delete-at-zero lifecycle.
2. **(R2-M2)** Where is the "Manifest refcount drives GC" BDD? It is named in the matrix but absent from the BDD section.
3. **(R2-M3)** Is the step-7 honest marker's `<name>` interpolated from the raw `meta.Filename` (current code) or a safe-derived value? If raw, it is the same prompt-injection vector FR-020a just closed for step-5.
4. **(R2-M4)** Do channels / gateway replay / the upload-echo path resolve `media://workspace/<id>` refs through the new caller-workspace-aware resolver, or do they bypass the FR-028a membership guard? The 13+ call-sites are not enumerated.
5. **(R2-m1)** When the step-4 retry fails with a *different* error, is the turn classified by the retry's error or relabeled `media_unsupported`?
6. **(R2-m2)** What is the concrete gate/artifact for FR-024's "re-validated before freeze"?
7. **(R2-O1)** Which mutex serializes the manifest refcount's cross-package increment/decrement?

---

## Verdict

**REVISE.**

No CRITICAL. The architecture is sound, all seven locked operator decisions survive, and 13 of the 15 round-1 items are verified cleanly resolved. But the spec cannot govern an implementation until these four MAJORs close:

- **R2-M1 (incorrectness, vs code)** — FR-007a's "REUSES/EXTENDS `pathStates.refCount` … a SINGLE mechanism" is factually false; the existing counter is path-keyed, Store/release-triggered, and immediate-delete-at-zero (store.go:372-374), incompatible with the manifest refcount's 30d-deferred orphan-GC semantics. Strike the reuse claim; declare a separate counter.
- **R2-M2 (traceability — orphan BDD)** — the FR-007a matrix row names "Manifest refcount drives GC," a BDD that does not exist in the BDD section (round-1-correction-introduced; Phase-1 check #6 now fails).
- **R2-M3 (security incompleteness)** — FR-023's step-7 honest marker `<name>` is undefined and defaults to raw `meta.Filename` in the code (`loop_media.go:78-79,91,117`); same prompt-injection class as round-1 C1, unaddressed by FR-020a. Extend the FR-020a safe-name rule to all `<name>` interpolations into provider-bound content.
- **R2-M4 (impact / security blast radius)** — FR-028a's MUST membership guard has 13+ resolver call-sites (8 channels, replay, rest, 5 agent-loop sites) unenumerated in the Impact table; channel/replay workspace-ref resolution unspecified. Enumerate and clarify coverage.

The five MINORs (R2-m1 … R2-m5) and three OBSERVATIONS (R2-O1 … R2-O3) are required polish; none individually blocks, but R2-O1 (concurrency) should be settled alongside R2-M1 since they concern the same counter.

Once R2-M1–M4 are resolved (and ideally R2-m1/m2/m3 folded in), the spec can move to PASS.
