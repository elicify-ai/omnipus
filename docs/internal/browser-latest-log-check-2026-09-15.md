# Latest Amsterdam browser log check

Checked on 2026-09-15 at approximately 00:33 UTC. Branch: `browser-improvements`. No production change or deployment was made during this investigation.

## Scope and session attribution

Read the latest 8,000 gateway records from Amsterdam machine `784041ef9ed398`, application `uat-omnipus`. The retrieved records span September 14 12:41:11 through September 15 00:30:23 UTC. The most recent browser activity belongs to the automated Browser UAT Test agent, not an independently identified manual session. The user's approximate error time was requested to avoid attributing automated failures to their session.

## Confirmed current failure

The candidate `bbb446cc17c200ac60f472aafdf9ddd9c927a864` endurance attempt failed after 540.908 active seconds, with 60 complete rounds. It did not pass the requested 20 minutes. At the failing scroll assertion, the received picture showed 160 units against an expected 300; key, click, drag, and text counters matched at that point. No viewer error was recorded in the test's error list.

The same receiver peer's interval-average jitter-buffer residence rose from 87 ms at 00:27:03 to 996 ms at 00:28:49, 2,152 ms at 00:29:08, and 2,713 ms at 00:29:23 UTC. Its video packet-loss counter remained zero. Interval-average decoding cost was approximately 0.5–1.1 ms per frame. For the last 100 presented frames, receive-to-expected-display delay had a median of 2,731 ms and maximum of 3,161 ms.

This establishes substantial delay after the video reaches the client. It does not establish why the receiver builds that delay, prove that every input reached the remote page, or establish that the user's latest manual errors have the same cause. Sender timing, audio/video synchronization, receiver scheduling, and test-client overhead remain possible contributors. Ordinary page loading cannot explain the measured receive-to-display portion.

## Other log evidence

- September 14 22:57:59 UTC: `browser dedicated input failed`, reason `queue_expired`. This predates the current deployment; it is not evidence that the same expiry recurred on the current candidate.
- September 14 23:18–23:21 UTC: Mia video-health records report two viewers. This merits checking exclusive viewer ownership, but does not alone establish two simultaneous input owners or identify which viewers were manual versus automated.
- Many `connection_closed`, closed-pipe, and already-closed-stream warnings occur near viewer teardown or deployment. They must not all be counted as spontaneous input failures.

## Evidence locations

- Server snapshot: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/latest-user-session-server.log`
- Failed full-run artifacts: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/endurance-candidate-bbb446cc1/`
- Test log: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/endurance-bbb.log`

## Next investigation

Match the manual-session time before claiming its cause. For the reproducible endurance failure, inspect receiver target-delay growth and sender/audio-video clock behavior against the captured host/client measurements. Keep the five-second visible-result assertion; increasing it would hide the regression. The owned test samplers were stopped after the failed run; no replacement live UI test was started during this log check.

## Follow-up: resource and synchronization discriminators

Independent analysis of the captured host samples found CPU steal (time the hypervisor denies the virtual machine CPU execution) averaging 0.6% during 00:26 UTC, 17.6% during 00:27, and 21.2% during 00:28. A five-second sample at 00:27:16 reached 39.8%. Available memory remained approximately 3.2 GB. This aligns with the receiver-delay onset, but correlation does not establish the mechanism.

The test's Node process held approximately 300–319 MB before failure. Its large memory/CPU spike began at 00:29:26, after the failure, consistent with trace finalization. The available samples do not support trace-memory growth as the trigger. Unrelated Chrome processes were excluded from this comparison.

Receiver target delay grew to approximately 2.8 seconds while its reported minimum buffer requirement remained about 49 ms. Audio/video synchronization can add playback delay independently of this minimum: see the [WebRTC synchronization implementation](https://webrtc.googlesource.com/src/+/538b76a2f11fcf316aae20082d463ba03e437cec/video/rtp_streams_synchronizer2.cc). The application captures audio and video together and forwards source sender reports; it has no JavaScript audio sample queue. CPU starvation affecting source clocks or delivery remains a hypothesis.

The next comparison negotiates audio inactive only in the automated viewer, leaving production unchanged and retaining the identical mixed-input workload, resizes, five-second assertions, video receiver, and tracing. Negotiated audio inactivity and real received video must be checked. This diagnostic cannot count as full-feature acceptance or justify removing audio from the product. A successful comparison on an unloaded host would not by itself prove synchronization caused the prior failure; host contention must also be compared.

## Audio-inactive comparison: passed, not acceptance

The diagnostic completed on September 15 from 00:39:56.082 to 00:59:57.015 UTC: 1,200.933 seconds between workload markers, 137 complete rounds and 11 resizes. The saved active duration including final checks is 1,202.857 seconds. Final remote state exactly matched 137 clicks, 1,507 downs, 548 ups, no held keys/buttons, 300 cumulative scroll units, 137 drags, text `@é`, and zero fixture errors. The test passed with no Retry/Resume and only the expected resize transitions. Audio was actually negotiated inactive; video continued on the same recorded receiver SSRC.

Across per-round intervals, mean video buffer residence had a median of 92 ms and maximum of 292 ms. The last 600 presented frames had median receive-to-display residence of 204 ms and maximum of 310 ms. Reported target and minimum delay were equal at the end; the large additional target delay seen with audio enabled was absent. No video packet loss was reported. Host steal remained substantial during the diagnostic (individual observed minute averages included 35%, 44%, and 38%), so low host contention alone does not explain its success.

This is strong evidence that audio synchronization contributes to the additional video delay under contention. It does not yet identify whether delayed audio delivery, source clock behavior, or relay clock handling triggers that compensation. Nor was playback perfectly smooth: the receiver reported 69 freeze events totaling 26.67 seconds and a final 13 FPS under load, despite the exact-response checks passing. No production fix has been made based on this comparison.

Artifacts: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/audio-inactive-endurance-bbb446cc1/`. The evidence explicitly sets `diagnosticMode=audio-inactive` and `fullFeatureAcceptanceEligible=false`. The independent acceptance auditor now rejects diagnostic or missing-mode evidence before checking duration. A normal-audio comparison with browser-local one-second audio/video statistics is next.

