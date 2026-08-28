// Regression coverage for a live-UAT-reported bug (re-verification 2026-07):
// the WS event forwarder's orphan watchdog (pkg/gateway/websocket.go,
// startOrphanWatchdog) synthesizes subagent_end{status:"interrupted"} for
// any subagent span still open orphanWatchdogTimeout after its PARENT turn
// ends. Before 7dd9e7a5 ("background delegate's final answer lost when
// parent finishes first"), a background (async) delegate needing more than
// one LLM turn silently exited its own loop early the instant its parent
// ended, so the real EventKindSubTurnEnd almost always closed the span
// (via closeCh) well before the watchdog fired. Critical:true (7dd9e7a5)
// now lets the delegate run for its full, genuine duration — so a normal,
// still-working delegation can legitimately still be open when the
// watchdog timer fires, and the watchdog fabricated a false "interrupted"
// terminal status for a turn that was, in truth, still generating —
// self-correcting only once the real EventKindSubTurnEnd arrived later and
// overwrote it (the "transient interrupted 0ms flicker that self-heals"
// symptom).
//
// The fix (pkg/gateway/websocket.go's startOrphanWatchdog) re-checks real
// liveness via agent.AgentLoop.IsSubTurnActiveForSpawnCall before firing;
// when the sub-turn is still genuinely active it reschedules instead of
// synthesizing a fabricated status. This test drives a REAL async Critical
// delegate (contentRoutedOrphanProvider + orphanGateTool, mirroring the
// production DelegateTool -> spawnSubTurn -> runTurn chain used by
// pkg/agent's own delegate_async_toolcall_completion_test.go) through a
// shortened orphan-watchdog timeout and proves no synthetic "interrupted"
// frame is ever emitted while the delegate is deliberately held open, and
// that the real completion frame arrives once it is released.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	orphanParentRootTask = "ORPHAN_PARENT_ROOT_TASK-f21ab8: please research something and tell me"
	orphanChildMarker    = "ORPHAN_CHILD_TASK_MARKER-f21ab8: research the topic and report back"
	orphanFinalAnswer    = "ORPHAN_FINAL_SYNTHESIZED_ANSWER-f21ab8: here is what the research found."
)

// contentRoutedOrphanProvider scripts responses by inspecting the last
// user-role message and whether a tool-role message is already present,
// mirroring pkg/agent/delegate_async_toolcall_completion_test.go's
// contentRoutedDelegateProvider exactly — content-based routing sidesteps
// the genuine concurrency race between the parent's and the child's own
// continuations, both of which call Chat() on this same provider instance.
type contentRoutedOrphanProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *contentRoutedOrphanProvider) Chat(
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
	case strings.Contains(lastUser, orphanChildMarker) && !hasToolResult:
		return &providers.LLMResponse{
			Content: "Let me research this before answering.",
			ToolCalls: []providers.ToolCall{
				{
					ID:       "orphan_gate_tool-0",
					Function: &providers.FunctionCall{Name: "orphan_gate_tool", Arguments: "{}"},
				},
			},
		}, nil
	case strings.Contains(lastUser, orphanChildMarker) && hasToolResult:
		return &providers.LLMResponse{Content: orphanFinalAnswer}, nil
	case strings.Contains(lastUser, orphanParentRootTask) && !hasToolResult:
		argsJSON := fmt.Sprintf(`{"task":%q,"async":true}`, orphanChildMarker)
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "orphan-delegate-0", Function: &providers.FunctionCall{Name: "delegate", Arguments: argsJSON}},
			},
		}, nil
	case strings.Contains(lastUser, orphanParentRootTask) && hasToolResult:
		return &providers.LLMResponse{Content: "Delegated. I'll let you know."}, nil
	default:
		return nil, fmt.Errorf(
			"contentRoutedOrphanProvider: no scripted route for lastUser=%q hasToolResult=%v",
			lastUser, hasToolResult,
		)
	}
}

