# Feature Specification: Agent Loop Reliability — OpenClaw Parity

**Created**: 2026-03-31
**Status**: Revised (post-grill-spec review)
**Input**: Omnipus community stability analysis, OpenClaw agent loop reverse-engineering, Omnipus codebase audit, adversarial review findings

---

## Architecture Decision: Retry Layer Responsibility

The existing codebase has TWO retry/failover layers:

1. **Inline retry loop** (`runTurn`, loop.go:2056) — currently handles timeout + context overflow only
2. **FallbackChain** (`fallback.go:102`) + `CooldownTracker` + `expandMultiKeyModels` — handles provider failover, multi-key rotation, cooldown management

**Decision**: Provider-transient errors (429, 5xx, auth) are handled by the **FallbackChain** (which already has key rotation, cooldown, and multi-candidate support). The inline retry loop keeps ONLY turn-level concerns (timeout with compaction, context overflow with compaction, empty response retry). This avoids double-retry and leverages tested existing code.

**The real gap**: The `callLLM` function inside `runTurn` calls the provider directly, bypassing the FallbackChain for single-model setups. Fix: ensure `callLLM` routes through the FallbackChain when multiple keys/candidates exist, even for single-model configs. `expandMultiKeyModels` (config.go:1396) already creates separate candidates per key.

---

## User Stories & Acceptance Criteria

### User Story 1 — Turn-Level Timeout with Auto-Recovery (Priority: P0)

A user wants complex multi-tool tasks to complete within a predictable time, and when something takes too long, the agent should recover automatically instead of hanging forever.

Currently, a turn can run indefinitely. OpenClaw enforces `timeoutSeconds` per turn, auto-compacts on timeout, and retries.

**Why this priority**: This is the #1 cause of the "agent stops mid-process" experience.

**Independent Test**: Set `timeout_seconds: 10`, trigger a turn that takes >10s, verify the turn times out, compacts if needed, and retries or returns a timeout message.

**Acceptance Scenarios**:

1. **Given** an agent with `timeout_seconds: 300`, **When** a turn exceeds 300s, **Then** the turn context is cancelled, the agent checks if context usage > `SummarizeTokenPercent` (default 75%, configurable), compacts if so, and retries the LLM call once.
2. **Given** a turn timeout during an LLM call (no tools in progress), **When** the timeout fires, **Then** the LLM call is cancelled via context, and the retry logic runs.
3. **Given** a turn timeout during tool execution, **When** the timeout fires:
   - File write tools: the current atomic write completes (WriteFileAtomic is crash-safe by design)
   - Shell foreground commands: the process is sent SIGTERM, waited 5s, then SIGKILL
   - Web fetch: the HTTP request is cancelled via context
   - **Then** tool cleanup is best-effort (non-blocking), and the timeout handler proceeds with compaction/retry.
4. **Given** a turn timeout where auto-compaction succeeds and retry also times out, **Then** the agent returns partial content + "(Turn timed out after retry. The response may be incomplete.)" — no infinite retry loop.
5. **Given** `timeout_seconds: 0` or not configured, **When** a turn runs, **Then** no timeout is enforced (backward compatible).
6. **Given** a turn timeout fires mid-stream (tokens already streamed to user), **Then** the partial streamed content is preserved, and the timeout message is appended.
7. **Given** a sub-turn is in progress when the parent turn times out, **Then** the sub-turn's own timeout (`SubTurnConfig.DefaultTimeoutMinutes`) governs the sub-turn independently. The parent turn timeout does NOT cancel child sub-turns (they have their own context). The parent's retry proceeds without waiting for the child.

---

### User Story 2 — Route Retryable Errors Through FallbackChain (Priority: P0)

A user wants the agent to automatically recover from transient LLM errors (rate limits, server errors, auth token expiry) using the existing multi-key rotation and provider failover infrastructure.

Currently, `callLLM` in `runTurn` calls the provider directly, bypassing the `FallbackChain` for non-timeout errors. The FallbackChain already handles key rotation, cooldown, and multi-candidate failover — but it's only used when multiple model candidates are explicitly configured.

**Why this priority**: Rate limits (429) are the most common production LLM error. The fix leverages existing code.

**Independent Test**: Configure a provider with 2 API keys. Invalidate the first. Send a message. Verify the FallbackChain rotates to the second key.

