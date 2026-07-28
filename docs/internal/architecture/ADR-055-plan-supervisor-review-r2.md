# Adversarial Review (Round 2): ADR-055 — PlanSupervisor

**Spec reviewed**: `docs/internal/architecture/ADR-055-plan-supervisor.md` (v2, Proposed, 2026-07-27)
**Prior review**: `docs/internal/architecture/ADR-055-plan-supervisor-review.md` (v1 — BLOCK, 33 findings)
**Review date**: 2026-07-27
**Input mode**: structured-spec (Variant B)
**Verdict**: **BLOCK**

## Executive Summary

v2 genuinely closes most of v1's evidence defects — the `Owner{Kind,ID}` invention is gone,
D9 correctly defers to the existing REST guard, and the UAT causal claim is now honest. But
v2's own headline — *"a materially smaller change … one gate fix, one agent, one wiring"* —
is falsified by the document that contains it: **D14 adds a ~157-file, ~1000-occurrence rename
that deletes a required wire field, breaks two `[P1]` MUSTs, and renames persisted state in two
stores that have no migrator** — and it flatly contradicts D2 and D7 in the same document.
Beyond that, four load-bearing assertions are false against the working tree: outcome
notifications cannot reach an agent Owner at all, the parked phase has no restart re-wake,
PlanSupervisor is chat-addressable so its exclusive correction authority is trivially borrowed,
and `supersede` lets the adjudicator satisfy a DoD by deleting the evidence rather than meeting
it.

| Severity | Count |
|----------|-------|
| CRITICAL | 7 |
| MAJOR | 16 |
| MINOR | 14 |
| OBSERVATION | 4 |
| **Total** | **41** |

**Recurrence note.** v1's failure mode was *"proposes building something that already exists,
or would break production."* v2 repeats it in three places (G4 is answered by a five-minute
read of a file the ADR ships; D5's notification claim inverts the actual gap; D8 cites a file
that does not exist). The ADR's research discipline improved on the items it was told about
and did not generalise.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] D2 and D14 give opposite instructions for `owner_agent_id`; D14's is a larger one-way door than the one v1 was blocked for

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: D2 disambiguation table (line 228); D14 scope table row 2 (line 520); FR-11
- **Description**: The two decisions contradict each other verbatim.

  D2: *"`owner_agent_id` | Pre-existing field: the agent woken at decision points. That role
  moves to PlanSupervisor (D5); **the field itself is left in place (D7)**."*

  D14 row 2: *"`Plan.OwnerAgentID` | **delete** — wake target is PlanSupervisor (D5) | 67 |
  340 | yes — required field"*

  Verified: `owner_agent_id` is in the `required:` list of
  `contracts/components/schemas/Plan.yaml:23` under `additionalProperties: false`. Deleting a
  **required** wire property with no migration is precisely the one-way door v1's CRIT-001
  blocked — v2 removed it from `owner` and re-introduced it on `owner_agent_id`, one field
  over, while the v2 changelog claims *"v2 is a materially smaller change."*

  The deletion rationale ("its job moves to PlanSupervisor") describes **one** of the field's
  **eight** live jobs. The others, none of which the ADR names:
  1. `pkg/agent/plan_engine.go:2758` `requireOwner` — the correction gate's entire subject.
     Delete the field and D3's *"MUST continue to deny every other non-owner caller"* and FR-6
     have no "owner" to compare against. **The ADR's own core decision becomes unspecifiable.**
  2. `pkg/agent/plan_engine.go:1398` — `AssigneeAgentID: p.OwnerAgentID` in
     `JudgeCriteriaInput`. The plan-level judge loses the assignee it attributes verdicts to.
  3. `pkg/agent/plan_engine.go:2327` `setPausedForOwner`, wired from
     `pkg/gateway/rest_workspaces.go:975,1016` — disabling an agent pauses its running plans.
  4. `pkg/agent/plan_engine.go:2361` `HasActivePlansOwnedBy`, called from
     `pkg/gateway/rest.go:2660` — the 400-on-delete-while-owning-active-plans guard becomes a
     no-op, so agents get deleted out from under running plans.
  5. **Two independent** owner validators — `pkg/gateway/rest_plans.go:63-78`
     `validatePlanOwnerAgent` and `pkg/agent/loop.go:4292-4306`
     `validatePlanOwnerAgentForTool` — both rejecting System Agents and workers via
     `IsChatTarget()`. The field is validated as a *chat persona*, not a wake address. (Note the
     irony: PlanSupervisor is a System Agent, so it could never have been assigned to this field
     anyway — the "its job moves to PlanSupervisor" framing was never coherent.)
  6. `pkg/tools/plan.go:229-231` — `create_plan` **requires** it
     (`ErrorResult("owner_agent_id is required")`). Deleting it is an agent-facing tool-schema
     break, which also changes `pkg/skills/embedded/plan/SKILL.md`.
  7. `pkg/gateway/rest_plans.go:717-736` — the D9 freeze the ADR celebrates is a freeze *on
     this field*. D9 and D14 row 2 cannot both ship.
  8. SPA: the owner chip (`src/components/workspaces/PlansFilterBand.tsx:251`) and the
     create/edit form's required-field validation
     (`src/components/workspaces/CreatePlanSlideOver.tsx:44,65,76,132,138,148,193,203,300,310`).

  Spec **FR-140** `[P2]` (`docs/internal/specs/unified-goal-plan-subagent-spec.md:1515`) —
  *"Every plan MUST run inside a persistent owner agent session"* — is anchored on it and is
  never cited.

  The field is also `required` with `additionalProperties: false` on
  `contracts/components/schemas/PlanCreateRequest.yaml:13-14`, enforced at **runtime** by
  `decodeAndValidate` (`pkg/gateway/rest_plans.go:522,698`) against the embedded schema copy —
  so deletion makes every currently-shipped client 400 on plan creation.

  Finally, the row's own measurement is contaminated. Of the 340/408 occurrences, roughly **40%
  are not this field**: ~69 belong to `Schedule.owner_agent_id`, a different entity
  (`Schedule.yaml`, `ScheduleCreate.yaml`, `pkg/gateway/schedules.go`, `ScheduleFormSheet.tsx`),
  and ~28 are `ownerAgentID` **local variables** in the scheduled-session path
  (`pkg/agent/loop.go:5370-5451`, `loop_scheduler.go`, `loop_command.go`,
  `pkg/session/unified.go`). Real in-scope figure ≈ **46 files / ~245 occurrences**.
- **Impact**: An implementer following D2 leaves the field; one following D14 deletes it and
  silently removes the correction gate's authority subject, the judge's assignee, the
  agent-disable cascade, the agent-delete guard, two owner validators, a required tool
  argument, and the D9 freeze. Every persisted plan and every shipped client breaks. This is
  the same class of defect that produced the v1 BLOCK.
- **Recommendation**: Resolve to **one** instruction. Recommended: strike D14 row 2 entirely
  and keep D2's "left in place" — the field is still validated, frozen, and required, and its
  wake role moving to PlanSupervisor does not make it dead. If deletion is genuinely wanted,
  it needs its own ADR with all five consumers enumerated, a `Plan.yaml` `required` change, a
  persisted-record migration, and an FR-140 override argued the way D1 argues FR-146.

---

