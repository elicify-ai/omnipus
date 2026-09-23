// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix-lane 7 (Gap 2): BoundaryAsyncToolFeedback, BoundaryMedia,
// BoundaryRetryNotice and BoundaryTaskResultNotification were each
// exercised only by a pure enum test (pkg/steer's
// TestRecordingObserver_ObservesEveryBoundary) that constructs its own
// recorder and proves nothing about the product. This file drives each of
// the four through its REAL production call site against a real AgentLoop —
// never a mock of the boundary decision itself — with a positive control
// alongside every containment assertion, so a test cannot pass vacuously
// (a broken gate that always contains everything, or one that always
// publishes everything, must fail one half of each pair).
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- shared harness -------------------------------------------------------

// newBoundaryHarness builds a real AgentLoop wired exactly like production
// boot wires it for ADR-091 steering (SetSteerAudienceDeps with the real
// classifier/resolver, a real SteerLauncher), plus a dedicated
// OutboundRecorder as the BoundaryObserver so AssertBoundaryInvoked reflects
// only real audienceFor calls, never a synthetic one.
func newBoundaryHarness(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *SteerLauncher, *testutil.OutboundRecorder, *bus.MessageBus) {
	t.Helper()
	al, cleanup := newSteerALWithProvider(t, provider)
	t.Cleanup(cleanup)

	lifecycle := al.GetSessionLifecycleStore()
	classifier := NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	resolver := NewSteerAudienceResolver(classifier)
	recorder := testutil.RecordingOutbound(t)
	al.SetSteerAudienceDeps(resolver, recorder, NewSteerUpwardDeliverer())
	launcher := NewSteerLauncher(al)
	al.SetSteerSessionLauncher(launcher)

	return al, launcher, recorder, al.bus
}

// newSteerALWithProvider mirrors newSteerAL (steer_launcher_test.go) but
// takes a caller-supplied provider instead of the fixed &mockProvider{}, so
// a boundary test can script the exact tool call / retry / text sequence it
// needs to reach its target production code path.
// newSteerALWithProvider lives in steer_completion_test.go (fix lane 1); this
// lane's duplicate was removed when the two lanes merged.

// allowProbeTool registers tool on al and grants every currently-registered
// agent an explicit "allow" policy for it (test tools are unlisted, so the
// default catalog ceiling would otherwise ask/deny).
func allowProbeTool(t *testing.T, al *AgentLoop, tool tools.Tool) {
	t.Helper()
	al.RegisterTool(tool)
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		if instance, ok := al.GetRegistry().GetAgent(agentID); ok {
			instance.StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{tool.Name(): config.ToolPolicyAllow},
			})
		}
	}
}

// launchAndAwaitSteeredChild launches and dispatches a steered child of root, and
// waits for it to reach a terminal lifecycle state, returning its session id.
func launchAndAwaitSteeredChild(t *testing.T, al *AgentLoop, l *SteerLauncher, root, label string) string {
	t.Helper()
	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: root, TargetAgentID: testDefaultAgentID, Task: label,
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-" + label},
	})
	if err != nil {
		t.Fatalf("Launch(steered child %q): %v", label, err)
	}
	if _, err := l.Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("Dispatch(steered child %q): %v", label, err)
	}
	lifecycle := al.GetSessionLifecycleStore()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := lifecycle.Load(res.SessionID)
		if err == nil && rec.Terminal() {
			return res.SessionID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("steered child %q did not reach a terminal lifecycle state within the deadline", label)
	return ""
}

// runOrdinaryRootTurn drives a real, non-steered chat turn — the positive
// control proving a containment failure isn't vacuous — against a REAL,
// store-backed session (created the same way newTestSteeringSession does)
// via al.ProcessScheduled, which explicitly threads TranscriptSessionID
// through to the turn (loop.go's own comment: "passes the concrete
// sessionID as TranscriptSessionID so the turn registers under it").
// ProcessDirectWithChannel was tried first and does NOT do this for an
// unbound "cron"-sender message — the default-agent-fallback routing path
// it takes leaves opts.TranscriptSessionID empty, which the classifier
// cannot positively resolve, so audienceFor answered AudienceNone (an
// unreadable session, not an ordinary root) and every boundary stayed
// silently contained — making the earlier version of this control
// vacuous. A REAL session id run through ProcessScheduled classifies as
// ClassOrdinaryRoot (I-8 row 1: no lifecycle record, meta agrees it's a
// root) and resolves AudienceUser, as intended.
func runOrdinaryRootTurn(t *testing.T, al *AgentLoop, chatID, content string) {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatalf("create ordinary root session: %v", err)
	}
	if _, err := al.ProcessScheduled(context.Background(), testDefaultAgentID, meta.ID, content, "webchat", chatID); err != nil {
		t.Fatalf("ordinary root turn: %v", err)
	}
}

