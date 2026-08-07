# Spec Grill Report (Round 2) — `plan-supervisor-spec.md`

**Spec reviewed**: `docs/internal/specs/plan-supervisor-spec.md` (2143 lines, Draft, 2026-07-27)
**Branch**: `feature/plan-swimlane-board`
**Detected mode**: `plan-spec` (BDD scenarios + `FR-xxx` + traceability matrix + `SC-xxx` — full checks apply)
**Review date**: 2026-07-27
**Prior round**: `plan-supervisor-spec-review.md` (4 CRITICAL / 10 MAJOR / 6 MINOR / 3 OBS — BLOCK)
**Reviewer stance**: adversarial, read-only. No spec, ADR or code file was modified.

**Operator rulings carried forward from round 1** (these postdate the spec and I have applied them):

1. **No data migration, anywhere. Greenfield.** Existing on-disk data is expected not to load; that
   is accepted, not a defect. **ADR-055 D14 ships all seven rows**; AMB-1's descope recommendation is
   overruled.
2. **ADR-055 has been amended** — no migrator ships.

**Scope of this round.** Round 1 is sound and I did not re-derive it. I re-verified its four
CRITICALs hold and then attacked the areas it passed: the **runtime control flow after a supervision
wake**, the **tool's own input surface**, and the **outcome-delivery re-targeting**. All four new
CRITICALs live there. Round 1 marked structural checks 2, 3, 4 and 7 **PASS**; this round falsifies
checks 2/3 (mis-traced back-references) and 7 (a whole request field appears in no dataset).

Every code claim below was verified by opening the file in the working tree.

---

## 1. Executive Summary

Round 1 found that several of the spec's *remedies* are structurally rejected by the code. Round 2
finds something worse in a different place: **the spec never specifies what happens between waking
PlanSupervisor and the plan moving again.** That interval contains the entire feature, and it is
empty.

Three consequences, each independently CRITICAL. (a) FR-019's definition of "unavailable" has three
limbs and the engine can observe **none** of them — the wake is fire-and-forget and no observation
seam is added anywhere. (b) A supervision wake fires **once per park** and a rejected correction
mutates nothing, so the first time the adjudicator emits a bad tool call the plan is stranded
until idle expiry — and a plan correctly diagnosed as *un-correctable* has no exit path at all,
despite US-1 requiring one. (c) FR-012 re-targets the MET-synthesis wake to PlanSupervisor and
leaves the Owner only the two *failure* wakes, so a plan that **succeeds** now notifies nobody.

A fourth: the `plan_correct` tool's parameter schema is never written down. `CorrectionRequest`
carries `TailEdges []IntentEdge` — a field that appears in **no** dataset, **no** BDD scenario,
**no** edge case and **no** test — and the spec cites hand-authoring `[]task.Task` as the reason a
*human* cannot use this API without ever asking whether an *LLM* can.

| Severity | Count (this round) |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 8 |
| MINOR | 5 |
| OBSERVATION | 3 |
| **Total** | **20** |

**Combined with round 1: 8 CRITICAL, 18 MAJOR, 11 MINOR, 6 OBSERVATION.**

### Verdict: **BLOCK**

---

## 2. Findings

### CRITICAL

---

#### C2-01 — FR-019 requires the engine to detect that PlanSupervisor is "unavailable", and no observation seam exists or is specified

**Lens**: Incompleteness / Infeasibility · **Sections**: §14 FR-019, §11.1 "On failure", §7 US-4 AS6,
§12 *PlanSupervisor unavailable leaves the plan parked…*, §13.2 test #36, §22 AMB-4

FR-019 defines "unavailable" as **any of**: (a) the turn returns a provider error; (b) the turn
exceeds the supervision timeout; (c) the turn produces neither a `plan_correct` call nor an
honest-exit conclusion.

Verified control flow. The supervision wake is `wakeOwner` (`pkg/agent/plan_engine.go:2096`) →
`asyncNotifier.Notify` → `bus.PublishInbound` (`pkg/agent/async_notifier.go:271`). The spec states
the property itself in §11.2: *"**Data out**: none (fire-and-forget today)"*, and
`async_notifier.go:248-250` confirms the bus is a pure delivery primitive. `wakeOwner` returns
nothing about the turn and logs a WARN on publish failure (`:2109-2111`).

**Nothing anywhere in this spec adds a completion callback, a turn-outcome channel, a deadline
timer, or any other seam by which `PlanEngine` learns that PlanSupervisor's turn errored, timed out,
or produced nothing.** All three limbs are therefore unobservable by the component required to act
on them:

| Limb | What would have to observe it | Specified? |
|---|---|---|
| (a) provider error | the sub-turn result path reporting back to the engine | **no** |
| (b) timeout exceeded | a deadline armed at wake time and checked on a later tick | **no** |
| (c) no tool call and no conclusion | the same deadline, plus a definition of "conclusion" | **no** |

FR-023's boot re-wake fires once per **restart**, not on a timeout. AMB-4 asks *what the timeout
value is* — it never asks who arms it, who checks it, or what cancels it. Round 1's Unasked Question
3 raised the value; this is the mechanism, and it is a different gap.

"Honest-exit conclusion" is also never defined as an **observable artefact**. §12's only honest-exit
scenario fires *inside* `AppendCorrection` (`plan_engine.go:2678`, `planCannotProgress` →
`failPlanLocked`), i.e. only when a correction is successfully applied — so limb (c)'s second
disjunct describes a state the engine has no way to recognise.

**Impact.** US-4 AS6 (P0), the adjudication-unavailable notice, NFR-4 and test #36 are
unimplementable. The realistic implementation is a false green: test #36 injects a provider error
into a synchronous test double and passes, while in production PlanSupervisor's provider goes down,
every parked plan stays parked, and nobody is ever told — the exact failure US-5 exists to prevent.

