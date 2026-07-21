# ADR-052: Autonomous agent plan authoring & execution (tool-permission gated)

- **Status:** **Accepted** (r6, 2026-07-21 — 3 grill rounds passed through revision; operator interview closed ALL open points: full soul/Rubric unification, global-ceiling `ask` seeding [no deny], no upgrade concern [no existing installs / no back-compat], direct 3-caller conversion with anti-pattern + LLM e2e eval coverage, verifier window N=20k, target = the **v0.1.1 release line**. Judge = real agent in a *verifier role*, OWN session, read-only tools + scoped `inspect_session`, transcript-window-first context, evidence-marker gate, three-rung ladder)
- **Date:** 2026-07-20
- **Deciders:** Operator (Daniel Piatkowski); Albert (architecture)
- **Evidence level (highest used):** 1 (user-input, operator-locked) + codebase `[FACT]` grounding
- **Supersedes (in part):** the human-approval gate introduced by [ADR-049](ADR-049-planning-goals-system-agents.md) / Planning & Goals epic (PR #526, release/v0.1.1). ADR-049's engine, judge, System-Agents, and guardrails stand; only the *human-only kickoff* is replaced.
- **Ratification note:** the direction was interview-locked with the operator over an extended design conversation. This ADR records and grounds the decision; it does not re-litigate it.

---

## 1. Problem Understanding

The Planning & Goals epic shipped a fully-autonomous *execution* engine but a human-only *kickoff*. Today:

- `[FACT]` An LLM agent can create standalone tasks (`create_task` / `create_task_in_workspace`, `pkg/tools/task.go`, `pkg/sysagent/tools/task.go`) but has **no tool to create a plan and no tool that accepts `plan_id`** (grep for `"plan_id"` across both tool packages returns nothing). Plan creation is one call site: `POST /workspaces/{id}/plans` — human UI only.
- `[FACT]` Plan approval/start are human-only REST handlers (`handlePlanApprove`, `handlePlanStop`, `pkg/gateway/rest_plans.go`); no `approve_plan` / `execute_plan` agent tool exists.
- `[FACT]` Once a plan is `running`, the server-side `PlanEngine` (`pkg/agent/plan_engine.go`) is fully autonomous: it promotes `approved→running` (cap-gated), dispatches ready DAG members to their agents, judges via the evidence ladder, retries to the attempt limit, and synthesizes to `done` — with no further human step.

**Consequence:** "an agent plans **and** executes a complex goal" is currently impossible, even though the hard machinery (execution, judging, retry) already exists and is wired. The operator's objective: make agent-driven plan+execute the **default path for complex goals**, with authorization expressed through the existing per-agent tool-policy rather than a human approval click.

**Blast radius:** adds an agent tool surface + small backend deltas + UI controls. Reuses the execution engine and the cancel cascade unchanged. Security-relevant (agents gain autonomous multi-task execution), so the retained guardrails are load-bearing.

## 2. Extracted Requirements

### Functional
- FR-1: An authorized agent MUST be able to **create a plan** (goal, DoD, owner) via a tool. `[FACT]` gap today.
- FR-2: An agent MUST be able to **attach tasks to a plan** with `plan_id` + `blocked_by` (build the DAG) at task-create time. `[FACT]` gap today.
- FR-3: An agent MUST be able to **start execution** of a plan via a tool (`execute_plan`), with **no human approval** required. `[FACT]` gap today.
- FR-4: Authorization for FR-1..3 MUST be the agent's **explicit tool policy** (Constraint #6) — holding `execute_plan` *is* the approval. Seeded **allow for Jim (Orchestrator)**, deny for all other seeded agents.
- FR-5: `execute_plan` MUST enforce the same DoD/criteria gate the human path was supposed to (every member task carries ≥1 acceptance criterion). `[FACT]` today only `POST /approve` enforces FR-084; the SPA's PUT-based approve bypasses it (bug, §3).
- FR-6: A running plan or task MUST be **Stoppable** — a hard cancel that terminates the in-flight turn(s), delegated subagents, and shells.
- FR-7: A Stopped plan/task MUST become terminal and, on re-run, **restart fresh**; a plan restart MUST preserve `done` members and re-run only non-`done` members (continuation).
- FR-8: The three task views (Board / List / Graph) MUST expose ▶ Execute/Play and ■ Stop per the button matrix (§6), each behind a confirmation modal.

### Non-Functional
- NFR-1 (safety): autonomous execution MUST remain bounded by the existing guardrails — `[FACT]` global concurrency cap = 16, per-task attempts = 3, 7-day idle expiry, criteria-required (`pkg/config/planning.go`; `pkg/sysagent/tools/task.go:252` rejects an agent-assigned task with zero criteria). These replace the human gate as the safety net.
- NFR-2 (no parallel infra): Stop MUST reuse the existing agent-loop cancel; no second cancellation subsystem.
- NFR-3 (contract-first, Constraint #8): new tool I/O and any wire-shape changes go through `contracts/` before code.
- NFR-4 (maintainability): reuse `PlanEngine` and the state machine as-is; minimize new coordination code.

### Constraints
- Single Go binary; pure Go; file-based storage; no-default-policy (Constraint #6 — every tool resolves from an explicit seeded policy entry).
- Phase: **v0.1.1 release line** (operator interview 2026-07-21 — features accelerated into the current version; no existing installations, no backwards compatibility), shipped together — the UI controls are meaningless without the tools + fan-out behind them.

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve (in `/plan-spec`) |
|---|---|---|---|---|
| G1 | Judge isn't reachable by the turn-cancel **today** — `[FACT]` the Judge System Agent is invoked via a shortcut direct `Provider.Chat` call (`judge.go:505`) that registers no turn. | On Stop the judge must halt via the SAME cancel as everything else (A2). | — **RESOLVED (judge = real agent, own session — see Judge/Verifier architecture):** the verifier runs in its own session; the Stop fan-out cancels it via `RequestCancelForSession(verifierSession)` like any session. No new cancel machinery. |
| G2 | `PUT /plans/{id}` sets `patch.State` directly (`[FACT]` `rest_plans.go:651-654`), so **PUT bypasses BOTH the FR-084 criteria gate AND the cap** — `Admit` lives only in `tryStartApprovedPlan` (`[FACT]` `plan_engine.go:388`), so `PUT state:"running"` goes live skipping criteria *and* the cap of 16. | Falsifies §7's "bounded by cap 16" and defeats the criteria safety net (NFR-1). | — must fix. | **RESOLVED (operator A1):** forbid `PUT` from setting **any** `state` (raw-body reject, ADR-035 precedent); the gated `POST /approve` + `POST /stop` + the engine are the ONLY state-transition paths. Invariant: state never changes via PUT. |
| G3 | Tool boundary: `plan_id`+`blocked_by` as params on `create_task`, or a dedicated `plan_add_task` tool. | Ergonomics + contract shape. | Extend `create_task` with optional `plan_id`/`blocked_by`. | Decide in plan-spec (both viable). |
| G4 | `run_task` for a task **inside a plan**. | Manual out-of-DAG-order execution could violate dependencies. | In-plan tasks get **Stop-only**; `run_task`/▶ is standalone-only (the plan drives member start). | Confirm: no per-member manual start. |
| G5 | Does `execute_plan` return once `running`, or wait for a cap slot? | UX + tool semantics. | `[FACT]` engine already handles `approved→running` cap admission asynchronously; `execute_plan` sets `approved` and returns, engine promotes. | Confirm async semantics + how the tool reports "queued behind cap". |
| G6 | `stop_plan` / `stop_task` as **agent tools** vs UI-only REST. | Whether an agent can programmatically stop peers' work. | Ship `execute_plan` + `create_plan` as agent tools; expose stop primarily as UI/REST, add agent `stop_*` only if a delegation flow needs it. | Confirm minimal agent tool set. |
| G7 | Re-setting a **chat-level `/goal`** on Start requires the `GoalCondition` to be persisted (or re-supplied) — `[FACT]` plans/tasks persist their goal/DoD/criteria so restart re-derives it, but the chat condition is session-scoped and `/goal clear` tears it down. | "Start sets the goal again" (§6.9) needs a source to re-set from at chat level. | Store the last-cleared `GoalCondition` so Start can re-establish it; or scope §6.9's Start-re-set to plan/task (which persist) and leave chat `/goal` as clear-only (re-issue `/goal` manually — the v1 lean). | Decide in plan-spec: does chat `/goal` get a Start-re-set, and if so where is the condition persisted? |
| G8 | Member post-turn judge isn't cancelable **today** — `[FACT]` `finishTaskRun→JudgeCriteria` (`task_executor.go:260,481`) is a direct `Provider.Chat` call after the worker turn de-registers. | A Stop during a member's judge window can't cancel the eval. | — **RESOLVED (same own-session model as G1):** the member verifier runs in its **own session**, cancelled by the Stop fan-out. No `TaskExecutor.CancelTask`. |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Autonomy — default path for complex goals | High | Operator's primary objective |
| Safety / bounded execution | High | Guardrails must hold without the human gate |
| Reuse over new infrastructure | High | Minimize new coordination + cancellation code (risk, maintenance) |
| Consistency with Constraint #6 (explicit tool policy) | High | The gate should *be* the tool policy, not a new concept |
| Reversibility / blast radius | Medium | Prefer additive, config-seeded changes |

## 5. Option Analysis

### Option A — Tool-permission-gated agent tools; reuse engine + reuse cancel *(recommended)*
Add `create_plan` / plan-linkage / `execute_plan` agent tools; authorization is the seeded tool policy. Execution is the unchanged `PlanEngine`; Stop emits the existing `RequestCancelForSession` into each in-flight member session.

| Dimension | Assessment |
|---|---|
| Strengths | Delivers the objective directly; **zero new execution or cancellation infra** (`[FACT]` `handleTaskStop` already calls `RequestCancelForSession(task.SessionID)`, `rest_tasks.go:1398`); the gate reuses Constraint #6 with no new concept; guardrails already exist. |
| Weaknesses | Removes the human safety interlock — safety now rests entirely on the guardrails + who is granted `execute_plan`. Requires fixing the FR-084/PUT bypass (G2). |
| Risks | A mis-seeded or over-granted `execute_plan` lets an agent launch unbounded autonomous work (bounded by cap 16, but still). Mitigation: seed allow **only** for Jim; keep criteria-required + idle-expiry. |
| Complexity | Low-moderate: new tools + plan-Stop fan-out (a loop reusing `handleTaskStop`'s body) + a small judge-round cancel registry (G1) + `paused`-free state reuse. |
| Cost implications | Build: moderate (tools, contracts, UI). Run: none new. Scaling: bounded by existing cap. |
| Operational impact | No new services; same file store; same engine. Tool-policy seeding is the only new operator-facing surface. |

### Option B — Agent authors, human approves (proposal-only)
Agent builds a `draft` plan; a human clicks Approve to run.

| Dimension | Assessment |
|---|---|
| Strengths | Keeps a human interlock; smallest safety change. |
| Weaknesses | **Does not meet FR-3/FR-4** — no autonomous execution, which is the whole objective. |
| Risks | Bottlenecks unattended operation; the Orchestrator can't actually orchestrate. |
| Complexity | Low. | 
| Cost / Ops | Low / none new. |

*Rejected — one line: fails the primary objective (no autonomy).* 

### Option C — Configurable approval gate (default off)
Ship Option A plus a `require_plan_approval` flag (global→workspace→plan) that can re-impose a human gate.

| Dimension | Assessment |
|---|---|
| Strengths | Flexibility; an operator could tighten specific scopes. |
| Weaknesses | Adds a second authorization concept on top of tool policy — redundant with Constraint #6; more surface, more tests, more docs. |
| Risks | Two overlapping gates (tool policy *and* a flag) invite confusion about "who can actually start a plan". |
| Complexity | Moderate (config plumbing at every level). |
| Cost / Ops | Higher build; ongoing config surface. |

*Rejected — one line: operator explicitly rejected the optional gate; tool permission already IS the configurable gate.*

### Option D — New autonomous-execution subsystem / parallel driver
Build a dedicated orchestrator loop + its own cancellation tree rather than reuse the engine + agent-loop cancel.

| Dimension | Assessment |
|---|---|
| Strengths | Clean-slate control over scheduling/cancel semantics. |
| Weaknesses | Duplicates a **proven** engine and a **proven** cancel cascade that already reaches turns, subagents, and OS-level shell process groups. |
| Risks | Two divergent execution/cancel paths; high regression + maintenance cost; re-implements orphaned-shell handling that was already a fixed bug. |
| Complexity | High. |
| Cost / Ops | High build + perpetual dual-maintenance. |

*Rejected — one line: parallel infra for machinery that already exists and works (violates NFR-2, operator's explicit "ride the existing infrastructure").*

## 6. Recommended Architecture

**Option A.** It is the only option that meets FR-3/FR-4 (autonomy) while scoring highest on reuse and Constraint-#6 consistency, and it is grounded in a working precedent for every non-trivial mechanism.

**Shape of the change:**

1. **Authorization = tool policy.** `create_plan`, `execute_plan` (+ linkage) are builtin tools resolved per-agent by the explicit seeded policy (Constraint #6). Seed (**operator interview 2026-07-21**): **Jim `allow`** for `create_plan`/`execute_plan`/`run_task`; **every other seeded agent AND the global ceiling seed these tools as explicit `ask`** — a non-granted agent attempting them triggers an **operator approval prompt**, not a flat deny ("seeded in the global setting for ask, no deny"). Grill F5's requirement still holds in this form: the ceiling entry must be **explicit** (`ask`), never absent, so nothing resolves implicitly. No approval-agent, no approval flag. **Upgrade posture: moot** — there are **no existing installations and no backwards compatibility** on the v0.1.1 line (operator); fresh seeds apply everywhere, R3-3 closed.
2. **Agent tool surface (new):** `create_plan`; task-in-plan linkage (`plan_id` + `blocked_by` on task creation — G3); `execute_plan`. `run_task` (standalone only — G4; drives the task's **normal attempt-loop** per operator A3 — run → judge → retry with steering up to the attempt limit — the same way a plan member runs, not a single shot). Stop primarily UI/REST; agent `stop_*` deferred unless a flow needs it (G6).
3. **Execution engine unchanged** — `[FACT]` reuse `PlanEngine`: `execute_plan` sets the plan to `approved` through the **single gated transition** (enforcing FR-084 criteria), and the engine promotes `approved→running` under the global cap, then dispatches/judges/retries/synthesizes as today. **`PUT /plans/{id}` MUST be forbidden from setting ANY `state` value** (operator A1 — raw-body reject, ADR-035 precedent): every transition goes through a dedicated endpoint (POST `/approve`, POST `/stop`, the engine). `[FACT]` today PUT sets `patch.State` directly (`rest_plans.go:651-654`) and the cap `Admit` lives only in `tryStartApprovedPlan` (`plan_engine.go:388`), so `PUT state:"running"` would skip **both** criteria and the cap. One invariant: **plan state never changes via PUT** (fixes G2, closes the cap hole; the SPA `approvePlan` repoints to POST `/approve`).
4. **Stop = the existing chat cancel, fanned out across sessions.** `[FACT]` `RequestCancelForSession(<session>)` (`cancel.go`, precedent `rest_tasks.go:1398`) cancels a session's turn, its sub-agents, and its shells (OS process-group). The verifier runs in its **own session** (see *Judge / Verifier architecture* below — a real agent, not the old shortcut `Provider.Chat` call `judge.go:505`), so it is cancelled the **same way** as any session. No `TaskExecutor.CancelTask`, no judge registry, no goal-drain — one mechanism, A2 honored. **New code:**
   - **(a) The plan-Stop fan-out** issues `RequestCancelForSession` for **each `in_progress` member session + each registered verifier session (member- and plan-level)**, run **INSIDE the engine under `planDecisionMu`** (grill F3; the lock `processPlan` dispatches under, `[FACT]` `plan_engine.go:421`) so a concurrently-dispatched member can't escape. `[FACT]` `handlePlanStop` today only sets state — the REST handler signals the engine. (M1: the member `SessionID` is assigned **synchronously before dispatch** so the fan-out always has a handle.)
   - **(b)** A small state re-check before applying a *returned* verdict guards the case where a verifier finished microseconds before the cancel landed (don't mislabel a cancelled item) — a minor race-guard, not the primary mechanism.
   No judging resumes until Play — the engine starts no round unless `State==running`.
5. **No pause/resume** (deferred). Stop = cancel (worker + verifier sessions) + set `cancelled`. (Sessions being cancelable + resumable is the foundation a future pause/resume would build on.)
6. **`cancelled` ≠ `failed`, but shares the board (operator decision).** A user-Stop sets `failed` state/status **+ reason `stopped_by_user`**, surfaced as an **orange "Cancelled"** marker at task/plan level (distinct from red "Failed") — `[FACT]` plans already carry `failed(stopped_by_user)`. No 7th board column, no new status enum value: cancelled tasks sit in the **Failed column**. The **reason is the discriminator** — it drives both the orange visual and restart-eligibility (§6.7).
7. **Restart = continuation, gated to a *cancelled* item (grill F1).** `[FACT]` `StateFailed` is a **frozen terminal** today — `legalPlanTransitions` allows only `failed→failed` (`plan.go:84`; "a failed plan is NEVER retried"). Restart therefore **narrowly amends the state machine**: at **plan** level only `failed[reason=stopped_by_user]→approved` is un-frozen — the restart handler sets the plan back to **`approved`**, and the **engine** promotes `approved→running` under the global cap (`Admit` in `tryStartApprovedPlan`), exactly like a first execute. (Grill R3-4: restarting straight to `running` would **skip cap admission** — the same hole class the PUT-lockdown closed.) A genuinely failed plan (`judge_rounds_exhausted` / `idle_expired`) stays frozen, **no Play** (author fresh); at **task** level `failed→next` is un-frozen for **any** reason, so a plan restart re-runs *all* its non-`done` members (genuine failures + cancels alike — matching "repeat the failed tasks from scratch"). Play preserves `done` members + their evidence → continuation. This reverses ADR-049's frozen-failed invariant **only** for the user-cancel plan case — deliberate, documented. Restart is a **dedicated endpoint** (`POST /plans/{id}/restart`, `POST /tasks/{id}/restart`) whose handler does the reset orchestration — `[FACT]` no restart route exists today (only approve/stop), and A1's PUT-lockdown removes the old PUT path, so this is new (grill B2). **Idempotence (operator A4):** "fresh" = reset the re-run member's `attempt_count` **and** the plan's `JudgeRounds` counter (a restart is a clean slate — otherwise a plan restarted near its judge-round cap fails immediately); preserve `done` members' evidence. The reason-gated transition (`failed[stopped_by_user]→approved`) can't live in the reason-free `legalPlanTransitions` matrix (`[FACT]` `plan.go:79-105`), and `ValidateStateTransition` is enforced **store-level** — so the amendment is a store-level, reason-aware guard (not handler-only), performed by the restart path (grill M2/R3-4, plan-spec).
8. **UI button matrix** (all three views; ▶/■ both confirm-modal-gated):

  | Surface | State | Button |
  |---|---|---|
  | Plan | draft | ▶ Execute |
  | | running | ■ Stop |
  | | **cancelled** (`failed`+`stopped_by_user`) | ▶ Play (restart = re-run non-done) |
  | | **failed** (judge-exhausted / idle-expired) | — (terminal; author fresh) |
  | | done | — |
  | Standalone task | idle — inbox / next / failed / cancelled | ▶ Play (re-run) |
  | | in_progress | ■ Stop (chat-send toggle) |
  | In-plan task | in_progress | ■ Stop only |
  | | otherwise | — (plan drives its start + restart) |

9. **Stop = "goal clear", Play = "goal set" — one principle, three levels.** Stop clears the active goal (cancels its session[s]); Play sets it new. Uniform across:
   - **Task** — clear = `RequestCancelForSession` over the worker session **and** its verifier session; set `cancelled`. set = restart → `next` from the persisted criteria.
   - **Plan** — clear = a **fan-out** `RequestCancelForSession` over {every `in_progress` member session} + {every **registered** verifier session, member- and plan-level, via the registry}; set members `cancelled` + plan `failed(cancelled)`. (There is no standalone "plan session" — the plan-level verifier session is the plan-scope handle.) Then the engine stops re-dispatching/re-judging. set = restart → **`approved`** from the **persisted** plan goal/DoD (engine promotes under the cap), re-driving the non-`done` members (continuation).
   - **Chat `/goal`** — clear = the existing **`/goal clear`** (tears down `UnifiedMeta.GoalCondition`); set = re-establish it. `[FACT]` `/goal` is a session condition; `/goal clear` already exists.

   Because every unit — worker **and** verifier — runs as its own session, one `RequestCancelForSession` per session clears it all; no per-level bespoke cancel, no judge-specific machinery.

   Implementation reuses the existing goal clear/set + `/goal clear` primitives rather than per-level bespoke teardown. The Stop **event** therefore fires goal-clear at whatever level owns the goal; Start re-sets it. Critically, the plan/task goal is **persisted** (plan goal/DoD on the record; task criteria on the task), so "set it again" on restart re-derives it — the chat `GoalCondition`'s persistence across clear→set is the one open detail (G7).

10. **Member Stop ≠ Plan Stop (operator A5).** Pressing ■ on a single in-plan member clears **only that member's** goal — `RequestCancelForSession` over its worker session **and** its verifier session — + set it `cancelled`. The engine **continues** the plan's other independent members. **Plan outcome after a member cancel (grill GS-05 + R2-04):** the trigger is **"no further progress is possible"**, not "all members terminal" — `[FACT]` `AdvanceBlockedDependents` fires only on `done` deps, so a cancelled member's `blocked` dependents stay non-terminal forever and "all terminal" would never fire (the plan would rot to the frozen `idle_expired`). Rule: when ≥1 member was user-cancelled AND every non-`done` member is either terminal or blocked (directly or transitively) behind a cancelled member, the plan must **not** run judge rounds or wait for idle-expiry — it fails immediately with reason **`stopped_by_user`** (restartable), preserving the "re-run via plan restart" promise. The cancelled member is **not** auto-retried; its dependents stay `blocked`; it re-runs only via a plan restart (§6.7). Plan Stop, by contrast, is the fan-out over every `in_progress` member **+** the plan itself (§6.4).

### Judge / Verifier architecture (supersedes the shortcut call)

**Prior art (grounding, researched 2026-07):** Anthropic `/goal` runs a **separate small evaluator model after every turn** — *"the agent that wrote the code isn't the one grading it,"* deterministic criteria preferred ([loops docs](https://claude.com/blog/getting-started-with-loops)). OpenAI Codex `/goal` (Ralph loop) uses a **verifiable terminal state** (tests to green) + bounded turns/tokens + write-before-finish ([unrolling the Codex agent loop](https://openai.com/index/unrolling-the-codex-agent-loop/)). The `opencode` `willytop8/OpenCode-goal-plugin` ships the closest analogue to ours: an **independent, fail-closed auditor as a child session with read-only tools** (`read`/`glob`/`grep`), an **evidence-marker gate** (`[goal:evidence]` must precede `[goal:complete]`, bare claims auto-rejected), and bounded limits. CriticGPT shows a dedicated critic model is legitimate but oversight-oriented. Consensus: **executable/deterministic checks are the truth; when judgment is needed, the verifier gets read-only TOOLS to investigate — not a curated packet.**

**Problem this exposed.** Today the judge is `[FACT]` a one-shot **no-tools** `Provider.Chat` call (`judge.go:505`) fed only criteria + machine-check evidence + the worker's claim-summary — **no transcript, no tools, no artifacts** (`judge.go:26-29`). Fine for machine-checkable goals; **structurally blind for everything else** — and **Omnipus is not a coding tool**, so deterministic checks cover only a *minority* of goals (observed live: a "run 5 web searches" goal succeeded but the blind, fail-closed judge marked it not-done).

**Decision — the judge is a real agent in a "verifier role," own session.** Remove the shortcut; run the judge through the standard agent loop in its **own session**. A **verifier is a role you assign, not a species** — any agent + the properties below is a verifier, so **per-agent verifiers are trivially configurable later** (an agent's config points at a verifier agent).

**How a verifier differs from a normal agent — only these; everything else is identical** (same loop, same `ContextBuilder`/`SOUL.md`, same provider, same cancel/session):
1. **Memory OFF** — the one real execution flag; a verifier must be reproducible + impartial (same evidence → same verdict). Standing standards live in its **soul**, not episodic memory. (Named NEW work — grill R3-9: `[FACT]` the ContextBuilder injects memory unconditionally today; a per-agent memory-off option is a small new knob, not reuse.)
2. **Soul = the rubric — FULL unification (operator interview 2026-07-21, R3-1 closed).** Every agent has ONE prompt concept — its **soul**; editability is a **flag**, not a separate store. **Delete `AgentConfig.Rubric`** (`[FACT]` `config.go:813-820`): the Judge's judging standards become its soul, operator-editable while the agent stays otherwise locked (core agents keep their souls locked; custom verifiers use `SOUL.md` as any custom agent). The wire change (drop `rubric` from Agent.yaml/AgentUpdateRequest.yaml; re-label `AgentProfile.tsx`) is acceptable — **no existing installations, no backwards compatibility** on the v0.1.1 line. The third prompt-storage mechanism is eliminated.
3. **Read-only / verification tools** — the existing read-only file tools **`read_file`/`list_directory`** (artifacts; grill GS-01: the catalog has **no** glob/grep tool — a dedicated read-only content-search tool is optional named new work) + a scoped read-only `inspect_session` tool (below); no writes, mutations, commits, task-state changes, or delegation. **Seeding invariant redefined (grill R3-2):** `[FACT]` `seedSystemAgents` re-enforces **all-deny on every boot** as a hard invariant (`core.go:1226-1233`) — under it, verifier tool grants would be reverted at next boot. The invariant becomes **"System Agents carry exactly their seeded tool set, re-enforced every boot"** (for the Judge: the read-only verification set, everything else deny) — which also delivers verifier tools to upgraded installs automatically.
4. **Engine-invoked, input-as-data** — triggered at adjudication, not a chat persona; excluded from chat roster / routing / default-agent / delegation pickers (System Agents already are). The work-under-review is passed **as untrusted data, not instructions** (prompt-injection guard); the worker's claim is a claim, never a verdict.

(Cheap model, SEC-26 cost cap, no heartbeat/skills, seeded+locked are ordinary knobs or apply only to the seeded *default* — not verifier-defining.)

**Context model — auto-feed a window, tools only if needed:**
- **Default (one call, no tools):** fed the **criteria + machine-check evidence + worker's claim + the last N tokens of the working session.** For Omnipus the **transcript IS often the primary artifact** (a summary, research, N searches) — unlike opencode's file-first auditor — so the window is the main evidence channel. Renders a verdict in a single call — **as cheap as the old shortcut.**
- **Escalation (optional):** its read-only tools — files + `inspect_session` (history beyond the window, exact tool-call counts, specific outputs). **The rubric gates escalation:** *"judge from the provided context; if a criterion can't be confirmed, use your read-only tools, then judge."* No tool round-trips in the common case.
- **N is a cost/coverage dial** (global → per-verifier); evidence older than N → the `inspect_session` fallback.

**Three-rung evidence ladder (Omnipus-shaped):**
1. **Machine-checkable** — bash + expected exit code, engine-run (`runMachineCheck`). Deterministic, cheapest, least spoofable; preferred where expressible.
2. **Transcript / behavioral** — "called `web_search` 5×", "sent the message", "produced output Y" — deterministic from the **tool-call log** (`[FACT]` the session store already records per-entry tool calls, `daypartition.go:319-331`). Fixes the observed "5 searches" bug; covers far more of Omnipus than coding-tests do. (Named NEW work — grill R3-8: criteria kinds are `check|prose` only today; rung 2 needs a new **`behavior` criteria kind** + an engine-side deterministic scanner over the session's tool-call log — and then does NOT need the LLM verifier or `inspect_session` for these checks.)
3. **Subjective / qualitative** — "design until happy" — the tool-using investigating verifier over transcript + artifacts, at acceptance. No one has *objective* aesthetic judging; the win is independence + read-only re-verification + evidence-gating.

**`inspect_session` tool — security scoping (Constraint #6):** reading another session's transcript is a sensitive cross-session capability. The tool is **verifier-role only** (seeded policy allow=verifier, deny=all others), **scoped to the specific session(s) under review**, **read-only**. The target-session lock is **not expressible in tool policy** — it is enforced by an **engine-set context value** carrying the allowed target session id(s) into the verifier's turn (grill R3-10; precedent: `WithRunningTaskID`). Scope referent per level (R3-11): task/member verification → that task's session; **plan-level** verification → the plan session + that plan's member sessions (nothing else). Reuses the existing session-store read path; the new parts are the scoped view + the ctx plumb.

**Evidence-marker gate (adopt from opencode prior art):** require the worker to emit `[goal:evidence] <what was verified>` immediately before a completion claim; a bare claim is auto-rejected + re-prompted *before* the verifier runs — a cheap, high-value first filter. It **extends the existing ADR-043 completion-signal marker protocol** (one marker family, evidence line + completion marker), not a second parallel protocol (grill R3-13).

**Impartiality principle:** the worker/verifier split is about an **independent verdict, not hidden context.** The verifier *sees* the work (the fed window + its tools) — starving it blinds us to our own primary artifact — but judges **independently, skeptically**, treating the claim as data. The transcript-first choice softens purity vs opencode's file-only auditor; the mitigations (data-not-instructions framing, skeptical rubric, independent tool cross-check) are the right balance for a transcript-first platform.

**Visibility / audit (named NEW work — grill R3-6):** the verifier's own session is **persisted** (audit trail) but **hidden by default** from the chat thread + session-history list. This is **not** covered by the existing entry/render-level mechanisms (`EntryTypeSystem`, the delegate filter — keyed on `ParentSpawnCallID`, which an own-session verifier never has — and `toolVisibility.ts` are all per-entry, and `[FACT]` `listSessions` has no default exclusion; the SPA fetches all sessions unfiltered). Required new work, contract-touching (Constraint #8): a **verifier session-type value** on session meta (`UnifiedSessionType` is a closed enum today), **default exclusion in the session-list APIs**, and the SPA surfaces (Sidebar/Search/Usage) honoring it; surfaced on-demand (ActivityPanel + a "view judge reasoning" drill-down on the verdict). Verifier sessions follow the normal session **retention** (default 90d) — volume is bounded by judge invocations (R3-12).

**Cancel (+ the verifier-session registry — grill R3-7):** the verifier's own session is cancelled by the Stop **fan-out** (`RequestCancelForSession(verifierSession)`) alongside the member sessions — no special judge-cancel path. For the fan-out to have a handle, the engine keeps a **verifier-session registry**: `[FACT]` today's `inFlightJudge` is a `map[planID]bool` (no session id) — it becomes a registry mapping plan/task → verifier session id, **registered BEFORE the verifier is dispatched** (the same synchronous-assignment rule as M1 for member sessions), so a Stop in the creation window cannot miss it. (Own-session chosen for impartiality + resumability; cancel recovered by the fan-out. This supersedes the earlier "judge as a sub-turn" idea in §6.4/§6.9.)

CONFIDENCE: **High** — judge/verifier architecture (real agent, own session, read-only tools, transcript-window-first, memory-off, soul-not-rubric)
  Basis         : operator-locked over an extended design review + externally validated by prior art (Anthropic `/goal` separate-evaluator; opencode read-only-tool independent auditor; Codex terminal-state) + codebase `[FACT]` (shortcut `judge.go:505`; SpawnSubTurn/session machinery; SEC-26; session store).
  Evidence      : the live "5 searches" failure; the cited repos; the verified judge/session code.
  Missing       : exact `inspect_session` shape, the N default, and the memory-off `ContextBuilder` variant — plan-spec.
  Would improve : a spike on transcript-window sizing (cost vs coverage) + read-only-tool verifier fail-closed structured-output parity.

CONFIDENCE: **High** — core decision (autonomous agent plan+execute, tool-permission-gated, reuse engine + cancel)
  Basis         : operator-locked direction (evidence level 1) + direct codebase `[FACT]` for every load-bearing mechanism.
  Evidence      : verified `handleTaskStop→RequestCancelForSession` (rest_tasks.go:1398); plan state machine (plan.go:50-54); `handlePlanStop` state-only gap; no `plan_id` in agent tools; engine autonomy + cap admission; guardrail config.
  Missing       : the precise cancel-cascade leaf coverage was established by sub-agent trace, spot-verified at the two most load-bearing points — full re-verification lands in plan-spec's test design.
  Would improve : plan-spec BDD covering Stop-reaches-shell and restart-preserves-done; a spike confirming judge-round tail behavior.

CONFIDENCE: **High** — sub-decision G2 (forbid PUT→approved/running; single gated entry point)
  Basis         : grill-verified `[FACT]` — PUT sets `patch.State` directly (`rest_plans.go:651-654`) skipping criteria AND cap (`Admit` only in `tryStartApprovedPlan`, `plan_engine.go:388`); fix is a raw-body reject with a clear precedent (ADR-035 `sandbox_profile`).
  Evidence      : both approve paths + the cap-admission site verified.
  Missing       : whether to also unify PUT/POST approve into one handler — a refactor detail, not a correctness gap.
  Would improve : plan-spec locking the single gated transition entry point + a test that `PUT state:"running"` 400s.

CONFIDENCE: **High** — sub-decision G1+G8 (verifier = real agent, own session → cancelled by the Stop fan-out)
  Basis         : the Judge/Verifier architecture makes the verifier a real agent session; the Stop fan-out already sweeps member + plan sessions, so the verifier session is one more `RequestCancelForSession` — no new cancel machinery. A2 honored.
  Evidence      : `[FACT]` the `RequestCancelForSession` cascade (turn / subagents / OS-level shells) is proven; the shortcut `judge.go:505` is what's replaced.
  Missing       : verifier session lifecycle (create/register/cleanup) + read-only-tool fail-closed structured-output parity — plan-spec.
  Would improve : plan-spec BDD asserting a Stop mid-verification cancels the verifier session + its tool calls within the hard-abort window.

## 7. Risks and Caveats

- **One-way-door (security posture, sharpened — grill F10):** `[FACT]` standalone single-task execution is *already* autonomous today, so the marginal **new** autonomy is **multi-task DAG fan-out + agent-authored plans**, not "agents can now run code." Still, removing the human interlock shifts safety onto the guardrails + tool grants. Mitigation: seed `execute_plan` **Jim-only allow**, all others + the global ceiling explicit **`ask`** (operator-in-the-loop for any non-Jim attempt; F5's explicitness requirement holds); keep criteria-required, cap=16, idle=7d, attempts=3. Broadening `execute_plan` is a security decision; the grant UI should carry a **security affordance**, not read as an ordinary tool checkbox (grill F6).
- **G2 must ship with FR-3** — an execute path skipping criteria **and the cap** (§3 G2) is strictly worse than today's human gate. Forbid PUT→approved/running.
- **Stop fan-out is a locked engine op (grill F3):** it MUST run under `planDecisionMu` inside the engine, never the lock-free REST handler, or a concurrently-dispatched member escapes cancellation.
- **Verifier cancelled by the same session cancel (own-session):** the verifier runs in its own session, so `RequestCancelForSession(verifierSession)` — part of the Stop fan-out — cancels it + its tool calls + check bash like any session; no judge-specific machinery. **Refactor risk:** converting the judge from a direct `Provider.Chat` shortcut into a real agent (verifier role) must preserve its constrained, fail-closed, structured-verdict behaviour (SEC-26 cost caps, read-only tools, no free-tool escalation) — plan-spec pins this. Minor residual: a verdict returning microseconds before the cancel needs a state re-check so it can't mislabel a cancelled item.
- **Restart un-freezes `failed` narrowly (grill F1/R3-4):** only `failed[stopped_by_user]→approved` (plan — the engine then promotes under the cap) and `failed→next` (task) become legal; genuine plan failures stay frozen. Deliberate amendment of ADR-049's frozen-failed invariant, scoped to user-cancel. Idempotence: reset re-run members' `attempt_count`, preserve `done` evidence (plan-spec).
- **`cancelled`-as-`failed`+reason:** analytics/queries counting "failures" include user cancels unless they read the reason — but `[FACT]` `ComputeProgress` counts only `done` (`pkg/plan/store.go:492+`), so **plan progress is unaffected**; only raw failure-counts need the reason filter.

## 8. Confidence Assessment

Roll-up (per-block above): **core decision High**; **G2 (PUT never sets state) High — mandatory before ship**; **G1+G8 (verifier = own session, cancelled by the Stop fan-out) High**; **Judge/Verifier architecture High** (with the named new-work items: memory-off ContextBuilder option, `inspect_session`, verifier-session visibility filtering, verifier-session registry). No global-only score. Post-grill r3 (REVISE → addressed), the residual work is: the tool surface; the **locked** plan-Stop fan-out (under `planDecisionMu`) sweeping worker + verifier sessions; the G2 PUT-lockdown; restart→`approved` under the cap; and the judge-as-real-agent conversion. All bounded and grounded in verified code sites.

## 9. Validation / Next Steps

1. **Grill:** 3 rounds DONE — r1 REVISE (4 MAJORs fixed), r2 BLOCK (B1/B2 → judge-as-real-agent + restart endpoint), r3 REVISE (R3-1..13 → addressed above). Findings: `ADR-052-...-review.md`.
2. **Spec the chosen option:** `/plan-spec docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md` — resolve G1–G8 + the R3 items, and:
   - **Contracts (Constraint #8):** enumerate + author the new schemas before code — `create_plan` (goal/DoD/owner), the `plan_id`+`blocked_by` linkage on task-create, `execute_plan`, restart endpoints, the task cancel-reason field, the verifier session-type value, and any Stop wire shapes; regenerate `pkg/api/generated/` + `src/lib/api/generated/`.
   - the single criteria-gated approve entry point + the PUT-lockdown (G2);
   - the locked plan-Stop fan-out sweeping worker + **verifier** sessions (via the verifier-session registry, R3-7) + the verdict state re-check (F7);
   - restart→`approved`-under-cap + the store-level reason-gated transition + idempotence (R3-4/M2/A4);
   - the Judge/Verifier conversion: own-session lifecycle, memory-off ContextBuilder option, `inspect_session` (engine-set target ctx), transcript-window feed, visibility filtering (session-type + list-API default-exclusion + SPA), behavioral criteria kind (R3-8);
   - the UI button matrix + confirm modals + the `execute_plan` grant security affordance (F6).
3. **Spikes to raise confidence before committing:** (a) `RequestCancelForSession` reaches a shell leaf inside a plan-member turn end-to-end; (b) restart preserves `done` members + evidence and resets non-`done` cleanly; (c) a Stop mid-member-judge kills the judge call within the hard-abort window.
4. **Phase placement:** the **v0.1.1 release line** (operator, interview 2026-07-21), shipped together (tools + engine deltas + UI). Do not peel the UI buttons onto an earlier branch — they are inert without the tool surface and fan-out.
