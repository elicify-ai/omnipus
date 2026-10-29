// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// chat_presence.go — which chats a user is watching right now (chat-links
// spec, "Watching"; D-watch, D-presence). A browser tab sends chat_presence
// with watching=true when it shows a chat while visible, re-sends it every 15
// seconds, and sends watching=false when it stops. The gateway keeps the
// claims in memory only, bound to the sending connection — modelled on the
// Mail panel presence adapter (mail_presence.go): teardown drops a closed
// connection's claims, and nothing is journaled, replayed or persisted.
//
// The answer feeds the agent loop's "User watching" context line and
// show_to_user's success text through AgentLoop.SetUserWatching. Unknown,
// expired and unauthorised claims all read "not watching" — never yes.

import (
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	// chatPresenceTTL is how long a claim counts without a refresh: three
	// missed 15-second refreshes (architect, D-presence).
	chatPresenceTTL = 45 * time.Second
	// chatPresenceSessionIDMax mirrors the contract's session_id bound, for
	// installs that run with inbound schema validation off.
	chatPresenceSessionIDMax = 255
)

// chatPresenceStore holds, per chat session, the connections currently
// claiming to watch it and when each claim was last refreshed.
type chatPresenceStore struct {
	mu     sync.Mutex
	now    func() time.Time
	claims map[string]map[*wsConn]time.Time // session id -> conn -> last refresh
	byConn map[*wsConn]map[string]struct{}  // conn -> session ids it claims
}

func newChatPresenceStore(now func() time.Time) *chatPresenceStore {
	if now == nil {
		now = time.Now
	}
	return &chatPresenceStore{
		now:    now,
		claims: make(map[string]map[*wsConn]time.Time),
		byConn: make(map[*wsConn]map[string]struct{}),
	}
}

// set upserts (watching=true) or deletes (watching=false) this connection's
// claim on sessionID. A nil store is a no-op.
func (s *chatPresenceStore) set(sessionID string, wc *wsConn, watching bool) {
	if s == nil || sessionID == "" || wc == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !watching {
		s.deleteLocked(sessionID, wc)
		return
	}
	conns := s.claims[sessionID]
	if conns == nil {
		conns = make(map[*wsConn]time.Time)
		s.claims[sessionID] = conns
	}
	conns[wc] = s.now()
	sessions := s.byConn[wc]
	if sessions == nil {
		sessions = make(map[string]struct{})
		s.byConn[wc] = sessions
	}
	sessions[sessionID] = struct{}{}
}

// dropConn deletes every claim the connection holds (teardown).
func (s *chatPresenceStore) dropConn(wc *wsConn) {
	if s == nil || wc == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for sessionID := range s.byConn[wc] {
		s.deleteLocked(sessionID, wc)
	}
	delete(s.byConn, wc)
}

func (s *chatPresenceStore) deleteLocked(sessionID string, wc *wsConn) {
	if conns := s.claims[sessionID]; conns != nil {
		delete(conns, wc)
		if len(conns) == 0 {
			delete(s.claims, sessionID)
		}
	}
	if sessions := s.byConn[wc]; sessions != nil {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(s.byConn, wc)
		}
	}
}

// watching reports whether at least one unexpired claim exists for
// sessionID. Expired claims are pruned on the way.
func (s *chatPresenceStore) watching(sessionID string) bool {
	if s == nil || sessionID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-chatPresenceTTL)
	found := false
	for wc, at := range s.claims[sessionID] {
		if at.After(cutoff) {
			found = true
			continue
		}
		s.deleteLocked(sessionID, wc)
	}
	return found
}

// handleChatPresence serves one client chat_presence frame. A claim is
// accepted only for a session this connection may attach to (the
// attach_session checks); any other session id is ignored. watching=false
// always just deletes this connection's own claim.
func (h *WSHandler) handleChatPresence(wc *wsConn, f generated.ChatPresenceFrame) {
	if h.chatPresence == nil || wc == nil {
		return
	}
	sid := f.SessionId
	if sid == "" || len(sid) > chatPresenceSessionIDMax {
		wc.inboundDropped.Add(1)
		return
	}
	if !f.Watching {
		h.chatPresence.set(sid, wc, false)
		return
	}
	if !h.chatPresenceAllowed(sid) {
		wc.inboundDropped.Add(1)
		slog.Debug("ws: chat_presence for a session this connection cannot attach to — ignored")
		return
	}
	h.chatPresence.set(sid, wc, true)
}

// chatPresenceAllowed applies attach_session's session checks (valid id, a
// hidden main the pair is no longer entitled to is refused, the session
// exists) without sending any frame: presence refusals are silent.
func (h *WSHandler) chatPresenceAllowed(sessionID string) bool {
	if validateEntityID(sessionID) != nil || h.agentLoop == nil {
		return false
	}
	home := h.home
	if home == "" {
		home = config.OmnipusHomeDir()
	}
	isMain, eligible, err := mainSessionGate(home, h.agentLoop.GetConfig(), sessionID)
	if err != nil || (isMain && !eligible) {
		return false
	}
	return h.resolveSessionStore(sessionID) != nil
}

// userWatching is the lookup the agent loop reads (AgentLoop.SetUserWatching).
func (h *WSHandler) userWatching(sessionID string) bool {
	return h.chatPresence.watching(sessionID)
}
