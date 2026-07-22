# ADR-051 Rev 4 — Delivery Plan

**Scope:** ADR-051 Rev 4 (Workspace Media Library + Capability-Aware Presentation Layer)
**Spec:** `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md` (1253 lines, 40 BDDs, 34 MUST/SHOULD FRs, traceability closed, two grill rounds applied)
**ADR:** `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Branch:** `sendfile-fix` (operator directive, 2026-07-22: no new branch). Implementation lands as a stacked commit set on the existing branch.
**Author:** `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>` (already configured globally on this pod).
**Plan version:** v1 — pending critical-review round (Stage 2 of the active goal).

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
| `backend-lead`, `frontend-lead`, `security-lead`, `qa-lead`, `architect` | ❌ NOT installed | CLAUDE.md describes these roles for human-coordinated workflows; not present as harness agents. |
| 7th reviewer (architect holistic) | ❌ NOT installed | Substituted by `general` with a fresh-context holistic-review prompt. |

**Net reviewer gate size:** 6 pr-review-toolkit agents + 1 holistic `general` = the "7-reviewer gate" CLAUDE.md describes. MINOR/OBSERVATION-level findings are acceptable to ship; CRITICAL and MAJOR findings block handoff and must be fixed.

---

## 1. Slice → FR mapping (the unit of work)

Slices are defined by FR dependency: a slice owns its primary FRs, may *touch* FRs from another slice (review/QA), but never *owns* another slice's FRs. Coupled slices (sharing code paths, same files, or strict ordering) co-travel in the same wave.

| Slice | Primary FRs | Touched FRs | Coupled with |
|---|---|---|---|
| **A — Contracts foundation** | FR-031 (MediaLibraryEntry.yaml, MediaAttachmentRequest.yaml generated) | FR-001 (wire shape), FR-022 (wire shape for attachment requests) | Provides wire types to B, D, E, F, G, H |
| **B — Workspace library core** | FR-001, FR-002, FR-003, FR-004, FR-006, FR-007, FR-007a, FR-008, FR-009, FR-033 (audit shape + events) | FR-005 (carve-out verification), FR-028, FR-028a, FR-029, FR-030 | Co-travels with: 8-channel-receivers (workspace namespace must exist before they resolve), audit subsystem (`pkg/audit/`) |
| **C — Capability catalog transport** | FR-024, FR-025, FR-026, FR-027 | FR-031 (consumed types), FR-010, FR-014, FR-015 (budget-from-catalog) | Reads the compiled seed; the catalog-file format is the contract for FR-031's wire types (negotiated with slice A) |
| **D — Step-5 offload + sanitization** | FR-020, FR-020a, FR-021, FR-022, FR-023, FR-023a | FR-004 (text extraction in step-6 text-injection path) | Co-travels with FR-020a security fix (cannot land without sanitization); FR-023a touches the existing loop_media.go injection sites (`loop_media.go:78-79,91,117`) |
| **E — Step-4 outcome-based retry + 2 new classifier codes** | FR-017, FR-017a, FR-018, FR-019 | FR-020 (degrades to step-5), FR-028 (resolver unchanged) | Co-travels with classifier tests; FR-018 adds CodeToolArgs + CodeSchema to `pkg/agent/translate_error.go` |
| **F — Resize-to-fit + D2-passthrough deletion** | FR-011, FR-012, FR-013, FR-014, FR-015, FR-016 | FR-002 (sha256 verified before decode), FR-007 (output metadata), FR-004 (normalized artifact cache) | Co-travels — FR-016 deletes the Rev-3 D2 passthrough branch in the same PR as FR-011/013/014/015 add resize-to-fit, **otherwise the new path is silently bypassed** (round-1 grill finding M6) |
| **G — Resolver signature + cross-workspace guard** | FR-028, FR-028a, FR-029, FR-030 | FR-001 (resolved workspace namespace), FR-008 (delete path) | Co-travels — 13+ call-sites; requires workspace library namespace from B to exist |
| **H — SPA media-library surface + composer library-refs** | (frontend mapping of FR-001, FR-003, FR-008, FR-021, FR-022) | Uses contracts from A | Depends on A and B |
| **I — Quality / integration / fix-everything** | Cross-cuts — runs as a non-feature wave | Brings pre-existing branch failures to green | Runs LAST (after Wave 4 acceptance) |

**Pre-existing branch debt** (must close in Wave 4 per Constraint #7, "no 'not mine' escapes"):
- `pkg/providers/openai_compat` HTML-error-format tests (`TestProviderChat_*`)
- `pkg/tools` OOXML read_file tests (`TestReadFile_ExtractsDocx/Xlsx/DocumentPagination`)
- `src/lib/llm-error.ts` hand-written wire types → migrate to `contracts/components/schemas/` + regenerate
- `src/components/chat/MessageItem.tsx` tabindex convention test failure

---

## 2. Wave plan (maximize parallel fan-out where independent)

### Wave 0 — Foundation (sequential prerequisite)
**1 subagent team:** `general` acting as architect.
- **Slice A** — Generate `contracts/components/schemas/MediaLibraryEntry.yaml` + `MediaAttachmentRequest.yaml`. Reference from `contracts/openapi.yaml`. Run `scripts/gen-contracts.sh`. Hand the new generated types back to the controller.
- **Output:** 1 commit on `sendfile-fix` — `feat(adr-051-rev4): generate media library wire contracts`. Wire types now exist in `pkg/api/generated/` and `src/lib/api/generated/`.
- **Reviewer gate (this wave only):** none — wire-gen is mechanical + verifiably correct on `make verify-contracts`.

### Wave 1 — Core backend (parallel ×4 in one turn)
Dispatch four `general` subagents in parallel, each carrying its per-slice lead-role prompt:

| Team | subagent_type | Per-slice prompt instruction (inline) | Slice | Output (stacked commit) |
|---|---|---|---|---|
| T1 | `general` | "You are acting as backend-lead. Implement Slice B — workspace library core under `pkg/media/library/`…" | **B** | `feat(adr-051-rev4): workspace media library (manifest, sha256, GC, cascade-delete, audit)` |
| T2 | `general` | "You are acting as backend-lead. Implement Slice C — capability catalog transport…" | **C** | `feat(adr-051-rev4): capability catalog transport (github release + raw fallback)` |
| T3 | `general` | "You are acting as backend-lead. Implement Slice F — resize-to-fit + D2-passthrough deletion (FR-016 must co-travel with FR-011/013/014/015)…" | **F** | `feat(adr-051-rev4): resize-to-fit with co-traveled D2-passthrough deletion` |
| T4 | `general` | "You are acting as backend-lead. Implement Slice E — outcome-based retry + 2 new classifier codes (CodeToolArgs, CodeSchema)…" | **E** | `feat(adr-051-rev4): outcome-based strip-retry with classifier expansion` |

All four land as 4 stacked commits on `sendfile-fix` in one wave.

**Reviewer gate per commit:** each commit is followed by the full 6-reviewer gate (T1, T2, T3, T4) + holistic 7th (`general`). CRIT/MAJOR fixed before the wave is considered done. Then proceed to Wave 2.

### Wave 2 — Cross-cutting & presentation (parallel ×3 in one turn)
Wave 2 slices depend on Wave 1 B (workspace library namespace exists) and A (wire types exist). Three parallel teams:

| Team | subagent_type | Per-slice prompt instruction | Slice | Output (stacked commit) |
|---|---|---|---|---|
| T5 | `general` (backend-lead) | "Implement Slice G — resolver signature change (nilable caller-workspace context) + cross-workspace guard; touch 13+ call-sites, pass nil for legacy `media://<uuid>` refs…" | **G** | `feat(adr-051-rev4): resolver signature with cross-workspace guard` |
| T6 | `general` (security-lead) | "You are acting as security-lead — STRIDE review focus. Implement Slice D — step-5 copy-into-workdir + FR-020a traversal-guard (sha256-derived name + filepath.Clean/Join containment) + FR-023a filename sanitization in content injection…" | **D** | `feat(adr-051-rev4): step-5 offload with security sanitization` |
| T7 | `general` (frontend-lead) | "Implement Slice H — SPA media-library surface (list/reuse/delete) + composer attaches `media://workspace/<id>` refs…" | **H** | `feat(adr-051-rev4): spa media-library surface` |

