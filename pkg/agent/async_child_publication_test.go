package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	asyncChildPublicationProbeName = "async_child_publication_probe"
	childPreviewProbeName          = "child_preview_probe"
	asyncChildPublicationMarker    = "ASYNC-CHILD-OUTPUT-MUST-NOT-REACH-TOP-LEVEL-CHAT"
)

type asyncChildPublicationProbe struct {
	tools.BaseTool
	release       chan struct{}
	done          chan struct{}
	callbackCount atomic.Int32
}

func newAsyncChildPublicationProbe() *asyncChildPublicationProbe {
	return &asyncChildPublicationProbe{
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (*asyncChildPublicationProbe) Name() string { return asyncChildPublicationProbeName }

func (*asyncChildPublicationProbe) Description() string {
	return "test-only async tool that reports distinct user-facing output"
}

func (*asyncChildPublicationProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (*asyncChildPublicationProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (*asyncChildPublicationProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("async publication probe must execute through ExecuteAsync")
}

func (p *asyncChildPublicationProbe) ExecuteAsync(
	ctx context.Context,
	_ map[string]any,
	callback tools.AsyncCallback,
) *tools.ToolResult {
	go func() {
		<-p.release
		p.callbackCount.Add(1)
		callback(ctx, &tools.ToolResult{ForUser: asyncChildPublicationMarker})
		close(p.done)
	}()
	return &tools.ToolResult{ForLLM: "async publication probe started", Async: true}
}

func (p *asyncChildPublicationProbe) completeAfterTurn(t *testing.T) {
	t.Helper()
	close(p.release)
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the late async tool callback")
	}
	require.Equal(t, int32(1), p.callbackCount.Load(),
		"the async tool callback must execute exactly once before publication is asserted")
}

type childPreviewProbe struct {
	tools.BaseTool
	executionCount atomic.Int32
}

func (*childPreviewProbe) Name() string { return childPreviewProbeName }

func (*childPreviewProbe) Description() string {
	return "test-only synchronous tool used to detect child tool-call previews"
}

func (*childPreviewProbe) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"argument": map[string]any{"type": "string"},
		},
	}
}

func (*childPreviewProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (p *childPreviewProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.executionCount.Add(1)
	return &tools.ToolResult{ForLLM: "preview probe completed"}
}

func newAsyncChildPublicationTestLoop(
	t *testing.T,
	provider providers.LLMProvider,
	toolFeedbackEnabled bool,
) (*AgentLoop, *bus.MessageBus, *AgentInstance) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ToolFeedback: config.ToolFeedbackConfig{
					Enabled:       toolFeedbackEnabled,
					MaxArgsLength: 300,
				},
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent, "test setup: default agent must exist")
	return al, msgBus, agent
}

func registerAsyncChildPublicationProbe(t *testing.T, al *AgentLoop) *asyncChildPublicationProbe {
	t.Helper()
	probe := newAsyncChildPublicationProbe()
	al.RegisterTool(probe)
	setAskPolicyForAllAgents(t, al, asyncChildPublicationProbeName, config.ToolPolicyAllow)
	return probe
}

func spawnPublicationTestChild(
	t *testing.T,
	al *AgentLoop,
	agent *AgentInstance,
	channel, chatID string,
) *tools.ToolResult {
	t.Helper()
	store := al.GetSessionStore()
	require.NotNil(t, store, "test setup: shared session store must exist")
	parentMeta, err := store.NewSession(session.SessionTypeChannel, channel, agent.ID)
	require.NoError(t, err, "mint a real parent session")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-async-publication",
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, 4),
		session:             &ephemeralSessionStore{},
		agent:               agent,
		agentID:             agent.ID,
		channel:             channel,
		chatID:              chatID,
		transcriptSessionID: parentMeta.ID,
		routingSessionID:    session.RoutingSessionID(parentMeta.ID),
		transcriptStore:     store,
	}

	result, err := spawnSubTurn(
		withSpawnToolCallID(context.Background(), "call-async-publication"),
		al,
		parent,
		SubTurnConfig{
			SystemPrompt:      "run the requested test tool",
			Model:             agent.Model,
			DelegateSessionID: "async-publication-child-" + parentMeta.ID,
		},
	)
	require.NoError(t, err, "spawn the delegated child through the production path")
	require.NotNil(t, result)
	return result
}

