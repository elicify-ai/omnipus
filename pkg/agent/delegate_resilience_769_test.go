// delegate_resilience_769_test.go reproduces GitHub issue #769.
//
// A provider 429 must get the same bounded retry treatment for a delegated
// child that transient transport failures already receive. Separately, a
// child stuck in code that ignores cancellation must stop blocking its caller
// once the delegation's time limit and detach grace have elapsed.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func issue769ParentTurn(t *testing.T, al *AgentLoop, parent *AgentInstance, suffix string) *turnState {
	t.Helper()
	parentSessionID, store := stiMintParentSession(t, al)
	return &turnState{
		ctx:                 context.Background(),
		turnID:              "issue-769-parent-" + suffix,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     store,
	}
}

func issue769RateLimitError() error {
	return &providers.FailoverError{
		Reason:   providers.FailoverRateLimit,
		Provider: "scripted",
		Model:    "test-model",
		Status:   429,
		Wrapped:  errors.New("rate limit reached"),
	}
}

func issue769AwaitSubTurnEnd(t *testing.T, events <-chan Event) SubTurnEndPayload {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if payload, ok := event.Payload.(SubTurnEndPayload); ok {
				return payload
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for the delegated child's terminal event")
		}
	}
}

// TestIssue769_DelegatedRateLimitRetriesWithBackoffAndBound is the RED for
// candidate defect 1. The success row proves a 429 does not permanently kill
// the delegated child; the exhaustion row proves recovery is bounded at three
// total provider attempts. The injected per-loop sleep records the exact
// requested backoffs, so removing the sleep cannot pass under scheduler load.
func TestIssue769_DelegatedRateLimitRetriesWithBackoffAndBound(t *testing.T) {
	t.Run("recovers after one rate limit", func(t *testing.T) {
		al, home := schedTestLoop(t)
		var backoffs []time.Duration
		al.delegatedRateLimitSleep = func(ctx context.Context, delay time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			backoffs = append(backoffs, delay)
			return nil
		}
		parent := registerAgent(t, al, home, "issue-769-lead-recover", testutil.NewScenario().WithText("idle"), true)
		childProvider := testutil.NewScenario().
			WithError(issue769RateLimitError()).
			WithText("recovered delegated result")
		registerAgent(t, al, home, "issue-769-worker-recover", childProvider, false)
		parentTS := issue769ParentTurn(t, al, parent, "recover")

		result, err := spawnSubTurn(
			withSpawnToolCallID(context.Background(), "issue-769-call-recover"),
			al,
			parentTS,
			SubTurnConfig{
				Model:             "test-model",
				SystemPrompt:      "finish after a transient provider throttle",
				TargetAgentID:     "issue-769-worker-recover",
				DelegateSessionID: "issue-769-child-recover",
				Timeout:           5 * time.Second,
			},
		)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Contains(t, result.ForLLM, "recovered delegated result")
		require.Equal(t, 2, childProvider.CallCount(), "one 429 must cause exactly one retry before success")
		require.Len(t, backoffs, 1, "one retry must request exactly one backoff")
		assert.GreaterOrEqual(t, backoffs[0], 500*time.Millisecond)
		assert.LessOrEqual(t, backoffs[0], time.Second)
	})

	t.Run("stops after the bounded attempt budget", func(t *testing.T) {
		al, home := schedTestLoop(t)
		events := al.SubscribeEvents(16)
		defer al.UnsubscribeEvents(events.ID)
		var backoffs []time.Duration
		al.delegatedRateLimitSleep = func(ctx context.Context, delay time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			backoffs = append(backoffs, delay)
			return nil
		}
		parent := registerAgent(t, al, home, "issue-769-lead-bound", testutil.NewScenario().WithText("idle"), true)
		childProvider := testutil.NewScenario().
			WithError(issue769RateLimitError()).
			WithError(issue769RateLimitError()).
			WithError(issue769RateLimitError()).
			WithText("a fourth call would be a retry storm")
		registerAgent(t, al, home, "issue-769-worker-bound", childProvider, false)
		parentTS := issue769ParentTurn(t, al, parent, "bound")

		result, err := spawnSubTurn(
			withSpawnToolCallID(context.Background(), "issue-769-call-bound"),
			al,
			parentTS,
			SubTurnConfig{
				Model:             "test-model",
				SystemPrompt:      "fail after the bounded provider retry budget",
				TargetAgentID:     "issue-769-worker-bound",
				DelegateSessionID: "issue-769-child-bound",
				Timeout:           5 * time.Second,
			},
		)

		require.Error(t, err)
		require.NotNil(t, result)
		assert.True(t, result.IsError)
		require.Equal(t, 3, childProvider.CallCount(), "the initial call plus two retries is the hard bound")
		require.Len(t, backoffs, 2, "two retries must request exactly two backoffs")
		assert.GreaterOrEqual(t, backoffs[0], 500*time.Millisecond)
		assert.LessOrEqual(t, backoffs[0], time.Second)
		assert.GreaterOrEqual(t, backoffs[1], time.Second)
		assert.LessOrEqual(t, backoffs[1], 2*time.Second)
		assert.NotContains(t, result.ForLLM, "a fourth call would be a retry storm")
		assert.False(t, result.Interrupted,
			"exhausting provider retries is a failure, not a user interruption")
		end := issue769AwaitSubTurnEnd(t, events.C)
		assert.Equal(t, SubTurnStatusError, end.Status,
			"the terminal span must report the exhausted 429 as an error")
	})
}

