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

## Latest-session recheck at 01:46 UTC

Read 12,000 recent records through September 15 01:45:03 UTC. The recent browser activity after 01:20 is attributed to Browser UAT Test. Five dedicated-input warnings at 01:29:30, 01:38:04, 01:39:15, 01:40:30, and 01:44:03 report `connection_closed`; these coincide with automated run endings and surrounding transport teardown. They do not establish five spontaneous manual input failures. No `queue_expired` recurrence appears in that window. The snapshot's last queue expiry remains September 14 22:57:59 UTC, before the current deployment.

Mia's latest recorded video-health transitions remain September 14 23:18–23:21 UTC, with two viewers. This is a lead for the outstanding viewer-ownership regression, not proof that two input owners were active. Manual-session attribution still needs the user's approximate time or exact error. Source snapshot: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/latest-session-recheck.log`.

The current 8cd6d89d1 deployment passed only a short playback-cap calibration; its full normal-audio 20-minute acceptance remains pending. The audio diagnostic with unmuted phases first terminated with an energy assertion failure; its temporary test edit was restored. The precise failing phase must be inspected before interpreting this as absent captured audio. No additional runtime change was made during this recheck.

## Current candidate calibration and corrected audio oracle

The deployed candidate is `8cd6d89d1f67bc986d0ef6893b4faddc5d2ba3a3`. Installed/running binary checks and unchanged Amsterdam machine configuration were reverified before the full run. A short live calibration negotiated video playout delay at extension ID 5 and passed 3 mixed-input rounds with exact remote counters. Its 23 one-second samples had video-buffer interval means ranging up to 67.2 ms. This is calibration evidence, not a full-duration acceptance result.

The unmuted-first audio diagnostic passed all three unmuted phases, with tone mean-square energy 0.000413 and subsequent silence approximately 9.31e-10; it failed only after muting during the tone. [LibWebRTC ChannelReceive](https://webrtc.googlesource.com/src/+/refs/heads/main/audio/channel_receive.cc) applies output gain before accumulating this energy statistic. Consequently, expecting nonzero energy from a muted element was an invalid oracle. The test now retains the original unmuted tone threshold, requires silence when muted, and checks advancing packets as well as samples/duration throughout. Independent review and focused semantic TypeScript checks passed; corrected live positive and mutation checks remain pending. No audio production code was changed.

The full normal-audio 1,200-second run is active under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/playout-endurance-8cd6d89d1/`. Early host contention is lower than the earlier normal-audio baseline; final analysis must retain that limitation.

## Full 20-minute acceptance on the playback-cap candidate

The normal-audio run passed on September 15, 01:48:31–02:08:36 UTC: 1,205.237 seconds between workload markers, 136 complete rounds, and 11 resizes. Exact final remote state was 136 clicks, 1,496 downs, 544 ups, 136 drags, no held keys/buttons, net scroll zero after alternating directions, text `@é`, and zero fixture errors. Outgoing observations included 4,080 wheel events, 5,104 coalesced hover events, 1,358 key downs, 406 key ups, and 136 text commits. Every one-minute bucket included mouse movement, scrolling, clicks, and keyboard events. All human input used binary-v1 WebRTC; no WebSocket input route appeared. There were no Retry/Resume actions or input failures.

Independent audit confirmed audio remained negotiated recvonly and the video playback-cap extension was negotiated. The viewer was Chromium 149.0.7827.55. There were 1,205 one-second media samples with no unfinished sampler at cleanup. Server records for the workload contain no dedicated-input failures; all 37 video-health records report one viewer. The 1,498 logged timing records all report completed outcomes, but these sampled/buffered logs are not a census of every input; the independently decoded remote state is the completion oracle.

Video buffer interval mean residence had median 99.95 ms, p95 286.48 ms, and maximum 414.74 ms, compared with 423/2,912/4,082 ms in the previous normal-audio baseline. Receiver target delay stayed below 196 ms. The last 600 retained frames had receive-to-display median 194 ms, p95 218.3 ms, and maximum 230.4 ms; this short retained history is not a whole-run end-to-end latency measurement.

