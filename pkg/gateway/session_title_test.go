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

// S2-07: the derived main title must reach EVERY read surface, not only
// GET /sessions. The agent is renamed first, so a stale stored title could
// never pass for the derived one, and the stored title is asserted empty at the
// end so no surface may have persisted it.
func TestMainSessionTitle_ReachesEveryReadSurface(t *testing.T) {
	api, _ := buildHeartbeatTestAPI(t)
	const agentID = "mia"
	const wsID = "01JXHBTESTWSID0000000011"
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: wsID, Name: "WS", Status: "active", CoreTeam: []string{agentID},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	store := api.agentLoop.GetSessionStore()
	mainMeta, err := store.GetOrCreateMainSession(wsID, agentID)
	require.NoError(t, err)
	inst, ok := api.agentLoop.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "instrument: the fixture agent must be in the registry")
	inst.Name = "Jim"
	const want = "Jim - Main Session"

	// getSession (GET /sessions/{id}): the title rides on the SessionDetail's
	// nested session object.
	t.Run("getSession", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.getSession(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+mainMeta.ID, nil)), mainMeta.ID)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var detail struct {
			Session struct {
				Title string `json:"title"`
			} `json:"session"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
		assert.Equal(t, want, detail.Session.Title,
			"the session detail must show the derived title, not an empty or stored one")
	})

	// listAgentSessions (GET /agents/{id}/sessions): a bare array of sessions.
	t.Run("listAgentSessions", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.listAgentSessions(w, agentID)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var sessions []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sessions))
		require.NotEmpty(t, sessions, "instrument: the agent's sessions must be listed")
		found := false
		for _, s := range sessions {
			if s.ID == mainMeta.ID {
				found = true
				assert.Equal(t, want, s.Title, "the agent's own session list must derive the same title")
			}
		}
		assert.True(t, found, "the main must appear in its agent's session list")
	})

	// Instrument: the same fixture DOES derive the title on the surface that was
	// already covered, so the two above are not passing on an empty fixture.
	t.Run("control: listSessions still derives it", func(t *testing.T) {
		w := httptest.NewRecorder()
		api.listSessions(w, withAdminRole(httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var page struct {
			Sessions []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"sessions"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
		found := false
		for _, s := range page.Sessions {
			if s.ID == mainMeta.ID {
				found = true
				assert.Equal(t, want, s.Title)
			}
		}
		assert.True(t, found, "control: the main must be listed")
	})

	stored, err := store.GetMeta(mainMeta.ID)
	require.NoError(t, err)
	assert.Empty(t, stored.Title, "no read surface may persist the derived title")
}
