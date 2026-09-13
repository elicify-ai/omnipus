# Browser input candidate — 2026-09-13

The separate input connection is available for manual testing in Amsterdam. Current source `8f5c39ac578c459f9635952dac1e6794ddb1a63e` adds explicit keyboard focus for Retry on top of `eb3206c03`, which added escaped diagnostic fields to the previously tested `ef76f7536` runtime, which includes the startup/control correction, detection of definite media-receiver failure, and explicit held-input release over the healthy control socket when the input connection fails. All six alternating interaction runs passed on that earlier deployment; the final follow-up dedicated interaction smoke also passed (54.1 seconds). An intermittent input-channel setup timeout remains unresolved; a passing rerun does not erase it. A general speed improvement is not established, and WebSocket remains the default.

- New mode: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=dedicated
- Comparison: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=websocket
- Select **Browser UAT Test**. Use one browser panel at a time. WebSocket remains the default.

The implementation uses one data-only WebRTC connection with reliable action and lossy latest-position hover channels, alongside the existing media connection with audio/video tracks. A local negotiation attempt does not become the control identity until its offer is sent. Server publication waits for admitted controls to finish; replacement cleanup does not cancel the new offer. Input failure now requests release through the existing control socket, waits for a request-specific acknowledgment, and preserves the failed state and explicit Retry. A generic transport failure is not treated as acknowledgment that held input was released.

## Earlier deployment — eb3206c03

Installed and running binary SHA256 both match `dd7dd4b048ab469f34acc31bcc906bf0fe3d94a679f628228b834b1979151ecf`. Machine configuration was preserved and health returned 200. Proof: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-eb3206c03.json`. Image: `registry.fly.io/uat-omnipus:browser-input-eb3206c03@sha256:fdda6e2c9c1c474f06064d3d32eda5c5ed4f3c56f3e841ec8906d5c4d50ec34d`.

The follow-up dedicated live smoke passed exact text/click/scroll/drag, tab return, resize, held-key release after input loss, explicit Retry and post-recovery click (54.1 seconds). Fixture registration used the configured test agent after restart and exact served bytes were verified. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/smoke-eb3206c03/`.

## Verification on ef76f7536

- Independent implementation review finding MAJ-001: corrected and rechecked, with no further concrete defect found in the bounded recheck.
- Frontend: 170 focused transport/composition tests, TypeScript and production build passed.
- Gateway: dedicated installer tests failed before implementation; focused dedicated gateway race run passed afterward.
- Final gateway race checks passed in 11.044 seconds; final production UI build passed in 36.44 seconds. Three isolated refusal-response faults were detected: missing established-peer acknowledgment, relabeling an older request with the latest control identity, and echoing an unchecked counter. Fault artifacts were removed; logs remain under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/refusal-*.log`.
- Queue: 36 added boundary cases; focused race suite passed; three isolated faults caught.
- Linux binary: built from the committed source and rebuilt UI; installed and running executable SHA256 both verified as `4ac06ccdcf4f30432612ddaf3f792b1b84f61846b3948d5d3888ac7da209417f`.
- Machine `784041ef9ed398`, region `ams`: environment, services, volumes, resources and restart configuration preserved. Health endpoint returned 200.
- Image: `registry.fly.io/uat-omnipus:browser-input-ef76f7536@sha256:795e89d7ad81ab0052e102db7965bdb546f210e7df39294b2923ae0de17767ba`.
- Static fixture registration restored through the configured test agent after restart; served bytes verified exactly.
- Current alternating live comparison: six runs, each terminal exit 0, in WebSocket/dedicated, dedicated/WebSocket, WebSocket/dedicated order. All exact interaction and recovery checkpoints passed, including held-key release before Retry.

Current comparison: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/paired-runner/results-2026-09-13T11-48-57.940Z/summary.json`.
Earlier deployment proof: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-ef76f7536.json`.

## Current live evidence and remaining limits

There are 30 measured clicks per mode. Median feedback was 646.5 ms for WebSocket and 650.8 ms for dedicated input; the descriptive 95th percentile was 1143.45 ms and 756.51 ms respectively. These measurements include viewer readiness checks, runner work and video-pixel sampling. The lower dedicated tail in this small sample does not establish a general speed win, especially with nearly equal pooled medians and variation across runs. That remote comparison is not the controlled 100-click gate. A separate Linux-hosted controlled run on this same verified runtime subsequently passed the unchanged 200 ms target, as recorded below.

The current recovery run passed media-only closure and control before offer publication, but failed the held-answer case at the ten-second channel setup deadline. An instrumented rerun passed in 33.9 seconds; three further repetitions all passed in 33.9, 30.7 and 33.2 seconds. These four passes had no intervening production change explaining the original failure. The intermittent timeout remains unresolved. A separate intentional setup-timeout test passed live in 55.5 seconds, verifying visible failure, Retry, unchanged media and an exact fresh click. This establishes bounded failure handling, not the cause of the earlier intermittent timeout. Its evidence is at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/timeout-retry-ef76f7536/`. Retained evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/recovery-ef76f7536/`, `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/answer-timing-ef76f7536/`, and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/answer-repeat-ef76f7536/`.

