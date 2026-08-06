# ADR-051 Rev 4 — Delivery Plan (v3 — accepted)

**Scope:** ADR-051 Rev 4 (Workspace Media Library + Capability-Aware Presentation Layer)
**Spec:** `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md` (1253 lines, 40 BDDs, 34 MUST/SHOULD FRs, traceability closed, two grill rounds applied)
**ADR:** `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Critical review (Stage 2):** `docs/internal/plans/ADR-051-rev4-delivery-plan-review.md` (verdict REVISE; 0 CRIT / 11 MAJOR / 7 MINOR / 5 OBSERVATION). All corrections folded into this v2.
**Branch:** `sendfile-fix` (operator directive, 2026-07-22: no new branch). Implementation lands as stacked commits on the existing branch.
**Author:** `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>` (already configured globally on this pod).
**Plan version:** v3 — accepted.

---

## 0. Available subagents (verified 2026-07-22)

| Agent role | Available as `subagent_type` | Notes |
|---|---|---|
| `general` | ✅ | Multi-purpose; carries per-slice lead-role prompts (backend-lead / frontend-lead / security-lead / qa-lead / architect) as inline instructions since no named lead agents are installed. |
| `explore` | ✅ | Fast code search / codebase intelligence. |
| `code-reviewer` | ✅ | pr-review-toolkit. |
| `code-simplifier` | ✅ | pr-review-toolkit. |
| `comment-analyzer` | ✅ | pr-review-toolkit. |
| `pr-test-analyzer` | ✅ | pr-review-toolkit. |
| `silent-failure-hunter` | ✅ | pr-review-toolkit. |
| `type-design-analyzer` | ✅ | pr-review-toolkit. |
| `backend-lead`, `frontend-lead`, `security-lead`, `qa-lead`, `architect` | ❌ NOT installed | CLAUDE.md describes these roles for human-coordinated workflows; not present as harness agents. **Compensating control:** the 7th holistic reviewer uses a deep-context prompt carrying the architect role + the spec's Decision sections; Slice D and Slice G get an extra STRIDE pass (`silent-failure-hunter` + holistic STRIDE-explicit). |
| 7th reviewer (architect holistic) | ❌ NOT installed | Substituted by `general` with a fresh-context holistic-review prompt. |

**Net reviewer gate size:** 6 pr-review-toolkit agents + 1 holistic `general` = the "7-reviewer gate" CLAUDE.md describes. MINOR/OBSERVATION-level findings are acceptable to ship; CRITICAL and MAJOR findings block handoff and must be fixed.

**Reviewer dispatch model:** the slice implementor spawns **6 parallel `general` agents in one turn**, each carrying per-reviewer-toolkit instructions (`code-reviewer`, `code-simplifier`, `comment-analyzer`, `pr-test-analyzer`, `silent-failure-hunter`, `type-design-analyzer`), plus 1 holistic. Total 7 parallel `general` calls per gate round. The pr-test-analyzer gets **first pass + longest context** (it sizes test-coverage gaps — the broadest job).

**Holistic reviewer prompt (compensating control, F-L4-1 PARTIAL resolution):** the 7th holistic `general` is supplied with the following context in its prompt: (i) the governing ADR's Decision section, (ii) the spec's Behavioral Contract + Explicit Non-Behaviors + Integration Boundaries, (iii) the round-1 and round-2 grill findings for the ADR + spec, (iv) the slice's TDD plan rows. This converts the absent-dedicated-architect problem into a context-loaded holistic review — the agent has the architect's frame-of-reference (the ADR's decision rationale) without needing the agent type. Without these supplies, the holistic role would degrade into a generic re-pass; with them, it is a structural-and-architectural holistic.

---

## 1. Slice → FR mapping (the unit of work)

Slices are defined by FR dependency: a slice owns its primary FRs, may *touch* FRs from another slice (review/QA), but never *owns* another slice's FRs. Coupled slices (sharing code paths, same files, or strict ordering) co-travel in the same wave.

| Slice | Primary FRs | Touched FRs | Coupled with |
|---|---|---|---|
| **A — Contracts foundation** | FR-031 (MediaLibraryEntry.yaml, MediaAttachmentRequest.yaml generated) | FR-001 (wire shape), FR-022 (wire shape for attachment requests) | Provides wire types to B, D, E, F, G, H |
| **B1 — Workspace library core (storage)** | FR-001, FR-002, FR-003, FR-004, FR-006, FR-007, FR-007a | FR-005 (carve-out verification), FR-028, FR-029, FR-030 | Standalone package `pkg/media/library/`; sha256-on-read; manifest; orphan-GC refcount (separate counter from `pathStates.refCount` per spec round-2 R2-M1); the cascade-delete **hook** in `pkg/workspace/` (function signature that B2 fills in) |
| **B2 — Audit events + cascade-delete audit wire-up** | FR-008 (audit), FR-009 (cascade-delete wire-up), FR-033 (audit event shape) | depends on B1's manifest refcount API existing | **MUST be sequential after B1 (NOT parallel).** Owns: audit event shape + single-file-delete audit + cascade-delete audit + the workspace-namespace wiring of the manifest API endpoint. The hook in B1's code (in `pkg/workspace/`) calls into B2's registered audit emitter. |
| **C — Capability catalog transport** | FR-024, FR-025, FR-026, FR-027 | FR-031 (consumed types), FR-010, FR-014, FR-015 (budget-from-catalog) | Lives in **new package `pkg/providers/capabilities/`** (NOT `pkg/providers/catalog/` — that already exists with provider metadata, not modalities). The freeze-gate artifact is committed at end of Slice C: `docs/internal/research/provider-media-format-support-2026-07.md` per FR-024. |
| **D — Step-5 offload + sanitization** | FR-020, FR-020a, FR-021, FR-022, FR-023, FR-023a | FR-004 (text extraction in step-6 text-injection path) | Co-travels with FR-020a security fix (cannot land without sanitization); FR-023a touches the existing `loop_media.go:78-79,91,117` injection sites. STRIDE-focused reviewer pass. |
| **E — Step-4 outcome-based retry + 2 new classifier codes** | FR-017, FR-017a, FR-018, FR-019 | FR-020 (degrades to step-5), FR-028 (resolver unchanged) | Co-travels with classifier tests; FR-018 adds `CodeToolArgs` + `CodeSchema` to `pkg/agent/translate_error.go` |
| **F — Resize-to-fit + D2-passthrough deletion** | FR-011, FR-012, FR-013, FR-014, FR-015, FR-016 | FR-002 (sha256 verified before decode), FR-007 (output metadata), FR-004 (normalized artifact cache) | Co-travels — FR-016 deletes the Rev-3 D2 passthrough branch in the same PR as FR-011/013/014/015 add resize-to-fit, **otherwise the new path is silently bypassed** (round-1 grill finding M6). Both live in `encodeImageToDataURL` (`pkg/agent/loop_media.go:464-478`). |
| **G — Resolver signature + cross-workspace guard** | FR-028, FR-028a, FR-029, FR-030 | FR-001 (resolved workspace namespace), FR-008 (delete path) | Co-travels — 13+ call-sites; requires B1 workspace library namespace to exist. **Extra STRIDE pass** — the cross-workspace guard is a security MUST (Spoofing guard). |
| **H — SPA media-library surface + composer library-refs** | (frontend mapping of FR-001, FR-003, FR-008, FR-021, FR-022) | Uses contracts from A; consumes `media://workspace/<id>` from B1+FR-028a | Depends on A and B1 |
| **I — Quality / integration / fix-everything** | Cross-cuts — runs as a non-feature wave | Brings pre-existing branch failures to green | Runs LAST (after Wave 4 acceptance) |

