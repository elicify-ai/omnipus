# Work-First Goal Flow — Specification

- **Source brief:** `docs/internal/architecture/ADR-081-work-first-goal-flow.md` (Accepted, operator-ratified 2026-09-07, grilled once + corrected).
- **Supersedes (flow parts of):** `planning-goals-spec.md` and the `/goal` compile/confirm flow described in `judgment-first-criteria-spec.md` v4 — the RECORD shape (definition / judgment-typed criteria / DoD with provenance) from those specs is unchanged and remains authoritative.
- **Discovery status:** Phase-1 requirements were gathered and confirmed point-by-point with the operator in the 2026-09-07 design session (recorded verbatim in ADR-081 §"The ratified design conversation"); the operator ordered this spec's production and its two grill rounds. Gate satisfied.
- **Greenfield ruling (operator):** no backward compatibility with the confirm-gate mechanism; its artifacts are deleted in full (ADR-081 D9), enforced mechanically.
- **Codebase intelligence note:** GitNexus MCP tools are not connected in this session; the "Existing Codebase Context" below is sourced from the ADR-081 architect grill, which verified every symbol first-hand against `release/v0.1.1` (`fbcbc5ed`). Reference-pattern review (Phase 1.7): N/A — agent-engine subsystem, not covered by the go-implementation reference library.

---

## 1. Problem & Actors (Phase 1 summary — confirmed)

**Actors.** The operator (goal author, steers work, answers questions); the working agent (executes the goal, authors the goal record); the Judge system agent (adjudicates criteria; also hosts the fast-lane model); the keeper (the goal loop's claim-or-idle machinery: judges at idle, pushes the agent back to work); channel users (Telegram/Discord/… origins).

**Problem.** Setting a goal today costs minutes of ceremony before any work starts (two heavyweight compile calls + clarify + confirm), the confirmation UI has four traced defects (duplicate display, a card that never clears, a visible "confirm" chat bubble, a judge that adjudicates before any work exists), and the keeper wedges after its first idle push. Benchmark: Claude Code, where a goal starts instantly and structure is authored by the working model itself.

**Scope.** The `/goal` lifecycle end-to-end: activation, record authorship, clarification, steering, keeper triggering/pushing, fallback compilation, observability, and the deletion of the confirm-gate mechanism. **Out of scope:** the record schema itself (ADR-080, unchanged), the judge's verdict logic and evidence assembly (unchanged), `/loop`, plans, heartbeats (untouched except where the goal loop's own sender-gating is repaired), model/provider selection UI.

