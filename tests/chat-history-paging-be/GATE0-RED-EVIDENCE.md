# Gate round0 backend RED evidence — 2026-10-23

## F1 Critical — nonempty checkpoint root loss becomes false empty success

| Scope | Verified execution |
|---|---|
| Source | Current parent435ece31ff2e27a1ce4349eaf03506b81bd2b03b (feature implementation7c4bf181a, C1/I3 tests only above it), before any F1 production fix. QA changed only a new test plus test plan/evidence. |
| Exact test | TestChatHistoryCheckpoint_NonemptyMissingRootIsUnavailable in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_checkpoint_root_red_test.go |
| Command | `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1 -run '^TestChatHistoryCheckpoint_NonemptyMissingRootIsUnavailable$' github.com/elicify-ai/omnipus/pkg/session`, one serial local process under /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh |
| Raw result | exit1; genuine_empty_root_zero_remains_readable PASS; missing_root_member FAIL; zero_root_with_nonempty_coverage FAIL. |
| Actual failing assertion | assert.ErrorIs(err, ErrChatHistoryUnavailable) in the named test (line81 at this source). Expected unavailable/index_inconsistent; actual err=nil, entries0, rows0. Exact Reason assertion remains conditional on receiving the required error and is not claimed reached in this RED. |
| Instrument | Real canonical/index writer and real valid40-entry page before damage; root/rank40/covered ends/anchor present; actual generation length equals cp.MutationEnd; canonical file sizes equal covered ends (NO pending suffix). Close/reopen clears in-process state. Only root is removed or zeroed; every other private checkpoint member/canonical byte is retained. Actual empty root0/rank0 is an independent positive control. |
| Spec | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md::D1a inconsistent/missing baseline, D7a/B21/B31; explicit gate0/user F1 requirement. |
| Log | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/gate0-red-20261023/F1-checkpoint-root.log |

This is an implementation defect for GREEN-BE, not a fixture/compile/timeout failure. No production edit, skip, weakened assertion, full suite, Fly or author mutation. GREEN/mutation/Proof-of-failability and formal CHECK remain deferred to separate owners.

## F2 — interrupted empty main creation cannot resume

Parent production/test source5736ff0d5 (F1 tests only above unchanged feature implementation). Exact TestChatHistoryCreation_InterruptedMainCreationResumes runs in the real session package. Before each retry, the actual empty durable checkpoint has root0/rank0, empty archive/covered ends, nil anchor and ABSENT meta.json. The computed MainSessionID is unchanged. Two error-return cases and two actual child-process exit86/reaped cases cover empty_checkpoint_ready and before_identity_publish. Each fresh reopened GetOrCreateMainSession should resume ONLY this verified empty unpublished baseline; current code instead returns history baseline already exists. Failing assert.NoError is source line33 in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_creation_retry_red_test.go::TestChatHistoryCreation_InterruptedMainCreationResumes. Real crash/helper source /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_creation_retry_fixture_test.go.

| Subcase family | Actual result |
|---|---|
| empty_checkpoint_ready, injected failure and real crash | Both named FAIL: unexpected history baseline already exists on same-main retry |
| before_identity_publish, injected failure and real crash | Both named FAIL: same unexpected refusal |
| Nonempty unpublished archive | PASS: retry refuses, no tree overwrite |
| Inconsistent unpublished checkpoint | PASS: retry refuses, no tree overwrite |
| Conflicting published main owner | PASS: retry refuses, no adoption/rewrite |

Exact command `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1 -run '^TestChatHistoryCreation_InterruptedMainCreationResumes$' github.com/elicify-ai/omnipus/pkg/session`, serial under the same slot wrapper. Saved /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/gate0-red-20261023/F2-creation-retry.log: raw1,4 named FAIL/3 named PASS; actual child Wait exit86 twice. No build/setup/control failure. Spec D1a durable creation/restart, creation publication before identity and explicit user/gate F2. Successful-resume reader/type/owner/repeat assertions remain conditional on resume succeeding; they are NOT falsely claimed reached in RED.

