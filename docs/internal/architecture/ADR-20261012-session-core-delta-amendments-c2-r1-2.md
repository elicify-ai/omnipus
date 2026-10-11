# ADR-20261012 — Session-core delta amendments: message-scoped refusals (C2) and transient launch refusals (R1 #2)

| Header | Value |
|---|---|
| Status | Accepted — both decisions are implemented; landed in git on 2026-10-12 (combined-review row S2-10) because code comments and tests cite them. |
| Date | Decisions 2026-10-11, landed and amended 2026-10-12 |
| Author | Architect (both decision notes); landing by backend-lead |
| Amends | `docs/internal/specs/session-core-spec.md` (BDD-05.2, BDD-05.10, C-ADDRESS, FR-045) |

## Amendments made when this was landed (2026-10-12)

| # | Amendment | Why |
|---|---|---|
| 1 | **C2 correction (combined review X-1):** the rule "an `ErrorFrame` with `session_id` and `client_message_id` and no `first_message_error` is message-scoped" also covers the mid-turn **append-failure** error (`websocket_chat.go::sendAppendFailureError`) and the **client_message_id retry error for another account** (`ws_client_message_dedupe.go`). Both now carry `session_id`. The first-send errors (no chat exists yet) stay untagged. | The first version of the note listed only the @-recipient refusals; the sender's other message-scoped errors still dropped the Stop button and unlocked the composer while the server turn ran. |
| 2 | **`message_status` gains `held`** (founder ruling F1, 2026-10-12): an @-request to a **stopped** recipient is held; the sender's row gets `held` after `received`, and it is not a failure and never becomes `failed`. | A person writing `@Mia ...` to a stopped agent otherwise saw an ordinary "sent" row. |
| 3 | **R1 #2 comment correction:** a full run queue is not a refusal (the launcher queues it), so the PATCH task-start 409 now means only "the task's plan is not executing". | The cap case cannot reach this path (as the note itself says); comments still described a dispatch-cap 409. |
| 4 | **/clear outcome (S3-02 server half):** a refused or not-performed `/clear` publishes `message_status` `failed` for the `/clear` row; the marker row stays the success signal. | Replaces the client inferring the outcome from reply text and a 90-second timer. |

---

## Part 1 — C2: @-recipient refusal reconciles the sender's chat and row

Status: decided 2026-10-11. Author: architect. Grounding tip: origin/work/session-core-integration-20261010 (d96f29a8e). Source finding: R3-CONSOLIDATED.md row C2.

### Bottom line
This is a design question, but a very small one. **No new field, no new frame and no type change.** Two existing optional fields (`ErrorFrame.session_id`, `ErrorFrame.client_message_id`) and the existing `MessageStatusFrame` with `state: failed` are enough. The only contract edit is a description-only edit that states the rule. One SPA rule is also needed, because tagging the error with a session alone does NOT fix scenario B.

