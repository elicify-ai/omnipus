// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 gate gaps — tests the #890 review gate asked for and the first
// pack did not pin. Oracles are ADR-093 (docs/internal/architecture/
// ADR-093-open-conversation-must-keep-delegation.md, test plan and D2–D6)
// and the founder decisions F890-1..F890-4, never the current implementation.
//
// Plan (expectations written before these tests were run):
//
//	Reply delivered     ADR-093 D4 routes a ClassOrdinaryRoot revival onto the
//	                    ordinary inbound path. That path's caller publishes the
//	                    turn's reply (the same publish the session worker uses
//	                    for every other human turn). The reply text is the
//	                    fixture below, not a value read off a previous run.
//	One turn           ADR-093 D4 / test plan: one human message, one turn,
//	                    one generation. A failed attempt is not a second turn.
//	                    The grace-window turn must not run beside the turn Stop
//	                    is still ending.
//	Shutdown drain     The revived turn is an ordinary turn, so shutdown must
//	                    cancel it. A turn started on a detached background
//	                    context outlives Stop.
//	Stop during revival ADR-093 test plan: "Stop lands after D4's revival, in
//	                    the same turn; delegate follows. Refusal — no child on
//	                    the generation a newer Stop covers."
//	Dispatch refusal   ADR-093 D5: delegate: dispatch: maps to the plain
//	                    sentence, same as delegate: launch:.
//	Task refusal       ADR-093 D5: startTaskNowViaLauncher's launch wrapper
//	                    maps a steering refusal to that same sentence.
//	Predicate halves   ADR-093 D4 / MIN-004: revive only when the channel is
//	                    not system AND the message carries no steer-wake
//	                    metadata. Each conjunct alone is enough to refuse.
//	Never auto-approve ADR-092's per-chat off switch still applies when D6
//	                    runs a task from that chat. F890-2 does not loosen it.
//	Read errors        A lifecycle read that is not "no such record" is logged
//	                    with the session id. A missing record stays quiet.
//
// Real stores. The only doubles are LLM providers and, for the two refusal
// races the production functions do not expose a sync point for, a launcher
// wrapper that reproduces the race outcome around the real launcher.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr093D5Sentence is ADR-093 D5's refusal, copied from the decision text.
// It is not read from steer.SteeringUnavailableMessage, so a drift in that
// constant fails these tests instead of moving the oracle with it.
const adr093D5Sentence = "Delegation is unavailable because this conversation is not active right now. Tell the user that sending a new message in this conversation resumes it, and that their request has not been started."

// adr093RevivedReply is the model reply the delivery test asks the provider
// to return. The oracle is that this exact text reaches the user.
const adr093RevivedReply = "The spreadsheet is ready."

func adr093UseProvider(t *testing.T, al *AgentLoop, provider providers.LLMProvider) {
	t.Helper()
	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	agentInst.Provider = provider
}

func adr093StoppedRoot(t *testing.T, al *AgentLoop, id string) {
	t.Helper()
	rec := adr093Record(id, 1, session.LifecycleRunning)
	rec.Stop = &session.Stop{At: time.Now(), Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman}}
	adr093Persist(t, al, rec)
}

// adr093FixedReplyProvider returns one fixed sentence and nothing else.
type adr093FixedReplyProvider struct{ reply string }

func (p adr093FixedReplyProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: p.reply}, nil
}

func (p adr093FixedReplyProvider) GetDefaultModel() string { return "adr093-fixed-reply" }

// adr093FailingProvider fails every model call and counts how many ran.
type adr093FailingProvider struct{ calls int }

func (p *adr093FailingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	return nil, errors.New("model call failed")
}

func (p *adr093FailingProvider) GetDefaultModel() string { return "adr093-failing" }

// adr093OverlapProvider counts how many turns are inside Chat at once.
type adr093OverlapProvider struct {
	mu      sync.Mutex
	current int
	max     int
	entries int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newAdr093OverlapProvider() *adr093OverlapProvider {
	return &adr093OverlapProvider{
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
}

func (p *adr093OverlapProvider) stop() { p.once.Do(func() { close(p.release) }) }

func (p *adr093OverlapProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.current++
	p.entries++
	if p.current > p.max {
		p.max = p.current
	}
	p.mu.Unlock()
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	p.mu.Lock()
	p.current--
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "finished"}, nil
}

func (p *adr093OverlapProvider) GetDefaultModel() string { return "adr093-overlap" }

func (p *adr093OverlapProvider) maxSeen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max
}

