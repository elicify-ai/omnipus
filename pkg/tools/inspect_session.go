// Omnipus — inspect_session Agent Tool
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// InspectSessionStore is the subset of *session.UnifiedStore
// InspectSessionTool needs: metadata + transcript read, keyed purely on
// session ID (the store resolves the owning agent internally — mirrors
// DelegateSessionStore/HandoffSessionStore's narrow-interface pattern in
// this package, delegate.go/handoff.go).
type InspectSessionStore interface {
	// GetMeta returns the session's metadata (agent, channel, etc.).
	GetMeta(sessionID string) (*session.UnifiedMeta, error)
	// ReadTranscript returns all transcript entries for the session.
	ReadTranscript(sessionID string) ([]session.TranscriptEntry, error)
}

// maxInspectSessionEntries / maxInspectSessionChars / maxToolArgsSummaryChars
// bound inspect_session's output (ADR-052 spec FR-033: "token-bounded
// output") so a verifier's escalation call cannot blow its own context
// budget reading an arbitrarily long transcript.
const (
	maxInspectSessionEntries = 200
	maxInspectSessionChars   = 40000
	maxToolArgsSummaryChars  = 300
)

// InspectSessionTool implements the inspect_session agent tool (ADR-052
// FR-033, spec US-13 Acceptance 3, Test 23). READ-ONLY: it never mutates
// anything. Verifier-role-only by seeded tool policy (Constraint #6 —
// enforced by the tool-dispatch gate outside this package, not here); the
// TARGET-SESSION lock is NOT expressible in tool policy, so it is enforced
// here via an engine-set context value (WithVerifierSessionScope, base.go —
// precedent WithRunningTaskID): the tool refuses any session id outside the
// ctx-carried allow-set, unconditionally, regardless of what tool policy
// would otherwise permit. Scope referents per adjudication level (R3-11):
// task verification -> that task's own session; plan verification -> that
// plan's member sessions (no "plan session" exists, GS-04); chat `/goal`
// verification -> that chat session only. Setting the scope is the ENGINE's
// job (pkg/agent, out of this package) at verifier-dispatch time.
type InspectSessionTool struct {
	BaseTool
	store InspectSessionStore
}

// NewInspectSessionTool constructs an InspectSessionTool. store may be nil
// for metadata-only construction (GeneralBuiltinMetadata) — never Execute()d
// in that mode.
func NewInspectSessionTool(store InspectSessionStore) *InspectSessionTool {
	return &InspectSessionTool{store: store}
}

func (t *InspectSessionTool) Name() string           { return "inspect_session" }
func (t *InspectSessionTool) Scope() ToolScope       { return ScopeCore }
func (t *InspectSessionTool) Category() ToolCategory { return CategoryTasks }

func (t *InspectSessionTool) Description() string {
	return "READ-ONLY escalation tool for verification: inspect a session's transcript and tool-call " +
		"history for evidence beyond what you were already given. Usable only during adjudication and " +
		"only for the session(s) under review — any other session id is refused. Narrow the read with " +
		"`role` (user/assistant/system) and `tool_name` filters, and page long sessions with `offset` " +
		"and `limit` (max 200 entries per call, applied to the filtered sequence). Output is " +
		"token-bounded: if the response has truncated=true, re-call with `offset` advanced to read the rest."
}

func (t *InspectSessionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{
				"type":        "string",
				"description": "ID of the session to inspect — must be one of the session(s) under review for this verification",
			},
			"tool_name": map[string]any{
				"type":        "string",
				"description": "Optional filter: only include tool calls matching this tool name",
			},
			"role": map[string]any{
				"type":        "string",
				"enum":        []string{"user", "assistant", "system"},
				"description": "Optional filter: only include transcript entries with this role",
			},
			"offset": map[string]any{
				"type": "integer",
				"description": "Entry index to start from among the entries that pass the filters " +
					"(0-based, must be >= 0).",
				"default": 0,
			},
			"limit": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Maximum number of transcript entries to return (must be >= 1, capped at %d).",
					maxInspectSessionEntries),
				"default": maxInspectSessionEntries,
			},
		},
		"required": []string{"session_id"},
	}
}

// toolCallSummary is one entry's tool-call summary in inspect_session's
// response — name, an args summary (bounded), and success/failure.
type toolCallSummary struct {
	Name    string `json:"name"`
	Args    string `json:"args_summary,omitempty"`
	Success bool   `json:"success"`
}

// inspectSessionEntry is one transcript entry in inspect_session's response.
type inspectSessionEntry struct {
	ID        string            `json:"id"`
	Role      string            `json:"role,omitempty"`
	Content   string            `json:"content,omitempty"`
	Timestamp string            `json:"timestamp"`
	ToolCalls []toolCallSummary `json:"tool_calls,omitempty"`
}