### Context
- The recipient refusal path sends a bare `ErrorFrame{Type, Message}` and returns: `pkg/gateway/websocket_chat.go::handleChatMessageToRecipient` (the `refuseRecipient` branch). The over-long `client_message_id` refusal above it does the same. No session_id, no client_message_id, no message_status.
- The refusal runs before `hcm` exists, so the deferred `sendMessageStatus("failed")` is not armed. `sendMessageStatus` also returns early when `hcm.sessionID == ""` (`websocket_chat.go::sendMessageStatus`).
- Two later failure paths in `pkg/gateway/ws_recipient.go::admitRecipientRequest` (source meta unreadable; `AdmitRequest` error) already tag `SessionId`, but carry no `client_message_id`.
- The SPA routes an untagged `error` by the active chat: `src/store/chat/slices/frames.ts` (`targetSid` resolver, falls back to `activeSid`). If the frame has a session_id, `targetSid = frameSessionId`, so scenario A (wrong chat) is fixed by the tag alone.
- Scenario B is NOT fixed by the tag alone. The `case 'error'` branch in `frames.ts` closes every streaming assistant bubble in the routed bucket, sets `isStreaming=false` and clears `activeTurnId`, and it never reads `frame.client_message_id`. A refused @-steer in chat X, with X's turn still live, would still drop Stop and unlock the composer while the server turn runs.
- The sender row is reconciled only by `frames.ts::applyMessageStatusFrame` (looks up `messagesById[client_message_id]` or `findFirstSendMessage`, sets `deliveryStatus`/`status`, calls `releaseUnstartedSend` on `failed`). It is keyed by `frame.session_id` bucket and `client_message_id`, both already required on `MessageStatusFrame` (`contracts/asyncapi.yaml`, `MessageStatusFrame`: required type, session_id, client_message_id, state; `failed` in the state enum).
- Precedent for the pair: `websocket_chat.go::sendAppendFailureError` sends an `ErrorFrame` carrying `client_message_id`, and its doc comment says the deferred `message_status` "failed" tick fires on top. `ErrorFrame.client_message_id` and `session_id` are both already optional properties (`contracts/asyncapi.yaml`, `ErrorFrame`).
- First-send errors are a separate path: `src/store/chat/slices/first-send-frames.ts` handles frames with `first_message_error`. Untouched.
- Recipient sends need an existing chat: `ws_recipient.go::refuseRecipient` refuses an empty `session_id`. So the sender's chat id is always known, except for that one refusal itself.

### Decision
**Schema change: none in shape.** `ErrorFrame` (inline in `contracts/asyncapi.yaml`, mirrored by hand in `contracts/components/schemas/ErrorFrame.yaml`) keeps `session_id?: string (max 255)` and `client_message_id?: string (1..128)`. `MessageStatusFrame` is unchanged. backend-lead makes a description-only edit, then regenerates (`scripts/gen-contracts.sh`; the generated diff is comments only):
1. `ErrorFrame.client_message_id` description: today "The initiating ordinary first message's client-supplied ID." becomes: the client_message_id of the user message this error is about. With `first_message_error` it is the first-send outcome (unchanged). Without `first_message_error` it marks a **message-scoped refusal**: the named message was refused and the error does not end any turn.
2. `ErrorFrame.session_id` description: add that a message-scoped refusal carries the chat the message was sent from.
3. No `required` change, no new enum, no `dependentRequired`. (`first_message_error` already requires `client_message_id`; the reverse is not required.)

