# Remote-browser architecture checklist review — 2026-09-13

Reviewed against browser-improvements source 848fe93bd. Subsequent implemented changes and live results are recorded in the [performance investigation](https://github.com/elicify-ai/omnipus/blob/browser-improvements/docs/internal/browser-performance-investigation-2026-09-13.md). This is an assessment, not authorization to change the accepted two-connection design. No runtime changes performed.

| Item | Current implementation / assessment |
| --- | --- |
| One PeerConnection | Two viewer connections intentionally separate input and media recovery. One is a reasonable simpler design, not a demonstrated speed improvement here. Neither arrangement eliminates the backend dispatch deadline. |
| Audio/video tracks | Both on the media connection. |
| Unreliable unordered mouse | Implemented for unheld hover with maxRetransmits=0; drag movement stays reliable. |
| Reliable clicks/keyboard/text | Implemented on ordered input-reliable; connection failure still leaves execution uncertain. |
| Commands on data channel | Navigation/tab/viewport/control remain on authenticated WebSocket with cross-connection acknowledgment fencing; no gesture fallback. Clipboard is not established as a dedicated structured command by this review. |
| Binary input | Not implemented; generated-schema JSON. No measurement implicates serialization in current timeouts. |
| Input compression | No application input compression. |
| 60–120 Hz mouse | Current 25 ms timer paces at about 40 Hz, with latest-hover replacement. Raising rate needs processing-capacity evidence. |
| 60 FPS | Capture currently caps at 30 FPS; no sustained 60 FPS claim. |
| Hardware realtime encoding | macOS allows GPU; Linux launch forces software. Current Amsterdam hardware encoding is not established; OpenH264 appears in logs. |
| Minimal buffering | Receiver requests zero jitter/playout delay where supported; hints do not prove actual zero buffering. |
| Quality before latency | Existing balanced degradation, bitrate ceiling and CPU-driven resolution adaptation; no verified strict end-to-end latency bound. Adaptation target is 12 FPS, restoration threshold 24 FPS. |
| Direct route / TURN | Direct connectivity supported; no current-session selected-route measurement in this review. TURN is a connectivity option, separate from prohibited WebSocket gesture fallback. |
| Nearby server | Amsterdam remains far from the Bali viewer. Earlier separate observations measured 337–404 ms RTT; not a current-session measurement. |
| Never build queues | Replaceable hover slot implemented. Reliable queue bounded at 512 events; count bound is not a latency bound. Queue age and backpressure require scrutiny. Important actions/releases cannot be silently dropped. |
| Unreliable scroll | Unsafe for current relative deltas: losing a +100 scroll event loses distance. Keep reliable with compatible delta accumulation; unreliable cumulative-state design would be a separate protocol change. |

Reliability and ordering apply within a channel, not across the hover and reliable channels. Existing sequence/barrier and capture-generation guards are necessary to prevent late hover from overtaking clicks or acting on a different picture. Reliable transport does not prove application execution and cannot guarantee delivery after connection failure.

Primary references:
- https://www.rfc-editor.org/rfc/rfc8831.html — shared SCTP congestion behavior and per-stream ordering.
- https://www.w3.org/TR/webrtc/ — channel reliability options and receiver buffering targets.
- https://webrtc.org/getting-started/turn-server — relay connectivity.

Source anchors: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserInputWebRTC.ts; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/components/browser/BrowserLiveView.tsx (MOVE_FLUSH_MS); /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/src/lib/browserWebRTC.ts (receiver hints); /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/captureext/embedded/encoder.js (capture maxFrameRate, balanced adaptation); /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/exec_resolver.go (platform GPU choice); /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/webrtc/dedicated_input_queue.go and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/webrtc/inputdc.go (512-event queue).

## Additional capture/render proposals

- **Local cursor:** native client cursor already used in BrowserLiveView; remote cursor shape/hotspot synchronization is not implemented by that code. Local movement avoids network delay but is not literally zero-latency presentation. No new cursor implementation justified as the first performance fix.
- **Input-triggered capture:** current capture is Chromium extension tabCapture, not a screenshot polling loop. Constraints request minFrameRate 15/maxFrameRate 30; actual cadence must be measured. CDP beginFrame requires BeginFrameControl and its availability/integration with this browser/extension pipeline is unverified. Input-only capture would miss animation, video and asynchronous page updates. A forced frame does not prove that an asynchronous action's result has rendered.
- **GPU zero-copy:** useful direction on suitable GPU infrastructure, but no per-frame CPU copy is established as the current bottleneck. Current code hands the captured MediaStream to Chromium's WebRTC sender; direct NVENC/VAAPI surface ownership is not exposed by this implementation.
- **Encoder controls:** intra-refresh and slices are codec/encoder-dependent and not general RTCRtpSender controls. Recovery/join keyframes still matter. Native viewport/DPR avoids gratuitous scaling but high DPR multiplies pixel work; deliberate downscale is a valid latency tradeoff.
- **Latency instrumentation:** highest priority. Existing fixture pixel counters prove visible effects and frame callbacks exist, but arbitrary input-to-result correlation and full stage timing are absent. An echoed input ID alone proves reception, not visible execution. Client-to-client elapsed time avoids cross-machine clock subtraction; per-stage clocks require proper correlation. Browser frame callbacks approximate compositor presentation, not physical display illumination.
- **WebTransport/WebCodecs:** separate architecture experiment. QUIC supplies transport congestion control; application owns media pacing/adaptation, buffering, loss handling and synchronization. Client-to-server transport does not require WebRTC peer NAT traversal, but UDP-restricted networks and fallback support remain deployment concerns. No guaranteed latency improvement.
- **Vector/DOM/hybrid:** major rendering architecture alternatives, not immediate fixes. Cloudflare NVR remotes drawing instructions, not merely DOM. Remote execution still incurs network round-trip latency; bandwidth and latency improvements are workload-dependent.
- **Codec/region/pointer sampling:** hardware-compatible H.264 and nearest-region placement are reasonable; actual encoder/decoder support must be verified. pointerrawupdate is optional and may increase event rate; retain coalescing and explicit pacing.

The claims of halving perceived latency or a remaining 20 percent transport contribution are not established for Omnipus. Prioritize measurement and known backend timeouts, then measured capture/encode improvements. No runtime changes made.

Additional primary references:
- https://chromedevtools.github.io/devtools-protocol/tot/HeadlessExperimental/
- https://www.w3.org/TR/webtransport/
- https://www.w3.org/TR/webcodecs/
- https://developers.cloudflare.com/cloudflare-one/remote-browser-isolation/canvas-remoting/
