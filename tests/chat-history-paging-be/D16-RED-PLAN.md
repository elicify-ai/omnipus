# D16 backend RED plan — #1418 / #1420

## Outcome, source and boundary

Preserve actually cancelled tool identity/state and restore authoritative recorded session usage through real canonical reopen, generated REST/detail and attach/current-state carriers. Expectations are derived FIRST from /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/docs/internal/specs/chat-history-paging-spec.md::D16, B52–B56, D-P3/D-U2, FR-026/027, T37/T38 and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/seams-decision.md::S. Published contract input be95728784b792b343027e1253ad3c4c3abcc324 was normally merged as093ac7a6af2c45b20c936f87e3d3c8cf65d98cd5; no backend implementation was changed by QA.

Real: native AgentLoop, ordinary gateway message ingress, tool dispatch/start, public Stop/Stop-all, actual cancellation timers/fences, canonical/addressed writers and effects, stored provider accounting, bounded history preparation, generated carriers, actual session hub and reopen. Controlled process edges only: model responses/usage and a registered external-tool boundary whose completion/cancellation can be released. Do not manually emit a successful/cancelled engine event or seed a UI object as writer proof. A separate writer-created saved-cancelled fixture tests projection only and is explicitly NOT the engine durability proof.

The old reconciliation CI window remains pinned to bfd63fecfa9d5edb4a95c379b0a1fc0b21fc5ba0. Its outputs do not cover this merge or these new tests. No new Fly window is started for RED; local execution stays one exact tagged test process at a time through /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/omnipus-l1/slotrun.sh. Native CI/kernel/UAT are not inferred from a local run.

## T37 — TestCancelledTool_ReloadPageSnapshotParity

| Input / sequence | Independent expected result | Source |
|---|---|---|
| Real provider requests one completed call, then one blocked call and an unexecuted queued call; actual Stop then forced cancellation | Actual start observed under original call/name/arguments/turn/agent; completed stays success, genuinely cancelled call has one durable cancelled display outcome; queued call is not executed or fabricated as cancelled | D16 cancelled durability, B52/B54, D-P3 |
| Stop-all variant on the same genuine engine boundary | Same per-call truth and original identity; scope is exercised through the real public Stop operation, not by changing a test flag in turn state | D16/B52; existing Stop scope unchanged |
| Confirmed hard cancellation followed by a controlled late successful tool return | Late success cannot replace cancelled; cancelled result is not admitted into model context; all owned work is released/joined before teardown | D16/B54 |
| Close/join producer, reopen canonical storage and fresh read/attach consumer | Same original completed/cancelled facts in actual archive, REST entries/detail tool_calls and generated ToolCallResultFrame.status=cancelled; exact owner/origin and no duplicate | B52/T37/section S |
| Recorded cancelled source at an older-page boundary with newer metadata/effects outside selected body | Same truthful bounded projection; one tool row per visible logical call, turn_canceled metadata0; preserve complete group/position and source ownership | B53/D4/D-P3 |
| Another session and unexecuted pending/queued call | Unrelated/completed states unchanged, no cross-session cancellation or invented execution; no reload/paging re-executes a tool or resends input | B54/FR-026 |
| Separate actual canonical saved-cancelled fixture (projection-only control) | Generated REST ToolCall already accepts cancelled; actual replay result must preserve cancelled rather than clamp error; cancellation marker remains0 rows | D16 contract profile; not a substitute for first row |

Positive controls must precede the durability-specific failure: actual completed result, actual running-call start, accepted public Stop and genuine turn/cancellation completion. A missing saved cancelled display record is a loud named RED, not a skip or a reason to fabricate it. Independently reachable projection subcases exercise the generated cancelled field even if the writer case fails first. Existing pending-result defects are reported precisely, never rescued by fake completion or counted as new fallback acceptance.

## T38 — TestSessionTokens_ReloadAbsoluteTotalParity

Frozen provider receipts are1200 then45123, yielding exactly46323; third genuinely accounted turn700 yields47023. Cache fields are populated consistently so PromptTokens+CompletionTokens is deliberately not the full provider total. These are spec D-U2 constants, not numbers guessed from the rounded screenshot.

| Input / sequence | Independent expected result | Source |
|---|---|---|
| Two real ordinary engine turns with frozen provider receipts | Canonical per-call accounting and actual stored SessionStats.tokens_total equal1200, then46323 before carrier checks; no repeated total per fragment | D16 usage authority, D-U2 |
| Add genuine writer-created display-only groups so old billed source is outside latest40 | REST SessionHistoryResponse.session_tokens_total and SessionDetail.session.stats.tokens_total remain46323, independent of page sum; capture actual bounded I/O where available | B55/FR-027 |
| Fresh attach/current state and live turn done | Generated SessionStateFrame.session_tokens_total and DoneStats.session_tokens_total carry known absolute totals; DoneStats.tokens remains per-turn1200/45123/700 | D16 carrier rules |
| Replay terminator and connection-open unscoped state | Omit absolute baseline; frames_emitted is diagnostic, not a token baseline; no fake0/default field | D16/section S4 |
| Actually unused separate session | Authoritative0 is present as0, not nil or borrowed46323 | B55/D-U2 |
| Closed historical storage with unavailable/absent aggregate evidence | Omitted baseline means unknown, never manufactured0 or hidden lifetime recount; preserve other session's known total | D16/B56 |
| Bind/hold attach cut, genuinely account third turn, release selected replay/current controls then held live done | Absolute observations retain their own real cut/facts; current stored total47023 and next attach47023; held done is700 delta plus47023 absolute, not an extra absolute debit | B56/T38 |
| Hold a real prepared REST response while a genuine newer done completes; release stale page, Retry/older/replay repeats | Stale absolute page observation cannot mutate the server's newer accounting; total stays47023, provider calls/tool/input counts unchanged; another session remains0 | B56; frontend application fencing separately belongs to T39 |

No hand-written wire structs: use generated MessageFrame/AuthFrame/SessionHistoryResponse/SessionDetail/SessionStateFrame/DoneFrame/ToolCallResultFrame and actual raw bytes for schema/omission checks. Do not weaken structural optionality: new producers' presence rules are semantic assertions on known authoritative fixtures.

## Proof order and limits

Write independent cases and instrument controls, run exact top-level T37 and T38 serially on the current merged production, inspect named expected-versus-actual failure and save raw exit/log/source hashes. Compile/setup/no-match failure is not accepted behavioral RED. No production fix, schema widening, fake live publication, archive rewrite, silent skip, cap/timeout widening, self-CHECK or new external dependency. Missing required brief API anywhere must be an explicit fatal BLOCKED test plus finding. Step4 items2/3 and production mutation/Proof-of-failability remain deferred to fresh CHECK. Go tests establish backend authority/carriers, not a DOM footer/composer or real browser reload; T39/T40/T41 and exact-commit independent UAT remain separate.