**Pre-existing branch debt** (Wave 4 T12 fix-everything — gated on observed CI failure, not a pre-planned list):
- T12 FIRST runs `gh workflow run pr.yml --ref sendfile-fix`, observes the actual failing jobs.
- T12 THEN triages the actual failures and groups them into fix commits.
- The four candidates the prior session saw failing were: `pkg/providers/openai_compat` HTML-error-format tests; `pkg/tools` OOXML read_file tests; `src/lib/llm-error.ts` wire-type migration (the Wire-Types-Lint/Verify-Contracts CI failures flagged these as duplicates that should live in `contracts/` — the lint is the authority); `MessageItem.tsx` tabindex. **But T12 does not assume these fail** — they may already pass on `main` or may have been fixed by Wave 1-3 changes. **Observed CI failures drive the fix scope**, not a pre-planned list.
- Constraint #7 ("no 'not mine' escapes") still applies: any CI failure on `sendfile-fix` HEAD at acceptance time must be fixed, regardless of authorship origin.

---

## 2. Wave plan (maximize parallel fan-out where independent)

### Wave 0 — Foundation (sequential prerequisite)
**1 subagent team:** `general` carrying architect role.
- **Slice A** — Generate `contracts/components/schemas/MediaLibraryEntry.yaml` + `MediaAttachmentRequest.yaml`. Reference from `contracts/openapi.yaml`. Run `scripts/gen-contracts.sh`. Verify with `make verify-contracts`. Hand the new generated types back to the controller.
- **Output:** 1 commit on `sendfile-fix` — `feat(adr-051-rev4): generate media library wire contracts`. Wire types now exist in `pkg/api/generated/` and `src/lib/api/generated/`.
- **Reviewer gate (this wave only):** none — wire-gen is mechanical + verifiably correct on `make verify-contracts`.

