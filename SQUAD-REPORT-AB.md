# Squad AB report — issue #786

## Outcome

An answered AskUserQuestion card now becomes a permanent, correlated user-role transcript record before its resume turn is dispatched. The record uses the existing canonical resume-message shape, so replay can correlate it to the collapsed card and the model sees the same content that was durably recorded.

Code correct and tested: yes, within the scoped tests and local gates listed below.

Reachable by a user/agent: yes. `AskUserQuestion` remains registered in the built-in manifest, global policy defaults, and core-agent role policies, and the gateway wires the registry to the production shared session store.

## RED receipt

Permanent regression assertion:

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/agent/ask_user_resume_through_bus_test.go::TestAskUserResume_ThroughBus_ChatTargetAgent`

Command:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAskUserResume_ThroughBus_ChatTargetAgent$' -p 1 ./pkg/agent/
```

Failure on the original code:

```text
--- FAIL: TestAskUserResume_ThroughBus_ChatTargetAgent (1.24s)
    Error: Expected value not to be nil.
    Messages: the answered card must persist as a correlated user-role transcript record for card_id=ask_34bbdbf763a470d012e96a16e670bc0b (entry_count=1)
FAIL
exit=1
```

The replacement turn completed and received the answer; the real transcript still had only the pre-existing entry. This is the reported defect, not a harness failure.

A second RED established the free-text decision:

```text
--- FAIL: TestSubmit_PersistsHumanFreeTextAsCorrelatedUserTranscript (0.18s)
    Error: "[]" should have 1 item(s), but has 0
    Messages: a human card submission must add one transcript record
FAIL
exit=1
```

## Root cause

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/gateway/websocket_chat.go::wsHandlerHandleChatMessage.recordSessionAndTranscript` persists ordinary web-chat messages before publishing them to the in-process bus.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/gateway/ws_ask_user.go::askUserResumeDispatcher.DispatchResume` publishes AskUser resumes directly to that bus, bypassing the ordinary WebSocket intake writer.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/agent/loop.go::AgentLoop.processMessage` intentionally writes inbound user records only for non-web-chat channels, because normal web chat was already persisted upstream.

The AskUser resume therefore passed through a gap between two individually intentional ownership rules: it was web chat, so the loop skipped it, but it did not traverse the WebSocket path that normally writes web-chat messages.

## Fix

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/askuser/registry.go::Registry.dispatchResume` now receives the resolution origin explicitly.

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/askuser/registry.go::Registry.Submit` and `Registry.CancelByUser` request a durable user record.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/askuser/registry.go::Registry.fireDefaultSafeSet` explicitly does not.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/askuser/registry.go::Registry.persistUserResumeRecord` appends through the production session store before dispatch. If that append fails, the resume is not dispatched, so the agent cannot act on an answer absent from history.
- The narrow transcript capability is separate from `MetaStore`. GitNexus rated changing the broad `MetaStore` interface CRITICAL-risk with 397 dependants, so that interface was deliberately left unchanged.

No gateway/SPA wire format changed, so contract regeneration was not required.

## Persisted record decision

The record is role `user` and contains the canonical resume message:

```text
Answers to your questions (card_id=<id>): {"status":"answered","answers":[...]}
```

It retains:

- the card-id correlation;
- terminal status;
- question header and original question text;
- selected option labels; and
- free text, when typed.

The selected label belongs in history because a record saying only that a card was answered still would not explain what instruction the agent followed. Free text also belongs: text typed into a card is user-authored conversation content in the same sense as text typed into the composer. It receives the transcript's existing storage/replay treatment and is not added to logs; existing redaction tests still pass.

## Both resume origins

| Origin | Transcript policy | Coverage |
|---|---|---|
| Human submit | Persist canonical user-role record before resume | Through-bus selected-option integration test and free-text registry test |
| Human card cancel | Persist canonical user-role cancelled record before resume | Existing cancel/resume test |
| Server default-safe auto-submit | Do not create a user-role record | `TestDefaultSafeAutoSubmit_LogsFiring` asserts there is no user entry |

The auto-submit still persists terminal card state and emits its existing audit record, but it is not labeled as user speech because nobody typed or clicked that answer.

## GREEN receipts

The permanent integration test, run verbosely so the named test is proven to have executed:

```text
exit=0
=== RUN   TestAskUserResume_ThroughBus_ChatTargetAgent
--- PASS: TestAskUserResume_ThroughBus_ChatTargetAgent (1.31s)
PASS
ok  github.com/elicify-ai/omnipus/pkg/agent  6.376s
```

Origin, free-text, and persistence-failure tests:

```text
exit=0
=== RUN   TestDefaultSafeAutoSubmit_LogsFiring
--- PASS: TestDefaultSafeAutoSubmit_LogsFiring (0.20s)
=== RUN   TestSubmit_PersistsHumanFreeTextAsCorrelatedUserTranscript
--- PASS: TestSubmit_PersistsHumanFreeTextAsCorrelatedUserTranscript (0.40s)
=== RUN   TestSubmit_TranscriptPersistenceUnavailableDoesNotResume
--- PASS: TestSubmit_TranscriptPersistenceUnavailableDoesNotResume (0.32s)
PASS
ok  github.com/elicify-ai/omnipus/pkg/askuser  2.776s
```

Existing tests for all three changed call sites:

```text
exit=0
--- PASS: TestLifecycleLogging_SubmitDeliveredCountsOnly (0.35s)
--- PASS: TestSubmit_ValidatedAndResumes (0.28s)
--- PASS: TestCancelByUser_ResumesCancelledWithoutAnswers (0.30s)
--- PASS: TestTimer_AllDefaultSafe_ServerAutoSubmitsWithAudit (0.28s)
--- PASS: TestTimer_MixedSet_ResolvesPendingSubmitButNeverServerSubmits (0.44s)
PASS
ok  github.com/elicify-ai/omnipus/pkg/askuser  4.894s
```

The existing assertions in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/ab-card-record/pkg/agent/ask_user_resume_through_bus_test.go` were not edited or removed. The diff is addition-only for that file, and the complete named test passes.

