package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// ErrAlreadyActive is returned by SessionStore.SwitchAgent when the session is
// already assigned to the requested agent. Treat as a no-op success.
// (Aliased from session.ErrAlreadyActive — both packages share the same sentinel.)
var ErrAlreadyActive = session.ErrAlreadyActive

// AgentRegistryReader is a minimal interface for looking up agents by ID.
// It is satisfied by *agent.AgentRegistry — using an interface here avoids
// an import cycle (tools → agent → tools).
type AgentRegistryReader interface {
	// GetAgentName returns the display name and a boolean indicating whether
	// the agent exists. Used by HandoffTool to validate agent_id and build
	// user-facing messages.
	GetAgentName(agentID string) (string, bool)

	// IsWorker reports whether the agent identified by agentID is a sub-agent
	// worker (the delegation-only labor tier). HandoffTool uses it to reject a
	// handoff to a worker — workers are invoked via delegation, not handoff, so a
	// worker must never become a session pin / live chat target. Returns false
	// for an unknown agent.
	IsWorker(agentID string) bool
}

// HandoffEvent carries all context fields for a handoff or return-to-default event.
// Using a struct avoids brittle positional arguments and makes call sites self-documenting.
type HandoffEvent struct {
	// Channel is the turn's channel (e.g. "webchat", "whatsapp", "telegram").
	// Load-bearing for routing: channel inbound messages carry no SessionID, so
	// their handoff override is keyed by chat scope ("chat:<channel>:<chatID>"),
	// not "session:<id>". Without Channel a channel handoff is silently dropped
	// and routing falls back to ResolveRoute (the "agent stays" bug).
	Channel   string
	ChatID    string
	SessionID string
	AgentID   string
	AgentName string
}

// HandoffFunc is the callback signature for handoff notifications.
type HandoffFunc func(HandoffEvent)

// HandoffSessionStore is the subset of *session.UnifiedStore that HandoffTool
// and ReturnToDefaultTool require. Defining it as an interface decouples the
// tools package from the concrete store type, which is being refactored by the
// session-store subagent in parallel.
//
// *session.UnifiedStore satisfies this interface once SwitchAgent is added to it.
type HandoffSessionStore interface {
	// SwitchAgent atomically changes the active agent on the session.
	// Returns ErrAlreadyActive (session.ErrAlreadyActive) when the session is
	// already on the requested agent — callers MUST treat this as success.
	SwitchAgent(sessionID, newAgentID string) error

	// ReadTranscript returns all transcript entries for the session.
	ReadTranscript(sessionID string) ([]session.TranscriptEntry, error)

	// AppendTranscript appends a single entry to the session transcript.
	AppendTranscript(sessionID string, entry session.TranscriptEntry) error
}

// HandoffTool transfers the active session to a specialist agent.
//
// On a successful handoff it:
//  1. Validates the target agent against the registry.
//  2. Atomically switches the active agent on the session (idempotent).
//  3. Reads the session transcript and applies a 50% token-budget split so the
//     target agent receives recent context without overflowing its context window.
//  4. Appends a system entry to the transcript as an audit-trail record.
//  5. Notifies the frontend so the UI can update its active-agent indicator.
type HandoffTool struct {
	BaseTool
	getRegistry      func() AgentRegistryReader
	sessionStore     HandoffSessionStore
	getContextWindow func(agentID string) int
	onHandoff        HandoffFunc
}

// NewHandoffTool creates a HandoffTool.
//
//   - getRegistry is called at Execute time (not construction time) so hot reloads
//     are automatically reflected without rebuilding the tool.
//   - sessionStore provides atomic agent switching and transcript access.
//   - getContextWindow resolves the target agent's context window for budget math;
//     it should follow: agent-specific → defaults → 8192.
//   - onHandoff notifies the frontend of the agent switch; may be nil.
func NewHandoffTool(
	getRegistry func() AgentRegistryReader,
	sessionStore HandoffSessionStore,
	getContextWindow func(agentID string) int,
	onHandoff HandoffFunc,
) *HandoffTool {
	return &HandoffTool{
		getRegistry:      getRegistry,
		sessionStore:     sessionStore,
		getContextWindow: getContextWindow,
		onHandoff:        onHandoff,
	}
}

func (t *HandoffTool) Name() string           { return "hand_off" }
func (t *HandoffTool) Scope() ToolScope       { return ScopeCore }
func (t *HandoffTool) Category() ToolCategory { return CategoryDelegation }

func (t *HandoffTool) Description() string {
	return "Hand off the conversation to a specialist agent. The user's subsequent messages will go to the target agent."
}

func (t *HandoffTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id": map[string]any{
				"type":        "string",
				"description": "ID of the target agent (e.g. \"ray\", \"max\", \"ava\", \"jim\")",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Context or instructions to give the target agent about this conversation",
			},
		},
		"required": []string{"agent_id", "context"},
	}
}

