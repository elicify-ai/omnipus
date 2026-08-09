---
name: mechanism-not-property-defect-class
description: "Omnipus's recurring defect class — tests asserting the mechanism not the property — and the review technique that actually finds it"
metadata: 
  node_type: memory
  type: project
  originSessionId: 90a5dcd1-4156-4d4a-9727-bab20b4a9cf8
  modified: 2026-07-28T05:02:39.626Z
---

**The defect class.** On `feature/plan-swimlane-board` (ADR-055/056 epic, 2026-07-28)
at least **eight** controls shipped fully implemented, fully wired and fully tested
while being non-functional in production. Every one had the same shape: **two layers,
each correct, each tested against a fake of the other, and the join missing or gated
by a predicate that can never be true.**

Worked instances (all confirmed by tool result, all green in CI beforehand):
- `plan_correct` wired end-to-end but the tool admitted only `plansupervisor` while
  the engine admitted only the plan owner — and a System Agent can never *be* an owner.
- The supervision wake named the plan by **title**; `plan_correct` requires `plan_id`,
  and the supervisor is seeded exactly one tool so it cannot look the id up.
- `AppendCorrection` dispatched members on the supervisor's **turn** context, so every
  correction's work died at `turnCancel()`. Prior callers all passed `context.Background()`.
- `isMemberSuperseded` had **zero callers**; `gamingGuardEvidence` was called only from
  a test — so supersede never reached the judge.
- `CreatedByAgentID` had a sanctioned writer no call site used.
- A config key with a resolver nothing called; a kill switch whose reader had no wiring site.
- `toWirePlan` never emitted 5 of 29 contract fields.
- Origin predicate tested **emptiness** where it needed **internality**, so `cli` plans
  were dropped — and a test asserted that drop as correct.

**Why the tests passed:** each asserted that the mechanism ran (a map entry was written,
a dispatch was called, a prompt contained "UNMET"), never that the property held.

**The technique that found them.** Three earlier generic review rounds missed all of it.
What worked: briefing reviewers with **the specific defect shape plus 3–4 worked
instances from this very codebase**, and giving each a distinct lens rather than N
passes of the same sweep. Two concrete prompts that paid off repeatedly:
- *"What value must the world take for this predicate to be false, and can it ever take it?"*
- *"Who writes this field, who reads it, and is that path reachable in production?"*
  (then exclude `_test.go` from the caller set — a symbol whose only callers are tests
  IS the finding)

Also: require **mutation testing** of every fix (revert it, prove the test goes red,
restore) and demand the red output in the report. Agents caught their own vacuous
tests this way more than once.

**Related durable sub-pattern:** *a partial object meeting replace semantics* — hit 3×
here (plan bounds, agent MCP bindings, two latent sites). It stays invisible while the
client happens to send every field that exists, then arms itself the moment someone adds
a field, with no change at the write site. Four ad-hoc defences against it already exist
in the repo, each with a comment citing a prior incident — it wants a lint rule, not a
fifth patch.

Related: [[adr-053-unified-goal-plan-subagent]], [[adr-052-autonomous-plan-execution]]
