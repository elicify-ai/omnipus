//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	generated "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/security"
)

// wsApprovalEntry holds the in-flight decision channel and the session that made the request.
type wsApprovalEntry struct {
	ch        chan agent.ApprovalDecision
	SessionID string // session_id at request time; validated on response
}

// wsApprovalRegistry tracks in-flight tool-approval requests awaiting browser decisions.
// Each entry maps a request UUID to a channel on which the approval decision will be sent.
// Entries are inserted by wsApprovalHook.ApproveTool and resolved (or expired) by the
// WSHandler readLoop when it processes "exec_approval_response" frames.
type wsApprovalRegistry struct {
	mu      sync.Mutex
	pending map[string]*wsApprovalEntry
}

func newWSApprovalRegistry() *wsApprovalRegistry {
	return &wsApprovalRegistry{
		pending: make(map[string]*wsApprovalEntry),
	}
}

// register creates a buffered decision channel for the given request ID and returns it.
// sessionID is recorded for validation when the response arrives.
// If a channel for this ID already exists (UUID collision or duplicate call), it logs a
// warning and returns the existing channel — the caller must still call unregister.
// The caller must call unregister when done (whether the decision arrived or not).
func (r *wsApprovalRegistry) register(id, sessionID string) chan agent.ApprovalDecision {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.pending[id]; ok {
		slog.Warn("ws: approval registry: duplicate request ID", "id", id)
		return existing.ch
	}
	entry := &wsApprovalEntry{
		ch:        make(chan agent.ApprovalDecision, 1),
		SessionID: sessionID,
	}
	r.pending[id] = entry
	return entry.ch
}

// getSessionID returns the session_id recorded for the given request ID.
// Returns ("", false) if no pending request with that ID exists.
func (r *wsApprovalRegistry) getSessionID(id string) (string, bool) {
	r.mu.Lock()
	entry, ok := r.pending[id]
	r.mu.Unlock()
	if !ok {
		return "", false
	}
	return entry.SessionID, true
}

// unregister removes the pending entry for the given request ID.
func (r *wsApprovalRegistry) unregister(id string) {
	r.mu.Lock()
	delete(r.pending, id)
	r.mu.Unlock()
}

// resolve delivers a decision to the waiting ApproveTool call.
// Returns false if no pending request with that ID exists (e.g. it already timed out).
func (r *wsApprovalRegistry) resolve(id string, decision agent.ApprovalDecision) bool {
	r.mu.Lock()
	entry, ok := r.pending[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case entry.ch <- decision:
		return true
	default:
		// Channel already has a value — possible duplicate response from the browser.
		slog.Warn("ws: approval registry: channel already has value — possible duplicate response", "id", id)
		return false
	}
}

// wsApprovalHook implements agent.ToolApprover for a single WebSocket connection.
// When ApproveTool is called by the agent loop, it:
//  1. Generates a unique request ID.
//  2. Registers a pending channel in the registry.
//  3. Sends an "exec_approval_request" frame to the browser via the connection.
//  4. Blocks until the context is canceled, the registry resolves the decision, or
//     the approval timeout fires.
//
// The WSHandler readLoop calls registry.resolve when it receives an
// "exec_approval_response" frame from the browser.
type wsApprovalHook struct {
	conn     *wsConn
	chatID   string // the chatID this connection owns — only handle requests for this chatID
	registry *wsApprovalRegistry
	// timeout is the per-request approval deadline. Defaults to wsApprovalTimeout.
	timeout time.Duration

	// policyResolver returns the tool policy ("allow"/"ask"/"deny") for a tool
	// on a specific agent. If nil, falls back to autoApproveSafeTool.
	policyResolver func(toolName string, agentID string) string

	// approvalGrants is the session-scoped "Always Allow" grant store, shared
	// across ALL WebSocket connections via the parent AgentLoop
	// (al.ApprovalGrants()). This replaces the old per-connection
	// alwaysAllowed map, which was wiped out on every reconnect (network
	// blip, idle, gateway restart, refresh — the SPA auto-reconnects on any
	// drop) because a fresh wsApprovalHook was mounted with an empty map each
	// time. Nil-safe: every ApprovalGrantStore method fails closed (IsAllowed
	// => false, i.e. "ask") on a nil store, so a hook built without this
	// field set (e.g. in tests) never silently auto-approves.
	approvalGrants *security.ApprovalGrantStore
}

const wsApprovalTimeout = 90 * time.Second

