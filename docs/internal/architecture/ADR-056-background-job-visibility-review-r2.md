# Adversarial Review (round 2): ADR-056 — `list_jobs`, unified background-job visibility

**Spec reviewed**: `docs/internal/architecture/ADR-056-background-job-visibility.md` (v2, Proposed)
**Prior round**: `ADR-056-background-job-visibility-review.md` (v1 — BLOCK, 31 findings)
**Review date**: 2026-07-27
**Input mode detected**: structured-spec (FR/NFR/D/G/R identifiers; no BDD scenarios, no traceability matrix)
**Verdict**: **BLOCK**

---

## Executive Summary

v2 correctly killed the `shell` kind and correctly widened the status vocabulary. It then
applied the D7 standard to exactly one of the four kinds. **The `subagent` kind fails the
same test `shell` failed**: `LifecycleRecord` carries no parent-agent id, and the only
parent-ish field it does carry (`ParentDurableKey`) is the transcript session id — the very
field D7 cites, one file over, to prove that "a parent and every subagent it spawns stamp the
*same* value; the record cannot distinguish them". D4's third leg was asserted, not verified,
and §8 makes shipping it non-negotiable.

Three further defects are structural, not editorial: an unresolvable calling principal
silently returns the entire installation's jobs (all three stores treat `""` as "no filter");
the tool ships **denied on every existing installation** because the upgrade path backfills
unknown builtins to `deny`, not `allow`; and the durable session id `list_jobs` would hand
back is rejected by all seven `delegate` actions that consume it after a restart, so the
"durable, owner-scoped form" that Option B is chosen for delivers durable *listing* without
durable *acting*.

Two `[FACT]`-adjacent claims do not survive checking: `plan.Filter` has no owner field
(D4 claims "two of three kinds have cheap, existing owner filters"), and `Admit` is a
mutex-taking full plan-store scan (D2 claims cap pressure "costs nothing extra"). One claim
is a citation ADR-055 v3 explicitly *retracted* and this ADR inherited.

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 14 |
| MINOR | 8 |
| OBSERVATION | 3 |
| **Total** | **29** |

**Verified sound and left alone** (v2 fixed these correctly): D1's `blocked` slot and the
`state × plan_phase × paused_reason` model; D7's four shell blockers (all four re-verified
independently below); the `queued` non-derivability finding (`plan_engine.go:745` is
log-only, confirmed); §1's "seven of the nine `delegate` actions" (exactly seven at
`delegate.go:468`, nine cases at `:766-782`); `RECENTLY_FINISHED_CAP = 8`; `GET /activity`'s
≤50/24h shape; `list_tasks`' existing owner scoping and unbounded `json.Marshal`.

---

## Findings

### CRITICAL

---

#### [R2-CRIT-001] The `subagent` kind has no parent-agent linkage — D4's third leg is unimplementable for the majority case

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: D4 ("subagent → the durable lifecycle parent linkage"), FR-1, FR-2, NFR-1, §8 roll-up, R1
- **Description**: D4 assigns each kind an owner predicate and cites evidence for two of
  three. The third — *"subagent → the durable lifecycle parent linkage"* — names no field and
  cites nothing. The code answers it, and the answer is the same one that killed `shell`:

  1. **No parent-agent field exists.** `pkg/session/lifecycle.go` — `LifecycleRecord` carries
     `SessionID, Generation, ResumedFrom, State, OwnerScopeKind, OwnerScopeID, OwnsPlanID,
     GoalRef, WorkspaceID, AgentID, Is3P, LaunchProfile, ParentDurableKey, OriginChannel,
     OriginChatID, LastCheckpointRef, UndeliveredMessageIDs, NeedsInput, FailedReason,
     CreatedAt, UpdatedAt`. `AgentID` is the **child's** agent id (`pkg/tools/delegate.go:953`
     — `AgentID: agentID`, where `agentID` is the delegate *target*). There is no
     `ParentAgentID`.
  2. **`OwnerScopeID` is empty for the common case.** `delegate.go:935-939`:
     ```go
     ownerScopeKind := session.OwnerScopeHuman
     ownerScopeID := ""
     if parentDelegateID := strings.TrimSpace(ToolDelegateSessionID(ctx)); parentDelegateID != "" {
         ownerScopeKind = session.OwnerScopeParentSession
         ownerScopeID = parentDelegateID
     }
     ```
     A **top-level** delegation — a Main agent spawning a subagent, the primary case — mints
     `owner_scope_kind: human, owner_scope_id: ""`. Only a delegate spawning a *nested*
     delegate records a parent id, and even then it is the parent's **delegate session id**,
     not an agent id.
  3. **`ParentDurableKey` is the shared transcript session id.** `delegate.go:924` —
     `parentDurableKey := strings.TrimSpace(ToolTranscriptSessionID(ctx))`. And
     `pkg/agent/subturn.go:970` sets `TranscriptSessionID: parentTS.transcriptSessionID` on
     every child turn. This is **exactly** D7's own bullet 1, verbatim: *"a delegated child
     shares its parent's transcript session … a parent and every subagent it spawns stamp the
     same value; the record cannot distinguish them."* Scoping subagents by it returns the
     caller's children, its grandchildren, and — when the caller is itself a delegate — its
     siblings and its parent's other children.
  4. **The filter has no such field either.** `LifecycleFilter{WorkspaceID, AgentID, States,
     NonTerminalOnly}`, and `matches` tests `r.AgentID` — the child's.

  D7 declares four verified structural blockers for `shell` and calls them *"architectural"*,
  concluding *"shipping the kind would produce rows that are wrong, unrecoverable, or leak
  secrets."* Blockers 1 and 2 of that list apply verbatim to `subagent`. Yet R1 records the
  subagent kind's only risk as **cost**, and §8's roll-up makes it non-negotiable:
  *"`subagent` is the kind where handles are actually lost in practice, so shipping without it
  would miss the primary use case."*
- **Impact**: Implementation reaches §9 step 2 ("lifecycle records"), finds no parent
  attribution, and either (a) scopes by `ParentDurableKey` — shipping a roster that silently
  mixes in siblings, cousins and grandchildren, and that is *session*-scoped, the exact
  property Option C and Option D were rejected for; (b) scopes by `AgentID` — silently
  inverting the tool's meaning from "work I started" to "work others assigned me"; or (c)
  stalls. Every one of those is a wrong answer to "which of my jobs is still running", the
  question the ADR exists to answer.
