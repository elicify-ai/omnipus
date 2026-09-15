// websocket_model_name_test.go — unit tests for FR-010 (per-turn model override).
//
// The WS handler must forward `MessageFrame.Metadata.Name` to the bus as
// `msg.Metadata["model_name"]` so the agent loop's switch-compress path can
// route THIS turn to the chosen model instead of the agent's default. The
// tests below drive handleChatMessage directly (no real WS connection) and
// assert the published bus.InboundMessage carries the expected Metadata.

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newTestWSHandlerForModelName creates a WSHandler that uses the supplied
// message bus so the test can drain the inbound channel after the handler
// publishes. OMNIPUS_BEARER_TOKEN is left unset to disable auth.
func newTestWSHandlerForModelName(t *testing.T, msgBus *bus.MessageBus) (*WSHandler, *config.Config) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-default-model"},
				MaxTokens:    4096,
			},
			// A real, chat-target agent ("mia") so the default-agent
			// resolution most callers of this helper rely on (they pass
			// agentID="" to handleChatMessage) has something to resolve to.
			// The retired "main" sentinel used to be registered implicitly
			// regardless of cfg (pkg/agent/registry.go's old always-on
			// fallback); with it gone, pkg/gateway/websocket.go's
			// handleChatMessage now rejects rather than silently persisting
			// an empty owner when no agent_id is supplied and no default can
			// be resolved. Every one of this helper's 13+ call sites across 5
			// files passes agentID="", so this is seeded here rather than in
			// each caller individually.
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	return handler, cfg
}