func drainOutboundText(msgBus *bus.MessageBus) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for {
		select {
		case m := <-msgBus.OutboundChan():
			out = append(out, m)
		default:
			return out
		}
	}
}

func drainOutboundMedia(msgBus *bus.MessageBus) []bus.OutboundMediaMessage {
	var out []bus.OutboundMediaMessage
	for {
		select {
		case m := <-msgBus.OutboundMediaChan():
			out = append(out, m)
		default:
			return out
		}
	}
}

// --- BoundaryMedia ----------------------------------------------------------

const boundaryMediaProbeToolName = "adr091_boundary_media_probe"

type boundaryMediaProbe struct{ tools.BaseTool }

func (*boundaryMediaProbe) Name() string        { return boundaryMediaProbeToolName }
func (*boundaryMediaProbe) Description() string { return "test-only tool that returns media" }
func (*boundaryMediaProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*boundaryMediaProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*boundaryMediaProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "media probe done", Media: []string{"adr091-media-ref.png"}}
}

// TestSteeredChild_ToolMedia_IsPersistedButNeverPublished proves boundary 4
// (loop_run_turn_tools.go's "this boundary was UNGATED before ADR-091" media
// gate): a steered child's tool media is never sent to a channel or
// published to the outbound media bus.
func TestSteeredChild_ToolMedia_IsPersistedButNeverPublished(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall(boundaryMediaProbeToolName, `{}`).WithText("done")
	al, launcher, recorder, msgBus := newBoundaryHarness(t, provider)
	allowProbeTool(t, al, &boundaryMediaProbe{})

	root := newTestSteeringSession(t, al, "")
	launchAndAwaitSteeredChild(t, al, launcher, root, "media-child")

	recorder.AssertBoundaryInvoked(steer.BoundaryMedia)
	media := drainOutboundMedia(msgBus)
	if len(media) != 0 {
		t.Fatalf("steered child's tool media reached the outbound media bus: %+v — media boundary containment is broken", media)
	}
}

// TestOrdinaryRoot_ToolMedia_StillPublished is the positive control: the
// SAME probe tool, run by an ordinary (non-steered) session, MUST still
// reach the outbound media bus — proving the assertion above is contained,
// not merely a dead code path nothing ever reaches.
func TestOrdinaryRoot_ToolMedia_StillPublished(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall(boundaryMediaProbeToolName, `{}`).WithText("done")
	al, _, recorder, msgBus := newBoundaryHarness(t, provider)
	allowProbeTool(t, al, &boundaryMediaProbe{})

	runOrdinaryRootTurn(t, al, "media-control-chat", "please attach the file")

	recorder.AssertBoundaryInvoked(steer.BoundaryMedia)
	media := drainOutboundMedia(msgBus)
	if len(media) == 0 {
		t.Fatal("an ordinary root's tool media never reached the outbound media bus — the control has no teeth")
	}
}

// --- BoundaryAsyncToolFeedback ----------------------------------------------

const boundaryAsyncFeedbackProbeToolName = "adr091_boundary_async_feedback_probe"
const boundaryAsyncFeedbackMarker = "ADR091-ASYNC-FEEDBACK-MUST-BE-CONTAINED"

type boundaryAsyncFeedbackProbe struct{ tools.BaseTool }

