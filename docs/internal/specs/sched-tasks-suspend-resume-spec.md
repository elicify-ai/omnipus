# Feature Specification: Agent scheduled tasks and Suspend / Resume

**Created**: 2026-10-15 (UTC)
**Status**: Draft — specification grill round 2 corrected, 2026-10-15, plus **FQ-4 added after that final grill**. Q1 B, Q2 YES, FQ-2 and FQ-3 are settled (Source: Interview, 2026-10-15). FQ-4's founder rule is the same interview source; its three delivery choices are team-lead technical defaults, also 2026-10-15. FQ-1 A's creator reading is superseded by FQ-2 and is not normative. FQ-4 is new scope: no third grill, no implementation readiness and no review PASS. Verify FQ-4 at the code-review gate and at grill-code, not by another spec grill.
**Correction, 2026-10-15 (round 2):** said a timed fire keeps an original creator, and said a one-time fire that fails before claim can be recovered later by the queue; wrong because the founder ruled that the assigned agent does the work and today's stored `Task.Initiator` stays as the code already writes it (FQ-2), and because `SpawnReset` plus an `at` job with `DeleteAfterRun` drops the only job before `ClaimForRun` (FQ-3). Correct is AUTH-1, the pre-claim rule in §5.1, and BDD-64/BDD-65. The link check in §17 does not execute those scenarios.
**Addition, 2026-10-15 (after the final grill):** FQ-4. An agent-created scheduled task (`Task.CreatedByAgentID` non-empty) sends a short result notice into that creating agent's main chat and wakes that agent after every finished timed run. This does not change who the run executes as (FQ-2) and does not change today's starter or assignee notices.
**Correction, 2026-10-15:** said persisting task/goal before notification was a complete publication safeguard; wrong because recovery enumerates final task files independently; correct is goal-first preparation and complete-task publication last (§5.1.1). The same correction round completes authority/continuation ownership, Clear, REST preconditions/activation, request-wide limits, audit and hand-off requirements below.
**Correction (source self-check):** said task → goal locking in the initial fix draft, wrong because the goal-store contract mandates goalLock → taskFileLock → sessionLock → cacheMu; correct is goal → task coordinated creation/cleanup below. This was corrected before commit; no production code ran.
**Author**: architect, acting as specification author.
**Input**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/design/sched-tasks-suspend-resume-design-20261015.md`, committed at `3b9c4b4ab65e6f4680bf2248ba3145406a51374b`.
**Runtime baseline**: `feat/sched-tasks-20261015` at that commit. This document specifies future behaviour; it does not claim implementation, executed tests, UAT or a review PASS.
**Policy integration baseline**: L1 candidate `ad853522d1a9bbcbf800ea59b76c337cbada3feb`, inspected through Git objects, **not merged into this worktree**. Reconcile against the actual L1 landing before implementing policy/creation consumers. [S1, S15]

## 1. Authority, captured requirements and scope

This is a feature-sized change, not a new scheduler. People and agents must be able to schedule the same task, suspend its future automatic starts, and resume without replaying missed work. Suspension is independent of whether the last run succeeded or a run is still working. [S1–S4]

The dispatch supplies the committed design and D1–D6 as confirmed discovery input. A subsequent squad-lead steer supplies the founder's Q1 B / Q2 YES answers, **Source: Interview, 2026-10-15**, overriding the original brief and the design note's recommendations. Those answers are recorded below and amended into the design note in this same change. This author did not independently conduct that interview. No Q1/Q2 confirmation remains outstanding; Draft status reflects the still-required specification review/interview/fix process, not unresolved policy decisions. [S1, S22, S24]

| Design decision | Captured requirement | Authority in the input design |
|---|---|---|
| D1 | Repair agent access to task-trigger scheduling; keep one task/scheduler system and do not resurrect the retired cron tool or Schedules screen. | Context; Decision §2; Alternatives |
| D2 | Offer the Calendar's scheduling capability through structured agent input; also expose schedule reads, occurrences, history and explicit Run now. | Decision §2 and §5 |
| D3 | Keep schedule mutation behind one separate permission; ordinary create/update remain unscheduled task tools. | Decision §1, §2 and §5 |
| D4 | Global Ask, conversation-agent access, **new-worker** default Deny, readable exact-proposal approval and AutoAsks. Existing workers without an override inherit Ask (Q1 B); Omnipus may schedule through inherited Ask (Q2 YES). | Decision §3; dated founder correction; Source: Interview, 2026-10-15 |
| D5 | Suspend/Resume affect future scheduling only; preserve work already admitted, schedule identity and run truth; no missed-run replay. | Decision §1, §4 and §5 |
| D6 | Do not add occurrence exceptions, one-occurrence moves, event-versus-series choices, series-zone changes or Calendar slide-over deletion. | Decision §2; closing scope statement |

| Actor / boundary | In scope | Not in scope |
|---|---|---|
| Person | Calendar and existing task-detail schedule controls; exact approval card; factual run/history display. | New scheduling screen, raw schedule-syntax entry/display, new deletion affordance. |
| Conversation agent | Structured create/set/clear/suspend/resume within its resolved current workspace; authorized reads and explicit runs. | Foreign-workspace scheduling or borrowing operator authority. |
| Worker | Receives scheduled task work normally. New workers get scheduling Deny at creation; existing workers missing the key inherit global Ask. Explicit operator settings still govern. | Worker rollout/migration, runtime worker-specific policy fallback or control over an external command-line program's own tools. |
| Existing runtime | Task/paired-goal persistence, scheduler, execution admission, existing policy/grant/approval system, Calendar recurrence engine. | New service, dependency, scheduler, run-status vocabulary or suspension-history ledger. |

Priority labels below rank acceptance/safety within this feature; the dispatch sets no delivery deadline or independent queue urgency. [S1]

### 1.1 Decisions log — founder answers supersede the original recommendations

**Source: Interview, 2026-10-15**, relayed by squad-lead in the author dispatch. The original design note's Q1/Q2 open-question sections and the original brief's A recommendations are superseded. Its status and affected policy/delivery/docs text are amended in this same commit. [S24]

| Decision | Founder answer | Required behaviour |
|---|---|---|
| **Q1 B** | **No rollout or migration — greenfield rule.** | Existing workers with no `schedule_task` override stay on the reconciled global **Ask**. Only **new** workers receive explicit Deny at creation, including fresh built-in worker seeds. Preserve every existing entity/operator value. No worker backfill, rollout marker or runtime role-based fallback. |
| **Q2 YES** (original option B) | **The Omnipus system agent may schedule.** | Add only `schedule_task` to L1's `omnipusSupportedTools`. Omnipus has **no per-agent Ask entry** and rides global **Ask**, with **AutoAsks**. Ordinary local-workspace/ownership/delegation checks still apply. No implicit `list_tasks` / `run_task` expansion is authorized. |
| **FQ-1 A** (round-1 steer; **superseded by FQ-2, not normative**) | Withdrawn creator reading. | Source: Interview, 2026-10-15, round 1. Do not implement a separate original-creator record, and do not move today's retry write off `Task.Initiator`. FQ-2 is the authority rule. |
| **FQ-2** (round-2 founder question 1, MAJ-002) | **A timed run is a normal automatic run of the assigned agent. Do not track or describe an original creator.** | Source: Interview, 2026-10-15. Keep today's stored `Task.Initiator` exactly as the existing code keeps it, including `consumeTaskAttempt`'s retry carry-forward (a non-nil `retryAuthority` is written onto `Task.Initiator` with `Direct: true`; a nil person result does not overwrite). No new field, no history reconstruction, no migration. The round-1 plan to move that carry-forward off `Task.Initiator` is dropped: CRIT-001 happens before any receipt exists, and the `pending_attempt` receipt remains only the round-1 admission proof that separates an admitted continuation from a never-admitted twin. Scheduling permission is checked when Set/Resume runs. The assigned agent's membership and delegation checks still apply at fire time, through the existing automatic path. Honest consequence, stated in AUTH-1: a task last retried by a person runs on its timer with that stored authority, exactly as a retry does today. The approval card, reconnect preview and DOC-01/02/03 use only: “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.” |
| **FQ-3 A** (round-2 founder question 2, CRIT-001) | **Silent retry. The one-time job and the pre-reset task file survive until a run is actually claimed.** | Source: Interview, 2026-10-15. No warning surface and no skipped-history record. A repeating pre-claim refusal does not leave a queue-runnable `next` task and does not double-run. BDD-64 is the barrier coverage; BDD-57 does not cover the first fire. |
| **FQ-4** (after the final grill) | **Result notice to the creating agent.** Founder rule, Source: Interview, 2026-10-15: every finished timed run of a task with non-empty `Task.CreatedByAgentID` delivers a short notice into that agent's main chat in the task's workspace and wakes that agent. Team-lead technical defaults the same day: Q1 A, Q2 A, Q3 A. | See §5.7. The notice is not a fire principal and does not restore FQ-1. A person-created task (empty `CreatedByAgentID`) sends no agent notice. A pre-claim retry that never became a run (FQ-3) sends nothing. Verify at the code-review gate and grill-code. |

Q1 preserves **Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill** without an exception. Q2 is an explicit founder-authorized addition to the supporting set in **The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field**; it is not authority for other task-tool grants or foreign scheduling. FQ-2 and FQ-3 are settled in the same interview source and are normative; FQ-1 A is not. There is no remaining Q1/Q2 or round-2 confirmation gate. [S14, S15, S24, S31]

### 1.2 Round-1 correction authority and ADR applicability

The round-1 correction input is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review.md`::MAJ-001–MAJ-007 / MIN-001–MIN-002 / OBS-001, reviewed at `08af07b45a8b558a5915ea53a5545f1d72572470`. The round-2 correction input is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review-round2.md`::CRIT-001 / MAJ-001–MAJ-004 / MIN-001–MIN-003 / OBS-001, reviewed at `dc43856edeb106e974841eb7ac40e8dc30d3a77c`. **FQ-2 and FQ-3 — Source: Interview, 2026-10-15** — supersede FQ-1 A's creator reading. AUTH-1 is the sole detailed authority rule. A timed run uses today's stored `Task.Initiator` and is work for the assigned agent. Set/Resume checks scheduling permission at that call; removing that permission later does not stop the schedule. No extra standing-authority state is authorized. [S25, S31]

**ADR applicability ruling:** **no new ADR** is required for this feature hand-off now that D1–D6, Q1/Q2, FQ-2 and FQ-3 are settled by the founder. FQ-4, added after the final grill, also reuses existing delivery primitives (§5.7) and does not commission an ADR. These corrections reuse today's stored task authority, the existing cron job and its transient-retry classification, the task/trigger owner, additive run history and the two-layer policy model; they do not select a second scheduler, execution model, creator record or policy system. The governing decisions are **RRULE (RFC 5545) as the Recurrence Model for Task Triggers**, **Per-Task Run History (an additive execution-record layer)**, **Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill**, and **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**. Superseded passages remain qualified in §15. The founder interview leaves no architectural choice open to commission an ADR for; do not manufacture one for resolved choices. Any later genuinely open significant choice goes back to team-lead/founder and the architect's prescribed one-grill/one-correction ADR process, not a silent implementation assumption. This is specification correction round 2 of 2, not a third grill. [S14, S21, S22, S25, S31]

### 1.3 Round-1 finding dispositions — corrections, not a review PASS

| Finding | Spec correction / discriminating acceptance |
|---|---|
| MAJ-001 | Round-1 text kept the scheduling-permission check and forbade a new standing-authority field. **FQ-2 supersedes the creator reading and the “retries cannot overwrite `Task.Initiator`” sentence.** See §1.4. |
| MAJ-002 | §5.1.1 goal-first/complete-task-last publication, canonical goal→task locking, orphan cleanup and crash/discovery cut points. FR-001, BDD-07/56, T16/T18. |
| MAJ-003 | §5.1 positive admitted-continuation predicate/receipt, later queue pickup after capacity refusal and never-admitted twin; preserve named current-graph/budget regression. The receipt stays. FQ-2 drops only the planned move of the `consumeTaskAttempt` carry-forward off `Task.Initiator`. FR-009/014, BDD-57, T20/T28. Not the first fire (that is BDD-64). |
| MAJ-004 | Locked Clear sends idle unadmitted Next/blocked work to Inbox; actual later drain/unblock stays inert, admitted work preserved. FR-002, BDD-58, T17. |
| MAJ-005 | §5.5 required conditional trigger PATCH / locked compare / exact 400 and 409; both views keep drafts for read/review/reapply. FR-031/033, BDD-59, T11/T26/T31. |
| MAJ-006 | Shared Task.schedule activation state in 200 mutation and GET read-back; saved-unavailable versus exhausted and both-controls recovery. FR-028/033, BDD-44/60, T10/T26/T31. |
| MAJ-007 | §5.6 request-wide series/work/item/encoded-byte maxima and whole-query refusal, selected-series recovery. FR-025, BDD-61, T09/T27 and DS-4 exact-cap/+1 cases. |
| MIN-001 | §5.3.1 existing semantic audit with actor/transition/outcome, no-op and sink-failure behaviour, no instruction ledger. FR-035, BDD-62, T36. |
| MIN-002 | §1.2 explicit no-new-ADR ruling after founder answer, §5.0 contract inventory before mechanisms, §6.5 named catalogue-first dependencies. Document integrity checks these sections; no visual redesign. |
| OBS-001 | Adopted: T35 is thin browser wiring only. Full lower-level policy matrix and every mandatory UAT-07/UAT-08 journey remain. |

### 1.4 Round-2 finding dispositions — corrections, not a review PASS

Reviewed against `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review-round2.md`. Founder answers are FQ-2 and FQ-3 in §1.1. No CRITICAL item is left open. The document-link check does not execute BDD-57 or BDD-64 and is not evidence that the first fire is covered. [S31]

| Finding | Spec correction |
|---|---|
| CRIT-001 / FQ-3 A | §5.1: a one-time pre-claim refusal (capacity, unmet dependency, plan gate) does not keep `SpawnReset` writes and does not remove the `at` job. The same job retries silently until `ClaimForRun`; no warning and no skipped-history row. A repeating pre-claim refusal re-arms only the next planned position and must not also leave a queue-runnable `next` task. BDD-64, DS-5 row 20, T20. BDD-57 is not this case. |
| MAJ-001 | A Set/replace whose saved result stays suspended carries `firing_stays_off_notice` on the frozen preview and the registered card, live and after reconnect, before the decision controls: “Automatic firing stays off. This change does not resume the schedule.” Missing or altered text blocks Approve / Always Allow. An unsuspended Set does not show that sentence. Count-restart text is not a substitute. BDD-65, FR-020. |
| MAJ-002 / FQ-2 | AUTH-1, FR-014, FR-036, BDD-55, BDD-63 and DOC-01/02/03. Today's `Task.Initiator` write stays. No creator wording on the card or in those TODOs. |
| MAJ-003 | §5.6 work units are an input formula. The frozen cap integers are 100000 and 100001; the worked small total is 7. DS-4 row 12. Not a counter the production function returns. |
| MAJ-004 | `NewCustomAgentToolsCfg` sets `schedule_task` to explicit Ask. `workerTierDeniedTools` then stores Deny for a new worker. Mia and Jim inventory Ask, so the sparse seed omits the key. Worker, Planner and Researcher stay inventory Deny. Do not change `denyAllThenOverride`, `buildToolConfig`'s final fill, or `resolveEffectivePolicyWith`. |
| MIN-001 | Create/set/resume audit rows add a non-secret fire principal: `human` or the agent id, plus `retry_carry_forward` when stored `Initiator.Direct` is true. No depth, budget, prompt or criteria. BDD-62. |
| MIN-002 | A timing-clear PATCH that also sets `next`, `in_progress`, `done` or `failed` is 400 and writes nothing. Omitted status and explicit `inbox` use the Clear transition. DS-1 row 14. |
| MIN-003 | DOC-01 and DOC-05 say a trigger change must send `schedule_hash` from the last Task read; a client-computed hash is not valid; metadata saves omit both `trigger` and the hash. |
| OBS-001 | §17 and SC-001 state that the link check does not prove first-fire behaviour. |

## 2. User stories and acceptance criteria

These criteria describe observable behaviour. Mechanisms and wire shapes appear later, separately.

### US-1 — Ask an agent to schedule work without knowing schedule syntax (P1)

A person asks a conversation agent for a weekly report or a one-time reminder. The agent can create the work, change its timing, or remove its schedule without disguising timing as ordinary task editing. The person can tell whether the work was saved and whether automatic firing is available.

**Why this priority**: this is the missing agent entry point in D1–D3. **Independent test**: use one eligible agent and one workspace to create, read, change and clear a future task; no run starts merely because the task was created.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-1.AC1 | **Given** an eligible local agent and complete work instructions, **When** it schedules a one-time or repeating task, **Then** one task and its judged completion contract are saved, initially Inbox, with the approved timing and no immediate run. A one-time fire that cannot claim yet keeps that same job and the pre-fire task file and still runs once when it can, with no missed-run record (FQ-3). |
| US-1.AC2 | **Given** an owned task, **When** its schedule is set, replaced or cleared, **Then** timing changes without changing task identity/history; Clear returns not-yet-admitted ready/dependency-waiting work to Inbox so later queue drains or dependency completion cannot start it, while admitted work keeps its own continuation. |
| US-1.AC3 | **Given** a supported repeat pattern, **When** an agent proposes it, **Then** its occurrences match the Calendar's interpretation, including time zone, month end, daylight-saving changes and end condition. |
| US-1.AC4 | **Given** invalid input or failed storage/scheduler dependencies, **When** scheduling is attempted, **Then** the caller receives the actual refusal or saved-but-unavailable outcome, never false activation or an instruction to create a duplicate. |

### US-2 — Suspend future starts without stopping work already admitted (P0)

A person or permitted agent suspends a schedule while the current run may still be working. Resume restores future opportunities, not the missed workload. Unrelated edits and restart cannot undo the suspension.

**Why this priority**: a false suspension can spend money or perform unwanted work. **Independent test**: suspend a timed task with a controllable clock, advance through due positions, and observe no new automatic run; an already admitted run may finish.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-2.AC1 | **Given** an active time schedule, **When** suspension succeeds, **Then** no new automatic schedule admission occurs afterward; previously admitted work and its retries continue normally. |
| US-2.AC2 | **Given** missed calendar positions, **When** the schedule resumes, **Then** only a strictly future eligible fire is armed; planned positions still count toward the original end and an expired one-time event does not fire. |
| US-2.AC3 | **Given** a suspended task, **When** another field, run outcome or schedule definition changes, **Then** suspension stays set unless the schedule is explicitly resumed or cleared. |
| US-2.AC4 | **Given** invalid targets, invalid stored scheduling data or restart/recovery, **When** suspension state is consulted or changed, **Then** invalid changes refuse and valid persisted suspension remains effective; faults are visible rather than treated as success. |

### US-3 — Keep scheduling authority understandable and local (P0)

An agent discovers one scheduling capability with an explicit policy. New workers receive explicit Deny at creation; existing workers without this override inherit global Ask without any migration. Omnipus may schedule through inherited Ask. A creator or assignee can manage legitimate local work, but delivery to another team does not grant permission to schedule it there.

**Why this priority**: future execution is an authorization boundary, not a cosmetic task field. **Independent test**: inspect discovery and attempt calls across conversation-agent, worker, operator-override and foreign-workspace fixtures.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-3.AC1 | **Given** a properly wired installation, **When** an agent searches for scheduling, **Then** eligible conversation agents, including Omnipus, can discover and invoke it under the effective policy; new-worker Deny, existing-worker inherited Ask and unattended Ask behaviour are enforced. |
| US-3.AC2 | **Given** an operator's saved settings and the confirmed Q1 B/Q2 YES decisions, **When** defaults are seeded or the installation upgrades, **Then** explicit choices survive, existing workers are not migrated/backfilled, only new workers get default Deny, and Omnipus inherits the global scheduling policy without a per-agent Ask entry. |
| US-3.AC3 | **Given** a local task and a known caller, **When** scheduling or explicit execution is authorized, **Then** today's assignee-or-agent-created ownership and current delegation checks apply, including after approval. A timed run is a normal automatic run of the assigned agent under the stored `Task.Initiator` (FQ-2), including a retry carry-forward already on the file. Scheduling permission is checked at Set/Resume. Enabled auditing records the actor and the non-secret fire principal without copying task instructions. |
| US-3.AC4 | **Given** an unresolved caller, unrelated task or foreign/inaccessible destination, **When** mutation is attempted, **Then** it refuses before any task, goal, run or session change and explains the local-delivery boundary. |

### US-4 — Approve the exact future commitment, including after reconnect (P0)

The person sees which agent wants to change which task, what the previous and proposed schedules mean, and any suspension/resume consequences. Returning to the app must show that same proposal, not the latest task or bare arguments.

**Why this priority**: approving a different time or target is not informed consent. **Independent test**: hold an approval pending, reconnect, and compare the proposal before approving or refusing it.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-4.AC1 | **Given** a proposal needing approval, **When** its card appears, **Then** all five actions have readable identity/timing/consequence details; creation exposes its whole instructions/completion contract; create/set/resume show the FQ-2 notice that the schedule keeps running until suspended or removed and that later removal of this agent's scheduling permission does not stop it. A Set whose saved result stays suspended also says, before the decision controls, that automatic firing stays off and this change does not resume the schedule. |
| US-4.AC2 | **Given** a prepared proposal or remembered exact-call grant, **When** it proceeds, **Then** defaults stay fixed, changed targets/schedules do not reuse an earlier grant, and changed authority or approved state refuses before writing. |
| US-4.AC3 | **Given** an outstanding approval, **When** the browser reconnects or reloads, **Then** the original complete proposal and remaining decision lifetime return without inventing new scheduling details. |
| US-4.AC4 | **Given** Deny, Cancel, expiry or unusable preview information, **When** that condition is reached, **Then** no scheduling mutation occurs; invalid previews cannot be approved and the person receives recovery guidance. |

### US-5 — Read schedules and history, and explicitly run permitted work (P1)

An agent can distinguish planned calendar positions from the next automatic fire and the last actual result. It can inspect old runs even after a rule edit. An explicit Run now is allowed during suspension without secretly resuming the schedule.

**Why this priority**: the agent needs a read-back and recovery workflow, not only a write tool. **Independent test**: inspect a suspended series with prior runs, change its rule, then run a past/current occurrence under normal authority.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-5.AC1 | **Given** matching local tasks, **When** the agent lists them, **Then** bounded rows disclose schedule/suspension, up to three planned next positions, actual nullable next fire and latest recorded run without private provenance. |
| US-5.AC2 | **Given** a valid calendar range, **When** occurrences are requested, **Then** authorized agent and Calendar reads agree on planned positions, buckets, limits and suspension without expanding forbidden tasks first. |
| US-5.AC3 | **Given** existing history or a read fault, **When** runs are requested, **Then** history stays newest-first and independent of the current rule, pagination is explicit, faults are not empty history and foreign status-only access reveals no schedule/results/sessions. |
| US-5.AC4 | **Given** a suspended scheduled task, **When** an authorized explicit run is requested, **Then** normal execution gates apply, suspension remains set, future calendar positions are not consumed, and only actual acceptance/session facts are returned. |

### US-6 — Use reversible schedule controls in both existing detail surfaces (P1)

A person finds Suspend schedule or Resume schedule next to the schedule, not next to execution Stop. The controls explain what will happen, show saving/failure, and update the other open views after server confirmation.

**Why this priority**: an API without visible controls does not deliver D5 to people. **Independent test**: render and operate the Calendar editor and existing task detail separately using the same persisted task.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-6.AC1 | **Given** a user-surface timed task in either existing detail surface, **When** the schedule section renders, **Then** the correct Suspend schedule or Resume schedule control is visible beside it; manual tasks and heartbeats have no such control. |
| US-6.AC2 | **Given** a control activation, **When** the save is pending, succeeds, fails or loses its response, **Then** the UI shows the real pending/persisted/error state, prevents duplicate pending activation and offers safe read-back/retry without inventing success. |
| US-6.AC3 | **Given** multiple views or a stale form, **When** a save or refresh completes, **Then** task/list/detail/occurrence state is refreshed without clearing suspension, overwriting the newer schedule or erasing unrelated unsaved form edits. |
| US-6.AC4 | **Given** keyboard, narrow-screen or assistive-technology use, **When** the person uses the controls or approval card, **Then** labels, focus, errors and schedule consequences are accessible without colour-only meaning or raw rule syntax. |

### US-7 — See schedule availability without falsifying run outcomes (P1)

A suspended series remains visible in the Calendar. Recorded outcomes and chat links stay truthful. A current suspension flag cannot prove what happened to an unrecorded past position.

**Why this priority**: schedule state and execution truth answer different questions. **Independent test**: render future/unrecorded, past/unrecorded and recorded occurrences of the same suspended series, plus a one-time event and a dense day group.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-7.AC1 | **Given** suspended recurring work, **When** its occurrences render, **Then** future unrun positions say Suspended, past unrecorded positions stay No record, and recorded runs keep their own status/result/chat link. |
| US-7.AC2 | **Given** a suspended one-time event or grouped day, **When** it renders, **Then** suspension is identified in text while existing timestamps and factual run counts remain unchanged. |
| US-7.AC3 | **Given** a refreshed or older compatible response, **When** views load, **Then** absent suspension means active, invalid responses surface an error, and scheduled work remains excluded from Board/List. |
| US-7.AC4 | **Given** restart, upgrade, deployment or operator rollback planning, **When** the feature is used and documented, **Then** persistence and supported-platform behaviour are verified and the older-binary suspension hazard is clearly disclosed; no new runtime service is required. |

### US-8 — Tell the creating agent when a timed run finishes (P1)

An agent that created a scheduled task hears about every finished timed run, in its own main chat in that task's workspace, and is woken so it can react or tell the person. A task a person created does not wake an agent. This story is FQ-4, new scope after the final grill. It does not change who runs the task (FQ-2).

**Why this priority**: a creating agent that never hears the outcome cannot tell the person or decide what to do next. **Independent test**: finish one scheduled run for an agent-created task and one for a person-created task; only the first wakes the creating agent's main chat.

| Acceptance scenario | Given / When / Then |
|---|---|
| US-8.AC1 | **Given** a scheduled task whose `CreatedByAgentID` is set, and a timed run that has finished `done` or `failed` (one-time or repeating), **When** that run closes, **Then** the creating agent's main chat in the task's workspace receives one short result notice and that agent is woken: an idle main starts one turn, a busy main receives the notice queued into the current turn, and a main that has never had a turn gets the same ordinary-root record a first turn creates and is then woken. |
| US-8.AC2 | **Given** the creating agent is deleted, no longer in the workspace, or its main is hidden, or the task was created by a person, or the fire never became a run, **When** the schedule reaches that point, **Then** no substitute wake is sent. A finished run stays in run history and the miss is logged. A person-created task and a pre-claim retry send no agent notice. |
| US-8.AC3 | **Given** a finished timed run whose stored text includes a `TASK_SUMMARY` line, a secret, the task instructions, and a result longer than one paragraph, **When** the notice is built, **Then** the summary is that one paragraph after the existing sensitive-data filter, cut at 500 UTF-8 bytes, and the notice does not contain the instructions or the full result. |
| US-8.AC4 | **Given** the notice wake, **When** it is delivered, **Then** it does not open a task run and does not fire the schedule again. A later scheduling request from that woken turn is an ordinary scheduling call under the normal checks. |

## 3. Behavioural contract, edges and safeguards

### 3.1 Behavioural contract

| When | The system must |
|---|---|
| Valid local scheduling is approved/permitted | Save exactly the proposal and report saved versus armed/disarmed/exhausted/unavailable truthfully. |
| Suspension commits before admission | Admit no new automatic work for that schedule, even if an old callback or notification arrives later. |
| Admission commits first | Let that run and its own attempts continue; suspension is not Stop. |
| Resume crosses missed positions | Skip them, preserving the original planned-position count and end; arm only a strictly future fire. |
| An unrelated save or run outcome lands | Preserve suspension and any untouched timing. |
| A schedule is cleared | Remove automatic scheduling and suspension without starting work or deleting history. |
| Approval state/authority changed | Refuse the stale commitment with no write and ask for a freshly prepared proposal. |
| A read, write or activation dependency fails | Expose the failure; never fabricate no-history, successful activation or safe replay. |
| A person explicitly runs suspended work | Apply ordinary run gates and keep the schedule suspended. |

### 3.2 Edge cases and exact outcomes

| Edge | Required outcome | BDD coverage |
|---|---|---|
| Due callback versus Suspend, including engine-lane queuing | Exactly one admission order; zero new automatic admission if Suspend won. | BDD-10, BDD-11 |
| Suspend → Resume restores identical rule | Old callback remains invalid despite equal rule text. | BDD-11 |
| Delayed upsert, recovery or run-completion notification | Re-read authoritative state; never re-arm suspended or superseded work. | BDD-11, BDD-17 |
| Queue/dependency/follow-up sees a timed task as ready | Do not start a new run outside its authorized timed or explicit admission. | BDD-18 |
| One-time fire refused before claim (capacity, unmet dependency, or plan gate) | Keep the pre-fire task file and the same job; retry silently until exactly one claim; no warning and no skipped-history row. | BDD-64 |
| Repeating fire refused before claim | Do not leave a queue-runnable `next` task; arm only the next planned position; do not double-run. | BDD-64 |
| Resume at exactly a planned instant | That position is not strictly future; choose the next one. | BDD-12, BDD-13 |
| Count/date end expires while suspended | Resume succeeds with no future fire; edit the rule or use permitted Run now. | BDD-13 |
| Future one-time schedule on a Done/Failed mirror | A deliberate future set/move remains eligible; execution status is not a new schedule status. | BDD-14 |
| Retry of an already matching Suspend/Resume/Clear | Successful no-op; no duplicate job or run and no re-anchor. | BDD-03, BDD-09, BDD-12 |
| Time-zone offset mismatches the chosen named zone | Refuse the proposal; do not guess a zone or silently shift the time. | BDD-05, BDD-06 |
| Missing caller, removed assignee membership or unreadable authority | Refuse before new execution or mutation, including after approval. | BDD-23–BDD-25, BDD-30 |
| Response lost after persistence | Show unknown save outcome, read back before retry; no blind create retry. | BDD-08, BDD-44 |
| Old file/response without the new flag | Treat as false; no bulk task migration. | BDD-17, BDD-51 |
| Older binary reads a suspended file | It may ignore suspension and fire: rollback is unsafe while relying on the flag. | BDD-53 |
| Agent-created timed run finishes (`RunKindScheduled`, `done` or `failed`) | One short notice in the creating agent's main, and a wake of that main. Not sent to a person-created task. | BDD-66, BDD-67, BDD-68 |
| Creating agent deleted, removed, or main hidden | No substitute wake. The run remains in history. The miss is logged. | BDD-69 |
| Notice text | Status word, bounded summary, no task instructions, no full result. | BDD-71 |
| Notice wake | Not a task run and not another schedule fire. | BDD-72 |

### 3.3 Explicit non-behaviours

The system must not confuse suspension with execution Stop/Restart, freeze a run's attempts, replay misses, freeze occurrence counts, invent skipped-history entries, or relabel past missing records as historically suspended. These would change the approved meaning of D5. [S1, S4]

The system must not restore a cron agent tool, Schedules/Command Center screen, legacy task-timing compatibility, raw cron/RRULE UI, a generic per-agent deny repair, a runtime role-based permission fallback, or foreign-workspace arming. These conflict with current decisions. REST's existing validated recurrence representation remains an internal/API boundary, not a raw-rule agent escape hatch. [S1, S14, S21, S22]

The system must not add skip/move-one-occurrence operations, event-versus-series prompts, zone-changing controls, task deletion to the new tool, or Calendar slide-over deletion. Existing deletion and heartbeat controls remain separate. No new feature may be smuggled into these mechanisms as cleanup. [S1]

FQ-4 must not add the creating agent to `captureRunRecipients`, must not change the full-result notice today's starter and assignee already receive, and must not change `wakeStoredMessage` for an ordinary child-finished wake. The known gap remains: that ordinary path can still leave an idle parent with only an inbox row (`DeliveryStoredNotWoken`). It is tracked for L2 with #1317, whose open title is “Woken parent turn's reply not shown live in the open chat (wake streams under channel main, not webchat)”. FQ-4 does not fix that gap. [S32]

### 3.4 Machine-verifiable constraints

| Boundary | Exact acceptance constraint |
|---|---|
| Repeat input | Frequencies minute/hour/day/week/month/year; integer interval 1–99; weekly 1–7 unique valid weekdays; monthly day 1–31 or ordinal 1–5 / last; count 1–100,000. Missing required, null, wrong-type, fractional, contradictory and unknown inputs refuse. |
| Time input | One-time absolute date/time includes an explicit offset. New recurrence requires a starting instant and named time zone. Existing-series edits keep the stored zone; another zone refuses. |
| Shared recurrence validation | At least 60 seconds between allowed fires, rule representation at most 512 characters, an occurrence within five years of its anchor, and valid end/zone. Suspension never bypasses validity. |
| Occurrence query | Start inclusive/end exclusive; start precedes end; range at most 400 × 24 hours. At most 500 raw instants / 10,000 irregular iterations per task; at most 8 × 24 hours uses detail mode; broader ranges group days with more than three positions. Request-wide maxima: 100 authorized candidate series, 100,000 work units by the input formula in §5.6 (not positions visited), 10,000 output items and 1,048,576 encoded UTF-8 JSON bytes. Exceeding any maximum refuses the whole query, never a successful partial calendar (§5.6). |
| Agent lists/history | Task rows at most 100, newest-updated first; upcoming preview at most three positions. Run page default 20, maximum 100, nonnegative integer offset; disclose total matched, returned, truncation and next offset. |
| Suspend/Resume HTTP | 200 Task with required-on-these-responses `schedule` state for a saved change/no-op, **including** saved-but-unavailable activation; 400 malformed ID/body; 404 absent task; 409 manual/no schedule or heartbeat; 500 storage failure before commit. Existing authentication/authorization applies unchanged. `schedule.scheduler_state=exhausted` is successful no-future-fire, not activation failure (§5.5). |
| Conditional trigger PATCH | `expected_schedule_hash` is required with any supplied trigger, forbidden without it. Missing/malformed precondition: 400 ErrorResponse, `code=schedule_precondition_required`, `field=expected_schedule_hash`. Mismatch under the task lock: 409 ErrorResponse, `code=schedule_conflict`, `field=trigger`; no part of that PATCH commits. Exact messages and read/review/reapply flow: §5.5/§6.2. |
| Aggregate occurrence refusal | REST 400 ErrorResponse, `code=occurrence_query_budget_exceeded`, `field=range`, error text “Occurrence query exceeds the request budget. Narrow task_id or the date range.” Tool uses the existing error result with the same text. No returned sets, continuation token or success-shaped partial result (§5.6). |
| Read/explicit-run errors | A failed history read is an error, not zero runs. A future occurrence request is refused. Agent/tool refusals use the existing error/permission/delegation families, not a new failure discriminator. |
| UI truth | No success badge before server confirmation; duplicate pending activation disabled; zero raw rule/cron strings in scheduling controls or approvals; recorded-run status has precedence over availability. |
| Runtime envelope | Existing standalone application on Linux/macOS/Windows; no new runtime dependency or service. Preserve the root security-feature overhead ceiling of less than 10 MB beyond baseline; do not claim it measured until implementation evidence exists. |

No new response-latency service-level target or workload lifetime deadline is commissioned. Bound work by the stated query/validation limits; do not invent a timeout that stops admitted agent work. [S1, S3, S8, S22]

## 4. Available reference patterns and current codebase context

### 4.1 Available reference patterns

The requested Go pattern index `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/reference/go-implementation/00-overview.md` **does not exist in this checkout**. The reference-root positive control `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/reference/built-in-tools.md` exists. Do not invent patterns from the missing library or import database infrastructure. Reuse the verified local patterns instead. [E-CONTEXT]

| Existing pattern | Reuse / adaptation | Evidence |
|---|---|---|
| Per-task striped locking and atomic file publication | Preserve current storage; extend the same mutation/admission discipline for suspension. | [S2, S3] |
| Paired task-goal create and cleanup | Reuse validation, normalized criterion identities and goal ownership, **not** the current task-first publication ordering. Preallocate/validate task identity, write its defining goal first, then publish the complete scheduled task last; orphan-goal cleanup cannot make incomplete work discoverable (§5.1.1). | [S6, S26] |
| Server-owned occurrence expansion | Share it between REST, tools and scheduler; do not add client-side occurrence calculation. | [S3, S8, S9] |
| Per-tool readable approval registry | Add a schedule preview, backed by a server-frozen generated contract rather than model prose. | [S10–S12] |
| L1 role seed and custom-worker restriction | Extend new-worker creation data, not existing entities or the resolver; Q1 B forbids a worker upgrade rollout. | [S14, S15] |

### 4.2 Existing codebase context and impact limitation

GitNexus MCP tools are not connected in this session. Checkout-specific CLI status reports **Repository not indexed**; its explicit checkout query fails. No graph processes, cluster names or graph risk score are claimed. Caller/writer sweeps and first-hand reads provide the following **Inferred (medium confidence)** impact assessment. This document edits no production symbols; implementers must rerun graph impact when available, or repeat these controlled sweeps before editing. [E-CONTEXT]

| Symbols involved / intended change | Direct consumers that must be updated or tested | Wider flows / risk (inferred) |
|---|---|---|
| `Trigger`, `ValidateTrigger`, `Store.updateLocked`, `Store.SpawnReset` [S2, S3] — extend state and conditional mutation | REST trigger mappers, scheduler reset, run resets and task mutation tools | High cross-tree coupling: every writer must preserve the flag; no execution enum change. |
| `OnTaskUpserted`, `RunScheduled`, `rearmRrule`, `Reconcile`, `RunRecoverySweep` [S4] — extend every arm/admission path | Cron callbacks, gateway notifications, boot/recovery | High: stale callback and lock-order correctness; a single top-of-callback check is insufficient. |
| `TaskExecutor.executeTask`, `SpawnTriggeredRun`, `CheckQueuedTasks`, `StartOccurrenceRun` [S5] — trusted new-run admission and agent-authorized explicit adapter | Queue/dependency/follow-up starts, scheduled starts, agent Run now | High: preserve plan/delegation/depth, attended/unattended context, exactly-once admission and existing task/session mode. |
| Task create/update/list/run tools and `registerTaskAndPlanTools` / `wirePlanToolsForAgent` [S6, S7, S13] | Creation cleanup, ownership checks, live registration, reload and late binding | Catalog presence alone is not live execution proof. |
| `buildOccurrenceSets`, `buildOneOccurrenceSet`, `toWireTaskRun` [S8] — extract narrow shared query responsibility | REST occurrences/runs, agent schedule/history reads | Preserve limits and run projection; extraction must report unavailable instead of silently manufacturing empty history. |
| `resolveAskPolicy`, `requestAskApproval`, `CheckGrantOrRequestApproval` [S10] — prepare before all grant checks | Pending transcript, grant fingerprints, tool execution recheck | High: freeze defaults once and bind the exact approved proposal to execution. |
| `broadcastToolApprovalRequired`, `emitSessionState`, `TOOL_APPROVAL_PREVIEWS`, `reconcileWithSessionState` [S11, S12] | Generated WS consumers, modal/store, reconnect | Exact proposal must survive reconnect; no live-task reconstruction. |
| `CalendarEventSlideOver`, `TaskDetailPanel`, `mapToCalendarEvents`, `resolveRunForOccurrence`, tasks API/query keys [S9, S12, S18] | Calendar chips/groups, both detail controls, task/occurrence/history caches | High UI state coupling: preserve unsaved edits, recorded runs and Calendar-only filtering. |
| Role seeds, global shipped policy, Auto classification and creation/preview routes [S13–S15] | Both agent creation routes and tools-default preview | Seed changes must be coordinated with L1; preserve existing worker maps/operator values, add Omnipus to the supported scheduling roles and keep two-layer resolution unchanged. |

Manual flow map, **not GitNexus process names**:

```text
Agent ToolSearch -> policy-filtered live schedule tool
                    -> prepare canonical proposal -> grant / approval
                    -> recheck state + authority -> shared task/goal mutation