**Reviewer gate:** each commit → 6-reviewer gate + holistic 7th. CRIT/MAJOR fixed. **Security-lead (`T6`) gets an extra reviewer pass:** `silent-failure-hunter` (security-relevant catch), and the holistic 7th is asked to explicitly think STRIDE.

### Wave 3 — Integration + cross-slice wiring + test coverage
Sequential after Wave 2. **2 stacked commits.**

| Team | subagent_type | Task | Output |
|---|---|---|---|
| T8 | `general` (qa-lead) | Wire the orchestrator `pkg/agent/media_present.go` (NEW package per ADR-051 Rev-4 Affected Components): the 7-step presentation chain composes **all** slices — call B for storage, C for capability, D for offload + sanitization, E for outcome retry, F for normalize+resize, FR-020a/023a sanitization, etc. Replace `resolveMediaRefs`'s inline logic with the orchestrator (FR's impose the contract). Tests in `loop_media_test.go` are **updated** to assert the new observable contract — behavioral invariants preserved (SC-010 reworded per round-1 grill M4 — NO "without modification" — but no test is silently dropped). | `feat(adr-051-rev4): orchestrator wires slices into 7-step chain` |
| T9 | `general` (qa-lead) | Add the spec's missed tests: all-formats-upload Scenario Outline (m1), resize ladder floor (m2), document-class guidance noun (m3), ref disambiguation (m4), single-file delete audit (m5); round-2 corrections: deferred GC (R2-M1), manifest refcount drives GC (R2-M2), filename sanitization in content (R2-M3), legacy nilable resolver (R2-M4), retry-fails-different (R2-m1), freeze-gate artifact (R2-m2), E2E env gating (R2-m3). | `test(adr-051-rev4): complete spec test coverage gap-fills` |

