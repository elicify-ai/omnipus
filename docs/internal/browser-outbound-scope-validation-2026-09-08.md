# Browser outbound message lifetime

Status: queue behavior verified; production callback migration pending.

A queued browser message retains the original attachment or input-source context
and, when necessary, an immutable exact-state validator. Cancellation while
waiting for queue capacity must release the caller promptly. Cancellation or a
newer state after enqueue must suppress the old message at writer admission.
An invalid old health event must not replace a newer valid latest-state slot.
Neither queue admission nor writing may resolve a new attachment by viewer ID.

Use one bounded critical queue to preserve discrete-message ordering. Its
internal envelope carries encoded bytes, original context, and an optional
nonblocking current-state check. Keep the two fixed latest-state slots; retain
the envelope through dequeue so the final writer can revalidate it. Connection
keepalive and genuinely connection-wide messages may use an explicit unscoped
wrapper. This does not retract a transport write already admitted before the
source was retired.

Tests derive from the attachment lifetime and panel isolation requirements:
canceled/missing origins and invalid current state reject before enqueue;
cancellation during JSON encoding rejects; cancellation on a full queue ends
within 500ms instead of waiting for its two-second overflow timeout; real local
WebSocket delivery suppresses messages retired after enqueue while still
transmitting a subsequent valid marker. Schema/frame encoding and the real
single writer stay intact; only the transport is local loopback. Fault injections
will omit origin validation, the cancellation select, and writer revalidation.
Production callback adoption is a separate gate from helper verification.

Pre-edit impact: sendCritical has one direct caller and 21 total indexed
dependents (tool LOW); sendCriticalGen has 12 direct and 22 total (tool MEDIUM).
The shared browser message path is conservatively treated as high risk under
the impact skill's breadth threshold. Connection type, writer and ping pump each
have one indexed caller in the browser ServeHTTP process. The newer latest-state
helpers are absent from the graph (UNKNOWN); exact source review identified
the writer and latest-state tests. The queue representation migration requires
mechanical fixture updates, while existing payload assertions must be retained.

Run 42007 failed all nine intended leaves against legacy queue behavior
(terminal exit 1, 4.574 seconds), with no setup errors. The real WebSocket tests
received retired critical messages and a stale latest-state message after the
valid marker. The full-queue case never reached an original-context cancellation
wait; canceled encoding and invalid origins entered the queue. The queue now
retains a source/validator envelope, waits on original cancellation, and
revalidates immediately before the single writer starts transport I/O. Latest
state keeps the same envelope through dequeue. Green/mutation verification and
production callback adoption remain pending.

Independent bounded review found a post-validator cancellation gap: an exact
state validator may wait while the original source or connection retires. Run
6920 reproduced both cases (terminal exit 1, 3.575 seconds); the state remains
valid but must not revive its original sender. The common admission helper now
checks source/connection liveness both before and after that validator. A
controlled encoding test also proves that an old callback cannot overwrite a
newer recovery while its message is being encoded. The dedicated inner-check
mutation remains pending. GitNexus canSendFrame query returned UNKNOWN; its new
callers are the critical queue, latest slots, writer, and focused tests.

Run 73687 passed the expanded scoped-send/latest-state/write-deadline/attachment/
factory/registry selection (terminal exit 0, 26.062 seconds). This run includes
the added factory token registration/cleanup assertions. Nine fault injections
and restored shuffled race verification remain pending. The 26.062 seconds is
test execution time, not a UI latency measurement.

Driver 78672 completed with terminal exit 0. All nine deliberate faults failed
at their intended assertions: lost original encoding scope, missing origin
accepted, full-queue cancellation ignored, superseded state accepted, final
writer validation omitted, post-validator source recheck omitted, latest-slot
recheck omitted, factory registration omitted, and factory cleanup omitted.
Every mutation was restored before continuing. The final shuffled race run
passed 53 tests/subtests, with zero failures, zero skips, and no race warnings,
in 27.502 seconds. This is focused gateway evidence, not a full gateway suite
or browser latency result. The exact initial selection had already passed in
73687; the mutation driver did not repeat that unchanged initial run.

The bounded independent review's P1 cancellation defect and P2 verification gap
are addressed and covered by explicit fault checks. Source-scoped callback
adoption, full health publication, live target/viewport migration, independent
release-base review, and final browser/CI acceptance remain separate work.
