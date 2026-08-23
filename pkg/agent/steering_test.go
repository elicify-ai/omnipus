package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- steeringQueue unit tests ---

func TestSteeringQueue_PushDequeue_OneAtATime(t *testing.T) {
	sq := newSteeringQueue(SteeringOneAtATime)

	sq.push(providers.Message{Role: "user", Content: "msg1"})
	sq.push(providers.Message{Role: "user", Content: "msg2"})
	sq.push(providers.Message{Role: "user", Content: "msg3"})

	if sq.len() != 3 {
		t.Fatalf("expected 3 messages, got %d", sq.len())
	}

	msgs := sq.dequeue()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in one-at-a-time mode, got %d", len(msgs))
	}
	if msgs[0].Content != "msg1" {
		t.Fatalf("expected 'msg1', got %q", msgs[0].Content)
	}
	if sq.len() != 2 {
		t.Fatalf("expected 2 remaining, got %d", sq.len())
	}

	msgs = sq.dequeue()
	if len(msgs) != 1 || msgs[0].Content != "msg2" {
		t.Fatalf("expected 'msg2', got %v", msgs)
	}

	msgs = sq.dequeue()
	if len(msgs) != 1 || msgs[0].Content != "msg3" {
		t.Fatalf("expected 'msg3', got %v", msgs)
	}

	msgs = sq.dequeue()
	if msgs != nil {
		t.Fatalf("expected nil from empty queue, got %v", msgs)
	}
}

func TestSteeringQueue_PushDequeue_All(t *testing.T) {
	sq := newSteeringQueue(SteeringAll)

	sq.push(providers.Message{Role: "user", Content: "msg1"})
	sq.push(providers.Message{Role: "user", Content: "msg2"})
	sq.push(providers.Message{Role: "user", Content: "msg3"})

	msgs := sq.dequeue()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages in all mode, got %d", len(msgs))
	}
	if msgs[0].Content != "msg1" || msgs[1].Content != "msg2" || msgs[2].Content != "msg3" {
		t.Fatalf("unexpected messages: %v", msgs)
	}

	if sq.len() != 0 {
		t.Fatalf("expected 0 remaining, got %d", sq.len())
	}

	msgs = sq.dequeue()
	if msgs != nil {
		t.Fatalf("expected nil from empty queue, got %v", msgs)
	}
}

func TestSteeringQueue_EmptyDequeue(t *testing.T) {
	sq := newSteeringQueue(SteeringOneAtATime)
	if msgs := sq.dequeue(); msgs != nil {
		t.Fatalf("expected nil, got %v", msgs)
	}
}

func TestSteeringQueue_SetMode(t *testing.T) {
	sq := newSteeringQueue(SteeringOneAtATime)
	if sq.getMode() != SteeringOneAtATime {
		t.Fatalf("expected one-at-a-time, got %v", sq.getMode())
	}

	sq.setMode(SteeringAll)
	if sq.getMode() != SteeringAll {
		t.Fatalf("expected all, got %v", sq.getMode())
	}

	// Push two messages and verify all-mode drains them
	sq.push(providers.Message{Role: "user", Content: "a"})
	sq.push(providers.Message{Role: "user", Content: "b"})

	msgs := sq.dequeue()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after mode switch, got %d", len(msgs))
	}
}

func TestSteeringQueue_ConcurrentAccess(t *testing.T) {
	sq := newSteeringQueue(SteeringOneAtATime)

	var wg sync.WaitGroup
	const n = MaxQueueSize

	// Push from multiple goroutines
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sq.push(providers.Message{Role: "user", Content: fmt.Sprintf("msg%d", i)})
		}(i)
	}
	wg.Wait()

	if sq.len() != n {
		t.Fatalf("expected %d messages, got %d", n, sq.len())
	}

	// Drain from multiple goroutines
	var drained int
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if msgs := sq.dequeue(); len(msgs) > 0 {
				mu.Lock()
				drained += len(msgs)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if drained != n {
		t.Fatalf("expected to drain %d messages, got %d", n, drained)
	}
}

func TestSteeringQueue_Overflow(t *testing.T) {
	sq := newSteeringQueue(SteeringOneAtATime)

	// Fill the queue up to its maximum capacity
	for i := 0; i < MaxQueueSize; i++ {
		err := sq.push(providers.Message{Role: "user", Content: fmt.Sprintf("msg%d", i)})
		if err != nil {
			t.Fatalf("unexpected error pushing message %d: %v", i, err)
		}
	}

	// Sanity check: ensure the queue is actually full
	if sq.len() != MaxQueueSize {
		t.Fatalf("expected queue length %d, got %d", MaxQueueSize, sq.len())
	}

	// Attempt to push one more message, which MUST fail
	err := sq.push(providers.Message{Role: "user", Content: "overflow_msg"})

	// Assert the error happened and is the exact one we expect
	if err == nil {
		t.Fatal("expected an error when pushing to a full queue, but got nil")
	}

	expectedErr := "steering queue is full"
	if err.Error() != expectedErr {
		t.Errorf("expected error message %q, got %q", expectedErr, err.Error())
	}
}