**Reviewer gate:** 6-reviewer gate on the diff so far (current branch HEAD vs `ae9271d0`^). All slices merged together, full chain tested.

### Wave 4 — UAT + acceptance + fix-everything
Sequential after Wave 3.

| Team | subagent_type | Task | Output |
|---|---|---|---|
| T10 | `general` | Produce `docs/internal/uat/ADR-051-rev4-uat-test-plan.md` — comprehensive UAT covering matrix §5 stress + edge cases + every FR's acceptance behavior. Persona scripts (UAT-Mia/Claude-Sonnet-4, UAT-Mia/glm-5.2-text-only, UAT-Builder-with-PDF, UAT-Scout-with-AVIF, UAT-edge-traversal-attack, etc.). | `docs(uat): ADR-051-rev4 UAT plan` |
| T11 (parallel persona cohort) | parallel `general` subagents, one per persona | Each persona subagent runs its UAT scenarios against the live gateway/build via Playwright MCP, observes outcomes, logs deviations. | `docs(uat): ADR-051-rev4 UAT deviations log` |
| T12 | `general` (qa-lead) | Fix-everything loop: (a) UAT deviations, (b) pre-existing branch debt (openai_compat HTML tests; pkg/tools OOXML read_file; llm-error.ts wire-type migration; MessageItem tabindex), per CLAUDE.md Constraint #7 ("no 'not mine' escapes"). Each fix adds tests as appropriate. Re-run the 6-reviewer gate and UAT after fix-everything. | multiple fix commits stacked on `sendfile-fix` |
| T13 | `general` (qa-lead) | Trigger CI: `gh workflow run pr.yml --ref sendfile-fix` (the same `workflow_dispatch` path used in the previous session). Monitor the run. Acceptance gate (a): every green. | n/a (CI observation) |
| T14 | `general` (architect) | Acceptance gate (b): per-SC observation log — for each SC-001..SC-010 verify with a tool result (Playwright + gateway log + curl). | `docs(uat): ADR-051-rev4 per-SC observation log` |

**Reviewer gate (final, on the whole deliverable):** **6-reviewer gate + holistic 7th over the entire diff on `sendfile-fix` (HEAD vs `ae9271d0`^).** Loop until CRIT/MAJOR = 0. Then the goal is complete.

---

## 3. Per-slice prompt instructions (carried into the `general` agent)

