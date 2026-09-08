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

## Current integration checkpoint — 2026-09-08

The unchecked wave-level items remain open until their complete production
behavior is verified. Component tests are not end-to-end acceptance.

Integrated on `browser-improvements`:

- Cold startup and attachment cancellation, exact panel capture ownership,
  original input/command lifetimes, bounded outbound queues, and coherent
  frame/health publication are wired into production paths. Earlier focused
  evidence is retained in the corresponding validation documents.
- Typed recapture retains its measured frame, original binding and request
  through final socket admission (`b960a0883`). Explicit refresh retires queued
  automatic recovery while preserving its retry budget. The final focused
  race run passed 46 leaf cases; three deliberate faults were caught/restored.
- Frame health and stop messages retain their original registered viewer and
  immutable publication (`6660831fe`). The worker's focused race closeout
  passed 33 leaf cases and caught three deliberate faults.
- Capture preparation honors caller cancellation, and focusing an existing
  target cannot recreate a dead tab or continue after cancellation/Stop
  (`0c5a8d256`). Seven behavioral failures were reproduced; three faults were
  caught/restored; 13 focused race cases passed.
- Long error text has bounded wrapping (`6cfb6cca0`). The SPA build passed.
  Isolated rendering with built CSS fits 320/560-pixel containers, but did not
  reproduce the original whole-app clipping; full-app visual verification
  remains pending.

- Viewer offer/input lifetime and immutable ingest response integration
  (`f9f019de6`): two browser identity cases and 24 collected gateway cases/
  subtests passed with race detection; two faults caught/restored. Real
  authenticated socket cancellation passed.
- Failed writes retire the original socket even after request cancellation;
  cancellation before writing preserves it (`84596915f`). Eighteen focused
  race cases passed; both deliberate faults caught all three negative cases.
- Measured viewport/refresh and original-context errors, including both
  same-tab panel routes, are integrated (`2a5c0d02d`). Thirty-one affected race
  leaves passed; three faults caught. The initial viewport now also waits for
  its exact original pending attachment (`5db25b399`); its observed regression
  and replacement control passed the affected race check after correction.
- Back recovery, shortcut releases, media-loss cleanup, bounded real-session
  retries and attachment-aware Retry are integrated (`77a6c6fc1`). Fifty
  affected tests passed after all seven selected old-fault cases failed.
  Notices no longer change picture geometry in actual compiled-CSS measurement
  at widths 320/560/900. Independent review confirmed wiring; full-app remains open.
- Encoder shutdown success paths and adaptation lifetime/overlap guards are
  integrated (`8c70a0b98`), with 13 focused tests and two caught faults. Review
  subsequently reproduced a retired capture rejection aborting its queued
  replacement; media owns that additional negative outcome and documentation.

Latest integration and open findings are recorded in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-review-closeout-2026-09-08.md`.

- Encoder retired rejection correction integrated as `b9820ca97` and independently
  rechecked. Annotation coordinates and stale enrichment integrated as `c11facff8`.
- UI/server wheel semantics and actual Back recovery integrated as `27a5e68ad`.
  Eight wheel and three Back behavioral regressions were reproduced; restored
  affected Go race checks passed. Eighty affected UI/annotation tests, integrated
  typecheck and focused lint passed. Independent correction recheck pending.
- Gateway scoped-route/fixture and combined-message correction integrated as
  `e2a3223c3`. Worker restored race selection passed 44 entries; final Linux-only
  fixture correction still needs compilation/execution on its supported build.
- Independent review read all 78 relay/capture files and all 52 manager files.
  New findings are assigned to capture, manager and startup workers; fixes and
  independent rechecks remain open. Root owns document navigation fencing.
- New document transition design and reproduction preparation are recorded in
  `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-document-transition-plan.md`.

Intermediate real-browser evidence at build `b80287c49`: video/scroll and
click/typing tests each passed, with no skips. Opening was 18.004 seconds;
click-to-destination 904ms included external loading, and typed content appeared
in 380ms. These are smoke results, not controlled cold/warm or 100-action latency
acceptance. Full-app long-error wrapping was visually verified. The production
instance on 10994 remains preserved; the isolated test instance uses 11094.

Go test/build batches remain serial within this team; independent source work
continues in isolated worktrees. No full CI is run for each fix. Other operators'
processes are not controlled or terminated by this work.

## Release evidence (all pending)

- [ ] Independent intensive `/review` of complete release-base diff; seven review lenses, every finding resolved and rechecked.
- [ ] Exact branch build and direct browser fixture testing, including ten-minute idle and mixed-input runs and measured latency.
- [ ] Native installed macOS web-app focus/sleep/wake verification; preserve user profile and production data.
- [ ] Local and remote connection behavior, audio playback/synchronization and failure recovery.
- [ ] Push `browser-improvements`, run applicable CI on that exact commit, fix failures without suppressions or bypasses.
- [ ] Final requirement-by-requirement completion audit; no commercial-readiness claim before all required evidence exists.

## Evidence qualifications

Mach rendezvous errors occurred after successful isolated Chrome launches during teardown; they are not independently the startup root cause. Initial UI/data-channel probes were characterizations, not reproductions of the user's exact stall. New real socket tests do reproduce reader blocking. H264 track replacement and Opus packets are measured, but audible synchronization is not yet demonstrated. The root graph refresh completed successfully; it indexed a changing worktree, so new helpers can still be absent. Impact results are supplemented with current source callsites and do not imply complete coverage. Missing index registration is recorded explicitly when `detect_changes` cannot inspect a worker worktree.
