package gateway

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const ceilingLiveModel = "ceiling-live-model"
const ceilingLiveToolName = "ceiling_live_note"
const ceilingLiveAnswer = "FOLLOWUPS-HANDLED-OK"

// Only the paid model and external note operation are scripted. Request capture
// observes the actual messages sent across the provider boundary.
type ceilingLiveProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
}

func (*ceilingLiveProvider) GetDefaultModel() string { return ceilingLiveModel }
func (p *ceilingLiveProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	call := len(p.requests)
	p.mu.Unlock()
	if call <= 2 {
		return &providers.LLMResponse{
			Content: fmt.Sprintf("Reading note %d.", call), FinishReason: "tool_calls",
			ToolCalls: []providers.ToolCall{{ID: fmt.Sprintf("live-note-%d", call), Type: "function", Name: ceilingLiveToolName,
				Function: &providers.FunctionCall{Name: ceilingLiveToolName, Arguments: fmt.Sprintf(`{"note":"note-%d"}`, call)}}},
		}, nil
	}
	if call == 3 {
		return &providers.LLMResponse{Content: ceilingLiveAnswer, FinishReason: "stop"}, nil
	}
	return nil, fmt.Errorf("unexpected model call %d: ceiling continuation must not renew itself", call)
}

func (p *ceilingLiveProvider) snapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

type ceilingLiveStreamingProvider struct{ *ceilingLiveProvider }

func (p *ceilingLiveStreamingProvider) ChatStream(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, messages, defs, model, options)
	if err == nil {
		chunk(response.Content)
	}
	return response, err
}

type ceilingLiveTool struct {
	tools.BaseTool
	calls       atomic.Int32
	entered     [2]chan struct{}
	release     [2]chan struct{}
	releaseOnce [2]sync.Once
}

func newCeilingLiveTool() *ceilingLiveTool {
	return &ceilingLiveTool{
		entered: [2]chan struct{}{make(chan struct{}), make(chan struct{})},
		release: [2]chan struct{}{make(chan struct{}), make(chan struct{})},
	}
}
func (*ceilingLiveTool) Name() string { return ceilingLiveToolName }
func (*ceilingLiveTool) Description() string {
	return "Read a fixture note at a gated process boundary."
}
func (*ceilingLiveTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*ceilingLiveTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string"}}}
}
func (tool *ceilingLiveTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	index := int(tool.calls.Add(1)) - 1
	if index >= len(tool.entered) {
		return &tools.ToolResult{ForLLM: "unexpected repeated note operation", IsError: true}
	}
	close(tool.entered[index])
	select {
	case <-tool.release[index]:
	case <-ctx.Done():
		return &tools.ToolResult{ForLLM: "note operation cancelled", IsError: true}
	}
	note, _ := args["note"].(string)
	return &tools.ToolResult{ForLLM: note + "-result"}
}
func (tool *ceilingLiveTool) open(index int) {
	tool.releaseOnce[index].Do(func() { close(tool.release[index]) })
}
func (tool *ceilingLiveTool) openAll() {
	for i := range tool.release {
		tool.open(i)
	}
}