func (p *adr093OverlapProvider) entryCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entries
}

// adr093CancelWatchProvider reports whether the turn's own context was cancelled.
type adr093CancelWatchProvider struct {
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newAdr093CancelWatchProvider() *adr093CancelWatchProvider {
	return &adr093CancelWatchProvider{
		entered:   make(chan struct{}, 1),
		cancelled: make(chan struct{}),
		release:   make(chan struct{}),
	}
}

func (p *adr093CancelWatchProvider) stop() { p.once.Do(func() { close(p.release) }) }

func (p *adr093CancelWatchProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		close(p.cancelled)
		return nil, ctx.Err()
	case <-p.release:
		return &providers.LLMResponse{Content: "finished"}, nil
	}
}

func (p *adr093CancelWatchProvider) GetDefaultModel() string { return "adr093-cancel-watch" }

// adr093StopBeforeDispatch is the launch-then-Stop-then-dispatch race from
// ADR-093 D5, made deterministic: Launch is the real launcher, and Dispatch
// stamps a current Stop — what the cascade does between the two calls —
// before the real Dispatch runs.
type adr093StopBeforeDispatch struct {
	inner steer.SessionLauncher
	al    *AgentLoop
}

func (s adr093StopBeforeDispatch) Launch(ctx context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	return s.inner.Launch(ctx, req)
}

func (s adr093StopBeforeDispatch) Dispatch(ctx context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	rec, err := s.al.GetSessionLifecycleStore().Load(sessionID)
	if err != nil {
		return steer.DispatchResult{}, err
	}
	stamped := *rec
	stamped.Stop = &session.Stop{
		At:         time.Now(),
		Generation: rec.Generation,
		By:         session.Principal{Kind: session.PrincipalKindHuman},
	}
	if err := s.al.GetSessionLifecycleStore().Persist(&stamped); err != nil {
		return steer.DispatchResult{}, err
	}
	return s.inner.Dispatch(ctx, sessionID, gen)
}

// adr093RefuseLiveSteering returns D2's sentinel when the launch still names
// a steering session. That is the race ADR-093 D5 names for the task path:
// the creator was live at the gate and stopped before Launch.
type adr093RefuseLiveSteering struct{ inner steer.SessionLauncher }

func (s adr093RefuseLiveSteering) Launch(ctx context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	if strings.TrimSpace(req.SteeringSessionID) != "" {
		return steer.LaunchResult{}, steer.ErrSteeringStopped
	}
	return s.inner.Launch(ctx, req)
}

func (s adr093RefuseLiveSteering) Dispatch(ctx context.Context, sessionID string, gen int) (steer.DispatchResult, error) {
	return s.inner.Dispatch(ctx, sessionID, gen)
}

func adr093MakeLifecycleUnreadable(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	path := filepath.Join(al.GetSessionLifecycleStore().Dir(), sessionID+".jsonl")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod lifecycle file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	_, err := al.GetSessionLifecycleStore().Load(sessionID)
	if err == nil || errors.Is(err, session.ErrLifecycleNotFound) {
		t.Fatalf("setup: Load(%s) = %v, want a read error that is not not-found", sessionID, err)
	}
}

func adr093GlobalAutoOn(t *testing.T, al *AgentLoop) {
	t.Helper()
	cfg := al.GetConfig()
	if cfg == nil {
		t.Fatal("setup: agent loop has no config")
	}
	cfg.Sandbox.AutoApprove = true
	if al.agentAutoApproveDisabled(testDefaultAgentID) {
		t.Fatal("setup: the test agent has its own auto-approve off switch; this case is about the chat's switch")
	}
	if !al.SessionAutoApprove(testDefaultAgentID, "adr093-no-modifier") {
		t.Fatal("setup: global auto-approve is not on, so a chat's off switch cannot be told apart from the default")
	}
}

