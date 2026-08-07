# Spec Grill Report — `plan-supervisor-spec.md`

**Spec reviewed**: `docs/internal/specs/plan-supervisor-spec.md` (2142 lines, Draft, 2026-07-27)
**Branch**: `feature/plan-swimlane-board` @ `0da704cb`
**Detected mode**: `plan-spec` (BDD scenarios + `FR-xxx` + traceability matrix + `SC-xxx` — full checks apply)
**Review date**: 2026-07-27
**Reviewer stance**: adversarial, read-only. No spec or ADR file was modified.

**Operator rulings applied to this review** (these postdate the spec):

1. **No data migration, anywhere. Greenfield.** Unconditional, every store. Existing on-disk data is
   expected not to load; that is accepted, not a defect. The boot-sweep hazard is unreachable, not
   mitigated. **ADR-055 D14 ships all seven rows** — the spec's AMB-1 recommendation to descope rows
   4–5 is **OVERRULED**.
2. **ADR-055 has been amended.** I read the current file, not the spec's summary of it. Confirmed
   verbatim at `docs/internal/architecture/ADR-055-plan-supervisor.md`:
   > *"**Migration — NOT required. Greenfield** `[FACT: operator, 2026-07-27]` … **No migrator ships.**
   > v3 required one, citing `pkg/task/migrate_planning_status.go`. Withdrawn."*
   and
   > *"**Therefore D14 ships in full — all seven rows, including 4 and 5**"*

Per instruction, I did **not** spend budget re-deriving the 14 ADR contradictions in §5 or the 6
findings in §6 — those are known and the ADR is the stale document. I independently re-verified
§5/§6 only where a **downstream requirement depends on them**, and I report those checks below.

---

## 1. Executive Summary

Every `[FACT]` citation load-bearing enough to matter was opened in the working tree. **The
evidence quality is a large step up from the ADR** — C2, C4, C5, C6, C10, C13, C14, N1, N1b, N2,
N3, N4, N6, N7 all check out against the code, and the two hardest structural claims (`pkg/agent`
never imports `pkg/notifications`; `AppendCorrection` / `FromSessionMessageRevisionEntry` /
`PublishSessionMessage` have zero non-test callers) are correct. The spec also correctly identified
that `bootReconcile` rehydrates the persisted unmet signature — a claim I set out to falsify and
could not.

It nevertheless fails on the same axis its predecessor did, three more times, and the failures are
the expensive kind: **remedies the code structurally rejects.**

- The **entire kill switch** — NFR-6, SC-020, User Story 8, its BDD scenario, the §20 runbook and
  holdout H7 — rests on an operator setting a tool policy on a `Locked` System Agent. That write
  path returns **403** by design, and even a hand-edit is reverted on the next boot by a
  re-enforcement the spec's own FR-002 *mandates*.
- **FR-035 / SC-017** demand three distinguishable handover messages from two causes that are the
  same predicate at the same line, using data FR-034 explicitly forbids creating.
- **SC-002 / Dataset B7** demand byte-identical denials on a path `requireOwner` never executes,
  and SC-002's arithmetic contradicts the dataset it cites.

Add the greenfield ruling and roughly **18 % of the spec becomes dead weight** (two migrators, four
BDD scenarios, five tests, a whole dataset, two success criteria, a risk, a holdout) while the
rename the spec now *owes* — D14 rows 3–7 — is specified nowhere: it names exactly one target
identifier, and three sections cross-reference an "S9 rows" table that does not exist in this
document.

| Severity | Count |
|---|---|
| **CRITICAL** | 4 |
| **MAJOR** | 10 |
| **MINOR** | 6 |
| **OBSERVATION** | 3 |
| **Total** | 23 |

### Verdict: **BLOCK**

---

## 2. Findings

### CRITICAL

---

#### C-01 — The documented kill switch is impossible twice over; US-8, NFR-6, SC-020, §20 and H7 are all unbuildable

**Lens**: Infeasibility / Incorrectness · **Sections**: §7 US-8, §14 FR-002, §15 NFR-6, §16 SC-020,
§20 "Kill switch", §23 H7, §17 rows FR-002 / FR-039 / NFR-6

§20 states, as the *only* supported control:

> *"**Kill switch (must be documented in `docs/operations/`).** Set PlanSupervisor's `plan_correct`
> policy to `deny` via Settings → the tool-policy editor. Corrections stop; plans still park; owners
> are still notified. **No redeploy, no restart.** This is the only supported way to disable
> adjudication…"*

Both halves are refuted by the tree.

**(a) The write is rejected — 403.** `updateAgentTools` (`pkg/gateway/rest.go:6773`) guards at
`:6789-6793`, verbatim:

```go
// Locked (core/system) agents cannot have their tool policy overwritten via the API.
// Use coreagent.IsCoreAgent or check the Locked flag.
if foundAgent.Locked {
    jsonErr(w, http.StatusForbidden, fmt.Sprintf("agent %q is locked and cannot be modified", agentID))
    return
}
```

FR-002 seeds PlanSupervisor `Locked=true` and re-enforces it every boot. The Settings tool-policy
editor therefore cannot reach it at all.

**(b) Even a config-file edit is reverted on the next boot — by this spec's own requirement.**
`seedSystemAgents` (`pkg/coreagent/core.go:1331`) does, on the existing-agent branch:

```go
// Re-enforce the EXACT seeded tool policy on EVERY boot (ADR-052
// R3-2: "System Agents carry exactly their seeded tool set,
// re-enforced every boot" — no longer "all-deny re-enforced").
if !toolPolicyMapsEqual(a.Tools.Builtin.Policies, policies) {
    a.Tools.Builtin.Policies = policies
    modified = true
}
```

FR-002 requires this ("MUST re-enforce all five on **every** boot"), and test #8
(`TestSeedSystemAgents_PlanSupervisorInvariantsReEnforced`) is specified to *assert* that "the
tool-policy map are repaired on boot". The spec therefore mandates the mechanism that destroys its
own kill switch, and mandates a test that would fail if the kill switch worked.

The spec knows locked agents are write-protected — FR-003 says *"`PUT /api/v1/agents/{id}` MUST
reject a disable attempt the way the Judge's is rejected"* — and still routes the kill switch
through a locked-agent write.

**Blast radius**: US-8 (entire story, all 3 acceptance scenarios), NFR-6, SC-020, BDD *"An operator
disables adjudication without a redeploy"*, §20's runbook step 2, the `docs/operations/`
documentation deliverable, and holdout H7. NFR-6 is the spec's only operability requirement.

**Recommended fix**: pick a control the code can express, and specify it end to end. The in-repo
precedent is `gateway.preview_enabled` (live, read per-request, no restart, ADR-044). Add
`planning.supervision_enabled bool` to `config.PlanningConfig` (`pkg/config/planning.go`), read it
in `processPlan` before the parked-phase wake, add it to the contracts in §18 step 1, give it its
own FR, and rewrite SC-020 to assert it. Then delete the tool-policy kill-switch claim from §20,
NFR-6, US-8 and H7. *Do not* attempt to carve a `Locked` exemption: it would have to be carved in
**two** places (`updateAgentTools` and `seedSystemAgents`) and would weaken the System-Agent
invariant for every future System Agent.

---

#### C-02 — FR-035 / SC-017 require three distinct messages from two indistinguishable causes, using a field FR-034 forbids

**Lens**: Infeasibility · **Sections**: §14 FR-034, §14 FR-035, §16 SC-017, §12 Scenario Outline
*"The three `judge_rounds_exhausted` causes…"*, §13.2 test #62, §21 RISK-6