### Wave 1 — Core backend (Wave 1a: B1; Wave 1b: B2/C/F/E in parallel ×4)

**Wave 1a** is a single-slice prerequisite: B1 produces the storage + manifest + refcount API that B2's audit + cascade-delete wiring calls into.

**Wave 1b** dispatches four `general` subagents in parallel, each carrying its per-slice lead-role prompt:

| Team | subagent_type | Per-slice prompt instruction | Slice | Output (stacked commit) |
|---|---|---|---|---|
| T1 (Wave 1a, alone) | `general` (backend-lead) | "Implement Slice B1 — workspace library core storage under `pkg/media/library/`. **Define** the cascade-delete hook signature in `pkg/workspace/` (the function stub B2 fills in); B2 cannot land without it. Stops after B1 commits; B2 follows sequentially." | **B1** | `feat(adr-051-rev4): workspace media library storage (manifest, sha256, GC, refcount)` |
| T2 | `general` (backend-lead) | "Implement Slice B2 — audit events + cascade-delete wire-up (FR-009, FR-033). Requires B1's manifest refcount API and the cascade-delete hook signature to exist on `sendfile-fix` HEAD. Verify both before starting. Depends sequentially on B1." | **B2** | `feat(adr-051-rev4): media audit events + cascade-delete wiring` |
| T3 | `general` (backend-lead) | "Implement Slice C — capability catalog transport in NEW package `pkg/providers/capabilities/` (NOT `pkg/providers/catalog/`)…" | **C** | `feat(adr-051-rev4): capability catalog transport (GitHub Release + raw fallback)` |
| T4 | `general` (backend-lead) | "Implement Slice F — resize-to-fit + DecodeConfig guard + PNG→JPEG ladder + **DELETE D2 passthrough (FR-016)** in the same PR as FR-011/013/014/015 (same `encodeImageToDataURL` function). Co-travel enforced at the function boundary per round-1 grill M6…" | **F** | `feat(adr-051-rev4): resize-to-fit with co-traveled D2-passthrough deletion` |
| T5 | `general` (backend-lead) | "Implement Slice E — outcome-based retry + 2 new classifier codes (`CodeToolArgs`, `CodeSchema`) + outcome-relabel…" | **E** | `feat(adr-051-rev4): outcome-based strip-retry with classifier expansion` |

Five commits land as stacked commits on `sendfile-fix` in this wave.

**Wave-1 prompt rule (added to every slice prompt in §3):** "Do NOT modify the bodies of existing tests in `loop_media_test.go` (16 `TestResolveMediaRefs_*` tests), `loop_test.go` (6 `TestResolveMediaRefs_*` cross-cutting tests), or `loop_media_normalization_test.go` (4 `TestEncodeImageToDataURL_*` tests). Wave 3 T8 owns the rewrite of those tests against the new orchestrator contract. If your slice requires a test on `resolveMediaRefs` behavior, add a NEW test (e.g. `TestB1_NewManifest_RefcountDrivesGC`) rather than modifying an existing one. This avoids double-write churn in T8."

