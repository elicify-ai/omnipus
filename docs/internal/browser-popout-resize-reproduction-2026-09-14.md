# Pop-out resize reproduction — 2026-09-14

The user reports broken resizing after opening the browser viewer in a separate client tab. Reproduction ran against Amsterdam's existing candidate from the Mac using headless Chromium, through the actual Pop out button. No production changes or deployment were made.

Two runs covered display pixel ratios 1 and 2, embedded-to-popout handover, window sizes 1000×700, 1700×1100, 800×900 and 1440×1000, plus 1200×800 with address-bar focus and then viewer focus. The second run also exercised 24 rapid size changes. All 17 recorded settled observations showed dedicated input ready, advancing video and no alert. This checks readiness, not successful delivery of a physical gesture after every resize.

A persistent wrong-size or failed-input state was not reproduced. Normal-density requested-size to matching recovered-picture signals took 1.263–2.288 seconds for subsequent resizes, in addition to the client debounce/settle interval. Smaller encoded video dimensions are expected: the capture uses a proportional pixel budget and 12-pixel rounding. The inspected large-window screenshot filled the viewer with the current YouTube page; no navigation or page action was sent.

Independent read-only review found both layouts share the same resize handling. It also found a conditional interference path: multiple viewers sharing the workspace/operator tab can change the shared viewport when no explicit controller exists. Resize operations serialize, but the latest applied size wins. An explicit controller blocks another viewer's resize. Different agent labels do not establish distinct browser targets. This is a code-supported hypothesis for the user's symptom, not a reproduced cause. The test used the configured Browser UAT Test agent but could still share the operator tab; it must not be described as an isolated remote browser.

The exact client browser and visual failure (stale size, stretched/cropped picture, or lost input) were requested. Safari/native-window behavior and competing viewers were not exercised in these runs. No claim that the user's report is resolved follows from these samples.

Evidence directories:

- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-resize-repro`
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-resize-retina-repro`

Each contains timestamped size/video/readiness observations, selected browser WebSocket frames, screenshots and a Playwright trace. Server snapshot: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-server-before.log`.

## Requested correction

The user requires Pop out to hand over the existing viewer, not leave two active surfaces. The originating tab must unmount its docked viewer and initiate input/media/socket cleanup before the new tab loads the viewer. Only that originating tab may restore the dock after its own pop-out closes. Reload is not close. While its pop-out remains open, another Open browser request must not mount a competing dock. A blocked pop-out must leave a usable dock.

The existing origin-wide `popout-closed` broadcast cannot distinguish reload from close and is not scoped to a parent. Replace that restoration mechanism with ownership of the actual opened window. Open a trusted blank tab during the user gesture, sever its opener, synchronously tear down the dock, then navigate it to the same-origin viewer. The owner checks actual window closure to restore its panel; unrelated tabs receive no restore broadcast. This concerns the originating app/pop-out pair, not a server-wide ban on other authenticated clients.

Focused tests derive from these user-visible lifecycle requirements. Live verification must inspect old-viewer detach/close initiation before new attach, resizing the old app without viewport traffic, reload without re-docking, and native close restoring only the owner. This does not claim an ordering acknowledgment from the server across separate sockets. No gesture transport, encoder or tool-policy change is part of the correction.

## Implementation checks

The owner now retains a handle to its own trusted blank pop-out, severs the child's opener, and flushes the dock unmount before navigating the child. Synchronous store interception and a render guard prevent any Open browser affordance from mounting a second dock. The owner monitors actual window closure every 250 ms; reload keeps that window alive and does not restore. Closing or reloading the parent ends its ownership and closes its child. The global dock is suppressed on the full-screen route itself. Blocked, throwing and unnavigable popup creation preserve or restore the dock with a visible explanation.

Twenty-four focused lifecycle tests passed, following observed behavioral failures for unowned close broadcasts and blocked/throwing popup creation. Independent review found no blocker. Scoped production/acceptance TypeScript and the production SPA build passed. Final mutation and live candidate results follow when available. The first live harness run was interrupted because its URL check incorrectly expected path routing; the app uses hash routing. Both test waits were corrected; that attempt is retained and is not counted as a product failure or a pass.


## Deployed result

Amsterdam runs source `7ebbae280f387c6bdc0fde6e0f22beb1ca0fa17a`, image `registry.fly.io/uat-omnipus:browser-input-7ebbae280`, digest `sha256:a66805f788a6c649d817093584d9cf7043919621f08c044c24aa58593ed74601`. Installed and running binary SHA256 both match `ff19d4395d7a0b374575ef6dee56b2da52364e0c9a2dc646657a24ffcf1b878e`; machine configuration and health were verified. Main and the installed Mac app were untouched.

The corrected live test reproduced a real old-version failure: after successful popout handover, Open browser in the source mounted one dock when zero were required. The same test passed on the new candidate in 1.6 minutes. It verified old-client detach/socket-close and both peer-close initiation before the new attach, no viewport updates when resizing the original app, no competing dock on Open browser, popup reload without re-docking, and owner-only restoration after both the product Close button and native tab close. The unrelated app tab made no browser attachment. There were zero page errors and no observation overflow. The test does not assert delivery of a new physical gesture after every handover or certify Safari; input readiness and actual video are checked.

Live milestones UTC: popout ready 14:22:03.402; reload retained ownership 14:22:19.750; product-close return ready 14:22:30.124; native-close return ready 14:22:57.397. Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/popout-handoff-candidate-7ebbae280`. Failed baseline and interrupted harness evidence remain alongside it under source `92b5f1fd6`.

