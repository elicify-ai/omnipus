# Judgment-first acceptance criteria — implementation spec

- **Status:** Draft **v3** (2026-09-05). Grill round 1 (0C/14M) corrected in v2; grill round 2 (0C/8M/7m/2o — concentrated in four v2 corrections and the new state machine) corrected here. The mandated two grill rounds are complete; a light round 3 is recommended but optional.
- **Authoritative parents:** ADR-074 r3 (grilled twice); design doc `docs/internal/design/judge-plansupervisor-target-design.html`. On conflict, ADR-074 wins; this spec adds testable requirements and precision — it does not re-decide. (R2-02 was exactly such a re-decision and is reverted below.)
- **Scope:** ADR-074 D1–D8, with: (a) D6 calibration metrics observational (only FR-014's narrowed hook here); (b) **the Judge-rubric quote emission explicitly IN delivery scope** (R2-01): the quote-emitting `JudgeDefaultRubric` (and companion `PlanSupervisorDefaultRubric` edits) from the 2026-09 prompt-review session exist today only as **uncommitted working-tree changes to `pkg/coreagent/core.go`** — they MUST be committed in a rollout slot before or with FR-010's parse half, else the parse half wires to a rubric that never emits a quote and no test notices. **Out of scope** (tracked): all-check gate for `create_plan`/`plan_correct`/REST; plan-scope evidence for non-file artifacts; hard skill-trigger gate; `update_task`/`update_plan` skill trigger; PlanSupervisor wake `evidence_quote` enrichment (FR-010's UNTRUSTED-re-emission rule binds that deferred work); unvetted-authoring-rate metric (R2-08 — deferred with the other Phase-2 calibration items).

---

## 0. Cross-cutting security invariants

**INV-1 (MUST):** the `/goal` LLM compiler emits **prose criteria only**. Technical (`check`/`behavior`) criteria enter a goal exclusively through the deterministic marker parser, byte-identical to today's output — the LLM never authors, rewrites, or paraphrases them.
**INV-1b (MUST, scoped — R2-05):** on the **LLM path** (any prose or mixed goal), no technical payload activates without having been displayed verbatim in the confirmation surface. **Marker-only goals are exempt with rationale:** the author IS the user, the expansion is deterministic and documented (`[tests]` → `go test ./...`), and the path is pinned unchanged; a MUST that a pinned P0 path violates would be resolved arbitrarily. (Displaying expansions on the marker-only path is a possible future ADR amendment, not required here.)
**INV-2 (accepted-mitigation statement — R2 unasked-Q1):** LLM-compiled prose criteria land in the Judge's criteria section, which is not UNTRUSTED-wrapped. The accepted mitigations are (a) the human reading the confirmation surface before activation, and (b) the Judge's existing skepticism rules (claim-last, fail-closed, quote-before-verdict). This is stated as the deliberate posture, not an oversight.

## 1. User stories

### US-1 (P0) — Author criteria in plain language, no forced classification
Scenarios (trace FR-001..004):
1. `create_task`, criterion `text` only → persisted `kind: prose`.
2. `text` + `check` payload, no kind → `check`; assignee `bash: deny` + all-(inferred)-check → rejected by the D2-rule-5 gate (both gated tools).
3. `text` + `behavior` payload (`min_count: 0`, `max_count: 0`), no kind, via **each of the four tools** → `behavior`, explicit zero preserved.
4. Explicit `kind: prose` + `check` payload → 400.
5. `create_plan` schema: `kind` not required; enum lists all three.
6. REST **update** replacement criteria with omitted kind → inferred identically.

### US-2 (P0) — Judge input: criteria first, machine results as context, claim last
Scenarios (trace FR-005):
1. Order: `extraContext` (leading, unchanged) → criteria → diff → window → machine-results (re-headed as supporting context) → claim LAST, UNTRUSTED-framed.
2. The three directional back-references rewritten (verified sites: diff header, window header, empty-window fallback).

### US-3 (P0) — Prose `/goal`: rewrite → (≤1 question) → echo → confirm; marker-only unchanged
Scenarios (trace FR-006/007, INV-1/1b):
1. `/goal <prose>` → admission check → LLM compile (prose-only) → pending goal + itemized echo → activation only on confirmation → round 1.
2. Mixed `/goal [tests] plus prose` → LLM path; marker criterion byte-identical to deterministic parse; prose remainder LLM-authored prose.
3. Marker-only → today's path pinned: deterministic, immediate, same-turn, zero LLM calls (INV-1b exempt).
4. LLM failure/timeout → deterministic fallback → **still pending + echo + confirm**; observable: WARN log with reason, fallback counter, one echo line noting no quality-bar rewrite.
5. Feasibility veto → one repair → second veto → deterministic fallback (pending+confirm) — and the fallback may itself reject (D9/hedging/no-criteria), always plain-language; silent failure is the prohibited outcome.
6. Admission checked pre-compile; authoritative `Admit` at confirm (`confirmPendingGoal` re-check — verified real).
7. **Clarifying question:** compile returns `oneOf {criteria[], clarifying_question}`. On question: no pending criteria; a **pending-clarification record** (session meta: original intent + question) persists; question renders in chat. Max one round. The resumed compile (intent+question+answer) **gets its own single repair attempt**; whole-episode LLM budget ≤ 4 calls (compile, repair, resume, resume-repair) — R2 unasked-Q4 pinned.
8. Amendment paths: active-goal restate stays deterministic this phase (tracked follow-up); restate over pending replaces the pending compile.
9. **Reply routing (R2-06, the state machine's interception mechanism):** a pre-LLM hook in the turn path (the `applyGoalCommandPrompt` seam), keyed on the pending record in session meta, intercepts messages when a pending state exists. Taxonomy:
   - **Pending-confirm:** `/goal confirm` (command form) OR a bare message that is exactly one token from `confirmGoalAliases` (confirm/yes/ok/okay/activate/y) → activates. A bare **non-confirm** reply → ordinary chat passthrough; the pending goal stays pending (explicitly NOT a restate, NOT a discard — a routine chat message must never silently mutate goal state). `/goal <new prose>` → replaces pending. `/goal clear` → discards.
   - **Pending-clarification:** the next **ordinary chat message — whatever it says, including confirm-words —** is the answer feeding the resumed compile (nothing is confirmable yet). `/goal confirm` here → informative reply "answer the pending question first (or `/goal clear`)" — never "No pending goal to confirm". `/goal clear` and `/goal <new prose>` both discard the clarification record (R2-10).
   - **Double-send while a compile is in flight:** turn serialization already queues the second message; it is then processed under the taxonomy above (R2 unasked-Q2, stated).
10. **Pending-state lifecycle (R2-09/11/17):** pending-confirm and pending-clarification records are covered by the goal idle-expiry sweep (same TTL policy; the sweep's empty-condition skip is extended to check the pending fields); `/goal` status during a pending state reports it ("goal pending your confirmation" / "waiting for your answer"), never "No active goal"; on gateway restart the pending record persists in session meta and the pill frame is re-emitted by the boot sweep (mirroring the documented `re-planning` reconstruction precedent).

### US-4 (P1) — `define-done` everywhere an LLM authors criteria
Scenarios (trace FR-008/009):
1. Fresh install → skill file + roster allowlists + PlanSupervisor `{plan, define-done}`.
2. Existing install, no marker → one atomic config write (marker + appends together); second boot byte-identical.
3. Nil and operator-emptied `[]` allowlists untouched.
4. PlanSupervisor tamper → reverts to exactly `{plan, define-done}`; three tamper cases still fail any other widening.
5. Goal-compile loading: `define-done` content injected engine-side into the compile call, independent of the goal-bearing agent's allowlist — **tested** (R2-07): a curated-allowlist agent's prose `/goal` compile input contains the quality bar.
6. **Marker wire posture (R2-04, corrected):** `GET /api/v1/config` marshals the whole config with only credential-shaped redaction, so an in-config marker WOULD cross the wire. **Decision:** `seeded_skill_grants (renamed from seeded_skill_grants at implementation: the ADR-067 greenfield gate token-scans pkg/config for migration machinery; the marker is a seed record, not a migration path)` is stripped from the config response (added to the existing response-scrub path) — **tested**: the config endpoint's response JSON lacks the key while config.json on disk carries it.

### US-5 (P1) — Verdicts read as judgment; the quote is real
Scenarios (trace FR-010/012):
1. Judge JSON with `evidence_quote` → persisted → wire → inert quoted render under the reason. **Emission prerequisite:** the quote-emitting rubric (currently uncommitted, see Scope) lands first; test 18's fixture matches the rubric's declared response shape, and the parser-side contract comment in `judge.go` (still declaring `{"id","met","reason"}`) is updated in the same commit (R2-15).
2. Quote > 500 **code points** → rune-safe truncation (never splitting a rune); multi-byte boundary fixture.
3. Fail-closed / pre-D7 / old-soul verdicts → no quote line.
4. Reason renders at criterion-text size.

### US-6 (P1) — The goal confirmation surface shows what I'm agreeing to
Scenarios (trace FR-011):
1. Pending prose goal (2 prose + 1 marker check) → card: 3 rows, text first, verbatim command chip on the check row.
2. Wire shape: breakdown `$ref`s `AcceptanceCriterion` (no third criteria shape; `Goal.yaml` alignment; the `GoalStatusFrame` canonical copy is inline in asyncapi.yaml and hand-synced — two-place edit named in the PR checklist).
3. **Pill state (R2-02, corrected — ADR wins):** the pending state emits **`queued`**, giving `GoalThreadTailCards`' existing (currently-dead) `queued` filter its first real occupant exactly as ADR-074 D4a states. The enum's description is amended (description-only) to cover "compiled, awaiting user confirmation — not yet admitted"; the semantic tension with "admitted to the global cap" is noted for a possible ADR r4 cleanup, not resolved here. **Negative test kept from R2-03's concern:** a G-5 `waiting_on_user` pause frame (active goal, mid-run) must NOT render the confirm card.
4. Chat echo itemizes identically; no `[kind]` tokens (negative assertion).

### US-7 (P2) — Criteria authoring UI + agent-draft confirmation
Scenarios (trace FR-012):
1. Editor: text + Add, no expander → prose (no kind control).
2. Expanders → check / behavior payloads (explicit 0 preserved).
3. Untouched plan Save → no `dod` PATCH.
4. Agent-drafted criteria in Create Task/Plan flows → confirmed via shared `CriteriaBreakdown` (same component as US-6).

## 2. Edge cases

EC-1 kind-omitted + both payloads → 400. EC-1b explicit kind + both payloads → 400. EC-2 text {0 → 400 (R2-13), 1000 → ok, 1001 → 400} runes through the Input type. EC-3 clarification per US-3 S7/S9. EC-4 `agentInst == nil` → fallback, no LLM. EC-5 restate over pending replaces; over clarification discards. EC-6 old-soul JSON without the field → parse ok, empty, no render. EC-7 post-marker new core agent → creation-time seeding. EC-8 REST create kind-omitted check payload agent-assigned → accepted (ungated, pinned, tracked).

## 3. Behavioral contract

- Kind-less criteria: one inference implementation, before any kind-keyed gate on the gated tools.
- Judge input: extraContext → criteria → diff → window → machine-context → claim-last-untrusted.
- Prose `/goal`: admission-checked prose-only compile → ≤1 question → pending + echo → confirm-only activation; every prose path (fallbacks included) ends at the confirm gate; marker-only unchanged.
- Compile failures: observable fallback (WARN + counter + echo note); rejection always visible and plain-language; silent failure prohibited.
- Verdicts: reason + bounded quote persisted and rendered primary; UNTRUSTED framing on any re-emission (binds deferred wake work).
- A routine chat message never silently mutates goal state (only the exact confirm taxonomy above does).

## 4. Non-behaviors & machine-verifiable constraints

**Prohibitions:** No technical check required anywhere. Claim never evidence/instruction. Inference never overrides explicit kind; never runs post-gate on gated tools. LLM compiler never authors technical payloads (INV-1). Migration: nil/[] untouched, marker-keyed once, atomic, marker stripped from the config wire. No `[kind]` labels user-facing. No OpenAPI `default:` on `AcceptanceCriterionInput.kind`.

**Machine-verifiable:** Input/response field-set parity, `required` delta = {kind}; TS kind optional (Input) / required (response) + `minLength: 1` pinned (R2-13); `evidence_quote` optional `maxLength: 500`, absent from fail-closed constructors; input-order guard incl. extraContext-leading + claim-last; four tool schemas full enum, `required: ["text"]` post-D3b; PlanSupervisor allowlist exact; marker present post-upgrade + second-boot byte-identical + absent from `GET /api/v1/config` response; goal breakdown `$ref AcceptanceCriterion`. **FR-002 call-site pinning (R2-12):** the grep-guard allowed set is exactly {`parseCriteriaArgs`, the sysagent twin, `normalizeCriteria`}; the ~10 gateway conversion sites pass an absent kind **through** (empty) to `normalizeCriteria` — they do NOT call the helper; ADR D2's "converting via `InferCriterionKind`" is read as "resolving through it downstream", and this spec pins the precise mechanics.

## 5. Test datasets

| DS | Input | Expected |
|----|-------|----------|
| DS-1 | kind absent × {none, check, behavior, both} | prose / check / behavior / 400 |
| DS-2 | explicit kind × mismatched payload (3) | 400 each |
| DS-3 | text {0, 1, 1000, 1001} runes via Input | 400 / ok / ok / 400 |
| DS-4 | behavior min/max {absent→1, 0/0, 0/3, 3/1} | default / never-call / range / 400 |
| DS-5 | quote {"", 500cp, 501cp multi-byte boundary} | no line / intact / rune-safe 500 |
| DS-6 | goals {marker-only, mixed, prose, hedging} | immediate / LLM+byte-identical marker / LLM / question-or-plain-rejection |
| DS-7 | allowlists {nil, [], curated, seeded} × migration | untouched / untouched / +define-done / +define-done |
| DS-8 | pending-state replies {bare confirm-token, bare other, /goal confirm, /goal clear, /goal new-prose} × {confirm, clarification} | per US-3 S9 taxonomy (10 cells) |

## 6. TDD plan

| # | Test | Level | Traces | Notes |
|---|------|-------|--------|-------|
| 1 | InferCriterionKind table (DS-1/2) | Unit | US-1 S1-4 | first |
| 2 | Parsers decode behavior (DS-4) | Unit | US-1 S3 | both parsers |
| 2b | Behavior end-to-end through each of the 4 tools | Integration | US-1 S3 | ADR #5 |
| 3 | Gate fires, kind-omitted all-check, both gated tools | Unit | US-1 S2 | ADR #1 |
| 4 | Persisted kind always non-empty valid | Unit | US-1 | ADR #4 |
| 5 | 4 tool schemas (enum/required per phase) + define-done directive string | Unit | US-1 S5, FR-009 | |
| 6 | Contract parity + TS optionality + minLength (DS-3) | Contract | §4, EC-2 | ADR #11 |
| 7 | REST create+update inference + pinned EC-8 | Integration | US-1 S6, EC-8 | |
| 8 | Input-order guard: extraContext→criteria→diff→window→machine→claim (NEW) | Unit | US-2 S1 | |
| 9 | Back-reference rewrites (3 sites) | Unit | US-2 S2 | |
| 10 | Marker-only: no LLM, immediate | Integration | US-3 S3 | pinned |
| 11 | Prose: pending+echo (no `[kind]`)+confirm; admission pre-compile | Integration | US-3 S1,S6 | |
| 11b | Mixed: marker byte-identical through LLM path | Integration | US-3 S2 | |
| 11c | Compile schema rejects technical kinds (INV-1) | Unit | INV-1 | |
| 12 | LLM failure → fallback → pending+confirm; WARN+counter+echo note | Integration | US-3 S4 | |
| 13 | Veto → repair → fallback; plain-language rejection | Integration | US-3 S5 | |
| 14 | Clarification: record, taxonomy (DS-8 clarification column), one round, resumed-compile repair budget | Integration | US-3 S7,S9 | |
| 14b | Amendment pins: active deterministic; pending replace; clarification discard on clear/restate | Integration | US-3 S8, EC-5 | |
| 14c | Pending-confirm reply taxonomy (DS-8 confirm column) incl. bare-non-confirm passthrough leaves pending intact | Integration | US-3 S9 | R2-06 |
| 14d | Pending lifecycle: sweep expiry; status reply during pending; restart re-emission | Integration | US-3 S10 | R2-09/11/17 |
| 15 | Fresh-install seeding | Unit | US-4 S1 | |
| 16 | Migration atomic once; nil/[] untouched; second boot byte-identical; EC-7 | Unit | US-4 S2-3, EC-7 | ADR #12 |
| 16b | Curated-allowlist agent's compile input contains quality bar | Integration | US-4 S5 | R2-07 |
| 16c | `GET /api/v1/config` response lacks `seeded_skill_grants`; disk carries it | Integration | US-4 S6 | R2-04 |
| 17 | PlanSupervisor allowlist + 3 tamper cases | Unit | US-4 S4 | ADR #7 |
| 18 | Quote round-trip (DS-5), truncation, empty-render, EC-6, parser contract-comment updated | Unit+Contract | US-5 | ADR #9; rubric commit precedes |
| 19 | Breakdown wire ($ref) + card render + `queued` frame emission + G-5 pause does NOT render confirm card | Contract+Component | US-6 | R2-02/03 |
| 20 | Editor prose-first; expanders; chip; untouched-save; reason size; CriteriaBreakdown flow | Component | US-7, US-5 S4 | |
| 21 | Stubbed-LLM e2e full journey | E2E | US-3+5+6 | real-LLM reserved for H-1 |
| 22 | `agentInst == nil` → fallback, no LLM | Unit | EC-4 | |

**Regression protection:** unchanged from v2 (verifier antipatterns, plan-supervisor seed [where ADR names updates], conformance shards, `TestContract_*` + two fixture updates).

## 7. Functional requirements

- **FR-001 (MUST):** kind-less criteria accepted, payload-inferred, on all four tools + REST create/update.
- **FR-002 (MUST):** one inference implementation; call sites exactly {both parsers pre-gate, `normalizeCriteria`}; gateway passes absent kind through; grep-level CI guard on the allowed set.
- **FR-003 (MUST):** four schemas full enum; parsers decode behavior; per-tool round-trip.
- **FR-004 (MUST):** response schema kind-required; Input relaxed, no `default:`, `minLength: 1`; artifacts atomic.
- **FR-005 (MUST):** input order with extraContext leading and claim last; back-references rewritten; new guard.
- **FR-006 (MUST):** prose `/goal` per US-3 S1-S10 incl. the routing taxonomy, pending lifecycle, observability, INV-1/1b.
- **FR-007 (MUST):** gate unchanged/last/only-net; one repair per compile call (initial and resumed); episode budget ≤ 4 LLM calls; all rejections plain-language; every call bounded per the round-timeout precedent.
- **FR-008 (MUST):** `define-done` embedded + seeded; atomic marker-keyed additive migration (nil/[] untouched; marker stripped from config wire); PlanSupervisor `{plan, define-done}` re-enforced; engine-side injection for goal compiles.
- **FR-009 (SHOULD):** tool descriptions direct loading `define-done` (advisory; string-asserted).
- **FR-010 (MUST):** `CriterionVerdict.evidence_quote` optional ≤500cp rune-safe, empty-safe render; **the quote-emitting rubric (currently uncommitted) lands before/with this**; parser contract comment updated; UNTRUSTED-framing binds deferred re-emission.
- **FR-011 (MUST):** breakdown wire via `$ref AcceptanceCriterion`; card + echo criteria-first, chips, no `[kind]`; pending pill = `queued` (description amended); G-5 pause never renders the confirm card.
- **FR-012 (MUST):** editor prose-first + behavior + expanders; reason at criterion-text size; `CriteriaBreakdown` for agent drafts.
- **FR-014 (SHOULD, narrowed — R2-08):** a fallback-compile counter, exposed as a structured WARN log field (log-based observability; no new metrics endpoint) — the on-call reader greps the named logger key. The unvetted-authoring-rate metric is deferred (no defined observable event without turn introspection).

## 8. Traceability

| FR | Story/Scenario | Tests |
|----|----------------|-------|
| FR-001 | US-1 S1-S4,S6; EC-1/1b/8 | 1,2,3,7 |
| FR-002 | US-1 S2; §4 pinning | 1,3 + grep guard |
| FR-003 | US-1 S3,S5 | 2,2b,5 |
| FR-004 | US-1 S5; EC-2 | 6 |
| FR-005 | US-2 S1-S2 | 8,9 |
| FR-006 | US-3 S1-S10; EC-3/4/5; INV-1/1b | 10,11,11b,11c,12,14,14b,14c,14d,22 |
| FR-007 | US-3 S5,S7 | 13,14 |
| FR-008 | US-4 S1-S6; EC-7 | 15,16,16b,16c,17 |
| FR-009 | US-4 | 5 |
| FR-010 | US-5 S1-S3; EC-6 | 18 |
| FR-011 | US-6 S1-S4 | 19, 11 (echo half) |
| FR-012 | US-7 S1-S4; US-5 S4 | 20 |
| FR-014 | US-3 S4 | 12 (counter assert) |

## 9. Ambiguity warnings

1. **RESOLVED (operator ruling, 2026-09-05 interview — ADR-074 D4b):** the clarifying question is delivered via a NEW general-purpose structured clarification agent tool (`ask_user`-style: question + context + options with descriptions + recommendation + free-text), not a plain chat message; the tool is usable by any agent for any clarification, and its own spec is a tracked dependency of US-3 S7. Until it ships, plain chat message is the explicit fallback — US-3 S7's scenarios and tests 14 target the fallback now and the tool when it lands. 2. `GoalStatusFrame` additive field vs new frame: additive-first, `$ref AcceptanceCriterion` either way, dual-copy sync obligation named. 3. `define-done` final wording: operator-reviewed at implementation. 4. Repair-prompt wording: free within FR-007. 5. Compile cost surfacing in `/goal` status: not surfaced this phase. 6. `queued` description tension: possible ADR r5 cleanup.

## 10. Holdout evaluation scenarios (external only; dev loop must not build against these)

H-1 real-LLM prose-goal journey (the only real-LLM goal run). H-2 agent defines done-ness for research task via chat. H-3 UI criterion, no expander, end-to-end. H-4 `/goal be better` → question or plain-language rejection. H-5 dead provider key → observable fallback, pending+confirm reached, no hang. H-6 pre-ADR-074 upgrade → Mia loads skill; emptied list stays; second restart no-op. H-7 transcript-only-evidence task → fair verdict with window quote. H-8 plan-DoD non-technical member via `CriteriaBreakdown` (D5.4 surface H-1 doesn't touch).

## 11. Review audit trail (R2-16)

Both grill reports are committed alongside this spec: `docs/internal/specs/reviews/judgment-first-criteria-spec-r1-review.md` and `-r2-review.md`. The F-nn / R2-nn markers in this document cite them.