**Reviewer gate per commit:** each of the 5 commits is followed by the full 6-reviewer gate (T1, T2, T3, T4, T5) + holistic 7th. CRIT/MAJOR fixed before the wave is considered done. Per-wave convergence threshold = 3 rounds; after that, escalate to operator.

### Wave 2 — Cross-cutting & presentation (parallel ×3 in one turn)
Wave 2 slices depend on Wave 1 B1 (workspace library namespace exists) and A (wire types exist). Three parallel teams:

| Team | subagent_type | Per-slice prompt instruction | Slice | Output (stacked commit) |
|---|---|---|---|---|
| T6 | `general` (backend-lead) | "Implement Slice G — resolver signature change (nilable caller-workspace context) + cross-workspace guard; touch 13+ call-sites, pass nil for legacy `media://<uuid>` refs. Verify EACH of the 8 channels (telegram/discord/slack/matrix/irc/google_chat/whatsapp/signal) by name in the test matrix…" | **G** | `feat(adr-051-rev4): resolver signature with cross-workspace guard` |
| T7 | `general` (security-lead) | "You are acting as security-lead — STRIDE review focus. Implement Slice D — step-5 copy-into-workdir + FR-020a traversal-guard (sha256-derived name + `filepath.Clean`/`Join` containment against `SafeWorkDir`) + FR-023a filename sanitization in content injection…" | **D** | `feat(adr-051-rev4): step-5 offload with security sanitization` |
| T8 | `general` (frontend-lead) | "Implement Slice H — SPA media-library surface (list/reuse/delete) + composer attaches `media://workspace/<id>` refs…" | **H** | `feat(adr-051-rev4): spa media-library surface` |

**Reviewer gate:** each commit → 6-reviewer gate + holistic 7th. CRIT/MAJOR fixed.
**Extra STRIDE pass on T6 (Slice G) AND T7 (Slice D):** both are security-relevant (cross-workspace Spoofing guard; prompt-injection sanitization). The 7th holistic reviewer is asked to explicitly think STRIDE; `silent-failure-hunter` gets the first pass per the reviewer-dispatch model.

### Wave 3 — Integration + cross-slice wiring + test coverage
Sequential after Wave 2. **2 stacked commits.**

| Team | subagent_type | Task | Output |
|---|---|---|---|
| T9 | `general` (qa-lead) | Wire the orchestrator `pkg/agent/media_present.go` (NEW package per ADR-051 Rev-4 Affected Components): the 7-step presentation chain composes **all** slices — call B1 for storage, C for capability, D for offload + sanitization, E for outcome retry, F for normalize+resize, FR-020a/023a sanitization, etc. Replace `resolveMediaRefs`'s inline logic with the orchestrator (FR's impose the contract). T9 owns the **rewrite** of the 16 existing `TestResolveMediaRefs_*` tests + the 4 `TestEncodeImageToDataURL_*` tests to assert the new observable contract — behavioral invariants preserved per the round-1 grill M4 fix (NO "without modification" — but no test is silently dropped; every updated test maps to the same acceptance criterion). | `feat(adr-051-rev4): orchestrator wires slices into 7-step chain` |
| T10 | `general` (qa-lead) | Add the spec's TDD-plan named tests not yet implemented by Wave 1-2: all-formats-upload Scenario Outline (FR-001/SC-001, m1), resize ladder floor (FR-015, m2), document-class guidance noun (FR-021, m3), ref disambiguation (FR-028, m4), single-file-delete audit (FR-008/033, m5); round-2 corrections: deferred GC (R2-M1+M2 → test #42), filename sanitization in content (R2-M3 → test #43), legacy nilable resolver (R2-M4 → test #45), retry-fails-different (R2-m1 → test #44), freeze-gate artifact (R2-m2 → committed alongside), E2E env gating (R2-m3 → wired into E2E rows). | `test(adr-051-rev4): complete spec test coverage gap-fills` |