This run did encounter the previous adverse condition: host contention rose at approximately 01:57:29 UTC and audio target delay exceeded one second at 01:57:39, while the video target remained bounded. Average host steal was 26.0%, versus 37.3% in the prior baseline; the comparison is not a controlled identical-load experiment. Available memory stayed above 3,242 MiB. The safeguard does not eliminate all playback imperfections: the receiver reported 41 freezes totaling 15.081 seconds (previous baseline 102/35.863), and audio buffer intervals still reached 3.17 seconds with substantial concealed samples. These are explicit remaining quality limitations, not input-completion failures or a claim of zero jitter.

Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/playout-endurance-8cd6d89d1/`; independent analysis `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/playout-endurance-analysis.json`; workload server log `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/playout-endurance-server.log`. Both owned resource samplers were stopped after the test completed. Focused audio, native-scroll, and fullscreen regression checks follow.

## Focused audio follow-up

The corrected six-phase audio regression passed in 2.5 minutes. Muted phases reported zero output energy while packets advanced; unmuted tone mean-square energy was 0.00040244, followed by zero measured energy in the final silence window. All phases retained the same media peer and live audio/video tracks. A deliberate zero-energy observer mutation failed specifically at the unmuted authored-tone assertion; the original test was restored before the positive run. Artifacts: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/audio-energy-corrected-8cd6d89d1/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/audio-energy-oracle-corrected-mutant-8cd6d89d1/`. This verifies content delivery and muting, not perfect audio quality under contention.

A separate native-scroll fault check deliberately prevented the browser's default wheel action. The test reached the real fixture and failed because actual document scroll remained zero instead of the independently expected 300. The original fixture and content-addressed configuration were restored. Normal native-scroll and popout ownership checks are next on installed Chrome 152.

## Chrome 152 native-scroll and viewer-ownership regressions

Both focused checks passed on installed Chrome 152: native document scrolling in 21.9 seconds and popout ownership in 1.4 minutes. Native scroll moved exactly 0→300→600→450, remained at 450 after text entry, and passed four trusted hover coordinate checks with text `@é` and no errors. This is actual document scrolling, not a JavaScript wheel counter.

Popout ownership verified that the source panel disappears and closes its media/input peers before the new viewer attaches; resizing the old window sends no viewport update; popout reload does not reopen the panel; both the product Close button and native tab close return the viewer only to its original owner. An unrelated same-origin app window never attaches or opens a viewer. No observer overflow or page error occurred. Artifacts: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/chrome152-regressions-8cd6d89d1/`. The private Chrome config initially failed before browser launch due to an ES-module path expression; it was corrected before these successful runs.

## Remaining new-target failure found by external-page retest

Do not treat the branch as fully accepted yet. On installed Chrome 152, the actual Google result click opened a new remote YouTube tab and updated the address, but the received picture remained Google for over 45 seconds. The last Google capture recovery was at 02:20:08 UTC. The click's down/up reached Chrome at 02:20:25–26, and the server logged `live view: could not re-apply the panel viewport` with `context deadline exceeded` at 02:20:52. No new-target capture-health event followed the click. The earlier “Waiting for the current page” notice was transient and the eventual click was delivered; this failure is specifically the subsequent new-target picture handover.

The failed run's screenshot proves the mismatch: YouTube is the selected tab/address while Google results remain displayed. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/popout-google-youtube-8cd6d89d1-chrome152-1789438777311/`; server snapshot `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/newtab-stale-video-server.log`. An earlier interactive attempt was stopped before clicking because its first screenshot preceded loaded results; it is not a product-failure verdict.

A deterministic local child-tab fixture is being added. Its first two attempts did not reach the child assertion: the first called the pixel observer before video was ready; the corrected readiness check then exposed a real residual error in the same session, `browser live: new tab viewport is still settling`, after ordinary navigation. This suggests failed viewport handover can leave the session unable to start its next picture; it does not yet identify which admission or Chrome operation timed out. The second failure's popup screenshot was extracted from its trace because the standard screenshot captured only the source window.

