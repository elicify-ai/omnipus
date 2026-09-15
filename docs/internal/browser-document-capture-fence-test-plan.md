# Document capture fence — independent test plan

Scope: capture-owned document transition identity, authorization, geometry waiting,
and encoder startup. Root owns detecting main-frame document changes, proving a
new-document paint, measuring under the live command gate, and calling completion.
A navigation command acknowledgement is not evidence of a new document paint.

The token belongs to one document transition and survives same-target viewport
changes. Every new document creates a fresh capture generation, including repeated
same-target pending navigations. While the token is pending, public width/height
are zero and readiness is false; input, media boundary commits, and every qualified
or legacy recapture path must fail closed. Completing only the exact current token
with measured valid geometry forces a fresh measured generation and makes a frame available for subsequent qualified
recapture; media readiness still requires its own new frame boundary. Completion,
supersession, target switch, and Stop end the token context. Invalid completion
must not retire the current token or alter its pending state.

| Timeline | Independent outcome |
|---|---|
| Ready frame → document A → same-target document B | Both generations advance; A context ends; B remains pending |
| Pending document → same-target measured viewport | Token unchanged/live; public geometry zero; input/commit/recapture denied |
| Pending B → retained A completion | Stale error, B still pending, no publication of A geometry |
| Pending token → actual target switch | Token retired; old completion rejected; switched target retains its own frame |
| Pending token → valid measured completion | Token ends; unclaimed geometry waiter wakes; readiness remains false until boundary |
| Pending token → invalid dimensions/scale, exhaustion or Stop | No fabricated measured frame; errors preserve current ownership |
| Pending token → startup preparation | Startup releases command gate while waiting; no old-document measurement or injected frame |
| Startup measurement → document begins before its publication | Measured old-document geometry cannot complete startup; wait for document completion |
| Pending startup → caller cancellation/Stop | Prompt cancellation; persistent tab remains alive; no geometry publication |
| Retained legacy tab recapture → document starts during foreground wait | No later old-document command, including after new-document completion |

Tests keep capture, frame tracker, real gate and real startup preparation intact.
Only browser layout reads, native focus, transport send, and clock barriers are
controlled. Expected generations and geometry use literal test inputs; opaque
capture/token identity is retained before the transition. Behavioral reds precede
implementation, followed by deliberate removal of pending authorization and token
qualification, exact restoration, and focused race checks. Full decoded browser
validation and integrated CI are parent-owned and cannot be inferred from this slice.

## Recorded evidence

The baseline used a minimal API shell over the existing frame-transition mechanism,
so failures tested behavior rather than missing symbols. Session 55145 exited 1 and
reproduced repeated-document identity, pending authorization, stale completion,
geometry waiting, startup gate release, legacy continuation, exhaustion, and
private health/ingest failures.

Milestone session 12213 ran one affected selection, combined faults, and exact
restoration followed by race detection. Both initial green and restored race runs
passed 236 test entries with zero skips; the restored run reported no races.
The combined faults disabled pending authorization, removed token qualification,
and reused an existing generation during completion. They produced 10 failing
entries and 11 passing entries through behavioral assertions. The driver restored
the exact production source before the final run.

The affected package was `./pkg/tools/browser`, with `-count=1 -p 1`,
`GOMAXPROCS=2`, tags `goolm,stdjson`, and a 120-second package timeout. The selection
was `^(TestDocument|TestPrepareEncoderFrame|TestWaitConfirmedFrame|TestRecapture|TestIngest|TestVideoHealth|TestCapture(Frame|Health|Ingest|Recapture|Session_|Start|Stop|Grace))`.
The fault phase selected `^TestDocument`; the final phase enabled `-race` and CGO.

Raw logs:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/document-capture-fence/initial-green.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/document-capture-fence/combined-faults.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/document-capture-fence/restored-race.log`

Direct caller inspection and the staged diff establish the capture-only scope;
GitNexus was not used for this followup, following the user's explicit override.
These are implementation checks, not an independent review of the author's work.
Actual browser document-paint detection, integrated live routing, decoded video
and audio, and full CI remain outside this finite milestone. The live caller must
measure and complete while holding the command gate, then use the returned fresh
frame and its original request lifetime for subsequent recapture.