**Recommended fix**: add an FR specifying the seam, e.g. *"On issuing a supervision wake the engine
MUST record `supervision_wake_at` on the plan and arm a deadline of `<AMB-4 value>`. On the first
tick after `supervision_wake_at + deadline` at which the plan is still at `awaiting_supervision`
with an unchanged unmet signature, the engine MUST execute FR-019."* That single mechanism covers
limbs (b) and (c). Then either specify how a provider error reaches the engine, or delete limb (a).
Note this needs the same durable per-plan field round 1's **M-03** already requires — specify them
together, in §18 step 1, as one contract change.

---

#### C2-02 — One wake per park, and a rejected correction changes nothing: the first bad tool call strands the plan permanently, and an un-correctable plan has no exit at all

**Lens**: Incompleteness / Incorrectness · **Sections**: §14 FR-023, FR-030, FR-034; §12 *Repeated
ticks do not produce repeated supervision wakes*; §16 SC-003; §7 US-1 "Definition of correctable",
US-1 AS4; §9 (no row covers this)

Three verified facts compose into a dead end.

1. **The wake is once per park.** FR-023 dedups on the persisted unmet signature; the BDD scenario
   is explicit — *"the engine ticks ten more times with no member state change → **exactly one**
   supervision wake has been delivered in total"*.
2. **A rejected correction mutates nothing.** FR-030 rejects a bare `supersede` "before any
   mutation"; test #45 asserts no revision entry; the phase and the unmet signature are untouched.
   Verified: `validateCorrection` (`plan_engine.go:2693-2712`) returns before `AppendCorrection`
   writes anything.
3. **A rejected correction consumes no budget.** FR-034 forbids the correction path from
   incrementing `JudgeRounds`, and no re-judge is provoked (nothing changed). Verified sole
   incrementer at `:1495`.

Therefore: PlanSupervisor is woken once. If its turn emits a bare `supersede` (rejected by FR-030),
a `targeted_retry` on a `done` member (rejected by `validateMemberRef`, `:2730-2733`), a malformed
tool call, or **no tool call at all**, the plan returns to *precisely the state that produced no new
wake*. No re-wake. No round charged. No terminal transition. No timeout (C2-01). The plan sits at
`awaiting_supervision` until idle expiry — the "silently stuck" outcome the spec calls the whole
difference between success and failure.

**This also falsifies SC-003.** *"A plan whose only defect is one unmet criterion does not reach
`done` after **20** consecutive bare-`supersede` attempts (**the default round ceiling**)."* Twenty
attempts require twenty wakes the design prevents, and rejected corrections consume none of the
ceiling SC-003 invokes. Round 1's **m-02** identified that SC-003 measures a path that consumes
nothing; this is the reason why, and the consequence is far larger than a weak criterion.

**And an un-correctable plan has no exit.** US-1's "Definition of correctable" states: *"A plan with
no such target is **not** correctable and MUST take the honest-exit path."* The only honest exit in
the spec is `planCannotProgress` **inside** `AppendCorrection` — reachable only when a correction is
successfully applied. A supervisor that correctly concludes "nothing can fix this" and applies no
correction triggers nothing. FR-008 denies it every write tool, and `plan_correct` has no
give-up verb.

**Impact.** SC-001 — the headline capability — holds only when the LLM emits a valid correction on
its first and only attempt. Every other outcome, including the *correct* diagnosis that a plan is
unsalvageable, is a permanently parked plan: the status quo, now with an extra agent and an LLM turn
billed for it.

**Recommended fix**: add an FR for the post-turn state machine, covering three transitions the spec
currently has none of:
- a **rejected** `plan_correct` MUST re-arm the supervision wake, bounded by an explicit attempt
  counter (**not** `PlanJudgeMaxRounds` — rejections consume no rounds, which is why SC-003 is
  incoherent today);
- exhausting that counter MUST terminate the plan with a handover distinct from the three in FR-035;
- an un-correctable plan MUST have a reachable terminal path — either a fourth `plan_correct` verb
  (`abandon`, carrying the falsified assumption) or an engine-side transition on C2-01's deadline.

Then restate SC-003 against whichever bound actually applies, and add an E-row for "PlanSupervisor's
turn produced no tool call".

---

#### C2-03 — FR-012 re-targets the MET-synthesis wake to PlanSupervisor, so a plan that *succeeds* notifies nobody

**Lens**: Incorrectness / Incompleteness · **Sections**: §14 FR-012, §8 Primary flows, §7 US-4 AS1,
§2 S4, §4.2 `wakeOwner` impact row, §13.2 tests #31/#32

Verified `wakeOwner`'s five call sites and their source kinds:

| Site | Function | Source kind | FR-012 target |
|---|---|---|---|
| `:1254` | `surfaceStallIfAny` | `plan_stalled` | PlanSupervisor |
| `:1542` | `applyJudgeRoundOutcomeLocked` (UNMET) | `plan_judge_unmet` | PlanSupervisor |
| `:1571` | `synthesizeAndComplete` (**DoD MET**) | `plan_judge_met` | **PlanSupervisor** |
| `:1610` | `failPlanLocked` | `plan_<reason>` | Owner |
| `:1742` | `StopPlan` | `plan_stopped_by_user` | Owner |

FR-012 assigns the first three to PlanSupervisor and calls `:1610`/`:1742` "the two outcome wakes".
But `:1610` fires only on **failure** and `:1742` only on **user stop**. **Neither fires when a plan
completes successfully** — that path is `synthesizeAndComplete` at `:1571`, which FR-012 has just
re-targeted away from the Owner.

US-4 AS1 asserts the opposite: *"Given **any** plan, When it reaches a terminal state, Then its
`owner_agent_id` is woken over the bus with the handover text."* `done` is a terminal state.

