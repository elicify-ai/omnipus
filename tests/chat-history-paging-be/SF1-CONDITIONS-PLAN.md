# SF1 security conditions — RED follow-up plan

Authority: explicit user F1/F2 and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/logs/sec-sf1.stream.jsonl`::result, read in full. Verdict is CONFIRMED WITH CONDITIONS, not a clean security gate. Coverage gaps are Important; reviewer mutation outcomes were inferred, not executed.

| Condition | Independent oracle / instrument |
|---|---|
| F1 eligibility, not just authentication | Use a real valid bearer token and the same real main. First prove authenticated, eligible **unreadable-history** detail returns metadata200/unavailable. Remove that agent from the workspace CoreTeam through the existing real fixture writer. Then independently named messages_gateMainRead and detail_resolveMainSessionForRead requests both require existing404 session-not-found with no identity/private content. A bypass of only either gate must fail its own assertion, not hide behind401. |
| F2 exact refusal/privacy | Messages500 must equal the exact fixed C6 error, not merely contain it. Physical path/raw OS error bans apply to its whole body. Authorized detail keeps whole-body physical/raw-cause bans and all legacy identity/transcript/main-session bans outside explicitly permitted identity metadata; error text keeps all original bans. |
| F2 identity exception policy | **Security-lead approved WITH safeguards.** Precisely check fixture-derived types/values of session.id, agent_id, optional workspace_id and the COMPLETE optional agent_ids list before masking. Parse an independent copy and replace ONLY those exact nodes with neutral placeholders; preserve every other key and original bytes. Physical/raw-cause bans inspect ORIGINAL serialized text AND decoded strings/keys; all identity bans remain on original sanitized history message. Add a negative instrument control duplicating an allowed identity into unexempt session.title; masking must still reject it. No exemption for title/summaries/task_id/parent_session_id. Implemented in the test-only source0138a88f55f705df9ba0927b42b5c3435ef81893; current-baseline probe results and limits are below. |

Real auth wrapper, handlers, workspace membership storage, store and both production gate paths remain real. No mock gate/approval, compatibility implementation or production change. The expected pre-implementation F1 failure is the old producer's required metadata-only200 control, before later eligibility assertions; this source-only expectation was not itself an executed F1 RED receipt. The approved GREEN-BE production baseline was subsequently merged normally. A current-baseline pass does not replace RED-before-GREEN evidence or independent gate-bypass mutation kills.

Future independent CHECK must separately bypass messages gateMainRead and detail resolveMainSessionForRead (one at a time), append ENOTDIR/permission-denied to an otherwise fixed messages error, and leak a forbidden fragment outside the explicitly permitted identity slots. This author never treats source presence as passed security acceptance. Exact source SHA/log/raw exits/named runs under slotrun remain mandatory; no full local suite.

## Executed current-baseline probes — not security gate closure

All three probes ran serially at **0138a88f55f705df9ba0927b42b5c3435ef81893**, after the authorized normal merge of GREEN-BE cdeba2d7ee9fd90741fd8c995d1d8c2fa14a30ee. Commands use the shared slot wrapper, workspace TMPDIR, `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1 -run '^<exact name>$'` on `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway`. Each exact full command and candidate SHA is saved in the corresponding `.meta` artifact beside its log.

| Exact test | Observed result / scope | Evidence |
|---|---|---|
| TestSF1_IdentityMaskDoesNotExemptUncontractedLocations | Named PASS, raw0. Instrument detects the duplicate identity in unexempt title and the JSON-escaped physical path; only checked exact nodes are masked. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/sf1-conditions/instrument-0138a88f5.log` |
| TestSF1_UnreadableTranscriptOfAValidMainLeaksNothing | Named PASS, raw0. Real handler reaches exact fixed messages refusal plus complete original/masked privacy checks. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/sf1-conditions/privacy-0138a88f5.log` |
| TestSF1_AuthenticatedIneligibleUnreadableMainRefusesBothReadGates | Parent plus BOTH messages_gateMainRead and detail_resolveMainSessionForRead named PASS, raw0. Valid-token positive control and both same-main ineligibility assertions were reached. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/sf1-conditions/eligibility-0138a88f5.log` |

The historical21-case RED receipt at4e2820d1951e7d66fdd01bc5a0e49e8e8f556028 shows the old metadata/carrier failure for the original named SF1 test, not the later strengthened privacy assertions. **New late-assertion RED proof is UNVERIFIED.** GREEN re-verification, implementation mutations, restored GREEN and Proof-of-failability are **deferred to CHECK**, a different instance; no self-CHECK or security PASS is claimed here.

| Claim | Evidence | Certainty |
|---|---|---|
| Test source and exact-node safeguards committed |0138a88f55f705df9ba0927b42b5c3435ef81893; two test files only | Verified source (high) |
| Current-baseline probe execution | Three exact logs above, raw0 each; both F1 subtest names present | Verified reached behaviors / instrument (high) |
| Security gate and new RED/mutation proof | No independent mutation or new pre-change receipt yet | Unknown / UNVERIFIED |
| **Self-check** | Re-read the test-only diff, all three named results/raw exits and both F1 gate names against F1/F2; preserved exact oracles and source/proof distinction | Verified source/probe self-check; independent acceptance open |
