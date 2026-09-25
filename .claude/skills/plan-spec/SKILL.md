---
name: plan-spec
description: >-
  Writes a feature spec at `docs/internal/specs/<name>-spec.md` from the founder's
  interview-me output (and the ADR, if one was written) plus this repo's real
  references — contracts first, then backend and frontend equally, BDD acceptance
  scenarios with oracles from the spec, a traceability table, and a Reachability
  section. Use for feature-size work only (small/standard skip the spec step). Ends
  by hand-off to grill-spec round 1.
argument-hint: "[interview-me output path] [ADR path, if one exists]"
allowed-tools: Read, Grep, Glob, Bash, Write, Edit
---

# Plan & Spec (plan-spec)

Last reviewed: 2026-09-25
Design source: `docs/internal/design/dev-team-setup-design-2026-09-25.md` (§7.1, the
feature-size flow); founder decisions:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/uat/spec-process/DECISIONS.md`.

## When this runs, and on what input

Feature-size work only. Small and standard changes never reach this skill — they build
directly, with tests, then go to their own reviewer set (§7.1 of the design doc above).
`/taskify` does not exist; task decomposition belongs to team-lead's planning
(`omnipus-planning-orchestration`), not to this skill.

By the time plan-spec runs, the founder has already been interviewed by `interview-me`,
and — only if a design decision was still open — architect has written an ADR that has
been through its one grill round and one correction round. plan-spec does **not**
re-interview the founder; that already happened. Its job is to turn what already exists
into the repo's spec:

- **interview-me's output** — the spec file it wrote (`spec-<kebab-name>.md`) and its
  `.<spec-name>.interview.json`: read the `Decisions Log` (six columns: ID, Topic,
  Decision, Rationale, Source, Date) and the `Dependency Graph & Implementation Order`
  sections — these are interview-me's fixed, parseable sections. Everything else in that
  file is free-form interview output; mine it for content, but do not carry its
  structure forward — the fixed structure below is this repo's spec structure, not
  interview-me's.
- **the ADR**, if one exists — link it in the new spec (below); cite it by title, not
  number alone (root `CLAUDE.md`).
- **this repo's real references** (below) — never invent a document; if a reference this
  skill names does not exist for the feature at hand (e.g. no ADR), say so and move on.

If the interview-me output is missing, incomplete, or contradicts itself on a point that
changes the spec's shape, **stop and ask** rather than filling the gap with an
assumption — record it in Ambiguity Warnings (Phase 4) only for gaps that do not block
starting; a blocking gap is a question, not a warning.

## References to use while writing (verified to exist in this repo)

- `CLAUDE.md`, `docs/internal/architecture/AS-IS-architecture.md` — code-verified system
  facts; code wins over docs.
- every `ADR-*.md` file under `docs/internal/architecture/` — existing decisions, cited
  by title, not number alone (numbers have review-round siblings).
- `docs/internal/_archive/preview-doc-v03-concept/` — the v0.3 direction, for anything
  that touches it.
- `contracts/openapi.yaml`, `contracts/asyncapi.yaml`, `contracts/components/schemas/` —
  contract-first wire types (Hard Constraint #8). Every new data exchange starts here;
  the generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only
  legal cross-boundary types.
- Frontend: `docs/internal/brand/brand-guidelines.md`,
  `docs/internal/design/design-system-definition.md`, `design-system/catalog.json`,
  `.claude/skills/omnipus-design-system/SKILL.md`, `src/components/ui/CLAUDE.md`.
- Quality: `docs/internal/false-green-patterns.md`, `.claude/skills/elicify-test-writing/`
  (test oracles come from the spec, never from code — the same law applies to writing
  the spec's own BDD scenarios and datasets), and
  `docs/internal/design/dev-team-setup-design-2026-09-25.md` for sizes and the
  RED/GREEN/CHECK gate this spec feeds.
- User promises: `docs/security.md` and `docs/tools.md`, whenever the feature touches
  tool access, approval, or the shell-rule/Auto-approve surface.
- Codebase understanding: GitNexus MCP tools — `query`, `context`, `impact`, `trace`,
  `explain`, `detect_changes` (exact names; there is no `gitnexus_query` or similar
  prefixed form). Fall back to Grep/Read when the graph does not cover a file or the
  index is stale/empty — that fallback is correct, not non-compliance
  (`.claude/CLAUDE.md`).

## Output location (fixed — never anywhere else)

`docs/internal/specs/<name>-spec.md`. Do not use any other output directory — none of the other directory names sometimes seen in older material exist in this repo. # agent-guard: allow
Reviews from grill-spec land next to it as
`<name>-spec-review.md` then `<name>-spec-review-round2.md` — do not create those files
yourself; that is grill-spec's job.

## Phase boundary guardrail

Sections **User Stories & Acceptance Criteria** and **Behavioral Contract** describe
observable behaviour only — no method signatures, schema definitions, framework
annotations, internal function/variable names, or struct/class definitions. The
**Reachability**, **Tech Stack**, and everything from **BDD Scenarios** onward are
exempt — implementation detail is expected there.

| Phrasing | Verdict |
|---|---|
| "the system returns a 4xx error" | Permitted — observable outcome |
| "the handler calls `http.Error(w, msg, 400)`" | Prohibited — implementation mechanism |
| "the SPA shows an inline error banner" | Permitted — observable outcome |
| "`useMutation`'s `onError` sets `errorState`" | Prohibited — implementation mechanism |

Before finalizing those two sections, self-check against this table and rewrite any
prohibited phrasing.

## Phase 1 — Synthesize from interview-me and the ADR

Read interview-me's output file and interview-state JSON, and the ADR if one exists.
Summarize, per topic:

- What was decided (from the Decisions Log), and its rationale.
- What is still open (any coverage area interview-me marked `pending`, or a decision the
  ADR deferred).
- What the Dependency Graph & Implementation Order section implies about build order.

Then explore the codebase and this repo's references (above) to ground the spec:

- GitNexus `query` for the feature's concept area, `context` on symbols the feature will
  call or extend, `impact` (direction: upstream) on any symbol the feature plans to
  modify — flag HIGH/CRITICAL blast radius prominently.
- `contracts/openapi.yaml` / `asyncapi.yaml` / `contracts/components/schemas/` for
  existing shapes this feature extends or must not collide with.
- `design-system/catalog.json` for components this feature's UI could reuse before
  proposing a new one.

Record findings under **Existing Codebase Context** (symbols, impact, execution flows —
GitNexus) and **Contract Context** (existing schemas this feature touches, extends, or
must add to) in the output spec. Omit either section if truly nothing applies (e.g. a
pure-frontend copy change has no contract context) — never leave a section with
templated placeholders.

**GATE**: if a point that changes the spec's shape is unresolved after this phase, stop
and ask (the founder, via whoever dispatched this skill) rather than assume. This is the
one point in the flow where plan-spec may still need a founder answer, precisely because
interview-me cannot always anticipate every question the codebase raises.

## Phase 2 — User Stories & Acceptance Criteria

For each distinct capability the interview settled on, write a user story:

- Priority (P0 critical … P4 backlog) with a one-line "why this priority".
- Narrative: who benefits, what they do, why it matters.
- **Independent Test**: how to verify this story in isolation.
- Numbered **Acceptance Scenarios**, Given-When-Then, one per distinct outcome.

Add an **Edge Cases** section: boundary conditions, error scenarios, unusual situations,
with expected behaviour for each.

## Phase 3 — Behavioral Contract, Non-Behaviors, Integration Boundaries

**Behavioral Contract** — concise When/Then statements covering primary flows, error
flows, and boundary conditions. A quick-reference summary, not a replacement for Phase 2.

**Explicit Non-Behaviors & Safeguards** — two subsections:

- *Qualitative Prohibitions*: "The system must not [X] because [reason]." Include
  anything an agent might "helpfully" add beyond scope, and any security/safety boundary
  — check against `docs/security.md` and `docs/tools.md` if the feature touches tool
  access, approval, or shell rules.
- *Machine-Verifiable Constraints*: exact HTTP status codes and response shapes (cite the
  contract schema, never invent one inline — Hard Constraint #8), exit codes for CLI
  surfaces, performance bounds with units, scope boundaries. No vague language — if you
  cannot write a test for it, rewrite it until you can.

**Integration Boundaries** — for each external system or internal service boundary the
feature crosses: data in/out, contract (cite the schema file), failure behaviour,
real-service-vs-mock development approach.

## Phase 4 — Ambiguity Self-Audit

Scan the whole spec so far for places an implementing agent would have to assume
something. For each: what's ambiguous, the likely assumption an agent would make
unprompted, and the question that resolves it. This table ships in the spec
(**Ambiguity Warnings**) for whoever dispatched plan-spec to resolve, accept, or defer —
it is not a blocking gate the way Phase 1's gate is; a genuinely blocking ambiguity
belongs in Phase 1, not here.

## Phase 5 — BDD Scenarios and Test Datasets

Format and mandatory rules: `knowledge/bdd-template.md`. Dataset construction:
`knowledge/test-dataset-template.md`. In short:

- Every scenario carries `Traces to: User Story [N], Acceptance Scenario [M]` and one
  category (Happy Path / Alternate Path / Error Path / Edge Case).
- Expected values in every scenario and every dataset row come from this spec (or the
  cited contract schema / ADR), never from reading any existing implementation —
  `elicify-test-writing`'s oracle-independence law applies to the spec's own scenarios,
  not only to the tests qa-lead writes from them later. If you find yourself wanting to
  open an implementation file to find the "right" expected value, that is the signal the
  spec itself is underspecified — resolve it in Phase 4, not by copying the code's
  answer.
- One action per **When**. Boundary/negative/edge cases get their own dataset rows, not
  just happy-path rows.

## Phase 6 — Reachability, Functional Requirements, Traceability

**Reachability** (mandatory, Definition of Done, root `CLAUDE.md`) — state, concretely,
how a real user or agent will invoke this feature once built:

- Tool registration: is a new tool being added? Name it, and state which config file(s)
  carry its policy entry for every agent (Hard Constraint #6) — a tool nobody has a
  policy entry for is unreachable regardless of test status.
- Screen: which screen or component renders it. A backend change with no UI and no tool
  registration is a library, not a feature — say so plainly if that is genuinely the
  case (e.g. an internal-only refactor), never paper over a missing UI path.
- Test plan execution: name where in the flow (RED/GREEN/CHECK, §7.1) this gets executed,
  not merely written.

**Functional Requirements** — `FR-001: System MUST/SHOULD/MAY [requirement].` Testable;
if you cannot write a test for it, rewrite it.

**Success Criteria** — `SC-001: [measurable, no subjective language].`

**Traceability Matrix** — one row per FR-xxx, linking to its user story, its BDD
scenario(s), and its test name(s) (test names are placeholders here — qa-lead names the
real tests in RED). Every FR-xxx appears; every BDD scenario appears at least once. A gap
here is an incomplete spec — fill it before finishing.

## Phase 7 — Assemble and hand off

1. Assemble the spec using `knowledge/spec-template.md` as the structural skeleton —
   fill every section; remove only the sections that template marks removable, and only
   when genuinely not applicable (say so explicitly, don't just delete silently).
2. The spec **starts with the `Status:` field** — exactly one of `Draft`, `In review`,
   `Approved`, `Implemented`, `Superseded`. A new spec starts `Draft`; plan-spec sets it
   to `In review` once the file is written and ready for grill-spec (the field moves to
   `Approved`/`Implemented`/`Superseded` later, by spec-sync).
3. If an ADR exists for this feature, the second line links it by title:
   `ADR: [ADR-NNN — Title](../architecture/ADR-NNN-title.md)`. If no design decision was
   open, state that plainly instead of a placeholder: `ADR: none — no open design
   decision`.
4. Write the file to `docs/internal/specs/<name>-spec.md`, `<name>` in kebab-case from
   the feature name.
5. Report to whoever dispatched this skill: the spec path, section counts (user stories,
   BDD scenarios by category, FR-xxx count, dataset row count), any Ambiguity Warnings
   still open, and the explicit hand-off: **this spec is ready for grill-spec, round 1.**
   Do not invoke grill-spec yourself — that dispatch belongs to team-lead/squad-lead.

## Quality checks before reporting done

- [ ] `Status:` is the first line, one of the five fixed values.
- [ ] ADR link present (or explicitly "none — no open design decision").
- [ ] Backend and frontend covered equally: contract changes come first, then API/data,
      then UI screens and states (loading/empty/error/partial), the user journey,
      accessibility and keyboard, design-system components (catalog checked first before
      any new one), security and user promises.
- [ ] Every acceptance scenario has a BDD scenario; every BDD scenario traces back.
- [ ] Every dataset row traces to a BDD scenario; boundary, edge, and error categories are
      all represented, not just happy path.
- [ ] Reachability section names the actual tool-policy file(s) and the actual screen —
      not "TBD".
- [ ] Traceability Matrix has no gaps.
- [ ] No implementation detail leaked into Phase 2/3 sections (phrasing table above).
- [ ] Output path is exactly `docs/internal/specs/<name>-spec.md` — nowhere else.
- [ ] Report names the spec path and states the grill-spec round 1 hand-off explicitly.

## Supporting files

- `knowledge/spec-template.md` — the output document's structure.
- `knowledge/bdd-template.md` — Given-When-Then format and rules.
- `knowledge/test-dataset-template.md` — boundary/edge/error dataset construction.