// TestIssue769_DelegateToolExhaustedRateLimitPersistsFailure drives the real
// synchronous delegate tool so the span classification cannot be correct
// while durable lifecycle state is silently recorded as user cancellation.
func TestIssue769_DelegateToolExhaustedRateLimitPersistsFailure(t *testing.T) {
	al, home := schedTestLoop(t)
	al.delegatedRateLimitSleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	parent := registerAgent(t, al, home, "issue-769-lead-tool", testutil.NewScenario().WithText("idle"), true)
	childProvider := testutil.NewScenario().
		WithError(issue769RateLimitError()).
		WithError(issue769RateLimitError()).
		WithError(issue769RateLimitError())
	registerAgent(t, al, home, "issue-769-worker-tool", childProvider, false)
	parentTS := issue769ParentTurn(t, al, parent, "tool")

	delegateTool := tools.NewDelegateTool("test-model", 4096, 0)
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	lifecycle := session.NewLifecycleStore(t.TempDir())
	delegateTool.SetLifecycleStore(lifecycle)
	delegateTool.SetDelegationDenyCheckerAwait(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)

	ctx := withSpawnToolCallID(withTurnState(context.Background(), parentTS), "issue-769-real-delegate-tool")
	ctx = tools.WithAgentID(ctx, parent.ID)
	ctx = tools.WithTranscriptSessionID(ctx, parentTS.transcriptSessionID)
	ctx = tools.WithToolCallID(ctx, "issue-769-real-delegate-tool")
	result := delegateTool.Execute(ctx, map[string]any{
		"task":     "exhaust the bounded provider retry budget",
		"agent_id": "issue-769-worker-tool",
		"async":    false,
	})

	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.NotContains(t, result.ForLLM, "stopped by user")
	require.Equal(t, 3, childProvider.CallCount())
	records, err := lifecycle.List(session.LifecycleFilter{})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, session.LifecycleFailed, records[0].State,
		"exhausted provider retries must persist as failed, never cancelled")
}

type issue769StreamingProvider struct {
	mu                 sync.Mutex
	calls              int
	partialOnRateLimit bool
}

func (p *issue769StreamingProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	return nil, errors.New("issue #769 test setup: non-streaming path used")
}

func (p *issue769StreamingProvider) GetDefaultModel() string { return "test-model" }

func (p *issue769StreamingProvider) ChatStream(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
	onChunk func(string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()

	switch call {
	case 1:
		onChunk("working before the tool")
		fc := providers.FunctionCall{Name: "issue_769_continue", Arguments: `{}`}
		return &providers.LLMResponse{
			Content: "working before the tool",
			ToolCalls: []providers.ToolCall{{
				ID:       "issue-769-continue-0",
				Function: &fc,
			}},
		}, nil
	case 2:
		if p.partialOnRateLimit {
			onChunk("x")
		}
		return nil, issue769RateLimitError()
	default:
		onChunk("finished after the retry")
		return &providers.LLMResponse{Content: "finished after the retry"}, nil
	}
}

func (p *issue769StreamingProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type issue769CountingStreamer struct {
	mu      sync.Mutex
	content string
}

func (s *issue769CountingStreamer) Update(_ context.Context, delta string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content += delta
	return nil
}

func (s *issue769CountingStreamer) Finalize(context.Context, string) error { return nil }
func (s *issue769CountingStreamer) Cancel(context.Context)                 {}
func (s *issue769CountingStreamer) StreamedContentLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.content)
}

