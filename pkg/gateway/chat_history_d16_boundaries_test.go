package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// External-model transport edge only. Every returned usage fact is frozen by
// the independent D-U2 oracle; the engine/accounting/writer are never doubled.
type historyD16Reply struct {
	content string
	calls   []providers.ToolCall
	usage   providers.UsageInfo
}

type historyD16Provider struct {
	mu      sync.Mutex
	replies []historyD16Reply
	inputs  [][]providers.Message
}

func (*historyD16Provider) GetDefaultModel() string { return "d16-frozen-model" }

func (p *historyD16Provider) Chat(_ context.Context, input []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := len(p.inputs)
	p.inputs = append(p.inputs, append([]providers.Message(nil), input...))
	if index >= len(p.replies) {
		return nil, fmt.Errorf("D16 external fixture: unexpected provider call %d", index+1)
	}
	step := p.replies[index]
	usage := step.usage
	calls := make([]providers.ToolCall, len(step.calls))
	for i, call := range step.calls {
		calls[i] = call
		if call.Arguments != nil {
			calls[i].Arguments = make(map[string]any, len(call.Arguments))
			for key, value := range call.Arguments {
				calls[i].Arguments[key] = value
			}
		}
	}
	reason := "stop"
	if len(calls) > 0 {
		reason = "tool_calls"
	}
	return &providers.LLMResponse{Content: step.content, ToolCalls: calls, Usage: &usage, FinishReason: reason}, nil
}

func (p *historyD16Provider) ChatStream(ctx context.Context, input []providers.Message, definitions []providers.ToolDefinition, model string, options map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, input, definitions, model, options)
	if err != nil {
		return nil, err
	}
	if response.Content != "" {
		onChunk(response.Content)
	}
	return response, nil
}

var _ providers.StreamingProvider = (*historyD16Provider)(nil)

func (p *historyD16Provider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inputs)
}

const historyD16ToolName = "d16_external_boundary"

// The external tool boundary is controllable; registration, policy resolution,
// engine dispatch/start, Stop, result admission and persistence are all real.
type historyD16Tool struct {
	tools.BaseTool
	mu          sync.Mutex
	seen        []string
	started     chan struct{}
	cancelled   chan struct{}
	release     chan struct{}
	finished    chan struct{}
	lateSuccess bool
	startOnce   sync.Once
	cancelOnce  sync.Once
	releaseOnce sync.Once
	finishOnce  sync.Once
}

func newHistoryD16Tool(late bool) *historyD16Tool {
	return &historyD16Tool{started: make(chan struct{}), cancelled: make(chan struct{}),
		release: make(chan struct{}), finished: make(chan struct{}), lateSuccess: late}
}

func (*historyD16Tool) Name() string           { return historyD16ToolName }
func (*historyD16Tool) Description() string    { return "D16 controlled external execution boundary" }
func (*historyD16Tool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*historyD16Tool) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"step"},
		"properties": map[string]any{"step": map[string]any{"type": "string", "enum": []string{"completed", "blocked", "queued"}}}}
}

func (tool *historyD16Tool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	step, ok := args["step"].(string)
	if !ok {
		return tools.ErrorResult("D16 fixture requires a string step").WithError(errors.New("invalid fixture step"))
	}
	tool.mu.Lock()
	tool.seen = append(tool.seen, step)
	tool.mu.Unlock()
	switch step {
	case "completed":
		return tools.NewToolResult("D16 completed result")
	case "queued":
		return tools.NewToolResult("D16 queued call unexpectedly executed")
	case "blocked":
		tool.startOnce.Do(func() { close(tool.started) })
		defer tool.finishOnce.Do(func() { close(tool.finished) })
		<-ctx.Done() // the real forced Stop reaches the execution context
		tool.cancelOnce.Do(func() { close(tool.cancelled) })
		if tool.lateSuccess {
			<-tool.release // let real abandonment happen before the late result
			return tools.NewToolResult("D16 late success must not replace cancellation")
		}
		return tools.ErrorResult(ctx.Err().Error()).WithError(ctx.Err())
	default:
		return tools.ErrorResult("D16 fixture has an unknown step").WithError(errors.New("unknown fixture step"))
	}
}

func (tool *historyD16Tool) unblock() { tool.releaseOnce.Do(func() { close(tool.release) }) }
func (tool *historyD16Tool) invocations() []string {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	return append([]string(nil), tool.seen...)
}
