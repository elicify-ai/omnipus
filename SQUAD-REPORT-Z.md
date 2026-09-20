# Squad Z report — issue #782

## Outcome

System-woken turns now keep successful raw tool output suppressed while making tool failures visible and attributable.

- External messaging channels receive `Tool \`<name>\` failed:\n<detail>` through their ordinary outbound route.
- Synchronous webchat failures continue to use the existing structured tool-result frame; no duplicate text bubble was added.
- Asynchronous webchat failures use a separate, non-terminal structured tool notice with a server-unique ID. A late notice is rendered separately, so a provider-reused call ID cannot overwrite another turn's tool card.
- Immediate async callbacks are ordered after the start acknowledgement, and every post-dispatch abort releases the callback gate.
- Successful synchronous and asynchronous output remains suppressed on system-woken turns.

## Baseline reality established before the fix

| Surface | Before this change | Evidence |
|---|---|---|
| Webchat, synchronous tool failure | Already visible as a structured `tool_call_result` error; plain-text `ForUser` was suppressed. | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.recordToolResult`; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/gateway/websocket_forward.go::eventForwardState.onToolExecEnd` |
| Webchat, asynchronous callback failure | The ordinary structured result described only the async start acknowledgement. The later callback had no structured completion notice. | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.prepareDispatch` |
| External messaging channel | No structured tool UI exists. `SuppressToolFeedback` removed both successful output and errors, leaving no fallback. Namespaced channel IDs such as `telegram.main` also required normalization. | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/loop_inbound.go::AgentLoop.processSystemMessage`; `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.deliverToolOutput` |

## Root cause

`AgentLoop.processSystemMessage` correctly sets `SuppressToolFeedback: true` to enforce issue #766's containment. Both plain-text tool publication sites treated that setting as an unconditional drop, without distinguishing successful output from `ToolResult.IsError`. The synchronous site is `agentLoopRunTurnToolsExecute.deliverToolOutput`; the asynchronous site is the callback installed by `agentLoopRunTurnToolsExecute.prepareDispatch`.

The webchat's synchronous path was not actually silent because `agentLoopRunTurnToolsExecute.recordToolResult` independently emits `EventKindToolExecEnd`, which `eventForwardState.onToolExecEnd` converts to a structured error card. External adapters have no equivalent structured frame.

Two additional failure modes were found while proving the async path:

- A callback that completed inline could be overwritten by the async-start acknowledgement, or stranded forever if an `AfterTool` hook/hard abort exited before normal persistence.
- A genuinely delayed result arrived after the SPA's `done` handler had baked the original call. Matching only the provider's call ID was unsafe because providers can reuse IDs such as `call_0` across turns.

## Fix

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/loop_run_turn_tools.go::toolResultUserContent` centralizes the policy: ordinary turns keep existing behavior; suppressed successes remain empty; suppressed external errors become attributed notices after channel-instance normalization.
- `::agentLoopRunTurnToolsExecute.handleAsyncResult` sends external notices with the originating transcript session. For webchat callback errors it persists an attributed notice and emits a non-terminal `ToolExecEnd` using a server-unique notice ID.
- `::asyncToolCallbackGate` and `::agentLoopRunTurnToolsExecute.releaseAsyncCallback` order inline completions and cover normal finish, hard abort, `HookActionAbortTurn`, and `HookActionHardAbort`.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/src/store/chat/slices/frames.ts::appendUnmatchedToolError` renders an unmatched error result as a standalone completed tool notice. It does not mutate an active or historical call carrying a reused provider ID. Unknown non-error results retain the prior debug-only behavior.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/tools/AGENTS.md` was mechanically synchronized to its canonical `CLAUDE.md` twin so the mandatory twin-file guard reflects issue #761's already-ratified God-mode wording.

No wire contract changed; the fix uses the existing `tool_call_result` frame.

## RED receipts

All commands captured exit status directly before inspecting output.

| Receipt | Result before the relevant fix |
|---|---|
| External sync/async reproduction | `/tmp/squad-z-red.log`: exit 1; `TestProcessSystemMessage_ExternalChannelPublishesAttributedToolError` and `...AttributedAsyncToolError` failed because only final narration was outbound. |
| Full core-fix revert | `/tmp/squad-z-final-revert-red.log`: exit 1; external sync, external async, and webchat async regressions all failed. |
| Immediate callback ordering mutation | `/tmp/squad-z-tool-event-order-red.log`: exit 1; the async-start success overwrote the callback error. |
| Post-dispatch abort mutation | `/tmp/squad-z-abort-red.log`: exit 1; `TestProcessSystemMessage_ImmediateAsyncErrorSurvivesPostDispatchAbort` found no callback error event. |
| Late browser result | `/tmp/squad-z-frontend-red.log`: exit 1; the store logged `resolveToolCall for unknown call_id` and left the baked success unchanged. |

These independent reversions/mutations prove the tests can detect removal of the external fallback, non-terminal webchat event, callback ordering/release, and late-result rendering.

## GREEN receipts

| Check | Verified result |
|---|---|
| Focused agent matrix | `/tmp/squad-z-final-agent.log`: exit 0; 10 named tests passed, including the two unmodified #766 tests, external sync/async errors, success containment, webchat structured sync/async errors, abort release, and outbound session routing. |
| Frontend store regressions | `/tmp/squad-z-final-frontend.log`: exit 0; 2 files / 18 tests passed, including the reused-`call_0` late-error scenario. |
| Gateway mapping/replay | `/tmp/squad-z-final-gateway.log`: exit 0; live structured tool-error mapping and persisted generic error replay passed. |
| TypeScript | `/tmp/squad-z-final-typecheck.log`: exit 0 from `npm run typecheck`. |
| Guard suite | `/tmp/squad-z-final-guards.log`: exit 0; all 27 guards passed. |
| Size budgets | `/tmp/squad-z-final-budgets.log`: exit 0; warnings only, no new grandfathering. |
| Diff hygiene | `git diff --check`: exit 0. |
| Code-impact check | The refreshed GitNexus index rated each edited entry point LOW risk. Its final comparison against `79ae822b6` found the expected three chat-frame execution flows and an aggregate MEDIUM change scope; no unrelated process was reported. |

Reviewer verification was clean for correctness/concurrency, silent failures, and test strength. The focused Go regression set was also exercised under the race detector by the test reviewer.

## #766 containment proof

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/system_turn_tool_output_test.go` has no diff.
- Its blob hash remains `812d65d5a97e1bc31384b5b954760d6c2c4baeb5`.
- `TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration` passed.
- `TestProcessSystemMessage_SuppressesAsyncToolOutputButDeliversNarration` passed.