**Server behaviour.** Every refusal of a message that carries a recipient (the `refuseRecipient` refusal, the over-long id refusal, and the two `admitRecipientRequest` failure frames) sends, to the sending connection, an `ErrorFrame` with `session_id` = the frame's `session_id` (the sender's chat) and `client_message_id` = the frame's id when it supplied one. When `client_message_id` is non-empty it also publishes `MessageStatusFrame{session_id: sender's chat, client_message_id, state: failed}` through `hubPublishFrame(frameSessionID, ..., wc)`, the same publisher `sendMessageStatus` uses, so the sender's tab gets it even if not bound. The refusal runs before `hcm` exists, so the handler publishes the tick itself (or `hcm.sessionID` is set to `frameSessionID` before `AdmitRequest`, so the existing deferred tick works for the two later paths). Ordering: error first, then the failed tick, as in `sendAppendFailureError`. Nothing is persisted on a refusal, which is why the row is the only thing to reconcile. Tag only when the `session_id` is non-empty (an empty one is the "needs an existing chat" refusal itself; it stays global and the SPA keeps today's behaviour).

**SPA behaviour.** `message_status` is already handled and marks the sender row failed in the `session_id` bucket, so the row exits "sending" with no change there. In `case 'error'` (`frames.ts`), add one early rule: an error frame that has `session_id` and `client_message_id` and no `first_message_error` is message-scoped. Route it to that session's bucket (already true), render the refusal text as a notice in that chat (a normal error bubble or inline notice, the existing error rendering), and do NOT sweep streaming bubbles, do NOT set `isStreaming=false`, do NOT clear `activeTurnId` or `markTurnFinished`. An error with no `client_message_id` keeps today's behaviour in full. The rule is keyed on the wire fields only, not on "was it an @ send", so it also covers `sendAppendFailureError` refusals of a mid-turn steer.

### Alternatives rejected
| Alternative | Why rejected |
|---|---|
| Tag the error with `session_id` only (the navigation session's first half, plus failed tick) and leave `case 'error'` alone | Fixes scenario A only. Scenario B (live turn in the sender's chat is cut short in the UI) stays, because the error branch terminates the turn. |
| `message_status` failed only, no error frame | The row fails but the user gets no reason text, and the `failed` state has no message field. |
| Error frame only, SPA marks the row from `client_message_id` | One frame, but duplicates the row-failure logic in `case 'error'` and diverges from the established error + failed-tick pair (`sendAppendFailureError`). Acceptable fallback if the pair proves hard to order; same schema. |
| New `reason`/`message` field on `MessageStatusFrame`, or a new `recipient_refused` frame | A new wire surface for something two existing frames already express. |
| New `ErrorFrame.scope: message` enum | Redundant with `client_message_id` present and `first_message_error` absent. |

### Consequences
Positive: no wire-shape change, generated Go and TS types change only in comments (`make verify-contracts` stays satisfied after regeneration); older clients that ignore the new tags behave as today; fixes a wider class (any `client_message_id`-tagged refusal of a mid-turn send). Negative: the rule "client_message_id without first_message_error = message-scoped" is implicit semantics on an existing field; it is documented in the schema description and must be covered by tests (frame with both fields while a turn streams: bubble stays streaming, Stop stays, row failed). Neutral: the failed tick is journaled by the hub with a `seq` for a message that was never persisted; the SPA ignores a status for an unknown row (`applyMessageStatusFrame` returns when no user row matches), so catch-up replay is harmless. Not changed: refusal logic, `refuseRecipient` rules, first-send handling.

Affected: `contracts/asyncapi.yaml` and `contracts/components/schemas/ErrorFrame.yaml` (descriptions; backend-lead), `pkg/gateway/websocket_chat.go::handleChatMessageToRecipient`, `pkg/gateway/ws_recipient.go::admitRecipientRequest` (backend-lead), `src/store/chat/slices/frames.ts::handleFrame case 'error'` (frontend-lead). Tests absent today per the consolidated row: `chat.recipient-send.test.ts`, `ChatScreen.agent-mention.test.tsx`.

Open points for the implementer, not blockers: (a) does the SPA always supply `client_message_id` on a recipient send? Unknown, I did not read the outbound builder (`outbound-lifecycle.ts`); without one only the error is sent and the row is not reconciled by this contract. (b) the over-long-id refusal has a `client_message_id` that is by definition invalid, so tag only `session_id` there.

### Evidence
| Claim | Evidence | Certainty |
|---|---|---|
| Recipient refusal sends untagged ErrorFrame and returns | `git show origin/work/session-core-integration-20261010:pkg/gateway/websocket_chat.go`, `handleChatMessageToRecipient` refuseRecipient branch, ErrorFrame{Type, Message} only | Verified |
| `sendMessageStatus` no-ops without sessionID and clientMessageID | same file, `sendMessageStatus` first lines | Verified |
| Two admit failure frames already tag SessionId, no client id | `pkg/gateway/ws_recipient.go::admitRecipientRequest` (read at tip) | Verified |
| ErrorFrame has optional session_id and client_message_id; MessageStatusFrame requires session_id and client_message_id, has failed | `contracts/asyncapi.yaml` ErrorFrame / MessageStatusFrame (saved from tip to /tmp/asyncapi_tip.yaml, read) | Verified |
| Error case sweeps bubbles and clears activeTurnId, never reads client_message_id | `src/store/chat/slices/frames.ts` `case 'error'` (read at tip); `grep client_message_id` in the file returned only the message_status line | Verified |
| Row reconciled only by applyMessageStatusFrame | same file; consistent with R3 row C2 evidence | Verified (read), no independent writer search |
| Error + failed tick precedent | `websocket_chat.go::sendAppendFailureError` doc comment and body | Verified |
| Failed tick is not emitted for the two late admit failures when prepareMessage did not run | reasoning from `validateFrameIDs` setting `hcm.sessionID` only in the prepare path | Inferred (not traced end to end) |
| SPA always sends client_message_id on recipient sends | not read | Unknown |
| Nothing run or built; no production code written | n/a | Verified |
| **Self-check** | Re-read the note against the task: shape (none), fields, server paragraph, SPA paragraph, evidence table all present; corrected one slip while drafting (first shell read of frames.ts failed on a zsh `$R:s` modifier, re-read with `${R}`); all file citations were opened in this task | Done |


---

## Part 2 — R1 #2: which launch or dispatch refusals leave the task startable

Status: decided 2026-10-11. Author: architect. Reads: commit 5f4ae40ce (work/session-core-delta-r1misc-20261011, unpushed), integration 4e9c16f59, R1-CONSOLIDATED.md row 2.

### Bottom line
**Confirm dev1's three** (caller cancelled or timed out, executor draining, dispatch cap full), with two corrections: the cap case cannot actually reach this code, and the rule must be written as "refused before the task was moved to in-progress". Everything else stays permanent (Failed). The spec and #1026 contain no ruling on which refusals are transient, so this is a design call, not a citation.

### Context
- `pkg/agent/task_executor_session_binding.go::failTaskBeforeDispatch` (before the commit) wrote Failed and the raw `cause.Error()` as Result for every cause. Callers: `task_executor.go::startTaskNowViaLauncherAs` (launch, binding and dispatch refusals), `launchMainTaskRun` path, and `ExecuteTask` (`assigneeMain` error).
- #1026 (as cited in `task_executor.go::startTaskNowViaLauncherAs`, "retry-once-then-Failed binding") is about the durable session binding: a binding that cannot be persisted after one retry fails the task. Its test file says #1026 is about goal_claim's owner exception (`goal_claim_task_session_read_error_test.go`). Neither says anything about caller cancellation, drain or cap. The issue text at gh #1026 is the goal-tools stress test, also silent. So #1026 supports keeping binding failures permanent and nothing else.
- Spec `docs/internal/specs/session-core-spec.md`: BDD-05.2 keeps "the existing running-queued-refused admission disposition"; BDD-05.10 wants "visible error/refusal/failed disposition as appropriate". It states no transient/permanent split, so "as appropriate" is the architect's to define.
- The task moves to in-progress only after admission succeeds: `task_executor.go::dispatchLaunchedTask` calls `emitStatusChanged(t, task.StatusInProgress)` as its last step. A refusal earlier than that leaves the task in its prior status. The run slot is released by `defer te.releaseReservedRunSlot` in `StartTaskNow`. So not failing the task cannot leave a phantom "running" task.
- At dispatch stage a session is already launched and bound. Restart is safe: `boundSessionRetryable` re-dispatches a bound session when its lifecycle record exists and the task is not Failed. Failing it instead would block that route ("Rerun the task").
- Cap: the dispatch semaphore is skipped on the launcher path ("The launcher's own admission replaces the dispatch semaphore", `ExecuteTask`, `te.launcher != nil` branch). `ErrDispatchCapReached` is returned only after that branch, as a plain error, not through `failTaskBeforeDispatch`. So on the launcher path the cap sentinel never reaches this function.
- The REST caller already copes with a non-Failed refusal: `pkg/gateway/rest_tasks.go` (StartTaskNow failure handling) keeps Failed only when `fresh.Result == startErr.Error()`, otherwise reverts the task to its pre-start status (`buildLaunchRevertPatch`), maps `ErrDispatchCapReached` to 409 with the fixed text, and any other error to a generic 500 "the task could not be started. Check the agent and try again; details are in the server log." plus a Warn log.

### Decision
| Refusal | Ruling | Reason |
|---|---|---|
| `context.Canceled`, `context.DeadlineExceeded` | Transient | Says nothing about the task; the caller gave up. |
| `ErrExecutorDraining` | Transient | Shutdown; the task must be startable after restart. Failing it would persist a shutdown artefact. |
| `ErrDispatchCapReached` | Transient (keep in the list) | Correct by meaning and wanted for the non-launcher path, but unreachable here today; the test must not claim it exercises `failTaskBeforeDispatch` through the launcher path. |
| Binding persist failure after the retry (#1026), mint or resolve errors, `ErrAgentUnknown`, `ErrDepthExceeded`, `ErrInvalidEdge`, `ErrStoreWrite`, `ErrTitleRequired`, `ErrTaskIDRequired`, delegation-policy denial | Permanent (Failed) | About the task or its setup; a retry repeats them. |
| Steering unavailable (`steer.IsSteeringUnavailable`: stopped, terminal, stale generation, cancelled, revival failed) | Permanent (Failed) | R1's "briefly unavailable" is not accurate: these are lifecycle states, and at dispatch stage the same session is dead, so a retry hits the same refusal. A scheduler tick would otherwise retry a stopped main forever. The Result is already the plain sentence from `newSteeringRefusalError`, not machinery text. |

Rule to put in the code comment and test: **transient = the refusal says nothing about the task and the task has not yet been moved to in-progress**. Match with `errors.Is` through the wraps (`mapLaunchRefusal` uses `%w`). Unknown wrapped causes default to permanent.

**What the person sees for a transient refusal (never silent).** The task keeps its prior status and no raw error is stored as Result. The caller always gets the error back: REST reverts to the pre-start status, logs Warn and answers 409 with the fixed cap text, or the generic "could not be started ... try again" 500; an agent calling `run_task` gets the returned error text. Recommendation for backend-lead, no contract-shape change: answer drain with 503 and a fixed "the server is shutting down, start the task again after restart", not the generic 500; and a cancelled caller has no one to answer, so the Warn log is its only trace (acceptable; the task is unchanged).

### Consequences
Positive: a cancelled browser request or a restart no longer kills a task; raw error text stops reaching Result for these cases (SG-03). Negative: a repeatedly cancelled start leaves no on-task trace; the half-launched dispatch-stage session (bound, goal activated by `activateTaskGoal`, run closed Failed) waits for the next start. Neutral: scheduled starts will simply retry on the next tick.

### Evidence
| Claim | Evidence | Certainty |
|---|---|---|
| Commit adds `isTransientLaunchRefusal` with exactly the four sentinels and returns the cause without writing Failed | `git show 5f4ae40ce -- pkg/agent/task_executor_session_binding.go` | Verified |
| Cap is not reachable through the launcher path | `/tmp/te.go` (= 4e9c16f59 `task_executor.go::ExecuteTask`): launcher branch returns before `dispatchSema.TryAcquire` | Verified (read) |
| Task becomes in-progress only after successful admission | `task_executor.go::dispatchLaunchedTask`, `emitStatusChanged(t, task.StatusInProgress)` | Verified (read) |
| Bound session is re-dispatchable when task is not Failed | `task_executor_session_binding.go::boundSessionRetryable`, `startTaskNowViaLauncherAs` first branch | Verified (read) |
| REST reverts and reports a non-Failed refusal | `pkg/gateway/rest_tasks.go` StartTaskNow failure block (read at 4e9c16f59) | Verified (read) |
| Spec has no transient/permanent ruling | grep of `session-core-spec.md` at 4e9c16f59 for transient, startable, retry later, drain: no relevant rule; BDD-05.2 and BDD-05.10 read | Verified (search could see these words: the BDD-05.2 hit proves the search works) |
| #1026 is about session binding and the goal_claim owner check, not refusals | `task_executor.go` comment at the launcher path; `goal_claim_task_session_read_error_test.go` header; `gh issue view 1026` | Verified |
| The launcher's own admission refusal (queued or saturated) has no identifiable sentinel here | not found in pkg/steer/errors.go; admission code not traced | Unknown |
| Dispatch-stage transient leaves a consistent half-launched session | reasoning from the code above, not run | Inferred |
| Nothing run or built; no production code written | n/a | Verified |
| **Self-check** | Re-read each ruling against R1 row 2 and the commit; corrected my first instinct that steering-unavailable is transient (rejected, reason in table) and found the cap case is dead on this path; all citations opened in this task | Done |

