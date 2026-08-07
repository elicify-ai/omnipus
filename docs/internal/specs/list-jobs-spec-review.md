# Adversarial Review: `list_jobs` Specification

**Spec reviewed**: `docs/internal/specs/list-jobs-spec.md` (Draft, 2026-07-27)
**Review date**: 2026-07-27
**Reviewed against**: working tree, branch `feature/plan-swimlane-board`
**Input mode detected**: `plan-spec` (BDD scenarios + FR-xxx + traceability matrix + SC-xxx — full structural checks applied)

> **Method note.** This spec's central claim is that it re-verified every code fact ADR-056 got
> wrong. That claim was itself audited: every `[VERIFIED:]` tag load-bearing on a functional
> requirement was independently re-read in the working tree. **The nine corrections C1–C9 all hold.**
> The defects below are almost entirely in the *consequences* the spec drew from them, and in the
> code facts it did **not** check — the ones adjacent to the fields it added, not the ones the
> previous reviews named.

---

## 1. Executive Summary

Thirty findings: **3 CRITICAL, 17 MAJOR, 7 MINOR, 3 OBSERVATION**. The spec's evidence
discipline is real and its nine ADR corrections survive re-verification, but three requirements
ship a tool that actively lies to its caller in the two situations it was written for — after a
gateway restart, and when a plan is cap-waiting — and one required field is an unbounded,
unredacted free-text channel that defeats the spec's own redaction control.

**Verdict: BLOCK.**

> **Two addenda follow §6, and they carry the operator-facing conclusions:**
> **§7** applies the *no-migration / greenfield* ruling that postdates the spec — it deletes
> `unattributable_subagents` and its scenario/test and resolves Ambiguity #8, but **FR-015 must be
> rewritten, not deleted** (7.3): `ParentAgentID == ""` stays reachable at mint time on a brand-new
> install, and the proposed `omitempty` tag would turn a counted row into a silently dropped one.
> **§8** independently re-audits the four load-bearing evidence claims: `ToolAgentID` ✅ verified,
> `denyAllThenOverride` ✅ verified, the compositor's *"fails CLOSED"* headline ⚠️ misleading and
> missing god-mode.

---

## 2. Findings

### CRITICAL

---

#### CRIT-001 — Cap pressure is derived from an owner-scoped list but compared against a global cap; the number is arithmetically wrong and inverts the signal

**Lens**: Incorrectness · **Sections**: FR-021, US-2 AS-5, SC-003, BDD "Cap pressure distinguishes a real queue from a stopped engine", Evaluation Scenario "The agent correctly waits rather than intervening"

FR-021 mandates: *"Cap pressure (`cap_active`, `cap_max`) MUST be derived from the plan list the tool already reads plus `config.PlanningConfig`, with no additional locking."*

The plan list the tool already reads is filtered by `WorkspaceID` **and** `OwnerAgentID`
(FR-009, FR-010). The real active count is neither:

```go
// pkg/agent/plan_engine.go:2221 computeActiveLocked
runningPlans, err := pe.planStore.List(plan.Filter{})   // ← UNFILTERED: every workspace, every owner
...
for kind, fn := range pe.activeCounters {               // ← /goal and /loop counts,
    n, err := fn()                                      //   NOT in the plan store at all
    count += n
}
```

`cap_max` (16, `DefaultGlobalActiveLoopCap`, verified `pkg/config/planning.go:17`) is a **global**
brake shared across every agent, every workspace, and the `/goal` and `/loop` subsystems. A
`cap_active` derived from the caller's own workspace-and-owner-scoped rows is a strict undercount
missing three whole populations.

The consequence is not "slightly off" — it is **inverted**. Take exactly the scenario the field was
added for: agent A has one approved plan, cap-waiting behind 16 running plans owned by other
agents. The true state is `active=16, cap=16` → "healthy queue, wait." The derived value is
`cap_active=0, cap_max=16` → "far below cap, nothing will ever start it" → A intervenes on healthy
work. Evaluation Scenario "The agent correctly waits rather than intervening" asserts the exact
opposite of what this implementation produces, and `TestListJobs_CapPressureWithoutAdmit` (which
only asserts `Admit` was not called) will pass while it does so.

The spec correctly rejected calling `Admit` (C5 — it takes `pe.mu` exclusively). It then substituted
a number that is not the same number, without noticing. `admitLocked` additionally carries a
`reliable bool` fail-closed signal on a partial read; the derived version has no equivalent, and
FR-018's per-kind plan error entry leaves `cap_active` silently wrong with no marker.

**Fix**: pick one and state it explicitly.
(a) Add a lock-free read-only accessor to `PlanEngine` that returns the last `computeActiveLocked`
result plus its `reliable` flag and a staleness timestamp, cached on each tick — `Admit` is not the
only way to reach that data. (b) Have the tool perform its **own** unscoped `planStore.List(plan.Filter{})`
count *in addition to* its scoped read, and register/read the `activeCounters` — and then own the
doubled scan cost explicitly against A5. (c) Drop `cap_active`/`cap_max`, replace with a boolean
`engine_admitting` sourced from the engine's own started/stopped state, and rewrite US-2 AS-5 and
SC-003 around it. Whichever is chosen, add `cap_reliable` (or omit both fields when unreliable) and
a scenario asserting a cap-saturated install reports saturation.

---

#### CRIT-002 — `native_status` is a REQUIRED, unbounded, unredacted free-text field composed from two unvalidated string sources

**Lens**: Insecurity (Information Disclosure, DoS) · **Sections**: FR-003, FR-006, FR-019, SC-005, SC-011

FR-003: *"`native_status` is REQUIRED, never omitted."* FR-019 applies `FilterSensitiveData` +
rune truncation to **`label` only**. `native_status` gets neither.

Its inputs are not enums:

| Source | Type | Validated? |
|---|---|---|
| `session.LifecycleRecord.FailedReason` | `string` | **No** — the field's own doc says *"Left open (not a closed enum)"* (`pkg/session/lifecycle.go:238-240`) |
| `plan.Plan.PausedReason` | `string` | **No** — never passed to any validator; only `!= ""` is ever tested (`plan.go:378`, `:122`, `plan_engine.go` processPlan) |
| `plan.Plan.FailedReason` | `plan.FailedReason` | Yes — `IsValidFailedReason`, 4 values (`plan.go:302-311`, enforced `:498`) |
| `plan.PlanPhase` | `PlanPhase` | Yes — `validPlanPhases` (`plan.go:260-274`) |

So two of the four composite sources are arbitrary strings written by the runtime (wrapped errors,
paths, upstream API text). The spec's own BDD table lists `failed:interrupted` and
`failed:judge_rounds_exhausted` as if the lifecycle side were closed; it is not.

Two consequences:

1. **The redaction control is bypassable.** FR-019's entire justification — *"agent-authored free
   text landing in the same caller context and persisted transcript"* — applies verbatim to
   `native_status`, and the spec applies D7's standard to `label` and then leaves the adjacent
   required field open. A pause reason or failure reason carrying a credential-bearing URL reaches
   the caller's context and the persisted transcript unfiltered. SC-011's "leaks zero substrings"
   guarantee is scoped to `label` and is therefore not a guarantee about the response.
2. **SC-005 is unenforceable.** *"the serialized response is ≤ 32 KB"* — with an unbounded string
   on every row and no per-row cap, one wrapped error is enough to blow it. FR-016/FR-017 bound the
   *number* of rows; nothing bounds row *size* except `labelMax`.

**Fix**: extend FR-019 to *every* free-text field on the row — name them exhaustively
(`label`, `native_status`, and any future addition) rather than by field name — with the same
redact-then-rune-truncate order and a stated per-field maximum. Add a scenario and a dataset row
for a `LifecycleRecord.FailedReason` carrying a registered secret. Amend SC-005 to state the
response-size bound as a derived arithmetic identity
(`maxRows × (labelMax + nativeStatusMax + fixedOverhead)`) so it is checkable rather than hoped for.

---

#### CRIT-003 — After a restart — the single most common real trigger — the default call returns an empty roster with `total_omitted=0`, asserting the work does not exist

**Lens**: Incompleteness · **Sections**: US-4 AS-5, US-6, FR-017, FR-022, BDD "A post-restart subagent row is an honest tombstone", BDD "Terminal rows are excluded by default"

Three requirements compose into the exact failure US-4 was written to forbid.

1. The ADR-053 boot sweep reconciles every persisted non-terminal session with no live runtime turn
   to `failed(interrupted)` on restart (verified `pkg/agent/plan_engine.go:570-578`, `runBootSweep`).
   So after **every** gateway restart, all of the caller's subagent rows are terminal.
2. `include_terminal` defaults to `false` (FR-002), and terminal rows are then *"excluded entirely"*
   (FR-016) with *"no terminal-store scan cost … paid"* (US-4 AS-5).