The dedicated wrong-capture test passed in 26.2 seconds. It changes only the capture identity on a real UI click, then sends a valid click through the same ordered reliable channel and control epoch. The final exact video counters show one click, one down and one up. That later accepted click is the completion witness for the earlier rejected frames; there is no fabricated rejection acknowledgment or inference from a brief unchanged picture. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/authority-ef76f7536/`.

Both current real-page scrolling checks passed on the dated [W3C WebRTC document](https://www.w3.org/TR/2025/REC-webrtc-20250313/). Reviewed streamed screenshots show the document before scrolling, after scrolling down, and after returning. WebSocket down/return observations were 2126.75/987.92 ms; dedicated observations were 665.89/820.40 ms. These are one case per mode, not statistical performance evidence or exact remote-offset measurements. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/scroll-ef76f7536/`.

Consolidated continuous-integration checks and the intermittent setup timeout remain open. Audible audio and long-duration stability are not established by these short checks. No full CI was run per fix. The installed Mac instance on port 10994 and main were not modified.

## Controlled Linux speed gate

A separate Linux-hosted viewer ran the unchanged `runBrowserInputProbe(..., 'latency')` oracle: 100 clicks, 300 exact ordered events, a 50-second input window, and decoded-video p95 <=200 ms. The normal audio/video path remained enabled. A standalone wrapper changed only initial navigation to select dedicated input and added real route/peer checks; it did not change the event or timing oracle.

The run passed with **p95 182.6 ms**, all 300 events, no held input and no viewer errors. All 100 down/up pairs used `input-reliable`; the same data-only peer retained both open input channels. This establishes one controlled Linux speed pass on `ef76f7536`, not a long-duration result or Mac-to-Amsterdam network latency. The installed/running binary hash and preserved configuration were verified again afterward.

Evidence bundle and copied-source hashes: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-controlled-linux/`. Raw measurement: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-controlled-linux/evidence-ef76f7536/evidence/controlled-dedicated-ef76f7536/controlled-latency-dedicat-c9135--event-200ms-p95-acceptance/latency-evidence.json`. Route and post-run runtime proof are retained alongside it.

## Retained earlier failures and checkpoints

On `e15b9caef`, the dedicated live smoke passed all 13 checkpoints in 54.4 seconds, and both deliberately stalled startup cases passed: control before offer publication (32.4 seconds) and control while answer application was held (28.2 seconds). They exercise input Retry after stable media; initial epoch-zero and server pre-install ordering have separate unit evidence. Earlier smoke evidence remains at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/smoke-e15b9caef/input-connection-dedicated-f905a-rag-and-recovery-stay-exact/input-connection-evidence.json`; deployment proof remains at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-e15b9caef.json`.

The earlier inverse recovery test failed: explicitly closing the viewer's native media peer produced no unsafe-picture or Retry indication within 45 seconds. The UI still showed “Click to drive.” This was a controlled local receiver closure, not a natural packet-loss reproduction. The correction is now deployed and that case passed on `ef76f7536`; the original failure artifacts remain at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/results-20260913T105524Z/`.

The earlier six-run comparison completed five runs; the sixth dedicated run retained ArrowLeft after forced input disconnection for longer than the unchanged five-second visual gate. The runner correctly failed rather than reporting a passing aggregate. That client relied on eventual server transport-loss detection; the current deployment instead requests explicit release over the healthy control socket. Earlier timing portions remain unsuitable for a speed-win claim because of the failed recovery and concurrent local checks/builds. Evidence is retained at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/paired-runner/results-2026-09-13T10-57-59.968Z/`; the later six passing runs are reported separately above.

Separate real-page scrolling passed in both modes on the dated [W3C WebRTC document](https://www.w3.org/TR/2025/REC-webrtc-20250313/). Streamed screenshots show the heading, later document content, and return to the heading; outgoing wheel totals were +1800/-1800 on each intended route. This is visual scroll correctness, not exact remote-offset or latency evidence. The initial Omnipus landing-page target was unsuitable: first its hash route was missing, then its global fixed-body/hidden-overflow layout prevented document scrolling. Those failed harness attempts are retained; no unrelated landing-page source was changed.

Earlier real-page proof remains at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/scroll-doc-e15b9caef/`. These historical checkpoints do not replace the current deployment evidence or close the remaining intermittent setup failure.


