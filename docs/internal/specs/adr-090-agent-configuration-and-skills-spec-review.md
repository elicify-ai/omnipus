# Agent configuration and skills — initial grill-spec review

Date: 2026-09-17. Independent read-only reviewer applied grill-spec against ADR-090 and source. Initial verdict REVISE (0 critical, 1 major, 1 minor). Both corrections were reread and closed; **final verdict PASS** for this bounded specification review. Runtime implementation and test execution remain pending.

| ID | Severity | Lenses | Finding | Corrected sections |
|---|---|---|---|---|
| A1 | Major | Inconsistency, correctness, privilege boundaries | Ordinary role defaults were described as immutable even after authorized capability edits. | US-3 AS3, FR-009, BDD-14 and D17 now distinguish retained Deny, user-removed Deny under global Allow, and global Ask/Deny; ADR §5 clarified the same. Hidden scopes and GP helper identity remain hard boundaries. |
| A2 | Minor | Ambiguity, feasibility | Caller validation required startup to reject protected fields it must seed. | FR-002 separates caller-facing checks from trusted, non-caller-selectable seeding under FR-001; mutable user values are preserved. |

## Structural and test coverage

All 12 functional requirements and 19 BDD scenarios have planned test links; story acceptance scenarios are represented. Omission/empty/null, mixed-field rejection, conflict timing, interrupted mutations, publication failures, global policies, document dependency recovery and release provenance are addressed. Existing names inspected match the source; new deferred management tools are explicitly identified as new. Planned tests are not executed tests.

## Eight-lens and threat summary

Ambiguity and inconsistency findings above are closed. Completeness includes partial-save and activation failure paths. Feasibility follows existing stores, locks and provider/runtime limits. Privilege boundaries retain hidden identities, global strictest-wins and management-versus-execution separation. Operability distinguishes saved/active state and actual dependency availability. Correctness includes explicit field presence and override removal. No additional overcomplexity defect was found: expected-state digests detect stale writes and are not approval tokens or a cross-resource transaction framework.

No new founder decision was required. The later independent Claude Code Opus review remains a separate gate.