3. Because they are never scanned, they are never counted. FR-017's *"Every omission MUST be
   reported with an exact count"* applies to bound-driven truncation, not to the
   `include_terminal=false` exclusion — nothing in the spec says otherwise.

Net: agent A spawns three delegations, the gateway restarts, A calls `list_jobs` with default
arguments and receives `rows: [], total_omitted: 0` — a well-formed empty roster that US-1 AS-4
explicitly defines as the success shape for *"agent A has no background work at all."*

That is the SPA `RECENTLY_FINISHED_CAP = 8` anti-pattern the spec names as *"exactly the pattern
not to copy"* — a silent cap that *"asserts 'nothing else exists' and actively misleads"* — with a
worse trigger, because it fires on every restart rather than only under load. US-6's own BDD
scenario quietly concedes the problem by passing `include_terminal=true`, which the agent has no
reason to do: it calls the tool precisely because it does not know the state.

**Fix**: require a `terminal_suppressed` count (per kind and total) on **every** response where
`include_terminal=false`, populated from a cheap count that does not materialize the rows, and
require it to be non-zero-visible. Add a BDD scenario: *"Given the caller's only jobs are terminal,
When `list_jobs` is called with default arguments, Then zero rows are returned **and**
`terminal_suppressed` is exactly N."* Then re-word US-4 AS-5 — the "no scan cost" claim must be
dropped or reduced to "no terminal rows are materialized", because a count still requires the scan
(see MAJ-004). Add an SC asserting a post-restart default call never reports a state
indistinguishable from "no work".

---

### MAJOR

---

#### MAJ-001 — FR-018's `unreadable` count is unobtainable for two of the three kinds; the cited precedent proves the opposite of what it is cited for

**Lens**: Infeasibility · **Sections**: FR-018, US-5, SC-006, Integration Boundaries (`pkg/plan`), Dataset "Store failure modes"

FR-018: *"A per-record read failure MUST cause that record to be skipped and **counted**
(`unreadable`)."*

Integration Boundaries cites the plan store as the in-tree precedent: *"Unreadable/corrupt files are
already logged at Warn and skipped — the in-tree precedent for FR-018."* Verified:

```go
// pkg/plan/store.go List
p, err := s.load(id)
if err != nil {
    slog.Warn("plan: skip unreadable plan file", "id", id, "error", err)
    continue                    // ← count is discarded, never returned
}
```

`pkg/task/store.go` `List` is byte-for-byte the same shape. Both **skip** and both **swallow the
count**. A precedent for skipping is not a precedent for counting — and the spec's design work went
entirely into `ListLenient` for the lifecycle store (the one kind that today *aborts*), leaving the
two kinds that already skip with no way to report.

The Symbols Involved table lists `plan.Filter` as *modifies* and `plan.Store.List` not at all;
`task.Filter` is listed as *"calls"* / *"No change"*. So an implementer following the spec ships
`unreadable` permanently pinned at 0 for `plan` and `task`, while US-5's premise is *"A short list
that looks complete is the worst possible output."* Two of three kinds ship exactly that. Neither
the BDD scenarios nor the dataset covers a corrupt **plan** or **task** file — Dataset "Store
failure modes" rows 2/3/4/7 are all lifecycle; row 5 is plan but store-level.

**Fix**: add an FR requiring `plan.Store.List` and `task.Store.List` to gain a lenient sibling
(mirroring `ListLenient`) returning `(records, skipped int, err error)`, list both in the Symbols
table with their d=1 dependents, and add a BDD scenario + dataset row for one corrupt plan file and
one corrupt task file. Or scope FR-018's counting clause to the `subagent` kind explicitly and say
so in the tool description — but then SC-006 and US-5's guarantee must be re-worded to match.

---

#### MAJ-002 — `task.Filter` has no `PlanIDSet`; FR-010's `PlanID == ""` predicate is inexpressible, and the spec invents the field in a parenthetical while its own Symbols table says "No change"

**Lens**: Inconsistency / Infeasibility · **Sections**: FR-010, FR-022, Symbols Involved, Integration Boundaries (`pkg/task`)

Integration Boundaries: `task.Filter{WorkspaceID, CreatedBy, PlanID:"" (+PlanIDSet), Status}`.
Symbols Involved: `task.Filter` … *"Already has `CreatedBy`, `AgentID`, `PlanID`, `Status`,
`WorkspaceID`. **No change.**"* Those two statements contradict each other, and the code sides with
neither:

```go
// pkg/task/store.go Filter
ParentTaskID string
// ParentTaskIDSet, when true, applies the ParentTaskID filter even when
// ParentTaskID is empty — i.e. "only top-level tasks" (no parent).
ParentTaskIDSet bool
...
if f.PlanID != "" && t.PlanID != f.PlanID { return false }   // ← "" == filter off
```

The `…Set` escape hatch exists — for `ParentTaskID` only. There is **no `PlanIDSet`**, so
"standalone tasks only" cannot be expressed. No FR requires adding it (FR-014 adds
`plan.Filter.OwnerAgentID` and nothing else). An implementer either invents an unrequired field or
post-filters in the tool — and post-filtering breaks FR-022 and corrupts the bound arithmetic if
bounds are applied before the discard.

**Fix**: add an FR mirroring FR-014: *"`task.Filter` MUST gain `PlanIDSet bool`, matching the
existing `ParentTaskIDSet` convention"*, list `task.Filter` as **modifies** in the Symbols table
with its impact row, and add the regression test
`TestTaskFilter_UnsetPlanIDSetUnchangedBehaviour` to the Regression Test Requirements table
alongside the `plan.Filter` one already there.

---

#### MAJ-003 — FR-022's "apply the filter rather than loading and discarding" is false for all three stores; US-4 AS-5's cost claim has no basis

**Lens**: Incorrectness · **Sections**: FR-022, US-4 AS-5, SC-005, A5

FR-022: *"When `include_terminal=false`, the system MUST apply `NonTerminalOnly` at the filter level
rather than loading and discarding terminal rows."*

All three stores scan the directory, `load()` **every** file, and only then call `filter.matches()`:

- `plan.Store.List` — `scanPlanIDs()` → `s.load(id)` for every id → `filter.matches(p)`
- `task.Store.List` — identical shape
- `session.LifecycleStore.List` — `scanSessionIDs()` → `s.Load(id)` **taking the per-session striped
  mutex for every session** (`lifecycle.go:342-348`) → `filter.matches(rec)`

The filter trims the returned slice. It saves zero I/O and, for the lifecycle store, zero lock
acquisitions. `NonTerminalOnly` therefore cannot deliver what FR-022 claims — and `plan.Filter` and
`task.Filter` have no `NonTerminalOnly` field at all, so for two kinds the sentence names a
mechanism that does not exist. (`task.Filter.Status` is a single `Status`, not a set, so
"status ∈ {inbox, next, in_progress, blocked}" needs four calls or a post-filter.)

This propagates: US-4 AS-5's *"no terminal-store scan cost is paid"* is false; A5's *"This spec
accepts the read cost"* is accepting a cost it has mis-measured by the terminal:live ratio (the
Evaluation Scenario "Roster at scale" posits 2 000 terminal to 500 live — a 5× understatement); and
SC-005's scale claim rests on it.

**Fix**: replace FR-022's second sentence with what is achievable — *"the system MUST NOT
materialize terminal rows into the response, and MUST NOT be represented as avoiding store I/O;
every kind's `List` loads every record regardless of filter"* — and either add an FR for
directory-level pre-filtering (e.g. a status index) or state in A5 that read cost scales with
**total** records, live and terminal, in a monotonically growing store.

---

#### MAJ-004 — FR-011's `actionable` requires reading a private map behind the delegate tool's hottest mutex; no accessor exists, no wiring is specified, and the contention is unbudgeted

**Lens**: Infeasibility · **Sections**: FR-011, US-6, Symbols Involved, FR-021, SC-012

FR-011 requires resolving each subagent row against *"the current process's delegate session index."*
That index is:

```go
// pkg/tools/delegate.go:298-305
mu     sync.Mutex
tasks  map[string]*DelegateTaskState
nextID int
sessionIndex map[string]string
```

— an unexported field on `DelegateTool`, guarded by `t.mu`, the same mutex every `status`, `inbox`,
`inbox_ack`, `steer`, `respond`, `cancel`, `follow_up` and `peek` call takes
(`delegate.go:1316-1318`, `:2074`). There is **no exported accessor**. The Symbols table lists
`DelegateTool` only as *"modifies … Populates the new `ParentAgentID`"* — the `list_jobs` →
`DelegateTool` dependency is nowhere in the spec.

So FR-011 needs an unspecified new API and an unspecified new wiring edge, and the resulting call
takes the delegate tool's global mutex once per response (or once per row, if written naively).
FR-021 forbids contending with `pe.mu` on exactly this reasoning — *"a read-only visibility tool
must never contend with the dispatch path"* — while permitting, unremarked, contention with a mutex
that is strictly hotter. SC-012 measures added latency on *delegation state transitions*, which is
the right metric, but its only implementing test (`TestListJobs_ConcurrentDuringDispatch`) asserts
"no error, no panic, no race" and measures nothing.