// TestAdr093RevivedRoot_ReplyIsDeliveredOnTheOutboundBus is ADR-093 D4 for
// the grace-window path: the revived ordinary-root turn's reply is delivered
// to the user on the ordinary publish path (channel, chat, and the reply
// text), not discarded.
func TestAdr093RevivedRoot_ReplyIsDeliveredOnTheOutboundBus(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093StoppedRoot(t, al, parentID)
	adr093UseProvider(t, al, adr093FixedReplyProvider{reply: adr093RevivedReply})

	msg := adr093HumanMessage("Right, carry on with the plan.", parentID)
	if err := al.enqueueSteeringFromMessage(msg); err != nil {
		t.Fatalf("enqueueSteeringFromMessage: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case out := <-al.bus.OutboundChan():
			if out.Channel == msg.Channel && out.ChatID == msg.ChatID && out.Content == adr093RevivedReply {
				return
			}
		case <-deadline:
			t.Fatalf("outbound bus never delivered the revived turn's reply %q on channel %q chat %q — ADR-093 D4: the ordinary inbound path publishes that reply to the user", adr093RevivedReply, msg.Channel, msg.ChatID)
		}
	}
}

// TestAdr093RevivedRoot_FailedTurnIsNotQueuedAgain is ADR-093 D4's "one
// message, one turn": when the revived ordinary turn fails, that message is
// not put back on the worker inbox for a second turn.
func TestAdr093RevivedRoot_FailedTurnIsNotQueuedAgain(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093StoppedRoot(t, al, parentID)
	provider := &adr093FailingProvider{}
	adr093UseProvider(t, al, provider)

	w := newSessionWorker("agent:"+testDefaultAgentID+":session:"+parentID, al, func() {})
	w.inTurn.Store(true)
	msg := adr093HumanMessage("Right, carry on with the plan.", parentID)
	_ = w.enqueue(msg)

	if provider.calls != 1 {
		t.Fatalf("revived message entered the model %d times, want 1 — ADR-093 D4: one message, one turn", provider.calls)
	}
	if queued := len(w.inbox); queued != 0 {
		t.Fatalf("worker inbox holds %d copy(ies) of the revived message — ADR-093 D4: a failed revived turn is not run again", queued)
	}
}

// TestAdr093RevivedRoot_DoesNotOverlapTheDyingTurn is the grace window: Stop
// has landed, the turn it ended is still unwinding, and the human's next
// message must not run a second turn beside it (ADR-093 D4, one turn).
func TestAdr093RevivedRoot_DoesNotOverlapTheDyingTurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(parentID, 1, session.LifecycleRunning))
	provider := newAdr093OverlapProvider()
	t.Cleanup(provider.stop)
	adr093UseProvider(t, al, provider)

	w := newSessionWorker("agent:"+testDefaultAgentID+":session:"+parentID, al, func() {})
	w.inTurn.Store(true)

	firstDone := make(chan struct{})
	go func() {
		_, _, _ = al.processMessage(context.Background(), adr093HumanMessage("still working", parentID))
		close(firstDone)
	}()
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the in-flight turn never reached the model")
	}

	stopped := adr093Load(t, al, parentID)
	stopped.Stop = &session.Stop{At: time.Now(), Generation: stopped.Generation, By: session.Principal{Kind: session.PrincipalKindHuman}}
	adr093Persist(t, al, stopped)

	go func() {
		_ = w.enqueue(adr093HumanMessage("Right, carry on with the plan.", parentID))
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if provider.maxSeen() >= 2 {
			t.Fatalf("the revived turn entered the model while the turn Stop is ending was still inside (max %d) — ADR-093 D4: one message runs one turn, not beside the dying turn", provider.maxSeen())
		}
		time.Sleep(20 * time.Millisecond)
	}
	provider.stop()
	select {
	case <-firstDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the in-flight turn did not finish after release")
	}
	waitUntil := time.Now().Add(15 * time.Second)
	for provider.entryCount() < 2 && time.Now().Before(waitUntil) {
		time.Sleep(20 * time.Millisecond)
	}
	if provider.maxSeen() >= 2 {
		t.Fatalf("two turns were inside the model at once (max %d) — ADR-093 D4: the revived root runs one turn", provider.maxSeen())
	}
	if provider.entryCount() < 2 {
		t.Fatalf("the revived message never ran (model entries %d, want 2) — ADR-093 D4: the message is routed to the ordinary path, not dropped", provider.entryCount())
	}
}

