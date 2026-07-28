# ADR-055: PlanSupervisor — a System Agent that adjudicates and corrects running plans

- **Status:** Proposed (v3 — revised after a second `/grill-spec` BLOCK, 41 findings)
- **Date:** 2026-07-27
- **Amends:** [ADR-053](ADR-053-unified-goal-plan-subagent.md) — its correction handler
  (spec FR-143) and the plan-owner wake model. **Overrides spec FR-146** (see D1).
- **Deciders:** Operator (Daniel Piatkowski); architecture input: Albert
- **Evidence level (highest used):** 1 — operator decisions + direct codebase verification

> **v3 changelog.** The second grill blocked v2 on seven criticals. The three that changed
> the design: **`plan/SKILL.md` already ships the full re-planning playbook** (diagnose →
> SUPERSEDE / TARGETED-RETRY / APPEND) *and* explicitly says *"Do not create a forked
> 'Planner' agent"* — so D1's "it needs a bespoke prompt" rationale was false and is
> rewritten; **`owner_agent_id` is `required` in two schemas and carries eight jobs**, so
> D14 no longer deletes it; and **D8's Constraint #6 claim was backwards** — the resolver
> *fails closed to deny* (`pkg/tools/compositor.go:186-190`), it does not silently grant.
> That last error came from accepting a prior review's `[FACT: grill-verified]` citation
> without opening the file; it had propagated into ADR-056 as well. v3 also adds a
> migration requirement, an authority fix for chat-addressability, and a `supersede` guard.
>
> **v2 changelog (retained).** v1 was blocked. All four CRITICALs were cases where v1 proposed
> building something that already exists, or would have broken production: the
> `Owner{Kind,ID}` rename collided with an existing **required** wire field that already
> holds a dual-kind principal; the D9/D10 immutability guard is **already implemented** at
> the REST layer; putting that guard in `updateLocked` would have **bricked the engine**;
> and the UAT causal claim was overstated. v2 is a materially smaller change on the
> supervision axis — **one gate fix, one agent, one wiring**, and no new immutability rule.
> It does, however, carry a deliberate **vocabulary correction (D14)**: v1's *invented*
> `Owner{Kind,ID}` rename is dropped, and in its place the **existing** mis-named fields are
> renamed to what they actually are, so "owner" has one meaning everywhere.

---

## 1. Problem Understanding

ADR-052/053 gave plans an autonomous loop: members dispatch off a DAG, a per-task judge
evaluates acceptance criteria, and a plan-level judge evaluates the Definition of Done.
When the DoD is unmet the plan parks at `plan_phase = awaiting_owner_correction` and the
engine wakes the plan's owner to correct it.

**The loop does not close.** `AppendCorrection` (`pkg/agent/plan_engine.go:2574`) — the
handler that applies a correction — is fully implemented and tested but has **zero
non-test callers** `[FACT]`. Nothing in `pkg/gateway/`, `pkg/tools/` or `contracts/`
references it or `CorrectionRequest`. Spec FR-143 marks it `[P2]`, so this was deferred by
design `[FACT]`.

Consequently a plan that fails its DoD has exactly one exit: stop it and re-author from
scratch, discarding every completed member. The UI copy shipped 2026-07-26 says so
literally — *"There's no in-app action for that yet."*

### What the UAT does and does not prove `[corrected in v2 — CRIT-004]`

v1 claimed the 2026-07-26 UAT result (only 2 of 11 plans reaching `done`) as direct
evidence. That was overstated:

- The same report diagnoses an **independent dispatcher defect** — `inbox`→`next` members
  never promoted — fixed on 2026-07-26, and lying in this ADR's declared out-of-scope area
  `[FACT: uat-report-round2-2026-07-26.md]`.
- The showcase case v1 quoted (all members `done`, progress 1/1, parked 15+ min) had
  **nothing to correct**; the report classifies it as *"Open — judge behaviour"*.

**Honest claim:** the correction loop being unreachable is a real, verified capability
gap. Its contribution to the observed parked-plan rate is **unquantified**, and part of
that rate is attributable to defects already fixed. This ADR is justified on the
capability gap, not on a projected completion-rate improvement.

**Blast radius:** one new System Agent; one authority-gate change; one tool + one REST
route; wake routing; plus the **D14 vocabulary rename** (7 items, wire- and disk-visible —
see D14 for per-item cost). No change to member dispatch, task-level judging, or
delegation.

---

## 2. Extracted Requirements

### Functional

- **FR-1** Task-level judging is unchanged. `[FACT: operator]`
- **FR-2** A System Agent, **PlanSupervisor**, adjudicates the plan DoD and applies
  corrections. `[FACT: operator]`
- **FR-3** PlanSupervisor is granted the plan skill **in addition to** its own
  supervision-specific SOUL. `[FACT: operator]`
- **FR-4** The **Owner** — the plan's existing `owner` field — receives outcome
  notifications and may stop / cancel / resume. It has **no** adjudication or correction
  role. `[FACT: operator]`
- **FR-5** `AppendCorrection` is wired: `append`, `supersede`, `targeted_retry` exposed to
  PlanSupervisor **via a tool**. `[FACT: operator]` **v3: the REST route is dropped from
  this ADR** — it has no SPA client, `HandlePlans` has no per-plan authorization to inherit,
  and it would hold the process-wide `planDecisionMu` unrated-limited. Human parity, if
  wanted, is its own decision with its own authz model.
- **FR-6** The correction authority gate MUST permit PlanSupervisor and MUST continue to
  deny every other non-owner agent. `[INFERENCE — v2, closes MAJ-001]`
