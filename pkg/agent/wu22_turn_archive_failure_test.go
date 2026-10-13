package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fault is armed only AFTER the real request/user write succeeds, at
// the external provider edge. Thus this specifically reaches finalizeTurn,
// not the already-checked user-message admission failure.
type wu22FinalWriteProvider struct {
	onReply func()
	calls   int
}

func (p *wu22FinalWriteProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.onReply != nil {
		p.onReply()
	}
	return &providers.LLMResponse{Content: "WU22 exact final answer"}, nil
}
func (*wu22FinalWriteProvider) GetDefaultModel() string { return "wu22-model" }
func (p *wu22FinalWriteProvider) ChatStream(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	chunk("WU22 exact final answer")
	return p.Chat(ctx, messages, defs, model, opts)
}

func TestWU22FinalizeTurnArchiveFailureIsVisibleInRealLoop(t *testing.T) {
	provider := &wu22FinalWriteProvider{}
	h := wu22NewHarness(t, provider)
	h.al.bus.SetStreamDelegate(&identityStampStreamDelegate{streamer: h.stream})
	var restore func()
	provider.onReply = func() { restore = wu22BlockModelWrite(t, h.store, h.sid) }
	_, err := h.al.runTurn(context.Background(), h.ts) // real conductor, all stages and defers
	require.Equal(t, 1, provider.calls, "instrument: the real loop must reach the provider before arming the fault")
	require.NotNil(t, restore, "instrument: final-write fault was installed")
	restore()
	h.assertSaveFailure(t, err)
	h.mu.Lock()
	defer h.mu.Unlock()
	var statuses []TurnEndStatus
	for _, event := range h.events {
		if event.Kind == EventKindTurnEnd {
			payload, ok := event.Payload.(TurnEndPayload)
			require.True(t, ok)
			statuses = append(statuses, payload.Status)
		}
	}
	assert.Equal(t, []TurnEndStatus{TurnEndStatusError}, statuses, "the real turn must not end completed after an archive failure")
}

func TestWU22FinalizeTurnHealthyStoreCompletesInRealLoop(t *testing.T) {
	provider := &wu22FinalWriteProvider{}
	h := wu22NewHarness(t, provider)
	h.al.bus.SetStreamDelegate(&identityStampStreamDelegate{streamer: h.stream})
	result, err := h.al.runTurn(context.Background(), h.ts)
	require.NoError(t, err)
	assert.Equal(t, "WU22 exact final answer", result.finalContent)
	assert.Equal(t, TurnEndStatusCompleted, result.status)
	assert.False(t, result.turnFailed)
	assert.Equal(t, 1, provider.calls)
	assert.Equal(t, []bool{false}, h.stream.failed, "positive control: same transport edge receives successful done stats")
	assert.Equal(t, []string{"WU22 exact final answer"}, h.stream.finalized)
	assert.Equal(t, []providers.Message{
		{Role: "user", Content: "WU22 input"},
		{Role: "assistant", Content: "WU22 exact final answer"},
	}, h.store.GetHistory(h.sid), "healthy final answer is durably model-visible")
	h.assertSiblingWritable(t)
}

// Tool names are an external tool boundary: registering distinct names and
// changing the second fixture's reported name triggers the real invariant.
// Neither the registry nor prepareToolSurface is mocked.
func wu22DuplicateSurface(h *wu22Harness) *agentLoopRunTurnRequest {
	a := &dupTool{name: "wu22_dup", scopeVal: tools.ScopeCore, sourceTag: "builtin"}
	b := &dupTool{name: "wu22_other", scopeVal: tools.ScopeCore, sourceTag: "mcp:fixture"}
	h.agent.Tools = tools.NewToolRegistry()
	h.agent.Tools.Register(a)
	h.agent.Tools.Register(b)
	b.name = a.name
	h.agent.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"wu22_dup": config.ToolPolicyAllow}})
	return h.responseStage().rq
}

func TestWU22PrepareToolSurfaceArchiveFailureEndsTurn(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rq := wu22DuplicateSurface(h)
	restore := wu22BlockModelWrite(t, h.store, h.sid)
	flow := rq.prepareToolSurface()
	restore()
	assert.Equal(t, agentLoopRunTurnRequestReturn, flow)
	assert.Equal(t, TurnEndStatusError, rq.ri.turnStatus)
	h.assertSaveFailure(t, rq.ret1)
}

func TestWU22PrepareToolSurfaceHealthyStorePersistsDenial(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rq := wu22DuplicateSurface(h)
	assert.Equal(t, agentLoopRunTurnRequestReturn, rq.prepareToolSurface(), "same synthetic-denial branch")
	require.ErrorContains(t, rq.ret1, "dedup invariant violated")
	var notSaved *messageNotSavedError
	assert.False(t, errors.As(rq.ret1, &notSaved), "healthy synthetic denial is not a save failure")
	assert.Equal(t, TurnEndStatusError, rq.ri.turnStatus)
	history := h.store.GetHistory(h.sid)
	require.Len(t, history, 1, "synthetic denial is actually archived")
	assert.Equal(t, "system", history[0].Role)
	assert.Contains(t, history[0].Content, `"error":"tool_assembly_duplicate"`)
	assert.Contains(t, history[0].Content, "wu22_dup")
	h.assertSiblingWritable(t)
}