## Definition of done

Code correct and tested: focused backend, gateway, frontend, typecheck, guards, budgets, revert/mutation, and reviewer checks passed.

Reachable by a user/agent: external notices traverse `MessageBus.PublishOutbound`; webchat notices traverse the existing agent-event → gateway `tool_call_result` → SPA store path and remain visible when they arrive after `done`.

## Honest gaps

- The full Go suite was not run locally, as the repository explicitly forbids it in this environment. No CI run exists because this squad must not push.
- No live Telegram adapter or browser end-to-end session was exercised; delivery was verified at the real bus/event, gateway-frame, and SPA-store boundaries.
- Legacy SSE does not consume the WebSocket structured tool-event path and remains outside this change. The supported SPA WebSocket and external messaging routes are covered.
- The repository's dependency install reported existing audit findings; `package.json` and `package-lock.json` were unchanged.

## ROUND 2 — release/v0.1.1 merge conflict resolution

### Resolution

Merged `origin/release/v0.1.1` into `squad/z-tool-errors` and resolved the single conflict in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.prepareDispatch` without taking either side wholesale.

- Squad Z's `asyncToolCallbackGate` remains the only callback entry point, so immediate completions still wait for the async-start acknowledgement and every normal/abort release path remains intact.
- Squad AA's captured `allowTopLevelToolFeedback` predicate is passed through the callback gate into `::agentLoopRunTurnToolsExecute.handleAsyncResult`.
- Ordinary async `ForUser` publication requires AA's predicate. A separate exception permits only an error from a response-producing root system turn: `IsError && SuppressToolFeedback && SendResponse && depth == 0 && !IsTaskRun`.
- The exception therefore cannot publish delegated-child, task-run, or verifier output. Successful system-turn output also remains suppressed.
- System-turn external error notices retain the originating transcript session. Ordinary interactive-root async feedback keeps AA's pre-merge outbound shape with an empty `SessionID`.

The fixes are semantically compatible; no behavior was dropped.

### Combined green receipt

Backend command (scoped to the complete union of `async_child_publication_test.go`, Squad Z's seven new tests, and the two unmodified #766 tests):

```text
exit=0
--- PASS: TestDelegatedChild_GenericAsyncForUserDoesNotPublishOnParentRoute
--- PASS: TestInteractiveRoot_GenericAsyncForUserStillPublishes
--- PASS: TestDelegatedChild_ToolPreviewDoesNotPublishTopLevelOnExternalChannel
--- PASS: TestInteractiveRoot_ToolPreviewStillPublishesOnExternalChannel
--- PASS: TestInternalSessions_AsyncForUserDoesNotPublishTopLevel
    --- PASS: TestInternalSessions_AsyncForUserDoesNotPublishTopLevel/task
    --- PASS: TestInternalSessions_AsyncForUserDoesNotPublishTopLevel/verifier
--- PASS: TestProcessSystemMessage_ExternalChannelPublishesAttributedToolError
--- PASS: TestProcessSystemMessage_ExternalChannelPublishesAttributedAsyncToolError
--- PASS: TestProcessSystemMessage_ExternalChannelSuppressesSuccessfulToolOutput
--- PASS: TestProcessSystemMessage_ExternalChannelSuppressesSuccessfulAsyncToolOutput
--- PASS: TestProcessSystemMessage_WebchatKeepsToolErrorStructured
--- PASS: TestProcessSystemMessage_WebchatPublishesAttributedAsyncToolError
--- PASS: TestProcessSystemMessage_ImmediateAsyncErrorSurvivesPostDispatchAbort
--- PASS: TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration
--- PASS: TestProcessSystemMessage_SuppressesAsyncToolOutputButDeliversNarration
PASS
ok github.com/elicify-ai/omnipus/pkg/agent 12.283s
```

SPA command: `npx vitest run src/store/chat.async-tool-error.test.ts`

```text
exit=0
Test Files  1 passed (1)
Tests       1 passed (1)
Duration    2.98s
```

The two route-discovery warnings printed by Vitest are unchanged and non-fatal. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/z-tool-errors/pkg/agent/system_turn_tool_output_test.go` remains unmodified at blob `812d65d5a97e1bc31384b5b954760d6c2c4baeb5`.