§8 papers the gap over with *"that synthesis becomes the Owner's success notification"* — but FR-008
denies PlanSupervisor every write tool, `plan_correct` carries no synthesis field, and **no FR, BDD
scenario, test or success criterion anywhere in the spec describes how PlanSupervisor's synthesis
text reaches the Owner, the plan record, or the notification store.** Test #32 asserts only that
"the two outcome wakes address `owner_agent_id`" — counting two, which confirms the success path is
not among them. Dataset C's ten rows are all agnostic about which terminal path fired, so none
catches it.

**Impact.** The most common good outcome — a plan finishing successfully — silently stops notifying
the agent that owns it. Because FR-014(b)'s human notice hangs off the same outcome delivery, the
human who authored the plan also stops being told, which is the specific gap US-4 exists to close
(§7 US-4: *"the gap is that when a human authored the plan, nothing tells that human it finished"*).
Holdout **H3** fails. This is a regression the feature introduces, inside the story written to
prevent it.

**Recommended fix**: decide explicitly and state it. Either (a) leave `:1571` on the Owner and give
PlanSupervisor a separate synthesis commission with its own wake; or (b) keep the re-target and add
a **third** outcome wake fired after PlanSupervisor's synthesis lands, specifying how the synthesis
text is captured and attached to the plan. Add a BDD scenario *A plan that reaches `done` notifies
its owner agent and its human author*, a test, and a Dataset C row whose terminal path is `done`.

---

#### C2-04 — The `plan_correct` tool's input schema is never specified, and `TailEdges` appears in no dataset, scenario, edge case or test

**Lens**: Incompleteness / Infeasibility / Insecurity · **Sections**: §14 FR-004, §11.6, §13.3
Dataset A, §19, §9 E5, Dataset A3/A15/A16

FR-004 names three verbs and describes `Execute` building a `CorrectionCaller` from
`tools.ToolAgentID(ctx)`. It never gives the **parameter schema** — the JSON an LLM emits. Dataset A
implies `verb`, `superseded_member_id`, `retried_member_id`, `tail_members`. §19 reveals the rest:
`CorrectionRequest` carries `TailMembers []task.Task` **and `TailEdges []IntentEdge`**, and cites
this as the reason a *human* cannot use the API — without ever asking whether an *LLM* can.

Four unaddressed consequences, all verified against the code:

1. **`TailEdges` is invisible to the entire test plan.** It appears in no dataset column, no BDD
   scenario, no edge case and no test. Edges are how a new member is sequenced into the DAG. An
   LLM-authored edge set can introduce a **cycle**, which the dispatcher cannot resolve, and
   **nothing in the spec requires cycle validation.** It can also name a nonexistent member, or a
   superseded one. Round 1 marked dataset coverage **PASS**; this is the falsification.
2. **Member IDs are LLM-supplied, and a collision is silent data loss.** E5/A15 classify a tail
   member whose id already exists as *"Applied idempotently, no duplicate task"* — verified at
   `buildCorrectionApplyFunc` (`:2781-2798`), which skips existing tasks with no verb check. For
   intent-log **replay** that is correct. For an LLM that reuses an id it just read off the plan, the
   member is **never created**, the correction reports success, and the plan proceeds believing the
   work was added. The spec classifies the bug as a feature because it never distinguishes replay
   from first application.
3. **No size bound is a requirement.** A3 asserts 50 members "Applied"; no FR caps
   `len(TailMembers)`, `len(TailEdges)` or payload bytes. This is the payload that holds the
   process-wide `planDecisionMu` for its whole body (`:2575-2576`) — round 1's Unasked Question 2.
4. **`TailMembers` on the wrong verb is currently legal and unspecified.** Verified `:2621`:
   `Members: req.TailMembers` is set **unconditionally** inside the `IntentRecord`, and
   `validateCorrection` constrains `TailMembers` only on `append` (`:2710-2712`). So a
   `targeted_retry` carrying 50 tail members creates all 50. FR-030 adds a constraint for
   `supersede`; nothing covers `targeted_retry`, and the spec's verb model does not contemplate it.

Also untestable as written: **A16** expects a 10 KB title to be *"Applied **or** rejected by the
existing task validator — never a panic"*. "Applied or rejected" is not an expectation; the spec does
not know which, and neither will the test.

**Impact.** The most load-bearing interface in the feature will be invented at implementation time. A
cyclic edge set deadlocks the plan (and, per C2-02, deadlocks it *permanently* since no re-wake
follows). A colliding member id silently drops work and can flip a DoD verdict to MET — the false
success US-3 calls worse than a stuck plan.

**Recommended fix**: add an FR giving the tool's parameter schema field by field, and state whether
the LLM supplies member ids or the engine mints them (**strongly prefer engine-minted** — it retires
consequence 2 entirely). Add MUSTs to `validateCorrection`: reject a `TailEdges` set introducing a
cycle; reject an edge naming an unknown or superseded member; reject a first-application
`tail_members` id that already exists (keep the skip for replay only, distinguished explicitly); cap
`len(TailMembers)` and `len(TailEdges)`; reject `TailMembers` on `targeted_retry`. Add Dataset A rows
A18–A22 for each, and resolve A16 to a single expected outcome.

---

### MAJOR

---

#### M2-01 — The stall note is never cleared once a plan parks, so PlanSupervisor adjudicates on a stale diagnosis

**Lens**: Incorrectness · **Sections**: §9 E21, §14 FR-012, §13.2 test #59, §11.1 "Data in"

E21 asserts the parked and stalled conditions cannot co-occur because *"`surfaceStallIfAny` refuses
to touch `PlanPhase` while the parked phase holds"*. Verified at `plan_engine.go:1225-1230` — the
guard returns before any `planStore.Update`, so the phase claim is correct.