func TestDelegatedChild_GenericAsyncForUserDoesNotPublishOnParentRoute(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(asyncChildPublicationProbeName, `{}`).
		WithText("child completed")
	al, msgBus, agent := newAsyncChildPublicationTestLoop(t, provider, false)
	probe := registerAsyncChildPublicationProbe(t, al)

	spawnPublicationTestChild(t, al, agent, "telegram", "parent-chat")
	probe.completeAfterTurn(t)

	assert.Empty(t, drainOutbound(msgBus),
		"a generic async tool inside a delegated child must not publish ForUser on the parent's route")
}

func TestInteractiveRoot_GenericAsyncForUserStillPublishes(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(asyncChildPublicationProbeName, `{}`).
		WithText("root completed")
	al, msgBus, _ := newAsyncChildPublicationTestLoop(t, provider, false)
	probe := registerAsyncChildPublicationProbe(t, al)

	result, err := al.ProcessDirectWithChannel(
		context.Background(), "run the async probe", "root-session", "telegram", "root-chat",
	)
	require.NoError(t, err)
	assert.Equal(t, "root completed", result)
	probe.completeAfterTurn(t)
	assert.Equal(t, []bus.OutboundMessage{{
		Channel: "telegram",
		ChatID:  "root-chat",
		Content: asyncChildPublicationMarker,
	}}, drainOutbound(msgBus), "interactive root async feedback must remain user-visible")
}

func TestDelegatedChild_ToolPreviewDoesNotPublishTopLevelOnExternalChannel(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(childPreviewProbeName, `{"argument":"child-only"}`).
		WithText("child completed")
	al, msgBus, agent := newAsyncChildPublicationTestLoop(t, provider, true)
	probe := &childPreviewProbe{}
	al.RegisterTool(probe)
	setAskPolicyForAllAgents(t, al, childPreviewProbeName, config.ToolPolicyAllow)

	spawnPublicationTestChild(t, al, agent, "telegram", "parent-chat")

	require.Equal(t, int32(1), probe.executionCount.Load(),
		"the child preview probe must execute before publication is asserted")
	assert.Empty(t, drainOutbound(msgBus),
		"a delegated child's tool-call preview must not appear as a top-level external-channel message")
}

func TestInteractiveRoot_ToolPreviewStillPublishesOnExternalChannel(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(childPreviewProbeName, `{"argument":"root-visible"}`).
		WithText("root completed")
	al, msgBus, _ := newAsyncChildPublicationTestLoop(t, provider, true)
	probe := &childPreviewProbe{}
	al.RegisterTool(probe)
	setAskPolicyForAllAgents(t, al, childPreviewProbeName, config.ToolPolicyAllow)

	result, err := al.ProcessDirectWithChannel(
		context.Background(), "run the preview probe", "root-preview-session", "telegram", "root-chat",
	)
	require.NoError(t, err)
	assert.Equal(t, "root completed", result)
	require.Equal(t, int32(1), probe.executionCount.Load(),
		"the root preview probe must execute before publication is asserted")
	assert.Equal(t, []bus.OutboundMessage{{
		Channel: "telegram",
		ChatID:  "root-chat",
		Content: "[tool] `child_preview_probe`\n```\n{\"argument\":\"root-visible\"}\n```",
	}}, drainOutbound(msgBus), "interactive root tool previews must remain user-visible")
}

func TestInternalSessions_AsyncForUserDoesNotPublishTopLevel(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		provider := testutil.NewScenario().
			WithToolCall(asyncChildPublicationProbeName, `{}`).
			WithText("task completed")
		al, msgBus, agent := newAsyncChildPublicationTestLoop(t, provider, false)
		probe := registerAsyncChildPublicationProbe(t, al)

		result, err := al.processTaskDirect(
			context.Background(), agent.ID, "run the async probe", "task-session", "task:783",
		)
		require.NoError(t, err)
		assert.Equal(t, "task completed", result)
		probe.completeAfterTurn(t)
		assert.Empty(t, drainOutbound(msgBus),
			"an internal task turn with SendResponse=false must not publish async ForUser top-level")
	})

	t.Run("verifier", func(t *testing.T) {
		provider := testutil.NewScenario().
			WithToolCall(asyncChildPublicationProbeName, `{}`).
			WithText("verifier completed")
		al, msgBus, agent := newAsyncChildPublicationTestLoop(t, provider, false)
		probe := registerAsyncChildPublicationProbe(t, al)

		result, _, _, err := al.dispatchVerifierTurn(
			context.Background(), agent, "run the async probe", "verifier-session", "task:783-verifier",
		)
		require.NoError(t, err)
		assert.Equal(t, "verifier completed", result)
		probe.completeAfterTurn(t)
		assert.Empty(t, drainOutbound(msgBus),
			"an internal verifier turn with SendResponse=false must not publish async ForUser top-level")
	})
}
