# Final-Pass Review — ADR-051 Rev 4 Delivery Plan v2

**Review target:** `docs/internal/plans/ADR-051-rev4-delivery-plan.md` (v2)
**Prior review:** `docs/internal/plans/ADR-051-rev4-delivery-plan-review.md`
**Governing spec:** `docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md`
**Governing ADR:** `docs/internal/architecture/ADR-051-rev4-workspace-media-library-and-presentation-layer.md`
**Mode:** Stage 2, round 2; read-only on the plan
**Date:** 2026-07-22

## Executive Summary

V2 materially improves the plan: the Wave-1 test ownership rule, hard-constraint/build checks, Slice G security review, observed-CI debt triage, explicit PR strategy, and CI evidence artifact are concrete. However, two prior MAJOR findings remain only partially resolved, and v2 introduces a new load-bearing dependency defect: B2 is dispatched in parallel with B1 despite explicitly depending on B1's new API and manifest refcount. The plan therefore cannot proceed to Stage 3 as written.

**Verdict: REVISE.**

**Prior-finding status:** 17 PASS · 6 PARTIAL · 0 FAIL.

- MAJOR: 9 PASS · 2 PARTIAL · 0 FAIL
- MINOR: 4 PASS · 3 PARTIAL · 0 FAIL
- OBSERVATION: 4 PASS · 1 PARTIAL · 0 FAIL

**New findings:** 1 MAJOR · 3 MINOR.

## Prior Findings

| ID | Severity | Status | V2 evidence and assessment |
|---|---|---|---|
| F-L1-1 | MAJOR | PASS | Lines 80 and 131 prohibit modifying the named existing tests in Wave 1; line 101 assigns their rewrite to T9. Concrete ownership eliminates the double-write. |
| F-L1-2 | MAJOR | PASS | Lines 140–146 enumerate Hard Constraints #1/#3/#4/#5 as requested. |
| F-L1-3 | MAJOR | PASS | Lines 136 and 182 require tagged `go build ./cmd/omnipus/` before commit. |
| F-L2-1 | MAJOR | PARTIAL | B is split into B1/B2 at lines 41–42 and 72–73, but the correction is internally unsafe: line 42 says B2 depends on B1's manifest refcount, while lines 67–78 dispatch and land them in the same parallel wave. No merge/rebase/API handoff protocol makes that dependency executable. See N-M1. |
| F-L2-2 | MAJOR | PASS | Lines 47 and 94 add an explicit STRIDE pass for Slice G. Line 89 also requires named channel verification. |
| F-L3-1 | MAJOR | PASS | Lines 80/101/131 concretely assign all existing resolver/normalization test rewrites to T9 and prohibit Wave-1 edits. |
| F-L3-2 | MAJOR | PASS | Line 102 makes T10 a gap-fill task: only named tests not already implemented by Waves 1–2 are added. |
| F-L4-1 | MAJOR | PARTIAL | Lines 25–30 openly disclose that named leads/architect are unavailable and add compensating context. However, line 25 says the holistic reviewer carries the spec's Decision sections, while the actual dispatch definition at lines 161–168 only says “fresh-context holistic” and does not require those sections or grill findings to be supplied—especially for the Wave-3 orchestrator. The compensating control is asserted but not encoded in the executable prompt. |
| F-L5-1 | MAJOR | PASS | Line 197 records the operator's removal of the goal budget cap (`token_budget=null`), eliminating the disputed 20M limit. |
| F-L5-2 | MAJOR | PASS | Lines 51–55 and 113 require observing CI first and treating the old list only as candidates. |
| F-L5-3 | MAJOR | PASS | Lines 116, 201, and 215 choose one explicit strategy: stacked commits and one draft PR after Wave-4 acceptance. |
| F-L6-1 | MAJOR | PASS | Lines 114 and 184 require a final workflow invocation and a committed evidence artifact containing run URL, commit hash, per-job status, and timestamp. |
| F-L7-1 | MINOR | PASS | Lines 140–146 include the previously omitted hard constraints. |
| F-L7-2 | MINOR | PASS | Lines 134–136 require scoped tests, empty gofmt output, lint with no findings, and a successful build. |
| F-L7-3 | MINOR | PASS | Line 153 gives Wave 0 a distinct generator-only prompt and forbids hand-editing generated types. |
| F-L8-1 | MINOR | PARTIAL | Lines 209–213 settle package location, separate refcount, freeze artifact, and channel verification. The work-dir decision at line 211 introduces an operator-configurable per-workspace cap that the spec and ADR explicitly do not define; no config schema, enforcement owner, or test is assigned. See N-m2. |
| F-L8-2 | MINOR | PARTIAL | Line 214 locks two body substrings, but the requested status/class backstop is not defined. The plan relies on the existing exclusion set without specifying how `CodeToolArgs`/`CodeSchema` are recognized when provider phrasing differs. |
| F-L6-2 | MINOR | PARTIAL | Lines 115, 184, and 216 define SC ID/name, tool output, result, and timestamp, but omit the requested file-path evidence and operator/agent identity. Reproducibility remains weaker than requested. |
| F-L2-3 | MINOR | PASS | Lines 43, 74, and 209 explicitly select new `pkg/providers/capabilities/` and distinguish existing `pkg/providers/catalog/`. |
| F-L4-2 | MINOR | PASS | Lines 82 and 173 make the three-round threshold explicitly per-wave. |
| F-L5-4 | OBSERVATION | PASS | Line 198 preserves the retired-surface rule. This was observational and did not require a new lint rule. |
| F-L5-5 | OBSERVATION | PASS | Lines 43, 102, 199, and 212 schedule and name the dated freeze-gate artifact. |
| F-L4-3 | OBSERVATION | PASS | Lines 30 and 165 give `pr-test-analyzer` first pass and longest context. |
| F-L3-3 | OBSERVATION | PARTIAL | Lines 115 and 184 add per-SC live observations, but line 104 still describes a reviewer gate as “full chain tested”; reviewers do not execute behavior. Behavioral enforcement exists later, but the plan retains misleading gate semantics. |
| F-L1-4 | OBSERVATION | PASS | Lines 90 and 101 place Slice D's new work in `pkg/agent/media_present.go`/offload paths while G owns resolver threading; no new evidence makes their Wave-2 parallelism unsafe. |

