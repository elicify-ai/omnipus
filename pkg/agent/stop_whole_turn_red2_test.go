// stop_whole_turn_red2_test.go: #1534 RED round 2 — Q1 = A, "Stop ends the
// WHOLE turn" (founder decision 2026-10-31T16:25Z, relayed by the chief;
// receipt /workspace/logs/1534/check/founder-q1-receipt.md). After the server
// claims a Stop there is no further tool call, no graceful-terminal wrap-up
// provider round and no continuation text; the saved answer ends where the
// screen stopped.
//
// What changes vs today (the RED reasons): the cancel path still arms the
// graceful stage at a tool boundary (cancel_stop.go::interruptCancelTargets
// calls requestGracefulInterrupt), and the turn loop then runs ONE more
// provider round whose wrap-up answer is saved
// (loop_run_turn.go::prepareCallMessages gracefulTerminal branch;
// loop_run_turn_tools.go::finishCall's graceful skip only skips the remaining
// tools, it does not end the turn). The success path likewise appends a
// post-claim response into the continuation accumulator
// (loop_run_turn_iterations.go::finishTextResponse) and the tool-call path
// saves post-claim narration and dispatches the calls
// (loop_run_turn_response.go::recordToolCalls) without consulting
// stopRequested.
//
// Scope note: the founder decision governs the USER STOP (RequestCancel with
// TurnOnly / the /stop seam, StopSessionTurn). The session-level graceful
// interrupt (steering.go::Interrupt, administrative cancel) and hard abort
// keep today's behaviour; the existing tests that pin the wrap-up round for
// THOSE paths (steering_test.go::TestAgentLoop_InterruptGraceful_UsesTerminalNoToolCall,
// truncation_outcome_test.go::TestTruncationD6_GracefulStopBeatsContinuation)
// drive those other paths and are unchanged by this round.
package agent

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// stopTurnScriptStep is one scripted provider response for
// stopTurnScriptProvider.
type stopTurnScriptStep struct {
	content      string
	finishReason string
	toolCalls    []providers.ToolCall
	err          error
	// onCall runs synchronously INSIDE Chat, before the step's response is
	// returned — the deterministic place to claim the Stop while the round
	// is in flight (the claim strictly precedes the response).
	onCall func()
}

// stopTurnScriptProvider plays back a fixed sequence of responses and fails
// loudly on any call past the scripted end — an unexpected extra provider
// call (a wrap-up round that Q1 = A forbids) must fail the test with a clear
// message, never silently pass (same rationale as
// truncation_outcome_test.go's truncationScriptedProvider).
type stopTurnScriptProvider struct {
	mu    sync.Mutex
	steps []stopTurnScriptStep
	calls int
}

func (p *stopTurnScriptProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	var step stopTurnScriptStep
	haveStep := idx < len(p.steps)
	if haveStep {
		step = p.steps[idx]
	}
	p.mu.Unlock()

	if !haveStep {
		return nil, fmt.Errorf("stopTurnScriptProvider: unscripted provider call %d (only %d scripted) — "+
			"the loop made a provider call it should not have", idx+1, len(p.steps))
	}
	if step.onCall != nil {
		step.onCall()
	}
	if step.err != nil {
		return nil, step.err
	}
	return &providers.LLMResponse{
		Content:      step.content,
		FinishReason: step.finishReason,
		ToolCalls:    step.toolCalls,
	}, nil
}

func (p *stopTurnScriptProvider) GetDefaultModel() string { return "stop-turn-scripted-model" }

