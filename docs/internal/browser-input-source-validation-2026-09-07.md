# Browser input source and picture admission validation

Integration branch: `browser-improvements`. Main and the installed production process were not modified.

## Requirements and regressions

Input queued for a picture must be checked after waiting for command serialization. Missing, replaced, uncommitted, and stopped capture identities must not dispatch ordinary interaction. A release for an already-owned key remains permitted during a picture transition.

Held inputs belong to the original connection or input-channel lifetime, in addition to their viewer and browser target. Disconnect must release its holds without waiting for another input. Two active sources using the same viewer must not release each other's holds. A replacement must flush a canceled source before accepting a new press, and source cancellation must interrupt its in-flight command.

The first focused run exited 1 and reproduced seven capture-claim violations and five source-ownership/cancellation failures. The owned-release positive control had an incorrect test constant (`keyDown` versus the intended `rawKeyDown` protocol event); that test expectation was corrected and is not counted as a product defect. The replacement test still independently failed its required four-event sequence before implementation.

## Current evidence

After implementation, the complete selected `TestLiveInput*` and `TestLiveView_DispatchInput*` regressions passed with exit 0 in 2.540 seconds. The input path now also joins the manager's tab-operation gate before its own input gate, retaining the original caller/interactive deadline while waiting.

Five deliberate faults were caught: bypassing picture admission (seven failures), collapsing source ownership (three failures), removing disconnect cleanup (one failure), ignoring in-flight source cancellation (one failure), and bypassing the tab-operation gate (one failure). Production source was restored after every mutation. The final restored focused suite, including the gate-budget regression, passed with exit 0 in 3.735 seconds. The same selection passed with race detection and shuffled test order in 2.825 seconds; the complete driver terminated with exit 0.

The WebSocket adapter supplies the original attachment context. The relay exposes its original input-channel context, but the production data-channel adapter still needs to pass it into this live-input path. Initial capture preparation, complete target/viewport transition serialization, nil-capture admission, and end-to-end frame authorization remain integration work; these focused tests are not release acceptance evidence.

## Scope checks

GitNexus reports LOW impact for `LiveInput` (one direct gateway caller, five total indexed dependents). The newly introduced input-context helpers and source state are absent from the current index, so their risk is UNKNOWN in the graph. Manual callsite review covers registry/direct dispatch, tracked presses/releases, pending cleanup, and the WebSocket adapter. The shared lock order is manager tab-operation gate, then live-input gate; cleanup releases act on their original target.
