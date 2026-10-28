package gateway

// RM3: exact bounded contract accepted by the parent before implementation.
// Real boot, pause notifier, owned audience, persistent store and live publication.
import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestGoalPauseB1RM3UntitledChatBell(t *testing.T) {
	// Existing short reason input is reused, not read from notification output.
	const reason = "Fixture initial pause"
	for _, tc := range []struct {
		name, chatTitle, displayName, wantCarrier string
	}{
		{"UntitledRegisteredDisplayName", "", "  Jim  ", "your chat with Jim"},
		{"ExistingTitledChatUnchanged", b1CompTitle, "  Jim  ", b1CompTitle},
		{"UntitledEmptyNameNeverUsesID", "", "", "an untitled chat"},
		{"UntitledWhitespaceNameNeverUsesID", "", " \t ", "an untitled chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := b1CompBoot(t, false, false)
			f.arm(t)
			store := f.al.GetSessionStore()
			meta, err := store.GetMeta(f.sid)
			require.NoError(t, err)
			require.Equal(t, b1CompOwner, meta.Owner)
			inst, ok := f.al.GetRegistry().GetAgent(meta.AgentID)
			require.True(t, ok)
			require.NotNil(t, inst)
			originalName := inst.Name
			inst.Name = tc.displayName
			t.Cleanup(func() { inst.Name = originalName })
			title := tc.chatTitle
			require.NoError(t, store.SetMeta(f.sid, session.MetaPatch{Title: &title}))
			arranged, err := store.GetMeta(f.sid)
			require.NoError(t, err)
			require.Equal(t, tc.chatTitle, arranged.Title)
			require.Equal(t, meta.AgentID, arranged.AgentID)
			require.Equal(t, b1CompOwner, arranged.Owner)
			require.Equal(t, tc.displayName, inst.Name)
			if tc.chatTitle == "" && strings.TrimSpace(tc.displayName) != "" {
				require.Equal(t, "your chat with "+strings.TrimSpace(inst.Name), tc.wantCarrier)
			}

			f.user(t, "blocked", reason)
			current, err := store.GetMeta(f.sid)
			require.NoError(t, err)
			require.Equal(t, tc.chatTitle, current.Title, "RM3 must remain a genuinely arranged chat")
			require.Equal(t, meta.AgentID, current.AgentID)
			require.Equal(t, b1CompOwner, current.Owner)
			registered, ok := f.al.GetRegistry().GetAgent(current.AgentID)
			require.True(t, ok)
			require.Same(t, inst, registered)
			require.Equal(t, tc.displayName, registered.Name)

			bells := f.bells()
			require.Len(t, bells, 1, "one real owner-scoped global notification")
			bell := bells[0]
			require.NotEmpty(t, bell.ID)
			require.Equal(t, b1CompOwner, bell.Recipient)
			require.Equal(t, notifications.TypeGoalPaused, bell.NotificationType)
			require.Equal(t, notifications.SeverityWarning, bell.Severity)
			require.Equal(t, f.sid, bell.SessionID)
			require.Equal(t, meta.AgentID, bell.AgentID)
			require.Equal(t, reason, bell.Body)
			rows := f.saved(t, b1CompOwner)
			require.Len(t, rows, 1, "actual durable row must exist before checking copy")
			saved := rows[0]
			require.Equal(t, bell.ID, saved.ID)
			require.False(t, saved.Read)
			require.Equal(t, "goal_paused:"+f.gid, saved.CoalesceKey)
			require.Equal(t, bell.Title, saved.Title)
			require.Equal(t, reason, saved.Body)
			require.Equal(t, bell.SessionID, saved.SessionID)
			require.Equal(t, bell.AgentID, saved.AgentID)
			require.Equal(t, bell.NotificationType, saved.Type)
			require.Equal(t, bell.Severity, saved.Severity)
			for _, account := range f.cfg.Gateway.Users {
				if account.Username != b1CompOwner {
					require.Empty(t, f.saved(t, account.Username))
				}
			}
			f.assertLive(t, bell, false)
			for _, rawID := range []string{f.sid, f.gid, meta.AgentID} {
				require.NotEmpty(t, rawID)
				require.NotContains(t, bell.Title, rawID, "global title must never expose raw identifiers")
			}
			wantTitle := "Goal paused in " + tc.wantCarrier + ": " + reason
			require.Equal(t, wantTitle, bell.Title, "RM3 actual named/untitled conversation context is required")
			require.Equal(t, wantTitle, saved.Title, "saved and live global titles share the exact accepted contract")
		})
	}
}
