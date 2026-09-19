package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Expectations: ADR-090 FR-002 — voice is a supported editable persona field
// on custom/ordinary Main agents. It must persist and read back even though
// TTS playback is inactive. Workers reject a non-empty voice with zero write.

func TestUpdateAgent_VoicePersistsAndReadsBackOnCustomAgent(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"voice":"alloy"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var putResp struct {
		Voice         *string  `json:"voice"`
		ChangedFields []string `json:"changed_fields"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	require.NotNil(t, putResp.Voice, "PUT must echo the persisted voice, not omit it")
	assert.Equal(t, "alloy", *putResp.Voice)
	assert.Contains(t, putResp.ChangedFields, "voice")

	saved, err := agentstore.New(api.homePath).Get("test-agent")
	require.NoError(t, err)
	assert.Equal(t, "alloy", saved.Voice, "entity record must persist voice")

	wGet := httptest.NewRecorder()
	api.getAgent(wGet, "test-agent")
	require.Equal(t, http.StatusOK, wGet.Code, "GET body: %s", wGet.Body.String())
	var getResp struct {
		Voice *string `json:"voice"`
	}
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	require.NotNil(t, getResp.Voice, "GET must echo the persisted voice")
	assert.Equal(t, "alloy", *getResp.Voice)
}

func TestUpdateAgent_EmptyVoiceClearsPersistedValue(t *testing.T) {
	api := buildExecutorTestAPI(t)

	set := httptest.NewRecorder()
	setReq := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"voice":"alloy"}`))
	setReq.Header.Set("Content-Type", "application/json")
	api.HandleAgents(set, setReq)
	require.Equal(t, http.StatusOK, set.Code, "setup PUT body: %s", set.Body.String())

	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"voice":""}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	saved, err := agentstore.New(api.homePath).Get("test-agent")
	require.NoError(t, err)
	assert.Equal(t, "", saved.Voice, "empty string must clear the stored voice")
}

func TestUpdateAgent_RejectsVoiceOnWorkerWithZeroWrite(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)
	before, err := agentstore.New(api.homePath).Get("test-worker")
	require.NoError(t, err)
	require.Equal(t, "", before.Voice)

	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker",
		strings.NewReader(`{"voice":"alloy"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "voice")

	after, err := agentstore.New(api.homePath).Get("test-worker")
	require.NoError(t, err)
	assert.Equal(t, before.Voice, after.Voice, "rejected worker voice must not write")
	assert.Equal(t, before.Name, after.Name)
}
