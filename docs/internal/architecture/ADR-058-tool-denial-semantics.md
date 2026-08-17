# ADR-058: Tool-denial semantics — say which denials are permanent, and bound the retry

- **Status:** Accepted; **Amended 2026-08-05** (see [§10](#10-amendments-2026-08-05) — D4's mechanism superseded, W6 resolved by deletion, D1's reason enumeration corrected). Original decision text preserved throughout.
- **⚠️ PARTIALLY SUPERSEDED 2026-08-11 by [ADR-060](ADR-060-structured-tool-failure-family.md) §10:** §3's last bullet (scoping the `*ToolResult` refusal family OUT) is reversed, and §7 item 4's *"`PermissionDeniedResult` emits a plain string with no schema and is unaffected either way"* is no longer true — it now emits the generated `PermissionDenied` wire shape. Everything else in §3 and §7 stands.
- **Date:** 2026-08-05
- **Related:** [#594](https://github.com/elicify-ai/omnipus/issues/594) (this ADR is the second half of it; `6d0735ef` was the first); [ADR-036](ADR-036-consolidate-shell-and-subagent-tools.md) §3.4 (the standing-grant consultation point); [ADR-057](ADR-057-session-parent-child-parity.md) FR-080/FR-081 (approval-entry identity); FR-011/FR-016/FR-082 (the approval gate); FR-084 (`turn_synthetic_error_floor`); FR-009/#264 (headless auto-deny)
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 — direct codebase verification against `feature/plan-swimlane-board` @ `6793b96a` on 2026-08-05. Every code claim carries a `file::symbol` or `file:line` citation read in this session. Two classes of claim are **not** tool-verified by the author and are tagged inline: the raw `gateway.log` lines (operator-supplied; independently corroborated by a committed UAT report, see §1.1) and anything marked **[INFERRED]**.

> **Scope note.** This is a focused behavioural fix to one thing: what the system *tells the model* when a tool call is refused, and what bounds the model's response to being told. It changes no identity model, no storage layout and no wire contract (§7). It is deliberately small. A spec follows separately.

---

## 1. Context

### 1.1 The evidence

During the 32-task parallelism UAT of 2026-08-05, one task never finished.

**Operator-supplied** (`gateway.log`, not re-read by the author): five `approval: timeout fired … tool=run_task` entries at **06:05:41 → 06:10:45 → 06:15:47 → 06:20:49**, terminated only by a manual `turn_canceled` at **06:21:05**. The intervals are 304 s, 302 s, 302 s — i.e. the agent re-requested the *identical* tool immediately on each expiry, once per 300 s approval window, with nothing in between.

**Independently corroborated in the repo.** `docs/internal/uat/plan-32-task-parallelism-2026-08-05.md` records the same task from the other side:

- L58 — `mia`: *"3/4 failed (bash denied); **1/4 stuck `in_progress` for 13+ minutes with zero forward progress**"*
- L122 — *"1/32 stuck `in_progress` for 13+ minutes with no forward progress before the plan was manually stopped (`POST /plans/{id}/stop` … this task then resolved to `failed[stopped_by_user]`)"*
- L145 — *"it matches the same 'bash denied, delegate/run_task also denied' failure class as the other 23, but unlike all of them, it never resolved on its own in 13+ minutes"*

The timeline table (L85) shows `in_progress` stopped decreasing at 06:14:28 with exactly one task left. The two accounts agree on task, agent, window and outcome.

### 1.2 The control case — this is not agent indiscipline

In the **same run**, with the **same agents**, 23 tasks were denied `bash` **by policy** and every one of them stopped and reported the blocker. From the same report (L15): *"every single failure's `result` field cites the identical cause verbatim: 'the bash tool is denied by \[agent\]'s policy'"*, resolving in 4–439 s (L50).

Same run. Same models. Same agents. **Different denial kind, opposite outcome.** 23/24 correct, 1/24 unbounded. Whatever is wrong is not in the agent's judgement; it is in what the two denial kinds *say*.

### 1.3 The root cause, read in source

`pkg/agent/loop.go` has **three** denial emit sites, locatable by the `permission_denied` literal:

| Site | Branch | `message` | `reason` |
|---|---|---|---|
| `loop.go:8819` | TOCTOU policy flip → `deny` | `"Tool execution denied by policy."` | *(field absent)* |
| `loop.go:8851` | headless `AutoDenyAsk` (FR-009) | `"User denied tool execution."` | fixed literal, `"auto-denied: ask-policy tool not allowed in a headless scheduled run"` |
| `loop.go:8938` | interactive ask denied | `"User denied tool execution."` | verbatim from `PolicyApprover.RequestApproval` |

The third site is the defect:

```go
// pkg/agent/loop.go:8938
denyMsg := fmt.Sprintf(`{"error":"permission_denied","message":"User denied tool execution.","tool":%q,"reason":%q}`, toolName, denialReason)
```

`denialReason` here is one of `user`, `timeout`, `saturated`, `restart`, `cancel`, `batch_short_circuit` (`pkg/gateway/approvals.go:148` enumerates them; each is emitted at `:296`, `:342`, `:383`, `:386`, `:424`, `:483`), or the constant `nopApproverDenialReason` = `"no_approver_configured"` (`pkg/agent/tool_approver.go:65`).

**On a `timeout`, nobody denied anything. Nobody answered.** The model is handed a sentence asserting a human decision that did not occur, with the contradicting machine reason buried in a sibling field. It then behaves *reasonably on false information*: "no answer yet" is a textbook reason to try again later. The agent is not malfunctioning — it is misinformed.

The second site is the same false sentence applied to an even more obviously permanent case: a headless scheduled run has no operator *by construction*, for the whole run, and it still says "User denied".

### 1.4 The system already knows the truth — on the other rendering path

`pkg/agent/approval_transcript.go::askDenialText` renders the **human-facing** version of the *same event*, and it branches on the reason correctly:

```go
// pkg/agent/approval_transcript.go:175-190
case "timeout":
    return "Not run: the approval request expired before anyone answered it."
case "user":
    return "Not run: the approval request was denied."
case "cancel":
    return "Not run: the turn was cancelled while the approval request was open."
case "saturated":
    return "Not run: too many approval requests were already pending."
```

Its own doc comment (`:133-136`) states the principle this ADR exists to extend: *"it is surfaced verbatim because 'your tool call was denied' and 'nobody answered for five minutes' are very different things to a reader."*

**The transcript reader is told the truth. The model is not.** One event, two renderings, one of them false — and the false one is the one that drives behaviour. That is the whole bug in one sentence.

(`askDenialText` has no branch for `restart`, `batch_short_circuit` or `no_approver_configured`; all three fall through to `default: "Not run: " + reason`. Noted for completeness in §2 D1.)

### 1.5 Why nothing stopped it — the bound that exists but is not armed

FR-084 already provides a per-turn abort on repeated synthetic denials, and it is already called from all three sites above (`loop.go:8839`, `:8895`, `:8958` → `recordSyntheticDeny`). It did not fire. Reading why:

```go
// pkg/agent/loop.go:9778-9785
func (al *AgentLoop) syntheticErrorFloor() int {
	n := al.GetConfig().Gateway.TurnSyntheticErrorFloor
	if n < 0 {
		return defaultSyntheticErrorFloor   // 8
	}
	return n
}
```
```go
// pkg/agent/loop.go:9797
if floor <= 0 || ts.syntheticErrorCount < floor { return false, "" }
```

`TurnSyntheticErrorFloor` is `int` with `json:"turn_synthetic_error_floor,omitempty"` (`pkg/config/config.go:3033`) and is **assigned nowhere** — a repo-wide search over `pkg/` and `cmd/` for both the Go field and the JSON key returns only the declaration, this reader, and three doc comments. So on any install that has not hand-edited `config.json`, the value is the Go zero `0`, `syntheticErrorFloor()` returns `0`, and `floor <= 0` **disables the mechanism entirely**.

Two consequences worth stating plainly:

1. **The documented default is not the effective default.** `config.go:3030-3032` says *"Default: 8. Set to 0 to disable."* As implemented, unset **is** 0 **is** disabled; the value 8 is reachable only by explicitly configuring a *negative* number, which no operator would ever write. This is an inverted-sentinel defect: the safe value and the "unset" value collide.
2. **Even fully armed it would not have been a usable bound here.** 8 consecutive denials × the (then) 300 s window is ~40 minutes; at the new 600 s window it is ~80 minutes. A ceiling measured in hours is not a ceiling a user experiences.

A second existing bound, `MaxIterations` (default 200 — `pkg/agent/instance.go:208-216`, hard ceiling `2×` at `loop.go:7470-7473`), is even further away: 200 × 600 s ≈ 33 hours.

**So: the loop was unbounded in practice, and the two mechanisms that look like bounds were disabled and irrelevant respectively.**

### 1.6 What `6d0735ef` already did, and did not do

Shipped (context, not a decision of this ADR): the default approval timeout was raised 300 s → 600 s, and a second independent `300 * time.Second` literal in `approvals.go::newApprovalRegistryV2` was collapsed onto the shared `gateway.go::defaultToolApprovalTimeout` so boot and the registry constructor cannot drift. Its own commit message is explicit: *"This lengthens each wait window; it does NOT bound the retry loop, which is the other half of #594 and needs a separate fix."*

That change made each futile cycle **longer**. It is not a mitigation of this defect and must not be mistaken for one. **This ADR is the loop fix.**

### 1.7 What is explicitly correct and stays correct

`pkg/gateway/approvals.go::fireTimeout` fails **CLOSED** — `ApprovalStateDeniedTimeout`, `ApprovalOutcome{Approved: false, Reason: "timeout"}` (`:336`, `:342`). An unanswered approval is never treated as an approval. **This ADR proposes no change to it.** The problem is not the outcome; it is the sentence attached to the outcome.

---

## 2. Decision

### D1 — Every denial is classified PERMANENT or RETRYABLE, and the payload says which

Add a machine-readable marker to the denial payload — `"permanent":true|false` — **and** make the human-readable `message` accurate for the reason it carries. Both, not either: the marker is for reliable parsing, the message is for the model that reads prose. Neither is a control on its own (see D3).

**The classification, with per-reason reasoning:**

| # | Reason | Emitted at | Class | Why |
|---|---|---|---|---|
| 1 | `user` | `approvals.go:383` (`resolve`, deny action) | **PERMANENT** | A human considered this call and said no. Re-asking is an agent overriding a human decision. |
| 2 | `timeout` | `approvals.go:342` (`fireTimeout`) | **PERMANENT** *(within the turn)* | Nobody answered in the full window. The overwhelming cause is an unattended run — and an unattended run is still unattended 600 s later, so the retry buys another full window of nothing. This is the operator's call and is recorded as such: it trades a theoretically-recoverable case (a human who returns to their desk between windows) for termination, because the observed cost of the alternative is an unbounded turn. |
| 3 | `saturated` | `approvals.go:296` | **RETRYABLE** | A transient resource condition: the pending-approval count hit `ToolApprovalMaxPending` (default 64, `config.go:3029`). The queue drains as other approvals resolve; nothing about *this* call was refused on its merits. **This is the one reason that must remain retryable** — see AC-06. |
| 4 | `restart` | `approvals.go:483` (`cancelAllPendingForRestart`) | **PERMANENT** *(within the turn)* | The gateway is shutting down. The turn will not outlive the process; retrying inside it cannot succeed. |
| 5 | `cancel` | `approvals.go:386`, and `cancelAllPendingForSessions` | **PERMANENT** *(within the turn)* | Something is deliberately stopping this turn or session. Retrying fights an in-flight cancellation — the exact behaviour that forced the manual Stop in §1.1 to be issued at all. |
| 6 | `batch_short_circuit` | `approvals.go:424` (`cancelBatchShortCircuit`) | **PERMANENT** | Its doc comment (`:405-406`): *"Used when a prior call in the same batch was denied/canceled (FR-065)."* It inherits its permanence from an originating `user` or `cancel`, both PERMANENT above. |
| 7 | `no_approver_configured` | `tool_approver.go:116` (`nopPolicyApprover`) | **PERMANENT** | A wiring fault, not a decision — `SetToolApprover` was never called. No amount of retrying wires a gateway. Permanent until an operator intervenes, and the code already treats it that way: `nopApproverFallbackOnce` (`:73`) is process-scoped precisely because *"resetting it would require a process restart, which is also when an operator would have a chance to fix the wiring."* |
| 8 | *(policy deny)* | `loop.go:8819` — no reason field | **PERMANENT** | The tool's effective policy resolved to `deny`. **This is the control case from §1.2 that already behaves correctly.** It gets a marker for uniformity, not to change behaviour. |
| 9 | *(headless auto-deny)* | `loop.go:8851` — fixed literal reason | **PERMANENT** | FR-009/#264: a scheduled run has no operator by construction, for the entire run. The existing literal is accurate about *what* happened; only its `message` ("User denied…") is false. |

**Eight of nine are permanent.** That is the finding, not a design choice — approval denial is overwhelmingly a terminal condition, and the current code tells the model the opposite by omission.

**`askDenialText` becomes the single renderer for both audiences.** It already branches correctly (§1.4) and already sits at the settle path both sites call. The spec should promote it to the shared source of truth for the denial sentence, so the transcript rendering and the model-facing rendering are structurally incapable of diverging again — the divergence *is* the bug. Its three missing branches (`restart`, `batch_short_circuit`, `no_approver_configured`, §1.4) are filled in the same change. Exact strings are the spec's to fix; the constraint is that one function produces them.

### D2 — Never again claim a user denied something they did not

The `message` field must describe what actually happened, and must name the productive next step.

`"User denied tool execution."` is retired from every path where a user did not deny. Concretely, that is `loop.go:8938` for six of the seven approver reasons, and `loop.go:8851` in its entirety.

**Illustrative** (the spec fixes final wording):

| Reason | `message` | `permanent` |
|---|---|---|
| `user` | `The user denied this tool call. Do not retry it; stop and report the blocker.` | `true` |
| `timeout` | `The approval request expired with no answer — nobody is available to approve it. Do not retry; stop and report the blocker.` | `true` |
| `saturated` | `Too many approval requests were already pending. This one may be retried later.` | `false` |
| `no_approver_configured` | `No approval mechanism is configured on this gateway. Do not retry; report this as a configuration blocker.` | `true` |
| *(policy deny)* | `Tool execution denied by policy.` *(unchanged — already true)* | `true` |

The naming of the next step is load-bearing and is why D2 is not merely cosmetic: a model told only "denied, permanently" still has to infer what to do instead. §1.2's control case shows what the right inference looks like when the message supports it.

### D3 — The productive exit is: end the turn, report a blocker, name the tool

On a permanent denial the correct behaviour is to stop and report a blocker that **names the offending tool** — precisely what the 23 policy-denied `bash` tasks did (§1.2), whose `result` fields named the tool and the reason verbatim.

**Message wording is not a control.** It is a prompt-level nudge to a probabilistic system; it will be right most of the time and wrong some of the time, and "most of the time" is not a bound. D1 and D2 remove the *reason* to retry; they cannot remove the *ability*. That is the entire justification for D4, and the reason D4 is not optional dressing on top of D1/D2.

### D4 — A hard ceiling of 10, per turn, keyed by `(toolName, denialReason)`

> **⚠️ AMENDED 2026-08-05 — see [§10 Amendments](#10-amendments-2026-08-05) (A1, A2, A4, A5).** D4's *intent* (a hard, named, per-turn backstop of 10 that opens no approval round-trip and terminates with a tool-naming reason) stands unchanged and is implemented. D4's *mechanism as written below* does not: a red-team of the implementation spec proved that quarantining at the ceiling makes AC-02 arithmetically unsatisfiable and the quarantine gate unobservable, and that per-pair keying leaves the heterogeneous storm in §1.2's own data unbounded. **The original text below is preserved exactly as decided; the superseding mechanism is in §10.**

**Operator decision: the constant is 10.** It is not derived from a measurement and this ADR does not pretend otherwise; it is a chosen backstop, and it must be a **named constant** so it is greppable, reviewable and changeable in one place.

Counter state lives on `turnState` (`pkg/agent/turn.go`), alongside the existing `syntheticErrorCount` (`turn.go:355-360`), keyed by the pair `(toolName, denialReason)`.

On reaching the ceiling for a given pair, all four of the following, together:

1. **Stop dispatching that tool** for the remainder of the turn.
2. **Short-circuit further calls** to it with the cached denial payload.
3. **Open no new approval round-trip and re-prompt no human.** This is the property that matters most operationally: without it, each of the 10 attempts costs a full 600 s window, and the "ceiling" is 100 minutes of wall clock.
4. **Terminate the turn** with a reason naming tool + agent + denial reason — e.g. `blocked: tool "bash" denied by policy for agent "mia"`.

A plan task in this state must land in `failed` **carrying that reason**, not a bare failure. §1.1's task landed as `failed[stopped_by_user]` — an accurate record of the *human's* action and a useless record of the *cause*.

**Interaction with FR-084 — stated honestly, because the numbers conflict.** FR-084's floor is 8 and D4's ceiling is 10, both per-turn. If FR-084 is ever armed (it is not today, §1.5), then for a *homogeneous* denial stream — one tool, one reason, which is exactly §1.1's case — FR-084's counter reaches 8 first and aborts with `reason:"synthetic_error_loop"`, a message that names **neither the tool nor the agent**. D4's richer, actionable termination would then be unreachable in the case it was designed for. The spec MUST resolve this explicitly; it is a real conflict, not a cosmetic overlap. **[INFERRED]** — the author's recommendation, for the spec to accept or reject: once a `(tool, reason)` pair is quarantined under D4, its short-circuited results stop feeding `recordSyntheticDeny`, so FR-084 continues to do its own job (catching *heterogeneous* synthetic-error storms) without racing D4 for the homogeneous one. Whatever is chosen, it must be chosen deliberately.

**And D4 must not repeat FR-084's inverted sentinel.** §1.5's defect is that unset == disabled while the docs claim a default. Whatever configurability D4's constant gets, the *unset* state must be the armed default. AC-05 asserts this.

### D5 — REJECTED: removing a permanently-denied tool from the offered tool list mid-turn

**Proposed and explicitly declined by the operator. Recorded here so it is not re-proposed.**

The proposal: on a permanent denial, drop the tool from `tools[]` for subsequent LLM calls in the turn, so the model cannot name it again. Mechanically it would work — the tool list is reassembled per iteration from `FilterToolsByPolicy` (`loop.go:7606-7607`), so a per-turn subtraction is cheap and local.

Why it was rejected:

- **It hides the cause instead of stating it.** A tool that silently vanishes gives the model no reason for its absence. The model most likely to be misled by a mid-turn schema mutation is the same one this ADR is trying to inform.
- **It makes the advertised capability set unstable within a turn**, which nothing else in the system does, and which interacts badly with prompt caching and with any provider that reasons over the tool list.
- **D4 already achieves the operational goal** — no execution, no approval round-trip, no human re-prompt — without touching the schema.

**The advertised tool set stays stable for the whole turn. Denied calls are short-circuited, not un-offered.**

---

## 3. What this does NOT fix

Stated plainly, because the ADR is worth less if this section is soft.

- **A retryable denial can still be retried up to 10 times.** `saturated` is genuinely transient and must stay retryable (D1 #3), so it retains the full ceiling budget. D4 caps the damage; it does not eliminate it. No back-off is specified here.
- **The ceiling is a backstop, not the fix.** If D1/D2 work, D4 never fires. If D4 is firing in production, D1/D2 are not landing — treat a `blocked:` termination in the logs as a signal about the *messages*, not as the system working as designed.
- **The upstream validation gap remains** (§5). This ADR makes a denial honest and bounded; it does nothing to stop 24 of 32 tasks being assigned to agents that cannot perform them.
- **10 is unvalidated.** It is an operator-chosen constant with no measurement behind it. It is almost certainly too high for an interactive turn and possibly too low for a long autonomous plan; nothing here distinguishes those cases.
- **`timeout` as PERMANENT costs a real recovery case.** A human who steps away for 12 minutes and returns will now find the turn ended rather than a second approval waiting. That is the accepted trade (D1 #2), not an oversight.
- **Nothing here touches non-approval refusal classes.** `pkg/tools/fserrors.go::PermissionDeniedResult` (filesystem-scope denial) and `pkg/tools/result.go::DelegationDeniedResult` (delegation-policy denial) reuse the same `permission_denied` JSON convention and are also permanent, but they are returned as `*ToolResult` from tool execution rather than synthesised by the loop, and `DelegationDeniedResult` is contract-governed (§7). Extending the marker to them is a deliberate, separately-scoped choice — see §7 for why it is not free.

---

## 4. Consequences

### Gained

- The model is told the truth about why a call did not run, in prose and in a parseable field.
- The single worst behaviour — an unbounded turn requiring a human Stop — is bounded twice over: by the model having no reason to retry (D1/D2) and by a hard ceiling if it retries anyway (D4).
- A permanently-denied tool stops costing a 600 s human-wait per attempt (D4 #3). This is the largest wall-clock win in the change.
- Denial rendering has one source of truth for two audiences (D1), so the §1.4 divergence cannot silently reappear.
- Terminations name the tool, the agent and the reason, so a failed plan task explains itself without a log dive.
- §1.5's inverted-sentinel defect in FR-084 is documented, whether or not this change fixes it.

### Lost / changed

- A `timeout` no longer leaves the door open for a late human (§3).
- One more per-turn counter and one more termination path in `runTurn`, a function already carrying `syntheticErrorCount`, `MaxIterations` and its 2× hard ceiling. Four bounds on one loop is not obviously the right number, and D4 adds the fourth.
- A conflict with FR-084 that must be resolved in the spec rather than discovered later (D4).
- Every existing test asserting the literal `"User denied tool execution."` breaks. That is correct — the string is the defect — and those assertions must be **deliberately inverted, not deleted** (W5), following ADR-057 W22's precedent.

---

## 5. Out of scope — named, not deferred silently

**Upstream: nothing validates tool-policy feasibility at task-assign or plan-approve time.**

This is what created the situation at all. From `docs/internal/uat/plan-32-task-parallelism-2026-08-05.md` L15: 32 tasks were round-robined across 8 agents; only `jim` and `worker` have `bash` allowed; **all 24 tasks assigned to the other 6 failed** for that reason and no other. The system accepted every one of those assignments, approved the plan, and dispatched real, token-consuming LLM turns for work it could have known was impossible.

A denial-semantics fix makes those 24 failures *honest and fast*. It does not make them *not happen*. The check — validate that an assigned agent's tool policy permits its task's required tools, at assign or approve time — is a **separate follow-up** and is not decided here. It is named so that shipping this ADR is not mistaken for closing that gap.

---

## 6. Work items

| # | Item | Decision |
|---|---|---|
| **W1** | Classify: a reason → `(permanent bool, message string)` mapping covering all nine rows of D1's table, including the fixed-literal headless reason and the no-reason policy-deny path. One table, one place. | D1 |
| **W2** | Promote `askDenialText` (`approval_transcript.go:175-190`) to the shared renderer for transcript **and** model-facing text; add its three missing branches (`restart`, `batch_short_circuit`, `no_approver_configured`). | D1 |
| **W3** | Rewrite the three `permission_denied` payload builders — `loop.go:8819`, `:8851`, `:8938` — to emit `"permanent"` and the classified `message`. Retire `"User denied tool execution."` from every path where a user did not deny. | D1, D2 |
| **W4** | `turnState` counter keyed by `(toolName, denialReason)`; named ceiling constant = 10; short-circuit branch that opens **no** approval round-trip; turn termination naming tool + agent + reason; plumb that reason into a plan task's `failed` result. Resolve the FR-084 interaction explicitly (D4). | D4 |
| **W5** | Invert — never quietly delete — the tests that pin the current strings. At minimum `pkg/agent/scenario_runturn_test.go:224,248-254` (asserts a `role="tool"` message containing `permission_denied`), `subturn_delegate_nesting_test.go:15,76,215,276`, `turn_recheck_test.go:16`. Precedent: ADR-057 W22. | D1–D4 |
| **W6** | Fix FR-084's inverted sentinel (`loop.go::syntheticErrorFloor`) **or** record a deliberate decision not to. Today `config.go:3030-3032` documents a default of 8 that the code cannot produce from an unset field. Leaving both the doc and the behaviour as-is is the one option that is not acceptable. **→ AMENDED 2026-08-05: resolved by DELETING FR-084 outright, not by repairing the sentinel. See §10.A3.** | §1.5 |

---

## 7. Contract impact (Constraint #8) — investigated, not assumed

**Conclusion: as scoped by D1 (the three `loop.go` payloads), no `contracts/` change and no `scripts/gen-contracts.sh` run is required. Extending the marker beyond that scope is not free.**

The four surfaces were traced individually:

1. **The `denyMsg` string itself** — built by `fmt.Sprintf` and placed in `providers.Message{Role:"tool", Content: denyMsg}`. It travels to the LLM provider and to `Sessions.AddFullMessage` (`ts.agent.Sessions` is a `session.SessionStore`, `instance.go:70`). **It does not cross the gateway/SPA boundary:** the ask-deny branch `continue`s at `loop.go:8962` *before* the tool-execution/result path, so no `tool_call_result` frame is produced; and a search of `pkg/gateway/*.go` for reads of the message store returns nothing. Not a wire format. **No contract change.**

2. **The transcript entry** — `settleAskToolCallTranscript` (`approval_transcript.go:147-157`) writes into `session.ToolCall.Result`, which **is** a contract type (`contracts/components/schemas/ToolCall.yaml`). Its `result` property is `type: object` with `additionalProperties: true`, so **adding a key inside `result` needs no schema change**. ⚠️ `ToolCall` itself is `additionalProperties: false` — if the spec ever wants `permanent` as a **top-level** `ToolCall` field rather than a `result` key, that **is** a contract change and must run the 5-step pipeline.

3. **`ToolExecSkippedPayload.Reason`** — `EventKindToolExecSkipped` exists only as the internal event-kind string `"tool_exec_skipped"` (`pkg/agent/events.go:118`). Zero references in `pkg/gateway/`, zero in `contracts/`, zero in `src/`. Internal event bus only. **No contract change.**

4. **The adjacent denial results (§3's last bullet) — this is the trap.** `pkg/tools/result.go::DelegationDeniedResult` builds `generated.DelegationFailure`, a **generated** type whose schema is inline in `contracts/asyncapi.yaml:1747-1793` with **`additionalProperties: false`**, referenced from `ToolCallResultFrame.result`'s `oneOf` (`:1834`), and generated into a strict Zod schema (`src/lib/api/generated/schemas.ts:8790`). Adding `permanent` to a `DelegationFailure` **without** the contract change would fail SPA-edge Zod validation and the frame would be **dropped** — a silent, counter-only failure. So: if the follow-on spec extends the marker to delegation denials, it MUST run `contracts/` → `scripts/gen-contracts.sh` → atomic commit. `pkg/tools/fserrors.go::PermissionDeniedResult` emits a plain string with no schema and is unaffected either way.

**Recommendation for the spec: scope the marker to the three `loop.go` payloads (no contract change) and treat the delegation payload as a separate, contract-pipelined decision.**

---

## 8. Acceptance criteria

**Verification bar, inherited from ADR-057 §10 and non-negotiable here:** every criterion is asserted against a **real** `PolicyApprover` outcome and a **real** registered turn. A spy that records its argument and returns success proves nothing — this project has been burned by exactly that (ADR-057 §10, `plan_engine.go`'s derived `plan:<id>` that cancelled nothing for months while every test passed). Parent/child ids and tool names must be **distinct values** in every fixture.

| AC | Criterion |
|---|---|
| **AC-01** | For each of the nine D1 rows, drive a real denial and assert the emitted payload's `permanent` matches the table **and** its `message` does not contain the substring `"User denied"` unless the reason is literally `user`. Table-driven over the full enumeration — a new reason added to `ApprovalOutcome` with no classification must fail this test, not default silently. |
| **AC-02** | **The §1.1 regression, end to end.** A real `ask`-policy tool + an approver that lets the timeout fire + no human. Assert: the turn terminates on its own; the termination reason names the tool **and** the agent **and** the denial reason; the total elapsed time is bounded by a stated multiple of one approval window, **not** by 10 windows. Fail if a human Stop is required. |
| **AC-03** | **No new round-trip after quarantine.** With a `(tool, reason)` pair at its ceiling, assert the approval registry receives **zero** further `requestApproval` calls for that tool in that turn (assert on `pendingCount`/registry state, not on a mock's call log) and that no `tool_approval_required` frame is emitted. |
| **AC-04** | **The plan-task path.** A plan task whose turn terminates under D4 lands in `failed` with a `result` naming tool + agent + reason. Explicitly assert it is **not** `failed[stopped_by_user]` and **not** a bare failure — the §1.1 outcome must be distinguishable from a human Stop in the persisted record. |
| **AC-05** | **The ceiling is armed by default** — with a config that has never been hand-edited (Go zero values throughout), the ceiling fires at 10. This is the direct guard against repeating FR-084's inverted sentinel (§1.5): a test that only passes with an explicitly-configured value does not satisfy this criterion. |
| **AC-06** | **POSITIVE LOWER BOUND (Binding Rule 4) — a suite where everything is blocked must not pass.** A `saturated` denial (`permanent:false`) followed by a retry of the **same tool** must **succeed** when the queue has drained: assert the tool actually executes and produces a real result. Assert its `permanent` field is `false` and that the tool was **not** quarantined. Without this criterion, a change that classified every reason as permanent — or that quarantined on the first denial — would pass AC-01 through AC-05 completely. |
| **AC-07** | **One renderer, two audiences.** For the same denial event, the transcript entry's `result.text` and the model-facing `message` are produced by the same function, asserted by driving one real denial and comparing both persisted surfaces. A reason with no explicit branch (e.g. a newly-added one) must render identically on both paths rather than diverging. |
| **AC-08** | **FR-084 interaction, whichever way it is resolved.** A homogeneous stream of 10+ identical denials in one turn terminates with D4's tool-naming reason, not FR-084's `synthetic_error_loop` — or, if the spec decides otherwise, the chosen precedence is asserted explicitly. This AC must not be satisfiable by accident. |
| **AC-09** | **Contract non-regression.** `make verify-contracts` is clean, and — if and only if the marker was extended to `DelegationFailure` (§7 item 4) — a `tool_call_result` frame carrying a delegation denial round-trips through the SPA's generated Zod schema without being dropped. |

---

## 9. Open questions

1. **Should the ceiling be per-turn or per-task?** A long autonomous plan task and a 3-message interactive chat get the same budget of 10 today. Not decided here.
2. **Should `saturated` back off rather than retry immediately?** D1 keeps it retryable and D4 caps it at 10; neither introduces a delay, so 10 rapid retries against a full queue remain possible.
3. **Does the marker extend to `PermissionDeniedResult` / `DelegationDeniedResult`?** §7 prices it; §3 scopes it out. A follow-on decision, with a contract-pipeline cost attached to one of the two.

---

## 10. Amendments (2026-08-05)

**Status:** Accepted. **Author:** architect, at the operator's design resolution. **Trigger:** a red-team of the implementation spec (`docs/internal/specs/adr-058-tool-denial-semantics-spec.md`, revision 1) found that two clauses of this ADR are in direct conflict, and that three of §2's supporting claims are wrong. Nothing in §1–§9 is rewritten — the original decision text stands as decided, and each amendment names what it supersedes.

### A1 — D4's quarantine engages on the FIRST permanent denial, not at the ceiling

**Supersedes:** D4's opening clause (*"On reaching the ceiling for a given pair, all four of the following, together"*), for items 1–3 only.

D4 as written is internally unsatisfiable, on two independent grounds:

- **Arithmetic.** D4 #3 exists so that a permanently-denied tool stops costing a full approval window per attempt — §4 calls it *"the largest wall-clock win in the change"*. But with quarantine engaging only at the ceiling, attempts 1…10 each open a real approval round-trip. At the 600 s default that is **100 minutes**. §8's AC-02 simultaneously requires the turn be bounded *"by a stated multiple of one approval window, **not** by 10 windows"*. Both cannot hold.
- **Observability.** Quarantine engaged on the same denial that terminated the turn, so the quarantine map and its short-circuit gate had a lifetime of zero dispatches. No test could assert that a short-circuit ever occurred — a control that cannot be observed is the defect class this project keeps shipping.

**Amended decision.** A denial classified PERMANENT quarantines its tool **immediately, on its first occurrence** — which is what "permanent" means, and what D1/D2/D3 already imply. The turn **continues**; every subsequent call to that tool is answered from the cached denial payload with no hook call, no policy re-resolution, no `RequestApproval` and no approval round-trip. Wall clock is capped at ~one approval window per tool, making AC-02 satisfiable and giving the gate a real lifetime.

**Accepted losses, recorded:** a `user` denial is a decision about one call with one set of arguments, and quarantining the whole tool for the turn is broader than that decision; and a standing "Always Allow" grant issued *after* a tool is quarantined does not un-quarantine it for the remainder of that turn.

### A2 — The ceiling of 10 becomes an AGGREGATE per-turn bound, not per `(tool, reason)`

**Supersedes:** D4's title and its clause *"keyed by the pair `(toolName, denialReason)`"* for the counter. (The **quarantine map** is keyed by tool name alone — see A5.)

Per-pair counting leaves a hole this ADR's own evidence describes. §1.2 records agents cycling through **2–3 distinct denied tools each** ("bash denied, delegate/run_task also denied"); under per-pair counting that is 20–30 attempts before any single pair reaches 10.

**Amended decision.** `turnDenialBudget = 10` counts **every denial response handed to the model in the turn** — any tool, any reason, including responses served from the quarantine cache. On the 10th, the turn terminates via `abortTurn` with the reason of D4 #4 (tool + reason + agent), unchanged. The operator's constant of 10 is preserved; only its population widens.

Counting cache-served replays is deliberate: without it, a model repeating a quarantined tool would be bounded only by `MaxIterations` (200) — cheap in wall clock, expensive in tokens.

**Consequence, stated plainly:** a turn whose *only* denials are `saturated` now terminates on the 10th, even though every one was retryable. Accepted — ten saturation denials in one turn means the queue has not drained for the whole turn, which is the same unbounded shape this ADR exists to stop. §8's AC-06 remains binding: a `saturated` denial must never quarantine, and a retry after the queue drains must genuinely execute.

### A3 — W6 is resolved by DELETING FR-084, not by repairing its sentinel

**Supersedes:** W6's *"fix … or record a deliberate decision not to"*, and D4's **[INFERRED]** recommendation that FR-084 keep catching heterogeneous storms.

The one FR-084 call site that survives the D4 rewiring is the tool-assembly-duplicate branch (`loop.go:7625-7639`). **Both of its branches `return` from `runTurn`**, so its counter can reach at most 1 — a floor of 8 is unreachable. Repairing the inverted sentinel would therefore produce a documented-live, permanently-dead control: precisely the defect filed as **#595**, re-created by the fix for #595.

**Amended decision.** FR-084 is deleted in full: `config.GatewayConfig.TurnSyntheticErrorFloor`, `defaultSyntheticErrorFloor`, `AgentLoop.syntheticErrorFloor`, `AgentLoop.recordSyntheticDeny`, `turnState.syntheticErrorCount`, all four call sites, the `synthetic_error_floor` abort stage, the `audit.EventTurnAbortedSyntheticLoop` event, and their tests. Nothing is lost — the duplicate branch already terminates the turn unconditionally via its second `return`. A3 also dissolves the D4/FR-084 conflict §2 flagged: the competitor is gone, so §8's AC-08 is satisfied by asserting the deletion behaviourally, not by asserting a precedence.

**#595 is resolved by deletion rather than by fixing the sentinel.**

### A4 — §1.3 and D1 miscount the denial reasons

**Corrects:** §1.3's *"one of `user`, `timeout`, `saturated`, `restart`, `cancel`, `batch_short_circuit` (`approvals.go:148` enumerates them …)"* and D1's nine-row table.

Verified in-session against `9f5f5c4b`:

- **`approvals.go` produces six denial reasons, not seven.** `Reason: "approved"` (`:380`) carries `Approved: true` and is not a denial. Any classifier asserting it "known" would be asserting a lie.
- **`internal_error` was missed.** `pkg/gateway/policy_approver.go:58` returns `(false, "internal_error")` when `requestApproval` yields a nil entry. It is a denial reason the model can receive and must be classified. It is PERMANENT (a gateway fault).
- **`batch_short_circuit` cannot be produced by a real turn.** `cancelBatchShortCircuit` (`approvals.go:408`) has **zero production callers** — only its definition and test files. D1 #6's classification stands, but §8's AC-01 must not demand a *driven* denial for it; the FR-065 control being dead is filed as a separate finding.

The classification table therefore has ten rows (adding `internal_error` and the empty-reason branch that `askDenialText` already handles), of which exactly one — `saturated` — is retryable.

### A5 — The quarantine map is keyed by tool name alone

**Clarifies:** D4's *"keyed by the pair `(toolName, denialReason)`"* as applied to the short-circuit lookup.

The short-circuit gate runs **before** the approval path, so at gate time only the tool name is known — the denial reason of a hypothetical fresh attempt does not exist yet. Keying the lookup by `(tool, reason)` while quarantining by the same pair was a latent key-shape mismatch that becomes a real bug once quarantine has a lifetime (A1). The quarantine map is keyed by **tool name**, and its value carries the reason and payload that caused it. Once a tool has produced one permanent denial, every further call to it in that turn short-circuits regardless of what a fresh attempt might have returned.

### A6 — §7's env-tag reasoning, as restated by spec revision 1, was false

**Corrects:** not this ADR's text, but a claim spec revision 1 derived while assessing §7's contract scope, recorded here so it is not repeated: `pkg/config`'s `env:` struct tags are **not** decorative. `pkg/config/config.go:29` imports `github.com/caarlos0/env/v11` and `config.go:3723` calls `env.Parse(cfg)`; there are 214 live `env:` tags. §7's four contract conclusions are unaffected and stand.
