# UAT Root-Cause Analysis — Delegation Tools (2026-07-31)

Follow-up to `uat-report-delegation-tools-issues-consolidated-2026-07-31.md`. Each of the
14 issues (ISS-001–ISS-014) was independently re-investigated by direct code inspection
(not re-derived from the UAT tester's own diagnosis) to confirm or refute the reported
root cause before any fix work begins. Three competing hypotheses were considered per
issue where the cause was not immediately obvious from a single read.

**Result: 9 confirmed real bugs, 5 refuted as by-design (3 of those need better tool-facing
documentation, not a code fix).** GitHub issues filed for all 9 confirmed bugs — see the
table below for links.

## Confirmed bugs

| ID | Summary | Root cause (file:line) | Severity | Fix scope | Issue |
|---|---|---|---|---|---|
| ISS-001 | `message_parent` fails for every delegated child | `pkg/tools/message_parent.go:381` reads `ToolTranscriptSessionID` (parent's shared id) instead of `ToolDelegateSessionID` (child's own id, which is what the durable session record was persisted under) | P0 | One-line fix | #576 |
| ISS-002 | `cancel` (soft+hard) has zero effect on the target subagent | `pkg/tools/delegate.go` executeCancel passes the per-delegation `sessionKey` into `InterruptSession`/`InterruptSessionHard` (`pkg/agent/session_messaging_wire.go:156`), which filter `activeTurnStates` by `transcriptSessionID` — a different, deliberately-shared namespace (`pkg/agent/subturn.go:1034`). Zero matches → documented no-op → unconditional success message | P0 | Structural — needs a new `sessionKey`-keyed interrupt path | #577 |
| ISS-005 | Delegation timeout always 5 minutes, no working override | `args["timeout_seconds"]` is never read anywhere in `pkg/tools/delegate.go`; `SubTurnConfig.Timeout` is never populated on either dispatch path. Worse than UAT reported: not just `0` maps to 5min — **every value including an explicit override is silently ignored** | P1 (upgraded from P2) | Parse the arg, thread into `SubTurnConfig.Timeout` | #580 |
| ISS-003 | `create_task(plan_id=...)` always fails — "plan store is not configured" | `TaskCreateTool.planStore` has a `SetPlanStore` setter that only unit tests call; `wirePlanToolsForAgent` (which wires `create_plan`/`execute_plan`) never wires `create_task`. A test comment confirms the team knew this was a separate follow-up slice that was never finished | P1 | Two `SetPlanStore` call additions in `pkg/agent/loop.go` | #578 |
| ISS-004 | `follow_up` doesn't inject the new instruction | Schema documents `follow_up` alongside `steer`/`respond` (which both use the `text` field), but `executeFollowUp` (`pkg/tools/delegate.go:2206`) only reads `args["task"]`, silently falling back to a generic "Continue the previous task." placeholder when absent | P1 | Accept `text` (alias `task`), fix schema docs, reject empty instruction instead of silently substituting | #579 |
| ISS-010 | `update_task` can only be called by the assignee, not the creator | `pkg/tools/task.go:915-917` checks only `AgentID != callerID`; its sibling `TaskDeleteTool` (same file, line ~1234) correctly checks assignee OR `CreatedByAgent` with an explicit design rationale — `update_task` never got the same fix ported | P2 | One-line predicate change, mirror `delete_task` | #584 |
| ISS-006 | `steer` on an already-terminal session sometimes returns "queued" instead of an error | The terminal check exists (`pkg/tools/delegate.go:1925-1961`) but is a plain `Load()` + branch — a TOCTOU race against the concurrent atomic terminal transition (`pkg/agent/task_executor.go`). `EnqueueSteeringMessage` has no liveness check, so a race-window steer is queued into an orphaned map entry nobody ever reads | P2 | Route through the store's `Mutate` atomic primitive (already used by `executeRespond`), or verify liveness at enqueue time | #581 |
| ISS-007 | Bash tool blocks a simple bounded `for` loop | Not the loop syntax — a blanket `\$\([^)]+\)` regex (`pkg/tools/shell.go:231`) bans *any* command substitution, making four narrower, purpose-specific dangerous-substitution patterns immediately below it dead code. Empirically confirmed: the loop alone (no `$(...)`) passes; `$(seq 1 5)` alone trips it | P2 | Remove/narrow the blanket pattern; rely on the specific ones already present | #582 |
| ISS-008 | `list_jobs` reports `actionable: false` for every running subagent | The computation is correct, but `wireJobRosterForAgent` (`pkg/agent/loop.go:4696-4749`) never calls `SetSessionResolver` in production — the code's own comment admits it's unwired. Unit tests pass because the test wires it manually | P3 | Add `DelegateTool.ResolvableSessionIDs` (batch), wire it in `loop.go` | #583 |

## Refuted — working as designed

| ID | Summary | Why it's not a bug |
|---|---|---|
| ISS-009 | `list_jobs` hides terminal rows by default | Deliberate, documented in the tool's own `Description()`; `notes.terminal_suppressed` is populated whenever rows are hidden. UAT tester's own framing was "easy to miss," not "wrong." |
| ISS-011 | Worker agent can't delegate to anything | Worker is the seeded generic leaf agent; `coreAgentDelegation`'s code comment explicitly states the `nil` outgoing-delegation seed for Worker "is load-bearing, not incidental." An operator can add a `worker → X` edge via the workspace Team tab if desired — nothing prevents it, it's just not seeded by default. |
| ISS-012 | `create_plan` times out waiting for approval in automated contexts | Intentional consent-gating (ADR-052 FR-005): every seeded agent except Jim is `ask` for plan/task-dispatch tools. The UAT report's note that this shares a mechanism with ISS-005 is incorrect — it's the independently-configurable `pkg/gateway/approvals.go` timeout, not the sub-turn context deadline; they only coincidentally share the same default magnitude (5 min). |
| ISS-013 | Tasks with acceptance criteria can't be force-completed via `update_task` | Deliberate anti-self-certification-bypass gate (ADR-049 C1/SD-B2) — closes a bug class where a worker could previously skip judge adjudication by calling `update_task(done)` directly. |
| ISS-014 | `list_tasks`/`create_task` both appear denied via `load_tool` for Worker | `create_task` genuinely denies (correct, consistent on both enforcement paths). `list_tasks` actually succeeds with an "already available" message — but a batched `load_tool(names=["list_tasks","create_task"])` call sets the aggregate `IsError=true` because one of the two names failed, making the whole call read as a denial. No real cross-path inconsistency exists; this is a `load_tool` batch-result UX artifact. |

## Documentation debt (no code fix, tool-facing docs only)

ISS-009, ISS-011, and ISS-013 are all real behaviors that are correct but under-surfaced to
the calling agent/operator before they hit the restriction. Bundled into one tracking issue:
#585.

## Methodology note

Every root cause above was independently verified by direct `Read`/`Grep`/GitNexus
inspection of the current `feature/plan-swimlane-board` tree — not inferred from the UAT
report's own text. Three of the nine confirmed bugs (ISS-004, ISS-005, ISS-014) turned out
to have a different or more severe root cause than the UAT tester's own diagnosis; this is
reported explicitly per-issue above rather than silently corrected. No code was changed as
part of this analysis — see the linked GitHub issues for fix tracking.