## Final deployed follow-up — eb3206c03

The verified Amsterdam runtime is `eb3206c035eede6f6e4f90196a6dd7049336f63b`. Its dedicated interaction/recovery smoke passed (54.1 seconds). The unchanged controlled Linux 100-click/300-event gate passed again at p95 **152.7 ms**, below 200 ms, with all exact events, no held input or viewer errors, all down/up pairs on `input-reliable`, and the same connected data-only peer with both channels open. Installed and running binary hashes were reverified after the run. This is a second controlled pass on a later revision, not a matched speed comparison or long-duration result. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-controlled-linux/evidence-eb3206c03/`.

CI follow-up source `bcc192e78` adds only the reviewed security-test configuration and documentation after the runtime build. The reproduced temporary-directory cleanup failure was isolated to live model-limit discovery; 20 repetitions and the full security package passed after the configuration fix, with final lint and independent review clear. Combined CI is running. No additional full CI per individual correction was requested.

A bounded specification/evidence audit found the automated R1–R6 experiment gates covered. A human trial of this latest separate-input candidate is still pending; automated results do not substitute for that qualitative feedback. The intermittent setup timeout remains an unexplained observed risk, while explicit timeout/Retry handling has direct live proof. Long-duration and audible-content acceptance remain unproven release-readiness limits.


## Startup-delay attribution

Four instrumented successful held-answer runs on `ef76f7536` separate the deliberate test hold (1.44–1.89 seconds), native answer application after release (1.0–1.2 milliseconds), answer completion to ICE connectivity (0.69–0.94 seconds), ICE connectivity to secure peer connection (3.12–4.97 seconds), and subsequent channel opening (0.65–1.76 seconds). The largest observed segment is native secure-connection establishment. These timestamps are consistent with handshake/network startup but do not identify retransmissions or prove the original failure had the same cause. The original failure's trace bounds indicate a shorter deliberate hold than the successful repetitions. No timeout increase or speculative production correction follows from these observations. A test-only follow-up will sample native transport state, selected-path round-trip time and browser callback delay to distinguish these possibilities.


The follow-up observation on `eb3206c03` passed the unchanged held-answer recovery case in 26.7 seconds. The replacement input peer applied its native answer in 1.0 ms after a deliberate 1.913-second hold; ICE connectivity followed in 361.9 ms, secure peer connection in another 3104.2 ms, and channels opened 691.5 ms later. Native statistics explicitly remained `dtlsState=connecting` during the long segment. Twenty-nine input-peer samples reported maximum event-loop lag 3.4 ms, no statistics errors, and selected UDP path RTT 337–404 ms. This rules out a multi-second delayed browser callback in this observed run; it does not identify handshake retransmissions or prove the earlier failed run's cause. Instrumentation changes no assertions, faults, timeouts or production behavior and passed focused lint/type checking and independent review.

Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/startup-stats-registered-eb3206c03/`. The initial attempt stopped before browser startup because the temporary fixture returned HTTP404; its retained artifacts are in the sibling `startup-stats-eb3206c03` directory. The configured test agent restored registration and exact bytes before the observed run.


## Final keyboard-access correction — 8f5c39ac5

Amsterdam now runs `8f5c39ac578c459f9635952dac1e6794ddb1a63e`, with installed/running SHA256 `aa88493c41c1452dba97af0e9ef038feead72e47afc8f1f62b732572798492ee` verified and machine configuration preserved. Image: `registry.fly.io/uat-omnipus:browser-input-8f5c39ac5@sha256:b917b6b185f3e2a259142e337ee1ea8a54006deb7716ac6e6b27f39e3f228d30`. Proof: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-8f5c39ac5.json`. The only production change since the controlled 152.7 ms measurement is explicit `tabIndex={0}` on Retry; those performance measurements remain tied to `eb3206c03` and are not relabeled.

The enhanced existing recovery smoke failed on `eb3206c03` precisely because Retry lacked the explicit attribute, after the interaction and held-release checkpoints passed. It now also focuses Retry and activates it with Enter, retaining the exact post-recovery click check. This verifies keyboard activation, not a complete Tab-traversal sequence. Independent review found no concrete flaw. The final live smoke passed in 52.7 seconds, including held release, Enter activation and exact post-recovery click. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/keyboard-green-8f5c39ac5/`; the old-runtime failure is retained in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/keyboard-red-eb3206c03/`.
