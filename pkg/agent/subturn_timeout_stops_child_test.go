// subturn_timeout_stops_child_test.go — UAT A-17 regression.
//
// DOCUMENTED INTENT (pkg/tools/delegate.go DelegateTool.Description and the
// timeout_seconds parameter schema): "A delegation is force-cancelled after
// timeout_seconds (default 300s / 5 min) if it has not finished by then."
// A timed-out delegation is therefore DEAD: once its time limit has passed it
// must not start another tool call (no further writes) and must not make
// another model call.
//
// THE GAP THIS PINS. spawnSubTurn bounds the child with
// context.WithTimeout(context.Background(), timeout). When that deadline
// fires while the child is inside a tool call, the tool returns, and runTurn's
// tool-dispatch loop moves on to the NEXT queued tool call of the same model
// response. That loop's only pre-dispatch stop check is
// turnState.hardAbortRequested() — which a context deadline never sets — and
// ToolRegistry.ExecuteWithContext does not check ctx before calling Execute.
// A file-writing tool that does not itself poll ctx (write_file/edit_file do
// not) therefore still ran after the parent had already been told the
// delegation failed.
//
// Oracle: derived from the documented intent above, never from the
// implementation — "after the time limit, no further tool call starts".
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// blockUntilCtxDoneTool stands in for a long-running child tool call (the
// A-17 child sat in one call for minutes). It blocks until its context is
// done — i.e. until the sub-turn's own deadline fires — and then returns, so
// the tool loop is free to reach the next queued call.
type blockUntilCtxDoneTool struct {
	tools.BaseTool
	started chan struct{}
	calls   atomic.Int32
}

func (b *blockUntilCtxDoneTool) Name() string        { return "slow_step" }
func (b *blockUntilCtxDoneTool) Description() string { return "A-17 stub: blocks until ctx is done" }
func (b *blockUntilCtxDoneTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (b *blockUntilCtxDoneTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (b *blockUntilCtxDoneTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	if b.calls.Add(1) == 1 {
		close(b.started)
	}
	<-ctx.Done()
	return &tools.ToolResult{ForLLM: "slow step ended: " + ctx.Err().Error(), IsError: true}
}

// recordingWriteTool stands in for write_file: it ignores ctx entirely (as the
// real file tools do) and records every execution with a timestamp.
type recordingWriteTool struct {
	tools.BaseTool
	calls      atomic.Int32
	lastCallAt atomic.Int64
}

func (r *recordingWriteTool) Name() string        { return "write_artifact" }
func (r *recordingWriteTool) Description() string { return "A-17 stub: records a write" }
func (r *recordingWriteTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (r *recordingWriteTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (r *recordingWriteTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	r.calls.Add(1)
	r.lastCallAt.Store(time.Now().UnixNano())
	return &tools.ToolResult{ForLLM: "wrote artifact"}
}

func toolCall(id, name string) providers.ToolCall {
	return providers.ToolCall{ID: id, Function: &providers.FunctionCall{Name: name, Arguments: `{}`}}
}

// TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls: a delegated child whose
// model response queues [slow_step, write_artifact] is still inside slow_step
// when its time limit fires. After the deadline the child must NOT go on to
// execute write_artifact, must NOT make another model call, and the caller
// must see a timeout (not a success, not a user cancel).
func TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls(t *testing.T) {
	al, home := schedTestLoop(t)

	parent := registerAgent(t, al, home, "lead", testutil.NewScenario().WithText("parent idle"), true)

	childProvider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{
			toolCall("call-slow", "slow_step"),
			toolCall("call-write-1", "write_artifact"),
		}).
		// Were the child to survive its deadline it would get this second
		// round too — another write, the exact A-17 symptom.
		WithToolCall("write_artifact", `{}`).
		WithText("all files written")
	child := registerAgent(t, al, home, "builder", childProvider, false)

	slow := &blockUntilCtxDoneTool{started: make(chan struct{})}
	write := &recordingWriteTool{}
	child.Tools.Register(slow)
	child.Tools.Register(write)
	child.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"slow_step": "allow", "write_artifact": "allow"},
	})

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-a17-timeout",
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     sessionStore,
	}

	const limit = 1500 * time.Millisecond

	type outcome struct {
		res      *tools.ToolResult
		err      error
		returned time.Time
	}
	done := make(chan outcome, 1)
	spawnedAt := time.Now()
	go func() {
		spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-a17-timeout")
		res, err := spawnSubTurn(spawnCtx, al, parentTS, SubTurnConfig{
			Model:         "test-model",
			SystemPrompt:  "write the two files",
			TargetAgentID: "builder",
			Async:         false,
			Timeout:       limit,
		})
		done <- outcome{res: res, err: err, returned: time.Now()}
	}()

	select {
	case <-slow.started:
	case <-time.After(limit):
		t.Fatal("precondition: the child never reached slow_step before its own time limit — the " +
			"test would pass vacuously (nothing queued behind the deadline). Setup is too slow for " +
			"the chosen limit.")
	}

	var got outcome
	select {
	case got = <-done:
	case <-time.After(limit + 20*time.Second):
		t.Fatal("spawnSubTurn did not return within 20s of the child's time limit")
	}

	// Give any straggling goroutine a window to run a late write before we look.
	time.Sleep(300 * time.Millisecond)

	assert.GreaterOrEqual(t, got.returned.Sub(spawnedAt), limit,
		"spawnSubTurn returned before the child's time limit — the test did not exercise a timeout")
	require.Error(t, got.err, "a timed-out delegation must return an error to its caller")
	assert.True(t, errors.Is(got.err, context.DeadlineExceeded),
		"the caller must be able to classify this as a timeout (errors.Is DeadlineExceeded); got %v", got.err)

	assert.Equal(t, int32(0), write.calls.Load(),
		"after its time limit the child executed write_artifact %d time(s) — a timed-out delegation is "+
			"documented as force-cancelled, so no queued tool call may start once the deadline has passed",
		write.calls.Load())
	assert.Equal(t, 1, childProvider.CallCount(),
		"after its time limit the child made another model call — a force-cancelled delegation must not")

	require.NotNil(t, got.res, "spawnSubTurn must return a result describing the timeout")
	assert.True(t, got.res.IsError, "a timed-out delegation must not read as a success: %q", got.res.ForLLM)
	assert.False(t, got.res.Interrupted,
		"a timeout is not a user/parent cancellation — it must not be reported as interrupted")
}
