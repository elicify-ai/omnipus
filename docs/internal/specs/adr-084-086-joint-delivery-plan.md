# ADR-084 + ADR-085 + ADR-086 — Joint Delivery Plan

**Status:** Delivery plan, 2026-09-10 (revision 2 — ADR-085 folded in). Binding on every
implementing agent.
**Merges THREE independently-written wave plans:**
`docs/internal/specs/judge-active-reviewer-spec.md` §Wave Plan (ADR-084),
`docs/internal/specs/browser-control-handover-spec.md` §Wave Plan (ADR-085, waves W0–W8), and
`docs/internal/specs/goal-entity-spec.md` §12 Wave Plan (ADR-086).
**Sources of behaviour:** `docs/internal/architecture/ADR-084-judge-as-an-active-reviewer.md`
(**revision 9**), `docs/internal/architecture/ADR-085-browser-control-handover.md`, and
`docs/internal/architecture/ADR-086-goal-as-a-first-class-entity.md` (**revision 2**).

---

## 1. What this document is, and which authority wins

**Three** large pieces of work are about to be built at the same time in the same tree, by roughly
thirty agents working in parallel. None of the three is implemented today; all three are greenfield.

- **ADR-084** makes the Judge an active reviewer that reads files and forms a view.
- **ADR-085** lets an operator take the browser wheel from a running agent **without the agent's
  turn being cancelled**: the agent's browser calls are deferred and it is told to wait, rather than
  the turn being killed or parked.
- **ADR-086** takes the goal — today fourteen fields hung on a chat session — and turns it into its
  own stored record that a chat and a task both point at.

Each has its own specification, each specification was written without seeing the other two, and
**each contains its own wave plan.** Those three plans overlap on more than twenty-five files. Left
as they are: two agents would create two files that do the same job; three agents would edit one
3,600-line function; two agents would each regenerate the whole contract tree and silently clobber
each other; one agent would build a wire value that another's ADR has already withdrawn; and a
browser test would go red with a message about ADR-057 session routing that names nothing about the
change that broke it.

This document is the single merged plan. Ten analysts read the three specifications against the
actual code — six on the ADR-084/086 pair, four more folding ADR-085 into the result — and reported
where they collide. This document decides every collision, produces one wave graph in which anything
that runs at the same time writes to different files, and states the rules every implementing agent
follows. **An implementing agent is handed this document and its own wave row. It does not need to
reconcile the three specs itself — that has been done here.**

Wave identifiers carry their lane: **F** contracts, **E** engine, **S** storage, **U** SPA,
**T** tests, **G** guards, and **B** the ADR-085 browser lane. Requirement identifiers are prefixed
**JUDGE-** (ADR-084), **BROWSER-** (ADR-085) and **GOAL-** (ADR-086) throughout.

### The precedence rule

Read this in order. The first line that applies, wins.

1. **ADR-086 wins on where a goal lives and on what owns criteria.** The goal record, its identity,
   its status vocabulary, its budget, its criteria and definition-of-done lists, and the fact that a
   terminal goal is a retained record rather than an erasure — all ADR-086.
2. **ADR-084 wins on how the Judge reasons.** What evidence it may read, what grounds a verdict,
   what triggers an adjudication, what the rubric says, and the Judge's degraded-capability
   reporting — all ADR-084.
3. **ADR-085 wins on browser control and on turn parking.** Who holds the browser wheel, how a
   contended browser call is refused or deferred, how many times an agent may retry before it is
   told to stop, what the operator sees while the agent waits, and — most importantly — **that a
   turn is never cancelled, parked or ended because control changed hands.** Where any other
   document implies a browser take-over should stop, cancel or park a turn, ADR-085 overrules it.
4. **ADR-084 revision 9 beats the judge specification wherever they disagree.** The in-tree judge
   spec declares itself an implementation of ADR-084 *revision 7* (its line 5). The ADR is at
   revision 9 and revision 9 withdraws decision D2a in full. Roughly forty rows of the judge spec's
   test matrix, and at least six of its functional requirements, instruct an agent to build a state
   the ADR has deleted. **Where the judge spec and ADR-084 revision 9 disagree, the ADR wins and
   this document records the correction.**
5. **Where this document and any of the three specifications disagree, this document wins for
   delivery sequencing only** — who owns which file, in what order, in which wave, and which wave's
   commit a change rides in. **On behaviour, the specification wins.** If following a wave row would
   make you write behaviour the spec forbids, stop and report it; do not resolve it yourself.

### Operator decisions of 2026-09-11 — rule 0, above everything else

Seven decisions (**D-A** … **D-G**) were taken by the operator on 2026-09-11, after the first grill
round. They sit **above** the five rules in the list — including above the three ADRs and above this
document. Where anything below contradicts one of them, the decision wins and no agent may reopen it.
The full text is in §8; the short form is:

- **D-A — the quiet goal.** The seven-day idle-expiry sweep stays the terminator. The nudge ladder
  gets **no** bound of its own. The fix goes in the prompt text instead: the keeper's re-post and
  nudge text must tell the agent to mark the work complete with the claim tool if it believes it is
  done.
- **D-B — weak proof.** The Judge judges as a human would, on common sense, and **has** the
  authority. A quote that does not verify does **not** flip the verdict. The criterion stays `met`,
  and the Judge must report the missing or failed evidence and justify why common sense still says
  the work is done.
- **D-C — the edit gate.** Acceptance criteria and a definition of done are mandatory at **creation
  and at edit**, uniformly, on every surface.
- **D-D / D-E — the budget.** One global goal-tries setting under Settings → Performance, governing
  task goals and chat goals identically. **No per-goal override exists anywhere.**
- **D-F — no upgrade path, in any of the three designs.** Greenfield is assumed everywhere. No
  migration, no detection, no rescue, no degraded-install reporting.
- **D-G — the held wheel is per tab.** The person keeps the wheel until the agent receives a new
  prompt. If the agent's other work needs a browser meanwhile, it **opens a new tab** rather than
  waiting.

**What these decisions cancel, by name.** Any agent that finds one of these in a specification is
reading a cancelled requirement and must not build it:

| Cancelled | Where it lives | By |
|---|---|---|
| Judge spec **§D — "A `met` with no quote is rejected in code (D2b)"**: **JUDGE-FR-024**, **FR-025**, **FR-026** as a *rewrite* | `docs/internal/specs/judge-active-reviewer-spec.md` §D | D-B |
| **JUDGE-FR-027** survives, but only as a **reporting** obligation — count and WARN, never an outcome change | same section | D-B |
| **GOAL-FR-046** (per-goal budget control) and **GOAL-US-8** ("an operator can give a goal more room") | `docs/internal/specs/goal-entity-spec.md` | D-E |
| **GOAL-FR-051** (boot detection of orphaned goals) and **GOAL-US-12** ("an in-flight goal at upgrade ends visibly") | goal spec | D-F |
| The **`PendingAskJSON` rescue** on upgrade | goal spec, plan wave S2 | D-F |
| **JUDGE-US-8** ("an install running a pre-ADR-084 rubric degrades honestly") | judge spec line 972 | D-F |
| Judge spec **§M — "§I, the mixed state"**, i.e. **JUDGE-FR-077 – FR-080** | judge spec line 2153 | D-F |
| **JUDGE-FR-085's rollout choice** (the recorded choice at judge spec line 3487) | judge spec | D-F |
| Waves **S4** and **U3** in this plan, and the file `pkg/gateway/goal_orphan_boot.go` | §3 below | D-F |

**D-B is corroborated, not invented.** `GOAL-FR-038` and `GOAL-FR-039` in
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/docs/internal/specs/goal-entity-spec.md`
already say the same thing in the same words: FR-038 says a criterion with no machine-checkable form
is the normal case and reaches `met` on the Judge's reasoning alone; FR-039 says the grounding
controls of ADR-084 D2b–D2d "are anti-hallucination controls and MUST NOT be implemented as a proof
gate". The judge spec's §D was the one place that contradicted them. §D loses.

### The one sentence that matters most about ADR-084 revision 9

A criterion's verdict is **met** or **unmet**. There is no `unable_to_verify`, anywhere. A criterion
the Judge could not verify is `unmet` — unproven is not done. But **`met` does not mean a machine
check passed**: the standard is the Judge's reasoned conviction from evidence it actually opened, a
competent human reviewer's standard. A criterion with no test, no diff and no command is the normal
case, and the Judge decides it by reading the artifact and forming a view. Any implementation that
turns the Judge into a checklist evaluator has failed ADR-084 whatever else it satisfies.

### The one sentence that matters most about ADR-085

**Taking the browser wheel never stops the agent.** The turn keeps running; the agent's browser tool
calls are *deferred* with a reason, it is told in words that a human is currently controlling the
browser, and after a small number of attempts it is told to stop trying and do something else. There
is no cancel, no park, no resume dispatcher, and no control toggle. Any implementation in which
taking control cancels a stream, ends a turn, or requires the operator to hand control back before
the agent can continue has failed ADR-085 whatever else it satisfies.

### What was verified against the code, not taken from a spec

Every claim below was checked in the working tree at
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2` on 2026-09-10:

- `pkg/goal/` does not exist.
- Neither `pkg/agent/verdict_projection.go` nor `pkg/agent/verdict_status_projection.go` exists.
- `pkg/agent/task_executor_test.go` does not exist, though the goal spec names it as an existing
  regression file.
- `src/components/workspaces/GoalStatusPill.tsx` does not exist; the real pill is
  `src/components/chat/GoalPillTray.tsx`.
- `contracts/components/schemas/Goal.yaml` **already exists** and is already generated into Go and
  TypeScript, though both wave plans treat the goal wire shape as new.
- `contracts/components/schemas/Task.yaml` and its create/update twins have **no** `dod` field.
- `pkg/gateway/inboundschemas/` holds 338 tracked files and is inside `make verify-contracts`'
  drift check, and appears in no wave plan.
- `scripts/race-packages.sh` does not list `./pkg/goal/...`.
- `Makefile` line 310 reads `lint: golangci-lint-version-check lint-no-removed-providers` — eight
  guard targets exist and `make lint` runs none of them.
- `pkg/api/generated/contract_test.go` contains zero references to `Goal`.
- `pkg/api/generated/goal_dod_minitems_test.go` references `binding_kind` twice and will not
  compile after the rename ADR-086 requires.

Added by the ADR-085 fold, re-verified on the same tree:

- **`src/lib/ws.ts` contains no per-frame-type logic at all.** `grep -cE "case '" src/lib/ws.ts`
  returns **0**. Both parse paths run one `WsFrameSchema.safeParse(raw)` (lines 260 and 310) and
  gate unknown types on `WsFrameTypeSchema.options` (line 358), both imported from the generated
  tree. A new frame is validated and forwarded with **zero** `ws.ts` edits. No wave edits this file.
- `contracts/components/schemas/BrowserStatusFrame.yaml` **already** carries `released` in its state
  enum (line 21) and `control_only` (line 49), and `browser_status` is already in the generated
  `WsFrameType` enum. **BROWSER-FR-031b needs no contract change.**
- `src/components/browser/BrowserLiveView.tsx` imports nothing from the generated tree and nothing
  from `@/lib/ws` — only `@/store/ui` and `@/store/chat` (line 80). The browser panel rebuild has
  **no contract dependency**.
- `tests/e2e/shards.json` exists and has a `ui-heavy` shard (`key_slot: c`, `port: 6065`,
  `solo: true`) already holding `browser-live-video.spec.ts` and `uat-browser-panel.spec.ts`. The
  file appears in **none** of the four documents — `grep -n "shards.json"` over all four returned
  zero hits — while `.github/workflows/pr.yml`'s required `e2e-shard-check` job fails on every PR if
  any spec is unassigned.
- `pkg/gateway/websocket.go` appears **zero** times in this plan (`grep -c` → 0), so ADR-085 can own
  it outright. Its ADR-057 artefact anchor is real and present at line 3547.
- `scripts/` holds **twelve** `check-*.sh` files. One (`check-no-removed-providers-selfcheck.sh`) is
  another guard's proof-of-failure companion, not a guard; one (`check-dead-code.sh`) is referenced
  by nothing and builds a whole-program call graph. **Ten** are real, discoverable guards today.
- `pkg/agent/browser_deferral.go`, `pkg/tools/browser/tools_handover.go`, `ToolResult.Deferred`,
  `ToolRootChatSessionID`, `IsControlledByLiveViewer`, `ControlGatedToolNames`,
  `SetBrowserWheelReleaseHook`, `WaitingSurfaceEmitter`, the bus `OperatorPrompt` field and
  `ControlIdleReleaseSec` **do not exist yet** — every one confirmed absent by grep.
- The **name** of ADR-085's new waiting-surface frame appears nowhere in its spec. See OQ-23.

Several of these were established by a command that deliberately found nothing. **A shell tool
reporting a non-zero exit on `ls pkg/goal`, or `grep -c` returning `0`, is the evidence, not a
failure.**

---

## 2. Resolved collisions

Every conflict the ten analysts found, merged across all of them and de-duplicated, with the
decision and the wave that owns it. Severity is the consequence of getting it wrong: **blocker**
means the tree ends up incorrect, non-compiling, or silently green while broken; **major** means a
wave has no owner, two waves fight, or a requirement lands nowhere; **minor** means an agent wastes
effort or leaves a small defect.

Rows **C-01 … C-66** are the original ADR-084/086 reconciliation. Rows **C-67 … C-90** are the
ADR-085 fold. Where the fold **revises** an earlier row, the earlier row carries an inline
`REVISED BY THE ADR-085 FOLD` note pointing at the new row; nothing was silently overwritten.

### 2.1 Where two analysts disagreed, and which answer this document takes

Eight collisions in the original pass, and five more in the ADR-085 fold, were resolved two
different ways. Each is decided here, with the reason.

| # | The disagreement | Decision | Reason |
|---|---|---|---|
| D-a | Who owns `pkg/task/verdict.go` and `pkg/task/criterion.go` — the contracts analyst put them in their own wave after the contract commit; the engine-files analyst put them **inside** the atomic contract commit, citing Constraint #8. | **Their own wave, F2, after F1.** | `make verify-contracts` runs `git diff --exit-code` over `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/` and `pkg/gateway/inboundschemas/` — it does not reach `pkg/task`. Constraint #8's atomicity requirement therefore does not bind these two files. Keeping F1's commit purely schema-plus-generated means any future `verify-contracts` drift failure is unambiguous. |
| D-b | The order of the two additions to `pkg/gateway/gateway.go` — storage said the goal boot sweep first, engine-files said the Judge rubric self-check first. | ~~**E11 (rubric self-check) first, then S4 (boot sweep + active-goal counter).**~~ **SUPERSEDED — D-F (2026-09-11): there is no order left to decide.** | S4 is retired and the rubric self-check is retired, so `pkg/gateway/gateway.go` has exactly **one** writer in the delivery: **E11**, and it writes exactly **one** region, the `RegisterActiveCounter("goal", …)` closure. Nothing is rebased onto anything. |
| D-c | Judge FR-020a's aggregate withholding counter — the storage analyst listed it among the oracles to restate onto the goal record's attempt counter; the projection analyst called it retired. | **Retired. No wave delivers it.** | ADR-084 revision 9's retirement table names it directly: *"FR-020a's aggregate withholding bound … existed to bound a state that no longer exists."* There is nothing left to count. |
| D-d | Whether the verdict projection edits `pkg/agent/judge.go` — engine-files put `judge.go` in the projection wave's write-set; the projection analyst said the projection must never touch it. | **The projection never touches `pkg/agent/judge.go`.** | `finalizeVerdict` computes a verdict; it holds no store and no lock and is also reached from the fail-closed invalid-input branch. Writing there would stamp `unmet` on every criterion because a *caller* passed a malformed input. Dropping `judge.go` also lifts the projection out of that file's three-wave chain. |
| D-e | Whether the projection may edit `pkg/agent/goal_triggers.go` — engine-files forbade it, the projection analyst required it. | **The projection wave (E14) owns `goal_triggers.go`, last in that file's chain.** | The goal-path projection must swap `runGoalAdjudication`'s post-verdict emission from the criteria-less `emitGoalStatusFrame` to the criteria-carrying form, or the ticks it just persisted never reach the SPA. That swap is inside `goal_triggers.go`. E14 is last on the file, so it costs no extra serialisation. |
| D-f | Goal FR-042 — the goal spec, and the storage analyst, read it as an instruction to edit four contract copies; the projection and contracts analysts read it as a guard obligation. | **Guard only. No `.yaml` edit for criterion status, no regeneration, no contract commit for it.** | Verified: all four copies already read `[pending, met, unmet]`. Goal FR-037 three lines above FR-042 forbids adding a fourth value. An edit that changes nothing is a wave with no work; the drift FR-042 actually fears is caught by an equality test, which is what T1 ships. |
| D-g | The `pkg/session` goal-field deletion — the storage analyst accepted a wave that deliberately leaves `pkg/agent` non-compiling for later waves to fix. | **Split in two: S2 relocates `PendingAskJSON` additively (tree stays green); S6 deletes the goal fields, after every reader is re-pointed.** | A wave that cannot be green cannot be merged, cannot be reviewed on its own, and forces roughly a dozen waves into one enormous pull request. The additive-then-delete split is the ordinary way to do this and it costs one extra wave. |
| D-h | Whether goal FR-028's fourth terminal ending (idle expiry) needs a distinct value on the display pill as well as on the record. | **Added to both, and flagged as an open question for the operator.** | Adding to both satisfies either reading. Removing it later is a one-line change to F1 *before F1 lands* and a full regeneration afterwards, so the cheap moment is now. See open question OQ-04. |
| D-i | Whether ADR-085's W1+W2+W3 become **one wave under one agent** (guards analyst) or **three agents on one shared branch producing one pull request** (engine analyst). Both agreed it is one merge. | **One scheduling slot named `B123`, three named lanes, ~~three agents~~ ONE AGENT (D-I), one branch, one pull request.** The lanes keep the analysts' disjoint per-file write-sets. **AMENDED 2026-09-11 by D-I:** the operator's cap is **six AGENTS per round**, not six slots, and three agents inside one round-5 slot made that round eight agents wide. The three lanes are therefore worked **sequentially by one agent** (lane 1 → lane 2 → lane 3) on the one branch. The rationale below for *not* collapsing the lanes — ~40 files under one agent — is acknowledged and overridden by the cap: the work is the same size, it is now done in sequence rather than in parallel, and §4 states the wall-clock cost. | The compile cycle B1→B2→B3→B1 is real and irreducible, so one merge is forced. But collapsing it to one agent would put ~40 files across three packages, including the two riskiest engine files in the delivery, under a single agent. Three lanes with disjoint files preserves agent-sized work; one branch and one PR preserves the round rule that every round ends with a compiling tree. **Do not break the cycle with stub symbols** — a stub `ControlGatedToolNames()` returning an empty slice is a silently un-gated control gate, which is the exact defect BROWSER-FR-016a exists to prevent. |
| D-j | The order on `pkg/agent/loop.go` and `pkg/agent/turn.go`: **E2 → E13 → B123** (engine analyst, so the browser wave computes its ADR-057 counts against a settled file) or **E2 → B123 → E13** (guards analyst, so the browser lane costs no extra round). | **E2 → B123 → E13.** | The engine analyst's reason for putting the browser lane last is the ADR-057 consumer-set test's exact counts. But **both** analysts independently constrain E2 and E13 to add **zero** new `routingSessionID` reads to those two files — and with that constraint held, the counts are settled the moment B123 writes them, whichever order runs. Given the constraint, guards' ordering is strictly cheaper: B123 runs concurrently with E12 in round 5 and the browser lane costs **zero** extra rounds. The constraint is carried verbatim onto E2 and E13 (C-71), with an explicit escape hatch: if E13 genuinely needs such a read, E13 performs the whole four-part amendment in its own commit and says so — it does not leave it for anyone to discover. |
| D-k | Where ADR-085's SPA browser-panel rebuild (`BrowserLiveView.tsx`) runs: **round 1, no dependencies** (SPA analyst, verified it imports nothing generated) or **round 6, after the contracts PR** (guards analyst). | **Round 6 — but for a scheduling reason, not a dependency reason.** | The SPA analyst is factually right: `BrowserLiveView.tsx` imports only `@/store/ui` and `@/store/chat`, and `BrowserStatusFrame.yaml` already carries `released` and `control_only`, so **B5 has no contract dependency and its row says so.** It sits in round 6 only because rounds 1–5 are already at the six-agent cap and because B5 *reads* `src/store/chat.ts`, which F1⊕U2 is rewriting in round 1 — reading a store while another wave rewrites it buys nothing when the critical path is eight rounds either way. **If a round before 6 frees a slot, B5 may move earlier without any other change.** |
| D-l | Who owns `src/lib/ws.ts`. Two analysts assigned it to the browser thread-rendering wave; one proved by grep that it needs no edit at all. | **Nobody. No wave edits `src/lib/ws.ts`.** | The proof is specific and re-verified here: `grep -cE "case '" src/lib/ws.ts` returns 0; both parse paths use one generated `WsFrameSchema.safeParse` and one generated `WsFrameTypeSchema.options` check. Every wave that thought it needed `ws.ts` was reasoning from the file's name, not its contents. Consumers import the new frame type from `@/lib/api/generated/asyncapi-types`, exactly as `src/store/chat.ts` already imports `GoalStatusFrame`. This turns a three-way contested file into a zero-way one. |
| D-m | The new frame's name: `BrowserHandoverNoticeFrame` / `browser_handover_notice` (two analysts), `BrowserHandoverWaitingFrame` (one), `BrowserWaitingSurfaceFrame` (one). | **`BrowserHandoverNoticeFrame` / `browser_handover_notice`, provisionally.** | It is the only candidate *derived* rather than invented: the ADR-085 spec's own test filenames are `ChatScreen.browser-handover-notice.test.tsx` and `chat.browser-handover-notice.test.ts`, its e2e observable is `[data-testid="browser-handover-notice"]`, and its replay test is `TestReplay_HandoverNoticeReplaysAsTheSameFrameType`. **The spec still names no type string**, so this remains OQ-23 and must be confirmed before F1 writes. Changing it before F1 lands is one line; changing it afterwards is a second four-tree regeneration. |

### 2.2 The full collision table

| # | Collision | Sev | Resolution | Wave |
|---|---|---|---|---|
| C-01 | **A fourth criterion status.** Judge FR-076 extends `AcceptanceCriterion.status` to `[pending, met, unmet, unable_to_verify]` in four contract copies, in `pkg/task/criterion.go`, and as a third icon branch in the SPA. Goal FR-037 forbids any fourth value anywhere. Found by five of six analysts. | blocker | **Three values stand: `pending`, `met`, `unmet`, where `pending` means "not yet judged".** No YAML edit, no `IsValidCriterionStatus` extension, no third `CriterionStatusIcon` branch, no `sanitizeCriteria` change. Judge FR-076 collapses to its step 2 (the writer) alone; steps 1 and 3 are cancelled, as are `TestCriterionStatus_UnableToVerifyIsValid`, `pkg/task/criterion_adr084_test.go`, and the three `unable_to_verify` vitest rows. This is not ADR-086 overruling ADR-084 — ADR-084 revision 9 §10 withdraws it itself. | T1 locks it; G2 guards it |
| C-02 | **`CriterionVerdict.outcome`.** Judge FR-070/FR-070a add an `outcome` enum to the wire type and the Go struct. ADR-084 revision 9 retires it by name and states "`CriterionVerdict.Met` stays a bool". | blocker | **Four new optional fields, not five: `evidence_source`, `evidence_target`, `provenance`, `evidence[]`. `outcome` is added nowhere** — not to `CriterionVerdict.yaml`, not to `JudgeVerdictFrame.yaml`, not to the `asyncapi.yaml` inline copy, not to `pkg/task/verdict.go`. `met` stays a required bool. No engine wave computes an outcome enum. | F1 (schema), F2 (Go), E9 (populates) |
| C-03 | **Two files for one projection.** Goal W7 creates `pkg/agent/verdict_projection.go`; judge W4a creates `pkg/agent/verdict_status_projection.go`. Both write the Judge's per-criterion result onto criterion status. Found by four analysts. Neither file exists today. | blocker | **One file: `pkg/agent/verdict_projection.go`, one wave: E14.** `pkg/agent/verdict_status_projection.go` is never created. Judge W4a ceases to exist as a wave; its surviving obligation (the writer) is absorbed into E14, and its refusal/CAS reasons on the task run record land in the same E14 edit because they are two lines in the same function. A merge guard (`scripts/check-single-verdict-projection.sh`) fails the build if the second name returns. | E14; guard in G7 |
| C-04 | **Two waves each regenerate all four generated trees.** Goal W1 and judge W3 are each declared dependency-free and each own `contracts/asyncapi.yaml`, `pkg/api/generated/**` and `src/lib/api/generated/**`. | blocker | **One agent owns the entire contracts change-set as one wave and one commit (F1). It is not splittable.** `scripts/gen-contracts.sh` rewrites all four trees from whatever is in `contracts/` at that moment; two agents regenerating against half the schema edits each produce mutually clobbering diffs, and whichever lands second reverts the other's generated half while leaving its YAML in place — reported by `make verify-contracts` as a failure in files nobody edited. **REVISED BY THE ADR-085 FOLD (C-67): ADR-085's W4 is a *third* wave with the same write-set and is dissolved into F1 as well.** | F1 |
| C-05 | **`Goal.yaml` already exists and contradicts ADR-086.** Both plans treat the goal wire shape as new. The shipped schema has two separate budgets, a `plan` binding kind, a four-value state enum, and none of ADR-086's fields. Found by three analysts. | blocker | **F1 RESHAPES the existing file; it does not create a new one.** `binding_kind`→`owner_kind` narrowed to `[session, task]`; `binding_id`→`owner_id`; ~~`attempts_max` + `judge_rounds_max` collapse to one `max_rounds` plus an optional per-goal override~~ **RETIRED BY D-E (2026-09-11), in part: the collapse STANDS, the override does NOT.** `attempts_max` + `judge_rounds_max` collapse to ONE `max_rounds` **and nothing else — no per-goal override field enters `Goal.yaml`, `Task.yaml`, `TaskUpdateRequest.yaml` or any other schema, now or later**; `state` widens to distinguish ADR-086 FR-028's four terminal endings; add `active_session_id`, `attempts_used`, `latest_reason`, `terminal_reason`, `started_at`, `last_activity_at`, the two durable keeper counters, `superseded_criteria`, and the two surviving routing fields. `dod`'s `minItems: 1` is kept. The rename is safe — nothing outside generated code reads `binding_kind` — but `pkg/api/generated/goal_dod_minitems_test.go` uses it twice and F1 must update it in the same commit or the tree will not compile. | F1 |
| C-06 | **Erasure deletes the projection one statement later.** On a met verdict `runGoalAdjudication` calls `clearGoal`, whose single `SetMeta` patch zeroes `GoalCriteriaJSON` among ten fields. A projection written before that change produces a green test suite and a blank goal card. | blocker | **The terminal-transition wave (E8) lands before the projection wave (E14), and that ordering is not negotiable.** Ending a goal becomes a status transition on a retained record. Judge FR-102's and FR-096's claims that the terminal frame is emitted "via `clearGoal`" are restated: it is emitted by the goal-record status transition, which retains criteria and their final statuses. `clearGoal` survives only as the explicit-operator-clear transition and stops zeroing fields. E14's test must assert the surviving record, not the projection call, so it cannot pass vacuously. | E8 before E14 |
| C-07 | **Every judge wave that touches goal state is written against storage that is being deleted.** Judge W4a, W10 and W11 read and write `meta.GoalID`, `meta.GoalCondition`, `meta.GoalRoundsUsed`, `meta.GoalMaxRounds`, `meta.GoalCriteriaJSON` through `session.UnifiedMeta`. ADR-086 moves all of it into `pkg/goal`. | blocker | **Hard edge: the goal store (S1) and the goal-record seam re-point (E4) must be merged and green before any wave that reads or writes goal state starts.** No wave may be authored against `session.UnifiedMeta`'s `Goal*` fields. Judge waves that touch no goal state — the contracts lane, the budget/capture lane, the rung deletion, the rubric — interleave freely. This is encoded in the merged graph's dependencies, not left as advice. | S1, E4 |
| C-08 | **`JudgeCriteriaInput.GoalSessionID` is three different identities.** It is simultaneously the scope-correlating id, the verifier-registry and cancel key, and the transcript-window root. A task-owned goal in its definition phase has no session at all, so it cannot be a session id. | blocker | **Split the field.** `JudgeCriteriaInput` gains `GoalID` (the scope-correlating id, and the only thing `verifierUnitID`'s goal arm keys on) and keeps a renamed `GoalActiveSessionID` for the transcript window and descendant root only. `validate()`'s goal arm requires `GoalID`, and per ADR-086 permits `TaskID` alongside it for a running task's goal — the shipped mutual-exclusion rule becomes wrong and must be removed. `cancelGoalVerifierIfAny` takes a goal id. An empty `GoalActiveSessionID` at adjudication time is a programming error and must be logged, never silently producing an empty window. | E9 (field), S1 (id namespace) |
| C-09 | **Nine judge oracles are phrased in terms of a field ADR-086 deletes.** The judge spec's strongest safety property — the Unavailable branch never consumes a round — is written as "never touches `GoalRoundsUsed`". | blocker | **Restate every such oracle as: the goal record's attempts-used counter, read back from `pkg/goal`'s store after the call, is unchanged.** Affected judge FRs: FR-018a, FR-019, FR-020a *(itself retired — see D-c)*, FR-093, FR-095, FR-097, FR-101, FR-102, FR-103. S1 must expose a read accessor and a single-writer update path so the oracle has one observable. A test that reads a cached in-memory value would pass on an implementation that never persisted — the locking changed (a striped mutex plus sidecar flock, not the session shard), so the oracle must observe the store. | S1 exposes; T2/T3 assert |
| C-10 | **`pkg/gateway/inboundschemas/` is unowned and inside the drift gate.** It is a 338-file copy of `contracts/components/schemas/`, regenerated by `scripts/gen-contracts.sh` step 5, and named in `make verify-contracts`' `git diff --exit-code` list. Neither wave plan mentions it. | blocker | **Added to F1's write-set and committed in the same atomic commit.** It is never hand-edited; it is produced by running `scripts/gen-contracts.sh`, which the same agent runs once at the end of F1. | F1 |
| C-11 | **A task's definition of done has no wire surface.** Goal FR-021/FR-047/FR-048/FR-054 require a definition-of-done list on both task screens with an HTTP 400 naming which list is missing. `Task.yaml`, `TaskCreateRequest.yaml` and `TaskUpdateRequest.yaml` have `criteria` and no `dod`; there is no `/goals` REST path and no goal function in `src/lib/api.ts`. | blocker | **F1 adds `dod` to all three task shapes**, mirroring the existing `criteria` field (`$ref` `AcceptanceCriterion.yaml` on `Task`, `AcceptanceCriterionInput.yaml` on the two request shapes). **No `minItems: 1` in the schema** — the 400 must name *which* list is missing, which a schema violation cannot produce, so it is a handler rule. F1 also rewrites `Task.yaml::criteria`'s description, which currently advertises the soft-tier fallback that FR-047 removes. The gateway projects both lists onto the goal record. Every SPA wave except the pill and badge waves is blocked until this lands. | F1 (wire), E5 (handler) |
| C-12 | **`finalizeVerdict` is not a write path.** Judge W4a lists it as a projection call site. It receives only ids, holds no store and no lock, and is also reached from the fail-closed invalid-input branch. Its two real callers deliberately discard a verdict that a concurrent Stop invalidated. | blocker | **The projection runs at the three verdict-RECORDING sites, each strictly inside that site's existing applicability gate:** `task_executor.go::adjudicateClaim` after the `taskVerdictStillApplicable` gate; `plan_engine.go::applyJudgeRoundOutcome` inside the `planDecisionMu` critical section after the fresh running-state re-read; `goal_triggers.go::runGoalAdjudication` after the `jr.Unavailable` early return. A projection above those gates writes ticks for work that was cancelled. | E14 |
| C-13 | **Two waves specify opposite fates for the same two predicates.** Goal FR-017 extends the bounded zero-output push, tested through `goalZeroOutputTripleHolds`; judge FR-097 deletes that predicate and `sessionHasTranscriptOutputSince` and tests that they are unreferenced. | blocker | **ADR-084 wins — when the Judge is triggered is its domain. The predicates are deleted; the behaviour goal FR-017 wants survives inside the unified push ladder and must be owner-agnostic there.** One test replaces both: a task-owned and a chat-owned goal each receive the push, both bounded at the same count, and the push itself consumes zero rounds. The negative half (the two symbols are unreferenced) moves out of Go and into a guard script, because a Go test asserting a symbol is unreferenced is a source-text scan and cannot survive a merge. `pkg/agent/goal_keeper_repairs_test.go` calls `goalZeroOutputTripleHolds` directly and must be rewritten in the same commit as the deletion. | E13 deletes; T2 tests; G6 guards |
| C-14 | **Six waves claim `pkg/agent/goal_triggers.go`, and their regions overlap.** Goal W4, W8, W9 and judge W4a, W10, W11. Goal W4's `clearGoal` call at the bare-claim path sits *inside* the function judge W10 guards; judge W11's `settleGoalNormally` sits between two other waves' edits. | blocker | **Exactly four waves write this file, strictly in this order: E8 (terminal transition + frame signature) → E12 (keeper unification, drivers, maps, routing helpers) → E13 (claim resolution, `settleGoalNormally` rewrite, unified push ladder, the two deletions) → E14 (the projection call and the criteria-carrying frame swap).** Judge W10's call-site guard folds into E13 rather than being its own wave, because the guard and the settle rewrite are the same ~700-line span. | E8→E12→E13→E14 |
| C-15 | **Four waves claim `pkg/agent/goal_loop.go`, two of them inside the same switch statement.** The judge plan itself notes W10 and W11 are "both inside `checkGoalLoopAfterTurn`'s switch. Same region." | blocker | **Exactly three waves write it, in order: E8 (terminal paths, the `clearGoal` call-site removals, the frame definitions) → E12 (keeper gates) → E13 (claim resolution, the blocked park, deferred-dispatch handoff, deliverer swap — all inside `checkGoalLoopAfterTurn`, one wave, one agent).** Judge W10 and W11 must not be two agents on one switch. | E8→E12→E13 |
| C-16 | **`emitGoalStatusFrame` is not defined in the file the judge plan assigns it to.** Judge W4a names `pkg/agent/goal_triggers.go` for the frame change; the three definitions live in `pkg/agent/goal_loop.go`, and three further call sites live in `pkg/agent/goal_record_wiring.go`, which appears in no wave's write-set. | blocker | **The goal-status frame signature change is one commit spanning all three files, owned by E8.** A Go signature change and all its callers must be one commit or the tree does not compile. E8 is the earliest wave that already owns `goal_loop.go`, so it costs no extra serialisation. No later wave may change these signatures. | E8 |
| C-17 | **The new pill states break two deliberate typecheck tripwires.** `GoalPillTray.tsx` and `GoalIndicator.tsx` each narrow the state enum through a `const exhaustiveCheck: never = state` default whose own comment says a new enum value must fail typecheck. The contract commit therefore turns `npm run typecheck` red in files a different wave owns. | blocker | **U2 is the sole owner of the SPA half of the state enum — both components, both tests, and `src/store/chat.ts` — and F1 and U2 merge in ONE pull request.** Between them the tree does not typecheck, so they cannot be separate merges. `blocked` and `claim_overturned` are **not** added to `GOAL_TERMINAL_STATES`, so the terminal-pill display timer does not fire on either. **REVISED BY THE ADR-085 FOLD (C-68): the one-PR rule contradicted the round table, which put F1 in round 1 and U2 in round 4. U2 moves to round 1 and F1⊕U2 is one scheduling slot.** | F1⊕U2, one PR, round 1 |
| C-18 | **The judge specification is two ADR revisions behind itself.** Its header cites ADR-084 revision 7; the ADR is at revision 9, whose §10 retirement table removes six things the spec still instructs an agent to build. A grep for "revision 9" across the spec returns nothing. | blocker | **Treat ADR-084 §10 as overriding the judge spec on outcome vocabulary.** Four judge FRs are dead and must not be implemented: FR-070's `outcome` field, FR-076 steps 1 and 3, FR-020a, and FR-018a's per-criterion return-shape change. `UnableToVerifyTracker`, `NonVerdictUnableToVerify` and `UnjudgeableEscalationGate` **stay exactly where they are** — internal to the deterministic rungs and the verifier-turn outcome, never surfaced as a criterion result — and must not be deleted. A guard makes the withdrawal mechanical rather than advisory. | G2 |
| C-19 | **Four waves each edit the same three CI wiring files.** Judge W1, W9 and W11 and goal W14 all list `Makefile`, `.github/workflows/pr.yml` and `deploy/ci-worker/runci.sh` — an append-only list of guard invocations where a bad merge resolution silently drops a guard rather than failing. | blocker | **G1 lands first and is the sole owner of all three files for the whole delivery.** It introduces `scripts/guards.sh`, which discovers every `scripts/check-*.sh`, runs each guard's proof-of-failure companion before the guard itself, runs all of them rather than stopping at the first failure, prints one `name exit=N` line each, and fails if any guard failed, if zero guards were discovered, or if a non-exempt guard has no proof-of-failure path. The three wiring files then each hold exactly one guard line forever. **After G1, adding a guard is adding a file** — no shared-file edit, no merge conflict, and the CI worker picks up new guards from its own checkout with no `runci.sh` redeploy. This is the repo's own established answer, already applied by `scripts/race-packages.sh` and `scripts/check-vitest-coverage.mjs`. | G1 |
| C-20 | **The third verdict write path has no owner.** Goal FR-040 names three write paths and goal W7's write-set omits two of them; judge W4a names three call sites and its list is a different three. The production `JudgeCriteria` call sites are exactly three: `goal_triggers.go:448`, `task_executor.go:1189`, `plan_engine.go:1737`. | major | **Three write paths, and they are exactly those three call sites, all in E14's write-set.** Goal FR-040 is right about the count and wrong about the third one's name: the plan judge round judges the plan's own `plan.Plan.DoD`, never a member task's, so read FR-040's third path as "the plan record's DoD". The plan path's write target stays `pkg/plan` — ADR-086 D5 defers plan-DoD convergence. | E14 |
| C-21 | **The de-union cannot be derived from criterion ids.** Goal FR-041 says split the Judge's flat outcome list back onto `Criteria` and `DoD` by id, but only the two definition-of-done *floor* items carry stable ids; every LLM-compiled DoD item is a plain UUID from the same minting function as a criterion. | major | **Record membership where it is known instead of re-deriving it where it is not.** `compiledGoalCriteriaFor` gains a second return value — an origin table parallel to the returned union, each entry naming the source list and the index within it. The projection resolves each verdict's criterion id through that table and writes by index. `originSynthetic` (`goal-condition`, `soft-tier-implicit`) resolves to an explicit logged no-op. **The projection contains no id-prefix parsing and no UUID-shape test anywhere.** The origin table also makes a duplicate id across the two lists detectable, which the flat list hides. | E14 |
| C-22 | **A correct store write still produces a blank goal card.** `runGoalAdjudication`'s post-verdict emissions use `emitGoalStatusFrame`, which passes `nil` criteria; only `emitGoalStatusFrameWithCriteriaAndDoD` carries the breakdown, and its only callers are in `goal_record_wiring.go`. | major | **After a successful goal-path projection, `runGoalAdjudication` re-reads the just-updated goal record and emits through the criteria-carrying form** (or routes through `afterGoalRecordWrite`, which already does that re-read-and-emit). This is inside `goal_triggers.go`, which E14 already owns. The SPA half stays with U5; E14 delivers it a frame that actually carries per-criterion statuses. Without this the merged work passes its tests and shows nothing — the exact failure ADR-086 D9 exists to prevent, arriving through a different door. | E14 |
| C-23 | **Judge FR-091 claims `handleBareGoalClaim` is unchanged.** Its body reads four session-meta goal fields, writes a fifth, and calls `clearGoal` — all of which ADR-086 removes. | major | **The function's body changes under ADR-086, and that change belongs to the keeper wave (E12), not to the claim-resolution wave.** E13 keeps only its call-site guard. The bare-claim streak map re-keys to the goal id under goal FR-052, so the function's arguments change meaning too. Leaving FR-091's "unchanged" claim standing would have two waves silently sharing one function with no declared serialisation point. | E12 |
| C-24 | **Judge FR-096 keeps three functions whose signatures ADR-086 changes.** The quiet-window sweep selects sessions on `GoalCondition != ""` — a field a task session never sets — and `maybeSettleGoalIdle` takes a `*session.UnifiedMeta`. Goal FR-015 requires task-owned goals to be visible to this sweep, which is impossible while that is the selector. | major | **FR-096's eight surviving behaviours are preserved as the regression contract; the signatures in it are not.** The sweep enumerates active goal records from `pkg/goal`'s store, and `maybeSettleGoalIdle` takes the goal record plus the session. S1 must expose a list-active accessor so the sweep has a selector that does not go through session meta. | S1, E12 |
| C-25 | **`GoalCondition != ""` is the de-facto "does a goal exist" predicate in five places** and neither wave plan enumerates all five. One of them is the gateway's active-goal admission counter, which goal FR-049 separately changes. | major | **S1 defines and exports the single predicate — a goal record exists for this owner with status active — and all five sites re-point to it.** The gateway counter closure is owned by **E11** (S4 is retired — D-F); it is the same closure FR-049 must change to exempt task-owned goals, and no engine wave may edit that line. The other four belong to engine waves sequenced after S1. Missing one produces a silent wrong answer: the gateway counter would return zero forever after the field is gone, quietly disabling the global active-loop cap. | S1 defines; S4 owns the gateway line |
| C-26 | **Goal retention would invert the session lock order.** Goal FR-043 sweeps goal records "under the same retention rule and schedule as sessions", but `UnifiedStore.RetentionSweep` holds all 64 session shards for its entire body. Taking a goal-store lock inside that body creates a second lock class held under every session shard. | major | **Lock order is `goalLock(goalID)` → `sessionLock(sessionID)` → `cacheMu`, one-directional, documented in both `pkg/session/unified_lock.go`'s existing order comment and `pkg/goal`'s package doc. The goal retention sweep is a SEPARATE pass, run immediately after `RetentionSweep` returns and outside its shard hold** — same schedule, same retention-days argument, not the same critical section. Nothing inside `lockAllSessionShards` may take a goal lock. Both places must state the Windows posture: `fileutil.WithFlock` is a no-op on Windows, so the goal store's cross-process guarantee is POSIX-only. `go test -race` is not a lock-order checker, so this must be prevented structurally, not tested for. | S2 (doc), S3 (pass) |
| C-27 | **`pkg/gateway/gateway.go` has three claimants with no declared order:** the rubric self-check call, the goal boot sweep, and the active-goal counter closure. Goal W6, which owns FR-049, does not include `gateway.go` — so the exemption had no owner at all. | major | ~~Two waves, in this order.~~ **SUPERSEDED — D-F (2026-09-11): one wave, one region.** Of the three claimants, two are retired — the rubric self-check with judge spec §M, and the boot sweep with GOAL-FR-051 and wave S4. The survivor is the **active-goal counter closure**, and it belongs to **E11**, which is now `gateway.go`'s sole writer for the whole delivery. Nothing else in the file is touched by any wave. | E11→S4 |
| C-28 | **Two functions read the goal budget from session meta with a duplicated hardcoded fallback**, and three judge waves edit around both. Goal FR-025 replaces this with a per-goal override. | major | ~~**S1 persists the override on the goal record and rejects a value below 1 at the store boundary, so an API or tool write cannot persist 0. The config signature gains the override parameter (E6), and both read sites re-point to it (E12).**~~ **RETIRED BY D-E (2026-09-11) — NQ-2 CONFIRMED.** There is no per-goal budget override anywhere in the product, so there is nothing to persist, nothing to pass and nothing to re-point. **S1 adds no budget field to the goal record. E6 does not add an override parameter to the config signature — `EffectiveGoalMaxRounds()` keeps the signature it has today. E12 creates no second read site.** **What SURVIVES of this row, and is still this row's obligation:** *no wave may write a new `config.DefaultGoalMaxRounds` fallback into these files* — the duplicated hardcoded fallback is still deleted, and both existing read sites read the ONE global Settings → Performance value through the unchanged `EffectiveGoalMaxRounds()`. The task path's 2× hard ceiling applies to both owner kinds. *(The old closing line — "an override persisted but never read still returns 20" — described a hazard that no longer exists, because no override is persisted.)* | S1, E6, E12 |
| C-29 | **`pkg/config/defaults.go` has three writers** — judge timeout/cap keys, the `goal_claim` policy-ceiling entry, and the goal budget defaults — in one 859-line file every boot path reads. | major | **One owner for the whole delivery: E1, all three additions in one commit.** Every edit is an additive static literal whose value is known from the specs up front; none needs its consumer to exist first. This turns a three-deep rebase chain into one commit and unblocks the budget and claim-tool waves a full round earlier. **REVISED BY THE ADR-085 FOLD (C-70): a fourth addition, `browser_handover`'s global-ceiling entry, joins the same commit. `ControlIdleReleaseSec` gets NO `defaults.go` entry.** | E1 |
| C-30 | **A split that risks a boot panic.** Judge W10 bundles the `goal_claim` tool implementation with `allStaticToolNames` and the six per-agent seed maps. If a seed map gains the name in a commit where the catalogue does not, `validateOverrideKeys` **panics** at boot — not an error return, a panic. | major | **Split along the panic boundary. E1 owns the static-data half in one atomic commit: `allStaticToolNames` + the six per-agent seed maps + the policy-ceiling entry. E7 owns only `pkg/tools/goal_claim.go`, and depends on E1.** A registered catalogue name with no implementation is inert; an implementation with no catalogue name is a boot panic. | E1, E7 |
| C-31 | **`pkg/coreagent/core.go` has three claimants** — seed and skills, the tool catalogue, and the Judge rubric constant. | major | **Two waves: E1 (seed + skills + `allStaticToolNames` + the six seed maps — all static boot data, one commit) then E11 (the `JudgeDefaultRubric` constant only).** No third wave touches the file. **REVISED BY THE ADR-085 FOLD (C-70): E1's half additionally carries `browser_handover` in `allStaticToolNames` and in the six per-agent seed maps; ADR-085's W6 does NOT write this file. It stays a two-wave chain.** E11 additionally carries the merge-order rule: the commit removing "Do not run tools…" from the rubric must not merge until E1's closure tests and E2's budget tests are green on the base branch. | E1→E11 |
| C-32 | **`pkg/agent/task_executor.go` has three claimants** and the judge plan flatly asserts it belongs to one wave, which stops being true the moment ADR-086 lands. Two of the three claims are inside the same function. | major | **Two waves, in order: E12 (the activation and claim-dispatch region around `ClaimForRun`) then E14 (`adjudicateClaim`, carrying both the projection call and the refusal/CAS reasons in one edit).** The judge plan's "W4a only" assertion is void. | E12→E14 |
| C-33 | **The push ladder and the keeper unification are the same work described twice.** Judge W11 delivers "the push-ladder unification"; goal W8 delivers "the drivers and maps" — the same drivers, the same maps, two specs that never saw each other. | major | **Keeper unification lands first (E12) and defines the unified driver and map structure, including the per-goal trigger entries. The push ladder (E13) then unifies onto that structure rather than onto today's.** Building the ladder against session-shaped triggers and then re-pointing it at goal-shaped ones is two implementations of one behaviour. | E12→E13 |
| C-34 | **`pkg/agent/goal_record_wiring.go` is unowned.** The goal spec's impact table marks it modified; it appears in no wave's write-set. It holds the goal-record read/write seam and three of the frame call sites. | major | **Two waves: E4 owns the store seam re-point (`WriteRecord`, `ReadGoalState`); E8 owns the three frame call-site updates, in the same commit as the signature change.** No other wave writes this file. | E4→E8 |
| C-35 | **`pkg/agent/judge.go` has a four-wave chain in the wrong order.** The judge plan puts the rung-1.5 deletion last, "because it removes a dispatch branch W4 has been editing around" — which means the parser is written twice: once maintaining code, once after it is gone. | major | **Invert it and split the deletion out. `judge.go`'s chain is E3 (pure deletion, no new behaviour) → E9 (parser, rung dispatch, `summarizeVerdict`, `buildJudgeUserContent`, the timeout now config-driven) → E10 (the tier-assembly call site).** The deletion has no dependency on anything the parser produces — ADR-084 D14 rule 2 retires the inferred check unconditionally. Deleting first means the parser is written once against final structure. `pkg/agent/judge_artifact_criterion_test.go` is deleted by E3, in the same commit as the code path. | E3→E9→E10 |
| C-36 | **`pkg/agent/loop.go` has two claimants** in a ~14.7k-line file under constant churn: the tool-dispatch cap refusal inside `turnLoop`, and `runAgentLoop`'s reordering. | major | **A chain of two: E2 (the `turnLoop` refusal point) then E13 (the `runAgentLoop` reordering). `pkg/agent/tool_result_admit.go` stays E2-only** — later waves consume its seams and must not edit it, and the injection banner lands once inside `admitToolResult` itself, not at the eleven call sites in `loop.go`. E13 also owns the ADR-081 D3 base predicate in this file when the goal fields move. **REVISED BY THE ADR-085 FOLD (C-71): a third claimant, the browser lane, lands between them. The chain is E2 → B123 → E13, and `turn.go` moves with it.** | E2→B123→E13 |
| C-37 | **A per-goal budget has no writable wire field.** Goal FR-025 requires the override and FR-046 requires a human-editable control on the task detail panel. `Task.judge_rounds` is `readOnly` and is the consumed count, not a ceiling. | major | ~~F1 adds `max_judge_rounds` (integer, `minimum: 1`, optional) to `Task.yaml`, `TaskCreateRequest.yaml` and `TaskUpdateRequest.yaml`, modelled on the existing `max_attempts`.~~ **RETIRED BY D-E (2026-09-11) — NQ-2 CONFIRMED. No `max_judge_rounds` field enters `Task.yaml`, `TaskCreateRequest.yaml`, `TaskUpdateRequest.yaml` or any other schema. There is NO per-goal override; the single global Settings → Performance value governs task goals and chat goals identically. The rest of this row is historical.** It maps to the goal record's override. The `minimum: 1` is the schema half of "an override below 1 is rejected"; the handler rejects 0 and negatives with a message. Naming it `max_judge_rounds` keeps the `max_*` convention and prevents confusion with the readOnly counter one field above it. `Task.judge_rounds` is unchanged. | F1 |
| C-38 | **The Judge degraded-capability badge has no named shape.** Judge FR-078 requires structured facts and two actions; the copies table calls it "the agent-card degraded-state field" and names neither the field nor its shape. `Agent.yaml` already has a `degraded_reason` field scoped to provider binding. | major | **MOOT — D-F (2026-09-11): judge spec §M (JUDGE-FR-077 – FR-080) is retired, so there is no degraded-capability badge and no field to shape. `rubric_capability` is NOT added to `Agent.yaml`, and wave U3 is retired. The original decision is kept below, cancelled.** ~~Do not overload `degraded_reason`.~~ *Original text follows:* F1 adds a new optional object `rubric_capability` to `Agent.yaml`: `{declares_evidence_source: boolean, missing_sentinels: string[], disabled_controls: string[]}`, `additionalProperties: false`, present only when the boot self-check fails, documented as derived and never stored. **The sentinel set is `evidence_source` and the `evidence` array only** — FR-078's text names `outcome` as the second sentinel and that half dies with revision 9. The SPA renders whatever list the server sends and hard-codes no sentinel string. | F1 (shape), U3 (render) |
| C-39 | **The pill state enum gains values from four waves and one has no name anywhere.** Judge FR-083's value is called only "the CAS state". Goal FR-028 separately requires four distinguishable terminal endings where the pill has three. | major | **F1 adds five values to `GoalStatusFrame.state`, in both copies: `judge_refused_god_mode`, `judge_cas_loss` (this document names it; the machine-readable reason string stays `cas_loss`), `blocked`, `claim_overturned`, and `expired`.** `queued` is retained — the schema's own description records it as kept for wire compatibility, and removing it is a breaking narrowing neither spec asked for. Naming FR-083's value here is deliberate: a wave that discovers it needs one after F1 has landed cannot add it without a second regeneration. | F1 |
| C-40 | **The goal spec pins a regression file that does not exist.** `pkg/agent/task_executor_test.go` is named as protecting attempt accounting, the sole-writer invariant, the evidence-gate-free re-dispatch and the hard ceiling. It is not in the tree. | major | **Treat the row as a named set, not a file.** The obligation binds to the seven files that actually hold these assertions: `attempts_vs_rounds_test.go`, `task_executor_adjudicate_claim_test.go`, `task_executor_goal_loop_test.go`, `task_completion_contract_test.go`, `evidence_gate_test.go`, `task_executor_drain_test.go`, `task_executor_stop_toctou_test.go`. New tests land in their behavioural homes. **An agent asked to keep the named file passing would report green having run nothing** — `go test -run` prints `ok` when the pattern matches nothing. | T3 |
| C-41 | **The judge spec targets a component that does not exist.** Judge FR-057a and FR-078 assign vitest rows to `src/components/workspaces/GoalStatusPill.test.tsx`. Neither the component nor the test exists; the real pill is `src/components/chat/GoalPillTray.tsx`. | major | **Re-home both rows into `src/components/chat/GoalPillTray.test.tsx`, owned by U2 together with the component.** Do not create a new pill component — that would produce a second, unrendered pill beside a shipped one. Note that the path choice also changes which vitest matrix shard runs the test: `src/components/chat/` and `src/components/workspaces/` are separate shards. | U2 |
| C-42 | **`CriteriaVerdictList` is claimed three times** — judge W3 owns the component "(+ test)", goal W13 owns the component, and goal test 55 adds a second test file for the same rendering concern while the goal regression table pins the existing one. | major | **One component and one test file, both owned by U5: `src/components/workspaces/CriteriaVerdictList.tsx` and `CriteriaVerdictList.test.tsx`. `CriteriaVerdictList.status.test.tsx` is not created.** Confirmed against the code: the component **already** accepts a `dod` prop and already renders a labelled Definition of Done group; `TaskDetailPanel` simply never passes it, so goal FR-054 is a one-prop change in the panel, not a renderer change. Expected production diff inside the component is close to zero once the fourth status value is withdrawn. | U5 |
| C-43 | **The new concurrent package is outside the race gate.** `scripts/race-packages.sh` is the single source of truth for the `-race` package list consumed by both CI and the worker, and it does not list `./pkg/goal/...`. Neither wave plan names the file. | major | **G4 adds exactly one line, `./pkg/goal/...`, and it lands after S1 has created the package** — a pattern matching no directory makes the race gate error rather than skip. G4 owns that file alone. `pkg/goal` is the one new package in this delivery that is explicitly concurrent, so it is the last one that should be outside the gate. | G4 |
| C-44 | **Three committed guard self-tests are executed by nothing.** `check-browser-tests-gated.test.sh`, `check-no-handwritten-wire-types.test.sh` and `check-no-tool-error-from-status.test.sh` appear zero times in the Makefile, the PR workflow and the worker script. Both specs then propose four more guards "+ .test.sh" with no mechanism that runs the companion. | major | **`scripts/guards.sh` runs each guard's proof-of-failure companion before the guard itself**, resolving `<name>.test.sh`, then `<name>.sh --self-test`, then `<name>-selfcheck.sh`; a guard with none must be listed in a shrinking exemption file or the runner fails. Landing G1 switches the three orphaned self-tests on for the first time — G1 must run them and fix what they surface. **No new guard from this delivery may be added to the exemption file.** A guard that cannot go red manufactures the green it was meant to withhold. | G1 |
| C-45 | **`make lint` runs no guard.** It depends only on the golangci-lint version check and one provider guard; the other eight guard targets are reachable only by typing each name, and one check has no target at all. A local green means the guards were not run. | major | **G1 adds one aggregate target `lint-guards` and makes `lint` depend on it.** The eight existing per-guard targets stay as thin delegates so existing references and muscle memory still work. After G1, `make lint` locally is the same guard set CI runs — which is what the new guards need in order to be load-bearing rather than decorative. | G1 |
| C-46 | **`src/lib/api.ts` and `src/store/chat.ts` are in no wave's write-set** in either plan, yet six requirements need edits to them. | major | **U1 is the sole owner of `src/lib/api.ts`** (client wrappers and generated-type re-exports only — the SPA wire-format lint flags any hand-written `export interface` or object-literal type alias there) **and of `AcceptanceCriteriaEditor.tsx`. U2 is the sole owner of `src/store/chat.ts`.** Every second-tier SPA wave consumes both and edits neither. **REVISED BY THE ADR-085 FOLD (C-73): `src/store/chat.ts` becomes a two-region sequenced file — U2 owns the goal region and merges first inside F1's PR; B8 owns a separate, later region and rebases onto it. No third wave writes it.** | U1, U2, then B8 |
| C-47 | **The performance settings contract is unowned.** Goal FR-045 puts the goal budget default under Settings → Performance, but that screen reads and writes only through generated types from `PerformanceSettings.yaml` and `PerformanceSettingsUpdate.yaml`, which neither wave plan names. | major | **F1 adds the field to both schemas in the same atomic commit; E6 widens `pkg/gateway/rest_performance.go` to carry it; U6 adds the control.** Without the contract field the SPA edge drops the whole `/performance` payload at the zod boundary and the entire Performance screen blanks — on a screen with eleven other settings. | F1, E6, U6 |
| C-48 | **The definition-of-done editor is needed by two waves and created by one.** Goal W11 creates it for the create form; goal W12 must also offer editing on the panel; the two are declared file-disjoint and concurrent. | major | **`DefinitionOfDoneEditor.tsx` (and its test) move into the shared wave U1. U4 and U5 both import it and neither edits it.** Goal FR-054's "do not build a new renderer" governs the *verdict display* only: `CriteriaVerdictList` stays read-only and keeps rendering the DoD group; the panel places the editor above it, mirroring how the criteria editor already sits above the verdict list today. | U1 |
| C-49 | **"Pushed then judged" cannot happen under claim-triggered adjudication.** Goal FR-019 requires a silent task to be pushed and "then judged normally"; judge FR-095 requires the quiet window to make zero Judge calls and makes an empty claim a hard refusal. | major | **ADR-084 wins. The test becomes three phases in one function: (1) the task-owned goal goes quiet and receives the push, with zero Judge calls and zero rounds consumed; (2) the pushed worker emits a completion claim; (3) that claim triggers exactly one adjudication.** The positive assertion (the re-post was actually dispatched and correctly stamped) sits in the same function as the zero-Judge-calls negative, so an implementation that does nothing cannot pass. Assert counts, never elapsed wall-clock. | T2 |
| C-50 | **The scoped test command both specs give is itself a false-green.** Neither includes `-v` nor says to count `--- PASS` lines, and the judge spec's form omits `-count=1`. `go test -run` prints `ok` when the pattern matches nothing. | major | **One canonical scoped form for the whole delivery, given verbatim in §6 rule 3.** A test is verified only when the exit code is 0 **and** the `--- PASS` count equals the number of cases the test defines **and** the `--- FAIL` count is 0. `exit=0` with zero `--- PASS` lines is a failure to run and must be reported as one. | every wave |
| C-51 | **Judge FR-020a's counter would be stranded** by deleting the wave that owned it. | minor | **Retired with D2a; no wave inherits it.** The existing unable-to-verify tracker and non-verdict classification in `pkg/agent/judge.go` and `pkg/agent/verifier_adjudication.go` are untouched by the projection and stay internal. This also fixes the projection's boundary: its only input is the per-criterion `Met` bool, which keeps it out of `verifier_adjudication.go` entirely. | — (retired) |
| C-52 | **Three kinds of ephemeral criterion will appear in a verdict and match nothing persisted**, and neither spec says what happens to them, to a verdict entry whose id matches nothing, or to a criterion the Judge omitted. | minor | **Four rules, all inside `verdict_projection.go`:** (1) an entry resolving to a synthetic origin is an explicit warn-logged, counted no-op, never a store write and never a silent skip; (2) an entry resolving to nothing at all is likewise warn-logged and counted; (3) a criterion present in the input but absent from the verdict **keeps its existing status** — never reset to pending, never defaulted to met; (4) each adjudication is a full overwrite of the statuses it does resolve, last verdict wins. Every no-op increments a counter the tests assert on, so "the projection did nothing" is always distinguishable from "the projection was not called". | E14 |
| C-53 | **The synthesised fallback criterion stamps a session id as its author.** A task-owned goal in its definition phase has no session, so the author would be empty. | minor | **The author becomes the goal's owner reference (owner kind plus owner id), not the session id.** The fallback itself stays ephemeral and unpersisted; any projection targeting it is an explicit logged no-op, because there is no record row to write back to. | S1, E14 |
| C-54 | **The parked-card suppression must not be re-keyed with the other six maps.** Goal FR-005 relocates the pending-ask field and FR-052 re-keys six trigger maps to the goal id; read together they invite re-keying the parked-card lookup too, which is session state. | minor | **Explicit carve-out: `goalHasParkedCard` stays session-keyed and is not one of the six maps.** `PendingAskJSON` moves to a new session-owned file written by `pkg/session` on the same striped-shard path `goal.json` uses today, with its own read/compose/write triple. **The new file must be read on the same code path before any wave deletes the goal fields, or every parked question card disappears on upgrade.** | S2 |
| C-55 | **`contracts/openapi.yaml` is listed as a hand-sync point and is not one.** Every changed shape reaches it by `$ref`. | minor | **It is in F1's write-set for one reason only: registering a new schema *file* in `components.schemas` so Go and TypeScript types generate for it.** If F1 adds no new schema file, `openapi.yaml` needs no edit at all. Recorded so nobody hunts for an inline copy that does not exist, and so nobody adds a schema file and wonders why no Go type appeared. **REVISED BY THE ADR-085 FOLD (C-69): this instruction is correct for a REST schema and WRONG for a WS frame. Registering a frame schema file in `openapi.yaml` emits a duplicate TypeScript declaration and breaks the build. `openapi.yaml` gets no edit at all in this delivery.** | F1 |
| C-56 | **`provenance` will exist on two adjacent schemas with two unrelated enums** — the criterion's authority layer, and the Judge's evidence source. | minor | **Keep both names — the code generator scopes enum constants per schema, so they compile.** F1 must write both descriptions to say explicitly which concept each is and that they are unrelated. **The copies comparator must key on (schema, path), never on field name alone**, or it fires a false positive on day one — and a guard that cries wolf gets disabled, which is how copies drift in the first place. | F1, T1 |
| C-57 | **The plan schemas look in scope and are not.** Goal FR-040 names the plan judge round as a write path, which reads as though a plan's definition of done is in scope; ADR-086 D5 defers plan convergence entirely. | minor | **`Plan.yaml`, `PlanCreateRequest.yaml` and `PlanUpdateRequest.yaml` are explicitly out of F1's write-set and belong to no wave.** They inherit every criterion change by `$ref`. FR-040's third path is an engine change only. | — (out of scope) |
| C-58 | **A named symbol is missing.** Goal FR-030 says "`toWireCriteria` and its `rest_plans.go` mirror" without naming the mirror; a grep for the name in that file finds nothing. | minor | **The mirror is `pkg/gateway/rest_plans.go::toWirePlanDoD`, plus the create-path conversion below it.** Its own doc comment records that the conversion is deliberately duplicated because the generator emits distinct types, so re-pointing one does not re-point the other — and there are three near-duplicate functions in `rest_tasks.go` alone (`toWireCriteria`, `criteriaFromCreateWire`, `criteriaFromUpdateWire`). E5 must re-point all of them. | E5 |
| C-59 | **`pkg/tools/set_goal.go` is asserted about but written by nobody**, while ADR-086 relocates the record it authors. | minor | **E12 owns it and is accountable for finding out whether it needs a behaviour change at all.** It reaches the goal record only through the wiring seam, so if E4 re-points that seam cleanly the tool may be untouched. FR-110's negative assertions are test-only and ride with the guards, not with an engine write-set — a wave that "owns" a file only to assert it did not change is not an owner. | E12 |
| C-60 | **A discovery-based guard runner would pick up an expensive reporting tool.** `scripts/check-dead-code.sh` is referenced by nothing and builds a whole-program call graph. | minor | **Quarantine it by name in `scripts/guards.quarantine`, with a one-line reason, and print the quarantine list on every run** so the exclusion stays visible rather than buried. Quarantine means "discovered, deliberately not run" and is separate from the no-self-test exemption. | G1 |
| C-61 | **A hand-written test file is placed inside a generated directory.** Goal test 42 puts a vitest file in `src/lib/api/generated/`, which Constraint #8 says is never hand-edited, and gives it a Go-style name. | minor | **Move it to `src/lib/api/criterionStatusEnum.test.ts`, outside the generated tree, with vitest prose case names.** Verified: the Go side has precedent for hand-written files in its generated directory and the TypeScript side has none. The Go half of the same property extends the existing `pkg/api/generated/contract_test.go`. | T1 |
| C-62 | **The Todos relabel leaks onto a screen no spec mentions.** The shared checklist component is rendered three times by the calendar's event slide-over. | minor | **Accept one label everywhere.** U5 relabels the shared component's header and placeholder and fixes its test; the calendar picks it up for free and **must not** be forked with a per-surface label prop. **REVISED BY THE ADR-085 FOLD (C-79): "for free" holds for visible text and NOT for aria-labels, which the calendar's own test queries. The three aria-labels stay byte-identical.** U4 separately relabels the create form's own inline label and placeholder — the two halves are genuinely in different files. Leave the exported symbol and filename alone; the rename is explicitly not bundled. | U4, U5 |
| C-63 | **A third empty-state hint exists that no requirement means to remove.** Goal FR-053 says the hint "exists twice"; a third, differently worded one is on the plan create form. | minor | **Grep the two sentences, not the identifier.** The two to reduce to zero hits are, verbatim: `No criteria added — this task will be judged against its title and description (D5).` (create form, replaced by a plain instruction) and `No criteria — this task will be judged against its title and description (D5).` (detail panel, no replacement). They differ only by the word "added". **`CreatePlanSlideOver.tsx`'s hint is out of scope and must be left alone, and the `emptyHint` prop itself stays** — the plan form still passes it. | U4, U5 |
| C-64 | **The degraded-badge copy quotes a retired field.** Judge FR-078's mandated wording names `outcome` as one of two sentinels. | minor | **The surviving sentinel is `evidence_source` plus the `evidence` array. The SPA hard-codes no sentinel string** — it renders the server-supplied list, following the shipped `degraded_reason` precedent. Dropping `outcome` from the copy is a Go-side change in E11, not an SPA change. Note that the self-check's own Go symbols are named after the retired field and E11 must rename them. | E11, U3 |
| C-65 | **`pkg/session` is uncontested — and that is worth stating.** Goal W3 owns four files there; the judge plan touches the package in no wave. | minor | **S2 and S3 and S6 are its only owners for the whole delivery. One non-negotiable internal constraint: the pending-ask field exists in TWO places** (the unified goal-file shape and the day-partition shape) **and both must move in one commit.** The ADR-057 lock order — session shard then cache mutex, never the reverse, and the cache mutex never held across a filesystem call — is unchanged. | S2, S3, S6 |
| C-66 | **The verifier read-confinement slice was never sequenced.** Judge W1's security half (`pkg/tools/resolvepath.go`, `pkg/fspolicy/policy.go`, and the filesystem audit emit sites) is real, exists, and appears in no analyst's merged wave graph. | major | **It becomes its own dependency-free wave, E0, owned by `security-lead`, because it changes the shared filesystem gate for every tool, not just the Judge.** It is write-disjoint from everything else in round 1. If it were to land late, the Judge's read confinement would be incomplete in the interim — so it runs in round 1. | E0 |
| C-67 | **A third wave regenerates all four generated trees.** ADR-085's W4 declares itself dependency-free and owns `contracts/components/schemas/*.yaml`, `contracts/asyncapi.yaml`, `pkg/api/generated/**` and `src/lib/api/generated/**` — the same four paths as F1, which C-04 already declared "one agent, one commit, not splittable". W4's write-set also omits `pkg/gateway/inboundschemas/**`, which `make verify-contracts` diffs. Found by all four fold analysts. | blocker | **ADR-085 W4 is DISSOLVED. There is no browser contracts wave and no second `scripts/gen-contracts.sh` run in this delivery.** F1's write-set gains exactly one new schema file — `contracts/components/schemas/BrowserHandoverNoticeFrame.yaml` — plus its inline asyncapi copy and four asyncapi wiring points, and the regenerated diff rides F1's existing single commit. F1 delivers BROWSER-FR-042's schema half. Everything ADR-085 calls "W4 landed" now means "F1⊕U2 merged". | F1 |
| C-68 | **The plan contradicted itself on F1 and U2, and ADR-085 now depends on the answer.** C-17 requires F1 and U2 to merge in one pull request because the tree does not typecheck between them; §4's round table put F1 in round 1 and U2 in round 4. The browser thread wave chains on `src/store/chat.ts` after U2, so its earliest start was undefined. | blocker | **U2 moves to round 1 and merges with F1 as a single pull request from a single branch. `F1⊕U2` is ONE slot against the six-agent concurrency cap; two agents may work on it, there is one merge.** The tripwire is verified real, not taken from the spec: `src/components/chat/GoalPillTray.tsx:102` and `src/components/chat/GoalIndicator.tsx:137` each end their state switch with `const exhaustiveCheck: never = state`, so F1's widened enum reddens `npm run typecheck` until U2 lands — and a round boundary that leaves the base branch red is forbidden by the round rule. Round 4 loses U2 and gains B7. | F1⊕U2 |
| C-69 | **`contracts/openapi.yaml` must NOT register the new frame schema.** C-55 tells F1 that `openapi.yaml` is in its write-set "for registering a new schema *file* so Go and TypeScript types generate for it". Applied literally to a WS frame, that breaks the build. | blocker | **`contracts/openapi.yaml` gets NO edit in this delivery — not for the goal/judge shapes (all already registered by `$ref`) and explicitly not for the browser frame.** Verified: zero of the 56 `*Frame.yaml` files is `$ref`'d from `openapi.yaml`, and the file's own comment at lines 859–868 records why — two generator passes emitting the same exported TypeScript name produce `Cannot redeclare block-scoped variable`. The frame's *generating* copy is the inline schema in `contracts/asyncapi.yaml`; the sibling file under `contracts/components/schemas/` exists only for the Constraint #8 five-step process and the `inboundschemas` sync, exactly as `GoalStatusFrame.yaml` and `JudgeVerdictFrame.yaml` already do. Related: **`contracts/components/schemas/WsFrameType.yaml` is dead** — 40 lines, `$ref`'d from nowhere, already missing a dozen shipped values. F1 adds the new value to the **inline** `WsFrameType` enum in `contracts/asyncapi.yaml` (around line 1209) and does not touch the standalone file. | F1 |
| C-70 | **A split that risks a boot panic, a second time.** ADR-085's W6 writes `pkg/coreagent/core.go` and `pkg/config/defaults.go` to register `browser_handover` in the catalogue, the six per-agent seed maps and the global ceiling. Both files are single-owner here (C-29, C-31), and `pkg/coreagent/core.go::validateOverrideKeys` **panics** on a seed-map key absent from `allStaticToolNames`. ADR-085's W0 separately claims `pkg/config/config.go`, which E2 owns. | blocker | **Apply C-30's `goal_claim` split unchanged. E1 absorbs all three `browser_handover` registration sites** — the catalogue name, the six seed-map entries, the `defaults.go` ceiling entry — in its one static-boot-data commit, which satisfies Constraint #6's "atomically with core.go" requirement for free. **B6 keeps only** the tool body, its test, `pkg/tools/browser/register.go` and a seed-assertion test. Separately, **E2 becomes the sole owner of `pkg/config/config.go` for the whole delivery** and lands `Tools.Browser.ControlIdleReleaseSec` plus its `EffectiveControlIdleReleaseSec()` accessor (default 900 seconds, 0 disables) beside the existing `LeaseWaitSec`/`IdleTTLSec`, in the same commit as the judge keys. **`ControlIdleReleaseSec` gets NO `defaults.go` entry** — verified that `IdleTTLSec` and `LeaseWaitSec` have none either and the "unset means the shipped default" translation happens at the reader. B0 drops `config.go` and becomes a three-file, zero-conflict wave. | E1, E2; B6 reduced |
| C-71 | **`pkg/agent/loop.go` and `pkg/agent/turn.go` now have three claimants across three designs, two of them at the same dispatch point.** E2 adds the verifier cap refusal at the tool-dispatch point inside `runTurn`'s `turnLoop`; the browser lane adds a control-gate deferral short-circuit at that same point, plus a release hook in `processMessage`, plus the `ControlIdleReleaseSec`→`BrowserConfig` translation in `registerSharedTools`, plus a root-chat tool-context stamp; E13 reorders `runAgentLoop`. On `turn.go`, E13 adds a `turnResult` field and the browser lane adds a `turnState` deferral ledger. | blocker | **One order: E2 → B123 → E13, each in a different round, each rebasing onto the one before, and `turn.go` moves in lockstep so B123 and E13 each rebase once, not twice.** Regions are exhaustive and named in §5. The two refusals are **both real and neither subsumes the other**: E2's counts verifier tool calls per adjudication; the browser lane's counts control-gate deferrals per turn and issues a stop instruction on the third. They share only a code location. **Do not fold one counter into the other** — that would silently change both bounds. The browser branch keys on the deferral marker, never on `ForLLM`. Every commit message on either file cites `file::symbol`, never `file:line`. | E2→B123→E13 |
| C-72 | **A closed-set source-scanning tripwire nobody in this plan knew about.** `pkg/agent/routing_session_id_consumer_set_adr057_test.go` walks an exhaustive eight-file list that includes `turn.go` and `loop.go`, classifies every `routingSessionID` read into one of four buckets with an **exact** per-bucket count, asserts a grand total, fails hard on any unclassified read, **and** parses `pkg/gateway/websocket.go` at test time for a literal anchor string. Its classifier maps *any* read in `loop.go` unconditionally to the WS-stamping bucket. ADR-085 knows about it; this plan mentioned it zero times. | blocker | **Three binding rules.** (1) **E2 and E13 must add ZERO new `routingSessionID` reads to `pkg/agent/loop.go` or `pkg/agent/turn.go`** — a read added there is silently swallowed by an existing bucket and reddens a browser test with a message about ADR-057 WS payload stamping. If E13 genuinely needs one, **E13** performs the full four-part amendment in its own commit and hands over the new baseline in writing; it does not leave it to be discovered. (2) **B123 is the sole owner of that test file** and performs all four BROWSER-FR-022 amendments in one commit: add `browser_deferral.go` to the scan list and update its doc comment; add a fifth bucket keyed on `(browser_deferral.go, <reader fn>)`; raise the grand total by exactly the number of new reads; add the new bucket's own exact-count assertion. (3) **No wave may touch the anchor string `ADR-057 FR-089 — W5 audit classification artefact` in `pkg/gateway/websocket.go` (line 3547), the comment block beneath it, or its class-(a) frame-type list.** B123 owns that file and preserves it verbatim, except for the one prose correction in C-83. The new reader lives in `pkg/agent/browser_deferral.go` and nowhere else. | B123 owns; E2 and E13 constrained |
| C-73 | **`src/store/chat.ts` has two claimants across two designs, in one 6,159-line reducer with 30-plus switch arms.** U2 needs the five new goal-status pill states; the browser lane needs a new arm that synthesises the handover notice as a `role:'system'` message. | blocker | **Sequenced file, two named regions.** **U2 first** (inside F1's PR), owning the goal region only: the `GoalStatusFrame` state narrowing, `goalPills`, `mergeGoalPillFrame`, `GOAL_TERMINAL_STATES`, `buildGoalAckInsertion`, `case 'goal_status'`. U2 adds **no** reducer arm for any browser frame. **B8 second**, rebasing onto the merged commit, owning only: one line added to `SESSION_SCOPED_FRAME_TYPES` (the hand-written Set at lines 1582–1599); a new `browserHandoverNoticeId?: string` field on the `ChatMessage` interface; a new `buildBrowserHandoverInsertion` helper placed **after** `buildGoalAckInsertion`; and a new `case` arm placed **after** `case 'goal_status'` closes. B8 must not read, edit or reference any goal symbol in the file. Neither wave may rename or remove `cancelStream` or `sessionsById[…].isStreaming` — B5 reads both. There is no way to make one reducer file-disjoint across two features; sequencing is the pattern this plan already uses for four Go files. | U2 → B8 |
| C-74 | **The ADR-085 lock/gate/turn waves form a three-way compile cycle.** W1 reads accessors defined on W2's types; W2 registers a hook declared in W3's package; W3 calls a roster exported by W1. The ADR-085 plan calls them file-disjoint and independently committable but **not** independently compilable — under this plan's round rule ("a round ends when every wave in it is merged and green on the base branch") they therefore cannot be scheduled at all. | blocker | **One slot, `B123`; three lanes with disjoint per-file write-sets; ~~three agents~~ **ONE AGENT working the lanes in sequence (D-I)**; one integration branch; one pull request.** The branch is not merged until `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` exits 0 on it. **AMENDED 2026-09-11 by D-I:** it counts as **one** of the six concurrent **AGENTS** in its round — the cap is agents, not slots, and as three agents this slot made round 5 eight agents wide. Promoting the lanes to separate waves in separate rounds is **not** an option, for the reason this very row states: the three-way compile cycle means no lane merges alone. **Do not break the cycle with stub symbols** — see D-i. This is the same answer C-17 already gives for F1⊕U2. | B123 |
| C-75 | **`pkg/gateway/replay.go` has three claimants, two of them inside one 626-line function.** F2 owns the verdict shape on replay (JUDGE-FR-074); ADR-085's W2 owns the waiting-line replay (BROWSER-FR-043a); a judge wave was already reconciled against F2. | major | **Order: F2 → B123, strictly.** F2 owns `toJudgeVerdictFrame` and the `judge_verdict` transcript branch. B123 owns a **new** `EntryTypeSystem` discrimination arm inside `streamReplay` that emits F1's browser frame, and it **must discriminate on a stamped entry field, never on the entry's prose content** — BROWSER-FR-043a's own test rewords the content and asserts the frame type is unchanged. `pkg/gateway/replay_test.go` is B123's for its own case. Neither wave edits the other's region. B123 inherits F2's dependency on F1 transitively. | F2→B123 |
| C-76 | **A required, path-filter-free CI job would go red in a file nobody was watching.** ADR-085's W8 adds `tests/e2e/browser-control-handover.spec.ts`; `tests/e2e/shards.json` is in no wave's write-set in any of the four documents. `.github/workflows/pr.yml`'s `e2e-shard-check` job runs `scripts/e2e-shards.sh check` on **every** pull request with no path filter, is listed in `ci-required.needs`, and fails if any spec is unassigned — because an unassigned spec silently never runs. | blocker | **`tests/e2e/shards.json` joins B8's write-set and B8 is its sole owner for this delivery. The spec and its shard entry land in the SAME commit.** Assign it to the **existing** `ui-heavy` shard (`key_slot: c`, `port: 6065`, `solo: true`) — the shard that already holds `browser-live-video.spec.ts` and `uat-browser-panel.spec.ts`. **Do not create a new shard group**: a new group needs a new port and a new key slot, and `ui-heavy`'s `solo: true` already gives a live-browser spec a contention-free machine. `shards.json` is **not** a G1 file; G1's three wiring files consume it through `scripts/e2e-shards.sh` and need no edit. The new spec must also issue no `POST /api/v1/auth/login` — `scripts/check-e2e-login-crosstalk.sh` fails the build on that, and the failure surfaces in an unrelated later spec. | B8 |
| C-77 | **ADR-085 ships zero guard scripts while stating four merge-fragile prohibitions and one source-shape invariant.** Its Explicit Non-Behaviors list forbids reinstating the control toggle, a resume dispatcher, a frame-persistence path and a turn-park signal; its machine-verifiable constraints pin `bus.InboundMessage.OperatorPrompt` to exactly three non-test assignment sites. A count of source assignment sites is not a runtime property, and this repository's own rule (and `scripts/check-no-jpeg-screencast.sh`'s header) is that "a note in CLAUDE.md tells a human — it does not stop `git merge`". | blocker | **New wave B9, four new files and nothing else:** `scripts/check-operator-prompt-sites.sh` + `.test.sh`, and `scripts/check-no-browser-turn-park.sh` + `.test.sh`. Guard 1 fails if `OperatorPrompt` is assigned `true` at any number of **non-test** sites other than exactly three, scanning `pkg/` excluding `*_test.go`. Guard 2 fails if a control-toggle affordance ("Take control" / "Release control" / "Hand to agent" button strings) reappears under `src/components/browser/`, if a resume-dispatcher symbol reappears in `pkg/agent/`, or if any browser take path calls a turn-park or cancel entry point. **B9 edits no wiring file** — after G1, `scripts/guards.sh` discovers a guard by filename. Neither guard may be placed on the no-self-test exemption list. | B9 |
| C-78 | **A standing browser-test gate that ADR-085 never mentions.** `scripts/check-browser-tests-gated.sh` fails the build if any `pkg/tools/browser/*_test.go` function that calls `newCoordinatorTestConfig`, `resolveTestBinary` or `resolveTestBinaryHeadlessShell` does not also call `skipIfNoBrowser(t)` in the same function body. It runs as its own required job (`browser-tests-gated`) and again on the CI worker. ADR-085 adds at least five new test files into its exact scope; `grep -c skipIfNoBrowser` over the ADR-085 spec returns **0**. | major | **Binding on B123 and B6: every new or edited test function under `pkg/tools/browser/` that reaches any of those three helpers must call `skipIfNoBrowser(t)` in the same function body.** Prefer a fake or registry-level fixture — none of the new files needs a real Chrome to assert a gate classification, a lock lifecycle or a refusal string. Verify locally before pushing: `bash scripts/check-browser-tests-gated.sh; echo "exit=$?"` (grep-based, no Go build, sub-second). | B123, B6 |
| C-79 | **The Todos relabel would redden a test in a file no wave owns.** GOAL-FR-057 relabels the shared checklist component. `src/components/calendar/CalendarEventSlideOver.test.tsx` renders it three times and asserts on its **aria-labels** at lines 668, 685, 687 and 689. C-62's "the calendar inherits the relabel for free" is true for visible text and false for aria-labels. | major | **GOAL-FR-057's relabel is scoped to VISIBLE TEXT ONLY** — `TaskChecklistField.tsx`'s section header (line 103) and its placeholder (line 160), plus `CreateTaskSlideOver.tsx`'s own inline `<Label>` and placeholder. **The three aria-label strings in `TaskChecklistField.tsx` (lines 141, 151, 171) and the two in `CreateTaskSlideOver.tsx` (lines 579, 597) stay BYTE-IDENTICAL.** Verified that the calendar test contains no visible-text assertion, so the visible half genuinely is free. The FR's own wording already scopes to visible text, so the restriction costs nothing — but an agent told to "relabel Checklist to Todos" will tidy the aria-labels as a matter of course, and Constraint #7 forbids closing that as pre-existing. | U4, U5 |
| C-80 | **A grep-based acceptance criterion invites deleting a shipped prop.** GOAL-FR-053 requires zero hits for a soft-tier sentence carried by `emptyHint`. Two call sites carry it; a **third**, in `CreatePlanSlideOver.tsx:488`, carries different wording about the plan judge and belongs to no wave. `AcceptanceCriteriaEditor.test.tsx:550-563` has three tests exercising the prop, including one asserting it renders nothing when absent. | major | **The `emptyHint` prop survives unchanged. GOAL-FR-053 removes exactly two ATTRIBUTES at two call sites** — `CreateTaskSlideOver.tsx:405` (U4, replaced by the plain instruction "Add at least one.") and `TaskDetailPanel.tsx:642` (U5, no replacement). `CreatePlanSlideOver.tsx:488` is left exactly as it is. Deleting the prop would break a deliberately out-of-scope surface and turn three of U1's own tests red in the round U1 is being reviewed. | U1, U4, U5 |
| C-81 | **Read literally, GOAL-FR-054 gives the task panel a definition-of-done VERDICT display and no way to EDIT one** — while GOAL-FR-053 requires the panel to mark it required, GOAL-FR-048 requires "saving an edit MUST enforce FR-047", and the reference design draws an editor block with add/remove affordances. A task created before the rule with an empty DoD could never be given one. | major | **U5 ships BOTH on the panel, in one `<Field label="Definition of Done">` mirroring the existing acceptance-criteria field:** the DoD editor, autosaving via `doUpdate({ dod })` against F1's new `TaskUpdateRequest.dod`; **and** the `dod` prop passed to the existing `<CriteriaVerdictList>`. FR-054's "do not build a new renderer" binds the **verdict half only** — U5 writes zero new rendering code inside `CriteriaVerdictList.tsx`, which is what that sentence protects, and which already accepts a `dod` prop and already renders the labelled group. Rendering a list twice — editor above, verdicts below — is the shipped pattern in this very panel, not an invention. No Save button; both halves autosave. | U5 |
| C-82 | **`DefinitionOfDoneEditor.tsx` risks being a second copy of the criteria vocabulary.** C-48 gives U1 a new component; GOAL-FR-054 says the create form's DoD "is a second `AcceptanceCriteriaEditor` with identical vocabulary". `AcceptanceCriteriaEditor.tsx` is a 200-line editor with its own technical-check and action-count affordances. | minor | **`DefinitionOfDoneEditor.tsx` is a THIN WRAPPER** that renders `<AcceptanceCriteriaEditor>` and supplies only DoD chrome: the label, the required asterisk (the same span the Title field uses) and the helper line "Standing gates, judged on every attempt. Add at least one." It contains **no** editing logic, **no** criterion state and **no** second copy of the affordances. U4 and U5 both consume it. **If you find yourself copying a handler out of `AcceptanceCriteriaEditor`, stop and report.** | U1 |
| C-83 | **A transcript field would cross the wire undeclared, and two independent false-greens would hide it.** BROWSER-FR-043a needs a dedicated system-entry subtype stamped on the transcript entry so replay can discriminate without prefix-matching prose. `session.TranscriptEntry` is serialized **directly** onto `GET /sessions/{id}/messages` — no field-by-field copy — against `Message.yaml`, which is `additionalProperties: false`. The entry is also JSONL on disk, so the field cannot be `json:"-"`. | major | **F1 adds the subtype to `contracts/components/schemas/Message.yaml` in the same atomic commit**: optional, additive, recommended name `system_subtype`, with an enum of exactly one value so a future subtype is a deliberate contract edit rather than a free-text field. **Do not add it to the `type` enum** — the entry stays `EntryTypeSystem`; the subtype is a second axis. Why it must be declared even though nothing would fail today: the generated `Message` zod is a bare `z.object({…})` with no `.strict()`, so an undeclared key is silently stripped at the SPA edge, and the Go contract test validates a hand-rebuilt minimal shape rather than the real struct. Two independent false-greens over one undeclared field is exactly the failure class `docs/internal/false-green-patterns.md` exists for. Separately: **B123 corrects the ADR-057 artefact block's member count in `pkg/gateway/websocket.go` if the new frame joins the SPA's session-scoped set** — prose accuracy only, the test asserts `>= 1` and will not redden. | F1; B123 for the prose |
| C-84 | **A wall-clock nanosecond on the wire would break the notice's identity.** BROWSER-FR-044 derives the notice id from `(sessionID, holdStartedAtUnixNano)`. A 2026 unix-nanosecond value is roughly 1.78e18; `Number.MAX_SAFE_INTEGER` is 9.007e15 — three orders of magnitude past exact integer representation in a JSON number. Every SPA-computed id would differ from the server's, defeating the idempotency the FR rests on. | major | **The frame carries a server-computed `message_id` STRING and no raw nanosecond field.** The SPA reducer keys idempotency on `message_id` exactly as `buildGoalAckInsertion` keys on `goalAckMessageId(goal_id)` — returning null when `messagesById[id]` already exists. `pkg/gateway/replay.go` recomputes the same string from the stamped transcript entry, so live and replay collapse to one message. Shipping the nanoseconds as a string for the SPA to concatenate would make two code paths agree on a format for no gain. | F1 |
| C-85 | **The new frame introduces a hand-sync pair the copies comparator does not know about.** `contracts/components/schemas/BrowserHandoverNoticeFrame.yaml` (documentation plus the `inboundschemas` copy, read by no generator) versus `contracts/asyncapi.yaml`'s inline `BrowserHandoverNoticeFrame` (the copy both generators read). `make verify-contracts` is green whether or not two hand-written copies agree — it checks generated-versus-spec, never copy-versus-copy. | minor | **F1 hands T1 one more comparator row** — shape `browser_handover_notice frame`, canonical `contracts/asyncapi.yaml → components.schemas.BrowserHandoverNoticeFrame`, hand-sync `contracts/components/schemas/BrowserHandoverNoticeFrame.yaml` — and T1's recursive equality assertion covers it. **The `.yaml` file's header MUST carry the same "canonical copy lives inline in `contracts/asyncapi.yaml`, keep both in sync by hand" note that `JudgeVerdictFrame.yaml` already carries**, so the obligation is discoverable from the file itself. | F1 declares; T1 owns |
| C-86 | **Three ADR-085 surfaces look like contract work and are not.** BROWSER-FR-031b's unsolicited `browser_status{state:"released"}`; FR-046/FR-048/FR-051's tool and policy seeding; FR-031a's config key. | minor | **F1 makes NO contract change for any of them.** Verified: `BrowserStatusFrame.yaml` already carries `released`, `session_id`, `control_only` and `controlled_by_other`, and the ADR-085 spec agrees ("this change adds no new frame type here"); no tool name or tool-argument schema is enumerated anywhere in the contract, and `GlobalToolPolicies.yaml` is a free-form `additionalProperties` map; no browser config key crosses the wire today. **ADR-085's only contract deliverables are the new frame (C-67) and the `Message.yaml` subtype (C-83).** Every speculative field added to the atomic commit rewrites part of four generated trees and widens the blast radius of the one commit nothing downstream can start without. | F1 (as an explicit non-goal) |
| C-87 | **A structural census could be broken by a judge wave in a package it does not own.** BROWSER-FR-029a pins a source census: `bus.InboundMessage.OperatorPrompt` is set true at exactly `{websocket.go, sse.go, channels/base.go}` and never at `{ws_ask_user.go, async_notifier.go, loop.go}`, **and** the `PublishInbound` census is exactly six non-test call sites. Verified: exactly six exist today, and `loop.go`'s single one sits inside `runAgentLoop` — precisely E13's region. | major | **Binding on E13, verbatim: E13 must leave `pkg/agent/loop.go`'s `PublishInbound` call at exactly ONE non-test site, and must not assign `bus.InboundMessage.OperatorPrompt` anywhere.** The goal-loop follow-up re-injection is a never-sets site by ADR-085's own rules and stays one. If E13's unified push ladder genuinely needs a second publish site, that is a cross-design change and must be **reported before it is written**, not discovered by a browser gateway test. Symmetrically, B123 must not touch the follow-up publish site. Because B123 lands in round 5 and E13 in round 6 (D-j), the census is written first and E13 is the wave that must not disturb it. | B123 owns the test; E13 constrained |
| C-88 | **A reason-string rewrite would redden nine assertions across three files.** BROWSER-FR-011 rewrites the browser deferral reason. `pkg/tools/browser/lease_membership_test.go` defines `humanControlDeferralMarker = "human is currently controlling"` and it is asserted there, six times in `implicit_acquisition_test.go` and three times in `operator_takeover_test.go` — a **substring** match. | major | **Keep the exact substring `human is currently controlling` inside the rewritten reason.** That is the default and requires no test edits. If and only if the implementer chooses wording that drops the phrase, all three files are amended in the SAME commit and the constant is redefined — never left matching prose that no longer exists. All three files are in B123's write-set either way. | B123 |
| C-89 | **Two test rewrites assert the same tool roster with different counts, because they walk two catalogues whose sizes differ by one.** One walks `BrowserBuiltinMetadata()` (17 tools, partition 11 action / 3 capture / 3 exempt); the other walks `registry.GetAll()` (16 tools, partition 10 / 3 / 3). Getting either wrong reddens the other file with a message naming neither catalogue. | major | **B123 owns both files and lands both rewrites in one commit. Every count assertion in either file states its catalogue in the failure message.** The old function names `TestBrowserTools_ControlGateMembershipMatchesExemptions`, `TestWriteLease_EveryActionToolIsLeased` and `TestAudit_WriteClassSetIsTheControlledResultSet` must not survive their replacements. Rename the misleading local variable `registered` in `control_gate_membership_test.go` while editing it. | B123 |
| C-90 | **`src/lib/toolVisibility.ts` and `src/components/chat/ChatScreen.tsx` needed owners, or explicit non-owners.** ADR-085 cites `toolVisibility.ts` once, as an existing risk rather than a change; `ChatScreen.tsx` has **two** render paths for a system message and a wave that edits only the one it happens to open ships a notice visible in one scroll mode and not the other. | minor | **`src/lib/toolVisibility.ts`: no wave edits it. `browser_handover` gets NO hide entry** — it is an operator-facing act and must stay visible in the thread; CLAUDE.md scopes the hidden set to "infra-only calls with no standalone meaning to a reader", which this is the opposite of. **`src/components/chat/ChatScreen.tsx`: B8 owns it exclusively** (verified unclaimed elsewhere) and edits **both** `function SystemMessage()` (line 216, the non-virtualised path) and `function VirtualSystemMessageRow()` (line 1078, the virtualised path), copying the shipped two-site `isGoalAck` pattern. No layout work is needed — a `role:'system'` message already renders as a centred pill in both paths — but the `data-testid="browser-handover-notice"` discriminator **is** required, because the ADR-085 e2e spec names it as its positive observable. | nobody / B8 |
| C-91 | **A file the whole delivery must leave alone, and the reason is easy to miss.** `pkg/agent/subturn.go` is written by no wave in any of the three designs (verified: zero hits in this plan, and it appears in no ADR-085 wave row). It carries `childTS.routingSessionID = parentTS.routingSessionID`, which CLAUDE.md marks load-bearing: delete it and every chat-wide Stop silently stops reaching delegated sub-turns. The browser lane is the one wave that reasons about `routingSessionID` and could plausibly "tidy" it. | minor | **`pkg/agent/subturn.go` has ZERO owners for this delivery.** It appears in B123's must-not-touch list by name, with the reason. The assignment is independently protected by the inheritance bucket's exact-count assertion in the ADR-057 consumer-set test. Also zero-owner: `pkg/agent/tool_denial.go` (the pattern B123's ledger copies — copy it, do not edit it) and `pkg/agent/async_notifier.go` (a never-sets `OperatorPrompt` site that C-87's census reads). | nobody |
| C-92 | **How G1 wires the guard runner decides whether any guard in this delivery gates a merge.** `.github/workflows/pr.yml`'s required-check job `ci-required` lists its dependencies as an explicit, hand-maintained array of twenty-two job ids. A new guard job not added to that array goes red without blocking anything — and the job's own header comment records that this exact class of drift already made the entire workflow non-blocking once. | blocker | **G1 creates NO new `pr.yml` job.** It replaces the nine guard **steps** inside the existing `tool-error-status-lint` job with one step, `run: bash scripts/guards.sh`, so `ci-required.needs` is not edited and branch protection is not touched. **Leave the separate `wire-types-lint` and `browser-tests-gated` jobs standing exactly as they are** — `guards.sh` will run those two scripts a second time, which is cheap, idempotent, and keeps two branch-protection-named contexts alive. In the `Makefile`, G1 replaces the per-guard targets with one `guards:` target and adds `guards` to `lint:`'s prerequisites (today `lint:` depends only on one provider guard, so five committed guards are absent from `make lint` entirely). In `deploy/ci-worker/runci.sh`, G1 replaces the ten guard invocations inside `run_lint()` with one `bash scripts/guards.sh`. | G1 |
| C-93 | **The guard runner's discovery, companion-resolution and exemption rules were specified without their contents.** Three concrete defects: `check-no-removed-providers-selfcheck.sh` is another guard's **companion** and would be discovered as a guard in its own right, then fail for having no companion of its own; `check-no-handwritten-wire-types.sh` has **both** a `.test.sh` file and a `--self-test` flag, and C-44's ordered fallback would silently switch CI from the flag it runs today (35-plus assertions) to a file that has never run; and neither the exemption file nor the quarantine file has stated contents, so an agent would derive them and could exempt a guard it simply could not get to pass. | major | **Three rules, and two files with exact contents.** (1) **Companions are subtracted before guards are counted**: resolve companions first over the full discovered set, remove the resolved companion paths, and treat what remains as the guard list. State this in `scripts/guards.sh`'s header and exercise it in `scripts/guards.test.sh`. **Do not** solve it by adding the self-check file to the exemption list — that hides a whole class of misclassification behind one entry. (2) **When both companions resolve for one guard, run both**, and G1 must diff what each covers before landing: if the `.test.sh` file is weaker than the `--self-test` flag, bring it level or delete it in the same commit. Never leave two proof-of-failure paths of unknown relative strength attached to one guard. (3) `scripts/guards-no-selftest.exempt` ships with **exactly three lines** — `check-no-fail-closed-backfill.sh`, `check-no-goal-confirm-gate.sh`, `check-no-jpeg-screencast.sh` — and `scripts/guards.quarantine` with **exactly one**, `check-dead-code.sh` (reason: whole-program call graph, out-of-memory risk on the development machine, referenced by no CI surface today, so quarantining preserves current behaviour rather than removing a live gate). Every other discovered guard resolves a companion and appears in neither file. **The nine guards this delivery adds each ship with a `.test.sh` and are forbidden from the exemption file.** G1 must run the three previously-orphaned companions (`check-browser-tests-gated.test.sh`, `check-no-handwritten-wire-types.test.sh`, `check-no-tool-error-from-status.test.sh`) and fix whatever they surface, in G1's own commit. | G1 |
| C-94 | **The three specs give three different "canonical" scoped Go test commands, and ADR-085's uses a package wildcard CLAUDE.md forbids on this machine.** The goal spec's form has `-count=1`; the judge spec's does not; ADR-085's has neither `-count=1` nor a concrete package — it ends `./pkg/...`, which compiles and links **every** package including the gateway test binary under the `goolm` tag. That is the specific thing recorded as having out-of-memory-killed sessions here. | major | **One canonical form for every Go test in all three designs (§6 rule 3), and no other form is acceptable evidence. `./pkg/<pkg>/` is always a single concrete package directory; `./pkg/...` is FORBIDDEN in every local invocation — treat ADR-085's Test Matrix preamble as an error and do not copy it.** A green requires all three of: exit 0, a `--- PASS` count equal to the number of cases the test defines, and a `--- FAIL` count of 0. | every wave |

---

## 3. The merged wave graph

**Forty-five waves in forty-four scheduling slots** *(was forty-seven in forty-six; the operator decisions of 2026-09-11 retire waves **S4** and **U3** — see §8)* (F1 and U2 are two waves in one slot, because
they merge as one pull request). Every path is repo-relative to
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2`. **A wave writes only the paths in its
write-set. If you believe you must write anything else, stop and report it — do not widen your own
write-set.**

Requirement identifiers carry their design: **JUDGE-** for ADR-084, **BROWSER-** for ADR-085,
**GOAL-** for ADR-086. Two ADR-085 waves from its own plan are gone: **W4 is dissolved into F1**
(C-67) and **W1+W2+W3 are one slot and ONE agent, `B123`** (C-74, as re-packed by D-I). Two ADR-085 waves are reduced: **W0 loses
`pkg/config/config.go`** to E2 and **W6 loses `pkg/coreagent/core.go` and `pkg/config/defaults.go`**
to E1 (C-70). One wave is new: **B9**, the two guards ADR-085 needs and does not specify (C-77).

Where a file appears in more than one wave, §5 gives the strict order and the region each wave owns.
The disjointness of every pair of waves scheduled in the same round was checked path by path; §4
records the result.

| Wave | Title | Depends on | Write-set (exact repo-relative paths) | Delivers (FR ids) | Must NOT touch |
|---|---|---|---|---|---|
| **F1** | The one atomic contracts change-set for all three designs — one agent, one commit, one `scripts/gen-contracts.sh` run | — | `contracts/components/schemas/AcceptanceCriterion.yaml`, `.../AcceptanceCriterionInput.yaml`, `.../CriterionVerdict.yaml`, `.../JudgeVerdictFrame.yaml`, `.../GoalStatusFrame.yaml`, `.../Goal.yaml`, `.../Agent.yaml`, `.../AgentUpdateRequest.yaml`, `.../Task.yaml`, `.../TaskCreateRequest.yaml`, `.../TaskUpdateRequest.yaml`, `.../PerformanceSettings.yaml`, `.../PerformanceSettingsUpdate.yaml`, `.../Message.yaml` *(the ADR-085 system-entry subtype — C-83)*, `contracts/components/schemas/BrowserHandoverNoticeFrame.yaml` *(new — C-67)*, `contracts/asyncapi.yaml`, `pkg/api/generated/**` *(except `contract_test.go`)*, `src/lib/api/generated/**` *(generated artefacts only — no hand-written test lands in that tree; the goal spec's `asyncapi-criterion-status.test.ts` is T1's `src/lib/api/criterionStatusEnum.test.ts`)*, `pkg/gateway/inboundschemas/**`, `pkg/api/generated/goal_dod_minitems_test.go` | JUDGE-FR-006b, FR-028, FR-057a, FR-065, FR-070, FR-071, FR-072, FR-073, FR-075, ~~FR-078, FR-080~~ **RETIRED (D-F — judge spec §M, the mixed state, is an upgrade scenario)**, FR-083, ~~FR-085's rollout choice~~ **RETIRED (D-F)** *(FR-085's frame shape is unchanged, so nothing moves in this write-set on its account)*, FR-093, FR-102; GOAL-FR-002, FR-004, FR-006, FR-021 (wire half), FR-025 (wire half), FR-028 (wire half), FR-032–FR-035, ~~FR-046 (wire half)~~ **RETIRED (D-E — there is no per-goal budget override, so no request schema gains a writable budget field)**, FR-048 (wire half), GOAL-MV-1; **BROWSER-FR-042 (schema half), BROWSER-FR-043a (`Message.yaml` subtype), BROWSER-FR-044 (`message_id` string)** | `contracts/openapi.yaml` — **no edit at all** (C-69). **OQ-2 is ANSWERED (D-E): `Goal.yaml` stays TYPE-ONLY.** No `GET /api/v1/goals/{id}`, no `PATCH /api/v1/goals/{id}`, no `GoalUpdateRequest.yaml`, and no writable per-goal budget field on `Task.yaml`, `TaskUpdateRequest.yaml` or anywhere else — none of these enter this write-set, now or later in the delivery. **Strike `rubric_capability` from `Agent.yaml` (D-F retires judge spec §M and with it JUDGE-FR-077 – FR-080, so R-30's two-boolean shape is moot).** If `rubric_capability` was `Agent.yaml`'s only change, drop `Agent.yaml` from this write-set — and `AgentUpdateRequest.yaml` if the same is true of it — and **report that you did**; `contracts/components/schemas/WsFrameType.yaml` — dead, drifted, generates nothing; do **not** bring it level (C-69); `BrowserStatusFrame.yaml`, `GlobalToolPolicies.yaml`, `ToolPolicy.yaml` — ADR-085 needs no change to any of them (C-86); `pkg/task/**` (F2); `pkg/api/generated/contract_test.go` (T1); `contracts/components/schemas/Plan.yaml`, `PlanCreateRequest.yaml`, `PlanUpdateRequest.yaml` (nobody — ADR-086 D5 defers plan convergence and all three inherit by `$ref`); `pkg/agent/**`; `pkg/gateway/**`; `src/components/**`; `src/lib/api.ts`; `src/lib/ws.ts` (nobody). Adds **no** `unable_to_verify`, **no** `outcome` field, **no** fourth criterion status and **no** raw unix-nanosecond field anywhere. **Merges in ONE pull request with U2 (C-68).** |
| **E0** | Verifier read confinement and filesystem audit *(security-lead)* | — | `pkg/tools/resolvepath.go`, `pkg/fspolicy/policy.go`, `pkg/tools/filesystem.go`, `pkg/tools/filesystem_docextract_test.go`, `pkg/tools/resolvepath_readconfined_adr084_test.go` *(new)*, `pkg/fspolicy/readconfined_adr084_test.go` *(new)*, `pkg/tools/filesystem_refusal_text_adr084_test.go` *(new — FR-064's distinct not-found vs refusal texts, load-bearing for FR-063a)* | JUDGE-FR-060, FR-060a, FR-084 | `pkg/agent/**`; `pkg/coreagent/core.go` (E1); `pkg/tools/goal_claim.go` (E7); `pkg/tools/set_goal.go` (E12); `pkg/tools/task.go` (E5) |
| **E1** | Static boot data — seed, skills, tool catalogue, policy ceiling, budget defaults (now including `browser_handover`'s three registration sites) | — | `pkg/coreagent/core.go` *(seed, skills, `allStaticToolNames`, the six per-agent seed maps — **not** the rubric constant)*, `pkg/config/defaults.go`, `pkg/agent/verifier_capability_gate.go` *(new)*, `pkg/agent/instance.go`, `pkg/agent/context.go`, `pkg/coreagent/core_test.go` *(E1 owns this file; E11 owns `pkg/coreagent/judge_rubric_adr084_test.go` and writes nothing else in `pkg/coreagent` — the two never touch one file)*, `pkg/agent/verifier_capability_gate_adr084_test.go` *(new)*, `pkg/agent/instance_confinement_adr084_test.go` *(new)*, `pkg/config/defaults_test.go`, `pkg/coreagent/constructor_seed_test.go`, `pkg/coreagent/override_keys_panic_test.go` | JUDGE-FR-059a, ~~FR-077 (seed half)~~ **RETIRED (D-F — judge spec §M)**, JUDGE-D10 catalogue, JUDGE-D12 catalogue; GOAL-FR-024, FR-025 (default values), FR-026 (default values); **BROWSER-FR-048, FR-048a, FR-051 (the catalogue name, the six seed-map entries and the global-ceiling entry — C-70)** | `pkg/coreagent/core.go::JudgeDefaultRubric` (E11); `pkg/tools/goal_claim.go` (E7) and `pkg/tools/browser/tools_handover.go` (B6) — registering a catalogue name without an implementation is inert, the reverse is a **boot panic**; `pkg/tools/resolvepath.go` and `pkg/fspolicy/policy.go` (E0); `pkg/agent/judge.go` (E3); `pkg/config/config.go` (E2) — **`ControlIdleReleaseSec` gets NO `defaults.go` entry**, its 900-second translation lives at the reader in `loop.go` (B123's region). The `len(AllStaticToolNames()) == len(DefaultConfig().Sandbox.ToolPolicies)` assertion must stay green: catalogue name, six seed entries and ceiling entry land in **one** commit. |
| **E2** | Verifier budget, tool-result capture, dispatch cap — and sole owner of `pkg/config/config.go` for the whole delivery | — | `pkg/config/config.go`, `pkg/agent/verifier_budget.go` *(new)*, `pkg/agent/verifier_budget_test.go` *(new — this is the judge matrix's `verifier_budget_adr084_test.go`; ONE file, this name)*, `pkg/agent/verifier_injection_adr084_test.go` *(new — FR-009a)*, `pkg/config/judge_timeout_adr084_test.go` *(new — FR-050)*, `pkg/agent/tool_result_admit.go`, `pkg/agent/loop.go` *(the tool-dispatch refusal point inside `turnLoop` only)* | JUDGE-FR-009a, FR-030, FR-051, FR-052; **BROWSER-FR-031a (the config field and its `EffectiveControlIdleReleaseSec()` accessor only — default 900 seconds, `0` disables — C-70)** | `pkg/config/defaults.go` (E1 — and `ControlIdleReleaseSec` gets no entry there at all); `pkg/agent/judge.go` (E3/E9 — the timeout constant is wired to this wave's config key by E9); `pkg/agent/loop.go::runAgentLoop` (E13); `pkg/agent/loop.go::processMessage` and `::registerSharedTools` and the control-gate deferral branch (B123); `pkg/agent/turn.go` (B123 then E13); `pkg/agent/routing_session_id_consumer_set_adr057_test.go` (B123). **Add ZERO new `routingSessionID` reads to `loop.go` or `turn.go` (C-72).** The injection banner lands **once** inside `admitToolResult`, never at its eleven call sites. Cite `file::symbol` in every `loop.go` commit message. |
| **E3** | Rung-1.5 deletion — remove the inferred artifact check | — | `pkg/agent/judge.go` *(deletion only)*, `pkg/agent/judge_artifact_criterion_test.go` *(delete)*, `pkg/agent/judge_no_inferred_check_adr084_test.go` *(new — R-18's replacement oracle: asserts ZERO shell executions for a prose/artifact criterion)* | JUDGE-FR-109, JUDGE-D14 rule 2 | `pkg/agent/behavior_scan.go` — JUDGE-FR-107 forbids widening it; `pkg/agent/verifier_adjudication.go` (E9); `pkg/agent/judge.go::buildJudgeUserContent` and the parser (E9). **Deletion only: this wave adds no behaviour — but it does NOT land bare.** R-18: the five deletions AND the new `judge_no_inferred_check_adr084_test.go` land in the SAME commit. A wave that removes a code path removes its tests and supplies the oracle proving the path is gone; a guard script may never be the sole delivery of a requirement. |
| **G1** | Guard runner and CI wiring — sole owner of the three wiring files | — | `scripts/guards.sh` *(new)*, `scripts/guards.test.sh` *(new)*, `scripts/guards-no-selftest.exempt` *(new)*, `scripts/guards.quarantine` *(new)*, `Makefile`, `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh` | The mechanism for every guard below (C-19, C-44, C-45, C-60) | Every `scripts/check-*.sh` **body** — those belong to the wave that owns the feature each guards; `scripts/race-packages.sh` (G4); `scripts/check-vitest-coverage.mjs`; the vitest matrix patterns in `pr.yml`; all of `pkg/**` and `src/**`. G1 ships no product code. |
| **F2** | Persisted Go types brought level with the contract | F1 | `pkg/task/verdict.go`, `pkg/task/criterion.go`, `pkg/task/verdict_adr084_test.go` *(new)*, `pkg/gateway/replay.go` | JUDGE-FR-006b (persisted clause count), FR-070a *(four fields, **not** `Outcome`)*, FR-074; GOAL-FR-037 (the three constants stay three) | `contracts/**`, `pkg/api/generated/**`, `src/lib/api/generated/**`, `pkg/gateway/inboundschemas/**` (F1 only); `pkg/task/task.go`, `pkg/task/store.go` (E5); `pkg/task/criterion_test.go` (T1); `pkg/agent/**` — the loop that *populates* these fields is E9. **No fourth `CriterionStatus` constant; `IsValidCriterionStatus`'s switch stays byte-identical.** |
| **S1** | The goal entity store — `pkg/goal` | F1 | `pkg/goal/doc.go`, `pkg/goal/goal.go`, `pkg/goal/store.go`, `pkg/goal/criteria.go`, `pkg/goal/verdict.go`, `pkg/goal/status.go`, `pkg/goal/predicate.go`, and their `_test.go` siblings, plus `pkg/goal/store_test.go`, `pkg/goal/phase.go`, `pkg/goal/phase_test.go`, `pkg/goal/store_crossprocess_test.go` and `pkg/goal/lock_test.go`. *(`pkg/entity/flock_isolation_test.go` is the read-only precedent `lock_test.go` copies — S1 does NOT write `pkg/entity`.)* | GOAL-FR-001, FR-002, FR-003, FR-004, FR-006, FR-007, FR-008, FR-025 (persisted override), FR-027 (store half) | `pkg/goal/routing.go` (S5); `pkg/goal/retention.go` (S3); `pkg/session/**` (S2/S3/S6); `pkg/gateway/**`; `pkg/agent/**` — every goal-state re-point is an engine wave sequenced after S1; `pkg/entity/store.go` — reused as-is, do not fork it; `contracts/**` and both generated trees (F1). **No per-goal budget field on the goal record. No override parameter on the config signature. No second read site. The single global Settings -> Performance value is read directly (D-E).** |
| **S2** | Session storage, additive — `PendingAskJSON` relocates, lock order documented | — | `pkg/session/pending_ask.go` *(new)*, `pkg/session/pending_ask_test.go` *(new)*, `pkg/session/unified_meta_files.go`, `pkg/session/unified.go`, `pkg/session/daypartition.go`, `pkg/session/unified_lock.go`, `pkg/session/goal_meta_greenfield_test.go`, `pkg/session/unified_meta_split_adr057_test.go`, `pkg/session/unified_listsessions_adr057_test.go`, `pkg/session/unified_stats_flush_adr057_test.go` *(the four REAL `pkg/session` test files referencing the goal fields — verified 2026-09-11; `unified_meta_files_test.go` DOES NOT EXIST and must not be created, see R-34)* | GOAL-FR-005, FR-050 | `pkg/session/retention_sweep.go` (S3); `pkg/goal/**`; `pkg/agent/**`; `pkg/gateway/**`. **The goal fields are NOT deleted here — S6 does that after every reader is re-pointed. This wave must leave the tree green.** The pending-ask field exists in **two** shapes and both move in this one commit. **D-F — no rescue:** the `PendingAskJSON` upgrade rescue is RETIRED, and OQ-C is answered with it. Nothing reads a pre-existing install's `goal.json`, nothing is rescued, and no per-session warning is emitted. Greenfield is assumed. The relocation itself stands — it is a code move in a fresh tree, not a migration. |
| **E7** | The `goal_claim` tool implementation | E1 | `pkg/tools/goal_claim.go` *(new)*, `pkg/tools/goal_claim_test.go` *(new)* | JUDGE-FR-091 (the tool), JUDGE-D12 | `pkg/coreagent/core.go` — E1 already registered the name; editing it here risks the `validateOverrideKeys` **boot panic**; `pkg/config/defaults.go` (E1); `pkg/tools/set_goal.go` (E12) — model on it, do not edit it |
| **U1** | SPA shared surface — API client, criteria editor, the DoD editor primitive | F1 | `src/lib/api.ts`, `src/components/workspaces/AcceptanceCriteriaEditor.tsx`, `src/components/workspaces/AcceptanceCriteriaEditor.test.tsx`, `src/components/workspaces/DefinitionOfDoneEditor.tsx` *(new)*, `src/components/workspaces/DefinitionOfDoneEditor.test.tsx` *(new)* | GOAL-FR-030 (SPA half), FR-048 (the editor) | `src/lib/api/generated/**` (F1) — re-export, never redeclare; `src/components/workspaces/CreateTaskSlideOver.tsx` (U4); `TaskDetailPanel.tsx`, `CriteriaVerdictList.tsx` (U5); `CreatePlanSlideOver.tsx` (**nobody** — its line-488 `emptyHint` stays verbatim, C-80); `src/store/chat.ts` (U2). **The `emptyHint` prop on `AcceptanceCriteriaEditor.tsx` and its three tests SURVIVE** — two of its three callers lose the attribute, the third does not (C-80). **`DefinitionOfDoneEditor.tsx` is a thin wrapper only**: it renders `<AcceptanceCriteriaEditor>` plus the label, the required asterisk and the helper line, and contains no editing logic. If you find yourself copying a handler out of `AcceptanceCriteriaEditor`, **stop and report** (C-82). |
| **G5** | Inferred-check and task-type-classifier removal guards | G1, E3 | `scripts/check-no-inferred-check.sh` *(new)*, `scripts/check-no-inferred-check.test.sh` *(new)*, `scripts/check-no-task-type-classifier.sh` *(new)*, `scripts/check-no-task-type-classifier.test.sh` *(new)* | JUDGE-FR-109, FR-110 | `pkg/agent/judge.go` (E3 did the deletion; the guard only asserts absence); `pkg/agent/behavior_scan.go`; `Makefile`, `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh` (G1 — discovery picks these up with **zero** wiring edits) |
| **E4** | Goal-record seam re-point | S1 | `pkg/agent/goal_record_wiring.go` *(`WriteRecord` and `ReadGoalState` only)*, `pkg/agent/goal_record_wiring_test.go` *(this is the judge matrix's `goal_record_wiring_adr084_test.go`; ONE file, this name)* | GOAL-FR-003 (seam), FR-005 (consumer half) | The three `emitGoalStatusFrame*` call sites in the same file (E8); `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go` (E8); `pkg/session/**` (S2/S3/S6); `pkg/goal/**` (S1/S3/S5) |
| **E5** | Task criteria and definition-of-done — record, tools and REST | S1, F1, F2 | `pkg/task/task.go`, `pkg/task/store.go`, `pkg/tools/task.go`, `pkg/sysagent/tools/task.go`, `pkg/plan/lint.go`, `pkg/gateway/rest_tasks.go`, `pkg/gateway/rest_plans.go`, and their existing `_test.go` siblings, plus `pkg/gateway/rest_tasks_criteria_test.go` *(new)* and `pkg/task/criteria_consumers_test.go` *(new — FR-030's all-consumers-repointed oracle)* | GOAL-FR-021, FR-029, FR-030, FR-047 (API half), FR-048 (API half), GOAL-MV-6 — **D-C: the gate is UNIFORM. The HTTP 400 fires on creation and on edit alike** (`POST` and `PATCH`/`PUT` on the task and plan-member routes), **and the same rejection rides both `create_task` and `update_task`** in `pkg/tools/task.go` and `pkg/sysagent/tools/task.go` (R-25). OQ-F is answered: gate edits too | `pkg/task/criterion.go`, `pkg/task/verdict.go` (F2); `pkg/agent/task_executor.go` (E12/E14); `pkg/agent/plan_engine.go` (E6/E14); `contracts/**` (F1). Re-point **all three** near-duplicate conversions in `rest_tasks.go` and **both** in `rest_plans.go` — see C-58. |
| **E6** | Goal budget, cap, and the performance settings surface | E1, S1, F1 | `pkg/config/planning.go`, `pkg/config/planning_test.go` *(existing — one of the 19 files referencing the deleted `SessionMeta.Goal*` fields; this wave re-points it)*, `pkg/config/validator.go`, `pkg/agent/plan_engine.go` *(budget and cap region only)*, `pkg/agent/plan_engine_cap_test.go` *(new — FR-049's task-goals-exempt-from-the-active-loop-cap oracle)*, `pkg/gateway/rest_performance.go`, `pkg/gateway/rest_performance_test.go`, `pkg/gateway/rest_performance_ceiling_test.go` | GOAL-FR-024, FR-025, FR-026, FR-045 (backend half), FR-049 (config half) | `pkg/config/defaults.go` (E1); `pkg/config/config.go` (E2); `pkg/agent/plan_engine.go::applyJudgeRoundOutcome` (E14); `pkg/gateway/gateway.go` — FR-049's **counter closure** is **E11's** (S4 is retired — D-F), not this wave's. **No per-goal budget field on the goal record. No override parameter on the config signature. No second read site. The single global Settings -> Performance value is read directly (D-E).** |
| **E9** | Judge parser, rung dispatch, adjudication mapping, the input identity split | F1, F2, E2, E3 | `pkg/agent/judge.go`, `pkg/agent/verifier_adjudication.go`, `pkg/agent/judge_test.go`, `pkg/agent/verifier_adjudication_test.go` *(this is the judge matrix's `verifier_adjudication_adr084_test.go`; ONE file, this name)*, `pkg/agent/judge_user_content_adr084_test.go` *(new — FR-002a)*, `pkg/agent/judge_outcome_parse_adr084_test.go` *(new — FR-015, FR-016, FR-024, FR-025)*, `pkg/agent/judge_outcome_adr084_test.go` *(new — FR-014, FR-018, FR-022, FR-062)*, `pkg/agent/judge_summarize_adr084_test.go` *(new — FR-022's unmet/could-not-verify split)*, `pkg/agent/verifier_scope_descendants_adr084_test.go` *(new — FR-010, FR-014)*, `pkg/agent/judge_input_order_adr074_test.go` *(existing — the ADR-074 input-order guard this wave's input-identity split must not break)* | JUDGE-FR-002a, FR-022, FR-070a (populates), JUDGE-D1a, D2b, D2c, D5, D5a; and C-08's `GoalID` / `GoalActiveSessionID` split | `pkg/agent/tool_result_admit.go` (E2 — consume its seams); `pkg/agent/loop.go` (E2/E13); `pkg/agent/judge_evidence_tiers.go` (E10 creates it); `pkg/task/verdict.go` (F2). **Must not populate or reference an `Outcome` field — revision 9 retired it.** **D-B — the single most important instruction in this wave:** a `met` whose evidence quote is missing, empty, whitespace-only or unverifiable **MUST NOT be rewritten**. The judge spec's §D — *"A `met` with no quote is rejected in code (D2b)"*, JUDGE-FR-024, FR-025 and FR-026 — is **CANCELLED as a rewrite**. `parseJudgeResponse` keeps the **detection** and reports it; it changes no outcome, consumes no round and flips no verdict. The per-criterion `Reason` must then say what evidence was missing or failed **and** why common sense still says the work is done. JUDGE-FR-027's count and WARN log survive unchanged, as reporting. Corroborated by GOAL-FR-038 and GOAL-FR-039. |
| **S3** | Goal retention and orphan lifecycle | S1, S2 | `pkg/goal/retention.go` *(new)*, `pkg/goal/retention_test.go` *(new)*, `pkg/session/retention_sweep.go`, `pkg/session/retention_sweep_test.go` | GOAL-FR-043, FR-044 | `pkg/session/unified_lock.go` (S2 — the lock-order doc lands there); `pkg/goal/store.go` (S1 — call its exported accessors, do not reach into internals); `pkg/task/store.go` (E5). **Nothing inside `lockAllSessionShards` may take a goal lock.** |
| **S5** | Surviving routing fields on the goal record | S1 | `pkg/goal/routing.go` *(new)*, `pkg/goal/routing_test.go` *(new)* | GOAL-FR-032, FR-033, FR-034, FR-035 (persisted half) | `pkg/agent/goal_triggers.go` — the routing helpers are E12's; `pkg/goal/goal.go` (S1 declares the two fields); `pkg/session/**` |
| **E8** | Goal terminal transition and the goal-status frame signature | S1, E4 | `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go`, `pkg/agent/goal_record_wiring.go` *(the three frame call sites)*, `pkg/agent/goal_loop_test.go`, `pkg/agent/goal_triggers_test.go`, `pkg/agent/goal_terminal_transition_test.go` *(new — this is the goal spec's `goal_terminal_test.go`; ONE file, this name)*, and **`pkg/agent/goal_loop.go::goalIdleExpirySweep`** by name (R-06) | GOAL-FR-027, FR-028, FR-049 (the predicate the counter reads); JUDGE-FR-057a, FR-083, FR-093, FR-102 (the frame carriage) | `pkg/agent/task_executor.go` (E12/E14); `pkg/agent/judge.go` (E3/E9/E10); `checkGoalLoopAfterTurn`'s claim, park and dispatch switch arms (E13); `settleGoalNormally` (E13); `pkg/agent/goal_compile.go` (E14) |
| **E10** | Evidence tiers and provenance | E9 | `pkg/agent/judge_evidence_tiers.go` *(new)*, `pkg/agent/judge_evidence_tiers_test.go` *(new — this is the judge matrix's `judge_evidence_tiers_adr084_test.go`; ONE file, this name)*, `pkg/agent/verifier_provenance.go` *(new)*, `pkg/agent/verifier_provenance_test.go` *(new — this is the matrix's `verifier_provenance_adr084_test.go`; ONE file, this name)*, `pkg/agent/verifier_grounding_adr084_test.go` *(new — FR-006, FR-007a, FR-014a, FR-028, FR-029, FR-063a; **D-B**: these assert the Judge REPORTS missing or failed evidence, never that it flips a verdict)*, `pkg/agent/judge_declared_check_adr084_test.go` *(new — FR-036, FR-038)*, `pkg/agent/judge_noncoding_goal_adr084_test.go` *(new — FR-111)*, `pkg/agent/behavior_scan_test.go` *(existing — FR-107's guard-against-extension row; **the test only, `behavior_scan.go` itself stays forbidden below**)*, `pkg/agent/judge.go`, `pkg/agent/verifier_adjudication.go` | JUDGE-FR-069a, FR-104, FR-105, FR-106, FR-108, JUDGE-D7, D14 | `pkg/agent/behavior_scan.go` — FR-107 forbids widening it; `pkg/agent/tool_result_admit.go` (E2 — the tier-1 capture rides its seam); `pkg/agent/judge.go::finalizeVerdict` — no wave adds a projection call there (C-12). **D-B: evidence tiers and provenance are REPORTING obligations, never gates.** No tier value, no provenance value and no quote check may flip a criterion's outcome, withhold a verdict or downgrade a `met`. They exist to make a weak `met` **visible**, not to overrule the Judge — the Judge judges as a human would, on common sense, and has the authority. GOAL-FR-039 already states this ("MUST NOT be implemented as a proof gate") and GOAL-FR-038 forbids preferring, prioritising, deferring or differently routing a criterion that happens to carry a machine-checkable form. |
| **E11** | Judge rubric rewrite, **and the active-goal admission counter re-homed here from the retired S4** | E1, E2, E9, S1 | `pkg/coreagent/core.go` *(the `JudgeDefaultRubric` constant only)*, `pkg/gateway/gateway.go` *(the `RegisterActiveCounter("goal", …)` closure only)*, `pkg/coreagent/judge_rubric_adr084_test.go` *(new — the ONLY test file this wave writes, and the ONLY file it writes in `pkg/coreagent` besides the rubric constant; **E1 owns `pkg/coreagent/core_test.go` and this wave must not touch it**)* | JUDGE-D1, D2d (the rubric rewrite); **GOAL-FR-049 (the closure)** | `pkg/gateway/judge_rubric_selfcheck.go` and its test — **must NOT be created (D-F)**; `allStaticToolNames` and the six seed maps in the same file (E1); `pkg/agent/judge.go` (E9/E10); every other region of `gateway.go` — with S4 retired this wave is `gateway.go`'s **only** writer in the whole delivery. **Retired here by D-F:** JUDGE-FR-077, FR-078 (server half) and FR-080 — the rubric capability self-check, the degraded-install detection and its report are judge spec §M, "the mixed state", which is an upgrade scenario and is cancelled in full. The rubric constant rewrite itself **stands** — that is not an upgrade path. **D-B is binding on the rubric text:** it MUST instruct the Judge to REPORT missing or failed evidence and to justify why common sense still says the work is done, and it MUST NEVER instruct the Judge to flip a `met` to `unmet` because a quote did not verify. The grounding controls are reporting obligations, never gates. **Never created (D-F / D-H):** `pkg/gateway/judge_rubric_selfcheck_adr084_test.go`, `pkg/gateway/judge_soul_migration_adr084_test.go`, `pkg/coreagent/judge_rubric_history_adr084_test.go`. **The counter's predicate (R-22):** the `"goal"` counter returns the number of goal records with `owner_kind == session` **and** `state == active`, read from `pkg/goal`; the definition phase and every terminal state count 0, and task-owned goals are excluded by `owner_kind`. **Pairs with E8 in the same round** — E8 deletes the `pe.Admit("goal")` / `pe.Release("goal")` calls in `pkg/agent/goal_loop.go` in its own commit; different files, same round, and the round only closes when both are merged and green. |
| **U2** | Goal-status pill — the five new state values and their reasons (`src/store/chat.ts` region 1 of 2) | F1 | `src/components/chat/GoalPillTray.tsx`, `src/components/chat/GoalPillTray.test.tsx`, `src/components/chat/GoalIndicator.tsx`, `src/components/chat/GoalIndicator.test.tsx`, `src/store/chat.ts` | JUDGE-FR-057a, FR-083, FR-093, FR-102 (render half); GOAL-FR-028 (pill half) | `src/components/chat/tools/SetGoalToolUI.tsx` — no change; its `sanitizeCriteria` must **not** gain a fourth accepted status; `src/components/chat/GoalEchoCard.tsx` — no requirement needs it once the fourth status is withdrawn; `src/lib/api.ts` (U1); `CriteriaVerdictList.tsx` (U5); `src/components/chat/ChatScreen.tsx` (B8); `src/lib/ws.ts` (**nobody** — edge validation is generated; adding a re-export alias there is dead weight, C-73/D-l); `.github/workflows/pr.yml` (G1 — your new tests are already covered by the vitest matrix's `components-chat` and `lib-store` groups; if `check-vitest-coverage.mjs` fails you have put a test in an unlisted directory, so move the test, do not edit `pr.yml`). **In `src/store/chat.ts` own the GOAL region only** — the state narrowing, `goalPills`, `mergeGoalPillFrame`, `GOAL_TERMINAL_STATES`, `buildGoalAckInsertion`, `case 'goal_status'` — and add **no** reducer arm for any browser frame; B8 rebases onto you (C-73). **Do not rename or remove `cancelStream` or `sessionsById[…].isStreaming`** — `BrowserLiveView.tsx` (B5) reads both. **U2 and F1 merge in ONE pull request, in round 1 (C-17, C-68).** |
| ~~**U3**~~ | ~~Judge degraded-capability badge on the agent card~~ **RETIRED — D-F** | — | **none — write nothing** | — | U3 existed only to render JUDGE-FR-078, which is judge spec §M, "the mixed state": a report about an install whose Judge soul predates the new rubric. D-F retires every upgrade scenario in all three designs, so the badge has nothing left to describe. `src/components/agents/AgentCard.tsx` and `AgentCard.test.tsx` are **written by no wave**. Do not create a badge, a sentinel list or a `rubric_capability` reader anywhere in the SPA. |
| **T1** | Criterion-status vocabulary lock and the hand-synced-copies comparator | F1, F2 | `pkg/task/criterion_test.go`, `pkg/api/generated/contract_test.go`, `src/lib/api/criterionStatusEnum.test.ts` *(new)* | GOAL-FR-037, FR-042, GOAL-MV-7, GOAL-SC-008; JUDGE-FR-073a, FR-075, FR-076a | `pkg/task/criterion_adr084_test.go` — cancelled, must not be created; `src/lib/api/generated/**` — no hand-written test in the generated tree; `pkg/task/criterion.go` (F2) — this wave tests it, never edits it; `contracts/**` (F1) — a guard that edits what it guards is not a guard. **Assert behaviourally (call `IsValidCriterionStatus` over a candidate table), never by grepping source text.** The comparator keys on (schema, path), never on field name (C-56). |
| **E12** | Keeper unification, activation, goal routing helpers | S1, S5, E4, E7, E8 | `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go`, `pkg/agent/task_executor.go` *(the activation and `ClaimForRun` dispatch region only)*, `pkg/tools/set_goal.go` *(including **JUDGE-FR-006b's SECOND clause**, which F2 reported as unowned: a `set_goal` `mode:update` that LOWERS a criterion's persisted `clause_count` while a verdict for that criterion id exists MUST be rejected. F2 built only the compute-and-persist half, correctly — the rejection needs a verdict lookup at the update call path, which is this wave's file. Oracle: `TestSetGoalUpdate_CannotLowerPersistedClauseCountWithVerdictOnRecord`)*, `pkg/tools/set_goal_test.go` *(existing — references the deleted `SessionMeta.Goal*` fields; re-pointed here)*, `pkg/tools/set_goal_adr084_test.go` *(new — JUDGE-FR-110's negative assertions)*, `pkg/agent/goal_loop_test.go`, `pkg/agent/goal_triggers_test.go`, `pkg/agent/goal_keeper_repairs_test.go`, `pkg/agent/goal_first_move_test.go`, `pkg/agent/goal_compile_test.go`, `pkg/agent/goal_forcing_bounded_escape_test.go`, `pkg/agent/goal_activation_test.go`, `pkg/agent/goal_record_anchor_test.go`, `pkg/agent/goal_loop_adr057_test.go`, `pkg/agent/goal_flow_integration_test.go`, `pkg/agent/origin_gating_test.go`, `pkg/agent/conformance_design_test.go` *(the ten preceding files all reference the deleted `SessionMeta.Goal*` fields and are re-pointed by this wave — R-17)*, `pkg/agent/goal_triggers_routing_test.go` *(new — FR-032–FR-035 engine half)*, `pkg/agent/goal_claim_wiring_adr084_test.go` *(new — JUDGE-FR-091, `handleBareGoalClaim`)*, `pkg/agent/task_completion_signal_test.go` *(existing — the marker-claim-path regression row for JUDGE-FR-091)*, `pkg/agent/task_executor_goal_test.go` *(new — the activation oracle; **distinct from T3's `task_executor_goal_loop_test.go`, which is the attempt-accounting file** — neither wave writes the other's) | GOAL-FR-009 – FR-016, FR-018, FR-020, FR-022, FR-023, FR-032 – FR-035 (engine half), FR-052; JUDGE-FR-091 (`handleBareGoalClaim`'s body — C-23) | `pkg/agent/task_executor.go::adjudicateClaim` (E14); `settleGoalNormally` and the push ladder (E13); `checkGoalLoopAfterTurn`'s deferred-dispatch handoff (E13); `pkg/agent/loop.go`, `pkg/agent/turn.go` (E2/E13). **No per-goal budget field on the goal record. No override parameter on the config signature. No second read site. The single global Settings -> Performance value is read directly (D-E).** |
| ~~**S4**~~ | ~~Boot orphan detection and the active-goal admission counter~~ **RETIRED — D-F** | — | **none — write nothing.** `pkg/gateway/goal_orphan_boot.go` and `pkg/gateway/goal_orphan_boot_test.go` are **NOT created**, by any wave, ever | — | S4 carried two halves. The **boot orphan detector** (GOAL-FR-051, GOAL-US-12) is an upgrade scenario and is retired in full by D-F: there are no orphaned goals to detect, because there is no pre-existing install. The **active-goal admission counter** (GOAL-FR-049's closure) is *not* an upgrade scenario, so it survives and is **re-homed to E11**, which now owns `pkg/gateway/gateway.go` alone. R-33's boot-sweep lock-order paragraph lapses with the sweep. R-07 **dissolves** — see its row in the grill resolutions. |
| **U4** | Create-task form — the create half of the eight changes | U1 | `src/components/workspaces/CreateTaskSlideOver.tsx`, `src/components/workspaces/CreateTaskSlideOver.test.tsx`, `src/components/workspaces/CreateTaskSlideOver.initialDue.test.tsx`, `src/components/workspaces/CreateTaskSlideOver.criteria.test.tsx` *(new)*, `src/components/workspaces/CreateTaskSlideOver.plan.test.tsx` *(new)* | GOAL-FR-047, FR-053 (create half), FR-056, FR-057 (create half), FR-059, FR-060 (create half) — **D-C: the criteria + definition-of-done gate is uniform across creation and editing. This wave owns the CREATE half only**; U5 owns the edit half on the task panel and on the calendar slide-over | `TaskDetailPanel.tsx` (U5) — do not reach across to "finish" a change; `TaskChecklistField.tsx` (U5) — the create form has its **own inline** checklist, relabel that; `AcceptanceCriteriaEditor.tsx` and `DefinitionOfDoneEditor.tsx` (U1) — consume, do not edit; `CreatePlanSlideOver.tsx` (C-63, C-80); the `isScheduledTrigger` / `buildTrigger` helpers — FR-060 removes two **controls**, not the model field, the helpers or the calendar editor; **any aria-label containing the word "checklist"** — lines 579 and 597 stay byte-identical, GOAL-FR-057 is a visible-text change only (C-79); remove only the `emptyHint` **attribute** at line 405, never the prop (C-80) |
| **U5** | Task detail panel and calendar slide-over — DoD group, editable title, verdict render, and the uniform edit gate | U1 | `src/components/workspaces/TaskDetailPanel.tsx`, `.../TaskDetailPanel.test.tsx`, `.../TaskDetailPanel.no-milestone.test.tsx`, `.../TaskDetailPanel.dod.test.tsx` *(new)*, `.../TaskChecklistField.tsx`, `.../TaskChecklistField.test.tsx`, `src/components/calendar/CalendarEventSlideOver.tsx`, `src/components/calendar/CalendarEventSlideOver.test.tsx` *(R-26)*, `src/components/workspaces/CriteriaVerdictList.tsx`, `src/components/workspaces/CriteriaVerdictList.test.tsx`, `.../TaskDetailPanel.title.test.tsx` *(new)*, `.../TaskDetailPanel.criteria.test.tsx` *(new)*, `.../taskFormLabels.test.tsx` *(new — FR-056, FR-057 across **both** forms)*, `.../taskFormNoTrigger.test.tsx` *(new — FR-060 across **both** forms)*. *(The two cross-form files land here, not in U4, because they assert the create form and the panel agree and U5 is the later of the two. They **render** `CreateTaskSlideOver.tsx`; they do not edit it.)* | ~~GOAL-FR-046~~ **RETIRED (D-E — there is no per-goal budget override)**, FR-053 (panel half), FR-054, FR-055, FR-056, FR-057 (panel half), FR-058, FR-060 (panel half); JUDGE-FR-074, FR-076a (render) | `CreateTaskSlideOver.tsx` (U4); the panel renders the shared checklist three times and its test queries the **aria-labels**, so change only the visible header (`TaskChecklistField.tsx:103`) and the placeholder (line 160) and leave the three aria-labels at lines 141, 151 and 171 **byte-identical** (C-62 as revised by C-79); `src/components/settings/PerformanceSection.tsx` (U6) — **the one global setting is the ONLY budget control in the product (D-E); this panel renders no budget control at all, and `GoalBudgetField.tsx` must not be created**; `src/components/workspaces/CriteriaVerdictList.status.test.tsx` — cancelled, must not be created. **No Save button — the panel autosaves.** **GOAL-FR-054 is satisfied by BOTH halves in one `<Field label="Definition of Done">`** (C-81): the DoD editor autosaving via `doUpdate({ dod })` against F1's new `TaskUpdateRequest.dod`, **and** the `dod` prop passed to the existing `<CriteriaVerdictList>`. Write **zero** new rendering code inside `CriteriaVerdictList.tsx` — it already accepts `dod` and already renders the labelled group; the panel simply never passes it. Remove the `emptyHint` **attribute** at line 642, never the prop (C-80). **D-C — the uniform edit gate is this wave's:** an edit that would leave a task with no acceptance criteria, or with no definition of done, must be refused in the panel **and** in the calendar slide-over, matching FR-047's HTTP 400 on the update path. The panel autosaves, so the refusal is a blocked autosave with a stated reason and a focused empty list — not a disabled Save button, which does not exist here. The calendar slide-over gets the same two editors U4 adds to the create form, on **both** its create and its edit paths (R-26). |
| **U6** | Settings → Performance — the install-wide goal budget default | U1, F1, E6 | `src/components/settings/PerformanceSection.tsx`, `.../PerformanceSection.test.tsx`, `.../PerformanceSection.autoDefault.test.tsx`, `.../PerformanceSection.goal.test.tsx` *(new — the one global goal-tries control, D-E)* | GOAL-FR-045 (SPA half) | `src/lib/api.ts` (U1) — this wave only calls the existing wrappers; the two performance schemas (F1); `GoalBudgetField.tsx` — **must not be created by anyone (D-E)**. **No new settings tab — the screen already has eleven.** **D-E: this single global goal-tries setting is the only budget control in the product, and it governs task goals and chat goals identically.** There is no per-goal override — not on the task panel, not in chat, not on the wire, not in the store. Label and help text must say plainly that the setting applies to every goal. |
| **G4** | Race surface — add `pkg/goal` to the `-race` gate | G1, S1 | `scripts/race-packages.sh` | GOAL-FR-008 (verification half) | `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh` — both already consume this script; neither may be edited to hardcode a package list again; `Makefile` (G1). **Must not land before `pkg/goal` exists** — a pattern matching no directory makes the race gate error, not skip. |
| **E13** | Claim resolution, deferred dispatch, and the unified push ladder — last on `loop.go` and `turn.go`, and constrained by two browser tripwires | E2, E10, E12, **B123** | `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go`, `pkg/agent/loop.go` *(`runAgentLoop`'s reordering, and the ADR-081 D3 base predicate)*, `pkg/agent/turn.go`, `pkg/agent/goal_loop_test.go`, `pkg/agent/goal_triggers_test.go`, `pkg/agent/goal_keeper_repairs_test.go`, `pkg/agent/goal_triggers_adr084_test.go` *(new — FR-095's quiet-window zero-dispatch oracle)*, `pkg/agent/goal_claim_after_delivery_adr084_test.go` *(new — FR-098's observed publish-then-dispatch seam order)* | GOAL-FR-017, FR-019 (the push half); JUDGE-FR-092, FR-095, FR-097, FR-098, FR-099, FR-101, JUDGE-D13 | `pkg/agent/tool_result_admit.go` (E2); `loop.go`'s tool-dispatch refusal point (E2's region); `pkg/agent/task_executor.go` (E12/E14). **Must not add FR-020a's aggregate withholding counter anywhere — revision 9 retired it (C-51).** **D-A — the nudge and re-post text belong to this wave, and they are the whole fix.** Both prompt builders live in `pkg/agent/goal_triggers.go`: **`goal_triggers.go::goalContinuePushPrompt`** (FR-014b's keeper re-post) and **`goal_triggers.go::goalNudgePrompt`** (ADR-081 D6c's recordless nudge). **Each MUST carry a line telling the agent that if it believes the work is done it must say so by calling the claim tool (`goal_claim`), naming the tool.** That fixes the cause — the agent does not know it has to claim — rather than the symptom. **Do NOT bound the nudge ladder and do not add a terminator of any kind:** the seven-day idle-expiry sweep stays the sole terminator for a goal that never claims (D-A), and the sweep itself belongs to E8. Deletes `goalZeroOutputTripleHolds` and `sessionHasTranscriptOutputSince` **and** rewrites their direct call in `goal_keeper_repairs_test.go` in the same commit. **Three binding ADR-085 constraints (C-71, C-72, C-87):** (1) rebase onto B123 for both `loop.go` and `turn.go`, and touch neither its control-gate deferral branch in `runTurn` nor its `turnState` ledger — E13 adds only its `turnResult` deferred-dispatch field; (2) add **ZERO** new `routingSessionID` reads to either file — if you genuinely need one, perform the whole four-part amendment in `pkg/agent/routing_session_id_consumer_set_adr057_test.go` in your own commit and hand over the new baseline in writing; (3) leave `loop.go`'s `PublishInbound` call at exactly **one** non-test site and assign `bus.InboundMessage.OperatorPrompt` **nowhere**. If the push ladder genuinely needs a second publish site, **stop and report before writing it**. |
| **G2** | ADR-084 revision-9 withdrawal guard | G1 | `scripts/check-no-unable-to-verify-outcome.sh` *(new)*, `scripts/check-no-unable-to-verify-outcome.test.sh` *(new)* | GOAL-FR-037, FR-042 (mechanical half) | `pkg/agent/goal_compile.go`, `pkg/agent/judge.go`, `pkg/agent/verifier_adjudication.go`, `pkg/agent/behavior_scan.go` — the internal tracker and non-verdict classification are **preserved** by ADR-084 §10 and must be **outside the guard's scan set by construction**, not by exemption. The guard scans `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/` and `src/components/` only — all exactly zero today. `Makefile`, `pr.yml`, `runci.sh` (G1) |
| **G3** | ADR-084 D1 closure guard | G1, E1, E2 | `scripts/check-adr084-closures.sh` *(new)*, `scripts/check-adr084-closures.test.sh` *(new)* | JUDGE-FR-061a | `pkg/coreagent/core.go` (E1/E11) — the guard reads, never writes; `Makefile`, `pr.yml`, `runci.sh` (G1). **Scope it to co-presence at HEAD and describe it honestly — a script cannot enforce merge order (see OQ-13).** **D-B:** no fixture in this wave may assert that a `met` is rejected, downgraded or flipped because its quote failed to verify. A failed quote is reported and justified, never gated. |
| **E14** | The verdict → criterion-status projection — one writer, three recording sites | E5, E6, E8, E12, E13, T1 | `pkg/agent/verdict_projection.go` *(new)*, `pkg/agent/verdict_projection_test.go` *(new — this is the judge matrix's `verdict_status_projection_adr084_test.go`; ONE file, this name, because `verdict_status_projection.go` must never exist)*, `pkg/agent/goal_compile.go`, `pkg/agent/goal_triggers.go` *(the projection call and the criteria-carrying frame swap)*, `pkg/agent/task_executor.go` *(`adjudicateClaim` — the projection call **and** the refusal/CAS reasons)*, `pkg/agent/plan_engine.go` *(`applyJudgeRoundOutcome` only)* | GOAL-FR-031, FR-036, FR-038, FR-039, FR-040, FR-041; JUDGE-FR-076 (step 2 only), FR-057a and FR-083 (the task-run-record reasons) | `pkg/agent/verdict_status_projection.go` — **must never be created**; `pkg/agent/judge.go` (C-12 — `finalizeVerdict` is not a write path); `pkg/agent/verifier_adjudication.go` (E9/E10) — the projection's only input is the per-criterion `Met` bool; `pkg/task/criterion.go` (F2); `contracts/**` — **the projection needs no contract change at all**; `src/components/workspaces/CriteriaVerdictList.tsx` (U5); `pkg/agent/plan_engine.go`'s budget region (E6) |
| **G6** | Claimless-adjudication guard | G1, E13 | `scripts/check-no-claimless-adjudication.sh` *(new)*, `scripts/check-no-claimless-adjudication.test.sh` *(new)* | JUDGE-FR-095, FR-097 (mechanical half) | `pkg/agent/goal_triggers.go` (E13 did the deletions); `pkg/agent/goal_keeper_repairs_test.go` (E13 rewrote its direct call — **the guard must not land before that rewrite or it fails on merge day**); `Makefile`, `pr.yml`, `runci.sh` (G1) |
| **S6** | Goal fields leave session meta *(the deletion)* | S2, E4, E8, E12, E13 | `pkg/session/unified_meta_files.go`, `pkg/session/unified.go`, `pkg/session/daypartition.go`, `pkg/session/goal_meta_greenfield_test.go`, `pkg/session/unified_meta_split_adr057_test.go`, `pkg/session/unified_listsessions_adr057_test.go`, `pkg/session/unified_stats_flush_adr057_test.go` *(the four REAL `pkg/session` test files referencing the goal fields — verified 2026-09-11; `unified_meta_files_test.go` DOES NOT EXIST and must not be created, see R-34)* | GOAL-FR-005 (the deletion half) | `pkg/session/unified_lock.go`, `pkg/session/pending_ask.go` (S2); `pkg/session/retention_sweep.go` (S3); `pkg/agent/**`, `pkg/gateway/**` — **every reader must already be re-pointed; if anything still fails to compile here, that is a missed re-point in an earlier wave and must be reported, not fixed in place**. **D-F:** with GOAL-FR-051 retired there is no legacy presence check to preserve, so R-34's "keep `u5ReadGoalFile`" lapses — delete `u5ReadGoalFile`, `u5WriteGoalLocked` and the `writeGoal` branch of `writeMetaLocked` together, leaving no reader behind, and let S3's retention pass remove any stale file. R-07's instruction to add S4 to this wave's `dependsOn` also lapses: S4 does not exist. |
| **T2** | Keeper and push-ladder parity, and the goal parity suite | E13, E14 | `pkg/agent/goal_triggers_task_test.go` *(new)*, `pkg/agent/goal_triggers_test.go`, `pkg/agent/goal_parity_test.go` *(new)* | GOAL-FR-014, FR-015, FR-016, FR-017, FR-018, FR-019, FR-020, FR-052; JUDGE-FR-095, FR-096, FR-097 (oracles) | All production code — this wave writes tests only. The "these predicates are unreferenced" assertion belongs in G6's guard, **not** in a Go test: a Go test asserting a symbol is unreferenced is a source-text scan and cannot survive a merge. Merge the goal spec's seven-row suppression matrix and the judge spec's eight surviving behaviours into **one table-driven test whose rows are the union** — they overlap on five of seven rows and are not identical. **D-A oracles, both required:** (1) assert that `goalContinuePushPrompt` and `goalNudgePrompt` each instruct the agent to call the claim tool by name; (2) assert the nudge ladder has **no** terminator of its own — a goal that keeps working and never claims stays active until the seven-day idle-expiry sweep reaches it, and no bounded push count ends it. |
| **T3** | Attempt accounting and hard ceiling, re-homed to files that exist | E12, E14 | `pkg/agent/task_executor_adjudicate_claim_test.go`, `pkg/agent/attempts_vs_rounds_test.go`, `pkg/agent/task_executor_goal_loop_test.go` *(attempt accounting only — **E12 owns `pkg/agent/task_executor_goal_test.go`**; two files, two waves, no overlap)* | GOAL-FR-022, FR-023, FR-026, FR-049 (oracles) | `pkg/agent/task_executor_test.go` — **this file does not exist; do not create it to match the spec's table, and never report a scoped run against it as green**; `pkg/agent/task_executor.go` (E12/E14); `evidence_gate_test.go`, `task_completion_contract_test.go`, `task_executor_drain_test.go`, `task_executor_stop_toctou_test.go` — protected regression set, no wave edits them |
| **T4** | Judge regression rewrites, audit, cancel and CAS assertions | E9, E10, E11, E13, E0 | `pkg/agent/judge_evidence_quote_test.go`, `pkg/agent/verifier_antipatterns_adr052_qa_test.go`, `pkg/agent/verifier_registry_test.go`, `pkg/agent/verifier_cancel_adr084_test.go` *(new)*, `pkg/agent/verifier_audit_adr084_test.go` *(new — FR-084; it lives here and not in E0 because **E0 must not touch `pkg/agent/**`**)*, `pkg/config/judge_mcp_wildcard_adr084_test.go` *(new)*, `pkg/tools/compositor_judge_mcp_adr084_test.go` *(new)*, `pkg/coreagent/judge_seed_test.go` | JUDGE-FR-058, FR-061a (co-presence), FR-082, FR-084, FR-103 | All production code. **No test in this wave may assert `unable_to_verify` as a criterion outcome, a fourth status, or an `outcome` field** — every such row of the judge spec's matrix is cancelled (C-01, C-18). |
| **G7** | The two ADR-086 merge guards | G1, E8, E14 | `scripts/check-no-goal-field-erasure.sh` *(new)*, `scripts/check-no-goal-field-erasure.test.sh` *(new)*, `scripts/check-single-verdict-projection.sh` *(new)*, `scripts/check-single-verdict-projection.test.sh` *(new)* | GOAL-FR-028 (mechanical half), GOAL-FR-036 (mechanical half) | `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go` (E8/E12/E13/E14); `pkg/agent/verdict_projection.go` (E14); `Makefile`, `pr.yml`, `runci.sh` (G1). Guard 1 fails if `clearGoal`-style field-zeroing gains a non-test call site; guard 2 fails if `verdict_status_projection` reappears or a second non-test assignment of the met/unmet constants appears outside `verdict_projection.go`. |

### The ADR-085 browser lane

| Wave | Title | Depends on | Write-set (exact repo-relative paths) | Delivers (FR ids) | Must NOT touch |
|---|---|---|---|---|---|
| **B0** | Cross-cutting carriers — the deferral marker, the root-chat tool-context key, the operator-prompt bus field *(reduced: `config.go` moved to E2)* | — *(E2 supplies `ControlIdleReleaseSec`, already merged in round 1)* | `pkg/tools/result.go`, `pkg/tools/result_test.go`, `pkg/tools/base.go`, `pkg/tools/base_test.go`, `pkg/bus/types.go` | BROWSER-FR-012a, and the declarations FR-021 and FR-029 are built on | `pkg/config/config.go` and `pkg/config/defaults.go` (E2/E1 — C-70); `pkg/tools/resolvepath.go`, `pkg/tools/filesystem.go` (E0); `pkg/tools/set_goal.go` (E12); `pkg/tools/task.go` (E5); `pkg/tools/goal_claim.go` (E7); `pkg/tools/browser/**` (B123, B6); `pkg/agent/**`; `pkg/gateway/**`. **`ToolResult.Deferred` carries `json:"-"` — it is engine-internal and must never cross the gateway/SPA boundary (Constraint #8).** |
| **B7** | Browser audit event vocabulary | — | `pkg/audit/events.go` | The deferral / handover / `browser_control_idle_release` / take-control-disabled-sweep event constants consumed by B123 (BROWSER-FR-020, FR-031, FR-052) | `pkg/audit/**` other than `events.go`; `pkg/tools/browser/audit.go` (B123); `pkg/agent/**`; `pkg/gateway/**`; `scripts/race-packages.sh` (G4) — `pkg/audit` is not on the race surface and this wave does not put it there |
| **B5** | SPA live-panel take-over rebuild | — *(no contract dependency; scheduled in round 6 only because rounds 1–5 are at the concurrency cap — see D-k)* | `src/components/browser/BrowserLiveView.tsx`, `.../BrowserLiveView.takeTheWheel.test.tsx`, `.../BrowserLiveView.handover.test.tsx` *(new)*, `.../BrowserLiveView.controlToggle.test.tsx`, `.../BrowserLiveView.tabStrip.test.tsx` *(new)*, `.../BrowserLiveView.agentChip.test.tsx` *(new)* | BROWSER-FR-001, FR-031b (client half), FR-053, FR-054, FR-055, FR-056, FR-057, FR-058, FR-059 | `src/store/chat.ts` (U2, then B8) — B5 **consumes** the store: it removes the `useChatStore.getState().cancelStream(sessionId)` call inside `BrowserLiveView.tsx` and the now-unused import, and edits nothing in the store itself; `src/lib/ws.ts` (nobody); `src/lib/api.ts` (U1); `src/lib/api/generated/**` (F1); `src/components/chat/**` (U2/B8); `src/components/workspaces/**`, `src/components/agents/**`, `src/components/settings/**`; `src/lib/toolVisibility.ts` — `browser_handover` gets **no** hide entry (C-90); `contracts/**` — **no contract change is needed**, `BrowserStatusFrame.yaml` already carries `released` and `control_only` (C-86); `.github/workflows/pr.yml` (G1 — `src/components/browser/` is already in the `components-misc` vitest group). **No `data:image/jpeg;base64` sink may be created here** — `scripts/check-no-jpeg-screencast.sh` scans this exact directory for that shape. The `controlToggle` test's "never renders a Take control / Release control / Hand to agent button" case stays green unchanged. BROWSER-FR-053 also deletes `agentPausedByUser` / `agentPausedByUserRef` / `setAgentPausedByUser`; the test asserting their absence is part of this wave. **D-G: the hold is shown PER TAB.** The tab strip must make plain which tab the person is holding and that the agent is free to work in another tab; the panel never offers a hand-back button, and the wheel returns only when the agent receives a new prompt. If B123 reports the lock is not per-tab, this wave's tab-strip rendering is blocked on the operator's answer — say so and stop, do not invent a per-tab display over a per-session lock. |
| **B123** | Gate semantics, lock lifecycle and turn engine — ADR-085's W1+W2+W3 as **three lanes worked SEQUENTIALLY by ONE agent, one branch, one pull request** (C-74; one agent by **D-I** — the six-per-round cap counts AGENTS, not slots) | B0, B7, F1⊕U2, F2, E2 | **Lane 1 (gate semantics):** `pkg/tools/browser/tools.go`, `tools_interact.go`, `tools_snapshot.go`, `tabs.go`, `audit.go`, `key.go`, and the tests `tools_control_test.go`, `control_gate_membership_test.go`, `audit_test.go`, `lease_membership_test.go`, `interact_test.go`, `implicit_acquisition_test.go`, `operator_takeover_test.go`, `snapshot_test.go`, `snapshot_audit_redaction_test.go`, `evaluate_description_test.go`, `capture_description_test.go` *(new)*, `waiting_surface_test.go` *(new)*, plus `docs/internal/specs/browser-workspace-ownership-spec.md` and `docs/internal/specs/browser-agent-capability-spec.md`. **Lane 2 (lock lifecycle and gateway):** `pkg/tools/browser/live.go`, `pkg/tools/browser/manager.go`, `pkg/gateway/browser_ws.go`, `pkg/gateway/websocket.go`, `pkg/gateway/sse.go`, `pkg/gateway/ws_ask_user.go`, `pkg/gateway/replay.go`, `pkg/channels/base.go`, and the tests `live_notcontroller_test.go`, `shared_control_test.go`, `manager_reaper_test.go`, `viewer_heartbeat_test.go`, `pkg/gateway/browser_ws_test.go`, `pkg/gateway/replay_test.go`, `pkg/gateway/browser_release_sites_test.go` *(new)*, `pkg/gateway/browser_control_handover_test.go` *(new)*. **Lane 3 (turn engine):** `pkg/agent/browser_deferral.go` *(new)*, `pkg/agent/turn.go`, `pkg/agent/loop.go`, `pkg/agent/routing_session_id_consumer_set_adr057_test.go`, `pkg/agent/browser_deferral_test.go` *(new)*, `pkg/agent/browser_resolver_test.go` *(new)* | BROWSER-FR-002, FR-003, FR-004, FR-010, FR-011, FR-012, FR-013, FR-014, FR-015, FR-016, FR-016a, FR-017, FR-020, FR-021, FR-022, FR-024, FR-026a, FR-028, FR-029, FR-029a, FR-030, FR-031, FR-031a (reader half), FR-031b (server half), FR-032, FR-033, FR-033a, FR-034, FR-035, FR-035a, FR-036, FR-037, FR-038, FR-039, FR-040, FR-041, FR-042 (emission), FR-043, FR-043a, FR-044 (id and hold timestamp), FR-047, FR-050, FR-052, FR-060, FR-061, FR-062, FR-064, FR-065 | `pkg/gateway/gateway.go` (**E11** — S4 is retired by D-F, so E11 is this file's sole owner for the whole delivery) — the release hook registers inside `browser_ws.go::newBrowserWSHandler`, which is already handed the `*agent.AgentLoop`; `pkg/gateway/rest_tasks.go`, `rest_plans.go`, `rest_performance.go`; ~~`goal_orphan_boot.go`, `judge_rubric_selfcheck.go`~~ **— struck: under D-F and D-H these two files are NEVER CREATED, by any wave, ever. There is nothing here to avoid touching. Do not go looking for them, and do not create them because a do-not-touch list named them**; `pkg/gateway/replay.go::toJudgeVerdictFrame` and the `judge_verdict` branch (F2's region — C-75); the anchor string `ADR-057 FR-089 — W5 audit classification artefact` in `pkg/gateway/websocket.go`, the comment block beneath it and its class-(a) list — preserve verbatim except the one member-count correction in C-83; `pkg/tools/browser/register.go` and `tools_handover.go` (B6); `pkg/coreagent/core.go` and `pkg/config/defaults.go` (E1); `pkg/config/config.go` (E2); `pkg/agent/loop.go::runAgentLoop` (E13); `pkg/agent/tool_result_admit.go` (E2); `pkg/agent/goal_loop.go`, `goal_triggers.go`, `task_executor.go`, `set_goal.go` (E12/E13/E14); `pkg/agent/subturn.go` (**zero owners** — C-91); `pkg/agent/tool_denial.go` (copy the `turnDenialLedger` pattern, do not edit it); `contracts/**` (F1). **`dispatchInput` must NOT consult the control latch** — it is a tool gate, and `dispatchInput`'s "NO CONTROL GATE (operator directive, 2026-08-03)" block stands. **Do not reinstate a control toggle, a resume dispatcher, a frame-persistence path or a turn-park signal.** Every new `pkg/tools/browser` test function reaching `newCoordinatorTestConfig` / `resolveTestBinary` / `resolveTestBinaryHeadlessShell` must call `skipIfNoBrowser(t)` (C-78). Keep the substring `human is currently controlling` in FR-011's reason, or amend all three marker files in the same commit (C-88). Cite `file::symbol` for `loop.go` and `turn.go`, never `file:line`. **D-G — the stand-down is PER TAB, not per browser session.** The person keeps the wheel until the agent receives a **new prompt**; there is still no hand-back button and no timer that takes it back from an attached, watching viewer. **New requirement:** if the agent's other work needs a browser while a tab's wheel is held, the agent **opens a new tab and carries on — it never waits.** This is ADR-085's own directive (*"[continue other work] yes it should continue other work"*) made concrete: continuing is the required behaviour, and a fresh tab is how it continues. **Before writing any of this, establish what the control lock actually is today.** Grep `pkg/tools/browser` for the latch: `live.go`'s `lv.controller` is a single string on a live view resolved through `r.view(sessionID)`, which reads as **per view — that is, per browser session, not per tab**. **If the lock is not per-tab today, STOP and REPORT it. Do not silently make it per-tab** — changing the granularity of a control lock is a design change, and it belongs to the operator, not to this wave. |
| **B6** | The `browser_handover` tool body and its registration *(reduced: catalogue, seed maps and ceiling moved to E1)* | E1, B123 | `pkg/tools/browser/tools_handover.go` *(new)*, `pkg/tools/browser/tools_handover_test.go` *(new)*, `pkg/tools/browser/register.go`, `pkg/coreagent/browser_handover_seed_test.go` *(new)* | BROWSER-FR-046, FR-049, FR-052 (the tool's own refusal) | `pkg/coreagent/core.go` and `pkg/config/defaults.go` (E1) — **E1 already registered `browser_handover`; editing `core.go` here risks the `validateOverrideKeys` boot panic** (C-70); `pkg/tools/browser/tools.go`, `live.go`, `manager.go`, `audit.go` (B123); `pkg/tools/base.go`, `pkg/tools/result.go` (B0); `pkg/agent/**`; `pkg/gateway/**`. New test functions must call `skipIfNoBrowser(t)` if they reach any real-Chrome helper (C-78). **D-G: the refusal path must offer the new-tab route.** When the wheel is held on the tab the agent wanted, `browser_handover`'s result tells the agent to open a new tab and continue — never to wait, and never to ask for the wheel back. |
| **B8** | The handover waiting notice in the thread, and the browser-handover e2e with its shard registration (`src/store/chat.ts` region 2 of 2) | F1⊕U2, B123, B6 | `src/store/chat.ts`, `src/store/chat.browser-handover-notice.test.ts` *(new)*, `src/components/chat/ChatScreen.tsx`, `src/components/chat/ChatScreen.browser-handover-notice.test.tsx` *(new)*, `tests/e2e/browser-control-handover.spec.ts` *(new)*, `tests/e2e/shards.json` | BROWSER-FR-042 (render half), FR-044 (SPA idempotency half), and the ADR-085 e2e coverage | `src/lib/ws.ts` — **removed from this wave's write-set** (D-l); import the new frame type from `@/lib/api/generated/asyncapi-types`, exactly as `chat.ts` already imports `GoalStatusFrame`. In `src/store/chat.ts` edit **only**: one line added to `SESSION_SCOPED_FRAME_TYPES`; a new `browserHandoverNoticeId?: string` field on `ChatMessage`; a new `buildBrowserHandoverInsertion` helper placed **after** `buildGoalAckInsertion`; and a new `case` arm placed **after** `case 'goal_status'` closes. **Do not read, edit or reference `buildGoalAckInsertion`, `goalAckMessageId`, `GOAL_TERMINAL_STATES`, `goalPills`, `mergeGoalPillFrame` or `case 'goal_status'`** (C-73). Do not rename `cancelStream` or `isStreaming` — B5 reads both. `src/components/chat/GoalPillTray.tsx`, `GoalIndicator.tsx`, `tools/SetGoalToolUI.tsx` (U2); `src/components/browser/**` (B5); `src/lib/api.ts` (U1); `src/lib/api/generated/**` (F1); `src/lib/toolVisibility.ts` (C-90); `scripts/e2e-shards.sh` — read it, do not change it; `.github/workflows/pr.yml`, `Makefile`, `deploy/ci-worker/runci.sh` (G1) — all three consume `shards.json` and need no edit. **In `ChatScreen.tsx` edit BOTH `SystemMessage` and `VirtualSystemMessageRow`, or neither** (C-90); the `data-testid="browser-handover-notice"` discriminator is required because the e2e names it as its positive observable. **The spec and its `shards.json` entry land in ONE commit, on the existing `ui-heavy` shard** (C-76). The e2e must not `POST /api/v1/auth/login`. |
| **B9** | The two ADR-085 mechanical guards | G1, B123 | `scripts/check-operator-prompt-sites.sh` *(new)*, `scripts/check-operator-prompt-sites.test.sh` *(new)*, `scripts/check-no-browser-turn-park.sh` *(new)*, `scripts/check-no-browser-turn-park.test.sh` *(new)* | BROWSER-FR-029 / FR-029a (the mechanical half), and the ADR-085 Explicit Non-Behaviors prohibitions (the mechanical half) | `Makefile`, `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh` (G1 — `scripts/guards.sh` discovers both by filename with **zero** wiring edits); `pkg/bus/types.go` (B0), `pkg/tools/browser/**` and `pkg/agent/**` (B123) — the guards read, never write; `scripts/guards-no-selftest.exempt` — **neither guard may be listed there**, both ship with a `.test.sh`. Do not scan `pkg/gateway/websocket.go`'s ADR-057 artefact block or any `*_test.go` for `OperatorPrompt` assignments — the constraint is exactly three **non-test** assignment sites. |

---

## 4. The execution schedule

> **KNOWN-RED AFTER ROUND 1 (recorded 2026-09-11, closed the same day).** F1's contract change added
> `clause_count` to the criterion shapes and four evidence fields to the per-criterion verdict shape.
> Three hand-written anonymous-struct conversion functions in `pkg/gateway` did not compile against
> the regenerated types: `replay.go::toJudgeVerdictFrame`, `rest_tasks.go` (`toWireCriteria`,
> `criteriaFromCreateWire`, `criteriaFromUpdateWire`, and the per-criterion verdict mapping) and
> `rest_plans.go` (`toWirePlanDoD` and the plan create/update DoD conversions). Nothing else in the
> tree was red: `gofmt` 0 files, `go vet` over the four package paths exit 0, `npm run typecheck`
> exit 0. **`replay.go` is F2's (round 2). `rest_tasks.go` and `rest_plans.go` were E5's (round 3);
> that slice was PULLED FORWARD and repaired immediately after round 1 so the tree would not stay red
> across two rounds — E5 still owns those files in round 3 for its own behavioural work.**
> **STATUS 2026-09-11, verified:** `rest_tasks.go` and `rest_plans.go` are REPAIRED and compile —
> ten `clause_count` field insertions across the criterion and DoD conversions, and the two
> per-criterion verdict struct literals widened to the new evidence shape. Those four new fields
> are left ABSENT on the wire deliberately: `task.CriterionVerdict` does not carry them until F2
> adds the Go fields, and E9/E10/E14 populate them. **`pkg/gateway/replay.go` is STILL RED and is
> F2's to fix in round 2** — it is the only remaining compile failure in the tree.
> If you are a round-2 agent and `go build ./...` is red in `replay.go`, that is F2's and expected;
> if the error is anywhere else, it is new and it is yours.


Waves are grouped into rounds. **Every wave in a round starts at the same time and writes to files
no other wave in that round writes.** A round ends when every wave in it is merged and green on the
base branch. No wave in round N+1 starts before round N closes.

**At most SIX AGENTS run concurrently. The cap is counted in AGENTS, not in slots** — operator item
5, recorded as **D-I**. A *slot* is one branch and one merge; an *agent* is one worker. Those were
not the same number, and the difference breached the cap twice: two slots in this plan hold more
than one wave — `F1⊕U2` is one slot holding two waves (one branch, one merge — C-68) and `B123` is
one slot holding three lanes (one branch, one merge — C-74) — so round 1 ran **7** agents and round
5 ran **8**. **Both multi-wave slots are now worked by ONE agent each**, in sequence on their one
branch. Every slot in the schedule below is therefore exactly one agent, the `Agents` column equals
the `Slots` column, and **no round exceeds 6 in it**.

**Round 1 — `F1⊕U2` is ONE agent, in sequence on one branch.** *Chosen over moving U2 to a later
round, and the reason is that moving it is not actually available.* C-68 puts U2 inside F1's slot
precisely because U2's SPA consumers must ride F1's single atomic contracts commit; pushing U2 into
a later round splits that commit, which C-04 and Constraint #8 forbid. One agent is also the
cheaper change: the two waves were already one branch and one merge, so nothing about the write-set,
the review or the merge changes — only the sequencing. **Cost:** round 1's wall-clock grows by U2's
own duration rather than being the maximum of the two. F1 is by far the larger wave, U2 is a
consumer re-point across already-generated types, so this is hours inside a round that F1 already
dominates — it does not add a round.

**Round 5 — `B123` is ONE agent running its three lanes SEQUENTIALLY (lane 1 → lane 2 → lane 3).**
*Chosen over promoting the three lanes to separate waves spread across rounds, and again the
alternative is not available.* C-74 records why: W1/W2/W3 form a **three-way compile cycle** — lane 1
reads accessors defined on lane 2's types, lane 2 registers a hook declared in lane 3's package, and
lane 3 calls a roster exported by lane 1 — so **no lane compiles, and therefore no lane merges,
without the other two**. Splitting them across rounds means merging a branch that cannot build,
which §6 forbids outright, or inventing stub symbols, which B123's own rule forbids by name.
**Cost, stated plainly:** round 5's wall-clock is now bounded by B123's three lanes end to end
instead of by the longest single lane — roughly **2 to 2.5× a single lane's duration**. B123 was
already round 5's longest pole, so round 5 lengthens by about that much; **the round count is
unchanged at 8** and no other round moves. **Disjointness is unaffected by this choice, and that was
re-checked rather than assumed:** the combined write-set is byte-identical whether three agents or
one agent types it, the three lanes' file lists were already disjoint from each other (they are
`pkg/tools/browser` gate files, `pkg/tools/browser` lock files plus `pkg/gateway`, and `pkg/agent`
respectively), and both the round-5 disjointness check below and §5's `loop.go` / `turn.go` chains
(**E2 → B123 → E13**) are stated against B123's *combined* write-set — not one lane at a time — so
neither moves by a single path. The only property that changes is internal ordering inside one
branch, which no other wave observes.

| Round | Slots (concurrent) | Slots | **Agents (cap = 6)** | What this round makes possible |
|---|---|---|---|---|
| **1** | **F1⊕U2** *(ONE agent, F1 then U2)*, E0, E1, E2, E3, G1 | 6 | **6** | The wire shapes for all three designs, the boot data (including `browser_handover`'s three registration sites), `ControlIdleReleaseSec`, the capture seam, the rung deletion, and the guard runner. Nothing downstream can start without F1. |
| **2** | F2, S1, S2, E7, U1, **B0** | 6 | **6** | The goal store exists. The Go persisted types match the contract. The SPA has a client and a DoD editor. The browser lane's cross-cutting carriers are declared. |
| **3** | E4, E5, E6, E9, S3, S5 | 6 | **6** | The goal-record seam is re-pointed; tasks carry criteria and a definition of done; the Judge parses the new shape. |
| **4** | E8, E10, E11, T1, **B7**, U4 | 6 | **6** | **A terminal goal stops erasing itself.** The status vocabulary is locked. The browser audit vocabulary is declared. The create-task form lands. *(U3 retired — D-F. U4 pulled forward from round 5 to fill the slot.)* |
| **5** | E12, U5, U6, **B123** *(ONE agent, three lanes in sequence)*, G2, G3 | 6 | **6** | The keeper runs against tasks and chats alike. The task panel, the calendar slide-over and the one global budget setting land. **The operator can take the browser wheel and the turn keeps running.** *(S4 retired — D-F. G2 and G3 pulled forward from round 6.)* |
| **6** | E13, G4, G5, **B5**, **B6**, **B9** | 6 | **6** | Adjudication becomes claim-triggered; the push ladder unifies, and the nudge text starts telling the agent to claim. `pkg/goal` joins the race gate. The browser panel's take-over UI, the `browser_handover` tool and its two guards all land. *(B6 and B9 pulled forward from round 7.)* |
| **7** | E14, G6, S6, T4, **B8** | 5 | **5** | **The verdict reaches the tick marks.** The goal fields leave session metadata. The judge regression suite and the browser waiting notice with its e2e land. *(T4 and B8 pulled forward from round 8.)* |
| **8** | T2, T3, G7 | 3 | **3** | The parity and attempt-accounting suites, and the two ADR-086 merge guards. Every one of the three waits on E14, which is why this round cannot be compressed further. |

**Total: 8 rounds, 44 slots, 45 waves — and a peak of 6 concurrent AGENTS, in rounds 1 through 6.**
*(Was 8 rounds, 46 slots, 47 waves.)* **The cap is checkable at a glance in the `Agents` column: the
largest number in it is 6.** Before adding anything to a round, add its agent count to that column
first; a round that would read 7 is not schedulable, whatever its slot count says.

**What the operator decisions of 2026-09-11 changed here.** Two waves left the graph — **S4**
(retired by D-F; its surviving half, the active-goal counter, is re-homed to E11) and **U3** (retired
by D-F with judge spec §M). That freed one slot in round 4 and one in round 5, and the schedule was
re-packed forward: **U4** moved from round 5 to round 4; **G2** and **G3** from round 6 to round 5;
**B6** and **B9** from round 7 to round 6; **T4** and **B8** from round 8 to round 7. Every move was
checked against the moved wave's own `dependsOn` column, and every round still holds at most six
concurrent slots. Earlier moves still stand: **G4 from round 5 to round 6**, **G5 from round 2 to
round 6**, and **U2 from round 4 to round 1** (forced by C-68).

**The round count did NOT fall, and the critical path did NOT shorten.** Both are 8, and they are 8
for the same reason: the critical path is a chain of eight waves that depend on one another one at a
time (below), and none of the retired work sits on it. Freeing capacity pulls the *tail* forward — it
cannot compress a chain. Round 8 is now three waves wide rather than five, which is capacity for
review and for §7's exit proof, not an invitation to invent waves.

### Folding ADR-085 in costs ZERO extra rounds

The browser lane's heavy wave, B123, has no dependency on the goal chain past F2 (round 2) and E2
(round 1), so it runs concurrently with E12 in round 5. Rounds 6 and 7 were already under-subscribed
in the original plan for the reason it gives — the graph runs out of independent work — and that is
exactly the spare capacity the browser tail consumes. The binding constraint on the browser lane is
**not** agent supply: it is the W1/W2/W3 compile cycle plus the requirement that B123 settle
`loop.go` and `turn.go` before E13 rebases onto them. Adding agents cannot shorten it.

### The critical path

`F1⊕U2 → S1 → E4 → E8 → E12 → E13 → E14 → G7`

In plain terms: the wire shape must be settled before the goal store can be built; the store before
the engine seam can point at it; the seam before a terminal goal can stop erasing itself; that
before the keeper can be unified; the keeper before adjudication can be made claim-triggered; and
all of it before a verdict can be written onto a criterion and guarded. Every link is a real
dependency on a file or a behaviour, not a convenience. **Shortening the delivery means shortening
this chain, and it cannot be shortened by adding people.** **Re-checked after the 2026-09-11
operator decisions: the chain is unchanged.** Neither retired wave (S4, U3) appears on it, and
nothing retired from a surviving wave removed a link from it. Eight links, eight rounds.

### Why rounds 7 and 8 are narrow

Four files carry chains that no reordering removes: `pkg/agent/goal_triggers.go`
(E8→E12→E13→E14), `pkg/agent/goal_loop.go` (E8→E12→E13), `pkg/agent/judge.go` (E3→E9→E10) and
`pkg/agent/loop.go` (E2→B123→E13). Late in the delivery almost all remaining work sits on one of
them. Spare capacity in rounds 7 and 8 should go to review, to running the exit proof in §7, and to
verifying the operator answers in §8 have actually been honoured — **not** to inventing extra waves,
which would only create new shared-file chains. After the 2026-09-11 re-pack round 8 is three waves
wide (T2, T3, G7) and every one of them waits on E14; that is a real dependency, not slack.

### Disjointness check

Every pair of waves scheduled in the same round was compared path by path. Result: **no overlap in
any round.** **Re-run in full after the 2026-09-11 re-pack — rounds 4 through 8 all changed
membership, and the result still holds: no two waves in any round write the same path.** The re-pack
also *removed* the delivery's tightest near-collision: with S4 retired, `pkg/gateway/gateway.go` has
exactly one writer in the whole delivery (E11), instead of two waves splitting two adjacent regions
of the same boot block. Three near-misses are worth naming because they read like collisions and are
not:

- `pkg/task/verdict.go` (F2) and `pkg/goal/verdict.go` (S1) share a filename in round 2 and are
  different packages.
- `pkg/gateway/rest_tasks.go` / `rest_plans.go` (E5) and `pkg/gateway/rest_performance.go` (E6)
  share a package in round 3 and are different files.
- `pkg/api/generated/contract_test.go` is a **hand-written** file inside the generated tree. F1's
  write-set is `pkg/api/generated/**` **except** that file, which belongs to T1. Verified: it
  contains zero references to `Goal`, so F1's rename does not reach it.

Two overlaps the analysts left in place were fixed here rather than reported:

- The projection wave originally carried `pkg/agent/judge.go`, which would have put it inside that
  file's three-wave chain. It does not touch `judge.go` at all (C-12).
- Two SPA waves and two test waves both claimed `CriteriaVerdictList.test.tsx`, `GoalPillTray.test.tsx`
  and `GoalIndicator.test.tsx`. **A component and its vitest move together** — the exhaustive
  `never` switches mean a test cannot be written against a component that does not yet compile — so
  the separate SPA-test wave was dissolved into U2 and U5 (C-41, C-42).

### Disjointness of the rounds ADR-085 enters, checked path by path

**Round 2 (B0 joins).** B0 writes `pkg/tools/result.go`, `pkg/tools/base.go` and `pkg/bus/types.go`
plus two tests. F2 is `pkg/task/` and `pkg/gateway/replay.go`; S1 is `pkg/goal/`; S2 is
`pkg/session/`; E7 is `pkg/tools/goal_claim.go` **only**; U1 is `src/`. `pkg/tools` is the near-miss:
E7 and B0 share the directory and share **no file**. Nothing else touches `pkg/bus`.

**Round 4 — E8, E10, E11, T1, B7, U4.** B7 writes exactly one file, `pkg/audit/events.go`, and no
other wave in the delivery writes anything under `pkg/audit`. E8 writes `pkg/agent/{goal_loop,
goal_triggers, goal_record_wiring}.go` and their tests; E10 writes `pkg/agent/{judge_evidence_tiers,
verifier_provenance, judge, verifier_adjudication}.go` — **same package, disjoint file by file.**
E11 now writes `pkg/coreagent/core.go` (the rubric constant) and `pkg/gateway/gateway.go` (the
counter closure) and nothing else; no other round-4 wave touches either package. T1 writes three
test files (`pkg/task/`, `pkg/api/generated/`, `src/lib/api/`); U4 writes three files under
`src/components/workspaces/`. `src/` is touched by T1 (`src/lib/api/`) and U4
(`src/components/workspaces/`) — different directories. **No overlap.**

**Round 5 — E12, U5, U6, B123, G2, G3 — the one that matters most.** B123 writes
`pkg/tools/browser/*`, `pkg/gateway/{browser_ws, websocket, sse, ws_ask_user, replay}.go`,
`pkg/channels/base.go` and `pkg/agent/{browser_deferral, turn, loop}.go`. E12 writes
`pkg/agent/{goal_loop, goal_triggers, task_executor}.go` and `pkg/tools/set_goal.go` — **disjoint
from B123 file by file inside the same two packages.** **S4 is gone, so no round-5 wave writes
`pkg/gateway/gateway.go` at all** — the sharpest collision risk in the original round 5 has been
removed rather than managed. U5 writes `src/components/workspaces/` plus the two
`src/components/calendar/CalendarEventSlideOver` files; U6 writes `src/components/settings/` —
different directories. G2 writes `scripts/check-no-unable-to-verify-outcome.sh` and its `.test.sh`;
G3 writes `scripts/check-adr084-closures.sh` and its `.test.sh` — two distinct filenames, and no
other round-5 wave writes anything under `scripts/`. **No overlap.** The correction to ADR-085's own
shared-nothing claim still stands: "`pkg/agent` is entirely W3" was true inside ADR-085 and is
**false** in the merged tree — `pkg/agent` is written by E1, E2, E3, E4, E8, E9, E10, E12, E13, E14,
T2, T3, T4 and B123. The property B123 actually needs, and which does hold, is that **no other wave
writes `pkg/agent/loop.go` or `pkg/agent/turn.go` in round 5.**

**Round 6 — E13, G4, G5, B5, B6, B9.** E13 writes `pkg/agent/{goal_loop, goal_triggers, loop,
turn}.go` and their tests. B6 writes `pkg/tools/browser/{tools_handover.go, tools_handover_test.go,
register.go}` plus `pkg/coreagent/browser_handover_seed_test.go` — a different package from E13 and
a different file from anything else in the round. B5 writes `src/components/browser/` only, and is
the round's only `src/` wave. The three script waves are disjoint by filename: G4 owns
`scripts/race-packages.sh`; G5 owns `check-no-inferred-check.*` and
`check-no-task-type-classifier.*`; B9 owns `check-operator-prompt-sites.*` and
`check-no-browser-turn-park.*`. **No overlap.** B6 and B9 both depend on B123, which closed in round
5, so pulling them forward is legal.

**Round 7 — E14, G6, S6, T4, B8.** E14 writes `pkg/agent/{verdict_projection.go and its test,
goal_compile.go, goal_triggers.go, task_executor.go, plan_engine.go}`. T4 writes only test files —
`pkg/agent/{judge_evidence_quote_test.go, verifier_antipatterns_adr052_qa_test.go,
verifier_registry_test.go, verifier_cancel_adr084_test.go}`, `pkg/config/judge_mcp_wildcard_adr084_test.go`,
`pkg/tools/compositor_judge_mcp_adr084_test.go`, `pkg/coreagent/judge_seed_test.go`. **E14 and T4
share `pkg/agent` and share no file:** E14 writes exactly one `_test.go` (its own new
`verdict_projection_test.go`) and T4 writes none of E14's production files. This is the one pair in
the re-pack worth re-checking by hand before dispatch, and it was. S6 is `pkg/session` only; G6 is
`scripts/check-no-claimless-adjudication.*`; B8 is `src/store/chat.ts`,
`src/components/chat/ChatScreen.tsx` and `tests/e2e/` — the round's only `src/` and only `tests/`
wave. **No overlap.** B8 depends on B6, which closed in round 6.

**Round 8 — T2, T3, G7.** T2 writes `pkg/agent/{goal_triggers_task_test.go, goal_triggers_test.go,
goal_parity_test.go}`; T3 writes `pkg/agent/{task_executor_adjudicate_claim_test.go,
attempts_vs_rounds_test.go, task_executor_goal_loop_test.go}` — same package, six distinct files,
no overlap. G7 is `scripts/` only. **No overlap.**

---

## 5. Shared-file chains

For every file more than one wave writes: the strict order, and the region each wave owns. **A wave
later in a chain rebases onto the wave before it. A wave may not begin its edit to a shared file
until the previous wave in that file's chain is merged and green.**

| File | Order | Region each wave owns |
|---|---|---|
| `pkg/coreagent/core.go` | E1 → E11 | **E1:** the seed, the skills list, `allStaticToolNames`, and the six per-agent tool-policy seed maps — all static boot data, one commit. **E11:** the `JudgeDefaultRubric` constant only. E11's commit must not merge until E1's closure tests and E2's budget tests are green on the base branch. |
| `pkg/config/defaults.go` | E1 only | **E1** lands all four additions — the judge timeout/cap/token-ceiling defaults, the `goal_claim` policy-ceiling entry, the goal budget defaults, and `browser_handover`'s global-ceiling entry — in one commit. No other wave writes this file. **`ControlIdleReleaseSec` gets no entry here at all** (C-70). |
| `pkg/config/config.go` | E2 only | **E2** lands the judge timeout / cap / token-ceiling keys **and** `Tools.Browser.ControlIdleReleaseSec` with its `EffectiveControlIdleReleaseSec()` accessor, in one commit. A configuration field is static declaration data with no behaviour; one owner, one commit. No other wave writes this file (C-70). |
| `pkg/agent/loop.go` | **E2 → B123 → E13** | **E2:** the tool-dispatch cap refusal inside `runTurn`'s `turnLoop`, and nothing else. **B123:** three regions — the release-hook invocation in `processMessage` (gated on `bus.InboundMessage.OperatorPrompt`), the `ControlIdleReleaseSec` → `BrowserConfig` translation inside `registerSharedTools`'s browser-config block (one added line beside the existing `IdleTTLSec` translation), and inside `runTurn` both the root-chat tool-context stamp beside the existing `WithTranscriptSessionID` call and the pre-dispatch control-gate short-circuit. **E13:** `runAgentLoop`'s body — the reordering and the ADR-081 D3 base predicate — and nothing else. **See the paragraph below this table; this is the highest-risk file in the delivery.** |
| `pkg/agent/turn.go` | **B123 → E13** | **B123:** the per-turn control-gate deferral ledger field on `turnState`, modelled on `pkg/agent/tool_denial.go::turnDenialLedger` (copy that pattern; do not edit that file). **E13:** the deferred-dispatch field on `turnResult`. Two different structs, adjacent declarations, one file — so it is a rebase, not a merge. It moves in lockstep with `loop.go` so E13 rebases both at one moment rather than twice. |
| `pkg/agent/routing_session_id_consumer_set_adr057_test.go` | **B123 only** | **B123** performs all four BROWSER-FR-022 amendments in one commit. **E2 and E13 must add zero new `routingSessionID` reads to `loop.go` or `turn.go`**; if E13 genuinely must, E13 makes the classification edit itself, in its own commit, and says so (C-72). |
| `pkg/agent/tool_result_admit.go` | E2 only | **E2** owns `admitToolResult`, carrying both the tool-result capture and the injection banner. Every later wave consumes its seams and must not edit it. |
| `pkg/agent/judge.go` | E3 → E9 → E10 | **E3:** deletion only — the inferred artifact check, its regexes and helpers, the prose-artifact classification branch and the rung-1.5 dispatch. **E9:** the parser, the rung dispatch, `summarizeVerdict`, `buildJudgeUserContent`, the timeout wired to E2's config key, and the `GoalID` / `GoalActiveSessionID` input split. **E10:** the tier-assembly call site. **No projection wave appears in this chain.** |
| `pkg/agent/verifier_adjudication.go` | E9 → E10 | **E9:** the per-criterion mapping loop, the dedupe, the descendant-scope resolution. **E10:** the provenance call site inside E9's mapping loop, and the transcript-window feed. E10's site is *inside* E9's region, so this is a serialisation point, not an addition. |
| `pkg/agent/goal_record_wiring.go` | E4 → E8 | **E4:** `WriteRecord` and `ReadGoalState` — the store seam re-point. **E8:** the three `emitGoalStatusFrame*` call sites, in the same commit as the signature change. |
| `pkg/agent/goal_loop.go` | E8 → E12 → E13 | **E8:** the terminal paths, the `clearGoal` call-site removals, and the three `emitGoalStatusFrame*` definitions. **E12:** the keeper gates. **E13:** claim resolution, the blocked park, the deferred-dispatch handoff and the deliverer swap — all inside `checkGoalLoopAfterTurn`'s switch, one wave, one agent. |
| `pkg/agent/goal_triggers.go` | E8 → E12 → E13 → E14 | **E8:** the terminal transition and `runGoalAdjudication`'s frame call. **E12:** the drivers, the six re-keyed maps, `handleBareGoalClaim`'s body, the routing helpers. **E13:** `settleGoalNormally`'s rewrite, the unified push ladder, the tool-result refusal, and the deletion of the two dead predicates. **E14:** the projection call after the Unavailable early return, and the swap to the criteria-carrying frame emission. |
| `pkg/agent/task_executor.go` | E12 → E14 | **E12:** the activation and `ClaimForRun` dispatch region. **E14:** `adjudicateClaim` — the projection call **and** the refusal/CAS reasons on the task run record, one edit. |
| `pkg/agent/plan_engine.go` | E6 → E14 | **E6:** the budget and cap region. **E14:** `applyJudgeRoundOutcome`, inside the existing `planDecisionMu` critical section, after the fresh running-state re-read. |
| `pkg/gateway/replay.go` | **F2 → B123** | **F2:** `toJudgeVerdictFrame` and the `judge_verdict` transcript branch. **B123:** a new `EntryTypeSystem` discrimination arm inside `streamReplay` that emits F1's browser frame, discriminating on the **stamped entry field**, never on the entry's prose content. Both regions sit inside one 626-line function; neither wave edits the other's (C-75). |
| `src/store/chat.ts` | **U2 → B8** | **U2** (inside F1's pull request): the goal region — the `GoalStatusFrame` state narrowing, `goalPills`, `mergeGoalPillFrame`, `GOAL_TERMINAL_STATES`, `buildGoalAckInsertion`, `case 'goal_status'`. **B8:** one line in `SESSION_SCOPED_FRAME_TYPES`, one new `ChatMessage` field, one new `buildBrowserHandoverInsertion` helper placed after `buildGoalAckInsertion`, and one new `case` arm placed after `case 'goal_status'` closes. Neither wave may rename `cancelStream` or `isStreaming` — B5 reads both (C-73). |
| `pkg/gateway/gateway.go` | **E11 only — no chain** | **E11** owns the whole file for the delivery. It writes one region: the `RegisterActiveCounter("goal", …)` closure, re-pointed onto `pkg/goal`'s records per R-22. The judge rubric self-check call site beside `seedSystemAgentEagerSouls` is **not written** (JUDGE-FR-077 – FR-080 retired by D-F), and the boot orphan sweep is **not written** (GOAL-FR-051 and wave S4 retired by D-F). This row used to be the delivery's most delicate two-wave, two-adjacent-region split; the operator decisions removed the second wave, so it is now a single owner and a single region. |
| `pkg/session/unified_meta_files.go`, `pkg/session/unified.go`, `pkg/session/daypartition.go`, and the four `pkg/session` goal-referencing test files (`goal_meta_greenfield_test.go`, `unified_meta_split_adr057_test.go`, `unified_listsessions_adr057_test.go`, `unified_stats_flush_adr057_test.go`) | S2 → S6 | **S2:** additive — the new session-owned pending-ask file, both copies of the relocated field, and nothing removed. **S6:** the deletion of the goal fields, after every reader is re-pointed. |
| `pkg/agent/goal_triggers_test.go` | E8 → E12 → E13 → T2 | Each engine wave updates the assertions its own behaviour change breaks, in the same commit. **T2** adds the new parity and union suppression tables last. |
| `pkg/agent/goal_loop_test.go` | E8 → E12 → E13 | Same rule: the wave that changes the behaviour updates the test in the same commit. |
| `pkg/agent/goal_keeper_repairs_test.go` | E12 → E13 | **E13** must rewrite its direct call to the predicate it deletes, in the same commit as the deletion. |
| `pkg/goal/**` | S1, then S3 and S5 in parallel | **S1:** the record, the store, criteria, verdict, status, the existence predicate, the lock. **S3:** `retention.go` only. **S5:** `routing.go` only. Disjoint by filename; S3 and S5 call S1's exported accessors and never reach into the store's internals. |
| `pkg/api/generated/**` | F1, except `contract_test.go` → T1 | **F1** regenerates the whole tree and hand-fixes `goal_dod_minitems_test.go`, which references the renamed field and will not compile otherwise. **T1** owns `contract_test.go` alone. |
| `Makefile`, `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh` | G1 only | After G1 each contains exactly one guard line. Every later guard is a **new file** discovered by `scripts/guards.sh`; no later wave edits any of the three. G1 requires exactly one CI-worker redeploy, and it is the last one this delivery needs. **G1 creates no new `pr.yml` job** — it replaces the nine guard steps inside the existing `tool-error-status-lint` job with one `bash scripts/guards.sh` step, so `ci-required`'s hand-maintained `needs:` array and branch protection are untouched (C-92). |
| `tests/e2e/shards.json` | **B8 only** | The only wave in the delivery that writes it. The new e2e spec and its shard assignment land in one commit (C-76). |
| `pkg/coreagent/core.go` | E1 → E11 | Restated because ADR-085's W6 claimed it: **it is a two-wave chain, not three.** B6 does not write this file. |

### `pkg/agent/loop.go` — the highest-risk file in the delivery

This one file deserves its own paragraph because more can go wrong here, less visibly, than anywhere
else in the delivery.

It is roughly **14,700 lines**, it is under constant churn, and three of the delivery's four largest
behavioural changes land inside it. Its `runTurn` function alone is about 3,600 lines and contains
**both** contested regions: the point where a verifier tool call is refused because a cap was reached
(E2), and the point where a browser tool call is deferred because a human holds the wheel (B123).
Those are not merely the same file — they are **the same tool-dispatch point inside the same
function.** A fourth region, `runAgentLoop`, is a genuinely separate function (E13), and a fifth,
`registerSharedTools`'s browser-config block, is separate again (B123).

Four rules, all binding:

1. **The order is E2 → B123 → E13, one wave per round, each rebasing onto the merged result of the
   one before.** A wave may not begin editing this file until the previous wave in the chain is
   merged and green on the base branch. `pkg/agent/turn.go` moves with it (B123 → E13) so B123 and
   E13 each rebase both files at one moment.
2. **The two refusals stay separate.** E2's cap counts verifier tool calls per adjudication and
   refuses at its ceiling; B123's counts control-gate deferrals per turn, refuses at three, and
   issues a stop instruction on the third. They share a code location and nothing else. B123's branch
   keys on the deferral marker on the tool result, never on `ForLLM`, and sits **beside** E2's branch,
   not inside it. Folding one counter into the other silently changes both bounds.
3. **Zero new `routingSessionID` reads from E2 or E13.** The ADR-057 consumer-set test classifies any
   read in this file, unconditionally, into the WS-stamping bucket and asserts an exact count. A read
   added here by a judge or goal wave reddens a browser test whose failure message talks about
   WebSocket payload stamping and names nothing about the change that caused it. B123's new reader
   lives in `pkg/agent/browser_deferral.go` and nowhere else.
4. **Exactly one non-test `PublishInbound` call site, and no `OperatorPrompt` assignment**, from any
   wave other than B123's lane 2. A structural census pins both.

And one rule of hygiene that this project has already been bitten by: **cite `file::symbol`, never
`file:line`, in every commit message, code comment and report touching this file.** Every line number
quoted for `loop.go` in the three source specifications should be assumed stale — CLAUDE.md records
that its own line citations were wrong by roughly 1,200 lines within weeks.

---

## 6. The standing rules for every implementing agent

These apply to every wave without exception.

1. **Contract-first (Constraint #8).** Every byte crossing the gateway/SPA boundary must be defined
   in `contracts/openapi.yaml` or `contracts/asyncapi.yaml` **before** any Go or TypeScript code.
   The generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only legal
   cross-boundary types. Hand-written wire-format types are forbidden and lint-caught. **In this
   delivery, only wave F1 may add or change a wire type — for all three designs. There is exactly
   one contracts commit and exactly one `scripts/gen-contracts.sh` run in the whole programme.** If
   your wave needs a wire type, stop and report it — do not add it yourself, and do not work around
   it with a hand-written struct or interface. A field that reaches the SPA by being serialized off
   a domain struct (the transcript entry is the live example) **is** a wire field and needs a schema,
   even though nothing fails today when it does not have one: the generated zod object is not
   `.strict()`, so an undeclared key is silently stripped at the SPA edge.

2. **The five-step "add a wire type" process, in this order, for F1 alone.**
   (a) Add the schema to `contracts/components/schemas/<TypeName>.yaml`.
   (b) Reference it from `contracts/openapi.yaml` and/or `contracts/asyncapi.yaml`.
   (c) Run `scripts/gen-contracts.sh`.
   (d) Commit the generated diff **alongside** the spec change, in one atomic commit — including
   `pkg/gateway/inboundschemas/`, which is inside the drift gate and which neither original wave
   plan mentioned.
   (e) Write the handler or consumer using the generated type only — never a parallel struct.
   A discriminated union is the one exception to step (a): its `oneOf` + `discriminator` wrapper
   must be hosted inline in `openapi.yaml`. No union appears in this change-set, so the exception is
   not triggered; if a revision introduces one, it binds.
   **A WebSocket frame is the other special case, and it runs the opposite way (C-69):** a frame's
   *generating* copy is the inline schema in `contracts/asyncapi.yaml`; the sibling file under
   `contracts/components/schemas/` exists only for step (a) and the `inboundschemas` sync, and the
   frame is **never** registered in `openapi.yaml` — doing so emits the same exported TypeScript name
   twice and breaks the build.

3. **Never run the full Go test suite locally. Run one scoped test at a time, in exactly this
   form:**

   ```
   CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -v -run '^TestName$' ./pkg/<pkg>/ > /tmp/t.log 2>&1; echo "exit=$?"; grep -c '^--- PASS' /tmp/t.log; grep -c '^--- FAIL' /tmp/t.log
   ```

   A test is verified only when **exit is 0**, **the `--- PASS` count equals the number of cases the
   test defines (at least one)**, and **the `--- FAIL` count is 0**. `exit=0` with zero `--- PASS`
   lines means the pattern matched nothing — that is a failure to run and must be reported as one,
   never as a pass. Count table-driven subtests by name with `grep -c '^    --- PASS'`. **Never put
   a pipe between the command and the exit-code capture** — `cmd | tail` reports tail's status, and
   that has produced a false green in this repository before.

3a. **`./pkg/...` is forbidden in every local Go test invocation.** The package argument is always
   one concrete directory. ADR-085's Test Matrix preamble uses the wildcard; that is an error in the
   spec, and copying it links the whole gateway test binary under the `goolm` tag — the specific
   thing that has out-of-memory-killed sessions on this hardware (C-94).

4. **Build tags `goolm,stdjson` are mandatory.** The Matrix channel is behind `//go:build goolm` and
   the gateway imports it, so without the tags `pkg/channels/matrix` will not compile and you will
   get `build constraints exclude all Go files` — which is a missing build tag, not a flake, an
   out-of-memory kill, or a real bug. Prefer `make test` / `make build`, which inject the tags. Never
   run two Go test suites in parallel.

5. **`gofmt -l . | wc -l` must be 0 before you finish.** Run it without a pipe when capturing the
   exit code. Generated Go is gofmt-ed by `scripts/gen-contracts.sh`; hand-written Go is yours.

6. **Specifications, ADRs and the demo page are READ-ONLY.** You may not edit
   `docs/internal/specs/judge-active-reviewer-spec.md`,
   `docs/internal/specs/browser-control-handover-spec.md`,
   `docs/internal/specs/goal-entity-spec.md`,
   `docs/internal/architecture/ADR-084-judge-as-an-active-reviewer.md`,
   `docs/internal/architecture/ADR-085-browser-control-handover.md`,
   `docs/internal/architecture/ADR-086-goal-as-a-first-class-entity.md`, or
   `docs/internal/design/task-form-criteria-dod-demo.html`. Where this document records that a spec
   sentence is stale, implement what this document says and report the discrepancy; do not "fix" the
   spec. The demo page is the visual contract for the task-form changes: anything unmarked in it
   stays exactly as it is. **The two exceptions, both inside a write-set:**
   `docs/internal/specs/browser-workspace-ownership-spec.md` and
   `docs/internal/specs/browser-agent-capability-spec.md` are B123's, because ADR-085's own rule 3
   requires them updated with the tool classification.

7. **Do not touch any path outside your wave's write-set. If you believe you must, STOP and report
   it.** Do not widen your own write-set, do not reach into a neighbouring wave's file "to finish a
   change", and do not create a file another wave is scheduled to create. A wave that grows its
   write-set breaks the disjointness this whole plan rests on, and the breakage will show up as
   somebody else's merge conflict, not yours.

8. **The retired surfaces in `CLAUDE.md` must not be reintroduced.** Most of them are protected by a
   guard script, and every branch in this delivery is cut before or alongside those guards, so a
   `git merge` can re-add a retired surface as an ordinary, conflict-free addition. **Resolve every
   such conflict by keeping the deletion.** The list that matters here: the Command Center screen
   and the Schedules UI; raw cron in any UI; the JPEG live-browser screencast fallback; the goal
   confirm-gate machinery (`confirmPendingGoal`, `IsGoalConfirm`, `proposeGoalAmendment`,
   `buildGoalPendingNote`, `useGoalCompilingIndicator` and the rest); the fail-closed per-agent
   tool-policy backfill; and the goal-ending-on-lost-UI watchdog. This delivery adds **eight** more
   retired surfaces of its own: `unable_to_verify` as a criterion outcome; the `outcome` field on
   `CriterionVerdict`; `pkg/agent/verdict_status_projection.go`; `clearGoal`-style goal-field
   erasure; and from ADR-085 — the browser **control toggle** ("Take control" / "Release control" /
   "Hand to agent" affordances), a **resume dispatcher**, a **frame-persistence path**, and any
   **turn-park or cancel signal on a browser take-over**. Two of the ADR-085 four are additionally
   relevant to a merge: the goal-ending-on-lost-UI watchdog and the JPEG screencast fallback both
   live in the same neighbourhoods this delivery edits. **Resolve every such conflict by keeping the
   deletion.**

9. **Two layers of tool policy, no third.** The reconciled global ceiling *is* the default;
   per-agent overrides only tighten. Do not add a hardcoded allow/deny/ask fallback, do not
   reintroduce a default-policy field, and do not reintroduce the fail-closed backfill. `goal_claim`
   gets exactly one ceiling entry and explicit per-agent seed entries, both landed by E1.

10. **A catalogue name and its implementation are not the same commit, and the order matters.** A
    per-agent tool-policy override naming a tool absent from `allStaticToolNames` **panics at boot**
    — not an error return, a panic. E1 lands the catalogue name and the seed maps together; E7 lands
    the implementation afterwards. Never the reverse.

11. **Never merge to `main` without human approval.** No `--admin`, no `--auto`, no branch-protection
    bypass, regardless of how green CI is. Every pull request must list a closing keyword **per
    issue** in the pull request description — `Closes #1, closes #2`, not `Closes #1, #2`.

12. **Commit as the human running the work.** Author and committer must be their own GitHub identity
    using their GitHub no-reply email. No agent co-author trailer, ever — the CLA gate hard-fails on
    it and fixing it needs a history rewrite.

13. **Read `docs/internal/false-green-patterns.md` before trusting or reporting any green.** The
    traps that bite this delivery specifically: a scoped run against a file that does not exist
    exits 0 with no output; a guard test can pass with the feature it guards deleted; a contract
    change can be green while the SPA silently drops every frame at the zod boundary; and machine
    load on the development pod causes timeouts, which produce a bare package `FAIL` with zero
    `--- FAIL` lines — inconclusive, not a finding. **Reproduce any failure yourself before acting
    on it.**

14. **Assert behaviour, not source text.** A test that greps a source file for a symbol is not a
    behavioural test and cannot survive a refactor. Where the requirement genuinely is "this symbol
    must not exist", it belongs in a guard script under `scripts/`, discovered by
    `scripts/guards.sh` — never in a Go or TypeScript test. Every guard needs a proof-of-failure
    companion that shows it can go red; no guard added by this delivery may be placed on the
    no-self-test exemption list.

15. **A wave must leave the tree green.** Every wave compiles, passes `gofmt`, passes its own scoped
    tests, and does not break another package's build. If your wave's honest shape would leave
    `pkg/agent` non-compiling for a later wave to fix, that is a signal the wave was mis-scoped —
    stop and report it. This is exactly why the session-storage work is split into an additive wave
    (S2) and a deletion wave (S6).

16. **Cite `file::symbol`, never `file:line`, for `pkg/agent/loop.go`, `turn.go`, `goal_loop.go` and
    `goal_triggers.go`.** These files churn constantly and every line number in all three source
    specifications for them should be assumed stale.

17. **Report honestly when something does not work. Do not narrow your scope to make a red go
    green.** If a requirement in your row cannot be built as written, if a test you must keep passing
    fails for a reason you did not cause, or if the honest shape of your wave would need a file
    outside your write-set — **say so, with the command you ran and its exit code, and stop.** Three
    closure paths are never acceptable: "pre-existing", "not mine", and "broken on main too"
    (Constraint #7). Silently dropping a requirement, deleting an inconvenient assertion, or
    reporting a partial result as a completed wave is worse than an admitted blocker, because the
    next wave in your chain builds on what you said you delivered.

18. **Two verification traps that this delivery will hit, and the cheapest check for each.**
    (a) Any new test file you add under `src/` must fall inside an existing group pattern in
    `.github/workflows/pr.yml`'s vitest matrix, which is an **allowlist** — a test matching no group
    is silently never run while the job still reports green. Every new SPA test path in this plan was
    checked against the patterns and every one is covered. **If `node scripts/check-vitest-coverage.mjs`
    fails, you have put a test in an unlisted directory: move the test, do not edit `pr.yml`.**
    (b) Any new Playwright spec must be assigned to exactly one shard in `tests/e2e/shards.json`, or
    the required `e2e-shard-check` job goes red on every subsequent pull request in the programme.
    Only B8 adds a spec, and `shards.json` is in its write-set.

19. **A browser take-over never ends a turn.** If your wave is anywhere near the browser, the turn
    engine, or the chat cancel path: taking the wheel must not invoke the chat cancel action, must
    not park the turn, must not write a turn-cancelled transcript entry, and must not require the
    operator to hand control back before the agent may continue. The agent is *deferred and told to
    wait*, then told to stop trying and do something else. Two guards (B9) exist to keep this true
    across a merge.

---


> **`make verify-contracts` CANNOT be a per-round gate, and here is the substitute (recorded 2026-09-11,
> after round 2).** Its final step is `git diff --exit-code -- contracts/ pkg/api/generated/
> src/lib/api/generated/ pkg/gateway/inboundschemas/`. This delivery does not commit per round, so that
> step compares legitimate uncommitted work against HEAD and MUST fail for the whole delivery. It is
> not drift and it is not a defect. **The per-round substitute, which is the same check without the
> commit requirement:** run `bash scripts/gen-contracts.sh`, then confirm it changed nothing —
> `git diff --stat -- pkg/api/generated/ src/lib/api/generated/` must be IDENTICAL before and after.
> Regeneration being a no-op is exactly what "no drift" means. Verified at the end of round 2:
> regeneration produced zero change while the tree was legitimately dirty. Run the real
> `make verify-contracts` once, at the end, after the delivery is committed.

## 7. Exit proof — the commands whose exit codes decide this is done

This is the list, in order. **A green that was not produced by one of these commands does not
count.** Each is marked **local-safe** (run it on the development machine) or **CI-only** (it links
or runs the whole Go test binary set, or builds the whole tree, and has out-of-memory-killed a
session on this hardware before).

Two rules apply to every command below, and they exist because both have produced a false green in
this repository: **capture the exit code without a pipe** (`cmd > log 2>&1; echo "exit=$?"` — a
`cmd | tail` reports `tail`'s status, not the command's), and **read the counts, not just the exit
code** (`ok` with zero `--- PASS` lines means nothing ran).

### Local-safe — run these here, in this order

| # | Command | Passes when | Notes |
|---|---|---|---|
| L1 | `gofmt -l . > /tmp/fmt.log 2>&1; echo "exit=$?"; wc -l < /tmp/fmt.log` | exit 0 **and** 0 lines | Cheap, run it constantly, and always before you finish a wave. |
| L2 | `bash scripts/guards.test.sh; echo "exit=$?"` | exit 0 | The mutation proof that the guard **runner** can go red. Run this before believing any guard verdict. It is meaningless to run L3 first. |
| L3 | `bash scripts/guards.sh > /tmp/guards.log 2>&1; echo "exit=$?"; cat /tmp/guards.log` | exit 0 **and** the per-guard `name exit=N` lines name exactly **19** guards — 10 on the baseline plus the 9 this delivery adds (G5's two, G2, G3, G6, G7's two, B9's two) — **and** the quarantine line names exactly `check-dead-code.sh`. **A count other than 19 is a discovery bug, not a pass.** The baseline 10 is `scripts/`'s twelve `check-*.sh` files minus the quarantined one minus `check-no-removed-providers-selfcheck.sh`, which is a companion and not a guard (C-93) | Reading the list matters as much as the exit code: a discovery bug that finds zero guards must fail, and `guards.sh` is written to fail on it, but you should still see the names. |
| L4 | `make tools > /tmp/tools.log 2>&1; echo "exit=$?"` then `make verify-contracts > /tmp/vc.log 2>&1; echo "exit=$?"` | both exit 0 | `make tools` first is not optional — `scripts/gen-contracts.sh` hard-fails on an `oapi-codegen` version mismatch. `verify-contracts` runs `npx tsc -b --noEmit` and then `git diff --exit-code` over `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/` and `pkg/gateway/inboundschemas/`. A failure here means committed generated files are stale, never that you should edit a generated file. |
| L5 | `npm run typecheck > /tmp/tsc.log 2>&1; echo "exit=$?"` | exit 0 | **Never** bare `tsc --noEmit`. `tsconfig.json` is a project-references root with no `include`, so without `-b` the command is a silent no-op that always exits 0. `npm run typecheck` is wired to `tsc -b --noEmit`. |
| L6 | `npx vitest run > /tmp/vitest.log 2>&1; echo "exit=$?"` then `node scripts/check-vitest-coverage.mjs; echo "exit=$?"` | both exit 0 | The second command is what proves the first actually ran the new files. A quarter of this suite once never ran while the pipeline reported green; the coverage check exists because of that. |
| L7 | Each Go test individually, one at a time, in the canonical scoped form given in §6 rule 3 | exit 0 **and** `--- PASS` equals the number of cases the test defines **and** `--- FAIL` is 0 | One test per invocation. Never two Go suites at once. A single scoped `pkg/gateway` test is affordable (roughly 86 MB, about a minute clean); the full package is not. **The package argument is one concrete directory — never `./pkg/...`.** |
| L8 | `scripts/e2e-shards.sh check; echo "exit=$?"` | exit 0 | Sub-second. Catches an unassigned Playwright spec locally instead of reddening a required, path-filter-free job on every subsequent pull request. Added because ADR-085 adds a spec and named no shard (C-76). |
| L9 | `bash scripts/check-browser-tests-gated.sh; echo "exit=$?"` | exit 0 | Grep-based, no Go build, sub-second. Catches a `pkg/tools/browser` test that can launch a real Chrome without `skipIfNoBrowser(t)`. ADR-085 adds five files into this gate's exact scope and never mentions it (C-78). |
| L10 | From round 5 onward: `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -v -run '^TestRoutingSessionID_ConsumerSetIsClosed$' ./pkg/agent/ > /tmp/u19.log 2>&1; echo "exit=$?"; grep -c -- '--- PASS' /tmp/u19.log` | exit 0 **and** 1 PASS | The ADR-057 closed-set tripwire (C-72). It is the single most likely way this delivery goes red for a reason nobody can attribute — its failure message talks about WebSocket payload stamping and names nothing about judge, goal or browser work. Cheap to run scoped, which is why it belongs here rather than in CI. Run it after **every** round from 5 onward, not only when you touched `pkg/agent`. |
| L11 | `CGO_ENABLED=0 go vet -tags goolm,stdjson ./pkg/session/ ./pkg/agent/ ./pkg/tools/ ./pkg/config/ > /tmp/vet.log 2>&1; echo "exit=$?"` | exit 0 | **`go vet` compiles test files; `go build` does not.** That one difference makes this the **only** local gate that catches a test file referencing a symbol the delivery deleted — a build stays green while the test tree is already broken, and the failure then surfaces in CI's `go-test`, a round or more later, attributed to whoever pushed next. **Run it at the END OF EVERY ROUND, starting with the first round that touches `pkg/session` or `pkg/agent` — in this schedule that is round 2 (S1, S2) and every round after it — not only at the end of the delivery.** Four concrete package paths, never `./...`: this delivery deletes symbols from `pkg/session`'s goal fields (S6), `pkg/agent`'s goal and judge seams (E4, E8, E11, E12, E14), `pkg/tools` (B123, B6) and `pkg/config` (E2, E6), and every one of those has test files elsewhere in the same package that name them. It compiles rather than links, so it is affordable here — but it is still a compile: run it alone, never beside another Go command. |

**On L11, because it is the one gate this plan previously left out.** `go build` does **not** compile
test files; `go vet` **does**. A delivery that deletes fields — and this one deletes
`session.UnifiedMeta`'s whole `Goal*` set, `EffectiveGoalMaxRoundsWithOverride` before it is ever
written, `clearGoal`'s zeroing patch and the judge self-check — will leave a `_test.go` somewhere in
the same package still naming one of them. `go build` is green in that state. `gofmt` is green.
`golangci-lint` may well be green. The first red is CI's full `go-test`, which is expensive, slow
and lands on whoever pushed next. **L11 is therefore a per-round gate, not an exit-only one:** run it
at the end of every round from the first round that touches `pkg/session` or `pkg/agent` (round 2
here) through round 8. Running it only once, at the end of the delivery, would find the same defects
but attribute none of them.

### CI-only — these decide the rest, and only CI's answer counts

| # | Command | Passes when | Notes |
|---|---|---|---|
| C1 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> lint"` | exit 0 | **The one worker redeploy is part of G1's definition of done, not a follow-up: G1 is not complete until this command exits 0 on G1's own ref AND its output shows the per-guard `name exit=N` lines from `scripts/guards.sh`. No round after 1 may treat a worker `lint` green as evidence until that is confirmed once.** After G1, `scripts/guards.sh` lives under `scripts/` and the worker picks up every later guard from its own checkout with no further redeploy — which is exactly why the one redeploy has to happen, and be verified, at G1. **Read `deploy/ci-worker/CLAUDE.md` before trusting any verdict from this worker** — it documents the two false signals that have both bitten this project: a stale checkout producing a false red, and a wrapper exit code producing a false green. G1 changes `runci.sh`, so exactly one worker redeploy is required before this gate is trustworthy, and it is the only redeploy this delivery needs. |
| C2 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> go-build"` | exit 0 | |
| C3 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> go-vet"` | exit 0 | |
| C4 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> go-test"` | exit 0 | The full Go suite. **Never run this locally** — linking the gateway test binary with the pure-Go crypto pulled in by the `goolm` tag is the specific thing that has killed sessions on this hardware. |
| C5 | The `-race` gate, over the list `scripts/race-packages.sh` prints | exit 0 | After G4 this list includes `./pkg/goal/...`. Every race assertion in this delivery is trustworthy only from here. |
| C6 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> contracts"` | exit 0 | |
| C7 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> spa"` | exit 0 | |
| C8 | `golangci-lint run --build-tags=goolm,stdjson` on the worker | exit 0 | Note that `golangci-lint` silently caps findings at three per message, so a small local count is not a real count. |
| C9 | `govulncheck ./...` on the worker | 0 vulnerabilities | Whole-tree analysis; do not run it here. |
| C10 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> embed-build"` | exit 0 | The single-binary build with the SPA embedded. |
| C11 | `fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> e2e"` | exit 0 | Playwright, now including `tests/e2e/browser-control-handover.spec.ts`. Never run locally. |
| C12 | `gh pr checks <n> --watch` | the single required context **CI** (job `ci-required`) is green | **This is the only authoritative green for the delivery.** That job re-derives its verdict from every other gate and fails if the upstream `changes` job did not itself succeed, so it cannot be satisfied by a skipped matrix leg. A pull request is not done until this is green **and** a human has approved it. |

### Assertions that only CI can decide — never report these from this machine

These are not merely expensive here; a local run of any of them cannot distinguish a pass from
"nothing ran", so quoting one is a false green regardless of what it printed.

1. **Every concurrency assertion**, because it only decides under `go test -race`: the goal store's
   lock order, the verifier registry's compare-and-swap, the browser control-lock lifecycle, the
   thirty-second reaper ticker, and ADR-085's three-idle-window hold. Positive finding worth stating:
   `scripts/race-packages.sh` already lists `./pkg/tools/...`, `./pkg/agent/...`, `./pkg/gateway/...`
   and `./pkg/task/...`, so **ADR-085 needs no race-surface change and no wave should edit that file
   for it** — G4 adds `./pkg/goal/...` and nothing else. `pkg/bus` and `pkg/audit` are **not** on the
   race surface; if either gains concurrent state, say so rather than assuming it is raced.
2. **Every `pkg/gateway` test binary case**, including all of `pkg/gateway/browser_control_handover_test.go`,
   because linking that binary under the `goolm` tag is the specific out-of-memory risk.
3. **All Playwright specs**, including `tests/e2e/browser-control-handover.spec.ts`.
4. **`govulncheck ./...`**.
5. **`golangci-lint` at real counts** — locally it silently caps at three findings per message, so
   either pass `--max-issues-per-linter=0 --max-same-issues=0` or do not quote a number.

### Never run on this machine

Every entry here either links the full Go test binary set or builds the whole tree: `make test`
(the Makefile's `test` target *is* `go test ./...`); any unscoped `go test` over `./...`; any
whole-package run of `./pkg/gateway/` or `./pkg/agent/`; the `-race` gate; `govulncheck ./...`;
`npx playwright test`; `bash scripts/check-dead-code.sh` (a whole-program call graph, which is why
G1 quarantines it from the guard runner); and `make build` / the SPA embed pipeline. Two Go test
suites in parallel is forbidden regardless of scope. Do not use a `MemoryMax` cgroup cap with swap
enabled — it produces unkillable zombies instead of a clean failure.

### Results that are inconclusive, not findings

A package-level `FAIL` with **zero** `--- FAIL` lines is a hang or a global fault, usually machine
contention, not a failing test. An `ok` with **zero** `--- PASS` lines is a pattern that matched
nothing. Neither is a result. **Reproduce any reported failure yourself before acting on it** —
in one previous session three of five "must fix" items did not exist.

---

## 7b. Defects found during delivery, and how they were closed

| # | Found by | Defect | Fix | Proof |
|---|---|---|---|---|
| **DD-1** | Round 1 exit proof (me) | F1's contract change broke three hand-written conversion functions in `pkg/gateway`; the plan left two of them to round 3, so the tree would have stayed red across two rounds and round-2 agents could not have told their own breakage from inherited. | Pulled E5's `rest_tasks.go` / `rest_plans.go` compile re-point forward: ten `clause_count` insertions, two verdict structs widened. `replay.go` left to its owner F2, who closed it in round 2. The four new evidence fields were left ABSENT on the wire, not empty — the Go type does not carry them until F2, and writing `""` would have been a value the SPA renders. | `go vet ./pkg/gateway/` reported zero errors in the two files; `go build ./...` exit 0 after F2 landed. |
| **DD-2** | Round 2 exit proof (me) | `make verify-contracts` can never pass mid-delivery — its last step diffs the tree against HEAD, and this delivery does not commit per round. Left as-is it would read as a failing gate for six rounds and teach everyone to ignore it. | Substitute gate written into §7: regenerate, then confirm regeneration changed nothing. That is the same drift check without needing a commit. | Verified at round 2: regeneration produced zero change on a legitimately dirty tree. |
| **DD-3** | Wave F2 | JUDGE-FR-006b's second clause — rejecting an edit that LOWERS a criterion's persisted clause count while a verdict exists — was in no wave's Delivers column. F2 built the half it owned and refused to reach into another wave's files for the rest. | Assigned to E12, which owns `pkg/tools/set_goal.go`, the file where the check belongs. | Recorded in E12's row with its named oracle. |
| **DD-4** | Wave E4 | **Every `set_goal` call that omits an explicit definition of done would have failed.** `pkg/goal`'s `validateCriteriaList` rejected all three GOAL-FR-007 reserved id forms identically, but they are reserved for two different reasons: `soft-tier-implicit` and `goal-condition` are ephemeral and never persisted, while the `goal-dod-floor-*` prefix is FIXED-IDENTITY and deliberately persisted — both constructors that build the floor DoD say in terms that their ids must be stable sentinels so the records are "indistinguishable on disk". The blanket ban broke the backfill path. | The prefix is legal in the `dod` list, where its own name points, and illegal in the `criteria` list, where it would be a namespace collision. The two ephemeral ids stay banned in both. Rejected alternatives: minting UUIDs for floor items (breaks the byte-stability both constructors require, and duplicates the items on every reload) and dropping the check (it is the only thing enforcing the namespace, and FR-041's verdict de-union depends on it). | `TestFloorDoDIDsPersistInDoDButNotCriteria` in `pkg/goal/criteria_test.go`, 7 subtests, exit 0. Mutation-checked: with the carve-out removed the test fails with exit 1 and 2 failing subtests, then restored byte-identical and green. |

### DD-5 — the admission gate E8 refused to delete (recorded 2026-09-11, action required by E12)

**What the plan said.** R-22 instructed wave E8 to delete both `pe.Admit("goal")` and
`pe.Release("goal")` from `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/pkg/agent/goal_loop.go`.

**What E8 did, and why it was right.** It deleted `Release("goal")` only. Verified independently:
`PlanEngine.Release`'s entire body is a debug log whose own message reads *"admission release
(advisory no-op; the cap is recomputed live from persisted state on the next Admit)"* — deleting it
changes nothing. But `Admit("goal")` is still LIVE at two chat-goal activation paths in
`goal_loop.go`, and `TestGoalCommand_AdmissionRefusal` (`pkg/agent/goal_loop_test.go`) currently
passes by asserting it refuses once the cap is reached. Deleting it in round 4 would have removed
every cap guard on chat goals for at least one round, with no replacement landed — directly against
GOAL-FR-049's "chat goals MUST remain bounded by it unchanged".

**Therefore, binding on wave E12 (round 5).** `pe.Admit("goal")` is still present and still load-bearing.
E12 owns the new activation path. When E12 lands it, E12 — not E8 — decides and implements the
transition, and must do exactly one of:
  (a) keep `Admit("goal")` as the cap gate and wire the new activation path through it, or
  (b) replace it with the `pkg/goal`-backed counter E11 registered, IN THE SAME COMMIT that lands the
      replacement, leaving no round in which chat goals are uncapped.
E12 MUST NOT delete `Admit("goal")` without a working replacement in the same commit.
`TestGoalCommand_AdmissionRefusal` must still pass afterwards, by whichever mechanism.

**Two smaller findings from E8, recorded so nobody re-derives them.** (1) The goal spec says
`clearGoal` has eight call sites; a grep of the current tree finds seven. Stale by one, not a blocker.
(2) C-16 framed the goal-status frame change as a Go signature change spanning three files. E8 read
all three and found no signature change is needed — `state` and `reason` are already plain strings, so
the four new states pass through with zero signature change. E8 therefore left
`pkg/agent/goal_record_wiring.go` untouched despite owning it. If a later wave discovers a real
signature change IS needed, C-16 grants no other wave the right to make it: that wave must STOP and
report, not work around it.

### DD-6 — BLOCKER, production: the narrowed tool surface never lifts after a `set_goal` call. **E13 MUST fix this in round 6.**

**Found by** wave E12. **Reproduced and confirmed independently 2026-09-11.**

**The failure a user would see.** Set a goal in chat. The agent's tool surface is deliberately
narrowed while the goal has no criteria yet (ADR-081 D3). The agent then registers its criteria with
`set_goal` — and the narrowing NEVER LIFTS. The agent spends the rest of the goal with a reduced set
of tools and no indication why. This is not a test artefact; it is the primary work-first flow.

**The causal chain, verified against the code.**
1. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/pkg/agent/loop.go` line ~8465 is the
   D3 narrowing predicate. It reads ONLY session meta: `m.GoalCondition` and `m.GoalCriteriaJSON`.
   Narrowing is ON while a condition is set and the criteria field is still empty.
2. `pkg/agent/goal_record_wiring.go::WriteRecord` — the function a `set_goal` tool call goes through —
   was re-pointed by wave E4 onto `pkg/goal.Store.Update` and writes NO session meta at all
   (verified: its body contains no `SetMeta` and no `MetaPatch`).
3. The four remaining writers of `GoalCriteriaJSON` are `goal_loop.go::applyGoalCommandPrompt`,
   `::activateInstantGoal`, `::applyGoalMarkerRestate` and `task_executor.go::activateTaskGoal` —
   every one of them a `/goal` command or task path, NONE of them the tool path.
4. So activation stamps the field empty (correct — narrowing on), `set_goal` writes the criteria to
   the store, session meta is never updated, and the predicate reads empty forever.

**The fix, decided — E13 owns `loop.go` in round 6 and MUST land this there.** Re-point the predicate
at the goal store: narrowing stays on only while the session's ACTIVE `pkg/goal` record has an empty
criteria list, and lifts as soon as that record carries one. Read it through the same accessor E4
established, not by reaching into session meta.

**Rejected alternative, do NOT implement it:** making `WriteRecord` mirror the criteria back onto
session meta. That reinstates precisely the dual-write ADR-086 exists to delete — it would trade a
visible defect for the invisible one this whole design is here to remove.

**Proof required from E13.** The five currently-failing tests E12 named must go green:
`TestGoalTurn_EndToEnd_NarrowedDoorAndRestoration` and
`TestGoalTurn_NarrowedRequest_SuppressesNativeSearch` (`pkg/agent/goal_first_move_test.go`), and
`TestGoalClarify_WebCardRoundtrip`, `TestGoalFlow_EndToEnd_Web`, `TestGoalFlow_EndToEnd_Channel`
(`pkg/agent/goal_flow_integration_test.go`). At least one must be mutation-checked: break the
re-pointed predicate, confirm the test dies, restore.

### DD-7 — three loose ends from wave E12, each with a named owner

**(a) GOAL-FR-022 / FR-023 are NOT delivered, and the plan contradicts itself about who owns them.**
C-32 and E12's Must-NOT-touch column assign `pkg/agent/task_executor.go::adjudicateClaim` wholesale to
E14, while E12's Delivers column and R-27 name E12 as owning the trust-the-claim branch deletion —
which lives inside that same function. E12 followed the more specific instruction and did not touch it.
**Resolved: E14 owns it (round 7).** E14's Delivers gains GOAL-FR-022 and FR-023, and E14 must delete
both trust-the-claim branches inside `adjudicateClaim`.

**(b) GOAL-FR-052's map re-keying is 2 of 6 done, and the rest is not one wave's to finish.** E12
re-keyed `bareClaimStreak` and `waitingOnUser` — the two maps whose writers sit entirely inside its
write-set — and left `routing`, `idleSettling`, `diffBoundaryHash` and `outputWatermarks` keyed by
session id, with the reasoning recorded in `goalTriggerState`'s doc comment. It was right to stop:
`idleSettling`'s other writer is E13's not-yet-landed rewrite, and `diffBoundaryHash` is written from
E9's file. **Resolved: E13 (round 6) re-keys `idleSettling` and `outputWatermarks` when it rewrites
`settleGoalNormally`. `diffBoundaryHash` and `routing` stay session-keyed for this delivery, and that
is recorded as a deliberate, documented partial — not a silent one.** A map split across two key
spaces is the failure mode to avoid; a map consistently keyed the old way is merely unfinished.

**(c) E12 edited one file outside its write-set and said so.** `pkg/agent/goal_record_wiring_test.go`
(E4's) was a hard compile blocker after `recordGoalRouting` gained a required goal-id parameter. E12
made the minimal mechanical fix — capturing an existing helper's return value — rather than leave
`pkg/agent` non-compiling for every downstream wave. **Accepted.** Leaving the package uncompilable
would have blocked five other waves; the deviation was mechanical, minimal, and reported rather than
hidden, which is exactly the behaviour the write-set rule is meant to produce when it has to bend.

### Wave R7C — stale-test cleanup (NEW, round 7). Owner: one agent. Write-set is four test files, nothing else.

**Why this wave exists.** Two architectural changes landed correctly and left assertions behind that
test the world as it was before them. The production code is RIGHT; these tests are STALE. Nobody
owns them because their waves (E8, E12) are complete. Left alone they fail forever and make a correct
delivery look broken.

**Write-set — these four files and NOTHING else:**
`pkg/agent/goal_first_move_test.go`, `pkg/agent/goal_flow_integration_test.go`,
`pkg/agent/conformance_design_test.go`, `pkg/agent/goal_terminal_transition_test.go`.

**Group 1 — assertions on a data path ADR-086 deliberately retired.** These check that a `set_goal`
tool call wrote to SESSION META. It no longer does, by design: `goal_record_wiring.go::WriteRecord`
writes to `pkg/goal.Store` only, and DD-6 explicitly forbids reinstating the dual write.
Confirmed failing with these exact messages:
  - `goal_first_move_test.go:535` — "set_goal's write must have landed on the session meta"
  - `goal_flow_integration_test.go:320` — "the resumed turn's set_goal call must have registered the record"
  - `goal_flow_integration_test.go:418` — "the auto-submit resume's set_goal call must have registered the record"
  - `goal_flow_integration_test.go:649` — "turn 1's set_goal call must have registered the record"
**Fix:** re-point each assertion onto the `pkg/goal` store — read the session's active goal record and
assert ITS criteria list is populated. **You MUST NOT make `WriteRecord` write session meta. That is
forbidden by DD-6 and it is the whole point of ADR-086.** If an assertion cannot be re-pointed, delete
it and say which, rather than weakening it to something that passes either way.

**Group 2 — assertions that predate deferred adjudication (JUDGE-FR-098, landed by E13).**
`checkGoalLoopAfterTurn` no longer adjudicates inline; it records the work and returns. Tests that
call it and then immediately assert a terminal state now see `active`.
  - `goal_terminal_transition_test.go` — three tests, confirmed failing with `record state = "active", want "met"/"exhausted"`.
**Fix:** call `al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)` before asserting,
the same pattern E13 applied throughout its own write-set. Do not change what is asserted — only when.

**Group 3 — a latent key bug, pre-existing and unrelated to either change.**
`conformance_design_test.go` (~line 648) looks up `goalIsWaitingOnUser` by SESSION id; that map has
been GOAL-id keyed since E12's FR-052 rework. The assertion is vacuous — it fails for the wrong
reason and would have passed for the wrong reason too. E13 found and fixed the identical bug twice in
its own files. **Fix:** key by goal id. The same file's later `checkGoalLoopAfterTurn` call (~line 663)
additionally needs the Group 2 fix once execution reaches it.
`goal_flow_integration_test.go` (~line 727) asserts one Judge call per idle cycle — the claimless
idle-adjudication path JUDGE-FR-095 now forbids outright. **Fix:** rewrite to the push-ladder shape
E13 used in `goal_keeper_repairs_test.go`.

**Definition of done.** All four files compile, every test in them passes, and you have run each one
by exact name and recorded its exit code. **Mutation-check at least one Group 1 fix**: break the
production write, confirm the re-pointed assertion dies, restore. A test that passes whether or not
`set_goal` persisted anything is worse than the stale one it replaced.
**Report honestly** if any assertion turns out to be testing something that no longer exists at all —
deleting a test for a deleted behaviour is correct; quietly weakening one is not.

### DD-8 — round 7 broke the build, and my diagnosis of it was wrong too (2026-09-11, closed)

**The planning failure.** This plan's own rule says wave S6 deletes the session-meta `Goal*` fields
only AFTER every reader is re-pointed. I scheduled S6 into round 7 believing rounds 3-6 had done that.
They had not, and they were not wrong to: every wave was told to touch only its own named region, and
every wave obeyed. **Nobody was assigned "everything else."** That is the same unowned-work gap the
pre-delivery grill caught elsewhere, and I missed this instance when I set the round order.

**My diagnosis was also wrong, in an instructive way.** I measured `go build` and reported 212 errors
across five PRODUCTION files. By the time the repair agent started, round 7's other waves had finished
and those production files were already re-pointed — `go build` exited 0. The real, durable breakage
was **343 compile errors across 16 TEST files**, which `go build` does not compile and therefore does
not see. I had written the go-build-vs-go-vet warning into the plan myself and then fell into it while
diagnosing. The repair agent said so plainly instead of working to my stale brief, which is the
behaviour that saved the round.

**The repair.** One agent (Opus, sole owner — concurrency is what caused this) re-pointed 16 test
files from the deleted session-meta fields onto `pkg/goal`. 348 errors to 5. It made four judgment
calls on tests whose premise ADR-086 retired, each documented in the test's own doc comment and each
flagged for the ADR owner rather than silently applied: two renamed with their surviving contract
preserved, one assertion replaced because the original asserted the exact OPPOSITE of GOAL-FR-027
(that a clear ERASES the record, when the ADR says a clear is a status transition on a RETAINED one),
and one selector pin moved from `session.ListSessions` to `goal.Store.ListActive`.
Its mutation check: `rec.Round + 1` -> `+ 2`, `TestGoalLoop_UnmetVerdict_AdvancesRoundAndFeedsForward`
died with "rounds_used = 2, want 1", restored byte-identical.

**The last 5, fixed by hand.** `pkg/session/pending_ask_test.go` asserted on goal fields S6 deleted.
Both affected tests were made STRONGER, not weaker: the isolation test now uses a surviving field
group (`LoopMode`) as its unrelated write and additionally asserts that write landed, so it cannot
pass vacuously; and `TestNoMigrationPathExists` now asserts the stale `goal.json` is **still on disk
and simply never read** — previously it only checked one key was refused while the rest of the file
was still being parsed. Under D-F that is a stronger guarantee.

**Mutation evidence, including a failed first attempt, recorded because it matters.** My first probe
(forcing the composed `PendingAskJSON` to empty) did NOT kill the tests — exit 0. Two reasons, both my
error: the isolation test reads through a warm cache and never exercises that compose, and
`TestNoMigrationPathExists` asserts the value IS empty, so forcing it empty cannot fail it. Second
probe (dropping the value in `u5WritePendingAskLocked`) killed two tests with exit 1, restored
byte-identical, green again. **Honest limit: `TestNoMigrationPathExists` asserts a negative and I
could not construct a probe that kills it without adding the legacy reader D-F forbids. That one is
reasoned, not proven.**

**Exit proof after the repair, all observed:** `gofmt` 0 files; `go build ./...` exit 0;
`go vet` over 11 packages exit 0 with 0 errors (the gate that compiles test files);
`npm run typecheck` exit 0; contract regeneration a no-op.

**The rule this leaves behind.** A wave that DELETES a shared symbol must own, or explicitly name an
owner for, every reader of it — production and test. `go build` is not sufficient evidence that a
deletion is safe; only `go vet` or a test compile is.

## 8. The operator's answers, and what is still open

**The six questions that were put to the operator are ANSWERED.** He answered them on 2026-09-11.
The answers are below, in full, with what each one changes in this plan. They are binding: rule 0 of
the precedence list in §1. No agent may reopen one, and no specification sentence survives that
contradicts one.

Below the answers, the analyst questions **OQ-1 … OQ-31** are kept as they were. Those are a
different population — narrow, technical, and mostly about naming and ownership rather than about
what a person sees. Several are now settled by the answers and are marked so where they are.

---

### The six answers

**D-A — the quiet goal: it sits for seven days, and the fix goes in the prompt.**
*(Was OQ-A. Affects E8, E13, T2.)* The seven-day idle-expiry sweep
(`config.DefaultIdleExpiryDays = 7`) **stays** as the sole terminator for a goal whose agent works
and never claims. The nudge ladder does **not** get an ending of its own — do not bound it, do not
add a counter, do not add a stop reason. The operator's reasoning: bounding the nudges treats the
symptom. The cause is that the agent does not know it is supposed to claim. So the fix is in the
words: **the keeper's re-post text and the nudge text must each tell the agent that if it believes
the work is done it must mark it complete by calling the claim tool.**
*Consequences.* Both prompt builders live in one file and belong to **E13**:
`pkg/agent/goal_triggers.go::goalContinuePushPrompt` (the FR-014b keeper re-post) and
`pkg/agent/goal_triggers.go::goalNudgePrompt` (the ADR-081 D6c recordless nudge). Each must name the
claim tool (`goal_claim`). **E8** still owns `pkg/agent/goal_loop.go::goalIdleExpirySweep` by name
and still re-points its selector off `ListSessions()` + `GoalCondition` onto `pkg/goal`'s "an active
goal record exists for this owner" predicate, reading idle from the goal record's own
`last_activity_at` for both owner kinds (R-06). **T2** asserts both halves: the prompts name the
tool, and the ladder has no terminator of its own. R-06's "bound the nudges" option is **rejected**.

**D-B — weak proof: the Judge has the authority, and a failed quote does not overturn it.**
*(Was OQ-B. Affects E9, E10, E11, G3.)* The Judge judges as a human would, on common sense. A quote
that is missing, empty or does not verify **does not** flip the verdict to `unmet`. The criterion is
marked **met**; the Judge **must** report the missing or failed evidence; and the Judge **must**
justify why common sense says the work is done anyway.
*Consequences.* This **retires the judge specification's §D — "A `met` with no quote is rejected in
code (D2b)"** — as a rewrite: **JUDGE-FR-024, FR-025 and FR-026** are cancelled in that role.
`parseJudgeResponse` keeps the **detection** and reports it; it changes no outcome. **JUDGE-FR-027**
survives as pure reporting — the count and the WARN log line stay. The per-criterion `Reason` becomes
the place where both obligations land: what could not be verified, and why the work is nonetheless
done. **All grounding controls — evidence tiers, provenance, quote checks, length checks — are
REPORTING obligations and never gates.** This is not a new position: **GOAL-FR-038** already says a
criterion with no machine-checkable form is the normal case and reaches `met` on the Judge's
reasoning alone, and **GOAL-FR-039** already says the ADR-084 D2b–D2d grounding controls "are
anti-hallucination controls and MUST NOT be implemented as a proof gate". The judge spec's §D was the
one document that disagreed with them, and it loses.

**D-C — the edit gate: criteria and definition of done are mandatory at creation and at edit.**
*(Was OQ-F. Affects E5, U4, U5.)* Uniform. No exemption for an old task, no exemption for a rename.
*Consequences.* **E5** fires FR-047's HTTP 400 on the update path as well as the create path, on the
task and plan-member routes, and adds the same rejection to **both** `create_task` **and**
`update_task` in `pkg/tools/task.go` and `pkg/sysagent/tools/task.go` (R-25). **U4** owns the create
half of the form. **U5** owns the edit half: the task detail panel blocks the autosave with a stated
reason rather than disabling a Save button that does not exist, **and it owns
`src/components/calendar/CalendarEventSlideOver.tsx` and its test**, which get the same two editors
on both their create and edit paths (R-26). The adjudicator's recommendation of creation-only is
**overruled**.

**D-D and D-E — one global budget setting, no per-goal override anywhere.**
*(Was OQ-E. Affects F1, U5, U6, E6, S1.)* There is **one** goal-tries setting, under **Settings →
Performance**, and it governs task goals and chat goals **identically**. Goals behave the same for
tasks and for chat; any budget surface that exists for one must exist for the other, and the honest
way to satisfy that is to have exactly one, in one place. **A per-goal override does not exist.**
*Consequences.* **GOAL-FR-046 is RETIRED.** **GOAL-US-8 ("an operator can give a goal more room") is
RETIRED.** **GOAL-FR-045 stands** — the setting lives under Settings → Performance. **OQ-2 is
ANSWERED: `Goal.yaml` stays TYPE-ONLY with no REST path** — no `GET /api/v1/goals/{id}`, no
`PATCH /api/v1/goals/{id}`, no `GoalUpdateRequest.yaml`, and no writable budget field on any request
schema. **None of these enter F1's write-set**, and F1's *Delivers* column drops GOAL-FR-046's wire
half. **U5 loses `GoalBudgetField.tsx` and `GoalBudgetField.test.tsx` from its write-set** — they are
never created, by anyone. **U6 shrinks to the one global control**, and its label must say plainly
that it applies to every goal. The chat-goal asymmetry that OQ-E worried about is gone: neither kind
of goal has a per-goal control, so neither is worse off than the other.

**D-F — no upgrade path, in any of the three designs. Greenfield everywhere.**
*(Was OQ-C, and it reaches much further than OQ-C asked. Affects S2, S6, E11, U3, F1, E1.)* Build no
upgrade, no migration, no detection and no rescue, in ADR-084, ADR-085 or ADR-086.
*Consequences, by name.* **GOAL-FR-051** (boot detection of orphaned goals) is RETIRED.
**Wave S4 is RETIRED in full**, and **`pkg/gateway/goal_orphan_boot.go` is NOT created** — it is
removed from every write-set in this document. **GOAL-US-12** ("an in-flight goal at upgrade ends
visibly") is RETIRED. **The `PendingAskJSON` rescue is RETIRED** — S2's relocation of the field
stands, because that is a code move in a fresh tree, but nothing reads a pre-existing install and no
warning is emitted per affected session. **JUDGE-US-8** is RETIRED. **The judge specification's §M,
"§I — the mixed state" — JUDGE-FR-077, FR-078, FR-079 and FR-080 — is RETIRED**, and with it the
rubric capability self-check (`pkg/gateway/judge_rubric_selfcheck.go`, never created), the
`rubric_capability` field on `Agent.yaml`, R-30's two-boolean shape, and **wave U3** (the degraded
badge) in full. **JUDGE-FR-085's rollout choice** is RETIRED; FR-085's frame shape is unchanged, so
nothing in F1's write-set moves on its account. **GOAL-FR-050 — "build no migration" — now stands
unqualified**, with no read-only carve-out, because there is no detector left to feed:
`TestNoMigrationPathExists` may be written at its strongest, asserting that no legacy key is read at
all. **Blocker R-07 DISSOLVES** for exactly that reason; its row is kept and marked dissolved rather
than deleted, so that anyone who read the earlier version can see it was cancelled deliberately.
*One thing survived the retirement and was re-homed:* wave S4 also carried **GOAL-FR-049's
active-goal admission counter**, which is not an upgrade scenario. It moves to **E11**, which becomes
`pkg/gateway/gateway.go`'s only writer in the delivery. See **NQ-1** below.

**D-G — the held wheel is per tab, and the agent opens a new tab rather than waiting.**
*(Was OQ-D. Affects B123, B5, B6.)* The person keeps the wheel until the agent receives a **new
prompt** to take it back. There is still no hand-back button — unchanged from ADR-085. **New
requirement:** if the agent's other work needs a browser while the wheel is held, **the agent opens a
new tab and carries on.** It never waits. **The stand-down is therefore per tab, not per browser
session.**
*Consequences.* This is exactly what ADR-085 already asked for in its own words —
*"[continue other work] yes it should continue other work"* — made concrete: continuing is the
required behaviour, and a new tab is how it continues. **B123 must first establish what the control
lock actually is today, before writing anything.** Grep `pkg/tools/browser`: the latch is
`live.go`'s `lv.controller`, a single string on a live view resolved through `r.view(sessionID)`,
which reads as **per view — per browser session, not per tab**. **If the lock is not per-tab today,
B123 must STOP and REPORT it, not silently change the granularity of a control lock.** **B5** shows
the hold per tab in the tab strip, and is blocked on the operator's answer if B123 reports the lock
is per-session. **B6** makes `browser_handover`'s refusal offer the new-tab route rather than a wait.

---

### New questions raised by these answers

Three. **Two are now CLOSED by the operator — NQ-1 CONFIRMED (D-J) and NQ-2 CONFIRMED (D-K), both
on 2026-09-11. NQ-3 remains OPEN and is awaiting the operator.** Each names the wave it blocks.

**NQ-1 — wave S4 also carried GOAL-FR-049, which is not an upgrade scenario. Where should it live?**
*Blocks E11, round 4.* D-F retires S4 because of its boot-orphan-detector half. Its other half — the
`RegisterActiveCounter("goal", …)` closure that tells the gateway how many goals are active — has
nothing to do with upgrading. **This plan re-homes it to E11**, which already had a `gateway.go` call
site (now deleted with the rubric self-check) and is therefore the file's natural sole owner. The
pairing is odd on the surface — a rubric constant and an admission counter in one wave — but it is
one file, one round, and it removes the delivery's tightest two-wave file split.

> **ANSWERED — CONFIRMED (D-J, 2026-09-11). NQ-1 is CLOSED.** GOAL-FR-049's active-goal admission
> counter re-homes from the retired S4 to **E11**. **`pkg/gateway/gateway.go` therefore has a single
> owner for the whole delivery: E11.** No new one-file wave is created in round 4, and the counter is
> **not** given to E8. Every must-NOT-touch row naming this file now reads `(E11)`; a row that still
> reads `(E11/S4)` is stale and S4 does not exist.

**NQ-2 — does anything still need a *persisted* per-goal budget number, now that no one can edit
one?** *Blocks S1, E6, E8 and F1's final field list.* D-E removes the per-goal budget **control**.
Read strictly, that also removes the only writer for the per-goal **value**: GOAL-FR-025's persisted
override and R-21's `EffectiveGoalMaxRoundsWithOverride(override *int)` now have nothing to write to
them. **This plan treats them as retired with the control** — `EffectiveGoalMaxRounds()` stays as it
is, reading the one global setting, and R-21's E6 → E8 chain lapses.

> **ANSWERED — CONFIRMED (D-K, 2026-09-11). NQ-2 is CLOSED.** GOAL-FR-025's **persisted per-goal
> override is RETIRED**, and so is **`EffectiveGoalMaxRoundsWithOverride`** — it is never created.
> **`EffectiveGoalMaxRounds()` keeps its current signature**, reading the one global Settings →
> Performance value, and both existing call sites keep calling it unchanged. Nothing is kept "just in
> case": no per-goal budget field enters `Goal.yaml`, the goal record, `Task.yaml`, or any other
> schema, and no wave adds an override parameter to any config signature. A future release that wants
> one pays for a second contracts regeneration then. **Struck accordingly, in place and visibly:**
> C-05's override clause, C-28's whole resolution, R-03's override clause, and R-21 (already
> amended). **S1, E6 and E12 each carry the matching must-NOT-touch line.**

**NQ-3 (OPEN — AWAITING THE OPERATOR) — retiring judge spec §M removes the Judge's
degraded-capability reporting entirely. Is that intended for the hand-edited case too?**
*Blocks E11 and U3's retirement, round 4 — but see the status note below: no wave actually waits.* §M is written as
an upgrade story — an install whose `agents/judge/SOUL.md` predates the quote-emitting rubric — and
D-F retires it on that basis. But the same detection also covers a **fresh** install whose operator
hand-edits the Judge's soul and removes the instructions the rubric depends on. After the retirement
nothing notices and nothing reports it. **This plan retires it as instructed** (E11 loses the
self-check, U3 is retired, `rubric_capability` never reaches `Agent.yaml`). If the hand-edited case
should still be reported, the cheapest version is a boot-time WARN log with no wire field and no
badge — say so before round 4, and note it costs E11 one new file but **no** contract change.

> **STATUS: OPEN. This one is NOT answered, and is recorded here as awaiting the operator.** D-H
> retires judge spec §M "entirely, including the FRESH-install half", which leans hard toward *no
> reporting at all* — but the hand-edited fresh-install case has never been put to the operator as
> its own decision, and this plan will not record an inference as an answer. **No wave is blocked by
> the wait.** The plan proceeds on the retirement exactly as D-H instructs: U3 is retired, E11 loses
> `judge_rubric_selfcheck.go` (which is never created), `rubric_capability` never reaches
> `Agent.yaml`, and JUDGE-FR-077 – FR-080 stay retired. If the operator later wants the hand-edited
> case reported, it is additive: one new file in a later wave, a boot-time WARN, **no** contract
> change, **no** re-opening of F1.

---

### The analyst questions, unchanged

These are the things the ten analysts could not resolve. They are recorded as they were reported,
not smoothed over. Each names the wave it blocks. **OQ-1 … OQ-22 predate the ADR-085 fold; except
where marked ANSWERED below, they are still open — and every one of them now also blocks the
browser lane's B123 and B8, which their own wave plan declared dependency-free.** OQ-23 onward are
new with the fold. **Where this document had to take an assumption to let work start, the assumption
is stated and marked as an assumption — it is not an answer.**

Anyone answering one of these should answer it *here*, in this file, before the blocked wave starts.

### Blocking wave F1⊕U2 (the one contracts change-set) — these must be settled before it begins

**OQ-1 — the exact value set for `Goal.state`.**
> "FR-028 requires at least four distinguished terminal reasons (met, budget/round exhaustion, idle
> expiry, explicit operator clear); the shipped `contracts/components/schemas/Goal.yaml` state enum
> has only `active|done|failed|cleared`, collapsing idle expiry and round exhaustion onto `failed`.
> I flagged that the enum must widen but did not choose the values, because `GoalStatusFrame.state`
> is a separate DISPLAY enum that `Goal.yaml`'s own description explicitly warns must not be
> conflated with the record state. Getting this wrong in either direction blanks the goal card at
> the SPA edge with a green `make verify-contracts`."

*Assumption taken so F1 can be scoped:* `[defining, active, met, exhausted, expired, cleared]`.
**Blocks: F1, and through it S1 (the persisted struct must use the same field names).**

**OQ-2 — whether a goal needs a REST surface at all. — ANSWERED (D-E, 2026-09-11).**
> "Today `Goal.yaml` is registered in `openapi.yaml` `components.schemas` but referenced by NO path,
> so it generates types that nothing calls. The goal spec never adds an endpoint, and the SPA
> reaches goal state through `GoalStatusFrame` (WS) and through the task shapes. I left it type-only
> and put the operator-editable budget on the task shapes instead. If a later wave needs to read or
> write a goal record directly (e.g. a chat goal's budget, which has no task to hang off), a
> `GET/PATCH /api/v1/goals/{id}` and its request schema are a SECOND contracts commit — which is
> exactly what the one-atomic-commit rule is trying to avoid, so this is worth settling before F1
> starts."

**ANSWER (D-E): type-only, no REST path — and the question behind it has gone away.** `Goal.yaml`
stays registered in `components.schemas` and referenced by no path. There is no
`GET`/`PATCH /api/v1/goals/{id}` and no `GoalUpdateRequest.yaml`, in this delivery or as a follow-up
commit. The reason the original question was hard — a chat goal has no task panel to hang a budget
control off — is dissolved rather than answered: **there is no per-goal budget control at all**, for
either kind of goal. One global setting under Settings → Performance governs both identically. The
definition of done still reaches the SPA on the three task shapes (C-11). C-37's per-goal override is
retired with GOAL-FR-046. **No longer blocks anything.**

**OQ-3 — the name of FR-083's pill state.**
> "FR-083's `GoalStatusFrame.state` value has no name anywhere in the judge spec — the copies table
> calls it only 'the CAS state' and FR-083 itself gives only the machine-readable reason string
> `cas_loss`. I resolved it to `judge_cas_loss` so F1 can land, but this is my naming, not the
> spec's. If the operator or a later revision names it differently, F1's enum must change and
> everything downstream regenerates."

*Assumption taken:* `judge_cas_loss`, with the reason string staying `cas_loss`. **Blocks: F1, U2.**

**OQ-4 — the name of FR-006b's persisted clause-count field.**
> "FR-006b's persisted clause-count field has no name in the judge spec — it is described only as
> 'an optional integer on `AcceptanceCriterion`'. I resolved it to `clause_count` (integer, minimum
> 1, optional, in all four copies). Note also the `AcceptanceCriterionInput.yaml` header's
> documented codegen trap: do NOT give it an OpenAPI `default:` key, or `openapi-typescript` emits
> it as non-optional."

*Assumption taken:* `clause_count`. **Blocks: F1, F2.**

**OQ-5 — whether FR-006b survives revision 9 at all.**
> "I did not verify the judge spec's FR-006b 'clause count' addition to
> `AcceptanceCriterion`/`AcceptanceCriterionInput`, which judge W3 bundles with the (now dead)
> status-enum change. If that field survives rev 9 it is additive-optional and needs no SPA render,
> but I did not read the FR — someone should confirm it before F1 closes the `AcceptanceCriterion`
> YAML pair."

*Assumption taken:* it survives (ADR-084 §10 does not name it in the retirement table) and is
additive-optional with no SPA render. **Blocks: F1.**

**OQ-6 — whether the fourth terminal ending is distinguishable on the pill, or only on the record.**
> "FR-028 reasons from pill states ('all four already produce distinct pill states —
> done/failed/cleared'), and S-16 says only 'its terminal status is distinct from the other three'.
> I added `expired` to BOTH, which satisfies either reading, at the cost of one pill value the SPA
> must render. If the operator wants the pill kept at its current terminal vocabulary, drop
> `expired` from the two `GoalStatusFrame` copies and keep it on `Goal.state` only — that is a
> one-line change to F1, but only before F1 lands."

*Assumption taken:* `expired` on both. **Blocks: F1, U2.**

**OQ-7 — where the install-wide goal budget default lives in the settings contract.**
> "I put it on `PerformanceSettings.yaml` / `PerformanceSettingsUpdate.yaml` because that is the
> only surface `PerformanceSection.tsx` can read, but goal W6's backend write-set is
> `pkg/config/planning.go` and I did not verify that the `/performance` handler reads planning
> config at all. If it does not, F1 must either widen that handler or pick a different settings
> shape, and U6's dependency changes accordingly."

*Assumption taken:* the two performance schemas carry it, and E6 widens
`pkg/gateway/rest_performance.go` to read it from planning config. **Blocks: F1, E6, U6.**

**OQ-8 — whether a goal schema may inline the criterion status enum, creating a fifth copy.**
> "The goal spec's W1 also creates new `contracts/components/schemas/Goal*.yaml` files. If any of
> those inline their own criterion `status` enum rather than `$ref`-ing `AcceptanceCriterion.yaml`,
> that is a FIFTH copy and the four-copy lock test would not cover it. I could not check, because
> those files do not exist yet. Recommend the contracts wave be told: Goal schemas `$ref`
> `AcceptanceCriterion.yaml`, never inline — and if an inline copy is unavoidable (the way
> `asyncapi.yaml`'s already is), the test must be extended to five before that wave merges."

*Instruction taken, not an assumption:* F1 `$ref`s and never inlines. If F1 finds it cannot, it
must say so and T1's comparator is widened to five copies **before** F1 merges. **Blocks: F1, T1.**

**OQ-9 — whether the typecheck survives the `binding_kind` → `owner_kind` rename.**
> "I did not verify that `npx tsc -b --noEmit` (which `make verify-contracts` runs before the
> git-diff gate) stays green after the rename. Grep found no non-generated TypeScript consumer, so
> it should, but I could not run the typecheck read-only within this mandate. F1's implementer must
> run `make verify-contracts` once, in full, before committing — and must capture the exit code
> without a pipe."

**Blocks: F1.** This is an instruction to F1, not a decision anyone else needs to make.

### Blocking wave S1 (the goal store)

**OQ-10 — may a task hold more than one goal over its lifetime?**
> "ADR-086 §5 open question 2 (may a task hold more than one goal over its lifetime — a re-run after
> failure) is not answered by `goal-entity-spec.md`'s FR section A-E, which I read in full. It
> determines whether `Goal.OwnerID` is unique-per-owner or one-to-many, which is a persisted-shape
> decision S1 cannot defer. I did not find an answer anywhere in the ranges I read and I am not
> guessing."

**No assumption is taken here, because both shapes are defensible and the wrong one is expensive to
undo.** This is the single most important unanswered question in the delivery: it changes the goal
store's primary index, the orphan sweep, and whether a failed task's goal record is superseded or
replaced. **Blocks: S1, and through S1 the whole critical path.** Answer it first.

**OQ-11 — is `pkg/goal` built on `pkg/entity`'s generic store, and how is it indexed by owner?**
> "`pkg/entity.New[T]` is genuinely generic and its POSIX-only cross-process guarantee is already
> tested. Reusing it satisfies goal FR-001's 'follows the `pkg/entity` precedent' literally. But
> `Store[T]` has no query-by-owner index, and both the quiet-window sweep's new selector and the
> orphan detection need one; a `List()`-plus-filter over every goal record on every 30-second plan
> engine tick may not be acceptable. I have specified the accessor S1 must expose but NOT how it is
> indexed. Someone must size this against the expected goal count before S1 is written."

*Assumption taken:* reuse `pkg/entity` rather than fork it (this is in S1's must-not-touch list).
The indexing question is genuinely open. **Blocks: S1, and the sweep in E12 and S3.**

**OQ-12 — are the unable-to-verify tracker and the escalation gate durable goal-record state?**
> "I verified `UnableToVerifyTracker` and `UnjudgeableEscalationGate` are in-memory only. I could
> NOT determine whether ADR-086 intends these to become durable goal-record state alongside the two
> keeper counters FR-004 names. ADR-086 D1's field list does not mention them and the judge spec
> assumes in-memory. If they stay in-memory, a gateway restart resets an escalation ladder that a
> per-goal record would have preserved — the same class of loss FR-004 was added to prevent. Needs
> an operator or architect call; I have not assumed either way."

*Assumption taken so S1 can be scoped:* they stay in-memory and are **not** goal-record fields, on
the strength of ADR-084 §10 keeping them internal. If that is wrong, S1's record gains two fields
and F1's schema gains two — a second contracts commit. **Blocks: S1, F1.**

### Blocking waves G3 and E11 (the rubric change and its closure gate)

**OQ-13 — nothing in this repository enforces merge *order*, only co-presence.**
> "`check-adr084-closures.sh` is described as enforcing merge order, but a guard script running on a
> pull request can only ever assert co-presence at HEAD of that pull request's merge result — the
> same limitation the spec itself concedes for the equivalent Go test. I have not found a mechanism
> in this repo that genuinely enforces merge ORDER rather than co-presence, and I do not believe one
> exists; G3 should be scoped to co-presence and described honestly, or the requirement needs a
> different mechanism (a branch-protection rule, not a script)."

*Decision taken for delivery:* G3 ships as a **co-presence** guard and says so in its own header;
the actual ordering (the rubric's "Do not run tools" removal must not merge before E1's closure
tests and E2's budget tests are green on the base branch) is enforced by this plan's round
structure and by the human approving the merge — not by a script. If the operator wants it
mechanically enforced, it needs a branch-protection rule and that is outside this delivery.
**Blocks: nothing — but E11 must not be described as mechanically gated when it is not.**

### Blocking wave E14 (the projection)

**OQ-14 — does every rung combination emit exactly one verdict per input criterion?**
> "I did not verify whether `runVerifierAdjudication` guarantees exactly one `CriterionVerdict` per
> input criterion in every rung combination. I read the evidence that the fail-closed path and the
> unjudgeable path both emit an entry per criterion, and E14's rule (an absent id leaves the
> existing status untouched) is written to be safe either way — but if some rung can drop a
> criterion silently, that rule quietly preserves a stale status and a reader would not see it.
> Worth one scoped test against `verifier_adjudication.go`, which is outside my write set."

**Blocks: E14's correctness argument, and T4 owns the scoped test.** Add it to T4.

**OQ-15 — duplicate criterion ids across a goal's criteria and definition-of-done lists.**
> "The origin table makes this detectable at union time, but nothing today forbids it, and I did not
> find a validator that would reject it. I have specified detection (log plus count) but not a
> policy — reject at compile time, or last-write-wins at projection time. That belongs to whoever
> owns `goal_compile.go`'s validation, not to the projection."

*Assumption taken:* detect, log and count in E14; **no rejection policy is implemented**, so a
duplicate currently resolves last-write-wins by index. **Blocks: E14 (behaviour), and a future
validator wave that this delivery does not contain.**

### Blocking wave E12

**OQ-16 — does `pkg/tools/set_goal.go` need any behaviour change at all?**
> "It reaches the goal record only through `GoalRecordAccess`, so if E4 re-points that seam cleanly
> the tool may be untouched. I assigned it to E12 so someone is accountable for finding out, but the
> answer is genuinely unknown to me."

**Blocks: nothing — E12 owns the file and is accountable for the answer.** If the answer is "no
change", E12 says so explicitly rather than silently leaving the file alone.

### Blocking the SPA waves

**OQ-17 — do `blocked` and `claim_overturned` join `GOAL_TERMINAL_STATES`?**
> "I assigned NOT-terminal on the strength of FR-093 calling `blocked` a park and FR-102 calling
> `claim_overturned` 'a state, not an alert', but neither FR states it, and getting it wrong makes a
> pill vanish after the terminal display timeout instead of persisting — and FR-102's whole point is
> that a returning operator sees it."

*Assumption taken:* not terminal. **Blocks: U2.**

**OQ-18 — do the four surviving verdict fields get any SPA rendering?**
> "ADR-084 revision 9 retires only the `Outcome` sibling and does not restate FR-070a. No FR
> requires rendering the others, and `CriteriaVerdictList` already has an evidence expander, so I
> assumed no render change and gave the file a zero-diff expectation — but if a later revision asks
> for provenance in the row, that lands in U5, not U3."

*Assumption taken:* no render change. **Blocks: U5's scope (it is currently a near-zero-diff file).** *(The quoted alternative names U3, which is retired by D-F — if provenance is ever rendered it lands in U5, and nowhere else.)*

### Blocking the test and verification waves

**OQ-19 — the roughly forty stale rows of the judge test matrix were not enumerated one by one.**
> "Whether the judge spec should be formally re-issued at revision 9 before implementation starts,
> or whether the ~40 affected rows are corrected in place by the waves that own them. I have
> specified the correct behaviour for every row I inspected, but I did not enumerate all ~40 by
> name — the matrix is ~200 rows and I read it in full only at truncated width. Someone must do a
> line-by-line pass; the mechanical backstop is the withdrawal guard, which will fail the build if
> any of them is implemented as written."

**Blocks: T4 (it owns the judge regression rewrites) and, weakly, every judge wave.** The guard in
G2 is the safety net, not a substitute for the pass. Note that this document may not edit the spec
to fix them — §6 rule 6.

**OQ-20 — five judge waves were never checked for goal-state reads.**
> "I did NOT verify judge-spec waves W1, W2, W6, W8 or the W9 evidence-tier work against the code —
> they are outside the storage mandate and, on the ranges I read, touch no goal state. If any of
> them turns out to read `session.UnifiedMeta`'s `Goal*` fields, my 'may interleave freely' claim is
> too permissive for that wave."

**Blocks: the round-1 concurrency claim for E0, E1, E2, E3.** The cheap check is one grep for
`Goal` field reads across each of those waves' write-sets before round 1 starts; if one of them does
read goal state, it moves out of round 1 and behind S1 and E4.

**OQ-21 — should `pkg/session` join the `-race` list?**
> "It is absent today, pre-existing, and the goal spec's storage wave edits four files in it; I flag
> it but it is outside this delivery's scope to decide."

*Decision taken:* out of scope. G4 adds `./pkg/goal/...` only. Recorded so nobody adds it quietly
inside another wave. **Blocks: nothing.**

**OQ-22 — should the per-guard `make lint-<name>` targets survive?**
> "I kept them for continuity with `CLAUDE.md`'s per-guard references, but that is a judgement call,
> not a finding."

*Decision taken:* keep them as thin delegates. **Blocks: nothing.**

### New with the ADR-085 fold

**OQ-23 — the new frame has no name anywhere in ADR-085.**
> "The ADR-085 spec says only 'a new chat-channel wire type' and grepping it for a frame literal
> returns nothing. FR-042 itself names no type string. `BrowserHandoverNoticeFrame` /
> `browser_handover_notice` is DERIVED, not invented: the spec's own test filenames are
> `ChatScreen.browser-handover-notice.test.tsx` and `chat.browser-handover-notice.test.ts`, its e2e
> observable is `[data-testid=\"browser-handover-notice\"]`, and its replay test is
> `TestReplay_HandoverNoticeReplaysAsTheSameFrameType`. A different name is a one-line change
> BEFORE F1 lands and a full four-tree regeneration after."

*Assumption taken so F1 can be scoped:* `BrowserHandoverNoticeFrame` / `browser_handover_notice`.
**Blocks: F1⊕U2, and through it B123 and B8.**

**OQ-24 — the frame's field set beyond the four the spec pins.**
> "`type`, `session_id` and `message_id` and `text` are forced — by FR-041/FR-042/FR-044 and by
> `src/store/chat.ts`'s `SESSION_SCOPED_FRAME_TYPES` routing, which assumes a session-scoped frame
> carries a `session_id`. `reason` (the `browser_handover` tool's reason) and `cause` (an enum over
> the three producers: operator take, agent handover, idle release) are a proposal so the SPA can
> render the three producers distinctly. If the operator wants one undifferentiated line, drop both
> — but only before F1."

*Constraints that are NOT open, whichever way this lands:* `session_id` is **required** with
`minLength: 1`; the deterministic notice id is a **string** `message_id` and there is **no raw
unix-nanosecond field** (C-84); the frame is **not** a `oneOf`/discriminated union.
**Blocks: F1⊕U2, B123 (emission), B8 (render).**

**OQ-25 — the `Message.yaml` system-entry subtype field name.**
> "BROWSER-FR-043a needs a stamped subtype on the transcript entry so replay can discriminate
> without prefix-matching prose. Recommend `system_subtype` with a single-value enum rather than a
> free-text string, so the next subtype is a deliberate contract edit. Not decided."

*Assumption taken:* `system_subtype`, enum of one value. **Blocks: F1⊕U2, B123.**

**OQ-26 — the FR-078 agent-card capability field.**
> "The contracts analyst independently confirmed that `degraded_reason` is a closed single-value
> string enum and provably cannot carry FR-078's payload, and proposed
> `{missing_declarations[], required_sentinels[], disabled_controls[]}` as a new optional object."

**CANCELLED — D-F (2026-09-11).** JUDGE-FR-078 is part of judge spec §M, "the mixed state", which is
an upgrade scenario and is retired in full. No capability field is added to `Agent.yaml` under any
name, C-38's decision is moot, and wave U3 — the only consumer — is retired. **Blocks: nothing.**

**OQ-27 — which failure-aggregation behaviour `scripts/guards.sh` uses on the CI worker.**
> "C-19 says the runner 'runs all of them rather than stopping at the first failure', but the
> existing surfaces do the opposite: `deploy/ci-worker/runci.sh`'s `run_lint()` uses `|| return 1`
> after every guard, and `pr.yml`'s job relies on step-level fail-fast. Switching to run-all changes
> what a red CI run reports — one failure versus all of them — and how long the job takes when
> several fail at once. I found no statement of which behaviour the operator wants on the WORKER
> specifically."

*Recommendation, not a decision:* run-all with a summary line, matching C-19, and note that
`runci.sh`'s contract changes with it. **Blocks: G1's implementation detail, and the part of
`scripts/guards.test.sh` that would prove exit-code propagation under run-all.**

**OQ-28 — who emits the judge and goal frames from `pkg/gateway/websocket.go`, if anyone.**
> "Nobody in either plan owns a send site in `pkg/gateway/websocket.go` for the new
> `JudgeVerdictFrame` and the new `GoalStatusFrame` states — the file appears zero times in this
> plan — while B123 owns that file outright from round 5. Either those frames are emitted from
> somewhere else entirely (plausible: `goal_status` is emitted from `pkg/agent/goal_loop.go` and
> `goal_triggers.go`, which the ADR-057 artefact block itself cites) or a wave is missing. I did not
> trace the emission path far enough to say which."

*The cheap check, before round 5:* one grep for the emission path of `goal_status` and
`judge_verdict`. If either is emitted from `websocket.go`, a joint wave collides with B123 in round 5
and must be re-sequenced. **Blocks: the round-5 disjointness claim for B123.**

**OQ-29 — does the new frame join the SPA's session-scoped set, and does the ADR-057 artefact prose
go stale?**
> "The artefact block at `pkg/gateway/websocket.go:3547` states '19 members' and classifies each. If
> the new frame joins `SESSION_SCOPED_FRAME_TYPES` — which B8 is instructed to make it do — that
> prose count moves to 20. `TestRoutingSessionID_ConsumerSetIsClosed` only asserts
> `classACount >= 1`, so it will NOT redden: this is a documentation-accuracy gap, not a gate."

*Decision taken:* B123 corrects the member count and appends the new frame's classification when it
edits that file in round 5, and does not touch anything else in the block (C-83). **Blocks: nothing;
recorded so nobody 'fixes' it twice or treats it as a red.**

**OQ-30 — can ADR-085's three tool rosters be derived from today's two maps?**
> "BROWSER-FR-035 needs three rosters — action, capture, read-only — where `pkg/tools/browser/audit.go`
> has two maps today. FR-016a explicitly forbids a fourth hand-written list. I did not verify that
> the third roster can be derived rather than hand-written."

*Wholly inside B123's lane 1 and crossing no design boundary.* **B123's agent must confirm it before
writing `ControlGatedToolNames()`, and report if a hand-written third list turns out to be
unavoidable.** **Blocks: nothing outside B123.**

**OQ-31 — does the ADR-085 spec's FR-042 section contain a schema F1 must copy?**
> "R-03 folds ADR-085's contracts wave into F1, so FR-042's exact frame shape must reach F1's agent
> before round 1 begins. One analyst read that section only by heading and did not extract the
> schema. **Whoever briefs F1 must read `docs/internal/specs/browser-control-handover-spec.md`
> §F in full.** A schema invented at F1 time and corrected later means a second contracts
> regeneration — which is the exact thing folding W4 into F1 exists to prevent."

**Blocks: F1⊕U2. This is a briefing instruction, not a decision anyone needs to make.**

### One note on tooling, so it is not mistaken for a defect

Several analysts reported that read-only `grep` and `ls` probes exited non-zero and were flagged by
the session's tool gate as failures. Those were deliberate negative existence checks — proving that
`pkg/goal` does not exist, that neither projection file exists, that `dod` does not appear on
`Task.yaml`, and that the string "revision 9" appears nowhere in the judge specification. **The
zero hits are the evidence.** The same probes were re-run while assembling this document, with the
same results. Nothing here is an unresolved technical issue.

---

## 9. How to use this document

An implementing agent is handed **this document and one wave row**. Before writing anything:

1. Read §1 (which authority wins), §6 (the standing rules), and your own row in §3.
2. Read §5 for every file in your write-set that appears in a chain, and confirm the wave before you
   in that chain is merged and green. **If `pkg/agent/loop.go` is in your write-set, read §5's
   closing paragraph on that file in full before you open it.**
3. Read every collision in §2 whose "Wave" column names you — including the `C-67` … `C-94` rows,
   which are the ADR-085 fold and which several older rows now defer to.
4. Check §8 for an open question that blocks your wave. **If one does, stop and ask — do not take
   the recorded assumption as an answer.**
5. Work only inside your write-set. If you believe you must write anything else, stop and report it.
6. Finish with L1 and L7 of §7 for your own wave — plus L8 and L9 if you added a Playwright spec or a
   `pkg/tools/browser` test, and L10 from round 5 onward — and hand the wave to review with the exit
   codes and the `--- PASS` counts quoted. **Then, at the END OF THE ROUND, someone runs L11
   (`go vet`) over the four package paths, from round 1 onward — every round, not only the last. Round 1 already deletes symbols in `pkg/agent` (E3) and writes `pkg/config` and `pkg/tools` (E0, E2), so the first round is exactly where a test file referencing a deleted symbol first appears.**
   `go build` does not compile test files and `go vet` does, so L11 is the only local gate that
   catches a `_test.go` still naming a symbol this delivery deleted.

If you are working **B123**: you are **ONE agent** working its three lanes **in sequence** (lane 1 →
lane 2 → lane 3) on one branch, producing one pull request — **D-I**, because the six-per-round cap
counts agents, not slots. The lanes' file lists are disjoint from one another, but the branch is not
green — and not merged — until all three lanes are on it and
`CGO_ENABLED=0 go build -tags goolm,stdjson ./...` exits 0. **Do not create a stub symbol to make
your lane compile alone.** The same rule applies to the two waves in **F1⊕U2**: one branch, one
merge, and the tree does not typecheck in between.

---

## Grill resolutions (round 1)

Six adversarial lenses reviewed ADR-084's judge spec, ADR-085's browser-handover spec, ADR-086's
goal spec and this plan. They returned 56 findings. This section is the adjudication: duplicates
merged, every load-bearing claim re-checked against the code at
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2`, and every engineering call decided.

**How to read it.** The resolutions below are instructions, not suggestions. If a resolution names
a file, a symbol, a JSON key or a number, that is the value to use. Do not substitute your own
judgement, and do not treat a resolution as an invitation to reopen the design. Where a resolution
contradicts a sentence in one of the three specs, the resolution wins — the specs were written
before ADR-084 revision 9 and ADR-086 revision 2 landed, and this plan is the precedence document
(§1).

Six questions survived to the operator. **All six were ANSWERED on 2026-09-11** — the answers, and
what each one changes, are in §8. The short form is at the end of this section. Several resolutions
below were cancelled, dissolved or re-homed by those answers; each such row says so in place.

### What was re-checked, and what it cost

Every claim that an edit target does not exist, or that a symbol behaves as described, was re-run
here. **No ghost symbols were found.** All 21 judge-spec symbols, all 10 goal symbols and all 3
browser anchor symbols exist with real non-test definitions; every symbol the lenses claimed absent
(`ToolRootChatSessionID`, `IsControlledByLiveViewer`, `ControlGatedToolNames`,
`SetBrowserWheelReleaseHook`, `WaitingSurfaceEmitter`, `OperatorPrompt`, `ControlIdleReleaseSec`,
`ToolResult.Deferred`, `verdict_projection.go`, `verdict_status_projection.go`) is genuinely absent.
The plan's pre-flight inventory in §1 is accurate. That is the good news, and it means the failures
below are all failures of *assignment and vocabulary*, not of fiction.

### Resolved findings

| id | severity | where | decision | why |
|---|---|---|---|---|
| **R-01** (CON-01, GJ-1, UNDER-08) | blocker | `docs/internal/specs/judge-active-reviewer-spec.md`, 167 occurrences of `unable_to_verify` | **The judge spec gets a precedence block, not a rewrite.** Before round 1, prepend a `## §0 — Order of precedence` section at the top of the judge spec whose entire content is: *"ADR-084 revision 9 §10 withdrew `unable_to_verify` in full. Every sentence below that names it is cancelled. The replacement value is `unmet` in every case. The distinction survives only in the per-criterion `Reason` string and in a WARN log line — never as an outcome, a status, an enum value or a test oracle."* Do not strike the 167 sentences individually. Additionally: **wave G2 moves to round 1** and its guard must be green before any wave may regenerate contracts. | Verified: the spec says `unable_to_verify` 167 times, the plan cancels it in 4 table rows, and the spec header still cites revision 7. An agent handed `JUDGE-FR-076` builds the retired thing, regenerates both trees atomically, and `make verify-contracts` stays green. A precedence block at the top of the file the agent actually opens is cheaper than 167 edits and closes the same hole. |
| **R-02** (FG-03) | major | wave G2, `scripts/check-no-unable-to-verify-outcome.sh` | **Widen the scan set to `pkg/task/`, `pkg/agent/` and all of `src/`**, keeping only these four `pkg/agent` files as named construction-level exclusions (list them in the guard header). The guard must also catch a fourth `CriterionStatus` constant and an `Outcome` field on `task.CriterionVerdict`. Its `.test.sh` must plant both as fixtures and assert a non-zero exit for each. | Verified: `CriterionVerdict` is at `pkg/task/verdict.go:25` and the three `CriterionStatus` constants at `pkg/task/criterion.go:266-268`. `pkg/task/` was in neither the scan set nor the exclusion list, and wave F2 edits that exact struct. As scoped, the guard could not see the most likely reintroduction site. |
| **R-03** (OPS-F3, UNDER-01) | blocker | goal spec FR-024/MV-2 vs C-37 vs `pkg/agent/task_executor.go::consumeAttemptOrExhaust` | **Decided by ADR-086 D10, which is operator-ratified — do not reopen.** One budget *number*, default 20, ~~with a per-goal override~~ **— the override clause is RETIRED BY D-E (2026-09-11), NQ-2 CONFIRMED: there is NO per-goal override. The single global Settings → Performance value is the only budget control, governing task goals and chat goals identically.** Everything else in this row stands. Two *counters* survive, distinct as today: task attempts and goal rounds. `config.DefaultTaskMaxAttempts` changes from 3 to 20 in `pkg/config/planning.go:12`. `Task.MaxAttempts` stays as the per-task override. The `2 × maxAttempts` divergence brake stays, so the task ceiling becomes 40. `TestAttemptsVsRounds_DistinctBrakes` (`pkg/agent/attempts_vs_rounds_test.go`) stays and is **not** rewritten — it asserts the counters are distinct, not that their numbers differ, and that property is unchanged. | D10 says it in terms: *"a task's 3 attempts was never a considered choice against the goal loop's 20 rounds"*, and *"the `2 × maxAttempts` hard ceiling survives as a divergence brake — it exists because two independent gates can disagree"*. Two gates, one number. This is the reading that satisfies both documents and leaves the pinning test green. |
| **R-04** (OPS-F2, UNDER-10) | blocker | plan §8 OQ-10 | **OQ-10 is answered and is no longer blocking. Unblock wave S1.** `Goal.OwnerID` is unique per owner: one goal per task for the task's whole life. Add the missing transition to FR-028: a terminal task-owned goal re-enters `active` when its task is re-run, resetting `attempts_used` and `rounds_used` to 0, clearing `latest_reason` and the current verdict, and **appending** the prior verdict to a retained history array rather than overwriting it. A session-owned goal has no such edge — a terminal chat goal stays terminal. | Verified: goal spec line 1153 (A-2) and line 243 (EC-1) both answer it — *"the same goal is reused with its attempts reset; the prior verdict history is retained"*. The plan called this "the single most important unanswered question in the delivery" because the analyst read sections A–E and the assumptions table sits outside them. The residual gap was the missing terminal→active edge, decided above. |
| **R-05** (CON-02) | blocker | goal spec lines 263, 329, 137, 432, 655-660 vs judge FR-095/FR-097 | **ADR-084 wins on trigger.** A `met` claim is the sole adjudication trigger. The following goal-spec text is cancelled: line 263's *"and after a quiet period, whichever comes first"*, Row 2's *"judged on persisted evidence with no claim"*, US-3 AS-4's *"and then judges"*, FR-019's second sentence, and BDD scenario S-10 in full. S-10's `Then` is replaced by: *"it stops pushing and the goal stays active with no round consumed"*. | The plan already made this call for the narrower FR-017/FR-097 case in C-13 with the reason *"when the Judge is triggered is its domain"*. FR-095 backs it with a CI grep gate that would fail the goal spec's version. One trigger, one owner. |
| **R-06** (CON-03, CON-04, OPS-F4) | blocker | judge spec E-31 / FR-097 / FR-096, and `pkg/agent/goal_loop.go::goalIdleExpirySweep` | **Strike "the rounds bound and" from all three judge-spec statements.** **Amended by D-A (2026-09-11): the sweep stays, the nudges are NOT bounded.** After FR-095 the calendar brake is the sole terminator for a goal that never claims, and its default is 7 days (`config.DefaultIdleExpiryDays = 7`, `pkg/config/planning.go:16`). Separately, **`goalIdleExpirySweep` is added to wave E8's write-set by name** and to C-25's re-point list: its selector at `pkg/agent/goal_loop.go:1039` moves off `ListSessions()` + `GoalCondition` onto S1's exported "an active goal record exists for this owner" predicate over `pkg/goal`. Idle expiry uses the goal record's own `last_activity_at` for both owner kinds. **OQ-A is ANSWERED (D-A): seven days is the right bound, and the nudge ladder gets no ending of its own — do not bound it.** The "give the nudge ladder its own ending" option is **rejected**. The surviving half of this resolution still binds in full: **`goalIdleExpirySweep` is added to wave E8's write-set by name** and to C-25's re-point list, its selector moves off `ListSessions()` + `GoalCondition` onto S1's exported "an active goal record exists for this owner" predicate over `pkg/goal`, and idle is read from the goal record's own `last_activity_at` for both owner kinds. D-A adds one thing on top: the nudge and re-post **text** (E13) must instruct the agent to mark the work complete with the claim tool. | Verified: `GoalRoundsUsed` is incremented at exactly two sites (`pkg/agent/goal_triggers.go:534` and `:1417`), both inside adjudication result handling. No claim, no round, no rounds bound. And `goalIdleExpirySweep` appears **zero** times in this plan while being the only surviving terminator — its selector cannot see a task-owned goal, which has no session carrying `GoalCondition`. |
| **R-07** (BS-2, BS-3, BS-4, UNDER-06) | ~~blocker~~ **DISSOLVED — D-F** | goal spec FR-050 vs FR-051; waves S2, S4, S6 | **FR-050 gets a carve-out, written into this plan as binding:** *"A read-only, write-never presence check on the legacy keys in `goal.json`, whose only outputs are one terminal frame and one operator-visible log line, is NOT a migration and is the required implementation of FR-051."* `TestNoMigrationPathExists` is scoped to assert no legacy value is composed into `UnifiedMeta` or written back — not that a key is never read. **S4's detector MUST read `goal.json` as `map[string]any`** declared inside `pkg/gateway/goal_orphan_boot.go`, keying on the presence of `goal_condition`, `goal_criteria` or `goal_rounds_used`. It must never reference `session.SessionMeta` or `MetaPatch`. Add S4 to S6's `dependsOn`. **DISSOLVED by D-F (2026-09-11) — none of the above is to be implemented.** GOAL-FR-051 is retired, wave S4 is retired and `pkg/gateway/goal_orphan_boot.go` is never created, so **there is no detector left to feed**. **GOAL-FR-050's "build no migration" therefore stands UNQUALIFIED: no carve-out, no read-only presence check, no `map[string]any` reader, no legacy key lookup anywhere.** `TestNoMigrationPathExists` may be written at its strongest — assert that no legacy value is read at all, not merely that none is written back. S4 is **not** added to S6's `dependsOn`, because S4 does not exist. OQ-C is answered in §8: no rescue of the parked question card and no per-session warning either. This row is kept rather than deleted so the cancellation is visible to anyone who read the earlier version. | Verified: `pkg/session/unified_meta_files.go::u5ReadGoalFile` does a plain `json.Unmarshal` with no `DisallowUnknownFields`, so after S6 deletes the fields the orphan keys vanish silently. A typed detector written in round 5 stops working in round 7 with no test failing. FR-050 and FR-051 as written cannot both be obeyed; this is the only reading under which FR-051 can fire at all. |
| **R-08** (GJ-2, GJ-3) | major | judge spec lines 759, 773, 1703 | **Replace all three raw line numbers with `file::symbol` form before dispatch.** Lines 759 and 1703: *"the transcript tool-call record's `Parameters: cloneEventArguments(toolArgs)` field in `pkg/agent/loop.go` — NOT the event record's `Arguments:` field"*. Line 773: *"`pkg/agent/loop.go::runAgentLoop`'s `checkGoalLoopAfterTurn` call"*. In the same edit, add `Media` to line 759's field enumeration of `toolResultAdmission`. | Verified: both offsets are correct today and both go stale during the build — wave E13 reorders the region containing line 8302. `cloneEventArguments(toolArgs)` appears at two near-identical sites (`loop.go:11415` `Arguments:` and `loop.go:11999` `Parameters:`), so a drifted offset lands on a plausible wrong one rather than failing. `CLAUDE.md` forbids `file:line` in exactly these two files for exactly this reason. `toolResultAdmission` really has a sixth field, `Media []string`. |
| **R-09** (BL-1) | blocker | `pkg/agent/goal_triggers.go::runGoalAdjudication`, §5 file-chain table | **Add a fifth named region to the `pkg/agent/goal_triggers.go` row, owned by E13, in the same commit as the deferred dispatch:** *"`runGoalAdjudication`'s context derivation — `context.WithTimeout(ctx, goalJudgeRoundTimeout)` becomes `context.WithTimeout(context.Background(), goalJudgeRoundTimeout)`."* Gate it with FR-098's observed-order test plus an assertion that the adjudication context is not a descendant of the turn context. | Verified: the derivation is at `pkg/agent/goal_triggers.go:445`, inside a function the §5 table assigns to E8, while the requirement to change it belongs to E13's mechanism. Nobody owned it. E13 ships the deferred dispatch, the context is already finished, every claim-triggered adjudication returns Unavailable — and E13 has simultaneously deleted the claimless path, so nothing adjudicates at all. |
| **R-10** (BL-5, CON-07) | blocker | `pkg/agent/loop.go::runAgentLoop`, wave E13's region text | **Rewrite E13's `loop.go` region as:** *"`runAgentLoop`'s body — **add** a deferred-dispatch block after the `if opts.SendResponse && result.finalContent != \"\"` block closes, unconditionally, in a goroutine on `context.Background()`. `checkGoalLoopAfterTurn` and the `result.followUps` publish loop **do not move**. Plus the ADR-081 D3 base predicate. Nothing else."* FR-098's oracle becomes two cases: with final content, assert the observed order; **without** final content, assert the dispatch still happened. | Verified twice over. `checkGoalLoopAfterTurn` appends to `result.followUps`, which is iterated on the very next line — moving the call one line down silently stops the goal loop advancing, with no error and no failing test. And `PublishOutbound` sits inside a conditional, so a turn ending in a tool call rather than text never reaches it; a one-case test for FR-098 passes on the broken implementation. The word "reordering" in the current region text invites exactly the wrong edit. |
| **R-11** (BL-2) | blocker | `pkg/agent/loop.go` tool dispatch, judge FR-051/FR-052, wave E2 | **The Judge's tool-call cap refusal follows the ADR-066 D4 precedent verbatim and does NOT consult the denial ledger.** Return a `tools.ToolArgumentRefusalResult`-shaped result through `admitToolResult` with `IsError: true` and `EventKindToolExecSkipped`. Do **not** call `ts.recordToolDenial`. State this in E2's region text. `TestVerifierBudget_CapRefusalDoesNotKillTurn` must exercise **at least 11 refusals in one turn** — strictly more than `turnDenialBudget` — so a ledger-based implementation fails it. | Verified: `turnDenialBudget = 10` (`pkg/agent/tool_denial.go:41`) and every `recordToolDenial` site in `runTurn` calls `abortTurnForToolDenialBudget` on exhaustion. The Judge's cap is 25. Share the counter and the Judge's turn dies at refusal 10, violating FR-052's *"the turn MUST NOT be killed mid-flight"* — while the named test, using a cap of 3, still passes. The correct precedent sits ~60 lines away in the same function with a comment saying *"the ledger/quarantine machinery is deliberately not consulted"*. |
| **R-12** (BL-3) | major | `pkg/coreagent/core.go:2567 NewCustomAgentToolsCfg`, wave E1 | **Change E1's region text from "the six per-agent tool-policy seed maps" to "the six per-agent tool-policy seed maps **and `NewCustomAgentToolsCfg`**".** Every new tool name (`goal_claim`, `browser_handover`) gets an explicit, intended entry there. Add a test asserting that for each new tool name, `NewCustomAgentToolsCfg().Builtin.Policies[name]` is an explicitly-set value and not the `denyAllThenOverride` floor. | Verified: there is a seventh per-agent policy map at `pkg/coreagent/core.go:2567`, built with `denyAllThenOverride`, and it is named in **no** delivery document. A missing key is not an unknown key, so `validateOverrideKeys` does not fire and `ValidateToolPolicyCoverage` passes at `deny`. Every operator-created agent would silently resolve the new tools to `deny`. The map's own `set_goal` comment shows this exact omission has already been caught by hand once. |
| **R-13** (GC-01, GC-02) | major | `pkg/agent/goal_triggers.go:65-73`, waves F1/E8/U2 | **Assign the `goalPill*` constant block to E8 as a named region:** *"`pkg/agent/goal_triggers.go` lines 65-73, the `goalPill*` constant block — add one constant per new `GoalStatusFrame.state` value F1 introduces, spelled byte-identically to the contract enum."* **And add a Go test in E8 asserting every `goalPill*` constant is a member of the generated `GoalStatusFrame` state enum.** That test is E8's acceptance condition, not optional. | Verified: nine hand-written untyped constants, zero mentions of `goalPill` in this plan or the goal spec, and four waves write that file with regions the plan calls exhaustive. F1 widens the enum, U2 renders five new states, and nothing in Go can ever emit them. Worse, `state` is a **required** field on a `.strict()` zod frame, so one mis-spelled literal (`cas_loss` vs `judge_cas_loss` — two spellings one line apart in C-39) drops the **entire** goal_status frame at the SPA edge and blanks the goal card, with `make verify-contracts` green. |
| **R-14** (GC-03) | major | `contracts/components/schemas/Goal.yaml:119-136`, C-05 and OQ-1 | **Restate C-05 and OQ-1 as a RENAME, not a widening:** `done → met`, `failed → exhausted`, plus the new `defining` and `expired`. **And give the generated `GoalState` type a real consumer in S1's commit** — `pkg/goal`'s persisted state field typed as `generated.GoalState`, or an S1 test asserting `pkg/goal`'s constants equal the generated ones. Update goal spec FR-028's parenthetical, which still names `done`/`failed`. | Verified: the shipped enum is `[active, done, failed, cleared]` and `grep` for any consumer of `generated.Goal`, `generated.GoalState` or the four consts outside the generated tree returns **nothing**. `pkg/api/generated/contract_test.go` has zero Goal references. So S1 can write `StateDone = "done"` — the vocabulary FR-028 itself still uses — and nothing anywhere catches it until a real goal terminates in front of a user. |
| **R-15** (GC-04) | minor | `contracts/asyncapi.yaml`, wave F1 | **Replace "both GoalStatusFrame copies" in F1's brief with the literal inventory:** *"The AcceptanceCriterion shape has FOUR homes — `contracts/components/schemas/AcceptanceCriterion.yaml`, `contracts/components/schemas/AcceptanceCriterionInput.yaml`, and TWO inline duplicates inside `contracts/asyncapi.yaml` (`GoalStatusFrame.criteria.items` and `GoalStatusFrame.dod.items`). `GoalStatusFrame.yaml` uses `$ref` and needs no edit."* After regenerating, confirm `grep -c '^\s*- unmet' contracts/asyncapi.yaml` still returns 2. | Verified: `grep -c '^\s*- unmet'` returns 2 for `asyncapi.yaml` and 1 each for the two schema files. `GoalStatusFrame.yaml` `$ref`s and contains no copy. An agent told to mirror into "both GoalStatusFrame copies" opens that file, sees a `$ref`, correctly concludes there is nothing to do, and leaves the two inline copies stale — and the generated zod comes from `asyncapi.yaml` only. The copies are in sync today, so there is no pre-existing drift to hide behind. |
| **R-16** (GC-05) | minor | `pkg/config/config.go` BrowserConfig, wave E2 | **Use exactly:** `` ControlIdleReleaseSec int `json:"control_idle_release" env:"OMNIPUS_TOOLS_BROWSER_CONTROL_IDLE_RELEASE"` ``, default 900. | Verified: the two sibling fields drop the `Sec` suffix in both the JSON key and the env var — `LeaseWaitSec` is `json:"lease_wait"` / `OMNIPUS_TOOLS_BROWSER_LEASE_WAIT` (`pkg/config/config.go:3780`) and `IdleTTLSec` is `json:"idle_ttl"` / `OMNIPUS_TOOLS_BROWSER_IDLE_TTL` (`:3813`). This is an operator-facing config key; once shipped it is not freely renameable. |
| **R-17** (FG-01, BS-1) | blocker | every wave's write-set; §7's exit proof | **DONE — the pass was run, not prescribed (2026-09-11).** Every test file named in the three specs' test plans now resolves to exactly one wave's write-set in §3, or is recorded below as deliberately unwritten. **The census:** 127 distinct test-file basenames are named across the judge spec's Test Matrix, the goal spec's TDD plan and the browser spec. **58 of them appeared nowhere in this plan.** All 58 are now placed: **50 assigned to a wave**, **3 retired**, **1 relocated**, **4 recorded as unwritten regression canaries**. **The rule applied, in priority order:** (a) the wave whose *Delivers* column carries the FR the test proves; (b) where that wave is forbidden the package, the oracle moved to the wave that owns the package rather than to a new T-wave — `pkg/agent/verifier_audit_adr084_test.go` (FR-084) went to **T4**, not E0, because E0's *Must NOT touch* column bars `pkg/agent/**`; (c) a test proving a requirement retired by D-E or D-F was marked RETIRED rather than assigned. Throughout: **the wave that changes the behaviour owns the test that proves it.** **The 50 assignments, by wave** — E0 +3, E1 +5, E2 +3, F2 +1, S1 +2, E4 +1, E5 +2, E6 +1, E8 +1, E9 +7, E10 +6, E11 +1, E12 +4, E13 +2, E14 +1, T4 +1, B123 +2, U4 +2, U5 +4, U6 +1. Eight of these are **alias resolutions**, where the spec's name and this plan's name were two names for one file: the row now states *ONE file, this name* so no agent creates the second (`verifier_budget_test.go`, `verifier_adjudication_test.go`, `goal_record_wiring_test.go`, `goal_terminal_transition_test.go`, `judge_evidence_tiers_test.go`, `verifier_provenance_test.go`, `verdict_projection_test.go`, and E12/T3's two distinct `task_executor_goal*` files, deconflicted in both rows). **E11's specific hole is closed.** E11 delivered the rubric rewrite with an empty test write-set; it now owns `pkg/coreagent/judge_rubric_adr084_test.go`, and **both E1's row and E11's row state the split explicitly** — E1 owns `pkg/coreagent/core_test.go`, E11 owns the rubric test and nothing else in that package. Two waves never write one test file. **3 RETIRED, not assigned** — all three are judge spec §M, "the mixed state", an upgrade scenario: `pkg/gateway/judge_rubric_selfcheck_adr084_test.go` (**D-F**, reinforced by **D-H**: the self-check drops entirely, fresh-install half included), `pkg/gateway/judge_soul_migration_adr084_test.go` (**D-F** — JUDGE-FR-080), `pkg/coreagent/judge_rubric_history_adr084_test.go` (**D-F**, and already cancelled by ADR-084 rev 4 — JUDGE-FR-041–FR-048). **No wave creates any of the three, ever.** **1 RELOCATED** — the goal spec's `src/lib/api/generated/asyncapi-criterion-status.test.ts` (GOAL-FR-042) cannot live where the spec names it, because **T1's own *Must NOT touch* column bars a hand-written test in the generated tree**. It is the same oracle as T1's existing `src/lib/api/criterionStatusEnum.test.ts`; F1's write-set now carries the carve-out note so nobody re-creates it under `generated/`. **4 UNWRITTEN REGRESSION CANARIES** — the goal spec's own must-stay-untouched table names these, and **no wave writes them by design**; they are owned by §7's exit proof, which must show each still passing unchanged: `pkg/agent/subturn_target_identity_test.go` (ADR-032 delegation identity), `pkg/entity/flock_isolation_test.go` (the POSIX-only cross-process guarantee `pkg/goal/lock_test.go` copies — named in S1's row as a read-only precedent), `pkg/gateway/rest_default_agent_singleton_test.go` (the gateway-config-untouched canary), `src/components/workspaces/TaskCard.goalLoopStatus.test.tsx` (the `attempt N/M` label and its default fallback). **Widening a wave's write-set to "fix" one of these is a regression, not a repair.** **Disjointness re-checked after every edit:** no two waves in the same round name the same test file. The only two near-collisions the pass created were caught and deconflicted in both rows — E1/E11 in `pkg/coreagent`, and E12/T3 over `task_executor_goal_test.go` vs `task_executor_goal_loop_test.go`. **Still standing from the original resolution, unchanged and already applied: the 19 existing test files that reference the `SessionMeta.Goal*` fields are each added to the write-set of the wave that re-points their production file** (`pkg/agent/*` → E8 or E12; `pkg/tools/set_goal_test.go` → E12; the four `pkg/session` files → S6; `pkg/config/planning_test.go` → E6). **And §7's per-round exit proof gains `go vet -tags goolm,stdjson ./pkg/session/ ./pkg/agent/ ./pkg/tools/ ./pkg/config/`** — `go vet` compiles test files, `go build` does not. | Verified by a different measurement than the lens used: `grep -rlE "GoalCondition|GoalRoundsUsed|..." --include="*_test.go" pkg/` returns **19** files, and **zero** of them are named anywhere in this 1174-line plan. Standing rule 7 forbids a wave from writing outside its write-set, so round 7 closes on a tree whose test binary will not link, in three packages, and the agent that hits it is instructed to stop and report rather than fix. The lens's wider counts ("30 of 44 judge test files") were not re-verified — see Dropped, D-2 — but the defect is confirmed and it is structural. **Re-measured after the pass (2026-09-11):** of the 127 distinct test-file basenames named across the three specs, **zero now appear nowhere in this plan** — 50 previously-missing files were written into a wave's write-set, 3 marked retired, 1 relocated, 4 recorded as unwritten canaries owned by §7, and the 19 `SessionMeta.Goal*` files are each named in E6/E8/E12/S6. The numbers in the sentence above describe the defect as it stood **before** the pass and are left unedited for that reason. |
| **R-18** (FG-02, FG-04) | blocker | waves E3, G5, E12 | **E3 is not deletion-only.** Add `pkg/agent/judge_no_inferred_check_adr084_test.go` to E3's write-set and make its definition of done *"five deletions AND one new test asserting zero shell executions for a prose/artifact criterion, landing in the same commit"*. **JUDGE-FR-110 moves from G5 to E12**, with `pkg/tools/set_goal_adr084_test.go` added to E12's write-set; G5 keeps the merge-guard half only. Restate every guard row in §3: *"a guard is a supplement to a behavioural test, never the delivery of an FR."* | Verified: `pkg/agent/judge_artifact_criterion_test.go` exists and E3 deletes it; the replacement test is in no wave's write-set. That trades five real behavioural assertions for `scripts/check-no-inferred-check.sh`, a grep for five symbol names that E3 itself deletes — green from the moment it lands, forever, on any implementation. The judge spec's own rewrites table already requires *"every rewrite must land in the same commit as the behaviour it re-covers"*. |
| **R-19** (FG-05, FG-06, FG-07, FG-08) | major | every guard wave (G1–G7, B9) | **Four changes, all mechanical.** (a) Replace every *"matching the `check-no-goal-confirm-gate.sh` precedent"* citation with *"matching `scripts/check-no-orphan-turn-watchdog.test.sh`"*. (b) Tighten G1's runner contract from *"has a proof-of-failure path"* to *"the companion must exercise the guard against at least one planted offender and at least one clean tree, and print the guard's observed exit code for each"*. (c) `scripts/check-single-verdict-projection.sh` becomes a **cardinality** check: exactly one non-test met/unmet assignment, and it must be inside `pkg/agent/verdict_projection.go` — red on zero as well as on two, with both rows in its `.test.sh`. (d) `scripts/check-no-browser-turn-park.sh` clause (c) is scoped to *"these named functions must not appear in `pkg/tools/browser/` or `pkg/agent/browser_deferral.go`"*, with the symbol list handed over by B123 lane 3 before B9 starts, and a header line recording that reachability is out of scope. Add E0 and E11 to G3's `dependsOn` and require its `.test.sh` to include one fixture per closure. | Verified: `scripts/check-no-goal-confirm-gate.test.sh` does not exist — the cited template is the one guard in the repo that demonstrably lacks the companion it is cited for, and C-93 already lists it on the no-self-test exemption. `scripts/check-no-orphan-turn-watchdog.test.sh` does exist and does plant offenders. `cancelStream` under `src/components/browser/` appears today **only** in a test file, so a naive grep-based clause (c) is already near-empty and B5 empties it entirely. |
| **R-20** (UNDER-07) | major | `pkg/config/config.go` / `pkg/config/planning.go`, wave E2 | **Pin the four judge config values now, following the shipped browser-block naming convention** (Go field keeps the unit suffix, JSON key and env var drop it): `JudgeTurnTimeoutSec` / `judge_turn_timeout` / `OMNIPUS_PLANNING_JUDGE_TURN_TIMEOUT`, default 420, ceiling 900. `JudgeMaxToolCallsPerAdjudication` / `judge_max_tool_calls` / `OMNIPUS_PLANNING_JUDGE_MAX_TOOL_CALLS`, default 25, ceiling 60. `JudgeMaxToolBytesPerAdjudication` / `judge_max_tool_bytes` / `OMNIPUS_PLANNING_JUDGE_MAX_TOOL_BYTES`, default 2097152, ceiling 8388608. `JudgeMaxTokensPerAdjudication` / `judge_max_tokens` / `OMNIPUS_PLANNING_JUDGE_MAX_TOKENS`, default 120000, ceiling 480000. All four live in `PlanningConfig`. **FR-050's "reject (or clamp with a WARN)" resolves to CLAMP with a WARN** — boot never fails on an out-of-range judge number. | Verified: `grep` for any existing judge config key in `pkg/config/` returns nothing, so all four are new and there is no precedent to match except the browser block. These persist in the operator's `config.json`; a later rename is a migration, not a refactor. "Reject" and "clamp" are two different products and both are testable, so leaving it unstated guarantees two agents ship two behaviours. |
| **R-21** (UNDER-02) | blocker | goal spec FR-025, `pkg/config/planning.go:195`, waves E6 and E8 | **Make FR-025 additive.** E6 adds `EffectiveGoalMaxRoundsWithOverride(override *int) int` and leaves the zero-argument `EffectiveGoalMaxRounds()` in place delegating to it with `nil`. The two call sites at `pkg/agent/goal_loop.go:217` and `:343` switch to the override form in **E8**, which already owns that file and already reads the goal record through E4's seam. Record this as a two-wave chain E6 → E8 in §5. **Amended by D-E (2026-09-11): do not build it.** There is no per-goal budget override any more, so nothing can write one. `EffectiveGoalMaxRoundsWithOverride` is **not** created; `EffectiveGoalMaxRounds()` stays exactly as it is, reading the one global setting, and the two call sites in `pkg/agent/goal_loop.go` are left alone. The E6 → E8 chain this row created lapses with it. See **NQ-2** in §8 if a persisted per-goal number is still wanted for some other reason. | Verified: `EffectiveGoalMaxRounds` has exactly two non-test callers, both in `pkg/agent/goal_loop.go`, a file E6 may not write. `pkg/config/planning.go` is not in §5's shared-file chain table at all, so nobody sequenced this. An additive signature removes the cross-wave compile break entirely. |
| **R-22** (UNDER-03, OPS-F6) | blocker | goal spec FR-049, `pkg/agent/goal_loop.go:176/206/593`, wave S4 | **State the predicate in FR-049:** the `"goal"` admission counter returns the number of goal records with `owner_kind == session` **and** `state == active`. The definition phase and every terminal state count 0. Task-owned goals are excluded by `owner_kind`, which delivers MV-10 for free. **And give E8 explicit ownership of the `Admit`/`Release` pairing**: since the counter now reads the record store, the `pe.Admit("goal")` calls at `pkg/agent/goal_loop.go:176` and `:206` and the `pe.Release("goal")` at `:593` are all deleted together in E8's commit. Add a T3 oracle that creates cap+4 **terminal** session-owned goals and asserts the counter reads 0. **Re-homed by D-F (2026-09-11):** wave S4 is retired, so the `RegisterActiveCounter("goal", …)` closure in `pkg/gateway/gateway.go` belongs to **E11** (round 4). E8 still owns the `pe.Admit("goal")` / `pe.Release("goal")` deletion, in the same round, in a different file. The predicate itself is unchanged. | Verified: the slot is admitted at two sites and released in exactly one, inside `clearGoal`'s shared body — and the goal spec deletes `clearGoal` as a terminal mechanism entirely. Today's predicate only self-clears because `clearGoal` zeroes the fields; a retained terminal record still carries its condition, so the obvious port counts every goal ever created, forever, and the cap wedges at 16. |
| **R-23** (UNDER-04) | blocker | `pkg/session/daypartition.go:305 TranscriptEntry`, waves S2 and B123 | **Fold the persisted field into S2** (round 2, already additive on that file): add `` SystemSubtype string `json:"system_subtype,omitempty"` `` to `session.TranscriptEntry`, with the single legal value `"browser_handover_notice"`. Note in S2's row that this is a B123 dependency, not goal work. B123 then only writes and reads it. The deterministic notice id rides the existing `TranscriptEntry.ID`. | Verified: `TranscriptEntry` is at `pkg/session/daypartition.go:305` and carries no `SystemSubtype` today. FR-043a forbids prefix-matching prose and requires a dedicated subtype; that field must live in a package no browser wave may write. OQ-25 debated only the wire field name on `Message.yaml`, never the persisted one. |
| **R-24** (UNDER-05) | major | judge FR-093, goal FR-016, waves E8/E12/E13 | **`blocked` is a durable value of the goal record's own state field**, declared by E8 in round 4 alongside the terminal transitions — not a seventh in-memory map on `goalTriggerState`. FR-016's suppression list gains one row: *"a goal whose record state is `blocked`"*, evaluated from the record. The SPA renders it from the record after a restart. It is cleared by the same operator-message path that clears `waiting_on_user`. | The state it is modelled on (`waiting_on_user`) is an in-memory map, so a gateway restart forgets it. FR-093 asserts *"Both park. Both suppress idle settlement."* Two waves in two rounds must agree on a field neither spec names; a durable record field is the only version that survives a restart and is observable by both. |
| **R-25** (UNDER-09) | major | goal FR-021, `pkg/tools/task.go` and `pkg/sysagent/tools/task.go`, wave E5 | **The rule binds at every creation surface, agents included** — ADR-086 D11 is operator-ratified and says so (*"extends it to the DoD"*). E5 adds a required `dod` array parameter to `create_task` in **both** `pkg/tools/task.go` and `pkg/sysagent/tools/task.go`, with the same rejection wording already used for `criteria`, and the same rejection on the update path. | D11 verified verbatim. The asymmetry would surface as a person unable to save an edit to a task an agent created — the worst place to discover it. |
| **R-26** (OPS-F1) | blocker | `src/components/calendar/CalendarEventSlideOver.tsx`, wave U5 | **Add `src/components/calendar/CalendarEventSlideOver.tsx` and `CalendarEventSlideOver.test.tsx` to wave U5's write-set**, with the same criteria and DoD editors U4 adds to the main create form. The API-side 400 (FR-047) **must not merge before U5 does** — record that as a hard `dependsOn` from the wave that lands FR-047 to U5. **OQ-F is ANSWERED (D-C, 2026-09-11): the gate applies to edits too, uniformly, on every surface** — the create form, the task detail panel, the calendar slide-over, the REST update path and the `update_task` tool. There is no creation-only carve-out and no exemption for an old task. | Verified: `CalendarEventSlideOver.tsx:345` calls `createTask`, and `grep` for `criteria|Criteri|dod` in that file returns **zero** hits. This plan's U5 row currently says both files are *"files no wave owns"*. Per `CLAUDE.md`'s retired-surfaces section the workspace Calendar is the only place scheduled and recurring work lives, so the first person to reschedule a recurring job after FR-047 lands gets a rejection with no field to fill in. |
| **R-27** (OPS-F5) | major | `pkg/agent/task_executor.go::adjudicateClaim`, wave E12 | **There are TWO trust-the-claim branches, not one. Both belong to E12.** The structurally-empty branch (`pkg/agent/task_executor.go:1174`) becomes a hard failure with the reason string `"no acceptance criteria and no judgeable text"`. The Judge-unregistered branch (`:1184`) becomes an explicit non-terminal *"cannot adjudicate"* outcome with **no round consumed**, matching EC-6's judge-unavailable rule — not a silent completion. | Verified: `adjudicateClaim` has two `completeTaskWithResult(..., true, ...)` calls that bypass the Judge, at lines 1174 and 1184. FR-022 says *"the trust-the-claim branch MUST be deleted"*, singular, and names only the first. The second logs a WARN and trusts the claim outright; no wave row mentions it. |
| **R-28** (CON-05) | major | C-25, nine call sites | **Replace C-25's "five places" with the enumerated nine, each with its owning wave:** `pkg/agent/goal_triggers.go:607` (E12); `pkg/agent/goal_loop.go:119`, `:471`, `:527`, `:783`, `:1039` (E8); `pkg/agent/goal_record_wiring.go:72`, `:440` (E8); `pkg/gateway/gateway.go:5090` (**E11** — S4 is retired, D-F). **And E4 deletes `session.UnifiedMeta.GoalCondition` outright once the seam re-points** — a compile error is the right tool here, because it converts every missed site from a silent wrong answer into a build failure. | Verified by direct grep: nine live non-test sites, not five. C-25's own closing sentence is the reason this matters — *"Missing one produces a silent wrong answer"*. Counting is not a job to leave to an agent mid-wave. |
| **R-29** (UNDER-11) | major | goal FR-052, waves E12 and T2 | **Specify it in FR-052:** the six trigger maps are only ever read or written for a goal in the **active** phase. A lookup for a goal with no bound session returns the zero value, and the keeper drivers must not be reachable for such a goal at all. On activation the maps start empty for that goal id; nothing is carried over, because nothing may have been written. Add a T2 oracle that puts a definition-phase task-owned goal in front of the quiet-window driver and asserts zero keeper actions. | FR-052's own last sentence admits the case is unspecified and then nobody specifies it. Re-keying from session id to goal id makes lookups possible during a window where they were previously impossible; "returns zero, and is never reached anyway" is the only answer that needs no new state. |
| **R-30** (UNDER-12) | ~~minor~~ **MOOT — D-F** | `contracts/components/schemas/Agent.yaml`, C-38, wave F1 | **Do not build any of this.** D-F retires judge spec §M (JUDGE-FR-077 – FR-080) and wave U3, so `rubric_capability` is never added to `Agent.yaml` in any shape. The original resolution is kept below, cancelled. ~~**Carry both booleans.**~~ `rubric_capability` becomes `{declares_evidence_source: boolean, instructs_quote: boolean, missing_sentinels: string[], disabled_controls: string[]}`. Presence of the object means at least one capability fact is false. U3 renders the union of `missing_sentinels` and `disabled_controls` and never infers from a single boolean. | FR-077 computes two facts it explicitly forbids collapsing, and FR-079(c) gates a real control on the second. A single boolean named after the first renders an install as capable while a control is silently off. This is a one-field change inside F1's round-1 commit and therefore free; after F1 it costs a second full regeneration. |
| **R-31** (UNDER-13) | minor | goal FR-002 vs `Goal.yaml` `binding_kind`, waves F1 and E14 | **Drop `plan` from `owner_kind` in F1 — two values only, `session` and `task`.** State in FR-040 that the plan path projects criterion statuses directly onto the plan member's own DoD list with **no goal record involved**, using the same explicit, logged no-op discipline FR-031 already requires for the ephemeral soft-tier criterion. Note it in E14's row so the projection's third site is not written expecting a store. | The shipped enum has three values and FR-002 admits two; nothing said whether the rename drops `plan`. Left unstated, the third projection site gets criteria with a projected status, no goal record, no retention rule and no de-union rule. |
| **R-32** (UNDER-14) | minor | goal FR-036, wave E14 | **State it in FR-036:** the projection applies only the criterion ids present in the verdict and leaves the others untouched. It never resets previously-decided statuses to `pending`. **And amend `contracts/components/schemas/AcceptanceCriterion.yaml`'s `status` description in F1** to read *"the outcome of the most recent verdict that mentioned this criterion"* — so the contract stops asserting a freshness the store does not maintain. No new per-criterion round field is added. | The alternative (a `judged_in_round` integer) is more than this delivery needs, and the description fix costs one line inside the round-1 contracts commit. |
| **R-33** (BS-5) | ~~major~~ **PARTLY LAPSED — D-F** | wave S4, `pkg/gateway/goal_orphan_boot.go`; G4's credit | **The boot-sweep half lapses with wave S4 (retired) — there is no sweep and no `goal_orphan_boot.go` header to write it into.** The other two halves survive untouched: the total lock order is `goalLock → taskFileLock → sessionLock → cacheMu` and no goal lock may be taken inside a session shard; and G4 is still **not** credited with GOAL-FR-008's verification half, so S1 still ships the acquire/release observation seam and its test. Original text follows. | **Add the lock order verbatim to S4's row and to the new file's header:** *"The boot sweep collects candidate session ids and releases every session shard BEFORE taking any goal lock. The two are never nested."* Document the total order as `goalLock → taskFileLock → sessionLock → cacheMu`. **Stop crediting G4 with GOAL-FR-008's verification half** — `go test -race` is not a lock-order checker. Instead, S1 ships the same exported-for-test acquire/release seam `pkg/session` has (its FR-101 `sessionLockAcquireFn`/`sessionLockReleaseFn` pattern), plus one test asserting no goal lock is held across a session acquire. | Verified by reading `pkg/session/unified_lock.go`, whose own comment says *"go test -race is not a lock-order checker — it reports nothing for an inversion that does not happen to deadlock in the run under test"*, which is precisely why FR-101's observation seam exists. C-26 closed the retention inversion and left S4 — the second place in the delivery that touches both lock classes — silent. Boot is single-threaded, so the inversion would ship green and only bite once S3's retention pass takes the goal lock first. |
| **R-34** (BS-6) | minor | wave S6 | **Correct S6's write-set**: `pkg/session/unified_meta_files_test.go` does not exist. S6's `pkg/session` test files are `goal_meta_greenfield_test.go`, `unified_meta_split_adr057_test.go`, `unified_listsessions_adr057_test.go` and `unified_stats_flush_adr057_test.go` (all four added per R-17). **And state goal.json's fate explicitly in S6's row:** ~~keep `u5ReadGoalFile` as the FR-051 legacy presence check (per R-07)~~ **— cancelled by D-F: FR-051 is retired, so delete `u5ReadGoalFile` as well, leaving no legacy reader behind**; delete `u5WriteGoalLocked` and the `writeGoal` branch of `writeMetaLocked`; leave the stale file on disk to be removed by S3's retention pass, never by a migration. | Verified: the named file does not exist, and after S6 removes the `Goal*` fields and S2 moves `PendingAskJSON` out, `u5GoalFile` has zero fields — so `writeGoal` is permanently false and goal.json would sit on disk forever, read by nothing, with nobody having decided that. An agent whose write-set names a non-existent test file creates it and never finds the four real ones. |
| **R-35** (BL-4) | major | `pkg/agent/loop.go::Close`, wave E13 | **Add a fourth bounded drain to `Close()` in E13's commit**, modelled on `waitDelegateAsyncDrain`: a `WaitGroup` plus a `closing` flag that stops new dispatches, with a 30-second budget. State it as an FR in this plan. Do **not** cancel instead of draining — FR-082 says a cancelled adjudication is discarded whole, so cancel-on-shutdown silently loses the verdict for a claim the operator already saw answered. | Verified: `Close()` already drains three classes of detached goroutine at `pkg/agent/loop.go:4166`, `:4180` and `:4188`, each with a doc comment explaining that an undrained one races temp-dir cleanup. FR-098 introduces a fourth — `context.Background()`, up to 420 seconds — and neither the judge spec nor this plan mentions shutdown. The failure shows up as a flaky panic in an unrelated package's cleanup, not as a judge test failure. |
| **R-36** (GC-02, cross-cutting) | major | all four generated trees, wave F1 | **Rule for every wave, added to §6:** no wave may write a wire-enum value as a bare Go string literal. Any Go code emitting a value that appears in a generated enum must either use the generated type or be covered by a test asserting the literal is a member of the generated enum's value set. F1's acceptance conditions include one such test per new enum it introduces. | The `state` field is required on a `.strict()` frame, so a single mis-spelled literal drops the whole frame at the SPA edge with every Go and contract gate green. Nothing on the Go side — not the compiler, not `make verify-contracts`, not `pkg/api/generated/contract_test.go` (which has zero Goal references) — can catch it. This is the general form of R-13 and it is the cheapest single rule in this section. |

### Findings dropped or downgraded

**D-1 — The plan's own OQ-10, "the single most important unanswered question in the delivery".**
Dropped as a question. The goal specification answers it in two places
(`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/docs/internal/specs/goal-entity-spec.md`
line 243 and line 1153). The original analyst read sections A–E; the assumptions table sits outside
them. Wave S1 is unblocked — see R-04.

**D-2 — "Thirty of the forty-four judge test files and seventeen goal test files are in no
write-set."** The counts were not re-verified and are not relied on. The underlying defect is
confirmed by an independent measurement (19 existing test files that reference the deleted session
fields, none named in this plan), and R-17 **has now fixed it file by file rather than by a count** — the pass was run on 2026-09-11, not left as an instruction; see R-17 for the census and the per-wave assignments. Do
not quote the 30/44 figure.

**D-3 — The claim that ADR-085's browser hold can strand a goal forever (CON-08).** Confirmed only
at second hand. The browser specification's FR-026a and FR-031a were read by one lens and not
re-checked here; what *was* re-checked is the half that makes it dangerous — that after judge FR-095
the calendar brake is the only terminator, and that `goalIdleExpirySweep` cannot see a task-owned
goal. Treated as a real interaction and put to the operator as **OQ-D**, not resolved silently.

**D-4 — The premise behind UNDER-12's `rubric_capability` shape.** `grep -rn "rubric_capability"
contracts/` returns nothing: the field does not exist yet, so C-38's single-boolean shape is a
plan-internal decision rather than a shipped one. The finding's severity drops accordingly. R-30
stands anyway because the fix is free inside F1's commit and expensive after it.

**D-5 — The judge specification's "NOTHING IS IMPLEMENTED — greenfield" framing.** Dropped as
false. The Judge, the verifier adjudication path and the agent seeding are all shipped code; the
specification correctly labels every one of those rows *modify*, not *new*. Any wave brief that
repeats the greenfield framing for ADR-084 must be corrected before dispatch.

**D-6 — GJ-3 (`Media` missing from the `toolResultAdmission` enumeration).** Confirmed but
downgraded to a one-word edit folded into R-08. The wave modifies the struct rather than recreating
it, so the agent sees the field the moment it opens the file.

### Operator answers (round 1) — all six settled

Six questions went to the operator. **He answered all six on 2026-09-11.** The full answers, and
everything each one changes in this plan, are in **§8**. Nothing in this section may be read as still
open. The short form, in the order the questions were asked:

| was | question, in one line | answer |
|---|---|---|
| **OQ-A** | A goal whose agent works but never says "done" — end it sooner, or let it sit for seven days? | **Let it sit. Seven days stays, the nudge ladder gets no bound.** Fix the cause instead: the keeper's re-post and nudge text must tell the agent to mark the work complete with the claim tool. *(D-A — E8, E13, T2.)* |
| **OQ-B** | When the reviewer says "done" but its own justification does not hold up — "not done", or a log line beside a standing "done"? | **The "done" stands.** The Judge judges as a human would and has the authority; it must report the missing or failed evidence and justify why common sense says it is done. Grounding controls are reporting, never gates. *(D-B — retires judge spec §D: JUDGE-FR-024, FR-025, FR-026 as a rewrite; FR-027 survives as reporting. Corroborated by GOAL-FR-038 and GOAL-FR-039.)* |
| **OQ-C** | On upgrade, does the system read the old goal file once, and does it rescue a waiting question card? | **Neither — there is no upgrade at all.** *(D-F — the whole upgrade question is withdrawn for all three designs.)* |
| **OQ-D** | If someone takes the browser and leaves the panel open, does the assistant ever get it back? | **The person keeps the wheel until the agent gets a new prompt — and the agent stops waiting for it.** If other work needs a browser, the agent opens a new tab. The stand-down is per tab. *(D-G — B123, B5, B6.)* |
| **OQ-E** | Does a goal set in chat get a control to give it more room, or only a goal on a task? | **Neither.** One global goal-tries setting under Settings → Performance governs both kinds identically; no per-goal override exists. *(D-E — retires GOAL-FR-046 and GOAL-US-8; answers OQ-2; shrinks U6; removes `GoalBudgetField` from U5.)* |
| **OQ-F** | Must a task carry criteria and a definition of done to be *edited*, or only to be *created*? | **Both. Uniformly, on every surface.** *(D-C — E5, U4, U5, and the calendar slide-over.)* |

**Three new questions came out of the answers, and two are now closed** — **NQ-1 CONFIRMED (D-J)**:
GOAL-FR-049's counter closure re-homes to **E11**, making `pkg/gateway/gateway.go` single-owner.
**NQ-2 CONFIRMED (D-K)**: GOAL-FR-025's persisted per-goal override and
`EffectiveGoalMaxRoundsWithOverride` are retired, and `EffectiveGoalMaxRounds()` keeps its current
signature. **NQ-3 remains OPEN** (whether the hand-edited-rubric case should still be reported after
judge spec §M is retired) and is **awaiting the operator**; no wave waits on it, because the plan
implements the retirement D-H instructed and any later reporting is additive. All three are stated
in full in §8, each with the wave it names.
