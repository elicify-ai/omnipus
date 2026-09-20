# Squad Y report — issue #781 delegated child narration

## Outcome

Delegated child narration is now suppressed at the shared external-stream
wrapper used by Telegram and WeCom. The parent's own draft, final narration,
and cancellation still reach the underlying channel streamer.

Code correct and tested: the focused regression test is green, the full fix was
mutation-tested by removing it and observing the test fail, all 27 guards pass,
and both size-budget gates exit 0.

Reachable by a user/agent: yes. The existing native delegated-child path obtains
its streamer through `MessageBus.GetStreamer` and `Manager.GetStreamer`; the
agent loop stamps the child turn's existing nesting correlation before its first
streamed token.

## Audit verification

The audit is correct about the user-visible code path:

1. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/agent/subturn.go::spawnSubTurnState.prepareProcessOptions` keeps the
   parent's external channel/chat route.
2. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.guardAndDispatch`
   places the delegate tool-call ID in the subturn context. Then
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/agent/subturn.go::spawnSubTurnSetupState.resolveDelegateIdentity`
   reads it and `spawnSubTurnState.configureChildTurn` assigns it to the child
   turn as a non-empty `parentSpawnCallID`.
3. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/agent/loop_run_turn.go::agentLoopRunTurn.callProvider`
   obtains a channel streamer and calls
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/agent/turn_stream.go::turnState.stampStreamerParentSpawnCallID` before
   the provider can emit its first update.
4. Before this fix, `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/channels/manager.go::Manager.GetStreamer` returned a
   `finalizeHookStreamer` that did not implement the optional nesting setter.
   Child `Update` and `Finalize` therefore reached the Telegram/WeCom streamer
   exactly like a root turn.
5. Webchat already stores the same nesting identifier in
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/gateway/websocket_streamer.go::wsStreamer.SetParentSpawnCallID`;
   `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/gateway/websocket_streamer.go::wsStreamer.Update`
   and `wsStreamer.Finalize` enforce the child shadowing decision.

One audit statement is too strong: the external streamer interface does not
need to change to carry `parentSpawnCallID`. Although `bus.Streamer` does not
declare the method, the agent loop already uses an optional
`SetParentSpawnCallID(string)` capability. Making the Manager wrapper implement
that existing capability is sufficient and avoids forcing meaningless methods
onto every streamer.

GitNexus impact analysis rated `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/channels/manager.go::finalizeHookStreamer`
LOW risk: one direct production dependent (`Manager.GetStreamer`) in the
Channels module. Its concrete `Finalize` result was explicitly a lower bound
because interface dispatch cannot be fully traced.

## RED receipt — reproduced before product code changed

Test:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/channels/manager_child_stream_test.go::TestManagerGetStreamer_ContainsChildNarrationAndDeliversParentNarration`

Command:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestManagerGetStreamer_ContainsChildNarrationAndDeliversParentNarration$' -v -count=1 -p 1 github.com/elicify-ai/omnipus/pkg/channels
```

Exit and decisive output:

```text
exit=1
delegated child narration reached telegram: updates=["child draft"] finalized=["child final"]
delegated child narration reached wecom: updates=["child draft"] finalized=["child final"]
--- PASS: .../telegram/parent_is_delivered
--- PASS: .../wecom/parent_is_delivered
FAIL
```

This reproduces the defect for both external streaming routes and independently
proves that the parent's narration already reached each route.

## Fix

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/channels/manager.go::finalizeHookStreamer` now consumes the existing
`parentSpawnCallID` nesting fact. When it is non-empty, `Update`, `Finalize`,
and `Cancel` do not touch the underlying external streamer and do not arm the
root stream's outbound duplicate-suppression marker. When it is empty, all
three operations retain their previous behavior and successful finalization
still arms duplicate suppression.

No boolean flag family, channel-specific switch, wire type, API contract, or
mandatory streamer-interface method was added. This is intentionally temporary
containment. In the durable model proposed by the audit, the non-empty nesting
fact would compile into a per-turn publication policy whose child-narration
decision is `shadow`/`parent-only`; this wrapper check would then be replaced by
that policy-aware publisher.

The seven-reviewer gate completed clean after one shared LOW finding was fixed:
the first version guarded `Update` and `Finalize` but inherited `Cancel`.
Reviewers noted that a future WeCom cancel call could consume the shared parent
turn. The final wrapper and test cover cancellation for both child and parent.

## GREEN receipt

The same focused command after the final fix:

```text
exit=0
--- PASS: TestManagerGetStreamer_ContainsChildNarrationAndDeliversParentNarration
    --- PASS: .../telegram/delegated_child_is_contained
    --- PASS: .../telegram/parent_is_delivered
    --- PASS: .../wecom/delegated_child_is_contained
    --- PASS: .../wecom/parent_is_delivered