// TestAdr093RevivedRoot_ShutdownCancelsTheTurn is the shutdown half of the
// same rule: a revived ordinary-root turn is cancelled when the loop shuts
// down, the way every other in-flight turn is.
func TestAdr093RevivedRoot_ShutdownCancelsTheTurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093StoppedRoot(t, al, parentID)
	provider := newAdr093CancelWatchProvider()
	t.Cleanup(provider.stop)
	adr093UseProvider(t, al, provider)

	errCh := make(chan error, 1)
	go func() {
		errCh <- al.enqueueSteeringFromMessage(adr093HumanMessage("Right, carry on with the plan.", parentID))
	}()
	select {
	case <-provider.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the revived turn never reached the model")
	}

	// Gateway shutdown stops new work, then drains and closes. Stop plus the
	// worker drain is the cancellation half; WaitForActiveRequests only waits.
	al.Stop()
	al.stopSessionWorkers()

	select {
	case <-provider.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the revived turn was still running 2s after shutdown — ADR-093 D4: that turn is an ordinary turn and must be cancelled with the loop, not left on a detached context")
	}
	provider.stop()
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("the revived turn did not return after shutdown")
	}
}

// TestAdr093StopDuringRevivedTurn_DelegateRefuses drives a real Stop while
// the revived turn is still inside the model, then delegates. ADR-093's test
// plan: no child on the generation that newer Stop covers.
func TestAdr093StopDuringRevivedTurn_DelegateRefuses(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093StoppedRoot(t, al, parentID)
	provider, release := installParkedProvider(t, al)
	defer release()

	errCh := make(chan error, 1)
	go func() {
		errCh <- al.enqueueSteeringFromMessage(adr093HumanMessage("Right, carry on with the plan.", parentID))
	}()
	adr093WaitForEntered(t, provider, 30*time.Second)

	revived := adr093Load(t, al, parentID)
	if revived.Generation != 2 {
		t.Fatalf("generation after revival = %d, want 2 before Stop lands (ADR-093 D4)", revived.Generation)
	}

	if _, err := al.steerCanceller().CancelSubtree(context.Background(), parentID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "user-adr093",
	}); err != nil {
		t.Fatalf("CancelSubtree while the revived turn is in flight: %v", err)
	}
	release()
	select {
	case <-errCh:
	case <-time.After(15 * time.Second):
		t.Fatal("the revived turn did not return after Stop")
	}

	_, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "Follow up on the draft",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	if err == nil || !steer.IsSteeringUnavailable(err) {
		t.Fatalf("Launch after Stop during the revived turn = %v, want the steering refusal — ADR-093 test plan: no child on the generation a newer Stop covers", err)
	}
	records, listErr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if listErr != nil {
		t.Fatalf("List lifecycle: %v", listErr)
	}
	if len(records) != 1 || records[0].SessionID != parentID {
		ids := make([]string, 0, len(records))
		for _, rec := range records {
			ids = append(ids, rec.SessionID)
		}
		t.Fatalf("lifecycle records after Stop-during-revival = %v, want only the parent %s (no child on the stopped generation)", ids, parentID)
	}
	parent := adr093Load(t, al, parentID)
	if parent.Generation != 2 {
		t.Fatalf("parent generation = %d, want 2 (the revival's generation, which the newer Stop covers)", parent.Generation)
	}
	if !parent.Stopped() && !parent.Terminal() {
		t.Fatalf("parent after the concurrent Stop is generation %d state %q with no current Stop — ADR-093: the newer Stop covers that generation", parent.Generation, parent.State)
	}
}

// TestDelegateRun_DispatchRefusal_SaysHowToResume is ADR-093 D5's dispatch
// half: Launch succeeded, then Stop made Dispatch refuse. The model reads
// the plain sentence, never "delegate: dispatch:" machinery.
func TestDelegateRun_DispatchRefusal_SaysHowToResume(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(parentID, 1, session.LifecycleRunning))

	tool := adr093DelegateTool(t, al)
	tool.SetSessionLauncher(adr093StopBeforeDispatch{inner: NewSteerLauncher(al), al: al})
	result := tool.Execute(tools.WithToolCallID(
		tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), parentID), testDefaultAgentID),
		"call-adr093-dispatch-refusal",
	), map[string]any{
		"action":   "run",
		"agent_id": testDefaultAgentID,
		"task":     "Prepare the spreadsheet",
	})
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}
	if !result.IsError {
		t.Fatalf("delegate whose Dispatch was refused must be an error (ADR-093 D5), got success:\n%s", result.ForLLM)
	}
	assertIssue890RefusalText(t, "ForLLM", result.ForLLM, parentID)
	if result.ForLLM != adr093D5Sentence {
		t.Fatalf("dispatch refusal ForLLM =\n%s\nwant ADR-093 D5's sentence\n%s", result.ForLLM, adr093D5Sentence)
	}
}

