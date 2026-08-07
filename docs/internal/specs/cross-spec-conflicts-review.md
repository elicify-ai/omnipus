# Cross-Spec Conflict Review — `plan-supervisor-spec.md` × `list-jobs-spec.md`

**Created**: 2026-07-27
**Scope**: conflicts, contradictions and gaps **BETWEEN** the two specs only. Neither spec is
reviewed internally — two other agents are doing that concurrently.
**Branch**: `feature/plan-swimlane-board`
**Inputs**:
- `docs/internal/specs/plan-supervisor-spec.md` (rev 2, 3682 lines) — hereafter **PS**
- `docs/internal/specs/list-jobs-spec.md` (rev 2, 2218 lines) — hereafter **LJ**
- ADR-055 / ADR-056 read for decision context only; both specs are authoritative over their ADRs.

> **Method.** Every cross-spec claim below was checked against the working tree, not against either
> spec's citation. Verified facts are tagged `[V: path:line]`. Where a spec's own stated fact was
> re-confirmed, it is marked `[V-confirms-spec]`; where the tree contradicts a spec, that is called
> out explicitly.

> **One structural observation up front, because it explains most of what follows.**
> **LJ references ADR-055 seven times, including an open ambiguity (#7) and assumption A1 naming
> the exact risk. PS references ADR-056 / `list_jobs` ZERO times** — `rg -n "list_jobs|ADR-056|list-jobs" docs/internal/specs/plan-supervisor-spec.md`
> returns no matches. The spec that must be adapted knows it; the spec doing the breaking does not
> know the other exists. Every CRITICAL below is a consequence of that asymmetry.

---

## Verdict

**Do not land these in parallel, and do not land LJ first.** There are 3 CRITICAL and 6 MAJOR
cross-spec defects. Two of them (C2, C3) are latent in *both* specs and are invisible to either
spec's own review, because each is a contradiction only when the two documents are read together.

Recommended order: **PS steps 1–3 → LJ (rebased) in parallel with PS steps 4–11.** Reasoning in §
*Landing Order*.

---

## Severity Summary

| # | Conflict | Severity | Spec that must change |
|---|---|---|---|
| **C1** | `awaiting_owner_correction` renamed by PS; LJ hardcodes the old literal in 6 normative places, and PS's compiler-invisible sweep does not cover LJ's files | **CRITICAL** | **LJ** (adopt new literal) + **PS** (extend FR-062 sweep) |
| **C2** | The two specs give **contradictory and mutually incomplete** builtin-tool seeding recipes. `stop_plan` ships DENIED to every agent under PS's recipe; `list_jobs` turns `defaults_test.go` red under LJ's | **CRITICAL** | **Both** |
| **C3** | PlanSupervisor's supervision work appears on **no** roster, and PS's own `len(allowed)==1` invariant structurally forecloses ever granting it `list_jobs` | **CRITICAL** | **Both** (design decision required) |
| **M1** | PS's two new `failed_reason` values are uncovered by LJ's normalization table and mis-classified by LJ's `intentionally_stopped` derivation | MAJOR | **LJ** |
| **M2** | `session.LifecycleRecord` co-edited; PS's edit crosses a wire contract LJ asserts is untouched | MAJOR | **PS** (name the contract files) + **LJ** (stale field names) |
| **M3** | `PlanEngine.Tick` / `processPlan` seam co-edited; LJ's "refreshed unconditionally on every Tick" is false on the exact failure it exists to signal | MAJOR | **LJ** |
| **M4** | LJ's FR-029 staleness bound is never stated; PS supplies the 30 s tick that makes it decidable. A bound < 30 s silently retires LJ's entire cap-pressure feature | MAJOR | **LJ** |
| **M5** | PS step 2 is a mechanical `Owner*` rename sweep over `pkg/plan` + `pkg/session`; LJ adds **new** `Owner`-named fields to both packages | MAJOR | **PS** (scope the sweep) |
| **M6** | `blocked` means opposite things to the two specs; combined with PS's new owner-held `stop_plan` tool, LJ actively invites the intervention PS forbids | MAJOR | **Both** |
| m1 | `Plan.yaml` is *not* the merge hazard it looks like — but LJ's SC-014 is stated as a branch-wide gate | minor | LJ |
| m2 | Post-stop roster window: cap snapshot is up to one tick (30 s) stale; the stopped plan vanishes by default | minor | LJ |
| m3 | The two specs disagree about whether `buildKnownBuiltinToolNames` is a literal to edit | minor | PS |
| m4 | LJ's Edge Case / Ambiguity #10 name `OwnsPlanID` and `OwnerScopeID`, both renamed by PS | minor | LJ |

---

## CRITICAL

### C1 — Status vocabulary: PS renames the literal LJ's entire `blocked` semantics rest on

**Verified in the tree.** The phase constant is live and unrenamed today:

```go
// pkg/plan/plan.go:237                                   [V]
PhaseAwaitingOwnerCorrection PlanPhase = "awaiting_owner_correction"
```

and it is a member of `validPlanPhases` `[V: pkg/plan/plan.go:261-268]`.

**PS S9 row 1** renames it to `awaiting_supervision`, calls it *"the operator-locked item"*, and
lists it as the spec's **HIGHEST-RISK** change (PS §4.2 ⚠ block).

**LJ hardcodes the old literal in six normative places** — every one of which is a requirement,
a gate or a test name, not prose:

| LJ location | Text |
|---|---|
| US-2 AS-3 (`:264`) | *"`plan_phase=awaiting_owner_correction` … `native_status` reflects `awaiting_owner_correction`"* |
| BDD Scenario Outline row (`:745`) | `plan \| state=running, phase=awaiting_owner_correction \| blocked \| running/awaiting_owner_correction` |
| BDD Scenario title + body (`:780-787`) | *"awaiting_owner_correction outranks stalled"* |
| Test matrix #5 (`:1429`) | `TestNativeStatus_AwaitingCorrectionOutranksStalled` |
| Dataset row 5 (`:1517`) | `state=running, phase=awaiting_owner_correction` |
| **SC-002** (`:1989`) | *"A plan with `plan_phase ∈ {stalled, awaiting_owner_correction}` reports `status="blocked"` … 100% of cases"* |

**Why this is CRITICAL rather than a find-and-replace.** LJ's `native_status` output is a
**composed string literal** (`"running/awaiting_owner_correction"`, per LJ's own format contract in
Ambiguity #3). If the Go implementation switches on `plan.PhaseAwaitingSupervision` the compiler
catches the constant — but the composed output literal, the test name, the dataset and SC-002 are
all compiler-invisible.

**And PS's own control for exactly this class does not cover LJ.** PS FR-062's sweep is scoped to
`src/**`, `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`, `tests/e2e/**`, YAML prose and
`*.md` (PS step 2, `:3095`). It does **not** include `pkg/tools/**` or `pkg/agent/**` — precisely
where `list_jobs`' status mapping and its `native_status` composition will live. PS's C7 enumerates
"four categories invisible to `go build` and `tsc -b`"; a fifth exists the moment LJ lands, and PS
cannot know about it.

**Disposition**
- **LJ** must adopt `awaiting_supervision` throughout (6 sites above) and re-point SC-002.
- **PS** must extend FR-062's directory list to `pkg/tools/**` and `pkg/agent/**` for composed
  string literals, or state that composed literals are forbidden and the constant must be
  interpolated.
- If LJ lands first, PS's sweep will not find these and PS's SC-011 (*"`rg` returns zero"*) will
  pass while LJ ships a wrong `native_status`.

---

### C2 — Two contradictory builtin-tool seeding recipes; neither is complete, and each omits what the other requires

Both specs add builtin tools to the same catalog in the same release (LJ: `list_jobs`; PS:
`plan_correct` **and** `stop_plan`). Both wrote a "four sites" recipe. **The two lists are
different, and neither is a superset of the other.**

| Site | LJ FR-023 | PS FR-006 |
|---|---|---|
| `coreagent.allStaticToolNames` | ✅ (a) | ✅ |
| global `DefaultConfig().Sandbox.ToolPolicies` | ✅ (b) | ✅ |
| **every `denyAllThenOverride` override map for non-system agents** | ✅ **(c)** | ❌ **omitted** |
| **`pkg/config/defaults_test.go`'s `wantToolCount`** | ❌ **omitted** | ✅ |
| `tools.GeneralBuiltinMetadata()` / builtin metadata registry | ✅ (d) | ✅ |
| `buildKnownBuiltinToolNames` | "derives from live metadata, so registering is enough" (`:161`) | listed as a separate literal to edit |

#### C2a — `stop_plan` ships DENIED to every agent on a fresh install (PS's own N1b, applied to the tool it forgot)

**Verified in the tree:**

```go
// pkg/coreagent/core.go:384-394                          [V]
func denyAllThenOverride(overrides map[string]config.ToolPolicy) map[string]config.ToolPolicy {
	validateOverrideKeys(overrides)
	out := make(map[string]config.ToolPolicy, len(allStaticToolNames))
	for _, name := range allStaticToolNames {
		out[name] = config.ToolPolicyDeny          // ← explicit deny for EVERY catalog name
	}
	for name, policy := range overrides { out[name] = policy }
	return out
}
```

and `coreAgentSeed` uses `denyAllThenOverride` for **every non-Worker base agent**
`[V: pkg/coreagent/core.go:574, :585, :631, :674, :734]`; only `IDWorker` uses the sparse
`tightenGlobalCeiling` `[V: :447]`.

Per the compositor, a per-agent `deny` beats a global `allow` (strictest-wins,
`compositor.go:193-200`) — the exact mechanism PS documents at length in **N1b** for
`plan_correct`.

**PS applies N1b to `plan_correct` (FR-008) and never applies it to `stop_plan`.** PS FR-006 lists
only the four catalog literals; PS implementation step 4 (`:3098`) says *"the `systemAgentSeed`
case **naming `plan_correct` and nothing else**"*; no PS requirement anywhere adds
`"stop_plan": allow` to `coreAgentSeed`, the seeded specialists, or `NewCustomAgentToolsCfg`.

**Consequence:** on a fresh install `stop_plan` resolves `deny` for Mia, Jim, Ava, Ray, every
seeded specialist and every newly-created custom agent. PS's own **US-8 AS-2** (*"an agent that
owns a running plan … calls `stop_plan` … the plan transitions to `failed(stopped_by_user)`"*) and
**Dataset B11** (*"`jim` (the plan's owner) calling `stop_plan` → **Allowed**"*) are unreachable.
This is the ADR-037 anti-pattern CLAUDE.md explicitly bans — registered, green in CI, dead in the
field.

LJ's FR-023(c) is the correct recipe and PS must adopt it.

#### C2b — `list_jobs` turns `pkg/config/defaults_test.go` red

**Verified:**

```go
// pkg/config/defaults_test.go:92-96                      [V]
const wantToolCount = 83
if got := len(cfg.Sandbox.ToolPolicies); got != wantToolCount { … }
```

LJ never mentions `wantToolCount`. Adding `"list_jobs": allow` to `DefaultConfig().Sandbox.ToolPolicies`
(LJ FR-023(b), mandatory) makes `len == 84` and fails this test. LJ's SC-015 requires the Go suite
to pass in CI, so LJ is internally inconsistent *and* silently depends on a fact only PS records.

#### C2c — If both land, the count must reach 86, and each spec's commit collides on the same line

83 + `list_jobs` + `plan_correct` + `stop_plan` = **86**. PS step 3 says to update `wantToolCount`;
LJ does not touch it. Whoever lands second gets a red test on a line the first lander already
edited. PS FR-006 partially anticipates this (*"asserted mechanically … rather than quoted as a
literal in prose **or in a test**"*, test #2 asserts
`len(allStaticToolNames) == len(cfg.Sandbox.ToolPolicies)`) — but PS's own step 3 simultaneously
instructs updating the hardcoded `wantToolCount`, and LJ knows about neither.

**Disposition**
- **PS** adds `"stop_plan": allow` to `coreAgentSeed` (all base agents), the seeded specialists and
  `NewCustomAgentToolsCfg` — i.e. adopts LJ FR-023(c) verbatim; and asserts resolved policy, not
  the seed literal, for `stop_plan` on Jim (a Dataset-B11 test that actually runs the compositor).
- **LJ** adds `pkg/config/defaults_test.go`'s `wantToolCount` to its FR-023 site list.
- **Both** should replace the hardcoded `wantToolCount` with PS test #2's mechanical assertion in
  whichever change lands first, so the second lander never touches the number.

---

### C3 — PlanSupervisor has zero self-visibility, and PS's own invariant forecloses fixing it

Three independently verified facts compose into a gap neither spec sees.

1. **PlanSupervisor can never be a plan's `OwnerAgentID`.**
   ```go
   // pkg/config/config.go:1052-1054                      [V]
   func (a AgentConfig) IsChatTarget() bool { return !a.IsWorker() && !a.IsSystem() }
   ```
   Both `Plan.OwnerAgentID` write paths are guarded by `IsChatTarget()`
   `[V-confirms-spec: PS :190, :277]`, and PS seeds PlanSupervisor as a System Agent (S1, FR-002).
   LJ's plan predicate is `Plan.OwnerAgentID == caller` (FR-010) — **it can never match for
   PlanSupervisor.**

2. **The supervision session is not a `subagent` row either.** LJ's subagent predicate is
   `LifecycleRecord.ParentAgentID == caller`, populated **only** at `delegate`'s lifecycle mint
   (LJ FR-013/FR-015). PS's supervision session is created by the wake path
   (`wakeOwner` → `asyncNotifier` → bus, PS FR-016/N8), never through `delegate`. No
   `LifecycleRecord`, no `ParentAgentID`, no row.

3. **LJ forbids granting it, and PS forbids ever un-forbidding it.**
   - LJ Explicit Non-Behavior: *"must **not** be granted to System Agents by default"* (`:594`);
     US-7 AS-3 and SC-008 assert `deny` for every System Agent.
   - PS FR-008: *"The resolved policy MUST be `allow` for **exactly one** name — `plan_correct` —
     and `deny` for every other name in `allStaticToolNames`. **Stated as a complement, not as a
     list**, so a tool added to the catalog later can never silently land in PlanSupervisor's allow
     set."* PS test #4 asserts `len(allowed) == 1 && len(denied) == len(allStaticToolNames) - 1`.

**Net effect.** The one agent in the system that autonomously mutates running plans — that can
`append`, `supersede`, `targeted_retry` and `abandon` — has **no** way to enumerate the plans it is
supervising, no way to notice it has three parked plans awaiting adjudication, and no way to detect
that a wake it was sent produced nothing. PS's answer to "how does the engine know the turn
produced nothing" is a deadline the *engine* checks (FR-021); the agent itself is blind. And PS's
complement-complete assertion means a future change granting `list_jobs` to PlanSupervisor fails
PS's own test #4 — the guard PS added deliberately, for good reasons, now also blocks the fix.

This is **not** obviously wrong — an adjudicator woken per-plan arguably needs no roster. But it is
an undocumented consequence of two independently reasonable decisions, and it is invisible from
inside either spec.

**Disposition — decision required, not a mechanical fix.** Pick one and record it:
- **(a) Accept.** State in PS FR-008 that PlanSupervisor is deliberately roster-blind, that the
  engine's `supervision.wake_at` deadline is the *only* liveness control, and that LJ's `blocked`
  rows for parked plans are therefore visible to the Owner alone. Add the sentence to LJ's Explicit
  Non-Behaviors too, so the next author does not "fix" it.
- **(b) Grant.** Add `list_jobs` to PlanSupervisor's `systemAgentSeed` override map, change PS
  FR-008/test #4 to `len(allowed) == 2`, and add a `supervises` relation to LJ's FR-010 predicate
  table keyed on `plan_phase == awaiting_supervision` (not on ownership, which can never match).
  This is materially more work and widens the most privileged agent's surface.

Option (a) is the cheaper and probably correct call, but it must be **written down** — right now
neither spec says anything, and the gap reads as an oversight rather than a decision.

---

## MAJOR

### M1 — LJ's status mapping does not cover PS's two new `failed_reason` values, and mis-classifies one

**Verified today's enum is closed at four:**

```go
// pkg/plan/plan.go:291-303                               [V]
FailedReasonJudgeRoundsExhausted / StoppedByUser / IdleExpired / BudgetExhausted
var validFailedReasons = map[FailedReason]bool{ …exactly those four… }
```

PS FR-035 / S15 add `dod_unreachable` and `supervision_unavailable`.

**Gap 1 — coverage.** LJ's Scenario Outline (`:749-750`), Dataset rows 10/12 (`:1522`, `:1524`) and
US-2 AS-4 enumerate only `judge_rounds_exhausted` and `stopped_by_user`. Neither new value appears
anywhere in LJ. LJ's `native_status` format is `failed:<reason>`, so an unmapped value degrades
gracefully — but LJ has no test row proving it, and LJ's own FR-019 table classifies
`plan.Plan.FailedReason` as the one *validated, closed* source it can trust; that trust is silently
weakened by a value LJ has never seen.

**Gap 2 — semantic mis-classification.** LJ FR-006 derives `intentionally_stopped` **only** from
`plan.FailedReasonStoppedByUser` and `session.LifecycleCancelled`. PS's `abandon` verb is a
*deliberate* adjudicated termination that lands as `dod_unreachable` (PS FR-035 row 3, US-1 AS-6).
Under LJ's rule it reads `intentionally_stopped=false` — i.e. indistinguishable from a crash. That
is exactly the failure LJ's own rev-2 correction to FR-006 exists to prevent (*"an agent that
stopped a delegation on purpose, then lost context, would see `failed`, and re-dispatch work the
user deliberately cancelled"*), reintroduced through a value LJ does not know about.

**Disposition — LJ.** Add both values to the Scenario Outline and the dataset; decide and state
`intentionally_stopped` for each (`supervision_unavailable` → `false`, it is a failure;
`dod_unreachable` → arguable, but an `abandon`-driven one is deliberate and a
`planCannotProgress`-driven one is not — PS does not currently distinguish them on the record,
which is itself worth raising with PS).

---

### M2 — `session.LifecycleRecord` is co-edited, and PS's edit crosses a wire contract LJ asserts is untouched

**Verified — the fields sit in the same struct, adjacent to LJ's insertion point:**

```go
// pkg/session/lifecycle.go:183-243                       [V]
// not-wire-format: internal disk record; a caller (pkg/tools/delegate.go)
// maps it onto generated.SessionLifecycleRecord at the tool-result boundary.
type LifecycleRecord struct {
	…
	OwnerScopeKind OwnerScopeKind `json:"owner_scope_kind"`   // ← PS S9 row 4 renames
	OwnerScopeID   string         `json:"owner_scope_id,omitempty"`  // ← PS S9 row 4
	OwnsPlanID     string         `json:"owns_plan_id,omitempty"`    // ← PS S9 row 5
	…                                                        // ← LJ FR-013 adds ParentAgentID here
}
```

**And these names DO cross the generated wire contract:**

```
contracts/components/schemas/SessionLifecycleRecord.yaml:20   - owner_scope_kind   (in `required`)
contracts/components/schemas/SessionLifecycleRecord.yaml:76   owner_scope_kind:
contracts/components/schemas/SessionLifecycleRecord.yaml:91   owner_scope_id:
contracts/components/schemas/SessionLifecycleRecord.yaml:98   owns_plan_id:
contracts/components/schemas/Goal.yaml:41                     …for `SessionLifecycleRecord.owner_scope_kind`/`owner_scope_id`.
contracts/components/schemas/Plan.yaml:135                    …reciprocal of `SessionLifecycleRecord.owns_plan_id`
```
`[V — all six lines]`

**Two cross-spec problems:**

1. **PS's implementation step 1 lists `SessionLifecycleRecord.yaml`** (`:3094`) — good — but its
   step-1 list does **not** include `Goal.yaml:41` or `Plan.yaml:135`, both of which name
   `owner_scope_kind`/`owner_scope_id`/`owns_plan_id` in *prose descriptions*. Those are exactly
   the compiler-invisible class FR-062 exists for, and FR-062's directory list covers "YAML prose"
   generically without naming these two files. Low cost, easy to miss.

2. **LJ FR-025/SC-014 assert an absolute "no contract change, no regenerated artifacts" gate.**
   SC-014: *"A **non-empty `git diff`** on `pkg/api/generated/` or `src/lib/api/generated/` is a
   **failure** of this criterion."* On a shared branch where PS has already regenerated, that gate
   reads as branch-wide and will fail spuriously. LJ must scope it to *its own* diff
   (`git diff <LJ-base>..HEAD -- pkg/api/generated/`), not to the tree.

3. **LJ's Explicit Non-Behaviors and Symbols table name the pre-rename fields.** LJ `:590`
   (*"must not infer a subagent's parent from `ParentDurableKey`, `OwnerScopeID` or `AgentID`"*),
   LJ `:96` (C1), LJ Edge Case `:557` and Ambiguity #10 (`OwnsPlanID`). All stale after PS. These
   are load-bearing anti-inference rules; a stale field name in the rule that prevents the
   sibling/cousin leak is worse than cosmetic.

**Disposition** — **PS**: add `Goal.yaml` and `Plan.yaml` prose to step 1's explicit file list.
**LJ**: rename the field references and scope SC-014 to its own diff.

---

### M3 — `PlanEngine.Tick` co-edited, and LJ's "refreshed unconditionally on every `Tick`" is false on the failure it exists to signal

**Verified `Tick`'s actual body:**

```go
// pkg/agent/plan_engine.go (Tick)                        [V]
func (pe *PlanEngine) Tick(ctx context.Context) {
	if !pe.claimTick() { … return }              // overlap guard — a slow tick SKIPS the next pass
	defer pe.releaseTick()

	plans, err := pe.planStore.List(plan.Filter{})
	if err != nil {
		logger.WarnCF("plan_engine", "tick: list plans failed", …)
		return                                    // ← EARLY RETURN, before any refresh point
	}
	for i := range plans {
		switch p.State {
		case plan.StateApproved: pe.tryStartApprovedPlan(ctx, p.ID)
		case plan.StateRunning:  pe.processPlan(ctx, p.ID)      // ← PS step 7 rewrites this
		…
```

**Both specs edit this one function's immediate neighbourhood.** LJ FR-029 inserts a cap-snapshot
refresh into `Tick`'s body; PS step 7 adds a parked-phase case to `processPlan`'s phase switch,
reached from this loop, plus the supervision deadline check that also runs per tick. Textual merge
conflict is likely but small.

**The substantive cross-spec finding is different.** LJ FR-029 requires the snapshot *"refreshed
**unconditionally** on every `Tick`"* and relies on `reliable=false` to signal a bad read. But
`Tick` **returns early on a `planStore.List` error** — the snapshot is not refreshed at all, and
`computeActiveLocked` never runs, so `reliable` is never set false. LJ then falls through to its
staleness bound (M4, unstated) as the only remaining guard. The `Tick` overlap guard compounds it:
a slow tick makes the *next* tick a silent no-op, so the snapshot can be two intervals (60 s) stale
under exactly the load where cap pressure matters.

PS is the spec that verified this function in detail (PS `:190`, `:641`, FR-027) and LJ inherited
the "already scanning, so the refresh is free" claim without the early-return caveat.

**Disposition — LJ.** FR-029 must state: refresh at the *top* of `Tick` before the early return, or
explicitly mark the snapshot stale when `List` fails; and account for the overlap guard in the
staleness bound.

---

### M4 — LJ's FR-029 staleness bound is unstated; PS supplies the number that decides whether the feature works at all

LJ FR-029 / Integration Boundaries (`:642`) require omitting `cap_active` + `cap_max` when the
snapshot is *"older than **a stated staleness bound**"* — and **LJ never states one.**
`rg "staleness|observedAt|stale" list-jobs-spec.md` returns four hits, none a number.

PS FR-027 pins the unit LJ needs:

```go
// pkg/agent/plan_engine.go:131                           [V]
defaultPlanEngineTickInterval = 30 * time.Second
```
— a package const, **not** a config key `[V-confirms-spec: PS FR-027]`.

**Consequence.** Any bound below 30 s makes the snapshot *always* stale, and LJ's FR-029 then omits
both fields on **every** call. That silently retires US-2 AS-5, AS-6, AS-7, SC-003 and the entire
CRIT-001 fix — the single largest piece of rev-2 work in LJ — with green tests, because every LJ
cap-pressure test constructs a `PlanEngine` and Ticks it immediately. Combined with M3's early
return and overlap guard, a defensible bound is ≥ 2× the tick (60–90 s), which is a long staleness
window that LJ should state and justify rather than leave to the implementer.

**Disposition — LJ.** State the bound numerically, cite PS FR-027 for the 30 s tick, and add a test
that advances the clock past the bound and asserts both fields absent.

---

### M5 — PS's `Owner*` rename sweep runs over the two packages LJ adds new `Owner*` fields to

PS implementation step 2 is a mechanical rename pass: `Plan.OwnerSessionID` → `SupervisionSessionID`,
`OwnerScopeKind`/`OwnerScopeID` → `ScopeKind`/`ScopeID`, `OwnsPlanID` → `SupervisedPlanID`,
`ownerKey` → `scopeKey`, `ProcessSession.OwnerSessionID` → `TranscriptSessionID`. Five of the seven
rows are `Owner`-prefixed identifiers in `pkg/plan` and `pkg/session`.

LJ adds, in the same two packages:
- `plan.Filter.OwnerAgentID` (FR-014) — **a brand-new `Owner`-prefixed field in `pkg/plan/store.go`**,
  the same file PS renames `Plan.OwnerSessionID` in (`pkg/plan/store.go:397-398`, per PS S9 row 3).
  Verified today's filter is literally `struct { WorkspaceID string }` `[V: pkg/plan/store.go:120-123]`.
- `session.LifecycleRecord.ParentAgentID` + `LifecycleFilter.ParentAgentID` — not `Owner`-prefixed,
  but inserted into the struct PS is renaming three fields of.

PS's **O3** protects `Plan.OwnerAgentID` **by name** from the sweep. It says nothing about a
*different* `OwnerAgentID` on a *different* type added by another spec. An implementer running the
sweep as `rg`-driven find-and-replace on `Owner` will eat `plan.Filter.OwnerAgentID` if LJ has
already landed — and PS S9 row 3's own note warns that this exact over-reach hazard is why rows 3
and 7 were split.

**Disposition — PS.** Step 2 must state that the sweep is **allow-listed to the seven named
identifiers**, never a prefix match, and that `*.OwnerAgentID` on any type is out of scope. This is
free if PS lands first (the field does not exist yet) and is a live hazard if LJ lands first.

---

### M6 — `blocked` means opposite things to the two specs, and PS hands the Owner a stop button

This is the semantic half of C1 and is the one finding that produces wrong *behaviour* rather than
a wrong string.

**LJ's definition** (FR-006): `blocked` means *"live but unable to progress **without
intervention**"*. US-2's whole thesis is telling *waiting* from *working* from **"stuck and needing
intervention"**. LJ maps `plan_phase=awaiting_owner_correction` → `blocked` (BDD `:745`) and asserts
it at 100% (SC-002).

**PS's redefinition.** Under PS, that phase (renamed `awaiting_supervision`) means *"PlanSupervisor
has been woken and is adjudicating"* — an actively-handled state. PS FR-011 states the Owner *"MUST
have **no** adjudication or correction role"*, and PS FR-009/US-2 AS-2 require `plan_correct` to
**deny the plan's own `owner_agent_id`**.

**The composition.** The plan appears on the **Owner's** roster (LJ FR-010: `OwnerAgentID == caller`)
as `blocked` — "needs intervention". The Owner is the one principal PS forbids from intervening.
And PS FR-042/FR-043 newly grants that same Owner a `stop_plan` tool whose authority is *precisely*
`caller.AgentID == p.OwnerAgentID`.

So an Owner agent that calls `list_jobs`, reads `blocked` on its own plan, and acts on LJ's own
stated semantics has exactly one tool available: **stop the plan that is mid-adjudication.** PS's
FR-044 cascade then cancels the in-flight supervision turn. The two specs compose into an agent
that kills healthy work — the same class of error as LJ's CRIT-001, arriving from the other spec.

**Disposition — both.**
- **LJ**: `awaiting_supervision` must not normalize to bare `blocked` with no qualifier. Either add
  a distinct signal (the cleanest is a row field such as `attended_by`/`handled_elsewhere`, or
  reuse the existing `intentionally_stopped` sibling pattern), or at minimum make `native_status`
  carry `running/awaiting_supervision` **and** require the tool description to state that
  `awaiting_supervision` is handled by another agent and must not be intervened on. LJ already has
  the precedent for exactly this (FR-006's `intentionally_stopped` boolean, added because the
  five-value vocabulary had no slot for "deliberately stopped" — the same shape of problem).
- **PS**: FR-045's operator documentation, and the `stop_plan` tool description (FR-042), must state
  that stopping a plan at `awaiting_supervision` aborts an in-flight adjudication.

---

## Minor / watch

- **m1 — `Plan.yaml` is *not* the two-editor hazard it appears to be.** The brief's hypothesis #3
  does not hold: **LJ makes zero contract changes** (FR-025 is explicit and SC-014 gates on *no*
  drift). PS owns `Plan.yaml` entirely — the phase enum rename, the two new `failed_reason` values
  and the new `supervision` object all land in PS step 1's single `gen-contracts` run. There is one
  regeneration, not two, and no ordering hazard *inside* the file. The only real issue is LJ's
  branch-wide phrasing of SC-014 (see M2.2).

- **m2 — post-stop roster window.** `StopPlan` writes `failed(stopped_by_user)` synchronously under
  `planDecisionMu`, so LJ's plan row flips to `failed` + `intentionally_stopped=true` immediately —
  correct, no stale-`running` window. Member/verifier/supervision sessions are not `delegate`-minted
  lifecycle records, so they never appear as `subagent` rows either. Two residual effects worth a
  sentence each: (i) `cap_active` still counts the stopped plan for up to one tick (30 s, M3/M4);
  (ii) with LJ's `include_terminal=false` default the stopped plan **vanishes from the roster
  entirely**, surviving only as `terminal_suppressed` — and calling `list_jobs` right after
  `stop_plan` is the single most likely thing an agent does.

- **m3 — the specs disagree about `buildKnownBuiltinToolNames`.** Verified: it iterates
  `tools.GeneralBuiltinMetadata()` + `browser.BrowserBuiltinMetadata()` + `systools.AllTools(nil,nil)`
  and then unions **four** hardcoded ADR-052 names `[V: pkg/gateway/gateway.go:715-745]`. So LJ's
  *"derives from live metadata, so registering the tool is enough"* (`:161`) is **correct**, and
  PS's framing of it as a fourth literal to hand-edit is **incorrect** (harmless — an idempotent
  extra union — but it will confuse the implementer, and PS's test #2 asserts agreement across "all
  four literals" when only three are literals).

- **m4 — stale field names in LJ.** Edge Case *"A plan-owner session that is also a delegated
  subagent"* (`:557`) and Ambiguity #10 both key on `OwnsPlanID`; Explicit Non-Behaviors (`:590`)
  and C1 (`:96`) key on `OwnerScopeID`. All renamed by PS S9 rows 4–5.

---

## Landing Order

**Recommendation: PS steps 1–3 land first as their own PR. LJ then rebases and proceeds in parallel
with PS steps 4–11.**

### Why PS first

1. **PS owns every contract change in the pair; LJ owns none.** PS step 1 is a single
   `scripts/gen-contracts.sh` run committing `pkg/api/generated/`, `src/lib/api/generated/` and
   `pkg/gateway/inboundschemas/` atomically (Constraint #8). LJ's release gate (SC-014) is
   *"`make verify-contracts` green with **no drift**"* — a gate that is only meaningful, and only
   passes cleanly, on a base where the regeneration has already happened.

2. **PS owns the vocabulary LJ's `blocked` semantics rest on, and LJ says so itself.** LJ A1:
   *"`blocked`'s entire semantics rest on `plan.PlanPhase`'s `awaiting_owner_correction` and
   `stalled` values, which are **ADR-055 constructs** … if Phase 2 changes phase semantics, this
   spec's status mapping changes with it."* LJ Ambiguity #7 is still **Open** on exactly this.
   Writing LJ once against final names costs a spec edit; retrofitting it costs a code edit, a test
   rename, a dataset row and an SC restatement — in files PS's own compiler-invisible sweep does
   not cover (C1).

3. **PS step 2 is a rename sweep over the two packages LJ adds fields to.** Running it on a tree
   that does not yet contain `plan.Filter.OwnerAgentID` and `LifecycleRecord.ParentAgentID` is
   strictly safer (M5). PS also renames three fields adjacent to LJ's insertion point in
   `LifecycleRecord` (M2) — doing the rename first turns a three-way merge into a clean append.

4. **Only one ordering forces a double-edit.** LJ-first forces PS to re-touch LJ's status mapping,
   test names, dataset and SC-002 — work PS's FR-062 sweep will not find, so it will be found (or
   not) by hand. PS-first forces LJ to be authored against final names, which is a documentation
   change made before any code exists.

5. **The asymmetric awareness settles it.** LJ has already flagged the dependency (Ambiguity #7,
   A1) and can absorb the change deliberately. PS does not reference ADR-056 or `list_jobs`
   anywhere and will not absorb it at all.

### Why only steps 1–3, not all of PS

PS step 7 (`plan_engine.go`) is by PS's own description *"the largest step … should be reviewed as
several PRs"*. Blocking LJ behind all of PS serialises the whole release for no benefit: LJ's
dependencies on PS are entirely in steps 1–3 (contracts + rename + tool catalog). Once step 2 lands,
LJ's names are final and LJ can proceed concurrently with PS steps 4–11.

### Hard prerequisites before either lands

| # | Change | Owner | Blocks |
|---|---|---|---|
| 1 | LJ adopts `awaiting_supervision` in all 6 sites; re-points SC-002 | LJ | C1 |
| 2 | PS extends FR-062's sweep to `pkg/tools/**` + `pkg/agent/**` for composed literals | PS | C1 |
| 3 | PS adds `"stop_plan": allow` to `coreAgentSeed` / specialists / `NewCustomAgentToolsCfg`; asserts resolved policy on Jim | PS | C2a |
| 4 | LJ adds `wantToolCount` to its FR-023 site list; both agree to replace it with the mechanical assertion | Both | C2b/C2c |
| 5 | Record the PlanSupervisor roster-blindness decision (accept or grant) in **both** specs | Both | C3 |
| 6 | LJ adds `dod_unreachable` + `supervision_unavailable` rows and decides `intentionally_stopped` for each | LJ | M1 |
| 7 | LJ states FR-029's staleness bound numerically, citing PS FR-027's 30 s tick | LJ | M4 |
| 8 | PS scopes step 2's sweep to the seven named identifiers, never a `Owner` prefix match | PS | M5 |
| 9 | LJ adds a distinguishing signal for `awaiting_supervision`; PS documents that `stop_plan` aborts adjudication | Both | M6 |

Items 1–5 are release-blocking. Items 6–9 are correctness defects that will ship silently if not
fixed before implementation starts.

---

## Non-findings (checked, no conflict)

- **`Plan.yaml` double-edit** — does not occur. LJ makes no contract change (FR-025). See m1.
- **Ownership predicate divergence** — the brief's hypothesis #1 does **not** hold. Both specs
  independently converged on `Plan.OwnerAgentID` and explicitly reject `Plan.Owner` as a
  username-namespaced attribution field (LJ C4/FR-010; PS C6/FR-013). Their reasoning is
  compatible and their citations agree with the tree
  `[V: pkg/plan/plan.go:361-363, :437; contracts/components/schemas/Plan.yaml:244-250]`. The
  *consequence* neither drew is C3.
- **Greenfield ruling** — consistent across both (PS operator ruling 3, LJ operator ruling 2 / A0).
  No migrator in either; no conflict.
- **`gen-contracts` run twice** — no. One run, in PS step 1.
- **`plan_correct` vs `list_jobs` policy collision** — none. PS's `len(allowed)==1` assertion for
  PlanSupervisor survives LJ's grant because LJ explicitly excludes System Agents. The problem is
  the *opposite* one (C3): it survives too well.
- **Stop → stale `running` rows** — no. See m2; the plan row flips synchronously and member sessions
  were never roster rows.