**Reviewer gate:** 6-reviewer gate on the diff so far (current branch HEAD vs `ae9271d0`^). All slices merged together, full chain reviewed. **Note:** the 6-reviewer gate is a code review (correctness, security, simplicity, comment quality, test-coverage sizing, type-design quality) — it does NOT exercise behavioral correctness. Behavioral correctness is enforced by the per-SC observation log in Wave 4 (T15). The "full chain tested" phrase in this row refers to the slice-local unit tests; behavioral verification happens at acceptance (Wave 4 T14/T15).

### Wave 4 — UAT + acceptance + fix-everything
Sequential after Wave 3.

| Team | subagent_type | Task | Output |
|---|---|---|---|
| T11 | `general` | Produce `docs/internal/uat/ADR-051-rev4-uat-test-plan.md` — comprehensive UAT covering matrix §5 stress + edge cases + every FR's acceptance behavior. Persona scripts (UAT-Mia/Claude-Sonnet-4, UAT-Mia/glm-5.2-text-only, UAT-Builder-with-PDF, UAT-Scout-with-AVIF, UAT-edge-traversal-attack, etc.). | `docs(uat): ADR-051-rev4 UAT plan` |
| T12 (parallel persona cohort) | parallel `general` subagents, one per persona | Each persona subagent runs its UAT scenarios against the live gateway/build via Playwright MCP, observes outcomes, logs deviations. | `docs(uat): ADR-051-rev4 UAT deviations log` |
| T13 | `general` (qa-lead) | **Fix-everything loop:** (a) UAT deviations, (b) **observed** CI failures on `sendfile-fix` HEAD — T13 FIRST runs CI and observes the actual failing jobs, THEN triages (no pre-planned list per F-L5-2 correction). Per CLAUDE.md Constraint #7 — no "not mine" escapes; every CI failure on the branch must be fixed. Re-run the 6-reviewer gate and UAT after each fix batch. Each fix batch adds one or more stacked commits. | multiple fix commits stacked on `sendfile-fix` |
| T14 | `general` (qa-lead) | Trigger CI: `gh workflow run pr.yml --ref sendfile-fix`. **Capture** the green run URL into `docs/internal/uat/ADR-051-rev4-ci-evidence.md` (commit hash + run URL + per-job status + timestamp). Acceptance gate (a): every job green. If red, return to T13. | `docs(uat): ADR-051-rev4-ci-evidence.md` |
| T15 | `general` (architect) | Acceptance gate (b): per-SC observation log — for each SC-001..SC-010 verify with a tool result (Playwright + gateway log + curl). Format: `## SC-NNN: <name>\n- Verification: <tool output>\n- Result: PASS/FAIL\n- Timestamp: <ts>`. Committed as `docs/internal/uat/ADR-051-rev4-sc-observations.md`. | `docs(uat): ADR-051-rev4-sc-observations.md` |
| T16 | `general` | Open the single draft PR at the end: `gh pr create --base main --head sendfile-fix --draft --title "feat(adr-051-rev4): workspace media library + capability-aware presentation layer" --body "Closes ADR-051 Rev 4 + spec delivery. <summary>"`. No per-wave PRs. | draft PR URL recorded in evidence |

**Reviewer gate (final, on the whole deliverable):** **6-reviewer gate + holistic 7th over the entire diff on `sendfile-fix` (HEAD vs `ae9271d0`^).** Loop until CRIT/MAJOR = 0. Then the goal is complete.

---

## 3. Per-slice prompt instructions (carried into the `general` agent)

The `general` agent does not have a "backend-lead" persona. Slice ownership is conveyed by **inline role instructions** in the spawn prompt. Standard template per slice:

