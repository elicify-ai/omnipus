package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Founder Q1 B: an existing chat/helper cannot be adopted in another workspace.
// Refuse BEFORE append/publication; keep A's address and archive untouched.
func TestMemoryScopeRefusalRound2(t *testing.T) {
	t.Run("ConversationAndHelperFrame", func(t *testing.T) {
		for _, kind := range []session.UnifiedSessionType{session.SessionTypeChat, session.SessionTypeDelegate} {
			t.Run(string(kind), func(t *testing.T) {
				h, messages, store, wc := newProvenanceHarness(t)
				home := t.TempDir()
				h.home = home
				writeDelegationTestWorkspaceRecord(t, home, "alpha", true)
				writeDelegationTestWorkspaceRecord(t, home, "beta", false)
				meta, err := store.NewSession(kind, "webchat", "mia")
				require.NoError(t, err)
				a := "alpha"
				require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &a}))
				require.NoError(t, store.AddMessage(meta.ID, "user", "ROOM_ALPHA_RECALLED"))
				before, err := store.ReadArchive(t.Context(), meta.ID)
				require.NoError(t, err)
				require.Len(t, before, 1, "instrument must see A's existing model history")
				h.handleChatMessage(context.Background(), "scope-refusal", meta.ID, "work in B", "mia", nil, "", "beta", wc)
				frames := issue1090DrainQueuedFrames(t, wc)
				errors := make([]map[string]any, 0, len(frames))
				for _, frame := range frames {
					if frame["type"] == "error" {
						errors = append(errors, frame)
					}
				}
				require.Len(t, errors, 1, "cross-workspace reuse must return one visible refusal: %+v", frames)
				text, ok := errors[0]["message"].(string)
				require.True(t, ok)
				require.Contains(t, strings.ToLower(text), "workspace")
				require.Contains(t, strings.ToLower(text), "conversation")
				select {
				case msg := <-messages.InboundChan():
					t.Fatalf("refused frame was published for execution: %+v", msg)
				default:
				}
				after, err := store.GetMeta(meta.ID)
				require.NoError(t, err)
				require.Equal(t, a, after.WorkspaceID)
				archive, err := store.ReadArchive(t.Context(), meta.ID)
				require.NoError(t, err)
				require.Equal(t, before, archive, "refusal must preserve A without appending B input")
			})
		}
	})
	t.Run("ChannelBindingDoesNotRelabelArchives", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		writeTestWorkspaceJSON(t, api, "alpha", "active", []string{"test-agent"}, false)
		writeTestWorkspaceJSON(t, api, "beta", "active", []string{"test-agent"}, false)
		seedChannelInstance(t, api, "telegram.sales")
		require.Equal(t, http.StatusOK, setChannelRoutingReq(t, api, "telegram.sales", `{"workspace_id":"alpha","default_agent_id":"test-agent"}`).Code)
		old := seedExistingChannelSession(t, api, "telegram.sales", "peer", "test-agent", "alpha")
		store := api.agentLoop.GetSessionStore()
		require.NoError(t, store.AddMessage(old.ID, "user", "ROOM_ALPHA_RECALLED"))
		before, err := store.ReadArchive(t.Context(), old.ID)
		require.NoError(t, err)
		response := setChannelRoutingReq(t, api, "telegram.sales", `{"workspace_id":"beta","default_agent_id":"test-agent"}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		after, err := store.GetMeta(old.ID)
		require.NoError(t, err)
		require.Equal(t, "alpha", after.WorkspaceID, "channel settings must not move an old conversation")
		archive, err := store.ReadArchive(t.Context(), old.ID)
		require.NoError(t, err)
		require.Equal(t, before, archive)
	})
}
