// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// deliverLive sends frame to the connections bound to sessionID's hub right
// now, without numbering or journaling it (chat-links spec, "Replay
// guarantee" and D-hub). It is the delivery path for the live-only
// open_in_library frame: the frame has no seq, never enters the journal tail
// an incremental reconnect replays, never touches the active-turn projection,
// and is never persisted — so no reload or reconnect can ever receive it.
//
// It looks the hub up and never creates one: a delivery to a session with no
// hub, or to an evicted hub, drops the frame (a tab attaching later must open
// nothing, BS-6.11). Returns the number of connections the frame was queued
// for.
func (r *hubRegistry) deliverLive(sessionID string, frame []byte) int {
	if r == nil || sessionID == "" {
		return 0
	}
	h := r.lookup(sessionID)
	if h == nil {
		return 0
	}
	return h.deliverLive(frame)
}

// deliverLive is the per-hub half of hubRegistry.deliverLive. It follows
// publishMeta's connection handling and nothing else:
//
//   - an evicted hub delivers nothing and is NOT re-routed through
//     getOrCreate (its connections are gone; a new hub is a new attach);
//   - each bound connection's enqueue gets the raw bytes — no spliceSeq. A
//     connection in hold mode (mid-attach) queues it in held and sees it after
//     catch_up_complete via releaseHold;
//   - a connection whose enqueue returns false (dead, or just closed for
//     falling behind) is unbound under h.mu and handed to onOverflow after the
//     lock is released, in publishMeta's order;
//   - head, the journal, the projection, publishedTotal, the global budget,
//     the idle sweep and lastActive are all left untouched.
func (h *sessionHub) deliverLive(frame []byte) int {
	h.mu.Lock()
	if h.evicted {
		h.mu.Unlock()
		return 0
	}
	delivered := 0
	var overflowed []hubConn
	for c := range h.conns {
		if c.enqueue(frame) {
			delivered++
			continue
		}
		overflowed = append(overflowed, c)
	}
	for _, c := range overflowed {
		delete(h.conns, c)
	}
	onOverflow := h.onOverflow
	h.mu.Unlock()

	if onOverflow != nil {
		for _, c := range overflowed {
			onOverflow(c)
		}
	}
	return delivered
}

// hubDeliverOpenInLibrary turns show_to_user's open request into the
// generated open_in_library frame and delivers it live (chat-links
// "Tool-to-hub seam"). It publishes only for a successful, uncancelled
// show_to_user call carrying a target; the frame is built from the typed
// target, never from the result text.
func (h *WSHandler) hubDeliverOpenInLibrary(sessionID string, p agent.ToolExecEndPayload) {
	if h.hubs == nil || sessionID == "" || p.Tool != tools.ShowToUserToolName ||
		p.IsError || p.Cancelled || p.OpenInLibrary == nil {
		return
	}
	frame := generated.OpenInLibraryFrame{
		Type:        string(generated.WsFrameTypeOpenInLibrary),
		SessionId:   sessionID,
		CallId:      string(p.ToolCallID),
		RequestId:   p.OpenInLibrary.RequestID,
		WorkspaceId: p.OpenInLibrary.WorkspaceID,
		Path:        p.OpenInLibrary.Path,
	}
	data, err := json.Marshal(frame)
	if err != nil {
		logsafeError("ws: marshal open_in_library failed", "session_id", sessionID, "error", err)
		return
	}
	h.hubs.deliverLive(sessionID, data)
}