func (p *stopTurnScriptProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// stopGateTool blocks inside Execute until its release channel is closed —
// the running tool whose execution window the Stop is claimed in.
type stopGateTool struct {
	name       string
	started    chan struct{}
	release    chan struct{}
	startOnce  sync.Once
	releaseOne sync.Once
	execs      atomic.Int32
}

func newStopGateTool(name string) *stopGateTool {
	return &stopGateTool{name: name, started: make(chan struct{}), release: make(chan struct{})}
}

func (t *stopGateTool) Name() string                 { return t.name }
func (t *stopGateTool) Description() string          { return "blocks until released (RED round 2 fixture)" }
func (t *stopGateTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *stopGateTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *stopGateTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *stopGateTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	t.execs.Add(1)
	t.startOnce.Do(func() { close(t.started) })
	<-t.release
	return tools.SilentResult("executed " + t.name)
}

func (t *stopGateTool) letGo() { t.releaseOne.Do(func() { close(t.release) }) }

// stopCountTool counts executions and returns immediately — the QUEUED tool
// that Q1 = A says must never start once the Stop is claimed.
type stopCountTool struct {
	name  string
	execs atomic.Int32
}

func (t *stopCountTool) Name() string                 { return t.name }
func (t *stopCountTool) Description() string          { return "counts executions (RED round 2 fixture)" }
func (t *stopCountTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (t *stopCountTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (t *stopCountTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *stopCountTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	t.execs.Add(1)
	return tools.SilentResult("executed " + t.name)
}

// stopTurnToolCall builds a providers.ToolCall for the scripted steps.
func stopTurnToolCall(id, name string) providers.ToolCall {
	return providers.ToolCall{
		ID:   id,
		Type: "function",
		Name: name,
		Function: &providers.FunctionCall{
			Name:      name,
			Arguments: "{}",
		},
		Arguments: map[string]any{},
	}
}

// stopTurnAssistantEntries reads the transcript a reload would read and
// returns every assistant entry's content.
func stopTurnAssistantEntries(t *testing.T, store *session.UnifiedStore, sid string) []string {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err, "read transcript")
	var bodies []string
	for _, e := range entries {
		if e.Role == "assistant" {
			bodies = append(bodies, e.Content)
		}
	}
	return bodies
}

type stopTurnResult struct {
	answer string
	err    error
}

// stopTurnRun launches the turn through the real loop and returns the
// answer/err pair via a channel, so the test can claim the Stop mid-turn.
func stopTurnRun(al *AgentLoop, inst *AgentInstance, store *session.UnifiedStore, sessionID string) <-chan stopTurnResult {
	opts := processOptions{
		SessionKey:          "stop-whole-turn-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "do the thing",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	}
	ch := make(chan stopTurnResult, 1)
	go func() {
		answer, err := al.runAgentLoop(context.Background(), inst, opts)
		ch <- stopTurnResult{answer: answer, err: err}
	}()
	return ch
}

// TestStopWholeTurn_NoProviderCallOrToolAfterToolBoundaryStop pins Q1 = A at
// the tool boundary: the user Stop arrives while tool_one is executing (the
// claim precedes its release, through the real /stop seam); tool_two is
// still queued. The turn must END there — exactly one provider call, the
// queued tool never starts, and no wrap-up text produced or saved after the
// claim.
//
// RED today: the graceful stage skips tool_two but then runs ONE MORE
// provider round (prepareCallMessages' gracefulTerminal branch) whose
// wrap-up answer becomes the turn's answer and a saved transcript entry.
//
// BDD:
//
//	Given a turn whose first round returned narration "BEFORE-SHOWN" and
//	  requested a blocking tool and a queued tool,
//	When the user presses Stop (real StopSessionTurn seam) while the blocking
//	  tool is executing, and the tool then returns,
//	Then the provider is called exactly once (no wrap-up round),
//	And the queued tool never executes,
//	And the answer and every saved assistant entry exclude the wrap-up text
//	  while the shown "BEFORE-SHOWN" narration is retained.
func TestStopWholeTurn_NoProviderCallOrToolAfterToolBoundaryStop(t *testing.T) {
	const shown = "BEFORE-SHOWN"
	const wrapup = "WRAP-UP-AFTER-STOP"

	gate := newStopGateTool("stop_gate_one")
	count := &stopCountTool{name: "stop_count_two"}

	provider := &stopTurnScriptProvider{}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	t.Cleanup(gate.letGo) // release before the harness closes the loop on failure
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			gate.Name():  config.ToolPolicyAllow,
			count.Name(): config.ToolPolicyAllow,
		},
	})
	al.RegisterTool(gate)
	al.RegisterTool(count)

	provider.steps = []stopTurnScriptStep{
		{
			content: shown,
			toolCalls: []providers.ToolCall{
				stopTurnToolCall("call_1", gate.Name()),
				stopTurnToolCall("call_2", count.Name()),
			},
		},
		// The wrap-up round Q1 = A forbids. Scripted (not left unscripted) so
		// the RED failure is the behavioural assertion below, not the script
		// guard — CallCount() still catches the extra round either way.
		{content: wrapup},
	}

	resultCh := stopTurnRun(al, inst, store, sessionID)

	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the blocking tool to start")
	}

	// The real user Stop: the same /stop seam the web Stop, /stop and /cancel
	// command paths call (command_controls.go::StopSessionTurn → RequestCancel
	// → claimActiveTurn → interruptCancelTargets). It returns after the claim
	// and the graceful stage are armed, before the tool is released.
	fired, _, err := al.StopSessionTurn(context.Background(), sessionID, "human-red2", "web")
	require.NoError(t, err, "StopSessionTurn must accept the live turn")
	require.True(t, fired, "the Stop must fire against the live turn")

	gate.letGo()

	var res stopTurnResult
	select {
	case res = <-resultCh:
	case <-time.After(15 * time.Second):
		stacks := make([]byte, 1<<20)
		n := runtime.Stack(stacks, true)
		t.Fatalf("timeout waiting for the stopped turn to end (provider calls so far: %d)\n=== GOROUTINE DUMP ===\n%s",
			provider.CallCount(), stacks[:n])
	}

	assert.Equal(t, 1, provider.CallCount(),
		"Q1 = A: after the claimed Stop the turn must end — no graceful-terminal wrap-up provider round")
	assert.Equal(t, int32(1), gate.execs.Load(),
		"the tool that was already running when the Stop was claimed executes exactly once (fixture sanity)")
	assert.Equal(t, int32(0), count.execs.Load(),
		"Q1 = A: no further tool call may start after the claimed Stop — the queued tool must never run")
	assert.NotContains(t, res.answer, wrapup,
		"Q1 = A: no continuation text after Stop — the turn's answer must not carry wrap-up text (got %q)", res.answer)
	bodies := stopTurnAssistantEntries(t, store, sessionID)
	for i, body := range bodies {
		assert.NotContains(t, body, wrapup,
			"Q1 = A: nothing produced after the claimed Stop may be saved (assistant entry %d: %q)", i, body)
	}
	assert.Contains(t, strings.Join(bodies, "\n"), shown,
		"the narration shown before the Stop stays saved — the answer ends where the screen stopped")
}

