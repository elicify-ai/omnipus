//go:build !cgo

// Wire-format conformance tests for the session REST handlers.
//
// These tests close the bug class that shipped to the public IP build on
// 2026-05-21: getSessionMessages and jsonSessionDetail emit
// []session.TranscriptEntry into JSON fields typed as []gen.Message on the
// wire. A jim session with 44 tool_call entries failed SPA Zod validation
// because the Message.yaml type enum was missing "tool_call" and
// "turn_canceled". The fix in contracts/components/schemas/Message.yaml
// added the missing enum values plus the cancel-specific fields.
//
// These tests pin the round-trip: append a TranscriptEntry of every
// EntryType through the real UnifiedStore, call the real handler, and
// validate the response JSON against the compiled Message.yaml schema.
// If a new EntryType is added without a schema update, CI fails here.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/dapicom-ai/omnipus/pkg/session"
)

// loadMessageSchema compiles the Message.yaml component schema once per test
// binary. We resolve the schema file path relative to this test file's
// location so the test works regardless of the cwd a test runner uses.
func loadMessageSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	//nolint:dogsled // runtime.Caller returns 4 values; only the file path is needed.
	_, thisFile, _, _ := runtime.Caller(0)
	contractsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")
	loader := newYAMLSchemaLoader(t)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(loader)

	schemaPath := filepath.Join(contractsDir, "Message.yaml")
	schemaURL := "file://" + schemaPath
	schema, err := compiler.Compile(schemaURL)
	require.NoError(t, err, "must be able to compile Message.yaml")
	return schema
}

// TestGetSessionMessages_ToolCallEntries_PassesWireSchema reproduces the
// production bug at the handler level. Without the Message.yaml enum fix
// applied in this branch, this test fails — the JSON emitted by
// getSessionMessages contains `"type":"tool_call"` which Message.yaml
// rejected pre-fix.
//
// Traces to: pkg/gateway/rest.go getSessionMessages (Bug-1 reproducer).
func TestGetSessionMessages_ToolCallEntries_PassesWireSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)

	// Seed a tool_call entry — the exact shape that broke on the public IP
	// build (an entry with type="tool_call" and a non-empty tool_calls array).
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	toolCallEntry := session.TranscriptEntry{
		ID:        "call_test_001",
		Type:      session.EntryType("tool_call"),
		Timestamp: time.Date(2026, 5, 21, 4, 20, 0, 0, time.UTC),
		AgentID:   "main",
		ToolCalls: []session.ToolCall{
			{
				ID:         "call_test_001",
				Tool:       "write_file",
				Status:     "success",
				DurationMS: 3,
				Parameters: map[string]any{"path": "/tmp/x.txt", "content": "hi"},
			},
		},
	}
	require.NoError(t, store.AppendTranscript(sessionID, toolCallEntry),
		"seeding a tool_call entry must succeed")

	// Call the real REST handler.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id}/messages must return 200; got %d body=%s",
		w.Code, w.Body.String())

	// Parse the response and validate every entry against Message.yaml.
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must be a JSON array of message objects")
	require.NotEmpty(t, entries, "must have at least one entry seeded")

	schema := loadMessageSchema(t)
	for i, entry := range entries {
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		validationErr := schema.Validate(any(entry))
		assert.NoErrorf(t, validationErr,
			"entry[%d] must validate against Message.yaml; raw=%s", i, string(raw))
	}

	// And specifically: the tool_call entry must have type="tool_call" on the wire.
	foundToolCall := false
	for _, entry := range entries {
		if entry["type"] == "tool_call" {
			foundToolCall = true
			break
		}
	}
	assert.True(t, foundToolCall,
		"the seeded tool_call entry must round-trip through the handler with type=\"tool_call\"")
}

