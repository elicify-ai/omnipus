# Browser streaming research — 2026-09-09

Status: shared media transport ownership fixed, focused regressions verified, and replacement captures verified on Amsterdam UAT. Tool configuration and readiness-test blockers resolved. Capture-rate experiment completed without a recommended tuning change; latency variability remains unresolved. Two online research tracks ran independently alongside the external Claude timing review.

## Measured starting point

Amsterdam UAT source `ee32c6fa6`: 100 clicks, 300 exact ordered events over 50 seconds; no uncaught viewer errors. End-to-end decoded-video p95 235.1 ms exceeds the unchanged 200 ms target. Normal audio track retained, but audible content was not tested. Received video 14–15 fps; interval-average jitter-buffer residence 46.07 ms and decoding 1.10 ms. Sample observation cost p95 5.2 ms with an 86.6 ms maximum. Two individual response spikes were 615.7 and 701.6 ms. These aggregates do not prove a single cause, clock drift, or clock-fix effectiveness across platforms.

Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-results-1-2/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-latency-2-analysis.json`.

## Relevant documented approaches

1. **Capture cadence:** [Selkies FAQ](https://github.com/selkies-project/selkies/blob/main/docs/faq.md) discusses low-frame-rate and idle/background latency. At 15 fps frames are 66.7 ms apart; at 30 fps, 33.3 ms apart. This is one component, not the entire response time. Experiment: request capture floor 30 instead of 15 on an isolated UAT comparison, retaining audio, resolution and bitrate. Measure actual capture/encode/receive cadence and CPU; requests are not guarantees and extra load may worsen results.
2. **Encoding adaptation:** [W3C content hints](https://www.w3.org/TR/mst-content-hint/#video-content-hints) distinguishes detail and motion preferences. Explicit degradationPreference overrides the hint-derived default. Current `detail` plus explicit `balanced` does not prove resolution-only priority. Separately compare maintain-framerate, recording actual dimensions, readability, encoder limitation reason and audio. Resolution reduction is a tradeoff, not an equivalent-quality win.
3. **Audio/video synchronization:** [Chromium synchronization implementation](https://webrtc.googlesource.com/src/%2B/9fe745b2dd3dbfe0fef82c12cd7d72c95962f44f/video/stream_synchronization.cc) and [Pion discussion 1825](https://github.com/pion/webrtc/discussions/1825) explain cross-track synchronization and sender-report clock mapping. Collect both tracks’ interval actual/target/minimum jitter-buffer delays and audio concealment/acceleration. Residual timing errors remain a hypothesis, not a conclusion.
4. **Avoid indiscriminate zero-buffer tuning:** [Selkies issue 157](https://github.com/selkies-project/selkies/issues/157) reports stutter from aggressive repeated zero-delay hints. [Chromium playout-delay guidance](https://webrtc.googlesource.com/src/+/refs/heads/main/docs/native-code/rtp-hdrext/playout-delay/README.md) describes best-effort delay bounds. Any experiment must verify negotiation and preserve audio, synchronization and smoothness.
5. **Stuck input:** [Selkies issue 145](https://github.com/selkies-project/selkies/issues/145) and [PR 146](https://github.com/selkies-project/selkies/pull/146) document held-key confirmation/release and its subsequent reversion for performance considerations. [Guacamole keyboard reset API](https://guacamole.apache.org/doc/guacamole-common-js/Guacamole.Keyboard.html) supports release-all behavior. Test blur/disconnect with held input, original-session ownership, queue age and transport backlog. This is separate from the current run’s latency, where all checked events arrived.
6. **Wheel fidelity:** [Selkies issue 241](https://github.com/selkies-project/selkies/issues/241) documents collapsed wheel events. Preserve accumulated movement and exact input assertions while optimizing.

## Measurement constraints

Use the [W3C statistics definitions](https://www.w3.org/TR/webrtc-stats/) to derive interval deltas. Receiver processing delay overlaps jitter-buffer residence; do not add them. A target-minus-minimum gap can support extra buffering but does not uniquely identify synchronization. Inspect the two large spikes individually before claiming progressive delay growth. Do not subtract observation costs from acceptance results, remove audio as a product fix, weaken the 200 ms threshold, or call the short run a stability acceptance pass.

## Agent-skill discovery

Actual skill contents inspected in a bounded live search:

- [Official Chrome DevTools skill](https://github.com/ChromeDevTools/chrome-devtools-mcp/blob/main/skills/chrome-devtools/SKILL.md): page targeting, background-worker inspection and bounded diagnostic artifact retrieval. Useful for capture extension and browser inspection; does not provide an end-to-end streaming latency oracle.
- [Official Playwright trace skill](https://github.com/microsoft/playwright/blob/main/packages/playwright-core/src/tools/skills/playwright-trace/SKILL.md): offline inspection of recorded actions, requests and errors. Useful for retained failure artifacts. Avoid continuous tracing in the acceptance timing run until overhead is characterized.
- Chrome DevTools also has a memory-leak debugging skill, relevant if retained resources grow during repeated start/stop or recovery, not a demonstrated cause of current latency.

No credible Pion-specific skill was found in these bounded searches; this is not a claim that none exists. Ordinary libraries, examples and documentation are not agent skills. At the initial discovery checkpoint no third-party skill was installed; subsequent official skill installations are recorded below. Existing Playwright and direct RTP/RTCP tests remain necessary.

## Applied skills and independent review

Installed official Chrome DevTools at `/Users/danielpiatkowski/.codex/skills/chrome-devtools/SKILL.md` (source commit `4a3f6fc214a0ba46d1a3e68e7683208195397219`) and Playwright trace at `/Users/danielpiatkowski/.codex/skills/playwright-trace/SKILL.md` (`4302dbb90f65e80da3f4f08a2e028c9e642b64b9`). The trace CLI was used on a retained September 8 Darwin trace: 190 actions, memory-admission refusal, no video. This is historical setup evidence, not a current speed result. Chrome DevTools extension tools require an unavailable supporting MCP server; no global server configuration changed.

Claude independently inspected source and Linux evidence. Its buffer-growth observation was recomputed from raw snapshots: video target-minus-minimum rose from 0.6 ms in the first 20-click interval to 61.5 ms in clicks 80–100, then 126.8 ms in the short final interval. Audio interval residence rose to 126.0 and 170.0 ms in those final intervals. This supports investigating synchronization/receiver recovery, but does not prove their cause or explain the two upstream stalls.

Independent review corrected two Claude findings: WebSocket-only input is intentional and specified (do not reconnect an independent input transport); replacement sequence gap is currently 1, not 64. The nonzero-offset real-wire coverage gap remains valid. Forwarded sender-report packet/octet counters deserve a separate statistics-coherence review, without a demonstrated latency consequence.

## September 9 experiment setup failures

A fresh AI-prepared control produced zero click samples: Jim reported ToolSearch denied, nevertheless included the ready marker, and no Watch live button appeared. A clarified prompt also failed during tool discovery. The served policies report ToolSearch allowed globally and effectively for Jim; the model narrative is not sufficient evidence of the underlying denial mechanism. Failure artifacts retained at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-controls-setup-failures.tgz`.

