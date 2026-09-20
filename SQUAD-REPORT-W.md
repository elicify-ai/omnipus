# Squad W report — issue #770

## Outcome

AskUserQuestion now emits structured lifecycle records for card creation, WebSocket fan-out, answer submission, default-safe auto-submit, and resume dispatch outcomes. Normal lifecycle events are `INFO`; dropped fan-out, stranded resume, timeout, and accepted-but-unresumable submissions are `WARN`.

No answer text, selected label, free text, resume payload, token, or raw dispatcher error is written by the new logging. Tests use secret sentinels on success and failure paths to enforce that boundary.

The separate missing-chat-record defect survives merged PR #762. This change deliberately does not fix it.

## Phase 0 verdict — missing answer chat record survives PR #762

PR #762 fixed the parked-turn dequeue race, and its integration test proves the replacement model turn receives the answer. It did not prove that the answer becomes a persisted user-role transcript entry.

I temporarily strengthened `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/agent/ask_user_resume_through_bus_test.go::TestAskUserResume_ThroughBus_ChatTargetAgent` to read the real transcript after the successful replacement turn and look only for the correlated card identifier. The assertion failed:

```text
--- FAIL: TestAskUserResume_ThroughBus_ChatTargetAgent
    the answered card must persist as a correlated user-role transcript record
    for card_id=ask_451e320b2718f6a7d6ee4e6ccce61e2c (entry_count=1)
FAIL
exit=1
```

The temporary assertion was then removed; no Phase 0 product change is included.

Root cause evidence:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/gateway/ws_ask_user.go::askUserResumeDispatcher.DispatchResume` publishes the correlated resume directly to the message bus with channel `webchat`.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/agent/loop.go::AgentLoop.processMessage` skips transcript writes for `webchat`, because ordinary WebSocket intake is expected to have persisted the user message already.
- The AskUserQuestion resume bypasses that ordinary WebSocket intake.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/agent/steering.go::AgentLoop.continueWithSteeringMessages` starts the replacement turn with initial steering messages but without a transcript store/session for that synthetic user-role answer.

Meaning: the answer reaches the model but has no path that persists it as a visible chat record. The orchestrator should file this as a separate behavioral defect.

## Phase 1 RED receipts

Before product edits, the registry logging tests failed because the lifecycle records did not exist:

```text
--- FAIL: TestLifecycleLogging_SubmitDeliveredCountsOnly
    missing registry log message "askuser: card created"
--- FAIL: TestDispatchResume_LogsStrandedAndTimedOut
    missing registry log message "askuser: resume dispatch completed"
--- FAIL: TestDefaultSafeAutoSubmit_LogsFiring
    missing registry log message "askuser: default-safe auto-submit fired"
FAIL
exit=1
```

The gateway fan-out test independently failed at the production broadcast seam:

```text
--- FAIL: TestBroadcastAskUserCard_FanOutAndDropCounter
    missing gateway log message "ws: ask_user_question broadcast"
FAIL
exit=1
```

Review-driven RED tests also reproduced the remaining risks before their fixes:

```text
TestHandleAskUserAnswer_RejectionLogDoesNotExposeSubmittedContent
    expected reason="invalid_answer"; actual=<nil>
    rejected-answer log exposed submitted answer content

TestHandleAskUserAnswer_AcceptedAnswerWithResumeFailureIsNotRejected
    missing gateway log message "ws: ask_user_answer accepted; resume failed"

TestDefaultSafeAutoSubmit_ResumeFailureLogRedactsDispatcherError
    expected reason="resume_dispatch_failed"; actual=<nil>
    auto-submit resume-failure log exposed answer content through dispatcher error

TestBroadcastAskUserCard_NoDropsLogsInfo
    expected level="INFO"; actual="WARN"
```

## Root cause

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/askuser/registry.go::Registry.CreatePending`, `Registry.Submit`, `Registry.fireDefaultSafeSet`, and `Registry.dispatchResume` had no lifecycle logging.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/gateway/ws_ask_user.go::WSHandler.broadcastAskUserCard` only emitted a warning for an individual full send buffer and had no aggregate fan-out receipt.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/gateway/ws_ask_user.go::askUserResumeDispatcher.DispatchResume` returned the message-bus result without recording whether enqueue succeeded, timed out, or became stranded.
- Rejected answer frames logged the raw validation error; those errors can contain submitted headers or selected labels.
- A valid answer is consumed before resume dispatch, so a later bus failure must not be described as answer rejection.

## Fix

| Lifecycle point | Level | Safe fields |
|---|---:|---|
| Card created | INFO | `card_id`, question count, default-safe count |
| Card broadcast | INFO, or WARN on drops | `card_id`, status, fan-out count, enqueued count, drop count |
| Answer submitted | INFO | `card_id`, answer count, selected-option count, free-text count, auto-default count |
| Default-safe auto-submit | INFO | `card_id`, question count, answer count |
| Resume enqueued | INFO | `card_id`, answer count, `outcome=delivered`, `delivery_stage=message_bus` |
| Resume stranded/timed out | WARN | `card_id`, answer count, fixed outcome/reason |
| Validation rejection | INFO | `card_id`, session identifier, fixed reason |
| Accepted answer whose resume fails | WARN | `card_id`, session identifier, fixed outcome/reason |

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/gateway/websocket_pump.go::WSHandler.broadcastRaw` now returns fan-out and drop counts while retaining each connection's drop counter. The aggregate record accurately calls successful channel writes `enqueued_count`; it does not claim the WebSocket writer completed delivery.

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/w-askuser-observability/pkg/askuser/registry.go::ErrResumeDispatch` marks only errors returned after a valid answer or cancellation has already been accepted and consumed. This lets the gateway distinguish a routine validation rejection from an accepted answer whose parked turn could not resume, without changing submission behavior.