- **FR-7** Corrections consume the **existing judge round budget**; no separate counter.
  `[FACT: operator — closes MAJ-008]`
- **FR-8** Wake routing splits: decision events → PlanSupervisor; outcome events → Owner.
  `[FACT: operator]`
- **FR-9** If PlanSupervisor is unavailable, the plan **stays parked** and the Owner is
  notified that adjudication is unavailable. `[FACT: operator]`
  **v3 — the mechanism, which v2 asserted without specifying:** (a) `processPlan`'s boot
  reconcile MUST treat the parked phase as re-wakeable, or a restart silently re-parks with
  no wake; (b) a nil notifier MUST be a startup error, not a silent no-op; (c) a failed
  publish MUST escalate, not WARN-and-continue. Without all three, "never silently stalls"
  is a claim with no enforcement.
- **FR-10** No member-level manual retry is added, for any actor. `[FACT: operator]`
- **FR-11** The existing `owner` wire field is **reused as-is** — no new principal type.
  `[FACT: operator — closes CRIT-001]` It is retained precisely *because* it already carries
  the canonical meaning; the fields that do **not** carry that meaning are renamed under
  D14.
- **FR-13** System agents MUST NOT be chat targets. `[FACT: operator — v3]`
- **FR-14** `supersede` MUST NOT be usable to satisfy a DoD by discounting failure alone —
  see D16. `[INFERENCE — v3, closes CRIT-5]`
- **FR-12** "Owner" MUST have exactly one meaning across the codebase: the principal
  accountable for a thing (creator, outcome recipient, may stop it). Anything else currently
  named `Owner*` is renamed to what it is. `[FACT: operator — D14]`

### Non-Functional

- **NFR-1 (cost)** Wake frequency stays at ~1 per plan in the happy path. `wakeOwner` has
  exactly 5 call sites, all in `plan_engine.go` `[FACT]`. Per-member adjudication is an
  explicit non-goal.
- **NFR-2 (integrity)** PlanSupervisor MUST NOT alter the DoD it judges. See the D8 caveat
  — this is contingent on its tool grant, not purely structural.
- **NFR-3 (security)** No agent other than PlanSupervisor may reach correction, and
  unauthorized callers must not be able to probe plan state via error differentiation.
- **NFR-4 (availability)** A missing, malformed or human Owner MUST NOT block adjudication
  — only outcome delivery.
- **NFR-5 (auditability)** Every correction MUST be attributable and reviewable after the
  fact. `[v2 — closes MAJ-016]`

### Constraints

- Single Go binary; pure Go; file-based storage.
- **Constraint #8:** contracts first, then `scripts/gen-contracts.sh`, artifacts committed
  atomically.
- **Constraint #6:** see D8 — v1 stated this cost **backwards**.

---

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| G1 | PlanSupervisor's exact tool grant | It is a privileged, tool-bearing autonomous actor, and a missing policy map grants `bash`/`write_file` **silently** (D8) | Judge's read-only set + correction tool + plan skill | Enumerate and security-review before seeding |
| G2 | ~~Whether the phase rename ships here or separately~~ **RESOLVED** | — | — | Closed by D6/D14: it ships in this release, as part of the vocabulary correction |
| G3 | `judge_rounds_exhausted` now covers two exhaustion causes | A user cannot tell "judge gave up" from "corrections gave up" | Reuse the reason; improve the message | MIN-007 — accept or split |
| G4 | ~~Whether `plan/SKILL.md` fits *correcting*~~ **RESOLVED** | — | — | Read: `SKILL.md:156-219` is already a complete re-planning playbook. It also forbids a forked Planner agent at `:231-232` — D1 requires amending that text |
| G5 | Are revision entries exposed on any read surface? | NFR-5 auditability | They exist but may be JSONL-only | Check the plan read path |

---

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Closes the correction loop | 35% | The verified capability gap |
| Integrity (cannot self-lower the bar) | 25% | An adjudicator that can rewrite its DoD is worthless |
| Blast radius | 20% | v1 failed here; v2 is the corrective |
| Reuses proven mechanism | 15% | Lower risk than novel machinery |
| LLM hops / token cost | 5% | Real but secondary |

---

## 5. Option Analysis

### Option A — Wire `AppendCorrection` to the existing owner agent; no new agent

| Dimension | Assessment |
|---|---|
| Strengths | Smallest change; no roster growth; satisfies spec FR-146 literally |
| Weaknesses | The owner is an arbitrary agent named at create time `[FACT: pkg/tools/plan.go:229]`; adjudication becomes a dropdown lottery |
| Risks | A general-purpose persona adjudicating a DoD it did not author |
| Complexity | Low |
| Cost | Lowest |
| Operational | None |

### Option B — PlanSupervisor System Agent, reusing the existing `owner` field *(recommended)*

| Dimension | Assessment |
|---|---|
| Strengths | Purpose-built adjudicator; second instance of the **proven** Judge System-Agent pattern; **no wire rename** in v2 |
| Weaknesses | Overrides spec FR-146 (D1); one more locked agent to maintain |
| Risks | A new autonomous decision-maker needs its own failure model (D10) and audit trail (D13); tool grant is a security surface (D8) |
| Complexity | Medium |
| Cost | One agent + one gate change + one tool/route |
| Operational | Roster gains one locked, non-chat-target agent |

### Option C — Deliver correction as a skill only (literal FR-146)

