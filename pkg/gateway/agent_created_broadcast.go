// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// The agent_created frame — both halves of its short journey. Same shape as
// D-107's library_changed frame (library_change_broadcast.go): this closes
// the analogous defect for the Agent Picker instead of the Library tree.
//
// THE DEFECT THIS CLOSES (issue #1009): the AgentPicker's ['agents'] query
// (src/hooks/useChatAgents.ts) has no per-query staleTime override, so it
// inherits queryClient.ts's global 30s default. CreateAgentModal.tsx is the
// ONLY frontend code path that calls invalidateQueries({queryKey: ['agents']})
// after a create — every other creation path (any OTHER open tab, and the
// create_agent tool, which lets an agent create another agent mid-conversation
// with no invalidation hook at all) leaves the picker stale for up to 30s.
//
// WHY BROADCAST, NOT A TARGETED PUSH: same reasoning as library_changed —
// the single-user model has no per-account fanout, and the frame carries no
// scoping payload to target with anyway (the Agent Picker has no per-agent
// view; every tab's picker shows the whole roster).

package gateway

import (
	"encoding/json"
	"log/slog"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// broadcastAgentCreated fans one agent_created frame out to every connected
// WS client. Same construction + drop-log shape as broadcastLibraryChange: a
// client whose send buffer is full drops the frame (counted, never
// blocking); a full-buffer WS client's own reconnect/refetch behavior is the
// recovery path, exactly as it is for every other broadcast frame on this
// handler.
func (h *WSHandler) broadcastAgentCreated(f gen.AgentCreatedFrame) {
	f.Type = string(gen.WsFrameTypeAgentCreated)
	data, err := json.Marshal(f)
	if err != nil {
		// Cannot happen for this shape (two required strings), but a marshal
		// failure must never take the create path down — the agent already
		// persisted.
		slog.Error("ws: marshal agent_created frame failed", "error", err)
		return
	}
	h.broadcastRaw(data, "ws: agent_created frame dropped (send buffer full)")
}

// emitAgentCreated is the restAPI's side: called by createAgent AFTER the
// new agent has been durably persisted (never on a validation/4xx failure —
// the frame means "a new agent now exists", and a refused create created
// nothing). Nil-safe by design: the broadcaster is wired in at boot once the
// WS handler exists, and every restAPI constructed without it (tests,
// partial boots) must keep working.
func (a *restAPI) emitAgentCreated(agentID string) {
	if agentID == "" {
		return
	}
	fn := a.agentCreatedBroadcast.Load()
	if fn == nil {
		return
	}
	(*fn)(gen.AgentCreatedFrame{AgentId: agentID})
}
