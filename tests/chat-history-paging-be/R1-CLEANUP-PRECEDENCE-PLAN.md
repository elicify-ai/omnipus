# R1-1 cleanup precedence RED plan — 2026-10-23

## Input and scope

Normal merge e016e0a3ab29fa66bf0cd62ff826a3ccbac9b042 imports origin/feature/1266-green-be980a1a2f23f186172d50a742882a4e02a98a03b9. Original lane40dac80b551ae5848031982cd1bc0b294f7fb669 was clean; no unexpected commits/scratch edits, all11 prior saved exits complete and not rerun. Only new tests/test evidence are authored here. F5 session-list/Y is ON HOLD, untouched. No Fly run planned.

Read first: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/gate-round1/silent-failure-hunter.md::R1-1; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md::X; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md::D11/Content-free observability. The user independently requires actual cleanup errors be storage failures even with cancellation/not-found, retaining both original causes. Production code is read for placement only, never as oracle.

## Precedence and exact oracles

A real cleanup I/O failure is primary typed storage_error and summary outcome storage. Retain actual original read error AND actual native deletion error internally (errors.Is); never disclose either cause/path/query/identity/body in public response or history_page summary. For an active request, D11's existing sanitized storage500 wins over unknown-position404. Pure cancellation remains no body/abort; pure unknown position remains exact404/history position not found/invalid_request. D11/X do not explicitly spell out combined cancellation transport delivery. Minimal proposal sent to dispatcher for architect section Z: storage classification/logging always wins, active transport gets existing500, canceled transport has no body-delivery guarantee. Freeze NO canceled-response body/status guarantee while Z is pending. No new wire type/reason/field.

## Real boundary / fixture

Existing section-G prebuilt native session fixture driver writes two actual append days via its real private clock and ordinary canonical/index writer; parent store is closed/joined before driver, driver Run/Wait reaped before real agent/gateway store opens. Dispatcher builds exact e016 input read-only and supplies binary/hash receipt; QA does not build it. Use tiny two-row fixture, never measurement-sized data.

External request cancellation context observes the stable historyReadEvent → readChatHistoryPage function stack at the actual post-pin cut (same external context-observation pattern as approved C1), not a line number or timer. It pauses after real pin acquisition and lock release. Parent makes the actual old rolled partition eligible, runs ordinary RetentionSweep(90), verifies one actual removal/new checkpoint generation and that old generation STILL exists (positive proof of real reader pin). Hold real permission/sharing removal fault, proving direct os.Remove fails first; cancel the actual request or let real same-session unknown-token lookup fail. Release/join the actual reader/handler before clearing fault. Then ordinary new REST read must retry pending cleanup. No monkeypatched error, fake store/HTTP/summary, private-field unsafe access, production hook or invented index.

## Case table

| Case | Expected |
|---|---|
| Healthy baseline | Real REST200, two exact canonical entries; one success summary; real production sink positive control |
| Pure cancellation | Empty body, exactly one abort summary; no cleanup fault |
| Pure unknown position |404 with exact history position not found envelope, one invalid_request summary |
| Prepared core + cancel + real final-release removal fault | Typed storage_error, both context.Canceled and native error retained, sanitized error, pending generation remains |
| Prepared core + unknown position + real final-release removal fault | Typed storage_error, both ErrHistoryPositionNotFound and native error retained, sanitized error, pending generation remains |
| Real REST + cancel + removal fault | Exactly one storage summary, all numeric/conditional/privacy fields correct; no canceled transport delivery guarantee frozen |
| Real REST + unknown position + removal fault | Existing exact sanitized storage500, not404; exactly one storage summary and content-free required fields |
| Fault cleared | Actual ordinary REST200 contains retained current entry only, deletes formerly pending retired file; no automatic rollback |

Every scenario joins its own request and native fixture work; timeouts are failure backstops only. Native macOS permission execution is required for local proof. Unix-root must fail loudly BLOCKED because chmod cannot demonstrate remove denial; Windows helper uses actual deny-delete-sharing handle but native execution remains UNVERIFIED unless supplied. No skips to hide missing prerequisites.

## Proof and CHECK boundary

One exact tagged gateway top-level test at a time through Bash slotrun; inspect/report live or stale foreign owners, never remove foreign leases. Save raw exit, named expected-vs-actual failures, source/log hashes and exact input commit. No full suite/build or remote run. Author stops after genuine RED. GREEN, production fixes, scratch-copy mutation and Proof-of-failability belong to fresh independent CHECK. Critical mutants: cancellation wins despite storage,404 wins despite storage, summary abort/invalid_request despite storage, original read cause dropped, native cause dropped, raw diagnostic leakage. No author production mutation.

Additional user R2 (stable third attempt with churn2) is queued AFTER R1-1 pack is proven/pushed; preserve moved-third-cut exhaustion case. GitNexus unavailable in this session; new test symbols only, existing instrument helpers read/reused, direct caller/scope sweep is Inferred rather than graph-certified.