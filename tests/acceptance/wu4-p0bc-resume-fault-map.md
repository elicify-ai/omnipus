# WU-4 P0-b/c resume faults — RED plan

Oracles recorded before inspecting the production failure paths: the chief's 2026-10-17 binding B ruling in the resume brief; the system-agent spec FR-062/067/068/087, BDD-6.24 and section 6.7; the approved helper-seam ruling (ClaimResume at admitted consumption; zero duplicate model input); and the P0-c ordering brief. B means secret-only direct-bus helper wakes bypass the redundant generic consumed-marker and inbox acknowledgement. The real request-id ClaimResume remains mandatory. An explicit Stop after restart landing still vetoes a pending old secret wake.

| Row | Input / ordering | Required observable result |
|---|---|---|
| P0-b sensitive-set damage | Submit a healthy card after an unrelated stored entry is damaged | Saved references remain readable; dispatch succeeds; exactly one reference-only helper continuation and one durable ClaimResume; no secret in prompt, transcript, logs or errors |
| P0-b generic marker fault | Deliver a real secret-only direct-bus helper wake while generic consumed-marker publication is faulted | No attempt to publish the redundant marker; exactly one real ClaimResume and one continuation; no stranded claimed request |
| P0-b generic Ack fault | Same wake while the inbox Ack boundary is faulted | No redundant Ack attempt; ClaimResume still publishes once and the helper executes once |
| P0-b mandatory-claim control | Fault the real ClaimResume publication | Visible failure, zero continuation/model input, no claim; removing the fault leaves the submitted request retryable and the next admitted delivery claims/executes once |
| P0-b healthy/duplicate control | Deliver the same healthy secret wake twice through real admission | One durable claim, one continuation and one reference-only injection, not two; no redundant generic marker |
| P0-b ordinary-inbox control | Real durable non-secret inbox wake, healthy and with marker/Ack I/O faults | Healthy body executes once with marker and Ack; either fault remains visible, no model body runs and the input stays pending. B must not widen to ordinary wakes |
| P0-c restart then explicit Stop | Submitted receipt with original wake withheld; real boot lands restart interruption; explicit Stop; then retry/process old wake | Zero claim and zero continuation; reference remains stored and the request remains truthfully submitted |
| P0-c already-queued wake | Recover/retry queues the restart-permitted wake, then explicit Stop before actual processing | Same zero-claim/zero-continuation veto at the consumption boundary |
| P0-c durable veto control | After that Stop, clear the session Stop via public revival and create a fresh registry/boot | The old receipt still cannot resume; saved reference is unchanged; request metadata/transcript stay value-free |
| P0-c restart-only positive control | Identical submitted/restart fixture with no later explicit Stop | Exactly one admitted reference-only continuation; a later boot cannot consume it again |

Boundary: real AgentLoop reconstruction, admission, registry, submit receipt, credential store, session lifecycle, transcript, message bus and helper execution. The model is the existing deterministic process-edge fake. The secret-marker cut uses the real archive envelope-size refusal: only the generic `steer_message_id` is enlarged beyond the record bound; the canonical request id, binding and reference payload remain unchanged. This is a storage-admission fault, not an operating-system write-error claim. The secret-Ack cut blocks only that helper's inbox file with a directory; parent delivery stays healthy. Mandatory-claim and ordinary-marker cuts use the existing turn-registration seam to replace the actual transcript file with a directory after admission, then restore it. The ordinary-Ack cut does the same to its actual inbox file. No private replacement classifier, fabricated ClaimResume success, direct fabrication of terminal request metadata or weakened existing assertions.

Tests are added in new sibling test files; the existing P0-a pack and all existing continuation controls remain unchanged. If a named failure seam is unavailable, add a loud BLOCKED test and report that gap rather than silently skip. GitNexus tools and this checkout's index are unavailable; direct caller/file searches are the documented scope-analysis fallback (inferred, not a claimed graph run).

Execution: one narrow tagged Go process at a time through the shared slot runner, with raw logs and exit codes. Publish P0-b before waiting for the rest of P0-c. This author proves RED only; implementation GREEN, independent mutation probes and the Proof-of-failability checklist are deferred to fresh CHECK. Healthy controls that were not observed RED are controls, not proof of feature failability. Full CI, user-facing docs and live UAT are outside this tests-only increment.

## P0-b publication receipt

