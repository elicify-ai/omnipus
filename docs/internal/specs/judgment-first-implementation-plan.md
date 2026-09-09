# Judgment-first + AskUserQuestion — implementation plan (dependency graph, parallel waves)

- **Date:** 2026-09-05. Sources: ADR-074 r4 (Accepted, D8 rollout), `judgment-first-criteria-spec.md` v3, `askuserquestion-tool-spec.md` v3 (complete, zero open sign-offs).
- **Delivery mode (operator-directed):** parallel workflows, up to 6 parallel agents per wave, worktree isolation for concurrently-mutating streams; contract-touching work serialized within one stream (regenerated artifacts cannot merge).

## Workstreams

| ID | Work | Spec | Files (primary) | Depends on |
|----|------|------|-----------------|------------|
| W1 | D3a: 4 tool schemas widen to full enum; both parsers decode `behavior` (pointer semantics) | JF FR-003 | pkg/tools/task.go, plan.go, plan_correct.go; pkg/sysagent/tools/task.go | — |
| W2 | D2+D3b: `InferCriterionKind` (single impl, pre-gate), `AcceptanceCriterionInput.yaml`, gateway conversions, required-relaxation, zod atomic | JF FR-001/2/4 | pkg/task/criterion.go, contracts/, pkg/gateway/rest_tasks.go+rest_plans.go, generated | W1 (same parser files) |
| W3 | D7: `evidence_quote` — schema (maxLength 500) + parser capture + CriterionVerdict + fixtures | JF FR-010 | contracts/CriterionVerdict.yaml, pkg/agent/judge.go, pkg/task/verdict.go, generated | contracts-serial after W2 |
| W4 | Goal wire surface: criteria breakdown (additive `GoalStatusFrame` field or sibling), `$ref AcceptanceCriterion` | JF FR-011 wire | contracts/, asyncapi inline copy, generated | contracts-serial after W3 |
| W5 | D4: `define-done` embedded skill; 3-site Constraint-#6 seeding; marker-keyed migration; tool-description directives | JF FR-008/9 | pkg/skills/embedded/define-done/, pkg/coreagent/core.go, pkg/config/defaults.go | — |
| W6 | D4a: goal two-phase confirm for prose, bounded LLM compile w/ skill injection, repair-then-fallback, admission-before-compile, parked-turn goal gate | JF FR-006/7 | pkg/agent/goal_loop.go, goal_compile.go | W5 (skill file); AQ tool optional (chat fallback sanctioned) |
| W7 | D1: judge input reorder (extraContext→criteria→diff→window→machine→claim), back-reference rewrites, NEW claim-last guard | JF FR-005 | pkg/agent/judge.go (buildJudgeUserContent) | — |
| W8 | D5 UI: editor prose-first+behavior+chips; goal card criteria-first; verdict reason promotion+quote; `CriteriaBreakdown` shared component | JF FR-012, D5 | src/components/... | W2 (Input types), W3 (quote), W4 (goal wire) |
| W9a | AskUserQuestion backend: tool (validation, park-time stub, ParksTurn), pending registry+session-meta, timers, resume message, owner-only+liveness gates, seeding | AQ FR-1..6,9,11,12 | pkg/tools/, pkg/agent/, pkg/gateway/, pkg/coreagent seeds | — (frames stubbed until W9b) |
| W9b | AskUserQuestion wire+SPA: 2 frames (dual/triple-copy), snapshot field, zod, card component per mock, composer lock, collapsed record | AQ FR-7,10; §3 | contracts/, asyncapi, src/ | contracts-serial after W4; W9a |
| W10 | Catalog time-bomb: pin `nowFn` in `mustCatalog`/`bootEmbedded` (+audit package) | CI green prereq | pkg/providers/catalog/*_test.go | — |

**Contract-serialization rule:** W2 → W3 → W4 → W9b regenerate `pkg/api/generated` + `src/lib/api/generated` — they run in ONE stream, in that order, each an atomic spec+gen commit.

## Waves

- **Wave 1 (6 parallel agents, worktrees):** A1=W1→W2 (criteria stream incl. its contract step), A2=W7, A3=W10, A4=W5, A5=W9a, A6=W6.
- **Wave 2 (after wave-1 merge):** B1=W3→W4→W9b contracts+frames (serial stream), B2=W8 UI (starts on W2 types; finishes after B1), B3=integration/wire-up + test sweep (JF tests 1-22, AQ tests 1-15).
- **Wave 3 (goal steps 3-5):** /code-review on the entire diff → fix all findings → CI green (incl. W10) → Playwright-MCP UAT.

**Merge discipline:** each wave-1 agent works in its own worktree/branch off `release/v0.1.1`; the lead merges serially (A3, A2, A4, A1, A5, A6 — smallest/least-conflicting first), resolving conflicts and re-running scoped tests at each merge; contracts regenerate once per contract commit only in the serial stream.
