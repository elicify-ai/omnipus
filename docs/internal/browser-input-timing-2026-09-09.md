# Isolating streamed-browser input delay

Status: two live Amsterdam measurements completed. Input delivery is fast; video production/delivery and receiver buffering dominate remaining delay. The cause of variable buffer growth remains unresolved.

The prior Linux measurements place most variable delay between the viewer sending a mouse-up and the corresponding changed video frame arriving. They do not distinguish server input admission, Chrome input processing, rendering, capture, encoding, and transport. The existing 100-click test uses an immediate canvas response, without website navigation or network requests. Website loading time must not be counted as Omnipus overhead.

## Measurement boundaries

- Viewer: trusted pointer dispatch, outbound mouse-down/up send, changed-frame arrival and presentation metadata, pixel-sampling duration.
- Server: WebSocket message read completion, queue submission/start, live-input admission, tab-operation gate, input gate, coordinate mapping, final Chrome input command start/completion, and handler completion. These offsets use one Go monotonic clock.
- Controlled page: event handler entry, drawing-command start/completion, and bracketed wall-clock samples. Drawing-command completion is not proof of physical paint or completed video capture.
- Media: selected candidate pair and its actual protocol/address/ports, plus existing audio/video receiver statistics. A partial packet observation cannot establish that the entire media path is timely.

Server diagnostics require `OMNIPUS_BROWSER_INPUT_TIMING=1`, are disabled by default, and are bounded to 512 mouse-down/up records per connection. Logs exclude input text, keys, coordinates, page addresses, and arbitrary client-supplied identifiers. Synchronous logging occurs after the recorded completion boundary; its possible effect on following queued inputs must remain visible in the overall test and should be compared with a control.

Target-page diagnostics require `BROWSER_PROBE_TARGET_TIMING=1`. Timestamps are retained in memory, without per-click network traffic, and retrieved after the measured window using the already-authorized browser evaluation tool. An initial HTTPS-to-loopback collector preflight failed before any request reached the collector; no browser security flags were weakened. That collector approach is not part of the implementation.

## Clock and attribution limits

Server offsets can be compared directly within one input. Viewer and target `performance.now()` values have different origins and must never be subtracted directly. Linux target and viewer run on the same machine; bracketed `Date.now()` readings provide wall-minus-monotonic bounds, including millisecond quantization and bracket duration. Before/after samples must be checked for inconsistent offsets. This is a same-host experiment, not evidence of synchronized remote clocks.

Report the controlled page's own processing/drawing work and the test's pixel-detection overhead separately. Do not label the entire click-to-picture interval as Omnipus processing time. Even after these measurements, rendering/capture/encoding/delivery may remain a combined interval until additional evidence separates them.

## Separate regression work

The consolidated checks for commit `7ada86e18` reported unchecked type assertions in the new media-owner test, a test-fixture data race from changing the browser executor after asynchronous attachment discovery had started, and tab-switch recapture failures. The first two received test-only corrections in `17a38d33c`. Five focused repetitions after the fixture correction reported no race but still reproduced tab-switch functional failures.

A deterministic regression subsequently proved that late document-listener initialization replaced an already measured tab-switch picture: its generation changed from 5 to 7 without any page navigation. The correction makes replacement-listener initialization discover metadata only when the real tab-change path already owns measured refresh. Initial attachment, baseline recovery, failed-watch recovery, and genuine navigation retain picture initialization/invalidation. The test also queues real navigation during blocked discovery and requires old-picture invalidation followed by one measured recapture. Independent review found no remaining high-confidence issue.

After restoring three deliberate guard faults, 27 affected race-test cases passed with no skips or races. All four original failure groups then passed five repetitions each (20 passes, no skips or races). The corrected tab-switch source is separate from the deployed timing experiment's `17a38d33c` snapshot. Live verification of this additional correction and final combined checks remain pending. No latency threshold or failing assertion has been weakened.