// TestStopWholeTurn_LateSuccessContentNeverEntersSavedAnswer pins the
// success-racing-Stop rule: a provider success that RETURNS after the claim
// must not have its response content appended into the continuation
// accumulator or the saved answer. Round 1 is truncated with content (the D6
// accumulator holds it); round 2's response is delivered strictly after the
// claim (the claim happens inside round 2's provider call, before it
// returns), so its entire content is post-claim.
//
// RED today: finishTextResponse appends the post-claim content into the
// accumulator (no stopRequested check), and the appended text becomes the
// answer and the saved transcript entry.
//
// BDD:
//
//	Given a turn whose first round returned truncated content "A-SHOWN"
//	  (arming the D6 continuation) and whose continuation round is in flight,
//	When the user presses Stop while that round is in flight and the provider
//	  then returns success with content "B-AFTER-STOP",
//	Then the answer and the saved assistant entry are exactly "A-SHOWN" —
//	  the post-claim content never joins them.
func TestStopWholeTurn_LateSuccessContentNeverEntersSavedAnswer(t *testing.T) {
	const shown = "A-SHOWN"
	const late = "B-AFTER-STOP"

	provider := &stopTurnScriptProvider{}
	claim := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	provider.steps = []stopTurnScriptStep{
		{content: shown, finishReason: "truncated"},
		{
			content: late,
			onCall: func() {
				close(claim) // round 2 is in flight — the test may claim the Stop
				<-release    // the claim strictly precedes the response
			},
		},
	}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	t.Cleanup(letGo)

	resultCh := stopTurnRun(al, inst, store, sessionID)

	select {
	case <-claim:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the continuation round to start")
	}
	fired, _, err := al.StopSessionTurn(context.Background(), sessionID, "human-red2", "web")
	require.NoError(t, err, "StopSessionTurn must accept the live turn")
	require.True(t, fired, "the Stop must fire against the live turn")
	letGo()

	var res stopTurnResult
	select {
	case res = <-resultCh:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for the stopped turn to end")
	}

	assert.Equal(t, 2, provider.CallCount(),
		"the truncated round and its continuation happened; no third call may follow the claimed Stop")
	assert.Equal(t, shown, res.answer,
		"Q1 = A: content delivered after the claimed Stop must never join the answer (got %q)", res.answer)
	bodies := stopTurnAssistantEntries(t, store, sessionID)
	require.NotEmpty(t, bodies, "the shown prefix must be saved")
	assert.Equal(t, shown, bodies[len(bodies)-1],
		"Q1 = A: the saved stopped answer ends where the screen stopped — the post-claim success content must not be saved (got %q)", bodies[len(bodies)-1])
}

