// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rooms_runtime.go: the gateway's wiring of group-chat rooms (pkg/rooms,
// omnipus-system-agent US-17). It owns the room service's facts and side
// effects (workspace membership, the agent loop's room admission and answer
// mirror, the request ledger, audit, live delivery to the connections that
// attached a room) and adapts the service to the agent loop's RoomRuntime.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// roomsHub is the live room service plus the room subscriptions of the open
// WebSocket connections.
type roomsHub struct {
	svc  *rooms.Service
	mu   sync.Mutex
	subs map[string]map[*wsConn]struct{} // room id -> attached connections
}

// newRoomsHub builds the room service for one gateway and installs it into
// the agent loop.
func newRoomsHub(h *WSHandler, home string) *roomsHub {
	hub := &roomsHub{subs: map[string]map[*wsConn]struct{}{}}
	hub.svc = rooms.New(rooms.NewFileJournal(rooms.DefaultDir(home)), rooms.DepsFrom(&gatewayRoomDeps{h: h, home: home, hub: hub}))
	// #1449 TD-3: answer files are staged in the room service's own data,
	// never in an agent-reachable tree.
	hub.svc.SetStagingDir(rooms.DefaultStagingDir(home))
	if h.agentLoop != nil {
		h.agentLoop.SetRoomRuntime(agentRoomRuntime{svc: hub.svc})
	}
	return hub
}

func (hub *roomsHub) subscribe(roomID string, wc *wsConn) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.subs[roomID] == nil {
		hub.subs[roomID] = map[*wsConn]struct{}{}
	}
	hub.subs[roomID][wc] = struct{}{}
}

// unsubscribeAll drops a closing connection from every room.
func (hub *roomsHub) unsubscribeAll(wc *wsConn) {
	if hub == nil || wc == nil {
		return
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for id, conns := range hub.subs {
		delete(conns, wc)
		if len(conns) == 0 {
			delete(hub.subs, id)
		}
	}
}

// publish sends a room frame to every attached connection (each connection's
// queue keeps frame order; the service publishes under the room lock).
func (hub *roomsHub) publish(roomID string, frame any) {
	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("rooms: marshal room frame failed", "room_id", roomID, "error", err)
		return
	}
	hub.mu.Lock()
	conns := make([]*wsConn, 0, len(hub.subs[roomID]))
	for wc := range hub.subs[roomID] {
		conns = append(conns, wc)
	}
	hub.mu.Unlock()
	for _, wc := range conns {
		if !wc.enqueue(raw) {
			// A closed connection re-attaches (K45) and catches up from its cursor.
			slog.Debug("rooms: room frame not queued; the connection is closed", "room_id", roomID)
		}
	}
}

// gatewayRoomDeps implements rooms.Deps over the gateway.
type gatewayRoomDeps struct {
	h    *WSHandler
	home string
	hub  *roomsHub
}

var (
	_ rooms.Directory = (*gatewayRoomDeps)(nil)
	_ rooms.Receivers = (*gatewayRoomDeps)(nil)
	_ rooms.Effects   = (*gatewayRoomDeps)(nil)
)

// LookupWorkspace lists the workspace's conversation agents: team members that are
// chat targets, never workers, system agents or Omnipus (FR-160, FR-162).
func (d *gatewayRoomDeps) LookupWorkspace(workspaceID string) (rooms.WorkspaceInfo, bool, error) {
	ws, found, err := loadWorkspaceForMain(d.home, workspaceID)
	if err != nil || !found {
		return rooms.WorkspaceInfo{}, found, err
	}
	cfg := d.h.agentLoop.GetConfig()
	out := rooms.WorkspaceInfo{ID: ws.ID, Name: ws.Name}
	for _, id := range ws.CoreTeam {
		if id == string(coreagent.IDOmnipus) || !mainAgentEligible(cfg, ws, id) {
			continue
		}
		out.Agents = append(out.Agents, rooms.Agent{ID: id, Name: d.agentName(id)})
	}
	return out, true, nil
}

func (d *gatewayRoomDeps) agentName(id string) string {
	if name, ok := d.AgentName(id); ok {
		return name
	}
	return id
}

// AgentName returns an agent's display name from the live config.
func (d *gatewayRoomDeps) AgentName(agentID string) (string, bool) {
	ac := findAgentConfig(d.h.agentLoop.GetConfig(), agentID)
	if ac == nil {
		return "", false
	}
	if strings.TrimSpace(ac.Name) != "" {
		return ac.Name, true
	}
	return ac.ID, true
}