**Fix**: add an FR specifying the accessor — e.g.
`func (t *DelegateTool) ResolvableSessionIDs(ids []string) map[string]bool`, taking `t.mu` **once**
for a batch, never per row — and add the `list_jobs → DelegateTool` edge to Symbols Involved and
the Impact Assessment. Add a scenario asserting the batch property (one lock acquisition for N
rows). Either give SC-012 a real implementing test with a measured baseline, or downgrade it to an
observation and delete the numeric claim.

---

#### MAJ-005 — The three kinds use three incompatible ownership semantics; the agent's own assigned tasks are invisible

**Lens**: Inconsistency / Incorrectness · **Sections**: FR-010, US-1, Ambiguity #4, Explicit Non-Behaviors

FR-010's predicates mean three different things:

| Kind | Predicate | Semantics |
|---|---|---|
| plan | `Plan.OwnerAgentID` | *"the agent woken at plan decision points"* (`plan.go:361-363`) — work **assigned to me** |
| subagent | `LifecycleRecord.ParentAgentID` (new) | work **I dispatched to someone else** |
| task | `Task.CreatedBy` **and** `PlanID == ""` | work **I created for someone else** |

US-1 frames all three as *"the caller's own background work."* Two of them are other agents'
execution that the caller kicked off; one is the caller's own execution. Both readings are
defensible; mixing them in one roster without saying so is not.

The concrete loss: `task.Task.AgentID` is *"the assigned agent"* (`task.go`), and the in-tree
`list_tasks` exposes exactly this split — `role="assignee"` filters `AgentID`, `role="delegator"`
filters `CreatedBy` (verified `pkg/tools/task.go:60-66`). `list_jobs` implements **only** the
delegator half. An agent that had a standalone task assigned to it by a human or another agent —
a live `in_progress` task it is itself executing — sees nothing in `list_jobs`. That is the single
most literal reading of "what am I still working on?", and it is the Evaluation Scenario's own
prompt.

Explicit Non-Behaviors says the tool *"must not return an agent's whole authored task backlog — only
standalone tasks **it created**"*, which locks the delegator reading in without ever considering the
assignee one.

**Fix**: state the ownership axis explicitly in US-1 and FR-010 — one sentence naming, per kind,
whether the row is "work I run" or "work I dispatched". Then decide whether the `task` kind means
`CreatedBy` OR `AgentID` (a union is defensible; silence is not) and add a BDD scenario for a task
where `AgentID == caller` and `CreatedBy != caller`. Ambiguity #4 currently asks whether `list_jobs`
should supersede `list_tasks`; the prior question is whether it covers the same ground at all.

---

#### MAJ-006 — FR-013/FR-025/SC-014 impose the contract-first pipeline on a struct explicitly marked `not-wire-format`

**Lens**: Overcomplexity / Incorrectness · **Sections**: FR-013, FR-025, SC-014, Symbols Involved, Cluster Placement

Symbols Involved: *"`session.LifecycleRecord` … Has a generated wire counterpart
(`contracts/components/schemas/SessionLifecycleRecord.yaml`) → Constraint #8 applies."*

The struct's own header says otherwise:

```
// pkg/session/lifecycle.go:183
// not-wire-format: internal disk record; a caller (pkg/tools/delegate.go)
```

— the same marker `plan.Plan` (`plan.go:353`) and `task.Task` (`task.go:214`) carry. The disk record
and the wire schema are deliberately **different shapes**, and the divergence is documented on the
fields themselves: `ParentDurableKey` — *"Not part of the generated wire shape"* (`:222-223`);
`OriginChannel`/`OriginChatID` — *"Not part of the generated wire shape"* (`:230-232`).

`ParentAgentID` is a purely internal scoping predicate read by one Go tool. It has no SPA consumer —
A6 explicitly puts SPA work and REST parity out of scope. By the in-tree precedent it belongs in
exactly the `ParentDurableKey` category: disk-only, no schema change. FR-013 instead mandates the
full five-step dance, and because `SessionLifecycleRecord.yaml` declares
`additionalProperties: false`, following it **expands the SPA-visible wire contract** for a field
nothing on the wire reads.

FR-025 has the same problem one level up: *"The row and response types MUST be defined in
`contracts/components/schemas/`."* Constraint #8 governs *"every byte crossing the gateway/SPA
boundary (REST req/resp, WS frame, persisted JSON the SPA reads)."* A `ToolResult` string returned to
an LLM is none of the three. SC-014 then gates the release on `make verify-contracts` for a change
that need not exist.

**Fix**: re-write FR-013 to add `ParentAgentID` as a disk-only field with the same doc-comment
convention as `ParentDurableKey`, and say why (no wire consumer, A6). Delete FR-025 or narrow it to
*"if and when the REST parity endpoint of ADR-056 §9 step 4 is built"*. Re-scope SC-014 to assert
`make verify-contracts` stays green — i.e. that **no** drift is introduced — which is the real
requirement.

---

#### MAJ-007 — `started_at` does not exist on the subagent kind and is empty for the entire `queued` group; FR-007's tiebreak and FR-020's determinism are both unimplementable as written

**Lens**: Ambiguity / Infeasibility · **Sections**: FR-003, FR-007, FR-020, SC-010, `TestSortOrder_Deterministic`

FR-003 requires `started_at` and `last_activity_at` on every row. FR-007 sorts by group then
*"tiebroken by `started_at` **descending**."* The sources:

| Kind | `started_at` source | `last_activity_at` source |
|---|---|---|
| plan | `Plan.StartedAt` — RFC3339 **string**, `omitempty` (`plan.go:442`) | `Plan.LastActivityAt` — string, `omitempty` |
| task | `Task.StartedAt` — RFC3339 **string**, `omitempty` | **none** — only `UpdatedAt` |
| subagent | **none** — the record has no such field | `UpdatedAt` — **`time.Time`** |

Three problems, none addressed:

1. **The subagent kind has no start timestamp.** `LifecycleRecord` carries `CreatedAt time.Time` /
   `UpdatedAt time.Time` and nothing else. The mapping is undefined.
2. **Mixed representations.** RFC3339 strings vs `time.Time`. String comparison of RFC3339 is only
   safe if both sides are UTC with identical precision — nothing guarantees that across two
   packages, and FR-020's byte-identity requirement makes the formatting choice load-bearing.
3. **`started_at` is empty for exactly the group that needs the tiebreak.** A `queued` plan
   (`state=approved`/`draft`) has never started, so `StartedAt` is `""`. A `queued` task (`inbox`/
   `next`) likewise. The entire `queued` sub-bound of 25 rows therefore ties on `""`, and Go's
   `sort.Slice` is **not stable**. `TestSortOrder_Deterministic` ("shuffled input, identical output")
   will fail intermittently — the worst kind of test, one that passes in review and flakes in CI.
   SC-010's "byte-identical in 100% of 50 trials" is the same claim and fails the same way.

**Fix**: define the per-kind timestamp mapping in an explicit table in FR-003, including the
subagent fallback (`CreatedAt`) and the task `last_activity_at` fallback (`UpdatedAt`). Require a
single normalized representation (RFC3339 UTC, fixed precision) produced by one shared helper.
Require a **total** order in FR-007 — group, then `started_at` DESC, then a final deterministic
tiebreak on `(kind, id)` — and require `sort.SliceStable` or an explicitly total comparator. Add a
dataset row for "N rows all with empty `started_at`".

---

#### MAJ-008 — `limit` out-of-range: the Behavioral Contract says error, the Edge Cases and the BDD table say clamp

**Lens**: Inconsistency · **Sections**: Behavioral Contract (error flows), Edge Cases, BDD "Argument validation", FR-002

- Behavioral Contract: *"When an argument is invalid (unknown `kind`, unknown `status`, non-integer
  or **out-of-range** `limit`), the system returns a validation error and zero rows."*
- Edge Cases: *"**`limit` above the hard maximum.** Expected: clamped to the maximum, and the clamp
  reported."*
- BDD "Argument validation": `limit` = above hard max → *"clamped, clamp reported"*.

`limit=250` against a hard max of 200 is simultaneously required to error with zero rows and to
succeed with a clamp notice. `TestArgs_Validation` traces to the table that says clamp, so the
implementation will silently follow the table and the Behavioral Contract becomes decoration.

The interaction with FR-016 is separately undefined and makes the clamp misleading. Ambiguity #2
proposes sub-bounds of 25 per live group (75 live maximum) and a hard `limit` max of 200. A caller
passing `limit=200` can therefore never receive more than 75 rows, and "clamped to the maximum" is
never reported because 200 ≤ 200 — the caller is told nothing was clamped while receiving 37 % of
what it asked for. Is `limit` a total cap, a per-kind cap, or a per-status-group cap? The spec never
says.