Code review found that the final viewport cancellation handler erases detailed errors, leaving the generic deadline message. A narrow diagnostic correction preserves stage labels and wrapped cancellation identity without changing deadlines, command order, retry behavior, or successful operations. Two focused tests first failed for missing stages, then the relevant viewport/active-target suite passed in 19.547 seconds. Independent review found no blocker. This diagnostic candidate is needed to localize the handover failure; it is not the behavioral fix. The completed 20-minute result remains valid evidence for its original candidate and workload, not proof that new-tab handling is correct.

## Latest reported input issues: fresh log recheck through 02:45 UTC

Read a fresh 16,000-line Amsterdam gateway snapshot ending September 15 at 02:45:01 UTC (09:45:01 Jakarta). The confirmed unresolved behavioral failure remains the actual Google-to-YouTube new-target handover: click delivered at 02:20:25–26, viewport reapplication timed out at 02:20:52, old Google picture remained under the selected YouTube tab. Subsequent test attempts also encountered “new tab viewport is still settling” instead of live video. This proves a picture/viewport recovery failure can block interaction; the generic deadline does not yet identify the stalled operation.

The newest dedicated-input warnings at 02:44:32 and 02:45:01 say `connection_closed` and overlap the automated new-tab fault-check run. That run failed at initial video readiness, before its parent or child pixel assertions; it is not a successful mutation proof. Its original fixture and configuration were restored. No new `queue_expired` record appears after September 14 22:57:59 in this snapshot. Mia’s latest video-health record remains September 14 23:21:19; recent video-health activity is attributed to Browser UAT Test. Warning records alone lack enough session identity to attribute every disconnect to a human or distinguish intentional teardown from spontaneous failure. The user was asked for approximate local time and agent name; attribution remains pending.

Source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/latest-user-session-recheck.log`. No live browser interactions or runtime deployment were performed during this log investigation. The diagnostic image upload is separate and does not itself change the running instance.

## Diagnostic candidate deployed and stronger popup checks

Deployed `88413520b87cb69e24f3bb58ea64c971a0f9fae2` to the existing Amsterdam machine. Image digest `sha256:898868cf8adcd5476b4d89eb83b3ae8911cda3acd920f682307c18f8920ac0b5`; installed and running binary SHA256 `d212cc51369878c296a98b24a95004131bfd3e7e232614bea211ddbd47a44ae8` verified with unchanged machine configuration and health200. Image push succeeded; a subsequent builder-release timeout did not invalidate the published image. Auth and fixture registration were refreshed.

The actual Google→YouTube new-target switch succeeded in the first diagnostic run. Its first resize then failed a helper assumption: independent review found capture dimensions are rounded down to multiples of12, while the helper allowed only2pixels. Observed1236×732 matches the expected adaptive capture for1186×708, rather than proving a browser-layout defect. The full external-page test remains incomplete until a mathematically correct geometry assertion passes all resizes and search input.

The deterministic child-tab regression now checks status and alerts under the actual dedicated viewer. Its previous docked-panel scope matched no elements in a popup and therefore passed vacuously. With corrected scope, normal child navigation, `@é`, and three resizes passed in33.9seconds; finalnonce26766,1426×908,zeroerrors. A deliberate child-picture identity mutation then failed at the five-second child nonce assertion with the parent precondition satisfied; original fixture/config restored. Focused semantic TypeScript checking passed. Repeated child-tab testing follows. No intermittent-timeout fix is claimed from these successful runs.

## Repeated tab tests and target-aware shortcut correction

Five consecutive controlled new-tab runs passed on diagnostic884 in4.4minutes, including fresh child-picture identity, text, and three resizes per run. Artifact directory: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/new-tab-repeated-88413520b/`. These controlled passes do not erase the earlier real external-page timeout.

Committed `cab8d51b83db6d9bd11a0b5653a77db4242fbe9c`: require the current browser target to match the capture target before trying the unchanged-size shortcut. A different tab cannot reuse that old picture; it now enters existing invalidation/resize directly, retaining all admission and measured-convergence protections. Regression first failed on the old preflight path; focused viewport/active-target tests passed in7.342seconds; deliberate guard removal reproduced the regression failure, then original source was restored. Independent review found no blocker. This removes unnecessary new-target measurement, but no live stage evidence yet proves it resolves the intermittent deadline. Linux candidate build is in progress; deployed runtime remains884 at this checkpoint.