func (t *HandoffTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// Overall 10-second timeout (FR-009, SC-006).
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	agentID, ok := args["agent_id"].(string)
	if !ok || agentID == "" {
		return ErrorResult("agent_id is required")
	}
	contextMsg, _ := args["context"].(string)

	// Step 1: Validate target agent exists (FR-012).
	reg := t.getRegistry()
	agentName, exists := reg.GetAgentName(agentID)
	if !exists {
		return ErrorResult(fmt.Sprintf("agent %q not found — check the agent ID", agentID))
	}

	// Step 2: Reject a worker target. A worker is a delegation-only labor tier —
	// it is invoked via create_task / sub-agent delegation, never as a live chat
	// persona. Handing off would pin the session to the worker (making it a chat
	// target), so refuse before any switch occurs.
	if reg.IsWorker(agentID) {
		return ErrorResult(
			fmt.Sprintf(
				"cannot hand off to %q — it is a worker; workers are invoked via delegation, not handoff",
				agentName,
			),
		)
	}

	// Step 3: Get session ID from context.
	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return ErrorResult("handoff is not available in this context (no session key)")
	}

	// Step 4: Atomic switch (idempotent via ErrAlreadyActive) (MAJ-005, MAJ-006, FR-005, FR-015).
	if err := t.sessionStore.SwitchAgent(sessionID, agentID); err != nil {
		if errors.Is(err, ErrAlreadyActive) {
			return NewToolResult(fmt.Sprintf("Already connected to %s.", agentName))
		}
		return ErrorResult(fmt.Sprintf("failed to switch agent: %v", err))
	}

	// Step 5: Token-budget-aware context transfer (FR-004, FR-011).
	contextWindow := t.getContextWindow(agentID)
	budget := int(float64(contextWindow) * 0.50)

	transcript, err := t.sessionStore.ReadTranscript(sessionID)
	if err != nil {
		slog.Warn("handoff: could not read transcript for context transfer", "session", sessionID, "error", err)
		// Proceed with empty context rather than failing the handoff.
		transcript = nil
	}
	recent, older := splitByTokenBudget(transcript, budget)

	// Step 6: Build summary for older messages (tiered summarization).
	// Simple truncation fallback — LLM summarization can be layered on later once
	// provider access is available in the tool layer without import cycles.
	var summaryLine string
	if len(older) > 0 {
		summaryLine = fmt.Sprintf("[%d earlier messages not shown]", len(older))
	}

	// Step 7: Log handoff event in transcript as an audit trail (FR-016).
	currentAgentID := ToolAgentID(ctx)
	handoffContent := fmt.Sprintf("Handoff: %s → %s. Context: %s", currentAgentID, agentName, contextMsg)
	appendErr := t.sessionStore.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:   fmt.Sprintf("handoff-%d", time.Now().UnixNano()),
		Type: session.EntryTypeSystem,
		Role: "system",
		// AgentID = target so transcript hydration on a fresh turn picks
		// up this entry under the new agent's history (e.g. Ray sees the
		// brief on his first turn after Mia hands off to him).
		AgentID:   agentID,
		Content:   handoffContent,
		Timestamp: time.Now().UTC(),
	})
	if appendErr != nil {
		slog.Warn("handoff: could not write audit entry to transcript", "session", sessionID, "error", appendErr)
	}

	// Step 8: Notify frontend (so the UI can update its active-agent indicator).
	if t.onHandoff != nil {
		chatID := ToolChatID(ctx)
		if chatID == "" {
			slog.Warn("handoff: empty chatID; UI agent_switched will not target a specific connection",
				"session_id", sessionID)
		}
		t.onHandoff(HandoffEvent{
			Channel:   ToolChannel(ctx),
			ChatID:    chatID,
			SessionID: sessionID,
			AgentID:   agentID,
			AgentName: agentName,
		})
	}

	// Step 9: Return context for the target agent.
	forLLMParts := []string{
		fmt.Sprintf("Handoff complete. %s is now active.", agentName),
	}
	if summaryLine != "" {
		forLLMParts = append(forLLMParts, summaryLine)
	}
	if len(recent) > 0 {
		forLLMParts = append(forLLMParts, "Recent context:")
		forLLMParts = append(forLLMParts, formatRecentMessages(recent))
	}

	return &ToolResult{
		ForUser: fmt.Sprintf("Connecting you with %s...", agentName),
		ForLLM:  strings.Join(forLLMParts, "\n"),
	}
}

// resolveSessionID returns the session ID from context, preferring the transcript
// session ID (actual directory name) over the routing session key.
func resolveSessionID(ctx context.Context) string {
	if sid := ToolTranscriptSessionID(ctx); sid != "" {
		return sid
	}
	return ToolSessionKey(ctx)
}