// Admit runs the agent loop's room admission and maps its refusals.
func (d *gatewayRoomDeps) Admit(ctx context.Context, a rooms.Admission) (rooms.AdmitResult, error) {
	res, err := d.h.agentLoop.AdmitRoomRequest(ctx, agent.RoomRequestAdmission{
		Receiver: a.Receiver, DeliveryID: a.DeliveryID, RoomID: a.RoomID, RoomName: a.RoomName,
		Sender: a.Sender, SenderLabel: a.SenderLabel, DisplayText: a.DisplayText, ModelText: a.ModelText,
		Media: a.Media, Attachments: a.Attachments,
		OnAwaitingApproval: a.OnAwaitingApproval, BeforeCommit: a.BeforeCommit,
	})
	if err != nil {
		switch {
		case errors.Is(err, rooms.ErrAdmitCancelled), errors.Is(err, agent.ErrRoomRequestCancelled):
			return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitCancelled, err)
		case errors.Is(err, agent.ErrRoomRequestRefused):
			return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitRefused, err)
		case errors.Is(err, agent.ErrRoomReceiverUnavailable):
			return rooms.AdmitResult{}, errors.Join(rooms.ErrAdmitUnavailable, err)
		}
		return rooms.AdmitResult{}, err
	}
	return rooms.AdmitResult{ReceiverSessionID: res.ReceiverSessionID, Held: res.Held, AlreadyAdmitted: res.AlreadyAdmitted}, nil
}

// Facts reads the member's request ledger for a delivery id.
func (d *gatewayRoomDeps) Facts(receiver addressing.Pair, requestID string) (rooms.ReceiverFacts, error) {
	ledger := d.h.agentLoop.RequestLedger()
	if ledger == nil {
		return rooms.ReceiverFacts{}, errNoSessionStore
	}
	mainID, err := session.MainSessionID(receiver.WorkspaceID, receiver.AgentID)
	if err != nil {
		return rooms.ReceiverFacts{}, err
	}
	c, found, err := ledger.Get(mainID, requestID)
	if err != nil || !found {
		return rooms.ReceiverFacts{SessionID: mainID}, err
	}
	return rooms.ReceiverFacts{
		Found: true, Delivered: c.Delivered, Discarded: c.Discarded, Held: c.Held, SessionID: c.ReceiverSessionID,
	}, nil
}

// Discard discards a request from the member's ledger.
func (d *gatewayRoomDeps) Discard(sessionID, requestID string) error {
	return d.h.agentLoop.DiscardRequest(sessionID, requestID)
}

// Mirror writes an answer once into the responder's main chat.
func (d *gatewayRoomDeps) Mirror(m rooms.Mirror) error {
	return d.h.agentLoop.MirrorRoomAnswerWithFiles(m.MainSessionID, m.MirrorID, m.Author, m.Ref, m.Content, m.Attachments)
}

// Publish delivers a room frame to the attached connections.
func (d *gatewayRoomDeps) Publish(roomID string, frame any) { d.hub.publish(roomID, frame) }

// Audit records a room change (FR-172).
func (d *gatewayRoomDeps) Audit(e rooms.AuditEvent) {
	logger := d.h.agentLoop.AuditLogger()
	if logger == nil {
		slog.Warn("rooms: audit logger unavailable; room change not audited", "room_id", e.RoomID, "action", e.Action)
		return
	}
	audit.EmitEntry(logger, &audit.Entry{
		Event: audit.EventRoomChanged, Decision: audit.DecisionAllow, User: e.Principal,
		Details: map[string]any{"room_id": e.RoomID, "workspace_id": e.WorkspaceID, "action": e.Action, "agent_ids": e.AgentIDs},
	})
}

// SessionInWorkspace reports whether sessionID is the main chat of a
// conversation agent of workspaceID.
func (d *gatewayRoomDeps) SessionInWorkspace(sessionID, workspaceID string) bool {
	wsID, agentID, ok := session.SplitMainSessionID(sessionID)
	if !ok || wsID != workspaceID {
		return false
	}
	ws, found, err := d.LookupWorkspace(workspaceID)
	if err != nil || !found {
		return false
	}
	for _, a := range ws.Agents {
		if a.ID == agentID {
			return true
		}
	}
	return false
}

// CancelApproval withdraws a room's pending pre-admission card (request id =
// delivery id, FR-251) at once.
func (d *gatewayRoomDeps) CancelApproval(requestID string) {
	reg := d.h.approvalRegV2
	if reg == nil || requestID == "" {
		return
	}
	reg.cancelPendingMatching(func(e *approvalEntry) bool {
		return e.RoomPreAdmission && e.RoomRef != nil && e.RoomRef.RequestId == requestID
	}, "cancel")
}

// Limits returns the session messaging limits (FR-176).
func (d *gatewayRoomDeps) Limits() (int, int) {
	cfg := d.h.agentLoop.GetConfig()
	if cfg == nil {
		return 0, 0
	}
	return cfg.SessionMessaging.EffectiveSteerRatePerMinute(), cfg.SessionMessaging.EffectiveSteerBodyBytes()
}

// agentRoomRuntime adapts the room service to agent.RoomRuntime.
type agentRoomRuntime struct{ svc *rooms.Service }

var _ agent.RoomRuntime = agentRoomRuntime{}

