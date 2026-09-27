// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_hub_binding.go: which session hub a browser connection is bound to
// (#823 BE-DESIGN.md §2 lock order: WSHandler.mu → hubRegistry.mu →
// sessionHub.mu → wsConn.qmu).
//
// A connection receives a session's sequenced frames only while it is in
// that session's hub.conns set, and it is in at most one hub at a time:
// moving to another session unbinds it from the old hub first. The binding
// is recorded on wsConn.boundHub under WSHandler.mu so teardown and the
// "is this tab on that session?" checks read one consistent answer.
package gateway

// attachCursor is the client's position for a session, from
// attach_session{since_seq, boot_id}. A nil *attachCursor (or nil fields)
// means "no position": the attach is answered with a snapshot.
type attachCursor struct {
	SinceSeq *int64
	BootID   *string
}

// bindConnToSessionHubLocked binds wc for live delivery of sessionID's
// frames with no catch-up — for a connection that just minted the session,
// or sent a message on it without attaching first — moving it off any hub
// it was bound to before. It returns the hub's head at bind time (the
// connection's cursor: every later publish reaches it). Caller holds h.mu.
func (h *WSHandler) bindConnToSessionHubLocked(wc *wsConn, sessionID string) uint64 {
	if h.hubs == nil || wc == nil || sessionID == "" {
		return 0
	}
	for {
		hub := h.hubs.getOrCreate(sessionID)
		if wc.boundHub != nil && wc.boundHub != hub {
			wc.boundHub.unbind(wc)
			wc.boundHub = nil
		}
		if head, ok := hub.bindLive(wc); ok {
			wc.boundHub = hub
			return head
		}
		// The hub was evicted between the lookup and the bind; the next
		// getOrCreate returns (or creates) the live one.
	}
}

// attachConnToSessionHubLocked is the attach path's bind (BE-DESIGN.md §4.1
// A2-A4): move wc off its previous hub, then bind it to sessionID's hub in
// hold mode, deciding incremental vs snapshot and reading the tail or the
// projection in the same critical section. Caller holds h.mu.
func (h *WSHandler) attachConnToSessionHubLocked(wc *wsConn, sessionID string, cursor *attachCursor) attachResult {
	var since *int64
	var boot *string
	if cursor != nil {
		since, boot = cursor.SinceSeq, cursor.BootID
	}
	for {
		hub := h.hubs.getOrCreate(sessionID)
		if wc.boundHub != nil && wc.boundHub != hub {
			wc.boundHub.unbind(wc)
			wc.boundHub = nil
		}
		res := hub.bind(wc, since, boot, h.hubs.bootID)
		if !res.evicted {
			wc.boundHub = hub
			return res
		}
	}
}

// unbindConnHubLocked removes wc from whatever hub it is bound to (teardown).
// Caller holds h.mu.
func (h *WSHandler) unbindConnHubLocked(wc *wsConn) {
	if wc == nil || wc.boundHub == nil {
		return
	}
	wc.boundHub.unbind(wc)
	wc.boundHub = nil
}

// connBoundToSession reports whether wc currently receives sessionID's
// sequenced frames.
func (h *WSHandler) connBoundToSession(wc *wsConn, sessionID string) bool {
	if wc == nil || sessionID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return wc.boundHub != nil && wc.boundHub.id == sessionID
}
