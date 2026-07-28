# Adversarial Review (Round 3): ADR-055 — PlanSupervisor

**Spec reviewed**: `docs/internal/architecture/ADR-055-plan-supervisor.md` (v3, Proposed, 2026-07-27)
**Prior reviews**: `ADR-055-plan-supervisor-review.md` (v1 — BLOCK, 33 findings);
`ADR-055-plan-supervisor-review-r2.md` (v2 — BLOCK, 41 findings)
**Review date**: 2026-07-27
**Input mode**: structured-spec
**Verdict**: **BLOCK**

## Executive Summary

v3 closes real ground: `owner_agent_id` is out of the deletion scope, the migrator is now
mandatory, the chat-target hole is a stated decision (D15), the `supersede` integrity hole is
acknowledged (D16), and the byte-identical-DoD tautology is gone. But the document blocks on
its own headline correction. **D8's "v3 correction" reads one branch of a five-branch switch,
declares the permissive-inheritance risk false, and is itself wrong** — the ADR's own §7 R3
still states the correct version, and ADR-056 still cites "see ADR-055 D8" for the opposite
claim. Alongside it, **D5's new principal-kind fork has no field to fork on**, **D16's pairing
rule is half-impossible against `validateCorrection`**, and **§9 still instructs the
implementer to build the REST route FR-5 deleted**.

| Severity | Count |
|----------|-------|
| CRITICAL | 5 |
| MAJOR | 16 |
| MINOR | 12 |
| OBSERVATION | 3 |
| **Total** | **36** |

**Recurrence note.** v1's pattern was "proposes building something that already exists." v2's
was "research stopped at the questions it was asked about." v3's is narrower and more
specific: **it fixed the citations it was told about and re-derived the wrong conclusion from
them.** D8 opened the right file (a genuine improvement over v2) and read the wrong branch.
D5 identified the right gap and specified a fork the data cannot support. D16 named the right
hole and proposed a control the engine rejects by construction. Nine r2 findings — MAJ-004,
MAJ-007, MAJ-009 (method), MAJ-010, MAJ-011, MAJ-015, MAJ-016, MIN-001, MIN-004/005 — are
untouched, several now more acute because v3 promoted the rename into this release.

---

## Findings

### CRITICAL Findings

#### [CRIT-1] D8's v3 correction is wrong, contradicts the ADR's own R3, and now puts ADR-055 and ADR-056 in direct disagreement about the same code

- **Lens**: Incorrectness / Insecurity
- **Affected section**: v3 changelog bullet 3; D8; §7 R3; G1
- **Description**: v3's changelog leads with this as one of the three findings that changed the
  design: *"D8's Constraint #6 claim was backwards — the resolver **fails closed to deny**
  (`pkg/tools/compositor.go:186-190`), it does not silently grant."* D8 then quotes:

  ```go
  case g == "" && a == "":
      // Fail closed rather than silently defaulting to "allow".
      return config.ToolPolicyDeny
  ```

  That branch is real (verified at `pkg/tools/compositor.go:181-188`) and it is **not the
  branch the risk runs through**. The risk R3 names is *"forgetting PlanSupervisor's policy
  map"* — i.e. the agent map is empty but the **global** map is not. That is the next case
  down:

  ```
  191: 	case a == "":
  192: 		return g
  ```

  and the seeded global ceiling supplies `allow` for exactly the tools R3 names:
  `"bash": "allow"` (`pkg/config/defaults.go:284`), `"write_file": "allow"` (`:286`),
  `"set_config": "allow"` (`:364`), `"create_agent": "allow"` (`:365`). So an agent seeded
  with **no** policy map boots fine and resolves `allow` for all four. The `g == "" && a == ""`
  deny branch fires only when *neither* map has an entry, which the code's own comment calls
  *"structurally impossible once boot/write-time coverage validation is in place."*

  Three consequences, all inside this document or one door away:
  1. **D8 and R3 now contradict each other.** §7 R3 still reads: *"Forgetting PlanSupervisor's
     policy map grants it `bash`/`write_file`/`create_agent` **silently** — it does not fail
     boot."* That is correct. D8, four sections earlier, says it is false.
  2. **ADR-056 now disagrees with ADR-055.** v3 states *"Both documents are corrected."* Only
     the *path* was corrected. `ADR-056-background-job-visibility.md:92-94` still reads: *"the
     real risk is **silent permissive inheritance** via `pkg/tools/compositor.go:178-201`, not
     a boot abort — see ADR-055 D8."* ADR-056 is right; the ADR-055 D8 it points at now says
     the opposite.
  3. **The lesson v3 draws is inverted.** D8 concludes *"The original Constraint #6 reading
     stands: coverage gaps are caught by boot/write-time validation, and an unresolved tool
     denies rather than grants."* The accurate statement is the one r2 supplied and v3 did not
     adopt: the boot abort is real (`pkg/gateway/gateway.go` coverage validation), it simply
     **does not fire here**, because a coverage "gap" requires neither map to have an entry and
     the global ceiling enumerates every static tool.
- **Impact**: The ADR's single most security-relevant claim — and the one its changelog
  presents as freshly verified — tells a future implementer that omitting PlanSupervisor's tool
  grant is safe. It is not: the ADR's most privileged new agent would silently inherit `bash`,
  `write_file`, `set_config` and `create_agent`, which is precisely the NFR-2 escape D8's own
  caveat paragraph warns about two paragraphs later. v3's changelog names this exact failure
  mode ("A citation inherited from a review is not verification") and then commits it by
  reading one branch of a switch.
- **Recommendation**: Rewrite D8's first three paragraphs to: *"A missing per-agent policy map
  is **not** a coverage gap — a gap requires neither the global nor the agent map to have an
  entry, and the seeded global ceiling (`pkg/config/defaults.go:255-418`) enumerates every
  static tool. So boot validation does not fire, and `resolveEffectivePolicyWith`'s
  `case a == "": return g` (`pkg/tools/compositor.go:191-192`) hands the agent the global
  value — `allow` for `bash` (`defaults.go:284`), `write_file` (`:286`), `set_config` (`:364`),
  `create_agent` (`:365`). The explicit enumeration in `systemAgentSeed` is therefore
  load-bearing, not belt-and-braces."* Keep R3 as written. Leave ADR-056:92-94 as written and
  fix only its cross-reference target. Note also that the merge is
  **most-restrictive-wins** (`compositor.go:193-200`), not OR-based.

---

#### [CRIT-2] D5's outcome fork has no discriminator, and the wire contract says the field it forks on is always a username

