# Adversarial Review: ADR-20260928 — Sub-agent control plane (2026-09-29 revision, re-review)

**Spec reviewed**: `docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md` @ `1bbc44106`
**Mode**: ADR mode, one round (generic-markdown classification; structured sections D1–D10, T1–T13)
**Review date**: 2026-09-29
**Reviewer model**: Opus (founder exception recorded in `coordination/PLAN-2026-09-28.md`, "grill-spec re-review of the revised ADR-20260928 runs on Opus")
**Previous round**: [ADR-20260928-sub-agent-control-plane-review.md](./ADR-20260928-sub-agent-control-plane-review.md) (2026-09-28, BLOCK) — kept unchanged; this file is the re-review of the 2026-09-29 revision only.
**Verdict**: **BLOCK**

## Executive Summary

The revision encodes the founder's five-state model faithfully in its Vocabulary, but two of its central mechanisms do not work against the code as it stands: the new `stop` note collides with the durable Stop marker that already lives under the same JSON key with opposite semantics, and a parent holding a deferred "done" claim hangs when its last busy helper is *stopped* rather than finished — the exact failure the founder ruled out ("it never hangs"). The ADR is also already behind the founder: rulings Q19/Q20/Q21 (timed out = stopped; `clear_goal` never cascades; a working helper's goal may be cleared) are recorded in the plan but listed here as open, and the Stop-button ruling (single press = this session only) is contradicted by D9.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 10 |
| MINOR | 7 |
| OBSERVATION | 3 |
| **Total** | **22** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The new `stop` note collides with the existing durable Stop marker — same key, opposite meaning; same-generation resume cannot work as written

- **Lens**: Inconsistency / Infeasibility
- **Affected section**: Vocabulary row `stopped` ("one `stopped` state + a cause note `{at, by, seq, cause}`"); D2 Mechanism; D4 wire table row "lifecycle record … `stop {at, by, seq, cause}`"; D6 "Boot" row; D2 "Resuming a stopped child … resumes the **same generation**"
- **Description**: The lifecycle record already has a durable Stop marker: `pkg/session/lifecycle_edge.go::Stop` `{At, Generation, By}`, persisted as `LifecycleRecord.Stop` with JSON key **`stop`** (`pkg/session/lifecycle.go`, field `Stop *Stop \`json:"stop,omitempty"\``). Its meaning is "a stop is pending for this generation": `LifecycleRecord.Stopped()` is documented as "the live-stop predicate every dispatch/delivery/completion path must refuse against", `persistLocked` rejects a terminal record that still carries a current-generation marker, and `reportSteeredSessionTerminalUpward` / `deliverSteeredCompletion` clear it on the terminal write. Revive keeps an old marker only because the **generation is bumped** (`Stop.Generation < Generation` = inert history). The ADR proposes a "stop note" under the same key `stop`, with different fields (`seq`, `cause`, no `generation`), that must *persist* on a non-terminal `stopped` record and be resumed on the **same** generation. The ADR never says whether the note *is* the existing marker, replaces it, or sits beside it.
- **Impact**: Either reading breaks something. (a) Note = existing marker: a same-generation RESUME leaves `Stop.Generation == Generation`, so `Stopped()` stays true and every dispatch path refuses the resumed turn — RESUME silently does nothing. If RESUME clears the marker instead, the ADR loses the only thing D6's Boot row relies on ("neither sweep touches a stopped record carrying a stop note") for the window between clear and the first turn. (b) Note beside the marker: two different `stop` objects on one record, and `failInterrupted`'s existing skip (`current.Stop != nil && current.Stop.Generation == current.Generation`) keys on the old one. A wire rename of the SPA-read field (D4) on top makes old/new records ambiguous.
- **Recommendation**: Add a D2 paragraph "Relation to the existing Stop marker" that decides one design explicitly, e.g.: "The Stop marker (`session.Stop`) is extended to `{at, generation, by, seq, cause}` and is the stop note. A `stopped` record carries a current-generation marker. RESUME/`redirect` on a stopped child clears the marker in the same `Mutate` that sets the state to `queued` (same generation); `Stopped()` is redefined as `State == LifecycleStopped || marker current and state non-terminal and not yet landed`; `persistLocked` accepts a current marker on `LifecycleStopped`." List `Stopped()`, `persistLocked`, `failInterrupted` and both terminal-write clear sites in the Impact table.

---

