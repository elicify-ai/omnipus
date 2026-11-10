package agent

// RM3 D1: primary agent-level title contract, not a full-turn trigger or UAT.
// Expectations are the founder-approved main label and explicit untitled fallback.
import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestGoalPauseRM3TitleContract(t *testing.T) {
	const titledChat = "B1 post-API fixture chat"
	const goalID = "b1-postapi-metadata-goal"
	const reason = "blocked"
	for _, tc := range []struct {
		name           string
		main           bool
		configuredName string
		chatTitle      string
		wantTitle      string
	}{
		{"MainUsesActualConfiguredTrimmedDisplayName", true, "  Jim \t", "", "Jim - Main Session"},
		{"MainBlankNameNeverUsesID", true, "", "", "an untitled chat"},
		{"MainWhitespaceNameNeverUsesID", true, " \t ", "", "an untitled chat"},
		{"OrdinaryEmptyTitleDoesNotGuessAgentLabel", false, "Jim", "", "an untitled chat"},
		{"OrdinaryWhitespaceOnlyTitleIsUntitled", false, "Jim", " \t ", "an untitled chat"},
		{"TitledOrdinaryChatUnchanged", false, "Jim", titledChat, titledChat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentID := grReviewerID + "-rm3-title"
			h := newGRHarness(t, grHarnessOptions{extraAgents: []config.AgentConfig{{
				ID: agentID, Name: tc.configuredName, Type: config.AgentTypeCustom, Home: t.TempDir(),
			}}})
			// Config precedes the actual registry constructor; never edit live Name.
			inst, ok := h.al.GetRegistry().GetAgent(agentID)
			require.True(t, ok)
			require.NotNil(t, inst)
			require.Equal(t, tc.configuredName, inst.Name)
			store := h.al.GetSessionStore()
			var meta *session.UnifiedMeta
			var err error
			if tc.main {
				meta, err = store.GetOrCreateMainSession(testHarnessWorkspaceMembershipID, agentID)
			} else {
				meta, err = store.NewSession(session.SessionTypeChat, "web", agentID)
			}
			require.NoError(t, err)
			require.NotNil(t, meta)
			if tc.chatTitle != "" {
				title := tc.chatTitle
				require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{Title: &title}))
			}
			meta, err = store.GetMeta(meta.ID)
			require.NoError(t, err)
			require.Equal(t, agentID, meta.AgentID)
			require.Equal(t, tc.chatTitle, meta.Title)
			require.Empty(t, meta.Owner, "unit inputs use the genuine factory's ownership, not a synthetic user")
			if tc.main {
				require.Equal(t, session.SessionTypeMain, meta.Type)
				require.Equal(t, testHarnessWorkspaceMembershipID, meta.WorkspaceID)
				if strings.TrimSpace(inst.Name) != "" {
					require.Equal(t, tc.wantTitle, session.MainSessionTitle(strings.TrimSpace(inst.Name)))
				}
			} else {
				require.Equal(t, session.SessionTypeChat, meta.Type)
			}
			require.Same(t, store, h.al.ResolveSessionStore(meta.ID))
			events := []GoalPausedEvent{}
			h.al.SetGoalPausedHook(func(event GoalPausedEvent) { events = append(events, event) })
			t.Cleanup(func() { h.al.SetGoalPausedHook(nil) })
			h.al.notifyGoalPaused(meta.ID, goalID, "blocked", reason)
			require.Len(t, events, 1, "SETUP: actual notifier must invoke the approved hook exactly once before checking its title")
			actual := events[0]
			require.Equal(t, meta.ID, actual.SessionID)
			require.Equal(t, goalID, actual.GoalID)
			require.Equal(t, agentID, actual.AgentID)
			require.Equal(t, meta.Owner, actual.Owner)
			require.Equal(t, reason, actual.Reason)
			for _, rawID := range []string{meta.ID, goalID, agentID} {
				require.NotEmpty(t, rawID)
				require.NotContains(t, actual.ChatTitle, rawID, "RM3 display title must not expose raw identifiers")
			}
			require.Equal(t, tc.wantTitle, actual.ChatTitle, "RM3 D1 requires the actual main label or explicit untitled fallback")
			require.Equal(t, GoalPausedEvent{
				SessionID: meta.ID, GoalID: goalID, AgentID: agentID,
				ChatTitle: tc.wantTitle, Owner: meta.Owner, Reason: reason,
			}, actual)
		})
	}
}
