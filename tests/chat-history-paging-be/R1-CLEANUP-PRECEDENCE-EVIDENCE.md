# R1-1 cleanup precedence RED evidence — 2026-10-23

## Outcome and source

Genuine behavioral RED on normal-merge e016e0a3ab29fa66bf0cd62ff826a3ccbac9b042, which contains required backend980a1a2f23f186172d50a742882a4e02a98a03b9. Test source frozen BEFORE execution, all five test/support hashes unchanged afterward. No production change, exception swallowing, synthetic joined error, fake retention/index/HTTP/logger, Fly or full suite. Prior11 saved gate0 exits were not rerun for reboot recovery; no scratch state needed restoration. F5 session-list/Y remains ON HOLD untouched.

Exact command (one queued Bash slotrun process):

```text
CGO_ENABLED=0 OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/fixture-e016e0a3a/build/session.test go test -tags goolm,stdjson -count=1 -v -p 1 -run '^TestHistoryPageCleanupFailure_OutranksCancellationAndNotFound$' github.com/elicify-ai/omnipus/pkg/gateway
```

Saved log: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/gate1-red-20261023/R1-1-cleanup-precedence.log. Raw exit1. Named parent cases2 FAIL, classification child checks2 FAIL; pure-error family and both prepared-core cases PASS, including both pure child cases. No compile/setup/deadlock/timeout failure.

## Exact expected versus actual

| Case | Actual execution / failing statement |
|---|---|
| Pure cancellation / pure unknown position | PASS: no fabricated canceled response+abort; exact404/history position not found+invalid_request. Existing behavior stays unchanged. |
| Prepared core + cancellation + native remove fault | PASS: actual shared prepared reader returns typed storage_error; errors.Is sees context.Canceled AND actual fs.ErrPermission; fixed sanitized public Error string; old retired file remains pending and normal later read removes it. |
| Prepared core + unknown position + native remove fault | PASS: same actual typed storage_error with ErrHistoryPositionNotFound AND fs.ErrPermission retained; no fake joined error. |
| REST + cancellation + native remove fault | FAIL: actual one summary outcome=abort, expected storage. Failure in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_diagnostics_red_test.go::pagingAssertSummaryCarrier (line93 at execution), called by new rest_handler_cancellation/storage_classification child. No canceled-transport response/status/body guarantee is asserted. |
| REST + unknown position + native remove fault | FAIL: actual404 and history position not found, expected existing fixed sanitized storage500; assert.Equal statements at source138/139 in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_cleanup_precedence_red_test.go::r1CleanupCombined. Child summary also actual invalid_request, expected storage (same shared summary assertion93). |

Required numeric/conditional summary assertions after the wrong outcome stop in their child checks and are NOT falsely claimed reached. Separate privacy/absence-of-success-fields checks still execute outside those failed children and PASS, as do normal subsequent cleanup/read checks. Both real REST cases emit exactly one summary. No raw ID/query/body/path/native-cause canary leaks into those summaries. Existing privileged storage cause logs are a separate documented surface, not subject to new-summary privacy assertions; this pack does not require a specific privileged event name or canceled body delivery.

## Instrument and oracle

The dispatcher built the existing native section-G fixture utility read-only from clean exact e016 input. Receipt /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/fixture-e016e0a3a/build-wrapper.log gives go test -c exit0 and binary SHA256 dd021bfe010ad362c942050094736c0b8f7cf43bad3fdcf92b1ee7ce0b3d03f3, verified directly before use. QA did not build it. Its real private append clock produces2 actual append days/1 rolled partition/2 canonical records and one exact driver receipt; parent producer is closed before child and child Run/Wait joined before gateway opens. Healthy actual REST returns both exact records before fault.

An external request-context observation at actual post-pin historyReadEvent → readChatHistoryPage pauses the REAL reader after capture locks release; no production setter/hook/private reflection is added. Current runtime.CallersFrames documentation was read; stable function names are used, never line numbers or elapsed-time oracles. The real90-day retention sweep unlinks exactly the old rolled partition, publishes a different generation and leaves the old generation present ONLY due to the real pin. Unix helper chmods that actual generation directory and proves actual os.Remove returns fs.ErrPermission before allowing the reader to cancel or look up the actual unknown token. The real final-reader release fails. All owned readers and fixture children join. After fault clears, an ordinary fresh REST read serves only r1-current and removes pending old generation without a new expiry.

Independent authority: explicit user R1-1 requires storage classification for a combined cleanup failure and retaining original read cause; seams-decision X requires typed surfaced cleanup failure/pending retry; spec D11 freezes the existing C6 storage500 for required storage failure and Content-free observability freezes outcome=storage. C6's existing fixed redacted copy is pinned, not learned by running the failing response. Spec separately defines pure404/abort but has no explicit simultaneous-error transport delivery rule. Minimal proposal sent to dispatcher for architect section Z: storage failure is primary, retain both internal causes, active request uses existing500, canceled transport has no response-delivery guarantee, pure cases unchanged. Until Z exists, no canceled body/status or new privileged log message contract is invented. Tests assert the already-explicit task/X/D11 requirements.

Native macOS permission behavior executed. Windows helper uses actual non-delete-sharing handle but native Windows remains UNVERIFIED. Unix root fails loudly BLOCKED because chmod cannot prove native deletion denial; never skip/claim success. No native Windows or full-CI certification is inferred. GREEN, scratch-copy mutation, Proof-of-failability and formal audit remain deferred to fresh independent CHECK; author does not fix or mutate production.

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis, omnipus-failure-triage, github-cli, commit-messages, jev-use:jev-use.

| Claim | Evidence | Certainty |
|---|---|---|
| Required backend input normal-merged | e016e0a3ab29fa66bf0cd62ff826a3ccbac9b042 with ancestor980a1a2f23f186172d50a742882a4e02a98a03b9 | Verified high |
| Real gateway precedence RED | Exact command/log above, exit1; outcome storage vs abort/invalid_request and500 vs404; named2 parent+2 child failures | Verified high |
| Core/native/pure/cleanup/privacy controls | New test::r1CleanupCombined/r1CleanupPureControls/r1CleanupBuildFixture/r1CleanupHoldRemoveFault and the same saved raw log; prepared-core and pure cases PASS, no other assertion failures | Verified high |
| Exact combination delivery pin | User task/X/D11 guarantee storage classification and active500; section Z proposal pending, canceled-body guarantee deliberately absent | Inferred minimal delivery rule; final architect wording Unknown |
| Platform/GREEN/CHECK outcomes | Mac native receipt only; Windows/helper/root limits and independent author/CHECK separation above | Unknown unexecuted outcomes |
| Self-check | Re-read complete failure extraction/named cases, source+fixture hashes, actual runtime boundaries and oracle; checked test-only authored scope, no stale rerun/Fly/production edit or weakened expectation | Verified high |