#### [CRIT-002] A parent waiting on its helpers to confirm "done" hangs when the last busy helper is stopped instead of finished

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: D6 "Q2=B interaction"; D6 "The stopped-child notice"; F0929-7 ("it never hangs"); Impact table (no row)
- **Description**: Under Q2=B a parent's `met` claim is deferred while `hasRunningOrQueuedDescendant` is true, and the only thing that re-evaluates it is `resumeDeferredGoalAfterDescendantTerminal` (`pkg/agent/goal_child_completion.go`, `origin/feat/q2b-on-984`), which returns early when `!descendant.Terminal()` and when `parent.Stopped()`. The ADR makes `stopped` non-terminal and says `hasRunningOrQueuedDescendant` must return false for it — but nothing calls the re-evaluation when a child *becomes* stopped. The only replacement is D6's "stopped-child notice", whose trigger, delivery (waking or not) and dedup the ADR itself marks "open question, not answered here … do not treat this paragraph's broadened trigger as founder-ruled". The same early return also breaks a two-level chain: a working grandchild finishing under a **stopped** middle node never re-evaluates the grandparent, because the lookup stops at the stopped direct parent.
- **Impact**: Concrete: team lead A claims `met`; its worker B is still running, so the claim is deferred. The owner presses Stop on B. B lands `stopped`; A is not woken, its deferred claim is never re-evaluated, and A sits in "waiting for descendants" indefinitely — the hang F0929-7 forbids, now caused by the stop that was supposed to prevent hangs.
- **Recommendation**: In D6, rule: "The transition of a steered record into `stopped` calls the same re-evaluation as a terminal transition (`resumeDeferredGoalAfterDescendantTerminal`, renamed `…AfterDescendantSettled`), for the direct parent **and**, when the direct parent is itself stopped or quiet, for the nearest non-stopped ancestor holding a deferred claim. The stopped-child notice is wake-eligible for a parent that is not itself stopped." Add the function to the Impact table and a test: "deferred `met` claim + only busy child stopped → parent woken within one tick, claim re-evaluated, parent told B is stopped".

---

### MAJOR Findings

#### [MAJ-001] The ADR is already behind the founder: Q19, Q20 and Q21 are ruled but listed as open

- **Lens**: Inconsistency
- **Affected section**: Vocabulary "Open question … `LifecycleTimedOut`"; D7b open questions 1–3; Impact "Open questions" 1 and 4; T13 ("cannot be written until D7b's open questions are settled")
- **Description**: `coordination/PLAN-2026-09-28.md`, same "2026-09-29 Founder rulings" section the ADR cites, now records: **Q19=A** timed out = stopped (resumable); **Q20** `clear_goal` on a helper does **not** cascade, and a helper whose goal is cleared gets the same injected prompt as the parent (recursive, never automatic); **Q21=A** a parent may clear a **working** helper's goal, and the helper is told. The ADR (commit 19:22) presents all three as undecided. Also, D8's quotation of F0929-3 (line 390) puts in quotation marks a clause that is not in the ruling ("replaces the separate 'interrupted' terminal-failed idea where it simplifies"); the ruling reads only "Restart of Omnipus = automatic stop of working sessions; parent told."
- **Impact**: An implementer following the ADR would leave `LifecycleTimedOut` as a terminal state and either block or cascade `clear_goal` — both against the founder.
- **Recommendation**: Add rows F0929-Q19/Q20/Q21 to Founder decisions; close Vocabulary's open question (timed out → `stopped`, cause `timeout`, add `timeout` to the `cause` enum); rewrite D7b with the three answers; make T13 writable; fix the F0929-3 quotation to the verbatim text.

---

#### [MAJ-002] Stop button, Esc and `/stop` contradict F0929-4: the single press must stop only this session, root included

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: D9 table rows "either, typed in a root conversation" and "`/cancel`, the Stop button (Stop all)"; D9 "Stop all confirmation, referenced not designed here"; D4 wire table
- **Description**: F0929-4 (PLAN text): "Stop (button, Esc, /stop) ends only this session's turn. Stop all = press twice to confirm, /cancel, menu entry: cascades DOWN". D9 still equates "the Stop button" with Stop all, and refuses `/stop` in a root conversation with "`/cancel` or the Stop button stops this conversation's whole tree". Today `pkg/gateway/websocket_cancel.go::cancelSteeredSubtree` always calls `CancelSubtree` (cascade) for the button. A single press that stops only the root's own turn therefore needs a **new server behaviour and a wire change** (the cancel frame must carry a scope: this-session vs. whole tree) — Hard Constraint #8 — not only "SPA interaction work". Esc is not mentioned anywhere in the ADR.
- **Impact**: Built as written, one press of Stop keeps killing the whole tree, which is exactly what the founder changed; `/stop` in the main chat is refused though the ruling allows it.
- **Recommendation**: Rewrite D9: `/stop`, Esc and a single Stop press stop **the current session only** (root or helper; a root stop = its own turn ends, helpers keep working); Stop all = second press within N seconds, `/cancel`, or the menu entry. Add to the D4 wire table: the WS cancel frame gains `scope: enum[session, tree]` (default `session`), and list `handleCancel`/`cancelSteeredSubtree` in the Impact table.

