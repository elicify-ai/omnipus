# Independent implementation review — 2026-09-13

Reviewed runtime range `018723f5a..f092b634d`, with documentation/test follow-up at `a1690530c`. Read-only reviewer inspected actual implementation and tests. Verdict: **REVISE**. One high-confidence major runtime finding; no other high-confidence runtime defect reported.

## MAJ-001 — Control can refer to an unpublished input offer

`BrowserInputWebRTCSession.start()` increments the local input epoch before offer creation/network discovery completes. `beginControl()` can then cancel that attempt and send a navigation/viewport control carrying an epoch the server has never seen. The server correctly rejects it using its current epoch; the client ignores the acknowledgment because it expects its newer local epoch. Input remains paused until the 30-second timeout.

Evidence: actual TypeScript implementation executed by a read-only Node probe with deferred offer creation returned `offersSent:0`, control identity `{input_epoch:1,control_epoch:1}`, `serverFailureAckAccepted:false`, state `paused`, and `awaitingControl:true`.

Locations: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserInputWebRTC.ts:115` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/gateway/browser_dedicated_input.go:250` in the reviewed source.

Required correction: distinguish local attempts from server-visible ownership or serialize publication correctly; retain strict stale-identity rejection. Cover controls before offer creation, during network discovery, and after publication while the answer is pending. Verify canceled attempts cannot later publish or poison replacements.

Correction implemented: frontend controls now use the last published input identity; an unsent attempt is retired, while a published offer survives controls during its answer. The server waits until admitted controls finish before installing a replacement, and joins a retired predecessor without canceling the new offer. The original offer identity remains the answer identity; stale-offer rejection is retained.

Focused verification: the new frontend cases first failed, then the 170 transport/composition tests passed. TypeScript and the production UI build passed. The gateway installer tests failed against the initial stub; the integrated `^TestDedicated` gateway race run subsequently passed (9.989 seconds test execution). This includes direct admission, installer ordering, retired-peer control and existing dedicated lifecycle tests. Deployment was pending at that original checkpoint; the current deployed evidence is recorded below.

Queue boundary verification: 36 added cases cover stale/lost hover, safe-integer boundaries, invalid counters and saturation with a held button source. The focused dedicated-input Go race suite passed. Three isolated faults (stale hover acceptance, counter overflow acceptance and missing held-source cancellation) were all detected; production code was not mutated.

The independent reviewer rechecked only MAJ-001 and its correction: addressed in inspected code, with no additional concrete defect or lock cycle found. This bounded check did not execute tests or attest deployment.

## Proof gaps from the original review — current disposition

- Direct dedicated-offer admission tests now cover mismatched session/agent, stale peer/offer/control identity, and expired/retired attachment. Installer tests and deliberate startup interleavings cover related replacement ordering; do not describe these as exhaustive negotiation-race proof.
- Live-source and retired-source gateway control tests now exercise cancellation/join and acknowledgment. The final paired live runs additionally passed exact held-key release before Retry. The controlled Go sink does not itself prove browser key-up; the live fixture is that oracle.
- Queue boundary datasets now cover sequence overflow and held-state saturation; three isolated faults were detected.
- Media-only recovery preserving input/socket ownership passed on the current deployment after the definite-receiver-failure correction.
- Six final alternating runs passed and supplied descriptive timing evidence. They do not establish a general speed gain or satisfy the separate 100-click gate.
- The held-answer setup scenario remains intermittent: one current-deployment timeout followed by four instrumented passes. Its cause remains open. A distinct intentional timeout/Retry live gate passed, establishing bounded recovery rather than explaining the intermittent failure.

Earlier evidence remains valid within scope: the two-channel connection and short live correctness/recovery smoke passed on deployed `f092b634d`; the review demonstrates that these tests do not cover every startup interleaving. The default remains WebSocket. No main, installed Mac, tool-policy or memory-threshold changes are part of this correction.

## Live follow-up — media receiver closure

The deployed correction `e15b9caef` passed the 13-checkpoint dedicated smoke and both controlled negotiation interleavings. The inverse recovery test exposed a separate gap: native receiver `close()` can change connection state without the ICE event used by the session. After explicit receiver closure, no unsafe-picture/recovery indication appeared within 45 seconds. This does not reproduce natural packet loss.