func (*boundaryAsyncFeedbackProbe) Name() string { return boundaryAsyncFeedbackProbeToolName }
func (*boundaryAsyncFeedbackProbe) Description() string {
	return "test-only async tool that reports user-facing output through its callback"
}
func (*boundaryAsyncFeedbackProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*boundaryAsyncFeedbackProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*boundaryAsyncFeedbackProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("async probe must execute through ExecuteAsync")
}
func (*boundaryAsyncFeedbackProbe) ExecuteAsync(
	ctx context.Context, _ map[string]any, callback tools.AsyncCallback,
) *tools.ToolResult {
	// Fires synchronously, inside the SAME turn iteration — exactly the
	// shape loop_run_turn_tools.go wires ex.asyncCallback to (the gate that
	// calls handleAsyncResult, which is the ONLY call site of
	// BoundaryAsyncToolFeedback).
	callback(ctx, &tools.ToolResult{ForUser: boundaryAsyncFeedbackMarker})
	return &tools.ToolResult{ForLLM: "async probe started", Async: true}
}

// TestSteeredChild_AsyncToolError_DoesNotReachTheUser proves boundary 2: an
// async tool call's user-facing feedback, for a steered child, never
// publishes to the outbound text bus.
func TestSteeredChild_AsyncToolError_DoesNotReachTheUser(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall(boundaryAsyncFeedbackProbeToolName, `{}`).WithText("done")
	al, launcher, recorder, msgBus := newBoundaryHarness(t, provider)
	allowProbeTool(t, al, &boundaryAsyncFeedbackProbe{})

	root := newTestSteeringSession(t, al, "")
	launchAndAwaitSteeredChild(t, al, launcher, root, "async-child")

	recorder.AssertBoundaryInvoked(steer.BoundaryAsyncToolFeedback)
	for _, m := range drainOutboundText(msgBus) {
		if strings.Contains(m.Content, boundaryAsyncFeedbackMarker) {
			t.Fatalf("steered child's async tool feedback reached the outbound text bus: %+v", m)
		}
	}
}

// TestOrdinaryRoot_AsyncToolFeedback_StillPublished is the positive control.
func TestOrdinaryRoot_AsyncToolFeedback_StillPublished(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall(boundaryAsyncFeedbackProbeToolName, `{}`).WithText("done")
	al, _, recorder, msgBus := newBoundaryHarness(t, provider)
	allowProbeTool(t, al, &boundaryAsyncFeedbackProbe{})

	runOrdinaryRootTurn(t, al, "async-control-chat", "run the async probe")

	recorder.AssertBoundaryInvoked(steer.BoundaryAsyncToolFeedback)
	found := false
	for _, m := range drainOutboundText(msgBus) {
		if strings.Contains(m.Content, boundaryAsyncFeedbackMarker) {
			found = true
		}
	}
	if !found {
		t.Fatal("an ordinary root's async tool feedback never reached the outbound text bus — the control has no teeth")
	}
}

// --- BoundaryRetryNotice -----------------------------------------------------

// alternatingTimeoutProvider returns a transient-timeout error on every
// even-numbered call and a successful reply on every odd-numbered call —
// driving runTurn's real timeout-retry path (isTransientStreamError ->
// isTimeoutError -> the backoff branch that calls audienceFor with
// BoundaryRetryNotice) deterministically, without a real network.
type alternatingTimeoutProvider struct {
	callIdx int
}

func (p *alternatingTimeoutProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	i := p.callIdx
	p.callIdx++
	if i%2 == 0 {
		return nil, fmt.Errorf("streaming read error: http2: response body closed")
	}
	return &providers.LLMResponse{Content: "turn completed after retry", ToolCalls: []providers.ToolCall{}}, nil
}

func (*alternatingTimeoutProvider) GetDefaultModel() string { return "test-model" }

const retryNoticeMarker = "Retrying — please wait..."

// TestSteeredChild_ProviderRetryNotice_IsContained proves boundary 5: a
// steered child's LLM-timeout retry notice never reaches the user.
func TestSteeredChild_ProviderRetryNotice_IsContained(t *testing.T) {
	al, launcher, recorder, msgBus := newBoundaryHarness(t, &alternatingTimeoutProvider{})
	root := newTestSteeringSession(t, al, "")
	launchAndAwaitSteeredChild(t, al, launcher, root, "retry-child")

	recorder.AssertBoundaryInvoked(steer.BoundaryRetryNotice)
	for _, m := range drainOutboundText(msgBus) {
		if strings.Contains(m.Content, retryNoticeMarker) {
			t.Fatalf("steered child's retry notice reached the outbound text bus: %+v", m)
		}
	}
}