No wire contract changed, so contract regeneration was not required.

## GREEN receipts

All AskUserQuestion registry package tests:

```text
CGO_ENABLED=0; tags=goolm,stdjson; count=1; parallelism=1
package: github.com/elicify-ai/omnipus/pkg/askuser/...
ok  github.com/elicify-ai/omnipus/pkg/askuser  5.688s
exit=0
```

All AskUserQuestion gateway mapping, fan-out, dispatcher, and inbound-answer tests:

```text
CGO_ENABLED=0; tags=goolm,stdjson; parallelism=1
tests: TestToAskUserCard_.*, TestBroadcastAskUserCard_.*,
       TestAskUserResumeDispatcher_.*, TestHandleAskUserAnswer_.*
ok  github.com/elicify-ai/omnipus/pkg/gateway  11.793s
exit=0
```

## Revert and mutation proof

With the production lifecycle logging temporarily removed and all tests retained, the registry tests failed on the missing created, stranded, and auto-submit records; gateway tests failed on missing broadcast, delivered, stranded, and timed-out records:

```text
--- FAIL: TestLifecycleLogging_SubmitDeliveredCountsOnly
    missing registry log message "askuser: card created"
--- FAIL: TestDispatchResume_LogsStrandedWithoutDispatcher
    missing registry log message "askuser: resume dispatch completed"
--- FAIL: TestDefaultSafeAutoSubmit_LogsFiring
    missing registry log message "askuser: default-safe auto-submit fired"
FAIL

--- FAIL: TestBroadcastAskUserCard_FanOutAndDropCounter
    missing gateway log message "ws: ask_user_question broadcast"
--- FAIL: TestAskUserResumeDispatcher_PublishesToBus
--- FAIL: TestAskUserResumeDispatcher_LogsStrandedWithoutBus
--- FAIL: TestAskUserResumeDispatcher_LogsTimeout
    missing gateway log message "ws: ask_user resume dispatch completed"
FAIL
```

Controlled mutations proved the strengthened assertions can detect false greens:

- Zeroing selected-option and auto-default counters failed `TestLifecycleLogging_SubmitDeliveredCountsOnly` with expected `1`, actual `0`.
- Injecting the recommended answer into the auto-submit record failed `TestDefaultSafeAutoSubmit_LogsFiring` with `default-safe auto-submit log exposed answer content`.
- Injecting `resume_text` into the delivered record failed `TestAskUserResumeDispatcher_PublishesToBus` with `delivered resume log exposed answer content`.
- Injecting accepted answer content into a resume-failure record failed `TestHandleAskUserAnswer_AcceptedAnswerWithResumeFailureIsNotRejected`.
- Logging a normal no-drop broadcast at WARN failed `TestBroadcastAskUserCard_NoDropsLogsInfo`.
- Restoring the old broadcast field and old rejection level failed the new `enqueued_count` and `INFO` assertions.

After each proof, the production implementation was restored and the GREEN commands above passed.

## Verification

- `git diff --check`: passed.
- GitNexus `detect-changes --scope all`: 5 files, 34 symbols, 0 affected processes, LOW risk, exit 0.
- File budget: passed, exit 0; only existing warnings.
- Function budget: passed, exit 0; only existing warnings.
- Repository guards: 27/27 passed.
- Seven-reviewer gate: correctness, silent failures, test quality, type design, comments, observability, and security/scope all CLEAN after findings were fixed and re-reviewed.

## Definition of done

Code correct and tested: scoped registry and gateway tests pass, secrecy assertions and revert/mutation proofs fail for the intended reasons, budgets pass, guards are 27/27, and all seven reviewers are clean.

Reachable by a user/agent: the records are emitted from the production registry, WebSocket broadcast, inbound answer, and message-bus resume paths; they are not isolated library helpers or test-only calls.

## Honest gaps

- The missing user-visible answer transcript record is reproduced and remains unfixed by design; it needs a separate issue.
- `outcome=delivered` is explicitly scoped by `delivery_stage=message_bus`. It proves successful message-bus enqueue, not that the downstream session worker accepted or completed the replacement turn.
- The full Go suite was not run, per the repository's memory-safety rule. Testing remained scoped to the two affected packages and named gateway paths.
- One initial full `github.com/elicify-ai/omnipus/pkg/askuser` package run saw `TestRestart_RearmFromPersistedState` miss its rehydrated set under concurrent machine load. The same test passed twice in isolated fresh runs, and the full package then passed twice, including the final 5.688-second run. It did not fail twice in isolation and no success claim relies on the failed run.
- No live production gateway was used. Reachability is verified at the real production seams with structured-log capture and a real in-process message bus.