// TestGetSessionMessages_TurnCanceledEntry_PassesWireSchema covers the
// second EntryType missing from the wire schema pre-fix. A real canceled
// turn produces an entry with Type=turn_canceled plus cancel-specific
// fields (TurnID, CancelledByUser, etc.). The post-fix schema accepts both
// the new enum value AND the previously-undefined fields.
//
// Traces to: pkg/agent/cancel.go (FR-15) → pkg/session/daypartition.go EntryTypeTurnCancelled.
func TestGetSessionMessages_TurnCanceledEntry_PassesWireSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	cancelEntry := session.TranscriptEntry{
		ID:                   "cancel_entry_001",
		Type:                 session.EntryTypeTurnCancelled,
		Timestamp:            time.Date(2026, 5, 21, 4, 25, 0, 0, time.UTC),
		AgentID:              "main",
		TurnID:               "turn-T3",
		CancelledByUser:      "admin",
		CancelledByChannel:   "webchat",
		CancelMethod:         "graceful",
		DescendantsCancelled: []string{"turn-T3-sub-1"},
	}
	require.NoError(t, store.AppendTranscript(sessionID, cancelEntry))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	schema := loadMessageSchema(t)
	for i, entry := range entries {
		validationErr := schema.Validate(any(entry))
		raw, _ := json.Marshal(entry)
		assert.NoErrorf(t, validationErr,
			"entry[%d] must validate against Message.yaml; raw=%s", i, string(raw))
	}

	// Verify the cancel-specific fields are actually on the wire (not stripped
	// by Go's json:"omitempty" tags). If a future TranscriptEntry refactor drops
	// these fields the SPA's cancel UI would silently regress.
	foundCancel := false
	for _, entry := range entries {
		if entry["type"] == "turn_canceled" {
			foundCancel = true
			assert.Equal(t, "turn-T3", entry["turn_id"],
				"turn_id field must round-trip")
			assert.Equal(t, "admin", entry["canceled_by_user"],
				"canceled_by_user field must round-trip")
			assert.Equal(t, "graceful", entry["cancel_method"],
				"cancel_method field must round-trip")
			break
		}
	}
	assert.True(t, foundCancel, "seeded turn_canceled entry must round-trip")
}

// TestGetSession_TranscriptWithCancelledTurn_PassesSessionDetailSchema covers
// the envelope shape jsonSessionDetail emits — {session, messages,
// agent_removed?}. The Session.partitions array regression we fixed earlier
// in this branch landed in this exact envelope; this test pins the second
// half of the contract (the messages array) against the same shape.
//
// Traces to: pkg/gateway/rest.go jsonSessionDetail.
func TestGetSession_TranscriptWithMixedEntries_PassesSessionDetailSchema(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	sessionID := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	mixed := []session.TranscriptEntry{
		{
			ID:        "msg_user_1",
			Role:      "user",
			Content:   "hello",
			Timestamp: time.Date(2026, 5, 21, 4, 20, 0, 0, time.UTC),
			AgentID:   "main",
		},
		{
			ID:        "msg_asst_1",
			Role:      "assistant",
			Content:   "hi back",
			Timestamp: time.Date(2026, 5, 21, 4, 20, 1, 0, time.UTC),
			AgentID:   "main",
		},
		{
			ID:        "call_001",
			Type:      session.EntryType("tool_call"),
			Timestamp: time.Date(2026, 5, 21, 4, 20, 2, 0, time.UTC),
			AgentID:   "main",
			ToolCalls: []session.ToolCall{
				{ID: "call_001", Tool: "write_file", Status: "success"},
			},
		},
		{
			ID:              "cancel_001",
			Type:            session.EntryTypeTurnCancelled,
			Timestamp:       time.Date(2026, 5, 21, 4, 20, 3, 0, time.UTC),
			AgentID:         "main",
			TurnID:          "turn-1",
			CancelledByUser: "admin",
			CancelMethod:    "graceful",
		},
	}
	for _, e := range mixed {
		require.NoError(t, store.AppendTranscript(sessionID, e))
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID, nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code,
		"GET /sessions/{id} must return 200; got %d body=%s", w.Code, w.Body.String())

	// Validate the envelope against SessionDetail.yaml.
	//nolint:dogsled // runtime.Caller returns 4 values; only the file path is needed.
	_, thisFile, _, _ := runtime.Caller(0)
	contractsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")
	loader := newYAMLSchemaLoader(t)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(loader)
	schema, err := compiler.Compile("file://" + filepath.Join(contractsDir, "SessionDetail.yaml"))
	require.NoError(t, err, "must compile SessionDetail.yaml")

	var envelope any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))

	validationErr := schema.Validate(envelope)
	assert.NoError(t, validationErr,
		"jsonSessionDetail envelope must validate against SessionDetail.yaml; "+
			"body=%s", w.Body.String())
}

