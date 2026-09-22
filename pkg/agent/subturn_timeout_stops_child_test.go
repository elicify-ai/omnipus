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
	"strings"
	"sync"
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

func assertPersistedDelegatedTaskNotice(
	t *testing.T,
	store *session.UnifiedStore,
	parentSessionID string,
	wantParts []string,
	forbiddenParts []string,
) {
	t.Helper()
	entries, err := store.ReadTranscript(parentSessionID)
	require.NoError(t, err, "the delegating chat transcript must be readable after the child stops")

	var notices []session.TranscriptEntry
	for _, entry := range entries {
		if entry.ErrorCode == string(CodeDelegatedTaskLimit) {
			notices = append(notices, entry)
		}
	}
	require.Len(t, notices, 1,
		"the delegating chat must persist exactly one identified task-limit notice for replay")
	for _, part := range wantParts {
		assert.Contains(t, notices[0].Content, part)
	}
	for _, part := range forbiddenParts {
		assert.NotContains(t, notices[0].Content, part,
			"the replay notice may carry identifiers, never delegated prompts or raw child output")
	}
}

func TestDelegatedTaskNoticeIdentityBoundsAndFlattensLabel(t *testing.T) {
	label := "  " + strings.Repeat("界", delegatedTaskNoticeLabelMaxRunes+20) + "\nraw continuation  "
	identity := delegatedTaskNoticeIdentity(
		&turnState{turnID: "child-turn", agentID: "builder"},
		label,
		"delegate-14",
		"child-session-14",
	)

	lines := strings.Split(identity, "\n")
	require.Len(t, lines, 3, "the label must stay on one line so it cannot mimic another identifier")
	assert.LessOrEqual(t, len([]rune(strings.TrimPrefix(lines[0], "Label: "))), delegatedTaskNoticeLabelMaxRunes)
	assert.True(t, strings.HasSuffix(lines[0], "…"), "a truncated label must make the truncation visible")
	assert.NotContains(t, identity, "raw continuation")
	assert.Equal(t, "Task ID: delegate-14", lines[1])
	assert.Equal(t, "Session: child-session-14", lines[2])
	assert.Less(t, len(identity), 4096, "the identity block must fit inside the LLMError wire-message cap")
}

func TestSubTurnTimedOutResult_PublishesIdentifiedNoticeWhenChildAlreadyTimedOut(t *testing.T) {
	const (
		delegatedTaskID    = "delegate-backstop-race"
		delegatedTaskLabel = "backstop-race"
		childSessionID     = "child-backstop-race"
	)

	al, home := schedTestLoop(t)
	parent := registerAgent(t, al, home, "lead", testutil.NewScenario().WithText("parent idle"), true)
	child := registerAgent(t, al, home, "builder", testutil.NewScenario().WithText("child idle"), false)
	parentSessionID, store := stiMintParentSession(t, al)
	_, createErr := store.CreateSessionWithID(
		childSessionID, parentSessionID, session.SessionTypeDelegate, "", "builder",
	)
	require.NoError(t, createErr)
	parentTS := &turnState{
		turnID:              "parent-backstop-race",
		agentID:             "lead",
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     store,
	}
	childTS := &turnState{
		turnID:              "child-backstop-race-turn",
		agentID:             "builder",
		agent:               child,
		sessionKey:          childSessionID,
		transcriptSessionID: childSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     store,
		parentTurnState:     parentTS,
	}
	sub := al.SubscribeEvents(16)
	var frames []ErrorPayload
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range sub.C {
			if p, ok := evt.Payload.(ErrorPayload); ok && evt.Kind == EventKindError {
				frames = append(frames, p)
			}
		}
	}()

	_, _, childTimeoutErr := al.typedTurnExit(childTS, 1, "test-model", context.DeadlineExceeded)
	require.ErrorIs(t, childTimeoutErr, ErrTurnTimedOut)
	// Simulate the reverse ownership ordering: the child's own deadline exit
	// finishes before the delegation timeout latches its hard abort.
	require.True(t, childTS.requestHardAbort())
	_, err := subTurnTimedOutResult(
		al,
		childTS,
		delegatedTaskID,
		delegatedTaskLabel,
		childSessionID,
		5*time.Second,
		childTimeoutErr,
		false,
	)
	require.Error(t, err)
	al.UnsubscribeEvents(sub.ID)
	<-done
	require.Len(t, frames, 1,
		"the child timeout record and controller notice must produce one live operator error, not two")
	assert.Equal(t, string(CodeDelegatedTaskLimit), frames[0].Code)

	assertPersistedDelegatedTaskNotice(t, store, parentSessionID,
		[]string{
			"Label: " + delegatedTaskLabel,
			"Task ID: " + delegatedTaskID,
			"Session: " + childSessionID,
			"Limit: timeout_seconds (5s)",
		},
		nil,
	)
	childEntries, readErr := store.ReadTranscript(childSessionID)
	require.NoError(t, readErr)
	var childTimeouts int
	for _, entry := range childEntries {
		if entry.ErrorCode == string(CodeTurnTimedOut) {
			childTimeouts++
		}
	}
	assert.Equal(t, 1, childTimeouts,
		"the child keeps exactly one private timeout record while the controller owns live publication")
}

