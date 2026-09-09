# ADR-080 — Goal compile gains a restated statement, judgment-typed criteria, a Definition of Done, shared workspace context, and the `define-goal` skill rename

- **Status:** **Accepted** — operator-ratified 2026-09-07 (the five decisions below were agreed with the operator over several rounds and are ratified; this ADR is written to implement them, not to relitigate). Composes with and amends [ADR-079-goal-compile-confidence-context-clarify.md] (Accepted) and [ADR-074-judgment-first-criteria.md] (Accepted); leaves [ADR-078-goal-confirm-button-and-pending-context.md] intact but adds two rendering obligations to `buildGoalPendingNote`/`GoalEchoCard`.
- **Deciders:** architect (composed); Daniel Piatkowski (operator, ratified 2026-09-07).
- **Why a NEW ADR, not an edit to ADR-079 (stated per the operator's request):**
  1. ADR-079 is **Accepted and operator-ratified**; the established practice in this repo is a fresh composing ADR per decision batch (ADR-078 amends ADR-074; ADR-079 amends ADR-074), never an in-place rewrite of an Accepted record.
  2. **D-TYPES introduces the project's first goal-related Constraint #8 wire change** — a `judgment` field on the shared `AcceptanceCriterion`/`AcceptanceCriterionInput` contract. That directly **reverses ADR-079's headline consequence "Zero new wire types."** A reversal of a named consequence belongs in its own record, not a footnote edit to the document that made the claim.
  3. The five decisions form one coherent batch ("what the compile produces, how it is typed, judged, and contextualized, and the skill that governs authoring it") and read as a unit.
- **Extends / amends (precise):**
  - **ADR-079 D1** (session-transcript window into the compile) → extended by **D-CONTEXT2**: the compile ALSO receives workspace/project instructions (compile-only, operator-ratified 2026-09-07 — the Judge does NOT).
  - **ADR-079 D2** (compile response = `assessment.clarity` + `oneOf{criteria,questions}`) → amended by **D-STATEMENT** (adds `definition` on the clear branch), **D-TYPES** (each criterion object grows a required `judgment`), and **D-DOD** (adds `dod[]` on the clear branch). INV-1's "criteria carry only `text`" is amended to "only `text` and `judgment`".
  - **ADR-074 D4** (`define-done` skill, one criteria-authoring skill) → amended by **D-SKILL**: renamed to `define-goal` and widened to three parts (statement + criteria + DoD).
  - **ADR-074 D5 / ADR-078 D1** (goal echo/confirm card) → the card and the ADR-078 pending-context note now also render the restated statement and the DoD block.
- **Code cited (grounded, verified this session):**
  - Compile call: `pkg/agent/goal_compile_llm.go::buildGoalCompileMessages` (current signature `(prose, question, answer, repairReason)` — no `sessionWindow`/`workspaceInstructions` yet; ADR-079 D1 + this ADR add them), `::loadDefineDoneSkillContent` (`:194`), `::defineDoneSkillPath` (`:183`, path `skills/define-done/SKILL.md` `:188`), `::goalClarificationRecord` (`:78`), `::goalCompileLLMCall`.
  - Judge feed: `pkg/agent/judge.go::buildJudgeUserContent` (`:890`) — sections are Workspace file diff (`:923`), Session transcript window (`:933`), Machine-check results (`:945`). **Verified: it carries NO workspace/project-instructions section today** (grep of `judge.go`/`verifier_adjudication.go` for `buildWorkspaceInstructionsNote`/`WorkspaceInstruction` is empty).
  - Window reuse: `pkg/agent/verifier_adjudication.go::sessionWindowText` (`:325`), `::renderVerifierWindowText` (`:467`), `verifierWindowTokensDefault = 20000` (`:51`).
  - Workspace-instructions seam (reused by D-CONTEXT2): `pkg/agent/workspace_instructions.go::injectWorkspaceInstructions` (`:38`), `::buildWorkspaceInstructionsNote` (`:68`), `::buildProjectInstructionsNote`; cap `pkg/skills/project_instructions.go::MaxInstructionsBytes = 262144` (256 KB).
  - Criterion kind machinery (Concept A): `pkg/task/criterion.go::InferCriterionKind` (`:59`), `KindCheck`/`KindProse`/`KindBehavior`; `pkg/agent/goal_compile.go::sameShape` (`:664`); `pkg/tools/plan_correct.go::criterionKey` (`:877`); `normalizeCriteria` (per ADR-074: `pkg/task/store.go`, `pkg/plan/plan.go`/`store.go`).
  - Contract: `contracts/components/schemas/AcceptanceCriterion.yaml` (canonical, `kind ∈ {check,prose,behavior}`, `additionalProperties: false`), `AcceptanceCriterionInput.yaml` (derived, `kind` optional), `CriterionVerdict.yaml`, `Goal.yaml` (has `definition` + `criteria[]`, **no `dod[]`**), `GoalStatusFrame.yaml` (`criteria` $ref AcceptanceCriterion; **asyncapi.yaml carries a hand-synced INLINE duplicate**).
  - Skill rename touch points: `pkg/coreagent/core.go` `coreAgentSkills`/`systemAgentSkills` (`define-done` at `:1495`,`:1694-1706`), `SkillsMigrationDefineDone = "adr074-define-done"` (`:2125`); tool descriptions `pkg/tools/task.go:418`, `pkg/tools/plan.go:123`, `pkg/tools/plan_correct.go:154`, `pkg/sysagent/tools/task.go:183` (+ golden `pkg/tools/testdata/provider_defs.golden.json`).

## Context

Operator review of the ADR-079 goal-compile design settled five further decisions. All five are RATIFIED; the work here is to lock them into the ADR/spec surface precisely enough to implement, then grill once and correct. They centre on the same synchronous, interactive `/goal` compile step ADR-079 already redesigned.

## Decisions

### D-STATEMENT — the compile produces a restated goal statement, before the criteria

**Decision.** On the **clear** branch (ADR-079 D2 `clarity == "clear"`), the compile response gains a required **`definition`** string: the request restated as **one clear sentence**, staying close to the setter's own words, emitted BEFORE the criteria. It maps onto the **already-existing `Goal.definition` field** (`Goal.yaml`: "The compiled SMART restatement of `prompt` (US-3 echo-confirm) — distinct from the raw prompt") — no new persisted field; the change is that the compiler now actually PRODUCES it in the shape below and the confirm card RENDERS it.

**Template** (from the `define-goal` skill, Part 1): *"Produce `<outcome>` for `<who/what it serves>`, so that `<the one observable end-state>` — `<optional: by when / within a budget or attempt limit>`."*

**Rules (all enforced by prompt + the `define-goal` bar, since a sentence's quality is not schema-checkable):**
- **One primary outcome** (extra outcomes become criteria).
- **Observable end-state, not activity.**
- **Echo the setter's own words** (anti-drift).
- **A time/effort bound ONLY if the request implies one** — never invented.
- **Does NOT assert achievability** — the D9 feasibility gate (ADR-074 D4a, unchanged, still last) owns that.
- The setter approves **statement + criteria + DoD** together at the confirm card (ADR-078).

**Schema/parser.** `parseGoalCompileResponse` requires `definition` non-empty on the clear branch (absent/empty → `errGoalCompileSchema` → repair/fallback, unchanged machinery); absent on the ambiguous branch. On the deterministic fallback path (LLM failure/veto-twice), `definition` is set to the marker/condition text as today — the statement is a best-effort compile artifact, never a blocker.

**In-memory + echo.** `CompiledGoal` (`goal_compile.go:82`, today `{Intent, Prompt, Criteria}`) gains a `Definition string` field, DISTINCT from `Prompt` (which stays "the prose remainder / steering prompt"). `formatGoalEcho` (`goal_compile.go:502`, the single echo renderer that `GoalEchoCard` and the **channel plain-text echo** and the ADR-078 `buildGoalPendingNote` all share) renders `Definition` as the "Goal:" line in place of today's `Prompt`/`Intent` fallback, so the restated statement is what every confirmation surface shows.

**Wire.** The card needs the statement: `GoalStatusFrame` gains an **additive-optional `definition` string**, present on the `queued` (pending-confirm) emission alongside `criteria` (same shape decision as ADR-074 D5.2's `criteria` addition). Name reused from `Goal.definition` for consistency.

### D-TYPES — every acceptance criterion carries a required judgment kind (THE contract crux)

**Decision.** Every acceptance criterion carries a REQUIRED **judgment kind** ∈ **{`boolean`, `quantitative`, `artifact`}**:
- **`boolean`** — a yes/no fact the reader (Judge) can rule true or false.
- **`quantitative`** — a value against a threshold or comparator.
- **`artifact`** — a named produced/changed/sent thing whose existence is checkable.

The compiler MUST tag every criterion with exactly one kind; it MUST reject a criterion it cannot tag (forces a rewrite) and MUST NOT emit a compound "X and Y" line. **Honestly-subjective outcomes stay `boolean`** — the Judge rules on them; **no fabricated numbers for taste** (the `define-goal` bar forbids over-quantifying).

#### The two "kind" concepts — reconciliation (grill this hardest)

There are now two fields that could both be called "kind". They are **orthogonal axes and they COEXIST; neither merges, neither renames.** The new field takes the non-colliding name **`judgment`**.

| | **`kind`** (existing, Concept A) | **`judgment`** (new, D-TYPES, Concept B) |
|---|---|---|
| Question it answers | *By what MECHANISM is this verified?* | *What SHAPE of claim is this?* |
| Values | `check` / `prose` / `behavior` | `boolean` / `quantitative` / `artifact` |
| Carries a payload? | Yes — `check`⇒`{command,expected_exit_code}`, `behavior`⇒`{tool,min/max,scope}`, `prose`⇒none | No — a bare enum |
| Who evaluates | `check`=machine exit code, `behavior`=deterministic tool-call count, `prose`=LLM Judge | (does not change the evaluator; a property of the *statement*) |
| Embedded in | `InferCriterionKind`, `normalizeCriteria`, `sameShape`, `criterionKey`, 4 tool schemas, the D2-rule-5 all-check bash gate, `KindCheck/Prose/Behavior` | new |

**Why coexist, not merge.** They are genuinely independent: a `prose` (LLM-judged) criterion is very often `boolean` ("the email addresses all three questions"), `quantitative` ("names at least three competitors"), or `artifact` ("an itinerary document exists"). Merging would be lossy both ways — you would lose "an `artifact` criterion the Judge rules on subjectively" (no home in `{check,prose,behavior}`), and you would lose the mechanism/payload distinction (a `quantitative` claim can be either an LLM-judged "≥3 competitors" **or** a `behavior` tool-count — same judgment shape, different mechanism).

**Why `kind` does not rename.** It has 100+ internal references, four tool schemas, gate logic, and persisted-data semantics. Renaming it is a large, gratuitous blast radius. The NEW field is what takes a fresh name.

**The correlation (the part that makes this cheap and safe).** The two technical kinds have a *natural, deterministic* judgment:
- `kind: check` (exit-code pass/fail) ⇒ `judgment` is **`boolean`**.
- `kind: behavior` (count vs min/max) ⇒ `judgment` is **`quantitative`**.
- `kind: prose` ⇒ `judgment ∈ {boolean, quantitative, artifact}`, author-stated; **defaults to `boolean`** when omitted (the catch-all where honestly-subjective outcomes live).

So `judgment` is **fully server-inferable** for the technical kinds and **defaults to `boolean`** for prose — exactly parallel to how `kind` is inferred from payload presence. A new helper **`task.InferJudgment(c)`** encodes this: explicit `judgment` wins (mismatch with a technical `kind` → 400); else `check→boolean`, `behavior→quantitative`, `prose→boolean`. It is called by `normalizeCriteria` (guaranteeing every persisted criterion carries one) and by the two tool parsers, mirroring `InferCriterionKind` precisely.

**Requiredness (mirrors the `kind` precedent, ADR-074 D2):**
- **`AcceptanceCriterion.yaml` (canonical/response):** `judgment` is **required**; the server always persists an explicit value (`normalizeCriteria` backfills via `InferJudgment`, including a load-time backfill of pre-ADR-080 persisted criteria: `check→boolean`, `behavior→quantitative`, `prose→boolean`, so re-serialized legacy JSON is schema-valid).
- **`AcceptanceCriterionInput.yaml` (request):** `judgment` is **optional** (server infers). **No OpenAPI `default:` key** — same documented codegen trap as `kind` (a `default:` on a not-`required` field makes openapi-typescript emit it non-optional, defeating the relaxation). A required contract test asserts the generated TS emits `judgment` optional on the Input type, required on the response type.

**At the goal-compile step specifically (the forcing function).** The compiler authors prose criteria only (INV-1), so every compiled criterion is `kind: prose` with an author-stated `judgment`. Here the enforcement is HARDER than a server default: `parseGoalCompileResponse` REJECTS a compiled criterion whose object lacks a valid `judgment ∈ {boolean,quantitative,artifact}` (→ `errGoalCompileSchema` → repair, then deterministic fallback). This is the "reject untaggable → rewrite" behaviour, at the parser, not a silent default.

**INV-1 amendment (security-relevant).** ADR-079 D2 states compiled criteria carry "**only** `text`". D-TYPES amends this to "**only `text` and `judgment`**" — the criterion object is now `{text, judgment}`, still carrying **no** `check`/`behavior` payload. The security posture is unchanged: `judgment` is a closed three-value enum and cannot smuggle a command or tool-count. A steered compiler still cannot mint a technical criterion; the human still confirms. (This is a real wording edit to ADR-079 D2 / spec INV-1 — see spec edits.)

**Compound-line honesty.** The parser can enforce **one `judgment` per criterion object**; it CANNOT reliably detect a compound sentence carrying a single tag ("the email is polite **and** answers all questions", tagged `boolean`). Therefore "reject compound X and Y" is enforced in **two layers**: (schema) exactly one judgment per object; (prompt + `define-goal` bar) "one thing per criterion". True compound detection is an **LLM-authoring-quality holdout** (same class as ADR-079 D2's clarity-correctness holdout), not a schema guarantee — stated honestly, not overclaimed.

**Contract delta for D-TYPES (exact):**
1. `contracts/components/schemas/AcceptanceCriterion.yaml`: add `judgment` (enum) to `properties` and to `required`.
2. `contracts/components/schemas/AcceptanceCriterionInput.yaml`: add `judgment` (enum) to `properties`, NOT to `required`; description documents the inference + the no-`default:` trap; header's field-set-parity note updated so the parity contract test's required-delta becomes exactly **{kind, judgment}** (both optional-on-Input).
3. Both files are `additionalProperties: false`, so the Go struct field (`AcceptanceCriterion.Judgment`, a `task.JudgmentKind` string type with `JudgmentBoolean/Quantitative/Artifact` constants and an `IsValidJudgment`), the zod schema, and **every fixture and construction site** land in ONE atomic commit (else `TestContract_*` fails on the new required key — same discipline as ADR-074 D7's `evidence_quote`).
4. `GoalStatusFrame.criteria` items `$ref` the canonical `AcceptanceCriterion`, so `judgment` surfaces to the confirm card automatically — **except the asyncapi.yaml INLINE duplicate of AcceptanceCriterion, which must be hand-synced** (the standing two-place obligation named in `GoalStatusFrame.yaml`).
5. `sameShape` (`goal_compile.go:664`) and `criterionKey` (`plan_correct.go:877`) incorporate `judgment` (two criteria are "the same shape" only if their judgment matches too) — guarded by a required test, mirroring ADR-074 required-test #4 for `kind`.

**Goal-compile response is NOT itself a Constraint #8 wire type** — it is an engine↔provider LLM JSON parsed by `parseGoalCompileResponse`, and never crosses the gateway/SPA boundary. Only the *compiled result* crosses, via `GoalStatusFrame`/`Goal`, both of which already `$ref` `AcceptanceCriterion`. So D-TYPES engages Constraint #8 exactly at the shared criterion schema, and nowhere else new.

### D-DOD — every goal has a Definition of Done, distinct from its acceptance criteria

**Decision.** Every compiled goal has a **DoD** (mandatory; may be short), DISTINCT from its acceptance criteria: **generic standing quality gates** vs **outcome-specific checks**. The compile LLM DERIVES the DoD from four layers, highest authority first:
1. **Stated in the goal** — any quality gate the setter named.
2. **Workspace/project instructions** — the standing conventions already in the agent's context (the CLAUDE.md-equivalent), fed in by D-CONTEXT2.
3. **A built-in floor** — a few universal gates (no secrets/credentials in the output; factual claims grounded). This layer **guarantees a DoD always exists** (≥1 item).
4. **Bounded inference** — type-appropriate, only defensible gates, **SHOWN for the setter's approval, never silently invented.**

The Judge evaluates **acceptance criteria ∪ DoD** together.

**Modelling — a distinct `dod[]` array (mirrors `Plan.dod`).** DoD items are `AcceptanceCriterion`-shaped (they are judged identically) but live in a **separate `dod[]` array**, not mixed into `criteria[]` — matching the existing Plan precedent (`Goal.yaml`: "a Plan's DoD ... judged against the SAME `AcceptanceCriterion` model"). Each DoD item therefore also carries a `judgment` (D-TYPES applies to DoD items too: "tests pass" is `boolean`, "no new lint errors" is `boolean`, "cost ≤ budget" is `quantitative`).

**The inferred-flag (layer-4 visibility).** So the confirm card can show inferred items for approve/drop, `AcceptanceCriterion` gains an **additive-OPTIONAL `provenance` enum ∈ {`stated`, `workspace`, `floor`, `inferred`}**, meaningful only on DoD items (absent/ignored on regular criteria and on task/plan criteria — additive-safe, never required, never breaks an existing consumer). The card flags `provenance == inferred` items as "inferred — confirm or drop". This is the minimal structured surface for "SHOWN, never silently invented"; a purely-textual marker was rejected as un-actionable in the UI.

**Compile-response + wire delta:**
- Goal-compile LLM response (internal): clear branch grows `dod: [ {text, judgment, provenance} ]`, required non-empty (floor guarantees ≥1). `parseGoalCompileResponse` enforces: clear ⇒ `dod` present and non-empty, each item validly `judgment`-tagged and `provenance`-tagged; ambiguous ⇒ `dod` absent.
- `Goal.yaml`: add **`dod`** array (`$ref AcceptanceCriterion`), **schema-required with `minItems: 1`** (operator-ratified 2026-09-07, Q2) — at least one DoD item enforced at the schema layer (the built-in floor guarantees it on every newly-compiled goal). **Legacy-goal backfill migration:** pre-ADR-080 persisted goals have no `dod`; a load-time backfill injects the built-in floor DoD **before validation**, so a legacy goal validates (satisfies `minItems: 1`) rather than failing the read. The backfill runs in the goal-load path (mirroring the `normalizeCriteria` backfill pattern) and is a named affected surface in the rollout.
- `AcceptanceCriterion.yaml` + `AcceptanceCriterionInput.yaml`: add the optional `provenance` enum (both files, additive-optional, parity-preserved).
- `GoalStatusFrame.yaml` (+ asyncapi inline copy): add **additive-optional `dod`** array (`$ref AcceptanceCriterion`), present on the `queued` emission next to `criteria` and `definition`.

**In-memory + the judged-set union seam (the load-bearing part).** `CompiledGoal` gains `DoD []task.AcceptanceCriterion`. The DoD is only real if it is actually JUDGED: the goal-adjudication criteria assembly (the criteria fed into `runVerifierAdjudication`, `verifier_adjudication.go:868`, which today sources the goal's `Criteria` and dedupes via `dedupeJudgeCriteriaAnyUnmetWins`, `:1048`) MUST feed the Judge **`Criteria ∪ DoD`** — every DoD item gets its own per-criterion verdict exactly like an acceptance criterion, and an unmet DoD item fails the round the same way. Without this union the DoD would be defined, confirmed, and never scored. The verdict list / echo may GROUP the two (a "Definition of Done" subheading) but they are one judged set.

**Confirmation echo on EVERY surface (not just the web card).** `formatGoalEcho` (shared by the web card, the channel plain-text echo, and `buildGoalPendingNote`) renders a **distinct DoD block** after the criteria, each item via `criterionEchoLine`, with `provenance == inferred` items flagged "(inferred — confirm or drop)". A channel user who confirms by typing must see the DoD and its inferred gates too, else layer-4 inference would activate a gate the channel setter never saw — violating "SHOWN, never silently invented." `ephemeralSystemNoteTokens` accounting (ADR-078 D2) includes the statement + DoD rows.

### D-CONTEXT2 — feed workspace/project instructions into the COMPILE call ONLY; the Judge validates against the self-contained `criteria ∪ dod` (extends ADR-079 D1)

**Operator ratification (2026-09-07) — compile-only.** Workspace/project instructions go into the **compile call ONLY** — the LLM that articulates the goal statement + criteria + DoD reads them once, to DERIVE the DoD (layer 2). The **Judge does NOT receive or re-read workspace/project instructions at all**; it validates purely against the confirmed, self-contained `criteria ∪ dod`. Operator's words: *"not each judge round at all… the judge only validates against the goal, it does not need to reread the full instructions; for the judge the acceptance criteria and dods are the most relevant."*

**Problem.** Today `buildGoalCompileMessages` is an isolated context — compile contract + `define-done` bar + goal prose only. ADR-079 D1 adds the session window. It gets NO workspace/project instructions, so D-DOD layer 2 (deriving the DoD from workspace conventions) cannot work. That gap is closed at the compile step.

**Seam — reuse the main turn's builder, don't invent a second, at the COMPILE call only.** The main agent turn injects `buildWorkspaceInstructionsNote(workspaceID)` (`workspace_instructions.go:68`, composing the workspace `AGENT.md` + each mount's root CLAUDE.md/AGENTS.md, capped at `MaxInstructionsBytes`). The compile resolves the goal-bearing agent's workspace id and calls the SAME builder:
- **Compile:** `buildGoalCompileMessages` gains a `workspaceInstructions string` parameter (alongside ADR-079 D1's `sessionWindow`). Because workspace/project instructions are **TRUSTED operator content** (unlike the untrusted session window, which stays in the user message as background), they are rendered in the **system** message, under an authoritative heading, distinct from and above the untrusted window. Applies to the initial, resumed, and repair compile calls.
- **Judge:** **NO change.** `buildJudgeUserContent` is NOT extended — it receives no workspace/project-instructions section. The Judge's input stays exactly ADR-074 D1's order (criteria ∪ dod, diff, window, machine-context, claim-last).

**INV-4 (new, ratified compile-only form) — the Judge validates against the self-contained `criteria ∪ dod` and nothing else.** The Judge's pass/fail set is exactly the confirmed **`criteria ∪ dod`**; it receives **no raw workspace/project instructions**. The consequence, promoted to a REQUIREMENT: **every criterion and DoD item MUST be self-contained** — a DoD item derived from a workspace convention MUST restate the needed convention detail in its own `text`, because the Judge has nothing else to interpret it against. This is exactly the `define-goal` Part-2 rule 6 ("written for the judge that reads them; no reference to context the Judge will not have"), which D-CONTEXT2 elevates from a quality bar to a hard requirement for DoD items. "Compiled-against == judged-against" holds trivially: the compiler bakes the convention into the item text at compile time, and that item text is the whole of what the Judge sees. There is therefore no live-re-read and no drift surface.

**Budget — compile-only, so bounded and moot for the Judge.** `MaxInstructionsBytes = 262144` (256 KB ≈ ~65k tokens) is loaded ONLY into the compile call. The main agent turn ALREADY pays the full instructions cost every turn, so the compile matching it is *consistent with a normal turn's cost* — no NEW instruction budget beyond what the agent's own turns already carry; on top of that the compile adds the ADR-079 20000 window + the `define-goal` bar. Combined synchronous interactive cost (up to 4 compile calls/episode) is named as a **Phase-2 calibration metric**, extending ADR-079 D-negative and ADR-074 D6. **The Judge pays nothing** — it never loads instructions, so the "instructions cap" question is moot for the Judge and is settled for the compile (reuse the 256 KB cap, one interactive call). **Verified:** the D-DOD judged-set union (R-C1: `Criteria ∪ DoD` into `runVerifierAdjudication`) unions only the two criteria arrays — it does NOT pull workspace instructions into the Judge.

### D-SKILL — rename `define-done` → `define-goal`, widen to the full three-part scope

**Decision.** The embedded skill `define-done` is renamed **`define-goal`** and expanded to govern authoring all three parts: **goal statement + acceptance criteria + DoD**. The drafted content already reflects this (`pkg/skills/embedded/define-done/SKILL.md` is the define-goal content; its frontmatter `name:`/heading still read "Define Done" pending the coordinated rename below).

**Implementation renames recorded (executed atomically at implementation — out of this docs-only scope, but pinned here):**
- Embedded dir `pkg/skills/embedded/define-done/` → `pkg/skills/embedded/define-goal/`; SKILL.md frontmatter `name: Define Done`→`Define Goal`, heading `# Define Done`→`# Define Goal`, description updated to the three-part scope. **These three edits MUST land in the SAME commit as the dir move and the Go-symbol + allowlist rename** — editing the frontmatter `name` while the dir/allowlist still say `define-done` would desync name↔path↔allowlist and can break loading, which is why this docs pass deliberately does NOT touch the SKILL.md in isolation.
- Go symbols: `defineDoneSkillPath`→`defineGoalSkillPath`, `loadDefineDoneSkillContent`→`loadDefineGoalSkillContent`, path `skills/define-done/SKILL.md`→`skills/define-goal/SKILL.md` (`goal_compile_llm.go`).
- `coreagent.SeedConfig` seeding: `coreAgentSkills`/`systemAgentSkills` lists replace `"define-done"`→`"define-goal"` (`core.go:1495`, `:1694-1706`); PlanSupervisor becomes `{plan, define-goal}`.
- Tool descriptions: `pkg/tools/task.go:418`, `pkg/tools/plan.go:123`, `pkg/tools/plan_correct.go:154`, `pkg/sysagent/tools/task.go:183` — "load the define-done skill" → "load the define-goal skill"; the golden `pkg/tools/testdata/provider_defs.golden.json` regenerates.
- All `define-done` mentions in ADR-074/078/079 + both specs → `define-goal` (with a one-line "(renamed from `define-done` by ADR-080 D-SKILL)" note at first use in each, so the history is traceable).

**Migration for already-seeded installs (grill target).** Existing installs already seeded `$OMNIPUS_HOME/skills/define-done/` and already ran the `adr074-define-done` allowlist marker (appending `"define-done"` to core-roster allowlists). A NEW one-shot, marker-keyed migration `adr080-define-goal-rename`:
1. **Allowlists (rewrite, not append):** in every agent allowlist that CONTAINS `"define-done"` and LACKS `"define-goal"`, replace the token. **Nil and operator-emptied `[]` allowlists are untouched** (same discipline as ADR-074 D4 / ADR-072 D5.1 — never restore or mutate a list the operator zeroed). The `adr074-define-done` marker stays recorded (history); the new marker guards idempotency (second boot byte-identical).
2. **Skill file dir:** on upgrade `SeedDefaults` seeds the embedded `define-goal/` fresh because its destination dir is missing (`embed.go:57-90`); the migration then **DELETES the orphaned `$OMNIPUS_HOME/skills/define-done/` directory** (operator-ratified 2026-09-07, Q3) so no stale, manually-invokable skill serving OLD content survives. **Accepted caveat:** this removes any operator edits to the old `define-done/` skill — acceptable because `define-goal` is an engine-authoritative built-in (ADR-074 D4's no-drift skill), not a user-customization surface, and the fresh `define-goal/` content supersedes it. The delete is guarded by the same `adr080-define-goal-rename` marker (runs once).
3. **Fresh installs** get `define-goal` directly from the updated `coreAgentSkills`/`systemAgentSkills` under the `isFreshInstall && len(a.Skills)==0` gate; the migration does not run for them.
4. **Config-wire scrub:** the new marker, like `seeded_skill_grants`, is stripped from the `GET /api/v1/config` response.

### D-ORDER — rollout (each step independently green, Constraint #7)

1. **D-TYPES contract** — `judgment` (+ `provenance`) on `AcceptanceCriterion`/`AcceptanceCriterionInput`, `InferJudgment`, `normalizeCriteria` backfill, `sameShape`/`criterionKey` inclusion, Go+zod+fixtures atomic. No goal logic yet. Independently testable.
2. **D-DOD + D-STATEMENT wire** — `Goal.dod` (`minItems: 1`) + the **load-time floor-DoD backfill** for legacy goals (runs before validation), `GoalStatusFrame.definition`/`dod` (+ asyncapi inline sync). Contract commit + goal-load backfill; prerequisite for the card. Independently testable.
3. **D-CONTEXT2** — the COMPILE call gains the workspace-instructions feed (reusing `buildWorkspaceInstructionsNote`); the Judge is NOT touched. No wire change. Independently testable (present in the compile input, absent from the Judge input; every DoD item self-contained).
4. **Compile response + parser** — `definition` + per-criterion `judgment` + `dod[]` in `parseGoalCompileResponse`, INV-1 amended to `{text,judgment}`, statement/DoD prompt wording in the `define-goal` bar + compile prompt. Depends on 1–3.
5. **D-SKILL rename** — dir + Go symbols + allowlists + migration + SKILL.md frontmatter, one atomic commit.
6. **UI** — card + ADR-078 pending-note render statement + DoD + inferred flag, last.

## Invariants (net, after this ADR)

- **INV-1 (amended):** the `/goal` LLM compiler emits **prose criteria carrying only `text` and `judgment`** — never a technical (`check`/`behavior`) payload; those enter only via the deterministic marker parser.
- **INV-2 / INV-3 (ADR-079):** unchanged.
- **INV-4 (new, compile-only — operator-ratified 2026-09-07):** the Judge validates against exactly the confirmed, **self-contained** `criteria ∪ dod` and receives **NO** raw workspace/project instructions. Instructions are loaded into the COMPILE call only (to derive the DoD). Every criterion and DoD item MUST restate any convention detail it depends on, since the Judge has nothing else. Compiled-against == judged-against with no live-re-read / no drift surface.

## Regrill record (one adversarial pass, findings corrected in-place)

- **C1 (CRITICAL) — the `judgment`/`kind` name collision would confuse the Judge and the parser if either field were called "kind".** A criterion object with two "kind" fields, or a merged field, would make `InferCriterionKind` and the new `InferJudgment` ambiguous and would corrupt `sameShape`/`criterionKey` identity. **Corrected in the design itself:** the new field is named `judgment` (never "kind"); the two are documented as orthogonal axes with a deterministic correlation (`check→boolean`, `behavior→quantitative`, `prose→author-stated/boolean-default`); `sameShape`/`criterionKey` explicitly incorporate `judgment`; a required contract-parity test pins required-delta = {kind, judgment}. No collision remains.
- **C2 (CRITICAL) — making `judgment` required on the canonical schema breaks every read of pre-ADR-080 persisted criteria.** `AcceptanceCriterion` is `additionalProperties: false` and required-field-checked by the contract tests; legacy task/plan/goal JSON has no `judgment`, so re-serialization would be schema-invalid and `normalizeCriteria` round-trips would fail. **Corrected:** a load-time `InferJudgment` backfill in `normalizeCriteria` (check→boolean, behavior→quantitative, prose→boolean) guarantees every criterion carries a valid `judgment` before it is ever re-serialized — the exact pattern that already makes `kind` always-present. Required-test added.
- **M1 (MAJOR) — "reject compound X and Y lines" is not schema-enforceable and was overclaimed.** The parser can enforce one `judgment` per object but cannot detect a compound sentence under a single tag. **Corrected:** the ADR now states two-layer enforcement (schema: one judgment/object; prompt + bar: one thing per line) and records true compound detection as an LLM-authoring-quality **holdout**, not a guarantee — no overclaim.
- **M2 (MAJOR) — D-CONTEXT2 could let the Judge invent gates the setter never approved, breaking "confirmed == judged".** Feeding raw workspace instructions to the Judge risks it applying conventions that were not compiled into the confirmed DoD. **Corrected:** INV-4 fixes the pass/fail set to the confirmed `criteria ∪ dod` and demotes instructions to interpretive-only context; the DoD items derived from layer-2 at compile time are what the Judge scores, and the raw instructions only help interpret them.
- **M3 (MAJOR) — D-CONTEXT2 budget: 256 KB instructions dwarf the 20000 window and hit every Judge round. → RESOLVED by operator ratification 2026-09-07 (Q1): compile-only.** Instructions are loaded into the COMPILE call ONLY (consistent with a normal turn's cost, which already injects the same note); the **Judge pays nothing** (it never loads instructions). The "cap smaller than 256 KB" question is moot for the Judge and settled for the compile (reuse 256 KB, one interactive call).
- **M4 (MAJOR) — D-TYPES could over-reject legitimate qualitative outcomes.** If the prompt/schema pressures the LLM toward `quantitative`/`artifact`, honestly-subjective outcomes get fabricated numbers or get rejected. **Corrected:** the design makes `boolean` the explicit catch-all for any yes/no or subjective outcome; the `define-goal` bar forbids manufacturing numbers for taste; "reject" targets the *untaggable/vague*, and every subjective outcome is taggable as `boolean`. The compile prompt must state `boolean` is the default for subjective/yes-no outcomes (prompt-wording requirement recorded).
- **M5 (MAJOR) — D-SKILL rename can strand already-seeded installs three ways** (allowlists still say `define-done`; the compile loader looks for the new path; the old skill dir orphans). **Corrected:** the `adr080-define-goal-rename` marker rewrites `define-done→define-goal` in non-nil/non-empty allowlists (nil/[] untouched), `SeedDefaults` seeds the new dir automatically, and the migration **deletes the orphaned old dir** (operator-ratified 2026-09-07, Q3; accepted caveat that it removes operator edits to the built-in skill). The SKILL.md frontmatter/heading fix is pinned to land atomically with the dir+symbol+allowlist rename to avoid a name↔path↔allowlist desync.
- **m1 (MINOR) — `Goal.dod` required-vs-optional. → RATIFIED schema-required 2026-09-07 (Q2).** `Goal.dod` is `minItems: 1` at the schema layer PLUS a load-time floor-DoD backfill migration that runs before validation, so legacy goals validate rather than fail the read. (The draft's "optional-in-schema" posture is superseded.)
- **m2 (MINOR) — `provenance` field only meaningful for DoD items pollutes the shared `AcceptanceCriterion`.** Accepted: additive-optional, absent/ignored elsewhere, never required — the lightest structured way to satisfy "inferred items SHOWN for approve/drop"; a textual-only marker was rejected as un-actionable.
- **m3 (MINOR) — `definition` wire home.** Reuses the existing `Goal.definition` (already "the compiled SMART restatement") and adds an additive-optional `definition` to `GoalStatusFrame`; no invented field name.
- **m4 (MINOR) — asyncapi inline-duplicate drift.** `judgment`/`provenance`/`dod` on the inline `AcceptanceCriterion` copy inside `GoalStatusFrame` must be hand-synced; named in the PR checklist obligation.

No CRITICAL/MAJOR remains open after correction.

## Regrill — post-integration pass (over the fully-updated ADR + both specs; findings corrected in-place)

A second adversarial pass after the specs were folded found three MAJOR seams the first pass missed and three minors. All corrected above.

- **R-C1 (MAJOR) — the DoD was defined, confirmed, and never judged.** The first draft specified `Goal.dod`/`GoalStatusFrame.dod` and "the Judge scores criteria ∪ dod" but never named the SEAM that unions `dod` into the judged set. Verified: `runVerifierAdjudication` (`verifier_adjudication.go:868`) sources the goal's `Criteria` only; `CompiledGoal` (`goal_compile.go:82`) has no `DoD` field. **Corrected (D-DOD "judged-set union seam"):** `CompiledGoal` gains `DoD`; the goal-adjudication criteria assembly feeds `Criteria ∪ DoD` to the Judge, each DoD item getting its own per-criterion verdict; an unmet DoD item fails the round. Required-test added (spec test 26/29).
- **R-C2 (MAJOR) — a channel setter would confirm without seeing the DoD or its inferred gates.** The first draft put DoD/inferred rendering on the web `GoalEchoCard` only. Verified: `formatGoalEcho` (`goal_compile.go:502`) is the SINGLE echo renderer shared by the card, the channel plain-text echo, and `buildGoalPendingNote`. A channel user confirms by typing against that text echo. **Corrected (D-STATEMENT/D-DOD echo paragraphs):** `formatGoalEcho` renders statement + criteria + a distinct DoD block with `provenance == inferred` flagged, on every surface — so "SHOWN, never silently invented" holds on channels too, not just web.
- **R-C3 (MAJOR) — instructions-to-Judge is partly redundant with the self-containment bar and can drift live from compile time. → SUPERSEDED by operator ratification 2026-09-07 (Q1).** The concern is now moot: the Judge receives NO workspace/project instructions at all (compile-only). Self-containment of every criterion/DoD item is promoted from "secondary backup" to a hard requirement (INV-4); there is no Judge re-read and therefore no drift surface. The deferred "compile-time snapshot" hardening is dropped (nothing to snapshot for the Judge).
- **R-m1 (MINOR) — boolean-default imprecision on the tool/REST authoring path.** An omitted `judgment` on a *prose* criterion authored via `create_task`/`create_plan` defaults to `boolean`, which is imprecise for a quantitative/artifact prose statement (the server can't infer it from text). **Disposition:** accepted, presentation-only (it does not reroute adjudication — the criterion is still `kind: prose`, LLM-judged), and consistent with ADR-074 D5's deliberate removal of a forced kind control from the editor (re-adding a required judgment dropdown would re-introduce exactly that friction). The goal-compile path has NO default — the LLM states judgment explicitly and untagged criteria are rejected.
- **R-m2 (MINOR) — the added `judgment` requirement widens the whole-compile schema-error surface.** A single untagged compiled criterion fails the whole compile response (ADR-079's existing whole-response schema-error posture) → one repair → fallback loses all good LLM criteria. Accepted: the behaviour is unchanged from ADR-079 (any malformed criterion already does this); the one repair attempt is the mitigation; noted, not widened.
- **R-m3 (MINOR) — the asyncapi inline-sync obligation grew.** `GoalStatusFrame`'s inline `AcceptanceCriterion` duplicate in `asyncapi.yaml` must now carry `judgment` + `provenance` on its inline criteria items AND the new inline `definition` + `dod` fields. **Corrected:** the PR-checklist inline-sync obligation is enumerated explicitly (four inline additions), extending the standing two-place note on `GoalStatusFrame.yaml`.

## Open questions — ALL RESOLVED (operator-ratified 2026-09-07)

1. **Instructions cap / who gets instructions — RESOLVED: compile-only.** Workspace/project instructions load into the COMPILE call ONLY; the Judge receives none and validates against the self-contained `criteria ∪ dod`. The cap question is moot for the Judge and settled for the compile (reuse 256 KB, one interactive call). Folded into D-CONTEXT2 / INV-4.
2. **`Goal.dod` schema hardening — RESOLVED: schema-required.** `Goal.dod` is `minItems: 1` at the schema layer, with a load-time floor-DoD backfill migration that runs before validation so legacy goals validate rather than fail the read. Folded into D-DOD.
3. **Orphaned `define-done/` skill dir — RESOLVED: delete.** The rename migration deletes `$OMNIPUS_HOME/skills/define-done/` after seeding `define-goal` (accepted caveat: removes operator edits to the built-in skill). Folded into D-SKILL.

## Required regression tests (additions to ADR-079's set)

1. **D-TYPES inference:** `InferJudgment` table — `check→boolean`, `behavior→quantitative`, `prose→boolean`; explicit `judgment` mismatching a technical `kind` → 400; explicit `judgment` on prose honoured.
2. **D-TYPES backfill:** a pre-ADR-080 persisted criterion (no `judgment`) loads, `normalizeCriteria` backfills it, and it re-serializes schema-valid.
3. **D-TYPES contract:** field-set parity Input↔canonical with required-delta {kind, judgment}; generated TS emits `judgment` optional on Input, required on response; `additionalProperties:false` still holds.
4. **D-TYPES identity:** `sameShape`/`criterionKey` treat two criteria differing only in `judgment` as distinct.
5. **D-STATEMENT:** clear branch requires non-empty `definition`; card renders it above the criteria; ambiguous branch has no `definition`.
6. **D-TYPES compile gate:** a compiled criterion object lacking a valid `judgment` → schema error → repair/fallback; a `{text, judgment}` object with no technical payload passes INV-1.
7. **D-DOD:** clear branch requires non-empty `dod[]`, each item judgment- and provenance-tagged; the floor guarantees ≥1 even when goal+workspace say nothing; `Goal.dod` schema `minItems: 1` rejects an empty array; a legacy goal with no `dod` backfills the floor at load and then validates.
7a. **D-DOD judged-set union (R-C1):** the goal-adjudication criteria assembly feeds `Criteria ∪ DoD` to `runVerifierAdjudication`; each DoD item receives a per-criterion verdict; an unmet DoD item fails the round (fixture proves a DoD-only failure fails the goal).
8. **D-DOD/D-STATEMENT echo on every surface (R-C2):** `formatGoalEcho` renders statement + criteria + a distinct DoD block with `provenance == inferred` flagged — asserted on the channel plain-text echo, the web card, and the ADR-078 pending note (one shared renderer).
9. **D-CONTEXT2 compile:** the compile input carries the workspace-instructions note (system message, trusted) on initial/resume/repair, distinct from the untrusted window; empty when no workspace resolves.
10. **D-CONTEXT2 Judge + INV-4 (compile-only):** `buildJudgeUserContent` carries **NO** workspace/project-instructions section (a guard asserts the Judge input has no such section); the Judge scores exactly the enumerated `criteria ∪ dod`; a DoD item derived from a workspace convention is self-contained (its `text` restates the convention detail) so it is judgeable with no instructions present.
11. **D-SKILL migration:** `adr080-define-goal-rename` rewrites `define-done→define-goal` in a non-empty allowlist once; nil and `[]` untouched; second boot byte-identical; fresh install seeds `define-goal` directly; marker stripped from `GET /api/v1/config`.
12. **D-SKILL loader:** `loadDefineGoalSkillContent` reads `skills/define-goal/SKILL.md`; compile proceeds without it when absent (graceful, per ADR-079 D2).

## Consequences

**Positive**
- The setter confirms a **restated one-sentence goal + judgment-typed criteria + an explicit DoD**, compiled against the workspace conventions (loaded at compile) and then judged as a **self-contained** unit — the Judge needs nothing but the confirmed `criteria ∪ dod`, so compiled-against == judged-against with no drift.
- The two "kind" concepts are cleanly separated (`kind` = mechanism, `judgment` = claim shape), with a deterministic correlation that makes `judgment` cheap to infer and migration-safe.
- One authoring skill (`define-goal`) governs all three parts; instructions flow through exactly one path (`buildWorkspaceInstructionsNote` into the compile only).

**Negative / risks**
- **First goal-related Constraint #8 wire change** (reverses ADR-079's "zero new wire types"): `judgment` (+ `provenance`, `dod`, `definition`) touch the shared criterion schema and the goal frames — blast radius enumerated, atomic per D-ORDER, asyncapi inline copy hand-synced.
- **Compile cost** rises (instructions + window) — a Phase-2 calibration metric; the **Judge cost is unchanged** (it loads no instructions, operator-ratified compile-only).
- **Self-containment is now mandatory for every DoD item** (the Judge has no instructions to fall back on) — a real authoring burden pushed onto the compile prompt + `define-goal` bar, and an LLM-authoring-quality holdout.
- **`judgment` correctness and compound-line avoidance are LLM-authoring-quality holdouts**, not schema guarantees — stated honestly.
- **`provenance` slightly pollutes the shared criterion type** (DoD-only meaning) — accepted as the lightest actionable surface.

## Deferred (tracked on acceptance)
- Judgment-kind, compound-line, and **DoD self-containment** calibration (holdout metrics alongside ADR-079 D2's clarity holdout and ADR-074 D6's).

(Open questions 1–3 are RESOLVED, not deferred — folded into D-CONTEXT2/INV-4, D-DOD, and D-SKILL respectively. The former "compile-time instruction snapshot" deferral is dropped: the Judge loads no instructions, so there is nothing to snapshot.)