// TestStopWholeTurn_NoNarrationOrToolStartAfterClaim pins the second-writer
// rule (CHECK F4) at the transcript level: a response that arrives strictly
// after the claim carrying narration AND tool calls must have neither its
// narration saved (loop_run_turn_response.go::recordToolCalls's intermediate
// assistant entry) nor its tool calls dispatched.
//
// RED today: recordToolCalls saves the post-claim narration as an
// intermediate assistant entry (and marks the streamer persisted), and the
// tool call executes.
//
// BDD:
//
//	Given a turn whose provider call is in flight,
//	When the user presses Stop while it is in flight and the provider then
//	  returns success with narration "NARRATION-AFTER-STOP" and one tool call,
//	Then no saved assistant entry contains that narration,
//	And the tool never executes,
//	And no further provider call happens.
func TestStopWholeTurn_NoNarrationOrToolStartAfterClaim(t *testing.T) {
	const narration = "NARRATION-AFTER-STOP"

	count := &stopCountTool{name: "stop_count_after_claim"}

	provider := &stopTurnScriptProvider{}
	claim := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	provider.steps = []stopTurnScriptStep{
		{
			content:   narration,
			toolCalls: []providers.ToolCall{stopTurnToolCall("call_1", count.Name())},
			onCall: func() {
				close(claim)
				<-release
			},
		},
	}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	t.Cleanup(letGo)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{count.Name(): config.ToolPolicyAllow},
	})
	al.RegisterTool(count)

	resultCh := stopTurnRun(al, inst, store, sessionID)

	select {
	case <-claim:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the provider call to start")
	}
	fired, _, err := al.StopSessionTurn(context.Background(), sessionID, "human-red2", "web")
	require.NoError(t, err, "StopSessionTurn must accept the live turn")
	require.True(t, fired, "the Stop must fire against the live turn")
	letGo()

	var res stopTurnResult
	select {
	case res = <-resultCh:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for the stopped turn to end")
	}

	assert.Equal(t, 1, provider.CallCount(), "exactly the one in-flight provider call; nothing after the claim")
	assert.Equal(t, int32(0), count.execs.Load(),
		"Q1 = A: a tool call delivered after the claimed Stop must never be dispatched")
	assert.NotContains(t, res.answer, narration,
		"Q1 = A: post-claim narration must not surface as the turn's answer (got %q)", res.answer)
	for i, body := range stopTurnAssistantEntries(t, store, sessionID) {
		assert.NotContains(t, body, narration,
			"Q1 = A: the intermediate transcript writer must not save post-claim narration (assistant entry %d: %q)", i, body)
	}
}
