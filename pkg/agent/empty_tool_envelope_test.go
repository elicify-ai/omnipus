package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Issue #1278: a turn must never end as a "final answer" when the provider says
// it was calling tools (or died doing so) and no tool call came back.

func runEnvelopeTurn(t *testing.T, provider providers.LLMProvider) (reply string, stub *argsRecordingTool, err error) {
	t.Helper()
	al, home := schedTestLoop(t)
	mia := registerAgent(t, al, home, "mia", provider)
	stub = &argsRecordingTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"args_tool": "allow"}})
	meta, nerr := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, nerr)
	reply, err = al.ProcessScheduled(context.Background(), "mia", meta.ID, "use args_tool", "scheduled", meta.ID)
	return reply, stub, err
}

// A stream that dies after the 200 (provider error chunk) returns the typed
// error; the loop retries the whole completion. The errored attempt returned no
// response, so it executed no tool: the retry's tool call runs exactly once.
func TestEmptyEnvelope_StreamProviderErrorIsRetriedAndTheToolRunsExactlyOnce(t *testing.T) {
	provider := testutil.NewScenario().
		WithError(&common.StreamProviderError{Code: "server_error", Message: "Provider disconnected unexpectedly"}).
		WithToolCall("args_tool", `{"content":"x"}`).
		WithText("done")
	reply, stub, err := runEnvelopeTurn(t, provider)
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	assert.Equal(t, int32(1), stub.calls.Load(), "the retried completion must execute the tool exactly once, never twice")
	assert.Equal(t, 3, provider.CallCount(), "failed attempt, the retry with the tool call, then the final answer")
}

// scriptedFinishProvider returns the scripted responses in order and counts calls.
type scriptedFinishProvider struct {
	mu    sync.Mutex
	resps []*providers.LLMResponse
	calls int
}

func (p *scriptedFinishProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := p.calls
	p.calls++
	if i >= len(p.resps) {
		i = len(p.resps) - 1
	}
	return p.resps[i], nil
}
func (p *scriptedFinishProvider) GetDefaultModel() string { return "test-model" }
func (p *scriptedFinishProvider) count() int              { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }

// finish_reason "tool_calls" with nothing assembled is not a final answer: the
// completion is retried once, and a retry that brings the call proceeds.
func TestEmptyEnvelope_ToolCallsFinishWithNoCallsIsRetriedOnce(t *testing.T) {
	provider := &scriptedFinishProvider{resps: []*providers.LLMResponse{
		{Content: "Pointing the default at tavily and testing:", FinishReason: "tool_calls",
			Usage: &providers.UsageInfo{CompletionTokens: 8054}},
		{ToolCalls: []providers.ToolCall{{ID: "c1", Function: &providers.FunctionCall{Name: "args_tool", Arguments: `{"content":"x"}`}}}, FinishReason: "tool_calls"},
		{Content: "done", FinishReason: "stop"},
	}}
	reply, stub, err := runEnvelopeTurn(t, provider)
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	assert.Equal(t, int32(1), stub.calls.Load())
	assert.Equal(t, 3, provider.count())
}

// If the retry is just as empty the turn ends with a VISIBLE error, never with
// the preamble presented as the answer.
func TestEmptyEnvelope_RepeatedEmptyEnvelopeEndsWithAVisibleError(t *testing.T) {
	provider := &scriptedFinishProvider{resps: []*providers.LLMResponse{
		{Content: "Let me run it:", FinishReason: "tool_calls"},
	}}
	reply, stub, err := runEnvelopeTurn(t, provider)
	require.Error(t, err, "a model that keeps saying it is calling tools without calling any must fail loudly, got reply %q", reply)
	assert.Equal(t, 2, provider.count(), "one retry, then the visible failure")
	assert.Equal(t, int32(0), stub.calls.Load())
	assert.NotContains(t, reply, "Let me run it:", "the preamble must not be presented as the final answer")
}

// A normal final answer is untouched by the safety net.
func TestEmptyEnvelope_StopFinishIsAFinalAnswer(t *testing.T) {
	provider := &scriptedFinishProvider{resps: []*providers.LLMResponse{{Content: "All done.", FinishReason: "stop"}}}
	reply, _, err := runEnvelopeTurn(t, provider)
	require.NoError(t, err)
	assert.Equal(t, "All done.", reply)
	assert.Equal(t, 1, provider.count())
}