**Fix**: pick one disposition for out-of-range `limit` and make all three sections agree (clamping
with an explicit `limit_clamped_to` field is the better choice — it is the one behaviour that never
costs the caller a turn). Then define `limit`'s relationship to FR-016's sub-bounds in one sentence:
either `limit` is a total cap applied **after** sub-bounds (in which case the hard max must be ≤ the
sum of sub-bounds, or it is unreachable), or it scales the sub-bounds proportionally. Add a dataset
row for `limit` between the sub-bound sum and the hard max.

---

#### MAJ-009 — Workspace scoping fails **open**, and `ToolWorkspaceID` is conditionally injected

**Lens**: Insecurity · **Sections**: FR-008, FR-009, US-3 AS-4, SC-004, `TestListJobs_WorkspaceScoped`

US-3 elevates scoping to a P0 security control and argues it correctly for the agent id: *"Every
store the tool reads treats the empty string as filter disabled … so an unresolvable calling
principal would return the entire installation."* FR-008 then fails closed on an empty agent id.

The identical argument applies to the workspace and is not made. FR-009 says only *"MUST scope to
the calling context's workspace via `ToolWorkspaceID(ctx)`"* — with no non-empty requirement. And
the value is conditionally set:

```go
// pkg/agent/loop.go:6381-6383
if ts.opts.WorkspaceID != "" {
    turnCtx = tools.WithWorkspaceID(turnCtx, ts.opts.WorkspaceID)
}
```

`ToolWorkspaceID(ctx)` returns `""` for any turn whose channel binding carries no workspace
(`pkg/tools/base.go:230-233` — a plain type-assert with no error). Every store treats `""` as
"filter off", so on those turns the workspace boundary silently disappears and FR-003's required
`workspace_id` is `""` on every row.

The disclosure is bounded to the caller's own work across workspaces, which is why this is MAJOR and
not CRITICAL. But it is precisely the "green in CI, dead in the field" shape the spec calls out
elsewhere: `TestListJobs_WorkspaceScoped` constructs a context with an explicit workspace, so it
passes while the production path it is meant to protect is unexercised. SC-004 covers only the agent
id.

**Fix**: extend FR-008 to the workspace, or state the deliberate exception. If workspace-less turns
are legitimate (they appear to be — the injection is guarded for a reason), require FR-009 to name
the behaviour explicitly: *"when `ToolWorkspaceID(ctx)` is empty, the roster spans every workspace
for the resolved principal, and the response MUST carry `workspace_scoped: false`"* — so the caller
knows which answer it got. Add a BDD scenario and a dataset row for an empty workspace id, and
extend SC-004 to cover it.

---

#### MAJ-010 — A never-approved draft plan is `queued` forever and permanently occupies the `queued` sub-bound

**Lens**: Incorrectness · **Sections**: BDD "Normalized status" (`state=draft` → `queued`), Dataset "Plan native state" row 1, FR-016, Ambiguity #6, A5

The status table maps `state=draft` → `queued`. Ambiguity #6 notes a draft is *"authored-but-
unapproved, arguably not 'background work' at all"* and defers. The deferral has a consequence the
spec does not draw.

Drafts are terminal-in-practice-but-not-in-state: a plan an agent drafted and abandoned stays
`draft` indefinitely. A5 records that plans are **never swept** (`plan.Store.Delete` exists; nothing
calls it). So abandoned drafts accumulate monotonically, all normalize to `queued`, and all compete
for the same 25-row `queued` sub-bound (Ambiguity #2). Twenty-six abandoned drafts starve every real
cap-waiting plan out of the roster — with an honest `omitted` count, but the row the caller needs is
gone.

This is the same starvation FR-016's per-group sub-bounds were introduced to prevent (the spec
correctly identified it for `blocked` vs `running` and reordered the sort for it), reappearing
*inside* a group.