**Constraints.** Zero contract/wire changes (Constraint #8 untouched surface); single binary; the determinism of the workflow must not rest on model diligence (operator: "can not be based on the system prompt alone"); no dormant code (operator); the question tool remains web-only (standing operator ruling); INV-4 — the judge never receives workspace/project instructions (ADR-080).

**Priority.** P0 — the operator's live install is affected daily; this replaces a flow with known, reproduced defects.

---

## 2. Existing Codebase Context (Phase 1.5 — from the ADR-081 grill, verified on `fbcbc5ed`)

### Symbols Involved

| Symbol | Role in this feature |
|---|---|
| `pkg/agent/goal_loop.go::applyGoalCommandPrompt` | modifies — becomes instant activation; loses compile/pending branches |
| `pkg/agent/goal_loop.go::applyGoalPendingReply` / `applyGoalClarificationReply` | deletes (confirm branch, compile coupling); D3 state predicate replaces correlation |
| `pkg/agent/goal_loop.go::confirmPendingGoal`, `proposeGoalAmendment`, `applyGoalCompileOutcome`, `emitGoalClarificationCard` | deletes |
| `pkg/agent/goal_pending_note.go` (whole file) | deletes — call sites `pkg/agent/loop.go:9326`, `pkg/agent/midturn_budget.go:141` unwired |
| `pkg/agent/goal_compile_llm.go::compileGoalIntentLLM`, `goalCompileLLMCall`, `buildGoalCompileMessages`, `loadDefineGoalSkillContent` | re-scopes — fallback-only compile; assembly split into the per-turn injection builder |
| `pkg/agent/goal_compile.go::formatGoalEcho`, `diffGoalAmendment` | re-homes — channel record view + `/goal status`; `set_goal(mode:update)`'s differ |
| `pkg/agent/goal_compile.go::formatAmendmentEcho`, `goalEchoFallbackNote`, `isGoalConfirmVerb`, `formatGoalStatementAndCriteria`* | deletes (*unless still called by the re-scoped `formatGoalEcho`) |
| `pkg/agent/goal_triggers.go::maybeSettleGoalIdle`, `goalQuietWindowSettle`, `runGoalAdjudication`, `idleSteerDeliverer` | modifies — in-flight suppression, parked-card suppression, zero-output routing, sender stamping |
| `pkg/agent/goal_loop.go::checkGoalLoopAfterTurn` (`:1124` origin gate), `goalLoopFollowUpSenderID` | modifies — accepts keeper-originated turns (the un-wedge) |
| `pkg/agent/async_notifier.go` (`:272-286`) | modifies — idle-steer dispatch stamped as the goal-loop sender |
| `pkg/tools/` (new) `set_goal` tool | adds — validated write path over the existing record |
| `pkg/providers/protocoltypes/types.go` + the 6 native request builders (`openai_compat`, `azure`, `anthropic`, `anthropic_messages`, `bedrock`, `codex_provider`) | adds — typed `ToolChoice` (Layer 1 build); CLI providers excluded |
| `pkg/agent/loop.go` (tool-surface assembly, `runTurn` round loop ~`:9195-9269`; `injectWorkspaceInstructions` `:9346`) | modifies — per-request narrowing/forcing under the D3 predicate; goal-rubric note injection |
| `pkg/agent/midturn_budget.go` | modifies — registers the goal-rubric note; loses the pending-note entry |
| `pkg/session/unified_meta_files.go::u5GoalFile` + `pkg/session/daypartition.go::UnifiedMeta` + `pkg/session/unified.go::MetaPatch` (incl. its apply branches and the four u5 copy sites, one inside the cache-merge path) | modifies (**HIGH** — three structs + a shared cache-merge path + every `SetMeta` caller; round-2 M-5) — pending-draft fields removed (greenfield); `GoalQuestionRoundsUsed`, `GoalZeroOutputPushes`, and the persisted goal route (FR-031) added; `PendingAskJSON` untouched |
| `src/store/chat.ts::case 'goal_status'` (cite by symbol — churn rule; the `'_default'` pillKey line sits inside it) | modifies — `'_default'`-pill eviction hygiene |
| `src/components/chat/GoalEchoCard.tsx` (edited in place, NOT renamed), `GoalThreadTailCards.tsx`, `GoalAmendmentDiff.tsx`, `GoalPillTray.tsx`, `GoalIndicator.tsx`, `src/lib/toolVisibility.ts` | modifies/deletes — active-frame-keyed record card, button row deleted, queued rendering/branches deleted (8 non-test SPA files carry `queued`), dormant diff component deleted, `set_goal` added to the hidden-tools closed set |
| `pkg/agent/goal_status_criteria_wire_test.go`, `pkg/agent/conformance_design_test.go`, `pkg/session/goal_clarification_meta_test.go` + the 10 other test files in §6's inventory | rewrites/deletes — see Regression Requirements (grill M3/B5) |
| `contracts/components/schemas/GoalStatusFrame.yaml` + `contracts/asyncapi.yaml` inline duplicate + generated artifacts | modifies — description PROSE only (queued-only wording), no shape change (FR-026) |
| `pkg/config/defaults.go` + `pkg/coreagent/core.go` seeds | modifies — `set_goal` policy entries (ADR-077 two-layer) |

### Impact Assessment (grill-derived)

| Symbol modified | Risk | d=1 dependents that must be updated/tested |
|---|---|---|
| `checkGoalLoopAfterTurn` origin gate | HIGH | idle-steer path, claim path, GOAL_STATUS parsing, activity bump/re-arm |
| tool-surface assembly in `runTurn` | HIGH | policy filtering intersection, compressed-mode ToolSearch, `gracefulTerminal` nil-tools branch, native-search filter |
| providers request builders (ToolChoice) | MEDIUM | every native provider request; `extraBody` merge order in `openai_compat` |
| `u5GoalFile` field removal | MEDIUM | session meta read/write, `/goal status`, expiry sweep predicate |
| goal_status emission states | MEDIUM | SPA pill tray, record card, existing SPA tests asserting `queued` rendering |

**HIGH-risk flag:** the two HIGH rows sit inside `pkg/agent/loop.go`/`goal_loop.go` — ~11k-line churn files; implementers must anchor on symbols, not line numbers, and land regression tests named in §7 before refactoring.

### Cluster Placement

Agent engine (goal subsystem) + providers layer + SPA chat surface. Three clusters — the spec's wave plan (§9) isolates the providers-layer build (Layer 1) so the goal-flow waves do not block on it.

---

## 3. User Stories (Phase 2)

### US-1 — Instant goal start (P0)
The operator types a goal and the agent starts working on it immediately — first visible output within seconds, exactly like addressing the agent in normal chat. No compile wait, no questions gate, no confirmation gate stands between the goal and the work.
**Why P0:** this is the headline defect — minutes of dead ceremony before value — and the operator's explicit benchmark.
**Independent test:** on a clean session, issue a prose goal; measure time from submission to first streamed assistant output; verify the goal shows as active immediately and no confirmation is requested.
**Acceptance scenarios:**
1. **Given** a session with no active goal, **When** the operator submits a prose goal, **Then** the goal becomes active immediately, the agent's working response begins streaming without any intermediate approval step, and the goal indicator shows the goal as active.
2. **Given** a session with no active goal, **When** the operator submits a goal expressed entirely in explicit machine-checkable markers, **Then** the existing deterministic activation happens unchanged (no LLM involvement, criteria from markers, immediate activation).
3. **Given** the active-loop cap is already reached, **When** the operator submits a goal, **Then** the goal is refused with the cap message and nothing activates (same cap behavior as today, applied once at submission).

### US-2 — The agent authors the goal record (P0)
The working agent itself produces the formalized goal — restated statement, acceptance criteria, Definition of Done — as its opening move, and the system stores it as the authoritative record the judge and keeper enforce. The listing appears in the chat as working assumptions; nothing waits for approval.
**Why P0:** the record is what makes an Omnipus goal verifiable; without it the keeper has nothing to enforce.
**Independent test:** submit a clear prose goal; verify the structured record (statement + criteria + DoD with at least one DoD entry) exists in the session's goal state and is displayed in the chat flow, without any confirmation prompt.
**Acceptance scenarios:**
1. **Given** a clear prose goal was just submitted, **When** the agent's first move runs, **Then** the agent registers the structured record and the chat shows the restated statement, the criteria, and the DoD as working assumptions with no approval controls.
2. **Given** the agent submits a record that violates validation rules (empty criteria, unknown judgment type, missing statement), **Then** the submission is rejected back to the agent with the specific validation error and the agent retries; nothing invalid is ever stored.
3. **Given** the agent registers a record with no DoD entries, **Then** the stored record still contains at least one DoD entry (the built-in floor is applied on the way in).
4. **Given** a delegated sub-task's agent attempts to register a goal record, **Then** the attempt is refused and the parent session's record is untouched.
5. **Given** a session with no active goal, **When** an agent attempts to register a goal record, **Then** the attempt is refused (no orphan records).

### US-3 — Clarification without a gate (P0)
When the agent is not confident it understands the goal, it asks — on the web app through the existing question card; on other channels conversationally — and the operator's answers steer the record. Question budget: one question round per goal.
**Why P0:** vague goals are the norm; the old flow's clarify step was the single biggest latency contributor and must not return as a gate.
**Independent test:** submit a deliberately vague goal on the web app; verify a question card appears (not a plain text question), answer it, and verify the record registers afterwards without any further approval step.
**Acceptance scenarios:**
1. **Given** a vague prose goal on a web session, **When** the agent's first move runs, **Then** the agent asks via the question card; **When** the operator answers, **Then** the agent registers the record informed by the answers, with no confirmation step.
2. **Given** the one question round is already spent, **When** the agent would ask again, **Then** asking is no longer offered and the agent must register its best-understanding record (stating assumptions).
3. **Given** a vague goal arriving from a chat channel (not the web app), **When** the agent's first move runs, **Then** no question card is attempted (the card is web-only); the agent asks conversationally in the channel and the next operator message steers it.
4. **Given** a question card is answered by the server's auto-submit (all defaults, timer), **Then** the resume behaves identically to a human answer (the record gets registered; no step is skipped because the turn was not human-initiated).

### US-4 — Deterministic workflow enforcement (P0)
The system — not the prompt — guarantees the agent takes the record-or-ask step. On web sessions the first move is structurally forced to be one of exactly those two actions; everywhere, validation rejects bad submissions, the keeper nudges a goal that has no record, and after bounded nudges the engine produces the record itself.
**Why P0:** operator requirement verbatim: "how can that be halfway deterministic … can not be based on the system prompt alone."
**Independent test:** simulate an agent that ignores instructions (never registers); verify the keeper nudges twice and the engine then registers a fallback record — the goal never remains recordless.
**Acceptance scenarios:**
1. **Given** a goal turn on a web session with a supported provider, **When** the first model request is issued, **Then** the request offers exactly the two permitted actions and requires one of them; the model cannot respond with plain text or any other tool.
2. **Given** the agent's tool policy denies the record-writing action, **Then** no forcing occurs (the turn runs normally), a warning is logged, and the keeper/fallback layers still guarantee the record.
3. **Given** a goal that stays recordless with the turn machinery quiet, **Then** the keeper issues a nudge turn; **Given** two nudges produce no record, **Then** the engine runs one fallback compilation and registers its output, marked as engine-authored.
4. **Given** a provider integration that cannot carry structural forcing (CLI-bridged providers), **Then** the turn proceeds without forcing and the remaining layers apply unchanged.

### US-5 — Steering instead of confirming (P1)
The operator changes the goal by saying so. A direction change updates the record and the work; a restated goal replaces the working prompt. What changed is visible. No confirm ritual exists anywhere.
**Why P1:** the operator's ratified interaction model ("if a change is required I steer"); depends on US-2's record path.
**Independent test:** with an active goal and registered record, send a message changing a requirement; verify the record updates, a change summary is visible, and work continues without any approval prompt.
**Acceptance scenarios:**
1. **Given** an active goal with a registered record, **When** the operator sends a steering message, **Then** the agent updates the record, the change summary (added/removed/changed items) is observable, and no confirmation is requested.
2. **Given** an active goal, **When** the operator issues a new goal statement as a goal command (prose), **Then** the working prompt is replaced by the restated intent and the agent updates the record accordingly — immediately, no pending state.
3. **Given** an active goal, **When** the operator issues a marker-only restate, **Then** the record updates deterministically (no LLM), the feasibility veto still applies, and the diff is observable.

### US-6 — A keeper that cannot fire early and cannot wedge (P0)
The judge never adjudicates while the agent is still working and never adjudicates emptiness; when a verdict says "not met," the agent is automatically pushed back to work with the judge's missing-list; the push works every round, not just once.
**Why P0:** both live-traced defects (judge-on-empty; the one-shot wedged keeper) make goals silently fail today.
**Independent test:** run a goal whose first round exceeds the idle quiet window mid-call; verify no verdict occurs until the turn ends. Then produce a deliberate "not met" round; verify the agent is re-prompted with the missing items, and that a SECOND idle cycle can also fire (no wedge).
**Acceptance scenarios:**
1. **Given** an active goal whose session has a live in-flight turn (own turn or a delegated child's), **When** the idle quiet window elapses, **Then** no idle settlement fires; the window re-arms when the turn ends.
2. **Given** an active goal with a registered record but zero evidence, zero workspace changes, and zero transcript output (counting delegated descendants) since activation, **When** idle settlement would fire, **Then** no round is consumed and no fail-closed verdict is produced; the agent receives a bounded continue-push (at most 2 consecutive), after which normal adjudication resumes so the loop always terminates.
3. **Given** an idle settlement produces "not met," **Then** the agent receives a continuation prompt containing the unmet items, the round accounting increments, and the idle cycle re-arms so a later idle can settle again.
4. **Given** an active goal parked on an unanswered question card, **Then** neither idle settlement nor the nudge fires (the goal is waiting on the operator); the overall goal-expiry sweep still applies.
5. **Given** the agent claims completion with evidence, **Then** claim adjudication behaves exactly as today (claim-or-idle architecture unchanged).

### US-7 — Fast-lane fallback on the Judge agent's model (P1)
The only engine-invoked goal LLM call left (the fallback compilation) runs on the Judge system agent's model — one knob for all mechanical goal work. If the Judge agent has no usable provider, the degradation to the deterministic parser is visible in logs.
**Why P1:** operator-ratified model policy; low complexity; matters only on the fallback path.
**Independent test:** configure distinct models for the chat agent and Judge agent; force the fallback; verify the fallback call used the Judge agent's model. Remove the Judge's provider; verify the WARN and the deterministic fallback record.
**Acceptance scenarios:**
1. **Given** the fallback compilation runs, **Then** it resolves its model/provider from the Judge system agent, not the chat agent.
2. **Given** the Judge agent resolves to no usable provider, **Then** the fallback degrades to the deterministic parser, the resulting record is registered, and a warning names the degradation.

### US-8 — A goal path that narrates itself (P1)
Every goal lifecycle event — activation, record registration/update with validation outcome, which door the forced first move took (or why forcing was skipped), keeper nudges, idle suppression with its reason, verdicts, fallback compilation — is observable in the gateway log, without duplicating the events that already exist.
**Why P1:** the 2026-09-07 trace hit a forensic wall; this must not repeat.
**Independent test:** run one full goal lifecycle with INFO logging enabled; verify each lifecycle stage above appears exactly once with session and goal identifiers.
**Acceptance scenarios:**
1. **Given** INFO logging, **When** a goal runs end-to-end, **Then** activation, door taken, registration outcome, each keeper action, each suppression (with reason), and each verdict appear with `session_id`/`goal_id`.
2. **Given** an operationally critical event (fallback compilation, nudge exhaustion, Judge-provider degradation, forcing defeated by provider extra-body), **Then** it logs at WARN.

### US-9 — Greenfield deletion of the confirm mechanism (P0)
The confirm-gate machinery is removed in full — definitions, states, UI, tests — with a mechanical guard preventing resurrection by merge. Stale pending-draft data on disk is ignored and dropped. No dormant code remains.
**Why P0:** operator directive; dormant confirm code re-entangling the new flow is the biggest regression risk.
**Independent test:** run the guard script (must pass); grep the tree for the retired symbols (must be absent); load a session whose on-disk goal state carries a pre-upgrade pending draft (must load cleanly, draft ignored, dropped on next write).
**Acceptance scenarios:**
1. **Given** the implemented tree, **Then** none of the retired symbols exist as definitions or non-comment references, and the guard script passes (and fails when any is reintroduced).
2. **Given** an on-disk goal state containing pre-upgrade pending-draft fields, **When** the session loads, **Then** no error, no confirmation surface, and the stale fields disappear on the next write.
3. **Given** the operator sends the word "confirm" or the goal-confirm command, **Then** it is ordinary chat / an informative notice respectively — no state transition.

### US-10 — Channel users keep a record view (P1)
On chat channels (no SPA frames), the registered/updated record is delivered as a formatted text message, so channel users see the statement, criteria, and DoD.
**Why P1:** without it, channels lose all visibility into the contract (grill finding M10).
**Independent test:** run a goal from a channel session; verify a formatted record message arrives after registration and after an update.
**Acceptance scenarios:**
1. **Given** a channel-origin goal session, **When** the record registers or updates, **Then** the channel receives the formatted record text; **Given** a web session, **Then** it does not (the card is the surface there — no duplication).

### Edge Cases

| # | Condition | Expected behavior |
|---|---|---|
| E1 | Goal submitted while a question card from a PREVIOUS goal attempt is still parked | The new goal supersedes: the stale card is cancelled, the new activation proceeds |
| E2 | Two goal commands in rapid succession | Second is a restate/steer of the (already active) first — immediate, no pending state |
| E3 | Operator's provider config injects its own tool-choice via extra-body | The forced first move must win or the defeat must WARN — never silent degradation |
| E4 | Forcing would apply but the engine has no tools to offer on this request | The request must not demand a tool it does not offer — no malformed request (mechanism in §6, test 8) |
| E5 | Record registration and an idle settlement racing | Existing single-flight adjudication guard applies; registration is never lost |
| E6 | Question card answered with every answer empty | Counts as the spent question round (budget consumed), resume proceeds |
| E7 | Goal cleared while a nudge turn is in flight | Nudge turn's registration attempt refuses (no active goal); no resurrection |
| E8 | Session window trims away the original goal message | The active record is the durable authority; keeper prompts carry the condition text |
| E9 | Agent calls the record-writing action twice in one turn (register then update) | Both apply in order; the frame reflects the final state |
| E10 | `/goal status` on an active goal with a registered record | Shows condition, rounds, AND the record summary — a REWRITTEN surface (FR-029: record summary is new; the pending-draft branches are deleted) |

---

## 4. Behavioral Contract & Boundaries (Phase 2.5)

### Behavioral Contract

- When a prose goal is submitted with no active goal, the system activates it immediately and the agent starts working in the same turn.
- When the agent is confident, its first move registers the structured record; when not confident (web), its first move asks via the question card; the first move can be nothing else on web sessions with a capable provider.
- When a record submission fails validation, the system rejects it with the specific error and the agent retries; invalid data is never stored.
- When a record registers or updates, the web app renders it as an in-flow card without approval controls, channels receive it as formatted text, and the stored record is what the judge enforces.
- When the operator steers, the record updates and a change summary is observable; no approval is ever requested.
- When the session has a live turn (own or delegated child), idle settlement never fires; it re-arms at turn end.
- When idle settlement finds an active goal with no adjudicable output, it consumes no round and produces no verdict; the keeper nudges instead.
- When a verdict is "not met," the agent is automatically re-prompted with the unmet items, and the idle cycle remains capable of firing again later.
- When a goal remains recordless after two nudges, the engine registers a fallback record itself.
- When the goal-confirm word or command is received, nothing about goal state changes.

### Explicit Non-Behaviors

- The system must not display the goal twice on web sessions (text echo + card) — the card is the single web surface; duplication was traced defect #1.
- The system must not require or offer any confirmation step for activation, registration, or amendment — steering is the only control (operator ruling).
- The system must not emit the queued/pending display state anywhere — the state exists only as a retained wire-enum value.
- The system must not adjudicate a goal with nothing to adjudicate — no fail-closed massacre of empty work (traced defect #4).
- The system must not attempt the question card off the web app, at delegation depth, or in unattended runs — the tool's standing web-only ruling holds.
- The system must not feed workspace/project instructions to the judge (INV-4) nor inject them twice into work turns.
- The system must not keep any confirm-gate symbol compiled-but-unreachable — no dormant code (operator directive; guard-enforced).
- The system must not let a delegated sub-turn or a goalless session write a goal record.
- The system must not force a tool the agent's policy denies.

### Machine-Verifiable Constraints

| ID | Constraint |
|---|---|
| C-1 | The activation path issues ZERO LLM calls before the first working request — asserted by a call-counting scripted provider (test 4). Wall-clock speed lives in SC-001 only (grill M2). |
| C-2 | A stored goal record always has ≥1 criterion, ≥1 DoD entry, every judgment ∈ {boolean, quantitative, artifact}, every DoD provenance ∈ {stated, workspace, floor, inferred}. The non-empty-statement clause applies to records written via `set_goal` or the fallback compile ONLY — marker-path records legitimately carry an empty statement (round-2 B-3). |
| C-3 | On web + capable provider, the goal turn's first request offers exactly {`set_goal`, `AskUserQuestion`} ∩ policy-allowed ∩ (question budget unspent) — 1 or 2 definitions, never any other tool — with tool-choice required whenever ≥1 remains; the following request restores the full policy-filtered surface (round-2 M-2). |
| C-4 | Question rounds per goal ≤ 1; after the round is spent the ask action is absent from the offered surface. |
| C-5 | Keeper nudges before engine fallback = exactly 2; after the fallback, the record exists and is marked engine-authored. |
| C-6 | Zero idle verdicts while a matching live turn (own or delegated descendant) exists; zero rounds consumed by FR-014/FR-014b free cases; recorded-goal continue-pushes ≤ 2 consecutive before normal adjudication resumes (bounded — grill B1). |
| C-7 | After an unmet idle verdict: the continuation prompt is dispatched, activity re-arms, and a subsequent idle settlement CAN fire (asserted by a two-cycle test). |
| C-8 | After the FR-026 prose update + regen, a further `make gen-contracts` produces zero diff, `make verify-contracts` passes, and no `GoalStatusFrame` field is added, removed, or retyped (grill B5). |
| C-9 | The guard script exits non-zero if any of FR-023b's seven names reappears as a definition or non-comment reference; the tree at completion has zero occurrences of the FULL inventory (FR-023a) (grill M5). |
| C-10 | Goal lifecycle INFO/WARN events each appear exactly once per occurrence with `session_id` and `goal_id` fields (the two pre-existing lines backfilled per FR-025). |
| C-11 | On channel-ROUTED goals (per recorded routing, FR-020): exactly one formatted record message per successful register/update — including registrations on keeper turns; on web-routed goals: zero. |

### Integration Boundaries

| System | Flow | Contract | Failure behavior | Dev approach |
|---|---|---|---|---|
| LLM providers (native) | Out: requests optionally carrying a typed tool-choice + narrowed tool list | Existing provider call surface; tool-choice is additive | Provider rejects/ignores forcing → validation + keeper layers still guarantee the record; defeat via operator extra-body logs WARN | Unit tests per builder; live behavior via existing provider compliance tests |
| LLM providers (CLI-bridged) | Same, minus forcing | Tools flattened to text — forcing structurally inapplicable | Layers 2–3 only | Documented exclusion + test asserting no forcing attempted |
| Question tool/card (web) | Out: pending question set; In: answers resume | Existing tool contract; park/resume unchanged | Tool refuses off-web → conversational path | Existing registry tests + new goal-flow integration tests |
| SPA (WS frames) | Out: goal_status frames (active state now carries statement/criteria/DoD) | Existing generated frame schema — no regen | Frame dropped by zod → existing drop+counter behavior | vitest on store + card |
| Channels (bus) | Out: formatted record text | Plain `OutboundMessage.Content` | Channel down → normal channel failure handling | Unit test on the formatter + routing |
| Session store (`goal.json`) | In/out: goal record, rounds, question-round counter | Existing meta patch surface; pending-draft fields removed | Stale fields on disk ignored, dropped on next write | Unit tests incl. stale-file fixture |

---

## 5. BDD Scenarios (Phase 3)

Format per `bdd-template.md`. Categories: HP = Happy Path, AP = Alternate Path, EP = Error Path, EC = Edge Case.

**S-01 (HP)** — Instant activation and work start. *Traces to: US-1/A1.*
Given a web session with no active goal / When the operator submits `/goal build me a tetris game` / Then the goal state becomes active in the same turn / And the assistant's working output begins streaming / And no confirmation surface appears.

**S-02 (HP)** — Marker-only path unchanged. *Traces to: US-1/A2.*
Given a session with no active goal / When a marker-only goal is submitted / Then criteria derive deterministically from the markers / And activation is immediate with zero LLM calls.

**S-03 (EP)** — Cap refusal. *Traces to: US-1/A3.*
Given the active-loop cap is reached / When a goal is submitted / Then the refusal names the cap / And no goal state is created.

**S-04 (HP)** — First-move registration. *Traces to: US-2/A1.*
Given a clear goal was just activated on a web session / When the first model request resolves / Then the record-registration action is the response / And the stored record carries statement, criteria, and ≥1 DoD / And the chat shows the assumptions listing without buttons.

**S-05 (EP)** — Validation rejection loops back. *Traces to: US-2/A2.*
Given the agent submits a record with an unknown judgment type / Then the submission is rejected with the named violation / And no partial record is stored / And a corrected resubmission succeeds.

**S-06 (HP)** — DoD floor applied. *Traces to: US-2/A3.*
Given the agent registers with zero DoD entries / Then the stored record contains the floor DoD entry.

**S-07 (EP)** — Delegation depth refused. *Traces to: US-2/A4.*
Given a delegated child turn attempts registration / Then the action refuses / And the parent record is unchanged.

**S-08 (EP)** — Goalless registration refused. *Traces to: US-2/A5.*
Given a session with no active goal / When registration is attempted / Then it refuses and stores nothing.

**S-09 (HP)** — Vague goal → card → informed registration. *Traces to: US-3/A1.*
Given a vague goal on a web session / When the first move runs / Then the question card appears (turn parks) / When the operator answers / Then the resume's first move registers the record reflecting the answers / And no confirmation step occurs.

**S-10 (AP)** — Question budget spent. *Traces to: US-3/A2, C-4.*
Given the goal's one question round is spent / When the resume's first request is issued / Then only the registration action is offered.

**S-11 (AP)** — Channel clarification is conversational. *Traces to: US-3/A3, US-4/A4.*
Given a vague goal from a channel session / Then no question card is attempted / And the agent's question arrives as channel text / And the next operator message steers registration.

**S-12 (EC)** — Auto-submitted card resume. *Traces to: US-3/A4, E6.*
Given the card auto-submits with defaults (or all-empty answers) / Then the resume proceeds exactly as a human answer / And the question budget is consumed.

**S-13 (HP)** — Two-action forcing on web. *Traces to: US-4/A1, C-3.*
Given a goal turn's first request on a capable provider / Then the request offers exactly {record, ask} with tool choice required / And the second request restores the full surface.

**S-14 (AP)** — Policy-denied → no forcing. *Traces to: US-4/A2.*
Given the agent's policy denies the record action / Then the first request is not narrowed or forced / And a WARN is logged / And the keeper layers still produce the record.

**S-15 (HP)** — Nudge ladder to engine fallback. *Traces to: US-4/A3, C-5.*
Given an agent that never registers / Then nudge 1 and nudge 2 fire on successive quiet windows / And after nudge 2 the engine's fallback compilation registers an engine-authored record.

**S-16 (AP)** — CLI provider exclusion. *Traces to: US-4/A4.*
Given the goal session's agent runs on a CLI-bridged provider / Then no forcing is attempted and the turn proceeds normally.

**S-17 (HP)** — Steering updates the record. *Traces to: US-5/A1.*
Given an active goal with a record / When the operator sends a steering message / Then the record updates / And the change summary lists added/removed/changed items / And no approval is requested.

**S-18 (HP)** — Prose restate replaces the prompt. *Traces to: US-5/A2, E2.*
Given an active goal / When `/goal <new prose>` arrives / Then the working prompt becomes the new intent in the same turn / And the record is updated by the agent.

**S-19 (AP)** — Marker restate deterministic. *Traces to: US-5/A3.*
Given an active goal / When a marker-only restate arrives / Then the record updates without an LLM call / And an infeasible marker set is rejected with the named reason, record unchanged.

**S-20 (HP)** — In-flight suppression. *Traces to: US-6/A1, C-6.*
Given an active goal whose turn has a live in-flight model call longer than the quiet window / Then no idle settlement fires during the call / And the window re-arms at turn end.

**S-21 (HP)** — Zero-output idle consumes nothing. *Traces to: US-6/A2, C-6.*
Given an active goal, registered record, no output since activation / When idle fires / Then no verdict, no round consumed / And a nudge is dispatched instead.

**S-22 (HP)** — The push un-wedged, two cycles. *Traces to: US-6/A3, C-7.*
Given an idle verdict of "not met" / Then the continuation prompt with unmet items dispatches / And the goal's activity re-arms / And a second quiet window later produces a second settlement (no wedge).

**S-23 (EC)** — Parked card suppresses keeper. *Traces to: US-6/A4.*
Given an unanswered question card on the goal session / Then neither idle settlement nor nudges fire / And the expiry sweep remains in force.

**S-24 (AP)** — Claim path unchanged. *Traces to: US-6/A5.*
Given an evidence-backed completion claim / Then adjudication follows today's claim path (bounce on bare claims included).

**S-25 (HP)** — Fallback on the Judge's model. *Traces to: US-7/A1.*
Given distinct chat/Judge models / When the fallback compilation runs / Then the request uses the Judge agent's model.

**S-26 (EP)** — Judge provider missing degrades loudly. *Traces to: US-7/A2.*
Given the Judge agent resolves to no provider / Then the deterministic parser produces the record / And a WARN names the degradation.

**S-27 (HP)** — Lifecycle observability. *Traces to: US-8/A1-A2, C-10.*
Given INFO logging / When a full lifecycle runs (activation → door → registration → nudge → verdict) / Then each event logs exactly once with session and goal ids / And critical events log at WARN.

**S-28 (HP)** — Retired symbols gone, guard bites. *Traces to: US-9/A1, C-9.*
Given the implemented tree / Then the guard script passes / And re-adding any retired symbol makes it fail.

**S-29 (EC)** — Stale pending draft ignored. *Traces to: US-9/A2.*
Given an on-disk goal state with pre-upgrade pending-draft fields / When the session loads and the goal state is next written / Then no error, no confirm surface, and the stale fields are gone.

**S-30 (AP)** — "confirm" is inert. *Traces to: US-9/A3.*
Given any session state / When the operator sends "confirm" (bare) or the goal-confirm command / Then bare text is ordinary chat; the command replies with an informative notice; goal state is untouched.

**S-31 (HP)** — Channel record view; no web duplication. *Traces to: US-10/A1, C-11.*
Given a channel goal session / When the record registers and later updates / Then exactly one formatted record message per event arrives on the channel / And on a web session the same events produce frames only (no text echo).

**S-32 (EC)** — Stale card superseded by a new goal. *Traces to: E1.*
Given a parked question card from a previous goal attempt / When a new goal is submitted / Then the stale card cancels, the new goal activates, and the old resume can no longer mutate state.

**S-33 (EC)** — extra-body tool-choice collision. *Traces to: E3.*
Given a provider config whose extra-body sets its own tool choice / When forcing applies / Then the forced choice wins, or the defeat is WARN-logged — never silent.

**S-34 (EC)** — Terminal-request branch clears forcing. *Traces to: E4.*
Given the graceful-terminal branch (no tools offered) coincides with a forcing-eligible request / Then the request carries neither tools nor a tool-choice requirement.

**S-35 (EC)** — Goal cleared mid-nudge. *Traces to: E7.*
Given `/goal clear` lands while a nudge turn is in flight / Then the nudge's registration attempt refuses (no active goal) and nothing resurrects.

**S-36 (EC)** — Double write in one turn. *Traces to: E9.*
Given the agent registers then updates within one turn / Then both apply in order and the final frame reflects the update.

**S-37 (EC)** — Registration racing an idle settlement. *Traces to: E5, US-6.*
Given an idle adjudication is in flight for the goal / When a record registration lands concurrently / Then the registration is never lost (single-flight adjudication guard holds) / And the next settlement judges against the registered record.

**S-38 (EC)** — Window-trim survives. *Traces to: E8, US-6.*
Given a long-running goal whose original goal message has been evicted by window trimming / When the keeper dispatches a push or nudge / Then the prompt carries the goal condition text from the durable record (not the trimmed transcript) / And adjudication still reads the full record.

**S-39 (HP)** — `/goal status` shows the record. *Traces to: E10, US-2, FR-029.*
Given an active goal with a registered record / When `/goal status` runs / Then the reply contains condition, rounds, and the record summary (statement + criteria + DoD) / And no pending-draft or confirm phrasing appears.

**S-40 (EC)** — Pre-upgrade active goal continues. *Traces to: US-9, FR-030.*
Given an on-disk goal state with an active condition and a populated record written by the old compile path / When the session runs under the new binary / Then no forcing and no nudge occur (predicate false) / And claim/idle adjudication proceeds on the existing record with continued rounds accounting.

**S-41 (EC)** — Clear with a parked card. *Traces to: FR-028.*
Given a question card is parked on the goal session / When `/goal clear` runs / Then the card cancels, its late resume mutates nothing, and the goal state is cleared.

---

## 6. TDD Plan (Phase 4)

Implementation detail permitted from here on.

| Order | Test | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestSetGoalTool_ValidatesAndWrites` | Unit | S-04,S-05,S-06 | table-driven: valid register; unknown judgment rejected; empty criteria rejected; floor DoD backfilled; `assessment` validated + LOGGED, and asserted ABSENT from the persisted record (round-2 B-2) |
| 2 | `TestSetGoalTool_ScopePreconditions` | Unit | S-07,S-08 | refusal at `ToolDelegationDepth>0`; refusal with `GoalCondition==""`; no write on refusal |
| 3 | `TestSetGoalTool_UpdateDiffs` | Unit | S-17,S-36 | `mode:update` re-validates, `diffGoalAmendment` output in result; register→update ordering |
| 4 | `TestGoalActivation_InstantProsePath` | Unit | S-01,S-03 | `applyGoalCommandPrompt`: active record minted, `opts.UserMessage` rewritten, single Admit; cap refusal; **scripted call-counting provider records ZERO LLM calls before the first working request (C-1, round-2 m-5)**; restate does NOT mint a new `GoalID` (FR-001) |
| 5 | `TestGoalActivation_MarkerPathPinned` | Unit | S-02 | deterministic path byte-identical behavior |
| 6 | `TestGoalRestate_ActiveGoal` | Unit | S-18,S-19 | prose restate rewrites prompt; marker restate deterministic update + feasibility veto |
| 7 | `TestToolChoice_TypedFieldPerBuilder` | Unit | S-13,S-33 | split by builder SHAPE (round-2 m-2): `openai_compat` string-key body (+ extraBody ordering: forced wins or WARN); `azure` SDK typed union mapping; `anthropic`/`anthropic_messages`/`bedrock`/`codex_provider` — NEW tool-choice code (none exists today), one row each |
| 8 | `TestGoalTurn_ForcingPredicateAndNarrowing` | Unit | S-13,S-14,S-16,S-34,S-10,S-12 | predicate = active+empty record+web AND ignores `UserInitiated`/sender (negative rows: auto-submit resume + nudge turn still force — grill M1); intersection with policy filter; denial→no forcing+WARN; CLI provider→no forcing; gracefulTerminal clears choice with tools; compressed-mode ToolSearch force-through suspended on the narrowed request (grill m5); budget-spent→`{set_goal}` only |
| 9 | `TestGoalRubricInjection_OncePerGoalTurn` | Unit | S-04 | note injected only under the predicate; no workspace-instructions double-inject; registered in midturn budget |
| 10 | `TestIdleSettlement_InFlightSuppression` | Unit | S-20 | live turnState matching transcriptSessionID (incl. delegated child) suppresses; re-arm on end |
| 11 | `TestIdleSettlement_ZeroOutputBoundedPushes` | Unit | S-21,S-37,S-38 | FR-014b triple (incl. descendant-transcript counting — delegated-work row must NOT match; goal-scoped diff — co-tenant-workspace row must NOT mask); ≤2 consecutive free pushes asserted against the PERSISTED `GoalZeroOutputPushes` field (restart between pushes → streak survives); reset on triple-false and on `set_goal` write; then normal adjudication; registration-vs-settlement race (single-flight); push prompt sourced from the durable record after window trim |
| 12 | `TestKeeper_SenderGateUnwedged` | Unit | S-22 | idle-steer stamped via the per-event `SenderCanonicalID` override passes `checkGoalLoopAfterTurn`; activity bumps; settling flag clears; **two full idle cycles complete**; negative: non-goal async sources keep `async:<kind>` (round-2 m-4); route rehydrated from the persisted record after restart (FR-031) |
| 13 | `TestKeeper_ParkedCardSuppression` | Unit | S-23,S-41 | pending ask on session → no settle, no nudge; expiry sweep unaffected (incl. FR-016b's rewritten two-term predicate row); `/goal clear` cancels the parked card via the non-resuming primitive — **asserts NO resume turn is dispatched** — late resume returns `ErrNoPending` |
| 14 | `TestKeeper_NudgeLadderToFallback` | Unit | S-15,S-35,S-32 | N=2 then fallback registers engine-authored record; cleared-goal mid-nudge refusal; new-goal activation cancels a stale parked card via `cancelOrphanedClarifyCard`'s re-homed call site — **asserts NO resume turn is dispatched** (FR-028) |
| 15 | `TestFallbackCompile_JudgeModelResolution` | Unit | S-25,S-26 | Judge instance's model used; nil provider → deterministic parser + WARN |
| 16 | `TestGoalStatusFrame_ActiveCarriesRecord` | Unit | S-31,S-02,S-19 | active emission populates definition/criteria/dod on `set_goal`/fallback; marker-path frames carry criteria/dod with `definition` deliberately absent (round-2 B-3); queued never emitted; REWRITES `goal_status_criteria_wire_test.go`'s queued-only assertion to the new rule (grill B5) |
| 17 | `TestChannelRecordEcho` | Unit | S-31 | keyed on the goal's routing via `routeFor` (reader; `recordGoalRouting` is the writer), not turn Channel — keeper-turn registration on a channel-routed goal still echoes (grill M7); route persisted + rehydrated after restart (FR-031); once per register/update; zero on web-routed; missing route → WARN + `latest_reason`, never silent |
| 18 | `TestGoalMetaGreenfield` | Unit | S-29,S-40 | stale draft fields ignored on read, absent after write; `GoalQuestionRoundsUsed` round-trips; pre-upgrade ACTIVE goal continues (predicate false, adjudication proceeds — FR-030) |
| 19 | `TestConfirmInert` | Unit | S-30,S-39 | bare "confirm" passes as chat; `/goal confirm` informative notice via the NEW inline verb match (never activates a goal named "confirm"); `/goal status` rewrite: record summary present, pending branches gone (FR-029) |
| 20 | `TestGoalEventLogging` | Unit | S-27 | each lifecycle event exactly once; WARN set correct |
| 21 | `TestGoalClarify_WebCardRoundtrip` | Integration | S-09,S-12,S-32 | vague goal → card → park → resume (human + auto-submit) → informed registration; stale-card supersession |
| 22 | `TestGoalFlow_EndToEnd_Web` | Integration | S-01,S-04,S-17,S-22 | scripted provider: activation → forced door → register → steer → unmet idle verdict → push → second cycle |
| 23 | `TestGoalFlow_EndToEnd_Channel` | Integration | S-11,S-31 | channel origin: conversational clarify, record echo text, no card |
| 24 | SPA `GoalEchoCard.test.tsx` additions (component keeps its name — edited in place, NOT renamed; grill M6) | Unit | S-04,S-31 | renders from active frame (criteria+dod), no buttons, accordion collapsed; `'_default'` pill evicted on keyed frame |
| 25 | SPA `goalStore` additions in `chat.ts` tests | Unit | S-31 | store: active frame replaces; no queued rendering path; `GoalPillTray`/`GoalIndicator` queued branches removed |
| 26 | `check-no-goal-confirm-gate` script test | Integration | S-28 | guard passes on tree; fails on each of the seven FR-023b names planted |
| 27 | `tests/e2e/goal-work-first.spec.ts` (Playwright, embedded binary; registered in `tests/e2e/shards.json` with its own shard + `key_slot` — the shard checker fails CI on drift; grill M9) | E2E | S-01,S-04,S-17 | DETERMINISTIC assertions only: streaming starts with no confirm control; whichever door the live model takes, no button row renders; the record card renders from the active frame; steering produces an updated card. Model door-choice behavior (vague→ask) is holdout H-2, not this test |

### Test Datasets

**DS-1 — `set_goal` submissions** (Traces: S-04..S-08): valid minimal (1 criterion, 0 DoD → floor); valid full (16 criteria, 4 DoD, unicode text, 10KB text field); judgment `"bool"` (invalid); judgment empty; criteria `[]`; statement empty; statement 100KB (boundary per existing meta limits); duplicate criterion texts (normalization); provenance `"guessed"` (invalid); mode update with no prior record (error); delegation-depth ctx; goalless session.

**DS-2 — forcing matrix** (Traces: S-13,S-14,S-16,S-33,S-34): provider ∈ {openai_compat, azure, anthropic, anthropic_messages, bedrock, codex_provider, codex_cli, copilot_cli} × {policy allow, policy deny} × {extraBody clean, extraBody tool_choice} × {normal, gracefulTerminal} — expected: forced / not-forced+WARN / not-forced / cleared per the D3 rules.

**DS-3 — keeper timing** (Traces: S-20..S-23, S-37, S-38, S-40): in-flight own turn; in-flight delegated child; quiet + FR-014b triple true; **quiet + parent transcript empty but delegated child transcript has output → NOT zero-output (grill B2)**; **two active goals sharing one `WorkspaceID`, goal B produced nothing → B's goal-scoped diff term still holds (round-2 B-4)**; unbound chat goal (diff term degenerates to a pair); quiet+evidence present; parked card; post-push second cycle; **gateway restart between push 1 and push 2 → persisted `GoalZeroOutputPushes` streak survives, third idle adjudicates normally (round-2 B-1)**; **restart on a channel-routed goal → route rehydrated, push + echo still delivered (FR-031)**; third consecutive zero-output idle → normal adjudication resumes (bounded); concurrent registration + idle settlement (single-flight, S-37); window-trimmed transcript + keeper push (S-38); pre-upgrade active record (S-40); restate keeps the same `GoalID` (asserted) and inherits a spent question budget (grill m6, round-2 M-7).

**DS-4 — stale goal.json fixtures** (Traces: S-29): pre-upgrade file with pending draft; with clarification record; with both + active condition; empty file; corrupted JSON (existing corrupt-file behavior pinned).

### Regression Requirements

Modifies existing functionality — preserved behaviors and their guardians:
1. Marker-only activation, claim-path adjudication, bounce economics, `waiting_on_user` pause, expiry sweep, rounds accounting: existing `goal_triggers`/`goal_loop` tests MUST pass unchanged except those asserting deleted confirm/pending behavior. **The rewrite/delete inventory (grill M3 + round-2 M-3/M-8 — enumerated HERE, wave-4 checklist, not deferred to a PR description): 14 Go test files touch the confirm/pending mechanism** — `pkg/agent/goal_loop_test.go` (19 call sites), `goal_triggers_test.go` (6), `goal_compile_test.go`, `goal_compile_card_test.go`, `goal_compile_consumption_test.go`, `goal_compile_context_test.go` (harness-coupled: its ADR-079 D1 session-window half dies with the front path; its D-CONTEXT2 workspace-instructions half is REWRITTEN against the D7 fallback, which keeps that feed), `goal_compile_dod_floor_test.go`, `goal_loop_budget_test.go`, `goal_loop_parked_gate_test.go`, `goal_pending_note_test.go` (dies with its file), `goal_status_criteria_wire_test.go` (queued-only assertion rewritten, test 16), `goal_two_phase_test.go` (defines the shared `twoPhaseHarness`/`setGoal`/`compileJSON` helpers the others import), `conformance_design_test.go` (conformance suite — outside the exception clause, reviewed line-by-line), and `pkg/session/goal_clarification_meta_test.go` (dies with the field). The shared helper `activatePendingGoal` (~33 call sites) is replaced by a direct-activation helper. SPA (round-2 M-8): exactly **4 hand-written files** carry goal-state `queued` — `GoalThreadTailCards.tsx`, `GoalEchoCard.tsx`, `GoalIndicator.tsx`, `GoalPillTray.tsx` — their queued branches are removed in wave 3; the enum value survives untouched in the 4 GENERATED files per FR-026; every other `queued` in `src/` is an unrelated sense (outbound queue, tool approval, autosave, WebRTC, plans) and MUST NOT be swept.
2. Judge evidence assembly + INV-4: `pkg/agent/judge*_test.go` incl. the INV-4 guard test — unchanged.
3. Record schema/normalization: `pkg/task/criterion_test.go`, `InferJudgment` call-site pin test — unchanged.
4. Non-goal turns: tool-surface assembly untouched when the predicate is false — covered by test 8's negative rows.
5. NEW seam regressions: test 12 (two-cycle keeper) guards the un-wedge permanently; test 16 guards frame emission; C-8 (contracts no-diff) guards the wire.

---

## 7. Requirements & Success Criteria (Phase 5)

### Functional Requirements

- **FR-001** `/goal <prose>` MUST activate the goal and continue into the working turn without any LLM call, question, or confirmation preceding work start. A restate (prose or marker) on an active goal MUST NOT mint a new `GoalID` — only a fresh activation on a goalless session mints one (round-2 M-7: a re-minting restate would silently reset the FR-010 question budget and the FR-014b push counter).
- **FR-002** The marker-only path MUST remain byte-identical in behavior (activation, criteria derivation, zero LLM calls).
- **FR-003** Admit MUST run exactly once per activation, before state is written.
- **FR-004** A new `set_goal` builtin MUST write the existing goal record (statement/criteria/DoD) via the existing meta-patch surface, applying NormalizeCriteria, validateCriterion, IsValidJudgment, the DoD floor, and feasibility vetting before any write. The `assessment` arg (clarity + assumptions) is validated and **logged via FR-025's register event — NOT persisted** (round-2 B-2: persisting would change the `CompiledGoal` shape that §1 and the ADR declare unchanged).
- **FR-005** `set_goal` MUST refuse at delegation depth > 0 and on sessions without an active goal.
- **FR-006** `set_goal(mode:update)` MUST re-validate fully and return a diff computed by `diffGoalAmendment` against the current record.
- **FR-007** On web-origin goal turns where the predicate (active goal ∧ empty record) holds and the provider supports it, the first request MUST offer exactly {`set_goal`, `AskUserQuestion`} ∩ policy-allowed with tool-choice required; the full surface MUST be restored on the next request. **The predicate MUST NOT consult `opts.UserInitiated` or sender identity** — a card resume (human or auto-submitted) and a keeper nudge turn are goal turns; the surrounding code's `UserInitiated` fail-closed gates are the WRONG pattern to copy here (grill M1).
- **FR-008** A typed ToolChoice MUST be added to the provider request path and honored by all six native builders; the openai_compat extraBody merge MUST NOT silently override it (win or WARN).
- **FR-009** Forcing MUST NOT occur when: policy denies `set_goal` (WARN), origin is non-web, provider is CLI-bridged, or the terminal-request branch offers no tools.
- **FR-010** The question-round budget MUST be 1 per goal **generation (per `GoalID`)** — a prose restate keeps the `GoalID` and therefore inherits a spent budget (grill m6; the agent can still register with stated assumptions) — persisted (`GoalQuestionRoundsUsed`); once spent, `AskUserQuestion` MUST be absent from the narrowed surface.
- **FR-011** The goal rubric + define-goal skill content MUST be injected as a turn-scoped note exactly when the predicate holds, registered in the mid-turn budget, and MUST NOT re-inject workspace instructions.
- **FR-012** The front-path compile and clarity-gate calls MUST be removed; `compileGoalIntentLLM` MUST be reachable only from the keeper fallback.
- **FR-013** Idle settlement MUST be suppressed while a live turn (own or delegated child, resolved via turn-state scan by transcript session) exists, re-arming on turn end.
- **FR-014** (recordLESS quiet goal) Idle with the D3 predicate true (active goal ∧ empty record) and no parked card MUST consume no round and produce no verdict, routing to the D6c nudge ladder (N=2 → engine fallback, FR-017).
- **FR-014b** (RECORDED goal, zero adjudicable output — grill B1/B2; refined round-2 B-1/B-4) "Zero adjudicable output" is the named triple: zero evidence records ∧ zero **goal-scoped** workspace diff ∧ zero transcript output. Transcript output is counted **across the goal session and its delegated descendant sessions** (a goal whose agent delegated all its work MUST NOT match). The diff term is scoped to **this session's write set since the goal's start / last push boundary** (`AttemptDiff`'s scope argument — the current unscoped `AttemptDiff(nil)` would let a co-tenant session's diff on a shared `WorkspaceID` mask this goal's emptiness); on an unbound chat goal (`WorkspaceID==""`) the diff term degenerates away and the triple is deliberately a pair. When the triple holds at idle: no verdict, no round consumed, a continue-push dispatches (same stamped-sender mechanism as FR-015) — **at most 2 consecutive times, counted in a PERSISTED field** `GoalZeroOutputPushes` on the goal record (an in-memory streak resets on gateway restart and re-opens the unbounded loop). **Reset rule:** the counter resets to 0 whenever the triple evaluates false at an idle, and on any successful `set_goal` write. If the triple still holds at the next idle after the second push, idle settlement MUST adjudicate normally (fail-closed verdict permitted, round consumed) so the rounds bound terminates the loop.
- **FR-014c** (round-2 m-6, promoted from A-1) Keeper nudges and continue-pushes pace on the EXISTING idle quiet-window constant — no new cadence knob.
- **FR-015** Keeper-originated turns (idle steer, nudges) MUST be stamped with the goal-loop sender id and MUST pass the after-turn gate, bumping activity and clearing the settling flag (two-cycle capable). Mechanism (round-2 m-4): a per-event `SenderCanonicalID` override field on the async-notify event, set ONLY by the goal-loop deliverers — the shared notifier's default `async:<kind>` stamping for every other source is untouched (negative-asserted in tests).
- **FR-016** A parked question card on the goal session MUST suppress both idle settlement and nudges; the expiry sweep MUST remain in force.
- **FR-016b** `goalIdleExpirySweep`'s skip predicate MUST become `s.GoalCondition == "" && s.GoalCriteriaJSON == ""` — the `GoalPendingJSON`/`GoalClarificationJSON` terms are DELETED with their fields (leaving them is a compile error or dormant code), and the `"(pending — never confirmed)"` label fallback in the same function is deleted with them, added to FR-023a's sweep list (round-2 M-4).
- **FR-017** After exactly 2 recordless nudges, the engine MUST run one fallback compile and register its output marked engine-authored.
- **FR-018** The fallback compile MUST resolve model/provider from the Judge system agent; a nil resolution MUST degrade to the deterministic parser with a WARN.
- **FR-019** Every successful register/update MUST emit the `goal_status` frame in state `active` with definition/criteria/dod populated. Marker-path activations/restates emit the same `active` frame with criteria/dod populated and **`definition` absent** — the marker path legitimately stores an empty statement (the existing Prompt/Intent fallback; round-2 B-3 — FR-002's byte-identical guarantee wins). The `queued` state MUST never be emitted.
- **FR-020** The channel record echo MUST key on the goal's **recorded routing channel** (`recordGoalRouting`), never the current turn's own `Channel` — a keeper/nudge turn (`Channel: "system"`) on a Telegram-origin goal still echoes to Telegram (grill M7). On channel-routed goals, every successful register/update MUST send exactly one formatted record text (re-scoped `formatGoalEcho`); web-routed goals MUST NOT receive a text echo.
- **FR-021** The SPA MUST render the record card from the active frame (no buttons) and evict any empty-id pill when a keyed frame for the session arrives.
- **FR-022** Bare "confirm" MUST be ordinary chat (no interception exists). `/goal confirm` MUST reply informatively without state change, recognized by a **new inline verb match in the command router** — NOT the retired `isGoalConfirmVerb` (grill B3: without a recognizer it would fall through and activate a goal literally named "confirm").
- **FR-023a** At completion, the tree MUST contain zero definitions and zero non-comment references to ADR-081's FULL deletion inventory (human-verified sweep, wave 4 checklist §6).
- **FR-023b** `scripts/check-no-goal-confirm-gate.sh` MUST enforce exactly these seven names — `confirmPendingGoal`, `IsGoalConfirm`, `confirmGoalAliases`, `ConfirmGoalWord`, `proposeGoalAmendment`, `buildGoalPendingNote`, `useGoalCompilingIndicator` — wired into pr.yml, runci.sh lint, and make (grill M5).
- **FR-024** Stale pending-draft fields in on-disk goal state MUST be ignored on read and absent after the next write; no legacy parse path may exist.
- **FR-025** Goal lifecycle events MUST log per D8 (INFO set + WARN set), each exactly once, with session_id/goal_id, without duplicating existing lines; the two pre-existing INFO lines (`goal_triggers.go:549`, `goal_loop.go:1200`) gain a `goal_id` field (grill m1).
- **FR-026** The contracts **schema shape** MUST be unchanged — no field added, removed, or retyped. The queued-only DESCRIPTION prose on `GoalStatusFrame`'s `definition`/`criteria`/`dod` AND the `state` enum's own `queued` description (updated to "retained for wire compatibility; never emitted since ADR-081" — round-2 m-3) ARE updated (schema file + regen + hand-sync of `contracts/asyncapi.yaml`'s inline duplicate — an explicit wave-2 task; grill B5).
- **FR-027** The frontend prerequisite branch MUST be merged before D5 frontend work lands — wave-0 gate: `git merge-base --is-ancestor 1e0527d8 HEAD` succeeds (grill m10).
- **FR-028** Activating a new goal, and `/goal clear`, MUST cancel any parked question card belonging to the superseded/cleared goal **without dispatching a resume turn** (round-2 M-6: `CancelByUser` injects a resume and is the WRONG primitive; use a non-resuming cancellation — `CancelOnSessionStop`-style, or a new `CancelWithoutResume` on the registry interface — resolved via `PendingForSession(sessionID)`). The re-homed `cancelOrphanedClarifyCard` is the named wrapper (its old call sites die with `emitGoalClarificationCard`; its new call sites are exactly these two). Resume invalidation is free once cancelled: a later `Submit` returns `ErrNoPending`; the parked turn needs no unwinding (`TurnEndStatusParked` is terminal). Tests MUST assert no resume turn is dispatched by the cancellation.
- **FR-029** `/goal status` MUST be rewritten: `goalStatusReply`'s pending-draft branches (its `loadGoalClarification` branch and its `loadCompiledGoal(meta.GoalPendingJSON)` + `ConfirmGoalWord` branch — cited by symbol per the churn rule, round-2 m-1) are deleted; the reply gains the record summary via the re-scoped `formatGoalEcho` (a NEW call site — today `goalStatusReply` never calls it) alongside condition/elapsed/rounds/spend/reason (grill M8).
- **FR-030** A pre-upgrade ACTIVE goal (populated record written by the old compile path) MUST continue operating: predicate false → no forcing, no nudge; claim/idle adjudication proceeds on the existing record; rounds accounting continues (grill M10).
- **FR-031** The goal's routing (channel/chat-id/session-key/agent) MUST be persisted with the goal record and rehydrated on demand — the reader is `goalTriggers().routeFor(sessionID)`, which falls back to the persisted route when the in-memory map is empty (round-2 M-9: today a restart silently disables BOTH the keeper push — `idleSteerDeliverer`'s "no routing to re-inject steer" — and the channel record echo). A genuinely missing route MUST WARN and surface in `latest_reason`, never degrade silently.

### Success Criteria

- **SC-001** On the live install, `/goal <prose>` → first streamed token in ≤ 1.2× the session's plain-chat first-token latency. **Method (grill M2):** same agent, same model, same session; 3 paired runs (plain chat message, then a goal of comparable length); compare medians. Structural zero-pre-work is C-1's job; this SC measures only the lived experience (was: minutes).
- **SC-002** A scripted end-to-end goal (vague → card → answer → register → steer → unmet verdict → push → second cycle) completes with zero manual confirmations and zero wedges — asserted by integration test 22 and reproduced once manually on the live install.
- **SC-003** Zero idle verdicts against empty work in the two-cycle integration test and in one live multi-minute-model run.
- **SC-004** Guard script green on the final tree; planting `confirmPendingGoal` makes CI fail.
- **SC-005** All gates green: gofmt 0, golangci-lint 0, go test (CI), vitest 0, `tsc -b` 0, verify-contracts 0.
- **SC-006** A full goal lifecycle at INFO produces a complete, ordered event narrative (manual log review against the D8 list — every event present exactly once).

### Traceability Matrix

| Requirement | User Story | BDD | Tests |
|---|---|---|---|
| FR-001 | US-1 | S-01 | 4, 22, 27(E2E) |
| FR-002 | US-1 | S-02 | 5 |
| FR-003 | US-1 | S-01,S-03 | 4 |
| FR-004 | US-2 | S-04,S-05,S-06 | 1 |
| FR-005 | US-2 | S-07,S-08 | 2 |
| FR-006 | US-5 | S-17,S-36 | 3 |
| FR-007 | US-4 | S-13 | 7, 8 |
| FR-008 | US-4 | S-13,S-33 | 7 |
| FR-009 | US-4 | S-14,S-16,S-34 | 8 |
| FR-010 | US-3 | S-10 | 8, 21 |
| FR-011 | US-2 | S-04 | 9 |
| FR-012 | US-4 | S-15 | 14 (+ absence assert in 9) |
| FR-013 | US-6 | S-20 | 10 |
| FR-014 | US-6 | S-21 | 11 |
| FR-015 | US-6 | S-22 | 12 |
| FR-016 | US-6 | S-23 | 13 |
| FR-017 | US-4 | S-15,S-35 | 14 |
| FR-018 | US-7 | S-25,S-26 | 15 |
| FR-019 | US-2,US-10 | S-04,S-31,S-02,S-19 | 16 |
| FR-020 | US-10 | S-31 | 17, 23 |
| FR-021 | US-2 | S-04,S-31 | 24, 25 |
| FR-022 | US-9 | S-30 | 19 |
| FR-024 | US-9 | S-29 | 18 |
| FR-025 | US-8 | S-27 | 20 |
| FR-026 | US-2 | S-04 (frame reuse) | C-8 gate |
| FR-027 | US-2 (prereq) | — | wave-0 gate: `git merge-base --is-ancestor 1e0527d8 HEAD` |
| FR-014b | US-6 | S-21,S-37,S-38 | 11 |
| FR-014c | US-6 | S-21 | 11 |
| FR-016b | US-6 | S-23 | 13 |
| FR-031 | US-6,US-10 | S-22,S-31 | 12, 17 |
| FR-023a | US-9 | S-28 | wave-4 human sweep + 26 |
| FR-023b | US-9 | S-28 | 26 |
| FR-028 | US-3,US-9 | S-32,S-41 | 14, 13, 21 |
| FR-029 | US-2 | S-39 | 19 |
| FR-030 | US-9 | S-40 | 18 |
| (S-09) | US-3 | — clarify roundtrip | 21 |
| (S-11) | US-3,US-10 | — channel clarify | 23 |
| (S-12) | US-3,US-4 | — auto-submit resume | 21, 8 |
| (S-24) | US-6 | — claim path pinned | existing `goal_triggers_test.go` claim tests (regression req. 1) |

---

## 8. Ambiguity Self-Audit (Phase 5.5)

Resolved during design (recorded, no action needed): forcing on non-web (none — rubric only); question budget (1, persisted); nudge count (2); fallback authorship (engine-marked); channel record view (text echo); render source (typed frame, not tool args); `formatGoalEcho`/`diffGoalAmendment` (re-homed, not deleted).

Open items — **deferred as acknowledged risks** (operator pre-authorized the pipeline; each is small and implementation-decidable, flagged for the grills). A-1 was PROMOTED to FR-014c in round 2; A-3 was RESOLVED in round 1.

| # | Ambiguity | Likely implementation assumption |
|---|---|---|
| A-2 | Where `GoalQuestionRoundsUsed` displays (if anywhere) | not displayed; internal budget only |
| A-3 | `set_goal` result rendering in the SPA thread | RESOLVED (grill m2): hidden via `src/lib/toolVisibility.ts` — a deliberate extension of that CLAUDE.md-governed closed set, named as a wave-3 task; the record card (frame) is the visible surface |
| A-4 | Engine-fallback registration's `assessment` LOG content (nothing is persisted, FR-004) | logged as `clarity: ambiguous`, assumptions empty, author engine |
| A-5 | Whether steering SHOULD auto-trigger when the user edits mid-round vs next turn | ordinary chat mechanics; no special interception |

---

## 9. Holdout Evaluation Scenarios (Phase 5.7 — NOT for development use)

**HOLDOUT — excluded from the TDD plan and traceability matrix. For post-implementation evaluation on the live install by the operator or an independent evaluator.**

- **H-1 (happy):** Set a one-sentence software goal from the web app. Expectation: visible agent output within a few seconds; the assumptions card appears during (not before) the work; you never click anything to "approve" the goal.
- **H-2 (happy):** Set a deliberately vague non-software goal ("organize my week"). Expectation: a question card with sensible options; after answering, work proceeds and the recorded criteria reflect your answers.
- **H-3 (happy):** While the agent works, send "actually make it dark-themed only". Expectation: work absorbs the change; the goal record visibly updates; no re-confirmation.
- **H-4 (error):** Ask a goal while the maximum number of active loops is running. Expectation: a clear refusal naming the cap; nothing half-starts.
- **H-5 (error):** Kill the network to the LLM provider mid-goal-round, restore it, wait. Expectation: the goal survives; the keeper resumes it; no wedge (a later idle round still happens).
- **H-6 (edge):** Walk away after answering the clarify card; return after several judge rounds. Expectation: rounds show real progress attempts with reasons — never a round that "failed everything" with the work still visibly in flight.
- **H-7 (edge):** Run the same goal from Telegram. Expectation: questions arrive as normal messages, the goal record arrives as a readable message, and the loop behaves the same.

---

## 10. Wave Plan (Phase 6 supplement — implementation ordering)

- **Wave 0 (prereq):** merge `worktree-agent-a07d6c862886c03df` (b09b3d5f + 1e0527d8) to `release/v0.1.1`. Gate: `git merge-base --is-ancestor 1e0527d8 HEAD` succeeds (FR-027).
- **Wave 1 (backend core, parallelizable):** W1a `set_goal` tool + validation + preconditions + diff (tests 1-3). W1b instant activation + restate paths + greenfield meta + `/goal status` rewrite + inline confirm-verb match (tests 4-6, 18, 19). W1c ToolChoice providers build (test 7).
- **Wave 2 (engine wiring):** forcing predicate + narrowing + rubric injection (tests 8-9); keeper repairs incl. FR-014b persisted bounded pushes + FR-014c cadence + FR-016b rewritten sweep predicate + FR-028 non-resuming card cancellation + FR-031 persisted routing (tests 10-14); fallback fast-lane (test 15); frames + channel echo (tests 16-17); **GoalStatusFrame description-prose update (three fields + the state enum's queued wording) + regen + asyncapi.yaml inline hand-sync (FR-026, grill B5/m-3)**; logging incl. goal_id backfill on the two existing lines (test 20).
- **Wave 3 (frontend):** record card from active frame (GoalEchoCard edited in place), queued-branch removal in `GoalPillTray.tsx`/`GoalIndicator.tsx`, store hygiene, `set_goal` added to `toolVisibility.ts`'s hidden set (A-3) (tests 24-25).
- **Wave 4 (deletion + guard):** D9 full-inventory sweep against §6's enumerated 13-file test list + SPA list (FR-023a, human-verified), guard script + CI wiring (FR-023b, test 26), CLAUDE.md retired-surfaces entry, **superseded-flow banners on `planning-goals-spec.md` and `judgment-first-criteria-spec.md`** (grill m9).
- **Wave 5 (integration + E2E + live):** tests 21-23, 27; **operator pre-step: set the Judge agent to a fast model (D7 action item — SC-001's measurement depends on it)**; live-install upgrade + SC-001/002/003/006 measurements.

Per-wave gates: the standard quality gates (gofmt/lint/CI go-test/vitest/tsc/verify-contracts) + the wave's named tests green.
