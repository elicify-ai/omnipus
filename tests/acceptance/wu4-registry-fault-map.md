# WU-4 registry fault RED plan — 2026-10-17

## Independent authority and scope

Frozen implementation: `a2b0f0b3496d244a4ee9fee289988fb185a77d1c`, branch `work/l1-wu4-red-registry-20261017`, checkout `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-red-registry-20261017`.

Expected outcomes derive from the binding registry-fault brief (chief Q-PUBLISH A), `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/silent-failure-hunter.md` (SF-03/SF-05), `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4-gate/security-lead.md` (S4), and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-red-registry-20261017/docs/internal/specs/omnipus-system-agent-spec.md` (§6.7, FR-062/063/066/067/068/075/079/084/087). They do not derive from observed program output. FR-075's browser-control disposal is outside this registry-only assignment; its no-automatic-plaintext-replay implication applies to retries here.

Only new Go test files in the secretrequest package and this new acceptance map are writable. Existing tests, implementation, prompts, contracts, generated types and frontend remain unchanged.

## Observable case matrix

| Row | Independent expected outcome | Real cut / control |
|---|---|---|
| S4 receipt crash | Atomic encrypted values + bindings + receipt exist while session metadata is still pending and no submitted audit was emitted. Fresh recovery changes that request to submitted and emits exactly one truthful submitted or recovered-submitted audit; later recovery does not invent a second transition. | Public Store.CommitCard followed by newly opened store/session readers and real Registry.Recover; no fabricated submitted metadata. |
| S4 normal-submit control | Public Registry.Create/Submit reach a nonempty real audit file with requested/submitted events for the right request/session. Recovery of already-submitted metadata emits no additional submitted transition. | Normal public submission through the same capture adapter. |
| S4 no-receipt control | Fresh recovery is interrupted, has no references/receipt/value, and emits interrupted, never submitted. | Real pending metadata with no credential transaction. |
| SF-03 publication versus cleanup | At the 33rd terminal transition, successful cancelled-state publication remains cancelled immediately, on disk and after boot even if receipt pruning fails. Submit returns no error (the existing HTTP adapter maps this to 200), and notification/audit report cancelled. A later resubmit is 409/cancelled. | Real atomic state write, then real credential-receipt I/O failure; no private queue/helper oracle. |
| SF-03 later-operation retry | Error-level safe request/session/stage/cause diagnosis and repair guidance accompany failed receipt maintenance. After repair a later public state operation removes only the orphan receipt; repeated pruning is idempotent. | Observe persisted receipt before repair; public operations after repair. |
| SF-03 restart retry | The persisted orphan receipt survives the maintenance failure. Fresh full-inventory boot reconciliation prunes it after repair, without changing encrypted values, versions or recipient bindings. | New Registry/credential/session instances read existing files; no queue-state inspection. |
| SF-03 pre-publication control | Failed terminal state publication leaves the original pending metadata/state, no cancelled notification/audit, and no receipt pruning. Repair permits a real cancellation. | State destination blocked before atomic publication. |
| SF-05 store open | Failed real store access preserves the known pending request, diagnoses request/session + open stage + safe typed cause and actionable repair, with no value/raw-error echo. | Permission-denied credential-file access; wrapped typed-error sentinel control. |
| SF-05 credential commit | Failed real commit leaves no new values/receipt/resume and retains pending. Diagnostics identify commit and safe permission/locked cause, not arbitrary error text. | Real locked Store and filesystem write denial; positive healthy submit. |
| SF-05 state publication | Failure before publication never invents a persisted transition; failure after atomic credential commit still returns submitted state and exact stored references, never 'nothing stored'. | Real state-file publication obstruction, with no secret-bearing synthetic state. |
| SF-05 recovery | An unreadable/invalid saved request is unknown/unavailable, not pending or absent. Recovery diagnostics identify the known request/session where available, recovery stage and safe cause; failed inventory never prunes receipts. | Actual unreadable/malformed state files; real boot recovery and public read/refusal. |
| Cross-cutting non-disclosure | Public records/errors, callback records, audit JSONL, native/structured logs, on-disk request state, reference-only resume and real transcript contain neither submitted sentinel values nor a sentinel embedded in arbitrary lower-level error text. | Nonempty audit/log positive controls use public APIs; encrypted storage is checked independently by real Get, receipt and binding reads. |

## Boundary, assertion policy and gaps

REAL: Registry creation, admission, submit/cancel/state APIs, retention, recovery, credential encryption/atomic receipt commit, filesystem operations, session metadata/transcript and audit.Logger. Only continuation transport is the existing process-edge fixture; real ClaimResume exercises transcript publication. Diagnostics use the production native logger and slog bridge, not a fabricated log-return function. Faults are deterministic file/directory or permission cuts; no sleeps, changed time limits or production mutation. Typed errors use standard errors.Is/As-compatible causes, not a proposed private production type. Diagnostic stage/category assertions require semantic safe context and repair guidance, not incidental full English messages.

The one public HTTP adapter is read to trace its unchanged success/error mapping; these package-scoped tests do not claim an HTTP-server execution or browser UAT. Numeric value/body validation and the already-covered 31/32/33 retention bounds are not reimplemented here; these faults use one valid bounded field and the retention limit + 1. Negative/fault paths dominate the planned cases.

GitNexus MCP is not connected and the sibling index is known stale; direct source/caller searches are the scope fallback (Inferred, not a graph result). No reindex or graphify. New symbols only; no existing test or production symbol is edited.

Mutation ideas for fresh CHECK: omit the recovered-submitted event; roll back cancelled memory on prune failure; omit notification after successful publication; drop the cleanup retry; prune encrypted values/bindings with receipts; flatten typed causes to unclassified text; log a raw sentinel-bearing error; return pending after credential commit. This RED author does not run CHECK or production mutation probes.

## Execution and hand-off

One exact anchored top-level TestName and one package per run, serially through `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh`, lane `wu4qar`, with the brief's fixed Go environment. Raw log, direct exit, exact argv, immutable source HEAD and source hashes are saved under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017`.