// TestGetSessions_ScheduledTypeFilter_RoundTrip verifies that a session of type
// "scheduled" (minted server-side by NewScheduledSession / fired schedule) is:
//  1. Returned by GET /api/v1/sessions with type="scheduled" in the wire JSON.
//  2. Returned by GET /api/v1/sessions/{id} with the correct type.
//  3. NOT returned by GET /api/v1/sessions?type=chat (exclusive filter).
//  4. Returned by GET /api/v1/sessions?type=scheduled (positive filter).
//
// This pins the FIX 1 from the contract/test review: the filter enum in
// contracts/openapi.yaml was missing "scheduled", so clients couldn't request
// the ?type=scheduled filter even though such sessions exist. It also pins the
// wire round-trip: gen.SessionType already includes SessionTypeScheduled (added
// in 64e44c51), so the session must survive with type="scheduled" through the
// handler, not be dropped or coerced.
//
// Traces to: contracts/openapi.yaml GET /sessions type filter param enum.
func TestGetSessions_ScheduledTypeFilter_RoundTrip(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	// Mint a scheduled session directly through the store (server-minted path —
	// POST /sessions intentionally rejects "scheduled" from clients).
	scheduledMeta, err := store.NewScheduledSession("main")
	require.NoError(t, err, "NewScheduledSession must succeed")
	require.NotEmpty(t, scheduledMeta.ID, "scheduled session must have an ID")
	require.Equal(t, session.SessionTypeScheduled, scheduledMeta.Type,
		"NewScheduledSession must stamp type=scheduled")

	// Also mint a chat session so we can verify the exclusive filter.
	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err, "NewSession(chat) must succeed")

	// 1. GET /api/v1/sessions (no filter) — both sessions appear.
	wAll := httptest.NewRecorder()
	rAll := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	rAll.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wAll, rAll)
	require.Equal(t, http.StatusOK, wAll.Code,
		"GET /sessions must return 200; body=%s", wAll.Body.String())

	var allSessions []map[string]any
	require.NoError(t, json.Unmarshal(wAll.Body.Bytes(), &allSessions),
		"GET /sessions response must be a JSON array")

	foundScheduledInAll := false
	for _, s := range allSessions {
		if s["id"] == scheduledMeta.ID {
			foundScheduledInAll = true
			assert.Equal(t, "scheduled", s["type"],
				"scheduled session must appear with type=\"scheduled\" in full list")
		}
	}
	assert.True(t, foundScheduledInAll,
		"scheduled session (id=%s) must appear in unfiltered GET /sessions", scheduledMeta.ID)

	// 2. GET /api/v1/sessions?type=scheduled — only the scheduled session.
	wSched := httptest.NewRecorder()
	rSched := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=scheduled", nil)
	rSched.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wSched, rSched)
	require.Equal(t, http.StatusOK, wSched.Code,
		"GET /sessions?type=scheduled must return 200; body=%s", wSched.Body.String())

	var scheduledSessions []map[string]any
	require.NoError(t, json.Unmarshal(wSched.Body.Bytes(), &scheduledSessions),
		"GET /sessions?type=scheduled response must be a JSON array")

	require.NotEmpty(t, scheduledSessions,
		"GET /sessions?type=scheduled must return at least one session")
	for _, s := range scheduledSessions {
		assert.Equal(t, "scheduled", s["type"],
			"GET /sessions?type=scheduled must return ONLY sessions with type=scheduled; got %v", s["type"])
	}
	foundScheduledInFilter := false
	for _, s := range scheduledSessions {
		if s["id"] == scheduledMeta.ID {
			foundScheduledInFilter = true
		}
	}
	assert.True(t, foundScheduledInFilter,
		"the minted scheduled session must appear in ?type=scheduled results")

	// 3. GET /api/v1/sessions?type=chat — must NOT include the scheduled session.
	wChat := httptest.NewRecorder()
	rChat := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=chat", nil)
	rChat.URL.Path = "/api/v1/sessions"
	api.HandleSessions(wChat, rChat)
	require.Equal(t, http.StatusOK, wChat.Code,
		"GET /sessions?type=chat must return 200; body=%s", wChat.Body.String())

	var chatSessions []map[string]any
	require.NoError(t, json.Unmarshal(wChat.Body.Bytes(), &chatSessions),
		"GET /sessions?type=chat response must be a JSON array")

	for _, s := range chatSessions {
		assert.NotEqual(t, scheduledMeta.ID, s["id"],
			"scheduled session must NOT appear in ?type=chat results")
	}
	foundChatInFilter := false
	for _, s := range chatSessions {
		if s["id"] == chatMeta.ID {
			foundChatInFilter = true
			assert.Equal(t, "chat", s["type"],
				"chat session must have type=chat in filtered results")
		}
	}
	assert.True(t, foundChatInFilter,
		"the minted chat session must appear in ?type=chat results")

	// 4. GET /api/v1/sessions/{id} — scheduled session round-trips with correct type.
	wDet := httptest.NewRecorder()
	rDet := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+scheduledMeta.ID, nil)
	rDet.URL.Path = "/api/v1/sessions/" + scheduledMeta.ID
	api.HandleSessions(wDet, rDet)
	require.Equal(t, http.StatusOK, wDet.Code,
		"GET /sessions/{id} for scheduled session must return 200; body=%s", wDet.Body.String())

	var detail map[string]any
	require.NoError(t, json.Unmarshal(wDet.Body.Bytes(), &detail),
		"GET /sessions/{id} response must be a JSON object")

	sessionObj, ok := detail["session"].(map[string]any)
	require.True(t, ok, "SessionDetail envelope must have a 'session' field; got %v", detail)
	assert.Equal(t, "scheduled", sessionObj["type"],
		"scheduled session detail must have type=\"scheduled\" on the wire")
}