**But the stall-note *clearing* branch (`:1234-1241`) sits behind the same guard.** A plan that
carried a stall note and then entered `awaiting_supervision` keeps that note indefinitely — and
§11.1 lists "the plan record" as PlanSupervisor's input. The spec asserts mutual exclusion of
*phases* and reads it as mutual exclusion of *state*, which it is not.

FR-005 requires the SOUL to discriminate the stall wake from the UNMET wake, and RISK-9 concedes the
prompt is the only control for that. Feeding it a stale stall diagnosis alongside a DoD-unmet wake
is the input most likely to produce the wrong verb.

**Recommended fix**: add to FR-012 or FR-023: *"on entering `awaiting_supervision` the engine MUST
clear any stall note carried from a prior stall"*, extend test #59 to assert the note is absent, and
correct E21's rationale — it currently claims more than the guard delivers.

---

#### M2-02 — A corrupt intent log silently un-supersedes members across a restart, flipping the evidence set the judge weighs

**Lens**: Incompleteness / Incorrectness · **Sections**: §7 US-6, §12 *The revision history survives
a restart*, §13.2 test #44, §13.4 R1.7, NFR-5

Verified at `plan_engine.go:3105-3127`: `reconstructCorrections` (called from `bootReconcile` at
`:3179`) iterates each plan's intent-log JSONL and, on a per-plan `List` error, **`continue`s
silently**. The in-memory superseded-member set is rebuilt entirely from those entries.

So a truncated or corrupted intent log causes previously-superseded members to be silently
**un**-superseded at the next boot. R1.7 asserts only the clean replay (*"Replayed at boot into the
superseded set | Identical"*); test #44 asserts the history is "byte-identical to the pre-restart
read"; no dataset row covers a damaged log.

**Impact.** A restart after a partial write re-admits discounted evidence to the plan judge, which
can flip a DoD verdict to MET — the false-success outcome US-3 is written to prevent, reached by a
path US-3 does not consider. It is silent: no error surfaces, and the round-1 finding that no read
surface for revisions exists (**M-07**/FR-037) means nobody would notice.

**Recommended fix**: add an FR requiring an unreadable or malformed intent log to be surfaced
(ERROR plus the durable plan field M-03/C2-01 already require) and to fail the plan closed rather
than proceeding with an incomplete superseded set. Add a dataset row for a truncated intent-log
JSONL and a test `TestReconstructCorrections_CorruptLogIsSurfacedNotSwallowed`.

---

#### M2-03 — The identity gate is exact string equality on `plansupervisor`, and nothing in the spec reserves that id

**Lens**: Insecurity (Spoofing / Elevation of Privilege) · **Sections**: §14 FR-009, NFR-3, §13.3
Dataset B (B8/B9), §7 US-2, §6 N2

FR-009 requires `requireOwner` to admit PlanSupervisor "by **exact agent identity**", and Dataset B
correctly tests case (B8) and trailing-whitespace (B9) variants. FR-008 then designates the **engine
identity gate**, not the policy layer, as the *primary* control — because N2 proves the policy layer
is inert for agents persisted before the tool name existed.

NFR-3 requires the threat model to "include user-created agents", but considers only policy
resolution. It never asks whether a user, or an agent holding `create_agent`, can **create or rename
an agent whose id is `plansupervisor`**. A search of the agent create/update path in
`pkg/gateway/rest.go` surfaced no reserved-id check against `coreagent.systemAgentIDs`.

**Impact.** If no reservation exists, the entire integrity property of US-2 (P0) reduces to "nobody
picked that name". On an upgraded install the same agent would also pass the policy layer (N2), so
both controls fall to one string.

**Recommended fix**: add an FR — *"agent create and update MUST reject any id in `systemAgentIDs`,
or any id normalising to one, with a 400"* — plus Dataset B row B10 (operator creates an agent with
id `plansupervisor` → the **create** is rejected) and a test. If the create path already reserves
these ids, cite the file:line in §4.1 so the property is on the record rather than assumed; FR-009's
primacy depends on it.

---

#### M2-04 — Widening the notification `type` enum takes down the entire notification list, not one row — and the AsyncAPI copy uses a different field name the spec never mentions

**Lens**: Incorrectness / Inoperability · **Sections**: §14 FR-017, §11.3, §18 step 1

§11.3 correctly notes the SPA "throws `ApiSchemaError` on an unknown value". Three verified details
it omits, all material to the ordering FR-017 mandates:

1. **The REST failure is total, not per-row.** `src/lib/api.ts:838-847` `safeParse`s the **whole
   response body** against `NotificationListSchema` (`schemas.ts:2856`, `type: z.literal("schedule_failed")`).
   One unknown `type` makes the entire notification list throw — **every** notification disappears,
   not just the unknown one.
2. **The WS path fails differently and silently.** `src/lib/ws.ts:240-254` `parseFrameSafe` returns
   `null` and drops the frame with a dev-only toast (`_asyncapi-zod-schemas.generated.ts:434`,
   `notification_type: z.literal("schedule_failed")`). Two consumers, two unlike failure modes; the
   spec describes one.
3. **The AsyncAPI schema is an independent hand-maintained copy with a renamed field.**
   `contracts/asyncapi.yaml:2547-2592` carries its own `NotificationFrame` in which the event class
   is **`notification_type`**, not `type`, normalised by hand at `src/store/notifications.ts:47-52`.
   FR-017 says *"both gain the new `type` values"* — naming a field that does not exist in one of
   the two files, and never mentioning the normaliser that also needs the new values.

Bonus defect the spec is well-placed to fix: `contracts/components/schemas/Notification.yaml:22`
describes `type` as *"The event class. **Extensible; consumers must tolerate unknown values.**"*
while the enum is closed (`:18-22`) under `additionalProperties: false` (`:14`) and **neither**
consumer tolerates one. The description is actively false and will mislead the next author.