```
You are acting as <role> on this slice (<slice-id>, FR-xxx, FR-yyy).

The sub-tasks for your slice are:
  1. Read the governing spec sections (<section refs>) and the spec's TDD plan rows for this slice.
  2. Write failing tests first (TDD). Use the test names from the spec's TDD plan verbatim where given. **Wave-1+ rule:** do NOT modify the bodies of existing tests in `loop_media_test.go`, `loop_test.go`, or `loop_media_normalization_test.go` — Wave 3 T9 owns their rewrite against the new orchestrator contract.
  3. Write the minimum implementation to pass.
  4. Update any imports, registration sites, or symbol tables the spec calls for.
  5. Run scoped Go tests with `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<your-package>/`. Re-run until green.
  6. Run `gofmt -l pkg/<your-package>` and `golangci-lint run --build-tags=goolm,stdjson --new-from-rev=HEAD pkg/<your-package>/`. Both must return empty / no findings.
  7. Run `CGO_ENABLED=0 go build -tags goolm,stdjson ./cmd/omnipus/` — must succeed (Hard Constraint #1, single Go binary).
  8. Commit on `sendfile-fix` with author `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>` (the global git config is already set). One commit per slice. NEVER force-push; NEVER admin/auto-merge to main.
  9. Return a concise work report: files touched, tests added, commit hash, spec-section-touched list, anything the next slice needs.

Hard constraints (CLAUDE.md):
  - #1: Single Go binary (verify via `go build ./cmd/omnipus`).
  - #2: pure-Go, no CGo. oksvg/rasterx are pure-Go (confirmed).
  - #3: minimal footprint, <10 MB RAM overhead beyond baseline — verify the slice doesn't introduce heavy allocations.
  - #4: graceful degradation on Linux <5.13, non-Linux, Android/Termux (use Landlock/seccomp where available; app-level fallback where not).
  - #5: Omnipus/OpenClaw conventions (SKILL.md / HEARTBEAT.md / SOUL.md / AGENTS.md / JSON config).
  - #8: contract-first wire formats — use the generated types from `pkg/api/generated/`; never a parallel struct/interface.
  - Never modify ADR-051-rev4 itself.
  - Never edit generated files directly.
```

The security-lead prompt (T7 in Wave 2) adds: `Extra focus on STRIDE: every byte that enters or leaves the step-5 path must be reviewable for Spoofing/Tampering/Repudiation/Info-Disclosure/DoS/Elevation-of-Privilege. The FR-020a + FR-023a sanitization is the load-bearing security fix — do not weaken it.`

**Frontend prompt variant (N-m3 MINOR resolution):** for Slice H (SPA), the standard Go-shaped template is replaced with the frontend shape — `cd src && npm run typecheck`, `npx vitest run`, `npm run build`, plus the same contract-first rule (never hand-write wire types; use `src/lib/api/generated/`). The frontend slice is gated on these three commands returning clean, not on `go build`/`golangci-lint`. The reviewer-gate for the frontend slice uses the same 6-toolkit fan-out but the `pr-test-analyzer`'s scope is vitest coverage rather than Go coverage.

The Wave 0 Slice A prompt differs slightly: "You are the GENERATOR step. Run `scripts/gen-contracts.sh`, verify with `make verify-contracts`, commit the generated files. Do NOT hand-write any of the types — the schemas are the source of truth, the generator is the only writer."

---

## 4. 6-reviewer gate (loop protocol — per-wave threshold)

After each wave's commits:

1. **Fan out** (one turn, 7 parallel `general` calls — six mapped to the pr-review-toolkit agents, one holistic):
   - `subagent_type: general` carrying `pr-review-toolkit:code-reviewer` instructions
   - `subagent_type: general` carrying `pr-review-toolkit:code-simplifier` instructions
   - `subagent_type: general` carrying `pr-review-toolkit:comment-analyzer` instructions
   - `subagent_type: general` carrying `pr-review-toolkit:pr-test-analyzer` instructions (gets the first pass per F-L4-3 OBSERVATION)
   - `subagent_type: general` carrying `pr-review-toolkit:silent-failure-hunter` instructions
   - `subagent_type: general` carrying `pr-review-toolkit:type-design-analyzer` instructions
   - `subagent_type: general` carrying **holistic** instructions (fresh-context holistic review of the entire wave's diff vs the wave's baseline commit; for security-relevant slices T7/T6 the holistic prompt is explicitly STRIDE-focused)
