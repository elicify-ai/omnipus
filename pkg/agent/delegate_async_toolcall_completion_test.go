// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for a live-UAT-reported MEDIUM-severity completeness
// bug: for EVERY background-mode (`delegate(async=true)`) delegation whose
// delegate makes at least one tool call before its final answer, the
// delegate's synthesized final answer never reached the chat — only its
// pre-tool-call preamble sentence appeared, live AND after reload. The one
// delegation with ZERO tool-call steps (a direct-answer delegation) worked
// correctly.
//
// Root cause: pkg/tools/delegate.go's executeAsync never set
// SubTurnConfig.Critical on the spawned sub-turn. Background delegation's
// entire premise is "the parent moves on; tell me later" — the parent turn
// routinely finishes (Finish(false), after its own brief follow-up LLM call
// acknowledging the "running in background" ack) well before the delegate
// completes even one tool call. Without Critical:true, the delegate's own
// turn loop (pkg/agent/loop.go, the "Parent turn ended" check evaluated
// early in each iteration, before the next LLM call) sees !ts.critical &&
// ts.IsParentEnded() and exits gracefully — silently discarding the
// synthesized answer for any task that needed more than one LLM turn. The
// preamble survives (it was already persisted per-iteration), but the real
// answer is never even generated: spawnSubTurn's result comes back with
// ForLLM/ForUser == "", and asyncCallback's `content == "" { return }` guard
// then drops it with zero trace — no error, no notification, nothing
// delivered, live or on reload. A synchronous ("await", async=false)
// delegation never hits this: the parent is blocked inside the very tool
// call that would need to finish first, so it cannot have "ended" yet.
//
// This test drives the REAL production chain end to end — a real
// *tools.DelegateTool wired to a real spawner on a real AgentLoop — and
// deterministically forces the exact race the live bug hit: the parent's
// own turn is confirmed fully finished (Finish(false) already called)
// BEFORE the delegate's tool call is allowed to complete, so the delegate's
// loop-top IsParentEnded() check on its second iteration observes the
// parent as ended, exactly as in production.

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	delegateToolCallParentRootTask = "PARENT_ROOT_TASK-a91cf3: please research something and tell me"
	delegateToolCallChildMarker    = "CHILD_TASK_MARKER-a91cf3: research the topic and report back"
	delegateToolCallPreamble       = "Let me research current, factual information before answering."
	delegateToolCallFinalAnswer    = "FINAL_SYNTHESIZED_ANSWER-a91cf3: here is what the research found."
)

// contentRoutedDelegateProvider dispatches scripted responses by inspecting
// the LAST user-role message and whether a tool-role message is already
// present, rather than by raw Chat() call order. This sidesteps the genuine
// concurrency race between the parent turn's own continuation and the
// spawned child sub-turn's continuation (both call Chat() on the SAME
// provider instance, from different goroutines, the instant the delegate
// tool call's synchronous ack returns) — content-based routing makes the
// test's SCRIPT deterministic even though the underlying goroutine
// interleaving is not, which is exactly what lets the test separately force
// the parent-finishes-first ordering via the gate tool below.
type contentRoutedDelegateProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *contentRoutedDelegateProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	var lastUser string
	hasToolResult := false
	for _, m := range messages {
		switch m.Role {
		case "user":
			lastUser = m.Content
		case "tool":
			hasToolResult = true
		}
	}

	switch {
	case strings.Contains(lastUser, delegateToolCallChildMarker) && !hasToolResult:
		// Child's iteration 1: narration + a tool call, mirroring the live
		// UAT repro (a preamble sentence, then a tool step).
		return &providers.LLMResponse{
			Content: delegateToolCallPreamble,
			ToolCalls: []providers.ToolCall{
				{
					ID:       "gate_tool-0",
					Function: &providers.FunctionCall{Name: "gate_tool", Arguments: "{}"},
				},
			},
		}, nil
	case strings.Contains(lastUser, delegateToolCallChildMarker) && hasToolResult:
		// Child's iteration 2: the real synthesized final answer, produced
		// AFTER the tool result — this is the content the bug dropped.
		return &providers.LLMResponse{Content: delegateToolCallFinalAnswer}, nil
	case strings.Contains(lastUser, delegateToolCallParentRootTask) && !hasToolResult:
		// Parent's iteration 1: delegate in the background.
		argsJSON := fmt.Sprintf(`{"task":%q,"async":true}`, delegateToolCallChildMarker)
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "delegate-0", Function: &providers.FunctionCall{Name: "delegate", Arguments: argsJSON}},
			},
		}, nil
	case strings.Contains(lastUser, delegateToolCallParentRootTask) && hasToolResult:
		// Parent's iteration 2: a brief ack, then the parent turn finishes —
		// this is what races ahead of the child in the live bug.
		return &providers.LLMResponse{Content: "Delegated. I'll let you know."}, nil
	default:
		return nil, fmt.Errorf(
			"contentRoutedDelegateProvider: no scripted route for lastUser=%q hasToolResult=%v",
			lastUser, hasToolResult,
		)
	}
}

func (p *contentRoutedDelegateProvider) GetDefaultModel() string { return "content-routed-mock" }