**Impact.** FR-017's ordering is correct and the spec never states the blast radius that makes it
non-negotiable. If the widening slips relative to the emitter — or a user holds a cached SPA build —
the notification centre goes completely blank rather than degrading.

**Recommended fix**: state failure modes 1 and 2 in §11.3. Extend FR-017's scope to name
`asyncapi.yaml`'s `NotificationFrame.notification_type` **and** `src/store/notifications.ts`'s
normaliser, and require `Notification.yaml:22`'s false tolerance sentence to be corrected in the same
commit.

---

#### M2-05 — PlanSupervisor is granted `read_file` and `list_directory` with no stated workspace, and FR-002 does not re-enforce one

**Lens**: Insecurity (Information Disclosure) · **Sections**: §14 FR-008, FR-002, NFR-2, §11.1

Round 1's **M-09** established that `inspect_session` is structurally inert for this agent. The other
two grants are the complementary problem: **live and unbounded.**

FR-008 requires `allow` for `read_file` and `list_directory`. The spec never states PlanSupervisor's
`Workspace`, and FR-002's re-enforced field list (`Type`, `Locked`, `Default`, `MemoryEnabled`, tool
policies) **does not include it** — verified that `seedSystemAgents` (`pkg/coreagent/core.go:1377-1453`)
re-enforces exactly those and leaves the rest operator-editable. So the agent's filesystem reach is
unspecified at seed time and mutable thereafter.

§11.1 enumerates PlanSupervisor's inputs — the plan record, the judge's per-criterion verdict, member
outcomes, `plan/SKILL.md`, its SOUL — **none of which require filesystem access**. The spec offers no
justification for either grant. NFR-2's guarantee is scoped narrowly to "no *write* path to the plan
record" and says nothing about read reach, which on an unconfined workspace includes `$OMNIPUS_HOME`
(`master.key`, `credentials.json`, `config.json`).

**Impact.** The most privileged autonomous agent in the install reads arbitrary files with no stated
bound, in a feature whose stated integrity property is that this agent is tightly constrained. Round
1's **M-08** already notes 69 of 83 tools are unconstrained by any criterion; these two are
constrained *to `allow`* with no rationale.

**Recommended fix**: justify both grants against §11.1's input list or drop them. If kept, add
`Workspace` to FR-002's re-enforced set with a stated value, and extend test #4 to assert the
*effective reach* (a denied read outside the workspace), not just the policy string.

---

#### M2-06 — Six BDD scenarios trace to acceptance scenarios that do not describe them, so six real behaviours have no acceptance criterion

**Lens**: Inconsistency (CON-02) · **Sections**: §12 `Traces to:` lines, §17 completeness check

Round 1 passed structural checks 2 and 3 on the presence of back-references. Checking their
**content** falsifies both. US-1 Acceptance Scenario 1 is *"a missing step → `append` → phase becomes
`dispatching`"*. Six scenarios name it, or a similarly unrelated AS:

| BDD scenario | `Traces to` | What that acceptance scenario actually says |
|---|---|---|
| *PlanSupervisor diagnoses a stall rather than issuing a DoD verdict* | US-1 AS1 | US-1 has **no** stall acceptance criterion at all |
| *The parked phase and the stalled phase never co-occur* | US-1 AS1 | append → dispatching |
| *A correction does not itself consume a judge round* | US-1 AS1 | append → dispatching |
| *A correction and a stop are serialised* | US-1 AS1 | append → dispatching |
| *The correction request cannot carry a DoD or an owner reassignment* | US-3 AS1 | bare-`supersede` rejection |
| *A targeted_retry of a superseded member remains impossible* | US-3 AS2 | `supersede` **with** tail members is applied |

§17's completeness check acknowledges **two** of these (*"Two scenarios serve as regression anchors"*)
and leaves four unaccounted.

The most consequential is the **stall re-route**. FR-012 changes both who is woken on a stall and
what the wake asks for — a P0-adjacent behaviour change — and **no user story states a requirement
for it**. Its only anchor is a scenario about appending a missing member.

**Recommended fix**: add US-1 Acceptance Scenario 5 — *"Given a plan whose DAG cannot progress, When
the engine surfaces the stall, Then PlanSupervisor is woken with a stall diagnosis request and not a
Definition-of-Done verdict request"* — and repoint that scenario at it. Name the remaining five in
§17 as regression anchors, or give them acceptance criteria.

---

#### M2-07 — FR-030's guarantee is a speed bump, not a structural impossibility, and NFR-2 states it as the latter

**Lens**: Incorrectness · **Sections**: §7 US-3, §15 NFR-2, §14 FR-030, §16 SC-003, §23 H2

*(Round 1 raised this substance as Unasked Question 7. It is escalated here to a finding because it
falsifies a stated NFR guarantee, not merely an open question.)*

US-3 claims FR-030's `len(TailMembers) > 0` requirement makes discounting-without-replacement
*"structurally impossible"*, and NFR-2 lists it as one of three layers guaranteeing PlanSupervisor
"cannot lower the bar it judges".

It does not. The content of `tail_members` is entirely LLM-authored and unvalidated (C2-04) — the
adjudicator supersedes the member whose output fails a criterion and attaches one trivial,
instantly-satisfiable tail member. The DoD is unchanged, but the **evidence set the judge weighs**
is, which is the mechanism US-3 says it blocks. The bypass costs one throwaway member.

SC-003 only measures the *bare* case (20 bare-`supersede` attempts), so nothing in §12–§16 detects
the paired case. H2 is the only check, and it is a manual holdout.

**Impact.** A reviewer or operator relying on "structurally impossible" will not add the
compensating control the property actually needs, in the one place the spec identifies as
security-relevant enough to move out of the prompt.

