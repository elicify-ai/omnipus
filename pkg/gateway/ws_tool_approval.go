// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// WebSocket events for the Central Tool Registry redesign (A3 lane).
//
// Emits three event types:
//
//  1. tool_approval_required (FR-011, FR-082)
//     Sent to the connected WS clients of the account whose agent paused on
//     an ask-policy tool call (UAT 2026-09-13 D-16; approvalVisibleTo).
//     Uses expires_in_ms (not expires_at) per OBS-004.
//
//  2. tool_approval_resolved
//     Sent to all connected WS clients when a pending approval leaves the
//     pending state for ANY reason (see approvalRegistryV2.resolutionListener).
//
//  3. session_state (FR-052, FR-073, FR-081)
//     One-shot per WS connection on every reconnect.
//     Single-user model: every connection sees every pending approval.
//
// Workspace scoping: the two approval-carrying frames stamp workspace_id (the
// requesting session's workspace) so the SPA can show an approval only while
// that workspace is active. Delivery itself stays unscoped — every connection
// still receives every frame — because the SPA's active workspace can change
// without a reconnect and a hidden approval must reappear when the user
// switches to its workspace.

package gateway

import (
	"encoding/json"
	"log/slog"
	"time"
	"unicode/utf16"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// approvalChainMaxHops bounds approvalSessionContext's walk up the
// ParentSessionID chain (#1263 R1: 16 hops, cycle guard). Real chains are a
// handful of levels deep; the bound only guarantees termination over corrupt
// or cyclic on-disk meta.
const approvalChainMaxHops = 16

// Wire maxLength limits of the approval carriers (contracts/asyncapi.yaml,
// ToolApprovalRequiredFrame / SessionStatePendingApproval), in characters. A
// longer value is omitted rather than emitted schema-invalid.
const (
	approvalWorkspaceIDMaxLen = 128
	approvalRootSessionMaxLen = 255
	approvalActingNameMaxLen  = 128
	approvalOriginLabelMaxLen = 200
	approvalRoomNameMaxLen    = 80
)

// approvalContext is what every approval carrier derives from the acting
// session (#1263 §1.3): the workspace the approval belongs to and the root
// chat of the session chain the card renders in.
type approvalContext struct {
	// WorkspaceID is the first workspace found walking up from the acting
	// session ("" = none, or unreadable — the SPA shows it everywhere).
	WorkspaceID string
	// RootSessionID is the session at the top of the ParentSessionID chain.
	// On a failed walk it is the acting session itself, so the card shows in
	// the acting chat (plus the banner), never nowhere.
	RootSessionID string
}

// approvalSessionContext walks the acting session's ParentSessionID chain once
// (the one derivation every approval carrier uses, amendment §1.3). A helper's
// approval therefore names its root chat — the chat where a person is — while
// session_id stays the acting helper (the Open-run target).
func (h *WSHandler) approvalSessionContext(sessionID string) approvalContext {
	out := approvalContext{RootSessionID: sessionID}
	if h == nil || h.agentLoop == nil || sessionID == "" {
		return out
	}
	// fallback keeps the card in the acting chat (never nowhere) and says so
	// loudly: it only happens on an unreadable or corrupt chain, never on a
	// normal path, and a helper's card then shows outside its root chat.
	fallback := func(hopID, reason string, err error) approvalContext {
		attrs := []any{"acting_session_id", sessionID, "failing_session_id", hopID, "reason", reason}
		if err != nil {
			attrs = append(attrs, "error", err)
		}
		slog.Error("ws: approval root chat could not be derived; the card is shown in the acting session's chat instead", attrs...)
		return approvalContext{WorkspaceID: out.WorkspaceID, RootSessionID: sessionID}
	}
	seen := make(map[string]struct{}, 4)
	sid := sessionID
	workspaceKnown := false
	for hop := 0; hop < approvalChainMaxHops && sid != ""; hop++ {
		if _, dup := seen[sid]; dup {
			return fallback(sid, "parent chain has a cycle", nil)
		}
		seen[sid] = struct{}{}
		store := h.resolveSessionStore(sid)
		if store == nil {
			return fallback(sid, "session not found in any store", nil)
		}
		meta, err := store.GetMeta(sid)
		if err != nil || meta == nil {
			return fallback(sid, "session meta unreadable", err)
		}
		if !workspaceKnown && meta.WorkspaceID != "" {
			workspaceKnown = true
			if len(meta.WorkspaceID) <= approvalWorkspaceIDMaxLen {
				out.WorkspaceID = meta.WorkspaceID
			}
		}
		if meta.ParentSessionID == "" {
			out.RootSessionID = sid
			return out
		}
		sid = meta.ParentSessionID
	}
	return fallback(sid, "parent chain longer than the hop bound", nil)
}

// fitsWireLength reports whether s fits a contract maxLength as BOTH ends of
// the wire count it. Never UTF-8 bytes: a valid 80-character non-ASCII room
// name makes a "for group" label of well over 200 bytes that the contract
// accepts (gate round 1, architect F3 / SF-06). And not code points either:
// the browser's generated validator (zod z.string().max) counts JavaScript
// String.length, UTF-16 code units, so a character outside the Basic
// Multilingual Plane counts twice there. A value that fits in code points but
// not in UTF-16 units made the browser reject the whole approval frame and the
// whole reload snapshot (gate round 2, security S4 / SF-07 / architect N1;
// founder decision 2026-10-20). UTF-16 units are never fewer than code points,
// so this one count satisfies both the contract and the browser.
func fitsWireLength(s string, limit int) bool {
	units := 0
	for _, r := range s {
		units += utf16.RuneLen(r)
		if units > limit {
			return false
		}
	}
	return true
}

// rootSessionField returns the wire root_session_id: omitted (nil) when the
// root is the acting session itself or too long for the schema (K7).
func (c approvalContext) rootSessionField(actingSessionID string) *string {
	if c.RootSessionID == "" || c.RootSessionID == actingSessionID || len(c.RootSessionID) > approvalRootSessionMaxLen {
		return nil
	}
	root := c.RootSessionID
	return &root
}

// workspaceField returns the wire workspace_id, omitted when unknown.
func (c approvalContext) workspaceField() *string {
	if c.WorkspaceID == "" {
		return nil
	}
	ws := c.WorkspaceID
	return &ws
}

// approvalActingAgentName returns the acting agent's display name for the card
// header (K7 acting_agent_name), nil when it cannot be resolved.
func (h *WSHandler) approvalActingAgentName(agentID string) *string {
	if h == nil || h.agentLoop == nil || agentID == "" {
		return nil
	}
	reg := h.agentLoop.GetRegistry()
	if reg == nil {
		return nil
	}
	name, ok := reg.GetAgentName(agentID)
	if !ok || name == "" || !fitsWireLength(name, approvalActingNameMaxLen) {
		return nil
	}
	return &name
}

// broadcastToolApprovalRequired sends a tool_approval_required WS frame to
// every connected WebSocket client of the account that owns the acting
// session (FR-073; see approvalVisibleTo for the audience rule and its
// fallbacks).
//
// Wire format: generated.ToolApprovalRequiredFrame (contract-first, pkg/api/generated).
// Nil-safety: args MUST be an object (never null). The SPA's ToolApprovalModal calls
// Object.keys(args) directly — null crashes with "null is not an object" (Ava-chat bug).
// When entry.Args is nil, we coerce to map[string]any{} at this site.
//
// The frame is best-effort: clients that are disconnected or have a full send buffer
// will miss the frame and must rely on the next session_state reset on reconnect.
func (h *WSHandler) broadcastToolApprovalRequired(entry *approvalEntry) {
	if entry == nil {
		return
	}
	// Nil-safety: coerce nil args to empty map so JSON serializes as {} not null.
	// The SPA's ToolApprovalModal calls Object.keys(args) — null would crash.
	// cloneStringAnyMap (pkg/agent/hooks.go) returns nil for empty input, so a tool
	// invoked without parameters lands here with entry.Args == nil.
	args := entry.Args
	if args == nil {
		args = map[string]any{}
	}

	ctx := h.approvalSessionContext(entry.SessionID)
	frame := generated.ToolApprovalRequiredFrame{
		Type:            string(generated.WsFrameTypeToolApprovalRequired),
		ApprovalId:      entry.ApprovalID,
		ToolCallId:      entry.ToolCallID,
		ToolName:        entry.ToolName,
		Args:            args,
		AgentId:         entry.AgentID,
		SessionId:       entry.SessionID,
		TurnId:          entry.TurnID,
		ExpiresInMs:     int(entry.expiresInMs()), // OBS-004: relative, not absolute
		WorkspaceId:     ctx.workspaceField(),
		RootSessionId:   ctx.rootSessionField(entry.SessionID),
		ActingAgentName: h.approvalActingAgentName(entry.AgentID),
		RoomRef:         approvalRoomRef(entry),
		OriginLabel:     approvalOriginLabel(entry),
	}
	frame.Segments = h.bashApprovalSegments(entry)
	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal tool_approval_required", "error", err)
		return
	}

	// MERGE 2026-09-15: integrate's D-82 reliable delivery is kept (each
	// connection gets a bounded wait for send-buffer space instead of an
	// immediate drop); its per-account audience filter is not — Omnipus is
	// single-account by founder ruling, so every connection is the audience.
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		conns = append(conns, wc)
	}
	h.mu.Unlock()
	if len(conns) == 0 {
		slog.Warn("ws: tool_approval_required has no connected audience — the request will wait for a reconnect (session_state) or time out",
			"approval_id", entry.ApprovalID, "tool", entry.ToolName)
	}
	for _, wc := range conns {
		sendApprovalFrame(wc, raw, entry.ApprovalID)
	}
}

