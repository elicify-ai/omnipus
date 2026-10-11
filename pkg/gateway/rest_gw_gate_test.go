// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Final-gate S2 gateway cluster (S2-CONSOLIDATED.md). One test per finding, each
// drives the real handler.

// H1 (finding 1): DELETE /agents/{id} with a stale revision must refuse BEFORE
// any destructive step. Before the fix the cleanup cascade deleted the agent's
// sessions and unassigned its tasks, and only then did DeleteState compare
// revisions and answer 409 - the agent survived with its data gone.
func TestGW_H1_StaleRevisionAgentDeleteDestroysNothing(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(
		`{"name": "Stale Revision", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I must survive."}`))
	createR.Header.Set("Content-Type", "application/json")
	createR.URL.Path = "/api/v1/agents"
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code, createW.Body.String())
	var created gen.Agent
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))

	sessStore, err := session.NewUnifiedStore(filepath.Join(api.homePath, "sessions"))
	require.NoError(t, err)
	meta, err := sessStore.NewSession(session.SessionTypeChat, "webchat", created.Id)
	require.NoError(t, err)
	require.NoError(t, sessStore.Close())

	stale := strings.Repeat("0", 64)
	require.NotEqual(t, stale, created.Revision, "instrument check: the revision must really be stale")
	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+stale, nil)
	delR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(delW, delR)

	assert.Equal(t, http.StatusConflict, delW.Code, "a stale revision is still a conflict: %s", delW.Body.String())
	_, statErr := os.Stat(filepath.Join(api.homePath, "sessions", meta.ID))
	assert.False(t, errors.Is(statErr, os.ErrNotExist), "the agent's session must NOT be deleted by a refused delete")
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	getR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(getW, getR)
	assert.Equal(t, http.StatusOK, getW.Code, "the agent must still be there")

	// Control: the current revision still deletes, and then cleans the session.
	okW := httptest.NewRecorder()
	okR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+created.Revision, nil)
	okR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(okW, okR)
	assert.Equal(t, http.StatusOK, okW.Code, okW.Body.String())
	_, statErr = os.Stat(filepath.Join(api.homePath, "sessions", meta.ID))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "a valid delete still cleans the session")
}
