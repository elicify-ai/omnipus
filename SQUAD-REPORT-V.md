# Squad V report — issue #766

## Outcome

System-woken turns still publish their own final narration, but no longer publish a tool's
`ForUser` payload as ordinary chat text. The same turn-level suppression covers synchronous
tool results and asynchronous completion callbacks, so the fix is tool-agnostic rather than
specific to `bash`.

**Code correct and tested:** yes for the scoped agent-loop behavior, with mutation proof below.

**Reachable by a user/agent:** the fixed path is the existing `stop_plan` → owner wake →
`processSystemMessage` flow. Real-browser acceptance remains for the orchestrator and is not
claimed here.

## RED receipt

Test written before any product change:
`TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration`.

Command:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration$' -p 1 github.com/elicify-ai/omnipus/pkg/agent
```

Exit and failure:

```text
exit=1
--- FAIL: TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration
expected: []string{"Your plan was stopped."}
actual  : []string{"RAW-TOOL-OUTPUT-MUST-NOT-REACH-CHAT", "Your plan was stopped."}
FAIL github.com/elicify-ai/omnipus/pkg/agent
```

This failed for the reported defect: the synthetic tool executed successfully, its raw
`ForUser` marker reached the outbound chat bus, and the turn's narration also arrived.

## Verified root cause

The brief's behavior is substantially correct, but several paths moved after file splits:

1. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/plan_engine.go::PlanEngine.StopPlan`
   calls `PlanEngine.wakeOwner`, which sends a system wake addressed to the plan's originating
   chat.
2. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop_inbound.go::AgentLoop.processSystemMessage`
   reconstructs the owner turn with `SendResponse: true`. Ordinary interactive chat turns use
   `SendResponse: false` in
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop_process_message.go::agentLoopProcessMessage.prepareTurn`.
3. The synchronous publication decision is now
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.deliverToolOutput`.
   Before this fix it published any non-silent, non-empty `ForUser` result when
   `SendResponse` was true.
4. A second leak site exists in
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.prepareDispatch`:
   an asynchronous callback published `ForUser` without consulting `SendResponse` at all.
5. Full raw command output is placed in `ForUser` by
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/tools/shell.go::foregroundResultFromSandbox`.
   Other tools also produce `ForUser`, so a bash-specific fix would be incomplete.
6. The streamed-dedupe mechanism is armed only from
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/gateway/websocket_streamer.go::wsStreamer.Finalize`
   through `wsStreamerFinalize.sendDone` and `webchatChannel.markStreamed`. The live report that
   issue #760 prevented finalization is field evidence; this squad did not independently prove
   that runtime fact from code alone.
7. The fallback outbound path
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/gateway/webchat_channel.go::webchatChannel.Send`
   emits a token frame. The current frontend consumer is
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/src/store/chat/slices/frames.ts::createFrameSlice`,
   not the older `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/src/store/chat.ts`
   path in the brief.

