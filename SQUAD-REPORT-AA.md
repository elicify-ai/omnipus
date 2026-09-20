# Squad AA report — issue #783 async/child publication

## Outcome

All three suspected defects **REPRODUCED**. None is classified as not reproducible.

| Suspected defect | Result | Observed user-visible publication |
|---|---|---|
| Generic async tool inside a delegated child publishes on the parent route | **REPRODUCED** | `ASYNC-CHILD-OUTPUT-MUST-NOT-REACH-TOP-LEVEL-CHAT` was published to `telegram / parent-chat` after the child turn returned. |
| Child tool-call preview publishes top-level on an external channel | **REPRODUCED** | `[tool] child_preview_probe` plus `{"argument":"child-only"}` was published to `telegram / parent-chat`. The valid tool also executed exactly once, so this was not a preview of a rejected call. |
| Internal task/verifier sessions accept async top-level `ForUser` despite `SendResponse=false` | **REPRODUCED** | The marker was published to both `webchat / task:783` and `webchat / task:783-verifier`. This confirms that `Channel == "webchat"` does not prove a live human-originated turn. |

## RED reproduction receipt

The reproduction tests are in:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/async_child_publication_test.go::TestDelegatedChild_GenericAsyncForUserDoesNotPublishOnParentRoute`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/async_child_publication_test.go::TestDelegatedChild_ToolPreviewDoesNotPublishTopLevelOnExternalChannel`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/async_child_publication_test.go::TestInternalSessions_AsyncForUserDoesNotPublishTopLevel`

They use the production `spawnSubTurn`, `processTaskDirect`, and `dispatchVerifierTurn` entry points. The async probe blocks its callback until the originating turn has returned, then the test releases it, waits for completion, and requires exactly one callback. Preview probes require exactly one real tool execution before checking publication.

Pre-fix focused run:

```text
exit=1
TestDelegatedChild_GenericAsyncForUserDoesNotPublishOnParentRoute:
  Should be empty, but was [{telegram parent-chat ... ASYNC-CHILD-OUTPUT-MUST-NOT-REACH-TOP-LEVEL-CHAT ...}]

TestDelegatedChild_ToolPreviewDoesNotPublishTopLevelOnExternalChannel:
  Should be empty, but was [{telegram parent-chat ... [tool] `child_preview_probe`
  {"argument":"child-only"} ...}]

TestInternalSessions_AsyncForUserDoesNotPublishTopLevel/task:
  Should be empty, but was [{webchat task:783 ... ASYNC-CHILD-OUTPUT-MUST-NOT-REACH-TOP-LEVEL-CHAT ...}]

TestInternalSessions_AsyncForUserDoesNotPublishTopLevel/verifier:
  Should be empty, but was [{webchat task:783-verifier ... ASYNC-CHILD-OUTPUT-MUST-NOT-REACH-TOP-LEVEL-CHAT ...}]
```

## Root cause

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.prepareDispatch` had two automatic top-level publication paths:

- external-channel tool previews checked configuration, `SuppressToolFeedback`, and the channel allowlist;
- async `ForUser` checked `Silent`, non-empty content, and `SuppressToolFeedback`.

Neither path checked the existing turn-origin proxies:

- delegated children have `turnState.depth > 0` and inherit their parent's channel/chat route through `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/subturn.go::spawnSubTurnState.prepareProcessOptions`;
- native task and verifier turns set `processOptions.IsTaskRun` and use internal `webchat`-labelled routes through `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/task_executor.go::AgentLoop.processTaskDirect` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/verifier_adjudication.go::AgentLoop.dispatchVerifierTurn`.

`SendResponse=false` did not protect any of these async/preview paths.

## Minimal fix

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.prepareDispatch` now derives one local predicate:

```text
not SuppressToolFeedback AND root depth AND not IsTaskRun
```

Both automatic top-level publication sites use that predicate. This adds no configuration field and no new boolean flag family. Existing interactive root behavior remains covered by positive controls for both async `ForUser` and external tool previews.