## Exact failing stage reproduced on diagnostic884

A second actual Google→YouTube run switched targets successfully, then failed its first resize from1426×718 to1186×708. At03:01:59UTC the server reported acknowledged resize but failed CSS read-back; at03:02:03 it reported `viewport final geometry: layout viewport read: context deadline exceeded`. The viewer received capture generation135 transitioning, failed control3 acknowledgments, and no recovered picture for that generation. Screenshot contains both “Input connection failed. Retry input” and “Waiting for the current page…”. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/popout-google-youtube-88413520b-chrome152-1789441263974/`; server `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/viewport-diagnostics-resize-failure.log`.

The later successful control4 acknowledgment is explained by failure-triggered safety release, not completed resize. Gateway release acknowledgments may carry the current unready capture identity; the client correctly stays blocked. The target-aware shortcut correction does not address this final-read failure. Its Linux binary was built but is not being deployed separately while the targeted recovery correction is developed.

Next correction: one final-measurement retry only for a stage deadline while the original operation/caller remains live, with unchanged overall budget and target/capture checks. Exhausted recovery must also make the Retry input button repair the unfinished current viewport before enabling input; reconnecting input alone cannot recover an unready picture. These changes are in test-first development, not yet deployed or accepted.

## Recovery validation and the real command budget

The bounded final-read retry passed focused viewport/active-target tests in9.080seconds. A further convergence test passed in5.298seconds and proved the retry allowance spans the whole operation. Deliberately resetting that allowance for each convergence pass caused the expected failure: the test received success where a second-pass deadline must remain terminal. Original source restored; the wrapper initially expected different assertion wording, so its nonzero wrapper status was checked against the actual failed-test output rather than treated as a missing mutation failure.

The viewer recovery patch passed58 focused tests across sizing, dedicated input and frame generation; semantic TypeScript checking passed. Review then identified a late-picture edge: a fresh picture arriving immediately before Retry must be accepted against the original failed-resize boundary, not require another generation forever. That regression/correction is still underway.

Critical deadline clarification: epoch-stamped UI viewport commands inherit an absolute FIVE-second deadline from original enqueue in browser_command_queue.go. The browser layer’s21.2-second maximum is capped by this caller deadline. Consequently the reproduced final-read timeout exhausted the entire UI command, and an internal retry correctly cannot run afterward. A viewport-only ten-second queue budget is being tested; other commands retain five seconds, queue delay still consumes the original budget, and ownership/cancellation rules remain unchanged. This creates bounded room for measurement recovery without launching detached resize work. The UI handoff remains15seconds. None of these uncommitted combined recovery changes is deployed yet.

## Deterministic live six-second resize stall reproduced the failure

On diagnostic884, the authored fixture was visibly armed at03:22:15.764UTC with nonce54253 and actual633×908 dimensions. A real resize requested705×1008 and triggered exactly one six-second renderer stall. The page then reported started1/completed1 and zero fixture errors, but the viewer retained input-failed alerts and the final measured page was705×865. The test failed actual-geometry verification after the stall, not fixture setup. Server03:22:17 initial CSS settle read timed out;03:22:21 final geometry failed with the same layout-read deadline as the YouTube reproduction. Artifacts: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/viewport-stall-red-88413520b/`; server `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/viewport-stall-red-server.log`.

The late-picture Retry correction passed59 focused UI tests and semantic type checking. Deliberately removing supersession checks failed its superseded-tab regression; source restored. Production SPA build follows.

Queue budget RED verified: existing viewport expires at5s rather than10, queued4s leaves1 rather than6, and actual validated viewport classification lacks the new budget. Additional RED verified Stop/navigation/release/new-viewport supersession is not currently implemented. Both corrections are being implemented together so a longer viewport cannot trap a newer5s control behind it.

The live stall also proves why extra time alone is insufficient: a failed initial settle check skips Chrome content-height compensation. Recovery is being extended to carry that operation-local outcome and permit one existing content-size correction after a successful later measurement. Ordinary legitimate clamp behavior remains after that bounded attempt; new-target convergence remains strict. This is still implementation/validation work, not a deployed stable result.

