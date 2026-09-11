# Interactive browser input implementation plan and evidence

This lane implements the shared-input and cancellation requirements in the parent browser-improvements specification. The browser command boundary remains real in unit tests except for the external browser-control executor, which records or deliberately stalls commands.

Planned expectations before implementation:

- Caller cancellation stops validation, geometry acquisition, queue admission and command execution. The tab lifetime remains intact.
- One interactive operation uses at most two seconds, or the shorter configured page timeout/caller deadline. This is a responsiveness ceiling, not a promise that every website loads within two seconds.
- Navigation, history and reload acknowledge the browser command without waiting for full document loading. Destination protocol errors remain errors.
- State-changing key/button operations serialize across viewers. A departing viewer releases its own holds; a release never clears another viewer's same-key or same-button hold.
- An already-detached viewer cannot deliver a late queued action. Detach cancels its active commands.
- Successful presses are tracked. A timed-out/cancelled press has uncertain delivery, so it may require conservative release; this must not resend the press or typed content.
- Layout settling has one 600ms budget; each read and poll consumes only the remaining budget.

Initial regression cases cover caller cancellation, two viewers sharing Shift, explicit release while another viewer holds Shift, detached stale input, and the layout read budget. Follow-up cases cover real protocol command shape, destination errors, held mouse buttons, cancellation while waiting for another input, concurrent detach and press completion, and tab changes. Mutations must remove the new cancellation/ownership/deadline safeguards and make these tests fail.


## Observed validation

The initial five regression tests ran against the old input path and failed (exit 1, 13.698s): caller cancellation was ignored, final-owner detach omitted key-up, one viewer released another's Shift, detached input still dispatched, and a layout read requested five seconds despite a 600ms settle budget. Four additional cases were added before implementation but were not present in that original red run; they are not counted as observed reds.

The first implementation passed the focused new and related existing input, viewport, detach and rebind tests (exit 0, 9.997s).

Follow-up review produced three further observed reds (exit 1, 3.449s): a failed explicit key-up did not retry before the next text command; a non-protocol transport failure lost an uncertain press; and caller cancellation during coordinate mapping was misclassified as missing viewport and started a three-second backoff. All three were fixed. Only an explicit CDP protocol error is treated as definite press rejection; transport failure can have uncertain delivery. Failed owned releases remain pending until cleanup succeeds, with no replay of presses or typed content.

The expanded focused suite passed (exit 0, 5.214s), including actual navigation actions with a protocol-boundary executor that sends acknowledgment but no document-load event, preservation of destination errors, cancellation while waiting on the command gate, detach during an in-flight press, mouse drag button masks, release under rate pressure, and cleanup against the original tab after target retirement. The external browser executor is the mock boundary; input validation, ownership, cancellation and action construction remain production code. These tests are not an end-to-end network/browser measurement.

Command used for focused green: `go test -p 1 -tags 'goolm stdjson' ./pkg/tools/browser -run 'TestLiveInput|TestLiveView_DispatchInput|TestLiveViewRegistry_(Input|SetViewport)|TestBuildInputAction|TestLiveView_Detach|TestLiveView_RebindWatch' -count=1`.

## Integration boundary

`InputContext` enforces viewer attachment, while the existing internal `dispatchInput` compatibility method remains usable by package tests. Viewer input remains shared; the presentation control indicator is not an authorization gate. One operation uses a two-second budget, tightened by configured page timeout and caller deadline. The gateway's five-second queue deadline includes time before this operation begins. Expiry is an operation failure, not evidence that the video/browser died.

The input command gate serializes command delivery and pressed-state ownership. Attachment bookkeeping uses the existing view mutex; browser commands never hold that mutex. Detach cancels admitted viewer requests and gives independent cleanup up to two seconds. Tab retirement cancels old-target requests and uses one bounded cleanup worker. Failed cleanup is logged and retried before later input. At most 256 distinct holds are tracked per viewer.

Capture-generation invalidation and the gateway's queue integration are parent-owned follow-up work. This commit does not claim to prevent dispatch against a stale displayed frame until that integration is complete.