- **Recommendation**: Either
  (a) **Add the field and say so.** Add `ParentAgentID string \`json:"parent_agent_id,omitempty"\``
  to `LifecycleRecord`, populate it from `ToolAgentID(ctx)` in the `delegate.go:940-955` mint,
  add a `ParentAgentID` clause to `LifecycleFilter.matches`, and record in the ADR that
  **pre-existing records have no value** so subagent recovery is prospective only (records
  written before the upgrade are unattributable and must be reported, not silently dropped —
  NFR-3). This is a contract change: `LifecycleRecord` has a generated wire counterpart
  (`SessionLifecycleRecord*`), so Constraint #8's 5-step order applies.
  (b) **Or apply D7's own standard and defer `subagent` too** — and then rewrite §8's roll-up,
  which currently rejects staging on the strength of this kind.
  Do not proceed to /plan-spec with D4's third leg unresolved; it is the load-bearing one.

---

#### [R2-CRIT-002] An unresolvable calling principal returns every plan, task and delegated session in the installation

- **Lens**: Insecurity (Information Disclosure) / Incompleteness
- **Affected section**: D4, FR-1, FR-5, §9 success criteria
- **Description**: D4 is the entire scoping decision and states no behaviour for an empty or
  unresolvable principal. Every store the ADR reads treats the empty string as *"filter
  disabled"*:
  - `pkg/task/store.go` — `if f.CreatedBy != "" && t.CreatedBy != f.CreatedBy { return false }`
  - `pkg/session/lifecycle.go` — `if f.AgentID != "" && r.AgentID != f.AgentID { return false }`
  - `pkg/plan/store.go:121-133` — `Filter` has **only** `WorkspaceID`; there is no owner clause
    at all (see R2-MAJ-001), so the in-memory comparison the ADR must add would need its own
    empty-guard.

  The precedent is already in-tree and unguarded: `pkg/tools/task.go:59` does
  `agentID := ToolAgentID(ctx)` and passes it straight into `Filter` with no non-empty check.
  For `list_tasks` the blast radius is the task store. For `list_jobs` it is **all three
  stores at once**, each row carrying a human-readable `label` (FR-4: plan title, task title,
  agent name) and a live, steerable `id` (FR-3: `plan_id` / `task_id` / `session_id`).
  D4's own stated reason for rejecting workspace-wide scope is *"that leaks other agents'
  work"* — an empty principal leaks strictly more than workspace-wide.
