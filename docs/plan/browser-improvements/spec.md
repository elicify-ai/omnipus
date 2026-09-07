# Browser reliability and performance implementation

Created: 2026-09-07. Status: implementation in progress; acceptance unproven.

## Objective and scope

Deliver a browser usable as a commercial product: responsive mouse, keyboard and navigation; stable, readable streamed video; reliable startup and recovery; accurate resizing; clear failure reporting. The user authorized parallel implementation in separate worktrees, independent intensive review of the complete diff, correction of every finding, direct browser retesting, and green CI on `browser-improvements`. This is not authorization to merge to the release branch or publish a release.

The implementation base is release commit `fbcbc5edc9845f1fbecb01b423f15d09fe405f5c`, fetched on 7 September. The integration branch was fast-forwarded from its stale local base before workers were created. Investigation evidence lives in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/browser-verification-2026-09-07.md` and the linked corrective analysis. Incident attribution remains distinct from confirmed defects.

## User stories and behavioral contract

### US-1 — Continuous human input (P0)

People must be able to type, click, drag and scroll while viewing their browser, including while an agent replies in chat. Priority: loss of input prevents all useful work. Independent test: an animated local fixture records visible counters and typed text.

1. Given a connected browser, when the user performs repeated gestures, then every accepted discrete action appears once and in order.
2. Given chat activity begins, when the user types or clicks, then input remains usable without cancelling the conversation.
3. Given a connection/control transition, when input cannot be accepted, then the interface shows the failure and recovers without silently replaying uncertain clicks or text.
4. Given a key or button is held, when focus or connection changes, then it does not remain held in the remote page.

### US-2 — Readable, efficient video (P0)

People need a clear picture during both motion and idle reading. Priority: poor picture and encoder fallback directly impair usability. Independent test: capture dimensions and receiver statistics on static and animated fixtures.

1. Given supported hardware encoding, when capture starts or adapts, then every requested encoded size is compatible with that hardware path.
2. Given a static page, when no new frames are needed, then quality does not collapse merely because frame demand is low.
3. Given temporary resource pressure ends, when source demand permits recovery, then picture quality recovers within a bounded period.

### US-3 — Correct geometry and continuity (P0)

People must be able to resize or switch tabs without stale clicks and repeated connection rebuilding. Independent test: alternating fixture targets and window sizes.

1. Given a focused address bar or composer on desktop, when the panel resizes, then the final size is applied and clicks remain accurate.
2. Given rapidly changing sizes or tabs, when changes settle, then the latest target wins and old work cannot overwrite it.
3. Given a capture source replacement, when media resumes, then the relay adds no artificial packet loss and old-source frames cannot corrupt the replacement.

### US-4 — Responsive navigation and recovery (P0)

People must retain control during slow or failed navigation. Independent test: a page with a deliberately unfinished response and explicit connection-health probes.

1. Given a slow destination, when navigation starts, then connection health, detach, cancellation and subsequent navigation remain responsive.
2. Given a healthy unchanged page, when video packet counts remain unchanged, then the browser does not restart solely for silence.
3. Given a dead capture stage, when health checks detect the failure, then the system reports and recovers the failed stage without restarting healthy viewers unnecessarily.

### US-5 — Reliable startup (P0)

People must get a picture promptly or an actionable failure. Independent test: repeated isolated launches, deliberate child exits, cancellation and real-browser smoke tests.

1. Given a valid installation, when the browser opens, then the managed browser starts and a first picture appears.
2. Given the browser process exits before readiness, when startup is pending, then failure surfaces promptly with useful diagnostics and no orphan process.
3. Given repeated start/stop or cancellation, when cleanup completes, then process ownership and user profile integrity remain correct.

### US-6 — Release evidence (P0)

Maintainers need proof that improvements work beyond a demonstration. Independent test: inspect exact-commit review, runtime evidence and all required CI jobs.

1. Given the integrated diff, when independent review finishes, then all findings are resolved and rechecked.
2. Given the reviewed implementation, when direct browser tests and CI run, then all acceptance results are recorded against the tested commit and every applicable required check is green.

## Boundaries and safeguards

Preserve authentication, workspace isolation, destination security checks, browser profile data, audio, remote connectivity and the single-binary architecture. Do not disable certificate validation, sandboxing, hardware acceleration, tests, or CI gates to achieve green. Do not log credentials or typed content. Ambiguous workspace routing must fail clearly rather than choose an arbitrary workspace. Failed destination security negotiation must remain a destination-specific error with usable controls.

Local acceptance targets on a documented reference machine: warm first picture within 2 seconds; cold first picture within 10 seconds; settled desktop resize within 2 seconds; click-to-visible-effect 95th percentile within 200 ms over at least 100 actions. Require a ten-minute static session without idle-triggered restarts and a ten-minute mixed-input run without lost/duplicated discrete actions or stuck keys. These are targets, not measured results. Remote timing requires a stated network profile; do not substitute localhost evidence for remote compatibility.

## Implementation decisions and integration boundaries

Human input uses one ordered WebSocket path; video/audio retain WebRTC. This eliminates cross-transport input ordering and fallback replay. The server reader must only validate/enqueue slow work, not wait for browser page loads. Bounded command execution, coalescing and cancellation must preserve clicks, text and release semantics. A successful transport send is not a Chrome execution acknowledgement; operational evidence must distinguish receipt, execution and visible effect.

Media replacement needs generation-aware forwarding and timestamp continuity before removing the deliberate sequence gap. Prefer preserving the ingest peer during ordinary recapture if verified supported by Chrome. Dimension alignment must work at all adaptation levels and on small inputs; validate native adaptation rather than assuming a fixed scale grid controls it.

Health must distinguish healthy idle capture, ended source, encoding failure, relay failure and disconnected viewer. Fresh heartbeat alone does not prove source health; lack of packets alone does not prove death. Any new structured health fields belong in the canonical contract and generated artifacts, with compatibility tests.

Chrome launch changes must preserve the complete application/helper bundle and one process waiter. The Mach rendezvous messages can occur during teardown: an isolated launch already succeeded with those messages emitted afterward. Do not present the messages alone as the startup root cause.

## Dependency graph and worktrees

Four concurrency slots permit three workers plus the lead. Each owns an isolated branch/worktree. Only the lead integrates or pushes. Go test/build execution is serialized per repository resource rules even while implementation and JavaScript tests run concurrently.

| Lane | Worktree (absolute) | Ownership | Dependencies |
|---|---|---|---|
| UI | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/ui` | Browser component, input client, resize/focus tests | Shared-input contract fixed here; socket responsiveness from integration lane |
| Media | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/media` | Capture extension, relay sequence/timestamps, narrow media Session additions, tests | Generation ownership before gap removal; health producer after contract agreement |
| Startup | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup` | Pipe allocator, launch resolver, startup tests | Isolated root-cause evidence before platform-specific fix |
| Integration | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo` | Gateway dispatch/watchdog, live viewport deadline, contracts, integration fixtures, review/CI | Integrate lanes before direct runtime acceptance |

Wave 1: independent tests and fixes in all lanes. Wave 2: integrate agreed contracts and shared behavior, resolve integration failures. Wave 3: independent seven-lens review in batches of up to three reviewers, including correctness, simplification, errors, comments, tests, types and architecture/spec compliance. Fix every finding and repeat affected review/tests. Wave 4: build the exact branch, direct isolated and macOS-shell runtime verification, fix every discovered issue. Wave 5: push branch/draft PR for authoritative full CI; fix and re-review/retest changes until green. No merge or release publication.

## BDD scenarios, test plan and traceability

Each row is a scenario with its parent acceptance criterion. Tests are written before fixes; assertions derive from the behavioral contract, not current output. Existing browser tests that encode superseded exclusive-control behavior require explicit replacement by the shared-input requirement, not blanket deletion.

| Requirement / scenario | Traces to | Category | Given / When / Then | Test level and planned evidence |
|---|---|---|---|---|
| FR-001 / B01 | US-1.1 | Happy | Connected fixture / 100 discrete actions / exact ordered visible results | Component + real-browser input run |
| FR-002 / B02 | US-1.2 | Alternate | Chat stream active / human gesture / accepted without chat cancellation | Component state-transition regression |
| FR-003 / B03 | US-1.3 | Error | Delayed confirmation or closed transport / interaction / bounded visible failure/recovery, no replay | Component fake network boundary + runtime fault injection |
| FR-004 / B04 | US-1.4 | Edge | Held key/button / blur or reconnect / release cleanup | Component + fixture drag/modifier check |
| FR-005 / B05 | US-2.1 | Happy/Edge | Odd/even/small/oversized dimensions / capture and adaptation / hardware-compatible bounded size | Actual embedded JavaScript function tests + encoder logs |
| FR-006 / B06 | US-2.2 | Edge | Static low-demand source / adaptation sample / no false degradation | Demand/pressure unit dataset + ten-minute idle run |
| FR-007 / B07 | US-2.3 | Error | Temporary CPU pressure / fresh recovery evidence / bounded quality restoration | Adaptation state tests + runtime samples |
| FR-008 / B08 | US-3.1 | Happy | Focused desktop field / resize / final size and accurate click | Component + runtime geometry fixture |
| FR-009 / B09 | US-3.2 | Edge | A→B→C changes / obsolete completion / C remains authoritative | Generation/order tests + rapid resize/tab switching |
| FR-010 / B10 | US-3.3 | Error | Source replacement with late old packets / forwarding / no artificial gap or old-generation output | Real relay tests: sequence wrap, loss, reorder, timestamps, audio |
| FR-011 / B11 | US-4.1 | Error | Slow command / health or detach / socket reader remains responsive | Real socket integration test with controlled slow boundary |
| FR-012 / B12 | US-4.2 | Happy | Healthy idle source / unchanged packet samples / no restart | Watchdog stage-health tests + static soak |
| FR-013 / B13 | US-4.3 | Error | Ended source or stale health / recovery / failed stage recovers visibly | Stage-specific fault tests + runtime interruption |
| FR-014 / B14 | US-5.1 | Happy | Valid isolated install / open / first picture within target | Repeated real Chrome and branch-binary launch measurements |
| FR-015 / B15 | US-5.2 | Error | Child exits immediately / startup / prompt typed failure and reap | Subprocess integration regression |
| FR-016 / B16 | US-5.3 | Edge | Concurrent cancellation / teardown / exactly one reap and no profile corruption | Lifecycle/race tests + repeated isolated launch |
| FR-017 / B17 | US-6.1 | Happy | Integrated diff / independent review / no unresolved findings | Review report and resolution ledger |
| FR-018 / B18 | US-6.2 | Happy | Reviewed commit / browser tests and CI / recorded passing evidence | Runtime report, exact-commit required CI checks |

Boundary datasets: dimensions 0/1/2/11/12/13 and odd/even values below/at/above 1280×720 pixels, scales 1/1.5/2 and device scales 1/2/3; geometry zero/hidden and rapid alternation; pressure none/CPU/bandwidth with missing/stale/fresh samples; queue empty/full/full+1 and alternating moves/releases; source sequence near 0/65535 with duplicates, reorder, missing packets and overlapping generations; startup executable absent, immediate exit, healthy launch and cancellation before/during/after readiness. Each lane records concrete cases, expected values and mutation results in its evidence log.

## Existing codebase context and impact

Go backend and React/TypeScript frontend; focused Go tests use `goolm,stdjson` tags and `-p 1`, frontend uses Vitest. Canonical contract generation and CI workflow coverage remain mandatory. The optional reference-pattern directory is absent in this release; reuse actual neighboring browser patterns instead.

GitNexus exact-base indexing is running under alias `omnipus-browser-improvements`. Every existing symbol change requires impact analysis before editing; missing index coverage requires recorded callsite analysis rather than invented zero risk. Browser component changes and shared relay ownership are semantically high risk even where graph counts are small. Record exact results and callers in the implementation evidence as indexing completes.

## Holdout evaluation — post-implementation only

These scenarios are reserved for independent evaluation and are not developer test oracles: (1) use two browser viewers and alternate human typing; (2) read a static page, then immediately interact after ten minutes; (3) resize during a page animation and click a moving target; (4) navigate to an incompatible secure endpoint and recover to a valid page; (5) interrupt a media connection while leaving chat connected; (6) switch tabs while holding a modifier; (7) sleep/wake the Mac with the installed web-app shell open. Record outcomes and limitations rather than infer success from unit tests.

## Completion audit

All FR rows, performance targets, review findings, direct browser tests and required CI jobs need exact-commit evidence. Missing remote/native-shell evidence remains incomplete, not implicitly passing. No assertion of commercial readiness until the complete audit passes. The goal remains active through continuation turns until fulfilled or a verified external blocker meets the required blocked audit.
