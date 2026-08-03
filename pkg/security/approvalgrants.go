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

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

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
// fail-safe outcome (IsAllowed => false, i.e. "ask"; Record/InheritFrom/
// ClearSession => no-op). This lets callers hold a possibly-unwired store
// (e.g. in a test fixture) without an extra nil check at every call site.
type ApprovalGrantStore struct {
	mu     sync.Mutex
	grants map[grantKey]map[string]struct{}

	// inheritSourceMiss counts InheritFrom calls whose four key components
	// were all non-empty but whose SOURCE key held no grants, so nothing was
	// copied (ADR-057 FR-079). Read via InheritSourceMissCount.
	//
	// This counter — not the log level — is the FR-079 tripwire. A "no grants
	// under the source key" outcome is ROUTINE (most agents never record an
	// "Always Allow"), so logging it at Warn on every delegation would train
	// operators to ignore Warns; the log record is therefore Debug and the
	// counter is always on and assertable. Its purpose is to make ADR-057
	// grill finding C-1 impossible to reproduce silently: a re-key that makes
	// the source lookup miss when it should have hit shows up here as a
	// climbing count with zero inherited grants, instead of as a delegation
	// that hangs for 300 s with no signal anywhere.
	inheritSourceMiss atomic.Int64

	// inheritInvalidKey counts InheritFrom calls rejected before any lookup
	// because at least one of the four key components was empty (ADR-057
	// FR-079, dataset row 6). Read via InheritInvalidKeyCount.
	//
	// Unlike a source miss this is never routine: at spawn time every one of
	// the four components is a resolved id, so an empty one means the CALLER
	// is broken and the child will silently fall through to a fresh approval
	// prompt. It is therefore logged at Warn as well as counted.
	inheritInvalidKey atomic.Int64
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

// InheritFrom copies the grant set currently recorded under the SOURCE key
// {srcSessionID, srcAgentID} into the DESTINATION key {dstSessionID,
// dstAgentID} — a union, not a replace, so any grant the destination already
// holds in its own right is preserved, and a copy, not a move, so the source
// still resolves afterwards.
//
// This is copy-at-spawn semantics: it snapshots the source's grants at the
// moment of the call. Grants recorded on the source AFTER a child has already
// been spawned are NOT retroactively visible to that already-running child —
// this matches "copy-at-spawn" as specified for delegation inheritance
// (spawn / run_subagent), not a live/shared reference.
//
// # Why the operation takes TWO keys (ADR-057 FR-031, grill finding C-1)
//
// The predecessor of this method, Inherit(sessionID, parentAgentID,
// childAgentID), used ONE session id for both the source lookup and the
// destination write. That is correct only while parent and child share a
// session id. ADR-057 gives every delegated child its OWN store-backed
// session, so the parent's grants live under the parent's session id while
// the child reads under its own — and "re-key Inherit's first argument to the
// child" (the obvious one-line fix) makes the SOURCE lookup miss, returns
// having done nothing, and reports success. The child then blocks on a fresh
// approval prompt nobody is watching until the 300 s approval timeout fires.
//
// Source and destination are therefore separate parameters and MUST be passed
// separately: at spawn, the SOURCE is the parent's session id + the parent's
// agent id, and the DESTINATION is the child's OWN session id + the child's
// agent id. Self-delegation is the same-agent, different-session case
// (srcAgentID == dstAgentID, srcSessionID != dstSessionID) and is handled by
// the same union.
//
// Both no-op branches are counted and logged rather than returning silently
// (FR-079) — see inheritSourceMiss / inheritInvalidKey for why each has the
// log level it has:
//
//   - a nil store: no state exists to count into, so it returns immediately;
//   - any empty key component: counted in inheritInvalidKey, logged at Warn
//     (a caller defect — every component is a resolved id at spawn);
//   - a source key holding no grants: counted in inheritSourceMiss, logged at
//     Debug (routine — most agents never record an "Always Allow").
//
// An identity call (source key == destination key) copies nothing because the
// destination already holds exactly the source's set; it is neither an error
// nor a miss and is not counted.
func (s *ApprovalGrantStore) InheritFrom(srcSessionID, srcAgentID, dstSessionID, dstAgentID string) {
	if s == nil {
		return
	}
	if srcSessionID == "" || srcAgentID == "" || dstSessionID == "" || dstAgentID == "" {
		total := s.inheritInvalidKey.Add(1)
		slog.Warn("approvalgrants: InheritFrom skipped — empty key component, no grants inherited",
			"src_session_id", srcSessionID,
			"src_agent_id", srcAgentID,
			"dst_session_id", dstSessionID,
			"dst_agent_id", dstAgentID,
			"invalid_key_total", total)
		return
	}

	srcKey := grantKey{sessionID: srcSessionID, agentID: srcAgentID}
	dstKey := grantKey{sessionID: dstSessionID, agentID: dstAgentID}

	s.mu.Lock()
	srcSet := s.grants[srcKey]
	if len(srcSet) == 0 {
		s.mu.Unlock()
		total := s.inheritSourceMiss.Add(1)
		slog.Debug("approvalgrants: InheritFrom found no grants to inherit under the source key",
			"src_session_id", srcSessionID,
			"src_agent_id", srcAgentID,
			"dst_session_id", dstSessionID,
			"dst_agent_id", dstAgentID,
			"source_miss_total", total)
		return
	}
	// Identity: the destination set IS the source set. Ranging a map while
	// writing the keys it already contains is safe, but short-circuiting says
	// so explicitly instead of relying on that subtlety.
	if srcKey != dstKey {
		dstSet := s.grants[dstKey]
		if dstSet == nil {
			dstSet = make(map[string]struct{}, len(srcSet))
			s.grants[dstKey] = dstSet
		}
		for tool := range srcSet {
			dstSet[tool] = struct{}{}
		}
	}
	s.mu.Unlock()
}

// InheritSourceMissCount returns the number of InheritFrom calls that resolved
// a well-formed source key holding no grants (ADR-057 FR-079). Nil-safe.
//
// This is real store state, not instrumentation: a test asserts on it directly
// to prove the empty-source branch is observable rather than silent.
func (s *ApprovalGrantStore) InheritSourceMissCount() int64 {
	if s == nil {
		return 0
	}
	return s.inheritSourceMiss.Load()
}

// InheritInvalidKeyCount returns the number of InheritFrom calls rejected
// because at least one of the four key components was empty (ADR-057 FR-079,
// dataset row 6). Nil-safe.
func (s *ApprovalGrantStore) InheritInvalidKeyCount() int64 {
	if s == nil {
		return 0
	}
	return s.inheritInvalidKey.Load()
}

// Inherit is the retired single-key form, kept ONLY so the tree compiles
// between this change (ADR-057 Wave A / unit U17a, which publishes the
// InheritFrom signature) and unit U7 (Wave F), which owns the sole non-test
// call site at pkg/agent/subturn.go:916 and re-points it at InheritFrom with
// the child's own session id as the destination.
//
// This mirrors the shim precedent the ADR-057 spec sets for a cross-wave
// removal whose intermediate tree would otherwise not compile (hard ordering 6
// — IsDelegateChildEntry). It is NOT a supported API and MUST NOT acquire a
// second caller: FR-031 requires the single-key form to be removed outright,
// and U7's commit removes this shim together with its call site.
//
// Behaviour is byte-for-byte today's: source and destination share sessionID,
// which is exactly the pre-ADR-057 same-session case, so re-pointing the call
// site is U7's behaviour change and not this one's.
//
// Deprecated: use InheritFrom, which takes a separate source and destination
// session id. Removed by ADR-057 unit U7.
func (s *ApprovalGrantStore) Inherit(sessionID, parentAgentID, childAgentID string) {
	s.InheritFrom(sessionID, parentAgentID, sessionID, childAgentID)
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
