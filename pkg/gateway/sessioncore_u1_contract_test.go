// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: the U1 slice of BDD-12.3 / T14 "current generated
// contract controls" for the main-session shapes (C-MAIN).
//
// Driven through the REAL embedded schema validator
// (rest_inbound_validate.go::validateBodyAgainstSchema over pkg/gateway/
// inboundschemas, the synced copy of contracts/components/schemas) and the real
// REST handlers. Each rejection test has a baseline-accepting control built from
// the same document, so an unrelated rejection cannot mask the intended bound
// ("correct baseline control first").

package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u1SessionDoc returns a complete, otherwise-valid Session document.
func u1SessionDoc(typ any) map[string]any {
	doc := map[string]any{
		"id":           "main-session-W1+mia",
		"agent_id":     "mia",
		"workspace_id": "W1",
		"title":        "Mia",
		"status":       "active",
		"protected":    true,
		"created_at":   "2026-10-08T00:00:00Z",
		"updated_at":   "2026-10-08T00:00:00Z",
		"channel":      "",
		"partitions":   []string{},
		"stats": map[string]any{
			"tokens_in": 0, "tokens_out": 0, "tokens_total": 0, "cost": 0, "tool_calls": 0, "message_count": 0,
		},
	}
	if typ != nil {
		doc["type"] = typ
	}
	return doc
}

func u1Validate(t *testing.T, schema string, doc any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	msg, serverErr := validateBodyAgainstSchema(schema, raw)
	require.False(t, serverErr, "schema %s must compile", schema)
	return msg
}

// Control (baseline-passing): the validator accepts a complete current chat Session,
// so the rejections below are about the field under test.
func TestSessionCoreU1_Control_SessionSchemaAcceptsACompleteChatSession(t *testing.T) {
	assert.Empty(t, u1Validate(t, "Session", u1SessionDoc("chat")), "baseline control")
}

// C-MAIN / BDD-12.3: server-only type "main" is a valid current Session.
func TestSessionCoreU1_SessionSchemaAcceptsTheServerMintedMainShape(t *testing.T) {
	assert.Empty(t, u1Validate(t, "Session", u1SessionDoc("main")),
		"C-MAIN: a valid main Session (type=main, agent_id, workspace_id, protected) must validate")
}

// C-MAIN / BDD-12.3: Session.type is required (absent type refused, never defaulted to chat).
func TestSessionCoreU1_SessionSchemaRefusesAnAbsentType(t *testing.T) {
	assert.NotEmpty(t, u1Validate(t, "Session", u1SessionDoc(nil)),
		"C-MAIN/DEL-F07: a Session without type must be refused")
}

// C-MAIN / DEL-11: Session.active_agent_id is deleted from the contract.
func TestSessionCoreU1_SessionSchemaRefusesActiveAgentID(t *testing.T) {
	doc := u1SessionDoc("chat")
	doc["active_agent_id"] = "mia"
	assert.NotEmpty(t, u1Validate(t, "Session", doc), "C-MAIN: active_agent_id is deleted")
}

// C-MAIN: WorkspaceMemberConfig gains readOnly main_session_id.
func TestSessionCoreU1_MemberConfigSchemaAcceptsMainSessionID(t *testing.T) {
	assert.Empty(t, u1Validate(t, "WorkspaceMemberConfig", map[string]any{"main_session_id": "main-session-W1+mia"}),
		"C-MAIN: main_session_id is part of WorkspaceMemberConfig")
}

// Control (baseline-passing): a current heartbeat-only member config validates.
func TestSessionCoreU1_Control_MemberConfigSchemaAcceptsHeartbeatSettings(t *testing.T) {
	doc := map[string]any{"heartbeat": map[string]any{"enabled": true, "interval_minutes": 10, "body": "Check."}}
	assert.Empty(t, u1Validate(t, "WorkspaceMemberConfig", doc), "baseline control")
}

// C-MAIN / DEL-01: heartbeat.session_id is deleted from the contract.
func TestSessionCoreU1_HeartbeatSchemaRefusesSessionID(t *testing.T) {
	doc := map[string]any{"enabled": true, "interval_minutes": 10, "body": "Check.", "session_id": "x"}
	assert.NotEmpty(t, u1Validate(t, "WorkspaceMemberHeartbeat", doc), "C-MAIN: heartbeat.session_id is deleted")
}

// Controls (baseline-passing) + C-MAIN: client create enum stays narrow. A
// client can never create main (nor any other server-minted type).
func TestSessionCoreU1_Control_ClientCreateSchemaRefusesServerOnlyTypes(t *testing.T) {
	assert.Empty(t, u1Validate(t, "SessionCreateRequest", map[string]any{"type": "chat", "agent_id": "mia"}),
		"control: chat is client-creatable")
	for _, typ := range []string{"main", "scheduled", "heartbeat", "verifier", "delegate"} {
		assert.NotEmpty(t, u1Validate(t, "SessionCreateRequest", map[string]any{"type": typ, "agent_id": "mia"}),
			"server-minted type %q must not be client-creatable", typ)
	}
}

