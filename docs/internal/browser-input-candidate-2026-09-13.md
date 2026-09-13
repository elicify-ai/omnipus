# Browser input candidate — 2026-09-13

The separate input connection is available for manual testing in Amsterdam. Source `e15b9caefcc506c2d2a78eae74b464f29c5c1a15` corrects the reviewed startup/control race. The dedicated live smoke passed all 13 checkpoints on this exact deployment, including Unicode, clicks, native scrolling, dragging, tab return, resize, held-key release and Retry without replacing media. This is a correctness checkpoint; a speed improvement is not established.

- New mode: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=dedicated
- Comparison: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=websocket
- Select **Browser UAT Test**. Use one browser panel at a time. WebSocket remains the default.

The implementation uses one data-only WebRTC connection with reliable action and lossy latest-position hover channels, alongside the existing media connection with audio/video tracks. A local negotiation attempt does not become the control identity until its offer is sent. Server publication waits for admitted controls to finish; replacement cleanup does not cancel the new offer.

## Verification

- Independent implementation review finding MAJ-001: corrected and rechecked, with no further concrete defect found in the bounded recheck.
- Frontend: 170 focused transport/composition tests, TypeScript and production build passed.
- Gateway: dedicated installer tests failed before implementation; focused dedicated gateway race run passed afterward.
- Queue: 36 added boundary cases; focused race suite passed; three isolated faults caught.
- Linux binary: built from the committed source and rebuilt UI; installed and running executable SHA256 both verified as `4485c1961ecd41dca2bfd1f6bb42e957c0b6cbba0409957d8c34039701ab269c`.
- Machine `784041ef9ed398`, region `ams`: environment, services, volumes, resources and restart configuration preserved. Health endpoint returned 200.
- Image: `registry.fly.io/uat-omnipus:browser-input-e15b9caef@sha256:72117faa0f04f7cbdeaf108c97e1510b0b5bf145283b50b00945acc32e1e5660`.
- Static fixture registration restored through the configured test agent after restart; served bytes verified exactly.
- Dedicated live smoke: exit 0, 54.4 seconds test execution, 13 checkpoints.

Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/smoke-e15b9caef/input-connection-dedicated-f905a-rag-and-recovery-stay-exact/input-connection-evidence.json`.
Deployment proof: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/verified-provenance-e15b9caef.json`.

## Pending evidence

Both deliberately stalled startup cases passed live: control before offer publication (32.4 seconds) and control while answer application was held (28.2 seconds). They exercise input Retry after stable media; initial epoch-zero and server pre-install ordering have separate unit evidence.

The inverse recovery test failed: explicitly closing the viewer's native media peer produced no unsafe-picture or Retry indication within 45 seconds. The UI still showed “Click to drive.” This is a controlled local receiver closure, not a natural packet-loss reproduction. A bounded receiver-liveness correction is in progress; do not claim inverse recovery passes. Original failure artifacts: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/results-20260913T105524Z/`.

Six alternating runs used WS/dedicated, dedicated/WS, WS/dedicated. Five completed; the sixth dedicated run retained ArrowLeft after forced input disconnection for longer than the unchanged five-second visual gate. The comparison runner correctly failed rather than reporting a passing aggregate. Client failure currently relies on eventual server transport-loss detection; explicit release over the healthy control socket is being added. The timing portions are retained, but concurrent local checks/builds and the failed recovery make this unsuitable for a speed-win claim. Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/paired-runner/results-2026-09-13T10-57-59.968Z/`. Repeat the final comparison with local builds/tests stopped.

Separate real-page scrolling passed in both modes on the dated [W3C WebRTC document](https://www.w3.org/TR/2025/REC-webrtc-20250313/). Streamed screenshots show the heading, later document content, and return to the heading; outgoing wheel totals were +1800/-1800 on each intended route. This is visual scroll correctness, not exact remote-offset or latency evidence. The initial Omnipus landing-page target was unsuitable: first its hash route was missing, then its global fixed-body/hidden-overflow layout prevented document scrolling. Those failed harness attempts are retained; no unrelated landing-page source was changed.

Real-page proof: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/scroll-doc-e15b9caef/`. Final receiver/held-key recovery and a fully passing paired comparison remain pending. Audible audio and long-duration stability are not established by the short smoke. No full CI was run per fix. The installed Mac instance on port 10994 and main were not modified.