- **Lens**: Infeasibility / Incompleteness
- **Affected section**: D5 (*"outcome delivery forks by principal kind"*); D2; FR-4; FR-9;
  NFR-4; D10
- **Description**: v3 correctly identifies r2's CRIT-003 (the notification store is
  username-keyed and cannot address an agent) and resolves it with a fork:

  | Owner is | Mechanism |
  |---|---|
  | an **agent** | bus inbound (`PublishInbound`) |
  | a **human** (username) | `pkg/notifications/` store + WS push + bell |

  **Nothing in the data supports the decision.** `Plan.Owner` is a bare `string`, written
  identically on both paths and with no kind marker:
  - `pkg/tools/plan.go:286-287` — `Owner: callerID, CreatedBy: callerID`
  - `pkg/gateway/rest_plans.go:547-548` — `Owner: c.Username, CreatedBy: c.Username`

  There is no `owner_kind`, no `agent:`/`user:` prefix, and the two namespaces are flat and
  can collide. The engine has no legal way to take the branch D5 requires.

  Worse, the published contract asserts the opposite of D2's foundation.
  `contracts/components/schemas/Plan.yaml:244-248`:

  > `owner:` `type: string` `readOnly: true`
  > `description: Username of the user who created this plan.`

  D2's load-bearing claim — *"`Plan.Owner` … **already holds the dual-kind principal**"* — is
  true in Go and **false on the wire**. So D2 ratifies an undocumented contract divergence,
  and D5 then builds a runtime branch on the divergence D2 didn't record. (r2 raised the
  contract-description half as MAJ-016; v3 addressed neither half, and its new fork now
  depends on both.)
- **Impact**: FR-4, FR-9, NFR-4 and D10 are all unimplementable as specified. Taking the wrong
  branch reproduces exactly the failure v3 set out to fix: either an unreadable
  `<agent-id>.json` written by `notifications.Store.Create` (which sanitizes the recipient into
  a filename and returns success — `pkg/notifications/store.go:163-217`), or a bus wake
  addressed to a username that resolves to no agent. D10 — the only thing between "supervisor
  is down" and "plan is silently stuck forever" — silently no-ops in either direction.
  D2's "no wire change" claim also fails: fixing the description is a `contracts/` edit plus
  regen under Constraint #8.
- **Recommendation**: Decide the discriminator **in this ADR**. Cheapest coherent option: stop
  forking on `Plan.Owner` and fork on the write path instead — record the kind at creation
  (an `owner_kind: user|agent` property on `Plan.yaml`, set to `agent` in `pkg/tools/plan.go`
  and `user` in `pkg/gateway/rest_plans.go`), with a documented default for existing records
  and a migrator entry alongside D14's. Fix `Plan.yaml:244-248` to match reality in the same
  change. Then add: *"Given a plan whose owner is an agent id, When the plan terminates, Then
  the owner receives the outcome on a surface it can actually read"* and the username twin.
  Also settle r2's MAJ-016 while here: `Owner` and `CreatedBy` are provably always equal on
  both write paths; say which is authoritative.

---

#### [CRIT-3] D16 / FR-14 — the control that closes the integrity hole — is half impossible and half unmodelled