func (p *contentRoutedDelegateProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// gateTool is a test-only tool whose Execute blocks until explicitly
// unblocked. It stands in for the delegate's real research tool call and
// lets the test hold the child sub-turn at "tool executing" until it has
// independently confirmed the parent turn has fully finished — the precise
// ordering the live bug depended on.
//
// entered is closed (via enteredOnce) the instant Execute is reached, so a
// caller can actively WAIT for the child to arrive there instead of racing
// an instantaneous atomic-load check against the child goroutine's own
// (variable-latency) startup — under load (e.g. running alongside other
// tests' leftover background goroutines) that startup can take longer than
// the time between the parent finishing and the test checking, which would
// make an instantaneous check flaky without indicating any real bug.
type gateTool struct {
	tools.BaseTool
	unblock     chan struct{}
	entered     chan struct{}
	enteredOnce sync.Once
}

func (g *gateTool) Name() string        { return "gate_tool" }
func (g *gateTool) Description() string { return "test-only: blocks until deliberately unblocked" }
func (g *gateTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (g *gateTool) Scope() tools.ToolScope { return tools.ScopeCore }

func (g *gateTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	g.enteredOnce.Do(func() { close(g.entered) })
	select {
	case <-g.unblock:
		return tools.SilentResult("research step complete")
	case <-ctx.Done():
		return tools.ErrorResult("gate_tool: context canceled while waiting to be unblocked")
	}
}

// TestDelegateAsyncCompletion_ToolCallThenFinalAnswer_SurvivesParentFinish is
// the regression test for the live UAT bug described in this file's header.
func TestDelegateAsyncCompletion_ToolCallThenFinalAnswer_SurvivesParentFinish(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "content-routed-mock"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	provider := &contentRoutedDelegateProvider{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	unblock := make(chan struct{})
	gt := &gateTool{unblock: unblock, entered: make(chan struct{})}
	al.RegisterTool(gt)

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(NewSubTurnSpawner(al))
	// ADR-057 U14 fixture repair: DelegateTool now refuses to start a
	// delegated session without a durable lifecycle store wired (fail-closed
	// rather than an untracked, unrecoverable session) — mirrors
	// message_parent_real_context_test.go's wiring.
	lifecycleStore := session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle"))
	delegateTool.SetLifecycleStore(lifecycleStore)
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	al.RegisterTool(delegateTool)

	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"delegate":  "allow",
				"gate_tool": "allow",
			},
		})
	}

	const sessionKey = "test-session-delegate-async-toolcall"
	ctx := context.Background()

	parentDone := make(chan struct{})
	var parentErr error
	go func() {
		defer close(parentDone)
		_, parentErr = al.ProcessDirectWithChannel(ctx, delegateToolCallParentRootTask, sessionKey, "telegram", "77777")
	}()

	// Wait for the PARENT turn to fully finish — including its own Finish(false)
	// call — before letting the delegate's blocked tool call return. This is
	// the deterministic version of the live race: the delegate is parked
	// inside gate_tool's first (and only) tool call the entire time the
	// parent races ahead, acks, and completes.
	select {
	case <-parentDone:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: parent turn never finished")
	}
	require.NoError(t, parentErr)

	// Confirm the delegate genuinely reached its tool call — actively WAIT
	// (rather than an instantaneous atomic check) since the child goroutine's
	// own startup latency is independent of, and may outlast, however long
	// the parent's turn happened to take. This proves the scripted routing
	// produced a real tool call, without making the test racy under load.
	select {
	case <-gt.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: delegate never reached its tool call")
	}

	// Now that the parent has genuinely ended AND the delegate is confirmed
	// parked inside its tool call, let the tool call complete. The child's
	// loop will re-check IsParentEnded() at the top of its next iteration and
	// find it true — reproducing the exact live scenario.
	close(unblock)

	var msg bus.InboundMessage
	select {
	case msg = <-msgBus.InboundChan():
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: timed out waiting for the delegate's async completion notification — " +
			"a background delegate that made a tool call before its final answer never delivered its " +
			"synthesized content (regression: SubTurnConfig.Critical must be set for async delegation)")
	}

	assert.Equal(t, "system", msg.Channel)
	assert.Equal(t, "async:delegate", msg.Sender.CanonicalID)
	assert.Equal(t, "telegram:77777", msg.ChatID, "must carry the ORIGINATING channel/chat")

	assert.Contains(t, msg.Content, delegateToolCallFinalAnswer,
		"the delegate's REAL final synthesized answer (produced AFTER its tool call) must be delivered — "+
			"not silently dropped just because the parent turn finished first")
	assert.NotEqual(t, delegateToolCallPreamble, msg.Content,
		"must not deliver only the pre-tool-call preamble sentence")
	assert.NotEmpty(t, msg.Content, "delegate result content must not be empty")

	// The child sub-turn must have made exactly two LLM calls of its own
	// (tool-call round, then the final content-only round) on top of the
	// parent's own two calls — proving the follow-up call actually happened
	// rather than the loop exiting early with an empty finalContent.
	assert.Equal(t, 4, provider.CallCount(),
		"expected 2 parent calls + 2 child calls (tool-call round, then final answer round)")
}
