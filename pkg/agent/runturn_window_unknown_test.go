// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestRunTurn_LocalEndpointUnknownWindow_RefusedTyped — ADR-066 B-09 (US-2.AC3,
// FR-008 refusal half; SC-007 backend half). An agent bound to a
// `locality: local` endpoint whose window nobody reported must not guess
// 128,000 — the turn is refused as a REAL failed turn (turn.start, typed
// EventKindError with code context_window_unknown and the exact contract
// copy, turn.end error) and the provider is never called. The override
// → reload → next turn runs half is T066-17's (test 37).
func TestRunTurn_LocalEndpointUnknownWindow_RefusedTyped(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil) // the live rung reports nothing

	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// The agent is a member of a healthy workspace so the earlier
	// workspace gate passes and the window gate is what refuses.
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	wsDir := filepath.Join(workspacesDir, "ws-1")
	require.NoError(t, os.MkdirAll(filepath.Join(wsDir, "work"), 0o755))
	wsJSON := `{"id":"ws-1","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "ws-1.json"), []byte(wsJSON), 0o644))

	provider := testutil.NewScenario().WithText("should-never-be-called")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentWorkspaceDir,
				DefaultModel:        config.DefaultModel{Provider: "my-proxy", Model: "local-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
		// A custom row at a loopback host: locality local (ADR-067's
		// predicate), not in the catalog, no live value → unknown window.
		Providers: []*config.ModelConfig{{
			ModelName: "local-model",
			Model:     "local-model",
			Provider:  "my-proxy",
			APIBase:   "http://127.0.0.1:8000/v1",
		}},
	}
	cfg.Context = config.DefaultContextSettings()

	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err, "NewAgentLoop must succeed — an unknown window is a turn refusal, not a boot failure")
	defer al.Close()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	assert.True(t, agent.WindowUnknown, "the instance must carry the unknown-window state")
	assert.Equal(t, 0, agent.ContextWindow, "never 128,000 for a local endpoint")

	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	_, procErr := al.ProcessDirect(context.Background(), "hello", "test-session-window-unknown")
	require.Error(t, procErr, "a turn on an endpoint with no reported window must be refused")
	assert.True(t, errors.Is(procErr, ErrContextWindowUnknown),
		"refusal must wrap ErrContextWindowUnknown, got: %v", procErr)

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
		if p.Code == string(CodeContextWindowUnknown) {
			found = true
			assert.Equal(t, UserMessageForCode(CodeContextWindowUnknown), p.Message,
				"the live error must carry the catalogue copy, not the raw sentinel")
			assert.NotEmpty(t, p.SessionID)
		}
	}
	assert.True(t, found, "EventKindError must carry code context_window_unknown; payloads=%+v", errPayloads)
	assert.True(t, sawTurnEndError, "the turn must end as TurnEndStatusError")
	assert.Zero(t, provider.CallCount(), "the provider must never be called for a refused turn")
}
