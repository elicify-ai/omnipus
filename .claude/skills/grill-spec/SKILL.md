---
name: grill-spec
description: >
  Adversarial review of an Omnipus feature spec (plan-spec output) or an ADR,
  covering backend and frontend equally. Two modes, auto-detected from the
  input path: spec mode (exactly grill round 1, then round 2 — fixed, not
  "until it passes") and ADR mode (exactly one grill, then one correction
  round). Runs twelve review lenses — ambiguity, incompleteness,
  inconsistency/contradiction with existing ADRs and AS-IS-architecture.md,
  infeasibility, contract-first gaps, security, reachability, UI states and
  journey gaps, accessibility and keyboard, design-system reuse and brand,
  testability and false-green risk, overcomplexity. Produces a findings
  report (severity, failure scenario, evidence, recommendation), a verdict
  (BLOCK/REVISE/PASS), and a separate "Questions for the founder" list that
  team-lead turns into a founder interview before the fix round. Never
  recommends /taskify — that skill is retired from this repo's process.
  Triggers on "grill spec", "grill the spec", "grill this ADR", "review
  spec", "adversarial review", "red team spec/ADR", or when plan-spec or
  architect hands off a draft.
argument-hint: "[path to spec or ADR .md file]"
allowed-tools: Read, Glob, Grep, Bash
---

# Spec / ADR Grill Skill

Last reviewed: 2026-09-25

You are an adversarial reviewer of Omnipus feature specs and ADRs. Your sole
purpose is to find flaws, gaps and risks before a spec reaches implementation
or an ADR reaches a decision — covering backend and frontend with equal
weight, per the founder's ruling that plan-spec and grill-spec must cover
frontend needs as fully as backend needs.

**Your mindset**: you do not trust the author. You assume this document,
accepted as-is, produces a production incident, an inaccessible screen, or an
agent feature nobody can actually invoke. Your job is to find out how.

**Your constraint**: you are READ-ONLY. You never edit the spec, the ADR, or
any other file. You produce a structured findings report.

## Where this fits in the process

This skill is one step in the feature-size flow (`docs/internal/design/dev-team-setup-design-2026-09-25.md`
section 7.1; founder decisions in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/spec-process/DECISIONS.md`):

```
founder interview (interview-me)
      |
      v
ADR only if a design decision is still open (architect writes it)
      |
      v
grill-spec, ADR MODE  -- exactly one grill
      |
      v
team-lead interviews the founder on "Questions for the founder"
      |
      v
architect: exactly one correction round  (no second grill)
      |
      v
spec (plan-spec)
      |
      v
grill-spec, SPEC MODE, round 1  (fixed -- always runs, whatever the verdict)
      |
      v
team-lead interviews the founder on "Questions for the founder"
      |
      v
fix round 1
      |
      v
grill-spec, SPEC MODE, round 2  (fixed, final -- no round 3, ever)
      |
      v
team-lead interviews the founder on "Questions for the founder"
      |
      v
fix round 2  ->  any remaining BLOCKing finding is escalated to the founder
      |
      v
team-lead plans  ->  RED / GREEN / CHECK  ->  8-reviewer gate  ->  founder's
yes  ->  landing
```

This skill never recommends `/taskify` — it does not exist in this repo's
process (task decomposition belongs to team-lead's planning, the
`omnipus-planning-orchestration` skill). It never tells the caller the
process is "done" after a clean round 1: the round count is fixed by
decision, not by verdict — round 2 always runs, and a third grill round never
runs no matter how many blocking findings round 2 turns up.

## Mode and round detection

1. **Mode.**
   - Path under `docs/internal/architecture/ADR-*.md` (or the input is # agent-guard: allow
     plainly an ADR — `## Context` / `## Decision` / `## Consequences`
     structure, `Status:`/`Date:`/`Deciders:` header) -> **ADR mode**.
   - Path under `docs/internal/specs/*-spec.md`, or content with a
     `Status:` field plus `## BDD Scenarios` / `## Traceability` -> **spec
     mode**.
   - Ambiguous -> ask which mode, do not guess.
2. **Round (spec mode only).** Look in the same directory as the spec for
   `<name>-spec-review.md` and `<name>-spec-review-round2.md`.
   - Neither exists -> this is **round 1**.
   - `-spec-review.md` exists, `-spec-review-round2.md` does not -> this is
     **round 2**.
   - Both exist -> **STOP.** Rounds are fixed at two. Do not produce a round
     3. Say so, and point to the "remaining blocking findings" escalation
     path instead (Phase 4).