func (p *contentRoutedOrphanProvider) GetDefaultModel() string { return "content-routed-orphan-mock" }

// orphanGateTool blocks until explicitly unblocked, letting the test hold
// the child sub-turn open past the (shortened) orphan watchdog timeout so
// the watchdog's liveness re-check is genuinely exercised rather than
// racing a fast-finishing child.
type orphanGateTool struct {
	tools.BaseTool
	unblock chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (g *orphanGateTool) Name() string { return "orphan_gate_tool" }
func (g *orphanGateTool) Description() string {
	return "test-only: blocks until deliberately unblocked"
}

func (g *orphanGateTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (g *orphanGateTool) Scope() tools.ToolScope { return tools.ScopeCore }

func (g *orphanGateTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	g.once.Do(func() { close(g.entered) })
	select {
	case <-g.unblock:
		return tools.SilentResult("research step complete")
	case <-ctx.Done():
		return tools.ErrorResult("orphan_gate_tool: context canceled while waiting to be unblocked")
	}
}

// makeMinimalHandlerWithAgentLoop builds a WSHandler wired to a real
// *agent.AgentLoop (unlike makeMinimalHandler in sprint_h_forwarder_test.go,
// which leaves agentLoop nil) so the orphan watchdog's
// IsSubTurnActiveForSpawnCall liveness re-check has a real answer to
// consult instead of short-circuiting via the nil guard.
func makeMinimalHandlerWithAgentLoop(al *agent.AgentLoop) *WSHandler {
	return &WSHandler{
		sessions:    make(map[string]*wsConn),
		sessionIDs:  make(map[string]string),
		taskChatIDs: make(map[string]string),
		agentLoop:   al,
	}
}

// TestOrphanWatchdog_GenuinelyActiveDelegate_NeverSynthesizesInterrupted is
// the regression test for the transient false "interrupted 0ms" flicker
// described in this file's header. It drives a REAL async Critical
// delegate, deliberately held open by orphanGateTool well past several
// multiples of a drastically shortened orphanWatchdogTimeout, and asserts
// no synthetic subagent_end{status:"interrupted"} frame is EVER emitted
// while the delegate is genuinely still running — only after the gate is
// released does the real subagent_end (status:"success") arrive.
func TestOrphanWatchdog_GenuinelyActiveDelegate_NeverSynthesizesInterrupted(t *testing.T) {
	// A short watchdog timeout so the test can observe several would-be
	// firing points without sleeping for the real 60s default. Deliberately
	// short enough that, without the liveness re-check fix, this test would
	// fail almost immediately.
	restore := SetOrphanWatchdogTimeoutForTest(150 * time.Millisecond)
	defer restore()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "content-routed-orphan-mock"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// An explicitly registered agent. Routing resolves to NO agent for an
			// empty roster now that the "main" sentinel is gone (ADR-064), and the
			// watchdog's turn dispatch then fails with "no agent available for route".
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	provider := &contentRoutedOrphanProvider{}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)

	unblock := make(chan struct{})
	gate := &orphanGateTool{unblock: unblock, entered: make(chan struct{})}
	al.RegisterTool(gate)

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(agent.NewSubTurnSpawner(al))
	// ADR-057 U14 fixture repair (38ba80eb): DelegateTool now refuses to
	// start a delegated session without a durable lifecycle store wired
	// (fail-closed rather than an untracked, unrecoverable session,
	// BDD-20/FR-021) — mirrors
	// pkg/agent/delegate_async_toolcall_completion_test.go's identical
	// wiring for the same contentRouted*Provider/gateTool pattern. Without
	// this, executeRun's fail-closed guard returns ErrorResult before ever
	// dispatching the async delegate, so the delegate never reaches
	// orphan_gate_tool and every wait on gate.entered below times out at
	// 15s — a fixture gap, not a watchdog defect.
	delegateTool.SetLifecycleStore(session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle")))
	delegateTool.SetDelegationDenyCheckerBackground(
		func(context.Context, string) *tools.DelegationDenial { return nil },
	)
	al.RegisterTool(delegateTool)
	// Drain the async delegate's own detached goroutine before t.TempDir()'s
	// cleanup runs (mirrors pkg/tools/delegate_adr053_test.go's
	// newADR053TestTool and every other DelegateTool test fixture in this
	// codebase). executeAsync's goroutine (pkg/tools/delegate.go) keeps
	// writing to the lifecycle store — t.transitionLifecycle's
	// LifecycleStore.Persist, an atomic write under tmpDir/session_lifecycle
	// — AFTER SpawnSubTurn returns, which is itself AFTER the real
	// EventKindSubTurnEnd event this test observes on `ch` was already
	// published. So "saw the real subagent_end frame" does NOT imply this
	// goroutine has finished writing: the goroutine can still be mid-Persist
	// when this test function returns. al.Close() (registered by
	// mustAgentLoop) cannot help — it has no reference to this DelegateTool
	// or its asyncWG, since delegateTool is a local test fixture, not part of
	// *agent.AgentLoop. Without this, the trailing Persist call's
	// create-temp-file-then-rename can race t.TempDir()'s RemoveAll walk,
	// intermittently failing with "TempDir RemoveAll cleanup: ...: directory
	// not empty" — exactly the class of flake documented on
	// DelegateTool.WaitForAsyncTasks's own doc comment. Registered here
	// (after delegateTool is fully wired, before TempDir()-dependent test
	// logic runs) so LIFO cleanup ordering runs this BEFORE al.Close() and
	// well before the TempDir() cleanup registered at this test's very start.
	t.Cleanup(delegateTool.WaitForAsyncTasks)

	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				"delegate":         "allow",
				"orphan_gate_tool": "allow",
			},
		})
	}

	const chatID = "chat-orphan-liveness"
	const sessionKey = "test-session-orphan-watchdog-liveness"

	h := makeMinimalHandlerWithAgentLoop(al)
	sub := al.SubscribeEvents(64)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	wc, ch := makeForwarderTestConn(256)
	fwdDone := make(chan struct{})
	go h.eventForwarder(wc, chatID, sub, fwdDone)

	ctx := context.Background()
	parentDone := make(chan struct{})
	var parentErr error
	go func() {
		defer close(parentDone)
		_, parentErr = al.ProcessDirectWithChannel(ctx, orphanParentRootTask, sessionKey, "telegram", chatID)
	}()

	select {
	case <-parentDone:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: parent turn never finished")
	}
	require.NoError(t, parentErr)

	select {
	case <-gate.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: delegate never reached its gated tool call")
	}

	// The parent has ended (arming the orphan watchdog for the still-open
	// span) and the delegate is confirmed parked inside its gated tool
	// call. Hold it here across several multiples of the shortened
	// watchdog timeout — every prior watchdog implementation would have
	// synthesized a false "interrupted" subagent_end by now.
	require.Never(t, func() bool {
		for _, raw := range drainAllFrames(ch) {
			if frameHasInterruptedSubagentEnd(t, raw) {
				return true
			}
		}
		return false
	}, 900*time.Millisecond, 50*time.Millisecond,
		"BUG REGRESSION: orphan watchdog must not synthesize status:\"interrupted\" for a span "+
			"whose real sub-turn is confirmed still active (IsSubTurnActiveForSpawnCall) — a background "+
			"Critical delegate now legitimately outlives its parent's turn-end by design (7dd9e7a5)")

	// Release the gate — the delegate proceeds to its real, synthesized
	// final answer and genuinely finishes.
	close(unblock)

	var sawRealCompletion bool
	deadline := time.After(15 * time.Second)
	for !sawRealCompletion {
		select {
		case raw := <-ch:
			if isSubagentEndFrame(t, raw) {
				status := subagentEndStatus(t, raw)
				require.NotEqual(t, "interrupted", status,
					"BUG REGRESSION: the real completion must never itself be reported as interrupted "+
						"for a delegation that was never canceled")
				assert.Equal(t, "success", status,
					"the real subagent_end must reflect the delegate's true terminal status")
				sawRealCompletion = true
			}
		case <-deadline:
			t.Fatal("BLOCKED: timed out waiting for the real subagent_end frame after releasing the gate")
		}
	}

	al.UnsubscribeEvents(sub.ID)
	<-fwdDone
}