func TestSpawnSubTurn_SettledTimeoutPublicationChoosesSingleOwner(t *testing.T) {
	al, home := schedTestLoop(t)
	child := registerAgent(t, al, home, "builder", testutil.NewScenario().WithText("child idle"), false)
	childTS := &turnState{
		turnID:           "child-settled-timeout",
		agentID:          "builder",
		agent:            child,
		routingSessionID: session.RoutingSessionID("root-session"),
		parentTurnState:  &turnState{turnID: "root-turn"},
	}
	ex := &spawnSubTurnExecutionState{
		ss:      &spawnSubTurnSetupState{st: &spawnSubTurnState{al: al, childTS: childTS}},
		turnErr: errors.Join(ErrTurnTimedOut, context.DeadlineExceeded),
	}

	sub := al.SubscribeEvents(2)
	defer al.UnsubscribeEvents(sub.ID)
	ex.publishSettledNativeTimeout()
	select {
	case evt := <-sub.C:
		payload, ok := evt.Payload.(ErrorPayload)
		require.True(t, ok)
		assert.Equal(t, string(CodeTurnTimedOut), payload.Code,
			"a child-owned timeout must publish one generic timeout after settlement")
	default:
		t.Fatal("child-owned timeout did not publish its generic live notice")
	}

	ex.forceCancelFired = true
	ex.publishSettledNativeTimeout()
	select {
	case evt := <-sub.C:
		t.Fatalf("delegation-owned timeout must defer to the identified notice, got extra event %#v", evt)
	default:
	}
}

func TestDelegatedTaskLimitNotice_PersistsOnlyToNestedRootTranscript(t *testing.T) {
	const (
		middleSessionID = "middle-delegate-session"
		leafSessionID   = "leaf-delegate-session"
	)
	al, home := schedTestLoop(t)
	leafAgent := registerAgent(t, al, home, "leaf", testutil.NewScenario().WithText("leaf idle"), false)
	rootSessionID, store := stiMintParentSession(t, al)
	_, err := store.CreateSessionWithID(middleSessionID, rootSessionID, session.SessionTypeDelegate, "", "middle")
	require.NoError(t, err)
	_, err = store.CreateSessionWithID(leafSessionID, middleSessionID, session.SessionTypeDelegate, "", "leaf")
	require.NoError(t, err)

	rootTS := &turnState{
		turnID:              "root-turn",
		transcriptSessionID: rootSessionID,
		transcriptStore:     store,
	}
	middleTS := &turnState{
		turnID:              "middle-turn",
		transcriptSessionID: middleSessionID,
		transcriptStore:     store,
		parentTurnState:     rootTS,
	}
	leafTS := &turnState{
		turnID:              "leaf-turn",
		agentID:             "leaf",
		agent:               leafAgent,
		transcriptSessionID: leafSessionID,
		routingSessionID:    session.RoutingSessionID(rootSessionID),
		transcriptStore:     store,
		parentTurnState:     middleTS,
	}

	notice := newDelegatedTimeoutNotice(leafTS, "nested leaf", "delegate-nested", leafSessionID, 3*time.Second, false)
	al.emitDelegatedTaskLimitNotice(leafTS, leafTS.eventMeta("spawnSubTurn", "subturn.force_cancel"), notice)

	assertPersistedDelegatedTaskNotice(t, store, rootSessionID,
		[]string{"Label: nested leaf", "Task ID: delegate-nested", "Session: " + leafSessionID}, nil)
	for _, sessionID := range []string{middleSessionID, leafSessionID} {
		entries, readErr := store.ReadTranscript(sessionID)
		require.NoError(t, readErr)
		for _, entry := range entries {
			assert.NotEqual(t, string(CodeDelegatedTaskLimit), entry.ErrorCode,
				"identified notice belongs only to the root transcript, not %s", sessionID)
		}
	}
}

// TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls: a delegated child whose
// model response queues [slow_step, write_artifact] is still inside slow_step
// when its time limit fires. After the deadline the child must NOT go on to
// execute write_artifact, must NOT make another model call, and the caller
// must see a timeout (not a success, not a user cancel).
func TestSubTurn_TimedOutChild_StartsNoFurtherToolCalls(t *testing.T) {
	const (
		delegatedTaskID    = "delegate-12"
		delegatedTaskLabel = "build-docs-renderer"
		childSessionID     = "8813c467-1b06-4e4f-a5ba-77e33a6340fb"
	)

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

	// The force-cancel also records WHY the child stopped (asserted at the
	// end). Subscribe with a generous buffer so the child event burst does not
	// consume the identified notice's bounded retry window before it is
	// observed here.
	sub := al.SubscribeEvents(1024)
	var (
		framesMu sync.Mutex
		frames   []ErrorPayload
	)
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for evt := range sub.C {
			if p, ok := evt.Payload.(ErrorPayload); ok && evt.Kind == EventKindError && p.Stage == "subturn_timeout" {
				framesMu.Lock()
				frames = append(frames, p)
				framesMu.Unlock()
			}
		}
	}()
	var unsubscribeOnce sync.Once
	unsubscribe := func() {
		unsubscribeOnce.Do(func() {
			al.UnsubscribeEvents(sub.ID)
			<-collected
		})
	}
	t.Cleanup(unsubscribe)

	const limit = 1500 * time.Millisecond

	type outcome struct {
		res      *tools.ToolResult
		err      error
		returned time.Time
	}
	done := make(chan outcome, 1)
	spawnedAt := time.Now()
	go func() {
		spawnCtx := withSpawnToolCallID(withTurnState(context.Background(), parentTS), "test-spawn-a17-timeout")
		res, err := NewSubTurnSpawner(al).SpawnSubTurn(spawnCtx, tools.SubTurnConfig{
			Model:             "test-model",
			SystemPrompt:      "write the two files",
			TargetAgentID:     "builder",
			TaskID:            delegatedTaskID,
			TaskLabel:         delegatedTaskLabel,
			DelegateSessionID: childSessionID,
			Async:             false,
			Timeout:           limit,
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

	// The force-cancel's error frame: exactly one, on the delegating chat's
	// routing session (ADR-057: a child inherits it, so a reload or a second
	// tab of that chat still receives the frame), classified as a timeout and
	// worded as the delegation's own time limit.
	unsubscribe()
	framesMu.Lock()
	defer framesMu.Unlock()
	require.Len(t, frames, 1, "want exactly one subturn_timeout error frame for the force-cancelled child")
	assert.Equal(t, parentSessionID, frames[0].SessionID,
		"the force-cancel frame must carry the delegating chat's routing session id")
	assert.Equal(t, string(CodeDelegatedTaskLimit), frames[0].Code)
	assert.Contains(t, frames[0].Message, "Label: "+delegatedTaskLabel)
	assert.Contains(t, frames[0].Message, "Task ID: "+delegatedTaskID)
	assert.Contains(t, frames[0].Message, "Session: "+childSessionID)
	assert.Contains(t, frames[0].Message, "Limit: timeout_seconds ("+limit.String()+")")
	assert.NotContains(t, frames[0].Message, "write the two files",
		"the operator notice may carry identifiers, never the delegated prompt or child output")
	assert.NotContains(t, frames[0].Message, "slow step ended",
		"the operator notice may carry identifiers, never raw child tool output")
	assertPersistedDelegatedTaskNotice(t, sessionStore, parentSessionID,
		[]string{
			"Label: " + delegatedTaskLabel,
			"Task ID: " + delegatedTaskID,
			"Session: " + childSessionID,
			"Limit: timeout_seconds (" + limit.String() + ")",
		},
		[]string{"write the two files", "slow step ended"},
	)
}

func TestSubTurn_MaxToolIterationsPublishesIdentifiedOperatorNotice(t *testing.T) {
	const (
		delegatedTaskID    = "delegate-13"
		delegatedTaskLabel = "build-docs-renderer"
		childSessionID     = "6813c467-1b06-4e4f-a5ba-77e33a6340fc"
	)

	al, home := schedTestLoop(t)
	parent := registerAgent(t, al, home, "lead", testutil.NewScenario().WithText("parent idle"), true)
	child := registerAgent(t, al, home, "builder", testutil.NewScenario().WithToolCall("write_artifact", `{}`), false)
	child.MaxIterations = 1
	write := &recordingWriteTool{}
	child.Tools.Register(write)
	child.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"write_artifact": "allow"}})

	parentSessionID, sessionStore := stiMintParentSession(t, al)
	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-max-tool-iterations",
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		session:             &ephemeralSessionStore{},
		agent:               parent,
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     sessionStore,
	}

	sub := al.SubscribeEvents(1024)
	var (
		framesMu sync.Mutex
		frames   []ErrorPayload
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for evt := range sub.C {
			if p, ok := evt.Payload.(ErrorPayload); ok && evt.Kind == EventKindError && p.Stage == "subturn_limit" {
				framesMu.Lock()
				frames = append(frames, p)
				framesMu.Unlock()
			}
		}
	}()

	spawnCtx := withSpawnToolCallID(withTurnState(context.Background(), parentTS), "test-spawn-max-tool-iterations")
	result, err := NewSubTurnSpawner(al).SpawnSubTurn(spawnCtx, tools.SubTurnConfig{
		Model:             "test-model",
		SystemPrompt:      "write the artifact",
		TargetAgentID:     "builder",
		TaskID:            delegatedTaskID,
		TaskLabel:         delegatedTaskLabel,
		DelegateSessionID: childSessionID,
		Timeout:           10 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, toolLimitResponse, result.ForLLM)
	assert.Equal(t, int32(1), write.calls.Load())

	al.UnsubscribeEvents(sub.ID)
	<-done
	framesMu.Lock()
	defer framesMu.Unlock()
	require.Len(t, frames, 1, "max_tool_iterations must publish one deterministic operator notice")
	assert.Equal(t, parentSessionID, frames[0].SessionID)
	assert.Equal(t, "delegated_task_limit", frames[0].Code)
	assert.Contains(t, frames[0].Message, "Label: "+delegatedTaskLabel)
	assert.Contains(t, frames[0].Message, "Task ID: "+delegatedTaskID)
	assert.Contains(t, frames[0].Message, "Session: "+childSessionID)
	assert.Contains(t, frames[0].Message, "Limit: max_tool_iterations (1)")
	assert.NotContains(t, frames[0].Message, "wrote artifact",
		"the operator notice may carry identifiers, never raw child tool output")
	assertPersistedDelegatedTaskNotice(t, sessionStore, parentSessionID,
		[]string{
			"Label: " + delegatedTaskLabel,
			"Task ID: " + delegatedTaskID,
			"Session: " + childSessionID,
			"Limit: max_tool_iterations (1)",
		},
		[]string{"write the artifact", "wrote artifact"},
	)
}