var _ agent.RoomFence = agentRoomRuntime{}

func (rt agentRoomRuntime) BeginRoomConsume(roomID, requestID, sessionID string) bool {
	return rt.svc.BeginConsume(roomID, requestID, sessionID)
}

func (rt agentRoomRuntime) DecideRoomConsume(roomID, requestID, sessionID string) agent.RoomConsumeDecision {
	switch rt.svc.DecideConsume(roomID, requestID, sessionID) {
	case rooms.ConsumeRun:
		return agent.RoomConsumeRun
	case rooms.ConsumeCancel:
		return agent.RoomConsumeCancel
	default:
		return agent.RoomConsumeDefer
	}
}

func (rt agentRoomRuntime) RoomConsumeReleased(roomID, requestID string) error {
	return rt.svc.ReleaseConsume(roomID, requestID)
}

func (rt agentRoomRuntime) RoomConsumeHeld(roomID, requestID string) {
	rt.svc.ConsumeHeld(roomID, requestID)
}

func (rt agentRoomRuntime) RoomConsumeEnded(roomID, requestID string, turnErr error) (bool, error) {
	return rt.svc.ConsumeEnded(roomID, requestID, turnErr)
}

func (rt agentRoomRuntime) RoomReminderTurnEnded(sessionID string) error {
	return rt.svc.ReminderTurnEnded(sessionID)
}

func (rt agentRoomRuntime) RoomTurnCost(c agent.RoomTurnCost) {
	rt.svc.AddTurnCost(rooms.TurnCost{
		RoomID: c.RoomID, RequestID: c.RequestID, TurnID: c.TurnID,
		CostUSD: c.CostUSD, Priced: c.Priced, Inputs: c.Inputs, RoomInputs: c.RoomInputs,
	})
}

func (rt agentRoomRuntime) DeliverRoomAnswer(ctx context.Context, a agent.RoomAnswer) (tools.ReplyReceipt, error) {
	receipt, err := rt.svc.DeliverAnswer(ctx, rooms.Answer{
		RoomID: a.RoomID, RequestID: a.RequestID, Responder: a.Responder,
		ResponderSessionID: a.ResponderSessionID, Content: a.Content, Files: answerFiles(a.Files),
	})
	if errors.Is(err, rooms.ErrAnswerCompleting) {
		// FR-078 (5): accepted and completing, not "nothing posted".
		return tools.ReplyReceipt{ReplyID: receipt.AnswerID, Destination: "the group chat " + receipt.RoomName},
			errors.Join(tools.ErrReplyCompleting, err)
	}
	if err != nil {
		return tools.ReplyReceipt{}, err
	}
	return tools.ReplyReceipt{ReplyID: receipt.AnswerID, Destination: "the group chat " + receipt.RoomName}, nil
}

// roomCardMaxHops bounds the walk from a card's session to its main.
const roomCardMaxHops = 16

// roomCard tells the rooms that a card of the member's own work is pending
// (or no longer pending) in the main chat that sessionID belongs to.
func (h *WSHandler) roomCard(sessionID, cardID string, pending bool) {
	if h == nil || h.rooms == nil || h.rooms.svc == nil || sessionID == "" {
		return
	}
	mainID := h.mainSessionOf(sessionID)
	if mainID == "" {
		return
	}
	if pending {
		h.rooms.svc.CardPending(mainID, cardID)
		return
	}
	h.rooms.svc.CardResolved(mainID, cardID)
}

// roomCardFor is roomCard for a card that may carry a group reference
// (#1449 FR-053, ADR C-I7): a referenced card flips only its own request;
// any other card keeps the per-main behaviour (helpers, unrelated work, and a
// step that answered two groups, D28, which then lights up both).
func (h *WSHandler) roomCardFor(sessionID string, ref *generated.RoomRef, cardID string, pending bool) {
	if ref == nil || ref.RoomId == "" || ref.RequestId == "" {
		h.roomCard(sessionID, cardID, pending)
		return
	}
	if h == nil || h.rooms == nil || h.rooms.svc == nil {
		return
	}
	h.rooms.svc.CardForRequest(ref.RoomId, ref.RequestId, cardID, pending)
}

// mainSessionOf walks ParentSessionID up to the main session a helper
// belongs to (bounded, cycle-safe); "" when there is none or it cannot be
// read.
func (h *WSHandler) mainSessionOf(sessionID string) string {
	seen := map[string]bool{}
	for sid, hop := sessionID, 0; sid != "" && hop < roomCardMaxHops && !seen[sid]; hop++ {
		seen[sid] = true
		store := h.resolveSessionStore(sid)
		if store == nil {
			return ""
		}
		meta, err := store.GetMeta(sid)
		if err != nil || meta == nil {
			return ""
		}
		if meta.Type == session.SessionTypeMain {
			return sid
		}
		sid = meta.ParentSessionID
	}
	return ""
}