func TestParseSteeringMode(t *testing.T) {
	tests := []struct {
		input    string
		expected SteeringMode
	}{
		{"", SteeringOneAtATime},
		{"one-at-a-time", SteeringOneAtATime},
		{"all", SteeringAll},
		{"unknown", SteeringOneAtATime},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseSteeringMode(tt.input); got != tt.expected {
				t.Fatalf("parseSteeringMode(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// --- AgentLoop steering integration tests ---

func TestAgentLoop_Steer_Enqueues(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if cfg == nil {
		t.Fatal("expected config to be initialized")
	}
	if msgBus == nil {
		t.Fatal("expected message bus to be initialized")
	}
	if provider == nil {
		t.Fatal("expected provider to be initialized")
	}

	al.Steer(providers.Message{Role: "user", Content: "interrupt me"})

	if al.steering.len() != 1 {
		t.Fatalf("expected 1 steering message, got %d", al.steering.len())
	}

	msgs := al.dequeueSteeringMessages()
	if len(msgs) != 1 || msgs[0].Content != "interrupt me" {
		t.Fatalf("unexpected dequeued message: %v", msgs)
	}
}

func TestAgentLoop_SteeringMode_GetSet(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if cfg == nil {
		t.Fatal("expected config to be initialized")
	}
	if msgBus == nil {
		t.Fatal("expected message bus to be initialized")
	}
	if provider == nil {
		t.Fatal("expected provider to be initialized")
	}

	if al.SteeringMode() != SteeringOneAtATime {
		t.Fatalf("expected default mode one-at-a-time, got %v", al.SteeringMode())
	}

	al.SetSteeringMode(SteeringAll)
	if al.SteeringMode() != SteeringAll {
		t.Fatalf("expected all mode, got %v", al.SteeringMode())
	}
}

func TestAgentLoop_SteeringMode_ConfiguredFromConfig(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				SteeringMode:      "all",
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	if al.SteeringMode() != SteeringAll {
		t.Fatalf("expected 'all' mode from config, got %v", al.SteeringMode())
	}
}

func TestAgentLoop_Continue_NoMessages(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if cfg == nil {
		t.Fatal("expected config to be initialized")
	}
	if msgBus == nil {
		t.Fatal("expected message bus to be initialized")
	}
	if provider == nil {
		t.Fatal("expected provider to be initialized")
	}

	resp, err := al.Continue(context.Background(), "test-session", "test", "chat1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "" {
		t.Fatalf("expected empty response for no steering messages, got %q", resp)
	}
}

func TestAgentLoop_Continue_WithMessages(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err = os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &simpleMockProvider{response: "continued response"}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	al.Steer(providers.Message{Role: "user", Content: "new direction"})

	resp, err := al.Continue(context.Background(), "test-session", "test", "chat1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "continued response" {
		t.Fatalf("expected 'continued response', got %q", resp)
	}
}

// TestSessionWorker_DifferentScopesGetIndependentWorkers verifies that two
// messages for different scopes resolve to different scope keys (which is the
// pre-condition for the dispatcher to create independent workers).
// With per-session workers, messages for different scopes are routed into
// separate workers — there is no requeue path.
func TestSessionWorker_DifferentScopesGetIndependentWorkers(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Session: config.SessionConfig{
			DMScope: "per-peer",
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	msg1 := bus.InboundMessage{
		Channel: "telegram",
		Sender: bus.SenderInfo{
			CanonicalID: "user1",
		},
		ChatID:  "chat1",
		Content: "session one",
		Peer: bus.Peer{
			Kind: bus.PeerDirect,
			ID:   "user1",
		},
	}
	msg2 := bus.InboundMessage{
		Channel: "telegram",
		Sender: bus.SenderInfo{
			CanonicalID: "user2",
		},
		ChatID:  "chat2",
		Content: "session two",
		Peer: bus.Peer{
			Kind: bus.PeerDirect,
			ID:   "user2",
		},
	}

	scope1, _, ok1 := al.resolveSteeringTarget(msg1)
	scope2, _, ok2 := al.resolveSteeringTarget(msg2)
	if !ok1 || !ok2 {
		t.Fatal("expected both messages to resolve to steering scopes")
	}
	if scope1 == scope2 {
		t.Fatalf("expected different scopes, both resolved to %q", scope1)
	}

	// Spawn workers for both scopes — they must be distinct.
	w1 := newSessionWorker(scope1, al, func() {})
	w2 := newSessionWorker(scope2, al, func() {})
	if w1.scope == w2.scope {
		t.Fatalf("worker scopes must differ: %q == %q", w1.scope, w2.scope)
	}

	// Cancel workers immediately (we only needed the scope check).
	w1.cancel()
	w2.cancel()
}

// slowTool simulates a tool that takes some time to execute.
type slowTool struct {
	name     string
	duration time.Duration
	execCh   chan struct{} // closed when Execute starts
}

func (t *slowTool) Name() string                 { return t.name }
func (t *slowTool) Description() string          { return "slow tool for testing" }
func (t *slowTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *slowTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *slowTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *slowTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t.execCh != nil {
		close(t.execCh)
	}
	time.Sleep(t.duration)
	return tools.SilentResult(fmt.Sprintf("executed %s", t.name))
}

// toolCallProvider returns an LLM response with tool calls on the first call,
// then a direct response on subsequent calls.
type toolCallProvider struct {
	mu        sync.Mutex
	calls     int
	toolCalls []providers.ToolCall
	finalResp string
}

func (m *toolCallProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++

	if m.calls == 1 && len(m.toolCalls) > 0 {
		return &providers.LLMResponse{
			Content:   "",
			ToolCalls: m.toolCalls,
		}, nil
	}

	return &providers.LLMResponse{
		Content:   m.finalResp,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *toolCallProvider) GetDefaultModel() string {
	return "tool-call-mock"
}

type gracefulCaptureProvider struct {
	mu                 sync.Mutex
	calls              int
	toolCalls          []providers.ToolCall
	finalResp          string
	terminalMessages   []providers.Message
	terminalToolsCount int
}

func (p *gracefulCaptureProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++

	if p.calls == 1 {
		return &providers.LLMResponse{
			ToolCalls: p.toolCalls,
		}, nil
	}

	p.terminalMessages = append([]providers.Message(nil), messages...)
	p.terminalToolsCount = len(tools)
	return &providers.LLMResponse{
		Content: p.finalResp,
	}, nil
}

func (p *gracefulCaptureProvider) GetDefaultModel() string {
	return "graceful-capture-mock"
}

type lateSteeringProvider struct {
	mu                 sync.Mutex
	calls              int
	firstCallStarted   chan struct{}
	releaseFirstCall   chan struct{}
	firstStartOnce     sync.Once
	secondCallMessages []providers.Message
}

func (p *lateSteeringProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()

	if call == 1 {
		p.firstStartOnce.Do(func() { close(p.firstCallStarted) })
		<-p.releaseFirstCall
		return &providers.LLMResponse{Content: "first response"}, nil
	}

	p.mu.Lock()
	p.secondCallMessages = append([]providers.Message(nil), messages...)
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "continued response"}, nil
}

func (p *lateSteeringProvider) GetDefaultModel() string {
	return "late-steering-mock"
}

type blockingDirectProvider struct {
	mu           sync.Mutex
	calls        int
	firstStarted chan struct{}
	releaseFirst chan struct{}
	firstResp    string
	finalResp    string
}

func (p *blockingDirectProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	firstStarted := p.firstStarted
	releaseFirst := p.releaseFirst
	firstResp := p.firstResp
	finalResp := p.finalResp
	if call == 1 && p.firstStarted != nil {
		close(p.firstStarted)
		p.firstStarted = nil
	}
	p.mu.Unlock()

	if call == 1 {
		select {
		case <-releaseFirst:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &providers.LLMResponse{Content: firstResp}, nil
	}

	_ = firstStarted
	return &providers.LLMResponse{Content: finalResp}, nil
}

func (p *blockingDirectProvider) GetDefaultModel() string {
	return "blocking-direct-mock"
}

type interruptibleTool struct {
	name    string
	started chan struct{}
	once    sync.Once
}

func (t *interruptibleTool) Name() string                 { return t.name }
func (t *interruptibleTool) Description() string          { return "interruptible tool for testing" }
func (t *interruptibleTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *interruptibleTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *interruptibleTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *interruptibleTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t.started != nil {
		t.once.Do(func() { close(t.started) })
	}
	<-ctx.Done()
	return tools.ErrorResult(ctx.Err().Error()).WithError(ctx.Err())
}

func TestAgentLoop_Steering_SkipsRemainingTools(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	tool1ExecCh := make(chan struct{})
	tool1 := &slowTool{name: "tool_one", duration: 50 * time.Millisecond, execCh: tool1ExecCh}
	tool2 := &slowTool{name: "tool_two", duration: 50 * time.Millisecond}

	provider := &toolCallProvider{
		toolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Name: "tool_one",
				Function: &providers.FunctionCall{
					Name:      "tool_one",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
			{
				ID:   "call_2",
				Type: "function",
				Name: "tool_two",
				Function: &providers.FunctionCall{
					Name:      "tool_two",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
		},
		finalResp: "steered response",
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	al.RegisterTool(tool1)
	al.RegisterTool(tool2)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): both exercised
	// tools need an explicit agent-level grant, or they fail closed to "deny"
	// before tool_one ever executes and signals tool1ExecCh.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"tool_one": "allow", "tool_two": "allow"},
	})

	// Start processing in a goroutine
	type result struct {
		resp string
		err  error
	}
	resultCh := make(chan result, 1)

	go func() {
		resp, err := al.ProcessDirectWithChannel(
			context.Background(),
			"do something",
			"test-session",
			"test",
			"chat1",
		)
		resultCh <- result{resp, err}
	}()

	// Wait for tool_one to start executing, then enqueue a steering message
	select {
	case <-tool1ExecCh:
		// tool_one has started executing
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tool_one to start")
	}

	al.Steer(providers.Message{Role: "user", Content: "change course"})

	// Get the result
	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.resp != "steered response" {
			t.Fatalf("expected 'steered response', got %q", r.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for agent loop to complete")
	}

	// The provider should have been called twice:
	// 1. first call returned tool calls
	// 2. second call (after steering) returned the final response
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 provider calls, got %d", calls)
	}
}

func TestAgentLoop_Steering_InitialPoll(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err = os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	// Provider that captures messages it receives
	var capturedMessages []providers.Message
	var capMu sync.Mutex
	provider := &capturingMockProvider{
		response: "ack",
		captureFn: func(msgs []providers.Message) {
			capMu.Lock()
			capturedMessages = make([]providers.Message, len(msgs))
			copy(capturedMessages, msgs)
			capMu.Unlock()
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	// Enqueue a steering message before processing starts
	al.Steer(providers.Message{Role: "user", Content: "pre-enqueued steering"})

	// Process a normal message - the initial steering poll should inject the steering message
	_, err = al.ProcessDirectWithChannel(
		context.Background(),
		"initial message",
		"test-session",
		"test",
		"chat1",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The steering message should have been injected into the conversation
	capMu.Lock()
	msgs := capturedMessages
	capMu.Unlock()

	// Look for the steering message in the captured messages
	// The steering content must reach the LLM context. It may be merged into an
	// adjacent same-role user message by normalizeMessagesForProvider (provider
	// compatibility), so assert on substring presence rather than a standalone
	// message.
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "pre-enqueued steering") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected steering message to be injected into conversation context; captured messages: %+v", msgs)
	}
}

func TestAgentLoop_Run_AutoContinuesLateSteeringMessage(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &lateSteeringProvider{
		firstCallStarted: make(chan struct{}),
		releaseFirstCall: make(chan struct{}),
	}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- al.Run(runCtx)
	}()

	first := bus.InboundMessage{
		Channel: "test",
		Sender: bus.SenderInfo{
			CanonicalID: "user1",
		},
		ChatID:  "chat1",
		Content: "first message",
		Peer: bus.Peer{
			Kind: bus.PeerDirect,
			ID:   "user1",
		},
	}
	late := bus.InboundMessage{
		Channel: "test",
		Sender: bus.SenderInfo{
			CanonicalID: "user1",
		},
		ChatID:  "chat1",
		Content: "late append",
		Peer: bus.Peer{
			Kind: bus.PeerDirect,
			ID:   "user1",
		},
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()
	if err := msgBus.PublishInbound(pubCtx, first); err != nil {
		t.Fatalf("publish first inbound: %v", err)
	}

	select {
	case <-provider.firstCallStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first provider call to start")
	}

	if err := msgBus.PublishInbound(pubCtx, late); err != nil {
		t.Fatalf("publish late inbound: %v", err)
	}

	// PublishInbound returns once the message is on the bus, NOT once the loop
	// has queued it as steering. Releasing the first provider call before that
	// lets the turn finish with nothing to continue on, and the assertion below
	// sees "first response" -- observed on CI 2026-08-17. Wait for the message
	// to actually land in the steering queue first.
	waitFor(t, 2*time.Second, func() bool {
		return al.steering != nil && al.steering.len() > 0
	})

	close(provider.releaseFirstCall)

	subCtx, subCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer subCancel()

	var out1 bus.OutboundMessage
	select {
	case out1 = <-msgBus.OutboundChan():
	case <-subCtx.Done():
		t.Fatal("expected outbound response")
	}
	if out1.Content != "continued response" {
		t.Fatalf("expected continued response, got %q", out1.Content)
	}

	noExtraCtx, cancelNoExtra := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelNoExtra()
	select {
	case out2 := <-msgBus.OutboundChan():
		t.Fatalf("expected stale direct response to be suppressed, got extra outbound %q", out2.Content)
	case <-noExtraCtx.Done():
	}

	cancelRun()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run to stop")
	}

	provider.mu.Lock()
	calls := provider.calls
	secondMessages := append([]providers.Message(nil), provider.secondCallMessages...)
	provider.mu.Unlock()

	if calls != 2 {
		t.Fatalf("expected 2 provider calls, got %d", calls)
	}

	// The late message must reach the follow-up turn's context. It may be merged
	// into an adjacent same-role user message by normalizeMessagesForProvider, so
	// assert on substring presence within a user message.
	foundLateMessage := false
	for _, msg := range secondMessages {
		if msg.Role == "user" && strings.Contains(msg.Content, "late append") {
			foundLateMessage = true
			break
		}
	}
	if !foundLateMessage {
		t.Fatalf(
			"expected queued late message to be processed in an automatic follow-up turn; second-call messages: %+v",
			secondMessages,
		)
	}
}

func TestAgentLoop_Steering_DirectResponseContinuesWithQueuedMessage(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	sessionKey := routing.BuildAgentMainSessionKey("mia")
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	provider := &blockingDirectProvider{
		firstStarted: firstStarted,
		releaseFirst: releaseFirst,
		firstResp:    "stale direct response",
		finalResp:    "fresh response after steering",
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	resultCh := make(chan struct {
		resp string
		err  error
	}, 1)
	go func() {
		resp, err := al.ProcessDirectWithChannel(
			context.Background(),
			"initial request",
			sessionKey,
			"test",
			"chat1",
		)
		resultCh <- struct {
			resp string
			err  error
		}{resp: resp, err: err}
	}()

	// Use the local channel snapshot to avoid a race with Chat() setting
	// provider.firstStarted = nil under its mutex.
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first LLM call to start")
	}

	if err := al.Steer(providers.Message{Role: "user", Content: "follow-up instruction"}); err != nil {
		t.Fatalf("Steer failed: %v", err)
	}
	close(releaseFirst)

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("unexpected error: %v", result.err)
		}
		if result.resp != "fresh response after steering" {
			t.Fatalf("expected refreshed response, got %q", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for ProcessDirectWithChannel")
	}

	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 provider calls, got %d", calls)
	}

	if msgs := al.dequeueSteeringMessagesForScope(sessionKey); len(msgs) != 0 {
		t.Fatalf("expected steering queue to be empty after continuation, got %v", msgs)
	}
}

func TestAgentLoop_Continue_PreservesSteeringMedia(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err = os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	store := media.NewFileMediaStore()
	pngPath := filepath.Join(tmpDir, "steer.png")
	// Real 1x1 RGBA PNG bytes (Go image/png encoder output). The previous
	// hand-rolled byte block was a malformed PNG that the normalizer
	// (correctly) rejected as decode-failed; the real bytes below always
	// decode to a 1x1 transparent-gray image.
	pngHeader := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x10, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x62, 0x6A, 0x68, 0x68, 0x00,
		0x04, 0x00, 0x00, 0xFF, 0xFF, 0x03, 0x0C, 0x01,
		0x83, 0x71, 0x4B, 0xD2, 0x4E, 0x00, 0x00, 0x00,
		0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60,
		0x82,
	}
	if err = os.WriteFile(pngPath, pngHeader, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	ref, err := store.Store(pngPath, media.MediaMeta{Filename: "steer.png", ContentType: "image/png"}, "test")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	var capturedMessages []providers.Message
	var capMu sync.Mutex
	provider := &capturingMockProvider{
		response: "ack",
		captureFn: func(msgs []providers.Message) {
			capMu.Lock()
			defer capMu.Unlock()
			capturedMessages = append([]providers.Message(nil), msgs...)
		},
	}

	sessionKey := routing.BuildAgentMainSessionKey("mia")
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	al.SetMediaStore(store)

	if err = al.Steer(providers.Message{
		Role:    "user",
		Content: "describe this image",
		Media:   []string{ref},
	}); err != nil {
		t.Fatalf("Steer failed: %v", err)
	}

	resp, err := al.Continue(context.Background(), sessionKey, "test", "chat1", "")
	if err != nil {
		t.Fatalf("Continue failed: %v", err)
	}
	if resp != "ack" {
		t.Fatalf("expected ack, got %q", resp)
	}

	capMu.Lock()
	msgs := append([]providers.Message(nil), capturedMessages...)
	capMu.Unlock()

	foundResolvedMedia := false
	for _, msg := range msgs {
		if msg.Role != "user" || msg.Content != "describe this image" || len(msg.Media) != 1 {
			continue
		}
		if strings.HasPrefix(msg.Media[0], "data:image/png;base64,") {
			foundResolvedMedia = true
			break
		}
	}
	if !foundResolvedMedia {
		t.Fatal("expected continue path to inject steering media into the provider request")
	}

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	history := defaultAgent.Sessions.GetHistory(sessionKey)
	foundOriginalRef := false
	for _, msg := range history {
		if msg.Role == "user" && len(msg.Media) == 1 && msg.Media[0] == ref {
			foundOriginalRef = true
			break
		}
	}
	if !foundOriginalRef {
		t.Fatal("expected original steering media ref to be preserved in session history")
	}
}

func TestAgentLoop_InterruptGraceful_UsesTerminalNoToolCall(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	tool1ExecCh := make(chan struct{})
	tool1 := &slowTool{name: "tool_one", duration: 50 * time.Millisecond, execCh: tool1ExecCh}
	tool2 := &slowTool{name: "tool_two", duration: 50 * time.Millisecond}

	provider := &gracefulCaptureProvider{
		toolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Name: "tool_one",
				Function: &providers.FunctionCall{
					Name:      "tool_one",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
			{
				ID:   "call_2",
				Type: "function",
				Name: "tool_two",
				Function: &providers.FunctionCall{
					Name:      "tool_two",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
		},
		finalResp: "graceful summary",
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	al.RegisterTool(tool1)
	al.RegisterTool(tool2)
	sessionKey := routing.BuildAgentMainSessionKey("mia")
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): both exercised
	// tools need an explicit agent-level grant, or they fail closed to "deny"
	// before tool_one ever executes and signals tool1ExecCh.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"tool_one": "allow", "tool_two": "allow"},
	})

	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)

	type result struct {
		resp string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := al.ProcessDirectWithChannel(
			context.Background(),
			"do something",
			sessionKey,
			"test",
			"chat1",
		)
		resultCh <- result{resp: resp, err: err}
	}()

	select {
	case <-tool1ExecCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for tool_one to start")
	}

	active := al.GetActiveTurn()
	if active == nil {
		t.Fatal("expected active turn while tool is running")
	}
	if active.SessionKey != sessionKey {
		t.Fatalf("expected active session %q, got %q", sessionKey, active.SessionKey)
	}
	if active.Channel != "test" || active.ChatID != "chat1" {
		t.Fatalf("unexpected active turn target: %#v", active)
	}

	if err := al.InterruptGraceful("wrap it up"); err != nil {
		t.Fatalf("InterruptGraceful failed: %v", err)
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.resp != "graceful summary" {
			t.Fatalf("expected graceful summary, got %q", r.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for graceful interrupt result")
	}

	if active := al.GetActiveTurn(); active != nil {
		t.Fatalf("expected no active turn after completion, got %#v", active)
	}

	provider.mu.Lock()
	terminalMessages := append([]providers.Message(nil), provider.terminalMessages...)
	terminalToolsCount := provider.terminalToolsCount
	calls := provider.calls
	provider.mu.Unlock()

	if calls != 2 {
		t.Fatalf("expected 2 provider calls, got %d", calls)
	}
	if terminalToolsCount != 0 {
		t.Fatalf("expected graceful terminal call to disable tools, got %d tool defs", terminalToolsCount)
	}

	foundHint := false
	foundSkipped := false
	expectedHint := "Interrupt requested. Stop scheduling tools and provide a short final summary.\n\n" +
		"Interrupt hint: wrap it up"
	for _, msg := range terminalMessages {
		if msg.Role == "user" && msg.Content == expectedHint {
			foundHint = true
		}
		if msg.Role == "tool" && msg.ToolCallID == "call_2" && msg.Content == "Skipped due to graceful interrupt." {
			foundSkipped = true
		}
	}
	if !foundHint {
		t.Fatal("expected graceful terminal call to include interrupt hint message")
	}
	if !foundSkipped {
		t.Fatal("expected remaining tool to be marked as skipped after graceful interrupt")
	}

	events := collectEventStream(sub.C)
	interruptEvt, ok := findEvent(events, EventKindInterruptReceived)
	if !ok {
		t.Fatal("expected interrupt received event")
	}
	interruptPayload, ok := interruptEvt.Payload.(InterruptReceivedPayload)
	if !ok {
		t.Fatalf("expected InterruptReceivedPayload, got %T", interruptEvt.Payload)
	}
	if interruptPayload.Kind != InterruptKindGraceful {
		t.Fatalf("expected graceful interrupt payload, got %q", interruptPayload.Kind)
	}

	turnEndEvt, ok := findEvent(events, EventKindTurnEnd)
	if !ok {
		t.Fatal("expected turn end event")
	}
	turnEndPayload, ok := turnEndEvt.Payload.(TurnEndPayload)
	if !ok {
		t.Fatalf("expected TurnEndPayload, got %T", turnEndEvt.Payload)
	}
	if turnEndPayload.Status != TurnEndStatusCompleted {
		t.Fatalf("expected completed turn after graceful interrupt, got %q", turnEndPayload.Status)
	}
}

func TestAgentLoop_InterruptHard_RestoresSession(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &toolCallProvider{
		toolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Name: "cancel_tool",
				Function: &providers.FunctionCall{
					Name:      "cancel_tool",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
		},
		finalResp: "should not happen",
	}

	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	started := make(chan struct{})
	al.RegisterTool(&interruptibleTool{name: "cancel_tool", started: started})
	sessionKey := routing.BuildAgentMainSessionKey("mia")

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): cancel_tool needs
	// an explicit agent-level grant, or it fails closed to "deny" before ever
	// executing and signaling started.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"cancel_tool": "allow"},
	})

	originalHistory := []providers.Message{
		{Role: "user", Content: "before"},
		{Role: "assistant", Content: "after"},
	}
	defaultAgent.Sessions.SetHistory(sessionKey, originalHistory)

	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	type result struct {
		resp string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := al.ProcessDirectWithChannel(
			context.Background(),
			"do work",
			sessionKey,
			"test",
			"chat1",
		)
		resultCh <- result{resp: resp, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for interruptible tool to start")
	}

	if active := al.GetActiveTurn(); active == nil {
		t.Fatal("expected active turn before hard abort")
	}

	if err := al.InterruptHard(); err != nil {
		t.Fatalf("InterruptHard failed: %v", err)
	}

	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.resp != "" {
			t.Fatalf("expected no final response after hard abort, got %q", r.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for hard abort result")
	}

	if active := al.GetActiveTurn(); active != nil {
		t.Fatalf("expected no active turn after hard abort, got %#v", active)
	}

	finalHistory := defaultAgent.Sessions.GetHistory(sessionKey)
	if !reflect.DeepEqual(finalHistory, originalHistory) {
		t.Fatalf("expected history rollback after hard abort, got %#v", finalHistory)
	}

	events := collectEventStream(sub.C)
	interruptEvt, ok := findEvent(events, EventKindInterruptReceived)
	if !ok {
		t.Fatal("expected interrupt received event")
	}
	interruptPayload, ok := interruptEvt.Payload.(InterruptReceivedPayload)
	if !ok {
		t.Fatalf("expected InterruptReceivedPayload, got %T", interruptEvt.Payload)
	}
	if interruptPayload.Kind != InterruptKindHard {
		t.Fatalf("expected hard interrupt payload, got %q", interruptPayload.Kind)
	}

	turnEndEvt, ok := findEvent(events, EventKindTurnEnd)
	if !ok {
		t.Fatal("expected turn end event")
	}
	turnEndPayload, ok := turnEndEvt.Payload.(TurnEndPayload)
	if !ok {
		t.Fatalf("expected TurnEndPayload, got %T", turnEndEvt.Payload)
	}
	if turnEndPayload.Status != TurnEndStatusAborted {
		t.Fatalf("expected aborted turn, got %q", turnEndPayload.Status)
	}
}

// capturingMockProvider captures messages sent to Chat for inspection.
type capturingMockProvider struct {
	response  string
	calls     int
	captureFn func([]providers.Message)
}

func (m *capturingMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	m.calls++
	if m.captureFn != nil {
		m.captureFn(messages)
	}
	return &providers.LLMResponse{
		Content:   m.response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *capturingMockProvider) GetDefaultModel() string {
	return "capturing-mock"
}

func TestAgentLoop_Steering_SkippedToolsHaveErrorResults(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test drives
			// a real turn through ProcessDirectWithChannel/Continue, which
			// needs a REAL registered agent to route to.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	execCh := make(chan struct{})
	tool1 := &slowTool{name: "slow_tool", duration: 50 * time.Millisecond, execCh: execCh}
	tool2 := &slowTool{name: "skipped_tool", duration: 50 * time.Millisecond}

	// Provider that captures messages on the second call (after tools)
	var secondCallMessages []providers.Message
	var capMu sync.Mutex
	callCount := 0

	provider := &toolCallProvider{
		toolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Name: "slow_tool",
				Function: &providers.FunctionCall{
					Name:      "slow_tool",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
			{
				ID:   "call_2",
				Type: "function",
				Name: "skipped_tool",
				Function: &providers.FunctionCall{
					Name:      "skipped_tool",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
		},
		finalResp: "done",
	}

	// Wrap provider to capture messages on second call
	wrappedProvider := &wrappingProvider{
		inner: provider,
		onChat: func(msgs []providers.Message) {
			capMu.Lock()
			callCount++
			if callCount >= 2 {
				secondCallMessages = make([]providers.Message, len(msgs))
				copy(secondCallMessages, msgs)
			}
			capMu.Unlock()
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, wrappedProvider)
	al.RegisterTool(tool1)
	al.RegisterTool(tool2)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): both exercised
	// tools need an explicit agent-level grant, or they fail closed to "deny"
	// before ever executing and slow_tool never signals execCh.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"slow_tool": "allow", "skipped_tool": "allow"},
	})

	resultCh := make(chan string, 1)
	go func() {
		resp, _ := al.ProcessDirectWithChannel(
			context.Background(), "go", "test-session", "test", "chat1",
		)
		resultCh <- resp
	}()

	// Bounded wait (regression guard): before the explicit policy grant above,
	// a denied slow_tool never signaled execCh and this blocked forever,
	// masking a policy-fixture bug as a 10-minute test-binary hang instead of
	// a fast, readable failure.
	select {
	case <-execCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for slow_tool to start")
	}
	al.Steer(providers.Message{Role: "user", Content: "interrupt!"})

	select {
	case <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	// Check that the skipped tool result message is in the conversation
	capMu.Lock()
	msgs := secondCallMessages
	capMu.Unlock()

	foundSkipped := false
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == "call_2" && m.Content == "Skipped due to queued user message." {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		// Log what we actually got
		for i, m := range msgs {
			t.Logf("msg[%d]: role=%s toolCallID=%s content=%s", i, m.Role, m.ToolCallID, truncate(m.Content, 80))
		}
		t.Fatal("expected skipped tool result for call_2")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// wrappingProvider wraps another provider to hook into Chat calls.
type wrappingProvider struct {
	inner  providers.LLMProvider
	onChat func([]providers.Message)
}

func (w *wrappingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	if w.onChat != nil {
		w.onChat(messages)
	}
	return w.inner.Chat(ctx, messages, tools, model, opts)
}

func (w *wrappingProvider) GetDefaultModel() string {
	return w.inner.GetDefaultModel()
}

// Ensure NormalizeToolCall handles our test tool calls.
func init() {
	// This is a no-op init; we just need the tool call tests to work
	// with the proper argument serialization.
	_ = json.Marshal
}

// --------------------------------------------------------------------------
// Cancel-cascade tests (TDD Plan T1, T2, T4)
// Refs: docs/internal/specs/cancel-cross-channel-spec.md FR-6, FR-10, FR-11, FR-12a
// --------------------------------------------------------------------------

// stubTurnState builds a minimal turnState wired with a stub providerCancel.
// interruptedCh is closed when requestGracefulInterrupt fires.
// hardAbortedCh is closed when requestHardAbort fires.
// providerCancelledCh is closed when providerCancel is called.
//
// We store the channels in the turnState's providerCancel func so the cascade
// code exercises the real mutex-protected path rather than a side channel.
func stubTurnStateForCancel(t *testing.T, al *AgentLoop, sessionKey, transcriptSID string) (
	ts *turnState,
	providerCancelledCh chan struct{},
) {
	t.Helper()
	providerCancelledCh = make(chan struct{}, 1)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	opts := processOptions{
		SessionKey:          sessionKey,
		Channel:             "test",
		ChatID:              "chat1",
		TranscriptSessionID: transcriptSID,
	}
	scope := al.newTurnEventScope(agent.ID, sessionKey)
	ts = newTurnState(agent, opts, scope)

	// Wire a stub providerCancel that signals the channel.
	doneCh := providerCancelledCh
	ts.mu.Lock()
	ts.providerCancel = func() {
		select {
		case doneCh <- struct{}{}:
		default:
		}
	}
	ts.mu.Unlock()

	al.activeTurnStates.Store(sessionKey, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(sessionKey) })
	return ts, providerCancelledCh
}

// TestInterruptSession_CascadeToSubTurns is T1.
//
// BDD: Given a parent turn and 2 sub-turns all sharing transcriptSessionID "S"
// registered in activeTurnStates,
// When Interrupt("S", ScopeSubtree, "hint") is called,
// Then all 3 turnStates receive requestGracefulInterrupt (gracefulInterrupt=true)
// AND all 3 providerCancel stubs fire within 200ms.
//
// Refs: FR-6, FR-10, FR-12a, TDD Plan T1.
func TestInterruptSession_CascadeToSubTurns(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const sid = "test-session-cascade"

	_, pc1 := stubTurnStateForCancel(t, al, "parent", sid)
	ts2, pc2 := stubTurnStateForCancel(t, al, "sub1", sid)
	ts3, pc3 := stubTurnStateForCancel(t, al, "sub2", sid)
	_ = ts2
	_ = ts3

	if _, err := al.Interrupt(sid, ScopeSubtree, "wrap up"); err != nil {
		t.Fatalf("Interrupt returned unexpected error: %v", err)
	}

	// All three providerCancel stubs must have fired (FR-12a).
	timeout := time.After(200 * time.Millisecond)
	for i, ch := range []chan struct{}{pc1, pc2, pc3} {
		select {
		case <-ch:
		case <-timeout:
			t.Fatalf("providerCancel[%d] was not called within 200ms (FR-12a)", i)
		}
	}

	// All three turnStates must have gracefulInterrupt set (FR-6, FR-10).
	for i, key := range []string{"parent", "sub1", "sub2"} {
		raw, ok := al.activeTurnStates.Load(key)
		if !ok {
			t.Fatalf("turn %d (%s) not found in activeTurnStates", i, key)
		}
		ts := raw.(*turnState)
		interrupted, _ := ts.gracefulInterruptRequested()
		if !interrupted {
			t.Errorf("turn %d (%s): gracefulInterrupt not set after cascade", i, key)
		}
	}
}

// TestInterruptSession_NoActiveTurnIsAttemptOnly is T2.
//
// BDD: Given activeTurnStates is empty (no active turns),
// When InterruptSession is called with a valid sessionID,
// Then it returns nil (not an error) and does not panic.
//
// The cancel handler (websocket.go, next wave) is responsible for emitting
// turn_cancel_attempt{was_fired:false} in this case. This test only verifies
// the backend function signature contract.
//
// Refs: FR-6, TDD Plan T2.
func TestInterruptSession_NoActiveTurnIsAttemptOnly(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	// activeTurnStates is empty by construction.
	_, err := al.Interrupt("nonexistent-session", ScopeSubtree, "hint")
	if err != nil {
		t.Fatalf("Interrupt on empty activeTurnStates returned error: %v (want nil)", err)
	}
}

// TestRequestCancelByChannelChat_CascadesToSubTurns verifies that a Tier B cancel
// originating from a channel+chatID match propagates to sub-turns that share
// the same transcriptSessionID even though sub-turns have empty channel/chatID.
//
// BDD: Given a root turn (depth=0, channel="telegram", chatID="123", transcriptSessionID="S")
// and a sub-turn (depth=1, channel="", chatID="", transcriptSessionID="S"),
// When RequestCancelByChannelChat(ctx, "telegram", "123", "") is called,
// Then BOTH turns receive requestGracefulInterrupt (gracefulInterrupt=true).
//
// Refs: FR-6, FR-10.
func TestRequestCancelByChannelChat_CascadesToSubTurns(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const sid = "channel-chat-cascade-session"

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	// Helper to build a turnState stub with explicit channel/chatID/depth.
	makeTurn := func(sessionKey, channel, chatID string, depth int, transcriptSID string) (*turnState, chan struct{}) {
		t.Helper()
		provCh := make(chan struct{}, 1)
		opts := processOptions{
			SessionKey:          sessionKey,
			Channel:             channel,
			ChatID:              chatID,
			TranscriptSessionID: transcriptSID,
		}
		scope := al.newTurnEventScope(agent.ID, sessionKey)
		ts := newTurnState(agent, opts, scope)
		ts.depth = depth
		ts.mu.Lock()
		ts.providerCancel = func() {
			select {
			case provCh <- struct{}{}:
			default:
			}
		}
		ts.mu.Unlock()
		al.activeTurnStates.Store(sessionKey, ts)
		t.Cleanup(func() { al.activeTurnStates.Delete(sessionKey) })
		return ts, provCh
	}

	// Root turn: has channel/chatID populated.
	parentTS, parentPC := makeTurn("parent-key", "telegram", "123", 0, sid)
	// Sub-turn: inherits transcriptSessionID but has no channel/chatID (depth=1).
	subTS, subPC := makeTurn("sub-key", "", "", 1, sid)

	if _, _, err := al.RequestCancelByChannelChat(context.Background(), "telegram", "123", ""); err != nil {
		t.Fatalf("RequestCancelByChannelChat returned unexpected error: %v", err)
	}

	// Both providerCancel stubs must fire within 200ms (FR-12a).
	timeout := time.After(200 * time.Millisecond)
	for i, ch := range []chan struct{}{parentPC, subPC} {
		select {
		case <-ch:
		case <-timeout:
			t.Fatalf("providerCancel[%d] was not called within 200ms", i)
		}
	}

	// Both turns must have gracefulInterrupt set.
	for i, ts := range []*turnState{parentTS, subTS} {
		interrupted, _ := ts.gracefulInterruptRequested()
		if !interrupted {
			t.Errorf("turn %d: gracefulInterrupt not set after Tier B cascade", i)
		}
	}
}

// TestRequestCancelByChannelChat_NoMatchIsNoop verifies that calling
// RequestCancelByChannelChat when no root turn matches is a silent no-op.
func TestRequestCancelByChannelChat_NoMatchIsNoop(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	// No turns registered — must return nil, not error.
	if _, _, err := al.RequestCancelByChannelChat(context.Background(), "telegram", "999", ""); err != nil {
		t.Fatalf("expected nil for no-match, got: %v", err)
	}
}

// TestRequestCancelByChannelChat_EmptyArgsError verifies that empty channel or
// chatID returns a non-nil error.
func TestRequestCancelByChannelChat_EmptyArgsError(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	if _, _, err := al.RequestCancelByChannelChat(context.Background(), "", "123", ""); err == nil {
		t.Fatal("expected error for empty channel")
	}
	if _, _, err := al.RequestCancelByChannelChat(context.Background(), "telegram", "", ""); err == nil {
		t.Fatal("expected error for empty chatID")
	}
}

// TestInterruptSessionHard_CascadesAcrossSession is T4.
//
// BDD: Given a parent turn and 2 sub-turns all sharing transcriptSessionID "S"
// registered in activeTurnStates,
// When InterruptSessionHard("S", ScopeSubtree, "hard abort") is called,
// Then all 3 turnStates receive requestHardAbort (hardAbort=true)
// AND all 3 providerCancel stubs fire within 200ms.
//
// Refs: FR-11, TDD Plan T4.
func TestInterruptSessionHard_CascadesAcrossSession(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const sid = "test-session-hard"

	_, pc1 := stubTurnStateForCancel(t, al, "hard-parent", sid)
	_, pc2 := stubTurnStateForCancel(t, al, "hard-sub1", sid)
	_, pc3 := stubTurnStateForCancel(t, al, "hard-sub2", sid)

	if _, err := al.InterruptSessionHard(sid, ScopeSubtree, "hard abort"); err != nil {
		t.Fatalf("InterruptSessionHard returned unexpected error: %v", err)
	}

	// All three providerCancel stubs must have fired (FR-11 + requestHardAbort path).
	timeout := time.After(200 * time.Millisecond)
	for i, ch := range []chan struct{}{pc1, pc2, pc3} {
		select {
		case <-ch:
		case <-timeout:
			t.Fatalf("providerCancel[%d] was not called within 200ms for hard cascade", i)
		}
	}

	// All three turnStates must have hardAbort set.
	for i, key := range []string{"hard-parent", "hard-sub1", "hard-sub2"} {
		raw, ok := al.activeTurnStates.Load(key)
		if !ok {
			t.Fatalf("turn %d (%s) not found in activeTurnStates", i, key)
		}
		ts := raw.(*turnState)
		ts.mu.RLock()
		ha := ts.hardAbort
		ts.mu.RUnlock()
		if !ha {
			t.Errorf("turn %d (%s): hardAbort not set after InterruptSessionHard", i, key)
		}
	}
}
