# Adversarial Review: Joined Session Store — Multi-Agent Sessions (v3)

**Spec reviewed**: `docs/internal/specs/joined-session-store-spec.md`
**Review date**: 2026-06-08
**Verdict**: REVISE

## Executive Summary

The spec describes a feature that is substantially already implemented on the `hotfix/v0.1.1` branch, so several findings reflect spec-vs-code divergence rather than missing implementation. The biggest risks are: (1) the 50-agent soft limit and the 20-agent warning in the Design Decisions table are entirely unimplemented — no code enforces or logs them; (2) `getContextWindow` in the actual implementation ignores the per-agent model config and always returns the global default, making the 50%-budget split unreliable for agents with non-default windows; (3) the spec's statement that session deletion requires "admin role" contradicts the code, which uses plain `withAuth`; (4) the LLM summarization path described as step 6 in HandoffTool is explicitly deferred (code comment: "can be layered on later"), meaning tiered summarization is not implemented. Several structural issues make the spec ambiguous as a standalone document.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 6 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **14** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] LLM summarization in HandoffTool is deferred, not implemented

- **Lens**: Incorrectness
- **Affected section**: "Tiered summarization (5s timeout)" in HandoffTool Execute pseudocode (Step 6); Design Decisions table row "Tiered: LLM summary (5s timeout) → fallback to truncation"
- **Description**: The spec presents tiered LLM summarization as a current design decision and as an implemented step in `HandoffTool.Execute`. The actual implementation (`pkg/tools/handoff.go:167`) contains an explicit comment: "Simple truncation fallback — LLM summarization can be layered on later once provider access is available in the tool layer without import cycles." `getSummarizer` is not in the `HandoffTool` constructor; the spec's `NewHandoffTool` signature includes it, but the real constructor does not. The entire "Step 6: Tiered summarization" block in the pseudocode, including the 5-second timeout, does not exist.
- **Impact**: Any engineer reading this spec will build the LLM summarization path, expecting it is the committed design. Reviewers comparing code to spec will flag a regression that doesn't exist. The spec-vs-implementation divergence makes spec fidelity unverifiable.
- **Recommendation**: Either (a) mark the tiered summarization sub-section as "PLANNED (not yet implemented — see TODO)" with a tracked issue number, or (b) if the intent is to implement it, add `getSummarizer func(agentID string) Summarizer` back to the constructor signature and define the `Summarizer` interface explicitly. Do not describe deferred work as an implemented step.

---

### MAJOR Findings

#### [MAJ-001] Max-agents limit is a design decision with no enforcement specification

- **Lens**: Incompleteness
- **Affected section**: Design Decisions table — "Max agents per session: 50 (soft limit, warn at 20)"
- **Description**: The spec lists a 50-agent soft limit with a warning at 20, but neither `SwitchAgent` (the only code path that adds agents to `AgentIDs`) nor any other component enforces or checks these limits. There is no `Warn` log at 20, no check at 50, no error, and no fallback behaviour described. "Soft limit" is undefined — does a session with 51 agents fail silently, warn, return an error to the caller, or simply keep growing?
- **Impact**: Without enforcement, `AgentIDs` grows unboundedly. A misconfigured LLM that chains handoffs will fill disk with an ever-growing `meta.json` and trigger `AgentIDs` deserialization overhead on every read. This is a stated design decision that is unimplemented and unspecified.
- **Recommendation**: Define the enforcement behaviour: where the check lives (add to `SwitchAgent`), what happens at 20 (log a `slog.Warn`), and what happens at 50 (return a new `ErrMaxAgentsExceeded` error). Add both as functional requirements and add them to the success criteria as a measurable check.

#### [MAJ-002] `getContextWindow` ignores per-agent context window; budget split is unreliable

- **Lens**: Incorrectness
- **Affected section**: Token Estimation section — "target_agent.context_window = config.Agents.List[agentID].Model.ContextWindow ?? config.Agents.Defaults.ContextWindow ?? 8192"
- **Description**: The spec defines a three-level fallback chain for resolving a target agent's context window (per-agent → defaults → 8192). The actual implementation (`pkg/agent/loop.go:1334-1340`) only looks at `currentCfg.Agents.Defaults.ContextWindow`, skipping the per-agent `AgentConfig.Model` field entirely. No `AgentModelConfig.ContextWindow` field currently exists (the spec says "Add `ContextWindow int` to `AgentModelConfig` if not present" — it has not been added). As a result, every handoff uses the same global default, making the 50%-budget calculation wrong for agents with different context windows.
- **Impact**: An agent with a 32k-token window will receive only 4k tokens of context (based on the 8192 default) when the intent was 16k. A handoff to a small model (4k window) from a large-context session may overflow the context budget entirely.
- **Recommendation**: Add `ContextWindow int` to `AgentModelConfig`, implement the three-level fallback, and add a unit test that verifies each level of the chain. Update the spec to name the config field path and clarify that the resolver must call `registry.GetAgent(targetAgentID)` to look up `agentCfg.Model.ContextWindow`.

