package gateway

// mail_presence.go — the Mail panel presence adapter (w5-integration wave;
// w5 spec US-2/MC-4; landing-order register rows 5/11; w5 §2.3 lifecycle
// trace).
//
// This package is the ADAPTER, not a registry: the one PanelPresence store
// is W1's (pkg/email/pool.go), consumed here through its published
// Bind/Unbind/UnbindAll/Count API. The gateway binds each observer to the
// AUTHENTICATED CONNECTION's opaque chat ID — never to any caller-supplied
// connection or session identity — and revokes exactly that connection's
// observers in ServeHTTP's deferred teardown block, the single choke point
// every disconnected socket passes through (explicit close frame, read
// error, ping-deadline expiry, abnormal closure; logout and browser quit
// converge on the same block because they all end the socket).
//
// Presence never grants access: mailbox authorization stays entirely with
// the per-request pair check (mailPairClient); an open observer only ever
// EXTENDS what the pool may retain. Absence of presence degrades to
// request-scoped work, never to an error (US-2.4/US-2.5, B-7/B-8).
//
// Frame validation is owned here: no inbound JSON Schema was generated for
// mail_panel_observer (the landed inboundschemas set has no
// MailPanelObserverFrame.yaml), so wsFrameSchemaName maps nothing for this
// type and Mail's handler performs its own field validation — exactly the
// owned-ack/safe-error shape the w5 spec §2.3 correction requires. The
// refusal codes are the generated frame's closed classes:
// malformed_frame | unauthorized_workspace.

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailObserverIDMax bounds an opaque observer or workspace ID. Real IDs are
// UUID-scale; the bound only stops a hostile frame from parking arbitrary
// bytes in the registry.
const mailObserverIDMax = 256

// MailPanelObserverActionOpen / Close are the frame's two actions.
const (
	MailPanelObserverActionOpen  = "open"
	MailPanelObserverActionClose = "close"
)

// SetMailPresence binds W1's one presence registry to this handler. Boot
// calls it once, right after the WSHandler is constructed; a nil registry
// keeps the conservative default (every Mail panel connection is
// request-scoped).
func (h *WSHandler) SetMailPresence(p *email.PanelPresence) {
	h.mailPresence.Store(p)
}

// mailPresenceOf is the nil-safe registry reader.
func (h *WSHandler) mailPresenceOf() *email.PanelPresence { return h.mailPresence.Load() }

// mailWorkspaceAuthorized checks the open frame's workspace against this
// install's workspace store (the same source the REST workspace reads use).
// A missing/unreadable workspace fails CLOSED — authorization is never
// presumed from a lookup error.
func (h *WSHandler) mailWorkspaceAuthorized(workspaceID string) bool {
	_, err := readWorkspaceFile(h.home, workspaceID)
	return err == nil
}

// handleMailPanelObserverFrame serves one client mail_panel_observer frame
// (dispatchFrame's case). The observer binds to connID — the gateway-issued
// connection identity — and only this connection can unbind it.
func (h *WSHandler) handleMailPanelObserverFrame(wc *wsConn, connID string, f generated.MailPanelObserverFrame) {
	p := h.mailPresenceOf()
	if p == nil {
		// Mail runtime not wired (a harness without the boot path): the
		// panel's conservative fallback is request-scoped work; refuse
		// visibly rather than pretending the observer is bound.
		h.sendMailPanelObserverError(wc, f, "malformed_frame", "mail presence is not available")
		return
	}
	f.Action = strings.TrimSpace(f.Action)
	f.ObserverId = strings.TrimSpace(f.ObserverId)
	f.WorkspaceId = strings.TrimSpace(f.WorkspaceId)
	if (f.Action != MailPanelObserverActionOpen && f.Action != MailPanelObserverActionClose) ||
		f.ObserverId == "" || len(f.ObserverId) > mailObserverIDMax ||
		f.WorkspaceId == "" || len(f.WorkspaceId) > mailObserverIDMax {
		h.sendMailPanelObserverError(wc, f, "malformed_frame", "malformed mail panel observer frame")
		return
	}
	if f.Action == MailPanelObserverActionOpen {
		if !h.mailWorkspaceAuthorized(f.WorkspaceId) {
			// Never a hint about what WOULD be authorized (the generated
			// frame's contract for this class).
			h.sendMailPanelObserverError(wc, f, "unauthorized_workspace", "workspace not authorized for this connection")
			return
		}
		p.Bind(connID, f.ObserverId, f.WorkspaceId)
	} else {
		p.Unbind(connID, f.ObserverId)
	}
	sendConnGenFrame(wc, string(generated.WsFrameTypeMailPanelObserverAck), generated.MailPanelObserverAckFrame{
		Type:        string(generated.WsFrameTypeMailPanelObserverAck),
		Action:      f.Action,
		ObserverId:  f.ObserverId,
		WorkspaceId: f.WorkspaceId,
	})
}

// sendMailPanelObserverError emits the generated refusal frame. Failures to
// SEND (a dying socket) are the pump's problem — the registry binding for a
// dead connection is revoked by teardown regardless.
func (h *WSHandler) sendMailPanelObserverError(wc *wsConn, f generated.MailPanelObserverFrame, code, message string) {
	slog.Debug("ws: mail panel observer refused", "code", code)
	sendConnGenFrame(wc, string(generated.WsFrameTypeMailPanelObserverError), generated.MailPanelObserverErrorFrame{
		Type:        string(generated.WsFrameTypeMailPanelObserverError),
		Code:        code,
		Error:       message,
		ObserverId:  f.ObserverId,
		WorkspaceId: f.WorkspaceId,
	})
}

// revokeMailPanelObservers is the teardown hook: revoke every observer this
// connection bound. ServeHTTP's deferred block calls it on every exit path,
// before the socket closes.
func (h *WSHandler) revokeMailPanelObservers(connID string) {
	if p := h.mailPresenceOf(); p != nil {
		p.UnbindAll(connID)
	}
}