All three deliberately faulty variants failed their intended assertions: removing synchronous cleanup, bypassing actual-closure checks and allowing an owned source to reopen. Isolated copies were used; final normal-source tests passed 24/24. Focused checks only; full CI was not rerun.

## Follow-up screenshot: inset picture remains unverified

The user supplied `/Users/danielpiatkowski/Desktop/Screenshot 2026-09-14 at 10.21.32 PM.png` (2880×1800). It shows a Chrome pop-out for Mia/session `session_01M2F8FXJ61CGWEYMXAED5VF2H`, full-width toolbar and a page image centered with substantial black borders on all four sides. This visual defect is confirmed by inspection; the previous handover acceptance did not prove the picture fills its container.

A fresh viewer opened that exact session at 1440×810 CSS pixels and pixel ratio 2. Its video and input container both measured 1426×718; decoded video measured 1344×672 and visibly filled the container apart from small aspect-rounding margins. Resizes to 1200×800, 1700×1000 and back also filled, with input ready and no alerts. Raw decoded frames and screenshots are retained in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-mia-sizing-repro`. An earlier attempt stopped at expired test authentication and is retained separately; it is not a sizing failure.

Read-only review found no capture compositor that deliberately inserts such borders; ordinary object-contain aspect fitting cannot explain large borders on all four sides of a full-sized video element. The unresolved distinction is an undersized video element versus padding already inside the affected stream. Correct behavior in a fresh viewer does not establish the affected existing tab is fixed. Browser zoom, stale page code and competing capture state remain hypotheses, not diagnoses.

Read-only AppleScript inspection found only an Omnipus sign-in tab in the running Google Chrome, not the affected full-screen tab. The user was asked to leave that affected tab open on this Mac so its actual element rectangle, computed sizing and decoded frame can be inspected. No new production change or deployment was made for this follow-up.

A two-viewer experiment confirmed shared sizing: the first viewer's container stayed 1426×718, but its decoded image changed from 1344×672 to 1212×744 when a second 1000×700 viewer attached, then to 1308×696 when that second viewer grew to 1700×1000. Both remained input-ready. Closing the second viewer left the first on the last shared geometry; a two-pixel window change was below the existing eight-pixel resize threshold and did not reclaim it. This produces ordinary aspect margins and confirms independent viewers can compete; it did not reproduce the screenshot's large all-four-side inset. Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-two-viewers-sizing-repro`. Test-owned viewers were closed afterwards.

## Exact screenshot failure reproduced through Google result click

At the user's direction, the full-screen Mia viewer searched Google for `youtube` through its address field, then clicked the visible YouTube search-result link using the real WebRTC pointer path. The link opened a new remote YouTube tab. This reproduced the all-four-side inset shown in the user's screenshot.

At 15:54:36.340 UTC the viewer container and video element still measured 1426×718 CSS pixels with object-fit contain. The decoded frame changed from 1344×684 on Google to 1500×600 after the click. Its raw pixels contained black left/right strips; fitting this wider frame into the correctly sized element added top/bottom space. The server announced recovered geometry 1426×575 for new target `DC20C000BEF626DB281F2E828351A765`, despite the unchanged 1426×718 client container. Thus the inset is confirmed as incorrect new-tab capture/geometry, not merely an undersized frontend video element. The exact cause of that geometry discrepancy still needs tracing.