Incidental existing warning observed in this failure fixture: the constructor labels no-meta directories safe to delete even when a durable empty baseline exists. Not fixed or used as an exemption by QA; routed as a note with the RED.

## F5 — unknown lifetime authority becomes known after real new activity

Exact TestSessionTokens_UnknownBaselineSurvivesRealNewActivity in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_usage_unknown_activity_red_test.go. Independent D16/D-U2 positive establishes actual1200+45123=46323 saved usage first. After close/join, remove only real stats.json or only tokens_total from its produced object. A new real engine accepts one unique user input, pauses at the actual external-provider request, then consumes the frozen700-token receipt. Actual new input count1, completed turn/delta700, canonical billed entries exactly1200/45123/700, provider calls1 and no reload model work all pass. No aggregate is manually seeded and no lifetime scan/page sum is accepted as authority.

Both named cases FAIL/raw1: after user acceptance, after the actual provider completion/flush, and after another close/reopen, actual core/REST/scoped attach/live done has a non-nil absolute total despite missing historical authority. The missing-member variant also incorrectly reports known before new activity. All23 failures are Expected nil, got *int; there is no unexpected-error, accounting mismatch, compile failure, timeout or panic. Failing statements include assert.Nil(ReadSessionTokensTotal) at source line103 and assert.Nil(done.Stats.SessionTokensTotal) later in the named function. Current exact numeric pointer values are NOT printed by assert.Nil, so this report claims non-nil false authority rather than fabricating observed dereferenced numbers.

Command: `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1 -run '^TestSessionTokens_UnknownBaselineSurvivesRealNewActivity$' github.com/elicify-ai/omnipus/pkg/gateway`, same serial slot wrapper. Saved /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/gate0-red-20261023/F5-unknown-new-activity.log: raw1,2 named FAIL. Production code remained feature7c4bf181a (subsequent F1/F2 commits on this QA branch are tests only); new F5 source remains unchanged from execution. Spec D16/FR-027/B55/B56 + explicit user/gate F5. This extends the earlier unknown fixture through actual new activity and incomplete stats documents; earlier tests did NOT establish this behavior.

No production fix, threshold widening, skip, author mutation or full-suite/Fly claim. GREEN-BE receives this tests-only RED; independent CHECK and later GREEN proof remain separate.

## F3 — proven retained-reference expiry rejected by offline reconstruction

Exact TestChatHistoryRepair_ProvenExpiredEffectsAreNotInvalidReferences in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_expired_effect_repair_red_test.go, with fixture /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_expired_effect_repair_fixture_test.go. Three real writer fixtures place an old tool owner/projection on day1 and newer settlement/projection/retraction on day2; actual90-day retention removes exactly the old rolled partition. Published retained catalog/generation and token tombstone prove expiry. Actual bounded indexed paging succeeds with exactly retained-one and its independent original position before any offline test.

All three proven-expiry subcases FAIL twice: real built standalone repair CLI dry-run returns1/class=invalid_reference instead of0 (assert.Equal at source line41), and the same actual PrepareChatHistoryIndex returns invalid_reference instead of success (assert.NoError line56). The unrelated-missing-owner negative uses a real canonical effect envelope with an intentionally unexplained same-retained-partition target; it PASSes both shared typed invalid_reference and actual CLI exit1 checks. This proves the test is not a blanket reference-error exemption. Canonical bytes stay unchanged; no fake native probe, expiry fact, repair success or constructor is substituted.

Exact command `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -v -p 1 -run '^TestChatHistoryRepair_ProvenExpiredEffectsAreNotInvalidReferences$' github.com/elicify-ai/omnipus/pkg/session`, same serial slot wrapper. The small standalone CLI is built inside this one narrow test (CGO0/tags); this is not a full local Go build/suite. /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/gate0-red-20261023/F3-expired-effect-offline.log: raw1,3 named expiry FAIL/1 unexplained-target PASS. Spec D13 shared constructor/reducer and D14 validated expiry distinction, B30/B36/B41, explicit F3 gate/user requirement. Post-success offline publication/readback checks remain conditional and are NOT claimed reached in RED. No production fix, skip, author mutation or Fly. Remaining queued defects are planned/in progress, not silently declared tested.