func wu22ContinuationStage(h *wu22Harness) *agentLoopRunTurnResponse {
	h.ts.appendToAccumulator("WU22 pending prefix")
	rr := h.responseStage()
	rr.continuationChain = []providers.Message{{Role: "assistant", Content: "WU22 pending prefix"}}
	rr.rq.ri.rf.response = &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{ID: "wu22-call", Name: "read_file", Arguments: map[string]any{"path": "control.txt"}}},
	}
	return rr
}

func TestWU22FlushContinuationArchiveFailureStopsToolRound(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rr := wu22ContinuationStage(h)
	restore := wu22BlockModelWrite(t, h.store, h.sid)
	err := rr.recordToolCalls() // real consumer must stop on flush failure
	restore()
	h.assertSaveFailure(t, err)
	assert.Empty(t, rr.normalizedToolCalls, "no new tool round is admitted after the failed continuation flush")
	assert.Empty(t, rr.rq.ri.messages, "failed flush must not install the following tool-call message")
}

func TestWU22FlushContinuationHealthyStorePreservesOrder(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	rr := wu22ContinuationStage(h)
	require.NoError(t, rr.recordToolCalls())
	history := h.store.GetHistory(h.sid)
	require.Len(t, history, 2)
	assert.Equal(t, providers.Message{Role: "assistant", Content: "WU22 pending prefix"}, history[0])
	require.Len(t, history[1].ToolCalls, 1)
	assert.Equal(t, "wu22-call", history[1].ToolCalls[0].ID, "tool-call group follows persisted prefix")
	assert.Equal(t, "", h.ts.continuationAccumulated(), "healthy flush clears only once persisted")
	assert.Empty(t, rr.continuationChain)
	assert.False(t, h.ts.turnFailed)
	h.assertSiblingWritable(t)
}

func wu22SubTurnStage(t *testing.T, h *wu22Harness) *agentLoopRunTurnToolsExecute {
	t.Helper()
	issuer := providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "wu22-tool", Name: "read_file"}}}
	_, err := h.ts.appendWindowMessage(issuer, windowProducerAssistant)
	require.NoError(t, err)
	admitted := h.al.admitToolResult(h.ts, toolResultAdmission{Tool: "read_file", ToolCallID: "wu22-tool", Content: "WU22 tool answer", ParallelN: 1})
	require.NoError(t, admitted.Err)
	h.ts.pendingResults = make(chan *tools.ToolResult, 1)
	h.ts.pendingResults <- &tools.ToolResult{ForLLM: "WU22 child answer"}
	rr := h.responseStage()
	rr.rq.ri.messages = []providers.Message{issuer}
	rr.normalizedToolCalls = []providers.ToolCall{{ID: "wu22-tool", Name: "read_file"}}
	return &agentLoopRunTurnToolsExecute{
		rx:       &agentLoopRunTurnTools{ctx: context.Background(), rr: rr},
		toolName: "read_file", toolCallID: "wu22-tool",
		toolResult: &tools.ToolResult{ForLLM: "WU22 tool answer"}, contentForLLM: "WU22 tool answer",
		admitted: admitted, toolResultMsg: admitted.Message,
		tcRecord: session.ToolCall{ID: "wu22-tool", Tool: "read_file", Status: "success"},
	}
}

func TestWU22FinishCallSubTurnArchiveFailureEndsTurn(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	ex := wu22SubTurnStage(t, h)
	var restore func()
	// All earlier checked model/transcript writes must succeed. The real
	// tool-end event is the barrier immediately before the pending SubTurn cut.
	h.al.SetEventSyncTap(func(event Event) {
		h.mu.Lock()
		h.events = append(h.events, event)
		h.mu.Unlock()
		if event.Kind == EventKindToolExecEnd && restore == nil {
			restore = wu22BlockModelWrite(t, h.store, h.sid)
		}
	})
	flow := ex.finishCall(0)
	require.NotNil(t, restore, "instrument: preceding result checkpoint must succeed and reach the SubTurn cut")
	restore()
	assert.Equal(t, agentLoopRunTurnToolsExecuteReturn, flow, "failed SubTurn window write must not continue the turn")
	assert.Equal(t, TurnEndStatusError, ex.rx.rr.rq.ri.turnStatus)
	h.assertSaveFailure(t, ex.rx.ret1)
}

func TestWU22FinishCallSubTurnHealthyStorePersistsResult(t *testing.T) {
	h := wu22NewHarness(t, wu22HealthyProvider())
	ex := wu22SubTurnStage(t, h)
	require.Equal(t, agentLoopRunTurnToolsExecuteNext, ex.finishCall(0))
	require.NoError(t, ex.rx.ret1)
	history := h.store.GetHistory(h.sid)
	require.Len(t, history, 3)
	assert.Equal(t, providers.Message{Role: "user", Content: "[SubTurn Result] WU22 child answer"}, history[2])
	assert.Equal(t, history, ex.rx.rr.rq.ri.messages, "same healthy SubTurn result reaches the next provider request")
	assert.False(t, h.ts.turnFailed)
	h.assertSiblingWritable(t)
}