// sendApprovalFrame delivers one approval frame to one connection through
// its ordered queue (#823 ws_conn_queue.go). D-82's guarantee — a human is
// always asked — now holds without a timeout: the queue never drops; a
// connection too far behind is closed with 4008, reconnects, and its
// session_state lists the pending approval. A connection already closed is
// logged, since the approval then waits for a reconnect.
func sendApprovalFrame(wc *wsConn, raw []byte, approvalID string) {
	if !wc.enqueue(raw) {
		slog.Warn("ws: tool_approval_required not queued — connection closed; it will be re-offered via session_state on reconnect",
			"approval_id", approvalID, "user_id", wc.userID)
	}
}

// broadcastToolApprovalResolved sends a tool_approval_resolved WS frame to
// every connected client. It is the registry's resolution listener
// (approvalRegistryV2.setResolutionListener, wired in gateway.go), so it runs
// once for every pending→terminal transition whatever caused it — a decision
// from any tab, the timeout, a Stop, the owning agent's deletion, a batch
// short-circuit, or shutdown.
//
// Why it exists: before it, only the tab whose POST resolved an approval
// learned of the resolution. Every other open tab — and every tab after a
// resolution the server made on its own — kept a dialog whose actions could
// only return 410, then 404 once the registry's terminal retention elapsed,
// with no way to dismiss it short of a full reload.
//
// Best-effort like every broadcast: a client with a full send buffer misses
// the frame. It still converges — its next session_state snapshot no longer
// lists the approval, and the SPA dismisses on a 404/410 action response.
func (h *WSHandler) broadcastToolApprovalResolved(entry *approvalEntry, state ApprovalState) {
	if entry == nil {
		return
	}
	frame := generated.ToolApprovalResolvedFrame{
		Type:       string(generated.WsFrameTypeToolApprovalResolved),
		ApprovalId: entry.ApprovalID,
		State:      string(state),
	}
	if entry.SessionID != "" {
		sid := entry.SessionID
		frame.SessionId = &sid
		// K7: the root chat's card clears without a lookup.
		frame.RootSessionId = h.approvalSessionContext(entry.SessionID).rootSessionField(entry.SessionID)
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal tool_approval_resolved", "error", err)
		return
	}
	h.broadcastRaw(raw, "ws: tool_approval_resolved dropped — send buffer full",
		"approval_id", entry.ApprovalID, "state", string(state))
}