The previously served test preview remains HTTP 200 with its original fixture. A deterministic reuse path is being prepared: confine its directory to the same workspace, overwrite only this test fixture with fresh nonce/content, verify served bytes, open the real browser UI, and retain the decoded-pixel input oracle. This isolates browser timing from AI-tool setup. Capture-rate experiments must confirm loaded code via a one-shot local beacon, preserve audio/dimensions, and restore the original seeded bytes and retire experimental encoder pages afterward.

### Further experiment findings (not speed acceptance)

The deterministic preview setup reproduced viewer connection failure: browser attachment and offer/answer completed, and the gateway received encoder media, but the viewer did not display video. Normal and mDNS-disabled diagnostic runs produced zero latency samples. One mDNS-disabled video-only attempt displayed video but exposed early navigation submission before attachment, and failed the fresh-fixture pixel check. This does not prove the mDNS setting fixes connectivity. Explicit attachment and initial-video preparation barriers were added before the single navigation attempt; both subsequent controls still failed during initial video readiness.

The actual same-host route is unverified: previous successful receiver evidence did not retain selected candidate addresses. Pion rewrites advertised IPv4 host candidates to the Fly public address, leaving no advertised loopback IPv4 alternative in captured failure logs. A loopback-only viewer SDP experiment is prepared to isolate this transport question; it is not representative of normal remote connectivity. No capture-rate variant has yet produced a valid measurement.