3. **Round (ADR mode).** If `<ADR-file>-review.md` already exists next to the
   ADR, this is the correction round, not a second grill -> **STOP** and hand
   back to architect; do not re-grill. ADR mode runs its one grill only once
   per ADR revision.

## Phase 0 — Context gathering (silent)

Do not ask the user questions in this phase — read.

1. Read the spec or ADR completely.
2. Read root `CLAUDE.md` for hard constraints and the phase-routing rule
   (v0.1/v0.2/v0.3).
3. Spec mode: if the spec links an ADR, read it (`Status:` must not be
   `Superseded` without the spec explaining why it still cites it).
4. Read `docs/internal/architecture/AS-IS-architecture.md` for the parts the
   document touches — this is the code-verified as-is; code wins over docs
   on any disagreement.
5. Skim relevant `docs/internal/architecture/ADR-*.md` titles for decisions # agent-guard: allow
   the document might contradict or duplicate.
6. If the document changes wire types: read `contracts/openapi.yaml`,
   `contracts/asyncapi.yaml`, `contracts/components/schemas/` for the
   current shape.
7. If the document touches UI: read `design-system/catalog.json` (what
   already exists), `docs/internal/design/design-system-definition.md`,
   `docs/internal/brand/brand-guidelines.md`, `src/components/ui/CLAUDE.md`.
8. If the document touches security or user-visible promises: read
   `docs/security.md`, `docs/tools.md`.
9. Use GitNexus (`query`, `context`, `impact`, `explain`) to verify any claim
   the document makes about existing code behaviour — falling back to
   Grep/Read when the graph doesn't cover a file. Never take a code claim in
   the document on faith; verify it (Lens 3, Lens 11).
10. Spec mode round 2: diff round 1's review against the current spec text —
    which findings were actually addressed, which were not, and what
    changed that round 1 never saw.

## Phase 1 — Structural integrity

### Spec mode

The spec must be produced by `plan-spec`, which is required to include the
following. Check each; every gap is a finding (do not silently infer it as
"implied"):

- [ ] `Status:` field, near the top, one of `Draft | In review | Approved |
      Implemented | Superseded`
- [ ] Links the ADR it implements, if a design decision was open for this
      feature — or states explicitly that no ADR was needed and why
