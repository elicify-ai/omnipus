// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements the session-scoped "Always Allow" tool-approval grant
// store — the fix for the tool-consent-boundary bug where a user's "Always
// Allow" decision on the exec/tool approval dialog was recorded on a
// per-WebSocket-CONNECTION map (wsApprovalHook.alwaysAllowed) instead of a
// per-SESSION store. Because the SPA reconnects on any drop (network blip,
// idle, gateway restart, refresh), every reconnect created a fresh hook with
// an empty map, silently discarding the grant and re-prompting the user.
//
// ApprovalGrantStore fixes this by keying grants on (session_id, agent_id,
// tool_name) rather than the connection, so the grant survives reconnects for
// the lifetime of the SESSION, and correctly requires a fresh prompt when a
// DIFFERENT agent (in the same session) or a DIFFERENT session calls the same
// tool.

package security

import "sync"

// grantKey scopes a set of always-allowed tool names to one (session, agent)
// pair. Both fields participate in the key so a grant recorded for one agent
// never applies to a different agent running in the same session, and a
// grant recorded in one session never applies to another session.
type grantKey struct {
	sessionID string
	agentID   string
}

// ApprovalGrantStore is a thread-safe, session-scoped store of "Always
// Allow" tool-approval grants. The zero value is not usable — construct with
// NewApprovalGrantStore. Every method is nil-receiver-safe: calling a method
// on a nil *ApprovalGrantStore never panics and always resolves to the
// fail-safe outcome (IsAllowed => false, i.e. "ask"; Record/Inherit/
// ClearSession => no-op). This lets callers hold a possibly-unwired store
// (e.g. in a test fixture) without an extra nil check at every call site.
type ApprovalGrantStore struct {
	mu     sync.Mutex
	grants map[grantKey]map[string]struct{}
}

// NewApprovalGrantStore creates an empty grant store.
func NewApprovalGrantStore() *ApprovalGrantStore {
	return &ApprovalGrantStore{
		grants: make(map[grantKey]map[string]struct{}),
	}
}

// IsAllowed reports whether (sessionID, agentID) has previously been granted
// "Always Allow" for tool.
//
// Fail-safe (consent boundary — SEC audit): a nil store, or an empty
// sessionID / agentID / tool, ALWAYS returns false. This is deliberate and
// load-bearing: an empty string must never be treated as a valid scoping key,
// or two unrelated callers that both happen to have an empty session_id (or
// empty agent_id) would silently share the same grant. Callers MUST keep
// prompting ("ask") whenever this returns false.
func (s *ApprovalGrantStore) IsAllowed(sessionID, agentID, tool string) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tools, ok := s.grants[grantKey{sessionID: sessionID, agentID: agentID}]
	if !ok {
		return false
	}
	_, granted := tools[tool]
	return granted
}

// Record grants "Always Allow" for tool, scoped to (sessionID, agentID).
//
// Returns true when the grant was actually recorded, false when this call was
// a no-op — a nil store or an empty sessionID / agentID / tool provides no
// safe key to record the grant under, and recording it under an empty-string
// key would risk exactly the cross-caller collision IsAllowed's fail-safe
// check exists to prevent. Callers that report success back to a human (e.g.
// the "always" tool-approval action) MUST check this return value rather than
// assuming the grant took effect — see rest_tool_registry.go's
// HandleToolApprovals, which logs a Warn instead of Info when Record no-ops so
// the operator knows the tool will keep prompting on the next matching call.
func (s *ApprovalGrantStore) Record(sessionID, agentID, tool string) bool {
	if s == nil || sessionID == "" || agentID == "" || tool == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := grantKey{sessionID: sessionID, agentID: agentID}
	set, ok := s.grants[key]
	if !ok {
		set = make(map[string]struct{})
		s.grants[key] = set
	}
	set[tool] = struct{}{}
	return true
}

// Inherit copies the parent agent's CURRENT grant set (for sessionID) into
// the child agent's grant set — a union, not a replace, so any grant the
// child already holds in its own right is preserved. This is copy-at-spawn
// semantics: it snapshots the parent's grants at the moment of the call.
// Grants the parent records AFTER a child has already been spawned are NOT
// retroactively visible to that already-running child — this matches
// "copy-at-spawn" as specified for delegation inheritance (spawn /
// run_subagent), not a live/shared reference.
//
// No-op on a nil store, an empty sessionID / parentAgentID / childAgentID,
// or when the parent currently holds no grants for this session.
func (s *ApprovalGrantStore) Inherit(sessionID, parentAgentID, childAgentID string) {
	if s == nil || sessionID == "" || parentAgentID == "" || childAgentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	parentSet, ok := s.grants[grantKey{sessionID: sessionID, agentID: parentAgentID}]
	if !ok || len(parentSet) == 0 {
		return
	}
	childKey := grantKey{sessionID: sessionID, agentID: childAgentID}
	childSet, ok := s.grants[childKey]
	if !ok {
		childSet = make(map[string]struct{}, len(parentSet))
		s.grants[childKey] = childSet
	}
	for tool := range parentSet {
		childSet[tool] = struct{}{}
	}
}

// ClearSession removes every grant recorded for sessionID, across all
// agents. Called when a session ends (AgentLoop.CloseSession) so the store
// does not grow without bound and a finished session's grants can never leak
// into an unrelated future session.
//
// No-op on a nil store or an empty sessionID.
func (s *ApprovalGrantStore) ClearSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.grants {
		if key.sessionID == sessionID {
			delete(s.grants, key)
		}
	}
}
