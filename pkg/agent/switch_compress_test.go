package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// recordingSummaryProvider captures the messages sent to it and returns a
// canned summary. Used by switch-time compress tests to verify that
// summarizeDroppedTurns asks the LLM to summarize dropped turns.
type recordingSummaryProvider struct {
	chatCalls []chatCall
	summary   string
}

type chatCall struct {
	Messages []providers.Message
	Model    string
}

func (r *recordingSummaryProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	// Capture a copy of the messages we received.
	r.chatCalls = append(r.chatCalls, chatCall{
		Messages: append([]providers.Message(nil), messages...),
		Model:    model,
	})
	return &providers.LLMResponse{
		Content: r.summary,
	}, nil
}

func (r *recordingSummaryProvider) GetDefaultModel() string {
	return "recording-summary-model"
}

// newSwitchTestAgentLoop builds an AgentLoop with the models needed by the
// switch-time compress tests registered in cfg.Providers so that
// ApplyAgentModel (which handleModelSwitch now calls to orchestrate the
// provider+candidates swap alongside Model) can resolve them. Without this
// every ApplyAgentModel call would fail with "model not found".
//
// The ModelName is the public identifier (matches the input passed to
// handleModelSwitch), and the Model field is the provider-prefixed form
// ("openai/<name>") so the known-protocol factory can build a stub
// provider. Protocol = "openai" is a recognised protocol prefix (see
// pkg/providers/factory_provider.go::knownProtocols).
func newSwitchTestAgentLoop(t *testing.T, models ...string) (al *AgentLoop, cfg *config.Config, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("SWITCH_TEST_KEY", "switch-test-key")
	mkProvider := func(name string) *config.ModelConfig {
		return &config.ModelConfig{
			ModelName: name,
			Model:     "openai/" + name,
			Provider:  "openai",
			APIBase:   "http://127.0.0.1:1",
			APIKeyRef: "SWITCH_TEST_KEY",
		}
	}
	providers := make([]*config.ModelConfig, 0, len(models))
	for _, m := range models {
		providers = append(providers, mkProvider(m))
	}
	cfg = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: providers,
	}
	// Default model entry must be present too so registry construction succeeds.
	if len(providers) == 0 {
		cfg.Providers = append(cfg.Providers, mkProvider("test-model"))
	}
	mp := &mockProvider{}
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), mp)
	return al, cfg, func() {}
}

// TestSwitchTimeCompress_LargerToSmaller_TriggersCompress verifies that when
// the user switches from a large-context model to a smaller-context model and
// the conversation does not fit, the switch-time compress path is triggered.
//
// BDD-11: Old=200k, new=8k, conv=50k -> compress before switch
func TestSwitchTimeCompress_LargerToSmaller_TriggersCompress(t *testing.T) {
	action := decideSwitchCompressAction(50000, 8000)
	assert.Equal(t, SwitchActionCompress, action,
		"50k conversation switched into an 8k window must trigger compress")
}

// TestSwitchTimeCompress_SameWindow_NoOp verifies that switching between two
// models with the same context window does not trigger compress.
//
// BDD-14: Old=200k, new=200k, conv=50k -> no compress (new model fits)
func TestSwitchTimeCompress_SameWindow_NoOp(t *testing.T) {
	action := decideSwitchCompressAction(50000, 200000)
	assert.Equal(t, SwitchActionNoop, action,
		"50k conversation switched into a 200k window must be a no-op")

	action = decideSwitchCompressAction(50000, 50000)
	assert.Equal(t, SwitchActionNoop, action,
		"equal window and conversation size must be a no-op")
}

// TestSwitchTimeCompress_EmptySession_NoOp verifies that switching on an
// empty session never triggers compress regardless of window size.
//
// BDD-15, BDD-29: Old=200k, new=8k, conv=0 -> no compress (empty)
func TestSwitchTimeCompress_EmptySession_NoOp(t *testing.T) {
	action := decideSwitchCompressAction(0, 8000)
	assert.Equal(t, SwitchActionNoop, action,
		"empty session must never trigger compress")
}

// TestSwitchTimeCompress_BoundaryEqualNoCompress confirms the boundary case
// where current conversation exactly equals new window does not compress
// (no need to drop anything).
func TestSwitchTimeCompress_BoundaryEqualNoCompress(t *testing.T) {
	action := decideSwitchCompressAction(8000, 8000)
	assert.Equal(t, SwitchActionNoop, action,
		"conversation equal to new window must not compress (no overflow yet)")
}