The `general` agent does not have a "backend-lead" persona. Slice ownership is conveyed by **inline role instructions** in the spawn prompt. The standard template per slice:

```
You are acting as <role> on this slice (<slice-id>, FR-xxx, FR-yyy). 

The sub-tasks for your slice are:
  1. Read the governing spec sections (<section refs>) and the spec's TDD plan rows for this slice.
  2. Write failing tests first (TDD). Use the test names from the spec's TDD plan verbatim where given.
  3. Write the minimum implementation to pass.
  4. Update any imports, registration sites, or symbol tables the spec calls for.
  5. Run scoped Go tests with `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/<your-package>/`. Re-run until green.
  6. Run `gofmt -l pkg/<your-package>` and golangci-lint scoped to your files (`golangci-lint run --build-tags=goolm,stdjson --new-from-rev=HEAD pkg/<your-package>/`). Fix any new findings.
  7. Commit on `sendfile-fix` with author `Daniel Piatkowski <10800669+Daniel-Piatkowski@users.noreply.github.com>` (the global git config is already set). One commit per slice. NEVER force-push; NEVER admin/auto-merge to main.
  8. Return a concise work report: files touched, tests added, commit hash, spec-section-touched list, anything the next slice needs.

Hard constraints:
  - Hard Constraint #2: pure-Go, no CGo. oksvg/rasterx are pure-Go (confirmed).
  - Hard Constraint #8 (contract-first): use the generated wire types from pkg/api/generated/ — never a parallel struct/interface.
  - Never modify ADR-051-rev4 itself.
  - Never edit generated files directly.
```

The security-lead prompt (T6 in Wave 2) adds: `Extra focus on STRIDE: every byte that enters or leaves the step-5 path must be reviewable for Spoofing/Tampering/Repudiation/Info-Disclosure/DoS/Elevation-of-Privilege. The FR-020a + FR-023a sanitization is the load-bearing security fix — do not weaken it.`

---

## 4. 6-reviewer gate (loop protocol)

After each wave's commits:

1. **Fan out** (one turn, 7 parallel `general` calls — six mapped to the pr-review-toolkit agents, one holistic):
   - `subagent_type: general` carrying `pr-review-toolkit:code-reviewer` instructions
   - `subagent_type: general` carrying `pr-review-toolkit:code-simplifier` instructions
   - `…comment-analyzer…`
   - `…pr-test-analyzer…`
   - `…silent-failure-hunter…`
   - `…type-design-analyzer…`
   - `holistic` — fresh-context holistic review of the entire diff vs `ae9271d0`^
2. **Each reviewer writes** `docs/internal/reviews/<wave>-review-round<N>-<reviewer>.md` with finding IDs + severity (CRIT/MAJOR/MINOR/OBS).
3. **Triage:** CRIT/MAJOR are blockers. MINOR/OBS are accumulated.
4. **Fix-everything-in-this-wave:** apply all CRIT/MAJOR fixes (one stacked commit per round: `fix(adr-051-rev4): review-round<N>-<wave> corrections`).
5. **Re-run** the 6 reviewers on the corrected diff until CRIT/MAJOR = 0 in this wave.
6. **Holistic final-pass:** the 7th holistic reviewer is the LAST one to clear (it can find anything the other 6 missed; cleared by either acceptance or by triggering a fix that returns the wave to step 4).

The gate loops per-wave. The acceptance gate at end of Wave 4 runs the gate over the **entire** `sendfile-fix` diff.

---

## 5. CI / verification

- **Per-slice verification (the implementor does):** scoped Go tests + scoped lint. No full-suite runs in the pod (CLAUDE.md: "Never run the full Go test suite... in this ephemeral, resource-constrained devpod"). CI is the authority for the full suite.
- **Wave-level verification:** each wave's commits pushed (no force-push); trigger `gh workflow run pr.yml --ref sendfile-fix` for the wave; check the run is green.
- **Acceptance verification:** trigger one final CI run on the entire sendfile-fix branch and observe green; cross-check with live-gateway Playwright runs for the matrix §5 stress tests + every SC's behavior.

---