Person detail control ----------------------------> shared suspension mutation
                                                      |
                                              persisted task (authority)
                                                      |
                                      derived cron job -> fenced admission
                                                      |
                                  existing task executor -> actual run history
                                                      |
REST / agent shared query -> generated contracts -> Calendar / task detail
Approval registry         -> live + reconnect    -> exact readable card
```

Functional areas are task persistence, agent execution, tools/policy, gateway/contracts, Calendar/task-detail and approval UI. Keep query code below gateway/tools/agent to avoid import cycles. Exact transitive callers are **Unknown**, not a fabricated graph result. [S1, E-CONTEXT]

## 5. Normative backend and contract design

This section and subsequent technical sections are outside the behavioural-phase guardrail. Proposed schema/test/package names below are **planned**, not claims that those artifacts exist. Backend-lead alone edits and regenerates contracts; this spec author changes neither contracts nor production code. [S1, S16, S22]

### 5.0 Contract inventory — before mechanism design

Backend-lead defines/regenerates these named OpenAPI/AsyncAPI components **before Go/TypeScript consumers**. Exact shapes and statuses are in §5.5, not delegated to frontend implementation. All paths here are API routes, not filesystem paths. [S16, S22, S28]

| Boundary | Contract inventory / change |
|---|---|
| Existing task CRUD/read-back | TaskTrigger response flag; new TaskTriggerInput request; TaskUpdateRequest.expected_schedule_hash; read-only Task.schedule using TaskScheduleState, including activation/warnings and the opaque baseline. Task remains the response entity. |
| Narrow state controls | POST `/tasks/{id}/trigger/suspend` and `/tasks/{id}/trigger/resume`: 200 Task with schedule state, including saved-but-unavailable; existing generated ErrorResponse for explicit 4xx/5xx outcomes. |
| Agent schedule tool | TaskScheduleInput, ScheduleTaskCreateInput, ScheduleTaskArguments, TaskScheduleState, ScheduleTaskResult; private authority and retry receipts never become writable wire fields. |
| Reads / resource refusal | TaskOccurrenceSet.suspended; AgentTaskListRow/Result, AgentTaskOccurrencesResult, AgentTaskRunsResult; REST/tool shared aggregate-budget refusal through existing error contracts. |
| Exact approval / reconnect | TaskScheduleApprovalPreview; optional typed schedule_preview on ToolApprovalRequiredFrame and SessionStatePendingApproval, including shared-schema mirrors. |
| Existing run paths | Preserve RunNowRequest, TaskRun, DayBucket and existing run/status/frame families. Only the agent run tool gains optional occurrence_ms. |

### 5.1 One state owner, one admission order

Add optional persisted `Trigger.Suspended bool` with `suspended,omitempty`; absent means false. `Task.status`, `TaskRun.status`, `IsRepeating`, `SeriesRetired`, results, goal state, session ownership and execution mode retain their meanings. Manual + true is invalid. Fully validate a suspended trigger and its assignee. No bulk task rewrite is needed; write the flag on mutation. [S1–S4]

| Operation | Normative persistence/execution semantics |
|---|---|
| Suspend | Only user-surface once/recurring. Set true and disarm derived jobs; invalidate pending old callbacks. Already true is a no-op. |
| Resume | Set false without moving anchor/rule/zone/status; apply AUTH-1. Recurring next fire is strictly after server now; once arms only if `at_ms > now`. Already false with unchanged AUTH-1 commitment is a persistence no-op; reconcile a missing/unavailable derived job on retry, never duplicate an existing valid job. |
| Set/replace | Preserve suspension under the same task lock, including recurring→once and moving an existing once. Explicit future one-time edits remain eligible even if the last-run mirror is Done/Failed; narrow the terminal-once scheduler exclusion for this deliberate scheduling change, not the status vocabulary. |
| Clear | In one locked write install canonical `{type:"manual",config:{}}`, remove suspension/timing but retain the stored `Task.Initiator` bytes, including a retry carry-forward, and move an idle `next` or `blocked` task **without an admitted continuation** to `inbox`; retain its dependencies. Inbox does not auto-unblock to Next. Other states and valid admitted continuations keep their execution truth. Already manual is a no-op. If that same body also sets `status` to `next`, `in_progress`, `done` or `failed`, the whole PATCH is 400 and writes nothing — `next` is included, so the inbox rule cannot be undone in the same write. No separate existing terminal path is cited for `done` or `failed` inside this body, so those two are refused too. Omitted `status` uses the Clear transition. Explicit `inbox` is the same transition. A request still cannot set `blocked` (existing rule). The caller must separately choose Run now or mark Next. |
| Other writers | Title/assignment/progress/checklist/results/completion/run reset/retry and ordinary trigger replacement cannot implicitly Resume. Assignment still passes current validation. |
| Run now | Uses ordinary run permission/ownership/dependency/plan/overlap gates. Suspension stays true. It does not move the rule or consume a future planned position. |

Use the existing per-task lock to serialize suspension with the **final automatic-admission decision**. When nested, order **task lock → scheduler lock → cron-engine lock**. Never acquire them in reverse or hold them across execution launch, event publication, approval waits or callbacks. [S1, S3, S4]

A callback must match the **currently tracked job ID and trigger generation**, then pass the conditional locked reset/admission before any task-status mutation, run, session or executor launch. Trigger content hash alone cannot distinguish a callback from before Suspend→Resume when identical content returns. Every arm/re-arm/boot/recovery/upsert re-reads current persisted state; a delayed task snapshot is not authority. Reject stale/suspended callbacks without synthetic skipped records; keep existing real-overlap recording and unreadable-task diagnostics. Persistence is authoritative even if derived job removal/arming fails. Return/report that failure honestly; the persisted admission fence must still prevent forbidden starts. [S1, S4]

Close alternate new-run entries at the shared admission boundary **and** queue prefilter using the same classification below, rechecked under the task lock. `next`, a nil `occurrence_ms` (normal for once), a run kind or an old history row is never admission proof. Scheduled creation uses §5.1.1; never create a dispatchable manual task and patch timing later. [S1, S5, S6, S27]

| Trusted dispatch class | Positive admission rule |
|---|---|
| New automatic timed fire | Requires the current job-ID/generation context from `SpawnTriggeredRun`, the current persisted trigger and AUTH-1's stored-`Task.Initiator` check. Suspension or staleness refuses before reset/claim. Capacity (`ErrDispatchCapReached`), an unmet `BlockedBy`, or the plan gate (`requirePlanExecuting`) are pre-claim refusals: do not keep `Store.SpawnReset`'s status, result, artifact, session or timestamp writes unless `ClaimForRun` then succeeds for that same fire. Overlap (`ErrAlreadyRunning`) is not this class: `SpawnReset` already writes nothing, and its existing skip record stays. Queue, dependency-unblock and follow-up cannot mint this context and cannot start the refused fire. FQ-3 splits the refusal by schedule kind, below. |
| Continuation of admitted attempts | Requires the executor-owned pending-attempt receipt defined below, current matching execution evidence and remaining attempt budget. Queue pickup is **allowed even while suspended or after Clear**. This class exists only after an admitted attempt has failed. It is not the recovery path for a fire that never reached `ClaimForRun`. |
| Explicit new run | Existing user/agent-authorized explicit-run adapter, ordinary plan/dependency/overlap/policy gates. It supersedes any older pending-attempt receipt atomically; it does not change the stored `Task.Initiator` by itself. A later failed explicit run still follows today's `consumeTaskAttempt` carry-forward. |
| Ordinary manual new work | Existing manual queue/plan rules. A timed task without either of the first two proofs refuses; a manual task left Inbox by Clear is not eligible. A one-time task whose fire was refused before claim is not eligible here either: its file was not left `next`, and it has no receipt. |

**Pre-claim refusal — FQ-3 A, silent retry. Source: Interview, 2026-10-15.** `TaskTriggerScheduler.RunScheduled` calls `Store.SpawnReset` before dispatch. `SpawnReset` would set status to `next`, clear result and session fields, then `recomputeBlockedStateLocked` may leave the task `blocked`. `executeTask` returns on status not `next`, `requirePlanExecuting`, an unmet `BlockedBy`, or `ErrDispatchCapReached` **before** `ClaimForRun`. For a non-repeating trigger, `RunScheduled` returns that error. Once jobs are `at` jobs (`triggerToCronSchedule`) created with `DeleteAfterRun` when `Schedule.Kind == "at"`. `CronService.scheduleNextRunUnsafe` removes an `at` job with `DeleteAfterRun` **before** the transient-retry branch, on the error path as well as the success path. `IsTransient` is true only for `*cron.TransientError`. `RunRecoverySweep` continues past any trigger that is not a recurring rule, so it will not put the once job back. The normative repair is: [S4, S5, S31]

| Schedule kind | Pre-claim refusal rule (capacity, unmet dependency, or plan gate) |
|---|---|
| One-time | Do not keep `SpawnReset`'s writes unless `ClaimForRun` succeeds. Do not remove that `at` job. Return the refusal wrapped so `cron.IsTransient` is true. `scheduleNextRunUnsafe` must schedule that retry **before** any `at`+`DeleteAfterRun` removal, and this class must not take the removal branch. The shipped backoff is `defaultRetryBackoffMs` = 60000, 120000, 300000 ms and then normal cadence; a past `at` job does not fire again on normal cadence. For this class only, when that finite list would exhaust, keep the same job id and schedule the last offset again until `ClaimForRun` succeeds. Do not delete or disable the job on exhaustion. After claim succeeds, the existing success path may delete the one-time job. Exactly one claim. No warning string, chip, or skipped `TaskRun`. `scheduler_state` stays `armed` while the job remains; `next_fire_ms` is that job's actual retry instant, not null and not a missed-run marker. Dependency completion and a freed worker slot do not start the task through the queue. The same job's next retry is the only starter. |
| Repeating | Do not keep `SpawnReset`'s writes. Re-arm only the next planned occurrence through the existing `rearmRrule` path, and return success for that occurrence job so it is not also left behind as a second starter. Do not leave status `next` or `blocked` for the queue. Do not record a skipped-history row for this refusal. Do not apply the one-time indefinite retry to this occurrence job. When a slot later frees, the refused occurrence does not also run. The next planned occurrence is the one continuation. |

BDD-64's oracle is the task file and the job list after the barrier, then exactly one claim. BDD-57 starts only after a receipt exists and does not cover this. [S31]

**Admitted-continuation evidence:** extend the existing task envelope with an internal optional `pending_attempt` receipt, written **only by `consumeTaskAttempt` in the same locked update as its Next/blocked transition, attempt increment, and today's `Task.Initiator` carry-forward**. That carry-forward stays: when `retryAuthority` returns non-nil, `Patch.Initiator` is set from it, including `Direct: true`; when it returns nil, `Task.Initiator` is not overwritten. FQ-2 forbids moving that write onto the receipt. The receipt is not a creator record and not a replacement authority field. It binds source session/generation, task/workspace/assignee, attempt index, occurrence key/run kind and attended/unattended posture so admission can tell this continuation from a never-admitted twin. These are server-owned continuation facts, never tool/PATCH inputs or public Task fields; no new entity, history ledger or scheduler. Why the receipt remains: round-1 BDD-57 needs a proof the queue can see, and CRIT-001 cannot use it because no attempt has been admitted yet. An additive TaskRun history write may be unavailable and is **not required** as the sole proof of admitted execution. [S27, S31]

Both prefilter and final locked claim validate receipt/task/session/generation/attempt agreement, genuine task-origin lifecycle identity, no winning Stop/supersession and remaining attempts. Re-authorize through the existing path against the authority today's carry-forward stored; do not silently promote missing/unreadable authority to human, and do not reconstruct a creator the file no longer has. Capacity refusal of an **already admitted** attempt leaves the receipt intact for the later queue; successful retry claim consumes it exactly once. A subsequent failed admitted attempt may mint its next receipt. Stop, explicit fresh restart, final completion/exhaustion or changed captured assignment invalidates the old receipt visibly; concurrent queue/direct pickups cannot consume it twice. Unrelated schedule edits do not rewrite it and do not clear `Task.Initiator`. No legacy receipt is inferred from mirror status/count/nil occurrence alone: unreadable or unprovable continuation gets a visible refusal and explicit Run-now recovery. Preserve ordinary manual retry behaviour and the named existing queue-pickup regression in §9.3, **including** its assertion that a failed run's non-nil authority is persisted on `Task.Initiator`. [S5, S27, S31]

### 5.1.1 Scheduled creation publication — goal first, complete task last

**Invariant:** no task discoverable/admissible by direct lookup, queue, notification, boot reconciliation or recovery may be an incompletely created scheduled task. Notification timing is not the fence. For **both** agent scheduled-create and REST timed-create, preallocate a fresh validated task ID, normalize/default/validate the full task and completion contract once (preserving criterion IDs and agent attribution), and construct its paired **defining**, not active, goal before publishing anything schedulable. [S6, S26]

| Cut point under canonical goal → task creation locks | Discovery / failure / restart outcome |
|---|---|
| Before goal publication | No final task or goal exists; validation failure writes neither. |
| Goal published; final task absent | Only an unreferenced defining goal may exist. Every task discovery/admission path sees no task; goal-only recovery must not start work or manufacture its missing owner. |
| Atomic final-task publication | Publish the fully normalized Inbox task **with trigger, paired-goal identity/criteria agreement and the `Task.Initiator` today's create path already writes**. This final task-file publication is the sole eligibility cut point, not a callback. Do not add a creator field. |
| After final task publication, before notification/registration | Creation is complete. Boot/recovery may discover it and arm according to persisted state; the response/recovery reports saved identity and activation separately. |
| Failure before final-task publication, including failed rollback | Remove the orphan task-owned goal using existing goal cleanup. Cleanup failure is reported with allocated identity, never hidden; there is still no discoverable task to arm. Extend existing boot/recovery cleanup for task-owned orphan defining goals under the same canonical goal → task locks as creation, so it cannot race an in-progress create. Do not remove a goal whose owner task exists. |

Factor validation/preparation from current `Store.Create`/`CreateByAgent` so goal and task share **the same** normalized criterion IDs; do not call a task-first publishing helper and hope to delete it later or introduce a second task store/transaction service. The existing goal-store contract fixes goalLock → taskFileLock → sessionLock → cacheMu; the top-of-document correction records why the initial inverse draft was discarded. Preallocate the goal ID as well as task ID and expose a narrow goal-store coordinated, lock-held operation for goal-first write plus final task publication. Creation and orphan cleanup acquire **goal lock → task lock**, with internal non-reentrant operations (never call a public lock-taking Create/Delete under its own lock); cleanup checks owner absence under those locks before deleting. No session/cache/scheduler lock is needed in this publication section. Release all before notification, launch or audit. The lock-observer regression must prove this order; a race-detector green alone cannot. [S26] If an I/O result is ambiguous around final publication, inspect the final task and paired goal: complete publication is saved (activation may be unavailable); inability to establish it is an error/unknown outcome naming the reserved ID, never blind-create retry or deletion of the goal for a visible task. Crash/restart tests must inspect real stores at **every** cut point, including failed cleanup, recovery while creation is held, and successful post-publication recovery without notification. [S3, S6, S26]

COUNT counts planned positions, including suspended elapsed ones. UNTIL retains its original instant. Resume never back-fills, starts an already elapsed once, resets count or extends the end. Actual rule/time/end edits retain Calendar's existing re-anchor/count-restart semantics; suspension changes alone never do. Exhaustion is a successful saved state with no future fire, not a failed Resume. [S1, S3, S9]

### 5.2 Agent tool arguments and results

Add general-scope, Tasks-category `schedule_task`; keep discovery through existing ToolSearch and do not expand the upfront-tool set. Call shared domain operations, never authenticated operator REST handlers. [S1, S13]

| Action | Required | Forbidden action fields | Effect |
|---|---|---|---|
| `create` | `task`, `schedule` | `task_id`, `expected_schedule_hash` | Create one complete local task/paired goal/trigger, initially Inbox; no immediate run. |
| `set` | `task_id`, `schedule` | `task` | Add/replace timing and preserve suspension. |
| `clear` | `task_id` | `task`, `schedule` | Install manual. |
| `suspend` | `task_id` | `task`, `schedule` | Suspend future automatic admission. |
| `resume` | `task_id` | `task`, `schedule` | Resume future eligible scheduling without replay. |

All actions accept optional `workspace_id` as a same-current-workspace assertion. Existing-task actions accept optional `expected_schedule_hash`; server preparation injects it when absent. Reject unknown/contradictory keys and wrong types at every structured nesting level. No free-text reason or raw recurrence-rule escape hatch. `create_task` / `update_task` retain their unscheduled shape and explicitly refuse supplied scheduling keys with **“Use schedule_task to change a schedule”**. [S1]

`task` requires `title`, `prompt`, `agent_id`, nonempty `criteria` and a distinct nonempty `dod`. Optional fields are `description`, `priority`, `tags`, `todos`, `due`, `blocked_by`, `run_isolated`, using existing validation and defaults. Do not add plan authoring, arbitrary initial status, owner/provenance or execution-source arguments. Preserve existing delegation/depth/readiness checks, agent-namespaced creation attribution, `Initiator`, inherited budget and create-failure cleanup. An agent-created scheduled task must not become operator-created work. [S1, S5, S6]

| `TaskScheduleInput` proposed shape | Validation / compilation |
|---|---|
| `kind: once`, `at` | RFC3339 timestamp with explicit offset; absolute instant, no stored recurring zone. Other recurrence fields refuse. |
| `kind: recurring`, `start_at?`, `tz?`, `frequency`, `interval`, `weekdays?`, `monthly?`, `end` | `start_at`/`tz` required for create or new recurrence. Existing recurring set can omit anchor: freeze preparation time as the new anchor, preserve stored zone. A supplied different zone refuses; supplied offset must agree at that instant. |
| `frequency` | `minutely`, `hourly`, `daily`, `weekly`, `monthly`, `yearly`. Integer interval 1–99; no silent clamp at the agent boundary. |
| `weekdays` | Weekly only, nonempty unique codes `MO TU WE TH FR SA SU`; canonical weekday order. |
| `monthly` | Monthly only: `{mode:day_of_month,day:1..31}` or `{mode:nth_weekday,ordinal:1..5 or -1,weekday:weekday-code}`. Exactly one mode. |
| `end` | `{kind:never}`, `{kind:on_date,date:YYYY-MM-DD}`, or `{kind:after_count,count:1..100000}`. No unrelated fields. |

Compiler parity is normative: weekly BYDAY; month days 29–31 use the existing 28..day / last-position clamp; nth weekday uses BYDAY/BYSETPOS; end date is 23:59:59 in the series zone, then UTC; count becomes COUNT. Other frequencies rely on the anchor as the editor does. Run the existing `ValidateTrigger` afterwards; no second occurrence engine or new runtime dependency. Preserve server DST policy: nonexistent local time moves by the gap, repeated local time takes its first occurrence, duplicate instants collapse. [S1, S3, S9, S21]

Generated `ScheduleTaskResult` contains `task_id`, `workspace_id`, `agent_id`, task `status`, canonical `trigger`, `schedule_text`, opaque `schedule_hash`, at most three `next_occurrences_ms`, nullable `next_fire_ms`, `persistence:"saved"`, `scheduler_state:"armed|disarmed|exhausted|unavailable"`, `warnings[]`. Planned positions may remain while suspended; **next fire is null** for suspended/manual/expired/exhausted/unavailable state. Arrays are arrays, including empty arrays. [S1]

`schedule_hash` binds canonical trigger (including suspension), assignee, workspace and the stored `Task.Initiator` identity (nil/human, or the agent id); consumers treat it as opaque, not authority and not a client-computed token. It excludes depth, budget, derived activation, warnings and read times so read-back and registration retries cannot manufacture conflicts. Distinguish it from job identity/generation. Storage failure before commit is an error; ambiguous publication follows §5.1.1. If storage succeeded but activation failed, return saved identity, unavailable state and warning, not false scheduling success or duplicate-create advice. The coordinator must return an observable activation outcome: today's void notification is not proof of arming. All reads and HTTP mutations use the same state definition in §5.5. [S1, S4, S28]

### 5.3 Authority and exact approval

Resolve the real current/default workspace through the existing task-workspace path. Refuse foreign create/arm/mutation before writes; guide the caller to delivery-only `create_task` and local receiving-team scheduling. Existing-task ownership is **assignee OR nonempty agent-namespaced `CreatedByAgent`**, never mixed human/agent `Owner` or `CreatedBy`. Check the mutation caller's current assignee/workspace delegation authority for arming; clear/suspend grant no execution privilege. **A later timed run follows AUTH-1.** It is a normal automatic run of the assigned agent under the stored `Task.Initiator`, not a reconstructed creator. [S1, S5–S7, S21, S25, S27, S31]

#### AUTH-1 — Stored task authority for a timed run — founder FQ-2

**Source: Interview, 2026-10-15.** FQ-2 supersedes FQ-1 A's creator reading. The founder asked why the creator matters: the agent the task is assigned to does the work. A timed run is a normal automatic run of that assigned agent. Do not track or describe an original creator. Keep today's stored `Task.Initiator` exactly as the existing code keeps it, including today's retry carry-forward. No new field, no history reconstruction, no migration. [S31]

`TaskExecutor.automaticInitiator` already runs a non-plan task under `Task.Initiator` (nil when no agent is stored) and re-authorizes a non-nil value against the current graph, with the existing self-exemption when that initiator is the assignee and `Direct` is false. `consumeTaskAttempt` sets `Patch.Initiator` from `retryAuthority` when that value is non-nil, in the same write that sets `next`; `retryAuthority` sets `Direct: true`. A nil result does not overwrite the field. `TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup` requires that persisted carry-forward. This feature does not move that write. Unreadable or malformed stored authority still refuses visibly rather than being replaced with nil. Do not change the existing self-exemption or depth model. [S5, S27, S31]

| Operation / case | Required authority behaviour |
|---|---|
| Agent scheduled create | Preserve today's agent-create `Task.Initiator` write, depth and inherited budget. The task is not turned into operator-created work. No second creator field. |
| Agent Set/Resume, active or suspended | Check the caller's scheduling permission and ownership now, including after approval. Do not replace the stored `Task.Initiator`. The later timed fire uses those stored bytes, not the scheduling caller. Removing that caller's `schedule_task` permission afterwards leaves the schedule in effect. |
| Human timed-create / human Set/Resume | Human creation stores the same nil-or-agent `Initiator` today's create path stores. Human timing controls do not replace a stored `Initiator` with a bypass. |
| Repeated Set/Resume / another scheduling actor | Equal canonical schedule/state is a persistence no-op regardless of which authorized actor requested it; `Task.Initiator` is not rewritten. Rule-only edits with a newly prepared anchor are still real edits. Already-active Resume can reconcile unavailable activation without changing the anchor or the stored initiator. |
| Authorized assignment change | Keep today's assignment transition. A scheduling-only Set/Resume is not a reassignment and does not rewrite `Task.Initiator`. The later timed fire consults the stored initiator and the assigned agent's current membership/delegation. |
| Suspend/Clear and unrelated writes | Retain the stored `Task.Initiator`, including `Direct`. Clear removes timing and suspension, not those bytes. Admitted-attempt receipts stay independent admission proof (§5.1). |
| Explicit Run now / failure / retry / completion | Keep today's `consumeTaskAttempt` carry-forward on `Task.Initiator`. Do not move it onto `pending_attempt`. The receipt still proves which continuation may be queued (§5.1). **Honest consequence:** a task last retried by a person runs on its timer with that stored authority, exactly as a retry does today — a nil `retryAuthority` does not overwrite `Task.Initiator`, so the bytes already on the file are what the timer uses. |
| Pre-upgrade file with `Initiator.Direct == true` | That value was written by today's failed-run path. The next timed fire uses it. Do not reconstruct a creator the file no longer has. BDD-55's fresh nil row does not cover this file. |
| Later permission removal | Removing the **scheduling caller's schedule-tool permission** does not stop automatic fires. The assigned agent's current membership/delegation check can still refuse the next timed fire. Ordinary dependency, overlap and end conditions remain. Suspension or removing timing is how someone stops future automatic starts. |

The opaque schedule hash includes the stored initiator identity (nil/human or agent id) so a real assignment change can invalidate a pending proposal. It is not a readable principal and it does not include depth or budget. A readable notice is mandatory for **create/set/resume** in the generated preview and registered card (§5.5/§6.4), live and after reconnect, before the decision controls. The exact text is: **“This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.”** Show it beside normal end/exhaustion and execution-gate caveats. This is not a promise to ignore the schedule's end or the assigned agent's current checks. Do not say who created the task. Missing or altered text blocks Approve / Always Allow. BDD-55, BDD-63 and DOC-01/02/03 verify this wording, not a creator sentence.

For **Set/replace whose saved result stays suspended**, including recurring→once, the same frozen preview and registered card must also include, before the decision controls, live and after reconnect: **“Automatic firing stays off. This change does not resume the schedule.”** Missing or altered text blocks Approve / Always Allow the same way. An unsuspended Set must not show that sentence. Count-restart text stays and is not a substitute. BDD-65. [S31]

#### Exact preparation and approval

Preparation must run for effective Allow as well as Ask. Resolve caller/target/ownership, canonical before/after schedule and anchor **before every exact-grant lookup, approval request, grant recording and pending transcript write**. Normalize the complete executable proposal into the argument map, including resolved anchor/zone and the pinned existing-task hash. Use that same map everywhere; hidden time-dependent defaults cannot be the basis of an exact grant. Freeze the compiled proposal in call context, including create. Do not hold task locks while awaiting approval. [S1, S10]

After approval, recheck policy, authority and pinned current state under the task lock. A mismatch has zero write/dispatch and requires reading/preparing/approving the new proposal. Preserve exact-call Always Allow semantics, but changes to target, schedule or anchor cannot match an earlier grant. Validation/concurrency control is not a third policy layer. [S1, S10, S14]

The server-authored `TaskScheduleApprovalPreview` carries action, task identity/title (existing ID nullable for create), resolved workspace and assignee, before/after trigger, exact readable schedule text and applicable warnings. Creation additionally carries its **whole** proposed task payload, including instructions, criteria/DoD and every optional field supplied. Bind preview and execution to the same prepared proposal. No model-authored summary is trusted as the commitment. [S1]

Thread the stored typed preview through pending registry/approver adapters, live approval frame, pending transcript representation and reconnect snapshot/store/renderer. For scheduling, a valid reconnect preview is enough to render the exact decision even where the generic reconnect entry lacks tool-call/turn argument detail; it must not remain a deny-only bare-tool stub solely because those generic fields were not supplied. Missing/malformed scheduling preview disables Approve/Always Allow, retains Deny/Dismiss as applicable, and explains how to request a new proposal. Never fetch current task state and pretend it was approved. Preserve existing Cancel, expiry, resolved-ID deduplication, acting-session/audience and grant scope semantics. [S1, S10–S12]

### 5.3.1 Semantic schedule audit — existing logger, no history ledger

When existing auditing is enabled, the shared mutation path emits **one semantic entry per create/set/clear/suspend/resume operation** through the existing audit logger for both tool and REST/UI front doors. Register event names `task.schedule.create`, `task.schedule.set`, `task.schedule.clear`, `task.schedule.suspend`, `task.schedule.resume` in the existing event inventory. Retain generic tool/task auditing; it is not a substitute for this committed-transition record. [S29]

| Audit case | Required metadata / outcome |
|---|---|
| Committed mutation | Authenticated actor kind/ID (who edited), source tool or REST, task/workspace ID, action, before/after canonical trigger + opaque schedule hashes, `outcome=changed` and observed scheduler_state. Create has null before trigger identity; Clear has canonical manual after identity. On **create, set and resume only**, also record a non-secret fire principal taken from the stored `Task.Initiator` at commit: `fire_principal=human` when that value is nil, otherwise `fire_principal` is that agent id, plus `retry_carry_forward=true` only when the stored initiator is non-nil and `Direct` is true (today's `retryAuthority` marker). Do not record depth, budget, prompt or criteria. The schedule hash is not this principal: it is opaque and cannot be read back as an agent id or as “human”. Capture authoritative before/after under the mutation lock, emit after release; never re-read a later task as this operation's result. |
| Matching no-op | Emit `outcome=noop`, equal before/after identities, no duplicate mutation/job/run. A different authorized scheduling caller alone does not turn a matching-state no-op into a mutation and does not rewrite `Task.Initiator`. Activation-only recovery records its actual scheduler_state without inventing a rule edit. Create/set/resume no-ops still record the fire principal above. |
| Saved but unavailable | `outcome=saved_unavailable`, real committed identities and unavailable state, never “failed/nothing saved.” |
| Refused or failed before commit | Existing deny/error decision conventions; `outcome=refused` or `failed`, no invented after-commit identity. Do not read/disclose an unauthorized target just to enrich the audit. |
| Audit write failure / disabled auditor | Preserve existing best-effort convention: log the audit failure through the existing error path, do not roll back a committed schedule or turn it into a storage-failure response. Disabled auditing remains disabled. Tests must inspect the failure log and committed state, not assert an audit row that could not be written. |

Use existing `Entry` actor/details and redaction conventions. Do not copy title/instructions, criteria/DoD, private initiator budgets or whole task objects into these entries. Hashes stay the precondition record; the fire-principal label is the forensic record of who the next fire will run as. BDD-62/T36 exercise both real front doors, the principal label including a `Direct: true` carry-forward, no-op, refusal/storage/registration failures and an audit-sink write failure. This is not a persisted suspension-history ledger and is not an occurrence-rendering data source. [S29, S31]

### 5.4 Registration and policy — Reachability prerequisite

All rows land together. Explicit inventory classification is required for each role; it does **not** mean backfilling a dense per-agent map. Mia, Jim and Omnipus have no per-agent `schedule_task` entry and ride the global Ask ceiling. A **new custom Main** does not omit the key: `NewCustomAgentToolsCfg` stores explicit Ask, because `denyAllThenOverride` writes Deny for every name in `allStaticToolNames` and then applies only the override map. `buildToolConfig` copies that map and Deny-fills any name still missing from `buildKnownBuiltinToolNames`, so an omitted key cannot survive. New-worker Deny is stored by `RestrictForWorkerTier` after that Ask seed, never inferred at resolution. Existing workers missing this key inherit global Ask without backfill (Q1 B). Omnipus rides the ceiling with no per-agent scheduling entry (Q2 YES). Source: Interview, 2026-10-15. Do not change `denyAllThenOverride`, the final Deny-fill loop, or `resolveEffectivePolicyWith`. [S1, S13–S15, S31]

| Path / symbol | Required implementation |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/coreagent/seed.go`::allStaticToolNames / NewCustomAgentToolsCfg / RestrictForWorkerTier / workerTierDeniedTools | Add literal `schedule_task` to `allStaticToolNames`. In `NewCustomAgentToolsCfg`'s override map, set `schedule_task` to explicit Ask. Do not omit it. Add `schedule_task` to `workerTierDeniedTools` so `RestrictForWorkerTier` stores Deny for a new worker after the Ask seed and before the caller override, in both creation routes. Assert the constructor map contains Ask, and the map after `RestrictForWorkerTier` contains Deny. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/coreagent/role_policies_adr090.go`::ADR090RolePolicyInventory / adr090SparseRolePolicies | Grant Ask for Mia and Jim so the sparse seed omits the key (inventory Ask equals the Ask ceiling, and the key is not on the deliberate-posture list that forces a stored row). Leave Worker, Planner and Researcher at the inventory's default Deny so their sparse seed retains Deny. Do not backfill existing worker maps (Q1 B). Add schedule_task to L1's omnipusSupportedTools, with no per-agent entry (Q2 YES); other hidden system role seeds remain closed. Assert a Mia sparse seed does not contain the key. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/config/defaults.go`::defaultToolPoliciesGeneral | Literal global `schedule_task: ask`. Existing ceiling reconciliation supplies this missing key on old installs and preserves operator values. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/general_builtin_catalog.go`::GeneralBuiltinMetadata | Nil-dependency metadata instance with real name/scope/category/schema. It feeds both central-catalog passes; do not invent another central static list. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/gateway_sandbox.go`::buildKnownBuiltinToolNames | Explicit forward-registration union entry following L1; preserve catalog equality test and independently prove live implementation/discovery. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/loop_wire.go`::registerTaskAndPlanTools / wirePlanToolsForAgent | Real native-agent instance with stores, authorizer, coordinator and preparation hook at boot/reload/late wiring. Do not rely on metadata-only presence or omit registration by role. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/auto_approve.go`::autoApproveClasses | Explicit `schedule_task: AutoAsks`, all actions included. Effective Ask is never auto-approved merely because Auto is on. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_agents_create.go`::buildToolConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/sysagent/tools/agent.go`::prepareConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/sysagent/tools/agent_read.go`::AgentGetToolsTool.Execute | Apply `RestrictForWorkerTier` to the base **before** submitted overrides in both creation routes and the default preview. The constructor's explicit Ask is already present, so `buildToolConfig`'s final missing-name Deny-fill does not have to be changed and must not be taught to preserve an absent key. Preserve operator opt-in/out. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/autoApproveAskGroups.ts`::AUTO_APPROVE_ASK_GROUPS; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/humanizeToolName.ts`::EXPLICIT_LABELS | Ask group **Changing task schedules** and explicit label **Change task schedule**; compare against the actual landed catalog, not a copied fixed total. |