**Fix**: exclude `state=draft` from the roster by default (a draft is not running and consumes no
budget — US-1's stated trigger is *"still running and still consuming budget"*), or gate it behind an
`include_drafts` argument. If drafts stay, add a sub-sort within `queued` that ranks `approved`
above `draft` so drafts are the first thing truncated, and add a dataset row for
"26 abandoned drafts + 1 approved plan → the approved plan is present."

---

#### MAJ-011 — `cancelled` collapses to `failed`, so the agent cannot tell work it stopped from work that died

**Lens**: Incorrectness · **Sections**: FR-006, BDD "Normalized status" (subagent `cancelled` → `failed`; plan `stopped_by_user` → `failed`), US-2

US-2's thesis is that the agent must distinguish *"waiting for a slot from working from stuck and
needing intervention — otherwise it either interrupts healthy work or waits forever on dead work."*
The five-value vocabulary (FR-006) has no slot for "deliberately stopped", so:

- subagent `state=cancelled` → `failed`
- subagent `state=timed_out` → `failed`
- plan `failed_reason=stopped_by_user` → `failed`
- plan `failed_reason=judge_rounds_exhausted` → `failed`

A user-initiated stop and a crash are the same normalized value. The agent's only recourse is
`native_status` — the field CRIT-002 shows is unbounded and unredacted, and which FR-006 explicitly
subordinates (*"the normalized `status` vocabulary MUST be exactly …"*). US-2 AS-4 asserts only that
the two *plan* failure reasons produce different `native_status` values; nothing requires the agent
to be able to act on the difference.

The practical failure: an agent that stopped a delegation on purpose, then lost context, calls
`list_jobs`, sees `failed`, and re-dispatches work the user deliberately cancelled.

**Fix**: either add `cancelled` as a sixth normalized value (it is a distinct disposition, not a
shade of failure) and place it in the sort order, or require an explicit boolean
`intentionally_stopped` on the row derived from the closed portion of each kind's reason field
(`plan.FailedReasonStoppedByUser`, `session.LifecycleCancelled`), so the distinction survives
without depending on free text. Add a scenario asserting the agent-visible difference.

---

#### MAJ-012 — FR-012 and FR-024's tool-description clauses are traced to tests that cannot verify them

**Lens**: Inconsistency (traceability) · **Sections**: FR-012, FR-024, US-6 AS-3, Traceability Matrix

FR-012: *"The tool description MUST state plainly that a row with `actionable=false` is
informational only…"* Matrix maps it to the BDD scenario *"A post-restart subagent row is an honest
tombstone"* and the test `TestListJobs_PostRestartTombstone`, which asserts `status`,
`native_status` and `actionable` on a row. It asserts nothing about the description string. FR-012
therefore has **zero** coverage.

Same defect on FR-024's trailing clause (*"this MUST be stated in the tool description"*), traced to
`TestListJobs_ReadOnly` (directory byte-identity) and `TestListJobs_ConcurrentDuringDispatch`.

US-6 AS-3 — *"When the caller reads the tool description, Then the description states plainly…"* —
is the only acceptance scenario in the spec with **no** corresponding BDD scenario. The structural
check "every acceptance scenario has at least one BDD scenario" fails here.

**Fix**: add a BDD scenario (*"Given the registered `list_jobs` tool, When its description is read,
Then it contains a statement that a non-actionable id is informational only, and a statement that
the roster is a best-effort near-snapshot"*) and a unit test `TestListJobs_DescriptionContract`
asserting on `Description()`. Re-point the matrix rows for FR-012 and FR-024.

---

#### MAJ-013 — No audit entry, no metrics, no runbook, no operator kill switch

**Lens**: Inoperability · **Sections**: whole spec

The spec has no operability section. Concretely missing:

- **Audit.** The tool enumerates human-readable labels and steerable handles and is a security
  boundary (US-3, P0). The project has an audit subsystem (`pkg/audit`, Constraint list). No FR
  requires an audit entry for a `list_jobs` call, so a cross-agent scoping bug leaves no forensic
  trail — the "Cross-agent probing under adversarial prompting" evaluation scenario has nothing to
  read after the fact.
- **Metrics.** `unreadable`, `unattributable_subagents`, `total_omitted` and per-kind error entries
  are reported to the *caller* only. An operator has no way to learn that 40 % of an install's
  lifecycle records are corrupt or that every roster is truncating.
- **Kill switch.** FR-023 mandates `allow` at three sites. Dataset row 5 shows a global
  `list_jobs: deny` works (deny-wins over the fresh-install per-agent `allow`), but that is stated as
  a test case, not as the supported operator control, and it is not in the tool's documentation.
- **Runbook.** Nothing tells on-call what `unattributable_subagents: 4000` means or what to do about
  it (Ambiguity #8 raises the noise question and defers the meaning).

**Fix**: add an Operability section with an FR for a structured audit/log entry per call
(principal, workspace, kinds read, row count, omitted, unreadable, unattributable) at Debug, plus
Warn on any non-zero `unreadable` or per-kind error; name the global-`deny` kill switch in FR-023
and in the tool description; and add a short runbook paragraph for the two counters an operator will
actually see.

---

#### MAJ-014 — No caching, no rate limit, and no cost bound on a read whose own premise invites per-turn calling

**Lens**: Insecurity (DoS) / Incompleteness · **Sections**: Ambiguity #5, A5, SC-012, FR-024

Per call the tool performs: a full plan-directory scan loading every plan file; a full task-directory
scan loading every task file; and a full lifecycle scan taking **one per-session striped mutex per
session** (`lifecycle.go:342-348` — the same lock live delegations need for their own state
transitions). A5 records that neither plans nor lifecycle records are ever swept, so all three grow
monotonically for the life of the install.

US-1's trigger — *"its context window was trimmed, or a wake started a fresh turn"* — is a
per-turn condition. FR-012's description will tell agents the tool is the way to recover a handle.
Nothing bounds call frequency.

Ambiguity #5 names this and defers it to *"rely on SC-012 to catch regressions"* — but SC-012 has no
implementing test (MAJ-004), and even if it did, a 3-second 8-goroutine probe on a fresh temp-dir
store measures nothing about a two-year-old install with 50 000 lifecycle files.

**Fix**: the 2–5 s per-principal memo Ambiguity #5 already sketches is cheap and sufficient; make it
an FR rather than an open question, with an explicit statement that a memoized roster is compatible
with FR-024's best-effort-near-snapshot contract (it is — it weakens freshness, which FR-024 already
disclaims). Add a hard per-call work bound (max records scanned per kind, with the overflow reported
through the existing omission counters) so cost is bounded by configuration rather than by store
size.

---

#### MAJ-015 — The response carries eight diagnostic counter families, all emitted unconditionally, on a tool whose stated purpose is protecting the caller's context

**Lens**: Overcomplexity · **Sections**: FR-003, FR-017, FR-018, FR-015, FR-021, US-1 AS-4, US-4, Ambiguity #8

The response accumulates: `total_omitted`; per-kind `omitted`; per-kind `unreadable`; per-kind error
entries; `unattributable_subagents`; `cap_active`; `cap_max`; plus the clamp report (MAJ-008) and
`terminal_suppressed` if CRIT-003 is fixed. US-1 AS-4 requires the *empty* roster to be
*"well-formed"* with `total_omitted=0` — so an agent with no background work receives a payload of
a dozen zeroes.

US-4's stated cost model is *"A firehose is worse than no tool: it burns the caller's context."*
Every one of these fields is charged to that same budget on every call. Ambiguity #8 already worries
that one of them (`unattributable_subagents`) is *"permanently noisy"* — the observation
generalizes.

**Fix**: require omit-when-zero for every diagnostic counter (a single `notes` object present only
when something is non-nominal), and keep exactly one always-present field so the caller can tell
"nothing to report" from "field missing". Resolve Ambiguity #8 the same way for all eight rather
than for one. This is a pure win: it costs nothing in honesty (a zero and an absent field carry the
same information when the convention is documented) and removes the fixed per-call context tax.

---

#### MAJ-016 — SC-012 and SC-005 assert measured numbers that no test in the TDD plan measures

**Lens**: Infeasibility · **Sections**: SC-005, SC-012, Test Implementation Order

- **SC-012**: *"Median added latency of delegation state transitions while 8 goroutines poll
  `list_jobs` for 3 seconds is within **2×** the un-polled baseline."* The only candidate test,
  `TestListJobs_ConcurrentDuringDispatch` (order 39), is described as *"8 goroutines, `-race`"* and
  its BDD scenario asserts only "no unexpected error / no panic / race detector reports nothing."
  There is no baseline capture, no latency instrumentation on delegation state transitions, and no
  assertion. A 2× median-latency budget also needs a stated sample size and a stated flakiness
  disposition on shared CI runners — neither exists.
- **SC-005**: *"the serialized response is ≤ 32 KB"* — no test in the ordered list serializes a
  response and measures its size. `TestBounds_OmissionCountExact` counts; it does not weigh. And per
  CRIT-002 the bound is not derivable from the spec's own field constraints.

Both are unfalsifiable as written, which makes them decoration on the release gate.

**Fix**: either add the two missing tests to the Test Implementation Order with named harnesses
(`TestListJobs_ResponseSizeBound` asserting on `len(result.Content)`; a benchmark-style
`TestListJobs_DelegationLatencyUnderPolling` with an explicit baseline and a stated tolerance for
CI variance), or downgrade both to observations and remove the numeric claims from the success
criteria.

---

### MINOR

---

#### MIN-001 — `FilterSensitiveData`'s threshold is **bytes**, the spec reasons in **runes**

**Lens**: Incorrectness · **Sections**: FR-019a, Dataset "Bounds and truncation" row 13, SC-011

```go
// pkg/config/config.go:398-401
if len(content) < c.Tools.GetFilterMinLength() {   // len() on a string = BYTES
    return content
}
```

FR-019a says *"when the content is shorter than `FilterMinLength` (**default 8**)"* and dataset row
13 specifies *"6-rune label that **is** a registered secret"*. A 6-rune CJK or emoji label is 18–24
bytes and **is** filtered; a 7-byte ASCII secret is bypassed. The test as written may pass or fail
purely on the alphabet the author picks. SC-011's *"at every label length from 1 rune to
`labelMax+50`"* has the same conflation.

**Fix**: restate FR-019a in bytes (matching the code), and specify dataset row 13 as a 7-**byte**
ASCII secret — the actual bypass case. Keep runes for the truncation requirement (FR-019), where
runes are correct, and note the deliberate unit change so the next reader does not "fix" it back.

---

#### MIN-002 — The spec's own traceability completeness claim is false for 6 of 31 scenarios

**Lens**: Inconsistency · **Sections**: Traceability Matrix completeness check

*"Every one of the 31 scenarios carries a `Traces to` back-reference to a User Story and Acceptance
Scenario (31 scenarios / 31 `Traces to` lines) — verified mechanically."*

Counting confirms 31 scenarios and 31 `Traces to` lines. But six trace to something that is not a
User Story + Acceptance Scenario:

| Scenario | Traces to |
|---|---|
| Self-delegation yields exactly one row | Edge Case "Self-delegation" |
| Unicode label truncates on a rune boundary | Edge Case "Unicode label at exactly the limit" |
| Legacy lifecycle records are counted, never guessed at | Edge Case "Unattributable legacy lifecycle records" |
| Argument validation | Behavioral Contract, error flows |
| The tool never mutates state | Explicit Non-Behaviors |
| Concurrent calls during active dispatch never error | Edge Case "Concurrent mutation during the read" |

The mechanical check counted lines, not their targets. In a spec that makes evidence discipline its
headline, a verification claim that does not verify what it says it verifies is worth correcting.

**Fix**: either add User Story + Acceptance Scenario anchors for the six (each is coverable — e.g.
self-delegation and nested delegation both belong under US-3 AS-3), or re-word the completeness
claim to *"a `Traces to` back-reference to a User Story, Edge Case, Behavioral Contract clause or
Explicit Non-Behavior"* and say how many of each.

---

#### MIN-003 — Multi-generation lifecycle records are never addressed

**Lens**: Incompleteness · **Sections**: FR-003, FR-013, FR-015, Integration Boundaries (`pkg/session`)

`LifecycleRecord` carries `Generation int` and `ResumedFrom string`, and the schema's own description
states the immutable-terminal invariant: *"a terminal record … is never mutated in place —
`follow_up`/Play mint a NEW record with a new `generation`, linked back via `resumed_from`."*
`Persist` enforces it (`lifecycle.go:437-442`).

The spec never mentions generations. Open questions an implementer will hit on day one: does a
resumed session share the parent's `session_id` file (in which case `tail` returns gen-N and
`ParentAgentID` must be carried forward by whatever mints gen-N — a site FR-013 does not name), or a
new one (in which case the caller sees two rows for one conceptual job)? Does the row expose
`generation`/`resumed_from`? Today `delegate.go:942` is the **only** `LifecycleRecord` construction
site in non-test code, so the question is currently latent — but ADR-053's `follow_up`/Play path is
the thing that will change that.

**Fix**: add an edge case and an FR clause: *"a row represents the newest generation of a session;
`ParentAgentID` MUST be carried forward on every generation mint; the row MUST carry `generation`
when > 0."* Add a regression test to the existing table alongside
`TestLifecycleRecord_ParentAgentIDRoundTrip`.

---

#### MIN-004 — `(kind, id)` is the row identity, but FR-004 hands `id` back as a standalone handle

**Lens**: Ambiguity · **Sections**: FR-004, Edge Cases ("Duplicate ids across kinds")

The edge case establishes that *"`(kind, id)` is the identity; a plan id and a task id may collide
without ambiguity."* FR-004 then defines `id` as *"the handle that kind's other tools accept."* An
agent quoting an id to a user, or storing one across a context trim, loses the kind and reintroduces
the ambiguity the edge case waved away. The tool description says nothing about this.

**Fix**: require the description to state that a handle is only meaningful paired with its kind, or
prefix the id (`plan:abc`, `task:abc`) and require the action tools to accept the prefixed form —
the second is cleaner but touches three other tools, so state the choice explicitly rather than
leaving it to the implementer.

---

#### MIN-005 — FR-001's registration path is under-specified relative to the catalog-sync test

**Lens**: Ambiguity · **Sections**: FR-001, FR-023(a), Symbols Involved (`gateway.buildKnownBuiltinToolNames`)

FR-001 says *"register a read-only builtin tool named `list_jobs` in `pkg/tools`, scoped
`ScopeGeneral`."* The Symbols table says *"Derives from live metadata, so registering the tool is
enough."* That is true only if the tool is added to `tools.GeneralBuiltinMetadata()` specifically —
`buildKnownBuiltinToolNames` (`gateway.go:715-745`) iterates `GeneralBuiltinMetadata()`,
`browser.BrowserBuiltinMetadata()` and `systools.AllTools(nil, nil)`, plus a hardcoded union for the
four ADR-052 names. A tool registered elsewhere silently misses the catalog and fails
`TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog`.

**Fix**: name `tools.GeneralBuiltinMetadata()` explicitly in FR-001 as a fourth required site
alongside FR-023's (a)/(b)/(c).

---

#### MIN-006 — Dataset "Calling principal" row 5 conflicts with FR-008's stated rationale

**Lens**: Inconsistency · **Sections**: Dataset "Calling principal" rows 2/5, FR-008

Row 2: `""` → error, 0 rows. Row 5: `"nonexistent-agent"` → *"empty roster, success — Unknown ≠
unresolvable."* The distinction is right, but "unresolvable" is never defined. FR-008's trigger is
purely lexical (*"absent, empty, or whitespace-only after trimming"*), so a syntactically valid but
non-existent agent id succeeds — which is correct and matches row 5 — while the Behavioral Contract
says *"When the calling agent id is empty, whitespace-only, **or unresolvable**"*, implying a
registry lookup the FR does not require.

**Fix**: delete "or unresolvable" from the Behavioral Contract, or define it as exactly the lexical
test. Do not introduce a registry lookup — row 5 is right that unknown-but-well-formed should
succeed with an empty roster.

---

#### MIN-007 — FR-005's subagent label is the target agent's *name*, but the source record stores an *id*

**Lens**: Incompleteness · **Sections**: FR-005, Integration Boundaries (`pkg/session`)

FR-005: *"subagent → the target agent's name."* The lifecycle record carries `AgentID string` — an
id, not a display name (Integration Boundaries itself says *"`AgentID` (the child, used for the
label)"*). Resolving an id to a name needs the agent registry, an unlisted dependency, and needs a
stated behaviour when the agent has since been deleted or renamed (a plausible case for a durable
record). The BDD scenario asserts *"that row's `label` equals `B`'s agent name."*

**Fix**: name the registry dependency in Symbols Involved and specify the fallback: *"when the
target agent id no longer resolves, `label` MUST be the raw agent id"* — and add a dataset row for
a deleted target agent.

---

### OBSERVATION

- **OBS-001** — Ambiguity #7 (ADR-055 still Proposed) is dispatched with *"FR-010's plan predicate
  now uses `OwnerAgentID` rather than ADR-055 D2's `owner`, which weakens but does not remove the
  dependency."* Worth naming what remains: `plan.PlanPhase`'s `awaiting_owner_correction` and
  `stalled` values — which carry US-2's entire `blocked` semantics — are ADR-055 constructs, and
  `Plan.OwnerSessionID` is documented as *"Populated by the Phase-2 owner loop … empty until then."*
  If Phase 2 changes phase semantics, the status table changes with it. A one-line note in A1 would
  cost nothing.

- **OBS-002** — FR-020 (byte-identical responses across calls) and FR-024 (best-effort near-snapshot,
  concurrent mutation tolerated) are in tension by construction: the first is only testable against
  a frozen store, which is not the state the second describes. FR-020 buys the caller nothing — an
  agent does not diff two rosters. Consider replacing it with the property that actually matters and
  is separately testable: a **total, deterministic ordering function** given a fixed input set
  (which MAJ-007 requires anyway), and drop the response-level byte-identity claim and SC-010 with it.

- **OBS-003** — A7 leaves `list_tasks`' two defects in place (unbounded `json.Marshal(tasks)` at
  `pkg/tools/task.go:74-78`, and `agentID := ToolAgentID(ctx)` fed to a filter with no non-empty
  check at `:60`) and correctly notes Constraint #7 requires them tracked. Both are verified present.
  The second is the same fail-open class this spec spends US-3 closing for `list_jobs`, in a tool
  every agent already has `allow` for (`defaults.go` global seed). Worth filing before this spec
  merges rather than after, so the fix is not orphaned when this feature closes its issues.

