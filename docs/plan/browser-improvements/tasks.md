# Browser improvements delivery ledger

Integration branch: `browser-improvements`. Base: `fbcbc5edc9845f1fbecb01b423f15d09fe405f5c`. `main` is explicitly out of bounds; no checkout, merge, commit or push to it.

## Historical implementation checkpoints

The two waves below preserve their status when recorded. Statements that wiring
or review was pending are historical; the current checkpoint and release
evidence below govern outstanding work. Checked component work does not mean
its complete acceptance scenario passed.

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

Production wiring has advanced beyond the historical waves. Runtime acceptance
remains open even where implementation, focused tests and independent source
review are complete. Component tests are not end-to-end acceptance.

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
  Isolated rendering did not reproduce the original whole-app clipping.
  Later intermediate-build full-app verification passed, as recorded below;
  the final integrated build still needs runtime verification.

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
  at widths 320/560/900. Independent review confirmed wiring; final-build
  runtime acceptance remains open.
- Encoder shutdown success paths and adaptation lifetime/overlap guards are
  integrated (`8c70a0b98`), with 13 focused tests and two caught faults. Review
  subsequently reproduced a retired capture rejection aborting its queued
  replacement; `b9820ca97` corrected that failure and passed independent recheck.

Latest integration and open findings are recorded in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-review-closeout-2026-09-08.md`.

- Encoder retired rejection correction integrated as `b9820ca97` and independently
  rechecked. Annotation coordinates and stale enrichment integrated as `c11facff8`.
- UI/server wheel semantics and actual Back recovery integrated as `27a5e68ad`.
  Eight wheel and three Back behavioral regressions were reproduced; restored
  affected Go race checks passed. Eighty affected UI/annotation tests, integrated
  typecheck and focused lint passed. Independent wheel/Back and annotation
  rechecks found no new high-confidence defect; annotation enrichment during
  same-generation DOM movement remains best-effort.
- Gateway scoped-route/fixture and combined-message correction integrated as
  `e2a3223c3`. Worker restored race selection passed 44 entries; final Linux-only
  fixture correction still needs compilation/execution on its supported build.
  Independent cleanup/notice and pending-viewport recheck (`5db25b399`) found
  no new high-confidence behavioral defect.
- Independent review read all 78 relay/capture files and all 52 manager files.
  Those reviews produced the finite corrections recorded below and in the
  closeout ledger. This inventory is not proof that every latest correction
  or every acceptance requirement has passed independent review.
- New document transition design and reproduction preparation are recorded in
  `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-document-transition-plan.md`.

Intermediate real-browser evidence at build `b80287c49`: video/scroll and
click/typing tests each passed, with no skips. Opening was 18.004 seconds;
click-to-destination 904ms included external loading, and typed content appeared
in 380ms. These are smoke results, not controlled cold/warm or 100-action latency
acceptance. Full-app long-error wrapping was visually verified. The production
instance on 10994 remains preserved; the isolated test instance uses 11094.

Latest correction checkpoint (source fix `2259cd81f`; documentation/test
checkpoint `b158ec58c`):

| Correction | Integrated commit | Evidence and remaining qualification |
|---|---|---|
| Capture startup, cancellation, grace timer and bounded preparation | `cc027da56` | Focused race checks passed; independent lifecycle/document API review found no additional defect in that scope |
| Launch trust, held locks, pool/session retirement and profile identity | `257d2b9b6`, `eed3f05a6`, `22116776e` | Reviewed with finite followups below; unchanged trusted-PATH focused race control now passes; older failures remain recorded |
| Document capture and live paint fence | `9dcb178fb`, `4d035c62c`, `2259cd81f` | Reverse-initialization race reproduced and corrected; final focused race selection passed 123 entries, zero skips/races, in 5.422s; independent correction recheck clear |
| Live death cleanup and initial tab tracking | `3b31c61e2` | Affected race checks and independent source review completed |
| Cross-process profile deletion coordination | `766f940d8` | 31 affected race entries passed; Unix and mixed-version limits remain explicit |
| Popup lifetime ownership | `46ef9b117` | 39 affected race entries passed; independent correction review found no high-confidence defect |
| Installed ingest cleanup capacity | `8aece98b4` | Seven focused race entries passed; bound applies to authenticated preparation/retirement, not legacy unbound cleanup |
| Late pool registration publication | `57c25fa00` | 39 affected race records passed |
| Reconciliation snapshot ownership | `301b860ef` | 47 affected race entries passed; independent correction review found no high-confidence defect |

Build `39294` passed, including the SPA's 669 embedded files; contract generation
produced no changes. The binary contains source fix `2259cd81f`; subsequent
commits at this checkpoint changed only documentation/tests.

The combined browser/relay race batch `84333` exited 1. The browser package hit
its five-minute timeout after an actual-Chrome test spent about 280 seconds
downloading Chrome and another download began. The complete relay package
passed in 83.753s. This is not a passing whole-browser batch or full CI result.

The new isolated runtime uses port 11094, process 83139 (tool session 38626).
Installed port 10994, process 64851, remains preserved. Soak run `99078` exited 1
after 20.7 minutes: its sole failure was 100-click decoded-video p95 latency of
247.19999992847443ms against the unchanged 200ms target. Both preceding phases
lasted at least 600,000ms; exact ordered delivery of 550 trusted native events,
held-input/release checks, capture continuity, no renegotiation and no viewer
error assertions passed. This is partial acceptance evidence, not a passing soak.
Detailed latency samples, JSON and phase images were not persisted by the line
reporter; the terminal log and final failure screenshot are retained. Artifact
persistence correction and a short diagnostic were integrated as `b293c9808`.
Normal diagnostics `69699` and `71735` failed at p95 267ms and 306.1ms on
unchanged source `2259cd81f`; only test decoder overhead was reduced. Explicit
video-only experiment `67712` passed at 156.2ms with audio inactive, which does
not close product/audio/soak acceptance. Clock correction `d9352ceea` integrates
`5ff70a549` after both actual baselines reproduced. Its final focused race run
passed seven groups/nine records in 15.118s with zero skips/races, retaining actual
packet replay and receiver/picture-loss feedback controls. Independent startup
review is clear and build `42762` passed. Corrected runtime source `d9352ceea`
is on isolated port 11094 (process 17589, tool session 34973); installed port
10994 remains unchanged. Diagnostic `99970` exited 1 before clicks after
low-memory navigation refusals and a 30-second preview-address timeout. Its
zero samples/null p95 are a setup failure, not a measured clock-latency result.
Memory behavior is under read-only investigation; no speed/audio gain is claimed.
Causality for the earlier latency failure remains unproven.
Detailed evidence is retained in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/runtime-latency-validation.md`.