| Dimension | Assessment |
|---|---|
| Strengths | Fully spec-compliant; no roster growth |
| Weaknesses | Leaves *which agent adjudicates* unanswered — the dropdown lottery again |
| Risks | The skill is only as good as whichever persona loads it |
| Complexity | Low |
| Cost | Low |
| Operational | None |

---

## 6. Recommended Architecture

**Option B**, with FR-146 explicitly overridden.

### D1 — PlanSupervisor is a System Agent; spec FR-146 is overridden `[FACT: operator]`

Spec FR-146 `[P2]` states the re-planner *"MUST be delivered by EXTENDING `plan/SKILL.md`
— never a new Planner agent (BOM)."* v1 neither cited nor argued against it. v2 overrides
it on the operator's rationale:

> **PlanSupervisor holds tools no other agent holds, and being a fixed adjudicator is the
> point.** FR-146's concern — don't duplicate planning capability into a new agent — is
> satisfied by **granting PlanSupervisor `plan/SKILL.md`** rather than re-implementing
> planning inside it.

The *capability* is reused exactly as FR-146 intends; only the *actor* differs.

**v3 correction — the original rationale was partly false.** The operator's stated reason
was that supervising a running plan needs a *supervision-specific prompt* distinct from
greenfield authoring. It does not: `plan/SKILL.md:156-219` already ships a complete
**"Re-planning checklist (use when the DoD is UNMET)"** that diagnoses the failure and maps
it to SUPERSEDE / TARGETED-RETRY / APPEND `[FACT — read directly]`. That closes G4 and
removes half of D1's justification.

What survives, and is sufficient:

1. **The tool grant** — correction is a privileged verb that needs exactly one holder, and
   an agent is the only thing that can hold a tool.
2. **A fixed adjudicator** — the alternative is whichever agent was picked at create time,
   which is the dropdown lottery this ADR exists to remove.

**Required skill amendment.** `plan/SKILL.md:231-232` currently instructs: *"Do not create a
forked 'Planner' agent — this skill is the planning behavior. Any `create_plan`-granted
agent reuses it (BOM, FR-146)."* `[FACT — read directly]` Under FR-3 PlanSupervisor loads
this skill, and would therefore read a prompt telling it not to exist. That text MUST be
amended in the same change to carve out the supervisor explicitly. Shipping without it is a
self-contradiction the agent reads every turn.

Implementation follows the Judge verbatim: added to `SystemAgents()`, seeded `Type=system`,
locked, non-default, never a chat target, tool policy explicitly enumerated by
`systemAgentSeed`, identity/type/locked/policy re-enforced every boot; Model/Provider and
SOUL operator-editable `[FACT: pkg/coreagent/core.go]`.

```
CONFIDENCE: Medium-High
  Basis         : The mechanism is proven (Judge). The override is a deliberate, reasoned exception to an accepted [P2] MUST — recorded, not hidden.
  Evidence      : seedSystemAgents + JudgeDefaultRubric in pkg/coreagent/core.go; spec FR-146.
  Missing       : Whether plan/SKILL.md is usable for correction as opposed to authoring (G4).
  Would improve : Reading plan/SKILL.md end to end before implementation.
```

### D2 — Reuse the existing `owner` field; NO rename `[FACT: operator — closes CRIT-001]`

`Plan.Owner string` already exists (`pkg/plan/plan.go:437`), is **required** on
`contracts/components/schemas/Plan.yaml` under `additionalProperties: false`, and
**already holds the dual-kind principal** v1 proposed to introduce: the agent id on the
tool path (`pkg/tools/plan.go:286`), the username on the UI path
(`pkg/gateway/rest_plans.go:547`) `[FACT: grill-verified]`.

v1's `Owner{Kind,ID}` would have retyped a required wire property with no migration — a
one-way door — to build something that exists. **Dropped entirely.**

**Disambiguation (operator requirement).** Because both words are now in play, this ADR
fixes the vocabulary:

| Term | Meaning |
|---|---|
| **Owner** | `Plan.owner` — the human or agent that created the plan. Receives outcomes; may stop/cancel/resume. **Never adjudicates.** |
| **PlanSupervisor** | The System Agent that adjudicates the DoD and applies corrections. **Never owns a plan.** |
| `owner_agent_id` | Pre-existing **required** wire field (`Plan.yaml:23`, `PlanCreateRequest.yaml:13`, both under `additionalProperties: false`) `[FACT — verified]`. Its *decision-wake* role moves to PlanSupervisor (D5); **the field is NOT deleted or renamed by this ADR** — see D14. |

```
CONFIDENCE: High
  Basis         : Removes the ADR's only one-way door by reusing a field that already carries the required semantics.
  Evidence      : plan.go:437; Plan.yaml required `owner`; both write paths verified.
  Missing       : Whether `owner_agent_id` should eventually be deprecated.
  Would improve : A follow-up once supervision has shipped.
```

### D3 — Fix the correction authority gate `[v2 — closes MAJ-001]`

**This is the ADR's core change, and v1 got it wrong.**

`requireOwner` gates on `caller.AgentID != p.OwnerAgentID` **plus** a session check
`[FACT]`. v1's FR-9 removed only the session clause — which would have left PlanSupervisor
denied **every** correction, because its agent id is not the plan's owner agent id. The
loop would still not have closed.

v2: the gate MUST admit **PlanSupervisor** by system-agent identity, and MUST continue to
deny every other non-owner caller with the existing **opaque** denial (sec-MAJOR-2 — it
deliberately does not leak the owner id) `[FACT]`.

Per FR-4, the plan's Owner does **not** retain correction rights: correction is
PlanSupervisor's alone; the Owner stops or resumes.