## Combined recovery checks before packaging

Final viewer checks passed59 tests across fill-container, dedicated input and frame generation; semantic checking includes both live stall/retry tests and passed. Production SPA build passed in36seconds. The new11-second fixture case is authored to verify an exhausted command followed by exactly one explicit Retry and real input recovery; live execution remains pending.

Server behavior now includes: viewport-only10seconds from original enqueue;5seconds for other commands; one final geometry retry within the same operation; one content-size correction when initial measurement failed before compensation; current-owner Retry reapplying viewport before restoring input. Newer validated viewport/navigation/Stop/release/tab actions cancel active and queued obsolete viewports. Queued obsolete work receives cancellation even when its old deadline has elapsed, so existing epoch checks suppress stale failures rather than retiring the newer input connection.

The content-height regression failed before implementation for both first-read and retried-read recovery, while scrollbar and strict-clamp cases behaved as intended. After implementation, focused browser tests passed in26.127seconds and gateway tests passed in17.653seconds. Two older fake-tab fixtures initially failed because they consulted real Mac memory pressure; their test setup now injects available capacity, matching the existing viewport fixture. No production admission/memory setting was changed. Queue regressions cover fresh and11-second-old obsolete jobs plus prompt administrative supersession. Independent reviews found no remaining blocker in these changes. Final deliberate guard-removal checks and immutable candidate packaging follow; runtime remains diagnostic884 until deployment is explicitly verified.

## Fresh log and session check after the latest input report

Retrieved 18,000 gateway records through September 15 03:50:56 UTC and refreshed the session list. Amsterdam machine `784041ef9ed398` is started on image `browser-input-88413520b`; the combined recovery candidate `e3ed9cae0` is not deployed. Its Linux build completed and its image packaging was started.

The latest session is `session_01M2HHBYSGX4HJY7MJVF3YWX7C`, Browser UAT Test, created for the six-second stall reproduction at 03:22 UTC. At 03:22:17 viewport read-back timed out; at 03:22:21 the gateway reported `viewport final geometry: layout viewport read: context deadline exceeded`. The actual Google/YouTube resize reproduction produced the same final-stage error at 03:02:03 UTC. These confirm the existing resize/recovery defect on the old runtime; they do not independently identify the user's manual session.

Many connection-closed warnings overlap automation teardown. They cannot be counted as spontaneous manual failures. The user was asked for approximate error time and whether Amsterdam/Mia or the local Mac app was used. No new manual-session root cause is established by this snapshot. Evidence is retained privately under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/` in the `latest-investigation-gateway.log`, `latest-investigation-machine.json`, and `latest-investigation-sessions.json` files.

## Combined recovery candidate deployed and first live results

Amsterdam now runs `e3ed9cae07629eaf4c2cfdbc0091fcd912d750b5`, image digest `sha256:43bf45c6aeafc8933881546ad9589abd6328239150a967760eadf884112bc902`, installed and running binary SHA-256 `110a398618d0f6978f0efc4a8506512058c812f5e2cbf300df2b227e91a0c47c`. Verification passed with location, resources, environment, and services preserved.

The six-second authored renderer stall passed without Retry: 04:09:07 armed at 633×908; 04:09:15 recovered at 705×1008; exactly one completed stall, no transient failures, subsequent click and `@é` delivered through binary input. The eleven-second stall passed with exactly one Retry: failed at 04:11:01, Retry at 04:11:01.960, correct 705×1008 picture at 04:11:08.915, subsequent click/text correct. Evidence directories under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/` are `viewport-recovery-e3ed9cae0-warm` and `viewport-retry-e3ed9cae0-startup-observed`.

Two prior attempts failed their initial 15-second ready assertion before entering the authored scenario. Their empty observations/gesture logs and media setup timeline distinguish these from resize failures. The test now allows the existing 45-second product startup budget only for initial attachment and records actual startup time; recovery deadlines remain unchanged. The successful explicit-Retry run measured 8,803 ms initial startup. Cold startup above 15 seconds remains a performance limitation, not a passing startup-latency claim. Actual Google/YouTube fullscreen and final twenty-minute acceptance remain pending.

