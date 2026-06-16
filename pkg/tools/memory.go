// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// MemoryEntry is the common result type returned by MemorySearcher.
// It mirrors agent.LongTermEntry without creating an import dependency on pkg/agent.
type MemoryEntry struct {
	Timestamp time.Time
	Category  string
	Content   string
}

// MemoryRetro is the retro record passed to MemoryWriter.AppendRetro.
// It mirrors agent.Retro without creating an import dependency on pkg/agent.
type MemoryRetro struct {
	Timestamp        time.Time
	Trigger          string
	Fallback         bool
	FallbackReason   string
	Recap            string
	WentWell         []string
	NeedsImprovement []string
}

// MemoryWriter is the write-side interface of an agent's durable memory store.
// Defined here (not in pkg/agent) so pkg/tools does not import pkg/agent —
// that would create a circular import. pkg/agent.MemoryStore satisfies this
// interface via the MemoryStoreAdapter defined in pkg/agent/memory_adapter.go.
type MemoryWriter interface {
	AppendLongTerm(content, category string) error
	AppendRetro(sessionID string, r MemoryRetro) error
}

// MemorySearcher is the read-side interface.
type MemorySearcher interface {
	SearchEntries(query string, limit int) ([]MemoryEntry, error)
}

// MemoryAccess bundles write and search access for the memory tools.
type MemoryAccess interface {
	MemoryWriter
	MemorySearcher
}

// RoomMemoryWriter extends MemoryWriter with room-aware write (MIN-002 / FR-7.3).
// pkg/agent.MemoryStoreAdapter satisfies this via AppendLongTermToRoom +
// SetWorkspaceID. Tools type-assert to this interface to route by room.
// Using a string scope ("private" | "shared" | "both") avoids importing
// pkg/memrooms into pkg/tools (cycle prevention).
type RoomMemoryWriter interface {
	MemoryWriter
	// AppendLongTermToRoom writes to the room identified by scope string.
	// Accepted values: "private" | "shared" | "both" (both→ private for writes).
	AppendLongTermToRoom(content, category, scope string) error
	// SetWorkspaceID wires the shared room for the current turn.
	SetWorkspaceID(workspaceID string)
}

// RoomMemorySearcher extends MemorySearcher with room-aware read (MIN-002 / FR-7.3).
type RoomMemorySearcher interface {
	MemorySearcher
	// SearchEntriesInRoom searches the specified room scope.
	SearchEntriesInRoom(query string, limit int, scope string) ([]MemoryEntry, error)
}

// RoomMemoryAccess combines room-aware write + search.
type RoomMemoryAccess interface {
	RoomMemoryWriter
	RoomMemorySearcher
}

// RememberTool appends a fact, decision, reference, or lesson to a memory room.
// FR-7.3 (re-point to rooms), FR-013 (original tool).
type RememberTool struct {
	BaseTool
	store       MemoryAccess
	auditLogger *audit.Logger
	// rateLimiter is the v0.2 #155 item 6 rate limiter. May be nil — when
	// nil the gate is bypassed and writes always proceed. The agent loop
	// installs a real limiter via SetMemoryRateLimiter on the registry.
	rateLimiter *MemoryRateLimiter
}

// NewRememberTool creates a RememberTool backed by the given MemoryAccess.
func NewRememberTool(store MemoryAccess, auditLogger *audit.Logger) *RememberTool {
	return &RememberTool{store: store, auditLogger: auditLogger}
}

// SetAuditLogger satisfies the auditLoggerAware interface so the registry can
// propagate the audit logger after construction (SEC-15 late-wire pattern).
func (t *RememberTool) SetAuditLogger(logger *audit.Logger) {
	t.auditLogger = logger
}

// SetMemoryRateLimiter satisfies the memoryRateLimiterAware interface so the
// registry can propagate the rate limiter after construction (v0.2 #155
// item 6 late-wire pattern). Passing nil clears the limiter and bypasses
// the gate — used by tests and explicitly-disabled deployments.
func (t *RememberTool) SetMemoryRateLimiter(limiter *MemoryRateLimiter) {
	t.rateLimiter = limiter
}

func (t *RememberTool) Name() string     { return "remember" }
func (t *RememberTool) Scope() ToolScope { return ScopeGeneral }
func (t *RememberTool) Description() string {
	return "Persist a fact, decision, reference, or lesson to long-term memory. " +
		"Use category 'key_decision' for decisions made, 'reference' for reference information, " +
		"or 'lesson_learned' for retrospective lessons. Each memory is stored as a structured " +
		"file with full metadata. Use room='shared' to write to the workspace shared room (visible " +
		"to all agents in this workspace), 'private' for agent-only memory. Default: shared when " +
		"in a workspace session, private otherwise."
}