Prefer matching on **identity** rather than on `Type == system`, so a future System Agent
does not silently inherit correction rights.

```
CONFIDENCE: High
  Basis         : Directly closes the defect that would have made the whole ADR inert.
  Evidence      : requireOwner's two clauses, verified; CorrectionCaller carries AgentID + SessionID.
  Missing       : Final predicate shape.
  Would improve : Choosing during /plan-spec.
```

### D4 — Corrections share the judge round budget `[FACT: operator — closes MAJ-008]`

Each correction consumes a judge round, so `judge_max_rounds` bounds both, and exhaustion
terminates through the existing `judge_rounds_exhausted` reason. No second budget, no
second exhaustion path.

Caveat (G3/MIN-007): that reason now covers two causes. The user-facing message should
distinguish them even though the enum value does not.

```
CONFIDENCE: High
  Basis         : Operator decision; reuses a configured, surfaced, already-terminal budget.
  Evidence      : judge_max_rounds is per-plan configurable via PlanBounds and drives an existing terminal reason.
  Missing       : Whether a correction costs a full round or a fraction.
  Would improve : One round per correction unless measurement says otherwise.
```

### D5 — Wake routing split; outcomes use the notification store that already exists

All five `wakeOwner` sites, classified by what the message asks for `[FACT: message bodies read]`:

| Site | Message | Kind | New target |
|---|---|---|---|
| `:1254` | "Plan is stalled" + reason | decision | **PlanSupervisor** |
| `:1542` | DoD **UNMET**; "awaiting your correction" | decision | **PlanSupervisor** |
| `:1571` | DoD **MET**; *"write a closing synthesis for the requester"* | **work** | **PlanSupervisor** |
| `:1610` | "Plan has ended (reason)" + handover | outcome | **Owner** |
| `:1742` | stop handover | outcome | **Owner** |

`:1571` is not a "good job" round — it commissions the closing synthesis, and **that
synthesis becomes the Owner's success notification**.

**Human-owner notification exists — but it is username-keyed, and that breaks the agent
case `[v3, CRIT-3]`.** `pkg/notifications/` is a complete per-user durable store with REST,
per-recipient WS push and a mounted bell. However `Notification.Recipient` is documented as
*"(username) that scopes per-user storage and the live WS push"* `[FACT — read directly]`,
and the read path is `ListForUser(user.Username)`. `Plan.Owner` holds an **agent id** on the
tool path — the majority case. So routing every outcome through this store would silently
discard notices for agent owners, no-opping FR-4, FR-9 and D10 precisely where plans are
agent-authored.

**Therefore outcome delivery forks by principal kind:**

| Owner is | Mechanism |
|---|---|
| an **agent** | bus inbound (`PublishInbound`) — the existing `wakeOwner` mechanism, unchanged |
| a **human** (username) | `pkg/notifications/` store + WS push + bell |

Neither is new machinery; the ADR's job is to route correctly rather than assume one
surface serves both. Remaining gaps in the notification store: a closed one-value `type`
enum, no `plan_id` field, `ScheduleID`-only dedup, and no engine injection point.

**`stalled` handling `[MAJ-010]`:** on a stall wake PlanSupervisor's job is *not* to
adjudicate the DoD — members may be incomplete. It diagnoses why the DAG cannot progress
and either corrects (e.g. appends a member to satisfy an unreachable dependency) or
concludes the plan cannot proceed and lets it terminate. This must be stated in its SOUL,
or a stall wake will be answered with a DoD verdict that makes no sense.

```
CONFIDENCE: High
  Basis         : Decision wakes need no new mechanism; the outcome surface exists and was verified.
  Evidence      : five wakeOwner sites; pkg/notifications/ store + WS push + UI bell.
  Missing       : The four notification-store gaps.
  Would improve : Confirming the engine can inject a notification without a new dependency edge.
```

### D6 — Parked phase: RETAINED and RENAMED in this release `[FACT: operator]`

The durable parked phase is retained: it is the on-disk record that a decision is
outstanding, so a restart mid-adjudication can re-wake PlanSupervisor.

`awaiting_owner_correction` → **`awaiting_supervision`**, **in this release**. v2 initially
deferred this on cost grounds (31 files, 197 occurrences); the operator rejected the
deferral. The reasoning: under this ADR the Owner never corrects, so the name points at the
wrong actor — and leaving it would freeze that error into a **wire enum**, where it becomes
permanent. The confusion is not hypothetical; it recurred repeatedly during authoring, in
this document and in discussion.

This is part of the wider vocabulary correction in **D14**, not a standalone rename.

Note v1's crash-safety justification leaned on boot-sweep machinery the grill found **dead
in production** (MAJ-011); retention now stands on the simpler ground that the phase is the
only durable marker that adjudication is owed.