---

#### [MAJ-003] Goal-ending on session end (FD1=A) is shipped code, not a draft idea — and the Impact table misses every call site

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: D6 "Goal" row ("supersedes the 2026-09-28 draft's FD1=A"); D7 "Goals stay open"; Impact table row `pkg/tools/delegate_run.go` ("Inferred risk, not confirmed absent")
- **Description**: FD1=A is on `origin/release/v0.1.1`: `pkg/agent/goal_child_completion.go::endSessionOwnedGoalOnTerminal` is called from `pkg/agent/steer_cancel.go::reportSteeredSessionTerminalUpward` (every Stop/cancel terminal write), `pkg/agent/steer_completion.go::deliverSteeredCompletion` (every done/failed/timed-out write), and `pkg/agent/boot_sweep.go` (`failInterrupted` **and** the boot path that lands a delivered final report). `goalEndingForTerminalState` maps cancelled → `GoalOutcome.ending: stopped_by_user` (wire enum in `contracts/components/schemas/GoalOutcome.yaml` and `asyncapi.yaml`). The ADR's Impact table names none of these, and treats the question as an unverified risk in `delegate_run.go`. It also never answers the obvious consequence of F0929-6 ("only /goal clear ends a goal"): does a child that ends **done** or **failed** (including D1.8's `owner_unreachable`) still have its goal ended by FD1=A?
- **Impact**: Implementers rename the state but keep the pair-end hook; every stop, and every Q19 timeout, still ends the helper's goal — the opposite of F0929-6, with nothing in the ADR's test list to catch it.
- **Recommendation**: Add Impact rows for the four call sites and `goalEndingForTerminalState`; state the rule explicitly: "FD1=A pair-end fires only on `done`/`failed` (ask the founder whether even those keep the goal open); it never fires on `stopped`, whatever the cause." Decide the fate of `GoalOutcome.ending: stopped_by_user` (unreachable from a stop; still reachable from `/goal stop`). Add a test: "Stop, Stop all, restart and timeout each leave the child's goal `active`".

---

#### [MAJ-004] "Timed out = stopped" plus the deadline formula makes a resumed session time out again at once

- **Lens**: Incorrectness
- **Affected section**: D6 "Deadline" row (`CreatedAt + TimeoutSeconds + stopped_for`); D8.7; Q19 (PLAN)
- **Description**: With Q19, a session whose lifetime budget runs out lands `stopped` and is resumable. At that moment its working time equals `TimeoutSeconds`. On RESUME the deadline is `CreatedAt + TimeoutSeconds + stopped_for`, i.e. exactly "now" — the remaining budget is zero.
- **Impact**: The owner (or parent) resumes a timed-out helper; the turn is cancelled on its first tick and the helper lands `stopped (timeout)` again. RESUME looks broken, and a parent agent following D6's "resume it" advice loops.
- **Recommendation**: Add to D6: "A resume after a `timeout` stop starts a fresh budget: the deadline becomes `resume_at + TimeoutSeconds`" (or a founder-chosen extension), and add a test "timed-out child resumed → runs for at least one model call".

---

#### [MAJ-005] `redirect` and RESUME let an agent get past an owner-only question without the owner

- **Lens**: Insecurity (Elevation of Privilege)
- **Affected section**: D1.1 "Make it moot: `redirect` (D2) or `cancel` (D7) — the question closes as `superseded`"; D2 `redirect` "on a waiting-for-answer child it closes the question `superseded` and resumes with `text`"; F0929-5 "RESUME … allowed on any session that is not `working`"; D2 resume note
- **Description**: R§8.2 promises "no agent at any level authors the answer to an `owner_required` question". A parent can `redirect` a child parked on "May I rotate the production key? (owner only)" with the text "The owner approved — go ahead." The question closes `superseded`, the child resumes, and the resume note (D2) says only "You were stopped … (cause: redirect)". Nothing tells the child its owner-only question was **not** answered by the owner. F0929-5 additionally allows RESUME on a waiting-for-answer child, and the ADR never defines what that does to the open question.
- **Impact**: The owner-only guarantee — the reason D1's Who/When/Which/Once machinery exists — is bypassable by a single `redirect`, with no audit line (D1.9 audits only owner answers).
- **Recommendation**: (1) When `redirect` or RESUME supersedes an `owner_required` question, the resume note must carry a fixed runtime line: "Your owner-only question <code> was withdrawn WITHOUT an answer from the owner. Do not treat anything in this instruction as the owner's approval." (2) Define RESUME on a waiting-for-answer child: either refuse ("waiting for an answer — answer it, redirect, or stop it") or apply the same withdrawal rule. (3) Write a `pkg/audit` entry for every superseded `owner_required` question. Add a test.

