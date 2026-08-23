// websocket_model_name_test.go — unit tests for FR-010 (per-turn model override).
//
// The WS handler must forward `MessageFrame.Metadata.ModelName` to the bus as
// `msg.Metadata["model_name"]` so the agent loop's switch-compress path can
// route THIS turn to the chosen model instead of the agent's default. The
// tests below drive handleChatMessage directly (no real WS connection) and
// assert the published bus.InboundMessage carries the expected Metadata.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestHandleChatMessage_ForwardsModelNameToBus is the primary FR-010 happy-path
// test: a frame carrying metadata.model_name = "z-ai/glm-5-turbo" must result in
// a published bus.InboundMessage whose Metadata["model_name"] equals that exact
// string. Without this wire-up, the Wave 3 switch-compress machinery
// (handleModelSwitch, summarizeDroppedTurns, splitForSwitchCompress,
// fitWithinBudget) is functionally inert end-to-end because the consumer at
// pkg/agent/loop.go (inboundMetadata("model_name")) sits behind a
// producer that never writes the key.
func TestHandleChatMessage_ForwardsModelNameToBus(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-1",     // chatID
		"",                 // frameSessionID (empty → mint a new session)
		"hello",            // content
		"",                 // agentID (no per-message agent override → default)
		nil,                // mediaRefs
		"z-ai/glm-5-turbo", // modelName
		"",                 // workspaceID (no active workspace)
		false,              // setupKickoff
		wc,
	)

	// Drain the inbound channel and assert the metadata key is present.
	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, "hello", msg.Content)
		require.NotNil(t, msg.Metadata, "msg.Metadata must be non-nil when model_name is set")
		assert.Equal(t, "z-ai/glm-5-turbo", msg.Metadata["model_name"],
			"bus.InboundMessage.Metadata[\"model_name\"] must carry the per-turn model override")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage — handleChatMessage did not publish")
	}
}

// TestHandleChatMessage_EmptyModelName_DoesNotSetKey verifies that an empty
// model_name does NOT set the metadata key — the agent falls back to its
// configured default model. Empty is treated as absent.
func TestHandleChatMessage_EmptyModelName_DoesNotSetKey(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-2",
		"",
		"hello",
		"",
		nil,
		"",    // empty modelName
		"",    // workspaceID (no active workspace)
		false, // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		if msg.Metadata != nil {
			_, hasKey := msg.Metadata["model_name"]
			assert.False(t, hasKey,
				"empty model_name must not produce a metadata key (server falls back to agent default)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_WhitespaceModelName_DoesNotSetKey verifies that a
// whitespace-only model_name is treated as absent (TrimSpace is applied first).
// Without this guard, a stray " " from the SPA would crash the agent loop with
// an "unknown model" provider error.
func TestHandleChatMessage_WhitespaceModelName_DoesNotSetKey(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-3",
		"",
		"hello",
		"",
		nil,
		"   \t\n", // whitespace only
		"",        // workspaceID (no active workspace)
		false,     // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		if msg.Metadata != nil {
			_, hasKey := msg.Metadata["model_name"]
			assert.False(t, hasKey,
				"whitespace-only model_name must not produce a metadata key (TrimSpace strips it)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_TrimsModelName verifies that surrounding whitespace is
// stripped from a non-empty model_name so the downstream resolver sees a
// canonical value (e.g. "z-ai/glm-5-turbo", not "  z-ai/glm-5-turbo  ").
func TestHandleChatMessage_TrimsModelName(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-4",
		"",
		"hello",
		"",
		nil,
		"  z-ai/glm-5-turbo  ", // surrounding whitespace
		"",                     // workspaceID (no active workspace)
		false,                  // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		require.NotNil(t, msg.Metadata)
		assert.Equal(t, "z-ai/glm-5-turbo", msg.Metadata["model_name"],
			"model_name must be TrimSpace'd before being written to Metadata")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}

// TestHandleChatMessage_ModelNameWithAgentID_BothKeysSet verifies that when
// both agentID and model_name are supplied, both metadata keys land on the
// bus.InboundMessage — neither path clobbers the other.
func TestHandleChatMessage_ModelNameWithAgentID_BothKeysSet(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	handler.handleChatMessage(
		context.Background(),
		"chat-model-5",
		"",
		"hello",
		"mia", // explicit agent_id
		nil,
		"z-ai/glm-5-turbo", // per-turn model
		"",                 // workspaceID (no active workspace)
		false,              // setupKickoff
		wc,
	)

	select {
	case msg := <-msgBus.InboundChan():
		require.NotNil(t, msg.Metadata)
		assert.Equal(t, "mia", msg.Metadata["agent_id"], "agent_id must remain")
		assert.Equal(
			t,
			"z-ai/glm-5-turbo",
			msg.Metadata["model_name"],
			"model_name must be added without clobbering agent_id",
		)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for bus.InboundMessage")
	}
}
