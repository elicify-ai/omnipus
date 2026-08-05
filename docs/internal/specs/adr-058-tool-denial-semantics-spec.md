# Spec — Tool-denial semantics: honest denial payloads + a bounded turn (ADR-058)

- **ADR:** [ADR-058](../architecture/ADR-058-tool-denial-semantics.md) (Accepted 2026-08-05 `cbc30eed`; **Amended 2026-08-05 §10**). D1–D3, D5 and §8's acceptance criteria are non-negotiable inputs. **D4 is superseded in mechanism by ADR §10.A1–A3** and re-specified here in §3.
- **Issue:** [#594](https://github.com/elicify-ai/omnipus/issues/594) (second half; `6d0735ef` was the first). **[#595](https://github.com/elicify-ai/omnipus/issues/595) is resolved by this spec through deletion** — see §3.2.
- **Branch:** `feature/plan-swimlane-board` @ `9f5f5c4b`
- **Revision:** 2. Revision 1 was red-teamed and found to specify a mechanism that could not fire, an acceptance criterion that was arithmetically unsatisfiable, and three false code claims. §2.3 lists every correction made to revision 1.
- **Evidence level:** every `file::symbol` and `file:line` below was read in-session against `9f5f5c4b`. Unverified claims are marked **[UNVERIFIED]** and are never load-bearing. **No negative claim appears in this document that was not executed as a search in-session** — revision 1's C7 was a false negative asserted without checking (§2.3 X1).
- **Scope:** behavioural fix inside `pkg/agent`, plus deletions in `pkg/config` and `pkg/audit` and a constants-only change in `pkg/gateway`. No identity model, no storage layout, **no contract change** (§3.6).

---

## 1. Overview

Three denial emit sites in `pkg/agent/loop.go` hand the model a sentence asserting a human decision that did not occur — `"User denied tool execution."` — on every approver reason except `user`, and on the headless auto-deny path. The model then retries reasonably on false information. Nothing bounds the retry: the mechanism that looks like it would (FR-084) is disabled on every install *and*, after the only call site that can still reach it, structurally incapable of firing more than once per turn. One UAT task looped for 13+ minutes and needed a manual Stop.

This spec delivers four things:

1. **One classification table** making every denial payload honest and machine-readable (`permanent: true|false`).
2. **One renderer** shared by the transcript and the model, so the §1.4 divergence cannot reappear.
3. **Quarantine on the first permanent denial.** A permanently-denied tool is answered from a cached payload for the rest of the turn — no hook call, no policy re-resolution, no `RequestApproval`, no approval round-trip. The turn continues.
4. **One aggregate per-turn denial budget of 10**, counted across *all* denials regardless of tool or reason. On the 10th, the turn terminates with a reason naming tool + reason + agent.

And it **deletes FR-084 outright** rather than repairing it (§3.2).

**Out of scope, named:** upstream tool-policy feasibility validation at plan-approve time (ADR §5); `pkg/tools/fserrors.go::PermissionDeniedResult`; `pkg/tools/result.go::DelegationDeniedResult` (§3.6 prices it); `pkg/gateway/approvals.go::fireTimeout`'s fail-closed behaviour, which is correct and unchanged; back-off for `saturated` (ADR §9 Q2); per-turn vs per-task budget (ADR §9 Q1); reviving the dead FR-065 batch-short-circuit control (§2.4 F1).

---

## 2. Existing codebase context

### 2.1 Symbols involved (verified)

| Symbol | Role |
|---|---|
| `pkg/agent/loop.go::AgentLoop.runTurn` | Holds the dispatch loop `for i, tc := range normalizedToolCalls`. All three denial sites live inside it. |
| `pkg/agent/loop.go:8819` (TOCTOU deny) | Site 1 — policy flipped to `deny` at exec time. `message` already true; gains the marker only. Writes **no** transcript entry. |
| `pkg/agent/loop.go:8851` (headless `AutoDenyAsk`) | Site 2 — FR-009/#264. Fixed literal reason, false `message`. |
| `pkg/agent/loop.go:8938` (ask denied) | Site 3 — the defect. `denialReason` verbatim from `CheckGrantOrRequestApproval`. |
| `pkg/agent/loop.go:7625-7639` (tool-assembly duplicate) | Site 4 of `recordSyntheticDeny`. **Both branches `return` from `runTurn`** — see §3.2. |
| `pkg/agent/loop.go::AgentLoop.abortTurn` | `fmt.Errorf("turn aborted during %s: %s", stage, reason)`; emits `EventKindError`; appends an error transcript; returns `turnResult{status: TurnEndStatusAborted}, err`. |
| `pkg/agent/turn.go:114` | `mu sync.RWMutex` — **RWMutex**, not Mutex (N3). |
| `pkg/agent/turn.go:355-360` | `syntheticErrorCount int` — deleted by this spec. |
| `pkg/agent/approval_transcript.go::askDenialText` | Human renderer. Branches on `timeout`/`user`/`cancel`/`saturated`/`""`, else `"Not run: " + reason`. |
| `pkg/agent/approval_transcript.go::settleAskToolCallTranscript` | Writes `session.ToolCall.Result = {error, text, reason}`. Called by sites 2 and 3 only. |
| `pkg/agent/tool_approver.go:65` | `const nopApproverDenialReason = "no_approver_configured"`. |
| `pkg/gateway/approvals.go` | The denial reason literals (§2.2). `::fireTimeout` fails closed — unchanged. |
| `pkg/gateway/policy_approver.go:33` | `newPolicyApproverAdapter(reg *approvalRegistryV2, ws *WSHandler)` — the only real `PolicyApprover`. |
| `pkg/gateway/policy_approver.go:58` | `return false, "internal_error"` — a denial reason the ADR missed (M5). |
| `pkg/agent/task_executor.go::TaskExecutor.finishTaskRun` / `::failTask` | Where a turn error becomes `Task.Result`. |

### 2.2 The denial-reason enumeration — **six**, not seven

`grep -n 'Reason:\s*"' pkg/gateway/approvals.go`, executed in-session, returns seven lines. **One of them is not a denial:**

| Line | Literal | Denial? |
|---|---|---|
| `:296` | `saturated` | yes |
| `:342` | `timeout` (`fireTimeout`) | yes |
| `:380` | `approved` | **no — `Approved: true`** |
| `:383` | `user` | yes |
| `:386` | `cancel` | yes |
| `:424` | `batch_short_circuit` | yes |
| `:483` | `restart` | yes |

Six denial reasons in `approvals.go`. Revision 1's "seven approver reason literals" (FR-058-04) counted `approved` as a denial — a classifier asserting `known == true` for `approved` would have been asserting a lie. **Corrected.**

Two further reasons are produced **outside** `approvals.go` and must be in the table:

- `internal_error` — `pkg/gateway/policy_approver.go:58`, returned when `requestApproval` yields a nil entry.
- `cancel` (again) — `policy_approver.go`'s `ctx.Done()` branch. Same literal, already covered.
- `no_approver_configured` — `pkg/agent/tool_approver.go:65`.

Plus two loop-side pseudo-reasons: the policy-deny path (site 1, which emits **no** `reason` field at all) and the headless auto-deny literal `"auto-denied: ask-policy tool not allowed in a headless scheduled run"` (site 2, read at `loop.go:8850`).

### 2.3 Corrections to revision 1 of this spec

| # | Revision-1 claim | Verified reality |
|---|---|---|
| **X1** | C7: *"the `env:` tag is decorative: no reflection-based env loader consumes `env:` tags in `pkg/config`"*. | **FALSE.** `pkg/config/config.go:29` imports `github.com/caarlos0/env/v11`; `config.go:3723` calls `env.Parse(cfg)`; `grep -c 'env:"' pkg/config/config.go` → **214**. The tag is live. The field is deleted outright by this spec so the point is moot for the outcome — but the *methodology* error is not moot: revision 1 asserted a negative it had not searched for. This spec states no unchecked negative. |
| **X2** | FR-058-04: *"the seven approver reason literals"*. | Six. `approved` is not a denial (§2.2). |
| **X3** | §2.3: *"Precedent harnesses that already construct a real registry + adapter: `approvals_test.go`, `approvals_adr057_test.go`, `approval_grant_survival_fix_test.go`."* | **FALSE for the adapter.** `grep -rn newPolicyApproverAdapter --include=*.go .` returns exactly two hits: the definition (`policy_approver.go:33`) and the single production wiring (`gateway.go:2958`). **No test anywhere constructs it.** The *registry* half is true. Feasibility is now demonstrated rather than cited — §3.7. |
| **X4** | D1 table / FR-058-02: nine rows, `batch_short_circuit` drivable. | Ten rows (adds `internal_error`, `""`). `cancelBatchShortCircuit` has **zero production callers** — §2.4 F1. |
| **X5** | R1(b): fix FR-084's inverted sentinel via `int` → `*int`. | Superseded: FR-084 is deleted (§3.2). The `*int` work item and its `pkg/config/synthetic_floor_test.go` shape guard no longer exist. |
| **X6** | §5: *"a tool only reaches quarantine after 10 denials, which a granted tool cannot produce"* — used to argue standing grants are unaffected. | **No longer true** under quarantine-at-first. §5 restates the real, narrower guarantee and names the accepted loss. |
| **X7** | FR-058-08/§9: ledger keyed `(tool, reason)`, but `quarantinedDenial(tool)` keyed by tool alone. | Key-shape mismatch (N4). Resolved by making the quarantine map keyed by **tool alone** and the budget **unkeyed** — §3.4. |

### 2.4 Findings filed, not fixed here

- **F1 — FR-065's batch-short-circuit control is dead in production.** `grep -rn cancelBatchShortCircuit --include=*.go .` returns its definition (`pkg/gateway/approvals.go:408`), its doc comment, and **test files only** (`approvals_test.go:89`, `rest_tool_registry_test.go:471,472,500,620`). Nothing in production calls it, so the `batch_short_circuit` reason cannot be produced by a real denial flow. It is still classified (a reason that exists in the type must classify), but **AC-01 must not demand a driven denial for it** — §6 BDD-02 covers it at the classifier, and §8 drives it at the registry (`reg.cancelBatchShortCircuit` directly, as `approvals_test.go:89` already does), never through a turn. **File as a separate issue: an FR-065 control with no caller is a zombie under Constraint #6.**
- **F2 — `pkg/audit::EmitTurnAbortedSyntheticLoop` already has zero callers.** `loop.go:9815` emits the event via `audit.EmitEntry` directly, not through the helper. Deleted with FR-084 (§3.3).
- **S1 — the lifecycle record loses the reason.** `finishTaskRun` calls `transitionTaskLifecycle(sid, session.LifecycleFailed, "execution_error")` with a hardcoded literal. `Task.Result` carries the reason; the lifecycle store never learns it. File an issue.
- **S2 — `task.Task` has no structured failure enum**, unlike `plan.FailedReason`'s six discriminated causes. File an issue.
- **N2 — `abortTurn` can lose the denial reason.** `abortTurn`'s first branch: if `ts.restoreSession` fails it returns `turnResult{}, err` where `err` is the *restore* error, and the denial reason never reaches `Task.Result`. This makes AC-04 non-deterministic in principle. It is **not fixed here** (it is a pre-existing property of every `abortTurn` caller, not something this change introduces). §8 pins it: the AC-04 test asserts `restoreSession` succeeded, so a restore failure surfaces as a test error rather than a silent miss.

### 2.5 Import-direction constraint (drives test placement)

`pkg/gateway/gateway.go` imports `pkg/agent`. Therefore **`pkg/agent` cannot import `pkg/gateway`**, and the real `PolicyApprover` is unreachable from `pkg/agent` tests. Consequence, binding: **every criterion asserting on real approval-registry state (AC-02, AC-03, AC-06) is a `pkg/gateway` test.** Revision 1 got this direction right; only its precedent citation was false (X3). §3.7 replaces the citation with a demonstrated construction.

---

## 3. Design resolutions

### 3.1 R1 — Quarantine engages on the FIRST permanent denial

**Decision.** A denial classified `Permanent: true` quarantines its tool **immediately, on first occurrence**. That is what "permanent" means; D1/D2/D3 already imply it.

- The **turn continues**. Quarantine is not termination.
- Every subsequent call to that tool in that turn is answered from the **cached denial payload**: no `hooks.BeforeTool`, no `resolveToolPolicyAtExec`, no `CheckGrantOrRequestApproval`, no `RequestApproval`, no `tool_approval_required` frame.

**Why this and not "quarantine at the ceiling" (revision 1 / ADR D4 as written).** Two defects, both fatal:

1. **The mechanism could not be observed.** Quarantine engaged at the same count that terminated the turn, so the quarantine map and its gate had a lifetime of zero dispatches. Nothing could assert that a *short-circuit* ever happened, because the turn ended on the same denial that created it. (B2)
2. **The arithmetic was impossible.** ADR D4 #3 exists to stop each attempt costing a full approval window — but with quarantine at 10, attempts 1…10 each open a real approval round-trip. At the shipped 600 s default that is **100 minutes**. ADR AC-02 simultaneously demands the turn be bounded by "a stated multiple of one approval window, **not** by 10 windows". D4-as-written and AC-02 cannot both hold. (B3)

Quarantine-at-first caps wall clock at **~one approval window per tool**, which makes AC-02 satisfiable, and gives the quarantine gate a real, assertable lifetime.

**Accepted losses, stated:**

- A `user` denial is a decision about *this call with these arguments*. Quarantining the whole tool for the turn is **broader** than the human's decision: a later, legitimately different call to the same tool is refused without asking. Accepted — ADR D1 #1 already holds that re-asking after a human "no" is an agent overriding a human, and the alternative is the unbounded loop of §1.1.
- A standing "Always Allow" grant issued *after* a tool is quarantined does not un-quarantine it for the remainder of that turn (X6). The next turn is unaffected.

### 3.2 R2 — FR-084 is DELETED, not narrowed

**Decision.** Delete: `config.GatewayConfig.TurnSyntheticErrorFloor`, `defaultSyntheticErrorFloor`, `AgentLoop.syntheticErrorFloor`, `AgentLoop.recordSyntheticDeny`, `turnState.syntheticErrorCount`, all four call sites, the `synthetic_error_floor` abort stage, `audit.EventTurnAbortedSyntheticLoop`, `audit.EmitTurnAbortedSyntheticLoop`, and the tests that exercise them.

**Why deletion and not the revision-1 `int` → `*int` repair.** Revision 1 proposed keeping FR-084 for the one non-denial call site (tool-assembly duplicate) and arming it by inverting the sentinel. Read that call site:

```go
// pkg/agent/loop.go:7625-7639
if dedupErr := al.checkToolDedupInvariant(ts, policyFilteredTools); dedupErr != nil {
    …
    if shouldAbort, abortMsg := al.recordSyntheticDeny(ts); shouldAbort {
        turnStatus = TurnEndStatusAborted
        return al.abortTurn(ts, "synthetic_error_floor", abortMsg)     // returns
    }
    turnStatus = TurnEndStatusError
    return turnResult{status: TurnEndStatusError, finalContent: denyMsg}, dedupErr   // also returns
}
```

**Both branches `return` from `runTurn`.** The counter can therefore reach at most **1** at this site. A floor of 8 is unreachable — `recordSyntheticDeny` would return `(false, "")` on the only call it can ever receive, and the turn would end via the second `return` anyway. Repairing the sentinel would produce a **documented-live, permanently-dead control**: exactly the defect filed as **#595**, re-created by the fix for #595.

A dead control is worse than no control: it occupies the design space, it appears in `config.go`'s documentation, and it invites the next author to reason about a bound that cannot bind (Constraint #6 — no zombie mechanisms; Constraint #7 — no shipped known-dead code).

**Nothing is lost.** The tool-assembly-duplicate branch already terminates the turn unconditionally via its second `return`. Removing the abort branch removes a path that could never be taken.

**#595 is resolved by deletion, not by repair.** The issue's subject — a config field documenting a default of 8 that the code cannot produce from an unset value — ceases to exist because the field ceases to exist. *(Do not edit the issue from this spec; the PR closes it.)*

**The new aggregate budget (§3.4) subsumes FR-084's stated purpose** — bounding a turn that produces repeated synthetic denials — and does so with a mechanism that can actually fire.

### 3.3 R3 — Audit event swap

`audit.EventTurnAbortedSyntheticLoop` (`pkg/audit/events.go:96`, catalogued at `pkg/audit/audit.go:135`) is emitted only by `recordSyntheticDeny`. Deleting FR-084 while keeping the event leaves an audit event nothing emits — the same zombie class §3.2 exists to avoid.

**Decision.** Delete `EventTurnAbortedSyntheticLoop`, its catalog entry, the already-dead `EmitTurnAbortedSyntheticLoop` helper (F2) and their assertions in `pkg/audit/events_test.go:94,126`. Add `EventTurnAbortedToolDenialBudget = "turn.aborted_tool_denial_budget"` with a catalog entry, emitted by the new abort path via `audit.EmitEntry` (same shape as the code it replaces).

**Scope note, flagged for operator veto:** the operator's deletion list did not name the audit event. This spec extends the list to it, with the rationale above. If the operator prefers to keep the old event string for log continuity, say so — the alternative is to rename its *constant* while keeping the wire string, which preserves historical greppability at the cost of a misleading name.

### 3.4 R4 — The budget is AGGREGATE, per turn, across all denials

**Decision.** `const turnDenialBudget = 10` in `pkg/agent/tool_denial.go`. It counts **every denial response handed to the model in the turn**, regardless of tool, regardless of reason, including responses served from the quarantine cache. On the 10th, the turn terminates via `abortTurn`.

**Why aggregate and not per-`(tool, reason)`.** Per-pair counting leaves a hole the ADR's own evidence describes. ADR §1.2 quotes the UAT: agents were cycling through **2–3 distinct denied tools each** ("bash denied, delegate/run_task also denied"). Under per-pair counting that is 20–30 attempts before any pair reaches its ceiling. Revision 1 defended this with the claim that *"heterogeneous denial storms are not the observed pathology"* — **that claim is false against the ADR's own §1.2 data** and is withdrawn (M7). Aggregate counting closes the hole and is strictly simpler: one integer, no key.

**Counting quarantine replays is deliberate.** If short-circuited responses did not count, a model repeating a quarantined tool would be bounded only by `MaxIterations` (200, hard 2×) — cheap in wall-clock, but 200 real LLM calls in tokens. Counting them means a storm ends in ten denial responses. The cost is that a single LLM response containing 12 calls to one quarantined tool terminates the turn at the 10th; §6 BDD-05 asserts exactly that arithmetic rather than papering over it.

**`saturated` shares the budget (M8, stated explicitly).** A turn whose only denials are `saturated` will terminate on the 10th — even though every one of them was retryable and none was refused on its merits. **This is accepted.** Ten consecutive saturation denials in one turn means the approval queue has been full for the entire turn; continuing to retry into a queue that is not draining is the same unbounded-loop shape this ADR exists to stop, and terminating with a reason naming the tool and `saturated` is a strictly better outcome than a turn that never ends. The positive lower bound (§3.5) guarantees this never penalises a *recovering* queue.

### 3.5 R5 — Binding Rule 4: the positive lower bound

`saturated` is the **one** retryable reason. A `saturated` denial:

- does **not** quarantine the tool,
- consumes **one** unit of the aggregate budget,
- and leaves a later call to the same tool in the same turn able to reach the approver and **execute successfully**.

AC-06 asserts a real execution, not merely the absence of quarantine (§8). Without this bound, an implementation that quarantined on the first denial of *every* reason — or classified everything permanent — would satisfy AC-01 through AC-05 completely.

### 3.6 R6 — Constraint #8 scope (unchanged from revision 1; re-confirmed)

**IN SCOPE — no `contracts/` change, no `scripts/gen-contracts.sh` run:**

1. **The `denyMsg` provider message** → `providers.Message{Role:"tool"}` → the LLM and `Sessions.AddFullMessage`. `pkg/gateway/*.go` (non-test) contains zero reads of the message store; the deny branches `continue` before the tool-result path, so no `tool_call_result` frame is produced. Not a wire format.
2. **`session.ToolCall.Result`** — `permanent` goes **inside** `result`. `contracts/components/schemas/ToolCall.yaml`'s `result` is `type: object` + `additionalProperties: true`, and `ToolCallResultFrame.result` generates to `result: z.unknown()`. **Hard constraint: `permanent` MUST NOT become a top-level `ToolCall` field** — `ToolCall` itself is `additionalProperties: false`, which would make that a contract change.
3. **`ToolExecSkippedPayload.Reason`** — internal event bus only.
4. **Deleting `Gateway.TurnSyntheticErrorFloor`** — zero references in `contracts/` and `src/`; backend-only config. *(Deleting a JSON key is invisible to a config file that never contained it; `encoding/json` ignores unknown keys, so an operator who did hand-write it sees the key become inert rather than a boot failure. **[UNVERIFIED]** that no strict-decode path rejects unknown gateway keys — W4 must run one boot with the key present before merge.)*

**OUT OF SCOPE — explicitly:** `pkg/tools/result.go::DelegationDeniedResult` → `generated.DelegationFailure` (a generated Go struct from an `additionalProperties: false` schema; needs the full 5-step pipeline) and `pkg/tools/fserrors.go::PermissionDeniedResult` (plain string, no schema).

**AC-09 reduces to:** `make verify-contracts` exits 0, the diff touches no file under `contracts/`, `pkg/api/generated/` or `src/lib/api/generated/`, **and** `permanent` is nonetheless present inside a persisted `ToolCall.Result` — the paired positive assertion that stops AC-09 passing on a no-op (§8).

### 3.7 R7 — The `pkg/gateway` harness: demonstrated, not cited

Revision 1 cited three precedent harnesses that do not exist (X3). Rather than assert feasibility again, here is the construction, every piece verified in-session:

| Piece | Verified availability |
|---|---|
| Real registry | `newApprovalRegistryV2(maxCap int, timeout time.Duration)` — `approvals.go:207`. Already constructed by `approvals_test.go:30`, `cancel_handler_test.go:24`, `approval_grant_survival_fix_test.go:93`, `rest_tool_registry_test.go:42`. |
| Real adapter | `newPolicyApproverAdapter(reg, ws)` — `policy_approver.go:33`. Same package as the tests; no export needed. |
| Non-nil `*WSHandler` | `policy_approver.go:63` calls `a.wsHandler.broadcastToolApprovalRequired(entry)` **unconditionally** when `accepted == true`, so the field must be non-nil. A **zero-value `&WSHandler{}` suffices**: `broadcastToolApprovalRequired` guards `entry == nil`, then takes `h.mu` (valid zero-value mutex) and ranges `h.sessions` (nil map → zero iterations) — no panic, broadcast is a no-op. Precedent for zero-value construction already exists: `rest_devices_test.go:84` (`&WSHandler{agentLoop: …}`), `sprint_h_forwarder_test.go:55`, `orphan_watchdog_liveness_test.go:162`. |
| Real turn | `pkg/gateway/test_agent_loop_helper_test.go:70::mustAgentLoop` — a gateway helper that calls `agent.NewAgentLoop` and (per its own doc comment) exists precisely for *"any pkg/gateway test that actually runs a real turn (not merely constructs an AgentLoop)"*, seeding ADR-046 workspace membership so `resolveTurnWorkDirOrRefuse` does not refuse. |
| Scripted provider | `pkg/agent/testutil::NewScenario` — package doc: *"intentionally not a `_test.go` file so it can be imported from any `_test.go` in the repo without import cycles."* Its only non-stdlib import is `pkg/providers`, so importing it from `pkg/gateway` creates no cycle. |

So the wiring is one line, using only in-package identifiers and one existing helper:

```go
reg := newApprovalRegistryV2(64, 1*time.Second)
al  := mustAgentLoop(t, cfg, msgBus, testutil.NewScenario()./* … */)
al.SetToolApprover(newPolicyApproverAdapter(reg, &WSHandler{}))
```

**No new harness infrastructure is priced, because none is required.** If any piece fails on first use, that is a spec defect to report — not a licence to substitute a spy (§8's verification bar).

### 3.8 R8 — Turn-termination mechanics (unchanged; re-confirmed hop by hop)

```
runTurn (turnResult, error)
  └─ runAgentLoop (string, error)         ← turnResult dies here; error preserved verbatim
       └─ processTaskDirect (string, error)
            └─ TaskExecutor.runTask → finishTaskRun(…, err, "")
                 └─ err != nil ⇒ failTask(t.ID, fmt.Sprintf("execution error: %v", err))
                      └─ task.Store.Update{Status: failed, Result: <that string>, CompletedAt}
```

`turnResult` carries no reason field and `pkg/agent` defines no custom error type, but the error **string** propagates intact into `Task.Result` (capped at 50 000 chars). **Decision: no plumbing change.** Termination is:

```go
al.abortTurn(ts, "tool_denial_budget", toolDenialAbortReason(tool, reason, ts.agentID, turnDenialBudget))
```

producing exactly:

```
Task.Result == `execution error: turn aborted during tool_denial_budget: blocked: tool "bash" denied (timeout) for agent "mia" — the turn reached its limit of 10 tool denials`
```

Distinguishable from a human Stop by two independent assertions: `Task.CancelReason == ""` (a Stop sets `CancelReasonStoppedByUser`) and `Task.Result` not carrying `pkg/agent/plan_engine.go::memberCancelReasonMarker` (`"[reason:stopped_by_user]"`). Caveat N2 (§2.4) applies and is pinned by the test.

---

## 4. Functional requirements

| FR | Requirement |
|---|---|
| **FR-058-01** | One classifier `ClassifyDenial(reason string) (DenialClass, bool)` in `pkg/agent/tool_denial.go` is the sole source of denial semantics. `DenialClass{Reason string; Permanent bool; ModelMessage, TranscriptText string}`. The bool is `known` — false for a reason with no table row. |
| **FR-058-02** | The table has exactly the ten rows of §4.1. **Exactly one row (`saturated`) is `Permanent: false`.** `approved` is **not** a row — it is not a denial (§2.2). |
| **FR-058-03** | An unknown reason returns `known == false`, `Permanent: true`, and `ModelMessage == TranscriptText` (byte-identical on both surfaces — AC-07's divergence guard). |
| **FR-058-04** | The six denial reason literals in `pkg/gateway/approvals.go` become named constants collected in one exported-to-package slice `allApprovalDenialReasons`. `internal_error` (`policy_approver.go:58`) is included. `approved` is **excluded**. A `pkg/gateway` test asserts `agent.ClassifyDenial(r)` returns `known == true` for every member, so a new reason with no classification fails a test rather than defaulting silently. `fireTimeout` is byte-for-byte unchanged. |
| **FR-058-05** | All three `permission_denied` payloads are built by one function `denialPayloadJSON(tool, reason string, cls DenialClass) string` emitting `{"error":"permission_denied","message":<cls.ModelMessage>,"tool":<tool>,"reason":<reason>,"permanent":<bool>}`. Site 1 (policy deny) emits `reason: "policy_denied"` where it previously omitted the field. |
| **FR-058-06** | The literal `User denied tool execution.` appears **nowhere** in `pkg/` after this change (today: 2 occurrences, `loop.go:8851` and `:8938`). The case-insensitive substring `user denied` appears in exactly **one message string** — the `user` row's `ModelMessage` in `pkg/agent/tool_denial.go` (§4.2). One pre-existing **comment** also contains it (`pkg/audit/events.go:46`, describing an audit event); it is out of scope and is the single verified exception the DoD grep allows. |
| **FR-058-07** | `approval_transcript.go::askDenialText` returns `ClassifyDenial(reason).TranscriptText` and holds no `switch` of its own. The `""` row preserves its current persisted string verbatim (N1). |
| **FR-058-08** | `settleAskToolCallTranscript` adds `"permanent": cls.Permanent` **inside** `session.ToolCall.Result`, never as a top-level `ToolCall` field. |
| **FR-058-09** | `turnState` gains **two** fields, both guarded by `ts.mu` taken with **`Lock()`, not `RLock()`** (N3): an unkeyed `denialsUsed int` and `quarantined map[string]quarantinedDenial` keyed by **tool name alone** (N4 — the gate runs before the reason is known). A fresh `turnState` has zero and nil/empty; no cross-turn, no cross-session carry-over. |
| **FR-058-10** | A denial classified `Permanent: true` quarantines its tool on its **first** occurrence, caching `{reason, payload}`. `Permanent: false` (`saturated`) never quarantines. |
| **FR-058-11** | A quarantine gate sits in `runTurn`'s dispatch loop immediately after `toolName`/`toolArgs` resolution and **before** the `hooks.BeforeTool` block, the TOCTOU re-check and the approval path. A quarantined tool is answered from the cached payload with no hook call, no policy re-resolution, no `CheckGrantOrRequestApproval`, no `RequestApproval`, and no `tool_approval_required` frame. |
| **FR-058-12** | `const turnDenialBudget = 10`, aggregate per turn across all tools and reasons. Every denial response handed to the model — real **or** served from the quarantine cache — consumes one unit. |
| **FR-058-13** | On the 10th, the turn terminates via `abortTurn(ts, "tool_denial_budget", toolDenialAbortReason(...))`. The reason names the tool, the denial reason, the agent id and the budget. Denials 1–9 do **not** terminate. |
| **FR-058-14** | FR-084 is deleted in full: `config.GatewayConfig.TurnSyntheticErrorFloor`, `defaultSyntheticErrorFloor`, `AgentLoop.syntheticErrorFloor`, `AgentLoop.recordSyntheticDeny`, `turnState.syntheticErrorCount`, all four call sites, the `synthetic_error_floor` abort stage, and every comment referencing them. |
| **FR-058-15** | `audit.EventTurnAbortedSyntheticLoop` and `audit.EmitTurnAbortedSyntheticLoop` are deleted with their catalog entry and tests; `audit.EventTurnAbortedToolDenialBudget = "turn.aborted_tool_denial_budget"` replaces them in the catalog and is emitted by the FR-058-13 abort. |
| **FR-058-16** | `saturated` remains retryable end to end: below the budget it does not quarantine, and a later call to the same tool in the same turn reaches the approver and **executes** (AC-06). |
| **FR-058-17** | No file under `contracts/`, `pkg/api/generated/` or `src/lib/api/generated/` is modified; `make verify-contracts` exits 0. |

### 4.1 The classification table (normative)

| # | Reason | Source | `Permanent` | Reachable by a real turn? |
|---|---|---|---|---|
| 1 | `user` | `approvals.go:383` | **true** | yes |
| 2 | `timeout` | `approvals.go:342` | **true** | yes |
| 3 | `saturated` | `approvals.go:296` | **false** | yes |
| 4 | `cancel` | `approvals.go:386`, `policy_approver.go` ctx branch | **true** | yes |
| 5 | `restart` | `approvals.go:483` | **true** | yes |
| 6 | `batch_short_circuit` | `approvals.go:424` | **true** | **no — dead control (F1)**; registry-drivable only |
| 7 | `internal_error` | `policy_approver.go:58` | **true** | defensive branch; **[UNVERIFIED]** whether reachable in practice |
| 8 | `no_approver_configured` | `tool_approver.go:65` | **true** | yes |
| 9 | `policy_denied` *(loop pseudo-reason, site 1)* | `loop.go:8819` | **true** | yes. **No transcript consumer** — site 1 does not call `settleAskToolCallTranscript`, so this row's `TranscriptText` is unused today and no FR claims it renders. |
| 10 | `""` *(empty reason)* | `askDenialText`'s existing `""` branch | **true** | **[UNVERIFIED]** reachability. Row exists to preserve the persisted string (N1). |

Nine of ten permanent. That is the finding, not a design choice.

### 4.2 Pinned strings (single source; every assertion and grep references these)

**The `user` model message — pinned once, referenced three times (M2):**

```
The user denied this tool call. Do not retry it; stop and report the blocker.
```

Exported for tests as `agent.DenialMessageUser`. The three consumers that must reference **this same literal**, not a paraphrase:

- **BDD-01's negative assertion** — `!strings.Contains(strings.ToLower(msg), denialUserMarker)`
- **BDD-02's positive assertion** — only reason `user` satisfies `strings.Contains(strings.ToLower(m), denialUserMarker)`
- **The DoD grep** — §10

where `const denialUserMarker = "user denied"` (lower-case), which appears in `DenialMessageUser` and, by FR-058-06, nowhere else in `pkg/`.

**The `""` row's transcript text — preserved verbatim (N1):** `Not run: the approval request was not granted.`

**The abort reason format — pinned by test:**

```
blocked: tool %q denied (%s) for agent %q — the turn reached its limit of %d tool denials
```

**Unknown-reason identity string (FR-058-03), used for both surfaces:**

```
Not run: the tool call was refused (reason: %s). Treat this as permanent; do not retry — stop and report the blocker.
```

Remaining rows' wording is the implementer's, subject to two invariants: `TranscriptText` keeps the existing `"Not run: …"` reader-facing form, and `ModelMessage` names the productive next step (ADR D2/D3).

---

## 5. Behavioural contract & explicit non-behaviours

**Observable:**
- Every `permission_denied` payload reaching the model carries `permanent` and a message true for its reason.
- The transcript card and the model message for one denial event come from one table row.
- A permanently-denied tool costs **at most one** approval window per turn.
- A turn producing 10 denial responses ends by itself; a plan task lands `failed` with a `Result` naming tool + reason + agent.

**Explicit non-behaviours (must not happen):**
- **D5 is REJECTED.** The tool is NOT removed from `tools[]` or the provider tool defs mid-turn. `FilterToolsByPolicy`'s output is untouched; the advertised capability set is stable for the whole turn. Denied calls are short-circuited, **not un-offered**.
- `fireTimeout` still fails closed. Nothing here converts an unanswered approval into an approval.
- No back-off is introduced for `saturated`.
- No new WS frame, no new REST field, no new persisted top-level field.
- **Standing "Always Allow" grants — the honest statement (replaces revision 1's false one, X6).** The quarantine gate sits *before* the grant check, so a grant recorded **before** the first denial is unaffected (the tool is never denied, never quarantined). A grant recorded **after** a tool is quarantined does **not** un-quarantine it for the remainder of that turn. This is a real, accepted narrowing, not a no-op.

---

## 6. BDD scenarios

**BDD-01 — timeout no longer claims a user decided (FR-058-01/02/05/06 · AC-01, AC-02)**
```gherkin
Given an agent whose effective policy for "run_task" is "ask"
  And a real approvalRegistryV2 + real policyApproverAdapter whose window expires unanswered
When the agent calls run_task
Then the tool message the model receives parses as JSON with error="permission_denied"
  And its "reason" is "timeout"
  And its "permanent" is true
  And the lower-cased "message" does not contain "user denied"
  And its "message" tells the agent to stop and report the blocker
```

**BDD-02 — every classified reason, table-driven (FR-058-02/03/04 · AC-01)**
```gherkin
Given the six approvals.go denial reasons, internal_error, no_approver_configured,
      the policy_denied pseudo-reason and the empty reason
When each is classified
Then every one returns known=true
  And exactly one ("saturated") is permanent=false
  And only reason "user" yields a message whose lower-case form contains "user denied"
Given the literal "approved"
Then ClassifyDenial returns known=false — it is not a denial
Given "__unclassified_probe__"
Then known=false, permanent=true, and ModelMessage == TranscriptText byte-for-byte
```

**BDD-03 — one renderer, two audiences (FR-058-07/08 · AC-07)**
```gherkin
Given a real denial with reason "restart"
When the transcript entry and the model message are both produced
Then the persisted ToolCall.Result.text equals ClassifyDenial("restart").TranscriptText
  And the model payload's message equals ClassifyDenial("restart").ModelMessage
  And those two strings are NOT equal          # known rows differ by design
  And the persisted ToolCall.Result contains "permanent": true inside result
Given instead a reason with no table row
Then the transcript text and the model message ARE byte-identical
```

**BDD-04 — quarantine at the FIRST permanent denial; the turn continues (FR-058-10/11 · AC-03)**
```gherkin
Given a real approval registry and a tool whose policy is "ask"
  And nobody answers, so the first request denies with reason "timeout"
When the agent calls that tool a second and third time in the same turn
Then the registry created exactly ONE approval entry for that tool in the whole turn
  And zero tool_approval_required broadcasts occurred for calls 2 and 3
  And calls 2 and 3 still received a permission_denied payload identical to call 1's
  And the turn did NOT abort on call 1
  And total elapsed wall-clock is under 3 x the configured approval window
```

**BDD-05 — the aggregate budget fires at 10, not 9, and covers a quarantined storm (FR-058-12/13 · AC-05)**
```gherkin
Given a config built from Go zero values only (nothing hand-edited)
  And a single LLM response containing 12 calls to the same ask-policy tool
When the turn processes that batch
Then call 1 opens exactly one real approval round-trip and quarantines the tool
  And calls 2..10 are answered from the cache with no approval round-trip
  And on the 10th denial the turn aborts during stage "tool_denial_budget"
  And the abort reason names the tool, the denial reason and the agent id
  And calls 11 and 12 are never dispatched
```

**BDD-06 — heterogeneous storms are bounded too (FR-058-12 · AC-05)**
```gherkin
Given three distinct tools "bash", "run_task" and "web_fetch", each denied permanently
When the agent cycles through them across a turn
Then the turn aborts on the 10th denial in aggregate
  And it does NOT require 10 denials of any single (tool, reason) pair
```

**BDD-07 — POSITIVE LOWER BOUND: saturated still succeeds on retry (FR-058-16 · AC-06)**
```gherkin
Given a real approval registry with max_pending = 1
  And one approval already pending, holding the only slot
When the agent calls "web_fetch" and is denied with reason "saturated"
Then the payload's permanent is false
  And "web_fetch" is NOT in the turn's quarantine map
  And exactly 1 unit of the denial budget has been consumed
When the held approval resolves and the queue drains
  And the agent calls "web_fetch" again and it is approved
Then web_fetch's Execute actually ran (asserted on the tool's own atomic counter)
  And its real result text appears in the turn's output
  And the turn completes normally without aborting
```

**BDD-08 — the plan task explains itself (FR-058-13 · AC-04)**
```gherkin
Given a plan task assigned to agent "mia" whose tool "bash" is denied every time
When the turn exhausts the denial budget and terminates
Then the task's status is "failed"
  And its Result contains "bash" and "timeout" and "mia"       # three distinct fixture values
  And its CancelReason is empty
  And its Result does not contain "[reason:stopped_by_user]"
```

**BDD-09 — FR-084 is gone, and gone behaviourally (FR-058-14/15 · AC-08)**
```gherkin
Given the whole repository
Then the identifiers recordSyntheticDeny, syntheticErrorFloor, defaultSyntheticErrorFloor,
     syntheticErrorCount, TurnSyntheticErrorFloor and EventTurnAbortedSyntheticLoop resolve nowhere
Given a turn producing 10 denials
Then it aborts with the tool-naming reason
  And no session message anywhere in the turn contains "synthetic_error_loop"
  And the audit log carries event "turn.aborted_tool_denial_budget"
```

**BDD-10 — counters are per-turn (FR-058-09)**
```gherkin
Given a turn in which one tool was denied 9 times
When that turn ends and a new turn starts in the same session
Then the new turn's denialsUsed is 0 and its quarantine map is empty
  And 9 further denials in the new turn do not abort it
Given a second, unrelated session running concurrently on the same AgentLoop
Then its counters are unaffected by either turn
```

---

## 7. Traceability matrix

| FR | ADR | BDD | Test (package::file) | AC |
|---|---|---|---|---|
| FR-058-01/02/03 | D1 | BDD-02 | `pkg/agent::tool_denial_test.go` | AC-01 |
| FR-058-04 | D1 | BDD-02 | `pkg/gateway::approval_denial_classification_test.go` | AC-01 |
| FR-058-05/06 | D1, D2 | BDD-01 | `pkg/agent::loop_tool_denial_test.go` | AC-01 |
| FR-058-07/08 | D1 | BDD-03 | `pkg/agent::approval_transcript_denial_test.go` | AC-07 |
| FR-058-09 | §10.A2 | BDD-10 | `pkg/agent::tool_denial_test.go` | AC-05 |
| FR-058-10/11 | §10.A1 | BDD-04 | `pkg/gateway::tool_denial_quarantine_test.go` | AC-03, AC-02 |
| FR-058-12/13 | §10.A2 | BDD-05, BDD-06 | `pkg/agent::loop_tool_denial_test.go` | AC-05 |
| FR-058-13 | §10.A1 | BDD-08 | `pkg/agent::task_executor_tool_denial_test.go` | AC-04 |
| FR-058-14/15 | §10.A3, #595 | BDD-09 | `pkg/agent::loop_tool_denial_test.go`, `pkg/audit::events_test.go` | AC-08 |
| FR-058-16 | D1 #3 | BDD-07 | `pkg/gateway::tool_denial_saturated_retry_test.go` | **AC-06** |
| FR-058-17 | §7 | — | `make verify-contracts` + diff check + BDD-03's `permanent` assertion | AC-09 |
| — (regression) | §1.1 | BDD-01 + BDD-04 | `pkg/gateway::tool_denial_timeout_regression_test.go` | **AC-02** |

Every ADR §8 criterion AC-01…AC-09 appears at least once.

---

## 8. TDD plan

### 8.1 Verification bar (non-negotiable — ADR §8, ADR-057 §10)

- **No spy that records its argument and returns a canned value.** Where registry state is asserted, use the real `approvalRegistryV2` and assert on entry/`pendingCount` state, never a call log. Where a `pkg/agent`-side approver is unavoidable, its outcome must be produced by real state it owns (a bounded queue that genuinely saturates and genuinely drains), not a recorder.
- **Real store-backed state and real turn registration.** Shape references: `pkg/agent/task_executor_no_per_agent_cap_test.go`, `pkg/gateway/test_agent_loop_helper_test.go::mustAgentLoop`.
- **Distinct fixture values everywhere.** Agents `mia` (denied) and `jim` (allowed); tools `bash` (permanent path) and `web_fetch` (saturated path); reason `timeout`. Never `"a"`/`"a"` — a test must not be able to pass by conflating two values.
- Tests touching `ts.mu`-guarded state or the registry run with `-race`.

### 8.2 Stub-resistance — for every AC, the false-pass it excludes

> The red-team's central charge against revision 1 was that AC-03 and AC-08 passed against an implementation that did nothing. Each row names the specific stub that would falsely pass, and the assertion that kills it.

| AC | An implementation that would falsely pass | Excluded by |
|---|---|---|
| **AC-01** | A classifier returning `{Permanent:true}` for every reason. | BDD-02 asserts **exactly one** row is `false` **and** `known==false` for `approved` and for a probe reason. Cross-checked by AC-06's real execution. |
| **AC-02** | Any turn that ends for any reason within the timeout — e.g. one that never dispatches the tool at all. | Assert the registry created **exactly one** (not zero) approval entry for that tool, **and** wall-clock < 3× the window, **and** the abort reason contains all three distinct fixture values. Zero entries fails. |
| **AC-03** | A no-op loop that dispatches nothing, trivially making "zero further `requestApproval`" true. | A **positive lower bound inside the negative criterion**: assert exactly **1** approval entry was created (not 0), and that calls 2–3 still returned a real `permission_denied` payload. Silence fails. |
| **AC-04** | An implementation failing every task with a generic `"execution error: …"`. | Assert `Result` contains all three **distinct** fixture values (`bash`, `timeout`, `mia`) — a generic failure cannot produce them — plus `CancelReason == ""`. Also assert `restoreSession` succeeded, so N2 surfaces as an error rather than a silent miss. |
| **AC-05** | A test that sets the budget explicitly, hiding an unset-means-disabled sentinel (FR-084's exact defect). | Construct the config from **Go zero values only** and assert the abort fires. Plus a source guard: no `config` field governs the budget — it is a bare `const`, which has no unset state (§9 pins this; a future `int` config field is forbidden by FR-058-12). |
| **AC-06** | Quarantining on the first denial of **every** reason, or classifying everything permanent — passes AC-01…AC-05 completely. | Assert the retried tool's `Execute` **actually ran** via its own atomic counter **and** its real result text reaches the turn output. Absence of quarantine alone is not sufficient evidence. |
| **AC-07** | Both surfaces returning `""`, or one constant for everything. | Assert both equal their **pinned non-empty** table values **and** that for a known row they are **different** strings; identity is asserted only for the unknown row. A single-constant implementation fails the "different" leg. |
| **AC-08** | A grep-only assertion that a renamed clone of FR-084 would survive. | Pair the identifier grep with a **behavioural** assertion: 10 denials abort with the tool-naming reason, no session message contains `synthetic_error_loop`, and the audit log carries the new event. |
| **AC-09** | Trivially true if the change did nothing at all. | Pair the empty contract diff with the **positive** assertion that `permanent` is present inside a persisted `ToolCall.Result` (BDD-03). Both must hold. |

### 8.3 Order

1. **Unit — classification (`pkg/agent/tool_denial_test.go`).** Table-driven over all ten rows + `approved` + an unknown probe. Asserts the saturated-is-the-only-retryable invariant, `known` on/off, the unknown-row identity, and the pinned `user` marker uniqueness. Also unit-tests the ledger: aggregate increment, quarantine keyed by tool, permanent-only quarantine, budget at 10 not 9, empty on a fresh `turnState`, `Lock()` not `RLock()` (exercised under `-race` with concurrent mutators).
2. **Unit — renderer parity (`pkg/agent/approval_transcript_denial_test.go`).** `askDenialText` delegates and holds no switch; the previously-missing rows render; the `""` row's string is byte-identical to today's; `settleAskToolCallTranscript` writes `permanent` **inside** `Result` and the entry round-trips through `session.ToolCall`.
3. **Integration — loop behaviour (`pkg/agent/loop_tool_denial_test.go`).** Real `mustNewAgentLoop`, scripted provider, state-driven approver. BDD-01, BDD-05, BDD-06, BDD-09, BDD-10. `-race`.
4. **Integration — registry-backed (`pkg/gateway/`), harness per §3.7:**
   - `tool_denial_quarantine_test.go` — BDD-04 / AC-03. `-race`.
   - `tool_denial_saturated_retry_test.go` — BDD-07 / AC-06. `newApprovalRegistryV2(1, …)`. `-race`.
   - `tool_denial_timeout_regression_test.go` — BDD-01 + AC-02, the §1.1 regression: `newApprovalRegistryV2(64, 1*time.Second)`, nobody answers, assert the turn ends **by itself**, the reason names tool + agent + `timeout`, and elapsed < 3 × the window. No manual Stop anywhere in the test.
   - `approval_denial_classification_test.go` — FR-058-04. Drives `batch_short_circuit` at the registry (`reg.cancelBatchShortCircuit`, precedent `approvals_test.go:89`), **never through a turn** (F1).
5. **Integration — plan task (`pkg/agent/task_executor_tool_denial_test.go`).** BDD-08 / AC-04. Real `task.Store`, real `TaskExecutor`, real dispatch.
6. **Regression strengthening.** `pkg/agent/scenario_runturn_test.go`, `subturn_delegate_nesting_test.go`, `turn_recheck_test.go` — existing `permission_denied` assertions are **kept and extended** to pin `permanent` and the classified message. Nothing needs inverting: no test anywhere pins `"User denied tool execution."` (verified — the literal appears only at `loop.go:8851` and `:8938`).
7. **Deletion.** `pkg/agent/abort_turn_system_test.go::TestRunTurn_SyntheticDenyFloor_AbortsWithSurfacedError` is **deleted**, not edited (see §9 B4 note). `pkg/audit/events_test.go:94,126` updated for the event swap.
8. **Contracts.** `make verify-contracts` + assert the diff touches no generated/contract path.

### 8.4 Running tests in this pod — mandatory

- Build tags `goolm,stdjson` are required or `pkg/channels/matrix` will not compile (`[setup failed]` is a missing tag, not a bug).
- **Never run the full Go suite here** (OOM). One narrow test at a time:
  `CGO_ENABLED=0 go test -tags goolm,stdjson -race -run '^TestName$' -p 1 ./pkg/agent/`
- Full-suite verdicts come from CI or the `ci-omnipus` Fly worker, never this pod.

---

## 9. Work units and wave graph

**Ownership rule: no two units write the same file. No unit depends on another unit in the same wave.** (Revision 1 violated the second rule — W3 declared a dependency on W4 while both sat in Wave 1. B5.)

### Shared contract — lands in Wave 0, before anything else

```go
// pkg/agent/tool_denial.go
const turnDenialBudget = 10
const denialUserMarker  = "user denied"
const DenialMessageUser = "The user denied this tool call. Do not retry it; stop and report the blocker."

type DenialClass struct {
    Reason         string
    Permanent      bool
    ModelMessage   string
    TranscriptText string
}

func ClassifyDenial(reason string) (DenialClass, bool)                 // bool = known
func denialPayloadJSON(tool, reason string, cls DenialClass) string
func toolDenialAbortReason(tool, reason, agentID string, budget int) string

// the ledger — its own type, so turn.go gains exactly ONE field
type quarantinedDenial struct{ reason, payload string }

type turnDenialLedger struct {
    used        int
    quarantined map[string]quarantinedDenial   // keyed by TOOL NAME ONLY (N4)
}

// methods on *turnState, defined in tool_denial.go, each taking ts.mu.Lock() (N3):
func (ts *turnState) recordToolDenial(tool, reason string, permanent bool, payload string) (used int, exhausted bool)
func (ts *turnState) recordQuarantineReplay(tool string) (used int, exhausted bool)
func (ts *turnState) quarantinedDenialFor(tool string) (payload, reason string, ok bool)
```

`toolDenialAbortReason` format (pinned by test):
`blocked: tool %q denied (%s) for agent %q — the turn reached its limit of %d tool denials`

| Unit | Owns (writes) | Changes | Depends on |
|---|---|---|---|
| **W1** — classification core + ledger *(Wave 0, blocking)* | `pkg/agent/tool_denial.go` (new), `pkg/agent/tool_denial_test.go` (new) | The whole shared contract above. **Pure addition — compiles standalone against the current tree**, touching no existing file. | — |
| **W2** — single renderer | `pkg/agent/approval_transcript.go`, `pkg/agent/approval_transcript_denial_test.go` (new) | `::askDenialText` → `ClassifyDenial(reason).TranscriptText`, own switch deleted, `""` string preserved. `::settleAskToolCallTranscript` → `"permanent"` inside `Result`. | W1 |
| **W3** — reason constants | `pkg/gateway/approvals.go`, `pkg/gateway/approval_denial_classification_test.go` (new) | The six denial literals → named constants + `allApprovalDenialReasons` (plus `internal_error`; **excluding** `approved`). **No behaviour change; `fireTimeout` untouched.** | W1 |
| **W4** — loop rewiring + FR-084 excision *(atomic; one unit by necessity)* | `pkg/agent/loop.go`, `pkg/agent/turn.go`, `pkg/config/config.go`, `pkg/agent/abort_turn_system_test.go`, `pkg/audit/events.go`, `pkg/audit/audit.go`, `pkg/audit/events_test.go` | All three payloads → `denialPayloadJSON`. Quarantine gate in `runTurn` before `hooks.BeforeTool`. Denial sites → `recordToolDenial` + `abortTurn(ts,"tool_denial_budget",…)`. **Delete** FR-084 in full (FR-058-14) incl. the 4th call site's abort branch, `turnState.syntheticErrorCount`, the config field, and every stale comment. Audit event swap (FR-058-15). **Delete** `TestRunTurn_SyntheticDenyFloor_AbortsWithSurfacedError` — see the note below. | W1 |
| **W5** — loop + plan-task behavioural tests | `pkg/agent/loop_tool_denial_test.go` (new), `pkg/agent/task_executor_tool_denial_test.go` (new) | BDD-01, 05, 06, 08, 09, 10 (`-race`) | W1, W4 |
| **W6** — registry-backed tests | `pkg/gateway/tool_denial_quarantine_test.go`, `tool_denial_saturated_retry_test.go`, `tool_denial_timeout_regression_test.go` (all new) | BDD-04, 07 + AC-02 (`-race`), harness per §3.7 | W1, W3, W4 |
| **W7** — regression strengthening + contract gate | `pkg/agent/scenario_runturn_test.go`, `pkg/agent/subturn_delegate_nesting_test.go`, `pkg/agent/turn_recheck_test.go` | Extend existing `permission_denied` assertions to pin `permanent` + classified message. Run `make verify-contracts`; assert an empty generated/contract diff. | W4 |

**Why W4 is one unit and not three (B5).** Deleting `config.GatewayConfig.TurnSyntheticErrorFloor` breaks `loop.go::syntheticErrorFloor`; deleting `turnState.syntheticErrorCount` breaks `recordSyntheticDeny`; deleting the emit site orphans the audit event. These are one atomic compile unit spanning four packages. Splitting them across parallel agents would leave the tree un-buildable between commits — which is exactly the intra-wave dependency B5 forbids. One agent, one commit.

**B4 — test ownership follows the breakage.** `TestRunTurn_SyntheticDenyFloor_AbortsWithSurfacedError` (`abort_turn_system_test.go:173`) sets `TurnSyntheticErrorFloor: 2` and drives the TOCTOU policy-deny site twice, asserting the 2nd denial aborts with `synthetic_error_loop`. With FR-084 deleted **and** quarantine-at-first in place, both its mechanism and its expected behaviour are gone: under the new design the first denial quarantines `dangerous_tool` and the turn continues. It is therefore **deleted by W4**, the unit that causes the break — not edited, and not left for a test unit to discover. Verified safe: the file's other test (`TestAgentLoop_AbortTurn_HookHardAbort_SurfacesSystemInitiatedError`, `:91`) must survive, and every helper the deleted test uses is defined and used elsewhere — `dangerousStubTool` and `readAuditEntries` in `scenario_runturn_test.go:42,61`; `collectEventStream` and `findEvent` in `eventbus_test.go:1001,1037`. Its replacement coverage is W5's BDD-05/BDD-09.

### Wave graph

```
Wave 0 (1 agent, blocking):   W1                       — pure addition; tree stays green
Wave 1 (3 agents, parallel):  W2 · W3 · W4             — file-disjoint; each depends only on W1
Wave 2 (3 agents, parallel):  W5 · W6 · W7             — file-disjoint; each depends only on Wave 1
Wave 3:                       7-reviewer quality gate on the whole diff, then PR
```

No unit in any wave depends on another unit in the same wave. File ownership across all seven units is disjoint.

**TDD honesty under this graph.** Strict cross-agent red-green conflicts with "no intra-wave dependencies", so the discipline is preserved *procedurally* instead: **W5 and W6 must each demonstrate their tests RED before accepting them** — check out the Wave-0 commit (or `git stash` Wave 1), run the new test, capture the failure, then restore and run it green. A test that has never been observed failing is not accepted.

**Git-index caution:** parallel agents share `.git/index`. Each unit commits with `git commit --only <its own paths>`.

---

## 10. Definition of done

- [ ] FR-058-01 … FR-058-17 implemented, each covered by at least one test in §7.
- [ ] AC-01 … AC-09 asserted, each with its §8.2 stub-resistance assertion present. **AC-06 (positive lower bound) and AC-02 (the §1.1 regression) passing.**
- [ ] `gofmt -l . | wc -l` → 0; `golangci-lint run --build-tags=goolm,stdjson` → exit 0.
- [ ] CI green on `go test -tags goolm,stdjson -count=1 ./...` and the `-race` job. **Verdict from CI or `ci-omnipus`, never a full local run.**
- [ ] `make verify-contracts` exit 0 **and** `git diff --name-only` contains no path under `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/`.
- [ ] `grep -rn "User denied tool execution" pkg/` → **0 lines** (today: 2, at `loop.go:8851` and `:8938`).
- [ ] `grep -rin "user denied" pkg/ --include=*.go` → **exactly 2 lines** (today: 3): the `DenialMessageUser` constant in `pkg/agent/tool_denial.go` (§4.2), and the **pre-existing doc comment** at `pkg/audit/events.go:46` (*"User denied an ask call OR a system-deny…"*), which is a comment describing an audit event, not a message the model receives, and is out of scope. The constant is the same literal BDD-01 and BDD-02 assert against. **FR-058-06's "exactly one place" is about message strings; this comment is the one verified exception.**
- [ ] `grep -rn "recordSyntheticDeny\|syntheticErrorFloor\|defaultSyntheticErrorFloor\|syntheticErrorCount\|TurnSyntheticErrorFloor\|synthetic_error_floor\|EventTurnAbortedSyntheticLoop" --include=*.go .` → **0 lines**. Today: **56** — 46 in `pkg/agent`, 8 in `pkg/audit`, 2 in `pkg/config` (counts executed in-session). This includes the stale comments at `abort_turn_system_test.go:16,22,147`.
- [ ] One boot with a legacy `"turn_synthetic_error_floor"` key present in `config.json` starts cleanly (§3.6 item 4's **[UNVERIFIED]** strict-decode risk, discharged by W4).
- [ ] Constraint #7: nothing deferred silently. **F1, S1 and S2 (§2.4) filed as issues before merge.**

---

## 11. Open items carried forward (not decided here)

1. **F1** — FR-065's `cancelBatchShortCircuit` has no production caller. A control that cannot fire is a zombie (Constraint #6). File an issue; do not revive it inside this change.
2. **S1** — plan-task lifecycle records lose the failure reason (`"execution_error"` literal).
3. **S2** — `task.Task` has no structured failure enum, unlike `plan.FailedReason`.
4. **N2** — `abortTurn`'s `restoreSession` failure branch discards the denial reason. Pre-existing, pinned by the AC-04 test, not fixed here.
5. **§3.3** — the audit-event swap extends the operator's stated deletion list. Flagged for veto.
6. **ADR §5** — upstream tool-policy feasibility validation at plan-approve time. The 24/32 UAT failures become honest and fast; they are not prevented.
7. **ADR §9 Q1/Q2/Q3** — per-turn vs per-task budget; back-off for `saturated`; extending `permanent` to `DelegationFailure` / `PermissionDeniedResult` (priced in §3.6, needs the contract pipeline).
8. **[INFERRED]** 10 remains an unmeasured operator constant, now applied to a strictly larger population (all denials, not one pair), so it bites sooner than the ADR's original per-pair figure. If `tool_denial_budget` terminations appear in production logs, that is a signal the D1/D2 messages are not landing — not the system working as designed (ADR §3).