The delegate precedent is valid in principle but the date in the brief is not: commit
`b137778ef9ce0757c6eb08b36ad2f7b3b6fa8913` introduced the delegate `ForUser` clearing on
2026-07-12 UTC. It now lives in
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/tools/delegate_run.go::delegateToolExecuteAsync.runSubturn`.
That local fix is appropriate because only the delegate tool knows child first-person output
must be wrapped for the parent rather than spoken directly. Issue #766 is instead a turn-origin
rule, so it belongs at the generic publication decisions.

## Fix

- Expanded the existing internal `processOptions.SuppressToolFeedback` contract to cover
  user-facing tool result feedback as well as tool-call previews.
- Set that switch only in
  `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop_inbound.go::AgentLoop.processSystemMessage`.
- Applied it to both raw-result publication sites:
  `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.deliverToolOutput`
  and `::agentLoopRunTurnToolsExecute.prepareDispatch`.
- Left `SendResponse: true` unchanged, so
  `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/v-chat-leak/pkg/agent/loop.go::AgentLoop.runAgentLoop`
  still publishes the turn's final narration.

Rejected alternatives:

- Silencing system turns: would remove the required plan-stopped narration.
- Special-casing `bash`: would leave every other `ForUser` producer exposed.
- Clearing every tool's `ForUser`: would remove intentional feedback from ordinary turns and
  duplicate a turn-origin rule across tools.
- Relying on WebSocket streamed dedupe: it is downstream, finalization-dependent, and did not
  protect the reported live path.

No gateway/SPA wire format changed, so contract regeneration was not required.

## GREEN receipt

Final code, explicit test-name confirmation:

```text
CGO_ENABLED=0 go test -v -tags goolm,stdjson -count=1 -run '^TestProcessSystemMessage_Suppresses(Async)?ToolOutputButDeliversNarration$' -p 1 github.com/elicify-ai/omnipus/pkg/agent
exit=0
--- PASS: TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration
--- PASS: TestProcessSystemMessage_SuppressesAsyncToolOutputButDeliversNarration
PASS
ok github.com/elicify-ai/omnipus/pkg/agent 10.969s
```

Both tests assert that the only outbound message is `Your plan was stopped.`. Therefore the
same assertions guard suppression and the required narration path.

Additional checks:

```text
make lint-guards
exit=0
GUARD RUNNER: all 27 guards passed

make lint-budgets
exit=0
grandfathered: 70 functions; components over 240: 71

git diff --check
exit=0
```

Independent code review found no high-confidence issue. Independent simplification review
made no edits and found the implementation already minimal.

## Revert-proof receipt

I removed the exact final product fix while leaving both tests intact, then ran:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -run '^TestProcessSystemMessage_Suppresses(Async)?ToolOutputButDeliversNarration$' -p 1 github.com/elicify-ai/omnipus/pkg/agent
exit=1
--- FAIL: TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration
actual  : []string{"RAW-TOOL-OUTPUT-MUST-NOT-REACH-CHAT", "Your plan was stopped."}
--- FAIL: TestProcessSystemMessage_SuppressesAsyncToolOutputButDeliversNarration
actual  : []string{"ASYNC-RAW-TOOL-OUTPUT-MUST-NOT-REACH-CHAT", "Your plan was stopped."}
FAIL
```

After restoring the exact final fix:

```text
exit=0
ok github.com/elicify-ai/omnipus/pkg/agent 7.045s
```

The subsequent verbose GREEN receipt above names both passing tests explicitly.

## Orchestrator UI acceptance

Build and run the gateway from this branch. Reproduce the founder's original user action:

1. Open the plan owner's chat containing the pending `AskUserQuestion` card from an executing
   plan.
2. Submit an answer on that card. This resumes the parked turn; in the original flow its next
   action calls `stop_plan`, which wakes the plan owner on the same chat.
3. Drive the system-woken owner turn through a `ForUser`-carrying tool. The original instance
   used `bash` and produced a distinctive `find` listing plus `ls -la` dump.
4. Verify with Playwright that none of that raw listing/dump appears as assistant chat text.
5. Verify the owner turn's final plan-stopped narration still appears.

The exact triggering user action is **submitting the answer on the pending AskUserQuestion
card**; merely opening or refreshing the chat does not reproduce the original chain.

## Honest gaps

- This squad did not run Playwright or a live gateway. The orchestrator owns that required UI
  acceptance step.
- Per repository policy, the full Go suite was not run locally. Only the two focused agent-loop
  tests were run.
- GitNexus was attempted first, but its registered index points to another stale branch. A
  current-index refresh ran actively for roughly eighteen minutes without completing and was
  stopped. Required `impact` calls returned `UNKNOWN` because the split symbols were absent;
  `detect-changes` later returned the invalid false green `No changes detected`. Direct source,
  history, behavioral tests, independent review, and mutation receipts are the actual evidence.
- The report does not claim issue #760's non-finalization mechanism was reproduced; only the
  dedupe's dependency on finalization was verified from code.