Evidence is retained under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/`. The branch remains `browser-improvements`; `main` and the installed Mac instance are untouched.

## Focused verification

The restored Go timing tests recorded 19 passing test/subtest entries, no skips, and no race warnings across the affected browser and gateway packages. Three deliberate faults independently failed the pointer-mapping stage, record-limit, and millisecond-offset assertions; all were restored before the final run. Seven standard Vitest cases and the focused TypeScript check passed. Three separate JavaScript faults were caught for handler timing, pixel-state integrity, and clock quantization margin. Independent reviews of both implementations found no remaining high-confidence issue. These checks validate the measurement machinery; they are not live speed acceptance or a replacement for the separate tab-switch and live acceptance checks.

## Live results

Amsterdam ran the isolated `17a38d33c` snapshot, binary SHA-256 `296fdbd3293bbcc40df4242848f296797797f4aa28876612ab3f18fac65ca005`, image digest `sha256:b1383f7dbe17ae74d943f91575934ece92a1416ebe05001a2bd161c20d787c07`. Both runs used gateway PID 671, normal audio, the existing capture settings, and the unchanged 200 ms detection threshold. Each completed 100 clicks and 300 exact ordered events, with no held input or event-order error. Three raw browser-evaluation results per run were matched to the fresh nonce and exact final state. Evaluation and control release occurred after the measured window.

| Measurement | Run 1, slower | Run 2, faster |
|---|---:|---:|
| Pixel-detection p95 | 354.4 ms, fail | 178.9 ms, pass |
| Browser-reported expected-display p95 | 282.6 ms | 171.3 ms |
| Mouse-up send to target handler p95, approximate clock bounds | 4.0–9.6 ms | 3.6–9.4 ms |
| Target click-handler to drawing-command completion p95 | 0.2 ms | 0.3 ms |
| Drawing-command completion to reported frame receipt p95, approximate | 115.3–120.9 ms | 90.6–96.4 ms |
| Reported frame receipt to presentation p95 | 180.1 ms | 84.6 ms |
| Pixel sampler p95 / maximum | 5.6 / 616 ms | 5.5 / 7.0 ms |

These are independent percentiles: do not add them or subtract the two p95 columns to allocate one click's time. Viewer/target clock-offset envelopes were 3.6/3.8 ms wide. The frame callback can describe an older frame than pixels subsequently sampled after a delayed callback, so expected-display values are corroborating estimates, not proof of exact physical display time. Drawing commands do not establish completed rasterization or capture. The first run's click 49 had a 616 ms sampler pause; it is measurement overhead and must not be attributed entirely to the product.

Run 2 yielded all 200 successful down/up server records. Arrival to Chrome-command completion was p95 **7.347 ms**, maximum **10.085 ms**. Queue wait was p95 3.335 ms; the command itself p95 4.888 ms; tab/input gate waits were negligible by comparison. Legacy viewport-rescaling markers were absent because that branch did not execute; this is not a measured zero for all coordinate mapping. The same-host target timestamps independently bound input delivery to approximately 12 ms or less in both runs. These observations do not support input admission or website processing as the source of the several-hundred-millisecond delay.

Receiver statistics corroborate growing buffering independently of the pixel sampler. Run 1's mean video buffer residence across successive 20-click windows was 11.7, 41.1, 96.4, 126.2, and 123.6 ms. Run 2 was 9.8, 42.4, 62.5, 73.2, and 74.5 ms. Decode stayed below 0.75 ms. Both runs reported zero packet loss. Audio target/minimum delay rose above 100 ms in the slow run but stayed around 20 ms during the fast run. However, the fast run also had audio concealment, including a late 19,440-sample episode; concealment alone is not a sufficient explanation. The fast run's brief postmeasurement interval showed increased audio buffering, so a 100-click pass is not sustained-speed acceptance.

Both runs retained 14 receiver-setting samples: audio and video reported `jitterBufferTarget=0` and `playoutDelayHint=0` throughout those samples. The low-latency requests were already present; setting them again is not an evidence-based remedy. Their reported values do not prove that actual receiver buffering is zero.

The distinction between network-derived minimum delay and additional target delay follows the [WebRTC statistics definitions](https://www.w3.org/TR/webrtc-stats/#dom-rtcinboundrtpstreamstats-jitterbufferminimumdelay). Additional delay can arise from synchronization or other receiver mechanisms; these data do not establish which mechanism is responsible. [Video-frame callback timing](https://wicg.github.io/video-rvfc/) also includes scheduling beyond pure jitter-buffer residence.

## Logging configuration and retained evidence

Run 1's server records were filtered by the runtime's default `warn` log level, despite the diagnostic flag being enabled. This was confirmed from configuration, logger routing, and the active log descriptor; absence of records was not interpreted as zero delay. A supported configuration update enabled `info` for run 2 without restarting PID 671 or rebuilding. Effective `warn` was then restored through the same API; its explicit configuration key remains because the merge API does not delete keys. The update also removed a legacy workspace value from the API representation of an HTTP OpenRouter provider; private before/after evidence is retained, and exact configuration preservation is not claimed. No tool-system source was changed.

Artifacts and independent summaries are under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/fly-uat-browser/input-timing-image/`: both `linux-input-timing-*-evidence.tgz` archives and extracted `final/` / `final-2/` directories, raw and validated `target-readback/` / `target-readback-2/` results, `early-2/input-timing-2-records.jsonl`, `early-2/go-stage-summary.json`, and `two-run-stage-comparison.json`. Runtime health and WARN restoration were verified; no test or extraction process remains running.

Consolidated checks on `a91f25a97` passed the cross-platform, macOS phase-three, build, CodeQL, and shell checks. The PR workflow failed on two test-fixture lint findings and one settings keyboard-navigation test that passed only after retry. The lint findings were corrected; the enabled linters reported zero new issues across browser and gateway packages. The settings failure expected the Providers panel after keyboard wraparound but still observed Memory. Its failed trace confirmed that several per-step assertions accepted the previous tab before asynchronous focus movement completed, while all ten tab identities remained stable. The corrected test requires the exact successor tab and its associated panel after every key, including wraparound. It passed ten runs without retries against the actual Settings screen on the isolated Mac gateway at port 11094; focused TypeScript checking and independent review passed. A controlled delayed-focus reproduction failed with the original test and passed with the correction. Ignored first-key, reversed first-key, and ignored-wrap faults each failed the exact-focus assertion; the restored case passed. Evidence is retained under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/settings-flake/`. This verifies Settings test synchronization, not latest browser-streaming acceptance; final CI remains pending.

Next investigation: identify why receiver buffering grows, and separate rendering/capture/encoding from media delivery where useful. Existing ingest selected-pair/track diagnostics and the viewer's newly retained candidate metadata can identify the actual media endpoints without another product rebuild. The previous UDP observer's partial-route coverage and socket drops remain disqualifying for packet-starvation claims. The tab-switch correction `a91f25a97` is not part of these deployed measurements; its live verification, latest native Mac acceptance, and consistent speed acceptance remain open.
