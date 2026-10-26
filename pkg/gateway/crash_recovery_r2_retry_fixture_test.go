//go:build goolm && stdjson

package gateway

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Transparent observer, not a recording/fake launcher: every Launch and
// Dispatch goes through the real SteerLauncher with identical arguments and
// results. The first real durable launch is held before task binding, while
// the other caller crosses its real locked transition/admission boundary.
type r2ObservedLauncher struct {
	steer.SessionLauncher
	entered chan steer.LaunchResult
	release chan struct{}
	first   atomic.Bool
}

func (l *r2ObservedLauncher) Launch(ctx context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	result, err := l.SessionLauncher.Launch(ctx, req)
	if err == nil && l.first.CompareAndSwap(false, true) {
		l.entered <- result
		select {
		case <-l.release:
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
	return result, err
}

// Each real session's own input decides its next reply. No global task-step
// count can hide a second fresh worker: each would request its own append.
type r2RetryProvider struct{ *crashTaskProvider }

func (p *r2RetryProvider) Chat(ctx context.Context, input []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	settled := false
	for _, m := range input {
		settled = settled || (m.Role == "tool" && m.ToolCallID == "retry-effect")
	}
	if !settled {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "retry-effect", Name: "append_file", Arguments: map[string]any{"path": p.path, "content": "RETRY\n"}}}, FinishReason: "tool_calls"}, nil
	}
	p.resultCommitted <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled
	}
}

type r2ExecutionTrace struct {
	mu       sync.Mutex
	requests map[string]int
	effects  map[string]int
}

func (h *r2ExecutionTrace) BeforeLLM(_ context.Context, request *agent.LLMHookRequest) (*agent.LLMHookRequest, agent.HookDecision, error) {
	h.mu.Lock()
	h.requests[request.Meta.SessionKey+"|"+request.Meta.TurnID]++
	h.mu.Unlock()
	return request, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
func (*r2ExecutionTrace) AfterLLM(_ context.Context, response *agent.LLMHookResponse) (*agent.LLMHookResponse, agent.HookDecision, error) {
	return response, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
func (*r2ExecutionTrace) BeforeTool(_ context.Context, call *agent.ToolCallHookRequest) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
	return call, agent.HookDecision{Action: agent.HookActionContinue}, nil
}
func (h *r2ExecutionTrace) AfterTool(_ context.Context, result *agent.ToolResultHookResponse) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
	if result.Tool == "append_file" && !result.Result.IsError {
		h.mu.Lock()
		h.effects[result.Meta.SessionKey+"|"+result.Meta.TurnID]++
		h.mu.Unlock()
	}
	return result, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

func (h *r2ExecutionTrace) snapshot(sessionID string) (map[string]int, map[string]int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	requests, effects := map[string]int{}, map[string]int{}
	for key, n := range h.requests {
		if strings.Contains(key, sessionID) {
			requests[key] = n
		}
	}
	for key, n := range h.effects {
		if strings.Contains(key, sessionID) {
			effects[key] = n
		}
	}
	return requests, effects
}
