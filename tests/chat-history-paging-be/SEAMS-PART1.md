# Seam conversion plan — final architect decisions, part 1

This supersedes the earlier handoff's missing-name and malformed-query-copy notes. It does not assert that the newly agreed production APIs are implemented.

Authority read in full: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md`. The fetched specification amendment is `34612f1720ea22a5a47599c658f16dd14ff3ae0e` on `origin/feature/1266-chat-history-paging`; the complete diff was read. Original BDD/dataset oracles remain independent and unchanged except the architect-authorized D11/diagnostics clarifications.

| Area | Agreed target / conversion |
|---|---|
| Reader | `UnifiedStore.ReadChatHistoryPage`, core `ChatHistoryPageOptions`/`ChatHistoryPage`, canonical `HistoryPosition` parser/encoder, injected `ChatHistoryWeigher` and validated max-carrier `ChatHistoryWeight`. No wire struct substitutions. |
| Recovery | Same-package `reconcileChatHistoryIndex`, `initEmptyChatHistoryIndexLocked`, exact0/1/63/64/65 combined items, shared exported `PrepareChatHistoryIndex`/`VerifyChatHistoryIndex` builder; no lifetime online construction. |
| Faults | `chatHistoryPublicationHook` exact events and `chatHistoryWriteCheckpoint`; actual crash children exit86, reopened store/process, real durable files; no copied temp-file implementation. |
| Read barriers | `chatHistoryReadHook` at actual `record_opened`, cut/dependency/suffix/anchor/overflow physical observations, append completes before release, join cleanup. |
| Retention | `readChatHistoryIndexState`, `chatHistoryRetentionGeneration`, bounded `lookupChatHistoryTombstone`, real token/rank-only disk retirement; native Windows actual sharing-busy vs true permission errors. |
| Prepared gateway | Real `newHistoryPageRequest(...).Read(...)`, `preparedHistoryPage`, `historyPageProjectionDeps`; no side-effecting count-sink replay, guessed WS weight or stubbed unit under test. |
| Artifact | Session-private `.chat-history/FORMAT` exact `omnipus-chat-history-index/1\n`, checkpoint.json, generations/staging; unlisted codecs/log names are not test guesses. |
| Rolled fixture | QA sole-owned `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/session/chat_history_fixture_test.go`: `buildChatHistoryDayFixture`, real clock/writer/rollover, `TestChatHistoryFixtureDriver` and strict private v1 plan/receipt. Only that utility driver skips without its explicit plan, as section G permits; no feature test skips. |
| T11 | Malformed unrecognized key/value is400 `invalid history query`, after access and before recognized-field validation, both validation settings. Existing recognized-field error oracles stay. |
| T23 | Production bridge/file sink: exact `message=history_page`, `event=history_page`, carrier/mode/outcome, every physical-work counter and conditional metadata. Archive totals equal five disjoint categories. No session/agent/user IDs or guessed process allocation field. `allocated_bytes` belongs only to isolated T18 measurement receipts. |

## Safe sequencing

Team-lead/squad decision: backend-lead first supplies its own agreed production declarations with explicit named unimplemented errors. RED does not author production stubs, add skip/build tags, use reflection/unsafe matching or commit uncompilable tests referencing absent names. When the declaration SHA is supplied, merge it normally into `red/1266-be` and replace the remaining placeholder assertions with direct typed calls and spec-derived runtime assertions. Until then, the existing-surface query/log cases and test-only rolled driver are independent work.

The original T12/T13 duplicate-terminal-result question is pending architect confirmation: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_range_ownership_red_test.go`::TestSessionHistory_ToolOnlyFrameOriginAndPendingTruth is intentionally left byte-identical.

T30's decided source remains `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/cmd/chat-history-index-repair/main.go`; the command test may retain its loud missing-main fatal if that real source remains absent. No missing executable is interpreted as an expected refusal exit.

## Fixture-only handoff

Pushed driver-only commit: `0f6fbfbded522d8ead2863add33b37e335b7f841`. It adds only the Go fixture file. RED-E2E consumes this alongside its prebuilt-driver bridge; no frontend files are changed here.

Driver verifies the actual current `GatewayProcess` temporary-home prefix `omnipus-e2e-gwproc-`, containment under native os.TempDir, config.json/sessions presence, no root/session symlink, and a plan outside the source. The caller still must prove its gateway process exited before invoking the bridge. Driver closes and joins its store before emitting exactly one `CHAT_HISTORY_FIXTURE_RESULT=` receipt. Construction uses actual `AppendTranscriptAddressed`; no independent index/projection/token oracle is calculated by the builder.

Exact narrow baseline test at that commit exited1 with `G requires a healthy new-session baseline before seeding` and missing `.chat-history/FORMAT`, not a compile error. Log: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/squad-1266/red-be-20261021/seams-part1/driver-baseline-red.log`.

Every further claimed run must have a saved exact command, tested SHA, named failure and raw exit code. Exact local cases only under slotrun, serially; heavy/broader gates stay on Fly. Native Windows, GREEN and mutation proof remain unverified/deferred to independent CHECK until actual evidence exists.