**Also mandatory this release:** the chip copy shipped 2026-07-26 (*"there's no in-app
action for that yet"*) becomes false the moment corrections work.

```
CONFIDENCE: High
  Basis         : Operator decision after seeing the measured cost; retention is independently justified.
  Evidence      : 31 files / 197 occurrences (grep, 2026-07-27); boot-sweep exemption path found dead.
  Missing       : Whether any persisted plan on disk holds the old value.
  Would improve : A disk survey before the migration step (D14).
```

### D7 — `OwnerSessionID` / `OwnsPlanID` / `plan:<id>`: NO deletion in this ADR `[revised in v2]`

v1 proposed deleting them. The grill found this contradicts two `[P1]` MUSTs (spec FR-118,
FR-147) that v1 neither cited nor argued against (MAJ-003). Deleting published,
`[P1]`-mandated linkage as a side effect of a supervision change is out of proportion.

v2: **leave them in place.** They become unused by the new routing — tidiness debt, not a
defect. Retiring them is its own decision with its own spec impact.

```
CONFIDENCE: Medium-High
  Basis         : Reverts v1's overreach; unused-but-present costs nothing at runtime, whereas deleting P1-mandated fields needs its own ADR.
  Evidence      : OwnsPlanID has no non-test writer; spec FR-118/FR-147 are [P1] MUSTs.
  Missing       : Whether anything else still reads them.
  Would improve : A follow-up cleanup ADR once supervision has shipped.
```

### D8 — Tool grant, and Constraint #6 stated correctly `[v2 — closes MAJ-005, MAJ-006, MAJ-007]`

**v4 — the correct reading, after three attempts at one switch statement.** The resolver
(`pkg/tools/compositor.go`) has four branches, and which one fires depends on the scenario:

```go
case g == "" && a == "":  return Deny   // BOTH missing — fails closed
case g == "":             return a
case a == "":             return g      // ← agent map missing: returns the GLOBAL policy
```

The scenario that matters — a new agent with **no per-agent policy map** — hits
`case a == "": return g`. And the global defaults are `allow` for `bash`, `write_file`,
`set_config` and `create_agent` `[FACT — verified in pkg/config/defaults.go]`. So a missing
per-agent map **does** inherit permissive global policy. Only the both-missing case denies.

History, recorded because the failure mode matters more than the fact: v2 asserted this
correctly but cited the wrong file and marked it `[FACT: grill-verified]` without reading;
v3 read the file, quoted the **both-missing** branch, and "corrected" a true statement into
a false one; v4 traced which branch the actual scenario hits. **Opening the file was not
enough — the error was stopping at the first matching branch.**

Consequence for this ADR: PlanSupervisor's policy map MUST be explicitly enumerated. It is a
privileged, tool-bearing autonomous actor, and omitting its map grants it the permissive
global floor rather than denying.

The substantive requirement is unchanged and stands on its own merits:

PlanSupervisor's grant MUST therefore be explicitly enumerated in `systemAgentSeed` —
which uses a `denyAllThenOverride` shape `[FACT]` — and reviewed as a security change.
Starting point (G1): the Judge's read-only set (`read_file`, `list_directory`,
`inspect_session`), plus the correction tool, plus the plan skill. Notably **not** `bash`,
`write_file`, or any agent/config mutation.

**NFR-2 caveat `[MAJ-007]`:** v1 called DoD-immutability "structural, not a runtime check".
That holds for the *correction path* — `CorrectionRequest` has no DoD field `[FACT:
plan_engine.go:2410]` — but not for a **tool-bearing** agent in general: an agent with a
write tool could reach the DoD another way. The structural guarantee is contingent on the
grant above, which is exactly why it must be explicit.

```
CONFIDENCE: High
  Basis         : Corrects an inverted claim and names the actual failure mode, verified in compositor.go.
  Evidence      : compositor.go:178-201 permissive floor; systemAgentSeed's denyAllThenOverride.
  Missing       : The final enumerated grant (G1).
  Would improve : Security review during /plan-spec.
```

### D9 — Plan immutability: ALREADY IMPLEMENTED; document, do not rebuild `[v2 — closes CRIT-002, CRIT-003]`

v1 introduced "a started plan is immutable" as a new rule enforced in `updateLocked`. Both
halves were wrong.

**It already exists.** `pkg/gateway/rest_plans.go:717-736` already returns **409** for both
`dod` and `owner_agent_id` on any non-draft plan `[FACT — verified directly in the working
tree]`.

**And `updateLocked` is the wrong layer here.** 18 of 21 non-test writers are the *engine*
writing non-draft plans through that same function `[FACT: grill]`. A blanket non-draft
guard there would stop every plan from advancing past `approved` — it would brick the
engine.

This corrects a heuristic v1 applied too broadly: *"put invariants in the store, not the
handler"* holds when **all** writers should obey the invariant. Here the engine must be
exempt, so the **external mutation boundary (REST) is the correct place** — and it is
already guarded.

**Do not touch `Bounds`.** The same code comment records a deliberate exemption: *"an
operator may legitimately want to extend a running plan's idle-expiry/judge-round
budget"* `[FACT]`. v1 would have silently reversed it — and under D4 that budget now also
bounds corrections, making the exemption more useful, not less.

```
CONFIDENCE: High
  Basis         : Verified directly in the working tree, including the deliberate Bounds exemption and its stated rationale.
  Evidence      : rest_plans.go:714-736.
  Missing       : Nothing material.
  Would improve : n/a
```

### D10 — PlanSupervisor failure model `[FACT: operator — closes MAJ-015]`

If PlanSupervisor is unavailable (provider outage, misconfigured model, unseeded), the
plan **stays parked** and the Owner is notified that adjudication is unavailable. It never
silently stalls, and no other agent inherits adjudication — which would re-open the
integrity question FR-4 closes.

```
CONFIDENCE: High
  Basis         : Operator decision; fails safe and visible, reusing the notification surface D5 established already exists.
  Evidence      : pkg/notifications/ store + WS push + bell.
  Missing       : Retry/backoff before escalating (deliberately excluded for now).
  Would improve : Observed outage frequency.
```

### D11 — Model follows the Judge's contract `[FACT: operator]`

