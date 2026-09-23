// sessionmode.go: ADR-092 Auto-approve — resolving whether Auto-approve is
// on for one (agent, chat), and the session-scoped per-chat modifier store.
//
// The founder revision of ADR-092 (2026-09-23, recorded in the contracts —
// SandboxConfig.auto_approve, Agent.auto_approve_disabled,
// SessionModeUpdateFrame) replaced the "three shell modes as a presentation of
// the bash tool policy" framing with a separate Auto-approve setting at three
// scopes:
//
//   - global default: cfg.Sandbox.AutoApprove (re-auth-gated sandbox-config PUT)
//   - per agent: AgentConfig.AutoApproveDisabled — can only turn Auto OFF
//   - per chat: SessionModeStore — may turn Auto ON or OFF for that one chat,
//     because a human is present in it (the one scope allowed to loosen)
//
// Auto only has meaning for a tool resolved to "ask". The tool policy keeps
// its ordinary allow/deny/ask value; this file never reads or writes it.
package agent

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ResolveAutoApprove reports whether Auto-approve is on for agentID in a chat
// whose per-chat modifier is chat (nil = no modifier set).
//
// Resolution order: the global default, then the agent's off-switch, then the
// chat modifier. The chat modifier wins outright, in either direction, when
// set — SessionModeUpdateFrame documents it as the one scope that may loosen
// (turn Auto on for this chat even when the agent or global default has it
// off). The agent scope can only turn Auto off. A nil cfg resolves to off,
// the safe direction: off means every "ask" call prompts.
func ResolveAutoApprove(cfg *config.Config, agentID string, chat *bool) bool {
	if chat != nil {
		return *chat
	}
	if cfg == nil || !cfg.Sandbox.AutoApprove {
		return false
	}
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			return !cfg.Agents.List[i].AutoApproveDisabled
		}
	}
	return true
}

// SessionModeStore is a thread-safe, session-scoped store of the per-chat
// Auto-approve modifier set by the session_mode_update WS frame. It holds at
// most one value per session id. Structurally mirrors
// security.ApprovalGrantStore (session-keyed, nil-receiver-safe, dies with
// the session) but is not a tool-policy layer: there is no chat_id key on any
// policy map (Hard Constraint #6).
//
// The zero value is not usable — construct with NewSessionModeStore. Every
// method is nil-receiver-safe: a nil store never panics; Get misses and the
// writers are no-ops.
type SessionModeStore struct {
	mu        sync.Mutex
	modifiers map[string]bool // sessionID -> Auto-approve on/off for that chat
}

// NewSessionModeStore creates an empty session-mode store.
func NewSessionModeStore() *SessionModeStore {
	return &SessionModeStore{modifiers: make(map[string]bool)}
}

// Get returns sessionID's per-chat modifier, or (false, false) when none is
// set — the chat then follows the agent and global defaults. An empty
// sessionID always misses.
func (s *SessionModeStore) Get(sessionID string) (autoApprove, ok bool) {
	if s == nil || sessionID == "" {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.modifiers[sessionID]
	return v, ok
}

// Set records sessionID's per-chat modifier, replacing any earlier value.
// Returns false (no-op) for a nil store or an empty sessionID, so two callers
// with an empty id can never share a bucket.
func (s *SessionModeStore) Set(sessionID string, autoApprove bool) bool {
	if s == nil || sessionID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modifiers[sessionID] = autoApprove
	return true
}

// InheritFrom copies the parent's per-chat modifier from srcSessionID onto a
// delegate's dstSessionID at spawn time — the same copy-at-spawn semantics as
// security.ApprovalGrantStore.InheritFrom. A later change on the parent is not
// visible to an already-spawned delegate.
//
// When the destination already holds a value, off wins: a delegate never ends
// up with Auto on because of inheritance if it already had Auto off. No-op
// when the source has no modifier, or either id is empty.
func (s *SessionModeStore) InheritFrom(srcSessionID, dstSessionID string) {
	if s == nil || srcSessionID == "" || dstSessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.modifiers[srcSessionID]
	if !ok {
		return
	}
	if dst, ok := s.modifiers[dstSessionID]; ok {
		s.modifiers[dstSessionID] = dst && src
		return
	}
	s.modifiers[dstSessionID] = src
}

// ClearSession removes sessionID's modifier; the per-chat setting ends with
// the chat. No-op on a nil store or an empty sessionID.
func (s *SessionModeStore) ClearSession(sessionID string) {
	if s == nil || sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.modifiers, sessionID)
}