Independent audit confirmed all four required input categories in every one-minute bucket. Average host steal over the diagnostic was 34.9%, versus 7.3% across the failed baseline; the diagnostic's highest sampled steal was 62%. This strengthens the comparison without making the runs identical controlled resource conditions.

The diagnostic's negotiated-audio assertion was fault-checked by temporarily bypassing the audio-inactive override while retaining the diagnostic mode. The resulting live run failed specifically at the actual negotiated-direction assertion; the original probe was restored. The same bounded run verified 26 one-second audio/video samples, no unfinished sample at cleanup, and maximum observed sampling duration of 1.7 ms. Its short duration is not acceptance evidence.

The normal-audio 1,200-second run was then launched with the sampler on the unchanged `bbb446cc1` runtime. Results are pending under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/audio-normal-endurance-bbb446cc1/`.

## Normal-audio observations and proposed safeguard

Per-round results read from the still-running Playwright trace show audio buffer target/minimum delay around 1.8–2 seconds, with substantial concealed audio samples. Video's minimum remains around 0.1–0.2 seconds while its target periodically grows above 2 seconds. This further supports synchronization compensating for disrupted audio delivery. It does not yet localize the audio disruption to capture, encoding, relay, or client scheduling.

Test a viewer-only RTP playout-delay extension with minimum 0 and maximum 200 ms. Current [LibWebRTC receiver code](https://webrtc.googlesource.com/src/+/refs/heads/main/video/video_receive_stream2.cc) gives the frame maximum priority over the minimum requested by synchronization. This is a best-effort playback safeguard, not a 200 ms end-to-end guarantee or a cure for audio impairment. Under contention, audio may temporarily lag video; the goal is to prevent it from delaying browser control by seconds.

The implementation must use each viewer's negotiated extension ID, preserve shared source headers, leave audio and input untouched, and avoid advertising this viewer constraint to the capture leg. Tests are being prepared before implementation. Actual Chrome 152 negotiation and the resulting playback target must be measured; upstream support alone is insufficient. The existing silence/tone/silence audio regression will also assert received audio energy, rather than relying solely on the source page's visual tone indicator.

## Normal-audio result and safeguard validation

The normal-audio baseline completed 1,204.056 seconds between workload markers, 103 complete rounds, and exact final counters (1,133 downs, 412 ups, 103 clicks/drags, no held state, 300 scroll units, `@é`, no errors). The independent acceptance audit passed. This establishes sustained input completion in this run, not uniformly low latency: one-second video-buffer interval means reached 4.08 seconds. Audio accounted for approximately 41.6% concealed samples, mostly silent replacement. Host steal averaged 37.2%, comparable to the audio-inactive run's 34.9%. The independent timing analysis is in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/audio-normal-analysis.json`.

The safeguard now uses an outbound viewer interceptor, a per-viewer negotiated header ID, deep header cloning, and a separate viewer media engine. Audio packets and clock-report mapping are unchanged. Focused tests first failed for the missing packet value and negotiation capability, then passed. Four deliberate mutations were caught: incorrect cap, incorrect ID, shallow header copy, and sharing capability registration with ingest. Existing session/ingest/sender regressions passed in 77.652 seconds. UI sizing, dedicated-input and frame-generation tests passed (55 tests); recovery checks also passed.

Browser-version clarification: the automated runner's bundled Chromium is 149.0.7827.55; installed Chrome is 152.0.7977.83. The remote capture browser is separately Chrome 152. Both local viewer binaries were checked directly and offer the video playout-delay extension at ID 5. Future endurance evidence records the actual viewer version rather than assuming it matches installed Chrome. The final candidate still requires negotiated-answer and playback verification.

A live preflight against the old `bbb446cc1` runtime correctly failed the new mandatory negotiated-extension assertion before starting its workload. Thus a later 20-minute pass cannot silently omit the cap. The changed runtime has not yet been deployed or accepted at this checkpoint.