**Recommended fix**: downgrade the claim in US-3 and NFR-2 to what FR-030 delivers — *"makes a bare
discount impossible and raises the cost of a replacement; it does not prevent a low-effort one"* —
and then add a real control if the property is wanted: **a superseded member's replacement MUST
inherit the superseded member's acceptance criteria.** That is machine-checkable in
`validateCorrection` and is what "replacement work" actually means. Extend SC-003 to the paired case.

---

#### M2-08 — `OwnsPlanID` has no non-test writer, so D14 row 5 renames a field whose exemption cannot fire — and R1.5 regression-tests it as live

**Lens**: Incorrectness · **Sections**: §13.4 R1.5, §2 O4, §21 RISK-8, §3 FR-118 row, ADR-055 D14 row 5

Verified: `pkg/session/lifecycle.go:199` declares `OwnsPlanID`; **every** assignment in the repo is
in `boot_sweep_test.go` (`:218`, `:242`, `:389`) and `conformance_design_test.go` (`:1337`); the only
production read is `boot_sweep.go:160-161`. Nothing writes it outside tests.

RISK-8 concedes this in passing (*"`OwnsPlanID` has no non-test writer. Tidiness debt"*), but **R1.5**
then lists *"Lifecycle record, paused, owns an awaiting-correction plan | Preserved by exemption (b)
| Preserved by exemption (b)"* as a live regression row, and §3's FR-118 row treats the linkage as
load-bearing.

Two consequences the spec does not draw:
- **Exemption (b) is dead in production.** The *only* live protection for a parked `needs_input`
  session is `OwnerScopeKind` — verified at `boot_sweep.go:295-296`, where an empty kind returns
  `false` and the session is swept to `failed(interrupted)`, and `lifecycle.go:416-418`, where
  `persistLocked` hard-rejects an empty kind.
- **That makes D14 row 4 (`OwnerScopeKind` → `ScopeKind`) the risky rename and row 5 the free one** —
  the opposite of how §3 and AMB-1 weight them. Under the greenfield ruling the data-loss path is
  retired, but the *code* rename still has to keep `boot_sweep.go`'s single live gate intact, and the
  spec's own risk framing points at the wrong row.

**Recommended fix**: mark R1.5 as a synthetic-fixture row and state that exemption (b) is unreachable
in production. In the S9 rows table round 1's **C-04** requires, annotate row 4 as the one with a live
consumer (`boot_sweep.go:295`) and row 5 as dead-but-renamed, so the implementer tests the right one.

---

### MINOR

---

#### m2-01 — FR-018's coalescing requirement solves a duplicate the feature cannot produce

**Lens**: Overcomplexity (CPX-04) · **Sections**: §14 FR-018, §18 step 8

FR-018 requires the notification store's coalescing key to cover `plan_id` (*"today it fires only on
`ScheduleID`"*). Verified at `pkg/notifications/store.go:192-210`: the key is (recipient file) ×
(unread) × `ScheduleID`.

But FR-014(b) creates **at most one** notification per plan, at the plan's single terminal event.
There is nothing to coalesce. The requirement changes shared notification machinery — on the critical
path of the `schedule_failed` notifications that *do* coalesce — to serve a duplicate that cannot
occur.

**Recommended fix**: split FR-018. Keep the click-through routing on `plan_id` (genuinely needed —
`NotificationPanel.tsx:68-71` routes on `sessionId` then `scheduleId`, has no plan branch, and no
fallback when both are empty). Drop the coalescing change, or state the scenario producing two
notifications for one plan.

---

#### m2-02 — `validateMemberRef` checks status before plan ownership, putting another plan's id and member status into the adjudicator's context

**Lens**: Insecurity (Information Disclosure) · **Sections**: §13.3 Dataset A8, §14 FR-010, §4.1

Verified at `plan_engine.go:2730-2737`: the status-mismatch check runs **before** the
`t.PlanID != planID` ownership check. Dataset A8 expects *"Rejected: member belongs to plan X"* —
which names another plan's id.

Only PlanSupervisor reaches this code (FR-009 gates it), so this is a different and much smaller
oracle than round 1's **C-03**. But FR-010's discipline (denials must not differentiate) is applied
only to `requireOwner`, and this is the adjacent validator on the same call path.

**Recommended fix**: reorder the checks so ownership is enforced first, and change A8's expected
message to a non-naming form.

---

#### m2-03 — FR-024's retry is unbounded: no cap, no backoff, no escalation

**Lens**: Incompleteness (AMB-07) · **Sections**: §14 FR-024, §2 O7, §8 Error flows

*"A failed wake publish MUST be recorded on the plan and re-attempted on a later tick"* specifies no
maximum attempts, no backoff and no terminal escalation. O7 excludes retry/backoff for a
*PlanSupervisor* outage and says nothing about the bus. A permanently-failing notifier retries every
tick until idle expiry, writing an ERROR each time (§20 row 2).

**Recommended fix**: bound it — *"at most N attempts, then fail the plan with a distinct reason"* —
or state explicitly that unbounded per-tick retry until idle expiry is intended.

---

#### m2-04 — "Tick" is the unit SC-008 measures against and its duration is never stated

**Lens**: Ambiguity (AMB-03/AMB-06) · **Sections**: §16 SC-008, §14 FR-023, FR-024

SC-008 requires a recorded wake-failure *"within **one** tick"*; FR-024 retries *"on a later tick"*;
FR-023's boot case runs per tick. The plan-loop tick interval and its config key appear nowhere in
the spec, so SC-008 has no wall-clock meaning and FR-024's retry cadence is unknown.

**Recommended fix**: state the tick interval and its config key once in §4 and reference it.

---

