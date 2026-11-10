# Round1 pr-test-analyzer R2 — stable third attempt

## Plan frozen before test edit

After R1-1 RED pack12163311e344aae6a3039b42aa83f5b0cf38547e was pushed, extend only /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_retention_retry_bound_red_test.go::TestChatHistoryRead_RetentionChurnStopsAfterThreeAttempts. Oracle: explicit additional user R2 and current spec T48/B63/W: success when the cut stabilizes on attempt1,2 OR3, no fourth attempt, and typed index_inconsistent when the THIRD cut still moves.

| Input | Frozen expected |
|---|---|
| churn0 | Success, attempts1, moves/retries0, exact retained041..080 |
| churn1 | Success, attempts2, moves/retries1, same exact retained entries/positions |
| NEW churn2 | Success, attempts3, moves/retries2, same exact retained entries/positions |
| churn4 | Existing exhaustion unchanged: attempts3, moves/retries3, typed unavailable/index_inconsistent; never chase stable fifth cut |

Retain actual append-day fixture, real per-cut mtime eligibility/RetentionSweep/compaction and current canonical byte invariance. Add2 to the existing0/1/4 table. Strengthen positive entry oracle from length+endpoints to ALL40 exact IDs and ALL original independently derived D2 positions, without deleting coverage. No stopwatch or timeout oracle. No production implementation change.

Impact: GitNexus ToolSearch returns no matching tool; direct known-present symbol/caller sweep finds this test definition and its evidence descriptions only, no production caller. Test-only impact Inferred, not graph-certified. Worktree clean at expected12163311e before edit; no unexpected commits.

Current backend already contains the retry fix, so this coverage addition may be GREEN on first execution. Report that baseline honestly; do not fabricate historical RED. A mutant that refuses every attempt3 before checking stability must fail the new churn2 case while original churn4 exhaustion still passes. Production mutation and Proof-of-failability are deferred to a fresh CHECK instance, never this RED author. Exact narrow tagged session test only, serial Bash slotrun, save raw exit/named case/source hashes; no Fly/full suite/held F5-list work.

## Actual author baseline

Source parent12163311e344aae6a3039b42aa83f5b0cf38547e with required backend980a1a2f23f186172d50a742882a4e02a98a03b9. Exact `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1 -run '^TestChatHistoryRead_RetentionChurnStopsAfterThreeAttempts$' github.com/elicify-ai/omnipus/pkg/session`, one queued serial Bash slotrun process. Saved /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/gate1-red-20261023/R2-stable-third-attempt.log: raw exit0, all four named churn0/1/2/4 PASS. NEW churn2 actually succeeds with exactly3 real captured attempts,2 actual sweep/generation moves and RetentionRetries2; all40 exact IDs and original D2 positions pass. Existing churn4 still returns typed index_inconsistent after exactly3 moved cuts/retries, with no fourth attempt. Retained canonical bytes unchanged in every case.

This is an executed coverage-addition GREEN baseline on already implemented behavior, not a claim of historical RED. No production change or author mutation. A stable-third-rejecting mutant must be checked by a DIFFERENT qa-lead in its scratch clone: run the new exact /actual_retention_moves_2 subcase (must die) and unchanged /actual_retention_moves_4 exhaustion control (must pass); restore immediately. That mutation execution, full CI and formal CHECK remain UNVERIFIED/deferred, not silently certified. Source hash frozen before run and exact input unchanged at its saved result.

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis, omnipus-failure-triage, github-cli, commit-messages, jev-use:jev-use.

| Claim | Evidence | Certainty |
|---|---|---|
| Stable-third case executed and passed | Exact narrow command/log above, exit0; named actual_retention_moves_2 PASS with3 attempts/2 retries/exact40 IDs+positions asserted | Verified high |
| Earlier success and exhaustion retained | Same log named actual_retention_moves_0/1/4 PASS; original exhaustion branch unchanged | Verified high |
| Correct added-test failure sensitivity | Added require.NoError on actual stable-third read directly rejects premature-third-unavailable; independent scratch mutation not run by author | Inferred logic; execution Unknown/deferred CHECK |
| Self-check | Re-read actual raw0/named4 log and final test diff against user R2/T48; confirmed real sweep/no timeout, all exact IDs/D2 positions, unchanged exhaustion and source hash; only test+evidence authored | Verified high |