type issue769StreamDelegate struct{ streamer bus.Streamer }

func (d *issue769StreamDelegate) GetStreamer(context.Context, string, string, string) (bus.Streamer, bool) {
	return d.streamer, true
}

type issue769FreshStreamDelegate struct{}

func (*issue769FreshStreamDelegate) GetStreamer(context.Context, string, string, string) (bus.Streamer, bool) {
	return &issue769CountingStreamer{}, true
}

type issue769BlindStreamDelegate struct{}

func (*issue769BlindStreamDelegate) GetStreamer(context.Context, string, string, string) (bus.Streamer, bool) {
	return &mockStreamer{}, true
}

type issue769ContinueTool struct{ tools.BaseTool }

func (t *issue769ContinueTool) Name() string        { return "issue_769_continue" }
func (t *issue769ContinueTool) Description() string { return "continue the issue #769 test turn" }
func (t *issue769ContinueTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *issue769ContinueTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (t *issue769ContinueTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.NewToolResult("continue")
}

// TestIssue769_DelegatedRateLimitRetryIgnoresPriorRoundStream proves the retry
// guard is scoped to the failed provider attempt. Narration streamed before a
// tool call must not make a later pre-token 429 look like a partial response.
func TestIssue769_DelegatedRateLimitRetryIgnoresPriorRoundStream(t *testing.T) {
	al, home := schedTestLoop(t)
	streamer := &issue769CountingStreamer{}
	al.bus.SetStreamDelegate(&issue769StreamDelegate{streamer: streamer})
	parent := registerAgent(t, al, home, "issue-769-lead-stream", testutil.NewScenario().WithText("idle"), true)
	childProvider := &issue769StreamingProvider{}
	child := registerAgent(t, al, home, "issue-769-worker-stream", childProvider, false)
	child.Tools.Register(&issue769ContinueTool{})
	child.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"issue_769_continue": config.ToolPolicyAllow,
	}})
	parentTS := issue769ParentTurn(t, al, parent, "stream")

	result, err := spawnSubTurn(
		withSpawnToolCallID(context.Background(), "issue-769-call-stream"),
		al,
		parentTS,
		SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "stream, call the tool, then survive a pre-token throttle",
			TargetAgentID:     "issue-769-worker-stream",
			DelegateSessionID: "issue-769-child-stream",
			Timeout:           5 * time.Second,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "finished after the retry")
	assert.Equal(t, 3, childProvider.CallCount(),
		"prior-round streamed narration must not suppress the later pre-token 429 retry")
}

// TestIssue769_DelegatedRateLimitDoesNotRetryPartialCurrentStream covers the
// production WebSocket shape where GetStreamer returns a fresh streamer for
// each round. A failed attempt that emitted even one byte must not be retried,
// regardless of how much an earlier round streamed through another instance.
func TestIssue769_DelegatedRateLimitDoesNotRetryPartialCurrentStream(t *testing.T) {
	al, home := schedTestLoop(t)
	al.bus.SetStreamDelegate(&issue769FreshStreamDelegate{})
	parent := registerAgent(t, al, home, "issue-769-lead-partial", testutil.NewScenario().WithText("idle"), true)
	childProvider := &issue769StreamingProvider{partialOnRateLimit: true}
	child := registerAgent(t, al, home, "issue-769-worker-partial", childProvider, false)
	child.Tools.Register(&issue769ContinueTool{})
	child.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"issue_769_continue": config.ToolPolicyAllow,
	}})
	parentTS := issue769ParentTurn(t, al, parent, "partial")

	result, err := spawnSubTurn(
		withSpawnToolCallID(context.Background(), "issue-769-call-partial"),
		al,
		parentTS,
		SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "stream, call the tool, then partially stream before a throttle",
			TargetAgentID:     "issue-769-worker-partial",
			DelegateSessionID: "issue-769-child-partial",
			Timeout:           5 * time.Second,
		},
	)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, childProvider.CallCount(),
		"a 429 after current-attempt streamed output must not retry and duplicate visible text")
}

