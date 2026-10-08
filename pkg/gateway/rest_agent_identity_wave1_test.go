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

// Wire oracles: ARCH-DECISIONS 1.2 and 1.4.
// Create defaults: figure Omnipus, role general, colour #9CA3AF.
// A non-enum figure or role, or a hex outside the ten, is 400 with no write.
// Omitted on update means unchanged. A listed hex is stored uppercase.
// GET/list emit the defaults when the stored value is empty and do not write.

func TestAgentIdentity_CreateDefaultsOmnipusGeneralGrey(t *testing.T) {
	api := buildExecutorTestAPI(t)
	rec := postAgent(t, api, `{"name":"Plain Colleague","type":"Main","soul":"plain-soul"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	body := decodeObject(t, rec.Body.Bytes())
	assert.Equal(t, "Omnipus", body["figure"])
	assert.Equal(t, "general", body["role"])
	assert.Equal(t, "#9CA3AF", body["color"])

	id, idOK := body["id"].(string)
	require.True(t, idOK, "create response id must be a string, got %#v", body["id"])
	saved := savedAgent(t, api, id)
	assert.Equal(t, "Omnipus", saved["figure"])
	assert.Equal(t, "general", saved["role"])
	assert.Equal(t, "#9CA3AF", saved["color"])
}

func TestAgentIdentity_CreateRejectsNonEnumAndNonPalette(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "art key is not a figure", body: `{"name":"Bad Figure","type":"Main","soul":"s","figure":"octopus"}`, want: "figure"},
		{name: "display label is not a role slug", body: `{"name":"Bad Role","type":"Main","soul":"s","role":"Developer"}`, want: "role"},
		{name: "old brand hex is not in the palette", body: `{"name":"Bad Color","type":"Main","soul":"s","color":"#D4AF37"}`, want: "color"},
		{name: "arbitrary hex", body: `{"name":"Bad Color 2","type":"Main","soul":"s","color":"#ff0000"}`, want: "color"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			before := agentNameSet(t, api)
			rec := postAgent(t, api, tc.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			msg := strings.ToLower(rec.Body.String())
			assert.NotContains(t, msg, "unknown field", "enum rejection must not be the pre-contract unknown-field 400")
			assert.Contains(t, msg, tc.want)
			assert.Equal(t, before, agentNameSet(t, api), "400 must not persist a partial agent")
		})
	}
}

func TestAgentIdentity_CreateNormalisesHexAndKeepsIcon(t *testing.T) {
	api := buildExecutorTestAPI(t)
	rec := postAgent(t, api, `{
		"name":"Chosen",
		"type":"Main",
		"soul":"chosen-soul",
		"figure":"Woman",
		"role":"writer",
		"color":"#fb923c",
		"icon":"magnifying-glass"
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decodeObject(t, rec.Body.Bytes())
	assert.Equal(t, "Woman", body["figure"])
	assert.Equal(t, "writer", body["role"])
	assert.Equal(t, "#FB923C", body["color"])
	assert.Equal(t, "magnifying-glass", body["icon"], "legacy icon is stored as sent; the hyphenated name is not a role")

	id, idOK := body["id"].(string)
	require.True(t, idOK, "create response id must be a string, got %#v", body["id"])
	saved := savedAgent(t, api, id)
	assert.Equal(t, "Woman", saved["figure"])
	assert.Equal(t, "writer", saved["role"])
	assert.Equal(t, "#FB923C", saved["color"])
	assert.Equal(t, "magnifying-glass", saved["icon"])
}

func TestAgentIdentity_UpdateOmittedLeavesStoredAndRejectsBadValues(t *testing.T) {
	api := buildExecutorTestAPI(t)
	created := postAgent(t, api, `{
		"name":"Editable",
		"type":"Main",
		"soul":"editable-soul",
		"figure":"Man",
		"role":"developer",
		"color":"#3B82F6",
		"icon":"magnifying-glass"
	}`)
	require.Equal(t, http.StatusCreated, created.Code, "body: %s", created.Body.String())
	createdBody := decodeObject(t, created.Body.Bytes())
	id, idOK := createdBody["id"].(string)
	require.True(t, idOK, "create response id must be a string, got %#v", createdBody["id"])

	omit := putAgent(t, api, id, `{"description":"renamed only"}`)
	require.Equal(t, http.StatusOK, omit.Code, "body: %s", omit.Body.String())
	saved := savedAgent(t, api, id)
	assert.Equal(t, "Man", saved["figure"], "omitted figure stays")
	assert.Equal(t, "developer", saved["role"], "omitted role stays")
	assert.Equal(t, "#3B82F6", saved["color"], "omitted colour stays")
	assert.Equal(t, "magnifying-glass", saved["icon"], "a presentation save must not clear icon")

	before, err := agentstore.New(api.homePath).ReadState(id)
	require.NoError(t, err)
	bad := putAgent(t, api, id, `{"figure":"octopus","role":"writer","color":"#22D3EE"}`)
	require.Equal(t, http.StatusBadRequest, bad.Code, "body: %s", bad.Body.String())
	assert.NotContains(t, strings.ToLower(bad.Body.String()), "unknown field")
	after := savedAgent(t, api, id)
	assert.Equal(t, "Man", after["figure"], "400 does not apply a later valid role or colour")
	assert.Equal(t, "developer", after["role"])
	assert.Equal(t, "#3B82F6", after["color"])
	state, err := agentstore.New(api.homePath).ReadState(id)
	require.NoError(t, err)
	assert.Equal(t, before.Revision, state.Revision, "rejected update is zero-write")
}

func TestAgentIdentity_UpdateNormalisesPaletteHex(t *testing.T) {
	api := buildExecutorTestAPI(t)
	rec := putAgent(t, api, "test-agent", `{"color":"#3b82f6"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	saved := savedAgent(t, api, "test-agent")
	assert.Equal(t, "#3B82F6", saved["color"])

	nonPalette := putAgent(t, api, "test-agent", `{"color":"#123456"}`)
	require.Equal(t, http.StatusBadRequest, nonPalette.Code, "body: %s", nonPalette.Body.String())
	assert.NotContains(t, strings.ToLower(nonPalette.Body.String()), "unknown field")
	assert.Equal(t, "#3B82F6", savedAgent(t, api, "test-agent")["color"], "rejected hex must not replace the stored palette colour")
}

func TestAgentIdentity_ListAndGetEmitDefaultsWithoutWriting(t *testing.T) {
	api := buildExecutorTestAPI(t)
	before := savedAgent(t, api, "test-agent")
	_, hadFigure := before["figure"]
	_, hadRole := before["role"]
	_, hadColor := before["color"]
	require.False(t, hadFigure, "fixture stores no figure")
	require.False(t, hadRole, "fixture stores no role")
	require.False(t, hadColor, "fixture stores no colour")

	got := getAgentObject(t, api, "test-agent")
	assert.Equal(t, "Omnipus", got["figure"])
	assert.Equal(t, "general", got["role"])
	assert.Equal(t, "#9CA3AF", got["color"])

	listed := findListedAgent(t, api, "test-agent")
	assert.Equal(t, "Omnipus", listed["figure"])
	assert.Equal(t, "general", listed["role"])
	assert.Equal(t, "#9CA3AF", listed["color"])

	after := savedAgent(t, api, "test-agent")
	_, hadFigure = after["figure"]
	_, hadRole = after["role"]
	_, hadColor = after["color"]
	assert.False(t, hadFigure, "GET must not persist the figure default")
	assert.False(t, hadRole, "GET must not persist the role default")
	assert.False(t, hadColor, "GET must not persist the colour default")
}

func TestAgentIdentity_HiddenBuiltinRejectsFigureAndRole(t *testing.T) {
	api := newSeededJudgeAPI(t)
	for _, body := range []string{`{"figure":"Robot"}`, `{"role":"security"}`} {
		t.Run(body, func(t *testing.T) {
			rec := putAgent(t, api, "judge", body)
			require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
			msg := strings.ToLower(rec.Body.String())
			assert.Contains(t, msg, "protected_field")
			assert.NotContains(t, msg, "unknown field")
		})
	}
}

func putAgent(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	api.HandleAgents(rec, req)
	return rec
}

func getAgentObject(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	api.HandleAgents(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	return decodeObject(t, rec.Body.Bytes())
}

func findListedAgent(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows), "body: %s", rec.Body.String())
	for _, row := range rows {
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("agent %s not in list", id)
	return nil
}

func agentNameSet(t *testing.T, api *restAPI) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rows))
	out := map[string]bool{}
	for _, row := range rows {
		name, _ := row["name"].(string)
		out[name] = true
	}
	return out
}

func savedAgent(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	ag, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)
	raw, err := json.Marshal(ag)
	require.NoError(t, err)
	return decodeObject(t, raw)
}

func decodeObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out), "json: %s", raw)
	return out
}