- **Impact**: Any invocation path that does not inject an agent id — a programmatic
  `Execute`, a System Agent context, a test harness, a future REST parity endpoint (§9 step 4)
  that forgets to bind the principal — silently returns the full installation roster
  including plan titles and other agents' handles, with a 200 and no warning. `delegate
  status` already documents this exact hazard for itself (`executeStatus`: *"all tasks are
  listed only when no channel/chat context is injected at all (e.g. direct programmatic
  Execute calls)"*), so the failure mode is known and precedented in the code the ADR is
  unifying.
- **Recommendation**: Add an explicit FR: *"`list_jobs` MUST fail closed. When the calling
  agent id is empty or unresolvable, it returns an error and no rows — never an unfiltered
  list."* Add it to §9's success criteria and require a regression test that asserts an
  error (not an empty list, and not a full list) for a context with no agent id. Apply the
  same guard to the plan-kind in-memory owner comparison, which has no `Filter` field to
  inherit an empty-guard from.

---

#### [R2-CRIT-003] D8's grant is fresh-install only — on every existing installation `list_jobs` ships backfilled to `deny`

- **Lens**: Incompleteness / Inoperability
- **Affected section**: D8, §2 Constraints (#6), §9 step 3, §9 success criteria
- **Description**: D8 decides *"seeded `allow` for all non-system agents"*. That seeding runs
  through `coreagent.SeedConfig`, which is fresh-install gated. For an **existing**
  installation the sequence is:
  1. `list_jobs` joins the known-builtin catalog (`buildKnownBuiltinToolNames`).
  2. `config.ValidateToolPolicyCoverage(cfg, knownTools)` finds an `(agent, list_jobs)` gap
     for every agent on disk, because no persisted config enumerates a tool that did not
     exist when it was written.
  3. `config.RepairIncompleteToolPolicyCoverage` (`pkg/config/validate.go:525-590`) runs
     **first**, and backfills every gap to `ToolPolicyDeny` on that agent's own
     `Tools.Builtin.Policies` map:
     ```go
     agentCfg.Tools.Builtin.Policies[gap.ToolName] = ToolPolicyDeny
     ```
     It logs one WARN per agent and boot proceeds normally.

  So every upgraded install gets `list_jobs: deny` on every agent, persisted, and the deny
  survives all subsequent reloads. The ADR's §2 note actively points away from this: *"the
  real risk is silent permissive inheritance … **not a boot abort**"*. The real risk is
  neither — it is a silent, fail-closed, *persisted* backfill.

  Every §9 success criterion passes on a fresh install and fails on 100% of upgrades. This is
  the pattern CLAUDE.md names as banned precedent — *"the ADR-037 anti-pattern this project
  explicitly bans"* — a control that reports success while changing nothing.
- **Impact**: v0.3 ships; on every existing installation the agent either never sees the tool
  or is denied on call; the only signal is a WARN in `gateway.log` naming `list_jobs` among a
  list of backfilled tools. Recovery is a manual per-agent policy edit. The feature is dead
  in the field and green in CI.
- **Recommendation**: D8 must decide the **migration**, not only the seed. Options, in
  increasing order of intrusiveness: (a) add `list_jobs` to the **global**
  `sandbox.tool_policies` seed with a migration that writes it into existing configs — a
  global `allow` combined with an absent agent entry resolves to `allow`
  (`compositor.go:189-190`, `case a == "": return g`), which satisfies coverage without
  touching per-agent maps; (b) add a targeted migration that inserts `list_jobs: allow` for
  every non-system agent before `RepairIncompleteToolPolicyCoverage` runs; (c) accept
  deny-by-default on upgrade and say so explicitly, with a documented operator step. Whichever
  is chosen, add a §9 success criterion phrased against an **upgraded** config, not a fresh
  one.

---

#### [R2-CRIT-004] The durable `session_id` `list_jobs` returns is not accepted by any `delegate` action after a restart — FR-3 and Option B's core advantage overstate the benefit

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: FR-3, NFR-1, §3 table ("Its real defect … reads the in-memory task map (lost on restart)"), §5 Option B "Strengths", D6
- **Description**: FR-3 states *"The `id` is the handle that kind's other tools accept: …
  subagent → `session_id`."* Option B's headline strength is that it *"absorbs `delegate
  status`'s enumeration into a **durable**, owner-scoped form"*, and §3 identifies
  in-memory-ness as `delegate status`'s *"real defect"*. `list_jobs` would read the id from
  the durable `LifecycleStore`. But **every consumer resolves it through an in-memory index**:
  ```go
  // pkg/tools/delegate.go:1310-1322, executeStatus
  t.mu.Lock()
  resolved, found := t.sessionIndex[sid]
  t.mu.Unlock()
  if !found {
      return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sid))
  }
  ```
  `t.sessionIndex` and `t.tasks` are process-global in-memory maps populated at spawn time
  (`listTaskCopies` reads `t.tasks` under `t.mu`). All seven session_id-bearing actions
  (`inbox, inbox_ack, steer, respond, cancel, follow_up, peek` — `delegate.go:468`) plus
  `status` route through the same map. After a gateway restart the map is empty and every one
  of them returns "No subagent found with session ID".

  The gap is not hypothetical for this ADR's motivating scenario. §1 names *"a wake starts a
  fresh turn"* and NFR-1 requires recovery *"from `list_jobs` alone"*. Across a restart the
  ADR-053 boot sweep additionally reconciles *"every persisted non-terminal session with no
  live runtime turn to failed(interrupted)"* (`plan_engine.go:571-577`), so the durable rows
  that survive are tombstones — and D6 classifies terminal rows as best-effort precisely
  because *"terminal work needs no handle to steer"*.

  Net: the durability Option B is chosen for delivers durable **listing** and not durable
  **acting**. Within one process lifetime the id works (the in-memory map is populated); across
  a restart the row exists, the work is dead, and the handle is inert. The ADR states none of
  this and sells the opposite.
- **Impact**: An agent calls `list_jobs`, receives a `session_id`, calls
  `delegate(action="steer", session_id=…)`, and is told the subagent does not exist — after a
  tool whose entire purpose was to hand it a working handle. Worse than the current state,
  where the agent knows it lost the handle.
- **Recommendation**: State the bound explicitly and scope FR-3 to it. Concretely: (a) amend
  FR-3 to *"the `id` is the handle that kind's other tools accept **for work started in the
  current process lifetime**; a row surviving a restart is informational"*; (b) amend §3's
  table and Option B's "Strengths" so the durability claim reads "durable enumeration",
  not "durable handle"; (c) add a §9 success criterion asserting the post-restart behaviour is
  an honest terminal row with a stated reason, not a live-looking row with a dead handle; and
  (d) decide whether an id whose in-memory entry is gone should be flagged on the row
  (e.g. `actionable: false`) rather than leaving the caller to discover it by failing.

---

### MAJOR

---

#### [R2-MAJ-001] `plan.Filter` has no owner field — D4's "two of three kinds have cheap, existing owner filters" is false

- **Lens**: Incorrectness
- **Affected section**: D4 CONFIDENCE block ("Basis"), NFR-4, R1, R2
- **Description**: `pkg/plan/store.go:120-124`:
  ```go
  // Filter narrows the result of List. All fields are optional (zero = skip).
  type Filter struct {
      WorkspaceID string
  }
  ```
  There is no `Owner`, no `CreatedBy`, no `OwnerAgentID`. Only **one** of the three kinds
  (`task.Filter.CreatedBy`) has an existing owner filter. Plan owner-scoping requires
  `Store.List` — which `scanPlanIDs` the directory, `load`s **every** `*.json`, and sorts —
  followed by an in-memory comparison the ADR must specify.
  Compounding it: plans are never deleted. `idleExpirySweep` (`plan_engine.go:1998`) only
  transitions running plans to failed; `plan.Store.Delete` exists but no sweeper calls it. So
  the plan kind has the same monotonic-growth profile R2 attributes exclusively to lifecycle
  records, and the same full-scan cost R1 attributes exclusively to subagents.
- **Impact**: NFR-4's cost model (*"reads at most three stores"*) and R1's framing (*"the
  third is the cost driver"*) both understate: **two of three** kinds are unindexed full
  scans over monotonically growing directories. The follow-up index R1 defers is needed for
  plans too, and nobody is told.
- **Recommendation**: Correct D4's CONFIDENCE "Basis" to "one of three kinds has an existing
  owner filter". Extend R1/R2 to name the plan store. Either add `Owner`/`CreatedBy` to
  `plan.Filter` (a small, contained change that keeps the predicate in one place, matching
  `task.Filter`) or state in D4 that plan owner-scoping is an in-memory post-filter over a
  full `List`.

---

#### [R2-MAJ-002] D2's "the data is free" is false — `Admit` takes the engine's exclusive mutex and re-scans the plan store

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: D2 ("Optionally enrich with cap pressure … costs nothing extra"; "Would improve: Including it — the data is free"), NFR-4
- **Description**: `pkg/agent/plan_engine.go:2182`:
  ```go
  func (pe *PlanEngine) Admit(kind string) (ok bool, active, maxConcurrent int) {
      pe.mu.Lock()
      defer pe.mu.Unlock()
      return pe.admitLocked(kind)
  }
  ```
  `admitLocked` calls `computeActiveLocked`, whose own doc says it *"sums running plans
  (scanned directly) plus every registered ActiveCounterFunc's current count"*. So one
  `Admit` call from a read-only visibility tool: (a) takes `pe.mu` **exclusively**, serialising
  against the tick loop's own `tryStartApprovedPlan` admission; (b) performs a second full
  plan-store `List` on top of the one R2-MAJ-001 already pays; (c) invokes every registered
  active-counter callback. The signature returning `(ok, active, maxConcurrent)` is free; the
  call is not.
- **Impact**: If the "optional enrichment" is taken (D2's "Would improve" recommends it), the
  new tool becomes a per-call contention point on the plan engine's central mutex, invoked by
  every agent on every turn it feels uncertain — precisely the calling pattern the tool
  encourages.
- **Recommendation**: Either drop the enrichment, or derive cap pressure without `Admit` — the
  ADR already needs a full plan `List` for the plan kind (R2-MAJ-001), so `active` can be
  counted from that same in-memory slice and `maxConcurrent` read from
  `config.PlanningConfig` (`DefaultGlobalActiveLoopCap`, `pkg/config/planning.go:17`) with
  zero additional locking. Correct D2's "Evidence" line, which currently cites only the
  signature.

---

#### [R2-MAJ-003] §2 repeats a claim ADR-055 v3 explicitly retracted: `compositor.go` fails CLOSED, it is not a permissive floor

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: §2 Constraints — Constraint #6 note; §9 step 3
- **Description**: The ADR states: *"Note the real risk is **silent permissive inheritance**
  via `pkg/tools/compositor.go:178-201`, not a boot abort — see ADR-055 D8."* The cited code
  does the opposite:
  ```go
  // pkg/tools/compositor.go:181-189
  case g == "" && a == "":
      // Structurally impossible once boot/write-time coverage validation is
      // in place ... Fail closed rather than silently defaulting to "allow".
      slog.Error("tools: no policy entry (global or agent) for tool; failing closed to deny", ...)
      return config.ToolPolicyDeny
  ```
  and the merge below it is deny-wins, then ask-wins, then allow. There is no permissive
  inheritance anywhere in the function; the only unconditional `allow` is the explicit
  `cfg.GodMode` branch above it.

  ADR-055 D8's **body** already corrects exactly this — *"**v3 correction — v2's claim here
  was false, and it was my error twice over** … It **fails closed to deny**"* — and closes
  with *"A citation inherited from a review is not verification."* ADR-056 v2 inherited the
  retracted version. (ADR-055's own CONFIDENCE block still says "compositor.go:178-201
  permissive floor", so the stale claim survives in both documents; that half is ADR-055's to
  fix.)
- **Impact**: §9 step 3 directs the implementer to *"Verify the seeded grant, given the
  permissive-floor risk in `compositor.go`"* — sending them to look for a hazard that does not
  exist while the actual upgrade-path hazard (R2-CRIT-003, a persisted `deny` backfill) goes
  unlooked-for. The ADR simultaneously dismisses "a boot abort", which is the one mechanism a
  reader might otherwise have traced to `RepairIncompleteToolPolicyCoverage`.
- **Recommendation**: Replace the note with the verified mechanism: *"Constraint #6 is enforced
  by boot/write-time coverage validation; an uncovered tool resolves to `deny`
  (`compositor.go:181-189`). The upgrade risk is `RepairIncompleteToolPolicyCoverage`
  backfilling the new tool to `deny` on every existing agent — see D8."* Drop the ADR-055 D8
  cross-reference for this point, or cite its v3 body rather than its stale confidence line.

---

#### [R2-MAJ-004] `native_status` is one string over a four-field composite, and the ADR never says which projection

- **Lens**: Ambiguity
- **Affected section**: FR-9, FR-3, D1
- **Description**: FR-9 makes `native_status` REQUIRED and defines it as *"the kind's own
  value"*. D1 correctly establishes that a plan's own status is **not** one value: it is
  `state` × `plan_phase` × `paused_reason` — and D1's own illustration adds a fourth,
  `failed_reason` (*"a plan `failed` for `stopped_by_user` and one for
  `judge_rounds_exhausted` remain distinguishable"*). Nothing says how those collapse into one
  string. Three defensible readings: `state` alone (loses everything D1 just fixed);
  `plan_phase` (undefined for non-running plans); a composite like
  `running/stalled` (unspecified separator, unspecified precedence — `Plan.yaml:111-116`
  defines a real precedence rule between `awaiting_owner_correction` and `stalled` that a
  naive composite would violate).
  The same ambiguity hits the subagent kind, where G4 concedes two vocabularies exist
  (durable 8-state; legacy 4-state at `delegate.go:1383` — `{"running","completed","failed",
  "canceled"}` — with the `cancelled`/`canceled` spelling split), and defers the choice.
- **Impact**: FR-9 exists to guarantee *"nuance is never lost"*, and it is the sole mitigation
  D1 offers for normalization risk. Two engineers build two different fields; a `stalled` plan
  can normalize to `blocked` (passing §9's regression criterion) while `native_status` reads
  `"running"` — which is the string an agent will quote back to a user.
- **Recommendation**: Define `native_status` per kind in the ADR, not in /plan-spec: for
  plans, specify the exact composite and its precedence (deferring to `Plan.yaml:111-116`);
  for tasks, `task.Status`; for subagents, resolve G4 here — the durable `LifecycleState` plus
  `FailedReason` when terminal — since the choice determines the field's type. Add a §9
  criterion asserting a stalled plan's `native_status` is not the bare string `running`.

---

#### [R2-MAJ-005] The tool has no request shape, so NFR-3's "narrow the query" and G3's "limit defaults" have nothing to attach to

- **Lens**: Incompleteness
- **Affected section**: FR-1 through FR-11, D6, G3, NFR-3
- **Description**: Eleven FRs define the response — rows, fields, sort, bounds, error entries —
  and **not one input parameter**. Yet D6 says truncation reports a count *"telling the caller
  when to narrow the query"*; G3 defers *"the live and terminal `limit` defaults"*, implying a
  `limit` argument; and D5's "live-or-recent" scoping implies an include-terminal switch. None
  are specified. There is also no cursor, so omitted rows are unreachable by any means.
- **Impact**: A caller told "43 rows omitted" has no action available — the count is
  informational only, which contradicts NFR-3's stated purpose (*"A dropped row is precisely
  the failure the caller is hunting"*). NFR-1's guarantee holds only if the live limit is
  never hit; §9's own stress criterion posits **500 live jobs**, so it will be.
- **Recommendation**: Add an FR defining the parameter set, e.g. `kind?` (one of
  `plan|subagent|task`), `status?` (one normalized value), `include_terminal?` (bool, default
  false), `limit?` (bounded, defaulting per G3). Compare `list_tasks`, which already ships
  `role` (required) + `status` (optional) and is the closest precedent. Then G3's defaults and
  D6's "narrow the query" both have a home.

---

#### [R2-MAJ-006] No workspace scoping decision, and no `workspace_id` on the row

- **Lens**: Incompleteness / Insecurity (Information Disclosure)
- **Affected section**: D4, FR-3
- **Description**: D4 scopes by owner and rejects workspace-wide as *"leaks other agents'
  work"*, but never decides whether the roster is scoped **to a workspace**. All three stores
  are workspace-partitioned and every filter already supports it (`plan.Filter.WorkspaceID`,
  `task.Filter.WorkspaceID`, `LifecycleFilter.WorkspaceID`). CLAUDE.md is explicit that *"the
  same agent can sit on multiple workspaces with different rosters and different trust in
  each"*, and ADR-037 made workspace the unit of delegation trust.
  FR-3's row is `kind, id, label, status, native_status, started_at` — no `workspace_id`, so a
  caller cannot tell which workspace a row belongs to even if the mixing is intentional.
- **Impact**: An agent acting in workspace A sees plan titles and steerable `plan_id`s from
  workspace B, with nothing on the row to distinguish them, and can act on them — a
  cross-workspace handle leak in a system whose trust boundary is the workspace. If the
  mixing is deliberate (an agent legitimately wants all its work), the caller still cannot
  attribute a row.
- **Recommendation**: Decide explicitly. Either scope to the calling context's workspace
  (`ToolWorkspaceID(ctx)`, which all three filters accept for free) and say so in D4; or keep
  the roster cross-workspace and **add `workspace_id` to FR-3's row**. State which, and add
  the corresponding §9 criterion.

---

#### [R2-MAJ-007] FR-5's task predicate has three incompatible readings and contradicts D4 and FR-1

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: FR-5, FR-1, D4, D5
- **Description**: FR-5: *"Tasks: standalone only (`plan_id == ""`), and only those the agent
  actually started **or that are still live** — not its whole authored backlog."*
  - *"actually started"* is undefined against a store that already distinguishes two owner
    axes: `list_tasks` uses `Filter.AgentID` for `role=assignee` and `Filter.CreatedBy` for
    `role=delegator` (`pkg/tools/task.go:63-67`).
  - D4 picks `CreatedBy` — tasks the agent created **for others**.
  - FR-1 says *"background work the calling agent started"*. A task assigned **to** the calling
    agent is its own foreground work, not background; a task it created for another agent is
    background. So D4's `CreatedBy` and FR-1 agree — but then "actually started" is a misnomer
    for "delegated away", and the tool omits exactly the tasks whose handles a busy agent is
    most likely to hold.
  - *"or that are still live"* carries **no owner predicate at all**. Read literally, it
    returns every non-terminal standalone task in the store regardless of owner — the opposite
    of D4.
- **Impact**: Three readings, three implementations, one of which is an unscoped disclosure
  (compare R2-CRIT-002). D5's confidence is "High — Missing: none material"; the predicate is
  material.
- **Recommendation**: Write FR-5 as a boolean over named fields, e.g. *"`plan_id == "" AND
  created_by == <caller>` AND (status is non-terminal OR the row falls inside the terminal
  bound of D6)"*, and delete the free-standing "or that are still live" clause. If tasks
  assigned *to* the caller should also appear, say so as a second explicit disjunct with its
  own field (`agent_id == <caller>`) and reconcile it with FR-1's "background" framing.

---

#### [R2-MAJ-008] One corrupt lifecycle file wipes out the entire subagent kind — and FR-11 makes that conformant

- **Lens**: Incompleteness / Inoperability
- **Affected section**: FR-11, NFR-1, NFR-3
- **Description**: `LifecycleStore.List` (`pkg/session/lifecycle.go:570-589`) aborts the whole
  enumeration on any per-record failure other than not-found:
  ```go
  rec, err := s.Load(id)
  if err != nil {
      if err == ErrLifecycleNotFound { continue }
      return nil, err            // <- one bad file kills every row
  }
  ```
  `tail` tolerates a torn *trailing line*, but a scanner error (e.g. a line exceeding the 10 MB
  buffer, an unreadable file, a permission fault) propagates. FR-11 then converts that into
  *"an explicit error entry for that kind"* — so the specified, conformant behaviour is: the
  agent loses **all** subagent rows, i.e. the kind §8 calls the primary use case, because one
  unrelated session file is damaged.
  `plan.Store.List` does the opposite and is the in-tree precedent: *"Unreadable/corrupt files
  are logged at Warn and skipped."*
- **Impact**: A single damaged JSONL — the kind of thing a full disk or an OOM-killed write
  produces, and this project's own CLAUDE.md documents disks as full as ~96% — takes the
  recovery tool's most important kind offline entirely, and the tool reports it as a kind-level
  error rather than "3 of 400 records unreadable".
- **Recommendation**: Require per-record skip-and-count for the subagent kind, folded into the
  same omission count NFR-3 already mandates, and reserve FR-11's kind-level error entry for a
  store-level failure (directory unreadable). This likely means adding a skipping variant
  alongside `LifecycleStore.List` rather than reusing it — note that as implementation work in
  §9 step 2.

---

#### [R2-MAJ-009] R1 frames the subagent scan as latency; it is lock contention against the live delegation write path

- **Lens**: Incompleteness / Inoperability
- **Affected section**: R1, NFR-4, G1
- **Description**: `LifecycleStore.Load` takes the per-session striped mutex **before** reading:
  ```go
  // pkg/session/lifecycle.go:342-350
  mu := s.Lock(sessionID)
  mu.Lock()
  defer mu.Unlock()
  rec, err := s.tail(sessionID)
  ```
  and `Persist`/`Mutate` take the same lock to record state transitions. So enumerating N
  sessions acquires N locks that **live delegations need in order to write their own state
  changes**. R1 describes this purely as read cost (*"O(sessions × lines), not O(sessions)"*)
  and mitigates with *"operator has accepted this cost … measure opportunistically"*.
  Nothing in the ADR bounds **call frequency**: the tool is designed to be called whenever an
  agent is uncertain, i.e. potentially every turn, by every agent, concurrently. No caching,
  TTL, or rate-limit decision is recorded.
- **Impact**: The visibility tool degrades the thing it is observing. Under the load profile
  §9 posits (500 live jobs) plus a handful of agents polling, delegation state transitions
  queue behind roster reads. That is a self-inflicted availability problem attributable to a
  read-only feature, and R1's "accepted cost" framing does not cover it because it was never
  the cost being discussed.
- **Recommendation**: Extend R1 to name write-path contention explicitly. Add a decision on
  call economics — the cheapest sufficient one is a short-TTL memoised roster per agent
  (seconds), which also caps the plan-store scans of R2-MAJ-001/MAJ-002. If no caching, state
  that and add a §9 criterion measuring delegation-transition latency while the roster is
  being polled.

---

#### [R2-MAJ-010] `approved → queued` is only true while the engine is ticking, and the ADR states no such precondition

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: D2, §9 success criteria ("A cap-queued plan appears as `queued`")
- **Description**: D2 justifies the mapping with `plan.go:72`'s comment (*"ready to run (or
  cap-waiting)"*) — verified correct. But `approved` means cap-waiting **only while the tick
  loop runs**: `Tick` dispatches `tryStartApprovedPlan` for every approved plan
  (`plan_engine.go:684-688`), and `Start` returns early without starting the loop when
  `planStore/taskStore/dispatcher/judge` are not all set (`plan_engine.go:551-555`).
  If the engine never started, or its ticker is stopped, **every approved plan reports
  `queued` forever** — indistinguishable from a plan genuinely waiting for a slot. That is the
  ADR's own §1 failure ("indistinguishable silence") reproduced in a second place, and §9's
  criterion *"A cap-queued plan appears as `queued`, distinct from `running`"* passes in both
  cases.
- **Impact**: An agent reports "the plan is queued behind the cap, I'll wait" for a plan that
  will never start. The mitigation already exists in D2 — cap pressure ("queued behind 16
  active") disambiguates immediately, because a stopped engine reports `active` far below the
  cap — but D2 leaves it *optional*.
- **Recommendation**: Either make the cap-pressure enrichment mandatory (derived cheaply per
  R2-MAJ-002's recommendation, not via `Admit`), or add a distinct normalized value / native
  qualifier for "approved, engine not admitting". Add a §9 criterion: *"an approved plan with a
  stopped engine does not report a bare `queued`."*

---

#### [R2-MAJ-011] D7 establishes label redaction as mandatory for shells, then exempts every label that actually ships

- **Lens**: Insecurity (Information Disclosure) / Inconsistency
- **Affected section**: D7, FR-4, FR-8, D6
- **Description**: D7 makes redaction a **precondition** for the deferred kind: a raw command
  line *"would return raw command lines into the caller's context and the persisted
  transcript, with no redaction — a credential exfiltration path"*, and requires
  `SensitiveDataReplacer` (verified to exist and to be applied at a comparable egress seam —
  `pkg/agent/session_messaging_wire.go`'s `buildContentEgressFilter`, described in-place as
  *"the N-10 content-egress policy filter: the SAME credential-store SensitiveDataReplacer the
  agent's own outputs obey"*, applied *"before persisting"*).
  FR-4's shipped labels — plan title, task title, agent name — are equally user- and
  agent-authored free text landing in the same two places. D6 requires them to be
  **truncated**, never **filtered**. Nothing explains why a command line is an exfiltration
  path and an agent-authored plan title is not.
- **Impact**: An agent that has read a secret can encode it in a task title it creates, and
  any agent that can call `list_jobs` — D8 says all non-system agents — reads it back and it
  is persisted in that agent's transcript. The redaction seam is one function call away and
  the ADR reasons about it correctly for the one kind that is not shipping.
- **Recommendation**: Apply the D7 rule to FR-4 uniformly: every `label` passes through
  `config.FilterSensitiveData` before truncation. Note the ordering (filter, then truncate —
  truncating first can split a secret across the boundary and defeat the replacer). Add a §9
  criterion.

---

#### [R2-MAJ-012] R6 names the `delegate status` overlap and misses the closer one — `list_tasks`

- **Lens**: Overcomplexity / Inconsistency
- **Affected section**: R6, G5, D5, §3 table, Option B
- **Description**: R6/G5 flag that `list_jobs` and `delegate status`-without-id will coexist
  with different scoping, and defer the resolution. The *closer* duplicate is left unmentioned:
  `list_tasks` reads the same store, applies the same `CreatedBy` predicate D4 selects, and
  already offers a status filter (`pkg/tools/task.go:38-50`). D5 then narrows the task kind to
  a **strict subset** of what `list_tasks` returns (standalone, non-terminal, plus a bounded
  terminal tail).
  So the install ships two owner-scoped enumerations of the task store with different scoping
  and different bounds — the exact R6 defect, unlisted, and §3's own table already identifies
  `list_tasks`' real problem as *"an unbounded `json.Marshal` of all matches"*, which
  `list_jobs` does not fix.
- **Impact**: Agent confusion about which tool answers "what are my tasks", divergent
  scoping over time (the failure mode R6 correctly predicts for `delegate status`), and a
  third store read in every `list_jobs` call for information already reachable.
- **Recommendation**: Ask the Overcomplexity question directly in §5 or §6: **does the `task`
  kind earn its place?** Two defensible answers — (a) drop it, point agents at `list_tasks`,
  and fix that tool's unbounded marshal (a smaller change that also closes §3's stated defect);
  or (b) keep it and commit to superseding `list_tasks`. Do not ship both without deciding, and
  fold `list_tasks` into G5 alongside `delegate status`.

---

#### [R2-MAJ-013] FR-6's sort truncates `blocked` rows first — the rows the tool exists to surface

- **Lens**: Incorrectness
- **Affected section**: FR-6, D3, D6, D1
- **Description**: FR-6 sorts `queued → running → blocked → failed → completed`, and D6 bounds
  the **live** block (`queued`/`running`/`blocked`) with a single shared limit. `blocked` is
  the last of the three live groups, so whenever live work exceeds the limit, the rows dropped
  first are precisely the `blocked` ones — the *"live but unable to progress without
  intervention"* rows D1 created the value for, and the ones §9's regression criterion is
  written about.
  D3 justifies the order as "operator-confirmed", but the v1 finding it answers (CRIT-001)
  recommended putting the attention-needing state **first**, on the stated rationale that
  *"problems surface without scrolling"* — which D3's own v1 predecessor cited approvingly.
  v2 kept the operator's ordering and added `blocked` in the middle, inheriting neither
  rationale.
- **Impact**: Under exactly the load the bound exists for, the tool reports queued and running
  work and silently (well — with a count, but still dropped) omits the stuck work. §9's
  criterion *"A stalled plan appears as `blocked`, never `running`"* passes on a small roster
  and fails on a large one.
- **Recommendation**: Either sort `blocked → queued → running → failed → completed` (surfacing
  intervention-needing work first, per the v1 rationale — reconfirm with the operator, since
  D3 attributes the current order to them), or give each live group its own sub-bound so
  `blocked` cannot be starved by `running`. State whichever in D6 as well, since the bound and
  the sort are only meaningful together.

---

#### [R2-MAJ-014] Adding a builtin is a multi-site catalog change; §9 lists one site

- **Lens**: Incompleteness
- **Affected section**: §9 "Implementation order" step 3, D8
- **Description**: §9 step 3 reads *"The `list_jobs` tool in `pkg/tools` + explicit policy
  seeding (D8)."* Registering a static builtin in this codebase touches at minimum:
  `allStaticToolNames` (`pkg/coreagent/core.go:295` — *"the complete, hardcoded enumeration of
  every static tool"*, with a typo-safety net at `:357-365` that **rejects** a policy override
  naming a tool absent from the list); the global seed at `pkg/config/defaults.go:275` (whose
  comment states *"Every entry below mirrors pkg/coreagent/core.go's allStaticToolNames"*); the
  per-agent seeds in `coreagent.SeedConfig`'s `denyAllThenOverride` calls; and
  `buildKnownBuiltinToolNames` in the gateway. Miss `allStaticToolNames` and the seed override
  is rejected at boot; miss the seed and R2-CRIT-003's deny-backfill fires even on a fresh
  install.
- **Impact**: An implementer following §9 literally ships a tool that is registered but denied,
  or a seed that fails validation — with the failure surfacing at boot rather than at build.
- **Recommendation**: Enumerate the sites in §9 step 3, and add the upgrade migration decided
  under R2-CRIT-003 as its own step.

---

### MINOR

- **[R2-MIN-001] File citations omit package paths, and three are misleading.** `plan_engine.go`
  is `pkg/agent/plan_engine.go`, not `pkg/plan/` as §1's neighbouring `config/planning.go`
  citation implies; `session_messaging_wire.go` is `pkg/agent/`, not `pkg/gateway/`;
  `session.go` is `pkg/tools/`. Line numbers all check out (`:544` `Start`, `:623` `runTickLoop`,
  `:637` `runEventLoop`, `:2182` `Admit`, `:2248` `resolveGlobalCap`, `:743-748` the log-only
  cap branch, `planning.go:17` the cap of 16). Two are off by a few lines: D7's reaper cite
  `session.go:349-397` (the goroutine starts at `:344`, `cleanupOldSessions` runs `:361-397`)
  and D5's `rest_tasks.go:1648` (the `PlanID != ""` rejection is at `:1646-1649`). *Fix*: prefix
  every citation with its package path; ADR-055 D8's own lesson was a wrong path
  (`pkg/sandbox/` for a `pkg/tools/` file).

- **[R2-MIN-002] NFR-4 and R1 contradict each other.** NFR-4 requires the tool *"does not
  re-read whole session files where avoidable (see R1)"*; R1 states the operator has **accepted**
  that it does exactly that. As written NFR-4 is either vacuous ("where avoidable" is never) or
  violated by design. *Fix*: restate NFR-4 as a budget the design actually meets, or delete it
  and let R1 carry the cost statement alone.

- **[R2-MIN-003] G3 and §9's stress criterion are circular.** §9 requires *"Response size stays
  bounded with 500 live jobs and pathological labels"*, which is untestable until G3 picks the
  limits; G3 defers them to /plan-spec. *Fix*: pick provisional numbers here (live 100,
  terminal 20, label 120 chars are defensible starting points) so the criterion is executable,
  and let /plan-spec tune them.

- **[R2-MIN-004] R5 says "this ADR should not be accepted before ADR-055", but §9's handoff has
  no gate.** The handoff sends the ADR to `/grill-spec` then straight to `/plan-spec`; ADR-055
  is still Proposed and its D2 is the source of D4's plan-owner semantics. *Fix*: add the
  ADR-055 acceptance as an explicit precondition in §9's "Resolve before implementation" list.

- **[R2-MIN-005] "Evidence level (highest used): 1 — operator decisions + direct codebase
  verification" overstates.** D4's subagent leg (R2-CRIT-001), D8's availability claim
  (R2-CRIT-003) and D2's "costs nothing extra" (R2-MAJ-002) are asserted without codebase
  verification, in a document whose §2 also propagates a citation ADR-055 explicitly retracted
  (R2-MAJ-003). *Fix*: either verify them or downgrade the header and tag the unverified
  decisions individually.

- **[R2-MIN-006] The plan kind's owner field is `Owner`, but D4's mixed-namespace caveat is
  written about `CreatedBy`.** `plan.Plan` carries both (`pkg/plan/plan.go:437-438`), and
  `create_plan` sets `Owner: callerID, CreatedBy: callerID` (`pkg/tools/plan.go:285-287`) while
  the REST path sets `Owner: c.Username` (`rest_plans.go:547`). So `Owner` has the identical
  mixed-namespace defect, and the caveat names the wrong field for the kind it applies to.
  *Fix*: state the caveat against both fields, and say which one the plan kind filters on.

- **[R2-MIN-007] `OwnerAgentID` — the field that actually determines which agent runs and is
  woken by a plan — is never considered as the plan-kind predicate.** D4 picks `Owner` (the
  creator). A user creating a plan in the SPA sets `Owner = username` and `owner_agent_id =`
  the agent that will run it; that agent is exactly the one that loses handles and is woken at
  decision points (`plan_engine.go:1254, 1542, 1571, 1610, 1742`), and it would see **none** of
  those plans in `list_jobs`. *Fix*: state why `Owner` beats `OwnerAgentID`, or use both as a
  disjunction ("plans I created OR plans I run").

- **[R2-MIN-008] "`blocked` means live but unable to progress without intervention" does not fit
  the plan `paused_reason` case it maps.** D1 maps `paused_reason != ""` to `blocked`, but the
  only reason ever written is `owner_disabled` (`plan_engine.go:156`, confirmed the sole
  writer) — which resolves automatically when the owner agent is re-enabled, i.e. it is
  waiting, not blocked-on-intervention. *Fix*: either widen the definition or split
  auto-resolving pauses from intervention-requiring ones.

---

### OBSERVATIONS

- **[R2-OBS-001] The row has no freshness field.** FR-3 carries `started_at` only. For a
  `blocked` or `running` row the actionable signal is *how long since anything happened* —
  available for free as `Plan.last_activity_at` and `LifecycleRecord.UpdatedAt`. Consider
  adding `last_activity_at`; it costs nothing at read time and it is what an agent needs to
  decide whether to intervene.

- **[R2-OBS-002] `list_jobs` vs `list_tasks` vs the `task` kind.** The tool is named for "jobs",
  returns a kind called "task", and coexists with a tool called `list_tasks` (see R2-MAJ-012).
  If the task kind survives, consider a name that does not collide — the tool's own value is
  that an agent reaches for it *without knowing which kind it lost*, which argues for a name
  about recovery rather than about listing.

- **[R2-OBS-003] §9 step 4's REST parity would not let the SPA retire ActivityPanel.** The step
  suggests it *"could then let the SPA retire its narrower session-scoped aggregation"*. The
  SPA's panel is session-scoped **by design** (per CLAUDE.md it shows subagent spans and
  background bash sessions for the active session); an agent-scoped, cross-workspace roster is
  a different product. Treat step 4 as an independent decision, not a consequence.

---

## Structural Integrity Results (structured-spec mode)

| Check | Result | Note |
|---|---|---|
| Every stated goal has acceptance criteria | **PASS** | §9 success criteria map to NFR-1, D1, D2, NFR-3, FR-11, NFR-2 |
| Cross-references within the document are consistent | **FAIL** | NFR-4 ⟂ R1 (R2-MIN-002); FR-5 ⟂ D4 ⟂ FR-1 (R2-MAJ-007); FR-6 ⟂ D6 (R2-MAJ-013); R5's gate absent from §9 (R2-MIN-004) |
| Scope boundaries explicitly defined | **PASS** | §9 "Deliberately out of scope" is clear and D7 states preconditions |
| Success criteria measurable | **PARTIAL** | 5 of 6 measurable; the 500-job bound is untestable until G3 resolves (R2-MIN-003); none is written against an upgraded install (R2-CRIT-003) |
| Requirements that reference each other are consistent | **FAIL** | FR-9's `native_status` is undefined against D1's composite model (R2-MAJ-004) |
| Error/failure scenarios addressed per requirement | **PARTIAL** | FR-11 covers per-kind store failure; unresolvable principal (R2-CRIT-002), per-record corruption (R2-MAJ-008) and dead-handle-after-restart (R2-CRIT-004) are not |
| Dependencies between requirements identified | **PARTIAL** | R5 names ADR-055; the `LifecycleRecord` schema change CRIT-001 requires, and the config-migration CRIT-003 requires, are unidentified |
| Request/response contract complete | **FAIL** | No request shape at all (R2-MAJ-005) |
| Evidence tags survive verification | **FAIL** | 3 claims falsified (R2-MAJ-001, R2-MAJ-002, R2-MAJ-003); 1 asserted without evidence and falsified on check (R2-CRIT-001) |

---

## Test Coverage Assessment

The ADR has no test plan; §9's six success criteria are the closest artefact, and they are a
reasonable skeleton. Gaps, in priority order:

1. **No negative-scoping test.** Nothing asserts that agent A cannot see agent B's rows — the
   single most important property of D4, and the one R2-CRIT-002 shows is unguarded. Required:
   two agents, cross-visibility asserted zero; plus an empty-principal case asserting an
   **error**, not an empty list and not a full list.
2. **No upgrade-path test.** Every criterion is implicitly fresh-install. Required: load a
   pre-upgrade `config.json` with no `list_jobs` entry, boot, and assert the tool is callable
   by a non-system agent (R2-CRIT-003).
3. **No restart test.** NFR-1's motivating scenario is a lost handle; the handle's behaviour
   across a process boundary (R2-CRIT-004) is untested and the ADR's claim about it is wrong.
   Required: spawn async delegate → restart → `list_jobs` → assert the row's status and that
   the returned id's behaviour matches whatever FR-3 is amended to promise.
4. **No concurrency test.** Three stores are read non-atomically, so a plan can be observed
   `running` while its member task is already `done`. No requirement states whether that skew
   is acceptable. Required at minimum: a stated tolerance, and a test that concurrent
   `list_jobs` calls during active dispatch never error.
5. **No corruption test.** R2-MAJ-008's single-bad-file case needs a test asserting the other
   399 rows still return.
6. **Boundary tests missing for every bound.** G3 defers the numbers, so there is nothing to
   test at `limit`, `limit+1`, `limit-1`, at zero rows, or at a label exactly at/over the
   truncation length. `list_tasks`' existing unbounded `json.Marshal` is the in-tree example of
   this class of defect shipping.
7. **Regression risk is unnamed.** The ADR changes no existing behaviour, but adding a builtin
   perturbs `allStaticToolNames`, the global seed, and every agent's policy map — all of which
   have existing tests that will need updating. §9 does not mention it.

---

## STRIDE Threat Summary

| Component / flow | Threat | Status in ADR |
|---|---|---|
| `list_jobs` → caller principal | **Spoofing / EoP** — an unresolvable `ToolAgentID` disables all three filters | **Not addressed** (R2-CRIT-002). No fail-closed requirement. |
| Row `label` (plan/task title, agent name) | **Information disclosure** — agent-authored free text, unredacted, into caller context + persisted transcript | **Inconsistently addressed** (R2-MAJ-011). D7 mandates redaction for the deferred kind and exempts the shipped ones. |
| Row `id` (`plan_id` / `task_id` / `session_id`) | **EoP** — a handle from another workspace becomes steerable | **Not addressed** (R2-MAJ-006). No workspace scoping decision. |
| `LifecycleStore` enumeration | **DoS** — N striped-mutex acquisitions per call, unbounded call frequency, contends with the delegation write path | **Partially** (R1 covers latency only, R2-MAJ-009). |
| `PlanEngine.Admit` (optional enrichment) | **DoS** — exclusive engine mutex + full plan scan per call | **Not addressed** (R2-MAJ-002); ADR asserts it is free. |
| `plan.Store.List` | **DoS** — full directory scan over a never-swept store | **Not addressed** (R2-MAJ-001); R1/R2 name only lifecycle records. |
| Tool policy resolution | **EoP** — the ADR's stated risk (permissive inheritance) does not exist; the real one (persisted deny backfill) is unstated | **Misdirected** (R2-MAJ-003, R2-CRIT-003). |
| Tool invocation | **Repudiation** — no audit requirement for a cross-store read that returns other-entity metadata | Not addressed; low priority given the read is owner-scoped *when* scoping works. |
| `native_status` / `label` truncation | **Tampering** — n/a, read-only | Correctly out of scope. |

---

## Unasked Questions

1. **What field links a delegated session to the agent that started it?** D4 asserts a linkage
   exists. Name it, or add it. (R2-CRIT-001)
2. **What does `list_jobs` do when it cannot identify the caller?** (R2-CRIT-002)
3. **What happens on the upgrade path — not the fresh install?** (R2-CRIT-003)
4. **Is the returned `session_id` usable after a restart, and if not, does the row say so?**
   (R2-CRIT-004)
5. **Is the roster workspace-scoped?** Every store supports it for free; the ADR is silent.
   (R2-MAJ-006)
6. **What are the tool's input parameters?** Eleven FRs describe a response and no request.
   (R2-MAJ-005)
7. **Does the `task` kind earn its place next to `list_tasks`?** (R2-MAJ-012)
8. **Which of `Owner` / `OwnerAgentID` / `CreatedBy` identifies "the agent whose plan this is"?**
   All three exist on `plan.Plan`; the ADR names one without arguing for it. (R2-MIN-006/007)
9. **How often may an agent call this?** No caching, TTL, or rate decision, against a
   lock-taking O(sessions × lines) read. (R2-MAJ-009)
10. **Do plan/task labels need the same redaction D7 requires of shell labels?** (R2-MAJ-011)

---

## Verdict

**BLOCK** — 4 CRITICAL, 14 MAJOR.

The four CRITICAL findings are not editorial. R2-CRIT-001 removes the ADR's primary kind by the
ADR's own D7 standard; R2-CRIT-002 is an unscoped-disclosure path through the one decision that
defines scope; R2-CRIT-003 ships the feature disabled everywhere it matters; R2-CRIT-004 falsifies
the advantage Option B was selected for. Each is resolvable — CRIT-001 by one field on
`LifecycleRecord` plus a filter clause, CRIT-002 by one fail-closed FR, CRIT-003 by a migration
decision in D8, CRIT-004 by scoping FR-3's promise honestly — but none can be deferred to
/plan-spec, because each changes what the ADR decides rather than how it is built.

Address the findings, then re-run:

```
/grill-spec docs/internal/architecture/ADR-056-background-job-visibility.md
```
