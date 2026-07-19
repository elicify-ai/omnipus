# ADR-049: Planning & Goals — Plan entity, evidence-ladder judge, goal loops, System Agents

- **Status:** Proposed (ratification of operator interview 2026-07-19; **amended 2026-07-19 post grill-review r1**; pending `/grill-spec` re-run)
- **Date:** 2026-07-19
- **Deciders:** Operator (Daniel Piatkowski); structured/ratified by Albert
- **Evidence level (highest used):** 1 user-input (operator interview), grounded by 2–3 (documented patterns / shipped prior art) and codebase `[FACT]`s
- **Branch:** `feature/planning-goals` (cut from `release/v0.1.1` @ `96a1c1ab`)
- **Pipeline:** this ADR → `/grill-spec` → `/plan-spec` → wave implementation. **No `/taskify`** (operator direction).

> **Ratification mode.** The architecture direction was decided in an operator interview
> on 2026-07-19 after four grounding research passes (task subsystem map, goal/loop
> infrastructure inventory, command-surface map, external prior art). Amendment r1
> incorporates the grill-review findings (CRIT-001..003, MAJ-001..009, MIN-001..004,
> OBS-001..003) and two follow-up operator decisions (D3 scope; release placement).
> This ADR records decisions, rejected alternatives, and per-decision confidence.

## 1. Problem Understanding

Two operator-stated problems `[FACT — user input]`:

1. **Plans have no container.** Agents can already build dependency-linked task chains
   (`create_task` + `blocked_by`), but nothing names or groups a chain. The board can
   only filter by Milestone pills — there is no way to see "the task chain belonging to
   plan/release X". `[FACT]` The only grouping today is `Task.MilestoneID`
   (`pkg/task/task.go:191-255`), flat and date-oriented; no `Plan`/`Epic`/tag entity
   exists anywhere (no Go struct, schema, store, REST path, or tool).
2. **No goals/loops.** Omnipus has no goal-directed iteration: `[FACT]`
   `TaskExecutor` is single-shot — one turn, one ADR-043 verdict, terminal
   (`pkg/agent/task_executor.go:250` `finishTaskRun`); nothing consumes
   `task_status_changed` to coordinate a plan; there is no `/goal` or `/loop` command.
   The operator requires: chat-level `/goal` and `/loop` commands (Claude-Code-style),
   task execution always running as a goal loop with a judge, and whole plans running
   as a goal loop with a plan-level judge. This implies acceptance criteria / DoD on
   the task data model.