// Control (baseline-passing, FR-002/BDD-12.3 "client-created main refused"):
// the real handler with inbound validation ON refuses type=main and persists nothing.
func TestSessionCoreU1_Control_ClientPostOfMainIsRefusedAndStoresNothing(t *testing.T) {
	env := u1NewEnv(t, true)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", "application/json")
		env.api.createSessionHTTP(w, r)
		return w
	}
	ok := post(`{"type":"chat","agent_id":"mia"}`)
	require.Equal(t, http.StatusCreated, ok.Code, "control: a chat can be created: %s", ok.Body.String())
	before := len(env.storedSessions(t))

	bad := post(`{"type":"main","agent_id":"mia","workspace_id":"W1"}`)
	assert.Equal(t, http.StatusBadRequest, bad.Code, "type=main is refused: %s", bad.Body.String())
	assert.Equal(t, before, len(env.storedSessions(t)), "refusal stores nothing")
	assert.Empty(t, env.storedOfType(t, "main"))
}

// DEL-11 / DEL-01 on the live wire (T14 consumer side): real list/detail rows of
// a chat and of a main carry no active_agent_id, validate against the real
// Session schema, and the workspace wire carries no heartbeat.session_id.
func TestSessionCoreU1_LiveSessionRowsValidateAndCarryNoRetiredFields(t *testing.T) {
	env := u1NewEnv(t, true)
	const ws = "01JU1WIREWS00000000000000J"
	env.seedWorkspace(t, ws, false, "jim")
	require.Equal(t, http.StatusOK, env.putTeam(t, ws, "jim", "mia").Code, "control: add Mia")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader([]byte(`{"agent_id":"mia"}`)))
	r.Header.Set("Content-Type", "application/json")
	env.api.createSessionHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "control: extra chat: %s", w.Body.String())
	var chat map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &chat))
	chatID, _ := chat["id"].(string)
	require.NotEmpty(t, chatID)

	mainID := u1MainID(ws, "mia")
	rows := env.listSessions(t, "")
	for _, id := range []string{chatID, mainID} {
		row := u1Row(rows, id)
		require.NotNil(t, row, "%s listed", id)
		_, has := row["active_agent_id"]
		assert.False(t, has, "%s: list row must not carry active_agent_id", id)
		assert.Empty(t, u1Validate(t, "Session", row), "%s: list row must validate against the current Session schema", id)

		detail, code := env.getSession(t, id)
		require.Equal(t, http.StatusOK, code)
		_, has = detail["active_agent_id"]
		assert.False(t, has, "%s: detail must not carry active_agent_id", id)
		assert.Empty(t, u1Validate(t, "Session", detail), "%s: detail must validate", id)
	}
	assert.Equal(t, "main", u1Row(rows, mainID)["type"], "the main row is typed main")

	hb := map[string]any{"member_configs": map[string]any{"mia": map[string]any{
		"heartbeat": map[string]any{"enabled": true, "interval_minutes": 10, "body": "Check."}}}}
	require.Equal(t, http.StatusOK, env.put(t, ws, hb).Code, "control: enable heartbeat")
	mc := u1MemberConfig(env.getWorkspace(t, ws), "mia")
	require.NotNil(t, mc)
	heartbeat, _ := mc["heartbeat"].(map[string]any)
	require.NotNil(t, heartbeat, "control: heartbeat settings are returned")
	assert.Equal(t, true, heartbeat["enabled"], "control: enabled round-trips")
	_, has := heartbeat["session_id"]
	assert.False(t, has, "C-MAIN: heartbeat.session_id is gone from the workspace wire")
	assert.Empty(t, u1Validate(t, "WorkspaceMemberConfig", mc), "member config validates against the current schema")

	// C1 CHECK survivor m19r: the wire projection can conceal a retired heartbeat
	// address that REMAINS in the persisted workspace settings. Assert on the
	// PERSISTED record on disk, not only the wire: no "session_id" key under the
	// member's heartbeat, whatever the wire shows.
	persisted := env.u1PersistedWorkspace(t, ws)
	pMCs, _ := persisted["member_configs"].(map[string]any)
	pMC, _ := pMCs["mia"].(map[string]any)
	require.NotNil(t, pMC, "persisted member config for mia must exist: %v", persisted)
	pHB, _ := pMC["heartbeat"].(map[string]any)
	require.NotNil(t, pHB, "persisted heartbeat settings for mia must exist: %v", pMC)
	_, persistedHas := pHB["session_id"]
	assert.False(t, persistedHas,
		"C-MAIN/DEL-01: heartbeat.session_id must not be persisted in the workspace record: %v", pHB)
}