---

## 3. Structural Integrity Results (`plan-spec` mode)

| Check | Result | Notes |
|---|---|---|
| Every user story has ≥1 acceptance scenario | **PASS** | 7 stories, 27 acceptance scenarios |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** | US-6 AS-3 (tool-description content) has none — MAJ-012 |
| Every BDD scenario has a `Traces to` back-reference | **PASS (qualified)** | 31/31 present; 6 point at Edge Cases / Behavioral Contract / Explicit Non-Behaviors rather than a User Story — MIN-002 |
| Every BDD scenario has a corresponding test in the TDD plan | **PASS** | 39 ordered tests cover all 31 |
| Every FR appears in the traceability matrix | **PASS** | 26/26 (FR-001…025 + FR-019a) |
| Every BDD scenario appears in the matrix | **PASS** | verified by title match |
| Test datasets cover boundaries, edge cases, error scenarios | **PARTIAL** | 5 datasets, well-constructed; missing: corrupt plan/task file (MAJ-001), empty workspace id (MAJ-009), empty `started_at` ties (MAJ-007), `limit` between sub-bound sum and hard max (MAJ-008), deleted target agent (MIN-007), secret in `native_status` (CRIT-002) |
| Regression impact explicitly addressed | **PASS** | 9-row table, genuinely thorough; add `task.Filter` (MAJ-002) |
| Success criteria measurable, no subjective language | **PARTIAL** | 15 SCs, all quantified; SC-005 and SC-012 have no implementing test (MAJ-016); SC-011's unit is wrong (MIN-001) |

**Verified-correct and worth noting**: the spec's nine ADR corrections (C1–C9) all hold against the
working tree, including the two most consequential — `denyAllThenOverride` stamping an explicit
per-agent `deny` for every catalog name (`core.go:384-394`) which beats a global `allow`
(`compositor.go:196-198`), and `ValidateToolPolicyCoverage` short-circuiting on a global entry
(`validate.go:458-460`) so `RepairIncompleteToolPolicyCoverage` never fires. The System-Agent
`deny`-on-upgrade concern that FR-023 leaves implicit is also genuinely covered: `seedSystemAgents`
re-enforces the exact seeded policy map on **every** boot (`core.go:1436-1441`), so a persisted
pre-`list_jobs` System Agent map is repaired rather than falling through to the global `allow`.
FR-023's three sites are correct and complete.

---

## 4. Test Coverage Assessment

The TDD plan is the strongest part of the spec — 39 tests, correctly levelled, ordered by
dependency, and each traced to a scenario. Gaps, in priority order:

1. **Two success criteria have no implementing test** (MAJ-016): SC-005 (32 KB response) and SC-012
   (2× delegation latency). Both are release-gate numbers with no measurement.
2. **FR-012 and FR-024's description clauses are untestable by their assigned tests** (MAJ-012). Add
   `TestListJobs_DescriptionContract`.
3. **No negative test for the plan or task per-record failure path** (MAJ-001) — `unreadable` is
   only exercised for the lifecycle store, the one kind where it is achievable.
4. **`TestSortOrder_Deterministic` will flake** (MAJ-007) — every `queued` row ties on an empty
   `started_at` and `sort.Slice` is unstable.
5. **No concurrency test for the delegate-index read** (MAJ-004) — `TestListJobs_ConcurrentDuringDispatch`
   exercises store contention, not the `DelegateTool.mu` contention `actionable` introduces.
6. **No idempotency/repeat-call test under mutation** — FR-024 says concurrent mutation is tolerated;
   nothing asserts that two calls straddling a mutation both return well-formed (not merely
   non-erroring) rosters.
7. **`TestListJobs_CapPressureWithoutAdmit` asserts the wrong property** (CRIT-001) — it verifies
   `Admit` was not called, which will pass while the emitted numbers are wrong. It needs a companion
   asserting the *value* against a known global active count including a foreign-owner plan and a
   registered `activeCounter`.

---

## 5. STRIDE Threat Summary

