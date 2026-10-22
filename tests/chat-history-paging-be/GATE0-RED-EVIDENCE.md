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

This is an implementation defect for GREEN-BE, not a fixture/compile/timeout failure. No production edit, skip, weakened assertion, full suite, Fly or author mutation. GREEN/mutation/Proof-of-failability and formal CHECK remain deferred to separate owners. Remaining queued defects are planned, not claimed authored/executed by this receipt.
