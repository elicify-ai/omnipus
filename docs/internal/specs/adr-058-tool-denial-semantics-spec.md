# Spec — Tool-denial semantics: honest denial payloads + a bounded retry (ADR-058)

- **ADR:** [ADR-058](../architecture/ADR-058-tool-denial-semantics.md) (Accepted, 2026-08-05, commit `cbc30eed`). D1–D5 and §8 acceptance criteria are non-negotiable inputs to this spec.
- **Issue:** [#594](https://github.com/elicify-ai/omnipus/issues/594) (second half; `6d0735ef` was the first)
- **Branch:** `feature/plan-swimlane-board`
- **Evidence level:** every `file::symbol` below was read in-session against `feature/plan-swimlane-board` @ `6793b96a`. Claims that correct or extend the ADR are marked **[CORRECTION]**. Claims not tool-verified are marked **[INFERRED]**.
- **Scope:** behavioural fix inside `pkg/agent` + two mechanical changes in `pkg/config` and `pkg/gateway`. No identity model, no storage layout, **no contract change** (§3.2).

---

## 1. Overview

Three denial emit sites in `pkg/agent/loop.go` hand the model a sentence asserting a human decision that did not occur (`"User denied tool execution."`) on six of seven approver reasons and on the headless auto-deny path. The model then retries reasonably on false information; nothing bounds the retry, because the mechanism that would (FR-084) is disabled on every install. One UAT task looped for 13+ minutes and required a manual Stop.

This spec delivers: (a) one classification table making every denial payload honest and machine-readable; (b) one renderer shared by the transcript and the model; (c) a hard per-turn ceiling of 10 per `(tool, reason)` that quarantines the pair, opens no further approval round-trip, and terminates the turn with a reason naming tool + agent + denial reason; (d) the FR-084 sentinel fix that makes (c) provable.

**Out of scope, named:** upstream tool-policy feasibility validation at plan-approve time (ADR §5); `pkg/tools/fserrors.go::PermissionDeniedResult`; `pkg/tools/result.go::DelegationDeniedResult` (§3.2 prices it); `pkg/gateway/approvals.go::fireTimeout`'s fail-closed behaviour, which is correct and unchanged; back-off for `saturated` (ADR §9 Q2); per-task vs per-turn ceiling (ADR §9 Q1).

---

## 2. Existing codebase context (verified)

### 2.1 Symbols involved

| Symbol | Role |
|---|---|
| `pkg/agent/loop.go::AgentLoop.runTurn` | Holds the tool-dispatch loop `for i, tc := range normalizedToolCalls`. All three denial sites live inside it. |
| `pkg/agent/loop.go` (TOCTOU deny, `permission_denied` + `"Tool execution denied by policy."`) | Site 1 — policy flipped to `deny` at exec time. **Already correct**; gets the marker for uniformity only. |
| `pkg/agent/loop.go` (headless `AutoDenyAsk`) | Site 2 — FR-009/#264. Fixed literal reason, false `message`. |
| `pkg/agent/loop.go` (ask denied) | Site 3 — the defect. `denialReason` verbatim from `CheckGrantOrRequestApproval`. |
| `pkg/agent/loop.go::AgentLoop.CheckGrantOrRequestApproval` | Grant-store check, then `approver.RequestApproval` → `(approved, denialReason)`. |
| `pkg/agent/loop.go::AgentLoop.abortTurn` | `err := fmt.Errorf("turn aborted during %s: %s", stage, reason)`; emits `EventKindError`; returns `turnResult{status: TurnEndStatusAborted}, err`. |
| `pkg/agent/loop.go::AgentLoop.recordSyntheticDeny` / `::AgentLoop.syntheticErrorFloor` / `const defaultSyntheticErrorFloor = 8` | FR-084. |
| `pkg/agent/turn.go::turnState` (field `syntheticErrorCount`) | Per-turn counter home. |
| `pkg/agent/turn.go::turnResult` | `{finalContent, status, followUps, turnFailed}` — **no reason field**. |
| `pkg/agent/approval_transcript.go::askDenialText` | Human-facing renderer; branches correctly on 4 reasons + `""`. |
| `pkg/agent/approval_transcript.go::settleAskToolCallTranscript` | Writes `session.ToolCall.Result = {error, text, reason}`. |
| `pkg/agent/tool_approver.go::PolicyApprover`, `::nopApproverDenialReason` | Interface + `"no_approver_configured"`. |
| `pkg/gateway/approvals.go::approvalRegistryV2` | The 7 reason literals; `::fireTimeout` fails closed (unchanged). |
| `pkg/gateway/policy_approver.go::newPolicyApproverAdapter` | The only real `PolicyApprover`. Lives in `pkg/gateway` — see §2.3. |
| `pkg/agent/task_executor.go::TaskExecutor.finishTaskRun` / `::TaskExecutor.failTask` | Where a turn error becomes `Task.Result`. |

### 2.2 Corrections to the ADR (verified in-session)

| # | ADR statement | Verified reality |
|---|---|---|
| C1 | §1.5: `TurnSyntheticErrorFloor` "assigned nowhere". | **Confirmed for non-test code.** Its only assignment anywhere is `pkg/agent/abort_turn_system_test.go` (`TurnSyntheticErrorFloor: 2`). Unset → Go zero `0` → `floor <= 0` → disabled. |
| C2 | §1.5 implies FR-084 counts *consecutive* denials (the field's own doc comment says "consecutive"). | **[CORRECTION] The counter is never reset.** `syntheticErrorCount` appears at exactly 5 sites in `pkg/agent`; none assigns `0` after construction. It is a per-turn **cumulative** count, not consecutive. This matters: at floor 8 it fires on the 8th denial of a turn even when successes are interleaved. |
| C3 | §1.5: FR-084 "is already called from all three sites above". | **[CORRECTION] There are four `recordSyntheticDeny` call sites.** The fourth is the tool-assembly-duplicate branch (FR-066, `checkToolDedupInvariant` failure) — a genuinely non-denial synthetic error. Two further deny branches (`hooks.BeforeTool` → `HookActionDenyTool`, and `hooks.ApproveTool` rejection, both via `pkg/agent/loop.go::hookDeniedToolContent`) do **not** call it and emit no `permission_denied` payload; they are out of D1's nine rows and stay unchanged. |
| C4 | W5: tests "pin the current strings" and must be inverted. | **[CORRECTION] No test anywhere pins `"User denied tool execution."`.** A repo-wide search over `pkg/`, `src/`, `contracts/` finds that literal only in `pkg/agent/loop.go`. The named tests assert the substring `permission_denied` (the error *code*), which this change preserves. W5 therefore becomes *strengthen*, not *invert*: those assertions must additionally pin `permanent` and the classified message. |
| C5 | §7 item 2: the transcript entry "is a contract type … adding a key inside `result` needs no schema change". | **Conclusion correct; evidence chain incomplete.** `ToolCall.Result` **does** cross the gateway/SPA boundary, via `pkg/gateway/replay.go::buildResult` → `truncateResult` → `generated.ToolCallResultFrame.Result`. It is still safe: `contracts/components/schemas/ToolCall.yaml` `result` is `type: object` + `additionalProperties: true`, **and** `ToolCallResultFrame.result` generates to `result: z.unknown()` in `src/lib/api/generated/schemas.ts::ToolCallResultFrame`. Both gates verified. |
| C6 | §7 item 4: adding a field to `DelegationFailure` would make the SPA "silently drop the frame". | **[CORRECTION] Wrong mechanism, right conclusion.** The frame-level `WsFrameSchema.safeParse` (`src/lib/ws.ts`) sees `result: z.unknown()` and would accept the extra key; the strict generated `DelegationFailure` Zod (`src/lib/api/generated/schemas.ts`) is **never referenced by hand-written SPA code** — the render path uses the duck-type guard `src/components/chat/tools/GenericToolCall.tsx::isDelegationFailure`, which checks only `error === 'delegation_denied'`. The real blocker is upstream: `generated.DelegationFailure` is a **generated Go struct** (`pkg/api/generated/asyncapi_types.gen.go`), so the field cannot be added without editing `contracts/` and running `scripts/gen-contracts.sh`; `make verify-contracts` fails on drift. Same verdict (out of scope, pipeline-priced), recorded with the correct reason. |
| C7 | — | **[NEW]** The `env:"OMNIPUS_TURN_SYNTHETIC_ERROR_FLOOR"` struct tag is decorative: no reflection-based env loader consumes `env:` tags in `pkg/config`, and that variable is never read via `os.Getenv`. Changing the field's Go type carries **zero** env-loader risk. |

### 2.3 Import-direction constraint (drives test placement)

`pkg/gateway/gateway.go` imports `pkg/agent`. Therefore **`pkg/agent` cannot import `pkg/gateway`**, and the only real `PolicyApprover` (`pkg/gateway/policy_approver.go::policyApproverAdapter`, wired at `pkg/gateway/gateway.go` via `agentLoop.SetToolApprover(newPolicyApproverAdapter(approvalReg, wsHandler))`) is unreachable from `pkg/agent` tests.

Consequence, binding on the TDD plan: **every criterion that asserts on real approval-registry state (AC-02, AC-03, AC-06) is a `pkg/gateway` test.** Precedent harnesses that already construct a real registry + adapter: `pkg/gateway/approvals_test.go`, `pkg/gateway/approvals_adr057_test.go`, `pkg/gateway/approval_grant_survival_fix_test.go`.

---

## 3. The three resolutions the ADR left open

### 3.1 R1 — FR-084 collision and the inverted sentinel

**Verified arithmetic.** FR-084's floor is 8 **cumulative** per-turn denials (C2); D4's ceiling is 10 per `(tool, reason)` pair. On a homogeneous denial stream — §1.1's exact case — FR-084's counter reaches 8 before any pair reaches 10. The ADR's own **[INFERRED]** suggestion ("once quarantined, short-circuited results stop feeding `recordSyntheticDeny`") does **not** resolve this: quarantine engages at 10, which is after 8. The two mechanisms cannot coexist on denials; one must own them.

**R1(a) — DECISION: D4 supersedes FR-084 at the three tool-denial sites.** The three sites stop calling `recordSyntheticDeny` and call the new per-pair recorder instead. FR-084 retains the fourth call site (tool-assembly duplicate, C3) — a genuinely different synthetic-error class — and keeps its `synthetic_error_loop` abort there, unchanged.

Reasoning, and what is deliberately given up:
- They provably cannot coexist: 8 < 10 makes D4's richer termination unreachable in the case it exists for. AC-08 demands one precedence be asserted explicitly; this is it.
- The coverage FR-084 loses on denials is **coverage that does not exist today**. It is disabled on every install that has not hand-written a *negative* number into `config.json` (C1). Narrowing a disabled mechanism removes nothing live.
- **Accepted loss, stated:** with FR-084 armed (R1(b)) and D4 owning denials, 8 denials spread across 8 *different* `(tool, reason)` pairs no longer abort the turn; each pair needs its own 10. `MaxIterations` (200, hard 2×) remains the outer bound. This is accepted because heterogeneous denial storms are not the observed pathology — §1.1 was homogeneous, and §1.2's 23 heterogeneous-agent denials all self-terminated in 4–439 s.

**R1(b) — DECISION: the inverted sentinel is fixed in this change, not filed separately.** `Gateway.TurnSyntheticErrorFloor` becomes `*int`: `nil` (unset) → `defaultSyntheticErrorFloor` (8, armed); explicit `0` → disabled; `>0` → that value; `<0` → rejected at config validation rather than silently meaning "default".

Reasoning:
- Constraint #7 forbids deferral.
- **It is a prerequisite for honest testing.** AC-08 asserts D4 wins over FR-084. With the sentinel inverted, the only way to arm FR-084 for that test is `TurnSyntheticErrorFloor: -1` — a configuration no operator will ever have. A precedence test that only passes on an impossible config is exactly the dead-mechanism class this project keeps shipping.
- We are narrowing FR-084's scope in R1(a). Narrowing a mechanism while leaving it disabled is indistinguishable from deleting it, and would leave the `config.go` doc lying about a default of 8 — the one option ADR W6 rules out.
- `*int` (rather than flipping `0`/negative semantics on the existing `int`) preserves the documented contract *"Set to 0 to disable"* exactly, and makes unset structurally distinguishable from explicit 0. Zero env-loader risk (C7). Blast radius: the declaration, `syntheticErrorFloor()`, and one test assignment.

**R1(c) — DECISION: D4's ceiling is a bare Go constant, not config-backed.** `const toolDenialCeilingPerTurn = 10` in `pkg/agent/tool_denial.go`. An unconfigurable constant has no unset state and therefore cannot repeat FR-084's defect. If configurability is added later it MUST arrive as `*int` with nil-means-armed; a bare `int` field is forbidden by this spec. AC-05 is then satisfied by construction and asserted against a Go-zero-value config.

### 3.2 R2 — Constraint #8 scope (re-verified independently)

**IN SCOPE — no `contracts/` change, no `scripts/gen-contracts.sh` run:**

1. **The `denyMsg` provider message.** Placed in `providers.Message{Role:"tool", Content: denyMsg}` → the LLM and `Sessions.AddFullMessage`. **Verified:** `pkg/gateway/*.go` (non-test) contains **zero** calls to `GetHistory`, `AddFullMessage`, or `GetFullHistory` — the provider message list has no gateway reader. The ask-deny branch `continue`s before the tool-result path, so no `tool_call_result` frame is produced from it. Not a wire format.
2. **`session.ToolCall.Result`** — the `permanent` key goes **inside** `result`. Verified safe through both gates (C5). **Hard constraint: `permanent` MUST NOT become a top-level `ToolCall` field** — `contracts/components/schemas/ToolCall.yaml` is `additionalProperties: false`, which would make that a contract change.
3. **`ToolExecSkippedPayload.Reason`** — `tool_exec_skipped` appears only in `pkg/agent/loop.go` and `pkg/agent/events.go`. Zero references in `pkg/gateway/`, `src/`, `contracts/`. Internal event bus only.
4. **`Gateway.TurnSyntheticErrorFloor`** — zero references in `contracts/` and `src/`. Backend-only config; the `*int` change crosses no boundary.

**OUT OF SCOPE — explicitly:**

5. **`pkg/tools/result.go::DelegationDeniedResult` → `generated.DelegationFailure`.** Not extended. It is a generated Go struct from an `additionalProperties: false` schema; adding `permanent` requires the full 5-step pipeline (C6). Deliberately deferred to the ADR §9 Q3 follow-on decision.
6. **`pkg/tools/fserrors.go::PermissionDeniedResult`** — plain string, no schema, not extended (ADR §3).

**AC-09 reduces to:** `make verify-contracts` exits 0, and the diff touches no file under `contracts/`, `pkg/api/generated/`, or `src/lib/api/generated/`. The delegation round-trip half of AC-09 is vacuous under this scope and is asserted as "not applicable — no `DelegationFailure` change", not skipped silently.

### 3.3 R3 — Turn-termination mechanics (traced end to end)

**The seam, verified:**

```
runTurn (turnResult, error)
  └─ runAgentLoop (string, error)          ← turnResult DIES HERE
       │   `if err != nil { return "", err }` — error preserved verbatim
       │   `al.lastTurnResult = result` is test-only ("Never read in production paths")
       └─ processTaskDirect (string, error)
            └─ TaskExecutor.runTask → finishTaskRun(ctx, t, sid, resp, err, "")
                 └─ err != nil ⇒ failTask(t.ID, fmt.Sprintf("execution error: %v", err))
                      └─ task.Store.Update{Status: failed, Result: <that string>, CompletedAt}
```

**Findings:**

- `turnResult` carries **no** reason field. `turnState` has **no** `abortReason`/`terminationReason`. `pkg/agent` defines **no** custom error type (`) Error() string` has zero non-test matches); `abortTurn` synthesises a plain `fmt.Errorf`. **There is no structured termination-reason channel from turn to caller.**
- **But the error *string* propagates intact.** `runAgentLoop` returns it unwrapped; `finishTaskRun` writes `"execution error: " + err.Error()` into `Task.Result` verbatim (capped at 50 000 chars by `pkg/task/store.go`). Nothing generic replaces it.
- `Task.CancelReason` (`pkg/task/task.go::CancelReason`) has exactly one member, `CancelReasonStoppedByUser`, and is documented as *"empty for a genuine failure"*. `stopped_by_user` is written **out of band** by `pkg/agent/plan_engine.go::PlanEngine.cancelMemberLocked` under `planDecisionMu` — never by a turn.

**R3 — DECISION: no plumbing change. Use the existing error path.**

D4's termination is `al.abortTurn(ts, "tool_denial_ceiling", toolDenialAbortReason(tool, ts.agentID, reason, count))`, producing exactly:

```
Task.Result == `execution error: turn aborted during tool_denial_ceiling: blocked: tool "bash" denied (timeout) for agent "mia" after 10 attempts in this turn`
```

This satisfies D4 #4 and AC-04 with zero new plumbing, and is distinguishable from a human Stop by two independent assertions: `Task.CancelReason == ""` (a Stop sets `stopped_by_user`) and `Task.Result` not carrying `pkg/agent/plan_engine.go::memberCancelReasonMarker` (`"[reason:stopped_by_user]"`).

Two seam defects found and **explicitly not fixed here** (named, not silently accepted):

- **S1 — the lifecycle record loses the reason.** `finishTaskRun` calls `transitionTaskLifecycle(sid, session.LifecycleFailed, "execution_error")` with a hardcoded literal. The lifecycle store never learns *why*. D4's requirement is about the task record (`Task.Result`), which is satisfied; threading a reason into the lifecycle store is a separate change with its own store surface. **Filed, not deferred silently.**
- **S2 — the task has no structured failure enum.** `plan.FailedReason` has six discriminated causes; `task.Task` has prose plus a one-value `CancelReason`. A structured `failed[tool_denied]` would need a new `CancelReason`/failure enum member and its transition-guard updates. Out of scope; AC-04 is met by the prose + the negative `CancelReason` assertion.

---

## 4. Functional requirements

| FR | Requirement |
|---|---|
| **FR-058-01** | One exported classifier `agent.ClassifyDenial(reason string) (DenialClass, bool)` is the sole source of denial semantics. `DenialClass{Reason, Permanent bool, ModelMessage, TranscriptText string}`. The bool is `known` — false for a reason with no table row. |
| **FR-058-02** | The table covers all nine D1 rows: `user`, `timeout`, `saturated`, `restart`, `cancel`, `batch_short_circuit`, `no_approver_configured`, the policy-deny pseudo-reason, and the headless auto-deny literal. Exactly one (`saturated`) is `Permanent:false`. |
| **FR-058-03** | An unknown reason classifies as `Permanent:true` with a generic message, and returns `known == false`. For an unknown reason, `ModelMessage == TranscriptText` (identical rendering on both surfaces — AC-07). |
| **FR-058-04** | The seven approver reason literals in `pkg/gateway/approvals.go` become named constants collected in one slice. A `pkg/gateway` test asserts `ClassifyDenial(r)` returns `known == true` for every member — so a new reason with no classification fails a test rather than defaulting silently (AC-01). `fireTimeout`'s fail-closed behaviour is byte-for-byte unchanged. |
| **FR-058-05** | All three `permission_denied` payloads are built by one function `denialPayloadJSON(tool, reason string, cls DenialClass) string`, emitting `{"error":"permission_denied","message":<cls.ModelMessage>,"tool":<tool>,"reason":<reason>,"permanent":<bool>}`. The literal `"User denied tool execution."` exists nowhere in `pkg/` after this change except as the classified message for reason `user`. |
| **FR-058-06** | `pkg/agent/approval_transcript.go::askDenialText` returns `ClassifyDenial(reason).TranscriptText` and holds no `switch` of its own. Its three previously missing branches (`restart`, `batch_short_circuit`, `no_approver_configured`) are supplied by the table. |
| **FR-058-07** | `settleAskToolCallTranscript` adds `"permanent": cls.Permanent` **inside** `session.ToolCall.Result`, never as a top-level `ToolCall` field. |
| **FR-058-08** | `turnState` gains a per-turn denial ledger keyed by `(toolName, denialReason)`, guarded by `ts.mu`. Fresh `turnState` ⇒ empty ledger; no cross-turn and no cross-session carry-over. |
| **FR-058-09** | `const toolDenialCeilingPerTurn = 10`. On the 10th denial of a pair, the pair is **quarantined**: the tool is recorded in the turn's quarantine set together with the cached payload. 9 denials do NOT quarantine. |
| **FR-058-10** | A quarantine gate sits in `runTurn`'s dispatch loop immediately after `toolName`/`toolArgs` resolution and **before** the `hooks.BeforeTool` block, the TOCTOU re-check and the approval path. A quarantined tool is answered from the cached payload with no hook call, no policy re-resolution, no `RequestApproval`, and no `tool_approval_required` frame. |
| **FR-058-11** | On quarantine the turn terminates via `abortTurn(ts, "tool_denial_ceiling", toolDenialAbortReason(...))`. The reason string names the tool, the denial reason, the agent id and the attempt count. |
| **FR-058-12** | The three tool-denial sites no longer call `recordSyntheticDeny`. The tool-assembly-duplicate site still does, and still aborts with `synthetic_error_loop` (R1(a)). |
| **FR-058-13** | `Gateway.TurnSyntheticErrorFloor` becomes `*int`. `syntheticErrorFloor()` implements exactly: nil (unset) ⇒ `defaultSyntheticErrorFloor` (8, **armed**); `0` ⇒ disabled; `>0` ⇒ that value; `<0` ⇒ 8 **plus a one-time WARN naming the misconfiguration** (fail-safe toward bounded, never silent). `config.go`'s doc comment is rewritten to match. No boot abort is added — `pkg/gateway/gateway.go` is deliberately not touched by this change (R1(b)). |
| **FR-058-14** | `saturated` remains retryable: a `saturated` denial below the ceiling does **not** quarantine, and a later call to the same tool in the same turn reaches the approver and can execute (AC-06). |
| **FR-058-15** | No file under `contracts/`, `pkg/api/generated/`, or `src/lib/api/generated/` is modified. `make verify-contracts` exits 0 (R2). |

---

## 5. Behavioural contract & explicit non-behaviours

**Observable:**
- Every `permission_denied` payload reaching the model carries `permanent` and a message true for its reason.
- The transcript card and the model message for one denial event come from one table row.
- A turn that hits the ceiling ends by itself; a plan task lands `failed` with a `Result` naming tool + reason + agent.
- After quarantine, no further approval request is created for that tool in that turn.

**Explicit non-behaviours (must not happen):**
- **D5 is REJECTED.** The tool is NOT removed from `tools[]` / the provider tool defs mid-turn. `FilterToolsByPolicy`'s output is untouched by this change; the advertised capability set stays stable for the whole turn. Denied calls are short-circuited, not un-offered.
- `fireTimeout` still fails closed. Nothing here converts an unanswered approval into an approval.
- No back-off is introduced for `saturated`.
- No new WS frame, no new REST field, no new persisted top-level field.
- Standing "Always Allow" grants are unaffected — the quarantine gate sits before the grant check, but a tool only reaches quarantine after 10 denials, which a granted tool cannot produce.

---

## 6. BDD scenarios

**BDD-01 — timeout no longer claims a user decided (FR-058-01/02/05, AC-01)**
```gherkin
Given an agent whose effective policy for "run_task" is "ask"
  And a real PolicyApprover whose approval window expires with nobody answering
When the agent calls run_task
Then the tool message the model receives parses as JSON with error="permission_denied"
  And its "reason" is "timeout"
  And its "permanent" is true
  And its "message" does not contain "User denied"
  And its "message" tells the agent to stop and report the blocker
```

**BDD-02 — every classified reason, table-driven (FR-058-02/03/04, AC-01)**
```gherkin
Given the full enumeration of approver denial reasons plus the two loop-side pseudo-reasons
When each is classified
Then exactly one ("saturated") is permanent=false
  And every one returns known=true
  And only reason "user" yields a message containing "User denied"
```

**BDD-03 — one renderer, two audiences (FR-058-06, AC-07)**
```gherkin
Given a real denial with reason "restart"
When the transcript entry and the model message are both produced
Then both derive from the same DenialClass row
  And the persisted ToolCall.Result.text equals ClassifyDenial("restart").TranscriptText
  And the model payload's message equals ClassifyDenial("restart").ModelMessage
Given instead a reason with no table row
Then the transcript text and the model message are byte-identical
```

**BDD-04 — the ceiling fires at 10, not at 9 (FR-058-08/09/11, AC-05)**
```gherkin
Given a config built from Go zero values only (nothing hand-edited)
  And a tool that is denied with the same reason on every call
When the tool has been denied 9 times in one turn
Then the turn has not aborted
  And the tool is not quarantined
  And a 10th call still reaches the approver
When the 10th denial lands
Then the turn aborts during stage "tool_denial_ceiling"
  And the abort reason names the tool, the denial reason and the agent id
```

**BDD-05 — no approval round-trip after quarantine (FR-058-10, AC-03)**
```gherkin
Given a single LLM response containing 12 calls to the same ask-policy tool
  And a real approval registry
When the turn processes that batch
Then the registry observes exactly 10 requestApproval calls for that tool
  And zero tool_approval_required frames are emitted for calls 11 and 12
  And the registry's pending count returns to its pre-turn value
```

**BDD-06 — POSITIVE LOWER BOUND: saturated still succeeds on retry (FR-058-14, AC-06)**
```gherkin
Given a real approval registry configured with max_pending = 1
  And one approval already pending, holding the only slot
When the agent calls "web_fetch" and is denied with reason "saturated"
Then the payload's permanent is false
  And "web_fetch" is not quarantined
When the held approval resolves and the queue drains
  And the agent calls "web_fetch" again and it is approved
Then web_fetch actually executes and returns a real tool result
  And the turn completes normally
```

**BDD-07 — the plan task explains itself (FR-058-11, AC-04)**
```gherkin
Given a plan task assigned to agent "mia" whose tool "bash" is denied every time
When the turn hits the ceiling and terminates
Then the task's status is "failed"
  And its Result contains "bash" and the denial reason and "mia"
  And its CancelReason is empty
  And its Result does not start with "[reason:stopped_by_user]"
```

**BDD-08 — D4 beats FR-084 (FR-058-12, AC-08)**
```gherkin
Given gateway.turn_synthetic_error_floor is unset (nil ⇒ armed at 8)
When one tool is denied with one reason 10 times in a single turn
Then the turn's abort reason is D4's tool-naming reason
  And no message with reason "synthetic_error_loop" was written to the session
Given instead the tool-assembly duplicate invariant fails 8 times in a turn
Then the turn aborts with reason "synthetic_error_loop"
```

**BDD-09 — counters are per-turn (FR-058-08)**
```gherkin
Given a turn in which one tool was denied 9 times
When that turn ends and a new turn starts in the same session
Then the new turn's ledger for that pair is empty
  And 9 further denials in the new turn do not abort it
Given a second, unrelated session running concurrently on the same AgentLoop
Then its ledger is unaffected by either turn
```

---

## 7. Traceability matrix

| FR | ADR | BDD | Test (package::file) | AC |
|---|---|---|---|---|
| FR-058-01/02/03 | D1 | BDD-02 | `pkg/agent::tool_denial_test.go` | AC-01 |
| FR-058-04 | D1 | BDD-02 | `pkg/gateway::approval_denial_classification_test.go` | AC-01 |
| FR-058-05 | D1, D2 | BDD-01 | `pkg/agent::loop_tool_denial_test.go` | AC-01 |
| FR-058-06 | D1 | BDD-03 | `pkg/agent::approval_transcript_denial_test.go` | AC-07 |
| FR-058-07 | D1 | BDD-03 | `pkg/agent::approval_transcript_denial_test.go` | AC-07 |
| FR-058-08 | D4 | BDD-09 | `pkg/agent::tool_denial_test.go` | AC-05 |
| FR-058-09 | D4 | BDD-04 | `pkg/agent::loop_tool_denial_test.go` | AC-05 |
| FR-058-10 | D4 #1–#3 | BDD-05 | `pkg/gateway::tool_denial_quarantine_test.go` | AC-03 |
| FR-058-11 | D4 #4 | BDD-04, BDD-07 | `pkg/agent::loop_tool_denial_test.go`, `pkg/agent::task_executor_tool_denial_test.go` | AC-02, AC-04 |
| FR-058-12 | D4, §1.5 | BDD-08 | `pkg/agent::loop_tool_denial_test.go` | AC-08 |
| FR-058-13 | §1.5, W6 | BDD-04, BDD-08 | `pkg/agent::loop_tool_denial_test.go` (semantics), `pkg/config::synthetic_floor_test.go` (shape guard), `pkg/agent::abort_turn_system_test.go` | AC-05, AC-08 |
| FR-058-14 | D1 #3 | BDD-06 | `pkg/gateway::tool_denial_saturated_retry_test.go` | **AC-06** |
| FR-058-15 | §7 | — | `make verify-contracts` + diff check | AC-09 |
| — (regression) | §1.1 | BDD-01 + BDD-05 | `pkg/gateway::tool_denial_timeout_regression_test.go` | **AC-02** |

Every ADR §8 criterion AC-01…AC-09 appears at least once. AC-09's delegation half is asserted as *not applicable* by the diff check (R2).

---

## 8. TDD plan

**Verification bar (non-negotiable, inherited from ADR §8 and ADR-057 §10):**
- **No spy that records its argument and returns a canned value.** Where a `PolicyApprover` is needed in `pkg/agent`, it must be one whose outcome is produced by real state it owns (e.g. a bounded queue that genuinely saturates and genuinely drains), not a recorder. Where registry state is asserted, use the real `approvalRegistryV2` in `pkg/gateway` and assert on `pendingCount`/entry state, never a call log.
- **Real store-backed state and real turn registration.** Shape references: `pkg/agent/task_executor_no_per_agent_cap_test.go` (real `newTestAgentLoop` + real `task.Store` + real `TaskExecutor`) and `pkg/entity/store_crossprocess_test.go`.
- Parent/child ids, agent ids and tool names must be **distinct values** in every fixture (never `"a"`/`"a"`).
- Tests touching concurrent state (`ts.mu`-guarded ledger, registry) run with `-race`.

**Order:**

1. **Unit — classification (`pkg/agent/tool_denial_test.go`).** Table-driven over all nine rows + an unknown reason. Asserts the `saturated`-is-the-only-retryable invariant, `known` on/off, and the fallback identity `ModelMessage == TranscriptText`. Also unit-tests the ledger: increments, per-pair isolation, ceiling at 10 not 9, empty on a fresh `turnState`.
2. **Unit — config sentinel.** `pkg/config/synthetic_floor_test.go` guards the shape: asserts via reflection that `GatewayConfig.TurnSyntheticErrorFloor`'s Go kind is `reflect.Pointer`, so a future revert to a bare `int` fails a test rather than silently re-inverting the sentinel. The *semantics* (nil ⇒ 8 armed; `0` ⇒ disabled; `7` ⇒ 7; `-1` ⇒ 8 + WARN) are tested in `pkg/agent/loop_tool_denial_test.go` against `syntheticErrorFloor()`, since that is where the function lives. The nil case is the one that must not be skipped — it is the whole defect.
3. **Unit — renderer parity (`pkg/agent/approval_transcript_denial_test.go`).** `askDenialText` delegates; the three added branches render; `settleAskToolCallTranscript` writes `permanent` inside `Result` and the entry still round-trips through `session.ToolCall`.
4. **Integration — loop behaviour (`pkg/agent/loop_tool_denial_test.go`).** Real `newTestAgentLoop`, scripted provider, real state-driven approver. Covers BDD-01, BDD-04, BDD-08, BDD-09. `-race`.
5. **Integration — registry-backed (`pkg/gateway/`).** Real `approvalRegistryV2` + `newPolicyApproverAdapter` + a real agent turn:
   - `tool_denial_quarantine_test.go` — BDD-05 / AC-03. `-race`.
   - `tool_denial_saturated_retry_test.go` — BDD-06 / AC-06. `max_pending = 1`. `-race`.
   - `tool_denial_timeout_regression_test.go` — BDD-01 + AC-02, the §1.1 regression: `ToolApprovalTimeout: 1` second, nobody answers, assert the turn ends **by itself**, the reason names tool + agent + `timeout`, and **wall-clock elapsed < 3 × the configured window** (not 10 ×). No manual Stop anywhere in the test.
6. **Integration — plan task (`pkg/agent/task_executor_tool_denial_test.go`).** BDD-07 / AC-04. Real `task.Store`, real `TaskExecutor`, real dispatch. Asserts `Status == failed`, `Result` substrings, `CancelReason == ""`, and no `memberCancelReasonMarker` prefix.
7. **Regression strengthening.** `pkg/agent/scenario_runturn_test.go`, `pkg/agent/subturn_delegate_nesting_test.go`, `pkg/agent/turn_recheck_test.go` — their existing `permission_denied` assertions are **kept and extended** to also pin `permanent` and the classified message (C4: nothing here needs inverting, because nothing pinned the false string).
8. **Contracts.** `make verify-contracts` + assert the diff touches no generated/contract path.

**Test datasets.** Reasons: the 7 registry literals, `no_approver_configured`, the headless literal, plus `"__unclassified_probe__"` for FR-058-03. Agents: `mia` (denied) and `jim` (allowed) — distinct. Tools: `bash` for the permanent path, `web_fetch` for the saturated path — distinct, so a test cannot pass by conflating them. Counts: 9 (below), 10 (at), 12 (batch overflow).

**Running tests in this pod — mandatory:**
- Build tags `goolm,stdjson` are required or `pkg/channels/matrix` will not compile (`[setup failed]` is a missing tag, not a bug).
- **Never run the full Go suite here** (OOM). One narrow test at a time:
  `CGO_ENABLED=0 go test -tags goolm,stdjson -race -run '^TestName$' -p 1 ./pkg/agent/`
- Full-suite verdicts come from CI or the `ci-omnipus` Fly worker, not this pod.

---

## 9. Work units (parallel waves)

**Ownership rule: no two units write the same file.** Read-only access to any file is unrestricted.

### Shared interface contract — land FIRST, in W1's first commit

W1 commits `pkg/agent/tool_denial.go` with these exact signatures (bodies complete) plus the `turnState` field, before W2–W4 begin. Everyone else codes against them.

```go
const toolDenialCeilingPerTurn = 10

type DenialClass struct {
    Reason         string
    Permanent      bool
    ModelMessage   string
    TranscriptText string
}

func ClassifyDenial(reason string) (DenialClass, bool)                       // bool = known
func denialPayloadJSON(tool, reason string, cls DenialClass) string
func toolDenialAbortReason(tool, agentID, reason string, count int) string

// on *turnState (pkg/agent/turn.go):
func (ts *turnState) recordToolDenial(tool, reason string) (count int, atCeiling bool)
func (ts *turnState) quarantineTool(tool, reason, payload string)
func (ts *turnState) quarantinedDenial(tool string) (payload string, ok bool)
```

`toolDenialAbortReason` format (pinned by test):
`blocked: tool %q denied (%s) for agent %q after %d attempts in this turn`

| Unit | Owns (writes) | Changes (`file::symbol`) | Tests it writes | Depends on |
|---|---|---|---|---|
| **W1** — classification core + per-turn ledger *(critical path)* | `pkg/agent/tool_denial.go` (new), `pkg/agent/turn.go` | New file: the constant, `DenialClass`, `ClassifyDenial`, `denialPayloadJSON`, `toolDenialAbortReason`. `turn.go::turnState` gains the ledger + quarantine maps and the three `ts.mu`-guarded methods above. | `pkg/agent/tool_denial_test.go` | — |
| **W2** — single renderer | `pkg/agent/approval_transcript.go` | `::askDenialText` → returns `ClassifyDenial(reason).TranscriptText`, own `switch` deleted. `::settleAskToolCallTranscript` → adds `"permanent"` inside `Result`. | `pkg/agent/approval_transcript_denial_test.go` | W1 signatures |
| **W3** — loop rewiring | `pkg/agent/loop.go` | All three `permission_denied` builders → `denialPayloadJSON`. New quarantine gate in `::AgentLoop.runTurn`'s dispatch loop (before `hooks.BeforeTool`). Three denial sites: `recordSyntheticDeny` → `recordToolDenial` + `abortTurn(ts,"tool_denial_ceiling",…)`. `::AgentLoop.syntheticErrorFloor` → `*int` semantics. Tool-assembly-duplicate site untouched. | — (behavioural tests are W5's, TDD-first) | W1 signatures, W4 config shape |
| **W4** — config sentinel + reason constants | `pkg/config/config.go`, `pkg/gateway/approvals.go`, `pkg/agent/abort_turn_system_test.go` | `config.go::GatewayConfig.TurnSyntheticErrorFloor` `int`→`*int` + rewritten doc + negative-value validation. `approvals.go`: the 7 reason literals → named constants + one slice; **no behaviour change, `fireTimeout` untouched**. `abort_turn_system_test.go`: update the one `TurnSyntheticErrorFloor:` assignment. | `pkg/config/synthetic_floor_test.go`, `pkg/gateway/approval_denial_classification_test.go` | W1 signatures |
| **W5** — loop behavioural tests | `pkg/agent/loop_tool_denial_test.go` (new) | — | BDD-01, BDD-04, BDD-08, BDD-09 + the four `syntheticErrorFloor()` semantics cases (`-race`) | W1; asserts W3, W4 |
| **W6** — registry-backed + plan-task tests | `pkg/gateway/tool_denial_quarantine_test.go`, `pkg/gateway/tool_denial_saturated_retry_test.go`, `pkg/gateway/tool_denial_timeout_regression_test.go`, `pkg/agent/task_executor_tool_denial_test.go` (all new) | — | BDD-05, BDD-06, BDD-07 + AC-02 (`-race`) | W1; asserts W3, W4 |
| **W7** — regression strengthening + contract gate | `pkg/agent/scenario_runturn_test.go`, `pkg/agent/subturn_delegate_nesting_test.go`, `pkg/agent/turn_recheck_test.go` | Extend existing `permission_denied` assertions to pin `permanent` + classified message. Run `make verify-contracts` and assert an empty generated/contract diff. | — (edits existing) | W3 |

### Integration order

```
Wave 0 (1 agent, blocking):  W1  — lands the shared contract file + turnState fields
Wave 1 (4 agents, parallel): W2 · W3 · W4 · W5      (W5 writes failing tests first — TDD)
Wave 2 (2 agents, parallel): W6 · W7
Wave 3:                      7-reviewer quality gate on the whole diff, then PR
```

W2/W3/W4 write four disjoint files (`approval_transcript.go`, `loop.go`, `{config.go, approvals.go, abort_turn_system_test.go}`) and can run fully concurrently once W1 has landed. W5 writes only a new test file, so it collides with nobody and can start in Wave 1 alongside the implementation it tests.

**Git-index caution:** parallel agents share `.git/index`. Each unit commits with `git commit --only <its own paths>`.

---

## 10. Definition of done

- [ ] FR-058-01 … FR-058-15 all implemented and each covered by at least one test in §7.
- [ ] AC-01 … AC-09 all asserted; AC-06 (the positive lower bound) and AC-02 (the §1.1 regression) present and passing.
- [ ] `gofmt -l . | wc -l` → 0; `golangci-lint run --build-tags=goolm,stdjson` → exit 0.
- [ ] CI green on `go test -tags goolm,stdjson -count=1 ./...` and the `-race` job. **Verdict from CI or `ci-omnipus`, never from a full local run.**
- [ ] `make verify-contracts` exit 0 **and** `git diff --name-only` contains no path under `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/`.
- [ ] `grep -rn "User denied tool execution" pkg/` returns exactly one line, in `pkg/agent/tool_denial.go` (the classified `user` row). It returns two lines in `pkg/agent/loop.go` today.
- [ ] `grep -c "al.recordSyntheticDeny(ts)" pkg/agent/loop.go` returns `1` (the tool-assembly-duplicate site). It returns `4` today.
- [ ] Constraint #7: nothing deferred. S1 and S2 (§3.3) are recorded as follow-up issues before merge, not as unstated gaps.

---

## 11. Open items carried forward (not decided here)

1. **S1** — plan-task lifecycle records lose the failure reason (`"execution_error"` literal). File an issue.
2. **S2** — `task.Task` has no structured failure enum, unlike `plan.FailedReason`. File an issue.
3. **ADR §5** — upstream tool-policy feasibility validation at plan-approve time. The 24/32 UAT failures are made honest and fast by this change; they are not prevented.
4. **ADR §9 Q1/Q2/Q3** — per-turn vs per-task ceiling; back-off for `saturated`; extending `permanent` to `DelegationFailure` / `PermissionDeniedResult` (priced in §3.2, needs the contract pipeline).
5. **[INFERRED]** 10 remains an unmeasured operator constant. If `tool_denial_ceiling` terminations appear in production logs, that is a signal the D1/D2 messages are not landing — not the system working as designed (ADR §3).