Publish S4 independently first, then SF-03 and SF-05 increments. Implementation GREEN, mutations, the skill's Proof-of-failability checklist and independent fresh CHECK are **deferred to CHECK**, never claimed met. Full local Go suites/builds are forbidden; CI remains the whole-suite authority.

## S4 publication receipt

Source stayed unchanged during each run: implementation HEAD `a2b0f0b3496d244a4ee9fee289988fb185a77d1c`, new test overlays identified by SHA-256 in the per-selector manifests. Exactly one anchored selector/package ran per slot acquisition, in serial order.

| Exact top-level selector (`^…$`) | Observed result | Key evidence |
|---|---|---|
| `TestWU4RegistryRecoveredCommittedSubmitEmitsValueFreeAudit` | Behavioral RED, direct exit 1 | Real values/receipt/binding commit and new store/session recovery reach submitted; audit transition count is 0, expected 1. Later boot, actual ClaimResume, resubmit refusal and value-free scans also execute. |
| `TestWU4RegistryNormalSubmitAuditControlDoesNotInventRecoveryTransition` | Positive control PASS, direct exit 0 | Public Create/Submit produce requested/submitted through the same real audit logger; recovering already-submitted metadata creates no second transition. |
| `TestWU4RegistryNoReceiptRecoveryAuditsInterruptedNotSubmitted` | Negative control PASS, direct exit 0 | No receipt/value/resume; real recovery emits interrupted, never submitted; another fresh boot does not duplicate it. |