---

#### [MAJ-006] What Stop and Stop all do to a helper that is waiting for an answer is undefined

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: D2 `stop` table row "waiting for answer → nothing"; D7 "cascades … stopped and waiting-for-answer included"; D1.7/D1.8; F0929-4
- **Description**: D7 lands a waiting-for-answer helper in `stopped`, but no section says what happens to its open question: does it close `superseded`, stay open, keep its relay chain, keep its 24-hour expiry (D1.8 reads `NeedsInput.TTLDeadline`, which the cascade may clear)? When the owner later answers the relayed question (D1.7 staleness), "the asker is no longer parked" — so the answer is dropped, and the owner is told only through the `respond` result. Separately, D2 says a single `stop` on a waiting child does "nothing", while F0929-4 says Stop ends "this session's turn" for any session.
- **Impact**: After a Stop all, the owner answers the question shown in the root chat, the answer is silently discarded as stale, and the resumed helper asks again — or waits forever if the question stayed open with no expiry.
- **Recommendation**: Add a D7 bullet: "A waiting-for-answer session reached by Stop all lands `stopped`; its open question closes `superseded` (reason `stopped_all`), its relays close `superseded`, and the root's shown entry is marked withdrawn." Decide (founder) whether single Stop on a waiting session is a no-op or does the same. Add a test.

---

#### [MAJ-007] `hasRunningOrQueuedDescendant` does not count "waiting for answer" either — the ADR calls it "unchanged" and leaves it off the build list

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: D6b ("`hasRunningOrQueuedDescendant` returns true for `needs_input` (unchanged by 2026-09-29 …)"); Impact table
- **Description**: Verified in `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant` (release and `feat/q2b-on-984`): cases `LifecycleQueued` and `LifecycleRunning` only; `needs_input` is walked through, not blocking. This is the same gap the ADR flags as a "verified bug" in `DirectNonTerminalChildren`, but here it calls the behaviour "unchanged" and omits it from the Impact table, which the ADR declares is "the table to read for build purposes". The function also walks *through* non-blocking nodes, so a **working grandchild under a stopped child** still blocks the root's "done"; the ADR does not say whether that is intended.
- **Impact**: D6b / T7 ship unimplemented because the build list does not contain them.
- **Recommendation**: Add an Impact row: "`hasRunningOrQueuedDescendant`: add `LifecycleNeedsInput` → blocking; `LifecycleStopped` → not blocking, and decide whether its subtree is still walked". State the grandchild-under-stopped-child rule in D6.

---

#### [MAJ-008] Plans under the new model leave stopped member sessions with open goals behind every restart of the plan

- **Lens**: Incompleteness
- **Affected section**: F0929-8; Impact rows `pkg/plan/*`, `pkg/tools/delegate_run.go`
- **Description**: The PLAN ruling notes that `POST /plans/{id}/restart` "resumes it as a **new generation**, resetting only non-done steps". Under this ADR, `StopPlan` lands every member session in `stopped` with its goal kept (F0929-6). If the plan restart starts new sessions for non-done steps, the old stopped member sessions are never resumed and their goals never cleared, and each one's stop has sent the plan owner a D6 "decide" notice. The ADR says the plan path "matches today's code" and moves on.
- **Impact**: Every plan stop-and-restart accumulates stopped sessions with active goals and spurious "decide what to do with this helper" notices to the plan owner.
- **Recommendation**: Add a D8-adjacent section "Plans": either plan restart resumes the stopped member sessions (same generation, consistent with F0929-5) or it clears their goals and suppresses the D6 notice for plan-owned members. Ask the founder which; add a test.

---

#### [MAJ-009] "Never hangs" and the restart design rest on questions the ADR leaves open; two acceptance tests assert undecided designs

