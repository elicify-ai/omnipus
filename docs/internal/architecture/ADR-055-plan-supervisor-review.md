# Adversarial Review: ADR-055 — PlanSupervisor

**Spec reviewed**: `docs/internal/architecture/ADR-055-plan-supervisor.md`
**Review date**: 2026-07-27
**Input mode detected**: structured-spec (FR/NFR/D/G/R identifiers; no BDD scenarios, no traceability matrix, no SC-xxx)
**Verdict**: **BLOCK**

## Executive Summary

Four CRITICAL findings block this ADR. Two of its load-bearing `[FACT]` claims (D9, D10) are
falsified by code that already exists; its central wire change (`Owner{Kind,ID}`) collides with a
required wire property named `owner` that already holds exactly the dual-kind principal FR-3
describes; its enforcement instruction for D9 ("enforce in `updateLocked`") would brick the plan
engine if implemented literally; and its causal attribution of the UAT failure to the missing
correction loop is contradicted by the same report it cites, which names an independent, diagnosed
dispatcher root cause inside ADR-055's declared out-of-scope area.

Separately, the mechanism that is supposed to *close the loop* — `requireOwner` — is not
reconciled with the ADR's own redefinition of Owner, so as specified corrections cannot execute at
all.

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 16 |
| MINOR | 9 |
| OBSERVATION | 4 |
| **Total** | **33** |

All findings below were verified against the working tree at `feature/plan-swimlane-board`
(commit `428efc77`) by direct file reads. Line citations are from that tree.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] `Owner{Kind,ID}` collides with an existing required wire property that already IS the principal

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: FR-3, FR-5, FR-6, D4, G3, R6, NFR-5, §9 implementation step 1
- **Description**:
  §9 step 1 instructs: *"`contracts/components/schemas/Plan.yaml` — `owner_agent_id` → `owner{kind,id}`"*.
  But `owner` **already exists** on that schema, as a **required** property, under
  `additionalProperties: false`:

  ```yaml
  # contracts/components/schemas/Plan.yaml:244-250 (required list at :18-27)
  owner:
    type: string
    readOnly: true
    description: >
      Username of the user who created this plan. Set server-side at
      creation; read-only.
  ```

  Its Go backing is `pkg/plan/plan.go:436-438`:
  ```go
  Owner       string `json:"owner,omitempty"`
  CreatedBy   string `json:"created_by,omitempty"`
  ```

  Worse — the existing field already carries the exact dual-kind semantics FR-3 invents:
  - agent-authored path writes the **agent id**: `pkg/tools/plan.go:286` — `Owner: callerID`
  - UI-authored path writes the **human username**: `pkg/gateway/rest_plans.go:547` — `Owner: c.Username`

  FR-3 reads: *"`Owner` is redefined as the plan's creator/principal: agent-authored → that agent;
  UI-authored → the human username."* That is a verbatim description of a field that shipped.
  The ADR never mentions `Plan.Owner` or `Plan.CreatedBy` anywhere in 515 lines.
- **Impact**: Implementing §9 step 1 literally changes a **required** wire property from `string`
  to `object` with no migration, no deprecation, and no mention — under `additionalProperties:
  false`, which means every existing consumer that sends or validates a `Plan` breaks. The
  resulting record carries four owner-ish fields (`owner{kind,id}`, legacy required `owner:
  string`, `created_by: string`, `owner_session_id: string`), which directly violates the ADR's own
  **NFR-5** ("No second vocabulary for principals"). G3 scopes the migration question to
  `owner_agent_id` only and therefore misses the collision entirely; R6 ("one-way door: the wire
  rename") is under-scoped for the same reason.
- **Recommendation**: Pick one and state it explicitly, with the deletion plan:
  - **(a) Reuse in place** — upgrade the existing `owner: string` to `owner: {kind, id}`, and
    **delete `created_by`** in the same atomic contract commit (it becomes redundant), and delete
    `owner_agent_id`. Document this as three simultaneous breaking wire changes, not one rename.
  - **(b) Non-colliding name** — `principal: {kind, id}`, leaving `owner`/`created_by` alone; then
    NFR-5 must be relaxed or `owner`/`created_by` retired in a follow-up ADR.

  Rewrite G3 and R6 to enumerate every affected property. Add a decision block (D-number) for the
  choice, since it is a one-way door on a persisted shape.

---

#### [CRIT-002] D9's "enforce in `updateLocked`" bricks the plan engine if read literally

- **Lens**: Infeasibility
- **Affected section**: D9, FR-11, D10, §9 implementation step 2
- **Description**: D9 states *"Once a plan leaves `draft`, its definition can no longer be edited —
  DoD, members, edges, bounds"* and *"**Enforce in `pkg/plan`'s `updateLocked`, not in the REST
  handler.** That is the single choke point every writer traverses."*

  The choke-point observation is **correct** — `Store.Update` (`pkg/plan/store.go:283`) is the only
  caller of `updateLocked` (`store.go:287-515`), and there is no engine bypass. That is exactly why
  a blanket non-draft guard there is fatal: **18 of 21 non-test writers are the engine writing to
  non-draft plans through that same function.** Verified inventory:

  | Site | Fields | Plan state |
  |---|---|---|
  | `plan_engine.go:751` | `State=running` | approved |
  | `plan_engine.go:1237/1249/1310/1478/1514/1563` | `PlanPhase`, `JudgeRounds`, `HandoverText` | running |
  | `plan_engine.go:1578` | `State=done` | running |
  | `plan_engine.go:1600` `failPlanLocked` | `State=failed`, `FailedReason` | running/approved |
  | `plan_engine.go:1734` `StopPlan` | `State=failed` | running/approved |
  | `plan_engine.go:2081` `touchActivity` | `LastActivityAt` | running |
  | `plan_engine.go:2818` correction apply | `PlanPhase` | running |
  | `plan_engine.go:2944/2974` `PlayPlan` | `State=approved` | failed/approved |
  | `boot_sweep.go:445-454` intent replay | `PlanPhase` | running |

- **Impact**: A blanket guard makes every one of those return `ErrValidation`. **No plan could
  advance past `approved`.** The engine cannot dispatch, judge, complete, fail, stop, or apply a
  correction. This is the single most likely misreading by an implementing agent, because the ADR
  gives the placement instruction and the "definition is frozen" rule in the same breath without
  ever distinguishing definition fields from state fields on `Patch`.
- **Recommendation**: Restate D9 as a **field allow-list**, not a state-blanket. `Patch`
  (`store.go:232-271`) has 16 fields; exactly six are definition fields:
  `Title`, `Goal`, `Description`, `OwnerAgentID`, `DoD`, `Bounds`. Reject **only those six** when
  `p.State != StateDraft`. Verified safe: no engine, tool, or boot-sweep writer touches any of the
  six — the overlap set is empty; the only writer is `rest_plans.go:738-777`. Add the explicit
  sentence: *"The remaining ten `Patch` fields (`State`, `JudgeRounds`, `ActiveLoop`,
  `PausedReason`, `LastActivityAt`, `PlanPhase`, `FailedReason`, `HandoverText`,
  `LastUnmetTerminalSignature`, `OwnerSessionID`) are engine-owned runtime state and MUST remain
  writable in every non-terminal state."*

---

#### [CRIT-003] D9 and D10's `[FACT]` evidence is falsified — the hole they claim to close is already closed

