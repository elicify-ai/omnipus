// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// attention_ack.go: the gateway side of a main's attention (session-core U11,
// FR-047, C-ATTENTION). It computes Session.needs_attention for REST list and
// detail, answers the attach with attention_bound, and writes the one shared
// seen mark when an acknowledging attach completes.
//
// Founder rule: only a pending question card, a pending tool approval and an
// unseen finished/failed goal outcome light a main. A goal the user stopped
// never does. A source that cannot be read leaves needs_attention OUT (the
// client shows unknown), never a guessed false.

package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// attentionReadFailures counts attention sources that could not be read
// (a row left unknown, or a bound left out). A test-visible counter.
var attentionReadFailures atomic.Int64

// maxAttentionParentWalk bounds the helper-to-main parent walk.
const maxAttentionParentWalk = 32

// pendingApprovalMains is the set of main ids that have a pending tool
// approval, plus whether the set could be determined at all.
type pendingApprovalMains struct {
	mains   map[string]struct{}
	unknown bool
}

// attentionApprovalSource is satisfied by the approval registry; a func seam so
// the walk is testable without the whole gateway.
type attentionApprovalSource interface {
	pendingApprovals() []*approvalEntry
}

// pendingApprovalMainsFor reads the pending approvals once and assigns each to
// its main: the approval's own session when that is a main, otherwise the first
// main ancestor reached through ParentSessionID (a helper's approval blocks its
// main). store resolves a session's metadata. A nil registry, or a meta that
// cannot be read mid-walk, marks the result unknown.
func pendingApprovalMainsFor(reg attentionApprovalSource, getMeta func(string) (*session.UnifiedMeta, error)) pendingApprovalMains {
	out := pendingApprovalMains{mains: map[string]struct{}{}}
	if reg == nil {
		// A missing approval registry is a wiring fault, never "nothing
		// pending": the answer is unknown, so no main is reported false.
		out.unknown = true
		return out
	}
	for _, e := range reg.pendingApprovals() {
		id := e.SessionID
		for depth := 0; id != "" && depth < maxAttentionParentWalk; depth++ {
			meta, err := getMeta(id)
			if err != nil || meta == nil {
				slog.Warn("rest: attention: could not read a session while assigning a pending approval to its main",
					"session_id", id, "error", err)
				attentionReadFailures.Add(1)
				out.unknown = true
				break
			}
			if meta.Type == session.SessionTypeMain {
				out.mains[meta.ID] = struct{}{}
				break
			}
			id = meta.ParentSessionID
		}
	}
	return out
}

// pendingApprovalMains resolves the approval-to-main assignment for one request.
func (a *restAPI) pendingApprovalMains() pendingApprovalMains {
	if a.approvalReg == nil {
		// A nil registry is a wiring fault. Reporting false would hide it
		// (the founder's rule: never a false "nothing to see"), so the answer
		// is unknown and needs_attention is left out on every main row that is
		// not already true.
		slog.Warn("rest: attention: the approval registry is not wired; needs_attention is left unset on mains")
		attentionReadFailures.Add(1)
		return pendingApprovalMains{unknown: true}
	}
	return pendingApprovalMainsFor(a.approvalReg, func(id string) (*session.UnifiedMeta, error) {
		store := a.resolveSessionStore(id)
		if store == nil {
			return nil, fmt.Errorf("session %q: no store", id)
		}
		return store.GetMeta(id)
	})
}

// mainNeedsAttention computes needs_attention for one session. It returns nil
// for a non-main, and nil (unknown) for a main whose answer is not "true" while
// some source could not be read.
func mainNeedsAttention(m *session.UnifiedMeta, approvals pendingApprovalMains) *bool {
	if m == nil || m.Type != session.SessionTypeMain {
		return nil
	}
	yes := true
	if m.Attention.OutcomeOrder > m.Attention.SeenOrder {
		return &yes
	}
	if _, pending := approvals.mains[m.ID]; pending {
		return &yes
	}
	unknown := approvals.unknown
	if m.PendingAskJSON != "" {
		var set askuser.PendingSet
		if err := json.Unmarshal([]byte(m.PendingAskJSON), &set); err != nil {
			slog.Warn("rest: attention: unreadable pending ask on a main", "session_id", m.ID, "error", err)
			attentionReadFailures.Add(1)
			unknown = true
		} else if set.Status == askuser.StatusPending {
			return &yes
		}
	}
	if unknown {
		return nil
	}
	no := false
	return &no
}

// stampNeedsAttention sets s.NeedsAttention for the meta it was built from.
func stampNeedsAttention(s *gen.Session, m *session.UnifiedMeta, approvals pendingApprovalMains) {
	s.NeedsAttention = mainNeedsAttention(m, approvals)
}

// attentionBoundFor answers session_state.attention_bound for an attach: the
// main's saved outcome order (0 when none), nil for a non-main, the
// connection-open emit and an unreadable mark.
func (h *WSHandler) attentionBoundFor(sessionID string) *int64 {
	if sessionID == "" || h.agentLoop == nil {
		return nil
	}
	store := h.agentLoop.ResolveSessionStore(sessionID)
	if store == nil {
		return nil
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		if err != nil {
			slog.Warn("ws: attention: could not read the session for attention_bound", "session_id", sessionID, "error", err)
			attentionReadFailures.Add(1)
		}
		return nil
	}
	if meta.Type != session.SessionTypeMain {
		return nil
	}
	bound := meta.Attention.OutcomeOrder
	return &bound
}

// ackAttention is A9 of handleAttachSession: after the attach completed, an
// acknowledging attach (ack_attention true AND an attention_bound) of a main
// advances the one shared seen mark to max(seen, min(bound, outcomeOrder)).
// Every other case writes nothing. A failed write is logged and counted; the
// attach itself has already succeeded and stays so.
func (wh *wsHandlerHandleAttachSession) ackAttention() {
	if wh.failed || wh.ack == nil || !wh.ack.Ack || wh.ack.Bound == nil || wh.store == nil {
		return
	}
	meta, err := wh.store.GetMeta(wh.attachID)
	if err != nil || meta == nil || meta.Type != session.SessionTypeMain {
		return
	}
	if _, err := wh.store.AckAttentionSeen(wh.attachID, *wh.ack.Bound); err != nil {
		slog.Error("ws: attention: could not write the seen mark", "session_id", wh.attachID, "error", err)
		attentionReadFailures.Add(1)
	}
}