#### [CRIT-002] D14 renames persisted fields in two stores that have no migration mechanism — bricking parked plans and silently mis-sweeping live sessions

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: D6; D14 rows 1, 3, 4, 5; §9 step 6 (*"Survey persisted records for old
  values first"*); D14 *"Migration: no back-compat, per the ADR-035/037/054 precedent"*
- **Description**: **The repo has no schema-versioning or upgrade-on-read mechanism for either
  affected store.** `rg 'schema_version|SchemaVersion'` over non-generated Go returns **zero**
  hits. `pkg/plan` and `pkg/session` contain no migrator. Load is a bare `json.Unmarshal`
  (`pkg/plan/store.go:85`) and `pkg/plan/store.go:76` is explicitly commented *"It never
  rewrites on read."* A renamed key therefore decodes to the **zero value** with no error, no
  warning, and no log line — and the next write erases the old key from disk.

  **(a) Plans — the record becomes immortal, and the SPA throws.** Every write re-validates:

  ```go
  // pkg/plan/store.go:503  (updateLocked, after applying the patch)
  if err := p.normalize(); err != nil { return nil, err }
  // → pkg/plan/plan.go:494
  if p.PlanPhase != "" && !IsValidPlanPhase(p.PlanPhase) {
      return verr("invalid plan_phase %q", p.PlanPhase)
  }
  ```

  The comment at `store.go:498-502` documents the exact assumption the rename invalidates:
  *"Every field normalize() re-validates … already has an on-disk value that passed normalize()
  at Create time or a prior Update, so these re-checks are a no-op."* After the rename,
  `awaiting_owner_correction` is no longer in `validPlanPhases` (`plan.go:261-268`), so a plan
  persisted with it **loads fine and fails every subsequent `Update`** — it cannot be stopped
  (`StopPlan` → `Update`), corrected, or failed. It is an immortal record.

  On the SPA side it is worse than a drop: `src/lib/api.ts:842-850` **throws `ApiSchemaError`**
  on any zod mismatch, and `plan_phase` is a closed enum
  (`src/lib/api/generated/schemas.ts:9167`). One un-migrated plan file therefore **throws the
  whole plans list and plan detail**, not just that row. Note `state` has a documented
  forward-compat fallback (*"An unrecognized future value should render as draft"*,
  `Plan.yaml:76-77`); `plan_phase` has none. The value is also in `PlanStatusFrame.yaml:46` and
  `contracts/asyncapi.yaml:3363`, so a live tab breaks on the first WS frame carrying it.

  **(b) Session lifecycle — silent mis-classification, and the records are append-only JSONL.**
  Row 4 renames `OwnerScopeKind`/`OwnerScopeID`, which are persisted per-session at
  `$OMNIPUS_HOME/session_lifecycle/<id>.jsonl` (`pkg/session/lifecycle.go:265-281`).
  `owner_scope_kind` is `required` on `SessionLifecycleRecord.yaml:20` and carries **no
  `omitempty`**. Consequences of decoding a legacy record to `""`:
  - `pkg/agent/boot_sweep.go:295` — `if rec.OwnerScopeKind == "" { return false }` is the
    `isNeedsInputReconstructable` gate. Every pre-rename `needs_input` session is classified
    non-reconstructable and swept to `failed(interrupted)`. **Silent loss of live work.**
  - `pkg/session/lifecycle.go:416-421` — persist rejects an empty/invalid kind, so a legacy
    record becomes **untransitionable** the first time anything tries to move its state.
  - `pkg/tools/message_parent.go:409-410` — parent routing falls through to *"could not resolve
    this session's parent/owner scope"*.

  Unlike the plan store, these are **append-only JSONL with an immutable-terminal invariant**,
  so an in-place rewrite has no precedent in the codebase.

  D14 says *"no back-compat, per the ADR-035/037/054 precedent."* That precedent removed fields
  nobody had persisted **in a live state machine**. Here the affected records are *specifically
  a plan sitting in `awaiting_owner_correction`* — the exact population this ADR exists to
  unblock, and the one the 2026-07-26 UAT says is accumulating — and *specifically a paused
  `needs_input` session*. §9 step 6 asks for a survey and specifies **no action if the survey
  finds any**. (A survey of this dev box does come back clean — `~/.omnipus` has no `plans/` or
  `session_lifecycle/` dir and no fixture holds an old value — but that is a statement about
  this machine, not about installed users.)
- **Impact**: An operator upgrading with parked plans loses the ability to stop or clear them by
  any in-app route **and** the Plans screen throws; an operator with paused `needs_input`
  sessions loses them to the boot sweep without a log line. Both are data loss in the strict
  sense, and the ADR's own stated fallback ("stop and re-author") is a path that breaks.
- **Recommendation**: A read-path migration is mandatory, and **the codebase already has the
  pattern** — reuse it rather than inventing one. `pkg/task/migrate_planning_status.go`
  (`MigratePlanningStatusToNext`, ADR-051 D5) and `pkg/task/migrate_milestones.go`, both invoked
  at store construction (`pkg/task/store.go:89,92`): stat a sentinel file first and return with
  zero I/O if present; walk the store dir; rewrite in place; log-and-skip per-file errors; write
  the sentinel **only after every file succeeded**, so a partial failure retries next boot. It
  even keeps the legacy literal as an unexported const outside the live vocabulary — exactly
  what this rename needs. Two new migrators are required (`$OMNIPUS_HOME/plans/*.json` for rows
  1/2/3; `$OMNIPUS_HOME/session_lifecycle/*.jsonl` for rows 4/5), and the JSONL one needs an
  explicit decision on rewriting an append-only log. Add the BDD scenarios: *"Given a plan
  persisted at `awaiting_owner_correction`, When the upgraded gateway boots, Then the plan is
  readable, stoppable, and renders in the SPA"* and *"Given a paused `needs_input` session
  persisted with `owner_scope_kind`, When the upgraded gateway boots, Then it is not swept."*

---

#### [CRIT-003] Outcome notifications to an agent Owner are silently discarded — FR-4, FR-9, D10 and NFR-4 are unimplementable for the default case

- **Lens**: Incorrectness / Inoperability
- **Affected section**: FR-4; FR-9; NFR-4; D5 (*"Human-owner notification is not a risk … The
  real work is four small gaps"*); D10 (*"fails safe and visible"*)
- **Description**: D5 asserts the outcome surface already exists. It exists **for humans
  only**, on both halves:
  - Read path: `pkg/gateway/schedules.go:1256` — `a.notifStore.ListForUser(user.Username)`,
    resolved from the auth context. There is no agent-facing read API.
  - Live push: `pkg/gateway/websocket.go:3376` — `if p.Recipient != …AdminBroadcast && wc.userID != p.Recipient { continue }`, where `wc.userID` is a username set at auth
    (`websocket.go:707,745`). An agent has no WS connection and no `userID`.
  - The repo already documents this failure mode: `schedules.go:607-612` — *"the admin-broadcast
    sentinel is NOT a real username — persisting it writes `_admin_.json` which no
    `ListForUser(username)` ever reads."*

  `Store.Create` will happily write `<agent-id>.json` (the recipient is only a sanitized
  filename, `pkg/notifications/store.go:83-87`). **Nothing ever reads it.**

  Now join that to D2, which the ADR treats as its safest decision: `Plan.Owner` holds an
  **agent id** on the tool path (`pkg/tools/plan.go:286` `Owner: callerID`) and a **username**
  on the REST path (`pkg/gateway/rest_plans.go:547` `Owner: c.Username`). Agent-created plans
  are the normal case — `create_plan` is a granted agent tool. So for the majority of plans,
  every outcome notification the ADR specifies is written to a file no reader opens.
- **Impact**: FR-4 (*"The Owner … receives outcome notifications"*) is false for most plans.
  D10's failure model — the one thing standing between "PlanSupervisor is down" and "the plan
  is silently stuck forever" — **silently no-ops**, which is the exact outcome D10 exists to
  prevent. NFR-4 enumerates *"missing, malformed or human"* owners and never considers the
  agent owner, i.e. the majority. D5's *"four small gaps"* is materially incomplete.
- **Recommendation**: Decide, in this ADR, how an agent principal is notified. Two coherent
  options: (a) outcome events to an agent Owner keep using the existing `wakeOwner` bus path
  and only *human* Owners use `pkg/notifications` — then D5's table needs a third column for
  owner kind and FR-4 needs rewriting; or (b) give `Notification` an explicit
  `recipient_kind: user|agent` and an agent read path — a much larger change than "four small
  gaps". Either way, add the negative test: *"Given a plan whose owner is an agent id, When the
  plan terminates, Then the owner receives the outcome on a surface it can actually read."*

---

#### [CRIT-004] PlanSupervisor is chat-addressable, so D3's exclusive correction authority is borrowable by any user

- **Lens**: Insecurity (Elevation of Privilege)
- **Affected section**: D1 (*"never a chat target"*); D3; NFR-3; FR-6
- **Description**: D1 asserts System Agents are *"never a chat target"*. `IsChatTarget()`
  (`pkg/config/config.go:1052-1054`) does exclude them, and routing honours it — but **two
  write paths reject only workers**, and v1 reported this as MIN-003; v2 does not mention it:
  - `POST /api/v1/sessions` — `pkg/gateway/rest.go:1118` gates on `isWorkerAgentID` only.
  - The WS chat frame — `pkg/gateway/websocket.go:1243` gates on `isWorkerAgentID` only.

  Under this ADR, PlanSupervisor is the sole holder of the correction grant. D3 authenticates
  the **agent identity**, not the intent. Any authenticated user who can open a session against
  `plansupervisor` can simply ask it to apply a correction to any plan, and the gate passes —
  because the caller *is* PlanSupervisor.

  A second hole compounds it: `PUT /api/v1/agents/{id}` with `default:true` gates on
  `IsWorker()` only (`pkg/gateway/rest.go:2903`), and its own comment falsely asserts *"the
  worker guard already rejected this request above … so `id` is always a valid chat-target
  here."* `AgentRegistry.GetDefaultAgent()` (`pkg/agent/registry.go:337-345`) likewise filters
  workers only — `AgentInstance` has no `IsSystem()` (`pkg/agent/instance.go:783`). So a System
  Agent can be starred as the default agent and returned to ~15 callers.
- **Impact**: NFR-3 (*"No agent other than PlanSupervisor may reach correction"*) is satisfied
  literally and defeated in practice. The ADR's entire integrity argument — a purpose-built,
  locked, non-chattable adjudicator — rests on a property the code does not enforce, and this
  ADR doubles the blast radius of the existing hole by adding a second, more privileged System
  Agent behind it.
- **Recommendation**: Make closing both holes a **prerequisite in this ADR**, not a follow-up:
  change `pkg/gateway/rest.go:1118` and `pkg/gateway/websocket.go:1243` from `isWorkerAgentID`
  to a `!IsChatTarget()` check, and add `IsSystem()`/`IsChatTarget()` to `AgentInstance` so
  `GetDefaultAgent` and `rest.go:2903` agree with routing. Add the negative test: *"Given a
  user opens a chat session addressed to `plansupervisor`, Then the request is rejected 400."*

---

#### [CRIT-005] NFR-2's integrity guarantee is defeated by the correction verbs this ADR wires: `supersede` lets the adjudicator delete the evidence instead of meeting the DoD

- **Lens**: Incorrectness / Insecurity (Tampering)
- **Affected section**: NFR-2; FR-5; D8's *"structural guarantee"*; §9 success criterion
  *"The DoD is byte-identical across a full correction cycle"*
- **Description**: The ADR's whole integrity story is *"PlanSupervisor MUST NOT alter the DoD
  it judges"*, evidenced by `CorrectionRequest` having no DoD field
  (`pkg/agent/plan_engine.go:2410-2418` — verified, it does not). But the DoD is not the thing
  the judge reads. The judge reads the DoD **against the member outcomes**, and `supersede`
  changes exactly that: per spec FR-143 and ADR-053 D4, SUPERSEDE *"marks a done member's
  outcome ignored-by-Judge."* `validateMemberRef` (`plan_engine.go:2725`) permits it on any
  member with status `done`.

  So an adjudicator facing an UNMET verdict has a legal move that is not "fix the work": it can
  supersede the `done` member whose output caused the UNMET, append a thinner member, and
  re-judge. Nothing in the ADR — no rubric constraint, no rung, no rate limit, no reviewer —
  distinguishes that from a legitimate correction. With D4's budget it has up to
  `plan_judge_max_rounds` (default 20, `pkg/config/planning.go:14`) attempts to reshape the
  evidence set in its own favour.

  The §9 success criterion *"The DoD is byte-identical across a full correction cycle"* is
  therefore a tautology that tests nothing — `CorrectionRequest` structurally cannot carry a
  DoD. It gives false assurance about the risk that actually exists.
- **Impact**: The ADR's own decision criterion — *"Integrity (cannot self-lower the bar) | 25% |
  An adjudicator that can rewrite its DoD is worthless"* — is not met. A plan can reach `done`
  because the supervisor removed the failing evidence, and the audit trail (D13) records it as
  a routine `supersede` revision entry. This is worse than a stuck plan: it is a **false
  success**, and the plan is then frozen (`done` is terminal).
- **Recommendation**: This is a real architectural gap, not a wording fix. Options to decide in
  this ADR: (a) deny PlanSupervisor the `supersede` verb entirely and grant only `append` +
  `targeted_retry` — the two verbs that add work rather than remove evidence; (b) require a
  `falsified_assumption` on every `supersede` and make the plan judge re-read superseded
  outcomes as *contradicting* evidence rather than absent evidence (the ADR-053 G-3
  "unable to verify ≠ absent evidence" precedent); or (c) cap supersedes per plan
  independently of the round budget. Replace the byte-identical success criterion with:
  *"Given a plan whose DoD is UNMET solely because member M's outcome is wrong, When
  PlanSupervisor supersedes M without appending replacement work, Then the plan judge still
  returns UNMET."*

---

#### [CRIT-006] FR-5's REST correction route has no authorization model, on a surface that has none to inherit

- **Lens**: Insecurity (Elevation of Privilege, Denial of Service)
- **Affected section**: FR-5 (*"plus a REST route for human parity"*); FR-4; NFR-3; D3
- **Description**: Three problems, none addressed.

  1. **It contradicts FR-4.** FR-4 says the Owner *"has **no** adjudication or correction
     role"*, and D3 repeats *"correction is PlanSupervisor's alone; the Owner stops or
     resumes."* FR-5 then adds a human correction route. Who is authorized on it? If the Owner:
     FR-4 is false. If not the Owner: some *other* human has rights the Owner lacks. The ADR
     never says.
  2. **There is no per-plan authorization to inherit.** Verified across
     `pkg/gateway/rest_plans.go`: `HandlePlans` (`:611-644`) dispatches `approve`, `stop`,
     `restart`, GET/PUT/DELETE with **no owner check on any of them**; `callerIdentity` appears
     twice and only for attribution (`:542` creation stamp, `:1050` stop attribution). So a
     correction route registered under `a.withAuth` grants **every authenticated user
     correction rights on every plan** — while D3 spends its entire body ensuring no *agent*
     can. NFR-3's threat model covers agents and ignores humans.
  3. **It is a DoS lever.** `AppendCorrection` takes the process-wide `planDecisionMu` for its
     whole body (`plan_engine.go:2575-2576`), the same mutex held by `processPlan`, `StopPlan`,
     the judge round, and idle expiry. An HTTP-reachable handler that acquires the global
     plan-decision lock, does an intent-log write, and mutates the DAG has no rate limit
     specified.

  Compounding: the route has **no client**. §9 step 5 revises chip copy only; no SPA correction
  UI is in scope. A human would have to hand-author a `CorrectionRequest` containing
  `TailMembers []task.Task` and `TailEdges []IntentEdge` as raw JSON. So FR-5 ships a new
  privileged, unauthorized, unrate-limited mutation endpoint that nothing calls.
- **Impact**: The ADR closes the agent path with a hard identity gate and simultaneously opens a
  wider human path with no gate at all, delivering no user-facing capability in exchange.
- **Recommendation**: Either (a) **cut FR-5's REST route from this ADR** — it delivers nothing
  without a SPA client and can ship with the UI that needs it; or (b) specify its authz
  explicitly: which principal, checked against what (`Plan.Owner`? admin role?), what status
  code on denial (and make it match D3's opaque denial so the two paths cannot be
  differentially probed — NFR-3), whether `RequireNotBypass` applies, and what rate limit
  bounds `planDecisionMu` acquisition. Then reconcile with FR-4 in one sentence.

---

#### [CRIT-007] FR-9's *"Execution never silently stalls"* is false today and this ADR adds no mechanism; D6's restart-re-wake justification is a capability that does not exist

- **Lens**: Incorrectness / Inoperability
- **Affected section**: FR-9; D6 (*"a restart mid-adjudication can re-wake PlanSupervisor"*);
  D10
- **Description**: D6 retains the durable parked phase on the ground that it is *"the on-disk
  record that a decision is outstanding, so a restart mid-adjudication can re-wake
  PlanSupervisor."* Traced end to end, no such re-wake exists:
  - `bootReconcile` (`plan_engine.go:3176-3206`) rehydrates the F2 signature and calls
    `processPlan` for running plans.
  - `processPlan`'s phase switch (`:844-866`) special-cases only `PhaseJudging` and
    `PhaseSynthesizing`. **`PhaseAwaitingOwnerCorrection` is not handled.** It falls through to
    `beginPlanJudgeRound`, hits the F2 unchanged-signature gate (`:1293-1301`), and silently
    re-parks. **No `wakeOwner` is issued.**
  - The only wake for this phase is the single one at `:1542`, fired once at the UNMET verdict.

  That single wake is also best-effort: `wakeOwner` is a no-op when `pe.notifier == nil`
  (`:2097-2099`), and a bus publish failure is logged at WARN and swallowed (`:2109-2111`).
  There is no retry, no re-notify, no timeout escalation.

  Compounding it, the boot-sweep exemption that was supposed to keep the owner's session alive
  across the restart is confirmed **dead**: it keys on `session.LifecycleRecord.OwnsPlanID`
  (`pkg/agent/boot_sweep.go:160-165`), which has **zero non-test writers** — so the exempt
  branch never runs and the session is swept to `failed(interrupted)` (`:181`). The ADR
  acknowledges this (D6, citing MAJ-011) but treats it as a footnote rather than as the reason
  its own re-wake claim fails.
- **Impact**: One dropped bus publish, one gateway restart, or one nil notifier and the plan is
  parked forever with nobody woken and nobody notified — the precise failure this ADR exists to
  eliminate, now reachable through three independent paths. FR-9's *"Execution never silently
  stalls"* is stated as a requirement with no mechanism behind it, and D10's failure model
  covers only "PlanSupervisor is down", not "the wake was lost".
- **Recommendation**: Specify the re-entry mechanism as a first-class requirement, not an
  assumption. Concretely: add a `PhaseAwaitingSupervision` case to `processPlan`'s boot switch
  that re-issues the supervision wake (idempotently, guarded by the persisted signature the way
  `surfaceStallIfAny` already dedups at `:1245-1247`), and specify what happens when
  `pe.notifier == nil` or `Notify` errors — the honest answer is probably "record the failure on
  the plan and surface it, then retry on the next tick", not "log WARN". Add tests:
  *"Given a plan parked awaiting supervision, When the engine restarts, Then PlanSupervisor is
  woken exactly once"* and *"Given the notifier rejects the wake, Then the plan does not remain
  silently parked."*

---

### MAJOR Findings

#### [MAJ-001] `pkg/sandbox/compositor.go` does not exist — a `[FACT: grill-verified]` citation used twice, already propagated into a second ADR

- **Lens**: Incorrectness
- **Affected section**: G1; D8 (*"`pkg/sandbox/compositor.go:178-201` means a **missing**
  policy map silently inherits `allow`"* `[FACT: grill-verified]`); R3
- **Description**: There is no `pkg/sandbox/compositor.go`. The real file is
  **`pkg/tools/compositor.go`**, and the cited range (`:178-201`) does land on the right
  function there (`resolveEffectivePolicyWith`). `pkg/sandbox/` is the kernel-sandbox package
  (Landlock/seccomp/egress) and is unrelated to tool-policy resolution. The error has already
  been copied into `docs/internal/architecture/ADR-056-background-job-visibility.md:92-94`,
  which cites *"`pkg/sandbox/compositor.go:178-201` … see ADR-055 D8."*
- **Impact**: The ADR's single most security-relevant citation, marked as verified by the prior
  grill, points at a file that does not exist — and a second ADR now depends on it. An
  implementer or security reviewer following the reference finds nothing and cannot check the
  claim.
- **Recommendation**: Correct to `pkg/tools/compositor.go:178-201` in D8, G1 and R3, and file a
  one-line fix against ADR-056:92-94.

#### [MAJ-002] D8's *"Constraint #6 is inverted"* is itself wrong — a coverage gap does abort boot

- **Lens**: Incorrectness
- **Affected section**: D8; Constraints (*"Constraint #6: see D8 — v1 stated this cost
  backwards"*)
- **Description**: D8 says *"v1 claimed … that a gap aborts boot. **This is inverted**."* It is
  not. Verified: `ValidateToolPolicyCoverage` (`pkg/config/validate.go:448-475`) returns gaps,
  and `pkg/gateway/gateway.go:1541-1549` returns `"tool-policy coverage validation failed —
  aborting boot"`. CLAUDE.md Constraint #6 is accurate as written.

  The correct statement — and D8's *operational* conclusion is right — is narrower: **a missing
  per-agent map is not a "gap"**, because a gap requires *neither* side to have an entry
  (`validate.go:422-426`) and the seeded global ceiling (`pkg/config/defaults.go:255-418`)
  enumerates every static tool. So a zero-entry agent boots, and
  `resolveEffectivePolicyWith`'s `case a == "": return g` (`pkg/tools/compositor.go:186-187`)
  hands it the global value — `allow` for `bash` (`defaults.go:284`), `write_file` (`:286`),
  `set_config` (`:364`), `create_agent` (`:365`). R3's substance therefore **stands**; note
  `RepairIncompleteToolPolicyCoverage` (`validate.go:525`) does **not** save you, because it
  backfills only what validation calls a gap, and this is not one.

  Two further imprecisions: the merge is **most-restrictive-wins** (`deny > ask > allow`,
  `compositor.go:194-200`), not OR-based — only *coverage* is OR-based; and D8's implied
  "System Agents are all-deny" is stale (`pkg/coreagent/core.go:828`: *"The invariant is no
  longer 'all-deny'"*).
- **Impact**: The ADR tells future readers that a documented hard constraint is backwards. It
  has already been quoted that way in ADR-056. The right lesson — *coverage validation and
  policy resolution are different layers* — is lost.
- **Recommendation**: Rewrite D8's first paragraph: *"Constraint #6's boot abort is real
  (`gateway.go:1541`). It does not fire here because a coverage 'gap' requires neither the
  global map nor the agent map to have an entry, and the seeded global ceiling covers every
  static tool. The consequence is that a new agent with no policy map boots fine and inherits
  the global `allow` for `bash`/`write_file`/`set_config`/`create_agent`
  (`pkg/tools/compositor.go:186-187`)."* Remove "OR-based" from the description of resolution.

#### [MAJ-003] D7 claims to close v1's MAJ-003 while D14 renames the very `[P1]`-mandated fields D7 preserves

- **Lens**: Inconsistency
- **Affected section**: D7; D14 rows 3 and 5
- **Description**: D7's whole argument is *"Deleting published, `[P1]`-mandated linkage as a
  side effect of a supervision change is out of proportion. v2: **leave them in place**."* D14
  then renames them: row 3 `Plan.OwnerSessionID → SupervisionSessionID`, row 5 `OwnsPlanID →
  SupervisedPlanID`.

  Renaming a `[P1]`-mandated wire field breaks the MUST exactly as deletion does. Verified
  verbatim:
  - **FR-147** `[P1]` (`unified-goal-plan-subagent-spec.md:1522`): *"the Plan record MUST
    persist `owner_session_id` … and the owner session MUST carry a reciprocal `plan_id`."*
  - **FR-118** `[P1]` (`:1486`): *"The sweep MUST identify exemption (b) through the named
    plan↔owner-session linkage (`Plan.owner_session_id` / the owner session's reciprocal
    `plan_id`, FR-147) — **NOT** through `owner_scope`."*

  FR-118 resolves the exemption **by name**. D14 row 4 additionally renames `OwnerScope*` →
  `Scope*`, touching the other half of the same sentence.

  Also unsupported: D7's heading lumps `plan:<id>` with the two `[P1]` names. That synthetic id
  format appears in **neither** FR-118 nor FR-147; it is an implementation detail of
  `ensureOwnerSessionLocked` (`plan_engine.go:2469-2481`).
- **Impact**: v1's MAJ-003 is marked closed in the v2 changelog and is not. Three `[P1]`/`[P2]`
  MUSTs are broken by D14 without the ADR knowing.
- **Recommendation**: Either drop D14 rows 3 and 5 (consistent with D7), or move them into the
  explicit override treatment D1 gives FR-146 — quote FR-118/FR-147, state the reversal, and
  annotate the spec. Do not leave D7 and D14 in the same document giving opposite instructions.

#### [MAJ-004] Uncited upstream conflicts — one `[P1]`, four `[P2]`, six ADR-053 decisions, and ADR-049

- **Lens**: Inconsistency
- **Affected section**: header Amends line; FR-8; D1; D5; D6; D14
- **Description**: v2 fixed v1's five bogus anchors and now overrides FR-146 explicitly — good.
  The rest of v1's MAJ-003 table is still uncited. Verified against
  `docs/internal/specs/unified-goal-plan-subagent-spec.md`:

  | Upstream | Conflict | Cited? |
  |---|---|---|
  | **FR-193** `[P1]` (`:1553`) | *"The boot sweep MUST NOT spuriously re-arm a re-judge of `awaiting-owner-correction` … and the `paused` awaiting-correction owner session MUST be exempt."* Hit twice — by D6's rename and by D14 rows 3/5 renaming the fields the exemption resolves through. | **No** |
  | **FR-140** `[P2]` (`:1515`) | *"Every plan MUST run inside a persistent owner agent session."* D14 row 2 deletes its anchor field. | **No** |
  | **FR-141** `[P2]` (`:1516`), **FR-186** `[P2]` (`:1558`) | Both hard-code the literal `awaiting_owner_correction`. | **No** |
  | **FR-133** `[P2]` (`:1507`) | *"Ownership MUST derive from the **spawn edge** (owner = union of spawning parent · owning plan · human for top-level)"* — a materially different definition from D14's *binding* canonical one. A vocabulary ADR that never reconciles with the published definition of the word it is fixing. | **No** |
  | **FR-109** `[P2]` (`:1466`) | *"The plan owner MUST be excluded from the task-level idle trigger"* — undefined once `OwnerAgentID` is deleted. | **No** |
  | ADR-053 **D4** (`:86`) | The correction-verbs decision this ADR is amending. | **No** |
  | ADR-053 **D7** (`:87`) | *"Adjust a member = Stop plan → change → continue."* | **No** |
  | ADR-053 **D2** (`:95`) | *"only a direct session/plan owner asks the human"* — FR-4 demotes the Owner with no new terminus for plan-scoped `owner_required` questions. | **No** |
  | ADR-053 **§3 anti-drift / BOM rule** (`:47`) | *"a second goal store, a second messaging envelope … is a blocking finding (DoD-11)."* D5 adds a second wake/notify rail; D1 adds a new agent. The gate is never run. | **No** |
  | ADR-053 **§5.1** → **ADR-049:5** | ADR-049 already carries the ordered text *"D4 one-shot owner wake → a **persistent owner session**"* (verified at `ADR-049-planning-goals-system-agents.md:5`). D5 restores a one-shot wake, pointed at a different agent — silently falsifying a third ADR. | **No — ADR-049 is never mentioned** |
- **Impact**: Three ADRs now disagree about the owner-wake mechanism and ADR-055 documents none
  of it. FR-193 is a `[P1]` MUST hit twice.
- **Recommendation**: Add the `§ Upstream decisions this ADR reverses` section v1 asked for,
  with one line of justification per row, and run ADR-053 §3's BOM gate explicitly for both the
  new agent (D1) and the second notification rail (D5).

#### [MAJ-005] `plan/SKILL.md` ships an anti-pattern instruction that will sit inside PlanSupervisor's own context window telling it not to exist

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: FR-3; D1
- **Description**: FR-3 grants PlanSupervisor `plan/SKILL.md`. Its closing lines
  (`pkg/skills/embedded/plan/SKILL.md:231-232`, under `## Anti-patterns`) read:

  > `- Do not create a forked "Planner" agent — this skill is the planning behavior.`
  > `  Any create_plan-granted agent reuses it (BOM, FR-146).`

  This is not a documentation conflict. It is **shipped prompt text**, embedded via
  `//go:embed all:embedded` (`pkg/skills/embed.go:16`), that will be loaded into the context of
  the very agent D1 creates. For an LLM-driven adjudicator whose behaviour *is* its prompt,
  granting it a skill whose final instruction denies its legitimacy is a live behavioural risk,
  not a stylistic one.
- **Impact**: Unpredictable adjudication behaviour, and a directly self-contradicting prompt in
  the ADR's most safety-relevant agent.
- **Recommendation**: D1 must state what happens to `SKILL.md:231-232`. If FR-146 is overridden,
  the anti-pattern line is now false and must be amended in the same release (it is prompt text,
  so it is part of the change, not a doc chore). Add it to §9's implementation order.

#### [MAJ-006] G4 is answerable by reading, and its real answer weakens D1's own rationale

- **Lens**: Incompleteness (research gap) / Incorrectness
- **Affected section**: G4; D1 (*"Missing: Whether plan/SKILL.md is usable for correction"*);
  §9 validation step 2; §5 Option analysis
- **Description**: G4 says *"nobody has checked"*, with *"Likely assumption if unresolved:
  Usable as-is for authoring tail members."* `pkg/skills/embedded/plan/SKILL.md:156-219` is a
  **complete correction playbook**:
  - `:156-160` — *"## Re-planning checklist (use when the DoD is UNMET) … **The DoD stays
    immutable (G-11) — you never change the criteria; you change the plan's execution to meet
    them.**"* — this is ADR-055's NFR-2, already taught.
  - `:162-175` — diagnose: per-criterion verdict → member attribution → wrong outcome /
    transient / missing capability.
  - `:177-183` — a verb-selection table over exactly SUPERSEDE / TARGETED-RETRY / APPEND, i.e.
    exactly FR-5's verb set.
  - `:185-189` — record the falsified assumption (ADR-055 NFR-5 / D13).
  - `:198-204` — honest exit (G-10).

  D1's stated rationale for overriding FR-146 is *"Authoring a greenfield plan and supervising
  a running one are different jobs."* The skill already covers both — which is a direct
  argument **for** Option C (literal FR-146), the option D1 rejects. §5's Option analysis was
  therefore performed without reading the artifact that decides it.

  Two genuine residual gaps the ADR does not identify: the checklist is entered only *"When the
  plan Judge returns UNMET"* (`:157`), so it does **not** cover D5's `stalled` wake (non-terminal
  DAG) — D5 correctly predicts this must go in the SOUL; and it is written second-person to the
  plan's *author*, whereas under this ADR the corrector is a different actor.
- **Impact**: The ADR's central judgement call (D1, its only Medium-High) rests on an unchecked
  premise, and the check falsifies part of it.
- **Recommendation**: Read the file, replace G4 with its answer, and re-argue D1 against what
  the skill actually contains. The honest remaining argument for Option B is *actor selection*
  (a deterministic adjudicator vs. whichever persona owns the plan) plus the exclusive tool
  grant — not "the skill can't do corrections", which is false.

#### [MAJ-007] D4 needs a second writer of `JudgeRounds` and silently double-charges; `judge_rounds_exhausted` has three causes, not two

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: D4; FR-7; G3; R4; §9 success criterion 4
- **Description**: D4 says corrections *"consume the existing judge round budget … No second
  budget, no second exhaustion path."* Mechanically that is not free:
  - `JudgeRounds` has **exactly one writer** — `plan_engine.go:1495` `newRounds :=
    current.JudgeRounds + 1`, inside `applyJudgeRoundOutcomeLocked`, declared the sole writer in
    the comment at `:1488-1494`. Charging a correction requires a **second** incrementer,
    contradicting that invariant. The ADR does not mention it.
  - A correction is followed by re-dispatch and a re-judge, and that re-judge increments
    anyway. So "one round per correction" **double-charges** and silently halves the effective
    budget. D4's `Missing:` line frames the open question as *"full round or a fraction"*; the
    real question is *whether it is charged separately at all*.
  - The counter is not incremented per judge *call*: `Unavailable` results return early at
    `:1469-1485` burning zero rounds, and the F2 signature gate (`:1293-1301`) can skip the
    judge entirely. Any budget reasoning must account for that.
  - **G3 undercounts.** `FailedReasonJudgeRoundsExhausted` is produced at two sites — the real
    ceiling (`:1289`) and **`AppendCorrection`'s honest-exit path** (`:2680`, when
    `planCannotProgress(tasks)`), which means *"DoD unreachable"*, not "rounds exhausted". That
    third meaning is unreachable today only because `AppendCorrection` has no caller — it
    becomes reachable **the moment this ADR ships**. G3/R4 describe two causes; there are
    three, and the new one is the least like the name.
- **Impact**: The budget bound in §9's success criteria is unverifiable as stated, and a user
  reading `judge_rounds_exhausted` will get one of three unrelated stories.
- **Recommendation**: State explicitly where the correction increment happens and how it
  interacts with the subsequent re-judge (recommended: the correction does **not** increment;
  the re-judge it provokes already does — that gives D4's "one budget" property for free with no
  second writer). Split the reason enum or, at minimum, specify three distinct user-facing
  messages keyed off the handover text, and say so in G3.

#### [MAJ-008] D14 is a 157-file / 921-occurrence rename bundled into a feature ADR, with no acceptance criteria and an ordering that guarantees rework

- **Lens**: Overcomplexity
- **Affected section**: D14; §9 step 6; the v2 changelog's *"one gate fix, one agent, one
  wiring"*
- **Description**: Reproduced independently (rg, 2026-07-27, excluding `docs/` and `*.md`) — the
  ADR's per-item *line* counts reproduce essentially exactly: 31/197, 67/340, 23/80, 18/209,
  13/32, 5/63 against a measured 32/198, 69/408, 23/87, 19/215, 13/36, 5/63. Row 6 is exact.
  Two things the ADR never does with them:
  - **It never adds them up.** ~157 code file-slots and ~1000 occurrences of pure rename in one
    release, plus ~12 further doc files for row 1 alone, delivering **zero** user-visible
    behaviour.
  - **It never separates the identifier from its homonyms.** Row 2 is ~40% false positives (see
    CRIT-001), and ~40% of row 3's cost is actually row 7's (`pkg/tools/` accounts for 33 of
    row 3's occurrences via `ProcessSession`/`shell.go`). Counting matching lines cannot
    distinguish `Plan.OwnerAgentID` from `Schedule.owner_agent_id` — which is the same class of
    limitation D14 correctly diagnoses in `rename`/`impact`, applied to its own measurement.

  It also has no acceptance criteria. §9's five success criteria all measure the supervision
  feature; the largest work item in the document is unmeasured.

  §9 step 6 places the rename **last**, after steps 1–5 build the gate fix, the wake split, the
  tool, the REST route, the notification work and the SPA copy — all written against
  `awaiting_owner_correction` and `OwnerAgentID`, which step 6 then renames or deletes. Step 2
  (the D3 gate fix) is specified in terms of `p.OwnerAgentID`, the field step 6 removes.

  The ADR's justification is *"the confusion is not hypothetical; it recurred repeatedly during
  authoring, in this document and in discussion."* Authoring friction is a real cost, but it is
  an argument for doing the rename — not for doing it inside this ADR, in this release, coupled
  to a correctness-critical feature whose own findings list is this long.
- **Impact**: The feature's risk profile is dominated by a refactor that shares none of its
  rationale. If the rename lands badly (CRIT-002) it takes the feature with it; if the feature
  is deferred, the rename is orphaned.
- **Recommendation**: Split D14 into its own ADR and its own PR, sequenced **before** the
  feature so steps 1–5 are written once, against final names. Keep the D6 phase rename with it.
  If the operator insists on one release, at minimum reorder §9 so the rename is step 1 and give
  D14 its own success criteria (e.g. *"`rg 'OwnerScope|ownerKey|awaiting_owner_correction'` over
  non-doc files returns zero"*, plus the CRIT-002 migration test).

#### [MAJ-009] D14's compiler-driven method is structurally blind to embedded prompts, docs, and e2e specs

- **Lens**: Infeasibility
- **Affected section**: D14 *"Execution method"* (*"let the Go compiler and `tsc -b` enumerate
  every break. The compiler is exhaustive where the graph is not."*)
- **Description**: The method is right to rule out `rename`/`impact` (that dry-run evidence is
  good work), but "the compiler is exhaustive" is false for at least three categories inside the
  ADR's own count:
  - **`pkg/skills/embedded/plan/SKILL.md:158`** contains the literal `awaiting_owner_correction`
    and is `go:embed`ed. Neither `go build` nor `tsc -b` can see inside it. After the rename the
    shipped planning prompt teaches agents a phase value that no longer exists.
  - **`tests/e2e/conformance-design-e2e.spec.ts`** — 17 occurrences. Playwright specs are not in
    the `tsc -b` project graph and fail only at run time.
  - **YAML/contract prose** — `Plan.yaml:90-110` describes the phase and the boot-sweep exemption
    in free text; `Plan.yaml:131-139` describes `owner_session_id` by name. Regeneration does not
    rewrite prose.

  D14's *"wire-visible"* definition (three dirs: `contracts/`, `pkg/api/generated/`,
  `src/lib/api/generated/`) also misses a **fourth generated surface**:
  `pkg/gateway/inboundschemas/*.yaml`, a copy of `contracts/components/schemas/` made by
  `scripts/gen-contracts.sh:82-84`, embedded and used for **runtime inbound validation** — it
  holds occurrences of rows 1–5. And rows 4/5 reach a **fifth**, agent-facing surface:
  `contracts/components/schemas/DelegateStatusResponse.yaml:13-14` `$ref`s
  `SessionLifecycleRecord.yaml`, so the rename changes the shape of the `delegate status` tool
  result that agents read, and produces a second generated enum family
  (`DelegateStatusResponseSessionOwnerScopeKind`, `pkg/api/generated/openapi_types.gen.go:1151-1166`)
  on top of `SessionLifecycleRecordOwnerScopeKind` (`:3797-3813`). The ADR names neither.
- **Impact**: The rename ships "compiler-green" with a stale agent-facing prompt, red e2e, a
  runtime inbound validator still enforcing the old key, and a changed tool-result shape nobody
  reviewed.
- **Recommendation**: Amend the method: compiler + `tsc -b` for identifiers, **plus** an explicit
  `rg` sweep over `**/*.md`, `**/*.yaml`, `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`
  and `tests/e2e/**` as a mandatory second pass, and run the e2e gate before merge. Add
  `DelegateStatusResponse` to D14's impact list.

#### [MAJ-010] D12 understates the cost: `pkg/task/verdict.go` is a wire type in three generated surfaces plus two `Valid()` switches

- **Lens**: Incompleteness
- **Affected section**: D12 (*"reuse the existing type … Extend it … rather than introducing a
  parallel shape"*)
- **Description**: D12 is right that no *new* type is needed, and right that `VerdictScopePlan`
  exists — though at `pkg/task/verdict.go:12-20`, not `:43` (`:43` is the `JudgeVerdict` struct).
  What it omits is that this type **crosses the wire**:
  `contracts/components/schemas/JudgeVerdictFrame.yaml`, `contracts/asyncapi.yaml:132`,
  `pkg/api/generated/openapi_types.gen.go:1594-1596`/`:1825-1827` (each with a generated
  `Valid()` enum guard), and `src/lib/api/generated/schemas.ts:121-138`/`:1615-1622`/`:9173`. A
  REST endpoint returns it (`listTaskVerdicts`).

  So "extend it with the chosen correction verb" is the full Constraint #8 five-step pipeline in
  three committed locations, not an additive Go field. D12's confidence rationale
  (*"extension is additive"*) reads as if it were internal.
- **Recommendation**: Restate D12: *"the type exists and already has a plan scope, but it is a
  wire type — extending it is a contracts-first change (Constraint #8), not an internal edit."*
  Fix the line reference. Then answer D12's own open question (verb on the verdict vs. on the
  correction record) — putting it on the `RevisionEntry` avoids a wire change entirely and is
  probably the right answer.

#### [MAJ-011] D9's *"Missing: Nothing material"* is wrong — the freeze is handler-local and this ADR adds two paths that bypass it

- **Lens**: Incompleteness
- **Affected section**: D9
- **Description**: D9's core claim is verified verbatim (`rest_plans.go:708-736` returns 409 for
  `dod` and `owner_agent_id` on non-draft plans; the `Bounds` exemption comment is real at
  `:715-716`). But D9 then asserts *"Missing: Nothing material"*, and two things are:
  1. **The store enforces nothing.** `plan.Store.updateLocked` (`pkg/plan/store.go:338-356`)
     applies `patch.OwnerAgentID` and `patch.DoD` with **no state check whatsoever**. The
     invariant is a property of one handler, not of the data. D9's own framing — *"the external
     mutation boundary (REST) is the correct place"* — only holds while REST is the only external
     boundary.
  2. **This ADR adds two more external boundaries**: FR-5's correction tool (agent-facing) and
     FR-5's REST route. Neither inherits the 409 guard. `AppendCorrection` mutates a running
     plan's member DAG by design — which is fine — but nothing stops a future patch through those
     paths from carrying `DoD` or `OwnerAgentID`.
- **Recommendation**: Change D9's `Missing:` line to name both, and state the rule the new paths
  must obey: *"any new plan-mutation path MUST reject `dod` and `owner_agent_id` in its request
  shape (structurally, as `CorrectionRequest` already does), because the store does not enforce
  the freeze."* Add a conformance test asserting `CorrectionRequest` has no DoD/owner field —
  that is the test §9's byte-identical criterion should have been.

#### [MAJ-012] The two artifacts that decide PlanSupervisor's behaviour — its SOUL and D3's gate predicate — are both left undecided

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: FR-3; D1; D3 (*"**Prefer** matching on identity … Missing: Final
  predicate shape. Would improve: Choosing during /plan-spec"*); D5's stall note
- **Description**: For an LLM-driven adjudicator, the SOUL **is** the implementation — it decides
  what counts as correctable, which verb to choose, when to give up honestly, and (per D5) how to
  answer a `stalled` wake versus an UNMET wake. The ADR specifies it in one clause (FR-3: *"its
  own supervision-specific SOUL"*) plus one sentence in D5. Meanwhile the Judge's equivalent
  (`JudgeDefaultRubric`, `pkg/coreagent/core.go:889-901`) is a concrete, reviewable const with an
  explicit fail-closed rule — the precedent D1 says it follows verbatim.

  D3 is the ADR's *self-described core change* (*"This is the ADR's core change, and v1 got it
  wrong"*), and it ends with "prefer" and "choosing during /plan-spec". An authority predicate is
  a security decision; deferring its shape defers the security review with it.

  Related ambiguity: FR-9/D10's *"PlanSupervisor is unavailable"* is never defined. Provider
  outage? A turn that errors? A turn that returns unparseable output? A turn that never returns?
  No timeout is named, and D10 explicitly excludes retry/backoff without saying what detects the
  condition in the first place.
- **Recommendation**: Put a draft `PlanSupervisorDefaultRubric` in the ADR (as D1 claims to follow
  the Judge pattern, and as the Judge does), covering at minimum: UNMET-wake behaviour,
  stalled-wake behaviour, the verb-selection rule, the honest-exit rule, and an explicit
  prohibition on superseding evidence without replacement work (CRIT-005). Fix D3's predicate to
  a single stated form. Define "unavailable" with a concrete detection mechanism and timeout.

#### [MAJ-013] The notification `type` widening is a two-file contract change ordered *after* the code that needs it, and has no out-of-enum coercion

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: D5's four gaps; §9 implementation order step 4
- **Description**: `Notification.type` is a closed one-value enum in **two** contract files —
  `contracts/components/schemas/Notification.yaml:18-22` and `contracts/asyncapi.yaml:2570-2573`
  — generated into `pkg/api/generated/openapi_types.gen.go:2012-2018` with a `Valid()` guard and
  into the SPA zod schemas. Two problems:
  1. **Ordering.** §9 puts `pkg/notifications` at **step 4**, after `pkg/coreagent` (1),
     `plan_engine` (2), and `pkg/tools`/`pkg/gateway` (3). Constraint #8 requires the contract
     change *before* any consuming code. The notification enum widening must be step 1b.
  2. **Silent drop.** The store already hard-coerces out-of-set **severity**
     (`pkg/notifications/store.go:177-182`, comment: *"An out-of-set value would be dropped by the
     SPA's zod guard, silently losing the alert"*) — there is **no equivalent coercion for
     `type`**. A `plan_dod_unmet` type shipped without the contract change is silently dropped at
     the SPA edge, which is exactly the failure mode D10 must not have.

  Two of D5's other three gaps also have unstated tails: dedup keys only on `ScheduleID`
  (`store.go:193`), so plan notices are **never** coalesced and append until the 50-item cap
  evicts them; and `NotificationPanel.tsx:68-72` routes click-through on `sessionId` then
  `scheduleId` only, so a plan notification is a dead-end click even after `plan_id` is added.
- **Recommendation**: Reorder §9 so the contract change is first. Add the `type` coercion (or an
  explicit "unknown type is dropped" acknowledgement) and the SPA click-through target to D5's
  gap list, which should now read six, not four.

#### [MAJ-014] NFR-1 measures only the case where the feature does nothing; the unhappy path is unbounded in cost and uncited against ADR-053's budget

- **Lens**: Incompleteness
- **Affected section**: NFR-1 (*"Wake frequency stays at ~1 per plan in the happy path"*); D4
- **Description**: NFR-1 is true and irrelevant: in the happy path the DoD is MET and no
  correction happens, so the feature is inert. The cost this ADR introduces is entirely in the
  unhappy path, and it is unbounded in the ADR's own terms: up to
  `plan_judge_max_rounds` (default **20**, `pkg/config/planning.go:14`) supervision
  adjudications, each an LLM turn with the plan skill loaded, each potentially triggering member
  re-dispatch — i.e. up to 20 × (supervisor turn + judge turn + N member turns) per plan.

  ADR-053 **S5** defines the budget triple including *"one app-level OVERALL token budget
  (D12)"*. ADR-055 never cites it, and NFR-1's stated weight in §4 is *"LLM hops / token cost |
  5%"*.
- **Recommendation**: Replace NFR-1 with a bound on the case that matters: *"a plan that never
  reaches MET performs at most `plan_judge_max_rounds` supervision adjudications and consumes the
  ADR-053 S5 overall token budget."* Cite S5. State whether a plan that exhausts the budget mid-
  correction fails through `judge_rounds_exhausted` or through the S5 path (they are different
  terminal reasons).

#### [MAJ-015] D6, R5 and G2 disagree about whether the rename ships in this release

- **Lens**: Inconsistency
- **Affected section**: D6; R5; G2; §8
- **Description**: D6: *"`awaiting_owner_correction` → `awaiting_supervision`, **in this
  release**. v2 initially deferred this on cost grounds; the operator rejected the deferral."*
  §8 agrees (*"D6 Retain phase; rename this release — High"*). G2 agrees (*"RESOLVED … it ships
  in this release"*). But R5 still reads: *"**Deferred rename** leaves a misleading enum in place
  (D6): `awaiting_owner_correction` names the wrong actor **until the rename lands**."*

  R5 is stale text from the deferral v2 abandoned. v1 flagged the same class of defect
  (MIN-006, §8 contradicting D6's body); v2 fixed §8 and left R5.
- **Recommendation**: Delete R5 or rewrite it as the risk that actually exists — *"R5: renaming a
  persisted enum with no migrator bricks in-flight parked plans (see CRIT-002)."*

#### [MAJ-016] `Plan.Owner` and `Plan.CreatedBy` are written identically on every path; D14 retains both as "canonical" without saying which is authoritative — and `owner`'s contract description contradicts D2

- **Lens**: Inconsistency
- **Affected section**: D2; D14 (*"Retained deliberately: `Plan.Owner`, `Task.Owner`, `CreatedBy`
  — these are the canonical meaning"*)
- **Description**: Verified on both write paths, the two fields receive the **same value**:
  - `pkg/tools/plan.go:286-287` — `Owner: callerID, CreatedBy: callerID`
  - `pkg/gateway/rest_plans.go:547-548` — `Owner: c.Username, CreatedBy: c.Username`

  They are provably always equal. D14's stated goal is *"Owner MUST have exactly one meaning"* —
  it achieves one meaning across **two required wire fields** and never says which is
  authoritative. That matters because live consumers already differ: ADR-056 D4 scopes job
  visibility by `owner`; D14's own survey table notes `CreatedBy` *"drives the tiered-DoD gate"*.
  If they can never diverge, one is dead weight; if a future path sets them differently, the two
  consumers silently disagree. A vocabulary-correction ADR is the right place to settle this and
  it does not.

  Separately, D2's load-bearing claim — *"`Plan.Owner` … already holds the dual-kind principal
  … [FACT: grill-verified]"* — is true in Go and **false on the published wire**.
  `contracts/components/schemas/Plan.yaml:244-250` documents `owner` as *"Username of the user
  who created this plan"* (`readOnly: true`), while `created_by` (`:251-257`) is the field
  documented as *"Username (**or agent ID**)"*. So the tool path writing an agent id into `owner`
  is an **undocumented contract divergence** that D2 ratifies without noting or fixing — and D2's
  "no wire change" claim requires at minimum a `Plan.yaml` description change plus regen.
- **Recommendation**: In D14, either declare one of the pair authoritative and deprecate the
  other (with its consumers listed), or state explicitly why two identical required fields are
  retained. In D2, fix `Plan.yaml:244-250`'s description to match reality and add the regen to
  §9 — or, better, note that `created_by` is already the sanctioned dual-kind field and consider
  using it.

---

### MINOR Findings

#### [MIN-001] The budget field is named `plan_judge_max_rounds`, not `judge_max_rounds`
- **Lens**: Incorrectness
- **Affected section**: D4, R4, §9 success criterion 4
- **Description**: The real names are `PlanBounds.PlanJudgeMaxRounds` (`pkg/plan/plan.go:318`,
  JSON `plan_judge_max_rounds`) and `config.PlanningConfig.PlanJudgeMaxRounds`
  (`pkg/config/planning.go:41`). `judge_max_rounds` exists nowhere. Minor in isolation; notable
  in an ADR whose thesis is naming precision.
- **Recommendation**: Use the real field names.

#### [MIN-002] `pkg/task/verdict.go:43` is the struct; the scope constants are at `:12-20`
- **Lens**: Incorrectness
- **Affected section**: D12
- **Recommendation**: Cite `verdict.go:12-20` for `VerdictScopePlan`. Note also they are untyped
  string constants, not a named `VerdictScope` type — relevant if D12 wants a typed verb.

#### [MIN-003] `requireOwner` has three clauses, and the session clause is not an authentication factor
- **Lens**: Insecurity
- **Affected section**: D3 (*"gates on `caller.AgentID != p.OwnerAgentID` **plus** a session
  check"*)
- **Description**: Verified `plan_engine.go:2754-2770`: three clauses — empty-AgentID, agent
  mismatch, and a session check that is **conditional** (`if p.OwnerSessionID != "" && …`). Only
  the agent-mismatch branch is opaque; clauses 1 and 3 return differentiated strings, so a caller
  who knows the owner agent id can distinguish "wrong session" from "wrong agent" (v1's MIN-005,
  still open). And `OwnerSessionID` is synthesized as `"plan:" + p.ID`
  (`ensureOwnerSessionLocked`, `:2469-2481`) — a **derived, guessable** string, so clause 3
  proves nothing about the caller.
- **Recommendation**: D3 should say the session clause is not a security control and state
  whether it is kept, dropped, or replaced. If any clause is kept, make all denials return the
  identical opaque error (NFR-3).

#### [MIN-004] §9 step 1 omits the second system-agent membership site
- **Lens**: Incompleteness
- **Description**: Membership lives in two places: `SystemAgents()`
  (`pkg/coreagent/core.go:159-163`) **and** `systemAgentIDs` (`core.go:146-148`), which backs
  `IsSystemAgentID`. §9 step 1 names only the former. (v1's MIN-004, still open.)
- **Recommendation**: Add `systemAgentIDs` to §9 step 1.

#### [MIN-005] Registering the correction tool requires an `allStaticToolNames` edit or the seed panics at boot
- **Lens**: Incompleteness
- **Description**: `denyAllThenOverride` calls `validateOverrideKeys`
  (`pkg/coreagent/core.go:354-368`), which **panics** on any override key not present in
  `allStaticToolNames` (`core.go:295`). So seeding PlanSupervisor's grant with a new correction
  tool name before adding it to `allStaticToolNames` panics the binary at first call. §9 step 1
  ("`SystemAgents()` + `systemAgentSeed` grant + rubric") omits this ordering constraint.
- **Recommendation**: Add `allStaticToolNames` to §9 step 1 and note the ordering.

#### [MIN-006] D1's list of re-enforced System-Agent invariants omits `MemoryEnabled=false`, which is material for an adjudicator
- **Lens**: Incompleteness
- **Description**: `seedSystemAgents` (`pkg/coreagent/core.go:1379-1453`) also repairs
  `MemoryEnabled` to `false` in both directions (ADR-052 FR-039, impartiality/reproducibility),
  and clears a stray `Default` flag. D1 lists only identity/type/locked/policy.
- **Recommendation**: Add both to D1 — memory-off is a substantive property of an adjudicator
  and should be stated, not inherited by accident.

#### [MIN-007] §9 step 5 undercounts the copy fix: two strings and four test assertions
- **Lens**: Incompleteness
- **Description**: The *"There's no in-app action for that yet"* copy appears **twice** —
  `src/lib/planStateColors.ts:213` (parked) and `:234` (**stalled**). D5 routes the stalled wake
  to PlanSupervisor too, so both become false. Four tests assert the string:
  `WorkspaceGraphTab.test.tsx:270,300` and `PlansFilterBand.test.tsx:382,408`.
- **Recommendation**: §9 step 5: *"revise both parked- and stalled-phase copy in
  `planStateColors.ts` and update the four asserting tests."*

#### [MIN-008] D14 rows 3 and 5 rename fields to names that are also wrong
- **Lens**: Incorrectness
- **Description**: Row 3 renames `Plan.OwnerSessionID` → `SupervisionSessionID`, but under D7 the
  field is *unused legacy tidiness debt* — it is the old owner session, not the supervision
  session, so the new name asserts a relationship that does not exist. Row 5 renames `OwnsPlanID`
  → `SupervisedPlanID`, but that field is a session's reciprocal link to the plan it **owns** —
  under D14's own canonical definition that is ownership, not supervision. The vocabulary
  correction imports the new concept into two fields that have nothing to do with PlanSupervisor.
- **Recommendation**: If they are kept and renamed, use names that describe them —
  `LegacyOwnerSessionID` / leave as-is (D7's position), and `PlanID` for the reciprocal link.

#### [MIN-009] D14 row 2 names one of `OwnerAgentID`'s eight consumers
- **Lens**: Incompleteness
- **Description**: See CRIT-001 for the list. Even if the deletion is dropped, the row's cost
  column ("67 files / 340 occurrences") measures text, not coupling — and ~40% of that text is a
  different field entirely.
- **Recommendation**: Enumerate consumers, not occurrences, for any field deletion.

#### [MIN-014] D14's "Wire break" column is wrong for rows 3 and 5
- **Lens**: Incorrectness
- **Affected section**: D14 scope table, rows 3 (*"minor (`omitempty`)"*) and 5 (*"minor — no
  writer exists"*)
- **Description**: Both are full wire fields under `additionalProperties: false`.
  `owner_session_id` is on `contracts/components/schemas/Plan.yaml:131` and generated into
  `pkg/api/generated/openapi_types.gen.go:8909,9169,9336` and the TS schemas; `owns_plan_id` is
  on `SessionLifecycleRecord.yaml:98` **and** inherited by `DelegateStatusResponse` via `$ref`.
  `omitempty` only helps when the value is empty — a populated one is a wire break, and both are
  persisted. Row 5's *"no writer exists"* half is **correct and verified** (every assignment is
  in `pkg/agent/boot_sweep_test.go` / `conformance_design_test.go`), but "no writer" is not the
  same as "not on the wire": readers still parse it, and `pkg/agent/boot_sweep.go:160-161` is a
  live non-test reader.
- **Recommendation**: Correct both cells to "yes", and note that rows 1 and 5 are **coupled** —
  `boot_sweep.go:160-161` and `:249` join `OwnsPlanID` to `PhaseAwaitingOwnerCorrection`, so they
  must land in the same change or the exemption's behaviour shifts.

#### [MIN-010] No observability, no runbook, and the de-facto kill switch is never named
- **Lens**: Inoperability
- **Description**: The ADR adds an autonomous agent that mutates plans and specifies no metric,
  no log line, no alert, and no on-call procedure for "plans are parked and the supervisor is
  down". D13 covers post-hoc audit of *corrections*, not operational visibility of the
  *supervisor*. There is an implicit kill switch — setting PlanSupervisor's correction tool
  policy to `deny` — but the ADR never says so, which means an operator at 3 AM will not know it
  exists.
- **Recommendation**: Add one short §: the counters worth emitting (corrections applied by verb,
  denied, rounds consumed, adjudications unavailable), and one line naming the tool-policy `deny`
  as the supported kill switch with its consequence (plans park; owners notified per D10).

#### [MIN-011] There is no bell — D10's *"fails safe and **visible**"* is not supported by the surface it cites
- **Lens**: Incorrectness
- **Description**: D5/D10's evidence line says *"store + WS push + bell"*. The store, REST, WS
  push and zustand store are real (`src/store/notifications.ts:82`,
  `src/components/layout/AppShell.tsx:47-54,246`). The entry point is **not** a bell: it is a
  `Tray` `DropdownMenuItem` inside the sidebar account dropdown
  (`src/components/layout/Sidebar.tsx:684-697`), and the unread badge (`:690-694`) renders
  **only when that dropdown is already open**. There is no ambient unread indicator in the app
  shell.
- **Recommendation**: Correct the evidence line, and if D10's "visible" is load-bearing, put an
  ambient indicator in scope.

#### [MIN-012] Mixing usernames and agent ids into the notification namespace raises a collision surface
- **Lens**: Insecurity (Information Disclosure)
- **Description**: The recipient becomes a filename via `sanitize()`
  (`pkg/notifications/store.go:91-107`), mapping every char outside `[A-Za-z0-9._-]` to `_` — so
  `a b`, `a/b` and `a_b` all collide on `a_b.json`. Today the namespace is usernames only; this
  ADR (via `Plan.Owner`) adds agent ids to the same flat space. Also note `ListForUser` /
  `MarkRead` / `MarkAllRead` accept an arbitrary recipient string with **no store-level guard** —
  safe today only because the sole handler always passes the authenticated username, and this ADR
  adds a second caller passing `Plan.Owner`.
- **Recommendation**: If agent recipients are adopted (CRIT-003), namespace them explicitly
  (`agent:<id>` vs `user:<name>`) and validate the principal string at the store boundary.

#### [MIN-013] D13 understates the audit work: the wire path exists with a consumer and no producer
- **Lens**: Incompleteness
- **Description**: D13 says the data *"already exists"* and only needs to be *"reachable"*.
  Verified: `RevisionEntry` (`pkg/plan/intent_log.go:91-102`) is produced and persisted to the
  private intent log, and `contracts/components/schemas/RevisionEntry.yaml` exists, nested under
  `SessionMessageRevisionEntry` in `contracts/openapi.yaml:598-642`. But **no REST route returns
  it**, and the generated producer `FromSessionMessageRevisionEntry`
  (`pkg/api/generated/openapi_types.gen.go:14529`) is never called — the Go consumer
  (`pkg/agent/session_messaging_wire.go:604-623`) reads a rail nothing writes.
- **Recommendation**: D13 should say the work is *"wire the existing dead
  `SessionMessageRevisionEntry` producer and/or add a plan-revisions read route"* — a bounded but
  real task, not a visibility toggle.

---

### Observations

#### [OBS-001] Split D14 into its own ADR and land it first
- **Lens**: Overcomplexity
- **Suggestion**: See MAJ-008. The vocabulary correction has a clean, defensible rationale of its
  own and does not need this feature's approval to be right. Landing it first also removes the §9
  step-6 rework and lets the feature be specified once, against final names.

#### [OBS-002] Re-run the option analysis now that `plan/SKILL.md`'s content is known
- **Lens**: Incorrectness
- **Suggestion**: §5 scored Option C ("skill only") as *"leaves which agent adjudicates
  unanswered — the dropdown lottery"*. With MAJ-006's finding that the skill already contains the
  full correction playbook, the honest comparison is narrower: Option B buys a **deterministic
  actor** and an **exclusive tool grant**, nothing more. That is still a reasonable case — but it
  is a different case than the one §5 makes, and it should be re-stated so a future reader can
  see what was actually being bought.

#### [OBS-003] ADR-056 is coupled in three places and has inherited one of the errors
- **Lens**: Inconsistency
- **Suggestion**: `ADR-056-background-job-visibility.md` D4 (`:246`) scopes plan visibility by
  `Plan.owner` per ADR-055 D2; its Constraints section (`:92-94`) has copied the non-existent
  `pkg/sandbox/compositor.go` path (MAJ-001); and its R5 (`:374-377`) states it *"should not be
  accepted before ADR-055."* None of it accounts for D14 row 2 deleting `owner_agent_id`. Add a
  cross-reference note to both ADRs, and fix ADR-056's path citation regardless of this ADR's
  fate.

#### [OBS-004] Add the `§ Upstream decisions this ADR reverses` section v1 recommended
- **Lens**: Inconsistency
- **Suggestion**: v2 handles FR-146 exactly right — quoted, overridden, rationale recorded, risk
  R1 filed. Do the same for the ten rows in MAJ-004 rather than leaving them implicit. The FR-146
  treatment is the template; it just needs to be applied to the rest.

---

## Structural Integrity (Variant B — Structured Spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | **FAIL** | §9 lists 5 success criteria covering FR-5/FR-6/FR-9/NFR-2/D4/D10. **FR-3** (skill grant), **FR-8** (wake split), **FR-12/D14** (the largest work item) and **NFR-5** (auditability) have none. |
| Cross-references are consistent | **FAIL** | D2 vs D14 row 2 (CRIT-001); D7 vs D14 rows 3/5 (MAJ-003); D6/G2/§8 vs R5 (MAJ-015); `pkg/sandbox/compositor.go` does not exist (MAJ-001); `verdict.go:43` is the struct (MIN-002); `judge_max_rounds` is not a real field (MIN-001). |
| Scope boundaries are explicit | **PARTIAL** | Non-goals are named well (member dispatch, task judging, delegation, member-level retry, rollback). But §1's *"Blast radius"* and the v2 changelog's *"one gate fix, one agent, one wiring"* both understate D14 by an order of magnitude (157 files / 921 occurrences). |
| Success criteria are measurable | **PARTIAL** | 3 of 5 are good. *"unmet for a **correctable** reason"* is undefined — nothing in the ADR says what makes a DoD failure correctable. *"DoD is byte-identical"* is a tautology (`CorrectionRequest` structurally cannot carry a DoD) and gives false assurance against the real integrity risk (CRIT-005). |
| Error/failure scenarios addressed | **FAIL** | D10 covers exactly one failure (supervisor unavailable), and does not define it. Uncovered: restart while parked (CRIT-007), lost/failed wake publish (CRIT-007), nil notifier (CRIT-007), notification write failure, agent-owner delivery (CRIT-003), correction validation failure, concurrent stop during correction, budget exhaustion mid-correction. |
| Dependencies between requirements identified | **FAIL** | See MAJ-004: FR-193 `[P1]`, FR-140, FR-141, FR-186, FR-133, FR-109, six ADR-053 decisions and ADR-049's supersession note are all contradicted and none is cited. §9's implementation order also violates Constraint #8 (MAJ-013). |

---

## Test Coverage Assessment

The ADR names five success criteria and no test levels. Gaps against what it proposes to build:

| Category | Gap | Affected requirement |
|---|---|---|
| **Migration** | No test that an on-disk plan persisted at `awaiting_owner_correction` survives the rename, and none that a lifecycle record persisted with `owner_scope_kind` survives it. These are the two highest-value tests in the change, and no migrator exists to test. | D6, D14 rows 1/4/5 (CRIT-002) |
| **Authorization (human path)** | No test that a non-owner authenticated user cannot correct a plan via REST. The route's authz is unspecified, so nothing can be written yet. | FR-5, NFR-3 (CRIT-006) |
| **Authorization (chat path)** | No test that a user cannot open a chat session against PlanSupervisor and drive a correction through it. | D1, NFR-3 (CRIT-004) |
| **Integrity (the real one)** | *"DoD byte-identical"* tests a structural impossibility. Missing: superseding the failing member without replacement work must NOT let the plan reach MET. | NFR-2 (CRIT-005) |
| **Delivery** | No test that the Owner actually **receives** the outcome — the current assertion surface would pass while writing to an unreadable file. | FR-4, D10 (CRIT-003) |
| **Crash recovery** | No test that a restart while parked re-wakes PlanSupervisor. | D6, FR-9 (CRIT-007) |
| **Budget** | No test distinguishing "correction charged a round" from "the re-judge charged it"; no test of the three-way `judge_rounds_exhausted` overload. | D4 (MAJ-007) |
| **Concurrency** | `AppendCorrection` holds the process-wide `planDecisionMu` (`plan_engine.go:2575`). No test of correction-vs-stop or correction-vs-judge-round interleaving, and no rate limit on the new HTTP entry to that lock. | FR-5 (CRIT-006) |
| **Contract drift** | `make verify-contracts` will fail on any of the four contract-touching items (notification `type`, `plan_phase`, `owner_agent_id`, verdict verb) if regen is not committed atomically. Not in §9. | Constraint #8 |
| **Prompt regression** | Nothing asserts that PlanSupervisor's loaded skill text does not contradict its own existence (MAJ-005) or reference a deleted phase value (MAJ-009). | FR-3 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| PlanSupervisor agent | **risk** | **risk** | ok | ok | ok | **risk** | Chat-addressable (CRIT-004) ⇒ its identity can be borrowed; `supersede` lets it tamper with the judge's evidence set (CRIT-005); its tool grant is unenumerated and defaults permissive (D8/R3, MAJ-002). |
| Correction gate (`requireOwner`) | **risk** | ok | ok | **risk** | ok | **risk** | Predicate shape undecided (MAJ-012); session clause is a guessable derived string, not an auth factor (MIN-003); two of three denial branches return differentiated strings (NFR-3). |
| REST correction route (FR-5) | **risk** | **risk** | **risk** | **risk** | **risk** | **risk** | No authz model on a surface with no per-plan authz to inherit; no rate limit on a handler that takes the global `planDecisionMu`; no attribution requirement; no stated denial code. Every column is unaddressed (CRIT-006). |
| Notification delivery | ok | ok | **risk** | **risk** | ok | ok | Agent recipients are silently dropped (CRIT-003) — a repudiation surface: the system believes it notified, the owner never did. Recipient string becomes a filename in a namespace now shared by users and agents (MIN-012); store-level API has no recipient guard. |
| Persisted records (plans + session lifecycle) | ok | **risk** | **risk** | ok | **risk** | ok | The renames have no migrator: parked plans become permanently un-updatable and throw the SPA plans list; legacy `owner_scope_kind` records are silently swept to `failed(interrupted)` with no log line (CRIT-002). Self-inflicted availability failure plus untraceable loss of live sessions. |
| Plan store (`updateLocked`) | ok | **risk** | ok | ok | ok | ok | The DoD/owner freeze is handler-local; the store applies both fields unconditionally (`store.go:338-356`), and this ADR adds two new mutation paths (MAJ-011). |

---

## Unasked Questions

1. When `Plan.Owner` is an agent id (the tool path, i.e. the majority), **on what surface does
   that agent receive an outcome notification?** The store cannot address it (CRIT-003).
2. Decision wakes go to PlanSupervisor and outcome wakes to the Owner — but all five current
   wakes publish to the single synthetic chat id `system:plan:<id>`
   (`plan_engine.go:2091-2093`). **Do the two actors share one destination?** If so they see each
   other's traffic; if not, what creates the second one?
3. **Who is authorized on the FR-5 REST route, and what does it return on denial?** And given no
   SPA client exists, why does it ship now?
4. **What makes a DoD failure "correctable"?** §9's headline success criterion turns on this word
   and nothing defines it.
5. **What stops PlanSupervisor from superseding the evidence instead of fixing the work?**
   (CRIT-005.)
6. **What re-wakes a plan parked across a gateway restart, or after a dropped wake publish?**
   (CRIT-007.)
7. **Is `awaiting_owner_correction` present on any real installation's disk today?** §9 asks for
   the survey and specifies no action for a positive result (CRIT-002).
8. **Does the correction increment `JudgeRounds`, or does the re-judge it provokes?** Both is
   double-charging (MAJ-007).
9. **Which of `Plan.Owner` / `Plan.CreatedBy` is authoritative**, given they are written
   identically everywhere (MAJ-016)?
10. **What is PlanSupervisor's SOUL?** For an LLM adjudicator that is the implementation, and it
    is one clause long (MAJ-012).
11. **What happens to `plan/SKILL.md:231-232`** — the anti-pattern line that will sit in
    PlanSupervisor's own prompt telling it not to exist (MAJ-005)?
12. **Is `pkg/entity`-style cross-process protection needed** for the intent log and plan store
    once an HTTP route can trigger corrections concurrently with the engine?

---

## Verdict Rationale

**BLOCK.**

v2 did real work: the invented `Owner{Kind,ID}` type is gone, D9 correctly defers to the guard
that already exists rather than bricking the engine, §1's causal claim is now honest, and D1's
treatment of FR-146 — quote it, override it, record the rationale, file the risk — is exactly how
an override should be documented. Those are genuine closures.

But the document blocks on its own terms. **CRIT-001** is the same defect class that produced the
v1 BLOCK, one field over: a required wire property deleted with no migration, and D2 and D14 give
opposite instructions about it in the same document. **CRIT-002** turns the D6 rename into data
loss on precisely the population this ADR exists to rescue, because `pkg/plan` has no migrator and
validates the phase on every write. **CRIT-003**, **CRIT-004**, **CRIT-005** and **CRIT-007** each
falsify a stated requirement against the working tree — FR-4/D10's notification, D1's "never a
chat target", NFR-2's integrity guarantee, and FR-9's "never silently stalls" respectively — and
each is discoverable by reading code the ADR already cites. **CRIT-006** opens a wider,
unauthorized human path to the same mutation D3 spends its whole body locking down for agents.

Underneath the criticals is a pattern worth naming, because it is the same one v1 identified:
**the ADR's research stopped at the questions it was asked about.** G4 says "nobody has checked"
about a file the repo ships and that answers the question against the ADR (MAJ-006). D5 declares
the notification surface a solved problem and misses that it structurally cannot address the
principal type D2 celebrates (CRIT-003). D8's most security-relevant citation points at a file
that does not exist and has already been copied into a second ADR (MAJ-001). Each is a five-minute
read.

Finally, on scope: the v2 changelog's *"one gate fix, one agent, one wiring"* and D14 cannot both
be true. D14's per-item line counts reproduce almost exactly — the measurement work was done — and
they sum to roughly 157 file-slots and 1000 occurrences of pure rename, unmeasured by any success
criterion, scheduled last so that everything before it is written against names it removes, and
touching persisted state in two stores that have no migration mechanism. The vocabulary correction
is defensible on its merits and should be its own ADR (OBS-001); coupling it to a
correctness-critical feature makes both harder to review and gives them a single point of failure.

### Recommended Next Actions

- [ ] Resolve D2 vs D14 row 2 into one instruction; if deleting `owner_agent_id`, enumerate all eight consumers and override FR-140 explicitly — **CRIT-001**
- [ ] Write two migrators on the `pkg/task/migrate_planning_status.go` sentinel pattern — one for `$OMNIPUS_HOME/plans/*.json`, one for `$OMNIPUS_HOME/session_lifecycle/*.jsonl` — before any rename lands; add both boot tests — **CRIT-002**
- [ ] Decide how an **agent** Owner receives outcomes; rewrite FR-4/NFR-4/D10 accordingly and expand D5's gap list from four to six — **CRIT-003, MAJ-013**
- [ ] Make closing the two chat-target holes and the default-agent star guard prerequisites of this ADR — **CRIT-004**
- [ ] Add an integrity control over `supersede` and replace the byte-identical success criterion with a test of the real risk — **CRIT-005**
- [ ] Either cut FR-5's REST route or fully specify its principal, denial code, rate limit and `RequireNotBypass` posture; reconcile with FR-4 — **CRIT-006**
- [ ] Specify the restart/lost-wake re-entry mechanism and the nil-notifier behaviour; make FR-9 a requirement with a mechanism — **CRIT-007**
- [ ] Fix `pkg/sandbox/compositor.go` → `pkg/tools/compositor.go` in D8/G1/R3 and in ADR-056:92-94 — **MAJ-001**
- [ ] Rewrite D8's Constraint #6 paragraph: gaps *do* abort boot; a missing per-agent map is not a gap — **MAJ-002**
- [ ] Reconcile D7 with D14 rows 3/5 against FR-118/FR-147 `[P1]` — **MAJ-003**
- [ ] Add `§ Upstream decisions this ADR reverses` covering FR-193/FR-140/FR-141/FR-186/FR-133/FR-109, ADR-053 D2/D4/D7/§3/§5.1/S4 and ADR-049 — **MAJ-004, OBS-004**
- [ ] Decide the fate of `plan/SKILL.md:231-232` and re-argue D1 against the skill's actual content — **MAJ-005, MAJ-006, OBS-002**
- [ ] Settle where the correction round increment happens; split or re-message the three-cause `judge_rounds_exhausted` — **MAJ-007**
- [ ] Split D14 into its own ADR sequenced first, or reorder §9 so the rename is step 1 with its own success criteria and a non-compiler sweep covering `pkg/gateway/inboundschemas/` and the embedded skill — **MAJ-008, MAJ-009, OBS-001**
- [ ] Correct D14's "Wire break" column for rows 3 and 5, and note the rows 1↔5 boot-sweep coupling — **MIN-014**
- [ ] Draft `PlanSupervisorDefaultRubric` in the ADR and fix D3's predicate to a single stated form — **MAJ-012**
- [ ] Delete or rewrite R5; fix D12's line reference and wire-cost framing; fix the `judge_max_rounds` name — **MAJ-015, MAJ-010, MIN-001**