## Revert-proof receipt

The production fix was removed while the permanent tests remained:

```text
production_fix_reverted=yes
=== RUN   TestAskUserResume_ThroughBus_ChatTargetAgent
    Error: Expected value not to be nil.
    Messages: the answered card must persist as a correlated user-role transcript record for card_id=ask_f8734391e27c046392be6207d4d00547 (entry_count=1)
--- FAIL: TestAskUserResume_ThroughBus_ChatTargetAgent (1.22s)
FAIL
exit=1
```

The fix was then restored, and the same verbose command produced the GREEN integration receipt above. The test therefore dies when the fix is reverted.

## Other verification

```text
make lint-guards
GUARD RUNNER: all 27 guards passed
guard_exit=0
```

```text
make lint-budgets
budget_exit=0
selfcheck: OK (check-file-budget.sh provably fails on offenders)
selfcheck: OK (check-function-budget.sh provably fails on offenders and passes clean/grandfathered cases)
```

```text
CGO_ENABLED=0 go vet -tags goolm,stdjson ./pkg/askuser
vet_exit=0
```

The changed integration test function is 141 lines, which triggers the warning threshold but remains below the 241-line failure threshold. No changed production function exceeds the limit.

GitNexus worktree scan: three changed files, 14 mapped symbols, zero affected processes, LOW risk. The graph index is stale enough to misidentify some exact symbols, so direct source tracing was used as the permitted fallback. The requested comparison to `origin/main` reports CRITICAL over 7,375 files because this release branch is broadly divergent from main; that branch-wide result is not attributable to this three-file fix.

## Honest gaps

- The full Go suite was not run locally because repository instructions prohibit it on this machine; scoped tests and local guards are green. CI remains the authority for the full suite.
- No live browser acceptance run was performed. The defect and fix are exercised through the real message bus and real transcript store, but not through a running SPA.
- No push or pull request was created, as required.
