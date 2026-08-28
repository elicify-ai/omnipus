// Package testutil provides shared test infrastructure for all Plan-3 PRs.
// It is intentionally not a _test.go file so it can be imported from any
// _test.go in the repo without import cycles.
package testutil

import (
	"context"
	"errors"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ErrNoMoreResponses signals the scenario has run out of scripted responses.
var ErrNoMoreResponses = errors.New("scenario provider: no more scripted responses")

// scenarioStep holds one scripted Chat turn.
type scenarioStep struct {
	resp *providers.LLMResponse
	err  error
}

// ScenarioProvider returns pre-scripted responses in order. Thread-safe.
// Each builder method appends one step; Chat consumes steps sequentially.
//
// Example:
//
//	p := NewScenario().
//	    WithText("Hello!").
//	    WithToolCall("bash", `{"cmd":"ls"}`).
//	    WithText("Done.")
type ScenarioProvider struct {
	mu           sync.Mutex
	steps        []scenarioStep
	idx          int
	callCount    int
	modelName    string
	lastMessages []providers.Message
	allRequests  [][]providers.Message
}

// NewScenario returns an empty ScenarioProvider. Default model name is "scripted-model".
func NewScenario() *ScenarioProvider {
	return &ScenarioProvider{modelName: "scripted-model"}
}

// WithText appends a plain assistant text response (no tool calls).
func (s *ScenarioProvider) WithText(content string) *ScenarioProvider {
	s.steps = append(s.steps, scenarioStep{
		resp: &providers.LLMResponse{
			Content:   content,
			ToolCalls: []providers.ToolCall{},
		},
	})
	return s
}

// WithUsage attaches token usage to the MOST RECENTLY appended step. It sets
// LLMResponse.Usage so the agent loop accumulates turn-level token/cost stats
// (the #411 path: ts.AddTurnStats fires when response.Usage != nil), which lets
// tests assert that intermediate assistant entries carry 0 tokens while the
// final entry carries the turn total — i.e. no double-counting.
//
// Panics if called before any step has been appended (a test-authoring bug).
func (s *ScenarioProvider) WithUsage(promptTokens, completionTokens int) *ScenarioProvider {
	if len(s.steps) == 0 {
		panic("scenario provider: WithUsage called before any step was appended")
	}
	last := &s.steps[len(s.steps)-1]
	if last.resp == nil {
		panic("scenario provider: WithUsage on an error step (no response to annotate)")
	}
	last.resp.Usage = &providers.UsageInfo{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
	return s
}

// WithTextAndToolCall appends a response that carries both a narration text
// segment AND a single tool call. This mirrors the real-model pattern where the
// LLM writes a sentence ("Okay, I've saved your name.") and then requests a tool
// in the same response — the scenario used to reproduce bug #416.
func (s *ScenarioProvider) WithTextAndToolCall(text, name, argsJSON string) *ScenarioProvider {
	fc := providers.FunctionCall{
		Name:      name,
		Arguments: argsJSON,
	}
	s.steps = append(s.steps, scenarioStep{
		resp: &providers.LLMResponse{
			Content: text,
			ToolCalls: []providers.ToolCall{
				{
					ID:       name + "-0",
					Function: &fc,
				},
			},
		},
	})
	return s
}

// WithToolCall appends a response that invokes a single tool with the given JSON args.
func (s *ScenarioProvider) WithToolCall(name, argsJSON string) *ScenarioProvider {
	fc := providers.FunctionCall{
		Name:      name,
		Arguments: argsJSON,
	}
	return s.WithToolCalls([]providers.ToolCall{
		{
			ID:       name + "-0",
			Function: &fc,
		},
	})
}

// WithToolCalls appends a response with multiple parallel tool calls in a single turn.
func (s *ScenarioProvider) WithToolCalls(calls []providers.ToolCall) *ScenarioProvider {
	s.steps = append(s.steps, scenarioStep{
		resp: &providers.LLMResponse{
			Content:   "",
			ToolCalls: calls,
		},
	})
	return s
}

// WithError appends a step that returns err on the next Chat call.
func (s *ScenarioProvider) WithError(err error) *ScenarioProvider {
	s.steps = append(s.steps, scenarioStep{err: err})
	return s
}

// WithModelName sets the provider's model name (default "scripted-model").
func (s *ScenarioProvider) WithModelName(name string) *ScenarioProvider {
	s.modelName = name
	return s
}

// Chat pops the next scripted step. Returns ErrNoMoreResponses once exhausted.
// If ctx is already done when Chat is called, ctx.Err() is returned immediately.
// Implements providers.LLMProvider.
func (s *ScenarioProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	// Respect context cancellation before doing any work.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	s.mu.Lock()
	// Record the messages passed to this Chat call for inspection by tests.
	s.lastMessages = append([]providers.Message(nil), messages...)
	s.allRequests = append(s.allRequests, s.lastMessages)
	if s.idx >= len(s.steps) {
		s.mu.Unlock()
		return nil, ErrNoMoreResponses
	}
	step := s.steps[s.idx]
	s.idx++
	s.callCount++
	s.mu.Unlock()

	return step.resp, step.err
}

// LastMessages returns a snapshot of the messages passed to the most recent
// Chat call. Returns nil if Chat has never been called. Thread-safe.
// Used by V2.D integration tests to verify the LLM prompt does not contain
// orphaned tool_call entries after SIGKILL recovery.
func (s *ScenarioProvider) LastMessages() []providers.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastMessages == nil {
		return nil
	}
	cp := make([]providers.Message, len(s.lastMessages))
	copy(cp, s.lastMessages)
	return cp
}

// AllRequests returns a snapshot of the messages passed to EVERY Chat call,
// in call order (ADR-066 T066-13: the long-turn test asserts every request
// stayed under the budget B at every iteration, not just the last one).
// Thread-safe. Each element is the same snapshot LastMessages would have
// returned right after that call.
func (s *ScenarioProvider) AllRequests() [][]providers.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([][]providers.Message, len(s.allRequests))
	copy(cp, s.allRequests)
	return cp
}

// GetDefaultModel returns the configured model name.
// Implements providers.LLMProvider.
func (s *ScenarioProvider) GetDefaultModel() string {
	return s.modelName
}

// CallCount returns how many times Chat was invoked. Safe to read concurrently.
func (s *ScenarioProvider) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.callCount
}

// Remaining returns how many steps haven't been consumed yet.
func (s *ScenarioProvider) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steps) - s.idx
}

// Reset clears consumption state; the scripted steps are preserved.
// Useful between sub-tests that share the same ScenarioProvider.
func (s *ScenarioProvider) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idx = 0
	s.callCount = 0
	s.allRequests = nil
}