// TestIssue769_DelegatedRateLimitDoesNotRetryUnmeasurablePartialStream proves
// duplicate prevention does not depend on the streamer's optional
// StreamedContentLen method. Telegram/WeCom-style streamers implement only the
// base bus.Streamer contract, so the provider-call layer must track its own
// emitted bytes.
func TestIssue769_DelegatedRateLimitDoesNotRetryUnmeasurablePartialStream(t *testing.T) {
	al, home := schedTestLoop(t)
	al.bus.SetStreamDelegate(&issue769BlindStreamDelegate{})
	parent := registerAgent(t, al, home, "issue-769-lead-blind", testutil.NewScenario().WithText("idle"), true)
	childProvider := &issue769StreamingProvider{partialOnRateLimit: true}
	child := registerAgent(t, al, home, "issue-769-worker-blind", childProvider, false)
	child.Tools.Register(&issue769ContinueTool{})
	child.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"issue_769_continue": config.ToolPolicyAllow,
	}})
	parentTS := issue769ParentTurn(t, al, parent, "blind")

	result, err := spawnSubTurn(
		withSpawnToolCallID(context.Background(), "issue-769-call-blind"),
		al,
		parentTS,
		SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "partially stream through a base-contract streamer before a throttle",
			TargetAgentID:     "issue-769-worker-blind",
			DelegateSessionID: "issue-769-child-blind",
			Timeout:           5 * time.Second,
		},
	)

	require.Error(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 2, childProvider.CallCount(),
		"partial output must suppress retry even when the streamer cannot report its buffer length")
}

type issue769IgnoreCancellationTool struct {
	tools.BaseTool
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
	once     sync.Once
}

