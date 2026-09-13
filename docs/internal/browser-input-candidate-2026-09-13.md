# Browser input candidate — 2026-09-13

The separate input connection is available for manual testing in Amsterdam. Current source `ef76f753607dc1410f353eef211bf055895d5a41` includes the startup/control correction, detection of definite media-receiver failure, and explicit held-input release over the healthy control socket when the input connection fails. All six alternating interaction runs passed on this deployment. An intermittent input-channel setup timeout remains unresolved; a passing rerun does not erase it. A general speed improvement is not established, and WebSocket remains the default.

- New mode: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=dedicated
- Comparison: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=websocket
- Select **Browser UAT Test**. Use one browser panel at a time. WebSocket remains the default.

The implementation uses one data-only WebRTC connection with reliable action and lossy latest-position hover channels, alongside the existing media connection with audio/video tracks. A local negotiation attempt does not become the control identity until its offer is sent. Server publication waits for admitted controls to finish; replacement cleanup does not cancel the new offer. Input failure now requests release through the existing control socket, waits for a request-specific acknowledgment, and preserves the failed state and explicit Retry. A generic transport failure is not treated as acknowledgment that held input was released.

## Verification

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
Current deployment proof: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-ef76f7536.json`.

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
