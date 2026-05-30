# Spec: Joined Session Store — Multi-Agent Sessions (v3)

## Context

Currently each agent has its own isolated session store at `~/.omnipus/agents/{id}/sessions/`. When a handoff occurs (Mia → Ray), Ray starts a new session with no knowledge of the prior conversation. The user expects ONE continuous conversation where specialists participate.

**Goal:** Sessions belong to the USER, not the agent. One session can involve multiple agents. Each message is tagged with which agent sent it. On handoff, the target agent receives recent messages (token-budget-aware) + a summary of older context.

## Terminology

- **Session**: persistent storage unit — directory containing `meta.json` and `transcript.jsonl`. Spans the entire multi-agent conversation.
- **Conversation**: the LLM context window for a single agent turn. Each handoff starts a new conversation within the same session.
- **Handoff**: switching the active agent on a session. The session persists; only the active agent changes.
- **Shared store**: the single `*UnifiedStore` instance rooted at `$OMNIPUS_HOME/sessions/`.
- **Legacy store**: existing per-agent stores at `$OMNIPUS_HOME/agents/{id}/sessions/`.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| History on handoff | Token-budget-aware (50% of target context window) | Fixed message count risks overflow or underuse |
| Summarization strategy | Tiered: LLM summary (5s timeout) → fallback to truncation | Balance quality vs reliability |
| Storage location | `$OMNIPUS_HOME/sessions/` | Shared across agents, clean separation |
| Concurrency model | Single shared `*UnifiedStore` instance + NEW flock on meta.json | Prevents race conditions |
| UI display | Single flat list with agent participation badges | User sees one conversation |
| Migration | New sessions only — old per-agent sessions read-only | Zero migration risk |
| System agent handoff | BLOCKED | Incompatible tool model |
| Shared store agentID | Empty string `""` — constructor takes `""`, callers pass agentID per-operation | Decouples store from agent identity |
| Handoff events in transcript | YES — logged as system entries | Audit trail + replay |
| Channel field on handoff | Unchanged — reflects originating channel | Session is channel-scoped |
| Max agents per session | 50 (soft limit, warn at 20) | Prevent unbounded growth |
| Session deletion | Any user with admin role via REST API | Not agent-initiated |
| Deleted agent in AgentIDs | Render with fallback name "[removed agent]" + grey badge | Graceful degradation |
| LastCompactionSummary | Per-agent — stored as `map[agentID]string` | Each agent's compaction is independent |

---

## Modified UnifiedStore Constructor (CRIT-001)

### Current
```go
func NewUnifiedStore(baseDir, agentID string) *UnifiedStore
```
`agentID` is baked into every `NewSession()` call.

### New
```go
func NewUnifiedStore(baseDir string) *UnifiedStore
```
Remove `agentID` from the constructor. Instead, `NewSession` takes it as a parameter:

```go
func (s *UnifiedStore) NewSession(
    sessionType UnifiedSessionType,
    channel string,
    creatingAgentID string,  // NEW: the agent creating this session
) (*UnifiedMeta, error)
```

Inside `NewSession`:
```go
meta.AgentID = creatingAgentID        // legacy compat
meta.AgentIDs = []string{creatingAgentID}
meta.ActiveAgentID = creatingAgentID
```

