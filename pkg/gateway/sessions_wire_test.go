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

// validateMessage validates a single JSON-marshaled message against the
// Message.yaml schema. The schema is loaded once per test via the closure.
func validateMessage(t *testing.T, schema *jsonschema.Schema, raw []byte) error {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return schema.Validate(v)
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
// second EntryType missing from the wire schema pre-fix. A real cancelled
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