Operator-configurable in the UI; unconfigured, it falls back to the install default like
every other built-in agent. No special-cased tier, no new configuration surface.

```
CONFIDENCE: High
  Basis         : Operator decision; reuses the System-Agent seed contract.
  Evidence      : seedSystemAgents (Model/Provider + SOUL operator-editable; rest re-enforced each boot).
  Missing       : None material.
  Would improve : n/a
```

### D12 — Structured verdict: reuse the existing type `[v2 — closes MAJ-014]`

v1 proposed a "structured verdict + chosen verb" without noticing it would be a new wire
type. It need not be: `pkg/task/verdict.go:43` already defines a verdict type and already
carries `VerdictScopePlan` `[FACT: grill-verified]`. Extend it with the chosen correction
verb rather than introducing a parallel shape.

```
CONFIDENCE: Medium-High
  Basis         : The type exists with a plan scope already defined; extension is additive.
  Evidence      : pkg/task/verdict.go:43, VerdictScopePlan.
  Missing       : Whether the verb belongs on the verdict or on the correction record.
  Would improve : Reading verdict.go and RevisionEntry together.
```

### D13 — Auditability `[v2 — closes MAJ-016, NFR-5]`

PlanSupervisor is an autonomous decision-maker that mutates plans. Every correction MUST be
reviewable after the fact. `AppendCorrection` already produces a `RevisionEntry` and writes
an intent log `[FACT]`; this ADR requires only that those be **reachable** — surfaced on
the plan read path or the audit log — so an operator can answer *"why did this plan
change?"* without reading JSONL by hand.

No rollback is specified: corrections are additive and `done` records immutable, so the
recovery path remains stop-and-re-author.

```
CONFIDENCE: Medium
  Basis         : The data is already produced; only exposure is missing. Rollback deliberately out of scope.
  Evidence      : CorrectionResult carries RevisionEntry; intent log exists.
  Missing       : Whether revision entries are exposed anywhere readable (G5).
  Would improve : Checking the plan read path.
```

### D14 — Owner vocabulary: one definition, applied everywhere `[FACT: operator]`

**The problem.** "Owner" currently denotes **four different concepts**, and one name denotes
two different things. Surveyed 2026-07-27:

| Field | File | Actually means |
|---|---|---|
| `Plan.OwnerAgentID` | plan.go:363 | agent woken at decision points |
| `Plan.OwnerSessionID` | plan.go:403 | the synthetic `plan:<id>` chat |
| `Plan.Owner` | plan.go:437 | creator (username **or** agent id) |
| `Plan.CreatedBy` | plan.go:438 | creator — drives the tiered-DoD gate |
| `Task.Owner` / `Task.CreatedBy` | task.go:315-316 | creator |
| `LifecycleRecord.OwnerScopeKind/ID` | lifecycle.go:191-192 | **containment**, not ownership |
| `LifecycleRecord.OwnsPlanID` | lifecycle.go:199 | reciprocal link |
| `MessageInboxStore.ownerKey` | message_inbox.go | parent chat/plan id (containment) |
| `ProcessSession.OwnerSessionID` | tools/session.go | the **transcript** session |

`OwnerSessionID` is the sharpest case: in `Plan` it is the synthetic `plan:<id>`; in
`ProcessSession` it is the transcript session. Same name, different concept.

**Canonical definition (binding):**

> **Owner** = the principal accountable for a thing: who created it, who receives its
> outcomes, and who may stop it. **Nothing else.**

Everything that is not that is renamed to what it actually is.

**Scope of this ADR** — measured 2026-07-27 (grep; see the note on tooling below):

| # | Before | After | Files | Occurrences | Wire break |
|---|---|---|---|---|---|
| 1 | `awaiting_owner_correction` | `awaiting_supervision` | 31 | 197 | yes — enum + persisted |
| ~~2~~ | ~~`Plan.OwnerAgentID`~~ | **REMOVED FROM SCOPE in v3** — see below | — | — | — |
| 3 | `Plan.OwnerSessionID` | `SupervisionSessionID` | 23 | 80 | minor (`omitempty`) |
| 4 | `OwnerScopeKind` / `OwnerScopeID` | `ScopeKind` / `ScopeID` | 18 | 209 | yes — lifecycle records |
| 5 | `OwnsPlanID` | `SupervisedPlanID` | 13 | 32 | minor — no writer exists |
| 6 | `ownerKey` | `scopeKey` | 5 | 63 | **no** — internal only |
| 7 | `ProcessSession.OwnerSessionID` | `TranscriptSessionID` | *(subset of 3)* | — | **no** — in-memory only |

**v3 — why `OwnerAgentID` left the scope (CRIT-1).** v2's D14 said "delete" while D2 said
"left in place" — a direct contradiction, and the deletion was the more dangerous reading.
The field is `required` with `additionalProperties: false` in **two** schemas, so removing
it is runtime-enforced breakage — the same one-way door that blocked v1, one field over. It
also does not have "one job that moves to PlanSupervisor": it is additionally
`requireOwner`'s authority subject (so D3's own core change becomes unspecifiable without
it), the judge assignee, the agent-delete guard, and two owner validators. And roughly 40%
of the row's 340 grep occurrences are a **different entity** — `Schedule.owner_agent_id` —
which the count silently conflated. Retiring it is its own ADR, after supervision ships.

**#4 is the conceptually load-bearing one:** `OwnerScope*` is not ownership at all, it is
containment. Renaming it is what stops "owner" meaning two incompatible things.
**#7 is free and unambiguous** — the field is literally assigned from
`ToolTranscriptSessionID(ctx)`. **#2 is a deletion, not a rename** — D5 moves its job to
PlanSupervisor.