// emitSessionState sends a session_state frame to a single WS connection —
// once, immediately after authentication (FR-052, FR-073, FR-081), and again
// by handleAttachSession once a session id is known (ADR-082 D4).
//
// Wire format: generated.SessionStateFrame (contract-first, pkg/api/generated).
// Nil-safety: pending_approvals MUST be an array (never null). The SPA calls
// pending_approvals.map() — null would crash at render time. Coerced to [] when empty.
//
// FR-073 scoping is moot under the single-user model: every connection sees
// every pending approval, for all sessions.
//
// Note: When approvalRegV2 is nil (pre-registry harness), the payload has an empty
// pending_approvals array — the SPA receives a valid frame and clears any stale UI.
//
// sessionID (ADR-082 D4/FR-008): when non-empty, populates ActiveTurn from
// AgentLoop.ActiveForegroundTurnInfo — present only while a foreground turn
// is in flight for that session, absent otherwise. Pass "" at raw
// connection-open (before any session is known); handleAttachSession's
// post-bind call passes the real attachID so a reconnecting SPA immediately
// knows whether to render the streaming bubble + Stop control.
func (h *WSHandler) emitSessionState(wc *wsConn, sessionID string) {
	if wc == nil {
		return
	}
	if raw := h.sessionStateBytes(wc, sessionID); raw != nil {
		sendRawFrameBytes(wc, string(generated.WsFrameTypeSessionState), raw)
	}
}