// R2-S1-03 at the loop: a billing or auth failure that arrives mid-stream is not
// retried (each attempt is billed and fails the same way); a transient one is.
func TestEmptyEnvelope_MidStreamBillingAndAuthErrorsAreNotRetried(t *testing.T) {
	for _, code := range []string{"402", "401"} {
		t.Run(code, func(t *testing.T) {
			provider := testutil.NewScenario().
				WithError(&common.StreamProviderError{Code: code, Message: "no"}).
				WithText("must not be reached")
			_, _, err := runEnvelopeTurn(t, provider)
			require.Error(t, err)
			assert.Equal(t, 1, provider.CallCount(), "a %s mid-stream error must not be retried", code)
		})
	}
}

// R2-S1-06: a failed mid-stream attempt was billed. Its usage is debited on the
// turn exactly like a delivered one, so the turn's tokens include it.
func TestEmptyEnvelope_FailedMidStreamAttemptUsageIsDebitedOnTheTurn(t *testing.T) {
	al, home := schedTestLoop(t)
	provider := testutil.NewScenario().
		WithError(&common.StreamProviderError{Code: "server_error", Message: "x",
			Usage: &providers.UsageInfo{PromptTokens: 4000, CompletionTokens: 1000, TotalTokens: 5000}}).
		WithText("done").WithUsage(40, 60)
	registerAgent(t, al, home, "mia", provider)
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)
	_, err = al.ProcessScheduled(context.Background(), "mia", meta.ID, "go", "scheduled", meta.ID)
	require.NoError(t, err)
	entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
	require.NoError(t, err)
	var calls []session.TranscriptEntry
	var tokens, prompt, output int
	for _, e := range entries {
		if e.Role == "assistant" {
			calls = append(calls, e)
			tokens += e.Tokens
			prompt += e.PromptTokens
			output += e.CompletionTokens
		}
	}
	// Contract changes + chief ruling: EACH billed call owns its entry. The
	// failed, no-text attempt is invisible; final narration carries only its call.
	require.Len(t, calls, 2, "the failed attempt and delivered retry each need one accounting entry")
	assert.Equal(t, []string{"", "done"}, []string{calls[0].Content, calls[1].Content},
		"the failed call adds no visible narration and the delivered answer stays exact")
	assert.Equal(t, []int{4000, 1000, 5000}, []int{calls[0].PromptTokens, calls[0].CompletionTokens, calls[0].Tokens},
		"failed call's billed usage must persist independently")
	assert.Equal(t, []int{40, 60, 100}, []int{calls[1].PromptTokens, calls[1].CompletionTokens, calls[1].Tokens},
		"delivered call must not repeat the failed call's usage")
	for i, call := range calls {
		assert.Zero(t, call.CacheReadTokens, "call %d fixture has no cache read", i+1)
		assert.Zero(t, call.CacheWriteTokens, "call %d fixture has no cache write", i+1)
	}
	assert.Equal(t, []int{4040, 1060, 5100}, []int{prompt, output, tokens},
		"all entries conserve 4000+40 input, 1000+60 output, and 5000+100 total")
	stored, err := al.GetSessionStore().GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, []int{4040, 1060, 5100}, []int{stored.Stats.TokensIn, stored.Stats.TokensOut, stored.Stats.TokensTotal},
		"session aggregation must include the invisible failed call exactly once")
	assert.Equal(t, 2, provider.CallCount(), "exactly one failed attempt and one delivered retry")
}

// R2-S1-10: the retry budget is per blip. A second, separate empty envelope later
// in the same turn gets its own retry.
func TestEmptyEnvelope_ASecondSeparateBlipInTheSameTurnIsRetriedToo(t *testing.T) {
	call := func(id string) *providers.LLMResponse {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: id, Function: &providers.FunctionCall{Name: "args_tool", Arguments: `{"content":"x"}`}}}, FinishReason: "tool_calls"}
	}
	provider := &scriptedFinishProvider{resps: []*providers.LLMResponse{
		{Content: "first:", FinishReason: "tool_calls"}, // blip 1
		call("c1"), // retried; the tool runs
		{Content: "second:", FinishReason: "tool_calls"}, // blip 2, later in the same turn
		call("c2"),
		{Content: "done", FinishReason: "stop"},
	}}
	reply, stub, err := runEnvelopeTurn(t, provider)
	require.NoError(t, err, "the second blip must get its own retry")
	assert.Equal(t, "done", reply)
	assert.Equal(t, int32(2), stub.calls.Load())
	assert.Equal(t, 5, provider.count())
}