FR-035 asserts three causes; the tree has two distinguishable ones.

Causes **1** ("the judge round ceiling was reached with the DoD still unmet") and **2**
("corrections consumed the shared budget") are *the same predicate at the same line*:

```go
// pkg/agent/plan_engine.go:1288-1291  (beginPlanJudgeRound)
if p.JudgeRounds >= maxRounds {
    pe.failPlanLocked(p.ID, plan.FailedReasonJudgeRoundsExhausted, buildPlanRoundsExhaustedHandover(p, maxRounds))
    return
}
```

and the builder's entire input is `(p, maxRounds)`:

```go
// pkg/agent/plan_engine.go:2161
func buildPlanRoundsExhaustedHandover(p *plan.Plan, maxRounds int) string {
```

Nothing in the plan record records how many of `p.JudgeRounds` were provoked by a correction — and
**FR-034 forbids creating it**: *"Corrections MUST consume the **existing** round budget … The
correction itself MUST NOT increment `JudgeRounds`"*, plus §10's *"The system must **not** add a
second round budget or a second exhaustion path."* Without a correction counter the two causes are
not merely hard to tell apart — they are the same event.

Meanwhile cause **3** is already done: `buildUnreachableDoDHandover` (`plan_engine.go:2892`) already
produces a message that opens *"Plan %q cannot reach its Definition of Done: after the latest
correction and auto-reset, no member can make further progress…"* and is already wired at `:2680`.
So FR-035 simultaneously **overstates** the work for cause 3 and **understates** the impossibility
for cause 2.

SC-017 ("**three** distinct handover strings, asserted by exact match"), test #62
(`TestThreeExhaustionHandoverMessagesAreDistinct` — "exact-match assertion on all three strings")
and the 3-row Scenario Outline cannot pass.

**Recommended fix**: choose one.
- **Drop to two.** Merge causes 1 and 2 (they are the same operator-visible condition: "the shared
  budget ran out"), amend RISK-6, rewrite SC-017 as two strings, and reduce the Scenario Outline to
  two rows. Cheapest, and honest.
- **Or** add `Plan.CorrectionRounds int` contracts-first (`Plan.yaml` is
  `additionalProperties: false` at `:28`, so it is a step-1 change under §18), increment it in
  `AppendCorrection`, branch `buildPlanRoundsExhaustedHandover` on it, and **amend FR-034 and §10**
  to permit the counter. Note this reopens the "second budget" question FR-034 closed.

---

#### C-03 — Denial indistinguishability is specified only for `requireOwner`, but every criterion demands it for a path `requireOwner` never reaches — and SC-002's own arithmetic is wrong

**Lens**: Insecurity (Information Disclosure) / Inconsistency · **Sections**: §14 FR-010, §16 SC-002,
§13.3 Dataset B (B7), §12 *"Denials are indistinguishable and leak no plan state"*, §13.2 test #17

FR-010 is scoped to `requireOwner`: *"Every **`requireOwner`** denial MUST return one identical
wrapped `ErrCorrectionNotOwner` message across all branches."* That is achievable — all three
branches are at `plan_engine.go:2754-2772` and all three already wrap `ErrCorrectionNotOwner`.

But `requireOwner` is **not the first gate**. `AppendCorrection` loads the plan first:

```go
// pkg/agent/plan_engine.go:2578-2585
p, err := pe.planStore.Get(planID)
if err != nil {
    return nil, fmt.Errorf("plan_engine: AppendCorrection: get plan %q: %w", planID, err)
}
// Owner-authority gate (sec-MAJOR-2): only the plan's owner may correct it.
if err := pe.requireOwner(caller, p, planID); err != nil {
```

A nonexistent plan returns a **different error class** — a store error, not the wrapped sentinel —
**before** `requireOwner` runs. That is a perfect existence oracle, and it is exactly the leak the
whole story exists to close.

Three artefacts require the fix and **no FR mandates it**:
- BDD *"Denials are indistinguishable…"*: *"When it calls `plan_correct` against a parked plan, a
  running-but-unparked plan, **and a plan id that does not exist** … Then all three responses carry
  the identical message and error class."*
- Dataset **B7**: *"plan does not exist … Denied with the **identical** message as B3–B6."*
- SC-002: *"all 8 denial responses are **byte-identical**."*

Test #17 is scoped to `requireOwner`, so it cannot catch this either.

Three further defects in the same cluster:

1. **Byte-identity is unachievable as written.** Every denial embeds the plan id —
   `fmt.Errorf("%w: plan %q", ErrCorrectionNotOwner, planID)`. Responses for *different* plan ids can
   never be byte-identical. SC-002 is unsatisfiable regardless of the fix above.
2. **SC-002 contradicts Dataset B.** SC-002: *"exactly **1** row is allowed and **8** are denied."*
   Dataset B has **two** allowed rows — B1 and B2, both caller `plansupervisor`, both marked
   **Allowed**. Seven rows are denied. The criterion is arithmetically wrong about the dataset it
   cites by ID.
3. **B7 breaks the authorised holder.** B7's caller is `plansupervisor` — the one principal that is
   *supposed* to succeed — hitting a nonexistent plan, and the row demands it receive an authority
   denial identical to an unauthorised caller's. PlanSupervisor then cannot distinguish "I named the
   wrong plan" from "I am not permitted", which FR-019's honest-exit definition
   (*"the turn produces neither a `plan_correct` call nor an honest-exit conclusion"*) depends on.

**Recommended fix**:
- Add an FR: *"When the caller is not PlanSupervisor, a plan-load failure MUST be normalised to the
  same wrapped `ErrCorrectionNotOwner` response as an authority denial. When the caller **is**
  PlanSupervisor, the real not-found error MUST be returned."* Implement as an identity precheck
  before `pe.planStore.Get` (the check needs no plan state — it compares `caller.AgentID` to a
  constant), so the oracle closes without reordering the transactional body.
- Restate SC-002 as *"identical error class and identical message body, with the plan id removed
  from the message"*, and drop the plan id from `ErrCorrectionNotOwner`'s wrapping (log it
  server-side, as `:2761` already does).
- Correct SC-002 to **2 allowed / 7 denied**, or split B1/B2.
- Move B7 to a dedicated "authorised caller, missing plan" row with its own expected behaviour.

---

#### C-04 — Under the greenfield ruling ~18 % of the spec is dead weight, and the rename it now owes is specified nowhere

**Lens**: Incompleteness / Inconsistency (against the amended ADR-055) · **Sections**: §2 S9/S10,
§3, §4.2, §7 US-7, §9, §11.4, §12, §13.2, §13.3 Dataset E, §13.4 R1, §14 FR-060/FR-061, §16
SC-012/SC-013, §17, §18 step 2, §21 RISK-3/RISK-7, §22 AMB-1/AMB-2, §23 H6

**(a) Dead machinery to delete.** The current ADR says *"No migrator ships … Withdrawn."* The
following exist only to serve legacy data and are now dead weight:

| Artefact | Location |
|---|---|
| S10 — "Two one-shot migrators" | §2 In-scope table |
| FR-060 (plan-store migrator), FR-061 (lifecycle migrator) | §14 Group F |
| BDD *"A plan persisted with the old phase value survives the upgrade"* | §12 |
| BDD *"A session-lifecycle record persisted with the old key is not swept"* | §12 |
| BDD *"A partial migration retries cleanly on the next boot"* | §12 |
| BDD *"The migration is a no-op on the second boot"* | §12 |
| Tests #20, #21, #22, #24 | §13.2 |
| Dataset **E** (all 10 rows) | §13.3 |
| R1.1, R1.4, R1.5, R1.6 (pre-upgrade fixtures) | §13.4 |
| SC-012, SC-013 | §16 |
| US-7 acceptance scenarios 1, 2, 3 | §7 |
| E13, E14, E15, E22 | §9 |
| RISK-3, RISK-7 | §21 |
| AMB-1 (and its "descope rows 4–5" recommendation) | §22 |
| H6 "Upgrade an install that has parked plans" | §23 |
| §4.2's *"⚠ HIGHEST-RISK ITEM"* callout | §4.2 |
| §11.4's *"which is exactly why the migrators are mandatory rather than best-effort"* | §11.4 |
| §18 step 2's *"the plan-store migrator (FR-060)"* | §18 |
| Matrix rows FR-060, FR-061 | §17 |
| §3's FR-147 *"Requires the D14 migrator (FR-060/FR-061)"*, FR-193 *"which is why the migrator is mandatory"*, FR-118 *"the migrator keeps live records resolvable"* | §3 |
| §25 clarification *"Yes, with a required one-shot migrator (FR-060)"* | §25 |

RISK-7's mitigation is *"**AMB-1 recommends descoping it from this release**"* — overruled. FR-061's
body is *"**Recommendation (see AMB-1): defer this rename to its own ADR**"* — overruled. Those are
now instructions to build the wrong thing.

**(b) The rename the spec now owes is unspecified.** ADR-055 D14 ships all seven rows. The spec
names exactly **one** target identifier: `awaiting_supervision`. D14's other six targets appear
**nowhere** in the spec:

| D14 row | Before | After (per amended ADR) | Named in spec? |
|---|---|---|---|
| 1 | `awaiting_owner_correction` | `awaiting_supervision` | yes |
| 3 | `Plan.OwnerSessionID` | `SupervisionSessionID` | **no** (AMB-2 leaves it open) |
| 4 | `OwnerScopeKind` / `OwnerScopeID` | `ScopeKind` / `ScopeID` | **no** |
| 5 | `OwnsPlanID` | `SupervisedPlanID` | **no** |
| 6 | `ownerKey` | `scopeKey` | **no** |
| 7 | `ProcessSession.OwnerSessionID` | `TranscriptSessionID` | **no** |

**AMB-2 is answered by the amended ADR** ("rename only the `pkg/plan` field, or defer the row
entirely?" → row 3 ships; row 7 is the disambiguating rename for the `pkg/tools/session.go` usage
AMB-2 worries about, and it is explicitly *"free and unambiguous"*).

**(c) Dangling cross-references — structural.** Three sections cite an "S9 rows" table:
- §3 FR-193 row: *"The rename touches the exemption's field names (**S9 rows 3–5**)"*
- §3 FR-118 row: *"Renamed (**S9 row 3**), never deleted"*
- §4.2 callout: *"The phase rename (**S9 row 1**)"*

**There is no S9 rows table in this spec.** §2's S9 is a single table cell reading *"Vocabulary
correction: `awaiting_owner_correction` → `awaiting_supervision`, plus 5 further renames"*. The row
numbers silently refer to ADR-055 D14's table — a document the spec's own §5 declares unreliable.
An implementer reading only the spec cannot resolve "S9 row 3".