2. **Each reviewer writes** `docs/internal/reviews/<wave>-review-round<N>-<reviewer>.md` with finding IDs + severity (CRIT/MAJOR/MINOR/OBS).
3. **Triage:** CRIT/MAJOR are blockers. MINOR/OBS are accumulated.
4. **Fix-everything-in-this-wave:** apply all CRIT/MAJOR fixes (one stacked commit per round: `fix(adr-051-rev4): review-round<N>-<wave> corrections`).
5. **Re-run** the 6 reviewers on the corrected diff until CRIT/MAJOR = 0 in this wave.
6. **Per-wave convergence threshold = 3 rounds** (F-L4-2 correction — global threshold was too lax; a wave that fails to converge in 3 rounds is escalated to the operator for triage).
7. **Holistic final-pass:** the 7th holistic reviewer is the LAST one to clear; cleared by either acceptance or by triggering a fix that returns the wave to step 4.

The gate loops per-wave. The acceptance gate at end of Wave 4 runs the gate over the **entire** `sendfile-fix` diff.

---

## 5. CI / verification

- **Per-slice verification (the implementor does):** scoped Go tests + scoped lint + `go build ./cmd/omnipus` — all must pass before commit. No full-suite runs in the pod (CLAUDE.md: "Never run the full Go test suite... in this ephemeral, resource-constrained devpod"). CI is the authority for the full suite.
- **Wave-level verification:** each wave's commits pushed (no force-push); trigger `gh workflow run pr.yml --ref sendfile-fix` for the wave; check the run is green.
- **Acceptance verification (F-L6-1 correction):** trigger one final CI run on the entire sendfile-fix branch; observe green; **commit the green run URL into `docs/internal/uat/ADR-051-rev4-ci-evidence.md`** (run URL + commit hash + per-job status + timestamp). Cross-check with live-gateway Playwright runs for the matrix §5 stress tests + every SC's behavior; commit into `docs/internal/uat/ADR-051-rev4-sc-observations.md` (per-SC observation format: `## SC-NNN: <name>\n- Verification: <tool output>\n- Result: PASS/FAIL\n- Timestamp: <ts>`).

---

## 6. Risk register (updated per corrections)

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| 1 | Wave 2 T7 (security-lead) sanitizer is too aggressive and breaks legitimate filenames | medium | medium | FR-023a caps length to ≤128 chars + strips control chars; the test `TestContentInjection_SanitizesFilename_PromptInjection` (spec §test 43) uses a known-passing fixture + the reviewer's `silent-failure-hunter` will catch if legitimate text is mangled |
| 2 | The 6-reviewer gate never converges (reviewers disagree) | low | high | Re-run with explicit "no new findings, only re-confirm prior fix" prompt; per-wave threshold = 3 rounds; after that, escalate to operator |
| 3 | "Pre-existing branch debt" may not actually be failing on the branch | ~~high~~ ~~high~~ | medium | **RESOLVED (F-L5-2 correction):** T13 first runs CI, observes the actual failing jobs, then triages. No pre-planned list; the list is historical candidate, not authoritative. |
| 4 | Cross-workspace resolver change breaks channel receivers (the 13+ call-sites) | medium | high | 8 channels each tested via the legacy nilable param path; existing channel integration tests catch regressions; `TestResolver_LegacyCallSites_UnaffectedByNilableContextParam` covers it. Slice G gets an extra STRIDE pass (F-L2-2 correction). |
| 5 | Hard-reset during ralph loop / auto-continuation kills in-flight writes | low | medium | The work is checkpointed by stacked commits; if killed, resumed from last commit |
| 6 | Token budget hit mid-pipeline | ~~high~~ ~~high~~ | ~~high~~ | **RESOLVED:** operator removed the budget cap for this goal (token_budget=null). |
| 7 | "Command Center screen retired" + other unrelated deletions regress (CLAUDE.md retired surfaces list); we touched chat + workspace media but might accidentally un-reconcile a deleted CommandCenter | low | medium | Don't import from `src/components/command-center/`; the reviewer's `silent-failure-hunter` catches if a transitive import appears |
| 8 | LLM provider phrasings change between this plan and execution (the Gemini/z.ai phrases we pinned in the matrix might not be current in 7 days) | low | low | FR-024 freeze-gate: Slice C produces `provider-media-format-support-2026-07.md` re-validation report and the seed commit message references it |
| 9 | The `general` agent context window chokes on the 1253-line spec | medium | medium | Pass slice-relevant spec subsections + the slice's TDD rows explicitly in the per-slice prompt; don't make the agent re-read the whole spec |
| 10 | Stacked-commit strategy hides partial commits from PR reviewers | ~~high~~ ~~medium~~ | medium | **RESOLVED (F-L5-3 correction):** a single draft PR is opened at end of Wave 4 acceptance for the whole diff. The 6-reviewer gate is the per-wave code review; the draft PR is the final human review surface. |