The installed-Chrome-152 Google→YouTube popout test subsequently passed: clicked an actual Google search result into a new YouTube tab, resized to 1200×800, 1700×1000, and 1440×810, verified current control acknowledgements and recovered geometry, and typed/submitted `omnipus test` through the streamed page. All checkpoints were ready with no alerts. Screenshots and evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/popout-google-youtube-e3ed9cae0-chrome152-1789445484590/`. The final normal-audio twenty-minute endurance run has started on the same verified binary; its result is pending.

## First combined-candidate endurance attempt interrupted by authentication

The run did not pass: 60 complete rounds, 556.860 seconds at final collection. At the next round, expected key counters were visibly reached, but readiness then disappeared. The failure screenshot is the sign-in page, with a session-ended notice. Trace requests returned 401 from `/api/v1/workspaces` at 04:23:02 and `/api/v1/auth/validate` at 04:23:03 UTC. Final remote pixels are unavailable after that navigation; no complete-run success can be inferred.

Server chronology shows an admin login evicting oldest bearer tokens at 04:22:39, Mia session attachment at 04:22:45, and two simultaneous viewers on the shared operator by 04:22:55. An external-size viewport (633×720, DPR2) was applied at 04:22:56, whereas this test used its authored dimensions. These establish concurrent shared-browser activity and invalid authentication. The log does not identify which human or process initiated the other login. No production authentication change is justified from this overlap. The user was asked whether they were testing then and to leave the shared instance idle for the uninterrupted repeat.

Test artifact directory: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/endurance-e3ed9cae0/`. Full failure-window server evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/endurance-e3ed9cae0-failure-server.log`. Both owned resource samplers were stopped. The prior focused automatic/explicit recovery and actual Google/YouTube passes remain valid, but do not replace the required twenty-minute run.

## Subsequent queue timeout distinguished from the sign-out

At 04:23:55 UTC a remaining viewer's control epoch 2 failed with `queue_expired`. Timing evidence shows hover received at 04:23:53.654 spent about 1,521.857 ms inside Chrome dispatch after only 0.114 ms before dispatch. Mouse-down received at 04:23:54.694 waited 482.373 ms behind that hover, acquired browser gates in approximately 0.020 ms, then spent 719.983 ms in Chrome before queue expiry canceled active work. The expired waiting input was never started and its kind is not recorded; identifying it as mouse-up would be speculation. Capture generation 69 remained unchanged across these two slow inputs. No key events support a held-key resize explanation for this window.

This is an actual input-pressure failure separate from the automated sign-out. Proposed bounded follow-up: after a valid reliable input arrives, cancel only the active queue-selected unheld-hover wait, preserve the control/source lifetime, and join that sink before processing the reliable FIFO. Reliable movement/drags must never use this path. Cancellation cannot undo a Chrome event already issued, so targeted queue tests plus visible live hover/click/release behavior are required before claiming a fix. No change is yet deployed for this pressure case.

A local native-CDP experiment rejected hover-wait preemption as a performance fix for a busy renderer. Three paired runs used a 1,500 ms mouse-move handler and 600 ms mouse-down handler, with click intent after 1,000 ms. Serial click completion took 1.19–1.20 seconds after intent; sending down before hover acknowledgement took 1.19–1.28 seconds. All six runs produced exactly one trusted down/up. The down handler still started after hover ended: cancellation would merely move waiting into the click. Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/hover-cdp-experiment/results.json`. This is Chromium149 on the local Mac, not Linux acceptance. No hover-preemption production change was made; provisional tests are stored outside the active suite. A bounded matching-release continuation is being assessed separately against the existing two-second active-input and one-second queue limits.

## Bounded press/release implementation and live-test precondition

The focused queue regression demonstrated premature failure of a sole matching release after one second while the active press remained within its two-second execution budget. The implementation now permits that exact release to wait for the press’s original deadline, then executes it once with the existing bounded release budget. Mixed actions, mismatches, and queued repeats retain the normal waiting limit. Printable key-down text is preserved in the dispatched event; it is not incorrectly required on key-up. All dedicated-input unit tests passed (3.856 seconds), and removing either the release exemption or original active deadline made the relevant regression fail. The source was restored after both checks.

