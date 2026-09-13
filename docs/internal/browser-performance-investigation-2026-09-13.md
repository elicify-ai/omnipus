# Browser performance investigation — 2026-09-13

Amsterdam remains the target. The installed Mac app and main branch are unchanged. This records implementation and experiments, not a new specification.

## Implemented candidate

Commit `8165a714f` bounds reliable input waiting time to one second. Expiry retires the input source through the existing failure/release/Retry path, without replaying uncertain actions. This bounds stale work; it does not establish a fix for Chrome dispatch deadlines. The waiting interval starts at server enqueue and excludes network travel. The active browser-operation deadline remains two seconds.

Opt-in `OMNIPUS_BROWSER_INPUT_TIMING=1` records all gesture kinds, queue wait, internal gates, mapping, Chrome dispatch and remaining deadline budget. Fixed counters and allowlisted labels avoid input text, keys, coordinates and URLs. Separate quotas retain critical deadline/error diagnostics after routine samples are exhausted.

Opt-in `browserVideoDiagnostics=1` collects bounded receiver/playback timing counters locally. Encoder diagnostics add cumulative encode/send counters to existing local state. These counters require differences between samples; they are not a direct input-to-picture measurement. No codec, capture rate, bitrate, playback policy or location was changed. The native local cursor already moves immediately; remote cursor-shape synchronization remains separate work.

## Focused verification

- New queue age boundary reproduced before the fix; focused queue tests passed afterward.
- Queue concurrency checks passed with Go's race detector (4.361 seconds).
- Dedicated gesture timing tests reproduced absent instrumentation before the fix.
- Independent review caught critical diagnostic quota exhaustion by benign errors. A regression test reproduced it before correction.
- Final focused Go suite passed across gateway, browser, WebRTC and capture extension packages.
- 77 focused frontend tests, relevant lint and production frontend build passed.
- Deliberate missing encoder counters, overlapping stats reads and leaked transport identity mutations were caught by tests and restored.

## Live baseline findings on 848fe93bd

The user's recent manual session produced 22 backend input dispatch deadline errors. These reached the WebRTC backend; existing logs did not distinguish internal waiting from Chrome execution.

A stronger scroll test sent 62 reliable wheel messages. Its first run observed 820 pixels instead of the expected 900. A subsequent run passed with exactly 900 received and sent delta, plus exact clicks, Unicode, drag, tab/resize and held-key release/recovery. No production fix occurred between these runs. The first failure remains unresolved; an expected-state field in an artifact is not proof of actual target state.

Active receiver measurements showed sub-millisecond decode time. Mean per-frame jitter-buffer delay was approximately 15–19 ms during initial clicks/typing/scroll, but rose through idle/recovery periods to roughly 628 ms in the final interval. This is evidence for investigating playback buffering, not proof of its cause or a clean transport latency comparison. Capture, network, target-page work and test observation overhead remain separate factors.

Both audio and video receivers already requested zero jitter-buffer target and zero playout delay. Browser hints do not guarantee actual zero buffering.

A reversible viewer experiment kept both received tracks alive and compared muted audio/video playback, video-only playback, then restored audio/video. Median interval buffer delay was 9.13, 61.04 and 101.82 ms respectively; minimum target stayed around 12.7 ms. Sequential timing is confounded by elapsed time. This did not demonstrate that removing audio from the playback element improves latency, so it does not justify a production change. The test viewer was closed afterward; swapping its stream disrupts the existing frame-authorization callback lifecycle and is not a supported product setting.

A second audio experiment passed all six 20-second silence/tone/silence phases, first muted and then unmuted. Corrected test setup waits for current-frame input readiness and checks actual dedicated click routes. Audio target buffering stayed around 20–40 ms with approximately 50 packets per second. Video target rose to about 70 ms and then stayed there across mute/tone changes. Received audio energy increased during the unmuted tone. This validates the fixture and audio path, not human listening quality or a production speed improvement. Its drawn clock depends on AudioContext progression, so audio and video activity are confounded; a separate constant-visual-activity experiment is required to isolate audio cadence.