func (t *RememberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The fact, decision, reference, or lesson to remember. Max 4096 characters.",
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        []string{"key_decision", "reference", "lesson_learned"},
				"description": "Category: key_decision, reference, or lesson_learned.",
			},
			"room": map[string]any{
				"type": "string",
				"enum": []string{"private", "shared"},
				"description": "Room to write to: 'private' (agent-only) or 'shared' (workspace-wide). " +
					"Default: shared when in a workspace session, private otherwise.",
			},
		},
		"required": []string{"content", "category"},
	}
}

func (t *RememberTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	content, _ := args["content"].(string)
	category, _ := args["category"].(string)
	roomParam, _ := args["room"].(string)

	agentID := ToolAgentID(ctx)
	sessionID := ToolTranscriptSessionID(ctx)
	workspaceID := ToolWorkspaceID(ctx)

	if strings.TrimSpace(content) == "" {
		t.logAudit(agentID, sessionID, "error_invalid", category, content)
		return ErrorResult("remember: content must not be empty")
	}

	// FR-019: enforce the 4096-character content cap at the tool boundary.
	if len([]rune(strings.TrimSpace(content))) > 4096 {
		t.logAudit(agentID, sessionID, "error_invalid", category, content)
		return ErrorResult("remember: content exceeds 4096 characters; shorten the entry and try again")
	}

	// v0.2 #155 item 6: rate-limit memory writes.
	if decision := t.checkRateLimit(ctx, agentID); !decision.Allowed {
		t.logRateLimited(agentID, sessionID, callerIdentity(ctx), category, content, decision)
		return rateLimitedResult("remember", decision)
	}

	// Route to the correct room (FR-7.1, MIN-002).
	// Type-assert to RoomMemoryWriter if the store supports room-aware writes.
	if rw, ok := t.store.(RoomMemoryWriter); ok {
		// Wire the workspace ID for this turn so the store can resolve the shared room.
		rw.SetWorkspaceID(workspaceID)

		// Resolve the scope: explicit param > default (shared if workspace, else private).
		scope := resolveRoomScope(roomParam, workspaceID)
		if err := rw.AppendLongTermToRoom(content, category, scope); err != nil {
			t.logAudit(agentID, sessionID, "error_io", category, content)
			return ErrorResult(fmt.Sprintf("remember: %v", err))
		}
	} else {
		// Fallback for plain MemoryAccess stores (e.g., test doubles).
		if err := t.store.AppendLongTerm(content, category); err != nil {
			t.logAudit(agentID, sessionID, "error_io", category, content)
			return ErrorResult(fmt.Sprintf("remember: %v", err))
		}
	}

	t.logAudit(agentID, sessionID, "ok", category, content)
	return SilentResult("ok")
}

// checkRateLimit returns the rate-limit decision for the (agent, caller)
// pair derived from ctx. When the limiter is nil (no rate limiting
// configured) the decision is always Allowed=true.
func (t *RememberTool) checkRateLimit(ctx context.Context, agentID string) MemoryRateLimitDecision {
	return t.rateLimiter.Allow(agentID, callerIdentity(ctx))
}

// logRateLimited emits an audit entry for a rate-limit-rejected remember
// call. The entry uses Decision="deny" and Event="memory.rate_limited"
// (v0.2 #155 item 6) so SIEM rules can route on it independently from
// the success-path "memory.remember" event. Content is never logged raw —
// only a SHA-256 hex digest is emitted.
func (t *RememberTool) logRateLimited(
	agentID, sessionID, caller, category, content string,
	decision MemoryRateLimitDecision,
) {
	if t.auditLogger == nil {
		return
	}
	sum := sha256.Sum256([]byte(content))
	entry := &audit.Entry{
		Timestamp:  time.Now().UTC(),
		Event:      "memory.rate_limited",
		Decision:   "deny",
		AgentID:    agentID,
		SessionID:  sessionID,
		Tool:       "remember",
		PolicyRule: fmt.Sprintf("memory_rate_limit:%s", decision.Scope),
		Details: map[string]any{
			"outcome":             "rate_limited",
			"scope":               decision.Scope,
			"retry_after_seconds": int(decision.RetryAfter.Round(time.Second).Seconds()),
			"category":            category,
			"content_sha256":      fmt.Sprintf("%x", sum),
			"byte_count":          len([]byte(content)),
			// HIGH (silent-failure-hunter, #155): record the per-caller
			// bucket key so a forensic question "which Telegram chat is
			// hammering remember?" is answerable from audit alone. The
			// caller identity is not a secret — it's the channel:chat_id
			// pair, already visible elsewhere in the audit trail.
			"caller": caller,
		},
	}
	if err := t.auditLogger.Log(entry); err != nil {
		slog.Warn("memory: rate-limit audit log failed",
			"tool", "remember",
			"agent_id", agentID,
			"session_id", sessionID,
			"scope", decision.Scope,
			"error", err,
		)
	}
}