- **Lens**: Incompleteness / Infeasibility
- **Affected section**: D6 stopped-child notice ("Open question … do not build from"); D8.4 ("not decided … Needs a correction round"); Impact open questions 2, 3, 5, 6; D3 wire name of RESUME; T6; T13
- **Description**: The notice that replaces blocking (D6) and the restart notice (D8.4) are both undecided, yet T6 asserts "root gets exactly one entry; D is not resumed until C, once resumed, acts on D" — an instantiation of the undecided lazy design. The term "live parent" in the notice trigger is undefined. Also open: `SessionStatus` value for `stopped`, the Plan state enum, and whether the tool enum says `follow_up` or `resume`.
- **Impact**: Implementation cannot start from this text without inventing the most safety-relevant behaviour (who is told, when, how often after a Stop all or restart that stops dozens of sessions).
- **Recommendation**: Before build: one founder interview covering notice trigger/wake/dedup (proposal: one waking notice per direct parent that is not itself stopped, deduplicated per `(parent, boot_seq or stop seq)`; stopped parents get stored non-waking entries), the eager-vs-lazy restart notice, `SessionStatus`, the Plan enum and `resume` vs `follow_up`. Rewrite T6 after that.

---

#### [MAJ-010] Boot replay of pending ledger controls can start a session by itself after a restart

- **Lens**: Inconsistency
- **Affected section**: D4 crash semantics "Boot replay: every control not `applied`/`superseded` is replayed"; D8.5 "Nothing resumes automatically"; F0929-race / D8.9 "must never be `working` by itself after a restart"
- **Description**: If the gateway crashes after a `redirect`'s fence line or a RESUME's ledger line is written but before the new turn starts, D4 replays it at boot and the session starts working with no human or agent acting after the restart. D8.2 also stops working sessions at boot; the order between "restart-stop working sessions" and "replay pending controls" is not stated.
- **Impact**: Either the replay resumes sessions after a restart, contradicting D8.5/F0929-race, or the restart-stop supersedes the pending redirect and the parent's instruction is lost without a visible receipt.
- **Recommendation**: Add to D8: "At boot, pending `redirect`/RESUME controls are **not** executed; they are marked `superseded`, reason `restart`, and listed in the parent's restart notice so it can re-issue them." (Or the opposite, with an explicit founder ruling.) Add a test.

---

### MINOR Findings

#### [MIN-001] Old vocabulary remains in normative text