#### m2-05 — FR-070 makes the P1 rename a hard prerequisite for the P0 capability

**Lens**: Inconsistency (CON-03) · **Sections**: §14 FR-070, §18 step 2, §7 US-7 vs US-1

US-1 is P0 and is the capability gap the feature exists to close. US-7 (the rename) is P1 and FR-070
makes it a **blocking prerequisite**: *"the vocabulary rename MUST precede the feature work"*, §18
step 2.

Under the greenfield ruling this is much cheaper than it was (no migrators), so this is MINOR rather
than the MAJOR it would otherwise be. It remains a stated priority inversion, and it now spans seven
D14 rows across `pkg/plan`, `pkg/session`, `pkg/tools` and the contracts — six of which the spec does
not name (round 1 **C-04b**). The only genuine coupling is `plan/SKILL.md:158` holding the phase
literal (FR-040), which is a two-line edit either way.

**Recommended fix**: either allow steps 3–9 to proceed against the current literal with the rename as
a following step, or justify the coupling explicitly. "Writing the feature twice costs more than the
coupling risk" is a claim worth arguing rather than assuming, given that step 2 is now the step whose
identifier list is least specified.

---

### OBSERVATIONS

- **O2-01 (Positive, and load-bearing)** — The two fail-closed injection precedents FR-004 cites both
  hold up on inspection: `pkg/tools/run_task.go:131-134` and `pkg/tools/plan.go:239-250` each deny
  explicitly when unwired, with the discipline documented on the field. FR-004's seam choice is sound
  and test #52 is the right guard. Worth recording because §11.6's claim (*"the exact discipline both
  precedents document verbatim"*) is one of the few in the spec that survives a hostile read intact.

- **O2-02 (Repudiation)** — AMB-6 asks whether corrections need an audit entry and guesses "skip it".
  Verified: `auditPlan` (`pkg/gateway/rest_plans.go:93`) carries six events —
  `plan.create/update/delete/approve/stop/restart` — none for correction, every one hardcoded to
  `DecisionAllow`, and none recording an actor. Granting an autonomous agent a privileged mutation
  verb whose only trace is a structured log and an intent log the round-1 review shows nobody can
  read (**M-07**/FR-037) leaves NFR-5's "attributable and reviewable" unmet. AMB-6 should be resolved
  as **yes** rather than left to the implementer's judgement.

- **O2-03 (Environment)** — `git status` on this branch shows untracked `pkg/gateway/.milestones_migrated`
  and `pkg/gateway/.planning_status_migrated`: test runs point `$OMNIPUS_HOME` at a package directory
  and leave migration sentinels in the source tree. Under the greenfield ruling this spec adds no
  third sentinel, so it is not a finding against the spec — but whoever touches `pkg/plan`'s boot path
  is well placed to add the `.gitignore` entry.

---

## 3. Structural Integrity Results (plan-spec mode) — deltas from round 1

Round 1's table stands except where noted. Three results change:

| # | Check | Round 1 | Round 2 | Why |
|---|---|---|---|---|
| 2 | Every acceptance scenario has ≥1 BDD scenario | PASS | **FAIL** | No acceptance scenario exists for the stall re-route (**M2-06**) or the MET-synthesis path (**C2-03**), both of which are behaviour changes with tests. US-4 AS4 (`plan_id` on the notification) has no scenario of its own. |
| 3 | Every BDD scenario has a `Traces to:` back-reference | PASS | **FAIL** | All 43 carry one; **six point at acceptance scenarios that do not describe them** (**M2-06**). Presence was checked, content was not. |
| 7 | Test datasets cover boundary / edge / error | PASS | **FAIL** | `TailEdges` — a first-class field of `CorrectionRequest` — appears in **no** dataset, scenario, edge case or test (**C2-04**). No row covers a cyclic edge set, a dangling edge, a corrupt intent log (**M2-02**), a per-agent `ask` policy (AMB-10), or two concurrent corrections. |

Round 1's **FAIL** on checks 9 (measurable success criteria) and 10 (cross-references) is reinforced:
SC-003 is not merely weak but **unreachable** (**C2-02**), and FR-019 has no success criterion at all
while its mechanism does not exist (**C2-01**).

---

## 4. Test Coverage Assessment — gaps additional to round 1

| Category | Gap | Affected |
|---|---|---|
| **Post-turn state machine** | No test covers a PlanSupervisor turn producing a *rejected* or *absent* correction. Every test assumes a valid correction or an authority denial. | C2-02; US-1 AS4; SC-003 |
| **Outage detection** | #36 asserts FR-019's outcome; no test exercises the detection, because none is specified. | C2-01 |
| **Success-path outcome delivery** | No test asserts the Owner is woken when a plan reaches `done`. #32 counts two outcome wakes, neither of which is the MET path. | C2-03 |
| **Tool input validation** | No test for `TailEdges` at all — no cycle, no dangling edge, no `TailMembers`-on-`targeted_retry`. | C2-04 |
| **Corrupt persisted state** | No test for a truncated intent log, a garbage `plan_phase`, or a 0-byte plan file. | M2-02; R1.7 |
| **Stale carried state** | #59 asserts the phase is not masked; nothing asserts the stall note is cleared. | M2-01 |
| **Reserved identity** | No test that an agent cannot be created with id `plansupervisor`. | M2-03 |
| **Contract blast radius** | #55 asserts the widened enum exists; nothing asserts an *unknown* value's behaviour, which is a total list failure on REST and a silent drop on WS. | M2-04 |

**Dataset additions required**: A18–A22 (`tail_edges` cycle / dangling / onto-superseded;
`tail_members` at cap+1; `TailMembers` on `targeted_retry`); B10 (operator creates an agent with id
`plansupervisor`); a Dataset C row whose terminal path is `done`; a corrupt-intent-log row; D-row for
a per-agent `ask` (AMB-10, still open).