Subsequent actual window resizing restored correct fit: 1186×708 container → 1236×732 decoded video; 1686×908 → 1308×696; original 1426×718 → 1344×672. Input remained ready throughout the sampled states. The earlier open-existing-tab tests missed this click-created-tab transition.

Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-google-youtube-repro`, including `youtube-after-click.png`, raw `youtube-after-click-video.png`, geometry/control events and a trace. Test viewer closed normally. No production fix has yet been applied for this newly reproduced transition.

## New-tab convergence correction

The reproduced server warning confirms the first correction was attempted: requested1426×718, compensated outer bounds1426×861, but final measured CSS viewport still1426×575. The 143-pixel shortfall matches Chrome's documented decoration offset. Merely fitting the existing video element cannot repair pixels captured against this incorrect shape.

For newly active targets only, the viewport path now checks its measured postcondition before requesting capture. If width/height still differ from the requested viewport beyond the existing tolerance, it performs one full reapplication and fresh measurement inside the same admission and deadline. It never substitutes requested dimensions for measured values, and a superseded or canceled target cannot continue. Ordinary manual viewport updates retain their existing handling of legitimate window-size limits. Persistent failure is bounded, not an unbounded resizing loop.

Regression cases derive from the reproduction and lifecycle requirements: initial compensated shortfall then convergence, persistent mismatch with no recapture from this path, cancellation, and target supersession. Initial tests failed on the old implementation for missing reapplication; one initial cancellation fixture leaked a fake Chrome task and was corrected to cancel the caller instead. Focused new-target, existing viewport, tab-change and document tests passed together in15.821seconds. Final review, mutation and deployed reproduction results follow when complete.

A pending-target fence is installed synchronously when viewport reapplication is scheduled. Document completion, explicit capture refresh and encoder preparation all validate fresh measured geometry before publishing it. A later matching measurement can clear the fence; manual resizing retains the pre-existing measured-clamp policy. Review identified and closed refresh/encoder bypasses before deployment.

Final focused new-target, viewport, document and encoder-preparation regression tests passed in 15.878 seconds. The refresh and encoder regressions first failed on their unfenced paths. A document recovery fixture initially omitted the new navigation start event, so its unexpected commit was correctly rejected as stale; the fixture now emits the real start-then-commit sequence. No production stale-event protection was relaxed. Independent final review found no blockers.

Six deliberate fault variants were rejected: bypassing size convergence, allowing extra retries, accepting unconverged capture, omitting the synchronous fence, bypassing document validation and retaining the fence after legitimate manual clamp. Restored-source regression checks passed. Live verification remains pending at this source commit.

## First live convergence candidate: failed

Amsterdam source22524f822 passed installed/running binary verification with unchanged machine configuration. The first image lookup returned a transient manifest404; retry deployed the identical image. Exact Google→YouTube live retest failed at16:53:12UTC before any manual resize. A new target existed but never recovered its picture; the prior Google frame remained visible at16:53:18. The guard prevented undersized capture but did not fix window sizing.

Server evidence: first bounds pass remained1426×575 after compensation. The second pass briefly logged1426×718, but immediate fresh measurement again saw1426×575. This rules out declaring the reapplication sufficient; the narrow next correction uses Chrome’s content-area sizing operation instead of repeating outer bounds. Failed evidence retained at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/popout-google-youtube-failed-22524f822`.

An isolated Chrome152.0.7977.82 process on Amsterdam reproduced the exact143-pixel difference with the deployed headless mode and DPR2: Browser.setWindowBounds1426×718 yielded inner1426×575 and outer1426×718 across six samples over one second. Browser.setContentsSize1426×718 instead yielded inner1426×718 and outer1426×861 immediately and throughout the same sampling interval. The owned process and private profile were removed; no live session target was changed. Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/contents-size-probe-linux.log`.

## Direct content correction

The ineffective second outer-window pass is replaced with one Browser.setContentsSize operation for the measured mismatch on a newly selected target. Normal viewer resizes keep their existing path. Capture still requires fresh measured geometry. If CSS dimensions differ only because of scrollbars, fresh inner dimensions must match the requested size and a second CSS sample must remain consistent with the capture sample; the target and capture identity are revalidated after the additional reads. No fixed scrollbar width or wider generic tolerance is introduced.

The actual-command regression first failed with zero contents-size calls. Focused contents/convergence, scrollbar, viewport, document and encoder-preparation checks passed together in15.285seconds. Independent review found no blockers. The initial GREEN attempt encountered the old fake executor’s unhandled new measurement action; the test boundary was updated to provide actual inner/client values without changing the production requirement.