---

## 7. Decisions (locked per corrections — formerly §7 "open decisions")

The following are now **decided**, not open:

1. **Slice C package location**: `pkg/providers/capabilities/` (NEW) — NOT `pkg/providers/catalog/` (which already exists with provider metadata). F-L2-3 MINOR resolved.
2. **Manifest refcount design**: SEPARATE counter from `pathStates.refCount`, distinct semantics (deferred 30d vs immediate-delete-at-zero). Spec FR-007a updated in round-2 corrections (R2-M1). F-L8-1 MINOR resolved.
3. **Step-5 work/ size limit**: per-file cap remains `maxUploadFileSize` (100 MB). **No separate work-dir cap in this release** — the operator's "no quota" stance (ADR §Operator decision 1) carries; the two-mechanism split (user uploads = persistent, agent-generated = ephemeral) bounds the flood vector; orphan-GC covers stale refs. F-L8-1 MINOR + N-m2 resolved by aligning with the spec/ADR rather than inventing a new cap.
4. **Slice C freeze-gate artifact**: `docs/internal/research/provider-media-format-support-2026-07.md` is produced by Slice C and referenced in the seed commit. F-L8-1 MINOR resolved.
5. **Slice G channel-by-channel threading verification**: T6 enumerates each of the 8 channels in the test matrix and verifies the nilable param threading per channel. F-L8-1 MINOR resolved.
6. **Slice E classifier substrings for new codes**: `CodeToolArgs` detected by body-substring `"invalid tool arguments"`; `CodeSchema` detected by body-substring `"schema validation"`. **Status/class backstop:** the existing `classifyByHTTPStatus` already handles status-code-driven classification (`401`/`403`/`413` → `CodeProviderRejected`, etc.) — for any 4xx whose body phrasing doesn't match the new codes' substrings, the status-code path returns the appropriate non-media code and step-4 is suppressed regardless of phrasing. The body-substring match is a SECONDARY detector; the status-code path is the PRIMARY gate. If a new provider emits an unrecognized phrasing for a status code the existing classifier already covers (e.g. `400`), the classifier returns `CodeUnknown` (inconclusive) and step-4's outcome-based fallback fires — which is the intended outcome per FR-017. F-L8-2 MINOR resolved.
7. **PR strategy**: stacked commits on `sendfile-fix`; ONE draft PR opened at end of Wave 4 acceptance. F-L5-3 MAJOR resolved.
8. **Per-SC observation log format** (F-L6-2 MINOR resolution): `## SC-NNN: <name>\n- Verification: <tool output>\n- File path evidence: <path or cmd/exit>\n- Operator/agent: <who ran it>\n- Result: PASS/FAIL\n- Timestamp: <ts>`. Each observation references either a file path under `docs/internal/uat/` (commit-evidence, persona-log entries) or a recorded shell command + exit code for reproducibility.
9. **Slice B blast-radius mitigation**: B lands as TWO stacked commits (B1-core, B2-audit+namespace). F-L2-1 MAJOR resolved.

---

*End of plan v3 — accepted.*

## Goal active (no budget cap). Stage 1 plan written, Stage 2 review produced (REVISE). Now applying the 11 MAJOR + 7 MINOR + 5 OBS corrections to the plan (this v2), then will re-run the critical review once more for the final plan gate, then proceed to Stage 3 implementation.