| Component / flow | S | T | R | I | D | E | Findings |
|---|---|---|---|---|---|---|---|
| Calling-principal resolution (`ToolAgentID`) | — | — | — | ✔ closed by FR-008 | — | ✔ closed by FR-008 | Fail-closed on empty/whitespace is correct and well-tested |
| Workspace resolution (`ToolWorkspaceID`) | — | — | — | **⚠ fails open** | — | — | MAJ-009 — no non-empty guard; conditionally injected at `loop.go:6381` |
| Row `label` | — | — | — | ✔ closed by FR-019/019a | — | — | Redact-then-truncate order is correct; unit is bytes not runes (MIN-001) |
| Row `native_status` | — | — | — | **⚠ open** | **⚠ unbounded** | — | CRIT-002 — required field, two unvalidated string sources, no redaction, no length cap |
| Cross-kind store reads | — | ✔ read-only (FR-024, `TestListJobs_ReadOnly`) | **⚠ no audit** | — | **⚠ unbounded cost** | — | MAJ-013 (no audit trail), MAJ-014 (no rate limit/cache; N striped mutexes/call) |
| Delegate session index (`actionable`) | — | — | — | — | **⚠ lock contention** | — | MAJ-004 — takes the mutex every delegate action needs |
| Tool-policy grant (FR-023) | — | ✔ boot re-enforced for System Agents | — | — | — | ✔ three sites correct | Verified sound; kill switch undocumented (MAJ-013) |

---

## 6. Unasked Questions

1. **What is `cap_active` actually counting?** The spec never states whether it is the caller's
   plans, the workspace's plans, or the installation's — and the three give different answers to the
   question the field exists to answer. (CRIT-001)
2. **After a restart, how does the agent learn its work is gone?** The default call cannot tell it.
   (CRIT-003)
3. **Is `native_status` a contract or a debug string?** FR-003 makes it required and Ambiguity #3
   notes *"agents will quote this string back to users verbatim"* — but its inputs are unvalidated
   free text. It cannot be both.
4. **Which of "work I run" and "work I dispatched" is a job?** The three kinds answer differently.
   (MAJ-005)
5. **Why does `ParentAgentID` need to cross the wire?** No SPA consumer exists (A6), and the two
   nearest fields on the same struct are documented as deliberately disk-only. (MAJ-006)
6. **What does an operator do about `unattributable_subagents: 4000`?** Ambiguity #8 asks whether to
   *display* it; nobody asks what it means or how it is ever cleared, given A5 says lifecycle records
   are never swept. This number is permanent and monotonically increasing on every upgraded install.
7. **Does `limit` mean anything, given FR-016?** A hard max of 200 against a live maximum of 75 makes
   the argument unreachable in its upper 62 %. (MAJ-008)
8. **What happens when `list_jobs` is called from a path that is not an agent turn?** A2 names four
   verified paths and offers FR-008 as the backstop for future ones — but the REST parity endpoint
   ADR-056 §9 step 4 contemplates would have a *user* principal, not an agent one, and FR-008 would
   reject it. Is that the intended outcome, or does the REST path need a different predicate?
9. **How is a plan-owner session distinguished from a delegated subagent?** `LifecycleRecord.OwnsPlanID`
   marks the former. A plan-owner session with `ParentAgentID` set would appear as *both* a `plan`
   row and a `subagent` row for the same work. The spec's self-delegation edge case handles a
   narrower version of this; `OwnsPlanID` is never mentioned.
10. **Should the roster be readable at all when the agent has `delegate: deny`?** FR-023 grants
    `list_jobs` uniformly, but an agent denied `delegate` receives `subagent` rows with handles it
    cannot use — `actionable=true` and still unusable, which is the exact failure FR-011 exists to
    prevent, arriving through the policy layer instead of the restart layer.

---

## 7. Addendum — Operator ruling: **no data migration, greenfield**

> Operator ruling postdating the spec, verbatim: *"old sessions on disc do not matter, consider
> green field"* / *"not any data migration, not our problem."* Unconditional, every store. The spec
> was written before this and budgets real machinery for pre-upgrade data. This section states
> exactly what dies, what survives, and — the part that matters — **what must be replaced rather
> than deleted.**

### 7.1 What the ruling deletes outright

| Spec item | Disposition |
|---|---|
| **FR-015** (exclude + count + report legacy records; MUST NOT infer from `ParentDurableKey`/`OwnerScopeID`/`AgentID`) | **Delete the legacy framing.** See 7.3 — the *anti-inference* rule must be re-homed, not lost. |
| `unattributable_subagents` **response field** | **Delete.** Removes a required field from FR-003, shrinking the response contract. |
| BDD scenario *"Legacy lifecycle records are counted, never guessed at"* (asserts `= 2`) | **Delete** (spec:719). |
| TDD test **#28** `TestListJobs_LegacyRecordsCountedNotGuessed` | **Delete** (spec:868). |
| Regression-risk row `TestLifecycleRecord_LegacyRecordWithoutParentAgentIDStillLoads` | **Delete** (spec:978) — it exists only to prove a pre-FR-013 record still loads. |
| **Ambiguity #8** (*"could be large and permanent"*, permanently-noisy field) | **Resolved by deletion** (spec:1164). |
| **Unasked Question 6** in this review (*what does `unattributable_subagents: 4000` mean*) | **Withdrawn** — it was downstream of FR-015. |
| Edge case *"Unattributable legacy lifecycle records"* (spec:293) | **Delete.** |
| C7's *"no bespoke migration needed"* argument (spec:35) | **Moot but harmless.** The JSON round-trip finding is still *true* and still load-bearing for FR-023(b) reaching an existing dev config; it just no longer needs to be argued as a migration story. Keep the finding, drop the migration framing. |

### 7.2 Does cutting this retire the "prospective only" caveat?

**Yes — but only because of the ruling, and the spec must say so explicitly rather than silently
dropping the sentence.**

The caveat at spec:1041 (*"Subagent handle recovery is prospective only"*) and the matching
limitation at spec:293 exist **solely** because records minted before `ParentAgentID` shipped could
never be attributed. With no pre-upgrade data by fiat, every record in existence is minted by the
FR-013 code path, so **subagent recovery simply works** and the caveat is retired. This also
strengthens the answer to the spec's own open question at spec:1235 — the `subagent` kind survives
D7 *with a schema change alone*, no stated limitation attached.

**However** — this is the trap — the caveat is retired **only if `ParentAgentID` is guaranteed
non-empty at mint**. It is not, today. See 7.3.

### 7.3 What must be REPLACED, not deleted — the mint-time gap ⚠️

**This is the finding that changes what gets built.** Deleting FR-015 wholesale is wrong, because
`ParentAgentID == ""` is reachable on a **brand-new, greenfield install**:

1. **FR-013 states no non-empty requirement.** Spec:1037 says only *"populated at mint time from
   `ToolAgentID(ctx)`"*. There is no fail-closed clause — unlike FR-008, which guards only the
   **read** side.
2. **The mint site already treats an empty agent id as reachable.** In the very block FR-013
   modifies, the target agent id is guarded before use:
   ```go
   // pkg/tools/delegate.go:926
   if agentID != "" && t.getAgentRegistry != nil {
   ```
   `[VERIFIED: pkg/tools/delegate.go:915-959]` — the code's own author considered an empty agent id
   possible. FR-013 proposes to stamp the *parent* id from context with no equivalent guard.
3. **The proposed `omitempty` tag makes the failure invisible.** FR-013 specifies
   `` `json:"parent_agent_id,omitempty"` `` (spec:1037). An empty parent serializes to an **absent
   key** — byte-identical to the "legacy record" shape FR-015 was written to catch. Under the old
   spec that row was at least *counted*. Delete FR-015 and keep `omitempty`, and the row is
   **silently dropped from the roster with no counter anywhere** — strictly worse than the
   pre-greenfield design, and precisely the "green in CI, dead in the field" shape this review flags
   elsewhere (MAJ-009).

**Required replacement.** Rewrite FR-015 rather than deleting it:

> **FR-015 (rewritten)**: `delegate`'s lifecycle mint MUST fail closed when
> `strings.TrimSpace(ToolAgentID(ctx))` is empty — returning an error and minting **no** record —
> so no unattributable lifecycle record can be created. `LifecycleRecord.ParentAgentID` MUST be
> declared **without `omitempty`**, so the field is always present on disk and an empty value is a
> visible bug rather than an absent key. `list_jobs` MUST NOT infer a parent from
> `ParentDurableKey`, `OwnerScopeID` or `AgentID`.

Rationale for each half: the fail-closed clause makes the "prospective only" caveat genuinely
retired (7.2) instead of merely unstated; dropping `omitempty` is what makes the invariant
*auditable*; and the anti-inference sentence is the one part of the original FR-015 that was never
about legacy data at all — it encodes **C1**, the verified fact that `ParentDurableKey` is *shared*
between parent and children (`[VERIFIED: pkg/tools/delegate.go:924 + pkg/agent/subturn.go:970]`).
Lose that sentence and a future implementer re-introduces the sibling/cousin leak the spec's
strongest correction exists to prevent.

