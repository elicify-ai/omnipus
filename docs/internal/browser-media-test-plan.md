# Media regression test plan

Specification: browser corrective analysis, 7 September 2026, requirements 1, 2, and 5. Tests exercise the real embedded script in Node and actual Go forwarding state; browser APIs and clocks are process-edge fakes.

- Geometry: every requested dimension is positive and divisible by 12, yielding even output at scales 1, 1.5, and 2; output stays within 921600 pixels. Cover 899×456, 633×741, 632×906, budget boundaries, portrait/landscape, scales 1/1.25/1.5/2/3, tiny, negative, and nonfinite inputs. Invalid dimensions reject explicitly.
- Adaptation: absent source-demand evidence or an idle source never establishes CPU pressure; sustained demand exceeding encoding progress plus CPU limitation can reduce resolution; stale pressure restores quality in bounded steps. Missing and stale statistics remain unknown.
- RTP continuity: first packet after replacement follows the outgoing high-water mark without invented loss; true gaps and reorder are preserved including wraparound. Superseded source packets and sender reports cannot write after new ownership commits. Timestamp translation must apply the identical offset to sender reports.
- Recapture: preserve a connected ingest connection using replaceTrack only after real Chrome compatibility experiment; failed replacement recovers through explicit renegotiation. Negotiation and stale completion tests cover close races.

Mutation proof: remove alignment; remove pixel clamp; accept stale CPU source evidence; restore sequence gap; remove feed ownership guard; omit timestamp offset from sender report. These mutations must fail focused tests and then be reverted.

Runtime gaps: native encoder adaptation parity, tabCapture replacement behavior, actual decoded continuity/audio synchronization require browser testing, not pure-function claims.

## Current implementation evidence

Worktree: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/media`, branch `browser-improvements-media`.

The embedded module now aligns all requested dimensions to the hardware grid, limits the pixel budget even for extremely thin portrait/landscape input, derives source demand from fresh frame-counter deltas, and retains the original timestamp in health samples. Ordinary capture replacement stops the old source and replaces both negotiated media tracks while retaining the connection. Shutdown invalidates pending capture work. Relay forwarding has one owner per media kind, excludes retired packets/reports, removes artificial reconnect loss, and applies matching timestamp offsets to RTP and sender reports.

Observed red before implementation: odd initial dimensions, idle CPU-label downscale, unconditional peer teardown on recapture, artificial sequence gap. Additional edge reproduction exposed zero width for an extremely thin portrait input; fixed with an absolute dimension bound.

Mutation evidence: seven JavaScript faults were detected (hardware grid, uniform budget scaling, stale CPU evidence, unconditional peer teardown, falsified health freshness, capture resurrection after shutdown, stale source-rate label). Three Go faults were detected (artificial reconnect loss, acceptance of retired RTP, missing sender-report timestamp translation). The initial budget mutation survived the pixel-count-only assertion because the secondary clamp still enforced area while distorting aspect; adding the independent uniform-scaling oracle killed it. All mutations were reverted.

Related Go packages passed before the final thin-input and ended-feed cleanup: capture extension 13.043 seconds; WebRTC 82.204 seconds. Focused continuity tests passed in 4.719 seconds. Final checks are recorded separately by the integration gate; these elapsed times are test execution, not browser performance.

Real browser experiment on this Mac used Chrome for Testing 151.0.7922.77 with an isolated profile. Concurrent same-tab tabCapture was explicitly rejected; stop-old-source then replaceTrack succeeded. Running the actual modified `runCaptureAndOfferOnce` retained the same connected peer and VideoToolbox H.264 encoder, changed output from 888×456 to 624×732, then application scale 1.5 produced 416×488 on VideoToolbox. The video receiver decoded the replacement dimensions with zero packet loss. Opus packet/sample counters also advanced with zero packet loss, but measured audio energy remained zero in the fixture: audible output and synchronization are not verified.

Limits: native `balanced` adaptation under sustained overload is not proven to preserve even dimensions by the application grid alone. Production first-picture/input latency and long idle/motion soak belong to the integrated browser test. Capture health generation is an attempt identifier and does not identify a buffered viewer frame. The next generation-correlation wave must fence target/geometry changes with an immutable offer generation and relay RTP boundary before claiming stale-picture input is solved.

GitNexus: the fresh root index was still processing during implementation. Fallback index impacts were LOW for encoder policy, sampling, capture replacement, teardown, relay offer/forwarding and sender reports. Session was HIGH (three affected symbols, one direct dependency across browser/gateway/media); parent was informed. `budgetedCaptureDims`, `clearIngestIfCurrent`, and `endFeed` were missing from that old graph; current-source callers were inspected and the missing graph coverage explicitly reported. No zero-result query was interpreted as proof of safety.

A follow-up real Chrome static-page probe stopped its animation after seven seconds. Over a later ten-second sample window, source frames advanced 57→183 and encoded frames 49→176, with CPU limitation `none`. The requested minimum capture frame rate therefore does produce source progress on visually static content. The current policy correctly remained steady in this healthy case, but source-counter progress alone is not proof that pixels changed; fresh encode-cost evidence is still needed for the stale-CPU-label/low-output corner case.

Final wave-one validation, 7 September 2026: full capture-extension package collected/passed 25 tests, zero skips/failures (12.484 seconds); focused relay sequence/ownership/timestamp/ended-feed checks collected/passed eight tests, zero skips/failures (3.846 seconds). Both commands exited zero against the final source. The full relay package had already passed before the narrow ended-feed retirement addition. Race-detector and integrated gateway/browser verification remain integration gates.
