# Recapture admission validation

Requirement: browser replacement, tab changes and recovery cancellation must not let queued work operate on a newer picture. A recapture carries one measured capture/frame/target/geometry tuple and the original socket and request lifetimes through final transport admission.

The capture session and frame transitions remain real. A controlled outbound transport callback models a busy socket, allowing the test to retire the frame, binding, recovery request or capture before admitting the command. The expected result is rejection and zero accepted commands. A current command must preserve the exact original measured tuple. Pending zero geometry cannot authorize a recapture.

The gateway writer tests separately use actual WebSocket pairs: canceled work must leave writer admission promptly; encoding or admission delay cannot revive superseded work; a subsequent marker must be the first transmitted message. The actual Stop regression requires pending and active viewer-request listeners to be retired exactly once.

Run only these focused regressions and the affected binding tests during implementation. Reuse observed pre-fix failures and a minimal final-admission fault check; full CI belongs to the final integrated branch. Runtime responsiveness and encoder replacement remain unverified until the real browser test.

Current evidence: the initial actual Stop regression failed with both listeners retained. The initial gateway writer selection failed for canceled/superseded work, blocked cancellation, stale encoding completion and stale admission state. The final focused closeout passed; details follow below.

Focused run7292 exited1: the actual Stop hook passed and the current measured recapture control passed. Four retirement cases (frame, binding, request, capture) and pending zero geometry all failed behaviorally against the callable old transport bridge. The recapture implementation now retains the original binding/frame/request and invokes a typed sender; its final focused race run passed. The actual authenticated gateway pending-geometry socket regression also passed. No end-to-end claim follows from these component results.

Review requirement: ordinary recapture must retain the binding from its first admission lock; each coalesced ordinary tab pass needs its own fresh receipt baseline. Explicit same-frame recapture supersedes an active automatic episode: cancel the old context/timer, preserve attempt/gave-up accounting and the current failure event, establish one fresh baseline, and monitor the explicit request through a new bounded continuation when budget remains. Automatic recapture preserves its original context, timer and loss baseline. The new two-case regression contrasts those behaviors without waiting for real recovery delays.


Final closeout (process 83155, exit 0): 46 focused leaf cases passed with race detection enabled, zero failures, skips or race warnings. Three deliberate implementation faults were independently caught: admitting pending geometry, retaining an automatic recovery episode after explicit refresh, and removing final socket-writer admission checks. All mutations were restored before the final run. This includes actual frame transition hooks, typed recapture retirement, ordinary entry points, existing binding compatibility, authenticated ingest socket behavior and cancelable writer admission. It is component and socket evidence, not an end-to-end browser acceptance result.

Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/recapture-closeout/restored-race.jsonl`; driver: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-recapture-closeout.py`.