func (t *issue769IgnoreCancellationTool) Name() string { return "ignore_cancellation" }
func (t *issue769IgnoreCancellationTool) Description() string {
	return "issue #769 test tool that deliberately ignores context cancellation"
}
func (t *issue769IgnoreCancellationTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *issue769IgnoreCancellationTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (t *issue769IgnoreCancellationTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	t.once.Do(func() { close(t.started) })
	<-t.release
	close(t.finished)
	return tools.NewToolResult("released after caller already detached")
}

// TestIssue769_DelegatedChildIgnoringCancellationSurfacesTimeout is the RED
// for candidate defect 2. The child's tool deliberately ignores ctx.Done(),
// so requesting a hard abort is insufficient: the delegate caller must detach
// after the established cancellation grace and receive a user-visible timeout.
func TestIssue769_DelegatedChildIgnoringCancellationSurfacesTimeout(t *testing.T) {
	oldDetachDelay := cancelDetachDelay
	cancelDetachDelay = 50 * time.Millisecond
	t.Cleanup(func() { cancelDetachDelay = oldDetachDelay })

	al, home := schedTestLoop(t)
	parent := registerAgent(t, al, home, "issue-769-lead-stuck", testutil.NewScenario().WithText("idle"), true)
	childProvider := testutil.NewScenario().
		WithToolCall("ignore_cancellation", `{}`).
		WithText("must not make another model call after timing out")
	child := registerAgent(t, al, home, "issue-769-worker-stuck", childProvider, false)
	stuck := &issue769IgnoreCancellationTool{
		started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
	}
	released := false
	releaseStuckTool := func() {
		if !released {
			close(stuck.release)
			released = true
		}
	}
	t.Cleanup(releaseStuckTool)
	child.Tools.Register(stuck)
	child.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"ignore_cancellation": config.ToolPolicyAllow,
	}})
	parentTS := issue769ParentTurn(t, al, parent, "stuck")
	parentTS.concurrencySem = make(chan struct{}, 1)
	rootGate := NewRootDelegationAdmission(1)
	rootSpawner := newRootDelegationAdmittingSpawner(NewSubTurnSpawner(al), rootGate, parent.ID)
	blockedProvider := testutil.NewScenario().WithText("must not start while the detached child is still running")
	registerAgent(t, al, home, "issue-769-worker-blocked", blockedProvider, false)

	type outcome struct {
		result *tools.ToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		rootCtx := withTurnState(context.Background(), parentTS)
		result, err := rootSpawner.SpawnSubTurn(
			withSpawnToolCallID(rootCtx, "issue-769-call-stuck"),
			tools.SubTurnConfig{
				Model:             "test-model",
				SystemPrompt:      "enter the cancellation-ignoring tool",
				TargetAgentID:     "issue-769-worker-stuck",
				DelegateSessionID: "issue-769-child-stuck",
				Timeout:           time.Second,
			},
		)
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-stuck.started:
	case <-time.After(5 * time.Second):
		t.Fatal("test setup failed: delegated child never entered the cancellation-ignoring tool")
	}
	rawChildState, ok := al.activeTurnStates.Load("issue-769-child-stuck")
	require.True(t, ok, "test setup: running delegated child must be registered")
	childTS, ok := rawChildState.(*turnState)
	require.True(t, ok, "test setup: active delegated child has unexpected state type")

	var got outcome
	select {
	case got = <-done:
	case <-time.After(2500 * time.Millisecond):
		t.Error("delegated child stayed wedged after its time limit and detach grace; the parent still has no terminal error")
		releaseStuckTool()
		got = <-done
		return
	}

	require.Error(t, got.err)
	require.ErrorIs(t, got.err, tools.ErrDelegationTimedOut)
	require.NotNil(t, got.result)
	assert.True(t, got.result.IsError)
	assert.True(t, strings.Contains(got.result.ForLLM, "time limit") || strings.Contains(got.result.ForLLM, "timed out"),
		"timeout result must explain the terminal condition to the parent/user: %q", got.result.ForLLM)

	entries, transcriptErr := parentTS.transcriptStore.ReadTranscript("issue-769-child-stuck")
	require.NoError(t, transcriptErr, "the detached child's terminal timeout must survive session reload")
	var persistedTimeouts int
	for _, entry := range entries {
		if entry.ErrorCode == string(CodeTurnTimedOut) {
			persistedTimeouts++
			assert.Contains(t, entry.Content, "already-running operation may still be unwinding",
				"replay must preserve the truthful detached-timeout warning")
			assert.NotContains(t, entry.Content, "was stopped",
				"replay must not claim a cancellation-ignoring operation has already stopped")
		}
	}
	assert.Equal(t, 1, persistedTimeouts,
		"the detached child's transcript must contain exactly one typed terminal timeout")
	rawStillActive, stillActive := al.activeTurnStates.Load("issue-769-child-stuck")
	require.True(t, stillActive,
		"a detached child must remain discoverable until its physical goroutine exits")
	assert.Same(t, childTS, rawStillActive,
		"the active-turn entry must continue to identify the physically-running generation")
	require.Equal(t, 1, rootGate.Active(),
		"the physically-running detached child must retain its process-wide root admission lease")

	resumeParent := issue769ParentTurn(t, al, parent, "resume-while-stuck")
	_, resumeErr := spawnSubTurn(
		withSpawnToolCallID(context.Background(), "issue-769-call-resume-while-stuck"),
		al,
		resumeParent,
		SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "must not overlap the physically-running prior generation",
			TargetAgentID:     "issue-769-worker-stuck",
			DelegateSessionID: "issue-769-child-stuck",
			IsResume:          true,
			Timeout:           5 * time.Second,
		},
	)
	require.Error(t, resumeErr,
		"a warm resume must be refused while the prior generation is still physically running")
	assert.Contains(t, resumeErr.Error(), "still physically running")

	blockedCtx, cancelBlocked := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, blockedErr := spawnSubTurn(
		withSpawnToolCallID(blockedCtx, "issue-769-call-blocked"),
		al,
		parentTS,
		SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "do not start until the physically-running detached child exits",
			TargetAgentID:     "issue-769-worker-blocked",
			DelegateSessionID: "issue-769-child-blocked",
			Timeout:           5 * time.Second,
		},
	)
	cancelBlocked()
	require.ErrorIs(t, blockedErr, context.DeadlineExceeded,
		"a detached but still-running child must continue occupying its concurrency slot")
	assert.Equal(t, 0, blockedProvider.CallCount(),
		"a second child must not start while the cancellation-ignoring tool still holds the only slot")

	secondRootCtx, cancelSecondRoot := context.WithTimeout(
		withTurnState(context.Background(), parentTS), 150*time.Millisecond,
	)
	refused, refusedErr := rootSpawner.SpawnSubTurn(
		withSpawnToolCallID(secondRootCtx, "issue-769-call-root-blocked"),
		tools.SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "must be refused by the process-wide root cap",
			TargetAgentID:     "issue-769-worker-blocked",
			DelegateSessionID: "issue-769-child-root-blocked",
			Timeout:           5 * time.Second,
		},
	)
	cancelSecondRoot()
	require.NoError(t, refusedErr, "root-cap refusal is returned as a tool result")
	require.NotNil(t, refused)
	assert.True(t, refused.IsError)
	assert.Contains(t, refused.ForLLM, "concurrent root-delegation cap")
	assert.Equal(t, 0, blockedProvider.CallCount(),
		"another root turn must not bypass the process-wide cap while the detached child still runs")

	releaseStuckTool()
	select {
	case <-stuck.finished:
	case <-time.After(time.Second):
		t.Fatal("cancellation-ignoring tool did not finish after the test released it")
	}
	select {
	case <-childTS.Finished():
	case <-time.After(time.Second):
		t.Fatal("detached child turn did not finish after its stuck tool returned")
	}
	assert.Equal(t, 1, childProvider.CallCount(),
		"a detached timed-out child must not make another model call after its stuck tool returns")
	require.Eventually(t, func() bool { return rootGate.Active() == 0 }, time.Second, 10*time.Millisecond,
		"the process-wide root admission lease must release when the physical child exits")
	require.Eventually(t, func() bool {
		_, exists := al.activeTurnStates.Load("issue-769-child-stuck")
		return !exists
	}, time.Second, 10*time.Millisecond,
		"the active-turn entry must clear after the physical child exits")
}