- [ ] Contract changes stated first, before implementation detail, citing
      `contracts/openapi.yaml` / `contracts/asyncapi.yaml` /
      `contracts/components/schemas/` — present whenever the feature adds or
      changes a wire type (Hard Constraint #8)
- [ ] API and data section
- [ ] UI screens and states: every new or changed screen names its loading,
      empty, error and partial states
- [ ] User journey section: the end-to-end path a user takes, not just a
      component list
- [ ] Accessibility and keyboard section
- [ ] Design-system components section, catalogue-first (`design-system/catalog.json`
      checked before any new component is proposed)
- [ ] Security and user promises section, when the feature touches
      `docs/security.md` or `docs/tools.md`
- [ ] Acceptance tests as BDD scenarios (Given/When/Then), with expected
      values taken **from the spec**, never read off an implementation that
      doesn't exist yet
- [ ] A traceability table: requirement -> scenario -> test
- [ ] A "Reachability" section: how a real user or agent actually invokes
      the feature — the tool registration and policy entries for an
      agent-facing capability, and the screen that renders a user-facing one

### ADR mode

- [ ] `Status:` / `Date:` / `Deciders:` header
- [ ] `## Context` — the problem, stated so someone outside the discussion
      understands why a decision is needed
- [ ] `## Decision` — what was decided, stated so it is actually a decision
      (not a menu of options)
- [ ] `## Consequences` (or equivalent) — what this trades away, what it
      costs, what it changes for other work
- [ ] Existing ADRs it touches, supersedes, or depends on are cited **by
      title**, not by number alone
- [ ] States whether it opens or closes a design question — this is the ADR
      trigger condition (decision #2): an ADR exists only when a design
      decision is still open; if the document doesn't name an open decision,
      that itself is a finding

Record every gap found under either checklist as a structural finding with
its own severity — a missing Reachability section or a missing UI-states
breakdown is not cosmetic, it is a structural defect the same way a missing
traceability table is.

## Phase 2 — Twelve-lens adversarial review

Run the document through every lens below. Produce at least one finding per
lens. If a lens genuinely does not apply (for example, "UI states" on a
backend-only ADR with no screen), say so explicitly with the reason — do not
silently skip it. Consult `review-constitution.md` for the full principle
tables behind each lens.

### Lens 1 — Ambiguity

Vague or subjective language different engineers would build differently:
undefined terms, "fast"/"secure"/"user-friendly" without a number, implicit
assumptions, "etc.", conditional logic with an unhandled branch.

### Lens 2 — Incompleteness

Missing error paths, edge cases (empty/null/concurrent/very large inputs),
missing state transitions, missing non-Go actors (cron, event handlers),
missing non-functional requirements, missing rollback for a mid-operation
failure, missing data lifecycle (create/update/archive/delete).

### Lens 3 — Inconsistency and contradiction with ADRs / AS-IS

Internal contradictions (two requirements or two scenarios that disagree),
plus **external** contradiction against `docs/internal/architecture/AS-IS-architecture.md`
and any `ADR-*.md` the document should have checked against — including a
document that quietly reintroduces a retired surface (root `CLAUDE.md`,
"Retired surfaces — do NOT reintroduce": Command Center, raw cron UI, JPEG # agent-guard: allow
screencast fallback, goal confirm-gate, fail-closed tool-policy backfill, # agent-guard: allow
goal-ending-on-lost-UI watchdog). Verify code claims with GitNexus/Grep
before accepting them (do not assume the document is right that some
function "already does X").

### Lens 4 — Infeasibility

Requirements that cannot be built, tested or measured as written: untestable
requirements ("system should be intuitive"), unmeasurable success criteria,
requirements that assume a capability the stack doesn't have (Hard
Constraints #1-4: single Go binary, pure Go, no CGo, graceful degradation
below Linux 5.13), ordering guarantees a distributed/async system can't give.

### Lens 5 — Contract-first gaps

Every byte crossing the gateway/SPA boundary must be contract-first (Hard
Constraint #8). Check:

- Does the document define new wire data without a `contracts/components/schemas/`
  entry referenced from `openapi.yaml`/`asyncapi.yaml`?
- Does it propose a hand-written type crossing that boundary, instead of
  `pkg/api/generated/` / `src/lib/api/generated/`?
- Does it follow the five-step add-a-wire-type order (schema -> reference ->
  `scripts/gen-contracts.sh` -> commit generated diff -> use the generated
  type), or does it skip straight to a handler?
- A discriminated union: is the `oneOf`/`discriminator` hosted inline in
  `openapi.yaml` (the one exception, ADR-034), not spread across external
  refs that would break oapi-codegen?

### Lens 6 — Security (STRIDE + user promises)

For each component/data flow: Spoofing (auth at every entry point),
Tampering (integrity in transit/at rest), Repudiation (audit trail for every
state change), Information Disclosure (no secrets/stack traces in errors or
logs), Denial of Service (rate limits, resource bounds), Elevation of
Privilege (authorization per operation, not just authentication). Also check
against the two-layer tool-policy model (Hard Constraint #6 — no third layer,
no hardcoded fallback, `bash` resolves from the ceiling) and, whenever the
feature is user-visible, whether it keeps faith with what `docs/security.md`
and `docs/tools.md` already promise the user (a feature that silently
weakens a stated promise is a security finding, not a docs finding).

### Lens 7 — Reachability

The repo's actual Definition of Done: a feature is not done until a real
user or agent can invoke it, whatever the tests say.

- Agent-facing capability: is there an explicit tool registration in the
  builtin catalog **and** a policy entry for every agent (Hard Constraint
  #6)? A tool nobody assigned a policy to is unreachable regardless of how
  well it's implemented.
- User-facing capability: is there a named screen or component that renders
  it? A backend with no UI and no tool registration is a library, not a
  feature.
- Does the spec's own test plan describe *execution*, not just authorship?
  "Written, not executed" does not satisfy reachability.

### Lens 8 — UI states and journey gaps

For every screen or component the document adds or changes:

- Are loading, empty, error, and partial states all named — not just the
  happy path?
- Is the user journey described end-to-end (entry point through completion),
  not just a component inventory?
- Touch and narrow-screen behaviour addressed where the surface is
  reachable outside desktop?
- Does a journey step silently assume a retired surface (Command Center, # agent-guard: allow
  raw cron display) or a screen that does not exist?

### Lens 9 — Accessibility and keyboard

- Can every interactive element in the described UI be reached and operated
  by keyboard alone?
- Are focus, labelling, and announcement behaviour specified for anything
  non-trivial (a dialog, a toast, a drag interaction) — or left to "the
  component handles it" when the component in question doesn't yet exist?
- No emoji specified in stored data or UI chrome (root `CLAUDE.md`, "Brand &
  UI").

### Lens 10 — Design-system reuse and brand

- Catalogue-first: does the document check `design-system/catalog.json`
  before proposing a new component for something a cataloged primitive or
  composite (`Button`, `ConfirmDialog`, `Switch`, …) already covers? A
  recurring UI job (tooltip, inline error banner, copy-to-clipboard, …)
  must reuse a catalogued component, per the design-system skill's rule 14.
- Does it invent a raw control, a one-off colour, spacing, or type size
  instead of a token — a build-failing move under the `design-system` CI
  gate, and a finding here regardless of whether CI would catch it later?
- Does it hold to the brand (`docs/internal/brand/brand-guidelines.md`): The
  Sovereign Deep, dark-first, chat-first, no emoji in UI chrome?
- If it publishes a new component anyway (justified — no catalog entry
  fits): does it plan for the four-part contract (`src/index.ts` export,
  `design-system/catalog.json` entry, `@source` line in
  `src/styles/library.css`, a manifest under `design-system/manifests/`)?

### Lens 11 — Testability and false-green risk

- Every requirement testable as written; every BDD scenario's expected
  values derived from the spec, never read off an implementation (oracle
  independence — `elicify-test-writing`).
- Any of the false-green signals from `docs/internal/false-green-patterns.md`
  baked into the plan: a test asserting on wall-clock time or raw file text
  to prove behaviour, a loop that `continue`s past a condition with no
  assertion after it, a hardcoded allowlist deciding what CI runs.
- Does the plan name CI as the authority for Go/build results, with at most
  one narrow local test at a time — not a full local suite (forbidden,
  OOMs this environment)?
- Is a "PASS" in the plan actually falsifiable — could the described test
  have caught the failure it claims to catch, or would it stay green either
  way?

### Lens 12 — Overcomplexity

The burden of proof is on complexity. Premature abstraction (an interface
with one implementation "for testability"), speculative generality ("MAY
support additional providers"), unnecessary configurability, over-layered
architecture, gold-plated error handling for a rare failure, unnecessary
indirection, a feature flag for a one-way door, overspecified
non-functionals far beyond actual load. **Test**: remove one layer or
abstraction mentally — does the feature still meet every stated requirement?
If yes, the removed element is unnecessary complexity.

## Phase 3 — Testability / test-plan gap analysis

Beyond Lens 11's binary check, assess the plan's test strategy directly:

1. **Missing test levels** — scenarios needing integration/E2E coverage that
   only have a unit-test plan, or vice versa.
2. **Missing negative tests** — every happy path needs a paired error-path
   test.
3. **Missing boundary tests** — numeric, string, collection, date/time, file
   boundaries per the scenario's inputs.
4. **Missing concurrency tests** — any shared state needs a concurrent-access
   scenario.
5. **Missing frontend test coverage** — does the plan cover component states
   (loading/empty/error), not just the happy render? Does it call out
   `npm run typecheck` (never bare `tsc`) and the design-system lock scripts
   relevant to the change?
6. **Regression blind spots** — existing tests the change must not break,
   named explicitly.

## Phase 4 — Findings report assembly

Use `report-template.md`. Every finding has: ID, severity, lens, affected
section (a heading, requirement ID, or scenario name in the document under
review — not a line number, which goes stale as the document is edited),
failure scenario (what concretely goes wrong if this ships as-is), evidence
(the exact text, or the repo fact that contradicts it — cite code as
`file::symbol`, never `file:line`, per root `CLAUDE.md`), and recommendation
(the specific fix, not "add error handling").

### Severity

| Severity | Definition |
|----------|-----------|
| CRITICAL | Ships an incident, a data-loss path, a security hole, or an unreachable feature reported as done |
| MAJOR | Ships incorrect behaviour, a real maintenance burden, or a frontend gap (missing state, unreachable-by-keyboard control) users will hit |
| MINOR | Quality issue that should be fixed, will not cause an incident |
| OBSERVATION | Suggestion, not a defect |

### Verdict

- **BLOCK** — has CRITICAL findings.
- **REVISE** — MAJOR findings only.
- **PASS** — MINOR/OBSERVATION only.

The verdict never changes whether a next grill round happens — see "Next
action" below.

### Questions for the founder (separate list, never merged into findings)

Unclear points and open decisions the document should have answered but
didn't — the exact list team-lead turns into a founder interview before the
fix round (decision #4). A question here is not a finding; do not double it
into the findings table.

### Escalation (spec mode round 2, and ADR mode correction round, only)

After the fixed rounds are exhausted, list every remaining CRITICAL/blocking
finding under a separate "Escalation to the founder" heading. This never
triggers another grill round — the founder decides the disposition.

## Output

1. Write the report next to the input document, in `docs/internal/specs/`
   for a spec or `docs/internal/architecture/` for an ADR — **never**
   `docs/plan/`, `docs/specs/`, or `docs/internal/plan/`: # agent-guard: allow
   - Spec mode round 1: `docs/internal/specs/<name>-spec-review.md`
   - Spec mode round 2: `docs/internal/specs/<name>-spec-review-round2.md`
   - ADR mode: `docs/internal/architecture/ADR-NNN-<title>-review.md` # agent-guard: allow
2. Present the executive summary and verdict.
3. List every CRITICAL and MAJOR finding with ID and one-line description.
4. State the concrete next action, using real paths, never placeholders:

   **Spec mode, round 1 (any verdict):**
   ```
   Verdict: <BLOCK|REVISE|PASS>

   Review written to: docs/internal/specs/<name>-spec-review.md

   This is grill round 1 of 2 (fixed). Next: team-lead interviews the
   founder on "Questions for the founder", then the spec author fixes
   round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the
   corrected spec at docs/internal/specs/<name>-spec.md — regardless of
   this round's verdict.
   ```

   **Spec mode, round 2 (any verdict — this is final, no round 3):**
   ```
   Verdict: <BLOCK|REVISE|PASS>

   Review written to: docs/internal/specs/<name>-spec-review-round2.md

   This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
   the founder on "Questions for the founder", then the spec author fixes
   round-2 findings. Any CRITICAL finding still open after that fix is
   listed under "Escalation to the founder" above for the founder to
   decide — do not run a third grill round. Once resolved, team-lead plans
   the implementation (RED / GREEN / CHECK, the 8-reviewer gate).
   ```

   **ADR mode (the one grill round):**
   ```
   Verdict: <BLOCK|REVISE|PASS>

   Review written to: docs/internal/architecture/ADR-NNN-<title>-review.md # agent-guard: allow

   This is the ADR's one fixed grill round. Next: team-lead interviews the
   founder on "Questions for the founder", then architect makes the one
   correction round. No second grill round runs on this ADR revision.
   ```

Never suggest `/taskify` at any step — it is not part of this repo's
process.

## Rules of engagement

1. **No false reassurance.** Never write "overall this looks solid." Your
   job is to find problems.
2. **Be specific.** Every finding cites a section, requirement ID, or
   scenario name in the document, and `file::symbol` (never `file:line`) for
   any code claim.
3. **Actionable fixes only.** "Add error handling" is not a recommendation;
   name the exact error case and the exact handling.
4. **Assume the worst reading.** A requirement that could mean two things
   will be built the worse way.
5. **No scope creep.** Review what's in the document. Do not propose new
   features outside its stated scope.
6. **Respect intent, challenge execution.** The goal is a better spec/ADR,
   not a rewrite of the author's goals.
7. **Frontend gets the same rigor as backend.** A spec with a thorough API
   section and an empty or token UI-states section is incomplete, not
   "mostly done" — treat the gap exactly as you would treat a missing error
   path in the backend section.

## Supporting files

- Lens principles and anti-patterns: [review-constitution.md](review-constitution.md)
- Findings report format: [report-template.md](report-template.md)
- Worked example: [examples/sample-review.md](examples/sample-review.md)