PASS
ok github.com/elicify-ai/omnipus/pkg/channels
```

The child cases assert that the underlying streamer receives no update,
finalization, or cancellation and that no duplicate-suppression marker is
armed. The parent cases assert the opposite: draft, final narration, and cancel
all reach the underlying streamer, and finalization arms the marker.

## Revert-proof receipt

The complete production containment was removed while leaving the final test
unchanged. The focused command then produced:

```text
exit=1
delegated child stream reached telegram: updates=["child draft"] finalized=["child final"] cancels=1
delegated child stream reached wecom: updates=["child draft"] finalized=["child final"] cancels=1
--- PASS: .../telegram/parent_is_delivered
--- PASS: .../wecom/parent_is_delivered
FAIL
```

The exact production change was restored and the unchanged command produced:

```text
exit=0
--- PASS: TestManagerGetStreamer_ContainsChildNarrationAndDeliversParentNarration
    --- PASS: .../telegram/delegated_child_is_contained
    --- PASS: .../telegram/parent_is_delivered
    --- PASS: .../wecom/delegated_child_is_contained
    --- PASS: .../wecom/parent_is_delivered
PASS
```

This proves the test dies when the fix is reverted and that parent delivery is
not the reason for either RED.

## Other verification

```text
make lint-guards
exit=0
guards run: 27
GUARD RUNNER: all 27 guards passed

make lint-budgets
exit=0
```

The first guard run exposed a pre-existing byte mismatch between
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/tools/CLAUDE.md` and its required twin `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/tools/AGENTS.md`. The latter
still carried the superseded god-mode description. The newer issue #761 block
from `CLAUDE.md` was copied mechanically to `AGENTS.md`; the full guard rerun
then passed 27/27. This repair is unrelated to the issue #781 runtime change.

Per repository rules, the full Go suite was not run locally; CI is the
authority for the full tagged suite. No push or PR was made.

The required pre-commit GitNexus `detect-changes --scope all` completed before
the commits were created and reported LOW risk, 10 changed symbols, and zero
affected execution flows. After the separate documentation-sync commit moved
HEAD, an exact-HEAD index refresh was attempted; it failed during its final
checkpoint because GitNexus reached its 16 GB database limit. A subsequent
read attempt exited 139. The feature diff had not changed since the successful
analysis, so the commit proceeded using that recorded result; no exact-HEAD
GitNexus green is claimed.

## Channel coverage and honest gaps

Covered:

- Telegram delegated-child streaming at the shared
  `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/y-child-narration/pkg/channels/manager.go::Manager.GetStreamer` publication boundary.
- WeCom delegated-child streaming at that same boundary.
- Root/parent delivery on both named routes.
- `Update`, `Finalize`, `Cancel`, and duplicate-suppression state.

Not covered:

- No live Telegram Bot API or live WeCom runtime acceptance was executed. The
  regression test installs a recording `StreamingCapable` adapter under each
  channel name and exercises the exact shared Manager wrapper used by both
  real adapters.
- The non-streaming external final-narration path was not changed or given new
  runtime coverage. Source verification found it does not share this specific
  leak: delegated turns keep `SendResponse=false`, so the child's buffered
  final narration is not published directly.
- Child tool-call previews and generic asynchronous child-tool `ForUser`
  messages on external channels are separate suspected publication gaps from
  the audit. They are not fixed or claimed fixed here.
- Webchat behavior was not changed; its existing child shadowing remains the
  reference behavior.
- Replay/persistence and the future compiled per-turn publication policy are
  outside this containment fix.
