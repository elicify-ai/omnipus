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

## Clock correction checkpoint

Production correction `d9352ceea` integrates worker `5ff70a549`. Two actual
baselines reproduced distinct defects: compound audio/video reports were routed
under the wrong source identity, and Pion-generated reports introduced a second
clock alongside encoder reports. The correction qualifies source identity and
retains only encoder-origin sender clocks on the viewer path.

The final focused race selection passed seven groups/nine records in 15.118s,
with zero skips/races. Actual negative-acknowledgment packet replay, receiver
reports and picture-loss feedback controls remained covered. Both deliberate
faults were caught and production restored. Detailed evidence is in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-relay-clock-test-plan.md`.
Independent startup-lane review found no high-confidence residual defect.
Build `42762` exited 0. The isolated corrected runtime uses source `d9352ceea`,
process 17589 on port 11094 (tool session 34973); production port 10994 remains
unchanged. Binary SHA-256 and build provenance are retained in
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/clock-review-binary.json`.

Corrected normal audio/video diagnostic `99970` exited 1 during setup, before
any clicks: navigation encountered low-memory refusals at 17:36:55, 17:36:58 and
17:37:00, then the preview-address assertion timed out after 30 seconds. The
retained JSON has zero latency samples and null p95. This is not a measured
clock-correction latency failure. The memory investigation below explains the
later refusal condition. No speed or audio gain is established. The three earlier measured
runs remain results for `2259cd81f`.

Corrected-run setup evidence:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-clock-review-1.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-clock-review-1/browser-improvements-laten-f8442-d-and-receiver-video-timing/latency-evidence.json`

At 17:38:47 WIB, a read-only snapshot found 32 GiB physical memory and
1,084,825 free/purgeable/external pages of 4,096 bytes: the product's Darwin
availability estimate was 4.138 GiB (12.932%), below its 15% / 4.8 GiB
tab-admission threshold. `memory_pressure -Q` reported 46%, using a different
definition. No arithmetic or stale-read defect was established. Exact counters
at the earlier refusal times were not logged, so this later snapshot does not
prove their values or actual OS distress. The configured guard remains intact;
other operators' processes were not terminated to create a passing test.

## Retained raw evidence

Unlike the original twenty-minute soak, these runs persisted their detailed JSON:

- Diagnostic 1: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-diagnostic-1/browser-improvements-laten-f8442-d-and-receiver-video-timing/latency-evidence.json`
- Diagnostic 2: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-diagnostic-2/browser-improvements-laten-f8442-d-and-receiver-video-timing/latency-evidence.json`
- Video-only experiment: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/latency-video-only-1/browser-improvements-video-c6037-citly-inactive-viewer-audio/latency-video-only-evidence.json`
