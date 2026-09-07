# Browser command dispatch: plan and evidence

The authorized gateway lane completes queue cancellation through actual input/tab execution. Expectations derive from FR-004/011 and the parent instruction, not from current handler behavior:

- A queued command uses the manager and resolved tab set captured for its attachment. Detach or a newer attach cancels that attachment's queued and active work. A command never migrates into a replacement attachment, even if the replacement commits after the queue's initial admission check.
- Queue residence consumes the five-second command budget; it does not reset when execution starts. Adjacent pointer moves may coalesce, but discrete gestures retain order. Overflow resets the affected viewer rather than replaying uncertain gestures or silently losing a release.
- Input uses Live.InputContext. Tab operations receive the same caller cancellation and deadline, with no background mutation after a superficial timeout wrapper returns.
- Contextual tab APIs require a running manager and an existing attached tab set. Legacy APIs retain cold-start behavior. Successful tab lifetime must not inherit the command deadline. Canceled last-tab replacement leaves the old tab intact before commit.
- Command errors use an explicit generated operation-only status flag, preserving a healthy picture. Attachment/browser death remains a lifecycle error.

Tests will place barriers at actual handler/external browser boundaries and force attach replacement, caller cancellation, delayed queue admission, and tab-creation races. They will assert destination/state, command counts, deadlines and retained target lifetimes. At least three deliberate production mutations must fail named behavioral assertions. Chrome transport is the mock boundary; queue, attachment lifetime and manager state transitions remain real.

GitNexus impact completed before existing symbol edits: browserConnState LOW/0 callers; beginAttach LOW/1 direct/3 total; invalidateAttach LOW/2 direct/3 total; bindAttachment LOW/1 direct/3 total; clearAttachment LOW/3 direct/5 total; handleInput, handleControl and handleTabAction each LOW/1 direct/2 total. Affected flow is browser WebSocket ServeHTTP. New queue functions were not found in the root index; manual call-site review supplements that limitation. New contextual manager APIs are added separately so legacy implementations retain their behavior.


## Initial observed failures

The contextual-tab compatibility implementations failed five specified behaviors (exit 1, 5.881s): a precanceled switch still succeeded/moved the model; focus ignored caller cancellation; closing the last tab canceled it before replacement was ready and ignored replacement cancellation; a stale attachment tried to recreate a session; and concurrent switch commands mutated out of order instead of honoring the queued caller deadline. Successful target lifetime already passed and is retained as a regression guard.

The first gateway command did not execute assertions because this isolated worktree lacked ignored embedded SPA assets. Existing built assets were copied read-only from the root worktree solely to compile gateway tests; no new frontend validation is claimed. The corrected gateway run failed both input and tab attachment races (exit 1, 8.416s): after the controlled handler barrier, each emitted an error for `replacement-chat`, proving the old command used the replacement attachment. The queue-age test passed.

Additional impact checks: BrowserManager struct LOW, one direct caller; browserWSConn.close LOW, two direct callers and ServeHTTP flow; the shared input converter LOW, two direct/seven transitive symbols across WebSocket and optional data-channel input.

The manager uses a separate per-tab-set command gate, acquired without holding its mutex and held through callbacks. It must not hold the live input gate across callbacks, because target/viewport publication acquires that gate. Parent owns bounded tab/frame fan-out and generation integration. The encoder binds an exact Chrome tab ID, so this lane does not infer that foregrounding an unrelated tab changes an existing capture's source.

The additional red run failed all five intended behaviors (browser package 3.183s; gateway package 7.925s; exit 1): foreground rejection was reported as success; attachment replacement did not cancel admitted work; closing a real WebSocket connection left its reader blocked; the shared converter dropped capture identity; and an ordinary input failure lacked the operation-only flag. The reader test leaves the peer open deliberately: cleanup must not depend on peer cooperation.

The implementation pins immutable attachment identity at queue admission and combines its cancellation with the original queue deadline. `beginAttach`, invalidation and clear cancel that lifetime before publishing a replacement. Actual input and contextual tab operations receive this context. Closing the local WebSocket also closes its transport, unblocking the reader and its detach cleanup. Tab model changes already committed before a foreground failure remain committed and produce an error rather than a replay or rollback.

The first race/shuffle regression pass completed the browser package successfully (34.770s), including the new contextual commands and existing tab/session/callback tests. Gateway tests caught two obsolete throttle fixtures: they created a control-only view but never attached its viewer, which the earlier membership fix correctly rejects as benign. The fixtures now leave the live-view registry empty to produce a real deterministic dispatch failure; their exact repeat-message and navigate-retry assertions remain unchanged. All new command lifetime/queue/typed-status and real reader tests passed in that run, with no race report. Gateway rerun is required before claiming the regression selection green.

Additional test-only impact checks were LOW with zero callers/flows: the legacy open-tab cold-launch expectation and the two throttle fixtures. The interactive open expectation now checks the approved existing-attachment boundary; legacy tool OpenTab cold startup is unchanged.

The five-second tab budget governs queued/active browser work. Pinned chromedp v0.15.1 separately waits for target detach/close cleanup using a one-second background timeout; canceled target cleanup can therefore add up to that library budget. Existing synchronous tab notification delivery is a separate integration limitation, assigned to the bounded latest-state WebSocket delivery lane. The new manager gate currently serializes contextual UI operations; adopting the same gate in legacy tool tab operations is assigned to a later bounded commit so cross-path callback ordering is also serialized.

After correcting those fixtures, the gateway-only race/shuffle regression selection passed (exit 0, 21.008s), including command lifetimes, queue ordering/capacity, reader responsiveness, input throttling and existing browser WebSocket authentication/control/tab/viewport regressions. The unchanged browser package's race/shuffle result above remains the corresponding manager regression evidence.

## Mutation verification and final check

Four isolated production faults each caused the intended behavioral assertion to fail (each exit 1), and the driver restored the original file in a `finally` block after every probe:

| Deliberate fault | Test that rejected it |
| --- | --- |
| Stop canceling the previous attachment | `TestBrowserAttachmentLifetimeEndsBeforeReplacement` |
| Leave the WebSocket transport open on connection close | `TestBrowserConnectionCloseUnblocksReaderWithoutPeerCooperation` |
| Keep successful tab creation tied to caller cancellation | `TestLiveTabCommandSuccessfulNewTargetOutlivesCaller` |
| Give queued work five fresh seconds at execution | `TestBrowserCommandQueueAgeConsumesExecutionBudget` |

The final restored focused selection passed (exit 0): browser 3.026s, gateway 13.047s. Together with the race/shuffle runs above, this verifies the implemented lane at its real queue/socket/state boundaries and mocked Chrome command boundary. Integrated browser runtime and CI validation remain parent-owned; this document does not claim those have run.

Pre-commit `detect_changes --scope staged` was attempted in this isolated worktree and failed because it is not a registered index among the multiple registered repositories. Selecting the root alias would inspect a different worktree's diff, so that was not presented as coverage. Manual staged review confirmed only the nine expected command/manager source, test and evidence files; `git diff --cached --check` passed. Parent owns the final integrated GitNexus change map.