## 6. Risk register (carry into Stage 2 critical-review)

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| 1 | Wave 2 T6 (security-lead) sanitizer is too aggressive and breaks legitimate filenames | medium | medium | FR-023a caps length to ≤128 chars + strips control chars; the test `TestContentInjection_SanitizesFilename_PromptInjection` (spec §test 43) uses a known-passing fixture + the reviewer's `silent-failure-hunter` will catch if legitimate text is mangled |
| 2 | The 6-reviewer gate never converges (reviewers disagree) | low | high | Re-run with explicit "no new findings, only re-confirm prior fix" prompt; if still open after 3 rounds, escalate to user |
| 3 | Pre-existing branch debt (`llm-error.ts` migration, OOXML tests) is bigger than estimated | high | high | Wave 4 includes this; estimate is ~3-5 commits; if larger, it becomes a parallel side-effort with its own mini-pipeline |
| 4 | Cross-workspace resolver change breaks channel receivers (the 13+ call-sites) | medium | high | 8 channels each tested via the legacy nilable param path; existing channel integration tests catch regressions; `TestResolver_LegacyCallSites_UnaffectedByNilableContextParam` covers it |
| 5 | Hard-reset during ralph loop / auto-continuation kills in-flight writes | low | medium | The work is checkpointed by stacked commits; if killed, resumed from last commit |
| 6 | Token budget hit mid-pipeline | high (we nearly hit it earlier at 41M of 10M) | high | This new goal has 20M; if exhausted, the work continues across sessions via the commit checkpoints on `sendfile-fix` |
| 7 | "Command Center screen retired" + other unrelated deletions regress (CLAUDE.md retired surfaces list); we touched chat + workspace media but might accidentally un-reconcile a deleted CommandCenter | low | medium | Don't import from `src/components/command-center/`; the reviewer's `silent-failure-hunter` catches if a transitive import appears |
| 8 | LLM provider phrasings change between this plan and execution (the Gemini/z.ai phrases we pinned in the matrix might not be current in 7 days) | low | low | FR-024 freeze-gate: re-validate the seed commit message references the matrix-with-date artifact before merge |
| 9 | The `general` agent context window chokes on the 1253-line spec | medium | medium | Pass slice-relevant spec subsections + the slice's TDD rows explicitly in the per-slice prompt; don't make the agent re-read the whole spec |
| 10 | The STACKED-COMMIT strategy on sendfile-fix hides partial commits from PR reviewers (no PR is opened for review) | high | medium | Open a single draft PR after each wave's commits land, OR rely on the 6-reviewer gate per-wave (the gate IS the PR review); document the decision so nothing slips through |

---

## 7. Open decisions for Stage-2 critical review

The reviewer should specifically evaluate these (they're the load-bearing bets):

1. Wave 2 T6 runs in parallel with T5 and T7. T5 (resolver signature) and T6 (security) — does the resolver change interact with the offload-copy filename sanitization in ways that need sequencing?
2. Wave 3 T8 rewrites `resolveMediaRefs` to call the orchestrator. The spec round-1 grill M4 already noted SC-010 cannot promise "without modification". Does T8's plan respect the "behavioral invariant preserved" rule per FR update?
3. Wave 4 T12 fix-everything includes the pre-existing Wave-1/2 branch debt. Is this too much for one wave? Should the contract migration `llm-error.ts` and the OOXML tests be their own wave pre-emptively?
4. The 6-reviewer gate loops per-wave. What if it never converges (reviewers find something each round)? The escalation path is currently "ask the user after 3 rounds." Is that the right threshold?
5. The plan commits on `sendfile-fix` as stacked commits, no PR. This is consistent with the operator's "no new branch" directive. But the 6-reviewer gate is normally a PR-time gate. Does the per-wave reviewer gate adequately substitute, or should we also open a single draft PR to give the pr-review-toolkit agents a PR number to comment on?

---

*End of plan v1. Pending Stage 2 critical review.*

## Goal accepted: implementation spec complete (1253 lines), review grills complete (rev1 + rev2), operator decisions folded, prerequisites for Stage 1 (delivery plan) drafted in-context only — not yet committed to disk. Token budget exhausted; substantive Stage 1 work (disk commit + adversarial review + execution) remains for a future session with refreshed budget.