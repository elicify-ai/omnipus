# Independent implementation review — 2026-09-13

Reviewed runtime range `018723f5a..f092b634d`, with documentation/test follow-up at `a1690530c`. Read-only reviewer inspected actual implementation and tests. Verdict: **REVISE**. One high-confidence major runtime finding; no other high-confidence runtime defect reported.

## MAJ-001 — Control can refer to an unpublished input offer

`BrowserInputWebRTCSession.start()` increments the local input epoch before offer creation/network discovery completes. `beginControl()` can then cancel that attempt and send a navigation/viewport control carrying an epoch the server has never seen. The server correctly rejects it using its current epoch; the client ignores the acknowledgment because it expects its newer local epoch. Input remains paused until the 30-second timeout.

Evidence: actual TypeScript implementation executed by a read-only Node probe with deferred offer creation returned `offersSent:0`, control identity `{input_epoch:1,control_epoch:1}`, `serverFailureAckAccepted:false`, state `paused`, and `awaitingControl:true`.

Locations: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserInputWebRTC.ts:115` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/gateway/browser_dedicated_input.go:250` in the reviewed source.

Required correction: distinguish local attempts from server-visible ownership or serialize publication correctly; retain strict stale-identity rejection. Cover controls before offer creation, during network discovery, and after publication while the answer is pending. Verify canceled attempts cannot later publish or poison replacements.

Correction implemented: frontend controls now use the last published input identity; an unsent attempt is retired, while a published offer survives controls during its answer. The server waits until admitted controls finish before installing a replacement, and joins a retired predecessor without canceling the new offer. The original offer identity remains the answer identity; stale-offer rejection is retained.

Focused verification: the new frontend cases first failed, then the 170 transport/composition tests passed. TypeScript and the production UI build passed. The gateway installer tests failed against the initial stub; the integrated `^TestDedicated` gateway race run subsequently passed (9.989 seconds test execution). This includes direct admission, installer ordering, retired-peer control and existing dedicated lifecycle tests. Deployed validation is still pending; this document does not establish that the correction is already deployed.

Queue boundary verification: 36 added cases cover stale/lost hover, safe-integer boundaries, invalid counters and saturation with a held button source. The focused dedicated-input Go race suite passed. Three isolated faults (stale hover acceptance, counter overflow acceptance and missing held-source cancellation) were all detected; production code was not mutated.

The independent reviewer rechecked only MAJ-001 and its correction: addressed in inspected code, with no additional concrete defect or lock cycle found. This bounded check did not execute tests or attest deployment.

## Proof gaps to close or retain explicitly

- Direct dedicated-offer admission tests for mismatched session/agent and replacement during negotiation.
- Combined held-input retirement and gateway control transition, beyond the existing separate queue/drain/handler tests.
- Explicit queue boundary datasets requested by the spec, including sequence overflow and held-state saturation.
- Media-only recovery preserving the input peer, the inverse of the already-passed input-only recovery smoke.
- Final repeated paired timing evidence; early correctness handoff alone does not establish a speed gain.

Earlier evidence remains valid within scope: the two-channel connection and short live correctness/recovery smoke passed on deployed `f092b634d`; the review demonstrates that these tests do not cover every startup interleaving. The default remains WebSocket. No main, installed Mac, tool-policy or memory-threshold changes are part of this correction.

## Live follow-up — media receiver closure

The deployed correction `e15b9caef` passed the 13-checkpoint dedicated smoke and both controlled negotiation interleavings. The inverse recovery test exposed a separate gap: native receiver `close()` can change connection state without the ICE event used by the session. After explicit receiver closure, no unsafe-picture/recovery indication appeared within 45 seconds. This does not reproduce natural packet loss.

The prepared correction checks only definite receiver failure (closed/failed connection or ended video track), using existing media-only recovery. It does not infer failure from a static picture. The independent bounded review found no concrete defect: cleanup clears the check, old-peer callbacks are fenced, and input connection/socket ownership is preserved. The 250 ms interval is not a guaranteed detection deadline in throttled background tabs. Focused media unit tests passed; updated deployment and inverse live proof remain pending.
