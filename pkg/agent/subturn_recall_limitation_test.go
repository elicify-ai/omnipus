// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// subturnRecallProvider: call 1 asks for recall_conversation(turn_range 1-1);
// call 2 captures the tool-role message the child actually received.
type subturnRecallProvider struct {
	mu          sync.Mutex
	calls       int
	toolContent string
}

func (p *subturnRecallProvider) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: "call_child_recall", Type: "function", Name: "recall_conversation",
			Arguments: map[string]any{"turn_range": "1-1"},
		}}}, nil
	}
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == "call_child_recall" {
			p.toolContent = m.Content
		}
	}
	return &providers.LLMResponse{Content: "child done", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *subturnRecallProvider) GetDefaultModel() string { return "gpt-4o-mini" }

// TestSubTurn_RecallReadsParentStore_KnownLimitation — ADR-066 §6.4 / FR-044
// / B-51 (test 54). A delegated child runs with its own ephemeral session
// store, but recall_conversation was constructed with the PARENT's
// agent.Sessions, so the child's recall reads the parent store under the
// child's ephemeral key and finds nothing — even though the parent store
// holds turns. This pins the empty outcome and the INFO line naming the
// limitation. It is deliberately NOT fixed here.
func TestSubTurn_RecallReadsParentStore_KnownLimitation(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:           workspace,
				DefaultModel:   config.DefaultModel{Provider: "mock", Model: "parent-model"},
				DefaultAgentID: "recall-parent",
				MaxTokens:      1000,
			},
			List: []config.AgentConfig{
				{
					ID: "recall-parent", Type: config.AgentTypeCore, Default: true, Home: workspace,
					Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
						Policies: map[string]config.ToolPolicy{"recall_conversation": config.ToolPolicyAllow},
					}},
				},
				{
					ID: "recall-child", Type: config.AgentTypeWorker, Home: workspace,
					Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
						Policies: map[string]config.ToolPolicy{"recall_conversation": config.ToolPolicyAllow},
					}},
				},
			},
		},
	}
	cfg.Context.DefaultContextWindow = intPtr(200000)
	provider := &subturnRecallProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)

	parent := al.registry.GetDefaultAgent()
	require.NotNil(t, parent)
	target, ok := al.registry.GetAgent("recall-child")
	require.True(t, ok)

	// The TARGET's persistent store does hold a turn — under the parent-side
	// routing key. The child's recall reads THIS store, but under the child's
	// own ephemeral key, which is the limitation being pinned.
	const parentKey = "parent-routing-session"
	target.Sessions.SetHistory(parentKey, makeTurn("THE-PARENT-NONCE", "noted"))
	require.NoError(t, target.Sessions.Save(parentKey))

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-recall-limitation",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		sessionKey:          parentKey,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     sessionStore,
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-recall-limitation")
	result, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
		Model:         "sub-turn-config-model-unused-for-target-resolution",
		SystemPrompt:  "what was the nonce?",
		TargetAgentID: "recall-child",
		Async:         false,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NoError(t, result.Err)

	provider.mu.Lock()
	calls, content := provider.calls, provider.toolContent
	provider.mu.Unlock()
	require.GreaterOrEqual(t, calls, 2, "the child must be called again after its recall")
	require.Contains(t, content, "no turns found in this session's archive",
		"known limitation: the child's recall reads the parent store under the ephemeral key and finds nothing")
	require.False(t, strings.Contains(content, "THE-PARENT-NONCE"))

	logs := readLog()
	require.Contains(t, logs, "recall_conversation in a delegated sub-turn found no archive",
		"FR-044: the limitation must be logged at INFO, naming it")
	require.Contains(t, logs, "known limitation")
}