// logAudit emits an audit entry for the remember tool.
// Content is never logged raw — only a SHA-256 hex digest is emitted.
func (t *RememberTool) logAudit(agentID, sessionID, outcome, category, content string) {
	if t.auditLogger == nil {
		return
	}
	sum := sha256.Sum256([]byte(content))
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     "memory.remember",
		AgentID:   agentID,
		SessionID: sessionID,
		Tool:      "remember",
		Details: map[string]any{
			"outcome":        outcome,
			"category":       category,
			"content_sha256": fmt.Sprintf("%x", sum),
			"byte_count":     len([]byte(content)),
			"trigger":        "user_call",
		},
	}
	if err := t.auditLogger.Log(entry); err != nil {
		// Audit failures never block tool execution (FR-012), but they must
		// still be visible — a silently-dropped audit write hides real
		// logger regressions.
		slog.Warn("memory: remember audit log failed",
			"tool", "remember",
			"agent_id", agentID,
			"session_id", sessionID,
			"error", err,
		)
	}
}

// RecallMemoryTool searches long-term memory, last session, and retrospectives.
// FR-014: not audited (read-side stays clean).
//
// Asks for MemorySearcher only — the narrower of the two interfaces. This is
// intentional: read tools must not have write capability even via type
// coercion, and ISP-splitting the backend interface documents the contract.
type RecallMemoryTool struct {
	BaseTool
	store MemorySearcher
}

// NewRecallMemoryTool creates a RecallMemoryTool backed by the given searcher.
// Callers that have a MemoryAccess can pass it here — MemoryAccess embeds
// MemorySearcher, so it satisfies the parameter interface.
func NewRecallMemoryTool(store MemorySearcher) *RecallMemoryTool {
	return &RecallMemoryTool{store: store}
}

func (t *RecallMemoryTool) Name() string     { return "recall_memory" }
func (t *RecallMemoryTool) Scope() ToolScope { return ScopeGeneral }
func (t *RecallMemoryTool) Description() string {
	return "Search durable long-term memory. Returns matching entries newest-first. " +
		"Use this when you need to recall a past decision, reference, or lesson. " +
		"Use room='both' (default) to search all available rooms, 'private' for agent-only, " +
		"'shared' for workspace-wide memories."
}

func (t *RecallMemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Literal substring to search for (case-insensitive). No regex.",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum number of results (default 20, max 50).",
			},
			"room": map[string]any{
				"type":        "string",
				"enum":        []string{"private", "shared", "both"},
				"description": "Room scope: 'private' (agent-only), 'shared' (workspace), or 'both' (default).",
			},
		},
		"required": []string{"query"},
	}
}

func (t *RecallMemoryTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return ErrorResult("recall_memory: query must not be empty")
	}

	limit := 20
	if raw, ok := args["limit"]; ok && raw != nil {
		switch v := raw.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		}
	}

	roomParam, _ := args["room"].(string)
	workspaceID := ToolWorkspaceID(ctx)

	// Wire workspace_id and use room-aware search when the store supports it.
	if rs, ok := t.store.(RoomMemorySearcher); ok {
		// Also wire the workspace ID on the write side if it implements RoomMemoryWriter.
		if rw, ok2 := t.store.(RoomMemoryWriter); ok2 {
			rw.SetWorkspaceID(workspaceID)
		}
		// Recall default: both rooms (the broader the better for recall).
		scope := roomParam
		if scope == "" {
			scope = "both"
		}
		entries, err := rs.SearchEntriesInRoom(query, limit, scope)
		if err != nil {
			return ErrorResult(fmt.Sprintf("recall_memory: %v", err))
		}
		return formatRecallResult(entries)
	}

	// Fallback for plain MemorySearcher (test doubles etc.).
	entries, err := t.store.SearchEntries(query, limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("recall_memory: %v", err))
	}
	return formatRecallResult(entries)
}