Archive inspection confirmed four actual ToolSearch permission_denied/policy_denied results despite the config-derived API reporting allow. The precise running-policy discrepancy remains unproven; sanitized evidence is `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/toolsearch-denials-sanitized.json`.


### Connection blocker isolated further

A diagnostic captured 32 receiver-statistics snapshots before teardown. UDP and TCP candidate checks remained in progress with zero responses and zero received bytes; no click samples were collected. Evidence is preserved locally at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-connection-stats-1.tgz`, with a concise derived summary alongside it. This diagnostic rewrote advertised public IPv4 to the bound private interface and is not remote-network acceptance.

Independent source inspection found an ownership defect: each capture Session constructs a fresh UDP/TCP multiplexer around gateway-shared sockets. Each multiplexer starts its own reader and maintains its own session registrations. Session close removes its registrations but does not retire the multiplexer reader; successive sessions can therefore leave competing readers consuming packets intended for current sessions. The correction is one gateway-owned multiplexer per physical transport, borrowed by all sessions and closed at gateway shutdown. This is a confirmed code defect; its contribution to the UAT failure still requires a controlled runtime comparison.

The user authorized testing-agent configuration, while reserving tool-system source changes for separate approval. Jim is locked and its attempted settings update returned 403 without changing configuration. A dedicated Browser UAT Test agent was created through the supported API, using OpenRouter and z-ai/glm-5.3-flash, with 95 valid built-in policies set to allow. Seven administrative/destructive tools retain effective ask under the existing product fence. ToolSearch reports effective allow; actual execution verification is pending. No tool-system source changed.

A controlled Amsterdam machine restart restored gateway health but reset the ephemeral root filesystem, including remote-only intermediate failed-run artifacts and the runner installation. The connection-statistics archive and earlier setup-failure/valid-latency archives had already been downloaded; other intermediate raw evidence was not preserved. The preview registration also reset (HTTP 404). The runner is being restored onto the persistent UAT volume before the fresh-session experiment. Capture encoder bytes were verified original before restart; no cadence patch had been applied.


The original implementation has now failed focused regressions for both transport types: the first capture delivers real video, then a replacement capture fails to deliver within five seconds after the first closes. Separate deterministic assertions also detect additional socket readers. Baseline output is `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/shared-media-mux-red.log` (21.482 seconds, no race report). Corrected implementation and green verification are in progress; this result establishes the reproducible session-replacement defect without claiming the full UAT incident is resolved.


### Shared transport correction

Commit `7d5c18138` makes UDP/TCP multiplexers gateway-owned and borrowed by capture sessions. New capture creation is fenced during shutdown; both normal shutdown and startup-error cleanup stop captures before closing transports. Configuration reload retains the shared transports. Independent correctness and simplification reviews found no remaining high-confidence issue.

Focused race verification passed the relay and gateway cases (23 pass records, zero skips/race reports). A deliberate fresh-multiplexer regression failed the gateway identity test; restoring the correction passed the affected gateway cases again (11 pass records, zero skips/race reports). Logs: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/shared-media-mux-green.log`, `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/shared-media-mux-fault.log`, and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/shared-media-mux-restored.log`. This is focused validation, not full CI or live UAT acceptance. The canonical Linux build and old-version fresh-session baseline are running in parallel.


The dedicated testing agent initially was not listed in the default workspace team. Adding it through the supported workspace API fixed picker eligibility without changing existing team members. A runner-only anchored selector then failed on the picker avatar text; the tracked literal-name option avoids that issue. Setup navigation/action timeouts are now bounded to 30 seconds in the isolated runner configuration. Run 3 produced actual ToolSearch, serve_web and browser_navigate calls with returned results and no structured permission denial; session `01M22CWD31CRSVTZ1G06EHRM45`. These are configuration/setup corrections, not tool-system source changes.

Canonical Linux build completed. A compressed binary-only image reuses the prior Chromium/base image; authenticated registry lookup returned HTTP 200 and digest `sha256:1c582b6a37968c8c26f07de578bf9415eb36ba8cad79d359f308b5eb1ce02bc7`. Fly's build command returned 1 on post-push metadata readback, so the published image was verified independently rather than treating command output as success. Binary SHA-256: `4d9b27ddb7b8efaf4b6134dda94cc991bb9d6fe26b669f7a4029f283bc9d4f9e`. Deployment and runtime verification remain pending at this checkpoint.


Deployment completed successfully on Amsterdam machine `784041ef9ed398`; `/health` reports ok and new process PID 673. Binary SHA-256 matches the built artifact. Comparison of machine environment, services/ports, mounts, guest resources and init confirmed they were preserved. The latest pre-fix UAT baseline never reached measurement: actual agent setup completed, but the test expected `.prose-sm`, which is absent in completed historical assistant rendering. Run 3 waited 240 seconds for a nonexistent element despite an actual successful final readiness marker. A focused harness correction is pending; do not classify this as a video connection failure. Consolidated pre-fix run 1–3 evidence is now retained at `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/baseline-runs-1-3.tgz` and its extracted sibling directory.


The readiness harness correction is committed as `807ad920c`, following configurable-agent/direct-preview support in `b6ecba4f0`. It selects all top-level finalized markdown blocks and requires exactly one block containing only the ready marker; separate-paragraph denials remain rejected. TypeScript and five checks using the real finalized markdown renderer passed, and independent review found no issue. Safe phase logs expose setup/video/measurement progress without printing URLs or credentials. The exact tracked fixture SHA-256 `031c1df77c5ea4e83852d2b7845934c9205a6c95191133a9bb25e4b7a9023461` was verified on upload before starting post-deployment `linux-mux-av-1`. Cadence experiment files were restored on persistent storage with matching hashes but have not been executed.


### First valid post-fix Linux result

`linux-mux-av-1` completed 100 clicks and all 300 exact ordered events in 50.477 seconds, with no uncaught viewer errors. Its sole reported failure was p95 270.9 ms against the unchanged 200 ms limit. Video received at 15 fps; 769 decoded frames, zero packet loss, interval decode 0.646 ms and jitter residence 60.245 ms. This establishes working media/input for one post-fix capture, not replacement-session acceptance or a speed improvement.

Window analysis again shows accumulating receiver delay: video residence by 20-click intervals was 11.5, 26.6, 51.2, 55.6 and 147.7 ms; audio residence was 29.3, 28.5, 30.0, 65.6 and 168.6 ms. Audio target/minimum rose to 140 ms in the last interval, while video target/minimum were 155.6/36.5 ms. This supports investigating audio receiver adaptation and cross-track synchronization, but does not establish the upstream cause. Raw evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-mux-av-1-evidence.tgz`; derived windows: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-mux-av-1-summary.json`.

A second direct-preview run started after the first viewer closed and more than 131 seconds elapsed. A capture-stop log was unavailable; replacement must be proven by distinct capture IDs with the gateway PID unchanged, not assumed from elapsed time.


### Live replacement-session verification

`linux-mux-av-2` passed: 100 clicks, all 300 exact events, 50.015-second input phase, p95 167.4 ms, no uncaught errors. Its capture ID changed from `e9ff0423d787b8a244b1249ec6a0fe8d7aa0b03957b99c2c887a36c67daf6c87` to `577b1580ff402bb8c3e2826d7f4c54e7838e4f9fb837459d2299514853f4fadd` while gateway PID remained 673. This verifies a replacement browser capture in the same deployed process. It is not a 20-minute soak or universal latency pass.

Run 1's raw audio counters recorded one concealment event adding 10,770 samples (224.4 ms at 48 kHz) in clicks 61–80, followed by 5,040 samples removed for acceleration in the next interval. Run 2 had zero concealment events/samples, zero loss and zero video frame drops. That association supports a transient audio starvation/recovery hypothesis for latency variability. It does not locate the stall in the encoder, relay, network or receiver; packet-header timing at ingress and forwarding completion is the next discriminating measurement if cadence does not resolve variability.

Both raw archives are local: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-mux-av-1-evidence.tgz` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-mux-av-2-evidence.tgz`. The instrumented 15/30/restored comparison has now been launched with loaded-code beacon checks, original-byte restoration and unchanged audio/resolution/input/latency assertions.


### Cadence experiment completed; do not promote 30 fps

All three phases produced complete 100-click/300-event measurements, with distinct capture IDs and zero uncaught viewer errors. Loaded-code beacons verified both instrumented variants retained one audio track. Results: 15 fps control p95 169.5 ms (pass), requested 30 fps p95 485.4 ms (fail; received29fps), restored original p95 405.3 ms (fail; received15–16fps). The restored control remaining slow prevents attributing the entire difference to frame rate. No 30fps product change is justified by this comparison.

The candidate30 phase had zero audio concealment events despite high latency, so audio starvation cannot explain every slow run. Its average video jitter residence was48.4ms and decode0.647ms; restoredphase53.99ms/0.650ms. Additional delay-path analysis is required.

Controller terminal0 means all experiment measurements were valid, NOT all acceptance tests passed. It restored the exact original encoder SHA-256 after each phase and at completion, verified independently after the final grace interval. Full archive: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/cadence-comparison-evidence.tgz`; derived summary: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/cadence-comparison-summary.json`. No experimental capture-rate change remains deployed.


### Remaining delay localized; bounded observer limitation

Cadence per-click decomposition puts the main extra delay before the reported frame arrives at the viewer. Independent stage p95 values (do not add percentiles): input WebSocket send return to frame receive96.7/403.2/292.6ms for baseline15/candidate30/restored; receive to presentation58.6/86.1/93.7ms; pixel sampling4.7/4.4/4.6ms. The first interval includes server input handling, CDP, rendering, capture, encoding and delivery; it does not distinguish those internal stages.

An unchanged AV run with a passive75-second header observer reproduced p95 377.9ms with100clicks and300exactevents. Kernel timestamps were available, but only354RTP-like header records matched the loopback/fixed-UDP-port filter and43socketdrops were reported. The observation covered the entire62.9-second test but not the complete network path, so no claim of timely relay forwarding or a located packet stall is justified. No video/audio payload was persisted. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/linux-packet-av1-evidence/`.

Authenticated Fly platform metrics for06:25–06:49UTC reported zero CPU quota throttling and a positive burst balance throughout the measured phases. Average steal was below1% across the four virtual CPUs in each cadence phase; memory available was approximately2.74GB at the subsequent snapshot. This does not support quota exhaustion or memory pressure as the explanation. Metric semantics are documented in [Fly CPU performance](https://fly.io/docs/machines/cpu-performance/) and [Fly metrics](https://fly.io/docs/monitoring/metrics/); actual retained metrics are `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/fly-cpu-cadence.json`, `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/fly-cpu-mode-cadence.json`, and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/fly-memory-current.json`.

Source teardown review did not establish an encoder-page leak: capture stop closes relay and cancels the encoder target, while encoder shutdown stops tracks and timers. The one-second best-effort target-close operation is a measurement candidate, not a proven leak. The next discriminating timing boundaries are gateway input arrival, completion of CDP input dispatch, first changed-frame ingress, and viewer delivery, with the actual selected media route recorded. Product speed acceptance and final cross-platform CI remain open; the confirmed shared-multiplexer defect, tool configuration, and readiness-test blockers are resolved.
