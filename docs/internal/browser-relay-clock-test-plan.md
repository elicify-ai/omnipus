# Relay sender-report clock coherence

The viewer must receive one encoder-origin RTP-to-wall-clock mapping for each
outgoing audio/video SSRC. Pion's default viewer sender-report generator instead
also publishes a gateway-arrival-derived mapping every second. Different media
arrival delays can make the two authorities disagree. This source finding does
not establish the cause of the measured diagnostic latency.

Before production edits, a real Pion encoder/relay/viewer regression will send
known audio/video sender reports with independent RTP offsets and two distinctive
NTP anchors. The fake encoder disables its automatic reports and explicitly
writes the authored reports; the relay and viewer remain real. The observer must
receive both anchors for both outgoing SSRCs with every source field preserved,
and no locally generated reports across more than two default report intervals.
The encoder also observes actual receiver reports from the relay, independently
proving that removal of viewer sender generation does not remove ingest feedback.

The bounded correction uses a separate viewer interceptor registry. Pion 4.2.16
exposes no disable-sender-report option, so the helper follows its documented
custom-registration path: retain NACK, receiver reports, simulcast header
registration, statistics and TWCC, omitting only sender generation. The ingest
registry remains unchanged. The helper names its upstream version and upgrade
comparison requirement; existing real-wire feedback tests stay in validation.

No full CI, audio acceptance or measured latency improvement is implied. Root
coordinates all Go execution; baseline must precede the production correction.

## Observed regression and verification

Initial real-wire baseline 93440 exited 1 (5.026s) with a separate routing defect:
a compound video/audio RTCP packet reached both ingest receivers, each of which
forwarded all reports using its own media kind. The audio outgoing SSRC therefore
received video RTPTime 900000 instead of audio 240000. This justified the additional
source-SSRC qualification before forwarding; lifetime guards remain unchanged.

The isolated generator baseline 59694 sent each source report separately and
exited 1 (6.802s), observing four foreign NTP anchors across both media kinds.
Authored reports and ingest receiver feedback succeeded. Both production
corrections were applied only after these baseline observations.

The final regression includes separate reports and compound reports with an
additional unknown source, both with distinct audio/video RTP offsets and two
NTP anchors. A dedicated real receiver records video packet sequence numbers:
an explicit NACK must replay the exact requested packet. Encoder-side RTCP reads
also require actual reception-report blocks, preserving ingest receiver reports.
These strengthened feedback/unknown-source oracles were added before the restored
milestone, not claimed as part of the original baseline.

Initial race 35508 passed 7 top-level groups/9 total records (13.276s). The combined
fault run reconnected the viewer to the original default registry and removed
source-SSRC qualification: it exited 1 (9.391s), detecting foreign clock provenance
and cross-kind RTP field mismatches. Both files were restored byte-for-byte before
the final affected race, which passed 7 groups/9 records (15.118s), with no skips or
race reports. Scope:

```sh
GOMAXPROCS=2 CGO_ENABLED=1 go test -race -tags goolm,stdjson -p 1 ./pkg/tools/browser/webrtc -run '^(TestSessionViewerUsesOnlyEncoderSenderClock|TestSessionSenderReportForwardedWithRewrittenSSRC|TestSessionViewerPLIForwardedToIngestThrottled|TestSessionGoToGoFullFlow|TestSessionMultipleViewersShareTracks|TestSessionViewerLegReplacement_SameViewerID_OldPCClosedNewPCFlows|TestEndingFeedRetiresPacketAndReportOwner)$' -count=1 -shuffle=on -v -timeout=120s
```

The helper explicitly retains all other Pion 4.2.16 default registration stages.
Its shared MediaEngine safely deduplicates header URIs and feedback declarations
in RegisterHeaderExtension and RegisterFeedback. Statistics/TWCC preservation is
supported by those explicit registrations; this batch does not claim a separate
wire-level measurement of every feedback extension. Compare the upstream default
list on dependency upgrades.

Raw baseline, compound-baseline, initial-green, fault and restored logs reside in
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/manager-lifecycle/relay-clock/`.
No synthetic fault remains. Root owns independent re-review and a subsequent real
browser audio/video diagnostic; this correction is not yet evidence that the
observed latency or audio/video synchronization has improved.
