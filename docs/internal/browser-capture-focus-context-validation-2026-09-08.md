# Capture preparation and focus cancellation validation — 2026-09-08

Scope: the cold session-resolution call in `prepareEncoderFrame`, and the existing-target focus operation in `bringAgentTabToFront`. This follows the shared startup and attachment cancellation changes. No installed browser process or profile was used.

## Reproduced behavior

The original preparation path called the legacy `Session` API while waiting for tab admission. Canceling its caller, or stopping capture, did not release that wait promptly; after admission became available it created a session for the canceled request.

The original focus operation ran in a detached worker with a target-only browser context. Caller cancellation could return while a pending command still completed later. Capture Stop did not cancel that command. A canceled caller queued for tab admission could focus afterward. Finally, the existence check accepted a session whose target was dead: resolving `Session` then created a new tab, despite focus's no-new-tab contract.

## Change

Cold preparation now passes its existing caller/Stop-linked context into `SessionContext`. Focus acquires manager tab admission under its existing five-second aggregate budget, snapshots the existing active target without creating one, and executes the browser command synchronously. Its target-derived command context also follows caller cancellation and capture Stop. A bounded Stop watcher is canceled and joined before return. Tab admission remains held until command completion, and temporary operation cancellation does not cancel the persistent tab.

## Evidence

- Initial run `32328` did not compile because the new fixture name collided with another test helper. Excluded from behavioral evidence; only the new helper was renamed.
- Corrected run `49089`: terminal exit 1, 8.419s. Six cancellation leaves failed as intended; missing-target and valid-selected-target controls passed. The dead-target leaf was invalid evidence because a repeated fake allocator cancellation consumed its five-second wait.
- Corrected dead-target-only run `79981`: terminal exit 1, 2.049s. A canceled child context models a dead target without double-canceling the fake allocator. The original focus path created one tab and dispatched focus twice.
- Initial focused green `71629`: terminal exit 0, 3.074s. All 13 leaves passed: nine new cases and four existing preparation budget/measurement/target controls.
- Final driver `56392` deliberately removes cold-resolution caller propagation, focus caller propagation, and focus Stop propagation. Each fault was caught by its relevant behavioral regression; source was restored after every fault. Driver terminal exit 0; restored focused race/shuffle passed all 13 leaves in 3.949s with no skips or race warnings.

Tests substitute only the browser protocol boundary and fake target creation. Manager admission, session resolution, capture lifecycle, context propagation, target selection, and focus orchestration remain real. They verify request cancellation and lifetime behavior, not measured video latency or a successful production video stream. Integrated runtime validation and full CI remain separate work.

GitNexus impact: `bringAgentTabToFront` LOW, three direct callers and eight total affected symbols, no indexed processes. The older index did not resolve `prepareEncoderFrame`; manual tracing confirmed the capture encoder startup closure invokes it before starting the encoder. Only the assigned preparation/focus regions changed.

Precommit `detect_changes --scope staged` (handle `1202`) was attempted in the isolated worktree and exited 1 because it is not registered and multiple repository indexes are available. Manual staged review confirmed exactly the two owned production regions, new focused tests, and this evidence document; whitespace checks passed. Parent performs integrated indexed review after cherry-pick.