func formatRecallResult(entries []MemoryEntry) *ToolResult {
	if len(entries) == 0 {
		return NewToolResult("no matching entries")
	}
	var sb strings.Builder
	for i, e := range entries {
		if i > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		ts := e.Timestamp.UTC().Format("2006-01-02T15:04:05Z")
		fmt.Fprintf(&sb, "[%s | %s]\n%s", ts, e.Category, e.Content)
	}
	return NewToolResult(sb.String())
}

// RetrospectiveTool records a session retrospective.
// FR-015.
type RetrospectiveTool struct {
	BaseTool
	store       MemoryAccess
	auditLogger *audit.Logger
	// rateLimiter is the v0.2 #155 item 6 rate limiter. May be nil — when
	// nil the gate is bypassed.
	rateLimiter *MemoryRateLimiter
}

// NewRetrospectiveTool creates a RetrospectiveTool backed by the given MemoryAccess.
func NewRetrospectiveTool(store MemoryAccess, auditLogger *audit.Logger) *RetrospectiveTool {
	return &RetrospectiveTool{store: store, auditLogger: auditLogger}
}

// SetAuditLogger satisfies the auditLoggerAware interface so the registry can
// propagate the audit logger after construction (SEC-15 late-wire pattern).
func (t *RetrospectiveTool) SetAuditLogger(logger *audit.Logger) {
	t.auditLogger = logger
}

// SetMemoryRateLimiter satisfies the memoryRateLimiterAware interface so the
// registry can propagate the rate limiter after construction (v0.2 #155
// item 6 late-wire pattern).
func (t *RetrospectiveTool) SetMemoryRateLimiter(limiter *MemoryRateLimiter) {
	t.rateLimiter = limiter
}

func (t *RetrospectiveTool) Name() string     { return "retrospective" }
func (t *RetrospectiveTool) Scope() ToolScope { return ScopeGeneral }
func (t *RetrospectiveTool) Description() string {
	return "Record a session retrospective after confirming its contents with the user. " +
		"Captures what went well and what needs improvement for future reference. " +
		"Call this at the end of a productive session after the user has reviewed the summary."
}

func (t *RetrospectiveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"went_well": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "List of things that went well this session.",
			},
			"needs_improvement": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "List of things to improve in future sessions.",
			},
		},
		"required": []string{"went_well", "needs_improvement"},
	}
}

func (t *RetrospectiveTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	wentWell := extractStringSlice(args["went_well"])
	needsImprovement := extractStringSlice(args["needs_improvement"])

	if len(wentWell) == 0 && len(needsImprovement) == 0 {
		return ErrorResult("retrospective: at least one of went_well or needs_improvement must be non-empty")
	}

	sessionID := ToolTranscriptSessionID(ctx)
	agentID := ToolAgentID(ctx)

	// Fall back to a timestamp-based ID when no session ID is in context,
	// so retrospectives can still be recorded from direct tool calls.
	if sessionID == "" {
		sessionID = fmt.Sprintf("session-%d", time.Now().UnixMilli())
	}

	r := MemoryRetro{
		Timestamp:        time.Now().UTC(),
		Trigger:          "joined",
		Fallback:         false,
		Recap:            "",
		WentWell:         wentWell,
		NeedsImprovement: needsImprovement,
	}

	// v0.2 #155 item 6: rate-limit memory writes. Same gate as RememberTool.
	// Counts against the SAME per-agent + per-caller buckets so a single
	// agent can't trivially work around the limit by alternating remember
	// and retrospective calls.
	if decision := t.rateLimiter.Allow(agentID, callerIdentity(ctx)); !decision.Allowed {
		t.logRateLimited(agentID, sessionID, callerIdentity(ctx), r, decision)
		return rateLimitedResult("retrospective", decision)
	}

	if err := t.store.AppendRetro(sessionID, r); err != nil {
		t.logAudit(agentID, sessionID, "error_io", r)
		return ErrorResult(fmt.Sprintf("retrospective: %v", err))
	}

	t.logAudit(agentID, sessionID, "ok", r)
	return SilentResult("ok")
}