**Acceptance Scenarios**:

1. **Given** an LLM call fails with a retryable error (429, 5xx, auth), **When** the FallbackChain has multiple candidates (from `expandMultiKeyModels`), **Then** the next candidate is tried with the existing cooldown/backoff logic.
2. **Given** a single API key that returns 429, **When** the FallbackChain has only one candidate, **Then** the CooldownTracker applies backoff and the candidate is retried after the cooldown period (respecting `Retry-After` header if present).
3. **Given** all candidates are exhausted, **When** the FallbackChain returns `FallbackExhaustedError`, **Then** the error is surfaced to the user with an actionable message.
4. **Given** a billing error (402), **When** classified by `ClassifyError`, **Then** the CooldownTracker disables the provider (existing behavior: `disableBilling`) — no rotation to other keys on the same account.
5. **Given** a non-retryable error (format, context overflow), **When** classified by `ClassifyError`, **Then** no fallback retry occurs — the error is returned to the inline retry loop for turn-level handling (compaction).

---

### User Story 3 — Never Lose a Completed Response (Priority: P0)

A user wants every response the agent generates to be delivered, even when post-turn processing encounters errors.

Currently, there are 4 early-return paths in post-turn processing (loop.go:445-525) that exit without publishing the response.

**Why this priority**: This directly causes the most-reported Omnipus issue.

