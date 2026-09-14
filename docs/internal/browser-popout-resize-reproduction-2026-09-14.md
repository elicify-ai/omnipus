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