// logRateLimited emits a memory.rate_limited audit entry for retrospective
// writes that were rejected by the rate-limit gate (v0.2 #155 item 6).
func (t *RetrospectiveTool) logRateLimited(
	agentID, sessionID, caller string,
	r MemoryRetro,
	decision MemoryRateLimitDecision,
) {
	if t.auditLogger == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp:  time.Now().UTC(),
		Event:      "memory.rate_limited",
		Decision:   "deny",
		AgentID:    agentID,
		SessionID:  sessionID,
		Tool:       "retrospective",
		PolicyRule: fmt.Sprintf("memory_rate_limit:%s", decision.Scope),
		Details: map[string]any{
			"outcome":             "rate_limited",
			"scope":               decision.Scope,
			"retry_after_seconds": int(decision.RetryAfter.Round(time.Second).Seconds()),
			"went_well_count":     len(r.WentWell),
			"needs_improve_count": len(r.NeedsImprovement),
			"caller":              caller, // see RememberTool.logRateLimited
		},
	}
	if err := t.auditLogger.Log(entry); err != nil {
		slog.Warn("memory: retrospective rate-limit audit log failed",
			"tool", "retrospective",
			"agent_id", agentID,
			"session_id", sessionID,
			"scope", decision.Scope,
			"error", err,
		)
	}
}

// logAudit emits an audit entry for the retrospective tool.
func (t *RetrospectiveTool) logAudit(agentID, sessionID, outcome string, r MemoryRetro) {
	if t.auditLogger == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     "memory.retrospective",
		AgentID:   agentID,
		SessionID: sessionID,
		Tool:      "retrospective",
		Details: map[string]any{
			"outcome":             outcome,
			"went_well_count":     len(r.WentWell),
			"needs_improve_count": len(r.NeedsImprovement),
			"trigger":             "user_call",
		},
	}
	if err := t.auditLogger.Log(entry); err != nil {
		// Audit failures never block tool execution (FR-012), but they must
		// still be visible — a silently-dropped audit write hides real
		// logger regressions.
		slog.Warn("memory: retrospective audit log failed",
			"tool", "retrospective",
			"agent_id", agentID,
			"session_id", sessionID,
			"error", err,
		)
	}
}

// resolveRoomScope determines the effective room scope string for a write operation.
// roomParam is the explicit param from the LLM (may be ""); workspaceID is from ctx.
// Logic: if explicit param is given, use it; else default to "shared" when a workspace
// session is active, "private" otherwise.
func resolveRoomScope(roomParam, workspaceID string) string {
	if roomParam == "private" || roomParam == "shared" {
		return roomParam
	}
	if workspaceID != "" {
		return "shared"
	}
	return "private"
}

// callerIdentity derives a stable string identifying the originating
// caller of a tool execution, suitable for the per-caller bucket of
// MemoryRateLimiter (v0.2 #155 item 6).
//
// Tool calls happen inside the agent loop, not in the request handler, so
// there is no literal HTTP `r.RemoteAddr` to key on. The closest analog is
// the channel + chat ID pair already plumbed through the tool context
// (e.g. "telegram:123456789", "rest:user-x", "websocket:session_01XXXX").
// If both are empty (rare — direct loop test invocation) we fall back to
// "<local>" so all such callers share one bucket and the limit still
// applies as a per-process ceiling.
//
// The pair is concatenated with ':' rather than encoded as JSON for
// human-readable audit entries; channel and chat ID values are restricted
// to a small alphabet by the channel implementations so collision is not
// a concern.
func callerIdentity(ctx context.Context) string {
	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	if channel == "" && chatID == "" {
		return "<local>"
	}
	if chatID == "" {
		return channel
	}
	if channel == "" {
		return chatID
	}
	return channel + ":" + chatID
}

// rateLimitedResult builds the ToolResult returned to the LLM when a
// memory write is rejected by the rate-limit gate. The message embeds a
// machine-parseable suffix so a model that knows to look for it can
// reschedule the call, and a human-readable prefix so an operator
// reading the transcript understands what happened. error_kind is
// included as a JSON-style fragment to match the spec's wire shape.
func rateLimitedResult(toolName string, decision MemoryRateLimitDecision) *ToolResult {
	retryAfterSecs := int(decision.RetryAfter.Round(time.Second).Seconds())
	if retryAfterSecs < 1 {
		retryAfterSecs = 1
	}
	msg := fmt.Sprintf(
		`%s: rate limit exceeded for the %s scope; retry after %d seconds. error_kind="rate_limited" retry_after_seconds=%d`,
		toolName,
		decision.Scope,
		retryAfterSecs,
		retryAfterSecs,
	)
	return &ToolResult{
		ForLLM:  msg,
		Silent:  true,
		IsError: true,
	}
}

// extractStringSlice safely converts an interface{} to []string.
// Accepts []interface{} (JSON unmarshaled) and []string directly.
func extractStringSlice(raw any) []string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}
