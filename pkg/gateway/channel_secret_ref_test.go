package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLockedStoreChannelAPI builds a channel API whose credential store is
// present but NOT unlocked, so storeCredential / credentialRefResolves return
// ErrStoreLocked — used to exercise the store-fault paths deterministically.
func newLockedStoreChannelAPI(t *testing.T, configJSON string) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(configJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		credStore:     credentials.NewStore(tmpDir + "/credentials.json"), // locked
	}
}

func newChannelTestAPI(t *testing.T, configJSON string) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(configJSON), 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return newOnboardingTestAPI(t, tmpDir, al)
}

// mustJSONString returns s encoded as a JSON string literal (with quotes).
func mustJSONString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

// TestChannels_EmailIsUnknown guards the M11 retirement of the legacy
// /channels/email/* surface: email is NOT a channel (config.knownChannelTypes
// deliberately excludes it; the SPA uses /agents/{id}/mailbox exclusively).
// validChannelIDs, channelSensitiveFields, and channelRequiredFields must not
// carry an "email" entry — the routing gate in HandleChannels must 404 it as
// an unknown channel type, not treat it as a valid-but-misconfigured channel.
func TestChannels_EmailIsUnknown(t *testing.T) {
	assert.False(t, validChannelIDs["email"], "email must not be a valid channel ID")
	_, hasSensitive := channelSensitiveFields["email"]
	assert.False(t, hasSensitive, "channelSensitiveFields must not carry a legacy email entry")
	_, hasRequired := channelRequiredFields["email"]
	assert.False(t, hasRequired, "channelRequiredFields must not carry a legacy email entry")

	api := newChannelTestAPI(t, `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/channels/email", nil)
	api.HandleChannels(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code, "GET /channels/email must 404 as an unknown channel type")
}
