package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Founder ruling (UI feedback 4): a main session's title is the fixed string
// "<Agent> - Main Session" (ASCII hyphen, one space each side), always showing
// the agent's CURRENT display name; renaming the agent renames its main.
func TestMainSessionTitle_DerivedFromCurrentAgentName(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"
	const wsID = "01JXHBTESTWSID0000000009"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "WS", Status: "active", CoreTeam: []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	store := api.agentLoop.GetSessionStore()
	mainMeta, err := store.GetOrCreateMainSession(wsID, agentID)
	require.NoError(t, err)
	chat, err := store.NewSession("chat", "webchat", agentID)
	require.NoError(t, err)

	inst, ok := api.agentLoop.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "instrument: the fixture agent must be in the registry")

	titles := func() map[string]string {
		t.Helper()
		w := httptest.NewRecorder()
		api.listSessions(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		out := map[string]string{}
		var page struct {
			Sessions []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		for _, s := range page.Sessions {
			out[s.ID] = s.Title
		}
		return out
	}

	inst.Name = "Jim"
	got := titles()
	require.Contains(t, got, mainMeta.ID, "the main must be listed")
	assert.Equal(t, "Jim - Main Session", got[mainMeta.ID])
	assert.Empty(t, got[chat.ID], "an ordinary untitled chat is not given a derived title on read")

	inst.Name = "Jimmy"
	assert.Equal(t, "Jimmy - Main Session", titles()[mainMeta.ID], "the title follows the agent's rename")

	stored, err := store.GetMeta(mainMeta.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.Title, "nothing is stored: the title is derived on read")
}

// A main's title is fixed, so renaming it is refused rather than accepted and
// then never shown.
func TestRenameSession_MainTitleIsFixed(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"
	const wsID = "01JXHBTESTWSID0000000010"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "WS", Status: "active", CoreTeam: []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	store := api.agentLoop.GetSessionStore()
	mainMeta, err := store.GetOrCreateMainSession(wsID, agentID)
	require.NoError(t, err)
	chat, err := store.NewSession("chat", "webchat", agentID)
	require.NoError(t, err)

	rename := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := withAdminRole(httptest.NewRequest(http.MethodPut, "/api/v1/sessions/"+id, strings.NewReader(`{"title":"Mine"}`)))
		r.Header.Set("Content-Type", "application/json")
		api.renameSession(w, r, id)
		return w
	}
	assert.Equal(t, http.StatusOK, rename(chat.ID).Code, "control: an ordinary chat can be renamed by hand")
	w := rename(mainMeta.ID)
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	stored, _ := store.GetMeta(mainMeta.ID)
	assert.Empty(t, stored.Title)
}