// TestSwitchTimeCompress_SmallerToLarger_NoOp verifies switching to a model
// with more room never triggers compress.
func TestSwitchTimeCompress_SmallerToLarger_NoOp(t *testing.T) {
	action := decideSwitchCompressAction(7000, 200000)
	assert.Equal(t, SwitchActionNoop, action,
		"switching to a larger-window model must not trigger compress")
}

// TestBuildSyntheticSwitchMessage_Wording verifies the synthetic system
// message exactly matches the agreed wording from spec Q4.
//
// Spec Q4: "Conversation moved to {new_model} from {old_model} on {timestamp}.
// The prior turns have been compressed to fit the new context window.
// Summary: {summary}".
func TestBuildSyntheticSwitchMessage_Wording(t *testing.T) {
	ts := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	msg := buildSyntheticSwitchMessage("z-ai/glm-5.2", "gpt-4o", "decisions: A, B; open: C", ts)

	assert.Equal(t, "system", msg.Role, "synthetic switch message must be a system message")
	assert.True(t, msg.Synthetic, "synthetic switch message must carry Synthetic=true")

	const wantPrefix = "Conversation moved to gpt-4o from z-ai/glm-5.2 on 2026-06-17 12:00:00 UTC."
	require.True(t, len(msg.Content) > len(wantPrefix),
		"message must include the agreed prefix")
	assert.Equal(t, wantPrefix, msg.Content[:len(wantPrefix)])

	const wantTail = "Summary: decisions: A, B; open: C"
	assert.Contains(t, msg.Content, wantTail,
		"message must include the agreed summary section")
	assert.Contains(t, msg.Content, "compressed to fit the new context window",
		"message must explain why the prior turns were compressed")
}

// TestSummarizeDroppedTurns_FallsBackOnLLMError verifies that when the
// summarization LLM call fails, the function returns the error to the caller
// (the caller then falls back to forceCompression — see spec FR-011).
func TestSummarizeDroppedTurns_FailingProvider_ReturnsError(t *testing.T) {
	tp, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	defer cleanup()

	failingProv := &failingProvider{}
	tp.GetRegistry() // ensure registry initialized

	// Build a temporary agent with a failing provider so we can exercise the
	// LLM call path. The provider must be set on the agent instance because
	// summarizeDroppedTurns reads it from there.
	agent := tp.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	agent.Provider = failingProv

	dropped := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := tp.summarizeDroppedTurns(ctx, agent, dropped, 8000)
	require.Error(t, err,
		"summarizeDroppedTurns must propagate LLM errors to the caller")
}

