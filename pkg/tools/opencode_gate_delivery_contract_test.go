package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Team-lead 2026-11-21 07:55Z, simplest-design ruling: RESPOND stays an
// acknowledgement-only JSON object. Its published description must tell both
// runtime possibilities instead of promising an interrupt during startup.
func TestOpenCodeGateA1_RespondContractDescribesQueuedStartupWithoutAddingFields(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", "DelegateRespondResponse.yaml"))
	require.NoError(t, err)
	var schema map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &schema))
	require.Equal(t, "DelegateRespondResponse", schema["title"], "positive control: parse the intended published response schema")
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	require.Len(t, properties, 1, "commission forbids adding a response field")
	ack, ok := properties["acknowledged"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", ack["type"])
	assert.Equal(t, false, schema["additionalProperties"])
	description, ok := schema["description"].(string)
	require.True(t, ok)
	description = strings.ToLower(description)
	assert.Contains(t, description, "interrupt", "the underway-run route must remain described")
	assert.Contains(t, description, "resume", "the underway-run route must remain described")
	assert.Contains(t, description, "queu", "A1: acknowledge queued delivery as an alternative to interruption")
	assert.Contains(t, description, "start", "A1: identify startup as the queued case")
	assert.Contains(t, description, "current run ends", "ruling R2 section 3: startup delivery waits for the original run to end, not an immediate resume")
}

// The delivery interface here reports successful acceptance and a correlation
// id ONLY; it does not report which runtime action happened. This is a tools
// boundary test of truthful rendering, not a claim to test real CLI delivery.
// The real starting/ready runtime controls live in the agent A1 regression.
func TestOpenCodeGateA1_UnknownDeliveryOutcomeDoesNotInventInterruptOrQueue(t *testing.T) {
	tool, lifecycle, _, _ := newADR053TestTool(t)
	sink := &cliDelivererSink{}
	tool.SetSteeringSink(sink)
	seedRedirectChild(t, lifecycle, "gate-a1-legacy-delivery", session.LifecycleRunning, true)
	result := tool.Execute(WithTranscriptSessionID(context.Background(), "parent-1"), map[string]any{
		"action": "steer", "session_id": "gate-a1-legacy-delivery", "text": "keep the same conversation",
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "legacy capability accepted the instruction: %s", result.ForLLM)
	assert.Equal(t, []string{"gate-a1-legacy-delivery"}, sink.sessions)
	assert.Equal(t, []string{"keep the same conversation"}, sink.texts)
	notice := strings.ToLower(result.ForLLM)
	assert.Contains(t, notice, "delivered", "report the acceptance we actually know")
	assert.Contains(t, notice, "same conversation")
	assert.NotContains(t, notice, "interrupt", "unknown delivery outcome cannot claim interruption")
	assert.NotContains(t, notice, "resume", "unknown delivery outcome cannot claim resumption")
	assert.NotContains(t, notice, "queued", "unknown delivery outcome cannot claim queuing either")
}