Four deliberate mutations were each caught by their designated behavioral test: remove caller cancellation propagation; allow one viewer to release another's Shift; omit failed-release retry; and restore the five-second per-read viewport timeout. The driver required a named test assertion failure, not merely compilation failure, and restored production after every run.

GitNexus impact before changing existing entry points classified input, detach, tab change, viewport mapping and settling as LOW (1–2 direct callers each, up to seven transitive symbols in browser/gateway flows). The final root index independently reported viewport mapping LOW, one direct/four total symbols. `detect_changes --scope all` in this isolated unregistered worktree failed because multiple indexed repositories require an explicit repository; selecting the root alias would inspect the parent's worktree, not this diff. Parent owns integrated mapping after cherry-pick. Manual scope review and `git diff --check` accompany this lane's commit.


Two final held-state regressions failed as intended (exit 1, 1.916s): an unmappable mouse-up left an earlier press held; and a key-down with `Key=A, KeyCode=65` followed by `Key=a, KeyCode=65` retained stale ownership. The fixes preserve pending cleanup when a release fails before delivery, and use physical Code, then numeric KeyCode, then Key as the identity preference.

## Actual Chrome mouse-release semantics

An isolated, fresh-profile CDP-pipe probe used the installed Chrome 151.0.7922.77 bundle in headless mode. It created an `about:blank` page containing a button, registered real DOM event listeners and dispatched a left-button press at (50,50). Production Chrome and its profile were not touched.

- A normal release at (50,50) generated pointer-up/mouse-up and button click.
- Explicit release clickCount zero still generated the button click, with detail 1.
- Release at (-1,-1) targeted HTML and still generated a document click. When the button called `setPointerCapture`, the same outside release generated the original button click.
- `Input.cancelDragging` before release still generated the click.
- Releasing button `none` generated no release event and left `hasPointerCapture(1)` true; it does not clean up the held pointer.

Consequently, cleanup sends normal releases at the last successfully dispatched coordinates of the shared pointer. This terminates held input and can complete a page click/drag action. It is not universally click-free cancellation. No unverified click-count, outside-coordinate or button-none workaround was added. The parent confirmed that FR-004 requires normal matched releases, not universal suppression of arbitrary page handlers; normal release behavior is the implemented contract. The same concern applies to normal operating-system releases, and the probe must not be presented as proof that all page event side effects can be suppressed.


The isolated Chrome probe also repeated a successful release, both with and without pointer capture. The first release generated one native click; the second generated pointer-up/mouse-up with detail zero and no second native click. Arbitrary page mouse-up listeners still run. Successful backend cleanup removes its ownership record and therefore sends no second release; a retry is needed only when prior delivery was uncertain.


The ownership follow-up ran red (exit 1, 2.498s): unowned key/mouse releases reached Chrome, and cleanup used stale coordinates after another viewer moved the shared pointer. Explicit releases now require the sender's own previously accepted or uncertain press; shared-pointer moves update every held button's last confirmed position. Repeated successful detach cleanup already passed. The existing geometry test for mouse-up now first dispatches the matching press and clears recorded setup commands, preserving its original coordinate assertions. GitNexus classified that test change LOW, with no upstream callers or flows.

A final explicit `detect_changes --scope all --repo <startup-worktree-absolute-path>` attempt also failed with repository-not-found, confirming the isolated worktree is not registered. Integrated root inspection remains required after cherry-pick.


Final focused validation passed with the race detector and shuffled execution (exit 0, 6.721s): `go test -race -shuffle=on -p 1 -tags 'goolm stdjson' ./pkg/tools/browser -run 'TestLiveInput|TestLiveView_DispatchInput|TestLiveViewRegistry_(Input|SetViewport)|TestBuildInputAction|TestLiveView_Detach|TestLiveView_RebindWatch|TestLiveView_RescaleToCSSViewport' -count=1`. This includes all later ownership, coordinate-failure and physical-key regressions. `git diff --cached --check` passed. Full integrated browser/gateway and live UI validation remain parent-owned.
