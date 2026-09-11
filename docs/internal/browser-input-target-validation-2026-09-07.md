# Input target admission validation

Status: target admission implemented and focused verification complete; release
acceptance remains outstanding.

## Contract and scope

The browser reliability requirement binds an ordinary input to the page shown
in its committed capture generation. A correct capture ID and generation must
not authorize dispatch to another tab or an obsolete context. This requirement
holds for both shared and independent chat-panel views.

The test boundary keeps the real manager, capture frame tracker, input queue,
and admission code. Only the browser command transport is replaced. A matching
target must deliver exactly one text command. A different target, missing
target ID, removed tab set, or stale live-view context must deliver zero commands
and return the specific benign displayed-target-change error.

Mutation checks removed the target-ID comparison, context comparison, and
missing-tab-set rejection independently. These tests do not prove cross-panel
capture allocation, viewport transitions, nil-capture admission, or live-browser
input latency. Those remain separate implementation and acceptance work.

GitNexus cannot resolve the newly added `dispatchInputContext` symbol; its graph
risk is UNKNOWN. Manual scope covers registry/direct input dispatch and both
transport adapters. The check belongs under existing manager and input admission
so tab changes cannot interleave between the lookup and command dispatch.

## Evidence

Expectations derive from FR-009 / B09 in
[the browser improvement specification](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/plan/browser-improvements/spec.md)
and the picture-to-input contract. The original run (39081, exit 1) reproduced
all four rejected-target cases as actual command delivery with no error; the
matching-target positive case passed. The fix checks one coherent capture
snapshot against the current manager target and the live view's exact context.
Three older capture tests now include matching manager tab entries; their
generation and held-release assertions remain intact.

Driver 58424 exited 0. The focused `TestLiveInput*` and
`TestLiveView_DispatchInput*` selection initially passed in 2.211 seconds. Each
of the three mutants failed its intended command-delivery assertion. Restored
tests passed in 3.337 seconds; the same restored selection with race detection
and shuffled execution passed in 4.198 seconds. This is focused package
verification, not the full package suite or live-browser acceptance.

A separate agent reviewed the intended target guard, lock ordering, held-release
exception, and test oracles without finding a concrete defect. This bounded
review does not replace the mandatory independent review of the full release
diff. Nil-capture admission and cross-panel capture allocation remain open.

The required staged GitNexus check (13693, exit 0) reported no indexed changes,
consistent with its inability to resolve this new input helper. Manual staged
inspection covers the four intended files; this graph result is not evidence
that the change has no behavioral impact.