---

## 5. STRIDE — additions to round 1's table

| Component | Threat | Note |
|---|---|---|
| `plan_correct` **input surface** | **T**, **D** | Round 1 assessed the tool's *authority*; the *payload* is unassessed. Unvalidated LLM-authored `TailEdges` (cycle → permanent deadlock, compounded by C2-02's no-re-wake) and unvalidated member ids (silent drop, can flip a verdict). No size cap on the payload that holds `planDecisionMu`. (**C2-04**) |
| PlanSupervisor **filesystem reach** | **I** | `read_file` / `list_directory` granted with no stated `Workspace`, and FR-002 does not re-enforce one. §11.1's input list requires neither. (**M2-05**) |
| PlanSupervisor **identity** | **S**, **E** | The primary control (FR-008/FR-009) is string equality against an id nothing is specified to reserve. (**M2-03**) |
| Supervision turn **liveness** | **D** | No deadline, no observer, no re-wake: a single unavailable turn parks a plan indefinitely with no signal. (**C2-01**, **C2-02**) |
| Outcome delivery, **success path** | **R** | A plan reaching `done` wakes nobody accountable; the human author is never told their plan finished. (**C2-03**) |
| Intent-log **replay** | **T** | A corrupt log silently un-supersedes members, re-admitting discounted evidence to the judge. (**M2-02**) |

---

## 6. Unasked Questions (additional to round 1's ten)

11. **How does the engine learn that PlanSupervisor's turn finished, succeeded, or failed?** Every
    limb of FR-019 depends on it; §11.2 states the wake is fire-and-forget. (**C2-01**)
12. **What happens after a turn that emits no valid correction?** No re-wake, no round charged, no
    terminal transition, no timeout. (**C2-02**)
13. **How does a plan correctly diagnosed as un-correctable terminate?** US-1 mandates an honest exit;
    the only one implemented fires inside a *successful* correction. (**C2-02**)
14. **Who wakes the Owner when a plan succeeds?** FR-012 gives `:1571` to PlanSupervisor and leaves
    the Owner only `failPlanLocked` and `StopPlan`. (**C2-03**)
15. **What is `plan_correct`'s parameter schema, and does the LLM or the engine mint member ids?**
    `TailEdges` is mentioned once, in §19, as an argument about *humans*. (**C2-04**)
16. **Can an operator or an agent create an agent whose id is `plansupervisor`?** (**M2-03**)
17. **Can an agent influence `Plan.Owner` via `create_plan`?** If `callerID` is server-derived the
    answer is no — the spec should say so, because FR-014(b) turns that field into a notification
    routing key and an agent id byte-identical to a username delivers into that user's feed (E11/C9
    call this "harmless" without analysis).
18. **What clears the stall note when a plan parks?** (**M2-01**)
19. **What is the plan-loop tick interval?** SC-008 measures against it. (**m2-04**)

---

## 7. Verdict and Next Action

```
Verdict: BLOCK

This round: 4 CRITICAL, 8 MAJOR, 5 MINOR, 3 OBSERVATION.
Combined with round 1: 8 CRITICAL, 18 MAJOR, 11 MINOR, 6 OBSERVATION.
```

Round 1 blocked on remedies the code rejects. Round 2 blocks on something more basic: the spec
specifies who wakes PlanSupervisor and what authority it holds, but **not what happens next**. The
interval between the wake and the plan moving again contains the entire feature, and it has no
timeout (**C2-01**), no retry (**C2-02**), no exit for the un-correctable case (**C2-02**), no
defined tool payload (**C2-04**) — and the one path that *does* complete successfully now notifies
nobody (**C2-03**).

None of the four is a drafting error. Each needs a design decision the spec has not taken, and three
of them (C2-01's deadline field, C2-02's attempt counter, round 1's M-03 wake-state field) are the
same durable per-plan state, which under Constraint #8 is a `contracts/` change belonging in §18
step 1. **Take that decision once, and three CRITICALs collapse into one contract addition.**

| Must resolve before implementation | Finding |
|---|---|
| Specify the supervision observation seam + deadline; resolve AMB-4's value | **C2-01** |
| Specify the post-turn state machine: re-wake on rejection, attempt bound, reachable honest exit; restate SC-003 | **C2-02** |
| Decide who wakes the Owner on a successful `done`; add scenario, test and Dataset C row | **C2-03** |
| Specify `plan_correct`'s parameter schema incl. `TailEdges`, cycles, id minting and caps; add Dataset A18–A22 | **C2-04** |
| Add the durable per-plan field(s) to §18 step 1's contract work (shared with round 1's M-03) | C2-01, C2-02 |
| Clear the stall note on park; correct E21's rationale | **M2-01** |
| Surface a corrupt intent log instead of swallowing it | **M2-02** |
| Reserve `systemAgentIDs` on agent create/update, or cite the existing check | **M2-03** |
| Extend FR-017 to `notification_type` + the SPA normaliser; fix `Notification.yaml:22` | **M2-04** |
| Justify or drop `read_file`/`list_directory`; state and re-enforce `Workspace` | **M2-05** |
| Repair the six mis-traced BDD back-references; add the stall acceptance scenario | **M2-06** |
| Downgrade NFR-2/US-3's FR-030 claim, or add the inherit-criteria control | **M2-07** |
| Annotate D14 row 4 as the live one and row 5 as dead, in round 1's S9 table | **M2-08** |

```
Review written to: docs/internal/specs/plan-supervisor-spec-review-r2.md
(Round 1: docs/internal/specs/plan-supervisor-spec-review.md)

To address these findings, run:
  /plan-spec --revise docs/internal/specs/plan-supervisor-spec.md docs/internal/specs/plan-supervisor-spec-review-r2.md
```