Gateway followup: run `99178` passed 33 groups, failed one obsolete viewport
fixture and skipped 11 Linux-only groups (34.114s, no race warnings). The fixture
had authenticated without attaching, so current admission correctly refused it.
Correction `7a404a82e` establishes a real attachment and retains the original
handler-entry and socket-response assertions. Driver `43028` passed both reader
checks (18.420s), caught a temporary inline-dispatch fault at the intended
socket-response assertion, then restored production exactly and passed both
checks with race detection (17.538s, zero skips/races). This closes that fixture
failure; it does not turn the original failed batch or Linux skips into passes.
No production correction was required.

Go test/build batches remain serial within this team; independent source work
continues in isolated worktrees. No full CI is run for each fix. Other operators'
processes are not controlled or terminated by this work.

## Amsterdam and CI checkpoint — 2026-09-08 18:22 WIB

- [x] Deploy the user-authorized Amsterdam UAT source `ee32c6fa6` to existing machine `784041ef9ed398`; verify image/binary identity, Chrome `152.0.7977.82` and health, preserving machine configuration and snapshot `vs_YYD1njpDJLGRiBxVKkmJ9glO`.
- [x] Independently review manual runner `22ac66c0f`; designated-origin restrictions and the original pixel/event/timing oracle remain intact.
- [ ] Obtain retained Linux audio-enabled latency and full-soak results. At this checkpoint setup had started, but no Linux latency result existed.
- [ ] Complete corrections from PR 685's failed initial CI run `34216723190`, then obtain applicable passing checks on the final commit targeting `release/v0.1.1`. Earlier gating, admission/filesystem and WebRTC lint corrections were integrated; gateway/browser lint and race-fixture validation remained open at this checkpoint.

The first Depot image was unavailable despite reported publication. Replacement
BuildKit build `30862` and deployment `19145` succeeded and the running image
was verified. Detailed immutable deployment provenance and evidence limits are in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/runtime-latency-validation.md`.
These checkpoint entries do not establish final CI or product acceptance.

## Release evidence (incomplete)

- [ ] Independent intensive `/review` of complete release-base diff; seven review lenses, every finding resolved and rechecked.
- [ ] Complete direct-browser acceptance: exact build and both ten-minute phases now have evidence, but the 100-click latency target failed and detailed soak artifacts were not retained.
- [ ] Native installed macOS web-app focus/sleep/wake verification; preserve user profile and production data.
- [ ] Local and remote connection behavior, audio playback/synchronization and failure recovery.
- [ ] Obtain applicable passing CI on the final `browser-improvements` commit in PR 685 targeting `release/v0.1.1`; the initial run failed and corrections require a new run.
- [ ] Final requirement-by-requirement completion audit; no commercial-readiness claim before all required evidence exists.

## Evidence qualifications

Mach rendezvous errors occurred after successful isolated Chrome launches during teardown; they are not independently the startup root cause. Initial UI/data-channel probes were characterizations, not reproductions of the user's exact stall. New real socket tests do reproduce reader blocking. H264 track replacement and Opus packets are measured, but audible synchronization is not yet demonstrated. Earlier graph/index results describe historical checks only. The user subsequently instructed the team to stop GitNexus; current reviews and change-scope checks use direct source/caller inspection and git diffs. No graph result establishes complete review coverage.

The earlier trusted-PATH race runs exceeded the real five-second shell-probe deadline; their cause is not established by the later pass. Unchanged focused race control run `58637` now passed (test 1.05s, package 6.140s, exit 0), closing that current control gap without converting earlier failed runs or the inconclusive mutation into passing evidence. Profile deletion uses sibling locks on the exercised Unix path and refuses already-held legacy locks, but cannot serialize against an older binary that starts without honoring the sibling lock. No cross-platform runtime guarantee follows from those tests. Native ingest cleanup cannot be forcibly interrupted; authenticated unfinished work is bounded, while legacy unbound cleanup remains outside that capacity bound.