Correspondingly, **test #28 should be replaced, not merely deleted**, by
`TestDelegateMint_FailsClosedOnEmptyParentAgentID`, and the regression row at spec:979
(`TestLifecycleFilter_UnsetParentAgentIDUnchangedBehaviour`) **survives** — the boot sweep's own
queries leave `ParentAgentID` unset and must stay unfiltered.

### 7.4 Does anything else quietly depend on the deleted machinery?

Audited; three couplings, one of them real:

- **FR-003 / SC-005 (32 KB response bound)** — `unattributable_subagents` is a required response
  field. Removing it changes the response shape and the byte budget. **Harmless, but the contract
  schema and SC-005's arithmetic must be updated in the same commit** (Constraint #8: schema →
  refs → `gen-contracts.sh` → atomic commit).
- **CRIT-003 (empty roster after restart)** — **unaffected, and now more exposed.** Its cause is the
  ADR-053 boot sweep marking rows terminal plus `include_terminal=false`, not legacy data. With
  `unattributable_subagents` gone, the response loses the *last* non-zero field that could have hinted
  something was omitted. CRIT-003 must still be fixed on its own terms.
- **A5 (*"lifecycle records are never swept"*)** — survives the ruling and is now the **only**
  remaining unbounded-growth statement in the spec. Greenfield resets the store once; it does not
  bound its growth thereafter.

**Net effect on the finding count:** the ruling retires 0 CRITICAL, 0 MAJOR and 0 MINOR findings from
§2 — every finding in this review is orthogonal to legacy data. It removes review Unasked-Question 6,
resolves spec Ambiguity #8, and **adds one new MAJOR (7.3, the mint-time gap)**.

---

## 8. Addendum — Independent audit of the four load-bearing evidence claims

The parent ADR failed three grill rounds on evidence quality. Each claim below was re-read in the
working tree **independently of the spec's own citation**.

### 8.1 `ToolAgentID(ctx)` is populated in production — ✅ **VERIFIED, spec is correct**

This is the claim the entire `subagent` kind rests on. It holds, and is stronger than the spec claims:

```go
// pkg/agent/loop.go:6356 — unconditional, no guard
turnCtx = tools.WithAgentID(turnCtx, ts.agent.ID)
```

Exhaustive sweep of non-test injection sites (`grep -rn "WithAgentID(" --include=*.go pkg/`):
`loop.go:6356`, `loop.go:4681`, `task_executor.go:442`, `task_executor.go:1876`, `judge.go:537` —
five sites, all present, **none conditional**. Contrast `WithWorkspaceID`, which *is* guarded at
`loop.go:6381` (this review's MAJ-009). The spec's cited line number is exact.

**Sub-turns are also correct, and this is the non-obvious part the spec does not claim but gets for
free:** a delegated sub-turn re-enters `runTurn` with the *child's* `agent.ID`, because `spawnSubTurn`
swaps identity wholesale. `pkg/agent/subturn.go:816-833` documents a landed production bug ("tool-policy
split-brain", an observed infinite `load_tool` retry loop) caused by *not* swapping it. So
`ToolAgentID(ctx)` at a nested mint yields the immediate parent, not the root — which is exactly what
FR-013 needs for the "no grandchildren" scenario to be true rather than accidental.

**The `subagent` kind is viable.** The one caveat is 7.3 (nothing *enforces* non-empty at mint).

### 8.2 Compositor "fails CLOSED" — ⚠️ **MISLEADING HEADLINE, and it misses god-mode**

The spec's C2 row (spec:30) leads with a bolded *"The compositor **fails CLOSED**"*. Verified source:

```go
// pkg/tools/compositor.go:162-201
if cfg.GodMode { return config.ToolPolicyAllow }          // ← short-circuits the ENTIRE merge
g := resolveFromMap(toolName, cfg.GlobalPolicies, globalWildcards)
a := resolveFromMap(toolName, cfg.Policies, agentWildcards)
switch {
case g == "" && a == "": … return config.ToolPolicyDeny   // ← the ONLY denying case
case g == "":  return a
case a == "":  return g                                   // ← missing per-agent map ⇒ GLOBAL wins
default:       /* deny > ask > allow */
}
```

Two corrections to the spec's framing:

1. **"Fails closed" is true of exactly one of four branches.** Only the *both-empty* case denies. A
   missing **per-agent** map returns the **global** verdict verbatim — and `bash`, `write_file`,
   `set_config` and `create_agent` are all seeded **`allow`** globally
   (`[VERIFIED: pkg/config/defaults.go:360-366]`). **In fairness to the spec, the same table cell
   states this correctly** — *"When only the agent side is empty, `case a == "": return g` — the
   global entry wins, whatever it says."* The evidence is right; the **bolded summary contradicts
   the sentence two clauses later**, and a bolded "fails CLOSED" is what an implementer skimming a
   correction table will carry away. **Downgrade the headline to "fails closed only when neither
   side has an entry."**
2. **God-mode is missed entirely, by both the spec and this review's §2.** `if cfg.GodMode { return
   ToolPolicyAllow }` floors **every** tool at `allow` *before* the merge runs, so under sandbox
   `off` the merge never executes. The spec's dataset row 8 (spec:969, "both sides absent → deny +
   Error log") is therefore **wrong under god-mode**, and SC-008 has no god-mode row. Add a dataset
   row `god-mode enabled → allow regardless of either map`. This is a genuine coverage gap, not a
   wording quibble.

### 8.3 `denyAllThenOverride` ⇒ a new tool ships DISABLED on a fresh install — ✅ **VERIFIED, and the spec is more precise than the question**

```go
// pkg/coreagent/core.go:384-394
func denyAllThenOverride(overrides map[string]config.ToolPolicy) map[string]config.ToolPolicy {
    validateOverrideKeys(overrides)
    out := make(map[string]config.ToolPolicy, len(allStaticToolNames))
    for _, name := range allStaticToolNames { out[name] = config.ToolPolicyDeny }   // ← every catalog name
    for name, policy := range overrides     { out[name] = policy }
    return out
}
```

Fully enumerated over `allStaticToolNames`, every tool stamped `deny`, overrides applied on top. A
per-agent explicit `deny` beats a global `allow` via the `default` branch's deny-wins rule (8.2). So
**yes — adding `list_jobs` to the catalog without adding it to each override map ships it disabled**,
and FR-023(a)(b)(c) is the correct three-part remedy.

**The spec also correctly captures the one exception, which the question did not presuppose:**
`IDWorker` is seeded via `tightenGlobalCeiling` (`core.go:398-415`), which returns a **sparse** map —
absent keys mean *inherit the global*, not *deny*. So Worker resolves `list_jobs` through
`case a == "": return g` and gets the global `allow` **without** an override. The spec states this at
spec:754 and dataset row 2 (spec:963). `validateOverrideKeys` **panics** on a name absent from
`allStaticToolNames` (`core.go:346-368`) — also correctly captured (spec:79-80, 970).

This is the spec's strongest section. No correction needed.

### 8.4 Verdict on evidence quality overall

**The spec's evidence discipline is real and materially better than the ADR's.** All nine C1–C9
corrections survive re-verification (§ method note); every line number spot-checked in this addendum
resolved to the file and construct claimed — `loop.go:6356`, `compositor.go:181-189`,
`core.go:384-394`, `defaults.go:360-366`, `delegate.go:915-959`. The C1 correction in particular
(`AgentID` is the child's, `OwnerScopeID` is `""` for top-level, `ParentDurableKey` is shared) is
confirmed verbatim at the mint site.

The failures are **not** the ADR's failure mode (wrong paths, wrong branches, invented fields). They
are: one bolded summary that contradicts its own evidence (8.2), one un-modelled branch (god-mode),
and — the dominant category in §2 — **correct facts from which the wrong consequence was drawn**
(CRIT-001, MAJ-001, MAJ-002, MAJ-003). The spec's own invented field `task.Filter.PlanIDSet`
(MAJ-002) is the single instance of the old failure mode, and it appears in a parenthetical rather
than a `[VERIFIED:]` tag.

---

## 9. Verdict

**BLOCK** — 3 CRITICAL, 17 MAJOR (16 from §2 + the mint-time gap at 7.3), 7 MINOR.

The three criticals are not polish. CRIT-001 and CRIT-003 each make the tool report a state that is
the opposite of the truth, in the two situations US-1 and US-2 were written for, with green tests.
CRIT-002 leaves the spec's own redaction control open on the field next to the one it closed.

The greenfield ruling **reduces scope but does not unblock**: it deletes `unattributable_subagents`
and its scenario/test, resolves Ambiguity #8, and retires the "prospective only" caveat — but only
if FR-015 is **rewritten as a mint-time fail-closed invariant** (7.3) rather than deleted. Deleting
it while keeping `omitempty` is a net regression: unattributable rows would be silently dropped
instead of counted.

Review written to: `docs/internal/specs/list-jobs-spec-review.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/list-jobs-spec.md docs/internal/specs/list-jobs-spec-review.md
```
