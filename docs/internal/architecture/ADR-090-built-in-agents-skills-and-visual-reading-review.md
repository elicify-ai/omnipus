# ADR-090 — grill-spec review and dispositions

Date: 2026-09-17. Independent read-only reviewer applied grill-spec and its eight lenses. Initial verdict REVISE: 0 critical, 2 major, 1 minor. Author corrected every finding; closure reread confirmed all three resolved. **Final initial-review verdict: PASS.** No runtime code or tests were run.

| ID | Severity | Lens | Finding | Correction |
|---|---|---|---|---|
| ADR-MAJ-001 | Major | Ambiguity / inconsistency / privilege boundary | An omitted role override inherits the global ceiling, so a dash in the matrix cannot mean mere omission. | §5 requires deliberately authored sparse Deny entries for role exclusions and explicit empty/disabled connector assignments where omission inherits. §10 tests global bash Allow with fresh Jim execution denied. |
| ADR-MAJ-002 | Major | Completeness / correctness | A preliminary re-read does not prevent a competing write before mutation. | §5.2 checks expected state inside existing resource locks; conflict rejects that resource, earlier results remain accurately reported. §10 covers the interleaving. No cross-resource transaction framework. |
| ADR-MIN-001 | Minor | Ambiguity | Chosen creation type could be mistaken for permission to create engine agents. | §2.6 preserves supported user-creatable runtime types and excludes engine-owned Judge/Supervisor identities from custom creation. |

## Structural and testability assessment

Goals, scope exclusions, source distinction, dependencies, and measurable workflow acceptance are present. All three findings are engineering clarifications within confirmed decisions; no founder question is required. The specifications must carry the inherited-permission negative case, write-interleaving case, and hidden-type create rejection through tool/API paths, not just UI controls.

## Eight-lens and threat assessment

Ambiguity/inconsistency and privilege escalation are addressed by explicit default override semantics. Completeness/correctness and concurrent tampering are addressed by atomic per-resource expected-state checks. Hidden engine identity boundaries are explicit. No further infeasibility defect was found: provider media corrections and actual dependency setup are required, not assumed. Operational limitations, partial application, persistence, audit hygiene, and media failures are covered. No additional complexity was recommended: existing readers, media, policy, and configuration infrastructure are retained. The review found no demonstrated generic-file prompt-write bypass and did not invent one.

Review outcome applies to the ADR, not implemented behavior. Subsequent specification and Claude Code Opus findings are recorded separately.