// TestOrdinaryRoot_ProviderRetryNotice_StillPublished is the positive
// control: the SAME alternating-timeout provider, driven for an ordinary
// root, MUST publish the retry notice.
func TestOrdinaryRoot_ProviderRetryNotice_StillPublished(t *testing.T) {
	al, _, recorder, msgBus := newBoundaryHarness(t, &alternatingTimeoutProvider{})
	runOrdinaryRootTurn(t, al, "retry-control-chat", "hello")

	recorder.AssertBoundaryInvoked(steer.BoundaryRetryNotice)
	found := false
	for _, m := range drainOutboundText(msgBus) {
		if strings.Contains(m.Content, retryNoticeMarker) {
			found = true
		}
	}
	if !found {
		t.Fatal("an ordinary root's retry notice never reached the outbound text bus — the control has no teeth")
	}
}

// --- BoundaryTaskResultNotification -----------------------------------------

// TestSteeredChild_TaskResultNotification_IsContained proves boundary 9
// (task_executor.go::notifySourceChannel): a steered task session's
// completion is never announced on its origin channel — its steering
// session hears about it through the upward handback instead. Calls the
// REAL production method directly (the exact function landing-order §6
// names) against a hand-built lifecycle record, isolating the assertion to
// the boundary gate itself rather than the whole task-dispatch machinery.
func TestSteeredChild_TaskResultNotification_IsContained(t *testing.T) {
	al, _, recorder, msgBus := newBoundaryHarness(t, testutil.NewScenario())
	lifecycle := al.GetSessionLifecycleStore()

	root := newTestSteeringSession(t, al, "")
	childID := "adr091-task-result-steered-child"
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: childID, Generation: 1, State: session.LifecycleCompleted,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: root,
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: root, RootSessionID: root,
			ReportingTarget: session.ReportingTarget{SessionID: root},
			Authorization:   session.Authorization{Mode: session.AuthorizationModeDirect, RemainingDepth: 3},
		},
	}); err != nil {
		t.Fatalf("persist steered task child record: %v", err)
	}

	te := &TaskExecutor{agentLoop: al}
	te.notifySourceChannel(&task.Task{
		ID: "task-1", Title: "steered task", Status: task.StatusDone, Result: "the answer",
		SessionID: childID, SourceChannel: "webchat", SourceChatID: "chat-task-result",
	})

	recorder.AssertBoundaryInvoked(steer.BoundaryTaskResultNotification)
	msgs := drainOutboundText(msgBus)
	if len(msgs) != 0 {
		t.Fatalf("a steered task's completion reached its source channel: %+v — task-result-notification containment is broken", msgs)
	}
}

// TestOrdinaryRoot_TaskResultNotification_StillPublished is the positive
// control: an ordinary_root task session's completion MUST still reach its
// source channel.
func TestOrdinaryRoot_TaskResultNotification_StillPublished(t *testing.T) {
	al, _, recorder, msgBus := newBoundaryHarness(t, testutil.NewScenario())
	lifecycle := al.GetSessionLifecycleStore()

	childID := "adr091-task-result-ordinary-root"
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: childID, Generation: 1, State: session.LifecycleCompleted,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-2"},
		OwnerScopeKind: session.OwnerScopeHuman,
	}); err != nil {
		t.Fatalf("persist ordinary_root task record: %v", err)
	}

	te := &TaskExecutor{agentLoop: al}
	te.notifySourceChannel(&task.Task{
		ID: "task-2", Title: "ordinary task", Status: task.StatusDone, Result: "the answer",
		SessionID: childID, SourceChannel: "webchat", SourceChatID: "chat-task-result-control",
	})

	recorder.AssertBoundaryInvoked(steer.BoundaryTaskResultNotification)
	msgs := drainOutboundText(msgBus)
	if len(msgs) == 0 {
		t.Fatal("an ordinary root task's completion never reached its source channel — the control has no teeth")
	}
}
