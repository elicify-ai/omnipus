# Browser viewport admission validation — 2026-09-07

Viewport requests previously bypassed the tab-command and input gates. A tab could change during resizing, and a canceled caller could still resize a target, retry an uncertain resize, or continue through scale and measurement stages. Eight behavioral reproductions failed against the old implementation (2.439 seconds, exit 1).

## Contract and implementation

`SetViewportContext` operates only on an existing attached view; it does not start or recreate a browser session. Both it and the legacy `SetViewport` enter `applyViewportContext`. Async target reapplication reaches the same admission through the legacy `applyViewport` wrapper.

Admission order is manager tab-command gate, live input gate, then the existing viewport mutex. No manager mutex is held during browser work. Every apply path acquires input admission first, so another viewport apply cannot hold the viewport mutex while this caller waits on it. Waiting for the outer gates is cancelable. After waiting, the selected target must still match the manager's current target before the remembered request or browser state changes.

Browser work uses a child of the selected target context, preserving its executor values. Caller cancellation reaches this child. The original target context remains separate for cache identity checks. Existing stage budgets remain five seconds for each bounds or scale operation and 600 milliseconds for each settle pass. A legacy operation's overall budget is 21.2 seconds, the sum of its existing maximum stages (initial bounds, one retry, scale, one compensation, and two settles). Admission uses this same budget. A caller's earlier deadline shortens it. This is a stage-budget bound, not evidence of meeting the two-second responsiveness acceptance target; synchronous observer work is not independently interrupted by the context.

Canceled admission issues no browser commands and preserves existing verified geometry. Cancellation during admitted browser work stops later stages and invalidates cached geometry while admission is still held. The result remains `applied=true` if the initial bounds were acknowledged, with the cancellation error returned alongside it. A first bounds operation canceled without acknowledgment returns false: its eventual browser effect is uncertain and is not replayed. A scale-stage timeout alone remains cosmetic while the caller is alive.

## Evidence

The initial red covered pre-cancellation, manager/input gate waits, obsolete target rejection, in-flight bounds cancellation without replay, exclusion of tab changes during browser work, and cancellation at scale or measurement. Independent browser commands are faked only at the CDP boundary; registry, admission, and resize orchestration remain real.

Two existing target-reapplication fixtures now identify fake targets using distinct inherited context values instead of requiring pointer identity between a target context and its cancelable child. Target distinction, scale, and final geometry assertions are preserved.

The first focused integration run (17.997 seconds, exit 1) passed those eight original negatives and the selected existing resize/retry/reapplication regressions. A new compensation-boundary test caught one missing cancellation check in the first implementation: 30 measurements had run before cancellation, and a 31st started afterward. The implementation now checks cancellation after compensation acknowledgment before starting its measurement stage. Additional tests cover manual cancellation without a deadline and a target changing while the resize waits for admission.

The corrected focused selection passed in 18.192 seconds. Four separate behavior faults were injected and each produced the intended assertion failure: disconnect caller cancellation from the running CDP context, bypass manager admission, skip target identity validation, and bypass input admission. Sources were restored after every fault. The final restored selection passed with `-race -shuffle=on` in 21.457 seconds, without race warnings. It includes the context boundary cases plus existing resize/retry/compensation, viewport settling, stale measurement, tab reapplication, capture-basis, and scale-cache regressions.

GitNexus impact was LOW for `applyViewport` (two direct callers, six affected symbols) and `SetViewport` (one direct caller, three affected symbols). The two adjusted fixtures had zero callers/processes. The staged `detect_changes --scope staged` attempt exited 1 because this isolated worktree is not registered and GitNexus required selection among other indexed repositories. Selecting the root index would inspect another checkout, so it was not presented as worktree verification. Manual staged review confirmed exactly the expected six files, no capture/gateway/manager changes, and clean whitespace. Integrated index verification remains parent-owned.

## Integration boundary

Capture-generation invalidation, recapture callbacks, health reporting, gateway caller wiring, and integrated live-instance performance validation are owned by the parent integration lane. This change creates the admission boundary for those hooks; it does not claim that they have been integrated or tested here.