The correction checks only definite receiver failure (closed/failed connection or ended video track), using existing media-only recovery. It does not infer failure from a static picture. The independent bounded review found no concrete defect: cleanup clears the check, old-peer callbacks are fenced, and input connection/socket ownership is preserved. The 250 ms interval is not a guaranteed detection deadline in throttled background tabs. Focused media unit tests passed; the correction is now deployed in `ef76f7536`, and inverse live recovery passed. Original failure evidence remains retained.

## Current deployment and bounded follow-up

Source `ef76f753607dc1410f353eef211bf055895d5a41` is verified in Amsterdam by `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-ef76f7536.json`. It also corrects delayed held-key release after local input loss: the healthy control socket explicitly retires input, and request-specific acknowledgments distinguish completed release or actual refusal from an unrelated transport failure. Failure remains visible with explicit Retry.

Final gateway race checks passed in 11.044 seconds and the final UI build passed in 36.44 seconds. Three isolated faults against refusal acknowledgments were detected: omission for an established peer, relabeling an older request with the latest control epoch, and an unchecked echoed counter. Production was not left mutated; retained logs are `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/refusal-missing-established-ack.log`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/refusal-relabel-current-control.log`, and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/refusal-unchecked-counter.log`.

The final six alternating interaction runs all exited 0. Thirty click samples per mode gave median feedback 646.5 ms WebSocket / 650.8 ms dedicated and descriptive 95th percentiles 1143.45 / 756.51 ms. These include runner and video observation work; that comparison did not run the 100-click gate, and no general speed win is claimed. The subsequent controlled Linux gate is recorded below. Summary: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/paired-runner/results-2026-09-13T11-48-57.940Z/summary.json`. The earlier sixth-run held-key failure remains indexed separately, rather than being overwritten by this result.

Current media-closure and before-offer recovery cases passed. The held-answer case first reached the ten-second channel setup deadline, then passed one instrumented rerun and three repetitions (33.9, 33.9, 30.7 and 33.2 seconds). No intervening production change explains the original failure. Retain the timeout as unresolved. A separate intentional timeout/visible-error/Retry/same-media/exact-click live test passed in 55.5 seconds; the bounded review found no assertion flaw. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/timeout-retry-ef76f7536/`. Current wrong-capture authority proof passed in 26.2 seconds using a subsequent valid click on the same ordered channel as the completion witness. Real documentation-page scrolling passed in both modes; its single down/return measurement per mode is not statistical evidence.

Current results and retained failures are indexed in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-input-candidate-2026-09-13.md`. Consolidated continuous-integration checks, intermittent setup diagnosis, audible-content verification and long-duration stability remain outside the completed proof. The separate controlled Linux gate subsequently passed, as recorded below. No additional formal review round, main changes or installed-Mac port-10994 changes are claimed.

Controlled Linux follow-up on verified `ef76f7536`: the unchanged 100-click/300-event latency oracle passed at p95 182.6 ms against 200 ms, with dedicated route and same-peer proof. This closes the single controlled speed measurement gap for that runtime; sustained and remote-network claims remain out of scope for this result. CI follow-up corrections remain under focused review.


## Final deployed follow-up — eb3206c03

The verified Amsterdam runtime is `eb3206c035eede6f6e4f90196a6dd7049336f63b`. Its dedicated interaction/recovery smoke passed (54.1 seconds). The unchanged controlled Linux 100-click/300-event gate passed again at p95 **152.7 ms**, below 200 ms, with all exact events, no held input or viewer errors, all down/up pairs on `input-reliable`, and the same connected data-only peer with both channels open. Installed and running binary hashes were reverified after the run. This is a second controlled pass on a later revision, not a matched speed comparison or long-duration result. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-controlled-linux/evidence-eb3206c03/`.

CI follow-up source `bcc192e78` adds only the reviewed security-test configuration and documentation after the runtime build. The reproduced temporary-directory cleanup failure was isolated to live model-limit discovery; 20 repetitions and the full security package passed after the configuration fix, with final lint and independent review clear. Combined CI is running. No additional full CI per individual correction was requested.

A bounded specification/evidence audit found the automated R1–R6 experiment gates covered. A human trial of this latest separate-input candidate is still pending; automated results do not substitute for that qualitative feedback. The intermittent setup timeout remains an unexplained observed risk, while explicit timeout/Retry handling has direct live proof. Long-duration and audible-content acceptance remain unproven release-readiness limits.