// TestSummarizeDroppedTurns_Success_StoresSummary verifies the happy path:
// dropped turns are summarized by the LLM and the summary string is returned.
func TestSummarizeDroppedTurns_Success_StoresSummary(t *testing.T) {
	tp, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled
	defer cleanup()

	recProv := &recordingSummaryProvider{
		summary: "key decisions: pick A; open: confirm with user",
	}

	agent := tp.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	agent.Provider = recProv

	dropped := []providers.Message{
		{Role: "user", Content: "should I pick A or B?"},
		{Role: "assistant", Content: "I'd recommend A because ..."},
		{Role: "user", Content: "are you sure?"},
		{Role: "assistant", Content: "Let me verify ..."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Use a context window whose half is larger than the agent's existing
	// MaxTokens so the budget ends up clamped to MaxTokens — this is the
	// production behaviour and we assert against it directly.
	const contextWindow = 16000
	expectedBudget := contextWindow / 2
	if agent.MaxTokens > 0 && agent.MaxTokens < expectedBudget {
		expectedBudget = agent.MaxTokens
	}

	summary, err := tp.summarizeDroppedTurns(ctx, agent, dropped, contextWindow)
	require.NoError(t, err)
	assert.Equal(t, "key decisions: pick A; open: confirm with user", summary)

	require.Len(t, recProv.chatCalls, 1,
		"summarizeDroppedTurns must invoke the LLM exactly once")
	captured := recProv.chatCalls[0]
	require.NotEmpty(t, captured.Messages,
		"summarizeDroppedTurns must send at least one prompt message")
	assert.Contains(t, captured.Messages[0].Content, "Summarize",
		"summary prompt must ask the LLM to summarize")
	assert.Contains(t, captured.Messages[0].Content, itoa(expectedBudget),
		"summary prompt must include the budget token cap so the LLM knows the size bound")
}

// TestSwitchTime_EndToEnd_HappyPath verifies the full integration: when an
// incoming message carries a model_name metadata that differs from the
// current agent model and the conversation is over-budget, the loop:
//
//  1. detects the switch,
//  2. calls summarizeDroppedTurns (LLM),
//  3. inserts the synthetic system message into the session history,
//  4. drops the oldest turns (forceCompression path),
//  5. keeps the new Model field on the next outgoing assistant message
//     (verified here by checking that the agent's Model field was updated).
func TestSwitchTime_EndToEnd_HappyPath(t *testing.T) {
	// Register both the default model and the new model so ApplyAgentModel
	// (called by handleModelSwitch to swap Provider+Candidates alongside
	// Model) can resolve them.
	const newModel = "openrouter/some-small-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	// Configure a small window so the existing 50k history triggers compress.
	agent.ContextWindow = 8000
	agent.MaxTokens = 4096
	// Force the agent to a known starting model so we can detect the switch.
	oldModel := agent.Model
	require.NotEmpty(t, oldModel, "test setup: default agent must have a model")

	// Seed the session history with 50k of conversation (well over 8k window).
	const sessionKey = "switch-test-session"
	bigText := make([]byte, 0, 50000)
	for i := 0; i < 50000; i++ {
		bigText = append(bigText, 'a')
	}
	agent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "I need help with: " + string(bigText)},
		{Role: "assistant", Content: "Sure, here is what I think: " + string(bigText)},
	})
	require.NoError(t, agent.Sessions.Save(sessionKey))

	// Override the agent's provider so the summary call is captured, not actually
	// dispatched to a real LLM.
	recProv := &recordingSummaryProvider{
		summary: "Decisions: stay focused on the topic. Open: confirm plan with user.",
	}
	agent.Provider = recProv

	require.NotEqual(t, oldModel, newModel,
		"test invariant: new model must differ from current model")

	// Run the switch-time compress path.
	updatedAgent, err := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		newModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": newModel},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, updatedAgent)

	// After the switch the agent's Model field reflects the new model.
	assert.Equal(t, newModel, updatedAgent.Model,
		"agent.Model must be updated to the new model after a switch")

	// Strategy B (FR-012): the switch summary lives in Sessions.GetSummary
	// (consumed by BuildMessages into the dynamic system prompt), NOT as a
	// role:"system" message in history. sanitizeHistoryForProvider would
	// have stripped that synthetic system message before it reached the LLM.
	sessionSummary := updatedAgent.Sessions.GetSummary(sessionKey)
	require.NotEmpty(t, sessionSummary,
		"strategy B: switch summary must be stored in session summary (FR-012)")
	assert.Contains(t, sessionSummary, newModel, "switch summary must name the new model")
	assert.Contains(t, sessionSummary, oldModel, "switch summary must name the old model")
	assert.Contains(t, sessionSummary, "Conversation moved",
		"switch summary must use the agreed wording")

	// The conversation was compressed — total token estimate must be < 8k.
	// Strategy B has no synthetic message in history; only the kept real
	// turns remain, trimmed to fit.
	history := updatedAgent.Sessions.GetHistory(sessionKey)
	for _, m := range history {
		assert.False(t, m.Synthetic,
			"strategy B: no synthetic system message must be inserted into history")
	}
	total := 0
	for _, m := range history {
		total += estimateMessageTokens(m)
	}
	assert.Less(t, total, 8000,
		"after compress, history token estimate must fit in 8k window")

	// The LLM summary call actually happened.
	require.NotEmpty(t, recProv.chatCalls,
		"summarizeDroppedTurns must have invoked the provider's Chat")

	// ApplyAgentModel swap: agent.Provider must have been replaced (the
	// orchestration call resolved and created a new provider for newModel).
	require.NotNil(t, updatedAgent.Provider,
		"agent.Provider must not be nil after ApplyAgentModel swap")
}

