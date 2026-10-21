# US1.6 real ordinary-send / approval / Stop test plan — draft

Target: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/gateway/chat_history_us16_controls_test.go`::TestUnavailableHistory_US16_CurrentApprovalSendAndEngineStop.

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md`::User Story 1 criterion6 / D7a / D15 / FR-023; architect Section P in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md`. This is Go-level admission and current-control authority, not a browser composer claim.

## Boundary

Use the real message bus, ordinary AgentLoop.Run dispatch, authenticated WebSocket ServeHTTP/read loop, canonical store, unavailable-index attach path, real registered write_file tool, real approval registry/adapter, authenticated approval POST and cancel frame. Only the external model transport is scripted: first response requests write_file, second call records the genuine tool result and blocks on its actual context until Stop. No direct canonical append for the tested instruction, ProcessScheduled substitute, fake tool, fabricated pending approval/event, private-state injection or fake cancel.

Create old-data history using the existing canonical-copy fixture; require its missing-index error and completed live_only controls BEFORE the ordinary instruction. File and process activity remain inside an owned workspace temporary root. Channels and actual completion/join are synchronization; deadlines are hang backstops, never quiet-period or elapsed-time assertions.

## Cases and invariants

| Stage / input | Exact expected result | Oracle |
|---|---|---|
| Authorized attach to real unindexed history | Session-scoped unavailable/index_missing/fixed pagingUnavailable; no snapshot/replay done; live_only complete | D7a, fixed existing error contract |
| One normal generated message frame with client_message_id | One canonical user instruction and received receipt; no partial index; engine invokes provider once | D7a New sends / FR-023, normal save-before-ack contract |
| Provider asks real write_file under explicit Ask, Auto off | One genuine pending approval for same session/agent/call/turn and exact args; file absent and no second provider call before approval | D7a current controls, existing Ask policy contract |
| Explicit history Retry attach without cursor | Same unavailable state and same pending approval; no resend, new turn or partial index | D7a Retry |
| Disconnect and authenticated reconnect/attach | Same history error/live_only completion; genuine current approval and active turn restored | US1.6 / D7a |
| Real authenticated allow_once POST | Typed200 matching approval/action/status; actual file contains exact requested bytes; provider second call receives real tool result | D7a current controls, approval API / builtin write_file contract |
| Retry while resumed provider call is still running | Same unavailable history; active turn stays exposed; no cancellation, duplicate input or relaunch | D7a / FR-023 |
| Actual generated cancel frame on the authenticated connection | Real provider context is cancelled; engine turn actually ends; one turn dispatch, one real tool execution and two provider invocations total | D7a / D15 / B49 |
| Final canonical/index checks | Exactly one tested input, no derived index, history still refuses rather than claiming success | D7a / B42 |

## What would break this (independent CHECK only)

Remove live_only completion; suppress pending approvals on unavailable attach; turn Retry into a resend/relaunch; fabricate a receipt before persistence; bypass the real approval action or block Stop after history error. CHECK must exercise relevant mutants outside the feature worktree. RED author never claims those mutation kills.

## Known gaps / proof status

Test source is implemented in the five dedicated gateway test files; it has not yet been compiled or executed. The original partial scheduled-turn/Stop test and separate canonical-append/incomplete-control negatives remain untouched. The branch already carries the explicitly approved GREEN-BE baseline; a passing current-baseline run alone is not new RED-before-GREEN proof. Preserve the named test and assertions and record the actual reached failure or pass honestly; no production mutation by this author. Browser focus/composer, genuine secret handling, platform certification and exact-commit UAT remain separate lane obligations.
