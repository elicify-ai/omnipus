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

For each selector, the raw `<selector>.log`, direct `<selector>.exit`, exact `<selector>.command.json`, `<selector>.source-manifest.json` and unchanged `<selector>.after-manifest.json` live under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/registry-20261017`. These filenames are descriptive suffixes, not relative locations to execute from. No compilation, collection or timeout failure is credited as RED. The controls are controls, not tests claimed to have shown RED. SF-03 and SF-05 remain pending in later increments.