#### [MAJ-003] Session deletion authorization contradicts the spec

- **Lens**: Inconsistency
- **Affected section**: Design Decisions table — "Session deletion: Any user with admin role via REST API"
- **Description**: The spec says session deletion requires "admin role". The implementation registers `DELETE /api/v1/sessions/{id}` via `api.withAuth(api.HandleSessions)` (`pkg/gateway/gateway.go:1342,1345`) which enforces authentication but not admin role. Any authenticated user can delete any session.
- **Impact**: A non-admin user can delete another user's session. In a multi-user deployment this is a privilege escalation on session data. Alternatively, if the intent was `withAuth` (not admin), the spec is wrong and will cause engineers to add incorrect admin-role guards.
- **Recommendation**: Decide which is correct and make spec and code agree. If admin-only: wrap `deleteSession` with `middleware.RequireAdmin`. If any authenticated user: update the spec's Design Decisions table to say "Any authenticated user" and add a note explaining why admin-only was rejected.

#### [MAJ-004] System agent handoff block (FR-013) is not implemented in HandoffTool

- **Lens**: Incorrectness
- **Affected section**: HandoffTool Execute pseudocode Step 1 — "if agentID == 'omnipus-system': return error"; FR-013
- **Description**: The spec states the system agent handoff MUST be blocked and assigns this FR-013. The actual `HandoffTool.Execute` (`pkg/tools/handoff.go:123-223`) has no check for the system agent. Validation is entirely `GetAgentName → exists` check. The string literal `"omnipus-system"` does not appear anywhere in the handoff code. A system agent with that ID could receive a handoff.
- **Impact**: Handing off to the system agent could break its tool model (per the spec's own rationale: "Incompatible tool model"). The spec documents a security-relevant block that does not exist.
- **Recommendation**: Add an explicit guard at the top of `Execute`: if `agentID == "omnipus-system"` (or check `config.AgentTypeSystem`), return `ErrorResult("handoff to the system agent is not supported")`. Add a test for FR-013. The spec should also name the exact string literal or config constant used so future renames are caught.

#### [MAJ-005] Retention sweep covers shared store only; legacy per-agent sessions silently excluded

- **Lens**: Incompleteness
- **Affected section**: Design Decisions table — "Migration: New sessions only — old per-agent sessions read-only"; Success Criteria SC-004 "No data loss — zero sessions go missing"
- **Description**: The retention sweep is wired only to the shared store (`pkg/gateway/gateway.go:777-778`). Legacy per-agent sessions at `$OMNIPUS_HOME/agents/{id}/sessions/` accumulate indefinitely — they are never swept. The spec does not address retention policy for legacy sessions and asserts SC-004 ("no data loss") without distinguishing between "data retention" and "indefinite disk growth".
- **Impact**: On a long-running deployment, legacy session JSONL files grow without bound. A user who migrates to the joined model still has their pre-migration sessions consuming unbounded disk. SC-004 as written is satisfied, but disk exhaustion is a production incident waiting to happen.
- **Recommendation**: Add a FR: "Legacy per-agent sessions MUST be included in the retention sweep." Wire `RetentionSweep` for each agent's store in `gateway.go`. Or explicitly state that legacy session retention is out of scope, and add a tracked issue with a target date.

#### [MAJ-006] `PostLoad()` is not called in `readUnifiedMeta` for all code paths

- **Lens**: Incorrectness
- **Affected section**: "Modified UnifiedStore Constructor (CRIT-001)" / SessionMeta v2 — `PostLoad()` backfill
- **Description**: The spec requires `PostLoad()` to backfill `AgentIDs` and `ActiveAgentID` from the legacy `AgentID` field after every unmarshal. `readUnifiedMeta` (`pkg/session/unified.go:775-789`) does call `meta.PostLoad()`. However, `PartitionStore.GetMeta` (`pkg/session/daypartition.go:359`) also calls `PostLoad()`. The concern is that `wrapLegacy` in `ListAllSessions` is described in the spec as a conversion function ("Wrap legacy SessionMeta as UnifiedMeta"), but the actual code (`pkg/agent/loop.go:2693-2696`) just appends the `*UnifiedMeta` from the legacy store directly — it does NOT call a separate `wrapLegacy` function. The spec's pseudocode names a non-existent `wrapLegacy` function that carries the backfill logic. If `PostLoad()` is not called before the session is added to `all`, legacy sessions with empty `AgentIDs` will be rendered without participation badges.
- **Impact**: Legacy sessions listed in the Sessions panel will show no agent badges. FR-003 ("track all participating agent IDs") is violated for legacy sessions.
- **Recommendation**: Remove the fictional `wrapLegacy` from the spec pseudocode and instead document that `PostLoad()` is relied upon at `readUnifiedMeta` time (which already happens via the legacy store's own `readUnifiedMeta`). Confirm with a test: list sessions from a legacy store with only `agent_id` set; assert `AgentIDs` is populated and the badge renders.

---

### MINOR Findings

#### [MIN-001] FR-004 and FR-011 are duplicates of each other

- **Lens**: Inconsistency
- **Affected section**: Functional Requirements — FR-004 and FR-011
- **Description**: FR-004 reads "Context transfer MUST fit within 50% of target agent's context window." FR-011 reads "Context transfer MUST NOT exceed 50% of target agent's context window." These are semantically identical. Having both wastes a requirement slot and creates traceability noise.
- **Recommendation**: Delete FR-011. Update any `Depends on` references to point to FR-004.

#### [MIN-002] SC-005 performance target is untestable as written

- **Lens**: Infeasibility
- **Affected section**: Success Criteria SC-005 — "New session creation <50ms p99, single-core SSD, no concurrent ops"
- **Description**: "Single-core SSD, no concurrent ops" is not a test environment that CI can reliably reproduce. p99 requires a statistical sample (hundreds of runs), but the spec provides no sample size or test harness. The qualifier "single-core" is undefined (single physical core? single goroutine? single CPU affinity?).
- **Recommendation**: Replace with a simpler, automatable criterion: "New session creation completes within 100ms (wall clock) in a benchmark of 100 sequential calls on a tmpfs (`t.TempDir()`) without concurrent writers." This can be an in-package benchmark (`BenchmarkNewSession`).

#### [MIN-003] `ToolSessionKey` vs `ToolTranscriptSessionID` resolution not explained in spec

- **Lens**: Ambiguity
- **Affected section**: HandoffTool Execute Step 3 — "Get session ID from context"
- **Description**: The spec writes `sessionKey = ToolSessionKey(ctx)` but the real code calls `resolveSessionID` which tries `ToolTranscriptSessionID` first, falling back to `ToolSessionKey`. For channel sessions these IDs are different. The spec does not define the priority or the difference between these two context values.
- **Recommendation**: Add a terminology note: `ToolTranscriptSessionID` is the actual session directory name; `ToolSessionKey` is the routing key (may be a peer ID or chat ID). `resolveSessionID` prefers the former. This is relevant to FR-002 (transcript entry tagged with correct session).

#### [MIN-004] `CompactionSummaries` field is added but its write path is unspecified

- **Lens**: Incompleteness
- **Affected section**: SessionMeta v2 — `CompactionSummaries map[string]string`; Design Decisions — "LastCompactionSummary: Per-agent — stored as `map[agentID]string`"
- **Description**: The spec deprecates `LastCompactionSummary` in favour of `CompactionSummaries` (per-agent map). However, the spec does not define who writes to `CompactionSummaries`, when, or via what API. The existing compaction path in `pkg/agent/loop.go` (around line 5786) writes to `LastCompactionSummary`. If that write path is not updated, `CompactionSummaries` will always be empty and the deprecation is cosmetic only.
- **Recommendation**: Add a functional requirement: "On context compaction, MUST write the summary to `CompactionSummaries[agentID]` in addition to (or instead of) `LastCompactionSummary`." Name the code site that must change (`forceCompression` at loop.go ~5749).

---

### Observations

#### [OBS-001] `CanDelegateTo` config field is silently ignored during handoff

- **Lens**: Incorrectness
- **Affected section**: HandoffTool Execute pseudocode (Step 2 validation)
- **Description**: `AgentConfig.CanDelegateTo` and `config.AgentDefaults.CanDelegateTo` exist in the codebase and a `buildDelegateChecker` function exists in `loop.go:1583`. The spec does not reference this config at all, and the `HandoffTool` does not check it. This means the allow-list is currently dead config for the chat path (it may be used elsewhere). This is not necessarily wrong, but the omission should be intentional.
- **Suggestion**: Add a note in the spec: "`CanDelegateTo` is enforced at the registry-injection layer [cite the relevant code site], not inside `HandoffTool.Execute`" — or add it to Execute if that's the intended enforcement point.

#### [OBS-002] Channel sessions route through shared store, but handoff for channel sessions is unaddressed

- **Lens**: Incompleteness
- **Affected section**: Out of Scope section (no mention of channel sessions + handoff)
- **Description**: The spec says new sessions go to the shared store, including channel sessions (`NewChannelSession` now routes to shared store). A Telegram user mid-conversation could theoretically trigger a handoff. The spec neither allows nor forbids handoffs inside channel sessions. The "Out of Scope" list is silent on this.
- **Suggestion**: Explicitly state whether channel sessions support handoff. If yes, verify `SwitchAgent` + `AppendTranscript` work correctly for a channel session (the `channel` field stays unchanged per the spec, but `ActiveAgentID` would switch — is the channel handler's routing aware of `ActiveAgentID`?). If no, add a guard in `HandoffTool.Execute` that rejects handoffs when the session type is `SessionTypeChannel`.

#### [OBS-003] Task sessions still use per-agent store, but this is not stated in Out of Scope

- **Lens**: Incompleteness
- **Affected section**: Out of Scope — "Task session sharing (task sessions remain per-agent)"
- **Description**: The Out of Scope entry exists, but `processTaskDirect` (`loop.go:2752`) still passes `al.GetAgentStore(agentID)` as `TranscriptStore` — confirming task sessions bypass the shared store. However, the `resolveSessionStore` REST handler (`loop.go:2627-2644`) and `ListAllSessions` both check the shared store first, then legacy. A task session created via `processTaskDirect` and stored in `GetAgentStore(agentID)` may be findable via `ListAllSessions` but deletable only via the per-agent store path. This edge case is not documented.
- **Suggestion**: Add a note in the spec clarifying that task sessions created via `processTaskDirect` use `GetAgentStore`, making them legacy sessions that are visible in the merged list but handled by the per-agent delete path.

---

## Structural Integrity

**Mode detected**: `structured-spec` (functional requirements with FR-xxx IDs and success criteria, but no BDD scenarios or traceability matrix)

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PARTIAL | SC-001–SC-006 cover the goals but do not map 1:1 to FR-xxx IDs |
| Cross-references are consistent | FAIL | `wrapLegacy` referenced in pseudocode does not exist in code; FR-004 and FR-011 are duplicates |
| Scope boundaries are explicit | PARTIAL | Out of Scope list exists but misses channel-session handoff and task-session delete routing |
| Success criteria are measurable | PARTIAL | SC-005 is not reproducibly testable (see MIN-002) |
| Error/failure scenarios addressed | FAIL | Shared store init failure, flock timeout, and `SwitchAgent` concurrency under 50+ agents not covered |
| Dependencies between requirements identified | PARTIAL | `Depends on` column exists; MAJ-001 (max-agent limit) has no FR or dependency chain |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Requirements |
|----------|----------------|----------------------|
| Agent limit enforcement | No test verifies warning at 20 agents or behaviour at 50 | MAJ-001, Design Decisions |
| Per-agent context window | No test verifies that `getContextWindow` reads agent-specific config | MAJ-002, FR-004/FR-011 |
| System agent block | No test for FR-013 (handoff to `omnipus-system` must fail) | MAJ-004, FR-013 |
| LLM summarization | No test for tiered summarization (because it is not implemented) | CRIT-001 |
| Legacy session retention | No test that legacy sessions accumulate when sweep runs | MAJ-005 |
| Channel session handoff rejection/acceptance | No test for handoff inside a channel session | OBS-002 |
| `CompactionSummaries` write | No test that compaction writes to the per-agent map | MIN-004 |

### Dataset Gaps

| Area | Missing Boundary | Recommendation |
|------|-----------------|----------------|
| `splitByTokenBudget` | All entries larger than budget (every entry is > budget) | Test that `recent` returns the single newest entry and `older` contains the rest |
| `splitByTokenBudget` | Empty transcript | Already handled in code; add explicit test |
| `AgentIDs` growth | Exactly 20 and exactly 50 agents | Needed once MAJ-001 is resolved |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `SwitchAgent` (meta.json write) | ok | ok | risk | ok | ok | ok | No audit event is emitted when an agent switch occurs — only transcript entry. RBAC cannot reconstruct who initiated the handoff. |
| `HandoffTool.Execute` | ok | ok | risk | risk | ok | ok | Repudiation: `currentAgentID` from ctx may be empty (no guard); if empty, transcript entry has blank agent attribution. Information: `contextMsg` from the LLM is written verbatim into `transcript.jsonl` — no sanitization; PII injected by LLM is persisted. |
| `DELETE /api/v1/sessions/{id}` | ok | ok | ok | ok | ok | risk | Any authenticated user can delete any session (see MAJ-003). No ownership check. |
| Shared session store init | ok | ok | ok | ok | risk | ok | If `NewUnifiedStoreWithHome` fails, new sessions silently fall back to per-agent stores (logged at Error, but no user notification, no metric). A misconfigured home causes split-brain session routing. |
| `ReadTranscript` for context transfer | ok | ok | ok | risk | ok | ok | Full transcript content is passed to the target agent. If a previous agent recorded sensitive user input (credentials, PII) in a `tool_result` entry, that content lands in the new agent's context. |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. **What happens if `SwitchAgent` is called while `HandoffTool` is mid-execution on a concurrent request?** The in-process mutex serializes writes but a second HTTP request could call `SwitchAgent` on the same session simultaneously. The spec says flock + mutex, but the scenario is not tested.

2. **How does the SPA know to re-render the session panel with the new `ActiveAgentID` after a handoff?** The `agent_switched` WS frame is sent, but is the session panel subscribed to re-fetch session metadata on that event? The spec defines the UI requirement (FR-007, FR-008) but provides no SPA integration spec for how the session list is refreshed post-handoff.

3. **What is the degradation path when the shared store is nil at runtime?** The code falls back to per-agent stores, but `HandoffTool` holds a reference set at construction time. If `sharedStore` was nil at construction, `HandoffTool` receives a nil `sessionStore`. What does `SwitchAgent` return on a nil store? (The interface check will panic.)

4. **How does the retention sweep handle a session where `transcript.jsonl` has entries from multiple agents and only some are older than the retention cutoff?** The sweep deletes `.jsonl` files by mtime — it does not read per-entry timestamps. A session where one agent was active 91 days ago and another 5 days ago will have its full `transcript.jsonl` deleted because the file mtime reflects the last write.

5. **Who owns session title generation after a handoff?** The Out of Scope list defers "session title regeneration after handoff", but the title was set by the creating agent. After handoff, the session panel shows the old title. Is that intentional UX?

6. **Is `retentionDays = 0` (disabled) honoured for shared sessions?** `RetentionSweep` no-ops when `retentionDays <= 0`. But if a user sets retention to 0 meaning "keep forever", the spec does not clarify that legacy-store sessions are still not swept. Could lead to confusion.

---

## Verdict Rationale

REVISE. The most significant issues are that two published design decisions are either not implemented (tiered LLM summarization in CRIT-001; max-agent limit in MAJ-001) or incorrectly implemented (per-agent context window in MAJ-002; system agent block in MAJ-004). The spec presents these as complete design choices and live pseudocode, but the code diverges on all four points. An engineer using this spec as the source of truth would either implement something that conflicts with existing code or fail to implement guards the spec asserts exist.

MAJ-003 (admin vs. `withAuth` for session delete) is a quiet privilege inconsistency that needs a deliberate decision before implementation proceeds.

The test coverage gaps (no enforcement of the 50-agent limit, no per-agent context window test, no system agent block test) mean these gaps would ship undetected.

### Recommended Next Actions

- [ ] [CRIT-001] Mark tiered LLM summarization as PLANNED with a tracked issue, or implement `getSummarizer` and add the 5s timeout.
- [ ] [MAJ-001] Add `ErrMaxAgentsExceeded`, enforce in `SwitchAgent` at 50, warn at 20.
- [ ] [MAJ-002] Add `ContextWindow int` to `AgentModelConfig`; implement three-level fallback in `getContextWindow`; add unit test.
- [ ] [MAJ-003] Decide admin vs. any-authenticated for session delete; align spec and code.
- [ ] [MAJ-004] Add system agent guard to `HandoffTool.Execute`; add test for FR-013.
- [ ] [MAJ-005] Wire `RetentionSweep` to legacy per-agent stores, or add an explicit out-of-scope note with a tracked issue.
- [ ] [MAJ-006] Remove fictional `wrapLegacy` from pseudocode; document actual `PostLoad` reliance; add integration test.
- [ ] [MIN-001] Delete duplicate FR-011.
- [ ] [MIN-004] Specify who writes `CompactionSummaries` and when.

---

Address the findings above, then re-run:
```
/grill-spec docs/internal/specs/joined-session-store-spec.md
```