A configuration-only isolated test workspace was created: `01M2HNY7GBFJTSM939ST0RM682`, named Browser Stability Automated Test, with the existing Browser UAT Test agent. The test helper now uses the app’s hash route and asserts it, rather than a plain path that silently selects the default workspace. The isolated live release regression has not yet reached its press scenario: the captured UI explicitly reports live video already in use by another agent. The gateway has a cross-workspace capture admission fence; a separate agent would not remove it. The other viewer must close before the live regression and final endurance attempt. This is not a passing live release result. Evidence screenshot: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/release-isolation-before-cleanup.jpeg`.


## Matching-release fix deployed and verified live

Amsterdam now runs source `91783b0c11c99e91622db28656055bd77e41292b`, image digest `sha256:5161146985438d7da275146230b398ffb06873fd71c8eff10426809977d22b7f`, with installed and running binary SHA-256 `42e7d8d5c2a12347a4304a9e98dc51c6bc9585fe33dced61b6792eb80fe6f4c1`. Runtime verification confirmed health and preserved machine configuration. The existing cookie remained valid, so no additional login was performed.

The live regression passed in 24.4 seconds. Initial attachment took 10,978 ms. Both authored 1,200 ms press handlers completed with exactly one matching release: final counters were one click, two downs, two ups, zero held inputs, and zero errors. The observer retained no transient failure or Retry states; all eight reliable input packets used binary-v1. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/release-recovery-green-91783b0c1/`.

The normal-audio, mixed-input twenty-minute run started on this verified runtime in the isolated test workspace. Its result remains pending; the focused release result does not substitute for endurance acceptance.


## Final-candidate twenty-minute input acceptance passed

Playwright exited successfully on the verified `91783b0c1` runtime; the independent artifact audit also passed. The measured workload lasted 1,213.269 seconds (20 minutes 13 seconds), with 135 rounds and eleven scheduled resizes. Each minute contained mixed input. All final video-decoded counters matched: 135 clicks, 1,485 key/mouse downs, 540 ups, 135 drags, zero held inputs, zero errors, final text `@é`, and the expected final wheel accumulator of 300. Counts intentionally differ between down/up because the authored workload includes keyboard repeat. There were 11,837 outgoing events: 5,091 moves, 4,050 wheels, 405 mouse downs, 405 mouse ups, 1,348 key downs, 403 key ups, and 135 text events. All used binary-v1 WebRTC; no input WebSocket fallback or Retry/Resume was observed.

Evidence directory: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/endurance-91783b0c1/`. The Playwright pass is required alongside the private artifact auditor, which does not itself enforce every per-minute assertion. Native scrolling and remote hover effects require the separate regression suite: the endurance fixture validates trusted wheel deltas with default scrolling prevented and records outgoing hover movement.

Performance remains qualified. Interval-average video buffering was median 118 ms, p95 211 ms, maximum 311 ms; 57 freezes totalled 22.142 seconds over the run. Audio buffering was median 147 ms, p95 1,898 ms, maximum 3,208 ms, with substantial concealed audio. Audio degradation began immediately after server CPU steal rose from 0.3% to 36.4% and then 54.9% at 05:08:33–39 UTC. CPU steal is time the virtual machine wanted CPU but the host did not provide it. Memory remained available; client CPU showed no comparable sudden rise. This is a strong correlation, not proof of which audio stage stalled. No location or resource changes were made. Input acceptance is not a claim of flawless audio/video under shared-host contention.

Analysis: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/endurance-91783b0c1-analysis.json`. The workload server-log window contains no queue-expiry, dispatch-failed, or deadline-exceeded messages. It does contain Chromium background warnings and two duplicate incoming-frame timestamp warnings at 05:17:36. Server evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/endurance-91783b0c1-server.log`.


Installed Chrome 152 follow-up regressions also passed on the same runtime: native document scrolling/latest trusted hover (34.1 seconds), and single-viewer fullscreen handoff with return after actual close (1.7 minutes). Combined result: two tests passed in 2.3 minutes. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/chrome152-regressions-91783b0c1/`.