// TestIssue769_PriorHardAbortStillDetachesIgnoringChild covers the race where
// a user/parent cancellation claims hard-abort before the delegation's own
// timer. Reaching the later time limit must still bound the wait, but must not
// relabel the earlier cancellation as a timeout.
func TestIssue769_PriorHardAbortStillDetachesIgnoringChild(t *testing.T) {
	oldDetachDelay := cancelDetachDelay
	cancelDetachDelay = 40 * time.Millisecond
	t.Cleanup(func() { cancelDetachDelay = oldDetachDelay })

	al, home := schedTestLoop(t)
	events := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(events.ID)
	parent := registerAgent(t, al, home, "issue-769-lead-prior-abort", testutil.NewScenario().WithText("idle"), true)
	childProvider := testutil.NewScenario().
		WithToolCall("ignore_cancellation", `{}`).
		WithText("must not continue after the prior hard abort")
	child := registerAgent(t, al, home, "issue-769-worker-prior-abort", childProvider, false)
	stuck := &issue769IgnoreCancellationTool{
		started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
	}
	released := false
	releaseStuckTool := func() {
		if !released {
			close(stuck.release)
			released = true
		}
	}
	t.Cleanup(releaseStuckTool)
	child.Tools.Register(stuck)
	child.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"ignore_cancellation": config.ToolPolicyAllow,
	}})
	parentTS := issue769ParentTurn(t, al, parent, "prior-abort")

	type outcome struct {
		result *tools.ToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := spawnSubTurn(
			withSpawnToolCallID(context.Background(), "issue-769-call-prior-abort"),
			al,
			parentTS,
			SubTurnConfig{
				Model:             "test-model",
				SystemPrompt:      "enter the cancellation-ignoring tool",
				TargetAgentID:     "issue-769-worker-prior-abort",
				DelegateSessionID: "issue-769-child-prior-abort",
				Timeout:           3 * time.Second,
			},
		)
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-stuck.started:
	case <-time.After(5 * time.Second):
		t.Fatal("test setup failed: delegated child never entered the cancellation-ignoring tool")
	}
	rawChildState, ok := al.activeTurnStates.Load("issue-769-child-prior-abort")
	require.True(t, ok)
	childTS, ok := rawChildState.(*turnState)
	require.True(t, ok)
	require.True(t, childTS.requestHardAbort(),
		"the external cancellation must win before the delegation timer")

	var got outcome
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Error("a prior hard abort left the delegation waiting without a detach bound")
		releaseStuckTool()
		got = <-done
		return
	}

	require.Error(t, got.err)
	assert.NotErrorIs(t, got.err, tools.ErrDelegationTimedOut,
		"the delegation timer must not steal classification from the earlier cancellation")
	require.NotNil(t, got.result)
	assert.True(t, got.result.Interrupted,
		"the returned result must preserve cancellation semantics after detaching")
	end := issue769AwaitSubTurnEnd(t, events.C)
	assert.Equal(t, SubTurnStatusCancelled, end.Status)

	rawStillActive, stillActive := al.activeTurnStates.Load("issue-769-child-prior-abort")
	require.True(t, stillActive,
		"the cancellation-detached child must remain registered until physical exit")
	assert.Same(t, childTS, rawStillActive)

	releaseStuckTool()
	select {
	case <-stuck.finished:
	case <-time.After(time.Second):
		t.Fatal("cancellation-ignoring tool did not finish after release")
	}
	select {
	case <-childTS.Finished():
	case <-time.After(time.Second):
		t.Fatal("detached child turn did not finish after its stuck tool returned")
	}
	assert.Equal(t, 1, childProvider.CallCount(),
		"the detached hard-aborted child must not make another model call")
}