## New Findings

| ID | Severity | Section | Finding | Required correction |
|---|---|---|---|---|
| N-M1 | MAJOR | Lines 42, 67–78 | **B2 is parallelized despite a declared hard dependency on B1.** B2 “depends on B1's manifest refcount existing,” yet T1 and T2 run concurrently and produce separate stacked commits. T2 cannot reliably compile or test against an API that does not exist in its starting tree, and parallel commits touching `pkg/workspace/`/integration surfaces create reconciliation risk. | Make B1 sequentially precede B2 (e.g. Wave 1a → Wave 1b), or define a concrete shared-base/API-stub handoff and merge protocol. The simplest correction is sequential B1 then B2. |
| N-m1 | MINOR | Lines 41–42, 217 | **B1/B2 ownership is contradictory.** B1's coupling text says it owns the cascade-delete hook into `pkg/workspace/`, while B2 owns FR-009 cascade-delete wire-up; line 217 calls B2 “audit+namespace,” although namespace/storage are B1 concerns. | Assign the hook and namespace unambiguously to one slice and make names, FR mapping, and commit descriptions agree. |
| N-m2 | MINOR | Lines 211 vs governing spec/ADR | **V2 invents an unplanned workspace work-dir quota/config.** The governing spec says no storage quota and describes only the existing 100 MB per-file cap; the ADR records disc-as-limit. “Operator-configurable per-workspace `work/` cap” has no contract/config/task/test and risks scope creep or contradiction. | Either remove the new cap from this delivery plan and state “no separate work-dir cap in this release,” or amend the governing ADR/spec before planning implementation. |
| N-m3 | MINOR | Lines 101, 140–146 | **Prompt hard-constraint text is backend-only but is declared standard for every slice.** H is frontend-only, yet the template requires `pkg/<your-package>` Go tests/lint/build and discusses security-feature memory/kernel degradation. | Define frontend and backend prompt variants; H should run typecheck/vitest/build, while Go slices retain the Go gate. |

## Rejected New-Defect Hypotheses

1. **CI evidence remained non-reproducible:** rejected. V2 records workflow invocation, URL, HEAD hash, per-job status, and timestamp.
2. **Wave-1 tests still collide with the orchestrator rewrite:** rejected. The prohibition and T9 ownership are explicit in both the wave rule and prompt template.
3. **Slice G still lacks security review:** rejected. V2 adds STRIDE review and named channel verification.

## Verdict

**REVISE.** F-L2-1 and F-L4-1 remain PARTIAL, and N-M1 is a new MAJOR. Per the review rule, Stage 3 must not begin until B1/B2 ordering is made executable and the remaining MAJOR compensating-control gap is encoded in the actual reviewer prompt.
