# Capture health identity and progress test plan

Independent oracle: a live authenticated socket may refresh its heartbeat, but only evidence naming the current server capture generation and exact target may drive capture recovery. The encoder's local attempt counter is a separate identity. A parsed packet received from the current source is progress even when writing to viewers fails; old-source packets, old boundaries and writer-success totals cannot prove current-source progress.

## Required examples

- Current socket + current measured frame: preserve local attempt 47 separately from server generation 1; stamp the server binding epoch and observation time.
- Current socket + absent/stale/wrong-target frame envelope: refresh socket liveness, leave retained evidence and its observation time unchanged.
- Canceled/replaced socket: alter neither heartbeat nor evidence, including bare pings.
- Async encoder stats: changing desired capture alone cannot relabel the sampled peer. Replacing the peer, stream, current command, or local attempt while awaiting stats sends only a bare ping on the unchanged socket; replacing the socket sends nothing from the old completion.
- Schema validation enabled: qualified ping envelope survives validation and maps into the internal observation; missing envelope remains compatible for liveness only.
- Watchdog: static idle and missing/stale telemetry do not cause recapture. A finite repaint with unresolved encoding/sending evidence survives repeated fresh samples until corresponding progress occurs.
- Receipt identity: changing installed binding/generation without a new matching packet is not progress. A same-binding generation change cannot inherit old receipt proof.
- Recovery: the first finite matching receipt after a loss can complete recovery even if the next watchdog sample arrives after that receipt. A pre-loss receipt, delayed callback, canceled binding, stale target or old generation cannot complete recovery.
- Sampled heartbeat shutdown: a newer heartbeat or replacement binding between sample and claim must prevent shutdown, including equal timestamps on different bindings. Exact timeout boundary and zero timestamps do not claim shutdown; unchanged stale evidence closes resources exactly once.
- Viewer write failures do not erase actual ingest receipt progress. They do not constitute proof of viewer presentation.

## Boundaries and fixtures

Use real CaptureSession state and real gateway mapping/reader. Fake only the relay's externally observed receipt snapshot, timers where already supported, and browser globals around the actual embedded encoder JavaScript. Expected identities and counters are assigned by this protocol plan, never copied from observed production output.

Production uses context-bound ingest. Legacy nil-binding test APIs retain their existing behavior; new correctness cases must use a real context binding so legacy compatibility cannot hide a production defect.

## Verification sequence

Metadata declarations may precede behavioral red tests to keep failures executable. First exercise producer envelope and atomic heartbeat/evidence admission. Then integrate media's coherent VideoReceipt snapshot and test watchdog/recovery timelines. Every implementation phase requires observed behavioral reds, focused greens, targeted mutations, restoration, and appropriate race checks. No running production instance is involved.

Unknown intervals remain conservative: stale, muted or incomplete counter telemetry resets unresolved counter evidence. A finite-repaint latch spans only fresh complete observations within one binding, server capture frame and local attempt; this does not claim detection of failures hidden by absent telemetry.

## Observed phase-A evidence

- Red run 94720 exited 1: six core cases, five gateway cases and two embedded encoder functions failed behaviorally. The async replacement loop stopped at its first failing peer case; later fences require their individual mutation checks. Current/bare heartbeat controls passed.
- Canonical generation completed with TypeScript validation; only the expected copied inbound schema description changed. Wire shape already permits the qualified ping fields.
- Green run 98781 passed the phase-A selection: browser 1.631s, gateway 5.863s, capture extension 1.431s.
- Separate subsequent red selection in 98781 exited 1: unchanged stale socket did not stop with the compilable stub; both server generation and target changes wrongly retained the old finite-repaint latch. These are independent of the already-green heartbeat admission cases.
- Guarded shutdown extraction affects the existing Stop path (GitNexus MEDIUM, 14 affected symbols, six direct callers). Shutdown resource cleanup remains outside the capture mutex, with the conditional ownership claim and stopped state change inside the same mutex. New health helpers are absent from the stale graph, so their explicit callsites are reviewed manually.

## Phase-A and guarded-shutdown validation

Broader focused green passed browser 2.577s, gateway 5.616s and embedded encoder 1.527s. All 18 targeted mutations were caught by behavioral assertions: four shutdown guards, two tracker frame-identity resets, two gateway identity mappings, three frame evidence admission checks, the atomic stale recovery claim, desired-versus-current producer identity, and five asynchronous peer/stream/command/attempt/socket fences. The driver restored each source file in a finally block.

The restored selection passed three shuffled race repetitions: browser 4.097s, gateway 13.899s, capture extension 4.305s. No race warning appeared. Run 99384 exited zero. The selection was `TestCaptureHealth|TestEncoderHealth|TestCaptureIngestWireHeartbeats|TestCaptureSession_Stop|TestCaptureSession_Done`, using the required `goolm,stdjson` tags and serialized package execution. Initial tests used CGO_ENABLED=0; race tests used CGO_ENABLED=1.