// ApproveTool sends the tool name and arguments to the connected browser and waits
// for a human decision. It denies execution on timeout or context cancellation.
func (h *wsApprovalHook) ApproveTool(
	ctx context.Context,
	req *agent.ToolApprovalRequest,
) (agent.ApprovalDecision, error) {
	if h == nil || h.conn == nil || h.registry == nil {
		// No active WebSocket — deny by default rather than approving blindly.
		return agent.Deny("no active WebSocket connection for interactive approval"), nil
	}

	// Only handle requests for this connection's chatID. Other connections' hooks
	// will handle their own requests. Returning Allow here means "I have no opinion"
	// so the HookManager continues to the next hook (the one that owns the chatID).
	if h.chatID != "" && req.ChatID != "" && h.chatID != req.ChatID {
		slog.Debug(
			"ws: approval hook skipped — chatID mismatch",
			"hook_chat_id",
			h.chatID,
			"request_chat_id",
			req.ChatID,
		)
		return agent.ApprovalDecision{Verdict: agent.VerdictAllow}, nil
	}

	// Check tool policy: allow → auto-approve, ask → show dialog, deny → reject.
	policy := "ask" // default to ask if no resolver
	if h.policyResolver != nil {
		policy = h.policyResolver(req.Tool, req.Meta.AgentID)
	} else if autoApproveSafeTool(req.Tool) {
		policy = "allow"
	}
	switch policy {
	case "allow":
		return agent.ApprovalDecision{Verdict: agent.VerdictAllow}, nil
	case "deny":
		return agent.ApprovalDecision{Verdict: agent.VerdictDeny, Reason: "tool denied by agent policy"}, nil
	}
	// policy == "ask" — fall through to interactive approval

	// Check if this (session_id, agent_id, tool) triple was previously
	// granted "Always Allow" by the user. Scoped by session AND agent (not
	// just tool name) so a DIFFERENT agent in the same session — or the same
	// agent in a DIFFERENT session — still has to ask. Backed by
	// AgentLoop.ApprovalGrants(), which SURVIVES WebSocket reconnects (the
	// consent-boundary bug this replaces: the grant used to live on this
	// per-connection hook and was discarded on every reconnect). Fail-safe:
	// IsAllowed returns false for a nil store or an empty session_id/
	// agent_id/tool — it never silently auto-approves on ambiguous input.
	if h.approvalGrants.IsAllowed(req.SessionID, req.Meta.AgentID, req.Tool) {
		slog.Debug("ws: tool auto-approved (always allowed)",
			"tool", req.Tool, "session_id", req.SessionID, "agent_id", req.Meta.AgentID)
		return agent.ApprovalDecision{Verdict: agent.VerdictAlways}, nil
	}

	id := uuid.New().String()
	ch := h.registry.register(id, req.SessionID)
	defer h.registry.unregister(id)

	// Send the approval-request frame to the browser using the generated type.
	// Decision: Option B — the spec requires `command` (what the SPA reads); we set it
	// to req.Tool (the tool name). The optional `tool` and `params` fields carry
	// the structured data for richer programmatic consumers.
	msg := fmt.Sprintf("Agent wants to call tool %q. Allow?", req.Tool)
	approvalFrame := generated.ExecApprovalRequestFrame{
		Type:      string(generated.WsFrameTypeExecApprovalRequest),
		Id:        id,
		SessionId: req.SessionID,
		Command:   req.Tool,
		Message:   &msg,
	}
	if req.Tool != "" {
		toolCopy := req.Tool
		approvalFrame.Tool = &toolCopy
	}
	if len(req.Arguments) > 0 {
		approvalFrame.Params = req.Arguments
	}
	sendConnGenFrame(h.conn, string(generated.WsFrameTypeExecApprovalRequest), approvalFrame)

	slog.Info("ws: sent exec_approval_request to browser", "id", id, "tool", req.Tool)

	timeout := h.timeout
	if timeout <= 0 {
		timeout = wsApprovalTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decision := <-ch:
		slog.Info("ws: exec_approval_response received", "id", id, "verdict", decision.Verdict)
		if decision.Verdict == agent.VerdictAlways {
			h.approvalGrants.Record(req.SessionID, req.Meta.AgentID, req.Tool)
			slog.Info("ws: tool added to always-allowed list",
				"tool", req.Tool, "session_id", req.SessionID, "agent_id", req.Meta.AgentID)
		}
		return decision, nil
	case <-timer.C:
		slog.Warn("ws: exec_approval_request timed out — denying tool execution", "id", id, "tool", req.Tool)
		// Inform the browser the request expired using the generated type.
		expiredMsg := fmt.Sprintf(
			"Approval request for %q timed out after %s — tool execution denied.",
			req.Tool,
			timeout,
		)
		expiredFrame := generated.ExecApprovalExpiredFrame{
			Type:      string(generated.WsFrameTypeExecApprovalExpired),
			Id:        id,
			SessionId: req.SessionID,
			Message:   &expiredMsg,
		}
		sendConnGenFrame(h.conn, string(generated.WsFrameTypeExecApprovalExpired), expiredFrame)
		return agent.Deny("approval timed out"), nil
	case <-h.conn.doneCh:
		slog.Warn(
			"ws: connection closed while waiting for approval — denying tool execution",
			"id",
			id,
			"tool",
			req.Tool,
		)
		return agent.Deny("WebSocket connection closed during approval"), nil
	case <-ctx.Done():
		slog.Warn(
			"ws: context canceled while waiting for approval — denying tool execution",
			"id",
			id,
			"tool",
			req.Tool,
		)
		return agent.Deny("context canceled"), ctx.Err()
	}
}

// autoApproveSafeTool returns true for tools that are pre-approved without interactive confirmation.
// These are low-risk tools: read-only operations, workspace-scoped writes, web research,
// and agent orchestration (spawn/subagent).
// Only exec (shell commands) requires explicit user approval.
func autoApproveSafeTool(tool string) bool {
	switch tool {
	case "read_file", "list_directory", "write_file", "edit_file", "append_file",
		"search_web", "fetch_url", "send_file", "send_message",
		"find_skills", "spawn", "run_subagent", "check_spawn_status",
		"list_tasks", "create_task", "update_task", "delete_task", "list_agents":
		return true
	default:
		return false
	}
}
