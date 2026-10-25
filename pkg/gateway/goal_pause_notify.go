package gateway

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/google/uuid"
)

// goalPausedNotifier uses the same store and recipient-scoped live delivery as
// the other bell producers. A shared session is broadcast only after the agent
// has successfully read its genuinely empty Owner.
func goalPausedNotifier(store *notifications.Store, emitter knowledgeDriftEmitter, getCfg func() *config.Config) agent.GoalPausedFunc {
	return func(event agent.GoalPausedEvent) {
		recipients := []string{event.Owner}
		if event.Owner == "" {
			recipients = knowledgeDriftRecipients(getCfg)
		}
		reason := goalPausedReason(event.Reason)
		title := fmt.Sprintf("Goal paused in %s: %s", event.ChatTitle, reason)
		for _, recipient := range recipients {
			n := notifications.Notification{
				Recipient: recipient, Type: notifications.TypeGoalPaused,
				Title: title, Body: reason, Severity: notifications.SeverityWarning,
				CreatedAtMs: time.Now().UnixMilli(), CoalesceKey: "goal_paused:" + event.GoalID,
				SessionID: event.SessionID, AgentID: event.AgentID,
			}
			stored := n
			switch {
			case recipient == agent.NotificationAdminBroadcast:
				slog.Warn("goal: pause notification has no persistable recipient; live-broadcast only", "session_id", event.SessionID, "goal_id", event.GoalID)
			case store == nil:
				slog.Error("goal: pause notification store unavailable; pushing live only", "session_id", event.SessionID, "goal_id", event.GoalID)
			default:
				persisted, err := store.Create(n)
				if err != nil {
					slog.Error("goal: could not persist pause notification; pushing live anyway", "session_id", event.SessionID, "goal_id", event.GoalID, "error", err)
				} else {
					stored = persisted
				}
			}
			if stored.ID == "" {
				stored.ID = uuid.NewString()
			}
			if emitter != nil {
				emitter.EmitNotification(agent.NotificationPayload{
					Recipient: recipient, ID: stored.ID, NotificationType: stored.Type,
					Title: stored.Title, Body: stored.Body, Severity: stored.Severity,
					Read: stored.Read, CreatedAtMs: stored.CreatedAtMs,
					SessionID: stored.SessionID, AgentID: stored.AgentID,
				})
			}
		}
	}
}

// goalPausedReason follows the chief's 160-code-point default. Short reasons
// are byte-for-byte unchanged, including trailing whitespace. Only a truncated
// prefix is trimmed, then receives one Unicode ellipsis.
func goalPausedReason(reason string) string {
	points := []rune(reason)
	if len(points) <= 160 {
		return reason
	}
	return strings.TrimRightFunc(string(points[:159]), unicode.IsSpace) + "…"
}
