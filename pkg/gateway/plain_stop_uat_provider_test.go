// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	plainStopRootPrompt      = "Start the plain Stop regression helpers and keep working until I stop you."
	plainStopUnrelatedPrompt = "Keep this unrelated chat working; the other chat's Stop must not affect it."
	plainStopContinuePrompt  = "I explicitly continue this stopped chat. Reply with CONTINUED BY HUMAN."
	plainStopContinueReply   = "CONTINUED BY HUMAN"
	plainStopHandbackReply   = "AUTOMATIC HELPER HANDBACK TURN"
	plainStopHelperReply     = "HELPER WORK FINISHED"
	plainStopBranchAgent     = "plain-stop-branch"
	plainStopLeafAgent       = "plain-stop-leaf"
)

// This is the only scripted dependency: the external model. It emits actual
// delegate calls and parks actual provider requests. All IDs/contexts are
// supplied by production admission; the fixture never injects turn states.
type plainStopUATProvider struct {
	nested     bool
	rootFinish chan struct{}
	leafFinish chan struct{}
	cleanup    chan struct{}
	mu         sync.Mutex
	requests   map[string][]plainStopUATRequest
	held       map[string]context.Context
}

type plainStopUATRequest struct {
	agentID  string
	lastUser string
}

func newPlainStopUATProvider(nested bool) *plainStopUATProvider {
	return &plainStopUATProvider{
		nested: nested, rootFinish: make(chan struct{}), leafFinish: make(chan struct{}), cleanup: make(chan struct{}),
		requests: make(map[string][]plainStopUATRequest), held: make(map[string]context.Context),
	}
}

func (*plainStopUATProvider) GetDefaultModel() string { return "plain-stop-uat-provider" }

func (p *plainStopUATProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	id, agentID := tools.ToolTranscriptSessionID(ctx), tools.ToolAgentID(ctx)
	if id == "" || agentID == "" {
		return nil, fmt.Errorf("plain Stop fixture: provider lacks production session/agent identity: %q/%q", id, agentID)
	}
	var lastUser string
	for _, message := range messages {
		if message.Role == "user" {
			lastUser = message.Content
		}
	}
	p.mu.Lock()
	p.requests[id] = append(p.requests[id], plainStopUATRequest{agentID: agentID, lastUser: lastUser})
	call := len(p.requests[id])
	p.mu.Unlock()

	switch agentID {
	case "mia":
		if lastUser == plainStopContinuePrompt {
			return &providers.LLMResponse{Content: plainStopContinueReply, FinishReason: "stop"}, nil
		}
		if call == 1 && lastUser == plainStopRootPrompt {
			calls := []providers.ToolCall{plainStopDelegateCall("plain-stop-sibling", plainStopLeafAgent, "Work as the direct helper until stopped.")}
			if p.nested {
				calls = append(calls, plainStopDelegateCall("plain-stop-branch", plainStopBranchAgent, "Delegate one grandchild, then keep working until stopped."))
			}
			return &providers.LLMResponse{ToolCalls: calls, FinishReason: "tool_calls"}, nil
		}
		if call == 1 && lastUser != plainStopUnrelatedPrompt {
			return nil, fmt.Errorf("plain Stop fixture: unexpected first human input %q", lastUser)
		}
		if call >= 3 {
			return &providers.LLMResponse{Content: plainStopHandbackReply, FinishReason: "stop"}, nil
		}
		return p.park(ctx, id, p.rootFinish, "The original parent turn has finished.")
	case plainStopBranchAgent:
		if call == 1 {
			return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{
				plainStopDelegateCall("plain-stop-grandchild", plainStopLeafAgent, "Work as the grandchild until stopped."),
			}}, nil
		}
		return p.park(ctx, id, p.rootFinish, "The branch turn has finished.")
	case plainStopLeafAgent:
		return p.park(ctx, id, p.leafFinish, plainStopHelperReply)
	default:
		return nil, fmt.Errorf("plain Stop fixture: unexpected agent %q", agentID)
	}
}

func plainStopDelegateCall(callID, agentID, task string) providers.ToolCall {
	// The model's arguments go through the registered production delegate tool.
	arguments, err := json.Marshal(map[string]any{"action": "run", "agent_id": agentID, "task": task})
	if err != nil {
		panic(err) // These three string arguments are always JSON encodable.
	}
	return providers.ToolCall{ID: callID, Type: "function", Name: "delegate", Function: &providers.FunctionCall{
		Name: "delegate", Arguments: string(arguments),
	}}
}

func (p *plainStopUATProvider) park(ctx context.Context, id string, finish <-chan struct{}, answer string) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.held[id] = ctx
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.cleanup:
		return nil, context.Canceled
	case <-finish:
		// Cancellation wins even if the provider response becomes ready at once.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &providers.LLMResponse{Content: answer, FinishReason: "stop"}, nil
	}
}

func (p *plainStopUATProvider) contextFor(id string) context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.held[id]
}

func (p *plainStopUATProvider) requestsFor(id string) []plainStopUATRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]plainStopUATRequest(nil), p.requests[id]...)
}