// TestOrphanWatchdog_PermanentlyStuckDelegate_ForceFiresInterruptedPastCeiling
// is the regression test for the reschedule ceiling (7-reviewer-gate finding
// 3): startOrphanWatchdog's re-check-and-reschedule loop is correctly bounded
// for the NORMAL case by the sub-turn's own context timeout, but has no
// ceiling of its own — so a genuinely wedged/deadlocked turn (a goroutine
// that neither returns nor panics, e.g. blocked on a tool call not honoring
// context cancellation) would let IsSubTurnActiveForSpawnCall keep reporting
// "active" forever and the watchdog would reschedule indefinitely, never
// emitting a terminal frame for that span.
//
// This test deliberately NEVER releases orphanGateTool (unlike
// TestOrphanWatchdog_GenuinelyActiveDelegate_NeverSynthesizesInterrupted,
// which releases it and asserts the OPPOSITE — no synthetic frame — for a
// span that is only transiently still active) — modeling a permanently
// stuck sub-turn. With a short watchdog timeout and a short reschedule
// ceiling (both test-overridden), it proves the watchdog eventually gives up
// and force-emits status:"interrupted" regardless of what the liveness check
// still reports (fail-closed), and that the Debug->Warn log-level escalation
// and the final ceiling-exceeded Error log both fire along the way.
func TestOrphanWatchdog_PermanentlyStuckDelegate_ForceFiresInterruptedPastCeiling(t *testing.T) {
	restoreTimeout := SetOrphanWatchdogTimeoutForTest(50 * time.Millisecond)
	defer restoreTimeout()
	// Ceiling of 3: the loop logs "still active, rescheduling" for rechecks
	// 1-3 (escalating Debug->Warn once rechecks > 2, i.e. on recheck 3), then
	// on recheck 4 (4 > 3) gives up and force-fires — bounded at ~4 x 50ms,
	// nowhere near the real 15-reschedule/60s-timeout production defaults.
	restoreCeiling := SetOrphanWatchdogMaxRechecksForTest(3)
	defer restoreCeiling()

	var logBuf bytes.Buffer
	oldHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(slog.New(oldHandler))

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "content-routed-orphan-mock"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// An explicitly registered agent. Routing resolves to NO agent for an
			// empty roster now that the "main" sentinel is gone (ADR-064), and the
			// watchdog's turn dispatch then fails with "no agent available for route".
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	provider := &contentRoutedOrphanProvider{}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)

	// Deliberately never closed for the lifetime of this test — models a
	// permanently, not just transiently, wedged tool call/sub-turn.
	unblock := make(chan struct{})
	gate := &orphanGateTool{unblock: unblock, entered: make(chan struct{})}
	al.RegisterTool(gate)

	delegateTool := tools.NewDelegateTool(cfg.Agents.Defaults.DefaultModel.Model, cfg.Agents.Defaults.MaxTokens, 0)
	delegateTool.SetSpawner(agent.NewSubTurnSpawner(al))
	// ADR-057 U14 fixture repair (38ba80eb): DelegateTool now refuses to
	// start a delegated session without a durable lifecycle store wired
	// (fail-closed rather than an untracked, unrecoverable session,
	// BDD-20/FR-021) — mirrors
	// pkg/agent/delegate_async_toolcall_completion_test.go's identical
	// wiring for the same contentRouted*Provider/gateTool pattern. Without
	// this, executeRun's fail-closed guard returns ErrorResult before ever
	// dispatching the async delegate, so the delegate never reaches
	// orphan_gate_tool and every wait on gate.entered below times out at
	// 15s — a fixture gap, not a watchdog defect.
	delegateTool.SetLifecycleStore(session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle")))
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
				"delegate":         "allow",
				"orphan_gate_tool": "allow",
			},
		})
	}

	const chatID = "chat-orphan-ceiling"
	const sessionKey = "test-session-orphan-watchdog-ceiling"

	h := makeMinimalHandlerWithAgentLoop(al)
	sub := al.SubscribeEvents(64)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	wc, ch := makeForwarderTestConn(256)
	fwdDone := make(chan struct{})
	go h.eventForwarder(wc, chatID, sub, fwdDone)

	ctx := context.Background()
	parentDone := make(chan struct{})
	var parentErr error
	go func() {
		defer close(parentDone)
		_, parentErr = al.ProcessDirectWithChannel(ctx, orphanParentRootTask, sessionKey, "telegram", chatID)
	}()

	select {
	case <-parentDone:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: parent turn never finished")
	}
	require.NoError(t, parentErr)

	select {
	case <-gate.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: delegate never reached its gated tool call")
	}

	// The gate is never released within this test — the child sub-turn stays
	// genuinely, permanently active. Wait for the watchdog to eventually
	// force-fire the synthetic interrupted frame past the reschedule ceiling.
	var sawForcedInterrupted bool
	// #605/#606 hardening: nominal force-fire is ~200ms with this test's
	// 50ms/ceiling-3 overrides; 15s adds starvation margin for saturated CI
	// runners without weakening the assertion (a broken force-fire path never
	// emits the frame at any deadline).
	deadline := time.After(15 * time.Second)
	for !sawForcedInterrupted {
		select {
		case raw := <-ch:
			if frameHasInterruptedSubagentEnd(t, raw) {
				sawForcedInterrupted = true
			}
		case <-deadline:
			t.Fatal("BUG REGRESSION: orphan watchdog must eventually force-emit status:\"interrupted\" " +
				"for a span whose liveness check reports active past the reschedule ceiling — a " +
				"genuinely wedged sub-turn must never wedge the watchdog itself forever")
		}
	}

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "span_orphan_recheck_still_alive",
		"the reschedule loop must log its still-active rechecks")
	assert.Contains(t, logOutput, `"level":"WARN"`,
		"the reschedule loop must escalate from Debug to Warn once it has re-checked more than once or "+
			"twice, so a genuinely stuck span is discoverable without changing production log levels")
	assert.Contains(t, logOutput, "span_orphan_ceiling_exceeded",
		"exceeding the reschedule ceiling must be logged distinctly from a normal orphaned-span synthesis")

	al.UnsubscribeEvents(sub.ID)
	<-fwdDone
	// unblock is deliberately never closed — the gated goroutine remains
	// blocked for the rest of the test process; harmless at process exit and
	// consistent with modeling a permanently wedged tool call.
}

// drainAllFrames non-blockingly drains every currently-queued raw frame
// from ch.
func drainAllFrames(ch chan []byte) [][]byte {
	var out [][]byte
	for {
		select {
		case raw := <-ch:
			out = append(out, raw)
		default:
			return out
		}
	}
}

func frameHasInterruptedSubagentEnd(t *testing.T, raw []byte) bool {
	t.Helper()
	return isSubagentEndFrame(t, raw) && subagentEndStatus(t, raw) == "interrupted"
}

func isSubagentEndFrame(t *testing.T, raw []byte) bool {
	t.Helper()
	var f replayFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	return f.Type == "subagent_end"
}

func subagentEndStatus(t *testing.T, raw []byte) string {
	t.Helper()
	var f replayFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	return f.Status
}