Scope limitation: this commit supplies and verifies guarded heartbeat shutdown and frame-reset tracker behavior; the production watchdog still needs the subsequent coherent receipt/snapshot integration to call the guarded API. Receipt recovery, immutable health-event publication and frame-transition recovery reset are the next bounded implementation phase. No production instance was changed.

## Receipt and recovery continuation phase

The next phase uses `Stats.VideoReceipt` as the actual ingress oracle. Its raw global serial remains visible even when binding/frame identity is stale; only a current tuple can clear receipt failure or complete recovery. A failed viewer write does not erase accepted ingress. Current HasVideo/VideoGeneration alone never prove a packet arrived.

The loss and recapture claim capture receipt baselines before later packets can be mistaken for pre-claim evidence. A consumed serial cannot be reused by an OnTrack callback. Production binding mode persists after Unbind so an ended authenticated connection cannot regain legacy receipt-free behavior.

Health events carry a CaptureFrameState value captured under the same state-claim lock; observer delivery remains outside the lock. Frame reset invalidates both scheduled and already-running recovery continuations with a private epoch, clears frame-specific health/budget/proof, preserves socket liveness and global receipt history, and opens the new frame's settle window. Root owns the actual BeginFrameTransition callsite; direct helper tests do not claim that production wiring is complete.

The relay loss callback carries the original installed binding/offer/generation/target. The installed snapshot also carries a receipt serial read under the relay mutex, so a later replacement's finite first packet remains post-claim progress. No callback executes under relay locks, and delayed events are never retagged from a new current connection.

Gateway fixtures leave the watchdog and CaptureSession real. Only relay observations are synthetic: an independent sequence describes continuing accepted packets, including packets whose viewer writes failed or belong to an older binding/frame. Twenty observations exceed the specified six-tick failure debounce without relying on implementation-derived expected values.

Additional recovery ordering oracle: a current post-loss receipt while the Lost observer is blocked resolves the already-claimed episode, emits exactly one Recovered event, and prevents the resumed continuation from issuing any recapture. Source: agreed episode cancellation contract, not attempt-counter implementation. Real CaptureSession and event delivery remain under test; only external relay receipt counters are synthetic. Mutation: omit active episode from failing-state detection or omit continuation cancellation.

Internal publication version contract: each claimed state, including Recovered, advances a private monotonically increasing version. Delayed same-frame Lost must fail IsCurrentVideoHealthEvent after matching receipt resolves its episode; latest Recovered must pass despite episode cancellation. Reset and Stop invalidate prior claims; uint64 exhaustion rejects publication without wrap. Real claims/observer barriers remain under test; relay packet counters alone are synthetic. Mutations: omit version comparison, omit reset invalidation, wrap version on exhaustion. Root owns queue/final writer checks; helper validation alone does not prove atomic write admission.

### Phase-B execution checkpoint

- Retained run 43963: exit 1; all original 20 leaf cases reproduced behaviorally (16 browser core, four gateway watcher), without setup errors.
- Retained run 96884: original 16 core cases passed (browser 4.439s). Separate additional selection exited 1: 18 behavioral failures and two passing identity controls; browser 3.061s and gateway 4.362s. The finite-packet watcher case failed because it emitted Recovered while encoding remained unresolved.
- Draft implementation now retains the episode loss baseline across automatic attempts, qualifies typed relay loss against the original installed tuple and current binding/frame, cancels episode work on recovery/reset/Stop/binding replacement, and samples coherent receipt evidence in the watchdog. These drafts await green and mutation validation.
- Internal event-version claim/validation remains a compilable shell pending separate behavioral red. Exhaustion cancellation is also pending its separate red.
- Integration boundaries remain explicit: root must wire frame-health reset on actual generation changes, replace the temporary RecaptureFrameContext adapter with immutable command and original binding/frame/episode admission at final write, and validate claimed health event version at enqueue/final write. A helper precheck alone does not prove these queue races fixed.
- GitNexus additional impacts for stopWhen, noteRecaptureIssuedLocked, captureProgressSerialLocked, and onIngestLostForOffer returned UNKNOWN (new helpers missing from stale index); manual scope covers shutdown, recapture baseline, recovery receipt admission and typed loss registration. Existing Stop impact is MEDIUM (14 symbols/six direct callers), already reported.

Phase-B mutation targets prepared from the agreed failure contracts: wrong receipt binding/generation/target; pre-loss receipt acceptance; late loss-snapshot rebasing; duplicate pre-observer episode claims; omitted episode cancellation; omitted reset epoch invalidation; late event frame lookup; stale stored-health proof retained; omitted ordinary recapture baseline; context-bound mode lost; unqualified relay registration; retired installed offer accepted; viewer-forward count substituted for accepted receipt; stage failure bypassed by old packets; unqualified receipt treated as progress; previous-health-sample receipt baseline discarded; retained finite receipt never reconsidered. Event-version/reset/exhaustion guards will receive separate mutations after their observed red/green.

