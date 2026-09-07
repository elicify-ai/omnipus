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
