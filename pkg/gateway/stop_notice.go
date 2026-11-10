package gateway

import (
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/notifications"
)

// publishStopNotice reports a Stop operation, never the end of a chat turn.
// The existing per-recipient notification forwarder delivers it; the session
// hub must not turn it into a chat error or attach a session sequence number.
func (h *WSHandler) publishStopNotice(wc *wsConn, sessionID, class, title, body, severity string) {
	if wc == nil || wc.userID == "" {
		// No authenticated recipient: do not replace it with admin broadcast
		// or widen the audience of this Stop. Its original outcome still
		// returns through the calling Stop path; keep the operational trace.
		slog.Warn("ws: Stop notice has no recipient", "session_id", sessionID, "class", class, "message", body)
		return
	}
	n := notifications.Notification{
		Recipient: wc.userID, Type: notifications.TypeStopNotice,
		Title: title, Body: body, Severity: severity, SessionID: sessionID,
		CreatedAtMs: time.Now().UnixMilli(),
	}
	if sessionID != "" {
		n.CoalesceKey = "stop_notice:" + class + ":" + sessionID
	}
	if h.notifStore != nil {
		stored, err := h.notifStore.Create(n)
		if err != nil {
			// Retain live delivery to this same recipient if storage failed,
			// as schedule/drift notifications do. Never lose both channels.
			slog.Error("ws: could not persist Stop notice; pushing live", "session_id", sessionID, "recipient", wc.userID, "error", err)
		} else {
			n = stored
		}
	} else {
		slog.Error("ws: Stop notification store unavailable; pushing live", "session_id", sessionID, "recipient", wc.userID)
	}
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	h.agentLoop.EmitNotification(agent.NotificationPayload{
		Recipient: n.Recipient, ID: n.ID, NotificationType: n.Type,
		Title: n.Title, Body: n.Body, Severity: n.Severity, Read: n.Read,
		CreatedAtMs: n.CreatedAtMs, SessionID: n.SessionID,
	})
}