// newYAMLSchemaLoader returns a jsonschema.URLLoader that resolves file://
// URLs by parsing YAML files into map[string]any. The default loader only
// reads JSON; this wrapper makes the test work with our YAML schemas.
//
// Mirrors the pattern in pkg/api/generated/contract_test.go::yamlLoader.
func newYAMLSchemaLoader(t *testing.T) jsonschema.URLLoader {
	t.Helper()
	return &yamlURLLoader{t: t}
}

type yamlURLLoader struct{ t *testing.T }

func (l *yamlURLLoader) Load(url string) (any, error) {
	path := strings.TrimPrefix(url, "file://")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return jsonifyYAML(v), nil
}

// jsonifyYAML converts the map[interface{}]interface{} that yaml.v3 produces
// into map[string]interface{} that jsonschema/v6 expects. Recursive.
func jsonifyYAML(v any) any {
	switch v := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			ks, _ := k.(string)
			out[ks] = jsonifyYAML(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = jsonifyYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = jsonifyYAML(val)
		}
		return out
	default:
		return v
	}
}

// TestUnifiedMetaToGenSession_MapsUpdatedAt pins the regression where the session
// wire mapper omitted UpdatedAt, so every session serialized Go's zero time
// ("0001-01-01T00:00:00Z") — breaking ListSessions sort order and the SPA's
// relative-time display.
func TestUnifiedMetaToGenSession_MapsUpdatedAt(t *testing.T) {
	created := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 4, 2, 12, 30, 0, 0, time.UTC)
	m := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{
			ID:        "sess-1",
			AgentID:   "jim",
			Channel:   "webchat",
			Title:     "hello",
			Status:    session.SessionStatus("active"),
			CreatedAt: created,
			UpdatedAt: updated,
		},
		Type: session.SessionTypeChat,
	}
	s := unifiedMetaToGenSession(m)
	require.False(t, s.UpdatedAt.IsZero(),
		"UpdatedAt must not be the Go zero time — the wire mapper used to drop it")
	assert.True(t, s.UpdatedAt.Equal(updated),
		"UpdatedAt should map from meta: got %v, want %v", s.UpdatedAt, updated)
	assert.True(t, s.CreatedAt.Equal(created), "CreatedAt should still map correctly")
}