This is explicitly temporary containment. In the proposed compiled per-turn publication model, the predicate folds into the turn's origin/audience policy: automatic tool feedback for delegated-child, task, and verifier origins resolves to parent/caller-only or suppressed, while interactive-root feedback can resolve to top-level delivery. The callback should eventually capture that compiled policy rather than `depth`, `IsTaskRun`, and `SuppressToolFeedback` proxies.

## GREEN receipt

Exact-tree focused run, uncached and verbose:

```text
exit=0
PASS TestDelegatedChild_GenericAsyncForUserDoesNotPublishOnParentRoute
PASS TestInteractiveRoot_GenericAsyncForUserStillPublishes
PASS TestDelegatedChild_ToolPreviewDoesNotPublishTopLevelOnExternalChannel
PASS TestInteractiveRoot_ToolPreviewStillPublishesOnExternalChannel
PASS TestInternalSessions_AsyncForUserDoesNotPublishTopLevel
PASS TestInternalSessions_AsyncForUserDoesNotPublishTopLevel/task
PASS TestInternalSessions_AsyncForUserDoesNotPublishTopLevel/verifier
PASS TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration
PASS TestProcessSystemMessage_SuppressesAsyncToolOutputButDeliversNarration
ok github.com/elicify-ai/omnipus/pkg/agent
```

The protected file `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/agent/system_turn_tool_output_test.go` is unmodified (`git diff --exit-code` returned `0`) and both #766 tests pass.

Additional gates:

```text
make lint-budgets: exit=0
make lint-guards: exit=0 — all 27 guards passed
git diff --check: exit=0
```

## Revert-proof receipt

The production predicate was removed while leaving the strengthened tests intact. The focused negative tests failed again:

```text
exit=1
delegated async child: leaked marker to telegram / parent-chat
delegated child preview: leaked valid child tool arguments to telegram / parent-chat
internal task: leaked marker to webchat / task:783
internal verifier: leaked marker to webchat / task:783-verifier
```

The fix was then restored, and the exact-tree GREEN run above passed.

## Review and impact evidence

- Current GitNexus impact for `prepareDispatch`: **LOW**, one direct caller, no mapped process impact.
- `spawnSubTurn` impact: **CRITICAL**, 86 upstream symbols. It was exercised but not modified.
- `processTaskDirect` impact: **MEDIUM**; `dispatchVerifierTurn`: **LOW**. They were exercised but not modified.
- Independent code review: clean.
- Silent-failure review: the initial test-strength findings were fixed; re-review clean for this diff.
- Comment review: both accuracy findings were fixed; re-review clean.

## Mandatory delivery status

**Code correct and tested:** yes — all reproduced defects have mutation-proven tests, positive root controls pass, the unchanged #766 containment passes, budgets pass, and 27/27 guards pass.

**Reachable by a user/agent:** yes — the tests invoke the real delegated-child, native task, and verifier turn entry points and observe the real outbound bus; no unregistered feature surface was added.

## Honest gaps

- The full Go suite was not run locally because repository policy forbids it in this environment; CI is the authority for the full tagged suite.
- No live Telegram adapter or browser capture was used. The reproduction reached the real outbound bus with the real routes and payloads, but adapter presentation was not separately captured.
- A pre-existing async failure mode remains outside issue #783: if `PublishOutbound` itself errors, the callback logs a warning and cannot propagate the delivery failure; a `ForUser`-only completion can therefore be lost. This diff neither introduces nor worsens that behavior.
- The containment still relies on proxy fields rather than a first-class compiled publication policy. The broader audit's proposed policy remains the durable solution.
- `make lint-guards` initially exposed pre-existing drift between `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/tools/CLAUDE.md` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/pkg/tools/AGENTS.md`. The stale `AGENTS.md` god-mode wording was mechanically synchronized to the current issue-#761 rule so the mandatory 27/27 guard gate could pass. This is unrelated to the #783 product fix.
- The user-supplied `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/SQUAD-BRIEF-AA.md` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/aa-async-child/AUDIT-REFERENCE.md` remain untracked inputs and are not part of the commit.