The QA branch retains older production, so defect runs use an unchanged source archive of developer commit `78d1b04e5e2ff1418c9ec56b5fb3aaeefd161a17`, with only the new test files overlaid. No production edit or import is made on the QA branch. Exact commands, raw exits and final-source hashes are in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/p0bc-20261017/p0b-final-runs.json` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/p0bc-20261017/p0b-final-source-manifest.json`.

| Selector (anchored with `^` and `$`) | Observed on unchanged production | Reason |
|---|---|---|
| `TestWU4P0BUnrelatedSensitiveValueDamageDoesNotStrandResume` | RED, exit 1 | The healthy value and receipt commit, but unrelated damage makes submit report its continuation unconfirmed |
| `TestWU4P0BDirectBusSecretWakeUsesOnlyMandatoryClaim` | RED, exit 1 | One real claim and one model turn succeed, duplicates do not execute; a forbidden redundant generic marker is still persisted |
| `TestWU4P0BGenericMarkerAppendRefusalCannotStrandClaimedWake` | RED, exit 1 | The real claim is already present but the later marker refusal leaves zero model calls |
| `TestWU4P0BGenericInboxAckFailureCannotStrandClaimedWake` | RED, exit 1 | The real claim is already present but the later Ack refusal leaves zero model calls |
| `TestWU4P0BClaimWriteFailurePreventsHelperModelInput` | Control PASS, exit 0 | Failed claim publication gives an error and zero model calls/claims; after repair, a real admitted retry claims and executes once; another claim returns false |
| `TestWU4P0BOrdinaryInboxWakeRetainsMarkerAndAckRequirements` | Three control leaves PASS, exit 0 | Healthy ordinary inbox delivery executes/marks/acknowledges; marker and Ack faults each keep its body from running and its input pending |

Correction: the initial ordinary-control draft used `UnackedCount`, which counts questions/blockers and excludes its final handback. That instrument-only failure is excluded from RED evidence. The final control uses `Drain`, proves a real pending handback before each case, and passes all three named leaves. No compile error, panic, timeout or empty collection is credited as RED. Post-acceptance assertions that the four RED rows cannot reach remain deferred to GREEN/CHECK.

## P0-c publication receipt

Same unchanged production archive `78d1b04e5e2ff1418c9ec56b5fb3aaeefd161a17`. The new fixture runs the registered request_secret tool in an actual helper, waits for its park turn to finish, submits the real card, and retains its actual original bus wake. Boot is the existing persisted boot-epoch/recovery path. Stop is the ordinary public StopSession operation, with human and agent actors tested separately. Fresh registries re-read durable request metadata. A stopped stale wake may be a harmless no-op or a rejection: its return-error representation is not the oracle; zero ClaimResume publications and zero additional model turns are.

| Selector (anchored with `^` and `$`) | Observed on unchanged production | Reason |
|---|---|---|
| `TestWU4P0CExplicitStopAfterRestartVetoesWithheldSecretWake` | Human + agent RED, exit 1 | Restart lands, explicit Stop is accepted, then retry incorrectly re-dispatches and the withheld wake claims/executes once instead of zero times |
| `TestWU4P0CExplicitStopAfterRecoveredWakeQueuedVetoesConsumption` | Human + agent RED, exit 1 | A recovery wake already queued before explicit Stop still claims/executes after that Stop |
| `TestWU4P0CPostRestartStopVetoSurvivesRevivalAndAnotherBoot` | Human + agent RED, exit 1 | After public revival clears the session Stop, another real boot and fresh registry resurrect the old saved request; one claim/extra turn proves the explicit veto was not durable |
| Existing `TestSecretWake_RestartStopRevivesUnconsumedReceiptOnlyOnceAcrossBoots` | Control PASS, exit 0 | Without a later explicit Stop, restart-only recovery really executes once, and another boot never consumes it twice |

All six RED leaves reach the required zero-versus-one claim and no-additional-turn assertions, not a constructor/compile/panic/timeout failure. Saved references remain readable and submitted; log/error/durable plaintext scans report no separate failures. Raw commands/exits are in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/p0bc-20261017/p0c-runs.json`; exact executed source hashes are in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/p0bc-20261017/p0c-source-manifest.json`. Independent implementation GREEN, mutation probes, fresh CHECK and live UAT remain deferred, not passed.