// splitByTokenBudget partitions entries so that the entries in recent fit within
// budgetTokens (counting from the end of the transcript). Entries that do not
// fit are returned as older.
//
// The algorithm walks backward from the newest entry, accumulating estimated
// token counts until the budget is exhausted. All entries before the cutoff
// point are "older"; entries at or after the cutoff are "recent".
func splitByTokenBudget(entries []session.TranscriptEntry, budgetTokens int) (recent, older []session.TranscriptEntry) {
	if len(entries) == 0 {
		return nil, nil
	}
	tokensSoFar := 0
	cutoff := 0
	for i := len(entries) - 1; i >= 0; i-- {
		tokens := estimateEntryTokens(entries[i])
		if tokensSoFar+tokens > budgetTokens {
			cutoff = i + 1
			break
		}
		tokensSoFar += tokens
	}
	return entries[cutoff:], entries[:cutoff]
}

// estimateEntryTokens returns a fast token-count estimate for a transcript entry.
// When the entry carries a pre-computed Tokens value, that is used directly.
// Otherwise, content length is divided by 2.5 chars/token (matching context_budget.go).
func estimateEntryTokens(e session.TranscriptEntry) int {
	if e.Tokens > 0 {
		return e.Tokens
	}
	// ~2.5 chars per token — same heuristic as context_budget.go.
	return len(e.Content)/2 + 1
}

// formatRecentMessages renders a slice of transcript entries as a compact
// text block suitable for injection into the LLM's context.
func formatRecentMessages(entries []session.TranscriptEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, e := range entries {
		agentTag := ""
		if e.AgentID != "" {
			agentTag = fmt.Sprintf(" [%s]", e.AgentID)
		}
		sb.WriteString(fmt.Sprintf("%s%s: %s\n", e.Role, agentTag, e.Content))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ReturnToDefaultTool clears the session-level agent override by switching the
// active agent back to the configured default.
type ReturnToDefaultTool struct {
	BaseTool
	sessionStore    HandoffSessionStore
	getDefaultAgent func() string
	onHandoff       HandoffFunc
}

// NewReturnToDefaultTool creates a ReturnToDefaultTool.
//
//   - sessionStore is used to switch the active agent atomically.
//   - getDefaultAgent resolves the default agent ID from config at call time.
//   - onHandoff notifies the frontend of the agent switch; may be nil.
func NewReturnToDefaultTool(
	sessionStore HandoffSessionStore,
	getDefaultAgent func() string,
	onHandoff HandoffFunc,
) *ReturnToDefaultTool {
	return &ReturnToDefaultTool{
		sessionStore:    sessionStore,
		getDefaultAgent: getDefaultAgent,
		onHandoff:       onHandoff,
	}
}

func (t *ReturnToDefaultTool) Name() string           { return "return_to_default" }
func (t *ReturnToDefaultTool) Scope() ToolScope       { return ScopeCore }
func (t *ReturnToDefaultTool) Category() ToolCategory { return CategoryDelegation }

func (t *ReturnToDefaultTool) Description() string {
	return "Return the conversation to the default agent. Clears any active handoff override for this session."
}

func (t *ReturnToDefaultTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{
				"type":        "string",
				"description": "Optional summary of what was accomplished before returning",
			},
		},
		"required": []string{},
	}
}

func (t *ReturnToDefaultTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	sessionID := resolveSessionID(ctx)
	if sessionID == "" {
		return ErrorResult("return_to_default is not available in this context (no session key)")
	}

	defaultAgentID := t.getDefaultAgent()
	if defaultAgentID == "" {
		return ErrorResult("no default agent configured")
	}

	if err := t.sessionStore.SwitchAgent(sessionID, defaultAgentID); err != nil && !errors.Is(err, ErrAlreadyActive) {
		return ErrorResult(fmt.Sprintf("failed to return to default agent: %v", err))
	}

	// Build the message once for both audit trail and LLM response.
	summary, _ := args["summary"].(string)
	message := fmt.Sprintf("Returned to default agent (%s).", defaultAgentID)
	if summary != "" {
		message = fmt.Sprintf("Returned to default agent (%s). Summary: %s", defaultAgentID, summary)
	}

	// Log the return event as an audit trail (FR-016).
	currentAgentID := ToolAgentID(ctx)
	appendErr := t.sessionStore.AppendTranscript(sessionID, session.TranscriptEntry{
		ID:        fmt.Sprintf("return-%d", time.Now().UnixNano()),
		Type:      session.EntryTypeSystem,
		Role:      "system",
		Content:   message,
		AgentID:   currentAgentID,
		Timestamp: time.Now().UTC(),
	})
	if appendErr != nil {
		slog.Warn("handoff: could not write audit entry to transcript", "session", sessionID, "error", appendErr)
	}

	// Notify the frontend.
	if t.onHandoff != nil {
		chatID := ToolChatID(ctx)
		if chatID == "" {
			slog.Warn("return_to_default: empty chatID; UI agent_switched will not target a specific connection",
				"session_id", sessionID)
		}
		t.onHandoff(HandoffEvent{
			Channel:   ToolChannel(ctx),
			ChatID:    chatID,
			SessionID: sessionID,
			AgentID:   defaultAgentID,
			AgentName: defaultAgentID,
		})
	}

	return &ToolResult{
		ForUser: "Returning to default agent.",
		ForLLM:  message,
	}
}