Blast radius: task data model + wire contracts (Constraint #8), task board SPA,
TaskExecutor, agent turn loop seams, commands palette, agent-type taxonomy (new
System Agents category), and **removal of Milestones** (a shipped feature).
`[Amended r1: the memory-compaction path is NOT in the blast radius — see D3.]`

Leverageable infrastructure already present `[FACT]`:
- ADR-043 fail-closed verdict parser (`pkg/agent/task_completion_signal.go`).
- Offline LLM-as-judge with 5-dimension scoring (`evals/judge/scorer.go:115`), eval-only today.
- Cron service with `continue` session mode, retry/backoff, overlap guard
  (`pkg/cron/service.go`); workspace heartbeat rides it (`pkg/gateway/heartbeat_schedule.go`).
- Turn-loop re-inject seams: `turnLoop` condition (`pkg/agent/loop.go:6257`),
  `pendingResults` drain (`loop.go:6371`), follow-up re-publish (`loop.go:5784`),
  async notifier that synthesizes inbound system messages — with a code comment
  explicitly anticipating "a Goals feature" (`pkg/agent/async_notifier.go:292`).
- Full slash-command palette both ends (`src/hooks/useSlashMenu.ts`,
  `GET /api/v1/commands`, `AgentLoop.handleCommand` `loop.go:9582`,
  `applyExplicitSkillCommand` `loop.go:9693` as the rewrite precedent).
- `blocked_by` DAG with cycle detection + auto-advance (`pkg/task/blocked_by.go:181-264`).
- The v0.3 preview-doc (`.preview-doc/delegation.html`) already sketches the
  coordinator pattern this ADR ratifies. `[FACT]`

**Context-compression facts (corrected, r1)** `[FACT — verified against branch base]`:
`forceCompression` no longer exists. `windowTrim` (`pkg/agent/loop.go:8779`) replaced
it under ADR-028 — token-budget eviction of oldest whole turns, **zero LLM calls**.
The only surviving LLM summarization is `maybeSummarize`/`summarizeSession`
(`loop.go:8654`/`:9312`), whose output renders as inert, reference-only legacy text
(`pkg/agent/context.go:877-885`). CLAUDE.md's Storage paragraph was stale on this
point and is corrected in the same change set as this amendment (OBS-001).

## 2. Extracted Requirements

### Functional
- FR-1: A first-class **Plan** entity groups a task DAG with a goal, DoD, state machine, and owner agent; the board can filter/view by plan. `[FACT — user input]`
- FR-2: **Milestones are removed**; tasks get generic **multi-tags** usable for releases/milestones/themes. Migration requirements in D1. `[FACT — user input]`
- FR-3: Tasks carry **acceptance criteria**: machine-checkable (command + expected exit code, evidence recorded under the rules in D2) and prose (LLM-judged). Each criterion records its **author identity** (r1). `[FACT — user input + review]`
- FR-4: **Every task execution runs as a goal loop with a judge**; every plan runs as a goal loop with a plan-level judge. Scratchpad (`set_todos`) tasks are exempt — they are never executed as goal loops (r1). `[FACT — user input; exemption from review]`
- FR-5: Chat **`/goal <condition>`** (proof-driven) and **`/loop`** (time-driven: interval mode and self-paced mode; `status`/`stop` verbs) commands. One active `/goal` per session, replace-on-set. `[FACT — user input]`
- FR-6: Agent tool-created tasks MUST carry ≥1 acceptance criterion (strict); agent tool paths cannot edit criteria count below 1 (r1); human/UI creation is soft (fallback in D5). `[FACT — user input + review]`
- FR-7 (r1): New **System Agents** category: seeded, locked, visible, editable model + rubric prompt. **This epic ships the category + the Judge only.** Standing rule ratified: every future *out-of-turn* LLM action (scheduled retrospectives, dreaming mode, memory consolidation) MUST run as a System Agent — the Memory System Agent lands with the first such feature (v0.3 memory work). `[FACT — operator decision 2026-07-19 r1]`
- FR-8: Goal-clear affordances at every level: task/plan card button, `/goal clear` (+ aliases), `/loop stop`. `[FACT — user input]`
- FR-9 (r1): Loop bounds configurable **globally and per entity that runs a loop** (plan, task, /goal, /loop). A workspace-level default layer is deferred until a concrete need appears (review MIN-004; a workspace is not itself a level where a goal is set). `[FACT — user input, scoped per review]`

### Non-Functional
- NFR-1: **No token/money brakes** — count + calendar bounds only; token spend visible, never enforcing. `[FACT — operator explicit]`
- NFR-2: Fail-closed judging — absence of evidence/verdict never defaults to success (extends ADR-043). Applies to check timeouts and policy denials (D2). `[FACT]`
- NFR-3: Graceful wind-down on any brake: finish current step, write handover summary. Handover destination: the plan/task record AND the owning session transcript (r1). `[FACT — prior art + review]`
- NFR-4: Hard Constraints #1–#8 hold. System Agents satisfy the explicit agent×tool policy coverage validation (D3) **and remain subject to SEC-26 rate limits and cost caps** (r1, CRIT-002). `[FACT]`
- NFR-5 (r1): Judge calls are auditable/visible via **named mechanisms**: a judge-verdict entry type in the session transcript wire contract, an ActivityPanel span type extension for System-Agent calls, and out-of-turn usage metering attributed to the System Agent's `agent_id` with plan/task/goal correlation IDs. (The previous unscoped "visible in ActivityPanel" claim was infeasible — MAJ-006.) `[INFERENCE from operator note, scoped by review]`

### Constraints
- File-based storage only (JSON per entity, atomic writes, striped locks). `[FACT]`
- No back-compat obligation for Milestone removal — precedent ADR-035/ADR-037. `[FACT]`
- **Release placement (r1, operator decision):** this epic ships **in release v0.1.1**. PRs target the release line; the release line merges to `main` at ship time via a human-approved PR (never admin-bypassed). Because non-`main` PRs do not auto-close issues, whoever merges the release→main PR closes the epic's issues manually with PR citations (repo convention). The epic consciously pulls the plan/goal/orchestrator slice of the `.preview-doc/` v0.3 concept forward; the Workspaces re-cast itself remains v0.3. `[FACT — operator decision 2026-07-19 r1]`

## 3. Gaps and Ambiguities

Resolved-by-ratification items moved into D1–D8 (r1). Remaining open items — all
plan-spec decisions, none direction-changing:

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| 1 | Judge default model on fresh install | Fresh installs may have one provider | Judge System Agent model chosen at onboarding; fallback = the default agent's model (no "cheapest" heuristic — not machine-derivable) | plan-spec |
| 2 | Cross-agent-authored machine checks: confirmation flow | CRIT-003c residual: trust edge ⇒ mechanical exec | Default: machine checks authored by a different agent than the assignee require assignee-owner confirmation OR a workspace setting to waive | plan-spec |
| 3 | ActivityPanel span shape for System-Agent calls | NFR-5 rendering | New span type mirroring subagent spans, same cap rules | plan-spec |
| 4 | `/goal`,`/loop` on non-web channels | Palette is SPA-only | Server-side parse in `handleCommand` works on any text channel; palette UI web-only | plan-spec |
| 5 | Role gating on a multi-user gateway | Any authenticated user can start loops | Loops obey existing session ownership/auth; channel-originated loops run under the routed agent + that session's brakes; no extra role gate in v1 | plan-spec + security review |
| 6 | Owner disabled mid-loop: notification detail | Paused-plan visibility | Resolved in D4 (r2): deletion rejected while owning active loops; disable pauses + surfaces blocked; plan-spec details the notification | plan-spec |
| 7 | Recurring `Trigger` tasks × goal loop | Interaction | Each recurring fire creates a run; each run gets the attempt loop; criteria immutable per run (D5) | plan-spec |
| 8 | Loops spawning loops | Runaway nesting | `/goal`,`/loop` are NOT available to task runs or delegated sub-turns in v1; enforcement discriminates on **message origin** (system/cron/async-injected messages cannot start goals or loops — a cron-injected prompt beginning `/goal` is inert), not merely on surface (r2); plan tasks bounded by existing `DelegationDepth` | plan-spec confirms enforcement point |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Correctness / fail-closed completion | High | #1 documented failure mode: workers claiming done |
| Reuse of existing infrastructure | High | ADR-043, cron, async-notifier, command palette |
| Transparency (Omnipus philosophy) | High | Internal LLM calls visible/configurable — drives System Agents |
| Cost control without money-brakes | Medium | Operator explicitly rejected token budgets |
| UX simplicity | Medium | Two commands, plain-prose goals, buttons to clear |
| Reversibility | Medium | Milestone removal + System Agents are one-way doors |

## 5. Option Analysis

Ratification format: chosen option per decision, rejected alternatives one line each.
Dimension tables for the three highest-blast-radius decisions.

### D1 — Plan container: new `Plan` entity; Milestones removed, replaced by multi-tags

| Dimension | Assessment |
|---|---|
| Strengths | Home for plan-level judge, DoD, owner, state machine; board filter by plan; tags cover releases/themes generically; matches preview-doc direction `[FACT]` |
| Weaknesses | New entity = store + REST + tools + SPA surface; Milestone removal deletes a shipped feature (`rest_milestones.go`, `MilestoneFilterPills.tsx`, milestone endpoints) `[FACT]` |
| Risks | Migration errors (mitigated by ratified requirements below); tag proliferation (mitigated by tag rules below) |
| Complexity | Moderate — mirrors the proven milestone store/REST pattern `[INFERENCE]` |
| Cost implications | Build: medium. Run: negligible (file-based). |
| Operational impact | Contract regen (Constraint #8), SPA board rework, docs |

**Ratified migration requirements (r1, MAJ-002; r2 refined):** idempotent; runs at
task-store load; logged; tag names are **normalized first** (lowercase, trim), then
truncated to the 64-char cap (including the `milestone:` prefix), then collisions
disambiguated (`milestone:<name>`, `milestone:<name>-2`, … keyed by milestone ID
order) — so post-normalization/truncation collisions ("Q3"/"q3") are caught (r2);
milestone `due_date` is **not discarded** — it is copied into each member task's
`Due` field when that field is empty (tasks with their own `Due` keep it);
milestones with **no member tasks** are preserved as migration-log entries (name +
due_date) since a tag cannot exist unattached (r2); "progress" needs no migration
(computed read-time `[FACT — rest_milestones.go:121-166]`); after migration the
`MilestoneID` field and milestone endpoints/schemas are removed from struct and
contracts; crash mid-migration is safe to re-run (idempotency requirement).

**Ratified tag rules (r1, MIN-003):** tags are free-form strings, lowercase, trimmed,
max length 64, max 16 per task, workspace-scoped namespace; `prefix:value` is
convention only (established by the migration's `milestone:` prefix), not schema.

Rejected: **Upgrade Milestone into the container** — conflates date-bucket with
executable plan. **Tags/saved-filters only** — no home for plan-level judge/DoD/state,
fails FR-4.

### D2 — Judge: evidence ladder, fail-closed

| Dimension | Assessment |
|---|---|
| Strengths | Machine-checkable criteria produce unfakeable evidence (exit codes); prose criteria get per-criterion `{met, reason}`; reasons feed forward as steering (evaluator-optimizer pattern, Anthropic "Building Effective Agents"); defeats self-certification |
| Weaknesses | Two evaluation paths; judge quality depends on criterion quality |
| Risks | Check-command attack surface (ratified execution rules below); prose-judge gaming (mitigated below) |
| Complexity | Moderate; ADR-043 verdict grammar extends (not forked) `[FACT]` |
| Cost implications | One judge call per iteration + check runtime; bounded by D7 |
| Operational impact | Evidence storage rules below; NFR-5 visibility mechanisms |

**Ratified machine-check execution rules (r1, CRIT-003):**
1. Checks MUST be dispatched through the assignee agent's **existing `bash` tool
   machinery** — same tool registry, policy resolution, sandbox enforcement, and audit
   trail. Building a parallel judge-owned exec path is **forbidden**.
2. Policy resolution in the unattended judge context: `allow` runs; **`ask` resolves
   to deny** (fail-closed — there is no interactive approver mid-loop); `deny` fails
   the criterion closed.
3. Every criterion records its **author** (agent or user identity). Cross-agent-
   authored machine checks: confirmation flow is Gap #2 (plan-spec), defaulting to
   assignee-owner confirmation.
4. Per-check **timeout, default 60s, configurable**, plus an output-size cap; timeout
   ⇒ criterion failed (closed). A hung check therefore cannot stall a loop or hold
   its idle-expiry clock.
5. Validation guard (r1, MIN-002; r2 widened): agent tool paths **reject**
   creating/updating a task whose criteria are all machine-type when the assignee's
   effective `bash` policy is deny **or ask** (rule 2 resolves `ask` to deny
   unattended, so both are structurally unsatisfiable); the UI warns instead.

**Ratified evidence rules (r1, MAJ-003 / Gap #5):** evidence passes the registered
sensitive-value redaction (`RegisterSensitiveValues`, ADR-004 flow) **before
persistence**; per-attempt size cap with truncation marker; stored under
`$OMNIPUS_HOME` with the same permissions posture as sessions; retention follows the
90-day session default; deleted with the task.

**Ratified prose-judge input rule (r1, OBS-003):** the judge's primary inputs are
machine-check evidence records and workspace file diffs; the worker's own summary is
provided last; the rubric instructs that **unevidenced claims score unmet** (NFR-2).

Rejected: **LLM-judge only** — transcript-only judge is documented as foolable.
**Marker+retry only** — keeps self-certification, fails the requirement.

### D3 — System Agents category (r1: Judge in this epic; Memory System Agent deferred to first real tenant)

| Dimension | Assessment |
|---|---|
| Strengths | Transparency parity: out-of-turn LLM functions become visible, model-configurable, metered, audited agents; the type revival path is clean `[FACT — Agent.yaml enum]` |
| Weaknesses | Touches agent taxonomy, Agents screen, seeding; adds exclusion rules across agent-enumerating surfaces (below) |
| Risks | Privilege-string interaction with SEC-26 (resolved below); UI noise (resolved by System section + exclusions) |
| Complexity | Moderate `[INFERENCE]` |
| Cost implications | Judge calls are metered and rate-limited like any non-core agent (below) |
| Operational impact | New "System" section in Agents screen; seeded locked agent; docs |

**Ratified scope (r1, CRIT-001/MAJ-008 + operator decision):** this epic ships the
**category + the Judge only**. The previously-planned "Summarizer reroute" was
premised on `forceCompression`, which no longer exists (§1 corrected facts); the
surviving `summarizeSession` output is legacy reference-only text and does not
justify same-epic scope. Standing rule (FR-7): all future out-of-turn LLM actions —
scheduled retrospectives, dreaming mode/Dreamcatcher, memory consolidation — MUST run
as System Agents; the **Memory System Agent** ships together with the first such
feature (v0.3 memory work). Today's memory LLM work (`/remember`, `/recall`,
`/retrospective`) runs inside the acting agent's own turn via steering prompts + tools
`[FACT — cmd_memory.go, applyMemoryCommandPrompt loop.go:9770]` and is unaffected.
**Grandfather clause (r2):** `summarizeSession` runs in a goroutine and is genuinely
out-of-turn; it is explicitly grandfathered un-attributed (legacy, reference-only
output) until the Memory System Agent lands — it is the sole exception to the FR-7
standing rule and is re-homed in the v0.3 memory work.

**Ratified privilege decision (r1, CRIT-002):** System Agents are **NOT privileged**.
The wire type value `system` is revived for the category, and
`security.IsPrivilegedAgent` (`pkg/security/ratelimit.go:17-22`) is **narrowed to
`core` only** in the same change, with SEC-26 tests extended to assert that a
type-`system` agent is subject to per-agent LLM rate limits and daily cost caps.
`[FACT]` no seeded agent uses type `system` today (`SeedConfig` seeds none), so the
narrowing has no seeded-install impact; operators who hand-crafted a legacy
type-`system` agent lose the (undocumented) exemption — recorded as an intentional
behavior change. Rejected: *new enum value (e.g. `system_service`)* — extra wire
surface for the same semantics; *keeping the exemption* — a silent SEC-26 bypass for
the system's highest-volume new caller is not a defensible cost posture.

**Ratified enumeration exclusions (r1, MAJ-001; r2 citation fix):** System Agents
are excluded from default-agent fallback resolution — `resolveDefaultAgentID` picks
the first enabled **chat-target** via `IsChatTarget()` (= `!IsWorker()`), so type
`system` must be excluded from chat-target status; invalid as channel-routing
binding targets (400 on write); excluded from delegation-target enumeration,
`list_agents` results for delegation pickers, and workspace team rosters; rendered
only in the Agents screen "System" section. **Lifecycle (r2, R2-MIN-004):** type
`system` is not creatable via REST or agent tools (400, mirroring the ADR-035/037
raw-body-sniff precedent) and seeded System Agents are not deletable — seeding is
the only creation path. Constraint #6 coverage:
seeded with explicit all-deny tool policies (they execute as no-tools structured
calls), keeping the boot-time agent×tool matrix total (`ValidateToolPolicyCoverage`
iterates `Agents.List` uniformly `[FACT — validate.go:491]`).

Rejected (from r0): **Judge as plain config setting** — invisible internals
contradict the transparency principle. **Judge as full roster agent** — full turns
per judgment cost more and pollute the delegation surface; execution stays a
no-tools structured call under the System-Agent identity.

### D4–D8 — remaining ratified decisions (chosen → rejected)

- **D4 Coordinator = hybrid.** Server-side plan engine dispatches ready tasks as the
  `blocked_by` DAG clears (extends `AdvanceBlockedDependents` + `TaskExecutor`); the
  owner agent is woken via the async-notifier seam only at decision points (attempts
  exhausted / plan judge failed / plan complete → synthesis). **Durability (r1,
  MAJ-004):** all loop/goal/plan counters and timestamps are persisted fields (Plan
  entity + `UnifiedMeta` extension following the `TaskID` precedent); the engine
  performs **boot-time reconciliation from the task store** — task statuses are the
  source of truth, events are an optimization; exactly **one engine instance** with a
  cron-style overlap guard (hot-reload safe — review Q10); the 7-day idle-expiry
  sweeper runs on the existing cron service. **Owner lifecycle (r2, R2-MIN-005):**
  deleting an agent that owns active plans/goal-loops is rejected (400) until they
  are reassigned or stopped; disabling pauses them (plan surfaces a blocked state on
  the board, resumes on re-enable) — no silent week-long stall path exists. Rejected: *pure agent coordinator*
  (token cost per step, stalls if the coordinator dies); *pure server engine* (no
  adaptive replanning).
- **D5 DoD policy = tiered.** Agent tool paths reject tasks without ≥1 criterion and
  reject criteria edits that reduce the count below 1 (r1); Scratchpad/`set_todos`
  tasks are exempt from goal-loop execution entirely (r1); UI/human creation is soft —
  judge falls back to prompt-as-criterion; a human task with neither criteria nor
  prompt is judged against **title + description** (r1); recurring-run criteria are
  immutable per run (r1). Rejected: *strict everywhere*; *optional everywhere*.
- **D6 Commands = separate `/goal` and `/loop`.** goal = proof-driven, loop =
  time-driven. `/loop` interval mode → cron `every` + `continue` session; self-paced
  mode → agent-chosen next delay + stated reason via one-shot `at` jobs;
  `status`/`stop` verbs; `/goal clear` + aliases. Implementation follows the
  `applyExplicitSkillCommand`/`applyMemoryCommandPrompt` rewrite precedent
  (`loop.go:9582+`) with `DeliveryAgent` definitions in `pkg/commands`. Command
  authorization follows existing session ownership/auth (Gap #5). Rejected: */loop
  folded into /goal*.
- **D7 Brakes = count + calendar only, graceful wind-down.** Defaults: task **3
  attempts** → wake owner; `/goal` **20 rounds**; **plan judge: 20 rounds** (r1,
  symmetric with /goal — MAJ-009); `/loop` **100 runs**; **7-day idle expiry**
  everywhere; hard ceiling mirrors `2×MaxIterations` `[FACT — loop.go:6272]`.
  **Definition (r1, MIN-001): a "round" = one worker turn plus its judge
  evaluation.** "Idle" = no attempt, state transition, or user interaction on the
  loop's unit (a hung check cannot hold the clock — checks time out at 60s, D2).
  **Global concurrency (r1, MAJ-009):** a configurable global cap on simultaneously
  ACTIVE goal loops (default **16**) across /goal + /loop + running plans; task
  attempt loops inside a running plan are bounded by the plan, not counted
  individually; `/goal` and `/loop` status output shows `active loops: N/cap`. Bounds
  are configurable globally and per entity (FR-9).
  **Judge unavailability ≠ verdict absent (r2, R2-MAJ-001):** NFR-2's fail-closed
  rule applies only to a judge that RAN and produced no/negative verdict. If the
  judge call itself is unavailable — SEC-26 throttled or cost-capped (the Judge is a
  shared, non-privileged bucket across all active loops per D3), provider error,
  timeout — the loop **pauses and retries with backoff** (reuse the cron transient
  schedule 60s/120s/300s `[FACT — service.go defaultRetryBackoffMs]`); the attempt
  is **not consumed**, no verdict is recorded, and the pause surfaces in loop/plan
  status. The idle-expiry clock keeps running, so a permanently unavailable judge
  ends loops via calendar brake, not via a correlated attempt-burn cascade. On brake fire: finish the current
  step, write handover to the plan/task record and owning session (NFR-3). Token
  spend visible via NFR-5 metering, never enforcing. Rejected: *token budgets*
  (operator explicit); *prose-embedded limits* (documented /goal complaint).
- **D8 Goal-clear everywhere.** Clear/Stop button on task and plan cards; `/goal
  clear` (+ aliases) and `/loop stop` in chat. Rejected: none — pure affordance.

## 6. Recommended Architecture

Ratified as decided: D1–D8 as amended. Assembly on existing seams — Plan store
mirrors the milestone store pattern; task loop wraps `finishTaskRun`; plan engine
consumes task-completion events server-side (boot-reconciled from the task store) and
wakes the owner through the async notifier; the Judge runs as a System-Agent-
attributed no-tools call, metered and rate-limited; the **plan-level judge is the
same Judge System Agent with a plan rubric** (review Q5 — no second seeded agent);
judge verdicts are written to the session transcript as a dedicated entry type
alongside the worker's ADR-043 marker so the two cannot silently disagree (review
Q3); **Plans are workspace-scoped and may only reference same-workspace tasks,
validated** (review Q4, mirrors the existing same-workspace blocker guard `[FACT —
pkg/tools/task.go:218-232]`); commands land in `pkg/commands` + `handleCommand`
rewrite hooks; goal/loop session state follows the `UnifiedMeta.TaskID` precedent
(`pkg/session/unified.go:62`) `[FACT]`.

**Contract surface (r1, OBS-002)** — new wire types to be specified spec-first, in
one table for plan-spec: `Plan`, `PlanState`, `PlanCreateRequest`/`PlanUpdateRequest`,
`Task.tags`, `AcceptanceCriterion` (kind, text, check, author, status),
`EvidenceRecord`, `JudgeVerdict` (+ transcript entry type), goal/loop status frames
(AsyncAPI), `Agent` additions for the System section, command registry entries, and
removal diffs for `Milestone*` schemas/endpoints.

CONFIDENCE: High (D1 Plan entity + tags + migration)
  Basis         : Operator decision; proven store/REST pattern; migration ratified with no data loss (due_date preserved)
  Evidence      : Task-subsystem map; milestone implementation as template; review MAJ-002 requirements adopted
  Missing       : Tag metadata shape if future needs exceed plain strings
  Would improve : plan-spec data-model section + idempotency test

CONFIDENCE: High (D2 evidence ladder + execution rules)
  Basis         : Convergent shipped prior art; ADR-043 grammar in place; CRIT-003 axes ratified (bash-tool dispatch, ask→deny, authorship, timeout)
  Evidence      : Prior-art report; verdict parser; sandbox/policy machinery reused by construction
  Missing       : Cross-agent-check confirmation flow detail (Gap #2)
  Would improve : grill-spec re-run + security tests named in §9

CONFIDENCE: High (D3 System Agents: category + Judge; non-privileged)
  Basis         : Re-grounded post CRIT-001 (Summarizer removed from epic — operator decision r1); CRIT-002 resolved by narrowing IsPrivilegedAgent; exclusions enumerated (MAJ-001)
  Evidence      : Verified enum + privilege seams; no seeded type-`system` agents today
  Missing       : ActivityPanel span shape (Gap #3); judge default-model choice (Gap #1)
  Would improve : plan-spec surfaces; SEC-26 test extension

CONFIDENCE: High (D4 hybrid coordinator + durability)
  Basis         : Half the machinery exists; durability/reconciliation/overlap-guard ratified (MAJ-004, Q10)
  Evidence      : Infra inventory with file:line seams; cron precedents for sweeper + overlap guard
  Missing       : Owner-agent-removed handling detail (Gap #6)
  Would improve : plan-spec state machine + crash-recovery test

CONFIDENCE: High (D5 tiered DoD) / High (D6 commands) / High (D7 brakes incl. global cap) / High (D8 clear affordances)
  Basis         : Direct operator decisions; review bypass/edge findings ratified into D5/D7 (MAJ-005/009, MIN-001/002)
  Evidence      : Command-surface map; cron capabilities; prior-art defaults
  Missing       : Channel-surface command behavior (Gap #4); role gating detail (Gap #5)
  Would improve : plan-spec BDD per surface

## 7. Risks and Caveats

- **One-way doors:** Milestone removal (D1) and the System Agents category +
  privilege narrowing (D3) are deliberate no-back-compat changes per repo precedent
  (ADR-035/-037). The privilege narrowing changes behavior for any hand-crafted
  legacy type-`system` agent (documented above, intentional).
- **Machine-check execution surface** is now bounded by construction (assignee's
  `bash` machinery, ask→deny, timeout, authorship) — residual risk concentrates in
  the Gap #2 confirmation-flow decision.
- **Judge cost multiplication** bounded by D7 counts, the global active-loop cap,
  and SEC-26 limits now applying to the Judge (CRIT-002 resolution).
- **Prose-criteria judging remains foolable in principle**; mitigated by
  machine-check preference, evidence-first judge input (OBS-003), and fail-closed
  defaults.
- **Contract surface growth** enumerated in §6; five-step wire-type process applies
  to every row before code (Constraint #8).
- **Release-line delivery**: shipping in v0.1.1 means manual issue closure at the
  release→main merge (recorded in §2 Constraints); the epic must not slip
  unreviewed scope into the release (Constraint #7 discipline applies).

## 8. Confidence Assessment

D1 High · D2 High · D3 High (r1 — Summarizer removed, privilege resolved) · D4 High ·
D5 High · D6 High · D7 High · D8 High. No Medium remains: the r1 amendments converted
the two stressed areas (D3 premise, check execution) into ratified requirements.
Residual uncertainty lives in the eight plan-spec gaps (§3), none direction-changing.

## 9. Validation / Next Steps

1. Re-run the red team: `/grill-spec docs/internal/architecture/ADR-049-planning-goals-system-agents.md` — verify CRIT-001..003 and MAJ-001..009 are closed; stress the remaining §3 gaps.
2. On PASS, spec the architecture: `/plan-spec docs/internal/architecture/ADR-049-planning-goals-system-agents.md` — resolves §3 gaps as spec decisions with BDD/TDD coverage, starting from the §6 contract-surface table.
3. Wave implementation with the 7-reviewer gate (per CLAUDE.md). **No `/taskify`** (operator direction).
4. Tests the review demands (carry into plan-spec): security tests for check execution (in-sandbox assertion; `bash: deny` ⇒ fail-closed; ask→deny; timeout); Constraint #6 boot test with a seeded System Agent; SEC-26 test for type-`system` non-privilege; plan-engine crash-recovery/reconciliation test; milestone-migration idempotency test; ADR-043 parser **extension** (not fork) tests.
5. CLAUDE.md correction (OBS-001: stale `forceCompression` Storage paragraph) lands with this amendment's change set.