- Retained run 26231: exit 1 overall by design. The phase-B selection passed all 40 leaves (35 browser in 2.379s; five gateway in 5.585s). The subsequent five event/exhaustion leaves reproduced all five intended behaviors as red (browser 1.203s), without setup errors.
- After that terminal, internal event versions are claimed under cs.mu, reset invalidates the version, exhaustion rejects without wrapping, and IsCurrentVideoHealthEvent checks stopped/version/original frame identity. Exhausted automatic recovery cancels its episode context. Callback delivery remains outside cs.mu. These final additions await green/mutation/race verification.
- Relay dependency ff9d3a245 was ordinarily cherry-picked as 38ed2713c; the agreed typed original-offer callback and coherent installed tuple/receipt snapshot now exist in the actual relay in this worktree. No viewport or attachment adapter dependency was needed.
- Final draft mutation driver has 24 anchored independent faults, with initial/restored health/recovery/watchdog selection and three shuffled race repetitions planned. Driver rejects build errors as mutation evidence and restores source after every run, including exceptions. It has not yet executed.

Post-wait source lifetime regressions: original binding cancellation during InstalledIngestOffer or Stats must reject a new loss claim, publish no Lost event, dispatch no recapture, and create no episode. Cases cover typed installed offer, direct authenticated failure, and sampled authenticated failure; only external relay snapshot waits are simulated by cancellation callbacks, leaving the real claim mutex/state machine in place. Expected outcome follows the agreed original socket lifetime contract. These three newly drafted tests await red; the post-wait checks are not yet implemented.

### Phase-B final validation and remaining integration boundaries

Retained driver **41965 exited 0**. The selected health/recovery/watchdog suite contains 138 unique leaf cases: browser 81 and gateway 57. Restored green passed all 138 (browser 1.955s, gateway 6.926s). Three shuffled race repetitions passed 414 leaf executions (browser 4.730s, gateway 15.971s), with zero failed/skipped cases and zero race warnings. This is the scoped health suite, not the full repository or browser UI/runtime release suite.

All **25 independent mutations were caught behaviorally**, none survived, and none relied on compilation failure. The restoring driver returned every changed source to its original final implementation before the green/race runs. The 48 new leaf cases include 46 observed behavioral reds and two passing identity controls (current installed offer and stopped capture); those controls are not misreported as new reproductions.

Additional retained run **28145 exited 1 by design**: five event/exhaustion cases passed (1.479s), followed by three snapshot-cancellation behavioral reds (1.113s). The final post-snapshot binding check is included in 41965's green and mutation proof.

Reproduction commands, from `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/ui`:

```sh
python3 browser-health-phase-b-mutations.py
CGO_ENABLED=0 GOMAXPROCS=2 go test -tags goolm,stdjson -p 1 -count=1 ./pkg/tools/browser ./pkg/gateway -run 'TestCaptureHealth|TestBrowserWatchdog|TestCaptureStageFailure|TestIngestLoss|TestIngestRecovery|TestWatchEncoderLiveness|TestCaptureSession_WiresIngestLiveHook|TestEnsureCaptureSession_InstallsTheManagerVideoHealthObserver|TestSetVideoHealthObserver'
CGO_ENABLED=1 GOMAXPROCS=2 go test -tags goolm,stdjson -p 1 -race -shuffle=on -count=3 ./pkg/tools/browser ./pkg/gateway -run 'TestCaptureHealth|TestBrowserWatchdog|TestCaptureStageFailure|TestIngestLoss|TestIngestRecovery|TestWatchEncoderLiveness|TestCaptureSession_WiresIngestLiveHook|TestEnsureCaptureSession_InstallsTheManagerVideoHealthObserver|TestSetVideoHealthObserver'
```

Evidence artifacts are in that same worktree: `browser-health-phase-b-mutation-results.log`, the 25 individual `browser-health-phase-b-mutation-*.log` fault logs, and JSON `browser-health-phase-b-restored-green.log` / `browser-health-phase-b-restored-race.log`. The driver and logs are workspace audit artifacts, not required production inputs.

`detect_changes` was attempted before commit (98993 exit 1), but GitNexus cannot resolve this unregistered worktree. The parent explicitly permits the recorded failure plus manual exact-file diff review, and owns integrated graph review. The branch remains `browser-improvements-ui`; main and the running app were not changed.

**Still required in parent integration:** call `resetCaptureHealthForFrameLocked` only after an actual frame generation change under `cs.mu`; replace the explicitly temporary `RecaptureFrameContext` adapter with immutable command admission at final write retaining original binding/frame/episode lifetimes; take a fresh receipt baseline only for admitted ordinary recapture, never automatic retries; use `VideoHealthEvent.Frame` and `.Version` and `IsCurrentVideoHealthEvent` at gateway enqueue/final write. A pre-dispatch helper check alone does not prove queue cancellation or prevent a late serializer from relabeling a command. Final full-diff review and actual browser/runtime verification remain separate release gates.
