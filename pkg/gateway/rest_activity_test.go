// rest_activity_test.go — whole-codebase-review Backend-High finding #1:
// HandleActivity computed a partial-failure session-listing warning but only
// slog'd it, then always returned the bare events array — discarding the
// warning entirely. The fix returns gen.ActivityEventsResponse ({events,
// warning?}) instead of a bare array, only setting Warning when non-empty.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestHandleActivity_ReturnsWrappedResponseNoWarning verifies GET
// /api/v1/activity returns the wire-contract ActivityEventsResponse shape
// ({"events": [...]}) — not a bare JSON array — when there is nothing to warn
// about, and that the warning key is absent (contracts/components/schemas/
// ActivityEventsResponse.yaml: warning is optional).
func TestHandleActivity_ReturnsWrappedResponseNoWarning(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
	api.HandleActivity(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	events, hasEvents := resp["events"]
	require.True(t, hasEvents,
		"response must be the ActivityEventsResponse object shape ({\"events\": [...]}), not a bare array")
	assert.NotNil(t, events, "events must be a non-null (possibly empty) array")
	_, hasWarning := resp["warning"]
	assert.False(t, hasWarning, "warning key must be absent when there is nothing to warn about")
}

// TestHandleActivity_PartialFailure_SurfacesWarning verifies the Backend-High
// fix: when session listing partially fails for one agent, the warning
// computed internally (sessionWarning in HandleActivity) is returned to the
// caller via ActivityEventsResponse.Warning instead of being discarded after
// only a slog.Warn call.
func TestHandleActivity_PartialFailure_SurfacesWarning(t *testing.T) {
	if os.Getuid() == 0 {
		// Root bypasses DAC, so chmod 0o000 does not prevent os.ReadDir. This
		// mirrors pkg/agent/list_all_sessions_test.go's TestListAllSessions_PartialErrors.
		t.Skip("permission-based failure injection is ineffective under root; run as non-root")
	}
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "agent-broken", Name: "Broken Agent", Home: tmpDir},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Wire a broken UnifiedStore for "agent-broken": create the store then
	// remove all read permission on its base dir so ListSessions fails,
	// forcing ListAllSessions to return a partial error for this agent.
	brokenBaseDir := t.TempDir()
	brokenStore, err := session.NewUnifiedStore(brokenBaseDir)
	require.NoError(t, err, "NewUnifiedStore(agent-broken)")
	require.NoError(t, os.Chmod(brokenBaseDir, 0o000), "chmod brokenBaseDir")
	t.Cleanup(func() { _ = os.Chmod(brokenBaseDir, 0o700) }) // restore for temp-dir cleanup

	brokenAgent, ok := al.GetRegistry().GetAgent("agent-broken")
	require.True(t, ok, "agent-broken must be registered")
	brokenAgent.Sessions = brokenStore

	api := &restAPI{
		agentLoop: al,
		taskStore: task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/activity", nil)
	api.HandleActivity(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"a partial session-listing failure must not fail the whole request; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	warning, _ := resp["warning"].(string)
	assert.NotEmpty(t, warning,
		"warning must be surfaced to the caller (previously discarded after only a slog.Warn call)")
	assert.Contains(t, warning, "agent-broken", "warning should identify the affected agent")
	_, hasEvents := resp["events"]
	assert.True(t, hasEvents, "events key must still be present alongside the warning")
}