// TestAdr093TaskLaunch_SteeringRefusalUsesThePlainSentence is ADR-093 D5 for
// the task executor: a launch refused because the creator stopped between
// the gate and Launch is the plain sentence, not "task_executor: StartTaskNow: launch:".
func TestAdr093TaskLaunch_SteeringRefusalUsesThePlainSentence(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(creatorID, 1, session.LifecycleRunning))

	te := adr093TaskExecutor(t, al)
	te.launcher = adr093RefuseLiveSteering{inner: te.launcher}
	tk := adr093CreatorTask("Live then stopped", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	_, err := te.startTaskNowViaLauncher(context.Background(), tk)
	if err == nil {
		t.Fatal("startTaskNowViaLauncher succeeded — ADR-093 D5: a steering refusal is returned to the caller")
	}
	if err.Error() != adr093D5Sentence {
		t.Fatalf("task launch refusal =\n%s\nwant ADR-093 D5's sentence\n%s", err.Error(), adr093D5Sentence)
	}
	if strings.Contains(err.Error(), "task_executor:") || strings.Contains(err.Error(), "steer:") {
		t.Fatalf("task launch refusal still quotes machinery: %s", err.Error())
	}
}

// TestAdr093RevivePredicate_Halves is MIN-004's predicate, both conjuncts:
// channel system refuses, and steer-wake metadata refuses even on a human
// channel. Either conjunct false means no revival.
func TestAdr093RevivePredicate_Halves(t *testing.T) {
	cases := []struct {
		name    string
		msg     bus.InboundMessage
		revives bool
	}{
		{name: "webchat without steer metadata", msg: bus.InboundMessage{Channel: "webchat"}, revives: true},
		{name: "webchat with steer-wake metadata", msg: bus.InboundMessage{Channel: "webchat", Metadata: map[string]string{"steer_message_id": "wake-1"}}, revives: false},
		{name: "system channel", msg: bus.InboundMessage{Channel: "system"}, revives: false},
		{name: "system channel and steer metadata", msg: bus.InboundMessage{Channel: "system", Metadata: map[string]string{"steer_message_id": "wake-1"}}, revives: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviveInboundIsHumanTurn(tc.msg); got != tc.revives {
				t.Fatalf("reviveInboundIsHumanTurn(%+v) = %v, want %v — ADR-093 MIN-004: revive only when the channel is not system and the message has no steer-wake metadata", tc.msg, got, tc.revives)
			}
		})
	}
}

// TestAdr093RevivePredicate_SteerWakeOnWebchatDoesNotRevive is the metadata
// half on the real admission path, not only the predicate: a webchat message
// that carries steer-wake metadata must leave a terminal record unrevived.
func TestAdr093RevivePredicate_SteerWakeOnWebchatDoesNotRevive(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	parent := adr093Record(parentID, 1, session.LifecycleFailed)
	parent.FailedReason = failedReasonInterrupted
	adr093Persist(t, al, parent)

	msg := adr093HumanMessage("this is a steer wake, not a person", parentID)
	msg.Metadata = map[string]string{"steer_message_id": "wake-1"}
	if _, _, err := al.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
	got := adr093Load(t, al, parentID)
	if got.Generation != 1 || got.State != session.LifecycleFailed || got.ResumedFrom != "" {
		t.Fatalf("record after a webchat steer-wake = gen %d state %q resumed_from %q — ADR-093 MIN-004: steer-wake metadata never revives, even when the channel is not system", got.Generation, got.State, got.ResumedFrom)
	}
}