// TestSwitchTime_EmptySession_NoSyntheticMessage verifies that switching on an
// empty session does NOT insert a synthetic system message — there is nothing
// to summarize.
func TestSwitchTime_EmptySession_NoSyntheticMessage(t *testing.T) {
	const newModel = "openrouter/some-other-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	agent.ContextWindow = 8000
	agent.MaxTokens = 4096

	oldModel := agent.Model
	require.NotEqual(t, oldModel, newModel)

	const sessionKey = "empty-session"
	agent.Sessions.SetHistory(sessionKey, nil)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	recProv := &recordingSummaryProvider{summary: "should not be used"}
	agent.Provider = recProv

	updatedAgent, err := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		newModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": newModel},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, updatedAgent)

	history := updatedAgent.Sessions.GetHistory(sessionKey)
	for _, m := range history {
		assert.False(t, m.Synthetic,
			"empty session must not produce a synthetic system message")
	}
	assert.Equal(t, newModel, updatedAgent.Model,
		"empty session switch still updates agent.Model")
	assert.Empty(t, recProv.chatCalls,
		"empty session must not invoke the summarization LLM call")
}

// TestSwitchTime_LLMReceivesSyntheticSummary is the FR-012 regression
// guard: after a model switch with compression, the LLM MUST see the
// switch summary text — either in the system prompt (Strategy B) or in
// the message list (Strategy A). The previous bug was that
// sanitizeHistoryForProvider stripped the synthetic role:"system" message
// before it reached the LLM, so the summary the chat runtime paid an LLM
// call to produce was silently lost.
//
// This test drives handleModelSwitch with a recording summary provider,
// then runs the next LLM call through the agent loop's BuildMessages path
// and asserts the captured messages contain the summary text.
func TestSwitchTime_LLMReceivesSyntheticSummary(t *testing.T) {
	const newModel = "openrouter/some-small-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	// Force compress to fire.
	agent.ContextWindow = 8000
	agent.MaxTokens = 4096

	const sessionKey = "llm-receives-summary"
	bigText := make([]byte, 0, 50000)
	for i := 0; i < 50000; i++ {
		bigText = append(bigText, 'a')
	}
	agent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "I need help with: " + string(bigText)},
		{Role: "assistant", Content: "Sure, here is what I think: " + string(bigText)},
	})
	require.NoError(t, agent.Sessions.Save(sessionKey))

	// Summary the LLM will produce — verify it reaches the next chat call.
	const summaryText = "FR-012-key-decisions: pick A; open: confirm plan"
	recProv := &recordingSummaryProvider{summary: summaryText}
	agent.Provider = recProv

	// Trigger the switch — this is the production path that previously lost
	// the synthetic system message to sanitizeHistoryForProvider.
	updatedAgent, err := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		newModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": newModel},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, updatedAgent)

	// BuildMessages consumes Sessions.GetSummary and stitches it into the
	// system prompt. Verify the next LLM call would see the summary text
	// by running BuildMessages and asserting the system message contains
	// the summary string (Strategy B: summary → system prompt).
	messages := updatedAgent.ContextBuilder.BuildMessages(
		updatedAgent.Sessions.GetHistory(sessionKey),
		updatedAgent.Sessions.GetSummary(sessionKey),
		"now please continue",
		nil,
		"", "", "", "",
	)
	require.NotEmpty(t, messages,
		"BuildMessages must produce messages for the next turn")
	require.Equal(t, "system", messages[0].Role,
		"first message must be the system prompt")
	systemPrompt := messages[0].Content
	assert.Contains(t, systemPrompt, summaryText,
		"the LLM system prompt must contain the switch summary text — FR-012 regression guard")

	// Also confirm the summary is not present in history (Strategy B stores
	// it in summary, not in history messages).
	for _, m := range messages {
		if m.Role == "system" {
			continue
		}
		assert.False(t, m.Synthetic,
			"no message outside the system prompt must carry Synthetic=true")
	}
}

// failingProvider is used to exercise summarizeDroppedTurns' error path. It
// returns an error from Chat and exposes a default model.
type failingProvider struct{}

func (f *failingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return nil, context.DeadlineExceeded
}

func (f *failingProvider) GetDefaultModel() string {
	return "failing-model"
}

// Compile-time check that failingProvider satisfies the LLMProvider interface.
var _ providers.LLMProvider = (*failingProvider)(nil)
var _ providers.LLMProvider = (*recordingSummaryProvider)(nil)

// Reference unused import session to avoid compile error if the file is later trimmed.
var _ session.UnifiedSessionType = session.SessionTypeChat
