# Browser runtime latency validation — 2026-09-08

Product latency acceptance remains open. All three runs used unchanged production
source `2259cd81f` on isolated port 11094. Each recorded 100 trusted native clicks
and 300 exact ordered events through decoded video. Millisecond values below
are rounded; the linked JSON retains the original samples and statistics.

| Run / mode | Exit | Click-to-decoded-video p95 | Video buffer mean / target mean | Decode mean |
|---|---:|---:|---:|---:|
| `69699`, normal diagnostic 1 | 1 | 267.0ms | 40.85 / 64.54ms | 0.677ms |
| `71735`, normal diagnostic 2 | 1 | 306.1ms | 137.96 / 146.15ms | 0.643ms |
| `67712`, explicit video-only experiment | 0 | 156.2ms | 27.14 / 33.32ms | 0.557ms |

Both normal diagnostics failed the unchanged 200ms p95 target. Diagnostic 1's
pixel sampler cost mean 23.81ms, p95 60.9ms and maximum 139.7ms; viewer press to
outbound send p95 was 0.7ms. Test-only decoder optimization `d41f2c07b` reduced
sampler cost to mean 2.48ms, p95 4ms and maximum 4.6ms in diagnostic 2, but did
not produce a latency pass. Its network-minimum buffer interval mean was 43.91ms.
No production decoder, input or audio behavior changed between those runs.

Experiment `f35ec3902` explicitly made viewer audio negotiation inactive. Its
assertions verified that the override ran, observed inactive/unnegotiated audio
transceivers, zero packets in any reported audio receiver, and actual decoded
video. Normal diagnostic negotiation and soak behavior were unchanged. This
single fresh-session comparison supports investigating audio/video synchronization;
it does not prove sender-clock causality. The experiment's pass is not a product
fix, audio acceptance or passing twenty-minute soak.

The network-minimum buffer statistic excludes extra delay introduced by audio/video
synchronization and other external target mechanisms. A gap above it suggests
additional buffering, without identifying its cause. See the primary
[W3C WebRTC statistics definition](https://www.w3.org/TR/webrtc-stats/#dom-rtcinboundrtpstreamstats-jitterbufferminimumdelay).
Reported processing time already includes buffering; do not add those values
as independent latency stages. Zero final packet-loss delta does not mean no
retransmission requests: diagnostic 1 had NACK count +3 and diagnostic 2 +1.
Earlier display metadata also does not justify subtracting time to manufacture
a 200ms acceptance pass.

A separate source inspection found two sender-report clocks for the same stream
identifier: Pion's default reports and forwarded encoder reports. The manager
lane is preparing its real reproduction and correction. That pre-existing source
finding is open, and these runtime comparisons do not prove it caused the delay.
No corrected production runtime has been measured at this checkpoint.

## Retained raw evidence

Unlike the original twenty-minute soak, these runs persisted their detailed JSON:

- Diagnostic 1: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-diagnostic-1/browser-improvements-laten-f8442-d-and-receiver-video-timing/latency-evidence.json`
- Diagnostic 2: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-diagnostic-2/browser-improvements-laten-f8442-d-and-receiver-video-timing/latency-evidence.json`
- Video-only experiment: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-video-only-1/browser-improvements-video-c6037-citly-inactive-viewer-audio/latency-video-only-evidence.json`
