// Package gateway — Spec-1 regression test for issue #358.
//
// #358 (FR-111): enabling a channel via PUT /api/v1/channels/{id}/enable must
// trigger a config reload so ChannelManager.Reload starts the newly-enabled
// channel (e.g. whatsapp_native, which then emits its pairing QR over the
// whatsapp_pairing WS frame). Before the fix, setChannelEnabled only persisted
// the flag + swapped the in-memory config pointer and never reloaded, so the
// channel never started and the QR never appeared.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEnableReloadTestAPI builds a restAPI backed by a real AgentLoop and an
// on-disk config.json, returning the api plus a reload-call counter wired via
// SetReloadFunc. The reloadFn argument lets a test simulate reload success/failure.
func newEnableReloadTestAPI(t *testing.T, reloadFn func() error) (*restAPI, *int32) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")

	tmpDir := t.TempDir()
	cfgData := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "claude-sonnet-4-6"},
			"list":     []any{},
		},
		"channels": map[string]any{
			"whatsapp": map[string]any{"enabled": false},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "claude-sonnet-4-6"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	var reloadCalls int32
	al.SetReloadFunc(func() error {
		atomic.AddInt32(&reloadCalls, 1)
		if reloadFn != nil {
			return reloadFn()
		}
		return nil
	})

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
	}
	return api, &reloadCalls
}

// TestSetChannelDisabled_TriggersReload confirms the same wiring applies to the
// disable path (so a stopped channel is actually torn down on disable).
func TestSetChannelDisabled_TriggersReload(t *testing.T) {
	api, reloadCalls := newEnableReloadTestAPI(t, nil)

	w := httptest.NewRecorder()
	api.setChannelEnabled(w, "whatsapp", false)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(1), atomic.LoadInt32(reloadCalls),
		"disabling a channel must also trigger a reload so the channel is stopped")
}