- **Lens**: Incorrectness
- **Affected section**: D9 ("Today this is unenforced… `[FACT]`"), D10 ("`PUT /plans/{id}` can
  currently reassign the owner at any time… `[FACT]`"), R2, R3
- **Description**: D9 asserts `[FACT]`: *"`pkg/plan/store.go:237` exposes `DoD` on `Patch`,
  `store.go:344-352` applies it with **no state check**, and `rest_plans.go:763-764` wires
  `PUT /plans/{id}` straight to it. So a human can currently move the goalposts *while*
  PlanSupervisor judges against them."* D10 asserts `[FACT]`: *"`PUT /plans/{id}` can currently
  reassign the owner at any time (`pkg/gateway/rest_plans.go:760`)."*

  The store half is true. **The conclusion is false.** `pkg/gateway/rest_plans.go:717-736` — a
  block labelled "review r1 m6" in the source — already blocks both:

  ```go
  if req.Dod != nil || req.OwnerAgentId != nil {
      existing, gerr := a.planStore.Get(id)
      ...
      if existing.State != plan.StateDraft {
          if req.Dod != nil { 409 "dod cannot be changed once the plan has left draft state" }
          409 "owner_agent_id cannot be changed once the plan has left draft state"
      }
  }
  ```

  Additionally, `handlePlanPut` hard-rejects `state` with 400 at `rest_plans.go:701-706`.
- **Impact**: Two High-confidence decisions rest on a mischaracterised current state. The genuine
  residual issue is *placement* (a handler-level invariant that a future second writer could skip),
  which is a materially weaker justification than "a live hole lets a human move the goalposts
  mid-judge." An implementer reading D9 will believe they are closing an exploitable gap and may
  not notice they are relocating an existing one — and, per CRIT-002, may relocate it wrongly.

  D9 also **silently reverses a deliberate decision**: it lists `bounds` as frozen, but
  `rest_plans.go:715-716` explicitly exempts `Bounds` with a rationale — *"an operator may
  legitimately want to extend a running plan's idle-expiry/judge-round budget."* D9 reverses that
  without acknowledging or arguing against it.
- **Recommendation**:
  1. Rewrite D9/D10's evidence to: *"DoD and owner reassignment are already blocked for non-draft
     plans at `rest_plans.go:717-736`. This ADR moves that invariant from the handler into
     `pkg/plan.updateLocked` so the forthcoming correction tool cannot bypass it, and extends it to
     `Title`/`Goal`/`Description`."*
  2. Downgrade D9/D10 confidence from High to Medium, or re-justify on the placement argument alone.
  3. Explicitly decide `Bounds`: either keep the existing operator-escape-hatch exemption
     (recommended — it is the only lever an operator has on a stuck running plan) or argue against
     `rest_plans.go:715-716` on the record.
  4. Delete or rewrite R2/R3 ("RESOLVED by D9/D10") — they resolve risks that were not live.

---

#### [CRIT-004] The UAT evidence does not support the ADR's causal claim; shipping this may move zero parked plans

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: §1 Problem Understanding, §6 opening ("It is the only option that closes
  the correction loop (35% criterion)"), §4 Decision Criteria row 1
- **Description**: §1 attributes the measured failure — *"only 2 of 11 reached `done`"* — to the
  un-wired correction loop. Both numbers verify verbatim against
  `docs/internal/uat/uat-report-round2-2026-07-26.md:100`. But three things in the same report
  contradict the attribution:

  1. **An independent, fully-diagnosed root cause sits one row above**, at `:99`:
     > *"Plan members attached to a plan sit in **Inbox** forever while the card reports 'Running
     > 0/N'. Root cause: `dispatchReadyMembers` only takes `next`; nothing promotes `inbox`→`next`.
     > Hit by 2 testers on the naive happy path; one sat 225 s, the other 14 min | Fix in progress"*

     Members that never leave Inbox never reach all-terminal, so the plan judge never runs and no
     correction is possible. This sits **inside ADR-055's declared out-of-scope area** — §1 states
     the blast radius *"does not touch delegation, task-level judging, or the member dispatch path."*

  2. **The showcase case refutes the thesis.** §1 cites *"One plan with all members `done` and
     progress 1/1 parked for 15+ minutes."* All members done and DoD unmet with nothing failed
     means the judge did not return MET on a **complete** DAG. There was **nothing to correct** —
     `append`, `supersede` and `targeted_retry` all operate on members, and every member had
     already succeeded. A PlanSupervisor with exactly those three verbs cannot fix this case. The
     report's own Status column for that row reads **"Open — judge behaviour"**, not "missing
     correction actor."

  3. **A third confound is named at `:117`**: *"F3 mid-loop stop — 6 attempts burned in <20 s, so
     the loop had already capped."*

- **Impact**: The 35%-weighted decision criterion ("Closes the correction loop — the measured
  failure: 2/11 plans completed") is scored against a failure the ADR may not fix. If the ADR ships
  and the dispatcher bug and judge-MET bug remain, the completion rate does not move, and a new
  System Agent, a breaking wire change, and a 169-occurrence enum rename will have been spent on it.
- **Recommendation**:
  1. Rewrite §1 to attribute the 9/11 shortfall to **at least three independent causes** and state
     which ones ADR-055 addresses. The honest claim is narrow and still sufficient: *"When a plan
     legitimately reaches all-terminal-but-DoD-unmet, no party can supply the correction —
     `AppendCorrection` has zero non-test callers. That is one of at least three causes of the UAT
     shortfall; the dispatcher `inbox`→`next` bug (`uat:99`) and the judge-MET-on-complete-DAG
     behaviour (`uat:100`) are separate and are not addressed here."*
  2. Add an explicit dependency: this ADR's value is **gated on** the dispatcher fix landing.
  3. Re-score criterion 1 with the narrower claim, and add a validation step: *"before implementing,
     re-run the 11 UAT plans against the dispatcher fix and count how many actually park at
     all-terminal-but-unmet."* That number, not 9/11, is the real addressable population.

---

### MAJOR Findings

#### [MAJ-001] The correction gate (`requireOwner`) is not reconciled with the new Owner — as specified, corrections cannot execute

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: FR-4, FR-8, FR-9, D8 (NFR-3 row), NFR-3
- **Description**: D8's table says only PlanSupervisor may correct, and the Owner may not. NFR-3 is
  claimed *"satisfied by the pre-existing `requireOwner` agent-identity gate."* But `requireOwner`
  (`plan_engine.go:2755-2771`) is:
  ```go
  if caller.AgentID != p.OwnerAgentID { ... return ErrCorrectionNotOwner }
  ```
  Under FR-3/FR-4 the Owner is no longer necessarily an agent, and has **no** correction rights.
  So the gate either (a) denies PlanSupervisor, because PlanSupervisor is never the owner, or
  (b) must be rewritten to `caller.AgentID == <planSupervisorID>`. **The ADR never says which.**
  FR-9 only removes the *session* clause; it is silent on the agent clause.
- **Impact**: The single mechanism this ADR exists to unblock remains blocked. An implementer
  following FR-9 literally (drop only the session clause) ships a PlanSupervisor whose every
  correction attempt returns `ErrCorrectionNotOwner`.
- **Recommendation**: Add an explicit decision: *"`requireOwner` is renamed `requireCorrectionAuthority`
  and its predicate becomes `caller.AgentID == coreagent.IDPlanSupervisor`, preserving the opaque
  denial (sec-MAJOR-2). The `OwnerAgentID` comparison is deleted along with the session clause."*
  State what the REST parity route (FR-8) checks instead, since a human caller has no `AgentID`.

---

#### [MAJ-002] The Amends line cites five anchors that do not exist in ADR-053

- **Lens**: Inconsistency
- **Affected section**: Header line 5 — *"Amends: ADR-053 (§Contract Surface, FR-143/FR-147, C1/INV-2/INV-7)"*
- **Description**: Verified against all 231 lines of `ADR-053-unified-goal-plan-subagent.md`:
  ADR-053 contains **no FR- identifiers at all**, and grep for `C1`, `INV-2`, `INV-7` returns
  nothing. All five anchors live in `docs/internal/specs/unified-goal-plan-subagent-spec.md`
  (FR-143 `:1518`, FR-147 `:1522`, C1 `:39`/`:92`, INV-2 `:514`, INV-7 `:519`). The section is
  `## 7. Contract-first surface this ADR authorizes`, not `§Contract Surface`.
- **Impact**: The amendment record is unfollowable. A future reader trying to reconcile ADR-053
  with ADR-055 will find nothing at the cited anchors and may conclude the amendment is spurious.
- **Recommendation**: Split the line: *"Amends: ADR-053 (D2, D4, D7, §3 anti-drift rule, §5.1 —
  see MAJ-003) and `docs/internal/specs/unified-goal-plan-subagent-spec.md` (FR-118, FR-140,
  FR-143, FR-146, FR-147, FR-186, INV-2, INV-7, C1)."*

---

#### [MAJ-003] Undeclared breakage of at least ten upstream decisions, including a `[P2]` MUST-NOT and two `[P1]` MUSTs

- **Lens**: Inconsistency
- **Affected section**: Header Amends line; FR-2, FR-4, FR-11, D1, D7
- **Description**: Seven ADR-053/ADR-049 items and four spec FRs are contradicted but not listed:

  | Upstream | ADR-055 change | Listed? |
  |---|---|---|
  | ADR-053 **D4** (`:86`, High) — the correction-verbs decision, i.e. the actual decision being amended | FR-8/D5 move the correction actor | **No — D4 is never named** |
  | ADR-053 **D7** (`:87`) — "Adjust a member = Stop plan → change → continue" | FR-11/D9 delete the "change" step | **No** |
  | ADR-053 **D2** (`:95`) — "only a direct session/plan owner asks the human" | FR-4 demotes Owner to passive; no new terminus for plan-scoped `owner_required` questions | **No** |
  | ADR-053 **§5.1** (`:117`) — text ADR-053 ordered inserted **verbatim into ADR-049**: "persistent owner session… correction loop runs as an owner inbox" | FR-4 + D7 retire it | **No — silently falsifies a third ADR** |
  | ADR-053 **S4 spine seam** (`:54`, "highest-risk seam") — Owner and Judge as two of four interlocked parties | D1 merges them | **No** |
  | ADR-053 **§3 anti-drift rule** (`:47`) — "a second goal store, a second messaging envelope… is a blocking finding (DoD-11)" | New System Agent replacing the plan judge | **No — the BOM/anti-drift gate is never run** |
  | spec **FR-146** `[P2]` (`:1546`) — re-planner "MUST be delivered by EXTENDING `pkg/skills/embedded/plan/SKILL.md` — **never a new Planner agent (BOM)**" | D1 introduces a new agent that owns re-planning | **No — direct MUST-NOT violation** |
  | spec **FR-118** `[P1]` (`:1486`) — boot-sweep exemption resolved via `Plan.owner_session_id` | D7 retires the linkage | **No** |
  | spec **FR-147** `[P1]` (`:1522`) — "the Plan record MUST persist `owner_session_id`… the owner session MUST carry a reciprocal `plan_id`" | D7 retires both | **No** |
  | spec **FR-186** `[P2]` (`:1558`) + **FR-140** `[P2]` (`:1515`) | D6 renames the phase; FR-4 removes the persistent owner session | **No** |

- **Impact**: FR-146 is the sharpest: it is an explicit, on-the-record MUST-NOT against exactly what
  D1 proposes, written to prevent Bill-of-Materials drift. ADR-055 does not argue against it — it
  does not know it exists. An architecture review that misses a direct prohibition on its central
  decision has not been performed.
- **Recommendation**: Add a `§ Upstream decisions this ADR reverses` section listing each row above
  with a one-line justification for the reversal. FR-146 needs a real argument (why a System Agent
  is not the BOM growth FR-146 forbids — the Judge precedent is probably the answer, but it must be
  stated). Correct §1's framing that this was "deferred by design, not forgotten" — that reads on
  FR-143 `[P2]` but not on FR-118/FR-147, both `[P1]`.

---

#### [MAJ-004] G2/R1 — "the ADR's largest open risk" is not a risk; the surface already ships

- **Lens**: Incompleteness (research gap)
- **Affected section**: G2, R1, D5 `Missing:` line, §9 Validation step 1
- **Description**: R1 states *"`notifyOwner` for a human has no identified delivery surface…
  **This is the ADR's largest open risk.**"* A complete, durable, per-human notification system
  already exists end to end:
  - Store: `pkg/notifications/store.go:1-12` — *"file-based, **per-user** notification store…
    Each notification belongs to a single recipient (**a username**)"*; `$OMNIPUS_HOME/notifications/<recipient>.json`,
    atomic write, cap 50 (`:42`); `Create` `:169`, `ListForUser` `:224`, `UnreadCount` `:236`,
    `MarkRead` `:254`.
  - Boot wiring: `pkg/gateway/gateway.go:2262` (+ reload `:3294`), injected `:2867`.
  - REST: `/api/v1/notifications`, `/{id}/read`, `/read-all` — `pkg/gateway/rest.go:4863-4865`,
    handler `pkg/gateway/schedules.go:1190-1236`.
  - Contract: `contracts/openapi.yaml:5472,5491,5504`; `contracts/components/schemas/Notification.yaml`.
  - Live push filtered per user: `pkg/agent/loop.go:3585-3592` → `pkg/gateway/websocket.go:3364-3400`,
    `wc.userID == p.Recipient`.
  - SPA: `src/store/notifications.ts` (zustand, hydrate + WS apply + read state),
    `src/components/layout/NotificationPanel.tsx` (bell + unread badge), mounted at
    `src/components/layout/AppShell.tsx:246`, hydrated `:45-54`.
- **Impact**: The ADR's stated highest risk, and the first item in its pre-implementation validation
  list, is answered by reading. Leaving it as `[UNKNOWN]` invites an implementer to build a second
  notification surface — which would violate NFR-5's spirit and ADR-053 §3's anti-drift rule.
- **Recommendation**: Replace R1/G2 with a bounded work list. The **real** gaps, all small and all
  unstated:
  1. `Notification.type` is a **closed one-value enum**: `enum: [schedule_failed]`
     (`contracts/components/schemas/Notification.yaml:20-24`; Go mirror `store.go:37-38`). Adding
     `plan_done`/`plan_failed`/`plan_stopped` is a contract change + `scripts/gen-contracts.sh` +
     atomic commit (Constraint #8, 5-step process) — put it in §9 as step 1b.
  2. **No `plan_id` field** on `Notification` — click-through has only `schedule_id`/`session_id`/`agent_id`.
  3. `Create` coalesces unread items **only on `ScheduleID`** (`store.go:169`, `~:194`) — plan
     notices will not dedup without a new key.
  4. The plan engine holds **no `notifStore` reference**; a new injection point is needed, and
     `restAPI.notifStore` can be nil (`rest.go:182-184`, 503 at `schedules.go:1194`) — decide the
     nil behaviour.

  Also state explicitly that `plan_status` (the existing WS frame) is **not** the rail: it is
  broadcast to all connections with no recipient filter, carries no title/body, and the SPA
  discards it after `invalidateQueries` (`src/store/chat.ts:4378-4391`) — it cannot survive a reload.

---

#### [MAJ-005] Constraint #6 cost claim is inverted; the real risk is silent privilege inheritance

- **Lens**: Insecurity / Incorrectness
- **Affected section**: §5 Option B — *"This also pre-solves most of the Constraint #6 tax"*;
  Constraints bullet 3
- **Description**: The validator (`pkg/config/validate.go:448-475`) is **OR-based per (agent, tool)**:
  a global `cfg.Sandbox.ToolPolicies[tool]` entry alone satisfies coverage (`:459-461`). And
  `config.DefaultConfig()` already seeds a global ceiling entry for **every** static tool
  (`pkg/config/defaults.go:282-420`). Therefore adding a new agent requires **zero** per-agent
  entries to pass the boot gate (`pkg/gateway/gateway.go:1541-1549`). There is no "tax" to pre-solve.

  The real, unstated risk is the inverse. `resolveEffectivePolicyWith`
  (`pkg/tools/compositor.go:178-201`) is strictest-wins with `case a == "": return g` — an agent
  with **no** per-agent entry **inherits the global ceiling**, which is `allow` for `bash`,
  `write_file`, `set_config`, `create_agent`. A PlanSupervisor seeded without an explicit policy map
  would **boot cleanly, pass validation, and silently hold near-god-mode tools**.
- **Impact**: An implementer who reads "Constraint #6 is pre-solved" and omits the
  `systemAgentSeed` entry ships a new, locked, boot-re-enforced agent with shell and config-write
  access — and nothing fails. This is a plausible path to the worst security outcome in the change.
- **Recommendation**: Replace the "pre-solves the tax" sentence with:
  > *"Constraint #6's validator is satisfied by the pre-seeded global ceiling, so a missing
  > per-agent policy map does **not** fail boot — it silently inherits `allow` for `bash`,
  > `write_file`, `set_config`, `create_agent` (`pkg/tools/compositor.go:178-201`). PlanSupervisor
  > MUST therefore be added to `systemAgentSeed`'s switch with `denyAllThenOverride(...)`, which
  > generates all 83 explicit entries from a granted-tool allow-list. A test MUST assert
  > PlanSupervisor's effective policy for `bash` is `deny`."*

---

#### [MAJ-006] PlanSupervisor's tool grant is never specified

- **Lens**: Incompleteness / Insecurity
- **Affected section**: D1 ("tool policy explicitly enumerated in `systemAgentSeed`"), D8, NFR-2
- **Description**: D1 says the policy will be enumerated but never lists a single tool. The Judge's
  grant is `read_file` / `list_directory` / `inspect_session` allow, else deny (`core.go:847-859`).
  PlanSupervisor needs strictly more: it must read plan and task state to judge a DoD, and must hold
  the new correction tool (FR-8). Neither is named.
- **Impact**: Blocks implementation (nothing to build), makes MAJ-005's inheritance trap live, and
  makes NFR-2's structural claim unverifiable — see the next finding.
- **Recommendation**: Add a `D12 — PlanSupervisor tool grant` block with the literal list and a
  one-line justification per tool. State whether it gets `read_file` (needed to inspect member
  artifacts? or is `inspect_session` enough?) and explicitly deny `write_file`, `bash`,
  `set_config`, `create_agent`, `delegate`.

---

#### [MAJ-007] NFR-2's "structural, not runtime" guarantee does not hold for a tool-bearing agent

- **Lens**: Insecurity
- **Affected section**: NFR-2, D8 — *"NFR-2 is satisfied **structurally, not by a runtime check**:
  `CorrectionRequest` has seven fields… and **no DoD field**"*
- **Description**: The `CorrectionRequest` claim is verified correct (`plan_engine.go:2410-2418`,
  exactly the seven named fields, no DoD; verbs `append`/`supersede`/`targeted_retry` only,
  `validateCorrection` `:2693` rejects a fourth). But PlanSupervisor is **an agent with a toolset**,
  not a pure function. The DoD is reachable by any of: `write_file` on the plan JSON, a
  plan-mutating tool, or the new REST correction route (FR-8) if PlanSupervisor can issue HTTP.
  D8's counter-argument — *"tool possession is not authority, so distributing a correction tool is
  not itself a privilege escalation"* — is about **other** agents; it says nothing about the
  supervisor's own grant.
- **Impact**: The 25%-weighted integrity criterion ("an adjudicator that can rewrite its own DoD is
  worthless") is asserted, not established. If MAJ-006 is resolved carelessly (e.g. `write_file`
  granted "so it can read/write artifacts"), the property is void and nothing detects it.
- **Recommendation**: Restate NFR-2 as a **conjunction**: *"(a) `CorrectionRequest` carries no DoD
  field, and (b) PlanSupervisor's tool grant contains no filesystem-write, config-write, or
  HTTP-issuing tool. Both are required; (a) alone is insufficient."* Add the regression test D8's
  own `Would improve` line suggests, plus a second asserting PlanSupervisor's effective policy denies
  `write_file`/`bash`.

---

#### [MAJ-008] Corrections are unbounded — no counter, no ceiling, no budget

- **Lens**: Incompleteness / Insecurity (DoS/cost)
- **Affected section**: §5 Option C — *"versus correction rounds bounded by `judge_max_rounds`"*;
  NFR-1
- **Description**: There is **no correction counter and no correction-specific bound anywhere**.
  `AppendCorrection` (`plan_engine.go:2574-2688`) performs no ceiling check. The only bound is
  `PlanJudgeMaxRounds` (default **20**, `pkg/config/planning.go:14,41`), enforced solely in
  `beginPlanJudgeRound` (`plan_engine.go:1281-1291`) against `JudgeRounds`, incremented at exactly
  one place (`:1497`). Corrections are bounded only transitively, and only if each correction leads
  to a judge round.
- **Impact**: Today this is harmless — nothing calls `AppendCorrection`. Wiring an **autonomous
  LLM** to it creates a supervise → correct → dispatch → judge-UNMET → supervise loop that can run
  20 rounds, each with member re-execution and a full LLM adjudication. That is the primary
  cost-amplification path this change opens, and the ADR's only cost control (NFR-1) bounds *wake
  frequency*, not correction volume or token spend. A supervisor that keeps appending tail members
  in response to its own UNMET verdicts is a plausible, unbounded burn.
- **Recommendation**: Add an explicit bound decision. Minimum: a per-plan `corrections_applied`
  counter with its own ceiling in `PlanBounds` (alongside `PlanJudgeMaxRounds`), and an explicit
  terminal reason when exhausted. Also address the semantic overload noted in MIN-007.

---

#### [MAJ-009] D6's rename is a 169-occurrence, 31-file wire-and-disk change with no migration story

- **Lens**: Incompleteness
- **Affected section**: D6, G3, R6, §9 step 2
- **Description**: D6's own `Would improve` line asks for the grep; here it is.
  `awaiting_owner_correction` / `PhaseAwaitingOwnerCorrection` appears **169 times across 31 files**:
  - Contracts: `contracts/asyncapi.yaml:3363`; `Plan.yaml:87,96,111,116,124,129,136`;
    `PlanStatusFrame.yaml:46,52`; `SessionLifecycleRecord.yaml:65`; `GoalStatusFrame.yaml:107`;
    plus the four `pkg/gateway/inboundschemas/` mirrors.
  - Generated: `pkg/api/generated/openapi_types.gen.go` (6 enum sites + 16 doc strings);
    `src/lib/api/generated/` × 4 files.
  - Go: `pkg/plan/plan.go:237` (definition), `:266` (valid-phase set); `pkg/agent/plan_engine.go`
    (12 sites); `pkg/agent/boot_sweep.go:241-249`; `pkg/plan/intent_log.go:107`.
  - SPA: `src/lib/planStateColors.ts:177,249`; `PlansFilterBand.tsx:256,349`;
    `WorkspaceGraphTab.tsx:248`.
  - Tests: `tests/e2e/conformance-design-e2e.spec.ts` (17), plus 5 Go and 3 TS test files.

  **Critically: the phase value is persisted on disk in plan records.** G3 scopes the no-migration
  decision to `owner_agent_id` only. A plan parked at `awaiting_owner_correction` before the upgrade
  will, after it, carry a phase string absent from `IsValidPhase`'s set (`plan.go:266`).
- **Impact**: Per the UAT, **9 of 11 plans were parked in exactly this phase**. Those are precisely
  the records most likely to exist on an upgraded install, and their behaviour post-rename is
  undefined — silently re-read as an unknown phase, or rejected by normalisation.
- **Recommendation**: Extend G3 to cover the phase value explicitly and decide: (a) no-migration
  (state what a stale on-disk value does — most likely `EffectivePlanPhase()` falls through to
  `idle`, which would make a parked plan look runnable and is *worse* than the status quo), or
  (b) a one-shot read-time coercion in `plan.normalize()`. Given the 9/11 concentration, (b) is the
  safer default. Put the reader-grep result in the ADR rather than as a `Would improve`.

---

#### [MAJ-010] D5 routes the `stalled` wake to PlanSupervisor without saying what it does there

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: D5 table row `:1254`; D6
- **Description**: D5 classifies `plan_engine.go:1254` ("Plan %q is stalled." + reason) as a
  **decision** and routes it to PlanSupervisor. But `stalled` is a **different phase** from
  `awaiting_owner_correction`, with no judge verdict attached and nothing structurally to correct.
  The SPA carries an explicit guardrail against conflating them
  (`src/lib/planStateColors.ts:179-183`): *"Never collapse the two into the same label/copy"* —
  chip `'Stalled — needs a correction'` at `:186`, `STALLED_EXPLANATION` at `:233`. D6 renames only
  the correction phase and never mentions `stalled`.
- **Impact**: PlanSupervisor is woken with no verdict and no criteria evaluation, and the ADR gives
  it no defined behaviour there. Post-rename, the phase vocabulary becomes `awaiting_supervision` +
  `stalled` — two phases both meaning "supervisor input needed", which is exactly the naming
  confusion D6 exists to remove, reintroduced.
- **Recommendation**: Add to D5 (or a new decision) what PlanSupervisor does on a stall: does it run
  a DoD judgement, issue a correction directly, or something else? And decide whether `stalled`
  survives D6's rename or merges into `awaiting_supervision` — respecting the
  `planStateColors.ts:179-183` guardrail either way.

---

#### [MAJ-011] D6's crash-safety justification rests on machinery that is dead in production

- **Lens**: Incorrectness
- **Affected section**: D6 ("Why retain" — *"the phase is what tells the boot sweep to re-wake it"*),
  D7, G4, §9 Validation step 2
- **Description**: The boot-sweep exemption (`boot_sweep.go:160-165`) is:
  ```go
  if rec.State == session.LifecyclePaused && rec.OwnsPlanID != "" {
      if pe.planIsAwaitingOwnerCorrection(rec.OwnsPlanID) { preserve; continue }
  }
  ```
  **Both** predicates are unreachable in production:
  - `LifecycleRecord.OwnsPlanID` (`pkg/session/lifecycle.go:199`) has **zero non-test writers**
    (the ADR notes this at D7).
  - `session.LifecyclePaused` **also** has zero non-test writers — the only production lifecycle
    writes are `LifecycleQueued` (`delegate.go:945,2086`), `LifecycleNeedsInput`
    (`message_parent.go:691`), `LifecycleFailed` (`boot_sweep.go:203`).

  So exemption (b) has never fired outside tests. What actually re-arms a parked plan after restart
  is `bootReconcile` (`plan_engine.go:3176-3206`) — a **different** mechanism that lists plans,
  filters `State == StateRunning` (`:3189`), rehydrates `LastUnmetTerminalSignature` (`:3198-3201`)
  and calls `processPlan`. D6 conflates the two.
- **Impact**: D6's retention argument ("without the marker… it is silently stuck") is directionally
  right but cites the wrong machinery, and the machinery it cites doesn't work. If retention is
  justified on a dead path, a reviewer could reasonably conclude the phase is droppable — the
  opposite of D6's conclusion.
- **Recommendation**: Rewrite D6's "Why retain" to cite `bootReconcile` (`plan_engine.go:3189-3202`)
  and the `unmetTerminalSignatureUnchanged` gate (`:1295`), which are live. Then answer G4/D7
  definitively — the answer is **yes, `OwnsPlanID` can go**: build the exemption set from
  `planStore.List` where `EffectivePlanPhase()==<parked>` and `OwnerSessionID != ""` (the reciprocal
  `Plan.OwnerSessionID` **is** written in production, `plan_engine.go:2469-2478`, at the same moment
  the phase is set, `:1514`). `pe.planStore` is already in scope in `boot_sweep.go`, and
  `bootReconcile` already does a `planStore.List` at `:3181` that can be shared. Promote D7 from
  Medium to High and delete the "do not delete blind" caveat.

---

#### [MAJ-012] FR-5's "never empty" human id is unsatisfiable on at least three paths

- **Lens**: Infeasibility
- **Affected section**: FR-5, D4, NFR-4
- **Description**: FR-5 requires *"A human owner MUST carry a real id (the username) — never an
  empty one."* Three problems, none addressed:
  1. **Validation rejects it today.** `validatePlanOwnerAgent` (`pkg/gateway/rest_plans.go:64-78`)
     requires the owner be a registered agent in `cfg.Agents.List` passing `IsChatTarget()`
     (`pkg/config/config.go:1045-1054`). A username is rejected. Same validator gates the agent
     `create_plan` tool (`pkg/tools/plan.go:87,110,239-249`, deny-by-default if unset).
  2. **Dev-mode bypass yields an empty username.** `caller.Username` is `""` under bypass
     (`pkg/gateway/rest_workspaces.go:66`) — the exact violation FR-5 forbids, on a supported path.
  3. **There is no stable user id.** `config.UserConfig` (`pkg/config/config.go:2327-2331`) has
     **no `ID` field** — the username *is* the primary key, looked up by string match
     (`config.go:3844-3856`). No endpoint renames or deletes a user, and nothing repairs a dangling
     reference if `config.json` is hand-edited. The system is effectively single-account
     (`pkg/config/single_account_migration.go`).
- **Impact**: (1) blocks the feature outright until the validator is split by principal kind — an
  implementation task §9 does not list. (2) produces a plan with `owner.kind=human, owner.id=""`,
  which is precisely the `OwnerScopeKind`-empty-id failure D4 cites as its reason for *not* copying
  that convention — reintroduced through a different door. (3) means a renamed user orphans every
  plan they own, with no notification delivery (`notifications` are keyed by username filename,
  `pkg/notifications/store.go:84-91`).
- **Recommendation**: Add a decision covering: the validator split (`validatePlanOwner` branching on
  `kind`), the bypass case (reject the write, or stamp a reserved sentinel and document it), and an
  explicit statement that username *is* the human principal id with no rename support, tied to
  NFR-4 (a dangling human owner must degrade to "no notification delivered", never block execution).

---

#### [MAJ-013] `Plan.CreatedBy` and `Plan.OwnerSessionID` create a four-field owner vocabulary — NFR-5 violated by the ADR itself

- **Lens**: Inconsistency
- **Affected section**: NFR-5, FR-6, D4, D7
- **Description**: After the change the `Plan` record carries: the new `owner{kind,id}`, the existing
  required `owner: string` (CRIT-001), `created_by: string` (`plan.go:438`, wire-required,
  `Plan.yaml` required list `:18-27`), and `owner_session_id` (`plan.go:393-403`, which D7 proposes
  retiring but spec FR-118/FR-147 `[P1]` mandate). NFR-5 states: *"No second vocabulary for
  principals."*
- **Impact**: The ADR fails its own non-functional requirement.
- **Recommendation**: Enumerate the final field set explicitly in D4 and state which fields are
  deleted in the same atomic contract commit. Note that `Plan.Owner`/`Plan.CreatedBy` are currently
  **never rendered by the SPA** (grep of `src/components/`, `src/routes/` finds only
  `task.created_by` at `TaskDetailPanel.tsx:1133`), so deleting or repurposing them is cheap on the
  frontend — that is an argument for CRIT-001 option (a).

---

#### [MAJ-014] D3's "structured verdict + chosen verb" is an undeclared new wire type

- **Lens**: Incompleteness (Constraint #8)
- **Affected section**: D3, §9 implementation order
- **Description**: D3's `Missing` line asks *"Whether the existing judge verdict schema can be
  reused verbatim for plan-level DoD."* It can be answered by reading: `pkg/task/verdict.go:43`
  defines `JudgeVerdict{ID, Scope, TaskID, PlanID, GoalSessionID, Round, Met, PerCriterion
  []CriterionVerdict, Model, JudgedAt, JudgeAgentID}` with `CriterionVerdict{CriterionID, Met,
  Reason}` (`:25`) and `VerdictScopePlan` already defined (`:12-19`). It is already on the wire
  (`pkg/api/generated/openapi_types.gen.go:7968`, `asyncapi_types.gen.go:285` `JudgeVerdictFrame`)
  and already used for plan scope (`plan_engine.go:1395-1407`). It is fail-closed by construction
  (`Met` zero-value false).

  But D3 asks for **more**: *"(met/unmet, per-criterion reasoning, **chosen verb**)"*. There is no
  verb field on `JudgeVerdict`. That is a **new or extended wire type**, which under Constraint #8
  requires the 5-step process (schema file → reference → `scripts/gen-contracts.sh` → atomic commit
  → handler). §9's implementation order does not include it.
- **Impact**: An implementer following §9 writes a hand-rolled Go struct for the verdict-plus-verb —
  a hand-written wire-format type, which is FORBIDDEN and lint-caught per Constraint #8.
- **Recommendation**: Replace D3's `Missing` line with the answer, and decide the shape: either
  (a) reuse `JudgeVerdict` unchanged for adjudication and have the verb arrive as a **separate tool
  call** (cleaner — keeps adjudication and remediation decoupled, and keeps NFR-2's structural
  argument simple), or (b) extend `JudgeVerdict` with an optional verb and add the contract step to
  §9. Option (a) is recommended; see OBS-002.

---

#### [MAJ-015] No failure model for PlanSupervisor itself

- **Lens**: Incompleteness / Inoperability
- **Affected section**: FR-2, NFR-4, D1, D11, §7 Risks
- **Description**: The ADR specifies nothing for: PlanSupervisor unavailable (provider 404,
  rate-limited, model retired); a malformed or unparseable verdict; an invalid verb; a
  `SupersededMemberID`/`RetriedMemberID` naming a nonexistent member; the supervisor's own turn
  timing out. NFR-4 covers a missing **Owner**, not a missing **Supervisor**.

  The task judge already has this: `JudgeCriteriaResult.Unavailable` (`pkg/agent/judge.go:214-229`)
  reverts the plan to `dispatching` and burns **0 rounds** (`plan_engine.go:1478-1495`). No analogue
  is specified for supervision. D11 makes the model operator-configurable, and per `CLAUDE.md` a
  non-tool-capable model returns 404 on every request — a plausible misconfiguration with no
  specified behaviour.
- **Impact**: A provider outage or a misconfigured PlanSupervisor model parks **every** plan
  indefinitely with no adjudicator — reintroducing the exact failure class this ADR exists to
  eliminate, and now with no fallback actor, because FR-4 removed the Owner's adjudication role.
- **Recommendation**: Add `D13 — PlanSupervisor unavailability`: mirror the judge's `Unavailable`
  contract (revert to `dispatching`, burn 0 rounds, retry with backoff), define a bounded retry
  count, and define the terminal outcome when retries are exhausted (fail the plan with a distinct
  `failed_reason`, and notify the Owner — which is a legitimate use of the FR-7 outcome path).
  Specify verdict-parse-failure and invalid-verb handling as `Unavailable`, not as a correction.

---

#### [MAJ-016] No observability, audit, rollback, or runbook for a new autonomous decision-maker

- **Lens**: Inoperability
- **Affected section**: whole ADR; §7 Risks
- **Description**: Nothing in the ADR addresses:
  - **Observability**: how an operator sees *why* a DoD was ruled UNMET across rounds, or which verb
    the supervisor chose and why. `HandoverText` exists but is steering text for the actor, not an
    operator record.
  - **Audit/repudiation**: `plan.update` is audited at `rest_plans.go:796`, but a **tool-path**
    correction is not. FR-8 distributes correction authority to an autonomous agent with no stated
    audit requirement.
  - **Rollback**: R6 declares the wire rename a one-way door, but the ADR never says what happens if
    PlanSupervisor adjudicates badly in production. There is no flag, no way back, and FR-4 has
    deleted the human's adjudication role, so there is no fallback actor either.
  - **Runbook**: nothing on "PlanSupervisor is looping" or "every plan is parked" — which MAJ-008
    and MAJ-015 both make reachable.
  - **Auth on the new REST route** (FR-8, "a REST route for human parity"): unspecified. Note
    `PUT /plans/{id}` today has **no** `adminWrap`/`RequireNotBypass` (`rest.go:4838-4839` →
    `withAuthAndBodyLimit`), so under dev-mode bypass any caller reaches it. A correction route
    inheriting that posture is a live elevation path.
- **Impact**: A new autonomous actor with mutation authority ships with no way to see what it did,
  no audit trail, and no way to turn it off.
- **Recommendation**: Add an operability section covering: an audit event per correction (verb,
  reason, falsified assumption, member ids) on **both** the tool and REST paths; persistence of each
  plan-level verdict for operator inspection; the auth posture for the FR-8 route (recommend
  `adminWrap` + `RequireNotBypass`, matching the sandbox-config precedent); and either a kill-switch
  (config flag that reverts to today's park-and-wait) or an explicit statement that none exists and
  why, given R6's one-way door.

---

### MINOR Findings

#### [MIN-001] `JudgeDefaultRubric` is cited at the wrong lines and is not lazily materialized

- **Lens**: Incorrectness
- **Affected section**: D1, D3, §5 Option B bullet 4
- **Description**: The ADR says the rubric is *"lazily materialized from the exported
  `JudgeDefaultRubric` constant (`core.go:861-889`)"*. The const is at `core.go:889-901`; `861-889`
  is its doc comment. Materialization is **eager at boot** — `gateway.seedJudgeEagerSoul`
  (`pkg/gateway/gateway.go:907-919`) calls `agent.SeedJudgeSoulFile` right after
  `coreagent.SeedConfig` — **plus** a lazy backstop on first dispatch
  (`verifier_adjudication.go:~200-212`, `:864`).
- **Recommendation**: Correct the citation, and add the eager-seed call site to §9 step 3 —
  PlanSupervisor needs its own `seedPlanSupervisorEagerSoul` equivalent, which §9 currently omits.
  (Operator edits are correctly never clobbered: `SeedJudgeSoulFile` returns early on a non-empty
  existing file, `verifier_adjudication.go:233-236`.)

---

#### [MIN-002] `seedSystemAgents` preserves far more than "Model/Provider + SOUL" — R4's mitigation is overstated

- **Lens**: Incorrectness / Insecurity
- **Affected section**: D11, §5 Option B, R4
- **Description**: D11 says the seed contract preserves *"Model/Provider and the SOUL"*. The
  re-enforcement loop (`pkg/coreagent/core.go:1386-1442`) re-enforces exactly nine things — `Locked`,
  `Type`, `Default`, `Name`, `Description`, `Color`, `Icon`, `MemoryEnabled`, and
  `Tools.Builtin.Policies`. **By omission it preserves everything else**: `Home`, `Skills`,
  `MaxToolIterations`, `Subagents`, `FallbackModels`, `Voice`, `ShellPolicy`, and — notably —
  `Tools.MCP`, since only `Tools.Builtin.Policies` is compared (`:1439-1442`). An injected MCP
  tool-policy block on a system agent survives every boot. (SOUL is not a config field at all;
  `seedSystemAgents` performs zero filesystem writes by design, `core.go:1340-1348`.)
- **Recommendation**: Correct D11's list. Then revise R4, which currently says *"Mitigation: none
  needed beyond the existing per-boot re-enforcement, which already covers
  identity/type/locked/tool-policy `[FACT]`"* — it does not cover `Tools.MCP`, `Skills`,
  `Subagents` or `ShellPolicy`, all of which are tamper-relevant for an agent with correction
  authority. Either extend the re-enforcement or state the accepted residual.

---

#### [MIN-003] "Never a chat target" has two live holes that a second System Agent doubles

- **Lens**: Insecurity
- **Affected section**: D1, R4
- **Description**: `IsChatTarget()` (`pkg/config/config.go:1052-1054`) correctly excludes system
  agents and is honoured by routing (`pkg/routing/route.go:139,345,435,445`) and the plan-owner
  roster (`rest_plans.go:71`). But two write paths reject only **workers**, via `isWorkerAgentID`
  (`rest.go:1073-1079`):
  - `POST /api/v1/sessions` (`rest.go:1113-1116`) — an explicit `agent_id: "judge"` is not rejected.
  - The WS chat frame (`websocket.go:1243-1256`) — same worker-only check.

  `rest.go:2903` likewise blocks only `IsWorker()` from being starred as the routing default.
- **Recommendation**: Name this in R4 as an existing gap the change widens, and either fix the two
  checks to `!IsChatTarget()` in the same release or file it explicitly.

---

#### [MIN-004] System-agent membership is duplicated; §9 step 3 lists only one of the two edit sites

- **Lens**: Incompleteness
- **Affected section**: §9 implementation step 3
- **Description**: Membership lives in **two** places: `SystemAgents()` (`core.go:159-163`, returns
  `[]*CoreAgent{Judge()}`) and `systemAgentIDs` (`core.go:146-148`), which backs `IsSystemAgentID`.
  §9 step 3 says *"`SystemAgents()` + `systemAgentSeed` + `PlanSupervisorDefaultRubric`."*
- **Recommendation**: Add `systemAgentIDs` to step 3. Also note `CoreAgent` (`core.go:100-109`) has
  no `Type` field — `Type=system` is applied only at seed time (`core.go:1355`).

---

#### [MIN-005] `requireOwner`'s denial is opaque on only one of two branches

- **Lens**: Insecurity
- **Affected section**: D7 — *"keep the agent-identity clause **and its opaque denial** (sec-MAJOR-2)"*
- **Description**: Verified (`plan_engine.go:2755-2771`): the agent-mismatch branch is genuinely
  opaque (logs server-side at `:2761-2763`, returns a bare `ErrCorrectionNotOwner` with no owner id).
  The **session-mismatch** branch at `:2765` returns a *different* error string ("caller session does
  not match the plan's owner session") and is **not logged**. A caller who already knows the owner
  agent id can distinguish "wrong session" from "wrong agent". FR-9 deletes that branch, so the
  issue self-resolves — but the ADR should say so rather than implying the current denial is
  uniformly opaque.
- **Recommendation**: One clause in D7: *"FR-9's removal of the session clause also removes the one
  branch whose distinct error string leaked a bit of state."*

---

#### [MIN-006] §8's confidence table contradicts D6's own body

- **Lens**: Inconsistency
- **Affected section**: §8 row *"D6 Retain `awaiting_owner_correction` | **Medium**"*
- **Description**: D6's body states `CONFIDENCE: High` and renames the value to
  `awaiting_supervision`. The §8 row says Medium and uses the old name. Similarly the §8 roll-up
  paragraph lists D5/D6/D7 as "removal or delivery questions" — D6 is a rename with a 169-site blast
  radius and a persisted value (MAJ-009), not a removal question.
- **Recommendation**: Reconcile the table with the bodies; update the roll-up sentence.

---

#### [MIN-007] `judge_rounds_exhausted` is already semantically overloaded and supervision makes it visible

- **Lens**: Ambiguity
- **Affected section**: D8, MAJ-008
- **Description**: `FailedReasonJudgeRoundsExhausted` is used for two different things: the real
  round ceiling (`plan_engine.go:1287`) **and** `AppendCorrection`'s honest-exit when
  `planCannotProgress(tasks)` (`:2679`, with `buildUnreachableDoDHandover`). Today nobody calls
  `AppendCorrection`, so the second meaning never surfaces. Wiring it makes users see "judge rounds
  exhausted" for plans that exhausted nothing.
- **Recommendation**: Add a distinct `FailedReason` for structurally-unreachable DoD (e.g.
  `dod_unreachable`) in the same release — it is a `plan.FailedReason` enum addition, so it rides
  the same contract regen as D6's phase rename.

---

#### [MIN-008] D-block ordering is scrambled

- **Lens**: (structural readability)
- **Affected section**: §6 — D1…D7, then D9, D10, D11, then D8
- **Description**: D8 (the authority model, which D9/D10 reference forward) appears last. D9's text
  says "see D9 below" from inside R2, which is above it. §8's table order differs again.
- **Recommendation**: Reorder to D1…D11, or renumber.

---

#### [MIN-009] Owner-disabled sweeps become meaningless for human owners

- **Lens**: Incompleteness
- **Affected section**: FR-3, FR-5, NFR-4
- **Description**: `plan_engine.go:2327` and `:2361` read `OwnerAgentID` to sweep plans whose owner
  agent has been disabled. Under FR-3 the Owner may be a human username, for which "disabled" has no
  meaning and the lookup will not resolve.
- **Recommendation**: Say what those two sites do post-change — most likely skip when
  `owner.kind == human`, which is consistent with NFR-4 (a human owner must never block execution).

---

### Observations

#### [OBS-001] A staged A-then-B sequence would de-risk the one-way door

- **Lens**: Overcomplexity
- **Suggestion**: The UAT does not establish that owner **quality** was the failure — it establishes
  there was **no caller at all** (`AppendCorrection`, zero non-test callers, independently
  corroborated by the honesty note the frontend author left at `src/lib/planStateColors.ts:200-211`).
  Option A (wire `AppendCorrection` to the existing owner) tests the *loop-closing* hypothesis with
  no wire rename, no new agent, no seed change, and no 169-site enum rename — i.e. without opening
  R6's one-way door. If completion rates move, B's adjudication-quality argument can be made on real
  data; if they don't, CRIT-004's alternative causes are confirmed cheaply. Worth at least an
  explicit rejection in §5 ("we considered shipping A first as a probe; rejected because…").
  This is a sequencing comment, not a new feature.

#### [OBS-002] Coupling the verdict to the verb weakens NFR-2's structural argument

- **Lens**: Overcomplexity
- **Suggestion**: D3 asks for one structure carrying met/unmet + per-criterion reasoning + chosen
  verb. Keeping adjudication (an existing, wire-defined `JudgeVerdict`) separate from remediation
  (a `CorrectionRequest` tool call) preserves the clean structural story in D8 — the adjudicating
  output physically cannot express a DoD change — and avoids a new wire type (MAJ-014). Two calls,
  no new schema.

#### [OBS-003] Two doc-comment inaccuracies worth fixing while in the area

- **Lens**: (housekeeping)
- **Suggestion**: `docs/internal/architecture/AS-IS-architecture.md:471` cites `AppendCorrection` at
  `plan_engine.go:2209`; it is at `:2574`. And `pkg/coreagent/core.go:274-279`'s comment says "31
  general" tools while the literal at `:297-308` has 33 (catalog total 83, not 81). Neither blocks
  this ADR; both will mislead the implementer.

#### [OBS-004] ADR-056 hard-depends on the colliding field and pulls the opposite way on session linkage

- **Lens**: Inconsistency
- **Suggestion**: `ADR-056-background-job-visibility.md` (same date, same deciders) declares ADR-055
  Related at `:5`, and its core scoping decision D2/G4 (`:108`, `:182`, `:187`, `:307`) depends on
  ADR-055's `Owner` principal. If CRIT-001 forces a rename or in-place reuse, ADR-056 D2/G4 change
  with it, and ADR-056 has no stated fallback if ADR-055 is reshaped or rejected. Separately,
  ADR-056 R1 (`:258-261`) notes there is no parent index and enumerating a caller's subagents needs
  an O(all sessions) scan **by `OwnerScopeID`**, while ADR-055 D7 proposes retiring the
  `OwnerSessionID`/`OwnsPlanID`/`plan:<id>` linkage — the two ADRs pull on the same session-ownership
  metadata in opposite directions on the same day without cross-referencing on that point. Add a
  gating note to both.

---

## Structural Integrity (Variant B — Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | **FAIL** | No requirement (FR-1…FR-11, NFR-1…NFR-5) carries an acceptance criterion or test. NFR-1's "~1 wake per plan" has no measurement method; R5's proposed mitigation counts *call sites*, not wakes, and D5 routes 3 of 5 sites to PlanSupervisor including the MET-synthesis at `:1571`, so the happy path is at least 2 invocations — NFR-1 is already violated by D5 as written. |
| Cross-references are consistent | **FAIL** | MAJ-002 (five non-existent ADR-053 anchors); MIN-006 (§8 table vs D6 body); MIN-001 (`core.go:861-889`); MIN-008 (D-block ordering). |
| Scope boundaries are explicit | **PARTIAL** | §1 states the blast radius clearly — but CRIT-004 shows the excluded area (member dispatch) contains a co-equal root cause of the very failure being cited as motivation. |
| Success criteria are measurable | **FAIL** | The implicit success criterion is "plans reach `done`." No target is stated. Given CRIT-004, "2 of 11 → N of 11" is not attributable to this change alone. |
| Error/failure scenarios addressed | **FAIL** | MAJ-015: no failure model for PlanSupervisor itself. MAJ-008: corrections unbounded. MIN-009: owner-disabled sweeps. No handling for malformed verdicts or invalid verbs. |
| Dependencies between requirements identified | **PARTIAL** | D9→D10 is stated. FR-8→`requireOwner` (MAJ-001), FR-3→`validatePlanOwnerAgent` (MAJ-012), and D6→persisted phase values (MAJ-009) are all undeclared. |
| Upstream decisions reversed are declared | **FAIL** | MAJ-003: ten undeclared, including a `[P2]` MUST-NOT (spec FR-146) directly prohibiting D1's central decision, and two `[P1]` MUSTs (FR-118, FR-147) contradicted by D7. |

---

## Test Coverage Assessment

The ADR specifies no testing strategy. Beyond D8's `Would improve` line (a regression test asserting
`CorrectionRequest` gains no DoD field) and R5's call-site-count assertion, no verification is
described. Gaps by category:

| Category | Gap | Affected requirement |
|---|---|---|
| **Regression (engine writes)** | No test that the D9 immutability guard leaves the 10 runtime `Patch` fields writable in `running`. This is the CRIT-002 tripwire — without it, a blanket guard passes review and bricks the engine at runtime. | FR-11, D9 |
| **Authorization** | No test that PlanSupervisor *can* correct and that every other agent (including the Owner) cannot, post-`requireOwner` rewrite. | FR-8, FR-9, NFR-3, MAJ-001 |
| **Privilege** | No test asserting PlanSupervisor's **effective** policy (after `resolveEffectivePolicyWith` ceiling composition) denies `bash`/`write_file`/`set_config`/`create_agent`. Boot validation will not catch a missing map. | NFR-2, MAJ-005, MAJ-007 |
| **Availability / failure injection** | No test for PlanSupervisor unavailable, malformed verdict, invalid verb, or nonexistent member id. The judge's `Unavailable` path (`plan_engine.go:1478-1495`) is the model to mirror. | NFR-4, MAJ-015 |
| **Bounds / cost** | No test that corrections terminate. A supervisor that appends tail members in response to its own UNMET verdicts has no ceiling today. | NFR-1, MAJ-008 |
| **Migration / persistence** | No test that an on-disk plan carrying the pre-rename `awaiting_owner_correction` phase (9 of 11 UAT plans) behaves correctly after upgrade. | D6, G3, MAJ-009 |
| **Contract drift** | `make verify-contracts` must be green after the `Plan.yaml` change; `pkg/api/generated/contract_test.go` must still pass with the new owner shape. Not mentioned. | Constraint #8, CRIT-001 |
| **Human principal** | No test for `owner.kind=human` with an empty username (dev-mode bypass path, `rest_workspaces.go:66`) — FR-5's stated invariant. | FR-5, MAJ-012 |
| **E2E** | `tests/e2e/conformance-design-e2e.spec.ts` references the old phase 17 times; no plan for updating it, and no new E2E for the correction round-trip. | D6, FR-8 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| PlanSupervisor agent | ok | **risk** | **risk** | ok | **risk** | **risk** | T: tool grant unspecified (MAJ-006); `seedSystemAgents` does not re-enforce `Tools.MCP`/`Skills`/`Subagents`/`ShellPolicy` (MIN-002). R: no audit of supervision verdicts or tool-path corrections (MAJ-016). D: corrections unbounded → token/compute amplification (MAJ-008). E: missing policy map silently inherits `allow` for `bash`/`write_file`/`set_config`/`create_agent` (MAJ-005). |
| Correction path (`AppendCorrection`) | **risk** | ok | **risk** | ok | **risk** | **risk** | S: `requireOwner`'s predicate is undefined post-change (MAJ-001). T: `CorrectionRequest` genuinely has no DoD field — verified. R: tool-path corrections unaudited. D/E: see above. |
| REST correction route (FR-8) | **risk** | **risk** | ok | ok | **risk** | **risk** | Auth posture entirely unspecified. `PUT /plans/{id}` today carries no `adminWrap`/`RequireNotBypass` (`rest.go:4838-4839`); inheriting that means dev-mode bypass reaches plan correction. No rate limit stated. |
| `Owner` principal (human) | **risk** | ok | ok | ok | ok | ok | S: username is the only identifier, no stable id, renameable by hand-editing `config.json` with nothing repairing dangling refs (MAJ-012). Empty under dev-mode bypass. |
| Notification delivery | ok | ok | ok | **risk** | ok | ok | I: WS fan-out filters on `wc.userID == p.Recipient` (`websocket.go:3364-3400`) — correct today, but the ADR does not state that a plan synthesis (which may quote member output) is user-scoped content, nor whether the `NotificationAdminBroadcast` sentinel path could ever carry one. |
| `Plan` wire type | ok | **risk** | ok | ok | ok | ok | T: `additionalProperties: false` with a required `owner` being retyped string→object, no migration (CRIT-001). |

**Legend**: risk = identified threat not mitigated in the ADR; ok = adequately addressed or not applicable.

---

## Unasked Questions

1. **Why does `Plan.Owner` already exist, and why doesn't the ADR mention it?** It holds exactly the
   principal FR-3 defines. Is the intent to upgrade it in place, or was it simply not found?
2. **What is `requireOwner`'s predicate after this change?** Without an answer, no correction can
   execute (MAJ-001).
3. **Exactly which tools does PlanSupervisor hold?** (MAJ-006 — and NFR-2's integrity claim depends
   on the answer, MAJ-007.)
4. **What bounds a correction sequence?** (MAJ-008.)
5. **What happens when PlanSupervisor is unavailable, or emits an unparseable verdict?** (MAJ-015.)
6. **How many of the 9 parked UAT plans would this ADR actually have unblocked?** The showcase case
   (all members done, 1/1) had nothing to correct. (CRIT-004.)
7. **What does PlanSupervisor do when woken for a `stalled` plan?** (MAJ-010.)
8. **What happens to plans on disk already carrying `awaiting_owner_correction`?** 9 of 11 UAT plans
   were in that state. (MAJ-009.)
9. **Does spec FR-146's "never a new Planner agent (BOM)" apply to a System Agent?** If yes, D1 is
   prohibited; if no, say why the Judge precedent exempts it. (MAJ-003.)
10. **What is the auth posture on the FR-8 REST correction route?** (MAJ-016.)
11. **Is a human owner's username stable enough to be a persisted foreign key?** There is no user id
    and no rename support. (MAJ-012.)
12. **What is the rollback story?** R6 declares a one-way door, FR-4 deletes the human's
    adjudication role, and there is no flag. If PlanSupervisor adjudicates badly, what does an
    operator do? (MAJ-016.)
13. **Which release phase is this?** Per `CLAUDE.md`'s routing rule, plans/agents/tasks work is v0.3
    (#156); the ADR does not say, and its wire break is not v0.2-shaped.
14. **Should ADR-056 be gated on this ADR's wire decision?** It hard-depends on `Owner{Kind,ID}`
    (OBS-004).

---

## Verdict Rationale

**BLOCK.** Four findings independently justify it. CRIT-001 makes the ADR's own implementation
instruction (§9 step 1) a silent breaking change to a required wire property whose existing meaning
is the very thing FR-3 claims to introduce — and the ADR does not know that field exists. CRIT-002
makes D9's enforcement instruction fatal to the plan engine under its most natural reading, on a
change where a single misread ships a system that cannot run a plan. CRIT-003 shows two
High-confidence decisions resting on `[FACT]` claims that current code falsifies. CRIT-004 shows the
motivating evidence does not support the causal claim: the report cited names a separate, diagnosed
dispatcher bug inside the ADR's declared out-of-scope area, and the single case quoted as the
showcase failure is one this ADR's three correction verbs structurally cannot fix.

MAJ-001 is nearly as serious in effect: the gate that must permit PlanSupervisor to correct is never
reconciled with the ADR's own redefinition of Owner, so as written the loop still does not close.
MAJ-003 is the governance failure — spec FR-146 `[P2]` states the re-planner *"MUST be delivered by
EXTENDING `pkg/skills/embedded/plan/SKILL.md` — **never a new Planner agent (BOM)**"*, which is a
direct prohibition on D1 that the ADR neither cites nor argues against, alongside two `[P1]`
requirements (FR-118, FR-147) that D7 contradicts.

The recommended architecture may well still be right — the System-Agent reuse argument is sound and
verified, `CorrectionRequest`'s DoD-free shape is real, and the correction loop genuinely has zero
callers. But it cannot proceed to `/plan-spec` on this evidence base: three of its `[FACT]`-labelled
claims are wrong, its largest declared risk is already solved, its cost argument for Constraint #6
is inverted in the security-relevant direction, and its central wire change collides with an
existing field.

### Recommended Next Actions

- [ ] Resolve the `Plan.Owner` / `owner` collision with an explicit decision block and a full field
      inventory — CRIT-001, MAJ-013
- [ ] Rewrite D9 as a six-field allow-list (`Title`/`Goal`/`Description`/`OwnerAgentID`/`DoD`/`Bounds`),
      naming the ten runtime fields that stay writable — CRIT-002
- [ ] Correct D9/D10's `[FACT]` evidence against `rest_plans.go:717-736`; decide `Bounds` explicitly
      against the deliberate exemption at `:715-716`; delete R2/R3 — CRIT-003
- [ ] Rewrite §1 to attribute the UAT shortfall to ≥3 causes; gate this ADR's value on the
      dispatcher fix; re-score decision criterion 1 — CRIT-004
- [ ] Specify `requireOwner`'s post-change predicate and the FR-8 route's auth — MAJ-001, MAJ-016
- [ ] Fix the Amends line and add a `§ Upstream decisions this ADR reverses` section; argue FR-146
      explicitly — MAJ-002, MAJ-003
- [ ] Replace G2/R1 with the four-item `pkg/notifications` work list (enum extension, `plan_id`,
      dedup key, engine injection) — MAJ-004
- [ ] Invert the Constraint #6 paragraph and mandate `denyAllThenOverride` + an effective-policy
      test — MAJ-005
- [ ] Add `D12 — PlanSupervisor tool grant` (literal list) and restate NFR-2 as a conjunction —
      MAJ-006, MAJ-007
- [ ] Add a correction bound (counter + ceiling + terminal reason) — MAJ-008, MIN-007
- [ ] Extend G3 to the persisted phase value and decide migration vs. read-time coercion — MAJ-009
- [ ] Define PlanSupervisor's behaviour on `stalled`, and whether `stalled` survives the rename —
      MAJ-010
- [ ] Rewrite D6's crash-safety basis to cite `bootReconcile`; close G4 as "yes, resolvable from
      plan state" and promote D7 to High — MAJ-011
- [ ] Add the human-principal decision (validator split, bypass empty-username, no stable user id) —
      MAJ-012
- [ ] Decide the verdict/verb shape and add any contract step to §9 — MAJ-014
- [ ] Add `D13 — PlanSupervisor unavailability` mirroring the judge's `Unavailable` contract —
      MAJ-015
- [ ] Add an operability section: audit events, verdict persistence, rollback/kill-switch, runbook —
      MAJ-016
- [ ] Sweep the MINOR citation corrections (MIN-001, MIN-002, MIN-004, MIN-006, MIN-008) and add
      gating notes to ADR-056 — OBS-004