Policy matrix under the confirmed founder decisions (Source: Interview, 2026-10-15):

| Global / per-agent state | Expected result |
|---|---|
| Ask / no entry (Mia, Jim, Omnipus and existing workers missing this key) | Ask; Auto keeps asking; no matching grant in unattended context means immediate existing denial, not an orphaned approval wait. |
| Ask / explicit Ask (new custom Main, from `NewCustomAgentToolsCfg`) | Ask. This is a stored constructor entry, not an omitted key. Strictest-wins with global Ask stays Ask. A new worker does not keep this entry: `RestrictForWorkerTier` replaces it with Deny. |
| Ask / explicit worker Deny (new workers by default, or saved operator choice) | Deny; no discovery/execution, including God Mode. |
| Ask / explicit operator Allow | Still Ask (strictest wins). |
| Allow / no entry or Allow | Allow; preparation/ownership/concurrency still apply. |
| Allow / explicit Ask | Ask; the agent's own Ask is not erased by God Mode. |
| Any / explicit Deny, or global Deny outside God Mode | Deny. |
| God Mode / no entry | Existing global-floor semantics permit execution without an Ask card; no new special override. |

Do not edit `resolveEffectivePolicyWith` or `ReconcileToolPolicyCeiling` to implement this feature. No wildcard for this static tool and no generic per-agent coverage repair. Q1 B explicitly forbids a worker rollout/migration, including a named one-time backfill or marker. Existing-worker missing keys deliberately inherit global Ask; only new creation gets explicit Deny. Q2 YES adds schedule_task alone to the Omnipus supporting set, with no per-agent Ask. Source: Interview, 2026-10-15. External command-line workers' own tools are outside native policy enforcement. [S1, S14, S15]

L1 integration warning already recorded in the input: its candidate Go Ask membership and UI groups disagree (five Go names missing from groups and one retired extra); the copied membership test total is stale. The L1/integration owner must reconcile that set before this feature extends it. Do not silently fix unrelated tools or preserve the stale total in this unit. Candidate details remain pinned evidence, not an assertion about the eventual landing. [S1::L1 integration note, S15]

### 5.5 Exact contract delta — backend-lead owns edits and generation

All schema names marked new below are proposed under the absolute schema directory `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/`. These are contract-first source edits, not handwritten Go/TypeScript interfaces. New argument/preview objects are closed; action/frequency conditionals are also enforced server-side. Keep the current trigger-config growth boundary without silently accepting a suspension input or retired timing keys. Avoid an external-reference discriminated union that creates invalid generated Go accessors. [S1, S16]

| Existing or proposed contract | Delta / exact shape |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskTrigger.yaml` | Optional read-only boolean `suspended`; no schema default that turns optional TS presence into a required property. Server response mappings emit effective true/false; older compatible responses may omit it. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskTriggerInput.yaml` | Request-only `type,config`; no writable suspended. Retarget trigger refs in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskCreateRequest.yaml` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskUpdateRequest.yaml`. POST initially active through complete-publication create; PATCH preserves suspension atomically; manual applies the locked Clear transition (§5.1). Explicit suspension/private authority/receipt input refuses even with optional inbound validation off. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskUpdateRequest.yaml` | Add optional string `expected_schedule_hash` (nonempty opaque SHA-256 hex token, 64 lowercase hex characters). Server and schema enforce: required whenever `trigger` is present, forbidden when absent; null/wrong-type/invalid-format refuses. Compare against current schedule identity under the **same task lock** before applying any of the PATCH; mismatch writes nothing. Metadata-only saves omit trigger/token. No generic entity-version redesign. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/Task.yaml` | Add optional, read-only `schedule: TaskScheduleState`; new server emits it for every user-surface task (manual included) in task list/detail/create/PATCH/state-POST responses. It supplies both the Calendar baseline hash and fresh activation/read-back state. Old compatible Task responses may omit it; never fabricate a token from the trigger. No persisted activation field and no raw private authority/receipt serialization. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/openapi.yaml` | New POST `/tasks/{id}/trigger/suspend` (`suspendTaskTrigger`) / `/tasks/{id}/trigger/resume` (`resumeTaskTrigger`), no body; 200 generated Task **with schedule state**, including saved-but-unavailable, 400 malformed ID/body, 404 missing, 409 no time schedule/heartbeat, 500 pre-commit storage failure, plus existing auth responses. Extend updateTask's 400/409 descriptions for conditional writes and occurrence GET's budget refusal. Public prefix remains `/api/v1`; existing task routes, no schedule entity. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ErrorResponse.yaml` | Reuse existing `error,code,field` properties, not a handwritten error envelope. Document exact `schedule_precondition_required`, `schedule_conflict` and `occurrence_query_budget_exceeded` responses below and in endpoint schemas. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskOccurrenceSet.yaml` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/openapi.yaml`::listTaskOccurrences | Optional current-state `suspended`; keep planned recurring expansion, arrays/overlays/per-task caps. Add optional validated `task_id` query parameter to existing occurrence GET (not present at baseline), constrained by the same authorized workspace selection; default remains all selected series. Narrow detail/upcoming reads can then recover within the shared budget without a new endpoint/UI family. Correct descriptions equating projections with armed jobs; once still uses Task time, no duplicate set. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskScheduleInput.yaml` | §5.2 structured schedule fields, closed objects; no raw RRULE/cron input. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ScheduleTaskCreateInput.yaml` | §5.2 allowlisted task payload; reuse existing generated criteria/DoD item shapes and validation. No private attribution fields. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ScheduleTaskArguments.yaml` | Action plus optional same-workspace assertion, task/task_id/schedule and existing-task precondition, with the action matrix in §5.2. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskScheduleState.yaml` | Required canonical trigger, schedule_text, opaque SHA-256 schedule_hash, next_occurrences_ms array (0–3), nullable next_fire_ms, `scheduler_state: armed/disarmed/exhausted/unavailable`, `warnings: string[]`. Read-only computed state shared by Task.schedule and tool projections, **not** a new persisted entity. All required arrays are arrays, not null. Activation truth follows the outcome table below. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ScheduleTaskResult.yaml` | §5.2 result fields, saved identity, truthful persistence/scheduler_state/warnings. Reuse the state fields; no success-shaped error branch. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskScheduleApprovalPreview.yaml` | §5.3 server-authored action/identity/destination, before/after trigger and readable texts/warnings; full create payload when action=create. Add `continuing_authority_notice: string`, **required for create/set/resume and equal to AUTH-1's exact notice**, absent for clear/suspend. Add `firing_stays_off_notice: string`, **required for Set/replace whose saved result stays suspended and equal to “Automatic firing stays off. This change does not resume the schedule.”**, absent for every other action, including an unsuspended Set. Freeze/validate both with the proposal on live and reconnect paths. Missing or altered required text cannot be approved. Existing task ID absent/null only for creation; assignee/workspace resolved. No private Initiator/budget serialization and no creator sentence. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/AgentTaskListRow.yaml` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/AgentTaskListResult.yaml` | Preserve current allowlisted row/envelope fields and scoping metadata; add optional schedule state and nullable latest_run using TaskRun. Foreign rows omit both. No disk-task serialization or private provenance. |
| **New** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/AgentTaskOccurrencesResult.yaml` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/AgentTaskRunsResult.yaml` | Occurrences: generated sets plus workspace/foreign scope and requested bounds/zone. Runs: task_id, generated TaskRun array, matched/returned/truncated/nullable next_offset. No new run enum/status or instance entity. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/asyncapi.yaml`::ToolApprovalRequiredFrame / SessionStatePendingApproval and matching `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ToolApprovalRequiredFrame.yaml` / `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/SessionStatePendingApproval.yaml` | Optional typed schedule_preview on live and reconnect carriers; same frozen value. No new frame family. Use generated Go/TS/Zod values through registry, approver adapters, snapshot and store. |
| Existing task CRUD, occurrence, run-list and Run-now paths | Keep paths/entities and existing RunNowRequest, TaskRun and DayBucket shapes. Document suspended Run now. Add optional occurrence_ms to the agent run tool, not a new REST request/status. |

**Conditional PATCH responses:** missing/null/wrong-type/malformed token with a trigger returns 400 generated ErrorResponse with `code="schedule_precondition_required"`, `field="expected_schedule_hash"`, `error="A valid expected_schedule_hash is required when trigger is supplied."` Token without a trigger is 400 with `code="schedule_precondition_required"`, the same field and `error="expected_schedule_hash must be omitted when trigger is absent."` A well-formed mismatching token returns **409** with `code="schedule_conflict"`, `field="trigger"`, `error="The schedule changed. Read the current schedule, review your draft and reapply."` No other requested field commits on conflict. Fetch the latest Task for a new baseline, not a token calculated from form values. This is a breaking request change for any current client that PATCHes `trigger` without the 64-character `schedule_hash` from the last Task read: that call gets `400 schedule_precondition_required`. A hash computed by the client is not valid. Metadata saves omit both `trigger` and the hash. DOC-01 and DOC-05 must say so. Hash comparison and suspension-preserving trigger/Clear mutation are one locked operation; a refetch before an unconditional save is not sufficient. [S28, S31]

**Saved state versus activation — same generated state for HTTP and tools:**

| Authoritative observed outcome | HTTP / read-back result | Required user meaning |
|---|---|---|
| Saved active schedule with confirmed current derived job/admission generation | 200 Task; schedule.scheduler_state=armed, next_fire_ms from the actual current registration | Saved and automatic firing available; no guarantee of a completed execution. |
| Saved suspended/manual state with successful disarm | 200 Task; disarmed, null next_fire_ms | Future automatic admission disabled. Already admitted work remains independent. |
| Active elapsed once or exhausted recurrence, no future eligible position | 200 Task; exhausted, null next_fire_ms, explanatory warning | Save succeeded; there is no future fire. This is **not** a registration failure; edit timing or use permitted Run now. |
| Save succeeds; registration/removal/reconciliation fails or current activation cannot be established | **200 Task** with the saved trigger/flag; unavailable, null next_fire_ms, nonempty actionable warnings | Persistence succeeded; automatic starts are unavailable/not confirmed. A saved Resume has suspended=false, not a false Suspended badge or “nothing saved.” A saved Suspend still fences new admissions even if derived-job cleanup failed. |
| Storage failure before commit | 500 existing ErrorResponse; no success Task | No committed change. Ambiguous post-publication I/O follows §5.1.1 instead of a false definite-failure claim. |

GET task/list projections recompute `schedule` from **current persisted timing and current coordinator state**; they do not remember “armed” just because the flag is false or return a stale success from the mutation response. GET does not arm a job. After restart before reconciliation, a future active rule without confirmed activation is unavailable until reconciliation succeeds. A missing/unreadable activation dependency yields unavailable/warnings, not an invented next fire; an unreadable task/trigger is the normal read error, not a made-up state. Manual/suspended planned projections may remain, but their next fire is null. [S4, S28]

Recovery in both controls: read back Task; if active+unavailable, keep **Suspend schedule** available and offer **Retry activation**, using the same idempotent Resume endpoint to reconcile without re-anchoring. A repeated matching-state request is a persistence no-op, but may retry registration; valid existing jobs are not duplicated and the recovery actor does not replace the stored `Task.Initiator`. If the next read instead reports exhausted, explain no-future-fire and offer the existing edit/Run-now choices, not an endless activation retry. No new endpoint, job entity or durable availability flag is added. [S28, S31]

Reference new models as named OpenAPI components; the inspected Go configuration retains models with `skip-prune: true`. Generate using `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/scripts/gen-contracts.sh` and atomically commit specs plus artifacts under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/api/generated/`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/api/generated/` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/inboundschemas/` **before consumers**. Verify actual runtime rejection/response parsing with valid controls, not regeneration alone. [S16, S22]

### 5.6 Shared queries and explicit agent Run now

Extract the narrow occurrence/run projection into proposed package `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/taskquery/`, depending on task and generated contracts, never agent/gateway/tools. Both REST and tools use it. Preserve the existing pure-Go occurrence engine and run projection. One list request must not rescan the whole task store per returned row. Enforce range/caps and propagate real read/expansion/history faults rather than treating them as absence. [S1, S8]

**Request-wide occurrence budget (REST and tool share one counter set):** first apply workspace/principal and optional task-ID selection; never expand an unauthorized series to count or truncate it. Visit authorized candidate series in ascending task-ID order for deterministic results. Request-wide limits are **inclusive**; refuse before doing/adding the first item beyond a limit. Keep current per-task truncation flags for their existing per-task bounds; aggregate exhaustion is a distinct **whole-request refusal**, not another successful truncation. [S8, S28]

| Aggregate bound | Counting / exact boundary behaviour |
|---|---|
| 100 candidate series | Count every authorized selected recurring series before expansion, including one with no in-range positions. The 101st refuses before its expansion; optional task_id narrows the selection. Do not silently select the first 100. |
| 100,000 work units | Charge this published input formula, not positions the engine happened to visit and not a counter the production function returns. `work_units = selected_task_files + (recurring_series × inclusive_query_zone_days) + in_window_runs + irregular_charge`. `selected_task_files` is the count of authorized task files the selection reads. `recurring_series` is how many of those files are recurring series. `inclusive_query_zone_days` is the count of calendar dates in the query zone that intersect `[from, to)`; a UTC midnight-aligned span of N×24h is N. `in_window_runs` is the count of rows `Store.RunsInRange(taskID, fromMs, toMs)` returns (non-nil `OccurrenceMs` in `[from, to)`). Runs that function does not return are not charged. Do not call `ListRuns` or any other whole-history read to compute this charge. `irregular_charge` is 0 for a regular series. For an irregular series it is the iteration count of the existing per-task loop, at most `perTaskIterationBudget` (10,000) per series. The frozen fixtures below are regular, so that term is 0. Worked total: 2 series, 2 inclusive UTC days (`2026-10-19T00:00:00Z` inclusive to `2026-10-21T00:00:00Z` exclusive), 1 in-window run, 1 out-of-window run not charged = 2 + 2×2 + 1 + 0 = **7**. Cap fixture: 100 recurring series, zero other selected files, UTC `[2026-01-01T00:00:00Z, 2027-02-05T00:00:00Z)` = 400 inclusive days, 59900 in-window runs = 100 + 100×400 + 59900 = **100000**, which succeeds when every other cap allows it. 59901 in-window runs = **100001** and refuses the whole query. Charge before the work; at exactly 100,000, completion is allowed if nothing further is needed. No unmetered pre-expansion, whole-history materialization or per-task budget reset can escape this counter. [S8, S31] |
| 10,000 output items | One item per emitted series envelope, raw instant, day bucket or occurrence-run overlay. Counts inside a bucket are scalars, not enumerated positions. A projected item that would be 10,001 refuses; no partial sets leave the server. |
| 1,048,576 encoded UTF-8 JSON bytes | Entire response envelope and nested arrays, after encoding/escaping. Because REST returns an array and the tool adds scope metadata, charge the **larger of the two defined response encodings for the same projection**, so the shared query cannot pass one surface and exceed the other. Count both framing lengths without retaining two payload copies; stage the selected response in a capped encoder/buffer before sending headers/body. Do not materialize an unbounded result then measure. Exactly the charged bound succeeds; one extra byte refuses both. |

Selection/history traversal must stop on the shared work budget without first allocating all matching tasks/runs. Keep only bounded candidate references, per-task scratch and the capped staged result. Reuse existing store traversal/decoding with a bounded visitor where necessary; no new derived index/database. At any aggregate breach discard the staged projection and return the exact refusal in §3.4; the caller narrows task_id/range. REST and tool return the same error regardless of input enumeration order, and the UI shows actionable query failure, never a convincing incomplete calendar. Do not expose unauthorized task counts/identities in refusal text. DS-4/BDD-61 test many authorized series and **each exact maximum/+1**, including regular buckets, sparse irregular work, overlay reads and encoded-byte limits, plus forbidden-series positive/negative controls. [S8, S28]

| Read/run front door | Required behaviour |
|---|---|
| `list_tasks`, default `view=tasks` | Preserve required role, default/current-workspace semantics, status filtering, newest-updated order and 100-row matched/returned/truncated disclosure. Add schedule state and latest recorded run, not only the task mirror. A suspended task may have planned positions but no next fire. |
| `view=occurrences` | Required role/from_ms/to_ms/tz; optional task_id. Principal/workspace filtering **before** expansion. Same inclusive/exclusive range, regular arithmetic, irregular budget, bucket-day zone, overlays and truncation as REST. No duplicated once sets. |
| `view=runs` | Required role/task_id; limit default 20/max 100 and offset default 0. Retention-bounded newest-first history independent of current rule; matched/returned/truncated/next_offset. |
| Foreign reads | Existing status/identity task projection only. Do not provide occurrence/run detail for a foreign target, including an explicit task_id, and do not widen on missing caller or explicit invalid workspace. |
| `run_task` | Optional occurrence_ms. A scheduled standalone task uses a new **agent-authorized** adapter into the existing Calendar-equivalent run path, retaining initiating caller/depth/budget/current workspace/delegation and unattended markers. Scheduled Done mirror no longer blocks a fresh explicit run; unscheduled Done refusal stays. Future occurrence, plan member, dependency and overlap gates remain. |