// sessionStateBytes builds the session_state frame emitSessionState sends
// (nil on a marshal failure, which is logged). The attach path queues these
// bytes itself, ahead of its held live frames (BE-DESIGN.md §4.1 A6).
func (h *WSHandler) sessionStateBytes(wc *wsConn, sessionID string, historyStates ...generated.SessionHistoryState) []byte {
	// Always initialize to non-nil slice so JSON encodes as [] not null.
	pendingApprovals := make([]generated.SessionStatePendingApproval, 0)

	if h.approvalRegV2 != nil {
		allPending := h.approvalRegV2.pendingApprovals()

		// D-16: the reconnect snapshot follows the same audience rule as the
		// live frame — a tab reconnecting under account B must not re-hydrate
		// account A's pending approvals as blocking stubs.
		for _, e := range allPending {
			pendingApprovals = append(pendingApprovals, h.pendingApprovalSnapshot(e))
		}
	}

	frame := generated.SessionStateFrame{
		Type:             string(generated.WsFrameTypeSessionState),
		UserId:           wc.userID,
		PendingApprovals: pendingApprovals,
		EmittedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	// #823 BE-DESIGN.md §3.4: every connection opens with a session_state,
	// so this is how a client learns the gateway's current boot id and can
	// tell that a stored cursor predates a restart.
	if h.hubs != nil {
		bootID := h.hubs.bootID
		frame.BootId = &bootID
	}

	// ADR-082 review CR3: stamp the session this snapshot describes so a
	// client juggling several attached sessions (or a re-attach mid-flight)
	// can tell which session_state a frame belongs to instead of guessing
	// from arrival order. Absent (nil) at the raw connection-open emit, where
	// sessionID is "" because no session has been bound yet.
	if sessionID != "" {
		sid := sessionID
		frame.SessionId = &sid
	}

	// ADR-082 D4/FR-008: announce the in-flight foreground turn (if any) for
	// the session this connection is bound to, so a reconnecting SPA
	// immediately knows to render the streaming bubble + Stop control
	// instead of a deceptively idle composer. Absent when sessionID is
	// unknown (the raw connection-open call, before any attach/message) or
	// the session has no foreground turn in flight.
	if sessionID != "" && h.agentLoop != nil {
		if turnID, agentID, startedAt, ok := h.agentLoop.ActiveForegroundTurnInfo(sessionID); ok {
			frame.ActiveTurn = &generated.SessionStateActiveTurn{
				TurnId:    turnID,
				AgentId:   agentID,
				StartedAt: startedAt.UTC().Format(time.RFC3339),
			}
		}
	}

	// ADR-092 (review finding D): carry this session's per-chat Auto-approve
	// modifier so a reloading SPA re-learns it instead of falling back to
	// the global default while the server still applies the chat's
	// own. Absent when no session is bound yet or no modifier is set — the
	// store is in memory, so after a gateway restart it is absent too and
	// the UI follows the server.
	if sessionID != "" && h.agentLoop != nil {
		if v, ok := h.agentLoop.SessionModes().Get(sessionID); ok {
			frame.AutoApproveModifier = &v
		}
	}

	// askuserquestion-tool-spec v3 US-6 S1/FR-9: snapshot every PENDING
	// AskUserQuestion card so a reconnecting SPA re-hydrates its card +
	// composer lock (the boot rearm sweep in gateway.go re-populates the
	// registry from session meta after a restart). Optional field — absent
	// when no registry is wired or nothing is pending.
	if h.askUserReg != nil {
		if pendingSets := h.askUserReg.PendingAll(); len(pendingSets) > 0 {
			delay := h.askUserReg.EffectiveDefaultSafeDelay()
			for _, set := range pendingSets {
				frame.PendingAsks = append(frame.PendingAsks, toAskUserCard(set, delay))
			}
		}
	}

	// U11 (FR-047): a main attach carries the saved attention order the client
	// may later echo back to acknowledge. 0 when the main has no outcome yet;
	// omitted for a non-main, the connection-open emit, and an unreadable mark.
	frame.AttentionBound = h.attentionBoundFor(sessionID)
	if sessionID != "" {
		if len(historyStates) > 0 {
			state := historyStates[0]
			frame.HistoryState = &state
		} else if state, ok := wc.rememberedHistoryState(sessionID); ok {
			frame.HistoryState = &state
		}
	}

	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal session_state", "error", err)
		return nil
	}
	slog.Debug("ws: session_state built", "user_id", wc.userID, "pending", len(pendingApprovals))
	return raw
}

