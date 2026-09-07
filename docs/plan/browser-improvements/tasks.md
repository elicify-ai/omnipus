# Browser improvements delivery ledger

Integration branch: `browser-improvements`. Base: `fbcbc5edc9845f1fbecb01b423f15d09fe405f5c`. `main` is explicitly out of bounds; no checkout, merge, commit or push to it.

## Wave 1

- [x] Fetch current release and fast-forward the new integration branch to the installed revision.
- [x] Create three isolated worktrees with disjoint ownership.
- [x] Write requirements, scenarios, dependencies and acceptance targets.
- [x] Startup early-exit/cancellation fix integrated as `9f1cb6a9c`; worker reports race tests, shuffled order and four caught mutations. Independent final review/runtime acceptance still pending.
- [x] UI continuous shared input, one ordered socket, pressed-state cleanup, focused desktop resizing integrated as `f7b7714cc`. Worker ran 503 browser tests, corrected one obsolete expectation and reran its suite green; final typecheck, lint and mutation checks passed. Frame-generation gating and independent final review remain pending.
- [x] Media sizing, demand-aware adaptation, generation-fenced forwarding and timestamp translation integrated as `364969cb1`. Final 25 capture tests and eight focused relay tests passed without skips; worker reports seven JavaScript and three Go mutations caught. Real H264/Opus transport replacement is measured; audible synchronization and end-to-end geometry mapping remain pending.
- [ ] Gateway bounded ordered command queue. Real socket regression failed before reader dispatch was moved and passed afterward for both input and tab actions; queue ordering/bounds/cancellation tests passed. Production cancellation wiring and mutation audit remain pending.
- [x] Live engine caller cancellation, bounded interactive commands, held-state ownership and remaining viewport deadline integrated as `2a81a8db1`. Worker reported twelve observed behavioral regressions, four caught mutations and a final focused race/shuffle pass. Gateway lifetime wiring and final integration review remain pending.
- [x] Capture heartbeat ownership core committed as `212a75a03`: five behavioral failures reproduced, three mutations caught, restored focused suite passed. The socket adapter uses the current authenticated binding epoch.
- [x] Watchdog policy and focused regressions: the old policy failed both healthy-idle preservation and explicit-stage recovery. The replacement policy passes those cases, wire health mapping, stage classification and existing focused liveness tests; four deliberate faults were caught, and the restored suite exited 0 in 9.474 seconds. The obsolete silence-only shutdown expectation was replaced with idle preservation and non-destructive recovery requirements. Production frame authorization integration and complete runtime recovery acceptance remain pending.

## Wave 2 — frame/target correctness

- [ ] Resolve incremental independent health review: retain finite-repaint evidence across fresh complete samples; require progress after loss before completing recovery; reject superseded observations atomically. Separate relay ingress from partial viewer-write failures. Earlier focused green results did not cover these timelines.

- [ ] Capture generation changes on actual target or CSS geometry changes; peer reuse is restricted to unchanged target/geometry until exact frame correlation can be proven.
- [x] Verify exact source identity bridge on installed Chrome 151: read-only `chrome.debugger.getTargets()` mapped duplicate-URL page IDs to distinct tab IDs; capturing requested red tab while blue tab was foreground produced a red frame. Implementation requires documented debugger permission in the managed extension, without debugger attachment.
- [ ] Immutable offer generation/target, first-forwarded RTP timestamp boundary strictly after every old-forwarded timestamp, generation fence at relay writes.
- [ ] UI waits for actually presented frame boundary and includes displayed generation on input. Server rejects stale-generation actions while preserving cleanup releases.
- [x] Standalone UI presentation gate integrated as `e500d2309`: 37 observed failing tests before implementation, 37 passing afterward, three effective mutation families caught. Production UI wiring is still pending.
- [x] Server generation tracker and capture-session adapter integrated as `c0a8fb833`: tracker red/green plus three caught mutations; adapter red/green plus two caught mutations for replacement identity and stopped captures. Production startup, transition and input wiring remain pending.
- [x] Shared generation/health contracts committed as `4c2de19d3`; capture-session/viewer-offer identity as `cd6a3c64c`; ingest-answer correlation as `431bfac6a`; operation-only errors and confirmed CSS geometry as `d672e2df4`. All corresponding generator/typecheck runs completed successfully.
- [ ] Capability-tested fallback for viewers without received-frame RTP timestamps; no arbitrary delay or generic next-frame unlock.
- [ ] Avoid redundant stale tab-API polling when verified current-generation geometry is available; measure recapture against the two-second target.
- [ ] Integrate all lanes and resolve cross-component failures.