// summarizeToolArgs renders params as a bounded JSON summary string. Returns
// "" for empty params. Truncates (rather than erroring) an oversized encoding
// so one very large call never blows the whole response budget.
func summarizeToolArgs(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	s := string(encoded)
	if len(s) > maxToolArgsSummaryChars {
		return s[:maxToolArgsSummaryChars] + "...[truncated]"
	}
	return s
}

func (t *InspectSessionTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.store == nil {
		return ErrorResult("inspect_session failed: session store is not available")
	}
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return ErrorResult("session_id is required")
	}

	// Bounding parameters (ADR-066 §15 task 1, FR-039): entries, named as
	// read_file names its byte window. Validated before the store is read so
	// a malformed call never returns transcript content.
	offset, err := getInt64Arg(args, "offset", 0)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if offset < 0 {
		return ErrorResult("offset must be >= 0")
	}
	limit, err := getInt64Arg(args, "limit", maxInspectSessionEntries)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if limit < 1 {
		return ErrorResult("limit must be >= 1")
	}
	if limit > maxInspectSessionEntries {
		limit = maxInspectSessionEntries
	}

	// Target-lock (FR-033) — refuse any session id outside the engine-set
	// scope, unconditionally. This is NOT a tool-policy-expressible check;
	// it is enforced purely from the ctx value the engine set before
	// dispatching this verifier turn. A turn with no scope set at all (i.e.
	// not a verifier turn) refuses every session id — fail closed.
	if !VerifierSessionScopeAllows(ctx, sessionID) {
		return ErrorResult(fmt.Sprintf(
			"inspect_session refused: session %q is outside the scope authorized for this verification",
			sessionID))
	}

	meta, mErr := t.store.GetMeta(sessionID)
	if mErr != nil {
		return ErrorResult(fmt.Sprintf("inspect_session failed: could not read session metadata: %v", mErr))
	}
	entries, tErr := t.store.ReadTranscript(sessionID)
	if tErr != nil {
		return ErrorResult(fmt.Sprintf("inspect_session failed: could not read transcript: %v", tErr))
	}

	toolNameFilter, _ := args["tool_name"].(string)
	roleFilter, _ := args["role"].(string)

	out := make([]inspectSessionEntry, 0, min(int(limit), len(entries)))
	totalChars := 0
	truncated := false
	// matched counts the entries that pass the filters, whether or not they
	// are on the requested page; offset is applied to that filtered sequence
	// so paging is stable under a filter (FR-039).
	matched := 0
	// ADR-057 D1/W11 (FR-034/FR-038): a delegated child now owns its own real
	// store-backed session (FR-005), so its narration lives in the CHILD's
	// OWN transcript.jsonl and never lands in this session's entries at all
	// — the old retired child-entry visibility predicate's skip is removed
	// outright, not reapplied (FR-034). inspect_session against a child's own sessionID now returns
	// that child's full transcript unfiltered, same as every other read
	// boundary (BDD-37).
	for _, e := range entries {
		if roleFilter != "" && e.Role != roleFilter {
			continue
		}
		var calls []toolCallSummary
		for _, tc := range e.ToolCalls {
			if toolNameFilter != "" && tc.Tool != toolNameFilter {
				continue
			}
			calls = append(calls, toolCallSummary{
				Name:    tc.Tool,
				Args:    summarizeToolArgs(tc.Parameters),
				Success: tc.Status == "success",
			})
		}
		// When filtering by tool_name, an entry whose tool calls existed but
		// none matched the filter — and which carries no plain message
		// content either — contributes nothing; drop it.
		if toolNameFilter != "" && len(calls) == 0 && len(e.ToolCalls) > 0 && e.Content == "" {
			continue
		}

		matched++
		if int64(matched) <= offset {
			// Before the requested page — skipped entries cost no budget.
			continue
		}

		totalChars += len(e.Content)
		if totalChars > maxInspectSessionChars || int64(len(out)) >= limit {
			truncated = true
			break
		}
		out = append(out, inspectSessionEntry{
			ID:        e.ID,
			Role:      e.Role,
			Content:   e.Content,
			Timestamp: e.Timestamp.Format(time.RFC3339),
			ToolCalls: calls,
		})
	}

	payload := map[string]any{
		"session_id":  sessionID,
		"agent_id":    meta.AgentID,
		"entries":     out,
		"entry_count": len(out),
		"offset":      offset,
		"truncated":   truncated,
	}
	encoded, mErr2 := json.Marshal(payload)
	if mErr2 != nil {
		return ErrorResult(fmt.Sprintf("inspect_session failed: could not encode result: %v", mErr2))
	}
	return NewToolResult(string(encoded))
}