For each selector, the raw `<selector>.log`, direct `<selector>.exit`, exact `<selector>.command.json`, `<selector>.source-manifest.json` and unchanged `<selector>.after-manifest.json` live under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017`. These filenames are descriptive suffixes, not relative locations to execute from. No compilation, collection or timeout failure is credited as RED. The controls are controls, not tests claimed to have shown RED.

## SF-03 publication receipt

S4 was published independently as `f294895a23f6145bfd378a2304ccfe658584ae9b`. The next increment adds portable actual-I/O cuts: the existing encrypted file is preserved byte-for-byte under a test-only temporary name, and a directory obstructs its original path. The late-bound public credential opener arms this cut only AFTER it reads the newly cancelled state on disk. The same real store's receipt read proves the obstruction is effective (`*os.PathError`). The encrypted file is restored intact before repair/retry checks; no credential value or binding is removed to simulate maintenance. No permissions/root assumption or production-private seam is required.

| Exact top-level selector (`^…$`) | Observed result | Raw evidence prefix under the evidence directory |
|---|---|---|
| `TestWU4RegistryPublishedCancelSurvivesReceiptIOFailure` | Behavioral RED, direct exit 1: cancelled on disk but pending in returned/live state, error instead of success, zero cancelled notice/audit, missing Error diagnosis, and 500 instead of 409/cancelled on resubmit. After repair the fresh-store/session boot sweep succeeds without altering values/bindings. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF03-publication-final` |
| `TestWU4RegistryReceiptCleanupRetriesOnLaterStateOperation` | Behavioral RED, direct exit 1: after repair a public state read still reports pending and leaves the orphan receipt. Repeated absent-receipt pruning, encrypted data/binding comparisons and value-free scans also execute. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF03-later-operation-final` |
| `TestWU4RegistryCancelPublicationFailureKeepsPendingAndDoesNotPruneReceipt` | Control PASS, direct exit 0: blocking the STATE publication itself retains pending, sends no false terminal audit/notification and preserves the old receipt; repair permits real cancellation and receipt-only pruning. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF03-prepublication-final` |
| `TestWU4RegistryDiagnosticCapturePreservesContextFromBothPublicLoggers` | Instrument-control PASS, direct exit 0: both public native and slog loggers preserve Error level, IDs, stage, cause and repair text through the SAME sink/matcher used by fault rows. This is not a claim that production emitted a diagnostic. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/TestWU4RegistryDiagnosticCapturePreservesContextFromBothPublicLoggers` |

Each prefix has `.log`, `.exit`, `.command.json`, `.source-manifest.json` and `.after-manifest.json` receipts. The final named runs include all SF-03 test source bytes. Earlier first-run logs are preserved, not overwritten. GitNexus CLI was also attempted: the sibling index is 4,264 commits stale; an unqualified Recover resolved the wrong package, the file-qualified current Recover is absent, and detect-changes incorrectly reports no changes for staged new test files. Those results are not safety evidence. Direct caller/scope checks are the documented fallback, labelled Inferred. GREEN/mutations/independent CHECK remain deferred.

## Authorized oracle correction — 2026-10-18

**Correction:** the first SF-05 locked-store recovery run also rejected the valid existing instruction `provide master key ...`. That sub-failure was an **oracle defect: valid English verb omitted**, not a production guidance failure. It is excluded from behavioral defect evidence. Missing contextual recovery diagnostics and the unhelpful public unavailable-state message remain real, independent failures.

Chief `omnipus-13` explicitly authorized Q1 A / Q2 A: add only `provide` to the shared repair-verb set; accept a tiny `Helper`/`Errorf` reporter interface so the real helper can be exercised with an assertion-output recorder. All original `*testing.T` callers are unchanged. The helper body, storage-subject check and every other accepted verb remain identical. Named interface parameters satisfy the pinned linter without changing its method types. The authorization record is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/chief-oracle-correction-authorization.md`.

| Exact selector (`^…$`) | Before correction | After correction |
|---|---|---|
| `TestWU4RegistryGuidanceOracleAcceptsProvideKeyAndRejectsNoRepairInstruction` | Direct exit 1: real helper rejects valid `provide master key` text. Raw `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/oracle-control-before-verb.log`. | Direct exit 0: the same helper accepts that valid instruction **and still rejects** the public unavailable-state text that contains no repair instruction. Raw `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF05-guidance-oracle-final.log`. |

The SF-03 publication and later-operation rows were re-executed after this narrow correction and remain behavioral RED (direct exits 1). Its pre-publication control remains PASS (direct exit 0). Raw prefixes are `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF03-publication-oracle-final`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF03-later-operation-oracle-final`, and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017/SF03-prepublication-oracle-final`. Both native/structured public-log controls also remain PASS. No production mutation or self-CHECK was performed. SF-05 publication follows in the next increment.