**Implementation approach**: Use a `defer` pattern at the top of the `Run()` goroutine that publishes `finalResponse` if it's non-empty and hasn't been published yet. This catches ALL return paths automatically (current and future). A `published` boolean flag prevents double-publish. The `defer` checks `HasSentInRound()` — if a tool already sent a message via `MessageTool`, the defer does NOT publish (the tool's message IS the response).

**Independent Test**: Send a message that triggers steering. Verify the response is delivered even if steering fails.

**Acceptance Scenarios**:

1. **Given** a turn completes with `finalResponse = "Hello"`, **When** `buildContinuationTarget()` at line 458 fails and returns early, **Then** the deferred publish sends "Hello" to the user.
2. **Given** a turn completes, **When** `Continue()` at line 486 returns an error, **Then** the deferred publish sends the original response.
3. **Given** a turn completes, **When** `Continue()` at line 489 returns empty string, **Then** the deferred publish sends the original response.
4. **Given** a turn completes, **When** `Continue()` at line 513 returns an error during drain, **Then** the deferred publish sends the original response.
5. **Given** a tool already sent a response via `MessageTool` (`HasSentInRound() == true`), **When** the defer fires, **Then** the defer does NOT publish (no duplicate message).
6. **Given** `finalResponse` is empty (turn produced no content), **When** the defer fires, **Then** nothing is published (no empty message).

---

### User Story 4 — Ensure FallbackChain is Used for All LLM Calls (Priority: P1)

A user with multiple API keys wants the existing FallbackChain infrastructure to be used even for single-model setups.

Currently, `expandMultiKeyModels` creates separate candidates per key, and `FallbackChain.Execute` handles rotation. But `callLLM` inside `runTurn` may bypass this when only one model is configured (even if it has multiple keys).

**Why this priority**: The infrastructure already exists and works. The gap is that it's not always invoked.

**Independent Test**: Configure one model with 2 API keys. Verify both appear as FallbackChain candidates and rotation occurs on failure.

**Acceptance Scenarios**:

1. **Given** a model with 2 API keys, **When** `expandMultiKeyModels` runs, **Then** 2 separate `FallbackCandidate` entries are created.
2. **Given** 2 candidates from the same model, **When** candidate 1 fails with 429, **Then** the FallbackChain tries candidate 2.
3. **Given** the FallbackChain is used for a single-model config, **When** the CooldownTracker has candidate 1 in cooldown, **Then** candidate 2 is tried first.

---

### User Story 5 — Empty LLM Response Recovery (Priority: P1)

A user wants the agent to handle empty LLM responses gracefully instead of silently stopping.

**Acceptance Scenarios**:

1. **Given** the LLM returns empty response (no content, no tool calls), **When** this is the first empty response in the turn, **Then** the inline retry loop retries the LLM call once (within the existing `maxRetries` budget).
2. **Given** the LLM returns empty on retry, **When** the second attempt is also empty, **Then** the agent sends: "I was unable to generate a response. Could you rephrase your request?"
3. **Given** the LLM returns empty but with `ReasoningContent`, **When** Content is empty but ReasoningContent is not, **Then** ReasoningContent is used (current behavior preserved).
4. **Given** the LLM returns whitespace-only content (e.g., `"   "`), **When** `strings.TrimSpace(content) == ""`, **Then** this is treated as empty (same as no content).

---

### User Story 6 — Background Process Hard Kill (Priority: P1)

**Acceptance Scenarios**:

1. **Given** a background process running for longer than `exec.max_background_seconds` (default: 300), **When** the timeout fires, **Then** SIGTERM is sent, 5s grace period, then SIGKILL.
2. **Given** `exec.max_background_seconds: 0`, **Then** no timeout enforced (backward compatible).
3. **Given** multiple background processes hitting timeout simultaneously, **When** all receive SIGKILL, **Then** cleanup is sequential (not a SIGKILL storm).

---

### User Story 7 — Graceful Shutdown with Active Turn Wait (Priority: P2)

**Acceptance Scenarios**:

1. **Given** an active turn, **When** SIGTERM arrives, **Then** the gateway waits up to `shutdown_timeout_seconds` (default: `min(timeout_seconds, 60)`) for the turn.
2. **Given** the turn completes within the timeout, **Then** sessions are saved and gateway exits cleanly.
3. **Given** the turn does NOT complete, **Then** turn contexts are force-cancelled and gateway exits.

---

## Behavioral Contract

Primary flows:
- When the turn exceeds `timeout_seconds`, the system auto-compacts (if context > `SummarizeTokenPercent` % full) and retries once, or returns a timeout message with partial content.
- When an LLM call fails with a retryable error, the FallbackChain handles retry/rotation using existing CooldownTracker and multi-key candidates.
- When any post-turn code path returns, the deferred publish ensures the completed response is delivered.
- When the LLM returns empty, the inline retry loop retries once before sending a fallback message.

Error flows:
- When all FallbackChain candidates are exhausted, the last error is returned with actionable guidance.
- When a non-retryable error occurs, no retry/fallback — error returned immediately.
- When forceCompression fails during timeout recovery (e.g., summary LLM call itself times out), the turn returns the partial response + timeout message WITHOUT retrying compaction.

Boundary conditions:
- When `timeout_seconds: 0`, no timeout enforcement (backward compatible).
- When `max_retries: 0` in the inline loop, no retries for timeout/context — errors returned immediately.
- When the turn timeout fires during sub-turn execution, the sub-turn continues under its own timeout — the parent does not cancel children.

---

## Explicit Non-Behaviors

- The system must not add a NEW retry mechanism for 429/5xx/auth — the existing FallbackChain handles these.
- The system must not retry on billing errors (402) — `CooldownTracker.disableBilling` already handles this.
- The system must not retry on format errors — they fail identically every time.
- The system must not kill foreground shell commands via the background process timeout — only background sessions.
- The system must not publish a response via the defer if `HasSentInRound()` is true (tool already sent a response).
- The system must not cancel sub-turn contexts when the parent turn times out — sub-turns are independent.

---

## Config Changes

```go
type AgentDefaults struct {
    // ... existing fields ...
    TimeoutSeconds int  // default: 300 (5 min). 0 = disabled.
    MaxRetries     int  // default: 3 (for inline loop: timeout+compaction+empty retries only)
}

type ExecConfig struct {
    // ... existing fields ...
    MaxBackgroundSeconds int  // default: 300 (5 min). 0 = disabled.
}
```

Note: `MaxRetries` controls only the INLINE retry loop (timeout, context overflow, empty response). Provider-transient errors (429, 5xx, auth) are handled by the FallbackChain with its own retry/cooldown logic.

---

## Functional Requirements

- **FR-001**: System MUST enforce a configurable per-turn timeout (`timeout_seconds`, default 300) via `context.WithTimeout` wrapping `runTurn`.
- **FR-002**: System MUST route provider-transient errors (429, 5xx, auth) through the existing `FallbackChain` + `CooldownTracker`, NOT through the inline retry loop.
- **FR-003**: System MUST auto-compact context when turn timeout fires and context usage exceeds `SummarizeTokenPercent`, retrying once after compaction.
- **FR-004**: System MUST use a `defer` pattern in `Run()` to guarantee response publication on ALL exit paths.
- **FR-005**: System MUST ensure `expandMultiKeyModels` creates separate FallbackChain candidates for each API key, enabling existing rotation.
- **FR-006**: System MUST retry once on empty LLM response (no content, no tools) within the inline retry loop before sending fallback message.
- **FR-007**: System MUST kill background processes after `max_background_seconds` with SIGTERM → 5s → SIGKILL.
- **FR-008**: System MUST wait for active turns on shutdown (up to `min(timeout_seconds, 60)`) before force-cancelling.
- **FR-009**: System MUST use exponential backoff with full jitter between inline retries (base: 2s, factor: 2×, max: 30s, jitter: uniform random [0, calculated_delay]).
- **FR-010**: System MUST use `ClassifyError()` in the inline retry loop to distinguish turn-level errors (timeout, context) from provider errors (delegate to FallbackChain).
- **FR-011**: System MUST preserve partial streamed content when a turn times out mid-stream.
- **FR-012**: System MUST return actionable error messages (not raw HTTP status codes).
- **FR-013**: System MUST publish a "Retrying..." indicator to the chat stream when the first retry fires, so the user knows the agent is not hung.
- **FR-014**: System MUST emit events for: turn timeout, empty response retry, compaction retry, background process kill, shutdown wait — using the existing `emitEvent` infrastructure.
- **FR-015**: System MUST respect `Retry-After` header from 429 responses when computing backoff in the FallbackChain (CooldownTracker already supports this — verify it's wired).
- **FR-016**: When `forceCompression` fails during timeout recovery, the system MUST NOT retry compaction — return partial response + timeout message.

---

## Success Criteria

- **SC-001**: Zero silent response losses — every completed response is delivered (defer pattern).
- **SC-002**: 429 rate limit errors are retried automatically via FallbackChain without user intervention.
- **SC-003**: Turns complete within `timeout_seconds` or deliver an explicit timeout message.
- **SC-004**: Background processes are killed within `max_background_seconds + 5` seconds.
- **SC-005**: Gateway shutdown waits for active turns (clean exit code after SIGTERM during active turn).
- **SC-006**: Multi-key providers survive single-key failure via existing FallbackChain rotation.
- **SC-007**: Empty LLM responses produce a user-friendly fallback, not silence.
- **SC-008**: Users see "Retrying..." indicator during retry backoff (not silent waiting).

---

## Review Findings Resolution

| Finding | Resolution |
|---------|-----------|
| MAJ-001: Multi-key rotation conflicts with FallbackChain | Removed separate rotation. US-4 rewritten to ensure FallbackChain is used for all LLM calls. |
| MAJ-002: Tool cleanup during timeout underspecified | Added acceptance scenario 1.3 with per-tool-type cleanup behavior. |
| MAJ-003: Retry loop vs FallbackChain scope unclear | Added Architecture Decision section. Inline loop = turn-level. FallbackChain = provider-level. |
| MAJ-004: Response loss fix needs concrete approach | Specified `defer` pattern with `published` flag and `HasSentInRound()` interaction. |
| MAJ-005: No jitter in backoff | FR-009 updated to include full jitter. |
| MIN-001: max_retries=5 too aggressive | Reduced to 3 (inline loop only). FallbackChain has its own limits. |
| MIN-002: 65% threshold unjustified | Changed to use existing `SummarizeTokenPercent` config (default 75%). |
| MIN-003: Shutdown timeout < turn timeout | Changed default to `min(timeout_seconds, 60)`. |
| Q1 (HasSentInRound) | Addressed in US-3 scenario 5. |
| Q2 (retry scope) | Per-LLM-call, not per-turn. Budget resets between LLM calls. |
| Q3 (counter interaction) | Single counter for inline loop (timeout+context+empty). FallbackChain has separate counts. |
| Q4 (forceCompression fails) | FR-016: don't retry compaction, return partial + timeout. |
| Q5 (sub-turn interaction) | US-1 scenario 7: sub-turns independent, not cancelled by parent. |
| Q6 (Retry-After header) | FR-015: verify CooldownTracker respects it. |
| Q7 (provider timeout vs turn timeout) | Turn timeout wraps everything including provider timeout. Provider timeout is nested within. |