- **Lens**: Infeasibility / Incorrectness
- **Affected section**: FR-14; D16; §9 success criterion 3
- **Description**: D16 is right that the hole is real and right that v2's success criterion was
  a tautology. Its proposed control does not survive contact with the engine:

  > *"A `supersede` MUST be paired with work that addresses the same criterion (an `append`
  > in the same correction, **or** a `targeted_retry` of the superseded member)."*

  **The second option is impossible by construction.** `validateCorrection`
  (`pkg/agent/plan_engine.go:2693-2716`) routes the two verbs through the same helper with
  opposite status requirements:

  ```
  2699: 	pe.validateMemberRef(planID, req.SupersededMemberID, "supersede", task.StatusDone, "done")
  2706: 	pe.validateMemberRef(planID, req.RetriedMemberID, "targeted_retry", task.StatusFailed, "failed")
  ```

  and `validateMemberRef` (`:2725-2739`) hard-rejects a status mismatch. A member cannot be
  both `done` and `failed`, so **you can never `targeted_retry` a member you superseded.**
  The ADR offers a remedy the engine refuses.

  **The first option is not modelled either.** `CorrectionRequest.Verb` is a **single**
  `CorrectionVerb` (`:2411`), and `validateCorrection`'s `case CorrectionSupersede:` requires
  only `SupersededMemberID` — it neither requires nor inspects `TailMembers`. (`TailMembers` is
  carried on every request and applied verb-independently by `buildCorrectionApplyFunc`
  `:2781-2785`, so "supersede + tail work in one request" is *expressible* — but nothing in the
  type or the validator makes it the *pairing rule D16 states*, and the ADR does not specify
  the new validation.)

  D16's own `Missing:` line asks exactly this — *"Whether the engine can enforce pairing
  atomically within one `CorrectionRequest`"* — and `Would improve: Reading
  buildCorrectionApplyFunc`. The read was not done, and FR-14 is nevertheless stated as a MUST
  and §9 success criterion 3 depends on it.
- **Impact**: The ADR's only answer to its own 25%-weighted decision criterion (*"Integrity
  (cannot self-lower the bar)"*) is unimplementable as written. An implementer either drops
  it — leaving the r2 CRIT-005 hole open, with up to `PlanJudgeMaxRounds` attempts to reshape
  the evidence set — or invents a semantics the ADR never authorized. D16's confidence is
  already flagged Medium *"pairing rule is my proposal, not an operator decision"*; the
  proposal is also infeasible.
- **Recommendation**: Pick a control the engine can actually carry. Ranked:
  (a) **Deny PlanSupervisor the `supersede` verb** in its `systemAgentSeed` grant — the only
  option that needs no engine change and is trivially testable, and it costs only the
  transient-failure case that `targeted_retry` already covers;
  (b) require `len(req.TailMembers) > 0` in `validateCorrection`'s `CorrectionSupersede` case
  and state that as the pairing rule (drop the `targeted_retry` disjunct entirely — it is
  contradictory);
  (c) cap supersedes per plan independently of the round budget.
  Whichever is chosen, keep §9's behavioural criterion but restate it against the chosen
  mechanism. Note that `reconstructCorrections` (`:3130-3131`) already replays supersedes from
  the intent log on boot, so the audit half of D16 is cheaper than it reads.

---

#### [CRIT-4] §9's implementation order instructs the implementer to build the REST route FR-5 deleted, and still orders the contract change after its consumers

- **Lens**: Inconsistency
- **Affected section**: FR-5 (line 87-90); §1 blast radius (line 68); §5 Option B cost cell
  (line 184); §9 implementation order steps 3 and 4
- **Description**: FR-5 v3 is unambiguous:

  > *"**v3: the REST route is dropped from this ADR** — it has no SPA client, `HandlePlans` has
  > no per-plan authorization to inherit, and it would hold the process-wide `planDecisionMu`
  > unrated-limited."*

  §9 step 3, 690 lines later:

  > *"3. `pkg/tools` + `pkg/gateway` — correction tool **and REST route** (contracts first)."*

  §1's blast radius still reads *"one tool + one REST route"*; §5's Option B cost cell still
  reads *"one tool/route"*. Three of the four references were not updated. An implementer
  working from §9 — the section headed *"Implementation order"* — ships the endpoint r2's
  CRIT-006 blocked on: registered under `a.withAuth` on a surface where `HandlePlans`
  (`pkg/gateway/rest_plans.go`) performs **no** per-plan owner check on any verb, granting every
  authenticated user correction rights on every plan, with no rate limit on a handler that
  takes the process-wide `planDecisionMu` for its whole body
  (`pkg/agent/plan_engine.go:2575-2576`).

  **Second defect in the same list.** Step 4 is *"`pkg/notifications` — the four gaps in D5"*,
  ordered after steps 2-3 which emit those notifications. One of those gaps is a closed
  single-value `type` enum in two contract files
  (`contracts/components/schemas/Notification.yaml`, `contracts/asyncapi.yaml`) generated into
  Go with a `Valid()` guard and into the SPA zod schemas. Constraint #8 requires the contract
  change **before** any consuming code. r2 raised this as MAJ-013; the ordering is unchanged.
  (The `Notification.yaml` enum also contradicts itself: a closed one-value `enum:` under
  `additionalProperties: false`, described as *"Extensible; consumers must tolerate unknown
  values."* The SPA does not tolerate them — `src/lib/api.ts` throws `ApiSchemaError`.)
- **Impact**: The ADR's own scope deletion does not reach the section implementers read. The
  unauthorized-endpoint risk that motivated the deletion ships anyway, and the notification
  contract widening lands after the code that depends on it, guaranteeing either a
  `verify-contracts` failure or a silently dropped notification — the exact failure D10 must
  not have.
- **Recommendation**: Delete "and REST route" from §9 step 3; fix §1's blast radius and §5's
  Option B cost cell to match FR-5. Promote the notification contract widening to step 1b.
  Add a line to FR-5 stating that human correction parity is explicitly deferred and what it
  will need when it comes (principal, denial code matching D3's opaque form, `RequireNotBypass`
  posture, rate limit on `planDecisionMu`).

---

#### [CRIT-5] The D6 phase rename lands inside PlanSupervisor's own prompt, and D14's stated method cannot see it

- **Lens**: Incorrectness / Infeasibility
- **Affected section**: D6; D14 *"Execution method"*; FR-3; §9 step 6
- **Description**: v3 promotes the phase rename into this release (D6, operator decision) and
  specifies the method:

  > *"Use the Constraint #8 pipeline instead: edit `contracts/` → `scripts/gen-contracts.sh` →
  > let the Go compiler and `tsc -b` enumerate every break. **The compiler is exhaustive where
  > the graph is not.**"*

  It is not exhaustive, and the counter-example is in a file this ADR already edits.
  `pkg/skills/embedded/plan/SKILL.md:158` contains the literal:

  > *"When the plan Judge returns UNMET and the plan enters `awaiting_owner_correction`, work
  > through this checklist…"*

  That file is `go:embed`ed shipped prompt text. Neither `go build` nor `tsc -b` can see inside
  it. Under FR-3 it is granted to **PlanSupervisor itself**, so after the rename the ADR's
  adjudicator reads a playbook keyed on a phase value that no longer exists — as does every
  other `create_plan`-granted agent.

  v3 does identify and schedule the amendment of `SKILL.md:231-232` (the *"do not create a
  forked Planner agent"* anti-pattern line) — correct and a genuine v3 improvement. It never
  mentions `:158`, **in the same file, 73 lines earlier**, which the rename it just promoted
  invalidates.

  r2 raised the method defect as MAJ-009 with three categories — the embedded skill,
  `tests/e2e/*.spec.ts` (outside the `tsc -b` project graph), and
  `pkg/gateway/inboundschemas/*.yaml` (the embedded copy of the schemas used for **runtime**
  inbound validation, produced by `scripts/gen-contracts.sh`). None of the three is in v3's
  method, and the rename is now in-release rather than deferred, so the exposure went up.
- **Impact**: The rename ships compiler-green with a stale agent-facing prompt teaching a dead
  enum value to the very agent this ADR creates, red e2e specs, and a runtime inbound validator
  still enforcing the old key. Also note `pkg/plan/plan.go:237` is the const
  (`PhaseAwaitingOwnerCorrection PlanPhase = "awaiting_owner_correction"`) and `:266` the
  validity map — both compiler-visible, which is what makes the invisible surfaces easy to
  miss.
- **Recommendation**: Amend D14's execution method to: *"compiler + `tsc -b` for identifiers,
  **plus a mandatory `rg` sweep** over `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`,
  `tests/e2e/**`, `**/*.yaml` prose and `**/*.md`, and run the e2e gate before merge."* Add
  `plan/SKILL.md:158` to §9 step 6 explicitly, next to the `:231-232` amendment already there.
  Give D14 a success criterion: *"`rg 'awaiting_owner_correction|OwnerScope|ownerKey'` over
  non-doc files returns zero."*

---

### MAJOR Findings

#### [MAJ-1] D7's "they become unused by the new routing" is false — `requireOwner`, the ADR's own core gate, reads `OwnerSessionID`
- **Lens**: Incorrectness
- **Affected section**: D7; D3; D14 row 3
- **Description**: D7: *"They become unused by the new routing — tidiness debt, not a defect."*
  `requireOwner` (`pkg/agent/plan_engine.go:2765`) — the subject of D3, the ADR's self-declared
  core change — reads it as its third clause:
  `if p.OwnerSessionID != "" && caller.SessionID != p.OwnerSessionID { … }`. It is also read at
  `:1710-1711` (session collection) and written at `:2474` (`ensureOwnerSessionLocked`) and
  `pkg/plan/store.go:397-398`. D3 never says what that clause does for PlanSupervisor, whose
  session will not be the plan's `plan:<id>` owner session — so as written, admitting
  PlanSupervisor by identity still fails at clause 3.
- **Recommendation**: State in D3 whether the session clause is dropped, exempted for
  PlanSupervisor, or replaced. Correct D7's "unused" claim.

#### [MAJ-2] NFR-3's new anti-probing clause is falsified by the gate D3 preserves
- **Lens**: Insecurity (Information Disclosure)
- **Affected section**: NFR-3; D3 (*"the existing **opaque** denial"*)
- **Description**: NFR-3 now requires *"unauthorized callers must not be able to probe plan
  state via error differentiation."* `requireOwner` (`:2754-2770`) returns **three different
  strings**: `"caller agent identity is empty"`, the opaque `"plan %q"`, and `"caller session
  does not match the plan's owner session"`. Only the middle one is opaque. Clause 3 fires
  *only after* the agent match passes, so its distinct message confirms to the caller that they
  **are** the owner agent — and `OwnerSessionID` is the derived, guessable `"plan:" + p.ID`
  (`:2474`), so it proves nothing about the caller in the first place. D3 asserts the denial is
  opaque; one third of it is. (r2 MIN-003, unaddressed and now promoted by NFR-3's new wording.)
- **Recommendation**: Make all three branches return one identical wrapped
  `ErrCorrectionNotOwner` message, or state that the session clause is not a security control
  and is being dropped.

#### [MAJ-3] FR-9(b) and FR-9(c) reverse two documented deliberate designs without citing them
- **Lens**: Incompleteness / Infeasibility
- **Affected section**: FR-9 (v3 mechanism a/b/c)
- **Description**: FR-9(c) — *"a failed publish MUST escalate, not WARN-and-continue"* —
  reverses `wakeOwner`'s stated contract (`:2093-2095`): *"Best-effort: a notify failure is
  logged, never escalated (the mechanical plan-state transition it accompanies has already been
  persisted regardless)."* FR-9(b) — *"a nil notifier MUST be a startup error"* — reverses a
  house nil-guard pattern documented twice: `:198` (*"cancelSessions nil-guards it the same way
  notifier/dispatcher calls do"*) and `:329-330` (*"tests construct a `*PlanEngine` struct
  literal directly (same package) with fake judge/dispatcher/notifier/clock fields"*).
  `pe.notifier` is assigned once from `al.asyncNotifier` (`:358`); `Start`'s existing hard
  precondition check (`:550-552`) covers planStore/taskStore/dispatcher/judge but deliberately
  not the notifier. Both reversals may be right; neither says what it is reversing, and (b)
  will break in-package test construction unless scoped to `Start`.
- **Recommendation**: Quote both existing contracts and argue the reversal, as D1 does for
  FR-146. Scope (b) to `Start`'s precondition check specifically. FR-9(a) — adding a
  `PhaseAwaitingOwnerCorrection` case to `processPlan`'s boot switch — is correct and verified
  missing (`:844-866` handles only `PhaseJudging` and `PhaseSynthesizing`); keep it.

#### [MAJ-4] D4 still needs a second `JudgeRounds` writer, still double-charges, and G3 still undercounts the exhaustion causes
- **Lens**: Incorrectness / Infeasibility
- **Affected section**: D4; FR-7; G3; R4; §9 success criterion 7
- **Description**: Unchanged from v2 despite r2 MAJ-007. Verified: `JudgeRounds` has exactly
  one writer, `newRounds := current.JudgeRounds + 1` (`:1495`), declared **sole** in the comment
  at `:1488-1494`. Charging a correction requires a second incrementer, contradicting that
  invariant, and the re-judge a correction provokes increments anyway — so "one round per
  correction" halves the effective budget. G3 says `judge_rounds_exhausted` covers two causes;
  it has **three** producers: the real ceiling (`:1289`) and `AppendCorrection`'s honest-exit
  path (`:2680`, fired on `planCannotProgress`), which means *"DoD unreachable"* — a third
  meaning that becomes reachable only when this ADR ships.
- **Recommendation**: State that the correction does **not** increment and the re-judge it
  provokes does — that gives D4's one-budget property with no second writer and no
  double-charge. Update G3 to three causes and specify three distinct handover messages.

#### [MAJ-5] No `§ Upstream decisions this ADR reverses`; one `[P1]` and five `[P2]` MUSTs plus six ADR-053 decisions and ADR-049 remain uncited
- **Lens**: Inconsistency
- **Affected section**: header Amends line; D6; D14; FR-8
- **Description**: Verified by grep over the whole v3 document: `FR-118` and `FR-147` appear
  **only inside D7**, as history. `FR-193` `[P1]`, `FR-140`, `FR-141`, `FR-186`, `FR-133`,
  `FR-109`, ADR-053 D2/D4/D7/§3-BOM/§5.1, and `ADR-049` appear **nowhere**. FR-141 and FR-186
  hard-code the literal `awaiting_owner_correction` that D6 now renames *this release*; FR-193
  `[P1]` governs the boot sweep's treatment of that exact phase and is hit by both the rename
  and D14 row 4. r2 raised this as MAJ-004 with the verified table; v3 adds no section.
- **Recommendation**: Add the section. The FR-146 treatment in D1 (quote, override, rationale,
  risk filed) is the template — apply it to each row.

#### [MAJ-6] D15 closes one of the two chat-target holes r2 named; the default-agent star is untouched
- **Lens**: Insecurity (Elevation of Privilege)
- **Affected section**: D15; FR-13
- **Description**: D15's fix is right for the session/WS surface (`pkg/gateway/rest.go:1117`,
  `pkg/gateway/websocket.go:1243` — both verified as `isWorkerAgentID`-only). The second
  surface is unmentioned: `PUT /api/v1/agents/{id}` with `default:true` gates on
  `foundAgent.IsWorker()` only (`rest.go:2903`), and `AgentRegistry.GetDefaultAgent()`
  (`pkg/agent/registry.go:330-350`) filters workers at all three priorities and never system
  agents — `AgentInstance` has no `IsSystem()`/`IsChatTarget()` at all (verified: the method
  does not exist). So a System Agent can still be starred default and handed to every
  `GetDefaultAgent()` caller. FR-13 states the MUST; D15's mechanism satisfies it only
  partially.
- **Recommendation**: Add both to D15: change `rest.go:2903` to reject non-chat-targets, and add
  `IsSystem()`/`IsChatTarget()` to `AgentInstance` so `GetDefaultAgent` agrees with routing.

#### [MAJ-7] D15's guard and D5's wake use overlapping machinery, and the ADR names no boundary
- **Lens**: Ambiguity
- **Affected section**: D15; D5; FR-8
- **Description**: D5 wakes PlanSupervisor over the bus — `wakeOwner` publishes
  `AsyncNotifyEvent{Channel: "system", ChatID: "plan:" + planID, AgentID: …}` (`:2102-2107`),
  which materializes a session for that agent. D15 says only *"extend the guard so system agents
  are rejected as chat targets"* without naming which boundary. The two current guards are the
  REST session-create handler and the WS chat frame, neither of which is on the bus path — but
  the ADR does not say so, and a guard placed one layer deeper disables its own wake path.
  Compounding: all five `wakeOwner` sites publish to the **same** synthetic `plan:<id>`
  destination (verified at `:1254, :1542, :1571, :1610, :1742`), so D5's decision/outcome split
  routes two different actors to one chat id. r2's Unasked Question 2, unanswered.
- **Recommendation**: Name the exact two call sites D15 changes and state explicitly that the
  bus/async-notifier path is unaffected. Say whether the split needs a second destination or
  whether both actors share `plan:<id>`.

#### [MAJ-8] PlanSupervisor's SOUL — the actual implementation of an LLM adjudicator — is still one clause
- **Lens**: Ambiguity / Incompleteness
- **Affected section**: FR-3; D1; D3 (*"Prefer matching on identity … Missing: Final predicate
  shape. Would improve: Choosing during /plan-spec"*); D5's stall note; D16
- **Description**: v3 adds two more behaviours the SOUL must carry — the `stalled` vs UNMET
  distinction (D5) and D16's supersede discipline (which, per CRIT-3, the engine may not be able
  to enforce, making the prompt the only control) — while still specifying the SOUL in one
  clause. §9 step 1 names `PlanSupervisorDefaultRubric` but the ADR contains no draft, whereas
  the Judge precedent D1 claims to follow verbatim ships a concrete reviewable const
  (`JudgeDefaultRubric`, `pkg/coreagent/core.go`). D3's predicate is likewise still deferred,
  which defers a security decision. And FR-9/D10's *"PlanSupervisor is unavailable"* is still
  undefined — provider outage? unparseable output? a turn that never returns? No detection
  mechanism and no timeout, while D10 explicitly excludes retry/backoff.
- **Recommendation**: Draft the rubric in the ADR covering UNMET-wake, stalled-wake, verb
  selection, honest exit, and the supersede rule. Fix D3's predicate to one stated form. Define
  "unavailable" with a mechanism and a timeout.

#### [MAJ-9] D12 unchanged: wrong line reference and a wire-type cost presented as an internal edit
- **Lens**: Incompleteness / Incorrectness
- **Affected section**: D12
- **Description**: Verified: `VerdictScopePlan` is at `pkg/task/verdict.go:11-20` (untyped string
  constants); `:43` is inside the `JudgeVerdict` struct. And the type **crosses the wire** —
  `contracts/components/schemas/JudgeVerdictFrame.yaml`, `contracts/asyncapi.yaml`, generated Go
  with a `Valid()` guard and the SPA zod schemas — so *"extension is additive"* understates
  "extend it with the chosen correction verb" to a full Constraint #8 five-step pipeline in three
  committed locations. r2 MAJ-010/MIN-002, unaddressed.
- **Recommendation**: Fix the reference; restate the cost; answer D12's own open question —
  putting the verb on `RevisionEntry` avoids the wire change entirely and is probably right.

#### [MAJ-10] D9's *"Missing: Nothing material"* unchanged, while this ADR adds a mutation path that does not inherit the freeze
- **Lens**: Incompleteness
- **Affected section**: D9
- **Description**: D9's core claim is verified verbatim (`pkg/gateway/rest_plans.go:717-736`
  returns 409 for `dod` and `owner_agent_id` on non-draft plans; the deliberate `Bounds`
  exemption comment is real at `:715-716`). But the invariant is a property of **one handler** —
  `plan.Store.updateLocked` applies both fields with no state check — and this ADR adds the
  correction tool as a new agent-facing mutation path. r2 MAJ-011, unaddressed.
- **Recommendation**: Change the `Missing:` line and state the rule: *"any new plan-mutation path
  MUST reject `dod` and `owner_agent_id` structurally in its request shape, as `CorrectionRequest`
  already does, because the store does not enforce the freeze."* Add a conformance test asserting
  `CorrectionRequest` has no DoD/owner field — that is the test §9's byte-identical criterion
  should have been.

#### [MAJ-11] D14's mandatory migrator stops at the store that cannot take one
- **Lens**: Incompleteness
- **Affected section**: D14 *"Migration — REQUIRED, not optional"*
- **Description**: v3's migration requirement is a genuine and important fix, and the named
  precedent is real (`pkg/task/migrate_planning_status.go::MigratePlanningStatusToNext`, plus
  `migrate_milestones.go`; `rg schema_version` over non-doc files still returns **zero**). But
  rows 4/5 (`OwnerScopeKind`/`OwnerScopeID`, `OwnsPlanID`) live in
  `$OMNIPUS_HOME/session_lifecycle/<id>.jsonl` — **append-only JSONL with an immutable-terminal
  invariant** — and the ADR never says whether the migrator rewrites the log in place, appends a
  corrected record, or something else. The plan-store migrator is a straightforward reuse; the
  lifecycle one has no precedent in the codebase.
- **Recommendation**: Split the requirement into two migrators and make an explicit decision about
  rewriting an append-only log. Add the two boot tests as acceptance criteria.

#### [MAJ-12] R5 still says the rename is deferred; R3 still contradicts D8
- **Lens**: Inconsistency
- **Affected section**: §7 R3, R5
- **Description**: R5: *"**Deferred rename** leaves a misleading enum in place (D6):
  `awaiting_owner_correction` names the wrong actor **until the rename lands**."* D6, G2 and §8
  all say it ships this release. r2 MAJ-015, unfixed. R3 vs D8 is CRIT-1. Both are v2 text that
  survived a decision reversal.
- **Recommendation**: Rewrite R5 as the risk that actually exists (*"renaming a persisted enum
  depends on the D14 migrator landing correctly; a partial migration strands parked plans"*).
  Keep R3 and fix D8.

#### [MAJ-13] D14 leftover text contradicts D14's own scope table
- **Lens**: Inconsistency
- **Affected section**: D14, line 607
- **Description**: Row 2 is marked **REMOVED FROM SCOPE in v3**, with a solid paragraph
  explaining why deleting `OwnerAgentID` is a one-way door. Two paragraphs later the v2
  instruction survives verbatim: *"**#2 is a deletion, not a rename** — D5 moves its job to
  PlanSupervisor."* This is the same D2-vs-D14 contradiction class that produced r2's CRIT-001,
  reduced to one stray sentence.
- **Recommendation**: Delete the sentence.

#### [MAJ-14] The largest work item still has no success criterion
- **Lens**: Incompleteness
- **Affected section**: §9 success criteria
- **Description**: The eight criteria cover FR-5/6/9/14, NFR-2, D4, D10, D15 and the D14
  migrator. Nothing measures **FR-12/D14 itself** (the ~150-file vocabulary rename, the single
  largest item in the document), **FR-3** (the skill grant), **FR-8** (the wake split) or
  **NFR-5** (auditability). r2 raised this; v3 added criteria for the new decisions and not for
  the old gap.
- **Recommendation**: Add one per item. For D14 a mechanical one suffices (the `rg`-returns-zero
  criterion in CRIT-5).

#### [MAJ-15] D14's occurrence counts do not reproduce; two rows are roughly double
- **Lens**: Incorrectness
- **Affected section**: D14 scope table
- **Description**: Re-measured 2026-07-27 with `rg`, excluding `docs/`, `*.md` **and the two
  full repo copies under `.claude/worktrees/`**:

  | Row | ADR says | Measured (code only) |
  |---|---|---|
  | 1 `awaiting_owner_correction` | 31 files / 197 | 31 files / **140** |
  | 3 `OwnerSessionID` | 23 / 80 | **15 / 57** |
  | 5 `OwnsPlanID` | 13 / 32 | **6 / 18** |
  | 6 `ownerKey` | 5 / 63 | 5 / **62** |

  Rows 1 and 6 match on files and are close on occurrences; rows 3 and 5 are roughly double the
  real figure. The most likely cause is counting the duplicated worktrees. In an ADR whose thesis
  is *"the confusion is not hypothetical"* and whose method section rests on measurement, the
  measurement should exclude repo copies.
- **Recommendation**: Re-run with `--glob '!.claude/**'` and note the exclusion in the table.

#### [MAJ-16] D2 and D12 still carry `[FACT: grill-verified]`, the exact evidence class D8's own correction declares invalid
- **Lens**: Inconsistency
- **Affected section**: D2 (line 258); D12 (line 526); v3 changelog
- **Description**: v3's most quotable line is *"**A citation inherited from a review is not
  verification.**"* Two decisions still rest on inherited `[FACT: grill-verified]` tags — and
  D12's is wrong (`verdict.go:43`, MAJ-9) while D2's is right in Go but false on the wire
  (CRIT-2). The lesson was stated and not applied to the other two instances of the same tag in
  the same document.
- **Recommendation**: Re-verify or downgrade both tags. Consider dropping the
  `[FACT: grill-verified]` class entirely in favour of `[FACT — read directly]`, which v3 uses
  correctly elsewhere.

---

### MINOR Findings

- **[MIN-1]** `judge_max_rounds` is not a real field. The real names are
  `PlanBounds.PlanJudgeMaxRounds` and `config.PlanningConfig.PlanJudgeMaxRounds`. Used wrong in
  D4 (twice) and §9 success criterion 7. (r2 MIN-001, unfixed — notable in a naming-precision ADR.)
- **[MIN-2]** §9 step 1 omits `systemAgentIDs` (`pkg/coreagent/core.go:146`, backing
  `IsSystemAgentID`) — membership lives in two places, not one. (r2 MIN-004, unfixed.)
- **[MIN-3]** §9 step 1 also omits `allStaticToolNames` (`core.go:295`). `denyAllThenOverride`
  runs `validateOverrideKeys` (`:347-357`), which **panics** on an override key not in that list —
  so seeding PlanSupervisor's correction-tool grant before registering the name panics the binary.
  (r2 MIN-005, unfixed.)
- **[MIN-4]** D1's re-enforced-invariant list omits `MemoryEnabled=false`, which `seedSystemAgents`
  repairs in both directions and which is a substantive property of an adjudicator. (r2 MIN-006.)
- **[MIN-5]** D1's CONFIDENCE block is stale: `Missing: Whether plan/SKILL.md is usable for
  correction … (G4)` and `Would improve: Reading plan/SKILL.md end to end` — while the body four
  lines above records that it was read and G4 is marked **RESOLVED**. §9 validation step 2 repeats
  the stale instruction. Three references to a question the document answers.
- **[MIN-6]** FR ordering: FR-12 appears after FR-13 and FR-14 (lines 109-114).
- **[MIN-7]** §9 step 5 says *"revise the parked-phase copy"* (singular). The *"There's no in-app
  action for that yet"* string appears twice in `src/lib/planStateColors.ts` — parked and
  **stalled** — and D5 routes the stalled wake to PlanSupervisor too, so both become false; four
  tests assert the string. (r2 MIN-007, unfixed.)
- **[MIN-8]** D5/D10's evidence line says *"store + WS push + **bell**"*. There is no bell — the
  entry point is a `Tray` item inside the sidebar account dropdown, and the unread badge renders
  only when that dropdown is already open. If D10's *"visible"* is load-bearing, an ambient
  indicator belongs in scope. (r2 MIN-011, unfixed.)
- **[MIN-9]** D13 unchanged despite r2 MIN-013: `RevisionEntry` is produced and persisted, and
  `contracts/components/schemas/RevisionEntry.yaml` exists nested under
  `SessionMessageRevisionEntry`, but **no REST route returns it** and the generated producer is
  never called. The work is "wire a dead producer or add a plan-revisions read route", not a
  visibility toggle. G5 is still open and is one grep away.
- **[MIN-10]** No observability section: no metric, no log line, no alert, no runbook for *"plans
  are parked and the supervisor is down"*. The implicit kill switch — setting PlanSupervisor's
  correction-tool policy to `deny` — is never named, so an operator at 3 AM will not know it
  exists. (r2 MIN-010, unfixed.)
- **[MIN-11]** *"correctable"* is still undefined, and §9's headline success criterion turns on
  the word (*"a **correctable** reason"*).
- **[MIN-12]** `Notification.yaml`'s `type` is a closed single-value `enum:` under
  `additionalProperties: false` whose own description says *"Extensible; consumers must tolerate
  unknown values."* The SPA throws on unknown values. D5's gap list should note the contract
  contradicts itself, and that the store hard-coerces out-of-set **severity** but has no
  equivalent for `type`.

---

### Observations

- **[OBS-1]** Split D14 into its own ADR, landed first. Still the right call (r2 OBS-001) and
  now stronger: v3 makes the migrator mandatory, which turns the rename into a change with its
  own data-safety story, its own tests and its own rollback question — none of which it shares
  with the supervision feature. §9 still schedules it **last**, so steps 1-5 are written against
  names step 6 changes.
- **[OBS-2]** `reconstructCorrections` (`pkg/agent/plan_engine.go:3119-3137`) already replays
  supersedes and generations from the intent log at boot. That is a real asset for D13 and D16
  that the ADR does not mention — worth citing, since it means the audit trail survives restart
  without new machinery.
- **[OBS-3]** v3's honest self-correction in the changelog ("that error came from accepting a
  prior review's `[FACT: grill-verified]` citation without opening the file") is exactly the right
  instinct, and D8 opening `compositor.go` is a genuine improvement over v2. The failure was
  stopping at the first `case` in a five-case switch. A useful discipline for v4: when a claim
  turns on a `switch`, quote **every** branch that the described input could reach, not the first
  one that matches the expected answer.

---

## Structural Integrity (structured-spec)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | **FAIL** | §9's eight criteria cover FR-5/6/9/14, NFR-2, D4, D10, D15, D14-migration. **FR-3**, **FR-8**, **FR-12/D14** (largest item) and **NFR-5** have none (MAJ-14). |
| Cross-references consistent | **FAIL** | D8 vs R3 (CRIT-1); D8 vs ADR-056:92-94 (CRIT-1); FR-5 vs §9 step 3 vs §1 vs §5 (CRIT-4); D6/G2/§8 vs R5 (MAJ-12); D14 row 2 vs D14 line 607 (MAJ-13); D7 vs `requireOwner` (MAJ-1); G4 RESOLVED vs D1 confidence block vs §9 step 2 (MIN-5); `verdict.go:43` (MAJ-9); `judge_max_rounds` (MIN-1). |
| Scope boundaries explicit | **PARTIAL** | Non-goals are well named. But §1's blast radius and §5's Option B cost cell still describe a REST route FR-5 deleted, and neither sums D14. |
| Success criteria measurable | **PARTIAL** | Six of eight are good and the supersede criterion is a real improvement over v2's tautology — but it tests a control that is unimplementable as specified (CRIT-3), and *"correctable"* is undefined (MIN-11). |
| Error/failure scenarios addressed | **PARTIAL** | FR-9's a/b/c is a genuine advance on v2 and (a) is verified missing in the code. Still uncovered: agent-vs-human owner resolution (CRIT-2), notification write failure, correction validation failure, concurrent stop during correction, budget exhaustion mid-correction, partial migration. |
| Dependencies between requirements identified | **FAIL** | One `[P1]` and five `[P2]` MUSTs, six ADR-053 decisions and ADR-049 remain uncited (MAJ-5). §9's order still violates Constraint #8 for the notification contract (CRIT-4). |

---

## Test Coverage Assessment

| Category | Gap | Requirement |
|---|---|---|
| **Tool grant** | No test that PlanSupervisor's effective policy for `bash`/`write_file`/`set_config`/`create_agent` resolves `deny`. Given CRIT-1 this is now the highest-value test in the change and the ADR does not name it. | D8, NFR-2, R3 |
| **Integrity** | The stated criterion tests a control the engine cannot express (CRIT-3). Nothing testable exists until D16 picks a feasible mechanism. | NFR-2, FR-14 |
| **Delivery** | No test that an **agent** owner receives an outcome on a readable surface — and no discriminator exists to write the test against. | FR-4, D10 (CRIT-2) |
| **Migration** | v3 mandates the migrator; §9 lists one criterion covering both stores. The lifecycle JSONL half needs its own test and its own rewrite decision. | D14 (MAJ-11) |
| **Prompt regression** | Nothing asserts PlanSupervisor's loaded skill text is free of dead phase values (`SKILL.md:158`) or self-negating instructions (`:231-232`). | FR-3 (CRIT-5) |
| **Budget** | No test distinguishing "correction charged a round" from "the re-judge charged it"; no test of the three-way `judge_rounds_exhausted` overload. | D4 (MAJ-4) |
| **Authorization** | No test that the three `requireOwner` denial branches are indistinguishable. | NFR-3 (MAJ-2) |
| **Concurrency** | `AppendCorrection` holds the process-wide `planDecisionMu` (`:2575`). No correction-vs-stop or correction-vs-judge-round interleaving test. | FR-5 |
| **Contract drift** | `make verify-contracts` fails on any of the contract-touching items (notification `type`, `plan_phase`, verdict verb, `Plan.owner` description) if regen is not atomic. Not in §9. | Constraint #8 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| PlanSupervisor agent | ok | **risk** | ok | ok | ok | **risk** | D15 closes the chat-target path at the session/WS boundary — real progress. Remaining: the tool grant is unenumerated and the ADR now asserts a missing map fails closed when it inherits global `allow` (CRIT-1); `supersede` tampering is acknowledged but its control is infeasible (CRIT-3); the default-agent star still admits system agents (MAJ-6). |
| Correction gate (`requireOwner`) | **risk** | ok | ok | **risk** | ok | ok | Predicate shape still deferred (MAJ-8); two of three denial branches are differentiated against NFR-3's new wording (MAJ-2); the session clause reads a field D7 calls unused and D3 never scopes for PlanSupervisor (MAJ-1). |
| REST correction route | — | — | — | — | — | — | **Correctly removed by FR-5** — the single best scope decision in v3. Reintroduced by §9 step 3 (CRIT-4). |
| Outcome delivery | ok | ok | **risk** | ok | ok | ok | The fork is the right shape; it has no discriminator, so the system can believe it notified while writing to an unreadable file (CRIT-2). |
| Persisted records | ok | ok | ok | ok | **risk** | ok | v3's mandatory migrator materially reduces this vs v2. Residual: the append-only lifecycle JSONL has no rewrite decision (MAJ-11), and the method cannot see the embedded prompt copy (CRIT-5). |
| Plan store (`updateLocked`) | ok | **risk** | ok | ok | ok | ok | Freeze is handler-local; the new tool path does not inherit it (MAJ-10). |

---

## Unasked Questions

1. If a missing per-agent policy map inherits the global `allow` (it does), **what is the test
   that proves PlanSupervisor's grant is actually restrictive?** D8 currently argues one is not
   needed.
2. **How does the engine tell an agent owner from a human owner?** D5 forks on a distinction the
   data does not carry (CRIT-2).
3. **Which pairing does D16 actually mandate**, given `targeted_retry` of a superseded member is
   rejected by `validateMemberRef`?
4. **Does the ADR ship a REST correction route or not?** FR-5 and §9 step 3 disagree.
5. **What happens to `requireOwner`'s session clause for PlanSupervisor**, whose session is not
   `plan:<id>`?
6. **Does the correction increment `JudgeRounds`, or does the re-judge it provokes?** Both is
   double-charging (MAJ-4).
7. **Who rewrites `plan/SKILL.md:158`** when the phase is renamed — and what enforces that it
   happened, given no compiler sees it?
8. **What is PlanSupervisor's SOUL?** It now carries three behaviours (UNMET, stalled, supersede
   discipline) and is still one clause.
9. **Is the lifecycle JSONL migrator a rewrite or an append?** No precedent exists either way.
10. **Which of `Plan.Owner` / `Plan.CreatedBy` is authoritative**, given both are written the same
    value on both paths (r2 MAJ-016, still open, and now load-bearing for D5)?

---

## Verdict Rationale

**BLOCK.**

v3 did substantial and correct work. Dropping `OwnerAgentID` from the rename scope removes the
one-way door that blocked v2. Making the migrator mandatory — with the right precedent named —
converts D14 from a data-loss risk into a scoped engineering task. D15 closes a genuine
privilege-escalation path at the session boundary and repairs the Judge's long-standing
convention-only guarantee at the same time. FR-9's a/b/c turns a bare assertion into three
checkable mechanisms, and (a) is verified missing in `processPlan`. FR-5 dropping the REST route
is the single best scope call in the document. D16 correctly identifies that v2's
byte-identical criterion was a tautology. These are real closures and they should survive into v4.

The block is on five items, and the shape of them is what matters. **CRIT-1** is the ADR's own
headline correction, verified wrong: D8 opened the right file — a real improvement on v2 — read
the first `case` of a five-case switch, and concluded the opposite of what the code does for the
input the risk describes. Its own §7 R3 still states the truth, and ADR-056 still points at D8
for a claim D8 now denies, so the correction did not converge; it split the record in two.
**CRIT-2** and **CRIT-3** are the same failure at one remove: both correctly diagnose an r2
CRITICAL and both propose a remedy the code cannot carry — a fork with no discriminator, a
pairing rule whose second half `validateMemberRef` rejects by construction. In both cases the
ADR's own `Missing:` line asks the question that would have caught it. **CRIT-4** is a scope
deletion that reached one section out of four, so the implementation order still instructs an
implementer to build the unauthorized endpoint FR-5 deleted. **CRIT-5** is the promoted rename
landing inside the adjudicator's own prompt through a surface the stated method cannot see.

Underneath is the round-3 pattern, and it is narrower than v1's or v2's: **v3 fixed what it was
shown and re-derived the wrong answer from the fixed evidence.** That is a better failure than
v1's (inventing what existed) or v2's (not looking), and it is why the remediation list is short
and mostly mechanical. But nine r2 findings are also simply untouched — MAJ-004, MAJ-007,
MAJ-009's method, MAJ-010, MAJ-011, MAJ-015, MAJ-016, MIN-001, MIN-004/005 — and several got
worse when the rename moved into this release. A v4 that fixes CRIT-1 through CRIT-5 and sweeps
those nine should pass; the architecture underneath (Option B, the gate fix, the wake split,
the shared budget) has not been the problem since v2.

### Recommended Next Actions

- [ ] Rewrite D8 to the accurate statement: a missing per-agent map is not a coverage gap and inherits the global `allow` via `compositor.go:191-192`; keep R3; retarget ADR-056's cross-reference rather than changing its (correct) substance — **CRIT-1**
- [ ] Add a principal-kind discriminator to the plan record and fix `Plan.yaml:244-248`'s description; then restate D5's fork against it — **CRIT-2**
- [ ] Replace D16's pairing rule with a feasible control (recommended: deny PlanSupervisor the `supersede` verb, or require `TailMembers` on the supersede case); drop the `targeted_retry` disjunct, which `validateMemberRef` rejects — **CRIT-3**
- [ ] Delete "and REST route" from §9 step 3; fix §1's blast radius and §5's Option B cost; promote the notification contract widening to step 1b — **CRIT-4**
- [ ] Add `plan/SKILL.md:158` to §9 step 6 and amend D14's method to require an `rg` sweep over `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`, `tests/e2e/**` and YAML/MD prose — **CRIT-5**
- [ ] Correct D7's "unused" claim and say what `requireOwner`'s session clause does for PlanSupervisor — **MAJ-1, MAJ-2**
- [ ] Cite the two contracts FR-9(b)/(c) reverse and scope the nil-notifier error to `Start` — **MAJ-3**
- [ ] Settle where the correction round increment happens; update G3 to three causes — **MAJ-4**
- [ ] Add `§ Upstream decisions this ADR reverses` (FR-193 `[P1]`, FR-140/141/186/133/109, ADR-053 D2/D4/D7/§3/§5.1, ADR-049) — **MAJ-5**
- [ ] Extend D15 to `rest.go:2903` and `GetDefaultAgent`; name the exact guard sites and confirm the bus path is unaffected — **MAJ-6, MAJ-7**
- [ ] Draft `PlanSupervisorDefaultRubric` in the ADR; fix D3's predicate to one form; define "unavailable" — **MAJ-8**
- [ ] Fix D12's line reference and wire cost; fix D9's `Missing:` line; split the migrator in two with a JSONL rewrite decision — **MAJ-9, MAJ-10, MAJ-11**
- [ ] Sweep the stale v2 text: R5, D14 line 607, D1's confidence block, §9 validation step 2, `judge_max_rounds`, FR-12 ordering — **MAJ-12, MAJ-13, MIN-1, MIN-5, MIN-6**
- [ ] Add success criteria for FR-3, FR-8, FR-12/D14 and NFR-5; re-measure D14 excluding `.claude/worktrees/` — **MAJ-14, MAJ-15**
- [ ] Add `systemAgentIDs` and `allStaticToolNames` to §9 step 1 (the latter panics if missed) — **MIN-2, MIN-3**
