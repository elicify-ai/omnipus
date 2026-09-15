# Browser integration with updated release — 2026-09-15

Integrates release `fad606838d4175f517b4340cc6882f04fa5e6a75` into browser improvements starting at `97cf7e52fdb290d228774e1e32b542b27f7c6230`. The release includes the goal/judge and library work. Work was isolated from the original dirty checkout; main was not changed.

## Integration decisions

- Human mouse and keyboard input remains binary WebRTC, without a WebSocket input fallback. Navigation and tab commands retain the ordered WebSocket control path.
- Physical input acquires human control in the backend after input-source, picture, coordinate and action validation. Hover and matching release events do not acquire control. This avoids racing a separate takeover message against the first input.
- Preserve release handover semantics: taking control does not cancel chat. Viewer-scoped release atomically checks ownership and clears the stand-down state, so an old viewer cannot release a new holder.
- Notify the acting viewer on implicit takeover. Acquisition and release status messages check attachment lifetime and current ownership both at enqueue and socket delivery, rejecting obsolete queued status.
- Preserve Chrome 153 activation-before-attachment startup and failed-tab cleanup, alongside browser-branch cancellation and process cleanup. Remote Chrome allocators now establish the browser connection before creating and activating the first target, within the existing startup budget.
- Merge generated contracts to retain browser input/control messages and release handover, goal-outcome and library-change messages.

## Executed validation

- Browser UI and URL suite: 367 tests across 19 files passed.
- Chat handover store and local video diagnostics: 11 tests across two files passed.
- Full TypeScript type checking and frontend production build passed.
- Contract regeneration, AsyncAPI validation and wire-type lint passed. The viewer-only diagnostic sample is explicitly marked as internal, not a network contract.
- Focused browser, CDP pipe and gateway tests passed, including dedicated input, remote bootstrap, tab activation, cancellation, stand-down release and queued ownership status.
- Deliberate fault overlays removed dedicated takeover, bypassed the viewer ownership guard and disabled the queued acquisition-status guard. The corresponding regression tests failed for the intended reasons. Tracked source was never modified by these overlays.
- UI cancellation regression mutation was detected and the restored tests passed.
- Final bounded code review reported no remaining high-confidence blockers.
- Targeted Go race-detector tests passed for remote bootstrap, dedicated input takeover, viewer-scoped release and queued control status in both browser and gateway packages.

## Delivery limits

This integration has not been deployed or live-tested as a combined build. Earlier Amsterdam endurance results apply to the earlier browser candidate, not this merge. The last verified Amsterdam runtime was `3be6cea27`; this integration work does not deploy a replacement.

The previously observed audio degradation under CPU pressure remains unresolved. This merge does not claim an audio-performance fix. The next release gate is a combined live browser/handover smoke test before merging the browser pull request into release.