Do not call today's operator-only `StartOccurrenceRun` with nil authority or erase unattended context by detaching to bare Background. Preserve context values while detaching request lifetime where necessary, and keep the existing Stop-owned cancellation path. An accepted asynchronous run need not have an opened run ID yet: return accepted status and session identity only when known, not fabricated started/completed facts. A task-level explicit run and a past/current occurrence run are different requests; neither resumes the schedule. [S1, S5, S7, S8]

### 5.7 Creator result notice — FQ-4, new scope after the final grill

This section is the only normative rule for the creating-agent notice. It is new scope after the final spec grill. The code-review gate and grill-code verify it. It is not a third grill and not a review PASS. No new REST or WebSocket schema is required: the notice is an existing session-message inbox entry plus the existing wake primitives. No new ADR. [S32]

**Who.** Only a task whose `CreatedByAgentID` is non-empty, and only a run whose kind is `RunKindScheduled` and whose close status is `done` or `failed`. One notice per such closed run, including one-time and repeating. A `RunKindManual` run, an explicit Run now, a goal retry that does not close a run, an overlap that does not finish (`ErrAlreadyRunning`), and a pre-claim refusal that never became a run (FQ-3, BDD-64) send nothing. Empty `CreatedByAgentID` is a person-created or pre-attribution task and sends nothing. FQ-2 is unchanged: the timed run still executes as the assigned agent under today's stored `Task.Initiator`.

**Where.** The recipient is `session.MainSessionID(task.WorkspaceID, task.CreatedByAgentID)`, and only when `MainSessionResolver.EligibleMain` says that pair currently has a visible main. Do not add this id to `captureRunRecipients`. Do not send this notice through `deliverTaskCompletionUpward`'s full-result path. Starter and assignee notices stay as they are today.

**Delivery, this path only.** Call `SteerUpwardDeliverer.Deliver` with `RecipientSessionID` set to that main id and `SuppressWake` set, so the ordinary idle wake in `wakeStoredMessage` does not fire at the child run's reporting target. The inbox body is the short notice below. Then wake that same main, and no other session:

| Main state | Wake |
|---|---|
| Lifecycle record exists and `getActiveTurnState` for that main is alive | `AgentLoop.EnqueueSteeringWake` on that main. No second turn. If enqueue returns `errSteeringScopeClosed` for the same generation and the record is not terminal, fall through to the idle wake. Do not drop the inbox row. |
| No lifecycle record yet (the main has never had a turn) | `AgentLoop.ensureOrdinaryRootRecord`, the same record a first turn creates, then the idle wake. |
| Lifecycle record exists and no live turn | Idle wake: `asyncNotifierImpl.WakeParentAlways`. Channel and chat id come from `SteerLauncher.reportingTargetFor` on that main. A main is stored with channel `main` and no peer id, so the existing fallback applies: channel `webchat` and chat id equal to the main session id. Do not publish this wake on the stored channel `main`. |

Do not edit `wakeStoredMessage`, `captureRunRecipients`, or `deliverTaskCompletionUpward` to achieve this. Those stay the starter/assignee and ordinary child-finished paths.

**Gone.** If `EligibleMain` is false, or the workspace or agent cannot be resolved, or the computed main is hidden or removed: do not wake, do not pick another session, and do not create a main for an ineligible pair. The closed run stays in run history. Log the miss by calling `reportUndeliveredWake` with `DeliveryStoredNotWoken` for this notice. A `Deliver` error or a failed `WakeParentAlways` is likewise not swallowed: keep the inbox row if it was written, and log through `reportUndeliveredWake` or, when no delivery result exists, the existing error log `recordNoticeLoss`. An info line alone is not enough.

**Words.** `done`, `failed`, or `stopped`. `stopped` keeps today's meaning: status `failed` and `CancelReason == stopped_by_user`. A refusal is a `failed` run plus its stored reason inside the summary rules below. There is no `refused` status.

**Text.** Header lines, then one summary paragraph. The header always includes the task title, the task id, the scheduled occurrence time in UTC (`occurrence` when present, otherwise the words `unscheduled time`), the actual finish time in UTC, the outcome word, and the task id again as the reference. Do not read or copy the task prompt, instructions, or acceptance criteria. Apply `FilterSensitiveData` — the same filter `asyncNotifierImpl.Notify` already applies — to the title and the summary before the inbox append and before the wake content. The wake content is this same notice, not `deliverySummary`'s “delegated session” wrapper, and not `Task.Result` uncut.

The summary paragraph is at most **500 UTF-8 bytes**. That number is the existing short-summary budget on `SessionMessageCheckpoint.summary` (`maxLength: 500`, “1-3 sentence checkpoint summary”), not the 50,000-character handback and not the 1MB `asyncNotifyMaxContentBytes` wake cap. Source, in order: the worker's `TASK_SUMMARY` line when `taskSummaryLabel` parsing finds one; otherwise the fixed sentence `Outcome <word>.` plus, only for `failed` or `stopped`, the stored result after the sensitive-data filter. Filter first, then cap. When the filtered paragraph is over 500 bytes, keep a code-point-aligned prefix of 480 bytes and append ` [summary truncated]` (20 bytes). The stored paragraph is then 500 bytes. The 1MB truncation marker must not be reused: it would claim the cut was 1MB.

**Not a run.** The wake is an inbound message on the creating agent's main (`EnqueueSteeringWake` or `WakeParentAlways` / `processSystemMessage`). It must not call `ClaimForRun` or `RunScheduled` and must not open a `TaskRun`. If that woken turn later calls `schedule_task`, that call is ordinary scheduling under the normal checks, not a loop from the notice itself. [S32]

## 6. Normative frontend design

### 6.1 UX verdict and priority actions

**UX verdict: Solid foundation with state/approval safeguards required.** This is design guidance, not a rendered-UI review or brand decision. Follow the existing design system; no new palette, iconography language or bespoke control family is commissioned. [S1, S18, S22]

| Heuristic | Required decision |
|---|---|
| H1 — visibility of system status | Show saving, persisted suspension and failures separately from execution status. Unknown save outcome must be visible. |
| H2 — real-world language | Show exact schedule/time zone/end in ordinary language; never raw cron/RRULE, including generic approval fallbacks. |
| H3 — user control | Keep Suspend/Resume reversible and visibly distinct from Stop/Restart/Run now. |
| H5 — error prevention | Preserve suspension on edits, refuse stale approvals and do not replay missed work. |
| H9 — recovery | Inline actionable errors, explicit unavailable versus empty results, and read-back before retrying ambiguous writes. |
| H10 — documentation | Explain counts, exhausted/expired schedules and rollback danger in the affected public pages. |

**Priority actions**: (1) make the two schedule controls truthful and independently reachable; (2) bind the readable card and reconnect to the exact server proposal; (3) preserve recorded run truth while adding suspension labels.

### 6.2 Both detail controls, with a complete state contract

Add **Suspend schedule / Resume schedule** in the schedule section of `CalendarEventSlideOver` and `TaskDetailPanel`, not in execution controls. The task panel remains an existing detail surface, not a new Board entry for scheduled work; scheduled tasks stay Calendar-only. Do not add a trigger-time editor to task detail. Both controls use generated REST response types via the tasks API wrapper and the narrow suspend/resume endpoints. Do not copy the read-only response flag into a PATCH. [S1, S9, S18]

| Visible state | Control and feedback | Required next state |
|---|---|---|
| Active valid time schedule | Suspend schedule; hint “No new scheduled runs; already admitted work continues.” | Only server-confirmed success changes to Resume schedule and Suspended. |
| Suspended valid time schedule | Resume schedule; hint “Missed occurrences will not run; planned occurrences still count toward the end.” | Only server-confirmed success removes Suspended; do not promise a fire if expired/exhausted. |
| Saving | “Suspending…” or “Resuming…”, disabled pending activation, existing schedule state remains factual. | Do not close the detail surface merely to hide pending/error state. |
| Definite pre-commit refusal/failure | Inline FormError next to schedule; preserve current known state and drafts. | Read current task if conflicting, offer valid retry; do not label saved-but-unavailable as a failed write. |
| Saved Resume, activation unavailable | Use saved suspended=false: remove the Suspended badge, retain Suspend schedule, and show “Schedule saved; automatic starts are unavailable” plus the server's actionable warning. | Read back activation state and offer Retry activation through idempotent Resume; do not retry create, re-anchor or claim operational scheduling. |
| Saved Suspend, derived cleanup unavailable | Show saved Suspended/Resume schedule plus warning that cleanup needs recovery; persisted admission fencing remains effective. | Read back/reconcile; do not falsely say the suspension was unsaved or stop admitted work. |
| Saved active, no future eligible position | No Suspended badge; explain expired/exhausted and null next fire. | Existing Calendar edit or permitted Run now, not an activation-failure loop. |
| Lost response / unknown persistence | Explain that save could not be confirmed; refetch Task **including schedule state**. | Reconcile saved flag **and** armed/disarmed/exhausted/unavailable; no automatic create retry or success claim. Idempotent state controls may be retried after reconciliation. |
| Manual/no trigger/heartbeat | No Suspend/Resume control. | Existing feature-specific heartbeat controls remain separate. |

After a confirmed change invalidate/refetch task list, selected detail and occurrence queries; refresh run queries only when their execution data actually changes. Agent-origin task updates must also refresh visible schedule state through the existing task event/query path. Do not manufacture a new WS frame family. Reopening/reloading reads server truth; one view's successful change must update another open view. Background refresh must not wipe unrelated unsaved instructions, title or recurrence edits. [S1, S12, S18]

Title/agent/metadata-only saves omit trigger and `expected_schedule_hash` entirely. A real rule/time edit sends input-only type/config **and the baseline hash captured when this recurrence draft was loaded**, not a newer token substituted by background refetch. Keep existing rule-edit count-reset notice/zone behaviour; suspension alone never shows “count restarted.” Server comparison and suspension preservation are atomic (§5.5). [S1, S9, S28]

On `409 schedule_conflict`, **both detail surfaces** retain the user's local drafts, show that the server schedule changed, and fetch/display the current schedule separately. Offer **Read current schedule / Review draft / Reapply after review** using existing buttons, not a blind automatic retry. Only an explicit reviewed reapply adopts the newly fetched baseline; another intervening change can conflict again. TaskDetailPanel has no new timing editor: its schedule controls refresh truth while unrelated title/instruction drafts stay local; any trigger edit it submits through shared actions uses the same precondition. If an older payload lacks `schedule.schedule_hash`, refetch from the new server; if still unavailable, disable trigger-changing save with a recoverable error rather than compute a hash. Metadata-only saving remains independent. T26 uses a barrier-controlled genuine trigger PATCH from view A arriving after view B/agent commits, and T11/T31 assert draft preservation/review/reapply. [S18, S28]

### 6.3 Calendar and history presentation

| Data state | Required presentation |
|---|---|
| Any suspended time schedule | Textual **Suspended** badge/label in schedule detail; never colour alone. |
| Future/current unrecorded recurring position | Suspended when current schedule is suspended; otherwise existing Scheduled. No Run now for a strictly future occurrence. |
| Recorded recurring run | Its actual in_progress/done/failed/skipped status, result and chat link win. Show current schedule suspension separately; never relabel the run. |
| Past unrecorded position | Existing **No record**. Explain that a current suspension flag is not historical evidence; do not say “was suspended” or create a skipped run. |
| One-time chip | Preserve existing fire instant/path and execution information; identify current suspension in text/accessibility/tooltip. No duplicate recurring occurrence set. |
| Dense recurring day group | Label schedule Suspended separately; preserve factual bucket timestamps/counts and recorded outcome breakdown. Do not turn all future planned bucket positions into executed/skipped counts. |
| Month dots | Preserve current Month navigation/rendering. Include suspension in the accessible/hover title where event state is exposed; do not add a new Month interaction or raw rule display. |
| Older compatible payload without flag | False, not unknown. A malformed present field is a contract error, not false. |
| Failed occurrence/history fetch | Recoverable error/unavailable state and retry, never a convincing empty calendar/history. |

An aggregate-budget refusal is a recoverable QueryErrorState with the server's narrower-task/range guidance, never empty Calendar success. Detail/upcoming queries send the selected task_id through the extended occurrence GET and include that selector in their query key; whole-workspace Calendar queries remain unfiltered. Preserve the existing response array. Use existing task-detail access for a single-series recovery rather than commission a new screen/filter family. No automatic fan-out into unbounded per-task requests to evade the aggregate cap. [S28, S30]

The server remains the occurrence authority. No new client expansion, calendar-position ledger, schedule-specific run status or synthetic history is introduced. UI local view models may add a presentation-only suspended state without changing Task/TaskRun wire enums. [S1, S4, S8, S9]

### 6.4 Readable approval card, not a raw-argument dump

Extend the existing per-tool preview registry with a replace-mode scheduling preview. It consumes the generated server preview through the live/reconnect store, never model prose or a fetch of the newest task. The ordinary approval modal retains its decision controls and scope. [S1, S11, S12]

| Action | Required readable content |
|---|---|
| Create | Acting agent; full task title/instructions and completion contract; workspace/assignee; exact starting date/time/zone, rhythm and end; every supplied optional task field accessible before approval. No hidden executable payload. |
| Set | Identity and assignee/workspace; old → new schedule; explicit anchor, retained time zone and count-restart consequence. When the saved result stays suspended, also the exact `firing_stays_off_notice` before the decision controls. An unsuspended Set does not show that sentence. Count-restart text is not a substitute. |
| Clear | Old schedule → manual/no future automatic starts; no claim of task deletion or execution Stop. |
| Suspend | Exact target/schedule; “No new scheduled runs; already admitted work continues.” |
| Resume | Exact retained schedule; “Missed occurrences will not run; planned occurrences still count toward the end.” Show expired/exhausted warning when applicable. |

One-time dates include the explicit offset/UTC, never an unlabelled browser-local conversion. Recurring schedules name the series zone and end. Example: “Mia wants to schedule ‘Weekly report’: every Monday at 09:00, Europe/Berlin, starting 19 October 2026, never ends.” Escape titles/instructions as text; do not render task-supplied markup. Missing/malformed preview blocks approval with recovery copy, not a fallback RRULE/JSON dump. A reconnect card must retain the same proposed instant and payload even if the task was edited elsewhere. [S1]

For **create/set/resume**, render the frozen generated `continuing_authority_notice` from AUTH-1 as ordinary visible copy **before the decision controls**, on live and reconnect cards, beside the schedule/end consequences. The exact sentence is “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.” A missing or altered notice makes those scheduling previews unusable: disable Approve/Always Allow and request a fresh proposal. Never say later revoking that agent's scheduling permission cancels the schedule, and do not name a creator. For a Set whose saved result stays suspended, render `firing_stays_off_notice` in the same place and with the same block-on-missing rule; an unsuspended Set omits it. [S31]

Add the **Changing task schedules** Auto Ask group and **Change task schedule** humanized label. Explain that Auto does not waive an effective Ask for this tool; Allow/God Mode and stored per-agent Deny retain the existing model. Update independent membership/label tests against the actual L1 set, not mutually stale copies. [S1, S15]

### 6.5 Interaction/accessibility acceptance

**Catalogue-first reuse map**, verified against `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/design-system/catalog.json`::entries. No new public component family, visual tokens or modal is commissioned. [S30]