// pendingApprovalSnapshot is one reload-snapshot entry (bundle B3, amendment
// §1.3): the complete request, equal field for field to the live
// tool_approval_required frame, so a reloaded card is complete and
// approvable — never a Deny-only stub. tool_call_id, turn_id and args are
// required by the contract; the registry never admits an entry without a
// turn id (approvals.go::requestApproval, D-03).
func (h *WSHandler) pendingApprovalSnapshot(e *approvalEntry) generated.SessionStatePendingApproval {
	args := e.Args
	if args == nil {
		args = map[string]any{}
	}
	ctx := h.approvalSessionContext(e.SessionID)
	return generated.SessionStatePendingApproval{
		ApprovalId:      e.ApprovalID,
		SessionId:       e.SessionID,
		ToolName:        e.ToolName,
		AgentId:         e.AgentID,
		ExpiresInMs:     int(e.expiresInMs()),
		ToolCallId:      e.ToolCallID,
		TurnId:          e.TurnID,
		Args:            args,
		Segments:        h.bashApprovalSegments(e),
		WorkspaceId:     ctx.workspaceField(),
		RootSessionId:   ctx.rootSessionField(e.SessionID),
		ActingAgentName: h.approvalActingAgentName(e.AgentID),
		RoomRef:         approvalRoomRef(e),
		OriginLabel:     approvalOriginLabel(e),
	}
}

// approvalRoomRef projects a group chat's pre-admission card room reference
// (FR-251, K7); absent on every other approval.
//
// A room_ref whose room name does not fit the browser's 80-unit limit is
// omitted, never sent: the browser would otherwise reject the whole frame or
// reload snapshot (gate round 3, security O1). Room names are validated in
// UTF-16 units at creation (pkg/rooms/admin.go::roomName); this is the
// producer's safety net, the same rule as the S4 fields.
func approvalRoomRef(e *approvalEntry) *generated.RoomRef {
	if e == nil || e.RoomRef == nil || !fitsWireLength(e.RoomRef.RoomName, approvalRoomNameMaxLen) {
		return nil
	}
	ref := *e.RoomRef
	return &ref
}

// approvalOriginLabel projects the server-derived turn origin label (spec
// §6.18, K7): "for group <name>" on a room's pre-admission card, "asked by
// <Agent>" for a turn that consumed a peer request. Absent for a plain web
// turn, or when longer than the schema allows.
func approvalOriginLabel(e *approvalEntry) *string {
	if e == nil || e.OriginLabel == "" || !fitsWireLength(e.OriginLabel, approvalOriginLabelMaxLen) {
		return nil
	}
	l := e.OriginLabel
	return &l
}