A third experiment removed that confound: an independent visual clock remained near 4 Hz while AudioContext was absent, running at zero gain, and closed. All three phases passed, preserving the same media peer, live tracks and muted state. Audio stayed near 50 packets/s and 48,000 emitted samples/s, with no emission stalls; median audio buffer delay stayed 30 ms. Video target was approximately 13, 54 and 54 ms. This does not support adding a silent AudioContext keepalive. The earlier quiet-page behavior remains unexplained, rather than confirmed as an audio-source defect.

Browser behavior can legitimately adjust buffering for audio/video synchronization; the [WebRTC specification](https://www.w3.org/TR/webrtc/) describes receiver target hints and synchronization. Our measured delay remains an observation, not attribution to a particular browser mechanism.

## Instrumented Amsterdam candidate

Amsterdam machine `784041ef9ed398` now runs `8165a714f389b92a16355a39ebd5c242706d64cd`, image digest `sha256:068e6d17f2ff4ad7d1736ecadb16aa7735be9fe9bd968e7b13b179155793294f`. Installed and running binary SHA256 both match `7d4a87de42536d894f956ab3a0d0a02a8ce6184b85a25ea612c8c16466f11bf5`. Machine settings changed only by enabling `OMNIPUS_BROWSER_INPUT_TIMING=1`. The existing application log level was `warn`, which filtered the new INFO records; it was hot-updated through the settings API to `info`, with all other returned configuration verified unchanged.

The stronger interaction check passed twice after deployment (45.4 s and 39.0 s), covering exact clicks, Unicode, 900 scroll pixels, dragging, tab/resize, held-input release and input-only Retry. Only the second pass had visible server timing records.

All 106 captured input records in that second pass completed. For 62 wheels, server queue wait median/p95/max was 0.076/0.114/1.158 ms; Chrome dispatch response was 1.335/13.136/15.653 ms. Internal preparation before Chrome had median 0.040 ms, maximum 0.063 ms. Queue wait did not grow over the burst. The smallest remaining Chrome budget was 1999.932 ms of 2000 ms. Mapping stages were absent, not independently measured as zero. This establishes a fast normal backend path in this sample; it does not explain the earlier spontaneous deadline failures or high-rate input not exercised here.

The first post-deployment video observation produced 31 diagnostic samples without statistics errors. Median per-frame decode was 0.492 ms, buffering 36.60 ms, and media network round trip 311 ms. The run recorded three freezes totaling 0.832 s and eight playback-dropped frames; it included deliberate resize and recovery, so these counters alone do not establish spontaneous stuttering. Network and browser playback cost were much larger than normal backend input handling in these observations. This is not a paired end-to-end speed comparison.

Calibration initially failed before any gesture because the previous page remained displayed after early address submission. The harness now waits for readiness before navigation. Separately, source review found an existing usability gap: the address field remains enabled while command submission can silently return before socket connection. The trace does not prove that this caused its failed navigation. No production fix for this startup navigation gap is included.

The corrected calibration passed in 19.4 s. Its first isolated wheel deliberately blocked the target page for 3000 ms. Server queue wait was 0.090 ms, internal preparation 0.045 ms, Chrome wait 2000.109 ms, and outcome `deadline_exceeded` with 1999.965 ms budget at Chrome entry. The normal second wheel waited 0.064 ms in the queue, 0.040 ms internally and 9.502 ms in Chrome, completing successfully. Both wheel effects ultimately appeared exactly once. This confirms that target-page work can produce the same timeout and execution can still occur afterward; automatic replay would be unsafe. It does not attribute the prior 22 spontaneous failures to page work.

## Remaining evidence needed

Use the instrumented candidate to capture a spontaneous failing interaction and distinguish it from the deliberately injected page workload. The stronger post-deployment interaction test has passed, with dedicated routing and exact scrolling verified. Investigate rising playback buffering with controlled repeated conditions before changing encoder or presentation policy. Do not count page loading or intentional page computation as application-harness delay. No percentage speed improvement is established by these experiments.