// TestAdr093TaskFromStoppedChat_InheritsNeverAutoApprove is ADR-092's per-chat
// off switch on ADR-093 D6's path: global auto-approve is on, the chat turned
// it off, the chat is stopped, and the task started from it must still be off.
func TestAdr093TaskFromStoppedChat_InheritsNeverAutoApprove(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	adr093GlobalAutoOn(t, al)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)
	al.SessionModes().Set(creatorID, false)
	adr093StoppedRoot(t, al, creatorID)

	te := adr093TaskExecutor(t, al)
	tk := adr093CreatorTask("Stopped chat task", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	childID, err := te.startTaskNowViaLauncher(context.Background(), tk)
	if err != nil {
		t.Fatalf("startTaskNowViaLauncher: %v — ADR-093 D6: the task still runs", err)
	}
	adr093AssertOrdinaryTaskRoot(t, al, childID, tk.ID)
	if al.SessionAutoApprove(testDefaultAgentID, childID) {
		t.Fatalf("task session %s has auto-approve on — the stopped chat had turned it off, and ADR-092's per-chat off switch still applies (D6 does not drop it)", childID)
	}
}

// TestAdr093TaskFromLiveChat_InheritsNeverAutoApprove is the control: a task
// from a live chat with the same off switch inherits it. If this fails, the
// stopped-chat test is not measuring inheritance.
func TestAdr093TaskFromLiveChat_InheritsNeverAutoApprove(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	adr093GlobalAutoOn(t, al)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)
	al.SessionModes().Set(creatorID, false)
	adr093Persist(t, al, adr093Record(creatorID, 1, session.LifecycleRunning))

	te := adr093TaskExecutor(t, al)
	tk := adr093CreatorTask("Live chat task", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	childID, err := te.startTaskNowViaLauncher(context.Background(), tk)
	if err != nil {
		t.Fatalf("startTaskNowViaLauncher: %v", err)
	}
	if al.SessionAutoApprove(testDefaultAgentID, childID) {
		t.Fatalf("task session %s has auto-approve on — a live chat's off switch must be inherited (control for the stopped-chat case)", childID)
	}
}

// TestAdr093Revival_ReadErrorIsLogged: a lifecycle read that fails for a
// reason other than "no record" is logged with the session id. Not-found is
// the quiet case; permission denied is not.
func TestAdr093Revival_ReadErrorIsLogged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	parent := adr093Record(parentID, 1, session.LifecycleFailed)
	parent.FailedReason = failedReasonInterrupted
	adr093Persist(t, al, parent)
	adr093MakeLifecycleUnreadable(t, al, parentID)

	readLog := captureLogFile(t, logger.ERROR)
	_ = al.inboundRevivable(parentID)
	log := readLog()
	if !strings.Contains(log, parentID) || !strings.Contains(strings.ToLower(log), "permission denied") {
		t.Fatalf("revival read error was not logged at error level with the session id and the read error.\nlog:\n%s\nwant both %q and \"permission denied\" — a failed read must not look like a record that needs no revival", log, parentID)
	}
}

// TestAdr093Revival_MissingRecordIsNotAnErrorLog is the other half: no
// lifecycle file is "nothing to revive", and that is not an error log.
func TestAdr093Revival_MissingRecordIsNotAnErrorLog(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	readLog := captureLogFile(t, logger.ERROR)
	if al.inboundRevivable(parentID) {
		t.Fatal("a session with no lifecycle record is not revivable")
	}
	if log := readLog(); strings.Contains(log, parentID) {
		t.Fatalf("a missing lifecycle record was logged at error level:\n%s\nnot-found is not a read failure", log)
	}
}

// TestAdr093Task_CreatorReadErrorIsLogged is the same rule on the task path:
// the creator record could not be read, and that is logged, not dropped.
func TestAdr093Task_CreatorReadErrorIsLogged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)
	adr093StoppedRoot(t, al, creatorID)
	adr093MakeLifecycleUnreadable(t, al, creatorID)

	te := adr093TaskExecutor(t, al)
	tk := adr093CreatorTask("Unreadable creator", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	readLog := captureLogFile(t, logger.ERROR)
	_, err := te.startTaskNowViaLauncher(context.Background(), tk)
	log := readLog()
	if err == nil {
		t.Fatal("startTaskNowViaLauncher succeeded despite an unreadable creator record")
	}
	if !strings.Contains(log, creatorID) || !strings.Contains(strings.ToLower(log), "permission denied") {
		t.Fatalf("creator-record read error was not logged at error level.\nerr: %v\nlog:\n%s\nwant both %q and \"permission denied\"", err, log, creatorID)
	}
}