**Recommended fix**: delete everything in (a); add an S9 rows table to §2 reproducing all seven D14
rows with before/after identifiers, files and wire impact; convert the three dangling references to
point at it; delete AMB-1 and AMB-2 and record both as resolved in §25; and replace §11.4's
"upgrade-on-read" framing with a one-line greenfield statement (*"pre-rename records are expected
not to load; that is accepted"*). Note the phase rename then needs **no** boot hook at all, which
also removes §18 step 2's only non-mechanical work.

---

### MAJOR

---

#### M-01 — `plan/SKILL.md`'s verb table contradicts FR-030, and no FR, test or SC fixes it

**Lens**: Incorrectness · **Sections**: §14 FR-030, §14 FR-040, §16 SC-015, §13.2 test #25, §21 RISK-9

`pkg/skills/embedded/plan/SKILL.md:181`, verbatim:

> `| A done member's outcome is wrong | **SUPERSEDE** | Marks the done member's outcome ignored-by-Judge (record stays immutable). **Optionally** append a replacement tail member. |`

FR-030 makes that tail member **mandatory** and rejects a bare supersede before any mutation. FR-040
requires `SKILL.md` amendment at **`:158`** (the phase literal) and **`:231-232`** (the "no forked
Planner agent" line) — and nowhere else. §4.2's impact table *does* flag `plan/SKILL.md` verb table
(`:177-183`) as a d=2 "SHOULD be tested" dependent of `validateCorrection`, and then nothing acts on
it: no FR, no test, no success criterion.

Test #25 (`TestPlanSkillEmbeddedText_NoRetiredVocabulary`) and SC-015 assert only the **absence** of
two strings. Neither would catch the stale "Optionally".

This is RISK-9 made concrete. The verb table is PlanSupervisor's only guidance on verb selection,
and it instructs the adjudicator that the thing the engine hard-rejects is optional. Every bare
supersede it attempts fails validation, burning a supervision turn each time, with the failure text
being a validation error the SOUL never prepared it for.

**Recommended fix**: extend FR-040 to a **third** site — `SKILL.md:181` — changing "Optionally" to a
statement of the pairing rule ("must be accompanied by at least one replacement tail member"), and
extend test #25 / SC-015 to assert the embedded bytes contain the mandatory phrasing and **no**
occurrence of "Optionally append a replacement". Add `plan/SKILL.md:177-183` to §18 step 2.

---

#### M-02 — FR-005's "lazy backstop" mirrors a seam that does not exist for this agent

**Lens**: Infeasibility · **Sections**: §6 N4, §14 FR-005, §18 step 5, §13.2 test #10

FR-005: *"materialise it into PlanSupervisor's `SOUL.md` through **both** a gateway-side eager seed
(mirroring `seedJudgeEagerSoul`) **and** a lazy backstop."*

The eager half is fine — `seedJudgeEagerSoul` (`pkg/gateway/gateway.go:906`, called at `:1373`) is a
clean template. The lazy half is not. `ensureVerifierSoul` (`pkg/agent/verifier_adjudication.go:198`)
opens with:

```go
func ensureVerifierSoul(agentInst *AgentInstance) {
    if agentInst == nil || agentInst.ID != string(coreagent.IDJudge) {
        return
    }
```

and its **only** call site is `verifier_adjudication.go:860`, inside the Judge's verifier dispatch.
PlanSupervisor is woken over the **bus** into an ordinary agent turn; nothing on that path reaches
`verifier_adjudication.go`. There is no analogous hook to mirror — the backstop needs a **new** call
site in the ordinary turn/instance-construction path, which FR-005 does not name, §18 step 5 does
not name, and §4.1's symbol table does not list.

Test #10 (`TestPlanSupervisorSoulSeededAndNotOverwritten`) asserts the SOUL "materialises into
SOUL.md on first boot" — satisfied by the eager seed alone. The backstop is therefore both
unspecified and untested; its absence is undetectable.

**Recommended fix**: either (a) name the call site — the natural one is
`pkg/agent/instance.go`'s agent construction, gated on `agentID == string(coreagent.IDPlanSupervisor)`
— add it to §4.1 as a **modify** row, and add a test that constructs the instance with an absent
SOUL and asserts the backfill; or (b) drop the backstop, state that the eager gateway seed is the
sole path, and record the consequence (a `$OMNIPUS_HOME` whose `plansupervisor/SOUL.md` is deleted
while the gateway runs stays empty until restart).

---

#### M-03 — FR-023 / FR-024 need a durable per-plan wake-state field that no FR creates and Constraint #8 would gate

**Lens**: Incompleteness / Infeasibility · **Sections**: §14 FR-023, §14 FR-024, §16 SC-007, §16
SC-008, §12 *"Repeated ticks do not produce repeated supervision wakes"*, §13.2 tests #37/#41/#42,
§13.3 Dataset C8, §18 step 1

Credit first: the spec's claim that the persisted signature is honoured at boot is **correct**.
`bootReconcile` rehydrates it (`pkg/agent/plan_engine.go:3198-3199`):

```go
if plans[i].LastUnmetTerminalSignature != "" {
    pe.recordUnmetTerminalSignature(plans[i].ID, plans[i].LastUnmetTerminalSignature)
```

so FR-193 is genuinely preserved and SC-007's "`judge_rounds` unchanged" is reachable. I set out to
falsify this and could not.

The gap is the **wake** marker, not the judge marker. FR-023 says the boot re-wake is *"guarded by
the persisted unmet-terminal signature the way `surfaceStallIfAny` already dedups"*. Neither half of
that sentence holds:

- `surfaceStallIfAny` dedups on a **persisted side effect of the previous wake** —
  `if p.HandoverText == note && p.EffectivePlanPhase() == plan.PhaseStalled { return }`
  (`plan_engine.go:1246-1248`). It is a wake receipt.
- `Plan.LastUnmetTerminalSignature` (`pkg/plan/plan.go:392`) is set once at UNMET
  (`plan_engine.go:1518`) and cleared only by a correction (`:2625`). It carries **no** information
  about whether a wake was delivered, and it stays set on every subsequent tick. A `processPlan`
  case keyed on `phase == parked && signature != ""` fires on **every** tick.

Four artefacts require the missing field:
- test #42 `TestSupervisionWake_IdempotentAcrossTicks` — "exactly one supervision wake … in ten ticks"
- SC-007 — "re-woken **exactly once** across a gateway restart"
- FR-024 — "recorded **on the plan** and re-attempted on a later tick"
- SC-008 — "has a **recorded wake-failure on its record** within one tick"; Dataset C8 the same

That is a new durable field on `Plan`. `contracts/components/schemas/Plan.yaml:28` is
`additionalProperties: false`, and `pkg/api/generated/contract_test.go` "fails on any Go struct
producing schema-invalid JSON" (CLAUDE.md Constraint #8), so it is a **step-1 contracts change**.
§18 step 1 lists the phase rename, the notification widening and the `owner` description fix. It
does not list this.

**Recommended fix**: name the field (e.g. `Plan.SupervisionWakeState { last_wake_at, last_wake_error }`
or the simpler `supervision_wake_delivered_at *time.Time`), add it to `Plan.yaml` in §18 step 1, add
it to §4.1's symbol table and §4.2's impact table, rewrite FR-023's guard sentence to reference it
rather than the signature, and state the reset rule (cleared when the phase leaves
`awaiting_supervision`, so a later re-park re-wakes).

---

#### M-04 — FR-009 and FR-016 assert opposite things about PlanSupervisor's session, and neither is verified

**Lens**: Ambiguity / Incorrectness · **Sections**: §6 N5, §14 FR-009, §14 FR-016, §13.2 test #18,
§13.3 Dataset B1/B2

- **FR-016**: *"**Both actors share the synthetic `plan:<id>` `ChatID`** because it is constructed
  inside `wakeOwner` and no call site can vary it; the recipient is distinguished by `AgentID` alone."*
- **FR-009**: *"The existing `OwnerSessionID` clause MUST NOT deny PlanSupervisor — **whose session
  is not `plan:<id>`**."*

Those cannot both be casually true, and the spec never verifies either. What the tree shows:

```go
// pkg/agent/plan_engine.go:2096-2108
func (pe *PlanEngine) wakeOwner(planID, ownerAgentID, content, sourceKind string) {
    ...
    if err := pe.notifier.Notify(notifyCtx, AsyncNotifyEvent{
        Channel:    "system",
        ChatID:     "plan:" + planID,
        AgentID:    ownerAgentID,
```

`TranscriptSessionID` is **left unset**. `asyncNotifierImpl.Notify` then publishes
`ChatID: fmt.Sprintf("%s:%s", event.Channel, event.ChatID)` = `"system:plan:<id>"` with
`AsyncTranscriptSessionID: ""` (`pkg/agent/async_notifier.go:277,286`), leaving `processSystemMessage`
to resolve the session itself. **Whether two agents woken on one ChatID get one session or two is
unresolved by the spec**, and it decides:

- whether PlanSupervisor's adjudication turns are written into the Owner's persistent `plan:<id>`
  transcript — the session `ensureOwnerSessionLocked` mints at `:2469-2474` and that spec FR-118's
  boot-sweep exemption is keyed on;
- whether `requireOwner`'s clause 3 (`p.OwnerSessionID != "" && caller.SessionID != p.OwnerSessionID`,
  `:2769-2772`) is a pass-through or a denial for PlanSupervisor.

FR-009's remedy (an explicit early return for PlanSupervisor before clause 3) is **correct and
necessary** — the spec caught the real hazard. But its stated *reason* is an unverified assumption
presented as settled, and test #18 (`TestRequireOwner_SessionClauseScopedForPlanSupervisor`) pins a
behaviour whose premise nobody checked. Dataset rows B1 (`SessionID: any`) and B2 (`SessionID: any`)
encode the same unexamined assumption.

**Recommended fix**: resolve it explicitly. Trace `processSystemMessage`'s session resolution for
`system:plan:<id>` with `AsyncOriginAgentID` set and `AsyncTranscriptSessionID` empty, state the
answer in FR-016 as a `[FACT]`, and reconcile FR-009's parenthetical with it. If the two agents
**do** share `plan:<id>`, add an FR and a test for transcript separation (an adjudicator's reasoning
in the Owner's plan session is a real information-disclosure and context-pollution surface), and
re-derive whether FR-009's early return is still needed. If they do **not**, delete FR-016's "share
the ChatID" claim.

---

#### M-05 — Verified-wrong counts and citations, in a spec whose C12 criticises the ADR for exactly this

**Lens**: Incorrectness (evidence quality) · **Sections**: §4.1, §4.2, §5 C12, §14 FR-006, §18 step 3

The spec's evidence-discipline banner claims *"Every `[FACT]` in this spec was re-verified by opening
the file in the working tree at `feature/plan-swimlane-board` on 2026-07-27"*, and C12 faults ADR-055
because its *"D14 scope table occurrence counts … are roughly **double** the real figure"*. These
are wrong in the same way:

| Spec claim | Location | Actual (verified 2026-07-27) |
|---|---|---|
| *"`allStaticToolNames` … **Hardcoded 81-name literal**"* | §4.1 | **83** entries (`pkg/coreagent/core.go:295-333`). The spec's own N2 correctly quotes `pkg/config/defaults_test.go:92`'s `const wantToolCount = 83` and describes the map as matching `allStaticToolNames` "literal-for-literal" — so §4.1 contradicts §6 **and** the tree. |
| *"`tests/e2e/conformance-design-e2e.spec.ts` (**17** occ.)"* | §4.2 | **21** occurrences. |
| *"`buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:739`)"* | §4.1, §4.2, FR-006, §18 step 3 | The function is at **`:715`**. `:739` is an unrelated inner loop over four tool names inside its body. Cited four times. |
| *"validated non-empty at `:484-486`"* | §4.1, FR-013 | The validation is `:485-486`; `:484` is a closing brace. |
| *"`AppendCorrection`'s phase check (`:2597`)"* | §4.2 | The phase check is `:2591-2593`; `:2597` is a closing brace. |
| *"`ensureOwnerSessionLocked`'s persist … `:2474-2478`"* | §6 N7 | The `Update` call is `:2474`; the WARN block runs `:2475-2478`. Correct in substance. |
| *"`persistLocked` hard-rejects an empty ownership kind (`:409-419`)"* | FR-061 | The rejection is `:415-417`. Correct in substance. |

The last three are drift, not error. The first three are wrong, and the "81" would be copied into
implementation: an implementer adding `plan_correct` to a literal they believe has 81 entries and
finding 83 has been given a reason to doubt every other number in §4.

**Recommended fix**: correct all four, and add the count assertion the spec already implies —
`len(allStaticToolNames) == len(cfg.Sandbox.ToolPolicies)` — to test #2 so the number never has to
be quoted by hand again.

---

#### M-06 — FR-062's mandated sweep cannot achieve SC-011; it omits the directory holding most of the hits

**Lens**: Incompleteness · **Sections**: §14 FR-062, §16 SC-011, §18 step 2

SC-011 requires **zero** hits for `awaiting_owner_correction` repo-wide excluding `.claude/**`,
`docs/**` and `pkg/gateway/spa/`.

FR-062's mandated sweep is: *"`pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`,
`tests/e2e/**`, `**/*.yaml` prose and `**/*.md`."* It **omits `src/**`**, which currently holds 39 of
the occurrences:

```
src/lib/planStateColors.test.ts            11
src/lib/planStateColors.ts                 10
src/components/workspaces/PlansFilterBand.test.tsx   7
src/components/workspaces/WorkspaceGraphTab.test.tsx 5
src/components/workspaces/PlansFilterBand.tsx        2
src/lib/ws.new-frames-validation.test.ts             2
src/components/workspaces/WorkspaceGraphTab.tsx      1
src/lib/api/generated/…                              (regenerated)
```

FR-062's own rationale is *"The rename MUST NOT rely on the compiler alone"* — and these are exactly
the compiler-invisible cases it exists to catch: string-literal map keys in `planStateColors.ts` and
string fixtures in four `.test.*` files, none of which `tsc -b --noEmit` type-checks against the
generated enum. §13.4 even names all four test files as needing update, so the spec knows they
exist; FR-062's sweep list just doesn't cover them.

**Recommended fix**: add `src/**` (including `*.test.ts`/`*.test.tsx`) to FR-062's file list, and
restate SC-011 as the mechanical command actually run, e.g.
`rg -n 'awaiting_owner_correction' --glob '!.claude/**' --glob '!docs/**' --glob '!pkg/gateway/spa/**' .`
returning zero — so the criterion and the requirement are the same artefact.

---

#### M-07 — Seven traceability rows assert coverage the named tests do not provide, and one test is orphaned

**Lens**: Inconsistency (traceability) · **Section**: §17

The matrix's closing claim — *"Every FR and NFR above has at least one BDD scenario, at least one
test, and at least one success criterion"* — is true only in the sense that every row is populated.
Several rows are populated with artefacts that verify something else.

| Row | Traced to | Does it verify the requirement? |
|---|---|---|
| **FR-063** (SPA copy at `planStateColors.ts:213`/`:234`) | *"A long-parked plan is still reaped by idle expiry"* **(SPA sibling)**, #58 | **No.** The matrix annotates its own mismatch. FR-063 has **no** BDD scenario. |
| **FR-041** (annotate spec FR-146 in `unified-goal-plan-subagent-spec.md`) | *"PlanSupervisor's own prompt contains no retired vocabulary"*, #25, SC-015 | **No.** #25 asserts bytes of `plan/SKILL.md`. Nothing checks the annotation in the other spec. |
| **FR-052** (record that FR-133's ownership and this spec's Owner are distinct) | same scenario, #25, SC-011 | **No.** A documentation requirement traced to an embedded-bytes test and a `rg` criterion. |
| **FR-020** ("MUST NOT add member-level manual retry") | *"PlanSupervisor targets one failed member for retry"*, #28 | **No.** A negative requirement traced to a test that asserts `targeted_retry` works. |
| **FR-033** ("MUST NOT guard `updateLocked`; MUST NOT freeze `Bounds`") | *"A correction and a stop are serialised"*, #46 | **No.** #46 tests `planDecisionMu` interleaving. Neither clause of FR-033 is exercised. |
| **FR-038** ("MUST NOT implement rollback") | *"A rejected correction leaves no audit trace"*, #45 | **No.** Unrelated. |
| **FR-039** (structured log line) → **SC-020**; **NFR-6** (kill switch) → **#54** | | **Swapped.** SC-020 is the kill-switch criterion; #54 is `TestCorrectionEmitsStructuredLog`. NFR-6 has no test and SC-020 names none ("verified in an integration test"). Compounded by C-01, which shows the kill switch cannot exist. |

**Orphan**: test **#53** (`TestIdleExpiry_ReapsLongParkedPlan`) appears in **zero** matrix rows — the
only test in §13.2 that does. Its behaviour (US-8 AS-3, BDD *"A long-parked plan is still reaped by
idle expiry"*) has no owning FR and no success criterion; it survives only by being parked in
FR-063's row where it does not belong.

**Recommended fix**: add an FR owning idle-expiry preservation ("supervision MUST NOT create an
immortal plan record; the existing idle-expiry path MUST still reap a parked plan") and trace #53 to
it; give FR-063 its own BDD scenario asserting the new chip copy; retrace FR-041/FR-052 to a
documentation check or mark them explicitly untestable and drop them from the matrix's coverage
claim; retrace FR-020/FR-033/FR-038 to negative conformance assertions (e.g. `rg` that no
`updateLocked` state guard exists) or mark them as constraints rather than requirements; and swap
the FR-039 / NFR-6 rows.

---

#### M-08 — SC-004 constrains 14 of 83 tools; NFR-2's integrity guarantee covers the other 69 without evidence

**Lens**: Incompleteness / Insecurity (Elevation of Privilege) · **Sections**: §14 FR-008, §16 SC-004,
§15 NFR-2, §13.2 test #4

FR-008 and SC-004 pin `allow` for **4** tools (`plan_correct`, `read_file`, `list_directory`,
`inspect_session`) and `deny` for **10** named ones. The catalog has **83**. SC-004's phrasing —
*"`deny` for all **10** others"* — states the complement is ten tools. It is sixty-nine.

NFR-2 claims *"PlanSupervisor MUST NOT be able to lower the bar it judges … the tool grant holds no
write path to the plan record"*. That property is asserted over the whole grant but tested over 14
of 83. `delegate`, the memory tools, network/fetch tools and the task tools are unconstrained by any
criterion, and any one of them is a path to influence the evidence the judge weighs.

The good news: `denyAllThenOverride` (`pkg/coreagent/core.go:384`) makes the strong assertion
**cheaper** than the weak one — it stamps an explicit entry for every catalog name, so the complement
is trivially enumerable.

**Recommended fix**: restate SC-004 as *"`allow` for exactly the 4 named tools and `deny` for every
other name in `allStaticToolNames`"*, and have test #4 assert
`len(allowed) == 4 && len(denied) == len(allStaticToolNames)-4` against `ResolveEffectivePolicy`.
This also future-proofs it: a tool added to the catalog later can never silently land in
PlanSupervisor's allow set.

---

#### M-09 — FR-008/SC-004 grant `inspect_session`, which is structurally inert for this agent

**Lens**: Overcomplexity / Insecurity · **Sections**: §14 FR-008, §16 SC-004, §13.2 test #4

FR-008 requires PlanSupervisor's resolved policy to be `allow` for `inspect_session`. The spec's own
N2 quotes the reason it will never work (`pkg/config/defaults.go:408-414`, verified verbatim):

> *"Custom/unlisted agents are NOT deny-backfilled for this tool … Their real protection is the
> engine-set, fail-closed verifier-session scope lock (`tools.VerifierSessionScopeAllows`): **a turn
> without the scope is refused every session id regardless of policy.**"*

PlanSupervisor is not a verifier and is not dispatched through the verifier path, so it never holds
the scope. The grant can never succeed at runtime. It appears to have been copied wholesale from the
Judge's `systemAgentSeed` case (`core.go:849-855`), whose three allows are `read_file`,
`list_directory`, `inspect_session`.

Cost: it widens the seeded surface of the most privileged new agent for zero capability, and it puts
a dead entry into SC-004's assertion set — so the criterion passes while asserting something
meaningless.

**Recommended fix**: drop `inspect_session` from FR-008's allow set and from SC-004, reducing the
allow set to 3. If it *is* wanted, FR-008 must additionally specify how PlanSupervisor obtains the
verifier session scope — which would make it a verifier, contradicting FR-009's "authority is matched
on identity, not on `Type == system`".

---

#### M-10 — FR-063 / SC-019 delete copy that remains true, and specify no replacement

**Lens**: Incorrectness · **Sections**: §2 S13, §14 FR-063, §16 SC-019, §13.4

Both strings are verified present:

```
src/lib/planStateColors.ts:213  AWAITING_OWNER_CORRECTION_EXPLANATION — "…There's no in-app action for that yet — Stop this plan (■) and create a new one with the fix instead."
src/lib/planStateColors.ts:234  STALLED_EXPLANATION                  — "…There's no in-app action for that yet — Stop this plan (■) and create a new one with the fix instead."
```

FR-063 asserts *"both become false"*. They do not. That sentence is about an action available to a
**human in the UI**, and this release adds none:

- O1 deletes the REST correction route ("It has no SPA client").
- §24: *"**No SPA correction UI ships in this release**; the only correction actor is PlanSupervisor."*
- §19 defers human parity entirely.

After this feature, a human looking at a parked plan still has exactly one in-app action: Stop. The
copy is still accurate for its reader. What *has* changed is that an autonomous supervisor is now
working on it — which is worth telling the user, but that is different text, and **FR-063 does not
say what the new copy is**. SC-019 requires zero occurrences of "no in-app action" and §13.4 requires
four test files rewritten — against an unspecified string.

**Recommended fix**: specify both replacement strings in FR-063 (e.g. parked: *"A supervisor is
reviewing this plan and will correct it automatically. You can still Stop it (■)."*; stalled: keep
its own distinct wording — `planStateColors.ts:222-226` documents at length why the two must never
share copy). Add a BDD scenario for FR-063 (it currently has none — see M-07) and restate SC-019 as
an assertion on the new strings, not on the absence of the old one.

---

### MINOR

---

#### m-01 — Two internal cross-references point at the wrong FR

**Sections**: §10, §11.3

- §10: *"it must be widened explicitly in `contracts/` with the specific new values (**FR-018**)"*
- §11.3: *"**Both must be widened first** (**FR-018**)"*

The contract widening is **FR-017**. **FR-018** is notification dedup + click-through. Both
references should read FR-017.

---

#### m-02 — SC-003 measures nothing, and reintroduces the tautology the spec boasts of removing

**Sections**: §16 SC-003, §13.2 test #30

SC-003: *"A plan whose only defect is one unmet criterion does **not** reach `done` after **20**
consecutive bare-`supersede` attempts (the default round ceiling)."*

Under FR-030 a bare supersede is rejected by `validateCorrection` (`plan_engine.go:2693`) **before
any mutation**, and a rejection consumes no judge round —
`applyJudgeRoundOutcomeLocked:1495` (`newRounds := current.JudgeRounds + 1`) is the sole incrementer
and is never reached. So 20 attempts burn **zero** rounds; the "20" and the reference to the round
ceiling are decorative, and the criterion is satisfied by the same validation rule test #11 already
asserts.

§13.2 advertises test #30 (`TestPlanNeverReachesDoneViaSupersedeAlone`) as *"The behavioural
integrity test (**replaces v2's tautology**)"*. SC-003 puts one back.

**Fix**: restate SC-003 as the property that actually matters — *"a plan whose only defect is an
unmet criterion never reaches `done` through any sequence of corrections that adds no replacement
work"* — and have #30 drive real corrections (supersede+tail where the tail also fails) rather than
counting rejections.

---

#### m-03 — The `supersede` auto-reset side effect is undocumented

**Sections**: §9 E5, §13.3 A15, §12 *"A supersede paired with replacement work is applied"*

E5/A15 say a supersede with an existing tail-member id is *"Idempotent — `buildCorrectionApplyFunc`
skips existing tasks"*. True (`plan_engine.go:2782-2789`). But `AppendCorrection` also does, for any
verb other than `targeted_retry` (`:2666-2668`):

```go
if req.Verb != CorrectionTargetedRetry {
    // append/supersede: auto-reset ALL live-round failed members
    // (excludes frozen/done members).
    pe.autoResetLiveRoundFailedMembers(planID, tasks)
```

So a supersede resets **every** live-round failed member in the plan, not just the superseded one.
Nothing in §8, §9, §12 or Dataset A mentions it, and the BDD scenario *"A supersede paired with
replacement work is applied"* asserts only `M` superseded and `R` at `next`. A reviewer reading only
the spec would not expect unrelated failed members to change status.

**Fix**: add the side effect to §8's primary flows and to the supersede BDD scenario's Then clauses;
add a Dataset A row with a co-existing failed member.

---

#### m-04 — AMB-9's "provably always equal" claim sits against a contract that documents them differently

**Sections**: §22 AMB-9, §14 FR-013, §18 step 1

AMB-9: *"`Plan.Owner` or `Plan.CreatedBy` … are written the **same value on both write paths** and
are provably always equal today."*

The contract disagrees about what they mean:

```yaml
# contracts/components/schemas/Plan.yaml:244-250
owner:      description: Username of the user who created this plan. …  readOnly: true
# :252-256
created_by: description: Username (or agent ID) that created the plan. …
```

FR-014 routes the human notification on **`Owner`** — the field the contract says is a username —
while `created_by` is the one already documented as dual-kind. FR-013 concedes the description needs
fixing (*"The spec SHOULD still correct that description"*), and §18 step 1 lists only *"the
`Plan.yaml` `owner` description fix"*. If the two are genuinely always equal, the routing choice is
arbitrary and should be justified; if they can diverge (AMB-9 also notes `CreatedBy` "drives the
tiered-DoD gate"), the choice is load-bearing and unproven.

**Fix**: state the equality as a verified `[FACT]` with both write-path citations, or route on
`created_by` and say why.

---

#### m-05 — E10 and Dataset C8 disagree about whether a dangling `owner_agent_id` is reachable

**Sections**: §9 E10, §13.3 C8

E10: *"`owner_agent_id` names an agent that was deleted | **Cannot normally occur** —
`HasActivePlansOwnedBy` blocks the delete (E8)."*

Verified: the guard is `pkg/gateway/rest.go:2660`
(`if pe := agent.GetPlanEngine(a.agentLoop); pe != nil && pe.HasActivePlansOwnedBy(id)`) — it is a
**REST-handler** guard only. A `config.json` edit, or any non-REST removal path, is unguarded.
Dataset C8 then exercises exactly that state (`owner_agent_id: ghost (agent deleted)`) and requires
FR-024's escalation to fire. The edge-case table calls it near-impossible; the dataset requires it to
work. Both are fine — but the spec should not do both silently.

**Fix**: reword E10 to *"unreachable through the REST delete path; reachable by config edit — see C8"*.

---

#### m-06 — Nothing stops a future caller reintroducing the orphan-notification bug

**Sections**: §4.1, §14 FR-014, §16 SC-010

§4.1 correctly records that `notifications.Store.Create` *"Sanitises `Recipient` into a filename;
**succeeds for an agent id nothing ever reads**"*, and FR-014 gates on the `Gateway.Users` lookup at
the one new call site — a good fix. But `Store.Create` itself stays unguarded, and SC-010's *"**zero**
notification files are written keyed on an agent id"* is enforced only at that call site. The next
caller reintroduces the exact failure `pkg/gateway/schedules.go:604-608` already documents.

**Fix**: add an FR requiring `notifications.Store.Create` to reject a recipient that does not resolve
to a configured user (or to a documented sentinel), with its own unit test — the fail-closed
discipline the rest of this spec applies everywhere else.

---

### OBSERVATIONS

- **O-01 (Overcomplexity)** — FR-016's *"If a second destination is later required, `wakeOwner` gains
  a destination parameter — not a second notifier"* is guidance for a case the spec explicitly does
  not have. It is speculative generality in a requirement; move it to §21 or drop it.

- **O-02 (Readiness)** — §22 carries **10** unresolved ambiguities on a spec with four P0 stories.
  Two are blocking in practice: **AMB-3** (the `PlanSupervisorDefaultRubric` text) is *the actual
  implementation of the adjudicator* and is unwritten — FR-005 enumerates six topics it must cover
  and RISK-9 concedes it is the only control for verb selection and stall-vs-UNMET discrimination;
  **AMB-4** (the supervision timeout) is a required input to FR-019's definition of "unavailable",
  and no success criterion measures it. AMB-1 and AMB-2 are now answered by the amended ADR (see
  C-04) and should be closed. The remainder should be resolved or explicitly deferred with a stated
  default before §18 step 1 begins.

- **O-03 (Positive)** — §23's holdout design is genuinely good: H2 ("The supervisor cannot cheat")
  and H4 ("Borrowing the supervisor's authority fails") are externally-observable, adversarial, and
  independent of the implementation's own assertions. Note **H6 is dead** under the greenfield ruling
  and **H7 tests the kill switch C-01 shows cannot exist** — both need replacing, and the holdout for
  whatever replaces the kill switch should be written before implementation so it stays a holdout.

---

## 3. Structural Integrity Results (plan-spec mode)

| # | Check | Result | Note |
|---|---|---|---|
| 1 | Every user story has ≥1 acceptance scenario | **PASS** | 8 stories, 34 acceptance scenarios. |
| 2 | Every acceptance scenario has ≥1 BDD scenario | **PASS** | Verified all 34. |
| 3 | Every BDD scenario has a `Traces to:` back-reference | **PASS** | All 43. |
| 4 | Every BDD scenario has a corresponding test in the TDD plan | **PASS** | 62 tests cover all 43. |
| 5 | Every functional requirement appears in the traceability matrix | **PASS** | 39 FR + 6 NFR rows. |
| 6 | Every BDD scenario appears in the traceability matrix | **PASS (nominal)** | All 43 appear — but see **M-07**: seven rows pair a requirement with an artefact that does not verify it, FR-063 has no scenario of its own, and test **#53** appears in **zero** rows. |
| 7 | Test datasets cover boundary / edge / error | **PASS** | Datasets A–D are strong (empty collection, empty string, wrong status, cross-entity, unicode, 10 KB, duplicate/replay, case sensitivity, trailing whitespace, namespace collision). Dataset E is dead under greenfield (**C-04**). |
| 8 | Regression impact explicitly addressed | **PASS** | §13.4 is the strongest section in the document — 20 rows, each naming the existing anchor test and a preserve/extend verdict. Four rows (R1.1/R1.4/R1.5/R1.6) die with greenfield. |
| 9 | Success criteria measurable, no subjective language | **FAIL** | **SC-002** arithmetic contradicts Dataset B and demands unachievable byte-identity (**C-03**); **SC-003** measures a rejection path that consumes nothing (**m-02**); **SC-004** says "all 10 others" for a 69-member complement (**M-08**); **SC-011** is unachievable via FR-062's sweep list (**M-06**); **SC-017** is infeasible (**C-02**); **SC-019** asserts the removal of a string with no specified replacement (**M-10**); **SC-020** names no test and measures a control that cannot exist (**C-01**); **SC-012/SC-013** are dead (**C-04**). |
| 10 | Cross-references resolve | **FAIL** | "S9 row 1", "S9 row 3", "S9 rows 3–5" reference a table absent from this spec (**C-04c**). §10 and §11.3 cite FR-018 for FR-017's requirement (**m-01**). |

---

## 4. Test Coverage Assessment

**What is good.** The test plan is dependency-ordered (catalog → policy → seed → gate → loop), which
is correct and non-obvious — E16 correctly identifies that `validateOverrideKeys` **panics** if the
seed names a tool the catalog lacks, making the ordering a hard requirement rather than a preference.
Levels are appropriate: policy resolution and validation at unit, the loop and wake routing at
integration, wire shape at contract, one E2E for the closed loop. The insistence on asserting against
`ResolveEffectivePolicy` rather than the seed literal (SC-004, tests #4/#5/#6) is exactly right and
is what catches N1b.

**Gaps.**

1. **No test for NFR-6 / SC-020.** SC-020 says "verified in an integration test"; §13.2 has none.
   Test #54 is the log test, wrongly traced (M-07). Compounded by C-01 — the control is unbuildable.
2. **Test #53 is orphaned** from the matrix; the idle-expiry behaviour has no owning requirement.
3. **No negative test for the tool-policy complement.** #4 asserts 14 of 83 names (M-08).
4. **No test for the prompt/engine contradiction.** #25 asserts two absences in `plan/SKILL.md`;
   nothing asserts the verb table agrees with `validateCorrection` (M-01). This is the single
   highest-value cheap test in the whole feature — the SOUL and the skill are the only controls for
   behaviour the engine cannot enforce (RISK-9).
5. **No concurrency test beyond one pair.** #46 covers correction-vs-stop. `AppendCorrection` holds
   the process-wide `planDecisionMu` for its whole body (`:2575-2576`) — the same mutex `processPlan`,
   `StopPlan`, the judge round and idle expiry take. E6/E7 name the interleavings; only one is tested.
   A correction racing a **judge round** (E6) and a correction racing **idle expiry** (§20's reaper)
   are untested.
6. **No idempotency test for the wake-state field** — because the field does not exist (M-03). #42
   asserts once-only wakes over ten ticks against a mechanism that fires every tick.
7. **Five tests and one dataset are dead** under greenfield: #20, #21, #22, #24 and Dataset E, plus
   #58's migration half (C-04).
8. **The E2E gate rests on a real LLM.** §11.1 says *"mock provider in tests; **real provider in the
   E2E gate**"*, and test #60 (`TestCorrectedPlanReachesDoneWithNoHumanInput`) is the headline SC-001
   assertion. A non-deterministic adjudicator in a blocking merge gate is a flake source the spec
   does not acknowledge. §13.1's E2E row and CLAUDE.md's `e2e` gate both apply; no retry/quarantine
   policy is stated.

---

## 5. STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| `plan_correct` tool → `AppendCorrection` | — | — | — | **✗** | **✗** | ~ | **I**: existence oracle on the plan-load path (**C-03**). **D**: holds process-wide `planDecisionMu` for the whole body; the spec identifies the rate-limit need only for the *deferred* REST route (§19), not for the agent path it is shipping — a looping PlanSupervisor can stall `processPlan`, `StopPlan`, judging and idle expiry for every plan. **E**: identity gate is correct and is correctly identified as primary (N2/FR-008). |
| `requireOwner` authority gate | ✓ | — | — | **✗** | — | ✓ | Exact-identity match (never `Type == system`) is the right call — FR-009 explicitly prevents a future System Agent inheriting correction rights. Information disclosure per C-03. |
| PlanSupervisor tool grant | — | ~ | — | — | — | **✗** | 69 of 83 tools unconstrained by any criterion while NFR-2 asserts a property over all of them (**M-08**). `inspect_session` granted but inert (**M-09**). |
| Supervision wake (bus) | **✗** | — | — | **✗** | ~ | — | `async_notifier.go:249-251` states *"no authorization happens here"* — correct by design, and §13.4 correctly requires the chat-target guards stay at the gateway. But **S/I**: if PlanSupervisor and the Owner share the `plan:<id>` session (unresolved, **M-04**), the adjudicator's reasoning lands in the Owner's transcript and either party's turn can read the other's. |
| Human notification store | — | — | — | ✓ | — | ✓ | Best-handled surface. FR-014's `Gateway.Users` gate closes the orphan-file class; C1/C5/C9/C10 cover collision, dangling and oversize. Residual: `Store.Create` unguarded for future callers (**m-06**). |
| Kill switch / operator control | **✗** | **✗** | — | — | **✗** | — | **The control does not exist** (**C-01**). There is no supported way to stop an autonomous mutating agent without a redeploy — the operability failure mode this feature most needs. |
| Vocabulary rename (D14) | — | ~ | — | — | — | — | Data-loss risk retired by the greenfield ruling. Residual: the compiler-invisible surface FR-062 under-sweeps (**M-06**). |
| Audit trail (`RevisionEntry`) | — | ~ | **✗** | — | — | — | **R**: FR-037 verified that **no read surface exists** — `FromSessionMessageRevisionEntry` has zero call sites and `PublishSessionMessage` has zero non-test callers (both re-verified). Excellent finding. But the resolution is AMB-5, unresolved, and the fallback ("return it in the tool result") means the only reader of the audit trail is the agent being audited. NFR-5's "reviewable after the fact" is not met by that. |

---

## 6. Unasked Questions

1. **How does an operator stop PlanSupervisor?** C-01 shows the stated answer is impossible. What is
   the real one, and does it survive a boot?
2. **Is `AppendCorrection` rate-limited on the agent path?** §19 identifies the `planDecisionMu`
   problem for the deferred REST route and does not apply the same reasoning to the tool it ships. A
   PlanSupervisor in a correct/re-judge/correct loop holds a process-wide lock 20 times per plan.
3. **What is the supervision turn timeout?** AMB-4 flags it and warns the likely default (the 10 s
   `wakeOwner` notify timeout) is the wrong scale for an LLM turn. FR-019's "unavailable" definition
   depends on it and nothing measures it.
4. **When PlanSupervisor and the plan's Owner are woken into the same `plan:<id>` ChatID, do they
   share a session?** M-04. It determines transcript isolation, the FR-118 exemption, and whether
   `requireOwner`'s clause 3 is a pass-through or a denial.
5. **Who reads the audit trail?** If FR-037's fallback is "return it in the tool result", the only
   consumer is PlanSupervisor itself. What surface does an *operator* use to answer "why did this
   plan change?" — the question US-6 is written around?
6. **What does the parked/stalled chip say now?** M-10. Four tests are to be rewritten against an
   unspecified string.
7. **What happens when PlanSupervisor's correction is itself wrong?** O5 excludes rollback and §21
   RISK-2 concedes adjudication quality is unmeasured. With `done` terminal and frozen, a supervisor
   that appends a passing-but-wrong tail member manufactures a false success — the exact failure US-3
   exists to prevent, via a path FR-030 does not cover (FR-030 blocks *discounting*, not *adding
   work that trivially satisfies the criterion*). H2 tests this externally; nothing in §12–§16 does.
8. **Does anything cap supervision cost?** NFR-1 bounds it at `PlanJudgeMaxRounds` (default 20) LLM
   turns *with the plan skill loaded* per plan, in the unhappy path. No SC measures it, and §22 has
   no ambiguity for it.
9. **Which of the seven D14 renames land in which step?** C-04. §18 step 2 says "the rename sweep"
   and names one identifier.
10. **What is the `PlanSupervisorDefaultRubric`?** AMB-3. It is the feature.

---

## 7. Verdict and Next Action

```
Verdict: BLOCK

4 CRITICAL, 10 MAJOR, 6 MINOR, 3 OBSERVATION.

Review written to: docs/internal/specs/plan-supervisor-spec-review.md

To address these findings, run:
  /plan-spec --revise docs/internal/specs/plan-supervisor-spec.md docs/internal/specs/plan-supervisor-spec-review.md
```

**Blocking set** — these change what gets built, not just what gets written:

| ID | One line |
|---|---|
| **C-01** | The kill switch is 403'd by the `Locked` guard and reverted by the boot re-enforcement FR-002 mandates. US-8/NFR-6/SC-020/§20/H7 are unbuildable; pick a real mechanism. |
| **C-02** | Three handover messages cannot come from two causes that are one predicate, using a counter FR-034 forbids. Drop to two, or add the field and amend FR-034. |
| **C-03** | The existence oracle lives on the plan-load path `requireOwner` never reaches; SC-002 is unachievable and its arithmetic contradicts Dataset B. |
| **C-04** | Greenfield kills two migrators, four scenarios, five tests, a dataset, two SCs, a risk and a holdout — while the rename now owed (D14 rows 3–7) names one identifier and cross-references a table that does not exist. |

**Also required before implementation**: M-01 (the skill tells the agent the opposite of what the
engine enforces), M-03 (the wake-state field nothing creates), M-04 (the session question), M-10
(unspecified replacement copy), and AMB-3 (the rubric — the feature itself).

**Two things the spec got right that are worth preserving through the revision**: the
`ResolveEffectivePolicy`-not-seed-literal discipline in SC-004 and tests #4–#6 (this is what catches
N1b, and it is the only reason the fresh-install dead-on-arrival failure is visible at all), and
§13.4's regression table, which is the most rigorous section in the document and should not be
trimmed when the migration rows are removed.
