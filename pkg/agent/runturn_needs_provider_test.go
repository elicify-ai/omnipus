// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-09 — the pre-turn `needs_provider` refusal.
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md FR-016 (typed
// refusal, WARN, zero upstream requests), FR-038 (LLMError code
// needs_provider, attribution config, and the gate ORDER: needs_provider →
// model_unassigned → context_window_unknown). Scenario US-6.AC2; dataset DS-8
// row 2. Test T33.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// TestAgentTurn_NeedsProvider_TypedRefusal — T33. An agent bound to a provider
// id the catalog has never heard of refuses every turn with LLMError code
// `needs_provider`, logs one WARN, and makes ZERO upstream requests. Boot
// itself is unaffected: the loop constructs, the agent is registered, and the
// OTHER agent in the same config runs normally (US-6.AC1's per-agent
// non-fatal guarantee, MAJ-010).
//
// The fixture also pins the GATE ORDER (FR-038). The unknown row carries a
// loopback `api_base` but no `protocol`, so it is:
//
//   - UNKNOWN as a provider — FR-035 needs BOTH halves for a custom row, and
//   - `locality: local` with no reported window — which is exactly what
//     ADR-066's context_window_unknown gate refuses on.
//
// Both gates therefore apply to this one agent, and the code the turn ends
// with proves which ran FIRST. If the ordering ever inverts, this test fails
// with `context_window_unknown` — the failure names the regression.
func TestAgentTurn_NeedsProvider_TypedRefusal(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil)

	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	// Both agents are members of a healthy workspace so the earlier workspace
	// gate passes and the provider gate is what refuses.
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspacesDir, "ws-1", "work"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspacesDir, "ws-1.json"),
		[]byte(`{"id":"ws-1","core_team":["agent-a","agent-b"]}`), 0o644))

	agentAHome := filepath.Join(tmpHome, "agents", "agent-a")
	agentBHome := filepath.Join(tmpHome, "agents", "agent-b")
	require.NoError(t, os.MkdirAll(agentAHome, 0o755))
	require.NoError(t, os.MkdirAll(agentBHome, 0o755))

	t.Setenv("T067_09_OPENAI_KEY", "sk-openai")
	provider := testutil.NewScenario().WithText("agent A's answer")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                tmpHome,
				DefaultModel:        config.DefaultModel{Provider: "openai", Model: "gpt-5.2"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Home: agentAHome},
				{
					ID:   "agent-b",
					Home: agentBHome,
					// B pins the unknown id explicitly.
					Model: &config.AgentModelConfig{Primary: "nope/x", Provider: "nope"},
				},
			},
		},
		Providers: []*config.ModelConfig{
			{
				Name:      "openai-1",
				Provider:  "openai",
				Model:     "gpt-5.2",
				APIBase:   "https://api.openai.com/v1",
				APIKeyRef: "T067_09_OPENAI_KEY",
			},
			{
				// Half a custom row: an api_base but no protocol. FR-035
				// requires both, so the id stays UNKNOWN — and the loopback
				// host makes ADR-066's window gate applicable too, which is
				// what makes the ordering assertion below meaningful.
				Name:     "nope",
				Provider: "nope",
				Model:    "nope/x",
				APIBase:  "http://127.0.0.1:9/v1",
			},
		},
	}
	cfg.Context = config.DefaultContextSettings()

	readLog := captureLogFile(t, logger.WARN)

	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err,
		"an unknown provider is a per-agent degrade, never a boot failure (FR-016, MAJ-010)")
	defer al.Close()

	agentA, ok := al.GetRegistry().GetAgent("agent-a")
	require.True(t, ok, "agent A must still be registered")
	agentB, ok := al.GetRegistry().GetAgent("agent-b")
	require.True(t, ok, "agent B must still be registered — the unknown provider is non-fatal")

	needsA, _ := agentA.needsProviderSnapshot()
	assert.False(t, needsA, "agent A is bound to a catalog provider and must not be degraded")
	needsB, idB := agentB.needsProviderSnapshot()
	require.True(t, needsB, "agent B is bound to an unknown provider and must carry the degrade")
	assert.Equal(t, "nope", idB, "the degrade names the operator's own spelling")

	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	// ProcessScheduled pins the acting agent directly (no routing), which is
	// what lets one loop drive both the degraded and the healthy agent.
	metaB, err := al.GetSessionStore().NewScheduledSession("agent-b")
	require.NoError(t, err)
	_, procErr := al.ProcessScheduled(
		context.Background(), "agent-b", metaB.ID, "hello", "scheduled", metaB.ID)
	require.Error(t, procErr, "a turn on an agent with an unknown provider must be refused")
	assert.True(t, errors.Is(procErr, ErrAgentNeedsProvider),
		"refusal must wrap ErrAgentNeedsProvider, got: %v", procErr)
	assert.False(t, errors.Is(procErr, ErrContextWindowUnknown),
		"FR-038 gate order: needs_provider is evaluated BEFORE ADR-066's window refusal")

	events := drainEvents(sub.C)
	var errPayloads []ErrorPayload
	var sawTurnStart, sawTurnEndError bool
	for _, e := range events {
		switch e.Kind {
		case EventKindTurnStart:
			sawTurnStart = true
		case EventKindTurnEnd:
			if p, ok := e.Payload.(TurnEndPayload); ok && p.Status == TurnEndStatusError {
				sawTurnEndError = true
			}
		case EventKindError:
			if p, ok := e.Payload.(ErrorPayload); ok {
				errPayloads = append(errPayloads, p)
			}
		}
	}
	require.True(t, sawTurnStart, "the refusal must be a registered turn; events=%v", eventKinds(events))
	require.NotEmpty(t, errPayloads, "the refusal must emit EventKindError; events=%v", eventKinds(events))

	found := false
	for _, p := range errPayloads {
		assert.NotEqual(t, string(CodeContextWindowUnknown), p.Code,
			"FR-038 gate order: the window gate must never be reached for a needs_provider agent")
		if p.Code == string(CodeNeedsProvider) {
			found = true
			assert.Equal(t, UserMessageForCode(CodeNeedsProvider), p.Message,
				"the live error must carry the catalogue copy, not the raw sentinel")
		}
	}
	assert.True(t, found, "EventKindError must carry code needs_provider; payloads=%+v", errPayloads)
	assert.True(t, sawTurnEndError, "the turn must end as TurnEndStatusError")
	assert.Zero(t, provider.CallCount(), "zero upstream requests for a refused turn (FR-016)")

	// US-6.AC1's other half: the healthy agent in the SAME config runs. The
	// unknown provider degraded exactly one agent, not the install.
	metaA, err := al.GetSessionStore().NewScheduledSession("agent-a")
	require.NoError(t, err)
	answer, aErr := al.ProcessScheduled(
		context.Background(), "agent-a", metaA.ID, "hello", "scheduled", metaA.ID)
	require.NoError(t, aErr, "agent A must run normally alongside a degraded agent B")
	assert.Equal(t, "agent A's answer", answer)

	log := readLog()
	assert.Contains(t, log, "Turn refused: the agent's provider is unknown",
		"the refusal is logged at WARN (FR-016); log=%s", log)
	assert.NotContains(t, strings.ToLower(log), "did you mean",
		"no hint, ever (FR-015); log=%s", log)
}