| UI job | Existing dependency / adaptation |
|---|---|
| Suspend/Resume, Read current/Review/Reapply, Retry activation | Button from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/button.tsx`; use its existing pending/disabled semantics and ordinary status text, not a raw/local control. |
| Suspended versus current activation warning | Badge from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/badge.tsx`; text names state independently of colour. |
| Local save/conflict/saved-unavailable guidance | FormError from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/FormError.tsx`; retain existing accessible feedback rather than build another banner. |
| Query loading/error/genuinely empty data | Skeleton, QueryErrorState and EmptyState from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/skeleton.tsx`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/query-error-state.tsx`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/empty-state.tsx`; failed/over-budget is not empty. |
| Existing detail and approval containment | Keep the detail's Sheet (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/sheet.tsx`) and approval modal's existing Dialog (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/dialog.tsx`); extend the registered preview inside that containment, not a parallel dialog. |

Load the design-system skill before implementation. Keep the existing modal/Sheet focus trap and return focus to the initiating control on dismissal. Keyboard users can Tab to the schedule control and operate it with Enter/Space; disabled saving state cannot double-submit. Errors and state changes are announced through the existing accessible status/error pattern. The full create payload is keyboard-reachable and readable on narrow screens before approval; no horizontal-only inspection requirement. Test normal/forced colours, zoom/reflow and reduced motion through the existing design-system conventions. No new local focus ring or colour-only suspension meaning. [S18, S22]

## 7. Integration boundaries and runtime

| Boundary | Data in / out and contract | Failure behaviour | Development / verification |
|---|---|---|---|
| Native tool policy and delegation | Prepared structured arguments; existing policy/grant/delegation decisions; allowlisted generated result. | Refuse unknown caller, effective Deny, stale state or unreadable authority before writes. Unattended unresolved Ask refuses immediately. | Real policy/grant paths; controlled role/authority fixtures. |
| Task + paired goal storage | Preallocated/normalized identity, defining goal first, complete Inbox task last (§5.1.1), per-entity atomic files. | No discoverable incomplete task even after failed rollback/restart; orphan cleanup/ambiguous publication reported, no blind retry. | Real store/discovery/recovery held at each cut; no substitute aggregate file. |
| Derived cron jobs and executor | Stored `Task.Initiator` → current job/generation → trusted timed admission; a pre-claim one-time refusal keeps that job and the pre-fire task file (FQ-3); an admitted continuation uses its receipt plus today's Initiator carry-forward. | Saved-unavailable explicit; no stale/suspended new admission; positive capacity-refused retry pickup after a receipt exists; a never-claimed one-time fire is not dropped. | Real queue/executor/cron-lane barriers and effect counts, not wall-clock sleep. BDD-64 is not proved by the link check. |
| Gateway REST / SPA | Generated input-only trigger + required conditional PATCH baseline; Task.schedule read-back and narrow state POSTs. | Exact 400/409 and saved-but-unavailable 200; no partial stale PATCH or ignored suspension/private input even validation-off. | Real HTTP barrier/faults plus generated validators and valid controls, both mounted UIs. |
| Existing audit logger | Actor/task/workspace/action, authoritative before/after scheduling identity and actual outcome (§5.3.1). | Best-effort sink failure logged, not false unsaved response/rollback; disabled auditing unchanged. | Real tool/UI mutations, inspect rows and audit-failure log, not a second ledger. |
| Approval WebSocket / reconnect | Frozen typed preview through existing live and pending-state carriers. | Missing/unusable preview is unapprovable; resolution/expiry is authoritative; disconnect never implies Stop. | Real registry plus live/reconnect serialization/store tests and browser reload. |
| External model/provider | Ordinary native-agent requests/tools; no new provider API or credential. | Existing provider failure remains visible; no false “armed”/“ran” inference from model prose. | Deterministic provider twin for automated tool-loop tests; real mandated provider for UAT. |
| User-facing documentation | Updated pages for actual controls, permission, run/read semantics and operations. | Missing or inaccurate behaviour documentation blocks completion. | Implementing leads draft; independent docs-verifier audits against implementation. |

**Prerequisites / stack**: existing Go application (minimum Go 1.26.9 from the module), existing React/TypeScript embedded SPA and its installed build tooling; pure-Go recurrence library already present, no new runtime dependency. Supported systems are Linux/macOS/Windows only. Time scheduling itself needs no external service; executing an agent still needs its configured usable model/credentials. No extra database, background server or hardware minimum is invented. [S1, S16, S22, S23]

**Development setup**: use the existing repository setup and CI workflow, not a new feature bootstrap. All commands run from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks`. Go gates use `CGO_ENABLED=0` and tags `goolm,stdjson`; no full local Go build/suite. A fresh build needs the existing SPA embed artifacts or approved stub; UAT needs the real rebuilt/synced SPA, never the stub or a stale embedded bundle. Use `npm run typecheck`, not bare TypeScript no-emit. Exact generator entry point is in §5.5. No setup, regeneration or runtime gate was executed by this spec-only change. [S16, S22, S23]

**Deployment/runtime**: retain existing gateway start/stop, logs, boot reconciliation and recovery sweep. Persisted suspension survives restart and active-run completion; scheduling metadata is not a new always-hot session. Keep query work bounded and avoid per-row whole-store scans. Do not use a rollback to an older flag-ignorant binary as a safe pause mechanism: explicitly stop scheduling outside that binary before any such operator rollback. Do not rewrite historical task/run statuses on upgrade. [S1–S5, S22]

## 8. BDD scenarios

BDD means behaviour-driven development: concrete Given/When/Then outcomes. IDs below identify scenarios, not executed tests. Each outline row is an independent case. All timestamps in the fixed-clock suspension fixtures are server times.

### Agent scheduling

#### BDD-01 — Scenario: Create a future one-time task
**Traces to:** User Story 1, Acceptance Scenario 1 (US-1.AC1).
**Category:** Happy Path.
- **Given** Mia can schedule locally and a complete task is proposed for 19 October 2026 at 09:00 +02:00.
- **When** the prepared create is approved.
- **Then** one Inbox task and its paired completion contract are saved with that instant and active scheduling.
- **And** no run/session is created immediately; saved identity and actual activation state are returned.

#### BDD-02 — Scenario Outline: Create each Calendar-supported recurrence
**Traces to:** User Story 1, Acceptance Scenario 1 (US-1.AC1).
**Category:** Happy Path.
- **Given** a complete local task, explicit anchor/zone, interval 1 and `<pattern>`.
- **When** the permitted schedule is created.
- **Then** the task's planned positions and readable summary match the Calendar's `<pattern>` interpretation.

| pattern |
|---|
| minutely |
| hourly |
| daily |
| weekly Monday and Friday |
| monthly day 31 with shorter-month clamp |
| monthly last Tuesday |
| yearly |

#### BDD-03 — Scenario Outline: Change or clear an existing schedule
**Traces to:** User Story 1, Acceptance Scenario 2 (US-1.AC2).
**Category:** Happy Path.
- **Given** an owned task with prior runs and `<before>`.
- **When** the agent performs `<action>`.
- **Then** `<after>` is saved under the same task ID and prior history remains.
- **And** no immediate run is caused by the schedule mutation.

| before | action | after |
|---|---|---|
| manual | set future weekly rule | active recurring schedule |
| suspended recurring | set future once | suspended once |
| active once | set later once | active later once |
| suspended recurring | clear | canonical manual, no suspension |
| manual | clear | unchanged manual successful no-op |

#### BDD-04 — Scenario: Freeze rule-only edit defaults before approval
**Traces to:** User Story 1, Acceptance Scenario 3 (US-1.AC3).
**Category:** Alternate Path.
- **Given** an existing Europe/Berlin series and a rule-only proposal prepared at 08:12:00Z without a supplied new anchor/zone.
- **When** it is approved at 08:15:00Z.
- **Then** the new anchor is the prepared 08:12:00Z, the stored zone remains Europe/Berlin, and the actual rule edit restarts its planned count.

#### BDD-05 — Scenario Outline: Refuse malformed or contradictory scheduling input
**Traces to:** User Story 1, Acceptance Scenario 4 (US-1.AC4).
**Category:** Error Path.
- **Given** an otherwise authorized call with `<bad_input>` from dataset DS-1.
- **When** it is submitted.
- **Then** the specific field/shape violation is reported and no task, goal, job, run or session is changed.

| bad_input |
|---|
| unknown action/key or wrong/null/fractional field type |
| missing/contradictory action fields or invalid completion contract |
| out-of-bound repeat/month/end or empty/duplicate weekday selection |
| missing new anchor/zone, offset mismatch or attempted series-zone change |
| raw rule escape hatch, or schedule supplied to ordinary create/update |

#### BDD-06 — Scenario Outline: Match compiler calendar/time edge semantics
**Traces to:** User Story 1, Acceptance Scenario 3 (US-1.AC3).
**Category:** Edge Case.
- **Given** a valid `<fixture>` authored through equivalent Calendar and agent forms.
- **When** the server computes the planned positions.
- **Then** both forms yield the fixed expected instants/end from DS-2, not an expectation generated by the compiler under test.

| fixture |
|---|
| Berlin spring gap and fall overlap |
| monthly day 29/30/31 in leap and short months |
| fifth/last weekday, end-date zone boundary and count end |

#### BDD-07 — Scenario: Report incomplete creation and cleanup faults
**Traces to:** User Story 1, Acceptance Scenario 4 (US-1.AC4).
**Category:** Error Path.
- **Given** goal-first creation with goal-write, pre-publication task-write or orphan-goal-cleanup failure injection.
- **When** a scheduled create is attempted.
- **Then** no incomplete task is published/admissible through lookup, notification or recovery, actual allocated identity/failure/possible orphan goal is reported, and cleanup failure is not swallowed.
- **And** a complete final task published before a later response/registration fault is reported as saved/unknown according to §5.1.1, never deleted or described as definitely unsaved.

#### BDD-08 — Scenario: Preserve saved identity when scheduler activation fails
**Traces to:** User Story 1, Acceptance Scenario 4 (US-1.AC4).
**Category:** Error Path.
- **Given** task and goal persistence succeed but job registration fails.
- **When** create/set/resume completes.
- **Then** the result names the saved task, persistence=saved, scheduler_state=unavailable and explicit warnings, with no claimed next fire or duplicate-create advice.

### Suspension and execution ordering

#### BDD-09 — Scenario: Suspend an idle time schedule, including repeat requests
**Traces to:** User Story 2, Acceptance Scenario 1 (US-2.AC1).
**Category:** Happy Path.
- **Given** a valid active once or recurring schedule without admitted work.
- **When** Suspend succeeds, including an already-suspended retry.
- **Then** persisted suspension is true, no new automatic fire is admitted and no task/run/goal outcome or anchor is reset.

#### BDD-10 — Scenario Outline: Order suspension against actual admission
**Traces to:** User Story 2, Acceptance Scenario 1 (US-2.AC1).
**Category:** Edge Case.
- **Given** a due callback and Suspend are held at deterministic admission barriers with `<winner>` committing first.
- **When** the second operation proceeds.
- **Then** `<outcome>` and the task remains suspended for later positions.

| winner | outcome |
|---|---|
| Suspend | zero new reset, run, session or executor start |
| admission | exactly the admitted run continues, including its own retry/helper work; no later automatic admission |

#### BDD-11 — Scenario Outline: Reject old callbacks and stale arm snapshots
**Traces to:** User Story 2, Acceptance Scenario 1 (US-2.AC1).
**Category:** Edge Case.
- **Given** `<stale_source>` was captured before a completed Suspend (and, where stated, Resume).
- **When** that stale operation proceeds.
- **Then** it cannot replace the current job or admit work, create synthetic skipped history or reset task execution state.

| stale_source |
|---|
| due callback queued before Suspend |
| old callback after Suspend→Resume restored identical rule |
| delayed upsert after newer suspension |
| re-arm after an already admitted run finishes |
| recovery/boot scan snapshot superseded before publication |

#### BDD-12 — Scenario: Resume at the first strictly future position
**Traces to:** User Story 2, Acceptance Scenario 2 (US-2.AC2).
**Category:** Happy Path.
- **Given** a daily 09:00 UTC series anchored 19 October, suspended across 20–21 October, and server now 21 October 09:00.
- **When** Resume succeeds, including a repeated already-active request.
- **Then** the next fire is 22 October 09:00, no missed/current position runs, and the original anchor/count/end/status remain unchanged.

#### BDD-13 — Scenario Outline: Resume after count, date or once exhaustion
**Traces to:** User Story 2, Acceptance Scenario 2 (US-2.AC2).
**Category:** Edge Case.
- **Given** a suspended `<expired_schedule>` from DS-2.
- **When** it resumes.
- **Then** Resume saves successfully without replay, next fire is null and the result explains no future fire and the edit/Run-now choices.

| expired_schedule |
|---|
| three-position daily series after its third planned position |
| original end instant has passed |
| once at server now or earlier |

#### BDD-14 — Scenario: Schedule a new future once after a completed recurring run
**Traces to:** User Story 2, Acceptance Scenario 2 (US-2.AC2).
**Category:** Edge Case.
- **Given** a Done or Failed last-run mirror and a deliberate future once replacement/move, suspended or active.
- **When** the new once schedule is active and its future instant arrives.
- **Then** it remains eligible for one normal timed admission, without changing status enums or replaying the previous once instant.

#### BDD-15 — Scenario Outline: Preserve suspension across other writers
**Traces to:** User Story 2, Acceptance Scenario 3 (US-2.AC3).
**Category:** Alternate Path.
- **Given** a valid suspended task with history.
- **When** `<writer>` updates it.
- **Then** suspension stays true and no automatic run is admitted; only explicit Resume or Clear can remove it.

| writer |
|---|
| title, prompt, assignment, priority, tags, due or checklist save |
| actual time/rule/end replacement |
| run reset, completion, result, dependency or retry write |

#### BDD-16 — Scenario Outline: Refuse unsupported or invalid suspension changes
**Traces to:** User Story 2, Acceptance Scenario 4 (US-2.AC4).
**Category:** Error Path.
- **Given** `<invalid_target>`.
- **When** Suspend/Resume or an invalid suspended-trigger write is attempted.
- **Then** it refuses with the documented boundary outcome and leaves all files/jobs/runs unchanged.

| invalid_target |
|---|
| manual/no trigger or heartbeat |
| absent task, malformed ID or unexpected POST body |
| invalid rule/zone/assignee, including manual plus suspended=true |
| unreadable task or failed persistence |

#### BDD-17 — Scenario: Keep suspension across restart and recovery
**Traces to:** User Story 2, Acceptance Scenario 4 (US-2.AC4).
**Category:** Happy Path.
- **Given** a persisted suspended task, an old derived engine job and a separate active positive-control task.
- **When** the application restarts and performs reconciliation/recovery.
- **Then** the suspended task stays disarmed and the active valid control can fire; old missing-flag task files remain active without bulk rewriting.

#### BDD-18 — Scenario Outline: Prevent an alternate automatic new-run start
**Traces to:** User Story 2, Acceptance Scenario 1 (US-2.AC1).
**Category:** Edge Case.
- **Given** a waiting timed task, active or suspended, and `<alternate_entry>`.
- **When** that automatic entry attempts a new run before trusted timed admission.
- **Then** no run/session/status reset is created; an ordinary manual positive-control task still follows existing dispatch.

| alternate_entry |
|---|
| queue drain, including creation/notification interleaving |
| dependency-unblock dispatch |
| parent/sibling follow-up dispatch |

### Discovery, defaults and authorization

#### BDD-19 — Scenario: Discover and execute the live tool after boot and reload
**Traces to:** User Story 3, Acceptance Scenario 1 (US-3.AC1).
**Category:** Happy Path.
- **Given** an eligible native conversation agent and real task stores/authorizer/coordinator.
- **When** it searches for task scheduling after boot, late binding or reload.
- **Then** the catalog and agent expose the real schedule tool, the normal approval path is reachable, and an approved valid call changes the persisted task, not a metadata-only stub.

#### BDD-20 — Scenario Outline: Apply the two-layer policy matrix
**Traces to:** User Story 3, Acceptance Scenario 1 (US-3.AC1).
**Category:** Happy Path.
- **Given** a role/global/per-agent/Auto/God configuration from DS-3.
- **When** scheduling is attempted.
- **Then** the exact expected policy/card outcome occurs with no resolver-specific role fallback.

#### BDD-21 — Scenario: Leave existing workers unchanged on upgrade
**Traces to:** User Story 3, Acceptance Scenario 2 (US-3.AC2).
**Category:** Edge Case.
- **Given** Q1 B (Source: Interview, 2026-10-15), existing native/external worker entities with absent or explicit Allow/Ask/Deny scheduling settings, and new-worker creation controls.
- **When** the installation upgrades or reloads with the new global catalog entry.
- **Then** all existing entity maps/explicit settings remain byte-equivalent, missing worker keys inherit global Ask, and only newly created workers receive explicit default Deny.
- **And** no worker rollout/migration marker or deny backfill is introduced; restart does not insert a deliberately absent key.

#### BDD-22 — Scenario: Let Omnipus schedule through the global ceiling
**Traces to:** User Story 3, Acceptance Scenario 2 (US-3.AC2).
**Category:** Alternate Path.
- **Given** Q2 YES (Source: Interview, 2026-10-15) and the global scheduling ceiling is Ask.
- **When** the new catalog/Omnipus supporting set is seeded and a local authorized proposal is made.
- **Then** schedule_task belongs to the supporting set, Omnipus has no per-agent scheduling Ask entry, and its effective Ask follows the same exact approval and AutoAsks paths.
- **And** no implicit task read/run grants or foreign-workspace authority are added; explicit operator policy still applies.

#### BDD-23 — Scenario Outline: Check real caller ownership
**Traces to:** User Story 3, Acceptance Scenario 3 (US-3.AC3).
**Category:** Error Path.
- **Given** an existing local task and `<caller_case>`.
- **When** set/clear/suspend/resume is attempted.
- **Then** only a real nonempty agent assignee or agent-namespaced creator is eligible; mixed-name collisions, unrelated callers and unresolved identities refuse before writes.

| caller_case |
|---|
| assignee or real agent creator (positive controls) |
| human Owner/CreatedBy string collides with caller agent ID |
| unrelated or empty caller |

#### BDD-24 — Scenario: Refuse foreign or inaccessible scheduling before writes
**Traces to:** User Story 3, Acceptance Scenario 4 (US-3.AC4).
**Category:** Error Path.
- **Given** an explicit foreign workspace, mismatching same-workspace assertion or inaccessible destination.
- **When** create/set/clear/suspend/resume is submitted.
- **Then** it refuses with local-delivery guidance and no target task/goal/job/run/session mutation.

#### BDD-25 — Scenario Outline: Recheck the correct authority at each boundary
**Traces to:** User Story 3, Acceptance Scenario 3 (US-3.AC3).
**Category:** Error Path.
- **Given** `<boundary>` and `<authority_change>` under AUTH-1 (founder FQ-2).
- **When** that boundary reaches its current authorization check.
- **Then** `<outcome>` without substituting the scheduling caller for the stored `Task.Initiator`, and without treating unreadable authority as a person.

| boundary | authority_change | outcome |
|---|---|---|
| Set/Resume after approval | scheduling caller's tool/ownership/delegation denied or unreadable before mutation | refusal with zero write/dispatch |
| next timed admission | assigned agent's current membership/delegation removed or unreadable, or the stored non-nil initiator fails the existing automatic check | refusal with no new run; stored initiator bytes stay; no creator is reconstructed |
| next timed admission | only the different scheduling agent's schedule_task permission removed after committed Set/Resume | the schedule is not cancelled; the fire still uses the stored `Task.Initiator` |
| next timed admission | stored initiator is nil (today's person/scheduler case) and a different scheduling agent is later denied | the nil stored value remains what the timer uses; the scheduling caller is not substituted |

#### BDD-26 — Scenario: Preserve every explicit operator policy
**Traces to:** User Story 3, Acceptance Scenario 2 (US-3.AC2).
**Category:** Edge Case.
- **Given** old/fresh global maps and explicit agent Allow/Ask/Deny settings in both creation routes/default preview.
- **When** defaults/reconciliation or permitted submitted overrides are applied.
- **Then** global explicit values and submitted choices survive; the custom-Main constructor map contains explicit Ask for `schedule_task`; `RestrictForWorkerTier` then stores Deny; REST's final Deny-fill is not changed and is not relied on to preserve an omitted key. A Mia sparse seed does not contain the key.

### Approval semantics

#### BDD-27 — Scenario Outline: Show a readable exact commitment
**Traces to:** User Story 4, Acceptance Scenario 1 (US-4.AC1).
**Category:** Happy Path.
- **Given** a server-prepared `<action>` proposal requiring Ask.
- **When** the approval card renders.
- **Then** it shows resolved task/workspace/assignee, exact before/after timing and applicable consequences, with no raw rule syntax or implicit browser-zone interpretation.

| action |
|---|
| create |
| set |
| clear |
| suspend |
| resume |

#### BDD-28 — Scenario: Expose the complete create payload safely
**Traces to:** User Story 4, Acceptance Scenario 1 (US-4.AC1).
**Category:** Edge Case.
- **Given** a create proposal with Unicode/markup-like title, multi-line instructions, judged contract and all optional task fields.
- **When** its card is inspected before approval.
- **Then** every executable field is accessible as escaped readable content, without executing markup, dropping hidden task fields or horizontal-only inspection.

#### BDD-29 — Scenario: Bind grants to normalized immutable proposals
**Traces to:** User Story 4, Acceptance Scenario 2 (US-4.AC2).
**Category:** Happy Path.
- **Given** a prepared proposal with fixed default time/zone/hash and a remembered identical-call grant.
- **When** the same normalized proposal is executed.
- **Then** grant lookup/request/recording use that same complete argument identity, the fixed proposal executes without a phantom pending card, and changed schedule/anchor/target requires a different grant.

#### BDD-30 — Scenario Outline: Refuse a changed pending commitment
**Traces to:** User Story 4, Acceptance Scenario 2 (US-4.AC2).
**Category:** Error Path.
- **Given** an outstanding proposal and `<concurrent_change>`.
- **When** its approval is accepted for execution.
- **Then** current state/policy/authority is rechecked, no stale write occurs, and the caller is told to read and prepare a new proposal.

| concurrent_change |
|---|
| trigger rule/time/suspension change |
| assignee or workspace change |
| policy/delegation removal or unreadable authority |
| target deleted |

#### BDD-31 — Scenario: Restore the exact pending proposal on reconnect
**Traces to:** User Story 4, Acceptance Scenario 3 (US-4.AC3).
**Category:** Happy Path.
- **Given** a complete pending scheduling preview and a cleared browser approval store.
- **When** the browser reconnects.
- **Then** the pending snapshot restores the same preview, proposal/remaining expiry and usable decision controls, never a latest-task reconstruction or bare-tool stub.

#### BDD-32 — Scenario: Block an unusable scheduling preview
**Traces to:** User Story 4, Acceptance Scenario 4 (US-4.AC4).
**Category:** Error Path.
- **Given** a missing/malformed preview in a live or reconnect entry.
- **When** the card is displayed.
- **Then** Approve/Always Allow cannot run, recoverable guidance is shown and Deny/Dismiss remains available as appropriate without fabricated schedule text.

#### BDD-33 — Scenario Outline: Refuse without mutation on approval termination
**Traces to:** User Story 4, Acceptance Scenario 4 (US-4.AC4).
**Category:** Error Path.
- **Given** an outstanding scheduling approval.
- **When** `<termination>` occurs.
- **Then** no schedule mutation happens and subsequent stale reconnect/resolution frames cannot resurrect or execute the proposal.

| termination |
|---|
| Deny |
| Cancel / Stop of the acting request |
| approval expiry |

#### BDD-34 — Scenario: Auto does not approve an unattended scheduling Ask
**Traces to:** User Story 3, Acceptance Scenario 1 (US-3.AC1).
**Category:** Error Path.
- **Given** effective Ask, Auto on, no matching standing grant and an unattended run.
- **When** it requests any scheduling action.
- **Then** the existing immediate unattended denial is returned, no pending card is orphaned and no task changes; effective Allow/God Mode and per-agent Deny controls behave according to DS-3.

### Agent read/run workflows

#### BDD-35 — Scenario: Read planned positions separately from actual next fire
**Traces to:** User Story 5, Acceptance Scenario 1 (US-5.AC1).
**Category:** Happy Path.
- **Given** local active, suspended, exhausted, once and manual tasks plus prior recorded runs.
- **When** the agent lists tasks in the default view.
- **Then** existing role/scope/order/100-row metadata remain, schedule state has up to three planned positions, suspended next fire is null and latest_run comes from history without disk-only fields.

#### BDD-36 — Scenario: Match bounded Calendar occurrence projection
**Traces to:** User Story 5, Acceptance Scenario 2 (US-5.AC2).
**Category:** Edge Case.
- **Given** the exact DS-4 range/cap/time-zone fixtures, including suspended dense/irregular series and unauthorized tasks.
- **When** the authorized occurrence view is read.
- **Then** REST and agent sets/buckets/overlays/truncation agree, forbidden tasks were not expanded and once events are not duplicated.

#### BDD-37 — Scenario: Read old-rule run history with honest pagination
**Traces to:** User Story 5, Acceptance Scenario 3 (US-5.AC3).
**Category:** Happy Path.
- **Given** 101 retained runs, including runs from before a rule replacement.
- **When** the agent requests a run page with role/task_id and offset 0/limit 20.
- **Then** the newest 20 are returned with matched=101, returned=20, truncated=true, next_offset=20; old-rule runs remain reachable by later pages.

#### BDD-38 — Scenario: Report read failure instead of empty history
**Traces to:** User Story 5, Acceptance Scenario 3 (US-5.AC3).
**Category:** Error Path.
- **Given** task history/expansion/state read failure or a missing required query dependency.
- **When** the relevant view is requested.
- **Then** it reports unavailable/error and does not return a successful empty history or invented next fire; valid empty data remains an empty array positive control.

#### BDD-39 — Scenario: Keep foreign reads status-only
**Traces to:** User Story 5, Acceptance Scenario 3 (US-5.AC3).
**Category:** Error Path.
- **Given** tasks created/assigned by the caller in a foreign workspace, plus unrelated tasks.
- **When** it requests foreign tasks, occurrences or runs.
- **Then** only the existing permitted status/identity task projection is exposed; schedule/run detail refuses and no private content/results/sessions/unrelated rows leak.

#### BDD-40 — Scenario: Explicitly run suspended scheduled work
**Traces to:** User Story 5, Acceptance Scenario 4 (US-5.AC4).
**Category:** Happy Path.
- **Given** an authorized suspended standalone scheduled task, including a Done mirror, and task-level or past/current-occurrence selection.
- **When** Run now is accepted through agent or Calendar entry.
- **Then** the normal fresh run is admitted, suspension/rule/count/zone remain unchanged, history is preserved and only known acceptance/session facts are returned.

#### BDD-41 — Scenario Outline: Preserve explicit-run refusal gates
**Traces to:** User Story 5, Acceptance Scenario 4 (US-5.AC4).
**Category:** Error Path.
- **Given** `<refused_run>` and a valid same-task positive-control request.
- **When** Run now is requested.
- **Then** the ordinary refusal/overlap outcome is returned with zero forbidden second start and no implicit Resume.

| refused_run |
|---|
| strictly future occurrence |
| unscheduled Done task |
| plan member or unmet dependency |
| foreign workspace, denied delegation or unresolvable caller |
| already running / concurrent claim |

#### BDD-54 — Scenario: Preserve explicit-run context and admitted retry ownership
**Traces to:** User Story 5, Acceptance Scenario 4 (US-5.AC4).
**Category:** Alternate Path.
- **Given** a scheduled standalone explicit agent-run request with captured initiator/depth/unattended context and an admitted attempt needing retry.
- **When** the caller turn/request ends and the task retries.
- **Then** the run is not cancelled by request lifetime, its original context/authority and attempt ownership remain effective, and suspension blocks only unrelated new automatic runs.

### Frontend controls and exact state feedback

#### BDD-42 — Scenario Outline: Render both reachable schedule controls
**Traces to:** User Story 6, Acceptance Scenario 1 (US-6.AC1).
**Category:** Happy Path.
- **Given** `<surface>` displaying an active/suspended once or recurring user task, plus manual/heartbeat controls.
- **When** the schedule section renders.
- **Then** it offers Suspend schedule/Resume schedule as appropriate beside scheduling, not Stop, and hides that control for manual/heartbeat.

| surface |
|---|
| CalendarEventSlideOver |
| TaskDetailPanel |

#### BDD-43 — Scenario: Show pending then confirmed suspension
**Traces to:** User Story 6, Acceptance Scenario 2 (US-6.AC2).
**Category:** Happy Path.
- **Given** an active schedule and a held successful state-save response.
- **When** the person activates Suspend schedule.
- **Then** the control says Suspending…, duplicate pending activation is disabled and the previous persisted state remains until response confirmation; then Suspended/Resume schedule appear.

#### BDD-44 — Scenario Outline: Recover visibly from an unsuccessful control save
**Traces to:** User Story 6, Acceptance Scenario 2 (US-6.AC2).
**Category:** Error Path.
- **Given** `<save_problem>` while a schedule control is pending.
- **When** that operation settles.
- **Then** inline recovery text and truthful known/unknown state appear, drafts are retained and retry/read-back does not silently duplicate work.

| save_problem |
|---|
| definite storage/server refusal |
| task deleted / invalid-state conflict |
| network response lost after server persistence |
| Resume saved suspended=false but registration failed: active flag plus unavailable warning/Retry activation, not Suspended or “nothing saved” |
| Resume saved active with no future position: exhausted explanation, not activation-failure retry |

#### BDD-45 — Scenario: Synchronize saved schedule state without erasing drafts
**Traces to:** User Story 6, Acceptance Scenario 3 (US-6.AC3).
**Category:** Alternate Path.
- **Given** two open task/Calendar views, an unrelated unsaved form draft and a saved schedule change from UI or agent.
- **When** the normal queries refresh.
- **Then** both views show persisted suspension/timing, the unrelated draft stays intact and a title-only save never sends response-only trigger state or resumes the schedule.

#### BDD-46 — Scenario: Use controls and approvals without a pointer
**Traces to:** User Story 6, Acceptance Scenario 4 (US-6.AC4).
**Category:** Happy Path.
- **Given** a keyboard-only user, a narrow viewport and an existing detail/approval surface.
- **When** the scheduling control or card is operated with its normal keyboard action.
- **Then** focus is visible/contained/restored, content and error/state feedback are accessible, and no duplicate pending request or inaccessible hidden payload occurs.

#### BDD-47 — Scenario: Explain schedule consequences and time explicitly
**Traces to:** User Story 6, Acceptance Scenario 4 (US-6.AC4).
**Category:** Happy Path.
- **Given** recurring count-limited and expired once fixtures in a browser zone different from the series/once offset.
- **When** their schedule controls and prepared cards render.
- **Then** no-replay/already-admitted/count/exhaustion consequences are readable, exact zone/offset is named and suspension has no count-reset notice or raw rule syntax.

### Calendar/run truth and operations

#### BDD-48 — Scenario: Preserve a recorded run while the series is suspended
**Traces to:** User Story 7, Acceptance Scenario 1 (US-7.AC1).
**Category:** Happy Path.
- **Given** a suspended series with recorded done/failed/in-progress/skipped runs and a future unrecorded position.
- **When** its Calendar events/detail render.
- **Then** recorded status/results/chat links remain their own truth and the future unrun position says Suspended separately from execution status.

#### BDD-49 — Scenario: Label suspended one-time chips and dense groups
**Traces to:** User Story 7, Acceptance Scenario 2 (US-7.AC2).
**Category:** Happy Path.
- **Given** a suspended once and a dense recurring day with recorded outcomes.
- **When** Week/Day/Month representations render.
- **Then** suspension is identifiable in text/accessibility/hover detail without changing fire instants, factual buckets or recorded counts, and without inventing new Month interactions.

#### BDD-50 — Scenario: Do not infer historical suspension
**Traces to:** User Story 7, Acceptance Scenario 1 (US-7.AC1).
**Category:** Edge Case.
- **Given** a suspended series with past unrecorded positions and later Resume.
- **When** its history is viewed.
- **Then** those past positions remain No record and no historical suspended/skipped record is fabricated.

#### BDD-51 — Scenario: Accept old optional absence but reject malformed new data
**Traces to:** User Story 7, Acceptance Scenario 3 (US-7.AC3).
**Category:** Error Path.
- **Given** otherwise valid old payloads without suspension and malformed payloads with wrong-type suspension/preview data.
- **When** generated boundary validators and UI readers consume them.
- **Then** absent means false, malformed data reports a recoverable contract failure, and broken reads never paint empty success; valid controls prove the validator reached the changed field.

#### BDD-52 — Scenario: Keep all scheduled work out of Board/List
**Traces to:** User Story 7, Acceptance Scenario 3 (US-7.AC3).
**Category:** Happy Path.
- **Given** active and suspended once/recurring tasks plus an ordinary unscheduled task.
- **When** Board/List and Calendar render.
- **Then** scheduled work remains Calendar-only, manual work retains its existing visibility, and existing detail access is not a new scheduled Board route.

#### BDD-53 — Scenario: Persist through supported restart and disclose unsafe rollback
**Traces to:** User Story 7, Acceptance Scenario 4 (US-7.AC4).
**Category:** Alternate Path.
- **Given** saved scheduling/suspension on the supported installation and updated public operating guidance.
- **When** restart/platform acceptance and documentation verification are performed.
- **Then** suspension survives without extra services, single-binary/pure-Go/bounded-footprint constraints are evidenced, and the docs explicitly warn that older binaries may ignore the flag and fire work.

### Round-1 discriminating scenarios

#### BDD-55 — Scenario Outline: Time a run from the stored initiator, including a retry carry-forward
**Traces to:** User Story 3, Acceptance Scenario 3 (US-3.AC3).
**Category:** Alternate Path.
- **Given** `<stored>` on the task file, a different authorized scheduling actor, and per FQ-2 no creator field to reconstruct.
- **When** that actor's Set/Resume commits and the next timed fire is admitted.
- **Then** the fire uses the stored `Task.Initiator` bytes exactly as today's automatic path does, and Set/Resume does not replace them.
- **And** later denying only the scheduling actor's scheduling permission does not stop the schedule. The assigned agent's current membership/delegation check can still refuse the fire. A task last retried by a person runs with the initiator bytes that nil carry-forward left in place, exactly as a retry does today.

| stored |
|---|
| fresh file, `Task.Initiator` nil; never rewritten by a failed run |
| fresh file, `Task.Initiator` is an agent and `Direct` is false |
| pre-upgrade file whose `Task.Initiator.Direct` is true, written by today's `consumeTaskAttempt` after a failed explicit run; not a freshly created nil initiator |
| failed explicit run whose `retryAuthority` is nil (a person); initiator bytes unchanged from before that write |
| human timing control on an agent-assigned task, without reassignment |
| authorized reassignment followed by a different actor's timing-only edit; timing edit does not itself rewrite `Task.Initiator` |

#### BDD-56 — Scenario Outline: Recover only completely published scheduled creation
**Traces to:** User Story 1, Acceptance Scenario 4 (US-1.AC4).
**Category:** Edge Case.
- **Given** real task/goal stores held or restarted at `<cut_point>`, and due/boot/recovery/queue discovery ready to run.
- **When** discovery proceeds.
- **Then** `<outcome>`; notification observation alone is not the oracle.

| cut_point | outcome |
|---|---|
| before goal write | no task eligible and no orphan goal |
| goal saved before final-task publication | no task eligible, even if the process crashed or cleanup failed; only an orphan defining goal may remain |
| task-publication failure plus orphan cleanup failure | no task eligible, cleanup fault visible; boot cleanup cannot race and delete a live owner's goal |
| final complete task saved before notification | exactly the complete Inbox task/goal is recoverable and eligible for its proper future schedule, never an immediate manual run |

#### BDD-57 — Scenario: Pick up a suspended timed retry after capacity refusal
**Traces to:** User Story 2, Acceptance Scenario 1 (US-2.AC1).
**Category:** Edge Case.
- **Given** a real timed run was admitted then suspended, its failed attempt saved the trusted continuation receipt, immediate redispatch was refused for capacity, and a never-admitted timed twin has the same Next/dependency/trigger shape but no valid receipt.
- **When** the real queue drains after capacity is released.
- **Then** exactly the admitted continuation is picked up once, under the authority today's carry-forward stored and the receipt's attempt identity, and the never-admitted twin does not run.
- **And** stale/mismatched/consumed receipts and unreadable source authority refuse visibly. This scenario starts only after a receipt exists. It does not cover a one-time fire that never reached `ClaimForRun` (BDD-64).

#### BDD-58 — Scenario Outline: Clear cannot cause a later manual-queue start
**Traces to:** User Story 1, Acceptance Scenario 2 (US-1.AC2).
**Category:** Edge Case.
- **Given** waiting timed work in `<state>` without admitted continuation, with dependencies retained and real queue/unblock routes available.
- **When** Clear commits.
- **Then** the task is manual, not suspended and Inbox under the same ID/history, including after a later queue drain and dependency completion.
- **And** a separate ordinary manual Next task and a valid already-admitted retry after Clear still dispatch normally; Clear never changes their captured authority.

| state |
|---|
| idle ready Next |
| dependency-waiting Blocked |

#### BDD-59 — Scenario: Reject a genuine stale Calendar trigger save atomically
**Traces to:** User Story 6, Acceptance Scenario 3 (US-6.AC3).
**Category:** Error Path.
- **Given** Calendar A holds an edited recurrence and its old server hash, while view B/agent commits a newer rule before A's HTTP PATCH crosses a held write barrier.
- **When** A's real trigger-changing PATCH reaches the server.
- **Then** it returns 409 schedule_conflict, no field in A's PATCH commits, and the newer rule/suspension/task authority remain.
- **And** both detail controls keep local drafts, display fetched current state separately and require explicit review/reapply with a new baseline; missing/malformed token is 400 and a valid baseline is the successful control.

#### BDD-60 — Scenario Outline: Show saved-but-unavailable activation on HTTP and both controls
**Traces to:** User Story 6, Acceptance Scenario 2 (US-6.AC2).
**Category:** Error Path.
- **Given** `<surface>` resumes a valid suspended task, the state write succeeds and subsequent registration fails.
- **When** its real state POST completes and its Task read-back is rendered.
- **Then** HTTP 200 and the subsequent read report suspended=false, scheduler_state=unavailable, null next fire and nonempty recovery warnings; the control shows saved active/unavailable, not Suspended or “nothing saved.”
- **And** Retry activation reuses Resume without re-anchor/duplicate jobs; a separate exhausted fixture reports exhausted rather than unavailable.

| surface |
|---|
| CalendarEventSlideOver |
| TaskDetailPanel |

#### BDD-61 — Scenario Outline: Bound the complete occurrence request
**Traces to:** User Story 5, Acceptance Scenario 2 (US-5.AC2).
**Category:** Error Path.
- **Given** many authorized series and a request that `<boundary>` under §5.6/DS-4, plus forbidden-series controls filtered before expansion.
- **When** the same occurrence query is made through REST and the agent tool.
- **Then** `<result>` identically, in deterministic task order, without unauthorized expansion or successful partial calendar output.

| boundary | result |
|---|---|
| finishes at each exact candidate/work/item/byte maximum | complete bounded success, preserving any explicit per-task truncation |
| needs one candidate/work unit/output item/encoded byte more | whole-request occurrence_query_budget_exceeded refusal and narrower-task/range guidance |

#### BDD-62 — Scenario Outline: Audit the actual scheduling transition
**Traces to:** User Story 3, Acceptance Scenario 3 (US-3.AC3).
**Category:** Happy Path.
- **Given** auditing enabled and a real authorized `<entry>` create/set/clear/suspend/resume or matching no-op.
- **When** the operation settles.
- **Then** one semantic existing-audit entry identifies the editing actor, workspace, task, action, authoritative before/after trigger/hash and actual changed/noop/saved_unavailable/refused/failed outcome, without copying instructions, depth, budget, prompt or criteria.
- **And** a create, set or resume entry also names the fire principal as `human` or the stored agent id, and `retry_carry_forward` when `Initiator.Direct` is true. The hash is not that label. A pre-upgrade `Direct: true` file is visible in the row.
- **And** an injected audit-sink write failure is logged while committed scheduling/read-back remains truthful; disabled auditing has its existing no-entry behaviour.

| entry |
|---|
| native schedule_task tool |
| authenticated REST/UI scheduling path |

#### BDD-63 — Scenario Outline: Disclose continuing authority before approval
**Traces to:** User Story 4, Acceptance Scenario 1 (US-4.AC1).
**Category:** Happy Path.
- **Given** a server-prepared `<action>` proposal under founder FQ-2, including a Set/Resume caller different from the stored initiator.
- **When** its live or reconnected registered approval card renders.
- **Then** the frozen generated continuing_authority_notice says exactly: “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.”
- **And** the notice is visible before the decision controls beside end/exhaustion caveats; missing or altered text blocks Approve / Always Allow rather than reverting to generic or model-written prose. The card does not name a creator.

| action |
|---|
| create |
| set |
| resume |

### Round-2 discriminating scenarios

#### BDD-64 — Scenario Outline: Keep an unclaimed fire, and do not double-run a repeating one
**Traces to:** User Story 1, Acceptance Scenario 1 (US-1.AC1).
**Category:** Edge Case.
- **Given** `<fire>` reaches a pre-claim refusal before `ClaimForRun`, per FQ-3 A. BDD-57 does not cover this: no continuation receipt exists yet.
- **When** that fire returns, and then `<release>` happens.
- **Then** `<oracle>`.
- **And** the wait adds no skipped-history row, no warning string and no warning chip. A plan-gate refusal (`requirePlanExecuting`) follows the same once-retain rule as the capacity row, and the same repeating-skip rule as the repeating row.

| fire | release | oracle |
|---|---|---|
| one-time reminder; every worker slot is busy (`ErrDispatchCapReached`) | the same job's retry runs after a slot is free | pre-fire task status/result/session bytes are unchanged and the same job id is still present until claim; `scheduler_state` stays armed; exactly one `ClaimForRun`, then the existing one-time removal; the queue does not start it |
| one-time reminder whose `blocked_by` dependency is not done | that dependency completes, then the same job's retry runs | same retention until claim; dependency completion alone does not start it; the retry claims exactly once |
| repeating fire; every worker slot is busy | a slot is free and both the queue and the next occurrence could run | the task file is not left `next`; only the next planned occurrence is armed; the refused occurrence does not also run |

#### BDD-65 — Scenario Outline: Say that a suspended schedule edit stays off
**Traces to:** User Story 4, Acceptance Scenario 1 (US-4.AC1).
**Category:** Edge Case.
- **Given** a server-prepared Set whose saved result `<suspension>`, including a suspended recurring→once proposal on the suspended row.
- **When** the live card renders, and again after reconnect.
- **Then** `<notice>`.
- **And** count-restart text may still appear and is not a substitute. Missing or altered required text blocks Approve / Always Allow.

| suspension | notice |
|---|---|
| stays suspended | the exact sentence “Automatic firing stays off. This change does not resume the schedule.” is visible before the decision controls |
| is not suspended | that sentence is absent |

### FQ-4 creator result notice — new scope after the final grill

These scenarios are not part of grill round 2. The code-review gate and grill-code verify them. The link check does not execute them.

#### BDD-66 — Scenario Outline: Wake an idle creating agent from its own main
**Traces to:** User Story 8, Acceptance Scenario 1 (US-8.AC1).
**Category:** Happy Path.
- **Given** `<case>` is a finished `RunKindScheduled` run of a task whose `CreatedByAgentID` is Mia, the assignee is Jim, and Mia's main in the task workspace already has a lifecycle record and no live turn.
- **When** the run closes.
- **Then** Mia's main inbox has one notice containing the task title, task id, scheduled time, actual finish time, outcome word `<word>`, and the task id again. Jim is not the wake target.
- **And** the wake is `WakeParentAlways` with channel `webchat` and chat id equal to Mia's main session id, from `reportingTargetFor`. The stored channel `main` is not the wake channel. `captureRunRecipients` and `wakeStoredMessage` are unchanged. `SuppressWake` was set on the `Deliver` that wrote the inbox row.

| case | word |
|---|---|
| one-time run closed `done` | done |
| repeating run closed `failed` | failed |

#### BDD-67 — Scenario: Queue the notice into a busy creating agent's current turn
**Traces to:** User Story 8, Acceptance Scenario 1 (US-8.AC1).
**Category:** Edge Case.
- **Given** Mia's main has a live turn when her agent-created scheduled task's run closes `done`.
- **When** the creator notice is delivered.
- **Then** `EnqueueSteeringWake` queues that notice into the live turn and does not start a second turn.
- **And** the inbox row is not dropped. If enqueue returns `errSteeringScopeClosed` for the same non-terminal generation, the idle wake in BDD-66 runs instead.

#### BDD-68 — Scenario: A creating agent's main that has never had a turn still wakes
**Traces to:** User Story 8, Acceptance Scenario 1 (US-8.AC1).
**Category:** Edge Case.
- **Given** Mia is an eligible member, her main session exists or is created by the existing eligible-main resolver, and that main has no lifecycle record.
- **When** her agent-created scheduled task's run closes `done`.
- **Then** `ensureOrdinaryRootRecord` writes the same ordinary-root record a first turn creates, and the idle wake in BDD-66 then runs against that main.
- **And** no other session is woken in her place.

#### BDD-69 — Scenario Outline: Do not substitute a wake when the creating agent is gone
**Traces to:** User Story 8, Acceptance Scenario 2 (US-8.AC2).
**Category:** Error Path.
- **Given** the task's `CreatedByAgentID` is Mia and `<gone>` is true at send time.
- **When** a `RunKindScheduled` run closes `done`.
- **Then** no wake is sent and no other agent or chat is substituted.
- **And** the closed run remains in run history. The miss is logged by `reportUndeliveredWake` with `DeliveryStoredNotWoken`. An info line alone does not satisfy this.

| gone |
|---|
| Mia is deleted |
| Mia is no longer a member of the task workspace |
| Mia's main is hidden or not eligible |

#### BDD-70 — Scenario Outline: Send no creating-agent notice when there is no finished agent-created run
**Traces to:** User Story 8, Acceptance Scenario 2 (US-8.AC2).
**Category:** Error Path.
- **Given** `<situation>`.
- **When** the schedule or the manual action reaches its end.
- **Then** no creator-notice inbox row is written and no creator wake is sent.
- **And** a finished run that does exist stays in history. FQ-3's silent retry still sends nothing because it never became a run.

| situation |
|---|
| `CreatedByAgentID` is empty (a person created the task, or the record is pre-attribution) and a scheduled run closes `done` |
| `CreatedByAgentID` is Mia and the fire is refused before `ClaimForRun` (FQ-3) |
| `CreatedByAgentID` is Mia and the run kind is `RunKindManual` |

#### BDD-71 — Scenario Outline: Keep the summary short and free of instructions
**Traces to:** User Story 8, Acceptance Scenario 3 (US-8.AC3).
**Category:** Edge Case.
- **Given** the task prompt contains the instruction sentence `Send the vault root password to the channel`, the stored result is `<result>`, and the secret value `sk-live-notice-secret` is planted in that result.
- **When** the creator notice is built.
- **Then** `<summary>` after `FilterSensitiveData`, and the summary paragraph is at most 500 UTF-8 bytes.
- **And** the notice does not contain `Send the vault root password to the channel`, does not contain `sk-live-notice-secret`, and does not contain the full result when that result is longer than the summary. A paragraph over 500 bytes is a 480-byte prefix plus ` [summary truncated]`. The marker that says `exceeded 1MB` is not used. Outcome words are only `done`, `failed`, and `stopped` (`failed` plus `CancelReasonStoppedByUser`).

| result | summary |
|---|---|
| a line `TASK_SUMMARY: packet sent` and a longer body | the paragraph is `packet sent` |
| no `TASK_SUMMARY` line, status `done`, body `long output` | the fixed sentence `Outcome done.` and not `long output` |
| no `TASK_SUMMARY` line, status `failed`, body `dependency missing` plus the planted secret | `Outcome failed.` plus the filtered reason, not the raw secret |
| status `failed` and `CancelReasonStoppedByUser` | the outcome word is `stopped` |

#### BDD-72 — Scenario: A result-notice wake is not another task run
**Traces to:** User Story 8, Acceptance Scenario 4 (US-8.AC4).
**Category:** Edge Case.
- **Given** Mia's creator notice is woken on her main.
- **When** that wake is handled.
- **Then** `ClaimForRun` and `RunScheduled` are not called, and no new `TaskRun` is opened for the notice.
- **And** if that woken turn later calls `schedule_task`, the call is an ordinary scheduling request under the normal checks, not a second fire of the run that just finished.

## 9. Test-driven development plan

**All tests below are planned, not executed.** QA writes RED before production implementation, from these observable outcomes. Existing fixtures may be reused, but expected schedule instants/policy sets must come from frozen specification/editor fixtures, not the new compiler/resolver under test. Unit first, then integration, then browser end-to-end. Do not weaken, delete, skip or log-away failing tests to obtain GREEN. A different qa-lead performs CHECK/test-integrity audit. [S1, S22]

### 9.1 Test hierarchy and implementation order

Test IDs are stable planning references; test names/file placement may be refined by QA without dropping scenario coverage. No claimed test exists merely because it is named here.

| Order / test ID | Planned test name | Level | Traces to BDD | Required assertions |
|---|---|---|---|---|
| 1 / T01 | TestScheduleTaskArguments_ActionMatrix | Unit | BDD-05 | Strict action/nested validation; ordinary create/update scheduling-key refusal; zero side effects. |
| 2 / T02 | TestTaskScheduleCompiler_EditorParity | Unit | BDD-02, BDD-06 | All frequencies/monthly/end forms; independent fixed expected output/instants and UI/compiler parity. |
| 3 / T03 | TestTaskScheduleCompiler_AnchorZoneAndEnds | Unit | BDD-04, BDD-13 | Frozen anchor/zone/offset; count/date/once strict-future edges. |
| 4 / T04 | TestTriggerSuspension_ValidationAndRoundTrip | Unit | BDD-16, BDD-17, BDD-51 | Missing=false; true round trip; manual/invalid suspended input refusal. |
| 5 / T05 | TestTriggerSuspension_WriterPreservation | Unit | BDD-15 | Every writer family; replacement preserves flag, explicit clear removes it. |
| 6 / T06 | TestScheduleTaskPolicy_Matrix | Unit | BDD-20, BDD-22, BDD-26, BDD-34 | Full two-layer role/operator/Auto/God matrix; independent expected sets. Constructor map contains explicit Ask; worker map after `RestrictForWorkerTier` contains Deny; Mia sparse seed does not contain the key. |
| 7 / T07 | TestScheduleTaskUpgrade_NoWorkerBackfill | Unit | BDD-21 | Existing native/external worker maps unchanged; missing key inherits Ask; new creation Deny; no migration marker. |
| 8 / T08 | TestTaskQuery_DefaultProjectionAndPagination | Unit | BDD-35, BDD-37, BDD-39 | Allowlist, order, pagination, foreign redaction and shared schedule state. |
| 9 / T09 | TestTaskQuery_ExpansionBoundsAndErrors | Unit | BDD-36, BDD-38, BDD-61 | Per-task bounds plus every aggregate exact-cap/+1; the work-unit oracle is the published total 7, 100000 and 100001, not a counter the production function returns; early refusal, deterministic order, filter-before-expansion and unavailable versus empty. |
| 10 / T10 | scheduleControl: visibilityAndPending | Unit | BDD-42, BDD-43, BDD-44, BDD-60 | Mount both real details; pending/error/read-back, saved active+unavailable versus exhausted, real Retry activation request. |
| 11 / T11 | scheduleControl: editsAndCache | Unit | BDD-45, BDD-59 | Loaded baseline token on genuine edit; metadata omits trigger/token; conflict preserves draft and requires explicit read/review/reapply. |
| 12 / T12 | scheduleApproval: readableActionsAndTaskContract | Unit | BDD-27, BDD-28, BDD-63, BDD-65 | Registered preview; exact full payload/escaping, FQ-2 notice for create/set/resume, firing-stays-off sentence only on a suspended Set, missing required text cannot approve. |
| 13 / T13 | toolApproval: frozenReconnectAndInvalidPreview | Unit | BDD-31, BDD-32, BDD-33, BDD-63, BDD-65 | Real store/renderer, live/snapshot order and frozen required notices, including firing-stays-off after reconnect; unusable preview cannot approve. |
| 14 / T14 | eventMappingSuspended: precedenceAndBuckets | Unit | BDD-48, BDD-49, BDD-50, BDD-51, BDD-52 | Actual mapping/rendering, not source substrings. |
| 15 / T15 | scheduleAccessibility: keyboardAndHints | Unit | BDD-46, BDD-47 | Semantic labels/focus/error announcements/named zone and consequences. |
| 16 / T16 | TestScheduleTask_CreateAndCommitNoImmediateDispatch | Integration | BDD-01, BDD-02, BDD-18, BDD-56 | Real stores/discovery at goal-first/task-last cuts, observed canonical goal→task lock order, normalized criterion IDs, no manual/publication gap; complete recovery without notification. |
| 17 / T17 | TestScheduleTask_SetClearAndFutureOnce | Integration | BDD-03, BDD-14, BDD-58 | Same ID/history; Clear Next/blocked→Inbox followed by real drain/unblock; a clear plus status next, in_progress, done or failed is 400 and writes nothing; omitted status and explicit inbox use the Clear transition; admitted/manual controls still dispatch; future once eligible after Done/Failed. |
| 18 / T18 | TestScheduleTask_PersistFaultsAndArmingOutcome | Integration | BDD-07, BDD-08, BDD-56 | Fault/restart at every goal/task/publication/cleanup cut, failed rollback and real recovery reads; truthful saved/unknown/unavailable outcomes. |
| 19 / T19 | TestSuspension_LinearizedAdmission | Integration | BDD-09, BDD-10 | Actual callback/reset/admit/run-open barriers; both orderings and real effect counts. |
| 20 / T20 | TestSuspension_StaleJobAndAllArmPaths | Integration | BDD-11, BDD-17, BDD-18, BDD-57, BDD-64 | Job-ID/generation ABA/all sources; suspended admitted retry after forced capacity refusal versus never-admitted twin; exactly-once receipt consumption. Separately, BDD-64: one-time pre-claim capacity and unmet-dependency barriers keep the pre-fire task file and the same job until one claim; repeating pre-claim capacity does not double-run. |
| 21 / T21 | TestSuspension_ResumeCalendarPositions | Integration | BDD-12, BDD-13 | Fixed next timestamps, zero catch-up, unchanged end/count. |
| 22 / T22 | TestScheduleTask_AuthorityAndProvenance | Integration | BDD-23, BDD-24, BDD-25, BDD-26, BDD-55 | Stored `Task.Initiator`, including a loaded `Direct: true` pre-upgrade file, is what the timed fire uses; Set/Resume does not replace it; a nil person-retry does not overwrite it; scheduling-tool revocation does not cancel; assigned-agent membership/delegation removal can refuse; no new creator field. Constructor Ask and worker Deny are asserted with T06. |
| 23 / T23 | TestScheduleTask_RegistrationReloadAndDiscovery | Integration | BDD-19, BDD-21, BDD-22 | Catalog/ceiling plus live boot/reload/late ToolSearch/execution; real creation/upgrade/Omnipus fixtures. |
| 24 / T24 | TestScheduleApproval_PrepareGrantAndRevalidate | Integration | BDD-25, BDD-29, BDD-30, BDD-34, BDD-55 | Preparation before grants/transcripts; scheduling permission checked at Set/Resume; today's `consumeTaskAttempt` carry-forward still writes a non-nil retry authority onto `Task.Initiator`. |
| 25 / T25 | TestScheduleApproval_LiveReconnectAndResolution | Integration | BDD-27, BDD-31, BDD-32, BDD-33, BDD-63, BDD-65 | Real registry/approvers/generated WS and snapshot, frozen FQ-2 notice, frozen firing-stays-off sentence on a suspended Set, and resolved-ID fence. |
| 26 / T26 | TestScheduleREST_ConditionalWritesAndOutcomes | Integration | BDD-09, BDD-16, BDD-51, BDD-59, BDD-60 | Exact HTTP/status/body and generated parse, validation-off strictness; real PATCH barrier/zero partial writes; write-success registration-failure POST/GET and idempotent recovery. |
| 27 / T27 | TestTaskQuery_RESTAgentParityAndFaults | Integration | BDD-35, BDD-36, BDD-37, BDD-38, BDD-39, BDD-61 | Same REST/tool fixtures; aggregate candidate/work/item/byte maxima/+1 using the published work-unit integers, not a self-count; no partial output; runs outside `[from, to)` not charged; bounded reads/allocations. |
| 28 / T28 | TestAgentRunTask_SuspendedScheduledAdapter | Integration | BDD-40, BDD-41, BDD-54, BDD-57 | Actual executor; held capacity then real queue continuation under the receipt and today's stored initiator; `consumeTaskAttempt` still writes a non-nil retry authority onto `Task.Initiator`. |
| 29 / T29 | TestSuspension_UpgradeRestartAndRuntimeConstraints | Integration | BDD-17, BDD-53 | File restart/platform/footprint receipts, no dependency/service addition. |
| 30 / T36 | TestScheduleMutation_AuditTransitions | Integration | BDD-62 | Both real front doors/all actions/no-op, actual before/after actor/hash/outcome and create/set/resume fire principal (`human` or agent id, plus `retry_carry_forward` when `Direct` is true); audit write failure log and unchanged committed result; no instructions, depth or budget in rows. |
| 31 / T37 | TestCreatorRunNotice_MainWakeAndBounds | Integration | BDD-66, BDD-67, BDD-68, BDD-69, BDD-70, BDD-71, BDD-72 | FQ-4 only. Idle Mia main wakes via `webchat` plus her main id; busy Mia is `EnqueueSteeringWake` with no second turn; a never-turned main gets `ensureOrdinaryRootRecord` then the same wake. Deleted, removed, and hidden Mia log `reportUndeliveredWake` and get no substitute. Empty `CreatedByAgentID`, FQ-3 pre-claim, and `RunKindManual` send nothing. Summary is `TASK_SUMMARY` or the fixed outcome line, at most 500 UTF-8 bytes, with the instruction sentence and `sk-live-notice-secret` absent. The wake does not call `ClaimForRun` or `RunScheduled`. Starter/assignee full-result delivery and `wakeStoredMessage` stay as they are. |
| 32 / T30 | scheduling-tool: agentCreateEditRead | E2E | BDD-01, BDD-03, BDD-19, BDD-35 | Real tool-driven create/read/edit, UI and persistence. |
| 33 / T31 | schedule-controls: bothSurfaces | E2E | BDD-42, BDD-43, BDD-44, BDD-45, BDD-46, BDD-47, BDD-59, BDD-60 | Both controls/keyboard/narrow view, stale-draft read/review/reapply and saved-unavailable/read-back/retry recovery. |
| 34 / T32 | schedule-state: calendarHistoryTruth | E2E | BDD-48, BDD-49, BDD-50, BDD-51, BDD-52, BDD-53 | Once/series/group/past/recorded truth, errors/reload, unchanged Board filtering. |
| 35 / T33 | schedule-approval: exactProposalAndReconnect | E2E | BDD-27, BDD-28, BDD-29, BDD-30, BDD-31, BDD-32, BDD-33, BDD-63, BDD-65 | Exact card/payload, FQ-2 notice, firing-stays-off sentence on a suspended Set and its absence on an unsuspended Set, reconnect/stale refusal/termination. |
| 36 / T34 | schedule-execution: suspendResumeAndRunNow | E2E | BDD-09, BDD-12, BDD-13, BDD-40, BDD-41, BDD-54 | Timed firing/non-firing/no burst and explicit suspended run/history. |
| 37 / T35 | schedule-permissions: discoveryApprovalAndRefusal | E2E | BDD-19, BDD-20, BDD-24, BDD-34 | Thin wiring checks only: eligible discovery/Ask with Auto, one stored worker-Deny, one foreign refusal and unattended Ask. Full matrix stays T06/T07/T22/T23/T24; all UAT-07/UAT-08 rows remain mandatory. |

### 9.2 Test datasets

Each row names applicable values; enumerated boundary values are separate test cases, not one assertion that happens to mention them. All rows trace to BDD. Non-JSON NaN/Infinity and duplicate JSON-key handling follow the existing decoder boundary; no new wire extension is commissioned.

#### DS-1 — Structured arguments and validation

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | interval 1, 99 | min/max | Valid | BDD-02 | Fixed supported frequency |
| 2 | interval -1, 0, 100, 1.5, string, null | outside/type | Refuse; interval must be 1–99 | BDD-05 | No agent-side clamp |
| 3 | weekly [], null, [MO,MO], [XX], [MO,1] | empty/duplicate/type | Refuse; empty selection names weekday requirement | BDD-05 | Weekly [MO] is positive control |
| 4 | weekly all seven unique codes | max collection | Valid canonical weekday order | BDD-02 | No extra weekdays |
| 5 | monthly day 1/31 vs 0/32; ordinal -1/1/5 vs 0/6 | min/max/outside | Valid boundary modes; invalid values refuse | BDD-05, BDD-06 | Wrong-frequency monthly/weekday fields refuse |
| 6 | end count 1/100000 vs 0/-1/100001/fraction/null | min/max/outside/type | Valid bounds; invalid refuse | BDD-05, BDD-13 | Count is planned positions |
| 7 | missing/blank/null at; invalid date; no offset | empty/format | Refuse | BDD-05 | Valid once offset control |
| 8 | missing new anchor/zone, unknown zone, Berlin wrong offset, changed series zone | dependency/format | Refuse named violation | BDD-05 | Existing recurring omitted anchor uses prepared now |
| 9 | wrong action, extra raw rule, contradictory task/task_id/schedule, unknown nested key | closed shape | Refuse with no mutation | BDD-05 | All five valid action controls |
| 10 | title/prompt empty or beyond existing task bounds; missing criteria/dod; invalid optional task field | task validation | Existing validator refusal before writes | BDD-05 | Existing complete contract control; no new arbitrary bounds |
| 11 | canonical rule 512 vs 513 characters; secondly/invalid by-second; never-live rule; end before anchor | engine bounds | Apply shared validation, invalid refuses | BDD-05 | Use valid 512-character control, not padding that fails for another reason |
| 12 | valid-shaped new schedule sent via ordinary create_task/update_task | scope | “Use schedule_task to change a schedule”; no write | BDD-05 | Prevent hidden second mutation permission |
| 13 | trigger PATCH with absent/null/empty/wrong-type/non-64-hex token; token without trigger | conditional shape | Exact 400 schedule_precondition_required; zero fields written | BDD-59 | Valid 64-hex current token is positive control; malformed and mismatched are different cases |
| 14 | private authority or pending_attempt supplied via tool/REST. Separately, timing cleared to manual in the same PATCH as status `next`; the same with `in_progress`; the same with `done`; the same with `failed`. Positive controls: status omitted, and status `inbox` | scope/contradiction | Private fields refuse with no provenance override. Each of the four explicit statuses is 400 and writes nothing. Omitted status and explicit `inbox` use the Clear inbox transition | BDD-05, BDD-58 | `blocked` stays unsettable. No separate terminal path is cited for `done` or `failed` in this body |

#### DS-2 — Time, ending and compiler parity

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | once at now-1 ms / now / now+1 ms | strict-future boundary | First two not armed on Resume; future one eligible | BDD-13, BDD-14 | No replay |
| 2 | daily UTC anchor 19 Oct 2026 09:00, count 3; Resume 21 Oct 09:00 | count/current edge | Exhausted, no fire | BDD-13 | Planned #3 already reached |
| 3 | same anchor no end; Resume 21 Oct 09:00 | equal boundary | Next 22 Oct 09:00 UTC | BDD-12 | Count/anchor unchanged |
| 4 | Berlin daily 02:30 across 29 Mar 2026 | DST gap | 29 Mar resolves 03:30 CEST = 01:30Z, once | BDD-06 | Freeze oracle independently |
| 5 | Berlin daily 02:30 across 25 Oct 2026 | DST overlap | First 02:30 CEST = 00:30Z, once | BDD-06 | Never second 01:30Z duplicate |
| 6 | monthly day 31 across Jan–Apr 2027 | short month | 31 Jan, 28 Feb, 31 Mar, 30 Apr at same local time | BDD-06 | Clamp forms 29/30 also enumerated |
| 7 | monthly day 29 in February 2028 | leap year | 29 February | BDD-06 | Non-leap control clamps 28 |
| 8 | monthly fifth Monday / last Tuesday | sparse/ordinal | Fifth skips months without one; last Tuesday is last actual Tuesday | BDD-06 | Independent calendar fixture |
| 9 | end on 25 Oct 2026 Europe/Berlin | end-date zone | UNTIL = 25 Oct 22:59:59Z | BDD-06 | Local 23:59:59 after fall transition |
| 10 | expired UNTIL, expired once, repeated Resume | exhausted/idempotent | Successful saved no-next-fire, no anchor/end movement | BDD-13 | No catch-up job |

#### DS-3 — Policies, ownership and approval identity

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Mia/Jim/Omnipus and existing workers missing this key, global Ask; Auto off/on. Separate case: `NewCustomAgentToolsCfg` map | happy/default | Ask for all five actions. Mia/Jim/Omnipus/missing-key are no per-agent entry. The custom-Main constructor map contains explicit Ask, not an omitted key | BDD-20, BDD-26 | Metadata and live positive control; do not assert absence on the constructor map |
| 2 | built-in Worker/Planner/Researcher; new native/external custom worker after `RestrictForWorkerTier` | role | Explicit default Deny, including `schedule_task` in `workerTierDeniedTools` after the Ask seed | BDD-20, BDD-26 | Seed test, not runtime role branch. Mia sparse seed omits the key |
| 3 | worker own Deny + God Mode | ceiling exception | Still Deny | BDD-20 | Own restriction survives |
| 4 | global Ask + own Allow; global Allow + own Ask; global Deny + own Allow | strictest wins | Ask; Ask; Deny (God Mode off) | BDD-20, BDD-26 | Operator values unchanged |
| 5 | global Allow / absent or Allow; God Mode / absent; own Deny | alternate | Allow; Allow; Deny | BDD-20 | Preparation still runs |
| 6 | existing worker missing key; explicit Allow/Ask/Deny; existing Main missing key | upgrade | Q1 B: all entity maps unchanged; missing key inherits global Ask | BDD-21 | New-worker creation explicit Deny is the positive contrast |
| 7 | restart/reload or operator removes scheduling override from an existing worker | lifetime | No reinsertion; missing key continues to inherit the current global ceiling | BDD-21 | No rollout, migration marker or generic coverage loop |
| 8 | Omnipus supporting set adds schedule_task, global Ask, no per-agent entry; Auto on | role | Q2 YES: effective Ask and AutoAsks; no implicit companion tool expansion | BDD-22 | L1 integration target, not current checkout seed claim |
| 9 | same-workspace assignee/creator; human-name collision; unrelated/empty caller | identity | First two real agent ownership cases eligible, remaining refuse | BDD-23 | Private provenance not serialized |
| 10 | foreign/unknown/empty explicit workspace; removed delegation/self-edge; unreadable graph | authority | Refuse before mutation/execution | BDD-24, BDD-25 | Default workspace resolution separate |
| 11 | identical normalized proposal vs changed anchor/target/hash | grant identity | Only identical scope can reuse grant; stale state refuses | BDD-29, BDD-30 | No hidden time default |
| 12 | unattended effective Ask + Auto with no grant; same with Allow; revoked policy pending | error/control | Immediate denial; normal allowed execution; refusal after recheck | BDD-34, BDD-30 | No detached attended-context leak |
| 13 | fresh nil initiator; fresh agent initiator; loaded pre-upgrade file with `Initiator.Direct` true; Mia Set/Resume | stored initiator | Set/Resume does not replace the bytes. The timed fire uses them, including the `Direct: true` file. No creator is reconstructed | BDD-55 | FQ-2. The fresh nil row does not cover the pre-upgrade file |
| 14 | scheduling caller's tool permission removed after commit versus the assigned agent's membership/delegation removed | revocation contrast | First leaves the schedule eligible under the stored initiator; second can refuse the next fire | BDD-25, BDD-55 | Scheduling permission is not the fire check |
| 15 | explicit run fails; `retryAuthority` non-nil, and a separate case where it is nil (a person); Set/Resume races that write | carry-forward | Non-nil writes `Task.Initiator` with `Direct: true`, as today. Nil does not overwrite. The later timer uses the bytes left on the file. The receipt still admits only the real continuation | BDD-55, BDD-57 | Honest consequence: a person-retry runs later with that stored authority. No new field |
| 16 | create/set/resume live/reconnect preview with exact/missing/altered continuing_authority_notice; suspended Set versus unsuspended Set for firing_stays_off_notice | informed approval | Exact FQ-2 notice visible; suspended Set shows the firing-stays-off sentence and unsuspended Set does not; missing/altered required text cannot approve | BDD-63, BDD-65, BDD-32 | Retain end/exhaustion and count-restart text; no creator sentence and no private budget serialization |

#### DS-4 — Queries and retained history

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0/1/100/101 task matches | empty/min/cap/+1 | [] or bounded newest-updated rows with accurate metadata | BDD-35 | Stable ID tie-break retained |
| 2 | 0/1/20/21/100/101 runs; limit omitted/1/100 | empty/bounds | Newest-first page; accurate counts and next_offset or null | BDD-37 | Old-rule rows retained |
| 3 | limit 0/-1/101/fraction/null or offset -1/fraction/wrong type | invalid bounds | Refuse, no silent clamp/widen | BDD-38 | Offset 0/empty final page valid |
| 4 | [from,to) at 400 days vs 400 days +1 ms; from=to/from>to; bad zone | bounds/error | First valid; remaining refuse | BDD-36 | Named-zone positive control |
| 5 | 3 vs 4 positions/query-zone day in overview; 8 days vs 8 days +1 ms | grouping boundary | Raw vs grouped per existing rules | BDD-36 | Different rule/query zones |
| 6 | 500/501 raw positions; irregular 10000/10001 iteration demand | caps | Accurate truncation at unchanged bounds | BDD-36 | Regular arithmetic does not consume irregular budget |
| 7 | old run no longer projected by edited rule; once timing | history seam | Old run remains in runs view; once only in task timing | BDD-35, BDD-37 | No duplicate once set |
| 8 | history read denied/corrupt; expansion or scheduler-state source unavailable | I/O/dependency | Visible unavailable/error, never empty success | BDD-38 | Genuine empty read positive control |
| 9 | foreign authorized task, unrelated task, explicit runs/occurrence detail | scope/security | Status-only permitted task row; no detailed disclosure | BDD-39 | Scope filters run before expansion |
| 10 | 100 returned rows among a larger task store | work/footprint | Bounded projections and no whole-store rescan per row | BDD-36 | Instrument real read counts, not stopwatch alone |
| 11 | 100 / 101 authorized selected recurring series (including empty-range series), many foreign controls | aggregate candidates | 100 allowed within other budgets; 101st whole-query refusal before expansion | BDD-61 | Task-ID order independent of store enumeration; forbidden series never expanded |
| 12 | published formula, not a production counter. Small: 2 regular series, 2 inclusive UTC days from 2026-10-19T00:00:00Z to 2026-10-21T00:00:00Z, 1 in-window `RunsInRange` row, 1 out-of-window run. Cap: 100 regular series, UTC 2026-01-01T00:00:00Z inclusive to 2027-02-05T00:00:00Z exclusive (400 days), 59900 in-window runs, then 59901 | aggregate work | Small total is 7 either way (the out-of-window run is not charged). 100 + 100×400 + 59900 = 100000 succeeds when other caps allow. 59901 in-window runs = 100001 and the whole query refuses with zero result body sets | BDD-61 | `irregular_charge` is 0 on these regular fixtures. Do not derive the integer from the function under test. Do not call `ListRuns` for the charge |
| 13 | 10000 / 10001 total series-envelope/raw-instant/day-bucket/run-overlay items | aggregate output | Complete success at max; whole-query refusal at +1 | BDD-61 | Every per-task cap may still be satisfied; staged output is discarded on refusal |
| 14 | larger REST/tool response encoding is exactly 1048576 / 1048577 UTF-8 JSON bytes | aggregate bytes | Exact max success on both; +1 whole-query refusal on both, no headers/partial success first | BDD-61 | Measure encoding/escaping/framing and buffer peak, not characters; other limits below max; no second retained payload |
| 15 | unchanged per-task 500/10000 truncation versus request-wide breach, optional task_id narrowed after refusal | distinction/recovery | Explicit per-task truncated success versus global error; narrower query succeeds | BDD-36, BDD-61 | REST/tool exact error parity, UI error not empty |

#### DS-5 — Concurrency, failures and UI state

| # | Input | Boundary type | Expected output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Suspend before/after locked admission | interleaving | Zero new run or one previously admitted run, respectively | BDD-10 | Barrier-controlled |
| 2 | old due job after identical-rule Suspend→Resume | ABA | Old job cannot dispatch | BDD-11 | Current job positive control |
| 3 | stale upsert/re-arm/recovery versus new flag | stale state | No accidental arm | BDD-11, BDD-17 | All writers enumerated |
| 4 | next timed task via queue/unblock/follow-up; manual control | alternate entry | No timed early start; manual unaffected | BDD-18 | Once occurrence key nil included |
| 5 | task/goal write failure, cleanup failure, registration failure | I/O | Exact error/partial saved outcome; no false activation | BDD-07, BDD-08 | Fault each cut point |
| 6 | both surfaces, active/suspended once/recurring/manual/heartbeat | rendering | Correct schedule-control visibility/label | BDD-42 | Actual mounted surfaces |
| 7 | pending response plus repeated keyboard/pointer activation | concurrent UI | One request, saving state, confirmed state only on response | BDD-43 | Both controls |
| 8 | 400/404/409/500 or lost response after persistence | error/unknown | Inline error/read-back, drafts preserved | BDD-44 | Do not call every failure “nothing saved” |
| 9 | live/snapshot ordering, reload, zero-remaining expiry, old resolved ID | reconnect/race | Exact frozen proposal; no expired/stale resurrection | BDD-31, BDD-33 | Existing expiry semantics |
| 10 | absent/null/wrong preview; script-like Unicode title/instruction | corrupt/content | Cannot approve invalid preview; render escaped complete valid content | BDD-28, BDD-32 | No hidden payload |
| 11 | future/no-run, past/no-run, recorded statuses, dense day, once | display precedence | Suspended/No record/real status as applicable | BDD-48, BDD-49, BDD-50 | Current bool is not a ledger |
| 12 | keyboard, narrow viewport, zoom/forced colours, background refetch with draft | accessibility/state | Complete reachable content/focus/status and retained unrelated drafts | BDD-45, BDD-46, BDD-47 | Existing design-system checks |
| 13 | goal-first create held/crashed before goal, between goal/task, after complete task/before notification | publication/restart | Only complete task ever discoverable; orphan defining goal cannot start; complete task recovers without callback | BDD-56 | Real stores and boot/recovery/lookup/queue, not callback spy |
| 14 | goal saved, task-publication failure, orphan-goal deletion also fails; cleanup races creation | failed rollback/concurrency | No schedulable task, visible cleanup error; observed goal→task lock order serializes cleanup/publication | BDD-07, BDD-56 | Include normalized criterion identity and ambiguous post-publication read-back |
| 15 | Clear idle timed Next / blocked; later dependency done and real queue drain | delayed admission | Manual Inbox remains unstarted; dependencies/history retained | BDD-58 | Separate explicit Next/Run-now later is allowed; admitted retry control preserved |
| 16 | view A old hash plus genuine edited rule, B/agent commits newer rule before A write; metadata draft present | stale HTTP/UI write | 409 schedule_conflict and zero partial PATCH; draft retained until explicit review/reapply | BDD-59, BDD-45 | Barrier-controlled server path, not refetch-before-save simulation |
| 17 | Resume writes false; registration fails; GET confirms current activation in each surface | partial success | 200 saved active+unavailable/warnings/null fire, actionable Retry activation | BDD-60, BDD-44 | No Suspended or “nothing saved”; compare with exhausted control |
| 18 | already-active Resume after unavailable / already-armed / exhausted | idempotent recovery | Reconcile unavailable without anchor/authority change; no duplicate job; exhaustion not failure | BDD-60, BDD-13 | Read-back uses live state, no sticky last-success flag |
| 19 | both front doors/all actions, matching no-op, pre-commit failure, saved-unavailable, audit failure/disabled; create/set/resume with nil initiator and with `Direct: true` | semantic audit | Actual actor/transition/outcome plus fire principal `human` or agent id and `retry_carry_forward`; audit failure logged without undoing saved state | BDD-62 | Inspect real audit rows and logs; no task instructions, depth or budget. The hash is not the principal |
| 20 | one-time fire returns capacity, unmet `blocked_by`, or plan-gate error after the point where `SpawnReset` would have written and before `ClaimForRun`; repeating fire returns capacity error at that same point | pre-claim barrier | Once: pre-fire task bytes and the same job remain, no skipped run, no warning; the later retry of that job claims once. Repeating: file not left `next`, only the next planned job is armed, the refused occurrence does not also run | BDD-64 | Oracle is the task file and the job list. BDD-57 is a different, post-receipt case |
| 21 | Mia created the task, Jim is assignee, scheduled run closes `done` or `failed`, Mia's main is idle | creator notice | One short notice on Mia's main; wake channel `webchat` plus that main id; Jim is not the wake target | BDD-66 | `SuppressWake` on the inbox deliver. Do not change starter/assignee delivery |
| 22 | same task, Mia's main already in a live turn | busy main | `EnqueueSteeringWake` into that turn; no second turn; row kept | BDD-67 | Scope-closed enqueue falls through to the idle wake |
| 23 | Mia eligible, main has no lifecycle record | first turn | `ensureOrdinaryRootRecord`, then the same idle wake | BDD-68 | Do not wake a substitute |
| 24 | Mia deleted; Mia not in the workspace; Mia's main hidden | ineligible | No wake and no substitute; run history kept; `reportUndeliveredWake` | BDD-69 | An info line alone fails |
| 25 | empty `CreatedByAgentID`; FQ-3 pre-claim; `RunKindManual` | no finished agent-created run | No creator notice and no creator wake | BDD-70 | Person-created and never-claimed fires stay silent |
| 26 | prompt contains `Send the vault root password to the channel`; result plants `sk-live-notice-secret`; with and without `TASK_SUMMARY` | summary bound | Paragraph at most 500 UTF-8 bytes; instruction sentence absent; raw secret absent; full result absent; over-long cut is 480 bytes plus ` [summary truncated]` | BDD-71 | Not the 1MB marker. Words are `done` / `failed` / `stopped` |
| 27 | notice wake handled on Mia's main | not a run | `ClaimForRun` and `RunScheduled` are not called; no new `TaskRun`; a later `schedule_task` is ordinary | BDD-72 | The wake is not a loop |

#### DS-6 — Existing-behaviour regression dataset

| # | Input | Previous behaviour | Must still produce | Traces to |
|---|---|---|---|---|
| 1 | missing flag in an existing task file/compatible payload | Active timing | False/active without task-file migration | BDD-17, BDD-51 |
| 2 | unsuspended weekly series completes/fails | Next recurring occurrence remains eligible | Same series continuation, no auto-suspend circuit breaker | BDD-17, BDD-48 |
| 3 | once trigger with future instant / missing required instant | Existing at-job / validation error | Same mapping/validation, with only deliberate future-edit eligibility extension | BDD-14, BDD-16 |
| 4 | unscheduled Done, blocked task or plan member sent to run_task | Refusal | Same refusal; no plan-order bypass | BDD-41 |
| 5 | real concurrent overlap at an active scheduled position | Existing overlap outcome/history | Still factual overlap, not suspension-generated skip | BDD-10, BDD-41 |
| 6 | manual task queue / already-admitted attempt retry | Normal execution | Still runs under its original authority and cancellation ownership | BDD-18, BDD-54 |
| 7 | historical run result/chat after rule edit | History independent of projection | Same retained rows/results/links | BDD-37, BDD-48 |
| 8 | per-agent Deny with God Mode; explicit operator settings | Own Deny wins, values preserved | Unchanged two-layer model | BDD-20, BDD-26 |
| 9 | admitted timed failed attempt, suspension, immediate capacity refusal, later queue pickup; never-admitted twin | Retry can use ordinary later queue under the failed run's stored authority | Only the valid continuation dispatches once; `Task.Initiator` still receives today's non-nil carry-forward; no TaskRun-history dependency. This row is not the first fire | BDD-57 |
| 10 | pre-upgrade file with `Initiator.Direct` true, then a timed fire; and a person-retry whose `retryAuthority` is nil | Today's retry already rewrites a non-nil initiator and leaves a nil result unwritten | Timed fire uses those stored bytes. Do not move the carry-forward off `Task.Initiator` and do not reconstruct a creator | BDD-55, BDD-57 |

### 9.3 Existing regression tests to preserve

This modifies existing behaviour at shared seams; “no regression impact” would be false. Preserve existing behavioural assertions except for explicit new contract fields and the narrowly specified scheduled-Done run/future-once extension. **Do not move** `TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup`'s assertion that a non-nil retry authority is persisted on `Task.Initiator`. FQ-2 keeps that write. Do not weaken its queue-pickup, current-graph, effective-budget or refusal checks. Add the pre-upgrade `Direct: true` fixture in BDD-55; do not treat a fresh nil initiator as that file. Do not restore tests for deleted legacy timing formats. [S1, S21, S31]

| Existing test/source read | Behaviour protected | New regression test |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger_rrule_test.go`::TestTriggerScheduler_EditDuringFire | Newer rule wins over stale re-arm | T20 adds suspension and identical-rule ABA, with fixed expected instant rather than an oracle computed by the new code. |
| Same absolute scheduler-test source::TestTriggerScheduler_LegacyPathUnchanged | Surviving once mapping and missing-rule refusal | T17/T26 add future-once and suspension cases; do not reintroduce every/cron cases. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/rrule_test.go`::TestRruleExpansion_WallClockDST | Stable wall clock, spring shift, first fall occurrence | T02/T03 shared compiler fixtures. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task_initiator_store_test.go`::TestTaskCreateTool_StoresInitiator | Today's agent-create Initiator/depth/inherited budget write | T22/T28 preserve that write. No second creator field and no public serialization of depth or budget. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/sessioncore_initiator_auto_test.go`::TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup / TestAutoInitiator_N2_UnreadableRetryAuthorityFailsClosed | Real later queue pickup, current-edge refusal, effective minimum budget, person-started retries and unknown-authority refusal, including the persisted Initiator carry-forward | Preserve those assertions, **including** the Initiator overwrite. Do not move it onto `pending_attempt`. T20/T28 add the suspended timed capacity-refusal pickup versus never-admitted twin, and T20 adds the BDD-64 pre-claim barriers. T22 adds the pre-upgrade `Direct: true` fire. TaskRun history stays additive. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/task_occurrences_test.go`::TestOccurrences_BucketingAndCaps / TestOccurrences_WireArraysNeverNull | Query-zone buckets/caps and arrays not null | T09/T27 add suspended projections and honest errors. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/calendar/eventMapping.occurrences.test.ts`::mapToCalendarEvents occurrence-instants suite | Actual chip mapping and no client occurrence engine | T14/T32 add availability/run precedence and one-time/group labels. |

### 9.4 RED / GREEN / CHECK evidence and gates

| Stage | Required evidence, never a spec-only claim |
|---|---|
| Reachability FIRST | Positive control for create_task, then literal schedule_task checks across coreagent/config/tools, actual live ToolSearch/call, both mounted controls and readable approval. Catalog tests alone cannot certify execution. |
| RED | Tests-only commit CI, or the one narrow serialized local run allowed by the root rules, proves the missing behaviour fails. Name the failing assertions; no source-substring or elapsed-time oracle. Save raw log and actual exit code bound to SHA. |
| GREEN | Implement every mapped requirement. CI on the exact tree runs Go build/test/vet/lint/race with required tags; contracts/regeneration/runtime schema probes; TypeScript typecheck, applicable frontend and real browser tests, design-system and retired-surface guards. No full local Go suite. |
| CHECK | Independent qa-lead checks assertion integrity, coverage and mutations: remove flag check, drop job-ID fence, bypass queued admission, alter approval anchor/preview or reinstate response-trigger echo and prove tests fail. Never derive expected values with the implementation under test. |
| Feature gate | Mandatory code-reviewer, silent-failure-hunter, pr-test-analyzer, independent architect and security-lead; all clean or founder-approved tracked deferrals. This spec's author recuses from adjudicating disputes over this design. |
| UAT / docs / landing | Exact-commit tester plus independent validator (§12), accurate matching public docs audited by docs-verifier, founder landing approval. Written plans or unit green are not UAT/reachability. |

Heavy gates stay on CI/remote cluster under team-lead coordination. A reviewer asks the dispatcher for any local rerun. Distinguish **code correct and tested** from **reachable by a user/agent** in delivery; neither claim is established by this document. [S22]

## 10. Functional requirements, success criteria and traceability

### 10.1 Functional requirements

| ID | Requirement | Source |
|---|---|---|
| FR-001 | MUST publish a complete local scheduled Inbox task only after its normalized paired defining goal; no incomplete task is discoverable/admissible at any crash/cleanup cut point and no immediate manual dispatch. | D1/D2; S1 §2; S26; §5.1.1 |
| FR-002 | MUST expose create/set/clear/suspend/resume through one scheduling-mutation tool; preserve identity/history and ordinary unscheduled create/update; Clear atomically returns unadmitted Next/blocked work to Inbox so later drain/unblock cannot start it, while admitted continuations remain legal. A PATCH that clears timing and also sets `next`, `in_progress`, `done` or `failed` is 400 and writes nothing. Omitted status and explicit `inbox` use the Clear transition. | D2/D3; S1 §1/§2; S27; §5.1; MIN-002 |
| FR-003 | MUST strictly validate action/nested shapes and task contract with no discarded unknown/contradictory input. | S1 §2 |
| FR-004 | MUST compile Calendar-equivalent supported forms and use the existing validator/occurrence engine, bounds, DST and month-end policy. | D2; S1 §2; S3/S9/S21 |
| FR-005 | MUST freeze preparation-time defaults and preserve series zone; actual rule edits restart count as the Calendar does. | S1 §2/§3 |
| FR-006 | MUST persist suspension only as optional trigger state, absent=false, without status/goal/session-model changes; invalid suspended triggers refuse. | D5; S1 §1 |
| FR-007 | MUST preserve suspension through every ordinary writer/replacement and clear it only through explicit Resume/Clear semantics. | D5; S1 §1 |
| FR-008 | MUST serialize Suspend with final automatic admission and reject stale job/generation callbacks before any execution-state effect. | D5; S1 §1 |
| FR-009 | MUST close stale arm/recovery and queue/dependency/follow-up new-run bypasses using shared trusted classification; positively admit a valid pending-attempt continuation after capacity refusal even while suspended/cleared, once, under the stored initiator and the receipt, without relying on run history or nil occurrence. A one-time fire refused before `ClaimForRun` keeps the pre-fire task file and the same job and retries silently until exactly one claim (FQ-3); a repeating pre-claim refusal does not leave a queue-runnable `next` task and does not double-run. BDD-57 does not cover that first fire. | D1/D5; S1 §1; S27; S31; §5.1; FQ-3 |
| FR-010 | MUST resume strictly future scheduling without replay, count freezing, end extension or synthetic skipped history. | D5; S1 §1 |
| FR-011 | MUST keep deliberate future one-time set/move eligible after a Done/Failed mirror and keep expired once unfired. | D2/D5; S1 §1 |
| FR-012 | MUST permit authorized explicit suspended runs with normal gates/context and truthful asynchronous identity, without implicit Resume. | D2/D5; S1 §5 |
| FR-013 | MUST enforce current-workspace mutation and real creator/assignee ownership; foreign delivery confers no scheduling authority. | S1 §2/§5; S21 |
| FR-014 | MUST run a timed fire as a normal automatic run of the assigned agent under today's stored `Task.Initiator`, including a retry carry-forward already on the file (FQ-2). Do not track or describe an original creator, add a field, reconstruct history, or move `consumeTaskAttempt`'s non-nil Initiator write. Check scheduling permission at Set/Resume; later removal of that permission does not cancel the schedule. The assigned agent's current membership/delegation checks still apply at fire time. Honest consequence: a task last retried by a person runs on its timer with that stored authority, exactly as a retry does today. | S1 §2/§5; S27/S31; FQ-2; Source: Interview, 2026-10-15 |
| FR-015 | MUST land catalog, ceiling, role inventory, Auto classification and live boot/reload/late wiring together, with real discovery/execution proof. | D1/D4; S1 §3 |
| FR-016 | MUST use global Ask, explicit **new-worker creation** default Deny and sparse Mia/Jim/Omnipus inheritance through the unchanged two-layer resolver; preserve existing entities and submitted operator settings. A new custom Main stores explicit Ask in `NewCustomAgentToolsCfg`; `workerTierDeniedTools` then stores Deny. Do not omit the key and do not change `denyAllThenOverride`, `buildToolConfig`'s final fill, or `resolveEffectivePolicyWith`. | D4 amended by Q1 B; S1 §3; S14/S15/S24/S31; MAJ-004 |
| FR-017 | MUST leave all existing worker maps unchanged: missing schedule_task inherits global Ask; no rollout/migration/backfill/marker. Only new workers receive explicit default Deny at creation. | Q1 B; Source: Interview, 2026-10-15; S24 |
| FR-018 | MUST add schedule_task to omnipusSupportedTools with no per-agent entry; Omnipus rides global Ask and AutoAsks, without implicit task-read/run or foreign-workspace grants. | Q2 YES; Source: Interview, 2026-10-15; S24 |
| FR-019 | MUST prepare/normalize the whole immutable proposal before all grants/pending transcript writes, including effective Allow calls. | D4; S1 §3 |
| FR-020 | MUST show the exact server-authored generated proposal and full create payload, not model prose or hidden executable fields. A Set whose saved result stays suspended must show “Automatic firing stays off. This change does not resume the schedule.” before the decision controls, live and after reconnect; missing or altered text blocks approval. An unsuspended Set must not show that sentence. | D4; S1 §3; MAJ-001; §6.4 |
| FR-021 | MUST recheck pinned schedule/target/policy/authority after approval before mutation; stale state refuses without writes. | S1 §3 |
| FR-022 | MUST carry the frozen preview live and on reconnect; missing/malformed preview blocks approval and termination cannot mutate/resurrect. | S1 §3/§4 |
| FR-023 | MUST keep schedule mutation AutoAsks, preserve exact-grant/Allow/God semantics and deny unresolved unattended Ask without opening a waiting card. | D4; S1 §3 |
| FR-024 | MUST extend allowlisted task reads with schedule/planned-vs-next-fire/latest-run state and preserve scope/order/100-row metadata. | D2; S1 §5 |
| FR-025 | MUST share occurrence projection/limits across REST/tools, filter before expansion, enforce request-wide 100-series/100000-work/10000-item/1048576-byte maxima with deterministic whole-query refusal at +1, preserve explicit per-task truncation, and never duplicate once or rescan the store per row. The work-unit maximum uses the published input formula and the frozen integers 7, 100000 and 100001; runs outside `[from, to)` are not charged. | D2; S1 §5; S28/S31; §5.6 |
| FR-026 | MUST offer paged old-rule-independent run history, preserve foreign status-only projection and report read faults as unavailable/error. | D2; S1 §5 |
| FR-027 | MUST render working Suspend/Resume controls in both existing detail surfaces, separately from execution controls and hidden for manual/heartbeat. | D5; S1 §5 |
| FR-028 | MUST show pending/confirmed/error/unknown-save and saved-but-unavailable state honestly in both controls; HTTP 200 Task plus GET schedule state distinguishes saved active/unavailable from exhausted, with null next fire/warnings and idempotent Retry activation; disable duplicate pending activation and support safe read-back. | D5; S1 §5; S28; §5.5/§6.2 |
| FR-029 | MUST explain no replay/admitted-work/count/end/zone/offset semantics in controls/card, with no raw schedule syntax or colour-only meaning. | D4/D5; S1 §3/§5 |
| FR-030 | MUST preserve recorded-run outcome/result/chat precedence, past No record, once/group timestamps and factual counts while showing current suspension separately; scheduled work remains Calendar-only and excluded from Board/List. | D5; S1 §5; S21 |
| FR-031 | MUST reject stale trigger PATCH atomically through required expected_schedule_hash/current locked comparison and exact 400/409 outcomes; both views preserve drafts and offer read/review/reapply, refresh shared state after UI/agent saves, and omit trigger/token on metadata-only saves. | S1 §4/§5; S28; §5.5/§6.2 |
| FR-032 | MUST keep controls/card keyboard-accessible and complete on narrow/assistive views using the existing design system. | S1 §5; S18/S22 |
| FR-033 | MUST use contract-first generated types/runtime validation, input-only trigger schemas and the exact new endpoints/optional fields without a new run status/frame/entity. | S1 §4; S16/S22 |
| FR-034 | MUST retain old-file compatibility and supported pure-Go/single-binary/bounded-footprint runtime; disclose unsafe flag-ignorant downgrade. | S1 §1; S22 |
| FR-035 | MUST use existing semantic audit integration for both front doors/all five actions, no-ops and actual saved/refused/failed outcomes, recording the editing actor, workspace, task and before-after schedule identity without instructions, depth, budget, prompt or criteria. Create/set/resume also record `fire_principal` as `human` or the agent id, and `retry_carry_forward` when stored `Initiator.Direct` is true. The hash is not that principal. Audit-write failure follows existing logged best-effort, not rollback. | MIN-001; S29/S31; §5.3.1 |
| FR-036 | MUST freeze and render this exact notice for create/set/resume in generated TaskScheduleApprovalPreview and live/reconnect approval cards: “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.” Missing or altered text cannot approve. DOC-01/02/03 use that sentence and do not describe a creator. | FQ-2; S31; Source: Interview, 2026-10-15; §5.5/§6.4/§13 |
| FR-037 | MUST, for every finished `RunKindScheduled` run of a task with non-empty `CreatedByAgentID`, deliver one short result notice into that creating agent's eligible main in the task's workspace and wake that main (idle: `WakeParentAlways` via `reportingTargetFor`'s `webchat` plus session id; busy: `EnqueueSteeringWake`; never-turned: `ensureOrdinaryRootRecord` then the idle wake). A person-created task, a manual run, and a pre-claim non-run send nothing. An ineligible creating agent gets no substitute wake; the run stays in history and the miss is logged through `reportUndeliveredWake`. The summary is at most 500 UTF-8 bytes, uses `TASK_SUMMARY` when present, otherwise the fixed outcome line, and never copies the task instructions or the full result. The wake is not a task run. Do not change starter/assignee delivery or `wakeStoredMessage`. | FQ-4; S32; Source: Interview, 2026-10-15; §5.7 |

### 10.2 Success criteria

| ID | Measurable acceptance outcome |
|---|---|
| SC-001 | All **37 FR**, **72 BDD** and **37 planned tests** remain linked: 8 stories / 32 acceptance criteria, 6 datasets / 92 rows, 6 success criteria and 7 evaluator-only scenarios. Later receipts prove each mapped behaviour, including Q1/Q2, FQ-2, FQ-3 and FQ-4. The document-link check does not execute BDD-57, BDD-64 or BDD-66 against a live run and is not evidence that the first fire or the creator notice works. FQ-4 is new scope after the final grill and is verified at the code-review gate and grill-code. |
| SC-002 | Pre-admission Suspend and unadmitted Clear cases create **zero** new automatic resets/runs/sessions, including later drain/unblock; admitted retries still pick up once after capacity refusal. A one-time fire refused before claim keeps its job and pre-fire task file and later claims once, with no warning and no skipped-history row. A repeating pre-claim refusal does not double-run. No extra catch-up burst. |
| SC-003 | Both detail surfaces reject genuine stale-rule saves without losing drafts and show saved-but-unavailable versus exhausted truthfully; live/reconnect approvals preserve exact payload/time, the FQ-2 notice, and the firing-stays-off sentence on a suspended Set only, refusing stale/invalid proposals. |
| SC-004 | Independent Calendar fixtures agree; 99/100000/400-day/per-task bounds and request-wide 100-series/100000-work/10000-item/1048576-byte maxima are enforced at cap/+1. The work-unit +1 test expects the published integers 7, 100000 and 100001, not a counter the production function returns. No per-row whole-store scans; runtime/footprint gates later evidenced, no unmeasured latency claim. |
| SC-005 | Local eligible roles discover and execute the real tool; default-worker, foreign-scope, Auto/unattended and operator-matrix fixtures produce the specified outcomes without changing the resolver. |
| SC-006 | All matching public-doc TODOs pass docs-verifier; exact-commit agent-engine UAT and independent validation pass before integration landing; the five-reviewer gate and founder approval are recorded. |

### 10.3 Traceability matrix — requirement → scenario → planned test

T IDs resolve to the named tests in §9.1. Multiple tests per scenario intentionally separate domain, integration and rendered behaviour. Q1 B/Q2 YES are confirmed requirements, not conditional or skippable rows.

| Requirement | User story | BDD scenarios | Planned tests |
|---|---|---|---|
| FR-001 | US-1 | BDD-01, BDD-02, BDD-07, BDD-08, BDD-56 | T16, T18, T30 |
| FR-002 | US-1 | BDD-03, BDD-05, BDD-58 | T01, T17, T30 |
| FR-003 | US-1 | BDD-05, BDD-07 | T01, T18 |
| FR-004 | US-1 | BDD-02, BDD-05, BDD-06 | T01, T02, T03 |
| FR-005 | US-1, US-4 | BDD-04, BDD-29 | T03, T24, T33 |
| FR-006 | US-2, US-7 | BDD-09, BDD-16, BDD-17, BDD-51 | T04, T19, T26, T29 |
| FR-007 | US-1, US-2 | BDD-03, BDD-15 | T05, T17 |
| FR-008 | US-2 | BDD-09, BDD-10, BDD-11 | T19, T20 |
| FR-009 | US-2, US-5 | BDD-11, BDD-17, BDD-18, BDD-54, BDD-57, BDD-64 | T16, T20, T28 |
| FR-010 | US-2, US-7 | BDD-12, BDD-13, BDD-50 | T03, T14, T21, T34 |
| FR-011 | US-2 | BDD-13, BDD-14 | T03, T17, T21 |
| FR-012 | US-5 | BDD-40, BDD-41, BDD-54 | T28, T34 |
| FR-013 | US-3, US-5 | BDD-23, BDD-24, BDD-39 | T08, T22, T27, T35 |
| FR-014 | US-3, US-5 | BDD-25, BDD-30, BDD-54, BDD-55, BDD-57 | T22, T24, T28 |
| FR-015 | US-3 | BDD-19 | T23, T30, T35 |
| FR-016 | US-3 | BDD-20, BDD-26 | T06, T22, T35 |
| FR-017 | US-3 | BDD-21 | T07, T23 |
| FR-018 | US-3 | BDD-22 | T06, T23 |
| FR-019 | US-4 | BDD-04, BDD-29, BDD-30 | T03, T24, T33 |
| FR-020 | US-4 | BDD-27, BDD-28, BDD-63, BDD-65 | T12, T25, T33 |
| FR-021 | US-4 | BDD-25, BDD-30 | T22, T24, T33 |
| FR-022 | US-4 | BDD-31, BDD-32, BDD-33 | T13, T25, T33 |
| FR-023 | US-3, US-4 | BDD-20, BDD-29, BDD-34 | T06, T24, T35 |
| FR-024 | US-5 | BDD-35 | T08, T27, T30 |
| FR-025 | US-5 | BDD-36, BDD-38, BDD-61 | T09, T27 |
| FR-026 | US-5 | BDD-37, BDD-38, BDD-39 | T08, T09, T27 |
| FR-027 | US-6 | BDD-42 | T10, T31 |
| FR-028 | US-6 | BDD-43, BDD-44, BDD-60 | T10, T26, T31 |
| FR-029 | US-4, US-6 | BDD-27, BDD-28, BDD-47 | T12, T15, T31, T33 |
| FR-030 | US-7 | BDD-48, BDD-49, BDD-50, BDD-52 | T14, T32 |
| FR-031 | US-6 | BDD-45, BDD-59 | T11, T26, T31 |
| FR-032 | US-6 | BDD-46, BDD-47 | T15, T31 |
| FR-033 | US-2, US-4, US-6, US-7 | BDD-16, BDD-31, BDD-51, BDD-59, BDD-60, BDD-63 | T04, T25, T26, T32 |
| FR-034 | US-2, US-7 | BDD-17, BDD-53 | T29, T32 |
| FR-035 | US-3 | BDD-62 | T36 |
| FR-036 | US-4 | BDD-63 | T12, T13, T25, T33 |
| FR-037 | US-8 | BDD-66, BDD-67, BDD-68, BDD-69, BDD-70, BDD-71, BDD-72 | T37 |

## 11. Reachability — mandatory before a completion claim

The baseline positive-control search found `create_task` in all three required trees; literal `schedule_task` occurs in **zero** production files in each tree at the source commit. That proves the present gap, not a delivered feature. UI/approval reachability below is planned. [E-CONTEXT]

| Front door | Required evidence on implementation commit | Owner |
|---|---|---|
| Static tool + policy | Positive-control literal search, then schedule_task present in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/coreagent/`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/config/`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/`; inventory/ceiling equality and per-role classification. Constructor map contains explicit Ask; worker map contains Deny; Mia sparse seed omits the key. No per-agent coverage repair and no change to `denyAllThenOverride` or the REST Deny-fill. | Backend-lead + QA |
| Actual native execution | ToolSearch returns it for an eligible role; approved call reaches real stores/coordinator and persists visible state after boot/reload/late wiring. Worker/foreign/unattended negative controls also run. | Backend-lead + QA/UAT |
| Calendar control | Mount and browser-open CalendarEventSlideOver; real Suspend/Resume POST and persisted refresh; running-work distinction and error recovery visible. | Frontend-lead + QA/UAT |
| Existing task detail | Independently mount and open TaskDetailPanel through its existing task-detail entry; same controls/endpoints, no new scheduled Board/List row or time editor. | Frontend-lead + QA/UAT |
| Approval card | Agent-origin Ask reaches the registered readable preview with full payload; Deny/Approve/Always Allow behave normally; reconnect restores exact proposal and invalid preview cannot approve. | Backend-/frontend-lead + QA/UAT |
| Read/run loop | Real list_tasks views and authorized run_task occurrence/task-level start are usable and show history/actual next-fire truth. | Backend-lead + QA/UAT |
| Public documentation | §13 pages updated in the same behaviour change and audited against the real feature. | Implementing leads + docs-verifier |

Delivery must be reported as two separate claims: **code correct and tested**; **reachable by a user/agent**. This specification-only change establishes neither for the future feature. [S22]

## 12. Agent-engine UAT — exact commit, tester and independent validator

**Mandatory before integration landing.** The implementation touches task admission/tool wiring/preparation under the agent engine and approval WebSocket emission; “no engine change” is not an exemption. Team-lead dispatches a `uat-tester` and a separate `uat-validator` on the **exact commit intended to land**, using **openrouter + deepseek/deepseek-v4.1-flash**. No alternate onboarding model default is acceptable. [S1, S22]

Use the real compiled backend with the matching embedded SPA and isolated data/workspaces, approved model credentials and fixed benign task instructions. Record full SHA, binary/SPA identity, provider/model, workspace, actor/assignee and policy state for every lane. Capture screenshots/page snapshots, approval payloads, task/trigger/run read-back and dispatch/non-dispatch observations with server timestamps. Unit fake-clock evidence supplements but does not replace observing real timed firing/non-firing. Merely polling an empty UI is not proof: include an active control that demonstrably fires through the same engine.

| UAT row | Human/agent journey | Pass evidence |
|---|---|---|
| UAT-01 | Conversation agent discovers/proposes once and recurring work; person approves full card. | Real tool/card, complete task/goal each, correct positions, zero immediate run; the **create** card shows the exact FQ-2 notice before approval and does not name a creator. |
| UAT-02 | Agent edits schedule/rule/end, converts a suspended recurring task to once, and moves a once after a completed mirror; include a stored `Direct: true` initiator and a simultaneous explicit-run retry. | Exact old→new preview, the FQ-2 notice, and on the suspended conversion the firing-stays-off sentence; preserved ID/history/suspension/zone and stored initiator; **actual later eligible once fire**; a non-nil retry still writes `Task.Initiator` as today; later scheduling-tool revocation does not cancel; the assigned agent's membership/delegation and end gates still apply. |
| UAT-03 | Use Suspend then Resume in **each** existing detail surface; additionally fault registration after saved Resume and exercise a genuine stale recurrence save. | Both controls render/POST with saving/error state, GET read-back/reload/cross-view agreement, keyboard/narrow-screen evidence; draft read/review/reapply on 409, saved-active/unavailable Retry activation versus exhausted. |
| UAT-04 | Suspend before a real due position; separately suspend after a run is admitted. Also hold every worker slot so a one-time reminder cannot claim, then free a slot. | Active positive-control fires; suspended new run count stays zero; already admitted run/helpers/attempts finish normally. The unclaimed one-time reminder stays armed with no warning chip and no missed-run record, and the later free slot runs it once. |
| UAT-05 | Leave multiple positions behind suspension, then Resume; include exhausted count and elapsed once. | No burst/missed replay; first eligible future fire only; exhausted/expired has null next fire and truthful explanation. |
| UAT-06 | Agent reads default tasks, occurrences and old-rule run history; explicitly Run now while suspended. | Planned-versus-next-fire distinction, paginated history/links, permitted run without Resume; future occurrence and ordinary Done refusal controls. |
| UAT-07 | Test new-worker Deny, existing-worker inherited Ask without migration, Omnipus inherited Ask/AutoAsks, operator values, same-workspace creator/assignee and foreign mutation/read boundaries. | Correct Deny/Ask/discovery/read redaction and no forbidden write/start; existing entity maps unchanged and Omnipus has no per-agent Ask. Q1/Q2 rows are mandatory, not conditional/N/A. |
| UAT-08 | Auto on, unattended Ask, Allow and God Mode/own Deny. | No silent Auto approval for scheduling Ask, no unattended waiting card, unchanged explicit policy semantics. |
| UAT-09 | Pending approval → rule/target/policy change → approval; reload/reconnect during another pending request; deny/cancel/expiry. | Exact frozen card on reconnect; stale/unusable proposal cannot mutate, terminate or reappear as live later. |
| UAT-10 | Render recorded runs, future suspended positions, past No record, once and dense-day group; simulate read/control failure and restart. | Factual statuses/results/chats/counts, no fabricated skips, visible recovery and retained suspension. |

The validator is dispatched independently, does not coordinate verdicts with the tester, checks every PASS claim against the pack, and re-drives critical paths with its own session. Any code change after acceptance invalidates exact-commit UAT for landing until rerun on the new commit. External workers' own command-line tool enforcement is not certified by native-policy UAT. No UAT has been run by this spec author. [S22]

## 13. User-facing documentation TODOs — same implementation change

These are specific **TODOs**, not claims that public docs changed in this spec-only commit. Existing docs contain stale legacy-schedule/Cron Tool guidance in the affected sections; correct it alongside the actual behaviour, not as an unrelated documentation sweep. Do not preserve a stale hardcoded Auto Ask total. [S1, S19]

| ID / owner | Existing absolute page | Matching documentation TODO / audit acceptance |
|---|---|---|
| DOC-01 / frontend-lead | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/calendar.md` | Explain both controls separate from Stop; Suspended versus recorded/No record/once/groups; no replay/count continues, exhausted/expired versus saved-active/unavailable and Retry activation; stale-save draft read/review/reapply; retained zone/count-reset-on-edit. **State plainly: “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.”** A suspended schedule edit tells the person, before they approve, “Automatic firing stays off. This change does not resume the schedule.” A one-time reminder that cannot start yet shows no missed-run record and no new warning. **A trigger change must send the `schedule_hash` from the last Task read; a hash computed by the client is not valid; metadata saves must omit both `trigger` and the hash.** Remove affected stale old-format guidance; keep Week/Day/Month description accurate. Do not describe a creator. **FQ-4:** when an agent created the scheduled task, each finished timed run also leaves a short result notice in that creating agent's main chat and wakes that agent. A task a person created does not. The person still sees the run here and in task detail. This sentence is new scope after the final grill. |
| DOC-02 / backend-lead | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/tasks.md` | Explain schedule_task versus ordinary create/update, local reads/history/Run now and future-occurrence refusal, no new Board scheduled rows; Clear unadmitted work returns Inbox, admitted retries continue. **Plainly state: “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.”** Use Suspend or remove the schedule to stop future starts; ordinary ends and the assigned agent's checks still apply. A task last retried by a person runs on its timer with that stored authority, exactly as a retry does today. Explain the same notice before create/set/resume approval, the firing-stays-off sentence on a suspended Set, and saved versus armed/unavailable read-back. Do not describe a creator. **FQ-4:** a finished timed run of an agent-created task puts a short result notice in the creating agent's main chat and wakes that agent; a person-created task does not, and the run remains visible in this page's history either way. New scope after the final grill. |
| DOC-03 / backend-lead with prompt-engineer copy | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/tools.md` | Describe Ask tool, new-worker Deny versus unchanged existing-worker inherited Ask (Q1 B), explicit Ask on a new custom Main, Omnipus inherited Ask/AutoAsks without per-agent Ask (Q2 YES), unattended refusal, exact card/grants. **Plainly state: “This schedule keeps running until you suspend it or remove the schedule. Removing this agent's scheduling permission later does not stop it.”** Scheduling permission is checked when Set/Resume runs. Do not describe a creator. Include the firing-stays-off sentence for a suspended Set, and Suspend/remove-timing guidance, not a promise to bypass ends or the assigned agent's checks. Update Ask membership from actual landed facts. **FQ-4:** say that a finished timed run notifies and wakes the creating agent's main chat only when `CreatedByAgentID` is set, that the notice is short and does not repeat the task instructions, and that the wake is not itself a task run. New scope after the final grill. |
| DOC-04 / backend-lead with prompt-engineer copy | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/reference/built-in-tools.md` | Regenerate the catalog from real tool metadata after descriptions/classification land; include scheduling tool and accurate Under Auto value. Do not hand-edit generated descriptions. Existing generator is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/cmd/docsref/main.go`::renderTools. |
| DOC-05 / backend-lead | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/operations/tools-configuration.md` | Replace obsolete Cron Tool guidance and affected config example with task-trigger scheduling/new permission. Warn explicitly that older binaries ignore suspension and can fire suspended work; saved and armed are separate claims. **A trigger change must send the `schedule_hash` from the last Task read; a hash computed by the client is not valid; metadata saves must omit both `trigger` and the hash.** Do not add a feature-specific execution timeout. |

Docs-verifier audits each checkable claim against the implemented code/real entry points. Missing matching documentation for this UI/behaviour change is an Important gate finding; inaccurate draft prose is not evidence of delivery. [S22]

## 14. Sequencing and ownership

| Unit | Owner | Dependency / scope |
|---|---|---|
| Decisions + reviews | Team-lead/founder | Q1 B, Q2 YES, FQ-2 and FQ-3 recorded, Source: Interview, 2026-10-15. FQ-1 A's creator reading is superseded. This is **fix round 2 of 2**, plus FQ-4 added after that grill. No third grill and no review PASS. No CRITICAL item from the round-2 review is left open. FQ-4 is verified at the code-review gate and grill-code. No new ADR choice remains (§1.2, §5.7). |
| Contracts | Backend-lead | Reconcile actual L1 target; contract edits/regeneration atomically before consumers (§5.5). No frontend-owned contract edit. |
| RED | QA-lead | Frozen acceptance/shape, before implementation. Preserve current regression tests and independent oracles. |
| Domain/query/scheduler/admission | Backend-lead | Contract baseline, then shared seams. Coordinate agent/gateway file ownership serially; new helpers in siblings rather than growing budget-pinned files. |
| Tool/policy/approval wiring | Backend-lead | Domain/preparation seams + L1; apply confirmed no-migration existing-worker and Omnipus supporting-set decisions; catalog/default/live/Auto changes together. No runtime resolver change. |
| UI/state/approval | Frontend-lead | Generated contracts first, then parallel with backend against agreed seams. Both controls, read/run truth, modal/store/cache and accessibility are equal delivery obligations. |
| Product text | Prometheus-prompt-engineer | Description/parameter/copy payloads for schedule/list/run and ordinary create/update guidance; backend applies Go strings without a concurrent writer in the same files. |
| CHECK, feature gate, docs/UAT | Independent QA, five reviewers, docs-verifier, tester/validator | Actual implementation SHA; all mandatory gates and public docs before founder landing approval. Architect author recuses from adjudicating its own design. |

Each writer gets its own worktree. Do not merge L1, modify source/contracts/tests, or land implementation as part of this spec-authoring dispatch. [S1, S22]

## 15. Ambiguity warnings, assumptions and clarifications

| Item | Potential wrong assumption | Recorded handling |
|---|---|---|
| Q1 worker upgrade | “Worker-default Deny means migrate old entities.” | Resolved **Q1 B**, Source: Interview, 2026-10-15. Existing maps stay unchanged; absent key inherits Ask. FR-017/T07/UAT-07 mandatory; no migration. |
| Q2 Omnipus capability | “Its older closed supporting set must keep scheduling denied, or adding scheduling grants all task tools.” | Resolved **Q2 YES**, Source: Interview, 2026-10-15. Add schedule_task only, no per-agent Ask; inherited Ask/AutoAsks. FR-018 mandatory, no silent read/run grants. |
| FQ-1 creator reading | “Future fires keep an original creator, and retries must not write `Task.Initiator`.” | **Superseded by FQ-2.** Do not implement that reading. AUTH-1, FR-014/036, BDD-55/63 and DOC-01/02/03 use the stored initiator and the plain notice. |
| FQ-2 stored initiator | “Who created the task decides the timed fire.” | Resolved **FQ-2**, Source: Interview, 2026-10-15. The assigned agent does the work. Keep today's `Task.Initiator`, including the retry carry-forward. No new field. |
| FQ-3 unclaimed one-time fire | “A one-time reminder that cannot claim is deleted, or the wait needs a warning chip.” | Resolved **FQ-3 A**, silent retry. The job and the pre-fire task file survive until claim. No warning and no skipped-history record. BDD-64. |
| FQ-4 creating-agent notice | “The creating agent is who the timed run executes as” or “change the ordinary child-finished wake so every idle parent is woken.” | FQ-4 does not restore FQ-1. FQ-2 still decides execution. The notice is a separate `Deliver` with `SuppressWake`, then a wake of that main only. `wakeStoredMessage` stays. #1317 stays an L2 gap. |
| L1 has not landed here | “Candidate helper and its UI groups are current checkout facts.” | Pin candidate separately, reconcile on actual landing and resolve its recorded membership drift in its owning unit. |
| Missing graph/reference library | “Impact processes/patterns were verified anyway.” | No graph claim; manual impact Inferred. Requested Go pattern index is absent; use named local evidence. |
| Holdout visibility | “A section in a shared spec is a secret oracle.” | §16 is evaluator-only and excluded from development/test matrices; it is not a genuinely blind secret in this repository. Team-lead keeps it out of implementing briefs. |

Assumptions are limited to dispatched D1–D6, existing prerequisites and the explicitly supplied Q1 B/Q2 YES/**FQ-2**/**FQ-3** founder answers. FQ-1 A's creator reading is superseded and is not an open question. No additional founder answer, global priority, production performance result, migration mechanism or approval authority is invented. Current code wins over dated documents. **RRULE (RFC 5545) as the Recurrence Model for Task Triggers** retains obsolete legacy-compatibility/agent-scheduling passages; use **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths** and verified current code for the removed timing/foreign-arming boundaries. This spec flags the disagreement and does not amend unrelated ADRs or restore removed features. [S1, S21]

**Clarification / decisions log, 2026-10-15 — Source: Interview, 2026-10-15**: squad-lead relayed founder Q1 B (existing workers inherit global Ask; NO one-time rollout/migration; only new workers explicit Deny), Q2 YES (Omnipus MAY schedule; supporting-set addition; no per-agent Ask, global Ask/AutoAsks), FQ-2 (a timed run is a normal automatic run of the assigned agent; keep today's stored `Task.Initiator`, including the retry carry-forward; no creator tracking) and FQ-3 A (silent retry: the one-time job and the pre-reset task file survive until claim; no warning and no skipped-history record). FQ-2 supersedes FQ-1 A's creator reading. The same date's team-lead steer adds FQ-4 (result notice and wake on the creating agent's main). These are confirmed decisions, not a claimed spec grill, implementation gate or UAT PASS. FQ-4 is new scope after the final grill.

## 16. Evaluation scenarios — HOLDOUT, evaluator-only

These seven external evaluations are for post-implementation verification by the founder or an independent evaluator. They are **excluded from the TDD plan and traceability matrix** and must not be used as development fixtures or advertised as a hidden/blind oracle. Use observation through the running app and external read-back, not internal implementation calls.

| Evaluation | Category | Setup / action / observable expected outcome |
|---|---|---|
| HO-A — Travel-week report | Happy Path | In a non-browser-local named zone, ask Mia for weekly reports, approve, then suspend for a travel week. No new automatic report appears while suspended; Resume waits for the next future position, with old results still inspectable. |
| HO-B — One-time follow-up | Happy Path | Convert a completed repeating task to one future one-time follow-up through the agent. Approve exact timing; one future run appears and history keeps the original completed run. |
| HO-C — Two-view control | Happy Path | Keep Calendar and existing task detail open on the same scheduled task. Suspend in one, resume in the other. Both agree after confirmation/reload without losing an unrelated unsaved instruction draft. |
| HO-D — Approval changed underneath | Error | Leave an edit proposal pending; change assignee or timing from another session. Approving the old card visibly refuses and does not overwrite the newer task. |
| HO-E — Failed save/read | Error | Make persistence or history temporarily unavailable. Controls and agent replies distinguish refusal/unavailable from saved state/empty history; retry does not create a duplicate task. |
| HO-F — Finite series passes its end | Edge Case | Suspend a short finite series through its final planned position. Resume reports no future fire, does not replenish the count, and does not back-fill work. |
| HO-G — Reload and an old callback | Edge Case | Suspend near a due position while execution capacity is busy; reconnect the browser and later Resume the unchanged rule. Old queued work cannot become a second valid run, and approval/schedule displays remain truthful. |

## 17. Evidence table

Evidence S1–S24/E-CONTEXT retains the initial author's pinned **source-inspection provenance**, not a claim that every old command was rerun in this fix. Current-round first-hand reads/decision receipts are S25–S30 and the revised E-VERIFY. Source inspection is not implemented behaviour exercised; proposed consequences/impact are inferred, and planned runtime tests/UAT have no RED/GREEN receipt yet. Absolute paths identify source symbols, never line numbers.

| Claim | Evidence | Certainty |
|---|---|---|
| S1 — Original design scope and dated founder amendment to Q1/Q2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/design/sched-tasks-suspend-resume-design-20261015.md`::Context / Decision §§1–5 / Dated correction / Alternatives and resolved founder decisions. Original note committed at `3b9c4b4ab65e6f4680bf2248ba3145406a51374b`; Q1/Q2 authority amended in this change from the supplied interview receipt. | Verified (high), source authority; proposals not execution |
| S2 — Trigger/task state and per-entity persistence model | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/task.go`::Trigger / TriggerConfig / Task / IsRepeating. | Verified (high) |
| S3 — Shared validation, updates, locking/reset and bounds | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/store.go`::ValidateTrigger / validateRRULE / Store.updateLocked / Store.SpawnReset / Store.CreateByAgent / recomputeBlockedStateLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/CLAUDE.md`::The canonical striped lock lives here. | Verified (high); planned conditional suspension admission not implemented |
| S4 — All scheduler arm/recovery/callback seams, content generation and current once exclusion | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger.go`::OnTaskUpserted / RunScheduled / rearmRrule / Reconcile / RunRecoverySweep / triggerGeneration / triggerToCronSchedule. | Verified (high) |
| S5 — Queue gap, shared executor and operator-only explicit occurrence context | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor.go`::CheckQueuedTasks / executeTask / SpawnTriggeredRun / StartOccurrenceRun. | Verified source (high); proposed admission changes untested |
| S6 — Agent creation/ownership and goal cleanup | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task.go`::TaskCreateTool.Parameters / taskCreateToolExecute.persistAndRespond / TaskUpdateTool.Parameters / taskUpdateToolExecute.validateAndLoad. | Verified source (high) |
| S7 — Existing allowlisted task reads and explicit run gates | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task_query.go`::TaskListTool.Execute / projectTaskListRow / foreignTaskStatusRow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/run_task.go`::TaskRunTool.Execute / Parameters. | Verified (high) |
| S8 — Existing query projection/limits and asynchronous REST Run now | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/task_occurrences.go`::buildOccurrenceSets / buildOneOccurrenceSet / perTaskInstantCap / perTaskIterationBudget / maxOccurrenceRangeSpanMs; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_task_runs.go`::handleTaskRuns / handleTaskRunNow / toWireTaskRun. | Verified (high) |
| S9 — Editor compile, re-anchor and current run-state mapping | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/calendar/recurrence.ts`::buildRruleString / compileRecurrence / endOfDayUtc; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/calendar/CalendarEventSlideOver.tsx`::buildTriggerForSave; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/calendar/eventMapping.ts`::resolveOccurrenceChipState / resolveRunForOccurrence. | Verified (high) |
| S10 — Existing grant/approval order must receive preparation before lookup | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/loop_run_turn_tools.go`::executeToolCalls / resolveAskPolicy / requestAskApproval; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/loop_policy.go`::CheckGrantOrRequestApproval caller seam. | Verified source (high); new preview/preparation is planned |
| S11 — Live carrier and reconnect are distinct approval paths | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/ws_tool_approval.go`::broadcastToolApprovalRequired / emitSessionState; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ToolApprovalRequiredFrame.yaml`::ToolApprovalRequiredFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/SessionStatePendingApproval.yaml`::SessionStatePendingApproval. | Verified (high) |
| S12 — Preview registry, reconnect stub and UI draft/cache consumers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/agents/approvalPreviews/registry.ts`::TOOL_APPROVAL_PREVIEWS; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/store/toolApproval.ts`::reconcileWithSessionState; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/api/tasks.ts`::tasksQueryKeys. | Verified (high) |
| S13 — Metadata versus live registration and current shipped Auto/global policy | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/general_builtin_catalog.go`::GeneralBuiltinMetadata; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/loop_wire.go`::registerTaskAndPlanTools / wirePlanToolsForAgent; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/config/defaults.go`::defaultToolPoliciesGeneral; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/auto_approve.go`::autoApproveClasses. | Verified source (high) |
| S14 — Two-layer model forbids generic per-agent Deny repair | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-077-two-layer-tool-policy-remove-fail-closed-backfill.md`::Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill, D1/D3/D6. | Verified (high) |
| S15 — L1 worker helper and closed Omnipus supporting set are candidate-only | `git show ad853522d1a9bbcbf800ea59b76c337cbada3feb:pkg/coreagent/seed.go` exit 0: `workerTierDeniedTools` / `RestrictForWorkerTier`; same command for `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/coreagent/role_policies_adr090.go` exit 0: `omnipusSupportedTools` / `omnipusSeedPolicies`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/gateway_sandbox.go`::buildKnownBuiltinToolNames at that SHA. Snapshots: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/l1-seed.go.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/l1-role_policies_adr090.go.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/l1-gateway_sandbox.go.log`. Omnipus role authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field, OSA-1. | Verified candidate source (high); actual integration pending |
| S16 — Existing contracts, mapping and generator ownership/flow | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskTrigger.yaml`::TaskTrigger; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskOccurrenceSet.yaml`::TaskOccurrenceSet; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_task_wire.go`::toWireTrigger / buildTrigger; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/scripts/gen-contracts.sh`::Steps 1–5; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/api/generated/oapi-codegen-config.yaml`::output-options.skip-prune. | Verified (high); all new schema names are proposals |
| S17 — Existing regression instruments read, not run | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger_rrule_test.go`::TestTriggerScheduler_EditDuringFire / TestTriggerScheduler_LegacyPathUnchanged; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/rrule_test.go`::TestRruleExpansion_WallClockDST; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task_initiator_store_test.go`::TestTaskCreateTool_StoresInitiator; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/task_occurrences_test.go`::TestOccurrences_BucketingAndCaps / TestOccurrences_WireArraysNeverNull; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/calendar/eventMapping.occurrences.test.ts`::occurrence-instants suite. | Verified source (high), no passing-test claim |
| S18 — Existing detail/API and design-system boundaries | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/workspaces/TaskDetailPanel.tsx`::TaskDetailPanel; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/api/tasks.ts`::updateTask / tasksQueryKeys; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/calendar/CLAUDE.md`::No raw cron; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/.claude/skills/omnipus-design-system/SKILL.md`::Controls / Focus / recurring UI job. | Verified source (high); future UI not rendered here |
| S19 — Existing public doc pages and generated reference source | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/calendar.md`::Limits and things to watch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/tasks.md`::The task detail panel; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/tools.md`::Allow, ask and deny; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/operations/tools-configuration.md`::Cron Tool; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/cmd/docsref/main.go`::renderTools. | Verified source (high); DOC-01–05 are unimplemented TODOs |
| S20 — Architectural grounding is dated; current code is authoritative | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/AS-IS-architecture.md`::Tools System / Contract-first wire-format pipeline; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/plugin-extensibility-assessment.md`::Tools — not a plugin system today. No new plugin mechanism is commissioned. | Verified source (high) |
| S21 — Existing recurrence/run-history decisions and newer timing/local-scope boundary | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-049-rrule-recurrence-model.md`::RRULE (RFC 5545) as the Recurrence Model for Task Triggers; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-050-task-run-history-model.md`::Per-Task Run History (an additive execution-record layer), RD2/RD6/RD8; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::Session core with an agent address book: reuse one standing session, one archive and the existing execution paths, D5/D11/D12. | Verified source (high); obsolete older passages explicitly flagged |
| S22 — Definition of Done, UAT, docs, test/gate and no-false-green duties | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/CLAUDE.md`::Definition of Done / Reporting Results / Hard Constraints / Build, test, and quality gates / Spec-Driven Workflow / Subagent Workflow; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/false-green-patterns.md`::Checklist: before you claim something passes / A substring scan is not a behavioural test / A stopwatch is not a proof of logic. | Verified (high) |
| S23 — Existing build/toolchain and frontend typecheck | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/go.mod`::go 1.26.9 / existing recurrence dependency; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/package.json`::scripts.typecheck / scripts.build. | Verified (high) |
| E-CONTEXT — Graph unavailable, controlled manual scope/reachability/reference checks | `gitnexus status` exit 0: “Repository not indexed”; checkout query exit 1: repository not found. `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/discovery-0.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/discovery-1.log`; manual `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/trigger-writers.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/dispatch-callers.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/approval-callers.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-evidence-20261015/ui-consumers.log`. Literal positive-control rg create_task exit 0 in all three required trees (3/1/13 files); schedule_task exit 1 in each (0 files). Python reference check found built-in reference true and requested Go index false. | Verified checks (high); blast radius Inferred (medium), no graph result |
| S24 — Founder answers override original recommendations and remove confirmation ambiguity | Supplied squad-lead steer, **Source: Interview, 2026-10-15**: “Q1 = B: existing workers stay on the global ask; NO one-time rollout/migration … only NEW workers get explicit deny”; “Q2 = YES … add schedule_task to omnipusSupportedTools … no per-agent Ask entry … global ask.” | Verified supplied decision receipt (high); no independent interview claimed |
| S25 — Round-1 review input. Its FQ-1 A creator reading is historical and superseded by FQ-2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review.md`::MAJ-001–007 / MIN-001–002 / OBS-001. The round-1 steer is not the normative authority rule. | Verified historical source (high); not the round-2 rule |
| S26 — Publication and canonical cross-store order | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/store.go`::Store.Create / CreateByAgent; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task.go`::SyncTaskGoalRecord / taskCreateToolExecute.persistAndRespond; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger.go`::Reconcile; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/goal/store.go`::Store.Create / Delete / LOCK ORDER; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/goal/doc.go`::Lock order. Initial inverse draft corrected to existing goal→task order, before commit. | Verified current source (high); proposed crash/publication safeguards Inferred, not executed |
| S27 — Existing task authority and retry/queue carrier collision | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/task.go`::Task.Initiator / Initiator; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/tools/task.go`::taskUpdateToolExecute.applyReassignment; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor.go`::automaticInitiator / executeTask / CheckQueuedTasks; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_run_loop.go`::consumeTaskAttempt / retryAuthority; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor_run.go`::runTask / openRun; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/sessioncore_initiator_auto_test.go`::TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup / TestAutoInitiator_N2_UnreadableRetryAuthorityFailsClosed. | Verified current source/test read (high), not a test run; new receipt is planned |
| S28 — Exact current REST/error/read contracts and consumers ground the new shapes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/Task.yaml`::Task; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/TaskUpdateRequest.yaml`::TaskUpdateRequest; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/ErrorResponse.yaml`::error / code / field; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/openapi.yaml`::updateTask / listTaskOccurrences; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/task_occurrences.go`::buildOccurrenceSets / perTaskInstantCap / perTaskIterationBudget; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/lib/calendar/useOccurrences.ts`::fetchTaskOccurrences / occurrencesQueryKey; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/calendar/CalendarEventSlideOver.tsx`::CalendarEventSlideOver. | Verified current source (high); conditional writes/activation/budget delta proposed, not regenerated |
| S29 — Existing semantic audit is explicit and best-effort | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_tasks.go`::auditTask / auditTriggerChange; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/audit/audit.go`::Entry / validEventNames / DecisionAllow / DecisionDeny / DecisionError. | Verified current source (high); new audit acceptance planned |
| S30 — Named catalogued UI reuse is available | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/design-system/catalog.json`::entries; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/button.tsx`::Button; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/src/components/ui/FormError.tsx`::FormError. Catalog extraction exit 0: `catalogued_required_components=8`; absolute-source export inventory `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-fix1-evidence-20261015/catalog-reuse.json`. | Verified source/catalog (high), no rendered conformance claim |
| S31 — Round-2 code reads and founder answers. GitNexus is not indexed in this checkout (`gitnexus status` exit 0: “Repository not indexed”); no production symbol was edited, so no graph impact is claimed | Round-2 review `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review-round2.md`::CRIT-001 / MAJ-001–004 / MIN-001–003 / OBS-001. Founder FQ-2 and FQ-3 A, Source: Interview, 2026-10-15, relayed in this dispatch. Reads this task: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_trigger.go`::RunScheduled / RunRecoverySweep / triggerToCronSchedule; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/store.go`::SpawnReset; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor.go`::executeTask / automaticInitiator; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_run_loop.go`::consumeTaskAttempt / retryAuthority; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/cron/service.go`::scheduleNextRunUnsafe / IsTransient / defaultRetryBackoffMs; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/run_store.go`::RunsInRange; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/task_occurrences.go`::perTaskIterationBudget; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/gateway/rest_agents_create.go`::buildToolConfig; L1 tip `origin/work/omnipus-l1-ui-rooms-wip` `pkg/coreagent/seed.go`::denyAllThenOverride / NewCustomAgentToolsCfg / workerTierDeniedTools / RestrictForWorkerTier and `pkg/coreagent/role_policies_adr090.go`::ADR090RolePolicyInventory / adr090SparseRolePolicies. | Verified source reads (high); proposed behaviour not executed |
| S32 — FQ-4 reuses the existing recipient delivery and wake primitives; it does not change the ordinary child-finished wake | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_executor_judge.go`::deliverTaskCompletionUpward; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_run_mode.go`::captureRunRecipients; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/steer_audience.go`::Deliver / wakeStoredMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/steer/types.go`::UpwardEvent.SuppressWake / DeliveryStoredNotWoken; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/steer_launcher.go`::reportingTargetFor; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/async_notifier.go`::WakeParentAlways / Notify / asyncNotifyMaxContentBytes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/ordinary_root_record.go`::ensureOrdinaryRootRecord; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/steering.go`::EnqueueSteeringWake; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/steer_completion.go`::reportUndeliveredWake; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/session/main_session.go`::MainSessionID; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/session/unified.go`::createMainSessionLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/contracts/components/schemas/SessionMessageCheckpoint.yaml`::summary maxLength 500; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/agent/task_completion_signal.go`::taskSummaryLabel; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/pkg/task/run_store.go`::RunKindScheduled. GitHub issue 1317 is open: “Woken parent turn's reply not shown live in the open chat (wake streams under channel main, not webchat)”. GitNexus is not indexed in this checkout; no production symbol was edited. | Verified source reads (high); FQ-4 behaviour not executed |
| E-VERIFY — Document integrity after FQ-4, not runtime tests and not a review PASS | `python3 -I /Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-fix2-evidence-20261015/check_spec.py` exit 0: PASS, **8 stories / 32 acceptance criteria / 72 BDD / 37 planned tests / 6 datasets / 92 rows / 37 FR / 6 SC / 7 evaluator scenarios**, errors=[]; all **10** deliberately broken-document controls rejected. This check does not execute BDD-57, BDD-64 or BDD-66. Independent link checker exit 0, same identifier counts, errors=[], all **3** negative controls detected. Logs: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-fix2-evidence-20261015/integrity-fq4.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-fix2-evidence-20261015/links-fq4.log`. Receipts: `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-fix2-evidence-20261015/spec-integrity.json`, `/Users/danielpiatkowski/AI-Agent-Workspace/sched-tasks-spec-fix2-evidence-20261015/spec-links.json`. | Verified document checks (high); not feature-test GREEN and not a review PASS |
| **Self-check** | Re-read the spec against FQ-2, FQ-3 and FQ-4. Round-2 rules stay. FQ-4 is §5.7, US-8, FR-037, BDD-66–BDD-72 and T37, marked new scope after the final grill. The creator notice does not change `wakeStoredMessage` or starter/assignee delivery. Summary cap is 500 UTF-8 bytes. No review PASS claimed. Checker receipts are written in E-VERIFY after the run. No production code. | Verified document self-check (high); implementation acceptance Unknown |