### Migration of existing per-agent stores
Legacy per-agent stores still use the old constructor: `NewUnifiedStore(dir)` (agentID param removed, but legacy stores set `AgentID` via `PostLoad()` backfill from the session's stored `meta.json`).

### AppendTranscript
Callers MUST set `TranscriptEntry.AgentID` before calling. The store does NOT infer it.

---

## New UnifiedStore.SwitchAgent Method (MAJ-005, MAJ-006)

```go
// SwitchAgent atomically updates the active agent on a session.
// Acquires mutex + flock. Returns ErrAlreadyActive if already on target.
func (s *UnifiedStore) SwitchAgent(sessionID, newAgentID string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    // flock meta.json
    meta := s.readMeta(sessionID)
    if meta.ActiveAgentID == newAgentID {
        return ErrAlreadyActive  // idempotent
    }
    meta.ActiveAgentID = newAgentID
    if !contains(meta.AgentIDs, newAgentID) {
        meta.AgentIDs = append(meta.AgentIDs, newAgentID)
    }
    meta.UpdatedAt = time.Now()
    s.writeMeta(sessionID, meta)  // atomic write
    // unflock
    return nil
}
```

`HandoffTool` calls `SwitchAgent` — idempotency is handled inside. The tool treats `ErrAlreadyActive` as success.

---

## Modified HandoffTool (CRIT-002)

### New Constructor
```go
func NewHandoffTool(
    getRegistry    func() AgentRegistryReader,
    sessionStore   *session.UnifiedStore,        // NEW: shared store
    getContextWindow func(agentID string) int,   // NEW: target agent's context window
    getSummarizer  func(agentID string) Summarizer, // NEW: LLM access for summary
    onHandoff      func(chatID, agentID, agentName string),
) *HandoffTool
```

### Execute Pseudocode
```
func Execute(ctx, args):
    agentID = args["agent_id"]
    context = args["context"]

    // 1. System agent block (FR-013)
    if agentID == "omnipus-system": return error

    // 2. Validate target agent exists (FR-012)
    agentName, exists = getRegistry().GetAgentName(agentID)
    if !exists: return error

    // 3. Get session ID from context
    sessionKey = ToolSessionKey(ctx)
    if sessionKey == "": return error

    // 4. Idempotency + atomic switch (MAJ-005, MAJ-006)
    err = sessionStore.SwitchAgent(sessionKey, agentID)
    if err == ErrAlreadyActive: return success("Already connected to {agentName}")

    // 5. Context transfer (token-budget-aware)
    contextWindow = getContextWindow(agentID)
    budget = contextWindow * 0.50
    systemPromptTokens = estimateSystemPrompt(agentID)
    available = budget - systemPromptTokens

    transcript = sessionStore.ReadTranscript(sessionKey)
    recentMessages, olderMessages = splitByTokenBudget(transcript, available)

    // 6. Tiered summarization (5s timeout)
    if len(olderMessages) > 0:
        summaryCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
        summary, err = getSummarizer(agentID).Summarize(summaryCtx, olderMessages)
        cancel()
        if err != nil:
            summary = "[Earlier context not available — showing recent messages only]"

    // 7. Store handoff event in transcript (audit trail)
    sessionStore.AppendTranscript(sessionKey, TranscriptEntry{
        Role: "system",
        Content: fmt.Sprintf("Handoff: %s → %s. Context: %s", currentAgent, agentName, context),
        AgentID: currentAgentID,
    })

    // 8. Notify frontend
    if onHandoff != nil:
        chatID = ToolChatID(ctx)
        onHandoff(chatID, agentID, agentName)

    // 9. Return context for the target agent
    return ToolResult{
        ForUser: "Connecting you with {agentName}...",
        ForLLM: "Handoff complete. Context: {summary + recentMessages}",
    }
```

### Overall timeout: 10 seconds
The entire Execute is wrapped in `context.WithTimeout(ctx, 10*time.Second)`.

---

## Token Estimation (MAJ-001)

### Context window resolution
```
target_agent.context_window = config.Agents.List[agentID].Model.ContextWindow
   ?? config.Agents.Defaults.ContextWindow
   ?? 8192  // safe default
```

Add `ContextWindow int` to `AgentModelConfig` if not present. The `getContextWindow` callback resolves this chain.

### Token estimation functions
Reuse existing functions from `pkg/agent/context_budget.go`:
- `estimateMessageTokens(content string) int` — ~2.5 chars/token
- `estimateToolDefsTokens(tools []ToolDefinition) int`
- If `TranscriptEntry.Tokens > 0`, use stored value; otherwise re-estimate from content

### splitByTokenBudget algorithm
```
func splitByTokenBudget(entries []TranscriptEntry, budget int) (recent, older):
    tokensSoFar = 0
    cutoff = len(entries)
    for i = len(entries) - 1; i >= 0; i--:
        tokens = entries[i].Tokens or estimateMessageTokens(entries[i].Content)
        if tokensSoFar + tokens > budget:
            cutoff = i + 1
            break
        tokensSoFar += tokens
    recent = entries[cutoff:]
    older = entries[:cutoff]
    return
```

### Summary enforcement
LLM call uses `max_tokens: 500`. If response exceeds, truncate to last complete sentence.

---

## Flock Integration (MAJ-002)

**NEW** — no flock currently exists in the session package.

Add flock in `UnifiedStore.writeUnifiedMeta`:
```go
func (s *UnifiedStore) writeUnifiedMeta(sessionID string, meta *UnifiedMeta) error {
    s.mu.Lock()                              // in-process serialization
    defer s.mu.Unlock()
    path := filepath.Join(s.baseDir, sessionID, "meta.json")
    return fileutil.WithFlock(path, func() error {  // cross-process defense-in-depth
        return fileutil.WriteFileAtomic(path, data, 0o600)
    })
}
```

Lock ordering: always `sync.Mutex` first, then flock. Never reverse.

Also add flock to `SwitchAgent` (which calls `writeUnifiedMeta` internally).

---

## AgentLoop Integration (MAJ-003)

### New field
```go
type AgentLoop struct {
    // ... existing fields ...
    sharedSessionStore *session.UnifiedStore  // $OMNIPUS_HOME/sessions/
}
```

### Initialization (in NewAgentLoop)
```go
sharedDir := filepath.Join(homePath, "sessions")
os.MkdirAll(sharedDir, 0o700)
al.sharedSessionStore = session.NewUnifiedStore(sharedDir)
```

### Agent injection
All `AgentInstance` objects share the same `sharedSessionStore`:
```go
agent.Sessions = al.sharedSessionStore  // NOT per-agent anymore
```

### GetSessionStore (replaces GetAgentStore for new sessions)
```go
func (al *AgentLoop) GetSessionStore() *session.UnifiedStore {
    return al.sharedSessionStore
}
```

`GetAgentStore(agentID)` kept for legacy session access only.

### ListAllSessions
```go
func (al *AgentLoop) ListAllSessions() ([]*session.UnifiedMeta, error) {
    // 1. Read shared store
    shared, err := al.sharedSessionStore.ListSessions()
    sharedIDs := set(shared.ID for each)

    // 2. Read legacy per-agent stores, dedup
    for _, agentID := range al.registry.ListAgentIDs() {
        legacyStore := al.getLegacyAgentStore(agentID)
        if legacyStore == nil { continue }
        legacySessions, _ := legacyStore.ListSessions()
        for _, s := range legacySessions {
            if !sharedIDs[s.ID] {
                // Wrap legacy SessionMeta as UnifiedMeta
                shared = append(shared, wrapLegacy(s, agentID))
            }
        }
    }

    // 3. Sort by UpdatedAt desc
    sort.Slice(shared, func(i, j int) bool {
        return shared[i].UpdatedAt.After(shared[j].UpdatedAt)
    })
    return shared, nil
}
```

Type compatibility: `wrapLegacy(*SessionMeta, agentID) *UnifiedMeta` converts legacy format, backfilling `AgentIDs` and `ActiveAgentID`.

---

## WebSocket Session Creation (MAJ-004)

### Current path (websocket.go:515-570)
```go
store := h.agentLoop.GetAgentStore(targetAgentID)
meta, err := store.NewSession(session.SessionTypeChat, "webchat")
```

### New path
```go
store := h.agentLoop.GetSessionStore()  // shared store
meta, err := store.NewSession(session.SessionTypeChat, "webchat", targetAgentID)
```

The `targetAgentID` comes from the frame's `agent_id` field (resolved from dropdown or handoff override). The shared store creates the session with:
- `AgentID = targetAgentID` (legacy compat)
- `AgentIDs = [targetAgentID]`
- `ActiveAgentID = targetAgentID`

---

## ReturnToDefaultTool (OBS-003)

`ReturnToDefaultTool` must also call `SwitchAgent` on the session store:

```go
func (t *ReturnToDefaultTool) Execute(ctx, args):
    sessionKey = ToolSessionKey(ctx)
    defaultAgentID = resolveDefaultAgent()  // from config
    sessionStore.SwitchAgent(sessionKey, defaultAgentID)
    // Log in transcript
    sessionStore.AppendTranscript(sessionKey, TranscriptEntry{
        Role: "system",
        Content: fmt.Sprintf("Returned to default agent (%s)", defaultAgentID),
        AgentID: currentAgentID,
    })
    // Notify frontend
    onHandoff(chatID, defaultAgentID, defaultAgentName)
```

---

## SessionMeta v2 (complete struct)

```go
type SessionMeta struct {
    // Existing fields (unchanged)
    ID                    string
    AgentID               string        `json:"agent_id"`  // legacy: creating agent
    Title                 string
    Status                SessionStatus
    CreatedAt             time.Time
    UpdatedAt             time.Time
    Channel               string
    Stats                 SessionStats
    Model                 string        // inherited from agent defaults
    Provider              string
    ProjectID             string
    TaskID                string
    LastCompactionSummary string        // DEPRECATED for shared sessions — see CompactionSummaries

    // New fields (v2)
    AgentIDs             []string          `json:"agent_ids,omitempty"`
    ActiveAgentID        string            `json:"active_agent_id,omitempty"`
    CompactionSummaries  map[string]string `json:"compaction_summaries,omitempty"` // per-agent compaction
}

func (m *SessionMeta) PostLoad() {
    if len(m.AgentIDs) == 0 && m.AgentID != "" {
        m.AgentIDs = []string{m.AgentID}
    }
    if m.ActiveAgentID == "" && m.AgentID != "" {
        m.ActiveAgentID = m.AgentID
    }
}
```

Note: `Channel` field does NOT change on handoff — reflects originating channel.

---

## Call Sites That Must Set AgentID

| File | Function |
|---|---|
| `pkg/session/unified.go` | `AppendTranscript` |
| `pkg/session/daypartition.go` | `AppendMessage` |
| `pkg/agent/loop.go` | `recordAssistantMessage` |
| `pkg/agent/loop.go` | `recordUserMessage` |
| `pkg/gateway/websocket.go` | WebSocket replay path |
| `pkg/tools/handoff.go` | Handoff system entries |

---

## Functional Requirements

| ID | Requirement | Depends on |
|---|---|---|
| FR-001 | System MUST create new sessions in `$OMNIPUS_HOME/sessions/{id}/` | — |
| FR-002 | System MUST tag each transcript entry with agent_id (never empty) | FR-001 |
| FR-003 | System MUST track all participating agent IDs in session metadata | FR-010 |
| FR-004 | Context transfer MUST fit within 50% of target agent's context window | FR-011 |
| FR-005 | System MUST switch ActiveAgentID in session meta on handoff | FR-010 |
| FR-006 | System MUST preserve existing per-agent sessions (read-only accessible) | — |
| FR-007 | System MUST display agent participation badges in session list | FR-003 |
| FR-008 | System MUST render per-message agent identity in chat view | FR-002 |
| FR-009 | System MUST complete handoff within 10 seconds | — |
| FR-010 | System MUST use a single UnifiedStore instance for the shared session directory | FR-001 |
| FR-011 | Context transfer MUST NOT exceed 50% of target agent's context window | — |
| FR-012 | System MUST validate target agent availability before updating ActiveAgentID | — |
| FR-013 | System MUST reject handoff requests targeting the system agent | — |
| FR-014 | System MUST deduplicate sessions when merging shared and legacy stores | FR-006 |
| FR-015 | SwitchAgent MUST be idempotent (return success if already on target) | FR-005 |
| FR-016 | Handoff events MUST be logged as system entries in transcript | FR-002 |
| FR-017 | Flock MUST be acquired on meta.json before writes (defense-in-depth) | FR-010 |

## Success Criteria

| ID | Criterion |
|---|---|
| SC-001 | Handoff from Mia to Ray: Ray's context contains recent messages from the session |
| SC-002 | Session panel shows a single flat list with agent badges |
| SC-003 | Old per-agent sessions remain accessible and visible |
| SC-004 | No data loss — zero sessions go missing |
| SC-005 | New session creation <50ms p99, single-core SSD, no concurrent ops |
| SC-006 | Handoff completes in <10 seconds including summarization |

---

## Out of Scope

- Migrating old sessions to new format
- Real-time collaborative editing (two agents simultaneously)
- Session merging (combining two sessions)
- Cross-device session sync
- Task session sharing (task sessions remain per-agent)
- Handoff to/from the system agent
- Session title regeneration after handoff