## Current integration checkpoint

The unchecked wave-level items above remain open until their complete production
behavior is verified. The following integrated work narrows those gaps:

- Capture startup measures the selected target and its actual geometry before
  injection (`563022dfe`). Focused cancellation, target-retirement, mutation, and
  race checks passed; cold shared-process registration remains a separate limit.
- Authenticated ingest offers retain socket, frame, and offer ownership through
  negotiation (`d6556cfb2`). Seven deliberate faults were caught and restored
  adapter tests passed. The gateway reader integration is committed as
  `a55f42757`: heartbeats and disconnects remain responsive during negotiation,
  offers have a bounded queue and receipt-based deadline, and answers preserve
  exact identity. Eight gateway mutations were caught; restored focused tests
  passed. Capture-only runtime and reader race verification remain pending.
- Input requires the committed capture to match the receiving target ID and
  exact target context (`625146088`), in addition to source lifetime and frame
  generation checks (`4fbc98bf2`). Four wrong-target deliveries were reproduced;
  three mutations and restored focused race tests passed. Nil-capture admission
  and cross-panel capture allocation are still open.
- Session removal retires pending target operations and prevents delayed
  completion from reviving or replacing the wrong session (`bc1e33752`). Nine
  behavioral failures, four caught mutations, and restored focused race/shuffle
  verification are recorded. Viewport admission is the next startup-worker lane.
- The complete relay race suite passed 159 tests/subtests with zero skips and
  zero race warnings (`62ab23e6d`, evidence). This covers connection lifetimes,
  offer admission, separate ingress/write-failure statistics, and bounded input
  overflow cleanup together. It does not establish viewer presentation, audible
  synchronization, remote performance, or exact-branch UI acceptance.

Current parallel ownership: startup worker handles viewport admission; media
worker handles context-aware capture constructors and viewer-offer adapters;
UI worker handles authenticated gateway ingest and its capture fixture. Root
handles health/binding integration, capture transitions, and final assembly.
Go test batches remain serial to respect the machine's resource constraints;
source work proceeds in the isolated worktrees.

## Release evidence (all pending)

- [ ] Independent intensive `/review` of complete release-base diff; seven review lenses, every finding resolved and rechecked.
- [ ] Exact branch build and direct browser fixture testing, including ten-minute idle and mixed-input runs and measured latency.
- [ ] Native installed macOS web-app focus/sleep/wake verification; preserve user profile and production data.
- [ ] Local and remote connection behavior, audio playback/synchronization and failure recovery.
- [ ] Push `browser-improvements`, run applicable CI on that exact commit, fix failures without suppressions or bypasses.
- [ ] Final requirement-by-requirement completion audit; no commercial-readiness claim before all required evidence exists.

## Evidence qualifications

Mach rendezvous errors occurred after successful isolated Chrome launches during teardown; they are not independently the startup root cause. Initial UI/data-channel probes were characterizations, not reproductions of the user's exact stall. New real socket tests do reproduce reader blocking. H264 track replacement and Opus packets are measured, but audible synchronization is not yet demonstrated. The root graph refresh completed successfully; it indexed a changing worktree, so new helpers can still be absent. Impact results are supplemented with current source callsites and do not imply complete coverage. Missing index registration is recorded explicitly when `detect_changes` cannot inspect a worker worktree.