**Retained deliberately:** `Plan.Owner`, `Task.Owner`, `CreatedBy` — these *are* the
canonical meaning. D2 keeps `Plan.Owner` as the principal.

**Execution method — do NOT use `rename` for these.** Verified 2026-07-27: for these struct
fields `rename` produced `graph_edits: 0, text_search_edits: 73`, proposed editing generated
files (desynchronising them from the contract, which Constraint #8 forbids), renamed a field
but not its sibling enum constants, and missed `contracts/` and the TS side entirely.
Likewise `impact` returns a **false zero** on struct fields unless `relationTypes` includes
`ACCESSES`. Use the Constraint #8 pipeline instead: edit `contracts/` →
`scripts/gen-contracts.sh` → let the Go compiler and `tsc -b` enumerate every break. The
compiler is exhaustive where the graph is not. (Both caveats are recorded in `CLAUDE.md`.)

**Migration — NOT required. Greenfield `[FACT: operator, 2026-07-27]`.**

The operator's ruling: **sessions already on disk do not matter; treat this as greenfield.**
That is consistent with the project's declared v0.3 posture (fresh-build, no back-compat) and
with the ADR-035/037/054 precedent.

So the whole migration concern is moot, and with it the boot-sweep hazard v3 raised:

- **No migrator ships.** v3 required one, citing `pkg/task/migrate_planning_status.go`. Withdrawn.
- **No `schema_version` is needed.**
- **The boot-sweep risk evaporates.** The hazard was *old records* deserialising with empty
  `OwnerScopeKind` / `OwnsPlanID`, which would cost a parked session both of
  `isNeedsInputReconstructable`'s and the plan-owner exemption's protections and sweep it to
  terminal `failed(interrupted)` (`boot_sweep.go:293-297` + the two INV-9 exemptions). With no
  legacy records, that path is unreachable.

**Therefore D14 ships in full — all seven rows, including 4 and 5** (`OwnerScopeKind`/
`OwnerScopeID`, `OwnsPlanID`). The spec's AMB-1 recommendation to descope them was correct
*given* a migration constraint; that constraint does not exist.

**What does NOT go away:** the code still has to be renamed consistently. The Go compiler
covers that — `boot_sweep.go:295` reads `rec.OwnerScopeKind` as a struct field, so a rename
is a compile error, not a silent runtime failure. The exceptions the compiler cannot see are
the ones D14's method section names: embedded markdown (`plan/SKILL.md:158`), the
`inboundschemas/*.yaml` mirrors, and the e2e specs. Those need the `rg` sweep.

**Operational note:** any existing dev/UAT `$OMNIPUS_HOME` should be recreated rather than
upgraded, since its records will not load.

**Scope of the ruling — unconditional `[FACT: operator, 2026-07-27]`.** Restated by the
operator: *"not any data migration, not our problem."* This covers **every** store — plans,
sessions, lifecycle records, tasks — not just sessions. No migrator, no `schema_version`, no
upgrade-on-read, no compatibility shim, for any field this ADR renames. Existing data is
expected not to load, and that is accepted.

Implementers: do not reintroduce a migration step "to be safe". It is out of scope by
decision, and a half-migration would be worse than none.

```
CONFIDENCE: Medium-High
  Basis         : Operator decision, with the canonical definition agreed and per-item cost measured. The method is settled after empirically ruling out the graph tooling.
  Evidence      : field survey (2026-07-27); per-item grep counts; rename/impact dry-run failures.
  Missing       : Whether persisted records on disk hold old values, and the true edit-site count (grep over-counts comments and wire strings).
  Would improve : A disk survey, and a compile-driven count once contracts are changed.
```


### D15 — System agents are not chat targets `[FACT: operator — v3, closes CRIT-4]`

The session guard rejects only **workers**: `isWorkerAgentID(...)` in
`pkg/gateway/rest.go:1117` (and the WS equivalent) `[FACT — read directly]`. A System Agent
is not a worker, so PlanSupervisor would be **chat-addressable**: any user could open a
session against the sole holder of the correction grant and simply ask it to correct a plan.

D3 authenticates the **agent**, not the **intent** — so the gate would pass. That is a
confused-deputy path straight through this ADR's authority model.

Extend the guard so system agents are rejected as chat targets. This also fixes the Judge,
whose seed already intends "never a chat target" but is enforced only by convention.

```
CONFIDENCE: High
  Basis         : Operator decision; closes a concrete escalation path at the session boundary, and repairs an existing gap for the Judge at the same time.
  Evidence      : rest.go:1117 isWorkerAgentID — workers only.
  Missing       : Whether any flow legitimately opens a session against a system agent today.
  Would improve : Grepping for session creation with a system-agent id before enforcing.
```

### D16 — `supersede` must not be able to satisfy a DoD by discounting failure `[v3 — closes CRIT-5]`

NFR-2 says PlanSupervisor cannot lower its own bar, and v2 justified that structurally:
`CorrectionRequest` has no DoD field. That is true and insufficient. **`supersede` marks a
member's outcome ignored-by-judge** — so an adjudicator facing an unmet criterion can
satisfy it by discounting the failing evidence rather than fixing the work. The DoD text is
untouched; the *standard* is not.

v2's success criterion — "the DoD is byte-identical across a correction cycle" — is a
**tautology**: no correction verb can alter it, so the test passes unconditionally and
provides false assurance.

Required instead:

- A `supersede` MUST be paired with work that addresses the same criterion (an `append`
  in the same correction, or a `targeted_retry` of the superseded member). Superseding with
  no replacement is discounting, not correcting.
- Every `supersede` MUST be distinctly visible in the audit trail (D13), not folded in with
  appends.
- The real integrity test is behavioural: **a plan whose only defect is an unmet criterion
  MUST NOT reach `done` via supersede alone.**

```
CONFIDENCE: Medium
  Basis         : The hole is real and the tautology is demonstrable; the specific pairing rule is my proposal, not an operator decision.
  Evidence      : supersede's documented semantics (done record immutable, judge weighting changes); CorrectionRequest has no DoD field.
  Missing       : Whether the engine can enforce pairing atomically within one CorrectionRequest.
  Would improve : Reading buildCorrectionApplyFunc to see if the verbs compose in a single call.
```

---

## 7. Risks and Caveats

- **R1 — FR-146 is overridden.** A future reader may follow the spec and be surprised.
  *Mitigation:* D1 records the rationale; spec FR-146 should be annotated to point here.
- **R2 — Unquantified benefit (CRIT-004).** The capability gap is real; the completion-rate
  improvement is not predicted. *Mitigation:* §9's success criteria measure the capability,
  not the rate.
- **R3 — Silent privilege inheritance (D8).** Forgetting PlanSupervisor's policy map grants
  it `bash`/`write_file`/`create_agent` **silently** — it does not fail boot.
  *Mitigation:* explicit enumeration + security review.
- **R4 — Two exhaustion causes share one reason** (G3/D4).
- **R5 — Deferred rename leaves a misleading enum in place** (D6): `awaiting_owner_correction`
  names the wrong actor until the rename lands.
- **R6 — Adjudication quality is unmeasured.** No baseline exists for whether
  PlanSupervisor's verdicts beat the status quo.
- **R7 — Unused fields retained** (D7). Tidiness debt, deliberately accepted.

---

## 8. Confidence Assessment

| Decision | Confidence |
|---|---|
| D1 System Agent; FR-146 overridden | **Medium-High** |
| D2 Reuse `owner`; no rename | **High** |
| D3 Fix the correction gate | **High** |
| D4 Share the judge round budget | **High** |
| D5 Wake split; notifications exist | **High** |
| D6 Retain phase; rename this release | **High** |
| D7 No deletion of session linkage | **Medium-High** |
| D8 Explicit tool grant; Constraint #6 corrected | **High** |
| D9 Immutability already implemented | **High** |
| D10 Failure model | **High** |
| D11 Model contract | **High** |
| D12 Reuse the verdict type | **Medium-High** |
| D13 Auditability | **Medium** |
| D14 Owner vocabulary (minus `OwnerAgentID`) | **Medium-High** |
| D15 System agents not chat targets | **High** |
| D16 `supersede` guard | **Medium** — pairing rule is my proposal |

**Roll-up:** v2's load-bearing decisions (D2, D3, D8, D9) are High and rest on
directly-verified code. The Mediums are deferrals and exposure questions, not architecture.
**D1 is the only genuine judgement call** — overriding an accepted `[P2]` MUST — and it is
recorded with its rationale rather than buried.

---

## 9. Validation / Next Steps

**Resolve before implementation:**

1. **G1 / D8 / R3** — enumerate PlanSupervisor's tool grant and review it as a security
   change. Highest-consequence open item.
2. **G4** — read `plan/SKILL.md`; confirm it is usable for *correcting*, not just authoring.
3. **G2 / D6 / D14** — survey plans AND lifecycle records on disk for old values before
   the rename lands (they are persisted, not merely wire-visible).
4. **G5 / D13** — check whether revision entries are exposed on any read surface.

**Success criteria.** The honest measure is not "parked plans drop" — CRIT-004 shows that
is multi-causal — but:

- A plan whose DoD is unmet for a **correctable** reason reaches a terminal state without
  human re-authoring, in a scenario test.
- No non-PlanSupervisor agent can invoke correction (negative test on D3).
- A plan whose only defect is an unmet criterion does NOT reach `done` via `supersede`
  alone (real integrity test on NFR-2 — **replaces v2's byte-identical DoD check, which was
  a tautology**: no correction verb can alter the DoD, so it passed unconditionally).
- A system agent cannot be opened as a chat target (D15).
- A parked plan survives a gateway restart and is re-woken (FR-9).
- *(Removed in v4: "pre-rename records still load" — contradicted the no-migration ruling.
  Existing data is expected not to load; that is accepted, not a defect.)*
- Corrections terminate within `judge_max_rounds` (bound test on D4).
- PlanSupervisor unavailable ⇒ plan parked **and** Owner notified (test on D10).

**Implementation order** (Constraint #8):

1. `pkg/coreagent` — `SystemAgents()` + `systemAgentSeed` grant + `PlanSupervisorDefaultRubric`.
2. `pkg/agent/plan_engine.go` — the D3 gate fix; split `wakeOwner`; the D10 failure path.
3. `pkg/tools` + `pkg/gateway` — correction tool and REST route (contracts first).
4. `pkg/notifications` — the four gaps in D5.
5. SPA — revise the parked-phase copy (mandatory this release).
6. SPA + contracts — the **D14 vocabulary rename** (including the D6 phase rename),
   contracts-first and compiler-driven. Survey persisted records for old values first.

**Handoff:**

- Re-red-team: `/grill-spec docs/internal/architecture/ADR-055-plan-supervisor.md`
- Then: `/plan-spec docs/internal/architecture/ADR-055-plan-supervisor.md`