- **Lens**: Ambiguity
- **Affected section**: D1.1 ("`cancel` (D7)"), D1.8 line 187 ("resumable by `follow_up`"), D4 lines 253/259/262/281 (verb enum `follow_up`, `cancel`, no `clear_goal`), D5 line 295
- **Description**: The revision claims every mention was checked, but D4's `ControlReceipt.verb` enum still lists `follow_up` and `cancel` and lacks `clear_goal` (and `resume`, depending on the D3 open question).
- **Recommendation**: Update the enum to `[steer, stop, stop_all, redirect, resume, respond, escalate, clear_goal]` (subject to D3's wire decision) and replace the remaining `follow_up` mentions with RESUME.

#### [MIN-002] Superseded tables kept in place invite building from the wrong one

- **Lens**: Inoperability
- **Affected section**: Affected components (2026-09-28), Alternatives, Review disposition (still "paused", "interrupted", "drop of an interrupted child")
- **Description**: Three large tables contradict the current decision and are marked "history" only by a preamble line.
- **Recommendation**: Move them to an appendix titled "2026-09-28 record (superseded)"; keep only the Impact table in the body.

#### [MIN-003] Evidence baseline is stale and one "verified bug" lives in unpushed work

- **Lens**: Incorrectness
- **Affected section**: Header "Evidence baseline"; [Q2B] markers; D6b `DirectNonTerminalChildren`
- **Description**: `hasRunningOrQueuedDescendant` and `completeSteeredTurnIfDeferredAtGate` are on `origin/release/v0.1.1` @ `029da6db8`, not Q2B-only. `DirectNonTerminalChildren` exists only in 4 local, unpushed commits on `feat/q2b-on-984` (`de33c22d6` "reject met claims with live direct children"), not on `origin/feat/q2b-on-984`. Its missing `needs_input` case is a defect in work in progress; its user text also says "Wait for them to finish or cancel them".
- **Recommendation**: Re-baseline on current release; route the `DirectNonTerminalChildren` fix to the Q2B branch owner before that branch lands, and fix the message wording to the new vocabulary.

#### [MIN-004] Ledger compaction never runs for a stopped session

- **Lens**: Inoperability
- **Affected section**: D4 Retention ("Once the session is terminal and has no open relay")
- **Description**: Stopped sessions are never terminal and may never be resumed (F0929-1: never deleted), so their ledgers never compact.
- **Recommendation**: Compact when the session is `done`, `failed` **or** `stopped` with no pending control and no open relay.

#### [MIN-005] Is the 24-hour park expiry a "time out"?

- **Lens**: Ambiguity
- **Affected section**: D1.8 (`failed`, `owner_unreachable`/`answer_timeout`) vs Q19 ("timed out = stopped")
- **Recommendation**: State that Q19 covers only the lifetime budget and that park expiry stays `failed` (F1011-Q2), or ask the founder.

#### [MIN-006] `stopped_by_user` wire enums in Plan, Task and GoalOutcome are not in the wire table

- **Lens**: Incompleteness
- **Affected section**: D4 wire table; F0929-8
- **Description**: `contracts/components/schemas/Plan.yaml`, `Task.yaml`, `GoalOutcome.yaml` and `asyncapi.yaml` carry `stopped_by_user`. The "align naming" work touches contracts.
- **Recommendation**: List these schemas in the D4 wire table with the chosen change (or "unchanged").

#### [MIN-007] Esc is never specified

- **Lens**: Incompleteness
- **Affected section**: D9
- **Recommendation**: Add Esc to D9's table with the same semantics as a single Stop press (MAJ-002), including where focus must be for it to fire.

---

### Observations

#### [OBS-001] Parent-first boot order no longer earns its keep

- **Lens**: Overcomplexity
- **Affected section**: D8.1
- **Suggestion**: With restart = an independent stop-note write per record and nothing resuming automatically, order matters only for notice generation. Consider dropping the edge-order rewrite of `SteerBootRecovery.sessionIDs` and generating notices in a second pass.

#### [OBS-002] Single-user posture (founder, 2026-09-29 09:41)

- **Lens**: Overcomplexity
- **Affected section**: D1.4
- **Suggestion**: Omnipus is single-user per instance; the founder keeps the owner infrastructure. The Who/When/Which/Once checks stay correct, but note in Consequences that in the supported mode "Who" is always the one account, so the protective value is in When/Which/Once.

#### [OBS-003] T11 needs a deterministic seam

- **Lens**: Infeasibility
- **Affected section**: T11 ("the same instant")
- **Suggestion**: Specify a test hook that holds the terminal write until the stop note is written, so both branches of the race are exercised deterministically rather than by timing.

---

## Structural Integrity (generic-markdown / ADR)

**Scope clarity**: Good at the #1011 level (Scope table). Weak for plans (MAJ-008) and for the root conversation's own Stop (MAJ-002).

**Actors identified**: Owner, parent agent, child, runtime, boot recovery, plan engine. The SPA's Stop/Esc gestures are under-specified as an actor with server behaviour.

**Success criteria**: T1–T13 exist; T6 asserts an undecided design, T13 is marked unwritable, and there is no test for the two critical paths (CRIT-001 same-generation resume, CRIT-002 deferred-claim re-evaluation on stop).

**Failure modes**: Races for `stop` are tabled; crash-after-ledger-line is covered in principle but conflicts with restart rules (MAJ-010). Timeout-resume loop (MAJ-004) missing.

**Implementation detail**: Detailed for D1/D2/D4; insufficient where the revision changed things (Stop marker, notice, goal pair-end sites).

**Assumptions & constraints**: The ADR openly lists six open questions; three of them are already answered by the founder (MAJ-001).

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap | Affected |
|---|---|---|
| Liveness | Deferred `met` claim + last busy child stopped → parent re-evaluated | CRIT-002, D6 |
| Data model | RESUME on same generation actually dispatches (Stopped() false afterwards) | CRIT-001, D2 |
| Goal lifecycle | Stop / Stop all / restart / timeout each leave the child's goal active | MAJ-003 |
| Timeout | Timed-out child resumed runs at least one model call | MAJ-004 |
| Security | `redirect` of an owner-only-parked child → note states "not answered by owner", audit line written | MAJ-005 |
| State | Stop all on waiting-for-answer child → question and relays closed, owner's late answer handled visibly | MAJ-006 |
| Boot | Pending redirect/RESUME at crash → not auto-executed, reported to parent | MAJ-010 |
| Plans | Plan stop + plan restart → no orphaned stopped members with active goals | MAJ-008 |
| Wire | Single Stop press vs Stop all produce different cancel scopes | MAJ-002 |

### Dataset Gaps

| Dataset | Missing boundary | Recommendation |
|---|---|---|
| Tree shapes | Stopped middle node with working grandchild | Pin whether the root's "done" is blocked (MAJ-007) |
| Notice volume | Stop all / restart over a 3-level, 10-session tree | Assert notice count per parent (MAJ-009) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Owner-only answer path (D1) | ok | ok | ok | ok | ok | **risk** | `redirect`/RESUME supersede the question with agent text (MAJ-005) |
| `redirect` / RESUME | ok | ok | **risk** | ok | ok | **risk** | No audit of superseded owner-only questions (MAJ-005) |
| `clear_goal` (D7b) | ok | ok | risk | ok | ok | risk | Any ancestor may clear a goal the owner set inside a helper; no audit specified |
| Stopped-child notice | ok | ok | ok | ok | **risk** | ok | Notice storm after Stop all / restart over a large tree; dedup undecided (MAJ-009) |
| Control ledger | ok | ok | ok | ok | risk | ok | Stopped sessions never compact (MIN-004) |
| Boot recovery | ok | risk | ok | ok | ok | ok | Replay of pending controls vs restart-stop order (MAJ-010) |

---

## Unasked Questions

1. Is the new stop note the existing `session.Stop` marker, and how does `Stopped()` read after a same-generation resume? (CRIT-001)
2. Which code path re-evaluates a deferred "done" claim when a helper becomes stopped? (CRIT-002)
3. Do `done` and `failed` still end a helper's goal (FD1=A), given "only `/goal clear` ends a goal"? (MAJ-003)
4. What deadline does a timed-out helper get when resumed? (MAJ-004)
5. What does RESUME do to a helper waiting for an answer, and how does the helper learn an owner-only question was withdrawn unanswered? (MAJ-005)
6. Does a single Stop press in the main chat stop only the main chat's turn, leaving helpers working? (MAJ-002)
7. After a plan restart, what happens to the old stopped member sessions and their goals? (MAJ-008)
8. May `clear_goal` clear a goal the owner typed into a helper's chat, and is it audited?

---

## Verdict Rationale

**BLOCK.** CRIT-001 means the central new mechanism (a persistent, resumable stop) cannot be built as described without choosing between two incompatible readings of an existing persisted field; CRIT-002 means the founder's "never hangs" rule is violated by the ordinary "stop one helper while the parent waits" case. On top of that the ADR contradicts three recorded founder rulings (MAJ-001) and the Stop-button ruling (MAJ-002), and it misses the shipped goal-ending hooks that F0929-6 must switch off (MAJ-003).

The ADR's correction-round budget is spent (Status line). These findings go to the architect for one correction pass encoding Q19–Q21 and deciding CRIT-001/CRIT-002. The founder is needed for MAJ-005 (owner-only bypass policy), MAJ-006, MAJ-008, MAJ-009 and MAJ-010.

### Recommended Next Actions

- [ ] Encode Q19, Q20, Q21 and fix the F0929-3 quotation (MAJ-001)
- [ ] Decide the stop-note / Stop-marker relationship and same-generation resume (CRIT-001)
- [ ] Specify re-evaluation of deferred claims on a transition into `stopped` (CRIT-002)
- [ ] Rewrite D9 for single-press Stop / Esc / `/stop` = this session only; add the cancel-scope wire field (MAJ-002)
- [ ] List every FD1=A pair-end call site and state the goal rule for done/failed (MAJ-003)
- [ ] Define the resume deadline after a timeout (MAJ-004)
- [ ] Close the owner-only bypass via `redirect`/RESUME (MAJ-005)
- [ ] Founder interview: MAJ-006, MAJ-008, MAJ-009, MAJ-010
- [ ] Add `hasRunningOrQueuedDescendant` to the Impact table (MAJ-007); route the `DirectNonTerminalChildren` fix to the Q2B branch (MIN-003)

---

## Addendum (architect, reviewer of record): the ADR's six open questions, classified

The ADR's Impact section lists six open questions. For each one, this table says whether it is a founder question or an engineering decision, and gives a proposed answer for the engineering ones. The architect gives proposals, not rulings: the ADR author's one correction pass decides them.

| # | Open question (ADR Impact section) | Classification | Proposed answer / status |
|---|---|---|---|
| 1 | Does `LifecycleTimedOut` fold into `stopped`, `failed` or stay its own state? | **Already ruled by the founder** (Q19=A, `coordination/PLAN-2026-09-28.md`) | Fold into `stopped`: it can be resumed, the goal stays open, and the stop note records `cause: "timeout"`. Resuming must grant a fresh time budget (MAJ-004). Not a question any more. |
| 2 | Does the Plan record gain a literal `stopped` state, or only renamed prose? | **Engineering** (F0929-8 already rules on the behaviour: plans stop and can be resumed) | Add a real `stopped` plan state and retire `failed` + `stopped_by_user` for the user-stop case. `stopped_by_user` is a wire value in `Plan.yaml`, `Task.yaml`, `GoalOutcome.yaml` and `asyncapi.yaml`, so it needs a contract change and regeneration either way (MIN-006). Keeping `failed` for a resumable state repeats the naming the founder asked to remove. Greenfield: no migration needed. |
| 3 | Does `stopped` need its own `Session.yaml` `status` value? | **Engineering** (wire shape; the architect owns contract shape) | Yes. Replace `interrupted` with `stopped`, and add `failed` for real failures, giving `active / stopped / failed / archived`. Keep `waiting_for_answer` as an extra status value, not as a field the SPA must derive from the stop note. Today `lifecycleToUnifiedStatus` maps `Failed`/`Cancelled`/`TimedOut` to `interrupted`, so the old name would mix up the two meanings. The SPA display belongs to #1083. |
| 4 | `clear_goal`: single hop or cascading; precondition on `stopped`; the same notice mechanism as D6? | **Mostly already ruled** (Q20: never cascades, and the cleared helper gets the same prompt injection and decides about its own helpers; Q21=A: a working helper's goal may be cleared and the helper is told) | Single hop only. No precondition on the helper's state. The "helper is told" message is a new steer-type inbound entry for the helper, separate from D6's parent-facing stopped-child notice. That last split is an engineering decision. |
| 5 | Restart: an eager multi-level notice, or the lazy per-parent one? | **Engineering** (the founder ruled only on the outcome: parent told, nothing resumes automatically) | Eager, once per boot, for each direct parent that is not stopped itself, keyed on the child ID plus the boot ID. A stopped parent gets its notice when it is resumed. The lazy design breaks "parent told" for a working parent that never runs again. It also fails CRIT-002: nothing ever re-checks a deferred claim. |
| 6 | Stopped-child notice: exact trigger and dedup | **Engineering** | Trigger: any move into `stopped` of a child whose parent is not stopped. Deliver to the direct parent only, one hop (matching Q20's "each level decides"). Dedup key: `(child_id, stop seq)`. The same trigger must also re-check a deferred `met` claim (fixes CRIT-002). |

Founder-facing items that remain after this: MAJ-005, MAJ-006, MAJ-008 and MAJ-010 (see Recommended Next Actions). MAJ-009 reduces to rows 5 and 6 above, which are engineering decisions.

## Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| The new stop note reuses the existing durable Stop marker's JSON key (CRIT-001) | `git show origin/release/v0.1.1:pkg/session/lifecycle.go`: `Stop *Stop \`json:"stop,omitempty"\`` ("durable Stop marker … Written by the cascade"). The ADR, in D-stop's Mechanism paragraph, writes "a `stop` note" on the same record | Verified |
| The deferred-claim resume exits early for a non-terminal descendant and for a stopped parent (CRIT-002) | `git show origin/feat/q2b-on-984:pkg/agent/goal_child_completion.go::resumeDeferredGoalAfterDescendantTerminal`: `!descendant.Terminal()` return; `parent.Stopped()` return | Verified |
| Q19/Q20/Q21 are ruled but the ADR lists them as open (MAJ-001) | `coordination/PLAN-2026-09-28.md`, the 2026-09-29 Q19/Q20/Q21 line; the ADR's Vocabulary "Open question … `LifecycleTimedOut`", D7b "Open questions", and Impact open questions 1 and 4 | Verified |
| `Session.yaml` status enum is `active/archived/interrupted` | `git show origin/release/v0.1.1:contracts/components/schemas/Session.yaml`, the `status.enum` block | Verified |
| `stopped_by_user` is a wire value in four contracts | `git grep stopped_by_user origin/release/v0.1.1 -- contracts` → `asyncapi.yaml`, `GoalOutcome.yaml`, `Plan.yaml`, `Task.yaml` | Verified |
| MAJ-002 through MAJ-010 and the minor findings | Evidence cited inside each finding by the grill-spec pass. Not re-read line by line in the architect's addendum pass | Verified by the grill pass (not independently re-checked) |
| MAJ-008 (orphaned plan members after a plan restart) | Reasoned from F0929-8's "new generation" wording; not traced in the plan engine | Inferred (medium) |
| **Self-check** | Re-read the Executive Summary, the finding headings, the Verdict and this addendum against the brief: verdict given; severity counts (2/10/7/3) match the finding headings; each of the six ADR open questions classified as founder or engineering, with an answer proposed for the engineering ones; the three findings the verdict rests on (CRIT-001, CRIT-002, MAJ-001) re-verified first-hand against origin refs; only this one file is staged | Verified |
